package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestShortFunctions(t *testing.T) {
	expectOutput(t, `
		import std.list;
		import std.option;
		import std.result.{Result};

		struct Person { name: String; age: Int; }
		struct Button { label: String; onClick: fn(String) -> String = label -> "clicked ${label}"; }

		fn apply(f: fn(Int, Int) -> Int, a: Int, b: Int) -> Int { return f(a, b); }
		fn twice(f: fn() -> Void) { f(); f(); }
		fn check(n: Int) -> Result<String, String> {
			if n > 3 { return Result.Err("big ${n}"); }
			return Result.Ok("ok");
		}
		fn make() -> fn(Int) -> Int { return n -> n * 10; }
		fn adder(by: Int) -> fn(Int) -> Int { return n -> n + by; }
		fn curried() -> fn(Int) -> fn(Int) -> Int { return a -> b -> a * b; }
		fn maybe(flag: Bool) -> (fn(Int) -> Int)? {
			if flag { return n -> n + 1; }
			return null;
		}
		fn pick<T>(items: [T], wanted: fn(T) -> Bool) -> T? { return items.find(wanted); }
	`, `
		let people = [Person { name: "grace", age: 45 }, Person { name: "ada", age: 36 }, Person { name: "alan", age: 41 }];

		// The types come from what the function is handed to
		let names = people.map(p -> p.name);
		let adults = people.filter(p -> p.age > 40);
		let total = people.fold(0, (sum, p) -> sum + p.age);
		println(names, adults.map(p -> p.name.upper()), total);
		println(people.sorted((a, b) -> a.age < b.age).map(p -> p.age), people.sortedBy(p -> p.age).map(p -> p.name));
		println(apply((a, b) -> a * b, 6, 7), make()(4), adder(5)(1), curried()(6)(7), option.map(pick(people, p -> p.name == "ada"), p -> p.age), pick([1, 2, 3], n -> n > 5));

		// One that gives nothing: an expression for what it does, an
		// assignment, a block
		let count = 0;
		twice(() -> count++);
		let log: [String] = [];
		names.each(name -> log.push("seen ${name}"));
		[1, 2].each(n -> { count += n; log.push(toString(n)); });
		[10].each(n -> count += n);
		println(count, log);

		// Where a function type is written: a variable, a field, a result
		let double: fn(Int) -> Int = n -> n * 2;
		let shout: fn(String) -> String = s -> { let up = s.upper(); return up + "!"; };
		let both: fn(Int, Int) -> (Int, Int) = (a, b) -> (b, a);
		println(double(21), shout("hey"), both(1, 2), Button { label: "ok" }.onClick("ok"), Button { label: "x", onClick: l -> "no" }.onClick("x"));
		let stored: [fn(Int) -> Int] = [n -> n + 1, n -> n * n, double];
		println(stored.map(f -> f(5)), (maybe(true) ?? double)(1), (maybe(false) ?? double)(1));

		// Methods of generic structs and enums
		let grouped = people.groupBy(p -> p.name.first(1));
		println(grouped.keys(), list.of(names).filter(n -> n.len() > 3).map(n -> n.len()).toArray());
		println(option.map("21".parseInt(), n -> n * 2), option.then("4".parseInt(), n -> [1, 2].at(n)));
		let r: Result<Int, String> = Result.Ok(4);
		println(r.map(n -> n + 1), r.then(n -> check(n)), r.mapErr(e -> e.len()), check(9).map(s -> s.len()));

		// One inside another, each with what the other names
		println([1, 2, 3].map(n -> [n, n * 2].map(m -> m + n)), [[1], [2, 3]].map(row -> row.len()));
		println(people.map(p -> (p.name, p.age))[0], ["a b", "c"].flatMap(s -> s.words()));

		// It captures like any function
		let seen: [String] = [];
		let remember = names.map(name -> () -> seen.push(name));
		for job in remember { job(); }
		println(seen, [1, 2].map(n -> n + total));
	`, `["grace", "ada", "alan"] ["GRACE", "ALAN"] 122
[36, 41, 45] ["ada", "alan", "grace"]
42 40 6 42 36 null
15 ["seen grace", "seen ada", "seen alan", "1", "2"]
42 HEY! (2, 1) clicked ok no
[6, 25, 10] 2 2
["g", "a"] [5, 4]
42 null
Ok(5) Err("big 4") Ok(4) Err("big 9")
[[2, 3], [4, 6], [6, 9]] [1, 2]
("grace", 45) ["a", "b", "c"]
["grace", "ada", "alan"] [123, 124]
`)
}

