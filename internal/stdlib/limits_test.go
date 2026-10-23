package stdlib_test

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/sandbox"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// allocated is how much Go allocated while f ran.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A text doubled again and again is refused once its bytes would not fit
// under the limit: not once its first reader has asked the machine for
// all of them.
func TestATextDoubledPastTheLimitIsRefused(t *testing.T) {
	chunk := compiled(t, `
fn main() -> Int {
    let s = "a".repeat(1024);
    for _ in 0..20 {
        s = s + s;
    }
    println(s.byteLength());
    println(s.contains("b"));
    return 0;
}`)
	var err error
	used := allocated(func() { _, err = sandbox.Run(chunk, sandbox.Limits{HeapBytes: 1 << 20}) })
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("a text of a gigabyte under a megabyte: %v", err)
	}
	if used > 64<<20 {
		t.Errorf("Go allocated %d MB for a program that may hold 1 MB", used>>20)
	}
}

// A native that builds a text asks for room before it builds it.
func TestANativeAsksForRoomFirst(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"join", `
    let big = "x".repeat(1000000);
    let parts: [String] = [];
    for _ in 0..2000 {
        parts.push(big);
    }
    println(parts.join(",").byteLength());`},
		{"replace", `
    let big = "x".repeat(1000000);
    println("a".repeat(2000).replace("a", big).byteLength());`},
		{"repeat", `
    println("ab".repeat(100000000000000).byteLength());`},
	} {
		t.Run(c.name, func(t *testing.T) {
			chunk := compiled(t, "fn main() -> Int {"+c.body+"\n    return 0;\n}")
			var err error
			used := allocated(func() { _, err = sandbox.Run(chunk, sandbox.Limits{}) })
			if err == nil || !strings.Contains(err.Error(), "out of memory") {
				t.Fatalf("%s past the limit: %v", c.name, err)
			}
			if used > 256<<20 {
				t.Errorf("Go allocated %d MB for a program that may hold 16 MB", used>>20)
			}
		})
	}
}

// A push refused for want of room leaves the array as it was, and the
// array never grows past the limit.
func TestAPushRefusedChangesNothing(t *testing.T) {
	chunk := compiled(t, `
import std.result;

fn main() -> Int {
    let xs: [Int] = [];
    let failures = 0;
    let changed = false;
    for i in 0..100000 {
        let before = xs.len();
        let r = result.attempt(fn() -> Bool { xs.push(i); return true; });
        if r.isErr() {
            failures++;
            if xs.len() != before {
                changed = true;
            }
        }
    }
    println("${xs.len()} ${failures > 0} ${changed}");
    return 0;
}`)
	out, err := sandbox.Run(chunk, sandbox.Limits{HeapBytes: 256 << 10, Steps: 100_000_000, Time: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	var failed, changed string
	if _, err := fmt.Sscan(out, &n, &failed, &changed); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if failed != "true" || changed != "false" || n*8 > 256<<10 {
		t.Errorf("after pushes past 256 KB: %q", out)
	}
}

// Values nested deeper than the Go stack is kept small here are compared
// without the comparison going down the Go stack as deep.
func TestDeepValuesAreCompared(t *testing.T) {
	chunk := compiled(t, `
struct N { child: N?; tag: Int; }

fn nest(n: Int) -> N? {
    let a: N? = null;
    for _ in 0..n {
        a = N { child: a, tag: 1 };
    }
    return a;
}

fn main() -> Int {
    let x = nest(300000);
    let y = nest(300000);
    println(x == y);
    return 0;
}`)
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	var out bytes.Buffer
	m := vm.NewVMWithHeapSize(512 << 20)
	m.SetOutput(&out)
	if _, err := m.Exec(chunk); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "true" {
		t.Errorf("printed %q", out.String())
	}
}

// Data nested deeper than it can be read back is refused as data.
func TestDataTooDeepIsRefused(t *testing.T) {
	chunk := compiled(t, `
struct N { child: N?; tag: Int; }

fn main() -> N? {
    let a: N? = null;
    for _ in 0..20000 {
        a = N { child: a, tag: 1 };
    }
    return a;
}`)
	m := vm.NewVMWithHeapSize(256 << 20)
	v, err := m.Exec(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DataOf(v); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("data 20000 deep: %v", err)
	}
}
