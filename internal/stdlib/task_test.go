package stdlib_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// program compiles and runs a whole program, on a heap of the given size
// (0 for the usual one). It gives what the program printed, how it ended
// and the machine it ran on.
func program(t *testing.T, src string, heap uint64) (string, error, *vm.VM) {
	t.Helper()
	errs := errors.NewHandler()
	result := compiler.Compile(src, "tasks.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	if heap > 0 {
		machine = vm.NewVMWithHeapSize(heap)
	}
	machine.SetOutput(&out)
	_, err := machine.Exec(result.Chunk)
	return out.String(), err, machine
}

// expectProgram runs a whole program and compares what it prints.
func expectProgram(t *testing.T, src string, want ...string) {
	t.Helper()
	out, err, _ := program(t, src, 0)
	if err != nil {
		t.Fatalf("runtime error: %v\nafter printing:\n%s", err, out)
	}
	got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	for i := 0; i < len(got) || i < len(want); i++ {
		g, w := "<nothing>", "<nothing>"
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("line %d:\n  want %s\n   got %s", i+1, w, g)
		}
	}
}

const taskImports = `
	import std.task;
	import std.task.{Task, Channel};
	import std.result;
	import std.result.{Result, Failure, Kind};
	import std.arrays;
	import std.strings;
	import std.time;`

func TestTasksTakeTurns(t *testing.T) {
	// A task starts when the one that made it waits, runs until it waits
	// itself, and the turn goes round in the order the tasks came to it
	expect(t, taskImports, `
		let log: [String] = [];
		let a = task.start(fn() -> Int {
			log.push("a starts as ${task.current()}");
			task.pause();
			log.push("a goes on");
			return 1;
		});
		let b = task.start(fn() -> String {
			log.push("b starts as ${task.current()}");
			return "two";
		});
		log.push("main is ${task.current()}");
		println(a.isDone(), b.isDone());
		println(wait a, wait b);
		println(a.isDone(), b.isDone());
		for line in log { println(line); }

		// Waiting again gives the same, and pausing alone changes nothing
		task.pause();
		println((wait a).or(0) + (wait a).or(0), (wait a).expect("a value"));
	`,
		"false false",
		`Ok(1) Ok("two")`,
		"true true",
		"main is 0",
		"a starts as 1",
		"b starts as 2",
		"a goes on",
		"2 1",
	)
}

func TestTaskValues(t *testing.T) {
	expectProgram(t, taskImports+`
		struct Point { x: Int; y: Int; }
		enum Shape { Circle(Float), Square(Float) }

		fn area(shape: Shape) -> Float {
			match shape {
				case Shape.Circle(r): { return 3.0 * r * r; }
				case Shape.Square(side): { return side * side; }
			}
		}

		fn main() -> Int {
			// What a task returns is whatever its function does
			let point = task.start(fn() -> Point { return Point { x: 3, y: 4 }; });
			let nothing = task.start(fn() -> Int? { return null; });
			let pair = task.start(fn() -> (String, [Int]) { return ("squares", [1, 2, 3].map(n -> n * n)); });
			let areas = wait task.map([Shape.Circle(1.0), Shape.Square(2.0)], area);
			println((wait point).map(p -> p.x * p.y), wait nothing, wait pair, areas);

			// Tasks share what they close over
			let total = 0;
			let adders: [Task<Bool>] = [];
			for step in [1, 10, 100] {
				adders.push(task.run(fn() { total += step; task.pause(); total += step; }));
			}
			println(wait task.all(adders), total);

			// A task for what it does
			let seen: [Int] = [];
			let job = task.run(fn() { seen.push(task.current()); });
			println(wait job, seen.len(), wait task.all([job, job]));

			// Tasks that start tasks
			let nested = task.start(fn() -> Int {
				let inner = wait task.map([1, 2, 3], n -> (wait task.map([n, n], m -> m * 10)).expect("inner"));
				return arrays.flatten(inner.expect("outer")).sum();
			});
			println(wait nested);
			return 0;
		}`,
		`Ok(12) Ok(null) Ok(("squares", [1, 4, 9])) Ok([3, 4])`,
		"Ok([true, true, true]) 222",
		"Ok(true) 1 Ok([true, true])",
		"Ok(120)",
	)
}

