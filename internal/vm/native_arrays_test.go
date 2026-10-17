package vm

import (
	"fmt"
	"strings"
	"testing"
)

// arrayFixture is an array on a VM's heap that a test changes through the
// natives of std.arrays and then looks into.
type arrayFixture struct {
	t       *testing.T
	machine *VM
	value   Value
	arr     *ArrayObj
}

// newArrayFixture makes an array of numbers ([]int) or of texts
// ([]string). The array is pinned: these tests collect garbage with no
// program running, and nothing else would hold it.
func newArrayFixture(t *testing.T, items interface{}) *arrayFixture {
	t.Helper()
	machine := NewVM()
	var value Value
	var err error
	switch list := items.(type) {
	case []int:
		numbers := make([]int64, len(list))
		for i, n := range list {
			numbers[i] = int64(n)
		}
		value, err = machine.newIntArray(numbers)
	case []string:
		value, err = machine.newStringArray(list)
	default:
		t.Fatalf("unsupported items %T", items)
	}
	if err != nil {
		t.Fatal(err)
	}
	// Making the array pinned what it was made of; only the array is to
	// stay, so that what is taken out of it is held by nothing
	machine.pinned = machine.pinned[:0]
	machine.pin(value)
	arr, err := argArray([]Value{value}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return &arrayFixture{t: t, machine: machine, value: value, arr: arr}
}

// call calls a native of std.arrays with the array and more arguments.
func (f *arrayFixture) call(name string, more ...Value) error {
	f.t.Helper()
	fn, ok := f.machine.externFuncs["std.arrays."+name]
	if !ok {
		f.t.Fatalf("no native function std.arrays.%s", name)
	}
	result, err := fn(f.machine, append([]Value{f.value}, more...))
	if err == nil && !result.IsNull() {
		f.t.Errorf("%s: expected nothing back, got %s", name, FormatValue(result))
	}
	return err
}

// must calls a native that is expected to succeed.
func (f *arrayFixture) must(name string, more ...Value) {
	f.t.Helper()
	if err := f.call(name, more...); err != nil {
		f.t.Fatalf("%s: unexpected error: %v", name, err)
	}
}

// expect checks what the array holds, printed as Qoqi prints it.
func (f *arrayFixture) expect(want string) {
	f.t.Helper()
	if got := FormatValue(f.value); got != want {
		f.t.Errorf("expected %s, got %s", want, got)
	}
}

// expectSpareHoldsNothing checks that no object is left behind in the
// part of the array's memory that is not its items.
func (f *arrayFixture) expectSpareHoldsNothing() {
	f.t.Helper()
	all := f.arr.elements[:cap(f.arr.elements)]
	for i := len(f.arr.elements); i < len(all); i++ {
		if header(all[i]) != nil {
			f.t.Errorf("slot %d, past the %d items, still holds %s", i, len(f.arr.elements), FormatValue(all[i]))
		}
	}
}

func TestNativeArraysCutLast(t *testing.T) {
	f := newArrayFixture(t, []int{1, 2, 3})
	capacity := cap(f.arr.elements)

	f.must("cutLast")
	f.expect("[1, 2]")
	f.must("cutLast")
	f.expect("[1]")

	// A single item, then none: an empty array is left alone
	f.must("cutLast")
	f.expect("[]")
	f.must("cutLast")
	f.must("cutLast")
	f.expect("[]")

	if got := cap(f.arr.elements); got != capacity {
		t.Errorf("expected the capacity to stay %d, got %d", capacity, got)
	}
	f.expectSpareHoldsNothing()

	// An array that never held anything
	empty := newArrayFixture(t, []int{})
	empty.must("cutLast")
	empty.expect("[]")
}

func TestNativeArraysCutAt(t *testing.T) {
	tests := []struct {
		items []int
		index int64
		want  string
	}{
		{[]int{1, 2, 3, 4}, 0, "[2, 3, 4]"},
		{[]int{1, 2, 3, 4}, 1, "[1, 3, 4]"},
		{[]int{1, 2, 3, 4}, 3, "[1, 2, 3]"},
		{[]int{7}, 0, "[]"},
		// Positions the array does not have change nothing
		{[]int{1, 2, 3, 4}, 4, "[1, 2, 3, 4]"},
		{[]int{1, 2, 3, 4}, -1, "[1, 2, 3, 4]"},
		{[]int{1, 2, 3, 4}, MaxInt, "[1, 2, 3, 4]"},
		{[]int{1, 2, 3, 4}, MinInt, "[1, 2, 3, 4]"},
		{[]int{7}, 1, "[7]"},
		{[]int{}, 0, "[]"},
		{[]int{}, -1, "[]"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v at %d", tt.items, tt.index), func(t *testing.T) {
			f := newArrayFixture(t, tt.items)
			capacity := cap(f.arr.elements)
			f.must("cutAt", NewInt(tt.index))
			f.expect(tt.want)
			f.expectSpareHoldsNothing()
			if got := cap(f.arr.elements); got != capacity {
				t.Errorf("expected the capacity to stay %d, got %d", capacity, got)
			}
		})
	}

	// One after the other, from the front: what a queue does
	f := newArrayFixture(t, []int{1, 2, 3})
	for _, want := range []string{"[2, 3]", "[3]", "[]", "[]"} {
		f.must("cutAt", NewInt(0))
		f.expect(want)
	}
	f.expectSpareHoldsNothing()
}

