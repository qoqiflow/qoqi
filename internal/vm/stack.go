package vm

import "fmt"

// regStack holds the registers of every call a task has in progress, one
// call after the other: a call takes the registers it needs from the top
// and gives them back when it returns, so that calling a function
// allocates nothing.
//
// The stack grows by moving to a larger array. Nothing may therefore keep
// the address of a register: the instruction loop takes its window on the
// stack again after anything that can make a call, and an upvalue names
// its register by its place (see UpvalueObj).
type regStack struct {
	vals []Value

	// used is the most registers the calls have taken since the collector
	// last looked: those above the top may still hold what a call that
	// returned left there (see markStack)
	used int

	// The upvalues that name a register of this stack, from the lowest
	// register to the highest. Calls return from the top, so the ones to
	// close are always at the end.
	open []*UpvalueObj
}

func newRegStack(size int) *regStack {
	return &regStack{vals: make([]Value, size)}
}

// grow makes room for at least top registers.
func (s *regStack) grow(top int) {
	size := 2 * len(s.vals)
	if size < top {
		size = top
	}
	if size < 64 {
		size = 64
	}
	vals := make([]Value, size)
	copy(vals, s.vals)
	s.vals = vals
}

// stackSlotsPerCall is how many registers the stack may hold for each call
// the depth limit allows. Functions with more than that exist, but not ten
// thousand of them nested: the limit is on the whole.
const stackSlotsPerCall = 256

// enter starts a call of a function of the program: it puts aside where
// the caller is and gives the function its registers, the arguments first.
// The caller has checked the number of arguments.
func (vm *VM) enter(fn *FunctionObj, args []Value) error {
	if vm.frameCount >= vm.maxCallDepth {
		return vm.runtimeError(fmt.Sprintf("stack overflow: max call depth %d exceeded", vm.maxCallDepth))
	}
	proto := fn.proto
	stack := vm.stk
	base := vm.base + len(vm.regs)
	top := base + proto.RegisterCount
	if top > len(stack.vals) {
		if top > vm.maxCallDepth*stackSlotsPerCall {
			return vm.runtimeError(fmt.Sprintf("stack overflow: the calls in progress need more than %d registers", vm.maxCallDepth*stackSlotsPerCall))
		}
		// The arguments are read from the old array below, which stays
		// as it was
		stack.grow(top)
	}

	vm.frames = append(vm.frames[:vm.frameCount], CallFrame{
		function: vm.currentFunction,
		ip:       vm.ip,
		base:     vm.base,
		size:     len(vm.regs),
	})
	vm.frameCount++

	// The registers besides the arguments keep what an earlier call left
	// there: the code of a function writes a register before it reads it.
	// What is left is a value the collector keeps, or null (see markStack)
	regs := stack.vals[base:top:top]
	copy(regs, args)
	if top > stack.used {
		stack.used = top
	}

	vm.currentFunction = fn
	vm.chunk = fn.chunk
	vm.base = base
	vm.regs = regs
	vm.ip = proto.CodeStart
	return nil
}

// leave ends the innermost call: the caller is where it was, at the
// instruction that made the call.
func (vm *VM) leave() {
	vm.frameCount--
	frame := &vm.frames[vm.frameCount]
	vm.currentFunction = frame.function
	vm.chunk = frame.function.chunk
	vm.ip = frame.ip
	vm.base = frame.base
	vm.regs = vm.window(frame.base, frame.size)
	frame.function = nil
}

// window is the registers of a call: size of them from base.
func (vm *VM) window(base, size int) []Value {
	return vm.stk.vals[base : base+size : base+size]
}

// findOrCreateUpvalue finds the open upvalue for a register of the current
// stack, or makes one. Closures that capture the same variable share the
// same upvalue this way.
func (vm *VM) findOrCreateUpvalue(slot int) (*UpvalueObj, error) {
	stack := vm.stk
	at := len(stack.open)
	for at > 0 && stack.open[at-1].slot >= slot {
		at--
		if stack.open[at].slot == slot {
			return stack.open[at], nil
		}
	}

	upval, _, err := vm.heap.AllocUpvalue(stack, slot)
	if err != nil {
		return nil, err
	}
	stack.open = append(stack.open, nil)
	copy(stack.open[at+1:], stack.open[at:])
	stack.open[at] = upval
	return upval, nil
}

// closeUpvalueAt closes the open upvalue for one register, if there is one.
func (vm *VM) closeUpvalueAt(slot int) {
	open := vm.stk.open
	for i := len(open) - 1; i >= 0 && open[i].slot >= slot; i-- {
		if open[i].slot == slot {
			open[i].Close()
			vm.stk.open = append(open[:i], open[i+1:]...)
			return
		}
	}
}

// closeUpvalues closes the upvalues of every register from a place of the
// current stack upwards. A function that returns does this for its own
// registers: the variables its closures captured move into the closures.
func (vm *VM) closeUpvalues(from int) {
	open := vm.stk.open
	n := len(open)
	for n > 0 && open[n-1].slot >= from {
		n--
		open[n].Close()
		open[n] = nil
	}
	vm.stk.open = open[:n]
}
