package flowrt_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

const startsAChild = `
import std.flow;
import std.flow.{Flow, Instance};
import std.random;

struct Child { n: Int; }
impl Flow<Int> for Child {
    fn run() -> Int { return work(self.n); }
}
step fn work(n: Int) -> Int { println("work", n); return n; }

struct Parent { n: Int; }
impl Flow<Int> for Parent {
    fn run() -> Int {
        let c = flow.start("child-" + random.uuid(), Child { n: self.n });
        match wait c {
            case Ok(v): { return v; }
            case Err(_): { return -1; }
        }
    }
}
fn main() {}
`

// A child is made with the write of the instance that started it: a
// process that stops before that write leaves no child behind, which
// would do its work beside the one the parent, taken up, starts again
// under another name.
func TestAChildIsMadeWithItsParentsWrite(t *testing.T) {
	chunk := compiled(t, startsAChild)
	crashed := 0
	for k := 1; k <= 8; k++ {
		var out bytes.Buffer
		store := flowrt.NewMemory()
		store.FailAt = k
		first := flowrt.New(chunk, store, &out)
		_, err := first.Start("p", "Parent", []byte(`{"$type": "Parent", "n": 7}`))
		if err == nil {
			err = first.RunUntilIdle(5 * time.Second)
		}
		if !errors.Is(err, flowrt.ErrCrash) {
			continue
		}
		if _, made, _ := store.Get("p"); !made {
			// Stopped before the parent was made: nothing to take up
			continue
		}
		crashed++
		store.FailAt = 0
		if err := flowrt.New(chunk, store, &out).RunUntilIdle(5 * time.Second); err != nil {
			t.Fatalf("stopped at write %d, taken up: %v", k, err)
		}
		if children := store.List("Child", ""); len(children) != 1 {
			t.Errorf("stopped at write %d: %d children for one parent", k, len(children))
		}
		if p, _, _ := store.Get("p"); p.Status != flowrt.Done || string(p.Result) != "7" {
			t.Errorf("stopped at write %d: the parent is %s %s", k, p.Status, p.Result)
		}
	}
	if crashed < 3 {
		t.Fatalf("the process stopped at %d writes only", crashed)
	}
}
