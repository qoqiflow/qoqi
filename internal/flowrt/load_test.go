package flowrt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// count is how many times a step was delivered.
func (a *application) count(step string) int {
	n := 0
	for _, d := range a.deliveries() {
		if d.Step == step {
			n++
		}
	}
	return n
}

// runFor takes up what is due, the clock moving a minute at a time, as
// many times as said.
func (l *later) runFor(rounds int) *flowrt.Instance {
	l.t.Helper()
	var inst *flowrt.Instance
	for i := 0; i < rounds; i++ {
		inst = l.run()
		if inst.Status != flowrt.Waiting {
			return inst
		}
		l.clock.Add(61_000)
	}
	return inst
}

// A queue of one: the second order's charge waits while the first is
// under way, out of memory and without spending an attempt, and leaves
// once the first is answered.
func TestAQueueHasRoomForSoMany(t *testing.T) {
	l := servedLater(t, flowrt.NewMemory(), chargeLater)
	for name, q := range l.queues {
		q.Concurrency = 1
		l.queues[name] = q
	}
	l.rt = l.runtime(l.queues)
	for _, id := range []string{"o", "p"} {
		if _, err := l.rt.Start(id, "Order", []byte(shopOrder(false))); err != nil {
			t.Fatal(err)
		}
	}
	l.run()
	if got := l.app.count("charge"); got != 1 {
		t.Fatalf("one charge under way, as many as billing takes, got %d: %s", got, l.app.steps())
	}
	held, _, _ := l.store.Get("p")
	if held.Status != flowrt.Waiting {
		t.Fatalf("the second waits, got %s %q", held.Status, held.Stopped)
	}

	first := l.sent("charge")
	if _, err := l.answer(first, chargedLater); err != nil {
		t.Fatal(err)
	}
	l.clock.Add(1000)
	l.run()
	if got := l.app.count("charge"); got != 2 {
		t.Fatalf("the second charge leaves once the first is answered, got %d: %s", got, l.app.steps())
	}
	second := l.sent("charge")
	if second.Key == first.Key || second.Attempt != 1 {
		t.Errorf("the second order's charge, its first attempt, got %s#%d", second.Key, second.Attempt)
	}
	if _, err := l.answer(second, chargedLater); err != nil {
		t.Fatal(err)
	}
	l.run()
	for _, id := range []string{"o", "p"} {
		if inst, _, _ := l.store.Get(id); inst.Status != flowrt.Done {
			t.Errorf("%s: expected done, got %s %q", id, inst.Status, inst.Stopped)
		}
	}
}

// A queue that keeps failing is paused after five failures in a row: it
// is sent nothing but a GET now and then, and its steps spend no
// attempts, until the GET is answered; then they leave again.
func TestAQueueThatFailsIsPaused(t *testing.T) {
	var l *later
	l = servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
		l.app.mu.Lock()
		down := l.app.down
		l.app.mu.Unlock()
		if d.Step == "charge" && down {
			return 503, `deploying`
		}
		return working(d, n)
	})
	l.app.down = true
	if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
		t.Fatal(err)
	}
	inst := l.runFor(30)
	if inst.Status != flowrt.Waiting {
		t.Fatalf("expected it waiting for billing, got %s %q", inst.Status, inst.Stopped)
	}
	if got := l.app.count("charge"); got != 5 {
		t.Errorf("five failures, then paused: got %d charges", got)
	}
	if l.app.probes == 0 {
		t.Errorf("a paused queue is asked whether it is back")
	}
	if paused, why := l.rt.Queues.(*flowrt.HTTPQueues).Paused("billing"); !paused || !strings.Contains(why, "503") {
		t.Errorf("billing paused, saying why: %v %q", paused, why)
	}

	l.app.mu.Lock()
	l.app.down = false
	l.app.mu.Unlock()
	inst = l.runFor(5)
	if inst.Status != flowrt.Done {
		t.Fatalf("back, it goes on: got %s %q", inst.Status, inst.Stopped)
	}
	if got := l.sent("charge").Attempt; got != 6 {
		t.Errorf("five attempts that may have arrived, then the sixth: got attempt %d", got)
	}
}

