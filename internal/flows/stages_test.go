package flows_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// staged is what the compiler says of the stages of a program, warnings
// included: "CODE line: message".
func staged(t *testing.T, src string) []string {
	t.Helper()
	errs := errors.NewHandler()
	compiler.Compile(imports+src+"\nfn main() {}\n", "flow.qi", errs)
	var out []string
	for _, d := range errs.Diagnostics {
		switch d.Code {
		case "F0002", "F0005", "F0006", "F0007", "W0011":
			out = append(out, fmt.Sprintf("%s %d: %s", d.Code, d.Pos.Line-strings.Count(imports, "\n"), d.Message))
		}
	}
	sort.Strings(out)
	return out
}

func expectStaged(t *testing.T, src string, want ...string) {
	t.Helper()
	got := staged(t, src)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

// Where a stage may be written.
func TestWhereAStageIs(t *testing.T) {
	expectStaged(t, `
struct Poll { n: Int; }

impl Flow<Int> for Poll {
    fn run() -> Int {
        let total = 0;
        stage start;
        stage counted;
        defer {
            stage late;
        }
        if total > 1 {
            stage counted;
        }
        let f = fn() {
            stage inside;
        };
        f();
        return total;
    }
}

fn helper() {
    stage nowhere;
}
`,
		"F0002 10: stage late is inside a defer",
		"F0005 13: stage counted is written twice in Poll.run, first on line 19",
		"F0005 16: stage inside is written in the run of a flow itself",
		"F0005 24: stage nowhere is written in the run of a flow itself",
		"F0005 7: stage start is the beginning of run, which has that name already")
}

// A loop of run that waits with no stage in it or around it is said;
// one with a stage, or in a loop with one, or that does not wait, is not.
func TestALoopThatWaitsWithoutAStage(t *testing.T) {
	expectStaged(t, `
struct Poll { n: Int; }

impl Flow<Int> for Poll {
    fn run() -> Int {
        let total = 0;

        for i in 0..self.n {
            total += ask(i);
        }

        while total < 10 {
            wait time.after(10.milliseconds());
            total += 1;
        }

        for i in 0..3 {
            total += i;
        }

        while total < 20 {
            stage polling;
            for j in 0..2 {
                total += ask(j);
            }
        }

        return total;
    }
}

step fn ask(i: Int) -> Int {
    return i;
}
`,
		"W0011 12: a loop of Poll.run waits with no stage in it: the instance keeps everything it did since it started, and never moves to a new version",
		"W0011 8: a loop of Poll.run waits with no stage in it: the instance keeps everything it did since it started, and never moves to a new version")
}

// A migration gives the struct of a stage of this version, its fields
// the variables alive there, and is written for a flow.
func TestMigrations(t *testing.T) {
	got := staged(t, `
struct Sub { months: Int; }

struct Month { paid: Int; month: Int; }
struct Polling { paid: String; extra: Int; }
struct Other { n: Int; }

impl Flow<Int> for Sub {
    fn run() -> Int {
        let paid = 0;
        for month in 0..self.months {
            stage month;
            paid += 1;
        }
        let tries = 0;
        stage polling;
        return paid + tries;
    }
}

impl Sub {
    migrate fn month(paid: Int) -> Month {
        return Month { paid: paid, month: 0 };
    }

    migrate fn waiting(paid: Int) -> Polling {
        return Polling { paid: "x", extra: 1 };
    }

    migrate fn gone(paid: Int) -> Other {
        return Other { n: paid };
    }
}

struct Plain { n: Int; }

impl Plain {
    migrate fn month(n: Int) -> Month {
        return Month { paid: n, month: 0 };
    }
}
`)
	want := []string{
		"F0007 26: migrate fn waiting gives Polling, which is not the shape of stage polling: Polling has no field tries: Int; Polling.extra: Int is no variable of stage polling; Polling.paid is a String; paid is a Int at the stage",
		"F0007 30: migrate fn gone gives Other, which is the struct of no stage of Sub",
		"F0007 38: migrate fn month is written for Plain, which is no flow",
	}
	var errs []string
	for _, line := range got {
		if strings.HasPrefix(line, "F0007") {
			errs = append(errs, line)
		}
	}
	sort.Strings(want)
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(errs, "\n  "))
	}
}

// migrate fn fields gives the flow; no stage is named fields.
func TestMigratingFields(t *testing.T) {
	got := staged(t, `
struct Sub { months: Int; plan: String; }
struct Other { n: Int; }

impl Flow<Int> for Sub {
    fn run() -> Int {
        stage fields;
        return self.months;
    }
}

impl Sub {
    migrate fn fields(months: Int) -> Other {
        return Other { n: months };
    }
}
`)
	want := []string{
		"F0005 7: stage fields would be told apart from the fields of the flow by nothing: migrate fn fields is theirs",
		"F0007 13: migrate fn fields gives Other, not Sub: it makes the fields of the flow of what they were",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

// A flow that waits for its cancellation has the case in every wait match
// of its run; one that does not wait for it needs none.
func TestEveryWaitMatchHearsTheCancellation(t *testing.T) {
	got := staged(t, `
struct Ask {
    who: String;
    answers: flow.Inbox<String> = flow.inbox<String>(1);
}

impl Flow<String> for Ask {
    fn run() -> String {
        wait match {
            case let a = self.answers.receive(): { return a; }
            case let reason = flow.cancelled(): { return "withdrawn: ${reason}"; }
        }
        wait match {
            case time.after(10.milliseconds()): {}
            case let a = self.answers.receive(): { return a; }
        }
        return "";
    }
}

struct Deaf { n: Int; }

impl Flow<Int> for Deaf {
    fn run() -> Int {
        wait match {
            case time.after(10.milliseconds()): {}
            case time.after(20.milliseconds()): {}
        }
        return self.n;
    }
}
`)
	var unheard []string
	for _, line := range got {
		if strings.HasPrefix(line, "F0006") {
			unheard = append(unheard, line)
		}
	}
	want := "F0006 13: a wait match of Ask.run, which waits for its cancellation, has no case for it"
	if strings.Join(unheard, "\n") != want {
		t.Errorf("expected:\n  %s\ngot:\n  %s", want, strings.Join(unheard, "\n  "))
	}
}
