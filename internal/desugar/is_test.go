package desugar

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
)

const isDecls = shapeDecls + `
	struct Point { x: Int; y: Int; }
	enum Link { To(Point?), End }
	fn next() -> Option<Shape> { return Option.None; }
	fn ready() -> Bool { return true; }
`

// lowered desugars a function `f` with the given parameters and body, and
// returns its body as text.
func lowered(t *testing.T, params, body string) string {
	t.Helper()
	prog := desugarProgram(t, isDecls+"fn f("+params+") -> Int {\n"+body+"\nreturn 0;\n}\nfn main() -> Int { return 0; }")
	return fnBody(t, prog, "f")
}

func TestPatternTestsBecomeTestsAndVariables(t *testing.T) {
	tests := []struct {
		name   string
		params string
		body   string
		want   string
	}{
		{
			"a variant without values is one test",
			"s: Shape", `if s is Shape.Empty { return 1; }`,
			"if (s is Shape.Empty) { return 1; }",
		},
		{
			"each name is a variable of the block, which stays a block of its own",
			"s: Shape", `if s is Shape.Rect(w, h) { return 1; } else { return 2; }`,
			"if (s is Shape.Rect) { let w = s.Rect#0; let h = s.Rect#1; { return 1; } } else { return 2; }",
		},
		{
			"a literal is compared, an ignored value is not read",
			"s: Shape", `if s is Shape.Rect(_, 0.0) { return 1; }`,
			"if ((s is Shape.Rect) && (s.Rect#1 == 0.0)) { return 1; }",
		},
		{
			"a pattern inside a pattern is tested after the one around it",
			"e: Expr", `if e is Expr.Add(Expr.Num(0), Expr.Neg(inner)) { return 1; }`,
			"if ((((e is Expr.Add) && (e.Add#0 is Expr.Num)) && (e.Add#0.Num#0 == 0)) && (e.Add#1 is Expr.Neg)) { let inner = e.Add#1.Neg#0; { return 1; } }",
		},
		{
			"the rest of a condition sees the names, from a copy of the value",
			"s: Shape", `if s is Shape.Circle(r) && r > 1.0 { return 1; }`,
			"{ let __is_0: Shape = s; if ((__is_0 is Shape.Circle) && { let r = __is_0.Circle#0; (r > 1.0) }) { let r = __is_0.Circle#0; { return 1; } } }",
		},
		{
			"a test without names needs no copy, whatever follows it",
			"s: Shape", `if s is Shape.Circle(_) && ready() { return 1; }`,
			"if ((s is Shape.Circle) && ready()) { return 1; }",
		},
		{
			"a value that is computed is computed once, before the statement when it comes first",
			"", `if next() is Option.Some(found) { return 1; }`,
			"{ let __is_0: Option<Shape> = next(); if (__is_0 is Option.Some) { let found = __is_0.Some#0; { return 1; } } }",
		},
		{
			"behind an &&, only when its turn comes",
			"", `if ready() && next() is Option.Some(found) { return 1; } else { return 2; }`,
			"{ let __is_0: Option<Shape>? = null; if (ready() && { (__is_0 = next()); (__is_0 is Option.Some) }) { let found = __is_0.Some#0; { return 1; } } else { return 2; } }",
		},
		{
			"a chain is regrouped so that all that follows a test sees its names",
			"s: Shape", `if next() is Option.Some(found) && found is Shape.Circle(r) && r > 1.0 { return 1; }`,
			"{ let __is_0: Option<Shape> = next(); let __is_1: Shape? = null; " +
				"if ((__is_0 is Option.Some) && { let found = __is_0.Some#0; { (__is_1 = found); ((__is_1 is Shape.Circle) && { let r = __is_1.Circle#0; (r > 1.0) }) } }) " +
				"{ let found = __is_0.Some#0; let r = __is_1.Circle#0; { return 1; } } }",
		},
		{
			"a pattern that names the value as the value is named reads a copy",
			"s: Option<Option<Int>>", `if s is Option.Some(s) { return 1; }`,
			"{ let __is_0: Option<Option<Int>> = s; if (__is_0 is Option.Some) { let s = __is_0.Some#0; { return 1; } } }",
		},
		{
			"what the condition established about a name is the type it is declared with in the block",
			"l: Link", `if l is Link.To(p) && p != null { return p.x; }`,
			"{ let __is_0: Link = l; if ((__is_0 is Link.To) && { let p = __is_0.To#0; (p != null) }) { let p: Point = __is_0.To#0; { return p.x; } } }",
		},
		{
			"an else if keeps its own temporaries in its own block",
			"s: Shape", `if s is Shape.Empty { return 1; } else if next() is Option.Some(found) { return 2; } else { return 3; }`,
			"if (s is Shape.Empty) { return 1; } else { let __is_0: Option<Shape> = next(); if (__is_0 is Option.Some) { let found = __is_0.Some#0; { return 2; } } else { return 3; } }",
		},
		{
			"a while on a variable stays a while",
			"s: Shape", `while s is Shape.Circle(r) { s = Shape.Empty; }`,
			"while (s is Shape.Circle) { let r = s.Circle#0; { (s = Shape.Empty); } }",
		},
		{
			"a while on a computed value computes it on every turn",
			"", `while next() is Option.Some(found) && ready() { continue; }`,
			"while true { let __is_0: Option<Shape> = next(); if ((__is_0 is Option.Some) && { let found = __is_0.Some#0; ready() }) { let found = __is_0.Some#0; { continue; } } else { break; } }",
		},
		{
			"a test that is a value",
			"s: Shape", `let round = s is Shape.Circle(_); let none = next() is Option.None;`,
			"let round = (s is Shape.Circle); let none = { let __is_0: Option<Shape> = next(); (__is_0 is Option.None) };",
		},
		{
			"a chain that is a value",
			"s: Shape", `let big = ready() && s is Shape.Circle(r) && r > 1.0;`,
			"let big = { let __is_0: Shape? = null; (ready() && { (__is_0 = s); ((__is_0 is Shape.Circle) && { let r = __is_0.Circle#0; (r > 1.0) }) }) };",
		},
		{
			"a test under a not, and on a side of an or",
			"s: Shape", `if !(s is Shape.Empty) || next() is Option.None { return 1; }`,
			"if ((!(s is Shape.Empty)) || { let __is_0: Option<Shape> = next(); (__is_0 is Option.None) }) { return 1; }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lowered(t, tt.params, tt.body)
			if strings.Contains(got, "PatternTest") {
				t.Errorf("a pattern test is left: %s", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("unexpected lowering\n--- want ---\n%s\n--- got ---\n%s", tt.want, got)
			}
		})
	}
}

