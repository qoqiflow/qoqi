package vm

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/qoqiflow/qoqi/internal/sqldb"
)

const (
	// DefaultMaxCallDepth bounds how deep calls may nest, so that runaway
	// recursion ends with an error instead of exhausting memory.
	DefaultMaxCallDepth = 10000
)

// CallFrame represents a function call frame.
// CallFrame is where a caller was when it made a call: what to go back to
// when the call returns.
type CallFrame struct {
	function *FunctionObj // The calling function, whose chunk is its bytecode
	ip       int          // The instruction that made the call
	base     int          // Where the caller's registers start on the stack
	size     int          // How many it has
}

// ExternFunc is a function callable from bytecode.
type ExternFunc func(*VM, []Value) (Value, error)

// DebugHook is a function called before each instruction execution.
// It receives a snapshot of the VM state and returns true if execution should pause.
type DebugHook func(snapshot interface{}) bool

// VM represents the Qoqi virtual machine.
type VM struct {
	frames          []CallFrame
	frameCount      int          // Number of active frames
	ip              int          // instruction pointer
	stk             *regStack    // Registers of the calls in progress (see stack.go)
	base            int          // Where the current function's registers start there
	regs            []Value      // Current registers: a window on the stack
	currentFunction *FunctionObj // NEW: Current executing function (for GC root and LDCAP)
	chunk           *Chunk       // Current chunk
	heap            *Heap
	externFuncs     map[string]ExternFunc // External functions
	debugHook       DebugHook             // Optional debug hook
	out             io.Writer             // Destination of print/println
	castTypes       map[string]*castType  // Parsed cast descriptors, by text

	externSignatures map[string]string // Declared types of external functions

	maxCallDepth int

	// Function objects that never differ between uses are created once:
	// a function without captured variables (by prototype index) and a host
	// function (by name). Without this every call of a function by its name
	// would allocate an object just to call it.
	externObjs map[string]Value

	// The texts of one ASCII character, made once each: what indexing a
	// text gives, most of the time
	charStrings [128]Value

	// What is kept for each chunk that has code running (see chunkCache)
	caches map[*Chunk]*chunkCache

	// The calls of frameless functions in progress (see compiled.go): calls
	// as much as frames are, for how deep a program may go
	framelessCalls int

	// What compiled functions read of caches: the chunk asked about last,
	// and what is kept for it (see keptNow)
	keptChunk *Chunk
	keptNow   *chunkCache

	gray []*ObjHeader // Objects marked, whose contents are still to be (see gc.go)

	// Use from several threads (see threads.go)
	execMu      sync.Mutex  // One program runs at a time
	interrupted atomic.Bool // Interrupt was called
	interruptMu sync.Mutex
	interrupt   chan struct{} // Closed when it is

	// What the host lets a program do (see limits.go)
	stepLimit uint64                 // How long it may run, in steps; 0 for as long as it takes
	steps     uint64                 // How many it has taken
	permitted func(name string) bool // Which native functions it may use; nil for all

	// State of the native functions (see native.go)
	outcome     Value                 // What the last protected call returned (see protect.go)
	failure     *RuntimeError         // ...or how it failed
	pinned      []Value               // Values a running native keeps alive
	handlers    []handler             // Where the calls in progress go on after a failure (see handlers.go)
	pending     []pendingFailure      // The failures their handlers are running for
	problem     string                // Why the last file operation failed (std.io)...
	args        []string              // What the program was started with (std.os.args)
	program     string                // The file it was run from (std.os.program)
	problemKind string                // ...and of which kind
	servers     map[int64]*httpServer // The servers of std.http, by number
	lastServer  int64
	in          *bufio.Reader // Input of std.io.readLine
	rng         *rand.Rand    // Generator of std.random
	inLock      sync.Mutex    // One task reads the input at a time

	// The tasks of the program and whose turn it is (see task.go)
	sched *scheduler

	// Variables kept from one program to the next (see repl.go); nil
	// unless the VM is that of a REPL
	session *keptVariables

	// Freezing a program that waits (see freeze.go): what is asked before
	// a task waits, the native it waits in while that is asked, and how
	// many instruction loops are running, one inside the other when a
	// native calls back into the program
	onWait  func(vm *VM, waiting string) error
	onStep  func(vm *VM, call *StepCall) (Value, bool, error)
	onIdle  func(vm *VM) error
	onStage func(vm *VM, name string) error
	stepKey string // the step running, for std.flow.key and attempt
	stepTry int
	// store opens the database of flow.store(), the store's; nil outside
	// a runtime of flows that keeps its store in one
	store func() (*sql.DB, sqldb.Kind, error)
	// What a step its queue carries out was given, as data, kept when it
	// was called: its body reads no parameter, so their registers are
	// free for what it works out before it leaves
	stepGiven [][]byte
	undos     []Undo // noted by flow.undo, until the runtime takes them
	// The runtime of flows, and the instances tasks wait for, by name
	flowHost FlowHost
	queues   Queues
	awaits   map[string][]int64

	// clock, when set, is the time now in milliseconds since 1970, in
	// place of the machine's: a trial of a flow has a clock of its own
	clock     func() int64
	waiting   string
	waitingMs int64
	executing int
}

