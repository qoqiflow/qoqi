package vm

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

// How struct and enum values are laid out: what the values of a type have
// in common is kept once, a value is made in one piece with room for its
// fields, a value that holds nothing is made once, and asking a value for
// its type is asking for a name it already carries.

func TestValuesOfATypeShareWhatTheyHaveInCommon(t *testing.T) {
	heap := NewHeap(0)
	point := heap.RootShape("Point", false)
	if heap.RootShape("Point", false) != point {
		t.Fatal("a type has two shapes to start from")
	}
	if heap.RootShape("Point", true) == point || heap.RootShape("Vector", false) == point || heap.rootShape == point {
		t.Fatal("two types start from the same shape")
	}

	a, _, _ := heap.AllocObjectOf(point, 2)
	b, _, _ := heap.AllocObjectOf(point, 2)
	for _, obj := range []*ObjectObj{a, b} {
		obj.Set("x", NewInt(1))
		obj.Set("y", NewInt(2))
	}
	if a.shape != b.shape || a.shape == point {
		t.Error("two values given the same fields do not share a shape")
	}
	if a.TypeName() != "Point" || typeName(valueOf(&a.header)) != "Point" || a.shape.variant {
		t.Errorf("the value says it is %q", a.TypeName())
	}

	// Taking a field away leaves a value of the same type
	a.Delete("x")
	if a.TypeName() != "Point" || a.shape.root != point || a.Len() != 1 {
		t.Errorf("after losing a field: %q, %d fields", a.TypeName(), a.Len())
	}

	// A plain object is of no type, and says so
	plain, _, _ := heap.AllocObject()
	if plain.TypeName() != "" || typeName(valueOf(&plain.header)) != "struct" {
		t.Errorf("a plain object says it is %q", plain.TypeName())
	}

	// An object is its shape and its values, and nothing per value besides
	if size := unsafe.Sizeof(ObjectObj{}); size > 48 {
		t.Errorf("an object takes %d bytes before its values", size)
	}
}

// A value made with the number of its fields known is one allocation, and
// its fields are where they should be whatever that number was. One given
// more than was said still holds them all.
func TestObjectsComeWithRoomForTheirFields(t *testing.T) {
	heap := NewHeap(0)
	shape := heap.RootShape("Record", false)

	for _, fields := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 12, 13, 16, 17, 40} {
		obj, _, err := heap.AllocObjectOf(shape, fields)
		if err != nil {
			t.Fatal(err)
		}
		if cap(obj.slots) < fields || len(obj.slots) != 0 {
			t.Fatalf("%d fields: room for %d, %d there already", fields, cap(obj.slots), len(obj.slots))
		}
		if cap(obj.slots) != roomFor(fields) {
			t.Errorf("%d fields: room for %d, counted as %d", fields, cap(obj.slots), roomFor(fields))
		}
		room := cap(obj.slots)

		// Twice as many as said, each checked after every one added
		for i := 0; i < 2*fields+3; i++ {
			obj.Set(fmt.Sprint("f", i), NewInt(int64(i*7)))
			for j := 0; j <= i; j++ {
				if v, ok := obj.Get(fmt.Sprint("f", j)); !ok || v.AsInt() != int64(j*7) {
					t.Fatalf("%d fields: after adding %d, field %d is %v", fields, i+1, j, v)
				}
			}
			if i < fields && cap(obj.slots) != room {
				t.Errorf("%d fields: the values moved after only %d", fields, i+1)
			}
		}
		if obj.header.Type != ObjTypeObject || obj.TypeName() != "Record" {
			t.Errorf("%d fields: not an object of the type any more", fields)
		}
	}

	// One allocation for the object and its values, when the number is
	// one an object can come with
	for _, fields := range []int{1, 2, 4, 8, 16} {
		allocations := testing.AllocsPerRun(100, func() {
			obj := newObject(shape, fields)
			obj.slots = append(obj.slots, NewInt(1))
		})
		if allocations != 1 {
			t.Errorf("%d fields: %v allocations", fields, allocations)
		}
	}
}

