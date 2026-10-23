package flowrt_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// An order that holds the stock, takes the payment, sleeps, then books a
// van. The hold is undone by the step that made it; the payment by run,
// once it knows of it. Each cart's id says what goes wrong.
const orders = `
import std.flow;
import std.flow.{Flow};
import std.time;

struct Cart { id: String; amount: Int; }
struct Hold { id: String; }
enum Problem { Refused(String), Broken(String) }

struct Order { cart: Cart; }

impl Flow<Result<String, Problem>> for Order {
    fn run() -> Result<String, Problem> {
        let hold = reserve(self.cart)?;
        let paid = pay(self.cart)?;
        flow.undo(refund, paid);

        wait time.after(10.milliseconds());

        if self.cart.id == "mistake" {
            let none: [Int] = [];
            println(none[2]);
        }
        let booked = book(self.cart, hold)?;
        return Ok("${hold.id} ${paid} ${booked}");
    }
}

step fn reserve(cart: Cart) -> Result<Hold, Problem> {
    println("WORLD reserve", cart.id);
    let hold = Hold { id: "h-" + cart.id };
    flow.undo(release, hold);
    return Ok(hold);
}

step fn pay(cart: Cart) -> Result<String, Problem> {
    println("WORLD pay", cart.id);
    if cart.amount > 1000 {
        return Err(Problem.Refused("too much"));
    }
    return Ok("p-" + cart.id);
}

step fn book(cart: Cart, hold: Hold) -> Result<String, Problem> {
    println("WORLD book", cart.id);
    if cart.id != "fine" {
        return Err(Problem.Broken("no van"));
    }
    return Ok("b-" + cart.id);
}

step fn release(hold: Hold) -> Result<Void, Problem> {
    println("WORLD release", hold.id);
    return Ok(null);
}

step fn refund(reference: String) -> Result<Void, Problem> {
    println("WORLD refund", reference);
    if reference == "p-stuck" {
        return Err(Problem.Broken("the bank is closed"));
    }
    if reference == "p-busy" && flow.attempt() == 1 {
        flow.retry("the bank is busy", 20.milliseconds());
    }
    return Ok(null);
}

struct Careless { n: Int; }

impl Flow<Result<Int, String>> for Careless {
    fn run() -> Result<Int, String> {
        flow.undo(fn(n: Int) -> Result<Void, String> { return Ok(null); }, self.n);
        return Err("never");
    }
}

fn main() {}
`

