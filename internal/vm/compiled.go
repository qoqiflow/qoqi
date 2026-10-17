package vm

import (
	"fmt"
	"unsafe"
)

// Functions compiled to Go (qoqi native).
//
// qoqi native turns the functions of a program into Go functions of this
// package, written into a copy of it next to the program's own main. Each
// does what the instruction loop does for the function's bytecode, with
// the loop taken away: a jump is a goto, a sum of two Ints is a sum, a call
// of a function known by its number is a call of its Go function.
//
// Everything else stays as it is. A call still goes through enter and
// leave, and the registers are still those of the stack: the collector, the
// tasks, stack traces and the limits see a compiled call as they see any
// other. A function the compiler does not know how to write stays
// bytecode, and the two call each other freely.

// CompiledFunc runs the body of a function compiled to Go. It is called
// once the call is entered, as the instruction loop would start the
// function, and returns what the function returns; the caller leaves the
// call.
type CompiledFunc func(vm *VM) (Value, error)

// compiledOf returns the compiled form of a function, if there is one.
func compiledOf(fn *FunctionObj) CompiledFunc {
	if compiled := fn.chunk.Compiled; compiled != nil {
		return compiled[fn.protoIdx]
	}
	return nil
}

// enterCompiled runs the function just entered, if it is compiled, and
// reports whether it was.
func (vm *VM) enterCompiled() (Value, error, bool) {
	fn := vm.currentFunction
	if fn == nil {
		return NewNull(), nil, false
	}
	compiled := compiledOf(fn)
	if compiled == nil {
		return NewNull(), nil, false
	}
	result, err := compiled(vm)
	return result, err, true
}

// kept is what is kept for the chunk of the call in progress: the same as
// the instruction loop uses (cacheFor), whose function objects the
// collector keeps, found once for each chunk a compiled function runs in.
func (vm *VM) kept() *chunkCache {
	if vm.keptChunk == vm.chunk {
		return vm.keptNow
	}
	return vm.keptFirst()
}

// keptFirst finds what is kept for a chunk compiled code has not run in
// before, or not last.
func (vm *VM) keptFirst() *chunkCache {
	vm.keptChunk, vm.keptNow = vm.chunk, vm.cacheFor(vm.chunk)
	return vm.keptNow
}

// enterCompiledCall enters a call that compiled code makes of a compiled
// function of the same chunk, with the arguments in its registers from
// first to last: what enter does, less what is the same on both sides
// (the chunk) and what compiled code sets before it is read (the place in
// the code). A call that goes too deep, or needs the stack to grow, is
// entered by enter, which fails or grows it.
func (vm *VM) enterCompiledCall(fn *FunctionObj, first, last int) error {
	stack := vm.stk
	base := vm.base + len(vm.regs)
	top := base + fn.proto.RegisterCount
	if vm.frameCount >= vm.maxCallDepth || top > len(stack.vals) {
		return vm.enter(fn, vm.regs[first:last:last])
	}
	frame := CallFrame{function: vm.currentFunction, ip: vm.ip, base: vm.base, size: len(vm.regs)}
	if n := vm.frameCount; n < len(vm.frames) {
		vm.frames[n] = frame
	} else {
		vm.frames = append(vm.frames, frame)
	}
	vm.frameCount++
	regs := stack.vals[base:top:top]
	// A few arguments: copied one by one, without calling memmove
	for i, arg := range vm.regs[first:last] {
		regs[i] = arg
	}
	if top > stack.used {
		stack.used = top
	}
	vm.currentFunction = fn
	vm.base = base
	vm.regs = regs
	return nil
}

// leaveCompiledCall ends a call enterCompiledCall entered. The frame keeps
// the function it held: nothing reads a frame above the calls in progress.
func (vm *VM) leaveCompiledCall() {
	vm.frameCount--
	frame := &vm.frames[vm.frameCount]
	vm.currentFunction = frame.function
	vm.ip = frame.ip
	vm.base = frame.base
	vm.regs = vm.stk.vals[frame.base : frame.base+frame.size : frame.base+frame.size]
}

