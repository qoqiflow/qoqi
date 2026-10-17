package vm

import (
	"io"
	"math"
	"strings"
	"testing"
)

// The instructions that say in one step what took several: each must do
// exactly what the several did, for every kind of value, failures included.

// both runs the same computation written the long way and the short way,
// and checks that they agree: on the result, or on how they fail.
func both(t *testing.T, name string, constants []Value, long, short []Instr) {
	t.Helper()
	outcome := func(code []Instr) string {
		chunk := NewChunk()
		chunk.Constants = constants
		chunk.StringPool = []string{"text", "other"}
		chunk.Code = code
		chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 6}}
		result, err := NewVM().Exec(chunk)
		if err != nil {
			failure, ok := err.(*RuntimeError)
			if !ok {
				return "error: " + err.Error()
			}
			// Where it happened differs by construction; what and of
			// which kind must not
			return "fails (" + string(failure.Kind) + "): " + failure.Message
		}
		return FormatValue(result)
	}
	if l, s := outcome(long), outcome(short); l != s {
		t.Errorf("%s:\n  the long way:  %s\n  the short way: %s", name, l, s)
	}
}

func TestTestsThatJumpDoWhatTheTestAndTheJumpDid(t *testing.T) {
	values := []Value{
		NewInt(3), NewInt(5), NewInt(-4), NewInt(0), NewInt(MaxInt), NewInt(MinInt),
		NewNumber(3), NewNumber(4.5), NewNumber(-0.5), NewBool(true), NewNull(),
	}
	nan := NewNumber(0).AsNumber()
	values = append(values, NewNumber(nan/nan))

	type test struct {
		long    OpCode // The comparison
		short   OpCode // The jump unless it holds
		swapped bool   // Written with the registers the other way round
	}
	tests := []test{
		{OpLT, OpJNLT, false}, {OpLE, OpJNLE, false},
		{OpGT, OpJNLT, true}, {OpGE, OpJNLE, true},
		{OpEQ, OpJNEQ, false}, {OpNE, OpJNNE, false},
	}
	for _, tt := range tests {
		for i, a := range values {
			for j, b := range values {
				// r1 = a, r2 = b; return 1 if the test holds, else 2
				load := []Instr{NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpLOADK, 2, uint16(j), 0)}
				tail := []Instr{
					NewInstr(OpLOADI, 0, 1, 0), NewInstr(OpRET, 0, 0, 0),
					NewInstr(OpLOADI, 0, 2, 0), NewInstr(OpRET, 0, 0, 0),
				}
				long := append(append([]Instr{}, load...), NewInstr(tt.long, 3, 1, 2), NewInstr(OpJMPIFNOT, 3, 6, 0))
				long = append(long, tail...)

				x, y := uint16(1), uint16(2)
				if tt.swapped {
					x, y = y, x
				}
				short := append(append([]Instr{}, load...), NewInstr(tt.short, x, y, 5))
				short = append(short, tail...)

				name := NewInstr(tt.long, 0, 0, 0).String() + " of " + a.String() + " and " + b.String()
				// A swapped test names its operands the other way round
				// when it refuses them; the refusal is the same
				if tt.swapped && !(bothInts(a, b) || (a.IsNumber() && b.IsNumber())) {
					continue
				}
				both(t, name, values, long, short)
			}
		}
	}

	// Two texts are compared by what they say, in a test that jumps too
	for _, tt := range tests {
		for _, pair := range [][2]uint16{{0, 0}, {0, 1}, {1, 0}} {
			load := []Instr{NewInstr(OpNEWSTR, 1, pair[0], 0), NewInstr(OpNEWSTR, 2, pair[1], 0)}
			tail := []Instr{
				NewInstr(OpLOADI, 0, 1, 0), NewInstr(OpRET, 0, 0, 0),
				NewInstr(OpLOADI, 0, 2, 0), NewInstr(OpRET, 0, 0, 0),
			}
			long := append(append([]Instr{}, load...), NewInstr(tt.long, 3, 1, 2), NewInstr(OpJMPIFNOT, 3, 6, 0))
			x, y := uint16(1), uint16(2)
			if tt.swapped {
				x, y = y, x
			}
			short := append(append([]Instr{}, load...), NewInstr(tt.short, x, y, 5))
			both(t, NewInstr(tt.long, 0, 0, 0).String()+" of two texts", []Value{NewInt(0), NewInt(1)}, append(long, tail...), append(short, tail...))
		}
	}
}

