package flowrt_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A shop on a clock the test moves, whose application answers the steps
// named later: 202 on the request, the answer on one of its own.
type later struct {
	t      *testing.T
	app    *application
	rt     *flowrt.Runtime
	store  flowrt.Store
	queues map[string]flowrt.Queue
	clock  atomic.Int64
}

func servedLater(t *testing.T, store flowrt.Store, answer func(d delivery, n int) (int, string)) *later {
	t.Helper()
	l := &later{t: t, app: &application{t: t, answer: answer}, store: store}
	l.clock.Store(1_800_000_000_000)
	l.app.clock = func() time.Time { return time.UnixMilli(l.clock.Load()) }
	queues := map[string]flowrt.Queue{}
	for _, name := range []string{"warehouse", "billing", "bank"} {
		server := httptest.NewServer(l.app.handler(name))
		t.Cleanup(server.Close)
		queues[name] = flowrt.Queue{URL: server.URL, Secret: shopSecret}
	}
	l.queues = queues
	l.rt = l.runtime(queues)
	return l
}

// runtime is a runtime of the shop on the store: another process, after
// the first stopped, is another runtime.
func (l *later) runtime(queues map[string]flowrt.Queue) *flowrt.Runtime {
	rt := flowrt.New(compiledWithQueues(l.t, shop, "warehouse", "billing", "bank"), l.store, &bytes.Buffer{})
	rt.Now = l.clock.Load
	q := flowrt.NewHTTPQueues(queues)
	q.Now = l.clock.Load
	rt.Queues = q
	return rt
}

// run takes up what is due, until nothing is.
func (l *later) run() *flowrt.Instance {
	l.t.Helper()
	for i := 0; i < 20; i++ {
		n, err := l.rt.RunDue()
		if err != nil {
			l.t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	inst, _, _ := l.store.Get("o")
	return inst
}

// answer answers a step's attempt later, as the application does.
func (l *later) answer(d delivery, body string) (bool, error) {
	key, _ := json.Marshal(d.Key)
	token, _ := json.Marshal(d.Token)
	return l.rt.Answer([]byte(fmt.Sprintf(`{"key": %s, "attempt": %d, "token": %s, %s}`, key, d.Attempt, token, strings.TrimPrefix(strings.TrimSuffix(body, "}"), "{"))))
}

// sent is the last delivery of a step.
func (l *later) sent(step string) delivery {
	l.t.Helper()
	got := l.app.deliveries()
	for i := len(got) - 1; i >= 0; i-- {
		if got[i].Step == step {
			return got[i]
		}
	}
	l.t.Fatalf("%s was not sent: %s", step, l.app.steps())
	return delivery{}
}

func chargeLater(d delivery, n int) (int, string) {
	if d.Step == "charge" {
		return 202, ``
	}
	return working(d, n)
}

const chargedLater = `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Charge", "id": "ch-late"}}}`

// stores are a store in memory and one in every place.
func stores(t *testing.T) map[string]flowrt.Store {
	out := map[string]flowrt.Store{"memory": flowrt.NewMemory()}
	for name, store := range openFresh(t) {
		out[name] = store
	}
	return out
}

// A step answered later: the instance waits for it out of memory, and
// goes on once it comes, even in a process that was not the one that
// sent it. The first answer stands; another token is refused.
func TestAStepAnsweredLater(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			l := servedLater(t, store, chargeLater)
			if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
				t.Fatal(err)
			}
			inst := l.run()
			if inst.Status != flowrt.Waiting || len(inst.Awaits) != 1 || !strings.HasPrefix(inst.Awaits[0], "answer:") {
				t.Fatalf("expected it waiting for charge's answer, got %s %v %q", inst.Status, inst.Awaits, inst.Stopped)
			}
			if inst.WakeAt != l.sent("charge").Until {
				t.Errorf("due at the step's limit, %d, got %d", l.sent("charge").Until, inst.WakeAt)
			}

			charge := l.sent("charge")
			wrong := charge
			wrong.Token = strings.Repeat("0", 32)
			if _, err := l.answer(wrong, chargedLater); !errors.Is(err, flowrt.ErrToken) {
				t.Errorf("another token: expected ErrToken, got %v", err)
			}
			if _, err := l.answer(charge, `{"working": true}`); err != nil {
				t.Errorf("under way: %v", err)
			}

			// Answered while another process serves it
			l.rt = l.runtime(l.queues)
			if already, err := l.answer(charge, chargedLater); already || err != nil {
				t.Fatalf("the answer: %v %v", already, err)
			}
			if already, err := l.answer(charge, `{"value": {"$type": "std.result.Result.Err", "$variant": true, "0": "no"}}`); !already || err != nil {
				t.Errorf("a second answer: expected already, got %v %v", already, err)
			}
			inst = l.run()
			if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "paid ch-late") {
				t.Fatalf("expected done with the later answer, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
			}
			if got := l.app.steps(); got != "reserve#1 charge#1 notify#1" {
				t.Errorf("charge sent once, got %s", got)
			}
		})
	}
}