// A queue whose secret is wrong on a side is paused at once.
func TestAWrongSecretPausesAtOnce(t *testing.T) {
	l := servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
		if d.Step == "charge" && n == 1 {
			return 401, `bad signature`
		}
		return working(d, n)
	})
	if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
		t.Fatal(err)
	}
	l.run()
	if paused, why := l.rt.Queues.(*flowrt.HTTPQueues).Paused("billing"); !paused || !strings.Contains(why, "secret") {
		t.Errorf("billing paused at once, saying why: %v %q", paused, why)
	}
	inst := l.runFor(5)
	if inst.Status != flowrt.Done || l.app.probes == 0 {
		t.Fatalf("asked whether it is back, then on: got %s %q, %d probes", inst.Status, inst.Stopped, l.app.probes)
	}
}

// A step that keeps failing alone, its queue answering the others, stops
// its instance, naming what the queue said last.
func TestAPoison(t *testing.T) {
	l := servedLater(t, flowrt.NewMemory(), func(d delivery, n int) (int, string) {
		if d.Step == "charge" {
			return 500, `Fatal error: Allowed memory size exhausted`
		}
		return working(d, n)
	})
	l.rt.Poison = 3
	if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
		t.Fatal(err)
	}
	inst := l.runFor(20)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "charge may have arrived 3 times in a row") || !strings.Contains(inst.Stopped, "Allowed memory size") {
		t.Fatalf("expected it stopped as a poison, got %s %q", inst.Status, inst.Stopped)
	}
	if got := l.app.count("charge"); got != 3 {
		t.Errorf("three charges, then the stop: got %d", got)
	}
}

// shapeOf is the shape of a step of the shop, as the daemon sends it.
func shapeOf(t *testing.T, step string) string {
	chunk := compiledWithQueues(t, shop, "warehouse", "billing", "bank")
	for _, p := range chunk.Prototypes {
		if p.Name == step {
			return vm.StepShape(step, p.Signature)
		}
	}
	t.Fatalf("no step %s", step)
	return ""
}

// A queue whose manifest does not list a step's shape is not sent it:
// the step waits, spending no attempt, until the application deployed
// lists it.
func TestAStepOfAShapeTheQueueDoesNotKnow(t *testing.T) {
	l := servedLater(t, flowrt.NewMemory(), working)
	l.app.manifest = `{"shapes": ["` + shapeOf(t, "reserve") + `", "` + shapeOf(t, "release") + `"], "once": [], "started": []}`
	q := l.rt.Queues.(*flowrt.HTTPQueues)
	q.ManifestAge = 30 * time.Second
	if _, err := l.rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
		t.Fatal(err)
	}
	inst := l.runFor(1)
	if inst.Status != flowrt.Waiting || l.app.count("charge") != 0 || l.app.count("reserve") != 1 {
		t.Fatalf("reserve sent, charge held: got %s %q, %s", inst.Status, inst.Stopped, l.app.steps())
	}
	if known, why := q.Knows("billing", shapeOf(t, "charge"), "charge"); known || !strings.Contains(why, "does not know the shape") {
		t.Errorf("billing says why: %v %q", known, why)
	}

	l.app.mu.Lock()
	l.app.manifest = `{"shapes": ["` + shapeOf(t, "charge") + `", "` + shapeOf(t, "notify") + `"]}`
	l.app.mu.Unlock()
	inst = l.runFor(5)
	if inst.Status != flowrt.Done {
		t.Fatalf("deployed, it goes on: got %s %q", inst.Status, inst.Stopped)
	}
	if got := l.sent("charge").Attempt; got != 1 {
		t.Errorf("charge's first attempt, the waits spending none: got %d", got)
	}
}