// NewVM creates a new VM instance.
func NewVM() *VM {
	return NewVMWithHeapSize(DefaultHeapSize)
}

// NewVMWithHeapSize creates a new VM with a custom heap size.
func NewVMWithHeapSize(heapSize uint64) *VM {
	vm := &VM{
		regs:        make([]Value, 0),
		stk:         newRegStack(0),
		heap:        NewHeap(heapSize),
		externFuncs: make(map[string]ExternFunc),
		out:         os.Stdout,
		castTypes:   make(map[string]*castType),
		outcome:     NewNull(),

		externSignatures: make(map[string]string),
		maxCallDepth:     DefaultMaxCallDepth,
		externObjs:       make(map[string]Value),
		caches:           make(map[*Chunk]*chunkCache),
		sched:            newScheduler(),
		interrupt:        make(chan struct{}),
	}
	vm.heap.collect = vm.CollectGarbage
	vm.registerBuiltins()
	vm.registerNativeFunctions()
	return vm
}

// RegisterExtern registers an external function.
func (vm *VM) RegisterExtern(name string, fn ExternFunc) {
	vm.externFuncs[name] = fn
}

// RegisterTypedExtern registers an external function together with its
// type, written as a cast descriptor ("fn(any)->string"). A function with a
// declared type can be cast to a function type; one without cannot.
func (vm *VM) RegisterTypedExtern(name, signature string, fn ExternFunc) {
	vm.externFuncs[name] = fn
	vm.externSignatures[name] = signature
}

// SetMaxCallDepth sets how deep calls may nest (default DefaultMaxCallDepth).
func (vm *VM) SetMaxCallDepth(depth int) {
	if depth > 0 {
		vm.maxCallDepth = depth
	}
}

// SetOutput redirects the output of print/println (default: os.Stdout).
func (vm *VM) SetOutput(w io.Writer) {
	vm.out = w
}

