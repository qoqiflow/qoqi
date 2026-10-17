package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

// condition ? one : other is the short if that gives a value: narrowing,
// widening and the branch not taken are as they are for `if`.
func TestChoiceGivesAValue(t *testing.T) {
	expectOutput(t, `
		import std.result.{Result};

		struct Account {
			owner: String;
			balance: Float;
			nickname: String? = null;
		}

		impl Account {
			// A null in one branch makes the value optional
			fn overdrawn() -> Float? {
				return self.balance < 0.0 ? -self.balance : null;
			}

			fn label() -> String {
				return self.nickname != null ? self.nickname.upper() : self.owner;
			}
		}

		fn grade(score: Int) -> String {
			return score >= 90 ? "A"
				: score >= 75 ? "B"
				: score >= 50 ? "C"
				: "F";
		}

		fn half(n: Int) -> Result<Int, String> {
			return n % 2 == 0 ? Result.Ok(n / 2) : Result.Err("${n} is odd");
		}

		// The ? of a failure and the ? of a choice side by side
		fn quarter(n: Int) -> Result<Int, String> {
			let h = half(n)?;
			return half(h)? > 0 ? Result.Ok(h / 2) : Result.Err("nothing left");
		}

		struct Tally {
			calls: Int = 0;
		}

		impl Tally {
			fn counted(value: Int) -> Int {
				self.calls += 1;
				return value;
			}
		}
	`, `
		let accounts = [
			Account { owner: "ada", balance: 120.5 },
			Account { owner: "grace", balance: -30.25, nickname: "amazing" },
		];

		for account in accounts {
			let owed = account.overdrawn();
			println(account.label(), owed == null ? "in credit" : "owes ${owed}");
		}

		println([95, 80, 51, 3].map(score -> grade(score)));
		println(quarter(8), quarter(6), quarter(2));

		// Only the branch taken runs
		let tally = Tally {};
		let picked = tally.calls == 0 ? tally.counted(1) : tally.counted(2);
		println(picked, tally.calls);

		// A whole number and a Float make a Float
		let ratio = accounts.len() > 5 ? 1 : 0.5;
		println(ratio * 3.0);

		// Inside other expressions: arguments, items, text, a function
		let n = accounts.len();
		println("${n} account${n == 1 ? "" : "s"}", [n > 1 ? "many" : "one", n > 9 ? "lots" : "few"]);
		let step = n > 1 ? fn(x: Int) -> Int { return x * 10; } : fn(x: Int) -> Int { return x; };
		println(step(4), (n > 1 ? 0..2 : 5..7).contains(1));
		let total = 0;

		for i in 0..6 {
			total += i % 2 == 0 ? i : -i;
		}

		println(total);
	`, "ada in credit\nAMAZING owes 30.25\n[\"A\", \"B\", \"C\", \"F\"]\nOk(2) Err(\"3 is odd\") Err(\"1 is odd\")\n1 1\n1.5\n2 accounts [\"many\", \"few\"]\n40 true\n-3\n")
}

func TestChoiceMistakes(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"a condition that is not a Bool", "let a = 3 ? 1 : 2;", "E2005", "condition must be Bool, got Int"},
		{"branches of two types", "let b = true ? 1 : \"one\";", "E2001", "this branch gives String, the one before gives Int"},
		{"an optional used outside its test", "let s: String? = null;\nlet n = s == null ? s.len() : 0;", "E2017", "cannot call 'len' on String?: the value may be null"},
		{"no other value", "let c = true ? 1;", "E0001", "expected ':' after the first value of '?'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := errors.NewHandler()
			Compile("import std.strings;\nfn main() {\n"+c.body+"\n}\n", "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if !strings.HasPrefix(d.Code, c.code) || !strings.Contains(d.Message, c.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", c.code, c.msg, d.Code, d.Message)
			}
		})
	}
}
