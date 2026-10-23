package flowrt

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/sqldb"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// SQL is a store in a database: a file of SQLite, or a server of
// Postgres or MySQL. What a runtime that stops leaves there is what the
// next one takes up. Like Memory, it can be told to fail at its n-th
// write, as a process that stops there.
//
// An instance is a row; what it is besides the columns `due` reads (its
// program as it was last written, the counts of its steps) is JSON in
// one column, written in one piece by every save.
//
// Every write is a transaction that runs as if it were alone: SQLite has
// one writer, and a server is asked for serializable transactions, run
// again when it ends one for another that ran at the same time. So an
// instance that starts to wait for a child as the child ends is woken
// on every kind of database alike.
type SQL struct {
	db   *sql.DB
	kind sqldb.Kind
	// prefix is put before the names of its tables: each program a
	// daemon serves from a folder has tables of its own in one database
	prefix string
	owner  bool // whether closing it closes the database
	// holder names the daemon, among those that share the store, that
	// runs its instances under their leases; "" for a store one process
	// has alone
	holder string
	// holds are the leases this store took, by instance: what each lease
	// was taken as. A lease lost is kept until it is let go of, so that
	// the store's saves of that instance are refused meanwhile
	holds map[string]string

	mu        sync.Mutex
	writes    int
	FailAt    int
	ResultLog []string
}

// OpenSQLite opens a store in a file, made if it is not there.
func OpenSQLite(path string) (*SQL, error) {
	return OpenStore(strings.TrimPrefix(path, "sqlite:"))
}

// OpenStore opens a store at an address: a file of SQLite, or
// postgres://... or mysql://... (see sqldb). Its tables are made if they
// are not there.
func OpenStore(address string) (*SQL, error) {
	db, kind, err := sqldb.OpenShared(address)
	if err != nil {
		return nil, err
	}
	if kind != sqldb.SQLite {
		db.SetMaxOpenConns(32)
		db.SetMaxIdleConns(8)
	}
	s := &SQL{db: db, kind: kind, owner: true}
	if err := s.makeTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot make the store in %s: %v", sqldb.Redacted(address), err)
	}
	return s, nil
}

// Kind is the kind of database the store is in.
func (s *SQL) Kind() sqldb.Kind { return s.kind }

// Program is the store of one program of a folder a daemon serves: its
// tables, named after it, in the same database, made if they are not
// there.
func (s *SQL) Program(name string) (*SQL, error) {
	var prefix strings.Builder
	prefix.WriteString(s.prefix)
	for _, c := range strings.ToLower(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			prefix.WriteRune(c)
		} else {
			prefix.WriteRune('_')
		}
	}
	program := &SQL{db: s.db, kind: s.kind, prefix: prefix.String() + "_", holder: s.holder}
	if err := program.makeTables(); err != nil {
		return nil, fmt.Errorf("cannot make the tables of %s: %v", name, err)
	}
	return program, nil
}

func (s *SQL) makeTables() error {
	for _, statement := range schemaOf(s.kind) {
		if _, err := s.db.Exec(s.q(statement)); err != nil {
			return err
		}
	}
	alters := added
	if s.kind != sqldb.SQLite {
		alters = addedOnServers[s.kind]
	}
	for _, alter := range alters {
		// Refused when the column is there already
		_, _ = s.db.Exec(s.q(alter))
	}
	return nil
}

// ShareAs says the store is shared by several daemons, and names this
// one: an instance is run by the daemon that holds its lease (Claim),
// another's leases are not due to it, and a save of an instance another
// holds is refused. The programs it opens after share it as well.
func (s *SQL) ShareAs(holder string) { s.holder = holder }

// ErrNotHeld is a save of an instance whose lease another daemon has
// taken: this one stopped holding it, too long without a word, and the
// other runs it now.
var ErrNotHeld = errors.New("another daemon holds the instance now")

