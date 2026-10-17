package codegen

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// returnReg is where the VM leaves the result of every call. It is never
// used to hold a variable.
const returnReg = 0

// funcGen generates the bytecode of one function.
//
// Register layout of a frame:
//
//	r0            call results (and the first argument on entry)
//	r1..rN-1      remaining arguments, as passed by the caller
//	permanents    variables and values that live across basic blocks
//	temporaries   values used within one block, slots are reused
//	call window   callee followed by its arguments, set up before each CALL
type funcGen struct {
	g      *Generator
	fn     *ir.Function
	blocks []*ir.BasicBlock // Reachable blocks in layout order

	phys     map[int]int // Virtual register ID -> physical register
	regCount int         // Registers used so far (before the call window)
	callBase int         // First register of the call window
	maxArgs  int         // Largest argument count of any call

	blockStart map[*ir.BasicBlock]int // Block -> index of its first instruction
	fixups     []fixup
	pos        token.Position // Position of the IR instruction being lowered
	failed     bool

	// long is set for a function that lies beyond what an operand can
	// address: its jumps are then written in the long form, and nothing
	// that needs an address in an operand is used.
	long bool

	// What was decided before any instruction was written (see shorten)
	hasCalls bool
	folded   map[*ir.Instruction]bool            // Instructions another one does the work of
	direct   map[*ir.Instruction]directCall      // Calls of a function known by its number
	fused    map[*ir.Instruction]*ir.Instruction // A branch -> the test it jumps on
	small    map[*ir.Instruction]smallOperand    // An operation -> the small number it takes
	window   map[int]int                         // A temporary made where a call wants it: its place there
	unread   map[int]bool                        // Temporaries nothing reads
	inReturn map[int]bool                        // Results of calls read where the call left them, in r0
	straight map[int]int                         // A temporary that is only moved into a variable: the variable
	typeTest map[*ir.Instruction]string          // A call that asks whether a value is of one type -> its name
	switches map[*ir.Instruction]*typeSwitch     // The branch that starts a run of such tests on one value -> the run
	inSwitch map[*ir.Instruction]bool            // The branches further down a run
	fields   map[*ir.Instruction]uint16          // A struct or enum value being made -> how many fields it is given
	constant map[*ir.Instruction]bool            // Values of an enum that hold nothing and are never given anything
	builtin  map[*ir.Instruction]vm.OpCode       // A call of a host function an instruction does the work of

	stages []vm.Stage // The stages of a flow written in the function
}

// fixup is a jump whose target is filled in once all blocks are placed.
type fixup struct {
	idx    int // Index in chunk.Code of the word to patch
	target *ir.BasicBlock
	field  byte // Which operand holds an address ('A', 'B', 'C'); 0 for the offset word of a long jump
}

// typeSwitch is a run of tests of what one value is, each going on to the
// next when it fails: what a match on an enum is.
type typeSwitch struct {
	value     ir.Value
	names     []string
	targets   []*ir.BasicBlock
	otherwise *ir.BasicBlock
}

// minSwitch is how many cases make a SWITCH worth its table: fewer are
// asked one after the other.
const minSwitch = 3

// directCall is a call of a function that needs no function value.
type directCall struct {
	op    vm.OpCode // CALLK or CALLX
	index uint16
}

// builtins is the host functions of one argument an instruction does
// exactly the work of, failures included. They are pure: a program that
// may compute may call them (see sandbox.Permitted).
var builtins = map[string]vm.OpCode{
	"std.math.toFloat": vm.OpTONUM,
}

// smallOperand is a number written in an instruction instead of a
// register: the number itself, or a number of any size that is a constant
// of the chunk (pooled).
type smallOperand struct {
	operand int // Which operand of the IR instruction it replaces
	value   uint16
	pooled  bool
	number  vm.Value // The constant, when pooled
}

// withConstant is the arithmetic that takes a number of the chunk's
// constants in place of its second register.
var withConstant = map[ir.OpType]vm.OpCode{
	ir.OpAdd: vm.OpADDK, ir.OpSub: vm.OpSUBK, ir.OpMul: vm.OpMULK, ir.OpDiv: vm.OpDIVK, ir.OpMod: vm.OpMODK,
}

func newFuncGen(g *Generator, fn *ir.Function) *funcGen {
	return &funcGen{
		g:          g,
		fn:         fn,
		phys:       make(map[int]int),
		blockStart: make(map[*ir.BasicBlock]int),
		pos:        firstPos(fn),
	}
}

// reset forgets what an attempt at generating the function left behind.
func (f *funcGen) reset(codeStart int) {
	chunk := f.g.chunk
	for ip := codeStart; ip < len(chunk.Code); ip++ {
		delete(chunk.DebugInfo, ip)
	}
	chunk.Code = chunk.Code[:codeStart]
	f.stages = nil
	f.phys = make(map[int]int)
	f.blockStart = make(map[*ir.BasicBlock]int)
	f.fixups = nil
	f.maxArgs = 0
	f.pos = firstPos(f.fn)
}

func firstPos(fn *ir.Function) token.Position {
	for _, bb := range fn.Blocks {
		for _, instr := range bb.Instructions {
			if instr.Pos.Line > 0 {
				return instr.Pos
			}
		}
	}
	return token.Position{}
}

func (f *funcGen) generate() {
	f.blocks = reachableBlocks(f.fn)
	codeStart := len(f.g.chunk.Code)

	// The short forms hold an address in an operand. A function that turns
	// out to reach past what an operand can say is generated again
	// without them; that takes a program of tens of thousands of
	// instructions before it.
	f.build()
	if len(f.g.chunk.Code) > vm.MaxOperand && !f.failed {
		f.reset(codeStart)
		f.long = true
		f.build()
	}

	total := f.callBase + 1 + f.maxArgs
	if total > maxRegisters && !f.failed {
		f.fail(ErrTooManyRegisters,
			fmt.Sprintf("function '%s' needs %d registers, the limit is %d", f.fn.Name, total, maxRegisters),
			"Split the function into smaller ones")
	}

	f.g.chunk.Prototypes = append(f.g.chunk.Prototypes, vm.FunctionPrototype{
		Name:          f.fn.Name,
		CodeStart:     codeStart,
		CodeSize:      len(f.g.chunk.Code) - codeStart,
		RegisterCount: total,
		ArgCount:      len(f.fn.Params),
		UpvalueCount:  f.fn.UpvalueCount,
		Signature:     f.fn.Signature,
		Step:          f.fn.Step,
		Once:          f.fn.Once,
		Stages:        f.stages,
		Migrate:       f.fn.Migrate,
		Params:        f.fn.ParamNames,
		Inboxes:       inboxesOf(f.fn.Inboxes),
		Listens:       f.fn.Listens,
		ByQueue:       f.fn.ByQueue,
	})
}

// build writes the instructions of the function.
func (f *funcGen) build() {
	f.allocateRegisters()

	// The first argument arrives in r0, which every call overwrites
	if len(f.fn.Params) > 0 {
		if home, ok := f.phys[f.fn.Params[0].ID]; ok && home != returnReg {
			f.emit(vm.OpMOV, uint16(home), returnReg, 0)
		}
	}

	for i, bb := range f.blocks {
		f.blockStart[bb] = len(f.g.chunk.Code)

		var next *ir.BasicBlock
		if i+1 < len(f.blocks) {
			next = f.blocks[i+1]
		}
		f.generateBlock(bb, next)
	}

	for _, fx := range f.fixups {
		target := f.blockStart[fx.target]
		word := &f.g.chunk.Code[fx.idx]
		switch fx.field {
		case 'A':
			word.A = uint16(target)
		case 'B':
			word.B = uint16(target)
		case 'C':
			word.C = uint16(target)
		default:
			*word = vm.EncodeOffset32(int32(target - (fx.idx + 1)))
		}
	}
}

