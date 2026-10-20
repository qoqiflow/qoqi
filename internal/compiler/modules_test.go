package compiler

import (
	"bytes"
	"fmt"
	"github.com/qoqiflow/qoqi/internal/ast"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// sources is a program of several files held in memory. The entry file is
// main.qi.
type sources map[string]string

func (s sources) read(path string) (string, error) {
	src, ok := s[path]
	if !ok {
		return "", fmt.Errorf("no such file: %s", path)
	}
	return src, nil
}

// compileModules compiles a program of several files.
func compileModules(fs sources) (*Result, *errors.Handler) {
	errs := errors.NewHandler()
	return CompileWith(fs["main.qi"], "main.qi", fs.read, errs), errs
}

// runModules compiles and runs a program of several files, returning what
// it printed and the runtime error, if any.
func runModules(t *testing.T, fs sources) (string, error) {
	t.Helper()

	result, errs := compileModules(fs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	_, err := machine.Exec(result.Chunk)
	return out.String(), err
}

func expectModulesOutput(t *testing.T, fs sources, want string) {
	t.Helper()
	out, err := runModules(t, fs)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
}

// expectModulesError compiles a program that must be rejected and checks
// the first diagnostic.
func expectModulesError(t *testing.T, fs sources, code, file, msg string) {
	t.Helper()
	result, errs := compileModules(fs)
	if !errs.HasErrors() {
		t.Fatal("expected a diagnostic, got none")
	}
	if result.Chunk != nil {
		t.Error("expected no bytecode for a rejected program")
	}
	d := errs.Diagnostics[0]
	if d.Code != code || d.Pos.File != file || !strings.Contains(d.Message, msg) {
		t.Errorf("expected %s in %s containing %q, got %s in %s: %s", code, file, msg, d.Code, d.Pos.File, d.Message)
	}
	if d.Context == "" || !strings.Contains(fs[d.Pos.File], d.Context) {
		t.Errorf("expected the diagnostic to quote a line of %s, got %q", d.Pos.File, d.Context)
	}
}

// A small shop: stock, a cart built on a generic list, pricing rules given
// as traits, and a report. Each file uses the others through a different
// form of import.
var shop = sources{
	"main.qi": `
		import shop.stock;
		import shop.cart.{Cart, newCart};
		import shop.pricing as rules;
		import shop.report;

		// A rule of the program's own, implementing a trait of a module
		struct Loyalty { points: Int; }
		impl rules.Rule for Loyalty {
			fn apply(total: Int) -> Int { return total - self.points; }
			fn label() -> String { return "loyalty"; }
		}

		fn main() -> Int {
			let store = stock.open();
			store.add("apple", 30, 10);
			store.add("pear", 45, 2);
			store.add("fig", 120, 0);

			let cart: Cart = newCart(store);
			println(cart.take("apple", 4), cart.take("pear", 3), cart.take("fig", 1), cart.take("kiwi", 1));

			let applied: [rules.Rule] = [rules.percent(10), Loyalty { points: 7 }, rules.minimum(50)];
			println(report.summary(cart, applied));
			println(report.lines(cart));
			println(store.left("apple"), store.left("pear"));
			return 0;
		}
	`,
	"shop/stock.qi": `
		import util.list.{List, newList};

		pub struct Item { name: String; price: Int; count: Int; }
		pub struct Stock { items: List<Item>; }

		pub fn open() -> Stock { return Stock { items: newList<Item>() }; }

		impl Stock {
			pub fn add(name: String, price: Int, count: Int) {
				self.items.add(Item { name: name, price: price, count: count });
			}
			pub fn find(name: String) -> Item? {
				return self.items.first(fn(item: Item) -> Bool { return item.name == name; });
			}
			pub fn left(name: String) -> Int {
				let item = self.find(name);
				if item == null { return 0; }
				return item.count;
			}
		}
	`,
	"shop/cart.qi": `
		import shop.stock;
		import util.list;

		pub struct Line { item: stock.Item; count: Int; }
		pub struct Cart { store: stock.Stock; lines: list.List<Line>; }

		pub fn newCart(store: stock.Stock) -> Cart {
			return Cart { store: store, lines: list.newList<Line>() };
		}

		impl Cart {
			// Takes what the stock has; returns how many were taken
			pub fn take(name: String, wanted: Int) -> Int {
				let item = self.store.find(name);
				if item == null { return 0; }
				let count = wanted;
				if item.count < count { count = item.count; }
				if count == 0 { return 0; }
				item.count -= count;
				self.lines.add(Line { item: item, count: count });
				return count;
			}
			pub fn total() -> Int {
				return self.lines.fold<Int>(0, fn(sum: Int, line: Line) -> Int {
					return sum + line.item.price * line.count;
				});
			}
		}
	`,
	"shop/pricing.qi": `
		pub trait Rule {
			fn apply(total: Int) -> Int;
			fn label() -> String;
		}

		struct Percent { off: Int; }
		impl Rule for Percent {
			fn apply(total: Int) -> Int { return total - total * self.off / 100; }
			fn label() -> String { return toString(self.off) + "% off"; }
		}

		struct Minimum { floor: Int; }
		impl Rule for Minimum {
			fn apply(total: Int) -> Int { if total < self.floor { return self.floor; } return total; }
			fn label() -> String { return "at least " + toString(self.floor); }
		}

		// The structs stay private; only the trait leaves the module
		pub fn percent(off: Int) -> Rule { return Percent { off: off }; }
		pub fn minimum(floor: Int) -> Rule { return Minimum { floor: floor }; }
	`,
	"shop/report.qi": `
		import shop.cart.{Cart, Line};
		import shop.pricing.{Rule};
		import util.list.{List};

		pub fn summary(cart: Cart, rules: [Rule]) -> String {
			let total = cart.total();
			let text = toString(total);
			for rule in rules {
				total = rule.apply(total);
				text += " -> " + rule.label() + ": " + toString(total);
			}
			return text;
		}

		pub fn lines(cart: Cart) -> [String] {
			let names: List<String> = cart.lines.map<String>(describe);
			return names.items;
		}

		fn describe(line: Line) -> String {
			return toString(line.count) + " x " + line.item.name;
		}
	`,
	"util/list.qi": `
		import std.arrays;

		pub struct List<T> { items: [T]; }

		pub fn newList<T>() -> List<T> { return List<T> { items: [] }; }

		impl<T> List<T> {
			pub fn add(value: T) { self.items.push(value); }
			pub fn size() -> Int { return self.items.len(); }
			pub fn first(wanted: fn(T) -> Bool) -> T? {
				for item in self.items {
					if wanted(item) { return item; }
				}
				return null;
			}
			pub fn map<U>(f: fn(T) -> U) -> List<U> {
				let out = newList<U>();
				for item in self.items { out.add(f(item)); }
				return out;
			}
			pub fn fold<A>(start: A, f: fn(A, T) -> A) -> A {
				let acc = start;
				for item in self.items { acc = f(acc, item); }
				return acc;
			}
		}
	`,
}

func TestModulesProgram(t *testing.T) {
	// apple 4 x 30 + pear 2 x 45 = 210; 10% off = 189; -7 = 182; floor 50
	expectModulesOutput(t, shop, `4 2 0 0
210 -> 10% off: 189 -> loyalty: 182 -> at least 50: 182
["4 x apple", "2 x pear"]
6 0
`)
}

func TestModulesKeepSameNamesApart(t *testing.T) {
	// Three structs called Node, three functions called make. Which is
	// which must hold at compile time and when the program tests a value.
	expectModulesOutput(t, sources{
		"main.qi": `
			import a;
			import b;

			struct Node { own: Int; }
			fn make() -> Node { return Node { own: 3 }; }

			fn which(value: Any) -> String {
				match value {
					case a.Node: { return "a:" + toString(value.value); }
					case b.Node: { return "b:" + toString(value.size); }
					case Node: { return "main:" + toString(value.own); }
					default: { return "other"; }
				}
			}

			fn main() -> Int {
				let values: [Any] = [a.make(), b.make(), make(), 4];
				for value in values { print(which(value), ""); }
				println();

				let any: Any = b.make();
				println(any is a.Node, any is b.Node, any is Node);
				println((any as b.Node).size, a.describe(a.make()), b.describe(any));
				return 0;
			}
		`,
		"a.qi": `
			pub struct Node { value: Int; }
			pub fn make() -> Node { return Node { value: 1 }; }
			pub fn describe(value: Any) -> String { if value is Node { return "a node"; } return "not a node of a"; }
		`,
		"b.qi": `
			pub struct Node { size: Int; }
			pub fn make() -> Node { return Node { size: 2 }; }
			pub fn describe(value: Any) -> String { if value is Node { return "b node"; } return "not a node of b"; }
		`,
	}, "a:1 b:2 main:3 other \nfalse true false\n2 a node b node\n")

	// A cast to the wrong Node fails, naming both
	_, err := runModules(t, sources{
		"main.qi": `
			import a;
			import b;
			fn main() -> Int { let any: Any = b.make(); return (any as a.Node).value; }
		`,
		"a.qi": `pub struct Node { value: Int; }`,
		"b.qi": `pub struct Node { size: Int; } pub fn make() -> Node { return Node { size: 2 }; }`,
	})
	if err == nil || !strings.Contains(err.Error(), "a.Node") || !strings.Contains(err.Error(), "b.Node") {
		t.Errorf("expected a failed cast naming a.Node and b.Node, got %v", err)
	}
}

func TestModulesTypesAreChecked(t *testing.T) {
	lib := `
		pub struct Point { x: Int; }
		pub trait Shape { fn area() -> Int; }
		pub fn make(x: Int) -> Point { return Point { x: x }; }
		pub fn first<T>(items: [T]) -> T { return items[0]; }
	`
	other := `pub struct Point { y: Int; } pub fn make() -> Point { return Point { y: 1 }; }`
	entry := func(body string) sources {
		return sources{
			"main.qi":  "import lib;\nimport other;\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n",
			"lib.qi":   lib,
			"other.qi": other,
		}
	}

	tests := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"argument type", `let p = lib.make("one");`, "E2010", "cannot use type String as argument of type Int"},
		{"argument count", `let p = lib.make(1, 2);`, "E2009", "expected 1 arguments, got 2"},
		{"structs of two modules", `let p: lib.Point = other.make();`, "E2001", "other.Point"},
		{"unknown field", `let p = lib.make(1); println(p.y);`, "E2014", "lib.Point has no field 'y'"},
		{"field type", `let p = lib.Point { x: "one" };`, "E2001", "String"},
		{"missing field", `let p = lib.Point {};`, "E2018", "struct literal of lib.Point is missing 'x'"},
		{"trait not implemented", `let s: lib.Shape = lib.make(1);`, "E2001", "lib.Shape"},
		{"generic result", `let s: String = lib.first([1, 2]);`, "E2001", "String"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := compileModules(entry(tt.body))
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code || !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected %s containing %q, got %s: %s", tt.code, tt.msg, d.Code, d.Message)
			}
		})
	}

	// The accepted counterparts
	expectModulesOutput(t, entry(`
		let p: lib.Point = lib.make(1);
		let q: other.Point = other.make();
		let r = lib.Point { x: lib.first([5, 6]) };
		println(p.x, q.y, r.x, lib.first<String>(["a"]));
	`), "1 1 5 a\n")
}