func TestTaskFailures(t *testing.T) {
	expectProgram(t, taskImports+`
		fn third(items: [Int]) -> Int { return items[2]; }

		fn main() -> Int {
			// A task that stops does not stop the others
			let bad = task.start(fn() -> Int { return third([1, 2]); });
			let good = task.start(fn() -> Int { return third([1, 2, 3]); });
			let angry = task.start(fn() -> Int { if task.current() > 0 { panic("no", "thanks"); } return 0; });
			println(wait good);
			match wait bad {
				case Result.Ok(n): { println("unexpected", n); }
				case Result.Err(failure): {
					println(failure.kind == Kind.Bounds, failure.message, failure.at.function, failure.at.line, failure.stack.len());
				}
			}
			// Asked again, and by several: the same failure
			let again = task.start(fn() -> String { return (wait bad).reason()?.message ?? "none"; });
			println(wait again, (wait bad).isErr(), bad.isDone());
			println((wait angry).reason()?.kind == Kind.Panic, (wait angry).reason()?.message);

			// The first failure of several, after all have ended
			let done: [Int] = [];
			let mixed = wait task.map([1, 0, 2, 0], fn(n: Int) -> Int { done.push(n); return 10 / n; });
			println(mixed.reason()?.message, mixed.reason()?.kind == Kind.Arithmetic, done);

			// A failure caught inside the task is the task's own business,
			// even when the task waited in the middle of the attempt
			let gate = task.channel<Int>(0);
			let careful = task.start(fn() -> String {
				let tried = result.attempt(fn() -> Int { return 100 / (wait gate.receive() ?? 1); });
				return tried.reason()?.message ?? "fine";
			});
			wait gate.send(0);
			println(wait careful);

			// A failing task keeps the closures it made usable
			let counters: [fn() -> Int] = [];
			let maker = task.run(fn() {
				let count = 40;
				counters.push(fn() -> Int { count++; return count; });
				third([]);
			});
			println((wait maker).isErr(), counters[0](), counters[0]());
			return 0;
		}`,
		"Ok(3)",
		"true index out of bounds: 2 (length 2) third 9 2",
		`Ok("index out of bounds: 2 (length 2)") true true`,
		"true no thanks",
		"division by zero true [1, 0, 2, 0]",
		`Ok("division by zero")`,
		"true 41 42",
	)
}