// What follows each answer that comes later, or none before the step's
// time is up.
func TestWhatALaterAnswerSays(t *testing.T) {
	for _, c := range []struct {
		name   string
		wired  bool
		step   string
		answer string // "" for none: the time passes
		status flowrt.Status
		result string
		steps  string
	}{
		{"a retry is a next attempt", false, "charge", `{"retry": {"reason": "busy", "after": 10}}`,
			flowrt.Done, "paid ch-9", "reserve#1 charge#1 charge#2 notify#1"},
		{"failed stops the instance", false, "charge", `{"failed": "no customer"}`,
			flowrt.Stopped, "charge failed in the application: no customer", "reserve#1 charge#1"},
		{"an Err undoes what was held", false, "charge", `{"value": {"$type": "std.result.Result.Err", "$variant": true, "0": "declined"}}`,
			flowrt.Done, "declined", "reserve#1 charge#1 release#1"},
		{"no answer in time: a plain step is sent again", false, "charge", "",
			flowrt.Done, "paid ch-9", "reserve#1 charge#1 charge#2 notify#1"},
		{"no answer in time: a settled step is Unknown", true, "wire", "",
			flowrt.Done, "wire unknown", "reserve#1 charge#1 notify#1 wire#1"},
		{"a settled step answered later", true, "wire", `{"value": {"$type": "std.flow.Settled.Done", "$variant": true, "0": {"$type": "std.result.Result.Ok", "$variant": true, "0": "w-9"}}}`,
			flowrt.Done, "wired w-9", "reserve#1 charge#1 notify#1 wire#1"},
		{"the undo comes with the value", false, "reserve", `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Hold", "id": "h-2"}}, "undo": {"$type": "Hold", "id": "h-2"}}`,
			flowrt.Done, "held h-2", "reserve#1 charge#1 notify#1"},
		{"a value without its undo is a panic", false, "reserve", `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Hold", "id": "h-2"}}}`,
			flowrt.Stopped, "the application gave reserve no undo", "reserve#1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			step := c.step
			l := servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
				if d.Step == step && n == 1 {
					return 202, ``
				}
				return working(d, n)
			})
			if _, err := l.rt.Start("o", "Order", []byte(shopOrder(c.wired))); err != nil {
				t.Fatal(err)
			}
			if inst := l.run(); inst.Status != flowrt.Waiting {
				t.Fatalf("expected it waiting, got %s %q", inst.Status, inst.Stopped)
			}
			sent := l.sent(c.step)
			if c.answer == "" {
				l.clock.Add(sent.Until - l.clock.Load() + 1)
			} else if _, err := l.answer(sent, c.answer); err != nil {
				t.Fatal(err)
			}
			inst := l.run()
			for i := 0; i < 5 && inst.Status == flowrt.Waiting; i++ {
				// A retry waits its while
				l.clock.Add(1000)
				inst = l.run()
			}
			said := string(inst.Result) + inst.Stopped
			if inst.Status != c.status || !strings.Contains(said, c.result) {
				t.Fatalf("expected %s with %s, got %s %s %q", c.status, c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := l.app.steps(); got != c.steps {
				t.Errorf("expected %s, got %s", c.steps, got)
			}
			if c.answer == "" {
				// What comes after is late: kept aside, not given
				if _, err := l.answer(sent, chargedLater); !errors.Is(err, flowrt.ErrLate) {
					t.Errorf("an answer past the limit: expected ErrLate, got %v", err)
				}
				a := l.store.Attempts(sent.Key)[sent.Attempt-1]
				if a.State != flowrt.GivenUp || len(a.Late) == 0 {
					t.Errorf("the attempt given up, its late answer kept, got %+v", a)
				}
			}
		})
	}
}

