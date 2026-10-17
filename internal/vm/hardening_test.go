package vm

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// What makes the VM safe to run anything on, and to run from anywhere:
// bytecode checked before it runs, failures of the VM itself turned into
// errors, memory counted the same way in and out, and use from several
// threads.

// mainOf makes a chunk of one main function with 4 registers.
func mainOf(code ...Instr) *Chunk {
	chunk := NewChunk()
	chunk.StringPool = []string{"text", "field"}
	chunk.Constants = []Value{NewInt(0), NewInt(1), NewNumber(1.5), NewInt(99)}
	chunk.ExternNames = []string{"println"}
	chunk.Code = code
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: len(code), RegisterCount: 4}}
	return chunk
}

func TestVerifyRefusesWhatWouldReadOutside(t *testing.T) {
	ret := NewInstr(OpRET, 0, 0, 0)
	cases := []struct {
		name string
		code []Instr
		want string
	}{
		{"register of the destination", []Instr{NewInstr(OpMOV, 4, 0, 0), ret}, "register r4"},
		{"register of an operand", []Instr{NewInstr(OpADD, 0, 1, 9), ret}, "register r9"},
		{"register returned", []Instr{NewInstr(OpRET, 60000, 0, 0)}, "register r60000"},
		{"constant", []Instr{NewInstr(OpLOADK, 0, 4, 0), ret}, "constant 4 of 4"},
		{"string that is a float", []Instr{NewInstr(OpNEWSTR, 0, 2, 0), ret}, "not the number of a string"},
		{"string outside the pool", []Instr{NewInstr(OpNEWSTR, 0, 3, 0), ret}, "string 99 of 2"},
		{"name of a struct", []Instr{NewInstr(OpNEWOBJ, 0, 3, ObjectIsNamed), ret}, "string 99 of 2"},
		{"name of a field", []Instr{NewInstr(OpGETFIELD, 0, 1, 2), ret}, "not the number of a string"},
		{"field of a register outside", []Instr{NewInstr(OpSETFIELD, 0, 1, 7), ret}, "register r7"},
		{"jump past the end", []Instr{NewInstr(OpJMP, 2, 0, 0), ret}, "jump to 2"},
		{"conditional jump past the end", []Instr{NewInstr(OpJMPIF, 0, 40, 0), ret}, "jump to 40"},
		{"long jump before the start", []Instr{NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMP32, 0, 0, 0), EncodeOffset32(-10), ret}, "jump to -7"},
		{"jump into a long jump", []Instr{NewInstr(OpJMP, 2, 0, 0), NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMP32, 0, 0, 0), EncodeOffset32(0), ret}, "jump to 2"},
		{"long without a jump", []Instr{NewInstr(OpLONG, 0, 0, 0), NewInstr(OpADD, 0, 0, 0), EncodeOffset32(0), ret}, "not a long jump"},
		{"long cut short", []Instr{ret, NewInstr(OpLONG, 0, 0, 0), NewInstr(OpJMP32, 0, 0, 0)}, "cut short"},
		{"long jump without its prefix", []Instr{NewInstr(OpJMP32, 0, 0, 0), ret}, "without LONG"},
		{"handler outside", []Instr{NewInstr(OpLONG, 0, 0, 0), NewInstr(OpTRY32, 0, 0, 0), EncodeOffset32(50), ret}, "jump to 53"},
		{"arguments past the registers", []Instr{NewInstr(OpCALL, 2, 2, 0), ret}, "reaches past"},
		{"function that is not there", []Instr{NewInstr(OpNEWFUNC, 0, 5, 0), ret}, "function 5 of 1"},
		{"host function that is not there", []Instr{NewInstr(OpNEWEXFUNC, 0, 3, 0), ret}, "host function 3 of 1"},
		{"captured variable that is not there", []Instr{NewInstr(OpLDCAP, 0, 0, 0), ret}, "captured variable 0"},
		{"unknown instruction", []Instr{NewInstr(OpCode(200), 0, 0, 0), ret}, "unknown instruction 200"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunk := mainOf(c.code...)
			err := Verify(chunk)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected an error with %q, got %v", c.want, err)
			}
			// And Exec refuses it rather than run into it
			if _, err := NewVM().Exec(chunk); err == nil || !strings.Contains(err.Error(), "invalid bytecode") {
				t.Errorf("Exec ran it: %v", err)
			}
		})
	}
}

