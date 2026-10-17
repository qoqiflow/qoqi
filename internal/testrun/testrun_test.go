package testrun_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/testrun"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// TestMain guards the tests: they run Qoqi programs.
func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

func TestTitle(t *testing.T) {
	for function, want := range map[string]string{
		"testATotalAddsUp":            "a total adds up",
		"testAddsUp":                  "adds up",
		"testReadsJSON":               "reads JSON",
		"testReadsJSONText":           "reads JSON text",
		"testHTTPServerAnswers404":    "HTTP server answers404",
		"test_an_empty_basket":        "an empty basket",
		"testX":                       "x",
		"test":                        "test",
		"test2Plus2":                  "2 plus2",
		"testAnEmptyBasketCostsALot_": "an empty basket costs a lot",
	} {
		if got := testrun.Title(function); got != want {
			t.Errorf("Title(%q) = %q, want %q", function, got, want)
		}
	}
}

func names(tests []testrun.Test) string {
	var all []string
	for _, test := range tests {
		all = append(all, test.Function)
	}
	return strings.Join(all, " ")
}

// A test is a function at the top of the file, named test..., that takes
// nothing and gives nothing: not a helper that happens to be called
// testData, not a method, not a word that only starts with the letters.
func TestTests(t *testing.T) {
	src := `
import std.test;

struct Basket { items: [Int]; }

impl Basket {
    fn testMethod() {}
}

fn testFirst() { test.equal(1, 1); }
fn helper() {}
fn testData() -> [Int] { return [1]; }
fn testWith(n: Int) {}
fn testGeneric<T>() {}
fn tested() {}
fn testimony() {}
pub fn testSecond() -> Void {}
fn test_third() {}
fn test4() {}
fn test() {}
extern fn testNative();
`
	tests, hasMain := testrun.Tests(src)
	if got := names(tests); got != "testFirst testSecond test_third test4 test" {
		t.Errorf("unexpected tests: %s", got)
	}
	if hasMain {
		t.Error("the file has no main")
	}
	if tests[0].Title != "first" || tests[2].Title != "third" {
		t.Errorf("unexpected titles: %+v", tests)
	}

	if _, hasMain := testrun.Tests("fn testA() {}\nfn main() {}"); !hasMain {
		t.Error("expected the main of the file to be seen")
	}
	// What does not parse has no tests; compiling it says why
	if tests, _ := testrun.Tests("fn testA( {"); len(tests) != 0 {
		t.Errorf("expected no tests in a file that does not parse, got %v", tests)
	}
	if tests, _ := testrun.Tests(""); len(tests) != 0 {
		t.Errorf("expected no tests in an empty file, got %v", tests)
	}
}

func TestMatching(t *testing.T) {
	tests, _ := testrun.Tests("fn testReadsJSON() {}\nfn testWritesText() {}\nfn testAddsUp() {}")
	for filter, want := range map[string]string{
		"":       "testReadsJSON testWritesText testAddsUp",
		"json":   "testReadsJSON",
		"TEXT":   "testWritesText",
		"adds u": "testAddsUp",
		"test":   "testReadsJSON testWritesText testAddsUp",
		"s":      "testReadsJSON testWritesText testAddsUp",
		"zebra":  "",
	} {
		if got := names(testrun.Matching(tests, filter)); got != want {
			t.Errorf("Matching(%q) = %q, want %q", filter, got, want)
		}
	}
}

// Find goes through folders for the files called _test.qi, leaves out
// what is hidden, and takes a file that is named whatever it is called.
func TestFind(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"main.qi", "main_test.qi", "notes.txt",
		"shop/cart_test.qi", "shop/cart.qi", "shop/deep/er/price_test.qi",
		".hidden/secret_test.qi", "shop/.cache/old_test.qi", "shop/._test.qi",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	relative := func(files []string) string {
		var all []string
		for _, file := range files {
			rel, _ := filepath.Rel(dir, file)
			all = append(all, filepath.ToSlash(rel))
		}
		return strings.Join(all, " ")
	}

	files, err := testrun.Find([]string{dir})
	if err != nil || relative(files) != "main_test.qi shop/cart_test.qi shop/deep/er/price_test.qi" {
		t.Errorf("unexpected test files: %s (%v)", relative(files), err)
	}

	// A folder and a file inside it: the file once. A file by its name,
	// test file or not, hidden or not
	files, err = testrun.Find([]string{
		filepath.Join(dir, "shop"), filepath.Join(dir, "shop", "cart_test.qi"),
		filepath.Join(dir, "main.qi"), filepath.Join(dir, ".hidden", "secret_test.qi"),
	})
	if err != nil || relative(files) != ".hidden/secret_test.qi main.qi shop/cart_test.qi shop/deep/er/price_test.qi" {
		t.Errorf("unexpected test files: %s (%v)", relative(files), err)
	}

	// A hidden folder that is named is gone into: it is what was asked
	files, err = testrun.Find([]string{filepath.Join(dir, ".hidden")})
	if err != nil || relative(files) != ".hidden/secret_test.qi" {
		t.Errorf("unexpected test files: %s (%v)", relative(files), err)
	}

	if _, err := testrun.Find([]string{filepath.Join(dir, "nowhere")}); err == nil || !strings.Contains(err.Error(), "no such file or folder") {
		t.Errorf("expected an error for a path that is not there, got %v", err)
	}
	if files, err := testrun.Find(nil); err != nil || len(files) != 0 {
		t.Errorf("expected nothing for no path, got %v (%v)", files, err)
	}
}

