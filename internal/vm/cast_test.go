package vm

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestParseCastType(t *testing.T) {
	// A descriptor and how the type it describes reads in a message
	valid := map[string]string{
		"int":                              "Int",
		"float":                            "Float",
		"bool":                             "Bool",
		"string":                           "String",
		"null":                             "null",
		"any":                              "Any",
		"function":                         "function",
		"?int":                             "Int?",
		"[int]":                            "[Int]",
		"[[string]]":                       "[[String]]",
		"[?int]":                           "[Int?]",
		"?[int]":                           "[Int]?",
		"{Point}":                          "Point",
		"{Circle|Square}":                  "Circle or Square",
		"{}":                               "a type nothing implements",
		"{Box:value=int}":                  "Box",
		"{Pair:first=int,second=[string]}": "Pair",
		"{List:head=int,tail=?{List}}":     "List",
		"[{Box:value=?{Point}}]":           "[Box]",
		"fn()->void":                       "fn() -> Void",
		"fn(int)->int":                     "fn(Int) -> Int",
		"fn(string,[int])->?bool":          "fn(String, [Int]) -> Bool?",
		"fn(string,...any)->void":          "fn(String, ...Any) -> Void",
		"fn(...any)->void":                 "fn(...Any) -> Void",
		"fn(fn(int)->int)->{Point}":        "fn(fn(Int) -> Int) -> Point",
		"[fn(any)->string]":                "[fn(Any) -> String]",
		"{Box:value=fn(int)->int}":         "Box",
	}
	for descriptor, want := range valid {
		typ, err := parseCastType(descriptor)
		if err != nil {
			t.Errorf("%q: unexpected error %v", descriptor, err)
			continue
		}
		if got := typ.String(); got != want {
			t.Errorf("%q: expected it to read %q, got %q", descriptor, want, got)
		}
	}

	invalid := []string{
		"", "integer", "Int", "[int", "int]", "[]", "?", "{Point", "{Box:value}",
		"{Box:value=}", "{Box:=int}", "{Box:value=int", "{Box:value=int;x=int}", "int int", "[int]x",
		"fn", "fn(", "fn()", "fn()->", "fn(int", "fn(int,)->int", "fn(int)int", "fn(...int,int)->int", "fn(int)->int)",
	}
	for _, descriptor := range invalid {
		if typ, err := parseCastType(descriptor); err == nil {
			t.Errorf("%q: expected an error, parsed as %s", descriptor, typ)
		}
	}
}

