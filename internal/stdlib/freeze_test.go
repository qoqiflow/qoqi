package stdlib_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A program that waits can be frozen while it waits, and taken up in
// another virtual machine as if nothing had happened. Every program here
// is run once as it is, then once for each place it waits, frozen there
// and taken up in a new machine: what it prints has to be the same, to
// the letter. Then once more, frozen and taken up at every wait in turn,
// from machine to machine.

func compiled(t *testing.T, src string) *vm.Chunk {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.Compile(src, "frozen.qi", errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	return result.Chunk
}

// plainRun runs a program to its end, and counts the times it waited.
func plainRun(t *testing.T, chunk *vm.Chunk) (string, int) {
	t.Helper()
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	waits := 0
	machine.SetOnWait(func(*vm.VM, string) error {
		waits++
		return nil
	})
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatalf("runtime error: %v\nafter printing:\n%s", err, out.String())
	}
	return out.String(), waits
}

// frozenAt runs a program until it waits for the k-th time, and freezes
// it there. A program whose waits depend on the clock may end before:
// the state is then nil.
func frozenAt(t *testing.T, chunk *vm.Chunk, k int) (string, []byte) {
	t.Helper()
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	waits := 0
	machine.SetOnWait(func(m *vm.VM, _ string) error {
		waits++
		if waits < k {
			return nil
		}
		state, err := m.Freeze()
		if err != nil {
			return err
		}
		return &vm.FrozenError{State: state}
	})
	_, err := machine.Exec(chunk)
	var frozen *vm.FrozenError
	if err == nil && waits < k {
		return out.String(), nil
	}
	if !errors.As(err, &frozen) {
		t.Fatalf("expected the program to be frozen at its wait %d, got %v\nafter printing:\n%s", k, err, out.String())
	}
	return out.String(), frozen.State
}

// thawed takes a program up in a new machine. With freezeNext, it is
// frozen again at its next wait, and what that wrote is given back.
func thawed(t *testing.T, chunk *vm.Chunk, state []byte, freezeNext bool) (string, []byte) {
	t.Helper()
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if freezeNext {
		machine.SetOnWait(func(m *vm.VM, _ string) error {
			next, err := m.Freeze()
			if err != nil {
				return err
			}
			return &vm.FrozenError{State: next}
		})
	}
	_, err := machine.Thaw(chunk, state)
	var frozen *vm.FrozenError
	if errors.As(err, &frozen) {
		return out.String(), frozen.State
	}
	if err != nil {
		t.Fatalf("runtime error after thawing: %v\nafter printing:\n%s", err, out.String())
	}
	return out.String(), nil
}

// expectFrozenAnywhere runs a program as it is, frozen at each of its
// waits, and frozen at all of them in turn, and compares what it prints.
func expectFrozenAnywhere(t *testing.T, src string, want ...string) {
	t.Helper()
	chunk := compiled(t, src)
	plain, waits := plainRun(t, chunk)
	if got := strings.TrimSuffix(plain, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("the program as it is:\nwant:\n%s\ngot:\n%s", strings.Join(want, "\n"), got)
	}
	if waits == 0 {
		t.Fatalf("the program never waits: there is nothing to freeze")
	}
	for k := 1; k <= waits; k++ {
		before, state := frozenAt(t, chunk, k)
		if state == nil {
			// It waited less this time: the clock decides
			continue
		}
		after, _ := thawed(t, chunk, state, false)
		if before+after != plain {
			t.Errorf("frozen at wait %d of %d:\nwant:\n%s\ngot:\n%s---- (taken up here)\n%s", k, waits, plain, before, after)
		}
	}

	// From machine to machine, frozen at every wait
	out, state := frozenAt(t, chunk, 1)
	machines := 1
	for state != nil {
		var more string
		more, state = thawed(t, chunk, state, true)
		out += more
		machines++
		// Taken up before its time has come, a program waits again for
		// what is left: there may be more waits than in one run
		if machines > 100000 {
			t.Fatalf("taken up %d times for %d waits", machines, waits)
		}
	}
	if out != plain {
		t.Errorf("frozen at every wait, from machine to machine:\nwant:\n%s\ngot:\n%s", plain, out)
	}
}

