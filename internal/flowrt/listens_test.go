package flowrt_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A request that waits for an answer and hears its own cancellation: it
// ends its own way. One whose answer is a refund undoes what it did; one
// that sleeps in a plain wait first is cancelled as a flow that does not
// listen.
const listening = `
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

struct Request {
    who: String;
    refund: Bool;
    nap: Int;
    answers: Inbox<String> = flow.inbox<String>(1);
}

impl Flow<Result<String, String>> for Request {
    fn run() -> Result<String, String> {
        hold(self.who);
        if self.nap > 0 {
            wait time.after(self.nap.milliseconds());
        }

        wait match {
            case let a = self.answers.receive(): {
                return Ok(a);
            }
            case let reason = flow.cancelled(): {
                note(self.who, reason);
                if self.refund {
                    return Err(reason);
                }
                return Ok("withdrawn: " + reason);
            }
        }

        return Err("no answer");
    }
}

step fn hold(who: String) {
    println("WORLD hold", who);
    flow.undo(release, who);
}

step fn note(who: String, reason: String) {
    println("WORLD note", who, reason);
}

step fn release(who: String) -> Result<Void, String> {
    println("WORLD release", who);
    return Ok(null);
}

fn main() {}
`

func TestAFlowHearsItsCancellation(t *testing.T) {
	for _, c := range []struct {
		name, fields string
		status       flowrt.Status
		result       string
		world        []string
	}{
		{"it ends its own way", `{"who": "ada", "refund": false, "nap": 0}`, flowrt.Done, `{"$type":"std.result.Result.Ok","$variant":true,"0":"withdrawn: no longer needed"}`,
			[]string{"WORLD hold ada", "WORLD note ada no longer needed"}},
		{"its Err undoes", `{"who": "lin", "refund": true, "nap": 0}`, flowrt.Done, `{"$type":"std.result.Result.Err","$variant":true,"0":"no longer needed"}`,
			[]string{"WORLD hold lin", "WORLD note lin no longer needed", "WORLD release lin"}},
		{"waiting elsewhere, it is stopped where it is", `{"who": "bo", "refund": false, "nap": 3600000}`, flowrt.Failed, ``,
			[]string{"WORLD hold bo", "WORLD release bo"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			rt := flowrt.New(compiled(t, listening), store, &out)
			if _, err := rt.Start("r", "Request", []byte(c.fields)); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RunDue(); err != nil {
				t.Fatal(err)
			}
			if err := rt.Cancel("r", "no longer needed"); err != nil {
				t.Fatal(err)
			}
			if err := rt.RunUntilIdle(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			inst, _, _ := store.Get("r")
			if inst.Status != c.status || string(inst.Result) != c.result {
				t.Fatalf("expected %s with %s, got %s %s %q", c.status, c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(c.world, "|") {
				t.Errorf("want %v\ngot  %v", c.world, got)
			}
		})
	}
}

// Cancelled before it ran at all, and stopped at every write: it hears
// the cancellation at its wait match, and ends the same each time.
func TestHearingSurvivesAStopAtEveryWrite(t *testing.T) {
	everyStopOf(t, listening, func(rt *flowrt.Runtime) error {
		if _, err := rt.Start("o", "Request", []byte(`{"who": "ada", "refund": true, "nap": 0}`)); err != nil {
			return err
		}
		return rt.Cancel("o", "gone")
	}, flowrt.Done, []string{"WORLD hold ada", "WORLD note ada gone", "WORLD release ada"})
}