func TestModulesMethodVisibility(t *testing.T) {
	account := `
		pub struct Account { balance: Int; }
		pub trait Printable { fn text() -> String; }
		pub fn open() -> Account { return Account { balance: 0 }; }

		impl Account {
			pub fn deposit(amount: Int) -> Int {
				self.check(amount);
				self.balance += amount;
				return self.balance;
			}
			fn check(amount: Int) { if amount < 0 { self.balance = 0; } }
		}
		impl Printable for Account {
			fn text() -> String { return "balance " + toString(self.balance); }
		}
		// A private method is usable anywhere in its module, lambdas included
		pub fn audit(a: Account) -> Int {
			let run = fn() -> Int { a.check(1); return a.balance; };
			return run();
		}
	`
	entry := func(body string) sources {
		return sources{
			"main.qi": `
				import bank.account;
				import bank.account.{Account};
				impl Account {
					fn doubled() -> Int { return self.balance * 2; }
				}
				fn main() -> Int {` + body + `
					return 0;
				}`,
			"bank/account.qi": account,
			"bank/teller.qi": `
				import bank.account.{Account};
				pub fn peek(a: Account) -> Int { return a.doubled(); }
			`,
		}
	}

	// pub methods, trait methods, and the entry file's own method on a
	// struct of another module
	expectModulesOutput(t, entry(`
		let a = account.open();
		println(a.deposit(5), a.text(), a.doubled(), account.audit(a));
	`), "5 balance 5 10 5\n")

	// A method without pub stays in its module
	expectModulesError(t, entry(`let a = account.open(); a.check(1);`),
		"E2027", "main.qi", "method 'check' of bank.account.Account is private")

	// The same holds for a method the entry file added: another module
	// cannot use it
	fs := entry("")
	fs["main.qi"] = "import bank.teller;\n" + fs["main.qi"]
	expectModulesError(t, fs, "E2027", "bank/teller.qi", "method 'doubled' of bank.account.Account is private")
}

