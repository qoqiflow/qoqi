// Package fuzz looks for what nobody thought to test: it feeds the
// compiler and the virtual machine inputs made by a machine, and checks
// that whatever they are, Qoqi answers with a result or an error of its
// own, never by failing itself.
//
// As ordinary tests, the functions here run on the programs of the
// repository. To let the machine search for new inputs:
//
//	go test ./test/fuzz -run '^$' -fuzz FuzzSource -fuzztime 60s
//	go test ./test/fuzz -run '^$' -fuzz FuzzBytecode -fuzztime 60s
//
// An input that makes one fail is saved under testdata/fuzz, and is run
// with the others from then on.
package fuzz

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/sandbox"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// TestMain guards every process of a search, of which there are several
// at once: each is ended well before it holds what a small program has no
// reason to need.
func TestMain(m *testing.M) {
	memguard.Enforce(1<<30, "a fuzzing process")
	os.Exit(m.Run())
}

// limits is what a program made by a machine gets: enough for the
// programs of the repository to do something, little enough for thousands
// of runs a minute.
var limits = sandbox.Limits{HeapBytes: 8 << 20, Steps: 200_000, Depth: 200, Output: 16 << 10, Time: 3 * time.Second}

// maxSource bounds the programs tried: longer ones are slower to compile
// and find nothing shorter ones do not.
const maxSource = 16 << 10

// noFiles is how imports are read: there is nothing to import but the
// standard library, which is not read from disk.
func noFiles(path string) (string, error) {
	return "", fmt.Errorf("%s: no such module", path)
}

// compile runs the whole compiler on a source text. A text that is not a
// program gives diagnostics; that is the compiler working.
func compile(src string) *vm.Chunk {
	errs := errors.NewHandler()
	result := compiler.CompileWith(src, "fuzz.qi", noFiles, errs)
	if errs.HasErrors() || result == nil || result.Chunk == nil {
		return nil
	}
	return result.Chunk
}

// run executes a chunk in the sandbox and fails the test if Qoqi itself
// went wrong. How the program ended otherwise is its own business.
func run(t *testing.T, chunk *vm.Chunk) (string, error) {
	t.Helper()
	out, err := sandbox.Run(chunk, limits)
	if sandbox.Broke(err) {
		t.Fatalf("the virtual machine failed:\n%v", err)
	}
	if vm.IsInterrupted(err) {
		// Steps bound what a program computes. One that had to be stopped
		// by the clock was kept busy by something they do not count
		t.Fatalf("the program was still running after %v, within its steps:\n%v", limits.Time, err)
	}
	return out, err
}

// sources are the programs of the repository in one file each: the
// examples and the cases of the end-to-end tests.
func sources(t testing.TB) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, pattern := range []string{"../../examples/*.qi", "../e2e/cases/*.qi"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			text, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if len(text) <= maxSource {
				found[file] = string(text)
			}
		}
	}
	if len(found) < 20 {
		t.Fatalf("only %d programs found to start from", len(found))
	}
	return found
}

// FuzzSource gives the compiler any text. A text that compiles is run.
func FuzzSource(f *testing.F) {
	for _, src := range sources(f) {
		f.Add(src)
	}
	// Small ones, which change faster into something else that compiles
	for _, src := range []string{
		`fn main() -> Int { return 0; }`,
		`fn main() -> Int { let xs = [1, 2, 3]; for x in xs { println(x); } return xs[5]; }`,
		`struct P { x: Int; } fn main() -> Int { let p = P { x: 1 }; p.x += 1; println(p); return p.x; }`,
		`enum E { A, B(Int) } fn main() -> Int { match E.B(2) { case E.A: { return 0; } case E.B(n): { return n; } } }`,
		`enum E { A, B(Int) } fn f() -> E { return E.B(2); } fn main() -> Int { if f() is E.B(n) && n > 1 { return n; } else if f() is E.A { return 0; } return 1; }`,
		`enum E { A, B(Int, E) } fn main() -> Int { let e = E.B(1, E.B(2, E.A)); let s = 0; while e is E.B(n, rest) { s += n; e = rest; } let p = (s, e is E.A); if p is (3, true) { return 0; } return 1; }`,
		`fn f(n: Int) -> Int { return f(n + 1); } fn main() -> Int { return f(0); }`,
		`fn main() -> Int { let s = "aé日"; let t = ""; for i in 0..3 { t = t + s[i]; } println(t); return 0; }`,
		`fn main() -> Int { let f = fn(n: Int) -> Int { return n * 2; }; defer println("done"); return f(21) / 0; }`,
	} {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > maxSource {
			t.Skip()
		}
		if chunk := compile(src); chunk != nil {
			run(t, chunk)
		}
	})
}