func TestChannels(t *testing.T) {
	expectProgram(t, taskImports+`
		fn main() -> Int {
			// Without room: each send waits for its receive
			let log: [String] = [];
			let hand = task.channel<Int>(0);
			let sender = task.run(fn() {
				for n in [1, 2, 3] { log.push("send ${n}"); wait hand.send(n); log.push("sent ${n}"); }
				hand.close();
			});
			hand.each(fn(n: Int) { log.push("got ${n}"); });
			println(log.join(", "));
			println(wait sender, wait hand.receive(), hand.isClosed(), hand.len());

			// With room for two: the sender runs ahead by two, then waits
			let trace: [String] = [];
			let queue = task.channel<String>(2);
			task.run(fn() {
				for word in ["a", "b", "c", "d"] { wait queue.send(word); trace.push("+${word}"); }
				queue.close();
			});
			task.pause();
			trace.push("len ${queue.len()}");
			queue.each(fn(word: String) { trace.push("-${word}"); });
			println(trace.join(" "));

			// What was sent before closing is still received
			let last = task.channel<Int>(3);
			wait last.send(7);
			wait last.send(8);
			last.close();
			last.close();
			println(wait last.receive(), last.drain(), wait last.receive());

			// A value may be null: each and drain tell it from the end
			let maybe = task.channel<Int?>(4);
			wait maybe.send(1);
			wait maybe.send(null);
			wait maybe.send(3);
			maybe.close();
			println(maybe.drain());

			// Sending once closed is a mistake, for whoever does it
			let shut = task.channel<Int>(1);
			shut.close();
			let late = task.run(fn() { wait shut.send(1); });
			println((wait late).reason()?.message);
			let waiting = task.channel<Int>(0);
			let stuck = task.run(fn() { wait waiting.send(1); });
			task.pause();
			waiting.close();
			println(wait stuck, wait waiting.receive(), wait waiting.receive());

			// Several senders, several receivers: nothing lost, nothing twice
			let jobs = task.channel<Int>(4);
			let results = task.channel<Int>(0);
			let workers: [Task<Bool>] = [];
			for worker in [1, 2, 3] {
				workers.push(task.run(fn() {
					jobs.each(fn(n: Int) { task.pause(); wait results.send(n * n); });
				}));
			}
			let feeders = [
				task.run(fn() { for n in arrays.range(0, 50) { wait jobs.send(n); } }),
				task.run(fn() { for n in arrays.range(50, 100) { wait jobs.send(n); } }),
			];
			task.run(fn() {
				wait task.all(feeders);
				jobs.close();
				wait task.all(workers);
				results.close();
			});
			let squares = results.drain();
			println(squares.len(), squares.sum(), arrays.range(0, 100).map(n -> n * n).sum());

			// A long queue, emptied as it fills
			let stream = task.channel<Int>(1000);
			task.run(fn() { for n in arrays.range(0, 5000) { wait stream.send(n); } stream.close(); });
			let count = 0;
			let inOrder = true;
			stream.each(fn(n: Int) { if n != count { inOrder = false; } count++; });
			println(count, inOrder, stream.len());
			return 0;
		}`,
		"send 1, got 1, sent 1, send 2, got 2, sent 2, send 3, got 3, sent 3",
		"Ok(true) null true 0",
		"+a +b len 2 -a -b +c +d -c -d",
		"7 [8] null",
		"[1, null, 3]",
		"send on a closed channel",
		"Ok(true) 1 null",
		"100 328350 328350",
		"5000 true 0",
	)
	expectFailure(t, taskImports, `task.channel<Int>(-1);`, "a channel cannot hold -1 values")
}

func TestTasksWaitForTheClock(t *testing.T) {
	// Tasks that sleep do so at the same time, and wake in the order of
	// their clocks whatever the order they were started in
	started := time.Now()
	expect(t, taskImports, `
		let order: [Int] = [];
		let naps: [Task<Bool>] = [];
		for ms in [120, 40, 80] {
			naps.push(task.run(fn() { wait time.after(ms.milliseconds()); order.push(ms); }));
		}
		println((wait task.all(naps)).isOk(), order);

		// main sleeps too, while a task works
		let ticks = 0;
		let ticker = task.run(fn() { for i in arrays.range(0, 5) { wait time.after(5.milliseconds()); ticks++; } });
		wait time.after(100.milliseconds());
		println(ticks, ticker.isDone());
	`,
		"true [40, 80, 120]",
		"5 true",
	)
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("expected the sleeps to overlap, the program took %v", took)
	}
}