// The instructions a compiled function leaves to a function of its own:
// each does what its case of the instruction loop does, on the registers
// of the call in progress, for the instruction at pc.

func (vm *VM) compiledNEWOBJ(kept *chunkCache, pc int, instr Instr) error {
	regs := vm.regs
	known := &kept.fields[pc]
	if known.shape == nil {
		known.shape = vm.heap.rootShape
		if kind := instr.C & ObjectKindMask; kind == ObjectIsNamed || kind == ObjectIsVariant {
			known.shape = vm.heap.RootShape(vm.chunk.StringPool[intOf(vm.chunk.Constants[instr.B])], kind == ObjectIsVariant)
		}
	}
	if instr.C&ObjectIsConstant != 0 {
		if kept.constants == nil {
			kept.constants = make([]Value, len(vm.chunk.Code))
		}
		if kept.constants[pc] != 0 {
			regs[instr.A] = kept.constants[pc]
			return nil
		}
	}
	obj, shouldGC, err := vm.heap.AllocObjectOf(known.shape, int(instr.C>>ObjectFieldsShift))
	if err != nil {
		return vm.runtimeError(fmt.Sprintf("object allocation failed: %v", err))
	}
	regs[instr.A] = valueOf(&obj.header)
	if instr.C&ObjectIsConstant != 0 {
		kept.constants[pc] = regs[instr.A]
	}
	if shouldGC {
		vm.CollectGarbage()
	}
	return nil
}

func (vm *VM) compiledGETFIELD(kept *chunkCache, pc int, instr Instr) error {
	regs := vm.regs
	target := header(regs[instr.B])
	if target == nil || target.Type != ObjTypeObject {
		_, err := vm.objectOperand("GETFIELD", regs[instr.B])
		return err
	}
	obj := (*ObjectObj)(unsafe.Pointer(target))
	known := &kept.fields[pc]
	if known.shape != obj.shape {
		name, err := vm.fieldName("GETFIELD", instr.C)
		if err != nil {
			return err
		}
		known.shape, known.slot = obj.shape, -1
		if slot, ok := obj.shape.index[name]; ok {
			known.slot = int32(slot)
		}
	}
	if known.slot >= 0 {
		regs[instr.A] = obj.slots[known.slot]
	} else {
		regs[instr.A] = valueNull
	}
	return nil
}

func (vm *VM) compiledSETFIELD(kept *chunkCache, pc int, instr Instr) error {
	regs := vm.regs
	target := header(regs[instr.A])
	if target == nil || target.Type != ObjTypeObject {
		_, err := vm.objectOperand("SETFIELD", regs[instr.A])
		return err
	}
	if target.Frozen {
		return vm.runtimeError(errFrozen)
	}
	obj := (*ObjectObj)(unsafe.Pointer(target))
	known := &kept.fields[pc]
	if known.shape != obj.shape {
		name, err := vm.fieldName("SETFIELD", instr.B)
		if err != nil {
			return err
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
		obj.slots[known.slot] = regs[instr.C]
		return nil
	}
	before := cap(obj.slots)
	obj.slots = append(obj.slots, regs[instr.C])
	obj.shape = known.next
	if after := cap(obj.slots); after > before {
		if _, err := vm.heap.grow(uint64(after-before) * valueSize); err != nil {
			return vm.runtimeError(err.Error())
		}
		vm.collectIfNeeded()
	}
	return nil
}

func (vm *VM) compiledNEWSTR(kept *chunkCache, instr Instr) error {
	regs := vm.regs
	index := intOf(vm.chunk.Constants[instr.B])
	if made := kept.strings[index]; made != 0 {
		regs[instr.A] = made
		return nil
	}
	strObj, shouldGC, err := vm.heap.AllocString(vm.chunk.StringPool[index])
	if err != nil {
		return vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
	}
	regs[instr.A] = valueOf(&strObj.header)
	kept.strings[index] = regs[instr.A]
	if shouldGC {
		vm.CollectGarbage()
	}
	return nil
}

func (vm *VM) compiledGETINDEX(instr Instr) error {
	regs := vm.regs
	indexVal := regs[instr.C]
	if !indexVal.IsInt() {
		return vm.runtimeError("GETINDEX requires integer index")
	}
	index := int(intOf(indexVal))
	target := header(regs[instr.B])
	if target != nil && target.Type == ObjTypeArray {
		elements := (*ArrayObj)(unsafe.Pointer(target)).elements
		if index < 0 || index >= len(elements) {
			return vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, len(elements)))
		}
		regs[instr.A] = elements[index]
		return nil
	}
	if target == nil || target.Type != ObjTypeString {
		_, err := vm.arrayOperand("GETINDEX", regs[instr.B])
		return err
	}
	str := (*StringObj)(unsafe.Pointer(target))
	if length := str.RuneLen(); index < 0 || index >= length {
		return vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, length))
	}
	char := str.RuneAt(index)
	if len(char) == 1 {
		value, err := vm.charString(char[0])
		if err != nil {
			return vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
		}
		regs[instr.A] = value
		return nil
	}
	value, err := vm.newString(char)
	if err != nil {
		return vm.runtimeError(fmt.Sprintf("string allocation failed: %v", err))
	}
	regs[instr.A] = value
	vm.collectIfNeeded()
	return nil
}

