package flowrt_test

import (
	"bytes"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

func compiled(t *testing.T, src string) *vm.Chunk {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.Compile(src, "flows.qi", errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	return result.Chunk
}

// An order: a hold, a sleep, a payment, then three shipments with a sleep
// after each. Every step says, on the output, that it reached the world.
const order = `
import std.flow.{Flow};
import std.time;

struct Order { id: String; amount: Int; }
struct Receipt { order: String; cents: Int; lines: [String]; }
enum Outcome { Shipped(Receipt), Refused(String) }

impl Flow<Outcome> for Order {
    fn run() -> Outcome {
        let hold = reserve(self.id);
        println("held", hold);
        wait time.after(15.milliseconds());

        let paid = charge(self.id, self.amount);
        let lines: [String] = [];
        for i in 0..3 {
            lines.push("parcel ${ship(self.id, i)}");
            wait time.after(5.milliseconds());
        }

        if paid < 0 {
            return Outcome.Refused("no money");
        }
        return Outcome.Shipped(Receipt { order: hold, cents: paid, lines: lines });
    }
}

step fn reserve(id: String) -> String {
    println("WORLD reserve", id);
    return "hold-" + id;
}

step fn charge(id: String, amount: Int) -> Int {
    println("WORLD charge", id, amount);
    return amount * 100;
}

step fn ship(id: String, n: Int) -> Int {
    println("WORLD ship", id, n);
    return n + 1;
}

fn main() {}
`

const orderFields = `{"$type": "Order", "id": "A1", "amount": 3}`

const shipped = `{"$type":"Outcome.Shipped","$variant":true,"0":{"$type":"Receipt","cents":300,"lines":["parcel 1","parcel 2","parcel 3"],"order":"hold-A1"}}`

// worldLines are the lines steps printed, in order.
func worldLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "WORLD ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestAFlowRunsToItsEnd(t *testing.T) {
	chunk := compiled(t, order)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("order:A1", "Order", []byte(orderFields)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("order:A1")
	if inst.Status != flowrt.Done || string(inst.Result) != shipped {
		t.Fatalf("expected done with %s, got %s %s %s", shipped, inst.Status, inst.Result, inst.Stopped)
	}
	want := []string{"WORLD reserve A1", "WORLD charge A1 3", "WORLD ship A1 0", "WORLD ship A1 1", "WORLD ship A1 2"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("each step once, in order:\nwant %v\ngot  %v", want, got)
	}
	if len(store.ResultLog) != 5 {
		t.Errorf("expected five results kept, got %v", store.ResultLog)
	}
}

// The process stops at each write of the store in turn, and starts again
// on what the store kept: the order ends the same, every step is done,
// and a step is done twice only when the process stopped between doing
// it and keeping what it gave, the one moment no runtime can know of.
func TestAFlowSurvivesAStopAtEveryWrite(t *testing.T) {
	chunk := compiled(t, order)

	// How many writes a run makes
	plain := flowrt.NewMemory()
	rt := flowrt.New(chunk, plain, &bytes.Buffer{})
	if _, err := rt.Start("order:A1", "Order", []byte(orderFields)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	writes := plain.Writes()
	if writes < 10 {
		t.Fatalf("expected a write before each step, at each sleep and for each result: got %d", writes)
	}

	doneTwice := 0
	for k := 1; k <= writes; k++ {
		var out bytes.Buffer
		store := flowrt.NewMemory()
		store.FailAt = k
		first := flowrt.New(chunk, store, &out)
		_, err := first.Start("order:A1", "Order", []byte(orderFields))
		if err == nil {
			err = first.RunUntilIdle(5 * time.Second)
		}
		if !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: expected the stop, got %v", k, err)
		}

		// A new process, on what the store kept
		store.FailAt = 0
		again := flowrt.New(chunk, store, &out)
		if _, err := again.Start("order:A1", "Order", []byte(orderFields)); err != nil {
			t.Fatal(err)
		}
		if err := again.RunUntilIdle(5 * time.Second); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}

		inst, _, _ := store.Get("order:A1")
		if inst.Status != flowrt.Done || string(inst.Result) != shipped {
			t.Fatalf("after stopping at write %d: expected done with %s, got %s %s %s", k, shipped, inst.Status, inst.Result, inst.Stopped)
		}

		lines := worldLines(out.String())
		distinct := map[string]int{}
		for _, line := range lines {
			distinct[line]++
		}
		if len(distinct) != 5 {
			t.Errorf("after stopping at write %d: expected the five steps, got %v", k, lines)
		}
		twice := 0
		for line, n := range distinct {
			if n > 2 {
				t.Errorf("after stopping at write %d: %q done %d times", k, line, n)
			}
			if n == 2 {
				twice++
			}
		}
		if twice > 1 {
			t.Errorf("after stopping at write %d: more than one step done twice: %v", k, lines)
		}
		doneTwice += twice
		keys := append([]string(nil), store.ResultLog...)
		sort.Strings(keys)
		if len(keys) != 5 {
			t.Errorf("after stopping at write %d: expected five results kept, got %v", k, keys)
		}
	}
	// Once for each step: stopped as it kept what the step returned
	if doneTwice != 5 {
		t.Errorf("expected each of the five steps done twice once, when the process stopped as it kept its result; got %d over %d writes", doneTwice, writes)
	}
	t.Logf("%d writes, each stopped at", writes)
}

// A name started twice is one instance; another name is another.
func TestANameIsOneInstance(t *testing.T) {
	chunk := compiled(t, order)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	first, err := rt.Start("order:A1", "Order", []byte(orderFields))
	if err != nil {
		t.Fatal(err)
	}
	again, err := rt.Start("order:A1", "Order", []byte(`{"$type": "Order", "id": "A1", "amount": 99}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Birth != again.Birth || string(again.Fields) != orderFields {
		t.Errorf("expected the instance that has the name, got %+v", again)
	}
	if _, err := rt.Start("order:B2", "Order", []byte(`{"$type": "Order", "id": "B2", "amount": 1}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := len(worldLines(out.String())); got != 10 {
		t.Errorf("expected two orders of five steps, got %d lines:\n%s", got, out.String())
	}
	other, _, _ := store.Get("order:B2")
	if other.Birth == first.Birth {
		t.Errorf("two instances with one birth: %s", first.Birth)
	}
}

// A mistake in the program stops the instance, with why; a flow that is
// not one of the program is refused.
func TestAMistakeStopsAnInstance(t *testing.T) {
	chunk := compiled(t, `
import std.flow.{Flow};
import std.time;

struct Broken { items: [Int]; }

impl Flow<Int> for Broken {
    fn run() -> Int {
        let first = note(self.items.len());
        wait time.after(5.milliseconds());
        return self.items[first + 10];
    }
}

step fn note(n: Int) -> Int {
    println("WORLD note", n);
    return n;
}

fn main() {}
`)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("broken", "Broken", []byte(`{"$type": "Broken", "items": [1, 2]}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("broken")
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "index out of bounds") {
		t.Errorf("expected stopped on the index, got %s %q", inst.Status, inst.Stopped)
	}
	if _, err := rt.Start("x", "Nothing", []byte(`{}`)); err == nil {
		t.Errorf("expected a flow the program has not to be refused")
	}
}

// A step knows its key, the same at every attempt, and which attempt it
// is: run again after the process stopped before its result was kept, it
// is the second, and asks the world what the first did.
func TestAStepKnowsItsKeyAndAttempt(t *testing.T) {
	chunk := compiled(t, `
import std.flow;
import std.flow.{Flow};

struct Transfer { to: String; }

impl Flow<String> for Transfer {
    fn run() -> String {
        let first = send(self.to);
        let second = send(self.to);
        return first + " | " + second;
    }
}

step fn send(to: String) -> String {
    println("WORLD send", flow.key(), flow.attempt());
    return "${to} ${flow.attempt()}";
}

fn main() {}
`)
	// Writes: 1 create, 2 before the first send, 3 its result, 4 before
	// the second, 5 its result, 6 done. Stopping at 3 runs the first
	// send again, its second attempt.
	var out bytes.Buffer
	store := flowrt.NewMemory()
	store.FailAt = 3
	first := flowrt.New(chunk, store, &out)
	if _, err := first.Start("t", "Transfer", []byte(`{"$type": "Transfer", "to": "ada"}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.RunUntilIdle(5 * time.Second); !errors.Is(err, flowrt.ErrCrash) {
		t.Fatalf("expected the stop, got %v", err)
	}
	store.FailAt = 0
	if err := flowrt.New(chunk, store, &out).RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("t")
	if string(inst.Result) != `"ada 2 | ada 1"` {
		t.Errorf("expected the first send on its second attempt, got %s %s", inst.Result, inst.Stopped)
	}
	lines := worldLines(out.String())
	key1 := "t/" + inst.Birth + "/0:0:send#1"
	key2 := "t/" + inst.Birth + "/0:0:send#2"
	want := []string{"WORLD send " + key1 + " 1", "WORLD send " + key1 + " 2", "WORLD send " + key2 + " 1"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("keys and attempts:\nwant %v\ngot  %v", want, lines)
	}
}