// What an object is counted for when it is made is what is taken off when
// it is freed, with its room and whatever it grew to since.
func TestRoomIsCountedInAndOut(t *testing.T) {
	machine := NewVM()
	heap := machine.heap
	shape := heap.RootShape("Record", false)
	machine.CollectGarbage()
	before := heap.BytesAllocated()

	for fields := 0; fields < 30; fields++ {
		obj, _, _ := heap.AllocObjectOf(shape, fields)
		if counted := heap.BytesAllocated() - before; counted < uint64(fields)*valueSize {
			t.Fatalf("%d fields: %d bytes counted", fields, counted)
		}
		// Past its room, and back under it
		for i := 0; i < fields+5; i++ {
			if err := machine.setField(obj, fmt.Sprint("f", i), NewInt(1)); err != nil {
				t.Fatal(err)
			}
		}
		obj.Delete("f0")
		machine.CollectGarbage()
		if left := heap.BytesAllocated() - before; left != 0 {
			t.Fatalf("%d fields: %d bytes counted for an object that is gone", fields, left)
		}
	}
}

// variants is a program that makes the same value of an enum in a loop,
// one that holds nothing and one that holds a number, and returns whether
// the last two of each were the very same object.
func variants(constant uint16) *Chunk {
	chunk := NewChunk()
	chunk.StringPool = []string{"Light.Red", "Light.Dim", "0"}
	chunk.Constants = []Value{NewInt(0), NewInt(1), NewInt(2)}
	chunk.Code = []Instr{
		NewInstr(OpLOADI, 5, 200, 0),                                   // 0: turns left
		NewInstr(OpMOV, 2, 1, 0),                                       // 1: the one before
		NewInstr(OpNEWOBJ, 1, 0, ObjectIsVariant|constant),             // 2: Light.Red
		NewInstr(OpNEWOBJ, 3, 1, ObjectIsVariant|1<<ObjectFieldsShift), // 3: Light.Dim(turns)
		NewInstr(OpSETFIELD, 3, 2, 5),                                  // 4
		NewInstr(OpGC, 0, 0, 0),                                        // 5
		NewInstr(OpSUBI, 5, 5, 1),                                      // 6
		NewInstr(OpJNEQI, 5, 0, 1),                                     // 7: round again unless done
		NewInstr(OpNEWARRAY, 0, 2, 0),                                  // 8
		NewInstr(OpARRPUSH, 0, 1, 0),                                   // 9
		NewInstr(OpARRPUSH, 0, 2, 0),                                   // 10
		NewInstr(OpRET, 0, 0, 0),                                       // 11
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 6}}
	return chunk
}

func TestAValueThatHoldsNothingIsMadeOnce(t *testing.T) {
	last := func(machine *VM, chunk *Chunk) (Value, Value) {
		t.Helper()
		result, err := machine.Exec(chunk)
		if err != nil {
			t.Fatal(err)
		}
		pair := (*ArrayObj)(unsafe.Pointer(header(result)))
		return pair.Get(0), pair.Get(1)
	}

	// Marked as never given anything: the same object each time, through
	// two hundred collections
	machine := NewVM()
	a, b := last(machine, variants(ObjectIsConstant))
	if a != b {
		t.Error("two values that hold nothing are two objects")
	}
	if FormatValue(a) != "Red" || !valuesEqual(a, b) {
		t.Errorf("the value is %s", FormatValue(a))
	}
	// With the program over, only what it returned is left of it
	machine.regs = []Value{a}
	machine.caches = map[*Chunk]*chunkCache{}
	machine.CollectGarbage()
	if FormatValue(a) != "Red" {
		t.Error("the value did not survive its program")
	}

	// Not marked: a new one each time, equal all the same
	c, d := last(NewVM(), variants(0))
	if c == d || !valuesEqual(c, d) {
		t.Errorf("unmarked values: the same object %v, equal %v", c == d, valuesEqual(c, d))
	}

	// What is kept is kept for its chunk: another program's value of the
	// same variant is another object, and equal
	other := NewVM()
	first, _ := last(other, variants(ObjectIsConstant))
	other.regs = []Value{first}
	second, _ := last(other, variants(ObjectIsConstant))
	if !valuesEqual(first, second) {
		t.Error("the same variant from two programs is not equal")
	}
}

