package types

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// Declarations most of the tests below use.
const enumDecls = `
	enum Shape { Circle(Float), Rect(Float, Float), Empty }
	enum Color { Red, Green, Blue }
	enum Option<T> { Some(T), None }
	enum Result<T, E> { Ok(T), Err(E) }
	enum Expr { Num(Int), Add(Expr, Expr), Neg(Expr) }
	enum List<T> { Cons(T, List<T>), Nil }
	struct Point { x: Int; y: Int; }
	trait Named { fn name() -> String; }
`

func TestEnumsAccepted(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"variants with and without values", `fn main() -> Int {
			let a = Shape.Circle(1.0); let b = Shape.Rect(1.0, 2.0); let c = Shape.Empty;
			let all: [Shape] = [a, b, c, Shape.Empty];
			return __len(all); }`},
		{"enum as parameter, result and field", `struct Tile { color: Color; shape: Shape?; }
			fn paint(c: Color) -> Tile { return Tile { color: c, shape: null }; }
			fn main() -> Int { let t = paint(Color.Red); t.color = Color.Blue; t.shape = Shape.Empty; return 0; }`},
		{"exhaustive match returns on every path", `fn area(s: Shape) -> Float {
			match s {
				case Shape.Circle(r): { return 3.0 * r * r; }
				case Shape.Rect(w, h): { return w * h; }
				case Shape.Empty: { return 0.0; }
			} }`},
		{"default completes a match", `fn isRound(s: Shape) -> Bool {
			match s { case Shape.Circle(_): { return true; } default: { return false; } } }`},
		{"variant matched without naming its values", `fn kind(s: Shape) -> Int {
			match s { case Shape.Circle: { return 1; } case Shape.Rect: { return 2; } case Shape.Empty: { return 3; } } }`},
		{"values of a pattern are typed", `fn f(s: Shape) -> Float {
			match s {
				case Shape.Rect(w, h): { let sum: Float = w + h; return sum; }
				case Shape.Circle(r): { let twice: Float = r * 2.0; return twice; }
				default: { return 0.0; }
			} }`},
		{"literals and nested patterns", `fn eval(e: Expr) -> Int {
			match e {
				case Expr.Num(0): { return 0; }
				case Expr.Num(-1): { return 1; }
				case Expr.Num(n): { return n; }
				case Expr.Add(Expr.Num(0), right): { return eval(right); }
				case Expr.Add(left, Expr.Neg(Expr.Num(n))): { return eval(left) - n; }
				case Expr.Add(left, right): { return eval(left) + eval(right); }
				case Expr.Neg(inner): { return 0 - eval(inner); }
			} }`},
		{"partial patterns need the full one after them", `fn f(s: Shape) -> Int {
			match s {
				case Shape.Circle(0.0): { return 0; }
				case Shape.Circle(r): { return 1; }
				case Shape.Rect(_, 0.0): { return 2; }
				case Shape.Rect(w, h): { return 3; }
				case Shape.Empty: { return 4; }
			} }`},
		{"generic enum: arguments from the values", `fn main() -> Int {
			let a = Option.Some(1); let b: Option<Int> = a;
			let d = Option.Some([1, 2]); let e: Option<[Int]> = d;
			let f = Option.Some(Option.Some("x")); let g: Option<Option<String>> = f;
			return 0; }`},
		{"generic enum: arguments from the expected type", `fn none() -> Option<Int> { return Option.None; }
			fn take(o: Option<String>) -> Int { return 0; }
			struct Slot { item: Option<Point>; }
			fn main() -> Int {
				let a: Option<Int> = Option.None;
				let b: Result<Int, String> = Result.Ok(1);
				let c: Result<Int, String> = Result.Err("no");
				let s = Slot { item: Option.None };
				s.item = Option.None;
				let xs: [Option<Int>] = [Option.None, Option.Some(1)];
				return take(Option.None) + take(Option.Some("x")); }`},
		{"generic enum: arguments written", `fn main() -> Int {
			let a = Option<Int>.None; let b: Option<Int> = a;
			let c = Result<Int, String>.Ok(1); let d = Option<[Point]>.Some([]);
			return 0; }`},
		{"generic enum: one side of a comparison tells the other", `fn f(o: Option<Int>) -> Bool {
			return o == Option.None || Option.None != o || o == Option.Some(1); }`},
		{"recursive generic enum", `fn length<T>(l: List<T>) -> Int {
			match l { case List.Cons(_, rest): { return 1 + length<T>(rest); } case List.Nil: { return 0; } } }
			fn main() -> Int { let l = List.Cons(1, List.Cons(2, List.Nil)); return length(l); }`},
		{"values of a generic pattern take the type arguments", `fn first(r: Result<[Int], String>) -> Int {
			match r {
				case Result.Ok(items): { return items[0] + __len(items); }
				case Result.Err(message): { return __len(message); }
			} }`},
		{"generic function over a generic enum", `fn unwrapOr<T>(o: Option<T>, fallback: T) -> T {
			match o { case Option.Some(v): { return v; } case Option.None: { return fallback; } } }
			fn main() -> Int { let n: Int = unwrapOr(Option.Some(1), 0); let s: String = unwrapOr(Option<String>.None, "x"); return n; }`},
		{"methods and traits on an enum", `impl Shape {
				fn area() -> Float { match self { case Shape.Circle(r): { return r; } default: { return 0.0; } } }
				fn scaled(by: Float) -> Shape { return self; }
			}
			impl Named for Shape { fn name() -> String { return "shape"; } }
			fn main() -> Int {
				let s = Shape.Circle(1.0);
				let n: Named = s;
				let a: Float = s.scaled(2.0).area();
				return __len(n.name()); }`},
		{"methods on a generic enum", `impl<T> Option<T> {
				fn map<U>(f: fn(T) -> U) -> Option<U> {
					match self { case Option.Some(v): { return Option.Some(f(v)); } case Option.None: { return Option.None; } } }
				fn isSome() -> Bool { return self != Option.None; }
				fn orElse(fallback: T) -> T { match self { case Option.Some(v): { return v; } case Option.None: { return fallback; } } }
			}
			fn main() -> Int {
				let n: Int = Option.Some("ab").map(fn(s: String) -> Int { return __len(s); }).orElse(0);
				let o: Option<String> = Option.Some(1).map(toString);
				return n; }`},
		{"a variant with values is a function", `fn apply(f: fn(Float) -> Shape, x: Float) -> Shape { return f(x); }
			fn main() -> Int {
				let make = Shape.Circle;
				let s: Shape = make(1.0);
				let t = apply(Shape.Circle, 2.0);
				let wrap: fn(Int) -> Option<Int> = Option.Some;
				let pair = Shape.Rect;
				let r: Shape = pair(1.0, 2.0);
				return 0; }`},
		{"match on Any and on a union", `fn f(v: Any, u: Shape | Int) -> Int {
			match v { case Shape.Circle(r): { let x: Float = r; return 1; } case Color.Red: { return 2; } default: { } }
			match u { case Shape.Empty: { return 3; } default: { return 4; } } }`},
		{"enum type in a type test and a cast", `fn f(v: Any) -> Int {
			if v is Shape { return 1; }
			let c = v as Color;
			match v { case Shape: { return 2; } case Option<Int>: { return 3; } default: { return 0; } } }`},
		{"match on an optional enum after a null test", `fn f(s: Shape?) -> Int {
			if s == null { return 0; }
			match s { case Shape.Circle(_): { return 1; } case Shape.Rect(_, _): { return 2; } case Shape.Empty: { return 3; } } }`},
		{"names of a pattern are variables of the case only", `fn f(s: Shape, r: Int) -> Int {
			match s {
				case Shape.Circle(r): { let x: Float = r; }
				case Shape.Rect(r, w): { r = w; let y: Float = r; }
				default: { }
			}
			return r + 1; }`},
		{"a variant named like a builtin type", `enum Json { Null, Bool(Bool), String(String), Int(Int) }
			fn f(j: Json) -> Int {
				match j { case Json.Bool(b): { if b { return 1; } return 0; } case Json.String(s): { return __len(s); } case Json.Int(n): { return n; } case Json.Null: { return 0; } } }
			fn main() -> Int { return f(Json.Bool(true)) + f(Json.String("x")) + f(Json.Int(1)) + f(Json.Null); }`},
		{"match that changes what it matches", `fn drain(l: List<Int>) -> Int {
			let total = 0; let rest = l;
			while true {
				match rest { case List.Cons(head, tail): { total += head; rest = tail; } case List.Nil: { break; } }
			}
			return total; }`},
		{"equality between values of an enum", `fn f(a: Shape, b: Shape, c: Color) -> Bool {
			return a == b && a != Shape.Empty && c == Color.Red && Shape.Circle(1.0) == a; }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msgs, _, _ := checkSource(t, enumDecls+tt.src); len(msgs) != 0 {
				t.Errorf("expected no errors, got %v", msgs)
			}
		})
	}
}

func TestEnumsRejected(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// Making values
		{"unknown variant", `fn main() -> Int { let s = Shape.Square(1.0); return 0; }`, "enum Shape has no variant 'Square'"},
		{"misspelled variant", `fn main() -> Int { let s = Shape.Circl(1.0); return 0; }`, "enum Shape has no variant 'Circl'"},
		{"wrong type of value", `fn main() -> Int { let s = Shape.Circle("wide"); return 0; }`, "cannot use type String as value 1 of Shape.Circle, which is of type Float"},
		{"wrong type of second value", `fn main() -> Int { let s = Shape.Rect(1.0, "x"); return 0; }`, "cannot use type String as value 2 of Shape.Rect"},
		{"too many values", `fn main() -> Int { let s = Shape.Circle(1.0, 2.0); return 0; }`, "Shape.Circle holds 1 value, got 2"},
		{"too few values", `fn main() -> Int { let s = Shape.Rect(1.0); return 0; }`, "Shape.Rect holds 2 values, got 1"},
		{"values for a variant without any", `fn main() -> Int { let s = Shape.Empty(); return 0; }`, "Shape.Empty holds no values"},
		{"enum used as a value", `fn main() -> Int { let s = Shape; return 0; }`, "'Shape' is an enum, not a value"},
		{"enum built like a struct", `fn main() -> Int { let s = Shape { r: 1.0 }; return 0; }`, "'Shape' is an enum, not a struct"},
		{"field of an enum value", `fn f(s: Shape) -> Float { return s.radius; }`, "a value of enum Shape has no fields"},
		{"one enum for another", `fn main() -> Int { let s: Shape = Color.Red; return 0; }`, "cannot assign type Color to Shape"},
		{"enum for a primitive", `fn main() -> Int { let n: Int = Color.Red; return 0; }`, "cannot assign type Color to Int"},
		{"variant function with the wrong signature", `fn main() -> Int { let f: fn(Int) -> Shape = Shape.Circle; return 0; }`, "cannot assign type fn(Float) -> Shape"},
		{"variant function called with the wrong type", `fn main() -> Int { let f = Shape.Circle; let s = f("x"); return 0; }`, "cannot use type String as argument of type Float"},
		{"comparison of different enums", `fn f(s: Shape, c: Color) -> Bool { return s == c; }`, "operator == not defined for types Shape and Color"},
		{"arithmetic on an enum", `fn f(c: Color) -> Int { return c + 1; }`, "Color"},

		// Generic enums
		{"type argument nothing tells", `fn main() -> Int { let o = Option.None; return 0; }`, "nothing here says what T of Option is"},
		{"second type argument nothing tells", `fn main() -> Int { let r = Result.Ok(1); return 0; }`, "nothing here says what E of Result is"},
		{"wrong number of type arguments", `fn main() -> Int { let o = Option<Int, String>.None; return 0; }`, "enum Option takes 1 type arguments, got 2"},
		{"value against the written type argument", `fn main() -> Int { let o = Option<Int>.Some("x"); return 0; }`, "cannot use type String as value 1 of Option.Some, which is of type Int"},
		{"value against the expected type argument", `fn main() -> Int { let o: Option<Int> = Option.Some("x"); return 0; }`, "Option<String>"},
		{"different type arguments", `fn main() -> Int { let a: Option<Int> = Option<String>.None; return 0; }`, "cannot assign type Option<String> to Option<Int>"},
		{"values that disagree on the type argument", `enum Pair<T> { Of(T, T) }
			fn main() -> Int { let p = Pair.Of(1, "x"); return 0; }`, "cannot use type String as value 2 of Pair.Of, which is of type Int"},
		{"value of a generic pattern used as another type", `fn f(o: Option<String>) -> Int { match o { case Option.Some(v): { return v + 1; } case Option.None: { return 0; } } }`, "String"},

		// Patterns
		{"variant left out", `fn f(s: Shape) -> Int { match s { case Shape.Circle(r): { return 1; } case Shape.Empty: { return 2; } } }`, "match on Shape does not handle Shape.Rect"},
		{"several variants left out", `fn f(c: Color) -> Int { match c { case Color.Green: { return 1; } } }`, "match on Color does not handle Color.Blue, Color.Red"},
		{"only partial patterns for a variant", `fn f(s: Shape) -> Int { match s { case Shape.Circle(0.0): { return 1; } case Shape.Rect(_, _): { return 2; } case Shape.Empty: { return 3; } } }`, "match on Shape does not handle Shape.Circle"},
		{"only nested patterns for a variant", `fn f(e: Expr) -> Int { match e { case Expr.Num(_): { return 1; } case Expr.Add(_, _): { return 2; } case Expr.Neg(Expr.Num(n)): { return n; } } }`, "match on Expr does not handle Expr.Neg"},
		{"match without cases", `fn f(c: Color) -> Int { match c { } return 0; }`, "match on Color does not handle Color.Blue, Color.Green, Color.Red"},
		{"unknown variant in a pattern", `fn f(s: Shape) -> Int { match s { case Shape.Square(x): { return 1; } default: { return 0; } } }`, "enum Shape has no variant 'Square'"},
		{"too few names in a pattern", `fn f(s: Shape) -> Int { match s { case Shape.Rect(w): { return 1; } default: { return 0; } } }`, "Shape.Rect(Float, Float) holds 2 values, the pattern has 1"},
		{"too many names in a pattern", `fn f(s: Shape) -> Int { match s { case Shape.Circle(a, b): { return 1; } default: { return 0; } } }`, "Shape.Circle(Float) holds 1 value, the pattern has 2"},
		{"names for a variant without values", `fn f(s: Shape) -> Int { match s { case Shape.Empty(x): { return 1; } default: { return 0; } } }`, "Shape.Empty holds no values, the pattern has 1"},
		{"pattern of another enum", `fn f(s: Shape) -> Int { match s { case Color.Red: { return 1; } default: { return 0; } } }`, "a value of type Shape is never a Color.Red"},
		{"pattern on a primitive", `fn f(n: Int) -> Int { match n { case Shape.Empty: { return 1; } default: { return 0; } } }`, "a value of type Int is never a Shape.Empty"},
		{"nested pattern of another enum", `fn f(e: Expr) -> Int { match e { case Expr.Neg(Color.Red): { return 1; } default: { return 0; } } }`, "a value of type Expr is never a Color.Red"},
		{"literal of the wrong type in a pattern", `fn f(s: Shape) -> Int { match s { case Shape.Circle("big"): { return 1; } default: { return 0; } } }`, "a value of type Float is never equal to a String"},
		{"pattern that is not a variant", `fn f(p: Point) -> Int { match p { case p.x: { return 1; } default: { return 0; } } }`, "'p.x' is not a variant of an enum"},
		{"value of a pattern used as another type", `fn f(s: Shape) -> Int { match s { case Shape.Circle(r): { return r; } default: { return 0; } } }`, "cannot return type Float"},
		{"name of a pattern used outside its case", `fn f(s: Shape) -> Float { match s { case Shape.Circle(r): { } default: { } } return r; }`, "undefined identifier 'r'"},
		{"name of one case used in another", `fn f(s: Shape) -> Float { match s { case Shape.Circle(r): { return r; } case Shape.Rect(w, h): { return r; } default: { return 0.0; } } }`, "undefined identifier 'r'"},
		{"missing return after an incomplete match", `fn f(s: Shape) -> Int { match s { case Shape.Circle(r): { return 1; } default: { } } }`, "can reach its end without returning a value"},

		// Declarations
		{"variant declared twice", `enum Bad { A, B, A }`, "enum Bad already has a variant 'A'"},
		{"variant of an unknown type", `enum Bad { A(Missing) } fn f(b: Bad) -> Int { match b { case Bad.A(m): { return m.x; } } }`, "Missing"},
		{"variant and method of one name", `enum Bad { size, Other } impl Bad { fn size() -> Int { return 1; } }`, "'size' is both a variant and a method of enum Bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := enumDecls + tt.src
			msgs, ok := checkSourceLoose(src)
			if !ok {
				t.Fatalf("test source does not parse")
			}
			for _, msg := range msgs {
				if strings.Contains(msg, tt.want) {
					return
				}
			}
			t.Errorf("expected an error containing %q, got %v", tt.want, msgs)
		})
	}
}

func TestEnumDiagnosticsCarrySuggestions(t *testing.T) {
	tests := []struct {
		src, code, suggestion string
	}{
		{`fn main() -> Int { let s = Shape.Circl(1.0); return 0; }`, ErrInvalidEnumUse, "Did you mean 'Circle'?"},
		{`fn main() -> Int { let s = Shape.Hexagon; return 0; }`, ErrInvalidEnumUse, "Its variants are Circle, Rect, Empty"},
		{`fn main() -> Int { let s = Shape.Empty(); return 0; }`, ErrInvalidEnumUse, "Write Shape.Empty without parentheses"},
		{`fn main() -> Int { let s = Shape.Rect(1.0); return 0; }`, ErrInvalidEnumUse, "It is declared as Shape.Rect(Float, Float)"},
		{`fn main() -> Int { let o = Option.None; return 0; }`, ErrInvalidEnumUse, "Write the type: Option<...>.None"},
		{`fn main() -> Int { let s = Shape; return 0; }`, ErrInvalidEnumUse, "Name one of its variants: Shape.Variant"},
		{`fn main() -> Int { let s = Shape { r: 1.0 }; return 0; }`, ErrInvalidEnumUse, "A value of an enum is made by naming a variant: Shape.Circle"},
		{`fn f(s: Shape) -> Float { return s.radius; }`, ErrUnknownField, "Take the value apart with a match"},
		{`fn f(s: Shape) -> Int { match s { case Shape.Circle(r): { return 1; } } }`, ErrNonExhaustiveMatch, "Add a case for each, or a 'default' case: case Shape.Empty: { ... }"},
		{`fn f(s: Shape) -> Int { match s { case Shape.Rect(w): { return 1; } default: { return 0; } } }`, ErrInvalidPattern, "Write the pattern as Shape.Rect(a, b)"},
		{`fn f(s: Shape) -> Int { match s { case Shape.Circel(r): { return 1; } default: { return 0; } } }`, ErrInvalidPattern, "Did you mean 'Circle'?"},
	}
	for _, tt := range tests {
		diags := checkSourceDiagnostics(t, enumDecls+tt.src)
		found := false
		for _, d := range diags {
			if d.Code == tt.code && strings.Contains(d.Suggestion, tt.suggestion) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s\n  expected %s with suggestion %q, got %v", tt.src, tt.code, tt.suggestion, diags)
		}
	}
}

func TestIncompleteMatchIsReportedOnce(t *testing.T) {
	// A match that leaves variants out is one mistake: it must not also be
	// reported as a function that may not return
	diags := checkSourceDiagnostics(t, enumDecls+`
		fn f(s: Shape) -> Int {
			match s {
				case Shape.Circle(r): { return 1; }
				case Shape.Empty: { return 2; }
			}
		}`)
	if len(diags) != 1 || diags[0].Code != ErrNonExhaustiveMatch {
		t.Errorf("expected only %s, got %v", ErrNonExhaustiveMatch, diags)
	}
	// Positioned at the match
	if diags[0].Pos.Line != 12 {
		t.Errorf("expected the error on the line of 'match', got line %d", diags[0].Pos.Line)
	}
}

func TestUnreachableCasesAreWarnings(t *testing.T) {
	diags := checkSourceDiagnostics(t, enumDecls+`
		fn f(s: Shape) -> Int {
			match s {
				case Shape.Circle(r): { return 1; }
				case Shape.Circle(q): { return 2; }
				case Shape.Rect: { return 3; }
				case Shape.Rect(w, 0.0): { return 4; }
				case Shape.Empty: { return 5; }
			}
		}`)
	if len(diags) != 2 {
		t.Fatalf("expected two warnings, got %v", diags)
	}
	for _, d := range diags {
		if d.Code != ErrUnreachableCode || d.Severity.String() != "WARNING" || !strings.Contains(d.Message, "this case is never reached") {
			t.Errorf("expected an unreachable-case warning, got %s: %s", d.Code, d.Message)
		}
	}
	if !strings.Contains(diags[0].Message, "Shape.Circle is handled above") || !strings.Contains(diags[1].Message, "Shape.Rect is handled above") {
		t.Errorf("unexpected messages: %q, %q", diags[0].Message, diags[1].Message)
	}

	// A partial pattern before the full one is not a warning: that is the
	// order that makes sense
	diags = checkSourceDiagnostics(t, enumDecls+`
		fn f(s: Shape) -> Int {
			match s {
				case Shape.Circle(0.0): { return 0; }
				case Shape.Circle(r): { return 1; }
				default: { return 2; }
			}
		}`)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestExhaustiveMatchIsMarked(t *testing.T) {
	_, _, prog := checkSource(t, enumDecls+`
		fn complete(s: Shape) -> Int {
			match s { case Shape.Circle(_): { return 1; } case Shape.Rect: { return 2; } case Shape.Empty: { return 3; } }
		}
		fn withDefault(s: Shape) -> Int {
			match s { case Shape.Circle(_): { return 1; } default: { return 0; } }
		}
		fn onInt(n: Int) -> Int {
			match n { case 1: { return 1; } default: { return 0; } }
		}`)

	exhaustive := make(map[string]bool)
	for _, decl := range prog.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			exhaustive[fn.Name] = fn.Body.Stmts[0].(*ast.MatchStmt).Exhaustive
		}
	}
	if !exhaustive["complete"] {
		t.Error("expected a match on every variant to be marked exhaustive")
	}
	// A default makes a match complete in the ordinary way; the mark is for
	// matches that are complete without one
	if exhaustive["withDefault"] || exhaustive["onInt"] {
		t.Errorf("expected only the match without a default to be marked, got %v", exhaustive)
	}
}

func TestEnumValueTypes(t *testing.T) {
	msgs, info, prog := checkSource(t, enumDecls+`
		fn main() -> Int {
			let a = Shape.Circle(1.0);
			let b = Shape.Empty;
			let c = Option.Some(1);
			let d = Option<String>.None;
			let e = Result<Int, String>.Ok(1);
			let f = Option.Some([Shape.Empty]);
			let g = Shape.Circle;
			let h = Shape.Rect;
			let i = List.Cons(1, List.Nil);
			let j = Option.Some(Option.Some(2.5));
			return 0;
		}`)
	if len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}

	want := map[string]string{
		"a": "Shape", "b": "Shape",
		"c": "Option<Int>", "d": "Option<String>", "e": "Result<Int, String>",
		"f": "Option<[Shape]>",
		"g": "fn(Float) -> Shape", "h": "fn(Float, Float) -> Shape",
		"i": "List<Int>", "j": "Option<Option<Float>>",
	}
	// Written the way messages write types
	main := prog.Decls[len(prog.Decls)-1].(*ast.FnDecl)
	for _, stmt := range main.Body.Stmts {
		let, ok := stmt.(*ast.LetStmt)
		if !ok {
			continue
		}
		if got := describe(info.Types[let.Value]); got != want[let.Name] {
			t.Errorf("%s: expected %s, got %s", let.Name, want[let.Name], got)
		}
	}
}

func TestEnumCastDescriptors(t *testing.T) {
	_, info, _ := checkSource(t, enumDecls+`
		impl Named for Shape { fn name() -> String { return "s"; } }
		impl Named for Point { fn name() -> String { return "p"; } }
		fn main() -> Int {
			let v: Any = 1;
			let a = v as Shape;
			let b = v as Color;
			let c = v as Option<Int>;
			let d = v as Named;
			let e = v is [Color];
			return 0;
		}`)

	var got []string
	for _, descriptor := range info.Casts {
		got = append(got, descriptor)
	}
	for _, descriptor := range info.Tests {
		got = append(got, descriptor)
	}
	joined := strings.Join(got, " ")
	// A value of an enum carries the name of its variant
	for _, want := range []string{
		"{Shape.Circle|Shape.Rect|Shape.Empty}",
		"{Color.Red|Color.Green|Color.Blue}",
		"{Option.Some|Option.None}",
		"[{Color.Red|Color.Green|Color.Blue}]",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a cast described as %s, got %v", want, got)
		}
	}
	// A trait is what implements it: structs by name, enums by their variants
	found := false
	for _, descriptor := range got {
		if strings.Contains(descriptor, "Point") && strings.Contains(descriptor, "Shape.Circle") && strings.Contains(descriptor, "Shape.Empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the cast to Named to list Point and the variants of Shape, got %v", got)
	}
}

func TestConstantsAccepted(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"literals of every kind", `const A = 1; const B = 2.5; const C = "text"; const D = true; const E: Int? = null;
			fn main() -> Int { let a: Int = A; let b: Float = B; let c: String = C; let d: Bool = D; return a; }`},
		{"operators and other constants", `const WIDTH = 4; const HEIGHT = WIDTH * 3 - 2; const AREA = WIDTH * HEIGHT; const NEG = -AREA;
			const BIG = AREA > 10 && !false; const LABEL = "w" + "x" + "h";
			fn main() -> Int { let ok: Bool = BIG; let s: String = LABEL; return AREA + NEG; }`},
		{"used before it is declared", `fn main() -> Int { return LATER + 1; } const LATER = EVEN_LATER * 2; const EVEN_LATER = 21;`},
		{"declared type", `const RATIO: Float = 1.5; const COUNT: Int = 3; const MAYBE: Int? = 4; const ANY: Any = "x";
			fn main() -> Int { let m: Int? = MAYBE; return COUNT; }`},
		{"in every kind of place", `const N = 3; struct P { x: Int; }
			fn twice(n: Int) -> Int { return n * 2; }
			fn main() -> Int {
				let xs = [N, N + 1]; let p = P { x: N }; let f = fn() -> Int { return N; };
				if N > 2 { return twice(N) + xs[N - 3] + p.x + f(); }
				match N { case 3: { return 1; } default: { return 0; } } }`},
		{"hidden by a local of the same name", `const N = 3;
			fn main() -> Int { let N = "text"; N = "other"; return __len(N); }`},
		{"hidden by a parameter", `const N = 3; fn f(N: String) -> Int { return __len(N); } fn main() -> Int { return f("x") + N; }`},
		{"in a generic function and a method", `const STEP = 2; struct C { n: Int; }
			impl C { fn next() -> Int { return self.n + STEP; } }
			fn bump<T>(v: T) -> Int { return STEP; }
			fn main() -> Int { return bump("x") + C { n: 1 }.next(); }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msgs, _, _ := checkSource(t, tt.src); len(msgs) != 0 {
				t.Errorf("expected no errors, got %v", msgs)
			}
		})
	}
}

