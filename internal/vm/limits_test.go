package vm

import (
	"strings"
	"testing"
	"unsafe"
)

// chunkOf builds a chunk from named functions given in order; the first is
// main. Each entry is the prototype and its code.
func chunkOf(externs []string, constants []Value, funcs ...struct {
	proto FunctionPrototype
	code  []Instr
}) *Chunk {
	chunk := NewChunk()
	chunk.ExternNames = externs
	chunk.Constants = constants
	for _, fn := range funcs {
		fn.proto.CodeStart = len(chunk.Code)
		fn.proto.CodeSize = len(fn.code)
		chunk.Code = append(chunk.Code, fn.code...)
		chunk.Prototypes = append(chunk.Prototypes, fn.proto)
	}
	return chunk
}

type fnDef = struct {
	proto FunctionPrototype
	code  []Instr
}

func TestFunctionObjectsAreReused(t *testing.T) {
	// main: r1 = first NEWFUNC/NEWEXFUNC, r2 = second, return r1 == r2
	same := func(first, second Instr) bool {
		chunk := chunkOf([]string{"print", "println"}, nil,
			fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
				first, second, NewInstr(OpEQ, 3, 1, 2), NewInstr(OpRET, 3, 0, 0),
			}},
			fnDef{FunctionPrototype{Name: "plain", RegisterCount: 1}, []Instr{NewInstr(OpRET, 0, 0, 0)}},
			fnDef{FunctionPrototype{Name: "other", RegisterCount: 1}, []Instr{NewInstr(OpRET, 0, 0, 0)}},
			fnDef{FunctionPrototype{Name: "closure", RegisterCount: 1, UpvalueCount: 1}, []Instr{NewInstr(OpRET, 0, 0, 0)}},
		)
		result, err := NewVM().Exec(chunk)
		if err != nil {
			t.Fatalf("execution failed: %v", err)
		}
		return result.AsBool()
	}

	// A function without captured variables is one object, however often
	// it is named: calling it in a loop allocates nothing
	if !same(NewInstr(OpNEWFUNC, 1, 1, 0), NewInstr(OpNEWFUNC, 2, 1, 0)) {
		t.Error("expected the same function to be the same object")
	}
	if same(NewInstr(OpNEWFUNC, 1, 1, 0), NewInstr(OpNEWFUNC, 2, 2, 0)) {
		t.Error("expected different functions to be different objects")
	}
	// A closure carries its captured variables: each one is its own object
	if same(NewInstr(OpNEWFUNC, 1, 3, 0), NewInstr(OpNEWFUNC, 2, 3, 0)) {
		t.Error("expected two closures of the same function to be different objects")
	}
	// Host functions likewise
	if !same(NewInstr(OpNEWEXFUNC, 1, 0, 0), NewInstr(OpNEWEXFUNC, 2, 0, 0)) {
		t.Error("expected the same host function to be the same object")
	}
	if same(NewInstr(OpNEWEXFUNC, 1, 0, 0), NewInstr(OpNEWEXFUNC, 2, 1, 0)) {
		t.Error("expected different host functions to be different objects")
	}
}

func TestCallingAFunctionDoesNotAllocate(t *testing.T) {
	// main: i = 0; while i < 250 { helper(); toString-less host call; i++ }
	chunk := chunkOf([]string{"__push"}, []Value{NewInt(250)},
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 8}, []Instr{
			NewInstr(OpNEWARRAY, 6, 0, 0),
			NewInstr(OpLOADI, 1, 0, 0),
			NewInstr(OpLOADK, 2, 0, 0),
			NewInstr(OpLOADI, 5, 1, 0),
			// loop at 4
			NewInstr(OpLT, 3, 1, 2),
			NewInstr(OpJMPIFNOT, 3, 12, 0),
			NewInstr(OpNEWFUNC, 4, 1, 0),
			NewInstr(OpCALL, 4, 0, 0),
			NewInstr(OpNEWEXFUNC, 4, 0, 0),
			NewInstr(OpADD, 1, 1, 5),
			NewInstr(OpJMP, 4, 0, 0),
			NewInstr(OpNOP, 0, 0, 0),
			NewInstr(OpRET, 1, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "helper", RegisterCount: 1}, []Instr{
			NewInstr(OpLOADNULL, 0, 0, 0), NewInstr(OpRET, 0, 0, 0),
		}},
	)

	machine := NewVM()
	before := machine.heap.BytesAllocated()
	result, err := machine.Exec(chunk)
	if err != nil || !result.IsInt() || result.AsInt() != 250 {
		t.Fatalf("expected 250, got %s, %v", FormatValue(result), err)
	}

	// main, helper and push: three function objects and one empty array,
	// no matter how many iterations
	used := machine.heap.BytesAllocated() - before
	if used > 1024 {
		t.Errorf("expected a few hundred bytes for 250 calls, used %d", used)
	}
}

