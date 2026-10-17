package codegen

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// One instruction where there were several: calls of a function known by
// its name, values made where a call wants them, tests that jump, small
// numbers written in the instruction. Each test checks what the function
// computes, then how it was written.

// count returns how many instructions of a function have one of the ops.
func count(chunk *vm.Chunk, function string, ops ...vm.OpCode) int {
	idx := chunk.FindPrototype(function)
	if idx < 0 {
		return -1
	}
	p := chunk.Prototypes[idx]
	n := 0
	for ip := p.CodeStart; ip < p.CodeStart+p.CodeSize; ip++ {
		for _, op := range ops {
			if chunk.Code[ip].Op == op {
				n++
			}
		}
	}
	return n
}

func expectCount(t *testing.T, chunk *vm.Chunk, function string, want int, ops ...vm.OpCode) {
	t.Helper()
	if got := count(chunk, function, ops...); got != want {
		t.Errorf("%s: expected %d of %v, found %d", function, want, ops, got)
	}
}

// binary makes "fn name(a, b) { return a op b; }".
func binary(name string, op ir.OpType) *ir.Function {
	b := ir.NewBuilder(name)
	x, y := b.AddParam(), b.AddParam()
	dest := b.NewRegister()
	b.Emit(op, dest, x, y)
	b.CreateRet(dest)
	return b.Func
}

func TestCallsOfNamedFunctionsNeedNoFunctionValue(t *testing.T) {
	// main: return sub(add(20, 3), 5) + twice(4), with a host function
	// called on the way
	main := ir.NewBuilder("main")
	sum := main.CreateCall(main.CreateMov(funcRef("add")), main.CreateMov(intConst(20)), main.CreateMov(intConst(3)))
	diff := main.CreateCall(main.CreateMov(funcRef("sub")), sum, main.CreateMov(intConst(5)))
	main.CreateCall(main.CreateMov(funcRef("toString")), diff)
	double := main.CreateCall(main.CreateMov(funcRef("twice")), main.CreateMov(intConst(4)))
	main.CreateRet(main.CreateAdd(diff, double))

	twice := ir.NewBuilder("twice")
	n := twice.AddParam()
	twice.CreateRet(twice.CreateAdd(n, n))

	chunk := generate(t, main.Func, binary("add", ir.OpAdd), binary("sub", ir.OpSub), twice.Func)
	expectInt(t, run(t, chunk), 26)

	expectCount(t, chunk, "main", 3, vm.OpCALLK)
	expectCount(t, chunk, "main", 1, vm.OpCALLX)
	expectCount(t, chunk, "main", 0, vm.OpCALL, vm.OpNEWFUNC, vm.OpNEWEXFUNC)
	// A function that calls nothing keeps its first argument where it came
	expectCount(t, chunk, "twice", 0, vm.OpMOV)
	if err := vm.Verify(chunk); err != nil {
		t.Errorf("does not verify: %v", err)
	}
}

// A function held in a variable that is passed around is a value like any
// other, made as one; where the variable is called, and holds nothing but
// that function, the call is of the function by its number. A parameter
// holds whatever the caller gave, and is called as a value.
func TestCallsOfFunctionValuesStayCalls(t *testing.T) {
	main := ir.NewBuilder("main")
	// Read twice: called, and returned from a helper that calls it again
	held := main.CreateMov(funcRef("seven"))
	first := main.CreateCall(held)
	second := main.CreateCall(main.CreateMov(funcRef("apply")), held)
	main.CreateRet(main.CreateAdd(first, second))

	apply := ir.NewBuilder("apply")
	f := apply.AddParam()
	apply.CreateRet(apply.CreateCall(f))

	seven := ir.NewBuilder("seven")
	seven.CreateRet(seven.CreateMov(intConst(7)))

	chunk := generate(t, main.Func, apply.Func, seven.Func)
	expectInt(t, run(t, chunk), 14)
	expectCount(t, chunk, "main", 1, vm.OpNEWFUNC)
	expectCount(t, chunk, "main", 0, vm.OpCALL)
	expectCount(t, chunk, "main", 2, vm.OpCALLK)
	expectCount(t, chunk, "apply", 1, vm.OpCALL)
}

// A function that captures variables is a new value each time it is made:
// it is never called by its number.
func TestClosuresAreNotCalledByNumber(t *testing.T) {
	main := ir.NewBuilder("main")
	captured := main.CreateMov(intConst(40))
	closure := main.CreateMakeClosure(funcRef("addCaptured"), captured)
	main.CreateRet(main.CreateCall(closure, main.CreateMov(intConst(2))))

	inner := ir.NewBuilder("addCaptured")
	inner.Func.UpvalueCount = 1
	n := inner.AddParam()
	inner.CreateRet(inner.CreateAdd(n, inner.CreateLoadUpvalue(0)))

	chunk := generate(t, main.Func, inner.Func)
	expectInt(t, run(t, chunk), 42)
	expectCount(t, chunk, "main", 0, vm.OpCALLK)
	expectCount(t, chunk, "main", 1, vm.OpCALL)
}

