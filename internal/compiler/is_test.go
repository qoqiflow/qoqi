package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestPatternTests(t *testing.T) {
	expectOutput(t, `
		enum Shape { Circle(Float), Rect(Float, Float), Empty }
		enum Expr { Num(Int), Add(Expr, Expr), Neg(Expr) }

		fn describe(s: Shape) -> String {
			if s is Shape.Circle(r) {
				return "circle ${r}";
			} else if s is Shape.Rect(w, h) && w == h {
				return "square ${w}";
			} else if s is Shape.Rect(w, _) {
				return "rect ${w}";
			}
			return "empty";
		}

		impl Shape {
			fn radius() -> Float {
				if self is Shape.Circle(r) { return r; }
				return 0.0;
			}
			fn isRound() -> Bool { return self is Shape.Circle; }
		}

		fn rounds(shapes: [Shape]) -> Int {
			let count = 0;
			for shape in shapes {
				if shape is Shape.Circle { count += 1; }
			}
			return count;
		}

		fn simplify(e: Expr) -> Expr {
			if e is Expr.Add(Expr.Num(0), right) { return simplify(right); }
			if e is Expr.Neg(Expr.Neg(inner)) { return simplify(inner); }
			if e is Expr.Add(Expr.Num(a), Expr.Num(b)) { return Expr.Num(a + b); }
			return e;
		}
	`, `
		println(describe(Shape.Circle(1.5)), describe(Shape.Rect(2.0, 2.0)), describe(Shape.Rect(2.0, 3.0)), describe(Shape.Empty));
		println(Shape.Circle(2.5).radius(), Shape.Empty.radius(), Shape.Circle(1.0).isRound(), Shape.Empty.isRound());
		println(simplify(Expr.Add(Expr.Num(0), Expr.Neg(Expr.Neg(Expr.Add(Expr.Num(2), Expr.Num(3)))))));

		// A test is a Bool like any other
		let s = Shape.Circle(2.0);
		let round = s is Shape.Circle(_);
		let big = s is Shape.Circle(r) && r > 1.0;
		let small = s is Shape.Circle(r) && r < 1.0;
		println(round, big, small, s is Shape.Empty, !(s is Shape.Empty), s is Shape.Empty || s is Shape.Circle(2.0));
		println([s is Shape.Circle, s is Shape.Rect], rounds([s, Shape.Empty, Shape.Circle(0.5)]));

		// Tuples are taken apart the same way
		let pair = (3, "three");
		if pair is (3, name) { println(name); }
		if pair is (4, name) { println("never"); } else { println("not four"); }
		if (s, pair) is (Shape.Circle(r), (n, _)) && n == 3 { println(r, n); }

		// An optional, and an Any
		let maybe: Shape? = Shape.Rect(1.0, 2.0);
		if maybe is Shape.Rect(w, h) { println(w + h, maybe.radius()); }
		maybe = null;
		if maybe is Shape.Rect(_, _) { println("never"); } else { println("null fits no pattern"); }
		let any: Any = Shape.Circle(9.0);
		if any is Shape.Circle(r) { println(r, any.isRound()); }
		if any is Expr.Num(_) { println("never"); }

		// The name of the value itself, and a variable of the block named
		// as the pattern names
		if s is Shape.Circle(s) { println(s + 1.0); }
		if s is Shape.Circle(r) {
			let r = r * 10.0;
			println(r);
		}
		println(s);
	`, `circle 1.5 square 2 rect 2 empty
2.5 0 true false
Num(5)
true true false false true true
[true, false] 2
three
not four
2 3
3 0
null fits no pattern
9 true
3
20
Circle(2)
`)
}