// registerBuiltins registers built-in external functions.
func (vm *VM) registerBuiltins() {
	// The values are all written before anything is: a show that fails
	// stops the program without half a line
	write := func(args []Value, end string) error {
		parts := make([]string, len(args))
		for i, arg := range args {
			text, err := vm.format(arg)
			if err != nil {
				return err
			}
			parts[i] = text
		}
		fmt.Fprint(vm.out, strings.Join(parts, " ")+end)
		return nil
	}

	// print function
	vm.RegisterTypedExtern("print", "fn(...any)->void", func(vm *VM, args []Value) (Value, error) {
		return NewNull(), write(args, "")
	})

	// println function
	vm.RegisterTypedExtern("println", "fn(...any)->void", func(vm *VM, args []Value) (Value, error) {
		return NewNull(), write(args, "\n")
	})

	// panic function: stops the program. The message is written like the
	// arguments of print. The VM turns the error into a failure at the
	// place of the call.
	vm.RegisterTypedExtern("panic", "fn(...any)->void", func(vm *VM, args []Value) (Value, error) {
		parts := make([]string, len(args))
		for i, arg := range args {
			// By its show where it has one, as print does: the reason
			// expect stops with reads as a sentence
			text, err := vm.format(arg)
			if err != nil {
				text = FormatValue(arg)
			}
			parts[i] = text
		}
		return NewNull(), &panicError{message: strings.Join(parts, " ")}
	})

	// toString function
	vm.RegisterTypedExtern("toString", "fn(any)->string", func(vm *VM, args []Value) (Value, error) {
		if len(args) != 1 {
			return NewNull(), fmt.Errorf("toString expects 1 argument, got %d", len(args))
		}
		return vm.textOf(args[0])
	})

	// __text intrinsic: an expression inside a string, "${value}"
	vm.RegisterTypedExtern("__text", "fn(any)->string", func(vm *VM, args []Value) (Value, error) {
		if len(args) != 1 {
			return NewNull(), fmt.Errorf("__text expects 1 argument, got %d", len(args))
		}
		return vm.textOf(args[0])
	})

	// __cast intrinsic: `value as Type`. Returns the value if it is what
	// the descriptor says, fails otherwise.
	vm.RegisterExtern("__cast", func(vm *VM, args []Value) (Value, error) {
		if len(args) != 2 {
			return NewNull(), fmt.Errorf("__cast expects 2 arguments, got %d", len(args))
		}
		descriptor, ok := stringValue(args[1])
		if !ok {
			return NewNull(), fmt.Errorf("__cast expects a type descriptor")
		}
		return vm.cast(args[0], descriptor)
	})

	// __is intrinsic: `value is Type`. True if the value is what the
	// descriptor says.
	vm.RegisterExtern("__is", func(vm *VM, args []Value) (Value, error) {
		if len(args) != 2 {
			return NewNull(), fmt.Errorf("__is expects 2 arguments, got %d", len(args))
		}
		descriptor, ok := stringValue(args[1])
		if !ok {
			return NewNull(), fmt.Errorf("__is expects a type descriptor")
		}
		typ, err := vm.castTypeFor(descriptor)
		if err != nil {
			return NewNull(), err
		}
		return NewBool(typ.mismatch(args[0]) == ""), nil
	})

	// __slice: items[1..3], the part of an array or a text a range takes
	vm.RegisterTypedExtern("__slice", "fn(any,int,int,bool,int)->any", func(vm *VM, args []Value) (Value, error) {
		return vm.slice(args)
	})

	// push function: appends a value to an array
	vm.RegisterTypedExtern("__push", "fn(any,any)->void", func(vm *VM, args []Value) (Value, error) {
		if len(args) != 2 {
			return NewNull(), fmt.Errorf("push expects 2 arguments, got %d", len(args))
		}
		if t, ok := heapType(args[0]); !ok || t != ObjTypeArray {
			return NewNull(), fmt.Errorf("push expects an array, got %s", typeName(args[0]))
		}
		if frozen(args[0]) {
			// Said as the program's mistake, at the line of the push
			return NewNull(), vm.runtimeError(errFrozen)
		}
		// The caller collects garbage after the call if it is due
		return NewNull(), vm.appendToArray((*ArrayObj)(unsafe.Pointer(args[0].AsHeapPtr())), args[1])
	})
}

// Ints are 48-bit signed.
const (
	MaxInt = int64(1)<<47 - 1
	MinInt = -(int64(1) << 47)
)

// intResult makes an Int of the result of an integer operation, or fails
// if it does not fit: an Int silently wrapping around would turn a large
// positive number into a negative one.
func (vm *VM) intResult(n int64) (Value, error) {
	if n < MinInt || n > MaxInt {
		return NewNull(), vm.runtimeError("integer overflow")
	}
	return NewInt(n), nil
}

// newString allocates a string on the VM heap. It does not collect garbage:
// the caller must store the value in a register first.
func (vm *VM) newString(s string) (Value, error) {
	strObj, _, err := vm.heap.AllocString(s)
	if err != nil {
		return NewNull(), err
	}
	return NewHeapPtr(uintptr(unsafe.Pointer(strObj))), nil
}

