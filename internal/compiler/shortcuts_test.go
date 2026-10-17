package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

const peopleDecls = `
import std.result.{Result};

struct Address { city: String; zip: String?; }
struct User { name: String; address: Address?; friend: User?; }
impl User {
	fn shout() -> String { return self.name.upper(); }
	fn rename(to: String) { self.name = to; }
	fn best() -> User? { return self.friend; }
}

fn user(name: String, city: String?) -> User {
	let address: Address? = null;
	if city != null { address = Address { city: city, zip: null }; }
	return User { name: name, address: address, friend: null };
}
`

func TestSafeAccess(t *testing.T) {
	expectOutput(t, peopleDecls, `
		let ada = user("ada", "London");
		let bob = user("bob", null);
		bob.friend = ada;
		let nobody: User? = null;
		let someone: User? = bob;

		// A field, a method, a chain; null as soon as something is missing
		println(someone?.name, nobody?.name, someone?.shout(), nobody?.shout());
		println(ada.address?.city, bob.address?.city, ada.address?.zip, someone?.address?.city);
		println(someone?.friend?.address?.city, someone?.best()?.shout(), nobody?.best()?.shout(), ada.best()?.name);
		println(someone?.name ?? "none", nobody?.name ?? "none", (nobody?.friend?.name ?? "no") + " friend");

		// A method called for what it does is not called on null
		nobody?.rename("x");
		someone?.rename("Bob");
		println(bob.name, someone?.name == "Bob", nobody?.name == null);

		// What is not reached is not evaluated
		let calls = 0;
		let next = fn() -> String { calls++; return "n${calls}"; };
		nobody?.rename(next());
		println(calls);
		someone?.rename(next());
		println(calls, bob.name);

		// The value is looked at once
		let asked = 0;
		let find = fn() -> User? { asked++; return bob; };
		println(find()?.name, find()?.friend?.name, asked);

		// In a condition, a loop, an argument, a text
		let crowd: [User?] = [ada, null, bob];
		let cities: [String] = [];
		for person in crowd {
			if person?.address != null { cities.push(person?.address?.city ?? "?"); }
		}
		println(cities, crowd.map(fn(person: User?) -> String { return person?.shout() ?? "-"; }), "hello ${nobody?.name ?? "you"}");

		// A variable the access assigns keeps the value it was tested with
		let current: User? = ada;
		let rebind = fn() -> String { current = null; return "gone"; };
		current?.rename(rebind());
		println(current == null, ada.name);
	`, `bob null BOB null
London null null null
London ADA null null
bob none no friend
Bob true true
0
1 n1
n1 ada 2
["London"] ["ADA", "-", "N1"] hello you
true gone
`)
}

// The methods of the builtin types after a ?.: they are rewritten into
// calls of the library, with what they are called on as an argument, and
// the value of the ?. still has to be found in there.
func TestSafeAccessToBuiltinMethods(t *testing.T) {
	expectOutput(t, peopleDecls+`
		struct Team { members: [User]; tags: [String]; }
	`, `
		let ada = user("ada", "London");
		let bob = user("bob", null);
		let team: Team? = Team { members: [ada, bob], tags: ["core", "infra"] };
		let none: Team? = null;
		let suffix = "!";

		// A method of an array, of a text, of a number
		println(team?.members.len(), none?.members.len(), team?.tags.join("+"), team?.tags[1].upper());
		println(team?.members[0].name.upper(), none?.tags.first(), team?.members.len().max(10));

		// With a function written on the spot, that captures a variable
		println(team?.members.map(m -> m.name + suffix), none?.members.map(m -> m.name + suffix));
		println(team?.members.filter(m -> m.address?.city != null).map(m -> m.shout()) ?? []);
		println(team?.tags.any(tag -> tag.startsWith("in")), team?.members.fold(0, (sum, m) -> sum + m.name.len()));

		// A ?. inside the chain of another, and inside a function
		println(team?.members[1].address?.city.upper(), team?.members[0].address?.city.lower().len());
		let cities = fn(t: Team?) -> [String] {
			return t?.members.map(m -> m.address?.city.upper() ?? "-") ?? [];
		};
		println(cities(team), cities(none));

		// In a loop, and in a closure kept for later
		let labels: [fn() -> String] = [];
		for maybe in [team, none] {
			labels.push(() -> maybe?.tags.map(tag -> tag.upper()).join(",") ?? "none");
		}
		println(labels.map(label -> label()));
	`, `2 null core+infra INFRA
ADA null 10
["ada!", "bob!"] null
["ADA"]
true 6
null 6
["LONDON", "-"] []
["CORE,INFRA", "none"]
`)
}

