package flows_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/flows"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A version compared with the one deployed last, as its flow.lock keeps
// it: what it changed of a stage, and no migration answers, is said where
// it is written, before anything is deployed.

// counter is a flow whose stage holds count; more adds variables, and
// lines that go after `let count = 0;`, or in the impl.
func counter(fields, more, after, impl string) string {
	return `import std.flow;
import std.flow.{Flow, Inbox};

struct Counter {
    notes: Inbox<String> = flow.inbox<String>(8);` + fields + `
}
` + more + `
impl Flow<Int> for Counter {
    fn run() -> Int {
        let count = 0;` + after + `

        while true {
            stage counting;

            let note = wait self.notes.receive();

            if note == "stop" {
                return count;
            }

            count = count + 1;
        }

        return 0;
    }
` + impl + `
}

fn main() {}
`
}

// lockOf is the lock a version leaves when it is deployed.
func lockOf(t *testing.T, src string) *flowrt.Lock {
	t.Helper()
	errs := errors.NewHandler()
	result := compiler.Compile(src, "counter.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	return &flowrt.Lock{Version: vm.Fingerprint(result.Chunk), Flows: flowrt.ShapesOf(result.Chunk, flows.FlowsOf(result.AST))}
}

// warned is what a version is told against a lock: code, line, message.
func warned(t *testing.T, src string, lock *flowrt.Lock) []string {
	t.Helper()
	errs := errors.NewHandler()
	compiler.CompileOptions(src, "counter.qi", compiler.Options{Lock: lock}, errs)
	var out []string
	for _, d := range errs.Diagnostics {
		if d.Severity == errors.SeverityError {
			t.Fatalf("unexpected error: %s", d.String())
		}
		if d.Code == flows.WarnUnmigrated {
			// Said at the line it is about
			line := strings.TrimSpace(strings.Split(src, "\n")[d.Pos.Line-1])
			out = append(out, fmt.Sprintf("%s | %s", line, d.Message))
		}
	}
	sort.Strings(out)
	return out
}

const listening = `
struct Counting {
    count: Int;
    last: String;
}
`

func TestAVersionComparedWithTheOneDeployed(t *testing.T) {
	deployed := counter("", "", "", "")
	lock := lockOf(t, deployed)

	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{"the same", deployed, nil},
		{"no lock", "", nil},
		{"a variable more, no migration", counter("", listening, "\n        let last = \"\";", ""),
			[]string{"stage counting; | since the version deployed, stage counting of Counter: last: String is new, and no migrate fn counting says how an instance there goes on"}},
		{"a variable more, migrated", counter("", listening, "\n        let last = \"\";",
			"\n    migrate fn counting(count: Int) -> Counting {\n        return Counting { count: count, last: \"\" };\n    }"), nil},
		{"a migration that takes what was not", counter("", listening, "\n        let last = \"\";",
			"\n    migrate fn counting(count: String) -> Counting {\n        return Counting { count: 0, last: count };\n    }"),
			[]string{"migrate fn counting(count: String) -> Counting { | since the version deployed, stage counting of Counter: migrate fn counting takes count as a String; it was a Int there"}},
		{"a field more, no migration of the fields", counter("\n    owner: String;", "", "", ""),
			[]string{"stage counting; | since the version deployed, stage counting of Counter: self.owner: String is new, and no migrate fn fields says what an instance's fields become"}},
		{"a field more, migrated", counter("\n    owner: String;", "", "",
			"\n    migrate fn fields(notes: Inbox<String>) -> Counter {\n        return Counter { notes: notes, owner: \"nobody\" };\n    }"), nil},
		{"the stage gone", strings.Replace(deployed, "stage counting;", "stage tallying;", 1),
			[]string{"fn run() -> Int { | since the version deployed, stage counting of Counter: it is gone, and no migrate fn counting says how an instance there goes on"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := lock
			if c.src == "" {
				l, c.src = nil, counter("\n    owner: String;", "", "", "")
			}
			got := warned(t, c.src, l)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(c.want, "\n  "))
			}
		})
	}

	// A flow the lock does not know, or a stage it does not, is no change:
	// no instance is there
	other := strings.Replace(deployed, "fn main() {}", `struct Other {
    n: Int;
}

impl Flow<Int> for Other {
    fn run() -> Int {
        stage only;
        return self.n;
    }
}

fn main() {}`, 1)
	if got := warned(t, other, lock); len(got) > 0 {
		t.Errorf("a new flow: %v", got)
	}
}

// The lock next to the program is read, as the editor and qoqi check
// read it: shop.flow.lock next to shop.qi.
func TestTheLockNextToTheProgram(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "counter.qi")
	if err := flowrt.WriteLock(filepath.Join(dir, "counter.flow.lock"), lockOf(t, counter("", "", "", ""))); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(file, []byte(counter("", listening, "\n        let last = \"\";", "")), 0o644)
	errs := errors.NewHandler()
	if _, err := compiler.CompileFileOptions(file, compiler.Options{}, errs); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range errs.Diagnostics {
		if d.Code == flows.WarnUnmigrated && d.Severity == errors.SeverityWarning && strings.Contains(d.Suggestion, "needs a migrate fn counting") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the lock next to the program read: %v", errs.Diagnostics)
	}
}
