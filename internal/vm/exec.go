package vm

import (
	"fmt"
	"math"
	"strings"
	"unsafe"
)

// fieldCache is what an instruction that reaches a field remembers of the
// last object it met: its shape, and where the field is in objects of that
// shape. Values of one struct share a shape, so an instruction that keeps
// meeting the same struct finds the field without looking its name up.
type fieldCache struct {
	shape *Shape // The shape the object had
	next  *Shape // For a field that was added: the shape the object got
	slot  int32  // Where the field is; -1 if objects of that shape have none

	// For an instruction that finds a method (see methodOf): the number
	// the chunk gave the method's name, plus one
	other int32
}

// chunkCache is what the VM keeps for a chunk whose code it runs, so as
// not to work the same thing out at every instruction. It belongs to the
// chunk: a function may run long after the program it came with has ended
// (a value kept from one program of an interactive session to the next),
// and what it remembers must be about its own code.
type chunkCache struct {
	// What each instruction that reaches a field remembers
	fields []fieldCache

	// The function object of each function that captures nothing, made
	// the first time it is needed: calling a function by its name, or
	// taking it as a value, allocates nothing afterwards
	funcs []Value

	// The same for the host functions the chunk names
	externs []Value

	// The text of each string of the chunk, made the first time the
	// program reaches it. A text never changes, so the one object serves
	// every time: a text written in a loop is not made again at each turn.
	strings []Value

	// The values that are made once and handed out each time (see
	// ObjectIsConstant), by the instruction that makes them. Not there
	// until a first one is made.
	constants []Value

	// The functions of the chunk by their name, and the number given to
	// each method name a call has asked for (see method). Not there until
	// a first method is looked for.
	byName      map[string]int
	methodNames map[uint16]int32

	// How many SWITCH instructions have been given a number (see switchTo)
	switches int32

	// The value of each constant that is made once (see the ONCE
	// instruction), by the function that makes it, and whether it is
	// being made. Not there until a first one is.
	once   []Value
	making []bool
}

// noMethod is what is remembered of a type that has no method of a name.
const noMethod = -1

// methodNumber gives the name of a method, known by its constant, a small
// number of its own: the methods a type has are kept by it.
func (kept *chunkCache) methodNumber(chunk *Chunk, nameConst uint16) int32 {
	if number, ok := kept.methodNames[nameConst]; ok {
		return number
	}
	if kept.byName == nil {
		kept.byName = make(map[string]int, len(chunk.Prototypes))
		kept.methodNames = make(map[uint16]int32)
		for i := range chunk.Prototypes {
			kept.byName[chunk.Prototypes[i].Name] = i
		}
	}
	number := int32(len(kept.methodNames))
	kept.methodNames[nameConst] = number
	return number
}

// method returns the number of the function that is a method for the
// values that start with a shape, or noMethod. The method is known by the
// number its name was given.
//
// A method is a function of the program like any other, named after its
// type: "Point.moved", or "Shape.area" for every variant of the enum
// Shape. Nothing is kept on the values: a value says what it is a value
// of, and that is enough to find the function. What is found is kept with
// the type, so that the names are put together once per type and method.
func (kept *chunkCache) method(chunk *Chunk, root *Shape, number int32, nameConst uint16) int32 {
	if root.methodsOf != kept {
		// Another program's methods, or none yet
		root.methods, root.methodsOf = nil, kept
	}
	if int(number) < len(root.methods) {
		if found := root.methods[number]; found != 0 {
			return found - 2
		}
	} else {
		root.methods = append(root.methods, make([]int32, int(number)+1-len(root.methods))...)
	}

	owner := root.typeName
	if root.variant {
		// "Enum.Variant": the methods are the enum's
		if dot := strings.LastIndex(owner, "."); dot >= 0 {
			owner = owner[:dot]
		}
	}
	index := int32(noMethod)
	name := chunk.StringPool[intOf(chunk.Constants[nameConst])]
	if found, ok := kept.byName[owner+"."+name]; ok && owner != "" && chunk.Prototypes[found].UpvalueCount == 0 {
		index = int32(found)
	}
	root.methods[number] = index + 2
	return index
}

// caseOf is where the SWITCH at an instruction goes on for a value: the
// case of its type, or the place for none of them. It is kept out of the
// instruction loop, which is the faster for every instruction the
// smaller it is.
//
//go:noinline
func (kept *chunkCache) caseOf(chunk *Chunk, at int, known *fieldCache, v Value) int {
	h := header(v)
	if h == nil || h.Type != ObjTypeObject {
		return int(chunk.Code[at].C)
	}
	root := (*ObjectObj)(unsafe.Pointer(h)).shape.root
	if known.other == 0 {
		kept.switches++
		known.other = kept.switches
	}
	number := known.other - 1
	if root.casesOf == kept && int(number) < len(root.cases) && root.cases[number] != 0 {
		return int(root.cases[number]) - 1
	}
	return int(kept.switchTo(chunk, at, number, root))
}

// switchTo finds where a SWITCH goes on for the values of a type: the
// target of the CASE that names the type, or the one for none of them.
// The cases are gone through once per type; what is found is kept with the
// type, by the number of the SWITCH.
func (kept *chunkCache) switchTo(chunk *Chunk, at int, number int32, root *Shape) int32 {
	if root.casesOf != kept {
		root.cases, root.casesOf = nil, kept
	}
	if int(number) >= len(root.cases) {
		root.cases = append(root.cases, make([]int32, int(number)+1-len(root.cases))...)
	}

	instr := chunk.Code[at]
	target := int32(instr.C)
	for i := 1; i <= int(instr.B); i++ {
		option := chunk.Code[at+i]
		if chunk.StringPool[intOf(chunk.Constants[option.A])] == root.typeName {
			target = int32(option.B)
			break
		}
	}
	root.cases[number] = target + 1
	return target
}

// methodOf finds the method of a name on a value: the function object to
// call. The instruction that asks remembers the last type it met.
func (vm *VM) methodOf(v Value, chunk *Chunk, kept *chunkCache, known *fieldCache, nameConst uint16) (Value, error) {
	h := header(v)
	if h == nil || h.Type != ObjTypeObject {
		return NewNull(), vm.noSuchMethod(v, chunk, nameConst)
	}
	root := (*ObjectObj)(unsafe.Pointer(h)).shape.root
	if known.shape != root {
		// Another type than last time: its methods are kept with it
		if known.other == 0 {
			known.other = kept.methodNumber(chunk, nameConst) + 1
		}
		known.shape, known.slot = root, kept.method(chunk, root, known.other-1, nameConst)
	}
	if known.slot == noMethod {
		return NewNull(), vm.noSuchMethod(v, chunk, nameConst)
	}
	if callee := kept.funcs[known.slot]; callee != 0 {
		return callee, nil
	}
	return vm.functionObject(chunk, kept.funcs, int(known.slot))
}

// calledMethod is the function a CALLM calls: the method found on the
// value, checked to take the arguments it is given, unless the program is
// to stop here. Kept out of the instruction loop like caseOf.
//
//go:noinline
func (vm *VM) calledMethod(v Value, chunk *Chunk, kept *chunkCache, known *fieldCache, instr Instr) (*FunctionObj, error) {
	callee, err := vm.methodOf(v, chunk, kept, known, instr.C)
	if err != nil {
		return nil, err
	}
	funcObj := (*FunctionObj)(unsafe.Pointer(header(callee)))
	if int(instr.B) != funcObj.proto.ArgCount {
		return nil, vm.runtimeError(fmt.Sprintf("function %s expects %d arguments, got %d", funcObj.proto.Name, funcObj.proto.ArgCount, instr.B))
	}
	if vm.interrupted.Load() || vm.outOfSteps() {
		return nil, vm.stopped()
	}
	return funcObj, nil
}

