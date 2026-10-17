package parser

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestParseShortFunctions(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`f(x -> x + 1);`, `f(fn(x) { return (x + 1); });`},
		{`f((a, b) -> a < b);`, `f(fn(a, b) { return (a < b); });`},
		{`f(() -> now());`, `f(fn() { return now(); });`},
		{`f((x) -> x);`, `f(fn(x) { return x; });`},
		{`f(x -> x.name, 2);`, `f(fn(x) { return x.name; }, 2);`},
		{`f(x -> y -> x + y);`, `f(fn(x) { return fn(y) { return (x + y); }; });`},
		{`f(x -> g(y -> y, x));`, `f(fn(x) { return g(fn(y) { return y; }, x); });`},
		{`f(x -> (x, x * 2));`, `f(fn(x) { return __Tuple2.Of(x, (x * 2)); });`},
		{`f(x -> Point { x: x });`, `f(fn(x) { return Point { x: x }; });`},
		{`f(x -> x?.name ?? "none");`, `f(fn(x) { return ((x?.__safe1.name) ?? "none"); });`},
		{`f(x -> [x, x]);`, `f(fn(x) { return [x, x]; });`},
		// A block, or one assignment, in place of the expression
		{`f(x -> { g(x); return x; });`, `f(fn(x) { g(x); return x; });`},
		{`f(() -> count++);`, `f(fn() { (count += 1); });`},
		{`f(n -> total += n);`, `f(fn(n) { (total += n); });`},
		{`f(v -> last = v);`, `f(fn(v) { (last = v); });`},
		// Where a value goes, it is a value
		{`let f: fn(Int) -> Int = n -> n * 2;`, `let f: fn(Int) -> Int = fn(n) { return (n * 2); };`},
		{`return x -> x;`, `return fn(x) { return x; };`},
		// The arrow of a function written out is not one of these
		{`let g = fn(n: Int) -> Int { return n; };`, `let g = fn(n: Int) -> Int { return n; };`},
		{`let p = (a, b);`, `let p = __Tuple2.Of(a, b);`},
	}
	for _, tt := range tests {
		got := strings.Join(strings.Fields(writtenBody(t, tt.src)), " ")
		if got != tt.want {
			t.Errorf("%s\n  want %s\n   got %s", tt.src, tt.want, got)
		}
	}

	// What the type checker is told about each
	errs := errors.NewHandler()
	prog := Parse("fn f() { g(x -> x); g(x -> { return x; }); g(() -> n++); g(fn(x: Int) -> Int { return x; }); }", "test.qi", errs)
	stmts := prog.Decls[0].(*ast.FnDecl).Body.Stmts
	want := []struct{ short, exprBody bool }{{true, true}, {true, false}, {true, false}, {false, false}}
	for i, w := range want {
		fn := stmts[i].(*ast.ExprStmt).Expr.(*ast.CallExpr).Args[0].(*ast.FnExpr)
		if fn.Short != w.short || fn.ExprBody != w.exprBody {
			t.Errorf("function %d: expected short=%v with an expression body=%v, got %v and %v", i, w.short, w.exprBody, fn.Short, fn.ExprBody)
		}
		if w.short && len(fn.Params) > 0 && fn.Params[0].Type != nil {
			t.Errorf("function %d: expected no type on its parameter yet", i)
		}
	}

	failures := []struct{ src, want, hint string }{
		{`f(x => x);`, "a function is written with '->', not '=>'", "item -> item.name"},
		{`f((a, b) => a);`, "a function is written with '->', not '=>'", ""},
		{`f((a, 1) -> a);`, "the parameters of a function written with '->' are names", "(first, second) -> ..."},
		{`f((a.b) -> a);`, "the parameters of a function written with '->' are names", ""},
		{`f(x -> );`, "expected expression", ""},
	}
	for _, tt := range failures {
		errs := errors.NewHandler()
		Parse("fn f() {\n"+tt.src+"\n}", "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, tt.want) || !strings.Contains(errs.Diagnostics[0].Suggestion, tt.hint) {
			t.Errorf("%s: expected an error containing %q, got %v", tt.src, tt.want, errs.Diagnostics)
		}
	}
}