// What is worked out for one call is worked out in its place; what has to
// live through another call is kept elsewhere and moved, or the second
// call would write its own arguments over it.
func TestArgumentsAreMadeWhereTheCallWantsThem(t *testing.T) {
	main := ir.NewBuilder("main")
	x := main.CreateMov(intConst(100000)) // Too large for an instruction: a register of its own
	// sub(x + 7, x - 30000): both made for the call, nothing in between
	left := main.CreateAdd(x, main.CreateMov(intConst(7)))
	right := main.CreateSub(x, main.CreateMov(intConst(30000)))
	plain := main.CreateCall(main.CreateMov(funcRef("sub")), left, right)

	// sub(x + 1, sub(x, 5)): the first argument has to outlive the inner call
	early := main.CreateAdd(x, main.CreateMov(intConst(1)))
	inner := main.CreateCall(main.CreateMov(funcRef("sub")), x, main.CreateMov(intConst(5)))
	nested := main.CreateCall(main.CreateMov(funcRef("sub")), early, inner)
	main.CreateRet(main.CreateAdd(plain, nested))

	chunk := generate(t, main.Func, binary("sub", ir.OpSub))
	// (100007 - 70000) + (100001 - 99995)
	expectInt(t, run(t, chunk), 30007+6)

	// Moves: the result of the plain call, which the two others must not
	// overwrite; x into the inner call; the early argument and the inner
	// result into the outer one. The arguments of the plain call need none.
	expectCount(t, chunk, "main", 4, vm.OpMOV)
	expectCount(t, chunk, "main", 2, vm.OpADDI)
	expectCount(t, chunk, "main", 1, vm.OpSUBI)
}

// The result of a call is in r0 until the next call: read once before
// that, it is read from there.
func TestResultsAreReadWhereTheCallLeftThem(t *testing.T) {
	main := ir.NewBuilder("main")
	a := main.CreateCall(main.CreateMov(funcRef("seven")))
	b := main.CreateCall(main.CreateMov(funcRef("seven"))) // a lives through this call: moved
	main.CreateRet(main.CreateAdd(a, b))                   // b is read at once: not moved

	seven := ir.NewBuilder("seven")
	seven.CreateRet(seven.CreateMov(intConst(7)))

	chunk := generate(t, main.Func, seven.Func)
	expectInt(t, run(t, chunk), 14)
	expectCount(t, chunk, "main", 1, vm.OpMOV)

	// A result nothing reads is not moved either
	unread := ir.NewBuilder("main")
	unread.CreateCall(unread.CreateMov(funcRef("seven")))
	unread.CreateRet(unread.CreateMov(intConst(1)))
	chunk = generate(t, unread.Func, seven.Func)
	expectInt(t, run(t, chunk), 1)
	expectCount(t, chunk, "main", 0, vm.OpMOV)
}

// Every test, both ways round, against a register and against a number on
// either side, for values on each side of the line: the branch taken is
// the one the test says.
func TestTestsThatJump(t *testing.T) {
	holds := map[ir.OpType]func(a, b int64) bool{
		ir.OpLt:  func(a, b int64) bool { return a < b },
		ir.OpLte: func(a, b int64) bool { return a <= b },
		ir.OpGt:  func(a, b int64) bool { return a > b },
		ir.OpGte: func(a, b int64) bool { return a >= b },
		ir.OpEq:  func(a, b int64) bool { return a == b },
		ir.OpNeq: func(a, b int64) bool { return a != b },
	}
	// pick(a, b) returns 1 if the test holds and 2 if not. A value too
	// large for an instruction stays in a register; a small one is
	// written in the test.
	build := func(op ir.OpType, a, b int64) *ir.Function {
		f := ir.NewBuilder("main")
		yes, no := f.NewBlock(), f.NewBlock()
		left, right := f.CreateMov(intConst(a)), f.CreateMov(intConst(b))
		cond := f.NewRegister()
		f.Emit(op, cond, left, right)
		f.CreateBr(cond, yes, no)
		f.SetBlock(yes)
		f.CreateRet(f.CreateMov(intConst(1)))
		f.SetBlock(no)
		f.CreateRet(f.CreateMov(intConst(2)))
		return f.Func
	}

	const large = 70000 // More than an operand holds
	for op, test := range holds {
		for _, pair := range [][2]int64{{3, 5}, {5, 3}, {4, 4}, {0, 0}, {large, 5}, {5, large}, {large, large}, {large, large + 1}, {-2, 1}, {1, -2}} {
			a, b := pair[0], pair[1]
			chunk := generate(t, build(op, a, b))
			want := int64(2)
			if test(a, b) {
				want = 1
			}
			result := run(t, chunk)
			if !result.IsInt() || result.AsInt() != want {
				t.Errorf("%d %s %d: took branch %s, expected %d", a, op, b, vm.FormatValue(result), want)
			}
			// The test is the jump: no value of it is made
			expectCount(t, chunk, "main", 0, vm.OpLT, vm.OpLE, vm.OpGT, vm.OpGE, vm.OpEQ, vm.OpNE, vm.OpJMPIF, vm.OpJMPIFNOT, vm.OpLONG)
			if err := vm.Verify(chunk); err != nil {
				t.Errorf("%d %s %d does not verify: %v", a, op, b, err)
			}
		}
	}
}

// A test whose result is also kept is a value: it is made, and the branch
// reads it.
func TestATestThatIsAlsoAValueIsMade(t *testing.T) {
	f := ir.NewBuilder("main")
	yes, no := f.NewBlock(), f.NewBlock()
	cond := f.NewRegister()
	f.Emit(ir.OpLt, cond, f.CreateMov(intConst(1)), f.CreateMov(intConst(2)))
	f.CreateBr(cond, yes, no)
	f.SetBlock(yes)
	f.CreateRet(cond) // Read again: true
	f.SetBlock(no)
	f.CreateRet(f.CreateMov(intConst(0)))

	chunk := generate(t, f.Func)
	if result := run(t, chunk); !result.IsBool() || !result.AsBool() {
		t.Errorf("expected true, got %s", vm.FormatValue(result))
	}
	expectCount(t, chunk, "main", 1, vm.OpLT)
	expectCount(t, chunk, "main", 1, vm.OpJMPIFNOT)
}