// Numbers of every kind, texts, arrays, structs, enums, optionals and a
// dictionary large enough to have an index, alive across waits and
// changed between them.
func TestFreezeValues(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;
import std.dict;
import std.dict.{Dict};

struct Point { x: Int; y: Float; }
enum Shape { Circle(Float), Rect(Point, Point), Dot }

fn main() -> Int {
    let n = 41;
    let f = 2.5;
    let odd = [0.0 / 0.0, 1.0 / 0.0, -1.0 / 0.0];
    let text = "naïve " + "café";
    let maybe: Int? = null;
    let point = Point { x: 3, y: 4.5 };
    let shapes = [Shape.Circle(1.5), Shape.Rect(point, Point { x: -1, y: 0.0 }), Shape.Dot];
    let counts = dict.empty<Int>();

    for i in 0..20 {
        counts.set("key${i}", i * i);
    }

    wait time.after(10.milliseconds());
    n++;
    f = f * 2.0;
    maybe = n;
    point.x = 30;

    wait time.after(10.milliseconds());
    counts.set("key3", -9);
    shapes.push(Shape.Circle(f));

    wait time.after(10.milliseconds());
    println(n, f, odd, text, maybe, point.x, point.y);
    println(shapes.len(), counts.get("key3"), counts.get("key19"), counts.keys().len());

    for shape in shapes {
        match shape {
            case Circle(r): { println("circle", r); }
            case Rect(a, b): { println("rect", a.x, b.x); }
            case Dot: { println("dot"); }
        }
    }

    return 0;
}`,
		"42 5 [NaN, +Inf, -Inf] naïve café 42 30 4.5",
		"4 -9 361 20",
		"circle 1.5",
		"rect 30 -1",
		"dot",
		"circle 5",
	)
}

// Calls in progress: a wait deep inside calls, inside a loop, inside a
// recursion, and in a method.
func TestFreezeCalls(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

struct Counter { total: Int; }

impl Counter {
    fn add(n: Int) -> Int {
        wait time.after(10.milliseconds());
        self.total += n;
        return self.total;
    }
}

fn depth(n: Int, log: [String]) -> Int {
    if n == 0 {
        wait time.after(10.milliseconds());
        log.push("bottom");
        return 0;
    }
    log.push("down ${n}");
    let below = depth(n - 1, log);
    log.push("up ${n}");
    return below + n;
}

fn main() -> Int {
    let log: [String] = [];
    println(depth(3, log), log);

    let counter = Counter { total: 0 };
    for i in 1..=3 {
        println("added", counter.add(i * 10));
    }
    return 0;
}`,
		`6 ["down 3", "down 2", "down 1", "bottom", "up 1", "up 2", "up 3"]`,
		"added 10",
		"added 30",
		"added 60",
	)
}

// Functions that keep variables: made before a wait, called after it,
// sharing a variable with the function that made them, which goes on
// changing it.
func TestFreezeClosures(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

fn counter() -> fn() -> Int {
    let count = 100;
    return fn() -> Int {
        count++;
        return count;
    };
}

fn main() -> Int {
    let next = counter();
    let seen = 0;
    let watch = fn() -> Int { return seen; };
    println(next(), watch());

    wait time.after(10.milliseconds());
    seen = 7;
    println(next(), watch());

    wait time.after(10.milliseconds());
    let doubled = [1, 2, 3].map(n -> n * 2 + seen);
    println(next(), doubled);
    return 0;
}`,
		"101 0",
		"102 7",
		"103 [9, 11, 13]",
	)
}

// The variables of a module, made once, and changed across waits.
func TestFreezeModuleVariables(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

let visits = 0;
let names: [String] = [];
const LIMIT = 3;

fn visit(name: String) {
    visits++;
    names.push(name);
}

fn main() -> Int {
    visit("ada");
    wait time.after(10.milliseconds());
    visit("grace");
    wait time.after(10.milliseconds());
    println(visits, names, LIMIT);
    return 0;
}`,
		`2 ["ada", "grace"] 3`,
	)
}

