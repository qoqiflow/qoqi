package types

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// `value is Pattern` is checked as a case of a match on the value would
// be, and is a Bool.
func TestPatternTestsAccepted(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"the names have the types of the values", `fn f(s: Shape) -> Float {
			if s is Shape.Rect(w, h) { let sum: Float = w + h; return sum; }
			if s is Shape.Circle(r) { let twice: Float = r * 2.0; return twice; }
			return 0.0; }`},
		{"a test is a Bool wherever it stands", `fn f(s: Shape) -> Bool {
			let round: Bool = s is Shape.Circle;
			let flat: Bool = !(s is Shape.Circle(_)) && s is Shape.Rect(_, 0.0);
			return round || flat || s is Shape.Empty; }`},
		{"a name is used by the rest of the condition", `fn f(s: Shape) -> Bool {
			if s is Shape.Rect(w, h) && w == h && w > 1.0 { return true; }
			return s is Shape.Circle(r) && r > 1.0; }`},
		{"generic enum: the names take the type arguments of the value", `fn f(r: Result<[Int], String>, o: Option<Point>) -> Int {
			if r is Result.Ok(items) { let first: Int = items[0]; return first; }
			if r is Result.Err(message) && o is Option.Some(p) { let s: String = message; return p.x; }
			return 0; }`},
		{"patterns inside patterns and literals", `fn f(e: Expr) -> Int {
			if e is Expr.Add(Expr.Num(0), Expr.Neg(Expr.Num(n))) { return n; }
			if e is Expr.Num(-1) { return 1; }
			return 0; }`},
		{"an else if chain returns on every path", `fn f(s: Shape) -> Int {
			if s is Shape.Circle(_) { return 1; } else if s is Shape.Rect(w, _) && w > 0.0 { return 2; } else { return 3; } }`},
		{"a while whose condition names", `fn next() -> Option<Int> { return Option.None; }
			fn f() -> Int {
				let total = 0;
				while next() is Option.Some(n) { total += n; }
				while next() is Option.Some(n) && n > 0 { total += n; if total > 9 { break; } }
				return total; }`},
		{"an optional value: null fits no pattern", `fn f(s: Shape?) -> Float {
			if s is Shape.Circle(r) { return r; }
			return 0.0; }`},
		{"an optional value is not null where the test held", `impl Shape { fn area() -> Float { return 1.0; } }
			fn f(s: Shape?) -> Float {
				if s is Shape.Circle(_) { return s.area(); }
				if s is Shape.Rect(w, _) && s.area() > w { return w; }
				return 0.0; }`},
		{"an Any is of the enum where the test held", `impl Color { fn warm() -> Bool { return self is Color.Red; } }
			fn f(v: Any) -> Bool {
				if v is Color.Green { return v.warm(); }
				if v is Shape.Circle(r) { let x: Float = r; return true; }
				return false; }`},
		{"a union and a trait", `impl Named for Color { fn name() -> String { return "c"; } }
			fn f(u: Shape | Int, n: Named) -> Bool {
				return u is Shape.Empty || n is Color.Red; }`},
		{"what the condition says about a name holds in the block", `enum Link { To(Point?), End }
			fn f(l: Link) -> Int {
				if l is Link.To(p) && p != null { return p.x; }
				return 0; }`},
		{"a name can be assigned and captured", `fn f(s: Shape) -> Float {
			if s is Shape.Circle(r) {
				r = r + 1.0;
				let get = fn() -> Float { return r; };
				return get();
			}
			return 0.0; }`},
		{"self in a method of the enum", `impl Shape {
				fn radius() -> Float { if self is Shape.Circle(r) { return r; } return 0.0; }
			}
			fn main() -> Int { return 0; }`},
		{"in a generic function", `fn orElse<T>(o: Option<T>, fallback: T) -> T {
				if o is Option.Some(v) { return v; }
				return fallback;
			}
			fn main() -> Int { let n: Int = orElse(Option.Some(1), 0); let s: String = orElse(Option<String>.None, "x"); return n; }`},
		{"in the condition of a case", `fn f(s: Shape, o: Option<Float>) -> Int {
			match s {
				case Shape.Circle(r) if o is Option.Some(limit) && r > limit: { return 1; }
				default: { return 0; }
			} }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := enumDecls + tt.src
			if !strings.Contains(tt.src, "fn main") {
				src += "\nfn main() -> Int { return 0; }"
			}
			if msgs, _, _ := checkSource(t, src); len(msgs) != 0 {
				t.Errorf("expected no diagnostics, got %v", msgs)
			}
		})
	}
}

func TestPatternTestsRejected(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{"unknown variant", `fn f(s: Shape) -> Bool { return s is Shape.Square; }`, ErrInvalidPattern, "enum Shape has no variant 'Square'"},
		{"too few values", `fn f(s: Shape) -> Bool { if s is Shape.Rect(w) { return true; } return false; }`, ErrInvalidPattern, "holds 2 values, the pattern has 1"},
		{"values for a variant without any", `fn f(s: Shape) -> Bool { return s is Shape.Empty(_); }`, ErrInvalidPattern, "holds no values, the pattern has 1"},
		{"the variant of another enum", `fn f(s: Shape) -> Bool { return s is Color.Red; }`, ErrInvalidPattern, "a value of type Shape is never a Color.Red"},
		{"a value that is no enum", `fn f(n: Int) -> Bool { return n is Shape.Empty; }`, ErrInvalidPattern, "a value of type Int is never a Shape.Empty"},
		{"a literal of the wrong type", `fn f(s: Shape) -> Bool { return s is Shape.Circle("wide"); }`, ErrInvalidPattern, "a value of type Float is never equal to a String"},
		{"a pattern inside of the wrong enum", `fn f(e: Expr) -> Bool { return e is Expr.Neg(Shape.Empty); }`, ErrInvalidPattern, "a value of type Expr is never a Shape.Empty"},
		{"not an enum", `fn f(p: Point) -> Bool { return p is Point.x(_); }`, ErrInvalidPattern, "is not a variant of an enum"},
		{"a name used as the wrong type", `fn f(s: Shape) -> Int { if s is Shape.Circle(r) { let n: Int = r; return n; } return 0; }`, ErrTypeMismatch, "cannot assign type Float to Int"},
		{"a name of the wrong type in the condition", `fn f(s: Shape) -> Bool { return s is Shape.Circle(r) && r == "wide"; }`, ErrInvalidOperation, "operator == not defined"},
		{"an optional name used without a test", `enum Link { To(Point?), End }
			fn f(l: Link) -> Int { if l is Link.To(p) { return p.x; } return 0; }`, ErrPossiblyNull, "cannot read 'x' of Point?"},
		{"the test as something else than a Bool", `fn f(s: Shape) -> Int { let n: Int = s is Shape.Empty; return n; }`, ErrTypeMismatch, "cannot assign type Bool to Int"},
		{"an else that does not return", `fn f(s: Shape) -> Int { if s is Shape.Circle(_) { return 1; } }`, ErrMissingReturn, ""},
		{"nothing known in the else", `impl Shape { fn area() -> Float { return 1.0; } }
			fn f(s: Shape?) -> Float { if s is Shape.Circle(_) { return 1.0; } else { return s.area(); } }`, ErrPossiblyNull, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnostics := checkSourceDiagnostics(t, enumDecls+tt.src+"\nfn main() -> Int { return 0; }")
			var found *errors.Diagnostic
			for i, d := range diagnostics {
				if d.Severity == errors.SeverityError {
					found = &diagnostics[i]
					break
				}
			}
			if found == nil {
				t.Fatal("expected a diagnostic, got none")
			}
			if found.Code != tt.code || !strings.Contains(found.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, found.Code, found.Message)
			}
		})
	}
}

// A pattern every value fits asks nothing: the program compiles, with a
// warning where the pattern is.
func TestPatternTestThatAlwaysHoldsIsAWarning(t *testing.T) {
	src := "enum Wrap { Of(Int, String) }\n" +
		"enum Shape { Circle(Float), Empty }\n" +
		"fn f(w: Wrap, s: Shape, o: Wrap?) -> Int {\n" +
		"    if w is Wrap.Of(n, _) { return n; }\n" +
		"    if w is Wrap.Of { return 1; }\n" +
		"    if w is Wrap.Of(0, _) { return 2; }\n" +
		"    if s is Shape.Circle(_) { return 3; }\n" +
		"    if o is Wrap.Of(n, _) { return n; }\n" +
		"    return 0;\n" +
		"}\n" +
		"fn main() -> Int { return 0; }\n"

	var lines []int
	for _, d := range checkSourceDiagnostics(t, src) {
		if d.Severity != errors.SeverityWarning {
			t.Errorf("unexpected error: %s: %s", d.Code, d.Message)
			continue
		}
		if d.Code != ErrUnreachableCode || !strings.Contains(d.Message, "this test always holds: every value of type Wrap fits the pattern") {
			t.Errorf("unexpected warning: %s: %s", d.Code, d.Message)
		}
		lines = append(lines, d.Pos.Line)
	}
	// Not the pattern with a literal, the enum with another variant, or
	// the value that may be null
	if len(lines) != 2 || lines[0] != 4 || lines[1] != 5 {
		t.Errorf("expected warnings on lines 4 and 5, got %v", lines)
	}
}

// What the rest of a condition established about a name of a pattern is
// kept for desugaring, which declares the name again for the block.
func TestNarrowedPatternNamesAreRecorded(t *testing.T) {
	_, info, prog := checkSource(t, `
		struct Point { x: Int; y: Int; }
		enum Link { To(Point?, Any, Int), End }
		fn main() -> Int {
			let l = Link.End;
			if l is Link.To(p, v, n) && p != null && v is String && n > 0 { return p.x; }
			while l is Link.To(q, _, _) && q != null { return q.y; }
			return 0;
		}
	`)

	recorded := make(map[string]string)
	for binding, typ := range info.NarrowedNames {
		recorded[binding.Name] = describe(typ)
	}
	want := map[string]string{"p": "Point", "v": "String", "q": "Point"}
	for name, typ := range want {
		if recorded[name] != typ {
			t.Errorf("expected %s to be recorded as %s, got %q", name, typ, recorded[name])
		}
	}
	if _, ok := recorded["n"]; ok {
		t.Errorf("nothing was established about n, got %s", recorded["n"])
	}

	// The test itself is a Bool
	var tests int
	for _, decl := range prog.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok || fn.Name != "main" {
			continue
		}
		WalkExprs(fn.Body, func(expr ast.Expr) {
			if test, ok := expr.(*ast.PatternTestExpr); ok {
				tests++
				if got := info.Types[test]; got == nil || !got.Equals(Bool) {
					t.Errorf("expected %s to be a Bool, got %v", test, got)
				}
			}
		})
	}
	if tests != 2 {
		t.Errorf("expected 2 pattern tests, got %d", tests)
	}
}
