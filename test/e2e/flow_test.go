package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const shopProgram = `import std.flow.{Flow};
import std.time;

struct Order { id: String; amount: Int; }

impl Flow<String> for Order {
    fn run() -> String {
        let hold = reserve(self.id);
        wait time.after(700.milliseconds());
        let paid = charge(self.id, self.amount);
        return "${hold} paid ${paid}";
    }
}

step fn reserve(id: String) -> String {
    println("WORLD reserve", id);
    return "hold-" + id;
}

step fn charge(id: String, amount: Int) -> Int {
    println("WORLD charge", id, amount);
    return amount * 100;
}

fn main() {}
`

// flowEnv is the environment of a qoqi command whose home, QOQI_PATH,
// is dir: the daemon keeps its files there, and the commands find it by
// them. What follows is added to it.
func flowEnv(dir string, more ...string) []string {
	env := append(os.Environ(), "QOQI_PATH="+dir, "QOQI_URL=", "QOQI_TOKEN=", "QOQI_STORE=")
	return append(env, more...)
}

// flowCommand runs qoqi in a folder, the folder its home.
func flowCommand(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(qoqi, args...)
	cmd.Dir = dir
	cmd.Env = flowEnv(dir)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// serveCommand is qoqi serve in a folder, the folder its home, with
// what the environment adds.
func serveCommand(dir string, env ...string) *exec.Cmd {
	cmd := exec.Command(qoqi, "serve", "--listen", "127.0.0.1:0")
	cmd.Dir = dir
	cmd.Env = flowEnv(dir, env...)
	return cmd
}

// serveIn starts a daemon with no program in a folder, the folder its
// home, and waits until it says where it is in daemon.json.
func serveIn(t *testing.T, dir string, out *lockedWriter, env ...string) *exec.Cmd {
	t.Helper()
	cmd := serveCommand(dir, env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "daemon.json")); err == nil && strings.Contains(string(data), `"pid": `+itoa(cmd.Process.Pid)) {
			return cmd
		}
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Kill()
	t.Fatalf("the daemon did not start:\n%s", out.String())
	return nil
}

// deployIn deploys a program, or a folder of them, to the daemon of a
// folder, and gives what qoqi deploy said.
func deployIn(t *testing.T, dir, program string, daemon *lockedWriter) string {
	t.Helper()
	got, code := flowCommand(t, dir, "deploy", program)
	if code != 0 {
		t.Fatalf("deploy %s: %d %s\ndaemon:\n%s", program, code, got, daemon.String())
	}
	return got
}

// startDaemon starts qoqi serve in a folder, waits until it says where
// it is, and deploys shop.qi to it.
func startDaemon(t *testing.T, dir string, out *lockedWriter, env ...string) *exec.Cmd {
	t.Helper()
	cmd := serveIn(t, dir, out, env...)
	got, code := flowCommand(t, dir, "deploy", "shop.qi")
	if code != 0 {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("deploy shop.qi: %d %s\ndaemon:\n%s", code, got, out.String())
	}
	return cmd
}