func TestConstantsRejected(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
		want string
	}{
		{"assigned", `const N = 3; fn main() -> Int { N = 4; return N; }`, ErrAssignToConst, "cannot assign to constant 'N'"},
		{"compound assignment", `const N = 3; fn main() -> Int { N += 1; return N; }`, ErrAssignToConst, "cannot assign to constant 'N'"},
		{"incremented", `const N = 3; fn main() -> Int { N++; return N; }`, ErrAssignToConst, "cannot assign to constant 'N'"},
		{"defined by itself", `const A = A + 1;`, ErrConstantCycle, "constant 'A' is defined in terms of itself"},
		{"defined in a circle", `const A = B; const B = C * 2; const C = A - 1;`, ErrConstantCycle, "is defined in terms of itself"},
		{"value from a call", `fn size() -> Int { return 1; } const N = size();`, ErrNotConstant, "has to be known when the program is compiled"},
		{"an array with an item that is worked out", `fn f() -> Int { return 1; } const XS = [1, f()];`, ErrNotConstant, "has to be known when the program is compiled"},
		{"a struct with a field that is worked out", `struct P { x: Int; } fn f() -> Int { return 1; } const ORIGIN = P { x: f() };`, ErrNotConstant, "has to be known when the program is compiled"},
		{"an item of an array", `const XS = [1, 2]; const FIRST = XS[0];`, ErrNotConstant, "has to be known when the program is compiled"},
		{"value that is a lambda", `const F = fn() -> Int { return 1; };`, ErrNotConstant, "has to be known when the program is compiled"},
		{"part of the value from a call", `fn size() -> Int { return 1; } const N = 2 * (size() + 1);`, ErrNotConstant, "has to be known when the program is compiled"},
		{"value of the wrong type", `const N: Int = "three";`, ErrTypeMismatch, "cannot use a value of type String for constant 'N' of type Int"},
		{"used as the wrong type", `const N = 3; fn main() -> Int { let s: String = N; return 0; }`, ErrTypeMismatch, "cannot assign type Int to String"},
		{"operators on the wrong types", `const N = 3 + "x";`, "", "Int"},
		{"a number and a text mixed", `const A = 2; const B = A * "x";`, "", "Int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := checkSourceDiagnostics(t, tt.src)
			for _, d := range diags {
				if (tt.code == "" || d.Code == tt.code) && strings.Contains(d.Message, tt.want) {
					return
				}
			}
			t.Errorf("expected %s containing %q, got %v", tt.code, tt.want, diags)
		})
	}

	// A circle of constants is one mistake, reported once
	diags := checkSourceDiagnostics(t, `const A = B; const B = C * 2; const C = A - 1; fn main() -> Int { return A + B + C; }`)
	cycles := 0
	for _, d := range diags {
		if d.Code == ErrConstantCycle {
			cycles++
		}
	}
	if cycles != 1 {
		t.Errorf("expected the circle to be reported once, got %d times: %v", cycles, diags)
	}
}