func TestTryOnOptionals(t *testing.T) {
	expectOutput(t, `
		struct Config { port: String?; host: String?; }

		fn port(config: Config) -> Int? {
			return (config.port?).parseInt()?;
		}

		fn address(config: Config) -> String? {
			let host = config.host?;
			let number = port(config)?;
			return "${host}:${number}";
		}

		// The first that is missing ends it, and nothing after it runs
		fn total(texts: [String], seen: [String]) -> Int? {
			let sum = 0;
			for text in texts {
				seen.push(text);
				sum += text.parseInt()?;
			}
			return sum;
		}

		// In a function that returns nothing, it just ends there
		fn note(log: [String], text: String?) {
			log.push("got " + text?);
		}

		fn firstLong(words: [String]) -> String? {
			let pick = fn(word: String) -> String? {
				if word.len() < 4 { return null; }
				return word;
			};
			for word in words {
				let found = pick(word);
				if found != null { return found.upper(); }
			}
			return words.first()? + "?";
		}
	`, `
		println(port(Config { port: "80", host: null }), port(Config { port: "x", host: null }), port(Config { port: null, host: null }));
		println(address(Config { port: "80", host: "local" }), address(Config { port: "80", host: null }), address(Config { port: "eighty", host: "local" }));

		let seen: [String] = [];
		println(total(["1", "2", "3"], seen), total(["4", "x", "5"], seen), total([], seen), seen);

		let log: [String] = [];
		note(log, "a");
		note(log, null);
		note(log, "b");
		println(log);
		println(firstLong(["a", "word", "b"]), firstLong(["a", "b"]), firstLong([]));

		// In a lambda it leaves the lambda, not the function around it
		let half = fn(text: String) -> Int? { return text.parseInt()? / 2; };
		println(half("10"), half("ten"), ["4", "x"].map(half));
	`, `80 null null
local:80 null null
6 null 0 ["1", "2", "3", "4", "x"]
["got a", "got b"]
WORD a? null
5 null [2, null]
`)
}

func TestTryOnResults(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};

		enum Problem { Empty, NotANumber(String), TooBig(Int) }

		fn number(text: String) -> Result<Int, Problem> {
			if text == "" { return Result.Err(Problem.Empty); }
			let n = text.parseInt();
			if n == null { return Result.Err(Problem.NotANumber(text)); }
			return Result.Ok(n);
		}

		fn small(n: Int) -> Result<Int, Problem> {
			if n > 100 { return Result.Err(Problem.TooBig(n)); }
			return Result.Ok(n);
		}

		// Steps that can each fail, in a row: the first failure comes out
		fn sum(a: String, b: String) -> Result<Int, Problem> {
			return Result.Ok(small(number(a)?)? + small(number(b)?)?);
		}

		// The failure passes through a function of another value type
		fn label(text: String) -> Result<String, Problem> {
			let n = number(text)?;
			return Result.Ok("#${n}");
		}

		// A failure of another type is turned into this function's first
		fn describe(text: String) -> Result<Int, String> {
			let n = number(text).mapErr(fn(problem: Problem) -> String { return "bad: ${problem}"; })?;
			return Result.Ok(n * 2);
		}

		fn all(texts: [String], done: [Int]) -> Result<[Int], Problem> {
			for text in texts { done.push(number(text)?); }
			return Result.Ok(done);
		}

		struct Counter { total: Int; }
		impl Counter {
			fn add(text: String) -> Result<Counter, Problem> {
				self.total += number(text)?;
				return Result.Ok(self);
			}
		}

		fn first<T>(items: [T], why: Problem) -> Result<T, Problem> {
			let item = items.first();
			if item == null { return Result.Err(why); }
			return Result.Ok(item);
		}

		fn firstNumber(texts: [String]) -> Result<Int, Problem> {
			return number(first(texts, Problem.Empty)?);
		}
	`, `
		println(sum("1", "2"), sum("", "2"), sum("1", "x"), sum("500", "1"), sum("1", "500"), sum("x", ""));
		println(label("7"), label("seven"), describe("21"), describe("x"), describe(""));

		// What was done before the failure stays done
		let done: [Int] = [];
		println(all(["1", "2"], done), all(["3", "x", "4"], done).isErr(), done);

		let counter = Counter { total: 0 };
		println(counter.add("5").isOk(), counter.add("no").reason(), counter.add("6").value()?.total ?? -1, counter.total);
		println(firstNumber(["8", "9"]), firstNumber([]), firstNumber(["x"]));

		// The value of a failure is the very value that failed
		let failed = number("");
		let passed = label("");
		println(failed.reason() == passed.reason(), passed);

		// In a lambda, against the lambda's own result
		let twice = fn(text: String) -> Result<Int, Problem> { return Result.Ok(number(text)? * 2); };
		println(twice("4"), twice("four"), ["1", ""].map(twice));
	`, `Ok(3) Err(Empty) Err(NotANumber("x")) Err(TooBig(500)) Err(TooBig(500)) Err(NotANumber("x"))