func TestProgramEndsWithItsTasks(t *testing.T) {
	// main has returned: the tasks still going reach their end
	expect(t, taskImports, `
		let lines = task.channel<String>(0);
		task.run(fn() { lines.each(fn(line: String) { println("printed", line); }); println("printer done"); });
		task.run(fn() { wait time.after(10.milliseconds()); wait lines.send("late"); lines.close(); });
		wait lines.send("early");
		println("main done");
	`,
		"printed early",
		"main done",
		"printed late",
		"printer done",
	)

	// A failure nobody waited for is the program's
	out, err := run(t, taskImports, `
		task.run(fn() { println("working"); let items = [1]; println(items[9]); });
		task.run(fn() { wait time.after(10.milliseconds()); println("the others finish"); });
		println("main done");
	`, "")
	if out != "main done\nworking\nthe others finish\n" {
		t.Errorf("unexpected output: %q", out)
	}
	if failure, ok := err.(*vm.RuntimeError); !ok || failure.Kind != vm.KindBounds || !strings.Contains(failure.Message, "index out of bounds: 9") {
		t.Errorf("expected the task's failure to end the program, got %v", err)
	}

	// ...but not one that was waited for
	expect(t, taskImports, `
		let failing = task.run(fn() { panic("handled"); });
		println((wait failing).reason()?.message);
	`, "handled")

	// main failing ends everything at once
	out, err = run(t, taskImports, `
		task.run(fn() { wait time.after(5000.milliseconds()); println("never"); });
		task.run(fn() { println("once"); wait task.channel<Int>(0).receive(); println("never"); });
		task.pause();
		if task.current() == 0 { panic("main gives up"); }
	`, "")
	if out != "once\n" || err == nil || !strings.Contains(err.Error(), "main gives up") {
		t.Errorf("expected main's failure to end the program, got %q and %v", out, err)
	}

	// A task can end the program, whatever the others are doing
	started := time.Now()
	out, err = run(t, taskImports+"\nimport std.os;", `
		task.run(fn() { wait time.after(5000.milliseconds()); println("never"); });
		task.run(fn() { wait time.after(20.milliseconds()); println("leaving"); os.exit(7); });
		let never = task.channel<Int>(0);
		result.attempt(fn() -> Int? { return wait never.receive(); });
		println("never");
	`, "")
	if code, leaving := vm.ExitCode(err); out != "leaving\n" || !leaving || code != 7 {
		t.Errorf("expected the program to exit with 7, got %q and %v", out, err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("expected the exit not to wait for the sleeping task, it took %v", took)
	}
}

func TestDeadlocks(t *testing.T) {
	deadlock := func(body string, message string, line int) {
		t.Helper()
		out, err := run(t, taskImports, body, "")
		failure, ok := err.(*vm.RuntimeError)
		if !ok || failure.Kind != vm.KindDeadlock || failure.Code() != "R0009" {
			t.Fatalf("expected a deadlock, got %v after printing %q", err, out)
		}
		if failure.Message != message {
			t.Errorf("expected the message %q, got %q", message, failure.Message)
		}
		// The place is in the program, not in the library. The body of
		// main comes after the imports and the line that opens main
		want := strings.Count(taskImports, "\n") + 2 + line
		if at := failure.Stack[0]; at.File != "test.qi" || at.Line != want {
			t.Errorf("expected line %d of the program, got %s:%d", want, at.File, at.Line)
		}
		if failure.Suggestion() == "" {
			t.Error("expected a suggestion")
		}
	}

	// Alone, waiting for a value nobody can send
	deadlock(`
		let empty = task.channel<Int>(1);
		wait empty.receive();`,
		"deadlock: the task waits for something no other task is left to do", 3)

	// Two tasks waiting for each other; nothing attempt can catch. The
	// place is where the last of them went to wait
	deadlock(`
		let there = task.channel<Int>(0);
		let back = task.channel<Int>(0);
		let other = task.start(fn() -> Int { return wait back.receive() ?? 0; });
		println(result.attempt(fn() -> Int? { return wait there.receive(); }));`,
		"deadlock: 2 tasks wait for each other, and none can go on", 4)

	// Found when the last task that could have helped ends
	deadlock(`
		let never = task.channel<Int>(0);
		let quitter = task.run(fn() { task.pause(); });
		wait never.send(1);`,
		"deadlock: the task waits for something no other task is left to do", 4)

	// After main has returned: a task left waiting
	deadlock(`
		let forgotten = task.channel<Int>(0);
		task.run(fn() {
			forgotten.each(fn(n: Int) { println(n); });
		});
		wait forgotten.send(1);`,
		"deadlock: the task waits for something no other task is left to do", 4)

	// A task that sleeps is not waiting for another: no deadlock yet
	expect(t, taskImports, `
		let slow = task.channel<Int>(0);
		task.run(fn() { wait time.after(30.milliseconds()); wait slow.send(5); });
		println(wait slow.receive());
	`, "5")
}

func TestTasksUnderMemoryPressure(t *testing.T) {
	// Tasks that wait hold values nothing else does: in their variables,
	// in channels, in what they are about to return. The collector runs
	// many times while they do.
	out, err, machine := program(t, taskImports+`
		struct Batch { name: String; rows: [String]; }

		fn build(worker: Int, round: Int) -> Batch {
			let rows: [String] = [];
			for i in arrays.range(0, 200) { rows.push("worker ${worker} round ${round} row ${i}"); }
			return Batch { name: "batch ${worker}.${round}", rows: rows };
		}

		fn main() -> Int {
			let batches = task.channel<Batch>(2);
			let workers: [Task<Int>] = [];
			for worker in arrays.range(0, 6) {
				workers.push(task.start(fn() -> Int {
					let made = 0;
					for round in arrays.range(0, 40) {
						let mine = build(worker, round);
						task.pause();
						if mine.rows[199] != "worker ${worker} round ${round} row 199" { panic("${mine.name} was spoiled"); }
						wait batches.send(mine);
						made++;
					}
					return made;
				}));
			}
			task.run(fn() { wait task.all(workers); batches.close(); });

			let rows = 0;
			let names = 0;
			batches.each(fn(batch: Batch) {
				if !batch.rows[0].startsWith("worker ") || !batch.name.startsWith("batch ") { panic("a batch was spoiled"); }
				rows += batch.rows.len();
				names++;
			});
			println(rows, names, (wait task.all(workers)).map(made -> made.sum()));
			return 0;
		}`, 4<<20)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if out != "48000 240 Ok(240)\n" {
		t.Errorf("unexpected output: %q", out)
	}
	if machine.Collections() < 5 {
		t.Errorf("expected the heap to be collected many times, got %d", machine.Collections())
	}
}

func TestManyTasks(t *testing.T) {
	expectProgram(t, taskImports+`
		// A task that passes on what it gets, plus one
		fn passOn(from: Channel<Int>, to: Channel<Int>) {
			task.run(fn() { wait to.send((wait from.receive() ?? 0) + 1); });
		}

		fn main() -> Int {
			let ring: [Channel<Int>] = [];
			for i in arrays.range(0, 300) { ring.push(task.channel<Int>(0)); }
			for i in arrays.range(0, 299) { passOn(ring[i], ring[i + 1]); }
			wait ring[0].send(0);
			println(wait ring[299].receive());

			println((wait task.map(arrays.range(0, 2000), n -> n % 7)).map(all -> all.sum()));
			return 0;
		}`,
		"299",
		"Ok(5995)",
	)
}

func TestServerAnswersSeveralRequestsAtOnce(t *testing.T) {
	const requests = 8
	var answers [requests]string
	started := time.Now()
	printed := serve(t, `
		import std.http;
		import std.http.{Request, Response};
		import std.task;
		import std.time;
		import std.strings;
	`, `
		let inProgress = 0;
		let most = 0;
		let app = http.router();
		app.get("/slow/:n", fn(request: Request) -> Response {
			inProgress++;
			if inProgress > most { most = inProgress; }
			wait time.after(150.milliseconds());
			inProgress--;
			return http.text("slow ${request.params.get("n") ?? "?"}");
		})
			.get("/boom", fn(request: Request) -> Response { let items = [1]; return http.text(toString(items[5])); })
			.get("/stop", fn(request: Request) -> Response { return http.text("stopping"); });

		let server = http.listen(0).expect("a free port");
		println(server.port);
		let serving = task.run(fn() { server.handleAll(app.handler()); });

		// Meanwhile the program does something else
		let beats = 0;
		while most < 8 || inProgress > 0 { wait time.after(10.milliseconds()); beats++; }
		server.close();
		println(wait serving, most, beats > 5);
	`, func(base string) {
		var wait sync.WaitGroup
		for i := 0; i < requests; i++ {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				response, err := http.Get(fmt.Sprintf("%s/slow/%d", base, i))
				if err != nil {
					answers[i] = err.Error()
					return
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(response.Body)
				answers[i] = fmt.Sprintf("%d %s", response.StatusCode, body)
			}(i)
		}
		// A handler that fails, in the middle of the others
		if response, err := http.Get(base + "/boom"); err != nil || response.StatusCode != 500 {
			t.Errorf("expected 500 from the handler that fails, got %v %v", response, err)
		}
		wait.Wait()
	})

	for i, answer := range answers {
		if want := fmt.Sprintf("200 slow %d", i); answer != want {
			t.Errorf("request %d: want %q, got %q", i, want, answer)
		}
	}
	if !strings.HasSuffix(printed, "Ok(true) 8 true\n") {
		t.Errorf("expected the requests to be in progress together, got %q", printed)
	}
	// Eight requests of 150 ms each, one after the other, take 1.2 s
	if took := time.Since(started); took > 900*time.Millisecond {
		t.Errorf("expected the requests to overlap, they took %v", took)
	}
}

// Closing a server lets the requests in progress be answered; one still
// waiting to be taken is answered 503, with the text as it is, and the
// port is free again once close returns
func TestServerClosesGracefully(t *testing.T) {
	expectProgram(t, `
		import std.http;
		import std.http.{Request, Response, Method, ErrorKind};
		import std.task;
		import std.time;

		fn answerOf(asked: String) -> String {
			return match http.get(asked) {
				case Ok(response): "${response.status} [${response.body}]";
				case Err(error): "no answer: ${error.kind}";
			};
		}

		fn main() -> Int {
			// handleAll in a task, closed while a slow answer is on its way
			let server = http.listen(0).expect("a free port");
			let base = "http://127.0.0.1:${server.port}";
			let serving = task.run(fn() {
				server.handleAll(fn(request: Request) -> Response {
					wait time.after(300.milliseconds());
					return http.text("slow answer");
				});
			});
			let slow = task.start(fn() -> String { return answerOf(base + "/slow"); });
			wait time.after(100.milliseconds());
			server.close();
			println((wait slow).expect("an answer"), wait serving, answerOf(base + "/late"));

			// One request answered by hand, another still waiting to be taken
			let other = http.listen(0).expect("a free port");
			let at = "http://127.0.0.1:${other.port}";
			let first = task.start(fn() -> String { return answerOf(at + "/first"); });
			let second = task.start(fn() -> String { wait time.after(50.milliseconds()); return answerOf(at + "/second"); });
			other.handle(1, fn(request: Request) -> Response {
				wait time.after(200.milliseconds());
				return http.text("first answered");
			});
			other.close();
			println((wait first).expect("an answer"), "|", (wait second).expect("an answer"));

			// The port can be listened on again
			let again = http.listen(other.port);
			println(again.isOk(), other.nextWithin(10) == null);
			again.value()?.close();

			// A port that is taken says so
			let held = http.listen(0).expect("a free port");
			match http.listen(held.port) {
				case Ok(_): { println("listened twice"); }
				case Err(error): { println(error.kind, error.describe().replace(toString(held.port), "N"), "${error}" == error.describe()); }
			}
			held.close();
			return 0;
		}`,
		"200 [slow answer] Ok(true) no answer: Refused",
		"200 [first answered] | 503 [the server is closing]",
		"true true",
		"InUse cannot listen on port N: the port is taken true",
	)
}

// A HEAD request is answered by the GET route of its path, without a
// body; a route of its own for HEAD comes first
func TestHeadAnsweredByGetRoute(t *testing.T) {
	expectProgram(t, `
		import std.http;
		import std.http.{Request, Response, Method};
		import std.task;

		fn main() -> Int {
			let app = http.router();
			app.get("/notes/:id", fn(request: Request) -> Response {
				return http.respond(203, "note ${request.params.get("id") ?? "?"}").with("X-Note", request.params.get("id") ?? "?");
			})
				.on(Method.Head, "/own", fn(request: Request) -> Response { return http.respond(204, ""); })
				.get("/own", fn(request: Request) -> Response { return http.text("the GET"); })
				.post("/only-post", fn(request: Request) -> Response { return http.text("posted"); });

			let head = app.answer(http.request(Method.Head, "/notes/7"));
			println(head.status, head.header("x-note"), head.header("content-type"), "[${head.body}]");
			let get = app.answer(http.request(Method.Get, "/notes/7?x=1&x=2"));
			println(get.status, get.body, app.answer(http.request(Method.Head, "/own")).status);
			println(app.answer(http.request(Method.Head, "/only-post")).status, app.answer(http.request(Method.Head, "/nowhere")).status);

			// http.request: a path, its query, a body
			let asked = http.request(Method.Post, "/a/b?tag=x%20y&n=1&n=2", "{}");
			println(asked.path, asked.query.get("tag"), asked.query.get("n"), asked.body, asked.headers.len(), asked.sender, http.request(Method.Get, "/").body == "");

			// Through a server: the GET route's status and headers, nothing else
			let server = http.listen(0).expect("a free port");
			let serving = task.run(fn() { server.handleAll(app.handler()); });
			let answered = http.to(Method.Head, "http://127.0.0.1:${server.port}/notes/9").send().expect("an answer");
			println(answered.status, answered.header("x-note"), "[${answered.body}]");
			server.close();
			wait serving;
			return 0;
		}`,
		"203 7 text/plain; charset=utf-8 []",
		"203 note 7 204",
		"405 404",
		"/a/b x y 2 {} 0 test true",
		"203 9 []",
	)
}

func TestClientAsksSeveralServersAtOnce(t *testing.T) {
	origin := http.NewServeMux()
	origin.HandleFunc("/wait/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintf(w, "page %s", strings.TrimPrefix(r.URL.Path, "/wait/"))
	})
	base := newLocalServer(t, origin)

	started := time.Now()
	expect(t, taskImports+"\nimport std.http;", `
		let base = `+quote(base)+`;
		let pages = wait task.map(arrays.range(0, 6), n -> http.get("${base}/wait/${n}").map(response -> response.body).or("no answer"));
		println(pages);

		// One that gets no answer does not hold the others up
		let dead = task.start(fn() -> Bool { return http.get("http://127.0.0.1:1/").isErr(); });
		let alive = task.start(fn() -> Int { return http.get(base + "/wait/x").map(response -> response.status).or(0); });
		println(wait dead, wait alive);
	`,
		`Ok(["page 0", "page 1", "page 2", "page 3", "page 4", "page 5"])`,
		"Ok(true) Ok(200)",
	)
	if took := time.Since(started); took > 800*time.Millisecond {
		t.Errorf("expected the requests to overlap, they took %v", took)
	}
}

