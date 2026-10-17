package stdlib_test

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	_ "github.com/qoqiflow/qoqi/internal/inspect" // The natives of std.compiler
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// run compiles and runs a program that uses the library. imports are the
// import lines, body the content of main. input is what readLine reads.
func run(t *testing.T, imports, body, input string) (string, error) {
	t.Helper()
	src := imports + "\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n"

	errs := errors.NewHandler()
	result := compiler.Compile(src, "test.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	machine.SetInput(strings.NewReader(input))
	_, err := machine.Exec(result.Chunk)
	return out.String(), err
}

// expect runs a program and compares what it prints, line by line.
func expect(t *testing.T, imports, body string, want ...string) {
	t.Helper()
	out, err := run(t, imports, body, "")
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	for i := 0; i < len(got) || i < len(want); i++ {
		g, w := "<nothing>", "<nothing>"
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("line %d:\n  want %s\n   got %s", i+1, w, g)
		}
	}
}

// expectFailure runs a program that must stop with a runtime error.
func expectFailure(t *testing.T, imports, body, want string) {
	t.Helper()
	_, err := run(t, imports, body, "")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("expected a runtime error containing %q, got %v", want, err)
	}
}

// expectFailureInProgram runs a program that must stop with a runtime
// error that names a function of the library and is shown at the line of
// the program that called it, as "abc".substring(0, 5) is, and not at a
// line of the library.
func expectFailureInProgram(t *testing.T, imports, body, want, source string) {
	t.Helper()
	_, err := run(t, imports, body, "")
	failure, ok := err.(*vm.RuntimeError)
	if !ok || !strings.Contains(failure.Message, want) {
		t.Fatalf("expected a runtime error containing %q, got %v", want, err)
	}
	if at := failure.Stack[0]; at.File != "test.qi" || !strings.Contains(at.SourceLine, source) {
		t.Errorf("expected %q to be shown at the line %q of the program, got %s:%d %q", want, source, at.File, at.Line, at.SourceLine)
	}
}

func TestModulesAreEmbedded(t *testing.T) {
	want := []string{
		"std.arrays", "std.compiler", "std.csv", "std.dict", "std.encoding", "std.flow", "std.format", "std.formatter", "std.fs", "std.hash", "std.http", "std.io", "std.json",
		"std.layout", "std.list", "std.log", "std.map",
		"std.math", "std.ops", "std.option", "std.os", "std.path", "std.random", "std.regex", "std.result", "std.semver", "std.set", "std.sql", "std.strings", "std.task", "std.test", "std.time", "std.url",
	}
	if got := stdlib.Modules(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("expected the modules %v, got %v", want, got)
	}

	src, err := stdlib.Read(stdlib.Root + "/math.qi")
	if err != nil || !strings.Contains(src, "pub fn abs(") {
		t.Errorf("expected the source of std.math, got %v", err)
	}
	if _, err := stdlib.Read(stdlib.Root + "/nothing.qi"); err == nil {
		t.Error("expected an error for a module that does not exist")
	}
	// Nothing outside the library can be read through it
	for _, path := range []string{stdlib.Root + "/../stdlib.go", stdlib.Root + "/sub/math.qi", "math.qi"} {
		if _, err := stdlib.Read(path); err == nil {
			t.Errorf("expected %s not to be readable", path)
		}
	}
	if !stdlib.IsPath(stdlib.Root+"/math.qi") || stdlib.IsPath("std/math.qi") || stdlib.IsPath("math.qi") {
		t.Error("IsPath must recognize the library's files and nothing else")
	}
}

// TestExternsMatchNatives checks the two halves of the library against
// each other: every `extern fn` of a module needs a native of that name in
// the virtual machine, and a native nobody declares is dead code.
func TestExternsMatchNatives(t *testing.T) {
	declared := make(map[string]*ast.FnDecl)
	for _, module := range stdlib.Modules() {
		file := stdlib.Root + "/" + strings.TrimPrefix(module, "std.") + ".qi"
		src, err := stdlib.Read(file)
		if err != nil {
			t.Fatal(err)
		}
		errs := errors.NewHandler()
		prog := parser.Parse(src, file, errs)
		for _, d := range errs.Diagnostics {
			t.Errorf("%s does not parse: %s", file, d.Message)
		}
		for _, decl := range prog.Decls {
			if fn, ok := decl.(*ast.FnDecl); ok && fn.Extern {
				declared[module+"."+fn.Name] = fn
			}
		}
	}

	for name := range declared {
		if !vm.IsNative(name) {
			t.Errorf("%s is declared with extern fn but the virtual machine has no such native", name)
		}
	}
	for _, name := range vm.NativeNames() {
		if declared[name] == nil {
			t.Errorf("the native %s is not declared by any module of the library", name)
		}
	}
}

// TestEveryModuleCompiles imports the whole library into one program. A
// module is compiled whole when it is imported, so this type checks every
// function of the library, called by a test below or not.
func TestEveryModuleCompiles(t *testing.T) {
	var imports []string
	for _, module := range stdlib.Modules() {
		imports = append(imports, "import "+module+";")
	}
	sort.Strings(imports)
	expect(t, strings.Join(imports, "\n"), `println("compiled");`, "compiled")
}