// --- Control flow graph ---

// body returns the instructions of a block up to and including its first
// terminator; anything after it can never run.
func body(bb *ir.BasicBlock) []*ir.Instruction {
	for i, instr := range bb.Instructions {
		if isTerminator(instr.Op) {
			return bb.Instructions[:i+1]
		}
	}
	return bb.Instructions
}

func isTerminator(op ir.OpType) bool {
	switch op {
	case ir.OpRet, ir.OpJmp, ir.OpBr, ir.OpTry, ir.OpRethrow:
		return true
	}
	return false
}

// successors returns the blocks a block can jump to.
func successors(bb *ir.BasicBlock) []*ir.BasicBlock {
	instrs := body(bb)
	if len(instrs) == 0 {
		return nil
	}

	var targets []*ir.BasicBlock
	for _, operand := range instrs[len(instrs)-1].Operands {
		if target, ok := operand.(*ir.BasicBlock); ok {
			targets = append(targets, target)
		}
	}
	return targets
}

// reachableBlocks returns the blocks reachable from the entry, in their
// original order. Blocks left behind by code after return/break are dropped.
func reachableBlocks(fn *ir.Function) []*ir.BasicBlock {
	if fn.EntryBlock == nil {
		return nil
	}

	seen := map[*ir.BasicBlock]bool{fn.EntryBlock: true}
	work := []*ir.BasicBlock{fn.EntryBlock}
	for len(work) > 0 {
		bb := work[len(work)-1]
		work = work[:len(work)-1]
		for _, succ := range successors(bb) {
			if !seen[succ] {
				seen[succ] = true
				work = append(work, succ)
			}
		}
	}

	var blocks []*ir.BasicBlock
	for _, bb := range fn.Blocks {
		if seen[bb] {
			blocks = append(blocks, bb)
		}
	}
	return blocks
}

// --- Register allocation ---

// regID extracts the virtual register ID from an operand.
func regID(v ir.Value) (int, bool) {
	switch r := v.(type) {
	case *ir.Register:
		if r != nil {
			return r.ID, true
		}
	case ir.Register:
		return r.ID, true
	}
	return 0, false
}

// usage summarizes how a virtual register is used.
type usage struct {
	defs     int
	defBlock *ir.BasicBlock
	defIdx   int
	defInstr *ir.Instruction
	lastUse  int  // Index of the last use inside defBlock
	escapes  bool // Used outside defBlock, before its definition, or captured

	// How often it is read, and where it was read last
	reads      int
	useInstr   *ir.Instruction
	useIdx     int // Index of that instruction in its block
	useOperand int // Which operand of it
}

// allocateRegisters maps every virtual register to a physical one.
//
// A register that is written once and only read later in the same block is
// a temporary: its slot is released after the last read and reused. All
// other registers are permanent and keep a slot for the whole function,
// which is also what closures need, since they capture slots by reference.
func (f *funcGen) allocateRegisters() {
	uses := make(map[int]*usage)
	capturesFirst := false // Whether a function made here captures the first argument
	var order []int        // First appearance, for a deterministic layout
	get := func(id int) *usage {
		u, ok := uses[id]
		if !ok {
			u = &usage{}
			uses[id] = u
			order = append(order, id)
		}
		return u
	}

	for _, bb := range f.blocks {
		for idx, instr := range body(bb) {
			for n, operand := range instr.Operands {
				id, ok := regID(operand)
				if !ok {
					continue
				}
				u := get(id)
				if u.defs == 0 || u.defBlock != bb || idx <= u.defIdx || instr.Op == ir.OpMakeClosure {
					u.escapes = true
				}
				u.lastUse = idx
				u.reads++
				u.useInstr, u.useIdx, u.useOperand = instr, idx, n
			}
			if instr.Dest != nil {
				u := get(instr.Dest.ID)
				u.defs++
				if u.defs == 1 {
					u.defBlock, u.defIdx, u.lastUse, u.defInstr = bb, idx, idx, instr
				}
			}
			if instr.Op == ir.OpCall {
				f.hasCalls = true
				if len(instr.Operands)-1 > f.maxArgs {
					f.maxArgs = len(instr.Operands) - 1
				}
			}
			if instr.Op == ir.OpMakeClosure && len(f.fn.Params) > 0 {
				// A function made here that captures the first argument
				// reads it where it is, for as long as this call lasts
				for _, operand := range instr.Operands {
					if reg, ok := operand.(*ir.Register); ok && reg.ID == f.fn.Params[0].ID {
						capturesFirst = true
					}
				}
			}
		}
	}

	// Arguments: all but the first stay where the caller put them
	next := len(f.fn.Params)
	if next == 0 {
		next = 1 // r0 is reserved even without arguments
	}
	isParam := make(map[int]bool)
	for i, param := range f.fn.Params {
		isParam[param.ID] = true
		if i > 0 {
			f.phys[param.ID] = i
		} else if !f.hasCalls && !capturesFirst {
			// Nothing overwrites r0 in a function that calls nothing: the
			// first argument stays where it arrived. Nothing but what it
			// returns, which is put there while a function it made may
			// still be reading the argument: one that is captured moves.
			f.phys[param.ID] = returnReg
		} else if _, used := uses[param.ID]; used {
			f.phys[param.ID] = next
			next++
		}
	}

	// Permanents
	isTemp := func(id int) bool {
		u := uses[id]
		return u.defs == 1 && !u.escapes && !isParam[id]
	}
	f.shorten(uses, isTemp)
	for _, id := range order {
		if isParam[id] || isTemp(id) {
			continue
		}
		f.phys[id] = next
		next++
	}

	// Temporaries: walk each block, reusing slots as values die
	high := next
	var free []int
	for _, bb := range f.blocks {
		for idx, instr := range body(bb) {
			// Operands are read before the result is written, so a slot
			// that dies here can already hold this instruction's result.
			for _, operand := range instr.Operands {
				id, ok := regID(operand)
				if !ok || !isTemp(id) || uses[id].lastUse != idx {
					continue
				}
				if slot, live := f.phys[id]; live {
					free = append(free, slot)
					uses[id].lastUse = -1 // Released; guards against double free
				}
			}

			if instr.Dest == nil || !isTemp(instr.Dest.ID) || f.folded[instr] {
				continue
			}
			if _, placed := f.window[instr.Dest.ID]; placed || f.inReturn[instr.Dest.ID] {
				continue
			}
			if _, written := f.straight[instr.Dest.ID]; written {
				continue
			}
			var slot int
			if len(free) > 0 {
				slot = free[len(free)-1]
				free = free[:len(free)-1]
			} else {
				slot = high
				high++
			}
			f.phys[instr.Dest.ID] = slot
			if uses[instr.Dest.ID].lastUse == idx {
				free = append(free, slot) // Result is never read
			}
		}
	}

	f.regCount = high
	f.callBase = high

	// The temporaries made for a call go where the call wants them
	for id, offset := range f.window {
		f.phys[id] = f.callBase + offset
	}
	for id := range f.inReturn {
		f.phys[id] = returnReg
	}
	for id, variable := range f.straight {
		f.phys[id] = f.phys[variable]
	}
}

