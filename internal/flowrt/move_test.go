package flowrt_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A conversation that nudges a person who says nothing for two days,
// three times at most, and answers what they say; after an answer, it
// lets an hour pass before it listens again. nudged is what it does when
// the person said nothing; answered, with what they said.
func chat(nudged, answered string) string {
	return `
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

struct Chat {
    who: String;
    messages: Inbox<String> = flow.inbox<String>(8);
}

impl Flow<Int> for Chat {
    fn run() -> Int {
        let nudges = 0;

        while nudges < 3 {
            stage idle;

            let said = wait match {
                case let m = self.messages.receive(): m;
                case time.after(2.days()): null;
            };

            if said == null {
                nudges += 1;
                ` + nudged + `
            } else {
                ` + answered + `
                wait time.after(1.hours());
            }
        }

        return nudges;
    }
}

step fn nudge(who: String, n: Int) {
    println("WORLD nudge", who, n);
}

step fn answer(text: String) {
    println("WORLD answer", text);
}

fn main() {}
`
}

var (
	chatV1 = chat(`nudge(self.who, nudges);`, `answer(said);`)
	// The nudges stopped, in a hurry: an instance that waits to nudge
	// should not nudge once more
	chatPaused = chat(`return -1;`, `answer(said);`)
	// Answers otherwise: an instance that answered has done what this
	// version would not have
	chatLoud = chat(`nudge(self.who, nudges);`, `answer(said + "!");`)
)

const day = 24 * 60 * 60 * 1000

// chatWaiting starts a chat in the first version, and runs it until it waits.
func chatWaiting(t *testing.T, store flowrt.Store, clock *atomic.Int64, out *bytes.Buffer) *flowrt.Instance {
	t.Helper()
	rt := noticeRuntime(t, chatV1, store, clock, out)
	if _, err := rt.Start("c1", "Chat", []byte(`{"who": "ann"}`)); err != nil {
		t.Fatal(err)
	}
	settle(t, rt)
	inst, _, _ := store.Get("c1")
	if inst.Status != flowrt.Waiting || inst.Version != vm.Fingerprint(compiled(t, chatV1)) {
		t.Fatalf("expected it waiting in the first version, got %s %s", inst.Status, inst.Version)
	}
	return inst
}