func TestSmallNumbersAreWrittenInTheInstruction(t *testing.T) {
	cases := []struct {
		name  string
		build func(f *ir.Builder, x *ir.Register) *ir.Register
		want  int64
		op    vm.OpCode
		times int
	}{
		{"x + 5", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateAdd(x, f.CreateMov(intConst(5))) }, 100005, vm.OpADDI, 1},
		{"5 + x", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateAdd(f.CreateMov(intConst(5)), x) }, 100005, vm.OpADDI, 1},
		{"x - 5", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateSub(x, f.CreateMov(intConst(5))) }, 99995, vm.OpSUBI, 1},
		// Not the same thing the other way round
		{"5 - x", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateSub(f.CreateMov(intConst(5)), x) }, -99995, vm.OpSUBI, 0},
		{"x + 65535", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateAdd(x, f.CreateMov(intConst(65535))) }, 165535, vm.OpADDI, 1},
		// Too large, or negative: a register
		{"x + 65536", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateAdd(x, f.CreateMov(intConst(65536))) }, 165536, vm.OpADDI, 0},
		{"x + -1", func(f *ir.Builder, x *ir.Register) *ir.Register { return f.CreateAdd(x, f.CreateMov(intConst(-1))) }, 99999, vm.OpADDI, 0},
		// The number is read twice: it stays a value
		{"k + k", func(f *ir.Builder, x *ir.Register) *ir.Register {
			k := f.CreateMov(intConst(5))
			return f.CreateAdd(f.CreateAdd(x, k), k)
		}, 100010, vm.OpADDI, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ir.NewBuilder("main")
			x := f.CreateMov(intConst(100000))
			f.CreateRet(c.build(f, x))
			chunk := generate(t, f.Func)
			expectInt(t, run(t, chunk), c.want)
			expectCount(t, chunk, "main", c.times, c.op)
		})
	}
}

// A loop counted down by a test that jumps and a small number: what most
// loops are.
func TestALoopIsThreeInstructions(t *testing.T) {
	f := ir.NewBuilder("main")
	head, body, done := f.NewBlock(), f.NewBlock(), f.NewBlock()
	sum := f.CreateMov(intConst(0))
	i := f.CreateMov(intConst(0))
	f.CreateJmp(head)

	f.SetBlock(head)
	cond := f.NewRegister()
	f.Emit(ir.OpLt, cond, i, f.CreateMov(intConst(100)))
	f.CreateBr(cond, body, done)

	f.SetBlock(body)
	f.Emit(ir.OpAdd, sum, sum, i)
	f.Emit(ir.OpAdd, i, i, f.CreateMov(intConst(1)))
	f.CreateJmp(head)

	f.SetBlock(done)
	f.CreateRet(sum)

	chunk := generate(t, f.Func)
	expectInt(t, run(t, chunk), 4950)
	// Two to start, the test, two additions, the jump back, the return
	if p := proto(t, chunk, "main"); p.CodeSize != 7 {
		t.Errorf("expected 7 instructions, got %d", p.CodeSize)
	}
	expectCount(t, chunk, "main", 1, vm.OpJNLTI)
	expectCount(t, chunk, "main", 1, vm.OpADDI)
	expectCount(t, chunk, "main", 1, vm.OpJMP)
}

// A function that lies past what an operand can address is written with
// long jumps, and does the same.
func TestAFunctionFarIntoTheProgramUsesLongJumps(t *testing.T) {
	// filler: 68000 additions of a constant, one instruction each (ADDK),
	// so that what follows it starts beyond 65535
	filler := ir.NewBuilder("filler")
	acc := filler.CreateMov(intConst(100000))
	for i := 0; i < 68000; i++ {
		filler.Emit(ir.OpAdd, acc, acc, filler.CreateMov(intConst(100000)))
	}
	filler.CreateRet(acc)

	// countTo(n): a loop, a test, a call
	loop := ir.NewBuilder("countTo")
	n := loop.AddParam()
	head, body, done := loop.NewBlock(), loop.NewBlock(), loop.NewBlock()
	sum := loop.CreateMov(intConst(0))
	i := loop.CreateMov(intConst(0))
	loop.CreateJmp(head)
	loop.SetBlock(head)
	cond := loop.NewRegister()
	loop.Emit(ir.OpLt, cond, i, n)
	loop.CreateBr(cond, body, done)
	loop.SetBlock(body)
	loop.Emit(ir.OpAdd, sum, sum, loop.CreateCall(loop.CreateMov(funcRef("double")), i))
	loop.Emit(ir.OpAdd, i, i, loop.CreateMov(intConst(1)))
	loop.CreateJmp(head)
	loop.SetBlock(done)
	loop.CreateRet(sum)

	double := ir.NewBuilder("double")
	d := double.AddParam()
	double.CreateRet(double.CreateAdd(d, d))

	main := ir.NewBuilder("main")
	main.CreateCall(main.CreateMov(funcRef("filler")))
	main.CreateRet(main.CreateCall(main.CreateMov(funcRef("countTo")), main.CreateMov(intConst(10))))

	errs := errors.NewHandler()
	chunk := NewGenerator(&ir.Program{Functions: []*ir.Function{main.Func, filler.Func, loop.Func, double.Func}}, nil, errs).Generate()
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	if start := proto(t, chunk, "countTo").CodeStart; start <= vm.MaxOperand {
		t.Fatalf("countTo starts at %d: the filler is too short for this test", start)
	}
	expectInt(t, run(t, chunk), 90)

	// No address in an operand out there: long jumps, and the test made
	expectCount(t, chunk, "countTo", 0, vm.OpJMP, vm.OpJMPIF, vm.OpJMPIFNOT, vm.OpJNLT, vm.OpJNLTI)
	expectCount(t, chunk, "countTo", 2, vm.OpLONG)
	expectCount(t, chunk, "countTo", 1, vm.OpLT)
	// What needs no address is used all the same
	expectCount(t, chunk, "countTo", 1, vm.OpCALLK)
	expectCount(t, chunk, "countTo", 1, vm.OpADDI)
	expectCount(t, chunk, "filler", 68000, vm.OpADDK)
	if err := vm.Verify(chunk); err != nil {
		t.Errorf("does not verify: %v", err)
	}

	// The function that crosses the line itself is written long too
	for _, name := range []string{"main", "filler", "countTo", "double"} {
		p := proto(t, chunk, name)
		crosses := p.CodeStart+p.CodeSize > vm.MaxOperand
		if short := count(chunk, name, vm.OpJMP, vm.OpJMPIF, vm.OpJMPIFNOT); crosses && short > 0 {
			t.Errorf("%s reaches instruction %d with %d short jumps", name, p.CodeStart+p.CodeSize, short)
		}
	}
}