func TestModulesDiagnosticsNameTheirFile(t *testing.T) {
	// A type error deep in a module is reported in that file, with its line
	fs := sources{
		"main.qi": "import geo.area;\n\n\nfn main() -> Int { return area.square(2); }\n",
		"geo/area.qi": "import geo.units;\n" +
			"pub fn square(side: Int) -> Int {\n" +
			"    let label: Int = units.name();\n" +
			"    return side * side;\n" +
			"}\n",
		"geo/units.qi": "pub fn name() -> String { return \"cm\"; }\n",
	}
	_, errs := compileModules(fs)
	if len(errs.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %v", errs.Diagnostics)
	}
	d := errs.Diagnostics[0]
	if d.Code != "E2001" || d.Pos.File != "geo/area.qi" || d.Pos.Line != 3 {
		t.Errorf("expected E2001 at geo/area.qi:3, got %s at %s", d.Code, d.Pos)
	}
	if d.Context != "    let label: Int = units.name();" {
		t.Errorf("expected the line of geo/area.qi, got %q", d.Context)
	}

	// Name resolution, in another file
	expectModulesError(t, sources{
		"main.qi": "import a;\nfn main() -> Int { return a.f(); }",
		"a.qi":    "\n\npub fn f() -> Int { return undefinedThing; }",
	}, "E1001", "a.qi", "undefinedThing")

	// A missing return, in another file
	expectModulesError(t, sources{
		"main.qi": "import a;\nfn main() -> Int { return a.f(1); }",
		"a.qi":    "pub fn f(n: Int) -> Int {\n    if n > 0 { return 1; }\n}",
	}, "E2004", "a.qi", "function 'a.f' can reach its end without returning a value")
}

func TestModulesRuntimeErrorsNameTheirFile(t *testing.T) {
	_, err := runModules(t, sources{
		"main.qi": `
			import data.table;
			fn main() -> Int {
				let t = table.of([1, 2, 3]);
				println(t.at(1));
				return t.at(7);
			}
		`,
		"data/table.qi": "pub struct Table { cells: [Int]; }\n" +
			"pub fn of(cells: [Int]) -> Table { return Table { cells: cells }; }\n" +
			"impl Table {\n" +
			"    pub fn at(i: Int) -> Int {\n" +
			"        return self.cells[i];\n" +
			"    }\n" +
			"}\n",
	})
	if err == nil {
		t.Fatal("expected an out-of-bounds error")
	}
	for _, want := range []string{"index out of bounds: 7 (length 3)", "data/table.qi:5", "return self.cells[i];", "data.table.Table.at"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got:\n%v", want, err)
		}
	}
}

func TestModulesEntryPoint(t *testing.T) {
	// A module may have a function called main; the program starts in the
	// entry file's
	expectModulesOutput(t, sources{
		"main.qi": `
			import tool;
			fn main() -> Int { println("entry", tool.main()); return 0; }
		`,
		"tool.qi": `pub fn main() -> Int { return 7; }`,
	}, "entry 7\n")

	// ...and a module's main does not make up for a missing one
	_, errs := compileModules(sources{
		"main.qi": "import tool;\nfn helper() -> Int { return tool.main(); }",
		"tool.qi": `pub fn main() -> Int { return 7; }`,
	})
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E6006" {
		t.Errorf("expected the program to lack an entry point, got %v", errs.Diagnostics)
	}
}

func TestModulesFunctionsAreNamedInBytecode(t *testing.T) {
	result, errs := compileModules(sources{
		"main.qi":     "import geo.area;\nfn main() -> Int { return area.square(3); }",
		"geo/area.qi": "pub fn square(side: Int) -> Int { return side * side; }",
	})
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}

	idx := result.Chunk.FindPrototype("geo.area.square")
	if idx < 0 {
		t.Fatal("expected the function to be named geo.area.square in the bytecode")
	}
	info := result.Chunk.GetDebugInfo(result.Chunk.Prototypes[idx].CodeStart)
	if info == nil || info.FilePath != "geo/area.qi" || !strings.Contains(info.SourceLine, "side * side") {
		t.Errorf("expected debug info pointing into geo/area.qi, got %+v", info)
	}

	value, err := vm.NewVM().Exec(result.Chunk)
	if err != nil || !value.IsInt() || value.AsInt() != 9 {
		t.Errorf("expected 9, got %s, %v", vm.FormatValue(value), err)
	}
}

func TestModulesUnusedModuleStillChecked(t *testing.T) {
	// A module that is imported is compiled whole, used or not
	expectModulesError(t, sources{
		"main.qi": "import a;\nfn main() -> Int { return 0; }",
		"a.qi":    "pub fn f() -> Int { return \"text\"; }",
	}, "E2003", "a.qi", "cannot return type String")
}

