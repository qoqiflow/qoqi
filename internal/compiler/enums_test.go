package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestEnumValues(t *testing.T) {
	expectOutput(t, `
		enum Shape { Circle(Float), Rect(Float, Float), Empty }
		enum Color { Red, Green, Blue }

		fn area(s: Shape) -> Float {
			match s {
				case Shape.Circle(r): { return 3.0 * r * r; }
				case Shape.Rect(w, h): { return w * h; }
				case Shape.Empty: { return 0.0; }
			}
		}
		fn next(c: Color) -> Color {
			match c {
				case Color.Red: { return Color.Green; }
				case Color.Green: { return Color.Blue; }
				case Color.Blue: { return Color.Red; }
			}
		}
	`, `
		let shapes = [Shape.Circle(1.0), Shape.Rect(2.0, 3.5), Shape.Empty];
		for s in shapes { println(s, area(s)); }

		// Printed as they are made; in arrays and inside each other too
		println(shapes, Color.Red, [Color.Green, next(Color.Green)]);

		// Equal when the same variant holds equal values
		println(Shape.Empty == Shape.Empty, Shape.Circle(1.0) == Shape.Circle(1.0), Shape.Circle(1.0) == Shape.Circle(2.0));
		println(Shape.Rect(1.0, 2.0) != Shape.Rect(1.0, 2.0), Shape.Empty != Shape.Circle(0.0), next(next(next(Color.Red))) == Color.Red);

		// A variable of an enum type can hold any variant, one after the other
		let current = Shape.Empty;
		current = Shape.Circle(2.0);
		println(current, area(current));
	`, `Circle(1) 3
Rect(2, 3.5) 7
Empty 0
[Circle(1), Rect(2, 3.5), Empty] Red [Green, Blue]
true true false
false true true
Circle(2) 12
`)
}

func TestEnumPatterns(t *testing.T) {
	expectOutput(t, `
		enum Expr { Num(Int), Add(Expr, Expr), Mul(Expr, Expr), Neg(Expr), Var(String) }

		fn show(e: Expr) -> String {
			match e {
				case Expr.Num(n): { return toString(n); }
				case Expr.Var(name): { return name; }
				case Expr.Neg(inner): { return "-" + show(inner); }
				case Expr.Add(l, r): { return "(" + show(l) + " + " + show(r) + ")"; }
				case Expr.Mul(l, r): { return "(" + show(l) + " * " + show(r) + ")"; }
			}
		}

		// Patterns inside patterns, and literals: the rules of arithmetic
		// as cases, the most specific first
		fn simplify(e: Expr) -> Expr {
			match e {
				case Expr.Add(Expr.Num(0), right): { return simplify(right); }
				case Expr.Add(left, Expr.Num(0)): { return simplify(left); }
				case Expr.Mul(Expr.Num(0), _): { return Expr.Num(0); }
				case Expr.Mul(_, Expr.Num(0)): { return Expr.Num(0); }
				case Expr.Mul(Expr.Num(1), right): { return simplify(right); }
				case Expr.Mul(left, Expr.Num(1)): { return simplify(left); }
				case Expr.Neg(Expr.Neg(inner)): { return simplify(inner); }
				case Expr.Neg(Expr.Num(n)): { return Expr.Num(-n); }
				case Expr.Add(Expr.Num(a), Expr.Num(b)): { return Expr.Num(a + b); }
				case Expr.Mul(Expr.Num(a), Expr.Num(b)): { return Expr.Num(a * b); }
				case Expr.Add(l, r): { return Expr.Add(simplify(l), simplify(r)); }
				case Expr.Mul(l, r): { return Expr.Mul(simplify(l), simplify(r)); }
				case Expr.Neg(inner): { return Expr.Neg(simplify(inner)); }
				default: { return e; }
			}
		}

		// Simplifying may open the way for another rule
		fn reduce(e: Expr) -> Expr {
			let current = e;
			while true {
				let next = simplify(current);
				if next == current { return current; }
				current = next;
			}
		}

		fn eval(e: Expr, x: Int) -> Int {
			match e {
				case Expr.Num(n): { return n; }
				case Expr.Var(_): { return x; }
				case Expr.Neg(inner): { return -eval(inner, x); }
				case Expr.Add(l, r): { return eval(l, x) + eval(r, x); }
				case Expr.Mul(l, r): { return eval(l, x) * eval(r, x); }
			}
		}
	`, `
		let x = Expr.Var("x");
		let tests = [
			Expr.Add(Expr.Num(0), x),
			Expr.Mul(Expr.Add(x, Expr.Num(0)), Expr.Num(1)),
			Expr.Mul(Expr.Num(0), Expr.Add(x, Expr.Num(5))),
			Expr.Neg(Expr.Neg(Expr.Neg(Expr.Num(4)))),
			Expr.Add(Expr.Mul(Expr.Num(2), Expr.Num(3)), Expr.Mul(x, Expr.Add(Expr.Num(1), Expr.Num(0)))),
			Expr.Add(Expr.Add(Expr.Num(1), Expr.Num(2)), Expr.Add(Expr.Num(3), Expr.Num(4))),
		];
		for e in tests {
			let reduced = reduce(e);
			println(show(e), "=>", show(reduced), "|", eval(e, 7), eval(reduced, 7));
		}
	`, `(0 + x) => x | 7 7
((x + 0) * 1) => x | 7 7
(0 * (x + 5)) => 0 | 0 0
---4 => -4 | -4 -4
((2 * 3) + (x * (1 + 0))) => (6 + x) | 13 13
((1 + 2) + (3 + 4)) => 10 | 10 10
`)
}

