package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestOperatorsOnStructs(t *testing.T) {
	expectOutput(t, `
		import std.ops;
		import std.ops.{Equal, Ordered, Add, Subtract, Multiply, Divide, Show};

		struct Point { x: Int; y: Int; }
		impl Equal for Point { fn equals(other: Point) -> Bool { return self.x == other.x && self.y == other.y; } }
		impl Add for Point { fn plus(other: Point) -> Point { return Point { x: self.x + other.x, y: self.y + other.y }; } }
		impl Subtract for Point { fn minus(other: Point) -> Point { return Point { x: self.x - other.x, y: self.y - other.y }; } }
		impl Multiply for Point { fn times(other: Point) -> Point { return Point { x: self.x * other.x, y: self.y * other.y }; } }
		impl Divide for Point { fn dividedBy(other: Point) -> Point { return Point { x: self.x / other.x, y: self.y / other.y }; } }
		impl Show for Point { fn show() -> String { return "(${self.x}, ${self.y})"; } }

		struct Version { major: Int; minor: Int; }
		impl Ordered for Version {
			fn compare(other: Version) -> Int {
				if self.major != other.major { return self.major - other.major; }
				return self.minor - other.minor;
			}
		}

		// Without the trait a struct is only equal to itself
		struct Plain { n: Int; }

		enum Suit { Clubs, Hearts }
		impl Ordered for Suit {
			fn compare(other: Suit) -> Int { return self.rank() - other.rank(); }
		}
		impl Suit {
			fn rank() -> Int {
				match self {
					case Suit.Clubs: { return 0; }
					case Suit.Hearts: { return 1; }
				}
			}
		}

		struct Path { steps: [Point]; calls: Int; }
		impl Path {
			fn last() -> Point { self.calls++; return self.steps[self.steps.len() - 1]; }
		}
	`, `
		let a = Point { x: 1, y: 2 };
		let b = Point { x: 1, y: 2 };
		let c = Point { x: 3, y: 4 };
		println(a == b, a != b, a == c, a != c, a == a);
		println(a + c, c - a, a * c, c / a, a + b + c, (a + c) * (c - a));
		println(a + c == Point { x: 4, y: 6 }, a == b && b != c, !(a == c));

		// Through an operator and an assignment
		let sum = a;
		sum += c;
		sum -= b;
		sum *= c;
		println(sum, a);
		let path = Path { steps: [a, c], calls: 0 };
		path.steps[0] += c;
		println(path.steps[0], path.last() + path.last(), path.calls);

		// Order
		let v1 = Version { major: 1, minor: 9 };
		let v2 = Version { major: 1, minor: 10 };
		let v3 = Version { major: 2, minor: 0 };
		println(v1 < v2, v2 < v1, v1 <= v1, v1 >= v2, v3 > v2, v1 < v2 && v2 < v3);
		println(v1.isBefore(v2), v3.isAfter(v3), Suit.Clubs < Suit.Hearts, Suit.Hearts <= Suit.Clubs);
		println(ops.max(v1, v3).major, ops.min(v2, v1).minor, ops.sorted([v3, v1, v2]).map(fn(v: Version) -> Int { return v.major * 100 + v.minor; }));

		// Arrays of them, through the library
		println([a, c].contains(b), [c, a].indexOf(b), ops.contains([a], c), ops.indexOf([c, c, a], b), ops.same([a, c], [b, c]), ops.same([a], [c]));

		// What does not say when it is equal is equal by its fields
		let p = Plain { n: 1 };
		let q = Plain { n: 1 };
		println(p == p, p == q, p != q, [p].contains(q), [p].contains(p));

		// Null on either side is still a test for null
		let maybe: Point? = null;
		let other: Point? = a;
		println(maybe == null, other != null, other == null);
	`, `true false false true true
(4, 6) (2, 2) (3, 8) (3, 2) (5, 8) (8, 12)
true true true
(9, 16) (1, 2)
(4, 6) (6, 8) 2
true false true false true true
true false true false
2 9 [109, 110, 200]
true 1 false 2 true false
true true false true true
true true false
`)
}

func TestShowIsHowAValueReads(t *testing.T) {
	expectOutput(t, `
		import std.ops;
		import std.ops.{Show};

		struct Money { cents: Int; }
		impl Show for Money {
			fn show() -> String {
				let rest = self.cents % 100;
				if rest < 10 { return "${self.cents / 100}.0${rest}"; }
				return "${self.cents / 100}.${rest}";
			}
		}
		enum Level { Low, High(Int) }
		impl Show for Level {
			fn show() -> String {
				match self {
					case Level.Low: { return "low"; }
					case Level.High(n): { return "high by ${n}"; }
				}
			}
		}
		struct Raw { n: Int; }

		fn label<T: Show>(item: T) -> String { return "[${item}]"; }
		fn boxed(item: Show) -> String { return "<${item}>"; }
	`, `
		let price = Money { cents: 1205 };
		println(price, "costs ${price}", toString(price), "${price}" + "!", price.show());
		print(price, Level.Low, Level.High(3));
		println("");
		println(label(price), label(Level.High(2)), label(7), label("text"), label(true), label(2.5));
		println(boxed(price), boxed(Level.Low), boxed(1), ops.join([price, Money { cents: 5 }], " + "), ops.join(["a", "b"], "-"), ops.join([1, 2, 3], ""));

		// Inside another value it is written as any value is
		println([price], Raw { n: 1 }, "${Raw { n: 2 }}");
	`, `12.05 costs 12.05 12.05 12.05! 12.05
12.05 low high by 3
[12.05] [high by 2] [7] [text] [true] [2.5]
<12.05> <low> <1> 12.05 + 0.05 a-b 123
[12.05] {n: 1} {n: 2}
`)
}

