package vm

import "fmt"

// The natives of std.arrays: taking items out of an array, and putting one
// in the middle, without making the array again. Qoqi itself can only add
// at the end (push) and replace (xs[i] = v), so what shortens an array has
// to be done here.
//
// None of them gives memory back. The heap counts an array by its capacity
// (see sizeOf), and counts it out by the same number when it is freed: an
// array made shorter keeps its capacity, so what is counted stays right
// without anything being said to the heap. Making the capacity smaller
// would need the difference counted out, and would make an array that is
// used as a stack allocate each time it grows back.
//
// The slots that stop being part of the array are set to null. The
// collector only looks at the items an array has (markReachable ranges over
// elements, which stops at the length), so a value left behind in the
// spare capacity would not be kept alive by it; but it would then be a
// pointer to an object that may have been freed, and nothing should be
// able to come across one.
//
// They return nothing, not even the item taken out: std.arrays reads it
// before it asks for it to be removed, and so gets it with its type. A value
// handed back from here would be of any type, and making it a T again is a
// cast, which is verified, all the way through an item that is itself an
// array.
//
// Only putAt can need memory, when the array is full, and it gets it the
// way push does (appendToArray). The array and the value are arguments,
// which the caller holds in registers: nothing here needs pinning.

func init() {
	registerNatives("std.arrays",
		// Takes the last item out. Nothing happens to an empty array.
		native{"cutLast", 1, func(vm *VM, args []Value) (Value, error) {
			arr, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if arr.header.Frozen {
				return NewNull(), fmt.Errorf(errFrozen)
			}
			arr.removeAt(len(arr.elements) - 1)
			return NewNull(), nil
		}},
		// Takes the item at a position out, the ones after it moving down.
		// Nothing happens if the array has no such position.
		native{"cutAt", 2, func(vm *VM, args []Value) (Value, error) {
			arr, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if arr.header.Frozen {
				return NewNull(), fmt.Errorf(errFrozen)
			}
			index, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if index >= 0 && index < int64(len(arr.elements)) {
				arr.removeAt(int(index))
			}
			return NewNull(), nil
		}},
		// Puts a value at a position, the items from there on moving up. A
		// position past the end puts it at the end; a negative one changes
		// nothing, as arrays.inserted does.
		native{"putAt", 3, func(vm *VM, args []Value) (Value, error) {
			arr, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if arr.header.Frozen {
				return NewNull(), fmt.Errorf(errFrozen)
			}
			index, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if index < 0 {
				return NewNull(), nil
			}
			length := len(arr.elements)
			at := length
			if index < int64(length) {
				at = int(index)
			}
			// Room for one more first, counted as push counts it. If there
			// is none the array is left as it was.
			if err := vm.appendToArray(arr, NewNull()); err != nil {
				arr.elements = arr.elements[:length]
				return NewNull(), err
			}
			copy(arr.elements[at+1:], arr.elements[at:length])
			arr.elements[at] = args[2]
			return NewNull(), nil
		}},
		// Takes every item out. The array keeps its capacity. Emptying the
		// slots is the one thing here that takes longer for a longer array;
		// it allocates nothing, and is what freeing the items costs anyway.
		native{"cutAll", 1, func(vm *VM, args []Value) (Value, error) {
			arr, err := argArray(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if arr.header.Frozen {
				return NewNull(), fmt.Errorf(errFrozen)
			}
			for i := range arr.elements {
				arr.elements[i] = NewNull()
			}
			arr.elements = arr.elements[:0]
			return NewNull(), nil
		}},
	)
}

// removeAt takes the item at a position out of an array, if it has that
// position. The capacity stays what it was.
func (a *ArrayObj) removeAt(index int) {
	last := len(a.elements) - 1
	if index < 0 || index > last {
		return
	}
	copy(a.elements[index:], a.elements[index+1:])
	a.elements[last] = NewNull() // Not the array's any more: see above
	a.elements = a.elements[:last]
}