// Asking a value for its type by the instruction answers what asking the
// host function with the descriptor "{Name}" answers, for every value.
func TestAskingForATypeIsAskingForTheName(t *testing.T) {
	machine := NewVM()
	heap := machine.heap
	object := func(name string, variant bool) Value {
		obj, _, _ := heap.AllocObjectOf(heap.RootShape(name, variant), 1)
		obj.Set("0", NewInt(1))
		return valueOf(&obj.header)
	}
	text, _ := machine.newString("Shape.Circle")
	arr, _, _ := heap.AllocArray(0)
	plain, _, _ := heap.AllocObject()
	values := []Value{
		object("Shape.Circle", true), object("Shape.Square", true), object("Shape", false), object("Circle", false),
		object("Shape.Circle", false), // A struct that happens to be named like the variant
		valueOf(&plain.header), text, valueOf(&arr.header),
		NewInt(3), NewNumber(1.5), NewBool(true), NewNull(),
	}
	machine.regs = values // Kept through the allocations below

	for _, name := range []string{"Shape.Circle", "Shape.Square", "Shape", "Circle", "Nothing"} {
		typ, err := machine.castTypeFor("{" + name + "}")
		if err != nil {
			t.Fatal(err)
		}
		var known fieldCache
		// Twice round: what the instruction remembers must not change its answers
		for round := 0; round < 2; round++ {
			for _, v := range values {
				want := typ.mismatch(v) == ""
				if got := isOfType(v, &known, name); got != want {
					t.Errorf("round %d: is %s a %s? the instruction says %v, the function %v", round, FormatValue(v), name, got, want)
				}
			}
		}
	}
}

func TestTypeTestsRun(t *testing.T) {
	// r1 = a Shape.Circle; r2 = whether it is one; then jump on a type it
	// is not, and on the one it is
	chunk := NewChunk()
	chunk.StringPool = []string{"Shape.Circle", "Shape.Square"}
	chunk.Constants = []Value{NewInt(0), NewInt(1)}
	chunk.Code = []Instr{
		NewInstr(OpNEWOBJ, 1, 0, ObjectIsVariant), // 0
		NewInstr(OpISTYPE, 2, 1, 0),               // 1: true
		NewInstr(OpISTYPE, 3, 1, 1),               // 2: false
		NewInstr(OpJNTYPE, 1, 1, 6),               // 3: not a Square: go to 6
		NewInstr(OpLOADI, 0, 99, 0),               // 4
		NewInstr(OpRET, 0, 0, 0),                  // 5
		NewInstr(OpJNTYPE, 1, 0, 4),               // 6: a Circle: go on
		NewInstr(OpNEWARRAY, 0, 2, 0),             // 7
		NewInstr(OpARRPUSH, 0, 2, 0),              // 8
		NewInstr(OpARRPUSH, 0, 3, 0),              // 9
		NewInstr(OpRET, 0, 0, 0),                  // 10
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 4}}
	result, err := NewVM().Exec(chunk)
	if err != nil || FormatValue(result) != "[true, false]" {
		t.Fatalf("%s, %v", FormatValue(result), err)
	}

	for name, change := range map[string]func(*Chunk){
		"a name that is a number":      func(c *Chunk) { c.Constants[0] = NewNumber(1.5) },
		"a name outside the pool":      func(c *Chunk) { c.Constants[1] = NewInt(9) },
		"a jump out of the function":   func(c *Chunk) { c.Code[3].C = 40 },
		"a register that is not there": func(c *Chunk) { c.Code[1].A = 7 },
	} {
		damaged := *chunk
		damaged.Code = append([]Instr(nil), chunk.Code...)
		damaged.Constants = append([]Value(nil), chunk.Constants...)
		change(&damaged)
		if err := Verify(&damaged); err == nil || !strings.Contains(err.Error(), "invalid bytecode") {
			t.Errorf("%s: expected the chunk to be refused, got %v", name, err)
		}
	}
}