func TestNativeArraysPutAt(t *testing.T) {
	tests := []struct {
		items []int
		index int64
		want  string
	}{
		{[]int{1, 2, 3}, 0, "[9, 1, 2, 3]"},
		{[]int{1, 2, 3}, 1, "[1, 9, 2, 3]"},
		{[]int{1, 2, 3}, 2, "[1, 2, 9, 3]"},
		{[]int{1, 2, 3}, 3, "[1, 2, 3, 9]"}, // The length: at the end
		{[]int{}, 0, "[9]"},
		{[]int{7}, 0, "[9, 7]"},
		{[]int{7}, 1, "[7, 9]"},
		// Past the end is the end, as with arrays.inserted
		{[]int{1, 2, 3}, 4, "[1, 2, 3, 9]"},
		{[]int{1, 2, 3}, MaxInt, "[1, 2, 3, 9]"},
		{[]int{}, 5, "[9]"},
		// A negative position changes nothing, as with arrays.inserted
		{[]int{1, 2, 3}, -1, "[1, 2, 3]"},
		{[]int{1, 2, 3}, MinInt, "[1, 2, 3]"},
		{[]int{}, -1, "[]"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v at %d", tt.items, tt.index), func(t *testing.T) {
			f := newArrayFixture(t, tt.items)
			f.must("putAt", NewInt(tt.index), NewInt(9))
			f.expect(tt.want)
			f.expectSpareHoldsNothing()
		})
	}
}

func TestNativeArraysCutAll(t *testing.T) {
	f := newArrayFixture(t, []int{1, 2, 3, 4, 5})
	capacity := cap(f.arr.elements)
	f.must("cutAll")
	f.expect("[]")
	f.expectSpareHoldsNothing()
	if got := cap(f.arr.elements); got != capacity {
		t.Errorf("expected the capacity to stay %d, got %d", capacity, got)
	}

	// Empty already, and usable afterwards
	f.must("cutAll")
	f.expect("[]")
	f.must("putAt", NewInt(0), NewInt(1))
	f.must("putAt", NewInt(0), NewInt(2))
	f.expect("[2, 1]")
}