// Claim takes an instance for this daemon until a time, or holds it
// longer: false when another holds it. On a store one process has
// alone, it is always this one's.
//
// Each lease is taken as a token of its own, drawn when it is taken: it
// is held longer only as that token, and a save of the instance is
// refused unless the lease is still the one the store took. A daemon
// that stopped for longer than its lease, and whose instance another
// took, and maybe let go of, writes nothing of it any more.
func (s *SQL) Claim(id string, now, until int64) (bool, error) {
	if s.holder == "" {
		return true, nil
	}
	s.mu.Lock()
	as, holding := s.holds[id]
	s.mu.Unlock()
	if holding {
		// Held longer, as it was taken; lost if another took it meanwhile
		n, err := s.exec(s.q(`UPDATE instances SET held_until = ? WHERE id = ? AND held_by = ? AND held_as = ?`), until, id, s.holder, as)
		return n == 1, err
	}
	as = birth() + birth()
	n, err := s.exec(s.q(`UPDATE instances SET held_by = ?, held_until = ?, held_as = ?
		WHERE id = ? AND (held_by = '' OR held_until <= ?)`), s.holder, until, as, id, now)
	if err != nil || n != 1 {
		return false, err
	}
	s.mu.Lock()
	if s.holds == nil {
		s.holds = map[string]string{}
	}
	s.holds[id] = as
	s.mu.Unlock()
	return true, nil
}

// Unclaim lets an instance go, for any daemon to take up when it is due:
// the lease the store took, if another has not taken it since.
func (s *SQL) Unclaim(id string) error {
	if s.holder == "" {
		return nil
	}
	s.mu.Lock()
	as, holding := s.holds[id]
	delete(s.holds, id)
	s.mu.Unlock()
	if !holding {
		return nil
	}
	_, err := s.exec(s.q(`UPDATE instances SET held_by = '', held_until = 0, held_as = '' WHERE id = ? AND held_by = ? AND held_as = ?`), id, s.holder, as)
	return err
}

// LetGoOf lets go of the leases held by the daemons a test says are gone
// (a daemon of this machine whose process died): their instances are
// due to the others at once, without waiting for the leases to run out.
func (s *SQL) LetGoOf(gone func(holder string) bool) error {
	rows, err := s.db.Query(s.q(`SELECT DISTINCT held_by FROM instances WHERE held_by != ''`))
	if err != nil {
		return err
	}
	var holders []string
	for rows.Next() {
		var holder string
		if rows.Scan(&holder) == nil && holder != s.holder && gone(holder) {
			holders = append(holders, holder)
		}
	}
	rows.Close()
	for _, holder := range holders {
		if _, err := s.exec(s.q(`UPDATE instances SET held_by = '', held_until = 0, held_as = '' WHERE held_by = ?`), holder); err != nil {
			return err
		}
	}
	return nil
}