func TestMath(t *testing.T) {
	expect(t, "import std.math;", `
		println((-4).abs(), 4.abs(), 0.abs());
		println(3.min(9), 9.min(3), 3.max(9), (-1).max(-2));
		println(15.clamp(0, 10), (-5).clamp(0, 10), 5.clamp(0, 10));
		println((-9).sign(), 0.sign(), 9.sign());
		println(2.pow(10), (-3).pow(3), 7.pow(0), 0.pow(0), 10.pow(14));
		println(2.pow(-1), 1.pow(-5), (-1).pow(-3), (-1).pow(-4));
		println(12.gcd(18), (-12).gcd(18), 7.gcd(0), 0.gcd(0), 17.gcd(5));
		println(4.lcm(6), (-4).lcm(6), 0.lcm(6), 7.lcm(7));
		println(4.isEven(), (-3).isEven(), (-3).isOdd(), 0.isOdd());

		println(3.toFloat() / 2.0, 2.7.floor(), 2.1.ceil(), 2.5.round(), (-2.7).trunc());
		println(16.0.sqrt(), 2.0.pow(0.5), 0.0.exp(), math.e.log());
		println((-1.5).abs(), 1.5.min(2.5), 1.5.max(2.5));
		println(1.5.clamp(0.0, 1.0), (-0.5).clamp(0.0, 1.0), 0.25.clamp(0.0, 1.0), 7.clamp(0, 5).toFloat().clamp(1.5, 2.5));
		println((-0.1).sign(), 0.0.sign(), (-0.0).sign(), 3.5.sign(), (0.0 / 0.0).sign(), 1.0e300.sign());
		println(((math.pi / 2.0).sin() * 1000.0).round(), (math.pi.cos() * 1000.0).round());
		println(math.pi.degrees(), (180.0.radians() * 1000.0).round(), (1.0.atan2(1.0) * 4000.0).round());

		// The hypotenuse of a 3-4-5 triangle, through Int and back
		let a = 3;
		let b = 4;
		println((a * a + b * b).toFloat().sqrt().round());
	`,
		"4 4 0",
		"3 3 9 -1",
		"10 0 5",
		"-1 0 1",
		"1024 -27 1 1 100000000000000",
		"0 1 -1 1",
		"6 6 7 0 1",
		"12 12 0 7",
		"true false true false",
		"1.5 2 3 3 -2",
		"4 1.4142135623730951 1 1",
		"1.5 1.5 2.5",
		"1 0 0.25 2.5",
		"-1 0 0 1 0 1",
		"1000 -1000",
		"180 3142 3142",
		"5",
	)

	expectFailure(t, "import std.math;", `println((-4.0).sqrt());`, "std.math.sqrt: the square root of -4 is not a number")
	expectFailure(t, "import std.math;", `println(0.0.log());`, "std.math.log: the logarithm of 0 is not a number")
	expectFailure(t, "import std.math;", `println(1.0e300.floor());`, "std.math.floor: 1e+300 does not fit in an Int")
	// pow overflows like *, and says so at the line that called it
	expectFailure(t, "import std.math;", `println(10.pow(30));`, "integer overflow")
	expectFailureInProgram(t, "import std.math;", `let big = 2.pow(47);`, "std.math.pow: integer overflow", "let big = 2.pow(47);")
	expectFailureInProgram(t, "import std.math;", `let low = (-2).pow(48);`, "std.math.pow: integer overflow", "(-2).pow(48)")
	expect(t, "import std.math;", `println(2.pow(46), (-2).pow(47), 3.pow(29), (-1).pow(140737488355327), 0.pow(99), 1.pow(140737488355327));`,
		"70368744177664 -140737488355328 68630377364883 -1 0 1")
}

func TestStrings(t *testing.T) {
	expect(t, "import std.strings;", `
		println("flows".len(), "".len(), "héλ".len(), "ab".repeat(50).len(), "a b\n".len());
		println("flows".substring(1, 3), "flows".from(2), "flows".first(2), "flows".last(2));
		println("[" + "ab".first(9) + "]", "[" + "ab".last(9) + "]", "[" + "ab".first(-1) + "]", "[" + "ab".from(2) + "]");
		println("a,b,,c".split(","), "héλ".chars(), "".split(","));
		println("x\ny\n".lines(), "x\r\ny".lines(), "".lines(), "\n\n".lines());
		println("  a  b\tc\nd ".words(), "".words(), "   ".words());
		println(["a", "b", "c"].join("-"), "[" + [].join("-") + "]", "ab".repeat(3), "[" + "ab".repeat(0) + "]");
		println("[" + "7".padLeft(3) + "]", "[" + "7".padRight(3) + "]", "[" + "1234".padLeft(3) + "]", "[" + "".padRight(2) + "]");
		println("abc".reverse(), "héλ".reverse(), "[" + "".reverse() + "]");
		println("banana".indexOf("na"), "banana".lastIndexOf("na"), "banana".indexOf("x"), "héλlo".indexOf("l"));
		println("banana".contains("nan"), "banana".contains("nab"), "banana".contains(""));
		println("banana".startsWith("ban"), "ban".startsWith("banana"), "banana".startsWith(""), "banana".startsWith("an"));
		println("banana".endsWith("na"), "na".endsWith("banana"), "banana".endsWith(""), "banana".endsWith("an"));
		println("banana".count("a"), "banana".count("an"), "aaaa".count("aa"), "banana".count("x"), "banana".count(""));
		println("a-b-c".replace("-", "+"), "école".upper(), "ÉCOLE".lower(), "[" + " \t x y \n".trim() + "]");
		println("ada".capitalize(), "élan".capitalize(), "[" + "".capitalize() + "]", "A".capitalize());
		println("".isEmpty(), " ".isEmpty(), " \t\n".isBlank(), " x ".isBlank());
		println("123".isDigits(), "12a".isDigits(), "".isDigits(), "-1".isDigits());
		println("42".parseInt(), " -7 ".parseInt(), "4x".parseInt(), "".parseInt(), "2.5".parseFloat(), "x".parseFloat());
		println("A".code(), strings.fromCode(955), strings.fromCode("z".code() - 25));
		println("ada".hash() == "ada".hash(), "ada".hash() == "adb".hash(), "".hash() >= 0);

		// What a parse gives is an optional: it has to be tested before use
		let total = 0;
		for text in ["10", "x", "32", ""] {
			let n = text.parseInt();
			if n != null { total += n; }
		}
		println(total, "oops".parseInt() ?? -1);

		// The position found by a search, used to cut the text
		let line = "name=ada lovelace";
		let at = line.indexOf("=");
		if at != null {
			println(line.first(at), "/", line.from(at + 1));
		}
	`,
		"5 0 3 100 4",
		"lo ows fl ws",
		"[ab] [ab] [] []",
		`["a", "b", "", "c"] ["h", "é", "λ"] [""]`,
		`["x", "y"] ["x", "y"] [] ["", ""]`,
		`["a", "b", "c", "d"] [] []`,
		"a-b-c [] ababab []",
		"[  7] [7  ] [1234] [  ]",
		"cba λéh []",
		"2 4 null 3",
		"true false true",
		"true false true false",
		"true false true false",
		"3 2 2 0 0",
		"a+b+c ÉCOLE école [x y]",
		"Ada Élan [] A",
		"true false true false",
		"true false false false",
		"42 -7 null null 2.5 null",
		"65 λ a",
		"true false true",
		"42 -1",
		"name / ada lovelace",
	)

	expectFailure(t, "import std.strings;", `println("abc".substring(2, 9));`, "std.strings.substring: range 2..9 is outside a text of 3 characters")
	expectFailure(t, "import std.strings;", `println("abc".from(4));`, "range 4..3 is outside")
	expectFailure(t, "import std.strings;", `println("abc".replace("", "x"));`, "the text to replace is empty")
	expectFailure(t, "import std.strings;", `println("ab".repeat(-1));`, "cannot repeat a text -1 times")
	expectFailure(t, "import std.strings;", `println("".code());`, "an empty text has no first character")
}

