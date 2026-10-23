package flowrt_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// An order in several versions: the first has a mistake after it is
// paid; the second is the fix; the third charges another amount; the
// fourth no longer charges; the fifth names the stage otherwise.
func shopVersion(early, stage, charge, after string) string {
	return `
import std.flow.{Flow};

struct Order { amount: Int; }

impl Flow<String> for Order {
    fn run() -> String {
        let hold = reserve(self.amount);
` + early + `
        stage ` + stage + `;
        let paid = ` + charge + `;
` + after + `
        return ship(paid);
    }
}

step fn reserve(amount: Int) -> String {
    println("WORLD reserve", amount);
    return "hold";
}

step fn charge(hold: String, amount: Int) -> String {
    println("WORLD charge", amount);
    return "paid-" + hold;
}

step fn ship(paid: String) -> String {
    println("WORLD ship", paid);
    return "shipped " + paid;
}

fn main() {}
`
}

const mistake = `let none: [Int] = [];
        println(none[1]);`

var (
	buggy      = shopVersion("", "paying", "charge(hold, self.amount)", mistake)
	fixed      = shopVersion("", "paying", "charge(hold, self.amount)", "")
	otherwise  = shopVersion("", "paying", "charge(hold, self.amount * 2)", "")
	free       = shopVersion("", "paying", `"free"`, "")
	renamed    = shopVersion("", "charging", "charge(hold, self.amount)", "")
	buggyEarly = shopVersion(mistake, "paying", "charge(hold, self.amount)", "")
)

// stoppedIn runs an order in a version until its mistake stops it.
func stoppedIn(t *testing.T, src string, store flowrt.Store, out *bytes.Buffer) {
	t.Helper()
	inst := runFlow(t, flowrt.New(compiled(t, src), store, out), memoryOf(t, store), "o", "Order", `{"amount": 30}`)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "index out of bounds") {
		t.Fatalf("expected it stopped by the mistake, got %s %q", inst.Status, inst.Stopped)
	}
}

func memoryOf(t *testing.T, store flowrt.Store) *flowrt.Memory {
	m, ok := store.(*flowrt.Memory)
	if !ok {
		t.Fatal("a store in memory")
	}
	return m
}

func resumeIn(t *testing.T, src string, store *flowrt.Memory, out *bytes.Buffer) *flowrt.Instance {
	t.Helper()
	rt := flowrt.New(compiled(t, src), store, out)
	if err := rt.Resume("o"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("o")
	return inst
}

// The fix resumed from the stage: what was paid is found by its key,
// and nothing is done twice.
func TestResumingAfterAFix(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	stoppedIn(t, buggy, store, &out)

	inst := resumeIn(t, fixed, store, &out)
	if inst.Status != flowrt.Done || string(inst.Result) != `"shipped paid-hold"` || inst.Replaying {
		t.Fatalf("expected done, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	want := []string{"WORLD reserve 30", "WORLD charge 30", "WORLD ship paid-hold"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("each step once:\nwant %v\ngot  %v", want, got)
	}
}

// A mistake before the first stage: run starts again from its beginning,
// and the steps it had done are found by their keys.
func TestResumingBeforeTheFirstStage(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	stoppedIn(t, buggyEarly, store, &out)

	inst := resumeIn(t, fixed, store, &out)
	if inst.Status != flowrt.Done {
		t.Fatalf("expected done, got %s %q", inst.Status, inst.Stopped)
	}
	if got := strings.Count(out.String(), "WORLD reserve"); got != 1 {
		t.Errorf("expected the reserve once, got %d:\n%s", got, out.String())
	}
}

// What the new code asks otherwise, does not ask for, or cannot reach:
// it stops again, saying so, and nothing leaves.
func TestResumesThatGoWrong(t *testing.T) {
	// Asked otherwise, it stops at the step; what is not asked for is
	// found at the next stage or at the end, after what is new has left:
	// the dry run is where a person sees it before
	for _, c := range []struct {
		name, src, says string
		shipped         bool
	}{
		{"asked otherwise", otherwise, "F0102: o asks charge otherwise than it was asked under", false},
		{"not asked for", free, "F0101: o did something its new version does not account for", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			stoppedIn(t, buggy, store, &out)
			inst := resumeIn(t, c.src, store, &out)
			if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, c.says) {
				t.Fatalf("expected stopped with %q, got %s %q", c.says, inst.Status, inst.Stopped)
			}
			if strings.Count(out.String(), "WORLD charge") != 1 || strings.Contains(out.String(), "WORLD ship") != c.shipped {
				t.Errorf("expected nothing more to leave:\n%s", out.String())
			}
		})
	}

	store := flowrt.NewMemory()
	stoppedIn(t, buggy, store, &bytes.Buffer{})
	err := flowrt.New(compiled(t, renamed), store, &bytes.Buffer{}).Resume("o")
	if err == nil || !strings.Contains(err.Error(), "F0103: stage paying of Order is not in the newest version") {
		t.Errorf("a stage gone: expected F0103, got %v", err)
	}
	if err := flowrt.New(compiled(t, fixed), store, &bytes.Buffer{}).Resume("nobody"); err == nil {
		t.Errorf("no instance: expected a refusal")
	}
}