func TestPatternTestsEvaluateOnceAndInOrder(t *testing.T) {
	expectOutput(t, `
		enum Shape { Circle(Float), Rect(Float, Float), Empty }
		enum Option<T> { Some(T), None }

		struct Queue { items: [Int]; at: Int; asked: Int; }
		impl Queue {
			fn take() -> Option<Int> {
				self.asked += 1;
				if self.at >= self.items.len() { return Option.None; }
				self.at += 1;
				return Option.Some(self.items[self.at - 1]);
			}
		}

		fn calls(log: [String], name: String, value: Shape) -> Shape {
			log.push(name);
			return value;
		}
	`, `
		// Each value is worked out once, and only when its turn comes
		let log: [String] = [];
		if calls(log, "a", Shape.Empty) is Shape.Circle(r) && calls(log, "b", Shape.Empty) is Shape.Empty {
			println("never");
		} else if calls(log, "c", Shape.Rect(1.0, 2.0)) is Shape.Rect(w, h) && calls(log, "d", Shape.Circle(w + h)) is Shape.Circle(r) && r > 2.0 {
			println("r", r, w, h);
		} else {
			println("never");
		}
		println(log);

		let quiet: [String] = [];
		let no = false;
		if no && calls(quiet, "x", Shape.Empty) is Shape.Empty { println("never"); }
		let value = no || (calls(quiet, "y", Shape.Circle(1.0)) is Shape.Circle(r) && r > 5.0 && calls(quiet, "z", Shape.Empty) is Shape.Empty);
		println(quiet, value);

		// A while asks again on every turn, continue included
		let q = Queue { items: [1, 2, 3, 4, 5], at: 0, asked: 0 };
		let sum = 0;
		while q.take() is Option.Some(n) {
			if n == 2 { continue; }
			if n == 5 { break; }
			sum += n;
		}
		println(sum, q.asked);

		let limited = Queue { items: [10, 20, 30], at: 0, asked: 0 };
		let seen: [Int] = [];
		while seen.len() < 2 && limited.take() is Option.Some(n) && n > 0 { seen.push(n); }
		println(seen, limited.asked);

		// What a name was is kept, whatever the rest of the condition does
		let shape = Shape.Circle(7.0);
		let swap = fn() -> Bool { shape = Shape.Rect(1.0, 1.0); return true; };
		if shape is Shape.Circle(r) && swap() { println(r, shape); }

		// A loop over a variable that the body assigns
		let current = Shape.Rect(3.0, 1.0);
		let steps = 0;
		while current is Shape.Rect(w, h) {
			steps += 1;
			if w <= 1.0 { current = Shape.Circle(h); } else { current = Shape.Rect(w - 1.0, h + 1.0); }
		}
		println(steps, current);
	`, `r 3 1 2
["a", "c", "d"]
["y"] false
8 5
[10, 20] 2
7 Rect(1, 1)
3 Circle(3)
`)
}

// A realistic program: an inventory of orders, with results, optionals,
// closures that keep what a pattern named, and methods of generic enums.
func TestPatternTestsInAProgram(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};
		import std.dict;
		import std.dict.{Dict};

		enum Option<T> { Some(T), None }
		impl<T> Option<T> {
			fn orElse(fallback: T) -> T {
				if self is Option.Some(value) { return value; }
				return fallback;
			}
			fn map<U>(f: fn(T) -> U) -> Option<U> {
				if self is Option.Some(value) { return Option.Some(f(value)); }
				return Option.None;
			}
		}

		enum Item { Book(String, Int), Tool(String, Int, Warranty), Gift }
		enum Warranty { Years(Int), Lifetime, No }
		enum Line { Sale(Item, Int), Refund(Item), Note(String) }

		struct Stock { counts: Dict<Int>; log: [String]; }
		impl Stock {
			fn take(name: String, wanted: Int) -> Result<Int, String> {
				let have = self.counts.get(name);
				if have == null { return Result.Err("no ${name}"); }
				if have < wanted { return Result.Err("only ${have} of ${name}"); }
				self.counts.set(name, have - wanted);
				return Result.Ok(have - wanted);
			}
		}

		fn name(item: Item) -> Option<String> {
			if item is Item.Book(title, _) { return Option.Some(title); }
			if item is Item.Tool(label, _, _) { return Option.Some(label); }
			return Option.None;
		}

		fn price(item: Item) -> Int {
			if item is Item.Book(_, cents) {
				return cents;
			} else if item is Item.Tool(_, cents, Warranty.Years(years)) && years > 1 {
				return cents + 500 * (years - 1);
			} else if item is Item.Tool(_, cents, Warranty.Lifetime) {
				return cents * 2;
			} else if item is Item.Tool(_, cents, _) {
				return cents;
			}
			return 0;
		}

		fn first<T>(items: [T], wanted: fn(T) -> Bool) -> Option<T> {
			for item in items {
				if wanted(item) { return Option.Some(item); }
			}
			return Option.None;
		}

		fn settle(stock: Stock, lines: [Line]) -> (Int, [fn() -> String]) {
			let total = 0;
			let receipts: [fn() -> String] = [];
			let at = 0;
			while at < lines.len() && !(lines[at] is Line.Note("stop")) {
				let line = lines[at];
				at += 1;
				if line is Line.Sale(item, quantity) && name(item) is Option.Some(label) {
					if stock.take(label, quantity) is Result.Err(reason) {
						stock.log.push(reason);
						continue;
					}
					let cost = price(item) * quantity;
					total += cost;
					receipts.push(() -> "${quantity} x ${label}: ${cost}");
				} else if line is Line.Sale(Item.Gift, quantity) {
					receipts.push(() -> "${quantity} gifts");
				} else if line is Line.Refund(item) {
					total -= price(item);
					let what = name(item).orElse("a gift");
					receipts.push(() -> "refund of ${what}");
				} else if line is Line.Note(text) {
					stock.log.push("note: " + text);
				}
			}
			return (total, receipts);
		}
	`, `
		let counts = dict.empty<Int>();
		counts.set("Dune", 3);
		counts.set("saw", 1);
		counts.set("drill", 10);
		let stock = Stock { counts: counts, log: [] };

		let saw = Item.Tool("saw", 2000, Warranty.Years(3));
		let drill = Item.Tool("drill", 9000, Warranty.Lifetime);
		let lines = [
			Line.Sale(Item.Book("Dune", 900), 2),
			Line.Sale(saw, 1),
			Line.Sale(saw, 1),
			Line.Note("half way"),
			Line.Sale(Item.Gift, 4),
			Line.Sale(Item.Book("Emma", 700), 1),
			Line.Sale(drill, 2),
			Line.Refund(Item.Book("Dune", 900)),
			Line.Refund(Item.Gift),
			Line.Sale(Item.Tool("drill", 9000, Warranty.No), 1),
			Line.Note("stop"),
			Line.Sale(Item.Book("Dune", 900), 1),
		];

		let (total, receipts) = settle(stock, lines);
		println(total);
		for receipt in receipts { println(receipt()); }
		println(stock.log);
		println(stock.counts.get("Dune"), stock.counts.get("saw"), stock.counts.get("drill"));

		let costly = first(lines, line -> line is Line.Sale(item, _) && price(item) > 5000);
		if costly is Option.Some(Line.Sale(item, quantity)) { println(name(item).orElse("?"), quantity); }
		let none = first(lines, line -> line is Line.Refund(Item.Tool(_, _, _)));
		println(none is Option.None, name(Item.Gift).map(text -> text.len()).orElse(-1), name(saw).map(text -> text.len()).orElse(-1));
	`, `48900
