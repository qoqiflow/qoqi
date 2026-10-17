package flowrt_test

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/sqldb"
)

// place is a kind of database a store is kept in: a file of SQLite
// always, and a server of Postgres or of MySQL when QOQI_TEST_POSTGRES
// or QOQI_TEST_MYSQL gives its address:
//
//	QOQI_TEST_POSTGRES=postgres://qoqi:qoqi@127.0.0.1:5432/qoqi
//	QOQI_TEST_MYSQL=mysql://root:qoqi@127.0.0.1:3306/qoqi
//
// On a server, each store of a test is tables of their own, dropped once
// the test is over.
type place struct {
	name string
	// fresh is where no store is yet
	fresh func(t *testing.T) string
	// open opens the store there, as a process that starts does
	open func(t *testing.T, at string) *flowrt.SQL
}

func places(t *testing.T) []place {
	out := []place{{
		name:  "sqlite",
		fresh: func(t *testing.T) string { return filepath.Join(t.TempDir(), "flows.db") },
		open: func(t *testing.T, at string) *flowrt.SQL {
			t.Helper()
			store, err := flowrt.OpenSQLite(at)
			if err != nil {
				t.Fatal(err)
			}
			return store
		},
	}}
	for _, server := range []struct{ name, env string }{{"postgres", "QOQI_TEST_POSTGRES"}, {"mysql", "QOQI_TEST_MYSQL"}} {
		address := os.Getenv(server.env)
		if address == "" {
			continue
		}
		out = append(out, place{
			name: server.name,
			fresh: func(t *testing.T) string {
				t.Helper()
				var b [4]byte
				rand.Read(b[:])
				at := "t" + hex.EncodeToString(b[:])
				t.Cleanup(func() { dropTables(t, address, at+"_") })
				return at
			},
			open: func(t *testing.T, at string) *flowrt.SQL {
				t.Helper()
				root, err := flowrt.OpenStore(address)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { root.Close() })
				store, err := root.Program(at)
				if err != nil {
					t.Fatal(err)
				}
				return store
			},
		})
	}
	return out
}

// openFresh is a store where none was, in each place.
func openFresh(t *testing.T) map[string]*flowrt.SQL {
	out := map[string]*flowrt.SQL{}
	for _, p := range places(t) {
		store := p.open(t, p.fresh(t))
		t.Cleanup(func() { store.Close() })
		out[p.name] = store
	}
	return out
}

// dropTables drops the tables whose names start with a prefix.
func dropTables(t *testing.T, address, prefix string) {
	db, kind, err := sqldb.Open(address)
	if err != nil {
		t.Errorf("dropping the tables of %s: %v", prefix, err)
		return
	}
	defer db.Close()
	query := `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name LIKE ?`
	if kind == sqldb.Postgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name LIKE $1`
	}
	rows, err := db.Query(query, prefix+"%")
	if err != nil {
		t.Errorf("dropping the tables of %s: %v", prefix, err)
		return
	}
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	rows.Close()
	for _, name := range names {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	}
}
