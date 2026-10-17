package vm

import (
	"testing"
	"unsafe"
)

// TestStringAPIDirectCreation tests direct string creation without assembly
func TestStringAPIDirectCreation(t *testing.T) {
	v := NewVM()

	// Create chunk programmatically
	chunk := NewChunk()
	chunk.StringPool = []string{"Direct API String"}
	chunk.Constants = []Value{NewInt(0)} // Index into string pool
	chunk.Code = []Instr{
		NewInstr(OpNEWSTR, 0, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
	}
	addMainProto(chunk, 1, 0)

	// Execute
	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify
	if !result.IsHeapPtr() {
		t.Fatalf("Expected heap pointer, got %s", result.String())
	}

	strObj := (*StringObj)(unsafe.Pointer(result.AsHeapPtr()))
	if strObj.String() != "Direct API String" {
		t.Errorf("Expected %q, got %q", "Direct API String", strObj.String())
	}
}

// TestStringAPIConcatenationProgram tests a program that uses multiple strings
func TestStringAPIConcatenationProgram(t *testing.T) {
	v := NewVM()

	// Create a program that creates three strings and returns the second one
	chunk := NewChunk()

	// Add three strings to pool
	chunk.StringPool = []string{
		"Alpha",
		"Beta",
		"Gamma",
	}

	// Add constants (string pool indices)
	chunk.Constants = []Value{
		NewInt(0),
		NewInt(1),
		NewInt(2),
	}

	// Code: create all three strings, return middle one
	chunk.Code = []Instr{
		NewInstr(OpNEWSTR, 0, 0, 0), // r0 = "Alpha"
		NewInstr(OpNEWSTR, 1, 1, 0), // r1 = "Beta"
		NewInstr(OpNEWSTR, 2, 2, 0), // r2 = "Gamma"
		NewInstr(OpRET, 1, 0, 0),    // return r1
	}
	addMainProto(chunk, 3, 0)

	// Execute
	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify we got "Beta"
	strObj := (*StringObj)(unsafe.Pointer(result.AsHeapPtr()))
	if strObj.String() != "Beta" {
		t.Errorf("Expected %q, got %q", "Beta", strObj.String())
	}
}

// TestStringAPIBytecodeFormatVersion tests that bytecode has correct version
func TestStringAPIBytecodeFormatVersion(t *testing.T) {
	chunk := NewChunk()
	chunk.StringPool = []string{"Test"}
	chunk.Constants = []Value{NewInt(0)}
	chunk.Code = []Instr{NewInstr(OpNOP, 0, 0, 0)}
	addMainProto(chunk, 1, 0)

	// Serialize
	bytecode, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Check magic number (bytes 0-3)
	magic := uint32(bytecode[0]) | uint32(bytecode[1])<<8 |
		uint32(bytecode[2])<<16 | uint32(bytecode[3])<<24
	expectedMagic := uint32(0x4C554D4E) // "LUMN"
	if magic != expectedMagic {
		t.Errorf("Expected magic 0x%X, got 0x%X", expectedMagic, magic)
	}

	// Check version (bytes 4-7)
	version := uint32(bytecode[4]) | uint32(bytecode[5])<<8 |
		uint32(bytecode[6])<<16 | uint32(bytecode[7])<<24
	expectedVersion := bytecodeVersion
	if version != expectedVersion {
		t.Errorf("Expected version %d, got %d", expectedVersion, version)
	}
}

// TestStringAPIEmptyPool tests chunk with empty string pool
func TestStringAPIEmptyPool(t *testing.T) {
	chunk := NewChunk()
	chunk.StringPool = []string{} // Empty pool
	chunk.Code = []Instr{NewInstr(OpNOP, 0, 0, 0)}
	addMainProto(chunk, 1, 0)

	// Serialize and deserialize
	bytecode, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	chunk2, err := DeserializeChunk(bytecode)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Verify empty string pool preserved
	if len(chunk2.StringPool) != 0 {
		t.Errorf("Expected empty string pool, got %d strings", len(chunk2.StringPool))
	}
}

// TestStringAPILargePool tests chunk with many strings
func TestStringAPILargePool(t *testing.T) {
	chunk := NewChunk()

	// Create 100 strings
	for i := 0; i < 100; i++ {
		chunk.StringPool = append(chunk.StringPool, string(rune('A'+i%26))+string(rune('0'+i/26)))
	}

	chunk.Code = []Instr{NewInstr(OpNOP, 0, 0, 0)}
	addMainProto(chunk, 1, 0)

	// Serialize and deserialize
	bytecode, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	chunk2, err := DeserializeChunk(bytecode)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Verify all strings preserved
	if len(chunk2.StringPool) != 100 {
		t.Fatalf("Expected 100 strings, got %d", len(chunk2.StringPool))
	}

	for i := 0; i < 100; i++ {
		if chunk2.StringPool[i] != chunk.StringPool[i] {
			t.Errorf("String %d mismatch: %q vs %q", i, chunk.StringPool[i], chunk2.StringPool[i])
		}
	}
}

// TestStringAPISpecialCharacters tests strings with special UTF-8 characters
func TestStringAPISpecialCharacters(t *testing.T) {
	specialStrings := []string{
		"",                         // empty
		" ",                        // space
		"\x00",                     // null byte
		"a\x00b",                   // null in middle
		"\u0001\u0002\u0003",       // control chars
		"🎉🎊🎈",                      // emojis
		"Ä Ö Ü ß",                  // German umlauts
		"Привет мир",               // Cyrillic
		"🇺🇸🇬🇧🇯🇵",                   // Flag emojis
		"\n\r\t",                   // whitespace
		string([]byte{0xFF, 0xFE}), // invalid UTF-8
	}

	v := NewVM()

	for i, str := range specialStrings {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			chunk := NewChunk()
			chunk.StringPool = []string{str}
			chunk.Constants = []Value{NewInt(0)}
			chunk.Code = []Instr{
				NewInstr(OpNEWSTR, 0, 0, 0),
				NewInstr(OpRET, 0, 0, 0),
			}
			addMainProto(chunk, 1, 0)

			result, err := v.Exec(chunk)
			if err != nil {
				t.Fatalf("Execution failed for %q: %v", str, err)
			}

			strObj := (*StringObj)(unsafe.Pointer(result.AsHeapPtr()))
			if strObj.String() != str {
				t.Errorf("String mismatch: expected %q, got %q", str, strObj.String())
			}
		})
	}
}

