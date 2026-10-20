package flow_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/qoqiflow/qoqi/pkg/flow"
)

// mysqlDriver stands for a MySQL driver, with no server: it keeps the
// queries it is given, so that a test reads how they quote their names.
// Its type's name tells the Keeper it is MySQL's, as go-sql-driver's does.
type mysqlDriver struct {
	mu      sync.Mutex
	queries []string
}

func (d *mysqlDriver) Open(string) (driver.Conn, error) { return &recorded{d}, nil }

func (d *mysqlDriver) said() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.queries...)
}

// otherDriver keeps its queries too, under a name that tells nothing.
type otherDriver struct{ mysqlDriver }

func (d *otherDriver) Open(string) (driver.Conn, error) { return &recorded{&d.mysqlDriver}, nil }

type recorded struct{ d *mysqlDriver }

func (c *recorded) Prepare(query string) (driver.Stmt, error) {
	c.d.mu.Lock()
	c.d.queries = append(c.d.queries, query)
	c.d.mu.Unlock()
	return statement{}, nil
}
func (c *recorded) Close() error              { return nil }
func (c *recorded) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type statement struct{}

func (statement) Close() error                               { return nil }
func (statement) NumInput() int                              { return -1 }
func (statement) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (statement) Query([]driver.Value) (driver.Rows, error)  { return nothing{}, nil }

type nothing struct{}

func (nothing) Columns() []string         { return []string{"state", "answer", "beat"} }
func (nothing) Close() error              { return nil }
func (nothing) Next([]driver.Value) error { return io.EOF }

var registered sync.Once

// A Keeper on MySQL quotes the key column with backticks, key being a
// reserved word there: told by the driver's type, or said by MySQL.
func TestKeeperQuotesKeyOnMySQL(t *testing.T) {
	my, other := &mysqlDriver{}, &otherDriver{}
	registered.Do(func() {
		sql.Register("flowtest-mysql", my)
		sql.Register("flowtest-other", other)
	})
	for _, c := range []struct {
		name   string
		keeper func() (*flow.Keeper, func() []string)
	}{
		{"told by the driver", func() (*flow.Keeper, func() []string) {
			db, _ := sql.Open("flowtest-mysql", "")
			t.Cleanup(func() { db.Close() })
			return &flow.Keeper{DB: db}, my.said
		}},
		{"said by MySQL", func() (*flow.Keeper, func() []string) {
			db, _ := sql.Open("flowtest-other", "")
			t.Cleanup(func() { db.Close() })
			return &flow.Keeper{DB: db, MySQL: true}, other.said
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			k, said := c.keeper()
			ctx := context.Background()
			if err := k.Setup(ctx); err != nil {
				t.Fatal(err)
			}
			if err := k.Forget(ctx, "a"); err != nil {
				t.Fatal(err)
			}
			q := flow.NewQueue("billing", "s1")
			q.Handle("wire-1", func(context.Context, *flow.Task) flow.Answer { return flow.Answer{} })
			q.KeepStarted(k)
			probe := taskOf("wire-1", "a")
			probe.Probe = true
			if _, answer := post(t, q, "POST", probe); answer != `{"unseen":true}` {
				t.Errorf("a probe: %s", answer)
			}
			queries := said()
			if len(queries) < 3 {
				t.Fatalf("setup, forget and a probe each query: %q", queries)
			}
			for _, query := range queries {
				if !strings.Contains(query, "`key`") || strings.Contains(query, `"key"`) {
					t.Errorf("the key column is quoted with backticks: %s", query)
				}
			}
		})
	}
}
