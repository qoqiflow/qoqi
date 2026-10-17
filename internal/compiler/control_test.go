package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestMatchGivesAValue(t *testing.T) {
	expectOutput(t, `
		enum Shape { Circle(Float), Rect(Float, Float), Dot }
		enum Token { Number(Int), Op(String), End }

		fn describe(shape: Shape) -> String {
			return match shape {
				case Shape.Circle(r): "circle ${r}";
				case Shape.Rect(w, h): "rect ${w} by ${h}";
				case Shape.Dot: "dot";
			};
		}

		// A case that is a block leaves: the function, or the loop
		fn eval(tokens: [Token]) -> Int {
			let total = 0;
			let sign = 1;
			for token in tokens {
				total += match token {
					case Token.Number(n): sign * n;
					case Token.Op(op): {
						sign = match op { case "-": -1; default: 1; };
						continue;
					}
					case Token.End: { break; }
				};
			}
			return total;
		}

		fn first(items: [Int]) -> Int? {
			let head = match items.len() {
				case 0: { return null; }
				default: items[0];
			};
			return head * 2;
		}

		fn size(value: Int?) -> String {
			let words = ["none", "one", "some"];
			return words[match value { case null: 0; case 1: 1; default: 2; }];
		}

		fn label(pair: (Int, String)) -> String {
			return match pair {
				case (0, _): "zero";
				case (_, text): text;
			};
		}

		fn sides(count: Int) -> Int { println("asked", count); return count; }

		// What is expected where the match stands takes cases of
		// different types: here two structs of one trait
		trait Named { fn name() -> String; }
		struct Cat {}
		struct Dog {}
		impl Named for Cat { fn name() -> String { return "cat"; } }
		impl Named for Dog { fn name() -> String { return "dog"; } }
		fn pet(barks: Bool) -> Named {
			return match barks { case true: Dog {}; default: Cat {}; };
		}
	`, `
		println(pet(true).name(), pet(false).name());
		println(describe(Shape.Circle(2.0)), describe(Shape.Rect(1.0, 2.5)), describe(Shape.Dot));
		println(eval([Token.Number(5), Token.Op("-"), Token.Number(2), Token.Op("+"), Token.Number(10), Token.End, Token.Number(1000)]));
		println(first([]), first([21]));
		println(size(null), size(1), size(9));
		println(label((0, "a")), label((5, "c")));

		// What is matched is worked out once, whatever the number of cases
		let name = match sides(4) { case 3: "triangle"; case 4: "square"; default: "polygon"; };
		println(name);

		// Commas end the cases as well, and the last needs nothing
		let short = match name { case "square": 4, case "triangle": 3, default: 0 };
		println(short);

		// One of the values is null: the match gives an optional
		let maybe = match short { case 4: "four"; default: null; };
		println(maybe ?? "nothing", (maybe ?? "").len());

		// As an argument, in a function written with '->', inside another
		println([1, 2, 3].map(n -> match n { case 2: 0; default: n * 2; }));
		println(match short { case 4: match name { case "square": "yes"; default: "no"; }; default: "never"; });

		// On types: inside the case the value is of that type
		let mixed: [Int | String] = [1, "two"];
		for value in mixed {
			println(match value { case Int: value + 1; case String: value.len(); default: 0; });
		}
	`, `dog cat
circle 2 rect 1 by 2.5 dot
13
null 42
none one some
zero c
asked 4
square
4
four 4
[2, 0, 6]
yes
2
3
`)
}

