package vm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// heapText makes a text object of the heap.
func heapText(t *testing.T, h *Heap, s string) *StringObj {
	t.Helper()
	obj, _, err := h.AllocString(s)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

// joinTexts is what ADD does of two texts.
func joinTexts(t *testing.T, machine *VM, a, b *StringObj) *StringObj {
	t.Helper()
	v, err := machine.concatenate(valueOf(&a.header), valueOf(&b.header))
	if err != nil {
		t.Fatal(err)
	}
	return stringObject(v)
}

// Two long texts joined are kept as the two: the text, its length and its
// characters are what they would be written out, and asking for its length
// does not write it out.
func TestLongTextsAreJoinedWithoutCopying(t *testing.T) {
	machine := NewVM()
	h := machine.heap
	left := heapText(t, h, strings.Repeat("étoile ", 100)) // 800 bytes, 700 characters
	right := heapText(t, h, strings.Repeat("日本🙂", 100))    // 1000 bytes, 300 characters
	joined := joinTexts(t, machine, left, right)
	if joined.rope() == nil {
		t.Fatal("expected two long texts to be kept as the two")
	}
	if joined.RuneLen() != 1000 || joined.ByteLen() != 1800 || joined.rope() == nil {
		t.Errorf("length %d characters, %d bytes; written out: %v", joined.RuneLen(), joined.ByteLen(), joined.rope() == nil)
	}

	want := left.Data() + right.Data()
	runes := []rune(want)
	for _, i := range []int{0, 1, 699, 700, 701, 998, 999} {
		if got := joined.RuneAt(i); got != string(runes[i]) {
			t.Errorf("character %d: %q, expected %q", i, got, string(runes[i]))
		}
	}
	if joined.rope() != nil || joined.Data() != want {
		t.Error("expected the text written out, and whole, once a character was asked for")
	}
	if got := joined.Slice(695, 705); got != string(runes[695:705]) {
		t.Errorf("slice: %q", got)
	}

	// Short texts are joined as before: written out at once
	short := joinTexts(t, machine, heapText(t, h, "ab"), heapText(t, h, "cd"))
	if short.rope() != nil || short.Data() != "abcd" {
		t.Errorf("short texts joined: %q, kept as two: %v", short.Data(), short.rope() != nil)
	}
}

// A text built a part at a time is a chain as long as the parts: it is
// written out without recursion, equal to the same text made at once.
func TestATextBuiltAPartAtATime(t *testing.T) {
	machine := NewVM()
	h := machine.heap
	built := heapText(t, h, strings.Repeat("x", ropeMin))
	var want strings.Builder
	want.WriteString(built.Data())
	for i := 0; i < 200000; i++ {
		part := "part " + string(rune('a'+i%26)) + "é\n"
		built = joinTexts(t, machine, built, heapText(t, h, part))
		want.WriteString(part)
	}
	if built.RuneLen() != utf8.RuneCountInString(want.String()) {
		t.Errorf("%d characters, expected %d", built.RuneLen(), utf8.RuneCountInString(want.String()))
	}
	written := heapText(t, h, want.String())
	if !valuesEqual(valueOf(&built.header), valueOf(&written.header)) {
		t.Error("the text built is not equal to the same text made at once")
	}
}

// The texts a text is joined of are kept while it is not written out, and
// let go once it is; what the heap counts is back where it started once
// nothing is held.
func TestTextsJoinedAndTheCollector(t *testing.T) {
	machine := NewVM()
	h := machine.heap
	var freed []*ObjHeader
	h.onFree = func(o *ObjHeader) { freed = append(freed, o) }
	machine.stk = newRegStack(8)
	machine.regs = machine.window(0, 2)

	start := h.BytesAllocated()
	left := heapText(t, h, strings.Repeat("a", 600))
	right := heapText(t, h, strings.Repeat("b", 600))
	joined := joinTexts(t, machine, left, right)
	// Only the joined text is held
	machine.regs[0], machine.regs[1] = valueOf(&joined.header), valueOf(&joined.header)
	machine.CollectGarbage()
	if len(freed) != 0 {
		t.Fatalf("%d texts freed while the joined one needs them", len(freed))
	}

	// Written out, it needs them no more; its bytes are counted now
	before := h.BytesAllocated()
	if joined.Data() != strings.Repeat("a", 600)+strings.Repeat("b", 600) {
		t.Fatal("the text written out is not the two")
	}
	if h.BytesAllocated() != before+1200 {
		t.Errorf("expected the 1200 bytes written out to be counted: %d, then %d", before, h.BytesAllocated())
	}
	machine.CollectGarbage()
	if len(freed) != 2 {
		t.Errorf("expected the two parts to be freed, %d were", len(freed))
	}

	machine.regs[0], machine.regs[1] = valueNull, valueNull
	machine.CollectGarbage()
	if got := h.BytesAllocated(); got != start {
		t.Errorf("the heap counts %d bytes with nothing held, started at %d", got, start)
	}
}