// appendToArray appends a value and accounts for the memory when the array
// has to grow. Counting growth is what lets the collector's threshold
// follow the size of the data; an array that grows unseen keeps the
// threshold low, and every collection then walks the whole array again.
func (vm *VM) appendToArray(arr *ArrayObj, v Value) error {
	if arr.header.Frozen {
		return fmt.Errorf(errFrozen)
	}
	before := cap(arr.elements)
	arr.Append(v)
	if after := cap(arr.elements); after > before {
		_, err := vm.heap.grow(uint64(after-before) * valueSize)
		return err
	}
	return nil
}

// setField gives a field of an object a value, and accounts for the memory
// when that makes the object larger.
func (vm *VM) setField(obj *ObjectObj, name string, v Value) error {
	if obj.header.Frozen {
		return fmt.Errorf(errFrozen)
	}
	before := cap(obj.slots)
	obj.Set(name, v)
	if after := cap(obj.slots); after > before {
		_, err := vm.heap.grow(uint64(after-before) * valueSize)
		return err
	}
	return nil
}

// charString is the text of one ASCII character.
func (vm *VM) charString(c byte) (Value, error) {
	if cached := vm.charStrings[c]; cached != 0 {
		return cached, nil
	}
	s, err := vm.newString(string(rune(c)))
	if err == nil {
		vm.charStrings[c] = s
	}
	return s, err
}

// collectIfNeeded runs the GC when the heap has passed its threshold. Call
// it only when every live value is reachable from a register.
func (vm *VM) collectIfNeeded() {
	if vm.heap.ShouldCollect() {
		vm.CollectGarbage()
	}
}

// fieldName resolves the field name operand of GETFIELD/SETFIELD/HASFIELD/
// DELFIELD. The constant is either an index into the string pool (what the
// assembler and compiler emit) or a pre-allocated heap string.
func (vm *VM) fieldName(op string, constIdx uint16) (string, error) {
	if int(constIdx) >= len(vm.chunk.Constants) {
		return "", vm.runtimeError(fmt.Sprintf("constant index out of bounds: %d", constIdx))
	}

	constVal := vm.chunk.Constants[constIdx]
	if constVal.IsInt() {
		strIdx := int(constVal.AsInt())
		if strIdx < 0 || strIdx >= len(vm.chunk.StringPool) {
			return "", vm.runtimeError(fmt.Sprintf("string pool index out of bounds: %d", strIdx))
		}
		return vm.chunk.StringPool[strIdx], nil
	}
	if name, ok := stringValue(constVal); ok {
		return name, nil
	}
	return "", vm.runtimeError(op + " requires string constant for field name")
}

// objectOperand returns the object a field instruction operates on.
func (vm *VM) objectOperand(op string, v Value) (*ObjectObj, error) {
	if t, ok := heapType(v); !ok || t != ObjTypeObject {
		return nil, vm.runtimeError(fmt.Sprintf("%s requires object, got %s", op, typeName(v)))
	}
	return (*ObjectObj)(unsafe.Pointer(v.AsHeapPtr())), nil
}

// arrayOperand returns the array an array instruction operates on.
func (vm *VM) arrayOperand(op string, v Value) (*ArrayObj, error) {
	if t, ok := heapType(v); !ok || t != ObjTypeArray {
		return nil, vm.runtimeError(fmt.Sprintf("%s requires array, got %s", op, typeName(v)))
	}
	return (*ArrayObj)(unsafe.Pointer(v.AsHeapPtr())), nil
}

// compareStrings applies an ordering operator to two strings.
func compareStrings(op OpCode, a, b string) bool {
	switch op {
	case OpLT:
		return a < b
	case OpLE:
		return a <= b
	case OpGT:
		return a > b
	default:
		return a >= b
	}
}

// stringPair returns both operands as strings when both are strings.
func stringPair(a, b Value) (string, string, bool) {
	sa, ok := stringValue(a)
	if !ok {
		return "", "", false
	}
	sb, ok := stringValue(b)
	return sa, sb, ok
}

