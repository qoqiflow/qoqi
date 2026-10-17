package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

// diagnose compiles a program that must be rejected and returns what is
// said of it.
func diagnose(t *testing.T, src string) []errors.Diagnostic {
	t.Helper()
	errs := errors.NewHandler()
	Compile(src, "test.qi", errs)
	if !errs.HasErrors() {
		t.Fatalf("expected a diagnostic, got none for:\n%s", src)
	}
	var said []errors.Diagnostic
	for _, d := range errs.Diagnostics {
		if d.Severity == errors.SeverityError {
			said = append(said, d)
		}
	}
	return said
}

// What a mistake is told with: the types as the program writes them, the
// name it most likely meant, whose method it asked for, and the mistake
// alone, without what follows from it.
func TestMistakesAreToldPlainly(t *testing.T) {
	tests := []struct {
		name, src, code, msg, hint string
	}{
		{"types are written as in the program",
			`fn main() -> Int { let names: [String] = ["a"]; let n: Int? = 1; let x: Int = names; return n; }`,
			"E2001", "cannot assign type [String] to Int", "Ensure the value is of type Int"},
		{"an optional, a function and a tuple",
			`fn f(g: fn(Float) -> Bool, p: (Int, String)) -> Int { let s: String? = null; return s.len(); }`,
			"E2017", "cannot call 'len' on String?", ""},
		{"a field with two letters swapped",
			`struct User { name: String; age: Int; } fn main() -> Int { let u = User { name: "a", age: 1 }; println(u.nmae); return 0; }`,
			"E2014", "User has no field 'nmae'", "Did you mean 'name'?"},
		{"a method with two letters swapped",
			`struct Cart { items: [Int]; } impl Cart { fn total() -> Int { return 0; } } fn main() -> Int { return (Cart { items: [] }).ttoal(); }`,
			"E2015", "has no method 'ttoal'", "Did you mean 'total'?"},
		{"a method only the arrays of numbers have",
			`fn main() -> Int { let words = ["a", "b"]; println(words.sum()); return 0; }`,
			"E2015", "[String] has no method 'sum'", "Only [Float] and [Int] have it"},
		{"a method only the arrays of texts have",
			`fn main() -> Int { println([1, 2].sortedTexts()); return 0; }`,
			"E2015", "[Int] has no method 'sortedTexts'", "Only [String] has it"},
		{"items with no order of their own",
			`struct Job { hours: Int; } fn main() -> Int { println([Job { hours: 1 }].sorted()); return 0; }`,
			"E2009", "only numbers and texts have an order of their own", "ops.sorted(items) when Job is Ordered, items.sortedBy("},
		{"a condition that is not a Bool",
			`fn main() -> Int { if 1.5 { return 1; } return 0; }`,
			"E2005", "condition must be Bool, got Float", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := diagnose(t, tt.src)[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) || !strings.Contains(d.Suggestion, tt.hint) {
				t.Errorf("expected %s %q with %q, got %s %q with %q", tt.code, tt.msg, tt.hint, d.Code, d.Message, d.Suggestion)
			}
		})
	}
}

// A function written with -> given to a method that does not exist is not
// told apart: the method is what is wrong, and nothing could say what the
// parameters of the function are.
func TestAMissingMethodIsTheOneMistake(t *testing.T) {
	for _, src := range []string{
		`fn main() -> Int { let xs = ["a"]; println(xs.firstWhere(x -> x == "a")); return 0; }`,
		`fn main() -> Int { let xs = [1, 2]; println(xs.foldLeft(0, (a, b) -> a + b)); return 0; }`,
		`struct Bag { items: [Int]; } fn main() -> Int { let b = Bag { items: [] }; b.each(n -> println(n)); return 0; }`,
	} {
		said := diagnose(t, src)
		if len(said) != 1 || said[0].Code != "E2015" {
			t.Errorf("expected only E2015 for %s, got %v", src, said)
		}
	}

	// The function is still checked where the call is one
	said := diagnose(t, `fn main() -> Int { let xs = [1]; println(xs.map(x -> x + "a")); return 0; }`)
	if len(said) != 1 || said[0].Code != "E2006" {
		t.Errorf("expected the body of the function to be checked, got %v", said)
	}
}

