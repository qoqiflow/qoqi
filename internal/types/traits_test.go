package types

import (
	"strings"
	"testing"
)

const traitDecls = `
trait Ordered { fn compare(other: Self) -> Int; }
trait Show { fn show() -> String; fn loud() -> String { return self.show(); } }
trait Source<T> { fn next() -> T?; }
trait Pairs<K, V> { fn pairs() -> [K]; fn value(key: K) -> V?; }

struct Version { n: Int; }
impl Ordered for Version { fn compare(other: Version) -> Int { return self.n - other.n; } }
impl Show for Version { fn show() -> String { return "v"; } fn loud() -> String { return "V"; } }
struct Point { x: Int; }
impl Ordered for Int { fn compare(other: Int) -> Int { return self - other; } }
impl Show for String { fn show() -> String { return self; } fn loud() -> String { return self; } }

struct Counter { n: Int; }
impl Source<Int> for Counter { fn next() -> Int? { return self.n; } }
struct Repeat<T> { item: T; }
impl<T> Source<T> for Repeat<T> { fn next() -> T? { return self.item; } }
struct Table<V> { keys: [String]; values: [V]; }
impl<V> Pairs<String, V> for Table<V> {
	fn pairs() -> [String] { return self.keys; }
	fn value(key: String) -> V? { return null; }
}

struct Sorted<T: Ordered> { items: [T]; }
impl<T: Ordered> Sorted<T> { fn first() -> T? { return null; } }
struct Box<T> { item: T; }
impl<T: Show> Box<T> { fn label() -> String { return self.item.show(); } }
impl<T> Box<T> { fn get() -> T { return self.item; } fn swap<U>(u: U) -> U { return u; } }
`

func checkTraits(t *testing.T, src string) []string {
	t.Helper()
	msgs, _, _ := checkSource(t, traitDecls+src)
	return msgs
}

