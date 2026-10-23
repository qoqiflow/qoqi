package flowrt_test

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// Flows of several tasks: three invoices retried side by side, a task
// that feeds another through a channel across sleeps, a step that asks
// to be run again inside one task while the others go on, and tasks
// that fail or wait for each other for good.
const tasks = `
import std.flow;
import std.flow.{Flow};
import std.task;
import std.time;

struct Invoice { id: String; amount: Int; }
enum Outcome { Paid(String), Refused(String) }

struct Retries { invoices: [Invoice]; }

impl Flow<[Outcome]> for Retries {
    fn run() -> [Outcome] {
        let outcomes = wait task.map(self.invoices, retryInvoice);
        return outcomes.expect("the retries");
    }
}

fn retryInvoice(invoice: Invoice) -> Outcome {
    for round in 1..4 {
        if charge(invoice.id, invoice.amount, round) {
            return Outcome.Paid(receipt(invoice.id));
        }
        wait time.after((round * 10).milliseconds());
    }
    return Outcome.Refused(invoice.id);
}

step fn charge(id: String, amount: Int, round: Int) -> Bool {
    println("WORLD charge", id, round);
    return amount <= round * 100;
}

step fn receipt(id: String) -> String {
    println("WORLD receipt", id);
    if id == "B" && flow.attempt() == 1 {
        flow.retry("the printer is busy", 25.milliseconds());
    }
    return "r-" + id;
}

struct Relay { count: Int; }

impl Flow<Int> for Relay {
    fn run() -> Int {
        let box = task.channel<Int>(1);
        let count = self.count;
        let producer = task.run(fn() {
            for i in 0..count {
                wait time.after(5.milliseconds());
                wait box.send(fetch(i));
            }
            box.close();
        });

        let total = 0;
        while true {
            let got = wait box.receive();
            if got == null {
                break;
            }
            total += got ?? 0;
        }
        wait producer;
        return total;
    }
}

step fn fetch(i: Int) -> Int {
    println("WORLD fetch", i);
    return i * i;
}

struct Careless { n: Int; }

impl Flow<String> for Careless {
    fn run() -> String {
        let watched = task.start(fn() -> Int {
            let empty: [Int] = [];
            return empty[1];
        });
        let label = "";
        match wait watched {
            case Ok(n): { label = "got ${n}"; }
            case Err(f): { label = "caught: ${f.message}"; }
        }
        if self.n > 0 {
            task.start(fn() -> Int {
                let empty: [Int] = [];
                return empty[self.n];
            });
        }
        wait time.after(5.milliseconds());
        return label;
    }
}

struct Stuck { n: Int; }

impl Flow<Int> for Stuck {
    fn run() -> Int {
        let box = task.channel<Int>(1);
        let other = task.start(fn() -> Int { return (wait box.receive()) ?? 0; });
        return (wait other).expect("other");
    }
}

struct Slow { n: Int; }

impl Flow<Int> for Slow {
    fn run() -> Int {
        let parts = wait task.map([1, 2, 3], fn(i: Int) -> Int { return measure(i); });
        return parts.expect("parts").len() + self.n;
    }
}

step fn measure(i: Int) -> Int {
    let helper = task.start(fn() -> Int {
        wait time.after((3 * i).milliseconds());
        return i;
    });
    wait time.after(2.milliseconds());
    println("WORLD measure", i, flow.key());
    return (wait helper).expect("helper");
}

fn main() {}
`

const retriesFields = `{"$type": "Retries", "invoices": [
	{"$type": "Invoice", "id": "A", "amount": 50},
	{"$type": "Invoice", "id": "B", "amount": 150},
	{"$type": "Invoice", "id": "C", "amount": 999}]}`

const retried = `[{"$type":"Outcome.Paid","$variant":true,"0":"r-A"},{"$type":"Outcome.Paid","$variant":true,"0":"r-B"},{"$type":"Outcome.Refused","$variant":true,"0":"C"}]`

var retriesWorld = []string{
	"WORLD charge A 1", "WORLD charge B 1", "WORLD charge C 1", "WORLD receipt A",
	"WORLD charge B 2", "WORLD charge C 2", "WORLD receipt B", "WORLD receipt B",
	"WORLD charge C 3",
}