// wait match, the clock, a channel the program writes to itself, and a
// time that has passed by the time the program is taken up.
func TestFreezeWaits(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.task;
import std.time;

fn main() -> Int {
    let box = task.channel<String>(4);
    wait box.send("first");
    let got = wait match {
        case let s = box.receive(): s ?? "?";
        case time.after(1000.milliseconds()): "too late";
    };
    println(got);

    let ticks = 0;
    let started = time.now();
    while time.now() - started < 5 {
        wait match {
            case box.receive(): { println("never"); }
            case time.after(1.milliseconds()): { ticks++; }
        }
    }
    println(ticks > 0);

    let soon = time.after(1.milliseconds());
    wait time.after(3.milliseconds());
    wait soon;
    println("done");
    return 0;
}`,
		"first",
		"true",
		"done",
	)
}

// Several tasks, frozen wherever any of them waits: a pipeline of
// channels, tasks that share a variable, a task not started yet, one
// that failed with its failure not taken, and task.map.
func TestFreezeTasks(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.task;

fn square(n: Int) -> Int {
    task.pause();
    return n * n;
}

fn main() -> Int {
    let numbers = task.channel<Int>(1);
    let squares = task.channel<Int>(1);
    let seen = 0;

    let producer = task.run(fn() {
        for i in 1..6 {
            wait numbers.send(i);
        }
        numbers.close();
    });
    let worker = task.run(fn() {
        while true {
            let n = wait numbers.receive();
            if n == null {
                break;
            }
            seen += 1;
            wait squares.send(n * n);
        }
        squares.close();
    });

    let total = 0;
    while true {
        let s = wait squares.receive();
        if s == null {
            break;
        }
        total += s;
    }
    wait producer;
    wait worker;
    println(total, seen);

    let failing = task.start(fn() -> Int {
        let empty: [Int] = [];
        return empty[3];
    });
    let late = task.start(fn() -> Int { return 7; });
    task.pause();
    match wait failing {
        case Ok(n): { println(n); }
        case Err(f): { println("failed:", f.message); }
    }
    println((wait late).expect("late"));

    let all = wait task.map([1, 2, 3, 4], square);
    println(all.expect("squares"));
    return 0;
}`,
		"55 5",
		"failed: index out of bounds: 3 (length 0)",
		"7",
		"[1, 4, 9, 16]",
	)
}

// What Freeze refuses: a task that waits for the world, and one that
// waits inside a function it gave to a native one.
func TestFreezeRefuses(t *testing.T) {
	for _, c := range []struct{ name, src, says string }{
		{"a task asleep outside", `
import std.task;
import std.time;
fn main() -> Int {
    let other = task.start(fn() -> Int { wait time.after(50.milliseconds()); return 1; });
    wait time.after(20.milliseconds());
    println(wait other);
    return 0;
}`, "waits for the world"},
		{"inside a function given to a native", `
import std.time;
import std.result;
fn main() -> Int {
    let tried = result.attempt(fn() -> Int { wait time.after(20.milliseconds()); return 1; });
    println(tried);
    return 0;
}`, "inside a function given to a native"},
	} {
		t.Run(c.name, func(t *testing.T) {
			chunk := compiled(t, c.src)
			machine := vm.NewVM()
			machine.SetOutput(&bytes.Buffer{})
			var refused error
			machine.SetOnWait(func(m *vm.VM, _ string) error {
				if refused == nil {
					_, refused = m.Freeze()
				}
				return nil
			})
			if _, err := machine.Exec(chunk); err != nil {
				t.Fatal(err)
			}
			if refused == nil || !strings.Contains(refused.Error(), c.says) {
				t.Errorf("expected Freeze to refuse saying %q, got %v", c.says, refused)
			}
		})
	}
}

