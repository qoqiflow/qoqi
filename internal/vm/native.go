package vm

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"
	"unsafe"
)

// Native functions are the part of the standard library that cannot be
// written in Qoqi: they need the machine (files, the clock) or would be
// too slow (searching a string). A Qoqi module declares one with
// `extern fn`, and the function here with the same program-wide name
// provides it: `extern fn sqrt(x: Float) -> Float;` in the module std.math
// is the native "std.math.sqrt".
//
// The type checker has verified the arguments against the declaration, but
// natives check them again: bytecode can also be written by hand.

// native describes one native function.
type native struct {
	name string
	args int // Number of arguments
	fn   ExternFunc
}

// natives is every native function, by name. The files native_*.go add to
// it when the package is loaded.
var natives = make(map[string]native)

func registerNatives(prefix string, list ...native) {
	for _, n := range list {
		n.name = prefix + "." + n.name
		natives[n.name] = n
	}
}

// IsNative reports whether the runtime provides a function of that name.
func IsNative(name string) bool {
	_, ok := natives[name]
	return ok
}

// NativeNames lists the native functions, sorted.
func NativeNames() []string {
	names := make([]string, 0, len(natives))
	for name := range natives {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// registerNativeFunctions makes the natives callable in this VM.
func (vm *VM) registerNativeFunctions() {
	for _, n := range natives {
		n := n
		vm.RegisterExtern(n.name, func(vm *VM, args []Value) (Value, error) {
			if len(args) != n.args {
				return NewNull(), fmt.Errorf("expects %d arguments, got %d", n.args, len(args))
			}
			return n.fn(vm, args)
		})
	}
}

// SetInput sets where std.io.readLine reads from (default: os.Stdin).
func (vm *VM) SetInput(r io.Reader) {
	vm.in = bufio.NewReader(r)
}

func (vm *VM) input() *bufio.Reader {
	if vm.in == nil {
		vm.in = bufio.NewReader(os.Stdin)
	}
	return vm.in
}

// random returns the VM's random number generator. Each VM has its own, so
// a program that seeds it gets the same numbers on every run.
func (vm *VM) random() *rand.Rand {
	if vm.rng == nil {
		vm.rng = rand.New(rand.NewSource(rand.Int63()))
	}
	return vm.rng
}

// --- Keeping values alive ---

// pin keeps a value alive until the native function that is running
// returns. A native that allocates more than once needs it: the second
// allocation may collect garbage, and a value held only in a Go variable
// is not something the collector can see.
func (vm *VM) pin(v Value) Value {
	vm.pinned = append(vm.pinned, v)
	return v
}

// --- Arguments ---

func argString(args []Value, i int) (string, error) {
	if s, ok := stringValue(args[i]); ok {
		return s, nil
	}
	return "", fmt.Errorf("argument %d must be a String, got %s", i+1, typeName(args[i]))
}

func argInt(args []Value, i int) (int64, error) {
	if args[i].IsInt() {
		return args[i].AsInt(), nil
	}
	return 0, fmt.Errorf("argument %d must be an Int, got %s", i+1, typeName(args[i]))
}

func argFloat(args []Value, i int) (float64, error) {
	if args[i].IsNumber() {
		return args[i].AsNumber(), nil
	}
	return 0, fmt.Errorf("argument %d must be a Float, got %s", i+1, typeName(args[i]))
}

func argArray(args []Value, i int) (*ArrayObj, error) {
	if t, ok := heapType(args[i]); ok && t == ObjTypeArray {
		return (*ArrayObj)(unsafe.Pointer(args[i].AsHeapPtr())), nil
	}
	return nil, fmt.Errorf("argument %d must be an array, got %s", i+1, typeName(args[i]))
}

// argStrings reads an argument that is an array of strings.
func argStrings(args []Value, i int) ([]string, error) {
	arr, err := argArray(args, i)
	if err != nil {
		return nil, err
	}
	out := make([]string, arr.Len())
	for j := range out {
		s, ok := stringValue(arr.Get(j))
		if !ok {
			return nil, fmt.Errorf("argument %d must be an array of Strings, element %d is %s", i+1, j, typeName(arr.Get(j)))
		}
		out[j] = s
	}
	return out, nil
}

// --- Results ---

// newStringArray builds an array of strings.
func (vm *VM) newStringArray(items []string) (Value, error) {
	arr, _, err := vm.heap.AllocArray(len(items))
	if err != nil {
		return NewNull(), err
	}
	result := vm.pin(NewHeapPtr(uintptr(unsafe.Pointer(arr))))
	for _, item := range items {
		s, err := vm.newString(item)
		if err != nil {
			return NewNull(), err
		}
		if err := vm.appendToArray(arr, vm.pin(s)); err != nil {
			return NewNull(), err
		}
	}
	return result, nil
}

// ptrOf is the address of a heap object, as a value holds it.
func ptrOf(arr *ArrayObj) unsafe.Pointer {
	return unsafe.Pointer(arr)
}

// Collections reports how many times the heap has been collected.
func (vm *VM) Collections() int {
	return vm.heap.Collections()
}

// --- Natives provided by other packages ---

// RegisterNative adds a native function provided outside this package: by
// a package that needs what this one cannot import, the compiler first of
// all. It is to be called when the program starts, before a VM is made.
func RegisterNative(name string, args int, fn ExternFunc) {
	natives[name] = native{name: name, args: args, fn: fn}
}

// ArgString reads an argument that is a String.
func ArgString(args []Value, i int) (string, error) { return argString(args, i) }

// ArgInt reads an argument that is an Int.
func ArgInt(args []Value, i int) (int64, error) { return argInt(args, i) }

// NewStringArray builds an array of strings, as a native returns one.
func (vm *VM) NewStringArray(items []string) (Value, error) { return vm.newStringArray(items) }
