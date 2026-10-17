package compiler

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

const failureImports = `
	import std.result;
	import std.result.{Result, Failure, Kind, Location, attempt, guard};
`

func TestPanicStopsTheProgram(t *testing.T) {
	src := `fn withdraw(balance: Int, amount: Int) -> Int {
    if amount > balance {
        panic("cannot withdraw", amount, "from", balance);
    }
    return balance - amount;
}
fn pay(amount: Int) -> Int {
    return withdraw(100, amount);
}
fn main() -> Int {
    println(pay(30));
    println(pay(300));
    println("never printed");
    return 0;
}
`
	out, _, err := runProgram(t, src)
	if out != "70\n" {
		t.Errorf("expected the program to stop after the first line, got %q", out)
	}
	failure, ok := err.(*vm.RuntimeError)
	if !ok {
		t.Fatalf("expected a runtime error, got %T: %v", err, err)
	}
	// The message is the arguments, written as print writes them
	if failure.Kind != vm.KindPanic || failure.Message != "cannot withdraw 300 from 100" {
		t.Errorf("unexpected failure: %s %q", failure.Kind, failure.Message)
	}
	// Where it was called, then the calls that led there
	want := []string{"withdraw:3", "pay:8", "main:12"}
	if len(failure.Stack) != len(want) {
		t.Fatalf("expected %d frames, got %+v", len(want), failure.Stack)
	}
	for i, w := range want {
		name, line, _ := strings.Cut(w, ":")
		if frame := failure.Stack[i]; frame.Function != name || frame.File != "test.qi" || toText(frame.Line) != line {
			t.Errorf("frame %d: expected %s, got %s:%d", i, w, frame.Function, frame.Line)
		}
	}

	text := err.Error()
	for _, part := range []string{
		"[ERROR] R0001: cannot withdraw 300 from 100",
		"--> test.qi:3:9 in function 'withdraw'",
		`        panic("cannot withdraw", amount, "from", balance);`,
		"^^^^^",
		"called from test.qi:8:12 in function 'pay'",
		"called from test.qi:12:13 in function 'main'",
	} {
		if !strings.Contains(text, part) {
			t.Errorf("expected the message to contain %q, got:\n%s", part, text)
		}
	}
}

func toText(n int) string { return strconv.Itoa(n) }

func TestPanicEndsAPath(t *testing.T) {
	// A function that ends in a panic does not need a return after it,
	// in whatever construct the panic is
	expectOutput(t, `
		enum Shape { Circle(Int), Square(Int), Unknown }

		fn positive(n: Int) -> Int {
			if n > 0 { return n; }
			panic("not positive:", n);
		}
		fn sign(n: Int) -> String {
			if n > 0 { return "+"; } else if n < 0 { return "-"; } else { panic("zero has no sign"); }
		}
		fn area(s: Shape) -> Int {
			match s {
				case Shape.Circle(r): { return 3 * r * r; }
				case Shape.Square(side): { return side * side; }
				case Shape.Unknown: { panic("no area for an unknown shape"); }
			}
		}
		fn first(items: [Int]) -> Int {
			for item in items { return item; }
			panic("empty");
		}
		fn never() -> Int { panic("always"); }
	`, `
		println(positive(3), sign(4), sign(-4), area(Shape.Circle(2)), area(Shape.Square(3)), first([7, 8]));
	`, "3 + - 12 9 7\n")

	// What follows a panic never runs, and the compiler says so
	errs := errors.NewHandler()
	Compile(`fn main() -> Int {
    panic("stop");
    println("after");
    return 0;
}`, "test.qi", errs)
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E2023" || errs.Diagnostics[0].Pos.Line != 3 {
		t.Errorf("expected an unreachable-code warning on line 3, got %v", errs.Diagnostics)
	}

	// A panic in one branch only is not the end of the function
	errs = errors.NewHandler()
	Compile(`fn f(n: Int) -> Int { if n > 0 { panic("big"); } }
fn main() -> Int { return f(0); }`, "test.qi", errs)
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E2004" {
		t.Errorf("expected a missing return, got %v", errs.Diagnostics)
	}
}

