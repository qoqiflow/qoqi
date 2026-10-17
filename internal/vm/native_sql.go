package vm

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/qoqiflow/qoqi/internal/sqldb"
)

// std.sql: databases, a file of SQLite or a server of Postgres or MySQL.
// The program sees numbers; the databases and transactions they stand
// for are kept here. What crosses is text: a value is a letter and what
// follows it (n for null, i an Int, f a Float, t a text, b 1 or 0), and
// the rows of a query are their number of columns, the names, then the
// values row after row. Every wait for the database lets the other
// tasks go on.
//
// A step of a flow asks for its store with flow.store(): a transaction
// on the store's own database, which the runtime commits with what the
// step gives, or rolls back.

type sqlHandle struct {
	db   *sql.DB // a database the program opened
	tx   *sql.Tx // or a transaction
	pool *sql.DB // the database of a transaction
	kind sqldb.Kind
	// log is what a step's transaction on SQLite ran, to run again
	log []replayedStatement
	// step is, for the store's transaction of a step, the step's key: the
	// runtime commits it, not the program
	step string
	// conflict says the database ended the transaction for another that
	// ran at the same time: nothing more runs in it, and it cannot be
	// committed. MySQL has rolled it back, and would run what follows
	// alone
	conflict bool
	// One statement at a time on a transaction: tasks may share one
	mu sync.Mutex
}

type sqlState struct {
	vm      *VM
	mu      sync.Mutex
	handles map[int64]*sqlHandle
	steps   map[string]int64 // the store's transaction of each step
}

var (
	sqlStates sync.Map // *VM -> *sqlState
	// The numbers are never given twice, so that a database closed is
	// never taken for one opened later
	sqlLast atomic.Int64
)

func (vm *VM) sqlDBs() *sqlState {
	if state, ok := sqlStates.Load(vm); ok {
		return state.(*sqlState)
	}
	state, _ := sqlStates.LoadOrStore(vm, &sqlState{vm: vm, handles: map[int64]*sqlHandle{}, steps: map[string]int64{}})
	return state.(*sqlState)
}

func (s *sqlState) add(h *sqlHandle) int64 {
	id := sqlLast.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handles[id] = h
	if h.step != "" {
		s.steps[h.step] = id
	}
	return id
}

func (s *sqlState) get(id int64) *sqlHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handles[id]
}

func (s *sqlState) forget(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.handles[id]; h != nil && h.step != "" {
		delete(s.steps, h.step)
	}
	delete(s.handles, id)
	if len(s.handles) == 0 {
		// Nothing open: a machine run for a flow's step leaves nothing
		// behind it
		sqlStates.CompareAndDelete(s.vm, s)
	}
}

// SetStore gives flow.store its database: the store's, for a step that
// writes there in the transaction that keeps what it gives.
func (vm *VM) SetStore(open func() (*sql.DB, sqldb.Kind, error)) {
	vm.store = open
}

// TakeStoreTx gives the transaction the step of a key opened with
// flow.store(), nil if it opened none, and forgets it: the runtime
// commits it with what the step gave, or rolls it back. ended says the
// database ended it for another transaction: what the step wrote is
// gone, and the step is to run again, whatever it gave.
func (vm *VM) TakeStoreTx(key string) (tx *sql.Tx, ended bool) {
	s := vm.sqlDBs()
	s.mu.Lock()
	id, ok := s.steps[key]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	h := s.get(id)
	s.forget(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.replayed() && !h.conflict && len(h.log) > 0 {
		// Everything the step ran, again, in the transaction its result
		// is kept in
		tx, err := h.pool.BeginTx(context.Background(), nil)
		if err != nil {
			return nil, true
		}
		if same, _ := h.again(tx); !same {
			tx.Rollback()
			return nil, true
		}
		return tx, false
	}
	return h.tx, h.conflict
}

// errEnded is a statement in a transaction the database ended.
var errEnded = errors.New("the transaction was ended by the database for another at the same time: run it again")

// sqlRefused keeps why the database said no, sorted into the kinds of
// std.sql.ErrorKind.
func (vm *VM) sqlRefused(err error) (Value, error) {
	return vm.refused(kindOfSQLError(err), err.Error())
}

func kindOfSQLError(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case strings.HasPrefix(pg.Code, "23"):
			return "constraint"
		case pg.Code == "40001" || pg.Code == "40P01":
			return "conflict"
		case strings.HasPrefix(pg.Code, "42"):
			return "query"
		case strings.HasPrefix(pg.Code, "08"):
			return "unreachable"
		}
		return "other"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		switch my.Number {
		case 1062, 1451, 1452, 1048, 1364, 3819:
			return "constraint"
		case 1213, 1205:
			return "conflict"
		case 1064, 1146, 1054, 1149, 1050:
			return "query"
		case 1045, 1049:
			return "unreachable"
		}
		return "other"
	}
	var lite interface{ Code() int }
	if errors.As(err, &lite) {
		switch lite.Code() & 0xff {
		case 19:
			return "constraint"
		case 5, 6:
			return "conflict"
		case 1:
			return "query"
		case 14:
			return "unreachable"
		}
		return "other"
	}
	if errors.Is(err, errEnded) {
		return "conflict"
	}
	if errors.Is(err, sql.ErrTxDone) || errors.Is(err, sql.ErrConnDone) {
		return "closed"
	}
	return "other"
}