// fusable is the tests a branch can be made to jump on directly, with the
// instruction for "go there unless it holds". Greater than is written as
// less than with the operands the other way round.
var fusable = map[ir.OpType]struct {
	op      vm.OpCode
	swapped bool
}{
	ir.OpLt:  {vm.OpJNLT, false},
	ir.OpLte: {vm.OpJNLE, false},
	ir.OpGt:  {vm.OpJNLT, true},
	ir.OpGte: {vm.OpJNLE, true},
	ir.OpEq:  {vm.OpJNEQ, false},
	ir.OpNeq: {vm.OpJNNE, false},
}

// againstSmall is the same tests against a small number on the right, and
// what they become with the number on the left: 2 < n is n > 2.
var againstSmall = map[ir.OpType][2]vm.OpCode{
	ir.OpLt:  {vm.OpJNLTI, vm.OpJNGTI},
	ir.OpLte: {vm.OpJNLEI, vm.OpJNGEI},
	ir.OpGt:  {vm.OpJNGTI, vm.OpJNLTI},
	ir.OpGte: {vm.OpJNGEI, vm.OpJNLEI},
	ir.OpEq:  {vm.OpJNEQI, vm.OpJNEQI},
	ir.OpNeq: {vm.OpJNNEI, vm.OpJNNEI},
}

// againstConstant is the same against a number of the chunk's constants.
var againstConstant = map[ir.OpType][2]vm.OpCode{
	ir.OpLt:  {vm.OpJNLTK, vm.OpJNGTK},
	ir.OpLte: {vm.OpJNLEK, vm.OpJNGEK},
	ir.OpGt:  {vm.OpJNGTK, vm.OpJNLTK},
	ir.OpGte: {vm.OpJNGEK, vm.OpJNLEK},
	ir.OpEq:  {vm.OpJNEQK, vm.OpJNEQK},
	ir.OpNeq: {vm.OpJNNEK, vm.OpJNNEK},
}

// shorten decides, before any instruction is written, where one
// instruction can do the work of several:
//
//   - a call of a function known by its name needs no function value
//     (CALLK, CALLX);
//   - a value worked out only to be an argument is worked out where the
//     call wants it, instead of being moved there;
//   - a test whose only use is the branch after it becomes the branch
//     (JNLT and the like);
//   - a small number that is only an operand of an addition, a
//     subtraction or such a test is written in the instruction;
//   - any other number that is only an operand of arithmetic or of such
//     a test is read from the chunk's constants by the instruction
//     (ADDK, JNLTK and the like), instead of being loaded first.
//
// Each concerns a temporary: a value written once and read once, in the
// block it was made in. Nothing else can depend on how it is produced.
func (f *funcGen) shorten(uses map[int]*usage, isTemp func(int) bool) {
	f.folded = make(map[*ir.Instruction]bool)
	f.direct = make(map[*ir.Instruction]directCall)
	f.fused = make(map[*ir.Instruction]*ir.Instruction)
	f.small = make(map[*ir.Instruction]smallOperand)
	f.window = make(map[int]int)
	f.unread = make(map[int]bool)
	f.inReturn = make(map[int]bool)
	f.straight = make(map[int]int)
	f.typeTest = make(map[*ir.Instruction]string)
	f.switches = make(map[*ir.Instruction]*typeSwitch)
	f.inSwitch = make(map[*ir.Instruction]bool)
	f.fields = make(map[*ir.Instruction]uint16)
	f.constant = make(map[*ir.Instruction]bool)
	f.builtin = make(map[*ir.Instruction]vm.OpCode)
	for id, u := range uses {
		if isTemp(id) && u.reads == 0 {
			f.unread[id] = true
		}
	}

	// once returns how a register is used, if it is a temporary read once
	once := func(v ir.Value) *usage {
		id, ok := regID(v)
		if !ok || !isTemp(id) || uses[id].reads != 1 || f.folded[uses[id].defInstr] {
			return nil
		}
		return uses[id]
	}
	// smallNumber returns the number a register was given, if it is a
	// temporary read once that holds a number an operand can hold
	smallNumber := func(v ir.Value) (uint16, *usage, bool) {
		u := once(v)
		if u == nil || u.defInstr.Op != ir.OpMov || len(u.defInstr.Operands) != 1 {
			return 0, nil, false
		}
		c, ok := u.defInstr.Operands[0].(ir.Constant)
		if !ok || c.Type != "Int" {
			return 0, nil, false
		}
		n, ok := toInt64(c.Val)
		if !ok || n < 0 || n > vm.MaxOperand {
			return 0, nil, false
		}
		return uint16(n), u, true
	}
	// pooledNumber is the same for any Int or Float, read from the
	// chunk's constants: a number too large for an operand, or a Float
	pooledNumber := func(v ir.Value) (smallOperand, *usage, bool) {
		u := once(v)
		if u == nil || u.defInstr.Op != ir.OpMov || len(u.defInstr.Operands) != 1 {
			return smallOperand{}, nil, false
		}
		c, ok := u.defInstr.Operands[0].(ir.Constant)
		if !ok {
			return smallOperand{}, nil, false
		}
		switch c.Type {
		case "Int":
			if n, ok := toInt64(c.Val); ok && n >= vm.MinInt && n <= vm.MaxInt {
				return smallOperand{pooled: true, number: vm.NewInt(n)}, u, true
			}
		case "Float":
			if x, ok := c.Val.(float64); ok {
				return smallOperand{pooled: true, number: vm.NewNumber(x)}, u, true
			}
		}
		return smallOperand{}, nil, false
	}
	// takeConstant writes a number into instr in place of its operand
	// `operand`, if that is a number used for nothing else
	takeConstant := func(instr *ir.Instruction, operand int) bool {
		k, u, ok := pooledNumber(instr.Operands[operand])
		if !ok {
			return false
		}
		f.folded[u.defInstr] = true
		k.operand = operand
		f.small[instr] = k
		return true
	}

	for _, bb := range f.blocks {
		instrs := body(bb)
		for idx, instr := range instrs {
			switch instr.Op {
			case ir.OpCall:
				if name, ok := f.typeTestOf(instr, once); ok {
					f.typeTest[instr] = name
					continue
				}
				if op, u, ok := f.builtinOf(instr, once); ok {
					f.builtin[instr] = op
					f.folded[u.defInstr] = true
					continue
				}
				f.shortenCall(instr, idx, instrs, uses, isTemp, once)

			case ir.OpNewStruct, ir.OpNewVariant:
				f.measure(instr, idx, instrs, uses)

			case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpDiv, ir.OpMod:
				if len(instr.Operands) != 2 {
					continue
				}
				// A number on the left is taken the other way round where
				// that is the same: n + 1 is 1 + n, n * 2 is 2 * n. The types
				// make the other operand a number too, so + is not joining
				// texts here
				swaps := instr.Op == ir.OpAdd || instr.Op == ir.OpMul
				isSum := instr.Op == ir.OpAdd || instr.Op == ir.OpSub
				if n, u, ok := smallNumber(instr.Operands[1]); ok && isSum {
					f.folded[u.defInstr] = true
					f.small[instr] = smallOperand{operand: 1, value: n}
				} else if n, u, ok := smallNumber(instr.Operands[0]); ok && instr.Op == ir.OpAdd {
					f.folded[u.defInstr] = true
					f.small[instr] = smallOperand{operand: 0, value: n}
				} else if !takeConstant(instr, 1) && swaps {
					takeConstant(instr, 0)
				}

			case ir.OpBr:
				// The jump holds the address of where to go: not in a
				// function too far into the program for that
				if f.long || idx == 0 || len(instr.Operands) == 0 {
					continue
				}
				test := instrs[idx-1]
				u := once(instr.Operands[0])
				if u == nil || u.defInstr != test {
					continue
				}
				if _, asksType := f.typeTest[test]; asksType {
					f.folded[test] = true
					f.fused[instr] = test
					continue
				}
				if len(test.Operands) != 2 {
					continue
				}
				if _, ok := fusable[test.Op]; !ok {
					continue
				}
				f.folded[test] = true
				f.fused[instr] = test
				if n, u, ok := smallNumber(test.Operands[1]); ok {
					f.folded[u.defInstr] = true
					f.small[test] = smallOperand{operand: 1, value: n}
				} else if n, u, ok := smallNumber(test.Operands[0]); ok {
					f.folded[u.defInstr] = true
					f.small[test] = smallOperand{operand: 0, value: n}
				} else if !takeConstant(test, 1) {
					takeConstant(test, 0)
				}
			}
		}
	}
	if !f.long {
		f.findSwitches()
	}
	f.foldUnneededNames(uses)
	f.writeStraight(uses, isTemp)
}

