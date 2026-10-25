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
