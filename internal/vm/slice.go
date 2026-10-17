package vm

import (
	"fmt"
	"unsafe"
)

// slice is the builtin __slice: the items of an array, or the characters
// of a text, at the positions a range goes through, items[1..3]. Its
// arguments are the collection and what the range is made of: from, to,
// whether to is included, and the step.
//
// Positions outside the collection are left out, as arrays.slice does:
// items[1..] goes to the end because its end is the largest Int, and
// items[2..10] of three items is the last one.
func (vm *VM) slice(args []Value) (Value, error) {
	if len(args) != 5 {
		return NewNull(), fmt.Errorf("__slice expects 5 arguments, got %d", len(args))
	}
	from, err := argInt(args, 1)
	if err != nil {
		return NewNull(), err
	}
	to, err := argInt(args, 2)
	if err != nil {
		return NewNull(), err
	}
	inclusive, err := argBool(args, 3)
	if err != nil {
		return NewNull(), err
	}
	by, err := argInt(args, 4)
	if err != nil {
		return NewNull(), err
	}
	if by == 0 {
		return NewNull(), vm.runtimeError("a range cannot go by steps of 0")
	}

	kind, ok := heapType(args[0])
	var length int64
	switch {
	case ok && kind == ObjTypeArray:
		length = int64((*ArrayObj)(unsafe.Pointer(header(args[0]))).Len())
	case ok && kind == ObjTypeString:
		length = int64((*StringObj)(unsafe.Pointer(header(args[0]))).RuneLen())
	default:
		return NewNull(), fmt.Errorf("__slice expects an array or a text, got %s", typeName(args[0]))
	}

	// The first position past the end, as Range.stop has it
	end := to
	if inclusive {
		if by > 0 {
			end++
		} else {
			end--
		}
	}
	positions := slicePositions(from, end, by, length)

	if kind == ObjTypeString {
		text := (*StringObj)(unsafe.Pointer(header(args[0])))
		if by == 1 {
			if len(positions) == 0 {
				return vm.newString("")
			}
			first, last := positions[0], positions[len(positions)-1]+1
			if first == 0 && last == length {
				return args[0], nil
			}
			return vm.newString(text.Slice(int(first), int(last)))
		}
		var picked []byte
		for _, at := range positions {
			picked = append(picked, text.Slice(int(at), int(at)+1)...)
		}
		return vm.newString(string(picked))
	}

	items := (*ArrayObj)(unsafe.Pointer(header(args[0])))
	values := make([]Value, len(positions))
	for i, at := range positions {
		values[i] = items.Get(int(at))
	}
	return vm.newArrayOf(values)
}

// slicePositions lists the positions from `from`, by steps of `by`, that
// come before `end` and are inside a collection of `length` items. The
// steps outside it are skipped without being gone through: a range that
// ends with the largest Int is not counted to its end.
func slicePositions(from, end, by, length int64) []int64 {
	var positions []int64
	if by > 0 {
		if from < 0 {
			// The first step at or past 0
			from += (-from + by - 1) / by * by
		}
		if end > length {
			end = length
		}
		for at := from; at < end; at += by {
			positions = append(positions, at)
		}
		return positions
	}

	step := -by
	if from > length-1 {
		// The first step at or below the last position
		from -= (from - (length - 1) + step - 1) / step * step
	}
	if end < -1 {
		end = -1
	}
	for at := from; at > end; at -= step {
		positions = append(positions, at)
	}
	return positions
}
