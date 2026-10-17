package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// write puts files under a folder: names with slashes, their texts.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const (
	denseHello = "fn main(){println(\"hello\");}\n"
	tidyHello  = "fn main() {\n    println(\"hello\");\n}\n"
	denseShape = "pub struct Point{x:Int,y:Int}\npub fn origin()->Point{return Point{x:0,y:0};}\n"
	tidyShape  = "pub struct Point {\n    x: Int;\n    y: Int;\n}\n\npub fn origin() -> Point {\n    return Point { x: 0, y: 0 };\n}\n"
)

// TestFmt formats the files of a folder where they are: it names the ones
// it changed, leaves alone the ones that are fine and the folders that are
// hidden, and says nothing the second time.
func TestFmt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"main.qi":           denseHello,
		"geo/shape.qi":      denseShape,
		"geo/deep/fine.qi":  tidyHello,
		".git/hook.qi":      denseHello,
		"geo/.cache/old.qi": denseHello,
		"notes.txt":         denseHello,
	})

	res := runQoqiIn(t, dir, "", "fmt")
	if res.exit != 0 || res.stdout != "geo/shape.qi\nmain.qi\n" || res.stderr != "2 files formatted, 1 left as they were\n" {
		t.Fatalf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	for name, want := range map[string]string{
		"main.qi": tidyHello, "geo/shape.qi": tidyShape, "geo/deep/fine.qi": tidyHello,
		".git/hook.qi": denseHello, "geo/.cache/old.qi": denseHello, "notes.txt": denseHello,
	} {
		if got := read(t, dir, name); got != want {
			t.Errorf("%s: expected\n%s\ngot\n%s", name, want, got)
		}
	}

	if again := runQoqiIn(t, dir, "", "fmt"); again.exit != 0 || again.stdout != "" || again.stderr != "" {
		t.Errorf("formatting what is formatted has nothing to say: exit %d\n%s%s", again.exit, again.stdout, again.stderr)
	}

	// What it wrote is a program still
	if run := runQoqiIn(t, dir, "", "run", "main.qi"); run.exit != 0 || run.stdout != "hello\n" {
		t.Errorf("expected the formatted program to run: exit %d\n%s%s", run.exit, run.stdout, run.stderr)
	}
}

// Files and folders can be named, a hidden one too: what is named is
// what was asked for.
func TestFmtNamedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"a.qi": denseHello, "b.qi": denseHello, "sub/c.qi": denseHello, ".hidden/d.qi": denseHello})

	res := runQoqiIn(t, dir, "", "fmt", "b.qi", "sub", ".hidden")
	if res.exit != 0 || res.stdout != "b.qi\nsub/c.qi\n.hidden/d.qi\n" || res.stderr != "3 files formatted, 0 left as they were\n" {
		t.Fatalf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if read(t, dir, "a.qi") != denseHello || read(t, dir, "sub/c.qi") != tidyHello || read(t, dir, ".hidden/d.qi") != tidyHello {
		t.Error("expected the files named to be formatted, and no other")
	}

	missing := runQoqiIn(t, dir, "", "fmt", "nowhere.qi", "a.qi")
	if missing.exit != 1 || !strings.Contains(missing.stderr, "qoqi fmt: nowhere.qi: no such file or folder") {
		t.Errorf("expected an error for a file that is not there: exit %d\n%s", missing.exit, missing.stderr)
	}
	if read(t, dir, "a.qi") != tidyHello {
		t.Error("a file that is not there must not keep the others from being formatted")
	}

	unknown := runQoqiIn(t, dir, "", "fmt", "--fast")
	if unknown.exit != 2 || !strings.Contains(unknown.stderr, "unknown option --fast") || !strings.Contains(unknown.stderr, "Usage: qoqi fmt") {
		t.Errorf("expected an unknown option to be refused: exit %d\n%s", unknown.exit, unknown.stderr)
	}
}