func TestAttemptRecoversFromEveryKindOfFailure(t *testing.T) {
	expectOutput(t, failureImports+`
		fn pick(items: [Int], index: Int) -> Int { return items[index]; }
		fn deep(n: Int) -> Int { return deep(n + 1); }
		fn check(n: Int) -> Int {
			if n < 0 { panic("negative:", n); }
			return n * 2;
		}
	`, `
		let jobs: [fn() -> Int] = [
			fn() -> Int { return pick([1, 2, 3], 1); },
			fn() -> Int { return pick([1, 2, 3], 7); },
			fn() -> Int { return check(-4); },
			fn() -> Int { return 10 / (3 - 3); },
			fn() -> Int { return 140737488355327 + 1; },
			fn() -> Int { return deep(0); },
			fn() -> Int { let v: Any = "x"; return v as Int; },
			fn() -> Int { return "abc".substring(2, 9).len(); },
			fn() -> Int { return check(21); },
		];
		for i, job in jobs {
			match attempt(job) {
				case Result.Ok(n): { println(i, "ok", n); }
				case Result.Err(f): { println(i, f.kind, "|", f.message, "|", f.at.function); }
			}
		}
		// The program is whole after all that: deep recursion still has
		// its full depth, and values made before are intact
		println(jobs.len(), attempt(fn() -> Int { return check(1); }));
	`, `0 ok 2
1 Bounds | index out of bounds: 7 (length 3) | pick
2 Panic | negative: -4 | check
3 Arithmetic | division by zero | lambda_3
4 Arithmetic | integer overflow | lambda_4
5 Depth | stack overflow: max call depth 10000 exceeded | deep
6 Cast | cannot cast String to Int | lambda_6
7 Library | std.strings.substring: range 2..9 is outside a text of 3 characters | lambda_7
8 ok 42
9 Ok(2)
`)
}

func TestAttemptLeavesTheProgramConsistent(t *testing.T) {
	expectOutput(t, failureImports+`
		struct Account { balance: Int; log: [String]; }

		fn withdraw(a: Account, amount: Int) {
			a.log.push("asked " + toString(amount));
			if amount > a.balance { panic("not enough"); }
			a.balance -= amount;
			a.log.push("gave " + toString(amount));
		}
		fn depth(n: Int) -> Int { if n == 0 { return 0; } return 1 + depth(n - 1); }
		fn fails(n: Int) -> Int { if n == 0 { panic("bottom"); } return fails(n - 1); }
	`, `
		// What was done before the failure stays done; what came after it
		// did not happen
		let a = Account { balance: 50, log: [] };
		println(guard(fn() -> Void { withdraw(a, 20); }) == null);
		let failure = guard(fn() -> Void { withdraw(a, 500); });
		println(failure != null, a.balance, a.log);

		// A loop goes on after a failure in one of its turns
		let done: [Int] = [];
		let failed = 0;
		for n in [3, 0, 5, 0, 7] {
			match attempt(fn() -> Int { return 100 / n; }) {
				case Result.Ok(q): { done.push(q); }
				case Result.Err(_): { failed++; }
			}
		}
		println(done, failed);

		// Failing deep down and going deep again: the calls that were
		// abandoned do not count against the limit
		let i = 0;
		while i < 20 {
			let f = attempt(fn() -> Int { return fails(4000); });
			i++;
		}
		println(depth(9000));

		// A closure made in a call that failed still has its variables
		let keep: [fn() -> Int] = [];
		guard(fn() -> Void {
			let counter = 10;
			keep.push(fn() -> Int { counter++; return counter; });
			let boom = [1][5];
		});
		println(keep[0](), keep[0](), keep[0]());

		// An attempt inside an attempt: the inner one handles its own, the
		// outer one what gets out
		let outer = attempt(fn() -> String {
			let inner = attempt(fn() -> Int { return [1][9]; });
			let text = "inner failed: " + toString(inner.isErr());
			if inner.isErr() { panic(text); }
			return text;
		});
		match outer {
			case Result.Err(f): { println(f.kind, f.message); }
			case Result.Ok(text): { println("no failure:", text); }
		}

		// A function that does not fail is not disturbed
		println(attempt(fn() -> [Int] { return [1, 2]; }), attempt(fn() -> String { return "ok"; }).or("?"));

		// Many failures do not use the memory up
		let count = 0;
		let round = 0;
		while round < 3000 {
			let f = guard(fn() -> Void { let big = arrays.fill(200, "x"); panic("discard", big.len()); });
			if f != null { count++; }
			round++;
		}
		println(count);
	`, `true
true 30 ["asked 20", "gave 20", "asked 500"]
[33, 20, 14] 2
9000
11 12 13
Panic inner failed: true
Ok([1, 2]) ok
3000
`)
}

