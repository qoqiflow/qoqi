package vm

import (
	"fmt"
	"unsafe"
)

// What std.dict needs from the machine.
//
// A dictionary keeps its keys and its values in two arrays, in the order
// the keys were set: that part is written in Qoqi, and typed. Finding
// where a key is among the keys is what is done here.
//
// A dictionary of a few keys is looked through: comparing a handful of
// texts is faster than anything cleverer, and it then costs nothing to
// make, which matters to a program that makes one per call of something
// (the variables of a scope, the fields of an object). Past
// smallDictionary keys it gets an index, which says where each key is.

// smallDictionary is how many keys a dictionary has without an index.
// The Qoqi side decides when to make one; this is the number it uses,
// kept here for the tests.
const smallDictionary = 8

func argIndex(args []Value, i int) (*IndexObj, error) {
	if h := header(args[i]); h != nil && h.Type == ObjTypeIndex {
		return (*IndexObj)(unsafe.Pointer(h)), nil
	}
	return nil, fmt.Errorf("argument %d must be the index of a dictionary, got %s", i+1, typeName(args[i]))
}

func init() {
	registerNatives("std.dict",
		// Where a key is among the keys, or -1. With an index it is
		// asked; without, the keys are looked through. The index never
		// holds a key that was removed.
		native{"find", 3, func(vm *VM, args []Value) (Value, error) {
			key, err := argString(args, 2)
			if err != nil {
				return NewNull(), err
			}
			if !args[1].IsNull() {
				index, err := argIndex(args, 1)
				if err != nil {
					return NewNull(), err
				}
				if at, ok := index.at[key]; ok {
					return NewInt(int64(at)), nil
				}
				return NewInt(-1), nil
			}
			keys, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			for at, held := range keys.elements {
				if h := header(held); h != nil && h.Type == ObjTypeString && (*StringObj)(unsafe.Pointer(h)).Data() == key {
					return NewInt(int64(at)), nil
				}
			}
			return NewInt(-1), nil
		}},
		// The index of the keys of a dictionary: where each is. A
		// position that holds no text (a key that was removed) is left out.
		native{"indexed", 1, func(vm *VM, args []Value) (Value, error) {
			keys, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			index, err := vm.heap.AllocIndex(len(keys.elements))
			if err != nil {
				return NewNull(), err
			}
			for at, held := range keys.elements {
				if key, ok := stringValue(held); ok {
					index.at[key] = int32(at)
				}
			}
			return valueOf(&index.header), nil
		}},
		// Notes where a key is
		native{"indexPut", 3, func(vm *VM, args []Value) (Value, error) {
			index, err := argIndex(args, 0)
			if err != nil {
				return NewNull(), err
			}
			key, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			at, err := argInt(args, 2)
			if err != nil {
				return NewNull(), err
			}
			if _, known := index.at[key]; !known {
				if _, err := vm.heap.grow(indexEntrySize); err != nil {
					return NewNull(), err
				}
				index.counted += indexEntrySize
			}
			index.at[key] = int32(at)
			return NewNull(), nil
		}},
		// Forgets a key
		native{"indexDrop", 2, func(vm *VM, args []Value) (Value, error) {
			index, err := argIndex(args, 0)
			if err != nil {
				return NewNull(), err
			}
			key, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			delete(index.at, key)
			return NewNull(), nil
		}},
	)
}
