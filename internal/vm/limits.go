package vm

import (
	"context"
	"fmt"
)

// What a host lets a program do.
//
// A program run for its author needs no more than the limits on memory
// and depth. One run for someone else — a submission, a plugin, a test
// case made by a machine — must also end, and must not reach what the host
// did not mean to hand over. These are set before Exec, and hold for every
// program the VM runs afterwards.

// SetStepLimit bounds how long a program may run: it ends with an error of
// kind KindBudget once it has taken more than limit steps, a step being a
// call of a function of the program or a jump backwards (one turn of a
// loop). 0 lifts the bound.
//
// Steps are counted rather than time, so that the same program ends at the
// same place every time, on any machine. Nothing in the program catches
// the error.
func (vm *VM) SetStepLimit(limit uint64) {
	vm.stepLimit = limit
}

// Steps is how many steps the program run last has taken.
func (vm *VM) Steps() uint64 {
	return vm.steps
}

// outOfSteps counts a step and reports whether it was one too many.
func (vm *VM) outOfSteps() bool {
	if vm.stepLimit == 0 {
		return false
	}
	vm.steps++
	return vm.steps > vm.stepLimit
}

// stopped is the error for a program the instruction loop will not carry
// on with: told to stop from outside, or out of steps.
func (vm *VM) stopped() error {
	if vm.interrupted.Load() {
		return vm.interruption()
	}
	message := fmt.Sprintf("the program ran for more than the %d steps it may", vm.stepLimit)
	return &fatalError{vm.runtimeErrorOf(KindBudget, message)}
}

// PermitNatives says which native functions a program may use: those for
// which permitted returns true, by their name ("std.fs.remove"). A program
// that reaches for another fails there, with an error it can catch like
// any refusal of the library. nil permits all, which is the default.
//
// The builtins that only compute (print, toString, casts) are natives too,
// and are asked about like the others.
func (vm *VM) PermitNatives(permitted func(name string) bool) {
	vm.permitted = permitted
	// Function objects made under the old rule are not handed out again
	vm.externObjs = make(map[string]Value)
}

// ExecContext is Exec for a program that is to stop when a context is
// done: at its deadline, or when it is cancelled.
func (vm *VM) ExecContext(ctx context.Context, chunk *Chunk) (Value, error) {
	finished := make(chan struct{})
	watching := make(chan bool)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt()
			watching <- true
		case <-finished:
			watching <- false
		}
	}()
	result, err := vm.Exec(chunk)
	close(finished)
	if interrupted := <-watching; interrupted && !IsInterrupted(err) {
		// The context ended as the program did: there is nothing left to
		// stop, and the next program is not the one that was meant
		vm.clearInterrupt()
	}
	return result, err
}