// A fix deployed while an instance waits reaches it before it wakes: it
// moves at once, still waiting, to the same moment; when that comes, the
// new code runs, and the old one never again.
func TestAnInstanceThatWaitsMovesAtOnce(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	before := chatWaiting(t, store, clock, &out)

	rt := noticeRuntime(t, chatPaused, store, clock, &out)
	moves := rt.MoveAll(false)
	if len(moves) != 1 || !moves[0].Moved || moves[0].From != before.Version || moves[0].Flow != "Chat" {
		t.Fatalf("expected c1 moved, got %+v", moves)
	}
	settle(t, rt)
	inst, _, _ := store.Get("c1")
	if inst.Status != flowrt.Waiting || inst.Version != vm.Fingerprint(rt.Chunk) {
		t.Fatalf("expected it waiting in the newer version, got %s %s %q", inst.Status, inst.Version, inst.Stopped)
	}
	if inst.WakeAt != before.WakeAt {
		t.Errorf("expected it to wait until the moment it waited for, %d, got %d", before.WakeAt, inst.WakeAt)
	}

	clock.Add(2 * day)
	settle(t, rt)
	inst, _, _ = store.Get("c1")
	if inst.Status != flowrt.Done || string(inst.Result) != "-1" {
		t.Fatalf("expected the newer version to end it, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	if got := worldLines(out.String()); len(got) != 0 {
		t.Errorf("expected no nudge, got %v", got)
	}
}

// One not moved by a deploy moves as it is taken up: it does not run the
// version before when it wakes.
func TestAnInstanceMovesAsItIsTakenUp(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	chatWaiting(t, store, clock, &out)

	rt := noticeRuntime(t, chatPaused, store, clock, &out)
	clock.Add(2 * day)
	settle(t, rt)
	inst, _, _ := store.Get("c1")
	if inst.Status != flowrt.Done || string(inst.Result) != "-1" || inst.Version != vm.Fingerprint(rt.Chunk) {
		t.Fatalf("expected the newer version to end it, got %s %s %q %s", inst.Status, inst.Result, inst.Stopped, inst.Version)
	}
	if got := worldLines(out.String()); len(got) != 0 {
		t.Errorf("expected no nudge, got %v", got)
	}
}

// chatAnswered runs a chat in the first version through a message: the
// instance took it and answered it, and lets the hour pass.
func chatAnswered(t *testing.T, store flowrt.Store, clock *atomic.Int64, out *bytes.Buffer) *flowrt.Instance {
	t.Helper()
	chatWaiting(t, store, clock, out)
	rt := noticeRuntime(t, chatV1, store, clock, out)
	if _, err := rt.Send("c1", "messages", "m1", []byte(`"hello"`)); err != nil {
		t.Fatal(err)
	}
	settle(t, rt)
	inst, _, _ := store.Get("c1")
	if inst.Status != flowrt.Waiting || inst.Pass != 1 {
		t.Fatalf("expected it waiting for the hour to pass, at its first pass, got %s %d", inst.Status, inst.Pass)
	}
	return inst
}

// What it did since its stage, the newer version would have done: it
// takes the message again and finds its answer by its key, nothing leaves
// twice, and the message is not taken again once it goes on.
func TestAnInstanceMovesWithWhatItTook(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var out bytes.Buffer
			var store flowrt.Store = flowrt.NewMemory()
			if kind == "sqlite" {
				s, err := flowrt.OpenSQLite(filepath.Join(t.TempDir(), "flows.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				store = s
			}
			clock := handClock(1_000_000)
			before := chatAnswered(t, store, clock, &out)

			rt := noticeRuntime(t, chatPaused, store, clock, &out)
			if moves := rt.MoveAll(false); len(moves) != 1 || !moves[0].Moved {
				t.Fatalf("expected c1 moved, got %+v", moves)
			}
			settle(t, rt)
			inst, _, _ := store.Get("c1")
			if inst.Status != flowrt.Waiting || inst.Version != vm.Fingerprint(rt.Chunk) || inst.WakeAt != before.WakeAt {
				t.Fatalf("expected it waiting in the newer version until %d, got %s %s %d %q", before.WakeAt, inst.Status, inst.Version, inst.WakeAt, inst.Stopped)
			}
			if left := store.Messages("c1", "messages"); len(left) != 0 {
				t.Errorf("expected the message taken still, got %d in the inbox", len(left))
			}

			// The hour, then two days of silence: the newer version ends it
			clock.Add(60 * 60 * 1000)
			settle(t, rt)
			clock.Add(2 * day)
			settle(t, rt)
			inst, _, _ = store.Get("c1")
			if inst.Status != flowrt.Done || string(inst.Result) != "-1" {
				t.Fatalf("expected the newer version to end it, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != "WORLD answer hello" {
				t.Errorf("expected one answer, got %v", got)
			}
		})
	}
}

// A newer version that would not have done what the instance did since
// its stage leaves it on its own version, unwritten, and says why; it
// moves at its next stage.
func TestAnInstanceStaysWhenTheNewerVersionDoesOtherwise(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	before := chatAnswered(t, store, clock, &out)

	rt := noticeRuntime(t, chatLoud, store, clock, &out)
	moves := rt.MoveAll(false)
	if len(moves) != 1 || moves[0].Moved || !strings.Contains(moves[0].Why, "played again from stage idle") || !strings.Contains(moves[0].Why, flowrt.ErrAskedOtherwise) {
		t.Fatalf("expected c1 to stay, asked otherwise, got %+v", moves)
	}
	inst, _, _ := store.Get("c1")
	if inst.Version != before.Version || inst.WakeAt != before.WakeAt || string(inst.State) != string(before.State) {
		t.Fatalf("expected it as it was, got %s %d", inst.Version, inst.WakeAt)
	}

	// Taken up when the hour has passed: refused again, it finishes its
	// pass in its own version, and moves at the stage
	clock.Add(60 * 60 * 1000)
	settle(t, rt)
	inst, _, _ = store.Get("c1")
	if inst.Status != flowrt.Waiting || inst.Version != vm.Fingerprint(rt.Chunk) || inst.Pass != 2 {
		t.Fatalf("expected it moved at its next stage, got %s %s pass %d %q", inst.Status, inst.Version, inst.Pass, inst.Stopped)
	}
	if got := worldLines(out.String()); strings.Join(got, "|") != "WORLD answer hello" {
		t.Errorf("expected one answer, got %v", got)
	}
}

// A newer version that does not take a message the instance took since
// its stage would lose it: the instance stays, and the message is still
// taken.
func TestAnInstanceStaysWhenTheNewerVersionTakesLess(t *testing.T) {
	box := func(first string) string {
		return `
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

struct Box { inbox: Inbox<String> = flow.inbox<String>(8); }

impl Flow<Int> for Box {
    fn run() -> Int {
        stage open;
        let first = ` + first + `;
        wait time.after(1.hours());
        return first.len();
    }
}

fn main() {}
`
	}
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	rt := noticeRuntime(t, box(`wait self.inbox.receive()`), store, clock, &out)
	if _, err := rt.Start("b1", "Box", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	settle(t, rt)
	if _, err := rt.Send("b1", "inbox", "m1", []byte(`"hello"`)); err != nil {
		t.Fatal(err)
	}
	settle(t, rt)

	moves := noticeRuntime(t, box(`"set"`), store, clock, &out).MoveAll(false)
	if len(moves) != 1 || moves[0].Moved || !strings.Contains(moves[0].Why, "does not take again 1 of the messages it took") {
		t.Fatalf("expected b1 to stay, the message not taken again, got %+v", moves)
	}
	if left := store.Messages("b1", "inbox"); len(left) != 0 {
		t.Errorf("expected the message taken still, got %d in the inbox", len(left))
	}
}

// A flow with no stage moves while it waits too, played again from its
// start; a dry run says it would, and writes nothing.
func TestAFlowWithNoStageMoves(t *testing.T) {
	reminder := func(remind string) string {
		return `
import std.flow.{Flow};
import std.time;

struct Reminder { who: String; }

impl Flow<Int> for Reminder {
    fn run() -> Int {
        let at = greet(self.who);
        wait time.after(1.days());
        ` + remind + `
        return at;
    }
}

step fn greet(who: String) -> Int {
    println("WORLD greet", who);
    return 7;
}

step fn remind(text: String) {
    println("WORLD remind", text);
}

fn main() {}
`
	}
	var out bytes.Buffer
	store := flowrt.NewMemory()
	clock := handClock(1_000_000)
	first := noticeRuntime(t, reminder(`remind(self.who);`), store, clock, &out)
	if _, err := first.Start("r1", "Reminder", []byte(`{"who": "ann"}`)); err != nil {
		t.Fatal(err)
	}
	settle(t, first)
	before, _, _ := store.Get("r1")

	rt := noticeRuntime(t, reminder(`remind("${self.who}, again");`), store, clock, &out)
	if moves := rt.MoveAll(true); len(moves) != 1 || !moves[0].Moved {
		t.Fatalf("expected r1 to move, dry, got %+v", moves)
	}
	if moves := flowrt.WouldMove(rt.Chunk, store); len(moves) != 1 || !moves[0].Moved {
		t.Fatalf("expected r1 to move, before a deploy, got %+v", moves)
	}
	if inst, _, _ := store.Get("r1"); inst.Version != before.Version || inst.WakeAt != before.WakeAt {
		t.Fatalf("a dry run wrote: %s %d", inst.Version, inst.WakeAt)
	}

	if moves := rt.MoveAll(false); len(moves) != 1 || !moves[0].Moved {
		t.Fatalf("expected r1 moved, got %+v", moves)
	}
	settle(t, rt)
	if inst, _, _ := store.Get("r1"); inst.WakeAt != before.WakeAt || inst.Version != vm.Fingerprint(rt.Chunk) {
		t.Fatalf("expected it waiting in the newer version until %d, got %d %s", before.WakeAt, inst.WakeAt, inst.Version)
	}
	clock.Add(day)
	settle(t, rt)
	inst, _, _ := store.Get("r1")
	if inst.Status != flowrt.Done || string(inst.Result) != "7" {
		t.Fatalf("expected done with 7, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	if got := worldLines(out.String()); strings.Join(got, "|") != "WORLD greet ann|WORLD remind ann, again" {
		t.Errorf("expected one greeting, then the newer reminder, got %v", got)
	}
}
