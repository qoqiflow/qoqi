package flowrt_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// Two daemons share one store, each with four workers: twenty orders
// with their payments and a campaign of twelve, a child run by one and
// its parent by the other. Each ends as it does alone, every step leaves
// once, and both daemons run some.
func TestTwoDaemonsShareAStore(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			out := &lockedBuffer{}
			chunk := compiled(t, family)

			var mu sync.Mutex
			ran := map[string]map[string]bool{}
			daemons := map[string]*flowrt.Runtime{}
			for _, name := range []string{"a", "b"} {
				store := p.open(t, at)
				store.ShareAs(name)
				t.Cleanup(func() { store.Close() })
				rt := flowrt.New(chunk, store, out)
				rt.Workers = 4
				ran[name] = map[string]bool{}
				rt.Ran = func(step, key string, once bool) {
					mu.Lock()
					defer mu.Unlock()
					ran[name][key[:strings.Index(key, "/")]] = true
				}
				daemons[name] = rt
			}

			for i := 0; i < 20; i++ {
				amount := 5
				if i%5 == 0 {
					amount = 5000
				}
				id := fmt.Sprintf("o%d", i)
				fields := `{"$type": "Order", "cart": {"$type": "Cart", "id": "` + id + `", "amount": ` + fmt.Sprint(amount) + `}}`
				// Started on either: any daemon takes it up
				starter := daemons["a"]
				if i%2 == 1 {
					starter = daemons["b"]
				}
				if _, err := starter.Start(id, "Order", []byte(fields)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := daemons["a"].Start("campaign", "Campaign", []byte(`{"count": 12}`)); err != nil {
				t.Fatal(err)
			}

			var wg sync.WaitGroup
			for name, rt := range daemons {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := rt.RunUntilIdle(60 * time.Second); err != nil {
						t.Errorf("daemon %s: %v", name, err)
					}
				}()
			}
			wg.Wait()

			store := p.open(t, at)
			defer store.Close()
			for i := 0; i < 20; i++ {
				inst, _ := store.Get(fmt.Sprintf("o%d", i))
				want := "std.result.Result.Ok"
				if i%5 == 0 {
					want = "too much"
				}
				if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), want) {
					t.Errorf("o%d: expected done with %s, got %s %s %q", i, want, inst.Status, inst.Result, inst.Stopped)
				}
			}
			if campaign, _ := store.Get("campaign"); campaign.Status != flowrt.Done || string(campaign.Result) != "4" {
				t.Errorf("the campaign: expected done with 4, got %s %s %q", campaign.Status, campaign.Result, campaign.Stopped)
			}
			done := map[string]int{}
			for _, line := range worldLines(out.String()) {
				done[line]++
			}
			for line, n := range done {
				if n > 1 {
					t.Errorf("%q done %d times", line, n)
				}
			}
			if len(ran["a"]) == 0 || len(ran["b"]) == 0 {
				t.Errorf("expected both daemons to run instances: a ran %d, b %d", len(ran["a"]), len(ran["b"]))
			}
		})
	}
}

// A daemon that stops holding an instance, too long without a word,
// loses it to another once its lease runs out: the instance is due to
// the other then, not before, and the first one's next save is refused.
func TestALeaseRunsOut(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			a, b := p.open(t, at), p.open(t, at)
			defer a.Close()
			defer b.Close()
			a.ShareAs("a")
			b.ShareAs("b")
			chunk := compiled(t, family)
			if _, err := flowrt.New(chunk, a, nil).Start("o1", "Order", []byte(`{"$type": "Order", "cart": {"$type": "Cart", "id": "o1", "amount": 5}}`)); err != nil {
				t.Fatal(err)
			}

			now := time.Now().UnixMilli()
			if held, err := a.Claim("o1", now, now+300); err != nil || !held {
				t.Fatalf("a: expected to hold o1, got %v %v", held, err)
			}
			if held, _ := b.Claim("o1", now, now+30000); held {
				t.Fatalf("b took o1 while a held it")
			}
			if due := b.Due(now); len(due) != 0 {
				t.Errorf("o1 is due to b while a holds it: %v", due)
			}
			if due := a.Due(now); len(due) != 1 {
				t.Errorf("o1 is not due to a, which holds it: %v", due)
			}

			later := now + 301
			if due := b.Due(later); len(due) != 1 {
				t.Fatalf("o1 is not due to b once a's lease ran out: %v", due)
			}
			if held, err := b.Claim("o1", later, later+30000); err != nil || !held {
				t.Fatalf("b: expected to hold o1 once a's lease ran out, got %v %v", held, err)
			}
			inst, _ := a.Get("o1")
			inst.Status = flowrt.Running
			if err := a.Save(inst); !errors.Is(err, flowrt.ErrNotHeld) {
				t.Errorf("a saved o1, which b holds: %v", err)
			}
			if err := b.Save(inst); err != nil {
				t.Errorf("b cannot save o1, which it holds: %v", err)
			}

			// b runs it to its end, and lets it go
			if err := b.Unclaim("o1"); err != nil {
				t.Fatal(err)
			}
			rt := flowrt.New(chunk, b, nil)
			if err := rt.RunUntilIdle(20 * time.Second); err != nil {
				t.Fatal(err)
			}
			if inst, _ := b.Get("o1"); inst.Status != flowrt.Done {
				t.Errorf("expected b to finish o1, got %s %s", inst.Status, inst.Stopped)
			}
		})
	}
}

// The leases of a daemon that died are let go of by the next one that
// says so: its instances are due at once, not when the leases run out.
func TestTheLeasesOfADeadDaemon(t *testing.T) {
	for _, p := range places(t) {
		t.Run(p.name, func(t *testing.T) {
			at := p.fresh(t)
			dead, next := p.open(t, at), p.open(t, at)
			defer dead.Close()
			defer next.Close()
			dead.ShareAs("here, process 1")
			next.ShareAs("here, process 2")
			chunk := compiled(t, family)
			if _, err := flowrt.New(chunk, dead, nil).Start("o1", "Order", []byte(`{"$type": "Order", "cart": {"$type": "Cart", "id": "o1", "amount": 5}}`)); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UnixMilli()
			dead.Claim("o1", now, now+60000)
			if due := next.Due(now); len(due) != 0 {
				t.Fatalf("o1 is due while the dead one holds it: %v", due)
			}
			if err := next.LetGoOf(func(holder string) bool { return holder == "here, process 1" }); err != nil {
				t.Fatal(err)
			}
			if due := next.Due(now); len(due) != 1 {
				t.Errorf("o1 is not due once the dead one's leases are let go of: %v", due)
			}
		})
	}
}
