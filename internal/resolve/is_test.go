package resolve

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
)

const isDecls = `
	enum Shape { Circle(Float), Rect(Float, Float), Empty }
	enum Option<T> { Some(T), None }
	fn next() -> Option<Shape> { return Option.None; }
`

// isBody wraps statements in a function that has a shape `s` and a
// boolean `ready` to test.
func isBody(body string) string {
	return isDecls + "fn main() -> Void { let s = Shape.Empty; let ready = true;\n" + body + "\n}"
}

// The names of `value is Pattern` can be used in what only runs when the
// test held: the rest of a chain of `&&`, and the block of the `if` or
// `while` the test is the condition of.
func TestResolvePatternTestNames(t *testing.T) {
	accepted := []struct {
		name string
		body string
	}{
		{"the block of an if", `if s is Shape.Circle(r) { println(r); }`},
		{"the rest of the condition", `if s is Shape.Rect(w, h) && w == h { println(w, h); }`},
		{"a test after another, using its names", `if next() is Option.Some(inner) && inner is Shape.Circle(r) { println(inner, r); }`},
		{"each branch of a chain has its own", `if s is Shape.Circle(r) { println(r); } else if s is Shape.Rect(r, _) { println(r); } else if s is Shape.Empty { }`},
		{"the body of a while", `while next() is Option.Some(found) { println(found); }`},
		{"a while with more to its condition", `let n = 0; while next() is Option.Some(found) && n < 3 { n += 1; println(found); }`},
		{"a chain that is a value", `let big = s is Shape.Circle(r) && r > 1.0; println(big);`},
		{"a chain inside another condition", `if ready || (s is Shape.Circle(r) && r > 1.0) { }`},
		{"a chain under a not", `if !(s is Shape.Circle(r) && r > 1.0) { }`},
		{"a chain in the condition of a case", `match s { case Shape.Rect(w, _) if next() is Option.Some(o) && o == s: { println(w); } default: { } }`},
		{"no names, anywhere", `let a = s is Shape.Empty; let b = !(s is Shape.Circle(_)) || s is Shape.Rect(_, _); println(a, b);`},
		{"a name of the block may be declared again inside it", `if s is Shape.Circle(r) { let r = r * 2.0; println(r); }`},
		{"the same name after the if", `if s is Shape.Circle(r) { println(r); } let r = 1; println(r);`},
		{"the name of the value itself", `if s is Shape.Circle(s) { println(s); }`},
		{"a closure in the block", `if s is Shape.Circle(r) { let f = fn() -> Float { return r; }; println(f()); }`},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			if msgs := resolveSource(t, isBody(tt.body)); len(msgs) != 0 {
				t.Errorf("expected no diagnostics, got %v", msgs)
			}
		})
	}

	rejected := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"a name in the else", `if s is Shape.Circle(r) { } else { println(r); }`, ErrUndefined, "undefined identifier 'r'"},
		{"a name after the if", `if s is Shape.Circle(r) { } println(r);`, ErrUndefined, "undefined identifier 'r'"},
		{"a name after the while", `while next() is Option.Some(found) { } println(found);`, ErrUndefined, "undefined identifier 'found'"},
		{"a name before its test", `if r > 1.0 && s is Shape.Circle(r) { }`, ErrUndefined, "undefined identifier 'r'"},
		{"a name after the chain it is of", `let big = s is Shape.Circle(r) && r > 1.0; println(r);`, ErrUndefined, "undefined identifier 'r'"},
		{"a name in the next branch of a chain", `if s is Shape.Circle(r) { } else if r > 1.0 { }`, ErrUndefined, "undefined identifier 'r'"},
		{"a name under a not, in a block that may not leave", `if !(s is Shape.Circle(r)) { println(1); }`, ErrUnusableName, "'r' cannot be named here"},
		{"a name on a side of an or", `if s is Shape.Empty || s is Shape.Circle(r) { }`, ErrUnusableName, "'r' cannot be named here"},
		{"a name on the left of an or", `if s is Shape.Circle(r) || ready { }`, ErrUnusableName, "'r' cannot be named here"},
		{"a name in a test that is a value", `let round = s is Shape.Circle(r);`, ErrUnusableName, "'r' cannot be named here"},
		{"a name in a test that is returned", `return s is Shape.Rect(_, h);`, ErrUnusableName, "'h' cannot be named here"},
		{"a name in an argument", `println(s is Shape.Circle(r));`, ErrUnusableName, "'r' cannot be named here"},
		{"a name at the end of a chain that is a value", `let both = ready && s is Shape.Circle(r);`, ErrUnusableName, "'r' cannot be named here"},
		{"a name in the condition of a case", `match s { case Shape.Rect(w, _) if next() is Option.Some(o): { } default: { } }`, ErrUnusableName, "'o' cannot be named here"},
		{"a name inside a pattern inside the test", `let found = next() is Option.Some(Shape.Circle(r));`, ErrUnusableName, "'r' cannot be named here"},
		{"a name given twice by one pattern", `if s is Shape.Rect(side, side) { }`, ErrRedeclaration, "'side' is already declared"},
		{"a name given by two tests of a condition", `if s is Shape.Circle(r) && next() is Option.Some(r) { }`, ErrRedeclaration, "'r' is already declared"},
		{"an enum that does not exist", `if s is Shapes.Circle(r) { }`, ErrUndefined, "undefined identifier 'Shapes'"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			prog := parser.Parse(isBody(tt.body), "test.qi", errs)
			if errs.HasErrors() {
				t.Fatalf("test source does not parse: %s", errs.Diagnostics[0].Message)
			}
			NewResolver(errs).Resolve(prog)
			if len(errs.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
		})
	}
}

// Every name that cannot be used is reported, where it is written, with
// what to write instead.
func TestResolveUnusableNamesAreEachReported(t *testing.T) {
	src := "enum Shape { Circle(Float), Rect(Float, Float), Empty }\n" +
		"fn wide(s: Shape) -> Bool {\n" +
		"    return s is Shape.Rect(w, h);\n" +
		"}\n"
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	NewResolver(errs).Resolve(prog)

	if len(errs.Diagnostics) != 2 {
		t.Fatalf("expected a diagnostic for each of the two names, got %d", len(errs.Diagnostics))
	}
	for i, want := range []struct {
		name string
		col  int
	}{{"'w'", 28}, {"'h'", 31}} {
		d := errs.Diagnostics[i]
		if d.Code != ErrUnusableName || !strings.Contains(d.Message, want.name) {
			t.Errorf("unexpected diagnostic: %s: %s", d.Code, d.Message)
		}
		if d.Pos.Line != 3 || d.Pos.Col != want.col {
			t.Errorf("expected %s at 3:%d, got %d:%d", want.name, want.col, d.Pos.Line, d.Pos.Col)
		}
		if !strings.Contains(d.Suggestion, "Write '_' to test without naming") {
			t.Errorf("expected the suggestion to say what to write, got %q", d.Suggestion)
		}
	}
}
