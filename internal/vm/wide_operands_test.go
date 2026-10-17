package vm

import (
	"fmt"
	"math"
	"testing"
)

// Operands used to be one byte, which capped a function at 256 registers
// and a program at 256 constants and 256 functions. These tests work well
// past those numbers.

func TestRegistersAndConstantsBeyondOneByte(t *testing.T) {
	constants := make([]Value, 1000)
	for i := range constants {
		constants[i] = NewInt(int64(i) * 7)
	}
	chunk := chunkOf(nil, constants, fnDef{FunctionPrototype{Name: "main", RegisterCount: 1000}, []Instr{
		NewInstr(OpLOADK, 900, 999, 0),   // 6993
		NewInstr(OpLOADK, 256, 256, 0),   // 1792: the first index that did not fit
		NewInstr(OpLOADI, 901, 60000, 0), // Immediates are wider too
		NewInstr(OpLOADI, 902, MaxOperand, 0),
		NewInstr(OpADD, 999, 900, 256),
		NewInstr(OpADD, 999, 999, 901),
		NewInstr(OpADD, 999, 999, 902),
		NewInstr(OpMOV, 300, 999, 0),
		NewInstr(OpRET, 300, 0, 0),
	}})

	result, err := NewVM().Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if want := int64(6993 + 1792 + 60000 + MaxOperand); !result.IsInt() || result.AsInt() != want {
		t.Errorf("expected %d, got %s", want, FormatValue(result))
	}
}

func TestFunctionsBeyondOneByte(t *testing.T) {
	// 400 functions; function i returns i. main calls three of them from a
	// call window placed above register 255 and adds the results.
	funcs := []fnDef{{FunctionPrototype{Name: "main", RegisterCount: 320}, []Instr{
		NewInstr(OpNEWFUNC, 300, 399, 0),
		NewInstr(OpCALL, 300, 0, 0),
		NewInstr(OpMOV, 310, 0, 0),
		NewInstr(OpNEWFUNC, 300, 256, 0),
		NewInstr(OpCALL, 300, 0, 0),
		NewInstr(OpADD, 310, 310, 0),
		NewInstr(OpNEWFUNC, 300, 255, 0),
		NewInstr(OpCALL, 300, 0, 0),
		NewInstr(OpADD, 310, 310, 0),
		NewInstr(OpRET, 310, 0, 0),
	}}}
	for i := 1; i < 400; i++ {
		funcs = append(funcs, fnDef{FunctionPrototype{Name: fmt.Sprintf("f%d", i), RegisterCount: 1}, []Instr{
			NewInstr(OpLOADI, 0, uint16(i), 0),
			NewInstr(OpRET, 0, 0, 0),
		}})
	}

	result, err := NewVM().Exec(chunkOf(nil, nil, funcs...))
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if want := int64(399 + 256 + 255); !result.IsInt() || result.AsInt() != want {
		t.Errorf("expected %d, got %s", want, FormatValue(result))
	}
}

func TestFieldNamesBeyondOneByte(t *testing.T) {
	// 600 field names: constant i refers to string i. An index that wrapped
	// around at 256 would read or write the wrong field.
	chunk := NewChunk()
	for i := 0; i < 600; i++ {
		chunk.AddConstant(NewInt(int64(chunk.AddString(fmt.Sprintf("field%d", i)))))
	}
	code := []Instr{
		NewInstr(OpNEWOBJ, 1, 0, 0),
		NewInstr(OpLOADI, 2, 11, 0),
		NewInstr(OpSETFIELD, 1, 500, 2), // field500 = 11
		NewInstr(OpLOADI, 2, 22, 0),
		NewInstr(OpSETFIELD, 1, 244, 2), // field244 = 22; 500 mod 256 is 244
		NewInstr(OpGETFIELD, 3, 1, 500),
		NewInstr(OpGETFIELD, 4, 1, 244),
		NewInstr(OpHASFIELD, 5, 1, 599), // Never set
		NewInstr(OpDELFIELD, 6, 1, 500),
		NewInstr(OpHASFIELD, 7, 1, 500),
		NewInstr(OpHASFIELD, 8, 1, 244),
		NewInstr(OpNEWARRAY, 9, 8, 0),
	}
	for reg := uint16(3); reg <= 8; reg++ {
		code = append(code, NewInstr(OpARRPUSH, 9, reg, 0))
	}
	code = append(code, NewInstr(OpRET, 9, 0, 0))
	chunk.Code = code
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 10}}

	result, err := NewVM().Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if got, want := FormatValue(result), "[11, 22, false, true, false, true]"; got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestConstantIndexOutOfBounds(t *testing.T) {
	// A wide index is still checked against the pool
	chunk := chunkOf(nil, []Value{NewInt(1)}, fnDef{FunctionPrototype{Name: "main", RegisterCount: 2}, []Instr{
		NewInstr(OpLOADK, 0, 40000, 0),
		NewInstr(OpRET, 0, 0, 0),
	}})
	if _, err := NewVM().Exec(chunk); err == nil {
		t.Error("expected an error for a constant index past the pool")
	}
}

