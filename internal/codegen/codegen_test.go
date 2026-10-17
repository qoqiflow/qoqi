package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func intConst(n int64) ir.Constant    { return ir.Constant{Val: n, Type: "Int"} }
func funcRef(name string) ir.Constant { return ir.Constant{Val: name, Type: "FuncRef"} }

// generate lowers the functions to bytecode and fails the test on any error.
func generate(t *testing.T, fns ...*ir.Function) *vm.Chunk {
	t.Helper()
	errs := errors.NewHandler()
	chunk := NewGenerator(&ir.Program{Functions: fns}, []string{"print", "toString"}, errs).Generate()
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic: %s: %s", d.Code, d.Message)
	}
	if t.Failed() {
		t.FailNow()
	}
	return chunk
}

// run executes a chunk and returns what main returned.
func run(t *testing.T, chunk *vm.Chunk) vm.Value {
	t.Helper()
	result, err := vm.NewVM().Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	return result
}

func expectInt(t *testing.T, got vm.Value, want int64) {
	t.Helper()
	if !got.IsInt() || got.AsInt() != want {
		t.Errorf("expected Int %d, got %s", want, vm.FormatValue(got))
	}
}

func proto(t *testing.T, chunk *vm.Chunk, name string) vm.FunctionPrototype {
	t.Helper()
	idx := chunk.FindPrototype(name)
	if idx < 0 {
		t.Fatalf("function %s not found in chunk", name)
	}
	return chunk.Prototypes[idx]
}

