package inspect

import (
	"strings"
	"testing"
)

// control uses a match that gives a value, cases with conditions and
// several patterns, ranges and defer: what an editor is asked about them.
const control = `import std.strings;

struct Point { x: Int; y: Int; }
impl Point { fn sum() -> Int { return self.x + self.y; } }
enum Shape { Circle(Float), At(Point), Dot }

fn describe(shape: Shape, limit: Float) -> String {
    return match shape {
        case Shape.Circle(radius) if radius > limit: "big";
        case Shape.Circle(_) | Shape.Dot: "round";
        case Shape.At(place): "x".repeat(place.sum());
    };
}

fn main() -> Int {
    let origin = Point { x: 0, y: 0 };
    defer println(origin.sum());
    for step in 0..origin.sum() + 3 {
        defer { println(step, origin.x); }
        let word = match step { case 0..2: "few"; default: describe(Shape.Dot, 1.0); };
        println(word);
    }
    return 0;
}
`

func TestControlFlowInTheEditor(t *testing.T) {
	lines := strings.Split(control, "\n")

	// Hover: the names of a pattern in its condition and in a case that is
	// an expression, the variable of a range, what a defer uses
	for _, c := range []struct {
		line      int
		at        string
		signature string
		declared  int
	}{
		{9, "radius >", "radius: Float", 9},
		{9, "limit:", "(parameter) limit: Float", 7},
		{11, "place.sum", "place: Point", 11},
		{11, "sum()", "fn Point.sum() -> Int", 4},
		{10, "Dot", "(variant) Shape.Dot", 5},
		{17, "origin.sum", "let origin: Point", 16},
		{18, "step", "(variable) step: Int", 18},
		{18, "sum()", "fn Point.sum() -> Int", 4},
		{19, "step,", "step: Int", 18},
		{19, "x)", "(field) Point.x: Int", 3},
		{20, "word", "let word: String", 20},
		{20, "step {", "step: Int", 18},
		{20, "describe", "fn describe(shape: Shape, limit: Float) -> String", 7},
		{21, "word", "let word: String", 20},
	} {
		column := strings.Index(lines[c.line-1], c.at) + 1
		d := Describe("main.qi", control, c.line, column)
		if d == nil {
			t.Errorf("%d %q: expected %q, got nothing", c.line, c.at, c.signature)
		} else if d.Signature != c.signature || d.Line != c.declared {
			t.Errorf("%d %q: got %q declared at line %d, want %q at line %d", c.line, c.at, d.Signature, d.Line, c.signature, c.declared)
		}
	}

	// Completion, with the line being typed left unfinished
	complete := func(line int, old, typed string) []Completion {
		src := append([]string{}, lines...)
		src[line-1] = strings.Replace(src[line-1], old, typed, 1)
		column := strings.Index(src[line-1], typed) + len(typed) + 1
		return Complete("main.qi", strings.Join(src, "\n"), line, column)
	}
	labels := func(items []Completion) string {
		var out []string
		for _, c := range items {
			out = append(out, c.Label)
		}
		return strings.Join(out, " ")
	}
	has := func(items []Completion, label, kind, detail string) bool {
		for _, c := range items {
			if c.Label == label && c.Kind == kind && (detail == "" || c.Detail == detail) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		line       int
		old, typed string
		want       string
	}{
		// After a dot, in a case that is an expression: the pattern's name
		{11, "place.sum()", "place.", "x y sum"},
		// In a defer, of one statement and of several
		{17, "origin.sum()", "origin.", "x y sum"},
		{19, "origin.x", "origin.", "x y sum"},
		// In a match that is the value of a let, and after one
		{20, "describe(Shape.Dot, 1.0)", "Shape.", "Circle At Dot"},
		{21, "println(word);", "origin.", "x y sum"},
	} {
		if got := labels(complete(c.line, c.old, c.typed)); got != c.want {
			t.Errorf("line %d after %q: got %q, want %q", c.line, c.typed, got, c.want)
		}
	}

	// Anywhere else: the names of patterns and of loops are in reach, used
	// yet or not, and defer is a word of the language
	inGuard := complete(9, "radius > limit", "radius > li")
	if !has(inGuard, "radius", "variable", "") || !has(inGuard, "limit", "variable", "") {
		t.Errorf("in the condition of a case: %s", labels(inGuard))
	}
	inCase := complete(11, `"x".repeat(place.sum())`, "sha")
	if !has(inCase, "place", "variable", "") {
		t.Errorf("in a case, before its name is used: %s", labels(inCase))
	}
	// ...and after a dot there, before the colon of the case is written
	if got := labels(complete(11, `case Shape.At(place): "x".repeat(place.sum());`, "case Shape.At(place) if place.")); got != "x y sum" {
		t.Errorf("after a dot in a condition being typed: %q", got)
	}
	inLoop := complete(21, "println(word);", "de")
	if !has(inLoop, "step", "variable", "Int") || !has(inLoop, "word", "variable", "String") || !has(inLoop, "defer", "keyword", "") {
		t.Errorf("in the loop over a range: %s", labels(inLoop))
	}

	// Diagnostics: none for the program, and those of the new checks
	if got := errorsOf(Check("main.qi", control)); len(got) != 0 {
		t.Errorf("expected a clean program, got %+v", got)
	}
	for _, c := range []struct{ old, new, code string }{
		{"default: describe(Shape.Dot, 1.0);", "", "E2028"},
		{"defer println(origin.sum());", "defer { return 1; }", "E2039"},
		{"0..origin.sum() + 3", "0..1.5", "E2038"},
		{"case Shape.Circle(_) | Shape.Dot", "case Shape.Circle(r) | Shape.Dot", "E2029"},
	} {
		var errs []Diagnostic
		for _, problem := range Check("main.qi", strings.Replace(control, c.old, c.new, 1)) {
			if problem.Severity == "error" {
				errs = append(errs, problem)
			}
		}
		if len(errs) == 0 || errs[0].Code != c.code {
			t.Errorf("%q: expected %s, got %+v", c.new, c.code, errs)
		}
	}
}
