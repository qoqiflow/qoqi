package flows_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// queued is what the compiler says of a program whose project declares
// the queues given: "CODE line: message", every error.
func queued(t *testing.T, src string, queues ...string) []string {
	t.Helper()
	errs := errors.NewHandler()
	compiler.CompileOptions(src, "flow.qi", compiler.Options{Queues: queues}, errs)
	var out []string
	for _, d := range errs.Diagnostics {
		if d.Severity == errors.SeverityError {
			out = append(out, fmt.Sprintf("%s %d: %s", d.Code, d.Pos.Line, d.Message))
		}
	}
	sort.Strings(out)
	return out
}

const shopSteps = `import std.flow.{Flow, Settled};
import std.time;

struct Cart { id: String; }
struct Hold { id: String; }

struct Order { cart: Cart; }

impl Flow<Result<String, String>> for Order {
    fn run() -> Result<String, String> {
        let hold = reserve(self.cart)?;
        match wire(3) {
            case Done(_): { return Ok(hold.id); }
            case Unknown: { return Err("unknown"); }
        }
    }
}

step fn reserve(cart: Cart) -> Result<Hold, String> by warehouse undo release;
step fn release(hold: Hold) -> Result<Void, String> by warehouse;
step fn wire(amount: Int) -> Settled<Result<String, String>> within 2.hours() by bank;

fn main() {}
`

// A project that declares its queues: the steps by them compile.
func TestStepsByQueuesTheProjectDeclares(t *testing.T) {
	if got := queued(t, shopSteps, "warehouse", "bank"); len(got) > 0 {
		t.Errorf("expected none, got:\n  %s", strings.Join(got, "\n  "))
	}
}

// A queue the project does not declare is an error at the step, as a
// misspelt name is; outside a project, every queue is.
func TestAQueueTheProjectDoesNotDeclare(t *testing.T) {
	got := queued(t, shopSteps, "warehouse", "billing")
	want := []string{"F0012 21: wire is carried out by the queue bank, which the project does not declare"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
	if got := queued(t, shopSteps); len(got) != 3 {
		t.Errorf("outside a project, each step by a queue: got\n  %s", strings.Join(got, "\n  "))
	}
}

// A Settled step by a queue says how long it may take.
func TestASettledStepByAQueueSaysItsTime(t *testing.T) {
	src := strings.Replace(shopSteps, " within 2.hours()", "", 1)
	got := queued(t, src, "warehouse", "bank")
	want := []string{"F0009 21: wire returns a Settled, and does not say how long it may take"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

// What a step by a queue may not be: given a body, generic, undone by
// what is not there, or taking a time that is not a number.
func TestWhatAStepByAQueueIsNot(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"a body", `step fn charge(amount: Int) -> Int by billing { return 1; }`, "a step by billing has no body"},
		{"generic", `step fn charge<T>(amount: T) -> Int by billing;`, "cannot have type parameters"},
		{"an undo not there", `step fn charge(amount: Int) -> Int by billing undo refund;`, "refund"},
		{"a time not a Duration", `step fn charge(amount: Int) -> Int within "soon" by billing;`, "std.time.Duration"},
		{"a number for a time", `step fn charge(amount: Int) -> Int within 5000 by billing;`, "cannot use type Int as argument of type std.time.Duration"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(queued(t, "import std.flow;\n"+c.src+"\nfn main() {}\n", "billing"), "\n")
			if !strings.Contains(got, c.want) {
				t.Errorf("expected %q, got:\n%s", c.want, got)
			}
		})
	}
}

// A Settled step in Qoqi says its time the same way, after its
// signature; a plain one may, and says a minute otherwise.
func TestASettledStepInQoqiSaysItsTime(t *testing.T) {
	src := `import std.flow.{Flow, Settled};
import std.time;

struct Pay { amount: Int; }

impl Flow<String> for Pay {
    fn run() -> String {
        let receipt = note(self.amount);
        match capture(self.amount) {
            case Done(id): { return id + receipt; }
            case Unknown: { return "unknown"; }
        }
    }
}

step fn note(amount: Int) -> String within 30.seconds() {
    return "${amount}";
}

step fn capture(amount: Int) -> Settled<String> within 5.minutes() {
    return Settled.Done("pay-${amount}");
}

fn main() {}
`
	if got := queued(t, src); len(got) > 0 {
		t.Errorf("expected none, got:\n  %s", strings.Join(got, "\n  "))
	}
	got := queued(t, strings.Replace(src, " within 5.minutes()", "", 1))
	want := []string{"F0009 20: capture returns a Settled, and does not say how long it may take"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
	got = queued(t, strings.Replace(src, "within 30.seconds()", `within "soon"`, 1))
	if len(got) != 1 || !strings.Contains(got[0], "std.time.Duration") {
		t.Errorf("a time that is not a Duration: got\n  %s", strings.Join(got, "\n  "))
	}
}