Ok("#7") Err(NotANumber("seven")) Ok(42) Err("bad: NotANumber(\"x\")") Err("bad: Empty")
Ok([1, 2, 3]) true [1, 2, 3]
true NotANumber("no") 11 11
Ok(8) Err(Empty) Err(NotANumber("x"))
true Err(Empty)
Ok(8) Err(NotANumber("four")) [Ok(2), Err(Empty)]
`)
}

func TestTryLeavesClosuresWorking(t *testing.T) {
	// Leaving a function early with `?` is a return like any other: what
	// its closures captured stays with them
	expectOutput(t, `
		fn counters(texts: [String], made: [fn() -> Int]) -> Int? {
			let count = 0;
			for text in texts {
				let step = text.parseInt()?;
				made.push(fn() -> Int { count += step; return count; });
			}
			return count;
		}
	`, `
		let made: [fn() -> Int] = [];
		println(counters(["1", "10", "x", "100"], made), made.len());
		println(made[0](), made[1](), made[0](), made[1]());
	`, "null 2\n1 11 12 22\n")
}

func TestNarrowingReachesLambdas(t *testing.T) {
	expectOutput(t, `
		struct Note { title: String; tags: [String]; }

		fn tagged(notes: [Note], tag: String?) -> [String] {
			if tag == null { return notes.map(fn(note: Note) -> String { return note.title; }); }
			let kept = notes.filter(fn(note: Note) -> Bool { return note.tags.contains(tag); });
			return kept.map(fn(note: Note) -> String { return "${note.title} (${tag.upper()})"; });
		}

		fn later(text: String?) -> fn() -> Int {
			if text == null { return fn() -> Int { return -1; }; }
			return fn() -> Int { return text.len(); };
		}

		fn shouts(words: [String?]) -> [String] {
			let jobs: [fn() -> String] = [];
			for word in words {
				if word != null { jobs.push(fn() -> String { return word.upper(); }); }
			}
			return jobs.map(fn(job: fn() -> String) -> String { return job(); });
		}

		fn plus(value: Any) -> Int {
			if value is Int {
				let add = fn(n: Int) -> Int { return n + value; };
				return add(1);
			}
			return 0;
		}
	`, `
		let notes = [Note { title: "a", tags: ["x"] }, Note { title: "b", tags: ["y", "x"] }, Note { title: "c", tags: [] }];
		println(tagged(notes, "x"), tagged(notes, "y"), tagged(notes, null), tagged(notes, "z"));
		println(later("four")(), later(null)(), shouts(["a", null, "b"]), plus(41), plus("x"));
	`, `["a (X)", "b (X)"] ["b (Y)"] ["a", "b", "c"] []