// sqlArgs are the parameters of a statement, from their letters.
func sqlArgs(tokens []string) ([]any, error) {
	args := make([]any, len(tokens))
	for i, token := range tokens {
		if token == "" {
			return nil, fmt.Errorf("a parameter of a statement is empty")
		}
		rest := token[1:]
		switch token[0] {
		case 'n':
			args[i] = nil
		case 'i':
			n, err := strconv.ParseInt(rest, 10, 64)
			if err != nil {
				return nil, err
			}
			args[i] = n
		case 'f':
			f, err := strconv.ParseFloat(rest, 64)
			if err != nil {
				return nil, err
			}
			args[i] = f
		case 't':
			args[i] = rest
		case 'b':
			args[i] = rest == "1"
		default:
			return nil, fmt.Errorf("a parameter of a statement is %q", token)
		}
	}
	return args, nil
}

// The largest whole number an Int holds: beyond it, a number comes as
// its digits, a text.
const largestInt = 1<<47 - 1

// sqlToken is a value of a row, as a letter and what follows it. A
// column says by its type whether its text is a number: MySQL gives
// every value as text, and a decimal comes as its digits, an Int when it
// is whole and a Float otherwise.
func sqlToken(v any, column string) string {
	if column == "NUMERIC" || column == "DECIMAL" {
		var digits string
		switch v := v.(type) {
		case string:
			digits = v
		case []byte:
			digits = string(v)
		}
		if n, err := strconv.ParseInt(digits, 10, 64); err == nil {
			return sqlToken(n, "")
		}
		if whole, ok := strings.CutSuffix(strings.TrimRight(digits, "0"), "."); ok {
			if n, err := strconv.ParseInt(whole, 10, 64); err == nil {
				return sqlToken(n, "")
			}
		}
		if f, err := strconv.ParseFloat(digits, 64); err == nil {
			return sqlToken(f, "")
		}
	}
	switch v := v.(type) {
	case nil:
		return "n"
	case int64:
		if v > largestInt || v < -largestInt {
			return "t" + strconv.FormatInt(v, 10)
		}
		return "i" + strconv.FormatInt(v, 10)
	case int32:
		return "i" + strconv.FormatInt(int64(v), 10)
	case int16:
		return "i" + strconv.FormatInt(int64(v), 10)
	case int8:
		return "i" + strconv.FormatInt(int64(v), 10)
	case uint64:
		if v > largestInt {
			return "t" + strconv.FormatUint(v, 10)
		}
		return "i" + strconv.FormatUint(v, 10)
	case float64:
		return "f" + strconv.FormatFloat(v, 'g', -1, 64)
	case float32:
		return "f" + strconv.FormatFloat(float64(v), 'g', -1, 32)
	case bool:
		if v {
			return "b1"
		}
		return "b0"
	case time.Time:
		return "t" + v.UTC().Format(time.RFC3339Nano)
	case string:
		return "t" + v
	case []byte:
		switch column {
		case "INT", "BIGINT", "TINYINT", "SMALLINT", "MEDIUMINT", "YEAR", "UNSIGNED INT", "UNSIGNED BIGINT", "UNSIGNED TINYINT", "UNSIGNED SMALLINT", "UNSIGNED MEDIUMINT":
			if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
				return sqlToken(n, "")
			}
		case "FLOAT", "DOUBLE":
			if f, err := strconv.ParseFloat(string(v), 64); err == nil {
				return sqlToken(f, "")
			}
		}
		if utf8.Valid(v) {
			return "t" + string(v)
		}
		return `t\x` + hex.EncodeToString(v)
	}
	return "t" + fmt.Sprint(v)
}

