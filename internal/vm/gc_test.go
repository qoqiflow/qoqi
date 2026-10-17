package vm

import (
	"strings"
	"testing"
	"unsafe"
)

func TestGarbageCollection(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024) // 10KB heap

	// Allocate some objects and keep references
	str1, _, _ := vm.heap.AllocString("keep this")
	arr1, _, _ := vm.heap.AllocArray(5)

	// Store in registers (GC roots)
	vm.regs = make([]Value, 2)
	vm.regs[0] = NewHeapPtr(uintptr(unsafe.Pointer(str1)))
	vm.regs[1] = NewHeapPtr(uintptr(unsafe.Pointer(arr1)))

	// Allocate garbage (no references)
	for i := 0; i < 10; i++ {
		vm.heap.AllocString("garbage")
	}

	// Count objects before GC
	countBefore := countObjects(vm.heap)
	bytesBefore := vm.heap.BytesAllocated()

	// Run GC
	vm.CollectGarbage()

	// Count objects after GC
	countAfter := countObjects(vm.heap)
	bytesAfter := vm.heap.BytesAllocated()

	// Should have collected garbage
	if countAfter >= countBefore {
		t.Errorf("GC did not collect objects: before=%d, after=%d", countBefore, countAfter)
	}

	if bytesAfter >= bytesBefore {
		t.Errorf("GC did not free memory: before=%d, after=%d", bytesBefore, bytesAfter)
	}

	// Should keep exactly 2 objects (str1 and arr1)
	if countAfter != 2 {
		t.Errorf("Expected 2 objects after GC, got %d", countAfter)
	}
}

func TestGCMarksReachableObjects(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024)

	// Create object graph: array -> object -> string
	str, _, _ := vm.heap.AllocString("nested")
	obj, _, _ := vm.heap.AllocObject()
	arr, _, _ := vm.heap.AllocArray(2)

	obj.Set("data", NewHeapPtr(uintptr(unsafe.Pointer(str))))
	arr.Append(NewHeapPtr(uintptr(unsafe.Pointer(obj))))

	// Only keep reference to array
	vm.regs = make([]Value, 1)
	vm.regs[0] = NewHeapPtr(uintptr(unsafe.Pointer(arr)))

	// Allocate garbage
	for i := 0; i < 5; i++ {
		vm.heap.AllocString("garbage")
		vm.heap.AllocObject()
	}

	countBefore := countObjects(vm.heap)

	// Run GC
	vm.CollectGarbage()

	countAfter := countObjects(vm.heap)

	// Should keep all 3 objects in the graph
	if countAfter != 3 {
		t.Errorf("Expected 3 reachable objects after GC, got %d", countAfter)
	}

	// Should have collected garbage
	if countAfter >= countBefore {
		t.Errorf("GC did not collect anything: before=%d, after=%d", countBefore, countAfter)
	}
}

func TestGCFromStack(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024)

	// Allocate object and put on stack
	str, _, _ := vm.heap.AllocString("on stack")
	vm.stk = newRegStack(4)
	vm.stk.vals[0] = NewHeapPtr(uintptr(unsafe.Pointer(str)))
	vm.regs = vm.window(0, 1)

	// Allocate garbage
	for i := 0; i < 5; i++ {
		vm.heap.AllocString("garbage")
	}

	// Run GC
	vm.CollectGarbage()

	// Should keep the object on the stack
	count := countObjects(vm.heap)
	if count != 1 {
		t.Errorf("Expected 1 object after GC (on stack), got %d", count)
	}
}

func TestGCClearsMarkedFlag(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024)

	// Allocate and keep reference
	str, _, _ := vm.heap.AllocString("test")
	vm.regs = make([]Value, 1)
	vm.regs[0] = NewHeapPtr(uintptr(unsafe.Pointer(str)))

	// Run GC - this will mark and then unmark during sweep
	vm.CollectGarbage()

	// After GC, marked flag should be cleared by sweep
	if str.Header().Marked {
		t.Error("Object marked flag should be cleared after GC sweep")
	}

	// Run another GC cycle
	vm.CollectGarbage()

	// Should still work and keep the object
	count := countObjects(vm.heap)
	if count != 1 {
		t.Errorf("Expected 1 object after second GC, got %d", count)
	}
}

