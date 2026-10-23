package flowrt_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// waiting has a slow step that writes in the store, then waits for the
// world, then writes again; and a quick one that writes in the store
// five times meanwhile.
const waiting = `
import std.flow.{Flow};
import std.flow;
import std.http;
import std.sql;
import std.sql.{Value};

struct Slow { url: String; }
struct Quick { n: Int; }

impl Flow<String> for Slow {
    fn run() -> String {
        return slowly(self.url);
    }
}

impl Flow<Int> for Quick {
    fn run() -> Int {
        let total = 0;

        for i in 0..self.n {
            total = total + quickly(i);
        }

        println("WORLD quick done");
        return total;
    }
}

step fn slowly(url: String) -> String {
    let store = flow.store().expect("the store");
    let before = store.one("SELECT COUNT(*) AS n FROM ledger").expect("a count");
    let n = before?.int("n") ?? -1;
    store.exec("INSERT INTO ledger (who, n) VALUES (?, ?)", [Value.Text("slow"), Value.Int(n)]).expect("a line");
    http.get(url).expect("an answer");
    store.exec("UPDATE ledger SET who = ? WHERE who = ?", [Value.Text("slow, answered"), Value.Text("slow")]).expect("the line changed");
    println("WORLD slow saw", n);
    return "saw ${n}";
}

step fn quickly(i: Int) -> Int {
    let store = flow.store().expect("the store");
    store.exec("INSERT INTO marks (i) VALUES (?)", [Value.Int(i)]).expect("a mark");
    return 1;
}

fn main() {}
`

func waitingIn(t *testing.T, store *flowrt.SQL, at string, p place) (string, func(table string) string) {
	t.Helper()
	name := func(table string) string {
		if p.name == "sqlite" {
			return table
		}
		return at + "_" + table
	}
	db, _ := store.Database()
	for _, table := range []string{
		`CREATE TABLE IF NOT EXISTS ` + name("ledger") + ` (who VARCHAR(64) NOT NULL, n BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS ` + name("marks") + ` (i BIGINT NOT NULL)`,
	} {
		if _, err := db.Exec(table); err != nil {
			t.Fatal(err)
		}
	}
	src := strings.ReplaceAll(waiting, "FROM ledger", "FROM "+name("ledger"))
	src = strings.ReplaceAll(src, "INTO ledger", "INTO "+name("ledger"))
	src = strings.ReplaceAll(src, "UPDATE ledger", "UPDATE "+name("ledger"))
	src = strings.ReplaceAll(src, "INTO marks", "INTO "+name("marks"))
	return src, name
}

