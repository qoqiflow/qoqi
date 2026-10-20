package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A step of a queue has to be safe to run twice under its key: the daemon
// sends a step again whenever it may not have arrived. A provider that
// takes an idempotency key makes it so, the key given to it. For the
// rest, a Keeper keeps the answers of steps in the application's own
// database, keyed by the step's key, of two kinds:
//
//   - once, for an effect in that database: the effect and the answer are
//     written in one transaction, which the step reaches with TxOf. A
//     step sent again gives the answer kept, without acting;
//   - started, for an effect elsewhere: a claim is written before it
//     acts, alive until the attempt's deadline, and the answer once it is
//     done. A claim found without its answer past its deadline is a
//     process that died as it acted: the step answers that it cannot say
//     how it went, and a person says what happened.
//
// The daemon knows which shapes are kept which way from the queue's
// manifest: a step that returns a Settled is sent again under once,
// asked of (probed) under started, and Unknown otherwise.
type Keeper struct {
	DB *sql.DB
	// Table holds the answers: flow_steps when not said
	Table string
	// Numbered says the database writes its parameters $1, $2, as
	// Postgres does; otherwise ?, as SQLite and MySQL do
	Numbered bool
	// MySQL says the database quotes its names with backticks, as MySQL
	// and MariaDB do; key is a reserved word there. It need not be said
	// of a database opened with a MySQL driver, which is told by its type
	MySQL bool
}

// What is under a key.
const (
	claimed  = "started"
	answered = "answered"
)

func (k *Keeper) table() string {
	if k.Table == "" {
		return "flow_steps"
	}
	return k.Table
}

// mysql says whether the database quotes its names with backticks: as
// the Keeper says, or as its driver's type tells, such as go-sql-driver's
// *mysql.MySQLDriver.
func (k *Keeper) mysql() bool {
	if k.MySQL {
		return true
	}
	return k.DB != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", k.DB.Driver())), "mysql")
}

// q writes a query's names and parameters as the database does. The
// queries quote the key column with double quotes, as SQLite and Postgres
// do; MySQL, where key is a reserved word, quotes it with backticks.
func (k *Keeper) q(query string) string {
	query = strings.ReplaceAll(query, "flow_steps", k.table())
	if k.mysql() {
		query = strings.ReplaceAll(query, `"key"`, "`key`")
	}
	if !k.Numbered {
		return query
	}
	var out strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&out, "$%d", n)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Setup makes the table of answers, if it is not there.
func (k *Keeper) Setup(ctx context.Context) error {
	_, err := k.DB.ExecContext(ctx, k.q(`CREATE TABLE IF NOT EXISTS flow_steps (
		"key"  VARCHAR(255) PRIMARY KEY,
		state  VARCHAR(16) NOT NULL,
		answer TEXT NOT NULL,
		beat   BIGINT NOT NULL
	)`))
	return err
}

// row is what is under a key: for a claim, beat is the deadline of the
// attempt that holds it.
type row struct {
	state  string
	answer string
	beat   int64
}

