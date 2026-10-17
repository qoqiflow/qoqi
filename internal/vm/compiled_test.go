package vm

import (
	"strings"
	"testing"
)

// twoFunctions is a chunk whose main calls f(a, b): enough to be in a call
// with arguments to pass on.
func twoFunctions(fRegisters int) *Chunk {
	chunk := NewChunk()
	chunk.Code = []Instr{
		NewInstr(OpLOADI, 0, 0, 0), NewInstr(OpRET, 0, 0, 0), // main
		NewInstr(OpRET, 0, 0, 0), // f
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 2, RegisterCount: 4},
		{Name: "f", CodeStart: 2, CodeSize: 1, RegisterCount: fRegisters, ArgCount: 2},
	}
	return chunk
}

// machineIn is a VM in the middle of main, with its registers set.
func machineIn(t *testing.T, chunk *Chunk) (*VM, *FunctionObj) {
	t.Helper()
	machine := NewVM()
	main, _, err := machine.heap.AllocFunction(chunk, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := machine.heap.AllocFunction(chunk, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	machine.chunk, machine.currentFunction = chunk, main
	machine.stk = newRegStack(64)
	machine.base, machine.regs = 0, machine.window(0, 4)
	for i := range machine.regs {
		machine.regs[i] = NewInt(int64(10 + i))
	}
	machine.ip = 1
	return machine, f
}

// A call entered and left the lighter way leaves the machine as one
// entered and left by enter and leave.
func TestCompiledCallsEnterAndLeaveAsCallsDo(t *testing.T) {
	type state struct {
		frameCount, base, ip int
		regs                 []Value
		function             *FunctionObj
	}
	look := func(machine *VM) state {
		return state{machine.frameCount, machine.base, machine.ip, append([]Value(nil), machine.regs...), machine.currentFunction}
	}
	same := func(a, b state) bool {
		// Two machines have objects of their own: the same function is the
		// same prototype
		if a.frameCount != b.frameCount || a.base != b.base || a.function.proto != b.function.proto || len(a.regs) != len(b.regs) {
			return false
		}
		for i := range a.regs {
			if a.regs[i] != b.regs[i] {
				return false
			}
		}
		return true
	}

	chunk := twoFunctions(5)
	slow, f1 := machineIn(t, chunk)
	fast, f2 := machineIn(t, chunk)
	before := look(slow)

	if err := slow.enter(f1, slow.regs[2:4:4]); err != nil {
		t.Fatal(err)
	}
	if err := fast.enterCompiledCall(f2, 2, 4); err != nil {
		t.Fatal(err)
	}
	inside := look(slow)
	if got := look(fast); !same(got, inside) || got.function != f2 {
		t.Errorf("inside the call:\n  enter:             %+v\n  enterCompiledCall: %+v", inside, got)
	}
	if fast.regs[0] != NewInt(12) || fast.regs[1] != NewInt(13) {
		t.Errorf("expected the arguments in r0 and r1, got %v", fast.regs)
	}
	if fast.frames[0].ip != 1 || fast.frames[0].function.proto != slow.frames[0].function.proto || fast.stk.used != slow.stk.used {
		t.Errorf("the frame of the caller: %+v, %+v", fast.frames[0], slow.frames[0])
	}

	slow.leave()
	fast.leaveCompiledCall()
	if got := look(fast); !same(got, look(slow)) || !same(got, before) || got.ip != 1 {
		t.Errorf("after the call: %+v, expected %+v", got, before)
	}
}

// What enter does when a call goes too deep or the stack has to grow, the
// lighter way does too: it is enter that does it.
func TestCompiledCallsGoTooDeepAndGrowTheStackAsCallsDo(t *testing.T) {
	machine, f := machineIn(t, twoFunctions(5))
	machine.SetMaxCallDepth(1)
	if err := machine.enterCompiledCall(f, 2, 4); err != nil {
		t.Fatalf("the first call: %v", err)
	}
	err := machine.enterCompiledCall(f, 0, 2)
	if err == nil || !strings.Contains(err.Error(), "stack overflow: max call depth 1 exceeded") {
		t.Errorf("expected the depth to be refused, got %v", err)
	}

	// Registers beyond the stack's end: it grows, the arguments come along
	machine, f = machineIn(t, twoFunctions(200))
	if err := machine.enterCompiledCall(f, 2, 4); err != nil {
		t.Fatal(err)
	}
	if len(machine.stk.vals) < 204 || len(machine.regs) != 200 || machine.regs[0] != NewInt(12) || machine.regs[1] != NewInt(13) {
		t.Errorf("expected a grown stack with the arguments, got %d registers of %d: %v", len(machine.regs), len(machine.stk.vals), machine.regs[:2])
	}
	machine.leaveCompiledCall()
	if machine.frameCount != 0 || len(machine.regs) != 4 || machine.regs[2] != NewInt(12) {
		t.Errorf("after the call: %d frames, %v", machine.frameCount, machine.regs)
	}
}

// A failure on its way out of frameless calls becomes the error the calls
// would have made with frames: the frameless calls first, innermost first,
// then the calls with frames, from where the last of them made its call.
func TestFramelessFailuresAreWhatFramesWouldHaveMade(t *testing.T) {
	chunk := twoFunctions(5)
	machine, _ := machineIn(t, chunk)
	machine.ip = 1

	failure := machine.framelessFailed(1, 2, "integer overflow")
	failure = machine.framelessCalled(failure, 1, 2)
	err := machine.framelessArrived(failure)
	got, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("expected a RuntimeError, got %T", err)
	}
	if got.Kind != kindOf("integer overflow") || got.Message != "integer overflow" || got.instruction != 2 {
		t.Errorf("got %v %q at %d", got.Kind, got.Message, got.instruction)
	}
	names := []string{}
	for _, frame := range got.Stack {
		names = append(names, frame.Function)
	}
	if strings.Join(names, " ") != "f f main" {
		t.Errorf("expected the frameless calls then main, got %v", names)
	}

	// What stops a program is fatal, frames or none
	machine.Interrupt()
	if fatal, ok := machine.framelessArrived(machine.framelessStopped(1, 2)).(*fatalError); !ok || !IsInterrupted(fatal.err) {
		t.Errorf("expected an interruption that ends the program, got %v", fatal)
	}
	machine.interrupted.Store(false)
	machine.SetStepLimit(7)
	stopped := machine.framelessArrived(machine.framelessStopped(1, 2))
	if fatal, ok := stopped.(*fatalError); !ok || !strings.Contains(fatal.Error(), "more than the 7 steps") {
		t.Errorf("expected the budget to end the program, got %v", stopped)
	}

	// Too deep says what enter says
	machine.SetMaxCallDepth(3)
	if err := machine.framelessArrived(machine.framelessTooDeep(1, 2)); !strings.Contains(err.Error(), "stack overflow: max call depth 3 exceeded") {
		t.Errorf("got %v", err)
	}
	if err := machine.tooDeep(); !strings.Contains(err.Error(), "stack overflow: max call depth 3 exceeded") {
		t.Errorf("got %v", err)
	}

	// An error that did not come out of frameless calls is passed on as it is
	plain := machine.runtimeError("other")
	if machine.framelessArrived(plain) != plain {
		t.Error("expected another error to be left as it is")
	}
}