func TestMatchGuardsAndAlternatives(t *testing.T) {
	expectOutput(t, `
		enum Shape { Circle(Float), Rect(Float, Float), Dot }
		enum Light { Red, Amber, Green }

		fn describe(shape: Shape) -> String {
			return match shape {
				case Shape.Circle(r) if r > 10.0: "big circle";
				case Shape.Circle(r): "circle ${r}";
				case Shape.Rect(w, h) if w == h: "square";
				case Shape.Rect(_, _): "rect";
				case Shape.Dot: "dot";
			};
		}

		fn moving(light: Light) -> Bool {
			match light {
				case Light.Red | Light.Amber: { return false; }
				case Light.Green: { return true; }
			}
		}

		fn kind(value: Int | String | Float) -> String {
			match value {
				case Int | Float: { return "number"; }
				case String if value == "": { return "empty"; }
				case String: { return "text of ${value.len()}"; }
				default: { return "?"; }
			}
		}

		fn label(pair: (Int, String)) -> String {
			return match pair {
				case (0, _) | (1, _): "few";
				case (n, text) if n > 100: "${text}!";
				case (_, text): text;
			};
		}

		fn checked(n: Int) -> Bool { println("checked", n); return n > 1; }
	`, `
		println(describe(Shape.Circle(12.0)), describe(Shape.Circle(2.0)), describe(Shape.Rect(2.0, 2.0)), describe(Shape.Rect(1.0, 2.0)), describe(Shape.Dot));
		println(moving(Light.Red), moving(Light.Amber), moving(Light.Green));
		println(kind(1), kind(2.5), kind(""), kind("abc"));
		println(label((0, "a")), label((1, "b")), label((500, "c")), label((5, "d")));

		// Values: any of several, with a condition
		for n in [0, 1, 2, 3, 7] {
			match n {
				case 0 | 1: println(n, "is small");
				case 2 | 3 if n % 2 == 0: println(n, "is two");
				default: println(n, "is something else");
			}
		}

		// The condition is only asked when the pattern matched
		match 5 {
			case 1 if checked(1): println("one");
			case 5 if checked(5): println("five");
			default: println("none");
		}
	`, `big circle circle 2 square rect dot
false false true
number number empty text of 3
few few c! d
0 is small
1 is small
2 is two
3 is something else
7 is something else
checked 5
five
`)
}

func TestRanges(t *testing.T) {
	expectOutput(t, `
		fn bound(name: String, value: Int) -> Int { println("bound", name); return value; }

		fn grade(score: Int) -> String {
			return match score {
				case 90..=100: "A";
				case 80..90: "B";
				case 0 | 1 | 2: "tiny";
				case -5..0: "negative";
				default: "other";
			};
		}

		fn warm(degrees: Float) -> Bool {
			match degrees {
				case 18.5..30.0: { return true; }
				default: { return false; }
			}
		}
	`, `
		let total = 0;
		for i in 1..=4 { total += i; }
		println(total);

		// The end is left out, the bounds are worked out once
		for i in bound("low", 2)..bound("high", 5) { print(i, ""); }
		println("");

		// Nothing to go through
		for i in 3..3 { println("never", i); }
		for i in 5..1 { println("never", i); }
		for i in 3..=3 { println("once", i); }

		// With the position; break and continue; a range inside another
		for n, i in 10..13 { print("${n}:${i} "); }
		println("");
		for i in 0..10 {
			if i % 2 == 0 { continue; }
			if i > 6 { break; }
			for j in 0..i { print(j); }
			print(" ");
		}
		println("");

		// Arithmetic binds tighter than the two dots
		let count = 3;
		for i in 0..count - 1 { print(i); }
		println("");

		// Each iteration has its own variable for the functions made in it
		let fns: [fn() -> Int] = [];
		for i in 0..3 { fns.push(() -> i * i); }
		for f in fns { print(f(), ""); }
		println("");

		println(grade(95), grade(100), grade(85), grade(90), grade(1), grade(-3), grade(50));
		println(warm(20.0), warm(30.0), warm(18.5));
	`, `10
bound low
bound high
2 3 4 
once 3
0:10 1:11 2:12 
0 012 01234 
01
0 1 4 
A A B A tiny negative other
true false true
`)
}