// With --check nothing is written: the files that would change are
// named, and the status says whether there were any, for a script.
func TestFmtCheck(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"main.qi": denseHello, "fine.qi": tidyHello})

	res := runQoqiIn(t, dir, "", "fmt", "--check")
	if res.exit != 1 || res.stdout != "main.qi\n" || res.stderr != "1 file would change: run qoqi fmt\n" {
		t.Fatalf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if read(t, dir, "main.qi") != denseHello {
		t.Error("--check must not write")
	}

	if fine := runQoqiIn(t, dir, "", "fmt", "--check", "fine.qi"); fine.exit != 0 || fine.stdout != "" || fine.stderr != "" {
		t.Errorf("expected nothing to say about a formatted file: exit %d\n%s%s", fine.exit, fine.stdout, fine.stderr)
	}
}

// A file that does not read as a program is left as it is, with the place
// where it stops; the others are formatted all the same.
func TestFmtProblem(t *testing.T) {
	dir := t.TempDir()
	broken := "fn main() {\n    let a = 1\n    println(a);\n}\n"
	write(t, dir, map[string]string{"broken.qi": broken, "main.qi": denseHello})

	res := runQoqiIn(t, dir, "", "fmt")
	if res.exit != 1 || res.stdout != "main.qi\n" || !strings.Contains(res.stderr, "broken.qi:3:5: expected ';', found 'println'") {
		t.Fatalf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if read(t, dir, "broken.qi") != broken || read(t, dir, "main.qi") != tidyHello {
		t.Error("expected the broken file untouched and the other formatted")
	}
}

// `qoqi fmt -` is for an editor: the text comes in, the text goes out.
func TestFmtInput(t *testing.T) {
	res := runQoqiWithInput(t, denseShape, "fmt", "-")
	if res.exit != 0 || res.stdout != tidyShape || res.stderr != "" {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}

	broken := runQoqiWithInput(t, "fn main() {", "fmt", "-")
	if broken.exit != 1 || broken.stdout != "" || !strings.Contains(broken.stderr, "<input>:2:1: expected '}', found the end of the file") {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", broken.exit, broken.stdout, broken.stderr)
	}
}

// Every example of the repository, formatted, does what it did: the
// examples are copied, formatted where the copy is, and run against the
// same expected output as the originals.
func TestFormattedExamplesRunTheSame(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-R", filepath.Join(repoRoot, "examples"), dir).CombinedOutput(); err != nil {
		t.Fatalf("cannot copy the examples: %v\n%s", err, out)
	}
	examples := filepath.Join(dir, "examples")

	res := runQoqiIn(t, examples, "", "fmt")
	if res.exit != 0 {
		t.Fatalf("expected the examples to be formatted: exit %d\n%s", res.exit, res.stderr)
	}
	if again := runQoqiIn(t, examples, "", "fmt", "--check"); again.exit != 0 || again.stdout != "" {
		t.Errorf("expected the formatted examples to be formatted: exit %d\n%s", again.exit, again.stdout)
	}

	// A program that prints where it failed names a line, which formatting
	// moves, in a file the copy has elsewhere: places are compared as places
	place := regexp.MustCompile(`[\w./-]*\.qi:\d+(:\d+)?`)
	ran := 0
	check := func(name, target string) {
		t.Run(name, func(t *testing.T) {
			res := runQoqiIn(t, examples, "", "run", target)
			if res.exit != 0 || res.stderr != "" {
				t.Fatalf("expected a clean run, got exit code %d\nstderr:\n%s", res.exit, res.stderr)
			}
			want, err := os.ReadFile(filepath.Join(repoRoot, "examples", "expected", name+".out"))
			if err != nil {
				t.Fatal(err)
			}
			got, wanted := place.ReplaceAllString(res.stdout, "<place>"), place.ReplaceAllString(string(want), "<place>")
			if got != wanted {
				t.Errorf("formatted, the example prints\n%s\nand not\n%s", got, wanted)
			}
		})
		ran++
	}
	for _, path := range sources(t, "examples") {
		name := strings.TrimSuffix(filepath.Base(path), ".qi")
		check(name, name+".qi")
	}
	configs, _ := filepath.Glob(filepath.Join(examples, "*", "qoqi.json"))
	for _, config := range configs {
		name := filepath.Base(filepath.Dir(config))
		if opensAWindow[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(config), "qoqi.lock")); err == nil {
			t.Setenv("QOQI_CACHE", t.TempDir())
			if install := runQoqiIn(t, filepath.Dir(config), "", "pkg", "install"); install.exit != 0 {
				t.Fatalf("%s: qoqi install failed: exit %d\n%s", name, install.exit, install.stderr)
			}
		}
		check(name, name)
	}
	if ran < 5 {
		t.Errorf("expected the examples to be run, ran %d", ran)
	}
}

