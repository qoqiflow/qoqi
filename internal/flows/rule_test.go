package flows_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
)

const imports = `
import std.flow.{Flow};
import std.http;
import std.io;
import std.os;
import std.time;
import std.random;
import std.task;
import std.json;
import std.strings;
import std.log;
`

// found is what the compiler says of a program: one line per error,
// "CODE line: message", with the notes of an F0001 after it.
func found(t *testing.T, src string) []string {
	t.Helper()
	errs := errors.NewHandler()
	compiler.Compile(imports+src+"\nfn main() {}\n", "flow.qi", errs)
	var out []string
	for _, d := range errs.Diagnostics {
		if d.Severity != errors.SeverityError {
			continue
		}
		line := fmt.Sprintf("%s %d: %s", d.Code, d.Pos.Line-strings.Count(imports, "\n"), d.Message)
		for _, note := range d.Notes {
			note = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(note), "= Note:"))
			// The file and line of a note are those of flow.qi
			if i := strings.Index(note, " at "); i >= 0 {
				note = note[:i]
			}
			line += " | " + note
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func expectFound(t *testing.T, src string, want ...string) {
	t.Helper()
	got := found(t, src)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected:\n  %s\ngot:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

const reminder = `
struct Reminder { user: String; }
`

// The world reached from run itself, or from what run calls, as deep as
// it goes: the error is at the call of the program's own code, with the
// calls that led there.
func TestTheWorldOutsideAStep(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        http.get("https://example.com");
    }
}`, "F0001 6: a flow reaches the network outside a step")

	// Listening, on whatever host, is the network too
	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        http.listen(0, "0.0.0.0");
    }
}`, "F0001 6: a flow reaches the network outside a step")

	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        remind(self.user);
    }
}

fn remind(user: String) {
    deliver(user);
}

fn deliver(user: String) {
    io.writeFile("/tmp/" + user, "hello");
}`, "F0001 15: a flow reaches files outside a step | 'deliver' is called by remind | 'remind' is called by Reminder.run")

	expectFound(t, reminder+`
impl Flow<Int> for Reminder {
    fn run() -> Int {
        let n = os.args().len();
        return n;
    }
}`, "F0001 6: a flow reaches the process and other programs outside a step")
}

// What a step does is its business, and so is what only a step calls; a
// function the flow never reaches does what it likes.
func TestStepsAreTheWay(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<Bool> for Reminder {
    fn run() -> Bool {
        let sent = send(self.user);
        return sent;
    }
}

step fn send(user: String) -> Bool {
    return deliver(user);
}

fn deliver(user: String) -> Bool {
    return http.get("https://example.com/" + user).isOk();
}

fn elsewhere() {
    io.writeFile("x", "y");
}`)
}

// Computing, waiting, the clock, random numbers and writing to the
// program's own output are not the world.
func TestWhatIsNotTheWorld(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<String> for Reminder {
    fn run() -> String {
        println("starting", time.now());
        log.info("a reminder", {"user": self.user});
        wait time.after(10.milliseconds());
        let pick = random.below(10);
        let words = self.user.split(" ").map(w -> w.upper());
        let data = json.parse("{\"a\": 1}");
        let doubled = [1, 2, 3].map(n -> n * 2).filter(n -> n > pick);
        let ch = task.channel<Int>(1);
        wait ch.send(doubled.len());
        let got = wait ch.receive() ?? 0;
        return "${words} ${data.isOk()} ${got}";
    }
}`)
}

// A function written inside run is part of it; one passed to a function
// of the library, kept in a variable, or kept in a field, is called.
func TestFunctionValues(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        [self.user].map(u -> io.writeFile(u, "x"));
    }
}`, "F0001 6: a flow reaches files outside a step")

	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        let action = shout;
        action(self.user);
    }
}

fn shout(text: String) {
    http.post("https://example.com", text, "text/plain");
}`, "F0001 12: a flow reaches the network outside a step | 'shout' is called by Reminder.run")

	expectFound(t, reminder+`
struct Handler { act: fn(String) -> Void; }

impl Flow<Void> for Reminder {
    fn run() {
        let handler = Handler { act: shout };
        handler.act(self.user);
    }
}

fn shout(text: String) {
    http.post("https://example.com", text, "text/plain");
}`, "F0001 14: a flow reaches the network outside a step | 'shout' is called by Reminder.run")

	// A function of the same arity whose value is never taken in the
	// flow is not what it calls
	expectFound(t, reminder+`
impl Flow<Int> for Reminder {
    fn run() -> Int {
        let double = fn(n: Int) -> Int { return n * 2; };
        return twice(double, 3);
    }
}

fn twice(f: fn(Int) -> Int, n: Int) -> Int {
    return f(f(n));
}