// builtinOf returns the instruction that does the work of a call, if it
// calls one of the builtins by its name, and how the name was given.
func (f *funcGen) builtinOf(call *ir.Instruction, once func(ir.Value) *usage) (vm.OpCode, *usage, bool) {
	if len(call.Operands) != 2 {
		return 0, nil, false
	}
	u := once(call.Operands[0])
	if u == nil || u.defInstr.Op != ir.OpMov || len(u.defInstr.Operands) != 1 {
		return 0, nil, false
	}
	c, ok := u.defInstr.Operands[0].(ir.Constant)
	if !ok || c.Type != "FuncRef" {
		return 0, nil, false
	}
	name, _ := c.Val.(string)
	op, ok := builtins[name]
	if _, inProgram := f.g.protoIndex[name]; !ok || inProgram || !f.g.externs[name] {
		return 0, nil, false
	}
	return op, u, true
}

// namedFunction reports whether a register holds a function named in the
// program everywhere it is read: given it once, by its name, and never
// anything else. It returns how to call that function directly.
func (f *funcGen) namedFunction(v ir.Value, uses map[int]*usage) (vm.OpCode, uint16, bool) {
	id, ok := regID(v)
	if !ok {
		return 0, 0, false
	}
	u := uses[id]
	if u == nil || u.defs != 1 || u.defInstr == nil || u.defInstr.Op != ir.OpMov || len(u.defInstr.Operands) != 1 {
		return 0, 0, false
	}
	c, ok := u.defInstr.Operands[0].(ir.Constant)
	if !ok || c.Type != "FuncRef" {
		return 0, 0, false
	}
	name, _ := c.Val.(string)
	if index, known := f.g.protoIndex[name]; known && f.g.capturesNothing[name] {
		return vm.OpCALLK, uint16(index), true
	} else if !known && f.g.externs[name] {
		return vm.OpCALLX, f.g.extern(name), true
	}
	return 0, 0, false
}

// foldUnneededNames leaves out what gives a register a function by its
// name when nothing reads the register but direct calls of that function.
func (f *funcGen) foldUnneededNames(uses map[int]*usage) {
	called := map[int]int{}
	for _, bb := range f.blocks {
		for _, instr := range body(bb) {
			if _, ok := f.direct[instr]; !ok || instr.Op != ir.OpCall || len(instr.Operands) == 0 {
				continue
			}
			if id, ok := regID(instr.Operands[0]); ok {
				called[id]++
			}
		}
	}
	for id, n := range called {
		if u := uses[id]; u != nil && u.defs == 1 && u.reads == n && u.defInstr != nil {
			if _, _, named := f.namedFunction(&ir.Register{ID: id}, uses); named {
				f.folded[u.defInstr] = true
			}
		}
	}
}

// direct is the operations that read what they work on before they write
// their result, and write it once: their result can go anywhere.
var direct = map[ir.OpType]bool{
	ir.OpAdd: true, ir.OpSub: true, ir.OpMul: true, ir.OpDiv: true, ir.OpMod: true,
	ir.OpBitAnd: true, ir.OpBitOr: true, ir.OpBitXor: true, ir.OpShl: true, ir.OpShr: true,
	ir.OpEq: true, ir.OpNeq: true, ir.OpLt: true, ir.OpLte: true, ir.OpGt: true, ir.OpGte: true,
	ir.OpNeg: true, ir.OpNot: true, ir.OpGetField: true, ir.OpGetIndex: true, ir.OpLen: true, ir.OpOnce: true,
	ir.OpGetMethod: true, ir.OpLoadUpvalue: true, ir.OpCall: true,
}

// writeStraight finds the values that are worked out only to be put in a
// variable by the very next instruction, as in `total = total + n`: the
// result is written to the variable itself, and the move is not made.
func (f *funcGen) writeStraight(uses map[int]*usage, isTemp func(int) bool) {
	for _, bb := range f.blocks {
		instrs := body(bb)
		for idx := 0; idx+1 < len(instrs); idx++ {
			made, move := instrs[idx], instrs[idx+1]
			if made.Dest == nil || !direct[made.Op] || f.folded[made] || f.typeTest[made] != "" {
				continue
			}
			id := made.Dest.ID
			if !isTemp(id) || uses[id].reads != 1 || f.inReturn[id] {
				continue
			}
			if _, placed := f.window[id]; placed {
				continue
			}
			if move.Op != ir.OpMov || move.Dest == nil || len(move.Operands) != 1 || f.folded[move] {
				continue
			}
			if source, ok := regID(move.Operands[0]); !ok || source != id {
				continue
			}
			// Into a variable that has a place of its own for the whole
			// function, which a temporary has not
			if isTemp(move.Dest.ID) {
				continue
			}
			f.straight[id] = move.Dest.ID
			f.folded[move] = true
		}
	}
}

// asksType returns the test a block ends with, if it asks whether a value
// is of one type and branches on the answer: the type's name, the value,
// the branch. A block further down a run must do nothing else: whatever
// it did besides would be skipped by going straight to a case.
func (f *funcGen) asksType(bb *ir.BasicBlock, first bool) (string, ir.Value, *ir.Instruction, bool) {
	instrs := body(bb)
	if len(instrs) == 0 || instrs[len(instrs)-1].Op != ir.OpBr {
		return "", nil, nil, false
	}
	branch := instrs[len(instrs)-1]
	for _, instr := range instrs[:len(instrs)-1] {
		if !first && !f.folded[instr] {
			return "", nil, nil, false
		}
	}
	test, fused := f.fused[branch]
	name, isType := f.typeTest[test]
	if !fused || !isType || len(branch.Operands) != 3 {
		return "", nil, nil, false
	}
	return name, test.Operands[1], branch, true
}

