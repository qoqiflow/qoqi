package vm

import (
	"strconv"
	"testing"
	"unsafe"
)

// variantChunk builds a program whose main runs the given code. Strings
// are available as constants: constant i is the i-th of names.
func variantChunk(names []string, code []Instr) *Chunk {
	chunk := NewChunk()
	for _, name := range names {
		chunk.AddConstant(NewInt(int64(chunk.AddString(name))))
	}
	chunk.ExternNames = []string{"__is", "__cast"}
	chunk.Code = code
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 32}}
	return chunk
}

// A value of an enum is an object that carries the name of its variant and
// holds its values as the fields "0", "1", ... The helpers below write the
// instructions the compiler emits for one.

// newVariant emits `dest = Name(values...)`, the values taken from registers.
func newVariant(dest, name uint16, fieldNames []uint16, values ...uint16) []Instr {
	code := []Instr{NewInstr(OpNEWOBJ, dest, name, ObjectIsVariant)}
	for i, reg := range values {
		code = append(code, NewInstr(OpSETFIELD, dest, fieldNames[i], reg))
	}
	return code
}

func runVariant(t *testing.T, names []string, code []Instr) Value {
	t.Helper()
	result, err := NewVM().Exec(variantChunk(names, code))
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	return result
}

func TestVariantValuesArePrintedAsTheyAreMade(t *testing.T) {
	// Constants: 0 "Shape.Rect", 1 "Shape.Empty", 2 "0", 3 "1", 4 "geo.Shape.Circle", 5 "text"
	names := []string{"Shape.Rect", "Shape.Empty", "0", "1", "geo.Shape.Circle", "text"}
	fields := []uint16{2, 3}

	tests := []struct {
		name string
		code []Instr
		want string
	}{
		{"with values", join(
			[]Instr{NewInstr(OpLOADI, 1, 3, 0), NewInstr(OpLOADI, 2, 4, 0)},
			newVariant(0, 0, fields, 1, 2),
		), "Rect(3, 4)"},
		{"without values", newVariant(0, 1, fields), "Empty"},
		// Only the variant's own name, whatever module the enum is in
		{"of a module's enum", join(
			[]Instr{NewInstr(OpLOADI, 1, 7, 0)},
			newVariant(0, 4, fields, 1),
		), "Circle(7)"},
		{"holding a text", join(
			[]Instr{NewInstr(OpNEWSTR, 1, 5, 0)},
			newVariant(0, 4, fields, 1),
		), `Circle("text")`},
		{"holding another value of an enum", join(
			newVariant(1, 1, fields),
			[]Instr{NewInstr(OpLOADI, 2, 9, 0)},
			newVariant(3, 0, fields, 1, 2),
			newVariant(0, 4, fields, 3),
		), "Circle(Rect(Empty, 9))"},
	}
	for _, tt := range tests {
		code := append(tt.code, NewInstr(OpRET, 0, 0, 0))
		if got := FormatValue(runVariant(t, names, code)); got != tt.want {
			t.Errorf("%s: expected %s, got %s", tt.name, tt.want, got)
		}
	}

	// A value of a struct keeps the form of a struct
	code := []Instr{
		NewInstr(OpNEWOBJ, 0, 0, ObjectIsNamed),
		NewInstr(OpLOADI, 1, 3, 0),
		NewInstr(OpSETFIELD, 0, 2, 1),
		NewInstr(OpRET, 0, 0, 0),
	}
	if got := FormatValue(runVariant(t, names, code)); got != "{0: 3}" {
		t.Errorf("expected a struct to be printed with its fields, got %s", got)
	}
}