// An answer that comes on a request of its own before the 202 is back:
// the flow does not wait for it.
func TestAnAnswerBeforeItsAccepted(t *testing.T) {
	var l *later
	l = servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
		if d.Step == "charge" {
			if _, err := l.answer(d, chargedLater); err != nil {
				t.Errorf("answered at once: %v", err)
			}
			return 202, ``
		}
		return working(d, n)
	})
	if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
		t.Fatal(err)
	}
	if inst := l.run(); inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "paid ch-late") {
		t.Fatalf("expected done at once, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
}

// Stopped at every write, a shop whose charge is answered later ends the
// same: whatever attempt of charge waits is answered, as the application
// would, and the flow goes on from it in the process that comes after.
func TestALaterAnswerSurvivesAStopAtEveryWrite(t *testing.T) {
	scenario := func(l *later) *flowrt.Instance {
		if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			if _, err := l.rt.RunDue(); err != nil && !errors.Is(err, flowrt.ErrCrash) {
				t.Fatal(err)
			}
			inst, _, _ := l.store.Get("o")
			if inst == nil {
				// Stopped as it was made: started again
				if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if inst.Status == flowrt.Done {
				return inst
			}
			// What waits for an answer gets it, once
			for _, name := range inst.Awaits {
				key := strings.TrimPrefix(name, "answer:")
				a, ok, _ := l.store.Attempt(key)
				if !ok || a.State == flowrt.Answered {
					continue
				}
				for _, d := range l.app.deliveries() {
					if d.Key == key && d.Token == a.Token {
						if _, err := l.answer(d, chargedLater); err != nil && !errors.Is(err, flowrt.ErrCrash) {
							t.Fatal(err)
						}
					}
				}
			}
			l.rt = l.runtime(l.queues)
		}
		inst, _, _ := l.store.Get("o")
		return inst
	}
	plain := servedLater(t, flowrt.NewMemory(), chargeLater)
	clean := scenario(plain)
	if clean.Status != flowrt.Done || !strings.Contains(string(clean.Result), "paid ch-late") {
		t.Fatalf("expected done, got %s %s %q", clean.Status, clean.Result, clean.Stopped)
	}
	writes := plain.store.(*flowrt.Memory).Writes()
	for k := 1; k <= writes; k++ {
		store := flowrt.NewMemory()
		store.FailAt = k
		l := servedLater(t, store, chargeLater)
		inst := scenario(l)
		if inst == nil || inst.Status != flowrt.Done || string(inst.Result) != string(clean.Result) {
			t.Errorf("stopped at write %d: expected %s, got %+v", k, clean.Result, inst)
			continue
		}
		keys := map[string]bool{}
		for _, d := range l.app.deliveries() {
			if d.Step == "charge" {
				keys[d.Key] = true
			}
		}
		if len(keys) != 1 {
			t.Errorf("stopped at write %d: charge under %d keys", k, len(keys))
		}
	}
}