func TestModulesImplementationsAreGlobal(t *testing.T) {
	lib := `
		pub struct Point { x: Int; }
		pub trait Named { fn name() -> String; }
		impl Point { pub fn double() -> Int { return self.x * 2; } }
		impl Named for Point { fn name() -> String { return "point"; } }
		pub fn make() -> Point { return Point { x: 2 }; }
	`
	// A second method of the same name, from another module
	_, errs := compileModules(sources{
		"main.qi": "import lib;\nimpl lib.Point { fn double() -> Int { return 0; } }\nfn main() -> Int { return 0; }",
		"lib.qi":  lib,
	})
	if !errs.HasErrors() || errs.Diagnostics[0].Code != "E2019" {
		t.Errorf("expected a duplicate method error, got %v", errs.Diagnostics)
	}

	// A method added by another module, and a trait of the program
	// implemented for a struct of a module
	expectModulesOutput(t, sources{
		"main.qi": `
			import lib;
			trait Sized { fn size() -> Int; }
			impl Sized for lib.Point { fn size() -> Int { return self.x + self.triple(); } }
			impl lib.Point { fn triple() -> Int { return self.x * 3; } }
			fn main() -> Int {
				let p = lib.make();
				let s: Sized = p;
				let n: lib.Named = p;
				println(p.double(), p.triple(), s.size(), n.name());
				return 0;
			}
		`,
		"lib.qi": lib,
	}, "4 6 8 point\n")
}

func TestExternFunctions(t *testing.T) {
	// An extern function the runtime does not have is a compile error,
	// wherever it is declared and whether or not it is called
	expectModulesError(t, sources{
		"main.qi": "extern fn launch(code: Int) -> Bool;\nfn main() -> Int { return 0; }",
	}, "E7002", "main.qi", "the runtime has no function 'launch'")
	expectModulesError(t, sources{
		"main.qi":     "import hw.motor;\nfn main() -> Int { return 0; }",
		"hw/motor.qi": "\npub extern fn spin(speed: Int);\n",
	}, "E7002", "hw/motor.qi", "the runtime has no function 'hw.motor.spin'")
	// Declaring a library function under another module's name does not reach it
	expectModulesError(t, sources{
		"main.qi": "extern fn sqrt(x: Float) -> Float;\nfn main() -> Int { return 0; }",
	}, "E7002", "main.qi", "the runtime has no function 'sqrt'")

	// Calls of extern functions are type checked against their declaration
	lib := sources{"main.qi": ""}
	check := func(body, code, msg string) {
		t.Helper()
		lib["main.qi"] = "import std.math;\nimport std.strings;\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n"
		expectModulesError(t, lib, code, "main.qi", msg)
	}
	check(`let r = strings.fromCode("4");`, "E2010", "cannot use type String as argument of type Int")
	check(`let r = strings.fromCode(4, 2);`, "E2009", "expected 1 arguments, got 2")
	check(`let r: Int = strings.fromCode(65);`, "E2001", "String")
	check(`let n: Int = "abc".indexOf("b");`, "E2001", "Int?")
	check(`let n = "abc".indexOf("b") + 1;`, "E2017", "the value may be null")
	check(`let s: String = "a b".split(" ");`, "E2001", "[String]")
	check(`let r = "2".floor();`, "E2015", "a text has no method 'floor'")
	check(`let r = 4.0.sqrt(2.0);`, "E2009", "expected 0 arguments, got 1")
	check(`let r = math.nothing(2);`, "E8002", "module 'std.math' has no 'nothing'")
	// What a method of the library is made of is the library's own
	check(`let r = strings.trim(" a ");`, "E8003", "function 'trim' of module 'std.strings' is private")
	check(`let r = math.sqrt(4.0);`, "E8003", "function 'sqrt' of module 'std.math' is private")

	// An extern function is a value like any function
	expectModulesOutput(t, sources{"main.qi": `
		import std.strings;
		import std.strings.{fromCode};

		fn each(codes: [Int], f: fn(Int) -> String) -> [String] {
			let out: [String] = [];
			for code in codes { out.push(f(code)); }
			return out;
		}
		fn main() -> Int {
			let make = strings.fromCode;
			println(make(65), each([72, 105], fromCode), each([33], make));
			let table = [strings.fromCode, fromCode, make];
			for f in table { print(f(66), ""); }
			println();
			return 0;
		}
	`}, "A [\"H\", \"i\"] [\"!\"]\nB B B \n")
}

func TestStandardLibraryIsAPackage(t *testing.T) {
	// A folder named std in the program is not where std.* is looked for
	expectModulesOutput(t, sources{
		"main.qi":     "import std.math;\nfn main() -> Int { println((-3).abs()); return 0; }",
		"std/math.qi": "pub fn abs(n: Int) -> Int { return 999; }",
	}, "3\n")

	// A module the library does not have
	_, errs := compileModules(sources{"main.qi": "import std.network;\nfn main() -> Int { return 0; }"})
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != "E8001" || !strings.Contains(errs.Diagnostics[0].Suggestion, "'std' is a package") {
		t.Errorf("expected a missing module of the package std, got %v", errs.Diagnostics)
	}

	// A program can bring its own package std; then that one is used, whole
	fs := sources{
		"main.qi":      "import std.math;\nfn main() -> Int { println(math.abs(-3)); return 0; }",
		"mine/math.qi": "pub fn abs(n: Int) -> Int { return 42; }",
	}
	errs = errors.NewHandler()
	result := CompileOptions(fs["main.qi"], "main.qi", Options{Read: fs.read, Packages: map[string]string{"std": "mine"}}, errs)
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil || out.String() != "42\n" {
		t.Errorf("expected the program's own std to be used, got %q, %v", out.String(), err)
	}

	// A type error in the library's own source would name its file; here,
	// a misuse found inside the library's generic code after instantiation
	// still points at the caller's line
	expectModulesError(t, sources{
		"main.qi": "import std.arrays;\nfn main() -> Int {\n    let n: String = [1, 2].sum();\n    return 0;\n}\n",
	}, "E2001", "main.qi", "Int")
}