func TestCastMismatch(t *testing.T) {
	heap := NewHeap(0)
	ptr := func(obj Obj) Value { return NewHeapPtr(uintptr(unsafe.Pointer(obj.Header()))) }
	str := func(s string) Value {
		obj, _, _ := heap.AllocString(s)
		return ptr(obj)
	}
	array := func(elems ...Value) Value {
		obj, _, _ := heap.AllocArray(len(elems))
		for _, elem := range elems {
			obj.Append(elem)
		}
		return ptr(obj)
	}
	object := func(name string, fields map[string]Value) Value {
		obj, _, _ := heap.AllocObjectOf(heap.RootShape(name, false), 0)
		for field, value := range fields {
			obj.Set(field, value)
		}
		return ptr(obj)
	}
	fn, _, _ := heap.AllocExternFunction("f", nil, 0)

	// An array that contains itself
	loopObj, _, _ := heap.AllocArray(1)
	loop := ptr(loopObj)
	loopObj.Append(loop)

	one, two := NewInt(1), NewInt(2)
	point := object("Point", map[string]Value{"x": one})
	intBox := object("Box", map[string]Value{"value": one})
	strBox := object("Box", map[string]Value{"value": str("s")})
	list := object("List", map[string]Value{"head": one, "tail": object("List", map[string]Value{"head": two, "tail": NewNull()})})
	badList := object("List", map[string]Value{"head": str("s"), "tail": NewNull()})

	tests := []struct {
		name       string
		value      Value
		descriptor string
		want       string // "" when the cast succeeds
	}{
		// Scalars
		{"int", one, "int", ""},
		{"int is not a float", one, "float", "Int is not a Float"},
		{"float", NewNumber(1.5), "float", ""},
		{"float is not an int", NewNumber(1), "int", "Float is not an Int"},
		{"bool", NewBool(false), "bool", ""},
		{"string", str("s"), "string", ""},
		{"string is not an int", str("1"), "int", "String is not an Int"},
		{"null is not an int", NewNull(), "int", "null is not an Int"},
		{"anything is any", point, "any", ""},
		{"function", ptr(fn), "function", ""},
		{"array is not a function", array(), "function", "array is not a function"},

		// Optionals
		{"null as optional", NewNull(), "?int", ""},
		{"value as optional", one, "?int", ""},
		{"wrong value as optional", str("s"), "?int", "String is not an Int"},

		// Arrays: every element is verified
		{"empty array fits any element type", array(), "[int]", ""},
		{"array of ints", array(one, two), "[int]", ""},
		{"array with a wrong element", array(one, str("s"), two), "[int]", "element 1: String is not an Int"},
		{"array is not a scalar", array(one), "int", "array is not an Int"},
		{"scalar is not an array", one, "[int]", "Int is not a [Int]"},
		{"nested arrays", array(array(one), array()), "[[int]]", ""},
		{"nested array with a wrong element", array(array(one), array(NewBool(true))), "[[int]]", "element 1: element 0: Bool is not an Int"},
		{"array of optionals", array(one, NewNull()), "[?int]", ""},
		{"null in a plain array", array(one, NewNull()), "[int]", "element 1: null is not an Int"},
		{"array of any", array(one, str("s"), NewNull()), "[any]", ""},
		{"array of structs", array(point, point), "[{Point}]", ""},
		{"array with a wrong struct", array(point, intBox), "[{Point}]", "element 1: Box is not a Point"},
		// The check ends where the descriptor does, even though the value never does
		{"self-containing array", loop, "[[[int]]]", "element 0: element 0: element 0: array is not an Int"},
		{"self-containing array of anything", loop, "[[any]]", ""},

		// Structs
		{"struct", point, "{Point}", ""},
		{"one of several structs", point, "{Circle|Point}", ""},
		{"another struct", point, "{Circle|Square}", "Point is not a Circle or Square"},
		{"struct is not a scalar", point, "int", "Point is not an Int"},
		{"scalar is not a struct", one, "{Point}", "Int is not a Point"},
		{"nothing implements the trait", point, "{}", "Point is not a type nothing implements"},
		{"object without a struct name", object("", nil), "{Point}", "struct is not a Point"},

		// Generic structs are verified through their fields
		{"generic struct with matching field", intBox, "{Box:value=int}", ""},
		{"generic struct with wrong field", strBox, "{Box:value=int}", "field 'value': String is not an Int"},
		{"generic struct, other instantiation", strBox, "{Box:value=string}", ""},
		{"struct with a missing field", object("Box", nil), "{Box:value=int}", "field 'value': null is not an Int"},
		{"recursive struct, first level", list, "{List:head=int,tail=?{List}}", ""},
		{"recursive struct with wrong head", badList, "{List:head=int,tail=?{List}}", "field 'head': String is not an Int"},
		{"array of generic structs", array(intBox, strBox), "[{Box:value=int}]", "element 1: field 'value': String is not an Int"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, err := parseCastType(tt.descriptor)
			if err != nil {
				t.Fatalf("invalid descriptor in test: %v", err)
			}
			if got := typ.mismatch(tt.value); got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCastIntrinsic(t *testing.T) {
	// r1 = value under test; then __cast(r1, descriptor), result in r0
	run := func(load []Instr, descriptor string) (Value, error) {
		code := append(append([]Instr{}, load...),
			NewInstr(OpNEWEXFUNC, 2, 4, 0),
			NewInstr(OpMOV, 3, 1, 0),
			NewInstr(OpNEWSTR, 4, 2, 0),
			NewInstr(OpCALL, 2, 2, 0),
			NewInstr(OpRET, 0, 0, 0),
		)
		result, _, err := execMain(t, []string{"Point", "text", descriptor},
			[]Value{NewInt(0), NewInt(1), NewInt(2)}, code...)
		return result, err
	}

	loadInt := []Instr{NewInstr(OpLOADI, 1, 7, 0)}
	loadString := []Instr{NewInstr(OpNEWSTR, 1, 1, 0)}
	// NEWOBJ with C = 1 names the struct after constant B ("Point")
	loadPoint := []Instr{NewInstr(OpNEWOBJ, 1, 0, 1)}
	loadPlainObject := []Instr{NewInstr(OpNEWOBJ, 1, 0, 0)}
	loadMixedArray := []Instr{
		NewInstr(OpNEWARRAY, 1, 2, 0),
		NewInstr(OpLOADI, 5, 1, 0),
		NewInstr(OpARRPUSH, 1, 5, 0),
		NewInstr(OpNEWSTR, 5, 1, 0),
		NewInstr(OpARRPUSH, 1, 5, 0),
	}

	t.Run("a successful cast returns the value", func(t *testing.T) {
		result, err := run(loadInt, "int")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsInt() || result.AsInt() != 7 {
			t.Errorf("expected 7, got %s", FormatValue(result))
		}
	})

	failures := []struct {
		name       string
		load       []Instr
		descriptor string
		want       string
	}{
		{"wrong kind", loadString, "int", "cannot cast String to Int"},
		{"wrong struct", loadPoint, "{Circle}", "cannot cast Point to Circle"},
		{"unnamed object", loadPlainObject, "{Point}", "cannot cast struct to Point"},
		{"wrong element", loadMixedArray, "[int]", "cannot cast array to [Int]: element 1: String is not an Int"},
		{"malformed descriptor", loadInt, "[int", "invalid cast descriptor"},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(tt.load, tt.descriptor)
			if err == nil {
				t.Fatalf("expected error %q, got none", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got %v", tt.want, err)
			}
			// The user wrote `as`, not a call
			if strings.Contains(err.Error(), "__cast") {
				t.Errorf("error should not mention the intrinsic's name: %v", err)
			}
		})
	}

	t.Run("named and plain objects", func(t *testing.T) {
		if _, err := run(loadPoint, "{Point}"); err != nil {
			t.Errorf("expected a Point to cast to Point, got %v", err)
		}
	})
}

func TestCastDescriptorsAreParsedOnce(t *testing.T) {
	machine := NewVM()
	for i := 0; i < 3; i++ {
		if _, err := machine.cast(NewInt(1), "[int]"); err == nil {
			t.Fatal("expected an Int not to cast to [Int]")
		}
	}
	if len(machine.castTypes) != 1 {
		t.Errorf("expected 1 cached descriptor, got %d", len(machine.castTypes))
	}
}

func TestCastSubtyping(t *testing.T) {
	// Whether every value of the first type is also one of the second.
	// This decides if a function's recorded type fits the type asked for.
	tests := []struct {
		sub, super string
		want       bool
	}{
		{"int", "int", true},
		{"int", "float", false},
		{"int", "any", true},
		{"any", "int", false},
		{"any", "any", true},
		{"int", "?int", true},
		{"null", "?int", true},
		{"?int", "?int", true},
		{"?int", "int", false},
		{"?int", "?any", true},
		{"string", "?int", false},
		{"[int]", "[int]", true},
		{"[int]", "[any]", true},
		{"[any]", "[int]", false},
		{"[int]", "int", false},
		{"{Square}", "{Circle|Square}", true},
		{"{Circle|Square}", "{Square}", false},
		{"{Square}", "{Circle}", false},
		{"{}", "{Square}", false},
		{"{Box:value=int}", "{Box:value=int}", true},
		{"{Box:value=int}", "{Box:value=string}", false},
		{"{Box:value=int}", "{Box}", true},
		{"void", "void", true},
		{"void", "int", false},
		{"fn(int)->int", "fn(int)->int", true},
		{"fn(any)->string", "fn(int)->string", true},  // accepts more than required
		{"fn(int)->string", "fn(any)->string", false}, // accepts less than required
		{"fn(int)->int", "fn(int)->any", true},        // returns something more specific
		{"fn(int)->any", "fn(int)->int", false},
		{"fn(int)->int", "fn(int,int)->int", false},
		{"fn(...any)->void", "fn(...int)->void", true},
		{"fn(...any)->void", "fn(any)->void", false}, // variadic and fixed do not mix
		{"fn(int)->void", "fn(int)->int", false},
		{"fn(fn(int)->int)->int", "fn(fn(any)->int)->int", true},
	}

	for _, tt := range tests {
		sub, err1 := parseCastType(tt.sub)
		super, err2 := parseCastType(tt.super)
		if err1 != nil || err2 != nil {
			t.Fatalf("invalid descriptors in test: %v %v", err1, err2)
		}
		if got := sub.subtypeOf(super); got != tt.want {
			t.Errorf("%s as a %s: expected %v, got %v", tt.sub, tt.super, tt.want, got)
		}
	}
}

func TestCastFunctionSignatures(t *testing.T) {
	tests := []struct {
		name      string
		signature string // recorded for the function value
		target    string // type the cast asks for
		want      string
	}{
		{"same signature", "fn(int)->int", "fn(int)->int", ""},
		{"wider parameter", "fn(any)->string", "fn(int)->string", ""},
		{"narrower result", "fn(int)->{Square}", "fn(int)->{Circle|Square}", ""},
		{"other parameter type", "fn(string)->string", "fn(int)->int", "it is a fn(String) -> String"},
		{"other result type", "fn(int)->string", "fn(int)->int", "it is a fn(Int) -> String"},
		{"other arity", "fn(int,int)->int", "fn(int)->int", "it is a fn(Int, Int) -> Int"},
		{"narrower parameter", "fn(int)->string", "fn(any)->string", "it is a fn(Int) -> String"},
		{"variadic as fixed", "fn(...any)->void", "fn(any)->void", "it is a fn(...Any) -> Void"},
		{"no recorded type", "", "fn(int)->int", "the function has no recorded type"},
		{"unusable recorded type", "int", "fn(int)->int", "the function has no usable type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := parseCastType(tt.target)
			if err != nil {
				t.Fatalf("invalid descriptor in test: %v", err)
			}
			if got := target.signatureMismatch(tt.signature); got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCastToFunctionTypeUsesRecordedSignature(t *testing.T) {
	// r1 = a function value; then __cast(r1, descriptor) or __is(r1, descriptor)
	run := func(load Instr, intrinsic uint16, descriptor string) (Value, error) {
		chunk := NewChunk()
		chunk.StringPool = []string{descriptor}
		chunk.Constants = []Value{NewInt(0)}
		chunk.ExternNames = []string{"toString", "__cast", "__is"}
		chunk.Code = []Instr{
			load,
			NewInstr(OpNEWEXFUNC, 2, intrinsic, 0),
			NewInstr(OpMOV, 3, 1, 0),
			NewInstr(OpNEWSTR, 4, 0, 0),
			NewInstr(OpCALL, 2, 2, 0),
			NewInstr(OpRET, 0, 0, 0),
			NewInstr(OpRET, 0, 0, 0), // body of `inc` and `plain`
		}
		chunk.Prototypes = []FunctionPrototype{
			{Name: "main", CodeSize: 6, RegisterCount: 6},
			{Name: "inc", CodeStart: 6, CodeSize: 1, RegisterCount: 1, ArgCount: 1, Signature: "fn(int)->int"},
			{Name: "plain", CodeStart: 6, CodeSize: 1, RegisterCount: 1, ArgCount: 1},
		}
		return NewVM().Exec(chunk)
	}

	const cast, is = 1, 2
	loadInc := NewInstr(OpNEWFUNC, 1, 1, 0)
	loadPlain := NewInstr(OpNEWFUNC, 1, 2, 0)      // written in assembly: no signature
	loadToString := NewInstr(OpNEWEXFUNC, 1, 0, 0) // host function with a declared type

	succeed := []struct {
		name       string
		load       Instr
		descriptor string
	}{
		{"compiled function", loadInc, "fn(int)->int"},
		{"compiled function, wider result", loadInc, "fn(int)->any"},
		{"host function", loadToString, "fn(any)->string"},
		{"host function, narrower parameter", loadToString, "fn(int)->string"},
		{"just a function", loadPlain, "function"},
	}
	for _, tt := range succeed {
		if _, err := run(tt.load, cast, tt.descriptor); err != nil {
			t.Errorf("%s: expected the cast to succeed, got %v", tt.name, err)
		}
		if result, err := run(tt.load, is, tt.descriptor); err != nil || !result.IsBool() || !result.AsBool() {
			t.Errorf("%s: expected the type test to be true, got %s, %v", tt.name, FormatValue(result), err)
		}
	}

	fail := []struct {
		name       string
		load       Instr
		descriptor string
		want       string
	}{
		{"other signature", loadInc, "fn(string)->int", "cannot cast function to fn(String) -> Int: it is a fn(Int) -> Int"},
		{"host function, other result", loadToString, "fn(any)->int", "it is a fn(Any) -> String"},
		{"function without a type", loadPlain, "fn(int)->int", "the function has no recorded type"},
	}
	for _, tt := range fail {
		_, err := run(tt.load, cast, tt.descriptor)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: expected error containing %q, got %v", tt.name, tt.want, err)
		}
		// The same question as a test is answered, not failed
		if result, err := run(tt.load, is, tt.descriptor); err != nil || !result.IsBool() || result.AsBool() {
			t.Errorf("%s: expected the type test to be false, got %s, %v", tt.name, FormatValue(result), err)
		}
	}
}

func TestFunctionSignatureSurvivesSerialization(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{NewInstr(OpRET, 0, 0, 0)}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeSize: 1, RegisterCount: 1, Signature: "fn()->int"},
		{Name: "helper", CodeSize: 1, RegisterCount: 1, ArgCount: 2, UpvalueCount: 1, Signature: "fn(string,[int])->?bool"},
		{Name: "untyped", CodeSize: 1, RegisterCount: 1},
	}

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}
	loaded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}

	for i, want := range chunk.Prototypes {
		if got := loaded.Prototypes[i]; !reflect.DeepEqual(got, want) {
			t.Errorf("prototype %d: expected %+v, got %+v", i, want, got)
		}
	}

	// A file of an older format version is refused, not misread
	data[4] = 1
	if _, err := DeserializeChunk(data); err == nil || !strings.Contains(err.Error(), "unsupported bytecode version") {
		t.Errorf("expected a version error for an old file, got %v", err)
	}
}
