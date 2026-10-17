package irgen

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// generateSource runs the front end and IR generation on source text.
func generateSource(t *testing.T, src string) (*ir.Program, *errors.Handler) {
	t.Helper()

	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	table := resolve.NewResolver(errs).Resolve(prog)
	typeInfo := types.NewChecker(prog, table, errs).Check()
	if errs.HasErrors() {
		t.Fatalf("test source is invalid: %s", errs.Diagnostics[0].Message)
	}
	captures := analysis.NewAnalyzer(prog, table, typeInfo, errs).Analyze()
	return NewIRGenerator(prog, typeInfo, table, captures, errs).Generate(), errs
}

// dumpFunction returns the IR of one function, failing on any diagnostic.
func dumpFunction(t *testing.T, src, name string) string {
	t.Helper()

	prog, errs := generateSource(t, src)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
	for _, fn := range prog.Functions {
		if fn.Name == name {
			return fn.Dump()
		}
	}
	t.Fatalf("function %s not generated", name)
	return ""
}

func expectLines(t *testing.T, dump string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(dump, line) {
			t.Errorf("expected %q in:\n%s", line, dump)
		}
	}
}

func TestLetCopiesVariablesButAdoptsTemporaries(t *testing.T) {
	dump := dumpFunction(t, `fn main() -> Int { let a = 1; let b = a; b = 2; return a; }`, "main")

	// `let b = a` must not alias a's register, or `b = 2` would change a
	expectLines(t, dump,
		"%v1 = MOV, 1",
		"%v2 = MOV, %v1",
		"%v2 = MOV, %v3",
		"RET %v1",
	)
}

func TestBlocksScopeTheirVariables(t *testing.T) {
	dump := dumpFunction(t, `fn main() -> Int { let x = 1; { let x = 2; } return x; }`, "main")
	expectLines(t, dump, "%v1 = MOV, 1", "%v2 = MOV, 2", "RET %v1")
}

func TestAssignmentTargets(t *testing.T) {
	dump := dumpFunction(t, `
		struct P { x: Int; }
		fn main() -> Int {
			let xs = [1];
			let p = P { x: 1 };
			xs[0] = 5;
			p.x = 6;
			return 0;
		}`, "main")

	expectLines(t, dump, "SET_INDEX %v", `SET_FIELD %v`, `, "x", %v`)
}

func TestShortCircuitBranchesAroundRightOperand(t *testing.T) {
	src := `
		fn side() -> Bool { return true; }
		fn main() -> Bool { let a = false; return a %s side(); }`

	for op, branch := range map[string]string{"&&": "BR %v2, b1, b2", "||": "BR %v2, b2, b1"} {
		dump := dumpFunction(t, strings.Replace(src, "%s", op, 1), "main")
		// The call lives in its own block, reached only when needed
		expectLines(t, dump, branch, "b1:\n\t%v3 = MOV, \"side\"\n\t%v4 = CALL, %v3\n\t%v2 = MOV, %v4\n\tJMP b2")
	}

	dump := dumpFunction(t, `fn main() -> Int { let a = null; return a ?? 1; }`, "main")
	expectLines(t, dump, "EQ, %v2, %v3", "BR %v4, b1, b2")
}

func TestStructInitSetsFieldsByName(t *testing.T) {
	dump := dumpFunction(t, `
		struct P { x: Int; y: Int; }
		fn main() -> Int { let p = P { y: 2, x: 1 }; return p.x; }`, "main")

	expectLines(t, dump,
		`NEW_STRUCT, "P"`,
		`SET_FIELD %v3, "x", %v1`,
		`SET_FIELD %v3, "y", %v2`,
	)
}

func TestMethodCallOnKnownStructIsDirect(t *testing.T) {
	dump := dumpFunction(t, `
		struct P { x: Int; y: Int; }
		impl P { fn sum() -> Int { return self.x + self.y; } }
		fn main() -> Int { let p = P { x: 1, y: 2 }; return p.sum(); }`, "main")

	// The receiver is a P, so the call names P.sum and passes p as self
	expectLines(t, dump, `%v4 = MOV, "P.sum"`, `%v5 = CALL, %v4, %v3`)

	// Nothing has to be looked up at runtime, so nothing is stored on the value
	if strings.Contains(dump, `"sum"`) {
		t.Errorf("expected no method stored on or read from the value, got:\n%s", dump)
	}
}

