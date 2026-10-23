package flowrt_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// unreadable is a store some of whose reads fail, as a database that
// times out: the instance is not gone for that.
type unreadable struct {
	*flowrt.Memory
	mu    sync.Mutex
	of    string
	fails int
}

var errUnread = errors.New("the database did not answer")

func (u *unreadable) Get(id string) (*flowrt.Instance, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if id == u.of && u.fails > 0 {
		u.fails--
		return nil, false, errUnread
	}
	return u.Memory.Get(id)
}

// A read of the store that fails is a fault, never "there is none": the
// parent that waits for its child is not told the child is gone.
func TestAReadThatFailsIsNotAnInstanceGone(t *testing.T) {
	store := &unreadable{Memory: flowrt.NewMemory(), of: "payment:o1", fails: 3}
	rt := flowrt.New(compiled(t, family), store, &lockedBuffer{})
	if _, err := rt.Start("o1", "Order", []byte(`{"$type": "Order", "cart": {"$type": "Cart", "id": "o1", "amount": 5}}`)); err != nil {
		t.Fatal(err)
	}
	faults := 0
	for {
		err := rt.RunUntilIdle(10 * time.Second)
		if err == nil {
			break
		}
		if !errors.Is(err, errUnread) || faults == 10 {
			t.Fatal(err)
		}
		faults++
	}
	if faults == 0 {
		t.Fatal("no read failed")
	}
	o, _, _ := store.Get("o1")
	if o.Status != flowrt.Done || !strings.Contains(string(o.Result), "Ok") {
		t.Errorf("o1 is %s %s %s", o.Status, o.Result, o.Stopped)
	}
	if p, _, _ := store.Get("payment:o1"); p.Status != flowrt.Done {
		t.Errorf("payment:o1 is %s", p.Status)
	}
}
