package fuzz

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/sandbox"
)

// A program in the sandbox may take items out of its arrays: the natives of
// std.arrays only compute. Without that, a program found by the fuzzers
// that pops would be stopped for using the library, not run.
func TestSandboxPermitsChangingArrays(t *testing.T) {
	for _, name := range []string{"std.arrays.cutLast", "std.arrays.cutAt", "std.arrays.putAt", "std.arrays.cutAll"} {
		if !sandbox.Permitted(name) {
			t.Errorf("expected %s to be permitted", name)
		}
	}
	for _, name := range []string{"std.fs.read", "std.os.run", "std.http.get", "std.arraysx.cutLast"} {
		if sandbox.Permitted(name) {
			t.Errorf("expected %s not to be permitted", name)
		}
	}

	chunk := compile(`
		import std.arrays;
		import std.list;

		fn main() -> Int {
			let xs = [1, 2, 3, 4];
			let last = xs.pop();
			let first = xs.removeAt(0);
			xs.insertAt(1, 9);
			println(last, first, xs);
			xs.clear();
			let names = list.of(["a", "b"]);
			names.insert(0, "z");
			println(xs, names.pop(), names.removeAt(0), names.toArray());
			return 0;
		}
	`)
	if chunk == nil {
		t.Fatal("the program does not compile")
	}
	out, err := run(t, chunk)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if want := "4 1 [2, 9, 3]\n[] b z [\"a\"]\n"; out != want {
		t.Errorf("expected %q, got %q", want, strings.TrimSpace(out))
	}
}