func TestReusedFunctionObjectsSurviveCollection(t *testing.T) {
	chunk := chunkOf([]string{"toString"}, nil,
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 6}, []Instr{
			NewInstr(OpNEWFUNC, 1, 1, 0),
			NewInstr(OpNEWEXFUNC, 2, 0, 0),
			// Drop every reference from the registers, then collect
			NewInstr(OpLOADNULL, 1, 0, 0),
			NewInstr(OpLOADNULL, 2, 0, 0),
			NewInstr(OpGC, 0, 0, 0),
			// The same objects are handed out again and must still work
			NewInstr(OpNEWFUNC, 3, 1, 0),
			NewInstr(OpCALL, 3, 0, 0),
			NewInstr(OpMOV, 5, 0, 0),
			NewInstr(OpNEWEXFUNC, 3, 0, 0),
			NewInstr(OpMOV, 4, 5, 0),
			NewInstr(OpCALL, 3, 1, 0),
			NewInstr(OpRET, 0, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "seven", RegisterCount: 1}, []Instr{
			NewInstr(OpLOADI, 0, 7, 0), NewInstr(OpRET, 0, 0, 0),
		}},
	)

	result, err := NewVM().Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if got := FormatValue(result); got != "7" {
		t.Errorf("expected \"7\", got %q", got)
	}
}

func TestArrayGrowthIsCounted(t *testing.T) {
	machine := NewVM()
	arr, _, _ := machine.heap.AllocArray(0)
	machine.regs = []Value{NewHeapPtr(uintptr(unsafe.Pointer(arr)))}
	empty := machine.heap.BytesAllocated()

	const n = 200000
	for i := 0; i < n; i++ {
		if err := machine.appendToArray(arr, NewInt(int64(i))); err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
		machine.collectIfNeeded()
	}

	// Every element is accounted for...
	grown := machine.heap.BytesAllocated() - empty
	if want := uint64(n) * 8; grown < want {
		t.Errorf("expected at least %d bytes counted for %d elements, got %d", want, n, grown)
	}
	// ...which lets the threshold follow the array. Collections double
	// their spacing, so there are few of them; before, the array grew
	// unseen and each of thousands of collections walked all of it.
	if got := machine.heap.Collections(); got > 16 {
		t.Errorf("expected a handful of collections for %d appends, got %d", n, got)
	}
	if arr.Len() != n || arr.Get(n-1).AsInt() != n-1 {
		t.Errorf("array lost elements: length %d", arr.Len())
	}

	// Freeing the array gives the memory back
	machine.regs = nil
	machine.CollectGarbage()
	if left := machine.heap.BytesAllocated(); left > empty {
		t.Errorf("expected the array's memory to be released, %d bytes still counted", left)
	}
}

func TestObjectGrowthIsCounted(t *testing.T) {
	// obj.a = 1; obj.b = 2; obj.a = 3  -> two fields counted, not three
	chunk := chunkOf(nil, []Value{NewInt(0), NewInt(1)},
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 3}, []Instr{
			NewInstr(OpNEWOBJ, 1, 0, 0),
			NewInstr(OpLOADI, 2, 1, 0),
			NewInstr(OpSETFIELD, 1, 0, 2),
			NewInstr(OpSETFIELD, 1, 1, 2),
			NewInstr(OpSETFIELD, 1, 0, 2),
			NewInstr(OpRET, 1, 0, 0),
		}},
	)
	chunk.StringPool = []string{"a", "b"}

	machine := NewVM()
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	before := machine.heap.BytesAllocated()
	machine.regs = nil
	machine.currentFunction = nil
	machine.caches = map[*Chunk]*chunkCache{}
	machine.CollectGarbage()

	// What was counted while the object grew is exactly what is released
	if left := machine.heap.BytesAllocated(); left != 0 {
		t.Errorf("expected everything to be released, %d of %d bytes still counted", left, before)
	}
}