func TestExhaustivenessLooksInsidePatterns(t *testing.T) {
	const decls = enumDecls + `
		enum Event { Deposit(Int), Withdraw(Int), Closed }
		enum Report { Line(String, Event), Total(Int) }
		enum Flag { On(Bool), Pair(Bool, Bool) }
	`

	complete := []struct{ name, src string }{
		{"every variant of the inner enum", `fn f(o: Option<Color>) -> Int {
			match o {
				case Option.Some(Color.Red): { return 1; }
				case Option.Some(Color.Green): { return 2; }
				case Option.Some(Color.Blue): { return 3; }
				case Option.None: { return 0; }
			} }`},
		{"inner variants and a name for the rest", `fn f(o: Option<Color>) -> Int {
			match o {
				case Option.Some(Color.Red): { return 1; }
				case Option.Some(other): { return 2; }
				case Option.None: { return 0; }
			} }`},
		{"two levels down", `fn f(o: Option<Result<Int, String>>) -> Int {
			match o {
				case Option.Some(Result.Ok(n)): { return n; }
				case Option.Some(Result.Err(e)): { return __len(e); }
				case Option.None: { return 0; }
			} }`},
		{"an inner enum in one of two values", `fn f(r: Report) -> Int {
			match r {
				case Report.Line(who, Event.Deposit(n)): { return n; }
				case Report.Line(who, Event.Withdraw(n)): { return n; }
				case Report.Line(who, Event.Closed): { return 0; }
				case Report.Total(n): { return n; }
			} }`},
		{"both values of a Bool", `fn f(g: Flag) -> Int {
			match g {
				case Flag.On(true): { return 1; }
				case Flag.On(false): { return 0; }
				case Flag.Pair(true, true): { return 3; }
				case Flag.Pair(true, false): { return 2; }
				case Flag.Pair(false, _): { return 1; }
			} }`},
		{"a pair covered column by column", `fn f(a: Option<Color>, b: Option<Bool>) -> Int {
			match Result<Option<Color>, Option<Bool>>.Ok(a) {
				case Result.Ok(Option.Some(_)): { return 1; }
				case Result.Ok(Option.None): { return 2; }
				case Result.Err(Option.Some(true)): { return 3; }
				case Result.Err(Option.Some(false)): { return 4; }
				case Result.Err(Option.None): { return 5; }
			} }`},
		{"an enum that holds itself", `fn f(e: Expr) -> Int {
			match e {
				case Expr.Neg(Expr.Neg(inner)): { return 1; }
				case Expr.Neg(Expr.Num(n)): { return 2; }
				case Expr.Neg(Expr.Add(_, _)): { return 3; }
				case Expr.Num(_): { return 4; }
				case Expr.Add(_, _): { return 5; }
			} }`},
		{"a whole-type case takes everything", `fn f(s: Shape) -> Int {
			match s { case Shape.Circle(0.0): { return 1; } case Shape: { return 0; } } }`},
		{"a literal first, then the rest", `fn f(o: Option<Int>) -> Int {
			match o { case Option.Some(0): { return 0; } case Option.Some(n): { return n; } case Option.None: { return -1; } } }`},
	}
	for _, tt := range complete {
		t.Run(tt.name, func(t *testing.T) {
			if diags := checkSourceDiagnostics(t, decls+tt.src); len(diags) != 0 {
				t.Errorf("expected the match to be complete, got %v", diags)
			}
		})
	}

	// What is left out is named as a pattern: the variant alone when none
	// of it is handled, with the value that is missing when some of it is
	incomplete := []struct{ name, src, want string }{
		{"one inner variant missing", `fn f(o: Option<Color>) -> Int {
			match o { case Option.Some(Color.Red): { return 1; } case Option.Some(Color.Green): { return 2; } case Option.None: { return 0; } } }`,
			"match on Option does not handle Option.Some(Color.Blue)"},
		{"the inner enum's other variant", `fn f(o: Option<Result<Int, String>>) -> Int {
			match o { case Option.Some(Result.Ok(n)): { return n; } case Option.None: { return 0; } } }`,
			"match on Option does not handle Option.Some(Result.Err)"},
		{"one combination of two values", `fn f(r: Report) -> Int {
			match r { case Report.Line(who, Event.Deposit(n)): { return n; } case Report.Line(who, Event.Closed): { return 0; } case Report.Total(n): { return n; } } }`,
			"match on Report does not handle Report.Line(_, Event.Withdraw)"},
		{"one value of a Bool", `fn f(g: Flag) -> Int {
			match g { case Flag.On(true): { return 1; } case Flag.Pair(_, _): { return 2; } } }`,
			"match on Flag does not handle Flag.On(false)"},
		{"one combination of two Bools", `fn f(g: Flag) -> Int {
			match g { case Flag.On(_): { return 1; } case Flag.Pair(true, _): { return 2; } case Flag.Pair(false, true): { return 3; } } }`,
			"match on Flag does not handle Flag.Pair(false, false)"},
		{"numbers cannot be listed", `fn f(o: Option<Int>) -> Int {
			match o { case Option.Some(0): { return 0; } case Option.Some(1): { return 1; } case Option.None: { return -1; } } }`,
			"match on Option does not handle Option.Some"},
		{"texts cannot be listed", `fn f(r: Result<String, Int>) -> Int {
			match r { case Result.Ok("yes"): { return 1; } case Result.Ok("no"): { return 0; } case Result.Err(_): { return -1; } } }`,
			"match on Result does not handle Result.Ok"},
		{"a variant and part of another", `fn f(o: Option<Color>) -> Int {
			match o { case Option.Some(Color.Red): { return 1; } } }`,
			"match on Option does not handle Option.None, Option.Some(Color.Green)"},
		{"deep inside an enum that holds itself", `fn f(e: Expr) -> Int {
			match e { case Expr.Neg(Expr.Neg(Expr.Num(n))): { return n; } case Expr.Num(_): { return 0; } case Expr.Add(_, _): { return 1; } } }`,
			"match on Expr does not handle Expr.Neg(Expr.Num)"},
	}
	for _, tt := range incomplete {
		t.Run(tt.name, func(t *testing.T) {
			diags := checkSourceDiagnostics(t, decls+tt.src)
			if len(diags) != 1 || diags[0].Code != ErrNonExhaustiveMatch || !strings.Contains(diags[0].Message, tt.want) {
				t.Errorf("expected %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestTypeArgumentsReachValuesInsideValues(t *testing.T) {
	msgs, _, _ := checkSource(t, enumDecls+`
		struct Slot { item: Option<Result<Int, String>>; }
		fn give() -> Option<Result<[Int], String>> { return Option.Some(Result.Ok([])); }
		fn take(o: Option<Option<Int>>) -> Int { return 0; }
		fn main() -> Int {
			let a: Option<Result<Int, String>> = Option.Some(Result.Ok(1));
			let b: Option<Result<Int, String>> = Option.Some(Result.Err("no"));
			let c: [Option<Result<Int, String>>] = [Option.Some(Result.Ok(1)), Option.None, Option.Some(Result.Err("x"))];
			let d: Result<Option<Int>, String> = Result.Ok(Option.None);
			let e: List<Option<Int>> = List.Cons(Option.None, List.Cons(Option.Some(1), List.Nil));
			let s = Slot { item: Option.Some(Result.Ok(2)) };
			s.item = Option.Some(Result.Err("gone"));
			return take(Option.Some(Option.None)) + take(Option.Some(Option.Some(3)));
		}`)
	if len(msgs) != 0 {
		t.Errorf("expected the expected type to reach the inner values, got %v", msgs)
	}

	// The expected type does not paper over a value of the wrong type
	msgs, _ = checkSourceLoose(enumDecls + `
		fn main() -> Int { let a: Option<Result<Int, String>> = Option.Some(Result.Ok("text")); return 0; }`)
	if len(msgs) == 0 || !strings.Contains(strings.Join(msgs, "\n"), "cannot assign type Option<Result<String, String>> to Option<Result<Int, String>>") {
		t.Errorf("expected the inner value to be rejected, got %v", msgs)
	}
}

func TestPanicEndsControlFlow(t *testing.T) {
	accepted := []struct{ name, src string }{
		{"last statement of a function", `fn f(n: Int) -> Int { if n > 0 { return n; } panic("no"); }`},
		{"both branches leave", `fn f(n: Int) -> Int { if n > 0 { return n; } else { panic("no"); } }`},
		{"a case of a match", `fn f(c: Color) -> Int { match c { case Color.Red: { return 1; } case Color.Green: { return 2; } case Color.Blue: { panic("no blue"); } } }`},
		{"default of a match", `fn f(n: Int) -> Int { match n { case 1: { return 1; } default: { panic("other"); } } }`},
		{"narrows like a return", `fn f(n: Int?) -> Int { if n == null { panic("nothing"); } return n + 1; }`},
		{"with any values", `struct P { x: Int; } fn f() -> Int { panic("state", 1, 2.5, true, null, [1], P { x: 1 }); }`},
		{"without a message", `fn f() -> Int { panic(); }`},
		{"in a Void function", `fn f(n: Int) { if n < 0 { panic("negative"); } println(n); }`},
		{"in a lambda", `fn main() -> Int { let f = fn(n: Int) -> Int { if n > 0 { return n; } panic("no"); }; return f(1); }`},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			if msgs, _, _ := checkSource(t, enumDecls+tt.src); len(msgs) != 0 {
				t.Errorf("expected no errors, got %v", msgs)
			}
		})
	}

	rejected := []struct{ name, src, want string }{
		{"panic in one branch only", `fn f(n: Int) -> Int { if n > 0 { panic("big"); } }`, "can reach its end without returning a value"},
		{"panic in a loop that may not run", `fn f(xs: [Int]) -> Int { for x in xs { panic("any"); } }`, "can reach its end without returning a value"},
		{"its result used as a value", `fn f() -> Int { let x: Int = panic("no"); return x; }`, "Void"},
		{"no narrowing when the panic is conditional", `fn f(n: Int?, strict: Bool) -> Int { if n == null { if strict { panic("nothing"); } } return n + 1; }`, "the value may be null"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			msgs, _ := checkSourceLoose(enumDecls + tt.src)
			if !strings.Contains(strings.Join(msgs, "\n"), tt.want) {
				t.Errorf("expected an error containing %q, got %v", tt.want, msgs)
			}
		})
	}

	// Code after a panic is never reached
	diags := checkSourceDiagnostics(t, `fn f() -> Int { panic("stop"); return 1; }`)
	if len(diags) != 1 || diags[0].Code != ErrUnreachableCode {
		t.Errorf("expected an unreachable-code warning, got %v", diags)
	}
}