// A function asked of a module of the library that does not have it, when
// another module does, is told where it is.
func TestAFunctionOfAnotherModuleOfTheLibrary(t *testing.T) {
	expectModulesError(t, sources{
		"main.qi": "import std.task;\nfn main() -> Int {\n    wait task.after(5);\n    return 0;\n}\n",
	}, "E8002", "main.qi", "module 'std.task' has no 'after'")

	_, errs := compileModules(sources{
		"main.qi": "import std.task;\nimport std.strings;\nfn main() -> Int {\n    wait task.after(5);\n    let s = strings.parseJson(\"1\");\n    return 0;\n}\n",
	})
	if len(errs.Diagnostics) < 2 {
		t.Fatalf("expected two diagnostics, got %v", errs.Diagnostics)
	}
	if hint := errs.Diagnostics[0].Suggestion; hint != "'after' is in std.time: import it and call time.after(...)" {
		t.Errorf("expected the module that has after, got %q", hint)
	}
	// parse is a function of json, csv and time, parseJson of none
	if hint := errs.Diagnostics[1].Suggestion; strings.Contains(hint, "is in std.") {
		t.Errorf("expected no module for a name none has, got %q", hint)
	}

	// The program's own modules are not the library: nothing is guessed
	_, errs = compileModules(sources{
		"main.qi": "import util;\nfn main() -> Int {\n    util.sleep(5);\n    return 0;\n}\n",
		"util.qi": "pub fn nap(ms: Int) { }\n",
	})
	if len(errs.Diagnostics) == 0 || strings.Contains(errs.Diagnostics[0].Suggestion, "std.time") {
		t.Errorf("expected no module of the library for a module of the program, got %v", errs.Diagnostics)
	}
}

// A length of time is a Duration: a number of milliseconds, as std.time
// once took, is told what to write instead, wherever it is given.
func TestLengthsOfTimeAreDurations(t *testing.T) {
	for _, c := range []struct {
		name, main, code, hint string
	}{
		{"a unit that was a number", "import std.time;\nfn main() -> Int {\n    let d = 2 * time.day;\n    return 0;\n}\n",
			"E8002", "A length of time is a Duration, written as what it is: 2.days(), as in wait time.after(2.days())"},
		{"a number to wait for", "import std.time;\nfn main() -> Int {\n    wait time.after(500);\n    return 0;\n}\n",
			"E2010", "A number alone is not a length of time: write what it is, 500.milliseconds(), 30.seconds(), 2.days()"},
		{"a number for a field", "import std.time;\nimport std.time.{Duration};\nstruct Expense {\n    patience: Duration;\n}\nfn main() -> Int {\n    let e = Expense { patience: 3000 };\n    return 0;\n}\n",
			"E2001", "A number alone is not a length of time: write what it is, 500.milliseconds(), 30.seconds(), 2.days()"},
		{"a unit without std.time", "fn main() -> Int {\n    let d = 2.days();\n    return 0;\n}\n",
			"E2015", "'days' is a method std.time gives numbers, for a Duration: import std.time"},
	} {
		_, errs := compileModules(sources{"main.qi": c.main})
		var found bool
		for _, d := range errs.Diagnostics {
			if d.Code == c.code && d.Suggestion == c.hint {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected %s saying %q, got %+v", c.name, c.code, c.hint, errs.Diagnostics)
		}
	}

	// Its methods are what std.time is imported for: no warning that it
	// is not used
	_, errs := compileModules(sources{"main.qi": "import std.time;\nfn main() -> Int {\n    println(90.minutes());\n    return 0;\n}\n"})
	for _, d := range errs.Diagnostics {
		t.Errorf("expected nothing said, got %s: %s", d.Code, d.Message)
	}
}
