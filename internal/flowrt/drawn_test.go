package flowrt_test

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A notice in several versions: it reads the clock before its stage,
// which is forgotten there; after its stage it reads the clock and
// draws a token and a uuid, and gives them to a step; the first version
// has a mistake after that step, the others are fixes, one of which
// reads the clock once more after the step.
func noticeVersion(before, after string) string {
	return `
import std.flow.{Flow};
import std.random;
import std.time;

struct Notice { to: String; }

impl Flow<String> for Notice {
    fn run() -> String {
        let id = open(self.to);
        println("BEFORE", time.now());
        stage noting;
` + before + `
        let when = time.now();
        let code = random.token(3);
        let ticket = random.uuid();
        let sent = notify(id, "at ${when} ${code} ${ticket}");
` + after + `
        return sent;
    }
}

step fn open(to: String) -> String {
    println("WORLD open", to);
    return "n-" + to;
}

step fn notify(id: String, text: String) -> String {
    println("WORLD notify", id, text);
    return "sent " + text;
}

fn main() {}
`
}

var (
	noticeBuggy = noticeVersion("", mistake)
	noticeFixed = noticeVersion("", `println("LATER", time.now(), random.token(3).len());`)
	// One that reads the clock before what it read: the clock is asked
	// where the token was drawn, and from there it draws anew
	noticeEarly = noticeVersion("let early = time.now();\nlet other = random.token(3);", "")
	// One that waits an hour after the stage, before it draws
	noticeWaits = noticeVersion("wait time.after(1.hours());", mistake)
)

// The stores a resume is tried on: in memory, and in SQLite, opened anew
// by each process.
func noticeStores(t *testing.T) map[string]func() flowrt.Store {
	at := places(t)[0].fresh(t)
	return map[string]func() flowrt.Store{
		"memory": func() func() flowrt.Store {
			store := flowrt.NewMemory()
			return func() flowrt.Store { return store }
		}(),
		"sqlite": func() flowrt.Store {
			store := places(t)[0].open(t, at)
			t.Cleanup(func() { store.Close() })
			return store
		},
	}
}

// A clock a test moves by hand, in milliseconds since 1970.
func handClock(at int64) *atomic.Int64 {
	var clock atomic.Int64
	clock.Store(at)
	return &clock
}

func noticeRuntime(t *testing.T, src string, store flowrt.Store, clock *atomic.Int64, out *bytes.Buffer) *flowrt.Runtime {
	rt := flowrt.New(compiled(t, src), store, out)
	rt.Now = clock.Load
	return rt
}

func idle(t *testing.T, rt *flowrt.Runtime) {
	t.Helper()
	if err := rt.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
}

// due runs, once, the instances due by the runtime's clock.
func due(t *testing.T, rt *flowrt.Runtime) {
	t.Helper()
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
}

// What a stopped notice is given back: the clock and the random numbers
// it drew after its stage, in the order it drew them. The fix resumed
// asks the step as it was asked, minutes later, and the step is found
// by its key, not sent again; what it reads past what was drawn is new.
func TestResumingGivesBackTheClockAndTheRandomNumbers(t *testing.T) {
	for name, open := range noticeStores(t) {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			clock := handClock(1_000_000)
			rt := noticeRuntime(t, noticeBuggy, open(), clock, &out)
			inst := runFlow(t, rt, open(), "n", "Notice", `{"to": "ada"}`)
			if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "index out of bounds") {
				t.Fatalf("expected it stopped by the mistake, got %s %q", inst.Status, inst.Stopped)
			}
			if len(inst.Drawn["0"]) != 3 {
				t.Fatalf("expected the clock, the token and the uuid kept since the stage, got %+v", inst.Drawn)
			}

			// The fix, later: the minute has changed
			clock.Store(1_000_000 + 7*60_000)
			rt = noticeRuntime(t, noticeFixed, open(), clock, &out)
			dry, err := rt.DryResume("n")
			if err != nil || dry.Stopped != "" || len(dry.Took) != 1 {
				t.Errorf("a dry run takes the notice by its key: %v %+v", err, dry)
			}
			if err := rt.Resume("n"); err != nil {
				t.Fatal(err)
			}
			idle(t, rt)

			inst, _, _ = open().Get("n")
			if inst.Status != flowrt.Done || inst.Replaying || !strings.HasPrefix(string(inst.Result), `"sent at 1000000 `) {
				t.Fatalf("expected done with what was sent, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
			}
			if got := strings.Count(out.String(), "WORLD notify"); got != 1 {
				t.Errorf("expected the notice sent once, got %d:\n%s", got, out.String())
			}
			if !strings.Contains(out.String(), "LATER 1420000 6") {
				t.Errorf("expected the clock read past what was drawn to be new:\n%s", out.String())
			}
		})
	}
}

// Drawn otherwise than it was, the clock where a token was: from there
// the task draws anew, and the step, asked otherwise, stops it.
func TestResumingThatDrawsOtherwise(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	runFlow(t, noticeRuntime(t, noticeBuggy, store, clock, &out), store, "n", "Notice", `{"to": "ada"}`)

	clock.Store(2_000_000)
	rt := noticeRuntime(t, noticeEarly, store, clock, &out)
	if err := rt.Resume("n"); err != nil {
		t.Fatal(err)
	}
	idle(t, rt)
	inst, _, _ := store.Get("n")
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "F0102") || !strings.Contains(inst.Stopped, "at 2000000 ") {
		t.Fatalf("expected F0102 with the clock read anew, got %s %q", inst.Status, inst.Stopped)
	}
	if got := strings.Count(out.String(), "WORLD notify"); got != 1 {
		t.Errorf("expected nothing more to leave, got %d:\n%s", got, out.String())
	}
}

// What was drawn is kept across the writes of a wait: the notice waits
// an hour after its stage, then draws, and stops; resumed hours later it
// reads the clock as it did through its wait, and does not wait again.
func TestResumingAfterAWait(t *testing.T) {
	for name, open := range noticeStores(t) {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			clock := handClock(1_000_000)
			rt := noticeRuntime(t, noticeWaits, open(), clock, &out)
			if _, err := rt.Start("n", "Notice", []byte(`{"to": "ada"}`)); err != nil {
				t.Fatal(err)
			}
			due(t, rt)
			inst, _, _ := open().Get("n")
			if inst.Status != flowrt.Waiting {
				t.Fatalf("expected it asleep, got %s %q", inst.Status, inst.Stopped)
			}
			clock.Store(1_000_000 + 3_600_000)
			due(t, rt)
			inst, _, _ = open().Get("n")
			if inst.Status != flowrt.Stopped {
				t.Fatalf("expected it stopped by the mistake, got %s %q", inst.Status, inst.Stopped)
			}

			clock.Store(1_000_000 + 5*3_600_000)
			rt = noticeRuntime(t, noticeVersion("wait time.after(1.hours());", ""), open(), clock, &out)
			if err := rt.Resume("n"); err != nil {
				t.Fatal(err)
			}
			due(t, rt)
			inst, _, _ = open().Get("n")
			if inst.Status != flowrt.Done || !strings.HasPrefix(string(inst.Result), `"sent at 4600000 `) {
				t.Fatalf("expected done with what was sent, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
			}
			if got := strings.Count(out.String(), "WORLD notify"); got != 1 {
				t.Errorf("expected the notice sent once, got %d:\n%s", got, out.String())
			}
		})
	}
}