// sqlRun runs a statement on a database or a transaction, without the
// turn. rows is nil for one that gives none.
//
// In a transaction, a statement that fails leaves it as it was, on every
// database: Postgres, which would refuse everything after, has each run
// under a savepoint. One the database ended for another transaction
// ends it for good. MySQL, which commits a transaction when a table is
// made, changed or dropped in it, has that done beside it, at once.
func sqlRun(h *sqlHandle, query string, args []any, rows bool) (changed int64, out []string, err error) {
	if h.replayed() {
		return sqlReplay(h, query, args, rows)
	}
	if h.tx == nil {
		return sqlRunOn(h.db, h.kind, query, args, rows)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conflict {
		return 0, nil, errEnded
	}
	if h.kind == sqldb.MySQL && isDDL(query) {
		return sqlBeside(h.pool, query, args)
	}
	saved := h.kind == sqldb.Postgres
	if saved {
		if _, err := h.tx.Exec("SAVEPOINT qoqi_statement"); err != nil {
			return 0, nil, err
		}
	}
	changed, out, err = sqlRunOn(h.tx, h.kind, query, args, rows)
	switch {
	case err != nil && kindOfSQLError(err) == "conflict":
		h.conflict = true
	case err != nil && saved:
		if _, undo := h.tx.Exec("ROLLBACK TO SAVEPOINT qoqi_statement"); undo != nil {
			h.conflict = true
		}
	case saved:
		if _, err := h.tx.Exec("RELEASE SAVEPOINT qoqi_statement"); err != nil {
			h.conflict = true
			return 0, nil, err
		}
	}
	return changed, out, err
}

// isDDL says whether a statement makes, changes or drops a table, an
// index or a view: what MySQL commits a transaction for.
func isDDL(query string) bool {
	text := strings.TrimSpace(query)
	for strings.HasPrefix(text, "--") || strings.HasPrefix(text, "/*") {
		end := "\n"
		if strings.HasPrefix(text, "/*") {
			end = "*/"
		}
		i := strings.Index(text, end)
		if i < 0 {
			return false
		}
		text = strings.TrimSpace(text[i+len(end):])
	}
	word, _, _ := strings.Cut(text, " ")
	switch strings.ToUpper(word) {
	case "CREATE", "ALTER", "DROP", "RENAME", "TRUNCATE":
		return true
	}
	return false
}

// sqlBeside runs a statement on a connection of its own, committed at
// once: on MySQL, what makes a table, kept even when the transaction it
// was written in is rolled back. It waits ten seconds at most for a
// table the transaction holds.
func sqlBeside(pool *sql.DB, query string, args []any) (int64, []string, error) {
	ctx := context.Background()
	conn, err := pool.Conn(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET SESSION lock_wait_timeout = 10"); err != nil {
		return 0, nil, err
	}
	return sqlRunOn(conn, sqldb.MySQL, query, args, false)
}

// querier is a database, a connection or a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func sqlRunOn(on querier, kind sqldb.Kind, query string, args []any, rows bool) (changed int64, out []string, err error) {
	query = kind.Rebind(query)
	ctx := context.Background()
	if !rows {
		result, err := on.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, nil, err
		}
		changed, _ = result.RowsAffected()
		return changed, nil, nil
	}
	found, err := on.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, err
	}
	defer found.Close()
	names, err := found.Columns()
	if err != nil {
		return 0, nil, err
	}
	types := make([]string, len(names))
	if columns, err := found.ColumnTypes(); err == nil {
		for i, c := range columns {
			types[i] = strings.ToUpper(c.DatabaseTypeName())
		}
	}
	out = append([]string{strconv.Itoa(len(names))}, names...)
	values := make([]any, len(names))
	pointers := make([]any, len(names))
	for i := range values {
		pointers[i] = &values[i]
	}
	for found.Next() {
		if err := found.Scan(pointers...); err != nil {
			return 0, nil, err
		}
		for i, v := range values {
			out = append(out, sqlToken(v, types[i]))
		}
	}
	return 0, out, found.Err()
}