func TestGenericEnums(t *testing.T) {
	expectOutput(t, `
		enum Option<T> { Some(T), None }
		enum Result<T, E> { Ok(T), Err(E) }
		enum List<T> { Cons(T, List<T>), Nil }

		impl<T> Option<T> {
			fn map<U>(f: fn(T) -> U) -> Option<U> {
				match self {
					case Option.Some(v): { return Option.Some(f(v)); }
					case Option.None: { return Option.None; }
				}
			}
			fn then<U>(f: fn(T) -> Option<U>) -> Option<U> {
				match self {
					case Option.Some(v): { return f(v); }
					case Option.None: { return Option.None; }
				}
			}
			fn orElse(fallback: T) -> T {
				match self {
					case Option.Some(v): { return v; }
					case Option.None: { return fallback; }
				}
			}
			fn isSome() -> Bool { return self != Option.None; }
		}

		impl<T, E> Result<T, E> {
			fn ok() -> Option<T> {
				match self {
					case Result.Ok(v): { return Option.Some(v); }
					case Result.Err(_): { return Option.None; }
				}
			}
			fn mapErr<F>(f: fn(E) -> F) -> Result<T, F> {
				match self {
					case Result.Ok(v): { return Result.Ok(v); }
					case Result.Err(e): { return Result.Err(f(e)); }
				}
			}
		}

		fn divide(a: Int, b: Int) -> Result<Int, String> {
			if b == 0 { return Result.Err("division by zero"); }
			return Result.Ok(a / b);
		}
		fn half(n: Int) -> Option<Int> {
			if n % 2 != 0 { return Option.None; }
			return Option.Some(n / 2);
		}

		fn fromArray<T>(items: [T]) -> List<T> {
			let list: List<T> = List.Nil;
			let i = items.len() - 1;
			while i >= 0 { list = List.Cons(items[i], list); i--; }
			return list;
		}
		fn length<T>(list: List<T>) -> Int {
			match list {
				case List.Cons(_, rest): { return 1 + length<T>(rest); }
				case List.Nil: { return 0; }
			}
		}
		fn mapList<T, U>(list: List<T>, f: fn(T) -> U) -> List<U> {
			match list {
				case List.Cons(head, rest): { return List.Cons(f(head), mapList<T, U>(rest, f)); }
				case List.Nil: { return List.Nil; }
			}
		}
		fn second<T>(list: List<T>) -> Option<T> {
			match list {
				case List.Cons(_, List.Cons(value, _)): { return Option.Some(value); }
				default: { return Option.None; }
			}
		}
	`, `
		// The type arguments come from the values, the expected type, or are written
		let a = Option.Some(20);
		let b: Option<String> = Option.None;
		let c = Option<Int>.None;
		println(a, b, c, a.isSome(), c.isSome());

		// Methods with a type parameter of their own, chained
		println(a.map(fn(n: Int) -> Int { return n * 2; }).orElse(0), c.map(fn(n: Int) -> Int { return n * 2; }).orElse(-1));
		println(a.then(half).then(half).map(toString).orElse("odd"), Option.Some(6).then(half).then(half).map(toString).orElse("odd"));

		// Two type parameters, one of them changed by a method
		for pair in [[9, 3], [1, 0]] {
			let r = divide(pair[0], pair[1]);
			match r {
				case Result.Ok(v): { println("ok", v, r.ok()); }
				case Result.Err(message): { println("err", message, r.ok(), r.mapErr(fn(m: String) -> Int { return m.len(); })); }
			}
		}

		// An enum that holds itself, behind generic functions
		let numbers = fromArray([1, 2, 3]);
		let words = mapList(numbers, fn(n: Int) -> String { return "<" + toString(n) + ">"; });
		println(numbers, length(numbers), length(fromArray<String>([])));
		println(words, second(numbers), second(words), second(fromArray([7])));

		// Enums inside enums inside arrays
		let nested: [Option<Result<Int, String>>] = [Option.Some(Result.Ok(1)), Option.None, Option.Some(Result.Err("no"))];
		for item in nested {
			match item {
				case Option.Some(Result.Ok(n)): { print("ok", n, ""); }
				case Option.Some(Result.Err(e)): { print("err", e, ""); }
				case Option.None: { print("none", ""); }
			}
		}
		println();
		println(Option.Some(Option.Some(1)) == Option.Some(Option.Some(1)), Option.Some(Option<Int>.None) == Option.Some(Option.Some(1)));
	`, `Some(20) None None true false
40 -1
5 odd
ok 3 Some(3)
err division by zero None Err(16)
Cons(1, Cons(2, Cons(3, Nil))) 3 0
Cons("<1>", Cons("<2>", Cons("<3>", Nil))) Some(2) Some("<2>") None
ok 1 none err no 
true false
`)
}