fn unrelated(n: Int) -> Int {
    io.writeFile("x", "${n}");
    return n;
}`)
}

// A method of a known type is that method; through a trait, every impl
// of it, since what the value is is only known when the program runs.
func TestMethods(t *testing.T) {
	expectFound(t, reminder+`
struct Mailer { to: String; }
struct Counter { n: Int; }

impl Mailer {
    fn send(text: String) { http.post(self.to, text, "text/plain"); }
}

impl Counter {
    fn send(text: String) { self.n += text.len(); }
}

impl Flow<Void> for Reminder {
    fn run() {
        let counter = Counter { n: 0 };
        counter.send(self.user);
    }
}`)

	expectFound(t, reminder+`
trait Notifier { fn notify(text: String); }
struct Mail { to: String; }
struct Note { log: [String]; }

impl Notifier for Mail {
    fn notify(text: String) { http.post(self.to, text, "text/plain"); }
}

impl Notifier for Note {
    fn notify(text: String) { self.log.push(text); }
}

impl Flow<Void> for Reminder {
    fn run() {
        let notifier: Notifier = Note { log: [] };
        notifier.notify(self.user);
    }
}`, "F0001 9: a flow reaches the network outside a step | 'Mail.notify' is called by Reminder.run")
}

// Tasks of a flow are part of it; so are generic functions, in the copy
// for the types they are used with.
func TestTasksAndGenerics(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        let job = task.start(fn() -> Bool { return io.exists("/tmp"); });
        println(wait job);
    }
}`, "F0001 6: a flow reaches files outside a step")

	expectFound(t, reminder+`
fn each<T>(items: [T], save: Bool) -> Int {
    if save {
        io.writeFile("x", "${items.len()}");
    }
    return items.len();
}

impl Flow<Int> for Reminder {
    fn run() -> Int {
        return each([self.user], false);
    }
}`, "F0001 6: a flow reaches files outside a step | 'each' is called by Reminder.run")
}

// One error per call that leads to the world, however many flows reach
// it; a function that calls itself is walked once.
func TestReportedOnce(t *testing.T) {
	expectFound(t, reminder+`
struct Payment { id: String; }

impl Flow<Void> for Reminder {
    fn run() { record(self.user, 3); }
}

impl Flow<Void> for Payment {
    fn run() { record(self.id, 1); }
}

fn record(text: String, times: Int) {
    if times > 0 {
        io.writeFile("log", text);
        record(text, times - 1);
    }
}`, "F0001 16: a flow reaches files outside a step | 'record' is called by Reminder.run")
}

// What a step is given and returns is data.
func TestStepsTakeData(t *testing.T) {
	expectFound(t, `
import std.task.{Task, Channel};
import std.dict.{Dict};

struct Invoice { id: String; amount: Int; lines: [String]; tags: Dict<String>; }
struct Bad { callback: fn(Int) -> Int; }
enum Status { Paid(String), Refused(String), Pending }
enum Wrapped { Plain(Int), Hook(Bad) }
trait Named { fn name() -> String; }

step fn fine(invoice: Invoice, n: Int?, all: [Invoice], pair: (Int, String)) -> Result<Status, String> { return Ok(Status.Pending); }
step fn nothing(n: Int) {}
step fn takesFn(f: fn(Int) -> Int) -> Int { return 1; }
step fn takesTask(t: Task<Int>) -> Int { return 1; }
step fn takesBad(b: Bad) -> Int { return 1; }
step fn takesWrapped(w: Wrapped) -> Int { return 1; }
step fn takesNamed(n: Named) -> Int { return 1; }
step fn givesChannel() -> Channel<Int> { return task.channel<Int>(1); }
step fn generic<T>(x: T) -> Int { return 1; }
step fn anything(x: Any) -> Int { return 1; }`,
		"F0004 13: what a step is given is kept with the flow, and has to be data: 'f' is a function",
		"F0004 14: what a step is given is kept with the flow, and has to be data: 't' is a std.task.Task<Int>, which stands for something alive in the running program",
		"F0004 15: what a step is given is kept with the flow, and has to be data: 'b' is a function, in the field 'callback' of Bad",
		"F0004 16: what a step is given is kept with the flow, and has to be data: 'w' is a function, in the field 'callback' of Bad, in Wrapped.Hook",
		"F0004 17: what a step is given is kept with the flow, and has to be data: 'n' is a Named, a trait: what it holds is only known when the program runs",
		"F0004 18: what a step returns is kept with the flow, and has to be data: this is a std.task.Channel<Int>, which stands for something alive in the running program",
		"F0004 19: a step has no type parameters: what it is given and returns is kept, and is of a type known where it is written",
		"F0004 20: what a step is given is kept with the flow, and has to be data: 'x' is Any, which may hold anything",
	)
}

