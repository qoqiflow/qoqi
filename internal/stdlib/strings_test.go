package stdlib_test

import "testing"

// parseFloat reads a number written the plain way, and nothing Go would
// read besides: reading the prices of a file, a bad line is null.
func TestParseFloatTakesPlainNumbers(t *testing.T) {
	expect(t, "", `
		let lines = ["apple 2.5", "pear .5", "plum 3.", "fig -1e1", "kiwi nan", "lime inf", "date 1_000", "yuzu 0x1p4", "cherry Infinity", "melon +4E-1"];
		let total = 0.0;
		let bad: [String] = [];
		for line in lines {
			let (name, price) = line.cut(" ") ?? ("", "");
			let value = price.parseFloat();
			if value == null {
				bad.push(name);
			} else {
				total += value;
			}
		}
		println(total, bad);
		println(" 2.5 ".parseFloat(), "".parseFloat(), ".".parseFloat(), "1e".parseFloat(), "-0".parseFloat(), "1e999".parseFloat());
	`,
		`-3.6 ["kiwi", "lime", "date", "yuzu", "cherry"]`,
		`2.5 null null null -0 null`,
	)
}

// Numbers join as texts do, written as toString writes them; trimStart and
// trimEnd take the spaces, tabs and line ends off one end.
func TestJoinNumbersAndTrimOneEnd(t *testing.T) {
	expect(t, "", `
		let scores = [12, 7, 30];
		let ratios = [0.5, 2.0, -1.25];
		println(scores.sorted().join(", "), ratios.join(" | "), [3, 1, 3].join(""));
		let none: [Int] = [];
		println("[" + none.join(",") + "]", [42].join(", "), scores.map(n -> n * 2).join("+"));
		println("<" + " \t a b \t".trimStart() + ">", "<" + " \t a b \t".trimEnd() + ">", "\n x\n".trimStart().trimEnd().len());
	`,
		`7, 12, 30 0.5 | 2 | -1.25 313`,
		`[] 42 24+14+60`,
		"<a b \t> < \t a b> 1",
	)
}

// A text of one character is one character, not "1 characters".
func TestSubstringSaysCharacter(t *testing.T) {
	expectFailure(t, "", `println("a".substring(0, 3));`, "range 0..3 is outside a text of 1 character")
	expectFailure(t, "", `println("ab".substring(1, 3));`, "range 1..3 is outside a text of 2 characters")
}

// A method is handed to a function by its name, as the documentation of
// std.option says: of a type, String.parseInt, or of a value, users.get.
func TestMethodsArePassedByName(t *testing.T) {
	expect(t, "import std.option;\nstruct User { name: String; manager: String?; }", `
		let users = {"ada": User { name: "Ada", manager: "bo" }, "bo": User { name: "Bo", manager: null }};
		println(["1", " 2", "x"].map(String.parseInt), [" a ", "b "].map(String.trim), ["ada", "zed"].map(users.get).map(u -> u?.name));
		let manager = option.then(users.get("ada"), u -> u.manager);
		let found = option.then(manager, users.get);
		println(option.mapOr(found, "no manager", u -> u.name), option.then("42", String.parseInt), option.map(7, toString));
	`,
		`[1, 2, null] ["a", "b"] ["Ada", null]`,
		`Bo 42 7`,
	)
}