func TestDefer(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};

		struct Journal { lines: [String]; }
		impl Journal {
			fn note(text: String) { self.lines.push(text); }
			fn read() -> [String] {
				let lines = self.lines;
				self.lines = [];
				return lines;
			}
		}

		fn parse(text: String) -> Result<Int, String> {
			let n = text.parseInt();
			if n == null { return Result.Err("not a number: ${text}"); }
			return Result.Ok(n);
		}

		// '?' leaves through the defers reached so far, the last first
		fn sum(a: String, b: String, journal: Journal) -> Result<Int, String> {
			defer journal.note("sum done");
			let x = parse(a)?;
			defer journal.note("after first");
			let y = parse(b)?;
			defer journal.note("after second");
			return Result.Ok(x + y);
		}

		// What is returned is worked out before the defers run
		fn counted() -> Int {
			let n = 1;
			defer { n = 100; }
			return n;
		}

		// A defer sees its variables as they are when it runs, and not
		// those of the same name declared after it
		fn names(journal: Journal) {
			let name = "first";
			defer journal.note("name is ${name}");
			name = "changed";
			if true {
				let name = "inner";
				journal.note("body sees ${name}");
				return;
			}
		}

		fn loops(journal: Journal) -> Int {
			let found = -1;
			for i in 0..5 {
				defer journal.note("i${i}");
				for j in 0..5 {
					defer journal.note("j${j}");
					if j == 1 { break; }
				}
				if i == 1 { continue; }
				if i == 2 {
					found = i;
					break;
				}
				journal.note("end of ${i}");
			}
			let k = 0;
			while true {
				defer { k += 10; }
				k++;
				if k > 20 { return k + found; }
			}
		}

		fn closures() -> [fn() -> Int] {
			let fns: [fn() -> Int] = [];
			for i in 0..3 {
				defer fns.push(() -> i * 10);
				fns.push(() -> i);
			}
			return fns;
		}
	`, `
		let journal = Journal { lines: [] };
		println(sum("1", "2", journal), journal.read());
		println(sum("1", "x", journal), journal.read());
		println(sum("x", "2", journal), journal.read());
		println(counted());
		names(journal);
		println(journal.read());
		println(loops(journal), journal.read());

		let outs: [Int] = [];
		for f in closures() { outs.push(f()); }
		println(outs);

		// A block of its own: left at its closing brace
		{
			defer journal.note("block left");
			journal.note("in block");
		}
		journal.note("after block");
		println(journal.read());

		// A loop inside a defer may break and continue
		{
			defer {
				for i in 0..10 {
					if i == 1 { continue; }
					if i == 3 { break; }
					journal.note("cleanup ${i}");
				}
			}
		}
		println(journal.read());
	`, `Ok(3) ["after second", "after first", "sum done"]
Err("not a number: x") ["after first", "sum done"]
Err("not a number: x") ["sum done"]
1
["body sees inner", "name is changed"]
25 ["j0", "j1", "end of 0", "i0", "j0", "j1", "i1", "j0", "j1", "i2"]
[0, 0, 1, 10, 2, 20]
["in block", "block left", "after block"]
["cleanup 0", "cleanup 2"]
`)
}

func TestDeferRunsWhenSomethingFails(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result, attempt};
		import std.task;

		struct Journal { lines: [String]; }
		impl Journal {
			fn note(text: String) { self.lines.push(text); }
			fn read() -> [String] {
				let lines = self.lines;
				self.lines = [];
				return lines;
			}
		}

		// Every call in progress cleans up, the innermost first
		fn deep(n: Int, journal: Journal) -> Int {
			defer journal.note("leave ${n}");
			if n == 0 { panic("bottom"); }
			return deep(n - 1, journal) + 1;
		}

		// A defer that fails itself: the one before it still runs, and the
		// new failure is the one that goes on
		fn twice(journal: Journal) {
			defer journal.note("outer ran");
			defer { panic("in defer"); }
			panic("first");
		}

		// The defers of a block already left do not run again
		fn later(journal: Journal) -> Int {
			{
				defer journal.note("block");
			}
			for i in 0..2 {
				defer journal.note("loop ${i}");
			}
			let zero = 0;
			defer journal.note("function");
			return 1 / zero;
		}

		// A failure caught on the way: the function goes on, and its own
		// defer runs once, at the end
		fn survives(journal: Journal) -> String {
			defer journal.note("survivor leaves");
			let inner = attempt(() -> deep(1, journal));
			journal.note("went on");
			return match inner {
				case Result.Ok(_): "fine";
				case Result.Err(failure): failure.message;
			};
		}
	`, `
		let journal = Journal { lines: [] };

		println(attempt(() -> deep(3, journal)).isErr(), journal.read());

		match attempt(() -> twice(journal)) {
			case Result.Err(failure): { println(failure.message, journal.read()); }
			case Result.Ok(_): { println("?"); }
		}

		println(attempt(() -> later(journal)).isErr(), journal.read());
		println(survives(journal), journal.read());

		// When nothing fails after all, they run once
		println(attempt(() -> deep(0 - 1, journal)).isErr());
		println(journal.read().len() > 100);

		// In a task: the failure reaches whoever waits, the cleanup has run
		let work = task.run(fn() { defer journal.note("task cleaned"); deep(1, journal); });
		println((wait work).isErr(), journal.read());
	`, `true ["leave 0", "leave 1", "leave 2", "leave 3"]
in defer ["outer ran"]
true ["block", "loop 0", "loop 1", "function"]
bottom ["leave 0", "leave 1", "went on", "survivor leaves"]
true
true
true ["leave 0", "leave 1", "task cleaned"]
`)
}