func TestMethodCallThroughTraitIsLookedUpOnValue(t *testing.T) {
	src := `
		trait Shape { fn area() -> Int; }
		struct Sq { side: Int; }
		impl Shape for Sq { fn area() -> Int { return self.side * self.side; } }
		fn measure(s: Shape) -> Int { return s.area(); }
		fn main() -> Int { return measure(Sq { side: 3 }); }`

	// Only the trait is known in measure: read the method from the value
	// Only the trait is known in measure: the value is asked for the
	// method, by the name of the type it carries
	expectLines(t, dumpFunction(t, src, "measure"), `%v2 = GET_METHOD, %v1, "area"`, `%v3 = CALL, %v2, %v1`)

	// ...and nothing is kept on the value for that: making one sets its
	// fields and no more
	main := dumpFunction(t, src, "main")
	expectLines(t, main, `NEW_STRUCT, "Sq"`, `"side", %v`)
	if strings.Contains(main, "Sq.area") || strings.Count(main, "SET_FIELD") != 1 {
		t.Errorf("a method is stored on the value:\n%s", main)
	}
}

func TestFieldAndMethodMayShareAName(t *testing.T) {
	src := `
		struct Box { size: Int; }
		struct Crate { n: Int; }
		impl Crate { fn size() -> Int { return self.n; } }
		struct Holder { size: fn() -> Int; }
		fn main() -> Int {
			let b = Box { size: 1 };
			let c = Crate { n: 2 };
			let h = Holder { size: fn() -> Int { return 3; } };
			return b.size + c.size() + h.size();
		}`
	dump := dumpFunction(t, src, "main")

	// c.size() is the method of Crate; h.size() calls the function in the field
	expectLines(t, dump, `MOV, "Crate.size"`)
	if n := strings.Count(dump, `GET_FIELD`); n != 2 {
		t.Errorf("expected 2 field reads (b.size and h.size), got %d in:\n%s", n, dump)
	}
}

func TestMethodsTakeSelfAsFirstParameter(t *testing.T) {
	dump := dumpFunction(t, `
		struct P { x: Int; }
		impl P { fn add(n: Int) -> Int { return self.x + n; } }`, "P.add")

	expectLines(t, dump, "fn P.add(%v1, %v2)", `GET_FIELD, %v1, "x"`)
}

func TestVariadicArgumentsArePacked(t *testing.T) {
	dump := dumpFunction(t, `
		fn sum(first: Int, rest: ...Int) -> Int { return first; }
		fn main() -> Int { return sum(1, 2, 3); }`, "main")

	// One fixed argument, then the rest in an array
	expectLines(t, dump, "NEW_ARRAY, 2", "PUSH %v", "= CALL, %v1, %v2, %v5")

	// Builtins take their arguments as they are
	dump = dumpFunction(t, `fn main() -> Int { print(1, 2); return 0; }`, "main")
	if strings.Contains(dump, "NEW_ARRAY") {
		t.Errorf("expected no array for a builtin call, got:\n%s", dump)
	}
}

func TestLengthAndForLoop(t *testing.T) {
	dump := dumpFunction(t, `fn main() -> Int { let xs = [1]; return __len(xs); }`, "main")
	expectLines(t, dump, "LEN, %v")

	// A for loop that was not desugared is still compiled as a real loop
	dump = dumpFunction(t, `fn main() -> Int { let t = 0; for v in [1, 2] { t = t + v; } return t; }`, "main")
	expectLines(t, dump, "LEN, %v", "LT, %v", "GET_INDEX, %v", "BR %v")
}