// runFlow starts an instance and runs it to its end.
func runFlow(t *testing.T, rt *flowrt.Runtime, store flowrt.Store, id, flow, fields string) *flowrt.Instance {
	t.Helper()
	if _, err := rt.Start(id, flow, []byte(fields)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get(id)
	return inst
}

func sorted(lines []string) []string {
	out := append([]string(nil), lines...)
	sort.Strings(out)
	return out
}

// Three invoices side by side: the first charges of the three leave in
// one write, each task's steps have keys of their own, and the receipt
// of B, asked to be run again, waits while the others go on.
func TestTasksOfAFlow(t *testing.T) {
	chunk := compiled(t, tasks)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	leftAt := map[string]int{}
	rt.Ran = func(step, key string, once bool) {
		leftAt[key[strings.Index(key, ":")+1:]] = store.Writes()
	}

	inst := runFlow(t, rt, store, "retries-1", "Retries", retriesFields)
	if inst.Status != flowrt.Done || string(inst.Result) != retried {
		t.Fatalf("expected done with %s, got %s %s %s", retried, inst.Status, inst.Result, inst.Stopped)
	}
	if got := sorted(worldLines(out.String())); strings.Join(got, "|") != strings.Join(sorted(retriesWorld), "|") {
		t.Errorf("each step once, the receipt of B twice:\nwant %v\ngot  %v", sorted(retriesWorld), got)
	}

	first := leftAt["0/task#1:charge#1"]
	if first == 0 || leftAt["0/task#2:charge#1"] != first || leftAt["0/task#3:charge#1"] != first {
		t.Errorf("expected the three first charges to leave after one write, got %v", leftAt)
	}
	for _, key := range []string{"0/task#1:receipt#1", "0/task#2:charge#2", "0/task#3:charge#3"} {
		if _, ok := leftAt[key]; !ok {
			t.Errorf("expected a step of key %s, got %v", key, leftAt)
		}
	}
	if inst.Tries["retries-1/"+inst.Birth+"/0:0/task#2:receipt#1"] != 2 {
		t.Errorf("expected the receipt of B tried twice, got %v", inst.Tries)
	}
}

// everyStop runs an instance stopped at each write of the store in turn,
// then taken up by a new process: it ends the same each time, and a step
// is done twice only when the process stopped between doing it and
// keeping what it gave.
func everyStop(t *testing.T, flow, fields, want string, world []string) {
	t.Helper()
	chunk := compiled(t, tasks)
	plain := flowrt.NewMemory()
	runFlow(t, flowrt.New(chunk, plain, &bytes.Buffer{}), plain, "i", flow, fields)
	writes := plain.Writes()

	expected := map[string]int{}
	for _, line := range world {
		expected[line]++
	}
	for k := 1; k <= writes; k++ {
		var out bytes.Buffer
		store := flowrt.NewMemory()
		store.FailAt = k
		first := flowrt.New(chunk, store, &out)
		_, err := first.Start("i", flow, []byte(fields))
		if err == nil {
			err = first.RunUntilIdle(10 * time.Second)
		}
		// A run that wrote less this time, as the clock decided, ends
		// without the stop: it has to end the same all the same
		if err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		store.FailAt = 0
		inst := runFlow(t, flowrt.New(chunk, store, &out), store, "i", flow, fields)
		if inst.Status != flowrt.Done || string(inst.Result) != want {
			t.Fatalf("after stopping at write %d of %d: expected done with %s, got %s %s %s", k, writes, want, inst.Status, inst.Result, inst.Stopped)
		}

		done := map[string]int{}
		for _, line := range worldLines(out.String()) {
			done[line]++
		}
		again := 0
		for line, n := range expected {
			switch {
			case done[line] < n:
				t.Errorf("after stopping at write %d: %q done %d times, not %d", k, line, done[line], n)
			case done[line] > n:
				again += done[line] - n
			}
		}
		for line := range done {
			if expected[line] == 0 {
				t.Errorf("after stopping at write %d: %q was not asked for", k, line)
			}
		}
		if again > 1 {
			t.Errorf("after stopping at write %d: %d steps done again: %v", k, again, worldLines(out.String()))
		}
	}
}

func TestTasksOfAFlowSurviveAStopAtEveryWrite(t *testing.T) {
	everyStop(t, "Retries", retriesFields, retried, retriesWorld)
}

// A task feeds the run through a channel, sleeping before each value:
// the instance is written asleep with both tasks in it, a value in the
// channel or none, and taken up as it was.
func TestTasksTalkAcrossSleeps(t *testing.T) {
	chunk := compiled(t, tasks)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &out), store, "relay", "Relay", `{"count": 4}`)
	if inst.Status != flowrt.Done || string(inst.Result) != "14" {
		t.Fatalf("expected done with 14, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
	}
	everyStop(t, "Relay", `{"count": 4}`, "14", []string{"WORLD fetch 0", "WORLD fetch 1", "WORLD fetch 2", "WORLD fetch 3"})
}

// A task's failure is the flow's to take: given to the task that waits
// for it, and, when no task does, the instance stops with it.
func TestTasksThatFail(t *testing.T) {
	chunk := compiled(t, tasks)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})

	inst := runFlow(t, rt, store, "careful", "Careless", `{"n": 0}`)
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "caught: index out of bounds") {
		t.Errorf("a failure waited for: expected it caught, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
	}
	inst = runFlow(t, rt, store, "careless", "Careless", `{"n": 4}`)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "index out of bounds: 4") {
		t.Errorf("a failure nobody waits for: expected the instance stopped with it, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
	}
	inst = runFlow(t, rt, store, "stuck", "Stuck", `{"n": 0}`)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "deadlock") {
		t.Errorf("tasks that wait for each other: expected the instance stopped in a deadlock, got %s %s %s", inst.Status, inst.Result, inst.Stopped)
	}
}

// The body of a step is not durable: it waits, and starts tasks, as any
// program does, and they know its key.
func TestAStepWaitsAsAnyProgram(t *testing.T) {
	chunk := compiled(t, tasks)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &out), store, "slow", "Slow", `{"n": 10}`)
	if inst.Status != flowrt.Done || string(inst.Result) != "13" {
		t.Fatalf("expected done with 13, got %s %s %s\n%s", inst.Status, inst.Result, inst.Stopped, out.String())
	}
	prefix := "slow/" + inst.Birth + "/"
	want := []string{"WORLD measure 1 " + prefix + "0:0/task#1:measure#1", "WORLD measure 2 " + prefix + "0:0/task#2:measure#1", "WORLD measure 3 " + prefix + "0:0/task#3:measure#1"}
	if got := sorted(worldLines(out.String())); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}