// A step that has written in the store and waits for the world keeps no
// other step from writing there: the quick instance ends while the slow
// one waits, on every kind of database, SQLite's one writer included.
func TestAStepWaitingKeepsNoneFromTheStore(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			store := p.open(t, at)
			defer store.Close()
			src, _ := waitingIn(t, store, at, p)

			out := &lockedBuffer{}
			release := make(chan struct{})
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-release:
				case <-time.After(5 * time.Second):
				}
				w.Write([]byte("ok"))
			}))
			defer slow.Close()

			rt := flowrt.New(compiled(t, src), store, out)
			rt.Workers = 2
			if _, err := rt.Start("slow", "Slow", []byte(`{"$type": "Slow", "url": "`+slow.URL+`"}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Start("quick", "Quick", []byte(`{"$type": "Quick", "n": 5}`)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- rt.RunUntilIdle(30 * time.Second) }()

			// The slow step waits until the quick instance has ended
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(out.String(), "WORLD quick done") {
				if time.Now().After(deadline) {
					close(release)
					<-done
					t.Fatalf("the quick instance did not end while the slow step waited:\n%s", out.String())
				}
				time.Sleep(5 * time.Millisecond)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			for id, want := range map[string]string{"slow": `"saw 0"`, "quick": "5"} {
				if inst, _, _ := store.Get(id); inst.Status != flowrt.Done || string(inst.Result) != want {
					t.Errorf("%s: expected done with %s, got %s %s %s", id, want, inst.Status, inst.Result, inst.Stopped)
				}
			}
		})
	}
}

// On SQLite, a step reads, waits for the world, and another writes what
// it read meanwhile: when it ends, what it read is not so any more, and
// it runs again from nothing, as a server ends a transaction for another.
// What it wrote the first time is not there; the second run saw the
// other's line.
func TestAStepThatReadWhatChangedRunsAgain(t *testing.T) {
	p := places(t)[0]
	at := p.fresh(t)
	store := p.open(t, at)
	defer store.Close()
	src, _ := waitingIn(t, store, at, p)
	db, _ := store.Database()

	var once sync.Once
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Another writes in the ledger while the step waits, the first time
		once.Do(func() {
			if _, err := db.Exec(`INSERT INTO ledger (who, n) VALUES ('other', 0)`); err != nil {
				t.Error(err)
			}
		})
		w.Write([]byte("ok"))
	}))
	defer slow.Close()

	var out bytes.Buffer
	rt := flowrt.New(compiled(t, src), store, &out)
	runs := 0
	rt.Ran = func(step, key string, once bool) { runs++ }
	if _, err := rt.Start("slow", "Slow", []byte(`{"$type": "Slow", "url": "`+slow.URL+`"}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	if inst, _, _ := store.Get("slow"); inst.Status != flowrt.Done || string(inst.Result) != `"saw 1"` {
		t.Errorf(`expected done with "saw 1", got %s %s %s`, inst.Status, inst.Result, inst.Stopped)
	}
	// The first run is ended at its next statement, before it says
	// anything: what it read is not so any more
	if got := strings.Join(worldLines(out.String()), "|"); got != "WORLD slow saw 1" || runs != 2 {
		t.Errorf("expected the step to run twice, the second seeing 1: ran %d, said %s", runs, got)
	}
	var lines string
	rows, _ := db.Query(`SELECT who || ':' || n FROM ledger ORDER BY who`)
	for rows.Next() {
		var line string
		rows.Scan(&line)
		lines += line + " "
	}
	rows.Close()
	if lines != "other:0 slow, answered:1 " {
		t.Errorf("expected the other's line and the second run's, got %q", lines)
	}
}

// A step that makes its table in the store's transaction, then asks to be
// run again: on MySQL, which commits a transaction for a table made in
// it, the table is made beside it; the rows of the first attempt are gone
// all the same, on every kind of database.
func TestAStepMakesItsTableInTheStore(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			store := p.open(t, at)
			defer store.Close()
			table := "notebook"
			if p.name != "sqlite" {
				table = at + "_notebook"
			}
			src := strings.ReplaceAll(`
import std.flow.{Flow};
import std.flow;
import std.sql;
import std.sql.{Value};
import std.time;

struct Note { text: String; }

impl Flow<Int> for Note {
    fn run() -> Int {
        return write(self.text);
    }
}

step fn write(text: String) -> Int {
    let store = flow.store().expect("the store");
    store.exec("INSERT INTO notebook (text) VALUES (?)", [Value.Text("before " + text)]).expect("a line before");
    store.exec("CREATE TABLE IF NOT EXISTS notebook (text VARCHAR(64) NOT NULL)").expect("the table");
    store.exec("INSERT INTO notebook (text) VALUES (?)", [Value.Text(text)]).expect("a line");

    if flow.attempt() == 1 {
        flow.retry("not yet", 5.milliseconds());
    }

    let n = store.one("SELECT COUNT(*) AS n FROM notebook").expect("a count");
    return n?.int("n") ?? -1;
}

fn main() {}
`, "notebook", table)
			db, _ := store.Database()
			if _, err := db.Exec(`CREATE TABLE ` + table + ` (text VARCHAR(64) NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			rt := flowrt.New(compiled(t, src), store, &out)
			inst := runFlow(t, rt, store, "n", "Note", `{"$type": "Note", "text": "hello"}`)
			if inst.Status != flowrt.Done || string(inst.Result) != "2" {
				t.Errorf("expected done with 2, the lines of one attempt, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
			}
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
			if n != 2 {
				t.Errorf("expected the two lines of the second attempt, got %d", n)
			}
		})
	}
}
