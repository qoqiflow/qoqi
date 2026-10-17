package vm

import (
	"bytes"
	"strings"
	"testing"
	"unsafe"
)

// execMain runs instructions as a main function with 8 registers. String
// constants are given as pool indices, the way the assembler and the
// compiler emit them.
func execMain(t *testing.T, pool []string, constants []Value, code ...Instr) (Value, string, error) {
	t.Helper()

	chunk := NewChunk()
	chunk.StringPool = pool
	chunk.Constants = constants
	chunk.Code = code
	chunk.ExternNames = []string{"print", "println", "toString", "__push", "__cast"}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 8}}

	var out bytes.Buffer
	machine := NewVM()
	machine.SetOutput(&out)
	result, err := machine.Exec(chunk)
	return result, out.String(), err
}

func TestStringOperations(t *testing.T) {
	pool := []string{"foo", "bar", "foobar"}
	consts := []Value{NewInt(0), NewInt(1), NewInt(2)}
	load := []Instr{
		NewInstr(OpNEWSTR, 1, 0, 0), // r1 = "foo"
		NewInstr(OpNEWSTR, 2, 1, 0), // r2 = "bar"
		NewInstr(OpNEWSTR, 3, 2, 0), // r3 = "foobar"
		NewInstr(OpADD, 4, 1, 2),    // r4 = "foo" + "bar"
	}

	tests := []struct {
		name string
		op   Instr
		want string
	}{
		{"concatenation", NewInstr(OpMOV, 5, 4, 0), "foobar"},
		{"equal by content", NewInstr(OpEQ, 5, 4, 3), "true"},
		{"not equal", NewInstr(OpNE, 5, 4, 3), "false"},
		{"different strings", NewInstr(OpEQ, 5, 1, 2), "false"},
		{"less than", NewInstr(OpLT, 5, 2, 1), "true"},
		{"greater or equal", NewInstr(OpGE, 5, 2, 1), "false"},
		{"length", NewInstr(OpARRLEN, 5, 4, 0), "6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := append(append([]Instr{}, load...), tt.op, NewInstr(OpRET, 5, 0, 0))
			result, _, err := execMain(t, pool, consts, code...)
			if err != nil {
				t.Fatalf("execution failed: %v", err)
			}
			if got := FormatValue(result); got != tt.want {
				t.Errorf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestStringIndexing(t *testing.T) {
	result, _, err := execMain(t, []string{"héllo"}, []Value{NewInt(0)},
		NewInstr(OpNEWSTR, 1, 0, 0),
		NewInstr(OpLOADI, 2, 1, 0),
		NewInstr(OpGETINDEX, 3, 1, 2), // Characters, not bytes
		NewInstr(OpRET, 3, 0, 0),
	)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if got := FormatValue(result); got != "é" {
		t.Errorf("expected é, got %q", got)
	}
}

func TestFieldNamesFromStringPool(t *testing.T) {
	// Field names are constants holding a string pool index
	result, _, err := execMain(t, []string{"age"}, []Value{NewInt(0)},
		NewInstr(OpNEWOBJ, 1, 0, 0),
		NewInstr(OpLOADI, 2, 30, 0),
		NewInstr(OpSETFIELD, 1, 0, 2),
		NewInstr(OpHASFIELD, 4, 1, 0),
		NewInstr(OpGETFIELD, 3, 1, 0),
		NewInstr(OpJMPIFNOT, 4, 8, 0),
		NewInstr(OpRET, 3, 0, 0),
		NewInstr(OpNOP, 0, 0, 0),
		NewInstr(OpLOADNULL, 3, 0, 0),
		NewInstr(OpRET, 3, 0, 0),
	)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if !result.IsInt() || result.AsInt() != 30 {
		t.Errorf("expected 30, got %s", FormatValue(result))
	}
}

func TestRuntimeTypeAndBoundsErrors(t *testing.T) {
	pool := []string{"field", "text"}
	consts := []Value{NewInt(0), NewInt(1)}

	tests := []struct {
		name string
		code []Instr
		want string
	}{
		{"read past the end", []Instr{
			NewInstr(OpNEWARRAY, 1, 2, 0),
			NewInstr(OpLOADI, 2, 0, 0),
			NewInstr(OpGETINDEX, 3, 1, 2),
		}, "index out of bounds: 0 (length 0)"},
		{"write past the capacity", []Instr{
			NewInstr(OpNEWARRAY, 1, 2, 0),
			NewInstr(OpLOADI, 2, 5, 0),
			NewInstr(OpSETINDEX, 1, 2, 2),
		}, "index out of bounds: 5"},
		{"index a number", []Instr{
			NewInstr(OpLOADI, 1, 7, 0),
			NewInstr(OpLOADI, 2, 0, 0),
			NewInstr(OpGETINDEX, 3, 1, 2),
		}, "GETINDEX requires array, got Int"},
		{"field of an array", []Instr{
			NewInstr(OpNEWARRAY, 1, 0, 0),
			NewInstr(OpGETFIELD, 2, 1, 0),
		}, "GETFIELD requires object, got array"},
		{"field of a string", []Instr{
			NewInstr(OpNEWSTR, 1, 1, 0),
			NewInstr(OpLOADI, 2, 1, 0),
			NewInstr(OpSETFIELD, 1, 0, 2),
		}, "SETFIELD requires object, got String"},
		{"push to an object", []Instr{
			NewInstr(OpNEWOBJ, 1, 0, 0),
			NewInstr(OpARRPUSH, 1, 1, 0),
		}, "ARRPUSH requires array, got struct"},
		{"length of a bool", []Instr{
			NewInstr(OpLOADBOOL, 1, 1, 0),
			NewInstr(OpARRLEN, 2, 1, 0),
		}, "ARRLEN requires array, got Bool"},
		{"string plus number", []Instr{
			NewInstr(OpNEWSTR, 1, 1, 0),
			NewInstr(OpLOADI, 2, 1, 0),
			NewInstr(OpADD, 3, 1, 2),
		}, "ADD cannot concatenate String and Int"},
		{"integer division by zero", []Instr{
			NewInstr(OpLOADI, 1, 1, 0),
			NewInstr(OpLOADI, 2, 0, 0),
			NewInstr(OpDIV, 3, 1, 2),
		}, "division by zero"},
		{"integer modulo by zero", []Instr{
			NewInstr(OpLOADI, 1, 1, 0),
			NewInstr(OpLOADI, 2, 0, 0),
			NewInstr(OpMOD, 3, 1, 2),
		}, "division by zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := append(tt.code, NewInstr(OpRET, 0, 0, 0))
			_, _, err := execMain(t, pool, consts, code...)
			if err == nil {
				t.Fatal("expected a runtime error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCheckedElementWrite(t *testing.T) {
	// r1 = [10, 20] with capacity 8; r2 = index; r3 = 99
	setup := func(index uint16) []Instr {
		return []Instr{
			NewInstr(OpNEWARRAY, 1, 8, 0),
			NewInstr(OpLOADI, 4, 10, 0),
			NewInstr(OpARRPUSH, 1, 4, 0),
			NewInstr(OpLOADI, 4, 20, 0),
			NewInstr(OpARRPUSH, 1, 4, 0),
			NewInstr(OpLOADI, 2, index, 0),
			NewInstr(OpLOADI, 3, 99, 0),
		}
	}
	run := func(index uint16, write OpCode) (string, error) {
		code := append(setup(index), NewInstr(write, 1, 2, 3), NewInstr(OpRET, 1, 0, 0))
		result, _, err := execMain(t, nil, nil, code...)
		return FormatValue(result), err
	}

	// SETELEM replaces an existing element...
	if got, err := run(1, OpSETELEM); err != nil || got != "[10, 99]" {
		t.Errorf("expected [10, 99], got %s, %v", got, err)
	}
	// ...and refuses anything past the length, even within the capacity
	for _, index := range []uint16{2, 5, 7, 200} {
		_, err := run(index, OpSETELEM)
		if err == nil || !strings.Contains(err.Error(), "index out of bounds") || !strings.Contains(err.Error(), "(length 2)") {
			t.Errorf("SETELEM at %d: expected an out-of-bounds error, got %v", index, err)
		}
	}

	// SETINDEX, used by hand-written assembly, grows up to the capacity
	if got, err := run(4, OpSETINDEX); err != nil || got != "[10, 20, null, null, 99]" {
		t.Errorf("expected SETINDEX to grow the array, got %s, %v", got, err)
	}

	// Both check what they are given
	notArray := []Instr{NewInstr(OpLOADI, 1, 1, 0), NewInstr(OpLOADI, 2, 0, 0), NewInstr(OpSETELEM, 1, 2, 2), NewInstr(OpRET, 1, 0, 0)}
	if _, _, err := execMain(t, nil, nil, notArray...); err == nil || !strings.Contains(err.Error(), "SETELEM requires array, got Int") {
		t.Errorf("expected a type error, got %v", err)
	}
	badIndex := append(setup(0), NewInstr(OpLOADBOOL, 2, 1, 0), NewInstr(OpSETELEM, 1, 2, 3), NewInstr(OpRET, 1, 0, 0))
	if _, _, err := execMain(t, nil, nil, badIndex...); err == nil || !strings.Contains(err.Error(), "SETELEM requires integer index") {
		t.Errorf("expected an index type error, got %v", err)
	}
}

func TestIntegerOverflow(t *testing.T) {
	// r1 op r2 with both loaded from constants
	run := func(op OpCode, a, b int64) (Value, error) {
		result, _, err := execMain(t, nil, []Value{NewInt(a), NewInt(b)},
			NewInstr(OpLOADK, 1, 0, 0),
			NewInstr(OpLOADK, 2, 1, 0),
			NewInstr(op, 3, 1, 2),
			NewInstr(OpRET, 3, 0, 0),
		)
		return result, err
	}

	within := []struct {
		name string
		op   OpCode
		a, b int64
		want int64
	}{
		{"largest sum", OpADD, MaxInt - 1, 1, MaxInt},
		{"smallest difference", OpSUB, MinInt + 1, 1, MinInt},
		{"largest product", OpMUL, MaxInt, 1, MaxInt},
		{"product of large factors", OpMUL, 1 << 23, 1 << 23, 1 << 46},
		{"negative product", OpMUL, -(1 << 23), 1 << 24, MinInt},
		{"division", OpDIV, MinInt, 2, MinInt / 2},
		{"remainder", OpMOD, MinInt, -1, 0},
	}
	for _, tt := range within {
		result, err := run(tt.op, tt.a, tt.b)
		if err != nil || !result.IsInt() || result.AsInt() != tt.want {
			t.Errorf("%s: expected %d, got %s, %v", tt.name, tt.want, FormatValue(result), err)
		}
	}

	overflowing := []struct {
		name string
		op   OpCode
		a, b int64
	}{
		{"sum past the largest", OpADD, MaxInt, 1},
		{"sum past the smallest", OpADD, MinInt, -1},
		{"difference past the smallest", OpSUB, MinInt, 1},
		{"difference past the largest", OpSUB, MaxInt, -1},
		{"product just past the range", OpMUL, 1 << 24, 1 << 23},
		{"product past 64 bits", OpMUL, MaxInt, MaxInt},
		{"negative product past 64 bits", OpMUL, MinInt, MaxInt},
		{"smallest divided by minus one", OpDIV, MinInt, -1},
	}
	for _, tt := range overflowing {
		result, err := run(tt.op, tt.a, tt.b)
		if err == nil || !strings.Contains(err.Error(), "integer overflow") {
			t.Errorf("%s: expected an overflow error, got %s, %v", tt.name, FormatValue(result), err)
		}
	}

	// Negating the smallest Int has no Int result either
	_, _, err := execMain(t, nil, []Value{NewInt(MinInt)},
		NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpNEG, 2, 1, 0), NewInstr(OpRET, 2, 0, 0))
	if err == nil || !strings.Contains(err.Error(), "integer overflow") {
		t.Errorf("expected negating the smallest Int to overflow, got %v", err)
	}

	// Floats follow IEEE 754 and do not fail
	result, _, err := execMain(t, nil, []Value{NewNumber(1e308)},
		NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpADD, 2, 1, 1), NewInstr(OpRET, 2, 0, 0))
	if err != nil || !result.IsNumber() {
		t.Errorf("expected float arithmetic not to fail, got %s, %v", FormatValue(result), err)
	}
}

func TestBuiltinFunctions(t *testing.T) {
	// print("n =", 42); println(toString(true)); push(arr, 7); return arr
	result, out, err := execMain(t, []string{"n ="}, []Value{NewInt(0)},
		NewInstr(OpNEWEXFUNC, 1, 0, 0), // print
		NewInstr(OpNEWSTR, 2, 0, 0),
		NewInstr(OpLOADI, 3, 42, 0),
		NewInstr(OpCALL, 1, 2, 0),

		NewInstr(OpNEWEXFUNC, 1, 2, 0), // toString
		NewInstr(OpLOADBOOL, 2, 1, 0),
		NewInstr(OpCALL, 1, 1, 0),
		NewInstr(OpMOV, 2, 0, 0),
		NewInstr(OpNEWEXFUNC, 1, 1, 0), // println
		NewInstr(OpCALL, 1, 1, 0),

		NewInstr(OpNEWARRAY, 5, 0, 0),
		NewInstr(OpNEWEXFUNC, 1, 3, 0), // push
		NewInstr(OpMOV, 2, 5, 0),
		NewInstr(OpLOADI, 3, 7, 0),
		NewInstr(OpCALL, 1, 2, 0),
		NewInstr(OpRET, 5, 0, 0),
	)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if want := "n = 42true\n"; out != want {
		t.Errorf("expected output %q, got %q", want, out)
	}
	if got := FormatValue(result); got != "[7]" {
		t.Errorf("expected [7], got %s", got)
	}
}

func TestFormatValue(t *testing.T) {
	heap := NewHeap(0)
	ptr := func(obj Obj) Value { return NewHeapPtr(uintptr(unsafe.Pointer(obj.Header()))) }

	str, _, _ := heap.AllocString("text")
	arr, _, _ := heap.AllocArray(2)
	arr.Append(NewInt(1))
	arr.Append(ptr(str))
	obj, _, _ := heap.AllocObject()
	obj.Set("b", NewBool(true))
	obj.Set("a", ptr(arr))

	// A structure that contains itself must still format
	loop, _, _ := heap.AllocArray(1)
	loop.Append(ptr(loop))

	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{"int", NewInt(-12), "-12"},
		{"float", NewNumber(2.5), "2.5"},
		{"whole float", NewNumber(3), "3"},
		{"bool", NewBool(false), "false"},
		{"null", NewNull(), "null"},
		{"string", ptr(str), "text"},
		{"array quotes nested strings", ptr(arr), `[1, "text"]`},
		{"object sorts its fields", ptr(obj), `{a: [1, "text"], b: true}`},
		{"self reference", ptr(loop), "[[[[[[[[[...]]]]]]]]]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatValue(tt.value); got != tt.want {
				t.Errorf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