func TestGenerateConstants(t *testing.T) {
	tests := []struct {
		name  string
		value ir.Constant
		check func(t *testing.T, v vm.Value)
	}{
		{"small int", intConst(7), func(t *testing.T, v vm.Value) { expectInt(t, v, 7) }},
		{"large int", intConst(100000), func(t *testing.T, v vm.Value) { expectInt(t, v, 100000) }},
		{"negative int", intConst(-3), func(t *testing.T, v vm.Value) { expectInt(t, v, -3) }},
		{"float", ir.Constant{Val: 2.5, Type: "Float"}, func(t *testing.T, v vm.Value) {
			if !v.IsNumber() || v.AsNumber() != 2.5 {
				t.Errorf("expected 2.5, got %s", vm.FormatValue(v))
			}
		}},
		{"bool", ir.Constant{Val: true, Type: "Bool"}, func(t *testing.T, v vm.Value) {
			if !v.IsBool() || !v.AsBool() {
				t.Errorf("expected true, got %s", vm.FormatValue(v))
			}
		}},
		{"null", ir.Constant{Val: nil, Type: "Null"}, func(t *testing.T, v vm.Value) {
			if !v.IsNull() {
				t.Errorf("expected null, got %s", vm.FormatValue(v))
			}
		}},
		{"string", ir.Constant{Val: "hi", Type: "String"}, func(t *testing.T, v vm.Value) {
			if got := vm.FormatValue(v); got != "hi" {
				t.Errorf("expected hi, got %s", got)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := ir.NewBuilder("main")
			b.CreateRet(b.CreateMov(tt.value))
			tt.check(t, run(t, generate(t, b.Func)))
		})
	}
}

func TestGenerateConstantsAreShared(t *testing.T) {
	b := ir.NewBuilder("main")
	b.CreateMov(intConst(100000))
	b.CreateMov(intConst(100000))
	b.CreateMov(ir.Constant{Val: "name", Type: "String"})
	obj := b.CreateNewStruct("T")
	b.CreateRet(b.CreateGetField(obj, "name"))

	chunk := generate(t, b.Func)
	// One slot for the int, one for the string used as value and field
	// name, one for the struct's name
	if len(chunk.Constants) != 3 || len(chunk.StringPool) != 2 {
		t.Errorf("expected 3 constants and 2 pooled strings, got %d and %d", len(chunk.Constants), len(chunk.StringPool))
	}
}

func TestGenerateBranchesAndLoops(t *testing.T) {
	// sum = 0; i = 0; while i < 5 { sum = sum + i; i = i + 1 }; return sum
	b := ir.NewBuilder("main")
	sum := b.CreateMov(intConst(0))
	i := b.CreateMov(intConst(0))
	cond, loop, exit := b.NewBlock(), b.NewBlock(), b.NewBlock()
	b.CreateJmp(cond)

	b.SetBlock(cond)
	inRange := b.NewRegister()
	b.Emit(ir.OpLt, inRange, i, b.CreateMov(intConst(5)))
	b.CreateBr(inRange, loop, exit)

	b.SetBlock(loop)
	b.Emit(ir.OpAdd, sum, sum, i)
	b.Emit(ir.OpAdd, i, i, b.CreateMov(intConst(1)))
	b.CreateJmp(cond)

	b.SetBlock(exit)
	b.CreateRet(sum)

	expectInt(t, run(t, generate(t, b.Func)), 10)
}

func TestGenerateCallKeepsArgumentsAcrossCalls(t *testing.T) {
	// The VM returns every call result in r0, which is also where the first
	// argument arrives. sub(a, b) calls id twice and must still see a and b.
	id := ir.NewBuilder("id")
	id.CreateRet(id.AddParam())

	sub := ir.NewBuilder("sub")
	a, bParam := sub.AddParam(), sub.AddParam()
	x := sub.CreateCall(sub.CreateMov(funcRef("id")), a)
	y := sub.CreateCall(sub.CreateMov(funcRef("id")), bParam)
	sub.CreateRet(sub.CreateSub(sub.CreateSub(x, y), sub.CreateSub(a, a)))

	main := ir.NewBuilder("main")
	main.CreateRet(main.CreateCall(main.CreateMov(funcRef("sub")), main.CreateMov(intConst(9)), main.CreateMov(intConst(4))))

	chunk := generate(t, id.Func, sub.Func, main.Func)
	expectInt(t, run(t, chunk), 5)

	if got := proto(t, chunk, "sub").ArgCount; got != 2 {
		t.Errorf("expected sub to take 2 arguments, got %d", got)
	}
}

func TestGenerateExternCall(t *testing.T) {
	b := ir.NewBuilder("main")
	text := b.CreateCall(b.CreateMov(funcRef("toString")), b.CreateMov(intConst(42)))
	b.CreateRet(text)

	chunk := generate(t, b.Func)
	if got := vm.FormatValue(run(t, chunk)); got != "42" {
		t.Errorf("expected \"42\", got %q", got)
	}
	if len(chunk.ExternNames) != 1 || chunk.ExternNames[0] != "toString" {
		t.Errorf("expected extern table [toString], got %v", chunk.ExternNames)
	}
}

func TestGenerateClosureCapturesByReference(t *testing.T) {
	// inc() { upvalue0 = upvalue0 + 1; return upvalue0 }
	inc := ir.NewBuilder("inc")
	inc.Func.UpvalueCount = 1
	next := inc.CreateAdd(inc.CreateLoadUpvalue(0), inc.CreateMov(intConst(1)))
	inc.CreateStoreUpvalue(0, next)
	inc.CreateRet(next)

	// main: count = 10; f = closure(inc, count); f(); f(); return count
	main := ir.NewBuilder("main")
	count := main.CreateMov(intConst(10))
	f := main.CreateMakeClosure(funcRef("inc"), count)
	main.CreateCall(f)
	main.CreateCall(f)
	main.CreateRet(count)

	chunk := generate(t, inc.Func, main.Func)
	expectInt(t, run(t, chunk), 12)

	if got := proto(t, chunk, "inc").UpvalueCount; got != 1 {
		t.Errorf("expected inc to have 1 upvalue, got %d", got)
	}
}

func TestGenerateArraysAndStructs(t *testing.T) {
	b := ir.NewBuilder("main")
	arr := b.NewRegister()
	b.Emit(ir.OpNewArray, arr, ir.Constant{Val: 2, Type: "Int"})
	b.Emit(ir.OpPush, nil, arr, b.CreateMov(intConst(5)))
	b.Emit(ir.OpPush, nil, arr, b.CreateMov(intConst(6)))
	b.Emit(ir.OpSetIndex, nil, arr, b.CreateMov(intConst(0)), b.CreateMov(intConst(30)))

	obj := b.CreateNewStruct("Point")
	b.Emit(ir.OpSetField, nil, obj, ir.Constant{Val: "x", Type: "String"}, b.CreateMov(intConst(4)))

	first := b.NewRegister()
	b.Emit(ir.OpGetIndex, first, arr, b.CreateMov(intConst(0)))
	length := b.NewRegister()
	b.Emit(ir.OpLen, length, arr)

	// 30 + 2 + 4
	b.CreateRet(b.CreateAdd(b.CreateAdd(first, length), b.CreateGetField(obj, "x")))

	expectInt(t, run(t, generate(t, b.Func)), 36)
}

func TestGenerateReusesTemporaryRegisters(t *testing.T) {
	// A long chain of single-use values must not need a register each
	b := ir.NewBuilder("main")
	acc := b.CreateMov(intConst(0))
	for i := 0; i < 400; i++ {
		acc = b.CreateAdd(acc, b.CreateMov(intConst(1)))
	}
	b.CreateRet(acc)

	chunk := generate(t, b.Func)
	expectInt(t, run(t, chunk), 400)

	if got := proto(t, chunk, "main").RegisterCount; got > 8 {
		t.Errorf("expected temporaries to share registers, function uses %d", got)
	}
}

func TestGenerateSkipsUnreachableBlocks(t *testing.T) {
	b := ir.NewBuilder("main")
	b.CreateRet(b.CreateMov(intConst(1)))
	// Code after a return lands in a block nothing jumps to
	b.SetBlock(b.NewBlock())
	b.CreateRet(b.CreateMov(intConst(2)))

	chunk := generate(t, b.Func)
	expectInt(t, run(t, chunk), 1)
	if got := proto(t, chunk, "main").CodeSize; got != 2 {
		t.Errorf("expected 2 instructions (load, return), got %d", got)
	}
}

func TestGenerateDebugInfo(t *testing.T) {
	b := ir.NewBuilder("main")
	b.Pos.File, b.Pos.Line, b.Pos.Col = "prog.qi", 3, 5
	b.CreateRet(b.CreateMov(intConst(1)))

	errs := errors.NewHandler()
	gen := NewGenerator(&ir.Program{Functions: []*ir.Function{b.Func}}, nil, errs)
	gen.SetSourceLines(map[int]string{3: "    return 1;"})
	chunk := gen.Generate()

	info := chunk.GetDebugInfo(0)
	if info == nil {
		t.Fatal("expected debug info on the first instruction")
	}
	if info.FilePath != "prog.qi" || info.Line != 3 || info.Column != 5 {
		t.Errorf("unexpected location: %s:%d:%d", info.FilePath, info.Line, info.Column)
	}
	if info.SourceLine != "    return 1;" || info.FuncName != "main" {
		t.Errorf("unexpected context: %q in %q", info.SourceLine, info.FuncName)
	}
}

func TestGenerateKeepsFunctionSignatures(t *testing.T) {
	typed := ir.NewBuilder("main")
	typed.Func.Signature = "fn()->int"
	typed.CreateMov(funcRef("helper"))
	typed.CreateRet(typed.CreateMov(intConst(1)))

	untyped := ir.NewBuilder("helper")
	untyped.CreateRet(nil)

	chunk := generate(t, typed.Func, untyped.Func)
	if got := proto(t, chunk, "main").Signature; got != "fn()->int" {
		t.Errorf("expected main to keep its signature, got %q", got)
	}
	if got := proto(t, chunk, "helper").Signature; got != "" {
		t.Errorf("expected helper to have no signature, got %q", got)
	}
}

func TestGenerateErrors(t *testing.T) {
	ret := func(name string, params int) *ir.Function {
		b := ir.NewBuilder(name)
		for i := 0; i < params; i++ {
			b.AddParam()
		}
		b.CreateRet(nil)
		return b.Func
	}

	tooManyRegisters := ir.NewBuilder("main")
	var live []*ir.Register
	for i := 0; i < maxRegisters+10; i++ {
		live = append(live, tooManyRegisters.CreateMov(intConst(1)))
	}
	// Reading every value in another block keeps all of them alive at once
	tail := tooManyRegisters.NewBlock()
	tooManyRegisters.CreateJmp(tail)
	tooManyRegisters.SetBlock(tail)
	sum := live[0]
	for _, reg := range live[1:] {
		sum = tooManyRegisters.CreateAdd(sum, reg)
	}
	tooManyRegisters.CreateRet(sum)

	tooManyConstants := ir.NewBuilder("main")
	for i := 0; i < maxConstants+10; i++ {
		tooManyConstants.CreateMov(intConst(int64(1_000_000 + i)))
	}
	tooManyConstants.CreateRet(nil)

	tooManyFunctions := []*ir.Function{ret("main", 0)}
	for i := 0; i < maxFunctions; i++ {
		tooManyFunctions = append(tooManyFunctions, ret(fmt.Sprintf("f%d", i), 0))
	}

	undefined := ir.NewBuilder("main")
	undefined.CreateRet(undefined.CreateMov(funcRef("nowhere")))

	unsupported := ir.NewBuilder("main")
	unsupported.CreateRet(unsupported.CreateCallTrait(unsupported.CreateMov(intConst(1)), "T", "m"))

	tests := []struct {
		name string
		fns  []*ir.Function
		code string
		msg  string
	}{
		{"no main", []*ir.Function{ret("helper", 0)}, ErrMissingMain, "no 'main' function"},
		{"main with parameters", []*ir.Function{ret("main", 1)}, ErrMissingMain, "must not take parameters"},
		{"too many registers", []*ir.Function{tooManyRegisters.Func}, ErrTooManyRegisters, "registers"},
		{"too many constants", []*ir.Function{tooManyConstants.Func}, ErrTooManyConstants, "distinct constants"},
		{"too many functions", tooManyFunctions, ErrTooManyFunctions, "functions, the limit is 65536"},
		{"undefined function", []*ir.Function{undefined.Func}, ErrUndefinedFunc, "undefined function 'nowhere'"},
		{"unsupported operation", []*ir.Function{unsupported.Func}, ErrUnsupportedIR, "CALL_TRAIT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			NewGenerator(&ir.Program{Functions: tt.fns}, nil, errs).Generate()

			if !errs.HasErrors() {
				t.Fatal("expected an error, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code {
				t.Errorf("expected code %s, got %s (%s)", tt.code, d.Code, d.Message)
			}
			if !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected message containing %q, got %q", tt.msg, d.Message)
			}
		})
	}
}

