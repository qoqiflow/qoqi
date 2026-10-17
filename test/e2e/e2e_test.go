// Package e2e tests the qoqi binary the way a user runs it: it builds the
// command once, then feeds it real .qi files and checks stdout, stderr and
// the exit code.
//
// Each program in cases/ may have, next to it:
//
//	name.out   exact expected stdout (missing = no output expected)
//	name.err   lines that must each appear in stderr (missing = stderr empty)
//	name.exit  expected exit code (missing = 0)
//
// Every program in examples/ is run as well and compared with
// examples/expected/name.out, so the examples cannot rot.
//
// Regenerate the .out files after an intended change with:
//
//	go test ./test/e2e -update
package e2e

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the expected .out files")

// qoqi is the path of the binary built for this test run.
var qoqi string

const repoRoot = "../.."

// runTimeout bounds one run of the binary.
const runTimeout = 20 * time.Second

func TestMain(m *testing.M) {
	flag.Parse()

	dir, err := os.MkdirTemp("", "qoqi-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	qoqi = filepath.Join(dir, "qoqi")
	build := exec.Command("go", "build", "-o", qoqi, "./cmd/qoqi")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build qoqi: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout string
	stderr string
	exit   int
}

// runQoqi runs the binary from the repository root, so the paths printed
// in diagnostics do not depend on where the tests are started from.
func runQoqi(t *testing.T, args ...string) result {
	t.Helper()
	return runQoqiWithInput(t, "", args...)
}

// runQoqiWithInput is runQoqi with text supplied on standard input.
func runQoqiWithInput(t *testing.T, input string, args ...string) result {
	t.Helper()
	return runQoqiIn(t, repoRoot, input, args...)
}

// runQoqiIn runs the binary with dir as the current directory.
func runQoqiIn(t *testing.T, dir, input string, args ...string) result {
	t.Helper()

	// No test program needs more than a moment. A run that takes this long
	// has stopped making progress, or has become slow enough to count as
	// broken: fail instead of waiting for it.
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, qoqi, args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	res := result{}
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("qoqi %v did not finish within %s", args, runTimeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("cannot run qoqi %v: %v", args, err)
		}
		res.exit = exitErr.ExitCode()
	}
	res.stdout, res.stderr = stdout.String(), stderr.String()
	return res
}

// sources returns the .qi files of a directory given relative to the root.
func sources(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot, dir, "*.qi"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no .qi files found in %s", dir)
	}
	return files
}