func (vm *VM) noSuchMethod(v Value, chunk *Chunk, nameConst uint16) error {
	return vm.runtimeError(fmt.Sprintf("%s has no method '%s'", typeName(v), chunk.StringPool[intOf(chunk.Constants[nameConst])]))
}

// cacheFor returns what is kept for a chunk.
func (vm *VM) cacheFor(chunk *Chunk) *chunkCache {
	cache, ok := vm.caches[chunk]
	if !ok || len(cache.fields) != len(chunk.Code) || len(cache.funcs) != len(chunk.Prototypes) || len(cache.externs) != len(chunk.ExternNames) || len(cache.strings) != len(chunk.StringPool) {
		cache = &chunkCache{
			fields:  make([]fieldCache, len(chunk.Code)),
			funcs:   make([]Value, len(chunk.Prototypes)),
			externs: make([]Value, len(chunk.ExternNames)),
			strings: make([]Value, len(chunk.StringPool)),
		}
		vm.caches[chunk] = cache
	}
	return cache
}

// externObject returns the function object of a host function, making it
// the first time. One object per host function is enough.
func (vm *VM) externObject(name string) (Value, error) {
	if cached, ok := vm.externObjs[name]; ok {
		return cached, nil
	}
	externFunc, ok := vm.externFuncs[name]
	if !ok {
		return NewNull(), vm.runtimeError(fmt.Sprintf("unknown extern function: %s", name))
	}
	if vm.permitted != nil && !vm.permitted(name) {
		return NewNull(), vm.runtimeErrorOf(KindLibrary, name+": this program is not permitted to use it")
	}

	// argCount is -1: the function checks its arguments itself
	funcObj, _, err := vm.heap.AllocExternFunction(name, externFunc, -1)
	if err != nil {
		return NewNull(), vm.runtimeError(fmt.Sprintf("extern function allocation failed: %v", err))
	}
	funcObj.signature = vm.externSignatures[name]

	value := valueOf(&funcObj.header)
	vm.externObjs[name] = value
	return value, nil
}

// functionObject returns the function object of a function that captures
// nothing, making it the first time.
func (vm *VM) functionObject(chunk *Chunk, funcs []Value, index int) (Value, error) {
	if cached := funcs[index]; cached != 0 {
		return cached, nil
	}
	funcObj, _, err := vm.heap.AllocFunction(chunk, index, 0)
	if err != nil {
		return NewNull(), vm.runtimeError(fmt.Sprintf("function allocation failed: %v", err))
	}
	funcs[index] = valueOf(&funcObj.header)
	return funcs[index], nil
}

// Exec executes a chunk of bytecode.
//
// The chunk is checked first (see Verify): one that could make the VM
// read outside its registers or its tables is refused rather than run. A
// VM runs one program at a time; a second call waits for the first.
func (vm *VM) Exec(chunk *Chunk) (result Value, err error) {
	if chunk == nil {
		return NewNull(), fmt.Errorf("cannot execute nil chunk")
	}
	if err := Verify(chunk); err != nil {
		return NewNull(), err
	}

	vm.execMu.Lock()
	defer vm.execMu.Unlock()
	defer vm.clearInterrupt()

	mainIdx := chunk.FindPrototype("main")
	if mainIdx == -1 {
		return NewNull(), fmt.Errorf("entry function 'main' not found")
	}
	mainProto := &chunk.Prototypes[mainIdx]

	// A mistake of the VM itself, or of a native function, must not take
	// the host program down with it: it ends this program, as an error.
	defer func() {
		if r := recover(); r != nil {
			vm.sched.stop()
			result, err = NewNull(), vm.internalError(r)
		}
	}()
	vm.reset(chunk)

	// Main function doesn't capture any upvalues (upvalueCount = 0)
	mainFunc, _, err := vm.heap.AllocFunction(chunk, mainIdx, 0)
	if err != nil {
		return NewNull(), fmt.Errorf("failed to allocate main function: %v", err)
	}
	if mainProto.RegisterCount > len(vm.stk.vals) {
		vm.stk.grow(mainProto.RegisterCount)
	}
	vm.regs = vm.window(0, mainProto.RegisterCount)
	for i := range vm.regs {
		vm.regs[i] = valueNull
	}
	vm.ip = mainProto.CodeStart
	vm.currentFunction = mainFunc
	vm.collectIfNeeded()

	return vm.finish(vm.run(0))
}

// reset forgets what the last program left behind, and makes the program
// one task, main, about to run a chunk.
func (vm *VM) reset(chunk *Chunk) {
	vm.chunk = chunk
	vm.currentFunction = nil
	vm.frameCount = 0
	vm.frames = vm.frames[:0]
	vm.stk = newRegStack(1024)
	vm.base = 0
	vm.regs = nil
	vm.pinned = vm.pinned[:0]
	vm.outcome, vm.failure = NewNull(), nil
	vm.handlers, vm.pending = nil, nil
	vm.steps = 0
	vm.caches = map[*Chunk]*chunkCache{}
	vm.keptChunk, vm.keptNow = nil, nil
	vm.executing = 0
	vm.stepKey, vm.stepTry = "", 0
	vm.stepGiven = nil
	vm.undos, vm.awaits = nil, nil
	vm.sched.stop()
	vm.sched = newScheduler()
	vm.sched.chunk = chunk
	if vm.onIdle != nil {
		s := vm.sched
		s.onIdle, s.now = vm.onIdle, vm.now
		s.idle = &task{id: -1, status: taskWaiting, wake: make(chan struct{}, 1), fn: NewNull(), name: "idle"}
		go vm.idling(s)
	}
}

// internalError makes the error for a failure that is not the program's
// doing: something in the VM or in a native function went wrong.
func (vm *VM) internalError(r interface{}) error {
	failure := &RuntimeError{Kind: KindInternal, Message: fmt.Sprintf("internal error: %v", r), instruction: vm.ip}
	func() {
		// Where the program was, if the VM is still in a state to tell
		defer func() { _ = recover() }()
		failure.Stack = vm.callStack()
	}()
	return failure
}

// sameValue is EQ. Two values with the same bits are equal, with one
// exception, which valuesEqual knows: NaN is a Float that equals nothing.
// Whatever is not a Float is decided by its bits when they are the same.
func sameValue(a, b Value) bool {
	if a == b && !a.IsNumber() {
		return true
	}
	return valuesEqual(a, b)
}

// mulInts multiplies two Ints, and reports whether the product is one.
// Both fit in 48 bits, so their product can exceed 64; two that fit in 32
// cannot, which is nearly always, and only the others are divided back to
// find out.
func mulInts(x, y int64) (int64, bool) {
	product := x * y
	small := uint64(x+1<<31) < 1<<32 && uint64(y+1<<31) < 1<<32
	if !small && x != 0 && product/x != y {
		return 0, false
	}
	return product, product >= MinInt && product <= MaxInt
}

func bothInts(a, b Value) bool {
	return uint64(a)&kindMask == kindInt && uint64(b)&kindMask == kindInt
}

// numbers reads two operands as Floats when both are numbers and at least
// one is a Float: a whole number met by a Float is worked out as one.
// Two Ints are not its business: the instructions deal with those first.
func numbers(a, b Value) (x, y float64, ok bool) {
	switch {
	case a.IsNumber():
		x = floatOf(a)
	case a.IsInt():
		x = float64(intOf(a))
	default:
		return 0, 0, false
	}
	switch {
	case b.IsNumber():
		y = floatOf(b)
	case b.IsInt():
		y = float64(intOf(b))
	default:
		return 0, 0, false
	}
	return x, y, true
}

// arithmeticError says why an arithmetic instruction cannot work on its
// operands.
func (vm *VM) arithmeticError(name, symbol string, a, b Value) error {
	return vm.runtimeError(fmt.Sprintf("%s requires numeric operands, got: %s %s %s", name, a.String(), symbol, b.String()))
}