// The limits used to be 256 registers per function and 256 constants and
// functions per program. Programs well past those compile and run.

func TestGenerateManyLiveRegisters(t *testing.T) {
	const count = 1000

	b := ir.NewBuilder("main")
	var live []*ir.Register
	for i := 0; i < count; i++ {
		live = append(live, b.CreateMov(intConst(int64(i))))
	}
	// Reading every value in another block keeps all of them alive at once
	tail := b.NewBlock()
	b.CreateJmp(tail)
	b.SetBlock(tail)
	sum := live[0]
	for _, reg := range live[1:] {
		sum = b.CreateAdd(sum, reg)
	}
	// A call from a function this large has its window above register 255
	b.CreateRet(b.CreateCall(b.CreateMov(funcRef("double")), sum))

	double := ir.NewBuilder("double")
	param := double.AddParam()
	double.CreateRet(double.CreateAdd(param, param))

	chunk := generate(t, b.Func, double.Func)
	if got := proto(t, chunk, "main").RegisterCount; got <= 256 {
		t.Errorf("expected main to need more than 256 registers, it has %d", got)
	}
	expectInt(t, run(t, chunk), count*(count-1))
}

func TestGenerateManyConstants(t *testing.T) {
	const count = 2000

	// Values above the range of LOADI, so each one takes a pool entry
	b := ir.NewBuilder("main")
	sum := b.CreateMov(intConst(0))
	want := int64(0)
	for i := 0; i < count; i++ {
		value := int64(1_000_000 + i)
		sum = b.CreateAdd(sum, b.CreateMov(intConst(value)))
		want += value
	}
	b.CreateRet(sum)

	chunk := generate(t, b.Func)
	if len(chunk.Constants) != count {
		t.Errorf("expected %d constants, got %d", count, len(chunk.Constants))
	}
	// Temporaries are reused, so the constants do not cost registers
	if got := proto(t, chunk, "main").RegisterCount; got > 16 {
		t.Errorf("expected a handful of registers, got %d", got)
	}
	expectInt(t, run(t, chunk), want)
}

