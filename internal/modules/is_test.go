package modules

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// After `is`, `name.Name` reads the same for a variant and for a type of
// a module, and a tuple of names the same for a pattern and for a tuple
// type. The loader knows the modules and their types: it is here that
// each becomes what it is.
func TestPatternTestsOnImportedNames(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import shapes;
			import shapes.{Shape, Circle};
			struct Local { x: Int; }
			enum Own { One(Int), None }

			fn main() -> Int {
				let value: Any = 1;
				let a = value is shapes.Square;
				let b = value is shapes.Shape.Round(r) && r > 0;
				let c = value is Shape.Flat;
				let d = value is shapes.Shape.Flat;
				let e = value is Own.One(_);
				let f = value is (Circle, Local);
				let g = value is (Circle, shapes.Shape.Round(_));
				let h = value is (first, second) && first == second;
				let i = value is (Shape.Round(2), Own.None);
				return 0;
			}
		`,
		"shapes.qi": `
			pub struct Circle { r: Int; }
			pub struct Square { side: Int; }
			pub enum Shape { Round(Int), Flat }
		`,
	})

	stmts := decl(t, prog, "main").(*ast.FnDecl).Body.Stmts
	value := func(i int) ast.Expr { return stmts[i].(*ast.LetStmt).Value }
	first := func(e ast.Expr) ast.Expr {
		if chain, ok := e.(*ast.BinaryExpr); ok {
			return chain.Left
		}
		return e
	}

	// Types: of a module, and a tuple of them
	for i, want := range map[int]string{1: "(value is shapes.Square)", 6: "(value is __Tuple2<shapes.Circle, Local>)"} {
		test, ok := value(i).(*ast.TypeTestExpr)
		if !ok {
			t.Errorf("statement %d: expected a type test, got %T", i, value(i))
			continue
		}
		if got := test.String(); got != want {
			t.Errorf("statement %d: got %s, want %s", i, got, want)
		}
	}

	// Variants: the enum takes its program-wide name, the names of the
	// pattern stay names
	for i, want := range map[int]string{
		2: "(value is shapes.Shape.Round(r))",
		3: "(value is shapes.Shape.Flat)",
		4: "(value is shapes.Shape.Flat)",
		5: "(value is Own.One(_))",
		7: "(value is __Tuple2.Of(Circle, shapes.Shape.Round(_)))",
		8: "(value is __Tuple2.Of(first, second))",
		9: "(value is __Tuple2.Of(shapes.Shape.Round(2), Own.None))",
	} {
		test, ok := first(value(i)).(*ast.PatternTestExpr)
		if !ok {
			t.Errorf("statement %d: expected a pattern test, got %T", i, first(value(i)))
			continue
		}
		if got := test.String(); got != want {
			t.Errorf("statement %d: got %s, want %s", i, got, want)
		}
	}
}

// A name of a pattern test is a variable where the test guards: it hides
// a function or a module of that name there, and nowhere else.
func TestPatternTestNamesHideModuleNames(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import shapes;
			import shapes.{Shape, area};

			fn main() -> Int {
				let s = Shape.Round(1);
				if s is Shape.Round(area) && area > 0 {
					return area;
				} else if s is Shape.Round(shapes) {
					return shapes;
				} else {
					return area(s) + shapes.area(s);
				}
				while s is Shape.Round(area) { return area; }
				let big = s is Shape.Round(area) && area > 1;
				return area(s);
			}
		`,
		"shapes.qi": `
			pub enum Shape { Round(Int), Flat }
			pub fn area(s: Shape) -> Int { return 1; }
		`,
	})

	got := body(t, prog, "main")
	for _, want := range []string{
		"if ((s is shapes.Shape.Round(area)) && (area > 0)) { return area; }",
		"else if (s is shapes.Shape.Round(shapes)) { return shapes; }",
		"else { return (shapes.area(s) + shapes.area(s)); }",
		"while (s is shapes.Shape.Round(area)) { return area; }",
		"let big = ((s is shapes.Shape.Round(area)) && (area > 1));",
		"return shapes.area(s);",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in:\n%s", want, got)
		}
	}
}

func TestPatternTestErrorsAcrossModules(t *testing.T) {
	tests := []struct {
		name string
		test string
		code string
		msg  string
	}{
		{"a name the module does not declare", "value is shapes.Missing", ErrUnknownName, "Missing"},
		{"a variant of an enum the module does not declare", "value is shapes.Missing.Round(_)", ErrUnknownName, "Missing"},
		{"a type that is not exported", "value is shapes.Hidden", ErrPrivate, "Hidden"},
		{"an enum that is not exported", "value is shapes.Secret.On", ErrPrivate, "Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := load(files{
				"main.qi": "import shapes;\nfn main() -> Int {\n  let value: Any = 1;\n  if " + tt.test + " { return 1; }\n  return 0;\n}\n",
				"shapes.qi": `
					pub enum Shape { Round(Int), Flat }
					struct Hidden { x: Int; }
					enum Secret { On, Off }
				`,
			})
			if len(errs.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
			if d.Pos.Line != 4 {
				t.Errorf("expected the diagnostic on line 4, got %d", d.Pos.Line)
			}
		})
	}
}
