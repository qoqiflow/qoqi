package flowrt_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

const deferring = `
import std.flow.{Flow};

struct Tidy { fails: Bool; }

step fn ping(n: Int) -> Int { return n + 1; }

impl Flow<Int> for Tidy {
    fn run() -> Int {
        defer println("tidied");
        let t = ping(1);
        if self.fails {
            let none: [Int] = [];
            return none[t];
        }
        return t;
    }
}

fn main() {}
`

// A flow that deferred something waits for its step as any other, and
// runs what it deferred once it is taken up: at its end, or when what
// follows fails.
func TestAFlowThatDefersWaitsForItsSteps(t *testing.T) {
	for _, c := range []struct {
		fails  bool
		status flowrt.Status
	}{{false, flowrt.Done}, {true, flowrt.Stopped}} {
		store := flowrt.NewMemory()
		var out bytes.Buffer
		rt := flowrt.New(compiled(t, deferring), store, &out)
		fields := `{"fails": false}`
		if c.fails {
			fields = `{"fails": true}`
		}
		if _, err := rt.Start("t", "Tidy", []byte(fields)); err != nil {
			t.Fatal(err)
		}
		if err := rt.RunUntilIdle(10 * time.Second); err != nil {
			t.Fatal(err)
		}
		inst, _, _ := store.Get("t")
		if inst.Status != c.status || strings.Contains(inst.Stopped, "freeze") {
			t.Errorf("fails %v: t is %s %s", c.fails, inst.Status, inst.Stopped)
		}
		if strings.Count(out.String(), "tidied") != 1 {
			t.Errorf("fails %v: printed %q", c.fails, out.String())
		}
	}
}