func readOptional(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// checkStdout compares stdout with the golden file, or rewrites it.
func checkStdout(t *testing.T, got, goldenPath string) {
	t.Helper()

	if *update {
		if got == "" {
			os.Remove(goldenPath)
			return
		}
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, _ := readOptional(t, goldenPath)
	if got != want {
		t.Errorf("unexpected stdout\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}

func TestCases(t *testing.T) {
	for _, path := range sources(t, "test/e2e/cases") {
		name := strings.TrimSuffix(filepath.Base(path), ".qi")
		base := strings.TrimSuffix(path, ".qi")

		t.Run(name, func(t *testing.T) {
			res := runQoqi(t, "run", filepath.Join("test/e2e/cases", name+".qi"))

			wantExit := 0
			if text, ok := readOptional(t, base+".exit"); ok {
				code, err := strconv.Atoi(strings.TrimSpace(text))
				if err != nil {
					t.Fatalf("invalid %s.exit: %v", name, err)
				}
				wantExit = code
			}
			if res.exit != wantExit {
				t.Errorf("expected exit code %d, got %d\nstderr:\n%s", wantExit, res.exit, res.stderr)
			}

			checkStdout(t, res.stdout, base+".out")

			wantErr, hasErr := readOptional(t, base+".err")
			if !hasErr && res.stderr != "" {
				t.Errorf("expected nothing on stderr, got:\n%s", res.stderr)
			}
			for _, line := range strings.Split(wantErr, "\n") {
				if line = strings.TrimSpace(line); line != "" && !strings.Contains(res.stderr, line) {
					t.Errorf("expected stderr to contain %q, got:\n%s", line, res.stderr)
				}
			}
		})
	}
}

func TestExamples(t *testing.T) {
	for _, path := range sources(t, "examples") {
		name := strings.TrimSuffix(filepath.Base(path), ".qi")

		t.Run(name, func(t *testing.T) {
			res := runQoqi(t, "run", filepath.Join("examples", name+".qi"))
			if res.exit != 0 || res.stderr != "" {
				t.Fatalf("expected a clean run, got exit code %d\nstderr:\n%s", res.exit, res.stderr)
			}
			checkStdout(t, res.stdout, filepath.Join(repoRoot, "examples", "expected", name+".out"))
		})
	}
}

// opensAWindow lists the examples that are not run by the tests: they open
// a window and wait for somebody to play.
var opensAWindow = map[string]bool{"raylib": true}

// TestExampleProjects runs every example that is a folder with a project
// file, and compares its output with examples/expected/<folder>.out.
func TestExampleProjects(t *testing.T) {
	configs, err := filepath.Glob(filepath.Join(repoRoot, "examples", "*", "qoqi.json"))
	if err != nil || len(configs) == 0 {
		t.Fatal("no example projects found")
	}
	for _, config := range configs {
		name := filepath.Base(filepath.Dir(config))
		if opensAWindow[name] {
			// Compiled by TestWindowedExamplesCompile, and not run
			continue
		}

		t.Run(name, func(t *testing.T) {
			var res result
			if _, err := os.Stat(filepath.Join(filepath.Dir(config), "qoqi.lock")); err == nil {
				// A project with packages is installed first, in a copy, so
				// that nothing is written into the repository
				dir := filepath.Join(t.TempDir(), name)
				if err := os.CopyFS(dir, os.DirFS(filepath.Dir(config))); err != nil {
					t.Fatal(err)
				}
				t.Setenv("QOQI_CACHE", t.TempDir())
				if install := runQoqiIn(t, dir, "", "pkg", "install"); install.exit != 0 {
					t.Fatalf("qoqi install failed: exit %d\n%s", install.exit, install.stderr)
				}
				res = runQoqiIn(t, dir, "", "run")
			} else {
				res = runQoqi(t, "run", filepath.Join("examples", name))
			}
			if res.exit != 0 || res.stderr != "" {
				t.Fatalf("expected a clean run, got exit code %d\nstderr:\n%s", res.exit, res.stderr)
			}
			checkStdout(t, res.stdout, filepath.Join(repoRoot, "examples", "expected", name+".out"))
		})
	}
}

func TestDumpListing(t *testing.T) {
	res := runQoqi(t, "dump", "examples/hello.qi")
	if res.exit != 0 {
		t.Fatalf("dump failed with exit code %d:\n%s", res.exit, res.stderr)
	}
	for _, want := range []string{".func main", "CALLX", "println", `"Hello, Qoqi!"`, "RET"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("expected the listing to contain %q, got:\n%s", want, res.stdout)
		}
	}
}

func TestDumpIR(t *testing.T) {
	res := runQoqi(t, "dump", "examples/hello.qi", "--ir")
	if res.exit != 0 {
		t.Fatalf("ir failed with exit code %d:\n%s", res.exit, res.stderr)
	}
	for _, want := range []string{"fn main() {", "b0:", `MOV, "println"`, "CALL", "RET"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("expected the IR to contain %q, got:\n%s", want, res.stdout)
		}
	}
}

// TestMachineLimits covers the options that bound what a program may use.
func TestMachineLimits(t *testing.T) {
	deep := "test/e2e/cases/limits_recursion.qi"
	big := "test/e2e/cases/limits_memory.qi"

	// Within the defaults both programs run
	if res := runQoqi(t, "run", deep); res.exit != 0 || res.stdout != "12497500 5000\n" {
		t.Errorf("expected the recursion to finish, got exit %d, %q, %s", res.exit, res.stdout, res.stderr)
	}
	if res := runQoqi(t, "run", big); res.exit != 0 || res.stdout != "1500000 1124999250000\n" {
		t.Errorf("expected the large array to be built, got exit %d, %q, %s", res.exit, res.stdout, res.stderr)
	}

	// --max-depth lowers how deep calls may nest
	res := runQoqi(t, "run", "--max-depth=1000", deep)
	if res.exit != 1 || !strings.Contains(res.stderr, "stack overflow: max call depth 1000 exceeded") {
		t.Errorf("expected a stack overflow at depth 1000, got exit %d:\n%s", res.exit, res.stderr)
	}
	if !strings.Contains(res.stderr, "limits_recursion.qi") || !strings.Contains(res.stderr, "in function 'sum'") {
		t.Errorf("expected the overflow to name where it happened, got:\n%s", res.stderr)
	}

	// --heap-mb lowers how much memory it may hold; the option may follow the file
	res = runQoqi(t, "run", big, "--heap-mb=2")
	if res.exit != 1 || !strings.Contains(res.stderr, "out of memory") || !strings.Contains(res.stderr, "limit 2097152") {
		t.Errorf("expected the program to run out of 2MB, got exit %d:\n%s", res.exit, res.stderr)
	}
	// ...and raising it is fine
	if res := runQoqi(t, "run", "--heap-mb=512", "--max-depth=20000", big); res.exit != 0 {
		t.Errorf("expected generous limits to work, got exit %d:\n%s", res.exit, res.stderr)
	}

	for _, bad := range [][]string{
		{"run", "--heap-mb=lots", big},
		{"run", "--heap-mb=0", big},
		{"run", "--max-depth", deep},
		{"run", "--speed=9", big},
	} {
		res := runQoqi(t, bad...)
		if res.exit != 1 || !strings.Contains(res.stderr, "Error:") || res.stdout != "" {
			t.Errorf("%v: expected an option error, got exit %d, %q, %q", bad, res.exit, res.stdout, res.stderr)
		}
	}
}

// TestCPUProfile covers --cpuprofile: the profile is written however the
// program ends, and only qoqi's own options are taken for it.
func TestCPUProfile(t *testing.T) {
	dir := t.TempDir()
	programs := map[string]string{
		"busy.qi": `fn spin(n: Int) -> Int {
    let total = 0;
    for i in 0..n {
        total = (total + i * 7) % 1000003;
    }
    return total;
}

fn main() -> Int {
    println(spin(300000));
    return 0;
}
`,
		"code.qi": `fn main() -> Int {
    println("leaving with 3");
    return 3;
}
`,
		"fails.qi": `fn main() -> Int {
    let items = [1, 2, 3];
    println(items[7]);
    return 0;
}
`,
		"args.qi": `import std.os;

fn main() -> Int {
    println(os.args());
    return 0;
}
`,
	}
	write(t, dir, programs)

	// A profile is a gzipped protocol buffer: what go tool pprof reads
	profiled := func(name string) bool {
		data, err := os.ReadFile(filepath.Join(dir, name))
		return err == nil && len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b
	}

	for _, c := range []struct {
		args   []string
		stdout string
		exit   int
		output string
	}{
		{[]string{"run", "--cpuprofile=" + filepath.Join(dir, "busy.prof"), filepath.Join(dir, "busy.qi")}, "", 0, "busy.prof"},
		// The option may follow the file, like the others
		{[]string{"run", filepath.Join(dir, "code.qi"), "--cpuprofile=" + filepath.Join(dir, "code.prof")}, "leaving with 3\n", 3, "code.prof"},
		// A failure ends the process early, and the profile is still written
		{[]string{"run", "--cpuprofile=" + filepath.Join(dir, "fails.prof"), filepath.Join(dir, "fails.qi")}, "", 1, "fails.prof"},
	} {
		res := runQoqi(t, c.args...)
		if res.exit != c.exit {
			t.Errorf("%v: expected exit %d, got %d:\n%s", c.args, c.exit, res.exit, res.stderr)
		}
		if c.stdout != "" && res.stdout != c.stdout {
			t.Errorf("%v: expected %q, got %q", c.args, c.stdout, res.stdout)
		}
		if !profiled(c.output) {
			t.Errorf("%v: expected a profile in %s", c.args, c.output)
		}
	}

	// After "--" it is the program's, like any other argument
	res := runQoqi(t, "run", filepath.Join(dir, "args.qi"), "--", "--cpuprofile="+filepath.Join(dir, "never.prof"))
	if res.exit != 0 || !strings.Contains(res.stdout, "--cpuprofile=") || profiled("never.prof") {
		t.Errorf("expected the program to be given the option, got exit %d, %q:\n%s", res.exit, res.stdout, res.stderr)
	}

	for _, bad := range [][]string{
		{"run", "--cpuprofile", filepath.Join(dir, "busy.qi")},
		{"run", "--cpuprofile=", filepath.Join(dir, "busy.qi")},
		{"run", "--cpuprofile=" + filepath.Join(dir, "no", "such", "folder.prof"), filepath.Join(dir, "busy.qi")},
	} {
		res := runQoqi(t, bad...)
		if res.exit != 1 || !strings.Contains(res.stderr, "Error:") || res.stdout != "" {
			t.Errorf("%v: expected an option error, got exit %d, %q, %q", bad, res.exit, res.stdout, res.stderr)
		}
	}
}

func TestMissingFile(t *testing.T) {
	res := runQoqi(t, "run", "does/not/exist.qi")
	if res.exit != 1 || !strings.Contains(res.stderr, "cannot read does/not/exist.qi") {
		t.Errorf("expected exit code 1 and a read error, got %d:\n%s", res.exit, res.stderr)
	}
	if res.stdout != "" {
		t.Errorf("expected nothing on stdout, got:\n%s", res.stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"dump"}, {"dump", "--ir"}, {"serve", "a", "b"}, {"start"}, {"no-such-command"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			res := runQoqi(t, args...)
			if res.exit != 1 {
				t.Errorf("expected exit code 1, got %d", res.exit)
			}
			if !strings.Contains(res.stdout, "Usage") {
				t.Errorf("expected usage help, got:\n%s", res.stdout)
			}
		})
	}
}

// TestLargeProgram runs a program well past what the bytecode format once
// allowed: 256 functions and 256 constants per program, 256 registers per
// function. It is generated, since it is two thousand lines of repetition.
func TestLargeProgram(t *testing.T) {
	const count = 500

	var sb strings.Builder
	sb.WriteString("trait Step {\n    fn apply(n: Int) -> Int;\n    fn label() -> String;\n}\n\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "struct Step%d { amount%d: Int; }\n", i, i)
		fmt.Fprintf(&sb, "impl Step for Step%d {\n", i)
		fmt.Fprintf(&sb, "    fn apply(n: Int) -> Int { return n + self.amount%d + %d; }\n", i, 70000+i)
		fmt.Fprintf(&sb, "    fn label() -> String { return \"step %d\"; }\n}\n", i)
		fmt.Fprintf(&sb, "fn step%d() -> Step { return Step%d { amount%d: %d }; }\n\n", i, i, i, i)
	}
	sb.WriteString("fn main() -> Int {\n    let steps: [Step] = [];\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "    let s%d = step%d();\n    steps.push(s%d);\n", i, i, i)
	}
	fmt.Fprintf(&sb, `    let value = 0;
    let last = "";
    for step in steps {
        value = step.apply(value);
        last = step.label();
    }
    println(steps.len(), value, last, s%d.label());
    return 7;
}
`, count-1)

	value := 0
	for i := 0; i < count; i++ {
		value += i + 70000 + i
	}
	want := fmt.Sprintf("%d %d step %d step %d\n", count, value, count-1, count-1)

	dir := t.TempDir()
	source := filepath.Join(dir, "large.qi")
	if err := os.WriteFile(source, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	res := runQoqi(t, "run", source)
	if res.exit != 7 || res.stdout != want || res.stderr != "" {
		t.Fatalf("run: expected exit 7 and %q, got exit %d, %q\n%s", want, res.exit, res.stdout, res.stderr)
	}

	// The listing shows the operands that did not fit before
	res = runQoqi(t, "dump", source)
	if res.exit != 0 {
		t.Fatalf("dump failed:\n%s", res.stderr)
	}
	text := []byte(res.stdout)
	if !strings.Contains(string(text), fmt.Sprintf(".func step%d ", count-1)) {
		t.Errorf("expected the listing to define step%d", count-1)
	}
	if !regexp.MustCompile(fmt.Sprintf(`CALLK r\d+ \d+ step%d\n`, count-1)).Match(text) {
		t.Errorf("expected the listing to refer to step%d by name", count-1)
	}
	// Constant indexes past 255 are printed in full, loaded or added
	if !regexp.MustCompile(`(LOADK r\d+|ADDK r\d+ r\d+) \d{4}\b`).Match(text) {
		t.Error("expected the listing to use a constant with a four-digit index")
	}
}

// TestModulesAreFoundNextToTheEntryFile checks that modules are looked up
// from the folder of the entry file, whatever the current directory and
// however the file is named on the command line.
func TestModulesAreFoundNextToTheEntryFile(t *testing.T) {
	dir := t.TempDir()
	write := func(path, src string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/start.qi", "import lib.text;\nimport peer;\nfn main() -> Int { println(text.shout(peer.word())); return 0; }\n")
	write("app/lib/text.qi", "pub fn shout(s: String) -> String { return s + \"!\"; }\n")
	write("app/peer.qi", "import lib.text;\npub fn word() -> String { return text.shout(\"hey\"); }\n")
	// A file outside the program's folder is not reachable
	write("text.qi", "pub fn shout(s: String) -> String { return \"wrong file\"; }\n")

	// An absolute path; the tests run from the repository root
	res := runQoqi(t, "run", filepath.Join(dir, "app", "start.qi"))
	if res.exit != 0 || res.stdout != "hey!!\n" {
		t.Errorf("expected hey!!, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
	}

	// A module is not found through a parent folder
	write("app/start.qi", "import text;\nfn main() -> Int { return 0; }\n")
	res = runQoqi(t, "run", filepath.Join(dir, "app", "start.qi"))
	if res.exit != 1 || !strings.Contains(res.stderr, "E8001") || !strings.Contains(res.stderr, filepath.Join(dir, "app", "text.qi")) {
		t.Errorf("expected the module to be looked for in app/, got exit %d:\n%s", res.exit, res.stderr)
	}

	// An error in a module shows the module's file and line
	write("app/start.qi", "import lib.text;\nfn main() -> Int { println(text.shout(1)); return 0; }\n")
	write("app/lib/text.qi", "pub fn shout(s: String) -> String {\n    let n: Int = s;\n    return s;\n}\n")
	res = runQoqi(t, "run", filepath.Join(dir, "app", "start.qi"))
	for _, want := range []string{"E2001", filepath.Join(dir, "app", "lib", "text.qi") + ":2:", "let n: Int = s;", "E2010", "start.qi:2:"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("expected stderr to contain %q, got:\n%s", want, res.stderr)
		}
	}
}

// project writes the files of a program into a new folder and returns it.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestExampleProject runs the example that has a project file, the ways a
// project can be run.
func TestExampleProject(t *testing.T) {
	want := "  1 * ada *\n  2 * grace *\n  3 * linus *\n------------\n    3 guests\n"
	example := filepath.Join(repoRoot, "examples", "project")

	runs := []struct {
		name string
		dir  string
		args []string
	}{
		{"the project folder", repoRoot, []string{"run", "examples/project"}},
		{"its entry file", repoRoot, []string{"run", "examples/project/src/main.qi"}},
		{"no argument, in the folder", example, []string{"run"}},
		{"no argument, deep inside it", filepath.Join(example, "src", "app"), []string{"run"}},
		{"the entry file, from inside", filepath.Join(example, "src"), []string{"run", "main.qi"}},
		{"a dot", example, []string{"run", "."}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			res := runQoqiIn(t, r.dir, "", r.args...)
			if res.exit != 0 || res.stdout != want || res.stderr != "" {
				t.Errorf("expected the guest list, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
			}
		})
	}

	// The other commands take a project the same way
	if res := runQoqiIn(t, example, "", "dump", "--ir"); !strings.Contains(res.stdout, "fn textkit.format.pad.left(") || !strings.Contains(res.stdout, "fn app.guests.invited(") {
		t.Errorf("expected the IR of the project, got:\n%s%s", res.stdout, res.stderr)
	}
	if res := runQoqi(t, "check", "examples/project"); res.exit != 0 || !strings.Contains(res.stdout, "no problems") {
		t.Errorf("expected check to take a project folder, got exit %d:\n%s%s", res.exit, res.stdout, res.stderr)
	}

	// The project sets max_depth to 2000; the command line wins over it
	res := runQoqi(t, "run", "--max-depth=5", "examples/project")
	if res.exit != 1 || !strings.Contains(res.stderr, "max call depth 5 exceeded") || !strings.Contains(res.stderr, "examples/project/packages/textkit/format/pad.qi") {
		t.Errorf("expected a stack overflow in the package, got exit %d:\n%s", res.exit, res.stderr)
	}
}

func TestProjectFile(t *testing.T) {
	deep := "fn down(n: Int) -> Int { if n == 0 { return 0; } return 1 + down(n - 1); }\n"

	t.Run("limits", func(t *testing.T) {
		dir := project(t, map[string]string{
			"qoqi.json": `{"entry": "main.qi", "max_depth": 50, "heap_mb": 1}`,
			"main.qi":   deep + "fn main() -> Int { println(down(30)); println(down(100)); return 0; }\n",
			"big.qi":    "fn main() -> Int { let xs: [Int] = []; let i = 0; while i < 2000000 { xs.push(i); i++; } return 0; }\n",
		})
		res := runQoqiIn(t, dir, "", "run")
		if res.exit != 1 || res.stdout != "30\n" || !strings.Contains(res.stderr, "max call depth 50 exceeded") {
			t.Errorf("expected the project's depth limit, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}
		// Limits apply to any file of the project, not only the entry
		res = runQoqiIn(t, dir, "", "run", "big.qi")
		if res.exit != 1 || !strings.Contains(res.stderr, "out of memory") || !strings.Contains(res.stderr, "limit 1048576") {
			t.Errorf("expected the project's memory limit, got exit %d:\n%s", res.exit, res.stderr)
		}
		// Options on the command line come first
		res = runQoqiIn(t, dir, "", "run", "--max-depth=500", "--heap-mb=128", "big.qi")
		if res.exit != 0 {
			t.Errorf("expected the command line to lift the limits, got exit %d:\n%s", res.exit, res.stderr)
		}
		if res := runQoqiIn(t, dir, "", "run", "--max-depth=500"); res.exit != 0 || res.stdout != "30\n100\n" {
			t.Errorf("expected the command line to lift the depth limit, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}
	})

	t.Run("root", func(t *testing.T) {
		files := map[string]string{
			"qoqi.json":             `{"entry": "code/bin/start.qi", "root": "code"}`,
			"code/bin/start.qi":     "import lib.greet;\nfn main() -> Int { println(greet.hello()); return 0; }\n",
			"code/lib/greet.qi":     "pub fn hello() -> String { return \"from the root\"; }\n",
			"code/bin/lib/greet.qi": "pub fn hello() -> String { return \"from next to the entry\"; }\n",
		}
		dir := project(t, files)
		if res := runQoqiIn(t, dir, "", "run"); res.exit != 0 || res.stdout != "from the root\n" {
			t.Errorf("expected module paths to start at the root, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}

		// Without "root" they start at the folder of the project file
		files["qoqi.json"] = `{"entry": "code/bin/start.qi"}`
		files["code/bin/start.qi"] = "import code.lib.greet;\nfn main() -> Int { println(greet.hello()); return 0; }\n"
		dir = project(t, files)
		if res := runQoqiIn(t, dir, "", "run"); res.exit != 0 || res.stdout != "from the root\n" {
			t.Errorf("expected module paths to start at the project file, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}

		// Without a project file they start next to the file that is run
		delete(files, "qoqi.json")
		files["code/bin/start.qi"] = "import lib.greet;\nfn main() -> Int { println(greet.hello()); return 0; }\n"
		dir = project(t, files)
		if res := runQoqiIn(t, dir, "", "run", "code/bin/start.qi"); res.exit != 0 || res.stdout != "from next to the entry\n" {
			t.Errorf("expected module paths to start at the entry file, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}
	})

	t.Run("packages", func(t *testing.T) {
		dir := project(t, map[string]string{
			"app/qoqi.json":      `{"entry": "main.qi", "packages": {"kit": "../shared/kit"}}`,
			"app/main.qi":        "import kit;\nimport kit.math.{double};\nfn main() -> Int { println(kit.name(), double(21)); return 0; }\n",
			"shared/kit.qi":      "import kit.math;\npub fn name() -> String { return \"kit\" + toString(math.double(1)); }\n",
			"shared/kit/math.qi": "pub fn double(n: Int) -> Int { return n * 2; }\n",
		})
		app := filepath.Join(dir, "app")
		if res := runQoqiIn(t, app, "", "run"); res.exit != 0 || res.stdout != "kit2 42\n" {
			t.Errorf("expected the package to be found, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}

		// A module the package does not have: the message names its folder
		if err := os.WriteFile(filepath.Join(app, "main.qi"), []byte("import kit.text;\nfn main() -> Int { return 0; }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		res := runQoqiIn(t, app, "", "run")
		if res.exit != 1 || !strings.Contains(res.stderr, "E8001") || !strings.Contains(res.stderr, "'kit' is a package in the folder") {
			t.Errorf("expected a missing module of the package, got exit %d:\n%s", res.exit, res.stderr)
		}
	})

	t.Run("mistakes", func(t *testing.T) {
		tests := []struct {
			name   string
			config string
			args   []string
			want   string
		}{
			{"unknown key", `{"main": "main.qi"}`, []string{"run"}, `qoqi.json: unknown key "main"; the keys are name, entry, root, packages, heap_mb, max_depth`},
			{"not JSON", "{\n  entry: \"main.qi\"\n}", []string{"run"}, "qoqi.json: line 2, column 3: not valid JSON"},
			{"no entry", `{"name": "x"}`, []string{"run"}, `does not say where the program starts; add "entry": "main.qi"`},
			{"no entry, for check", `{}`, []string{"check"}, `does not say where the program starts`},
			{"entry that does not exist", `{"entry": "start.qi"}`, []string{"run"}, "cannot read start.qi"},
			// A broken project file is reported even when a file is named
			{"broken, with a file", `{"heap_mb": "lots"}`, []string{"run", "main.qi"}, `"heap_mb" must be a whole number`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := project(t, map[string]string{
					"qoqi.json": tt.config,
					"main.qi":   "fn main() -> Int { return 0; }\n",
				})
				res := runQoqiIn(t, dir, "", tt.args...)
				if res.exit != 1 || !strings.Contains(res.stderr, tt.want) {
					t.Errorf("expected exit 1 and %q, got exit %d:\n%s%s", tt.want, res.exit, res.stdout, res.stderr)
				}
				if res.stdout != "" {
					t.Errorf("expected nothing on stdout, got %q", res.stdout)
				}
			})
		}
	})

	t.Run("no project", func(t *testing.T) {
		dir := project(t, map[string]string{"sub/main.qi": "fn main() -> Int { println(\"plain\"); return 0; }\n"})

		// Nothing to run: the usage, as before
		if res := runQoqiIn(t, dir, "", "run"); res.exit != 1 || !strings.Contains(res.stdout, "Usage: qoqi run") {
			t.Errorf("expected the usage, got exit %d: %q %q", res.exit, res.stdout, res.stderr)
		}
		// A folder that is not a project
		if res := runQoqiIn(t, dir, "", "run", "sub"); res.exit != 1 || !strings.Contains(res.stderr, "sub is a folder without a qoqi.json") {
			t.Errorf("expected a message about the folder, got exit %d: %q", res.exit, res.stderr)
		}
		// A file runs without one
		if res := runQoqiIn(t, dir, "", "run", "sub/main.qi"); res.exit != 0 || res.stdout != "plain\n" {
			t.Errorf("expected a single file to run, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
		}
	})
}

// TestStandardLibrary runs programs that use the library built into the
// binary: nothing but the binary and the program's own file is needed.
func TestStandardLibrary(t *testing.T) {
	// A program in a folder with nothing else in it, run from elsewhere
	dir := project(t, map[string]string{
		"greet.qi": `
import std.io;

fn main() -> Int {
    let name = io.ask("Who are you? ");
    if name == null {
        println("nobody there");
        return 1;
    }
    println("Hello, " + name.trim().capitalize() + "!");

    let numbers: [Int] = [];
    for line in io.readLines() {
        let n = line.parseInt();
        if n != null { numbers.push(n); }
    }
    println(numbers.len(), "numbers, sum", numbers.sum(), "largest", numbers.largest() ?? 0);
    return 0;
}
`,
	})
	program := filepath.Join(dir, "greet.qi")

	// Input comes from standard input
	res := runQoqiWithInput(t, "  ada \n4\nx\n10\n\n-3\n", "run", program)
	if res.exit != 0 || res.stdout != "Who are you? Hello, Ada!\n3 numbers, sum 11 largest 10\n" || res.stderr != "" {
		t.Errorf("unexpected run: exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
	}
	// No input at all: readLine gives null at once
	res = runQoqiIn(t, dir, "", "run", "greet.qi")
	if res.exit != 1 || res.stdout != "Who are you? nobody there\n" {
		t.Errorf("expected the program to see the end of the input, got exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
	}

	// The library's functions appear under their module's name
	ir := runQoqi(t, "dump", program, "--ir")
	for _, name := range []string{"fn std.strings.capitalize(", "fn std.arrays.sum(", "fn std.io.ask("} {
		if !strings.Contains(ir.stdout, name) {
			t.Errorf("expected the IR to contain %q", name)
		}
	}
	// ...and the natives are host functions, not compiled code
	if strings.Contains(ir.stdout, "fn std.strings.trim(") || strings.Contains(ir.stdout, "fn std.io.readLine(") {
		t.Error("did not expect native functions among the compiled ones")
	}
	listing := runQoqi(t, "dump", program)
	if listing.exit != 0 {
		t.Fatalf("dump failed:\n%s", listing.stderr)
	}
	if !regexp.MustCompile(`CALLX r\d+ \d+ std\.io\.readLine`).MatchString(listing.stdout) {
		t.Error("expected the listing to call std.io.readLine as a host function")
	}

	// Files: written by one run, read by the next
	notes := filepath.Join(dir, "notes.txt")
	files := project(t, map[string]string{
		"save.qi": "import std.io;\nfn main() -> Int {\n    if io.appendFile(\"" + notes + "\", \"a line\\n\").isOk() { return 0; }\n    return 1;\n}\n",
		"load.qi": "import std.io;\nimport std.strings;\nfn main() -> Int {\n    println(io.readFileLines(\"" + notes + "\").or([]).len());\n    return 0;\n}\n",
	})
	for i := 0; i < 3; i++ {
		if res := runQoqiIn(t, files, "", "run", "save.qi"); res.exit != 0 {
			t.Fatalf("expected the file to be written, got exit %d:\n%s", res.exit, res.stderr)
		}
	}
	if res := runQoqiIn(t, files, "", "run", "load.qi"); res.stdout != "3\n" {
		t.Errorf("expected three lines in the file, got %q\n%s", res.stdout, res.stderr)
	}

	// A project may replace the library with a package of its own
	own := project(t, map[string]string{
		"qoqi.json":     `{"entry": "main.qi", "packages": {"std": "mystd"}}`,
		"main.qi":       "import std.math;\nfn main() -> Int { println(math.abs(-5)); return 0; }\n",
		"mystd/math.qi": "pub fn abs(n: Int) -> Int { return 0 - 1; }\n",
	})
	if res := runQoqiIn(t, own, "", "run"); res.stdout != "-1\n" {
		t.Errorf("expected the project's own std to be used, got %q\n%s", res.stdout, res.stderr)
	}
}
