package stdlib_test

import (
	"strings"
	"testing"
)

// The functions of std.arrays that change the array they are given: pop,
// removeAt, insertAt and clear.

func TestArraysPop(t *testing.T) {
	expect(t, "import std.arrays;", `
		let xs = [1, 2, 3];
		println(xs.pop(), xs, xs.len());
		println(xs.pop(), xs.pop(), xs, xs.isEmpty());

		// An empty array gives null, as often as it is asked, and is still
		// an array to push onto
		println(xs.pop(), xs.pop(), xs);
		xs.push(7);
		println(xs);
		println(xs.pop(), xs.pop());

		// An array that never held anything
		let none: [String] = [];
		println(none.pop(), [].pop(), none.len());

		// What comes out has the type of the items: a number to add to
		let total = 0;
		let numbers = arrays.range(1, 101);
		while !numbers.isEmpty() { total += numbers.pop() ?? 0; }
		println(total, numbers);

		// push and pop in turns: the array is a stack
		let stack: [String] = [];
		for word in ["a", "b", "c", "d"] {
			stack.push(word);
			if word == "b" { println(stack.pop()); }
		}
		println(stack);
		println(stack.last(), stack.pop(), stack.last(), stack);
	`,
		"3 [1, 2] 2",
		"2 1 [] true",
		"null null []",
		"[7]",
		"7 null",
		"null null 0",
		"5050 []",
		"b",
		`["a", "c", "d"]`,
		`d d c ["a", "c"]`,
	)
}

func TestArraysRemoveAt(t *testing.T) {
	expect(t, "import std.arrays;", `
		let xs = ["a", "b", "c", "d", "e"];
		println(xs.removeAt(0), xs);
		println(xs.removeAt(1), xs);
		println(xs.removeAt(2), xs);

		// Where there is no item: null, and nothing changes
		println(xs.removeAt(2), xs.removeAt(-1), xs.removeAt(99), xs.removeAt(-99), xs);

		println(xs.removeAt(0), xs.removeAt(0), xs.removeAt(0), xs);
		let none: [Int] = [];
		println(none.removeAt(0), none);

		// The items after the one removed move down, in the same order
		let numbers = arrays.range(0, 10);
		numbers.removeAt(3);
		numbers.removeAt(3);
		println(numbers, numbers[3], numbers.len(), numbers.indexOf(9));

		// From the front, an array is a queue
		let queue = ["first", "second"];
		queue.push("third");
		println(queue.removeAt(0), queue.removeAt(0));
		queue.push("fourth");
		println(queue.removeAt(0), queue.removeAt(0), queue.removeAt(0));
	`,
		`a ["b", "c", "d", "e"]`,
		`c ["b", "d", "e"]`,
		`e ["b", "d"]`,
		`null null null null ["b", "d"]`,
		`b d null []`,
		`null []`,
		`[0, 1, 2, 5, 6, 7, 8, 9] 5 8 7`,
		`first second`,
		`third fourth null`,
	)
}

func TestArraysInsertAt(t *testing.T) {
	expect(t, "import std.arrays;", `
		let xs = [1, 2, 3];
		xs.insertAt(0, 0);
		println(xs);
		xs.insertAt(2, 15);
		println(xs);
		xs.insertAt(xs.len(), 4);
		println(xs);

		// Past the end is the end; a negative index changes nothing. Both
		// are what items -> items.inserted() does with them
		xs.insertAt(99, 5);
		xs.insertAt(-1, -1);
		xs.insertAt(-99, -1);
		println(xs, xs.len());
		println([1, 2].inserted(99, 3), [1, 2].inserted(-1, 3));

		let none: [String] = [];
		none.insertAt(-1, "lost");
		println(none);
		none.insertAt(0, "b");
		none.insertAt(0, "a");
		none.insertAt(5, "c");
		println(none);

		// For every index, the same as inserted, which makes a new array
		let same = true;
		for index in -2..7 {
			let changed = [10, 20, 30, 40];
			changed.insertAt(index, 99);
			let made = [10, 20, 30, 40].inserted(index, 99);
			if toString(changed) != toString(made) { same = false; }
		}
		println(same);

		// Many, always in front: the array grows and keeps its order
		let grown: [Int] = [];
		for n in 0..1000 { grown.insertAt(0, n); }
		println(grown.len(), grown[0], grown[500], grown[999]);

		// Kept in order as they come
		let sorted: [Int] = [];
		for n in [5, 1, 4, 1, 9, 2, 6] {
			let at = sorted.findIndex(held -> held > n) ?? sorted.len();
			sorted.insertAt(at, n);
		}
		println(sorted);
	`,
		"[0, 1, 2, 3]",
		"[0, 1, 15, 2, 3]",
		"[0, 1, 15, 2, 3, 4]",
		"[0, 1, 15, 2, 3, 4, 5] 7",
		"[1, 2, 3] [1, 2]",
		"[]",
		`["a", "b", "c"]`,
		"true",
		"1000 999 499 0",
		"[1, 1, 2, 4, 5, 6, 9]",
	)
}