func (vm *VM) compiledSETELEM(instr Instr) error {
	regs := vm.regs
	indexVal := regs[instr.B]
	if !indexVal.IsInt() {
		return vm.runtimeError("SETELEM requires integer index")
	}
	index := int(intOf(indexVal))
	arrayObj, err := vm.arrayOperand("SETELEM", regs[instr.A])
	if err != nil {
		return err
	}
	if index < 0 || index >= len(arrayObj.elements) {
		return vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, arrayObj.Len()))
	}
	if arrayObj.header.Frozen {
		return vm.runtimeError(errFrozen)
	}
	arrayObj.elements[index] = regs[instr.C]
	return nil
}

func (vm *VM) compiledARRLEN(instr Instr) error {
	regs := vm.regs
	target := header(regs[instr.B])
	if target == nil {
		_, err := vm.arrayOperand("ARRLEN", regs[instr.B])
		return err
	}
	switch target.Type {
	case ObjTypeArray:
		regs[instr.A] = NewInt(int64(len((*ArrayObj)(unsafe.Pointer(target)).elements)))
	case ObjTypeString:
		regs[instr.A] = NewInt(int64((*StringObj)(unsafe.Pointer(target)).RuneLen()))
	default:
		_, err := vm.arrayOperand("ARRLEN", regs[instr.B])
		return err
	}
	return nil
}

// compiledCALLX calls a host function as CALLX does.
func (vm *VM) compiledCALLX(kept *chunkCache, instr Instr) error {
	callee := kept.externs[instr.C]
	if callee == 0 {
		object, err := vm.externObject(vm.chunk.ExternNames[instr.C])
		if err != nil {
			return err
		}
		callee = object
		kept.externs[instr.C] = callee
	}
	first := int(instr.A) + 1
	last := first + int(instr.B)
	pinned := len(vm.pinned)
	retVal, err := vm.callExtern((*ExternFunctionObj)(unsafe.Pointer(header(callee))), vm.regs[first:last:last])
	if err != nil {
		return err
	}
	vm.regs[0] = retVal
	vm.pinned = vm.pinned[:pinned]
	vm.collectIfNeeded()
	return nil
}

// compiledCallee is the function a CALLK calls, made the first time.
func (vm *VM) compiledCallee(kept *chunkCache, index int) (*FunctionObj, error) {
	callee := kept.funcs[index]
	if callee == 0 {
		made, err := vm.functionObject(vm.chunk, kept.funcs, index)
		if err != nil {
			return nil, err
		}
		callee = made
	}
	return (*FunctionObj)(unsafe.Pointer(header(callee))), nil
}