2 x Dune: 1800
1 x saw: 3000
4 gifts
2 x drill: 36000
refund of Dune
refund of a gift
1 x drill: 9000
["only 0 of saw", "note: half way", "no Emma"]
1 0 7
drill 2
true -1 3
`)
}

func TestPatternTestsAcrossModules(t *testing.T) {
	fs := sources{
		"main.qi": `
			import geo;
			import geo.{Shape, Point};
			import geo.parse;
			import geo.parse.{Token, Outcome};

			struct Label { text: String; }

			fn area(shape: Shape) -> Float {
				if shape is Shape.Circle(r) { return 3.0 * r * r; }
				if shape is geo.Shape.Rect(w, h) { return w * h; }
				return 0.0;
			}

			// A name with a dot after is: a variant, or a type of a module
			fn kind(value: Any) -> String {
				if value is geo.Shape.At(place) { return "at ${place.x}"; }
				if value is geo.Shape { return "shape"; }
				if value is geo.Point { return "point"; }
				if value is parse.Token.Word(text) { return "word " + text; }
				if value is parse.Token { return "token"; }
				if value is (Point, Label) { return "pair"; }
				return "?";
			}

			fn main() -> Int {
				println(area(Shape.Circle(1.0)), area(geo.unit()), area(Shape.Empty));
				println(kind(Shape.At(Point { x: 4, y: 5 })), kind(Shape.Empty), kind(Point { x: 1, y: 1 }), kind(Token.Word("hi")), kind(Token.End), kind((Point { x: 1, y: 1 }, Label { text: "a" })), kind(1));

				let tokens = parse.scan("move 3 up 4 stop 9");
				let at = 0;
				let moved = 0;
				while parse.next(tokens, at) is Outcome.Found(token, after) {
					at = after;
					if token is Token.Number(n) { moved += n; } else if token is Token.Word("stop") { break; }
				}
				println(moved, at, parse.next(tokens, 99) is Outcome.Done);
				return 0;
			}
		`,
		"geo.qi": `
			pub struct Point { x: Int; y: Int; }
			pub enum Shape { Circle(Float), Rect(Float, Float), At(Point), Empty }

			pub fn unit() -> Shape { return Shape.Rect(1.0, 1.0); }
		`,
		"geo/parse.qi": `
			import std.arrays;
			import std.strings;

			pub enum Token { Word(String), Number(Int), End }
			pub enum Outcome { Found(Token, Int), Done }

			pub fn scan(text: String) -> [Token] {
				let tokens: [Token] = [];
				for word in text.words() {
					let n = word.parseInt();
					if n != null { tokens.push(Token.Number(n)); } else { tokens.push(Token.Word(word)); }
				}
				tokens.push(Token.End);
				return tokens;
			}

			pub fn next(tokens: [Token], at: Int) -> Outcome {
				if at >= tokens.len() || tokens[at] is Token.End { return Outcome.Done; }
				return Outcome.Found(tokens[at], at + 1);
			}
		`,
	}
	expectModulesOutput(t, fs, `3 1 0