func TestEnumsWithTraitsStructsAndClosures(t *testing.T) {
	expectOutput(t, `
		trait Describe { fn describe() -> String; }

		enum Payment { Cash(Int), Card(String, Int), Free }
		struct Order { id: Int; payment: Payment; history: [Status]; }
		enum Status { Placed, Shipped(String), Delivered, Cancelled(String) }

		impl Describe for Payment {
			fn describe() -> String {
				match self {
					case Payment.Cash(amount): { return "cash " + toString(amount); }
					case Payment.Card(holder, amount): { return holder + " pays " + toString(amount); }
					case Payment.Free: { return "free"; }
				}
			}
		}
		impl Describe for Status {
			fn describe() -> String {
				match self {
					case Status.Placed: { return "placed"; }
					case Status.Shipped(carrier): { return "shipped by " + carrier; }
					case Status.Delivered: { return "delivered"; }
					case Status.Cancelled(reason): { return "cancelled: " + reason; }
				}
			}
		}
		impl Describe for Order {
			fn describe() -> String { return "order " + toString(self.id) + ", " + self.payment.describe(); }
		}

		impl Payment {
			fn amount() -> Int {
				match self {
					case Payment.Cash(n): { return n; }
					case Payment.Card(_, n): { return n; }
					case Payment.Free: { return 0; }
				}
			}
		}

		impl Order {
			// The status an order may move to from the one it is in
			fn advance(to: Status) -> Bool {
				let current = self.history[self.history.len() - 1];
				match current {
					case Status.Delivered: { return false; }
					case Status.Cancelled(_): { return false; }
					default: { }
				}
				match to {
					case Status.Placed: { return false; }
					case Status.Delivered: {
						match current {
							case Status.Shipped(_): { }
							default: { return false; }
						}
					}
					default: { }
				}
				self.history.push(to);
				return true;
			}
		}

		// Any array of things that describe themselves: an array of
		// Describe, or of one type that implements it
		fn all<T: Describe>(items: [T]) -> [String] {
			let out: [String] = [];
			for item in items { out.push(item.describe()); }
			return out;
		}
	`, `
		let order = Order { id: 7, payment: Payment.Card("ada", 120), history: [Status.Placed] };
		println(order.advance(Status.Delivered), order.advance(Status.Placed), order.advance(Status.Shipped("post")));
		println(order.advance(Status.Delivered), order.advance(Status.Cancelled("late")), order.history);

		// Enums, a struct and their trait in one array
		let things: [Describe] = [order, order.payment, Payment.Free, Status.Cancelled("no stock")];
		println(all(things));
		println(all(order.history));

		// A trait value that is an enum can be tested and cast back
		let d: Describe = Payment.Cash(5);
		println(d is Payment, d is Status, d is Order, (d as Payment).amount());
		match d {
			case Payment.Cash(n): { println("cash of", n); }
			case Payment: { println("another payment"); }
			default: { println("not a payment"); }
		}

		// Closures keep the values a pattern named, one set per iteration
		let later: [fn() -> String] = [];
		for payment in [Payment.Cash(5), Payment.Card("bob", 9), Payment.Free] {
			match payment {
				case Payment.Cash(n): { later.push(fn() -> String { return "c" + toString(n); }); }
				case Payment.Card(who, n): { later.push(fn() -> String { return who + toString(n * 2); }); }
				case Payment.Free: { later.push(fn() -> String { return "-"; }); }
			}
		}
		for f in later { print(f(), ""); }
		println();

		// Totals by folding over variants
		let total = 0;
		for payment in [Payment.Cash(5), Payment.Card("bob", 9), Payment.Free] { total += payment.amount(); }
		println(total);
	`, `false false true
true false [Placed, Shipped("post"), Delivered]
["order 7, ada pays 120", "ada pays 120", "free", "cancelled: no stock"]
["placed", "shipped by post", "delivered"]
true false false 5
cash of 5
c5 bob18 - 
14
`)
}