// findSwitches finds the runs of tests of what one value is, where each
// test that fails leads to a block that is only the next test. A match on
// an enum compiles to such a run, one test per case. A run of minSwitch
// tests or more is written as one SWITCH, which goes straight to the case
// of the value's type.
//
// The blocks further down the run keep their own test: a case with a
// condition that fails goes on at the next one.
func (f *funcGen) findSwitches() {
	for _, bb := range f.blocks {
		name, value, branch, ok := f.asksType(bb, true)
		if !ok || f.inSwitch[branch] {
			continue
		}
		subject, _ := regID(value)
		run := &typeSwitch{value: value}
		var links []*ir.Instruction
		seen := map[*ir.BasicBlock]bool{}
		for at := bb; ok && !seen[at]; {
			seen[at] = true
			thenBlock, _ := branch.Operands[1].(*ir.BasicBlock)
			elseBlock, _ := branch.Operands[2].(*ir.BasicBlock)
			if thenBlock == nil || elseBlock == nil {
				break
			}
			run.names = append(run.names, name)
			run.targets = append(run.targets, thenBlock)
			run.otherwise = elseBlock
			links = append(links, branch)

			at = elseBlock
			name, value, branch, ok = f.asksType(at, false)
			if id, isReg := regID(value); ok && (!isReg || id != subject) {
				ok = false // Another value is asked about from there on
			}
		}
		if len(run.names) < minSwitch || len(run.names) > vm.MaxOperand {
			continue
		}
		f.switches[links[0]] = run
		for _, link := range links[1:] {
			f.inSwitch[link] = true
		}
	}
}

// typeTestIntrinsic is the host function `value is Type` and the cases of
// a match are compiled to calls of (irgen.TypeTestIntrinsic), with the
// type as a descriptor: a text the function reads.
const typeTestIntrinsic = "__is"

// typeTestOf recognizes the call that asks whether a value is of one
// struct, or one variant of an enum: the descriptor is then "{Name}", and
// the answer is whether the value carries that name. This is what every
// case of a match on an enum asks, and it needs neither the text nor the
// call. Any other descriptor (several names, fields, a builtin type) is
// left to the function.
func (f *funcGen) typeTestOf(call *ir.Instruction, once func(ir.Value) *usage) (string, bool) {
	if len(call.Operands) != 3 || call.Dest == nil {
		return "", false
	}
	constant := func(v ir.Value, kind ir.TypeRef) (string, *usage) {
		u := once(v)
		if u == nil || u.defInstr.Op != ir.OpMov || len(u.defInstr.Operands) != 1 {
			return "", nil
		}
		c, ok := u.defInstr.Operands[0].(ir.Constant)
		if !ok || c.Type != kind {
			return "", nil
		}
		text, _ := c.Val.(string)
		return text, u
	}
	fn, fnUse := constant(call.Operands[0], "FuncRef")
	desc, descUse := constant(call.Operands[2], "String")
	if fnUse == nil || descUse == nil || fn != typeTestIntrinsic {
		return "", false
	}
	if _, isReg := regID(call.Operands[1]); !isReg {
		return "", false
	}
	if len(desc) < 3 || desc[0] != '{' || desc[len(desc)-1] != '}' || strings.ContainsAny(desc[1:len(desc)-1], "{}|:,=") {
		return "", false
	}
	f.folded[fnUse.defInstr] = true
	f.folded[descUse.defInstr] = true
	return desc[1 : len(desc)-1], true
}

// measure counts the fields a struct or enum value is given where it is
// made, so that it is made with room for them: a value is built by making
// it, then setting its fields one after the other.
//
// A value of an enum that is given nothing holds nothing, and nothing in
// the language can give it anything later: every such value is the same
// one. That is only said of a value no other instruction of the function
// writes to.
func (f *funcGen) measure(made *ir.Instruction, idx int, instrs []*ir.Instruction, uses map[int]*usage) {
	if made.Dest == nil {
		return
	}
	id := made.Dest.ID
	count := 0
	for _, instr := range instrs[idx+1:] {
		if instr.Op != ir.OpSetField || len(instr.Operands) == 0 {
			continue
		}
		if target, ok := regID(instr.Operands[0]); ok && target == id {
			count++
		}
	}
	if count > vm.MaxObjectFields {
		count = vm.MaxObjectFields
	}
	f.fields[made] = uint16(count)

	if made.Op == ir.OpNewVariant && count == 0 && uses[id].defs == 1 && !f.setsFieldOf(id) {
		f.constant[made] = true
	}
}

// setsFieldOf reports whether any instruction of the function sets a
// field of a register.
func (f *funcGen) setsFieldOf(id int) bool {
	for _, bb := range f.blocks {
		for _, instr := range body(bb) {
			if instr.Op != ir.OpSetField || len(instr.Operands) == 0 {
				continue
			}
			if target, ok := regID(instr.Operands[0]); ok && target == id {
				return true
			}
		}
	}
	return false
}

// shortenCall decides how a call gets its function and its arguments.
func (f *funcGen) shortenCall(call *ir.Instruction, idx int, instrs []*ir.Instruction, uses map[int]*usage, isTemp func(int) bool, once func(ir.Value) *usage) {
	if len(call.Operands) == 0 {
		return
	}

	// A method looked for on the value it is called on, which is the first
	// argument: the looking and the calling are one instruction. The
	// value is then asked once the arguments are worked out; what it is a
	// value of does not change meanwhile.
	if u := once(call.Operands[0]); u != nil && u.defInstr.Op == ir.OpGetMethod && len(call.Operands) >= 2 && len(u.defInstr.Operands) == 2 {
		receiver, isReg := regID(u.defInstr.Operands[0])
		first, isFirst := regID(call.Operands[1])
		if c, ok := u.defInstr.Operands[1].(ir.Constant); ok && isReg && isFirst && receiver == first {
			if name, ok := c.Val.(string); ok {
				f.direct[call] = directCall{op: vm.OpCALLM, index: f.g.stringConstant(name, call.Pos)}
				f.folded[u.defInstr] = true
			}
		}
	}

	// A function named where it is called
	if u := once(call.Operands[0]); u != nil && u.defInstr.Op == ir.OpMov && len(u.defInstr.Operands) == 1 {
		if c, ok := u.defInstr.Operands[0].(ir.Constant); ok && c.Type == "FuncRef" {
			name, _ := c.Val.(string)
			if index, known := f.g.protoIndex[name]; known && f.g.capturesNothing[name] {
				f.direct[call] = directCall{op: vm.OpCALLK, index: uint16(index)}
				f.folded[u.defInstr] = true
			} else if !known && f.g.externs[name] {
				f.direct[call] = directCall{op: vm.OpCALLX, index: f.g.extern(name)}
				f.folded[u.defInstr] = true
			}
		}
	}

	// The same for a function named in another block, as `xs[i] = f(y) ?? 0`
	// names put before the branches of ??: a register given a function by
	// its name once, wherever it is read, is that function. What gives it
	// the function is left out when every read is a call (see shorten).
	if _, isDirect := f.direct[call]; !isDirect {
		if op, index, ok := f.namedFunction(call.Operands[0], uses); ok {
			f.direct[call] = directCall{op: op, index: index}
		}
	}

	callFree := func(from, to int) bool {
		for between := from + 1; between < to; between++ {
			if instrs[between].Op == ir.OpCall {
				return false
			}
		}
		return true
	}

	// What is made for this call alone is made in its place. Another call
	// in between would use the same registers for its own arguments.
	defer func() {
		// The result of the call is in r0 until the next call. If its one
		// reader comes before that, it is read from there and not moved.
		if call.Dest == nil {
			return
		}
		u := uses[call.Dest.ID]
		if _, placed := f.window[call.Dest.ID]; placed || !isTemp(call.Dest.ID) || u.reads != 1 {
			return
		}
		if callFree(idx, u.useIdx) {
			f.inReturn[call.Dest.ID] = true
		}
	}()
	for n, operand := range call.Operands {
		u := once(operand)
		if u == nil || u.useInstr != call || (n == 0 && f.folded[u.defInstr]) {
			continue
		}
		if id, ok := regID(operand); ok && callFree(u.defIdx, idx) {
			f.window[id] = n
		}
	}
}