func cart(id string, amount int) string {
	return `{"$type": "Order", "cart": {"$type": "Cart", "id": "` + id + `", "amount": ` + itoa(amount) + `}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestUndos(t *testing.T) {
	chunk := compiled(t, orders)
	for _, c := range []struct {
		name, id string
		amount   int
		status   flowrt.Status
		result   string
		world    []string
	}{
		{"an order that ends well forgets its undos", "fine", 5, flowrt.Done,
			`{"$type":"std.result.Result.Ok","$variant":true,"0":"h-fine p-fine b-fine"}`,
			[]string{"WORLD reserve fine", "WORLD pay fine", "WORLD book fine"}},
		{"an Err from run undoes the last first", "late", 5, flowrt.Done,
			`{"$type":"std.result.Result.Err","$variant":true,"0":{"$type":"Problem.Broken","$variant":true,"0":"no van"}}`,
			[]string{"WORLD reserve late", "WORLD pay late", "WORLD book late", "WORLD refund p-late", "WORLD release h-late"}},
		{"what was not done is not undone", "rich", 5000, flowrt.Done,
			`{"$type":"std.result.Result.Err","$variant":true,"0":{"$type":"Problem.Refused","$variant":true,"0":"too much"}}`,
			[]string{"WORLD reserve rich", "WORLD pay rich", "WORLD release h-rich"}},
		{"an undo asked to be run again runs again, then the others", "busy", 5, flowrt.Done,
			`{"$type":"std.result.Result.Err","$variant":true,"0":{"$type":"Problem.Broken","$variant":true,"0":"no van"}}`,
			[]string{"WORLD reserve busy", "WORLD pay busy", "WORLD book busy", "WORLD refund p-busy", "WORLD refund p-busy", "WORLD release h-busy"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			inst := runFlow(t, flowrt.New(chunk, store, &out), store, "o", "Order", cart(c.id, c.amount))
			if inst.Status != c.status || string(inst.Result) != c.result {
				t.Fatalf("expected %s with %s, got %s %s %s", c.status, c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(c.world, "|") {
				t.Errorf("want %v\ngot  %v", c.world, got)
			}
			if len(inst.Undos) != 0 {
				t.Errorf("an ended instance keeps no undos, got %v", inst.Undos)
			}
		})
	}
}

// An undo that returns an Err has not undone: the instance stops in its
// finish phase, the undos left shown, and the ones after it do not run.
func TestAnUndoThatFails(t *testing.T) {
	chunk := compiled(t, orders)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &out), store, "o", "Order", cart("stuck", 5))
	if inst.Status != flowrt.Stopped || inst.Phase != flowrt.Finish || !strings.Contains(inst.Stopped, "refund could not undo") || !strings.Contains(inst.Stopped, "the bank is closed") {
		t.Fatalf("expected stopped in its finish phase, got %s %s %q", inst.Status, inst.Phase, inst.Stopped)
	}
	if len(inst.Undos) != 2 || strings.Contains(out.String(), "WORLD release") {
		t.Errorf("expected the release still to run, got %v\n%s", inst.Undos, out.String())
	}
}

// A cancellation finds the instance asleep: it stops where it is, and its
// undos run, the one run noted and the one a step noted.
func TestCancelling(t *testing.T) {
	chunk := compiled(t, orders)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	now := int64(1_000_000)
	rt := flowrt.New(chunk, store, &out)
	rt.Now = func() int64 { return now }
	if _, err := rt.Start("o", "Order", []byte(cart("late", 5))); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	if inst, _, _ := store.Get("o"); inst.Status != flowrt.Waiting || len(inst.Undos) != 2 {
		t.Fatalf("expected it asleep with two undos, got %s %v", inst.Status, inst.Undos)
	}

	if err := rt.Cancel("o", "the customer changed their mind"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("o")
	if inst.Status != flowrt.Failed || inst.Cancel != "the customer changed their mind" || inst.Result != nil {
		t.Fatalf("expected failed, cancelled, got %s %q %s", inst.Status, inst.Cancel, inst.Result)
	}
	want := []string{"WORLD reserve late", "WORLD pay late", "WORLD refund p-late", "WORLD release h-late"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
	if err := rt.Cancel("o", "again"); err == nil || !strings.Contains(err.Error(), "has ended") {
		t.Errorf("an ended instance: expected a refusal, got %v", err)
	}
	if err := rt.Cancel("nobody", "x"); err == nil {
		t.Errorf("no instance: expected a refusal")
	}
}

// A mistake stops an instance and undoes nothing; a person who cancels
// it then has its undos run, from the state it stopped with.
func TestAMistakeThenACancellation(t *testing.T) {
	chunk := compiled(t, orders)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	inst := runFlow(t, rt, store, "o", "Order", cart("mistake", 5))
	if inst.Status != flowrt.Stopped || inst.Phase != "" || len(inst.Undos) != 2 || strings.Contains(out.String(), "WORLD release") {
		t.Fatalf("expected stopped with its undos kept and none run, got %s %q %v\n%s", inst.Status, inst.Stopped, inst.Undos, out.String())
	}
	if err := rt.Cancel("o", "a person looked"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ = store.Get("o")
	if inst.Status != flowrt.Failed || !strings.HasSuffix(strings.Join(worldLines(out.String()), "|"), "WORLD refund p-mistake|WORLD release h-mistake") {
		t.Errorf("expected failed with both undone, got %s\n%s", inst.Status, out.String())
	}
}

// What flow.undo is given is a step, and data.
func TestUndoIsGivenAStep(t *testing.T) {
	chunk := compiled(t, orders)
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &bytes.Buffer{}), store, "c", "Careless", `{"n": 1}`)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "flow.undo is given a step fn") {
		t.Errorf("expected stopped saying so, got %s %q", inst.Status, inst.Stopped)
	}
}

// Stopped at any write, an order that fails, and one cancelled, end the
// same: every undo done, none more than once more than asked.
func TestUndosSurviveAStopAtEveryWrite(t *testing.T) {
	everyStopOf(t, orders, func(rt *flowrt.Runtime) error {
		_, err := rt.Start("o", "Order", []byte(cart("late", 5)))
		return err
	}, flowrt.Done, []string{"WORLD reserve late", "WORLD pay late", "WORLD book late", "WORLD refund p-late", "WORLD release h-late"})

	everyStopOf(t, orders, func(rt *flowrt.Runtime) error {
		_, err := rt.Start("o", "Order", []byte(cart("busy", 5)))
		return err
	}, flowrt.Done, []string{"WORLD reserve busy", "WORLD pay busy", "WORLD book busy", "WORLD refund p-busy", "WORLD refund p-busy", "WORLD release h-busy"})
}

// everyStopOf runs a scenario on a store that fails at each of its
// writes in turn, then again on what it kept: it ends as without the stop.
func everyStopOf(t *testing.T, src string, start func(rt *flowrt.Runtime) error, status flowrt.Status, world []string) {
	t.Helper()
	chunk := compiled(t, src)
	run := func(store *flowrt.Memory, out *bytes.Buffer) error {
		rt := flowrt.New(chunk, store, out)
		if err := start(rt); err != nil {
			return err
		}
		return rt.RunUntilIdle(10 * time.Second)
	}
	plain := flowrt.NewMemory()
	if err := run(plain, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	clean, _, _ := plain.Get("o")
	writes := plain.Writes()

	expected := map[string]int{}
	for _, line := range world {
		expected[line]++
	}
	for k := 1; k <= writes; k++ {
		var out bytes.Buffer
		store := flowrt.NewMemory()
		store.FailAt = k
		// A run that wrote less this time, as the clock decided, ends
		// without the stop: it has to end the same all the same
		if err := run(store, &out); err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		store.FailAt = 0
		if err := run(store, &out); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		inst, _, _ := store.Get("o")
		if inst.Status != status || string(inst.Result) != string(clean.Result) {
			t.Fatalf("after stopping at write %d of %d: expected %s %s, got %s %s %q", k, writes, status, clean.Result, inst.Status, inst.Result, inst.Stopped)
		}
		done := map[string]int{}
		for _, line := range worldLines(out.String()) {
			done[line]++
		}
		again := 0
		for line, n := range expected {
			if done[line] < n {
				t.Errorf("after stopping at write %d: %q done %d times, not %d", k, line, done[line], n)
			}
			again += done[line] - n
		}
		if again > 1 {
			t.Errorf("after stopping at write %d: %d steps done again: %v", k, again, worldLines(out.String()))
		}
	}
}

// A cancellation asked while a step is under way: the step's answer is
// kept, the undo run notes after it too, and nothing more leaves.
func TestCancellingWhileAStepIsUnderWay(t *testing.T) {
	chunk := compiled(t, orders)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	rt.Ran = func(step, key string, once bool) {
		if step == "pay" {
			if err := rt.Cancel("o", "withdrawn"); err != nil {
				t.Error(err)
			}
		}
	}
	inst := runFlow(t, rt, store, "o", "Order", cart("late", 5))
	if inst.Status != flowrt.Failed || inst.Cancel != "withdrawn" {
		t.Fatalf("expected failed, got %s %q %q", inst.Status, inst.Cancel, inst.Stopped)
	}
	want := []string{"WORLD reserve late", "WORLD pay late", "WORLD refund p-late", "WORLD release h-late"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}

// On a file of SQLite, stopped at every write: the undos a step noted
// are kept with its result, and a failing order undoes everything.
func TestSQLiteUndos(t *testing.T) {
	chunk := compiled(t, orders)
	stoppedEverywhereOnDisk(t, chunk, "Order", cart("late", 5), func(k int, out string, inst *flowrt.Instance) {
		lines := strings.Join(worldLines(out), "|")
		if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "no van") || !strings.Contains(lines, "WORLD refund p-late") || !strings.HasSuffix(lines, "WORLD release h-late") {
			t.Errorf("stopped at write %d: expected done with both undone, got %s %s\n%s", k, inst.Status, inst.Result, lines)
		}
	})
}

// A cancellation on a file of SQLite outlives the process that asked it.
func TestSQLiteCancel(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) { cancelKept(t, p) })
	}
}

func cancelKept(t *testing.T, p place) {
	chunk := compiled(t, orders)
	at := p.fresh(t)
	store := p.open(t, at)
	var out bytes.Buffer
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("o", "Order", []byte(cart("late", 5))); err != nil {
		t.Fatal(err)
	}
	rt.RunDue()
	if err := rt.Cancel("o", "withdrawn"); err != nil {
		t.Fatal(err)
	}
	// A save of the instance leaves the cancellation as it is
	inst, _, _ := store.Get("o")
	if err := store.Save(inst); err != nil {
		t.Fatal(err)
	}
	store.Close()

	again := p.open(t, at)
	defer again.Close()
	if err := flowrt.New(chunk, again, &out).RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ = again.Get("o")
	if inst.Status != flowrt.Failed || inst.Cancel != "withdrawn" || !strings.HasSuffix(out.String(), "WORLD release h-late\n") {
		t.Errorf("expected failed and undone, got %s %q\n%s", inst.Status, inst.Cancel, out.String())
	}
}

// A cancellation asked as the instance is about to be written asleep is
// not lost: the save makes it due at once, on either store.
func TestACancellationIsNotSleptThrough(t *testing.T) {
	chunk := compiled(t, orders)
	for name, store := range stores(t) {
		rt := flowrt.New(chunk, store, &bytes.Buffer{})
		if _, err := rt.Start("o", "Order", []byte(cart("late", 5))); err != nil {
			t.Fatal(err)
		}
		inst, _, _ := store.Get("o")
		inst.Status = flowrt.Running
		if err := store.Save(inst); err != nil {
			t.Fatal(err)
		}
		if err := rt.Cancel("o", "now"); err != nil {
			t.Fatal(err)
		}
		inst.Status, inst.WakeAt = flowrt.Waiting, rt.Now()+3_600_000
		if err := store.Save(inst); err != nil {
			t.Fatal(err)
		}
		if due := store.Due(rt.Now()); len(due) != 1 {
			t.Errorf("%s: expected it due at once, got %v", name, due)
		}
	}
}