// TestGenericFunctionsOwnTheirFunctions checks that each instance of a
// generic function has function expressions and match statements of its
// own. They were shared once, and every instance then ran the code of the
// last one.
func TestGenericFunctionsOwnTheirFunctions(t *testing.T) {
	expectOutput(t, `
		trait Named { fn name() -> String; }
		struct Cat { }
		impl Named for Cat { fn name() -> String { return "cat"; } }
		struct Dog { }
		impl Named for Dog { fn name() -> String { return "dog"; } }
		enum Bird { Owl }
		impl Named for Bird { fn name() -> String { return "owl"; } }

		fn names<T: Named>(items: [T]) -> [String] {
			let found = items.map(item -> item.name());
			return found;
		}
		fn written<T: Named>(items: [T]) -> [String] {
			return items.map(fn(item: T) -> String { return item.name() + "!"; });
		}
		fn later<T: Named>(item: T) -> fn() -> String {
			return () -> "a " + item.name();
		}

		enum Option<T> { Some(T), None }
		fn describe<T: Named>(value: Option<T>, fallback: T) -> String {
			match value {
				case Option.Some(item): { return item.name(); }
				case Option.None: { return "no " + fallback.name(); }
			}
		}
		fn size<T>(items: [T]) -> String {
			match items.len() {
				case 0: { return "none"; }
				default: { let first: T = items[0]; return "some"; }
			}
		}
	`, `
		println(names([Cat { }]), names([Dog { }, Dog { }]), names([Bird.Owl]), names([Cat { }]));
		println(written([Cat { }]), written([Dog { }]), written([Bird.Owl]));
		let jobs = [later(Cat { }), later(Dog { }), later(Bird.Owl)];
		println(jobs.map(job -> job()));
		println(describe(Option.Some(Cat { }), Cat { }), describe(Option<Dog>.None, Dog { }), describe(Option.Some(Bird.Owl), Bird.Owl), describe(Option<Cat>.None, Cat { }));
		println(size([1]), size(["a"]), size([1.5].take(0)));
	`, `["cat"] ["dog", "dog"] ["owl"] ["cat"]
["cat!"] ["dog!"] ["owl!"]
["a cat", "a dog", "a owl"]
cat no dog owl no cat
some some none
`)
}

func TestShortFunctionErrors(t *testing.T) {
	prelude := "import std.arrays;\nstruct P { name: String; }\nfn apply(f: fn(Int, Int) -> Int) -> Int { return f(1, 2); }\nfn describe(value: Any) -> Int { return 0; }\n"
	tests := []struct {
		name string
		src  string
		code string
		msg  string
		hint string
	}{
		{"nothing says what it takes", "let a = x -> x + 1;", "E2037", "nothing here says what the parameters of this function are", "fn(name: Type) -> Type { ... }"},
		{"where anything is taken", "let n = describe(x -> x);", "E2037", "nothing here says what the parameters of this function are", ""},
		{"printed", "println(x -> x);", "E2037", "nothing here says what the parameters", ""},
		{"too few names", "let b = apply(x -> x);", "E2037", "a function of 2 parameters is expected here, and this one names 1", "(first, second) -> ..."},
		{"too many names", "let b = [1].map((a, b) -> a);", "E2037", "a function of 1 parameter is expected here, and this one names 2", "name -> ..."},
		{"the wrong result", "let c = apply((x, y) -> \"text\");", "E2003", "cannot return type String from function returning Int", ""},
		{"a result that is not a condition", "let g = [1, 2].filter(n -> n + 1);", "E2003", "cannot return type Int from function returning Bool", ""},
		{"a mistake in the body, said once", "let d = [P { name: \"a\" }].map(p -> p.nope);", "E2014", "P has no field 'nope'", ""},
		{"the arrow of other languages", "let f: fn(Int) -> Int = x => x;", "E0001", "a function is written with '->', not '=>'", "item -> item.name"},
		{"something else than a name", "let f: fn(Int) -> Int = (1) -> 2;", "E0001", "the parameters of a function written with '->' are names", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			result := Compile(prelude+"fn main() -> Int {\n"+tt.src+"\nreturn 0;\n}", "test.qi", errs)
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
			if !strings.Contains(d.Suggestion, tt.hint) {
				t.Errorf("expected a suggestion containing %q, got %q", tt.hint, d.Suggestion)
			}
			if n := len(errs.Diagnostics); tt.name == "a mistake in the body, said once" && n != 1 {
				t.Errorf("expected one diagnostic, got %d", n)
			}
		})
	}
}
