package types

import (
	"strings"
	"testing"
)

// The holes in the checker the audit of 2026-10-23 found: each program
// compiled, and failed when it ran. Each is now refused where it is
// written, or, when it is right, still accepted.
func TestHoles(t *testing.T) {
	for _, c := range []struct {
		name, src string
		says      string // "" when it is right
	}{
		{"T1: Self of a value known by its trait", `
trait S { fn plus(o: Self) -> Int; }
struct B { v: Int; }
struct C { w: String; }
impl S for B { fn plus(o: Self) -> Int { return o.v + self.v; } }
impl S for C { fn plus(o: Self) -> Int { return o.w.len(); } }
fn f(xs: [S]) -> Int { return xs[0].plus(xs[1]); }`, "receiver's own type (Self)"},
		{"T1: Self of a type parameter is one type", `
trait S { fn plus(o: Self) -> Int; }
fn f<T: S>(a: T, b: T) -> Int { return a.plus(b); }`, ""},
		{"T1: a trait's own methods call others on self", `
trait S {
    fn plus(o: Self) -> Int;
    fn twice(o: Self) -> Int { return self.plus(o) * 2; }
}`, ""},
		{"T1: what takes no Self is called through the trait", `
trait S { fn size() -> Int; }
fn f(xs: [S]) -> Int { return xs[0].size(); }`, ""},
		{"T2: a generic struct written without what it is of", `
struct Box<T> { v: T; }
fn f(b: Box) -> Int { return 1; }`, "takes type arguments"},
		{"T2: a literal without them is of what its fields are", `
struct Box<T> { v: T; }
fn f() -> Int { let b: Box<String> = Box { v: 1 }; let c = Box { v: 2 }; let d: Box<String> = c; return 1; }`, "cannot assign type Box<Int> to Box<String>"},
		{"T2: ...or of what is expected there", `
struct Box<T> { v: T; }
fn f() -> Box<Int?> { let b: Box<Int?> = Box { v: 1 }; return Box { v: null }; }`, ""},
		{"T2: too many", `
struct Box<T> { v: T; }
fn f(b: Box<Int, String>) -> Int { return 1; }`, "takes 1 type arguments"},
		{"T3: a generic function taken as a value", `
fn id<T>(x: T) -> T { return x; }
fn f() -> Int { let g = id; return 1; }`, "is generic: taken as a value"},
		{"T3: ...called, its types are the call's", `
fn id<T>(x: T) -> T { return x; }
fn f() -> Int { let s: String = id("a"); return id(1); }`, ""},
		{"T3: ...in a function that calls it", `
fn id<T>(x: T) -> T { return x; }
fn f() -> Int { let g = (x: Int) -> id(x); return g(1); }`, ""},
		{"T4: a cast to a method's own type parameter", `
struct Box { v: Any; }
impl Box { fn get<U>() -> U { return self.v as U; } }`, "a type parameter of a method, cannot be checked"},
		{"T4: ...and a test of one", `
struct Box { v: Any; }
impl Box { fn holds<U>() -> Bool { return self.v is U; } }`, "a type parameter of a method, cannot be told"},
		{"T4: a function generic of its own casts", `
fn as<U>(v: Any) -> U { return v as U; }
struct Box { v: Any; }
impl Box { fn get<U>() -> U { return as<U>(self.v); } }`, ""},
		{"T5: what && learnt of a field, its right side writes", `
struct Node { v: Int; next: Node?; }
fn clear(n: Node) -> Bool { n.next = null; return true; }
fn f(n: Node) -> Int { if n.next != null && clear(n) { return n.next.v; } return 0; }`, "may be null"},
		{"T5: ...and kept when it writes none", `
struct Node { v: Int; next: Node?; }
fn ready(n: Node) -> Bool { return n.v > 0; }
fn f(n: Node) -> Int { if n.next != null && ready(n) { return n.next.v; } return 0; }`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			msgs, _ := checkSourceLoose(c.src)
			all := strings.Join(msgs, "\n")
			switch {
			case c.says == "" && len(msgs) > 0:
				t.Errorf("refused:\n%s", all)
			case c.says != "" && !strings.Contains(all, c.says):
				t.Errorf("not refused as %q:\n%s", c.says, all)
			}
		})
	}
}