// compiledCall finishes a call of a function of the program the call has
// entered: compiled, or run by the instruction loop. The caller leaves it.
func (vm *VM) compiledCall() (Value, error) {
	if result, err, compiled := vm.enterCompiled(); compiled {
		return result, err
	}
	return vm.run(vm.frameCount)
}

// compiledCALL calls the function value of a register, as CALL does, and
// leaves the result in r0.
func (vm *VM) compiledCALL(instr Instr) error {
	regs := vm.regs
	funcReg := int(instr.A)
	nargs := int(instr.B)
	callee := header(regs[funcReg])
	if callee == nil {
		return vm.runtimeError(fmt.Sprintf("CALL requires function object in register r%d, got %v", funcReg, regs[funcReg]))
	}
	args := regs[funcReg+1 : funcReg+1+nargs : funcReg+1+nargs]
	switch callee.Type {
	case ObjTypeFunction:
		funcObj := (*FunctionObj)(unsafe.Pointer(callee))
		proto := funcObj.proto
		if nargs != proto.ArgCount {
			return vm.runtimeError(fmt.Sprintf("function %s expects %d arguments, got %d", proto.Name, proto.ArgCount, nargs))
		}
		if vm.interrupted.Load() || vm.outOfSteps() {
			return vm.stopped()
		}
		return vm.callFound(funcObj, funcReg+1, funcReg+1+nargs)
	case ObjTypeExternFunction:
		pinned := len(vm.pinned)
		retVal, err := vm.callExtern((*ExternFunctionObj)(unsafe.Pointer(callee)), args)
		if err != nil {
			return err
		}
		vm.regs[0] = retVal
		vm.pinned = vm.pinned[:pinned]
		vm.collectIfNeeded()
		return nil
	}
	return vm.runtimeError(fmt.Sprintf("CALL requires function object, got %v", callee.Type))
}

// compiledCALLM calls the method of a value, as CALLM does.
func (vm *VM) compiledCALLM(kept *chunkCache, pc int, instr Instr) error {
	first := int(instr.A) + 1
	last := first + int(instr.B)
	funcObj, err := vm.calledMethod(vm.regs[first], vm.chunk, kept, &kept.fields[pc], instr)
	if err != nil {
		return err
	}
	return vm.callFound(funcObj, first, last)
}

// callFound calls, from compiled code, a function found when the program
// runs (a closure, a method), with the arguments in its registers from
// first to last, and leaves the result in r0. A compiled function of the
// same chunk is called the short way, as CALLK calls one.
func (vm *VM) callFound(fn *FunctionObj, first, last int) error {
	if fn.chunk == vm.chunk {
		if compiled := compiledOf(fn); compiled != nil {
			if err := vm.enterCompiledCall(fn, first, last); err != nil {
				return err
			}
			result, err := compiled(vm)
			if err != nil {
				return err
			}
			vm.leaveCompiledCall()
			vm.regs[0] = result
			return nil
		}
	}
	if err := vm.enter(fn, vm.regs[first:last:last]); err != nil {
		return err
	}
	result, err := vm.compiledCall()
	if err != nil {
		return err
	}
	vm.leave()
	vm.regs[0] = result
	return nil
}

func (vm *VM) compiledGETMETHOD(kept *chunkCache, pc int, instr Instr) error {
	method, err := vm.methodOf(vm.regs[instr.B], vm.chunk, kept, &kept.fields[pc], instr.C)
	if err != nil {
		return err
	}
	vm.regs[instr.A] = method
	vm.collectIfNeeded()
	return nil
}

func (vm *VM) compiledONCE(kept *chunkCache, instr Instr) error {
	if kept.once != nil && kept.once[instr.B] != 0 {
		vm.regs[instr.A] = kept.once[instr.B]
		return nil
	}
	value, err := vm.once(vm.chunk, kept, int(instr.B), instr.C == 1)
	if err != nil {
		return err
	}
	vm.regs[instr.A] = value
	return nil
}