func TestOffset32RoundTrip(t *testing.T) {
	offsets := []int32{0, 1, -1, 255, 256, -256, 65535, 65536, -65536, 70000, -70000,
		1 << 24, -(1 << 24), math.MaxInt32, math.MinInt32}
	for _, offset := range offsets {
		instr := EncodeOffset32(offset)
		if got := DecodeOffset32(instr); got != offset {
			t.Errorf("offset %d came back as %d (%+v)", offset, got, instr)
		}
	}
}

func TestLongJumpAcrossManyInstructions(t *testing.T) {
	// Jump forward over 70000 instructions, more than one operand can
	// address, then backward over them again.
	const filler = 70000
	code := []Instr{
		NewInstr(OpLOADI, 1, 0, 0),
		NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMP32, 0, 0, 0), EncodeOffset32(filler + 2), // to "far"
		// "back": reached by the backward jump
		NewInstr(OpLOADI, 2, 5, 0),
		NewInstr(OpRET, 2, 0, 0),
	}
	for i := 0; i < filler; i++ {
		code = append(code, NewInstr(OpLOADI, 1, 9, 0)) // Skipped
	}
	// "far"
	code = append(code, NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMP32, 0, 0, 0), EncodeOffset32(-(filler + 5)))
	chunk := chunkOf(nil, nil, fnDef{FunctionPrototype{Name: "main", RegisterCount: 3}, code})

	result, err := NewVM().Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if !result.IsInt() || result.AsInt() != 5 {
		t.Errorf("expected 5, got %s", FormatValue(result))
	}
}

func TestSerializeWideOperands(t *testing.T) {
	chunk := chunkOf([]string{"print"}, []Value{NewInt(1), NewNumber(2.5)}, fnDef{
		FunctionPrototype{Name: "main", RegisterCount: 70000, Signature: "fn()->int"}, []Instr{
			NewInstr(OpADD, 256, 40000, MaxOperand),
			NewInstr(OpLOADK, MaxOperand, 257, 0),
			NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMPIF32, 300, 0, 0), EncodeOffset32(-123456),
			NewInstr(OpRET, 0, 0, 0),
		}})

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatalf("serialization failed: %v", err)
	}
	loaded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatalf("deserialization failed: %v", err)
	}
	if len(loaded.Code) != len(chunk.Code) {
		t.Fatalf("expected %d instructions, got %d", len(chunk.Code), len(loaded.Code))
	}
	for i, want := range chunk.Code {
		if loaded.Code[i] != want {
			t.Errorf("instruction %d: expected %+v, got %+v", i, want, loaded.Code[i])
		}
	}
	if got := DecodeOffset32(loaded.Code[4]); got != -123456 {
		t.Errorf("expected the jump offset to survive, got %d", got)
	}
	if loaded.Prototypes[0].RegisterCount != 70000 {
		t.Errorf("expected the register count to survive, got %d", loaded.Prototypes[0].RegisterCount)
	}

	// A file cut in the middle of an instruction is refused
	if _, err := DeserializeChunk(data[:8+4+16+4+instrSize+3]); err == nil {
		t.Error("expected an error for a truncated instruction")
	}
	// Files written with one-byte operands (version 3) are refused, not misread
	data[4] = 3
	if _, err := DeserializeChunk(data); err == nil {
		t.Error("expected a version error for a file with one-byte operands")
	}
}
