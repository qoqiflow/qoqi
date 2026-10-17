package flowrt_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// settling settles invoices in the store's own database: each step writes
// its rows with flow.store(), and its result is kept in the same
// transaction. settle asks to be run again at its first attempt, after
// it wrote: what it wrote then is gone with it.
const settling = `
import std.flow.{Flow};
import std.flow;
import std.sql;
import std.sql.{Value};
import std.time;

struct Billing {
    invoice: String;
    cents: Int;
}

impl Flow<String> for Billing {
    fn run() -> String {
        let receipt = settle(self.invoice, self.cents);
        let note = remember(self.invoice);
        return receipt + ", " + note;
    }
}

step fn settle(invoice: String, cents: Int) -> String {
    let store = flow.store().expect("the store");
    store.exec("INSERT INTO receipts (invoice, cents) VALUES (?, ?)", [Value.Text(invoice), Value.Int(cents)]).expect("a receipt");

    if flow.attempt() == 1 {
        flow.retry("the bank is slow", 5.milliseconds());
    }

    let seen = store.one("SELECT COUNT(*) AS n FROM receipts WHERE invoice = ?", [Value.Text(invoice)]).expect("a count");
    println("WORLD settle", invoice);
    return "receipt for ${invoice}, ${seen?.int("n")} seen";
}

step fn remember(invoice: String) -> String {
    let store = flow.store().expect("the store");
    let again = flow.store().expect("the same store");
    store.exec("INSERT INTO notes (invoice) VALUES (?)", [Value.Text(invoice)]).expect("a note");
    again.exec("UPDATE notes SET invoice = ? WHERE invoice = ?", [Value.Text(invoice + "!"), Value.Text(invoice)]).expect("the note changed");
    return "noted";
}

fn main() {}
`

const settled = `"receipt for inv-1, 1 seen, noted"`

// billingIn is the program on tables of the test's own, made in the
// store's database before it runs, as an application makes its tables.
func billingIn(t *testing.T, store *flowrt.SQL, at string, p place) string {
	t.Helper()
	receipts, notes := "receipts", "notes"
	if p.name != "sqlite" {
		receipts, notes = at+"_receipts", at+"_notes"
	}
	db, _ := store.Database()
	for _, table := range []string{
		`CREATE TABLE IF NOT EXISTS ` + receipts + ` (invoice VARCHAR(64) NOT NULL, cents BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS ` + notes + ` (invoice VARCHAR(64) NOT NULL)`,
	} {
		if _, err := db.Exec(table); err != nil {
			t.Fatal(err)
		}
	}
	src := strings.ReplaceAll(settling, "INTO receipts", "INTO "+receipts)
	src = strings.ReplaceAll(src, "FROM receipts", "FROM "+receipts)
	src = strings.ReplaceAll(src, "INTO notes", "INTO "+notes)
	src = strings.ReplaceAll(src, "UPDATE notes", "UPDATE "+notes)
	return src
}