func TestClosureCaptureOperands(t *testing.T) {
	src := `
		fn main() -> Int {
			let a = 1;
			let outer = fn() -> Int {
				let inner = fn() -> Int { a = a + 1; return a; };
				return inner();
			};
			return outer();
		}`
	prog, errs := generateSource(t, src)
	if errs.HasErrors() {
		t.Fatalf("unexpected errors: %v", errs.Diagnostics)
	}

	closures := make(map[string][]ir.Value)
	upvalues := make(map[string]int)
	for _, fn := range prog.Functions {
		upvalues[fn.Name] = fn.UpvalueCount
		for _, bb := range fn.Blocks {
			for _, instr := range bb.Instructions {
				if instr.Op == ir.OpMakeClosure {
					closures[fn.Name] = instr.Operands[1:]
				}
			}
		}
	}

	// main hands its local to the outer lambda by register...
	if caps := closures["main"]; len(caps) != 1 {
		t.Fatalf("expected main to capture 1 variable, got %v", caps)
	} else if _, ok := caps[0].(*ir.Register); !ok {
		t.Errorf("expected a register capture in main, got %v", caps[0])
	}
	// ...which passes it on by upvalue index
	if caps := closures["lambda_0"]; len(caps) != 1 {
		t.Fatalf("expected the outer lambda to capture 1 variable, got %v", caps)
	} else if c, ok := caps[0].(ir.Constant); !ok || c.Type != "Upvalue" {
		t.Errorf("expected an upvalue capture in the outer lambda, got %v", caps[0])
	}

	for _, name := range []string{"lambda_0", "lambda_1"} {
		if upvalues[name] != 1 {
			t.Errorf("expected %s to have 1 upvalue, got %d", name, upvalues[name])
		}
	}
	if inner := dumpFunction(t, src, "lambda_1"); !strings.Contains(inner, "STORE_UPVALUE 0, %v") {
		t.Errorf("expected the inner lambda to write through its upvalue:\n%s", inner)
	}
}

func TestCastsAreCheckedOnlyWhenTheyCanFail(t *testing.T) {
	// Out of Any: the value may be anything, so the cast is verified
	dump := dumpFunction(t, `fn f(v: Any) -> Int { return v as Int; } fn main() -> Int { return f(1); }`, "f")
	expectLines(t, dump, `%v2 = MOV, "__cast"`, `%v3 = MOV, "int"`, `%v4 = CALL, %v2, %v1, %v3`, `RET %v4`)

	// Into a wider type: always succeeds, nothing is emitted
	dump = dumpFunction(t, `fn f(n: Int) -> Any { return n as Any; } fn main() -> Int { return 0; }`, "f")
	if strings.Contains(dump, "__cast") || !strings.Contains(dump, "RET %v1") {
		t.Errorf("expected the value to be returned as it is, got:\n%s", dump)
	}

	// To a trait: the descriptor lists the structs implementing it
	dump = dumpFunction(t, `
		trait T { fn m() -> Int; } struct A { x: Int; } struct B { x: Int; } struct C { x: Int; }
		impl T for B { fn m() -> Int { return 1; } } impl T for A { fn m() -> Int { return 2; } }
		fn f(v: Any) -> T { return v as T; } fn main() -> Int { return 0; }`, "f")
	expectLines(t, dump, `MOV, "{A|B}"`)

	// The same cast twice is two checks: each `as` stands on its own
	dump = dumpFunction(t, `fn f(v: Any) -> Int { return (v as Int) + (v as Int); } fn main() -> Int { return 0; }`, "f")
	if n := strings.Count(dump, "CALL"); n != 2 {
		t.Errorf("expected 2 cast calls, got %d in:\n%s", n, dump)
	}
}

func TestMethodsOfGenericStructs(t *testing.T) {
	src := `
		struct Box<T> { value: T; }
		impl<T> Box<T> { fn get() -> T { return self.value; } }
		fn main() -> Int { let b = Box<Int> { value: 1 }; return b.get(); }`

	// One function serves every Box<...>, and the call names it directly
	expectLines(t, dumpFunction(t, src, "Box.get"), "fn Box.get(%v1)", `GET_FIELD, %v1, "value"`)
	expectLines(t, dumpFunction(t, src, "main"), `NEW_STRUCT, "Box"`, `MOV, "Box.get"`)
}

