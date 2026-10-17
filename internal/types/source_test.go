package types

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// checkSource type checks source text and returns the diagnostics' messages
// together with the type information.
func checkSource(t *testing.T, src string) ([]string, *Info, *ast.Program) {
	t.Helper()

	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	table := resolve.NewResolver(errs).Resolve(prog)
	if errs.HasErrors() {
		t.Fatalf("test source is invalid: %s", errs.Diagnostics[0].Message)
	}

	info := NewChecker(prog, table, errs).Check()
	var msgs []string
	for _, d := range errs.Diagnostics {
		msgs = append(msgs, d.Message)
	}
	return msgs, info, prog
}

// checkSourceLoose is checkSource for programs that name resolution may
// reject too: it returns the messages of both stages, and false if the
// source does not even parse.
func checkSourceLoose(src string) ([]string, bool) {
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	if errs.HasErrors() {
		return nil, false
	}
	table := resolve.NewResolver(errs).Resolve(prog)
	NewChecker(prog, table, errs).Check()

	var msgs []string
	for _, d := range errs.Diagnostics {
		msgs = append(msgs, d.Message)
	}
	return msgs, true
}

// checkSourceDiagnostics type checks source text and returns the
// diagnostics themselves, for tests about codes, positions and suggestions.
func checkSourceDiagnostics(t *testing.T, src string) []errors.Diagnostic {
	t.Helper()
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("test source does not parse: %s", errs.Diagnostics[0].Message)
	}
	table := resolve.NewResolver(errs).Resolve(prog)
	NewChecker(prog, table, errs).Check()
	return errs.Diagnostics
}

// letType returns the type inferred for the initializer of `let name` in main.
func letType(t *testing.T, info *Info, prog *ast.Program, name string) string {
	t.Helper()
	for _, decl := range prog.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok || fn.Name != "main" {
			continue
		}
		for _, stmt := range fn.Body.Stmts {
			if let, ok := stmt.(*ast.LetStmt); ok && let.Name == name {
				if typ, ok := info.Types[let.Value]; ok {
					return typ.String()
				}
				return "<untyped>"
			}
		}
	}
	t.Fatalf("no `let %s` in main", name)
	return ""
}

func TestCheckInfersExpressionTypes(t *testing.T) {
	msgs, info, prog := checkSource(t, `
		struct Point { x: Int; label: String; }
		fn id<T>(v: T) -> T { return v; }
		fn total(nums: ...Int) -> Int { return __len(nums); }

		fn main() -> Int {
			let p = Point { x: 1, label: "a" };
			let field = p.x;
			let text = p.label;
			let xs = [1.5, 2.5];
			let elem = xs[0];
			let count = __len(xs);
			let char = text[0];
			let size = __len(text);
			let lambda = fn(a: Int) -> Bool { return a > 0; };
			let called = lambda(1);
			let generic = id<Int>(1);
			let variadic = total(1, 2, 3);
			let cmp = text < "b";
			return 0;
		}
	`)
	if len(msgs) != 0 {
		t.Fatalf("expected no errors, got %v", msgs)
	}

	want := map[string]string{
		"field":    "int",
		"text":     "string",
		"elem":     "float",
		"count":    "int",
		"char":     "string",
		"size":     "int",
		"lambda":   "fn(int) -> bool",
		"called":   "bool",
		"generic":  "int", // id<Int> has type fn(int) -> int
		"variadic": "int",
		"cmp":      "bool",
	}
	for name, typ := range want {
		if got := letType(t, info, prog, name); got != typ {
			t.Errorf("let %s: expected %s, got %s", name, typ, got)
		}
	}
}

