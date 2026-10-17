package stdlib_test

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A worker that waits on its jobs, on a signal to stop and on the clock,
// and a producer that feeds it: each case runs once per wait, several
// ready channels take turns, a closed channel gives null, and the clock
// wins only when nothing else came.
func TestWaitMatch(t *testing.T) {
	expect(t, taskImports, `
		let jobs = task.channel<Int>(8);
		let quit = task.channel<Bool>(1);
		let log: [String] = [];
		task.run(fn() {
			for i in 1..=3 { wait jobs.send(i); wait time.after(10.milliseconds()); }
			wait time.after(120.milliseconds());
			wait jobs.send(4);
			wait time.after(20.milliseconds());
			wait quit.send(true);
		});

		let running = true;
		while running {
			wait match {
				case let n = jobs.receive(): { log.push("job ${n}"); }
				case quit.receive(): { log.push("quit"); running = false; }
				case time.after(60.milliseconds()): { log.push("idle"); }
			}
		}
		// How many times it idled depends on the machine: at least once
		println(log.filter(entry -> entry != "idle"), log.contains("idle"), log.indexOf("idle")! > log.indexOf("job 3")!);

		// Two channels always ready take turns
		let a = task.channel<String>(10);
		let b = task.channel<String>(10);
		for i in 0..5 { wait a.send("a${i}"); wait b.send("b${i}"); }
		let order: [String] = [];
		for _ in 0..6 {
			wait match {
				case let s = a.receive(): { order.push(s ?? "?"); }
				case let s = b.receive(): { order.push(s ?? "?"); }
			}
		}
		println(order.filter(s -> s.startsWith("a")).len(), order.filter(s -> s.startsWith("b")).len());

		// A closed channel is ready, with null
		let closed = task.channel<Int>(0);
		closed.close();
		wait match {
			case let n = closed.receive(): { println("closed gives", n); }
			case time.after(1000.milliseconds()): { println("no"); }
		}

		// Only the clock: a sleep that other tasks may cut short is not it
		let watch = time.stopwatch();
		wait match {
			case time.after(30.milliseconds()): { println("only time"); }
		}
		println(watch.milliseconds() >= 25);

		// Waiting on a quiet channel many times does not pile up
		let quiet = task.channel<Int>(1);
		for _ in 0..200 {
			wait match {
				case quiet.receive(): {}
				case time.after(0.milliseconds()): {}
			}
		}
		task.run(fn() { wait quiet.send(7); });
		wait match {
			case let n = quiet.receive(): { println("at last", n); }
		}
		`,
		`["job 1", "job 2", "job 3", "job 4", "quit"] true true`,
		"3 3",
		"closed gives null",
		"only time",
		"true",
		"at last 7",
	)
}

// The cases of one channel, told apart by their patterns: each value goes
// to the case it fits, and the names of the pattern are there in the case.
func TestWaitMatchPatterns(t *testing.T) {
	expectProgram(t, taskImports+`
		enum Command { Rename(String), Resize(Int, Int), Stop }
		struct Panel { name: String; width: Int; height: Int; }

		fn main() -> Int {
			let commands = task.channel<Command>(8);
			let panel = Panel { name: "main", width: 80, height: 24 };
			let seen: [String] = [];

			task.run(fn() {
				wait commands.send(Command.Rename("side"));
				wait commands.send(Command.Resize(120, 40));
				wait time.after(5.milliseconds());
				wait commands.send(Command.Resize(1, 1));
				wait commands.send(Command.Stop);
				commands.close();
			});

			let open = true;
			while open {
				wait match {
					case commands.receive() is Rename(name): { seen.push("rename to ${name}"); panel.name = name; }
					case commands.receive() is Resize(w, h): { seen.push("resize ${w}x${h}"); panel.width = w; panel.height = h; }
					case commands.receive() is Stop: { seen.push("stop"); open = false; }
					case commands.receive() is null: { seen.push("closed"); open = false; }
				}
			}
			println(seen.join(", "));
			println(panel.name, panel.width, panel.height);

			// As a value: each case gives one, and returns are the function's
			let quiet = task.channel<Command>(1);
			println(describe(quiet), describe(commands));
			return 0;
		}

		fn describe(commands: Channel<Command>) -> String {
			let first = wait match {
				case commands.receive() is Rename(name): "renamed ${name}";
				case commands.receive() is Resize(w, h): "${w}x${h}";
				case commands.receive() is Stop: { return "stopped"; }
				case commands.receive() is null: "closed";
				case time.after(10.milliseconds()): "nothing yet";
			};
			return first;
		}`,
		"rename to side, resize 120x40, resize 1x1, stop",
		"side 1 1",
		"nothing yet closed",
	)
}