func (vm *VM) compiledTOINT(instr Instr) error {
	a := vm.regs[instr.B]
	switch {
	case a.IsInt():
		vm.regs[instr.A] = a
	case a.IsNumber():
		whole, err := vm.toInt(floatOf(a))
		if err != nil {
			return err
		}
		vm.regs[instr.A] = whole
	default:
		return vm.runtimeError(fmt.Sprintf("TOINT requires number or int, got: %s", a.String()))
	}
	return nil
}

func (vm *VM) compiledBITS(instr Instr) error {
	regs := vm.regs
	a, b := regs[instr.B], regs[instr.C]
	switch instr.Op {
	case OpSHL, OpSHR:
		shifted, err := vm.shift(instr.Op, a, b)
		if err != nil {
			return err
		}
		regs[instr.A] = shifted
		return nil
	}
	names := map[OpCode][2]string{OpBAND: {"BAND", "&"}, OpBOR: {"BOR", "|"}, OpBXOR: {"BXOR", "^"}}[instr.Op]
	name, symbol := names[0], names[1]
	if !bothInts(a, b) {
		return vm.runtimeError(fmt.Sprintf("%s requires integer operands, got: %s %s %s", name, a.String(), symbol, b.String()))
	}
	switch instr.Op {
	case OpBAND:
		regs[instr.A] = NewInt(intOf(a) & intOf(b))
	case OpBOR:
		regs[instr.A] = NewInt(intOf(a) | intOf(b))
	default:
		regs[instr.A] = NewInt(intOf(a) ^ intOf(b))
	}
	return nil
}

func (vm *VM) compiledNEWFUNC(kept *chunkCache, instr Instr) error {
	protoIdx := int(instr.B)
	proto := &vm.chunk.Prototypes[protoIdx]
	if proto.UpvalueCount == 0 {
		shared, err := vm.functionObject(vm.chunk, kept.funcs, protoIdx)
		if err != nil {
			return err
		}
		vm.regs[instr.A] = shared
		vm.collectIfNeeded()
		return nil
	}
	funcObj, shouldGC, err := vm.heap.AllocFunction(vm.chunk, protoIdx, proto.UpvalueCount)
	if err != nil {
		return vm.runtimeError(fmt.Sprintf("function allocation failed: %v", err))
	}
	vm.regs[instr.A] = valueOf(&funcObj.header)
	if shouldGC {
		vm.CollectGarbage()
	}
	return nil
}

func (vm *VM) compiledCAPLOCAL(instr Instr) error {
	funcObj, err := vm.closureOperand("CAPLOCAL", vm.regs[instr.A], int(instr.B))
	if err != nil {
		return err
	}
	upval, err := vm.findOrCreateUpvalue(vm.base + int(instr.C))
	if err != nil {
		return vm.runtimeError(err.Error())
	}
	funcObj.upvalues[instr.B] = upval
	vm.collectIfNeeded()
	return nil
}

func (vm *VM) compiledCAPUPVAL(instr Instr) error {
	funcObj, err := vm.closureOperand("CAPUPVAL", vm.regs[instr.A], int(instr.B))
	if err != nil {
		return err
	}
	parent := vm.currentFunction.upvalues
	if int(instr.C) >= len(parent) {
		return vm.runtimeError(fmt.Sprintf("parent upvalue index out of bounds: %d", instr.C))
	}
	funcObj.upvalues[instr.B] = parent[instr.C]
	return nil
}

func (vm *VM) compiledNEWEXFUNC(instr Instr) error {
	object, err := vm.externObject(vm.chunk.ExternNames[instr.B])
	if err != nil {
		return err
	}
	vm.regs[instr.A] = object
	vm.collectIfNeeded()
	return nil
}

func (vm *VM) compiledCLOSEUP(instr Instr) {
	if len(vm.stk.open) > 0 {
		vm.closeUpvalueAt(vm.base + int(instr.A))
	}
}