func TestArrays(t *testing.T) {
	expect(t, "import std.arrays;\nimport std.strings;\nstruct City { name: String; people: Int; }", `
		let n = arrays.range(1, 8);
		println(n.len(), [].len(), ["a", "b"].len(), [[1, 2], [3]].len(), arrays.fill(40, true).len());
		println(n, arrays.range(3, 3), arrays.range(5, 2), arrays.range(-2, 1));
		println(arrays.fill(3, "x"), arrays.fill(0, 1), arrays.fill(2, [1]));
		println(n.slice(2, 4), n.slice(-3, 2), n.slice(5, 99), n.slice(4, 2));
		println([1].concat([2, 3]), [].concat([]), n.reversed(), [].reversed());
		println(n.without(0), n.without(6), [1, 2].without(9), [1, 2].without(-1));
		println([1, 3].inserted(1, 2), [1, 2].inserted(0, 0), [1, 2].inserted(2, 3), [1, 2].inserted(9, 3), [].inserted(0, 1));
		println(n.first(), n.last(), [].first(), [].last(), n.at(2), n.at(7), n.at(-1));
		println(n.isEmpty(), [].isEmpty());

		let even = fn(x: Int) -> Bool { return x % 2 == 0; };
		let big = fn(x: Int) -> Bool { return x > 100; };
		println(n.filter(even), n.filter(big), n.count(even), n.count(big));
		println(n.any(even), n.any(big), n.all(even), n.all(fn(x: Int) -> Bool { return x > 0; }), [].all(big));
		println(n.find(even), n.find(big), n.findIndex(even), n.findIndex(big));
		println(n.map(fn(x: Int) -> String { return "#" + toString(x); }), ["a", "bcd"].map(fn(s: String) -> Int { return s.len(); }));
		println(n.fold(0, fn(a: Int, x: Int) -> Int { return a + x; }), ["a", "b"].fold("", fn(a: String, s: String) -> String { return s + a; }), [].fold<Int>(7, fn(a: Int, x: Int) -> Int { return a + x; }));

		// each calls a function for its effect
		let seen: [Int] = [];
		n.each(fn(x: Int) -> Void { seen.push(x * x); });
		println(seen);

		println(n.sum(), [].sum(), [3, 1, 2].smallest(), [3, 1, 2].largest(), [].smallest(), [].largest());
		println([3, 1, 2, 1, -5].sortedNumbers(), ["pear", "Apple", "fig"].sortedTexts(), [].sortedNumbers());
		println(n.contains(3), n.contains(30), ["a"].contains("a"), ["a"].contains("b"), [true].contains(false), [].contains(1));
		println(n.indexOf(3), n.indexOf(30), ["a", "b", "b"].indexOf("b"), [1.5, 2.5].indexOf(2.5));
		// Arrays and structs are equal by what they hold
		let row = [1, 2];
		let rows = [[1, 2], row];
		println(rows.indexOf(row), rows.contains([1, 2]));

		// Sorting structs, by one field then stably by another
		let cities = [City { name: "Lagos", people: 15 }, City { name: "Oslo", people: 1 }, City { name: "Lima", people: 10 }, City { name: "Bern", people: 1 }];
		let name = fn(c: City) -> String { return c.name; };
		let bySize = cities.sorted(fn(a: City, b: City) -> Bool { return a.people < b.people; });
		println(bySize.map(name), cities.sorted(fn(a: City, b: City) -> Bool { return a.name < b.name; }).map(name));
		// The original is untouched by everything above
		println(cities.map(name), n);

		// swap and copy: one changes its array, the other protects it
		let original = [1, 2, 3];
		let duplicate = original.copy();
		duplicate.swap(0, 2);
		println(original, duplicate);

		// A sort of many items, checked against the sum and the order
		let many: [Int] = [];
		let i = 0;
		while i < 2000 { many.push((i * 7919) % 2003); i++; }
		let ordered = many.sortedNumbers();
		let inOrder = true;
		for j, v in ordered {
			if j > 0 && ordered[j - 1] > v { inOrder = false; }
		}
		println(ordered.len(), inOrder, ordered.sum() == many.sum(), ordered[0], ordered[1999]);
	`,
		"7 0 2 2 40",
		"[1, 2, 3, 4, 5, 6, 7] [] [] [-2, -1, 0]",
		`["x", "x", "x"] [] [[1], [1]]`,
		"[3, 4] [1, 2] [6, 7] []",
		"[1, 2, 3] [] [7, 6, 5, 4, 3, 2, 1] []",
		"[2, 3, 4, 5, 6, 7] [1, 2, 3, 4, 5, 6] [1, 2] [1, 2]",
		"[1, 2, 3] [0, 1, 2] [1, 2, 3] [1, 2, 3] [1]",
		"1 7 null null 3 null null",
		"false true",
		"[2, 4, 6] [] 3 0",
		"true false false true true",
		"2 null 1 null",
		`["#1", "#2", "#3", "#4", "#5", "#6", "#7"] [1, 3]`,
		"28 ba 7",
		"[1, 4, 9, 16, 25, 36, 49]",
		"28 0 1 3 null null",
		`[-5, 1, 1, 2, 3] ["Apple", "fig", "pear"] []`,
		"true false true false false false",
		"2 null 1 1",
		"0 true",
		`["Oslo", "Bern", "Lima", "Lagos"] ["Bern", "Lagos", "Lima", "Oslo"]`,
		`["Lagos", "Oslo", "Lima", "Bern"] [1, 2, 3, 4, 5, 6, 7]`,
		"[1, 2, 3] [3, 2, 1]",
		"2000 true true 0 2002",
	)

	expectFailure(t, "import std.arrays;", `[1, 2].swap(0, 5);`, "index out of bounds: 5 (length 2)")
}

