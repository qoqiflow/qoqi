package flowd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/sqldb"
)

// What the console keeps, in the daemon's own database, beside the tables
// of the flows: the people who sign in to it and their sessions, the
// programs written in its editor and not deployed yet, and what happened
// to the instances, day by day, for its pages of activity. A daemon
// without it (Keep nil) answers to its token alone, and has no editor.

// Keep is the console's tables, in a database.
type Keep struct {
	db   *sql.DB
	kind sqldb.Kind
}

// User is a person who signs in to the console.
type User struct {
	Name    string `json:"name"`
	Hash    string `json:"-"`
	Created int64  `json:"created"`
	Prefs   string `json:"-"` // their choices of the pages, as JSON
	// Renew says the password is one given, admin's first: it is to be
	// changed before anything else
	Renew bool `json:"renew,omitempty"`
	// Operator says the user works with instances only: they do not
	// deploy code, run a draft or change people. Whoever deploys can run
	// anything on the daemon's host, through a step
	Operator bool `json:"operator,omitempty"`
}

// Session is a person signed in, by the hash of what their cookie holds.
type Session struct {
	ID       string
	User     string
	Created  int64
	Expires  int64
	Renew    bool // the user's password is to be changed first
	Operator bool // the user works with instances only
}

// Draft is a file of a program written in the console, not deployed yet.
type Draft struct {
	Program string `json:"program"`
	File    string `json:"file"`
	Text    string `json:"text,omitempty"`
	Updated int64  `json:"updated"`
	Author  string `json:"author,omitempty"`
}

// Happened is an instance reaching a status, once: when it was first seen
// waiting or running, when it ended, when it stopped.
type Happened struct {
	Instance string
	Birth    string
	Status   string
	Flow     string
	Program  string
	At       int64
}

// StepRuns is how many times a step left, in an hour (UTC, 2006-01-02T15)
// or over a span, for a flow.
type StepRuns struct {
	Hour string `json:"hour,omitempty"`
	Flow string `json:"flow"`
	Step string `json:"step"`
	Runs int64  `json:"runs"`
}

// ErrNoUser is a user that is not there.
var ErrNoUser = errors.New("no such user")

const keepSQLite = `
CREATE TABLE IF NOT EXISTS console_users (
    name    TEXT PRIMARY KEY,
    hash    TEXT NOT NULL,
    created INTEGER NOT NULL,
    prefs   TEXT NOT NULL DEFAULT '',
    renew   INTEGER NOT NULL DEFAULT 0,
    operator INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS console_sessions (
    id      TEXT PRIMARY KEY,
    who     TEXT NOT NULL,
    created INTEGER NOT NULL,
    expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS console_drafts (
    program TEXT NOT NULL,
    file    TEXT NOT NULL,
    body    TEXT NOT NULL,
    updated INTEGER NOT NULL,
    author  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (program, file)
);
CREATE TABLE IF NOT EXISTS console_happened (
    instance TEXT NOT NULL,
    birth    TEXT NOT NULL,
    status   TEXT NOT NULL,
    flow     TEXT NOT NULL,
    program  TEXT NOT NULL,
    at       INTEGER NOT NULL,
    PRIMARY KEY (instance, birth, status)
);
CREATE INDEX IF NOT EXISTS console_happened_at ON console_happened(at);
CREATE TABLE IF NOT EXISTS console_steps (
    hour TEXT NOT NULL,
    flow TEXT NOT NULL,
    step TEXT NOT NULL,
    runs INTEGER NOT NULL,
    PRIMARY KEY (hour, flow, step)
);
`