const (
	passingTests = `import std.test;
import shop.price;

fn testATotalAddsUp() {
    test.equal(price.total([1, 2, 3]), 6);
}

fn testAnEmptyBasketCostsNothing() {
    test.equal(price.total([]), 0);
}
`
	failingTests = `import std.test;
import shop.price;

fn fixture() -> [Int] {
    return [5, 5];
}

fn testADiscountIsTakenOff() {
    test.equal(price.discounted(fixture(), 10), 9);
}

fn testNothingIsFree() {
    test.equal(price.discounted(fixture(), 100), 1);
}

fn testAPriceIsRead() {
    let prices = fixture();
    test.equal(prices[2], 5);
}
`
	priceModule = `// sum, a method of arrays, needs no import

pub fn total(prices: [Int]) -> Int {
    return prices.sum();
}

pub fn discounted(prices: [Int], percent: Int) -> Int {
    return total(prices) * (100 - percent) / 100;
}
`
)

// shop writes a project with a module and two test files, one of which
// has tests that fail.
func shop(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"qoqi.json":                 `{"name": "shop", "entry": "main.qi"}`,
		"main.qi":                   "import shop.price;\nfn main() { println(price.total([1, 2])); }\n",
		"shop/price.qi":             priceModule,
		"shop/price_test.qi":        passingTests,
		"shop/discount_test.qi":     failingTests,
		".old/broken_test.qi":       "fn testNeverRun() { panic(\"hidden\"); }\n",
		"shop/notes_about_test.txt": "fn testNotQoqi() {}",
	})
	return dir
}

// TestTestCommand runs the tests of a project: every file *_test.qi
// under the folder, each test reported by the words of its name, the
// ones that fail with why and where, and a status that says whether all
// passed.
func TestTestCommand(t *testing.T) {
	dir := shop(t)

	res := runQoqiIn(t, dir, "", "test")
	want := `shop/discount_test.qi
  ok    a discount is taken off
  FAIL  nothing is free
        expected 1, got 0
        at shop/discount_test.qi:13
  FAIL  a price is read
        index out of bounds: 2 (length 2)
        at shop/discount_test.qi:18
1 passed, 2 failed

shop/price_test.qi
  ok    a total adds up
  ok    an empty basket costs nothing
2 passed

5 tests in 2 files: 3 passed, 2 failed
`
	if res.exit != 1 || res.stdout != want || res.stderr != "" {
		t.Fatalf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}

	// One file, by its name, from a folder that is not the project's: its
	// modules are found all the same
	one := runQoqiIn(t, filepath.Join(dir, "shop"), "", "test", "price_test.qi")
	if one.exit != 0 || one.stdout != "price_test.qi\n  ok    a total adds up\n  ok    an empty basket costs nothing\n2 passed\n" || one.stderr != "" {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", one.exit, one.stdout, one.stderr)
	}

	// A folder, and some of its tests
	some := runQoqiIn(t, dir, "", "test", "shop", "--filter=FREE")
	if some.exit != 1 || some.stdout != "shop/discount_test.qi\n  FAIL  nothing is free\n        expected 1, got 0\n        at shop/discount_test.qi:13\n0 passed, 1 failed\n" {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", some.exit, some.stdout, some.stderr)
	}
	passing := runQoqiIn(t, dir, "", "test", "--filter=basket")
	if passing.exit != 0 || passing.stdout != "shop/price_test.qi\n  ok    an empty basket costs nothing\n1 passed\n" {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", passing.exit, passing.stdout, passing.stderr)
	}
	if none := runQoqiIn(t, dir, "", "test", "--filter=zebra"); none.exit != 0 || none.stdout != "no test matches 'zebra'\n" {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", none.exit, none.stdout, none.stderr)
	}
}