func TestOutOfMemory(t *testing.T) {
	// Garbage does not exhaust a small heap: a collection runs before giving up
	machine := NewVMWithHeapSize(64 * 1024)
	for i := 0; i < 10000; i++ {
		if _, _, err := machine.heap.AllocString(strings.Repeat("x", 100)); err != nil {
			t.Fatalf("allocation %d failed although everything before it is garbage: %v", i, err)
		}
	}
	if machine.heap.Collections() == 0 {
		t.Error("expected collections to have made room")
	}

	// Live data does: the error says how much was in use and asked for
	machine = NewVMWithHeapSize(64 * 1024)
	arr, _, _ := machine.heap.AllocArray(0)
	machine.regs = []Value{NewHeapPtr(uintptr(unsafe.Pointer(arr)))}
	var err error
	for i := 0; i < 100000 && err == nil; i++ {
		err = machine.appendToArray(arr, NewInt(1))
	}
	if err == nil {
		t.Fatal("expected a growing array to run out of memory")
	}
	for _, want := range []string{"out of memory", "bytes in use", "limit 65536"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to contain %q, got %v", want, err)
		}
	}
	// What was appended before is intact
	if arr.Len() == 0 || arr.Get(0).AsInt() != 1 {
		t.Error("expected the array to keep the elements appended so far")
	}
}

func TestCloseUpvalueDetachesClosures(t *testing.T) {
	// reader() { return upvalue 0 }
	reader := fnDef{FunctionPrototype{Name: "reader", RegisterCount: 1, UpvalueCount: 1}, []Instr{
		NewInstr(OpLDCAP, 0, 0, 0), NewInstr(OpRET, 0, 0, 0),
	}}
	// r1 = 10; r2 = closure(r1); [detach]; r1 = 20; return r2()
	run := func(detach Instr) int64 {
		chunk := chunkOf(nil, nil,
			fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
				NewInstr(OpLOADI, 1, 10, 0),
				NewInstr(OpNEWFUNC, 2, 1, 0),
				NewInstr(OpCAPLOCAL, 2, 0, 1),
				detach,
				NewInstr(OpLOADI, 1, 20, 0),
				NewInstr(OpCALL, 2, 0, 0),
				NewInstr(OpRET, 0, 0, 0),
			}}, reader)
		result, err := NewVM().Exec(chunk)
		if err != nil {
			t.Fatalf("execution failed: %v", err)
		}
		return result.AsInt()
	}

	// Without CLOSEUP the closure follows the register
	if got := run(NewInstr(OpNOP, 0, 0, 0)); got != 20 {
		t.Errorf("expected the closure to see the later value 20, got %d", got)
	}
	// With it, the closure keeps the value from that moment
	if got := run(NewInstr(OpCLOSEUP, 1, 0, 0)); got != 10 {
		t.Errorf("expected the detached closure to keep 10, got %d", got)
	}
	// Closing a register nothing captured is harmless
	if got := run(NewInstr(OpCLOSEUP, 3, 0, 0)); got != 20 {
		t.Errorf("expected closing another register to change nothing, got %d", got)
	}
}