4 -1 ["A", "B"] 42 0
`)
}

func TestTuples(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};

		fn divide(a: Int, b: Int) -> (Int, Int) {
			return (a / b, a % b);
		}

		fn minMax(numbers: [Int]) -> (Int, Int)? {
			let low = numbers.first()?;
			let high = low;
			for n in numbers {
				if n < low { low = n; }
				if n > high { high = n; }
			}
			return (low, high);
		}

		fn swap<A, B>(pair: (A, B)) -> (B, A) {
			let (a, b) = pair;
			return (b, a);
		}

		fn describe(pair: (Int, Bool)) -> String {
			match pair {
				case (0, _): { return "zero"; }
				case (n, true): { return "yes ${n}"; }
				case (n, false): { return "no ${n}"; }
			}
		}

		fn quadrant(point: (Bool, Bool)) -> Int {
			match point {
				case (true, true): { return 1; }
				case (false, true): { return 2; }
				case (false, false): { return 3; }
				case (true, false): { return 4; }
			}
		}

		struct Segment { from: (Int, Int); to: (Int, Int); }
		impl Segment {
			fn length2() -> Int {
				let (x1, y1) = self.from;
				let (x2, y2) = self.to;
				return (x2 - x1) * (x2 - x1) + (y2 - y1) * (y2 - y1);
			}
		}

		fn parse(text: String) -> Result<(String, Int), String> {
			let (name, value) = text.cut("=") ?? (text, "");
			let n = value.parseInt();
			if n == null { return Result.Err("no number for ${name}"); }
			return Result.Ok((name, n));
		}
	`, `
		let pair = (1, "a");
		let wide: (Int, Float, Bool, String, [Int], String?) = (1, 2.5, true, "x", [1], null);
		println(pair, pair.0, pair.1, wide, ((1, 2), [3]).0.1);

		// Taken apart
		let (q, r) = divide(17, 5);
		let (a, _, c) = (1, 2, 3);
		println(q, r, a + c, divide(9, 3).0, minMax([3, 9, 1, 4]), minMax([]), minMax([7])?.1);

		// Equal when what they hold is equal
		println(divide(17, 5) == (3, 2), (1, "a") == pair, (1, "b") == pair, ((1, 2), "x") == ((1, 2), "x"), (1, 2) != (2, 1));

		// Generic, in arrays, in loops
		println(swap(pair), swap(swap(pair)) == pair, swap(((1, 2), [true])));
		let pairs = arrays.zip(["one", "two", "three"], [1, 2, 3]);
		for (word, n) in pairs { print("${word}=${n} "); }
		println("");
		for i, (word, _) in pairs { print("${i}:${word} "); }
		println("");
		let byLength = pairs.sortedBy(fn(entry: (String, Int)) -> Int { return entry.0.len(); });
		println(byLength, byLength.map(fn(entry: (String, Int)) -> Int { return entry.1; }));
		let (words, numbers) = arrays.unzip(pairs);
		println(words, numbers, numbers.sum());

		// Matched, the compiler checking nothing is left out
		println(describe((0, true)), describe((4, true)), describe((5, false)), quadrant((false, true)), quadrant((true, false)));

		// Inside structs, enums and optionals
		let segment = Segment { from: (0, 0), to: (3, 4) };
		println(segment.length2(), segment.to.0, segment);
		println(parse("port=80"), parse("port=x"), parse("port"));
		match parse("size=3") {
			case Result.Ok((name, 3)): { println("three for", name); }
			case Result.Ok((name, n)): { println(n, "for", name); }
			case Result.Err(why): { println(why); }
		}
		let maybe: (Int, String)? = null;
		println(maybe?.1, maybe ?? (0, "none"), (maybe ?? (0, "none")).1);

		// A tuple does not change: a new one is made
		let moved = (pair.0 + 1, pair.1);
		println(pair, moved);

		// As Any, and back
		let anything: Any = (1, "a");
		println(anything is (Int, String), anything is Int, (anything as (Int, String)).1, toString((1, (2, 3))), "${(true, [1])}");
	`, `(1, "a") 1 a (1, 2.5, true, "x", [1], null) 2
3 2 4 3 (1, 9) null 7
true true false true true
("a", 1) true ([true], (1, 2))
one=1 two=2 three=3 
0:one 1:two 2:three 
[("one", 1), ("two", 2), ("three", 3)] [1, 2, 3]
["one", "two", "three"] [1, 2, 3] 6
zero yes 4 no 5 2 4
25 3 {from: (0, 0), to: (3, 4)}
Ok(("port", 80)) Err("no number for port") Err("no number for port")
three for size
null (0, "none") none
(1, "a") (2, "a")
true false a (1, (2, 3)) (true, [1])
`)
}

func TestTextOverSeveralLines(t *testing.T) {
	expectOutput(t, `
		const usage = """
			usage: tool <command>
			  add "title"
			  list
			""";
	`, `
		let name = "ada";
		let n = 2;
		let letter = """
			Dear ${name.capitalize()},

			  you have ${n} ${"message" + "s"}.
			""";
		println("[" + usage + "]");
		println("[" + letter + "]");
		println(usage.lines()[1].len(), letter.lines().len(), """{"a": "b"}""", """a\tb""" == "a\tb");
	`, `[usage: tool <command>
  add "title"
  list]
[Dear Ada,

  you have 2 messages.]
13 3 {"a": "b"} true
`)
}