// stillHeld refuses, in a write, an instance whose lease is not the one
// this store took: another daemon holds it now, or took it and let it
// go since. One this store took no lease of is refused while another
// daemon holds it.
func (s *SQL) stillHeld(tx *sql.Tx, id string, now int64) error {
	if s.holder == "" {
		return nil
	}
	s.mu.Lock()
	as, holding := s.holds[id]
	s.mu.Unlock()
	var holder, heldAs string
	var until int64
	err := tx.QueryRow(s.q(`SELECT held_by, held_until, held_as FROM instances WHERE id = ?`), id).Scan(&holder, &until, &heldAs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if holding {
		if holder != s.holder || heldAs != as {
			return ErrNotHeld
		}
		return nil
	}
	if holder != "" && holder != s.holder && until > now {
		return ErrNotHeld
	}
	return nil
}

// q is a query on the store's own tables, as its database writes it.
func (s *SQL) q(query string) string {
	if s.prefix != "" {
		query = tables.ReplaceAllString(query, s.prefix+"$1")
	}
	return dialect(s.kind, query)
}

// Close closes the database, from the store that opened it.
func (s *SQL) Close() error {
	if !s.owner {
		return nil
	}
	return s.db.Close()
}

// tx runs a write in one transaction. On a server, it is serializable,
// and run again, a little later each time, while the server ends it for
// another that ran at the same time; what it does is to be done again
// in full, from what it reads in the transaction.
func (s *SQL) tx(write func(tx *sql.Tx) error) error {
	var options *sql.TxOptions
	if s.kind != sqldb.SQLite {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	for try := 0; ; try++ {
		err := func() error {
			tx, err := s.db.BeginTx(context.Background(), options)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := write(tx); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil || try == 100 || !s.kind.Retry(err) {
			return err
		}
		pause := time.Duration(1+min(try, 20)) * time.Millisecond
		time.Sleep(pause + rand.N(pause))
	}
}

// exec is a write of one statement.
func (s *SQL) exec(query string, args ...any) (rows int64, err error) {
	err = s.tx(func(tx *sql.Tx) error {
		result, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
		rows, _ = result.RowsAffected()
		return nil
	})
	return rows, err
}

// Writes is how many writes the store has taken.
func (s *SQL) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *SQL) write() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.FailAt > 0 && s.writes == s.FailAt {
		return ErrCrash
	}
	return nil
}

// row is the part of an instance kept as JSON.
type row struct {
	Birth    string           `json:"birth"`
	Fields   json.RawMessage  `json:"fields"`
	State    json.RawMessage  `json:"state,omitempty"`
	Counts   map[string]int   `json:"counts,omitempty"`
	Tries    map[string]int   `json:"tries,omitempty"`
	Retried  map[string]int64 `json:"retried,omitempty"`
	Working  map[string]int64 `json:"working,omitempty"`
	Result   json.RawMessage  `json:"result,omitempty"`
	Stopped  string           `json:"stopped,omitempty"`
	Phase    string           `json:"phase,omitempty"`
	Undos    []vm.Undo        `json:"undos,omitempty"`
	Parent   string           `json:"parent,omitempty"`
	Children []string         `json:"children,omitempty"`
	Awaits   []string         `json:"awaits,omitempty"`
	Pass     int              `json:"pass,omitempty"`
	Version  string           `json:"version,omitempty"`
	Taken    bool             `json:"cancelTaken,omitempty"`
	Replay   bool             `json:"replaying,omitempty"`
	Ended    string           `json:"ended,omitempty"`
	AtStage  *AtStage         `json:"atStage,omitempty"`
	// Absent from what was written before values drawn were kept: such
	// an instance is replayed with the time and random numbers new
	Drawn  map[string][]vm.Drawn `json:"drawn,omitempty"`
	Giving map[string][]vm.Drawn `json:"giving,omitempty"`
}

func rowOf(inst *Instance) (string, error) {
	r := row{Birth: inst.Birth, Fields: inst.Fields, State: inst.State, Counts: inst.Counts, Tries: inst.Tries, Retried: inst.Retried, Working: inst.Working, Result: inst.Result, Stopped: inst.Stopped, Phase: inst.Phase, Undos: inst.Undos, Parent: inst.Parent, Children: inst.Children, Awaits: inst.Awaits, Pass: inst.Pass, AtStage: inst.AtStage, Version: inst.Version, Replay: inst.Replaying, Ended: inst.Ended, Taken: inst.CancelTaken, Drawn: inst.Drawn, Giving: inst.Giving}
	data, err := json.Marshal(r)
	return string(data), err
}

func instanceOf(id, flow, status string, wakeAt int64, data, cancel string) (*Instance, error) {
	var r row
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return nil, fmt.Errorf("instance %s: %v", id, err)
	}
	return &Instance{
		ID: id, Birth: r.Birth, Flow: flow, Status: Status(status), WakeAt: wakeAt,
		Fields: r.Fields, State: r.State, Counts: r.Counts, Tries: r.Tries, Retried: r.Retried, Working: r.Working,
		Result: r.Result, Stopped: r.Stopped, Phase: r.Phase, Undos: r.Undos, Cancel: cancel, Parent: r.Parent, Children: r.Children, Awaits: r.Awaits, Pass: r.Pass, AtStage: r.AtStage, Version: r.Version, Replaying: r.Replay, Ended: r.Ended, CancelTaken: r.Taken, Drawn: r.Drawn, Giving: r.Giving,
	}, nil
}

func (s *SQL) Create(inst *Instance) (bool, *Instance, error) {
	if existing, ok, err := s.Get(inst.ID); err != nil || ok {
		return false, existing, err
	}
	if err := s.write(); err != nil {
		return false, nil, err
	}
	data, err := rowOf(inst)
	if err != nil {
		return false, nil, err
	}
	n, err := s.exec(s.q(`INSERT INTO instances (id, flow, status, wake_at, row) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`),
		inst.ID, inst.Flow, string(inst.Status), inst.WakeAt, data)
	if err != nil {
		return false, nil, err
	}
	if n == 0 {
		// Another made it meanwhile
		existing, _, err := s.Get(inst.ID)
		return false, existing, err
	}
	return true, nil, nil
}

