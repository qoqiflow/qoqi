package flowtest

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

// run compiles a program and runs its main: what it printed, and how it
// ended.
func run(t *testing.T, src string) (string, error) {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.Compile(src, "trials.qi", errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	_, err := machine.Exec(result.Chunk)
	return out.String(), err
}

const flows = `
import std.flow;
import std.flow.{Flow, Settled};
import std.time;

struct Payment { invoice: String; amount: Int; }

impl Flow<String> for Payment {
    fn run() -> String {
        let request = initiate(self.invoice);
        wait time.after(1.hours());
        let status = check(request);
        match wire(request, self.amount) {
            case Done(reference): { return "${status} ${reference}"; }
            case Unknown: { return "${status} unknown"; }
        }
    }
}

step fn initiate(invoice: String) -> String {
    println("WORLD initiate", invoice);
    return "req-" + invoice;
}

step fn check(request: String) -> String {
    println("WORLD check", request);
    if flow.attempt() == 1 {
        flow.retry("the gateway is busy", 30.minutes());
    }
    return "checked";
}

step fn wire(request: String, amount: Int) -> Settled<String> within 5.minutes() {
    println("WORLD wire", request, amount);
    return Settled.Done("ref-${amount}");
}

struct Broken { items: [Int]; }

impl Flow<Int> for Broken {
    fn run() -> Int {
        wait time.after(10.milliseconds());
        return self.items[5];
    }
}
`

// A trial's clock moves only when the test moves it: an hour of sleep,
// half an hour before a retry, and the steps the test replaced.
func TestATrialOnItsOwnClock(t *testing.T) {
	out, err := run(t, flows+`
fn main() {
    let trial = flow.test(Payment { invoice: "A1", amount: 300 });
    let wired: [String] = [];
    trial.replace(initiate, fn(invoice: String) -> String { return "fake-" + invoice; });
    trial.replace(wire, fn(request: String, amount: Int) -> Settled<String> {
        wired.push(request);
        return Settled.Done("test-${amount}");
    });

    trial.go();
    println(trial.status(), trial.result(), trial.calls(initiate), trial.calls(check));
    trial.advance(59.minutes());
    println(trial.status(), trial.calls(check));
    trial.advance(1.minutes());
    println(trial.status(), trial.calls(check));
    trial.advance(30.minutes());
    println(trial.status(), trial.result(), trial.calls(check), trial.calls(wire), wired);
}
`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{
		"waiting null 1 0",
		"waiting 0",
		"waiting 1",
		`done checked test-300 2 1 ["fake-A1"]`,
	}
	got := strings.Split(strings.TrimSpace(out), "\n")
	var lines []string
	for _, line := range got {
		if !strings.HasPrefix(line, "WORLD") {
			lines = append(lines, line)
		}
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("want:\n%s\ngot:\n%s", strings.Join(want, "\n"), out)
	}
	if !strings.Contains(out, "WORLD check fake-A1") || strings.Contains(out, "WORLD initiate") || strings.Contains(out, "WORLD wire") {
		t.Errorf("only the steps not replaced reach the world:\n%s", out)
	}
}

// A mistake stops the trial's instance, which says why.
func TestATrialThatStops(t *testing.T) {
	out, err := run(t, flows+`
fn main() {
    let trial = flow.test(Broken { items: [1, 2] });
    trial.go();
    trial.advance(10.milliseconds());
    println(trial.status(), trial.stopped() ?? "", trial.result());
}
`)
	if err != nil || !strings.Contains(out, "stopped") || !strings.Contains(out, "index out of bounds") {
		t.Errorf("expected stopped on the index, got %v\n%s", err, out)
	}
}

// everyCrash runs a scenario stopped at every write, and passes when it
// ends the same each time.
func TestEveryCrashPasses(t *testing.T) {
	out, err := run(t, flows+`
fn main() {
    let trial = flow.test(Payment { invoice: "B2", amount: 5 });
    trial.replace(initiate, fn(invoice: String) -> String { return "fake-" + invoice; });
    trial.everyCrash(fn(t: flow.Trial<String>) {
        t.go();
        t.advance(1.hours());
        t.advance(30.minutes());
        if t.status() != "done" {
            panic("not done: ${t.status()}");
        }
    });
    println("every crash ends the same");
}
`)
	if err == nil {
		// The wire is never run twice; but stopped as its answer was being
		// kept, it is Unknown, and the payment ends otherwise
		t.Fatalf("expected everyCrash to find the stop that makes the wire Unknown:\n%s", out)
	}
	if !strings.Contains(err.Error(), "it gave \"checked unknown\", and \"checked ref-5\" without the stop") {
		t.Errorf("expected everyCrash to say how it ended otherwise, got: %v", err)
	}
}

// A flow whose every stop ends the same passes.
func TestEveryCrashOnASafeFlow(t *testing.T) {
	out, err := run(t, flows+`
struct Count { to: Int; }

impl Flow<Int> for Count {
    fn run() -> Int {
        let total = 0;
        for i in 0..3 {
            total += add(i);
            wait time.after(1000.milliseconds());
        }
        return total;
    }
}

step fn add(n: Int) -> Int {
    return n * 10;
}

fn main() {
    flow.test(Count { to: 3 }).everyCrash(fn(t: flow.Trial<Int>) {
        t.go();
        for _ in 0..3 {
            t.advance(1000.milliseconds());
        }
        if t.result() != 30 {
            panic("got ${t.result()}");
        }
    });
    println("safe");
}
`)
	if err != nil || strings.TrimSpace(out) != "safe" {
		t.Errorf("expected every stop to end the same: %v\n%s", err, out)
	}
}

// What a trial is given wrong is said.
func TestTrialMistakes(t *testing.T) {
	for _, c := range []struct{ name, body, says string }{
		{"a replacement that is not a function", `flow.test(Count2 { n: 1 }).replace(add2, 3);`, "what stands for add2 is a function"},
		{"a clock going back", `flow.test(Count2 { n: 1 }).advance((-5).milliseconds());`, "goes forward"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, flows+`
struct Count2 { n: Int; }
impl Flow<Int> for Count2 { fn run() -> Int { return add2(self.n); } }
step fn add2(n: Int) -> Int { return n; }
fn main() { `+c.body+` }
`)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("expected %q, got %v", c.says, err)
			}
		})
	}
}

