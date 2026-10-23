package vm

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/qoqiflow/qoqi/internal/sqldb"
)

// On SQLite, what a statement changed is its own count: a statement that
// changes no rows says 0, whatever the last one of its connection did.
func TestSQLiteCountsWhatAStatementChanged(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE a (n INT); INSERT INTO a VALUES (1), (2), (3)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, c := range []struct {
		query string
		want  int64
	}{
		{"CREATE TABLE IF NOT EXISTS b (n INT)", 0},
		{"UPDATE a SET n = n + 1 WHERE n > 1", 2},
		{"CREATE INDEX IF NOT EXISTS a_n ON a(n)", 0},
		{"INSERT INTO b VALUES (1)", 1},
	} {
		changed, _, err := sqlRunOn(tx, sqldb.SQLite, c.query, nil, false)
		if err != nil || changed != c.want {
			t.Errorf("%s: %d changed, %v; want %d", c.query, changed, err, c.want)
		}
	}
	tx.Rollback()
	// On a pool, the connection is not known: by the statement's first word
	if changed, _, err := sqlRunOn(db, sqldb.SQLite, "CREATE TABLE IF NOT EXISTS c (n INT)", nil, false); err == nil && changed != 0 {
		t.Errorf("a CREATE on the pool said %d changed", changed)
	}
}

func TestChangesRows(t *testing.T) {
	for query, want := range map[string]bool{
		"INSERT INTO a VALUES (1)":                           true,
		"  update a set n = 1":                               true,
		"-- a note\nDELETE FROM a":                           true,
		"/* why */ REPLACE INTO a VALUES (1)":                true,
		"WITH x AS (SELECT 1) INSERT INTO a SELECT * FROM x": true,
		"CREATE TABLE IF NOT EXISTS a (n INT)":               false,
		"DROP TABLE a":                                       false,
		"PRAGMA foreign_keys = ON":                           false,
	} {
		if got := changesRows(query); got != want {
			t.Errorf("changesRows(%q) = %v", query, got)
		}
	}
}
