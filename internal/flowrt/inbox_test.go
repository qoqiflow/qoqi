package flowrt_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// An approval told of a decision from outside, and a counter told what
// to add, then to stop, through inboxes.
const inboxes = `
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

enum Decision { Approved(String), Refused(String) }

struct Approval {
    author: String;
    decisions: Inbox<Decision> = flow.inbox<Decision>(1);
}

impl Flow<String> for Approval {
    fn run() -> String {
        let approver = "grace";

        for round in 0..3 {
            stage asking;
            ask(approver);

            let decision = wait match {
                case let d = self.decisions.receive(): d;
                case time.after(50.milliseconds()): null;
            };

            if decision != null {
                match decision {
                    case Approved(by): { return "approved by ${by}"; }
                    case Refused(why): { return "refused: ${why}"; }
                }
            }
            approver = up(approver);
        }

        return "no answer";
    }
}

step fn ask(approver: String) {
    println("WORLD ask", approver);
}

step fn up(name: String) -> String {
    return name + "+";
}

enum Command { Add(Int), Stop }

struct Counter {
    n: Int;
    commands: Inbox<Command> = flow.inbox<Command>(8);
}

impl Flow<Int> for Counter {
    fn run() -> Int {
        let total = self.n;

        while true {
            stage counting;

            wait match {
                case self.commands.receive() is Add(k): {
                    total += record(k);
                }
                case self.commands.receive() is Stop: {
                    return total;
                }
            }
        }

        return total;
    }
}

step fn record(k: Int) -> Int {
    println("WORLD record", k);
    if k == 13 {
        let none: [Int] = [];
        return none[0];
    }
    return k;
}

fn main() {}
`

func approved(by string) string {
	return `{"$type": "Decision.Approved", "$variant": true, "0": "` + by + `"}`
}

func add(k string) string {
	return `{"$type": "Command.Add", "$variant": true, "0": ` + k + `}`
}

const stop = `{"$type": "Command.Stop", "$variant": true}`