// FuzzBytecode gives the loader any bytes. A file that loads is verified
// and run: whatever it holds, it is refused or runs within the sandbox.
func FuzzBytecode(f *testing.F) {
	added := 0
	for _, src := range sources(f) {
		chunk := compile(src)
		if chunk == nil {
			continue
		}
		// Without what debug information adds: the instructions are what
		// is worth changing
		chunk.DebugInfo = map[int]*vm.DebugInfo{}
		if data, err := vm.SerializeChunk(chunk); err == nil && len(data) <= 256<<10 {
			f.Add(data)
			added++
		}
	}
	if added < 10 {
		f.Fatalf("only %d compiled programs to start from", added)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		chunk, err := vm.DeserializeChunk(data)
		if err != nil {
			return
		}
		if _, err := run(t, chunk); err != nil && strings.Contains(err.Error(), "invalid bytecode") {
			return
		}
		// What loads is written back as it was read
		again, err := vm.SerializeChunk(chunk)
		if err != nil {
			t.Fatalf("a chunk that was read cannot be written: %v", err)
		}
		if _, err := vm.DeserializeChunk(again); err != nil {
			t.Fatalf("a chunk written back cannot be read: %v", err)
		}
	})
}

// TestSandboxHoldsAProgramIn checks what the runs above rely on: a program
// in the sandbox ends, and reaches nothing outside itself.
func TestSandboxHoldsAProgramIn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.txt")
	if err := os.WriteFile(marker, []byte("still here"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, src string
		kind      vm.ErrorKind
		wrote     string
	}{
		{"a loop without end", `fn main() -> Int { let n = 0; while n >= 0 { n = (n + 1) % 10; } return n; }`, vm.KindBudget, ""},
		{"calls without end", `fn f(n: Int) -> Int { return f(n + 1) + 1; } fn main() -> Int { return f(0); }`, vm.KindDepth, ""},
		{"a loop of calls", `fn f(n: Int) -> Int { return n; } fn main() -> Int { let n = 0; while n >= 0 { n = f(n); } return n; }`, vm.KindBudget, ""},
		{"memory without end", `fn main() -> Int { let xs: [String] = []; let n = 0; while n >= 0 { xs.push("item " + toString(n)); n += 1; } return n; }`, "", ""},
		{"output without end", `fn main() -> Int { for i in 0..100000 { println("line", i); } return 0; }`, "", "line 0\nline 1\n"},
		{"removing a file", `import std.io; fn main() -> Int { println("before"); io.remove("` + marker + `"); println("after"); return 0; }`, vm.KindLibrary, "before\n"},
		{"reading a file", `import std.io; fn main() -> Int { println(io.readFile("` + marker + `")); return 0; }`, vm.KindLibrary, ""},
		{"running a program", `import std.os; fn main() -> Int { os.run("rm", ["` + marker + `"]); return 0; }`, vm.KindLibrary, ""},
		{"sleeping", `import std.time; fn main() -> Int { wait time.after(600000.milliseconds()); return 0; }`, vm.KindLibrary, ""},
		{"leaving", `import std.os; fn main() -> Int { os.exit(3); return 0; }`, vm.KindLibrary, ""},
		{"computing", `import std.strings; import std.math; fn main() -> Int { println("ok".upper(), 2.max(3)); return 0; }`, "", "OK 3\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunk := compile(c.src)
			if chunk == nil {
				t.Fatal("does not compile")
			}
			out, err := run(t, chunk)
			if c.wrote != "" && !strings.HasPrefix(out, c.wrote) {
				t.Errorf("wrote %.60q, expected it to start with %q", out, c.wrote)
			}
			if len(out) > limits.Output {
				t.Errorf("%d bytes of output kept, with room for %d", len(out), limits.Output)
			}
			if strings.Contains(out, "after") || strings.Contains(out, "still here") {
				t.Errorf("the program got through: %q", out)
			}
			switch failure, _ := err.(*vm.RuntimeError); {
			case c.kind == "" && c.name == "memory without end":
				if failure == nil || (failure.Kind != vm.KindMemory && failure.Kind != vm.KindBudget) {
					t.Errorf("expected it to run out of memory or of steps, got %v", err)
				}
			case c.kind == "":
				if err != nil {
					t.Errorf("stopped: %v", err)
				}
			case failure == nil || failure.Kind != c.kind:
				t.Errorf("expected a failure of kind %s, got %v", c.kind, err)
			}
		})
	}
	if text, err := os.ReadFile(marker); err != nil || string(text) != "still here" {
		t.Errorf("the file was reached: %q, %v", text, err)
	}
}
