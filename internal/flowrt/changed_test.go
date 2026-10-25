package flowrt_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/sqldb"
)

func idsOf(instances []*flowrt.Instance) map[string]bool {
	out := map[string]bool{}
	for _, inst := range instances {
		out[inst.ID] = true
	}
	return out
}

// tickedOn waits until the wall clock has moved on: a change stamped after it
// is seen as after.
func tickedOn() int64 {
	at := time.Now().UnixMilli()
	for time.Now().UnixMilli() <= at {
		time.Sleep(time.Millisecond)
	}
	return time.Now().UnixMilli()
}

// What a daemon looks at is what was written since it last looked: a
// save, a cancellation, a message that wakes, a start; not the others.
func TestChangedSince(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			store := p.open(t, p.fresh(t))
			defer store.Close()

			for _, id := range []string{"a", "b", "c", "d"} {
				if _, _, err := store.Create(&flowrt.Instance{ID: id, Birth: "1", Flow: "F", Status: flowrt.Waiting, WakeAt: 1 << 50}); err != nil {
					t.Fatal(err)
				}
			}

			since := tickedOn()
			if got := idsOf(store.ChangedSince(since)); len(got) != 0 {
				t.Fatalf("nothing written since: got %v", got)
			}

			a, _, _ := store.Get("a")
			a.State = []byte(`"moved on"`)
			if err := store.Save(a); err != nil {
				t.Fatal(err)
			}
			if err := store.Cancel("b", "withdrawn", time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Send("c", "messages", "k1", []byte(`"hello"`), 8); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Create(&flowrt.Instance{ID: "e", Birth: "1", Flow: "F", Status: flowrt.Waiting}); err != nil {
				t.Fatal(err)
			}

			got := idsOf(store.ChangedSince(since))
			for _, id := range []string{"a", "b", "c", "e"} {
				if !got[id] {
					t.Errorf("%s was written since: got %v", id, got)
				}
			}
			if got["d"] {
				t.Errorf("d was not written since: got %v", got)
			}

			// A lease taken is no change
			since = tickedOn()
			if _, err := store.Claim("d", time.Now().UnixMilli(), time.Now().Add(time.Minute).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if got := idsOf(store.ChangedSince(since)); got["d"] {
				t.Errorf("a lease is no change: got %v", got)
			}
		})
	}
}

// A file made before the column has it added, and its instances are
// found again by a look at them all: none changed since a moment.
func TestChangedSinceOnAnOldFile(t *testing.T) {
	at := filepath.Join(t.TempDir(), "flows.db")
	db, err := sql.Open("sqlite", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE instances (id TEXT PRIMARY KEY, flow TEXT NOT NULL, status TEXT NOT NULL, wake_at INTEGER NOT NULL, row TEXT NOT NULL);
		INSERT INTO instances (id, flow, status, wake_at, row) VALUES ('old', 'F', 'waiting', 0, '{"birth": "1"}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	store, err := flowrt.OpenSQLite(at)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if got := idsOf(store.List("", "")); !got["old"] {
		t.Fatalf("the old instance is there: got %v", got)
	}
	if got := idsOf(store.ChangedSince(1)); got["old"] {
		t.Errorf("an instance never stamped is not changed since: got %v", got)
	}

	since := tickedOn()
	old, _, _ := store.Get("old")
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(store.ChangedSince(since)); !got["old"] {
		t.Errorf("written again, it is changed since: got %v", got)
	}
}

// Memory keeps no time of change: it gives them all, which is more, and right.
func TestChangedSinceInMemory(t *testing.T) {
	store := flowrt.NewMemory()
	store.Create(&flowrt.Instance{ID: "a", Flow: "F", Status: flowrt.Waiting})
	if got := idsOf(store.ChangedSince(time.Now().Add(time.Hour).UnixMilli())); !got["a"] {
		t.Errorf("Memory gives every instance: got %v", got)
	}
}

// The tables of a program have the index too, under the program's name:
// not taken for the index of the root's tables, which has the same name.
func TestChangedIndexOfAProgram(t *testing.T) {
	at := filepath.Join(t.TempDir(), "flows.db")
	root, err := flowrt.OpenSQLite(at)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Program("main"); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", at)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, index := range []string{"instances_changed", "main_instances_changed"} {
		var table string
		if err := db.QueryRow(`SELECT tbl_name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&table); err != nil {
			t.Errorf("no index %s: %v", index, err)
		}
	}
}

// On a server, a program's table made before the column has it added, and
// its index: the migration of a store in use.
func TestChangedOnOldServerTables(t *testing.T) {
	for _, server := range []struct{ name, env, table string }{
		{"postgres", "QOQI_TEST_POSTGRES", `CREATE TABLE %s_instances (id TEXT COLLATE "C" PRIMARY KEY, flow TEXT NOT NULL, status TEXT NOT NULL, wake_at BIGINT NOT NULL, row TEXT NOT NULL, cancel TEXT NOT NULL DEFAULT '', awaits TEXT NOT NULL DEFAULT '', held_by TEXT NOT NULL DEFAULT '', held_until BIGINT NOT NULL DEFAULT 0, held_as TEXT NOT NULL DEFAULT '')`},
		{"mysql", "QOQI_TEST_MYSQL", "CREATE TABLE %s_instances (id VARCHAR(255) PRIMARY KEY, flow VARCHAR(255) NOT NULL, status VARCHAR(16) NOT NULL, wake_at BIGINT NOT NULL, `row` LONGTEXT NOT NULL, cancel TEXT NOT NULL DEFAULT (''), awaits MEDIUMTEXT NOT NULL DEFAULT (''), held_by VARCHAR(255) NOT NULL DEFAULT '', held_until BIGINT NOT NULL DEFAULT 0, held_as VARCHAR(64) NOT NULL DEFAULT '') CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"},
	} {
		address := os.Getenv(server.env)
		if address == "" {
			continue
		}
		t.Run(server.name, func(t *testing.T) {
			var b [4]byte
			rand.Read(b[:])
			prefix := "old" + hex.EncodeToString(b[:])
			t.Cleanup(func() { dropTables(t, address, prefix+"_") })

			raw, _, err := sqldb.Open(address)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.Exec(fmt.Sprintf(server.table, prefix)); err != nil {
				t.Fatal(err)
			}

			root, err := flowrt.OpenStore(address)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			store, err := root.Program(prefix)
			if err != nil {
				t.Fatal(err)
			}

			since := tickedOn()
			if _, _, err := store.Create(&flowrt.Instance{ID: "a", Birth: "1", Flow: "F", Status: flowrt.Waiting}); err != nil {
				t.Fatal(err)
			}
			if got := idsOf(store.ChangedSince(since)); !got["a"] {
				t.Errorf("the column is added: got %v", got)
			}

			var indexes int
			query := `SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`
			if server.name == "mysql" {
				query = `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND index_name = ?`
			}
			if err := raw.QueryRow(query, prefix+"_instances_changed").Scan(&indexes); err != nil || indexes == 0 {
				t.Errorf("the index %s_instances_changed is made: %d, %v", prefix, indexes, err)
			}
		})
	}
}