// --- Emission ---

func (f *funcGen) emit(op vm.OpCode, a, b, c uint16) {
	f.g.chunk.AddInstructionWithDebug(vm.NewInstr(op, a, b, c), f.g.debugInfo(f.pos, f.fn.Name))
}

func (f *funcGen) fail(code, msg, suggestion string) {
	f.failed = true
	f.g.report(f.pos, code, msg, suggestion)
}

// reg returns the physical register of a register operand.
func (f *funcGen) reg(v ir.Value) uint16 {
	id, ok := regID(v)
	if !ok {
		f.fail(ErrUnsupportedIR, fmt.Sprintf("expected a register operand, found %v", v), "")
		return 0
	}
	slot, ok := f.phys[id]
	if !ok || slot >= maxRegisters {
		// Too many registers; reported once the function is complete
		return 0
	}
	return uint16(slot)
}

// dest returns the physical register an instruction writes to.
func (f *funcGen) dest(instr *ir.Instruction) uint16 {
	if instr.Dest == nil {
		f.fail(ErrUnsupportedIR, fmt.Sprintf("%s has no destination register", instr.Op), "")
		return 0
	}
	return f.reg(instr.Dest)
}

// emitJump emits a jump to a block: JMP32, JMPIF32 or JMPIFNOT32 on the
// register cond. It is one instruction holding the address where it can
// be, and the three-word long form otherwise.
func (f *funcGen) emitJump(op vm.OpCode, cond uint16, target *ir.BasicBlock) {
	if !f.long {
		switch op {
		case vm.OpJMP32:
			f.fixups = append(f.fixups, fixup{idx: len(f.g.chunk.Code), target: target, field: 'A'})
			f.emit(vm.OpJMP, 0, 0, 0)
			return
		case vm.OpJMPIF32, vm.OpJMPIFNOT32:
			short := vm.OpJMPIF
			if op == vm.OpJMPIFNOT32 {
				short = vm.OpJMPIFNOT
			}
			f.fixups = append(f.fixups, fixup{idx: len(f.g.chunk.Code), target: target, field: 'B'})
			f.emit(short, cond, 0, 0)
			return
		}
	}
	f.emit(vm.OpLONG, 0, 0, 0)
	f.emit(op, cond, 0, 0)
	f.fixups = append(f.fixups, fixup{idx: len(f.g.chunk.Code), target: target})
	f.emit(vm.OpNOP, 0, 0, 0) // Offset word, patched later
}

// emitSwitch emits a jump to the case of a value's type: SWITCH, then one
// CASE word per type.
func (f *funcGen) emitSwitch(run *typeSwitch) {
	f.fixups = append(f.fixups, fixup{idx: len(f.g.chunk.Code), target: run.otherwise, field: 'C'})
	f.emit(vm.OpSWITCH, f.reg(run.value), uint16(len(run.names)), 0)
	for i, name := range run.names {
		f.fixups = append(f.fixups, fixup{idx: len(f.g.chunk.Code), target: run.targets[i], field: 'B'})
		f.emit(vm.OpCASE, f.g.stringConstant(name, f.pos), 0, 0)
	}
}

// emitTest emits a test that jumps to a block unless it holds, in place
// of the test and the branch on its result.
func (f *funcGen) emitTest(test *ir.Instruction, otherwise *ir.BasicBlock) {
	at := len(f.g.chunk.Code)
	if name, ok := f.typeTest[test]; ok {
		f.emit(vm.OpJNTYPE, f.reg(test.Operands[1]), f.g.stringConstant(name, f.pos), 0)
	} else if small, ok := f.small[test]; ok {
		// With the number on the left the test reads the other way round
		op, value := againstSmall[test.Op][1-small.operand], small.value
		if small.pooled {
			op, value = againstConstant[test.Op][1-small.operand], f.g.constant(small.number, f.pos)
		}
		f.emit(op, f.reg(test.Operands[1-small.operand]), value, 0)
	} else {
		how := fusable[test.Op]
		a, b := f.reg(test.Operands[0]), f.reg(test.Operands[1])
		if how.swapped {
			a, b = b, a
		}
		f.emit(how.op, a, b, 0)
	}
	f.fixups = append(f.fixups, fixup{idx: at, target: otherwise, field: 'C'})
}

func (f *funcGen) generateBlock(bb, next *ir.BasicBlock) {
	instrs := body(bb)
	for _, instr := range instrs {
		if instr.Pos.Line > 0 {
			f.pos = instr.Pos
		}
		if f.folded[instr] {
			continue // Another instruction does its work
		}
		f.generateInstr(instr, next)
	}

	// A block must not fall into whatever happens to be placed after it
	if len(instrs) == 0 || !isTerminator(instrs[len(instrs)-1].Op) {
		f.emit(vm.OpLOADNULL, returnReg, 0, 0)
		f.emit(vm.OpRET, returnReg, 0, 0)
	}
}

var binaryOps = map[ir.OpType]vm.OpCode{
	ir.OpAdd:    vm.OpADD,
	ir.OpSub:    vm.OpSUB,
	ir.OpMul:    vm.OpMUL,
	ir.OpDiv:    vm.OpDIV,
	ir.OpMod:    vm.OpMOD,
	ir.OpBitAnd: vm.OpBAND, ir.OpBitOr: vm.OpBOR, ir.OpBitXor: vm.OpBXOR, ir.OpShl: vm.OpSHL, ir.OpShr: vm.OpSHR,
	ir.OpEq:  vm.OpEQ,
	ir.OpNeq: vm.OpNE,
	ir.OpLt:  vm.OpLT,
	ir.OpLte: vm.OpLE,
	ir.OpGt:  vm.OpGT,
	ir.OpGte: vm.OpGE,
}