func TestGCUpdateThreshold(t *testing.T) {
	// The next collection is due when twice as much is in use as survived
	// this one, and never before the heap's starting threshold.
	newVM := func() *VM { return NewVMWithHeapSize(1024 * 1024) }
	keep := func(vm *VM, bytes int) {
		str, _, _ := vm.heap.AllocString(strings.Repeat("x", bytes))
		vm.regs = append(vm.regs, NewHeapPtr(uintptr(unsafe.Pointer(str))))
	}

	// Little survives: the threshold stays where it started. Dropping it to
	// just above the live size would make the collector run constantly.
	vm := newVM()
	start := vm.heap.nextGC
	keep(vm, 100)
	vm.heap.AllocString(strings.Repeat("garbage", 1000))
	vm.CollectGarbage()
	if vm.heap.nextGC != start {
		t.Errorf("expected the threshold to stay at %d with little live data, got %d", start, vm.heap.nextGC)
	}

	// Much survives: the threshold follows it
	vm = newVM()
	keep(vm, 500*1024)
	vm.CollectGarbage()
	live := vm.heap.BytesAllocated()
	if want := live * 2; vm.heap.nextGC != want {
		t.Errorf("expected the threshold at twice the live size (%d), got %d", want, vm.heap.nextGC)
	}

	// ...but never past the limit of the heap
	vm = newVM()
	keep(vm, 700*1024)
	vm.CollectGarbage()
	if vm.heap.nextGC != vm.heap.MaxBytes() {
		t.Errorf("expected the threshold capped at the heap limit %d, got %d", vm.heap.MaxBytes(), vm.heap.nextGC)
	}
}

func TestGCWithEmptyHeap(t *testing.T) {
	vm := NewVM()

	// Run GC on empty heap (should not crash)
	vm.CollectGarbage()

	count := countObjects(vm.heap)
	if count != 0 {
		t.Errorf("Expected 0 objects, got %d", count)
	}
}

func TestGCWithAllGarbage(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024)

	// Allocate garbage with no references
	for i := 0; i < 10; i++ {
		vm.heap.AllocString("garbage")
	}

	vm.CollectGarbage()

	count := countObjects(vm.heap)
	if count != 0 {
		t.Errorf("Expected all garbage to be collected, got %d objects", count)
	}

	if vm.heap.BytesAllocated() != 0 {
		t.Errorf("Expected 0 bytes after collecting all garbage, got %d", vm.heap.BytesAllocated())
	}
}

func TestAutoGCTrigger(t *testing.T) {
	vm := NewVMWithHeapSize(2 * 1024) // Very small heap

	// Keep reference to first object
	first, _, _ := vm.heap.AllocString("keep")
	vm.regs = make([]Value, 1)
	vm.regs[0] = NewHeapPtr(uintptr(unsafe.Pointer(first)))

	// Allocate until GC is triggered
	triggered := false
	for i := 0; i < 100; i++ {
		_, shouldGC, err := vm.heap.AllocString("test string with some content")
		if err != nil {
			break
		}
		if shouldGC {
			triggered = true
			break
		}
	}

	if !triggered {
		t.Error("GC should have been triggered")
	}
}

// Helper function to count objects in heap
func countObjects(h *Heap) int {
	count := 0
	current := h.objects
	for current != nil {
		count++
		current = current.Next
	}
	return count
}