// A task that waits deep inside its calls holds values in the registers of
// each of them, while the others run, return and collect many times: the
// registers a call leaves behind are cleared by the collector, those of a
// task that waits are not.
func TestTasksWaitingDeepInsideTheirCalls(t *testing.T) {
	out, err, machine := program(t, taskImports+`
		struct Leaf { depth: Int; label: String; }

		// Each level holds a text and a struct of its own across the pause
		// at the bottom, and checks them on the way back up
		fn descend(worker: Int, depth: Int) -> Int {
			let label = "w${worker} d${depth}";
			let leaf = Leaf { depth: depth, label: label + "!" };
			let below = 0;

			if depth == 0 {
				task.pause();
			} else {
				below = descend(worker, depth - 1);
			}

			if leaf.label != label + "!" || leaf.depth != depth {
				panic("level ${depth} of worker ${worker} was spoiled");
			}

			return below + depth;
		}

		// What churns: calls that leave texts and arrays in their registers
		fn churn(rounds: Int) -> Int {
			let total = 0;

			for round in 0..rounds {
				let parts: [String] = [];

				for i in 0..20 {
					parts.push("part ${round}.${i}");
				}

				total += parts.join("").len() % 7;
			}

			return total;
		}

		fn main() -> Int {
			let workers: [Task<Int>] = [];

			for worker in 0..8 {
				workers.push(task.start(fn() -> Int {
					let sum = 0;

					for round in 0..10 {
						sum += descend(worker, 30 + worker);
						sum += churn(30);
					}

					return sum;
				}));
			}

			println((wait task.all(workers)).map(sums -> sums.len()));
			return 0;
		}`, 2<<20)
	if err != nil {
		t.Fatalf("runtime error: %v\nafter printing:\n%s", err, out)
	}
	if out != "Ok(8)\n" {
		t.Errorf("unexpected output: %q", out)
	}
	if machine.Collections() < 10 {
		t.Errorf("expected the heap to be collected many times, got %d", machine.Collections())
	}
}
