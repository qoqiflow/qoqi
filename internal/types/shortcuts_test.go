package types

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// The declarations the snippets below share. Tuples are values of enums
// the compiler declares; here they are written in front of each snippet.
const shortcutDecls = `
struct Address { city: String; zip: String?; }
struct User { name: String; address: Address?; }
struct Account { owner: User; }
impl User {
	fn shout() -> String { return self.name; }
	fn rename(to: String) { self.name = to; }
	fn boss() -> User? { return null; }
}
fn find(id: Int) -> User? { return null; }
fn number(text: String) -> Int? { return null; }
`

func checkShortcut(t *testing.T, src string) []string {
	t.Helper()
	msgs, _, _ := checkSource(t, ast.TupleSource()+shortcutDecls+src)
	return msgs
}

func TestSafeAccessAndTryAreTyped(t *testing.T) {
	valid := []struct{ name, src string }{
		{"a field of an optional is optional", `fn f(u: User?) -> String? { return u?.name; }`},
		{"with a fallback it is the field's type", `fn f(u: User?) -> String { return u?.name ?? "nobody"; }`},
		{"an optional field stays optional, once", `fn f(u: User?) -> Address? { let a: Address? = u?.address; return a; }`},
		{"a chain", `fn f(u: User?) -> String? { let zip: String? = u?.address?.zip; return u?.address?.city; }`},
		{"a method", `fn f(u: User?) -> String? { return u?.shout(); }`},
		{"a method that returns an optional", `fn f(u: User?) -> String? { return u?.boss()?.boss()?.name; }`},
		{"a method called for what it does", `fn f(u: User?) { u?.rename("x"); }`},
		{"on a call", `fn f() -> String? { return find(1)?.name; }`},
		{"all that follows is done with the value", `fn f(a: Account?) -> String? { return a?.owner.name; }`},
		{"...calls and indexes too", `fn f(u: User?) -> String? { return u?.shout()[0]; }`},
		{"...up to the next thing that may be null", `fn f(a: Account?) -> String? { return a?.owner.address?.city; }`},
		{"on a field path", `fn f(u: User) -> String? { return u.address?.city; }`},

		{"try on an optional, in a function that may return null", `fn f(text: String) -> Int? { return number(text)? + 1; }`},
		{"its value is not optional", `fn f() -> User? { let u: User = find(1)?; return u; }`},
		{"several in one expression", `fn f(a: String, b: String) -> Int? { return number(a)? * number(b)?; }`},
		{"in a function that returns nothing", `fn f(u: User?) { let user = u?; user.rename("x"); }`},
		{"after a chain", `fn f(u: User?) -> String? { let zip = u?.address?.zip?; return zip + "!"; }`},
		{"in a lambda, against the lambda's own type", `fn f() -> Int { let g = fn(text: String) -> Int? { return number(text)? + 1; }; return g("1") ?? 0; }`},
		{"as an argument", `fn twice(n: Int) -> Int { return n * 2; } fn f(text: String) -> Int? { return twice(number(text)?); }`},
		{"in a loop", `fn f(texts: [String]) -> Int? { let sum = 0; for text in texts { sum += number(text)?; } return sum; }`},

		{"a test reaches a lambda for a parameter nothing assigns", `fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> Int { return n + 1; }; return g(); }`},
		{"...and for a variable", `fn f() -> String { let u = find(1); if u == null { return ""; } let g = fn() -> String { return u.name; }; return g(); }`},
		{"...inside the branch of an if", `fn f(n: Int?) -> Int { if n != null { let g = fn() -> Int { return n + 1; }; return g(); } return 0; }`},
		{"...through a lambda in a lambda", `fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> fn() -> Int { return fn() -> Int { return n + 1; }; }; return g()(); }`},
		{"...for a type test", `fn f(v: Any) -> Int { if v is Int { let g = fn() -> Int { return v + 1; }; return g(); } return 0; }`},
		{"...for the variable of a loop", `fn f(items: [Int?]) -> Int { let fs: [fn() -> Int] = []; for item in items { if item != null { __push(fs, fn() -> Int { return item + 1; }); } } return __len(fs); }`},
		{"a lambda may assign its own variables", `fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> Int { let m = n; m = m + 1; return m; }; return g(); }`},

		{"a tuple and its positions", `fn f() -> Int { let p = (1, "a"); let n: Int = p.0; let s: String = p.1; return n; }`},
		{"a tuple type", `fn f(p: (Int, String)) -> (String, Int) { return (p.1, p.0); }`},
		{"tuples in tuples", `fn f(p: ((Int, Bool), [String])) -> Bool { return p.0.1; }`},
		{"taken apart", `fn f(p: (Int, String, Bool)) -> String { let (n, s, _) = p; let m: Int = n; return s; }`},
		{"in a loop", `fn f(ps: [(String, Int)]) -> Int { let t = 0; for (name, n) in ps { t += n + __len(name); } return t; }`},
		{"generic", `fn swap<A, B>(p: (A, B)) -> (B, A) { return (p.1, p.0); } fn f() -> String { return swap((1, "a")).0; }`},
		{"compared", `fn f(p: (Int, Int)) -> Bool { return p == (1, 2) || p != (3, 4); }`},
		{"an optional tuple", `fn f(p: (Int, String)?) -> Int? { return p?.0; }`},
		{"null inside, with a type to say what", `fn f() -> (Int, String?) { return (1, null); }`},
		{"matched, completely", `fn f(p: (Bool, Bool)) -> Int { match p { case (true, true): { return 2; } case (true, false): { return 1; } case (false, _): { return 0; } } }`},
		{"matched, with a default", `fn f(p: (Int, String)) -> Int { match p { case (0, _): { return 0; } case (n, "x"): { return n; } default: { return -1; } } }`},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if msgs := checkShortcut(t, tt.src); len(msgs) > 0 {
				t.Errorf("unexpected errors: %v", msgs)
			}
		})
	}

	invalid := []struct{ name, src, want string }{
		{"safe access on what is never null", `fn f(u: User) -> String? { return u?.name; }`, "'?.' is for a value that may be null, and this User never is"},
		{"...once a test said so", `fn f(u: User?) -> String? { if u == null { return null; } return u?.name; }`, "this User never is"},
		{"a field that does not exist", `fn f(u: User?) -> String? { return u?.nope; }`, "User has no field 'nope'"},
		{"a method that does not exist", `fn f(u: User?) -> String? { return u?.nope(); }`, "nope"},
		{"the result is optional", `fn f(u: User?) -> String { return u?.name; }`, "cannot return type string? from function returning string"},
		{"a field that may be null needs '?.' of its own", `fn f(u: User?) -> String? { return u?.address.city; }`, "the value may be null"},
		{"in brackets the chain ends", `fn f(a: Account?) -> String? { return (a?.owner).name; }`, "the value may be null"},
		{"wrong argument in a safe call", `fn f(u: User?) { u?.rename(1); }`, "cannot use type int as argument"},

		{"try on what is neither", `fn f(n: Int) -> Int? { return n?; }`, "'?' is for a Result or a value that may be null, and this is a Int"},
		{"try on an optional, in a function that cannot return null", `fn f(text: String) -> Int { return number(text)?; }`, "returns null to the caller, and this function returns Int"},
		{"...in main", `fn main() -> Int { let n = number("1")?; return n; }`, "returns null to the caller"},
		{"...in a lambda of another type", `fn f() -> Int? { let g = fn() -> Int { return number("1")?; }; return g(); }`, "this function returns Int"},
		{"the value of a try is checked like any", `fn f(text: String) -> String? { return number(text)?; }`, "cannot return type int from function returning string?"},

		{"a position a tuple does not have", `fn f(p: (Int, String)) -> Int { return p.2; }`, "(Int, String) holds 2 values: there is none at position 2"},
		{"a name on a tuple", `fn f(p: (Int, String)) -> Int { return p.first; }`, "a tuple has no 'first'"},
		{"a position on a struct", `fn f(u: User) -> String { return u.0; }`, "User is not a tuple"},
		{"too many names", `fn f(p: (Int, String)) -> Int { let (a, b, c) = p; return a; }`, "(Int, String) holds 2 values, and 3 are named here"},
		{"too few names", `fn f(p: (Int, String, Bool)) -> Int { let (a, b) = p; return a; }`, "(Int, String, Bool) holds 3 values, and 2 are named here"},
		{"taking apart what is not a tuple", `fn f(u: User) -> Int { let (a, b) = u; return 0; }`, "User is not a tuple"},
		{"assigning to a position", `fn f(p: (Int, String)) { p.0 = 2; }`, "a tuple cannot be changed once made"},
		{"wrong type of a value", `fn f() -> (Int, String) { return (1, 2); }`, "(Int, Int)"},
		{"wrong size", `fn f() -> (Int, Int) { return (1, 2, 3); }`, "cannot return type (int, int, int) from function returning (int, int)"},
		{"match that leaves a pair out", `fn f(p: (Bool, Bool)) -> Int { match p { case (true, _): { return 1; } case (false, true): { return 2; } } }`, "match on (Bool, Bool) does not handle (false, false)"},
		{"pattern of the wrong size", `fn f(p: (Int, Int)) -> Int { match p { case (a, b, c): { return a; } default: { return 0; } } }`, "never"},
		{"names in a pattern are variables, not types", `fn f(p: (Int, Int)) -> String { match p { case (a, b): { return a; } } }`, "cannot return type int from function returning string"},
		{"numbers cannot be listed", `fn f(p: (Int, Bool)) -> Int { match p { case (0, _): { return 0; } case (1, _): { return 1; } } }`, "does not handle"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			msgs := checkShortcut(t, tt.src)
			if len(msgs) == 0 {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(strings.ToLower(strings.Join(msgs, "\n")), strings.ToLower(tt.want)) {
				t.Errorf("expected an error containing %q, got %q", tt.want, msgs)
			}
		})
	}
}

// TestSafeAccessIsSaidOnce checks that a mistake in the left side of a
// `?.` is the only error: the access is not looked at with a value of an
// unknown type.
func TestSafeAccessIsSaidOnce(t *testing.T) {
	for _, src := range []string{
		`fn f(u: User?) -> String? { return u?.nope?.more?.again; }`,
		`fn f(u: User?) -> String? { return u?.nope()?.more(1, 2); }`,
		`fn f(p: (Int, String)) -> Int { let (a, b, c) = p; return a + b + c; }`,
	} {
		if msgs := checkShortcut(t, src); len(msgs) != 1 {
			t.Errorf("%s: expected one error, got %d: %v", src, len(msgs), msgs)
		}
	}
}