// concatenate is ADD on something other than two numbers of a kind: it
// joins two strings.
func (vm *VM) concatenate(a, b Value) (Value, error) {
	// Long texts are joined without being copied: see ropeMin
	if left, right := stringObject(a), stringObject(b); left != nil && right != nil && left.ByteLen()+right.ByteLen() >= ropeMin {
		str, _, err := vm.heap.AllocRope(left, right)
		if err != nil {
			return NewNull(), vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
		}
		return valueOf(&str.header), nil
	}
	sa, ok := stringValue(a)
	if !ok {
		return NewNull(), vm.arithmeticError("ADD", "+", a, b)
	}
	sb, ok := stringValue(b)
	if !ok {
		return NewNull(), vm.runtimeError(fmt.Sprintf("ADD cannot concatenate String and %s", typeName(b)))
	}
	str, _, err := vm.heap.AllocJoined(sa, sb)
	if err != nil {
		return NewNull(), vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
	}
	return valueOf(&str.header), nil
}

// stringObject is the text a value is, or nil.
func stringObject(v Value) *StringObj {
	if t, ok := heapType(v); !ok || t != ObjTypeString {
		return nil
	}
	return (*StringObj)(unsafe.Pointer(v.AsHeapPtr()))
}

// compare applies an ordering instruction to operands that are not two
// Ints: two Numbers or two Strings.
func (vm *VM) compare(op OpCode, a, b Value) (bool, error) {
	if x, y, ok := numbers(a, b); ok {
		switch op {
		case OpLT:
			return x < y, nil
		case OpLE:
			return x <= y, nil
		case OpGT:
			return x > y, nil
		default:
			return x >= y, nil
		}
	}
	if sa, sb, ok := stringPair(a, b); ok {
		return compareStrings(op, sa, sb), nil
	}
	name, symbol := "LT", "<"
	switch op {
	case OpLE:
		name, symbol = "LE", "<="
	case OpGT:
		name, symbol = "GT", ">"
	case OpGE:
		name, symbol = "GE", ">="
	}
	return false, vm.arithmeticError(name, symbol, a, b)
}

// isOfType reports whether a value is of the struct, or the variant of an
// enum, of a name: whether that is the name its shape carries.
//
// The instruction that asks remembers the last shape it was told yes for
// and the last it was told no for. Values of one type start from one
// shape, and a case of a match keeps meeting the same few: the names are
// compared once for each.
func isOfType(v Value, known *fieldCache, name string) bool {
	h := header(v)
	if h == nil || h.Type != ObjTypeObject {
		return false
	}
	root := (*ObjectObj)(unsafe.Pointer(h)).shape.root
	switch root {
	case known.shape:
		return true
	case known.next:
		return false
	}
	if root.typeName == name {
		known.shape = root
		return true
	}
	known.next = root
	return false
}

// testAgainst is a test that jumps, against a small number, on a value
// that is not an Int: what the comparison it stands for would do.
// againstConstant is the test against a small number that each test
// against a constant does: the same, with the number elsewhere.
var againstConstant = map[OpCode]OpCode{
	OpJNLTK: OpJNLTI, OpJNLEK: OpJNLEI, OpJNGTK: OpJNGTI, OpJNGEK: OpJNGEI, OpJNEQK: OpJNEQI, OpJNNEK: OpJNNEI,
}

func (vm *VM) testAgainst(op OpCode, a, k Value) (bool, error) {
	switch op {
	case OpJNEQI:
		return valuesEqual(a, k), nil
	case OpJNNEI:
		return !valuesEqual(a, k), nil
	case OpJNLTI:
		return vm.compare(OpLT, a, k)
	case OpJNLEI:
		return vm.compare(OpLE, a, k)
	case OpJNGTI:
		return vm.compare(OpGT, a, k)
	}
	return vm.compare(OpGE, a, k)
}

// shift applies SHL or SHR. A shift to the left that pushes bits out of an
// Int fails, as every other operation whose result does not fit does.
func (vm *VM) shift(op OpCode, a, b Value) (Value, error) {
	name, symbol := "SHL", "<<"
	if op == OpSHR {
		name, symbol = "SHR", ">>"
	}
	if !bothInts(a, b) {
		return NewNull(), vm.runtimeError(fmt.Sprintf("%s requires integer operands, got: %s %s %s", name, a.String(), symbol, b.String()))
	}
	n, count := intOf(a), intOf(b)
	if count < 0 {
		return NewNull(), vm.runtimeError(name + " shift count must be non-negative")
	}
	if op == OpSHR {
		if count > 63 {
			count = 63
		}
		return NewInt(n >> uint(count)), nil
	}
	if n == 0 {
		return a, nil
	}
	if count >= 48 {
		return NewNull(), vm.runtimeError("integer overflow")
	}
	shifted := n << uint(count)
	if shifted>>uint(count) != n {
		return NewNull(), vm.runtimeError("integer overflow")
	}
	return vm.intResult(shifted)
}

// toInt is TOINT on a Number: the whole part, if an Int can hold it.
func (vm *VM) toInt(f float64) (Value, error) {
	if f != f {
		return NewNull(), vm.runtimeError("integer overflow: NaN has no Int value")
	}
	f = math.Trunc(f)
	if f < float64(MinInt) || f > float64(MaxInt) {
		return NewNull(), vm.runtimeError("integer overflow")
	}
	return NewInt(int64(f)), nil
}

// callExtern calls a host function with the arguments the registers hold,
// and makes of an error it returns the failure of the program.
func (vm *VM) callExtern(externObj *ExternFunctionObj, args []Value) (Value, error) {
	// Optional argument count validation (if argCount >= 0)
	if externObj.argCount >= 0 && len(args) != externObj.argCount {
		return NewNull(), vm.runtimeError(fmt.Sprintf("extern function %s expects %d arguments, got %d", externObj.name, externObj.argCount, len(args)))
	}

	// The function may pin values; a native that calls back into the
	// program (see callFunction) runs other natives meanwhile, whose pins
	// come and go above its own.
	pinned := len(vm.pinned)
	retVal, err := externObj.function(vm, args)
	if err == nil {
		return retVal, nil
	}

	vm.pinned = vm.pinned[:pinned]
	if failure, ok := err.(*RuntimeError); ok {
		// Raised by the program the function called back into: already
		// complete, with its own place
		return NewNull(), failure
	}
	if leaving(err) {
		// The program is ending: not a failure
		return NewNull(), err
	}
	if stop, ok := err.(*panicError); ok {
		return NewNull(), vm.runtimeErrorOf(KindPanic, stop.message)
	}
	outOfMemory := strings.HasPrefix(err.Error(), "out of memory")
	if IsNative(externObj.name) && !outOfMemory {
		// A function of the standard library: named as the program
		// names it
		return NewNull(), vm.runtimeError(externObj.name + ": " + err.Error())
	}
	if strings.HasPrefix(externObj.name, "__") || outOfMemory {
		// Compiler intrinsic: the user wrote an operator, not a call, so
		// the function name means nothing. Running out of memory is not
		// the function's doing either.
		return NewNull(), vm.runtimeError(err.Error())
	}
	return NewNull(), vm.runtimeError(fmt.Sprintf("extern function %s failed: %v", externObj.name, err))
}

// run executes instructions until the function that was running when it
// was called returns, and gives back what it returned. base is the number
// of frames at that moment: 0 for main, more for a function called from a
// native function (see callFunction).
//
// A failure does not always end it: a call in progress that left a handler
// goes on there first (see handlers.go).
func (vm *VM) run(base int) (Value, error) {
	// A function compiled to Go runs as Go: it has no handlers to go on at
	if result, err, compiled := vm.enterCompiled(); compiled {
		return result, err
	}
	for {
		result, err := vm.execute(base)
		if err == nil || !vm.handle(base, err) {
			return result, err
		}
	}
}