func (s *SQL) Save(inst *Instance) error {
	if err := s.write(); err != nil {
		return err
	}
	data, err := rowOf(inst)
	if err != nil {
		return err
	}
	// Each name it waits for, quoted, so that one is found in the list
	awaited := ""
	for _, name := range inst.Awaits {
		quoted, _ := json.Marshal(name)
		awaited += string(quoted)
	}
	return s.tx(func(tx *sql.Tx) error {
		if err := s.stillHeld(tx, inst.ID, time.Now().UnixMilli()); err != nil {
			return err
		}
		// A cancellation asked while it ran makes one that sleeps or
		// stopped due at once, to act on it
		_, err := tx.Exec(s.q(`INSERT INTO instances (id, flow, status, wake_at, row, awaits) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET flow = excluded.flow, row = excluded.row, awaits = excluded.awaits,
			status = CASE WHEN instances.cancel != '' AND ? = '' AND excluded.status IN ('waiting', 'stopped') THEN 'waiting' ELSE excluded.status END,
			wake_at = CASE WHEN instances.cancel != '' AND ? = '' AND excluded.status IN ('waiting', 'stopped') THEN 0 ELSE excluded.wake_at END`),
			inst.ID, inst.Flow, string(inst.Status), inst.WakeAt, data, awaited, untilTaken(inst), untilTaken(inst))
		if err != nil {
			return err
		}
		// The instances it started since its last write: made with this one
		for _, child := range inst.Starting {
			if err := s.makeStarted(tx, inst.ID, child); err != nil {
				return err
			}
		}
		// The messages it took since its last write: taken with this one
		for _, taken := range inst.Taking {
			if _, err := tx.Exec(s.q(`UPDATE messages SET taken = 1, pass = ? WHERE seq = ?`), taken.Pass, taken.Seq); err != nil {
				return err
			}
		}
		if inst.Status == Waiting {
			if err := s.wakeIfAnswered(tx, inst); err != nil {
				return err
			}
		}
		if inst.Status == Done || inst.Status == Failed {
			// Those that wait for it are due, in the same write
			quoted, _ := json.Marshal(inst.ID)
			if _, err := tx.Exec(s.q(`UPDATE instances SET wake_at = 0 WHERE status = 'waiting' AND instr(awaits, ?) > 0`), string(quoted)); err != nil {
				return err
			}
		}
		return nil
	})
}

// makeStarted makes, in a save of the instance that started it, an
// instance started since its last write. A name another took meanwhile
// fails the save, if the instance took it for its child: taken up again
// from its last write, it finds the name taken.
func (s *SQL) makeStarted(tx *sql.Tx, by string, child *Instance) error {
	data, err := rowOf(child)
	if err != nil {
		return err
	}
	result, err := tx.Exec(s.q(`INSERT INTO instances (id, flow, status, wake_at, row) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`),
		child.ID, child.Flow, string(child.Status), child.WakeAt, data)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 || child.Parent == "" {
		return nil
	}
	var row string
	if err := tx.QueryRow(s.q(`SELECT row FROM instances WHERE id = ?`), child.ID).Scan(&row); err != nil {
		return err
	}
	var made struct {
		Parent string `json:"parent"`
		Birth  string `json:"birth"`
	}
	if err := json.Unmarshal([]byte(row), &made); err != nil {
		return err
	}
	if made.Parent != by {
		return fmt.Errorf("%w: %s, started by %s, was started meanwhile by another", ErrStartedMeanwhile, child.ID, by)
	}
	return nil
}

// ErrStartedMeanwhile is a save of an instance that started a child under
// a name another took between the start and the save.
var ErrStartedMeanwhile = errors.New("a name was taken meanwhile")