func (f *funcGen) generateInstr(instr *ir.Instruction, next *ir.BasicBlock) {
	if op, ok := binaryOps[instr.Op]; ok {
		if !f.arity(instr, 2) {
			return
		}
		if small, ok := f.small[instr]; ok {
			op, value := vm.OpADDI, small.value
			if instr.Op == ir.OpSub {
				op = vm.OpSUBI
			}
			if small.pooled {
				op, value = withConstant[instr.Op], f.g.constant(small.number, f.pos)
			}
			f.emit(op, f.dest(instr), f.reg(instr.Operands[1-small.operand]), value)
			return
		}
		f.emit(op, f.dest(instr), f.reg(instr.Operands[0]), f.reg(instr.Operands[1]))
		return
	}

	switch instr.Op {
	case ir.OpNop:

	case ir.OpMov:
		if f.arity(instr, 1) {
			f.generateMov(instr)
		}

	case ir.OpNeg:
		if f.arity(instr, 1) {
			f.emit(vm.OpNEG, f.dest(instr), f.reg(instr.Operands[0]), 0)
		}
	case ir.OpNot:
		if f.arity(instr, 1) {
			f.emit(vm.OpNOT, f.dest(instr), f.reg(instr.Operands[0]), 0)
		}

	case ir.OpJmp:
		if target, ok := f.blockOperand(instr, 0); ok && target != next {
			f.emitJump(vm.OpJMP32, 0, target)
		}

	case ir.OpBr:
		thenBlock, ok1 := f.blockOperand(instr, 1)
		elseBlock, ok2 := f.blockOperand(instr, 2)
		if !ok1 || !ok2 {
			return
		}
		if run, ok := f.switches[instr]; ok {
			f.emitSwitch(run)
			return
		}
		if test, ok := f.fused[instr]; ok {
			f.emitTest(test, elseBlock)
		} else {
			f.emitJump(vm.OpJMPIFNOT32, f.reg(instr.Operands[0]), elseBlock)
		}
		if thenBlock != next {
			f.emitJump(vm.OpJMP32, 0, thenBlock)
		}

	case ir.OpRet:
		if len(instr.Operands) == 0 {
			f.emit(vm.OpLOADNULL, returnReg, 0, 0)
			f.emit(vm.OpRET, returnReg, 0, 0)
			return
		}
		f.emit(vm.OpRET, f.reg(instr.Operands[0]), 0, 0)

	case ir.OpTry:
		// The handler is given as a jump is, by how far away it is; then
		// the code goes on at the body
		handler, ok1 := f.blockOperand(instr, 0)
		bodyBlock, ok2 := f.blockOperand(instr, 1)
		if !ok1 || !ok2 {
			return
		}
		f.emitJump(vm.OpTRY32, 0, handler)
		if bodyBlock != next {
			f.emitJump(vm.OpJMP32, 0, bodyBlock)
		}
	case ir.OpEndTry:
		f.emit(vm.OpENDTRY, 0, 0, 0)
	case ir.OpRethrow:
		f.emit(vm.OpRETHROW, 0, 0, 0)

	case ir.OpCall:
		f.generateCall(instr)

	case ir.OpNewStruct:
		// The struct's name is recorded on the value; casts check it
		if name, ok := f.nameOperand(instr, 0); ok {
			f.emit(vm.OpNEWOBJ, f.dest(instr), f.g.stringConstant(name, f.pos), vm.ObjectIsNamed|f.fields[instr]<<vm.ObjectFieldsShift)
		}

	case ir.OpNewVariant:
		// A value of an enum: the object carries the variant's name, and a
		// mark that it is compared and printed by what it holds
		if name, ok := f.nameOperand(instr, 0); ok {
			what := vm.ObjectIsVariant | f.fields[instr]<<vm.ObjectFieldsShift
			if f.constant[instr] {
				what |= vm.ObjectIsConstant
			}
			f.emit(vm.OpNEWOBJ, f.dest(instr), f.g.stringConstant(name, f.pos), what)
		}

	case ir.OpGetMethod:
		if name, ok := f.nameOperand(instr, 1); ok {
			f.emit(vm.OpGETMETHOD, f.dest(instr), f.reg(instr.Operands[0]), f.g.stringConstant(name, f.pos))
		}
	case ir.OpGetField:
		if name, ok := f.nameOperand(instr, 1); ok {
			f.emit(vm.OpGETFIELD, f.dest(instr), f.reg(instr.Operands[0]), f.g.stringConstant(name, f.pos))
		}
	case ir.OpSetField:
		if name, ok := f.nameOperand(instr, 1); ok && f.arity(instr, 3) {
			f.emit(vm.OpSETFIELD, f.reg(instr.Operands[0]), f.g.stringConstant(name, f.pos), f.reg(instr.Operands[2]))
		}

	case ir.OpNewArray:
		capacity, _ := f.intOperand(instr, 0)
		if capacity > vm.MaxOperand {
			capacity = vm.MaxOperand // Only a hint; arrays grow on push
		}
		f.emit(vm.OpNEWARRAY, f.dest(instr), uint16(capacity), 0)
	case ir.OpGetIndex:
		if f.arity(instr, 2) {
			f.emit(vm.OpGETINDEX, f.dest(instr), f.reg(instr.Operands[0]), f.reg(instr.Operands[1]))
		}
	case ir.OpSetIndex:
		// SETELEM, not SETINDEX: assigning to an element must not grow
		// the array
		if f.arity(instr, 3) {
			f.emit(vm.OpSETELEM, f.reg(instr.Operands[0]), f.reg(instr.Operands[1]), f.reg(instr.Operands[2]))
		}
	case ir.OpOnce:
		if f.arity(instr, 1) {
			name, _ := instr.Operands[0].(ir.Constant).Val.(string)
			index, known := f.g.protoIndex[name]
			if !known {
				f.fail(ErrUnsupportedIR, fmt.Sprintf("the constant made by %s has no function", name), "")
				break
			}
			// A variable's place is changed, a constant's value never
			variable := uint16(0)
			if strings.HasPrefix(name, resolve.VariablePrefix) {
				variable = 1
			}
			f.emit(vm.OpONCE, f.dest(instr), uint16(index), variable)
		}
	case ir.OpLen:
		if f.arity(instr, 1) {
			f.emit(vm.OpARRLEN, f.dest(instr), f.reg(instr.Operands[0]), 0)
		}
	case ir.OpPush:
		if f.arity(instr, 2) {
			f.emit(vm.OpARRPUSH, f.reg(instr.Operands[0]), f.reg(instr.Operands[1]), 0)
		}

	case ir.OpMakeClosure:
		f.generateClosure(instr)
	case ir.OpCloseUpvalues:
		for _, operand := range instr.Operands {
			f.emit(vm.OpCLOSEUP, f.reg(operand), 0, 0)
		}
	case ir.OpStage:
		// Where each variable is, at the call just written
		name, _ := instr.Operands[0].(ir.Constant).Val.(string)
		shape, _ := instr.Operands[1].(ir.Constant).Val.(map[string]string)
		stage := vm.Stage{Name: name, At: len(f.g.chunk.Code) - 1, Shape: shape}
		for i := 2; i+1 < len(instr.Operands); i += 2 {
			variable, _ := instr.Operands[i].(ir.Constant).Val.(string)
			stage.Vars = append(stage.Vars, vm.StageVar{Name: variable, Reg: int(f.reg(instr.Operands[i+1]))})
		}
		f.stages = append(f.stages, stage)
	case ir.OpLoadUpvalue:
		if idx, ok := f.intOperand(instr, 0); ok {
			f.emit(vm.OpLDCAP, f.dest(instr), uint16(idx), 0)
		}
	case ir.OpStoreUpvalue:
		if idx, ok := f.intOperand(instr, 0); ok && f.arity(instr, 2) {
			f.emit(vm.OpSTCAP, uint16(idx), f.reg(instr.Operands[1]), 0)
		}

	default:
		f.fail(ErrUnsupportedIR, fmt.Sprintf("IR operation %s cannot be compiled to bytecode yet", instr.Op), "")
	}
}

