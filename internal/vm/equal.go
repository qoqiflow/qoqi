package vm

import "unsafe"

// EqualMethod is the method the compiler gives every type that implements
// std.ops.Equal, which calls its equals. Programs cannot write the name. A
// value of such a type found inside an array, a struct or an enum is
// compared by it: [p] == [q] asks p.equals(q).
const EqualMethod = "__equal"

// equal is EQ: numbers compare as numbers and texts by content, as
// valuesEqual has it, and arrays, structs and values of enums by what
// they hold, a type that says what its equality is by its equals.
// An equals that fails stops the comparison with its failure.
func (vm *VM) equal(a, b Value) (bool, error) {
	if a == b && !a.IsNumber() {
		return true, nil
	}
	if !holdsValues(a) || !holdsValues(b) {
		return valuesEqual(a, b), nil
	}
	c := comparison{vm: vm}
	return c.equal(a, b)
}

// holdsValues reports whether a value is an array or an object, which are
// compared by what they hold.
func holdsValues(v Value) bool {
	kind, ok := heapType(v)
	return ok && (kind == ObjTypeArray || kind == ObjTypeObject)
}

// comparison compares two values that hold others. The pairs being
// compared further up are kept: a value that holds itself, a node whose
// next leads back to it, is taken for equal where the comparison comes
// round to a pair it is already making, rather than going round for ever.
//
// A pair stays among them once compared: it was found equal, or the
// comparison would have ended. They are a list while they are few, and a
// set once a long chain makes them many.
type comparison struct {
	vm      *VM
	pending [][2]Value
	many    map[[2]Value]bool
}

// seen reports whether a pair is being compared, or was, and adds it.
func (c *comparison) seen(a, b Value) bool {
	pair := [2]Value{a, b}
	if c.many != nil {
		if c.many[pair] {
			return true
		}
		c.many[pair] = true
		return false
	}
	for _, p := range c.pending {
		if p == pair {
			return true
		}
	}
	c.pending = append(c.pending, pair)
	if len(c.pending) > 32 {
		c.many = make(map[[2]Value]bool, 64)
		for _, p := range c.pending {
			c.many[p] = true
		}
		c.pending = nil
	}
	return false
}

func (c *comparison) equal(a, b Value) (bool, error) {
	for {
		if a == b && !a.IsNumber() {
			return true, nil
		}
		if !holdsValues(a) || !holdsValues(b) {
			return valuesEqual(a, b), nil
		}
		ha, hb := header(a), header(b)
		if ha.Type != hb.Type {
			return false, nil
		}
		if c.seen(a, b) {
			return true, nil
		}

		if ha.Type == ObjTypeArray {
			xs, ys := (*ArrayObj)(unsafe.Pointer(ha)), (*ArrayObj)(unsafe.Pointer(hb))
			if xs.Len() != ys.Len() {
				return false, nil
			}
			for i := 0; i < xs.Len(); i++ {
				same, err := c.equal(xs.Get(i), ys.Get(i))
				if err != nil || !same {
					return false, err
				}
			}
			return true, nil
		}

		va, vb := (*ObjectObj)(unsafe.Pointer(ha)), (*ObjectObj)(unsafe.Pointer(hb))
		if va.shape.root.typeName != vb.shape.root.typeName || va.shape.root.variant != vb.shape.root.variant {
			return false, nil
		}
		if va.shape.variant && va.shape.root != vb.shape.root {
			// Two variants of one enum
			return false, nil
		}
		if fn, ok := c.vm.machineMethod(va, EqualMethod); ok {
			// The type says what its equality is
			result, err := c.vm.callFunction(fn, []Value{a, b})
			if err != nil {
				return false, err
			}
			return result.IsBool() && result.AsBool(), nil
		}
		if len(va.slots) != len(vb.slots) {
			return false, nil
		}
		if len(va.slots) == 0 {
			return true, nil
		}

		if va.shape != vb.shape {
			// Not made the same way: by name, which is rare
			for i, key := range va.shape.keys {
				other, ok := vb.Get(key)
				if !ok {
					return false, nil
				}
				same, err := c.equal(va.slots[i], other)
				if err != nil || !same {
					return false, err
				}
			}
			return true, nil
		}
		last := len(va.slots) - 1
		for i := 0; i < last; i++ {
			same, err := c.equal(va.slots[i], vb.slots[i])
			if err != nil || !same {
				return false, err
			}
		}
		// What is held last is compared by going round again rather
		// than by a call: a list made of values, each holding the rest
		// of the list, can be of any length.
		a, b = va.slots[last], vb.slots[last]
	}
}