func TestTestsAgainstASmallNumber(t *testing.T) {
	values := []Value{
		NewInt(0), NewInt(6), NewInt(7), NewInt(8), NewInt(-7), NewInt(65535), NewInt(65536), NewInt(MaxInt), NewInt(MinInt),
		NewNumber(7), NewNumber(6.5), NewBool(false), NewNull(),
	}
	tests := map[OpCode]OpCode{
		OpLT: OpJNLTI, OpLE: OpJNLEI, OpGT: OpJNGTI, OpGE: OpJNGEI, OpEQ: OpJNEQI, OpNE: OpJNNEI,
	}
	for long, short := range tests {
		for i, a := range values {
			for _, k := range []uint16{0, 7, 65535} {
				tail := []Instr{
					NewInstr(OpLOADI, 0, 1, 0), NewInstr(OpRET, 0, 0, 0),
					NewInstr(OpLOADI, 0, 2, 0), NewInstr(OpRET, 0, 0, 0),
				}
				// r1 = a; r2 = k; test; jump
				longCode := append([]Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpLOADI, 2, k, 0),
					NewInstr(long, 3, 1, 2), NewInstr(OpJMPIFNOT, 3, 6, 0),
				}, tail...)
				shortCode := append([]Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpNOP, 0, 0, 0), NewInstr(OpNOP, 0, 0, 0),
					NewInstr(short, 1, k, 6),
				}, tail...)
				both(t, NewInstr(long, 0, 0, 0).String()+" of "+a.String()+" and a small number", values, longCode, shortCode)
			}
		}
	}
}

func TestArithmeticWithASmallNumber(t *testing.T) {
	values := []Value{
		NewInt(0), NewInt(41), NewInt(-41), NewInt(MaxInt), NewInt(MaxInt - 5), NewInt(MinInt), NewInt(MinInt + 5),
		NewNumber(1.5), NewBool(true), NewNull(),
	}
	for i, a := range values {
		for _, k := range []uint16{0, 1, 5, 6, 65535} {
			for long, short := range map[OpCode]OpCode{OpADD: OpADDI, OpSUB: OpSUBI} {
				longCode := []Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpLOADI, 2, k, 0),
					NewInstr(long, 0, 1, 2), NewInstr(OpRET, 0, 0, 0),
				}
				shortCode := []Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0),
					NewInstr(short, 0, 1, k), NewInstr(OpRET, 0, 0, 0),
				}
				both(t, NewInstr(long, 0, 0, 0).String()+" of "+a.String()+" and a small number", values, longCode, shortCode)
			}
		}
	}

	// A text and a number do not add up, the short way either
	both(t, "a text plus a small number", []Value{NewInt(0)},
		[]Instr{NewInstr(OpNEWSTR, 1, 0, 0), NewInstr(OpLOADI, 2, 1, 0), NewInstr(OpADD, 0, 1, 2), NewInstr(OpRET, 0, 0, 0)},
		[]Instr{NewInstr(OpNEWSTR, 1, 0, 0), NewInstr(OpADDI, 0, 1, 1), NewInstr(OpRET, 0, 0, 0)})
}

// called is a program whose main calls add(a, b) by its number and a host
// function by its own.
func called() *Chunk {
	chunk := NewChunk()
	chunk.ExternNames = []string{"println", "toString"}
	chunk.Code = []Instr{
		// main
		NewInstr(OpLOADI, 2, 30, 0),
		NewInstr(OpLOADI, 3, 12, 0),
		NewInstr(OpCALLK, 1, 2, 1), // r0 = add(30, 12)
		NewInstr(OpMOV, 4, 0, 0),
		NewInstr(OpMOV, 2, 0, 0),
		NewInstr(OpCALLX, 1, 1, 0), // println(r0)
		NewInstr(OpRET, 4, 0, 0),
		// add
		NewInstr(OpADD, 0, 0, 1),
		NewInstr(OpRET, 0, 0, 0),
		// counter: captures a variable
		NewInstr(OpLDCAP, 0, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 7, RegisterCount: 5},
		{Name: "add", CodeStart: 7, CodeSize: 2, RegisterCount: 2, ArgCount: 2},
		{Name: "counter", CodeStart: 9, CodeSize: 2, RegisterCount: 1, UpvalueCount: 1},
	}
	return chunk
}

