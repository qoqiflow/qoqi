package vm

// Using a VM from several threads.
//
// A VM runs one program at a time, and a program runs one task at a time
// (see task.go): the heap, its collector and the registers are only ever
// touched by whoever has the turn, which is what lets them go without
// locks. What may be done from any thread, at any time:
//
//   - Exec: a second program waits for the first to end;
//   - Interrupt: stops the program that is running;
//   - anything on another VM. Each VM has its own heap and shares nothing
//     that changes with the others, so several run side by side, one per
//     thread, each on a processor of its own.
//
// Everything else (SetOutput, RegisterExtern, the limits) configures a VM
// and is to be done before Exec, by the thread that will call it.

// Interrupt stops the program the VM is running, from any thread: the
// program ends with an error of kind KindInterrupted that says where it
// was. Nothing in the program catches it, and no cleanup of its runs.
//
// A program that is computing stops within a few instructions; one that
// sleeps is woken. One that waits for the world otherwise (a line of
// input, an answer from the network) stops once that wait is over.
//
// Without a program running, the next one is stopped as it starts.
func (vm *VM) Interrupt() {
	vm.interruptMu.Lock()
	defer vm.interruptMu.Unlock()
	if !vm.interrupted.Swap(true) {
		close(vm.interrupt)
	}
}

// interruptSignal is closed when Interrupt is called: what a task that
// sleeps waits for besides the clock.
func (vm *VM) interruptSignal() <-chan struct{} {
	vm.interruptMu.Lock()
	defer vm.interruptMu.Unlock()
	return vm.interrupt
}

// clearInterrupt makes the VM ready for a next program, once the one that
// was interrupted has ended.
func (vm *VM) clearInterrupt() {
	vm.interruptMu.Lock()
	defer vm.interruptMu.Unlock()
	if vm.interrupted.Swap(false) {
		vm.interrupt = make(chan struct{})
	}
}

// interruption is the error a program ends with once Interrupt was called.
func (vm *VM) interruption() error {
	return &fatalError{vm.runtimeErrorOf(KindInterrupted, "interrupted")}
}

// IsInterrupted reports whether a program ended because it was told to
// from outside (VM.Interrupt).
func IsInterrupted(err error) bool {
	failure, ok := err.(*RuntimeError)
	return ok && failure.Kind == KindInterrupted
}
