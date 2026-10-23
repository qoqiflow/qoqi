package flowrt_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A subscription billed each month, a stage at the top of each: the keys
// of its steps say which pass they are of, and the instance keeps where
// it can be taken up from. A flow whose other task is alive at a stage
// cannot be taken up from it.
const billing = `
import std.flow.{Flow};
import std.task;
import std.time;

struct Billing { months: Int; }

impl Flow<Int> for Billing {
    fn run() -> Int {
        let paid = 0;

        for month in 0..self.months {
            stage month;
            paid += bill(month);
            wait time.after(10.milliseconds());
        }

        return paid;
    }
}

step fn bill(month: Int) -> Int {
    println("WORLD bill", month);
    return 100 + month;
}

struct Busy { n: Int; }

impl Flow<Int> for Busy {
    fn run() -> Int {
        let helper = task.start(fn() -> Int {
            wait time.after(30.milliseconds());
            return 1;
        });

        stage early;
        wait time.after(5.milliseconds());

        let got = (wait helper).expect("the helper");
        stage late;
        wait time.after(5.milliseconds());
        return got + self.n;
    }
}

fn main() {}
`

func TestStagesInTheKeys(t *testing.T) {
	chunk := compiled(t, billing)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	inst := runFlow(t, flowrt.New(chunk, store, &out), store, "o", "Billing", `{"months": 3}`)
	if inst.Status != flowrt.Done || string(inst.Result) != "303" {
		t.Fatalf("expected done with 303, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	prefix := "o/" + inst.Birth + "/"
	want := []string{prefix + "1:0:bill#1", prefix + "2:0:bill#1", prefix + "3:0:bill#1"}
	if got := strings.Join(store.ResultLog, " "); got != strings.Join(want, " ") {
		t.Errorf("expected a key per pass, its count starting again:\nwant %v\ngot  %v", want, store.ResultLog)
	}
	if inst.Pass != 3 || inst.AtStage == nil || inst.AtStage.Name != "month" || inst.AtStage.Pass != 3 {
		t.Fatalf("expected it at stage month, pass 3, got %d %+v", inst.Pass, inst.AtStage)
	}
	if string(inst.AtStage.Vars["paid"]) != "201" || string(inst.AtStage.Vars["month"]) != "2" {
		t.Errorf("expected the variables of the last pass, got %s", varsOfStage(inst.AtStage))
	}
}

func varsOfStage(at *flowrt.AtStage) string {
	var parts []string
	for name, data := range at.Vars {
		parts = append(parts, name+"="+string(data))
	}
	return strings.Join(parts, " ")
}

// The stage where another task is alive is passed, and counted, but the
// instance is not kept as at it; the next one, run alone, is.
func TestAStageWithAnotherTaskAlive(t *testing.T) {
	chunk := compiled(t, billing)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})
	if _, err := rt.Start("busy", "Busy", []byte(`{"n": 41}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("busy")
	if inst.Pass != 1 || inst.AtStage != nil {
		t.Errorf("at the first stage, a task alive: expected pass 1 and no stage kept, got %d %+v", inst.Pass, inst.AtStage)
	}
	inst = runFlow(t, rt, store, "busy", "Busy", `{"n": 41}`)
	if inst.Status != flowrt.Done || string(inst.Result) != "42" || inst.AtStage == nil || inst.AtStage.Name != "late" {
		t.Errorf("expected done with 42, at stage late, got %s %s %+v", inst.Status, inst.Result, inst.AtStage)
	}
}

// Stopped at any write, the billing ends the same, each month billed.
func TestStagesSurviveAStopAtEveryWrite(t *testing.T) {
	everyStopOf(t, billing, func(rt *flowrt.Runtime) error {
		_, err := rt.Start("o", "Billing", []byte(`{"months": 3}`))
		return err
	}, flowrt.Done, []string{"WORLD bill 0", "WORLD bill 1", "WORLD bill 2"})
}
