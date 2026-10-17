package vm

import (
	"fmt"
	"unsafe"
)

// Calling back into the program, and surviving its failures.
//
// A native function normally only computes and returns. `attempt` of the
// standard library needs more: to run a function of the program and, if
// that fails, to carry on. That takes two things the VM did not have:
// calling a function from Go (callFunction), and putting the VM back in
// the state it was in before a call that failed (unwind).

// state is what has to be restored when a call is abandoned half way.
type state struct {
	frameCount int
	base       int
	size       int
	function   *FunctionObj
	chunk      *Chunk
	ip         int
	pinned     int
	handlers   int
	pending    int
}

func (vm *VM) saveState() state {
	return state{
		frameCount: vm.frameCount,
		base:       vm.base,
		size:       len(vm.regs),
		function:   vm.currentFunction,
		chunk:      vm.chunk,
		ip:         vm.ip,
		pinned:     len(vm.pinned),
		handlers:   len(vm.handlers),
		pending:    len(vm.pending),
	}
}

// unwind abandons every call made since a state was saved.
//
// The frames of those calls are dropped. A closure made in one of them may
// have outlived it (stored in a struct, say) while still pointing at one
// of its variables: each such variable is moved to the closure first, as
// happens when a function returns normally.
func (vm *VM) unwind(to state) {
	vm.closeUpvalues(to.base + to.size)

	for i := to.frameCount; i < vm.frameCount && i < len(vm.frames); i++ {
		vm.frames[i] = CallFrame{}
	}
	vm.frameCount = to.frameCount
	vm.frames = vm.frames[:to.frameCount]
	vm.base = to.base
	vm.regs = vm.window(to.base, to.size)
	vm.currentFunction = to.function
	vm.chunk = to.chunk
	vm.ip = to.ip
	vm.pinned = vm.pinned[:to.pinned]
	// The handlers of the abandoned calls have run by now; what a program
	// ending on the way left behind goes with them
	if len(vm.handlers) > to.handlers {
		vm.handlers = vm.handlers[:to.handlers]
	}
	if len(vm.pending) > to.pending {
		vm.pending = vm.pending[:to.pending]
	}
}

// callFunction calls a function value of the program with the given
// arguments and waits for its result. It is for native functions: the
// instruction loop is entered again, and left when the function returns.
//
// If the function fails, the error is returned with the VM still inside
// the failed call; the caller decides between passing the error on, which
// ends the program, and unwinding to a state saved before the call.
func (vm *VM) callFunction(fn Value, args []Value) (Value, error) {
	kind, ok := heapType(fn)
	if !ok || (kind != ObjTypeFunction && kind != ObjTypeExternFunction) {
		return NewNull(), fmt.Errorf("expected a function, got %s", typeName(fn))
	}
	if kind == ObjTypeExternFunction {
		return (*ExternFunctionObj)(unsafe.Pointer(fn.AsHeapPtr())).Function()(vm, args)
	}

	funcObj := (*FunctionObj)(unsafe.Pointer(fn.AsHeapPtr()))
	proto := funcObj.Proto()
	if len(args) != proto.ArgCount {
		return NewNull(), fmt.Errorf("function %s expects %d arguments, got %d", proto.Name, proto.ArgCount, len(args))
	}

	// The same steps as the CALL instruction
	if err := vm.enter(funcObj, args); err != nil {
		return NewNull(), err
	}
	result, err := vm.run(vm.frameCount)
	if err != nil {
		return NewNull(), err
	}

	// The same steps as RET, without storing the result in a register
	vm.leave()
	return result, nil
}

// protect runs a function of the program. If the function fails, by a
// panic or by any other runtime error, the program is not stopped: the VM
// is put back where it was and the failure is kept for the caller.
// It reports whether the function completed. A program that asks to end
// (std.os.exit) is not a failure to recover from: that request is handed
// back as the error, to be passed on.
func (vm *VM) protect(fn Value) (bool, error) {
	saved := vm.saveState()
	vm.outcome, vm.failure = NewNull(), nil

	result, err := vm.callFunction(fn, nil)
	if err == nil {
		vm.outcome = result
		return true, nil
	}
	if leaving(err) {
		return false, err
	}

	failure, ok := err.(*RuntimeError)
	if !ok {
		// Raised before the function could start: not a function, the
		// wrong number of arguments
		failure = &RuntimeError{Kind: kindOf(err.Error()), Message: err.Error(), Stack: vm.callStack(), instruction: vm.ip}
	}
	failure.atTheCall()
	vm.unwind(saved)
	vm.failure = failure
	return false, nil
}

func init() {
	registerNatives("std.result",
		// The calls in progress, innermost first, four entries per call:
		// function, file, line and column
		native{"callers", 0, func(vm *VM, args []Value) (Value, error) {
			var parts []string
			for _, frame := range vm.callStack() {
				parts = append(parts, frame.Function, frame.File, fmt.Sprint(frame.Line), fmt.Sprint(frame.Column))
			}
			return vm.newStringArray(parts)
		}},
		// Runs a function that takes no arguments; false if it failed
		native{"protect", 1, func(vm *VM, args []Value) (Value, error) {
			completed, leaving := vm.protect(args[0])
			if leaving != nil {
				return NewNull(), leaving
			}
			return NewBool(completed), nil
		}},
		// What the function run by protect returned
		native{"outcome", 0, func(vm *VM, args []Value) (Value, error) {
			result := vm.outcome
			vm.outcome = NewNull()
			return result, nil
		}},
		// How the function run by protect failed: its kind, its message,
		// then the call stack, innermost call first, with four entries per
		// call: function, file, line and column
		native{"failure", 0, func(vm *VM, args []Value) (Value, error) {
			failure := vm.failure
			vm.failure = nil
			if failure == nil {
				return vm.newStringArray(nil)
			}
			parts := []string{string(failure.Kind), failure.Message}
			for _, frame := range failure.Stack {
				parts = append(parts, frame.Function, frame.File, fmt.Sprint(frame.Line), fmt.Sprint(frame.Column))
			}
			return vm.newStringArray(parts)
		}},
	)
}