func stringConst(s string) ir.Constant { return ir.Constant{Val: s, Type: "String"} }

// made returns the NEWOBJ instructions of a function, in order.
func made(chunk *vm.Chunk, function string) []vm.Instr {
	p := chunk.Prototypes[chunk.FindPrototype(function)]
	var found []vm.Instr
	for _, instr := range chunk.Code[p.CodeStart : p.CodeStart+p.CodeSize] {
		if instr.Op == vm.OpNEWOBJ {
			found = append(found, instr)
		}
	}
	return found
}

// A value is made with room for the fields it is about to be given, and a
// value of an enum that is given none is marked as one that never changes.
func TestValuesAreMadeWithWhatIsKnownOfThem(t *testing.T) {
	f := ir.NewBuilder("main")
	set := func(obj *ir.Register, field string, value ir.Value) {
		f.Emit(ir.OpSetField, nil, obj, stringConst(field), value)
	}
	one := f.CreateMov(intConst(1))

	point := f.NewRegister()
	f.Emit(ir.OpNewStruct, point, stringConst("Point"))
	set(point, "x", one)
	set(point, "y", one)
	set(point, "z", one)

	circle := f.NewRegister()
	f.Emit(ir.OpNewVariant, circle, stringConst("Shape.Circle"))
	set(circle, "0", one)

	empty := f.NewRegister()
	f.Emit(ir.OpNewVariant, empty, stringConst("Shape.Empty"))

	// A struct without fields is still a value of its own: it can be
	// given some later, and two of them are told apart
	unit := f.NewRegister()
	f.Emit(ir.OpNewStruct, unit, stringConst("Unit"))

	// Given a field further on, in another block: not one that never changes
	later, done := f.NewBlock(), f.NewBlock()
	late := f.NewRegister()
	f.Emit(ir.OpNewVariant, late, stringConst("Shape.Late"))
	f.CreateJmp(later)
	f.SetBlock(later)
	set(late, "0", one)
	f.CreateJmp(done)
	f.SetBlock(done)
	f.CreateRet(f.CreateGetField(point, "z"))

	chunk := generate(t, f.Func)
	expectInt(t, run(t, chunk), 1)

	objects := made(chunk, "main")
	if len(objects) != 5 {
		t.Fatalf("expected 5 values to be made, found %d", len(objects))
	}
	want := []struct {
		name     string
		kind     uint16
		fields   uint16
		constant bool
	}{
		{"Point", vm.ObjectIsNamed, 3, false},
		{"Shape.Circle", vm.ObjectIsVariant, 1, false},
		{"Shape.Empty", vm.ObjectIsVariant, 0, true},
		{"Unit", vm.ObjectIsNamed, 0, false},
		{"Shape.Late", vm.ObjectIsVariant, 0, false},
	}
	for i, w := range want {
		c := objects[i].C
		if c&vm.ObjectKindMask != w.kind || c>>vm.ObjectFieldsShift != w.fields || (c&vm.ObjectIsConstant != 0) != w.constant {
			t.Errorf("%s: made as kind %d with %d fields, constant %v", w.name, c&vm.ObjectKindMask, c>>vm.ObjectFieldsShift, c&vm.ObjectIsConstant != 0)
		}
	}
}