func TestArraysClear(t *testing.T) {
	expect(t, "import std.arrays;", `
		let xs = [1, 2, 3];
		xs.clear();
		println(xs, xs.len(), xs.isEmpty(), xs.first(), xs.pop());
		xs.clear();
		xs.push(4);
		xs.insertAt(0, 3);
		println(xs);

		let none: [String] = [];
		none.clear();
		println(none);

		// A buffer filled and emptied again and again is one array
		let buffer: [Int] = [];
		let sums: [Int] = [];
		for round in 1..=5 {
			for n in 0..round * 10 { buffer.push(n); }
			sums.push(buffer.sum());
			buffer.clear();
		}
		println(sums, buffer);
	`,
		"[] 0 true null null",
		"[3, 4]",
		"[]",
		"[45, 190, 435, 780, 1225] []",
	)
}

// The array itself changes: everything that holds it sees it.
func TestArraysChangedInPlaceAreShared(t *testing.T) {
	expect(t, "import std.arrays;\nstruct Team { name: String; members: [String]; }\nfn dismiss(from: [String]) -> String? { return from.pop(); }", `
		let xs = [1, 2, 3, 4];
		let same = xs;
		xs.pop();
		same.removeAt(0);
		println(xs, same, xs == same);
		same.insertAt(1, 9);
		println(xs);
		xs.clear();
		println(same);

		// A copy is another array
		let original = ["a", "b"];
		let duplicate = original.copy();
		duplicate.pop();
		println(original, duplicate);

		// In a field, and through a function that is given it
		let team = Team { name: "core", members: ["ada", "linus", "grace"] };
		let members = team.members;
		println(dismiss(team.members), members, team.members.len());
		members.insertAt(0, "alan");
		println(team.members);

		// Captured by a closure
		let log: [String] = [];
		let note = fn(text: String) { log.push(text); };
		let forget = fn() -> String? { return log.pop(); };
		note("one");
		note("two");
		println(log);
		println(forget(), forget(), forget(), log);

		// An array of arrays: taking one out leaves it whole
		let rows = [[1, 2], [3, 4], [5, 6]];
		let middle = rows.removeAt(1);
		if middle != null {
			middle.push(0);
			println(middle, rows);
			rows.insertAt(0, middle);
			middle.clear();
			println(rows);
		}
	`,
		"[2, 3] [2, 3] true",
		"[2, 9, 3]",
		"[]",
		`["a", "b"] ["a"]`,
		`grace ["ada", "linus"] 2`,
		`["alan", "ada", "linus"]`,
		`["one", "two"]`,
		`two one null []`,
		"[3, 4, 0] [[1, 2], [5, 6]]",
		"[[], [1, 2], [5, 6]]",
	)
}

