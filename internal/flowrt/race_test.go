package flowrt_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A parent starts to wait for its child as the child ends, in two
// writes at the same time, again and again: whichever lands first, the
// parent is due once both have, on every kind of database. On a server,
// where two writes can run at once, it is the transactions being
// serializable that keeps the parent from sleeping for ever.
func TestAWaitForAChildThatEndsMeanwhile(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for round := 0; round < 40; round++ {
				parent := &flowrt.Instance{ID: fmt.Sprintf("p%d", round), Birth: "b", Flow: "Parent", Status: flowrt.Running, Fields: []byte(`{}`)}
				child := &flowrt.Instance{ID: fmt.Sprintf("c%d", round), Birth: "b", Flow: "Child", Status: flowrt.Running, Fields: []byte(`{}`), Parent: parent.ID}
				for _, inst := range []*flowrt.Instance{parent, child} {
					if _, _, err := store.Create(inst); err != nil {
						t.Fatal(err)
					}
				}

				waiting := *parent
				waiting.Status, waiting.WakeAt, waiting.Awaits = flowrt.Waiting, 1<<40, []string{child.ID}
				ended := *child
				ended.Status, ended.Result = flowrt.Done, []byte(`1`)

				var wg sync.WaitGroup
				errs := make([]error, 2)
				for i, inst := range []*flowrt.Instance{&waiting, &ended} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						errs[i] = store.Save(inst)
					}()
				}
				wg.Wait()
				for _, err := range errs {
					if err != nil {
						t.Fatalf("round %d: %v", round, err)
					}
				}
				if got, _ := store.Get(parent.ID); got.Status != flowrt.Waiting || got.WakeAt != 0 {
					t.Fatalf("round %d: the parent sleeps through its child's end: %s, due at %d", round, got.Status, got.WakeAt)
				}
			}
		})
	}
}

// Sends to one inbox from many at once: each key is kept once, and no
// more messages than the inbox holds wait in it.
func TestSendsAtOnce(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			inst := &flowrt.Instance{ID: "i", Birth: "b", Flow: "Inbox", Status: flowrt.Waiting, WakeAt: 1 << 40, Fields: []byte(`{}`), Awaits: []string{"inbox:in"}}
			if _, _, err := store.Create(inst); err != nil {
				t.Fatal(err)
			}
			const capacity = 5
			var mu sync.Mutex
			kept, already, full := map[string]int{}, 0, 0
			var wg sync.WaitGroup
			for i := 0; i < 24; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// Three senders for each key
					key := fmt.Sprintf("k%d", i/3)
					again, err := store.Send("i", "in", key, []byte(`"`+key+`"`), capacity)
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == flowrt.ErrFull:
						full++
					case err != nil:
						t.Errorf("%s: %v", key, err)
					case again:
						already++
					default:
						kept[key]++
					}
				}()
			}
			wg.Wait()
			messages := store.Messages("i", "in")
			if len(messages) != capacity || len(kept) != capacity {
				t.Errorf("expected the inbox full with %d keys, got %d messages, kept %v", capacity, len(messages), kept)
			}
			for key, n := range kept {
				if n != 1 {
					t.Errorf("%s kept %d times", key, n)
				}
			}
			if len(kept)+already+full != 24 {
				t.Errorf("expected every send answered once: %d kept, %d already, %d full", len(kept), already, full)
			}
			if got, _ := store.Get("i"); got.WakeAt != 0 {
				t.Errorf("expected it due once a message came, due at %d", got.WakeAt)
			}
		})
	}
}