func (vm *VM) compiledLDCAP(instr Instr) error {
	upvalues := vm.currentFunction.upvalues
	if int(instr.B) >= len(upvalues) {
		return vm.runtimeError(fmt.Sprintf("LDCAP: upvalue index out of bounds: %d", instr.B))
	}
	upval := upvalues[instr.B]
	if upval == nil {
		return vm.runtimeError(fmt.Sprintf("LDCAP: uninitialized upvalue at index %d", instr.B))
	}
	vm.regs[instr.A] = upval.Get()
	return nil
}

func (vm *VM) compiledSTCAP(instr Instr) error {
	upvalues := vm.currentFunction.upvalues
	if int(instr.A) >= len(upvalues) {
		return vm.runtimeError(fmt.Sprintf("STCAP: upvalue index out of bounds: %d", instr.A))
	}
	upval := upvalues[instr.A]
	if upval == nil {
		return vm.runtimeError(fmt.Sprintf("STCAP: uninitialized upvalue at index %d", instr.A))
	}
	upval.Set(vm.regs[instr.B])
	return nil
}

func (vm *VM) compiledNEWARRAY(instr Instr) error {
	obj, shouldGC, err := vm.heap.AllocArray(int(instr.B))
	if err != nil {
		return vm.runtimeError(fmt.Sprintf("array allocation failed: %v", err))
	}
	vm.regs[instr.A] = valueOf(&obj.header)
	if shouldGC {
		vm.CollectGarbage()
	}
	return nil
}

func (vm *VM) compiledSETINDEX(instr Instr) error {
	regs := vm.regs
	indexVal := regs[instr.B]
	if !indexVal.IsInt() {
		return vm.runtimeError("SETINDEX requires integer index")
	}
	index := int(intOf(indexVal))
	arrayObj, err := vm.arrayOperand("SETINDEX", regs[instr.A])
	if err != nil {
		return err
	}
	if index < 0 || index >= cap(arrayObj.elements) {
		return vm.runtimeError(fmt.Sprintf("index out of bounds: %d (length %d)", index, arrayObj.Len()))
	}
	if arrayObj.header.Frozen {
		return vm.runtimeError(errFrozen)
	}
	arrayObj.Set(index, regs[instr.C])
	return nil
}

func (vm *VM) compiledARRPUSH(instr Instr) error {
	regs := vm.regs
	arrayObj, err := vm.arrayOperand("ARRPUSH", regs[instr.A])
	if err != nil {
		return err
	}
	if arrayObj.header.Frozen {
		return vm.runtimeError(errFrozen)
	}
	before := cap(arrayObj.elements)
	arrayObj.elements = append(arrayObj.elements, regs[instr.B])
	if after := cap(arrayObj.elements); after > before {
		if _, err := vm.heap.grow(uint64(after-before) * valueSize); err != nil {
			return vm.runtimeError(err.Error())
		}
		vm.collectIfNeeded()
	}
	return nil
}

func (vm *VM) compiledHASFIELD(instr Instr) error {
	objObj, err := vm.objectOperand("HASFIELD", vm.regs[instr.B])
	if err != nil {
		return err
	}
	fieldName, err := vm.fieldName("HASFIELD", instr.C)
	if err != nil {
		return err
	}
	_, ok := objObj.Get(fieldName)
	vm.regs[instr.A] = NewBool(ok)
	return nil
}

func (vm *VM) compiledDELFIELD(instr Instr) error {
	objObj, err := vm.objectOperand("DELFIELD", vm.regs[instr.B])
	if err != nil {
		return err
	}
	fieldName, err := vm.fieldName("DELFIELD", instr.C)
	if err != nil {
		return err
	}
	if objObj.header.Frozen {
		return vm.runtimeError(errFrozen)
	}
	vm.regs[instr.A] = NewBool(objObj.Delete(fieldName))
	return nil
}

// compiledTRY leaves where the call goes on if something fails, as TRY32
// does.
func (vm *VM) compiledTRY(target int) {
	vm.handlers = append(vm.handlers, handler{depth: vm.frameCount, ip: target, pinned: len(vm.pinned)})
}

