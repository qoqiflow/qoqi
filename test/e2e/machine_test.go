package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// watched is what a program writes, readable while it is still running.
type watched struct {
	mu   sync.Mutex
	text bytes.Buffer
}

func (w *watched) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.Write(p)
}

func (w *watched) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.String()
}

// TestInterruptedFromTheKeyboard stops programs that would not end, the way
// Ctrl-C does: qoqi says where the program was and ends with the status a
// shell expects of an interrupted command.
func TestInterruptedFromTheKeyboard(t *testing.T) {
	dir := project(t, map[string]string{
		"spin.qi": `
			fn collatz(start: Int) -> Int {
				let n = start;
				let steps = 0;
				while n != 1 || steps >= 0 {
					if n % 2 == 0 { n = n / 2; } else { n = 3 * n + 1; }
					steps += 1;
				}
				return steps;
			}
			fn main() -> Int {
				println("started");
				return collatz(27);
			}`,
		"sleep.qi": `
			import std.time;
			fn main() -> Int {
				println("started");
				wait time.after(600000.milliseconds());
				println("woke");
				return 0;
			}`,
	})

	for _, c := range []struct{ file, where string }{
		{"spin.qi", "in function 'collatz'"},
		{"sleep.qi", "in function 'main'"},
	} {
		t.Run(c.file, func(t *testing.T) {
			var stdout, stderr watched
			cmd := exec.Command(qoqi, "run", filepath.Join(dir, c.file))
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()

			// Once it has started running, interrupt it
			deadline := time.Now().Add(runTimeout)
			for !strings.Contains(stdout.String(), "started") && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			cmd.Process.Signal(syscall.SIGINT)

			select {
			case <-done:
			case <-time.After(runTimeout):
				cmd.Process.Kill()
				t.Fatal("the program went on after the interruption")
			}
			if status := cmd.ProcessState.ExitCode(); status != 130 {
				t.Errorf("expected exit 130, got %d", status)
			}
			if strings.Contains(stdout.String(), "woke") {
				t.Error("the program went on to its next line")
			}
			for _, want := range []string{"R0011", "interrupted", c.where, c.file} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("expected %q in:\n%s", want, stderr.String())
				}
			}
		})
	}
}

// TestALimitedHeapIsEnough runs a program that makes far more than its
// limit over time, and keeps little: memory that is given back is counted
// as given back, so the limit is only about what is alive at once.
func TestALimitedHeapIsEnough(t *testing.T) {
	dir := project(t, map[string]string{
		"churn.qi": `
			import std.strings;

			struct Reading { sensor: String; value: Int; }

			fn average(readings: [Reading]) -> Int {
				let sum = 0;
				for reading in readings { sum += reading.value; }
				return sum / readings.len();
			}

			fn main() -> Int {
				let best = 0;
				for batch in 0..3000 {
					let readings: [Reading] = [];
					for i in 0..100 {
						readings.push(Reading { sensor: "sensor-" + toString(i), value: (batch * 31 + i * 17) % 1000 });
					}
					let scale = batch % 3 + 1;
					let scaled = readings.map(r -> Reading { sensor: r.sensor.upper(), value: r.value * scale });
					let mean = average(scaled);
					if mean > best { best = mean; }
				}
				println(best);
				return 0;
			}`,
	})
	file := filepath.Join(dir, "churn.qi")

	res := runQoqi(t, "run", "--heap-mb=2", file)
	if res.exit != 0 || strings.TrimSpace(res.stdout) != "1702" {
		t.Errorf("with 2 MB: exit %d, %q\n%s", res.exit, res.stdout, res.stderr)
	}
	// The same result with room to spare
	if spare := runQoqi(t, "run", file); spare.stdout != res.stdout {
		t.Errorf("another result with the default heap: %q", spare.stdout)
	}
}