// A flow of several tasks under trial: steps replaced in each task, the
// clock moved for all of them, and every stop ending the same.
func TestATrialOfSeveralTasks(t *testing.T) {
	out, err := run(t, `
import std.flow;
import std.flow.{Flow};
import std.task;
import std.time;

struct Batch { ids: [String]; }

impl Flow<[String]> for Batch {
    fn run() -> [String] {
        let done = wait task.map(self.ids, settle);
        return done.expect("the batch");
    }
}

fn settle(id: String) -> String {
    let tries = 0;
    while !ready(id, tries) {
        tries += 1;
        wait time.after(1.minutes());
    }
    return "${id} after ${tries}";
}

step fn ready(id: String, tries: Int) -> Bool {
    println("WORLD ready", id);
    return true;
}

fn main() {
    let trial = flow.test(Batch { ids: ["a", "bb", "ccc"] });
    trial.replace(ready, fn(id: String, tries: Int) -> Bool { return tries >= id.len(); });
    trial.go();
    println(trial.status(), trial.calls(ready));
    trial.advance(1.minutes());
    trial.advance(1.minutes());
    println(trial.status(), trial.calls(ready));
    trial.advance(1.minutes());
    println(trial.status(), trial.result(), trial.calls(ready));

    trial.everyCrash(fn(t: flow.Trial<[String]>) {
        t.go();
        t.advance(3.minutes());
        t.advance(3.minutes());
        t.advance(3.minutes());
    });
    println("every crash ends the same");
}
`)
	want := `waiting 3
waiting 8
done ["a after 1", "bb after 2", "ccc after 3"] 9
every crash ends the same
`
	if err != nil || out != want {
		t.Errorf("want:\n%sgot (%v):\n%s", want, err, out)
	}
}

// Undos under trial: a replaced step notes its undo as the real one
// would, an Err from run undoes in reverse, a cancellation fails the
// instance, and every stop ends the same.
func TestATrialUndoes(t *testing.T) {
	out, err := run(t, `
import std.flow;
import std.flow.{Flow};
import std.time;

struct Booking { trip: String; }

impl Flow<Result<String, String>> for Booking {
    fn run() -> Result<String, String> {
        let seat = hold(self.trip)?;
        wait time.after(1.days());
        let room = hotel(self.trip)?;
        return Ok("${seat} ${room}");
    }
}

step fn hold(trip: String) -> Result<String, String> {
    flow.undo(free, "seat-" + trip);
    return Ok("seat-" + trip);
}

step fn hotel(trip: String) -> Result<String, String> {
    return Err("no room");
}

step fn free(seat: String) -> Result<Void, String> {
    println("WORLD free", seat);
    return Ok(null);
}

fn main() {
    let freed: [String] = [];
    let trial = flow.test(Booking { trip: "rome" });
    trial.replace(free, fn(seat: String) -> Result<Void, String> {
        freed.push(seat);
        return Ok(null);
    });
    trial.go();
    trial.advance(1.days());
    println(trial.status(), trial.result(), freed);

    let cancelled = flow.test(Booking { trip: "oslo" });
    cancelled.replace(hold, fn(trip: String) -> Result<String, String> {
        flow.undo(free, "fake-" + trip);
        return Ok("fake-" + trip);
    });
    cancelled.replace(free, fn(seat: String) -> Result<Void, String> {
        freed.push(seat);
        return Ok(null);
    });
    cancelled.go();
    cancelled.cancel("plans changed");
    println(cancelled.status(), cancelled.result(), cancelled.calls(free), freed);

    trial.everyCrash(fn(t: flow.Trial<Result<String, String>>) {
        t.go();
        t.advance(1.days());
    });
    cancelled.everyCrash(fn(t: flow.Trial<Result<String, String>>) {
        t.go();
        t.cancel("plans changed");
    });
    println("every crash ends the same");
}
`)
	want := `done Err("no room") ["seat-rome"]
failed null 1 ["seat-rome", "fake-oslo"]
every crash ends the same
`
	if err != nil || out[strings.LastIndex(out, "done"):] != want {
		t.Errorf("want:\n%sgot (%v):\n%s", want, err, out)
	}
}