// Nothing of a pattern test is left for the stages below, wherever it
// stood.
func TestNoPatternTestIsLeft(t *testing.T) {
	prog := desugarProgram(t, isDecls+`
		impl Shape {
			fn radius() -> Float { if self is Shape.Circle(r) { return r; } return 0.0; }
		}
		fn main() -> Int {
			let s = Shape.Circle(1.0);
			let shapes = [s, Shape.Empty];
			let big = fn(each: Shape) -> Bool { return each is Shape.Circle(r) && r > 1.0; };
			for each in shapes {
				match each {
					case Shape.Circle(r) if next() is Option.Some(o) && o is Shape.Empty: { println(r); }
					default: { defer { if each is Shape.Rect(w, _) { println(w); } } }
				}
			}
			println(big(s), s.radius(), [s is Shape.Empty, s is Shape.Circle(_)]);
			return 0;
		}
	`)

	for _, decl := range prog.Decls {
		var bodies []*ast.BlockStmt
		switch d := decl.(type) {
		case *ast.FnDecl:
			bodies = append(bodies, d.Body)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				bodies = append(bodies, method.Body)
			}
		}
		for _, body := range bodies {
			if body == nil {
				continue
			}
			ast.Walk(body, func(expr ast.Expr) {
				if test, ok := expr.(*ast.PatternTestExpr); ok {
					t.Errorf("a pattern test is left after desugaring: %s", test)
				}
			}, func(ast.Stmt) {})
		}
	}
}