func TestShortcutErrors(t *testing.T) {
	prelude := "import std.result.{Result};\nimport std.strings;\nstruct User { name: String; }\nfn number(text: String) -> Result<Int, String> { return Result.Ok(1); }\n"
	tests := []struct {
		name string
		src  string
		code string
		msg  string
		hint string
	}{
		{"try in a function that returns no Result", "fn f() -> Int { return number(\"1\")?; }", "E2034",
			"'?' passes a failure on to the caller, and this function returns Int, not a Result", "Make the function return Result<..., String>"},
		{"try with another kind of failure", "fn f() -> Result<Int, Int> { return Result.Ok(number(\"1\")?); }", "E2034",
			"'?' would pass on a failure of type String, and this function returns failures of type Int", "mapErr"},
		{"try on a Result in a function that returns an optional", "fn f() -> Int? { return number(\"1\")?; }", "E2034",
			"this function returns Int?, not a Result", ""},
		{"try on an optional in a function that returns a Result", "fn f() -> Result<Int, String> { return Result.Ok(\"1\".parseInt()?); }", "E2034",
			"returns null to the caller, and this function returns std.result.Result<Int, String>", "Give a fallback with '??'"},
		{"try on an optional in a function that returns a value", "fn f() -> Int { return \"1\".parseInt()?; }", "E2034",
			"returns null to the caller, and this function returns Int", "Make the function return Int?"},
		{"try on a plain value", "fn f() -> Int? { return 5?; }", "E2034", "'?' is for a Result or a value that may be null, and this is a Int", "Remove the '?'"},
		{"try in a function that returns no Result", "fn helper() -> Int { let n = number(\"1\")?; return n; }", "E2034", "this function returns Int, not a Result", ""},
		{"safe access on what is never null", "fn f(u: User) -> String? { return u?.name; }", "E2035", "'?.' is for a value that may be null, and this User never is", "Use '.' here"},
		{"safe access on a Result", "fn f(r: Result<User, String>) -> String? { return r?.name; }", "E2035",
			"'?.' is for a value that may be null, and a std.result.Result<User, String> never is", "(value?).member"},
		{"a position a tuple does not have", "fn f(p: (Int, Int)) -> Int { return p.2; }", "E2014", "(Int, Int) holds 2 values: there is none at position 2", "the last one is .1"},
		{"names for more values than there are", "fn f(p: (Int, Int)) -> Int { let (a, b, c) = p; return a; }", "E2001", "(Int, Int) holds 2 values, and 3 are named here", "with '_'"},
		{"assigning to a position", "fn f(p: (Int, Int)) { p.0 = 1; }", "E2022", "a tuple cannot be changed once made", "Make a new tuple"},
		{"a match that leaves a pair out", "fn f(p: (Bool, Bool)) -> Int { match p { case (true, _): { return 1; } case (_, true): { return 2; } } }", "E2028",
			"match on (Bool, Bool) does not handle (false, false)", "case (false, false): { ... }"},
		{"a value of a tuple nothing gives a type", "fn f() { let t = (1, null); }", "E2030", "nothing here says what type value 2 of this tuple has", "let pair: (Int, String?) = (1, null);"},
		{"a tuple that is too large", "fn f() { let t = (1, 2, 3, 4, 5, 6, 7); }", "E0001", "a tuple holds 6 values at most, and this one has 7", "Use a struct"},
		{"a text that is never closed", "fn f() -> String { return \"\"\"abc; }", "E0002", "unterminated text: one opened with \"\"\" ends with \"\"\"", ""},
		{"what a test taught does not reach a lambda for a variable that changes", "fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> Int { return n + 1; }; n = null; return g(); }", "E2006", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := prelude + tt.src
			if !strings.Contains(tt.src, "fn main") {
				src += "\nfn main() -> Int { return 0; }"
			}
			errs := errors.NewHandler()
			result := Compile(src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			if result.Chunk != nil {
				t.Error("expected no bytecode for a rejected program")
			}
			d := errs.Diagnostics[0]
			if tt.msg == "" {
				// Only that it is refused, by a null check
				if !strings.Contains(d.Message, "null") {
					t.Errorf("expected an error about null, got %s: %s", d.Code, d.Message)
				}
				return
			}
			if d.Code != tt.code || !strings.Contains(strings.ToLower(d.Message), strings.ToLower(tt.msg)) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
			if !strings.Contains(strings.ToLower(d.Suggestion), strings.ToLower(tt.hint)) {
				t.Errorf("expected a suggestion containing %q, got %q", tt.hint, d.Suggestion)
			}
			if d.Context == "" {
				t.Error("expected the diagnostic to carry the source line")
			}
		})
	}
}