// The same four functions for whatever an array holds.
func TestArraysChangedInPlaceOfEveryType(t *testing.T) {
	expect(t, `import std.arrays;
import std.dict;
import std.dict.{Dict};
import std.result.{Result};
struct Point { x: Int; y: Int; }
enum Shape { Circle(Float), Rect(Float, Float), Empty }
struct Box<T> { value: T; }
trait Named { fn name() -> String; }
struct Cat { }
struct Dog { }
impl Named for Cat { fn name() -> String { return "cat"; } }
impl Named for Dog { fn name() -> String { return "dog"; } }

// Generic code over them: T is whatever the caller's array holds
fn rotate<T>(items: [T]) {
	let front = items.removeAt(0);
	if front != null { items.push(front); }
}
fn drain<T>(items: [T]) -> [T] {
	let out: [T] = [];
	while !items.isEmpty() {
		let item = items.pop();
		if item != null { out.push(item); }
	}
	return out;
}
struct Stack<T> { items: [T]; }
impl<T> Stack<T> {
	fn push(item: T) { self.items.push(item); }
	fn pop() -> T? { return self.items.pop(); }
	fn under(item: T) { self.items.insertAt(self.items.len() - 1, item); }
	fn reset() { self.items.clear(); }
}`, `
		let floats = [1.5, 2.5, 3.5];
		let half = floats.pop() ?? 0.0;
		println(half / 2.0, floats.removeAt(0), floats);

		let flags = [true, false];
		flags.insertAt(1, true);
		println(flags.pop(), flags);

		let texts = ["x", "y"];
		let text = texts.pop() ?? "";
		println(text + text, texts);

		let points = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
		let point = points.pop();
		if point != null { println(point.x + point.y, points.len()); }
		points.insertAt(0, Point { x: 5, y: 6 });
		println(points.map(p -> p.x));

		let shapes = [Shape.Circle(1.0), Shape.Rect(2.0, 3.0), Shape.Empty];
		match shapes.removeAt(1) {
			case Shape.Rect(w, h): { println("rect", w * h); }
			default: { println("something else"); }
		}
		println(shapes);
		println(shapes.pop() == Shape.Empty, shapes);

		let pairs = [(1, "one"), (2, "two")];
		let pair = pairs.pop();
		if pair != null {
			let (number, word) = pair;
			println(number, word, pairs);
		}

		let functions: [fn(Int) -> Int] = [];
		functions.insertAt(0, n -> n + 1);
		functions.insertAt(0, n -> n * 10);
		let last = functions.pop();
		let first = functions.removeAt(0);
		if last != null && first != null { println(last(1), first(1), functions.len()); }

		let tables = [dict.empty<Int>(), dict.empty<Int>()];
		tables[1].set("k", 5);
		println(tables.pop()?.get("k"), tables.len());

		let boxes = [Box<String> { value: "in" }];
		println(boxes.pop()?.value, boxes.pop()?.value);

		let results: [Result<Int, String>] = [Result.Ok(1), Result.Err("no")];
		println(results.removeAt(0), results.pop(), results);

		let animals: [Named] = [Cat {}, Dog {}];
		animals.insertAt(1, Cat {});
		println(animals.pop()?.name(), animals.map(a -> a.name()));

		let anything: [Any] = [1, "two", 3.0];
		println(anything.pop(), anything.removeAt(0), anything);

		let letters = ["a", "b", "c"];
		rotate(letters);
		rotate<String>(letters);
		println(letters);
		println(drain(letters), letters);
		println(drain([Point { x: 7, y: 0 }])[0].x, (drain<Int>([])).len());

		let stack = Stack<Int> { items: [] };
		stack.push(1);
		stack.push(3);
		stack.under(2);
		println(stack.items);
		println(stack.pop(), stack.pop(), stack.items);
		stack.reset();
		println(stack.pop(), stack.items);
		let words = Stack<[String]> { items: [["a"], ["b", "c"]] };
		println(words.pop(), words.pop(), words.pop());
	`,
		"1.75 1.5 [2.5]",
		"false [true, true]",
		`yy ["x"]`,
		"7 1",
		"[5, 1]",
		"rect 6",
		"[Circle(1), Empty]",
		"true [Circle(1)]",
		`2 two [(1, "one")]`,
		"2 10 0",
		"5 1",
		"in null",
		`Ok(1) Err("no") []`,
		`dog ["cat", "cat"]`,
		`3 1 ["two"]`,
		`["c", "a", "b"]`,
		`["b", "a", "c"] []`,
		"7 0",
		"[1, 2, 3]",
		"3 2 [1]",
		"null []",
		`["b", "c"] ["a"] null`,
	)
}