at 4 shape point word hi token pair ?
7 5 true
`)
}

// A pattern of a match that names a value as the matched variable is
// named used to read its other values from the new variable.
func TestMatchPatternMayReuseTheNameOfItsValue(t *testing.T) {
	expectOutput(t, `
		enum Tree { Node(Tree, Int), Leaf }
	`, `
		let pair = (1, 2);
		match pair {
			case (pair, rest): { println(pair, rest); }
		}
		let tree = Tree.Node(Tree.Node(Tree.Leaf, 1), 2);
		let depth = 0;
		while tree is Tree.Node(tree, value) {
			depth += value;
			println(tree);
			break;
		}
		match tree {
			case Tree.Node(tree, value): { println(tree, value); }
			case Tree.Leaf: { println("leaf"); }
		}
		println(depth);
	`, `1 2
Node(Leaf, 1)
Node(Leaf, 1) 2
2
`)
}

func TestPatternTestErrors(t *testing.T) {
	decls := "enum Shape { Circle(Float), Rect(Float, Float), Empty }\nenum One { Only(Int) }\n"
	tests := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{"variant alone that no enum has", "fn main() -> Int { let s = Shape.Empty; if s is Circel(r) { return 1; } return 0; }", "E1001", "undefined identifier 'Circel'"},
		{"a value after is", "fn main() -> Int { let n = 3; if n is 3 { return 1; } return 0; }", "E0001", "'is' takes a type or the pattern of a variant, found '3'"},
		{"name in the else", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Circle(r) { return 1; } else { println(r); } return 0; }", "E1001", "undefined identifier 'r'"},
		{"name after the if", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Circle(r) { } println(r); return 0; }", "E1001", "undefined identifier 'r'"},
		{"name under a not, in a block that may not leave", "fn main() -> Int { let s = Shape.Empty; if !(s is Shape.Circle(r)) { println(1); } return 0; }", "E1003", "'r' cannot be named here: nothing runs only when this test holds"},
		{"name on a side of an or", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Empty || s is Shape.Rect(w, _) { return 1; } return 0; }", "E1003", "'w' cannot be named here"},
		{"name in a value", "fn main() -> Int { let s = Shape.Empty; let round = s is Shape.Circle(r); return 0; }", "E1003", "'r' cannot be named here"},
		{"name twice", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Rect(a, a) { return 1; } return 0; }", "E1002", "'a' is already declared"},
		{"unknown variant", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Square(r) { return 1; } return 0; }", "E2029", "enum Shape has no variant 'Square'"},
		{"wrong number of values", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Rect(w) { return 1; } return 0; }", "E2029", "holds 2 values, the pattern has 1"},
		{"value that is never the variant", "fn main() -> Int { let n = 3; if n is Shape.Empty { return 1; } return 0; }", "E2029", "a value of type Int is never a Shape.Empty"},
		{"tuple of the wrong size", "fn main() -> Int { let p = (1, 2); if p is (a, b, c) { return 1; } return 0; }", "E2029", "is never a"},
		{"name of the wrong type", "fn main() -> Int { let s = Shape.Empty; if s is Shape.Circle(r) && r == \"one\" { return 1; } return 0; }", "E2006", "operator == not defined"},
		{"unknown enum", "fn main() -> Int { let s = Shape.Empty; if s is Shapes.Circle(r) { return 1; } return 0; }", "E1001", "undefined identifier 'Shapes'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			result := Compile(decls+tt.src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			if result.Chunk != nil {
				t.Error("expected no bytecode for a rejected program")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
			if d.Context == "" {
				t.Error("expected the diagnostic to carry the source line")
			}
		})
	}

	// A test that always holds compiles and runs, with one warning
	errs := errors.NewHandler()
	result := Compile(decls+"fn main() -> Int { let o = One.Only(4); if o is One.Only(n) { return n; } return 0; }", "test.qi", errs)
	if errs.HasErrors() || result.Chunk == nil {
		t.Fatalf("expected the program to compile, got %v", errs.Diagnostics)
	}
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E2023" ||
		!strings.Contains(errs.Diagnostics[0].Message, "this test always holds: every value of type One fits the pattern") {
		t.Errorf("expected one warning that the test always holds, got %v", errs.Diagnostics)
	}
}