// wakeIfAnswered makes an instance that starts to wait due at once if
// what it waits for came while it ran: one it waits for that ended, a
// message to an inbox it waits on, an answer to a step.
//
// A read that fails fails the write: a server that ends a transaction
// for another has rolled back what it wrote, and what comes after would
// be written alone.
func (s *SQL) wakeIfAnswered(tx *sql.Tx, inst *Instance) error {
	wake := func() error {
		_, err := tx.Exec(s.q(`UPDATE instances SET wake_at = 0 WHERE id = ?`), inst.ID)
		return err
	}
	for _, name := range inst.Awaits {
		answered, err := s.answerWaited(tx, name)
		if err != nil {
			return err
		}
		if answered {
			// An answer that came to a step while it ran
			if err := wake(); err != nil {
				return err
			}
			continue
		}
		if inbox, ok := strings.CutPrefix(name, "inbox:"); ok {
			var waiting int
			if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM messages WHERE instance = ? AND inbox = ? AND taken = 0`), inst.ID, inbox).Scan(&waiting); err != nil {
				return err
			}
			if waiting > 0 {
				if err := wake(); err != nil {
					return err
				}
			}
			continue
		}
		var status string
		err = tx.QueryRow(s.q(`SELECT status FROM instances WHERE id = ?`), name).Scan(&status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if status == "done" || status == "failed" {
			return wake()
		}
	}
	return nil
}

func (s *SQL) Get(id string) (*Instance, bool, error) {
	var flow, status, data, cancel string
	var wakeAt int64
	err := s.db.QueryRow(s.q(`SELECT flow, status, wake_at, row, cancel FROM instances WHERE id = ?`), id).Scan(&flow, &status, &wakeAt, &data, &cancel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	inst, err := instanceOf(id, flow, status, wakeAt, data, cancel)
	if err != nil {
		return nil, false, err
	}
	return inst, true, nil
}

// Due takes up, as a runtime that starts again does, what was running
// when the process stopped.
//
// On a store several daemons share, an instance another holds is not
// due to this one, until its lease runs out.
func (s *SQL) Due(now int64) []string {
	rows, err := s.db.Query(s.q(`SELECT id FROM instances
		WHERE (status = 'running' OR (status = 'waiting' AND wake_at <= ?))
		AND (held_by = '' OR held_by = ? OR held_until <= ?)
		ORDER BY id`), now, s.holder, now)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *SQL) AnyWaiting() bool {
	var n int
	err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM instances WHERE status IN ('waiting', 'running')`)).Scan(&n)
	return err == nil && n > 0
}

func (s *SQL) Cancel(id, reason string, now int64) error {
	inst, ok, err := s.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("there is no instance %s", id)
	}
	if err := cancelable(inst); err != nil || inst.Phase == Finish || inst.Cancel != "" {
		return err
	}
	if err := s.write(); err != nil {
		return err
	}
	// The first reason stands; one that waits or stopped is due at once.
	// wake_at is set before status: MySQL reads, in an assignment, what
	// those before it wrote
	_, err = s.exec(s.q(`UPDATE instances SET cancel = ?,
		wake_at = CASE WHEN status IN ('waiting', 'stopped') THEN ? ELSE wake_at END,
		status = CASE WHEN status IN ('waiting', 'stopped') THEN 'waiting' ELSE status END
		WHERE id = ? AND cancel = '' AND status NOT IN ('done', 'failed')`), reason, now, id)
	return err
}

func (s *SQL) PutResult(key string, kept Kept) error {
	if err := s.write(); err != nil {
		return err
	}
	undo := ""
	if len(kept.Undos) > 0 {
		encoded, err := json.Marshal(kept.Undos)
		if err != nil {
			return err
		}
		undo = string(encoded)
	}
	n, err := s.exec(s.q(`INSERT INTO steps (key, result, undo, asked) VALUES (?, ?, ?, ?) ON CONFLICT (key) DO NOTHING`), key, string(kept.Data), undo, string(kept.Asked))
	if err != nil {
		return err
	}
	if n == 1 {
		s.mu.Lock()
		s.ResultLog = append(s.ResultLog, key)
		s.mu.Unlock()
	}
	return nil
}