func TestTraitsWithDefaultMethods(t *testing.T) {
	expectOutput(t, `
		trait Greeter {
			fn name() -> String;
			fn greet() -> String { return "hello, ${self.name()}"; }
			fn greetTwice() -> String { return self.greet() + " and " + self.greet(); }
			fn loud(times: Int) -> String { return (self.name().upper() + "!").repeat(times); }
		}
		struct Cat { }
		impl Greeter for Cat { fn name() -> String { return "cat"; } }
		struct Dog { calls: Int; }
		impl Greeter for Dog {
			fn name() -> String { self.calls++; return "dog"; }
			fn greet() -> String { return "woof from ${self.name()}"; }
		}
		enum Bird { Owl, Crow }
		impl Greeter for Bird {
			fn name() -> String {
				match self {
					case Bird.Owl: { return "owl"; }
					case Bird.Crow: { return "crow"; }
				}
			}
		}

		// A default that uses Self, and one that calls a method with a default
		trait Shape {
			fn area() -> Int;
			fn larger(other: Self) -> Self {
				if other.area() > self.area() { return other; }
				return self;
			}
			fn describe() -> String { return "area ${self.area()}"; }
		}
		struct Square { side: Int; }
		impl Shape for Square { fn area() -> Int { return self.side * self.side; } }
		struct Rect { w: Int; h: Int; }
		impl Shape for Rect {
			fn area() -> Int { return self.w * self.h; }
			fn describe() -> String { return "${self.w}x${self.h}"; }
		}

		fn welcome<G: Greeter>(guest: G) -> String { return guest.greetTwice(); }
	`, `
		let dog = Dog { calls: 0 };
		println(Cat { }.greet(), dog.greet(), Bird.Owl.greet(), Bird.Crow.loud(2));
		println(Cat { }.greetTwice(), dog.greetTwice(), dog.calls);

		// Through the trait, each value does what its own type says
		let all: [Greeter] = [Cat { }, Dog { calls: 0 }, Bird.Crow];
		for one in all { println(one.greet(), "/", one.loud(1)); }
		println(welcome(Cat { }), "/", welcome(Bird.Owl));

		println(Square { side: 2 }.larger(Square { side: 3 }).side, Square { side: 5 }.larger(Square { side: 3 }).side, Square { side: 2 }.describe(), Rect { w: 2, h: 3 }.describe(), Rect { w: 1, h: 1 }.larger(Rect { w: 2, h: 2 }).describe());
		let shapes: [Shape] = [Square { side: 3 }, Rect { w: 4, h: 5 }];
		println(shapes.map(fn(shape: Shape) -> String { return shape.describe(); }));
	`, `hello, cat woof from dog hello, owl CROW!CROW!
hello, cat and hello, cat woof from dog and woof from dog 3
hello, cat / CAT!
woof from dog / DOG!
hello, crow / CROW!
hello, cat and hello, cat / hello, owl and hello, owl
3 5 area 4 2x3 2x2
["area 9", "4x5"]
`)
}