// What a case of a match asks, whether a value is of one variant, is one
// instruction on the value: no text is made and nothing is called.
func TestTypeTestsAskTheValue(t *testing.T) {
	// classify(shape): 1 for a Circle, 2 for a Square, 3 otherwise
	build := func(descriptor string) *vm.Chunk {
		f := ir.NewBuilder("main")
		circle := f.NewRegister()
		f.Emit(ir.OpNewVariant, circle, stringConst("Shape.Circle"))
		f.Emit(ir.OpSetField, nil, circle, stringConst("0"), f.CreateMov(intConst(5)))

		isCircle, isSquare, other, yes := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
		f.CreateBr(f.CreateCall(f.CreateMov(funcRef("__is")), circle, f.CreateMov(stringConst("{Shape.Square}"))), isSquare, isCircle)
		f.SetBlock(isSquare)
		f.CreateRet(f.CreateMov(intConst(2)))
		f.SetBlock(isCircle)
		f.CreateBr(f.CreateCall(f.CreateMov(funcRef("__is")), circle, f.CreateMov(stringConst(descriptor))), yes, other)
		f.SetBlock(yes)
		f.CreateRet(f.CreateMov(intConst(1)))
		f.SetBlock(other)
		f.CreateRet(f.CreateMov(intConst(3)))

		errs := errors.NewHandler()
		chunk := NewGenerator(&ir.Program{Functions: []*ir.Function{f.Func}}, []string{"__is"}, errs).Generate()
		if errs.HasErrors() {
			t.Fatalf("%v", errs.Diagnostics)
		}
		return chunk
	}

	chunk := build("{Shape.Circle}")
	expectInt(t, run(t, chunk), 1)
	expectCount(t, chunk, "main", 2, vm.OpJNTYPE)
	expectCount(t, chunk, "main", 0, vm.OpCALL, vm.OpCALLX, vm.OpNEWEXFUNC, vm.OpISTYPE)
	// The descriptors are not in the program: only the names are
	for _, text := range chunk.StringPool {
		if text == "{Shape.Circle}" || text == "{Shape.Square}" {
			t.Errorf("the program holds the descriptor %s", text)
		}
	}

	// Anything but one name is for the function to read: several names,
	// a builtin type, a struct with fields
	for descriptor, want := range map[string]int64{
		"{Shape.Square|Shape.Circle}": 1,
		"{Shape.Square|Shape.Empty}":  3,
		"int":                         3,
		"any":                         1,
	} {
		chunk := build(descriptor)
		expectInt(t, run(t, chunk), want)
		expectCount(t, chunk, "main", 1, vm.OpJNTYPE) // The first test only
		expectCount(t, chunk, "main", 1, vm.OpCALLX)
	}

	// The answer kept as a value: asked of the value all the same
	f := ir.NewBuilder("main")
	square := f.NewRegister()
	f.Emit(ir.OpNewVariant, square, stringConst("Shape.Square"))
	answer := f.CreateCall(f.CreateMov(funcRef("__is")), square, f.CreateMov(stringConst("{Shape.Square}")))
	f.CreateMov(answer)
	f.CreateRet(answer)
	errs := errors.NewHandler()
	kept := NewGenerator(&ir.Program{Functions: []*ir.Function{f.Func}}, []string{"__is"}, errs).Generate()
	if result := run(t, kept); !result.IsBool() || !result.AsBool() {
		t.Errorf("expected true, got %s", vm.FormatValue(result))
	}
	expectCount(t, kept, "main", 1, vm.OpISTYPE)
	expectCount(t, kept, "main", 0, vm.OpCALLX)
}

// A call through a trait finds its method on the value: one instruction
// when the value is the first argument, which it is when a method is
// called; and every method of that name is kept, since any may be the one.
func TestMethodsFoundOnTheValue(t *testing.T) {
	method := func(name string, result int64) *ir.Function {
		b := ir.NewBuilder(name)
		b.AddParam()
		b.CreateRet(b.CreateMov(intConst(result)))
		return b.Func
	}

	main := ir.NewBuilder("main")
	circle := main.NewRegister()
	main.Emit(ir.OpNewStruct, circle, stringConst("Circle"))
	square := main.NewRegister()
	main.Emit(ir.OpNewStruct, square, stringConst("Square"))
	// circle.area() + square.area(), and the method of square kept as a value
	first := main.CreateCall(main.CreateGetMethod(circle, "area"), circle)
	second := main.CreateCall(main.CreateGetMethod(square, "area"), square)
	held := main.CreateGetMethod(square, "area")
	third := main.CreateCall(held, circle) // Found on one value, called on another
	main.CreateRet(main.CreateAdd(main.CreateAdd(first, second), third))

	prog := &ir.Program{
		Functions: []*ir.Function{
			main.Func,
			method("Circle.area", 1), method("Square.area", 20), method("Circle.name", 7),
			method("Triangle.area", 300), // Of a type no value is made of: kept all the same
		},
		Methods: []ir.Method{
			{Type: "Circle", Name: "area", Func: "Circle.area"},
			{Type: "Square", Name: "area", Func: "Square.area"},
			{Type: "Circle", Name: "name", Func: "Circle.name"},
			{Type: "Triangle", Name: "area", Func: "Triangle.area"},
		},
	}
	errs := errors.NewHandler()
	chunk := NewGenerator(prog, nil, errs).Generate()
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	if err := vm.Verify(chunk); err != nil {
		t.Fatal(err)
	}
	expectInt(t, run(t, chunk), 1+20+20)

	expectCount(t, chunk, "main", 2, vm.OpCALLM)
	expectCount(t, chunk, "main", 1, vm.OpGETMETHOD)
	expectCount(t, chunk, "main", 1, vm.OpCALL)
	// No value is given its methods when it is made
	expectCount(t, chunk, "main", 0, vm.OpSETFIELD, vm.OpNEWFUNC)

	var kept []string
	for _, p := range chunk.Prototypes {
		kept = append(kept, p.Name)
	}
	// name is a method nothing asks for
	if got := strings.Join(kept, " "); got != "main Circle.area Square.area Triangle.area" {
		t.Errorf("functions kept: %s", got)
	}
}