func TestCompoundAssignmentEvaluatesItsTargetOnce(t *testing.T) {
	// A variable: its register is operand and destination
	dump := dumpFunction(t, `fn main() -> Int { let i = 1; i += 2; i++; return i; }`, "main")
	expectLines(t, dump, "%v1 = ADD, %v1, %v2", "%v1 = ADD, %v1, %v3", "RET %v1")

	// An element: array and index are computed once, then read and written
	src := `
		fn next() -> Int { return 0; }
		fn main() -> Int { let xs = [1]; xs[next()] *= 3; return xs[0]; }`
	dump = dumpFunction(t, src, "main")
	if n := strings.Count(dump, `MOV, "next"`); n != 1 {
		t.Errorf("expected next() to be called once, found %d calls in:\n%s", n, dump)
	}
	// %v2 is the array, %v4 the index returned by next()
	expectLines(t, dump, "%v5 = GET_INDEX, %v2, %v4", "%v7 = MUL, %v5, %v6", "SET_INDEX %v2, %v4, %v7")

	// A field: the object is computed once
	src = `
		struct P { n: Int; }
		fn make() -> P { return P { n: 1 }; }
		fn main() -> Int { make().n -= 4; return 0; }`
	dump = dumpFunction(t, src, "main")
	if n := strings.Count(dump, `MOV, "make"`); n != 1 {
		t.Errorf("expected make() to be called once, found %d calls in:\n%s", n, dump)
	}
	expectLines(t, dump, `GET_FIELD, %v2, "n"`, "SUB, %v", `SET_FIELD %v2, "n", %v`)

	// A captured variable: read, compute, write back through the upvalue
	src = `fn main() -> Int { let t = 0; let f = fn() -> Void { t += 5; }; f(); return t; }`
	expectLines(t, dumpFunction(t, src, "lambda_0"), "LOAD_UPVALUE, 0", "ADD, %v", "STORE_UPVALUE 0, %v")
}

func TestForLoopIndexIsACopyOfThePosition(t *testing.T) {
	// The fallback loop (not desugared here) keeps its own counter and
	// hands the body a copy of it
	dump := dumpFunction(t, `fn main() -> Int { let t = 0; for i, x in [5, 6] { t += i + x; } return t; }`, "main")

	// %v5 is the loop's counter; the body's `i` is the copy %v10
	expectLines(t, dump,
		"%v8 = LT, %v5, %v7",        // the condition uses the counter
		"%v9 = GET_INDEX, %v4, %v5", // so does fetching the element
		"%v10 = MOV, %v5",           // i is a copy
		"%v5 = ADD, %v5, %v6",       // the counter advances on its own
		"ADD, %v10, %v9",            // the body reads the copy
	)
}