func TestShortcutsAcrossModules(t *testing.T) {
	expectModulesOutput(t, sources{
		"main.qi": `
			import geo.points;
			import geo.points.{Shape, Box};
			import std.result.{Result};

			fn main() -> Int {
				let full = Box<Int> { item: 4 };
				let empty = Box<Int> { item: null };
				println(full.pair("x"), empty.pair("x"), points.both(full, Box<String> { item: "s" }), points.both(empty, full));
				let (far, near) = points.spread([(0, 0), (3, 4), (1, 1)]) ?? ((0, 0), (0, 0));
				println(far, near, points.spread([]));
				let shape: Shape? = Shape { name: "dot", at: (2, 5) };
				let none: Shape? = null;
				println(shape?.at.1, none?.at.1, shape?.label(), none?.label() ?? "nothing");
				println(points.banner);
				return 0;
			}`,
		"geo/points.qi": `
			import std.result.{Result};
			import std.arrays;

			pub const banner = """
				points
				  v2
				""";

			pub struct Shape { name: String; at: (Int, Int); }
			impl Shape {
				pub fn label() -> String {
					let (x, y) = self.at;
					return "${self.name}@${x},${y}";
				}
			}

			pub struct Box<T> { item: T?; }
			impl<T> Box<T> {
				pub fn pair<U>(other: U) -> (T, U)? { return (self.item?, other); }
				pub fn need(why: String) -> Result<T, String> {
					let item = self.item;
					if item == null { return Result.Err(why); }
					return Result.Ok(item);
				}
			}

			pub fn both<A, B>(a: Box<A>, b: Box<B>) -> Result<(A, B), String> {
				return Result.Ok((a.need("no first")?, b.need("no second")?));
			}

			// The point furthest from the origin, and the nearest
			pub fn spread(points: [(Int, Int)]) -> ((Int, Int), (Int, Int))? {
				let distance = fn(point: (Int, Int)) -> Int { return point.0 * point.0 + point.1 * point.1; };
				return (points.maxBy(distance)?, points.minBy(distance)?);
			}`,
	}, `(4, "x") null Ok((4, "s")) Err("no first")
(3, 4) (0, 0) null
5 null dot@2,5 nothing
points
  v2
`)
}

// A fallback that may be null itself: a ?? b ?? c tries each in turn, and
// what is left optional stays so until a fallback that is not.
func TestFallbacksInARow(t *testing.T) {
	expectOutput(t, peopleDecls+`
		fn lookup(names: [String], wanted: String) -> Int? {
			return names.indexOf(wanted);
		}
	`, `
		let none: Int? = null;
		let some: Int? = 2;
		let ada = user("ada", "London");
		let bob = user("bob", null);
		bob.friend = ada;
		let nobody: User? = null;

		println(none ?? some ?? 0, none ?? none ?? 9, none ?? none, some ?? none);
		let total: Int = none ?? some ?? 0;
		println(total + 1);

		// With ?. and calls on either side
		println(nobody?.name ?? bob.friend?.name ?? "nobody", bob.address?.city ?? ada.address?.city ?? "nowhere");
		let names = ["ada", "bob"];
		println(lookup(names, "eve") ?? lookup(names, "bob") ?? -1, lookup(names, "eve") ?? lookup(names, "zed") ?? -1);

		// Tuples, as cut gives them
		let (key, value) = "a:b".cut("=") ?? "a:b".cut(":") ?? ("", "");
		println(key, value);

		// The fallbacks after the first that holds are not worked out
		let asked = 0;
		let ask = fn() -> Int? { asked++; return 5; };
		println(some ?? ask() ?? 0, none ?? ask() ?? ask() ?? 0, asked);
	`, `2 9 null 2
3
ada London
1 -1
a b
2 5 1
`)

	errs := errors.NewHandler()
	Compile("fn main() -> Int {\nlet a: Int? = null;\nlet b: String? = \"x\";\nlet c = a ?? b ?? 0;\nreturn 0;\n}\n", "test.qi", errs)
	if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, "fallback of type") {
		t.Errorf("expected a fallback of another type to be refused, got %v", errs.Diagnostics)
	}
}