// A daemon killed while an instance sleeps; another started on the same
// file finishes it: each step done once, across the two processes.
func TestAFlowOutlivesItsDaemon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(shopProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)
	if out, code := flowCommand(t, dir, "start", "Order", "order:A1", `{"id": "A1", "amount": 3}`); code != 0 || !strings.Contains(out, `"status"`) {
		t.Fatalf("start: %d %s", code, out)
	}

	// Asleep after its first step
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(first.String(), "WORLD reserve A1") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if out, _ := flowCommand(t, dir, "ls"); !strings.Contains(out, "order:A1") || !strings.Contains(out, "waiting") {
		t.Fatalf("expected it waiting:\n%s", out)
	}
	if err := daemon.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	daemon.Wait()

	second := &lockedWriter{}
	again := startDaemon(t, dir, second)
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	out, code := flowCommand(t, dir, "result", "order:A1", "--wait", "20s")
	if code != 0 || strings.TrimSpace(out) != `"hold-A1 paid 300"` {
		t.Fatalf("result: %d %s\nfirst daemon:\n%s\nsecond daemon:\n%s", code, out, first.String(), second.String())
	}
	if strings.Count(first.String()+second.String(), "WORLD reserve") != 1 || strings.Count(second.String(), "WORLD charge A1 3") != 1 {
		t.Errorf("each step once, the second in the second daemon:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}

	// A second daemon from the same home is refused while the first lives
	output, err := serveCommand(dir).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "a daemon (process "+itoa(again.Process.Pid)+") runs from "+dir+" already, at http://127.0.0.1:") {
		t.Errorf("expected a second daemon to be refused, got %v:\n%s", err, output)
	}

	// The commands say what is wrong
	if out, code := flowCommand(t, dir, "start", "Refund", "r1"); code == 0 || !strings.Contains(out, "the program has no flow Refund") {
		t.Errorf("an unknown flow: %d %s", code, out)
	}
	if out, code := flowCommand(t, dir, "show", "nobody"); code == 0 || !strings.Contains(out, "there is no instance nobody") {
		t.Errorf("an unknown instance: %d %s", code, out)
	}
}

const napProgram = `import std.flow;
import std.flow.{Flow};
import std.time;

struct Trip { city: String; }

impl Flow<Result<String, String>> for Trip {
    fn run() -> Result<String, String> {
        let seat = hold(self.city)?;
        wait time.after(1.hours());
        return Ok(seat);
    }
}

step fn hold(city: String) -> Result<String, String> {
    println("WORLD hold", city);
    flow.undo(free, "seat-" + city);
    return Ok("seat-" + city);
}

step fn free(seat: String) -> Result<Void, String> {
    println("WORLD free", seat);
    return Ok(null);
}

fn main() {}
`

// qoqi cancel: an instance asleep for an hour ends at once, failed,
// what it gave flow.undo run by the daemon.
func TestCancellingFromTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(napProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &lockedWriter{}
	daemon := startDaemon(t, dir, out)
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		daemon.Wait()
	}()
	if got, code := flowCommand(t, dir, "start", "Trip", "trip:rome", `{"city": "rome"}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "WORLD hold rome") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if got, code := flowCommand(t, dir, "cancel", "trip:rome", "plans changed"); code != 0 || !strings.Contains(got, "trip:rome is cancelled") {
		t.Fatalf("cancel: %d %s", code, got)
	}
	got, code := flowCommand(t, dir, "result", "trip:rome", "--wait", "10s")
	if code == 0 || !strings.Contains(got, "cancelled: plans changed") {
		t.Errorf("result: expected it cancelled, got %d %s", code, got)
	}
	if !strings.Contains(out.String(), "WORLD free seat-rome") {
		t.Errorf("expected the seat freed by the daemon:\n%s", out.String())
	}
	if got, _ := flowCommand(t, dir, "ls", "--status", "failed"); !strings.Contains(got, "trip:rome") {
		t.Errorf("expected it among the failed:\n%s", got)
	}
	if got, code := flowCommand(t, dir, "cancel", "trip:rome"); code == 0 || !strings.Contains(got, "has ended") {
		t.Errorf("cancelling it again: expected a refusal, got %d %s", code, got)
	}
}

const familyProgram = `import std.flow;
import std.flow.{Flow};
import std.time;

struct Payment { id: String; }

impl Flow<String> for Payment {
    fn run() -> String {
        let auth = authorize(self.id);
        wait time.after(700.milliseconds());
        return capture(auth);
    }
}

struct Order { id: String; }

impl Flow<String> for Order {
    fn run() -> String {
        let hold = reserve(self.id);
        match wait flow.start("payment:${self.id}", Payment { id: self.id }) {
            case Ok(paid): { return "${hold} ${paid}"; }
            case Err(cancelled): { return cancelled.reason; }
        }
    }
}

step fn reserve(id: String) -> String {
    println("WORLD reserve", id);
    return "hold-" + id;
}

step fn authorize(id: String) -> String {
    println("WORLD authorize", id);
    return "auth-" + id;
}

step fn capture(auth: String) -> String {
    println("WORLD capture", auth);
    return "paid-" + auth;
}

fn main() {}
`

// An order waits for its payment, which sleeps; the daemon is killed,
// another takes the two up: the order ends with what its payment gave,
// each step done once.
func TestAFamilyOutlivesItsDaemon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(familyProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)
	if got, code := flowCommand(t, dir, "start", "Order", "order:B2", `{"id": "B2"}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(first.String(), "WORLD authorize B2") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got, _ := flowCommand(t, dir, "ls"); !strings.Contains(got, "waits for payment:B2") {
		t.Errorf("expected the order shown waiting for its payment:\n%s", got)
	}
	daemon.Process.Signal(syscall.SIGKILL)
	daemon.Wait()

	second := &lockedWriter{}
	again := startDaemon(t, dir, second)
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	got, code := flowCommand(t, dir, "result", "order:B2", "--wait", "20s")
	if code != 0 || strings.TrimSpace(got) != `"hold-B2 paid-auth-B2"` {
		t.Fatalf("result: %d %s\nfirst:\n%s\nsecond:\n%s", code, got, first.String(), second.String())
	}
	world := first.String() + second.String()
	for _, step := range []string{"WORLD reserve B2", "WORLD authorize B2", "WORLD capture auth-B2"} {
		if strings.Count(world, step) != 1 {
			t.Errorf("expected %q once:\n%s", step, world)
		}
	}
}

