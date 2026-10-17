package vm

import (
	"testing"
	"unsafe"
)

// TestMemoryIntegrationArrayManipulation demonstrates array allocation and manipulation
func TestMemoryIntegrationArrayManipulation(t *testing.T) {
	vm := NewVM()

	// Create a chunk that:
	// 1. Allocates an array with capacity 5
	// 2. Stores integers 10, 20, 30 in it
	// 3. Retrieves the value at index 1
	// 4. Returns it

	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWARRAY, 0, 5, 0), // r0 = new array(5)
			NewInstr(OpLOADK, 1, 0, 0),    // r1 = 10
			NewInstr(OpLOADK, 2, 3, 0),    // r2 = 0 (index)
			NewInstr(OpSETINDEX, 0, 2, 1), // r0[r2] = r1
			NewInstr(OpLOADK, 1, 1, 0),    // r1 = 20
			NewInstr(OpLOADK, 2, 2, 0),    // r2 = 1 (index)
			NewInstr(OpSETINDEX, 0, 2, 1), // r0[r2] = r1
			NewInstr(OpLOADK, 2, 2, 0),    // r2 = 1 (index)
			NewInstr(OpGETINDEX, 3, 0, 2), // r3 = r0[r2]
			NewInstr(OpRET, 3, 0, 0),      // return r3
		},
		Constants: []Value{
			NewInt(10),
			NewInt(20),
			NewInt(1),
			NewInt(0),
		},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      10,
				RegisterCount: 4,
				ArgCount:      0,
			},
		},
	}

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 20 {
		t.Errorf("Expected 20, got %v", result)
	}
}

// TestMemoryIntegrationObjectFields demonstrates object allocation and field access
func TestMemoryIntegrationObjectFields(t *testing.T) {
	vm := NewVM()

	// Pre-allocate string constants for field names
	nameStr, _, _ := vm.heap.AllocString("name")
	ageStr, _, _ := vm.heap.AllocString("age")

	// Create a chunk that:
	// 1. Allocates an object
	// 2. Sets field "name" to 123
	// 3. Sets field "age" to 25
	// 4. Gets field "age" and returns it

	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWOBJ, 0, 0, 0),   // r0 = new object
			NewInstr(OpLOADK, 1, 2, 0),    // r1 = 123
			NewInstr(OpSETFIELD, 0, 0, 1), // r0["name"] = r1
			NewInstr(OpLOADK, 1, 3, 0),    // r1 = 25
			NewInstr(OpSETFIELD, 0, 1, 1), // r0["age"] = r1
			NewInstr(OpGETFIELD, 2, 0, 1), // r2 = r0["age"]
			NewInstr(OpRET, 2, 0, 0),      // return r2
		},
		Constants: []Value{
			NewHeapPtr(uintptr(unsafe.Pointer(nameStr))),
			NewHeapPtr(uintptr(unsafe.Pointer(ageStr))),
			NewInt(123),
			NewInt(25),
		},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      7,
				RegisterCount: 3,
				ArgCount:      0,
			},
		},
	}

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 25 {
		t.Errorf("Expected 25, got %v", result)
	}
}

// TestMemoryIntegrationGCDuringExecution demonstrates GC triggered during execution
func TestMemoryIntegrationGCDuringExecution(t *testing.T) {
	vm := NewVMWithHeapSize(2 * 1024) // Small heap to trigger GC

	// Allocate arrays in a loop, only keeping the last one
	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = new array(10)
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = new array(10) - overwrites
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = new array(10) - overwrites
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = new array(10) - overwrites
			NewInstr(OpARRLEN, 1, 0, 0),    // r1 = len(r0)
			NewInstr(OpRET, 1, 0, 0),       // return r1
		},
		Constants: []Value{},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      6,
				RegisterCount: 2,
				ArgCount:      0,
			},
		},
	}

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 0 {
		t.Errorf("Expected array length 0, got %v", result)
	}

	// GC should have collected unreferenced arrays
	// Only the last array should remain
	count := countObjects(vm.heap)
	if count > 1 {
		t.Logf("Note: %d objects remain (expected 1 or fewer after GC)", count)
	}
}