func TestFailureSaysWhereAndHow(t *testing.T) {
	src := `import std.result;
import std.result.{Result, Failure, Kind, attempt};
import std.arrays;
import std.strings;

fn third(items: [Int]) -> Int {
    return items[2];
}
fn load(items: [Int]) -> Int {
    return third(items) + 1;
}
fn main() -> Int {
    match attempt(fn() -> Int { return load([1]); }) {
        case Result.Ok(n): { println("ok", n); }
        case Result.Err(f): {
            println(f.kind == Kind.Bounds, f.message);
            println(f.at.function, f.at.file, f.at.line, f.at.column);
            println(f.describe());
            println(f.stack.map(fn(l: result.Location) -> String { return l.function + ":" + toString(l.line); }));
            println(f.trace());
            match f.kind {
                case Kind.Bounds: { println("handled as an index problem"); }
                default: { println("something else"); }
            }
        }
    }
    return 0;
}
`
	out, _, err := runProgram(t, src)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `true index out of bounds: 2 (length 1)
third test.qi 7 17
index out of bounds: 2 (length 1), at test.qi:7:17 in function 'third'
["third:7", "load:10", "lambda_0:13", "std.result.attempt_Int:261", "main:13"]
index out of bounds: 2 (length 1)
  at test.qi:7:17 in function 'third'
  called from test.qi:10:12 in function 'load'
  called from test.qi:13:40 in function 'lambda_0'
  called from <std>/result.qi:261:8 in function 'std.result.attempt_Int'
  called from test.qi:13:11 in function 'main'
handled as an index problem
`
	// The line inside the library moves when the library is edited
	normalize := func(s string) string {
		return regexpReplace(s, `(attempt:|result\.qi:)\d+`, "${1}N")
	}
	if normalize(out) != normalize(want) {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
}

func TestErrorsAsValuesCarryATrace(t *testing.T) {
	src := `import std.result;
import std.result.{Result, Location};

struct ParseError { message: String; stack: [Location]; }

fn bad(message: String) -> ParseError {
    return ParseError { message: message, stack: result.stack(1) };
}
fn digit(c: String) -> Result<Int, ParseError> {
    if c < "0" || c > "9" { return Result.Err(bad("'" + c + "' is not a digit")); }
    return Result.Ok(1);
}
fn number(text: String) -> Result<Int, ParseError> {
    return digit(text);
}
fn main() -> Int {
    match number("x") {
        case Result.Ok(n): { println(n); }
        case Result.Err(e): { println(result.trace(e.message, e.stack)); }
    }
    let here = result.stack(0);
    println(here[0].function, here[0].line);
    return 0;
}
`
	out, _, err := runProgram(t, src)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `'x' is not a digit
  at test.qi:10:47 in function 'digit'
  called from test.qi:14:12 in function 'number'
  called from test.qi:17:11 in function 'main'
main 21
`
	if out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
}

func TestResultCarriesReasons(t *testing.T) {
	expectOutput(t, failureImports+`
		struct Order { item: String; count: Int; }
		enum Problem { Empty, NotANumber(String), TooMany(Int) }

		fn count(text: String) -> Result<Int, Problem> {
			if text == "" { return Result.Err(Problem.Empty); }
			let n = text.parseInt();
			if n == null { return Result.Err(Problem.NotANumber(text)); }
			return Result.Ok(n);
		}
		fn limited(n: Int) -> Result<Int, Problem> {
			if n > 10 { return Result.Err(Problem.TooMany(n)); }
			return Result.Ok(n);
		}
		fn order(item: String, text: String) -> Result<Order, Problem> {
			return count(text).then(limited).map(fn(n: Int) -> Order { return Order { item: item, count: n }; });
		}
		fn explain(p: Problem) -> String {
			match p {
				case Problem.Empty: { return "nothing was typed"; }
				case Problem.NotANumber(text): { return "'" + text + "' is not a number"; }
				case Problem.TooMany(n): { return toString(n) + " is more than 10"; }
			}
		}
	`, `
		for text in ["3", "", "many", "25"] {
			match order("pen", text) {
				case Result.Ok(o): { println("ordered", o.count, o.item); }
				case Result.Err(problem): { println("refused:", explain(problem)); }
			}
		}

		let ok = count("7");
		let bad = count("x");
		println(ok.isOk(), ok.isErr(), bad.isOk(), bad.isErr());
		println(ok.value(), bad.value(), ok.reason(), bad.reason());
		println(ok.or(0), bad.or(0));
		println(ok.map(fn(n: Int) -> String { return "n" + toString(n); }), bad.map(fn(n: Int) -> String { return "never"; }));
		println(ok.mapErr(explain), bad.mapErr(explain));
		println(ok.then(limited), count("50").then(limited), bad.then(limited));
		println(ok.expect("a count"));

		// expect stops the program when there is nothing, saying why
		match attempt(fn() -> Int { return count("x").expect("the count of pens"); }) {
			case Result.Err(f): { println(f.kind, "|", f.message); }
			case Result.Ok(n): { println("no failure", n); }
		}
		println(Result<Int, String>.Ok(1) == Result.Ok(1), Result<Int, String>.Ok(1) == Result.Err("x"));
	`, `ordered 3 pen
refused: nothing was typed
refused: 'many' is not a number
refused: 25 is more than 10
true false false true
7 null null NotANumber("x")
7 0
Ok("n7") Err(NotANumber("x"))
Ok(7) Err("'x' is not a number")
Ok(7) Err(TooMany(50)) Err(NotANumber("x"))
7
Panic | the count of pens: NotANumber("x")
true false
`)
}

func TestPanicIsABuiltin(t *testing.T) {
	// It cannot be redeclared, and takes anything
	errs := errors.NewHandler()
	Compile("fn panic(m: String) { }\nfn main() -> Int { return 0; }", "test.qi", errs)
	if len(errs.Diagnostics) == 0 || errs.Diagnostics[0].Code != "E1002" {
		t.Errorf("expected panic to be taken, got %v", errs.Diagnostics)
	}

	_, _, err := runProgram(t, `struct P { x: Int; }
fn main() -> Int { panic("state:", P { x: 1 }, [1, 2], 3.5, true, null); }`)
	if err == nil || !strings.Contains(err.Error(), "R0001: state: {x: 1} [1, 2] 3.5 true null") {
		t.Errorf("expected every value in the message, got %v", err)
	}
	_, _, err = runProgram(t, `fn main() -> Int { panic(); }`)
	if failure, ok := err.(*vm.RuntimeError); !ok || failure.Kind != vm.KindPanic || failure.Message != "" {
		t.Errorf("expected a panic without a message, got %v", err)
	}
}

func regexpReplace(s, pattern, with string) string {
	return regexp.MustCompile(pattern).ReplaceAllString(s, with)
}

// TestResultWithNothing checks a Result<Void, E>: what a function gives
// that does something and may fail, but has nothing to say when it worked.
func TestResultWithNothing(t *testing.T) {
	expectOutput(t, failureImports+`
		fn save(ok: Bool) -> Result<Void, String> {
			if !ok { return Result.Err("disk full"); }
			return Result.Ok();
		}
		fn both(second: Bool) -> Result<Void, String> {
			save(true)?;
			save(second)?;
			return Result.Ok(null);
		}
	`, `
		match save(true) {
			case Result.Ok(_): { println("saved"); }
			case Result.Err(reason): { println("not saved:", reason); }
		}
		println(both(true).isOk(), both(false).reason() ?? "fine");
		let kept: Result<Void, String> = Result.Ok();
		let made = Result<Void, String>.Ok();
		println(kept.isOk(), made.isOk(), kept == made);
	`, `saved
true disk full
true true true
`)

	for _, src := range []string{
		"fn f() -> Result<Int, String> { return Result.Ok(); }",
		"fn f() -> Int { let r = Result.Ok(); return 0; }",
	} {
		errs := errors.NewHandler()
		Compile(failureImports+src+"\nfn main() -> Int { return 0; }", "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, "Result.Ok holds 1 value, got 0") {
			t.Errorf("expected Result.Ok() to be refused where its value is not Void: %s, got %v", src, errs.Diagnostics)
		}
	}
}

// TestTryInMain checks `?` in main, which has no caller: a failure ends
// the program there, as a panic does, saying why.
func TestTryInMain(t *testing.T) {
	for _, c := range []struct{ name, decls, want string }{
		{"a reason that is a text", `fn load() -> Result<Int, String> { return Result.Err("the config is missing"); }`, "the config is missing"},
		{"a reason that describes itself", `
			struct Problem { file: String; }
			impl Problem { fn describe() -> String { return "cannot read " + self.file; } }
			fn load() -> Result<Int, Problem> { return Result.Err(Problem { file: "app.json" }); }`, "cannot read app.json"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := runProgram(t, c.decls+`
fn main() -> Int {
    defer println("cleaned up");
    println("before");
    let n = load()?;
    println("not reached", n);
    return 0;
}
`)
			if out != "before\ncleaned up\n" {
				t.Errorf("expected the program to stop at the '?', its defer run, got %q", out)
			}
			failure, ok := err.(*vm.RuntimeError)
			if !ok || failure.Kind != vm.KindPanic || failure.Message != c.want {
				t.Fatalf("expected the program to end with %q, got %v", c.want, err)
			}
			if frame := failure.Stack[0]; frame.Function != "main" || strings.TrimSpace(frame.SourceLine) != "let n = load()?;" {
				t.Errorf("expected the failure where the '?' is, got %+v", frame)
			}
		})
	}

	// Anywhere else, a function that does not return a Result has a caller
	// to hand the failure to, and must say so
	for _, src := range []string{
		"fn f() -> Result<Int, String> { return Result.Ok(1); }\nfn helper() -> Int { return f()?; }\nfn main() -> Int { return helper(); }",
		"fn f() -> Result<Int, String> { return Result.Ok(1); }\nfn main() -> Int { let g = fn() -> Int { return f()?; }; return g(); }",
	} {
		errs := errors.NewHandler()
		Compile(src, "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, "'?' passes a failure on to the caller") {
			t.Errorf("expected '?' to be refused outside main:\n%s\ngot %v", src, errs.Diagnostics)
		}
	}
}