func (s *SQL) Result(key string) (Kept, bool, error) {
	var data, undo, asked string
	err := s.db.QueryRow(s.q(`SELECT result, undo, asked FROM steps WHERE key = ?`), key).Scan(&data, &undo, &asked)
	if errors.Is(err, sql.ErrNoRows) {
		return Kept{}, false, nil
	}
	if err != nil {
		return Kept{}, false, err
	}
	var undos []vm.Undo
	if undo != "" {
		if err := json.Unmarshal([]byte(undo), &undos); err != nil {
			return Kept{}, false, fmt.Errorf("what %s gave flow.undo: %v", key, err)
		}
	}
	kept := Kept{Data: []byte(data), Undos: undos}
	if asked != "" {
		kept.Asked = []byte(asked)
	}
	return kept, true, nil
}

func (s *SQL) ForgetResult(key string) error {
	if err := s.write(); err != nil {
		return err
	}
	_, err := s.exec(s.q(`DELETE FROM steps WHERE key = ?`), key)
	return err
}

// The keys that start with a prefix are those between it and the
// greatest text that does: compared as bytes, on every kind of database.
const greatest = "\U0010FFFF"

func (s *SQL) Keys(prefix string) []string {
	rows, err := s.db.Query(s.q(`SELECT key FROM steps WHERE key >= ? AND key < ? ORDER BY key`), prefix, prefix+greatest)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			keys = append(keys, key)
		}
	}
	return keys
}

func (s *SQL) List(flow string, status Status) []*Instance {
	rows, err := s.db.Query(s.q(`SELECT id, flow, status, wake_at, row, cancel FROM instances
		WHERE (? = '' OR flow = ?) AND (? = '' OR status = ?) ORDER BY id`), flow, flow, string(status), string(status))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Instance
	for rows.Next() {
		var id, f, st, data, cancel string
		var wakeAt int64
		if rows.Scan(&id, &f, &st, &wakeAt, &data, &cancel) != nil {
			continue
		}
		if inst, err := instanceOf(id, f, st, wakeAt, data, cancel); err == nil {
			out = append(out, inst)
		}
	}
	return out
}

func (s *SQL) PutVersion(fingerprint string, bytecode []byte) error {
	_, err := s.exec(s.q(`INSERT INTO versions (fingerprint, bytecode) VALUES (?, ?) ON CONFLICT (fingerprint) DO NOTHING`), fingerprint, bytecode)
	return err
}

func (s *SQL) Version(fingerprint string) ([]byte, bool) {
	var bytecode []byte
	if err := s.db.QueryRow(s.q(`SELECT bytecode FROM versions WHERE fingerprint = ?`), fingerprint).Scan(&bytecode); err != nil {
		return nil, false
	}
	return bytecode, true
}

func (s *SQL) Send(id, inbox, key string, data []byte, capacity int) (bool, error) {
	already := false
	err := s.tx(func(tx *sql.Tx) error {
		already = false
		var status string
		if err := tx.QueryRow(s.q(`SELECT status FROM instances WHERE id = ?`), id).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("there is no instance %s", id)
			}
			return err
		}
		if status == string(Done) || status == string(Failed) {
			return ErrEnded
		}
		if key != "" {
			var n int
			if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM messages WHERE instance = ? AND inbox = ? AND key = ?`), id, inbox, key).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				already = true
				return nil
			}
		}
		var waiting int
		if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM messages WHERE instance = ? AND inbox = ? AND taken = 0`), id, inbox).Scan(&waiting); err != nil {
			return err
		}
		if waiting >= capacity {
			return ErrFull
		}
		if _, err := tx.Exec(s.q(`INSERT INTO messages (instance, inbox, key, data) VALUES (?, ?, ?, ?)`), id, inbox, key, string(data)); err != nil {
			return err
		}
		_, err := tx.Exec(s.q(`UPDATE instances SET wake_at = 0 WHERE id = ? AND status = 'waiting'`), id)
		return err
	})
	return already, err
}

func (s *SQL) Messages(id, inbox string) []Message {
	rows, err := s.db.Query(s.q(`SELECT seq, data FROM messages WHERE instance = ? AND inbox = ? AND taken = 0 ORDER BY seq`), id, inbox)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var msg Message
		var data string
		if rows.Scan(&msg.Seq, &data) == nil {
			msg.Data = []byte(data)
			out = append(out, msg)
		}
	}
	return out
}