func TestCheckAcceptsValidPrograms(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"assignment", `fn main() -> Int { let a = 1; a = a + 1; return a; }`},
		{"empty array takes the expected type", `fn f() -> [Int] { return []; }
			fn g(xs: [String]) -> Int { return __len(xs); }
			fn main() -> Int { let a: [Int] = []; a = []; return g([]) + __len(f()); }`},
		{"optional accepts null", `fn main() -> Int { let a: Int? = null; return 0; }`},
		{"loop variable", `fn main() -> Int { let t = 0; for v in [1, 2] { t = t + v; } return t; }`},
		{"string loop variable", `fn main() -> Int { for c in "ab" { let s: String = c; } return 0; }`},
		{"variadic with no extras", `fn f(a: Int, rest: ...Int) -> Int { return a; } fn main() -> Int { return f(1); }`},
		{"lambda as argument", `fn ap(f: fn(Int) -> Int) -> Int { return f(1); }
			fn main() -> Int { return ap(fn(v: Int) -> Int { return v; }); }`},
		{"struct passed as trait", `struct P { x: Int; } trait T { fn m() -> Int; }
			impl T for P { fn m() -> Int { return self.x; } }
			fn use(t: T) -> Int { return t.m(); }
			fn main() -> Int { return use(P { x: 1 }); }`},
		{"self in method", `struct P { x: Int; } impl P { fn twice() -> Int { return self.x + self.x; } }`},
		{"null coalescing", `fn main() -> Int { let a: Int? = null; let b: Int = a ?? 3; return b; }`},
		{"optional accepts a value", `fn main() -> Int { let a: Int? = 3; return a ?? 0; }`},
		{"optional compared with null", `fn f(a: Int?) -> Bool { return a == null; }`},
		{"optional field in a struct", `struct Node { value: Int; next: Node?; }
			fn main() -> Int { let n = Node { value: 1, next: null }; let m = Node { value: 2, next: n }; return (m.next ?? m).value; }`},
		{"method result is typed", `struct P { x: Int; } impl P { fn get() -> Int { return self.x; } }
			fn main() -> Int { let p = P { x: 1 }; return p.get() + 1; }`},
		{"method before its impl", `fn main() -> Int { let p = P { x: 1 }; return p.get(); }
			struct P { x: Int; } impl P { fn get() -> Int { return self.x; } }`},
		{"Self in a method", `struct P { x: Int; } impl P { fn copy() -> Self { return P { x: self.x }; } }
			fn main() -> Int { let p = P { x: 1 }; return p.copy().copy().x; }`},
		{"Self in a trait", `trait Cloneable { fn clone() -> Self; } struct P { x: Int; }
			impl Cloneable for P { fn clone() -> Self { return P { x: self.x }; } }
			fn main() -> Int { let p = P { x: 1 }; return p.clone().x; }`},
		{"method on a trait value", `trait T { fn m() -> Int; } struct P { x: Int; }
			impl T for P { fn m() -> Int { return self.x; } }
			fn use(t: T) -> Int { return t.m() + 1; }`},
		{"array of a trait", `trait T { fn m() -> Int; } struct P { x: Int; } struct Q { y: Int; }
			impl T for P { fn m() -> Int { return self.x; } } impl T for Q { fn m() -> Int { return self.y; } }
			fn main() -> Int { let ts: [T] = [P { x: 1 }, Q { y: 2 }]; return ts[0].m() + ts[1].m(); }`},
		{"field holding a function", `struct H { f: fn(Int) -> Int; }
			fn main() -> Int { let h = H { f: fn(n: Int) -> Int { return n; } }; return h.f(1); }`},
		{"field and method of the same name", `struct A { size: Int; } struct B { n: Int; }
			impl B { fn size() -> Int { return self.n; } }
			fn main() -> Int { let a = A { size: 1 }; let b = B { n: 2 }; return a.size + b.size(); }`},
		{"generic call is typed", `fn first<T>(xs: [T]) -> T { return xs[0]; }
			fn main() -> Int { return first<Int>([1, 2]) + 1; }`},
		{"generic struct", `struct Box<T> { value: T; } fn unwrap<T>(b: Box<T>) -> T { return b.value; }
			fn main() -> Int { let b = Box<Int> { value: 1 }; return b.value + unwrap<Int>(b); }`},
		{"typed empty array", `fn main() -> Int { let xs: [Int] = []; __push(xs, 1); return xs[0] + 1; }`},
		{"Any accepts everything", `fn show(v: Any) -> Void { println(v); }
			fn main() -> Int { show(1); show("s"); show(null); show([1]); return 0; }`},
		{"function accepting more than required", `fn show(v: Any) -> String { return "v"; }
			fn apply(f: fn(Int) -> String) -> String { return f(1); }
			fn main() -> Int { apply(show); apply(toString); return 0; }`},
		{"narrowed by != null", `fn f(n: Int?) -> Int { if n != null { return n + 1; } return 0; }`},
		{"narrowed after an early return", `fn f(n: Int?) -> Int { if n == null { return 0; } return n + 1; }`},
		{"narrowed in the else branch", `fn f(n: Int?) -> Int { if n == null { return 0; } else { return n + 1; } }`},
		{"narrowed by null on the left", `fn f(n: Int?) -> Int { if null != n { return n + 1; } return 0; }`},
		{"narrowed by a negated test", `fn f(n: Int?) -> Int { if !(n == null) { return n + 1; } return 0; }`},
		{"narrowed across &&", `fn f(s: String?) -> Bool { return s != null && __len(s) > 2; }`},
		{"narrowed across ||", `fn f(s: String?) -> Bool { return s == null || __len(s) == 0; }`},
		{"both narrowed by &&", `fn f(a: Int?, b: Int?) -> Int { if a != null && b != null { return a + b; } return 0; }`},
		{"narrowed after early exit on ||", `fn f(a: Int?, b: Int?) -> Int { if a == null || b == null { return 0; } return a + b; }`},
		{"narrowed in a while condition", `struct Node { value: Int; next: Node?; }
			fn sum(list: Node?) -> Int { let t = 0; let n = list; while n != null { t = t + n.value; n = n.next; } return t; }`},
		{"narrowed by assigning a value", `fn main() -> Int { let n: Int? = null; n = 4; return n + 1; }`},
		{"narrowed variable may be set to null again", `fn f(n: Int?) -> Int { if n != null { n = null; } return 0; }`},
		{"narrowed with continue", `fn f(xs: [Int?]) -> Int { let t = 0; for x in xs { if x == null { continue; } t = t + x; } return t; }`},
		{"method on a narrowed struct", `struct P { x: Int; } impl P { fn get() -> Int { return self.x; } }
			fn f(p: P?) -> Int { if p == null { return 0; } return p.get() + p.x; }`},
		{"const read", `fn main() -> Int { const limit = 3; let xs = [1]; const ys = xs; ys[0] = limit; return ys[0]; }`},
		{"return on every path", `fn f(n: Int) -> Int { if n > 0 { return 1; } else if n < 0 { return -1; } else { return 0; } }`},
		{"return from an endless loop", `fn f() -> Int { let i = 0; while true { i = i + 1; if i > 3 { return i; } } }`},
		{"return from every match case", `fn f(n: Int) -> Int { match n { case 0: { return 1; } default: { return 2; } } }`},
		{"void function without return", `fn f() -> Void { let a = 1; }  fn g() { let b = 2; }`},
		{"cast out of Any", `fn f(v: Any) -> Int { return (v as Int) + 1; }`},
		{"cast out of an optional", `fn f(n: Int?) -> Int { return n as Int; }`},
		{"cast from a trait to a struct", `trait T { fn m() -> Int; } struct P { x: Int; }
			impl T for P { fn m() -> Int { return self.x; } }
			fn f(t: T) -> Int { return (t as P).x; }`},
		{"cast to a wider type", `fn f(n: Int) -> Any { let a = n as Any; let o = n as Int?; return a; }`},
		{"cast to an array or optional", `fn f(v: Any) -> Int { let xs = v as [Int]; let s = v as String?; return __len(xs); }`},
		{"method of a generic struct", `struct Box<T> { value: T; }
			impl<T> Box<T> { fn get() -> T { return self.value; } fn set(v: T) -> Void { self.value = v; } }
			fn main() -> Int { let b = Box<Int> { value: 1 }; b.set(2); return b.get() + 1; }`},
		{"generic method returning the struct", `struct Box<T> { value: T; }
			impl<T> Box<T> { fn copy() -> Box<T> { return Box<T> { value: self.value }; } fn me() -> Self { return self; } }
			fn main() -> Int { let b = Box<Int> { value: 1 }; return b.copy().me().value; }`},
		{"trait for a generic struct", `trait Sized { fn size() -> Int; } struct List<T> { items: [T]; }
			impl<T> Sized for List<T> { fn size() -> Int { return __len(self.items); } }
			fn measure(s: Sized) -> Int { return s.size(); }
			fn main() -> Int { return measure(List<String> { items: ["a"] }); }`},
		{"method with its own type parameter", `struct Stack<T> { items: [T]; }
			impl<T> Stack<T> { fn map<U>(f: fn(T) -> U) -> Stack<U> { let out = Stack<U> { items: [] }; for i in self.items { __push(out.items, f(i)); } return out; } }
			fn main() -> Int { let s = Stack<Int> { items: [1] }; let t: Stack<String> = s.map<String>(toString); return __len(t.items[0]); }`},
		{"generic method on a plain struct", `struct U { n: Int; } impl U { fn id<T>(v: T) -> T { return v; } }
			fn main() -> Int { let u = U { n: 1 }; return u.id<Int>(1) + __len(u.id<String>("s")); }`},
		{"generic method with two parameters", `struct U { n: Int; } impl U { fn first<A, B>(a: A, b: B) -> A { return a; } }
			fn main() -> Int { let u = U { n: 1 }; return u.first<Int, String>(1, "s"); }`},
		{"generic method called from a generic method", `struct S<T> { v: T; }
			impl<T> S<T> { fn id<U>(u: U) -> U { return u; } fn twice<U>(u: U) -> U { return self.id<U>(self.id<U>(u)); } }
			fn main() -> Int { return S<Bool> { v: true }.twice<Int>(1); }`},
		{"generic method required by a trait", `trait Mapper { fn apply<T>(v: T) -> T; } struct Id { n: Int; }
			impl Mapper for Id { fn apply<T>(v: T) -> T { return v; } }
			fn use(m: Mapper) -> Int { return m.apply<Int>(1); }`},
		{"variable assigned by its own lambda is narrowed there", `fn main() -> Int {
			let f = fn() -> Int { let n: Int? = 1; n = null; n = 2; if n != null { return n + 1; } return 0; };
			return f(); }`},
		{"variable only read by a lambda is narrowed", `fn f(n: Int?) -> Int {
			let show = fn() -> Void { println(n); };
			if n == null { return 0; } show(); return n + 1; }`},
		{"trait type parameter renamed in the impl", `trait C { fn conv<T>(v: T) -> [T]; } struct W { n: Int; }
			impl C for W { fn conv<U>(v: U) -> [U] { return [v]; } }
			fn use(c: C) -> Int { return c.conv<Int>(1)[0]; }`},
		{"trait type parameters renamed and reused", `trait P { fn pair<A, B>(a: A, b: B) -> A; } struct W { n: Int; }
			impl P for W { fn pair<B, A>(a: B, b: A) -> B { return a; } }`},
		{"field narrowed by a null test", `struct P { x: Int?; } fn f(p: P) -> Int { if p.x != null { return p.x + 1; } return 0; }`},
		{"field narrowed after an early return", `struct P { x: Int?; } fn f(p: P) -> Int { if p.x == null { return 0; } return p.x + 1; }`},
		{"field narrowed across &&", `struct N { v: Int; next: N?; } fn f(n: N) -> Bool { return n.next != null && n.next.v > 0; }`},
		{"nested field path narrowed", `struct N { v: Int; next: N?; }
			fn f(n: N) -> Int { if n.next != null && n.next.next != null { return n.next.next.v; } return 0; }`},
		{"field of self narrowed", `struct N { v: Int; next: N?; }
			impl N { fn second() -> Int { if self.next == null { return -1; } return self.next.v; } }`},
		{"narrowed field passed to a call", `struct N { v: Int; next: N?; } fn val(n: N) -> Int { return n.v; }
			fn f(n: N) -> Int { if n.next != null { return val(n.next); } return 0; }`},
		{"narrowed field may be set to null", `struct P { x: Int?; } fn f(p: P) -> Void { if p.x != null { p.x = null; } }`},
		{"walking a list through a field", `struct N { v: Int; next: N?; }
			fn last(n: N) -> Int { let cur = n; while cur.next != null { cur = cur.next; } return cur.v; }`},
		{"type test narrows a variable", `fn f(v: Any) -> Int { if v is Int { return v + 1; } return 0; }`},
		{"type test narrows after an early return", `fn f(v: Any) -> Int { if !(v is Int) { return 0; } return v + 1; }`},
		{"type test across &&", `fn f(v: Any) -> Bool { return v is String && __len(v) > 2; }`},
		{"type test from a trait to a struct", `trait S { fn a() -> Int; } struct Q { side: Int; }
			impl S for Q { fn a() -> Int { return 1; } }
			fn f(s: S) -> Int { if s is Q { return s.side; } return s.a(); }`},
		{"type test on a field", `struct H { v: Any; } fn f(h: H) -> Int { if h.v is Int { return h.v + 1; } return 0; }`},
		{"type test result is a Bool", `fn f(v: Any) -> Bool { let isInt = v is Int; return isInt || v is [String] || v is Int?; }`},
		{"match on types narrows each case", `struct P { x: Int; }
			fn f(v: Any) -> Int { match v { case Int: { return v + 1; } case String: { return __len(v); } case [Int]: { return v[0]; } case P: { return v.x; } default: { return 0; } } }`},
		{"type argument inferred from an array", `fn first<T>(xs: [T]) -> T { return xs[0]; } fn main() -> Int { return first([1, 2]) + 1; }`},
		{"type arguments inferred from a function", `fn map<T, U>(xs: [T], f: fn(T) -> U) -> [U] { let out: [U] = []; for x in xs { __push(out, f(x)); } return out; }
			fn main() -> Int { let ls: [String] = map([1], toString); let ns: [Int] = map(["a"], fn(s: String) -> Int { return __len(s); }); return ns[0]; }`},
		{"type argument inferred from variadic arguments", `fn collect<T>(items: ...T) -> [T] { return items; }
			fn main() -> Int { let xs: [String] = collect("a", "b"); return collect(1, 2, 3)[0]; }`},
		{"type argument inferred from an optional", `fn orElse<T>(v: T?, d: T) -> T { return v ?? d; }
			fn main() -> Int { let n: Int? = null; return orElse(n, 5) + orElse(3, 4); }`},
		{"type argument inferred from a generic struct", `struct Box<T> { value: T; } fn unbox<T>(b: Box<T>) -> T { return b.value; }
			fn main() -> Int { return unbox(Box<Int> { value: 1 }) + 1; }`},
		{"type argument inferred inside a generic function", `fn first<T>(xs: [T]) -> T { return xs[0]; } fn head<T>(xs: [T]) -> T { return first(xs); }
			fn main() -> Int { return head([1]) + 1; }`},
		{"method type argument inferred", `struct S<T> { items: [T]; }
			impl<T> S<T> { fn map<U>(f: fn(T) -> U) -> S<U> { return S<U> { items: [] }; } }
			fn main() -> Int { let s = S<Int> { items: [1] }; let t: S<String> = s.map(toString); return 0; }`},
		{"explicit type arguments still accepted", `fn first<T>(xs: [T]) -> T { return xs[0]; } fn main() -> Int { return first<Int>([1, 2]); }`},
		{"cast to a function type", `fn f(v: Any) -> Int { let g = v as fn(Int) -> Int; return g(1); }`},
		{"field stays narrowed across a builtin call", `struct P { x: Int?; }
			fn f(p: P) -> Int { if p.x != null { println("found", p.x); return p.x + 1; } return 0; }`},
		{"field stays narrowed across a call that writes nothing", `struct P { x: Int?; } fn twice(n: Int) -> Int { return n * 2; }
			fn f(p: P) -> Int { if p.x != null { let t = twice(p.x); return p.x + t; } return 0; }`},
		{"field stays narrowed across calls that only read", `struct P { x: Int?; }
			fn peek(p: P) -> Bool { return p.x == null; } fn check(p: P) -> Bool { return peek(p) || peek(p); }
			fn f(p: P) -> Int { if p.x != null { check(p); return p.x + 1; } return 0; }`},
		{"field stays narrowed across a method that writes nothing", `struct P { x: Int?; } impl P { fn show() -> String { return toString(self.x); } }
			fn f(p: P) -> Int { if p.x != null { p.show(); return p.x + 1; } return 0; }`},
		{"field stays narrowed across recursion that writes nothing", `struct P { x: Int?; }
			fn count(n: Int) -> Int { if n == 0 { return 0; } return 1 + count(n - 1); }
			fn f(p: P) -> Int { if p.x != null { count(3); return p.x + 1; } return 0; }`},
		{"field stays narrowed across an element assignment", `struct P { x: Int?; }
			fn f(p: P, ps: [P]) -> Int { if p.x != null { ps[0] = p; __push(ps, p); return p.x + 1; } return 0; }`},
		{"field stays narrowed through a loop that writes nothing", `struct P { x: Int?; }
			fn f(p: P) -> Int { if p.x == null { return 0; } let i = 0; while i < 2 { i = i + p.x; println(i); } return i; }`},
		{"field stays narrowed across a trait method no struct implements by writing", `struct P { x: Int?; }
			trait Job { fn run(p: P) -> Int; } struct A { n: Int; } struct B { n: Int; }
			impl Job for A { fn run(p: P) -> Int { return 1; } } impl Job for B { fn run(p: P) -> Int { return 2; } }
			fn f(p: P, job: Job) -> Int { if p.x != null { job.run(p); return p.x + 1; } return 0; }`},
		{"type argument from a declared type", `fn none<T>() -> [T] { return []; } fn main() -> Int { let xs: [Int] = none(); return __len(xs); }`},
		{"type argument from the return type", `fn none<T>() -> [T] { return []; } fn empty() -> [String] { return none(); }`},
		{"type argument from a parameter type", `fn none<T>() -> [T] { return []; } fn size(xs: [Bool]) -> Int { return __len(xs); }
			fn main() -> Int { return size(none()); }`},
		{"type argument from a field type", `fn none<T>() -> [T] { return []; } struct S { items: [Int]; }
			fn main() -> Int { let s = S { items: none() }; s.items = none(); return 0; }`},
		{"type argument from an assignment", `fn none<T>() -> [T] { return []; } fn main() -> Int { let xs = [1]; xs = none(); return 0; }`},
		{"type argument from an optional expected type", `fn nothing<T>() -> T? { return null; } fn main() -> Int { let n: Int? = nothing(); return n ?? 0; }`},
		{"one type argument from an argument, one from the expected type", `fn convert<A, B>(a: A, f: fn(A) -> Any) -> [B] { return []; }
			fn main() -> Int { let xs: [String] = convert(1, toString); return __len(xs); }`},
		{"method type argument from the expected type", `struct U { n: Int; } impl U { fn none<T>() -> [T] { return []; } }
			fn main() -> Int { let u = U { n: 1 }; let xs: [Int] = u.none(); return __len(xs); }`},
		{"compound assignment on numbers", `fn main() -> Int { let i = 1; i += 2; i -= 1; i *= 3; i /= 2; i %= 4; i++; i--; return i; }`},
		{"compound assignment on floats and strings", `fn main() -> Int { let f = 1.5; f *= 2.0; f -= 0.5; let s = "a"; s += "b"; return __len(s); }`},
		{"compound assignment on elements and fields", `struct P { n: Int; tags: [String]; }
			fn main() -> Int { let xs = [1, 2]; xs[0] += 5; xs[1]++; let p = P { n: 1, tags: ["a"] }; p.n *= 2; p.tags[0] += "b"; p.n--; return xs[0] + p.n; }`},
		{"compound assignment on a narrowed optional", `fn f(n: Int?) -> Int { if n == null { return 0; } n += 1; n++; return n; }`},
		{"compound assignment keeps a variable non-null", `fn main() -> Int { let n: Int? = null; n = 1; n += 2; return n + 1; }`},
		{"compound assignment in a lambda", `fn main() -> Int { let total = 0; let add = fn(v: Int) -> Void { total += v; }; add(2); return total; }`},
		{"compound assignment through self", `struct C { n: Int; } impl C { fn bump(by: Int) -> Int { self.n += by; self.n++; return self.n; } }`},
		{"loop index", `fn main() -> Int { let t = 0; for i, x in [10, 20] { t += i * x; } return t; }`},
		{"loop index over a string", `fn main() -> Int { let t = 0; for i, c in "abc" { let s: String = c; t += i; } return t; }`},
		{"loop variables read, and a copy changed", `fn main() -> Int { let t = 0; for i, x in [1] { let j = i; j += x; let y = x; y *= 2; t += j + y; } return t; }`},
		{"value reached through the loop variable changed", `struct P { n: Int; items: [Int]; }
			fn main() -> Int { let ps = [P { n: 1, items: [1] }]; for i, p in ps { p.n = 5; p.n += i; p.items[0]++; __push(p.items, 2); } return ps[0].n; }`},
		{"collection changed by index inside its loop", `fn main() -> Int { let xs = [1, 2]; for i, x in xs { xs[i] = x * 2; xs[i] += 1; } return xs[0]; }`},
		{"same name reused after the loop", `fn main() -> Int { for i, x in [1] { } let i = 0; i = 1; let x = 2; x++; return i + x; }`},
		{"loop variable shadowed inside the body", `fn main() -> Int { for i, x in [1] { { let i = 5; i++; let x = "s"; x += "t"; } } return 0; }`},
		{"nested loop indexes", `fn main() -> Int { let t = 0; for i, row in [[1], [2, 3]] { for j, cell in row { t += i + j + cell; } } return t; }`},
		{"loop index captured by a lambda", `fn main() -> Int { let fs: [fn() -> Int] = []; for i, x in [1, 2] { __push(fs, fn() -> Int { return i + x; }); } return fs[0](); }`},
		{"largest and smallest Int literals", `fn main() -> Int { let big = 140737488355327; let small = -140737488355328; return 0; }`},
		{"Any compared and printed", `fn same(a: Any, b: Any) -> Bool { return a == b; }`},

		// Only one branch of an if runs: what one assigns does not concern the other
		{"else branch after a branch that assigns", `struct N { v: Int; next: N?; }
			fn f(start: N?, flag: Bool) -> Int {
				let node = start;
				while node != null {
					if flag { node = node.next; } else { return node.v; }
				}
				return 0;
			}`},
		{"else-if chain after a branch that assigns", `struct N { v: Int; next: N?; }
			fn f(n: N?, k: Int) -> Int {
				if n == null { return 0; }
				if k == 0 { n = null; } else if k == 1 { return n.v; } else { return n.v + 1; }
				return 0;
			}`},
		{"own test of each branch kept", `fn f(a: Int?, b: Int?) -> Int {
				if a != null { b = null; return a + 1; } else if b != null { a = null; return b + 1; }
				return 0;
			}`},
		{"field known in the else branch after a branch that assigns it", `struct N { v: Int; next: N?; }
			fn f(n: N, flag: Bool) -> Int {
				if n.next == null { return 0; }
				if flag { n.next = null; } else { return n.next.v; }
				return 0;
			}`},
		{"match case after a case that assigns", `fn f(n: Int?, k: Int) -> Int {
				if n == null { return 0; }
				match k {
					case 0: { n = null; }
					case 1: { return n + 1; }
					default: { return n + 2; }
				}
				return 0;
			}`},
		{"match on types after a case that assigns", `fn f(v: Any, n: Int?) -> Int {
				if n == null { return 0; }
				match v {
					case String: { n = null; return __len(v); }
					case Int: { return v + n; }
					default: { return n; }
				}
			}`},
		{"variable untouched by either branch stays narrowed", `fn f(n: Int?, m: Int?, flag: Bool) -> Int {
				if n == null { return 0; }
				if flag { m = null; } else { m = 1; }
				return n + 1;
			}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msgs, _, _ := checkSource(t, tt.src); len(msgs) != 0 {
				t.Errorf("expected no errors, got %v", msgs)
			}
		})
	}
}

func TestCheckRejectsInvalidPrograms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"assign wrong type", `fn main() -> Int { let a = 1; a = "s"; return a; }`, "cannot assign type String to Int"},
		{"assign to field", `struct P { x: Int; } fn main() -> Int { let p = P { x: 1 }; p.x = true; return 0; }`, "cannot assign type Bool to Int"},
		{"assign to element", `fn main() -> Int { let xs = [1]; xs[0] = "s"; return 0; }`, "cannot assign type String to Int"},
		{"field has wrong type", `struct P { x: Int; } fn main() -> Int { let p = P { x: "s" }; return 0; }`, "cannot use type String for field 'x'"},
		{"unknown field in literal", `struct P { x: Int; } fn main() -> Int { let p = P { y: 1 }; return 0; }`, "struct P has no field 'y'"},
		{"non-int index", `fn main() -> Int { let xs = [1]; return xs["a"]; }`, "index must be Int, got String"},
		{"field type flows on", `struct P { x: Int; } fn main() -> Int { let p = P { x: 1 }; let s: String = p.x; return 0; }`, "cannot assign type Int to String"},
		{"lambda return", `fn main() -> Int { let f = fn() -> Int { return "s"; }; return 0; }`, "cannot return type String"},
		{"lambda argument", `fn main() -> Int { let f = fn(a: Int) -> Int { return a; }; return f("s"); }`, "cannot use type String as argument of type Int"},
		{"variadic element", `fn f(rest: ...Int) -> Int { return 0; } fn main() -> Int { return f(1, "s"); }`, "cannot use type String as argument of type Int"},
		{"array of wrong element", `fn f() -> [Int] { return ["s"]; }`, "array element has type String, expected Int"},
		{"array passed on with wrong element", `fn f() -> [Int] { let xs = ["s"]; return xs; }`, "cannot return type [String]"},
		{"unknown method", `struct P { x: Int; } fn main() -> Int { let p = P { x: 1 }; return p.nope(); }`, "P has no method 'nope'"},
		{"misspelled method", `struct P { x: Int; } impl P { fn total() -> Int { return self.x; } }
			fn main() -> Int { let p = P { x: 1 }; return p.totl(); }`, "P has no method 'totl'"},
		{"method on a number", `fn main() -> Int { let n = 1; return n.abs(); }`, "a number has no method 'abs'"},
		{"method argument count", `struct P { x: Int; } impl P { fn get() -> Int { return self.x; } }
			fn main() -> Int { let p = P { x: 1 }; return p.get(1); }`, "expected 0 arguments, got 1"},
		{"method result type", `struct P { x: Int; } impl P { fn name() -> String { return "p"; } }
			fn main() -> Int { let p = P { x: 1 }; return p.name(); }`, "cannot return type String"},
		{"Self is the implementing struct", `struct P { x: Int; } struct Q { x: Int; }
			impl P { fn copy() -> Self { return Q { x: 1 }; } }`, "cannot return type Q from function returning P"},
		{"duplicate method", `struct P { x: Int; } impl P { fn m() -> Int { return 1; } } impl P { fn m() -> Int { return 2; } }`, "method 'm' is already defined for P"},
		{"struct without the trait", `trait T { fn m() -> Int; } struct P { x: Int; }
			fn main() -> Int { let t: T = P { x: 1 }; return 0; }`, "cannot assign type P to T"},
		{"trait instantiated", `trait T { fn m() -> Int; } fn main() -> Int { let t = T { }; return 0; }`, "'T' is a trait and cannot be instantiated"},
		{"missing fields", `struct P { x: Int; y: Int; z: Int; } fn main() -> Int { let p = P { y: 1 }; return 0; }`, "missing 'x', 'z'"},
		{"null to non-optional", `fn main() -> Int { let s: String = null; return 0; }`, "cannot assign type null to String"},
		{"optional to non-optional", `fn f(n: Int?) -> Int { return n; }`, "cannot return type Int? from function returning Int"},
		{"method on optional", `struct P { x: Int; } impl P { fn get() -> Int { return self.x; } }
			fn f(p: P?) -> Int { return p.get(); }`, "cannot call 'get' on P?: the value may be null"},
		{"index of optional", `fn f(xs: [Int]?) -> Int { return xs[0]; }`, "cannot index [Int]?: the value may be null"},
		{"Any is not a number", `fn f(v: Any) -> Int { return v * 2; }`, "operator * not defined for types Any and Int"},
		{"Any has no fields", `fn f(v: Any) -> Int { return v.x; }`, "Any has no field 'x'"},
		{"Any is not callable", `fn f(v: Any) -> Int { return v(); }`, "cannot call non-function type Any"},
		{"Any cannot be iterated", `fn f(v: Any) -> Int { for x in v { } return 0; }`, "cannot iterate over type Any"},
		{"type parameter is opaque", `fn f<T>(v: T) -> Int { return v; }`, "cannot return type T from function returning Int"},
		{"generic struct field", `struct Box<T> { value: T; } fn main() -> Int { let b = Box<Int> { value: "s" }; return 0; }`, "cannot use type String for field 'value' of type Int"},
		{"generic struct field read", `struct Box<T> { value: T; }
			fn main() -> Int { let b = Box<String> { value: "s" }; return b.value; }`, "cannot return type String"},
		{"function accepting less than required", `fn half(n: Int) -> Int { return n / 2; }
			fn apply(f: fn(Any) -> Int) -> Int { return f("s"); }
			fn main() -> Int { return apply(half); }`, "cannot use type fn(Int) -> Int as argument of type fn(Any) -> Int"},
		{"untyped empty array", `fn main() -> Int { let xs = []; return 0; }`, "cannot infer the element type of '[]'"},
		{"optional not narrowed outside the test", `fn f(n: Int?) -> Int { if n != null { } return n + 1; }`, "the value may be null"},
		{"narrowing ends at an assignment", `fn f(n: Int?, m: Int?) -> Int { if n != null { n = m; return n + 1; } return 0; }`, "the value may be null"},
		{"narrowing does not survive a loop that assigns", `fn f(n: Int?) -> Int { if n == null { return 0; } let i = 0; while i < 2 { i = i + n; n = null; } return 0; }`, "the value may be null"},
		{"narrowing does not reach into a lambda for a variable assigned later", `fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> Int { return n + 1; }; n = null; return g(); }`, "the value may be null"},
		{"...or assigned by another lambda", `fn f(n: Int?) -> Int { if n == null { return 0; } let g = fn() -> Int { return n + 1; }; let h = fn() { n = null; }; h(); return g(); }`, "the value may be null"},
		{"...or assigned before the test", `fn f(n: Int?, m: Int?) -> Int { n = m; if n == null { return 0; } let g = fn() -> Int { return n + 1; }; return g(); }`, "the value may be null"},
		{"wrong branch narrowed", `fn f(n: Int?) -> Int { if n == null { return n + 1; } return 0; }`, "the value may be null"},
		{"assigned in the then branch, used after the if", `fn f(n: Int?, flag: Bool) -> Int { if n == null { return 0; } if flag { n = null; } else { } return n + 1; }`, "the value may be null"},
		{"assigned in the else branch, used after the if", `fn f(n: Int?, flag: Bool) -> Int { if n == null { return 0; } if flag { } else { n = null; } return n + 1; }`, "the value may be null"},
		{"assigned in the else branch, used later in it", `fn f(n: Int?, flag: Bool) -> Int { if n == null { return 0; } if flag { return n; } else { n = null; return n + 1; } }`, "the value may be null"},
		{"assigned in a match case, used after the match", `fn f(n: Int?, k: Int) -> Int { if n == null { return 0; } match k { case 0: { n = null; } default: { } } return n + 1; }`, "the value may be null"},
		{"assigned in a match case, used later in it", `fn f(n: Int?, k: Int) -> Int { if n == null { return 0; } match k { case 0: { return n; } default: { n = null; return n + 1; } } }`, "the value may be null"},
		{"field assigned in a branch, read after the if", `struct N { v: Int; next: N?; }
			fn f(n: N, flag: Bool) -> Int { if n.next == null { return 0; } if flag { n.next = null; } else { } return n.next.v; }`, "the value may be null"},
		{"fact of the then branch does not reach the else branch", `fn f(n: Int?, m: Int?) -> Int { if n != null { return n; } else { return n + 1; } }`, "the value may be null"},
		{"assign to a constant", `fn main() -> Int { const c = 1; c = 2; return c; }`, "cannot assign to constant 'c'"},
		{"assign to a constant in a lambda", `fn main() -> Int { const c = 1; let f = fn() -> Void { c = 2; }; return c; }`, "cannot assign to constant 'c'"},
		{"missing return", `fn f(n: Int) -> Int { if n > 0 { return 1; } }`, "function 'f' can reach its end without returning a value"},
		{"missing return in a branch", `fn f(n: Int) -> Int { if n > 0 { return 1; } else { let a = 2; } }`, "can reach its end"},
		{"missing return after a loop", `fn f(n: Int) -> Int { while n > 0 { return 1; } }`, "can reach its end"},
		{"missing return when a loop breaks", `fn f() -> Int { while true { break; } }`, "can reach its end"},
		{"missing return without default", `fn f(n: Int) -> Int { match n { case 0: { return 1; } } }`, "can reach its end"},
		{"missing return in a lambda", `fn main() -> Int { let f = fn(n: Int) -> Int { let a = n; }; return 0; }`, "function can reach its end"},
		{"missing return in a method", `struct P { x: Int; } impl P { fn get() -> Int { let a = self.x; } }`, "function 'get' can reach its end"},
		{"impossible cast", `fn f(n: Int) -> String { return n as String; }`, "cannot cast Int to String"},
		{"cast between unrelated structs", `struct P { x: Int; } struct Q { x: Int; } fn f(p: P) -> Q { return p as Q; }`, "cannot cast P to Q"},
		{"cast to a struct without the trait", `trait T { fn m() -> Int; } struct P { x: Int; } fn f(t: T) -> P { return t as P; }`, "cannot cast T to P"},
		{"result of a cast is typed", `fn f(v: Any) -> String { return v as Int; }`, "cannot return type Int"},
		{"generic method argument", `struct Box<T> { value: T; } impl<T> Box<T> { fn set(v: T) -> Void { self.value = v; } }
			fn main() -> Int { let b = Box<Int> { value: 1 }; b.set("s"); return 0; }`, "cannot use type String as argument of type Int"},
		{"generic method result", `struct Box<T> { value: T; } impl<T> Box<T> { fn get() -> T { return self.value; } }
			fn main() -> Int { let b = Box<String> { value: "s" }; return b.get(); }`, "cannot return type String"},
		{"generic structs differ by argument", `struct Box<T> { value: T; }
			fn main() -> Int { let b: Box<String> = Box<Int> { value: 1 }; return 0; }`, "cannot assign type Box<Int> to Box<String>"},
		{"type parameter opaque in a method", `struct Box<T> { value: T; } impl<T> Box<T> { fn bad() -> Int { return self.value; } }`, "cannot return type T from function returning Int"},
		{"method type argument that nothing determines", `struct U { n: Int; } impl U { fn none<T>() -> [T] { return []; } }
			fn main() -> Int { let u = U { n: 1 }; return __len(u.none()); }`, "cannot infer the type arguments of method 'none'"},
		{"method takes no type arguments", `struct U { n: Int; } impl U { fn get() -> Int { return self.n; } }
			fn main() -> Int { let u = U { n: 1 }; return u.get<Int>(); }`, "method 'get' takes no type arguments"},
		{"wrong number of type arguments", `struct U { n: Int; } impl U { fn id<T>(v: T) -> T { return v; } }
			fn main() -> Int { let u = U { n: 1 }; return u.id<Int, Int>(1); }`, "method 'id' takes 1 type arguments (<T>), got 2"},
		{"generic method argument checked", `struct U { n: Int; } impl U { fn id<T>(v: T) -> T { return v; } }
			fn main() -> Int { let u = U { n: 1 }; return u.id<Int>("s"); }`, "cannot use type String as argument of type Int"},
		{"generic method result checked", `struct U { n: Int; } impl U { fn id<T>(v: T) -> T { return v; } }
			fn main() -> Int { let u = U { n: 1 }; return u.id<String>("s"); }`, "cannot return type String"},
		{"generic method result uses both parameters", `struct Stack<T> { items: [T]; }
			impl<T> Stack<T> { fn map<U>(f: fn(T) -> U) -> Stack<U> { return Stack<U> { items: [] }; } }
			fn main() -> Int { let s = Stack<Int> { items: [1] }; let t: Stack<Int> = s.map<String>(toString); return 0; }`, "cannot assign type Stack<String> to Stack<Int>"},
		{"type parameter opaque in a generic method", `struct U { n: Int; } impl U { fn bad<T>(v: T) -> Int { return v; } }`, "cannot return type T from function returning Int"},
		{"undefined type argument", `struct U { n: Int; } impl U { fn id<T>(v: T) -> T { return v; } }
			fn main() -> Int { let u = U { n: 1 }; return 0; }
			fn g(u: U) -> Int { let b: Bool = u.id<Bool>(true); return u.id<Int>(b); }`, "cannot use type Bool as argument of type Int"},
		{"variable assigned by a lambda is not narrowed", `fn f(n: Int?) -> Int {
			let clear = fn() -> Void { n = null; };
			if n != null { clear(); return n + 1; } return 0; }`, "the value may be null"},
		{"shared variable not narrowed after early return", `fn f(n: Int?) -> Int {
			let clear = fn() -> Void { n = null; };
			if n == null { return 0; } clear(); return n + 1; }`, "the value may be null"},
		{"shared variable not narrowed by assignment", `fn main() -> Int {
			let n: Int? = null; let clear = fn() -> Void { n = null; };
			n = 1; clear(); return n + 1; }`, "the value may be null"},
		{"variable assigned by a nested lambda is shared", `fn f(n: Int?) -> Int {
			let outer = fn() -> Void { let inner = fn() -> Void { n = null; }; inner(); };
			if n != null { outer(); return n + 1; } return 0; }`, "the value may be null"},
		{"trait type parameter count differs", `trait C { fn conv<T>(v: T) -> T; } struct W { n: Int; }
			impl C for W { fn conv<A, B>(v: A) -> A { return v; } }`, "method 'conv' has 2 type parameters, trait requires 1"},
		{"trait type parameter missing in the impl", `trait C { fn conv<T>(v: T) -> T; } struct W { n: Int; }
			impl C for W { fn conv(v: Int) -> Int { return v; } }`, "method 'conv' has 0 type parameters, trait requires 1"},
		{"renamed type parameters used differently", `trait P { fn pair<A, B>(a: A, b: B) -> A; } struct W { n: Int; }
			impl P for W { fn pair<X, Y>(a: X, b: Y) -> Y { return b; } }`, "method 'pair' returns B, trait requires A"},
		{"field narrowing ends at a call", `struct P { x: Int?; } fn touch(p: P) -> Void { p.x = null; }
			fn f(p: P) -> Int { if p.x != null { touch(p); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a method call", `struct P { x: Int?; } impl P { fn clear() -> Void { self.x = null; } }
			fn f(p: P) -> Int { if p.x != null { p.clear(); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a field assignment", `struct P { x: Int?; }
			fn f(p: P, q: P) -> Int { if p.x != null { q.x = null; return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends when the variable is assigned", `struct P { x: Int?; }
			fn f(p: P, q: P) -> Int { if p.x != null { p = q; return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing does not survive a loop that writes fields", `struct P { x: Int?; }
			fn f(p: P, q: P) -> Int { if p.x == null { return 0; } let i = 0; while i < 2 { i = i + p.x; q.x = null; } return 0; }`, "the value may be null"},
		{"field narrowing does not survive a loop that calls a writer", `struct P { x: Int?; } fn touch(p: P) -> Void { p.x = null; }
			fn f(p: P) -> Int { if p.x == null { return 0; } let i = 0; while i < 2 { i = i + p.x; touch(p); } return 0; }`, "the value may be null"},
		{"field narrowing ends at a call that writes indirectly", `struct P { x: Int?; }
			fn inner(p: P) -> Void { p.x = null; } fn middle(p: P) -> Void { inner(p); } fn outer(p: P) -> Void { middle(p); }
			fn f(p: P) -> Int { if p.x != null { outer(p); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a call of a function value", `struct P { x: Int?; }
			fn f(p: P, g: fn() -> Void) -> Int { if p.x != null { g(); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a call of a lambda", `struct P { x: Int?; }
			fn f(p: P) -> Int { let clear = fn() -> Void { p.x = null; }; if p.x != null { clear(); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a call of a function in a field", `struct P { x: Int?; run: fn() -> Void; }
			fn f(p: P) -> Int { if p.x != null { p.run(); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a trait method some struct implements by writing", `struct P { x: Int?; }
			trait Job { fn run(p: P) -> Void; } struct Quiet { n: Int; } struct Loud { n: Int; }
			impl Job for Quiet { fn run(p: P) -> Void { return; } } impl Job for Loud { fn run(p: P) -> Void { p.x = null; } }
			fn f(p: P, job: Job) -> Int { if p.x != null { job.run(p); return p.x + 1; } return 0; }`, "the value may be null"},
		{"field narrowing ends at a recursive writer", `struct P { x: Int?; }
			fn drain(p: P, n: Int) -> Void { if n > 0 { drain(p, n - 1); } else { p.x = null; } }
			fn f(p: P) -> Int { if p.x != null { drain(p, 3); return p.x + 1; } return 0; }`, "the value may be null"},
		{"expected type does not override the arguments", `fn pick<T>(a: T) -> T { return a; }
			fn main() -> Int { let s: String = pick(1); return 0; }`, "cannot assign type Int to String"},
		{"explicit type argument against the expected type", `fn none<T>() -> [T] { return []; }
			fn main() -> Int { let xs: [Int] = none<String>(); return 0; }`, "cannot assign type [String] to [Int]"},
		{"other field of the same value is not narrowed", `struct P { x: Int?; y: Int?; } fn f(p: P) -> Int { if p.x != null { return p.y + 1; } return 0; }`, "the value may be null"},
		{"same field of another value is not narrowed", `struct P { x: Int?; } fn f(p: P, q: P) -> Int { if p.x != null { return q.x + 1; } return 0; }`, "the value may be null"},
		{"type test that can never succeed", `fn f(n: Int) -> Bool { return n is String; }`, "Int can never be String"},
		{"type test does not narrow the other branch", `fn f(v: Any) -> Int { if v is Int { return 0; } return v + 1; }`, "operator + not defined for types Any and Int"},
		{"type test does not narrow a shared variable", `fn f(v: Any) -> Int { let reset = fn() -> Void { v = "s"; }; if v is Int { reset(); return v + 1; } return 0; }`, "operator + not defined for types Any and Int"},
		{"match on a type that can never match", `fn f(n: Int) -> Int { match n { case String: { return 1; } default: { return 0; } } }`, "Int can never be String"},
		{"match case sees only its own type", `fn f(v: Any) -> Int { match v { case String: { return v + 1; } default: { return 0; } } }`, "operator + not defined for types String and Int"},
		{"inferred type argument conflicts with a later argument", `fn same<T>(a: T, b: T) -> T { return a; } fn main() -> Int { return same(1, "two"); }`, "cannot use type String as argument of type Int"},
		{"inferred result is checked", `fn first<T>(xs: [T]) -> T { return xs[0]; } fn main() -> Int { return first(["a"]); }`, "cannot return type String"},
		{"compound assignment of another type", `fn main() -> Int { let i = 1; i += "a"; return i; }`, "operator + not defined for types Int and String"},
		{"compound assignment mixing Int and Float", `fn main() -> Int { let i = 1; i *= 2.5; return i; }`, "cannot assign the Float result of *= to Int"},
		{"compound assignment not defined for the type", `fn main() -> Int { let s = "a"; s -= "b"; return 0; }`, "operator - not defined for types String and String"},
		{"compound assignment on a Bool", `fn main() -> Int { let b = true; b += 1; return 0; }`, "operator + not defined for types Bool and Int"},
		{"increment of a String", `fn main() -> Int { let s = "a"; s++; return 0; }`, "operator + not defined for types String and Int"},
		{"compound assignment to a constant", `fn main() -> Int { const c = 1; c += 1; return c; }`, "cannot assign to constant 'c'"},
		{"increment of a constant", `fn main() -> Int { const c = 1; c++; return c; }`, "cannot assign to constant 'c'"},
		{"compound assignment on an optional", `fn f(n: Int?) -> Int { n += 1; return 0; }`, "the value may be null"},
		{"compound assignment on an optional field", `struct P { n: Int?; } fn f(p: P) -> Void { p.n += 1; }`, "the value may be null"},
		{"compound assignment on a wrong element type", `fn main() -> Int { let xs = ["a"]; xs[0] += 1; return 0; }`, "operator + not defined for types String and Int"},
		{"compound assignment ends narrowing of fields", `struct P { x: Int?; n: Int; }
			fn f(p: P, q: P) -> Int { if p.x != null { q.n += 1; return p.x + 1; } return 0; }`, "the value may be null"},
		{"function with a compound field assignment is a writer", `struct P { x: Int?; n: Int; } fn bump(p: P) -> Void { p.n++; }
			fn f(p: P) -> Int { if p.x != null { bump(p); return p.x + 1; } return 0; }`, "the value may be null"},
		{"variable compound-assigned by a lambda is shared", `fn f(n: Int?) -> Int { let bump = fn() -> Void { if n != null { n += 1; } };
			if n != null { bump(); return n + 1; } return 0; }`, "the value may be null"},
		{"loop index is an Int", `fn main() -> Int { for i, x in ["a"] { let s: String = i; } return 0; }`, "cannot assign type Int to String"},
		{"integer literal too large", `fn main() -> Int { let n = 140737488355328; return 0; }`, "integer literal 140737488355328 is out of range"},
		{"negative integer literal too small", `fn main() -> Int { let n = -140737488355329; return 0; }`, "integer literal 140737488355329 is out of range"},
		{"integer literal far too large", `fn main() -> Int { let n = 99999999999999999999999; return 0; }`, "is out of range"},
		{"assignment to a loop index", `fn main() -> Int { for i, x in [1] { i = 5; } return 0; }`, "cannot assign to loop index 'i'"},
		{"compound assignment to a loop index", `fn main() -> Int { for i, x in [1] { i += 1; } return 0; }`, "cannot assign to loop index 'i'"},
		{"increment of a loop index", `fn main() -> Int { for i, x in [1] { i++; } return 0; }`, "cannot assign to loop index 'i'"},
		{"assignment to a loop variable", `fn main() -> Int { for x in [1] { x = 5; } return 0; }`, "cannot assign to loop variable 'x'"},
		{"compound assignment to a loop variable", `fn main() -> Int { for x in [1] { x *= 2; } return 0; }`, "cannot assign to loop variable 'x'"},
		{"decrement of a loop variable", `fn main() -> Int { for i, x in [1] { x--; } return 0; }`, "cannot assign to loop variable 'x'"},
		{"assignment to a character of a string loop", `fn main() -> Int { for c in "ab" { c = "z"; } return 0; }`, "cannot assign to loop variable 'c'"},
		{"assignment to a loop variable in a nested block", `fn main() -> Int { for i, x in [1] { if x > 0 { while true { i = 0; break; } } } return 0; }`, "cannot assign to loop index 'i'"},
		{"assignment to a loop variable from a lambda", `fn main() -> Int { for i, x in [1] { let f = fn() -> Void { x = 2; }; f(); } return 0; }`, "cannot assign to loop variable 'x'"},
		{"assignment to an outer loop's index", `fn main() -> Int { for i, row in [[1]] { for j, cell in row { i = j; } } return 0; }`, "cannot assign to loop index 'i'"},
		{"push wrong element", `fn main() -> Int { let xs: [Int] = []; __push(xs, true); return 0; }`, "cannot push type Bool onto [Int]"},
		{"push onto a non-array", `fn main() -> Int { __push(1, 2); return 0; }`, "push expects an array, got Int"},
		{"mixed arithmetic is a Float", `fn main() -> Int { return 1 + 2.5; }`, "cannot return type Float from function returning Int"},
		{"a number and a text", `fn main() -> Int { return 1 + "2"; }`, "operator + not defined for types Int and String"},
		{"a Float is not taken for a whole number", `fn main() -> Int { let n: Int = 2.0; return n; }`, "cannot assign type Float to Int"},
		{"nor where an index is expected", `fn main() -> Int { let xs = [1]; return xs[0.0]; }`, "Float"},
		{"a remainder of a text", `fn main() -> Int { let r = "a" % 2; return 0; }`, "operator % not defined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs, _, _ := checkSource(t, tt.src)
			if len(msgs) == 0 {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(msgs[0], tt.want) {
				t.Errorf("expected error containing %q, got %q", tt.want, msgs[0])
			}
		})
	}
}

func TestCastDescriptors(t *testing.T) {
	decls := `
		struct Point { x: Int; }
		struct Box<T> { value: T; label: String; }
		struct Pair<A, B> { first: A; second: [B]; count: Int; }
		struct List<T> { head: T; tail: List<T>?; }
		struct Tag<T> { name: String; }
		trait Shape { fn area() -> Int; }
		struct Square { s: Int; } struct Circle { r: Int; } struct Line { l: Int; }
		impl Shape for Square { fn area() -> Int { return 1; } }
		impl Shape for Circle { fn area() -> Int { return 2; } }
		trait Unused { fn nothing() -> Int; }
	`
	tests := []struct {
		source string // type of the value being cast
		target string
		want   string // "" when the cast needs no check
	}{
		{"Any", "Int", "int"},
		{"Any", "String?", "?string"},
		{"Any", "[Int]", "[int]"},
		{"Any", "[[String]?]", "[?[string]]"},
		{"Any", "[Any]", "[any]"},
		{"Any", "fn(Int) -> Int", "fn(int)->int"},
		{"Any", "fn(String, [Int]) -> Bool?", "fn(string,[int])->?bool"},
		{"Any", "fn() -> Void", "fn()->void"},
		{"Any", "fn(fn(Int) -> Int) -> Point", "fn(fn(int)->int)->{Point}"},
		{"Any", "[fn(Any) -> String]", "[fn(any)->string]"},
		{"Any", "Point", "{Point}"},
		{"Any", "[Point]", "[{Point}]"},
		{"Any", "Shape", "{Circle|Square}"},
		{"Any", "Unused", "{}"},
		{"Shape", "Square", "{Square}"},
		{"Int?", "Int", "int"},
		// Generic structs: the fields that depend on the type arguments
		{"Any", "Box<Int>", "{Box:value=int}"},
		{"Any", "Box<[Point]>", "{Box:value=[{Point}]}"},
		{"Any", "Pair<Int, String>", "{Pair:first=int,second=[string]}"},
		{"Any", "Box<Box<Bool>>", "{Box:value={Box:value=bool}}"},
		// A struct containing itself is described one level deep
		{"Any", "List<Int>", "{List:head=int,tail=?{List}}"},
		// No field depends on T: only the struct is verified
		{"Any", "Tag<Int>", "{Tag}"},
		// Always succeeds: nothing to verify
		{"Int", "Any", ""},
		{"Int", "Int?", ""},
		{"Square", "Shape", ""},
		{"[Int]", "[Int]", ""},
		{"Box<Int>", "Box<Int>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.source+" as "+tt.target, func(t *testing.T) {
			src := decls + "fn f(v: " + tt.source + ") -> Void { let r = v as " + tt.target + "; }"
			msgs, info, prog := checkSource(t, src)
			if len(msgs) != 0 {
				t.Fatalf("unexpected errors: %v", msgs)
			}

			var cast *ast.CastExpr
			for _, decl := range prog.Decls {
				if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == "f" {
					cast = fn.Body.Stmts[0].(*ast.LetStmt).Value.(*ast.CastExpr)
				}
			}
			got, checked := info.Casts[cast]
			if checked != (tt.want != "") || got != tt.want {
				t.Errorf("expected descriptor %q, got %q (checked: %v)", tt.want, got, checked)
			}
		})
	}
}

func TestUnreachableCodeIsAWarning(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		lines []int // line of each expected warning
	}{
		{"after return", "fn f() -> Int {\n  return 1;\n  let a = 2;\n  let b = 3;\n}", []int{3}},
		{"after break", "fn f() -> Void {\n  while true {\n    break;\n    f();\n  }\n}", []int{4}},
		{"after continue", "fn f() -> Void {\n  for x in [1] {\n    continue;\n    f();\n  }\n}", []int{4}},
		{"after an if that returns both ways", "fn f(n: Int) -> Int {\n  if n > 0 { return 1; } else { return 2; }\n  return 3;\n}", []int{3}},
		{"after an endless loop", "fn f() -> Int {\n  while true { }\n  return 3;\n}", []int{3}},
		{"after a match that always returns", "fn f(n: Int) -> Int {\n  match n { case 0: { return 1; } default: { return 2; } }\n  return 3;\n}", []int{3}},
		{"once per block, in each block", "fn f(n: Int) -> Int {\n  if n > 0 {\n    return 1;\n    f(1);\n    f(2);\n  }\n  return 2;\n  f(3);\n}", []int{4, 8}},
		{"reachable: if without else", "fn f(n: Int) -> Int {\n  if n > 0 { return 1; }\n  return 2;\n}", nil},
		{"reachable: loop with break", "fn f() -> Int {\n  while true { break; }\n  return 2;\n}", nil},
		{"reachable: match without default", "fn f(n: Int) -> Int {\n  match n { case 0: { return 1; } }\n  return 2;\n}", nil},
		{"reachable: return is the last statement", "fn f() -> Int {\n  let a = 1;\n  return a;\n}", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := errors.NewHandler()
			prog := parser.Parse(tt.src, "test.qi", errs)
			table := resolve.NewResolver(errs).Resolve(prog)
			NewChecker(prog, table, errs).Check()

			// Unreachable code does not stop compilation
			if errs.HasErrors() {
				t.Fatalf("expected warnings only, got %v", errs.Diagnostics)
			}

			var lines []int
			for _, d := range errs.Diagnostics {
				if d.Code != ErrUnreachableCode || d.Severity != errors.SeverityWarning {
					t.Errorf("unexpected diagnostic: %s %s %s", d.Severity, d.Code, d.Message)
				}
				lines = append(lines, d.Pos.Line)
			}
			if fmt.Sprint(lines) != fmt.Sprint(tt.lines) {
				t.Errorf("expected warnings on lines %v, got %v", tt.lines, lines)
			}
		})
	}
}

func TestInferredTypeArgumentsAreWrittenIntoTheProgram(t *testing.T) {
	// Later stages (monomorphization) only look at explicit type arguments,
	// so the checker fills them in.
	decls := `
		struct Box<T> { value: T; }
		struct S<T> { items: [T]; }
		impl<T> S<T> { fn map<U>(f: fn(T) -> U) -> S<U> { return S<U> { items: [] }; } }
		fn first<T>(xs: [T]) -> T { return xs[0]; }
		fn map<T, U>(xs: [T], f: fn(T) -> U) -> [U] { let out: [U] = []; return out; }
		fn collect<T>(items: ...T) -> [T] { return items; }
		fn orElse<T>(v: T?, d: T) -> T { return v ?? d; }
		fn unbox<T>(b: Box<T>) -> T { return b.value; }
		fn none<T>() -> [T] { return []; }
		fn len(s: String) -> Int { return __len(s); }
	`
	tests := []struct {
		call string
		want string // the call as it reads after checking
	}{
		{`first([1, 2])`, `first<Int>([1, 2])`},
		{`first([[true]])`, `first<[Bool]>([[true]])`},
		{`first(["a"])`, `first<String>(["a"])`},
		{`map([1], toString)`, `map<Int, String>([1], toString)`},
		{`map(["a"], len)`, `map<String, Int>(["a"], len)`},
		{`collect(1.5, 2.5)`, `collect<Float>(1.5, 2.5)`},
		{`orElse(null, 3)`, `orElse<Int>(null, 3)`},
		{`unbox(Box<String> { value: "s" })`, `unbox<String>(Box<String> { value: "s" })`},
		{`first([Box<Int> { value: 1 }])`, `first<Box<Int>>([Box<Int> { value: 1 }])`},
		{`S<Int> { items: [] }.map(toString)`, `S<Int> {  }.map<String>(toString)`},
		// Written out: left as it is
		{`first<Int>([1])`, `first<Int>([1])`},
		// Nothing determines T: left for the error that follows
		{`none()`, `none()`},
	}

	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			src := decls + "fn main() -> Void { let r = " + tt.call + "; }"
			errs := errors.NewHandler()
			prog := parser.Parse(src, "test.qi", errs)
			table := resolve.NewResolver(errs).Resolve(prog)
			NewChecker(prog, table, errs).Check()

			var got string
			for _, decl := range prog.Decls {
				if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == "main" {
					got = fn.Body.Stmts[0].(*ast.LetStmt).Value.String()
				}
			}
			// Struct literals print their fields in map order; compare without them
			strip := func(s string) string {
				for {
					i := strings.Index(s, "{ ")
					j := strings.Index(s, " }")
					if i < 0 || j < i {
						return s
					}
					s = s[:i] + "{}" + s[j+2:]
				}
			}
			if strip(got) != strip(tt.want) {
				t.Errorf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestFunctionSignaturesAreRecorded(t *testing.T) {
	src := `
		struct P { x: Int; }
		fn add(a: Int, b: Int) -> Int { return a + b; }
		fn log(prefix: String, values: ...Any) -> Void { return; }
		fn id<T>(v: T) -> T { return v; }
		fn make() -> P? { return null; }
		fn main() -> Int {
			let f = fn(s: String) -> [Int] { return [__len(s)]; };
			return 0;
		}
	`
	msgs, info, prog := checkSource(t, src)
	if len(msgs) != 0 {
		t.Fatalf("unexpected errors: %v", msgs)
	}

	want := map[string]string{
		"add":  "fn(int,int)->int",
		"log":  "fn(string,...any)->void",
		"id":   "fn(any)->any", // A type parameter is not known at runtime
		"make": "fn()->?{P}",
		"main": "fn()->int",
	}
	for _, decl := range prog.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		if got := info.Signatures[fn]; got != want[fn.Name] {
			t.Errorf("%s: expected signature %q, got %q", fn.Name, want[fn.Name], got)
		}
		if fn.Name == "main" {
			lambda := fn.Body.Stmts[0].(*ast.LetStmt).Value
			if got := info.Signatures[lambda]; got != "fn(string)->[int]" {
				t.Errorf("lambda: expected signature fn(string)->[int], got %q", got)
			}
		}
	}
}

func TestLoopVariableAssignmentSuggestsTheFix(t *testing.T) {
	tests := []struct {
		src  string
		want string // part of the suggestion
	}{
		{`fn main() -> Void { for i, x in [1] { i = 0; } }`, "use a while loop to control the position"},
		// With an index at hand, the suggestion spells out the assignment that works
		{`fn main() -> Void { let scores = [1]; for i, score in scores { score = 0; } }`, "assign to scores[i]"},
		{`fn main() -> Void { let scores = [1]; for score in scores { score = 0; } }`, "assign to it by index"},
	}

	for _, tt := range tests {
		errs := errors.NewHandler()
		prog := parser.Parse(tt.src, "test.qi", errs)
		table := resolve.NewResolver(errs).Resolve(prog)
		NewChecker(prog, table, errs).Check()

		if len(errs.Diagnostics) != 1 {
			t.Errorf("%s: expected exactly one diagnostic, got %v", tt.src, errs.Diagnostics)
			continue
		}
		d := errs.Diagnostics[0]
		if d.Code != ErrAssignToLoopVar || !strings.Contains(d.Suggestion, tt.want) {
			t.Errorf("%s: expected %s with a suggestion containing %q, got %s: %q", tt.src, ErrAssignToLoopVar, tt.want, d.Code, d.Suggestion)
		}
	}
}
