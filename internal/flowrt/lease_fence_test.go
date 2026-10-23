package flowrt_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

const charged = `
import std.flow.{Flow};

struct Pay { id: String; }

impl Flow<String> for Pay {
    fn run() -> String {
        let a = charge(self.id);
        println("went on with", a);
        return record(a);
    }
}

step fn charge(id: String) -> String { return "real"; }
step fn record(s: String) -> String { return "recorded " + s; }

fn main() {}
`

// expire ends the lease of an instance in a file of SQLite, as the time
// passing would while the daemon that holds it is paused.
func expire(t *testing.T, at, id string) {
	t.Helper()
	raw, err := sql.Open("sqlite", at)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`UPDATE instances SET held_until = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

// A lease is held as it was taken: once another daemon took it, and even
// once it let it go, the one that lost it holds it no longer, and its
// saves are refused.
func TestALeaseLostIsNotTakenBackUnseen(t *testing.T) {
	at := filepath.Join(t.TempDir(), "flows.db")
	a, b := openAt(t, at), openAt(t, at)
	a.ShareAs("a")
	b.ShareAs("b")
	if _, err := flowrt.New(compiled(t, charged), a, nil).Start("p1", "Pay", []byte(`{"$type": "Pay", "id": "p1"}`)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if held, err := a.Claim("p1", now, now+60000); err != nil || !held {
		t.Fatalf("a holds p1: %v %v", held, err)
	}
	expire(t, at, "p1")
	if held, err := b.Claim("p1", now, now+60000); err != nil || !held {
		t.Fatalf("b takes p1 once a's lease ran out: %v %v", held, err)
	}
	if err := b.Unclaim("p1"); err != nil {
		t.Fatal(err)
	}
	if held, _ := a.Claim("p1", now, now+60000); held {
		t.Errorf("a held p1 longer, as if it had never lost it")
	}
	inst, _, _ := a.Get("p1")
	if err := a.Save(inst); !errors.Is(err, flowrt.ErrNotHeld) {
		t.Errorf("a saved p1 after it lost it: %v", err)
	}
	// Let go of, it is a's to take again as a new lease
	a.Unclaim("p1")
	if held, err := a.Claim("p1", now, now+60000); err != nil || !held {
		t.Errorf("a takes p1 again: %v %v", held, err)
	}
}

// A daemon paused while its step runs, whose instance another daemon
// takes up, runs to its end and lets go of: back, it goes on with what
// the other kept under the step's key, not with what its own step gave,
// and writes nothing of the instance.
func TestADaemonBackFromAPauseWritesNothing(t *testing.T) {
	at := filepath.Join(t.TempDir(), "flows.db")
	a, b := openAt(t, at), openAt(t, at)
	a.ShareAs("a")
	b.ShareAs("b")
	chunk := compiled(t, charged)
	outA, outB := &lockedBuffer{}, &lockedBuffer{}
	ra, rb := flowrt.New(chunk, a, outA), flowrt.New(chunk, b, outB)
	ra.Lease = 600 * time.Millisecond

	release, blocked := make(chan struct{}), make(chan struct{})
	gives := func(value string, wait bool) func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error) {
		return func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error) {
			if wait {
				close(blocked)
				<-release
			}
			v, err := m.ValueOfData([]byte(`"` + value + `"`))
			return v, nil, err
		}
	}
	ra.Replace = map[string]func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error){"charge": gives("from-a", true)}
	rb.Replace = map[string]func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error){"charge": gives("from-b", false)}
	if _, err := ra.Start("p1", "Pay", []byte(`{"$type": "Pay", "id": "p1"}`)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var errA error
	go func() { defer wg.Done(); _, errA = ra.RunDue() }()
	<-blocked

	expire(t, at, "p1")
	if err := rb.RunUntilIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	// a's lease is renewed meanwhile, or tried to be
	time.Sleep(300 * time.Millisecond)
	close(release)
	wg.Wait()
	if errA != nil {
		t.Fatalf("a: %v", errA)
	}

	inst, _, _ := a.Get("p1")
	if inst.Status != flowrt.Done || string(inst.Result) != `"recorded from-b"` {
		t.Errorf("p1 is %s %s, not as b ended it", inst.Status, inst.Result)
	}
	if strings.Contains(outA.String(), "from-a") {
		t.Errorf("a went on with what its own step gave, not with what was kept: %q", outA.String())
	}
	kept, _, _ := a.Result(flowrt.Key(inst, 0, "0", "charge", 1))
	if string(kept.Data) != `"from-b"` {
		t.Errorf("kept under charge: %s", kept.Data)
	}
}

func openAt(t *testing.T, at string) *flowrt.SQL {
	t.Helper()
	s, err := flowrt.OpenSQLite(at)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