func TestControlFlowErrors(t *testing.T) {
	const decls = `
		enum Light { Red, Amber, Green }
		enum Shape { Circle(Float), Rect(Float, Float) }
	`
	tests := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"match value leaves a variant out", `let a = match Light.Red { case Light.Red: 1; };`, "E2028", "does not handle Light.Amber, Light.Green"},
		{"match value without default", `let a = match 3 { case 1: 1; case 2: 2; };`, "E2028", "no 'default'"},
		{"cases of different types", `let a = match 3 { case 1: 1; default: "two"; };`, "E2001", "this case gives String, the cases above give Int"},
		{"case that gives nothing", `let a = match 3 { case 1: { println("x"); } default: 2; };`, "E2040", "this case gives no value"},
		{"case that is a call of nothing", `let a = match 3 { case 1: println("x"); default: 2; };`, "E2040", "this case gives no value"},
		{"names some alternatives do not give", `let s = Shape.Circle(1.0); match s { case Shape.Circle(r) | Shape.Rect(_, _): { } default: { } }`, "E2029", "this pattern does not name 'r'"},
		{"condition that is not a boolean", `match 3 { case 1 if 5: { } default: { } }`, "E2005", "condition must be Bool"},
		{"a condition handles nothing for sure", `let s = Shape.Circle(1.0); match s { case Shape.Circle(r) if r > 1.0: { } case Shape.Rect(_, _): { } }`, "E2028", "does not handle Shape.Circle"},
		{"range of floats as a value", `let r = 0..10.5;`, "E2038", "the bounds of a range are whole numbers, this is Float"},
		{"range of texts as a value", `let r = ("a".."b").step(2);`, "E2038", "the bounds of a range are whole numbers, this is String"},
		{"range of floats in a loop", `for i in 0.5..2 { }`, "E2038", "whole numbers"},
		{"range of texts in a case", `match 3 { case "a".."b": { } default: { } }`, "E2038", "the bounds of a range are numbers"},
		{"range that cannot match", `match "x" { case 1..5: { } default: { } }`, "E2029", "never in a range"},
		{"assigning to the variable of a range", `for i in 0..3 { i = 2; }`, "E2026", "cannot assign to loop variable 'i'"},
		{"return in a defer", `defer { return 1; }`, "E2039", "'return' inside a defer"},
		{"break out of a defer", `while true { defer { break; } }`, "E2039", "'break' inside a defer"},
		{"continue out of a defer", `while true { defer { if true { continue; } } }`, "E2039", "'continue' inside a defer"},
		{"a defer with nothing to do", `defer;`, "E0001", "expected expression, found ';'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := decls + "\nfn main() -> Int {\n" + tt.body + "\nreturn 0;\n}\n"
			errs := errors.NewHandler()
			result := Compile(src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			if result.Chunk != nil {
				t.Error("expected no bytecode for a rejected program")
			}
			var first *errors.Diagnostic
			for i := range errs.Diagnostics {
				if errs.Diagnostics[i].Severity == errors.SeverityError {
					first = &errs.Diagnostics[i]
					break
				}
			}
			if first == nil || first.Code != tt.code || !strings.Contains(first.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %v", tt.code, tt.msg, errs.Diagnostics)
			}
		})
	}

	// '?' in a defer has nowhere to return to
	errs := errors.NewHandler()
	Compile(`
		import std.result.{Result};
		fn load() -> Result<Int, String> { return Result.Ok(1); }
		fn main() -> Result<Int, String> {
			defer { let n = load()?; }
			return Result.Ok(0);
		}
	`, "test.qi", errs)
	if !errs.HasErrors() || errs.Diagnostics[0].Code != "E2039" {
		t.Errorf("expected E2039 for '?' in a defer, got %v", errs.Diagnostics)
	}
}