func TestRequirementsOnTypeParameters(t *testing.T) {
	expectOutput(t, `
		import std.ops;
		import std.ops.{Equal, Ordered, Show};

		struct Version { major: Int; minor: Int; }
		impl Ordered for Version {
			fn compare(other: Version) -> Int { return (self.major - other.major) * 100 + self.minor - other.minor; }
		}
		impl Show for Version { fn show() -> String { return "v${self.major}.${self.minor}"; } }
		impl Equal for Version { fn equals(other: Version) -> Bool { return self.compare(other) == 0; } }

		// Compiled once for each type it is used with
		fn clampTo<T: Ordered + Show>(value: T, low: T, high: T) -> String {
			if value < low { return low.show(); }
			if value > high { return high.show(); }
			return value.show();
		}

		fn count<T: Equal>(items: [T], wanted: T) -> Int {
			let n = 0;
			for item in items { if item == wanted { n++; } }
			return n;
		}

		// A struct whose methods need something of T. These are compiled
		// once for every T, and find the method on the value as it runs
		struct Sorted<T: Ordered> { items: [T]; }
		impl<T: Ordered> Sorted<T> {
			fn add(item: T) -> Sorted<T> {
				let at = 0;
				while at < self.items.len() && self.items[at].compare(item) <= 0 { at++; }
				self.items = self.items.inserted(at, item);
				return self;
			}
			fn top() -> T? {
				let best = self.items.first()?;
				for item in self.items { if item > best { best = item; } }
				return best;
			}
			fn has(item: T) -> Bool {
				for mine in self.items { if mine.compare(item) == 0 { return true; } }
				return false;
			}
			fn between(low: T, high: T) -> [T] {
				return self.items.filter(fn(item: T) -> Bool { return item >= low && item <= high; });
			}
		}

		// Methods only some boxes have
		struct Box<T> { item: T; }
		impl<T> Box<T> { fn get() -> T { return self.item; } }
		impl<T: Show> Show for Box<T> { fn show() -> String { return "box of ${self.item.show()}"; } }

		struct Pair<A, B> { first: A; second: B; }
		impl<A: Show, B: Show> Show for Pair<A, B> {
			fn show() -> String { return "${self.first.show()}&${self.second.show()}"; }
		}

		enum Tree<T: Ordered> { Leaf, Node(Tree<T>, T, Tree<T>) }
		impl<T: Ordered> Tree<T> {
			fn with(item: T) -> Tree<T> {
				match self {
					case Tree.Leaf: { return Tree.Node(Tree.Leaf, item, Tree.Leaf); }
					case Tree.Node(left, value, right): {
						if item < value { return Tree.Node(left.with(item), value, right); }
						return Tree.Node(left, value, right.with(item));
					}
				}
			}
			fn inOrder(out: [T]) -> [T] {
				match self {
					case Tree.Leaf: { }
					case Tree.Node(left, value, right): {
						left.inOrder(out);
						out.push(value);
						right.inOrder(out);
					}
				}
				return out;
			}
		}
	`, `
		let v1 = Version { major: 1, minor: 4 };
		let v2 = Version { major: 2, minor: 0 };
		let v3 = Version { major: 3, minor: 1 };
		println(clampTo(15, 0, 10), clampTo(-3, 0, 10), clampTo(5, 0, 10), clampTo("m", "a", "f"), clampTo(2.5, 1.0, 2.0), clampTo(v1, v2, v3), clampTo(v3, v1, v2));
		println(count([1, 2, 1, 3], 1), count(["a", "b"], "c"), count([v1, v2, Version { major: 1, minor: 4 }], v1), count([true, true], true));

		let numbers = Sorted<Int> { items: [] }.add(5).add(1).add(3).add(1);
		let words = Sorted<String> { items: [] }.add("pear").add("apple").add("fig");
		let versions = Sorted<Version> { items: [] }.add(v3).add(v1).add(v2);
		println(numbers.items, numbers.top(), numbers.has(3), numbers.has(4), numbers.between(1, 3));
		println(words.items, words.top(), words.has("fig"), words.between("b", "g"));
		println(ops.join(versions.items, " "), versions.top()?.show(), versions.has(Version { major: 2, minor: 0 }), ops.join(versions.between(v1, v2), " "));
		println(Sorted<Float> { items: [] }.add(2.5).add(0.5).top(), Sorted<Int> { items: [] }.top());

		println(Box<Int> { item: 4 }.show(), Box<Version> { item: v1 }.show(), Box<Box<String>> { item: Box<String> { item: "x" } }.show(), Box<[Int]> { item: [1] }.get());
		println(Pair<Int, Version> { first: 1, second: v3 }.show(), ops.join([Pair<String, Bool> { first: "a", second: true }], ","));

		let tree: Tree<Int> = Tree.Leaf;
		for n in [5, 2, 8, 1, 9, 3] { tree = tree.with(n); }
		let named: Tree<String> = Tree.Leaf;
		for word in ["pear", "apple", "fig"] { named = named.with(word); }
		println(tree.inOrder([]), named.inOrder([]));
	`, `10 0 5 f 2 v2.0 v2.0
2 0 2 2
[1, 1, 3, 5] 5 true false [1, 1, 3]
["apple", "fig", "pear"] pear true ["fig"]
v1.4 v2.0 v3.1 v3.1 true v1.4 v2.0
2.5 null
box of 4 box of v1.4 box of box of x [1]
1&v3.1 a&true
[1, 2, 3, 5, 8, 9] ["apple", "fig", "pear"]
`)
}

