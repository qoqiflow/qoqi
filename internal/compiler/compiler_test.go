package compiler

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// runProgram compiles and executes source, returning what it printed, the
// value main returned and the runtime error, if any.
func runProgram(t *testing.T, src string) (string, vm.Value, error) {
	t.Helper()

	errs := errors.NewHandler()
	result := Compile(src, "test.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}
	if result.Chunk == nil {
		t.Fatal("compilation produced no bytecode")
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	value, err := machine.Exec(result.Chunk)
	return out.String(), value, err
}

// expectOutput runs the body of a main function and compares its output.
// decls holds any declarations the body needs.
func expectOutput(t *testing.T, decls, body, want string) {
	t.Helper()

	// The lengths of arrays and texts come from the standard library, and
	// most programs want one of them
	src := "import std.arrays;\nimport std.strings;\n" + decls + "\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n"
	out, _, err := runProgram(t, src)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
}

func TestMainReturnValue(t *testing.T) {
	_, value, err := runProgram(t, "fn main() -> Int { return 6 * 7; }")
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if !value.IsInt() || value.AsInt() != 42 {
		t.Errorf("expected main to return 42, got %s", vm.FormatValue(value))
	}
}

func TestArithmeticAndComparison(t *testing.T) {
	expectOutput(t, "", `
		println(1 + 2 * 3, (1 + 2) * 3, 7 / 2, 7 % 3, -4 + 1);
		println(1.5 * 2.0, 10.0 / 4.0, -2.5);
		println(1 < 2, 2 <= 2, 3 > 4, 4 >= 5, 1 == 1, 1 != 1);
		println(!true, !(1 > 2));
		println(100000 * 3, 0 - 1000000);
	`, "7 9 3 1 -3\n3 2.5 -2.5\ntrue true false false true false\nfalse true\n300000 -1000000\n")
}

func TestStrings(t *testing.T) {
	expectOutput(t, "", `
		let s = "foo" + "bar";
		println(s, s.len(), s[0], s[5]);
		println(s == "foobar", s != "foobar", "a" < "b", "b" <= "a");
		println("tab\there", "quote\"d", toString(42) + toString(true) + toString(null));
		for c in "héy" { print(c, ""); }
		println("");
	`, "foobar 6 f r\ntrue false true false\ntab\there quote\"d 42truenull\nh é y \n")
}

func TestVariablesAndScopes(t *testing.T) {
	expectOutput(t, "", `
		let x = 1;
		let y = x;
		y = 5;
		println(x, y);
		{
			let x = 10;
			x = x + 1;
			println(x);
		}
		println(x);
		const limit: Int = 3;
		println(limit);
	`, "1 5\n11\n1\n3\n")
}

func TestControlFlow(t *testing.T) {
	expectOutput(t, "", `
		let i = 0;
		let evens = 0;
		while true {
			i = i + 1;
			if i > 10 { break; }
			if i % 2 == 1 { continue; }
			evens = evens + i;
		}
		println(evens);

		let n = 5;
		if n < 3 { println("small"); } else if n < 10 { println("medium"); } else { println("large"); }

		let total = 0;
		for v in [1, 2, 3, 4, 5] {
			if v == 2 { continue; }
			if v == 5 { break; }
			total = total + v;
		}
		println(total);

		for row in [[1, 2], [3]] {
			for cell in row { print(cell, ""); }
		}
		println("");
	`, "30\nmedium\n8\n1 2 3 \n")
}

func TestShortCircuit(t *testing.T) {
	decls := `
		fn loud(label: String, value: Bool) -> Bool {
			print(label, "");
			return value;
		}
	`
	expectOutput(t, decls, `
		println(loud("a", false) && loud("b", true));
		println(loud("c", true) || loud("d", false));
		println(loud("e", true) && loud("f", false) || loud("g", true));
		let missing = null;
		println(missing ?? "fallback", "kept" ?? "unused");

		let i = 0;
		while i < 3 && loud("w", true) { i = i + 1; }
		println(i);

		// A variable initialized with && or || stays usable afterwards
		let both = loud("x", true) && loud("y", false);
		let either = both || loud("z", true);
		println(both, either);
		if both || (either && loud("n", true)) { println("nested"); }
	`, "a false\nc true\ne f g true\nfallback kept\nw w w 3\nx y z false true\nn nested\n")
}

func TestFunctionsAndRecursion(t *testing.T) {
	decls := `
		fn fact(n: Int) -> Int {
			if n <= 1 { return 1; }
			return n * fact(n - 1);
		}
		fn isEven(n: Int) -> Bool { if n == 0 { return true; } return isOdd(n - 1); }
		fn isOdd(n: Int) -> Bool { if n == 0 { return false; } return isEven(n - 1); }
		fn greet(name: String) -> Void { println("hi", name); }
		fn twice(f: fn(Int) -> Int, v: Int) -> Int { return f(f(v)); }
		fn addOne(v: Int) -> Int { return v + 1; }
	`
	expectOutput(t, decls, `
		println(fact(10), isEven(10), isOdd(7));
		greet("qoqi");
		println(twice(addOne, 5));
		let f = fact;
		println(f(5));
	`, "3628800 true true\nhi qoqi\n7\n120\n")
}

func TestArrays(t *testing.T) {
	expectOutput(t, "", `
		let xs = [10, 20, 30];
		xs[1] = 25;
		xs.push(40);
		println(xs, xs.len(), xs[3]);
		let empty: [String] = [];
		println(empty, empty.len());
		let alias = xs;
		alias[0] = 0;
		println(xs[0]);
		println([[1], [2, 3]], ["a", "b"]);
	`, "[10, 25, 30, 40] 4 40\n[] 0\n0\n[[1], [2, 3]] [\"a\", \"b\"]\n")
}

func TestStructsAndMethods(t *testing.T) {
	decls := `
		struct Point { x: Int; y: Int; }
		struct Line { from: Point; to: Point; }

		trait Shape { fn area() -> Int; }

		impl Shape for Point {
			fn area() -> Int { return self.x * self.y; }
		}
		impl Point {
			fn moved(dx: Int, dy: Int) -> Point { return Point { x: self.x + dx, y: self.y + dy }; }
			fn grow() -> Void { self.x = self.x + 1; }
		}

		fn describe(s: Shape) -> Int { return s.area(); }
	`
	expectOutput(t, decls, `
		let p = Point { x: 2, y: 3 };
		p.y = 5;
		println(p, p.x, p.area());
		p.grow();
		let q = p.moved(1, 1);
		println(p.x, q, describe(q), describe(p));
		let line = Line { from: p, to: q };
		line.to.x = 99;
		println(line.to.x, q.x);
	`, "{x: 2, y: 5} 2 10\n3 {x: 4, y: 6} 24 15\n99 99\n")
}

func TestMethodDispatch(t *testing.T) {
	decls := `
		trait Shape {
			fn area() -> Int;
			fn name() -> String;
		}

		struct Rect { w: Int; h: Int; }
		struct Square { side: Int; }
		struct Label { name: String; }          // a field called like a method

		impl Shape for Rect {
			fn area() -> Int { return self.w * self.h; }
			fn name() -> String { return "rect"; }
		}
		impl Shape for Square {
			fn area() -> Int { return self.side * self.side; }
			fn name() -> String { return "square"; }
		}
		impl Rect {
			fn grown(by: Int) -> Rect { return Rect { w: self.w + by, h: self.h + by }; }
			fn describe() -> String { return self.name() + " " + toString(self.area()); }
		}

		fn total(shapes: [Shape]) -> Int {
			let sum = 0;
			for shape in shapes { sum = sum + shape.area(); }
			return sum;
		}
	`
	expectOutput(t, decls, `
		let rect = Rect { w: 2, h: 3 };
		let square = Square { side: 4 };
		let label = Label { name: "tag" };

		// Known struct: the method is called directly, and chains keep their type
		println(rect.area(), rect.grown(1).grown(1).area(), rect.describe());
		// A field and a method may share a name
		println(label.name, square.name());

		// Through the trait, the method of each value's own struct runs
		let shapes: [Shape] = [rect, square, rect.grown(10)];
		println(total(shapes));
		for shape in shapes { print(shape.name(), ""); }
		println("");

		// A struct value prints its data, not the methods stored for dispatch
		println(square);
	`, "6 20 rect 6\ntag square\n178\nrect square rect \n{side: 4}\n")
}

func TestOptionals(t *testing.T) {
	decls := `
		struct User { name: String; manager: User?; }

		fn find(users: [User], name: String) -> User? {
			for user in users {
				if user.name == name { return user; }
			}
			return null;
		}

		fn bossOf(user: User) -> String {
			let nobody = User { name: "nobody", manager: null };
			return (user.manager ?? nobody).name;
		}
	`
	expectOutput(t, decls, `
		let ada = User { name: "ada", manager: null };
		let bob = User { name: "bob", manager: ada };
		let users = [ada, bob];

		println(bossOf(bob), bossOf(ada));
		println(find(users, "bob") == null, find(users, "zed") == null);
		println((find(users, "zed") ?? ada).name);

		let count: Int? = null;
		println(count ?? 0, (count ?? 0) + 1);
		count = 41;
		println((count ?? 0) + 1);
	`, "ada nobody\nfalse true\nada\n0 1\n42\n")
}

func TestNullChecksNarrowOptionals(t *testing.T) {
	decls := `
		struct Node { value: Int; next: Node?; }

		fn sum(list: Node?) -> Int {
			let total = 0;
			let node = list;
			while node != null {
				total = total + node.value;
				node = node.next;
			}
			return total;
		}

		fn firstOr(list: Node?, fallback: Int) -> Int {
			if list == null { return fallback; }
			return list.value;
		}

		fn label(name: String?) -> String {
			if name != null && name.len() > 0 { return "<" + name + ">"; }
			return "<none>";
		}

		fn total(values: [Int?]) -> Int {
			let sum = 0;
			for value in values {
				if value == null { continue; }
				sum = sum + value;
			}
			return sum;
		}
	`
	expectOutput(t, decls, `
		let list = Node { value: 1, next: Node { value: 2, next: Node { value: 3, next: null } } };
		println(sum(list), sum(null), firstOr(list, 9), firstOr(null, 9));
		println(label("ada"), label(""), label(null));
		println(total([1, null, 2, null, 3]));

		let count: Int? = null;
		println(count == null);
		count = 41;
		println(count + 1);
	`, "6 0 1 9\n<ada> <none> <none>\n6\ntrue\n42\n")
}

func TestGenericStructMethods(t *testing.T) {
	decls := `
		trait Sized { fn size() -> Int; }

		struct Stack<T> { items: [T]; }

		impl<T> Stack<T> {
			fn push(item: T) -> Void { self.items.push(item); }
			fn peek() -> T? {
				if self.items.len() == 0 { return null; }
				return self.items[self.items.len() - 1];
			}
			fn reversed() -> Stack<T> {
				let out = Stack<T> { items: [] };
				let i = self.items.len();
				while i > 0 {
					i = i - 1;
					out.push(self.items[i]);
				}
				return out;
			}
		}

		impl<T> Sized for Stack<T> {
			fn size() -> Int { return self.items.len(); }
		}

		fn sizeOf(s: Sized) -> Int { return s.size(); }
	`
	expectOutput(t, decls, `
		let numbers = Stack<Int> { items: [] };
		numbers.push(1);
		numbers.push(2);
		numbers.push(3);
		let top = numbers.peek();
		if top != null { println(top + 10, numbers.size()); }
		println(numbers.reversed().items, sizeOf(numbers));

		let words = Stack<String> { items: ["a"] };
		words.push("b");
		println(words.peek() ?? "empty", sizeOf(words));
		println(Stack<Bool> { items: [] }.peek() == null);
	`, "13 3\n[3, 2, 1] 3\nb 2\ntrue\n")
}

func TestGenericMethods(t *testing.T) {
	decls := `
		struct Stack<T> { items: [T]; }

		impl<T> Stack<T> {
			fn push(item: T) -> Void { self.items.push(item); }

			// A method may add type parameters of its own to the struct's
			fn map<U>(f: fn(T) -> U) -> Stack<U> {
				let out = Stack<U> { items: [] };
				for item in self.items { out.push(f(item)); }
				return out;
			}

			fn fold<A>(start: A, step: fn(A, T) -> A) -> A {
				let acc = start;
				for item in self.items { acc = step(acc, item); }
				return acc;
			}
		}

		trait Converter { fn convert<T>(value: T) -> [T]; }
		struct Wrap { times: Int; }
		impl Converter for Wrap {
			fn convert<T>(value: T) -> [T] {
				let out: [T] = [];
				let i = 0;
				while i < self.times { out.push(value); i = i + 1; }
				return out;
			}
		}

		fn through(c: Converter) -> [String] { return c.convert<String>("x"); }
	`
	expectOutput(t, decls, `
		let numbers = Stack<Int> { items: [1, 2, 3] };

		let labels = numbers.map<String>(fn(n: Int) -> String { return "#" + toString(n); });
		println(labels.items, labels.map<Int>(fn(s: String) -> Int { return s.len(); }).items);

		println(numbers.fold<Int>(0, fn(acc: Int, n: Int) -> Int { return acc + n; }));
		println(numbers.fold<String>(">", fn(acc: String, n: Int) -> String { return acc + toString(n); }));

		let wrap = Wrap { times: 2 };
		println(wrap.convert<Int>(7), wrap.convert<Bool>(true), through(wrap));
	`, "[\"#1\", \"#2\", \"#3\"] [2, 2, 2]\n6\n>123\n[7, 7] [true, true] [\"x\", \"x\"]\n")
}

func TestCastsVerifyElementsAndFields(t *testing.T) {
	decls := `
		struct Box<T> { value: T; }
		struct List<T> { head: T; tail: List<T>?; }
		trait Shape { fn area() -> Int; }
		struct Square { side: Int; }
		impl Shape for Square { fn area() -> Int { return self.side * self.side; } }

		fn total(v: Any) -> Int {
			let sum = 0;
			for n in v as [Int] { sum = sum + n; }
			return sum;
		}
		fn unbox(v: Any) -> Int { return (v as Box<Int>).value; }
		fn head(v: Any) -> Int { return (v as List<Int>).head; }
		fn areas(v: Any) -> Int {
			let sum = 0;
			for s in v as [Shape] { sum = sum + s.area(); }
			return sum;
		}
	`
	expectOutput(t, decls, `
		println(total([1, 2, 3]), total([]));
		println(unbox(Box<Int> { value: 9 }));
		println(head(List<Int> { head: 4, tail: List<Int> { head: 5, tail: null } }));
		println(areas([Square { side: 2 }, Square { side: 3 }]));

		let nested: Any = [[1], [2, 3]];
		println((nested as [[Int]])[1][1]);
		let mixed: [Any] = [1, null];
		println((mixed as [Int?])[1] == null);
	`, "6 0\n9\n4\n13\n3\ntrue\n")
}

func TestWarningsDoNotStopCompilation(t *testing.T) {
	src := "fn main() -> Int {\n  println(\"runs\");\n  return 3;\n  println(\"never\");\n}\n"

	errs := errors.NewHandler()
	result := Compile(src, "test.qi", errs)

	if errs.HasErrors() || result.Chunk == nil {
		t.Fatalf("expected the program to compile, got %v", errs.Diagnostics)
	}
	if len(errs.Diagnostics) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(errs.Diagnostics), errs.Diagnostics)
	}
	warning := errs.Diagnostics[0]
	if warning.Severity != errors.SeverityWarning || warning.Code != "E2023" || warning.Pos.Line != 4 {
		t.Errorf("expected an unreachable-code warning on line 4, got %s %s on line %d", warning.Severity, warning.Code, warning.Pos.Line)
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	value, err := machine.Exec(result.Chunk)
	if err != nil || out.String() != "runs\n" || !value.IsInt() || value.AsInt() != 3 {
		t.Errorf("expected the reachable part to run and return 3, got %q, %s, %v", out.String(), vm.FormatValue(value), err)
	}
}

func TestTypeTestsAndMatchOnTypes(t *testing.T) {
	decls := `
		trait Shape { fn area() -> Int; }
		struct Square { side: Int; }
		struct Circle { r: Int; }
		struct Box<T> { value: T; }
		impl Shape for Square { fn area() -> Int { return self.side * self.side; } }
		impl Shape for Circle { fn area() -> Int { return 3 * self.r * self.r; } }
		impl Square { fn diagonalSquared() -> Int { return 2 * self.side * self.side; } }

		fn describe(v: Any) -> String {
			match v {
				case Int: { return "int " + toString(v + 1); }
				case String: { return "string of " + toString(v.len()); }
				case [Int]: { return toString(v.len()) + " ints"; }
				case Box<String>: { return "box of " + v.value; }
				case Square: { return "square " + toString(v.diagonalSquared()); }
				case Shape: { return "shape " + toString(v.area()); }
				case fn(Int) -> Int: { return "function giving " + toString(v(1)); }
				default: { return "something else"; }
			}
		}

		fn sideOrZero(s: Shape) -> Int {
			if s is Square { return s.side; }
			return 0;
		}
	`
	expectOutput(t, decls, `
		println(describe(1));
		println(describe("abc"));
		println(describe([1, 2]));
		println(describe(Box<String> { value: "text" }));
		println(describe(Box<Int> { value: 1 }));
		println(describe(Square { side: 2 }));
		println(describe(Circle { r: 1 }));
		println(describe(fn(n: Int) -> Int { return n * 10; }));
		println(describe(null), describe(true), describe(["a"]));

		println(sideOrZero(Square { side: 4 }), sideOrZero(Circle { r: 4 }));

		let v: Any = "text";
		println(v is String, v is Int, v is String?, v is [String], !(v is Int));
		if v is String && v.len() == 4 { println("four letters"); }
	`, "int 2\nstring of 3\n2 ints\nbox of text\nsomething else\nsquare 8\nshape 3\nfunction giving 10\n"+
		"something else something else something else\n4 0\ntrue false true false true\nfour letters\n")
}

func TestFieldsAreNarrowedByNullTests(t *testing.T) {
	decls := `
		struct Node { value: Int; next: Node?; }
		struct Config { name: String?; retries: Int?; }

		fn second(n: Node) -> Int {
			if n.next == null { return -1; }
			return n.next.value;
		}

		fn third(n: Node) -> Int {
			if n.next != null && n.next.next != null { return n.next.next.value; }
			return -1;
		}

		fn last(n: Node) -> Int {
			let cur = n;
			while cur.next != null { cur = cur.next; }
			return cur.value;
		}

		fn summary(c: Config) -> String {
			let out = "";
			if c.name != null { out = out + c.name; } else { out = out + "?"; }
			if c.retries != null && c.retries > 1 { out = out + " x" + toString(c.retries); }
			return out;
		}

		fn double(n: Int) -> Int { return n * 2; }
		fn reset(c: Config) -> Void { c.retries = null; }

		// Calls that cannot assign a field leave what is known about fields
		// alone; one that can does not.
		fn report(c: Config) -> Int {
			if c.retries == null { return 0; }
			println("retries:", c.retries);
			let twice = double(c.retries);
			let before = c.retries;
			reset(c);
			return twice + before + (c.retries ?? 100);
		}
	`
	expectOutput(t, decls, `
		let list = Node { value: 1, next: Node { value: 2, next: Node { value: 3, next: null } } };
		println(second(list), third(list), last(list));
		let single = Node { value: 9, next: null };
		println(second(single), third(single), last(single));
		println(summary(Config { name: "job", retries: 3 }), summary(Config { name: null, retries: 1 }));
		println(report(Config { name: "job", retries: 4 }), report(Config { name: "job", retries: null }));
	`, "2 3 3\n-1 -1 9\njob x3 ?\nretries: 4\n112 0\n")
}

func TestTypeArgumentsAreInferred(t *testing.T) {
	decls := `
		struct Stack<T> { items: [T]; }
		impl<T> Stack<T> {
			fn map<U>(f: fn(T) -> U) -> Stack<U> {
				let out = Stack<U> { items: [] };
				for item in self.items { out.items.push(f(item)); }
				return out;
			}
		}

		fn first<T>(xs: [T]) -> T { return xs[0]; }
		fn map<T, U>(xs: [T], f: fn(T) -> U) -> [U] {
			let out: [U] = [];
			for x in xs { out.push(f(x)); }
			return out;
		}
		fn collect<T>(items: ...T) -> [T] { return items; }
		fn orElse<T>(value: T?, fallback: T) -> T { return value ?? fallback; }
		fn top<T>(s: Stack<T>) -> T { return first(s.items); }
		fn none<T>() -> [T] { return []; }
		fn count(flags: [Bool]) -> Int { return flags.len(); }
		fn emptyInts() -> [Int] { return none(); }
	`
	expectOutput(t, decls, `
		println(first([7, 8]) + 1, first(["a", "b"]).len(), first([[1, 2]]).len());
		println(map([1, 2, 3], toString), map(["a", "bb"], fn(s: String) -> Int { return s.len(); }));
		println(collect(1, 2, 3), collect("x"));

		let missing: Int? = null;
		println(orElse(missing, 5), orElse("set", "unset"));

		let stack = Stack<Int> { items: [1, 2] };
		println(stack.map(fn(n: Int) -> String { return "#" + toString(n); }).items, top(stack));

		// No argument says what T is: it comes from the type expected
		let names: [String] = none();
		names.push("ada");
		println(names, count(none()), emptyInts());

		// Written out where nothing at all determines it, and allowed anywhere
		println((none<String>()).len(), first<Int>([4]));
	`, "8 1 2\n[\"1\", \"2\", \"3\"] [1, 2]\n[1, 2, 3] [\"x\"]\n5 set\n[\"#1\", \"#2\"] 1\n[\"ada\"] 0 []\n0 4\n")
}

func TestCastsToFunctionTypes(t *testing.T) {
	decls := `
		fn inc(n: Int) -> Int { return n + 1; }
		fn shout(s: String) -> String { return s + "!"; }
		fn show(v: Any) -> String { return "<" + toString(v) + ">"; }
	`
	expectOutput(t, decls, `
		let fns: [Any] = [inc, shout, show, toString, fn(a: Int, b: Int) -> Int { return a + b; }];

		println((fns[0] as fn(Int) -> Int)(1), (fns[1] as fn(String) -> String)("hey"));
		// A function accepting more, or returning something more specific, fits
		println((fns[2] as fn(Int) -> String)(5), (fns[3] as fn(Bool) -> String)(true), (fns[0] as fn(Int) -> Any));
		println((fns[4] as fn(Int, Int) -> Int)(2, 3));

		println(fns[1] is fn(Int) -> Int, fns[1] is fn(String) -> String, fns[4] is fn(Int) -> Int);

		let total = 0;
		for f in fns {
			if f is fn(Int) -> Int { total = total + f(10); }
		}
		println(total);
	`, "2 hey!\n<5> true <fn inc>\n5\nfalse true false\n11\n")
}

func TestTraitMethodTypeParametersMatchByPosition(t *testing.T) {
	decls := `
		trait Converter { fn convert<T>(value: T) -> [T]; }
		struct Twice { n: Int; }
		impl Converter for Twice {
			// The impl calls its parameter U; the trait calls it T
			fn convert<U>(value: U) -> [U] { return [value, value]; }
		}
		fn through(c: Converter) -> [String] { return c.convert("x"); }
	`
	expectOutput(t, decls, `
		let twice = Twice { n: 0 };
		println(twice.convert(7), through(twice));
	`, "[7, 7] [\"x\", \"x\"]\n")
}

func TestCompoundAssignmentAndIncrement(t *testing.T) {
	decls := `
		struct Counter { hits: Int; names: [String]; }

		impl Counter {
			// Counts how often it was asked for the next slot
			fn next() -> Int { self.hits++; return self.hits - 1; }
			fn rename(i: Int, suffix: String) -> Void { self.names[i] += suffix; }
		}

		fn total(values: [Int]) -> Int {
			let sum = 0;
			for v in values { sum += v; }
			return sum;
		}
	`
	expectOutput(t, decls, `
		let i = 0;
		i += 5; i -= 2; i *= 4; i /= 3; i %= 3; i++; i--; i++;
		let text = "a"; text += "b"; text += toString(i);
		let ratio = 1.5; ratio *= 2.0; ratio -= 0.5; ratio /= 0.5;
		println(i, text, ratio);

		// The target is evaluated once: next() runs once per statement
		let slots = [10, 20, 30];
		let counter = Counter { hits: 0, names: ["x", "y"] };
		slots[counter.next()] += 1;
		slots[counter.next()] *= 2;
		slots[counter.next()]--;
		println(slots, counter.hits, total(slots));

		counter.rename(1, "!");
		counter.names[0] += counter.names[1];
		println(counter.names);

		// Through a closure, and on a variable that started as null
		let sum = 0;
		let add = fn(v: Int) -> Void { sum += v; };
		add(4); add(5);
		let maybe: Int? = null;
		maybe = 10;
		maybe += sum;
		println(sum, maybe);

		// Nested: a field of an element of a field
		let grid = [[1, 2], [3, 4]];
		grid[1][0] += grid[0][1];
		grid[0][0]++;
		println(grid);
	`, "2 ab2 5\n[11, 40, 29] 3 80\n[\"xy!\", \"y!\"]\n9 19\n[[2, 2], [5, 4]]\n")
}

func TestForLoopIndex(t *testing.T) {
	decls := `
		struct Entry { rank: Int; name: String; }

		fn ranked(names: [String]) -> [Entry] {
			let out: [Entry] = [];
			for i, name in names { out.push(Entry { rank: i + 1, name: name }); }
			return out;
		}

		fn indexOf<T>(items: [T], wanted: T) -> Int {
			for i, item in items {
				if item == wanted { return i; }
			}
			return -1;
		}
	`
	expectOutput(t, decls, `
		for i, name in ["ada", "bob"] { print(i, name, ""); }
		println("");

		// Strings, with continue and break not disturbing the count
		for i, ch in "héllo" {
			if i == 1 { continue; }
			if i == 4 { break; }
			print(i, ch, "");
		}
		println("");

		for entry in ranked(["x", "y", "z"]) { print(entry.rank, entry.name, ""); }
		println("");
		println(indexOf(["a", "b", "c"], "c"), indexOf([1, 2], 9));

		// Nested loops each have their own index
		let pairs: [String] = [];
		let letters = ["a", "b", "c"];
		for i, first in letters {
			for j, second in letters {
				if i < j { pairs.push(first + second); }
			}
		}
		println(pairs);

		// Changing the collection goes through the index
		let scores = [10, 20, 30];
		for i, score in scores {
			scores[i] = score * 2;
			scores[i] += i;
		}
		println(scores);

		// Each closure created in the loop keeps its own iteration's variables
		let readers: [fn() -> Int] = [];
		for i, v in [7, 8, 9] { readers.push(fn() -> Int { return i * 10 + v; }); }
		println(readers[0](), readers[2]());

		// An empty collection runs no iteration
		let nothing: [Int] = [];
		for i, v in nothing { println("never", i, v); }
		println("done");
	`, "0 ada 1 bob \n0 h 2 l 3 l \n1 x 2 y 3 z \n2 -1\n[\"ab\", \"ac\", \"bc\"]\n[20, 41, 62]\n7 29\ndone\n")
}

func TestCasts(t *testing.T) {
	decls := `
		trait Shape { fn area() -> Int; }
		struct Square { side: Int; }
		struct Circle { r: Int; }
		impl Shape for Square { fn area() -> Int { return self.side * self.side; } }
		impl Shape for Circle { fn area() -> Int { return 3 * self.r * self.r; } }

		fn twice(v: Any) -> Int { return (v as Int) * 2; }
		fn side(shape: Shape) -> Int { return (shape as Square).side; }
		fn back(v: Any) -> Shape { return v as Shape; }
	`
	expectOutput(t, decls, `
		let boxed: Any = 21;
		println(twice(boxed), twice(4));

		let text: Any = "hello";
		println((text as String).len(), (text as String?) ?? "none");

		let shape: Shape = Square { side: 5 };
		println(side(shape), back(shape).area(), back(Circle { r: 1 }).area());

		let maybe: Int? = 7;
		println((maybe as Int) + 1);

		let list: Any = [1, 2, 3];
		println((list as [Int]).len());
	`, "42 8\n5 hello\n5 25 3\n8\n3\n")
}

func TestClosures(t *testing.T) {
	decls := `
		fn makeCounter() -> fn() -> Int {
			let count = 0;
			return fn() -> Int {
				count = count + 1;
				return count;
			};
		}
		fn makeAdder(n: Int) -> fn(Int) -> Int {
			return fn(x: Int) -> Int { return x + n; };
		}
		fn nested(a: Int) -> fn(Int) -> fn(Int) -> Int {
			let b = 10;
			return fn(c: Int) -> fn(Int) -> Int {
				return fn(d: Int) -> Int { return a + b + c + d; };
			};
		}
	`
	expectOutput(t, decls, `
		let first = makeCounter();
		let second = makeCounter();
		first();
		first();
		println(first(), second());
		println(makeAdder(5)(10), nested(1)(2)(3));

		// A closure sees later writes to the variable it captured
		let total = 0;
		let add = fn(v: Int) -> Void { total = total + v; };
		add(4);
		total = total + 1;
		add(5);
		println(total);
	`, "3 1\n15 16\n10\n")
}

func TestClosuresInLoopsKeepTheirIteration(t *testing.T) {
	decls := `
		struct Button { label: String; onClick: fn() -> String; }

		fn makeButtons(labels: [String]) -> [Button] {
			let buttons: [Button] = [];
			for i, label in labels {
				let id = toString(i) + ":" + label;
				buttons.push(Button { label: label, onClick: fn() -> String { return "clicked " + id; } });
			}
			return buttons;
		}

		// Each call returns counters that share one total and own one count
		fn makeCounters(n: Int) -> [fn() -> String] {
			let counters: [fn() -> String] = [];
			let total = 0;
			let i = 0;
			while i < n {
				let own = 0;
				let name = i;
				i++;
				counters.push(fn() -> String {
					own++;
					total++;
					return toString(name) + "=" + toString(own) + "/" + toString(total);
				});
			}
			return counters;
		}
	`
	expectOutput(t, decls, `
		// for over an array, with and without index
		let readers: [fn() -> Int] = [];
		for v in [1, 2, 3] { readers.push(fn() -> Int { return v; }); }
		println(readers[0](), readers[1](), readers[2]());

		let indexed: [fn() -> Int] = [];
		for i, v in [10, 20, 30] { indexed.push(fn() -> Int { return i * 100 + v; }); }
		println(indexed[0](), indexed[1](), indexed[2]());

		// while, with continue skipping some iterations
		let squares: [fn() -> Int] = [];
		let n = 0;
		while n < 5 {
			let square = n * n;
			n++;
			if square == 4 { continue; }
			squares.push(fn() -> Int { return square; });
		}
		println(squares[0](), squares[1](), squares[2](), squares[3](), squares.len());

		// break leaves the closures made so far intact
		let early: [fn() -> String] = [];
		for word in ["a", "b", "c"] {
			early.push(fn() -> String { return word + "!"; });
			if word == "b" { break; }
		}
		println(early[0](), early[1](), early.len());

		// A variable changed after the closure was made, in the same iteration, is shared
		let late: [fn() -> Int] = [];
		for v in [1, 2] {
			let doubled = v;
			late.push(fn() -> Int { return doubled; });
			doubled *= 2;
		}
		println(late[0](), late[1]());

		// Each closure owns its iteration's variable, but they share an outer one
		let counters = makeCounters(3);
		println(counters[0](), counters[0](), counters[2](), counters[1](), counters[0]());

		// Closures stored in structs, through a function
		for button in makeButtons(["ok", "cancel"]) { println(button.label, "->", button.onClick()); }

		// Nested loops, and a closure returned from a closure
		let cells: [fn() -> String] = [];
		for r, row in ["a", "b"] {
			for c, col in ["x", "y"] {
				let make = fn() -> fn() -> String {
					return fn() -> String { return row + col + toString(r * 2 + c); };
				};
				cells.push(make());
			}
		}
		for cell in cells { print(cell(), ""); }
		println("");

		// Strings
		let letters: [fn() -> String] = [];
		for ch in "hey" { letters.push(fn() -> String { return ch + ch; }); }
		println(letters[0](), letters[1](), letters[2]());
	`, "1 2 3\n10 120 230\n0 1 9 16 4\na! b! 2\n2 4\n0=1/1 0=2/2 2=1/3 1=1/4 0=3/5\n"+
		"ok -> clicked 0:ok\ncancel -> clicked 1:cancel\nax0 ay1 bx2 by3 \nhh ee yy\n")
}

func TestDeepRecursion(t *testing.T) {
	decls := `
		struct Node { value: Int; next: Node?; }

		fn build(n: Int) -> Node? {
			let list: Node? = null;
			let i = 0;
			while i < n { list = Node { value: i, next: list }; i++; }
			return list;
		}
		fn sum(node: Node?) -> Int {
			if node == null { return 0; }
			return node.value + sum(node.next);
		}
		fn depth(node: Node?) -> Int {
			if node == null { return 0; }
			return 1 + depth(node.next);
		}
		fn isEven(n: Int) -> Bool { if n == 0 { return true; } return isOdd(n - 1); }
		fn isOdd(n: Int) -> Bool { if n == 0 { return false; } return isEven(n - 1); }
	`
	// 5000 calls deep: well past the old limit of 256
	expectOutput(t, decls, `
		let list = build(5000);
		println(sum(list), depth(list), isEven(4000), isOdd(4000));
	`, "12497500 5000 true false\n")

	// Recursion that never ends is still stopped, with the place it happened
	_, _, err := runProgram(t, "fn forever(n: Int) -> Int { return forever(n + 1); }\nfn main() -> Int { return forever(0); }")
	if err == nil || !strings.Contains(err.Error(), "stack overflow: max call depth 10000 exceeded") || !strings.Contains(err.Error(), "in function 'forever'") {
		t.Errorf("expected a stack overflow in 'forever', got %v", err)
	}
}

func TestLargeArraysStayFast(t *testing.T) {
	src := `
		import std.arrays;
		struct Point { x: Int; y: Int; }

		fn main() -> Int {
			// Growing arrays of numbers, strings and structs
			let numbers: [Int] = [];
			let labels: [String] = [];
			let points: [Point] = [];
			let i = 0;
			while i < 200000 {
				numbers.push(i);
				if i % 100 == 0 {
					labels.push("item " + toString(i));
					points.push(Point { x: i, y: 0 - i });
				}
				i++;
			}

			let sum = 0;
			for n in numbers { sum += n % 7; }
			let last = points[points.len() - 1];
			println(numbers.len(), labels.len(), labels[1999], last.x + last.y, sum);
			return 0;
		}
	`
	errs := errors.NewHandler()
	result := Compile(src, "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected errors: %v", errs.Diagnostics)
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if want := "200000 2000 item 199900 0 599994\n"; out.String() != want {
		t.Errorf("expected %q, got %q", want, out.String())
	}

	// The measure of "fast" that does not depend on the machine: how often
	// the collector ran. It used to run thousands of times here, each run
	// walking the whole array; now its runs spread out as the data grows.
	if got := machine.GetHeap().Collections(); got > 40 {
		t.Errorf("expected a few dozen collections at most, got %d", got)
	}
}

func TestOutOfMemoryIsReported(t *testing.T) {
	src := "fn main() -> Int {\n  let xs: [Int] = [];\n  while true { xs.push(1); }\n}\n"
	errs := errors.NewHandler()
	result := Compile(src, "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected errors: %v", errs.Diagnostics)
	}

	machine := vm.NewVMWithHeapSize(256 * 1024)
	_, err := machine.Exec(result.Chunk)
	if err == nil {
		t.Fatal("expected the program to run out of memory")
	}
	for _, want := range []string{"out of memory", "limit 262144", "test.qi:3:", "in function 'main'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
	// The message is about memory, not about the function that happened to ask
	if strings.Contains(err.Error(), "extern function") {
		t.Errorf("expected no mention of the host function, got:\n%v", err)
	}
}

func TestGenericsAndVariadics(t *testing.T) {
	decls := `
		fn identity<T>(v: T) -> T { return v; }
		fn first<T>(xs: [T]) -> T { return xs[0]; }
		fn collect<T>(items: ...T) -> [T] { return items; }
		struct Box<T> { value: T; }
		fn unwrap<T>(box: Box<T>) -> T { return box.value; }
		fn sum(label: String, nums: ...Int) -> Int {
			let total = 0;
			for n in nums { total = total + n; }
			println(label, total, nums.len());
			return total;
		}
	`
	expectOutput(t, decls, `
		println(identity<Int>(42), identity<String>("id"), first<Int>([7, 8]));
		println(collect<Int>(1, 2, 3), collect<String>());
		sum("three:", 1, 2, 3);
		sum("none:");
		let box = Box<String> { value: "boxed" };
		println(box, unwrap<String>(box));
	`, "42 id 7\n[1, 2, 3] []\nthree: 6 3\nnone: 0 0\n{value: \"boxed\"} boxed\n")
}

func TestMatch(t *testing.T) {
	decls := `
		fn name(n: Int) -> String {
			match n {
				case 0: { return "zero"; }
				case -1: { return "minus one"; }
				default: { return "other"; }
			}
		}
	`
	expectOutput(t, decls, `
		println(name(0), name(-1), name(7));
		match "b" {
			case "a": { println("is a"); }
			case "b": { println("is b"); }
		}
	`, "zero minus one other\nis b\n")
}

// A case may name a top-level constant, and compares the value with it
// as if its literal were written there.
func TestMatchOnConstants(t *testing.T) {
	decls := `
		const HAND_OVER = "hand_over";
		const STOP: String = "stop";
		const ANSWER = 42;
		const MINUS = -1;
		const SAME = ANSWER;
		const SUM = 1 + 2;
		const HALF: Float = 1;
		const YES = true;
		const NO = false;

		fn word(s: String) -> String {
			match s {
				case HAND_OVER: { return "hand over"; }
				case STOP | "halt": { return "stop"; }
				default: { return "other"; }
			}
		}

		fn number(n: Int) -> String {
			return match n {
				case ANSWER if n > 100: "never";
				case SAME: "answer";
				case MINUS: "minus one";
				case SUM: "three";
				case 0 | MINUS: "zero";
				default: "other";
			};
		}

		fn half(x: Float) -> String {
			match x {
				case HALF: { return "one"; }
				default: { return "other"; }
			}
		}

		fn yes(b: Bool) -> String {
			// Complete: a match that gives a value needs no default
			return match b {
				case YES: "yes";
				case NO: "no";
			};
		}
	`
	expectOutput(t, decls, `
		println(word("hand_over"), word("stop"), word("halt"), word("x"));
		println(number(42), number(-1), number(3), number(0));
		println(half(1.0), half(0.5));
		println(yes(true), yes(false));
		let limit = 2;
		match 2 {
			case Int if limit == 2: { println("guard"); }
		}
	`, "hand over stop stop other\nanswer minus one three zero\none other\nyes no\nguard\n")
}

func TestGarbageCollectionKeepsLiveValues(t *testing.T) {
	// Allocates far more than the heap holds, so the collector must run and
	// must not free anything still reachable.
	decls := `
		struct Node { value: Int; next: Node?; }
	`
	expectOutput(t, decls, `
		let head = Node { value: 0, next: null };
		let i = 1;
		while i < 2000 {
			head = Node { value: i, next: head };
			let scratch = "temporary " + toString(i);
			i = i + 1;
		}
		let total = 0;
		let node = head;
		let steps = 0;
		while steps < 2000 {
			total = total + node.value;
			node = node.next ?? node;
			steps = steps + 1;
		}
		println(total);
	`, "1999000\n")
}

func TestRuntimeErrorsPointAtSource(t *testing.T) {
	tests := []struct {
		name string
		body string
		msg  string
		line int
	}{
		{"index out of bounds", "let xs = [1];\nreturn xs[3];", "index out of bounds: 3 (length 1)", 4},
		{"division by zero", "let zero = 0;\nreturn 1 / zero;", "division by zero", 4},
		{"empty array", "let xs: [Int] = [];\nreturn xs[0];", "index out of bounds: 0 (length 0)", 4},
		{"write past the end", "let xs = [1, 2];\nxs[2] = 3;\nreturn 0;", "index out of bounds: 2 (length 2)", 4},
		{"write past the end after growing", "let xs = [1, 2, 3];\nxs.push(4);\nxs[5] = 9;\nreturn 0;", "index out of bounds: 5 (length 4)", 5},
		{"write at a negative index", "let xs = [1];\nlet i = 0 - 1;\nxs[i] = 2;\nreturn 0;", "index out of bounds: -1 (length 1)", 5},
		{"compound write past the end", "let xs = [1];\nxs[1] += 1;\nreturn 0;", "index out of bounds: 1 (length 1)", 4},
		{"sum overflows", "let big = 140737488355327;\nreturn big + 1;", "integer overflow", 4},
		{"product overflows", "let big = 16777216;\nreturn big * big;", "integer overflow", 4},
		{"increment overflows", "let big = 140737488355327;\nbig++;\nreturn 0;", "integer overflow", 4},
		{"negation overflows", "let small = -140737488355328;\nreturn -small;", "integer overflow", 4},
		{"compound division by zero", "let n = 4;\nlet zero = 0;\nn /= zero;\nreturn n;", "division by zero", 5},
		{"division by a zero written there", "let n = 4;\nreturn n / 0;", "division by zero", 4},
		{"remainder by a zero written there", "let n = 4;\nreturn n % 0;", "division by zero", 4},
		{"sum with a large number overflows", "let big = 140737488355327;\nreturn big + 70000;", "integer overflow", 4},
		{"product with a large number overflows", "let big = 16777216;\nreturn 70000000 * big;", "integer overflow", 4},
		{"difference with a large number overflows", "let small = -140737488355328;\nreturn small - 70000;", "integer overflow", 4},
		{"cast of the wrong kind", "let v: Any = \"text\";\nreturn v as Int;", "cannot cast String to Int", 4},
		{"cast of null", "let n: Int? = null;\nreturn n as Int;", "cannot cast null to Int", 4},
		{"cast to the wrong struct", "let v: Any = Empty {};\nlet o = v as Other;\nreturn 0;", "cannot cast Empty to Other", 4},
		{"cast to another function type", "let v: Any = fn(s: String) -> String { return s; };\nlet f = v as fn(Int) -> Int;\nreturn 0;", "cannot cast function to fn(Int) -> Int: it is a fn(String) -> String", 4},
		{"cast of an array of functions", "let v: [Any] = [toString, 3];\nlet fs = v as [fn(Any) -> String];\nreturn 0;", "element 1: Int is not a fn(Any) -> String", 4},
		{"cast of an array with a wrong element", "let v: [Any] = [1, \"two\", 3];\nlet xs = v as [Int];\nreturn 0;", "cannot cast array to [Int]: element 1: String is not an Int", 4},
		{"cast of a nested array", "let v: [[Any]] = [[1], [true]];\nlet xs = v as [[Int]];\nreturn 0;", "element 1: element 0: Bool is not an Int", 4},
		{"cast of a generic struct with another argument", "let v: Any = Holder<String> { item: \"s\" };\nlet h = v as Holder<Int>;\nreturn 0;", "cannot cast Holder to Holder: field 'item': String is not an Int", 4},
		{"cast of an array of the wrong structs", "let v: [Any] = [Empty {}, Other {}];\nlet xs = v as [Empty];\nreturn 0;", "element 1: Other is not an Empty", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "struct Empty {} struct Other {} struct Holder<T> { item: T; }\nfn main() -> Int {\n" + tt.body + "\n}\n"
			_, _, err := runProgram(t, src)
			if err == nil {
				t.Fatal("expected a runtime error")
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("expected error containing %q, got:\n%v", tt.msg, err)
			}
			if want := "test.qi:" + string(rune('0'+tt.line)) + ":"; !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to point at %s, got:\n%v", want, err)
			}
			if !strings.Contains(err.Error(), "in function 'main'") {
				t.Errorf("expected error to name the function, got:\n%v", err)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{"syntax error", "fn main() -> Int { return 1 }", "E0003", "expected ';'"},
		{"undefined variable", "fn main() -> Int { return missing; }", "E1001", "undefined identifier 'missing'"},
		{"redeclaration", "fn main() -> Int { let a = 1; let a = 2; return a; }", "E1002", "already declared"},
		{"type mismatch", `fn main() -> Int { let a: Int = "text"; return a; }`, "E2001", "cannot assign type String to Int"},
		{"bad operands", `fn main() -> Int { return 1 + "a"; }`, "E2006", "operator + not defined"},
		{"branches of an if of two types", `fn main() -> Int { let v = if true { 1 } else { "one" }; return 0; }`, "E2001", "this branch gives String, the one before gives Int"},
		{"an if whose condition is a number", `fn main() -> Int { return if 3 { 1 } else { 2 }; }`, "E2005", "condition must be Bool, got Int"},
		{"an if that may give null where it cannot", `fn main() -> Int { let n: Int = if true { 1 } else { null }; return n; }`, "E2001", "cannot assign type Int? to Int"},
		{"an if that gives the wrong type", `fn main() -> Int { return if true { "a" } else { "b" }; }`, "E2003", "cannot return type String from function returning Int"},
		{"a name of one branch used in the other", `enum S { A(Int), B }
			fn main() -> Int { let s = S.A(1); return if s is S.A(n) { n } else { n }; }`, "E1001", "undefined identifier 'n'"},
		{"a name of a branch used after it", `fn main() -> Int { let v = if true { let inner = 1; inner } else { 2 }; return inner; }`, "E1001", "undefined identifier 'inner'"},
		{"a constant made by a function", "fn make() -> Int { return 1; }\nconst N = make();\nfn main() -> Int { return N; }", "E2032", "the value of a constant has to be known when the program is compiled"},
		{"a struct constant with a field that is not constant", "struct P { x: Int; }\nfn make() -> Int { return 1; }\nconst O = P { x: make() };\nfn main() -> Int { return 0; }", "E2032", "has to be known when the program is compiled"},
		{"an array constant with an item that is not constant", "fn make() -> Int { return 1; }\nconst XS = [1, make()];\nfn main() -> Int { return 0; }", "E2032", "has to be known when the program is compiled"},
		{"a struct constant with a field of the wrong type", "struct P { x: Int; }\nconst O = P { x: \"one\" };\nfn main() -> Int { return 0; }", "E2001", "cannot use type String for field 'x' of type Int"},
		{"a struct constant missing a field", "struct P { x: Int; y: Int; }\nconst O = P { x: 1 };\nfn main() -> Int { return 0; }", "E2018", "struct literal of P is missing 'y'"},
		{"assigning to a field of a constant", "struct P { x: Int; }\nconst O = P { x: 1 };\nfn main() -> Int { O.x = 2; return 0; }", "E2022", "cannot change what the constant 'O' holds"},
		{"adding to a field of a constant", "struct P { x: Int; }\nconst O = P { x: 1 };\nfn main() -> Int { O.x += 2; return 0; }", "E2022", "cannot change what the constant 'O' holds"},
		{"assigning to an item of a constant", "const XS = [1, 2];\nfn main() -> Int { XS[0] = 2; return 0; }", "E2022", "cannot change what the constant 'XS' holds"},
		{"assigning deep in a constant", "struct P { xs: [Int]; }\nconst O = P { xs: [1] };\nfn main() -> Int { O.xs[0] = 2; return 0; }", "E2022", "cannot change what the constant 'O' holds"},
		{"assigning to a constant that is a struct", "struct P { x: Int; }\nconst O = P { x: 1 };\nfn main() -> Int { O = P { x: 2 }; return 0; }", "E2022", "cannot assign to constant 'O'"},
		{"a constant read by an index", "const XS = [1, 2];\nconst FIRST = XS[0];\nfn main() -> Int { return 0; }", "E2032", "has to be known when the program is compiled"},
		{"a constant that holds itself", "struct P { next: [P]; }\nconst O = P { next: [O] };\nfn main() -> Int { return 0; }", "E2031", "defined in terms of itself"},
		{"bits of a float", `fn main() -> Int { return 1.5 & 1; }`, "E2006", "operator & not defined for types Float and Int"},
		{"bits of a text", `fn main() -> Int { return "a" | "b"; }`, "E2006", "operator | not defined for types String and String"},
		{"bits of a boolean", `fn main() -> Bool { return true ^ false; }`, "E2006", "operator ^ not defined for types Bool and Bool"},
		{"shift by a float", `fn main() -> Int { return 1 << 2.0; }`, "E2006", "operator << not defined for types Int and Float"},
		{"shift of a text", `fn main() -> Int { return "a" >> 1; }`, "E2006", "operator >> not defined for types String and Int"},
		{"every bit of a float turned", `fn main() -> Int { return ~1.5; }`, "E2007", "operator ~ not defined for type Float"},
		{"every bit of a boolean turned", `fn main() -> Bool { return ~true; }`, "E2007", "operator ~ not defined for type Bool"},
		{"bits assigned to a text", `fn main() -> Int { let s = "a"; s |= 1; return 0; }`, "E2006", "operator | not defined for types String and Int"},
		{"a hexadecimal number too large", `fn main() -> Int { return 0x800000000000; }`, "E2025", "integer literal 140737488355328 is out of range"},
		{"wrong return type", `fn main() -> Int { return "a"; }`, "E2003", "cannot return type String"},
		{"wrong argument count", "fn f(a: Int) -> Int { return a; }\nfn main() -> Int { return f(1, 2); }", "E2009", "expected 1 arguments, got 2"},
		{"non-bool condition", "fn main() -> Int { if 1 { return 1; } return 0; }", "E2005", "condition must be Bool"},
		{"bad assignment", "fn main() -> Int { let a = 1; a = true; return a; }", "E2001", "cannot assign type Bool to Int"},
		{"mixed array", `fn main() -> Int { let a = [1, "two"]; return 0; }`, "E2001", "array element has type String, expected Int"},
		{"unknown struct field", "struct P { x: Int; }\nfn main() -> Int { let p = P { x: 1, z: 1 }; return 0; }", "E2014", "no field 'z'"},
		{"missing struct field", "struct P { x: Int; y: Int; }\nfn main() -> Int { let p = P { x: 1 }; return 0; }", "E2018", "missing 'y'"},
		{"unknown field read", "struct P { x: Int; }\nfn main() -> Int { let p = P { x: 1 }; return p.nope; }", "E2014", "P has no field 'nope'"},
		{"field of a number", "fn main() -> Int { let n = 5; return n.field; }", "E2014", "Int has no field 'field'"},
		{"unknown method", "struct P { x: Int; }\nfn main() -> Int { let p = P { x: 1 }; return p.missing(1); }", "E2015", "P has no method 'missing'"},
		{"method argument", "struct P { x: Int; }\nimpl P { fn add(n: Int) -> Int { return self.x + n; } }\nfn main() -> Int { let p = P { x: 1 }; return p.add(\"one\"); }", "E2010", "cannot use type String as argument of type Int"},
		{"method result", "struct P { x: Int; }\nimpl P { fn get() -> Int { return self.x; } }\nfn main() -> Int { let p = P { x: 1 }; let s: String = p.get(); return 0; }", "E2001", "cannot assign type Int to String"},
		{"method of a computed value used as a value", "struct P { x: Int; }\nimpl P { fn get() -> Int { return self.x; } }\nfn main() -> Int { let f = P { x: 1 }.get; return 0; }", "E2014", "a method can be used as a value on a variable or a field"},
		{"struct lacks the trait", "trait T { fn m() -> Int; }\nstruct P { x: Int; }\nfn use(t: T) -> Int { return t.m(); }\nfn main() -> Int { return use(P { x: 1 }); }", "E2010", "cannot use type P as argument of type T"},
		{"method not in the trait", "trait T { fn m() -> Int; }\nfn use(t: T) -> Int { return t.other(); }\nfn main() -> Int { return 0; }", "E2015", "trait T has no method 'other'"},
		{"generic result", "fn id<T>(v: T) -> T { return v; }\nfn main() -> Int { let s: String = id<Int>(1); return 0; }", "E2001", "cannot assign type Int to String"},
		{"generic argument", "fn id<T>(v: T) -> T { return v; }\nfn main() -> Int { return id<Int>(\"one\"); }", "E2010", "cannot use type String as argument of type Int"},
		{"operation on a type parameter", "fn twice<T>(v: T) -> T { return v + v; }\nfn main() -> Int { return twice<Int>(1); }", "E2006", "operator + not defined for types T and T"},
		{"null into a plain type", "fn main() -> Int { let n: Int = null; return n; }", "E2001", "cannot assign type null to Int"},
		{"optional used directly", "fn main() -> Int { let n: Int? = 1; return n + 1; }", "E2017", "the value may be null"},
		{"field of an optional", "struct P { x: Int; }\nfn main() -> Int { let p: P? = null; return p.x; }", "E2017", "cannot read 'x' of P?"},
		{"fallback of another type", "fn main() -> Int { let n: Int? = null; return n ?? \"none\"; }", "E2001", "fallback of type String does not match Int"},
		{"value of type Any", "fn show(v: Any) -> Int { return v + 1; }\nfn main() -> Int { return show(1); }", "E2006", "operator + not defined for types Any and Int"},
		{"Any into a plain type", "fn show(v: Any) -> Int { return v; }\nfn main() -> Int { return show(1); }", "E2003", "cannot return type Any"},
		{"push of another type", "fn main() -> Int { let xs = [1]; xs.push(\"two\"); return 0; }", "E2010", "cannot use type String as argument of type Int"},
		{"comparing unrelated types", "fn main() -> Int { if 1 == \"one\" { return 1; } return 0; }", "E2006", "operator == not defined for types Int and String"},
		{"untyped empty array", "fn main() -> Int { let xs = []; xs.push(1); return 0; }", "E2020", "cannot infer the element type"},
		{"assign to a constant", "fn main() -> Int { const c = 1; c = 2; return c; }", "E2022", "cannot assign to constant 'c'"},
		{"missing return", "fn f(n: Int) -> Int { if n > 0 { return 1; } }\nfn main() -> Int { return f(1); }", "E2004", "can reach its end without returning"},
		{"impossible cast", "fn main() -> Int { let s = 1 as String; return 0; }", "E2021", "cannot cast Int to String"},
		{"generic impl without parameters", "struct Box<T> { value: T; }\nimpl Box { fn get() -> T { return self.value; } }\nfn main() -> Int { return 0; }", "E1001", "undefined type 'T'"},
		{"method type argument nothing determines", "struct U { n: Int; }\nimpl U { fn none<T>() -> [T] { let _n = self.n; return []; } }\nfn size<V>(items: [V]) -> Int { return 0; }\nfn main() -> Int { let u = U { n: 1 }; return size(u.none()); }", "E2024", "cannot infer the type arguments of method 'none'"},
		{"trait type parameter count", "trait C { fn conv<T>(v: T) -> T; }\nstruct W { n: Int; }\nimpl C for W { fn conv<A, B>(v: A) -> A { return v; } }\nfn main() -> Int { return 0; }", "E2013", "has 2 type parameters, trait requires 1"},
		{"field used after a call that may change it", "struct P { x: Int?; }\nfn touch(p: P) -> Void { p.x = null; }\nfn main() -> Int { let p = P { x: 1 }; if p.x != null { touch(p); return p.x + 1; } return 0; }", "E2017", "the value may be null"},
		{"type test that can never succeed", "fn main() -> Int { let n = 1; if n is String { return 1; } return 0; }", "E2021", "Int can never be String"},
		{"null test on a variable a lambda assigns", "fn main() -> Int {\n let n: Int? = 1;\n let clear = fn() -> Void { n = null; };\n if n != null { clear(); return n + 1; }\n return 0;\n}", "E2017", "the value may be null"},
		{"compound assignment to a constant", "fn main() -> Int { const c = 1; c += 1; return c; }", "E2022", "cannot assign to constant 'c'"},
		{"compound assignment of another type", "fn main() -> Int { let n = 1; n += \"one\"; return n; }", "E2006", "operator + not defined for types Int and String"},
		{"increment as an expression", "fn main() -> Int { let n = 1; let m = n++; return m; }", "E0003", "expected ';'"},
		{"integer literal out of range", "fn main() -> Int { return 140737488355328; }", "E2025", "is out of range"},
		{"assignment to a loop index", "fn main() -> Int { for i, v in [1] { i = i + 1; } return 0; }", "E2026", "cannot assign to loop index 'i'"},
		{"assignment to a loop variable", "fn main() -> Int { let xs = [1]; for v in xs { v *= 2; } return 0; }", "E2026", "cannot assign to loop variable 'v'"},
		{"undefined struct", "fn main() -> Int { let p = Nope { x: 1 }; return 0; }", "E2016", "undefined struct 'Nope'"},
		{"assign to function", "fn f() -> Int { return 1; }\nfn main() -> Int { f = f; return 0; }", "E5003", "cannot assign to 'f'"},
		{"break outside loop", "fn main() -> Void { break; }", "E5005", "'break' outside of a loop"},
		{"missing main", "fn helper() -> Int { return 1; }", "E6006", "no 'main' function"},
		{"main with parameters", "fn main(a: Int) -> Int { return a; }", "E6006", "must not take parameters"},
		{"missing module", "import nowhere; fn main() -> Int { return 0; }", "E8001", "module 'nowhere' not found"},
		{"uninferred generic", "fn none<T>() -> [T] { return []; }\nfn size<U>(items: [U]) -> Int { return 0; }\nfn main() -> Int { return size(none()); }", "E3001", "Cannot infer type arguments"},
		{"type pattern that can never match", "fn main() -> Int { match 1 { case String: { return 1; } default: { return 0; } } }", "E2021", "Int can never be String"},
		{"variable as a pattern", "fn main() -> Int { let limit = 1; match 1 { case limit: { return 1; } default: { return 0; } } }", "E1005", "'limit' cannot be a pattern: it is a variable; a pattern names a constant or a literal"},
		{"parameter as a pattern", "fn f(limit: Int) -> Int { match 1 { case limit: { return 1; } default: { return 0; } } }\nfn main() -> Int { return f(1); }", "E1005", "'limit' cannot be a pattern: it is a parameter"},
		{"local constant as a pattern", "fn main() -> Int { const LIMIT = 1; match 1 { case LIMIT: { return 1; } default: { return 0; } } }", "E1005", "'LIMIT' cannot be a pattern: it is a local constant"},
		{"undeclared name as a pattern", "fn main() -> Int { match 1 { case LIMIT: { return 1; } default: { return 0; } } }", "E1005", "'LIMIT' cannot be a pattern: nothing of that name is declared"},
		{"function as a pattern", "fn limit() -> Int { return 1; }\nfn main() -> Int { match 1 { case 2 | limit: { return 1; } default: { return 0; } } }", "E1005", "'limit' cannot be a pattern: it is a function"},
		{"constant of the wrong type as a pattern", "const STOP = \"stop\";\nfn main() -> Int { match 1 { case STOP: { return 1; } default: { return 0; } } }", "E2001", "pattern of type String can never match a value of type Int"},
		{"array constant as a pattern", "const XS = [1];\nfn main() -> Int { match [1] { case XS: { return 1; } default: { return 0; } } }", "E2029", "a constant in a pattern is a String, an Int, a Float or a Bool"},
		{"underscore alone as a pattern", "fn main() -> Int { match 1 { case _: { return 1; } } }", "E1005", "'_' alone cannot be the pattern of a case"},
		{"unsupported pattern", "fn main() -> Int { match 2 { case 1 + 1: { return 1; } default: { return 0; } } }", "E7001", "this kind of match pattern is not supported yet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			result := Compile(tt.src, "test.qi", errs)

			if result.Chunk != nil {
				t.Error("expected no bytecode for a program with errors")
			}
			if len(errs.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic, got none")
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

func TestCompileStopsAtFirstFailingStage(t *testing.T) {
	// A program that cannot be resolved must not reach type checking, which
	// would report the same mistake a second time.
	errs := errors.NewHandler()
	result := Compile("fn main() -> Int { return a + b; }", "test.qi", errs)

	if len(errs.Diagnostics) != 2 {
		t.Errorf("expected exactly the 2 resolution errors, got %d", len(errs.Diagnostics))
	}
	if result.AST == nil {
		t.Error("expected the AST of the parsed program")
	}
	if result.IR != nil || result.Chunk != nil {
		t.Error("expected later stages not to run")
	}
}

func TestCompileExposesStageResults(t *testing.T) {
	errs := errors.NewHandler()
	result := Compile("fn main() -> Int { for v in [1] { print(v); } return 0; }", "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected errors: %v", errs.Diagnostics)
	}

	if !strings.Contains(result.AST.String(), "for v in") {
		t.Error("expected the AST to keep the for loop as written")
	}
	if lowered := result.Lowered.String(); strings.Contains(lowered, "for v in") || !strings.Contains(lowered, "while") {
		t.Errorf("expected the lowered program to use a while loop, got:\n%s", lowered)
	}
	if !strings.Contains(result.IR.Dump(), "fn main()") {
		t.Error("expected IR for main")
	}
	if result.Chunk.FindPrototype("main") < 0 {
		t.Error("expected bytecode for main")
	}
}

func TestCompileFileMissing(t *testing.T) {
	if _, err := CompileFile("does/not/exist.qi", errors.NewHandler()); err == nil {
		t.Error("expected an error for a missing file")
	}
}

// largeProgram builds a program far past what one-byte operands allowed:
// 256 functions and 256 constants per program, 256 registers per function.
// It has `count` struct types, each with its own field names, a trait
// implementation, a constructor and a label, so every struct adds
// functions, field-name constants, strings and numbers. It returns the
// source and what the program prints.
func largeProgram(count int) (string, string) {
	var sb strings.Builder
	sb.WriteString("import std.strings;\nimport std.arrays;\n\ntrait Shape {\n    fn area() -> Int;\n    fn name() -> String;\n}\n\n")

	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "struct S%d { w%d: Int; h%d: Int; }\n", i, i, i)
		fmt.Fprintf(&sb, "impl Shape for S%d {\n", i)
		fmt.Fprintf(&sb, "    fn area() -> Int { return self.w%d * self.h%d + %d; }\n", i, i, 100000+i)
		fmt.Fprintf(&sb, "    fn name() -> String { return \"shape-%d\"; }\n}\n", i)
		fmt.Fprintf(&sb, "fn make%d() -> Shape { return S%d { w%d: %d, h%d: 2 }; }\n\n", i, i, i, i, i)
	}

	// One function with more live variables than 256 registers could hold,
	// a closure over the last of them and a call made from up there
	sb.WriteString("fn wide(seed: Int) -> Int {\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "    let v%d = seed + %d;\n", i, i)
	}
	fmt.Fprintf(&sb, "    let addLast = fn(n: Int) -> Int { return n + v%d; };\n", count-1)
	sb.WriteString("    let total = 0;\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "    total += v%d;\n", i)
	}
	sb.WriteString("    return addLast(total);\n}\n\n")

	sb.WriteString("fn main() -> Int {\n    let shapes: [Shape] = [];\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "    shapes.push(make%d());\n", i)
	}
	sb.WriteString(`    let area = 0;
    let letters = 0;
    for i, shape in shapes {
        area += shape.area();
        letters += shape.name().len();
        if i == ` + fmt.Sprint(count-1) + ` { println(shape.name(), shape.area()); }
    }
    println(shapes.len(), area, letters, wide(3));
    return 0;
}
`)

	area, letters, wide := 0, 0, 0
	for i := 0; i < count; i++ {
		area += i*2 + 100000 + i
		letters += len(fmt.Sprintf("shape-%d", i))
		wide += 3 + i
	}
	wide += 3 + count - 1
	last := count - 1
	want := fmt.Sprintf("shape-%d %d\n%d %d %d %d\n", last, last*2+100000+last, count, area, letters, wide)
	return sb.String(), want
}

func TestLargeProgram(t *testing.T) {
	const count = 400
	src, want := largeProgram(count)

	errs := errors.NewHandler()
	result := Compile(src, "large.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	chunk := result.Chunk

	// The program really is past the old limits
	if len(chunk.Prototypes) <= 256 {
		t.Errorf("expected more than 256 functions, got %d", len(chunk.Prototypes))
	}
	if len(chunk.Constants) <= 256 {
		t.Errorf("expected more than 256 constants, got %d", len(chunk.Constants))
	}
	if idx := chunk.FindPrototype("wide"); idx < 0 || chunk.Prototypes[idx].RegisterCount <= 256 {
		t.Errorf("expected 'wide' to need more than 256 registers")
	}

	run := func(chunk *vm.Chunk) string {
		t.Helper()
		var out bytes.Buffer
		machine := vm.NewVM()
		machine.SetOutput(&out)
		if _, err := machine.Exec(chunk); err != nil {
			t.Fatalf("runtime error: %v", err)
		}
		return out.String()
	}
	if got := run(chunk); got != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, got)
	}

	// The bytecode file holds the same program
	data, err := vm.SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}
	loaded, err := vm.DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}
	if got := run(loaded); got != want {
		t.Errorf("unexpected output from the bytecode file\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}

func TestLargeProgramRuntimeErrorLocation(t *testing.T) {
	// An error raised in a function with a high index, from a register
	// above 255, is still reported where it happened
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "fn f%d(xs: [Int]) -> Int { return %d; }\n", i, i)
	}
	sb.WriteString("fn last(xs: [Int]) -> Int {\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "    let v%d = %d;\n", i, i)
	}
	sb.WriteString("    let at = v0")
	for i := 1; i < 300; i++ {
		fmt.Fprintf(&sb, " + v%d", i)
	}
	sb.WriteString(";\n    return xs[at];\n}\n")
	sb.WriteString("fn main() -> Int { return f299([1]) + last([1, 2, 3]); }\n")

	_, _, err := runProgram(t, sb.String())
	if err == nil {
		t.Fatal("expected an out-of-bounds error")
	}
	for _, want := range []string{"index out of bounds: 44850", "in function 'last'", "return xs[at];"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
}

func TestRecursiveGenericFunctions(t *testing.T) {
	// A generic function may call itself, and generic functions may call
	// each other: the instances are the same ones again, not new ones
	decls := `
		fn count<T>(items: [T], from: Int) -> Int {
			if from >= items.len() { return 0; }
			return 1 + count<T>(items, from + 1);
		}

		fn reversed<T>(items: [T]) -> [T] {
			let out: [T] = [];
			collect<T>(items, items.len() - 1, out);
			return out;
		}
		fn collect<T>(items: [T], at: Int, out: [T]) {
			if at < 0 { return; }
			out.push(items[at]);
			collect<T>(items, at - 1, out);
		}

		// Two functions that need each other
		fn evens<T>(items: [T], at: Int) -> [T] {
			if at >= items.len() { return []; }
			let rest = odds<T>(items, at + 1);
			let out: [T] = [items[at]];
			for item in rest { out.push(item); }
			return out;
		}
		fn odds<T>(items: [T], at: Int) -> [T] {
			if at >= items.len() { return []; }
			return evens<T>(items, at + 1);
		}

		// An instance that needs another one with a bigger type, once
		fn pair<T>(value: T) -> [T] { return [value, value]; }
		fn nested<T>(value: T) -> [[T]] { return pair<[T]>(pair<T>(value)); }
		fn deeper<T>(value: T) -> Int { return (nested<[T]>([value])).len(); }
	`
	expectOutput(t, decls, `
		println(count([1, 2, 3], 0), count(["a", "b"], 0), count<Bool>([], 0));
		println(reversed([1, 2, 3]), reversed(["x", "y"]));
		println(evens([10, 11, 12, 13, 14], 0), evens(["a", "b", "c"], 0));
		println(nested(7), nested("s"), deeper(1.5));
	`, `3 2 0
[3, 2, 1] ["y", "x"]
[10, 12, 14] ["a", "c"]
[[7, 7], [7, 7]] [["s", "s"], ["s", "s"]] 2
`)

	// A function whose instances each need a bigger one never ends
	errs := errors.NewHandler()
	Compile(`
		fn grow<T>(value: T, n: Int) -> Int {
			if n == 0 { return 0; }
			return grow<[T]>([value], n - 1);
		}
		fn main() -> Int { return grow<Int>(1, 3); }
	`, "test.qi", errs)
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E3002" || !strings.Contains(errs.Diagnostics[0].Message, "'grow'") {
		t.Errorf("expected one E3002 about grow, got %v", errs.Diagnostics)
	}
}

func TestGeneratedInstancesHaveAStableOrder(t *testing.T) {
	// The same source must compile to the same bytecode every time
	src := `
		fn a<T>(v: T) -> T { return b<T>(v); }
		fn b<T>(v: T) -> T { return c<T>(v); }
		fn c<T>(v: T) -> T { return v; }
		fn main() -> Int {
			println(a(1), a("s"), b(true), c(2.5), a([1]), b(["x"]));
			return 0;
		}
	`
	var first []string
	for run := 0; run < 20; run++ {
		errs := errors.NewHandler()
		result := Compile(src, "test.qi", errs)
		if errs.HasErrors() {
			t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
		}
		var names []string
		for _, proto := range result.Chunk.Prototypes {
			names = append(names, proto.Name)
		}
		if run == 0 {
			first = names
			continue
		}
		if strings.Join(names, " ") != strings.Join(first, " ") {
			t.Fatalf("run %d produced another order:\n%v\n%v", run, first, names)
		}
	}
	if len(first) < 10 {
		t.Errorf("expected an instance per type, got %v", first)
	}
}

func TestStringInterpolation(t *testing.T) {
	expectOutput(t, `
		struct Point { x: Int; y: Int; }
		enum Shape { Circle(Float), Empty }
		trait Named { fn name() -> String; }
		impl Named for Point { fn name() -> String { return "point"; } }
		impl Point { fn sum() -> Int { return self.x + self.y; } }

		fn double(n: Int) -> Int { return n * 2; }
		fn greet(name: String, age: Int?) -> String {
			if age == null { return "${name}, age unknown"; }
			return "${name} is ${age}, next year ${age + 1}";
		}
		const UNIT = "cm";
		const LABEL = "size in ${UNIT}";
	`, `
		let n = 3;
		let name = "ada";
		let p = Point { x: 7, y: 2 };

		// Variables, expressions, calls, fields and methods
		println("n is ${n}");
		println("${name} has ${name.len()} letters; twice ${double(name.len())}");
		println("sum ${n + 4 * 2}, test ${n > 2 && name == "ada"}, half ${1.5}, nothing ${null}");
		println("point ${p}, x ${p.x}, sum ${p.sum()}, named ${p.name()}");
		println("made here: ${Point { x: 1, y: 1 }.sum()}, shapes ${Shape.Circle(2.0)} ${Shape.Empty}");
		println("items ${[1, 2, 3]}, count ${[1, 2].len()}, second ${[10, 20][1]}");

		// Any position, any number, and none
		println("${n}", "${n}${n}", "${n}-${n}", "[${""}]", "plain");
		println("a" + "${n}" + "b", "${n}${name}".len(), "${n}" == "3", "${name}!".upper());

		// Strings and interpolation inside an interpolation
		println("outer ${"inner ${n + 1} done"} end");
		println("say ${"\"hi\""} twice: ${"ab".repeat(n)}");

		// Lambdas, in and out
		println("squares ${[1, 2, 3].map(fn(v: Int) -> Int { return v * v; })}");
		let label = fn(v: Int) -> String { return "<${v}/${n}>"; };
		println([1, 2].map(label));

		// What is not interpolation
		println("price: $5, set {a, b}, escaped \${n}, at the end $");

		// The value is worked out each time, left to right
		let count = 0;
		let next = fn() -> Int { count++; return count; };
		println("${next()} ${next()} ${next()}", count);

		// In constants, returns, conditions and optionals
		println(LABEL, greet("bob", 41), greet("eve", null));
		let maybe: Int? = null;
		println("maybe ${maybe}, or ${maybe ?? 0}");
		if "${n}" == "3" { println("as a condition"); }
	`, `n is 3
ada has 3 letters; twice 6
sum 11, test true, half 1.5, nothing null
point {x: 7, y: 2}, x 7, sum 9, named point
made here: 2, shapes Circle(2) Empty
items [1, 2, 3], count 2, second 20
3 33 3-3 [] plain
a3b 4 true ADA!
outer inner 4 done end
say "hi" twice: ababab
squares [1, 4, 9]
["<1/3>", "<2/3>"]
price: $5, set {a, b}, escaped ${n}, at the end $
1 2 3 3
size in cm bob is 41, next year 42 eve, age unknown
maybe null, or 0
as a condition
`)

	// A program's own toString does not get in the way
	out, _, err := runProgram(t, `fn main() -> Int { let toString = 5; println("v ${toString}"); return 0; }`)
	if err != nil || out != "v 5\n" {
		t.Errorf("expected a local named toString to be just a value, got %q, %v", out, err)
	}
}

func TestStringInterpolationErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
		msg  string
		col  int
	}{
		// What is inside is checked like any expression, and reported where it is
		{"unknown name", `fn main() -> Int { println("a ${missing} b"); return 0; }`, "E1001", "undefined identifier 'missing'", 33},
		{"type error", `fn main() -> Int { println("a ${1 + "x"} b"); return 0; }`, "E2006", "", 35},
		{"optional used as a value", `fn f(n: Int?) -> String { return "v ${n + 1}"; }` + "\nfn main() -> Int { return 0; }", "E2017", "the value may be null", 39},
		{"wrong argument", `fn half(n: Int) -> Int { return n / 2; }` + "\n" + `fn main() -> Int { println("${half("x")}"); return 0; }`, "E2010", "cannot use type String as argument of type Int", 36},
		// The result is a text
		{"used as a number", `fn main() -> Int { let n: Int = "x ${1}"; return n; }`, "E2001", "cannot assign type String to Int", 0},
		// Mistakes in the form
		{"nothing inside", `fn main() -> Int { println("a ${} b"); return 0; }`, "E0002", "nothing between '${' and '}' in a string", 31},
		{"never closed", `fn main() -> Int { println("a ${1 + 2 b"); return 0; }`, "E0002", "unterminated '${' in a string", 31},
		{"half an expression", `fn main() -> Int { println("a ${1 + } b"); return 0; }`, "E0001", "expected expression", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			Compile(tt.src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
			if tt.col != 0 && d.Pos.Col != tt.col {
				t.Errorf("expected the error at column %d, got %d", tt.col, d.Pos.Col)
			}
		})
	}

	// A broken one is a single error, with how to fix it
	errs := errors.NewHandler()
	Compile("fn main() -> Int {\n    println(\"a ${1 + 2 b\");\n    return 0;\n}\n", "test.qi", errs)
	if len(errs.Diagnostics) != 1 || !strings.Contains(errs.Diagnostics[0].Suggestion, "Close the expression with '}'") {
		t.Errorf("expected one error with a suggestion, got %v", errs.Diagnostics)
	}

	// A runtime error inside one is located like any other
	_, _, err := runProgram(t, "fn main() -> Int {\n    let xs = [1];\n    println(\"third: ${xs[2]}\");\n    return 0;\n}\n")
	if err == nil || !strings.Contains(err.Error(), "index out of bounds: 2 (length 1)") || !strings.Contains(err.Error(), "test.qi:3:") {
		t.Errorf("expected an out-of-bounds error on line 3, got %v", err)
	}
}