func (vm *VM) compiledENDTRY() error {
	n := len(vm.handlers)
	if n == 0 || vm.handlers[n-1].depth != vm.frameCount {
		return vm.runtimeError("ENDTRY without a handler in this call")
	}
	vm.handlers = vm.handlers[:n-1]
	return nil
}

func (vm *VM) compiledRETHROW() error {
	n := len(vm.pending)
	if n == 0 {
		return vm.runtimeError("RETHROW without a failure to pass on")
	}
	failure := vm.pending[n-1].failure
	vm.pending = vm.pending[:n-1]
	return failure
}

// Frameless functions.
//
// A compiled function whose every register holds an Int or a Float, and
// which calls nothing but functions like it, needs nothing of a frame: the
// collector has nothing to find in it, no task can take the turn in it,
// no closure follows its registers. qoqi native writes it a second time,
// as a Go function of Go numbers that compiled code calls directly, with
// no frame entered or left.
//
// What a frame is still needed for is a failure: its stack trace names
// every call in progress. A frameless function that fails says where, and
// each that the failure goes through on its way out adds where it made
// its call; the first function with a frame (framelessArrived) makes of it
// the error the calls would have made with frames: the same message, the
// same kind, the same trace. Frameless code never sets vm.ip, so that the
// function with a frame is still where it made the call.

// framelessFailure is a failure on its way out of frameless calls.
type framelessFailure struct {
	kind        ErrorKind
	message     string
	fatal       bool
	frames      []StackFrame // The innermost call first
	instruction int
}

func (f *framelessFailure) Error() string { return f.message }

// framelessFrame is where a frameless function is: its instruction pc.
func (vm *VM) framelessFrame(proto, pc int) StackFrame {
	frame := frameAt(vm.chunk, pc, nil)
	if frame.Function == "" {
		frame.Function = vm.chunk.Prototypes[proto].Name
	}
	return frame
}

// framelessFailed is the failure of the instruction pc of a frameless
// function, as runtimeError would have made it.
func (vm *VM) framelessFailed(proto, pc int, message string) error {
	return &framelessFailure{kind: kindOf(message), message: message, frames: []StackFrame{vm.framelessFrame(proto, pc)}, instruction: pc}
}

// framelessStopped is what stopped makes of a program told to stop, or out
// of steps, in a frameless function.
func (vm *VM) framelessStopped(proto, pc int) error {
	f := &framelessFailure{fatal: true, frames: []StackFrame{vm.framelessFrame(proto, pc)}, instruction: pc}
	if vm.interrupted.Load() {
		f.kind, f.message = KindInterrupted, "interrupted"
	} else {
		f.kind, f.message = KindBudget, fmt.Sprintf("the program ran for more than the %d steps it may", vm.stepLimit)
	}
	return f
}

// framelessTooDeep is the failure of a call that would go deeper than the
// program may, from a frameless function, as enter makes it.
func (vm *VM) framelessTooDeep(proto, pc int) error {
	return vm.framelessFailed(proto, pc, fmt.Sprintf("stack overflow: max call depth %d exceeded", vm.maxCallDepth))
}

// tooDeep is the same from a function with a frame, where it is.
func (vm *VM) tooDeep() error {
	return vm.runtimeError(fmt.Sprintf("stack overflow: max call depth %d exceeded", vm.maxCallDepth))
}

// framelessCalled passes on the failure of a call a frameless function
// made at pc, with where it made it.
func (vm *VM) framelessCalled(err error, proto, pc int) error {
	f := err.(*framelessFailure)
	f.frames = append(f.frames, vm.framelessFrame(proto, pc))
	return f
}

// framelessArrived makes of a failure that comes out of frameless calls,
// into a function with a frame that is where it made the call, the error
// the calls would have made with frames.
func (vm *VM) framelessArrived(err error) error {
	f, ok := err.(*framelessFailure)
	if !ok {
		return err
	}
	failure := &RuntimeError{Kind: f.kind, Message: f.message, Stack: append(f.frames, vm.callStack()...), instruction: f.instruction}
	if f.fatal {
		return &fatalError{failure}
	}
	return failure
}