func TestMatchOnEnumsInControlFlow(t *testing.T) {
	expectOutput(t, `
		enum Token { Num(Int), Op(String), Open, Close, End }
		enum Step { Go(Int), Skip, Stop }

		fn tokens() -> [Token] {
			return [Token.Num(2), Token.Op("+"), Token.Open, Token.Num(3), Token.Op("*"), Token.Num(4), Token.Close, Token.End, Token.Num(99)];
		}
	`, `
		// break and continue from inside a case go to the loop around the match
		let numbers = 0;
		let depth = 0;
		let ops = "";
		for token in tokens() {
			match token {
				case Token.End: { break; }
				case Token.Open: { depth++; continue; }
				case Token.Close: { depth--; continue; }
				case Token.Num(n): { numbers += n; }
				case Token.Op(text): { ops += text; }
			}
			depth += 0;
		}
		println(numbers, ops, depth);

		// A match that decides how a loop goes on
		let steps = [Step.Go(1), Step.Skip, Step.Go(5), Step.Stop, Step.Go(100)];
		let position = 0;
		let i = 0;
		while i < steps.len() {
			let step = steps[i];
			i++;
			match step {
				case Step.Go(by): { position += by; }
				case Step.Skip: { }
				case Step.Stop: { i = steps.len(); }
			}
		}
		println(position, i);

		// A match inside a match inside a lambda, with names reused
		let classify = fn(a: Token, b: Token) -> String {
			match a {
				case Token.Num(n): {
					match b {
						case Token.Num(n): { return "two numbers, second " + toString(n); }
						case Token.Op(n): { return "number then " + n; }
						default: { return "number " + toString(n); }
					}
				}
				case Token.Op(_): { return "operator first"; }
				default: { return "other"; }
			}
		};
		println(classify(Token.Num(1), Token.Num(2)), "|", classify(Token.Num(1), Token.Op("-")), "|", classify(Token.Num(1), Token.End), "|", classify(Token.Op("x"), Token.End), "|", classify(Token.End, Token.End));

		// The matched value is worked out once
		let calls = 0;
		let pick = fn() -> Step { calls++; return Step.Go(calls); };
		match pick() {
			case Step.Stop: { println("stop"); }
			case Step.Skip: { println("skip"); }
			case Step.Go(n): { println("go", n, calls); }
		}

		// A name of a pattern can be assigned without touching the value matched
		let step = Step.Go(1);
		match step {
			case Step.Go(n): { n = n + 10; println(n, step); }
			default: { }
		}
	`, `9 +* 0
6 5
two numbers, second 2 | number then - | number 1 | operator first | other
go 1 1
11 Go(1)
`)
}