func TestList(t *testing.T) {
	expect(t, "import std.list;\nimport std.list.{List};\nstruct City { name: String; people: Int; }", `
		let names = list.empty<String>();
		println(names.len(), names.isEmpty(), names.first(), names.last(), names.pop(), names.get(0));

		names.add("x");
		names.addAll(["y", "z"]);
		names.insert(1, "w");
		println(names.toArray(), names.len(), names.isEmpty(), names.first(), names.last(), names.get(1), names.get(9), names.get(-1));
		names.set(0, "a");
		println(names.pop(), names.removeAt(0), names.removeAt(7), names.toArray());
		names.insert(99, "end");
		names.insert(0, "start");
		println(names.toArray());
		names.clear();
		println(names.toArray(), names.isEmpty());

		// A list has its own items: the array it came from is not shared
		let source = [3, 1, 2];
		let numbers = list.of(source);
		numbers.add(0);
		let exported = numbers.toArray();
		exported.push(99);
		println(source, numbers.toArray(), exported);

		// Calls that return a list can be chained
		let even = fn(n: Int) -> Bool { return n % 2 == 0; };
		let up = fn(a: Int, b: Int) -> Bool { return a < b; };
		println(numbers.sorted(up).toArray(), numbers.reversed().toArray(), numbers.slice(1, 3).toArray(), numbers.filter(even).toArray());
		println(numbers.filter(even).map<String>(fn(n: Int) -> String { return "<" + toString(n) + ">"; }).reversed().toArray());
		println(numbers.fold<Int>(0, fn(a: Int, n: Int) -> Int { return a + n; }), numbers.count(even), numbers.any(even), numbers.all(even));
		println(numbers.find(even), numbers.findIndex(even), numbers.find(fn(n: Int) -> Bool { return n > 9; }));
		println(numbers.contains(2), numbers.contains(7), numbers.indexOf(1), numbers.indexOf(7));
		// Type arguments of map and fold can be left to be worked out
		println(numbers.map(fn(n: Int) -> String { return toString(n) + "!"; }).toArray(), numbers.fold(0, fn(a: Int, n: Int) -> Int { return a + n; }));
		// ...and none of them changed the list; sort does
		println(numbers.toArray());
		numbers.sort(up);
		println(numbers.toArray());

		let total = 0;
		numbers.each(fn(n: Int) -> Void { total += n; });
		println(total);

		// Structs, and a list as a parameter and a field type
		let cities: List<City> = list.of([City { name: "Lagos", people: 15 }, City { name: "Oslo", people: 1 }]);
		cities.add(City { name: "Lima", people: 10 });
		cities.sort(fn(a: City, b: City) -> Bool { return a.people > b.people; });
		let largest = cities.first();
		if largest != null { println(largest.name, cities.map<String>(fn(c: City) -> String { return c.name; }).toArray()); }
	`,
		"0 true null null null null",
		`["x", "w", "y", "z"] 4 false x z w null null`,
		`z a null ["w", "y"]`,
		`["start", "w", "y", "end"]`,
		"[] true",
		"[3, 1, 2] [3, 1, 2, 0] [3, 1, 2, 0, 99]",
		"[0, 1, 2, 3] [0, 2, 1, 3] [1, 2] [2, 0]",
		`["<0>", "<2>"]`,
		"6 2 true false",
		"2 2 null",
		"true false 1 null",
		`["3!", "1!", "2!", "0!"] 6`,
		"[3, 1, 2, 0]",
		"[0, 1, 2, 3]",
		"6",
		`Lagos ["Lagos", "Lima", "Oslo"]`,
	)

	expectFailure(t, "import std.list;", `let l = list.of([1]); l.set(3, 9);`, "index out of bounds")
}

func TestMap(t *testing.T) {
	expect(t, "import std.dict;\nimport std.arrays;\nimport std.dict.{Dict};\nstruct Point { x: Int; y: Int; }", `
		let ages = dict.empty<Int>();
		println(ages.len(), ages.isEmpty(), ages.get("ada"), ages.has("ada"), ages.remove("ada"), ages.keys(), ages.values());

		ages.set("ada", 36);
		ages.set("linus", 21);
		ages.set("grace", 85);
		ages.set("ada", 37);          // Replaces: still three keys
		println(ages.len(), ages.isEmpty(), ages.get("ada"), ages.get("zed"), ages.has("linus"), ages.has("Linus"));
		println(ages.keys(), ages.values());
		println(ages.get("zed") ?? -1, (ages.get("ada") ?? 0) + 1);

		println(ages.remove("linus"), ages.remove("linus"), ages.len(), ages.keys(), ages.get("linus"));
		// A key set again after being removed goes to the end
		ages.set("linus", 22);
		println(ages.keys(), ages.values());

		// Keys that are alike are different keys; "" is a key too
		let odd = dict.empty<String>();
		odd.set("", "empty");
		odd.set(" ", "space");
		odd.set("a", "lower");
		odd.set("A", "upper");
		println(odd.len(), odd.get(""), odd.get(" "), odd.get("a"), odd.get("A"));

		// Many keys: the table grows several times, removed keys drop out
		let big = dict.empty<Int>();
		let i = 0;
		while i < 3000 { big.set("key" + toString(i), i * 2); i++; }
		i = 0;
		while i < 3000 { if i % 3 == 0 { big.remove("key" + toString(i)); } i++; }
		while i < 3500 { big.set("key" + toString(i), i * 2); i++; }
		println(big.len(), big.get("key0"), big.get("key1"), big.get("key2999"), big.get("key3499"), big.keys().len(), big.values().len());
		let sum = 0;
		big.each(fn(key: String, value: Int) -> Void { sum += value; });
		let expected = 0;
		i = 0;
		while i < 3500 { if i >= 3000 || i % 3 != 0 { expected += i * 2; } i++; }
		println(sum == expected, big.keys()[0], big.keys()[1]);

		// Counting with a map
		let counts = dict.empty<Int>();
		for word in ["to", "be", "or", "not", "to", "be"] {
			counts.set(word, (counts.get(word) ?? 0) + 1);
		}
		println(counts.keys(), counts.values());

		// Values of any type: structs, arrays, other maps
		let points: Dict<Point> = dict.empty<Point>();
		points.set("origin", Point { x: 0, y: 0 });
		points.set("far", Point { x: 9, y: 4 });
		let far = points.get("far");
		if far != null { println(far.x + far.y); }
		let groups = dict.empty<[String]>();
		groups.set("fruit", ["fig"]);
		let fruit = groups.get("fruit");
		if fruit != null { fruit.push("pear"); }
		println(groups.get("fruit"), groups.get("meat"));
		let nested = dict.empty<Dict<Int>>();
		nested.set("inner", counts);
		let inner = nested.get("inner");
		if inner != null { println(inner.get("to")); }
	`,
		"0 true null false false [] []",
		"3 false 37 null true false",
		`["ada", "linus", "grace"] [37, 21, 85]`,
		"-1 38",
		`true false 2 ["ada", "grace"] null`,
		`["ada", "grace", "linus"] [37, 85, 22]`,
		"4 empty space lower upper",
		"2500 null 2 5998 6998 2500 2500",
		"true key1 key2",
		`["to", "be", "or", "not"] [2, 2, 1, 1]`,
		"13",
		`["fig", "pear"] null`,
		"2",
	)
}