// A decision sent while the approval sleeps wakes it; a key sent twice
// is one message; a full inbox, an instance that has ended, an inbox it
// has not are refused.
func TestADecisionFromOutside(t *testing.T) {
	chunk := compiled(t, inboxes)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("e1", "Approval", []byte(`{"author": "lin"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	if inst, _ := store.Get("e1"); inst.Status != flowrt.Waiting {
		t.Fatalf("expected it asleep, got %s %q", inst.Status, inst.Stopped)
	}

	if already, err := rt.Send("e1", "decisions", "mail-1", []byte(approved("grace"))); already || err != nil {
		t.Fatalf("a first send: %v %v", already, err)
	}
	if already, err := rt.Send("e1", "decisions", "mail-1", []byte(approved("grace"))); !already || err != nil {
		t.Errorf("the same key again: expected already, got %v %v", already, err)
	}
	if _, err := rt.Send("e1", "decisions", "mail-2", []byte(approved("ada"))); !errors.Is(err, flowrt.ErrFull) {
		t.Errorf("an inbox of one, full: expected ErrFull, got %v", err)
	}
	if _, err := rt.Send("e1", "orders", "", []byte(`1`)); err == nil || !strings.Contains(err.Error(), "has no inbox orders") {
		t.Errorf("no such inbox: got %v", err)
	}

	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("e1")
	if inst.Status != flowrt.Done || string(inst.Result) != `"approved by grace"` {
		t.Fatalf("expected approved, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	if _, err := rt.Send("e1", "decisions", "mail-3", []byte(approved("ada"))); !errors.Is(err, flowrt.ErrEnded) {
		t.Errorf("an instance that has ended: expected ErrEnded, got %v", err)
	}
	if got := strings.Count(out.String(), "WORLD ask"); got != 1 {
		t.Errorf("asked once, got %d:\n%s", got, out.String())
	}
}

// Without an answer, it goes up three times.
func TestNoDecision(t *testing.T) {
	chunk := compiled(t, inboxes)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &out), store, "e2", "Approval", `{"author": "lin"}`)
	if inst.Status != flowrt.Done || string(inst.Result) != `"no answer"` {
		t.Fatalf("expected no answer, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	want := []string{"WORLD ask grace", "WORLD ask grace+", "WORLD ask grace++"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}

// Commands sent before the counter runs, each taken once, the cases of
// the wait match telling them apart; stopped at every write, it ends the
// same and records each once, or twice when stopped between doing it and
// keeping what it gave.
func TestCommandsSurviveAStopAtEveryWrite(t *testing.T) {
	everyStopOf(t, inboxes, func(rt *flowrt.Runtime) error {
		inst, err := rt.Start("o", "Counter", []byte(`{"n": 100}`))
		if err != nil {
			return err
		}
		if inst.Status != flowrt.Waiting || inst.State != nil {
			// Started before, by the process that stopped: sent then too
			return nil
		}
		for _, command := range []string{add("1"), add("2"), add("3"), stop} {
			if _, err := rt.Send("o", "commands", "c"+command, []byte(command)); err != nil {
				return err
			}
		}
		return nil
	}, flowrt.Done, []string{"WORLD record 1", "WORLD record 2", "WORLD record 3"})
}

// A counter stopped by a mistake after it took a message: resumed, it
// takes the message again, from its stage.
func TestAResumeTakesItsMessagesAgain(t *testing.T) {
	chunk := compiled(t, inboxes)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("c", "Counter", []byte(`{"n": 0}`)); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{add("5"), add("13")} {
		if _, err := rt.Send("c", "commands", "", []byte(command)); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.RunUntilIdle(time.Second); err == nil {
		inst, _ := store.Get("c")
		if inst.Status != flowrt.Stopped {
			t.Fatalf("expected it stopped on 13, got %s %q", inst.Status, inst.Stopped)
		}
	}
	if len(store.Messages("c", "commands")) != 0 {
		t.Fatalf("expected both messages taken")
	}

	// The record of 13 is fixed by hand: the step ran and failed, it runs
	// again; the message taken after the stage is given back
	if err := rt.Resume("c"); err != nil {
		t.Fatal(err)
	}
	if got := len(store.Messages("c", "commands")); got != 1 {
		t.Errorf("expected the message taken since the stage given back, got %d", got)
	}
}

// On a file of SQLite, stopped at every write: the commands sent before
// it ran are taken once each, and it ends the same.
func TestSQLiteCommands(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) { commandsKept(t, p) })
	}
}

func commandsKept(t *testing.T, p place) {
	chunk := compiled(t, inboxes)
	store := p.open(t, p.fresh(t))
	defer store.Close()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})
	if _, err := rt.Start("c", "Counter", []byte(`{"n": 10}`)); err != nil {
		t.Fatal(err)
	}
	for i, command := range []string{add("1"), add("2"), stop} {
		if already, err := rt.Send("c", "commands", "k"+string(rune('0'+i)), []byte(command)); already || err != nil {
			t.Fatalf("send %d: %v %v", i, already, err)
		}
	}
	if already, _ := rt.Send("c", "commands", "k0", []byte(add("1"))); !already {
		t.Errorf("a key sent before: expected already")
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("c")
	if inst.Status != flowrt.Done || string(inst.Result) != "13" || len(store.Messages("c", "commands")) != 0 {
		t.Errorf("expected done with 13, every message taken, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
}

// A message that came while the instance ran is not slept through: the
// save that puts it to sleep on that inbox makes it due, on either store.
func TestAMessageIsNotSleptThrough(t *testing.T) {
	for name, store := range stores(t) {
		rt := flowrt.New(compiled(t, inboxes), store, &bytes.Buffer{})
		inst, err := rt.Start("e", "Approval", []byte(`{"author": "lin"}`))
		if err != nil {
			t.Fatal(err)
		}
		inst.Status = flowrt.Running
		store.Save(inst)
		if _, err := rt.Send("e", "decisions", "", []byte(approved("ada"))); err != nil {
			t.Fatal(err)
		}
		inst.Status, inst.WakeAt, inst.Awaits = flowrt.Waiting, flowrt.Never, []string{"inbox:decisions"}
		if err := store.Save(inst); err != nil {
			t.Fatal(err)
		}
		if due := store.Due(rt.Now()); len(due) != 1 {
			t.Errorf("%s: expected it due at once, got %v", name, due)
		}
	}
}