const keepPostgres = `
CREATE TABLE IF NOT EXISTS console_users (
    name    TEXT COLLATE "C" PRIMARY KEY,
    hash    TEXT NOT NULL,
    created BIGINT NOT NULL,
    prefs   TEXT NOT NULL DEFAULT '',
    renew   INTEGER NOT NULL DEFAULT 0,
    operator INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS console_sessions (
    id      TEXT PRIMARY KEY,
    who     TEXT NOT NULL,
    created BIGINT NOT NULL,
    expires BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS console_drafts (
    program TEXT COLLATE "C" NOT NULL,
    file    TEXT COLLATE "C" NOT NULL,
    body    TEXT NOT NULL,
    updated BIGINT NOT NULL,
    author  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (program, file)
);
CREATE TABLE IF NOT EXISTS console_happened (
    instance TEXT COLLATE "C" NOT NULL,
    birth    TEXT NOT NULL,
    status   TEXT NOT NULL,
    flow     TEXT NOT NULL,
    program  TEXT NOT NULL,
    at       BIGINT NOT NULL,
    PRIMARY KEY (instance, birth, status)
);
CREATE INDEX IF NOT EXISTS console_happened_at ON console_happened(at);
CREATE TABLE IF NOT EXISTS console_steps (
    hour TEXT NOT NULL,
    flow TEXT NOT NULL,
    step TEXT NOT NULL,
    runs BIGINT NOT NULL,
    PRIMARY KEY (hour, flow, step)
);
`

const keepMySQL = `
CREATE TABLE IF NOT EXISTS console_users (
    name    VARCHAR(255) PRIMARY KEY,
    hash    TEXT NOT NULL,
    created BIGINT NOT NULL,
    prefs   TEXT NOT NULL,
    renew   INTEGER NOT NULL DEFAULT 0,
    operator INTEGER NOT NULL DEFAULT 0
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE IF NOT EXISTS console_sessions (
    id      VARCHAR(128) PRIMARY KEY,
    who     VARCHAR(255) NOT NULL,
    created BIGINT NOT NULL,
    expires BIGINT NOT NULL
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE IF NOT EXISTS console_drafts (
    program VARCHAR(255) NOT NULL,
    file    VARCHAR(255) NOT NULL,
    body    LONGTEXT NOT NULL,
    updated BIGINT NOT NULL,
    author  VARCHAR(255) NOT NULL DEFAULT '',
    PRIMARY KEY (program, file)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE IF NOT EXISTS console_happened (
    instance VARCHAR(255) NOT NULL,
    birth    VARCHAR(64) NOT NULL,
    status   VARCHAR(32) NOT NULL,
    flow     VARCHAR(255) NOT NULL,
    program  VARCHAR(255) NOT NULL,
    at       BIGINT NOT NULL,
    PRIMARY KEY (instance, birth, status),
    INDEX console_happened_at (at)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE TABLE IF NOT EXISTS console_steps (
    hour VARCHAR(16) NOT NULL,
    flow VARCHAR(255) NOT NULL,
    step VARCHAR(255) NOT NULL,
    runs BIGINT NOT NULL,
    PRIMARY KEY (hour, flow, step)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
`

// OpenKeep makes the console's tables in a database, if they are not
// there, and keeps there from then on.
func OpenKeep(db *sql.DB, kind sqldb.Kind) (*Keep, error) {
	schema := keepSQLite
	switch kind {
	case sqldb.Postgres:
		schema = keepPostgres
	case sqldb.MySQL:
		schema = keepMySQL
	}
	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			return nil, fmt.Errorf("cannot make the tables of the console: %v", err)
		}
	}
	// Tables made before a column was: refused when it is there already
	_, _ = db.Exec(`ALTER TABLE console_users ADD COLUMN renew INTEGER NOT NULL DEFAULT 0`)
	// Users from before roles were changed code and people: admins still
	_, _ = db.Exec(`ALTER TABLE console_users ADD COLUMN operator INTEGER NOT NULL DEFAULT 0`)
	return &Keep{db: db, kind: kind}, nil
}

func (k *Keep) q(query string) string { return k.kind.Rebind(query) }

