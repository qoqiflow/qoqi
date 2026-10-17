package flowrt_test

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// The steps by a queue of a program go into its lock with their shapes.
// A new version that changes one keeps the old shape in the lock, and
// says so, while instances of the old version are alive; none alive, it
// is dropped.
func TestTheShapesStillSent(t *testing.T) {
	steps := flowrt.StepsOf(compiledWithQueues(t, shop, "warehouse", "billing", "bank"))
	if len(steps) != 5 || steps["charge"].Shape != shapeOf(t, "charge") || !strings.HasPrefix(steps["charge"].Signature, "fn(") {
		t.Fatalf("the five steps by queues, with their shapes: %v", steps)
	}

	changed := strings.Replace(shop, "step fn notify(text: String) by billing;", "step fn notify(text: String, loud: Bool) by billing;", 1)
	changed = strings.Replace(changed, `notify("paid ${paid.id}");`, `notify("paid ${paid.id}", false);`, 1)
	newer := flowrt.StepsOf(compiledWithQueues(t, changed, "warehouse", "billing", "bank"))
	if newer["notify"].Shape == steps["notify"].Shape || newer["charge"].Shape != steps["charge"].Shape {
		t.Fatalf("notify's shape changes with its signature, charge's does not")
	}

	last := &flowrt.Lock{Version: "sha256:aaaaaaaaaaaaaaaa", Steps: steps}
	alive := map[string]int{"sha256:aaaaaaaaaaaaaaaa": 2}
	count := func(v string) int { return alive[v] }
	before, said := flowrt.StillSent(last, "sha256:bbbbbbbbbbbbbbbb", newer, count)
	if len(before) != 1 || len(before[0].Steps) != 1 || before[0].Steps["notify"].Shape != steps["notify"].Shape {
		t.Fatalf("the old notify kept, alone: %+v", before)
	}
	if len(said) != 1 || !strings.Contains(said[0], "2 instances of version aaaaaaaaaaaa may still send notify as "+steps["notify"].Shape) {
		t.Errorf("said: %v", said)
	}

	// Served again: still alive, still kept; then none alive, dropped
	again := &flowrt.Lock{Version: "sha256:bbbbbbbbbbbbbbbb", Steps: newer, Before: before}
	if kept, _ := flowrt.StillSent(again, "sha256:bbbbbbbbbbbbbbbb", newer, count); len(kept) != 1 {
		t.Errorf("served again, the old shape stays while its instances live: %+v", kept)
	}
	alive["sha256:aaaaaaaaaaaaaaaa"] = 0
	if kept, said := flowrt.StillSent(again, "sha256:bbbbbbbbbbbbbbbb", newer, count); len(kept) != 0 || len(said) != 0 {
		t.Errorf("none alive, it goes: %+v %v", kept, said)
	}
}