func TestTraitsWithTypeParameters(t *testing.T) {
	expectOutput(t, `
		import std.ops.{Iterable};

		trait Source<T> {
			fn next() -> T?;
			fn take(count: Int) -> [T] {
				let out: [T] = [];
				while out.len() < count {
					let item = self.next();
					if item == null { break; }
					out.push(item);
				}
				return out;
			}
		}

		struct Counter { n: Int; limit: Int; }
		impl Source<Int> for Counter {
			fn next() -> Int? {
				if self.n >= self.limit { return null; }
				self.n++;
				return self.n;
			}
		}

		struct Repeat<T> { item: T; }
		impl<T> Source<T> for Repeat<T> { fn next() -> T? { return self.item; } }

		struct Letters { text: String; at: Int; }
		impl Source<String> for Letters {
			fn next() -> String? {
				if self.at >= self.text.len() { return null; }
				self.at++;
				return self.text[self.at - 1];
			}
			// Its own way of taking several
			fn take(count: Int) -> [String] {
				let word = "";
				for i in arrays.range(0, count) { word = word + (self.next() ?? ""); }
				return [word];
			}
		}

		// The type argument of the trait is worked out from the value
		fn drain<T>(source: Source<T>) -> [T] { return source.take(100); }
		fn firstOf<T, S: Source<T>>(source: S) -> T? { return source.next(); }
		fn total(source: Source<Int>) -> Int { return source.take(3).sum(); }

		trait Converter<From, To> { fn convert(value: From) -> To; }
		struct Length { }
		impl Converter<String, Int> for Length { fn convert(value: String) -> Int { return value.len(); } }
		struct Twice { }
		impl Converter<Int, Int> for Twice { fn convert(value: Int) -> Int { return value * 2; } }
		fn through<A, B, C>(first: Converter<A, B>, second: Converter<B, C>, value: A) -> C {
			return second.convert(first.convert(value));
		}

		struct Range { from: Int; to: Int; }
		impl Iterable<Int> for Range { fn iterate() -> [Int] { return arrays.range(self.from, self.to); } }
		fn sumOf<C: Iterable<Int>>(items: C) -> Int {
			let total = 0;
			for n in items { total += n; }
			return total;
		}
	`, `
		println(Counter { n: 0, limit: 5 }.take(3), drain(Counter { n: 2, limit: 5 }), firstOf(Counter { n: 0, limit: 1 }), total(Counter { n: 0, limit: 9 }));
		println(Repeat<String> { item: "x" }.take(2), firstOf(Repeat<Float> { item: 1.5 }), drain(Letters { text: "abc", at: 0 }), Letters { text: "héllo", at: 1 }.take(2));

		// Seen through the trait, each does what its own type says
		let sources: [Source<Int>] = [Counter { n: 3, limit: 9 }, Repeat<Int> { item: 7 }];
		for source in sources { print(source.take(2), source.next(), ""); }
		println("");
		let texts: [Source<String>] = [Letters { text: "ab", at: 0 }, Repeat<String> { item: "z" }];
		println(texts.map(fn(source: Source<String>) -> [String] { return source.take(2); }));

		println(through(Length { }, Twice { }, "four"), Twice { }.convert(21), sumOf(Range { from: 1, to: 5 }));
		for n in (Range { from: 0, to: 3 }) { print(n, ""); }
		for i, n in (Range { from: 5, to: 7 }) { print("${i}:${n}", ""); }
		println("");
	`, `[1, 2, 3] [3, 4, 5] 1 6
["x", "x"] 1.5 ["abc"] ["él"]
[4, 5] 6 [7, 7] 7 
[["ab"], ["z", "z"]]
8 42 10
0 1 2 0:5 1:6 
`)
}

func TestStructsInBracketsAndLoops(t *testing.T) {
	expectOutput(t, `
		import std.ops.{Iterable, Indexed, IndexedSet};
		import std.list;
		import std.dict;
		import std.set;

		struct Grid { width: Int; cells: [String]; reads: Int; }
		impl Indexed<(Int, Int), String> for Grid {
			fn at(key: (Int, Int)) -> String { self.reads++; return self.cells[key.1 * self.width + key.0]; }
		}
		impl IndexedSet<(Int, Int), String> for Grid {
			fn put(key: (Int, Int), value: String) { self.cells[key.1 * self.width + key.0] = value; }
		}
		impl Iterable<String> for Grid { fn iterate() -> [String] { return self.cells; } }

		struct Scores { names: [String]; points: [Int]; }
		impl Indexed<String, Int?> for Scores {
			fn at(key: String) -> Int? {
				let i = self.names.indexOf(key)?;
				return self.points[i];
			}
		}

		fn first<T, C: Indexed<Int, T>>(items: C) -> T { return items[0]; }
	`, `
		let grid = Grid { width: 2, cells: ["a", "b", "c", "d"], reads: 0 };
		grid[(1, 0)] = "B";
		grid[(0, 1)] = grid[(1, 1)] + grid[(0, 0)];
		println(grid[(1, 0)], grid[(0, 1)], grid.cells, grid.reads);
		for cell in grid { print(cell, ""); }
		for i, cell in grid { if i == 3 { println(i, cell); } }

		let scores = Scores { names: ["ada", "bob"], points: [3, 5] };
		println(scores["bob"], scores["eve"], (scores["ada"] ?? 0) + 1);

		// The collections of the library take brackets and loops
		let names = list.of(["ada", "linus"]);
		names[1] = "grace";
		names.add("alan");
		for name in names { print(name, ""); }
		println(names[0], names[2], first(names), names.len());

		let ages = dict.empty<Int>();
		ages["ada"] = 36;
		ages["bob"] = 41;
		ages["ada"] = (ages["ada"] ?? 0) + 1;
		for name in ages { print(name, ages[name], ""); }
		println(ages["nobody"], ages.len());

		let seen = set.of(["x", "y", "x"]);
		for item in seen { print(item, ""); }
		println(seen == set.of(["y", "x"]), seen != set.of(["x"]), seen == seen);
	`, `B da ["a", "B", "da", "d"] 4
a B da d 3 d
5 null 4
ada grace alan ada alan ada 3
ada 37 bob 41 null 2
x y true true true
`)
}