func TestMethodsOnValuesFromNestedGenerics(t *testing.T) {
	// The value a generic method returns is itself a generic struct, whose
	// methods are called in turn. Several structs share the method names.
	expectModulesOutput(t, sources{"main.qi": `
		import std.list;
		import std.dict;
		import std.dict.{Dict};
		import std.list.{List};

		struct Box<V> { value: V; }
		impl<V> Box<V> {
			fn get() -> V { return self.value; }
			fn maybe() -> V? { return self.value; }
		}

		fn main() -> Int {
			let inner = Box<Int> { value: 7 };
			let outer = Box<Box<Int>> { value: inner };
			let a = outer.get();
			let b = outer.maybe();
			if b != null { println(a.get(), b.get(), outer.get().get()); }

			// A map of maps, a map of lists, a list of maps
			let scores = dict.empty<Dict<Int>>();
			let maths = dict.empty<Int>();
			maths.set("ada", 19);
			scores.set("maths", maths);
			let found = scores.get("maths");
			if found != null {
				found.set("bob", 12);
				println(found.get("ada"), found.keys(), found.len());
			}

			let groups = dict.empty<List<String>>();
			groups.set("fruit", list.of(["fig"]));
			let fruit = groups.get("fruit");
			if fruit != null {
				fruit.add("pear");
				println(fruit.get(1), fruit.len(), fruit.toArray());
			}

			let rows = list.empty<Dict<Int>>();
			rows.add(maths);
			let row = rows.get(0);
			if row != null { println(row.get("bob"), row.has("zed")); }

			let boxed = Box<List<Int>> { value: list.of([1, 2, 3]) };
			println(boxed.get().len(), boxed.get().get(2), boxed.get().map<Int>(fn(n: Int) -> Int { return n * n; }).toArray());
			return 0;
		}
	`}, "7 7 7\n19 [\"ada\", \"bob\"] 2\npear 2 [\"fig\", \"pear\"]\n12 false\n3 3 [1, 4, 9]\n")
}

func TestLengthIsAFunctionOfTheLibrary(t *testing.T) {
	// Arrays and texts have no members; their length is asked with a function
	entry := func(body string) sources {
		return sources{"main.qi": "import std.arrays;\nimport std.strings;\nstruct Rope { length: Int; items: [Int]; }\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n"}
	}

	rejected := []struct{ name, body, msg, suggestion string }{
		{"array", `let xs = [1, 2]; println(xs.length);`,
			"an array has no members: '.length' is not part of the language", "Write xs.len(), with: import std.arrays;"},
		{"text", `let name = "ada"; println(name.length);`,
			"a text has no members: '.length' is not part of the language", "Write name.len(), with: import std.strings;"},
		{"array in a field", `let r = Rope { length: 1, items: [1] }; println(r.items.length);`,
			"an array has no members", "Write r.items.len()"},
		{"result of a call", `println("a b".words().length);`,
			"an array has no members", "Call .len() on it, with: import std.arrays;"},
		{"literal", `println("abc".length);`,
			"a text has no members", `Write "abc".len()`},
		{"in a condition", `let xs = [1]; let i = 0; while i < xs.length { i++; }`,
			"an array has no members", "Write xs.len()"},
		{"in a lambda", `let f = fn(s: String) -> Int { return s.length; };`,
			"a text has no members", "Write s.len()"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := compileModules(entry(tt.body))
			if len(errs.Diagnostics) != 1 {
				t.Fatalf("expected one diagnostic, got %v", errs.Diagnostics)
			}
			d := errs.Diagnostics[0]
			if d.Code != "E2033" || !strings.Contains(d.Message, tt.msg) || !strings.Contains(d.Suggestion, tt.suggestion) {
				t.Errorf("expected E2033 %q with suggestion %q, got %s: %s (%s)", tt.msg, tt.suggestion, d.Code, d.Message, d.Suggestion)
			}
		})
	}

	// A module of the program is no more allowed than its entry file
	expectModulesError(t, sources{
		"main.qi": "import util;\nfn main() -> Int { return util.size([1]); }",
		"util.qi": "pub fn size(xs: [Int]) -> Int { return xs.length; }",
	}, "E2033", "util.qi", "an array has no members")

	// A struct's own field called length is a field like any other
	expectModulesOutput(t, entry(`
		let r = Rope { length: 7, items: [1, 2, 3] };
		r.length = r.length + 1;
		println(r.length, r.items.len());
	`), "8 3\n")

	// The functions, on every kind of value they take, and as values
	expectModulesOutput(t, entry(`
		let rows = [[1, 2, 3], [], [4]];
		println(rows.len(), rows[0].len(), [].len(), ["a"].len(), [1.5, 2.5].len());
		println("flows".len(), "".len(), "héλ".len(), ("a" + "b").len());
		println(rows.map(fn(row: [Int]) -> Int { return row.len(); }), ["a", "bcd"].map(text -> text.len()));
		let count = fn(text: String) -> Int { return text.len(); };
		println(count("four"), "abc".chars().len());

		// In the places a length is most used
		let total = 0;
		let i = 0;
		while i < rows.len() {
			let j = 0;
			while j < rows[i].len() { total += rows[i][j]; j++; }
			i++;
		}
		println(total, rows[rows.len() - 1]);
	`), "3 3 0 1 2\n5 0 3 2\n[3, 0, 1] [1, 3]\n4 3\n10 [4]\n")

	// A local called arrays or strings hides the module, as any local does
	expectModulesError(t, entry(`let arrays = 5; println(arrays.range(0, 3));`), "E2015", "main.qi", "a number has no method 'range'")
}