// --- The store's transaction of a step, on SQLite ---
//
// SQLite has one writer for the whole file, the store's own writes among
// them: a step that held it while it waited for the world would keep
// every other instance waiting, and its own runtime too. So the step's
// transaction is not held. Each statement runs in a transaction of its
// own that runs those before it again first, and is rolled back: the
// step reads what it wrote, and the writer is held for a moment. At the
// step's end, every statement runs again, in the transaction its result
// is kept in: if one gives otherwise than it gave the step (another wrote
// meanwhile what it read), the step runs again from nothing, as on a
// server that ends a transaction for another.

// replayedStatement is a statement a step ran, and what it gave.
type replayedStatement struct {
	query   string
	args    []any
	rows    bool
	changed int64
	out     []string
}

func (h *sqlHandle) replayed() bool { return h.step != "" && h.kind == sqldb.SQLite }

// again runs the statements of a step again in a transaction: false when
// one gives otherwise than it gave.
func (h *sqlHandle) again(tx *sql.Tx) (bool, error) {
	for _, st := range h.log {
		changed, out, err := sqlRunOn(tx, sqldb.SQLite, st.query, st.args, st.rows)
		if err != nil {
			if kindOfSQLError(err) == "conflict" {
				return false, err
			}
			return false, nil
		}
		if changed != st.changed || strings.Join(out, "\x00") != strings.Join(st.out, "\x00") {
			return false, nil
		}
	}
	return true, nil
}

func sqlReplay(h *sqlHandle, query string, args []any, rows bool) (changed int64, out []string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conflict {
		return 0, nil, errEnded
	}
	tx, err := h.pool.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	same, err := h.again(tx)
	if err != nil {
		return 0, nil, err
	}
	if !same {
		h.conflict = true
		return 0, nil, errEnded
	}
	changed, out, err = sqlRunOn(tx, sqldb.SQLite, query, args, rows)
	if err != nil {
		return 0, nil, err
	}
	h.log = append(h.log, replayedStatement{query: query, args: args, rows: rows, changed: changed, out: out})
	return changed, out, nil
}

// sqlStatement is exec and query: on a handle, a statement and its
// parameters.
func sqlStatement(rows bool) func(vm *VM, args []Value) (Value, error) {
	return func(vm *VM, args []Value) (Value, error) {
		id, err := argInt(args, 0)
		if err != nil {
			return NewNull(), err
		}
		query, err := argString(args, 1)
		if err != nil {
			return NewNull(), err
		}
		tokens, err := argStrings(args, 2)
		if err != nil {
			return NewNull(), err
		}
		params, err := sqlArgs(tokens)
		if err != nil {
			return NewNull(), err
		}
		h := vm.sqlDBs().get(id)
		if h == nil {
			return vm.refused("closed", "the database or the transaction is closed")
		}
		var changed int64
		var out []string
		var failed error
		if gone := vm.blocking(func() { changed, out, failed = sqlRun(h, query, params, rows) }); gone != nil {
			return NewNull(), gone
		}
		if failed != nil {
			return vm.sqlRefused(failed)
		}
		if rows {
			return vm.newStringArray(out)
		}
		return NewInt(changed), nil
	}
}