// TestTemplates checks that a text written in the program, given where a
// Template is expected, keeps its parts and its values apart, and that a
// String made elsewhere is refused there.
func TestTemplates(t *testing.T) {
	expectOutput(t, `
		import std.ops.{Template};
		fn pieces(t: Template) -> String {
			return "${t.parts} ${t.values} ${t.text()}";
		}
	`, `
		let name = "ada";
		let n = 3;
		println(pieces("hello ${name}, ${n} times"));
		println(pieces("${name}${n}"));
		println(pieces("nothing put in"));
		println(pieces("""
			over ${name}
			lines
			"""));
	`, `["hello ", ", ", " times"] ["ada", "3"] hello ada, 3 times
["", "", ""] ["ada", "3"] ada3
["nothing put in"] [] nothing put in
["over ", "\nlines"] ["ada"] over ada
lines
`)

	errs := errors.NewHandler()
	Compile(`
		import std.ops.{Template};
		fn pieces(t: Template) -> String { return t.text(); }
		fn main() -> Int { let made = "a " + "b"; println(pieces(made)); return 0; }
	`, "test.qi", errs)
	if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, "a Template is a text written in the program, and this is a String made elsewhere") {
		t.Errorf("expected a String in a variable to be refused as a Template, got %v", errs.Diagnostics)
	}
}