// methodsChunk is a program with the methods of two types: "Circle.area",
// "Square.area", "Shape.name" for the variants of the enum Shape, and a
// function that is not a method of anything.
func methodsChunk(main ...Instr) *Chunk {
	chunk := NewChunk()
	chunk.StringPool = []string{"area", "name", "missing", "Circle", "Square", "Shape.Dot", "0"}
	chunk.Constants = []Value{NewInt(0), NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5), NewInt(6)}
	chunk.Code = append(main,
		NewInstr(OpLOADI, 0, 11, 0), NewInstr(OpRET, 0, 0, 0), // Circle.area
		NewInstr(OpLOADI, 0, 22, 0), NewInstr(OpRET, 0, 0, 0), // Square.area
		NewInstr(OpADDI, 0, 1, 33), NewInstr(OpRET, 0, 0, 0), // Shape.name(self, n)
		NewInstr(OpLDCAP, 0, 0, 0), NewInstr(OpRET, 0, 0, 0), // Circle.captured: not callable by name
	)
	at := len(main)
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: at, RegisterCount: 8},
		{Name: "Circle.area", CodeStart: at, CodeSize: 2, RegisterCount: 1, ArgCount: 1},
		{Name: "Square.area", CodeStart: at + 2, CodeSize: 2, RegisterCount: 1, ArgCount: 1},
		{Name: "Shape.name", CodeStart: at + 4, CodeSize: 2, RegisterCount: 2, ArgCount: 2},
		{Name: "Circle.captured", CodeStart: at + 6, CodeSize: 2, RegisterCount: 1, ArgCount: 1, UpvalueCount: 1},
	}
	return chunk
}