// Without a project file, module paths start at the folder qoqi test
// runs in: shop/price_test.qi imports shop.price, run from the folder
// that holds shop, by its name or as part of every test file.
func TestTestCommandWithoutProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"shop/price.qi":      priceModule,
		"shop/price_test.qi": passingTests,
	})
	want := "shop/price_test.qi\n  ok    a total adds up\n  ok    an empty basket costs nothing\n2 passed\n"
	for _, args := range [][]string{{"test", "shop/price_test.qi"}, {"test"}, {"test", "shop"}} {
		res := runQoqiIn(t, dir, "", args...)
		if res.exit != 0 || res.stdout != want || res.stderr != "" {
			t.Errorf("qoqi %v: unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", args, res.exit, res.stdout, res.stderr)
		}
	}
}

// What is not a run of tests says so: a folder without test files, a
// path that is not there, an option nobody knows, a test file that does
// not compile or has a main of its own. The files that can be run are.
func TestTestCommandProblems(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"main.qi": tidyHello})
	if res := runQoqiIn(t, dir, "", "test"); res.exit != 0 || !strings.Contains(res.stdout, "no test files: a test file is called name_test.qi, and its tests fn testSomething()") {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if res := runQoqiIn(t, dir, "", "test", "nowhere"); res.exit != 1 || !strings.Contains(res.stderr, "nowhere: no such file or folder") {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if res := runQoqiIn(t, dir, "", "test", "--verbose"); res.exit != 2 || !strings.Contains(res.stderr, "unknown option --verbose") || !strings.Contains(res.stderr, "Usage: qoqi test") {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}

	write(t, dir, map[string]string{
		"a_test.qi": "import std.test;\nfn testWrong() {\n    test.equal(1, \"one\");\n}\n",
		"b_test.qi": "fn testIt() {}\nfn main() {}\n",
		"c_test.qi": "import std.test;\nfn testFine() {\n    test.equal(1, 1);\n}\n",
		"d_test.qi": "import std.os;\nfn testLeaves() {\n    os.exit(3);\n}\n",
		"e_test.qi": "fn helper() {}\n",
		"f_test.qi": "fn testBroken() {\n    let a = ;\n}\n",
		"g_test.qi": "fn testIt() {}\nprintln(\"a script\");\n",
	})
	res := runQoqiIn(t, dir, "", "test")
	if res.exit != 1 {
		t.Errorf("expected the run to count as failed, got exit %d", res.exit)
	}
	for _, want := range []string{
		"a_test.qi:3:",
		"b_test.qi: a test file has no main, nor statements outside a function: qoqi test writes the main that runs its tests",
		"g_test.qi: a test file has no main, nor statements outside a function",
		"d_test.qi: a test ended the program with os.exit(3): the tests after it did not run",
		"f_test.qi:2:",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("expected %q in stderr:\n%s", want, res.stderr)
		}
	}
	for _, want := range []string{"c_test.qi\n  ok    fine\n1 passed\n", "\n1 test in 1 file: 1 passed; 5 files did not run\n"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("expected %q in stdout:\n%s", want, res.stdout)
		}
	}
	if strings.Contains(res.stdout, "e_test.qi") {
		t.Errorf("a file without tests has nothing to report:\n%s", res.stdout)
	}
}

// The flows of the examples come with tests, run as their README says
// to: trials, on a clock of their own.
func TestExampleTests(t *testing.T) {
	res := runQoqi(t, "test", filepath.Join("examples", "flows"))
	if res.exit != 0 || res.stderr != "" || !strings.HasSuffix(res.stdout, "19 passed\n") || strings.Contains(res.stdout, "FAIL") {
		t.Errorf("unexpected run: exit %d\nstdout:\n%s\nstderr:\n%s", res.exit, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "  ok    an answer ends it\n") {
		t.Errorf("expected the tests by the words of their names:\n%s", res.stdout)
	}
}