func (k *Keeper) read(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (*row, error) {
	var r row
	err := q.QueryRowContext(ctx, k.q(`SELECT state, answer, beat FROM flow_steps WHERE "key" = ?`), key).Scan(&r.state, &r.answer, &r.beat)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// alive says whether a claim's attempt may still act: until its
// deadline, past which the daemon gave it up.
func (k *Keeper) alive(r *row) bool {
	return time.Now().UnixMilli() < r.beat
}

type txKey struct{}

// TxOf is the transaction a step kept under once writes its effect in:
// committed with its answer, or rolled back with neither.
func TxOf(ctx context.Context) *sql.Tx {
	tx, _ := ctx.Value(txKey{}).(*sql.Tx)
	return tx
}

// kept is the answer a step gave before, as the daemon reads it.
func kept(r *row) Answer {
	var a Answer
	if err := json.Unmarshal([]byte(r.answer), &a); err != nil {
		return Failure(fmt.Errorf("the answer kept is not JSON: %w", err))
	}
	return a
}

// once carries out a step under once: the answer kept under its key, or
// the step's effect and its answer, written in one transaction.
func (k *Keeper) once(ctx context.Context, step StepFunc, task *Task) Answer {
	if r, err := k.read(ctx, k.DB, task.Key); err != nil {
		return Failure(err)
	} else if r != nil && r.state == answered {
		return kept(r)
	}
	tx, err := k.DB.BeginTx(ctx, nil)
	if err != nil {
		return Failure(err)
	}
	defer tx.Rollback()
	answer := step(context.WithValue(ctx, txKey{}, tx), task)
	if answer.Value == nil {
		// Only values are kept: a retry or a failure acts again next
		// time. Another delivery of it that won meanwhile, its effect
		// clashing with this one's, gives what it did
		tx.Rollback()
		if r, _ := k.read(ctx, k.DB, task.Key); r != nil && r.state == answered {
			return kept(r)
		}
		return answer
	}
	data, err := json.Marshal(answer)
	if err != nil {
		return Failure(err)
	}
	if _, err := tx.ExecContext(ctx, k.q(`INSERT INTO flow_steps ("key", state, answer, beat) VALUES (?, ?, ?, ?)`),
		task.Key, answered, string(data), time.Now().UnixMilli()); err != nil {
		// Another delivery of it won the key: its answer stands, and this
		// one's effect goes with the transaction
		tx.Rollback()
		if r, _ := k.read(ctx, k.DB, task.Key); r != nil && r.state == answered {
			return kept(r)
		}
		return Failure(err)
	}
	if err := tx.Commit(); err != nil {
		if r, _ := k.read(ctx, k.DB, task.Key); r != nil && r.state == answered {
			return kept(r)
		}
		return Failure(err)
	}
	return answer
}

// started carries out a step under started: its claim committed first,
// alive until the attempt's deadline, then its answer.
func (k *Keeper) started(ctx context.Context, step StepFunc, task *Task) Answer {
	r, err := k.read(ctx, k.DB, task.Key)
	if err != nil {
		return Failure(err)
	}
	switch {
	case r != nil && r.state == answered:
		return kept(r)
	case r != nil && k.alive(r):
		// An attempt before acts still
		return Answer{Working: true}
	case r != nil && task.Reclaim == 0:
		return Answer{Unknown: true}
	case r != nil:
		// A person said the dead claim did nothing: it is dropped
		if _, err := k.DB.ExecContext(ctx, k.q(`DELETE FROM flow_steps WHERE "key" = ? AND state = ?`), task.Key, claimed); err != nil {
			return Failure(err)
		}
	}
	if _, err := k.DB.ExecContext(ctx, k.q(`INSERT INTO flow_steps ("key", state, answer, beat) VALUES (?, ?, ?, ?)`),
		task.Key, claimed, "", task.Until); err != nil {
		// The loser of a race for the claim does not act
		return Answer{Working: true}
	}
	answer := step(ctx, task)

	if answer.Value == nil {
		// Not done: the claim goes, and the next attempt acts again
		k.DB.ExecContext(context.Background(), k.q(`DELETE FROM flow_steps WHERE "key" = ? AND state = ?`), task.Key, claimed)
		return answer
	}
	data, err := json.Marshal(answer)
	if err != nil {
		return Failure(err)
	}
	if _, err := k.DB.ExecContext(context.Background(), k.q(`UPDATE flow_steps SET state = ?, answer = ? WHERE "key" = ?`),
		answered, string(data), task.Key); err != nil {
		return Failure(err)
	}
	return answer
}

// probe says what became of an attempt under a key: its answer, nothing
// (unseen), under way, or unknown.
func (k *Keeper) probe(ctx context.Context, task *Task) Answer {
	r, err := k.read(ctx, k.DB, task.Key)
	switch {
	case err != nil:
		return Failure(err)
	case r == nil:
		return Answer{Unseen: true}
	case r.state == answered:
		return kept(r)
	case k.alive(r):
		return Answer{Working: true}
	}
	return Answer{Unknown: true}
}

// Forget drops what is under a key: a person said what happened, from
// the application's side.
func (k *Keeper) Forget(ctx context.Context, key string) error {
	_, err := k.DB.ExecContext(ctx, k.q(`DELETE FROM flow_steps WHERE "key" = ?`), key)
	return err
}

// Sweep drops the answers older than the daemon keeps an ended instance:
// what was kept under a key need be kept no longer.
func (k *Keeper) Sweep(ctx context.Context, keep time.Duration) error {
	_, err := k.DB.ExecContext(ctx, k.q(`DELETE FROM flow_steps WHERE state = ? AND beat < ?`), answered, time.Now().Add(-keep).UnixMilli())
	return err
}