// run compiles and runs the program Program makes of a test file, and
// returns what it printed and what its main returned.
func run(t *testing.T, src string, tests []testrun.Test) (string, int) {
	t.Helper()
	errs := errors.NewHandler()
	result := compiler.Compile(testrun.Program(src, "basket_test.qi", tests), "basket_test.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	value, err := machine.Exec(result.Chunk)
	if err != nil {
		t.Fatalf("the tests stopped: %v", err)
	}
	return out.String(), int(value.AsInt())
}

// The program made of a test file runs its tests in the order they are
// written, goes on after one that fails, whatever way it fails, says
// where, and returns how many did. The file uses the language as a
// program does: generics, closures, an enum, a trait, and a module of the
// library under the name the runner uses too.
func TestProgramRunsTheTests(t *testing.T) {
	src := `import std.test;
import std.arrays;
import std.result.{Kind};

enum Coin { Penny, Dime(Int) }

trait Worth { fn worth() -> Int; }

impl Worth for Coin {
    fn worth() -> Int {
        return match self {
            case Coin.Penny: 1;
            case Coin.Dime(count): 10 * count;
        };
    }
}

struct Basket<T: Worth> { items: [T]; }

impl<T: Worth> Basket<T> {
    fn total() -> Int { return self.items.fold(0, (sum, item) -> sum + item.worth()); }
}

fn basket() -> Basket<Coin> {
    return Basket<Coin> { items: [Coin.Penny, Coin.Dime(2), Coin.Penny] };
}

fn testATotalAddsUp() {
    test.equal(basket().total(), 22);
}

fn testAnEmptyBasketCostsNothing() {
    test.equal(Basket<Coin> { items: [] }.total(), 1);
}

fn testTheThirdCoinIsThere() {
    let coins = basket().items;
    test.equal(coins[3], Coin.Penny);
}

fn testAFailureIsExpected() {
    let failure = test.fails(Kind.Bounds, fn() { let coin = basket().items[9]; });
    test.contains(failure.message, "out of bounds");
}

fn testItSaysSo() {
    panic("the \"basket\" is $5 short");
}

fn testTheLastOneStillRuns() {
    test.same(basket().items.map(coin -> coin.worth()), [1, 20, 1]);
}
`
	tests, hasMain := testrun.Tests(src)
	if len(tests) != 6 || hasMain {
		t.Fatalf("expected six tests and no main, got %v", tests)
	}

	out, failed := run(t, src, tests)
	want := `basket_test.qi
  ok    a total adds up
  FAIL  an empty basket costs nothing
        expected 1, got 0
        at basket_test.qi:33
  FAIL  the third coin is there
        index out of bounds: 3 (length 3)
        at basket_test.qi:38
  ok    a failure is expected
  FAIL  it says so
        the "basket" is $5 short
        at basket_test.qi:47
  ok    the last one still runs
3 passed, 3 failed
`
	if out != want || failed != 3 {
		t.Errorf("the tests printed\n%s\nand %d failed; wanted\n%s", out, failed, want)
	}

	// Some of them only, as --filter has it
	out, failed = run(t, src, testrun.Matching(tests, "basket"))
	if !strings.Contains(out, "FAIL  an empty basket costs nothing") || strings.Contains(out, "a total adds up") || failed != 1 {
		t.Errorf("expected the one test that names a basket, got\n%s(%d failed)", out, failed)
	}

	// None at all is a suite that passes
	out, failed = run(t, src, nil)
	if out != "basket_test.qi\n0 passed\n" || failed != 0 {
		t.Errorf("expected an empty suite, got\n%s(%d failed)", out, failed)
	}
}

// What is added to a test file changes nothing in it: its lines keep
// their numbers, a name with quotes in it is still one text, and a file
// that does not end with a line end is not glued to what follows.
func TestProgramLeavesTheFileAsItIs(t *testing.T) {
	src := "fn testA() {}"
	program := testrun.Program(src, `shop "x"/$a_test.qi`, []testrun.Test{{Function: "testA", Title: `says "hi" for $5`}})
	if !strings.HasPrefix(program, src+"\n") {
		t.Errorf("expected the file first, on lines of its own:\n%s", program)
	}
	for _, want := range []string{
		`__test.suite("shop \"x\"/\$a_test.qi")`,
		`__suite.test("says \"hi\" for \$5", testA);`,
		"import std.test as __test;",
	} {
		if !strings.Contains(program, want) {
			t.Errorf("expected %s in:\n%s", want, program)
		}
	}
	errs := errors.NewHandler()
	if compiler.Compile(program, "a_test.qi", errs); errs.HasErrors() {
		t.Errorf("the program has to compile: %v", errs.Diagnostics)
	}
}
