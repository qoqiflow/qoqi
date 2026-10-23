package flowrt_test

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// An instance whose row cannot be read is put aside, and told of: the
// others run, and the runtime goes on.
func TestARowThatCannotBeReadStopsNoOther(t *testing.T) {
	at := filepath.Join(t.TempDir(), "flows.db")
	s := openAt(t, at)
	s.ShareAs("a")
	rt := flowrt.New(compiled(t, family), s, &lockedBuffer{})
	rt.Workers = 4
	var mu sync.Mutex
	problems := map[string]int{}
	rt.Problem = func(id string, err error) {
		mu.Lock()
		problems[id]++
		mu.Unlock()
	}
	if _, err := rt.Start("o1", "Order", []byte(`{"$type": "Order", "cart": {"$type": "Cart", "id": "o1", "amount": 5}}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO instances (id, flow, status, wake_at, row) VALUES ('a0', 'Order', 'waiting', 0, '{"birth": 5}')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := rt.RunDue(); err != nil {
			t.Fatalf("the runtime stopped for one row: %v", err)
		}
		o, _, _ := s.Get("o1")
		if o.Status == flowrt.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("o1 is %s, held up by a0", o.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if problems["a0"] == 0 || problems["o1"] != 0 {
		t.Errorf("problems told of: %v", problems)
	}
	if problems["a0"] > 3 {
		t.Errorf("a0 was tried %d times, not put aside", problems["a0"])
	}
}
