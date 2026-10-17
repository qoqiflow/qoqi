package vm

import (
	"testing"
	"unsafe"
)

// TestStringCreationAndExecution tests string creation at the VM level
func TestStringCreationAndExecution(t *testing.T) {
	v := NewVM()

	// Create a chunk with a string in the string pool
	chunk := NewChunk()
	strIdx := chunk.AddString("Hello, VM!")
	chunk.AddConstant(NewInt(int64(strIdx)))

	// NEWSTR r0 0
	chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	// RET r0
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	addMainProto(chunk, 1, 0)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify result is a heap pointer
	if !result.IsHeapPtr() {
		t.Fatalf("Expected heap pointer, got %s", result.String())
	}

	// Verify string content
	ptr := result.AsHeapPtr()
	strObj := (*StringObj)(unsafe.Pointer(ptr))
	if strObj.String() != "Hello, VM!" {
		t.Errorf("Expected %q, got %q", "Hello, VM!", strObj.String())
	}
}

// TestStringWithEscapeSequences tests escape sequences at VM level
func TestStringWithEscapeSequences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"newline", "Line1\nLine2", "Line1\nLine2"},
		{"tab", "Tab\there", "Tab\there"},
		{"quote", "Say \"Hello\"", "Say \"Hello\""},
		{"backslash", "Path\\to\\file", "Path\\to\\file"},
		{"carriage return", "CR\rhere", "CR\rhere"},
		{"multiple", "Test:\n\tHello\n\tWorld", "Test:\n\tHello\n\tWorld"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVM()
			chunk := NewChunk()

			strIdx := chunk.AddString(tt.input)
			chunk.AddConstant(NewInt(int64(strIdx)))
			chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
			chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
			mainProto := FunctionPrototype{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      2,
				RegisterCount: 1,
				ArgCount:      0,
			}
			chunk.Prototypes = append(chunk.Prototypes, mainProto)

			result, err := v.Exec(chunk)
			if err != nil {
				t.Fatalf("Execution failed: %v", err)
			}

			ptr := result.AsHeapPtr()
			strObj := (*StringObj)(unsafe.Pointer(ptr))
			if strObj.String() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, strObj.String())
			}
		})
	}
}

// TestStringPoolMultipleStrings tests multiple strings in the pool
func TestStringPoolMultipleStrings(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	// Add multiple strings to the pool
	strings := []string{"First", "Second", "Third", "Fourth", "Fifth"}
	for _, s := range strings {
		idx := chunk.AddString(s)
		chunk.AddConstant(NewInt(int64(idx)))
	}

	// Create all strings: NEWSTR r0-r4 from constants 0-4
	for i := 0; i < len(strings); i++ {
		chunk.AddInstruction(NewInstr(OpNEWSTR, uint16(i), uint16(i), 0))
	}

	// Return the third string (r2)
	chunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      len(chunk.Code),
		RegisterCount: 5,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	ptr := result.AsHeapPtr()
	strObj := (*StringObj)(unsafe.Pointer(ptr))
	if strObj.String() != "Third" {
		t.Errorf("Expected %q, got %q", "Third", strObj.String())
	}
}

// TestStringEmptyString tests empty string handling
func TestStringEmptyString(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	strIdx := chunk.AddString("")
	chunk.AddConstant(NewInt(int64(strIdx)))
	chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	ptr := result.AsHeapPtr()
	strObj := (*StringObj)(unsafe.Pointer(ptr))
	if strObj.String() != "" {
		t.Errorf("Expected empty string, got %q", strObj.String())
	}
}

// TestStringLongString tests handling of longer strings
func TestStringLongString(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	// Create a long string (1000 characters)
	longStr := ""
	for i := 0; i < 100; i++ {
		longStr += "0123456789"
	}

	strIdx := chunk.AddString(longStr)
	chunk.AddConstant(NewInt(int64(strIdx)))
	chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	ptr := result.AsHeapPtr()
	strObj := (*StringObj)(unsafe.Pointer(ptr))
	if strObj.String() != longStr {
		t.Errorf("String length mismatch: expected %d, got %d", len(longStr), len(strObj.String()))
	}
}

// TestStringUnicodeSupport tests Unicode string handling
func TestStringUnicodeSupport(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"emoji", "Hello 👋 World 🌍"},
		{"chinese", "你好世界"},
		{"japanese", "こんにちは世界"},
		{"arabic", "مرحبا بالعالم"},
		{"mixed", "Hello 世界 🌍 مرحبا"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVM()
			chunk := NewChunk()

			strIdx := chunk.AddString(tt.input)
			chunk.AddConstant(NewInt(int64(strIdx)))
			chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
			chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
			mainProto := FunctionPrototype{
				Name:          "main",
				CodeStart:     0,
				CodeSize:      2,
				RegisterCount: 1,
				ArgCount:      0,
			}
			chunk.Prototypes = append(chunk.Prototypes, mainProto)

			result, err := v.Exec(chunk)
			if err != nil {
				t.Fatalf("Execution failed: %v", err)
			}

			ptr := result.AsHeapPtr()
			strObj := (*StringObj)(unsafe.Pointer(ptr))
			if strObj.String() != tt.input {
				t.Errorf("Expected %q, got %q", tt.input, strObj.String())
			}
		})
	}
}