// valuesEqual implements EQ: numbers compare as numbers, strings by
// content, everything else by identity.
//
// Values of an enum are equal when they are the same variant holding equal
// values. Structs and arrays are only equal to themselves. What a variant
// holds last is compared by going round again rather than by a call: a
// list made of variants, each holding the rest of the list, can be of any
// length.
func valuesEqual(a, b Value) bool {
	for {
		// Two Floats are compared as numbers, not as the bits that hold
		// them: zero equals minus zero, and NaN equals nothing, itself
		// included
		if a.IsNumber() && b.IsNumber() {
			return floatOf(a) == floatOf(b)
		}
		if a == b {
			return true
		}
		// A whole number and a Float: equal when they are the same number
		if a.IsNumber() && b.IsInt() {
			return floatOf(a) == float64(intOf(b))
		}
		if a.IsInt() && b.IsNumber() {
			return float64(intOf(a)) == floatOf(b)
		}
		ha, hb := header(a), header(b)
		if ha == nil || hb == nil || ha.Type != hb.Type {
			return false
		}
		switch ha.Type {
		case ObjTypeString:
			return (*StringObj)(unsafe.Pointer(ha)).Data() == (*StringObj)(unsafe.Pointer(hb)).Data()
		case ObjTypeObject:
		default:
			return false
		}

		va, vb := (*ObjectObj)(unsafe.Pointer(ha)), (*ObjectObj)(unsafe.Pointer(hb))
		// Values of one variant start from the same shape
		if !va.shape.variant || va.shape.root != vb.shape.root || len(va.slots) != len(vb.slots) {
			return false
		}
		last := len(va.slots) - 1
		if last < 0 {
			return true
		}
		if va.shape != vb.shape {
			// Not made the same way: by name, which is rare
			for i, key := range va.shape.keys {
				other, ok := vb.Get(key)
				if !ok || !valuesEqual(va.slots[i], other) {
					return false
				}
			}
			return true
		}
		for i := 0; i < last; i++ {
			if !valuesEqual(va.slots[i], vb.slots[i]) {
				return false
			}
		}
		a, b = va.slots[last], vb.slots[last]
	}
}

// variantObject returns the object behind a value of an enum.
func variantObject(v Value) (*ObjectObj, bool) {
	if t, ok := heapType(v); !ok || t != ObjTypeObject {
		return nil, false
	}
	obj := (*ObjectObj)(unsafe.Pointer(v.AsHeapPtr()))
	return obj, obj.shape.variant
}

// GetHeap returns the VM's heap.
func (vm *VM) GetHeap() *Heap {
	return vm.heap
}

// SetDebugHook sets the debug hook function.
func (vm *VM) SetDebugHook(hook DebugHook) {
	vm.debugHook = hook
}

// Snapshot returns a snapshot of the current VM state.
// The snapshot interface{} is actually a struct containing all VM state.
func (vm *VM) Snapshot() interface{} {
	// Create a copy of registers
	regsCopy := make([]Value, len(vm.regs))
	copy(regsCopy, vm.regs)

	// Create snapshot struct (defined in debugger package to avoid circular dependency)
	snapshot := struct {
		IP            int
		Instr         Instr
		DebugInfo     *DebugInfo
		Registers     []Value
		Constants     []Value
		Code          []Instr
		StackPointer  int
		FramePointer  int
		FrameCount    int
		HeapAllocated uint64
		HeapMax       uint64
		RegisterCount int
		ArgCount      int
	}{
		IP:            vm.ip,
		Instr:         Instr{},
		DebugInfo:     nil,
		Registers:     regsCopy,
		Constants:     vm.chunk.Constants,
		Code:          vm.chunk.Code,
		StackPointer:  vm.base + len(vm.regs),
		FramePointer:  vm.base,
		FrameCount:    vm.frameCount,
		HeapAllocated: vm.heap.bytesAllocated,
		HeapMax:       vm.heap.maxBytes,
		RegisterCount: len(vm.regs),
		ArgCount:      0,
	}

	// Add current instruction if in valid range
	if vm.ip < len(vm.chunk.Code) {
		snapshot.Instr = vm.chunk.Code[vm.ip]
		snapshot.DebugInfo = vm.chunk.GetDebugInfo(vm.ip)
	}

	return snapshot
}