func TestLengthCompilesToOneInstruction(t *testing.T) {
	// Asking for a length is as cheap as it was when it was a member: the
	// call is replaced by the instruction, in loops included
	result, errs := compileModules(sources{"main.qi": `
		import std.arrays;
		import std.strings;
		fn main() -> Int {
			let xs = [1, 2, 3];
			let i = 0;
			while i < xs.len() { i++; }
			return i + "ab".len() + ["x"].len();
		}
	`})
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}

	var main string
	for _, fn := range result.IR.Functions {
		if fn.Name == "main" {
			main = fn.Dump()
		}
	}
	if strings.Count(main, "LEN") != 3 {
		t.Errorf("expected three length instructions in main, got:\n%s", main)
	}
	if strings.Contains(main, "std.arrays.len") || strings.Contains(main, "std.strings.len") {
		t.Errorf("expected no call of the length functions in main, got:\n%s", main)
	}

	value, err := vm.NewVM().Exec(result.Chunk)
	if err != nil || !value.IsInt() || value.AsInt() != 6 {
		t.Errorf("expected 6, got %s, %v", vm.FormatValue(value), err)
	}
}

// A range kept as a value is a Range of std.ops, in any module and without
// an import; the names the program gives its own things are still its own.
func TestRangesAsValuesInModules(t *testing.T) {
	expectModulesOutput(t, sources{
		"main.qi": `
			import ops;
			import calendar;
			import calendar.{Range};

			fn main() {
				// The program's own Range, and its own module called ops
				let mine = Range { label: "week" };
				println(mine.label, ops.double(21));

				// The library's, through a module that keeps ranges
				let days = calendar.workdays();
				println(days.len(), days.contains(5), calendar.weekend().toArray());
				let kept = 1..4;
				println(kept, kept.reversed().toArray(), calendar.total(kept.step(2)));
			}
		`,
		"ops.qi": `pub fn double(n: Int) -> Int { return n * 2; }`,
		"calendar.qi": `
			import std.ops;

			pub struct Range { label: String; }

			// Named in full where the module has a Range of its own
			pub fn workdays() -> ops.Range { return 1..=5; }
			pub fn weekend() -> ops.Range { return (7..5).step(-1); }
			pub fn total(days: ops.Range) -> Int {
				let sum = 0;
				for day in days { sum += day; }
				return sum;
			}
		`,
	}, "week 42\n5 true [7, 6]\n1..4 [3, 2, 1] 4\n")

	// What is wrong with a range that is kept is said of a range
	expectModulesError(t, sources{
		"main.qi": "fn main() { let r = 1..2.5; }",
	}, "E2038", "main.qi", "the bounds of a range are whole numbers, this is Float")
	expectModulesError(t, sources{
		"main.qi": "fn main() { let n: Int = 1..5; }",
	}, "E2001", "main.qi", "Range")
	expectModulesError(t, sources{
		"main.qi": "fn main() { let r = 1..5; r.grow(); }",
	}, "E2015", "main.qi", "Range has no method 'grow'")
}

// Methods a module gives a builtin type are the type's wherever the module
// is part of the program: public ones for everyone, the others for the
// module alone.
func TestMethodsOnBuiltinTypesAcrossModules(t *testing.T) {
	files := sources{
		"main.qi": `
			import geometry;
			import geometry.{Point};
			import text;

			fn main() {
				let points = [Point { x: 3, y: 4 }, Point { x: 9, y: 1 }];
				println(points.widest(), points.second()?.x, [1, 2, 3].second(), "ada lovelace".initials(), 5.doubled());
				println(geometry.spread(points), text.tidy("  Hi  "));
			}
		`,
		"geometry.qi": `
			import std.arrays;

			pub struct Point { x: Int; y: Int; }

			impl [Point] {
				pub fn widest() -> Int { return self.map(p -> p.x).largest() ?? 0; }
				fn narrowest() -> Int { return self.map(p -> p.x).smallest() ?? 0; }
			}
			impl<T> [T] { pub fn second() -> T? { return self.at(1); } }

			pub fn spread(points: [Point]) -> Int { return points.widest() - points.narrowest(); }
		`,
		"text.qi": `
			import std.strings;

			impl String {
				pub fn initials() -> String { return self.words().map(w -> w.first(1)).join(""); }
				fn squeezed() -> String { return self.trim().lower(); }
			}
			impl Int { pub fn doubled() -> Int { return self * 2; } }

			pub fn tidy(s: String) -> String { return s.squeezed(); }
		`,
	}
	expectModulesOutput(t, files, "9 9 2 al 10\n6 hi\n")

	files["main.qi"] = `
		import geometry;
		import geometry.{Point};
		fn main() { println([Point { x: 1, y: 1 }].narrowest()); }
	`
	expectModulesError(t, files, "E2027", "main.qi", "method 'narrowest'")
	files["main.qi"] = `
		import text;
		fn main() { println("a".squeezed()); }
	`
	expectModulesError(t, files, "E2027", "main.qi", "method 'squeezed'")
}

