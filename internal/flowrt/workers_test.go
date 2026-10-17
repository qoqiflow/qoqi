package flowrt_test

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// lockedBuffer is what several workers print to at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Twenty orders, each with its payment, and a campaign of twelve
// payments, run by eight workers at once: each ends as it does run one
// after another, every step leaves once, and no instance is run by two
// workers at a time.
func TestWorkers(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			out := &lockedBuffer{}
			rt := flowrt.New(compiled(t, family), store, out)
			rt.Workers = 8

			var mu sync.Mutex
			running, most := map[string]bool{}, 0
			rt.Ran = func(step, key string, once bool) {
				mu.Lock()
				defer mu.Unlock()
				running[key[:strings.Index(key, "/")]] = true
				if len(running) > most {
					most = len(running)
				}
			}
			rt.Finished = func() {
				mu.Lock()
				defer mu.Unlock()
				running = map[string]bool{}
			}

			for i := 0; i < 20; i++ {
				amount := 5
				if i%5 == 0 {
					// Refused by its payment
					amount = 5000
				}
				id := fmt.Sprintf("o%d", i)
				fields := `{"$type": "Order", "cart": {"$type": "Cart", "id": "` + id + `", "amount": ` + fmt.Sprint(amount) + `}}`
				if _, err := rt.Start(id, "Order", []byte(fields)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rt.Start("campaign", "Campaign", []byte(`{"count": 12}`)); err != nil {
				t.Fatal(err)
			}
			if err := rt.RunUntilIdle(30 * time.Second); err != nil {
				t.Fatal(err)
			}

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
			if most < 2 {
				t.Errorf("expected instances to run at once, at most %d did", most)
			}
		})
	}
}