// A match on an enum is a run of tests of what one value is, each going
// on to the next when it fails. Three or more are one SWITCH.
func TestARunOfTypeTestsIsASwitch(t *testing.T) {
	// classify(value): 1, 2, 3, 4 for A, B, C, D and 0 for anything else
	build := func(names []string, made string, another bool) *vm.Chunk {
		f := ir.NewBuilder("main")
		value := f.NewRegister()
		f.Emit(ir.OpNewVariant, value, stringConst(made))
		other := f.NewRegister()
		f.Emit(ir.OpNewVariant, other, stringConst("E.B"))

		for i, name := range names {
			yes, no := f.NewBlock(), f.NewBlock()
			asked := ir.Value(value)
			if another && i == 2 {
				asked = other // The third test is of another value
			}
			f.CreateBr(f.CreateCall(f.CreateMov(funcRef("__is")), asked, f.CreateMov(stringConst("{"+name+"}"))), yes, no)
			f.SetBlock(yes)
			f.CreateRet(f.CreateMov(intConst(int64(i + 1))))
			f.SetBlock(no)
		}
		f.CreateRet(f.CreateMov(intConst(0)))

		errs := errors.NewHandler()
		chunk := NewGenerator(&ir.Program{Functions: []*ir.Function{f.Func}}, []string{"__is"}, errs).Generate()
		if errs.HasErrors() {
			t.Fatalf("%v", errs.Diagnostics)
		}
		if err := vm.Verify(chunk); err != nil {
			t.Fatal(err)
		}
		return chunk
	}

	four := []string{"E.A", "E.B", "E.C", "E.D"}
	for made, want := range map[string]int64{"E.A": 1, "E.B": 2, "E.C": 3, "E.D": 4, "E.Z": 0} {
		chunk := build(four, made, false)
		expectInt(t, run(t, chunk), want)
		expectCount(t, chunk, "main", 1, vm.OpSWITCH)
		expectCount(t, chunk, "main", 4, vm.OpCASE)
	}

	// Two tests are asked one after the other
	chunk := build(four[:2], "E.B", false)
	expectInt(t, run(t, chunk), 2)
	expectCount(t, chunk, "main", 0, vm.OpSWITCH)
	expectCount(t, chunk, "main", 2, vm.OpJNTYPE)

	// A run ends where another value is asked about: A and B of one
	// value are too few, and so are C and D of what follows
	chunk = build(four, "E.D", true)
	expectInt(t, run(t, chunk), 4)
	expectCount(t, chunk, "main", 0, vm.OpSWITCH)
	chunk = build(four, "E.A", true)
	expectInt(t, run(t, chunk), 1)
}

// A value worked out only to be put in a variable by the next instruction
// is written to the variable: `total = total + n` is one instruction.
func TestResultsAreWrittenStraightToTheirVariable(t *testing.T) {
	f := ir.NewBuilder("main")
	head, body, done := f.NewBlock(), f.NewBlock(), f.NewBlock()
	total := f.CreateMov(intConst(100000))
	i := f.CreateMov(intConst(0))
	f.CreateJmp(head)

	f.SetBlock(head)
	cond := f.NewRegister()
	f.Emit(ir.OpLt, cond, i, f.CreateMov(intConst(10)))
	f.CreateBr(cond, body, done)

	f.SetBlock(body)
	// total = total * 3 - i, each step through a temporary, as source
	// code compiles: t = total * 3; total = t; u = total - i; total = u
	three := f.CreateMov(intConst(100003))
	product := f.NewRegister()
	f.Emit(ir.OpMul, product, total, three)
	f.Emit(ir.OpMov, total, product)
	difference := f.NewRegister()
	f.Emit(ir.OpSub, difference, total, i)
	f.Emit(ir.OpMov, total, difference)
	// A temporary read twice stays one
	kept := f.NewRegister()
	f.Emit(ir.OpAdd, kept, i, f.CreateMov(intConst(1)))
	f.Emit(ir.OpMov, i, kept)
	f.Emit(ir.OpMod, total, total, f.CreateMov(intConst(999983)))
	f.Emit(ir.OpAdd, total, total, kept)
	f.CreateJmp(head)

	f.SetBlock(done)
	f.CreateRet(total)

	chunk := generate(t, f.Func)
	want := int64(100000)
	for i := int64(0); i < 10; i++ {
		want = (want*100003-i)%999983 + i + 1
	}
	expectInt(t, run(t, chunk), want)
	if err := vm.Verify(chunk); err != nil {
		t.Fatal(err)
	}
	// The two results that only go to total need no move; the one read
	// again afterwards is moved
	expectCount(t, chunk, "main", 1, vm.OpMOV)
}

func floatConst(x float64) ir.Constant { return ir.Constant{Val: x, Type: "Float"} }

// unary makes "fn name(x) { return <what make does with x>; }".
func unary(name string, make func(b *ir.Builder, x *ir.Register) *ir.Register) *ir.Function {
	b := ir.NewBuilder(name)
	x := b.AddParam()
	b.CreateRet(make(b, x))
	return b.Func
}