func TestTraitRequirementsAreTyped(t *testing.T) {
	valid := []struct{ name, src string }{
		{"a method of the required trait", `fn f<T: Ordered>(a: T, b: T) -> Int { return a.compare(b); }`},
		{"its result has the types of the trait", `fn f<T: Show>(a: T) -> String { let s: String = a.show(); return a.loud(); }`},
		{"Self is the type parameter", `fn f<T: Ordered>(a: T) -> Int { return a.compare(a); }`},
		{"two requirements", `fn f<T: Ordered + Show>(a: T) -> String { if a.compare(a) == 0 { return a.show(); } return ""; }`},
		{"called with a struct that implements it", `fn f<T: Ordered>(a: T) -> T { return a; } fn g() -> Int { return f(Version { n: 1 }).n; }`},
		{"called with a number that implements it", `fn f<T: Ordered>(a: T) -> T { return a; } fn g() -> Int { return f(4) + 1; }`},
		{"passed on to another function that requires it", `fn f<T: Ordered>(a: T) -> Int { return a.compare(a); } fn g<U: Ordered + Show>(u: U) -> Int { return f(u); }`},
		{"a struct with a requirement", `fn f() -> Version? { let s = Sorted<Version> { items: [] }; return s.first(); }`},
		{"...of a type parameter that has it", `fn f<T: Ordered>(items: [T]) -> Sorted<T> { return Sorted<T> { items: items }; }`},
		{"methods only some have", `fn f(b: Box<Version>, c: Box<Point>) -> String { let p: Point = c.get(); return b.label(); }`},
		{"a value seen through a trait fits a requirement", `fn f<T: Show>(a: T) -> String { return a.show(); } fn g(s: Show) -> String { return f(s); }`},
		{"a number as a value of a trait", `fn f(o: Ordered) -> Int { return 1; } fn g() -> Int { return f(3); }`},
		{"a method a trait gives a number", `fn f() -> Int { return 5.compare(3) + __len("a".show()); }`},

		{"a trait with a type parameter", `fn f(s: Source<Int>) -> Int { return s.next() ?? 0; }`},
		{"the struct fits it with the right argument", `fn f(s: Source<Int>) -> Int { return 0; } fn g() -> Int { return f(Counter { n: 1 }) + f(Repeat<Int> { item: 2 }); }`},
		{"its argument is worked out from the struct", `fn first<T>(s: Source<T>) -> T? { return s.next(); } fn g() -> Int { let n: Int? = first(Counter { n: 1 }); let s: String? = first(Repeat<String> { item: "x" }); return 0; }`},
		{"...through a requirement", `fn first<T, S: Source<T>>(s: S) -> T? { return s.next(); } fn g() -> Int? { return first(Counter { n: 1 }); }`},
		{"two type parameters", `fn f(p: Pairs<String, Int>) -> Int? { return p.value(p.pairs()[0]); } fn g() -> Int? { return f(Table<Int> { keys: [], values: [] }); }`},
		{"in an array", `fn f() -> Int { let all: [Source<Int>] = [Counter { n: 1 }, Repeat<Int> { item: 2 }]; return all[0].next() ?? 0; }`},
		{"required of a type parameter", `fn f<S: Source<String>>(s: S) -> Int { return __len((s.next() ?? "")); } fn g() -> Int { return f(Repeat<String> { item: "x" }); }`},

		{"a field with a value of its own", `struct C { port: Int = 80; name: String; tags: [String] = []; next: C? = null; } fn f() -> C { return C { name: "x" }; }`},
		{"...given anyway", `struct C { port: Int = 80; } fn f() -> Int { return C { port: 1 }.port + C { }.port; }`},
		{"...of a generic struct", `struct S<T> { items: [T] = []; n: Int = 0; } fn f() -> [Int] { return S<Int> { }.items; }`},
		{"a method as a value", `fn f(v: Version) -> Int { let c: fn(Version) -> Int = v.compare; return c(v); }`},
		{"...of a field", `struct H { v: Version; } fn f(h: H) -> fn() -> String { return h.v.show; }`},
		{"...of a generic struct", `fn f(b: Box<Int>) -> Int { let get: fn() -> Int = b.get; return get(); }`},
		{"...handed to a function", `fn apply(g: fn() -> String) -> String { return g(); } fn f(v: Version) -> String { return apply(v.show); }`},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if msgs := checkTraits(t, tt.src); len(msgs) > 0 {
				t.Errorf("unexpected errors: %v", msgs)
			}
		})
	}

	invalid := []struct{ name, src, want string }{
		{"a method without a requirement", `fn f<T>(a: T) -> Int { return a.compare(a); }`, "nothing is known of a T: it has no method 'compare'"},
		{"a method of another trait", `fn f<T: Show>(a: T) -> Int { return a.compare(a); }`, "T is only known to implement Show, which has no method 'compare'"},
		{"an argument of another type parameter", `fn f<T: Ordered, U: Ordered>(a: T, b: U) -> Int { return a.compare(b); }`, "cannot use type U as argument of type T"},
		{"a struct that does not implement it", `fn f<T: Ordered>(a: T) -> T { return a; } fn g() -> Point { return f(Point { x: 1 }); }`, "Point does not implement Ordered, which function 'f' requires of T"},
		{"a number that does not implement it", `fn f<T: Show>(a: T) -> T { return a; } fn g() -> Int { return f(1); }`, "int does not implement Show"},
		{"an array is never what a trait is for", `fn f<T: Ordered>(a: T) -> T { return a; } fn g() -> [Int] { return f([1]); }`, "[int] does not implement Ordered"},
		{"one of two", `fn f<T: Ordered + Show>(a: T) -> T { return a; } fn g() -> Int { return f(1); }`, "int does not implement Show, which function 'f' requires of T"},
		{"not passed on", `fn f<T: Ordered>(a: T) -> T { return a; } fn g<U: Show>(u: U) -> U { return f(u); }`, "U does not implement Ordered"},
		{"a struct that requires it", `fn f() -> Int { let s = Sorted<Point> { items: [] }; return 0; }`, "Point does not implement Ordered, which struct Sorted requires of T"},
		{"...written as a type", `fn f(s: Sorted<Point>) -> Int { return 0; }`, "which struct Sorted requires of T"},
		{"...for a type parameter without it", `fn f<T>(items: [T]) -> Sorted<T> { return Sorted<T> { items: items }; }`, "T does not implement Ordered"},
		{"a method only some have", `fn f(b: Box<Point>) -> String { return b.label(); }`, "Point does not implement Show, which method 'label' requires of T"},
		{"a struct as a trait it does not implement", `fn f(o: Ordered) -> Int { return 1; } fn g() -> Int { return f(Point { x: 1 }); }`, "cannot use type Point as argument of type Ordered"},
		{"a method no trait gives a number", `fn f() -> String { return 5.show(); }`, "a number has no method 'show'"},

		{"the wrong argument of a trait", `fn f(s: Source<String>) -> Int { return 0; } fn g() -> Int { return f(Counter { n: 1 }); }`, "cannot use type Counter as argument of type Source<string>"},
		{"...of a generic struct", `fn f(s: Source<String>) -> Int { return 0; } fn g() -> Int { return f(Repeat<Int> { item: 1 }); }`, "as argument of type Source<string>"},
		{"what a method of the trait gives", `fn f(s: Source<Int>) -> String { return s.next() ?? 0; }`, "cannot return type int from function returning string"},
		{"two traits of one name are two types", `fn f(a: Source<Int>) -> Source<String> { return a; }`, "cannot return type Source<int> from function returning Source<string>"},
		{"too few type arguments in an impl", `struct C { } impl Source for C { fn next() -> Int? { return 1; } }`, "trait Source takes 1 type arguments (<T>), got 0"},
		{"too many", `struct C { } impl Ordered<Int> for C { fn compare(other: C) -> Int { return 1; } }`, "trait Ordered takes no type arguments"},
		{"a method that does not fit the argument", `struct C { } impl Source<String> for C { fn next() -> Int? { return 1; } }`, "method 'next' returns int?, trait requires string?"},
		{"a parameter that does not fit", `struct C { } impl Pairs<Int, Int> for C { fn pairs() -> [Int] { return []; } fn value(key: String) -> Int? { return 1; } }`, "parameter 'key' has type string, trait requires int"},
		{"a trait implemented twice", `struct C { } impl Source<Int> for C { fn next() -> Int? { return 1; } } impl Source<String> for C { fn next() -> String? { return ""; } }`, "C already implements Source"},
		{"a required method left out", `struct C { } impl Show for C { fn loud() -> String { return ""; } }`, "missing required method 'show'"},

		{"a field's own value of the wrong type", `struct C { port: Int = "x"; }`, "field 'port' is of type int, and its value when none is given is of type string"},
		{"a field without one, left out", `struct C { port: Int = 80; name: String; } fn f() -> C { return C { port: 1 }; }`, "struct literal of C is missing 'name'"},
		{"a method of a computed value as a value", `fn f() -> fn() -> String { return Version { n: 1 }.show; }`, "a method can be used as a value on a variable or a field"},
		{"a method with type parameters as a value", `fn f(b: Box<Int>) -> Int { let g = b.swap; return 0; }`, "method 'swap' has type parameters of its own"},
		{"a method value of the wrong type", `fn f(v: Version) -> fn() -> Int { return v.show; }`, "cannot return type fn() -> string"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			msgs := checkTraits(t, tt.src)
			if len(msgs) == 0 {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(strings.ToLower(strings.Join(msgs, "\n")), strings.ToLower(tt.want)) {
				t.Errorf("expected an error containing %q, got %q", tt.want, msgs)
			}
		})
	}
}

