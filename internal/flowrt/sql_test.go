package flowrt_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// stoppedEverywhereOnDisk is stoppedEverywhere on every kind of
// database a store is kept in: the process that stops closes nothing,
// and the one that starts again opens the store anew, as a process that
// starts does.
func stoppedEverywhereOnDisk(t *testing.T, chunk *vm.Chunk, flow, fields string, check func(k int, out string, inst *flowrt.Instance)) {
	t.Helper()
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			stoppedEverywhereIn(t, p, chunk, flow, fields, check)
		})
	}
}

func stoppedEverywhereIn(t *testing.T, p place, chunk *vm.Chunk, flow, fields string, check func(k int, out string, inst *flowrt.Instance)) {
	t.Helper()
	run := func(at string, failAt int, out *bytes.Buffer) error {
		store := p.open(t, at)
		defer store.Close()
		store.FailAt = failAt
		rt := flowrt.New(chunk, store, out)
		if _, err := rt.Start("x", flow, []byte(fields)); err != nil {
			return err
		}
		return rt.RunUntilIdle(10 * time.Second)
	}
	result := func(at string) *flowrt.Instance {
		store := p.open(t, at)
		defer store.Close()
		inst, ok := store.Get("x")
		if !ok {
			t.Fatalf("no instance in %s", at)
		}
		return inst
	}

	plain := p.fresh(t)
	var plainOut bytes.Buffer
	if err := run(plain, 0, &plainOut); err != nil {
		t.Fatal(err)
	}
	check(0, plainOut.String(), result(plain))

	// How many writes a run makes
	counted := p.open(t, p.fresh(t))
	rt := flowrt.New(chunk, counted, &bytes.Buffer{})
	if _, err := rt.Start("x", flow, []byte(fields)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	writes := counted.Writes()
	counted.Close()

	for k := 1; k <= writes; k++ {
		at := p.fresh(t)
		var out bytes.Buffer
		// A run that wrote less this time, as the clock decided, ends
		// without the stop: it has to end the same all the same
		if err := run(at, k, &out); err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		if err := run(at, 0, &out); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		check(k, out.String(), result(at))
	}
}

func TestSQLiteOrderSurvivesAStopAtEveryWrite(t *testing.T) {
	chunk := compiled(t, order)
	stoppedEverywhereOnDisk(t, chunk, "Order", orderFields, func(k int, out string, inst *flowrt.Instance) {
		if inst.Status != flowrt.Done || string(inst.Result) != shipped {
			t.Fatalf("stopped at write %d: expected done with %s, got %s %s %s", k, shipped, inst.Status, inst.Result, inst.Stopped)
		}
		distinct := map[string]int{}
		for _, line := range worldLines(out) {
			distinct[line]++
		}
		twice := 0
		for line, n := range distinct {
			if n > 2 {
				t.Errorf("stopped at write %d: %q done %d times", k, line, n)
			}
			if n == 2 {
				twice++
			}
		}
		if len(distinct) != 5 || twice > 1 {
			t.Errorf("stopped at write %d: expected the five steps, one at most twice: %v", k, worldLines(out))
		}
	})
}

func TestSQLiteSettledAndRetries(t *testing.T) {
	stoppedEverywhereOnDisk(t, compiled(t, wiring), "Payout", `{"$type": "Payout", "to": "ada", "cents": 500}`, func(k int, out string, inst *flowrt.Instance) {
		if lines := worldLines(out); len(lines) != 1 {
			t.Errorf("stopped at write %d: the wire went %d times", k, len(lines))
		}
		if r := string(inst.Result); r != `"for ada paid ref-500"` && r != `"for ada ask the bank"` {
			t.Errorf("stopped at write %d: got %s %s", k, r, inst.Stopped)
		}
	})
	stoppedEverywhereOnDisk(t, compiled(t, retrying), "Fetch", `{"$type": "Fetch", "url": "example.com"}`, func(k int, out string, inst *flowrt.Instance) {
		if r := string(inst.Result); r != `"got example.com on attempt 3"` && r != `"got example.com on attempt 4"` {
			t.Errorf("stopped at write %d: got %s %s", k, r, inst.Stopped)
		}
	})
}

// An instance asleep in the file is there for the next process: closed
// while it sleeps, opened again later, it goes on.
func TestSQLiteKeepsASleepingInstance(t *testing.T) {
	chunk := compiled(t, `
import std.flow.{Flow};
import std.time;

struct Nap { name: String; }

impl Flow<String> for Nap {
    fn run() -> String {
        let before = greet(self.name);
        wait time.after(150.milliseconds());
        return before + " and back";
    }
}

step fn greet(name: String) -> String {
    println("WORLD greet", name);
    return "hello " + name;
}

fn main() {}
`)
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) { keepsASleepingInstance(t, p, chunk) })
	}
}

func keepsASleepingInstance(t *testing.T, p place, chunk *vm.Chunk) {
	at := p.fresh(t)
	store := p.open(t, at)
	var out bytes.Buffer
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("nap", "Nap", []byte(`{"$type": "Nap", "name": "ada"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("nap")
	if inst.Status != flowrt.Waiting || inst.State == nil || inst.WakeAt <= time.Now().UnixMilli() {
		t.Fatalf("expected it asleep, written, due later; got %s, state %d bytes, wake %d", inst.Status, len(inst.State), inst.WakeAt)
	}
	store.Close()

	time.Sleep(160 * time.Millisecond)
	again := p.open(t, at)
	defer again.Close()
	if err := flowrt.New(chunk, again, &out).RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ = again.Get("nap")
	if inst.Status != flowrt.Done || string(inst.Result) != `"hello ada and back"` {
		t.Errorf("expected it done, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
	}
	if got := len(worldLines(out.String())); got != 1 {
		t.Errorf("expected the greeting once, got %d", got)
	}
}

// Two programs in one file, each on tables of its own: an instance of
// one name in each, neither seeing the other's, both found again when
// the file is opened anew.
func TestProgramsInOneFile(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) { programsInOneStore(t, p) })
	}
}

func programsInOneStore(t *testing.T, p place) {
	at := p.fresh(t)
	file := p.open(t, at)
	shop, err := file.Program("shop")
	if err != nil {
		t.Fatal(err)
	}
	bills, err := file.Program("billing.v2")
	if err != nil {
		t.Fatal(err)
	}
	runFlow(t, flowrt.New(compiled(t, order), shop, &bytes.Buffer{}), shop, "x", "Order", orderFields)
	runFlow(t, flowrt.New(compiled(t, billing), bills, &bytes.Buffer{}), bills, "x", "Billing", `{"months": 2}`)
	bills.Close() // a program's store leaves the file open
	file.Close()

	again := p.open(t, at)
	defer again.Close()
	shop, _ = again.Program("shop")
	bills, _ = again.Program("billing.v2")
	a, _ := shop.Get("x")
	b, _ := bills.Get("x")
	if a == nil || b == nil || a.Flow != "Order" || b.Flow != "Billing" || string(b.Result) != "201" {
		t.Fatalf("expected an Order and a Billing under the same name, got %+v %+v", a, b)
	}
	if len(shop.List("", "")) != 1 || len(again.List("", "")) != 0 {
		t.Errorf("expected each program's instances on its own tables")
	}
}