// Any number that is only an operand of arithmetic is read from the
// constants by the instruction: no register is loaded with it first.
func TestNumbersOfAnySizeAreReadByTheArithmetic(t *testing.T) {
	// mixed(x) = ((x * 100000 + 70000) / 3 - 123456) % 1000003
	mixed := unary("mixed", func(b *ir.Builder, x *ir.Register) *ir.Register {
		v := b.NewRegister()
		b.Emit(ir.OpMul, v, x, b.CreateMov(intConst(100000)))
		v = b.CreateAdd(v, b.CreateMov(intConst(70000)))
		w := b.NewRegister()
		b.Emit(ir.OpDiv, w, v, b.CreateMov(intConst(3)))
		w = b.CreateSub(w, b.CreateMov(intConst(123456)))
		r := b.NewRegister()
		b.Emit(ir.OpMod, r, w, b.CreateMov(intConst(1000003)))
		return r
	})
	main := ir.NewBuilder("main")
	main.CreateRet(main.CreateCall(main.CreateMov(funcRef("mixed")), main.CreateMov(intConst(7))))

	chunk := generate(t, main.Func, mixed)
	expectInt(t, run(t, chunk), 133210)
	for _, op := range []vm.OpCode{vm.OpMULK, vm.OpADDK, vm.OpDIVK, vm.OpSUBK, vm.OpMODK} {
		expectCount(t, chunk, "mixed", 1, op)
	}
	expectCount(t, chunk, "mixed", 0, vm.OpLOADK, vm.OpLOADI, vm.OpMUL, vm.OpADD, vm.OpDIV, vm.OpSUB, vm.OpMOD)
	if err := vm.Verify(chunk); err != nil {
		t.Errorf("does not verify: %v", err)
	}
}

// A number on the left is read the same way only where the operation
// gives the same the other way round.
func TestANumberOnTheLeftIsTakenWhereOrderDoesNotMatter(t *testing.T) {
	left := func(name string, op ir.OpType, k ir.Constant) *ir.Function {
		return unary(name, func(b *ir.Builder, x *ir.Register) *ir.Register {
			r := b.NewRegister()
			b.Emit(op, r, b.CreateMov(k), x)
			return r
		})
	}
	main := ir.NewBuilder("main")
	call := func(name string, n int64) *ir.Register {
		return main.CreateCall(main.CreateMov(funcRef(name)), main.CreateMov(intConst(n)))
	}
	// 100005 + 99995 + 20000 + 210000
	sum := main.CreateAdd(call("plus", 5), call("minus", 5))
	sum = main.CreateAdd(sum, call("over", 5))
	main.CreateRet(main.CreateAdd(sum, call("times", 70000)))

	chunk := generate(t, main.Func,
		left("plus", ir.OpAdd, intConst(100000)), left("minus", ir.OpSub, intConst(100000)),
		left("over", ir.OpDiv, intConst(100000)), left("times", ir.OpMul, intConst(3)))
	expectInt(t, run(t, chunk), 430000)

	expectCount(t, chunk, "plus", 1, vm.OpADDK)
	expectCount(t, chunk, "times", 1, vm.OpMULK)
	// 100000 - x is not x - 100000, nor 100000 / x x / 100000
	expectCount(t, chunk, "minus", 0, vm.OpSUBK)
	expectCount(t, chunk, "minus", 1, vm.OpSUB)
	expectCount(t, chunk, "over", 0, vm.OpDIVK)
	expectCount(t, chunk, "over", 1, vm.OpDIV)
	for _, name := range []string{"plus", "times"} {
		expectCount(t, chunk, name, 0, vm.OpLOADK, vm.OpLOADI)
	}
}

// A number read twice is loaded once, and read from its register.
func TestANumberReadTwiceStaysInARegister(t *testing.T) {
	twice := unary("twice", func(b *ir.Builder, x *ir.Register) *ir.Register {
		k := b.CreateMov(intConst(100000))
		v := b.CreateAdd(x, k)
		r := b.NewRegister()
		b.Emit(ir.OpMul, r, v, k)
		return r
	})
	main := ir.NewBuilder("main")
	main.CreateRet(main.CreateCall(main.CreateMov(funcRef("twice")), main.CreateMov(intConst(2))))

	chunk := generate(t, main.Func, twice)
	expectInt(t, run(t, chunk), 10000200000)
	expectCount(t, chunk, "twice", 1, vm.OpLOADK)
	expectCount(t, chunk, "twice", 0, vm.OpADDK, vm.OpMULK)
}