// An item taken out of an array is no longer held by it: once nothing else
// holds it either, the next collection frees it. And it frees nothing the
// array still has.
func TestNativeArraysReleaseWhatTheyRemove(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		do    func(f *arrayFixture)
		freed []string
		left  string
	}{
		{"cutLast", []string{"a", "b", "c"}, func(f *arrayFixture) { f.must("cutLast") }, []string{"c"}, `["a", "b"]`},
		{"cutLast of one", []string{"a"}, func(f *arrayFixture) { f.must("cutLast") }, []string{"a"}, `[]`},
		{"cutAt first", []string{"a", "b", "c"}, func(f *arrayFixture) { f.must("cutAt", NewInt(0)) }, []string{"a"}, `["b", "c"]`},
		{"cutAt middle", []string{"a", "b", "c"}, func(f *arrayFixture) { f.must("cutAt", NewInt(1)) }, []string{"b"}, `["a", "c"]`},
		{"cutAt outside", []string{"a", "b", "c"}, func(f *arrayFixture) { f.must("cutAt", NewInt(3)) }, nil, `["a", "b", "c"]`},
		{"cutAll", []string{"a", "b", "c"}, func(f *arrayFixture) { f.must("cutAll") }, []string{"a", "b", "c"}, `[]`},
		{"cutAll then cutLast", []string{"a", "b"}, func(f *arrayFixture) { f.must("cutAll"); f.must("cutLast") }, []string{"a", "b"}, `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newArrayFixture(t, tt.items)
			heap := f.machine.heap

			freed := map[string]bool{}
			heap.onFree = func(header *ObjHeader) {
				if s, ok := stringValue(valueOf(header)); ok {
					freed[s] = true
				}
			}

			// While the array holds them, nothing is freed
			f.machine.CollectGarbage()
			if len(freed) != 0 {
				t.Fatalf("expected nothing to be freed before the removal, got %v", freed)
			}
			before := countObjects(heap)

			tt.do(f)
			f.machine.CollectGarbage()

			if len(freed) != len(tt.freed) {
				t.Errorf("expected %v to be freed, got %v", tt.freed, freed)
			}
			for _, s := range tt.freed {
				if !freed[s] {
					t.Errorf("expected %q to be freed, got %v", s, freed)
				}
			}
			if got := countObjects(heap); got != before-len(tt.freed) {
				t.Errorf("expected %d objects left, got %d", before-len(tt.freed), got)
			}
			f.expect(tt.left)
			f.expectSpareHoldsNothing()
		})
	}
}

// What putAt moves up, and what it puts in, the array holds.
func TestNativeArraysKeepWhatTheyInsert(t *testing.T) {
	f := newArrayFixture(t, []string{"a", "c"})
	freed := 0
	f.machine.heap.onFree = func(*ObjHeader) { freed++ }

	// Held by nothing but the arguments of the call, then by the array
	added, err := f.machine.newString("b")
	if err != nil {
		t.Fatal(err)
	}
	f.must("putAt", NewInt(1), added)
	f.machine.CollectGarbage()
	if freed != 0 {
		t.Errorf("expected nothing to be freed, got %d objects", freed)
	}
	f.expect(`["a", "b", "c"]`)
}

// The heap counts an array by its capacity. Taking items out leaves the
// capacity, and the count, alone; putting one in counts what the array
// grew by, exactly; and when the array goes, all of it is counted out.
func TestNativeArraysKeepTheMemoryCountExact(t *testing.T) {
	baseline := NewVM().heap.bytesAllocated

	f := newArrayFixture(t, []int{1, 2, 3, 4, 5, 6, 7, 8})
	heap := f.machine.heap
	counted := func() uint64 {
		return heap.bytesAllocated
	}
	want := counted()
	if got := baseline + sizeOf(f.arr.Header()); got != want {
		t.Fatalf("expected the array to be all that is counted: %d bytes, got %d", got, want)
	}

	for _, step := range []struct {
		name string
		args []Value
	}{
		{"cutLast", nil},
		{"cutAt", []Value{NewInt(0)}},
		{"cutAt", []Value{NewInt(99)}},
		{"putAt", []Value{NewInt(1), NewInt(0)}}, // Into the room cutLast left
		{"putAt", []Value{NewInt(-1), NewInt(0)}},
		{"cutAll", nil},
		{"cutLast", nil},
	} {
		f.must(step.name, step.args...)
		if got := counted(); got != want {
			t.Errorf("%s: expected the count to stay %d bytes, got %d", step.name, want, got)
		}
		if got := baseline + sizeOf(f.arr.Header()); got != want {
			t.Errorf("%s: the array now counts for %d bytes, %d are counted", step.name, got, want)
		}
	}

	// Filling it past its capacity: each time it grows, the count grows by
	// as much as the capacity did
	grew := 0
	for i := 0; i < 100; i++ {
		before := cap(f.arr.elements)
		f.must("putAt", NewInt(int64(i/2)), NewInt(int64(i)))
		if after := cap(f.arr.elements); after > before {
			grew++
			want += uint64(after-before) * valueSize
		}
		if got := counted(); got != want {
			t.Fatalf("after %d insertions: expected %d bytes counted, got %d", i+1, want, got)
		}
	}
	if grew == 0 {
		t.Fatal("expected the array to grow")
	}
	if got := baseline + sizeOf(f.arr.Header()); got != want {
		t.Errorf("the array counts for %d bytes, %d are counted", got, want)
	}

	// Emptied, it still counts for what it can hold
	f.must("cutAll")
	if got := counted(); got != want {
		t.Errorf("cutAll: expected the count to stay %d bytes, got %d", want, got)
	}

	// Counted in is counted out
	f.machine.pinned = nil
	f.machine.CollectGarbage()
	if got := counted(); got != baseline {
		t.Errorf("expected %d bytes counted once the array is freed, got %d", baseline, got)
	}
}

// With no room left to grow into, putAt fails and leaves the array as it
// was.
func TestNativeArraysPutAtOutOfMemory(t *testing.T) {
	f := newArrayFixture(t, []int{1, 2, 3})
	for cap(f.arr.elements) > len(f.arr.elements) {
		f.must("putAt", NewInt(MaxInt), NewInt(0))
	}
	held := FormatValue(f.value)
	length := len(f.arr.elements)

	f.machine.heap.maxBytes = f.machine.heap.bytesAllocated
	err := f.call("putAt", NewInt(1), NewInt(9))
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("expected to be out of memory, got %v", err)
	}
	if len(f.arr.elements) != length {
		t.Errorf("expected the array to keep its %d items, got %d", length, len(f.arr.elements))
	}
	f.expect(held)

	// Taking out needs no memory, and makes the room to put in again
	f.must("cutLast")
	f.must("putAt", NewInt(0), NewInt(9))
	if len(f.arr.elements) != length {
		t.Errorf("expected %d items, got %d", length, len(f.arr.elements))
	}
}

func TestNativeArraysCheckTheirArguments(t *testing.T) {
	expectNativeError(t, "argument 1 must be an array, got Int", "std.arrays.cutLast", 1)
	expectNativeError(t, "argument 1 must be an array, got String", "std.arrays.cutLast", "abc")
	expectNativeError(t, "argument 1 must be an array, got null", "std.arrays.cutAll", nil)
	expectNativeError(t, "argument 1 must be an array", "std.arrays.cutAt", 1.5, 0)
	expectNativeError(t, "argument 2 must be an Int, got String", "std.arrays.cutAt", []int{1}, "0")
	expectNativeError(t, "argument 2 must be an Int, got Float", "std.arrays.cutAt", []int{1}, 0.0)
	expectNativeError(t, "argument 1 must be an array, got Bool", "std.arrays.putAt", true, 0, 1)
	expectNativeError(t, "argument 2 must be an Int, got null", "std.arrays.putAt", []int{1}, nil, 1)

	expectNativeError(t, "expects 1 arguments, got 0", "std.arrays.cutLast")
	expectNativeError(t, "expects 2 arguments, got 1", "std.arrays.cutAt", []int{1})
	expectNativeError(t, "expects 3 arguments, got 2", "std.arrays.putAt", []int{1}, 0)
	expectNativeError(t, "expects 1 arguments, got 2", "std.arrays.cutAll", []int{1}, 0)
}

// The natives through bytecode, as a program calls them: fifty texts
// pushed, all but the first taken off again.
func TestNativeArraysThroughBytecode(t *testing.T) {
	const height = 50

	// r0: what a call returns; r1: the function; r2, r3: its arguments;
	// r4: the array
	code := []Instr{NewInstr(OpNEWARRAY, 4, 0, 0)}
	for i := 0; i < height; i++ {
		code = append(code,
			NewInstr(OpNEWEXFUNC, 1, 0, 0),
			NewInstr(OpMOV, 2, 4, 0),
			NewInstr(OpNEWSTR, 3, 0, 0),
			// A text of its own each time: the one written in the program
			// is one object however often it is reached
			NewInstr(OpADD, 3, 3, 3),
			NewInstr(OpCALL, 1, 2, 0),
		)
	}
	for i := 0; i < height-1; i++ {
		code = append(code,
			NewInstr(OpNEWEXFUNC, 1, 1, 0),
			NewInstr(OpMOV, 2, 4, 0),
			NewInstr(OpCALL, 1, 1, 0),
		)
	}
	code = append(code, NewInstr(OpRET, 4, 0, 0))

	chunk := chunkOf([]string{"__push", "std.arrays.cutLast"}, []Value{NewInt(0)},
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 5}, code})
	chunk.StringPool = []string{"item"}

	machine := NewVM()
	result, err := machine.Exec(chunk)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if got := FormatValue(result); got != `["itemitem"]` {
		t.Errorf(`expected ["itemitem"], got %s`, got)
	}

	// The forty-nine texts taken off are garbage; the one left is not
	texts := func() int {
		count := 0
		for h := machine.heap.objects; h != nil; h = h.Next {
			if h.Type == ObjTypeString {
				count++
			}
		}
		return count
	}
	machine.pin(result)
	if got := texts(); got < height {
		t.Fatalf("expected the %d texts to be there before a collection, got %d", height, got)
	}
	machine.CollectGarbage()
	// The one in the array, the one written in the program, and at most
	// the last one made, which the machine may still have at hand
	if got := texts(); got < 2 || got > 3 {
		t.Errorf("expected 2 or 3 texts left after a collection, got %d", got)
	}
}