func TestVerifyRefusesFunctionsThatDoNotHold(t *testing.T) {
	cases := []struct {
		name  string
		proto FunctionPrototype
		want  string
	}{
		{"code past the chunk", FunctionPrototype{CodeStart: 1, CodeSize: 5, RegisterCount: 2}, "outside"},
		{"negative size", FunctionPrototype{CodeStart: 0, CodeSize: -1, RegisterCount: 2}, "outside"},
		{"no registers", FunctionPrototype{CodeSize: 2, RegisterCount: 0}, "registers"},
		{"too many registers", FunctionPrototype{CodeSize: 2, RegisterCount: 1 << 20}, "registers"},
		{"more arguments than registers", FunctionPrototype{CodeSize: 2, RegisterCount: 2, ArgCount: 3}, "arguments"},
		{"no instructions", FunctionPrototype{CodeSize: 0, RegisterCount: 2}, "no instructions"},
		{"runs into the next", FunctionPrototype{CodeSize: 1, RegisterCount: 2}, "does not end"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunk := mainOf(NewInstr(OpLOADI, 0, 1, 0), NewInstr(OpRET, 0, 0, 0))
			c.proto.Name = "main"
			chunk.Prototypes = []FunctionPrototype{c.proto}
			if err := Verify(chunk); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("expected an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestVerifyAcceptsWhatTheAssemblerWrites(t *testing.T) {
	// A loop with every form of jump, a handler, a call
	chunk := mainOf(
		NewInstr(OpLOADI, 1, 3, 0),   // 0
		NewInstr(OpLOADI, 2, 1, 0),   // 1
		NewInstr(OpLONG, 0, 0, 0),    // 2: handler at 12
		NewInstr(OpTRY32, 0, 0, 0),   // 3
		EncodeOffset32(7),            // 4
		NewInstr(OpSUB, 1, 1, 2),     // 5
		NewInstr(OpLOADI, 3, 0, 0),   // 6
		NewInstr(OpGT, 3, 1, 3),      // 7
		NewInstr(OpJMPIF, 3, 5, 0),   // 8: back to 5
		NewInstr(OpENDTRY, 0, 0, 0),  // 9
		NewInstr(OpJMP, 11, 0, 0),    // 10
		NewInstr(OpRET, 1, 0, 0),     // 11
		NewInstr(OpRETHROW, 0, 0, 0), // 12
	)
	if err := Verify(chunk); err != nil {
		t.Fatalf("refused: %v", err)
	}
	result, err := NewVM().Exec(chunk)
	if err != nil || !result.IsInt() || result.AsInt() != 0 {
		t.Errorf("expected 0, got %v, %v", result, err)
	}
}

// A file may say it holds more than it does. Nothing is made to hold what
// is not there.
func TestDeserializeRefusesLengthsThatLie(t *testing.T) {
	chunk := mainOf(NewInstr(OpNEWSTR, 0, 0, 0), NewInstr(OpRET, 0, 0, 0))
	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeserializeChunk(data); err != nil {
		t.Fatalf("the file as written is refused: %v", err)
	}

	// Every place where a number of items or of bytes is written: put the
	// largest number there
	for offset := 8; offset+4 <= len(data); offset++ {
		damaged := append([]byte(nil), data...)
		binary.LittleEndian.PutUint32(damaged[offset:], 0xFFFFFFF0)
		loaded, err := DeserializeChunk(damaged)
		if err != nil {
			continue
		}
		// What still loads must be refused or run without harm
		machine := NewVM()
		machine.SetOutput(io.Discard)
		machine.Exec(loaded)
	}

	// Cut anywhere, it is an error and nothing else
	for size := 0; size < len(data); size++ {
		if _, err := DeserializeChunk(data[:size]); err == nil {
			t.Errorf("a file cut at %d of %d bytes was accepted", size, len(data))
		}
	}
}

func TestDeserializeRefusesPointers(t *testing.T) {
	chunk := mainOf(NewInstr(OpLOADK, 0, 0, 0), NewInstr(OpRET, 0, 0, 0))
	chunk.Constants[0] = NewHeapPtr(0xDEADBEEF)
	data, _ := SerializeChunk(chunk)
	if _, err := DeserializeChunk(data); err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Errorf("expected the constant to be refused, got %v", err)
	}
}

// Damaged bytecode never takes the process down: it is refused, or it
// fails as a program does, or it runs. The code of a real program is
// changed at random, many times over.
func TestDamagedCodeNeverPanics(t *testing.T) {
	loop := mainOf(
		NewInstr(OpLOADI, 1, 50, 0),
		NewInstr(OpLOADI, 2, 1, 0),
		NewInstr(OpNEWARRAY, 3, 4, 0),
		NewInstr(OpARRPUSH, 3, 1, 0),
		NewInstr(OpNEWOBJ, 0, 0, 0),
		NewInstr(OpSETFIELD, 0, 1, 3),
		NewInstr(OpGETFIELD, 0, 0, 1),
		NewInstr(OpSUB, 1, 1, 2),
		NewInstr(OpLOADI, 0, 0, 0),
		NewInstr(OpGT, 0, 1, 0),
		NewInstr(OpJMPIF, 0, 3, 0),
		NewInstr(OpNEWEXFUNC, 0, 0, 0),
		NewInstr(OpMOV, 1, 3, 0),
		NewInstr(OpCALL, 0, 1, 0),
		NewInstr(OpRET, 1, 0, 0),
	)
	data, _ := SerializeChunk(loop)
	codeStart := 8 + 4 + len(loop.Constants)*8 + 4
	codeEnd := codeStart + len(loop.Code)*instrSize

	random := rand.New(rand.NewSource(1))
	refused, failed, ran := 0, 0, 0
	for round := 0; round < 3000; round++ {
		damaged := append([]byte(nil), data...)
		for changes := 1 + random.Intn(3); changes > 0; changes-- {
			damaged[codeStart+random.Intn(codeEnd-codeStart)] = byte(random.Intn(256))
		}
		chunk, err := DeserializeChunk(damaged)
		if err != nil {
			refused++
			continue
		}
		machine := NewVMWithHeapSize(1 << 20)
		machine.SetOutput(io.Discard)
		// A changed jump may make a loop that never ends
		stop := time.AfterFunc(200*time.Millisecond, machine.Interrupt)
		_, err = machine.Exec(chunk)
		stop.Stop()
		switch failure, _ := err.(*RuntimeError); {
		case err == nil:
			ran++
		case failure != nil && failure.Kind == KindInternal:
			t.Fatalf("round %d: the VM itself failed: %v", round, err)
		case strings.Contains(err.Error(), "invalid bytecode"):
			refused++
		default:
			failed++
		}
	}
	if refused == 0 || failed == 0 || ran == 0 {
		t.Errorf("expected some of each: %d refused, %d failed, %d ran", refused, failed, ran)
	}
}

// A host function that goes wrong ends the program with an error. The
// process, and the VM, go on.
func TestAPanicInAHostFunctionIsAnError(t *testing.T) {
	machine := NewVM()
	machine.RegisterExtern("println", func(vm *VM, args []Value) (Value, error) {
		var nothing []Value
		return nothing[3], nil
	})
	call := mainOf(
		NewInstr(OpNEWEXFUNC, 1, 0, 0),
		NewInstr(OpCALL, 1, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
	)
	_, err := machine.Exec(call)
	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindInternal || failure.Code() != "R0010" || !strings.Contains(failure.Message, "index out of range") {
		t.Fatalf("expected an internal error, got %v", err)
	}
	if failure.Suggestion() == "" {
		t.Error("an internal error says what to do about it")
	}

	// The same VM runs the next program
	result, err := machine.Exec(mainOf(NewInstr(OpLOADI, 0, 7, 0), NewInstr(OpRET, 0, 0, 0)))
	if err != nil || result.AsInt() != 7 {
		t.Errorf("after the failure: %v, %v", result, err)
	}
}

// What is counted when an object is made is taken off when it is freed,
// whatever grew it in between: after a collection with nothing alive, the
// count is back where it was.
func TestMemoryCountedInIsCountedOut(t *testing.T) {
	machine := NewVM()
	heap := machine.heap
	chunk := &Chunk{Prototypes: []FunctionPrototype{{Name: "f", UpvalueCount: 3}}}
	machine.CollectGarbage()
	before := heap.BytesAllocated()

	for round := 0; round < 200; round++ {
		heap.AllocString(strings.Repeat("x", round))
		closure, _, _ := heap.AllocFunction(chunk, 0, 3)
		upvalue, _, _ := heap.AllocUpvalue(newRegStack(1), 0)
		closure.SetUpvalue(0, upvalue)
		heap.AllocExternFunction("std.some.function", nil, -1)

		arr, _, _ := heap.AllocArray(round % 5)
		for i := 0; i < round; i++ {
			if err := machine.appendToArray(arr, NewInt(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		obj, _, _ := heap.AllocObject()
		for i := 0; i < round%20; i++ {
			if err := machine.setField(obj, fmt.Sprint("field", i), NewInt(int64(i))); err != nil {
				t.Fatal(err)
			}
		}
		obj.Delete("field3")
	}
	if heap.BytesAllocated() <= before {
		t.Fatal("nothing was counted")
	}

	machine.CollectGarbage()
	if after := heap.BytesAllocated(); after != before {
		t.Errorf("%d bytes are counted for objects that are gone", after-before)
	}
	if heap.objects != nil && countObjects(heap) != 0 {
		t.Errorf("%d objects left", countObjects(heap))
	}
}

// An array that could never fit is refused before the memory is asked of
// the machine.
func TestAnArrayLargerThanTheHeapIsRefused(t *testing.T) {
	heap := NewHeap(1 << 20)
	for _, capacity := range []int{1 << 40, 1 << 62, -1} {
		if _, _, err := heap.AllocArray(capacity); err == nil || !strings.Contains(err.Error(), "out of memory") {
			t.Errorf("capacity %d: expected out of memory, got %v", capacity, err)
		}
	}
	if heap.BytesAllocated() != 0 {
		t.Errorf("%d bytes counted for arrays that were not made", heap.BytesAllocated())
	}
}

// Marking does not go deeper for a longer list: two million nodes, each
// holding the next, are marked without two million calls inside each other.
func TestMarkingALongListDoesNotRecurse(t *testing.T) {
	machine := NewVMWithHeapSize(1 << 30)
	const length = 2_000_000

	next := NewNull()
	for i := 0; i < length; i++ {
		node, _, err := machine.heap.AllocObject()
		if err != nil {
			t.Fatal(err)
		}
		node.Set("next", next)
		next = valueOf(&node.header)
	}
	machine.regs = []Value{next}

	machine.CollectGarbage()
	if got := countObjects(machine.heap); got != length {
		t.Fatalf("expected the %d nodes to be kept, got %d", length, got)
	}
	if len(machine.gray) != 0 {
		t.Errorf("%d objects left to look into", len(machine.gray))
	}

	machine.regs = nil
	machine.CollectGarbage()
	if got := countObjects(machine.heap); got != 0 {
		t.Errorf("expected nothing to be kept, got %d", got)
	}
}

func TestObjectsOfOneStructShareAShape(t *testing.T) {
	heap := NewHeap(0)
	make := func(keys ...string) *ObjectObj {
		obj, _, _ := heap.AllocObject()
		for i, key := range keys {
			obj.Set(key, NewInt(int64(i)))
		}
		return obj
	}
	a, b := make("x", "y", "z"), make("x", "y", "z")
	other := make("y", "x", "z")
	if a.shape != b.shape {
		t.Error("the same fields in the same order give two shapes")
	}
	if a.shape == other.shape {
		t.Error("another order gives the same shape")
	}
	if got := strings.Join(other.Keys(), ","); got != "y,x,z" {
		t.Errorf("fields are listed in the order they were added, got %s", got)
	}

	// Setting a field that is there changes nothing but its value
	shape := a.shape
	a.Set("y", NewInt(42))
	if v, _ := a.Get("y"); a.shape != shape || v.AsInt() != 42 || a.Len() != 3 {
		t.Errorf("after setting y: %v, %d fields", v, a.Len())
	}
	if _, ok := a.Get("w"); ok {
		t.Error("a field that was never set is found")
	}

	// Taking one away leaves an object like one made without it
	if !a.Delete("y") || a.Delete("y") {
		t.Error("y is deleted once")
	}
	if a.shape != make("x", "z").shape {
		t.Error("an object without y is not shaped as one made without it")
	}
	if x, _ := a.Get("x"); x.AsInt() != 0 {
		t.Errorf("x moved: %v", x)
	}
	if z, _ := a.Get("z"); z.AsInt() != 2 {
		t.Errorf("z moved: %v", z)
	}
	if fields := a.Fields(); len(fields) != 2 || fields["z"].AsInt() != 2 {
		t.Errorf("fields: %v", fields)
	}
	// The other object of the shape is as it was
	if y, _ := b.Get("y"); b.Len() != 3 || y.AsInt() != 1 {
		t.Errorf("b changed: %d fields, y = %v", b.Len(), y)
	}
}

// Positions in a text are characters, found without going through the
// text from its start, and the same ones a conversion to runes gives.
func TestTextPositions(t *testing.T) {
	texts := []string{
		"",
		"a",
		"plain ascii text",
		"é",
		"naïve café",
		"日本語のテキスト",
		"mixed: aé日🙂z",
		strings.Repeat("0123456789", 50),
		strings.Repeat("aé日🙂", 40) + "end",
		strings.Repeat("x", runeStride) + "é" + strings.Repeat("y", runeStride-1) + "日",
	}
	heap := NewHeap(0)
	for _, text := range texts {
		runes := []rune(text)
		obj, _, _ := heap.AllocString(text)
		if obj.RuneLen() != len(runes) {
			t.Errorf("%q: length %d, expected %d", text, obj.RuneLen(), len(runes))
			continue
		}
		for i, r := range runes {
			if got := obj.RuneAt(i); got != string(r) {
				t.Errorf("%q[%d]: %q, expected %q", text, i, got, string(r))
			}
		}
		for from := 0; from <= len(runes); from += 1 + len(runes)/7 {
			for to := from; to <= len(runes); to += 1 + len(runes)/5 {
				if got := obj.Slice(from, to); got != string(runes[from:to]) {
					t.Errorf("%q[%d:%d]: %q, expected %q", text, from, to, got, string(runes[from:to]))
				}
			}
		}
		if got := obj.Slice(0, len(runes)); got != text {
			t.Errorf("%q: the whole text is %q", text, got)
		}
	}
}

func TestIndexingATextGivesOneTextPerCharacter(t *testing.T) {
	// r1 = "text"; r0 = r1[0]; r2 = r1[3]; r0 = (r0 == r2)
	machine := NewVM()
	chunk := mainOf(
		NewInstr(OpNEWSTR, 1, 0, 0),
		NewInstr(OpLOADI, 2, 0, 0),
		NewInstr(OpGETINDEX, 0, 1, 2),
		NewInstr(OpLOADI, 2, 3, 0),
		NewInstr(OpGETINDEX, 2, 1, 2),
		NewInstr(OpGC, 0, 0, 0),
		NewInstr(OpEQ, 3, 0, 2),
		NewInstr(OpRET, 0, 0, 0),
	)
	result, err := machine.Exec(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := stringValue(result); s != "t" {
		t.Errorf("expected t, got %s", FormatValue(result))
	}
	// Both were the letter t: one object, kept through the collection
	if machine.charStrings['t'] != result {
		t.Error("the text of a character is not the one kept for it")
	}
	machine.CollectGarbage()
	if s, _ := stringValue(machine.charStrings['t']); s != "t" {
		t.Error("the text kept for a character was collected")
	}
}

func TestShiftsAndConversionsThatDoNotFit(t *testing.T) {
	run := func(a, b Value, op OpCode) (Value, error) {
		chunk := mainOf(
			NewInstr(OpLOADK, 1, 0, 0),
			NewInstr(OpLOADK, 2, 1, 0),
			NewInstr(op, 0, 1, 2),
			NewInstr(OpRET, 0, 0, 0),
		)
		chunk.Constants = []Value{a, b}
		return NewVM().Exec(chunk)
	}
	overflow := func(name string, a, b Value, op OpCode) {
		t.Helper()
		_, err := run(a, b, op)
		failure, ok := err.(*RuntimeError)
		if !ok || failure.Kind != KindArithmetic {
			t.Errorf("%s: expected an arithmetic error, got %v", name, err)
		}
	}
	result := func(name string, a, b Value, op OpCode, want int64) {
		t.Helper()
		got, err := run(a, b, op)
		if err != nil || !got.IsInt() || got.AsInt() != want {
			t.Errorf("%s: expected %d, got %v, %v", name, want, got, err)
		}
	}

	result("1 << 10", NewInt(1), NewInt(10), OpSHL, 1024)
	result("-3 << 4", NewInt(-3), NewInt(4), OpSHL, -48)
	result("1 << 46", NewInt(1), NewInt(46), OpSHL, 1<<46)
	result("0 << 1000", NewInt(0), NewInt(1000), OpSHL, 0)
	overflow("1 << 47", NewInt(1), NewInt(47), OpSHL)
	overflow("1 << 64", NewInt(1), NewInt(64), OpSHL)
	overflow("max << 1", NewInt(MaxInt), NewInt(1), OpSHL)
	result("-1 >> 200", NewInt(-1), NewInt(200), OpSHR, -1)
	result("1024 >> 200", NewInt(1024), NewInt(200), OpSHR, 0)
	result("1024 >> 3", NewInt(1024), NewInt(3), OpSHR, 128)

	convert := func(f float64) (Value, error) {
		chunk := mainOf(NewInstr(OpLOADK, 1, 0, 0), NewInstr(OpTOINT, 0, 1, 0), NewInstr(OpRET, 0, 0, 0))
		chunk.Constants = []Value{NewNumber(f)}
		return NewVM().Exec(chunk)
	}
	nan := NewNumber(0).AsNumber()
	nan = nan / nan
	for _, f := range []float64{nan, 1e300, -1e300, float64(MaxInt) * 2, float64(MinInt) * 2} {
		if _, err := convert(f); err == nil || !strings.Contains(err.Error(), "integer overflow") {
			t.Errorf("TOINT of %v: expected an overflow, got %v", f, err)
		}
	}
	for f, want := range map[float64]int64{3.99: 3, -3.99: -3, 0: 0, 1e12: 1e12} {
		if got, err := convert(f); err != nil || got.AsInt() != want {
			t.Errorf("TOINT of %v: expected %d, got %v, %v", f, want, got, err)
		}
	}
}

// spinning is a program that never ends by itself.
func spinning() *Chunk {
	return mainOf(
		NewInstr(OpLOADI, 1, 1, 0),
		NewInstr(OpADD, 2, 1, 1),
		NewInstr(OpJMP, 1, 0, 0),
	)
}

func TestInterruptStopsAProgramFromAnotherThread(t *testing.T) {
	machine := NewVM()
	done := make(chan error, 1)
	go func() {
		_, err := machine.Exec(spinning())
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	machine.Interrupt()
	machine.Interrupt() // Twice is once

	select {
	case err := <-done:
		failure, ok := err.(*RuntimeError)
		if !ok || failure.Kind != KindInterrupted || !IsInterrupted(err) || failure.Code() != "R0011" {
			t.Fatalf("expected an interruption, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the program went on")
	}

	// It was that program that was stopped, not the next
	result, err := machine.Exec(mainOf(NewInstr(OpLOADI, 0, 9, 0), NewInstr(OpRET, 0, 0, 0)))
	if err != nil || result.AsInt() != 9 {
		t.Errorf("the next program: %v, %v", result, err)
	}

	// Asked before a program starts, it stops that one
	machine.Interrupt()
	if _, err := machine.Exec(spinning()); !IsInterrupted(err) {
		t.Errorf("expected the program to be stopped as it starts, got %v", err)
	}
}

// One VM, several threads: the programs run one after the other, each as
// if it were alone.
func TestExecFromSeveralThreads(t *testing.T) {
	machine := NewVM()
	machine.SetOutput(io.Discard)

	// Sums the numbers below n, in an array it fills first
	program := func(n int) *Chunk {
		return mainOf(
			NewInstr(OpNEWARRAY, 3, 0, 0),      // 0
			NewInstr(OpLOADI, 1, 0, 0),         // 1: i = 0
			NewInstr(OpLOADI, 2, uint16(n), 0), // 2
			NewInstr(OpLT, 0, 1, 2),            // 3
			NewInstr(OpJMPIFNOT, 0, 9, 0),      // 4
			NewInstr(OpARRPUSH, 3, 1, 0),       // 5
			NewInstr(OpLOADI, 0, 1, 0),         // 6
			NewInstr(OpADD, 1, 1, 0),           // 7
			NewInstr(OpJMP, 2, 0, 0),           // 8
			NewInstr(OpLOADI, 2, 0, 0),         // 9: sum = 0
			NewInstr(OpLOADI, 1, 0, 0),         // 10: i = 0
			NewInstr(OpARRLEN, 0, 3, 0),        // 11
			NewInstr(OpLT, 0, 1, 0),            // 12
			NewInstr(OpJMPIFNOT, 0, 19, 0),     // 13
			NewInstr(OpGETINDEX, 0, 3, 1),      // 14
			NewInstr(OpADD, 2, 2, 0),           // 15
			NewInstr(OpLOADI, 0, 1, 0),         // 16
			NewInstr(OpADD, 1, 1, 0),           // 17
			NewInstr(OpJMP, 11, 0, 0),          // 18
			NewInstr(OpRET, 2, 0, 0),           // 19
		)
	}

	var wg sync.WaitGroup
	for thread := 0; thread < 8; thread++ {
		wg.Add(1)
		go func(thread int) {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				n := 100 + thread*37 + round
				result, err := machine.Exec(program(n))
				if err != nil || !result.IsInt() || result.AsInt() != int64(n*(n-1)/2) {
					t.Errorf("thread %d: expected %d, got %v, %v", thread, n*(n-1)/2, result, err)
				}
			}
		}(thread)
	}
	wg.Wait()
}

// Several VMs, one per thread: they share nothing that changes.
func TestVMsSideBySide(t *testing.T) {
	var wg sync.WaitGroup
	for thread := 0; thread < 8; thread++ {
		wg.Add(1)
		go func(thread int) {
			defer wg.Done()
			machine := NewVMWithHeapSize(4 << 20)
			machine.SetOutput(io.Discard)
			// Makes texts in a loop, so that the collector runs
			chunk := mainOf(
				NewInstr(OpLOADI, 1, 20000, 0),
				NewInstr(OpNEWSTR, 2, 0, 0),
				NewInstr(OpADD, 2, 2, 2),
				NewInstr(OpLOADI, 3, 1, 0),
				NewInstr(OpSUB, 1, 1, 3),
				NewInstr(OpLOADI, 3, 0, 0),
				NewInstr(OpGT, 3, 1, 3),
				NewInstr(OpJMPIF, 3, 1, 0),
				NewInstr(OpRET, 2, 0, 0),
			)
			result, err := machine.Exec(chunk)
			if s, _ := stringValue(result); err != nil || s != "texttext" {
				t.Errorf("thread %d: %v, %v", thread, FormatValue(result), err)
			}
			if machine.Collections() == 0 {
				t.Errorf("thread %d: the collector never ran", thread)
			}
		}(thread)
	}
	wg.Wait()
}

// A call takes its registers from the stack and gives them back: calling
// allocates nothing, however often.
func TestCallsDoNotAllocate(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		// main: calls f 1000 times
		NewInstr(OpLOADI, 1, 1000, 0), // 0
		NewInstr(OpNEWFUNC, 2, 1, 0),  // 1
		NewInstr(OpMOV, 3, 1, 0),      // 2
		NewInstr(OpCALL, 2, 1, 0),     // 3
		NewInstr(OpLOADI, 3, 1, 0),    // 4
		NewInstr(OpSUB, 1, 1, 3),      // 5
		NewInstr(OpLOADI, 3, 0, 0),    // 6
		NewInstr(OpGT, 3, 1, 3),       // 7
		NewInstr(OpJMPIF, 3, 1, 0),    // 8
		NewInstr(OpRET, 0, 0, 0),      // 9
		// f(n): returns n + n
		NewInstr(OpADD, 1, 0, 0), // 10
		NewInstr(OpRET, 1, 0, 0), // 11
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 10, RegisterCount: 4},
		{Name: "f", CodeStart: 10, CodeSize: 2, RegisterCount: 2, ArgCount: 1},
	}
	machine := NewVM()
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatal(err)
	}
	perRun := testing.AllocsPerRun(20, func() {
		if result, err := machine.Exec(chunk); err != nil || result.AsInt() != 2 {
			t.Fatalf("%v, %v", result, err)
		}
	})
	// What starting a program takes, and nothing per call
	if perRun > 40 {
		t.Errorf("%v allocations for 1000 calls", perRun)
	}
}

// The stack moves when it grows. A variable a closure captured is found
// where it went, and a call that fails deep down leaves the callers'
// registers as they were.
func TestTheStackGrowsUnderOpenUpvalues(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		// main: x = 5; c = closure over x; r = deep(300); return c() + r
		NewInstr(OpLOADI, 1, 5, 0),    // 0: x in r1
		NewInstr(OpNEWFUNC, 2, 1, 0),  // 1: c
		NewInstr(OpCAPLOCAL, 2, 0, 1), // 2
		NewInstr(OpNEWFUNC, 3, 2, 0),  // 3: deep
		NewInstr(OpLOADI, 4, 300, 0),  // 4
		NewInstr(OpCALL, 3, 1, 0),     // 5: r0 = deep(300)
		NewInstr(OpMOV, 5, 0, 0),      // 6
		NewInstr(OpLOADI, 1, 7, 0),    // 7: x = 7, after the stack moved
		NewInstr(OpMOV, 3, 2, 0),      // 8
		NewInstr(OpCALL, 3, 0, 0),     // 9: r0 = c()
		NewInstr(OpADD, 0, 0, 5),      // 10
		NewInstr(OpRET, 0, 0, 0),      // 11
		// c: returns x
		NewInstr(OpLDCAP, 0, 0, 0), // 12
		NewInstr(OpRET, 0, 0, 0),   // 13
		// deep(n): n == 0 ? 0 : deep(n - 1) + 1, with registers to spare
		NewInstr(OpMOV, 1, 0, 0),       // 14
		NewInstr(OpLOADI, 2, 0, 0),     // 15
		NewInstr(OpEQ, 2, 1, 2),        // 16
		NewInstr(OpJMPIFNOT, 2, 19, 0), // 17
		NewInstr(OpRET, 1, 0, 0),       // 18
		NewInstr(OpNEWFUNC, 60, 2, 0),  // 19
		NewInstr(OpLOADI, 2, 1, 0),     // 20
		NewInstr(OpSUB, 61, 1, 2),      // 21
		NewInstr(OpCALL, 60, 1, 0),     // 22
		NewInstr(OpADD, 0, 0, 2),       // 23
		NewInstr(OpRET, 0, 0, 0),       // 24
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 12, RegisterCount: 6},
		{Name: "c", CodeStart: 12, CodeSize: 2, RegisterCount: 1, UpvalueCount: 1},
		{Name: "deep", CodeStart: 14, CodeSize: 11, RegisterCount: 64, ArgCount: 1},
	}
	machine := NewVM()
	result, err := machine.Exec(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if len(machine.stk.vals) <= 1024 {
		t.Fatalf("the stack did not grow: %d registers", len(machine.stk.vals))
	}
	// c() sees the 7 written after the stack moved; deep(300) is 300
	if !result.IsInt() || result.AsInt() != 307 {
		t.Errorf("expected 307, got %v", result)
	}
	if len(machine.stk.open) != 0 {
		t.Errorf("%d upvalues left open after main returned", len(machine.stk.open))
	}
}

// A handler runs when a call it covers fails, further down: the calls
// made since are dropped, and the failure passed on is the one that
// happened.
func TestAHandlerRunsThenPassesTheFailureOn(t *testing.T) {
	var out strings.Builder
	chunk := NewChunk()
	chunk.StringPool = []string{"cleanup"}
	chunk.Constants = []Value{NewInt(0)}
	chunk.ExternNames = []string{"println"}
	chunk.Code = []Instr{
		// main
		NewInstr(OpLONG, 0, 0, 0),      // 0: handler at 8
		NewInstr(OpTRY32, 0, 0, 0),     // 1
		EncodeOffset32(5),              // 2
		NewInstr(OpNEWFUNC, 1, 1, 0),   // 3
		NewInstr(OpLOADI, 2, 3, 0),     // 4
		NewInstr(OpCALL, 1, 1, 0),      // 5: fail(3)
		NewInstr(OpENDTRY, 0, 0, 0),    // 6
		NewInstr(OpRET, 0, 0, 0),       // 7
		NewInstr(OpNEWEXFUNC, 1, 0, 0), // 8: the handler
		NewInstr(OpNEWSTR, 2, 0, 0),    // 9
		NewInstr(OpCALL, 1, 1, 0),      // 10: println("cleanup")
		NewInstr(OpRETHROW, 0, 0, 0),   // 11
		// fail(n): n == 0 ? 1 / 0 : fail(n - 1)
		NewInstr(OpMOV, 1, 0, 0),       // 12
		NewInstr(OpLOADI, 2, 0, 0),     // 13
		NewInstr(OpEQ, 3, 1, 2),        // 14
		NewInstr(OpJMPIFNOT, 3, 18, 0), // 15
		NewInstr(OpDIV, 0, 1, 2),       // 16: 0 / 0
		NewInstr(OpRET, 0, 0, 0),       // 17
		NewInstr(OpNEWFUNC, 4, 1, 0),   // 18
		NewInstr(OpLOADI, 2, 1, 0),     // 19
		NewInstr(OpSUB, 5, 1, 2),       // 20
		NewInstr(OpCALL, 4, 1, 0),      // 21
		NewInstr(OpRET, 0, 0, 0),       // 22
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 12, RegisterCount: 3},
		{Name: "fail", CodeStart: 12, CodeSize: 11, RegisterCount: 6, ArgCount: 1},
	}
	machine := NewVM()
	machine.SetOutput(&out)
	_, err := machine.Exec(chunk)

	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindArithmetic || !strings.Contains(failure.Message, "division by zero") {
		t.Fatalf("expected the division to be what failed, got %v", err)
	}
	if out.String() != "cleanup\n" {
		t.Errorf("the handler wrote %q", out.String())
	}
	// Where it happened: four calls of fail below main
	if len(failure.Stack) != 5 || failure.Stack[0].Function != "fail" || failure.Stack[4].Function != "main" {
		t.Errorf("the stack is not the one of the failure: %+v", failure.Stack)
	}
	if machine.frameCount != 0 || len(machine.handlers) != 0 || len(machine.pending) != 0 {
		t.Errorf("left behind: %d frames, %d handlers, %d failures", machine.frameCount, len(machine.handlers), len(machine.pending))
	}

	// An interruption is not a failure: no handler runs for it
	out.Reset()
	chunk.Code[16] = NewInstr(OpJMP, 16, 0, 0)
	stop := time.AfterFunc(20*time.Millisecond, machine.Interrupt)
	defer stop.Stop()
	if _, err := machine.Exec(chunk); !IsInterrupted(err) || out.String() != "" {
		t.Errorf("expected an interruption and no cleanup, got %v, %q", err, out.String())
	}
}

var _ = unsafe.Pointer(nil)

// A value that holds itself many times over is written to a bounded
// length, in a bounded time: depth alone bounds neither.
func TestFormattingIsBounded(t *testing.T) {
	heap := NewHeap(0)
	nest, _, _ := heap.AllocArray(0)
	self := valueOf(&nest.header)
	for i := 0; i < 60; i++ {
		nest.Append(self)
	}

	started := time.Now()
	text := FormatValue(self)
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %v", took)
	}
	if len(text) > 2*maxFormatLength {
		t.Errorf("%d bytes written, with room for %d", len(text), maxFormatLength)
	}
	if !strings.Contains(text, elided) || !strings.HasPrefix(text, "[[[") {
		t.Errorf("expected nested arrays cut short, got %.80s...", text)
	}

	// What fits is written whole, as before
	flat, _, _ := heap.AllocArray(0)
	for i := 0; i < 1000; i++ {
		flat.Append(NewInt(int64(i)))
	}
	if text := FormatValue(valueOf(&flat.header)); strings.Contains(text, elided) || !strings.HasSuffix(text, "998, 999]") {
		t.Errorf("an array of 1000 numbers was cut short: ...%s", text[len(text)-30:])
	}
}

// --- How fast ---
//
// go test ./internal/vm -bench . -run '^$'

// benchFib is fib(n) by recursion: calls, comparisons, arithmetic.
func benchFib(n uint16) *Chunk {
	chunk := NewChunk()
	chunk.Code = []Instr{
		// main
		NewInstr(OpNEWFUNC, 1, 1, 0), // 0
		NewInstr(OpLOADI, 2, n, 0),   // 1
		NewInstr(OpCALL, 1, 1, 0),    // 2
		NewInstr(OpRET, 0, 0, 0),     // 3
		// fib(n)
		NewInstr(OpMOV, 1, 0, 0),      // 4
		NewInstr(OpLOADI, 2, 2, 0),    // 5
		NewInstr(OpLT, 2, 1, 2),       // 6
		NewInstr(OpJMPIFNOT, 2, 9, 0), // 7
		NewInstr(OpRET, 1, 0, 0),      // 8
		NewInstr(OpNEWFUNC, 4, 1, 0),  // 9
		NewInstr(OpLOADI, 2, 1, 0),    // 10
		NewInstr(OpSUB, 5, 1, 2),      // 11
		NewInstr(OpCALL, 4, 1, 0),     // 12
		NewInstr(OpMOV, 3, 0, 0),      // 13
		NewInstr(OpNEWFUNC, 4, 1, 0),  // 14
		NewInstr(OpLOADI, 2, 2, 0),    // 15
		NewInstr(OpSUB, 5, 1, 2),      // 16
		NewInstr(OpCALL, 4, 1, 0),     // 17
		NewInstr(OpADD, 0, 3, 0),      // 18
		NewInstr(OpRET, 0, 0, 0),      // 19
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 4, RegisterCount: 3},
		{Name: "fib", CodeStart: 4, CodeSize: 16, RegisterCount: 6, ArgCount: 1},
	}
	return chunk
}

func TestBenchmarkProgramsAreRight(t *testing.T) {
	result, err := NewVM().Exec(benchFib(20))
	if err != nil || result.AsInt() != 6765 {
		t.Errorf("fib(20): %v, %v", result, err)
	}
	result, err = NewVM().Exec(benchFields(100))
	if err != nil || result.AsInt() != 100*3 {
		t.Errorf("fields: %v, %v", result, err)
	}
}

func BenchmarkCalls(b *testing.B) {
	chunk := benchFib(25)
	machine := NewVM()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := machine.Exec(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// benchFields makes an object with two fields n times over and adds the
// fields up: allocation, fields written and read, collection.
func benchFields(n uint16) *Chunk {
	chunk := NewChunk()
	chunk.StringPool = []string{"x", "y"}
	chunk.Constants = []Value{NewInt(0), NewInt(1)}
	chunk.Code = []Instr{
		NewInstr(OpLOADI, 1, n, 0),    // 0: left
		NewInstr(OpLOADI, 2, 0, 0),    // 1: sum
		NewInstr(OpLOADI, 5, 1, 0),    // 2: one
		NewInstr(OpLOADI, 6, 2, 0),    // 3: two
		NewInstr(OpNEWOBJ, 3, 0, 0),   // 4
		NewInstr(OpSETFIELD, 3, 0, 5), // 5: .x = 1
		NewInstr(OpSETFIELD, 3, 1, 6), // 6: .y = 2
		NewInstr(OpGETFIELD, 4, 3, 0), // 7
		NewInstr(OpADD, 2, 2, 4),      // 8
		NewInstr(OpGETFIELD, 4, 3, 1), // 9
		NewInstr(OpADD, 2, 2, 4),      // 10
		NewInstr(OpSUB, 1, 1, 5),      // 11
		NewInstr(OpLOADI, 4, 0, 0),    // 12
		NewInstr(OpGT, 4, 1, 4),       // 13
		NewInstr(OpJMPIF, 4, 4, 0),    // 14
		NewInstr(OpRET, 2, 0, 0),      // 15
	}
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: 16, RegisterCount: 7}}
	return chunk
}

func BenchmarkFields(b *testing.B) {
	chunk := benchFields(50000)
	machine := NewVM()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := machine.Exec(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// The length instruction is for arrays and texts: it does not read a field
// called "length" of an object, which is a field like any other.
func TestLengthIsNotAField(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		NewInstr(OpNEWOBJ, 0, 0, 0),
		NewInstr(OpARRLEN, 1, 0, 0),
		NewInstr(OpRET, 1, 0, 0),
	}
	addMainProto(chunk, 3, 0)
	_, err := NewVM().Exec(chunk)
	if err == nil || !strings.Contains(err.Error(), "ARRLEN") {
		t.Errorf("expected the length of an object to be refused, got %v", err)
	}
}

// ONCE runs the function of a constant the first time, and hands out what
// it returned every time after that, frozen.
func TestOnceRunsItsFunctionOnce(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		// main: asks for the constant three times, returns the last
		NewInstr(OpONCE, 1, 1, 0), // 0
		NewInstr(OpONCE, 2, 1, 0), // 1
		NewInstr(OpONCE, 0, 1, 0), // 2
		NewInstr(OpRET, 0, 0, 0),  // 3
		// maker: a new array holding a new array
		NewInstr(OpNEWARRAY, 0, 4, 0), // 4
		NewInstr(OpNEWARRAY, 1, 4, 0), // 5
		NewInstr(OpARRPUSH, 0, 1, 0),  // 6
		NewInstr(OpRET, 0, 0, 0),      // 7
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 4, RegisterCount: 3},
		{Name: "__const.TABLE", CodeStart: 4, CodeSize: 4, RegisterCount: 2},
	}
	machine := NewVM()
	result, err := machine.Exec(chunk)
	if err != nil {
		t.Fatal(err)
	}
	outer := header(result)
	if outer == nil || outer.Type != ObjTypeArray || !outer.Frozen {
		t.Fatalf("expected a frozen array, got %s", result.String())
	}
	inner := (*ArrayObj)(unsafe.Pointer(outer)).elements[0]
	if !frozen(inner) {
		t.Errorf("expected what the constant holds to be frozen too")
	}

	// Asked twice in one run: the very same value, not another like it
	chunk.Code[1] = NewInstr(OpONCE, 2, 1, 0)
	chunk.Code[2] = NewInstr(OpEQ, 0, 1, 2)
	same, err := NewVM().Exec(chunk)
	if err != nil || same != NewBool(true) {
		t.Errorf("expected the same value both times, got %s, %v", same.String(), err)
	}
}