// TestTemplatesWhereverOneIsExpected checks that a text written in the
// program becomes a Template wherever the type says so, without an import:
// a variable, a return, a field, an item of an array, an optional, an
// assignment; and that a String is refused in each of those places.
func TestTemplatesWhereverOneIsExpected(t *testing.T) {
	expectOutput(t, `
		struct Job { name: String; command: Template; }
		fn build(n: String) -> Template { return "make ${n}"; }
		fn show(t: Template) -> String { return "${t.parts}|${t.values}"; }
	`, `
		let name = "a b";
		let kept: Template = "keep ${name}";
		let job = Job { name: "one", command: "run ${name} now" };
		let all: [Template] = ["first", "second ${name}"];
		let maybe: Template? = "maybe ${name}";
		println(show(kept), show(build(name)), show(job.command));
		println(show(all[0]), show(all[1]), show(maybe ?? kept));
		kept = "again ${name}";
		println(show(kept), kept.text());
		let plain = "still a String ${name}";
		println(plain.len());
	`, `["keep ", ""]|["a b"] ["make ", ""]|["a b"] ["run ", " now"]|["a b"]
["first"]|[] ["second ", ""]|["a b"] ["maybe ", ""]|["a b"]
["again ", ""]|["a b"] again a b
18
`)

	for _, src := range []string{
		`fn main() -> Int { let s = "x"; let t: Template = s; return 0; }`,
		`fn f(s: String) -> Template { return s; }` + "\nfn main() -> Int { return 0; }",
		`struct Job { command: Template; }` + "\n" + `fn main() -> Int { let s = "x"; let j = Job { command: s }; return 0; }`,
	} {
		errs := errors.NewHandler()
		Compile(src, "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, "a Template is a text written in the program, and this is a String made elsewhere") {
			t.Errorf("expected a String to be refused as a Template:\n%s\ngot %v", src, errs.Diagnostics)
		}
	}
}