// Result is a name every file has: the enum of std.result, without an
// import. A file that has a Result of its own keeps its own.
func TestResultNeedsNoImport(t *testing.T) {
	expectModulesOutput(t, sources{
		"main.qi": `
			import parsing;
			import shapes;

			fn half(n: Int) -> Result<Int, String> {
				if n % 2 != 0 { return Result.Err("${n} is odd"); }
				return Result.Ok(n / 2);
			}
			fn quarter(n: Int) -> Result<Int, String> { return Result.Ok(half(half(n)?)?); }

			fn main() {
				println(half(10), half(7), quarter(12), quarter(6));
				match parsing.number("41") {
					case Result.Ok(n): println(n + 1);
					case Result.Err(why): println(why);
				}
				println(parsing.number("x").isErr(), parsing.sum(["1", "2", "3"]), parsing.sum(["1", "no"]));
				println(shapes.check(3), shapes.check(-1));
			}
		`,
		// A module of the program has the name as well
		"parsing.qi": `
			pub fn number(text: String) -> Result<Int, String> {
				let n = text.parseInt();
				if n == null { return Result.Err("not a number: " + text); }
				return Result.Ok(n);
			}
			pub fn sum(texts: [String]) -> Result<Int, String> {
				let total = 0;
				for text in texts { total += number(text)?; }
				return Result.Ok(total);
			}
		`,
		// One that declares a Result of its own means its own
		"shapes.qi": `
			pub enum Result { Fine, Wrong(Int) }
			pub fn check(side: Int) -> Result {
				if side < 0 { return Result.Wrong(side); }
				return Result.Fine;
			}
		`,
	}, "Ok(5) Err(\"7 is odd\") Ok(3) Err(\"3 is odd\")\n42\ntrue Ok(6) Err(\"not a number: no\")\nFine Wrong(-1)\n")

	// Importing it still works, alone or with other names of the module
	expectModulesOutput(t, sources{"main.qi": `
		import std.result;
		import std.result.{Result, Failure};

		fn risky() -> Int { return [1][3]; }
		fn main() {
			match result.attempt(risky) {
				case Result.Ok(n): println(n);
				case Result.Err(failure): println(failure.message);
			}
		}
	`}, "index out of bounds: 3 (length 1)\n")

	// A program that never says Result has nothing of std.result in it
	result, errs := compileModules(sources{"main.qi": "fn main() { println(1); }"})
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}
	for _, proto := range result.Chunk.Prototypes {
		if strings.HasPrefix(proto.Name, "std.result") {
			t.Errorf("expected nothing of std.result in a program that does not say Result, found %s", proto.Name)
		}
	}

	// What is wrong with a Result is still said
	expectModulesError(t, sources{
		"main.qi": "fn f() -> Result<Int, String> { return Result.Ok(\"one\"); }\nfn main() { f(); }",
	}, "E2003", "main.qi", "cannot return type std.result.Result<String, String>")
}

// What a program does not use of the standard library does not go through
// the compiler: it is dropped once the program is checked, before the
// stages that would compile it for nothing.
func TestTheLibraryIsPrunedEarly(t *testing.T) {
	lowered := func(src string) (map[string]bool, *Result) {
		t.Helper()
		errs := errors.NewHandler()
		result := Compile(src, "test.qi", errs)
		if errs.HasErrors() {
			t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
		}
		names := make(map[string]bool)
		for _, decl := range result.Lowered.Decls {
			switch d := decl.(type) {
			case *ast.FnDecl:
				names[d.Name] = true
			case *ast.StructDecl:
				names[d.Name] = true
			case *ast.EnumDecl:
				names[d.Name] = true
			case *ast.ImplDecl:
				for _, method := range d.Methods {
					names[d.StructName+"."+method.Name] = true
				}
			}
		}
		return names, result
	}
	library := func(names map[string]bool) int {
		count := 0
		for name := range names {
			if strings.HasPrefix(name, "std.") || strings.HasPrefix(name, "[") || strings.HasPrefix(name, "String.") {
				count++
			}
		}
		return count
	}

	// One method of arrays: next to nothing of the module is compiled
	names, result := lowered(`fn main() { println([3, 1, 2].first()); }`)
	if n := library(names); n == 0 || n > 6 {
		t.Errorf("expected a handful of declarations of the library for one method, got %d: %v", n, names)
	}
	for _, unwanted := range []string{"std.arrays.sorted", "std.arrays.groupBy", "std.dict.Dict", "[].map", "[].filter"} {
		if names[unwanted] {
			t.Errorf("expected %s to be left out of a program that does not use it", unwanted)
		}
	}
	// What the compilation hands back as the program read is whole
	whole := 0
	for _, decl := range result.AST.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && strings.HasPrefix(fn.Name, "std.arrays.") {
			whole++
		}
	}
	if whole < 30 {
		t.Errorf("expected the program as read to still have all of std.arrays, found %d functions", whole)
	}

	// What is used through something else is kept: a method that calls
	// others, a struct of the library and its methods, what an operator,
	// a loop or brackets call, a method taken as a value
	names, _ = lowered(`
		import std.dict;
		import std.dict.{Dict};
		import std.list;

		fn main() {
			let seen: Dict<Int> = dict.empty();
			seen["a"] = 1;
			println(seen["a"], [1, 2, 3].groupBy(n -> toString(n % 2)).keys());
			let items = list.of([3, 1, 2]);
			for item in items { print(item, ""); }
			let add = items.add;
			add(4);
			println(items.len(), (0..10).step(5).toArray());
			for i in (0..4).reversed() { print(i, ""); }
			println();
		}
	`)
	for _, wanted := range []string{"std.dict.Dict", "std.dict.Dict.keys", "std.dict.Dict.at", "std.dict.Dict.put", "std.list.List.iterate", "std.list.List.add", "std.ops.Range.stop", "std.ops.Range.reversed"} {
		if !names[wanted] {
			t.Errorf("expected %s to be kept", wanted)
		}
	}
	if names["std.list.List.sorted"] || names["std.dict.Dict.remove"] {
		t.Errorf("expected the methods the program does not call to be left out")
	}
}

// A variant written alone is of an enum the file names without a dot:
// one it declares, or imports by name. An enum reached through its module
// keeps its variants behind it, and two enums with the same variant are
// told apart by the type the value has to be.
func TestVariantsAloneAcrossModules(t *testing.T) {
	expectModulesOutput(t, sources{
		"geo.qi": `
			pub enum Shape { Circle(Float), Square(Float), Empty }
			pub fn unit() -> Shape { return Square(1.0); }
		`,
		"store.qi": `pub enum Slot { Full(Int), Empty }`,
		"main.qi": `
			import geo.{Shape, unit};
			import store;

			fn size(s: Shape) -> Float {
				return match s {
					case Circle(r): r;
					case Square(side): side;
					case Empty: 0.0;
				};
			}

			fn main() -> Int {
				let slot: store.Slot = store.Slot.Empty;
				println(size(Circle(2.0)), size(unit()), size(Empty), slot);
				return 0;
			}
		`,
	}, "2 1 0 Empty\n")

	expectModulesError(t, sources{
		"store.qi": `pub enum Slot { Full(Int), Empty }`,
		"main.qi": `
			import store;
			fn main() -> Int { let s = Full(1); return 0; }
		`,
	}, "E1001", "main.qi", "undefined identifier 'Full'")
}