func TestDetachedClosureCanBeCapturedAgain(t *testing.T) {
	// Two closures over the same register, with a CLOSEUP in between: each
	// keeps the value from its own time, like two iterations of a loop.
	chunk := chunkOf(nil, nil,
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 6}, []Instr{
			NewInstr(OpLOADI, 1, 1, 0),
			NewInstr(OpNEWFUNC, 2, 1, 0),
			NewInstr(OpCAPLOCAL, 2, 0, 1),
			NewInstr(OpCLOSEUP, 1, 0, 0),
			NewInstr(OpLOADI, 1, 2, 0),
			NewInstr(OpNEWFUNC, 3, 1, 0),
			NewInstr(OpCAPLOCAL, 3, 0, 1),
			NewInstr(OpCLOSEUP, 1, 0, 0),
			NewInstr(OpLOADI, 1, 99, 0),
			// first() * 10 + second()
			NewInstr(OpCALL, 2, 0, 0),
			NewInstr(OpLOADI, 4, 10, 0),
			NewInstr(OpMUL, 5, 0, 4),
			NewInstr(OpCALL, 3, 0, 0),
			NewInstr(OpADD, 5, 5, 0),
			NewInstr(OpRET, 5, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "reader", RegisterCount: 1, UpvalueCount: 1}, []Instr{
			NewInstr(OpLDCAP, 0, 0, 0), NewInstr(OpRET, 0, 0, 0),
		}},
	)

	result, err := NewVM().Exec(chunk)
	if err != nil || result.AsInt() != 12 {
		t.Errorf("expected 12 (1 then 2), got %s, %v", FormatValue(result), err)
	}
}

func TestCallDepth(t *testing.T) {
	// down(n) { if n == 0 return 0; return down(n - 1) }, main calls down(N)
	program := func(depth int64) *Chunk {
		return chunkOf(nil, []Value{NewInt(depth)},
			fnDef{FunctionPrototype{Name: "main", RegisterCount: 3}, []Instr{
				NewInstr(OpNEWFUNC, 1, 1, 0),
				NewInstr(OpLOADK, 2, 0, 0),
				NewInstr(OpCALL, 1, 1, 0),
				NewInstr(OpRET, 0, 0, 0),
			}},
			fnDef{FunctionPrototype{Name: "down", RegisterCount: 6, ArgCount: 1}, []Instr{
				NewInstr(OpMOV, 1, 0, 0),
				NewInstr(OpLOADI, 2, 0, 0),
				NewInstr(OpEQ, 3, 1, 2),
				NewInstr(OpJMPIFNOT, 3, 9, 0),
				NewInstr(OpRET, 2, 0, 0),
				// index 9 in the chunk: recurse
				NewInstr(OpNEWFUNC, 4, 1, 0),
				NewInstr(OpLOADI, 2, 1, 0),
				NewInstr(OpSUB, 5, 1, 2),
				NewInstr(OpCALL, 4, 1, 0),
				NewInstr(OpRET, 0, 0, 0),
			}},
		)
	}

	// Well past the old limit of 256
	if result, err := NewVM().Exec(program(5000)); err != nil || result.AsInt() != 0 {
		t.Errorf("expected recursion 5000 deep to finish, got %s, %v", FormatValue(result), err)
	}

	// The default limit stops runaway recursion with an error
	_, err := NewVM().Exec(program(DefaultMaxCallDepth + 10))
	if err == nil || !strings.Contains(err.Error(), "stack overflow: max call depth 10000 exceeded") {
		t.Errorf("expected a stack overflow at the default depth, got %v", err)
	}

	// The limit is adjustable, in both directions
	shallow := NewVM()
	shallow.SetMaxCallDepth(50)
	if _, err := shallow.Exec(program(40)); err != nil {
		t.Errorf("expected depth 40 to fit a limit of 50, got %v", err)
	}
	if _, err := shallow.Exec(program(60)); err == nil || !strings.Contains(err.Error(), "max call depth 50 exceeded") {
		t.Errorf("expected depth 60 to exceed a limit of 50, got %v", err)
	}
	deep := NewVM()
	deep.SetMaxCallDepth(50000)
	if _, err := deep.Exec(program(30000)); err != nil {
		t.Errorf("expected depth 30000 to fit a limit of 50000, got %v", err)
	}

	// After an overflow the machine can run again from a clean state
	if result, err := shallow.Exec(program(10)); err != nil || result.AsInt() != 0 {
		t.Errorf("expected the machine to be usable after an overflow, got %s, %v", FormatValue(result), err)
	}
}
