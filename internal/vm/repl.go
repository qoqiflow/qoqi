package vm

import (
	"fmt"
	"sort"
	"strconv"
	"unsafe"
)

// Variables that outlive a program.
//
// The REPL compiles what is typed into a program of its own each time,
// and runs them one after the other on the same VM. A variable declared
// in one has to be there in the next, and be the same variable: a closure
// made earlier that counts in it and the line typed now have to see one
// count, not two copies.
//
// A variable that a closure captured already lives on after the function
// that declared it, in the upvalue the closure holds. That is all a kept
// variable is: an upvalue the VM holds on to, by name. Each program asks
// for it twice:
//
//	let count: Int = __repl_get_count();    // the value it has
//	__repl_link("count", () -> count);      // and the variable itself
//
// The second line hands over a closure that captured the new variable.
// The kept upvalue takes the place of the one just made for it, so that
// it names the register of the new variable for as long as the program
// runs: what earlier closures read and write through it is that register.
// When the program returns the upvalue is closed like any other, and
// holds the value until the next program.
//
// A variable declared by the program is kept the same way, with
// __repl_keep, from then on.

// Names of the functions a program of the REPL is given. They are
// registered on the VM of a session only (see KeepVariables): no other
// program can declare them.
const (
	KeepFunction = "__repl_keep"
	LinkFunction = "__repl_link"
	ShowFunction = "__repl_show"
	GetPrefix    = "__repl_get_"
)

// IsSessionFunction reports whether a name is one of the functions a VM
// provides once KeepVariables was called.
func IsSessionFunction(name string) bool {
	switch name {
	case KeepFunction, LinkFunction, ShowFunction:
		return true
	}
	return len(name) > len(GetPrefix) && name[:len(GetPrefix)] == GetPrefix
}

// keptVariables is what a VM keeps between the programs of a REPL.
type keptVariables struct {
	cells map[string]*UpvalueObj

	// What the last program asked to be shown
	shown    string
	hasShown bool
}

// KeepVariables makes the VM one that programs can leave variables in for
// the programs after them. It is called once, before the first.
func (vm *VM) KeepVariables() {
	if vm.session != nil {
		return
	}
	vm.session = &keptVariables{cells: make(map[string]*UpvalueObj)}

	vm.RegisterExtern(KeepFunction, func(vm *VM, args []Value) (Value, error) {
		name, cell, _, err := vm.variableOf(KeepFunction, args)
		if err != nil {
			return NewNull(), err
		}
		vm.keep(name, cell)
		return NewNull(), nil
	})

	vm.RegisterExtern(LinkFunction, func(vm *VM, args []Value) (Value, error) {
		name, made, reader, err := vm.variableOf(LinkFunction, args)
		if err != nil {
			return NewNull(), err
		}
		kept, ok := vm.session.cells[name]
		if !ok || kept == made {
			vm.keep(name, made)
			return NewNull(), nil
		}
		if !made.IsOpen() {
			return NewNull(), fmt.Errorf("%s: the variable '%s' is not one of a call in progress", LinkFunction, name)
		}

		// The kept upvalue becomes the one of the new variable's register
		stack := made.stack
		stack.vals[made.slot] = kept.Get()
		kept.stack, kept.slot = stack, made.slot
		for i, open := range stack.open {
			if open == made {
				stack.open[i] = kept
			}
		}
		reader.upvalues[0] = kept
		return NewNull(), nil
	})

	vm.RegisterExtern(ShowFunction, func(vm *VM, args []Value) (Value, error) {
		if len(args) != 1 {
			return NewNull(), fmt.Errorf("%s expects 1 argument, got %d", ShowFunction, len(args))
		}
		vm.session.shown = FormatLiteral(args[0])
		vm.session.hasShown = true
		return NewNull(), nil
	})
}

// variableOf reads the arguments of __repl_keep and __repl_link: a name,
// and a closure that captured the variable and nothing else.
func (vm *VM) variableOf(function string, args []Value) (string, *UpvalueObj, *FunctionObj, error) {
	if len(args) != 2 {
		return "", nil, nil, fmt.Errorf("%s expects 2 arguments, got %d", function, len(args))
	}
	name, err := argString(args, 0)
	if err != nil {
		return "", nil, nil, err
	}
	if kind, ok := heapType(args[1]); !ok || kind != ObjTypeFunction {
		return "", nil, nil, fmt.Errorf("%s: argument 2 must be a function that reads the variable, got %s", function, typeName(args[1]))
	}
	reader := (*FunctionObj)(unsafe.Pointer(args[1].AsHeapPtr()))
	if len(reader.upvalues) != 1 || reader.upvalues[0] == nil {
		return "", nil, nil, fmt.Errorf("%s: the function has to read the variable '%s' and nothing else", function, name)
	}
	return name, reader.upvalues[0], reader, nil
}

// keep makes an upvalue the variable of a name, and gives programs the
// function that reads it.
func (vm *VM) keep(name string, cell *UpvalueObj) {
	vm.session.cells[name] = cell
	vm.RegisterExtern(GetPrefix+name, func(vm *VM, args []Value) (Value, error) {
		kept, ok := vm.session.cells[name]
		if !ok {
			return NewNull(), fmt.Errorf("the variable '%s' is not kept any more", name)
		}
		return kept.Get(), nil
	})
}

// SettleVariables is called after each program of a session. One that
// failed did not return: the variables it was given are still registers
// of its stack, which nothing will run on again. They take their values
// with them, as they do when a program returns.
func (vm *VM) SettleVariables() {
	if vm.session == nil {
		return
	}
	for _, cell := range vm.session.cells {
		cell.Close()
	}
}

// Variable returns the value of a kept variable.
func (vm *VM) Variable(name string) (Value, bool) {
	if vm.session == nil {
		return NewNull(), false
	}
	cell, ok := vm.session.cells[name]
	if !ok {
		return NewNull(), false
	}
	return cell.Get(), true
}

// Variables lists the names of the kept variables, sorted.
func (vm *VM) Variables() []string {
	if vm.session == nil {
		return nil
	}
	names := make([]string, 0, len(vm.session.cells))
	for name := range vm.session.cells {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ForgetVariable stops keeping a variable. Closures that captured it go
// on sharing it among themselves.
func (vm *VM) ForgetVariable(name string) {
	if vm.session != nil {
		delete(vm.session.cells, name)
		delete(vm.externFuncs, GetPrefix+name)
		delete(vm.externObjs, GetPrefix+name)
	}
}

// TakeShown returns what the last program handed to __repl_show, as it
// is written in a program: a text in quotes. It is given once.
func (vm *VM) TakeShown() (string, bool) {
	if vm.session == nil || !vm.session.hasShown {
		return "", false
	}
	text := vm.session.shown
	vm.session.shown, vm.session.hasShown = "", false
	return text, true
}

// markSession marks the kept variables: nothing else may hold them
// between two programs.
func (vm *VM) markSession() {
	if vm.session == nil {
		return
	}
	for _, cell := range vm.session.cells {
		vm.markObject(&cell.header)
	}
}

// FormatLiteral writes a value as FormatValue does, except that a text
// is in quotes: the way the value is written in a program, which is what
// tells "12" from 12 when a value is shown on its own.
func FormatLiteral(v Value) string {
	if text, ok := stringValue(v); ok {
		return strconv.Quote(text)
	}
	return FormatValue(v)
}
