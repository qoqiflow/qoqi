package types

import (
	"testing"
)

func TestPrimitives(t *testing.T) {
	if !Int.Equals(Int) {
		t.Error("Int should equal Int")
	}
	if Int.Equals(Float) {
		t.Error("Int should not equal Float")
	}
	if !Any.Equals(Any) {
		t.Error("Any should equal Any")
	}
}

func TestStructs(t *testing.T) {
	s1 := NewStruct("Point", []Field{
		{"x", Int, true},
		{"y", Int, true},
	})
	s2 := NewStruct("Point", []Field{
		{"x", Int, true},
		{"y", Int, true},
	})
	s3 := NewStruct("Person", []Field{
		{"name", String, false},
	})

	if !s1.Equals(s2) {
		t.Error("Structs with same name should be equal")
	}
	if s1.Equals(s3) {
		t.Error("Structs with different names should not be equal")
	}

	// Test String representation with visibility
	expected := "struct Point { pub x: int, pub y: int }"
	if s1.String() != expected {
		t.Errorf("String() = %s; want %s", s1.String(), expected)
	}

	expectedPrivate := "struct Person { name: string }"
	if s3.String() != expectedPrivate {
		t.Errorf("String() = %s; want %s", s3.String(), expectedPrivate)
	}
}

func TestIsAssignable(t *testing.T) {
	registry := NewImplRegistry()

	// Define types
	myError := NewStruct("MyError", []Field{{"message", String, true}})
	otherStruct := NewStruct("Other", nil)

	errorLike := NewBehavior("ErrorLike", []Method{
		{"Error", NewFunction(nil, String), true},
	})
	stringable := NewBehavior("Stringable", []Method{
		{"ToString", NewFunction(nil, String), true},
	})

	// Register implementations
	registry.Register("MyError", "ErrorLike")
	registry.Register("MyError", "Stringable")

	tests := []struct {
		name     string
		target   Type
		source   Type
		expected bool
	}{
		{"Int -> Int", Int, Int, true},
		{"Int -> Float", Float, Int, false},
		{"Any -> Int", Any, Int, true},

		// Struct -> Behavior
		{"MyError -> ErrorLike", errorLike, myError, true},
		{"Other -> ErrorLike", errorLike, otherStruct, false},

		// Unions
		{"MyError -> (MyError | Int)", NewUnion(myError, Int), myError, true},
		{"Int -> (MyError | Int)", NewUnion(myError, Int), Int, true},
		{"Float -> (MyError | Int)", NewUnion(myError, Int), Float, false},

		// Union -> Union
		{"(Int) -> (Int | Float)", NewUnion(Int, Float), NewUnion(Int), true},
		{"(Int | Float) -> (Int | Float | Bool)", NewUnion(Int, Float, Bool), NewUnion(Int, Float), true},
		{"(Int | Bool) -> (Int | Float)", NewUnion(Int, Float), NewUnion(Int, Bool), false}, // Bool not in target

		// Intersections
		{"MyError -> (ErrorLike & Stringable)", NewIntersection(errorLike, stringable), myError, true},
		{"MyError -> (ErrorLike & Other)", NewIntersection(errorLike, NewBehavior("Other", nil)), myError, false},

		// Intersection -> Behavior
		{"(ErrorLike & Stringable) -> ErrorLike", errorLike, NewIntersection(errorLike, stringable), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAssignable(tt.target, tt.source, registry); got != tt.expected {
				t.Errorf("IsAssignable(%s, %s) = %v; want %v", tt.target, tt.source, got, tt.expected)
			}
		})
	}
}

func TestIsCastable(t *testing.T) {
	registry := NewImplRegistry()

	// Define types
	myError := NewStruct("MyError", []Field{{"message", String, true}})
	otherStruct := NewStruct("Other", nil)

	errorLike := NewBehavior("ErrorLike", []Method{
		{"Error", NewFunction(nil, String), true},
	})
	stringable := NewBehavior("Stringable", []Method{
		{"ToString", NewFunction(nil, String), true},
	})

	// Register implementations
	registry.Register("MyError", "ErrorLike")
	registry.Register("MyError", "Stringable")

	tests := []struct {
		name     string
		target   Type
		source   Type
		expected bool
	}{
		// Upcasts (Safe)
		{"MyError -> ErrorLike", errorLike, myError, true},
		{"Int -> Any", Any, Int, true},

		// Downcasts (Unsafe but allowed)
		{"Any -> Int", Int, Any, true},
		{"ErrorLike -> MyError", myError, errorLike, true},    // Allowed because MyError implements ErrorLike
		{"ErrorLike -> Other", otherStruct, errorLike, false}, // Not allowed: Other doesn't implement ErrorLike

		// Side-casts
		{"ErrorLike -> Stringable", stringable, errorLike, true}, // Allowed (runtime check)

		// Unions
		{"(MyError | Int) -> MyError", myError, NewUnion(myError, Int), true},
		{"(MyError | Int) -> Float", Float, NewUnion(myError, Int), false},

		// Primitives
		{"Int -> Float", Float, Int, true},
		{"Float -> Int", Int, Float, true},
		{"Int -> Bool", Bool, Int, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCastable(tt.target, tt.source, registry); got != tt.expected {
				t.Errorf("IsCastable(%s, %s) = %v; want %v", tt.target, tt.source, got, tt.expected)
			}
		})
	}
}

func TestVariadicFunctions(t *testing.T) {
	// fn(int, ...string) -> void
	v1 := NewVariadicFunction([]Type{Int, String}, Void)
	// fn(int, string) -> void
	n1 := NewFunction([]Type{Int, String}, Void)

	if v1.Equals(n1) {
		t.Error("Variadic function should not equal non-variadic function with same params")
	}

	expected := "fn(int, ...string) -> void"
	if v1.String() != expected {
		t.Errorf("String() = %s; want %s", v1.String(), expected)
	}
}

func TestGenericVariadicFunctions(t *testing.T) {
	// fn<T>(...T) -> void
	T := NewTypeParam("T")
	v1 := NewGenericVariadicFunction([]string{"T"}, []Type{T}, Void)

	expected := "fn<T>(...T) -> void"
	if v1.String() != expected {
		t.Errorf("String() = %s; want %s", v1.String(), expected)
	}

	if !v1.IsVariadic {
		t.Error("Should be variadic")
	}

	if len(v1.TypeParams) != 1 || v1.TypeParams[0] != "T" {
		t.Error("Should have type param T")
	}
}

func TestSelfType(t *testing.T) {
	if !Self.Equals(Self) {
		t.Error("Self should equal Self")
	}
	if Self.String() != "Self" {
		t.Errorf("String() = %s; want Self", Self.String())
	}
	if Self.Equals(Int) {
		t.Error("Self should not equal Int")
	}
}
