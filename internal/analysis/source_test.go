package analysis

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// analyzeSource runs closure analysis on source text and returns, for each
// lambda in order of appearance, its captures as "name" (taken from the
// enclosing function's locals) or "name^" (passed on from its upvalues).
func analyzeSource(t *testing.T, src string) []string {
	t.Helper()

	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	table := resolve.NewResolver(errs).Resolve(prog)
	typeInfo := types.NewChecker(prog, table, errs).Check()
	if errs.HasErrors() {
		t.Fatalf("test source is invalid: %s", errs.Diagnostics[0].Message)
	}
	info := NewAnalyzer(prog, table, typeInfo, errs).Analyze()

	var result []string
	var visitExpr func(expr ast.Expr)
	var visitStmt func(stmt ast.Stmt)
	visitExpr = func(expr ast.Expr) {
		switch e := expr.(type) {
		case *ast.FnExpr:
			var names []string
			for _, cv := range info.GetCaptures(e).CapturedVars {
				name := cv.Name
				if cv.FromUpvalue {
					name += "^"
				}
				names = append(names, name)
			}
			result = append(result, strings.Join(names, ","))
			visitStmt(e.Body)
		case *ast.CallExpr:
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		}
	}
	visitStmt = func(stmt ast.Stmt) {
		switch s := stmt.(type) {
		case *ast.BlockStmt:
			for _, inner := range s.Stmts {
				visitStmt(inner)
			}
		case *ast.LetStmt:
			visitExpr(s.Value)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.ForStmt:
			visitStmt(s.Body)
		case *ast.IfStmt:
			visitStmt(s.Then)
			if s.Else != nil {
				visitStmt(s.Else)
			}
		}
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			visitStmt(d.Body)
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				visitStmt(m.Body)
			}
		}
	}
	return result
}

func TestCapturesFromSource(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			"local declared with let",
			`fn main() -> Int { let count = 0; let f = fn() -> Int { return count; }; return 0; }`,
			[]string{"count"},
		},
		{
			"parameter and local together",
			`fn make(n: Int) -> Int { let m = 1; let f = fn(x: Int) -> Int { return x + n + m; }; return 0; }`,
			[]string{"n,m"},
		},
		{
			"own locals and parameters are not captures",
			`fn main() -> Int { let f = fn(x: Int) -> Int { let y = x; return y; }; return 0; }`,
			[]string{""},
		},
		{
			"globals are not captures",
			`fn helper() -> Int { return 1; }
			 fn main() -> Int { let f = fn() -> Int { return helper(); }; return 0; }`,
			[]string{""},
		},
		{
			"loop variable",
			`fn main() -> Int { for v in [1] { let f = fn() -> Int { return v; }; } return 0; }`,
			[]string{"v"},
		},
		{
			"assignment counts as a use",
			`fn main() -> Int { let n = 0; let f = fn() -> Void { n = 1; }; return 0; }`,
			[]string{"n"},
		},
		{
			// The middle lambda never mentions `a`, but it has to carry it
			// so the innermost one can reach it.
			"variable passed through an intermediate lambda",
			`fn main() -> Int {
				let a = 1;
				let outer = fn() -> Int {
					let inner = fn() -> Int { return a; };
					return inner();
				};
				return 0;
			}`,
			[]string{"a", "a^"},
		},
		{
			"each level captures what it needs",
			`fn main() -> Int {
				let a = 1;
				let outer = fn(b: Int) -> Int {
					let inner = fn() -> Int { return a + b; };
					return inner();
				};
				return 0;
			}`,
			[]string{"a", "a^,b"},
		},
		{
			"self inside a method",
			`struct P { x: Int; }
			 impl P { fn getter() -> Int { let f = fn() -> Int { return self.x; }; return f(); } }`,
			[]string{"self"},
		},
		{
			// A name a loop inside declares is the loop's: after it, the
			// name is the captured one again
			"a loop variable of the same name ends with the loop",
			`fn main() -> Int {
				let i = 7;
				let f = fn() -> Int {
					for i in [1, 2] { print(i); }
					return i;
				};
				return 0;
			}`,
			[]string{"i"},
		},
		{
			"a variable of a block ends with the block",
			`fn main() -> Int {
				let total = 7;
				let f = fn(flag: Bool) -> Int {
					if flag { let total = 1; print(total); }
					return total;
				};
				return 0;
			}`,
			[]string{"total"},
		},
		{
			"inside the block it is the block's",
			`fn main() -> Int {
				let total = 7;
				let f = fn(flag: Bool) -> Int {
					if flag { let total = 1; return total; }
					return 0;
				};
				return 0;
			}`,
			[]string{""},
		},
		{
			// The function in the middle declares n in a block; the one
			// inside, made after the block, reaches the outer n through it
			"two levels down, past a block of the middle one",
			`fn main() -> Int {
				let n = 3;
				let outer = fn() -> Int {
					if true { let n = 1; print(n); }
					let inner = fn() -> Int { return n; };
					return inner();
				};
				return 0;
			}`,
			[]string{"n", "n^"},
		},
		{
			"a function made inside the block sees the block's",
			`fn main() -> Int {
				let n = 3;
				let outer = fn() -> Int {
					if true {
						let n = 1;
						let inner = fn() -> Int { return n; };
						return inner();
					}
					return 0;
				};
				return 0;
			}`,
			[]string{"", "n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeSource(t, tt.src)
			if strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
				t.Errorf("expected captures %q, got %q", tt.want, got)
			}
		})
	}
}
