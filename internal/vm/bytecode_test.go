package vm

import (
	"bytes"
	"testing"
)

func TestSerializeDeserializeSimpleChunk(t *testing.T) {
	chunk := NewChunk()
	chunk.AddConstant(NewInt(42))
	chunk.AddConstant(NewNumber(3.14))
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

	// Serialize
	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}

	// Deserialize
	decoded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}

	// Verify constants
	if len(decoded.Constants) != 2 {
		t.Errorf("expected 2 constants, got %d", len(decoded.Constants))
	}
	if !decoded.Constants[0].IsInt() || decoded.Constants[0].AsInt() != 42 {
		t.Errorf("constant 0 mismatch: %s", decoded.Constants[0].String())
	}
	if !decoded.Constants[1].IsNumber() || decoded.Constants[1].AsNumber() != 3.14 {
		t.Errorf("constant 1 mismatch: %s", decoded.Constants[1].String())
	}

	// Verify code
	if len(decoded.Code) != 2 {
		t.Errorf("expected 2 instructions, got %d", len(decoded.Code))
	}

	// Verify metadata
	if len(decoded.Prototypes) != 1 {
		t.Errorf("expected 1 prototype, got %d", len(decoded.Prototypes))
	}
	if decoded.Prototypes[0].RegisterCount != 1 {
		t.Errorf("expected RegisterCount=1, got %d", decoded.Prototypes[0].RegisterCount)
	}
	if decoded.Prototypes[0].ArgCount != 0 {
		t.Errorf("expected ArgCount=0, got %d", decoded.Prototypes[0].ArgCount)
	}
}

func TestSerializeAllValueTypes(t *testing.T) {
	chunk := NewChunk()
	chunk.AddConstant(NewNumber(42.5))
	chunk.AddConstant(NewInt(100))
	chunk.AddConstant(NewBool(true))
	chunk.AddConstant(NewBool(false))
	chunk.AddConstant(NewNull())
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      0,
		RegisterCount: 0,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}

	decoded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}

	if len(decoded.Constants) != 5 {
		t.Fatalf("expected 5 constants, got %d", len(decoded.Constants))
	}

	// Verify types preserved
	if !decoded.Constants[0].IsNumber() {
		t.Error("constant 0 should be Number")
	}
	if !decoded.Constants[1].IsInt() {
		t.Error("constant 1 should be Int")
	}
	if !decoded.Constants[2].IsBool() || !decoded.Constants[2].AsBool() {
		t.Error("constant 2 should be Bool(true)")
	}
	if !decoded.Constants[3].IsBool() || decoded.Constants[3].AsBool() {
		t.Error("constant 3 should be Bool(false)")
	}
	if !decoded.Constants[4].IsNull() {
		t.Error("constant 4 should be Null")
	}
}

func TestSerializeEmptyChunk(t *testing.T) {
	chunk := NewChunk()
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      0,
		RegisterCount: 0,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}

	decoded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}

	if len(decoded.Constants) != 0 {
		t.Errorf("expected 0 constants, got %d", len(decoded.Constants))
	}
	if len(decoded.Code) != 0 {
		t.Errorf("expected 0 instructions, got %d", len(decoded.Code))
	}
}

func TestDeserializeInvalidMagic(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}

	_, err := DeserializeChunk(data)
	if err == nil {
		t.Fatal("expected error for invalid magic number")
	}
}

func TestDeserializeInvalidVersion(t *testing.T) {
	chunk := NewChunk()
	data, _ := SerializeChunk(chunk)

	// Corrupt version byte
	data[7] = 99

	_, err := DeserializeChunk(data)
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestDeserializeTruncated(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte{0x4E, 0x4D}},
		{"no version", []byte{0x4E, 0x4D, 0x55, 0x4C}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeserializeChunk(tt.data)
			if err == nil {
				t.Fatal("expected error for truncated bytecode")
			}
		})
	}
}

func TestRoundTripPreservesExactBytecode(t *testing.T) {
	chunk := NewChunk()
	chunk.AddConstant(NewInt(10))
	chunk.AddConstant(NewInt(20))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, 0, 0))
	chunk.AddInstruction(NewInstr(OpLOADK, 1, 1, 0))
	chunk.AddInstruction(NewInstr(OpADD, 2, 0, 1))
	chunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      4,
		RegisterCount: 3,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	// First serialization
	data1, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("first serialization failed: %v", err)
	}

	// Deserialize and re-serialize
	decoded, err := DeserializeChunk(data1)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}

	data2, err := SerializeChunk(decoded)
	if err != nil {
		t.Fatalf("second serialization failed: %v", err)
	}

	// Compare bytes
	if !bytes.Equal(data1, data2) {
		t.Error("round-trip did not preserve exact bytecode")
	}
}

// The priv fields of each struct are part of the file: a program read
// back hides from std.reflect what it hid when it was compiled.
func TestSerializeKeepsPrivateFields(t *testing.T) {
	chunk := NewChunk()
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{Name: "main", CodeSize: 1, RegisterCount: 1})
	chunk.Private = map[string][]string{"std.dict.Dict": {"keys", "values"}, "Account": {"pin"}}

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}
	decoded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}
	if got := decoded.Private; len(got) != 2 || len(got["std.dict.Dict"]) != 2 || got["std.dict.Dict"][1] != "values" || got["Account"][0] != "pin" {
		t.Errorf("expected the priv fields to come back, got %v", got)
	}
	again, _ := SerializeChunk(decoded)
	if !bytes.Equal(data, again) {
		t.Error("expected the same chunk to be written as the same bytes")
	}
}