// generateMov lowers a move from a register or a constant.
func (f *funcGen) generateMov(instr *ir.Instruction) {
	dest := f.dest(instr)

	if _, ok := regID(instr.Operands[0]); ok {
		if src := f.reg(instr.Operands[0]); src != dest {
			f.emit(vm.OpMOV, dest, src, 0)
		}
		return
	}

	c, ok := instr.Operands[0].(ir.Constant)
	if !ok {
		f.fail(ErrUnsupportedIR, fmt.Sprintf("cannot move %v into a register", instr.Operands[0]), "")
		return
	}

	switch c.Type {
	case "Null":
		f.emit(vm.OpLOADNULL, dest, 0, 0)

	case "Bool":
		var b uint16
		if v, _ := c.Val.(bool); v {
			b = 1
		}
		f.emit(vm.OpLOADBOOL, dest, b, 0)

	case "Int":
		n, ok := toInt64(c.Val)
		if !ok {
			f.fail(ErrUnsupportedIR, fmt.Sprintf("invalid integer constant %v", c.Val), "")
			return
		}
		if n >= 0 && n <= vm.MaxOperand {
			f.emit(vm.OpLOADI, dest, uint16(n), 0)
			return
		}
		f.emit(vm.OpLOADK, dest, f.g.constant(vm.NewInt(n), f.pos), 0)

	case "Float":
		v, _ := c.Val.(float64)
		f.emit(vm.OpLOADK, dest, f.g.constant(vm.NewNumber(v), f.pos), 0)

	case "String":
		s, _ := c.Val.(string)
		f.emit(vm.OpNEWSTR, dest, f.g.stringConstant(s, f.pos), 0)

	case "FuncRef":
		name, _ := c.Val.(string)
		f.loadFunction(dest, name)

	default:
		f.fail(ErrUnsupportedIR, fmt.Sprintf("constant of type %s cannot be loaded", c.Type), "")
	}
}

// loadFunction puts a function object for the named function in dest.
func (f *funcGen) loadFunction(dest uint16, name string) {
	if idx, ok := f.g.protoIndex[name]; ok {
		f.emit(vm.OpNEWFUNC, dest, uint16(idx), 0)
		return
	}
	if f.g.externs[name] {
		f.emit(vm.OpNEWEXFUNC, dest, f.g.extern(name), 0)
		return
	}
	f.fail(ErrUndefinedFunc, fmt.Sprintf("undefined function '%s'", name), "")
}

// generateCall moves the callee and its arguments into the call window,
// calls, and copies the result out of r0.
func (f *funcGen) generateCall(instr *ir.Instruction) {
	if len(instr.Operands) == 0 {
		f.fail(ErrUnsupportedIR, "call without a callee", "")
		return
	}

	if name, ok := f.typeTest[instr]; ok {
		// Whether a value is of one type is asked of the value
		f.emit(vm.OpISTYPE, f.dest(instr), f.reg(instr.Operands[1]), f.g.stringConstant(name, f.pos))
		return
	}
	if op, ok := f.builtin[instr]; ok {
		if instr.Dest != nil && !f.unread[instr.Dest.ID] {
			f.emit(op, f.dest(instr), f.reg(instr.Operands[1]), 0)
		}
		return
	}

	nargs := len(instr.Operands) - 1
	if f.callBase+nargs >= maxRegisters {
		f.fail(ErrCallTooLarge,
			fmt.Sprintf("call with %d arguments does not fit in the register window of '%s'", nargs, f.fn.Name),
			"Pass fewer arguments or split the function")
		return
	}

	direct, isDirect := f.direct[instr]
	for i, operand := range instr.Operands {
		if i == 0 && isDirect {
			continue // No function value is needed
		}
		// What was made for this call is already in its place
		if src, dest := f.reg(operand), uint16(f.callBase+i); src != dest {
			f.emit(vm.OpMOV, dest, src, 0)
		}
	}
	if isDirect {
		f.emit(direct.op, uint16(f.callBase), uint16(nargs), direct.index)
	} else {
		f.emit(vm.OpCALL, uint16(f.callBase), uint16(nargs), 0)
	}

	// The result is left in r0; it is moved only if something reads it
	if instr.Dest != nil && !f.unread[instr.Dest.ID] {
		if dest := f.reg(instr.Dest); dest != returnReg {
			f.emit(vm.OpMOV, dest, returnReg, 0)
		}
	}
}

// generateClosure creates a function object and binds its upvalues.
func (f *funcGen) generateClosure(instr *ir.Instruction) {
	if len(instr.Operands) == 0 {
		f.fail(ErrUnsupportedIR, "closure without a function", "")
		return
	}
	ref, ok := instr.Operands[0].(ir.Constant)
	name, isName := ref.Val.(string)
	if !ok || !isName {
		f.fail(ErrUnsupportedIR, "closure target must be a function reference", "")
		return
	}
	idx, ok := f.g.protoIndex[name]
	if !ok {
		f.fail(ErrUndefinedFunc, fmt.Sprintf("undefined function '%s'", name), "")
		return
	}

	dest := f.dest(instr)
	f.emit(vm.OpNEWFUNC, dest, uint16(idx), 0)

	for i, capture := range instr.Operands[1:] {
		if _, isReg := regID(capture); isReg {
			// A local of this function, shared by reference
			f.emit(vm.OpCAPLOCAL, dest, uint16(i), f.reg(capture))
			continue
		}
		c, isConst := capture.(ir.Constant)
		upvalIdx, isInt := toInt64(c.Val)
		if !isConst || c.Type != "Upvalue" || !isInt {
			f.fail(ErrUnsupportedIR, fmt.Sprintf("cannot capture %v", capture), "")
			continue
		}
		// A variable this function captured itself, passed on
		f.emit(vm.OpCAPUPVAL, dest, uint16(i), uint16(upvalIdx))
	}
}

// --- Operand helpers ---

func (f *funcGen) arity(instr *ir.Instruction, n int) bool {
	if len(instr.Operands) < n {
		f.fail(ErrUnsupportedIR, fmt.Sprintf("%s expects %d operands, found %d", instr.Op, n, len(instr.Operands)), "")
		return false
	}
	return true
}

func (f *funcGen) blockOperand(instr *ir.Instruction, i int) (*ir.BasicBlock, bool) {
	if i < len(instr.Operands) {
		if bb, ok := instr.Operands[i].(*ir.BasicBlock); ok {
			return bb, true
		}
	}
	f.fail(ErrUnsupportedIR, fmt.Sprintf("%s expects a block as operand %d", instr.Op, i), "")
	return nil, false
}

func (f *funcGen) nameOperand(instr *ir.Instruction, i int) (string, bool) {
	if i < len(instr.Operands) {
		if c, ok := instr.Operands[i].(ir.Constant); ok {
			if name, ok := c.Val.(string); ok {
				return name, true
			}
		}
	}
	f.fail(ErrUnsupportedIR, fmt.Sprintf("%s expects a name as operand %d", instr.Op, i), "")
	return "", false
}

func (f *funcGen) intOperand(instr *ir.Instruction, i int) (int64, bool) {
	if i < len(instr.Operands) {
		if c, ok := instr.Operands[i].(ir.Constant); ok {
			if n, ok := toInt64(c.Val); ok {
				return n, true
			}
		}
	}
	f.fail(ErrUnsupportedIR, fmt.Sprintf("%s expects an integer as operand %d", instr.Op, i), "")
	return 0, false
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	}
	return 0, false
}

func inboxesOf(inboxes []ir.Inbox) []vm.Inbox {
	var out []vm.Inbox
	for _, inbox := range inboxes {
		out = append(out, vm.Inbox{Name: inbox.Name, Capacity: inbox.Capacity, Message: inbox.Message})
	}
	return out
}