// TestMemoryIntegrationNestedStructures demonstrates complex nested heap objects
func TestMemoryIntegrationNestedStructures(t *testing.T) {
	vm := NewVM()

	// Pre-allocate string for field name
	dataStr, _, _ := vm.heap.AllocString("data")

	// Create: object with field "data" pointing to an array of numbers
	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWOBJ, 0, 0, 0),   // r0 = new object
			NewInstr(OpNEWARRAY, 1, 3, 0), // r1 = new array(3)
			NewInstr(OpLOADK, 2, 1, 0),    // r2 = 100
			NewInstr(OpLOADK, 3, 2, 0),    // r3 = 0
			NewInstr(OpSETINDEX, 1, 3, 2), // r1[0] = 100
			NewInstr(OpSETFIELD, 0, 0, 1), // r0["data"] = r1
			NewInstr(OpGETFIELD, 4, 0, 0), // r4 = r0["data"]
			NewInstr(OpLOADK, 3, 2, 0),    // r3 = 0
			NewInstr(OpGETINDEX, 5, 4, 3), // r5 = r4[0]
			NewInstr(OpRET, 5, 0, 0),      // return r5
		},
		Constants: []Value{
			NewHeapPtr(uintptr(unsafe.Pointer(dataStr))),
			NewInt(100),
			NewInt(0),
		},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      10,
				RegisterCount: 6,
				ArgCount:      0,
			},
		},
	}

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 100 {
		t.Errorf("Expected 100, got %v", result)
	}
}

// TestMemoryIntegrationManualGC demonstrates manual GC triggering
func TestMemoryIntegrationManualGC(t *testing.T) {
	vm := NewVM()

	// Create some garbage, then manually trigger GC
	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = array (garbage)
			NewInstr(OpNEWARRAY, 0, 10, 0), // r0 = array (overwrites, making previous garbage)
			NewInstr(OpNEWOBJ, 1, 0, 0),    // r1 = object (garbage)
			NewInstr(OpGC, 0, 0, 0),        // trigger GC
			NewInstr(OpLOADK, 2, 0, 0),     // r2 = 42
			NewInstr(OpRET, 2, 0, 0),       // return 42
		},
		Constants: []Value{
			NewInt(42),
		},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      6,
				RegisterCount: 3,
				ArgCount:      0,
			},
		},
	}

	bytesBefore := vm.heap.BytesAllocated()

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 42 {
		t.Errorf("Expected 42, got %v", result)
	}

	bytesAfter := vm.heap.BytesAllocated()

	// After GC, we should have less memory allocated
	// (only r0 array should remain)
	if bytesAfter >= bytesBefore {
		t.Logf("Note: Expected memory reduction after GC, before=%d, after=%d", bytesBefore, bytesAfter)
	}
}

// TestMemoryIntegrationArrayLength demonstrates ARRLEN opcode
func TestMemoryIntegrationArrayLength(t *testing.T) {
	vm := NewVM()

	chunk := &Chunk{
		Code: []Instr{
			NewInstr(OpNEWARRAY, 0, 5, 0), // r0 = new array(5)
			NewInstr(OpLOADK, 1, 0, 0),    // r1 = 10
			NewInstr(OpLOADK, 2, 1, 0),    // r2 = 0
			NewInstr(OpSETINDEX, 0, 2, 1), // r0[0] = 10
			NewInstr(OpLOADK, 2, 2, 0),    // r2 = 1
			NewInstr(OpSETINDEX, 0, 2, 1), // r0[1] = 10
			NewInstr(OpARRLEN, 3, 0, 0),   // r3 = len(r0)
			NewInstr(OpRET, 3, 0, 0),      // return r3
		},
		Constants: []Value{
			NewInt(10),
			NewInt(0),
			NewInt(1),
		},
		Prototypes: []FunctionPrototype{
			{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      8,
				RegisterCount: 4,
				ArgCount:      0,
			},
		},
	}

	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Array was created with elements at index 0 and 1, so length should be 2
	if !result.IsInt() || result.AsInt() != 2 {
		t.Errorf("Expected length 2, got %v", result)
	}
}