func TestMethodsAsValuesAndFieldDefaults(t *testing.T) {
	expectOutput(t, `
		import std.dict;
		import std.dict.{Dict};

		struct Counter { count: Int = 0; step: Int = 1; }
		impl Counter {
			fn bump() -> Int { self.count += self.step; return self.count; }
			fn add(n: Int) { self.count += n; }
			fn plus(n: Int, m: Int) -> Int { return self.count + n + m; }
			fn ticker() -> fn() -> Int { return self.bump; }
		}

		struct Retry { times: Int = 3; wait: Int = base * 2; }
		const base = 50;

		struct Config {
			name: String;
			host: String = "localhost";
			port: Int = 8000 + 80;
			tags: [String] = [];
			retry: Retry = Retry { };
			owner: String? = null;
			headers: Dict<String> = dict.empty<String>();
		}
		struct Shelf<T> { items: [T] = []; label: String = "shelf"; }
		struct Button { label: String; onClick: fn(String) -> String = fn(label: String) -> String { return "clicked " + label; }; }
	`, `
		let c = Counter { };
		let bump = c.bump;
		let add = c.add;
		let plus = c.plus;
		println(bump(), bump(), c.count);
		add(10);
		println(c.count, plus(1, 2), [1, 2].map(fn(n: Int) -> Int { return plus(n, 0); }));

		// Handed to what takes a function
		let ages = dict.empty<Int>();
		ages.set("ada", 36);
		ages.set("bob", 41);
		println(["ada", "eve", "bob"].map(ages.get), ["ada", "eve"].filter(ages.has));
		let other = Counter { step: 5 };
		let tick = other.ticker();
		println(tick(), tick(), other.count, c.count);

		// The value follows the variable: it is read when the function is called
		let current = c;
		let currentBump = current.bump;
		current = other;
		println(currentBump(), other.count, c.count);

		// What a literal leaves out has the value the struct gives it
		let plain = Config { name: "a" };
		let custom = Config { name: "b", port: 1, tags: ["x"], owner: "ada", retry: Retry { times: 9 } };
		println(plain.host, plain.port, plain.tags, plain.retry, plain.owner, plain.headers.len());
		println(custom.host, custom.port, custom.tags, custom.retry, custom.owner);

		// Each literal gets a value of its own
		plain.tags.push("mine");
		plain.headers.set("k", "v");
		plain.retry.times = 0;
		let fresh = Config { name: "c" };
		println(plain.tags, fresh.tags, fresh.headers.len(), fresh.retry.times);

		let shelf = Shelf<Int> { };
		shelf.items.push(4);
		println(shelf.items, shelf.label, Shelf<String> { label: "top" }.label, Shelf<String> { }.items);
		println(Button { label: "ok" }.onClick("ok"), Button { label: "x", onClick: fn(l: String) -> String { return "!"; } }.onClick("x"));
	`, `1 2 2
12 15 [13, 14]
[36, null, 41] ["ada"]
5 10 10 12
15 15 12
localhost 8080 [] {times: 3, wait: 100} null 0
localhost 1 ["x"] {times: 9, wait: 100} ada
["mine"] [] 0 3
[4] shelf top []
clicked ok !
`)
}