func init() {
	registerNatives("std.sql",
		// A database by its address; its number, or null
		native{"openAt", 1, func(vm *VM, args []Value) (Value, error) {
			address, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			var db *sql.DB
			var kind sqldb.Kind
			var failed error
			if gone := vm.blocking(func() { db, kind, failed = sqldb.Open(address) }); gone != nil {
				if db != nil {
					db.Close()
				}
				return NewNull(), gone
			}
			if failed != nil {
				return vm.refused("unreachable", failed.Error())
			}
			return NewInt(vm.sqlDBs().add(&sqlHandle{db: db, kind: kind})), nil
		}},
		// The kind of a database: "sqlite", "postgres" or "mysql"
		native{"kindOf", 1, func(vm *VM, args []Value) (Value, error) {
			id, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if h := vm.sqlDBs().get(id); h != nil {
				return vm.newString(h.kind.String())
			}
			return vm.newString("")
		}},
		native{"exec", 3, sqlStatement(false)},
		native{"query", 3, sqlStatement(true)},
		// A transaction on a database; its number, or null
		native{"begin", 1, func(vm *VM, args []Value) (Value, error) {
			id, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			h := vm.sqlDBs().get(id)
			if h == nil || h.db == nil {
				return vm.refused("closed", "the database is closed")
			}
			var tx *sql.Tx
			var failed error
			if gone := vm.blocking(func() { tx, failed = h.db.BeginTx(context.Background(), nil) }); gone != nil {
				if tx != nil {
					tx.Rollback()
				}
				return NewNull(), gone
			}
			if failed != nil {
				return vm.sqlRefused(failed)
			}
			return NewInt(vm.sqlDBs().add(&sqlHandle{tx: tx, pool: h.db, kind: h.kind})), nil
		}},
		// Ends a transaction, committed or rolled back: true, or false
		// with the reason kept
		native{"end", 2, func(vm *VM, args []Value) (Value, error) {
			id, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			commit := args[1].IsBool() && args[1].AsBool()
			s := vm.sqlDBs()
			h := s.get(id)
			if h != nil && h.step != "" {
				_, err := vm.refused("other", "the store's transaction of a step ends with the step: it is committed with what the step gives")
				return NewBool(false), err
			}
			if h == nil || h.tx == nil {
				_, err := vm.refused("closed", "the transaction has ended")
				return NewBool(false), err
			}
			s.forget(id)
			var failed error
			if gone := vm.blocking(func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				switch {
				case commit && h.conflict:
					h.tx.Rollback()
					failed = errEnded
				case commit:
					failed = h.tx.Commit()
				default:
					failed = h.tx.Rollback()
				}
			}); gone != nil {
				return NewBool(false), gone
			}
			if failed != nil {
				_, err := vm.sqlRefused(failed)
				return NewBool(false), err
			}
			return NewBool(true), nil
		}},
		native{"close", 1, func(vm *VM, args []Value) (Value, error) {
			id, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s := vm.sqlDBs()
			h := s.get(id)
			if h == nil || h.db == nil {
				return NewNull(), nil
			}
			s.forget(id)
			return NewNull(), vm.blocking(func() { h.db.Close() })
		}},
		// The store's transaction of the step running, flow.store(): begun
		// at the first call, the same at the next; its number, or null. A
		// native of std.sql, so that a flow that asks for it outside a
		// step is told it reaches a database
		native{"storeTx", 0, func(vm *VM, args []Value) (Value, error) {
			if vm.stepKey == "" {
				return NewNull(), fmt.Errorf("flow.store() is asked for inside a step of a flow, run by the runtime of flows")
			}
			if vm.store == nil {
				return NewNull(), fmt.Errorf("flow.store() is asked for in a runtime of flows that has no database")
			}
			key := vm.stepKey
			s := vm.sqlDBs()
			s.mu.Lock()
			id, ok := s.steps[key]
			s.mu.Unlock()
			if ok {
				return NewInt(id), nil
			}
			var tx *sql.Tx
			var db *sql.DB
			var kind sqldb.Kind
			var failed error
			if gone := vm.blocking(func() {
				if db, kind, failed = vm.store(); failed == nil && kind != sqldb.SQLite {
					// On SQLite, nothing is held until the step ends
					tx, failed = db.BeginTx(context.Background(), nil)
				}
			}); gone != nil {
				if tx != nil {
					tx.Rollback()
				}
				return NewNull(), gone
			}
			if failed != nil {
				return vm.sqlRefused(failed)
			}
			return NewInt(s.add(&sqlHandle{tx: tx, pool: db, kind: kind, step: key})), nil
		}},
		problemNative,
	)
}