// TestStringAPIMemoryLayout tests string pool is stored correctly in bytecode
func TestStringAPIMemoryLayout(t *testing.T) {
	chunk := NewChunk()
	chunk.StringPool = []string{"Short", "A much longer string with more content"}
	chunk.Constants = []Value{NewInt(0), NewInt(1)}
	chunk.Code = []Instr{NewInstr(OpNOP, 0, 0, 0)}
	addMainProto(chunk, 1, 0)

	bytecode, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Deserialize
	chunk2, err := DeserializeChunk(bytecode)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Verify lengths preserved
	if len(chunk2.StringPool[0]) != len(chunk.StringPool[0]) {
		t.Errorf("String 0 length mismatch")
	}
	if len(chunk2.StringPool[1]) != len(chunk.StringPool[1]) {
		t.Errorf("String 1 length mismatch")
	}

	// Verify content preserved exactly
	if chunk2.StringPool[0] != chunk.StringPool[0] {
		t.Errorf("String 0 content mismatch")
	}
	if chunk2.StringPool[1] != chunk.StringPool[1] {
		t.Errorf("String 1 content mismatch")
	}
}

// TestStringAPINoFileIO tests complete workflow without any file I/O
func TestStringAPINoFileIO(t *testing.T) {
	// This test verifies the entire string pipeline works
	// without touching the filesystem at all

	// Step 1: Create chunk in memory
	chunk1 := NewChunk()
	testStrings := []string{
		"Memory only test",
		"No files involved",
		"Pure API usage",
	}

	for _, s := range testStrings {
		idx := chunk1.AddString(s)
		chunk1.AddConstant(NewInt(int64(idx)))
	}

	// Create program that uses all strings
	for i := 0; i < len(testStrings); i++ {
		chunk1.AddInstruction(NewInstr(OpNEWSTR, uint16(i), uint16(i), 0))
	}
	chunk1.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	addMainProto(chunk1, len(testStrings), 0)

	// Step 2: Serialize to memory buffer
	bytecode, err := SerializeChunk(chunk1)
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Step 3: Deserialize from memory buffer
	chunk2, err := DeserializeChunk(bytecode)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Step 4: Execute in VM
	v := NewVM()
	result, err := v.Exec(chunk2)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Step 5: Verify result
	strObj := (*StringObj)(unsafe.Pointer(result.AsHeapPtr()))
	if strObj.String() != testStrings[0] {
		t.Errorf("Expected %q, got %q", testStrings[0], strObj.String())
	}

	// Verify string pool integrity
	for i, expected := range testStrings {
		if chunk2.StringPool[i] != expected {
			t.Errorf("String pool %d: expected %q, got %q", i, expected, chunk2.StringPool[i])
		}
	}
}

// TestStringAPIConstantsWithStrings tests that constants and strings work together
func TestStringAPIConstantsWithStrings(t *testing.T) {
	v := NewVM()
	chunk := NewChunk()

	// Mix regular constants with string pool indices
	chunk.Constants = []Value{
		NewInt(42),      // Regular int
		NewNumber(3.14), // Regular float
		NewBool(true),   // Regular bool
		NewInt(0),       // String pool index
		NewInt(1),       // String pool index
	}

	chunk.StringPool = []string{
		"First string",
		"Second string",
	}

	// Use both regular constants and strings
	chunk.Code = []Instr{
		NewInstr(OpLOADK, 0, 0, 0),  // r0 = 42 (int)
		NewInstr(OpLOADK, 1, 1, 0),  // r1 = 3.14 (float)
		NewInstr(OpLOADK, 2, 2, 0),  // r2 = true (bool)
		NewInstr(OpNEWSTR, 3, 3, 0), // r3 = "First string"
		NewInstr(OpNEWSTR, 4, 4, 0), // r4 = "Second string"
		NewInstr(OpRET, 3, 0, 0),    // return first string
	}
	addMainProto(chunk, 5, 0)

	result, err := v.Exec(chunk)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	strObj := (*StringObj)(unsafe.Pointer(result.AsHeapPtr()))
	if strObj.String() != "First string" {
		t.Errorf("Expected %q, got %q", "First string", strObj.String())
	}
}