// running is what the instruction loop reads of the call it is in: the
// chunk and what is kept for it, and the registers. The loop keeps it in
// one value in memory, not in variables of its own: a variable that lives
// from one instruction to the next and across a call somewhere in the loop
// is written to the stack at the start of every instruction, and there
// were eight of them.
type running struct {
	chunk   *Chunk
	code    []Instr
	consts  []Value
	kept    *chunkCache
	cache   []fieldCache
	funcs   []Value
	externs []Value
	regs    []Value
}

// running is the state of the call in progress, for the instruction loop.
func (vm *VM) running() running {
	kept := vm.cacheFor(vm.chunk)
	return running{
		chunk:   vm.chunk,
		code:    vm.chunk.Code,
		consts:  vm.chunk.Constants,
		kept:    kept,
		cache:   kept.fields,
		funcs:   kept.funcs,
		externs: kept.externs,
		regs:    vm.regs,
	}
}

// execute is run without the handlers: it stops at the first failure.
//
// The loop works on its own copies of what it reads at every instruction:
// the code, the registers, the instruction pointer. vm.ip is kept up to
// date, since errors, natives and the collector look at it; the others
// are taken from the VM again after anything that changes them, which is
// a call, a return, and a host function (it may call back into the
// program, or let another task have the turn).
func (vm *VM) execute(base int) (Value, error) {
	// How many loops run one inside the other (see Freeze). Not with a
	// defer: a task stopped as the program ends unwinds without the turn
	vm.executing++
	result, err := vm.loop(base)
	vm.executing--
	return result, err
}