func TestCallsByNumber(t *testing.T) {
	var out strings.Builder
	machine := NewVM()
	machine.SetOutput(&out)
	chunk := called()

	for round := 0; round < 3; round++ {
		out.Reset()
		result, err := machine.Exec(chunk)
		if err != nil || result.AsInt() != 42 || out.String() != "42\n" {
			t.Fatalf("round %d: %v, %v, wrote %q", round, result, err, out.String())
		}
	}

	// The function is made once, however often it is called
	before := countObjects(machine.heap)
	loop := called()
	loop.Code[6] = NewInstr(OpJMP, 0, 0, 0) // Round and round
	machine.SetStepLimit(2000)
	machine.SetOutput(io.Discard)
	if _, err := machine.Exec(loop); err == nil || err.(*RuntimeError).Kind != KindBudget {
		t.Fatalf("expected the loop to be ended, got %v", err)
	}
	machine.CollectGarbage()
	if after := countObjects(machine.heap); after > before+4 {
		t.Errorf("%d objects before a thousand calls, %d after", before, after)
	}

	// Calls by number count as steps and nest as deep as the others
	machine.SetStepLimit(0)
	deep := NewChunk()
	deep.Code = []Instr{
		NewInstr(OpCALLK, 0, 0, 1), NewInstr(OpRET, 0, 0, 0),
		NewInstr(OpCALLK, 0, 0, 1), NewInstr(OpRET, 0, 0, 0), // down calls itself
	}
	deep.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 2, RegisterCount: 1},
		{Name: "down", CodeStart: 2, CodeSize: 2, RegisterCount: 1},
	}
	_, err := machine.Exec(deep)
	if failure, ok := err.(*RuntimeError); !ok || failure.Kind != KindDepth || len(failure.Stack) < 100 || failure.Stack[0].Function != "down" {
		t.Errorf("expected the calls to go too deep, in down, got %v", err)
	}
}

func TestCallsByNumberAreCheckedWithTheChunk(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Chunk)
		want   string
	}{
		{"a function that is not there", func(c *Chunk) { c.Code[2].C = 3 }, "function 3 of 3"},
		{"too few arguments", func(c *Chunk) { c.Code[2].B = 1 }, "expects 2 arguments, got 1"},
		{"too many arguments", func(c *Chunk) { c.Code[2].B = 3 }, "expects 2 arguments, got 3"},
		{"a function that captures", func(c *Chunk) { c.Code[2] = NewInstr(OpCALLK, 1, 0, 2) }, "captures variables"},
		{"arguments past the registers", func(c *Chunk) { c.Code[2].A = 4 }, "reaches past"},
		{"a host function that is not there", func(c *Chunk) { c.Code[5].C = 2 }, "host function 2 of 2"},
		{"a test that jumps out of the function", func(c *Chunk) { c.Code[3] = NewInstr(OpJNLT, 0, 1, 8) }, "jump to 8"},
		{"a test on a register that is not there", func(c *Chunk) { c.Code[3] = NewInstr(OpJNEQI, 9, 1, 4) }, "register r9"},
		{"a sum into a register that is not there", func(c *Chunk) { c.Code[3] = NewInstr(OpADDI, 5, 1, 4) }, "register r5"},
		{"an instruction after the last", func(c *Chunk) { c.Code[3] = NewInstr(LastOp+1, 0, 0, 0) }, "unknown instruction"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunk := called()
			c.change(chunk)
			if err := Verify(chunk); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("expected an error with %q, got %v", c.want, err)
			}
		})
	}
	if err := Verify(called()); err != nil {
		t.Errorf("the program as written is refused: %v", err)
	}
}