// A closure the program keeps in a variable, a field or an array is
// reached as a value, not as the function being run. What it captured
// has to be kept all the same.
func TestGCKeepsWhatAClosureHeldAsAValueCaptured(t *testing.T) {
	vm := NewVMWithHeapSize(10 * 1024)
	chunk := &Chunk{Prototypes: []FunctionPrototype{{Name: "counter", UpvalueCount: 1}}}

	captured, _, _ := vm.heap.AllocString("captured")
	slot := NewHeapPtr(uintptr(unsafe.Pointer(captured)))
	stack := newRegStack(1)
	stack.vals[0] = slot
	upvalue, _, _ := vm.heap.AllocUpvalue(stack, 0)
	upvalue.Close()
	closure, _, _ := vm.heap.AllocFunction(chunk, 0, 1)
	closure.SetUpvalue(0, upvalue)

	// Held in an array held in a register
	holder, _, _ := vm.heap.AllocArray(1)
	holder.Append(NewHeapPtr(uintptr(unsafe.Pointer(closure))))
	vm.regs = []Value{NewHeapPtr(uintptr(unsafe.Pointer(holder)))}
	vm.heap.AllocString("garbage")

	var freed []ObjType
	vm.heap.onFree = func(header *ObjHeader) { freed = append(freed, header.Type) }
	vm.CollectGarbage()
	vm.CollectGarbage()

	if len(freed) != 1 || freed[0] != ObjTypeString {
		t.Errorf("expected only the garbage to be freed, got %v", freed)
	}
	if got := countObjects(vm.heap); got != 4 {
		t.Errorf("expected the array, the closure, its variable and the text to be kept, got %d objects", got)
	}
}

// A call does not clear its registers. What a call that returned left
// above the top is made null by the next collection, which frees it: a
// call that takes those registers later, and has the heap collected
// before it writes them, must not lead the collector to what was freed.
func TestRegistersLeftByACallAreNotFollowedOnceFreed(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		// main
		NewInstr(OpCALLK, 1, 0, 1), // leave()
		NewInstr(OpGC, 0, 0, 0),    // frees what leave left
		NewInstr(OpCALLK, 1, 0, 2), // reads()
		NewInstr(OpLOADNULL, 0, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
		// leave: three arrays in its registers, and nothing returned
		NewInstr(OpNEWARRAY, 1, 4, 0),
		NewInstr(OpNEWARRAY, 2, 4, 0),
		NewInstr(OpNEWARRAY, 3, 4, 0),
		NewInstr(OpLOADNULL, 0, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
		// reads: the same registers, collected before anything is written
		NewInstr(OpGC, 0, 0, 0),
		NewInstr(OpLOADNULL, 0, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 5, RegisterCount: 2},
		{Name: "leave", CodeStart: 5, CodeSize: 5, RegisterCount: 4},
		{Name: "reads", CodeStart: 10, CodeSize: 3, RegisterCount: 4},
	}
	if err := Verify(chunk); err != nil {
		t.Fatalf("does not verify: %v", err)
	}

	machine := NewVM()
	var freed []*ObjHeader
	machine.heap.onFree = func(header *ObjHeader) {
		if header.Type == ObjTypeArray {
			freed = append(freed, header)
		}
	}
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if len(freed) != 3 {
		t.Fatalf("expected the three arrays to be freed, %d were", len(freed))
	}
	// A freed object is off the list of objects, so nothing unmarks it: one
	// marked is one the collector was led to after it was freed
	for _, header := range freed {
		if header.Marked {
			t.Errorf("a freed array was marked by a later collection")
		}
	}
}

// The registers of a task that does not have the turn are cleared above
// where it stopped; those of the task that has it, above where it is,
// not where it stopped the last time.
func TestEachStackIsClearedAboveItsOwnTop(t *testing.T) {
	running := newRegStack(16)
	waiting := newRegStack(16)
	for i := 0; i < 16; i++ {
		running.vals[i] = NewInt(int64(i))
		waiting.vals[i] = NewInt(int64(i))
	}
	running.used, waiting.used = 16, 16

	clearAbove(running, 12)
	clearAbove(waiting, 5)
	for i, v := range running.vals {
		if held := i < 12; held != (v != valueNull) {
			t.Errorf("running stack: register %d is %s", i, v)
		}
	}
	for i, v := range waiting.vals {
		if held := i < 5; held != (v != valueNull) {
			t.Errorf("waiting stack: register %d is %s", i, v)
		}
	}
	if running.used != 12 || waiting.used != 5 {
		t.Errorf("expected what is used to be the tops, got %d and %d", running.used, waiting.used)
	}

	// What was never used is not looked at again
	clearAbove(waiting, 3)
	if waiting.vals[3] != valueNull || waiting.vals[2] == valueNull {
		t.Errorf("expected the registers from 3 to be cleared, and those below kept")
	}
}
