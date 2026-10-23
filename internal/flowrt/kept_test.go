package flowrt_test

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// bank is an application that keeps the answers of its wires under their
// keys, as the library's Keeper does: a wire sent again under a key is
// not done again, and a probe is told what became of it. The first
// response it gives a wire is lost when lose says so, as on a network
// that cuts after the request left.
type bank struct {
	mu    sync.Mutex
	kept  map[string]string // the answer of each key
	wires map[string]int    // how many times each key was wired
	lose  bool
	probe string // what a probe of an unseen key is told; unseen when ""
}

const wired = `{"value": {"$type": "std.flow.Settled.Done", "$variant": true, "0": {"$type": "std.result.Result.Ok", "$variant": true, "0": "w-3"}}}`

func (b *bank) answer(d delivery, n int) (int, string) {
	if d.Step != "wire" {
		return working(d, n)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if d.Probe {
		if said, ok := b.kept[d.Key]; ok {
			return 200, said
		}
		if b.probe != "" {
			return 200, b.probe
		}
		return 200, `{"unseen": true}`
	}
	if said, ok := b.kept[d.Key]; ok {
		return 200, said
	}
	b.wires[d.Key]++
	b.kept[d.Key] = wired
	if b.lose {
		b.lose = false
		return 502, `bad gateway`
	}
	return 200, wired
}

func (b *bank) wired() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, k := range b.wires {
		n += k
	}
	return n
}

// keptAs is the manifest of a shop whose wires are kept as said.
func keptAs(t *testing.T, kind string) string {
	shapes := `"` + shapeOf(t, "reserve") + `", "` + shapeOf(t, "charge") + `", "` + shapeOf(t, "notify") + `", "` + shapeOf(t, "wire") + `"`
	return `{"shapes": [` + shapes + `], "` + kind + `": ["` + shapeOf(t, "wire") + `"]}`
}

// A Settled step that may have arrived is not Unknown when its queue
// keeps its answers: under once it is sent again, and the application
// answers what it did; under started it is asked of, and its answer
// taken without sending it again; asked of what it never saw, it is sent
// again; asked of what died as it acted, it is Unknown.
func TestASettledStepItsQueueKeeps(t *testing.T) {
	for _, c := range []struct {
		name, kind, probe, result string
		wires                     int
		sent                      string
	}{
		{"under once, sent again", "once", "", "wired w-3", 1, "reserve#1 charge#1 notify#1 wire#1 wire#2"},
		{"under started, asked of", "started", "", "wired w-3", 1, "reserve#1 charge#1 notify#1 wire#1 wire#1"},
		{"kept nowhere, Unknown", "neither", "", "wire unknown", 1, "reserve#1 charge#1 notify#1 wire#1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := &bank{kept: map[string]string{}, wires: map[string]int{}, lose: true, probe: c.probe}
			l := servedLater(t, flowrt.NewMemory(), b.answer)
			l.app.manifest = keptAs(t, c.kind)
			if _, err := l.rt.Start("o", "Order", []byte(shopOrder(true))); err != nil {
				t.Fatal(err)
			}
			inst := l.runFor(10)
			if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), c.result) {
				t.Fatalf("expected %s, got %s %s %q", c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if b.wired() != c.wires {
				t.Errorf("wired %d times, expected %d", b.wired(), c.wires)
			}
			if got := l.app.steps(); got != c.sent {
				t.Errorf("expected %s, got %s", c.sent, got)
			}
		})
	}
}

// Asked of a wire it never saw, the application is sent it again; asked
// of one a process died in the middle of, the flow is told Unknown.
func TestAProbe(t *testing.T) {
	for _, c := range []struct{ name, probe, result string }{
		{"unseen: sent again", "", "wired w-3"},
		{"unknown: Unknown", `{"unknown": true}`, "wire unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := &bank{kept: map[string]string{}, wires: map[string]int{}, probe: c.probe}
			l := servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
				if d.Step == "wire" && !d.Probe && n == 1 {
					// Lost on the way, never seen
					return 502, `bad gateway`
				}
				return b.answer(d, n)
			})
			l.app.manifest = keptAs(t, "started")
			if _, err := l.rt.Start("o", "Order", []byte(shopOrder(true))); err != nil {
				t.Fatal(err)
			}
			inst := l.runFor(10)
			if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), c.result) {
				t.Fatalf("expected %s, got %s %s %q", c.result, inst.Status, inst.Result, inst.Stopped)
			}
		})
	}
}