func TestGenerateSmallIntsNeedNoConstant(t *testing.T) {
	b := ir.NewBuilder("main")
	b.CreateRet(b.CreateAdd(b.CreateMov(intConst(vm.MaxOperand)), b.CreateMov(intConst(256))))

	chunk := generate(t, b.Func)
	if len(chunk.Constants) != 0 {
		t.Errorf("expected immediates to be used up to %d, got %d constants", vm.MaxOperand, len(chunk.Constants))
	}
	expectInt(t, run(t, chunk), vm.MaxOperand+256)

	// One past the range goes to the pool
	b = ir.NewBuilder("main")
	b.CreateRet(b.CreateMov(intConst(vm.MaxOperand + 1)))
	chunk = generate(t, b.Func)
	if len(chunk.Constants) != 1 {
		t.Errorf("expected one constant, got %d", len(chunk.Constants))
	}
	expectInt(t, run(t, chunk), vm.MaxOperand+1)
}

func TestGenerateManyFunctions(t *testing.T) {
	const count = 1000

	// f0 returns its argument; f(i) returns f(i-1)(n) + i. Every function
	// refers to its neighbour, so indexes above 255 are both defined and used.
	fns := make([]*ir.Function, 0, count+1)
	main := ir.NewBuilder("main")
	main.CreateRet(main.CreateCall(main.CreateMov(funcRef(fmt.Sprintf("f%d", count-1))), main.CreateMov(intConst(5))))
	fns = append(fns, main.Func)

	for i := 0; i < count; i++ {
		b := ir.NewBuilder(fmt.Sprintf("f%d", i))
		n := b.AddParam()
		if i == 0 {
			b.CreateRet(n)
		} else {
			below := b.CreateCall(b.CreateMov(funcRef(fmt.Sprintf("f%d", i-1))), n)
			b.CreateRet(b.CreateAdd(below, b.CreateMov(intConst(int64(i)))))
		}
		fns = append(fns, b.Func)
	}

	chunk := generate(t, fns...)
	if len(chunk.Prototypes) != count+1 {
		t.Errorf("expected %d functions, got %d", count+1, len(chunk.Prototypes))
	}
	expectInt(t, run(t, chunk), 5+count*(count-1)/2)
}