// In the braces of a value, an if without an else and a match are
// statements when something follows them, as anywhere else; the last
// thing in the braces is the value, an if or a match included.
func TestStatementsBeforeAValue(t *testing.T) {
	expectOutput(t, `
		enum Light { Red, Amber, Green }

		fn label(n: Int, light: Light, log: [String]) -> String {
			let text = if n > 0 {
				if n > 100 {
					log.push("big");
				} else if n > 10 {
					log.push("medium");
				}

				match light {
					case Light.Red: { log.push("stop"); }
					default: {}
				}

				// An if with an else that an operator follows is a value going on
				let doubled = if n > 50 { n * 2 } else { n } + 1;

				for i in 0..2 {
					if i == 1 { log.push("twice"); }
				}

				match light {
					case Light.Red: "red ${doubled}";
					case Light.Amber: "amber ${doubled}";
					case Light.Green: "green ${doubled}";
				}
			} else {
				if n < -10 { log.push("very"); }
				if n == 0 { "zero" } else { "negative" }
			};

			return text;
		}
	`, `
		let log: [String] = [];
		println(label(200, Light.Red, log), log);
		log.clear();
		println(label(20, Light.Green, log), log);
		log.clear();
		println(label(-20, Light.Amber, log), log, label(0, Light.Red, []));

		// In a match that gives a value, and in a function written on the spot
		let kinds = [1, 50, -3].map(n -> match n > 0 {
			case true: {
				if n > 10 { log.push("large ${n}"); }
				"positive"
			}
			case false: "not";
		});
		println(kinds, log);
	`, `red 401 ["big", "stop", "twice"]
green 21 ["medium", "twice"]
negative ["very"] zero
["positive", "positive", "not"] ["very", "large 50"]
`)
}

// A match on a Bool with a case for true and one for false handles every
// value: it gives a value without a default, and a function can end on it.
func TestMatchOnBoolIsComplete(t *testing.T) {
	expectOutput(t, `
		fn sign(n: Int) -> String {
			match n >= 0 {
				case true: { return "+"; }
				case false: { return "-"; }
			}
		}

		fn both(a: Bool, b: Bool) -> String {
			return match a {
				case true: match b { case true: "both"; case false: "a"; };
				case false: if b { "b" } else { "none" };
			};
		}
	`, `
		println(sign(3), sign(-3), both(true, true), both(true, false), both(false, true), both(false, false));
		let parity = [1, 2, 3].map(n -> match n % 2 == 0 { case true: "even"; case false: "odd"; });
		println(parity);
	`, `+ - both a b none
["odd", "even", "odd"]
`)

	refused := []struct{ name, body string }{
		{"one value left out", `let v = match 1 > 0 { case true: "yes"; };`},
		{"a case with a condition", `let n = 1; let v = match n > 0 { case true if n > 5: "big"; case false: "no"; };`},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			Compile("fn main() -> Int {\n"+tt.body+"\nreturn 0;\n}\n", "test.qi", errs)
			if !errs.HasErrors() || errs.Diagnostics[0].Code != "E2028" {
				t.Errorf("expected E2028, got %v", errs.Diagnostics)
			}
		})
	}
}