func TestConstants(t *testing.T) {
	expectOutput(t, `
		const WIDTH = 4;
		const HEIGHT = WIDTH * 3 - 2;
		const AREA = WIDTH * HEIGHT;
		const HALF: Float = 0.5;
		const TITLE = "grid " + "of cells";
		const WIDE = WIDTH > 3;
		const NEGATIVE = -AREA;
		const LAST = AFTER - 1;      // used before it is declared
		const AFTER = 100;

		struct Grid { cells: [Int]; }
		fn newGrid() -> Grid {
			let cells: [Int] = [];
			let i = 0;
			while i < AREA { cells.push(i % WIDTH); i++; }
			return Grid { cells: cells };
		}
		impl Grid {
			fn row(y: Int) -> [Int] {
				let out: [Int] = [];
				let x = 0;
				while x < WIDTH { out.push(self.cells[y * WIDTH + x]); x++; }
				return out;
			}
		}
		fn scaled<T>(value: T) -> Int { return WIDTH * HEIGHT; }
	`, `
		println(WIDTH, HEIGHT, AREA, HALF, TITLE, WIDE, NEGATIVE, LAST);
		let grid = newGrid();
		println(grid.cells.len(), grid.row(0), grid.row(HEIGHT - 1), scaled("x"), scaled(1.5));

		// In closures, conditions, patterns' bodies and array literals
		let add = fn(n: Int) -> Int { return n + WIDTH; };
		let sizes = [WIDTH, HEIGHT, AREA];
		if WIDE && sizes[0] == WIDTH { println(add(1), sizes); }
		match AREA { case 40: { println("forty", AREA / WIDTH); } default: { println("other"); } }

		// A local of the same name hides the constant from there on
		let before = WIDTH;
		let WIDTH = "narrow";
		println(before, WIDTH, HEIGHT);
	`, `4 10 40 0.5 grid of cells true -40 99
40 [0, 1, 2, 3] [0, 1, 2, 3] 40 40
5 [4, 10, 40]
forty 10
4 narrow 10
`)

	// A value that cannot be worked out fails where the constant is used
	_, _, err := runProgram(t, "const ZERO = 0;\nconst BAD = 10 / ZERO;\nfn main() -> Int {\n    println(\"before\");\n    return BAD;\n}\n")
	if err == nil || !strings.Contains(err.Error(), "division by zero") || !strings.Contains(err.Error(), "test.qi:5:") {
		t.Errorf("expected a division by zero on line 5, got %v", err)
	}
	// ...and a constant that is never used costs nothing, not even its mistake
	out, _, err := runProgram(t, "const ZERO = 0;\nconst BAD = 10 / ZERO;\nfn main() -> Int { println(\"fine\"); return 0; }\n")
	if err != nil || out != "fine\n" {
		t.Errorf("expected an unused constant not to be worked out, got %q, %v", out, err)
	}
}