// subProgram bills each month; the version says what it prints.
func subProgram(version string) string {
	return `import std.flow.{Flow};
import std.time;

struct Sub { months: Int; }

impl Flow<Int> for Sub {
    fn run() -> Int {
        let paid = 0;

        for month in 0..self.months {
            stage month;
            paid += bill(month);
            wait time.after(700.milliseconds());
        }

        return paid;
    }
}

step fn bill(month: Int) -> Int {
    println("WORLD ` + version + ` bill", month);
    return 100;
}

fn main() {}
`
}

// A new version deployed while an instance sleeps: the daemon started on
// it takes the instance up in its own version, and the instance moves to
// the new one at its next stage. flow.lock says the shapes of the stages.
func TestAnInstanceMovesToANewVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(version string) {
		if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(subProgram(version)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("v1")
	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)
	if got, code := flowCommand(t, dir, "start", "Sub", "sub:1", `{"months": 3}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(first.String(), "WORLD v1 bill 0") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	daemon.Process.Signal(syscall.SIGTERM)
	daemon.Wait()
	lock, err := os.ReadFile(filepath.Join(dir, "shop.flow.lock"))
	if err != nil || !strings.Contains(string(lock), `"paid": "Int"`) {
		t.Fatalf("expected flow.lock with the shape of stage month, got %v\n%s", err, lock)
	}

	write("v2")
	second := &lockedWriter{}
	again := startDaemon(t, dir, second)
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	got, code := flowCommand(t, dir, "result", "sub:1", "--wait", "20s")
	if code != 0 || strings.TrimSpace(got) != "300" {
		t.Fatalf("result: %d %s\nfirst:\n%s\nsecond:\n%s", code, got, first.String(), second.String())
	}
	if !strings.Contains(second.String(), "WORLD v2 bill 1") || !strings.Contains(second.String(), "WORLD v2 bill 2") || strings.Contains(second.String(), "WORLD v1") {
		t.Errorf("expected the next months billed by the new version:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

// A new version that changes a stage while an instance is alive: refused
// without a migration, served with one, and the instance migrates.
func TestANewVersionThatChangesAStage(t *testing.T) {
	dir := t.TempDir()
	write := func(src string) {
		if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(subProgram("v1"))
	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)
	if got, code := flowCommand(t, dir, "start", "Sub", "sub:1", `{"months": 3}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(first.String(), "WORLD v1 bill 0") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	daemon.Process.Signal(syscall.SIGTERM)
	daemon.Wait()

	// The stage keeps a count it had not
	counting := strings.Replace(subProgram("v2"), "let paid = 0;", "let paid = 0;\n        let count = 7;", 1)
	counting = strings.Replace(counting, "paid += bill(month);", "paid += bill(month) + count;", 1)
	write(counting)
	second := &lockedWriter{}
	again := serveIn(t, dir, second)
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	if output, code := flowCommand(t, dir, "deploy", "shop.qi"); code == 0 || !strings.Contains(output, "count: Int is new, and no migrate fn month says how an instance there goes on (1 instances of Sub alive)") {
		t.Fatalf("expected the version refused, got %d:\n%s", code, output)
	}

	write(counting + `
struct Month { paid: Int; count: Int; month: Int; }

impl Sub {
    migrate fn month(paid: Int, month: Int) -> Month {
        return Month { paid: paid, count: 1, month: month };
    }
}
`)
	deployIn(t, dir, "shop.qi", second)
	got, code := flowCommand(t, dir, "result", "sub:1", "--wait", "20s")
	if code != 0 || strings.TrimSpace(got) != "302" {
		t.Fatalf("result: %d %s\nfirst:\n%s\nsecond:\n%s", code, got, first.String(), second.String())
	}
}

// An order stopped by a mistake after it was paid; the code fixed and
// served again; qoqi resume says what it would do, then --go does
// it: the payment is not taken twice.
func TestResumingFromTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	program := func(after string) string {
		return `import std.flow.{Flow};

struct Order { amount: Int; }

impl Flow<String> for Order {
    fn run() -> String {
        stage paying;
        let paid = charge(self.amount);
` + after + `
        return ship(paid);
    }
}

step fn charge(amount: Int) -> String {
    println("WORLD charge", amount);
    return "paid";
}

step fn ship(paid: String) -> String {
    println("WORLD ship", paid);
    return "shipped";
}

fn main() {}
`
	}
	write := func(src string) {
		if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(program("let none: [Int] = [];\n        println(none[1]);"))
	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)
	flowCommand(t, dir, "start", "Order", "o", `{"amount": 30}`)
	if got, code := flowCommand(t, dir, "result", "o", "--wait", "10s"); code == 0 || !strings.Contains(got, "index out of bounds") {
		t.Fatalf("expected it stopped: %d %s", code, got)
	}
	daemon.Process.Signal(syscall.SIGTERM)
	daemon.Wait()

	write(program(""))
	second := &lockedWriter{}
	again := startDaemon(t, dir, second)
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	got, code := flowCommand(t, dir, "resume", "o")
	if code != 0 || !strings.Contains(got, "take what o/") || !strings.Contains(got, `then: ship ["paid"]`) || !strings.Contains(got, "qoqi resume o --go does it") {
		t.Fatalf("a dry run: %d\n%s", code, got)
	}
	if got, code := flowCommand(t, dir, "resume", "o", "--go"); code != 0 {
		t.Fatalf("resume: %d %s", code, got)
	}
	if got, code := flowCommand(t, dir, "result", "o", "--wait", "10s"); code != 0 || strings.TrimSpace(got) != `"shipped"` {
		t.Fatalf("result: %d %s\n%s", code, got, second.String())
	}
	if strings.Count(first.String()+second.String(), "WORLD charge") != 1 {
		t.Errorf("the payment once:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

// A folder of programs, served by one daemon: each on tables of its own
// in one file, an instance of one name in each; two programs that have a
// flow of one name are refused.
func TestServingAFolder(t *testing.T) {
	dir := t.TempDir()
	programs := filepath.Join(dir, "flows")
	if err := os.Mkdir(programs, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(programs, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("shop.qi", shopProgram)
	write("billing.qi", subProgram("billing"))
	write("billing_test.qi", "fn testNothing() {}\n")

	out := &lockedWriter{}
	cmd := serveIn(t, dir, out)
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	waitOut(t, out, "no program yet: qoqi deploy <program> gives it one")
	deployed := deployIn(t, dir, "flows", out)
	if !strings.Contains(deployed, "billing deployed, version") || !strings.Contains(deployed, "shop deployed, version") {
		t.Fatalf("expected both programs deployed:\n%s", deployed)
	}

	if got, code := flowCommand(t, dir, "start", "Order", "x", `{"id": "A1", "amount": 3}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	if got, _ := flowCommand(t, dir, "start", "Sub", "x", `{"months": 1}`); !strings.Contains(got, "x is an instance of Order") {
		t.Errorf("a name is one instance across the programs, got %s", got)
	}
	flowCommand(t, dir, "start", "Sub", "y", `{"months": 1}`)
	for id, want := range map[string]string{"x": `"hold-A1 paid 300"`, "y": "100"} {
		if got, code := flowCommand(t, dir, "result", id, "--wait", "20s"); code != 0 || strings.TrimSpace(got) != want {
			t.Errorf("%s: expected %s, got %d %s\n%s", id, want, code, got, out.String())
		}
	}
	for _, lock := range []string{"shop.flow.lock", "billing.flow.lock"} {
		if _, err := os.Stat(filepath.Join(programs, lock)); err != nil {
			t.Errorf("expected %s next to its program: %v", lock, err)
		}
	}

	// Two programs with a flow of one name
	clash := filepath.Join(dir, "clash")
	os.Mkdir(clash, 0o755)
	os.WriteFile(filepath.Join(clash, "a.qi"), []byte(shopProgram), 0o644)
	os.WriteFile(filepath.Join(clash, "b.qi"), []byte(shopProgram), 0o644)
	if output, code := flowCommand(t, dir, "deploy", "clash"); code == 0 || !strings.Contains(output, "a and b both have a flow Order") || strings.Contains(output, "a deployed") {
		t.Errorf("expected two programs with one flow refused, got %d:\n%s", code, output)
	}
}

// A project of several files, deployed from its folder: the entry its
// qoqi.json names, with the modules it imports, which are no programs;
// a flow of a module is the program's, started by its name.
func TestServingAProjectOfModules(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"qoqi.json": `{"entry": "main.qi"}`,
		"main.qi":   "import easy;\nimport words;\n\nfn main() {\n    println(words.hello());\n}\n",
		"words.qi":  "pub fn hello() -> String {\n    return \"hello\";\n}\n",
		"easy.qi": `import std.flow.{Flow};
import words;

struct Easy {
    who: String;
}

impl Flow<String> for Easy {
    fn run() -> String {
        return greet(self.who);
    }
}

step fn greet(who: String) -> String {
    return "${words.hello()} ${who}";
}
`,
	} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out := &lockedWriter{}
	cmd := serveIn(t, dir, out)
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	if deployed := deployIn(t, dir, "app", out); !strings.Contains(deployed, "main deployed, version") || strings.Count(deployed, "deployed") != 1 {
		t.Fatalf("expected the entry deployed, alone:\n%s", deployed)
	}
	if got, code := flowCommand(t, dir, "start", "Easy", "e", `{"who": "ada"}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	if got, code := flowCommand(t, dir, "result", "e", "--wait", "20s"); code != 0 || strings.TrimSpace(got) != `"hello ada"` {
		t.Errorf("expected \"hello ada\", got %d %s\n%s", code, got, out.String())
	}
	if _, err := os.Stat(filepath.Join(app, "main.flow.lock")); err != nil {
		t.Errorf("expected the lock next to the entry: %v", err)
	}
}

// qoqi send: a message for an instance that waits on its inbox,
// sent twice under one key, taken once.
func TestSendingFromTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(`import std.flow;
import std.flow.{Flow, Inbox};

struct Note { text: String; }

struct Mailbox {
    owner: String;
    notes: Inbox<Note> = flow.inbox<Note>(4);
}

impl Flow<String> for Mailbox {
    fn run() -> String {
        let first = wait self.notes.receive();
        let second = wait self.notes.receive();
        return "${self.owner}: ${first.text}, ${second.text}";
    }
}

fn main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &lockedWriter{}
	daemon := startDaemon(t, dir, out)
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		daemon.Wait()
	}()
	if got, code := flowCommand(t, dir, "start", "Mailbox", "m", `{"owner": "ada"}`); code != 0 {
		t.Fatalf("start: %d %s", code, got)
	}
	if got, code := flowCommand(t, dir, "send", "m", "notes", `{"text": "hello"}`, "--key", "n1"); code != 0 || !strings.Contains(got, "sent to m") {
		t.Fatalf("send: %d %s", code, got)
	}
	if got, _ := flowCommand(t, dir, "send", "m", "notes", `{"text": "hello"}`, "--key", "n1"); !strings.Contains(got, "had it already") {
		t.Errorf("the same key: %s", got)
	}
	if got, code := flowCommand(t, dir, "send", "m", "letters", `{"text": "x"}`); code == 0 || !strings.Contains(got, "has no inbox letters") {
		t.Errorf("no such inbox: %d %s", code, got)
	}
	// What it waits for, in words: not its inbox's inner name
	if got, _ := flowCommand(t, dir, "ls"); !strings.Contains(got, "waits for a message on notes") || strings.Contains(got, "inbox:") {
		t.Errorf("ls of an instance waiting on its inbox: %s", got)
	}
	flowCommand(t, dir, "send", "m", "notes", `{"text": "again"}`, "--key", "n2")
	if got, code := flowCommand(t, dir, "result", "m", "--wait", "10s"); code != 0 || strings.TrimSpace(got) != `"ada: hello, again"` {
		t.Errorf("result: %d %s\n%s", code, got, out.String())
	}

	// Values as Qoqi writes them, and the API's JSON when asked
	if got, _ := flowCommand(t, dir, "ls"); !strings.Contains(got, `returned "ada: hello, again"`) {
		t.Errorf("ls of an ended instance: %s", got)
	}
	got, _ := flowCommand(t, dir, "show", "m")
	for _, line := range []string{"Mailbox   m   version ", `owner   "ada"`} {
		if !strings.Contains(got, line) {
			t.Errorf("show: expected %q in\n%s", line, got)
		}
	}
	if strings.Contains(got, "$type") || strings.Contains(got, "notes") {
		t.Errorf("show: expected no data written as JSON, and no inbox among the fields:\n%s", got)
	}
	if got, _ := flowCommand(t, dir, "show", "m", "--json"); !strings.Contains(got, `"$type": "Mailbox"`) {
		t.Errorf("show --json: expected the API's JSON, got %s", got)
	}
}

// The flows of examples/flows pass their own tests.
func TestTheExamplesOfFlows(t *testing.T) {
	res := runQoqi(t, "test", "examples/flows")
	out := res.stdout + res.stderr
	if res.exit != 0 || !strings.Contains(out, "19 tests in 3 files: 19 passed") {
		t.Errorf("expected the examples' tests to pass, got %d:\n%s", res.exit, out)
	}
}

// Without a daemon, the commands say how to start one.
func TestFlowCommandsWithoutADaemon(t *testing.T) {
	out, code := flowCommand(t, t.TempDir(), "ls")
	if code == 0 || !strings.Contains(out, "qoqi serve") {
		t.Errorf("expected to be told to start a daemon, got %d %s", code, out)
	}
}

const approvalTests = `import std.flow;
import std.flow.{Flow, Settled};
import std.test;
import std.time;

struct Expense { author: String; amount: Int; }

impl Flow<String> for Expense {
    fn run() -> String {
        let approver = managerOf(self.author);
        for round in 0..3 {
            if ask(approver, self.amount) {
                return "approved by ${approver}";
            }
            wait time.after(2.days());
            approver = managerOf(approver);
        }
        return "refused: no answer";
    }
}

step fn managerOf(name: String) -> String {
    println("WORLD looking up", name);
    return "boss of " + name;
}

step fn ask(approver: String, amount: Int) -> Bool {
    println("WORLD mail to", approver);
    return false;
}

fn testNoAnswerGoesUp() {
    let trial = flow.test(Expense { author: "lin", amount: 1200 });
    let asked: [String] = [];
    trial.replace(managerOf, fn(name: String) -> String { return name + "'s boss"; });
    trial.replace(ask, fn(approver: String, amount: Int) -> Bool {
        asked.push(approver);
        return approver == "lin's boss's boss";
    });

    trial.go();
    test.equal(trial.status(), "waiting");
    test.equal(asked, ["lin's boss"]);

    trial.advance(2.days());
    test.equal(trial.status(), "done");
    test.equal(trial.result(), "approved by lin's boss's boss");
    test.equal(trial.calls(ask), 2);
    test.equal(trial.calls(managerOf), 2);
}

fn testRefusedAfterThreeRounds() {
    let trial = flow.test(Expense { author: "ada", amount: 5 });
    trial.replace(ask, fn(approver: String, amount: Int) -> Bool { return false; });
    trial.replace(managerOf, fn(name: String) -> String { return name + "+"; });
    trial.go();
    trial.advance(1.days());
    test.equal(trial.status(), "waiting");
    trial.advance(1.days());
    trial.advance(2.days());
    trial.advance(2.days());
    test.equal(trial.result(), "refused: no answer");
}

fn testEveryCrash() {
    let trial = flow.test(Expense { author: "lin", amount: 1200 });
    trial.replace(managerOf, fn(name: String) -> String { return name + "'s boss"; });
    trial.replace(ask, fn(approver: String, amount: Int) -> Bool { return approver.len() > 15; });
    trial.everyCrash(fn(t: flow.Trial<String>) {
        t.go();
        t.advance(2.days());
        test.equal(t.result(), "approved by lin's boss's boss");
    });
}

struct Payout { amount: Int; }

impl Flow<String> for Payout {
    fn run() -> String {
        match send(self.amount) {
            case Done(reference): { return reference; }
            case Unknown: { return "unknown"; }
        }
    }
}

step fn send(amount: Int) -> Settled<String> within 5000.milliseconds() {
    return Settled.Done("ref-${amount}");
}

fn testAPayoutStoppedAnywhere() {
    flow.test(Payout { amount: 7 }).everyCrash(fn(t: flow.Trial<String>) {
        t.go();
    });
}
`

// flow.test under qoqi test: a flow tried on its own clock, with its
// steps replaced, and stopped at every write; a payout stopped as its
// answer was being kept ends otherwise, and the test says so.
func TestFlowsUnderQoqiTest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "approval_test.qi"), []byte(approvalTests), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runQoqiIn(t, dir, "", "test")
	out := res.stdout + res.stderr
	for _, want := range []string{
		"ok    no answer goes up",
		"ok    refused after three rounds",
		"ok    every crash",
		"FAIL  a payout stopped anywhere",
		`it gave "unknown", and "ref-7" without the stop`,
		"3 passed, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if res.exit == 0 {
		t.Errorf("a failing test fails the command")
	}
	if strings.Contains(out, "WORLD mail") {
		t.Errorf("the steps replaced do not reach the world:\n%s", out)
	}
}

// lockedWriter is a builder a process writes to while the test reads it.
type lockedWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func itoa(n int) string { return strconv.Itoa(n) }

const deliveryProgram = `import std.flow.{Flow};
import std.time;

struct Address {
    city: String;
    floor: Int?;
}

enum Speed {
    Standard,
    Express(Int),
}

struct Delivery {
    to: Address;
    speed: Speed;
}

impl Flow<String> for Delivery {
    fn run() -> String {
        let van = book(self.to);
        wait time.after(1.hours());
        return van;
    }
}

step fn book(to: Address) -> String {
    println("WORLD book", to.city);
    return "van-" + to.city;
}

fn main() {}
`

// The console of a daemon: its page served by the binary itself, the
// program described, a start written as plain JSON, where the instance
// waits; and the token, the same when the daemon is started again, so
// that a page left open goes on.
func TestTheConsoleOfADaemon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop.qi"), []byte(deliveryProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	first := &lockedWriter{}
	daemon := startDaemon(t, dir, first)

	address := func() (string, string) {
		data, err := os.ReadFile(filepath.Join(dir, "daemon.json"))
		if err != nil {
			t.Fatal(err)
		}
		var a struct {
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(data, &a); err != nil {
			t.Fatal(err)
		}
		return a.URL, a.Token
	}
	get := func(url, token string) (int, string) {
		req, _ := http.NewRequest("GET", url, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	url, token := address()
	if status, page := get(url+"/instances/delivery:1", ""); status != http.StatusOK || !strings.Contains(page, `<div id="root">`) || !strings.Contains(page, "/assets/") {
		t.Fatalf("the console, built into qoqi: %d %s", status, page)
	}
	if status, body := get(url+"/api/program", token); status != http.StatusOK || !strings.Contains(body, `"text": "Address"`) || !strings.Contains(body, `"name": "Express"`) {
		t.Errorf("the program described: %d %s", status, body)
	}
	kept, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil || strings.TrimSpace(string(kept)) != token {
		t.Errorf("the token is kept in the home of the daemon: %q %v", kept, err)
	}

	// Plain JSON: no "$type", a variant by its name
	if out, code := flowCommand(t, dir, "start", "Delivery", "delivery:1", `{"to": {"city": "Kribi"}, "speed": {"Express": 2}}`); code != 0 || !strings.Contains(out, `"$type": "Address"`) || !strings.Contains(out, `"$type": "Speed.Express"`) {
		t.Fatalf("a start written plainly: %d %s", code, out)
	}
	if out, code := flowCommand(t, dir, "start", "Delivery", "delivery:2", `{"to": {"town": "Kribi"}, "speed": "Standard"}`); code == 0 || !strings.Contains(out, "to has no field town") {
		t.Errorf("a field it has not, refused with where: %d %s", code, out)
	}

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(first.String(), "WORLD book Kribi") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	var detail struct {
		Status string `json:"status"`
		Tasks  []struct {
			Frames []struct {
				Function string `json:"function"`
				Line     int    `json:"line"`
			} `json:"frames"`
		} `json:"tasks"`
	}
	for time.Now().Before(deadline) {
		_, body := get(url+"/api/instances/delivery:1", token)
		_ = json.Unmarshal([]byte(body), &detail)
		if detail.Status == "waiting" && len(detail.Tasks) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	found := false
	for _, f := range detail.Tasks[0].Frames {
		found = found || (f.Function == "Delivery.run" && f.Line == 22)
	}
	if !found {
		t.Errorf("it waits at shop.qi:22: %+v", detail)
	}

	daemon.Process.Signal(syscall.SIGKILL)
	daemon.Wait()
	again := startDaemon(t, dir, &lockedWriter{})
	defer func() {
		again.Process.Signal(syscall.SIGTERM)
		again.Wait()
	}()
	url, second := address()
	if second != token {
		t.Errorf("started again, the daemon has the same token")
	}
	if status, _ := get(url+"/api/instances/delivery:1", token); status != http.StatusOK {
		t.Errorf("a page left open goes on with its token: %d", status)
	}
}