// step is a word only before `fn name`: a variable or a function called
// step is used as before.
func TestStepStaysAName(t *testing.T) {
	expectFound(t, `
fn step(n: Int) -> Int { return n + 1; }

fn walk() -> Int {
    let step = 2;
    let steps = [step, step + 1];
    return steps.len() + step;
}`)
}

// A program without flows is not walked: its functions do what they like.
func TestNoFlowNoRule(t *testing.T) {
	expectFound(t, `
fn save() {
    io.writeFile("x", "y");
    http.get("https://example.com");
}`)
}

// What the language does through a trait goes to that trait's methods:
// an operator, a loop, a value written into a text. A deferred call and a
// method of a generic struct are calls like the others.
func TestThroughTheLanguage(t *testing.T) {
	expectFound(t, reminder+`
struct Money { cents: Int; }

impl Add for Money {
    fn plus(other: Money) -> Money {
        io.writeFile("audit", "adding");
        return Money { cents: self.cents + other.cents };
    }
}

impl Flow<Int> for Reminder {
    fn run() -> Int {
        let total = Money { cents: 1 } + Money { cents: 2 };
        return total.cents;
    }
}`, "F0001 8: a flow reaches files outside a step | 'Money.plus' is called by Reminder.run")

	expectFound(t, reminder+`
struct Lines { path: String; }

impl Iterable<String> for Lines {
    fn iterate() -> [String] {
        return io.readFile(self.path).or("").split("\n");
    }
}

impl Flow<Int> for Reminder {
    fn run() -> Int {
        let count = 0;
        for line in (Lines { path: self.user }) { count += line.len(); }
        return count;
    }
}`, "F0001 8: a flow reaches files outside a step | 'Lines.iterate' is called by Reminder.run")

	expectFound(t, reminder+`
struct Badge { name: String; }

impl Show for Badge {
    fn show() -> String { return io.readFile(self.name).or("?"); }
}

impl Flow<String> for Reminder {
    fn run() -> String {
        let badge = Badge { name: self.user };
        return "hello ${badge}";
    }
}`, "F0001 7: a flow reaches files outside a step | 'Badge.show' is called by Reminder.run")

	expectFound(t, reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        defer io.writeFile("done", self.user);
        println("working");
    }
}`, "F0001 6: a flow reaches files outside a step")

	expectFound(t, reminder+`
struct Box<T> { value: T; }

impl<T> Box<T> {
    fn keep() { io.writeFile("box", "${self.value}"); }
}

impl Flow<Void> for Reminder {
    fn run() {
        let box = Box<String> { value: self.user };
        box.keep();
    }
}`, "F0001 7: a flow reaches files outside a step | 'Box.keep' is called by Reminder.run")
}

// A step reached as a function value is still a step.
func TestStepAsAValue(t *testing.T) {
	expectFound(t, reminder+`
impl Flow<Bool> for Reminder {
    fn run() -> Bool {
        let action = send;
        return action(self.user);
    }
}

step fn send(user: String) -> Bool {
    return http.get("https://example.com/" + user).isOk();
}`)
}

// A long way to the world says its first calls, and counts the rest.
func TestLongWay(t *testing.T) {
	var src strings.Builder
	src.WriteString(reminder + "\nimpl Flow<Void> for Reminder {\n    fn run() { hop0(); }\n}\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&src, "\nfn hop%d() { hop%d(); }\n", i, i+1)
	}
	src.WriteString("\nfn hop40() { io.writeFile(\"far\", \"away\"); }\n")
	got := found(t, src.String())
	if len(got) != 1 {
		t.Fatalf("expected one error, got %v", got)
	}
	parts := strings.Split(got[0], " | ")
	if len(parts) != 8 || parts[1] != "'hop40' is called by hop39" || parts[7] != "and 35 more calls, up to Reminder.run" {
		t.Errorf("unexpected way:\n%s", strings.Join(parts, "\n"))
	}
}

// A database is the world: std.sql outside a step is said, and so is the
// store of the flows asked for outside one. Inside a step, both are the
// step's to reach.
func TestADatabaseOutsideAStep(t *testing.T) {
	expectFound(t, "import std.sql;"+reminder+`
impl Flow<Void> for Reminder {
    fn run() {
        sql.open("shop.db");
        flow.store();
        keep(self.user);
    }
}

step fn keep(user: String) {
    let store = flow.store().expect("the store");
    store.exec("INSERT INTO users (name) VALUES (?)", [sql.Value.Text(user)]).expect("kept");
    sql.open("shop.db");
}`, "F0001 6: a flow reaches a database outside a step", "F0001 7: a flow reaches a database outside a step")
}
