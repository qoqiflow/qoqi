package flowrt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Where the tasks of an instance asleep are, read from what the store
// keeps: the console shows each at its line, with the calls that led
// there. And what its steps returned, listed by the prefix of its keys.

const crew = `import std.flow.{Flow};
import std.task;
import std.time;

struct Crew {
    rounds: Int;
}

impl Flow<Int> for Crew {
    fn run() -> Int {
        let rounds = self.rounds;

        let loader = task.run(fn() {
            haul(rounds);
        });

        let tally = count(rounds);
        wait time.after(2.hours());
        return tally;
    }
}

fn haul(rounds: Int) {
    for i in 0..rounds {
        wait time.after(1.hours());
    }
}

step fn count(n: Int) -> Int {
    return n * 2;
}

fn main() {}
`

const (
	crewRunWait  = 18 // wait time.after(2.hours());
	crewHaulCall = 14 // haul(rounds);
	crewHaulWait = 25 // wait time.after(1.hours());
)

func TestWhereTheTasksOfAnInstanceAre(t *testing.T) {
	chunk := compiled(t, crew)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, nil)
	if _, err := rt.Start("crew:1", "Crew", []byte(`{"$type": "Crew", "rounds": 3}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("crew:1")
	if inst.Status != flowrt.Waiting {
		t.Fatalf("expected it asleep, got %s %s", inst.Status, inst.Stopped)
	}
	places, err := vm.Where(chunk, inst.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 2 {
		t.Fatalf("expected run and the loader, got %+v", places)
	}

	own := func(p vm.Place) []vm.PlaceFrame {
		var frames []vm.PlaceFrame
		for _, f := range p.Frames {
			if f.File == "flows.qi" {
				frames = append(frames, f)
			}
		}
		return frames
	}
	run, loader := places[0], places[1]
	if run.Task != "0" || run.Waiting != "suspendFor" || !run.Started {
		t.Errorf("task 0 is run, asleep: %+v", run)
	}
	if f := own(run); len(f) != 1 || f[0].Function != "Crew.run" || f[0].Line != crewRunWait {
		t.Errorf("run waits at line %d: %+v", crewRunWait, f)
	}
	if loader.Task != "0/task#1" || loader.Until <= run.Until-2*time.Hour.Milliseconds() || loader.Until >= run.Until {
		t.Errorf("the loader wakes before run: %+v / %+v", loader, run)
	}
	f := own(loader)
	if len(f) != 2 || f[0].Function != "haul" || f[0].Line != crewHaulWait || f[1].Line != crewHaulCall {
		t.Errorf("the loader waits in haul at %d, called from the lambda at %d: %+v", crewHaulWait, crewHaulCall, f)
	}
	if !strings.HasPrefix(f[1].Function, "lambda") {
		t.Errorf("the lambda the task runs: %q", f[1].Function)
	}

	// The library's calls come first, innermost: hidden by the pages
	if run.Frames[0].File != "<std>/task.qi" {
		t.Errorf("expected the library's frames first: %+v", run.Frames)
	}

	if _, err := vm.Where(chunk, []byte("not a program")); err == nil {
		t.Errorf("a state that is not a frozen program is refused")
	}
}

// The results of one instance, whatever other instances have names that
// begin as its own: order:1 is not order:10, nor order:1/b.
func TestResultsOfOneInstance(t *testing.T) {
	all := map[string]interface {
		flowrt.Store
		flowrt.Lister
	}{"memory": flowrt.NewMemory()}
	for name, store := range openFresh(t) {
		all[name] = store
	}
	for name, store := range all {
		chunk := compiled(t, crew)
		rt := flowrt.New(chunk, store, nil)
		for _, id := range []string{"order:1", "order:10", "order:1/b"} {
			if _, err := rt.Start(id, "Crew", []byte(`{"$type": "Crew", "rounds": 2}`)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := rt.RunDue(); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"order:1", "order:10", "order:1/b"} {
			inst, _, _ := store.Get(id)
			kept := store.Results(flowrt.Prefix(inst))
			if len(kept) != 1 || !strings.HasPrefix(kept[0].Key, id+"/"+inst.Birth+"/") || string(kept[0].Result) != "4" {
				t.Errorf("%s: the results of %s alone, got %+v", name, id, kept)
			}
		}
		if all := store.Results(""); len(all) != 3 {
			t.Errorf("%s: every result under the empty prefix, got %d", name, len(all))
		}
	}
}