// Several things at once: a task, a channel's room, the clock, every task
// of a list. What each gives, and the first of them that comes.
func TestWaitables(t *testing.T) {
	expect(t, taskImports, `
		// A task: what it returned, or how it failed, as many times as asked
		let slow = task.start(fn() -> String { wait time.after(20.milliseconds()); return "slow"; });
		let fast = task.start(fn() -> String { return "fast"; });
		let failing = task.start(fn() -> Int { return [1][3]; });
		let first = wait match {
			case let r = slow: "slow first: ${r}";
			case let r = fast: "fast first: ${r}";
		};
		println(first, wait slow, wait fast);
		println((wait failing).isErr(), (wait failing).reason()?.kind == Kind.Bounds);

		// Every task of a list, and every outcome of a list
		let squares = wait task.map([1, 2, 3], n -> n * n);
		let mixed = [task.start(fn() -> Int { return 1; }), failing, task.start(fn() -> Int { return 3; })];
		println(squares, (wait task.all(mixed)).isErr());
		println((wait task.settle(mixed)).map(r -> r.isOk()));

		// Room on a full channel: the send waits for a receive
		let box = task.channel<String>(1);
		wait box.send("first");
		let sender = task.start(fn() -> String { wait box.send("second"); return "sent"; });
		let gotRoom = wait match {
			case sender: "no room needed?";
			case time.after(15.milliseconds()): "still full";
		};
		println(gotRoom, wait box.receive(), wait sender, wait box.receive());

		// A send in a wait match, against the clock
		let full = task.channel<Int>(1);
		wait full.send(1);
		let outcome = wait match {
			case full.send(2): "sent";
			case time.after(10.milliseconds()): "gave up";
		};
		println(outcome, full.len());

		// wait binds to what follows it, then ?? and the operators apply
		let numbers = task.channel<Int>(2);
		wait numbers.send(20);
		numbers.close();
		println((wait numbers.receive() ?? 0) + 1, wait numbers.receive() ?? -1);
		`,
		`fast first: Ok("fast") Ok("slow") Ok("fast")`,
		"true true",
		"Ok([1, 4, 9]) true",
		"[true, false, true]",
		`still full first Ok("sent") second`,
		"gave up 1",
		"21 -1",
	)
}

// A type of the program can be waited for, by implementing Waitable: here
// a latch that opens once, made of what std.task already waits on.
func TestWaitableOfTheProgram(t *testing.T) {
	expectProgram(t, taskImports+`
		import std.task.{Waitable};

		struct Latch {
			open: Bool;
			gate: Channel<Bool>;
			at: Int;
		}

		fn latch() -> Latch {
			return Latch { open: false, gate: task.channel<Bool>(1), at: -1 };
		}

		impl Latch {
			fn release() {
				self.open = true;
				self.gate.close();
			}
		}

		impl Waitable<String> for Latch {
			fn ready() -> Bool { return self.open || self.gate.isClosed(); }
			fn peek() -> String { return "open"; }
			fn take() -> String { return "open"; }
			fn watch() { self.gate.receive().watch(); }
		}

		fn main() -> Int {
			let door = latch();
			let log: [String] = [];
			let waiter = task.run(fn() { log.push("waiter: ${wait door}"); });
			task.run(fn() { wait time.after(5.milliseconds()); log.push("releasing"); door.release(); });
			wait waiter;
			println(log.join(", "));

			let other = latch();
			let what = wait match {
				case let s = other: s;
				case time.after(5.milliseconds()): "timed out";
			};
			other.release();
			println(what, wait other);
			return 0;
		}`,
		"releasing, waiter: open",
		"timed out open",
	)
}

// wait is a word only where it can be nothing else: a function, a
// variable or a method called wait are used as before.
func TestWaitStaysAName(t *testing.T) {
	expectProgram(t, taskImports+`
		struct Door { opened: Int; }
		impl Door { fn wait() -> String { self.opened++; return "waited ${self.opened}"; } }

		fn wait(n: Int) -> Int { return n * 2; }

		fn main() -> Int {
			let door = Door { opened: 0 };
			println(door.wait(), wait(21));
			let waits = [1, 2];
			let wait = waits.len();
			println(wait + 1, wait);
			return 0;
		}`,
		"waited 1 42",
		"3 2",
	)
}

// A wait match nothing can ever end is a deadlock, reported at the wait;
// one with no case at all is a mistake.
func TestWaitMistakes(t *testing.T) {
	_, err := run(t, taskImports, `
		let never = task.channel<String>(0);
		wait match {
			case never.receive(): {}
		}`, "")
	failure, ok := err.(*vm.RuntimeError)
	if !ok || failure.Kind != vm.KindDeadlock {
		t.Fatalf("expected a deadlock, got %v", err)
	}
}

// What the compiler says of waiting done wrong.
func TestWaitDiagnostics(t *testing.T) {
	cases := []struct {
		name, src, code, says string
	}{
		{"not waitable", `fn main() { let n = 3; println(wait n); }`, "E2044", "Int cannot be waited for"},
		{"send thrown away", `fn main() { let c = task.channel<Int>(1); c.send(1); }`, "E2043", "does nothing until it is waited for"},
		{"receive thrown away", `fn main() { let c = task.channel<Int>(1); c.receive(); }`, "E2043", "does nothing until it is waited for"},
		{"clock thrown away", `fn main() { time.after(10.milliseconds()); }`, "E2043", "does nothing until it is waited for"},
		{"null left out", `enum C { A, B }
			fn main() { let c = task.channel<C>(1); wait match { case c.receive() is A: {} case c.receive() is B: {} } }`,
			"E2028", "gives null once it is closed"},
		{"variant left out", `enum C { A, B }
			fn main() { let c = task.channel<C>(1); wait match { case c.receive() is A: {} case c.receive() is null: {} } }`,
			"E2028", "does not handle C.B"},
		{"values left out", `fn main() { let c = task.channel<Int>(1); wait match { case c.receive() is 1: {} case c.receive() is null: {} } }`,
			"E2028", "take some of its values and not the others"},
		{"no case", `fn main() { wait match { } }`, "E0", "would wait forever"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := errors.NewHandler()
			compiler.Compile("import std.task;\nimport std.time;\n"+c.src, "wait.qi", errs)
			found := false
			for _, d := range errs.Diagnostics {
				text := d.String()
				if strings.Contains(text, c.code) && strings.Contains(text, c.says) {
					found = true
				}
			}
			if !found {
				var all []string
				for _, d := range errs.Diagnostics {
					all = append(all, d.String())
				}
				t.Errorf("expected %s saying %q, got:\n%s", c.code, c.says, strings.Join(all, "\n"))
			}
		})
	}
}