func TestOption(t *testing.T) {
	expect(t, `import std.option;
import std.strings;
import std.arrays;
import std.dict;

struct User { name: String; manager: String?; }

fn half(n: Int) -> Int? { if n % 2 != 0 { return null; } return n / 2; }
fn double(n: Int) -> Int { return n * 2; }`, `
		// map: a step that always has an answer
		println(option.map("21".parseInt(), double), option.map("x".parseInt(), double), option.map(half(8), toString));
		// then: a step that may have none; nothing at any point is nothing at the end
		println(option.then("84".parseInt(), half), option.then("7".parseInt(), half), option.then("x".parseInt(), half));
		println(option.then(option.then("84".parseInt(), half), half), option.then(option.then("6".parseInt(), half), half));
		// A step changes the type
		println(option.map(option.then("84".parseInt(), half), fn(n: Int) -> String { return "<" + toString(n) + ">"; }));

		let big = fn(n: Int) -> Bool { return n > 3; };
		println(option.filter(half(10), big), option.filter(half(4), big), option.filter(half(3), big));
		println(option.mapOr(half(10), "none", toString), option.mapOr(half(3), "none", toString), option.mapOr(half(8), 0, double), option.mapOr(half(7), 0, double));
		println(option.or(half(3), half(4)), option.or(half(2), half(4)), option.or(half(3), half(5)), option.or(half(3), 9));

		// orElse only works out the fallback when it is needed
		let calls = 0;
		let fallback = fn() -> Int { calls++; return -1; };
		let present = option.orElse(half(4), fallback);
		let before = calls;
		let absent = option.orElse(half(5), fallback);
		println(present, before, absent, calls);

		let seen: [Int] = [];
		option.each(half(6), fn(n: Int) -> Void { seen.push(n); });
		option.each(half(7), fn(n: Int) -> Void { seen.push(n); });
		println(seen, option.isSome(half(2)), option.isSome(half(1)), option.isNone(half(1)), option.isNone(half(2)));

		println(option.values([half(2), half(3), half(4)]), option.values(["1", "x", "3"].map(text -> text.parseInt())), option.values<Int>([]));

		// What comes out is an ordinary optional: ?? and null tests apply
		let n: Int = option.map("5".parseInt(), double) ?? 0;
		let m = option.then("9".parseInt(), half);
		if m == null { println(n + 1, "no half"); }

		// Looking something up through two maps and a field that may be null
		let users = dict.empty<User>();
		users.set("ada", User { name: "Ada", manager: "grace" });
		users.set("grace", User { name: "Grace", manager: null });
		users.set("bob", User { name: "Bob", manager: "gone" });
		let managerOf = fn(key: String) -> String? {
			let manager = option.then(users.get(key), fn(u: User) -> String? { return u.manager; });
			let found = option.then(manager, fn(k: String) -> User? { return users.get(k); });
			return option.map(found, fn(u: User) -> String { return u.name; });
		};
		println(managerOf("ada"), managerOf("grace"), managerOf("bob"), managerOf("nobody"), managerOf("ada") ?? "no manager");

		// With structs and arrays as the values
		let first = option.map([User { name: "Zed", manager: null }].first(), fn(u: User) -> String { return u.name; });
		let none = option.map([].first(), fn(u: User) -> String { return u.name; });
		println(first, none, option.mapOr([[1, 2], [3]].last(), 0, fn(row: [Int]) -> Int { return row.len(); }));
	`,
		"42 null 4",
		"42 null null",
		"21 null",
		"<42>",
		"5 null null",
		"5 none 8 0",
		"2 1 null 9",
		"2 0 -1 1",
		"[3] true false true false",
		"[1, 2] [1, 3] []",
		"11 no half",
		"Grace null null null Grace",
		"Zed null 1",
	)

	// The result of a step is checked like any optional
	errs := errors.NewHandler()
	compiler.Compile(`import std.option;
import std.strings;
fn main() -> Int {
    let n: Int = option.map("5".parseInt(), fn(n: Int) -> Int { return n; });
    let s = option.then("5".parseInt(), fn(n: String) -> Int? { return 1; });
    return 0;
}`, "test.qi", errs)
	if len(errs.Diagnostics) != 2 || errs.Diagnostics[0].Code != "E2001" || errs.Diagnostics[1].Code != "E2010" {
		t.Errorf("expected an optional result and a mismatched step to be rejected, got %v", errs.Diagnostics)
	}
}

func TestRandom(t *testing.T) {
	expect(t, "import std.random;\nimport std.arrays;", `
		// The same seed gives the same numbers
		random.seed(7);
		let first = [random.below(1000), random.below(1000), random.between(1, 6)];
		let fraction = random.float();
		random.seed(7);
		println(first[0] == random.below(1000), first[1] == random.below(1000), first[2] == random.between(1, 6), fraction == random.float());

		// Everything stays within its limits
		let ok = true;
		let i = 0;
		let seen = arrays.fill(6, false);
		while i < 500 {
			let die = random.between(1, 6);
			if die < 1 || die > 6 { ok = false; }
			seen[die - 1] = true;
			let b = random.below(3);
			if b < 0 || b > 2 { ok = false; }
			let f = random.float();
			if f < 0.0 || f >= 1.0 { ok = false; }
			i++;
		}
		println(ok, seen.all(fn(s: Bool) -> Bool { return s; }), random.between(4, 4), random.below(1));
		println(random.chance(1.0), random.chance(0.0));

		// pick and shuffle keep what they are given
		println(random.pick([5]), random.pick<Int>([]), [1, 2, 3].contains(random.pick([1, 2, 3]) ?? 0));
		let deck = arrays.range(0, 52);
		random.shuffle(deck);
		println(deck.len(), deck.sum(), deck.sortedNumbers()[0], deck.sortedNumbers()[51], deck.sortedNumbers()[30]);
		let empty: [Int] = [];
		random.shuffle(empty);
		println(empty);
	`,
		"true true true true",
		"true true 4 0",
		"true false",
		"5 null true",
		"52 1326 0 51 30",
		"[]",
	)

	expectFailure(t, "import std.random;", `println(random.below(0));`, "std.random.below: the limit must be at least 1, got 0")
	expectFailureInProgram(t, "import std.random;", `println(random.between(5, 1));`, "std.random.between: the low end 5 is above the high end 1", "random.between(5, 1)")
	expect(t, "import std.random;", `println(random.between(-3, -3), random.between(-140737488355328, 140737488355327) >= -140737488355328);`, "-3 true")
}