// rowsIn counts the rows of the test's tables: receipts, then notes.
func rowsIn(t *testing.T, store *flowrt.SQL, at string, p place) (int, int) {
	t.Helper()
	receipts, notes := "receipts", "notes"
	if p.name != "sqlite" {
		receipts, notes = at+"_receipts", at+"_notes"
	}
	db, _ := store.Database()
	var r, n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + receipts).Scan(&r); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + notes + ` WHERE invoice = 'inv-1!'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return r, n
}

// A step's rows and its result are kept in one transaction, on every
// kind of database: the attempt that asked to be run again left nothing,
// and the one that ended left its rows once.
func TestStepsWriteInTheStore(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			store := p.open(t, at)
			defer store.Close()
			var out bytes.Buffer
			rt := flowrt.New(compiled(t, billingIn(t, store, at, p)), store, &out)
			if _, err := rt.Start("x", "Billing", []byte(`{"$type": "Billing", "invoice": "inv-1", "cents": 4200}`)); err != nil {
				t.Fatal(err)
			}
			if err := rt.RunUntilIdle(10 * time.Second); err != nil {
				t.Fatal(err)
			}
			inst, _ := store.Get("x")
			if inst.Status != flowrt.Done || string(inst.Result) != settled {
				t.Fatalf("expected done with %s, got %s %s %s", settled, inst.Status, inst.Result, inst.Stopped)
			}
			if receipts, notes := rowsIn(t, store, at, p); receipts != 1 || notes != 1 {
				t.Errorf("expected one receipt and one note, got %d and %d", receipts, notes)
			}
			if lines := worldLines(out.String()); len(lines) != 1 {
				t.Errorf("expected settle to end once, got %v", lines)
			}
		})
	}
}

// Stopped at every write, and taken up by a process that opens the store
// anew: the receipt and the note are there once each, whichever write
// the process stopped at, since a step's rows go with its result.
func TestStepsWriteInTheStoreOnceThroughEveryStop(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			run := func(at, src string, failAt int, out *bytes.Buffer) error {
				store := p.open(t, at)
				defer store.Close()
				store.FailAt = failAt
				rt := flowrt.New(compiled(t, src), store, out)
				if _, err := rt.Start("x", "Billing", []byte(`{"$type": "Billing", "invoice": "inv-1", "cents": 4200}`)); err != nil {
					return err
				}
				return rt.RunUntilIdle(10 * time.Second)
			}

			counted := p.fresh(t)
			store := p.open(t, counted)
			src := billingIn(t, store, counted, p)
			if err := run(counted, src, 0, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			again := p.open(t, counted)
			rt := flowrt.New(compiled(t, src), again, &bytes.Buffer{})
			if _, err := rt.Start("y", "Billing", []byte(`{"$type": "Billing", "invoice": "inv-2", "cents": 1}`)); err != nil {
				t.Fatal(err)
			}
			if err := rt.RunUntilIdle(10 * time.Second); err != nil {
				t.Fatal(err)
			}
			writes := again.Writes()
			if writes < 6 {
				t.Fatalf("expected a run to write a few times, it wrote %d", writes)
			}

			for k := 1; k <= writes; k++ {
				at := p.fresh(t)
				store := p.open(t, at)
				src := billingIn(t, store, at, p)
				var out bytes.Buffer
				if err := run(at, src, k, &out); err != nil && !errors.Is(err, flowrt.ErrCrash) {
					t.Fatalf("stopping at write %d: %v", k, err)
				}
				if err := run(at, src, 0, &out); err != nil {
					t.Fatalf("after stopping at write %d: %v", k, err)
				}
				inst, _ := store.Get("x")
				if inst.Status != flowrt.Done || string(inst.Result) != settled {
					t.Fatalf("stopped at write %d: expected done with %s, got %s %s %s", k, settled, inst.Status, inst.Result, inst.Stopped)
				}
				if receipts, notes := rowsIn(t, store, at, p); receipts != 1 || notes != 1 {
					t.Errorf("stopped at write %d: expected one receipt and one note, got %d and %d", k, receipts, notes)
				}
			}
		})
	}
}

// Eight workers settle twenty invoices at once, each step in the store's
// transaction: every invoice has its receipt and its note once.
func TestStepsWriteInTheStoreFromManyWorkers(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			store := p.open(t, at)
			defer store.Close()
			rt := flowrt.New(compiled(t, billingIn(t, store, at, p)), store, &lockedBuffer{})
			rt.Workers = 8
			for i := 0; i < 20; i++ {
				fields := fmt.Sprintf(`{"$type": "Billing", "invoice": "inv-%d", "cents": %d}`, i, 100+i)
				if _, err := rt.Start(fmt.Sprintf("b%d", i), "Billing", []byte(fields)); err != nil {
					t.Fatal(err)
				}
			}
			if err := rt.RunUntilIdle(60 * time.Second); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 20; i++ {
				inst, _ := store.Get(fmt.Sprintf("b%d", i))
				want := fmt.Sprintf(`"receipt for inv-%d, 1 seen, noted"`, i)
				if inst.Status != flowrt.Done || string(inst.Result) != want {
					t.Errorf("b%d: expected done with %s, got %s %s %s", i, want, inst.Status, inst.Result, inst.Stopped)
				}
			}
			receipts, notes := "receipts", "notes"
			if p.name != "sqlite" {
				receipts, notes = at+"_receipts", at+"_notes"
			}
			db, _ := store.Database()
			var r, n, distinct int
			db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT invoice) FROM `+receipts).Scan(&r, &distinct)
			db.QueryRow(`SELECT COUNT(*) FROM ` + notes).Scan(&n)
			if r != 20 || distinct != 20 || n != 20 {
				t.Errorf("expected twenty receipts, one each, and twenty notes: %d receipts for %d invoices, %d notes", r, distinct, n)
			}
		})
	}
}

// In memory, as a trial runs, a step's store is a database of the
// runtime's own: what it writes there is there for the next step.
func TestStepsWriteInAStoreInMemory(t *testing.T) {
	src := strings.Replace(settling, `    let store = flow.store().expect("the store");
    store.exec("INSERT INTO receipts`, `    let store = flow.store().expect("the store");
    store.exec("CREATE TABLE IF NOT EXISTS receipts (invoice TEXT, cents INTEGER)").expect("receipts");
    store.exec("CREATE TABLE IF NOT EXISTS notes (invoice TEXT)").expect("notes");
    store.exec("INSERT INTO receipts`, 1)
	store := flowrt.NewMemory()
	rt := flowrt.New(compiled(t, src), store, &bytes.Buffer{})
	inst := runFlow(t, rt, store, "x", "Billing", `{"$type": "Billing", "invoice": "inv-1", "cents": 4200}`)
	if inst.Status != flowrt.Done || string(inst.Result) != settled {
		t.Errorf("expected done with %s, got %s %s %s", settled, inst.Status, inst.Result, inst.Stopped)
	}
}
