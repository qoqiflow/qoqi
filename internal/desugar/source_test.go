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

// desugarBody desugars the statements of a main function and returns the
// printed result.
func desugarBody(t *testing.T, body string) string {
	t.Helper()

	src := "fn check(v: Bool) -> Bool { return v; }\nfn items() -> [Int] { return [1]; }\nfn main() -> Void {\n" + body + "\n}"
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	table := resolve.NewResolver(errs).Resolve(prog)
	typeInfo := types.NewChecker(prog, table, errs).Check()
	if errs.HasErrors() {
		t.Fatalf("test source is invalid: %s", errs.Diagnostics[0].Message)
	}

	lowered, _ := NewDesugarer(prog, typeInfo, table, errs, parser.SourceLines(src)).Desugar()
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
	return lowered.Decls[2].(*ast.FnDecl).Body.String()
}

// before reports whether a appears before b in s (and both appear).
func before(s, a, b string) bool {
	i, j := strings.Index(s, a), strings.Index(s, b)
	return i >= 0 && j >= 0 && i < j
}

func TestForLoopIncrementsBeforeBody(t *testing.T) {
	out := desugarBody(t, `let xs = [1, 2]; for v in xs { continue; }`)

	if strings.Contains(out, "for v in") {
		t.Fatalf("expected the for loop to be rewritten, got: %s", out)
	}
	// `continue` jumps back to the condition; the increment must already
	// have happened or the loop would never advance.
	if !before(out, "(__iter_0 = (__iter_0 + 1));", "continue;") {
		t.Errorf("expected the increment before the body, got: %s", out)
	}
	if !before(out, "let v: Int = xs[__iter_0];", "(__iter_0 = (__iter_0 + 1));") {
		t.Errorf("expected the element to be read before the increment, got: %s", out)
	}
}

func TestForLoopEvaluatesIterableOnce(t *testing.T) {
	out := desugarBody(t, `for v in items() { print(v); }`)

	if n := strings.Count(out, "items()"); n != 1 {
		t.Errorf("expected items() to be called once, found %d calls in: %s", n, out)
	}

	// A plain variable needs no temporary
	out = desugarBody(t, `let xs = [1]; for v in xs { print(v); }`)
	if strings.Contains(out, "__tmp_") {
		t.Errorf("expected no temporary for a variable iterable, got: %s", out)
	}
}

func TestForLoopIndexIsACopyTakenBeforeTheIncrement(t *testing.T) {
	out := desugarBody(t, `let xs = [1, 2]; for i, v in xs { if i == 0 { continue; } print(i, v); }`)

	// let v = xs[__iter]; let i = __iter; __iter = __iter + 1; body
	if !before(out, "let v: Int = xs[__iter_0];", "let i: Int = __iter_0;") {
		t.Errorf("expected the element, then the index, got: %s", out)
	}
	if !before(out, "let i: Int = __iter_0;", "(__iter_0 = (__iter_0 + 1));") {
		t.Errorf("expected the index to be copied before the increment, got: %s", out)
	}
	if !before(out, "(__iter_0 = (__iter_0 + 1));", "continue;") {
		t.Errorf("expected the increment before the body, got: %s", out)
	}

	// Without an index nothing extra is declared
	out = desugarBody(t, `let xs = [1]; for v in xs { print(v); }`)
	if strings.Count(out, "let ") != 4 { // xs, __iter, __len, v
		t.Errorf("expected no index variable, got: %s", out)
	}
}

func TestCompoundAssignmentsPassThrough(t *testing.T) {
	out := desugarBody(t, `let i = 0; let xs = [1]; i += 2; xs[0] *= 3; i++;`)
	for _, want := range []string{"(i += 2);", "(xs[0] *= 3);", "(i += 1);"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in: %s", want, out)
		}
	}
}

func TestShortCircuitOperatorsAreKept(t *testing.T) {
	// &&, || and ?? are lowered to branches by IR generation. They stay in
	// the tree so that the type checker, which runs again on the desugared
	// program, still sees what a condition says.
	tests := []struct {
		body string
		want string
	}{
		{`let i = 0; while i < 3 && check(true) { i = i + 1; }`, "while ((i < 3) && check(true)) {"},
		{`if check(true) || check(false) { print(1); }`, "if (check(true) || check(false)) {"},
		{`let ok = check(true) && check(false); print(ok);`, "let ok = (check(true) && check(false)); print(ok);"},
		{`let n: Int? = null; let m = n ?? 3;`, "let m = (n ?? 3);"},
	}

	for _, tt := range tests {
		out := desugarBody(t, tt.body)
		if !strings.Contains(out, tt.want) {
			t.Errorf("expected %q in: %s", tt.want, out)
		}
		if strings.Contains(out, "__tmp_") {
			t.Errorf("expected no temporaries, got: %s", out)
		}
	}
}

func TestOperandsOfShortCircuitOperatorsAreDesugared(t *testing.T) {
	// The operator stays, but what is inside it is still lowered
	out := desugarBody(t, `let f = fn() -> Bool { for v in items() { print(v); } return true; }; if f() && check(true) { print(1); }`)
	if strings.Contains(out, "for v in") {
		t.Errorf("expected the loop inside the lambda to be rewritten, got: %s", out)
	}
}

func TestTypePatternsBecomeTypeTests(t *testing.T) {
	out := desugarBody(t, `
		let v: Any = 1;
		match v {
			case Int: { print("int"); }
			case [String]: { print("strings"); }
			default: { print("other"); }
		}
	`)

	// The variable is tested directly, so the checker can narrow it
	for _, want := range []string{`if (v is Int)`, `else if (v is [String])`, `else if true`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in: %s", want, out)
		}
	}
	if strings.Contains(out, "__match_tmp") {
		t.Errorf("expected no temporary for a plain variable, got: %s", out)
	}

	// Anything else is evaluated once, into a temporary
	out = desugarBody(t, `match items()[0] { case 1: { print("one"); } default: { print("other"); } }`)
	if !strings.Contains(out, "let __match_tmp_") || strings.Count(out, "items()") != 1 {
		t.Errorf("expected the matched expression in a temporary, evaluated once: %s", out)
	}
}

func TestMatchDefaultAndNegativeLiterals(t *testing.T) {
	out := desugarBody(t, `
		let n = 1;
		match n {
			case -1: { print("neg"); }
			case 2: { print("two"); }
			default: { print("other"); }
		}
	`)

	if strings.Contains(out, "match") {
		t.Fatalf("expected the match to be rewritten, got: %s", out)
	}
	for _, want := range []string{`if (n == (-1))`, `else if (n == 2)`, `else if true { print("other"); }`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in: %s", want, out)
		}
	}
}

func TestLambdaBodiesAreDesugared(t *testing.T) {
	out := desugarBody(t, `let f = fn(xs: [Int]) -> Void { for v in xs { print(v); } };`)

	if strings.Contains(out, "for v in") || !strings.Contains(out, "while") {
		t.Errorf("expected the loop inside the lambda to be rewritten, got: %s", out)
	}
}