func (vm *VM) loop(base int) (Value, error) {
	cur := vm.running()
	ip := vm.ip
	hooked := vm.debugHook != nil

	for ip < len(cur.code) {
		vm.ip = ip
		if hooked {
			vm.debugHook(vm.Snapshot())
		}

		instr := cur.code[ip]

		switch instr.Op {
		case OpNOP:
			// Do nothing

		case OpMOV:
			cur.regs[instr.A] = cur.regs[instr.B]

		case OpLOADK:
			cur.regs[instr.A] = cur.consts[instr.B]

		case OpLOADNULL:
			cur.regs[instr.A] = valueNull

		case OpLOADBOOL:
			cur.regs[instr.A] = NewBool(instr.B != 0)

		case OpLOADI:
			cur.regs[instr.A] = NewInt(int64(instr.B))

		case OpADD, OpADDK:
			a, b := cur.regs[instr.B], Value(0)
			if instr.Op == OpADD {
				b = cur.regs[instr.C]
			} else {
				b = cur.consts[instr.C]
			}
			if bothInts(a, b) {
				sum := intOf(a) + intOf(b)
				if sum < MinInt || sum > MaxInt {
					return NewNull(), vm.runtimeError("integer overflow")
				}
				cur.regs[instr.A] = NewInt(sum)
			} else if x, y, ok := numbers(a, b); ok {
				cur.regs[instr.A] = NewNumber(x + y)
			} else {
				str, err := vm.concatenate(a, b)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = str
				vm.collectIfNeeded()
			}

		case OpSUB, OpSUBK:
			a, b := cur.regs[instr.B], Value(0)
			if instr.Op == OpSUB {
				b = cur.regs[instr.C]
			} else {
				b = cur.consts[instr.C]
			}
			if bothInts(a, b) {
				diff := intOf(a) - intOf(b)
				if diff < MinInt || diff > MaxInt {
					return NewNull(), vm.runtimeError("integer overflow")
				}
				cur.regs[instr.A] = NewInt(diff)
			} else if x, y, ok := numbers(a, b); ok {
				cur.regs[instr.A] = NewNumber(x - y)
			} else {
				return NewNull(), vm.arithmeticError("SUB", "-", a, b)
			}

		case OpMUL, OpMULK:
			a, b := cur.regs[instr.B], Value(0)
			if instr.Op == OpMUL {
				b = cur.regs[instr.C]
			} else {
				b = cur.consts[instr.C]
			}
			if bothInts(a, b) {
				product, fits := mulInts(intOf(a), intOf(b))
				if !fits {
					return NewNull(), vm.runtimeError("integer overflow")
				}
				cur.regs[instr.A] = NewInt(product)
			} else if x, y, ok := numbers(a, b); ok {
				cur.regs[instr.A] = NewNumber(x * y)
			} else {
				return NewNull(), vm.arithmeticError("MUL", "*", a, b)
			}

		case OpDIV, OpDIVK:
			a, b := cur.regs[instr.B], Value(0)
			if instr.Op == OpDIV {
				b = cur.regs[instr.C]
			} else {
				b = cur.consts[instr.C]
			}
			if bothInts(a, b) {
				if intOf(b) == 0 {
					return NewNull(), vm.runtimeError("division by zero")
				}
				// The smallest Int divided by -1 has no Int result
				quotient, err := vm.intResult(intOf(a) / intOf(b))
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = quotient
			} else if x, y, ok := numbers(a, b); ok {
				cur.regs[instr.A] = NewNumber(x / y)
			} else {
				return NewNull(), vm.arithmeticError("DIV", "/", a, b)
			}

		case OpMOD, OpMODK:
			a, b := cur.regs[instr.B], Value(0)
			if instr.Op == OpMOD {
				b = cur.regs[instr.C]
			} else {
				b = cur.consts[instr.C]
			}
			if bothInts(a, b) {
				if intOf(b) == 0 {
					return NewNull(), vm.runtimeError("division by zero")
				}
				cur.regs[instr.A] = NewInt(intOf(a) % intOf(b))
			} else if x, y, ok := numbers(a, b); ok {
				cur.regs[instr.A] = NewNumber(math.Mod(x, y))
			} else {
				return NewNull(), vm.arithmeticError("MOD", "%", a, b)
			}

		case OpNEG:
			a := cur.regs[instr.B]
			switch {
			case a.IsInt():
				negated, err := vm.intResult(-intOf(a))
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = negated
			case a.IsNumber():
				cur.regs[instr.A] = NewNumber(-floatOf(a))
			default:
				return NewNull(), vm.runtimeError(fmt.Sprintf("NEG requires numeric operand, got: %s", a.String()))
			}

		case OpNOT:
			a := cur.regs[instr.B]
			if !a.IsBool() {
				return NewNull(), vm.runtimeError(fmt.Sprintf("NOT requires boolean operand, got: %s", a.String()))
			}
			cur.regs[instr.A] = NewBool(a != valueTrue)

		case OpTOINT:
			a := cur.regs[instr.B]
			switch {
			case a.IsInt():
				cur.regs[instr.A] = a
			case a.IsNumber():
				whole, err := vm.toInt(floatOf(a))
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = whole
			default:
				return NewNull(), vm.runtimeError(fmt.Sprintf("TOINT requires number or int, got: %s", a.String()))
			}

		case OpTONUM:
			a := cur.regs[instr.B]
			switch {
			case a.IsNumber():
				cur.regs[instr.A] = a
			case a.IsInt():
				cur.regs[instr.A] = NewNumber(float64(intOf(a)))
			default:
				return NewNull(), vm.runtimeError(fmt.Sprintf("TONUM requires int or number, got: %s", a.String()))
			}

		case OpBAND:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if !bothInts(a, b) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("BAND requires integer operands, got: %s & %s", a.String(), b.String()))
			}
			cur.regs[instr.A] = NewInt(intOf(a) & intOf(b))

		case OpBOR:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if !bothInts(a, b) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("BOR requires integer operands, got: %s | %s", a.String(), b.String()))
			}
			cur.regs[instr.A] = NewInt(intOf(a) | intOf(b))

		case OpBXOR:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if !bothInts(a, b) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("BXOR requires integer operands, got: %s ^ %s", a.String(), b.String()))
			}
			cur.regs[instr.A] = NewInt(intOf(a) ^ intOf(b))

		case OpSHL, OpSHR:
			shifted, err := vm.shift(instr.Op, cur.regs[instr.B], cur.regs[instr.C])
			if err != nil {
				return NewNull(), err
			}
			cur.regs[instr.A] = shifted

		case OpEQ, OpNE:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			equal := a == b && !a.IsNumber()
			if !equal {
				var err error
				if equal, err = vm.equal(a, b); err != nil {
					return NewNull(), err
				}
			}
			cur.regs[instr.A] = NewBool(equal == (instr.Op == OpEQ))

		case OpLT:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if bothInts(a, b) {
				cur.regs[instr.A] = NewBool(intOf(a) < intOf(b))
			} else {
				result, err := vm.compare(OpLT, a, b)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = NewBool(result)
			}

		case OpLE:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if bothInts(a, b) {
				cur.regs[instr.A] = NewBool(intOf(a) <= intOf(b))
			} else {
				result, err := vm.compare(OpLE, a, b)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = NewBool(result)
			}

		case OpGT:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if bothInts(a, b) {
				cur.regs[instr.A] = NewBool(intOf(a) > intOf(b))
			} else {
				result, err := vm.compare(OpGT, a, b)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = NewBool(result)
			}

		case OpGE:
			a, b := cur.regs[instr.B], cur.regs[instr.C]
			if bothInts(a, b) {
				cur.regs[instr.A] = NewBool(intOf(a) >= intOf(b))
			} else {
				result, err := vm.compare(OpGE, a, b)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = NewBool(result)
			}

		case OpLONG:
			// LONG prefix: the next word is the jump, the one after its
			// 32-bit offset, counted from the end of the three
			jump := cur.code[ip+1]
			target := ip + 3 + int(DecodeOffset32(cur.code[ip+2]))

			taken := true
			switch jump.Op {
			case OpTRY32:
				// Not a jump: where this call goes on if something fails
				vm.handlers = append(vm.handlers, handler{depth: vm.frameCount, ip: target, pinned: len(vm.pinned)})
				ip += 3
				continue
			case OpJMP32:
			case OpJMPIF32:
				taken = cur.regs[jump.A] == valueTrue
			case OpJMPIFNOT32:
				taken = cur.regs[jump.A] == valueFalse
			default:
				return NewNull(), vm.runtimeError(fmt.Sprintf("LONG: invalid opcode %v", jump.Op))
			}
			if !taken {
				ip += 3
				continue
			}
			if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
				return NewNull(), vm.stopped()
			}
			ip = target
			continue

		case OpJMP:
			// JMP: A = target instruction address
			target := int(instr.A)
			if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
				return NewNull(), vm.stopped()
			}
			ip = target
			continue

		case OpJMPIF, OpJMPIFNOT:
			// A = condition register, B = target address
			wanted := valueTrue
			if instr.Op == OpJMPIFNOT {
				wanted = valueFalse
			}
			if cur.regs[instr.A] == wanted {
				target := int(instr.B)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpCALL:
			// CALL: A = func_reg (register containing FunctionObj or ExternFunctionObj), B = nargs
			// Arguments are in r(func_reg+1), r(func_reg+2), ... r(func_reg+nargs)
			funcReg := int(instr.A)
			nargs := int(instr.B)

			callee := header(cur.regs[funcReg])
			if callee == nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("CALL requires function object in register r%d, got %v", funcReg, cur.regs[funcReg]))
			}
			args := cur.regs[funcReg+1 : funcReg+1+nargs : funcReg+1+nargs]

			switch callee.Type {
			case ObjTypeFunction:
				// User-defined function
				funcObj := (*FunctionObj)(unsafe.Pointer(callee))
				proto := funcObj.proto

				// STRICT validation: argument count must match exactly
				if nargs != proto.ArgCount {
					return NewNull(), vm.runtimeError(fmt.Sprintf("function %s expects %d arguments, got %d", proto.Name, proto.ArgCount, nargs))
				}
				if vm.interrupted.Load() || vm.outOfSteps() {
					return NewNull(), vm.stopped()
				}
				if proto.Step && vm.onStep != nil {
					pinned := len(vm.pinned)
					result, answered, err := vm.stepCalled(funcObj, args)
					if err != nil {
						return NewNull(), err
					}
					if answered {
						if vm.chunk != cur.chunk {
							cur = vm.running()
						}
						cur.regs = vm.regs
						cur.regs[0] = result
						vm.pinned = vm.pinned[:pinned]
						break
					}
				}
				if err := vm.enter(funcObj, args); err != nil {
					return NewNull(), err
				}
				if vm.chunk.Compiled != nil {
					// A function compiled to Go is called as Go
					if result, err, compiled := vm.enterCompiled(); compiled {
						if err != nil {
							return NewNull(), err
						}
						vm.leave()
						if vm.chunk != cur.chunk {
							cur = vm.running()
						}
						cur.regs = vm.regs
						cur.regs[0] = result
						ip = vm.ip + 1
						continue
					}
				}
				if vm.chunk != cur.chunk {
					cur = vm.running()
				}
				cur.regs = vm.regs
				ip = vm.ip
				continue

			case ObjTypeExternFunction:
				// External (host) function. The arguments are handed over
				// where they are, in the caller's registers.
				pinned := len(vm.pinned)
				retVal, err := vm.callExtern((*ExternFunctionObj)(unsafe.Pointer(callee)), args)
				if err != nil {
					return NewNull(), err
				}

				// The function may have called back into the program, or
				// let other tasks run: the registers may have moved. With
				// the result in r0 it is a root, and whatever the function
				// kept alive no longer needs to be.
				cur.regs = vm.regs
				cur.regs[0] = retVal
				vm.pinned = vm.pinned[:pinned]

				// Extern functions may allocate; collect now that the result is rooted
				vm.collectIfNeeded()

			default:
				return NewNull(), vm.runtimeError(fmt.Sprintf("CALL requires function object, got %v", callee.Type))
			}

		case OpCALLK:
			// A function of this chunk, known by its number: A = the
			// register before the arguments, B = nargs, C = the function.
			// That it takes B arguments was checked with the chunk.
			callee := cur.funcs[instr.C]
			if callee == 0 {
				made, err := vm.functionObject(cur.chunk, cur.funcs, int(instr.C))
				if err != nil {
					return NewNull(), err
				}
				callee = made
			}
			if vm.interrupted.Load() || vm.outOfSteps() {
				return NewNull(), vm.stopped()
			}
			first := int(instr.A) + 1
			last := first + int(instr.B)
			if fn := (*FunctionObj)(unsafe.Pointer(header(callee))); fn.proto.Step && vm.onStep != nil {
				pinned := len(vm.pinned)
				result, answered, err := vm.stepCalled(fn, cur.regs[first:last:last])
				if err != nil {
					return NewNull(), err
				}
				if answered {
					if vm.chunk != cur.chunk {
						cur = vm.running()
					}
					cur.regs = vm.regs
					cur.regs[0] = result
					vm.pinned = vm.pinned[:pinned]
					break
				}
			}
			if err := vm.enter((*FunctionObj)(unsafe.Pointer(header(callee))), cur.regs[first:last:last]); err != nil {
				return NewNull(), err
			}
			if vm.chunk.Compiled != nil {
				// A function compiled to Go is called as Go
				if result, err, compiled := vm.enterCompiled(); compiled {
					if err != nil {
						return NewNull(), err
					}
					vm.leave()
					if vm.chunk != cur.chunk {
						cur = vm.running()
					}
					cur.regs = vm.regs
					cur.regs[0] = result
					ip = vm.ip + 1
					continue
				}
			}
			cur.regs = vm.regs
			ip = vm.ip
			continue

		case OpCALLX:
			// A host function known by its number: A = the register before
			// the arguments, B = nargs, C = the host function
			callee := cur.externs[instr.C]
			if callee == 0 {
				object, err := vm.externObject(cur.chunk.ExternNames[instr.C])
				if err != nil {
					return NewNull(), err
				}
				callee = object
				cur.externs[instr.C] = callee
			}
			first := int(instr.A) + 1
			last := first + int(instr.B)
			pinned := len(vm.pinned)
			retVal, err := vm.callExtern((*ExternFunctionObj)(unsafe.Pointer(header(callee))), cur.regs[first:last:last])
			if err != nil {
				return NewNull(), err
			}
			cur.regs = vm.regs
			cur.regs[0] = retVal
			vm.pinned = vm.pinned[:pinned]
			vm.collectIfNeeded()

		case OpONCE:
			// The value of a constant: A = dest, B = the function that
			// makes it, which is only run the first time; C = 1 for a
			// variable, which is not frozen
			if cur.kept.once != nil && cur.kept.once[instr.B] != 0 {
				cur.regs[instr.A] = cur.kept.once[instr.B]
				break
			}
			value, err := vm.once(cur.chunk, cur.kept, int(instr.B), instr.C == 1)
			if err != nil {
				return NewNull(), err
			}
			cur.regs = vm.regs
			cur.regs[instr.A] = value

		case OpJNLT:
			// Go on at C unless A < B
			a, b := cur.regs[instr.A], cur.regs[instr.B]
			holds := false
			if bothInts(a, b) {
				holds = intOf(a) < intOf(b)
			} else {
				result, err := vm.compare(OpLT, a, b)
				if err != nil {
					return NewNull(), err
				}
				holds = result
			}
			if !holds {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpJNLE:
			a, b := cur.regs[instr.A], cur.regs[instr.B]
			holds := false
			if bothInts(a, b) {
				holds = intOf(a) <= intOf(b)
			} else {
				result, err := vm.compare(OpLE, a, b)
				if err != nil {
					return NewNull(), err
				}
				holds = result
			}
			if !holds {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpJNEQ, OpJNNE:
			a, b := cur.regs[instr.A], cur.regs[instr.B]
			equal := a == b && !a.IsNumber()
			if !equal {
				var err error
				if equal, err = vm.equal(a, b); err != nil {
					return NewNull(), err
				}
			}
			if equal != (instr.Op == OpJNEQ) {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpJNLTI, OpJNLEI, OpJNGTI, OpJNGEI, OpJNEQI, OpJNNEI:
			// The same against a small number: go on at C unless the test
			// of A against B holds
			a := cur.regs[instr.A]
			holds := false
			if a.IsInt() {
				n, k := intOf(a), int64(instr.B)
				switch instr.Op {
				case OpJNLTI:
					holds = n < k
				case OpJNLEI:
					holds = n <= k
				case OpJNGTI:
					holds = n > k
				case OpJNGEI:
					holds = n >= k
				case OpJNEQI:
					holds = n == k
				default:
					holds = n != k
				}
			} else {
				result, err := vm.testAgainst(instr.Op, a, NewInt(int64(instr.B)))
				if err != nil {
					return NewNull(), err
				}
				holds = result
			}
			if !holds {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpJNLTK, OpJNLEK, OpJNGTK, OpJNGEK, OpJNEQK, OpJNNEK:
			// The same against a constant: go on at C unless the test of A
			// against the number B holds
			a, k := cur.regs[instr.A], cur.consts[instr.B]
			holds := false
			if bothInts(a, k) {
				n, m := intOf(a), intOf(k)
				switch instr.Op {
				case OpJNLTK:
					holds = n < m
				case OpJNLEK:
					holds = n <= m
				case OpJNGTK:
					holds = n > m
				case OpJNGEK:
					holds = n >= m
				case OpJNEQK:
					holds = n == m
				default:
					holds = n != m
				}
			} else if x, y, ok := numbers(a, k); ok && instr.Op <= OpJNGEK {
				switch instr.Op {
				case OpJNLTK:
					holds = x < y
				case OpJNLEK:
					holds = x <= y
				case OpJNGTK:
					holds = x > y
				default:
					holds = x >= y
				}
			} else {
				result, err := vm.testAgainst(againstConstant[instr.Op], a, k)
				if err != nil {
					return NewNull(), err
				}
				holds = result
			}
			if !holds {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpADDI:
			a := cur.regs[instr.B]
			if a.IsInt() {
				sum := intOf(a) + int64(instr.C)
				if sum > MaxInt {
					return NewNull(), vm.runtimeError("integer overflow")
				}
				cur.regs[instr.A] = NewInt(sum)
			} else if a.IsNumber() {
				cur.regs[instr.A] = NewNumber(floatOf(a) + float64(instr.C))
			} else {
				// Whatever ADD does of a value that is not a number and
				// one that is
				result, err := vm.concatenate(a, NewInt(int64(instr.C)))
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = result
			}

		case OpSUBI:
			a := cur.regs[instr.B]
			if a.IsNumber() {
				cur.regs[instr.A] = NewNumber(floatOf(a) - float64(instr.C))
				break
			}
			if !a.IsInt() {
				return NewNull(), vm.arithmeticError("SUB", "-", a, NewInt(int64(instr.C)))
			}
			diff := intOf(a) - int64(instr.C)
			if diff < MinInt {
				return NewNull(), vm.runtimeError("integer overflow")
			}
			cur.regs[instr.A] = NewInt(diff)

		case OpSWITCH:
			// A = the value, B = the number of CASE words that follow,
			// C = where to go on for a value none of them names
			target := cur.kept.caseOf(cur.chunk, ip, &cur.cache[ip], cur.regs[instr.A])
			if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
				return NewNull(), vm.stopped()
			}
			ip = target
			continue

		case OpGETMETHOD:
			method, err := vm.methodOf(cur.regs[instr.B], cur.chunk, cur.kept, &cur.cache[ip], instr.C)
			if err != nil {
				return NewNull(), err
			}
			cur.regs[instr.A] = method
			vm.collectIfNeeded()

		case OpCALLM:
			// A call of a method found on its first argument: A = the
			// register before the arguments, B = nargs, C = the name
			first := int(instr.A) + 1
			last := first + int(instr.B)
			funcObj, err := vm.calledMethod(cur.regs[first], cur.chunk, cur.kept, &cur.cache[ip], instr)
			if err != nil {
				return NewNull(), err
			}
			if err := vm.enter(funcObj, cur.regs[first:last:last]); err != nil {
				return NewNull(), err
			}
			if vm.chunk.Compiled != nil {
				// A function compiled to Go is called as Go
				if result, err, compiled := vm.enterCompiled(); compiled {
					if err != nil {
						return NewNull(), err
					}
					vm.leave()
					if vm.chunk != cur.chunk {
						cur = vm.running()
					}
					cur.regs = vm.regs
					cur.regs[0] = result
					ip = vm.ip + 1
					continue
				}
			}
			cur.regs = vm.regs
			ip = vm.ip
			continue

		case OpISTYPE:
			cur.regs[instr.A] = NewBool(isOfType(cur.regs[instr.B], &cur.cache[ip], cur.chunk.StringPool[intOf(cur.consts[instr.C])]))

		case OpJNTYPE:
			// Go on at C unless the value is of the type
			if !isOfType(cur.regs[instr.A], &cur.cache[ip], cur.chunk.StringPool[intOf(cur.consts[instr.B])]) {
				target := int(instr.C)
				if target <= ip && (vm.interrupted.Load() || vm.outOfSteps()) {
					return NewNull(), vm.stopped()
				}
				ip = target
				continue
			}

		case OpENDTRY:
			n := len(vm.handlers)
			if n == 0 || vm.handlers[n-1].depth != vm.frameCount {
				return NewNull(), vm.runtimeError("ENDTRY without a handler in this call")
			}
			vm.handlers = vm.handlers[:n-1]

		case OpRETHROW:
			n := len(vm.pending)
			if n == 0 {
				return NewNull(), vm.runtimeError("RETHROW without a failure to pass on")
			}
			failure := vm.pending[n-1].failure
			vm.pending = vm.pending[:n-1]
			return NewNull(), failure

		case OpRET:
			// RET: A = register containing return value
			retVal := cur.regs[instr.A]

			// Variables of this call that closures captured move into the
			// closures: its registers are about to be someone else's
			if len(vm.stk.open) > 0 {
				vm.closeUpvalues(vm.base)
			}

			// The function this run was started for has returned
			if vm.frameCount == base {
				return retVal, nil
			}

			// Back to the caller, with the return value in its r0
			vm.leave()
			if vm.chunk != cur.chunk {
				cur = vm.running()
			}
			cur.regs = vm.regs
			cur.regs[0] = retVal
			ip = vm.ip + 1
			continue

		case OpNEWFUNC:
			// NEWFUNC: A = dest_reg, B = proto_idx
			// Creates a function object. If the prototype specifies upvalues,
			// they are allocated but not initialized.
			protoIdx := int(instr.B)
			proto := &cur.chunk.Prototypes[protoIdx]

			// A function that captures nothing is the same object every
			// time; only closures need one of their own
			if proto.UpvalueCount == 0 {
				shared, err := vm.functionObject(cur.chunk, cur.funcs, protoIdx)
				if err != nil {
					return NewNull(), err
				}
				cur.regs[instr.A] = shared
				vm.collectIfNeeded()
				break
			}

			funcObj, shouldGC, err := vm.heap.AllocFunction(cur.chunk, protoIdx, proto.UpvalueCount)
			if err != nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("function allocation failed: %v", err))
			}
			cur.regs[instr.A] = valueOf(&funcObj.header)
			if shouldGC {
				vm.CollectGarbage()
			}

		case OpCAPLOCAL:
			// CAPLOCAL: A = dest_func_reg, B = upvalue_idx, C = local_reg_idx
			funcObj, err := vm.closureOperand("CAPLOCAL", cur.regs[instr.A], int(instr.B))
			if err != nil {
				return NewNull(), err
			}
			upval, err := vm.findOrCreateUpvalue(vm.base + int(instr.C))
			if err != nil {
				return NewNull(), vm.runtimeError(err.Error())
			}
			funcObj.upvalues[instr.B] = upval
			vm.collectIfNeeded()

		case OpCAPUPVAL:
			// CAPUPVAL: A = dest_func_reg, B = upvalue_idx, C = parent_upval_idx
			funcObj, err := vm.closureOperand("CAPUPVAL", cur.regs[instr.A], int(instr.B))
			if err != nil {
				return NewNull(), err
			}
			parent := vm.currentFunction.upvalues
			if int(instr.C) >= len(parent) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("parent upvalue index out of bounds: %d", instr.C))
			}
			funcObj.upvalues[instr.B] = parent[instr.C]

		case OpNEWEXFUNC:
			// NEWEXFUNC: A = dest_reg, B = extern_idx
			object, err := vm.externObject(cur.chunk.ExternNames[instr.B])
			if err != nil {
				return NewNull(), err
			}
			cur.regs[instr.A] = object
			vm.collectIfNeeded()

		case OpCLOSEUP:
			// CLOSEUP: A = register. Closures that captured this register
			// get their own copy of its value and stop following it. Used
			// at the end of a loop iteration, so that each iteration's
			// closures keep that iteration's variables.
			if len(vm.stk.open) > 0 {
				vm.closeUpvalueAt(vm.base + int(instr.A))
			}

		case OpLDCAP:
			// LDCAP: A = dest_reg, B = upvalue_idx
			upvalues := vm.currentFunction.upvalues
			if int(instr.B) >= len(upvalues) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("LDCAP: upvalue index out of bounds: %d", instr.B))
			}
			upval := upvalues[instr.B]
			if upval == nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("LDCAP: uninitialized upvalue at index %d", instr.B))
			}
			cur.regs[instr.A] = upval.Get()

		case OpSTCAP:
			// STCAP: A = upvalue_idx, B = src_reg
			upvalues := vm.currentFunction.upvalues
			if int(instr.A) >= len(upvalues) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("STCAP: upvalue index out of bounds: %d", instr.A))
			}
			upval := upvalues[instr.A]
			if upval == nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("STCAP: uninitialized upvalue at index %d", instr.A))
			}
			upval.Set(cur.regs[instr.B])

		case OpNEWSTR:
			// The text of a string of the pool: one object per string
			index := intOf(cur.consts[instr.B])
			if made := cur.kept.strings[index]; made != 0 {
				cur.regs[instr.A] = made
				break
			}
			strObj, shouldGC, err := vm.heap.AllocString(cur.chunk.StringPool[index])
			if err != nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
			}
			cur.regs[instr.A] = valueOf(&strObj.header)
			cur.kept.strings[index] = cur.regs[instr.A]
			if shouldGC {
				vm.CollectGarbage()
			}

		case OpNEWARRAY:
			// Allocate a new array
			obj, shouldGC, err := vm.heap.AllocArray(int(instr.B))
			if err != nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("array allocation failed: %v", err))
			}
			cur.regs[instr.A] = valueOf(&obj.header)
			if shouldGC {
				vm.CollectGarbage()
			}

		case OpNEWOBJ:
			// Allocate a new object: A = dest. C says what it is a value of
			// (see ObjectIsNamed); B is then the constant of the name.
			known := &cur.cache[ip]
			if known.shape == nil {
				// What the values made here start as, found once
				known.shape = vm.heap.rootShape
				if kind := instr.C & ObjectKindMask; kind == ObjectIsNamed || kind == ObjectIsVariant {
					known.shape = vm.heap.RootShape(cur.chunk.StringPool[intOf(cur.consts[instr.B])], kind == ObjectIsVariant)
				}
			}
			if instr.C&ObjectIsConstant != 0 {
				// A value that holds nothing and never will: the same one
				// every time
				if cur.kept.constants == nil {
					cur.kept.constants = make([]Value, len(cur.code))
				}
				if cur.kept.constants[ip] != 0 {
					cur.regs[instr.A] = cur.kept.constants[ip]
					break
				}
			}

			obj, shouldGC, err := vm.heap.AllocObjectOf(known.shape, int(instr.C>>ObjectFieldsShift))
			if err != nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("object allocation failed: %v", err))
			}
			cur.regs[instr.A] = valueOf(&obj.header)
			if instr.C&ObjectIsConstant != 0 {
				cur.kept.constants[ip] = cur.regs[instr.A]
			}
			if shouldGC {
				vm.CollectGarbage()
			}

		case OpGETINDEX:
			// Get element: A = dest, B = array or string, C = index
			indexVal := cur.regs[instr.C]
			if !indexVal.IsInt() {
				return NewNull(), vm.runtimeError("GETINDEX requires integer index")
			}
			index := int(intOf(indexVal))

			target := header(cur.regs[instr.B])
			if target != nil && target.Type == ObjTypeArray {
				elements := (*ArrayObj)(unsafe.Pointer(target)).elements
				if index < 0 || index >= len(elements) {
					return NewNull(), vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, len(elements)))
				}
				cur.regs[instr.A] = elements[index]
				break
			}
			if target == nil || target.Type != ObjTypeString {
				_, err := vm.arrayOperand("GETINDEX", cur.regs[instr.B])
				return NewNull(), err
			}

			// Indexing a string yields the character as a string
			str := (*StringObj)(unsafe.Pointer(target))
			if length := str.RuneLen(); index < 0 || index >= length {
				return NewNull(), vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, length))
			}
			char := str.RuneAt(index)
			if len(char) == 1 {
				// One of 128 texts, each made once
				value, err := vm.charString(char[0])
				if err != nil {
					return NewNull(), vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
				}
				cur.regs[instr.A] = value
				break
			}
			value, err := vm.newString(char)
			if err != nil {
				return NewNull(), vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
			}
			cur.regs[instr.A] = value
			vm.collectIfNeeded()

		case OpSETINDEX:
			// Set array element: A = array, B = index, C = value
			indexVal := cur.regs[instr.B]
			if !indexVal.IsInt() {
				return NewNull(), vm.runtimeError("SETINDEX requires integer index")
			}
			index := int(intOf(indexVal))

			arrayObj, err := vm.arrayOperand("SETINDEX", cur.regs[instr.A])
			if err != nil {
				return NewNull(), err
			}
			// Writing past the length grows the array, up to its capacity
			// (which was accounted for when the array was created)
			if index < 0 || index >= cap(arrayObj.elements) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, arrayObj.Len()))
			}
			if arrayObj.header.Frozen {
				return NewNull(), vm.runtimeError(errFrozen)
			}
			arrayObj.Set(index, cur.regs[instr.C])

		case OpSETELEM:
			// Replace an array element: A = array, B = index, C = value.
			// Unlike SETINDEX it never grows the array: the index has to
			// name an existing element. This is what `xs[i] = v` compiles to.
			indexVal := cur.regs[instr.B]
			if !indexVal.IsInt() {
				return NewNull(), vm.runtimeError("SETELEM requires integer index")
			}
			index := int(intOf(indexVal))

			arrayObj, err := vm.arrayOperand("SETELEM", cur.regs[instr.A])
			if err != nil {
				return NewNull(), err
			}
			if index < 0 || index >= len(arrayObj.elements) {
				return NewNull(), vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, arrayObj.Len()))
			}
			if arrayObj.header.Frozen {
				return NewNull(), vm.runtimeError(errFrozen)
			}
			arrayObj.elements[index] = cur.regs[instr.C]

		case OpGETFIELD:
			// Get object field: A = dest, B = object, C = const index (field name)
			target := header(cur.regs[instr.B])
			if target == nil || target.Type != ObjTypeObject {
				_, err := vm.objectOperand("GETFIELD", cur.regs[instr.B])
				return NewNull(), err
			}
			obj := (*ObjectObj)(unsafe.Pointer(target))

			// The same shape as last time: the field is where it was
			known := &cur.cache[ip]
			if known.shape != obj.shape {
				name, err := vm.fieldName("GETFIELD", instr.C)
				if err != nil {
					return NewNull(), err
				}
				known.shape, known.slot = obj.shape, -1
				if slot, ok := obj.shape.index[name]; ok {
					known.slot = int32(slot)
				}
			}
			if known.slot >= 0 {
				cur.regs[instr.A] = obj.slots[known.slot]
			} else {
				cur.regs[instr.A] = valueNull
			}

		case OpSETFIELD:
			// Set object field: A = object, B = const index (field name), C = value
			target := header(cur.regs[instr.A])
			if target == nil || target.Type != ObjTypeObject {
				_, err := vm.objectOperand("SETFIELD", cur.regs[instr.A])
				return NewNull(), err
			}
			if target.Frozen {
				return NewNull(), vm.runtimeError(errFrozen)
			}
			obj := (*ObjectObj)(unsafe.Pointer(target))

			known := &cur.cache[ip]
			if known.shape != obj.shape {
				name, err := vm.fieldName("SETFIELD", instr.B)
				if err != nil {
					return NewNull(), err
				}
				known.shape, known.next = obj.shape, nil
				if slot, ok := obj.shape.index[name]; ok {
					known.slot = int32(slot)
				} else {
					known.slot = int32(len(obj.shape.keys))
					known.next = obj.shape.with(name)
				}
			}
			if known.next == nil {
				obj.slots[known.slot] = cur.regs[instr.C]
				break
			}

			// A new field: the object takes the shape of those that have it
			before := cap(obj.slots)
			obj.slots = append(obj.slots, cur.regs[instr.C])
			obj.shape = known.next
			if after := cap(obj.slots); after > before {
				if _, err := vm.heap.grow(uint64(after-before) * valueSize); err != nil {
					return NewNull(), vm.runtimeError(err.Error())
				}
				vm.collectIfNeeded()
			}

		case OpARRLEN:
			// Get length: A = dest, B = array or string.
			target := header(cur.regs[instr.B])
			if target == nil {
				_, err := vm.arrayOperand("ARRLEN", cur.regs[instr.B])
				return NewNull(), err
			}
			switch target.Type {
			case ObjTypeArray:
				cur.regs[instr.A] = NewInt(int64(len((*ArrayObj)(unsafe.Pointer(target)).elements)))
			case ObjTypeString:
				cur.regs[instr.A] = NewInt(int64((*StringObj)(unsafe.Pointer(target)).RuneLen()))
			default:
				_, err := vm.arrayOperand("ARRLEN", cur.regs[instr.B])
				return NewNull(), err
			}

		case OpARRPUSH:
			// Push value into array: A = array, B = value
			arrayObj, err := vm.arrayOperand("ARRPUSH", cur.regs[instr.A])
			if err != nil {
				return NewNull(), err
			}
			if arrayObj.header.Frozen {
				return NewNull(), vm.runtimeError(errFrozen)
			}
			before := cap(arrayObj.elements)
			arrayObj.elements = append(arrayObj.elements, cur.regs[instr.B])
			if after := cap(arrayObj.elements); after > before {
				if _, err := vm.heap.grow(uint64(after-before) * valueSize); err != nil {
					return NewNull(), vm.runtimeError(err.Error())
				}
				vm.collectIfNeeded()
			}

		case OpHASFIELD:
			// Check if object has field: A = dest bool, B = object, C = const index
			objObj, err := vm.objectOperand("HASFIELD", cur.regs[instr.B])
			if err != nil {
				return NewNull(), err
			}
			fieldName, err := vm.fieldName("HASFIELD", instr.C)
			if err != nil {
				return NewNull(), err
			}
			_, ok := objObj.Get(fieldName)
			cur.regs[instr.A] = NewBool(ok)

		case OpDELFIELD:
			// Delete object field: A = dest bool, B = object, C = const index
			objObj, err := vm.objectOperand("DELFIELD", cur.regs[instr.B])
			if err != nil {
				return NewNull(), err
			}
			fieldName, err := vm.fieldName("DELFIELD", instr.C)
			if err != nil {
				return NewNull(), err
			}
			if objObj.header.Frozen {
				return NewNull(), vm.runtimeError(errFrozen)
			}
			cur.regs[instr.A] = NewBool(objObj.Delete(fieldName))

		case OpGC:
			// Manually trigger garbage collection
			vm.CollectGarbage()

		default:
			return NewNull(), vm.runtimeError(fmt.Sprintf("unimplemented opcode: %v", instr.Op))
		}

		ip++
	}

	vm.ip = ip
	return NewNull(), nil
}

// closureOperand returns the function a capturing instruction fills in,
// having checked that it has the upvalue the instruction names.
func (vm *VM) closureOperand(op string, v Value, upvalue int) (*FunctionObj, error) {
	h := header(v)
	if h == nil || h.Type != ObjTypeFunction {
		return nil, vm.runtimeError(fmt.Sprintf("%s requires function object, got %s", op, typeName(v)))
	}
	funcObj := (*FunctionObj)(unsafe.Pointer(h))
	if upvalue >= len(funcObj.upvalues) {
		return nil, vm.runtimeError(fmt.Sprintf("upvalue index out of bounds: %d", upvalue))
	}
	return funcObj, nil
}