// Only what main can come to run is compiled: a function nothing refers
// to is left out, and one reached through a closure or through a function
// that is itself only reached from another is kept.
func TestGenerateLeavesOutWhatIsNeverReached(t *testing.T) {
	ret := func(name string, refs ...ir.Value) *ir.Function {
		b := ir.NewBuilder(name)
		for _, ref := range refs {
			b.CreateMov(ref)
		}
		b.CreateRet(b.CreateMov(intConst(1)))
		return b.Func
	}

	main := ir.NewBuilder("main")
	main.CreateMov(funcRef("direct"))
	main.CreateMakeClosure(funcRef("captured"))
	// A text spelled like a function is not a reference to it
	main.CreateMov(ir.Constant{Val: "spelled", Type: "String"})
	main.CreateRet(main.CreateMov(intConst(0)))

	// After a return nothing runs: what only that code names is not reached
	early := ir.NewBuilder("direct")
	early.CreateMov(funcRef("indirect"))
	early.CreateRet(early.CreateMov(intConst(1)))
	early.CreateMov(funcRef("afterReturn"))

	chunk := generate(t,
		ret("unused", funcRef("onlyFromUnused")),
		main.Func,
		ret("spelled"),
		early.Func,
		ret("indirect", funcRef("direct")), // A cycle ends
		ret("captured"),
		ret("onlyFromUnused"),
		ret("afterReturn"),
	)

	var names []string
	for _, p := range chunk.Prototypes {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, " "); got != "main direct indirect captured" {
		t.Errorf("expected main and what it reaches, in their order, got: %s", got)
	}
	// What is kept still refers to the right functions by their new numbers
	if err := vm.Verify(chunk); err != nil {
		t.Errorf("the chunk does not verify: %v", err)
	}
	for _, instr := range chunk.Code {
		if instr.Op == vm.OpNEWFUNC && int(instr.B) >= len(chunk.Prototypes) {
			t.Errorf("a function is referred to by the number %d of %d", instr.B, len(chunk.Prototypes))
		}
	}
}

// Without a main there is nowhere to start from: the error says so, and
// is the only one.
func TestGenerateWithoutMainKeepsEverything(t *testing.T) {
	b := ir.NewBuilder("helper")
	b.CreateRet(b.CreateMov(intConst(1)))

	errs := errors.NewHandler()
	chunk := NewGenerator(&ir.Program{Functions: []*ir.Function{b.Func}}, nil, errs).Generate()
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != ErrMissingMain {
		t.Errorf("expected the missing entry point to be reported, got %v", errs.Diagnostics)
	}
	if len(chunk.Prototypes) != 1 {
		t.Errorf("expected the function to be compiled all the same, got %d", len(chunk.Prototypes))
	}
}