// A test against a Float or a large Int jumps on the constant itself,
// whichever side the number is on.
func TestTestsAgainstANumberOfAnySize(t *testing.T) {
	test := func(name string, op ir.OpType, numberLeft bool, k ir.Constant) *ir.Function {
		b := ir.NewBuilder(name)
		x := b.AddParam()
		yes, no := b.NewBlock(), b.NewBlock()
		cond := b.NewRegister()
		if numberLeft {
			b.Emit(op, cond, b.CreateMov(k), x)
		} else {
			b.Emit(op, cond, x, b.CreateMov(k))
		}
		b.CreateBr(cond, yes, no)
		b.SetBlock(yes)
		b.CreateRet(b.CreateMov(intConst(1)))
		b.SetBlock(no)
		b.CreateRet(b.CreateMov(intConst(0)))
		return b.Func
	}
	main := ir.NewBuilder("main")
	total := main.CreateMov(intConst(0))
	for _, c := range []struct {
		name   string
		arg    int64
		weight int64
	}{
		// Each that holds counts its weight: 1 + 10 + 100 + 1000
		{"below", 2, 1}, {"above", 3, 10}, {"equal", 70000, 100}, {"differs", 3, 1000},
		{"below", 3, 10000}, {"above", 2, 10000}, {"equal", 70001, 10000}, {"differs", 70000, 10000},
	} {
		got := main.CreateCall(main.CreateMov(funcRef(c.name)), main.CreateMov(intConst(c.arg)))
		weighted := main.NewRegister()
		main.Emit(ir.OpMul, weighted, got, main.CreateMov(intConst(c.weight)))
		total = main.CreateAdd(total, weighted)
	}
	main.CreateRet(total)

	chunk := generate(t, main.Func,
		test("below", ir.OpLt, false, floatConst(2.5)), // x < 2.5
		test("above", ir.OpLt, true, floatConst(2.5)),  // 2.5 < x
		test("equal", ir.OpEq, true, intConst(70000)),  // 70000 == x
		test("differs", ir.OpNeq, false, intConst(70000)))
	expectInt(t, run(t, chunk), 1111)

	expectCount(t, chunk, "below", 1, vm.OpJNLTK)
	expectCount(t, chunk, "above", 1, vm.OpJNGTK)
	expectCount(t, chunk, "equal", 1, vm.OpJNEQK)
	expectCount(t, chunk, "differs", 1, vm.OpJNNEK)
	for _, name := range []string{"below", "above", "equal", "differs"} {
		expectCount(t, chunk, name, 0, vm.OpLOADK, vm.OpLT, vm.OpEQ, vm.OpNE, vm.OpJMPIFNOT)
	}
	if err := vm.Verify(chunk); err != nil {
		t.Errorf("does not verify: %v", err)
	}
}

// A call of a host function that an instruction does the work of is that
// instruction; one of a function of the program of the same name is not.
func TestHostFunctionsDoneByAnInstruction(t *testing.T) {
	toFloat := func(b *ir.Builder, n int64) *ir.Register {
		return b.CreateCall(b.CreateMov(funcRef("std.math.toFloat")), b.CreateMov(intConst(n)))
	}
	compile := func(fns ...*ir.Function) *vm.Chunk {
		errs := errors.NewHandler()
		chunk := NewGenerator(&ir.Program{Functions: fns}, []string{"std.math.toFloat", "print"}, errs).Generate()
		if errs.HasErrors() {
			t.Fatalf("%v", errs.Diagnostics)
		}
		return chunk
	}

	// main: toFloat(8) for nothing; return toFloat(7) * 2.5 >= 17.5 ? 1 : 0,
	// with the Float compared, so that an Int would not do
	main := ir.NewBuilder("main")
	toFloat(main, 8)
	product := main.NewRegister()
	main.Emit(ir.OpMul, product, toFloat(main, 7), main.CreateMov(floatConst(2.5)))
	whole := main.NewRegister()
	main.Emit(ir.OpEq, whole, product, main.CreateMov(floatConst(17.5)))
	main.CreateRet(whole)

	chunk := compile(main.Func)
	if got := run(t, chunk); got != vm.NewBool(true) {
		t.Errorf("expected true, got %s", vm.FormatValue(got))
	}
	// The call whose result nothing reads is not made at all: TONUM fails
	// for nothing an Int argument can be
	expectCount(t, chunk, "main", 1, vm.OpTONUM)
	expectCount(t, chunk, "main", 0, vm.OpCALLX, vm.OpCALL, vm.OpNEWEXFUNC)

	// A program that has a function of that name calls it
	own := unary("std.math.toFloat", func(b *ir.Builder, x *ir.Register) *ir.Register {
		return b.CreateAdd(x, b.CreateMov(intConst(1)))
	})
	main = ir.NewBuilder("main")
	main.CreateRet(toFloat(main, 7))
	chunk = compile(main.Func, own)
	expectInt(t, run(t, chunk), 8)
	expectCount(t, chunk, "main", 0, vm.OpTONUM)
	expectCount(t, chunk, "main", 1, vm.OpCALLK)
}

// A function named in one block and called in another, as the put of
// `xs[k] = v ?? 0` is named before the branches of ??, is called by its
// number, and no value is made of it when nothing else reads it.
func TestAFunctionNamedInAnotherBlockIsCalledByItsNumber(t *testing.T) {
	pick := ir.NewBuilder("pick")
	x := pick.AddParam()
	named := pick.CreateMov(funcRef("double"))
	yes, no, join := pick.NewBlock(), pick.NewBlock(), pick.NewBlock()
	cond := pick.NewRegister()
	pick.Emit(ir.OpLt, cond, x, pick.CreateMov(intConst(10)))
	pick.CreateBr(cond, yes, no)
	pick.SetBlock(yes)
	value := pick.CreateMov(intConst(4))
	pick.CreateJmp(join)
	pick.SetBlock(no)
	pick.Emit(ir.OpMov, value, pick.CreateMov(intConst(5)))
	pick.CreateJmp(join)
	pick.SetBlock(join)
	pick.CreateRet(pick.CreateCall(named, value))

	double := ir.NewBuilder("double")
	n := double.AddParam()
	double.CreateRet(double.CreateAdd(n, n))

	entry := ir.NewBuilder("main")
	entry.CreateRet(entry.CreateCall(entry.CreateMov(funcRef("pick")), entry.CreateMov(intConst(3))))

	chunk := generate(t, entry.Func, pick.Func, double.Func)
	expectInt(t, run(t, chunk), 8)
	expectCount(t, chunk, "pick", 1, vm.OpCALLK)
	expectCount(t, chunk, "pick", 0, vm.OpCALL, vm.OpNEWFUNC)
}