// A module's variables outside functions are its own: its functions share
// them, another module only reaches them through those functions, and
// they are made before main starts.
func TestModuleVariables(t *testing.T) {
	expectModulesOutput(t, sources{
		"ids.qi": `
			let next = 100;
			let given: [Int] = [];
			pub fn take() -> Int { next += 1; given.push(next); return next; }
			pub fn all() -> [Int] { return given; }
		`,
		"main.qi": `
			import ids;
			let first = ids.take();
			fn main() -> Int {
				println(first, ids.take(), ids.take(), ids.all());
				return 0;
			}
		`,
	}, "101 102 103 [101, 102, 103]\n")

	expectModulesError(t, sources{
		"ids.qi":  `let next = 100;`,
		"main.qi": `import ids; fn main() -> Int { return ids.next; }`,
	}, "E8003", "main.qi", "variable 'next' of module 'ids' is private")

	expectModulesError(t, sources{
		"ids.qi":  "let next = 100;\nnext += 1;",
		"main.qi": `import ids; fn main() -> Int { return 0; }`,
	}, "E8007", "ids.qi", "statements outside a function only run in the program's entry file")
}

// A pub function or method of a module may not use a type of the module
// that other modules cannot name; one that is not pub may.
func TestPubUsesOnlyPubTypes(t *testing.T) {
	expectModulesError(t, sources{
		"vault.qi": "struct Secret { n: Int; }\npub fn make() -> Secret { return Secret { n: 1 }; }",
		"main.qi":  "import vault;\nfn main() -> Int { return 0; }",
	}, "E8009", "vault.qi", "pub function 'make' of module 'vault' uses Secret")

	expectModulesError(t, sources{
		"vault.qi": "struct Secret { n: Int; }\npub struct Box { n: Int; }\nimpl Box { pub fn open(keys: [Secret?]) -> Int { return self.n; } }",
		"main.qi":  "import vault;\nfn main() -> Int { return 0; }",
	}, "E8009", "vault.qi", "pub method 'open' of module 'vault' uses Secret")

	expectModulesOutput(t, sources{
		"vault.qi": `
			struct Secret { n: Int; }
			pub struct Key { code: Int; }
			fn hidden() -> Secret { return Secret { n: 7 }; }
			pub fn open<T>(value: T) -> Key { return Key { code: hidden().n }; }
		`,
		"main.qi": "import vault;\nfn main() -> Int { println(vault.open(1).code); return 0; }",
	}, "7\n")
}

// With Unused on, what nothing uses is said as warnings: variables,
// parameters, names of patterns, imports. Names starting with _, the
// parameters a trait or a short function asks for, and the library are
// left alone; without it nothing is said.
func TestUnusedWarnings(t *testing.T) {
	fs := sources{
		"lib.qi": "pub fn twice(n: Int) -> Int { return n * 2; }\npub fn half(n: Int) -> Int { return n / 2; }",
		"main.qi": `
			import lib;
			import lib.{twice, half};
			import std.json;
			trait Named { fn name(prefix: String) -> String; }
			struct P { n: Int; }
			impl Named for P { fn name(prefix: String) -> String { return "p"; } }
			enum E { A(Int), B }
			fn f(used: Int, unused: Int, _skipped: Int) -> Int {
				let spare = 1;
				let _fine = 2;
				let e = E.A(used);
				match e { case E.A(n): { } case E.B: { } }
				return [1, 2].fold(0, (sum, item) -> sum) + twice(used);
			}
			fn main() -> Int { return f(1, 2, 3); }
		`,
	}
	errs := errors.NewHandler()
	CompileOptions(fs["main.qi"], "main.qi", Options{Read: fs.read, Unused: true}, errs)
	var got []string
	for _, d := range errs.Diagnostics {
		got = append(got, d.Code+" "+d.Message)
	}
	want := []string{
		"E8010 module 'lib' is imported and never used",
		"E8010 'half' is imported and never used",
		"E8010 module 'std.json' is imported and never used",
		"E1004 parameter 'unused' is never used",
		"E1004 variable 'spare' is never used",
		"E1004 name 'n' is never used",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	errs = errors.NewHandler()
	CompileOptions(fs["main.qi"], "main.qi", Options{Read: fs.read}, errs)
	if len(errs.Diagnostics) != 0 {
		t.Errorf("expected nothing without Unused, got %v", errs.Diagnostics)
	}
}

// A case may name a constant of another module, imported by name or
// reached through the module's name.
func TestMatchOnConstantsOfModules(t *testing.T) {
	fs := sources{
		"signals.qi": `
pub const HAND_OVER = "hand_over";
pub const STOP = "stop";
pub const RETRIES = 3;
const SECRET = "secret";
`,
		"main.qi": `
import signals;
import signals.{HAND_OVER, RETRIES};

fn handle(name: String, tries: Int) -> String {
	match name {
		case HAND_OVER: { return "hand over"; }
		case signals.STOP if tries < RETRIES: { return "stop"; }
		case signals.STOP | "halt": { return "stop late"; }
		default: { return "other"; }
	}
}

fn main() -> Int {
	println(handle("hand_over", 0), handle("stop", 1), handle("stop", 5), handle("halt", 0), handle("x", 0));
	match 3 {
		case signals.RETRIES: { println("retries"); }
		default: { println("other"); }
	}
	return 0;
}
`,
	}
	expectModulesOutput(t, fs, "hand over stop stop late stop late other\nretries\n")

	fs["main.qi"] = "import signals;\nfn main() -> Int {\n\tmatch \"a\" { case signals.SECRET: { return 1; } default: { return 0; } }\n}\n"
	expectModulesError(t, fs, "E8003", "main.qi", "constant 'SECRET' of module 'signals' is private")

	fs["main.qi"] = "import signals;\nfn main() -> Int {\n\tlet s = \"a\";\n\tif s is signals.STOP { return 1; }\n\treturn 0;\n}\n"
	expectModulesError(t, fs, "E8005", "main.qi", "'is' takes a type or the pattern of a variant, and 'signals.STOP' is neither")
}
