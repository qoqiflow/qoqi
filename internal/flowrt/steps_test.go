package flowrt_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// stoppedEverywhere runs an instance once to learn how many writes it
// makes, then stops the process at each of them in turn and takes it up
// in a new runtime. check is given what was printed and the instance at
// the end, for each.
func stoppedEverywhere(t *testing.T, chunk *vm.Chunk, flow, fields string, check func(k int, out string, inst *flowrt.Instance)) {
	t.Helper()
	plain := flowrt.NewMemory()
	var plainOut bytes.Buffer
	rt := flowrt.New(chunk, plain, &plainOut)
	if _, err := rt.Start("x", flow, []byte(fields)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := plain.Get("x")
	check(0, plainOut.String(), inst)

	for k := 1; k <= plain.Writes(); k++ {
		var out bytes.Buffer
		store := flowrt.NewMemory()
		store.FailAt = k
		first := flowrt.New(chunk, store, &out)
		_, err := first.Start("x", flow, []byte(fields))
		if err == nil {
			err = first.RunUntilIdle(10 * time.Second)
		}
		if !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: expected the stop, got %v", k, err)
		}
		store.FailAt = 0
		again := flowrt.New(chunk, store, &out)
		if _, err := again.Start("x", flow, []byte(fields)); err != nil {
			t.Fatal(err)
		}
		if err := again.RunUntilIdle(10 * time.Second); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		inst, _ := store.Get("x")
		check(k, out.String(), inst)
	}
}

const retrying = `
import std.flow;
import std.flow.{Flow};

struct Fetch { url: String; }

impl Flow<String> for Fetch {
    fn run() -> String {
        let page = fetch(self.url);
        return "got " + page;
    }
}

// Down for its first two attempts
step fn fetch(url: String) -> String {
    println("WORLD fetch", flow.attempt());
    if flow.attempt() < 3 {
        flow.retry("the server is down", 10.milliseconds());
    }
    return url + " on attempt ${flow.attempt()}";
}

fn main() {}
`

// A step that says flow.retry is run again under its key, later, until it
// answers: asleep in the meantime.
func TestRetry(t *testing.T) {
	chunk := compiled(t, retrying)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	started := time.Now()
	if _, err := rt.Start("x", "Fetch", []byte(`{"$type": "Fetch", "url": "example.com"}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("x")
	if string(inst.Result) != `"got example.com on attempt 3"` {
		t.Fatalf("expected the third attempt's answer, got %s %s", inst.Result, inst.Stopped)
	}
	if got := strings.Join(worldLines(out.String()), "|"); got != "WORLD fetch 1|WORLD fetch 2|WORLD fetch 3" {
		t.Errorf("expected three attempts, got %s", got)
	}
	// The clock counts whole milliseconds: a wait of 10 may be 9 and some
	if elapsed := time.Since(started); elapsed < 18*time.Millisecond {
		t.Errorf("expected two waits of 10ms between attempts, took %v", elapsed)
	}
	if len(store.ResultLog) != 1 {
		t.Errorf("expected one result kept, its answer: %v", store.ResultLog)
	}
}

// A step that never answers is given so many attempts, then the instance
// stops, saying why.
func TestRetryGivesUp(t *testing.T) {
	chunk := compiled(t, strings.Replace(retrying, "flow.attempt() < 3", "true", 1))
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})
	rt.MaxTries = 4
	if _, err := rt.Start("x", "Fetch", []byte(`{"$type": "Fetch", "url": "example.com"}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("x")
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "fetch asked to be run again 4 times, and was not: the server is down") {
		t.Errorf("expected stopped after four attempts, got %s %q", inst.Status, inst.Stopped)
	}
}

// Stopped at any write, retries go on where they were.
func TestRetryAcrossStops(t *testing.T) {
	chunk := compiled(t, retrying)
	stoppedEverywhere(t, chunk, "Fetch", `{"$type": "Fetch", "url": "example.com"}`, func(k int, out string, inst *flowrt.Instance) {
		if string(inst.Result) != `"got example.com on attempt 3"` && string(inst.Result) != `"got example.com on attempt 4"` {
			t.Errorf("stopped at write %d: got %s %s", k, inst.Result, inst.Stopped)
		}
		lines := worldLines(out)
		if len(lines) < 3 || len(lines) > 4 {
			t.Errorf("stopped at write %d: expected three attempts, or four when stopped as the answer was kept: %v", k, lines)
		}
	})
}

const wiring = `
import std.flow;
import std.flow.{Flow, Settled};

struct Payout { to: String; cents: Int; }

impl Flow<String> for Payout {
    fn run() -> String {
        let note = remember(self.to);
        match wire(self.to, self.cents) {
            case Done(reference): { return note + " paid " + reference; }
            case Unknown: { return note + " ask the bank"; }
        }
    }
}

step fn remember(to: String) -> String {
    return "for " + to;
}

// Never twice: a wire that went may not go again
step fn wire(to: String, cents: Int) -> Settled<String> within 5000.milliseconds() {
    println("WORLD wire", to, cents);
    return Settled.Done("ref-${cents}");
}

fn main() {}
`

// A step that returns a Settled is never run twice: taken up after the
// process stopped while it ran, it gives Unknown, and the flow says what
// that means. Stopped anywhere else, it gives what it gave.
func TestSettled(t *testing.T) {
	chunk := compiled(t, wiring)
	unknown := 0
	stoppedEverywhere(t, chunk, "Payout", `{"$type": "Payout", "to": "ada", "cents": 500}`, func(k int, out string, inst *flowrt.Instance) {
		lines := worldLines(out)
		if len(lines) > 1 {
			t.Errorf("stopped at write %d: the wire went %d times: %v", k, len(lines), lines)
		}
		switch string(inst.Result) {
		case `"for ada paid ref-500"`:
			if len(lines) != 1 {
				t.Errorf("stopped at write %d: paid without a wire: %v", k, lines)
			}
		case `"for ada ask the bank"`:
			unknown++
			if len(lines) != 1 {
				t.Errorf("stopped at write %d: Unknown for a wire that never went: %v", k, lines)
			}
		default:
			t.Errorf("stopped at write %d: got %s %s", k, inst.Result, inst.Stopped)
		}
	})
	// Once: stopped as what the wire returned was being kept
	if unknown != 1 {
		t.Errorf("expected Unknown once, when the process stopped as the wire's answer was kept; got %d", unknown)
	}
}

// A step never run twice that says flow.retry before anything left, a
// connection refused, is run again: its author says nothing happened.
func TestSettledRetried(t *testing.T) {
	chunk := compiled(t, strings.Replace(wiring, `    println("WORLD wire", to, cents);`, `    if flow.attempt() == 1 {
        flow.retry("connection refused", 5.milliseconds());
    }
    println("WORLD wire", to, cents, flow.attempt());`, 1))
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("x", "Payout", []byte(`{"$type": "Payout", "to": "ada", "cents": 7}`)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("x")
	if string(inst.Result) != `"for ada paid ref-7"` {
		t.Errorf("expected the second attempt to pay, got %s %s", inst.Result, inst.Stopped)
	}
	if got := strings.Join(worldLines(out.String()), "|"); got != "WORLD wire ada 7 2" {
		t.Errorf("expected one wire, on the second attempt: %s", got)
	}
}