func TestTime(t *testing.T) {
	expect(t, "import std.time;", `
		println(time.now() > 1700000000000, time.seconds() > 1700000000, time.seconds() <= time.now() / 1000 + 1);
		let watch = time.stopwatch();
		let before = time.ticks();
		wait time.after(50.milliseconds());
		println(time.ticks() - before >= 50000, watch.microseconds() >= 50000, watch.milliseconds() >= 50);

		// Restarted, it counts from nothing again: well under what it had
		watch.restart();
		println(watch.milliseconds() < 50);
		wait time.after(0.milliseconds());
	`,
		"true true true",
		"true true true",
		"true",
	)
}

func TestIO(t *testing.T) {
	dir := t.TempDir()
	body := strings.ReplaceAll(`
		let path = "DIR/notes.txt";

		// Reading and writing give a Result: the value, or an error that
		// says what kind of failure it was
		println(io.exists(path), io.readFile(path).isErr());
		println(io.writeFile(path, "one\n"), io.appendFile(path, "two\n"), io.exists(path));
		println(io.readFile(path).map(text -> text.lines()), io.readFileLines(path));
		println(io.readFile(path).or("?") == "one\ntwo\n", io.readFile("DIR/missing.txt").or("fallback"));

		// What the error holds
		let missing = io.readFile("DIR/missing.txt").reason();
		if missing != null {
			println(missing.kind, missing.operation, missing.path == "DIR/missing.txt", missing.message);
			println(missing.describe() == "cannot read DIR/missing.txt: no such file or directory");
			// Where the program asked for the file
			println(missing.stack.len(), missing.stack[0].function, missing.stack[0].line > 0, missing.trace().contains("  at test.qi:"));
		}
		println(kindOf(io.readFile("DIR")), kindOf(io.writeFile("DIR/no/such/folder/file", "x")), kindOf(io.appendFile("DIR", "x")), kindOf(io.writeFile("DIR/notes.txt/inside", "x")));

		// The program acts on the kind
		match io.readFile("DIR/missing.txt") {
			case Result.Ok(text): { println("read", text); }
			case Result.Err(error): {
				match error.kind {
					case ErrorKind.NotFound: { println("not there yet"); }
					default: { println(error.describe()); }
				}
			}
		}

		// One failure does not leak into the next result
		println(io.readFile("DIR/missing.txt").isErr(), io.writeFile(path, "héλ").value(), io.readFile(path).reason() == null);

		// Removing gives the path, then why not once it is gone; a folder
		// goes only when it is empty
		println(io.remove(path).value() == path, io.remove(path).reason()?.describe() == "cannot remove ${path}: no such file or directory", io.exists(path));
		io.writeFile("DIR/kept.txt", "x").expect("a file");
		println(kindOf(io.remove("DIR")), io.remove("DIR").reason()?.operation, io.remove("DIR/kept.txt").isOk(), io.exists("DIR"));
		println(io.env("QOQI_NO_SUCH_VARIABLE"), io.env("PATH") != null);

		// Input: a question answered, then the rest line by line
		let name = io.ask("name? ");
		println(name ?? "nobody");
		let rest = io.readLines();
		println(rest, io.readLine(), io.ask("more? "));
	`, "DIR", dir)

	decls := `import std.io;
import std.io.{ErrorKind};
import std.strings;
import std.arrays;
import std.result.{Result};

fn kindOf<T>(r: Result<T, io.Error>) -> String {
	match r {
		case Result.Ok(_): { return "ok"; }
		case Result.Err(error): { return toString(error.kind); }
	}
}`
	out, err := run(t, decls, body, "ada\nfirst\n\nlast")
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := strings.Join([]string{
		`false true`,
		`Ok(4) Ok(4) true`,
		`Ok(["one", "two"]) Ok(["one", "two"])`,
		`true fallback`,
		`NotFound read true no such file or directory`,
		`true`,
		`1 main true true`,
		`IsDirectory NotFound IsDirectory NotDirectory`,
		`not there yet`,
		`true 3 true`,
		`true true false`,
		`AlreadyExists remove true true`,
		`null true`,
		`name? ada`,
		// The arguments of a call are worked out before it prints anything,
		// so the second question comes before the line it is part of
		`more? ["first", "", "last"] null null`,
		``,
	}, "\n")
	if out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
}

