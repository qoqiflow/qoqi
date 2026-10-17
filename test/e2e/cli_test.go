package e2e

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A module with a variable of its own, opened on its own as the editor
// and qoqi check do: its functions see the variable, as they do once
// the module is imported, instead of the file being taken for a script.
const counter = `let count = 0;

pub fn next() -> Int {
    count += 1;
    return count;
}
`

func TestAModuleWithAVariableChecks(t *testing.T) {
	dir := project(t, map[string]string{"counter.qi": counter})
	res := runQoqiIn(t, dir, "", "check", "counter.qi")
	if strings.Contains(res.stdout+res.stderr, "a variable of the script") {
		t.Errorf("expected the module's variable to be seen by its function, got\n%s%s", res.stdout, res.stderr)
	}
}

// A program that listens for signals stops its own way on Ctrl-C and on
// SIGTERM; one that does not take the signal is stopped by a second
// Ctrl-C; and listening does not keep a program alive that is done.
const graceful = `import std.os;
import std.task;
import std.time;

fn main() -> Int {
    let stop = os.signals();
    let stubborn = os.args().contains("stubborn");
    println("ready");
    if stubborn {
        while true { wait time.after(20.milliseconds()); }
    }
    let name = wait stop.receive() ?? "none";
    defer println("cleaned up");
    println("asked to stop:", name);
    return 3;
}
`

const quick = `import std.os;

fn main() -> Int {
    let _stop = os.signals();
    println("done");
    return 0;
}
`

func TestSignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals are for Unix")
	}
	dir := project(t, map[string]string{"graceful.qi": graceful, "quick.qi": quick})

	if res := runQoqiIn(t, dir, "", "run", "quick.qi"); res.exit != 0 || res.stdout != "done\n" {
		t.Errorf("expected a program done with its work to end, got exit %d\n%s%s", res.exit, res.stdout, res.stderr)
	}

	start := func(args ...string) (*exec.Cmd, *lockedBuffer, *lockedBuffer) {
		var stdout, stderr lockedBuffer
		cmd := exec.Command(qoqi, append([]string{"run", "graceful.qi"}, args...)...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &stdout, &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Until it says it listens
		for i := 0; i < 200 && !strings.Contains(stdout.String(), "ready"); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		return cmd, &stdout, &stderr
	}
	finish := func(cmd *exec.Cmd) int {
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			t.Fatal("the program did not stop")
		}
		return cmd.ProcessState.ExitCode()
	}

	for _, sig := range []struct {
		signal os.Signal
		name   string
	}{{os.Interrupt, "interrupt"}, {syscall.SIGTERM, "terminate"}} {
		cmd, stdout, stderr := start()
		cmd.Process.Signal(sig.signal)
		if status := finish(cmd); status != 3 || stdout.String() != "ready\nasked to stop: "+sig.name+"\ncleaned up\n" {
			t.Errorf("%s: expected the program to stop its way, got status %d\n%s%s", sig.name, status, stdout, stderr)
		}
	}

	cmd, stdout, stderr := start("stubborn")
	cmd.Process.Signal(os.Interrupt)
	time.Sleep(200 * time.Millisecond)
	cmd.Process.Signal(os.Interrupt)
	if status := finish(cmd); status != 130 {
		t.Errorf("expected a second Ctrl-C to stop the program, got status %d\n%s%s", status, stdout, stderr)
	}
}

// lockedBuffer is what a running program writes, read while it writes.
type lockedBuffer struct {
	mu  sync.Mutex
	out strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.String()
}

// A date at a fixed offset stays at it across the end of summer time,
// where the same offset told by local() follows the machine's zone. The
// zone is the process's, so the program runs in a process of its own.
const summerTime = `import std.time;

fn main() -> Int {
    let noon = time.dateTime(2026, 10, 24, 12, 0, 0);
    println(noon.atOffset(120).plusDays(2).isoTime(), time.local(noon.timestamp()).plusDays(2).isoTime());
    println(noon.atOffset(120).plus(2.days()).isoTime(), noon.atOffset(120).plusMonths(1).zone());
    return 0;
}
`

func TestFixedOffsetIgnoresSummerTime(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Paris"); err != nil {
		t.Skip("no time zone database:", err)
	}
	dir := project(t, map[string]string{"summer.qi": summerTime})
	cmd := exec.Command(qoqi, "run", "summer.qi")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TZ=Europe/Paris")
	out, err := cmd.CombinedOutput()
	want := "2026-10-26T14:00:00+02:00 2026-10-26T14:00:00+01:00\n2026-10-26T14:00:00+02:00 +02:00\n"
	if err != nil || string(out) != want {
		t.Errorf("expected the offset kept and the zone followed, got %v\n%s", err, out)
	}
}
