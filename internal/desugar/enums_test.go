package desugar

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// desugarProgram desugars a whole program and returns it.
func desugarProgram(t *testing.T, src string) *ast.Program {
	t.Helper()
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	table := resolve.NewResolver(errs).Resolve(prog)
	typeInfo := types.NewChecker(prog, table, errs).Check()
	if errs.HasErrors() {
		t.Fatalf("test source is invalid: %s", errs.Diagnostics[0].Message)
	}
	lowered, _ := NewDesugarer(prog, typeInfo, table, errs, parser.SourceLines(src)).Desugar()
	for _, d := range errs.Diagnostics {
		if d.Severity == errors.SeverityError {
			t.Errorf("unexpected diagnostic: %s", d.Message)
		}
	}
	return lowered
}

// fnBody returns the printed body of a function of a desugared program.
func fnBody(t *testing.T, prog *ast.Program, name string) string {
	t.Helper()
	for _, decl := range prog.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == name {
			return fn.Body.String()
		}
	}
	t.Fatalf("no function %s", name)
	return ""
}

const shapeDecls = `
	enum Shape { Circle(Float), Rect(Float, Float), Empty }
	enum Option<T> { Some(T), None }
	enum Expr { Num(Int), Add(Expr, Expr), Neg(Expr) }
`

func TestConstantsAreReplacedByTheirValue(t *testing.T) {
	prog := desugarProgram(t, `
		const WIDTH = 4;
		const HEIGHT = WIDTH * 3 - 2;
		const LABEL = "w" + "h";
		const NEG = -WIDTH;
		const ON = WIDTH > 2 && true;

		fn main() -> Int {
			let a = WIDTH;
			let b = HEIGHT + 1;
			let c = LABEL;
			let d = NEG * NEG;
			if ON { return WIDTH; }
			let WIDTH = 9;
			return WIDTH;
		}
	`)

	// Nothing of a constant is left to compile
	for _, decl := range prog.Decls {
		if c, ok := decl.(*ast.ConstDecl); ok {
			t.Errorf("did not expect the constant %s after desugaring", c.Name)
		}
	}
	if len(prog.Decls) != 1 {
		t.Errorf("expected only main, got %d declarations", len(prog.Decls))
	}

	// Each use is the value, with the constants it is made of replaced in
	// turn; the shape of the expression is kept, so 4 * 3 - 2 stays that
	want := `{ let a = 4; let b = (((4 * 3) - 2) + 1); let c = ("w" + "h"); let d = ((-4) * (-4)); if ((4 > 2) && true) { return 4; } let WIDTH = 9; return WIDTH; }`
	if got := fnBody(t, prog, "main"); got != want {
		t.Errorf("unexpected body:\n  want %s\n   got %s", want, got)
	}
}

func TestInlinedConstantIsPositionedAtItsUse(t *testing.T) {
	prog := desugarProgram(t, "const ZERO = 0;\nconst RATIO = 10 / ZERO;\n\nfn main() -> Int {\n    return RATIO;\n}\n")
	ret := prog.Decls[0].(*ast.FnDecl).Body.Stmts[0].(*ast.ReturnStmt)
	division, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected the division, got %T", ret.Value)
	}
	// A failure while working the value out is shown where it is used
	for _, node := range []ast.Node{division, division.Left, division.Right} {
		if node.Position().Line != 5 {
			t.Errorf("expected %s to be positioned on line 5, got line %d", node, node.Position().Line)
		}
	}
}

