// Package runner runs a program the way qoqi run does: with the limits it
// was given, stopped by Ctrl-C where it is, ending the process with the
// status it ends with. A program made by qoqi native runs the same way,
// so that it does what qoqi run of its source does.
package runner

import (
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Limits bounds a run. Zero means the VM's default.
type Limits struct {
	HeapMB   int
	MaxDepth int
}

// InterruptedStatus is what a process stopped by Ctrl-C ends with: 128
// plus the number of the signal.
const InterruptedStatus = 130

// Machine makes the VM a program runs on: its limits, its arguments, the
// file it is (std.os.program). exit is how the process ends.
func Machine(limits Limits, args []string, program string, exit func(int)) *vm.VM {
	var machine *vm.VM
	if limits.HeapMB > 0 {
		machine = vm.NewVMWithHeapSize(uint64(limits.HeapMB) * 1024 * 1024)
	} else {
		machine = vm.NewVM()
	}
	stopOnInterrupt(machine, exit)
	GuardMemory(machine)
	tuneCollector(machine)
	if limits.MaxDepth > 0 {
		machine.SetMaxCallDepth(limits.MaxDepth)
	}
	if len(args) > 0 {
		machine.SetArgs(args)
	}
	machine.SetProgram(program)
	return machine
}

// Finish ends a run as qoqi run does: with the failure said on stderr and
// status 1, with the status the program asked for, or with the Int main
// returned.
func Finish(result vm.Value, err error, exit func(int)) {
	if err != nil {
		LeaveIfAsked(err, exit)
		fmt.Fprint(os.Stderr, strings.TrimRight(err.Error(), "\n")+"\n")
		exit(1)
		return
	}
	if result.IsInt() {
		exit(int(result.AsInt() & 0xFF))
	}
}

// LeaveIfAsked ends the process with the status the program asked for, if
// the run ended by a call of std.os.exit and not by a failure. A program
// stopped from the keyboard ends as the shell expects, having said where
// it was.
func LeaveIfAsked(err error, exit func(int)) {
	if status, asked := vm.ExitCode(err); asked {
		exit(status & 0xFF)
	}
	if vm.IsInterrupted(err) {
		fmt.Fprint(os.Stderr, strings.TrimRight(err.Error(), "\n")+"\n")
		exit(InterruptedStatus)
	}
}

// GuardMemory ends the process if it comes to hold far more than the
// program may. The VM refuses a program more values than its limit; this
// is for what that count does not cover, and would otherwise only stop
// when the machine has no memory left: several times the limit, and room
// for qoqi itself.
func GuardMemory(machine *vm.VM) {
	const own = 1 << 30
	memguard.Enforce(4*machine.GetHeap().MaxBytes()+own, "the program")
}

// tuneCollector makes Go's collector run less often than it would. Every
// value of a program is a Go object that the VM's own collector frees: Go
// collecting the same memory over again, every few megabytes the program
// allocates, is most of what a program that makes many small values
// spends besides its own work (8 to 14% less time with 400). It still
// collects sooner when the process nears what the program may hold. GOGC
// and GOMEMLIMIT, when set, are left as they are.
func tuneCollector(machine *vm.VM) {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(int64(2*machine.GetHeap().MaxBytes()) + 512<<20)
	}
}

// stopOnInterrupt makes Ctrl-C stop the program a VM runs rather than the
// process: the program ends with an error that says where it was, which
// is what one wants to know of a program that would not end. A program
// that does not get to it, because it waits for something that does not
// come, is ended the plain way a moment later; so is one interrupted twice.
// A program that listens for signals gets the first Ctrl-C as a signal
// instead, and the second stops it.
func stopOnInterrupt(machine *vm.VM, exit func(int)) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt)
	go func() {
		<-signals
		// A program that listens for signals (std.os.signals) is told,
		// and stops its own way; the next Ctrl-C stops it this one's
		if machine.Deliver("interrupt") {
			<-signals
		}
		machine.Interrupt()
		select {
		case <-signals:
		case <-time.After(2 * time.Second):
		}
		exit(InterruptedStatus)
	}()
}