// Stopped at every write, a shop whose wires are kept under started ends
// wired every time, the wire done once: what was sent by the process that
// stopped is asked of, not taken as Unknown.
func TestAKeptStepSurvivesAStopAtEveryWrite(t *testing.T) {
	chunk := compiledWithQueues(t, shop, "warehouse", "billing", "bank")
	run := func(b *bank, store *flowrt.Memory) error {
		app := &application{t: t, answer: b.answer, manifest: keptAs(t, "started")}
		queues := map[string]flowrt.Queue{}
		for _, name := range []string{"warehouse", "billing", "bank"} {
			server := httptest.NewServer(app.handler(name))
			t.Cleanup(server.Close)
			queues[name] = flowrt.Queue{URL: server.URL, Secret: shopSecret}
		}
		rt := flowrt.New(chunk, store, &bytes.Buffer{})
		q := flowrt.NewHTTPQueues(queues)
		q.Backoff = 5 * time.Millisecond
		rt.Queues = q
		if _, err := rt.Start("o", "Order", []byte(shopOrder(true))); err != nil {
			return err
		}
		return rt.RunUntilIdle(10 * time.Second)
	}
	plain := flowrt.NewMemory()
	if err := run(&bank{kept: map[string]string{}, wires: map[string]int{}}, plain); err != nil {
		t.Fatal(err)
	}
	clean, _, _ := plain.Get("o")
	if !strings.Contains(string(clean.Result), "wired w-3") {
		t.Fatalf("expected wired, got %s %q", clean.Result, clean.Stopped)
	}
	for k := 1; k <= plain.Writes(); k++ {
		b := &bank{kept: map[string]string{}, wires: map[string]int{}}
		store := flowrt.NewMemory()
		store.FailAt = k
		if err := run(b, store); err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		store.FailAt = 0
		if err := run(b, store); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		inst, _, _ := store.Get("o")
		if string(inst.Result) != string(clean.Result) {
			t.Errorf("stopped at write %d: expected %s, got %s %s %q", k, clean.Result, inst.Status, inst.Result, inst.Stopped)
		}
		if b.wired() != 1 {
			t.Errorf("stopped at write %d: wired %d times", k, b.wired())
		}
	}
}

// A plain step the application could not tell of stops its instance,
// naming its key. A person answers what it did, and the instance goes on
// with it, the step not sent again; or says it did nothing, and the step
// is sent again reclaiming its dead claim.
func TestAStepAnsweredByHand(t *testing.T) {
	for _, c := range []struct {
		name    string
		nothing bool
		value   string
		sent    string
	}{
		{"what it did", false, `{"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Charge", "id": "ch-by-hand"}}`, "reserve#1 charge#1 notify#1"},
		{"nothing", true, ``, "reserve#1 charge#1 charge#2 notify#1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var reclaimed int
			l := servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
				if d.Step == "charge" && n == 1 {
					return 200, `{"unknown": true}`
				}
				if d.Step == "charge" {
					reclaimed = d.Reclaim
				}
				return working(d, n)
			})
			if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
				t.Fatal(err)
			}
			inst := l.runFor(3)
			key := l.sent("charge").Key
			if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "qoqi answer") || !strings.Contains(inst.Stopped, key) {
				t.Fatalf("expected it stopped, naming the key, got %s %q", inst.Status, inst.Stopped)
			}
			if err := l.rt.AnswerByHand("o", "p/"+key, nil, true); err == nil {
				t.Errorf("a key of another instance is refused")
			}
			if err := l.rt.AnswerByHand("o", key, []byte(c.value), c.nothing); err != nil {
				t.Fatal(err)
			}
			if err := l.rt.Resume("o"); err != nil {
				t.Fatal(err)
			}
			inst = l.runFor(3)
			if inst.Status != flowrt.Done {
				t.Fatalf("expected done, got %s %q", inst.Status, inst.Stopped)
			}
			if got := l.app.steps(); got != c.sent {
				t.Errorf("expected %s, got %s", c.sent, got)
			}
			if c.nothing && reclaimed != 1 {
				t.Errorf("sent again reclaiming attempt 1, got reclaim %d", reclaimed)
			}
			if !c.nothing && !strings.Contains(string(inst.Result), "ch-by-hand") {
				t.Errorf("the flow took what the person said: %s", inst.Result)
			}
		})
	}
}
