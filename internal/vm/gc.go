package vm

import "unsafe"

// CollectGarbage runs a mark-and-sweep garbage collection cycle.
func (vm *VM) CollectGarbage() {
	// Mark phase: mark all reachable objects
	vm.markRoots()
	vm.markReachable()

	// Sweep phase: free unmarked objects
	vm.heap.sweep()
}

// markRoots marks the objects the program holds directly: in the registers
// of its calls in progress, and in what the VM keeps for it. What those
// objects hold in turn is left for markReachable.
func (vm *VM) markRoots() {
	// Mark current function
	if vm.currentFunction != nil {
		vm.markObject(&vm.currentFunction.header)
	}

	// The registers of the task whose turn it is: those of every call in
	// progress, which are the stack up to the end of the innermost call
	vm.markStack(vm.stk, vm.base+len(vm.regs))
	clearAbove(vm.stk, vm.base+len(vm.regs))
	for i := 0; i < len(vm.regs); i++ {
		vm.markValue(vm.regs[i])
	}

	// The functions whose calls are in progress
	for i := 0; i < vm.frameCount; i++ {
		if fn := vm.frames[i].function; fn != nil {
			vm.markObject(&fn.header)
		}
	}

	// What the tasks whose turn it is not are in the middle of
	vm.markTasks()

	// The variables a REPL keeps between two programs
	vm.markSession()

	// What a protected call returned, until the program takes it
	vm.markValue(vm.outcome)

	// Values a native function is still working with
	for _, v := range vm.pinned {
		vm.markValue(v)
	}

	// Function objects kept for reuse (see functionObject, NEWEXFUNC), and
	// the texts of one character
	for _, cache := range vm.caches {
		for _, cached := range cache.funcs {
			vm.markValue(cached)
		}
		for _, cached := range cache.constants {
			vm.markValue(cached)
		}
		for _, cached := range cache.once {
			vm.markValue(cached)
		}
		for _, cached := range cache.strings {
			vm.markValue(cached)
		}
	}
	for _, cached := range vm.externObjs {
		vm.markValue(cached)
	}
	for _, cached := range vm.charStrings {
		vm.markValue(cached)
	}
}

// markStack marks what a stack holds below top, and the variables on it
// that closures share.
func (vm *VM) markStack(stack *regStack, top int) {
	if stack == nil {
		return
	}
	if top > len(stack.vals) {
		top = len(stack.vals)
	}
	for _, v := range stack.vals[:top] {
		vm.markValue(v)
	}
	for _, upval := range stack.open {
		vm.markObject(&upval.header)
	}
}

// clearAbove makes null what calls that returned left on a stack above
// its top, which is not marked and may be freed: a call that takes those
// registers again finds nothing freed in them, and the collector does not
// follow it. A call does not clear its registers (see enter): every value
// below top was marked by each collection since it was written there, or
// made null by one.
//
// top must be where the calls of the stack end now: that of the task
// whose turn it is, or that of another task where it stopped.
func clearAbove(stack *regStack, top int) {
	if stack == nil {
		return
	}
	if used := min(stack.used, len(stack.vals)); used > top {
		left := stack.vals[top:used]
		for i := range left {
			left[i] = valueNull
		}
	}
	stack.used = top
}

// markValue marks the object a value points at, if it does.
func (vm *VM) markValue(v Value) {
	if h := header(v); h != nil {
		vm.markObject(h)
	}
}

// markObject marks an object. If it can hold others it is put aside to
// be looked into by markReachable, rather than looked into here: a list of
// a million nodes, each holding the next, would otherwise be followed a
// million calls deep.
func (vm *VM) markObject(h *ObjHeader) {
	if h.Marked {
		return
	}
	h.Marked = true
	switch h.Type {
	case ObjTypeString:
		// A text holds the texts it was joined of until it is written out
		if (*StringObj)(unsafe.Pointer(h)).rope() != nil {
			vm.gray = append(vm.gray, h)
		}
	case ObjTypeExternFunction, ObjTypeIndex:
		// These hold no other object
	default:
		vm.gray = append(vm.gray, h)
	}
}

// markReachable marks everything that can be reached from the objects
// marked so far.
func (vm *VM) markReachable() {
	for len(vm.gray) > 0 {
		h := vm.gray[len(vm.gray)-1]
		vm.gray = vm.gray[:len(vm.gray)-1]

		switch h.Type {
		case ObjTypeString:
			if rope := (*StringObj)(unsafe.Pointer(h)).rope(); rope != nil {
				vm.markObject(&rope.left.header)
				vm.markObject(&rope.right.header)
			}
		case ObjTypeArray:
			for _, v := range (*ArrayObj)(unsafe.Pointer(h)).elements {
				vm.markValue(v)
			}
		case ObjTypeObject:
			for _, v := range (*ObjectObj)(unsafe.Pointer(h)).slots {
				vm.markValue(v)
			}
		case ObjTypeFunction:
			// The variables a closure captured
			for _, upval := range (*FunctionObj)(unsafe.Pointer(h)).upvalues {
				if upval != nil {
					vm.markObject(&upval.header)
				}
			}
		case ObjTypeUpvalue:
			// An open one names a register, which its stack keeps alive
			if upval := (*UpvalueObj)(unsafe.Pointer(h)); upval.stack == nil {
				vm.markValue(upval.closed)
			}
		}
	}
	if cap(vm.gray) > maxKeptGray {
		vm.gray = nil
	}
}

// maxKeptGray is how large a list of objects to look into is kept from one
// collection to the next, rather than made again.
const maxKeptGray = 1 << 16

// sweep walks the object list and frees unmarked objects.
func (h *Heap) sweep() {
	var prev *ObjHeader
	current := h.objects

	for current != nil {
		if current.Marked {
			// Object is reachable, unmark for next GC cycle
			current.Marked = false
			prev = current
			current = current.Next
		} else {
			// Object is unreachable, free it
			unreached := current
			current = current.Next

			if prev != nil {
				prev.Next = current
			} else {
				h.objects = current
			}

			h.freeObject(unreached)
		}
	}

	// Collect again when twice as much is in use as survived this time, and
	// never before minGC. A threshold tied to what survives keeps the total
	// work proportional to what the program allocates; one that stays small
	// while the live data grows makes every collection re-walk that data.
	h.collections++
	h.nextGC = h.bytesAllocated * 2
	if h.nextGC < h.minGC {
		h.nextGC = h.minGC
	}
	if h.nextGC > h.maxBytes {
		h.nextGC = h.maxBytes
	}
}

// freeObject takes an object off the count of what is in use. The memory
// itself goes back when Go's collector finds nothing pointing at it, which
// taking it off the list of objects has just made true.
func (h *Heap) freeObject(header *ObjHeader) {
	size := sizeOf(header)
	if h.bytesAllocated >= size {
		h.bytesAllocated -= size
	} else {
		h.bytesAllocated = 0
	}
	header.Next = nil
	if h.onFree != nil {
		h.onFree(header)
	}
}