// A parent and its child under trial: the child runs on the trial's
// clock, its steps replaced as the parent's are, and every stop of
// either ends the same.
func TestATrialOfAFamily(t *testing.T) {
	out, err := run(t, `
import std.flow;
import std.flow.{Flow};
import std.time;

struct Quote { item: String; }

impl Flow<Int> for Quote {
    fn run() -> Int {
        wait time.after(1.hours());
        return price(self.item);
    }
}

struct Basket { items: [String]; }

impl Flow<Int> for Basket {
    fn run() -> Int {
        let total = 0;
        for item in self.items {
            match wait flow.start("quote:${item}", Quote { item: item }) {
                case Ok(p): { total += p; }
                case Err(_): {}
            }
        }
        return total;
    }
}

step fn price(item: String) -> Int {
    return 1000;
}

fn main() {
    let trial = flow.test(Basket { items: ["tea", "rice"] });
    trial.replace(price, fn(item: String) -> Int { return item.len(); });
    trial.go();
    trial.advance(1.hours());
    println(trial.status(), trial.result());
    trial.advance(1.hours());
    println(trial.status(), trial.result(), trial.calls(price));

    trial.everyCrash(fn(t: flow.Trial<Int>) {
        t.go();
        t.advance(1.hours());
        t.advance(1.hours());
    });
    println("every crash ends the same");
}
`)
	want := "waiting null\ndone 7 2\nevery crash ends the same\n"
	if err != nil || out != want {
		t.Errorf("want:\n%sgot (%v):\n%s", want, err, out)
	}
}

// A shop whose steps its application carries out: a trial replaces them
// as it replaces any step, and one it did not replace stops the trial,
// naming it, since nobody would answer.
const queued = `
import std.flow;
import std.flow.{Flow, Settled};
import std.time;

struct Cart { id: String; amount: Int; }
struct Hold { id: String; }

struct Order { cart: Cart; }

impl Flow<Result<String, String>> for Order {
    fn run() -> Result<String, String> {
        let hold = reserve(self.cart)?;
        let paid = charge(self.cart.amount)?;
        return Ok("${hold.id} ${paid}");
    }
}

step fn reserve(cart: Cart) -> Result<Hold, String> by warehouse undo release;
step fn release(hold: Hold) -> Result<Void, String> by warehouse;
step fn charge(amount: Int) -> Result<String, String> within 2.hours() by billing;
`

func runQueued(t *testing.T, src string) (string, error) {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.CompileOptions(src, "trials.qi", compiler.Options{Queues: []string{"warehouse", "billing"}}, errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	_, err := machine.Exec(result.Chunk)
	return out.String(), err
}

func TestATrialOfStepsTheApplicationCarriesOut(t *testing.T) {
	out, err := runQueued(t, queued+`
fn replaced(declined: Bool) -> flow.Trial<Result<String, String>> {
    let trial = flow.test(Order { cart: Cart { id: "c1", amount: 1200 } });
    trial.replace(reserve, fn(cart: Cart) -> Result<Hold, String> {
        flow.undo(release, Hold { id: "h-${cart.id}" });
        return Ok(Hold { id: "h-${cart.id}" });
    });
    trial.replace(release, fn(hold: Hold) -> Result<Void, String> {
        println("released", hold.id);
        return Ok(null);
    });
    trial.replace(charge, fn(amount: Int) -> Result<String, String> {
        if declined {
            return Err("declined");
        }
        return Ok("paid ${amount}");
    });
    return trial;
}

fn main() {
    let trial = replaced(false);
    trial.go();
    println(trial.status(), trial.result(), trial.calls(charge));

    let refused = replaced(true);
    refused.go();
    println(refused.status(), refused.result(), refused.calls(release));

    let alone = flow.test(Order { cart: Cart { id: "c2", amount: 5 } });
    alone.go();
    println(alone.status(), alone.stopped() ?? "");

    replaced(true).everyCrash(fn(t: flow.Trial<Result<String, String>>) {
        t.go();
    });
    println("every crash");
}
`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		`done Ok("h-c1 paid 1200") 1`,
		"released h-c1",
		`done Err("declined") 1`,
		"stopped reserve is carried out by the queue warehouse, which nothing serves here: a trial replaces it, trial.replace(reserve, ...)",
		"every crash",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
}