// What Freeze writes is read back only on the bytecode it was frozen on.
func TestThawChecksTheBytecode(t *testing.T) {
	src := `
import std.time;
fn main() -> Int {
    wait time.after(10.milliseconds());
    println("taken up");
    return 0;
}`
	chunk := compiled(t, src)
	_, state := frozenAt(t, chunk, 1)

	var written map[string]any
	if err := json.Unmarshal(state, &written); err != nil || written["format"] != vm.FreezeFormat {
		t.Fatalf("expected JSON of format %s, got %s", vm.FreezeFormat, state)
	}

	other := compiled(t, strings.Replace(src, "taken up", "another", 1))
	if _, err := vm.NewVM().Thaw(other, state); err == nil || !strings.Contains(err.Error(), "other bytecode") {
		t.Errorf("expected thawing on other bytecode to be refused, got %v", err)
	}
	if _, err := vm.NewVM().Thaw(chunk, []byte(`{"format": "qoqi.vm/0"}`)); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("expected another format to be refused, got %v", err)
	}
}

// What is shared stays shared: two names for one object are still one
// object once taken up, an object that holds itself still does, and a
// text made by joining others is the text it was.
func TestFreezeSharing(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

struct Node { name: String; next: Node?; seen: [String]; }

fn main() -> Int {
    let log: [String] = [];
    let a = Node { name: "a", next: null, seen: log };
    let b = Node { name: "b", next: a, seen: log };
    a.next = b;
    let same = a;
    let joined = "";
    for i in 0..50 {
        joined = joined + "${i},";
    }

    wait time.after(10.milliseconds());
    same.name = "changed";
    log.push("one");
    b.seen.push("two");

    wait time.after(10.milliseconds());
    println(a.name, a.next?.next?.name, a.next?.name, log, a.seen.len(), joined.len());
    println(joined[0..20]);
    return 0;
}`,
		`changed changed b ["one", "two"] 2 140`,
		"0,1,2,3,4,5,6,7,8,9,",
	)
}

// A wait at the bottom of a thousand calls.
func TestFreezeDeep(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

fn down(n: Int) -> Int {
    if n == 0 {
        wait time.after(10.milliseconds());
        return 0;
    }
    return down(n - 1) + n;
}

fn main() -> Int {
    println(down(1000));
    return 0;
}`,
		"500500",
	)
}

// Taken up on a heap small enough that the collector runs while the
// program is being made again: nothing it has made yet may go.
func TestThawUnderPressure(t *testing.T) {
	src := `
import std.time;

struct Item { id: Int; label: String; tags: [String]; }

fn main() -> Int {
    let items: [Item] = [];
    for i in 0..3000 {
        items.push(Item { id: i, label: "item ${i}", tags: ["t${i % 7}", "u${i % 3}"] });
    }
    wait time.after(10.milliseconds());
    let total = 0;
    for item in items {
        total += item.id + item.label.len() + item.tags.len();
    }
    for _ in 0..200 {
        let garbage = items.map(item -> item.label + "!");
        total += garbage.len();
    }
    println(total);
    return 0;
}`
	chunk := compiled(t, src)
	plain, _ := plainRun(t, chunk)
	_, state := frozenAt(t, chunk, 1)

	var out bytes.Buffer
	machine := vm.NewVMWithHeapSize(4 << 20)
	machine.SetOutput(&out)
	if _, err := machine.Thaw(chunk, state); err != nil {
		t.Fatalf("thawing on a small heap: %v", err)
	}
	if out.String() != plain {
		t.Errorf("want %q, got %q", plain, out.String())
	}
}

// What a call deferred is kept while it waits, and run at the end of the
// call once it is taken up: on a return, or from a defer of a call
// further out.
func TestFreezeDefers(t *testing.T) {
	expectFrozenAnywhere(t, `
import std.time;

fn tidy(log: [String], early: Bool) -> Int {
    defer log.push("tidied");
    log.push("before");
    wait time.after(10.milliseconds());
    if early {
        return 0;
    }
    log.push("after");
    return 1;
}

fn outer(log: [String]) -> Int {
    defer log.push("outer tidied");
    let n = tidy(log, true);
    wait time.after(10.milliseconds());
    return n + 2;
}

fn main() -> Int {
    let log: [String] = [];
    println(tidy(log, false), log);
    let other: [String] = [];
    println(outer(other), other);
    return 0;
}`,
		`1 ["before", "after", "tidied"]`,
		`2 ["before", "tidied", "outer tidied"]`,
	)
}
