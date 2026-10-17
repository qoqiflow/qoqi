package vm

import (
	"fmt"
	"unsafe"
)

// What a constant holds.
//
//	const RED = Color { r: 230, g: 41, b: 55 };
//	const PRIMES = [2, 3, 5, 7];
//
// A constant that is a struct, an array or a value of an enum is made the
// first time the program uses it, and is that one value from then on (see
// the ONCE instruction): reading it costs what reading a variable costs.
//
// Every use being the same value, none may change it: the value and all
// it holds are frozen when it is made, and every way of writing to an
// object or an array refuses a frozen one. A program that wants to change
// what a constant holds changes a copy.

// errFrozen is why a write to what a constant holds is refused.
const errFrozen = "cannot change what a constant holds: it is the same value wherever the constant is used. Change a copy (arrays.copy, or a new struct)"

// frozen reports whether a value is, or is part of, what a constant holds.
func frozen(v Value) bool {
	h := header(v)
	return h != nil && h.Frozen
}

// freeze makes a value, and everything it holds, read-only.
func freeze(v Value) {
	pending := []Value{v}
	for len(pending) > 0 {
		v, pending = pending[len(pending)-1], pending[:len(pending)-1]
		h := header(v)
		if h == nil || h.Frozen {
			continue
		}
		switch h.Type {
		case ObjTypeObject:
			h.Frozen = true
			pending = append(pending, (*ObjectObj)(unsafe.Pointer(h)).slots...)
		case ObjTypeArray:
			h.Frozen = true
			pending = append(pending, (*ArrayObj)(unsafe.Pointer(h)).elements...)
		}
	}
}

// once gives the value of a constant: what its function returns, worked
// out the first time and kept. A constant that is still being worked out
// when it is asked for again is defined in terms of itself, which the
// compiler refuses; a chunk that does it anyway is told so.
//
// A variable outside functions is made the same way, by ONCE with C set:
// once, the first time it is used, and left as it is, to be changed.
//
//go:noinline
func (vm *VM) once(chunk *Chunk, kept *chunkCache, index int, variable bool) (Value, error) {
	if kept.once == nil {
		kept.once = make([]Value, len(chunk.Prototypes))
		kept.making = make([]bool, len(chunk.Prototypes))
	}
	if kept.making[index] {
		what := "constant"
		if variable {
			what = "variable"
		}
		return NewNull(), vm.runtimeError(fmt.Sprintf("the %s made by %s is defined in terms of itself", what, chunk.Prototypes[index].Name))
	}
	function, err := vm.functionObject(chunk, kept.funcs, index)
	if err != nil {
		return NewNull(), err
	}
	kept.making[index] = true
	value, err := vm.callFunction(function, nil)
	kept.making[index] = false
	if err != nil {
		return NewNull(), err
	}
	if !variable {
		freeze(value)
	}
	kept.once[index] = value
	return value, nil
}
