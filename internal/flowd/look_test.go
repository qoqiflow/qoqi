package flowd_test

import (
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// counting is a store that counts how it is asked for instances.
type counting struct {
	*flowrt.Memory
	all, changed int
	since        int64
}

func (c *counting) List(flow string, status flowrt.Status) []*flowrt.Instance {
	c.all++
	return c.Memory.List(flow, status)
}

func (c *counting) ChangedSince(since int64) []*flowrt.Instance {
	c.changed++
	c.since = since
	return c.Memory.ChangedSince(since)
}

// A daemon looks at every instance once, then at those changed since its
// last look, a little before it; and at every instance again each minute.
func TestLookAtWhatChanged(t *testing.T) {
	store := &counting{Memory: flowrt.NewMemory()}
	d, err := flowd.NewOf([]flowd.Program{{Name: "p", Runtime: flowrt.New(nil, store, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	p := d.Programs[0]

	d.Look(p)
	if store.all != 1 || store.changed != 0 {
		t.Fatalf("the first look is at every instance: all %d, changed %d", store.all, store.changed)
	}

	before := time.Now().UnixMilli()
	for range 20 {
		d.Look(p)
	}
	if store.all != 1 || store.changed != 20 {
		t.Fatalf("then at those changed: all %d, changed %d", store.all, store.changed)
	}
	if store.since > before || store.since < before-10_000 {
		t.Errorf("since the last look, a few seconds before it: %d for a look at %d", store.since, before)
	}

	d.AgeLooks(time.Minute)
	d.Look(p)
	if store.all != 2 {
		t.Errorf("a minute later, at every instance again: all %d", store.all)
	}
}