// A method is found on the value it is called on, by the name of the type
// the value carries: nothing is kept on the value for it.
func TestMethodsAreFoundByTheTypeOfTheValue(t *testing.T) {
	// r1 = Circle{}, r2 = Square{}, r3 = Shape.Dot; calls area on each of
	// the first two, twice over, and name on the third; returns the sum
	chunk := methodsChunk(
		NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed),   // 0
		NewInstr(OpNEWOBJ, 2, 4, ObjectIsNamed),   // 1
		NewInstr(OpNEWOBJ, 3, 5, ObjectIsVariant), // 2
		NewInstr(OpLOADI, 4, 0, 0),                // 3: sum
		NewInstr(OpLOADI, 5, 2, 0),                // 4: rounds
		NewInstr(OpMOV, 7, 1, 0),                  // 5
		NewInstr(OpCALLM, 6, 1, 0),                // 6: r1.area()
		NewInstr(OpADD, 4, 4, 0),                  // 7
		NewInstr(OpMOV, 7, 2, 0),                  // 8
		NewInstr(OpCALLM, 6, 1, 0),                // 9: r2.area(), another instruction
		NewInstr(OpADD, 4, 4, 0),                  // 10
		NewInstr(OpSUBI, 5, 5, 1),                 // 11
		NewInstr(OpJNEQI, 5, 0, 5),                // 12
		NewInstr(OpGETMETHOD, 6, 3, 1),            // 13: the method name of r3, as a value
		NewInstr(OpMOV, 7, 3, 0),                  // 14
		NewInstr(OpCALL, 6, 1, 0),                 // 15: called with one argument too few
		NewInstr(OpRET, 4, 0, 0),                  // 16
	)
	if err := Verify(chunk); err != nil {
		t.Fatal(err)
	}
	_, err := NewVM().Exec(chunk)
	if err == nil || !strings.Contains(err.Error(), "Shape.name expects 2 arguments, got 1") {
		t.Fatalf("expected the method of the enum to be found and to refuse one argument, got %v", err)
	}

	// With the right number of arguments, through the same instruction
	// for values of two types in turn
	chunk = methodsChunk(
		NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed),   // 0
		NewInstr(OpNEWOBJ, 2, 4, ObjectIsNamed),   // 1
		NewInstr(OpNEWOBJ, 3, 5, ObjectIsVariant), // 2
		NewInstr(OpLOADI, 4, 0, 0),                // 3: sum
		NewInstr(OpLOADI, 5, 6, 0),                // 4: turns
		NewInstr(OpMOV, 7, 1, 0),                  // 5: a Circle...
		NewInstr(OpJNLTI, 5, 4, 8),                // 6: ...for the last three turns
		NewInstr(OpMOV, 7, 2, 0),                  // 7: a Square before
		NewInstr(OpCALLM, 6, 1, 0),                // 8: area() of whichever it is
		NewInstr(OpADD, 4, 4, 0),                  // 9
		NewInstr(OpSUBI, 5, 5, 1),                 // 10
		NewInstr(OpJNEQI, 5, 0, 5),                // 11
		NewInstr(OpMOV, 6, 3, 0),                  // 12
		NewInstr(OpLOADI, 7, 100, 0),              // 13
		NewInstr(OpCALLM, 5, 2, 1),                // 14: name(dot, 100) = 133
		NewInstr(OpADD, 4, 4, 0),                  // 15
		NewInstr(OpRET, 4, 0, 0),                  // 16
	)
	machine := NewVM()
	for round := 0; round < 3; round++ {
		result, err := machine.Exec(chunk)
		// Turns 6, 5, 4 are Squares (22 each), 3, 2, 1 Circles (11 each)
		if err != nil || !result.IsInt() || result.AsInt() != 3*22+3*11+133 {
			t.Fatalf("round %d: %v, %v", round, result, err)
		}
	}

	// The values hold nothing but what they were given: no method
	heap := NewHeap(0)
	obj, _, _ := heap.AllocObjectOf(heap.RootShape("Circle", false), 0)
	if obj.Len() != 0 {
		t.Errorf("a value of a type with methods holds %d fields", obj.Len())
	}
}

func TestAMethodThatIsNotThere(t *testing.T) {
	cases := []struct {
		name string
		make []Instr
		want string
	}{
		{"a type without that method", []Instr{NewInstr(OpNEWOBJ, 1, 4, ObjectIsNamed)}, "Square has no method 'name'"},
		{"a method no type has", []Instr{NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed), NewInstr(OpNOP, 0, 0, 0)}, "Circle has no method 'missing'"},
		{"a plain object", []Instr{NewInstr(OpNEWOBJ, 1, 0, 0)}, "struct has no method 'name'"},
		{"a number", []Instr{NewInstr(OpLOADI, 1, 5, 0)}, "Int has no method 'name'"},
		{"null", []Instr{NewInstr(OpLOADNULL, 1, 0, 0)}, "null has no method 'name'"},
		{"a text", []Instr{NewInstr(OpNEWSTR, 1, 0, 0)}, "String has no method 'name'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := uint16(1)
			if len(c.make) == 2 {
				name = 2
			}
			for _, call := range []Instr{NewInstr(OpCALLM, 0, 1, name), NewInstr(OpGETMETHOD, 2, 1, name)} {
				main := append(append([]Instr{}, c.make[0]), call, NewInstr(OpRET, 0, 0, 0))
				_, err := NewVM().Exec(methodsChunk(main...))
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("expected %q, got %v", c.want, err)
				}
			}
		})
	}

	// A function that captures variables is not a method, whatever its name
	_, err := NewVM().Exec(methodsChunk(
		NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed),
		NewInstr(OpCALLM, 0, 1, 2),
		NewInstr(OpRET, 0, 0, 0)))
	if err == nil || !strings.Contains(err.Error(), "has no method") {
		t.Errorf("expected no method to be found, got %v", err)
	}

	for name, damaged := range map[string]Instr{
		"a method without a value": NewInstr(OpCALLM, 0, 0, 0),
		"a name that is not there": NewInstr(OpCALLM, 0, 1, 99),
		"a value past the end":     NewInstr(OpGETMETHOD, 0, 9, 0),
	} {
		chunk := methodsChunk(NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed), damaged, NewInstr(OpRET, 0, 0, 0))
		if err := Verify(chunk); err == nil {
			t.Errorf("%s: the chunk was accepted", name)
		}
	}
}

