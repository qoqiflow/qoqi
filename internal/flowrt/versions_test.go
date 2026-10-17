package flowrt_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A subscription billed each month, in three versions: the second bills
// otherwise, the stage as it was; the third keeps a count of its own at
// the stage, which the first has not.
func subscription(bill, before, after string) string {
	return `
import std.flow.{Flow};
import std.time;

struct Sub { months: Int; }

impl Flow<Int> for Sub {
    fn run() -> Int {
        let paid = 0;
` + before + `
        for month in 0..self.months {
            stage month;
            paid += bill(month)` + after + `;
            wait time.after(10.milliseconds());
        }

        return paid;
    }
}

step fn bill(month: Int) -> Int {
` + bill + `
}

fn main() {}
`
}

var (
	v1 = subscription(`println("WORLD v1 bill", month); return 100;`, ``, ``)
	v2 = subscription(`println("WORLD v2 bill", month); return 200;`, ``, ` + 1`)
	v3 = subscription(`println("WORLD v3 bill", month); return 300;`, `let count = 0;`, ` + count`)
)

// started runs an instance in the first version until it sleeps after
// its first month.
func started(t *testing.T, store flowrt.Store, out *bytes.Buffer) *flowrt.Instance {
	t.Helper()
	rt := flowrt.New(compiled(t, v1), store, out)
	if _, err := rt.Start("sub", "Sub", []byte(`{"months": 3}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("sub")
	if inst.Status != flowrt.Waiting || inst.Version != vm.Fingerprint(compiled(t, v1)) {
		t.Fatalf("expected it asleep in the first version, got %s %s", inst.Status, inst.Version)
	}
	return inst
}

// A newer version with the stage as it was: the instance finishes its
// month in its own, and goes on in the newer one from the next stage.
func TestAnInstanceMovesAtAStage(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	started(t, store, &out)

	newer := compiled(t, v2)
	rt := flowrt.New(newer, store, &out)
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("sub")
	if inst.Status != flowrt.Done || string(inst.Result) != "502" || inst.Version != vm.Fingerprint(newer) {
		t.Fatalf("expected done with 100 + 201 + 201 in the newer version, got %s %s %q %s", inst.Status, inst.Result, inst.Stopped, inst.Version)
	}
	want := []string{"WORLD v1 bill 0", "WORLD v2 bill 1", "WORLD v2 bill 2"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}

// A newer version where the stage holds more: the instance stays on its
// own version to the end, its run the old one's; its steps, the same
// there, run in the newer version, which fixed them. One started now
// runs the newer version.
func TestAnInstanceStaysWhenTheStageChanged(t *testing.T) {
	var out bytes.Buffer
	store := flowrt.NewMemory()
	started(t, store, &out)

	newer := compiled(t, v3)
	rt := flowrt.New(newer, store, &out)
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _ := store.Get("sub")
	if inst.Status != flowrt.Done || string(inst.Result) != "700" || inst.Version != vm.Fingerprint(compiled(t, v1)) {
		t.Fatalf("expected done in its own version, its bills the newer one's, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	want := []string{"WORLD v1 bill 0", "WORLD v3 bill 1", "WORLD v3 bill 2"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}

	fresh := runFlow(t, rt, store, "fresh", "Sub", `{"months": 2}`)
	if fresh.Version != vm.Fingerprint(newer) || string(fresh.Result) != "600" {
		t.Errorf("a new instance runs the newer version: got %s %s", fresh.Version, fresh.Result)
	}
}

// Stopped at any write of the newer runtime, the instance that moves
// ends the same, on a file of SQLite as in memory.
func TestMovingSurvivesAStopAtEveryWrite(t *testing.T) {
	newer := compiled(t, v2)
	count := func() int {
		store := flowrt.NewMemory()
		started(t, store, &bytes.Buffer{})
		before := store.Writes()
		if err := flowrt.New(newer, store, &bytes.Buffer{}).RunUntilIdle(5 * time.Second); err != nil {
			t.Fatal(err)
		}
		return store.Writes() - before
	}()

	if count < 8 {
		t.Fatalf("expected a write for each step, sleep and the move, got %d", count)
	}
	for k := 1; k <= count; k++ {
		store := flowrt.NewMemory()
		var out bytes.Buffer
		started(t, store, &out)
		store.FailAt = store.Writes() + k
		err := flowrt.New(newer, store, &out).RunUntilIdle(5 * time.Second)
		if err != nil && !errors.Is(err, flowrt.ErrCrash) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		store.FailAt = 0
		if err := flowrt.New(newer, store, &out).RunUntilIdle(5 * time.Second); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		inst, _ := store.Get("sub")
		if inst.Status != flowrt.Done || string(inst.Result) != "502" {
			t.Errorf("after stopping at write %d of %d: expected done with 502, got %s %s %q\n%s", k, count, inst.Status, inst.Result, inst.Stopped, out.String())
		}
	}
}

// What a new version changes of the stages an old one had, as flow.lock
// and the daemon say it.
func TestTheChangesOfAStage(t *testing.T) {
	old := flowrt.ShapesOf(compiled(t, v1), []string{"Sub"})
	if got := fmt.Sprint(old["Sub"]["month"]); got != "map[loop.end#1:Int month:Int paid:Int self:Sub self.months:Int]" {
		t.Fatalf("the shape of stage month: %s", got)
	}
	if changes := flowrt.Changes(old, flowrt.ShapesOf(compiled(t, v2), []string{"Sub"})); len(changes) != 0 {
		t.Errorf("the same stage: expected no change, got %v", changes)
	}
	changes := flowrt.Changes(old, flowrt.ShapesOf(compiled(t, v3), []string{"Sub"}))
	if len(changes) != 1 || changes[0].String() != "stage month of Sub: count: Int is new" {
		t.Errorf("a variable added: got %v", changes)
	}

	gone := flowrt.Shapes{"Sub": {}}
	changes = flowrt.Changes(old, gone)
	if len(changes) != 1 || changes[0].String() != "stage month of Sub: it is gone" {
		t.Errorf("a stage gone: got %v", changes)
	}
	retyped := flowrt.Shapes{"Sub": {"month": {"loop.end#1": "Int", "month": "Int", "paid": "Float", "self": "Sub"}}}
	changes = flowrt.Changes(old, retyped)
	if len(changes) != 1 || changes[0].String() != "stage month of Sub: paid: Int, now Float; self.months is gone" {
		t.Errorf("a type changed and a field gone: got %v", changes)
	}

	path := t.TempDir() + "/sub.flow.lock"
	if lock, err := flowrt.ReadLock(path); lock != nil || err != nil {
		t.Errorf("no lock: expected nothing, got %v %v", lock, err)
	}
	if err := flowrt.WriteLock(path, &flowrt.Lock{Version: "v1", Flows: old}); err != nil {
		t.Fatal(err)
	}
	lock, err := flowrt.ReadLock(path)
	if err != nil || lock.Version != "v1" || len(flowrt.Changes(lock.Flows, old)) != 0 {
		t.Errorf("read back: %+v %v", lock, err)
	}
}

// v4 keeps a count at the stage, which v1 has not, and says how to make
// it of what v1 had; v5 names the stage otherwise, and says that an
// instance at the old one goes on from the new.
var (
	v4 = subscription(`println("WORLD v4 bill", month); return 300;`, `let count = 0;`, ` + count`) + `
struct Month { paid: Int; count: Int; month: Int; }

impl Sub {
    migrate fn month(paid: Int, month: Int) -> Month {
        return Month { paid: paid, count: 50, month: month };
    }
}
`
	v5 = strings.Replace(subscription(`println("WORLD v5 bill", month); return 400;`, ``, ``), "stage month;", "stage billing;", 1) + `
struct Billing { paid: Int; month: Int; }

impl Sub {
    migrate fn month(paid: Int, month: Int) -> Billing {
        return Billing { paid: paid + 1000, month: month };
    }
}
`
)

func TestAnInstanceMigratesAtAStage(t *testing.T) {
	for _, c := range []struct {
		name, src, result string
		world             []string
	}{
		{"a variable made by the migration", v4, "800", []string{"WORLD v1 bill 0", "WORLD v4 bill 1", "WORLD v4 bill 2"}},
		{"a stage named otherwise", v5, "1900", []string{"WORLD v1 bill 0", "WORLD v5 bill 1", "WORLD v5 bill 2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			started(t, store, &out)
			newer := compiled(t, c.src)
			if err := flowrt.New(newer, store, &out).RunUntilIdle(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			inst, _ := store.Get("sub")
			if inst.Status != flowrt.Done || string(inst.Result) != c.result || inst.Version != vm.Fingerprint(newer) {
				t.Fatalf("expected done with %s in the newer version, got %s %s %q", c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(c.world, "|") {
				t.Errorf("want %v\ngot  %v", c.world, got)
			}
		})
	}
}

// What no migration answers, as the daemon refuses it.
func TestUnansweredChanges(t *testing.T) {
	old := flowrt.ShapesOf(compiled(t, v1), []string{"Sub"})
	changes := flowrt.Changes(old, flowrt.ShapesOf(compiled(t, v3), []string{"Sub"}))
	left := flowrt.Unanswered(changes, old, flowrt.ShapesOf(compiled(t, v3), []string{"Sub"}), nil)
	if len(left) != 1 || left[0].String() != "stage month of Sub: count: Int is new, and no migrate fn month says how an instance there goes on" {
		t.Errorf("no migration: got %v", left)
	}
	answered := []flowrt.Migration{{Flow: "Sub", Stage: "month", Params: map[string]string{"paid": "Int", "month": "Int"}}}
	if left := flowrt.Unanswered(changes, old, flowrt.ShapesOf(compiled(t, v3), []string{"Sub"}), answered); len(left) != 0 {
		t.Errorf("a migration that fits: got %v", left)
	}
	wrong := []flowrt.Migration{{Flow: "Sub", Stage: "month", Params: map[string]string{"paid": "String", "spent": "Int"}}}
	left = flowrt.Unanswered(changes, old, flowrt.ShapesOf(compiled(t, v3), []string{"Sub"}), wrong)
	if len(left) != 2 || left[0].What != "migrate fn month takes paid as a String; it was a Int there" || left[1].What != "migrate fn month takes spent, which the stage had not" {
		t.Errorf("a migration that does not fit: got %v", left)
	}
}

// billAfterWait bills after the wait, before the next stage: while an
// instance taken up there still runs its own version.
func billAfterWait(params, call, body string) string {
	return `
import std.flow.{Flow};
import std.time;

struct Late { months: Int; }

impl Flow<Int> for Late {
    fn run() -> Int {
        let paid = 0;
        for month in 0..self.months {
            stage month;
            wait time.after(10.milliseconds());
            paid += ` + call + `;
        }
        return paid;
    }
}

step fn bill(` + params + `) -> Int {
` + body + `
}

fn main() {}
`
}

// A step fixed in a newer version runs fixed for an instance of the
// older one at its next call, before any stage; one whose signature
// changed runs as the instance's version has it.
func TestStepsRunInTheNewestVersion(t *testing.T) {
	old := billAfterWait("month: Int", "bill(month)", `println("WORLD old bill", month); return 1;`)
	for _, c := range []struct {
		name, newer string
		world       []string
	}{
		{"the same step, fixed", billAfterWait("month: Int", "bill(month)", `println("WORLD fixed bill", month); return 10;`),
			[]string{"WORLD fixed bill 0", "WORLD fixed bill 1"}},
		{"a step that takes more", billAfterWait("month: Int, rate: Int", "bill(month, 2)", `println("WORLD rated bill", month, rate); return 10 * rate;`),
			[]string{"WORLD old bill 0", "WORLD rated bill 1 2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			first := flowrt.New(compiled(t, old), store, &out)
			if _, err := first.Start("late", "Late", []byte(`{"months": 2}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := first.RunDue(); err != nil {
				t.Fatal(err)
			}
			if err := flowrt.New(compiled(t, c.newer), store, &out).RunUntilIdle(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			if inst, _ := store.Get("late"); inst.Status != flowrt.Done {
				t.Fatalf("expected done, got %s %q", inst.Status, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(c.world, "|") {
				t.Errorf("want %v\ngot  %v", c.world, got)
			}
		})
	}
}

// planned is the subscription with a field it had not: a plan, which an
// instance of the first version does not have.
func planned(before, after, migrations string) string {
	src := strings.Replace(subscription(`println("WORLD planned bill", month); return 300;`, before, after+` + self.plan.len()`), "struct Sub { months: Int; }", "struct Sub { months: Int; plan: String; }", 1)
	return src + migrations
}

const fieldsMigration = `
impl Sub {
    migrate fn fields(months: Int) -> Sub {
        return Sub { months: months, plan: "basic" };
    }
}
`

func TestFieldsMigrate(t *testing.T) {
	for _, c := range []struct {
		name, src, result string
	}{
		// 100, then 300 + "basic".len() twice
		{"a field added", planned(``, ``, fieldsMigration), "710"},
		// and the stage keeps a count, made by its own migration
		{"a field added and a stage changed", planned(`let count = 0;`, ` + count`, fieldsMigration+`
struct Month { paid: Int; count: Int; month: Int; }

impl Sub {
    migrate fn month(paid: Int, month: Int) -> Month {
        return Month { paid: paid, count: 50, month: month };
    }
}
`), "810"},
		// with no migrate fn fields, it stays on its version, run as that
		// one had it; its bills, the same step, the newer's
		{"no migration of the fields", planned(``, ``, ``), "700"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			started(t, store, &out)
			newer := compiled(t, c.src)
			if err := flowrt.New(newer, store, &out).RunUntilIdle(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			inst, _ := store.Get("sub")
			if inst.Status != flowrt.Done || string(inst.Result) != c.result {
				t.Fatalf("expected done with %s, got %s %s %q\n%s", c.result, inst.Status, inst.Result, inst.Stopped, out.String())
			}
			moved := inst.Version == vm.Fingerprint(newer)
			if moved != (c.result != "700") {
				t.Errorf("moved to the newer version: %v", moved)
			}
			if moved && !strings.Contains(string(inst.Fields), `"plan":"basic"`) {
				t.Errorf("expected the fields migrated, got %s", inst.Fields)
			}
		})
	}

	// The daemon's check: a field added, answered or not
	old := flowrt.ShapesOf(compiled(t, v1), []string{"Sub"})
	newer := flowrt.ShapesOf(compiled(t, planned(``, ``, fieldsMigration)), []string{"Sub"})
	changes := flowrt.Changes(old, newer)
	if left := flowrt.Unanswered(changes, old, newer, nil); len(left) != 1 || !strings.Contains(left[0].What, "no migrate fn fields says what an instance's fields become") {
		t.Errorf("a field added, no migration: got %v", left)
	}
	fits := []flowrt.Migration{{Flow: "Sub", Stage: "fields", Params: map[string]string{"months": "Int"}}}
	if left := flowrt.Unanswered(changes, old, newer, fits); len(left) != 0 {
		t.Errorf("a migration of the fields that fits: got %v", left)
	}
	wrong := []flowrt.Migration{{Flow: "Sub", Stage: "fields", Params: map[string]string{"months": "String", "plan": "String"}}}
	if left := flowrt.Unanswered(changes, old, newer, wrong); len(left) != 2 {
		t.Errorf("a migration of the fields that does not fit: got %v", left)
	}
}
