package vm

import (
	"testing"
	"unsafe"
)

func TestIntegerAndBitwiseOps(t *testing.T) {
	chunk := NewChunk()

	chunk.AddInstruction(NewInstr(OpLOADI, 0, 10, 0))
	chunk.AddInstruction(NewInstr(OpLOADI, 1, 3, 0))
	chunk.AddInstruction(NewInstr(OpMOD, 2, 0, 1))
	chunk.AddInstruction(NewInstr(OpBAND, 3, 0, 1))
	chunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      5,
		RegisterCount: 4,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	val, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if !val.IsInt() || val.AsInt() != 1 {
		t.Fatalf("expected Int(1) from MOD, got %v", val)
	}

	// Re-run to check bitwise AND result (r3)
	chunk.Code[len(chunk.Code)-1] = NewInstr(OpRET, 3, 0, 0)
	val, err = vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if val.AsInt() != 2 {
		t.Fatalf("expected Int(2) from BAND, got %v", val)
	}
}

func TestShiftOps(t *testing.T) {
	chunk := NewChunk()
	chunk.AddInstruction(NewInstr(OpLOADI, 0, 1, 0))
	chunk.AddInstruction(NewInstr(OpLOADI, 1, 3, 0))
	chunk.AddInstruction(NewInstr(OpSHL, 2, 0, 1))
	chunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      4,
		RegisterCount: 3,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	val, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if val.AsInt() != 8 {
		t.Fatalf("expected Int(8) from SHL, got %v", val)
	}

	chunk2 := NewChunk()
	chunk2.AddInstruction(NewInstr(OpLOADI, 0, 8, 0))
	chunk2.AddInstruction(NewInstr(OpLOADI, 1, 3, 0))
	chunk2.AddInstruction(NewInstr(OpSHR, 2, 0, 1))
	chunk2.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	mainProto2 := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      4,
		RegisterCount: 3,
		ArgCount:      0,
	}
	chunk2.Prototypes = append(chunk2.Prototypes, mainProto2)

	val, err = vm.Exec(chunk2)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if val.AsInt() != 1 {
		t.Fatalf("expected Int(1) from SHR, got %v", val)
	}
}

func TestNegNotAndConversions(t *testing.T) {
	chunk := NewChunk()

	numConst := chunk.AddConstant(NewNumber(4.9))
	chunk.AddInstruction(NewInstr(OpLOADK, 0, uint16(numConst), 0))
	chunk.AddInstruction(NewInstr(OpNEG, 1, 0, 0))
	chunk.AddInstruction(NewInstr(OpTOINT, 2, 0, 0))
	chunk.AddInstruction(NewInstr(OpTONUM, 3, 2, 0))
	chunk.AddInstruction(NewInstr(OpRET, 3, 0, 0))
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      5,
		RegisterCount: 4,
		ArgCount:      0,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)

	vm := NewVM()
	val, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if !val.IsNumber() || val.AsNumber() != 4 {
		t.Fatalf("expected Number(4) after TOINT+TONUM truncation, got %v", val)
	}

	chunk.Code[4] = NewInstr(OpRET, 1, 0, 0)
	val, err = vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if val.AsNumber() != -4.9 {
		t.Fatalf("expected Number(-4.9) from NEG, got %v", val)
	}

	// NOT on non-bool should fail
	chunk.Code = []Instr{
		NewInstr(OpLOADK, 0, uint16(numConst), 0),
		NewInstr(OpNOT, 1, 0, 0),
		NewInstr(OpRET, 1, 0, 0),
	}
	chunk.Prototypes[0].CodeSize = 3
	chunk.Prototypes[0].RegisterCount = 2
	if _, err := vm.Exec(chunk); err == nil {
		t.Fatalf("expected NOT on number to fail")
	}
}

func TestLoadI(t *testing.T) {
	chunk := NewChunk()
	chunk.AddInstruction(NewInstr(OpLOADI, 0, 42, 0))
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
	val, err := vm.Exec(chunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if !val.IsInt() || val.AsInt() != 42 {
		t.Fatalf("expected Int(42) from LOADI, got %v", val)
	}
}

func TestArrayAndObjectHelpers(t *testing.T) {
	vm := NewVM()

	// Array push path
	arrayChunk := NewChunk()
	arrayChunk.AddInstruction(NewInstr(OpNEWARRAY, 0, 0, 0))
	arrayChunk.AddInstruction(NewInstr(OpLOADI, 1, 7, 0))
	arrayChunk.AddInstruction(NewInstr(OpARRPUSH, 0, 1, 0))
	arrayChunk.AddInstruction(NewInstr(OpARRLEN, 2, 0, 0))
	arrayChunk.AddInstruction(NewInstr(OpRET, 2, 0, 0))
	arrayProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      5,
		RegisterCount: 3,
		ArgCount:      0,
	}
	arrayChunk.Prototypes = append(arrayChunk.Prototypes, arrayProto)

	val, err := vm.Exec(arrayChunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if val.AsInt() != 1 {
		t.Fatalf("expected array length 1 after ARRPUSH, got %v", val)
	}

	// Object helpers path
	objChunk := NewChunk()

	strObj, _, err := vm.GetHeap().AllocString("foo")
	if err != nil {
		t.Fatalf("alloc string failed: %v", err)
	}
	fieldConst := objChunk.AddConstant(NewHeapPtr(uintptr(unsafe.Pointer(strObj))))

	objChunk.AddInstruction(NewInstr(OpNEWOBJ, 0, 0, 0))
	objChunk.AddInstruction(NewInstr(OpLOADBOOL, 1, 1, 0))
	objChunk.AddInstruction(NewInstr(OpSETFIELD, 0, uint16(fieldConst), 1))
	objChunk.AddInstruction(NewInstr(OpHASFIELD, 2, 0, uint16(fieldConst)))
	objChunk.AddInstruction(NewInstr(OpDELFIELD, 3, 0, uint16(fieldConst)))
	objChunk.AddInstruction(NewInstr(OpHASFIELD, 4, 0, uint16(fieldConst)))
	objChunk.AddInstruction(NewInstr(OpRET, 4, 0, 0))
	objProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      7,
		RegisterCount: 5,
		ArgCount:      0,
	}
	objChunk.Prototypes = append(objChunk.Prototypes, objProto)

	val, err = vm.Exec(objChunk)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if !val.IsBool() || val.AsBool() {
		t.Fatalf("expected final HASFIELD to be false, got %v", val)
	}
}

// mulInts says what dividing back said, for products on either side of
// every limit it takes a short way around.
func TestMulIntsAgreesWithDividingBack(t *testing.T) {
	edges := []int64{0, 1, -1, 2, -2, 3, 1 << 15, 1<<31 - 1, 1 << 31, 1<<31 + 1, -(1 << 31), -(1 << 31) - 1, -(1 << 31) + 1,
		1 << 32, 1<<32 + 7, 1 << 40, 1<<47 - 1, -(1 << 47), MaxInt, MinInt, MaxInt / 3, MinInt / 7, 3037000499, 3037000500, -3037000500, 46341, 16777216}
	for _, x := range edges {
		for _, y := range edges {
			for _, sign := range []int64{1, -1} {
				a, b := x, y*sign
				if b < MinInt || b > MaxInt {
					continue
				}
				product := a * b
				want := !(a != 0 && product/a != b) && product >= MinInt && product <= MaxInt
				got, fits := mulInts(a, b)
				if fits != want || (fits && got != product) {
					t.Errorf("%d * %d: got %d, %v; dividing back says %v", a, b, got, fits, want)
				}
			}
		}
	}
}