// In an array of optionals an item can be null, and pop then gives null
// for an item as it does for an empty array. The length tells them apart.
func TestArraysPopOfOptionals(t *testing.T) {
	expect(t, "import std.arrays;", `
		let readings: [Int?] = [3, null, 5, null];
		println(readings.len(), readings.pop(), readings.len());
		println(readings.pop(), readings.len());

		// Until it is empty, counting what is missing
		let missing = 0;
		let total = 0;
		while !readings.isEmpty() {
			let reading = readings.pop();
			if reading == null { missing++; } else { total += reading; }
		}
		println(missing, total, readings, readings.pop());

		let names: [String?] = [null, "ada", null];
		println(names.removeAt(0), names.len(), names.removeAt(5), names.len());
		names.insertAt(1, null);
		println(names);
		println(names.removeAt(0), names);
		names.clear();
		println(names);
	`,
		"4 null 3",
		"5 2",
		"1 3 [] null",
		"null 2 null 2",
		`["ada", null, null]`,
		`ada [null, null]`,
		"[]",
	)
}

// Taking items out of the array a loop is going through. With an index
// that is only moved on when nothing was removed, or going down from the
// end, every item is seen once.
func TestArraysRemovingWhileLooping(t *testing.T) {
	expect(t, "import std.arrays;\nstruct Task { name: String; done: Bool; }", `
		// Going up: stay where an item was removed, the next one is there now
		let numbers = arrays.range(1, 11);
		let i = 0;
		let removed: [Int] = [];
		while i < numbers.len() {
			if numbers[i] % 3 == 0 || numbers[i] % 2 == 0 {
				removed.push(numbers.removeAt(i) ?? 0);
			} else {
				i++;
			}
		}
		println(numbers, removed);

		// Going down: what moves has been looked at already
		let tasks = [
			Task { name: "write", done: true },
			Task { name: "test", done: true },
			Task { name: "review", done: false },
			Task { name: "ship", done: true },
		];
		let at = tasks.len() - 1;
		while at >= 0 {
			if tasks[at].done { tasks.removeAt(at); }
			at--;
		}
		println(tasks.map(task -> task.name));

		// Two neighbours that are the same, kept once
		let letters = ["a", "a", "b", "b", "b", "c", "a", "a"];
		let k = 1;
		while k < letters.len() {
			if letters[k] == letters[k - 1] { letters.removeAt(k); } else { k++; }
		}
		println(letters);

		// Inserting while going through: step over what was put in
		let spaced = [1, 2, 3];
		let j = 1;
		while j < spaced.len() {
			spaced.insertAt(j, 0);
			j += 2;
		}
		println(spaced);

		// A loop over a copy can change the array itself
		let scores = [4, 9, 2, 7];
		for score in scores.copy() {
			if score < 5 { scores.removeAt(scores.indexOf(score) ?? -1); }
		}
		println(scores);

		// Until nothing is left
		let pending = ["a", "b", "c"];
		let order = "";
		while true {
			let next = pending.pop();
			if next == null { break; }
			order += next;
		}
		println(order, pending);
	`,
		"[1, 5, 7] [2, 3, 4, 6, 8, 9, 10]",
		`["review"]`,
		`["a", "b", "c", "a"]`,
		"[1, 0, 2, 0, 3]",
		"[9, 7]",
		"cba []",
	)

	// `for ... in` counts the items once, before it starts: an array made
	// shorter under it is read past its end, and the program is stopped
	expectFailure(t, "import std.arrays;", `
		let xs = [1, 2, 3, 4];
		for x in xs { xs.pop(); }
	`, "index out of bounds: 2 (length 2)")
}