// After an if, what every way through it ends with still holds: a value
// filled in when it was missing is there, whichever branch ran.
func TestNarrowingSurvivesTheBranchesOfAnIf(t *testing.T) {
	expectOutput(t, `
		import std.dict;
		import std.dict.{Dict};

		struct Cache { known: Dict<[Int]>; loads: Int = 0; }

		impl Cache {
			// The usual lazy lookup: load what is not there yet
			fn get(name: String) -> [Int] {
				let found = self.known.get(name);

				if found == null {
					self.loads++;
					found = [name.len(), self.loads];
					self.known.set(name, found);
				}

				return found;
			}
		}

		fn label(n: Int, verbose: Bool) -> String {
			let text: String? = null;

			if n > 100 {
				text = "large";
			} else if n > 10 {
				text = "medium";
			} else {
				text = "small";
			}

			if verbose {
				text = text + " (" + toString(n) + ")";
			}

			return text.upper();
		}

		fn first(items: [Int]) -> Int {
			let best = items.first();

			if best != null {
				println("found", best);
			} else {
				best = -1;
			}

			return best + 0;
		}
	`, `
		let cache = Cache { known: dict.empty<[Int]>() };
		println(cache.get("ada"), cache.get("ada"), cache.get("grace"), cache.loads);
		println(label(500, false), label(50, true), label(5, false));
		println(first([7, 8]), first([]));
	`, `[3, 1] [3, 1] [5, 2] 2
LARGE MEDIUM (50) SMALL
found 7
7 -1
`)

	// A way through that can leave the value null keeps it optional
	refused := []string{
		"let x: Int? = null; if c { x = 1; } println(x + 1);",
		"let x: Int? = 4; if c { x = null; } println(x + 1);",
		"let x: Int? = null; if c { x = 1; } else if !c { x = 2; } println(x + 1);",
		"let x: Int? = null; if x == null { println(1); } println(x + 1);",
		"let x: Int? = null; if c { x = 1; } else { x = null; } println(x + 1);",
	}
	for _, body := range refused {
		errs := errors.NewHandler()
		Compile("fn main() -> Int {\nlet c = true;\n"+body+"\nreturn 0;\n}\n", "test.qi", errs)
		if !errs.HasErrors() || errs.Diagnostics[0].Code != "E2017" {
			t.Errorf("%s: expected E2017, got %v", body, errs.Diagnostics)
		}
	}
}