func TestFrozenValuesRefuseEveryWrite(t *testing.T) {
	writes := map[string][]Instr{
		"SETELEM":  {NewInstr(OpLOADI, 1, 0, 0), NewInstr(OpSETELEM, 0, 1, 1)},
		"SETINDEX": {NewInstr(OpLOADI, 1, 0, 0), NewInstr(OpSETINDEX, 0, 1, 1)},
		"ARRPUSH":  {NewInstr(OpARRPUSH, 0, 0, 0)},
	}
	for name, write := range writes {
		chunk := NewChunk()
		chunk.Code = append([]Instr{NewInstr(OpONCE, 0, 1, 0)}, write...)
		chunk.Code = append(chunk.Code, NewInstr(OpRET, 0, 0, 0))
		maker := len(chunk.Code)
		chunk.Code = append(chunk.Code,
			NewInstr(OpNEWARRAY, 0, 4, 0),
			NewInstr(OpLOADI, 1, 7, 0),
			NewInstr(OpARRPUSH, 0, 1, 0),
			NewInstr(OpRET, 0, 0, 0))
		chunk.Prototypes = []FunctionPrototype{
			{Name: "main", CodeStart: 0, CodeSize: maker, RegisterCount: 3},
			{Name: "__const.XS", CodeStart: maker, CodeSize: 4, RegisterCount: 2},
		}
		_, err := NewVM().Exec(chunk)
		if err == nil || !strings.Contains(err.Error(), "cannot change what a constant holds") {
			t.Errorf("%s: expected the write to be refused, got %v", name, err)
		}
	}
}

// A chunk is refused when ONCE names a function that is not one of a
// constant: none at all, or one that takes arguments.
func TestOnceIsVerified(t *testing.T) {
	for name, protos := range map[string][]FunctionPrototype{
		"no such function":           {{Name: "main", CodeStart: 0, CodeSize: 2, RegisterCount: 2}},
		"a function of one argument": {{Name: "main", CodeStart: 0, CodeSize: 2, RegisterCount: 2}, {Name: "f", CodeStart: 1, CodeSize: 1, RegisterCount: 2, ArgCount: 1}},
	} {
		chunk := NewChunk()
		chunk.Code = []Instr{NewInstr(OpONCE, 0, 1, 0), NewInstr(OpRET, 0, 0, 0)}
		chunk.Prototypes = protos
		if err := Verify(chunk); err == nil {
			t.Errorf("%s: expected the chunk to be refused", name)
		}
	}
}