func TestLoopIterationsDetachTheirClosures(t *testing.T) {
	// A variable declared in the loop body and captured: detached when the
	// iteration ends, at the back edge and at `continue`
	src := `
		fn main() -> Int {
			let fs: [fn() -> Int] = [];
			let i = 0;
			while i < 3 {
				let x = i * 2;
				i += 1;
				__push(fs, fn() -> Int { return x; });
				if x == 2 { continue; }
				i += 0;
			}
			return fs[0]();
		}`
	dump := dumpFunction(t, src, "main")
	if n := strings.Count(dump, "CLOSE_UPVALUES"); n != 2 {
		t.Fatalf("expected the closure to be detached at `continue` and at the end of the body, found %d in:\n%s", n, dump)
	}
	// It names the register the closure captured
	captured := dump[strings.Index(dump, "MAKE_CLOSURE"):]
	captured = captured[strings.Index(captured, "%v"):strings.Index(captured, "\n")]
	if !strings.Contains(dump, "CLOSE_UPVALUES "+captured) {
		t.Errorf("expected CLOSE_UPVALUES %s in:\n%s", captured, dump)
	}
	// ...and comes right before the jump back
	if !strings.Contains(dump, "CLOSE_UPVALUES "+captured+"\n\tJMP b1") {
		t.Errorf("expected the detach right before the jump to the condition, got:\n%s", dump)
	}

	nothingToDetach := []struct {
		name string
		src  string
	}{
		{"no closure in the loop", `fn main() -> Int { let t = 0; let i = 0; while i < 3 { let x = i; t += x; i += 1; } return t; }`},
		{"closure over a variable declared outside the loop", `fn main() -> Int {
			let total = 0; let i = 0;
			while i < 3 { let add = fn() -> Void { total += 1; }; add(); i += 1; }
			return total; }`},
		{"closure that captures nothing", `fn main() -> Int { let i = 0; while i < 3 { let f = fn() -> Int { return 1; }; i += f(); } return i; }`},
		{"closure outside any loop", `fn main() -> Int { let x = 1; let f = fn() -> Int { return x; }; return f(); }`},
	}
	for _, tt := range nothingToDetach {
		if dump := dumpFunction(t, tt.src, "main"); strings.Contains(dump, "CLOSE_UPVALUES") {
			t.Errorf("%s: expected nothing to detach, got:\n%s", tt.name, dump)
		}
	}

	// In nested loops the inner body's variable is detached by the inner
	// loop, and again by the outer one, whose body contains it
	nested := `
		fn main() -> Int {
			let fs: [fn() -> Int] = [];
			for a in [1, 2] { for b in [3, 4] { __push(fs, fn() -> Int { return a * b; }); } }
			return fs[0]();
		}`
	dump = dumpFunction(t, nested, "main")
	if n := strings.Count(dump, "CLOSE_UPVALUES"); n != 2 {
		t.Errorf("expected one detach per loop, found %d in:\n%s", n, dump)
	}
}

func TestNegativeLiteralsAreConstants(t *testing.T) {
	dump := dumpFunction(t, `fn main() -> Int { let a = -5; let b = -140737488355328; let c = -2.5; let d = 1; return -d; }`, "main")

	// No negation at runtime for literals; the smallest Int is expressible
	expectLines(t, dump, "%v1 = MOV, -5", "%v2 = MOV, -140737488355328", "%v3 = MOV, -2.5")
	if n := strings.Count(dump, "NEG"); n != 1 {
		t.Errorf("expected only `-d` to be negated at runtime, found %d NEG in:\n%s", n, dump)
	}
}

func TestInstructionsCarrySourcePositions(t *testing.T) {
	prog, _ := generateSource(t, "fn main() -> Int {\n  let a = 1;\n  return a + 2;\n}")

	lines := make(map[ir.OpType]int)
	for _, instr := range prog.Functions[0].EntryBlock.Instructions {
		lines[instr.Op] = instr.Pos.Line
	}
	if lines[ir.OpAdd] != 3 || lines[ir.OpRet] != 3 {
		t.Errorf("expected ADD and RET on line 3, got %d and %d", lines[ir.OpAdd], lines[ir.OpRet])
	}
}

func TestGenerationErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
	}{
		{"struct name as value", `struct P { x: Int; } fn main() -> Int { let p = P; return 0; }`, ErrNotAValue},
		{"assign to function", `fn f() -> Int { return 1; } fn main() -> Int { f = f; return 0; }`, ErrInvalidAssignment},
		{"break outside loop", `fn main() -> Void { break; }`, ErrLoopControl},
		{"continue outside loop", `fn main() -> Void { continue; }`, ErrLoopControl},
		{"match that was not desugared", `fn main() -> Int { match 1 { case 1: { return 1; } } return 0; }`, ErrUnsupportedConstruct},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := generateSource(t, tt.src)
			if len(errs.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic, got none")
			}
			if got := errs.Diagnostics[0].Code; got != tt.code {
				t.Errorf("expected code %s, got %s (%s)", tt.code, got, errs.Diagnostics[0].Message)
			}
		})
	}
}