// A program of several parts on arrays changed in place: an interpreter
// with a stack of values and a stack of calls, a queue of work walked
// breadth first, and a history that undoes.
func TestArraysInPlaceProgram(t *testing.T) {
	expect(t, `import std.arrays;
import std.dict;
import std.dict.{Dict};
import std.result.{Result};

enum Instr {
	Push(Int),
	Add,
	Sub,
	Mul,
	Call(String),
	Ret,
	JumpIfZero(Int),
	Jump(Int),
	Dup,
	Pop,
}

struct Frame { function: String; at: Int; }

struct Interpreter {
	functions: Dict<[Instr]>;
	stack: [Int] = [];
	calls: [Frame] = [];
	deepest: Int = 0;
	steps: Int = 0;
}

impl Interpreter {
	fn pop() -> Result<Int, String> {
		let value = self.stack.pop();
		if value == null { return Result.Err("nothing on the stack"); }
		return Result.Ok(value);
	}

	fn binary(apply: fn(Int, Int) -> Int) -> Result<Int, String> {
		let right = self.pop()?;
		let left = self.pop()?;
		self.stack.push(apply(left, right));
		return Result.Ok(0);
	}

	fn run(entry: String) -> Result<Int, String> {
		self.calls.push(Frame { function: entry, at: 0 });
		while !self.calls.isEmpty() {
			let frame = self.calls[self.calls.len() - 1];
			let code = self.functions.get(frame.function);
			if code == null { return Result.Err("no function " + frame.function); }
			if frame.at >= code.len() {
				self.calls.pop();
				continue;
			}
			let instr = code[frame.at];
			frame.at++;
			self.steps++;
			if self.steps > 10000 { return Result.Err("too many steps"); }
			match instr {
				case Instr.Push(n): { self.stack.push(n); }
				case Instr.Add: { self.binary((a, b) -> a + b)?; }
				case Instr.Sub: { self.binary((a, b) -> a - b)?; }
				case Instr.Mul: { self.binary((a, b) -> a * b)?; }
				case Instr.Dup: {
					let top = self.pop()?;
					self.stack.push(top);
					self.stack.push(top);
				}
				case Instr.Pop: { self.pop()?; }
				case Instr.Call(name): {
					self.calls.push(Frame { function: name, at: 0 });
					if self.calls.len() > self.deepest { self.deepest = self.calls.len(); }
				}
				case Instr.Ret: { self.calls.pop(); }
				case Instr.JumpIfZero(target): {
					if self.pop()? == 0 { frame.at = target; }
				}
				case Instr.Jump(target): { frame.at = target; }
			}
		}
		return self.pop();
	}
}

// The places of a map reached from one, nearest first
fn reach(roads: Dict<[String]>, from: String) -> [String] {
	let seen = dict.empty<Bool>();
	let order: [String] = [];
	let queue = [from];
	seen.set(from, true);
	while true {
		let place = queue.removeAt(0);
		if place == null { break; }
		order.push(place);
		for next in roads.get(place) ?? [] {
			if !seen.has(next) {
				seen.set(next, true);
				queue.push(next);
			}
		}
	}
	return order;
}

// A counter whose changes can be taken back, each one a function that
// undoes it
struct Counter {
	value: Int = 0;
	undo: [fn() -> Void] = [];
}

impl Counter {
	fn add(amount: Int) {
		self.value += amount;
		self.undo.push(fn() { self.value -= amount; });
	}
	fn double() {
		let before = self.value;
		self.value *= 2;
		self.undo.push(fn() { self.value = before; });
	}
	fn back(times: Int) -> Int {
		let undone = 0;
		for n in 0..times {
			let step = self.undo.pop();
			if step == null { break; }
			step();
			undone++;
		}
		return undone;
	}
}`, `
		let functions = dict.empty<[Instr]>();
		// factorial: n on the stack, n! left on it
		functions.set("factorial", [
			Instr.Dup,
			Instr.JumpIfZero(8),
			Instr.Dup,
			Instr.Push(1),
			Instr.Sub,
			Instr.Call("factorial"),
			Instr.Mul,
			Instr.Ret,
			Instr.Pop,
			Instr.Push(1),
		]);
		functions.set("main", [Instr.Push(6), Instr.Call("factorial"), Instr.Push(10), Instr.Add]);
		functions.set("broken", [Instr.Push(1), Instr.Add]);
		functions.set("lost", [Instr.Call("nowhere")]);
		functions.set("forever", [Instr.Jump(0)]);

		for entry in ["main", "broken", "lost", "forever"] {
			let machine = Interpreter { functions: functions };
			match machine.run(entry) {
				case Result.Ok(value): { println(entry, value, machine.stack, machine.calls.len(), machine.deepest); }
				case Result.Err(message): { println(entry, "failed:", message, machine.stack, machine.calls.len()); }
			}
		}

		let roads = dict.empty<[String]>();
		roads.set("home", ["shop", "school"]);
		roads.set("shop", ["home", "market"]);
		roads.set("school", ["library", "home"]);
		roads.set("market", ["harbour"]);
		roads.set("library", ["market"]);
		roads.set("island", ["harbour"]);
		println(reach(roads, "home"));
		println(reach(roads, "island"), reach(roads, "nowhere"));

		let counter = Counter {};
		counter.add(5);
		counter.double();
		counter.add(-3);
		counter.double();
		println(counter.value, counter.undo.len());
		println(counter.back(1), counter.value, counter.back(2), counter.value);
		counter.add(1);
		println(counter.value, counter.back(9), counter.value, counter.back(1), counter.undo.len());
	`,
		"main 730 [] 0 8",
		"broken failed: nothing on the stack [] 1",
		"lost failed: no function nowhere [] 2",
		"forever failed: too many steps [] 1",
		`["home", "shop", "school", "market", "library", "harbour"]`,
		`["island", "harbour"] ["nowhere"]`,
		"14 4",
		"1 7 2 5",
		"6 2 0 0 0",
	)
}

