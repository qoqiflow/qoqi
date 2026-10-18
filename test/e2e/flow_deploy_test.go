package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A daemon started with no program, given its programs by qoqi
// deploy, a version after another with no restart; the versions kept, and
// what changed between them; served again when it starts on its store.

// greeter answers the names it is sent until it is told to stop; each
// version greets its own way.
func greeter(greeting string) string {
	return `import std.flow;
import std.flow.{Flow, Inbox};

struct Greeter {
    names: Inbox<String> = flow.inbox<String>(8);
}

impl Flow<Int> for Greeter {
    fn run() -> Int {
        let greeted = 0;

        while true {
            stage greeting;

            let name = wait self.names.receive();

            if name == "stop" {
                return greeted;
            }

            greet(name);
            greeted = greeted + 1;
        }

        return 0;
    }
}

step fn greet(name: String) {
    println("` + greeting + ` ${name}");
}

fn main() {}
`
}

func waitOut(t *testing.T, out *lockedWriter, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(out.String(), what) {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %q:\n%s", what, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDeployingToADaemonThatRuns(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "greeter.qi")
	out := &lockedWriter{}
	daemon := serveIn(t, dir, out)
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		daemon.Wait()
	}()
	waitOut(t, out, "qoqi: no program yet: qoqi deploy <program> gives it one")
	if !strings.Contains(out.String(), "qoqi: serving from "+dir+", store "+filepath.Join(dir, "flows.db")+", at http://127.0.0.1:") {
		t.Errorf("expected the daemon to say its home, its store and its address:\n%s", out.String())
	}
	if log, err := os.ReadFile(filepath.Join(dir, "logs", "daemon.log")); err != nil || !strings.Contains(string(log), "serving from "+dir) {
		t.Errorf("expected what the daemon says in its log: %v\n%s", err, log)
	}

	// The first version
	os.WriteFile(program, []byte(greeter("Hello")), 0o644)
	if got, code := flowCommand(t, dir, "deploy", "greeter.qi"); code != 0 || !strings.Contains(got, "greeter deployed, version") {
		t.Fatalf("deploy: %d %s", code, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "greeter.flow.lock")); err != nil {
		t.Errorf("expected the lock next to the program: %v", err)
	}
	flowCommand(t, dir, "start", "Greeter", "g")
	flowCommand(t, dir, "send", "g", "names", `"ada"`, "--key", "1")
	waitOut(t, out, "Hello ada")

	// A second, said first, then sent: the instance greets with it
	os.WriteFile(program, []byte(greeter("Bonjour")), 0o644)
	if got, code := flowCommand(t, dir, "deploy", "greeter.qi", "--dry"); code != 0 || !strings.Contains(got, "would be deployed") || !strings.Contains(got, "1 instances move") {
		t.Fatalf("dry: %d %s", code, got)
	}
	flowCommand(t, dir, "send", "g", "names", `"lin"`, "--key", "2")
	waitOut(t, out, "Hello lin")
	if got, code := flowCommand(t, dir, "deploy", "greeter.qi"); code != 0 || !strings.Contains(got, "in place of") {
		t.Fatalf("second deploy: %d %s", code, got)
	}
	flowCommand(t, dir, "send", "g", "names", `"bo"`, "--key", "3")
	waitOut(t, out, "Bonjour bo")

	// The versions kept, and what changed
	got, _ := flowCommand(t, dir, "versions")
	if lines := strings.Split(strings.TrimSpace(got), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "#2") || !strings.Contains(lines[0], "serving") || !strings.Contains(lines[0], "1 alive") {
		t.Errorf("versions:\n%s", got)
	}
	got, _ = flowCommand(t, dir, "diff", "greeter")
	if !strings.Contains(got, "from greeter #1") || !strings.Contains(got, `-    println("Hello ${name}");`) || !strings.Contains(got, `+    println("Bonjour ${name}");`) {
		t.Errorf("diff:\n%s", got)
	}

	// A version that changes the stage, with no migration: refused, and
	// the one served goes on
	changed := strings.Replace(greeter("Salut"), "let greeted = 0;", "let greeted = 0;\n        let last = \"\";", 1)
	changed = strings.Replace(changed, "greeted = greeted + 1;", "greeted = greeted + 1;\n            last = name;", 1)
	os.WriteFile(program, []byte(changed), 0o644)
	if got, code := flowCommand(t, dir, "deploy", "greeter.qi"); code == 0 || !strings.Contains(got, "no migrate fn greeting") {
		t.Errorf("a change no migration answers: %d %s", code, got)
	}
	flowCommand(t, dir, "send", "g", "names", `"cy"`, "--key", "4")
	waitOut(t, out, "Bonjour cy")

	// Started again on its store, with no program: what it served
	daemon.Process.Signal(syscall.SIGTERM)
	daemon.Wait()
	again := &lockedWriter{}
	daemon = serveIn(t, dir, again)
	waitOut(t, again, "flows: Greeter")
	flowCommand(t, dir, "send", "g", "names", `"dee"`, "--key", "5")
	waitOut(t, again, "Bonjour dee")
	flowCommand(t, dir, "send", "g", "names", `"stop"`, "--key", "6")
	if got, code := flowCommand(t, dir, "result", "g", "--wait", "10s"); code != 0 || strings.TrimSpace(got) != "5" {
		t.Errorf("result: %d %s", code, got)
	}

	// Taken away, once its instance ended
	if got, code := flowCommand(t, dir, "undeploy", "greeter"); code != 0 || !strings.Contains(got, "greeter is taken away") {
		t.Errorf("undeploy: %d %s", code, got)
	}
	if got, _ := flowCommand(t, dir, "versions"); !strings.Contains(strings.Split(got, "\n")[0], "removed") {
		t.Errorf("versions after undeploy:\n%s", got)
	}
}

// qoqi deploy --watch deploys again what is saved: a mistake is said, and the version
// before goes on.
func TestWatching(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "greeter.qi")
	os.WriteFile(program, []byte(greeter("Hello")), 0o644)
	out := &lockedWriter{}
	daemon := serveIn(t, dir, out)
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		daemon.Wait()
	}()

	// qoqi deploy --watch, beside the daemon
	watched := &lockedWriter{}
	watch := exec.Command(qoqi, "deploy", "greeter.qi", "--watch")
	watch.Dir = dir
	watch.Env = flowEnv(dir)
	watch.Stdout, watch.Stderr = watched, watched
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		watch.Process.Signal(syscall.SIGTERM)
		watch.Wait()
	}()
	waitOut(t, watched, "greeter deployed, version")
	waitOut(t, watched, "watching greeter.qi")
	flowCommand(t, dir, "start", "Greeter", "g")
	flowCommand(t, dir, "send", "g", "names", `"ada"`, "--key", "1")
	waitOut(t, out, "Hello ada")

	// A mistake saved: said, nothing deployed
	os.WriteFile(program, []byte(strings.Replace(greeter("Hello"), "greeted = greeted + 1;", "greeted = greeted + true;", 1)), 0o644)
	waitOut(t, watched, "the version before goes on")
	flowCommand(t, dir, "send", "g", "names", `"lin"`, "--key", "2")
	waitOut(t, out, "Hello lin")

	// Fixed, and greeting otherwise: deployed as it is saved
	os.WriteFile(program, []byte(greeter("Hi")), 0o644)
	waitOut(t, watched, "instances move to it at their next stage")
	flowCommand(t, dir, "send", "g", "names", `"bo"`, "--key", "3")
	waitOut(t, out, "Hi bo")
}
