package vm

import (
	"fmt"
	"strings"
	"testing"
)

// The part of a dictionary the machine does: finding where a key is.

// keysOf makes the array of keys a dictionary would hold.
func keysOf(t *testing.T, machine *VM, keys ...string) Value {
	t.Helper()
	value, err := machine.newStringArray(keys)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func find(t *testing.T, machine *VM, keys, index Value, key string) int64 {
	t.Helper()
	text, err := machine.newString(key)
	if err != nil {
		t.Fatal(err)
	}
	at, err := natives["std.dict.find"].fn(machine, []Value{keys, index, text})
	if err != nil {
		t.Fatalf("find(%q): %v", key, err)
	}
	return at.AsInt()
}

func TestFindingAKeyAmongAFew(t *testing.T) {
	machine := NewVM()
	keys := keysOf(t, machine, "name", "age", "", "étoile", "name2")
	machine.regs = []Value{keys}

	for key, want := range map[string]int64{"name": 0, "age": 1, "": 2, "étoile": 3, "name2": 4, "nam": -1, "Name": -1, "name ": -1} {
		if got := find(t, machine, keys, NewNull(), key); got != want {
			t.Errorf("%q is at %d, expected %d", key, got, want)
		}
	}
	// No keys at all
	if got := find(t, machine, keysOf(t, machine), NewNull(), "name"); got != -1 {
		t.Errorf("a key was found among none, at %d", got)
	}
	// The same key twice is found where it is first: a dictionary never
	// holds one twice, and the first is the one it would mean
	if got := find(t, machine, keysOf(t, machine, "a", "b", "a"), NewNull(), "a"); got != 0 {
		t.Errorf("found at %d", got)
	}
}

func TestFindingAKeyThroughTheIndex(t *testing.T) {
	machine := NewVM()
	const count = 5000
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprint("key-", i*7)
	}
	keys := keysOf(t, machine, names...)
	machine.regs = []Value{keys, NewNull()}

	index, err := natives["std.dict.indexed"].fn(machine, []Value{keys})
	if err != nil {
		t.Fatal(err)
	}
	machine.regs[1] = index
	if typeName(index) != "index" || FormatValue(index) != "<index>" {
		t.Errorf("an index says it is %s, written %s", typeName(index), FormatValue(index))
	}

	for i, name := range names {
		if got := find(t, machine, keys, index, name); got != int64(i) {
			t.Fatalf("%q is at %d, expected %d", name, got, i)
		}
	}
	if got := find(t, machine, keys, index, "key-3"); got != -1 {
		t.Errorf("a key that is not there was found at %d", got)
	}

	// Told of a new key, and of one that went
	call := func(name string, args ...Value) {
		t.Helper()
		if _, err := natives["std.dict."+name].fn(machine, args); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	added, _ := machine.newString("added")
	call("indexPut", index, added, NewInt(count))
	gone, _ := machine.newString("key-14")
	call("indexDrop", index, gone)
	call("indexDrop", index, gone) // Twice is once
	if find(t, machine, keys, index, "added") != count || find(t, machine, keys, index, "key-14") != -1 || find(t, machine, keys, index, "key-21") != 3 {
		t.Error("the index does not follow what it is told")
	}

	// It survives a collection while something holds it, and is counted
	// out exactly when nothing does
	machine.CollectGarbage()
	if find(t, machine, keys, index, "key-7") != 1 {
		t.Error("the index did not survive a collection")
	}
	machine.regs, machine.pinned = nil, nil // What the natives called by hand kept alive
	machine.CollectGarbage()
	if left := machine.heap.BytesAllocated(); left != 0 {
		t.Errorf("%d bytes counted with nothing left", left)
	}
}

// What an index is counted for follows the keys it is given.
func TestAnIndexIsCountedAsItGrows(t *testing.T) {
	machine := NewVMWithHeapSize(64 << 10)
	keys := keysOf(t, machine)
	machine.regs = []Value{keys, NewNull()}
	index, err := natives["std.dict.indexed"].fn(machine, []Value{keys})
	if err != nil {
		t.Fatal(err)
	}
	machine.regs[1] = index
	before := machine.heap.BytesAllocated()

	// Told of keys until the heap has no room for more
	told := 0
	for ; told < 100000; told++ {
		key, err := machine.newString(fmt.Sprint("k", told))
		if err != nil {
			break
		}
		if _, err := natives["std.dict.indexPut"].fn(machine, []Value{index, key, NewInt(int64(told))}); err != nil {
			if !strings.Contains(err.Error(), "out of memory") {
				t.Fatalf("after %d keys: %v", told, err)
			}
			break
		}
	}
	if told < 100 || told >= 100000 {
		t.Fatalf("%d keys fitted in a heap of 64 KB", told)
	}
	if machine.heap.BytesAllocated() <= before {
		t.Error("the keys were not counted")
	}
	// The same key again takes no more room
	again, _ := machine.newString("k0")
	counted := machine.heap.BytesAllocated()
	machine.CollectGarbage()
	settled := machine.heap.BytesAllocated()
	natives["std.dict.indexPut"].fn(machine, []Value{index, again, NewInt(0)})
	machine.CollectGarbage()
	if machine.heap.BytesAllocated() > settled || settled > counted {
		t.Errorf("counted %d, %d after a collection, %d after the same key again", counted, settled, machine.heap.BytesAllocated())
	}
}

func TestDictionaryNativesRefuseWhatIsNotTheirs(t *testing.T) {
	machine := NewVM()
	keys := keysOf(t, machine, "a")
	text, _ := machine.newString("a")
	machine.regs = []Value{keys, text}

	for name, args := range map[string][]Value{
		"find":      {keys, NewInt(3), text}, // Neither null nor an index
		"indexed":   {NewInt(1)},             // Not an array
		"indexPut":  {keys, text, NewInt(0)}, // An array is not an index
		"indexDrop": {NewNull(), text},
	} {
		if _, err := natives["std.dict."+name].fn(machine, args); err == nil {
			t.Errorf("%s accepted %v", name, args)
		}
	}
	if _, err := natives["std.dict.find"].fn(machine, []Value{keys, NewNull(), NewInt(1)}); err == nil {
		t.Error("find accepted a key that is not a text")
	}
}