// std.list takes items out of, and puts them into, the array it holds: an
// item removed is the one that was there, whatever the list is a list of.
func TestListChangesItsArrayInPlace(t *testing.T) {
	expect(t, "import std.list;\nimport std.list.{List};\nimport std.arrays;\nstruct City { name: String; }\nenum Light { Red, Green }", `
		let cities = list.of([City { name: "Oslo" }, City { name: "Lima" }, City { name: "Bern" }]);
		println(cities.pop()?.name, cities.removeAt(0)?.name, cities.len(), cities.removeAt(1)?.name, cities.removeAt(-1)?.name);
		cities.insert(0, City { name: "Lagos" });
		cities.insert(99, City { name: "Kyiv" });
		cities.insert(-1, City { name: "nowhere" });
		println(cities.map(city -> city.name).toArray());
		cities.clear();
		println(cities.len(), cities.pop()?.name, cities.isEmpty());

		let lights = list.empty<Light>();
		lights.add(Light.Red);
		lights.insert(0, Light.Green);
		println(lights.pop(), lights.pop(), lights.pop());

		let maybe = list.of<Int?>([1, null]);
		println(maybe.pop(), maybe.len(), maybe.pop(), maybe.len(), maybe.pop(), maybe.len());

		let nested = list.of([[1], [2, 3]]);
		println(nested.removeAt(0), nested.toArray(), nested.pop(), nested.pop());

		let functions = list.empty<fn(Int) -> Int>();
		functions.add(n -> n + 1);
		functions.insert(0, n -> n * 2);
		let double = functions.removeAt(0);
		if double != null { println(double(21), functions.len()); }

		// The array the list was made of is still not the list's
		let source = [1, 2, 3];
		let numbers = list.of(source);
		numbers.pop();
		numbers.insert(0, 0);
		numbers.removeAt(1);
		println(source, numbers.toArray());
		numbers.clear();
		println(source, numbers.toArray(), numbers.first());

		// A list used as a stack and as a queue, for long enough to grow
		let work = list.empty<Int>();
		let total = 0;
		for n in 0..500 {
			work.add(n);
			work.add(n);
			total += (work.removeAt(0) ?? 0) + (work.pop() ?? 0);
		}
		println(total, work.len(), work.isEmpty());
	`,
		"Bern Oslo 1 null null",
		`["Lagos", "Lima", "Kyiv"]`,
		"0 null true",
		"Red Green null",
		"null 1 1 0 null 0",
		"[1] [[2, 3]] [2, 3] null",
		"42 1",
		"[1, 2, 3] [0, 2]",
		"[1, 2, 3] [] null",
		"249500 0 true",
	)
}

// A stack pushed onto and popped from many times over, on a small heap:
// what is popped is given back, and the array does not keep growing.
func TestArraysPoppedItemsAreCollected(t *testing.T) {
	out, err := runWith(t, 512*1024, `
		import std.arrays;
		import std.strings;

		struct Entry { label: String; payload: [Int]; }

		fn main() -> Int {
			let stack: [Entry] = [];
			let queue: [String] = [];
			let popped = 0;
			let served = 0;
			for round in 0..300 {
				for n in 0..100 {
					stack.push(Entry { label: "entry ${round}.${n}", payload: [round, n, round + n] });
					queue.insertAt(0, "x".repeat(20) + toString(n));
				}
				while !stack.isEmpty() {
					let entry = stack.pop();
					if entry != null { popped += entry.payload.len(); }
				}
				while queue.len() > 10 {
					if queue.removeAt(0) != null { served++; }
				}
				queue.clear();
			}
			println(popped, served, stack.len(), queue.len());
			return 0;
		}
	`, nil)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if got := strings.TrimSpace(out); got != "90000 27000 0 0" {
		t.Errorf("expected 90000 27000 0 0, got %s", got)
	}
}
