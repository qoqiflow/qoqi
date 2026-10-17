package stdlib_test

import "testing"

// Result has or and orElse as an optional has them: or takes the
// fallback, orElse a function that makes it, of the reason, and only when
// it is needed. collect gives all the values or the first failure.
func TestResultOrCollect(t *testing.T) {
	expect(t, "import std.result;\nenum Problem { Empty, NotANumber(String), TooLarge(Int) }\nfn port(text: String) -> Result<Int, Problem> {\n    if text == \"\" { return Err(Problem.Empty); }\n    let n = text.parseInt();\n    if n == null { return Err(Problem.NotANumber(text)); }\n    if n > 65535 { return Err(Problem.TooLarge(n)); }\n    return Ok(n);\n}\nfn explain(problem: Problem) -> String {\n    return match problem { case Problem.Empty: \"empty\"; case Problem.NotANumber(text): \"not a number: ${text}\"; case Problem.TooLarge(n): \"too large: ${n}\"; };\n}\nfn ports(texts: [String]) -> Result<Int, Problem> {\n    let all = result.collect(texts.map(port))?;\n    return Ok(all.sum());\n}", `
		println(port("80").or(8080), port("x").or(8080), port("").orElse(p -> explain(p).len()));

		// orElse works the fallback out only when there is no value
		let asked: [String] = [];
		let make = fn(p: Problem) -> Int { asked.push(explain(p)); return 0; };
		println(port("443").orElse(make), port("99999").orElse(make), port("x1").orElse(make), asked);

		println(result.collect(["80", "443", "8080"].map(port)), result.collect(["80", "", "x", "99999"].map(port)));
		println(result.collect<Int, Problem>([]), ports(["1", "2", "3"]), ports(["1", "y"]).map(toString).orElse(explain));
		println(ports(["4", "5"]).or(0) + ports(["70000"]).or(1));
	`,
		`80 8080 5`,
		`443 0 0 ["too large: 99999", "not a number: x1"]`,
		`Ok([80, 443, 8080]) Err(Empty)`,
		`Ok([]) Ok(6) not a number: y`,
		`10`,
	)
}

// A Failure prints as describe writes it, alone, in a text and in the
// Result attempt gives.
func TestFailurePrintsAsDescribed(t *testing.T) {
	expect(t, "import std.result;\nimport std.result.{Failure, attempt, guard};\nfn pick(items: [Int], at: Int) -> Int { return items[at]; }", `
		let failed = attempt(fn() -> Int { return pick([1, 2], 5); });
		match failed {
			case Ok(n): { println(n); }
			case Err(failure): {
				println(failure);
				println("caught: ${failure}" == "caught: " + failure.describe());
			}
		}
		println(failed.reason()?.message, guard(fn() { panic("stop"); }) != null);
		let all: [Failure] = [];
		for at in [0, 3, 1] {
			let got = attempt(fn() -> Int { return pick([7, 8], at); });
			if got is Err(failure) { all.push(failure); }
		}
		println(all.len(), all.map(f -> toString(f).startsWith("index out of bounds: 3 (length 2), at test.qi:")));
	`,
		`index out of bounds: 5 (length 2), at test.qi:3:53 in function 'pick'`,
		`true`,
		`index out of bounds: 5 (length 2) true`,
		`1 [true]`,
	)
}