// switched is a program that makes a value of each of four types and asks
// a SWITCH with cases for three of them where each goes: it returns the
// numbers the cases give, in an array, over two rounds.
func switched() *Chunk {
	chunk := NewChunk()
	chunk.StringPool = []string{"Shape.Circle", "Shape.Square", "Shape.Dot", "Other"}
	chunk.Constants = []Value{NewInt(0), NewInt(1), NewInt(2), NewInt(3)}
	chunk.Code = []Instr{
		NewInstr(OpNEWARRAY, 0, 8, 0),             // 0
		NewInstr(OpLOADI, 5, 0, 0),                // 1: turn
		NewInstr(OpNEWOBJ, 1, 0, ObjectIsVariant), // 2: a Circle...
		NewInstr(OpJNEQI, 5, 1, 5),                // 3
		NewInstr(OpNEWOBJ, 1, 1, ObjectIsVariant), // 4: ...a Square on turn 1
		NewInstr(OpJNEQI, 5, 2, 7),                // 5
		NewInstr(OpNEWOBJ, 1, 2, ObjectIsVariant), // 6: a Dot on turn 2
		NewInstr(OpJNEQI, 5, 3, 9),                // 7
		NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed),   // 8: something else on turn 3
		NewInstr(OpJNEQI, 5, 4, 11),               // 9
		NewInstr(OpLOADI, 1, 77, 0),               // 10: a number on turn 4
		NewInstr(OpSWITCH, 1, 3, 21),              // 11
		NewInstr(OpCASE, 0, 15, 0),                // 12: Circle
		NewInstr(OpCASE, 1, 17, 0),                // 13: Square
		NewInstr(OpCASE, 2, 19, 0),                // 14: Dot
		NewInstr(OpLOADI, 2, 10, 0),               // 15
		NewInstr(OpJMP, 22, 0, 0),                 // 16
		NewInstr(OpLOADI, 2, 20, 0),               // 17
		NewInstr(OpJMP, 22, 0, 0),                 // 18
		NewInstr(OpLOADI, 2, 30, 0),               // 19
		NewInstr(OpJMP, 22, 0, 0),                 // 20
		NewInstr(OpLOADI, 2, 99, 0),               // 21: none of them
		NewInstr(OpARRPUSH, 0, 2, 0),              // 22
		NewInstr(OpADDI, 5, 5, 1),                 // 23
		NewInstr(OpJNEQI, 5, 10, 26),              // 24: ten turns: five kinds, twice
		NewInstr(OpRET, 0, 0, 0),                  // 25
		NewInstr(OpJNLTI, 5, 5, 28),               // 26: the second five as the first
		NewInstr(OpJMP, 2, 0, 0),                  // 27
		NewInstr(OpSUBI, 5, 5, 5),                 // 28
		NewInstr(OpNEWOBJ, 1, 0, ObjectIsVariant), // 29
		NewInstr(OpJNEQI, 5, 1, 32),               // 30
		NewInstr(OpNEWOBJ, 1, 1, ObjectIsVariant), // 31
		NewInstr(OpJNEQI, 5, 2, 34),               // 32
		NewInstr(OpNEWOBJ, 1, 2, ObjectIsVariant), // 33
		NewInstr(OpJNEQI, 5, 3, 36),               // 34
		NewInstr(OpNEWOBJ, 1, 3, ObjectIsNamed),   // 35
		NewInstr(OpJNEQI, 5, 4, 38),               // 36
		NewInstr(OpLOADI, 1, 77, 0),               // 37
		NewInstr(OpADDI, 5, 5, 5),                 // 38
		NewInstr(OpJMP, 11, 0, 0),                 // 39: back to the same SWITCH
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 6}}
	return chunk
}