func TestTypeKeyNamesWhatAnImplIsFor(t *testing.T) {
	for typ, want := range map[Type]string{
		Int: "Int", Float: "Float", String: "String", Bool: "Bool", Void: "", Any: "", Null: "",
		&StructType{Name: "Point", BaseName: "Point"}:            "Point",
		&StructType{Name: "Box", BaseName: "Box", TypeArgs: nil}: "Box",
		NewArray(Int):                 "[]",
		NewArray(NewArray(String)):    "[]",
		NewTypeParam("T"):             "",
		NewBehavior("Ordered", nil):   "",
		NewFunction([]Type{Int}, Int): "",
	} {
		if got := typeKey(typ); got != want {
			t.Errorf("typeKey(%s): expected %q, got %q", typ, want, got)
		}
	}
	a, b := NewBehavior("Source", nil), NewBehavior("Source", nil)
	a.TypeArgs, b.TypeArgs = []Type{Int}, []Type{String}
	if a.Equals(b) || !a.Equals(a) || a.Equals(NewBehavior("Source", nil)) || describe(a) != "Source<Int>" {
		t.Errorf("expected a trait to be told apart by its type arguments, got %s and %s", describe(a), describe(b))
	}
	if !IsBuiltinTypeName("Int") || IsBuiltinTypeName("Point") || IsBuiltinTypeName("Any") ||
		!IsBuiltinTypeName("[]") || !IsBuiltinTypeName("[Int]") || !IsBuiltinTypeName("[Point]") {
		t.Error("IsBuiltinTypeName must name the builtin types an impl can be for, arrays among them")
	}
}