// A host function called by its number is asked about like any other.
func TestCallsByNumberAreAskedAbout(t *testing.T) {
	machine := NewVM()
	machine.SetOutput(io.Discard)
	machine.PermitNatives(func(name string) bool { return name != "println" })
	_, err := machine.Exec(called())
	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindLibrary || !strings.Contains(failure.Message, "println") {
		t.Fatalf("expected println to be refused, got %v", err)
	}
	// Permitted again, the same chunk runs: what is kept for a chunk does
	// not outlive the rule it was made under
	machine.PermitNatives(nil)
	if result, err := machine.Exec(called()); err != nil || result.AsInt() != 42 {
		t.Errorf("%v, %v", result, err)
	}
}

// What is kept for a chunk is about that chunk: two programs with
// different functions under the same numbers do not get each other's.
func TestWhatIsKeptBelongsToItsChunk(t *testing.T) {
	machine := NewVM()
	machine.SetOutput(io.Discard)
	first := called()
	second := called()
	second.Code[7] = NewInstr(OpSUB, 0, 0, 1) // The function numbered 1 subtracts here

	a, b := machine.cacheFor(first), machine.cacheFor(second)
	if a == b || machine.cacheFor(first) != a {
		t.Fatal("two chunks share what is kept for them, or one chunk has two")
	}
	for round := 0; round < 2; round++ {
		if result, err := machine.Exec(first); err != nil || result.AsInt() != 42 {
			t.Errorf("the first program: %v, %v", result, err)
		}
		if result, err := machine.Exec(second); err != nil || result.AsInt() != 18 {
			t.Errorf("the second program: %v, %v", result, err)
		}
	}
}

// Floats are equal as numbers are, not as the bits that hold them: zero
// and minus zero are the same number, and NaN is not equal to anything,
// itself included. The same wherever equality is asked: as a value, in a
// test that jumps, inside a value of an enum.
func TestFloatsAreEqualAsNumbers(t *testing.T) {
	zero := NewNumber(0)
	minusZero := NewNumber(math.Copysign(0, -1))
	nan := NewNumber(zero.AsNumber() / zero.AsNumber())
	if uint64(zero) == uint64(minusZero) {
		t.Fatal("zero and minus zero are the same bits: the test tests nothing")
	}

	cases := []struct {
		name  string
		a, b  Value
		equal bool
	}{
		{"zero and minus zero", zero, minusZero, true},
		{"minus zero and zero", minusZero, zero, true},
		{"NaN and itself", nan, nan, false},
		{"NaN and a number", nan, NewNumber(1), false},
		{"a number and itself", NewNumber(1.5), NewNumber(1.5), true},
		{"two numbers", NewNumber(1.5), NewNumber(2.5), false},
		{"infinity and itself", NewNumber(1 / zero.AsNumber()), NewNumber(1 / zero.AsNumber()), true},
		// An Int and a Float are compared as numbers
		{"an Int and the same Float", NewInt(1), NewNumber(1), true},
		{"a Float and the same Int", NewNumber(-3), NewInt(-3), true},
		{"an Int and another Float", NewInt(1), NewNumber(1.5), false},
		{"an Int and NaN", NewInt(0), nan, false},
		{"null and itself", NewNull(), NewNull(), true},
		{"true and itself", NewBool(true), NewBool(true), true},
	}
	for _, c := range cases {
		if got := valuesEqual(c.a, c.b); got != c.equal {
			t.Errorf("%s: valuesEqual says %v", c.name, got)
		}
		if got := sameValue(c.a, c.b); got != c.equal {
			t.Errorf("%s: sameValue says %v", c.name, got)
		}

		// As EQ, as NE, as a test that jumps, and held by a variant
		run := func(code ...Instr) Value {
			chunk := NewChunk()
			chunk.Constants = []Value{c.a, c.b, NewInt(0), NewInt(1)}
			chunk.StringPool = []string{"Box.Of", "0"}
			chunk.Code = code
			chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 6}}
			result, err := NewVM().Exec(chunk)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			return result
		}
		load := []Instr{NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpLOADK, 2, 1, 0)}
		if got := run(append(load, NewInstr(OpEQ, 0, 1, 2), NewInstr(OpRET, 0, 0, 0))...); got.AsBool() != c.equal {
			t.Errorf("%s: EQ says %v", c.name, got.AsBool())
		}
		if got := run(append(load, NewInstr(OpNE, 0, 1, 2), NewInstr(OpRET, 0, 0, 0))...); got.AsBool() == c.equal {
			t.Errorf("%s: NE says %v", c.name, got.AsBool())
		}
		jumped := run(append(load,
			NewInstr(OpJNEQ, 1, 2, 5),
			NewInstr(OpLOADBOOL, 0, 1, 0), NewInstr(OpRET, 0, 0, 0),
			NewInstr(OpLOADBOOL, 0, 0, 0), NewInstr(OpRET, 0, 0, 0))...)
		if jumped.AsBool() != c.equal {
			t.Errorf("%s: JNEQ says %v", c.name, jumped.AsBool())
		}
		boxed := run(append(load,
			NewInstr(OpNEWOBJ, 3, 2, ObjectIsVariant|1<<ObjectFieldsShift), NewInstr(OpSETFIELD, 3, 3, 1),
			NewInstr(OpNEWOBJ, 4, 2, ObjectIsVariant|1<<ObjectFieldsShift), NewInstr(OpSETFIELD, 4, 3, 2),
			NewInstr(OpEQ, 0, 3, 4), NewInstr(OpRET, 0, 0, 0))...)
		if boxed.AsBool() != c.equal {
			t.Errorf("%s: held by a variant, EQ says %v", c.name, boxed.AsBool())
		}
	}
}

