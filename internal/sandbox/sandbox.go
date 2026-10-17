// Package sandbox runs a program that is not trusted: one written by
// someone else, or made by a machine to see what breaks.
//
// Such a program gets a small heap, a bound on how deep it calls and on
// how long it runs, and only the native functions that compute: it cannot
// touch files, the network, other programs, the clock or the process. It
// ends, one way or another, and what it wrote is handed back.
package sandbox

import (
	"context"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Limits bounds a run. The zero value of a field means its default.
type Limits struct {
	HeapBytes uint64        // What the program may hold (default 16 MB)
	Steps     uint64        // Calls and turns of loops it may make (default 2 million)
	Depth     int           // How deep its calls may nest (default 500)
	Output    int           // Bytes of output kept; the rest is dropped (default 64 KB)
	Time      time.Duration // The longest it may take, whatever it does (default 10 s)
}

func (l Limits) withDefaults() Limits {
	if l.HeapBytes == 0 {
		l.HeapBytes = 16 << 20
	}
	if l.Steps == 0 {
		l.Steps = 2_000_000
	}
	if l.Depth == 0 {
		l.Depth = 500
	}
	if l.Output == 0 {
		l.Output = 64 << 10
	}
	if l.Time == 0 {
		l.Time = 10 * time.Second
	}
	return l
}

// pure lists the modules of the standard library whose native functions
// only compute. The others reach outside the program: std.fs, std.io and
// std.path (files), std.http (the network), std.os (the process, other
// programs), std.time (the clock; sleeping), std.compiler (files again).
var pure = []string{
	"std.arrays.", "std.csv.", "std.encoding.", "std.format.", "std.hash.",
	"std.json.", "std.math.", "std.random.", "std.regex.",
	"std.result.", "std.strings.", "std.task.", "std.url.",
}

// Permitted reports whether a sandboxed program may use a native function:
// the builtins (print, toString, the casts) and those of the pure modules.
func Permitted(name string) bool {
	if !strings.HasPrefix(name, "std.") {
		return true
	}
	for _, prefix := range pure {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// output keeps the start of what a program writes.
type output struct {
	text strings.Builder
	room int
}

func (o *output) Write(p []byte) (int, error) {
	if keep := len(p); o.room > 0 {
		if keep > o.room {
			keep = o.room
		}
		o.text.Write(p[:keep])
		o.room -= keep
	}
	return len(p), nil
}

// Run executes a chunk within limits, and returns what it wrote and how
// it ended. The chunk is verified like any other (see vm.Verify).
func Run(chunk *vm.Chunk, limits Limits) (string, error) {
	limits = limits.withDefaults()

	out := &output{room: limits.Output}
	machine := vm.NewVMWithHeapSize(limits.HeapBytes)
	machine.SetOutput(out)
	machine.SetInput(strings.NewReader(""))
	machine.SetMaxCallDepth(limits.Depth)
	machine.SetStepLimit(limits.Steps)
	machine.PermitNatives(Permitted)

	// Steps bound what the program computes; the clock is for what they
	// do not count, a native function that takes long
	ctx, cancel := context.WithTimeout(context.Background(), limits.Time)
	defer cancel()
	_, err := machine.ExecContext(ctx, chunk)
	return out.text.String(), err
}

// Broke reports whether a run ended in a way that is a defect of Qoqi
// rather than of the program: the VM or a native function failed.
func Broke(err error) bool {
	failure, ok := err.(*vm.RuntimeError)
	return ok && failure.Kind == vm.KindInternal
}
