package vm

import (
	"strings"
	"testing"
)

// TestMainEntryPoint tests that VM finds and executes main function
func TestMainEntryPoint(t *testing.T) {
	chunk := NewChunk()

	// Add main function that returns 42
	chunk.AddConstant(NewInt(42))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))

	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 42 {
		t.Errorf("expected 42, got %v", result)
	}
}

// TestMainNotFound tests error when main function is missing
func TestMainNotFound(t *testing.T) {
	chunk := NewChunk()

	// Add a function that's NOT named main
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))

	helperProto := FunctionPrototype{
		Name:          "helper",
		CodeStart:     0,
		CodeSize:      1,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, helperProto)

	vm := NewVM()
	_, err := vm.Exec(chunk)
	if err == nil {
		t.Fatal("expected error for missing main function")
	}

	if !strings.Contains(err.Error(), "entry function 'main' not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestMainWithMultipleFunctions tests that main is found among multiple functions
func TestMainWithMultipleFunctions(t *testing.T) {
	chunk := NewChunk()

	// Add helper function
	chunk.AddInstruction(NewInstr(OpADD, 2, 0, 1)) // Helper: add two numbers
	chunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))

	helperProto := FunctionPrototype{
		Name:          "add",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 3,
		ArgCount:      2,
	}
	chunk.Prototypes = append(chunk.Prototypes, helperProto)

	// Add main function
	chunk.AddConstant(NewInt(100))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, 0, 0)) // Main: return 100
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))

	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     2,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 100 {
		t.Errorf("expected 100, got %v", result)
	}
}

// TestMainIPStartsCorrectly tests that IP starts at main's CodeStart
func TestMainIPStartsCorrectly(t *testing.T) {
	chunk := NewChunk()

	// Add dummy instructions before main
	for i := 0; i < 10; i++ {
		chunk.AddInstruction(NewInstr(OpNOP, 0, 0, 0))
	}

	// Main function starts at instruction 10
	chunk.AddConstant(NewInt(99))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))

	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     10,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Should execute main's code, not the NOPs
	if !result.IsInt() || result.AsInt() != 99 {
		t.Errorf("expected 99, got %v", result)
	}
}

// TestMainAllocatesCorrectRegisters tests that main gets correct register count
func TestMainAllocatesCorrectRegisters(t *testing.T) {
	chunk := NewChunk()

	// Main function uses 5 registers
	chunk.AddConstant(NewInt(1))
	chunk.AddConstant(NewInt(2))
	chunk.AddConstant(NewInt(3))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, 0, 0)) // r0 = 1
	chunk.AddInstruction(NewInstr(OpLOADK, 1, 1, 0)) // r1 = 2
	chunk.AddInstruction(NewInstr(OpLOADK, 2, 2, 0)) // r2 = 3
	chunk.AddInstruction(NewInstr(OpADD, 3, 0, 1))   // r3 = r0 + r1 = 3
	chunk.AddInstruction(NewInstr(OpADD, 4, 2, 3))   // r4 = r2 + r3 = 6
	chunk.AddInstruction(NewInstr(OpRET, 4, 0, 0))   // return r4

	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      6,
		RegisterCount: 5,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	result, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 6 {
		t.Errorf("expected 6, got %v", result)
	}
}