// A whole number met by a Float is worked out as a Float, by every
// instruction that works on numbers, whichever side it is on. Two whole
// numbers are still worked out as whole numbers.
func TestIntsAndFloatsAreWorkedOutTogether(t *testing.T) {
	run := func(op OpCode, a, b Value) (Value, error) {
		chunk := NewChunk()
		chunk.Code = []Instr{
			NewInstr(OpLOADK, 1, uint16(chunk.AddConstant(a)), 0),
			NewInstr(OpLOADK, 2, uint16(chunk.AddConstant(b)), 0),
			NewInstr(op, 0, 1, 2),
			NewInstr(OpRET, 0, 0, 0),
		}
		addMainProto(chunk, 3, 0)
		return NewVM().Exec(chunk)
	}
	seven, two := NewInt(7), NewInt(2)
	sevenF, twoF := NewNumber(7), NewNumber(2)

	numbers := []struct {
		name string
		op   OpCode
		a, b Value
		want Value
	}{
		{"Int + Float", OpADD, seven, twoF, NewNumber(9)},
		{"Float + Int", OpADD, sevenF, two, NewNumber(9)},
		{"Int - Float", OpSUB, seven, twoF, NewNumber(5)},
		{"Float - Int", OpSUB, sevenF, two, NewNumber(5)},
		{"Int * Float", OpMUL, seven, twoF, NewNumber(14)},
		{"Float * Int", OpMUL, sevenF, two, NewNumber(14)},
		{"Int / Float", OpDIV, seven, twoF, NewNumber(3.5)},
		{"Float / Int", OpDIV, sevenF, two, NewNumber(3.5)},
		{"Int / Int", OpDIV, seven, two, NewInt(3)},
		{"Int + Int", OpADD, seven, two, NewInt(9)},
		{"Float / Float", OpDIV, sevenF, twoF, NewNumber(3.5)},
		{"Float / zero", OpDIV, sevenF, NewInt(0), NewNumber(math.Inf(1))},
	}
	for _, c := range numbers {
		got, err := run(c.op, c.a, c.b)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: expected %s, got %s", c.name, c.want.String(), got.String())
		}
	}

	tests := []struct {
		name string
		op   OpCode
		a, b Value
		want bool
	}{
		{"Int < Float", OpLT, two, NewNumber(2.5), true},
		{"Float < Int", OpLT, NewNumber(2.5), two, false},
		{"Int <= the same Float", OpLE, two, twoF, true},
		{"Float > Int", OpGT, NewNumber(2.5), two, true},
		{"Int >= Float", OpGE, two, NewNumber(2.5), false},
		{"Int == the same Float", OpEQ, two, twoF, true},
		{"Float != another Int", OpNE, twoF, seven, true},
	}
	for _, c := range tests {
		got, err := run(c.op, c.a, c.b)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != NewBool(c.want) {
			t.Errorf("%s: expected %v, got %s", c.name, c.want, got.String())
		}
	}

	// The short forms, whose second operand is a small whole number
	for _, c := range []struct {
		name string
		op   OpCode
		want Value
	}{
		{"Float + 3", OpADDI, NewNumber(10.5)},
		{"Float - 3", OpSUBI, NewNumber(4.5)},
	} {
		chunk := NewChunk()
		chunk.Code = []Instr{
			NewInstr(OpLOADK, 1, uint16(chunk.AddConstant(NewNumber(7.5))), 0),
			NewInstr(c.op, 0, 1, 3),
			NewInstr(OpRET, 0, 0, 0),
		}
		addMainProto(chunk, 2, 0)
		got, err := NewVM().Exec(chunk)
		if err != nil || got != c.want {
			t.Errorf("%s: expected %s, got %s, %v", c.name, c.want.String(), got.String(), err)
		}
	}

	// What is not a number is still refused
	if _, err := run(OpMUL, seven, NewBool(true)); err == nil {
		t.Errorf("expected a number and a boolean to be refused")
	}
	if _, err := run(OpLT, sevenF, NewNull()); err == nil {
		t.Errorf("expected a Float and null to be refused")
	}
}

