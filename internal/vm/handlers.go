package vm

// Cleanup: what `defer` needs from the VM.
//
// A failure normally ends every call in progress at once: run returns the
// error, and nothing of the program runs any more unless `attempt` was
// waiting for it (see protect.go). A function that deferred something
// must get to run it first. So before the code a defer covers, the
// function leaves the address of a handler (TRY32); when the code is done
// it takes the address back (ENDTRY) and runs the cleanup itself.
//
// If something fails in between, in that call or in any it made, the
// calls made since are dropped and the function goes on at the handler,
// with the failure put aside. The handler runs the cleanup and passes the
// failure on (RETHROW): to the next handler, of the same call or of one
// further out, and when there is none left, out of run as before.
//
// What ends the program whatever it does is not a failure, and no handler
// runs for it: a request to end (std.os.exit), an interruption from
// outside, a mistake of the VM itself.

// handler is where a call goes on after a failure.
type handler struct {
	depth  int // The number of frames when it was left: which call it is of
	ip     int
	pinned int // What natives had pinned then
}

// pendingFailure is a failure a handler is running for.
type pendingFailure struct {
	failure *RuntimeError
	// How many handlers were left when its handler started. Fewer later
	// means that handler failed itself, and this failure with it.
	handlers int
}

// handle gives a failure to the innermost handler of the calls that
// belong to this run (those at base or deeper). It reports whether there
// was one: the VM is then at the handler, ready to go on.
func (vm *VM) handle(base int, err error) bool {
	failure, ok := err.(*RuntimeError)
	if !ok || len(vm.handlers) == 0 {
		// The program ending, or nothing to run
		return false
	}
	h := vm.handlers[len(vm.handlers)-1]
	if h.depth < base || h.depth > vm.frameCount {
		// Of a call outside this run: for whoever started it
		return false
	}
	vm.handlers = vm.handlers[:len(vm.handlers)-1]

	// Drop the calls made since, as unwind does: a closure that outlives
	// one of them takes the variables it captured with it. What the call
	// that goes on was doing is in the frame of the first call dropped.
	if vm.frameCount > h.depth {
		frame := vm.frames[h.depth]
		vm.closeUpvalues(frame.base + frame.size)
		for i := h.depth; i < vm.frameCount; i++ {
			vm.frames[i] = CallFrame{}
		}
		vm.frameCount = h.depth
		vm.frames = vm.frames[:h.depth]
		vm.currentFunction = frame.function
		vm.chunk = frame.function.chunk
		vm.base = frame.base
		vm.regs = vm.window(frame.base, frame.size)
	}
	if len(vm.pinned) > h.pinned {
		vm.pinned = vm.pinned[:h.pinned]
	}
	vm.ip = h.ip

	// A failure whose handler was above this one did not get passed on:
	// its handler is what failed just now. This failure replaces it.
	kept := len(vm.pending)
	for kept > 0 && vm.pending[kept-1].handlers > len(vm.handlers) {
		kept--
	}
	vm.pending = append(vm.pending[:kept], pendingFailure{failure: failure, handlers: len(vm.handlers)})
	return true
}