// TestStringSerializationRoundTrip tests string pool serialization
func TestStringSerializationRoundTrip(t *testing.T) {
	// Create original chunk with strings
	chunk1 := NewChunk()
	strings := []string{
		"First",
		"Second with\nnewline",
		"Third with \"quotes\"",
		"",
		"Unicode: 世界 🌍",
	}

	for _, s := range strings {
		idx := chunk1.AddString(s)
		chunk1.AddConstant(NewInt(int64(idx)))
	}

	chunk1.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	chunk1.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	addMainProto(chunk1, 1, 0)

	// Serialize
	bytecode, err := SerializeChunk(chunk1)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Deserialize
	chunk2, err := DeserializeChunk(bytecode)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Compare string pools
	if len(chunk1.StringPool) != len(chunk2.StringPool) {
		t.Fatalf("String pool size mismatch: %d vs %d", len(chunk1.StringPool), len(chunk2.StringPool))
	}

	for i := range chunk1.StringPool {
		if chunk1.StringPool[i] != chunk2.StringPool[i] {
			t.Errorf("String %d mismatch: %q vs %q", i, chunk1.StringPool[i], chunk2.StringPool[i])
		}
	}

	// Execute both and verify results match
	v1 := NewVM()
	result1, err := v1.Exec(chunk1)
	if err != nil {
		t.Fatalf("Execution 1 failed: %v", err)
	}

	v2 := NewVM()
	result2, err := v2.Exec(chunk2)
	if err != nil {
		t.Fatalf("Execution 2 failed: %v", err)
	}

	// Both should produce strings with same content
	str1 := (*StringObj)(unsafe.Pointer(result1.AsHeapPtr())).String()
	str2 := (*StringObj)(unsafe.Pointer(result2.AsHeapPtr())).String()

	if str1 != str2 {
		t.Errorf("Execution results differ: %q vs %q", str1, str2)
	}
}

// TestStringErrorInvalidPoolIndex tests error handling for invalid string pool index
func TestStringErrorInvalidPoolIndex(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	// Add string to pool but reference wrong index
	chunk.AddString("Valid String")
	chunk.AddConstant(NewInt(999)) // Invalid index

	chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	_, err := v.Exec(chunk)
	if err == nil {
		t.Fatal("Expected error for invalid string pool index")
	}

	if !contains(err.Error(), "string 999 of 1") {
		t.Errorf("Expected string pool error, got: %v", err)
	}
}

// TestStringErrorNonIntegerConstant tests error when constant is not an integer
func TestStringErrorNonIntegerConstant(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	// Add non-integer constant
	chunk.AddConstant(NewNumber(3.14)) // Should be Int for string pool index

	chunk.AddInstruction(NewInstr(OpNEWSTR, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	_, err := v.Exec(chunk)
	if err == nil {
		t.Fatal("Expected error for non-integer constant")
	}

	if !contains(err.Error(), "is not the number of a string") {
		t.Errorf("Expected string pool index error, got: %v", err)
	}
}

// TestStringGarbageCollection tests that strings are properly garbage collected
func TestStringGarbageCollection(t *testing.T) {
	v := NewVMWithHeapSize(4 * 1024) // Small heap

	chunk := NewChunk()

	// Add multiple strings
	for i := 0; i < 10; i++ {
		idx := chunk.AddString("String number " + string(rune('0'+i)))
		chunk.AddConstant(NewInt(int64(idx)))
	}

	// Create strings in a loop, overwriting previous ones
	// This should trigger GC and clean up unreachable strings
	for i := 0; i < 10; i++ {
		chunk.AddInstruction(NewInstr(OpNEWSTR, 0, uint16(i), 0)) // Always r0, different constants
	}

	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      len(chunk.Code),
		RegisterCount: 1,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should return the last string
	ptr := result.AsHeapPtr()
	strObj := (*StringObj)(unsafe.Pointer(ptr))
	expected := "String number 9"
	if strObj.String() != expected {
		t.Errorf("Expected %q, got %q", expected, strObj.String())
	}

	// Verify that GC ran at some point (heap should not have grown excessively)
	if v.heap.BytesAllocated() > 10*1024 {
		t.Errorf("Heap too large: %d bytes (GC may not have run)", v.heap.BytesAllocated())
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