// An array is changed in place and through every name it has, so it is
// only ever seen with its own element type: an [Int] taken for an [Int?]
// could be given a null, which the [Int] would then hold.
func TestArraysKeepTheirElementType(t *testing.T) {
	decls := `
		import std.result.{Result};

		trait Shape { fn area() -> Int; }
		struct Square { side: Int; }
		impl Shape for Square { fn area() -> Int { return self.side * self.side; } }
		struct Bag { squares: [Square]; }

		fn optionals(items: [Int?]) { items.push(null); }
		fn shapes(items: [Shape]) -> Int { return items.len(); }
		fn squares() -> [Square] { return [Square { side: 1 }]; }
	`
	refused := []struct{ name, body, suggestion string }{
		{"a variable", `let a: [Int] = [1]; let b: [Int?] = a;`, "items.map(item -> item as Int?)"},
		{"an argument", `let a: [Int] = [1]; optionals(a);`, "could then be given values of type Int?, which it cannot hold"},
		{"an argument of a trait", `let a = [Square { side: 2 }]; shapes(a);`, "fn f<T: Shape>(items: [T])"},
		{"a field", `let bag = Bag { squares: [] }; let all: [Shape] = bag.squares;`, "Take any array of them"},
		{"a return", `let f = fn() -> [Shape] { return squares(); };`, ""},
		{"an assignment", `let b: [Int?] = [null]; let a: [Int] = [1]; b = a;`, ""},
		{"nested", `let a: [[Int]] = [[1]]; let b: [[Int?]] = a;`, ""},
		{"inside an enum", `let r: Result<[Int], String> = Result.Ok([1]); let w: Result<[Int?], String> = r;`, ""},
		{"a function's result", `let f: fn() -> [Shape] = squares;`, ""},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			Compile(decls+"\nfn main() -> Int {\n"+tt.body+"\nreturn 0;\n}\n", "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected the array to be refused")
			}
			if d := errs.Diagnostics[0]; !strings.Contains(d.Suggestion, tt.suggestion) {
				t.Errorf("expected a suggestion containing %q, got %q (%s)", tt.suggestion, d.Suggestion, d.Message)
			}
		})
	}

	// What is fine: a literal made for the wider type, a function that
	// takes any array of a trait, a new array, a cast that checks
	expectOutput(t, decls+`
		fn total<T: Shape>(items: [T]) -> Int {
			return items.fold(0, (sum, item) -> sum + item.area());
		}
	`, `
		let maybe: [Int?] = [1, null, 3];
		let some: [Shape] = [Square { side: 2 }, Square { side: 3 }];
		let mine = [Square { side: 4 }];
		println(maybe, shapes(some), total(some), total(mine), total(squares()));

		let widened: [Shape] = mine.map(square -> square as Shape);
		widened.push(Square { side: 1 });
		println(shapes(widened), mine.len());

		let anything: [Any] = [1, 2];
		let ints = anything as [Int];
		let wider = [1, 2] as [Int?];
		println(ints[0] + ints[1], wider);
	`, `[1, null, 3] 2 13 16 1
2 1
3 [1, 2]
`)
}