func (s *SQL) Untake(id string, fromPass int) error {
	_, err := s.exec(s.q(`UPDATE messages SET taken = 0 WHERE instance = ? AND taken = 1 AND pass >= ?`), id, fromPass)
	return err
}

// untilTaken is what makes an instance due for its cancellation: its
// phase, while it has not been given it; "taken" once it was.
func untilTaken(inst *Instance) string {
	if inst.CancelTaken {
		return "taken"
	}
	return inst.Phase
}

// PutDeployment keeps a deploy of a program, after those before it.
func (s *SQL) PutDeployment(d Deployed) (int64, error) {
	var seq int64
	err := s.tx(func(tx *sql.Tx) error {
		query := `INSERT INTO deploys (name, version, at, package, lock, report) VALUES (?, ?, ?, ?, ?, ?)`
		args := []any{d.Name, d.Version, d.At, d.Package, string(d.Lock), string(d.Report)}
		if s.kind == sqldb.Postgres {
			// Postgres says the number it gave only when asked
			return tx.QueryRow(s.q(query+` RETURNING seq`), args...).Scan(&seq)
		}
		result, err := tx.Exec(s.q(query), args...)
		if err != nil {
			return err
		}
		seq, err = result.LastInsertId()
		return err
	})
	return seq, err
}

// Deployments are the last deploy of each program, by name.
func (s *SQL) Deployments() ([]Deployed, error) {
	// A program taken away is deployed as no version
	return s.deploys(`SELECT seq, name, version, at, package, lock, report FROM deploys
		WHERE seq IN (SELECT MAX(seq) FROM deploys GROUP BY name) AND version != '' ORDER BY name`)
}

// History is every deploy, of one program or of all, the first first;
// with what was deployed, or without when only what is said of each is
// asked for.
func (s *SQL) History(name string, packages bool) ([]Deployed, error) {
	columns := "seq, name, version, at, x'', lock, report"
	if packages {
		columns = "seq, name, version, at, package, lock, report"
	}
	if name == "" {
		return s.deploys(`SELECT ` + columns + ` FROM deploys ORDER BY seq`)
	}
	return s.deploys(`SELECT `+columns+` FROM deploys WHERE name = ? ORDER BY seq`, name)
}

// Deploy is one deploy, with what was deployed.
func (s *SQL) Deploy(seq int64) (Deployed, bool) {
	out, err := s.deploys(`SELECT seq, name, version, at, package, lock, report FROM deploys WHERE seq = ?`, seq)
	if err != nil || len(out) == 0 {
		return Deployed{}, false
	}
	return out[0], true
}

func (s *SQL) deploys(query string, args ...any) ([]Deployed, error) {
	rows, err := s.db.Query(s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployed
	for rows.Next() {
		var d Deployed
		var lock, report string
		if err := rows.Scan(&d.Seq, &d.Name, &d.Version, &d.At, &d.Package, &lock, &report); err != nil {
			return nil, err
		}
		d.Lock, d.Report = []byte(lock), []byte(report)
		out = append(out, d)
	}
	return out, rows.Err()
}

// Database is the store's database, for a step that writes there with
// flow.store(): its tables beside the store's own.
func (s *SQL) Database() (*sql.DB, sqldb.Kind) { return s.db, s.kind }

// KeepIn keeps what a step gave in the transaction the step wrote in,
// and commits both: the rows the step wrote and its result are there
// together, or neither is. What another kept under the key first stands,
// and the step's rows are rolled back with it.
func (s *SQL) KeepIn(tx *sql.Tx, key string, kept Kept) error {
	defer tx.Rollback()
	if err := s.write(); err != nil {
		return err
	}
	undo := ""
	if len(kept.Undos) > 0 {
		encoded, err := json.Marshal(kept.Undos)
		if err != nil {
			return err
		}
		undo = string(encoded)
	}
	result, err := tx.Exec(s.q(`INSERT INTO steps (key, result, undo, asked) VALUES (?, ?, ?, ?) ON CONFLICT (key) DO NOTHING`), key, string(kept.Data), undo, string(kept.Asked))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.ResultLog = append(s.ResultLog, key)
	s.mu.Unlock()
	return nil
}
