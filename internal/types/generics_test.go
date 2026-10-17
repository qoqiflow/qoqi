package types

import (
	"testing"
)

func TestGenericStructInstantiation(t *testing.T) {
	// struct Box<T> { value: T }
	T := NewTypeParam("T")
	boxDef := NewGenericStruct("Box", []string{"T"}, []Field{
		{"value", T, true},
	})

	if boxDef.String() != "struct Box<T> { pub value: T }" {
		t.Errorf("Unexpected string representation: %s", boxDef.String())
	}

	// Instantiate Box<Int>
	bindings := map[string]Type{"T": Int}
	boxInt := Instantiate(boxDef, bindings)

	expectedName := "Box[int]"
	if s, ok := boxInt.(*StructType); !ok || s.Name != expectedName {
		t.Errorf("Expected struct name %s, got %s", expectedName, s.Name)
	}

	if s, ok := boxInt.(*StructType); ok {
		if len(s.Fields) != 1 {
			t.Errorf("Expected 1 field, got %d", len(s.Fields))
		}
		if !s.Fields[0].Type.Equals(Int) {
			t.Errorf("Expected field type Int, got %s", s.Fields[0].Type.String())
		}
		if len(s.TypeParams) != 0 {
			t.Errorf("Expected 0 type params, got %d", len(s.TypeParams))
		}
	}
}

func TestGenericFunctionInstantiation(t *testing.T) {
	// fn identity<T>(val: T) -> T
	T := NewTypeParam("T")
	identityDef := NewGenericFunction([]string{"T"}, []Type{T}, T)

	if identityDef.String() != "fn<T>(T) -> T" {
		t.Errorf("Unexpected string representation: %s", identityDef.String())
	}

	// Instantiate identity<String>
	bindings := map[string]Type{"T": String}
	identityStr := Instantiate(identityDef, bindings)

	expectedStr := "fn(string) -> string"
	if identityStr.String() != expectedStr {
		t.Errorf("Expected %s, got %s", expectedStr, identityStr.String())
	}
}

func TestNestedGenerics(t *testing.T) {
	// struct Pair<K, V> { key: K, value: V }
	K := NewTypeParam("K")
	V := NewTypeParam("V")
	pairDef := NewGenericStruct("Pair", []string{"K", "V"}, []Field{
		{"key", K, true},
		{"value", V, true},
	})

	// struct Wrapper<T> { inner: Pair<T, T> }
	T := NewTypeParam("T")
	// We need to represent Pair<T, T> as an instantiation of Pair with T
	// But here we are DEFINING Wrapper.
	// So the field type of `inner` is `Instantiate(pairDef, {"K": T, "V": T})`
	pairOfT := Instantiate(pairDef, map[string]Type{"K": T, "V": T})

	wrapperDef := NewGenericStruct("Wrapper", []string{"T"}, []Field{
		{"inner", pairOfT, true},
	})

	// Instantiate Wrapper<Int>
	wrapperInt := Instantiate(wrapperDef, map[string]Type{"T": Int})

	// Check Wrapper[int]
	s, ok := wrapperInt.(*StructType)
	if !ok {
		t.Fatalf("Expected StructType")
	}
	if s.Name != "Wrapper[int]" {
		t.Errorf("Expected name Wrapper[int], got %s", s.Name)
	}

	// Check inner field: should be Pair[int, int]
	innerField := s.Fields[0]
	innerType, ok := innerField.Type.(*StructType)
	if !ok {
		t.Fatalf("Expected inner field to be StructType")
	}
	if innerType.Name != "Pair[int, int]" {
		t.Errorf("Expected inner type name Pair[int, int], got %s", innerType.Name)
	}

	if !innerType.Fields[0].Type.Equals(Int) {
		t.Errorf("Expected Pair key to be Int")
	}
}