// numbersAndOthers is what an operand of arithmetic can hold, numbers at
// their limits first.
func numbersAndOthers() []Value {
	nan := NewNumber(0).AsNumber()
	return []Value{
		NewInt(0), NewInt(1), NewInt(-1), NewInt(7), NewInt(-7), NewInt(1000003), NewInt(-1000003),
		NewInt(65535), NewInt(65536), NewInt(MaxInt), NewInt(MinInt), NewInt(MaxInt / 2),
		NewNumber(0), NewNumber(-0.5), NewNumber(2.5), NewNumber(7), NewNumber(1e300), NewNumber(nan / nan),
		NewBool(true), NewNull(),
	}
}

func TestArithmeticWithAConstant(t *testing.T) {
	values := numbersAndOthers()
	// The constants are the numbers among them: what the compiler writes
	var constants []int
	for i, v := range values {
		if v.IsInt() || v.IsNumber() {
			constants = append(constants, i)
		}
	}
	ops := map[OpCode]OpCode{OpADD: OpADDK, OpSUB: OpSUBK, OpMUL: OpMULK, OpDIV: OpDIVK, OpMOD: OpMODK}
	for long, short := range ops {
		for i, a := range values {
			for _, k := range constants {
				// r1 = a; r2 = k; r0 = r1 op r2
				longCode := []Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpLOADK, 2, uint16(k), 0),
					NewInstr(long, 0, 1, 2), NewInstr(OpRET, 0, 0, 0),
				}
				shortCode := []Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0),
					NewInstr(short, 0, 1, uint16(k)), NewInstr(OpRET, 0, 0, 0),
				}
				name := NewInstr(long, 0, 0, 0).String() + " of " + a.String() + " and the constant " + values[k].String()
				both(t, name, values, longCode, shortCode)
			}
		}
	}

	// A text and a constant do not add up, the short way either
	both(t, "a text plus a constant", []Value{NewInt(70000)},
		[]Instr{NewInstr(OpNEWSTR, 1, 0, 0), NewInstr(OpLOADK, 2, 0, 0), NewInstr(OpADD, 0, 1, 2), NewInstr(OpRET, 0, 0, 0)},
		[]Instr{NewInstr(OpNEWSTR, 1, 0, 0), NewInstr(OpADDK, 0, 1, 0), NewInstr(OpRET, 0, 0, 0)})

	// The result may go to the register the operand came from, as in
	// `total = total * 31`
	both(t, "into its own operand", []Value{NewInt(12), NewNumber(0.5)},
		[]Instr{NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpLOADK, 2, 1, 0), NewInstr(OpMUL, 1, 1, 2), NewInstr(OpRET, 1, 0, 0)},
		[]Instr{NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpMULK, 1, 1, 1), NewInstr(OpRET, 1, 0, 0)})
}