func TestVariantsBecomeValues(t *testing.T) {
	prog := desugarProgram(t, shapeDecls+`
		fn main() -> Int {
			let a = Shape.Circle(1.0);
			let b = Shape.Rect(1.0, 2.0 + 3.0);
			let c = Shape.Empty;
			let d = Option.Some(1);
			let e = Option<String>.None;
			let f: Option<[Int]> = Option.None;
			let g = Expr.Add(Expr.Num(1), Expr.Neg(Expr.Num(2)));
			let h = Shape.Circle;
			return 0;
		}
	`)
	stmts := prog.Decls[len(prog.Decls)-1].(*ast.FnDecl).Body.Stmts
	value := func(i int) ast.Expr { return stmts[i].(*ast.LetStmt).Value }

	// Each is the creation of a value; type arguments the checker worked
	// out are written down for the stages that follow
	rendered := []string{
		"Shape.Circle(1.0)", "Shape.Rect(1.0, (2.0 + 3.0))", "Shape.Empty",
		"Option<Int>.Some(1)", "Option<String>.None", "Option<[Int]>.None",
		"Expr.Add(Expr.Num(1), Expr.Neg(Expr.Num(2)))",
	}
	for i, want := range rendered {
		variant, ok := value(i).(*ast.VariantExpr)
		if !ok {
			t.Errorf("statement %d: expected a variant value, got %T", i, value(i))
			continue
		}
		if variant.String() != want {
			t.Errorf("statement %d: expected %s, got %s", i, want, variant)
		}
	}
	// Values inside a value are lowered as well
	inner := value(6).(*ast.VariantExpr).Args[1]
	if neg, ok := inner.(*ast.VariantExpr); !ok || neg.Variant != "Neg" {
		t.Errorf("expected the nested value to be lowered, got %T", inner)
	}

	// A variant with values, not called, is the function that makes one
	lambda, ok := value(7).(*ast.FnExpr)
	if !ok {
		t.Fatalf("expected a function, got %T", value(7))
	}
	if len(lambda.Params) != 1 || lambda.Params[0].Type.String() != "Float" || lambda.ReturnType.String() != "Shape" {
		t.Errorf("unexpected signature: %s", lambda)
	}
	if got := lambda.Body.String(); got != "{ return Shape.Circle(__value_0); }" {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestPatternsBecomeTestsAndNames(t *testing.T) {
	prog := desugarProgram(t, shapeDecls+`
		fn area(s: Shape) -> Float {
			match s {
				case Shape.Circle(r): { return r; }
				case Shape.Rect(w, _): { return w; }
				case Shape.Empty: { return 0.0; }
			}
		}
		fn partial(s: Shape) -> Int {
			match s {
				case Shape.Rect(w, 0.0): { return 1; }
				case Shape.Circle: { return 2; }
				default: { return 3; }
			}
		}
		fn nested(e: Expr) -> Int {
			match e {
				case Expr.Add(Expr.Num(0), Expr.Neg(inner)): { return 1; }
				default: { return 0; }
			}
		}
		fn make() -> Shape { return Shape.Empty; }
		fn onCall() -> Int {
			match make() {
				case Shape.Circle(r): { return 1; }
				default: { return 0; }
			}
		}
	`)

	// A test for the variant, then its values named at the start of the
	// case. `_` names nothing. When every variant is handled the last case
	// needs no test: it is the final else.
	area := fnBody(t, prog, "area")
	want := "{ if (s is Shape.Circle) { let r = s.Circle#0; return r; } else if (s is Shape.Rect) { let w = s.Rect#0; return w; } else { return 0.0; } }"
	if area != want {
		t.Errorf("unexpected lowering of area:\n  want %s\n   got %s", want, area)
	}

	// A literal in a pattern is a comparison, made only after the test
	// that the value is the variant; a default stays the else
	partial := fnBody(t, prog, "partial")
	want = "{ if ((s is Shape.Rect) && (s.Rect#1 == 0.0)) { let w = s.Rect#0; return 1; } else if (s is Shape.Circle) { return 2; } else if true { return 3; } }"
	if partial != want {
		t.Errorf("unexpected lowering of partial:\n  want %s\n   got %s", want, partial)
	}

	// A pattern inside a pattern tests the value it is given, in order
	nested := fnBody(t, prog, "nested")
	for _, part := range []string{
		"(e is Expr.Add)", "(e.Add#0 is Expr.Num)", "(e.Add#0.Num#0 == 0)", "(e.Add#1 is Expr.Neg)", "let inner = e.Add#1.Neg#0;",
	} {
		if !strings.Contains(nested, part) {
			t.Errorf("expected the lowering of nested to contain %s, got:\n%s", part, nested)
		}
	}
	if !before(nested, "(e is Expr.Add)", "(e.Add#0 is Expr.Num)") || !before(nested, "(e.Add#0 is Expr.Num)", "(e.Add#0.Num#0 == 0)") {
		t.Errorf("expected each value to be read after the test that it is there:\n%s", nested)
	}

	// What is matched is worked out once, whatever the number of cases
	onCall := fnBody(t, prog, "onCall")
	if strings.Count(onCall, "make()") != 1 {
		t.Errorf("expected one call of make, got:\n%s", onCall)
	}
	if !strings.Contains(onCall, "let __match_tmp_") || !strings.Contains(onCall, " = make();") {
		t.Errorf("expected the value in a temporary, got:\n%s", onCall)
	}
}

func TestEnumsInGenericAndMethodBodies(t *testing.T) {
	prog := desugarProgram(t, shapeDecls+`
		impl Shape {
			fn isEmpty() -> Bool {
				match self { case Shape.Empty: { return true; } default: { return false; } }
			}
		}
		fn main() -> Int {
			let f = fn(s: Shape) -> Int {
				match s { case Shape.Circle(r): { return 1; } default: { return 0; } }
			};
			return f(Shape.Circle(1.0));
		}
	`)

	var method string
	for _, decl := range prog.Decls {
		if impl, ok := decl.(*ast.ImplDecl); ok {
			method = impl.Methods[0].Body.String()
		}
	}
	if !strings.Contains(method, "(self is Shape.Empty)") {
		t.Errorf("expected the method's match to be lowered, got %s", method)
	}
	main := fnBody(t, prog, "main")
	if !strings.Contains(main, "(s is Shape.Circle)") || !strings.Contains(main, "let r = s.Circle#0;") || !strings.Contains(main, "f(Shape.Circle(1.0))") {
		t.Errorf("expected the lambda's match and the argument to be lowered, got %s", main)
	}
}