// flag is a yes or no as the tables keep it: 1 or 0.
func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upsert writes an insert that replaces the row of its key, as the kind
// of database does: set lists the columns replaced.
func (k *Keep) upsert(table, columns, key string, set ...string) string {
	n := strings.Count(columns, ",") + 1
	marks := strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ", table, columns, marks)
	var sets []string
	for _, c := range set {
		if k.kind == sqldb.MySQL {
			sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", c, c))
		} else {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", c, c))
		}
	}
	if k.kind == sqldb.MySQL {
		return k.q(query + "ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", "))
	}
	return k.q(query + "ON CONFLICT (" + key + ") DO UPDATE SET " + strings.Join(sets, ", "))
}

// insertOnce writes an insert that leaves a row of its key as it is.
func (k *Keep) insertOnce(table, columns, key string) string {
	n := strings.Count(columns, ",") + 1
	marks := strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ", table, columns, marks)
	if k.kind == sqldb.MySQL {
		// The first column of the key set to itself changes nothing
		first, _, _ := strings.Cut(key, ",")
		return query + "ON DUPLICATE KEY UPDATE " + first + " = " + first
	}
	return k.q(query + "ON CONFLICT (" + key + ") DO NOTHING")
}

// Users are the people who sign in, by name.
func (k *Keep) Users() ([]User, error) {
	rows, err := k.db.Query(`SELECT name, hash, created, prefs, renew, operator FROM console_users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Name, &u.Hash, &u.Created, &u.Prefs, &u.Renew, &u.Operator); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers is how many people sign in: none before the console is set up.
func (k *Keep) CountUsers() (int, error) {
	var n int
	err := k.db.QueryRow(`SELECT COUNT(*) FROM console_users`).Scan(&n)
	return n, err
}

// User is a person, by name.
func (k *Keep) User(name string) (User, error) {
	var u User
	err := k.db.QueryRow(k.q(`SELECT name, hash, created, prefs, renew, operator FROM console_users WHERE name = ?`), name).Scan(&u.Name, &u.Hash, &u.Created, &u.Prefs, &u.Renew, &u.Operator)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNoUser
	}
	return u, err
}

// AddUser adds a person; one of that name is refused.
func (k *Keep) AddUser(u User) error {
	if _, err := k.User(u.Name); err == nil {
		return fmt.Errorf("there is a user %s already", u.Name)
	}
	_, err := k.db.Exec(k.q(`INSERT INTO console_users (name, hash, created, prefs, renew, operator) VALUES (?, ?, ?, ?, ?, ?)`), u.Name, u.Hash, u.Created, u.Prefs, flag(u.Renew), flag(u.Operator))
	return err
}

// DefaultAdmin is the user a console starts with: admin, password admin,
// to be changed at the first sign in.
const DefaultAdmin = "admin"

// EnsureAdmin gives a console that has no user its first: admin, whose
// password, admin, is changed before anything else is done. It says
// whether it made it.
func (k *Keep) EnsureAdmin(now int64) (bool, error) {
	n, err := k.CountUsers()
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := HashPassword(DefaultAdmin)
	if err != nil {
		return false, err
	}
	return true, k.AddUser(User{Name: DefaultAdmin, Hash: hash, Created: now, Renew: true})
}

// SetPassword gives a person another password, theirs from then on
// unless renew says it is to be changed again: their sessions end.
func (k *Keep) SetPassword(name, hash string, renew bool) error {
	res, err := k.db.Exec(k.q(`UPDATE console_users SET hash = ?, renew = ? WHERE name = ?`), hash, flag(renew), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoUser
	}
	_, err = k.db.Exec(k.q(`DELETE FROM console_sessions WHERE who = ?`), name)
	return err
}

// SetPrefs keeps a person's choices of the pages.
func (k *Keep) SetPrefs(name, prefs string) error {
	_, err := k.db.Exec(k.q(`UPDATE console_users SET prefs = ? WHERE name = ?`), prefs, name)
	return err
}

// RemoveUser takes a person away, and their sessions.
func (k *Keep) RemoveUser(name string) error {
	res, err := k.db.Exec(k.q(`DELETE FROM console_users WHERE name = ?`), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoUser
	}
	_, err = k.db.Exec(k.q(`DELETE FROM console_sessions WHERE who = ?`), name)
	return err
}

// PutSession keeps a session.
func (k *Keep) PutSession(s Session) error {
	_, err := k.db.Exec(k.q(`INSERT INTO console_sessions (id, who, created, expires) VALUES (?, ?, ?, ?)`), s.ID, s.User, s.Created, s.Expires)
	return err
}

// Session is a session by its id, if it has not expired by now.
func (k *Keep) Session(id string, now int64) (Session, bool) {
	var s Session
	err := k.db.QueryRow(k.q(`SELECT s.id, s.who, s.created, s.expires, COALESCE(u.renew, 0), COALESCE(u.operator, 1) FROM console_sessions s
		LEFT JOIN console_users u ON u.name = s.who WHERE s.id = ?`), id).Scan(&s.ID, &s.User, &s.Created, &s.Expires, &s.Renew, &s.Operator)
	if err != nil || s.Expires <= now {
		return s, false
	}
	return s, true
}

// EndSession ends a session, and those that expired by now.
func (k *Keep) EndSession(id string, now int64) error {
	_, err := k.db.Exec(k.q(`DELETE FROM console_sessions WHERE id = ? OR expires <= ?`), id, now)
	return err
}

// Drafts are the files of the programs written in the console; of one
// program, or of all, without their text when bodies is false.
func (k *Keep) Drafts(program string, bodies bool) ([]Draft, error) {
	columns := `program, file, '', updated, author`
	if bodies {
		columns = `program, file, body, updated, author`
	}
	query := `SELECT ` + columns + ` FROM console_drafts`
	var args []any
	if program != "" {
		query += ` WHERE program = ?`
		args = append(args, program)
	}
	rows, err := k.db.Query(k.q(query+` ORDER BY program, file`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		var d Draft
		if err := rows.Scan(&d.Program, &d.File, &d.Text, &d.Updated, &d.Author); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PutDraft keeps a file of a program written in the console.
func (k *Keep) PutDraft(d Draft) error {
	_, err := k.db.Exec(k.upsert("console_drafts", "program, file, body, updated, author", "program, file", "body", "updated", "author"),
		d.Program, d.File, d.Text, d.Updated, d.Author)
	return err
}

// RemoveDraft takes a file of a draft away; with file "", every file of
// the program.
func (k *Keep) RemoveDraft(program, file string) error {
	if file == "" {
		_, err := k.db.Exec(k.q(`DELETE FROM console_drafts WHERE program = ?`), program)
		return err
	}
	_, err := k.db.Exec(k.q(`DELETE FROM console_drafts WHERE program = ? AND file = ?`), program, file)
	return err
}

// Record keeps that an instance reached a status, the first time only:
// daemons that share a store all see it, and keep it once.
func (k *Keep) Record(h Happened) error {
	_, err := k.db.Exec(k.insertOnce("console_happened", "instance, birth, status, flow, program, at", "instance, birth, status"),
		h.Instance, h.Birth, h.Status, h.Flow, h.Program, h.At)
	return err
}

// Since is what happened from a time on, the first first.
func (k *Keep) Since(at int64) ([]Happened, error) {
	rows, err := k.db.Query(k.q(`SELECT instance, birth, status, flow, program, at FROM console_happened WHERE at >= ? ORDER BY at`), at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Happened
	for rows.Next() {
		var h Happened
		if err := rows.Scan(&h.Instance, &h.Birth, &h.Status, &h.Flow, &h.Program, &h.At); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CountStep counts n runs of a step that left, in its hour.
func (k *Keep) CountStep(hour, flow, step string, n int64) error {
	query := k.q(`INSERT INTO console_steps (hour, flow, step, runs) VALUES (?, ?, ?, ?) ON CONFLICT (hour, flow, step) DO UPDATE SET runs = console_steps.runs + excluded.runs`)
	if k.kind == sqldb.MySQL {
		query = `INSERT INTO console_steps (hour, flow, step, runs) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE runs = runs + VALUES(runs)`
	}
	_, err := k.db.Exec(query, hour, flow, step, n)
	return err
}

// Steps are the steps that left from an hour on.
func (k *Keep) Steps(fromHour string) ([]StepRuns, error) {
	rows, err := k.db.Query(k.q(`SELECT hour, flow, step, runs FROM console_steps WHERE hour >= ? ORDER BY hour`), fromHour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepRuns
	for rows.Next() {
		var s StepRuns
		if err := rows.Scan(&s.Hour, &s.Flow, &s.Step, &s.Runs); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