// A SWITCH goes to the case of the type of its value, and to the place for
// none of them for a value of another type or one that is not an object:
// the first time it meets a type, and every time after.
func TestASwitchGoesToTheCaseOfTheType(t *testing.T) {
	chunk := switched()
	if err := Verify(chunk); err != nil {
		t.Fatal(err)
	}
	machine := NewVM()
	for round := 0; round < 2; round++ {
		result, err := machine.Exec(chunk)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatValue(result); got != "[10, 20, 30, 99, 99, 10, 20, 30, 99, 99]" {
			t.Errorf("round %d: %s", round, got)
		}
	}

	for name, change := range map[string]func(*Chunk){
		"a case that is not one":          func(c *Chunk) { c.Code[13] = NewInstr(OpNOP, 0, 0, 0) },
		"more cases than there are":       func(c *Chunk) { c.Code[11].B = 60 },
		"a case outside the function":     func(c *Chunk) { c.Code[14].B = 500 },
		"a case into the cases":           func(c *Chunk) { c.Code[12].B = 13 },
		"a name that is not a text":       func(c *Chunk) { c.Constants[1] = NewNumber(2.5) },
		"nowhere to go for none of them":  func(c *Chunk) { c.Code[11].C = 500 },
		"a value in a register not there": func(c *Chunk) { c.Code[11].A = 9 },
		"a case without a switch":         func(c *Chunk) { c.Code[21] = NewInstr(OpCASE, 0, 22, 0) },
		"a jump into the cases":           func(c *Chunk) { c.Code[16].A = 13 },
	} {
		damaged := switched()
		change(damaged)
		if err := Verify(damaged); err == nil || !strings.Contains(err.Error(), "invalid bytecode") {
			t.Errorf("%s: expected the chunk to be refused, got %v", name, err)
		}
	}
}

// A text written in a program is one object, however often the program
// reaches it: making it again at every turn of a loop is what a loop that
// compares characters with "." used to spend its time on.
func TestATextWrittenInTheProgramIsMadeOnce(t *testing.T) {
	chunk := NewChunk()
	chunk.StringPool = []string{"same", "other"}
	chunk.Constants = []Value{NewInt(0), NewInt(1)}
	chunk.Code = []Instr{
		NewInstr(OpNEWARRAY, 0, 0, 0), // 0
		NewInstr(OpLOADI, 1, 300, 0),  // 1
		NewInstr(OpNEWSTR, 2, 0, 0),   // 2: "same", three hundred times
		NewInstr(OpNEWSTR, 3, 0, 0),   // 3: and from another instruction
		NewInstr(OpNEWSTR, 4, 1, 0),   // 4: "other"
		NewInstr(OpGC, 0, 0, 0),       // 5
		NewInstr(OpSUBI, 1, 1, 1),     // 6
		NewInstr(OpJNEQI, 1, 0, 2),    // 7
		NewInstr(OpARRPUSH, 0, 2, 0),  // 8
		NewInstr(OpARRPUSH, 0, 3, 0),  // 9
		NewInstr(OpARRPUSH, 0, 4, 0),  // 10
		NewInstr(OpRET, 0, 0, 0),      // 11
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(chunk.Code), RegisterCount: 5}}

	machine := NewVM()
	result, err := machine.Exec(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if FormatValue(result) != `["same", "same", "other"]` {
		t.Fatalf("got %s", FormatValue(result))
	}
	held := (*ArrayObj)(unsafe.Pointer(header(result)))
	if held.Get(0) != held.Get(1) || held.Get(0) == held.Get(2) {
		t.Error("the same text is two objects, or two texts are one")
	}
	// Two texts, and the array: not six hundred
	texts := 0
	for h := machine.heap.objects; h != nil; h = h.Next {
		if h.Type == ObjTypeString {
			texts++
		}
	}
	if texts != 2 {
		t.Errorf("%d texts were made for two written in the program", texts)
	}

	// Another program has its own, and what the first returned is whole
	machine.regs = []Value{result}
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatal(err)
	}
	if FormatValue(result) != `["same", "same", "other"]` {
		t.Errorf("after another program: %s", FormatValue(result))
	}
}