// A dry run says what a resume would take by key and do next, and
// changes nothing.
func TestADryRun(t *testing.T) {
	store := flowrt.NewMemory()
	stoppedIn(t, buggy, store, &bytes.Buffer{})
	writes := store.Writes()

	dry, err := flowrt.New(compiled(t, fixed), store, &bytes.Buffer{}).DryResume("o")
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Took) != 1 || !strings.HasSuffix(dry.Took[0], "1:0:charge#1") {
		t.Errorf("expected the charge taken by its key, got %v", dry.Took)
	}
	if len(dry.Next) != 1 || dry.Next[0] != `ship ["paid-hold"]` || dry.Stopped != "" {
		t.Errorf("expected the ship to leave next, got %v %q", dry.Next, dry.Stopped)
	}
	if inst, _, _ := store.Get("o"); store.Writes() != writes || inst.Status != flowrt.Stopped {
		t.Errorf("a dry run writes nothing: %d writes, %s", store.Writes()-writes, inst.Status)
	}

	dry, err = flowrt.New(compiled(t, otherwise), store, &bytes.Buffer{}).DryResume("o")
	if err != nil || !strings.Contains(dry.Stopped, "F0102") {
		t.Errorf("a dry run that would stop says why: %v %+v", err, dry)
	}
}

// An undo that gave an Err, resumed: it runs again as its next attempt,
// then the others; ended instead, the instance is done as it was.
const failingUndo = `
import std.flow;
import std.flow.{Flow};

struct Trip { city: String; }

impl Flow<Result<String, String>> for Trip {
    fn run() -> Result<String, String> {
        let seat = hold(self.city);
        let room = book(self.city);
        return Err("no flight to ${self.city}");
    }
}

step fn hold(city: String) -> String {
    flow.undo(free, "seat-" + city);
    return "seat-" + city;
}

step fn book(city: String) -> String {
    flow.undo(cancelRoom, "room-" + city);
    return "room-" + city;
}

step fn free(seat: String) -> Result<Void, String> {
    println("WORLD free", seat);
    return Ok(null);
}

step fn cancelRoom(room: String) -> Result<Void, String> {
    println("WORLD cancel", room, flow.attempt());
    if flow.attempt() == 1 {
        return Err("the hotel does not answer");
    }
    return Ok(null);
}

fn main() {}
`

func TestResumingAnUndo(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(compiled(t, failingUndo), store, &out)
	inst := runFlow(t, rt, store, "trip", "Trip", `{"city": "rome"}`)
	if inst.Status != flowrt.Stopped || inst.Phase != flowrt.Finish || !strings.Contains(inst.Stopped, "the hotel does not answer") {
		t.Fatalf("expected stopped as it ended, got %s %s %q", inst.Status, inst.Phase, inst.Stopped)
	}
	dry, err := rt.DryResume("trip")
	if err != nil || len(dry.Forget) != 1 || len(dry.Next) != 1 || !strings.HasPrefix(dry.Next[0], "undo cancelRoom") {
		t.Errorf("a dry run of an undo: %v %+v", err, dry)
	}

	if err := rt.Resume("trip"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ = store.Get("trip")
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "no flight to rome") {
		t.Fatalf("expected done with its Err, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	want := []string{"WORLD cancel room-rome 1", "WORLD cancel room-rome 2", "WORLD free seat-rome"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}

func TestEnding(t *testing.T) {
	// Stopped as it ended: done with its Err, the undo left as it was
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(compiled(t, failingUndo), store, &out)
	runFlow(t, rt, store, "trip", "Trip", `{"city": "oslo"}`)
	if err := rt.End("trip", "the hotel was called"); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("trip")
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "no flight to oslo") || inst.Ended != "the hotel was called" || strings.Contains(out.String(), "WORLD free") {
		t.Errorf("expected done as it was, no more undos, got %s %s %q\n%s", inst.Status, inst.Result, inst.Ended, out.String())
	}
	if err := rt.End("trip", ""); err == nil || !strings.Contains(err.Error(), "not stopped") {
		t.Errorf("an ended instance: expected a refusal, got %v", err)
	}

	// Stopped by a mistake: failed
	mistaken := flowrt.NewMemory()
	stoppedIn(t, buggy, mistaken, &bytes.Buffer{})
	if err := flowrt.New(compiled(t, buggy), mistaken, &bytes.Buffer{}).End("o", ""); err != nil {
		t.Fatal(err)
	}
	inst, _, _ = mistaken.Get("o")
	if inst.Status != flowrt.Failed || inst.Ended != "a person ended it" {
		t.Errorf("expected failed, got %s %q", inst.Status, inst.Ended)
	}
}