// TestLibraryErrorsPointAtTheCaller checks what a programmer sees when a
// call into the library fails: the function by the name they used, and
// the line of their own program.
func TestLibraryErrorsPointAtTheCaller(t *testing.T) {
	_, err := run(t, "import std.strings;", `
		let text = "abc";
		println(text.substring(2, 9));
	`, "")
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	for _, want := range []string{"std.strings.substring: range 2..9", "test.qi:5:", "in function 'main'", "text.substring(2, 9)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}

	// A failure inside a function of the library written in Qoqi is the
	// program's call of it, and names that function, not the library's
	// own lines
	_, err = run(t, "import std.arrays;", `[1].swap(0, 3);`, "")
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	for _, want := range []string{"R0002: std.arrays.swap: index out of bounds: 3 (length 1)", "test.qi:3:", "in function 'main'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "<std>/") {
		t.Errorf("expected no line of the library, got:\n%v", err)
	}

	// A private function of the library that refuses its arguments is
	// named as the public one the program called
	_, err = run(t, "import std.csv;", `csv.parseWith("a", "::");`, "")
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	for _, want := range []string{"R0007: std.csv.parseWith: the separator must be one character, got '::'", "test.qi:3:", "csv.parseWith("} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "std.csv.rows") || strings.Contains(err.Error(), "<std>/") {
		t.Errorf("expected nothing of the library's inside, got:\n%v", err)
	}

	// A panic of the library, at the program's call
	_, err = run(t, "import std.regex;", `let re = regex.must("(bad");
		println(re.pattern);`, "")
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	for _, want := range []string{"R0001: std.regex.must: '(bad' is not a regular expression", "test.qi:3:", `regex.must("(bad")`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "<std>/") {
		t.Errorf("expected no line of the library, got:\n%v", err)
	}

	// A function the program hands to the library fails at its own line
	_, err = run(t, "import std.arrays;", `let out = [1, 2].map(n -> [n][3]);
		println(out);`, "")
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	for _, want := range []string{"index out of bounds: 3 (length 1)", "test.qi:3:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "std.arrays.map:") {
		t.Errorf("expected the failure to stay the function's, got:\n%v", err)
	}
}

func TestSemver(t *testing.T) {
	imports := "import std.semver;\nimport std.semver.{Version};"

	// Reading versions: a v in front, parts left out, pre-release and build
	expect(t, imports, `
		for text in ["1.2.3", "v1.2.3", "V2", "1.4", "2.0.0-beta.3", "1.0.0+build.7", "1.0.0-rc.1+exp.sha.5114f85", "0.0.0"] {
			println(semver.parse(text).expect(text));
		}
		let v = semver.parse("3.1.4-alpha").expect("v");
		println(v.major, v.minor, v.patch, v.pre, v.isPrerelease(), v.release(), v.nextMajor(), v.nextMinor(), v.nextPatch());
	`, "1.2.3", "1.2.3", "2.0.0", "1.4.0", "2.0.0-beta.3", "1.0.0+build.7", "1.0.0-rc.1+exp.sha.5114f85", "0.0.0",
		`3 1 4 ["alpha"] true 3.1.4 4.0.0 3.2.0 3.1.5`)

	// What is not a version, and why
	expect(t, imports, `
		for text in ["", "abc", "1.2.3.4", "1..2", "1.x.2", "1.2.*", "1.-2.0", "1.2.3-", "1.2.3-a..b", "1.2.3+", "1.2.3-ü"] {
			match semver.parse(text) {
				case Result.Ok(v): { println("accepted", v); }
				case Result.Err(error): { println(error.describe()); }
			}
		}
	`, `"" is not a version: write it as 1.2.3`,
		`"abc" has "abc" where a number is expected`,
		`"1.2.3.4" is not a version: write it as 1.2.3`,
		`"1..2" has "" where a number is expected`,
		`"1.x.2" has a wildcard that is not its last part`,
		`"1.2.*" has a wildcard where a version is expected`,
		`"1.-2.0" has "" where a number is expected`,
		`"1.2.3-" has an empty or malformed pre-release part`,
		`"1.2.3-a..b" has an empty or malformed pre-release part`,
		`"1.2.3+" has an empty or malformed build part`,
		`"1.2.3-ü" has an empty or malformed pre-release part`)

	// Numbers with zeros in front are not versions (semver.org, item 2),
	// nor numbers of a pre-release (item 9); 0 alone is one, and words
	// may start with 0. An error prints as describe says
	expect(t, imports, `
		for text in ["01.2.3", "1.02.3", "1.2.00", "1.0.0-01", "1.0.0-alpha.007", "0.0.0", "1.0.0-0", "1.0.0-0a.10", "1.0.0+001", "^01.2"] {
			match semver.parse(text) {
				case Result.Ok(v): { println("accepted", v); }
				case Result.Err(error): { println(error); }
			}
		}
		println(semver.constraint("^01.2").reason());
	`, `"01.2.3" has "01" where a number is expected: a number has no zeros in front`,
		`"1.02.3" has "02" where a number is expected: a number has no zeros in front`,
		`"1.2.00" has "00" where a number is expected: a number has no zeros in front`,
		`"1.0.0-01" has "01" in its pre-release part: a number has no zeros in front`,
		`"1.0.0-alpha.007" has "007" in its pre-release part: a number has no zeros in front`,
		"accepted 0.0.0",
		"accepted 1.0.0-0",
		"accepted 1.0.0-0a.10",
		"accepted 1.0.0+001",
		`"^01.2" has "^01" where a number is expected`,
		`"^01.2" is not a constraint: "01.2" has "01" where a number is expected: a number has no zeros in front`)

	// The order of semver.org, item 11: 1.0.0-alpha < 1.0.0-alpha.1 <
	// 1.0.0-alpha.beta < 1.0.0-beta < 1.0.0-beta.2 < 1.0.0-beta.11 <
	// 1.0.0-rc.1 < 1.0.0; the build plays no part
	expect(t, imports, `
		let texts = ["1.0.0", "1.0.0-rc.1", "1.0.0-beta.11", "1.0.0-beta.2", "1.0.0-beta", "1.0.0-alpha.beta", "1.0.0-alpha.1", "1.0.0-alpha", "0.9.12", "0.10.0"];
		let ordered = semver.sorted(texts.map(text -> semver.parse(text).expect(text)));
		println(ordered.map(v -> toString(v)).join(" < "));
		let a = semver.parse("1.0.0+a").expect("a");
		let b = semver.parse("1.0.0+b").expect("b");
		println(a == b, a < b, semver.parse("2.0.0").expect("") > semver.parse("10.0.0").expect(""));
	`, "0.9.12 < 0.10.0 < 1.0.0-alpha < 1.0.0-alpha.1 < 1.0.0-alpha.beta < 1.0.0-beta < 1.0.0-beta.2 < 1.0.0-beta.11 < 1.0.0-rc.1 < 1.0.0",
		"true false false")
}