func TestTestsAgainstAConstant(t *testing.T) {
	values := numbersAndOthers()
	var constants []int
	for i, v := range values {
		if v.IsInt() || v.IsNumber() {
			constants = append(constants, i)
		}
	}
	tests := map[OpCode]OpCode{
		OpLT: OpJNLTK, OpLE: OpJNLEK, OpGT: OpJNGTK, OpGE: OpJNGEK, OpEQ: OpJNEQK, OpNE: OpJNNEK,
	}
	tail := []Instr{
		NewInstr(OpLOADI, 0, 1, 0), NewInstr(OpRET, 0, 0, 0),
		NewInstr(OpLOADI, 0, 2, 0), NewInstr(OpRET, 0, 0, 0),
	}
	for long, short := range tests {
		for i, a := range values {
			for _, k := range constants {
				// r1 = a; r2 = k; test; jump: 1 if it holds, else 2
				longCode := append([]Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpLOADK, 2, uint16(k), 0),
					NewInstr(long, 3, 1, 2), NewInstr(OpJMPIFNOT, 3, 6, 0),
				}, tail...)
				shortCode := append([]Instr{
					NewInstr(OpLOADK, 1, uint16(i), 0), NewInstr(OpNOP, 0, 0, 0), NewInstr(OpNOP, 0, 0, 0),
					NewInstr(short, 1, uint16(k), 6),
				}, tail...)
				name := NewInstr(long, 0, 0, 0).String() + " of " + a.String() + " and the constant " + values[k].String()
				both(t, name, values, longCode, shortCode)
			}
		}
	}
}

// A loop that jumps back on a test against a constant can be stopped, as
// one that jumps back any other way can.
func TestATestAgainstAConstantThatLoopsCanBeStopped(t *testing.T) {
	chunk := NewChunk()
	chunk.Constants = []Value{NewInt(100000)}
	chunk.Code = []Instr{
		NewInstr(OpLOADI, 1, 0, 0),
		NewInstr(OpNOP, 0, 0, 0),
		NewInstr(OpJNEQK, 1, 0, 1), // back unless r1 is 100000: forever
		NewInstr(OpRET, 1, 0, 0),
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 2}}
	machine := NewVM()
	machine.SetStepLimit(1000)
	_, err := machine.Exec(chunk)
	if failure, ok := err.(*RuntimeError); !ok || failure.Kind != KindBudget {
		t.Fatalf("expected the loop to run out of steps, got %v", err)
	}
}

func TestConstantsOfInstructionsAreCheckedWithTheChunk(t *testing.T) {
	cases := []struct {
		name string
		code Instr
		want string
	}{
		{"a constant that is not there", NewInstr(OpADDK, 2, 1, 3), "constant 3 of 3"},
		{"a constant that is not a number", NewInstr(OpMULK, 2, 1, 2), "constant 2 is not a number"},
		{"a result into a register that is not there", NewInstr(OpDIVK, 9, 1, 0), "register r9"},
		{"a test against a constant that is not there", NewInstr(OpJNLTK, 1, 7, 3), "constant 7 of 3"},
		{"a test against a constant that is not a number", NewInstr(OpJNEQK, 1, 2, 3), "constant 2 is not a number"},
		{"a test that jumps out of the function", NewInstr(OpJNGEK, 1, 0, 9), "jump to 9"},
	}
	program := func(at Instr) *Chunk {
		chunk := NewChunk()
		chunk.Constants = []Value{NewInt(70000), NewNumber(0.5), NewBool(true)}
		chunk.Code = []Instr{
			NewInstr(OpLOADI, 1, 3, 0),
			at,
			NewInstr(OpMODK, 2, 1, 1),
			NewInstr(OpRET, 2, 0, 0),
		}
		chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 3}}
		return chunk
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Verify(program(c.code)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("expected an error with %q, got %v", c.want, err)
			}
		})
	}
	for _, fine := range []Instr{NewInstr(OpSUBK, 2, 1, 0), NewInstr(OpJNLEK, 1, 1, 3)} {
		if err := Verify(program(fine)); err != nil {
			t.Errorf("%v is refused: %v", fine, err)
		}
	}
}