func join(parts ...[]Instr) []Instr {
	var out []Instr
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func TestVariantValuesAreEqualByWhatTheyHold(t *testing.T) {
	// Constants: 0 "S.A", 1 "S.B", 2 "0", 3 "1", 4 "x", 5 "y"
	names := []string{"S.A", "S.B", "0", "1", "x", "y"}
	fields := []uint16{2, 3}

	// Each test builds two values, in r10 and r11, and compares them
	tests := []struct {
		name  string
		left  []Instr
		right []Instr
		equal bool
	}{
		{"same variant without values",
			newVariant(10, 1, fields), newVariant(11, 1, fields), true},
		{"different variants",
			newVariant(10, 0, fields), newVariant(11, 1, fields), false},
		{"same variant, equal numbers",
			join([]Instr{NewInstr(OpLOADI, 1, 5, 0)}, newVariant(10, 0, fields, 1)),
			join([]Instr{NewInstr(OpLOADI, 2, 5, 0)}, newVariant(11, 0, fields, 2)), true},
		{"same variant, different numbers",
			join([]Instr{NewInstr(OpLOADI, 1, 5, 0)}, newVariant(10, 0, fields, 1)),
			join([]Instr{NewInstr(OpLOADI, 2, 6, 0)}, newVariant(11, 0, fields, 2)), false},
		{"same variant, equal texts in different objects",
			join([]Instr{NewInstr(OpNEWSTR, 1, 4, 0)}, newVariant(10, 0, fields, 1)),
			join([]Instr{NewInstr(OpNEWSTR, 2, 4, 0)}, newVariant(11, 0, fields, 2)), true},
		{"same variant, different texts",
			join([]Instr{NewInstr(OpNEWSTR, 1, 4, 0)}, newVariant(10, 0, fields, 1)),
			join([]Instr{NewInstr(OpNEWSTR, 2, 5, 0)}, newVariant(11, 0, fields, 2)), false},
		{"second value differs",
			join([]Instr{NewInstr(OpLOADI, 1, 5, 0), NewInstr(OpLOADI, 2, 1, 0)}, newVariant(10, 0, fields, 1, 2)),
			join([]Instr{NewInstr(OpLOADI, 3, 5, 0), NewInstr(OpLOADI, 4, 2, 0)}, newVariant(11, 0, fields, 3, 4)), false},
		{"values of an enum inside, equal",
			join(newVariant(1, 1, fields), newVariant(10, 0, fields, 1)),
			join(newVariant(2, 1, fields), newVariant(11, 0, fields, 2)), true},
		{"values of an enum inside, different",
			join(newVariant(1, 1, fields), newVariant(10, 0, fields, 1)),
			join(newVariant(2, 0, fields), newVariant(11, 0, fields, 2)), false},
		{"one holds more than the other",
			join([]Instr{NewInstr(OpLOADI, 1, 5, 0)}, newVariant(10, 0, fields, 1)),
			newVariant(11, 0, fields), false},
		// Arrays inside are compared by what they hold
		{"different arrays with the same content inside",
			join([]Instr{NewInstr(OpNEWARRAY, 1, 2, 0)}, newVariant(10, 0, fields, 1)),
			join([]Instr{NewInstr(OpNEWARRAY, 2, 2, 0)}, newVariant(11, 0, fields, 2)), true},
		{"the same array inside",
			join([]Instr{NewInstr(OpNEWARRAY, 1, 2, 0)}, newVariant(10, 0, fields, 1)),
			newVariant(11, 0, fields, 1), true},
	}
	for _, tt := range tests {
		for _, op := range []OpCode{OpEQ, OpNE} {
			code := join(tt.left, tt.right, []Instr{NewInstr(op, 0, 10, 11), NewInstr(OpRET, 0, 0, 0)})
			result := runVariant(t, names, code)
			if want := tt.equal == (op == OpEQ); !result.IsBool() || result.AsBool() != want {
				t.Errorf("%s (%v): expected %v, got %s", tt.name, op, want, FormatValue(result))
			}
		}
	}

	// Two structs of one type with the same fields are equal
	code := []Instr{
		NewInstr(OpNEWOBJ, 10, 0, ObjectIsNamed),
		NewInstr(OpNEWOBJ, 11, 0, ObjectIsNamed),
		NewInstr(OpEQ, 0, 10, 11),
		NewInstr(OpRET, 0, 0, 0),
	}
	if result := runVariant(t, names, code); !result.AsBool() {
		t.Error("expected two structs with the same fields to be equal")
	}
	// ...and a struct is never equal to a value of an enum of the same name
	code = []Instr{
		NewInstr(OpNEWOBJ, 10, 0, ObjectIsNamed),
		NewInstr(OpNEWOBJ, 11, 0, ObjectIsVariant),
		NewInstr(OpEQ, 0, 10, 11),
		NewInstr(OpRET, 0, 0, 0),
	}
	if result := runVariant(t, names, code); result.AsBool() {
		t.Error("expected a struct and a value of an enum to be different")
	}
}

func TestVariantValuesInTypeTests(t *testing.T) {
	// A test for a variant, or for an enum, is a test of the name the value
	// carries: the descriptor lists the names that pass
	machine := NewVM()
	make := func(name string) Value {
		obj, _, err := machine.heap.AllocObjectOf(machine.heap.RootShape(name, true), 0)
		if err != nil {
			t.Fatal(err)
		}
		return NewHeapPtr(uintptr(unsafe.Pointer(obj)))
	}
	circle, empty, red := make("Shape.Circle"), make("Shape.Empty"), make("Color.Red")

	tests := []struct {
		value      Value
		descriptor string
		want       bool
	}{
		{circle, "{Shape.Circle}", true},
		{circle, "{Shape.Empty}", false},
		{empty, "{Shape.Empty}", true},
		{circle, "{Shape.Circle|Shape.Rect|Shape.Empty}", true},
		{empty, "{Shape.Circle|Shape.Rect|Shape.Empty}", true},
		{red, "{Shape.Circle|Shape.Rect|Shape.Empty}", false},
		{red, "{Color.Red|Color.Green}", true},
		{circle, "[{Shape.Circle}]", false},
		{circle, "?{Shape.Circle}", true},
		{NewNull(), "?{Shape.Circle}", true},
		{NewNull(), "{Shape.Circle}", false},
		{NewInt(1), "{Shape.Circle}", false},
	}
	for _, tt := range tests {
		typ, err := machine.castTypeFor(tt.descriptor)
		if err != nil {
			t.Fatalf("%s: %v", tt.descriptor, err)
		}
		if got := typ.mismatch(tt.value) == ""; got != tt.want {
			t.Errorf("%s against %s: expected %v, got %v", FormatValue(tt.value), tt.descriptor, tt.want, got)
		}
	}

	// A failed cast says what the value was
	if _, err := machine.cast(red, "{Shape.Circle|Shape.Empty}"); err == nil {
		t.Error("expected the cast to fail")
	}
}

func TestVariantValuesSurviveCollection(t *testing.T) {
	// A long chain of values, each holding the one before, built with
	// garbage in between; all of it must be there at the end
	names := []string{"List.Cons", "List.Nil", "0", "1", "junk"}
	fields := []uint16{2, 3}

	code := newVariant(1, 1, fields) // r1 = Nil
	const length = 300
	for i := 0; i < length; i++ {
		code = join(code,
			[]Instr{NewInstr(OpLOADI, 2, uint16(i), 0), NewInstr(OpNEWSTR, 5, 4, 0)}, // r5: garbage
			newVariant(3, 0, fields, 2, 1),                                           // r3 = Cons(i, r1)
			[]Instr{NewInstr(OpMOV, 1, 3, 0), NewInstr(OpGC, 0, 0, 0)},
		)
	}
	code = append(code, NewInstr(OpRET, 1, 0, 0))

	machine := NewVM()
	result, err := machine.Exec(variantChunk(names, code))
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	count := 0
	for current := result; ; count++ {
		obj, ok := variantObject(current)
		if !ok {
			t.Fatalf("expected a value of an enum at position %d, got %s", count, FormatValue(current))
		}
		if obj.TypeName() == "List.Nil" {
			break
		}
		head, _ := obj.Get("0")
		if !head.IsInt() || head.AsInt() != int64(length-1-count) {
			t.Fatalf("position %d: expected %d, got %s", count, length-1-count, FormatValue(head))
		}
		current, _ = obj.Get(strconv.Itoa(1))
	}
	if count != length {
		t.Errorf("expected %d values in the chain, got %d", length, count)
	}
	if machine.heap.Collections() < length {
		t.Errorf("expected a collection per step, got %d", machine.heap.Collections())
	}
}