// Each constraint against the same versions: the ones it accepts, and the
// newest of them.
func TestSemverConstraints(t *testing.T) {
	imports := "import std.semver;\nimport std.semver.{Version};"
	versions := `["0.3.1", "0.3.9", "0.4.0", "0.0.3", "0.0.4", "1.0.0", "1.2.0", "1.2.3", "1.2.9", "1.3.0-rc.1", "1.4.0", "1.10.0", "2.0.0-beta.1", "2.0.0", "2.5.1", "3.0.0"]`
	body := `
		let all = ` + versions + `.map(text -> semver.parse(text).expect(text));
		for text in CONSTRAINTS {
			let wanted = semver.constraint(text).expect(text);
			let accepted = all.filter(v -> wanted.allows(v)).map(v -> toString(v)).join(" ");
			let best = semver.highest(all, wanted);
			let shown = if best == null { "none" } else { toString(best) };
			println("${text}: ${accepted} | ${shown}");
		}
	`
	cases := []struct{ constraint, want string }{
		{"1.2.3", "1.2.3 | 1.2.3"},
		{"=1.2.3", "1.2.3 | 1.2.3"},
		{"==1.2.3", "1.2.3 | 1.2.3"},
		{"v1.2.3", "1.2.3 | 1.2.3"},
		{"1.2", "1.2.0 1.2.3 1.2.9 | 1.2.9"},
		{"1", "1.0.0 1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"1.2.*", "1.2.0 1.2.3 1.2.9 | 1.2.9"},
		{"1.x", "1.0.0 1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"*", "0.3.1 0.3.9 0.4.0 0.0.3 0.0.4 1.0.0 1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 2.0.0 2.5.1 3.0.0 | 3.0.0"},
		{"^1.2", "1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"^1.2.3", "1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"^1", "1.0.0 1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"^0.3.1", "0.3.1 0.3.9 | 0.3.9"},
		{"^0.3", "0.3.1 0.3.9 | 0.3.9"},
		{"^0.0.3", "0.0.3 | 0.0.3"},
		{"^0", "0.3.1 0.3.9 0.4.0 0.0.3 0.0.4 | 0.4.0"},
		{"~1.2", "1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{"~1.2.3", "1.2.3 1.2.9 | 1.2.9"},
		{"~1", "1.0.0 1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{">=1.2 <2.0", "1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{">= 1.2, < 2.0", "1.2.0 1.2.3 1.2.9 1.4.0 1.10.0 | 1.10.0"},
		{">1.2.3 <=1.4.0", "1.2.9 1.4.0 | 1.4.0"},
		{">=1.0 !=1.2.3 <1.3", "1.0.0 1.2.0 1.2.9 | 1.2.9"},
		{"1.0 - 1.2", "1.0.0 1.2.0 1.2.3 1.2.9 | 1.2.9"},
		{"1.0.0 - 1.2.3", "1.0.0 1.2.0 1.2.3 | 1.2.3"},
		{"^0.3 || ^2.0", "0.3.1 0.3.9 2.0.0 2.5.1 | 2.5.1"},
		{"^0.3 | ~1.2.3 | 3.0.0", "0.3.1 0.3.9 1.2.3 1.2.9 3.0.0 | 3.0.0"},
		{"<0.1", "0.0.3 0.0.4 | 0.0.4"},
		{">3", " | none"},
		// A pre-release only for a constraint that names one of the same version
		{"^2.0.0-beta", "2.0.0-beta.1 2.0.0 2.5.1 | 2.5.1"},
		{">=2.0.0-alpha <2.0.0", "2.0.0-beta.1 | 2.0.0-beta.1"},
		{"1.3.0-rc.1", "1.3.0-rc.1 | 1.3.0-rc.1"},
		{">=1.3.0-rc.1", "1.3.0-rc.1 1.4.0 1.10.0 2.0.0 2.5.1 3.0.0 | 3.0.0"},
	}
	var list, want []string
	for _, c := range cases {
		list = append(list, `"`+c.constraint+`"`)
		want = append(want, c.constraint+": "+c.want)
	}
	expect(t, imports+"\nconst CONSTRAINTS = ["+strings.Join(list, ", ")+"];", body, want...)

	// What is not a constraint
	expect(t, imports, `
		for text in ["", "^", "~", ">=", "1.2 ||", "|| 1.2", ">=1.*", "^abc", "1.0 - ", "dev-main"] {
			match semver.constraint(text) {
				case Result.Ok(c): { println("accepted", c); }
				case Result.Err(error): { println(error.describe()); }
			}
		}
	`, `"" has an empty alternative`,
		`"^" is not a constraint: "" is not a version: write it as 1.2.3`,
		`"~" is not a constraint: "" is not a version: write it as 1.2.3`,
		`">=" is not a constraint: "" is not a version: write it as 1.2.3`,
		`"1.2 ||" has an empty alternative`,
		`"|| 1.2" has an empty alternative`,
		`">=1.*" has a wildcard after a comparison`,
		`"^abc" is not a constraint: "abc" has "abc" where a number is expected`,
		`"1.0 - " is not a constraint: "-" has an empty or malformed pre-release part`,
		`"dev-main" has "dev" where a number is expected`)

	// The constraint a new requirement is written with
	expect(t, imports, `
		for text in ["1.4.2", "2.0.0", "0.3.1", "0.0.7"] {
			println(semver.caret(semver.parse(text).expect(text)));
		}
		println(semver.constraint(" ^1.2 ").expect("").show(), semver.constraint("*").expect("").isAny(), semver.constraint("^1").expect("").isAny());
	`, "^1.4", "^2.0", "^0.3.1", "^0.0.7", "^1.2 true false")
}

// reduce combines the items two by two, from the first, and has nothing
// to give for an empty array.
func TestArraysReduce(t *testing.T) {
	expect(t, "struct Money { cents: Int; }\nimpl Money { fn plus(other: Money) -> Money { return Money { cents: self.cents + other.cents }; } }", `
		println([3, 9, 4].reduce((a, b) -> a > b ? a : b), ["a", "b", "c"].reduce((a, b) -> a + b));
		let none: [Int] = [];
		println(none.reduce((a, b) -> a + b), [7].reduce((a, b) -> a * b));
		println([Money { cents: 1 }, Money { cents: 2 }].reduce(Money.plus)?.cents);
	`,
		"9 abc",
		"null 7",
		"3",
	)
}

// % works on Floats as on Ints, its sign that of the number divided, and
// on a whole number with a Float as on two Floats.
func TestFloatRemainder(t *testing.T) {
	expect(t, "", `
		let angle = 370.5;
		angle %= 360.0;
		println(7.5 % 2.0, -7.5 % 2.0, 7 % 2.5, 7.5 % 2, angle);
	`,
		"1.5 -1.5 2 1.5 10.5",
	)
}