func TestTraitErrors(t *testing.T) {
	prelude := `import std.ops;
import std.ops.{Equal, Ordered, Add, Iterable, Indexed, Show};
struct Point { x: Int; }
struct Version { n: Int; }
impl Ordered for Version { fn compare(other: Version) -> Int { return self.n - other.n; } }
impl Show for Version { fn show() -> String { return "v"; } }
impl Indexed<Int, Int> for Version { fn at(key: Int) -> Int { return key; } }
struct Sorted<T: Ordered> { items: [T]; }
struct Box<T> { item: T; }
impl<T: Ordered> Box<T> { fn bigger(other: T) -> Bool { return self.item > other; } fn get() -> T { return self.item; } fn pick<U>(u: U) -> U { let _item = self.item; return u; } }
trait Source<T> { fn next() -> T?; }
struct Words { }
impl Source<String> for Words { fn next() -> String? { return null; } }
fn largest<T: Ordered>(items: [T]) -> T? { return null; }
fn both<T: Ordered + Show>(item: T) -> String { return item.show(); }
`
	tests := []struct {
		name string
		src  string
		code string
		msg  string
		hint string
	}{
		{"a type that does not implement what a function requires", "fn main() -> Int { let p = largest([Point { x: 1 }]); return 0; }", "E2036",
			"Point does not implement Ordered, which function 'largest' requires of T", "Add: impl Ordered for Point { ... }"},
		{"...written out", "fn main() -> Int { let p = largest<Point>([]); return 0; }", "E2036", "Point does not implement Ordered", ""},
		{"one of two requirements", "struct Half { }\nimpl Show for Half { fn show() -> String { return \"\"; } }\nfn main() -> Int { println(both(Half { })); return 0; }", "E2036",
			"Half does not implement Ordered, which function 'both' requires of T", ""},
		{"a struct with a requirement", "fn main() -> Int { let s = Sorted<Point> { items: [] }; return 0; }", "E2036",
			"Point does not implement Ordered, which struct Sorted requires of T", ""},
		{"...in a type", "fn f(s: Sorted<Point>) { }\nfn main() -> Int { return 0; }", "E2036", "which struct Sorted requires of T", ""},
		{"a method only some have", "fn main() -> Int { let b = Box<Point> { item: Point { x: 1 } }; println(b.bigger(Point { x: 2 })); return 0; }", "E2036",
			"Point does not implement Ordered, which method 'bigger' requires of T", ""},
		{"a type parameter without the requirement, passed on", "fn f<T>(items: [T]) -> T? { return largest(items); }\nfn main() -> Int { return 0; }", "E2036",
			"T does not implement Ordered", "Require it where T is introduced: <T: Ordered>"},
		{"a method on a type parameter nothing is known of", "fn f<T>(a: T) -> Int { return a.compare(a); }\nfn main() -> Int { return 0; }", "E2015",
			"nothing is known of a T: it has no method 'compare'", "<T: Trait>"},
		{"a method the requirement does not have", "fn f<T: Show>(a: T) -> Int { return a.compare(a); }\nfn main() -> Int { return 0; }", "E2015",
			"T is only known to implement Show, which has no method 'compare'", ""},
		{"an operator on a type parameter without the requirement", "fn f<T>(a: T, b: T) -> Bool { return a < b; }\nfn main() -> Int { return 0; }", "E2006",
			"operator < not defined for types T and T", "<T: Ordered>"},
		{"an operator a struct does not have", "fn main() -> Int { let p = Point { x: 1 }; let q = p + p; return 0; }", "E2006",
			"operator + not defined for types Point and Point", "Implement the trait Add for Point (method 'plus')"},
		{"an order a struct does not have", "fn main() -> Int { let p = Point { x: 1 }; println(p < p); return 0; }", "E2006",
			"operator < not defined", "Implement the trait Ordered for Point (method 'compare')"},
		{"an operator between two types", "fn main() -> Int { let v = Version { n: 1 }; println(v < 3); return 0; }", "E2001",
			"cannot use < on Version and int", "Both sides of < have to be of the same type"},
		{"a loop over a struct", "fn main() -> Int { let p = Point { x: 1 }; for x in p { } return 0; }", "E2011", "cannot iterate over type Point", "implement Iterable<...> for Point (method 'iterate')"},
		{"brackets on a struct", "fn main() -> Int { let p = Point { x: 1 }; return p[0]; }", "E2006", "type Point cannot be indexed", "implement Indexed<Key, Value> for Point (method 'at')"},
		{"the wrong key", "fn main() -> Int { let v = Version { n: 1 }; return v[\"a\"]; }", "E2001", "Version is indexed by int, got string", ""},
		{"assigning to brackets that can only be read", "fn main() -> Int { let v = Version { n: 1 }; v[0] = 1; return 0; }", "E2006",
			"what is in the brackets of Version can be read, not assigned", "IndexedSet<Key, Value>"},
		{"an assignment through an operator, to brackets of a struct", "fn main() -> Int { let v = Version { n: 1 }; v[0] += 1; return 0; }", "E2006",
			"+= cannot be used on what is in the brackets of Version", "target[key] = target[key] + value;"},
		{"a trait given no type arguments", "struct C { }\nimpl Source for C { fn next() -> Int? { return 1; } }\nfn main() -> Int { return 0; }", "E2013",
			"trait Source takes 1 type arguments (<T>), got 0", "impl Source<...> for C"},
		{"a trait given type arguments it does not take", "impl Show<Int> for Point { fn show() -> String { return \"\"; } }\nfn main() -> Int { return 0; }", "E2013",
			"trait std.ops.Show takes no type arguments", ""},
		{"a method that does not fit the trait's type argument", "struct C { }\nimpl Source<String> for C { fn next() -> Int? { return 1; } }\nfn main() -> Int { return 0; }", "E2013",
			"method 'next' returns int?, trait requires string?", ""},
		{"a trait implemented twice", "impl Show for Point { fn show() -> String { return \"a\"; } }\nimpl Show for Point { fn show() -> String { return \"b\"; } }\nfn main() -> Int { return 0; }", "E2019",
			"Point already implements std.ops.Show", "A type implements a trait once"},
		{"the wrong type argument of a trait", "fn main() -> Int { let s: Source<Int> = Words { }; return 0; }", "E2001", "cannot assign type Words to Source<int>", ""},
		{"a required method left out", "trait Two { fn a() -> Int; fn b() -> Int { return 1; } }\nstruct S { }\nimpl Two for S { }\nfn main() -> Int { return 0; }", "E2012",
			"missing required method 'a'", ""},
		{"a field whose own value does not fit", "struct Conf { port: Int = \"eighty\"; }\nfn main() -> Int { return 0; }", "E2001",
			"field 'port' is of type int, and its value when none is given is of type string", ""},
		{"a field without a value of its own, left out", "struct Conf { port: Int = 80; name: String; }\nfn main() -> Int { let c = Conf { }; return 0; }", "E2018",
			"struct literal of Conf is missing 'name'", "name: Type = value;"},
		{"a field's own value that names a variable", "struct Conf { port: Int = count; }\nfn main() -> Int { let count = 1; return 0; }", "E1001", "undefined identifier 'count'", ""},
		{"a method of a computed value, as a value", "fn main() -> Int { let f = Box<Version> { item: Version { n: 1 } }.get; return 0; }", "E2014",
			"a method can be used as a value on a variable or a field", "Put the value in a variable first"},
		{"a method with type parameters, as a value", "fn main() -> Int { let b = Box<Version> { item: Version { n: 1 } }; let f = b.pick; return 0; }", "E2014",
			"method 'pick' has type parameters of its own", ""},
		{"a method that does not exist on a number", "fn main() -> Int { return 5.nope(); }", "E2015", "a number has no method 'nope'", ""},
		{"a trait for the arrays of one type", "trait Sized { fn size() -> Int; }\nimpl Sized for [Int] { fn size() -> Int { return 0; } }\nfn main() -> Int { return 0; }", "E2015",
			"a trait is implemented for every array, not for the arrays of one type", "impl<T> Sized for [T] { ... }"},
		{"an impl for arrays of something that is not its parameter", "impl<T> [U] { fn size() -> Int { return 0; } }\nfn main() -> Int { return 0; }", "E0001",
			"an impl for arrays is for every array, or for the arrays of one type", "impl<T> [T] { ... }"},
		{"a method an array does not have at all", "import std.arrays;\nfn main() -> Int { return [1, 2].nothingLikeIt(); }", "E2015",
			"an array has no method 'nothingLikeIt'", "impl<T> [T] { fn nothingLikeIt(...) { ... } }"},
		{"a method of an array misspelt", "import std.arrays;\nfn main() -> Int { return [1, 2].lenn(); }", "E2015",
			"an array has no method 'lenn'", "Did you mean 'len'?"},
		{"a method of an array misspelt, where no method of an array is called", "fn main() -> Int { return [1, 2].lenght(); }", "E2015",
			"an array has no method 'lenght'", "Did you mean 'len'?"},
		{"a method of a number misspelt, where no method of a number is called", "fn main() -> Float { return 2.0.sqroot(); }", "E2015",
			"a number has no method 'sqroot'", "Did you mean 'sqrt'?"},
		{"a short name not near enough to suggest", "fn main() -> Int { return [1].xyz(); }", "E2015",
			"an array has no method 'xyz'", "impl<T> [T] { fn xyz(...) { ... } }"},
		{"a method of a text misspelt", "import std.strings;\nfn main() -> String { return \"a\".Trim(); }", "E2015",
			"a text has no method 'Trim'", "Did you mean 'trim'?"},
		{"a method only arrays of numbers have", "import std.arrays;\nfn main() -> Int { return [\"a\"].sum(); }", "E2015",
			"[String] has no method 'sum'", "Only [Float] and [Int] have it; give [String] one of your own: impl [String] { fn sum(...) { ... } }"},
		{"a requirement where there can be none", "trait T<A: Show> { }\nfn main() -> Int { return 0; }", "E0001", "a type parameter cannot be given a requirement here", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			result := Compile(prelude+tt.src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			if result.Chunk != nil {
				t.Error("expected no bytecode for a rejected program")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(strings.ToLower(d.Message), strings.ToLower(tt.msg)) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
			if !strings.Contains(d.Suggestion, tt.hint) {
				t.Errorf("expected a suggestion containing %q, got %q", tt.hint, d.Suggestion)
			}
		})
	}
}

func TestTraitsAcrossModules(t *testing.T) {
	expectModulesOutput(t, sources{
		"main.qi": `
			import shop.money;
			import shop.money.{Money, Priced};
			import shop.cart;
			import shop.cart.{Cart};
			import std.ops;

			struct Book { title: String; cost: Money; }
			impl Priced for Book { fn price() -> Money { return self.cost; } }

			fn main() -> Int {
				let basket = cart.empty<Book>();
				basket.add(Book { title: "Dune", cost: money.of(12, 50) });
				basket.add(Book { title: "Emma", cost: money.of(7, 5) });
				println(basket.total(), basket.dearest()?.title, basket[0].title, money.of(1, 0) + money.of(0, 99) == money.of(1, 99));
				for book in basket { println(book.title, book.price(), book.label(), book.price() > money.of(10, 0)); }
				println(ops.sorted([money.of(3, 0), money.of(1, 0)]), ops.max(money.of(1, 0), money.of(2, 0)), cart.empty<Book>().total());
				return 0;
			}`,
		"shop/money.qi": `
			import std.ops.{Equal, Ordered, Add, Show};

			pub struct Money { cents: Int = 0; }
			pub fn of(whole: Int, cents: Int) -> Money { return Money { cents: whole * 100 + cents }; }

			impl Equal for Money { fn equals(other: Money) -> Bool { return self.cents == other.cents; } }
			impl Ordered for Money { fn compare(other: Money) -> Int { return self.cents - other.cents; } }
			impl Add for Money { fn plus(other: Money) -> Money { return Money { cents: self.cents + other.cents }; } }
			impl Show for Money {
				fn show() -> String {
					let rest = self.cents % 100;
					if rest < 10 { return "${self.cents / 100}.0${rest}"; }
					return "${self.cents / 100}.${rest}";
				}
			}

			// What has a price. The label is the same for everything that has one
			pub trait Priced {
				fn price() -> Money;
				fn label() -> String { return "costs ${self.price()}"; }
			}`,
		"shop/cart.qi": `
			import std.ops.{Iterable, Indexed};
			import std.arrays;
			import shop.money.{Money, Priced};

			pub struct Cart<T: Priced> { items: [T] = []; }
			pub fn empty<T: Priced>() -> Cart<T> { return Cart<T> { }; }

			impl<T: Priced> Cart<T> {
				pub fn add(item: T) { self.items.push(item); }
				pub fn total() -> Money {
					let sum = Money { };
					for item in self.items { sum += item.price(); }
					return sum;
				}
				pub fn dearest() -> T? {
					let best = self.items.first()?;
					for item in self.items {
						if item.price() > best.price() { best = item; }
					}
					return best;
				}
			}
			impl<T: Priced> Iterable<T> for Cart<T> { fn iterate() -> [T] { return self.items; } }
			impl<T: Priced> Indexed<Int, T> for Cart<T> { fn at(key: Int) -> T { return self.items[key]; } }`,
	}, `19.55 Dune Dune true
Dune 12.50 costs 12.50 true
Emma 7.05 costs 7.05 false
[1.00, 3.00] 2.00 0.00
`)
}

// The usual methods of arrays, texts and numbers are there without an
// import: the modules of the library that declare them come with any
// program that calls one. Their names are another matter: a function of
// such a module still wants its import.
func TestMethodsOfBuiltinTypesNeedNoImport(t *testing.T) {
	out, _, err := runProgram(t, `
		struct Point { x: Int; y: Int; }

		fn main() {
			println([3, 1, 2].sortedNumbers(), ["b", "a"].join("-"), "  Hi ".trim().lower().len());
			println((0 - 5).abs(), 2.pow(8), 6.25.sqrt(), 7.toFloat() / 2);
			let points = [Point { x: 1, y: 2 }];
			points.push(Point { x: 3, y: 4 });
			println(points.map(p -> p.x + p.y), points.len(), (0..4).map(n -> n * n).filter(n -> n.isEven()));
		}
	`)
	if want := "[1, 2, 3] b-a 2\n5 256 2.5 3.5\n[3, 7] 2 [0, 4]\n"; err != nil || out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s%v", want, out, err)
	}

	// A program that calls none has none of the modules in it
	errs := errors.NewHandler()
	result := Compile("fn main() { println(1 + 2); }", "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}
	for _, proto := range result.Chunk.Prototypes {
		if strings.HasPrefix(proto.Name, "std.") {
			t.Errorf("expected nothing of the library in a program that uses none of it, found %s", proto.Name)
		}
	}

	// The name of a module of the library is known without its import,
	// unless the file has something of its own by that name
	errs = errors.NewHandler()
	Compile("fn main() { println(arrays.range(0, 3).len()); }", "test.qi", errs)
	if errs.HasErrors() {
		t.Errorf("expected the module to be imported implicitly, got %v", errs.Diagnostics)
	}
	errs = errors.NewHandler()
	Compile("fn arrays() -> Int { return 1; }\nfn main() { println(arrays.range(0, 3).len()); }", "test.qi", errs)
	if !errs.HasErrors() {
		t.Error("expected the function of the file to hide the module")
	}
}

// A value whose type implements Show is written by its show wherever it
// is: alone, in an array, in a field, in what an enum or a tuple holds, in
// a text. A show that fails stops the printing, and nothing of the line is
// written.
func TestShowReachesValuesInsideOthers(t *testing.T) {
	expectOutput(t, `
		import std.ops.{Show};
		import std.result.{attempt};
		import std.dict;
		import std.dict.{Dict};

		struct Money { cents: Int; }
		impl Show for Money {
			fn show() -> String { return "${self.cents / 100}.${self.cents % 100}"; }
		}

		enum Payment { Card(Money), Cash(Money), Free }
		impl Show for Payment {
			fn show() -> String {
				return match self {
					case Payment.Card(m): "card ${m}";
					case Payment.Cash(m): "cash ${m}";
					case Payment.Free: "free";
				};
			}
		}

		struct Order { id: Int; total: Money; paid: [Payment]; }
		struct Box<T> { item: T; }
		impl<T> Show for Box<T> { fn show() -> String { return "box"; } }

		enum Tree { Leaf(Money), Node(Tree, Tree) }

		struct Fussy { n: Int; }
		impl Show for Fussy {
			fn show() -> String {
				if self.n == 0 { panic("nothing to show"); }
				return "fussy ${self.n}";
			}
		}
	`, `
		let ten = Money { cents: 1050 };
		let order = Order { id: 7, total: ten, paid: [Payment.Card(ten), Payment.Free] };
		let none: Money? = null;

		println([ten, Money { cents: 25 }], [[ten]], [none, ten]);
		println(order);
		println(Tree.Node(Tree.Leaf(ten), Tree.Leaf(Money { cents: 199 })), (ten, "ten"));
		println([Box<Int> { item: 1 }], "in a text: ${[ten]}", toString(order.paid));

		let prices = dict.empty<Money>();
		prices.set("tea", Money { cents: 350 });
		println(prices.values(), prices.entries());

		let failed = attempt(fn() -> Int {
			println("before", [Fussy { n: 1 }, Fussy { n: 0 }]);
			return 0;
		});
		println(failed.reason()?.message ?? "went on", [Fussy { n: 2 }]);
	`, `[10.50, 0.25] [[10.50]] [null, 10.50]
{id: 7, paid: [card 10.50, free], total: 10.50}
Node(Leaf(10.50), Leaf(1.99)) (10.50, "ten")
[box] in a text: [10.50] [card 10.50, free]
[3.50] [("tea", 3.50)]
nothing to show [fussy 2]
`)
}

// sorted without the function that orders, of items that have no order of
// their own, says what it takes.
func TestSortedWithoutOrder(t *testing.T) {
	errs := errors.NewHandler()
	Compile(`struct Job { rank: Int; }
fn main() -> Int { let k = [Job { rank: 2 }].sorted(); return 0; }`, "test.qi", errs)
	if len(errs.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %v", errs.Diagnostics)
	}
	d := errs.Diagnostics[0]
	if !strings.Contains(d.Message, "sorted takes the function") || !strings.Contains(d.Suggestion, "when Job is Ordered") ||
		!strings.Contains(d.Suggestion, "sortedBy(") {
		t.Errorf("got %s (%s)", d.Message, d.Suggestion)
	}
}