// Two texts joined are the text of both, whatever their lengths and
// whatever they hold: short ones come with their bytes in one piece.
func TestJoinedTextsAreWhatTheyAreMadeOf(t *testing.T) {
	if size := unsafe.Sizeof(StringObj{}); size > 48 {
		t.Errorf("a text takes %d bytes before its characters", size)
	}

	parts := []string{"", "a", "é", "日本", "🙂", "0123456789", "étoile filante", strings.Repeat("x", 15), strings.Repeat("y", 16),
		strings.Repeat("z", 17), strings.Repeat("ä", 16), strings.Repeat("w", 33), strings.Repeat("v", 64), strings.Repeat("u", 65), strings.Repeat("🙂", 40)}
	machine := NewVM()
	before := machine.heap.BytesAllocated()
	for _, a := range parts {
		for _, b := range parts {
			obj, _, err := machine.heap.AllocJoined(a, b)
			if err != nil {
				t.Fatal(err)
			}
			if obj.Data() != a+b || obj.header.Type != ObjTypeString {
				t.Fatalf("%q and %q joined are %q", a, b, obj.Data())
			}
			// Positions are those of the characters, as for any text
			runes := []rune(a + b)
			if obj.RuneLen() != len(runes) {
				t.Fatalf("%q + %q: %d characters, expected %d", a, b, obj.RuneLen(), len(runes))
			}
			for i := 0; i < len(runes); i += 1 + len(runes)/6 {
				if obj.RuneAt(i) != string(runes[i]) {
					t.Fatalf("%q + %q: character %d is %q", a, b, i, obj.RuneAt(i))
				}
			}
		}
	}

	// One allocation for a short one, the object and its bytes together
	for _, pair := range [][2]string{{"ab", "cd"}, {strings.Repeat("x", 20), "tail"}, {strings.Repeat("x", 40), strings.Repeat("y", 24)}} {
		allocations := testing.AllocsPerRun(100, func() { joined(pair[0], pair[1]) })
		if allocations != 1 {
			t.Errorf("joining %d and %d bytes: %v allocations", len(pair[0]), len(pair[1]), allocations)
		}
	}

	// Counted in, and out again when nothing holds them
	if machine.heap.BytesAllocated() <= before {
		t.Error("the joined texts were not counted")
	}
	machine.CollectGarbage()
	if left := machine.heap.BytesAllocated(); left != before {
		t.Errorf("%d bytes counted for texts that are gone", left-before)
	}

	// A text taken out of a joined one outlives it
	whole, _, _ := machine.heap.AllocJoined("hello, ", "world")
	piece, _ := machine.newString(whole.Slice(7, 12))
	machine.regs = []Value{piece}
	whole = nil
	for i := 0; i < 3; i++ {
		machine.CollectGarbage()
	}
	if s, _ := stringValue(piece); s != "world" {
		t.Errorf("the piece is %q", s)
	}
}