func TestConstantsAreNotInTheBytecode(t *testing.T) {
	errs := errors.NewHandler()
	result := Compile(`
		const A = 1; const B = A + 1; const C = B * 2; const NAME = "n";
		fn main() -> Int { return C; }
	`, "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}
	if len(result.Chunk.Prototypes) != 1 || result.Chunk.Prototypes[0].Name != "main" {
		t.Errorf("expected main to be the only function, got %v", result.Chunk.Prototypes)
	}
	// The unused text was never put in the program
	for _, s := range result.Chunk.StringPool {
		if s == "n" {
			t.Error("did not expect the value of an unused constant in the string pool")
		}
	}
}

func TestEnumAndConstantErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{"missing case", "enum E { A, B }\nfn main() -> Int { match E.A { case E.A: { return 1; } } }", "E2028", "match on E does not handle E.B"},
		{"unknown variant", "enum E { A, B }\nfn main() -> Int { let e = E.C; return 0; }", "E2030", "enum E has no variant 'C'"},
		{"wrong pattern", "enum E { A(Int), B }\nfn main() -> Int { match E.B { case E.A(x, y): { return 1; } default: { return 0; } } }", "E2029", "holds 1 value, the pattern has 2"},
		{"constant in a circle", "const A = B;\nconst B = A;\nfn main() -> Int { return A; }", "E2031", "is defined in terms of itself"},
		{"constant from a call", "fn f() -> Int { return 1; }\nconst A = f();\nfn main() -> Int { return A; }", "E2032", "has to be known when the program is compiled"},
		{"constant assigned", "const A = 1;\nfn main() -> Int { A = 2; return A; }", "E2022", "cannot assign to constant 'A'"},
		{"a statement outside a function, beside main", "let total = 0;\ntotal += 1;\nfn main() -> Int { return total; }", "E8007", "cannot run in a file that has a main"},
		{"constant declared twice", "const A = 1;\nconst A = 2;\nfn main() -> Int { return A; }", "E1002", "'A' is already declared"},
		{"constant and function of one name", "const size = 1;\nfn size() -> Int { return 1; }\nfn main() -> Int { return 0; }", "E1002", "'size' is already declared"},
		{"enum and struct of one name", "enum T { A }\nstruct T { x: Int; }\nfn main() -> Int { return 0; }", "E1002", "'T' is already declared"},
		{"name of a pattern twice", "enum E { A(Int, Int) }\nfn main() -> Int { match E.A(1, 2) { case E.A(x, x): { return x; } } }", "E1002", "'x' is already declared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			result := Compile(tt.src, "test.qi", errs)
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
}

func TestEnumsAndConstantsAcrossModules(t *testing.T) {
	fs := sources{
		"main.qi": `
			import bank.account;
			import bank.account.{Account, Event, FEE};
			import bank.ledger;
			import bank.ledger.{Outcome};

			const DOUBLE_FEE = FEE * 2;

			// The program's own enum, holding a module's
			enum Report { Line(String, Event), Total(Int) }

			fn render(r: Report) -> String {
				match r {
					case Report.Line(who, Event.Deposit(n)): { return who + " +" + toString(n); }
					case Report.Line(who, Event.Withdraw(n)): { return who + " -" + toString(n); }
					case Report.Line(who, account.Event.Closed): { return who + " closed"; }
					case Report.Total(n): { return "total " + toString(n); }
				}
			}

			fn main() -> Int {
				println(FEE, account.FEE, DOUBLE_FEE, ledger.LIMIT, account.MINIMUM);
				let a = account.open("ada");
				let events = [Event.Deposit(100), Event.Withdraw(30), account.Event.Withdraw(500), Event.Closed, Event.Deposit(1)];
				let reports: [Report] = [];
				for e in events {
					match ledger.apply(a, e) {
						case Outcome.Done(balance): { reports.push(Report.Line(a.name, e)); }
						case Outcome.Refused(why): { println("refused:", why, e); }
					}
				}
				reports.push(Report.Total(a.balance));
				for r in reports { println(render(r)); }
				println(a.log, a.isOpen(), ledger.count(a, Event.Withdraw(30)), ledger.count(a, Event.Withdraw(31)));
				return 0;
			}
		`,
		"bank/account.qi": `
			pub const FEE = 2;
			pub const MINIMUM = FEE * 5;

			pub enum Event { Deposit(Int), Withdraw(Int), Closed }

			pub struct Account { name: String; balance: Int; log: [Event]; }

			pub fn open(name: String) -> Account {
				return Account { name: name, balance: 0, log: [] };
			}

			impl Account {
				pub fn isOpen() -> Bool {
					for event in self.log {
						if event == Event.Closed { return false; }
					}
					return true;
				}
			}
		`,
		"bank/ledger.qi": `
			import bank.account.{Account, Event, FEE};

			pub const LIMIT = 1000 - FEE;

			pub enum Outcome { Done(Int), Refused(String) }

			pub fn apply(a: Account, e: Event) -> Outcome {
				if !a.isOpen() { return Outcome.Refused("account is closed"); }
				match e {
					case Event.Deposit(n): { a.balance += n; }
					case Event.Withdraw(n): {
						if n + FEE > a.balance { return Outcome.Refused("not enough money"); }
						a.balance -= n + FEE;
					}
					case Event.Closed: { }
				}
				a.log.push(e);
				return Outcome.Done(a.balance);
			}

			pub fn count(a: Account, wanted: Event) -> Int {
				let n = 0;
				for e in a.log {
					if e == wanted { n++; }
				}
				return n;
			}
		`,
	}
	expectModulesOutput(t, fs, `2 2 4 998 10
refused: not enough money Withdraw(500)
refused: account is closed Deposit(1)
ada +100
ada -30
ada closed
total 68
[Deposit(100), Withdraw(30), Closed] false 1 0
`)

	// What a module keeps to itself
	private := sources{
		"main.qi": "import lib;\nfn main() -> Int { let s = lib.Secret.A; return lib.HIDDEN; }",
		"lib.qi":  "enum Secret { A }\nconst HIDDEN = 1;\npub fn f() -> Int { return HIDDEN; }",
	}
	expectModulesError(t, private, "E8003", "main.qi", "enum 'Secret' of module 'lib' is private")

	// A match in one module on an enum of another must still be complete
	expectModulesError(t, sources{
		"main.qi": "import lib.{Light};\nfn main() -> Int {\n    match Light.Red {\n        case Light.Red: { return 1; }\n    }\n}",
		"lib.qi":  "pub enum Light { Red, Amber, Green }",
	}, "E2028", "main.qi", "match on lib.Light does not handle lib.Light.Amber, lib.Light.Green")

	// A constant in a circle through two modules
	expectModulesError(t, sources{
		"main.qi": "import a;\nfn main() -> Int { return a.X; }",
		"a.qi":    "import b;\npub const X = b.Y + 1;",
		"b.qi":    "import a;\npub const Y = a.X + 1;",
	}, "E2031", "a.qi", "is defined in terms of itself")
}

func TestLambdasCaptureThroughEnumValues(t *testing.T) {
	// A variable of the enclosing function used inside the value of an
	// enum, inside a lambda: it has to be captured like any other use
	expectOutput(t, `
		enum Result<T, E> { Ok(T), Err(E) }
		enum Wrap { One(Int), Pair(Int, String), Deep(Wrap) }
		struct Note { key: String; n: Int; }

		fn check(key: String, limit: Int) -> fn(Int) -> Result<Int, Note> {
			return fn(n: Int) -> Result<Int, Note> {
				if n > limit { return Result.Err(Note { key: key, n: n - limit }); }
				return Result.Ok(n + limit);
			};
		}
		fn wrappers(label: String, base: Int) -> [fn() -> Wrap] {
			let step = 1;
			return [
				fn() -> Wrap { return Wrap.One(base); },
				fn() -> Wrap { step++; return Wrap.Pair(base + step, label); },
				fn() -> Wrap { return Wrap.Deep(Wrap.Deep(Wrap.Pair(step, label + "!"))); },
			];
		}
		fn matcher(wanted: Int) -> fn(Wrap) -> Bool {
			return fn(w: Wrap) -> Bool {
				match w {
					case Wrap.One(n): { return n == wanted; }
					case Wrap.Pair(n, _): { return n == wanted; }
					case Wrap.Deep(inner): { return false; }
				}
			};
		}
	`, `
		let c = check("volume", 10);
		println(c(3), c(25));
		let made = wrappers("x", 5);
		println(made[0](), made[1](), made[1](), made[2]());
		let isSeven = matcher(7);
		println(isSeven(Wrap.One(7)), isSeven(Wrap.Pair(7, "a")), isSeven(Wrap.One(8)), isSeven(Wrap.Deep(Wrap.One(7))));
	`, `Ok(13) Err({key: "volume", n: 15})
One(5) Pair(7, "x") Pair(8, "x") Deep(Deep(Pair(3, "x!")))
true true false false
`)
}

// The values of an enum are never changed once made, so an enum value
// holds whatever its type arguments allow: a Result<Lock, E> is a
// Result<Lock?, E>, and Result.Ok(lock) can be returned as one.
func TestEnumsAreWiderWithWiderTypeArguments(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};

		struct Lock { hash: String; }
		enum Shape<T> { Box(T), Pair(T, T), Empty }

		fn read(present: Bool) -> Result<Lock?, String> {
			if !present {
				return Result.Ok(null);
			}
			return Result.Ok(Lock { hash: "abc" });
		}

		fn total(shapes: [Shape<Int?>]) -> Int {
			let sum = 0;
			for shape in shapes {
				match shape {
					case Shape.Box(n): { sum += n ?? 100; }
					case Shape.Pair(a, b): { sum += (a ?? 0) + (b ?? 0); }
					case Shape.Empty: {}
				}
			}
			return sum;
		}
	`, `
		match read(true) {
			case Result.Ok(lock): { println(lock?.hash ?? "none"); }
			case Result.Err(reason): { println(reason); }
		}
		println(read(false).value() == null, read(true).isOk());

		let exact: Result<Int, String> = Result.Ok(5);
		let wider: Result<Int?, String> = exact;
		println(wider.value(), wider.map(n -> (n ?? 0) + 1).value(), wider.isOk());

		let some: Shape<Int> = Shape.Pair(1, 2);
		let none: Shape<Int?> = Shape.Box(null);
		println(total([some, none, Shape.Box(4), Shape.Empty]));
	`, `abc
true true
5 6 true
107
`)

	// The other way is not, nor is a struct, whose fields can be assigned
	refused := []string{
		"let r: Result<Int?, String> = Result.Ok(1); let back: Result<Int, String> = r;",
		"let b = Box<Result<Int, String>> { item: Result.Ok(1) }; let c: Box<Result<Int?, String>> = b;",
		"let b = Box<Int> { item: 1 }; let c: Box<Int?> = b;",
	}
	for _, body := range refused {
		errs := errors.NewHandler()
		Compile("import std.result.{Result};\nstruct Box<T> { item: T; }\nfn main() -> Int {\n"+body+"\nreturn 0;\n}\n", "test.qi", errs)
		if !errs.HasErrors() || errs.Diagnostics[0].Code != "E2001" {
			t.Errorf("%s: expected E2001, got %v", body, errs.Diagnostics)
		}
	}
}
