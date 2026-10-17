package ir

import "github.com/qoqiflow/qoqi/internal/token"

// Builder helps construct the IR
type Builder struct {
	Func         *Function
	CurrentBlock *BasicBlock

	// Pos is attached to every emitted instruction. The IR generator
	// updates it as it walks the AST.
	Pos token.Position
}

// NewBuilder creates a new builder for a function
func NewBuilder(funcName string) *Builder {
	entry := &BasicBlock{ID: 0}
	fn := &Function{
		Name:       funcName,
		Blocks:     []*BasicBlock{entry},
		EntryBlock: entry,
		RegCount:   0,
	}
	return &Builder{
		Func:         fn,
		CurrentBlock: entry,
	}
}

// NewRegister creates a new virtual register
func (b *Builder) NewRegister() *Register {
	b.Func.RegCount++
	return &Register{ID: b.Func.RegCount}
}

// NewBlock creates a new basic block
func (b *Builder) NewBlock() *BasicBlock {
	id := len(b.Func.Blocks)
	bb := &BasicBlock{ID: id}
	b.Func.Blocks = append(b.Func.Blocks, bb)
	return bb
}

// PlaceBefore moves the blocks made since there were `since` of them to
// just before a block. Blocks are written out in the order they are in, and
// a block that follows the one that leads to it needs no jump to be
// reached: the blocks a condition makes on its way belong before the code
// the condition guards, which was made first because they lead to it.
func (b *Builder) PlaceBefore(target *BasicBlock, since int) {
	blocks := b.Func.Blocks
	if since >= len(blocks) {
		return
	}
	at := -1
	for i, bb := range blocks[:since] {
		if bb == target {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	moved := append([]*BasicBlock(nil), blocks[since:]...)
	rest := append([]*BasicBlock(nil), blocks[at:since]...)
	b.Func.Blocks = append(append(blocks[:at], moved...), rest...)
}

// PlaceLast moves a block after all the others made so far: where what
// follows a loop belongs, once the loop's own blocks are known.
func (b *Builder) PlaceLast(target *BasicBlock) {
	blocks := b.Func.Blocks
	for i, bb := range blocks {
		if bb == target {
			copy(blocks[i:], blocks[i+1:])
			blocks[len(blocks)-1] = target
			return
		}
	}
}

// SetBlock sets the current block for insertion
func (b *Builder) SetBlock(bb *BasicBlock) {
	b.CurrentBlock = bb
}

// AddParam adds a parameter to the function
func (b *Builder) AddParam() *Register {
	reg := b.NewRegister()
	b.Func.Params = append(b.Func.Params, reg)
	return reg
}

// Emit adds an instruction to the current block
func (b *Builder) Emit(op OpType, dest *Register, operands ...Value) *Instruction {
	instr := &Instruction{
		Op:       op,
		Dest:     dest,
		Operands: operands,
		Pos:      b.Pos,
	}
	b.CurrentBlock.Instructions = append(b.CurrentBlock.Instructions, instr)
	return instr
}

// CreateAdd creates an addition instruction
func (b *Builder) CreateAdd(lhs, rhs Value) *Register {
	dest := b.NewRegister()
	b.Emit(OpAdd, dest, lhs, rhs)
	return dest
}

// CreateSub creates a subtraction instruction
func (b *Builder) CreateSub(lhs, rhs Value) *Register {
	dest := b.NewRegister()
	b.Emit(OpSub, dest, lhs, rhs)
	return dest
}

// CreateMov creates a move instruction (assignment)
func (b *Builder) CreateMov(src Value) *Register {
	dest := b.NewRegister()
	b.Emit(OpMov, dest, src)
	return dest
}

// CreateRet creates a return instruction
func (b *Builder) CreateRet(val Value) {
	if val == nil {
		b.Emit(OpRet, nil)
	} else {
		b.Emit(OpRet, nil, val)
	}
}

// CreateCallTrait creates a trait method call instruction
// Dest = CallTrait Receiver, TraitName, MethodName, Args...
func (b *Builder) CreateCallTrait(receiver Value, traitName string, methodName string, args ...Value) *Register {
	dest := b.NewRegister()
	operands := []Value{receiver, Constant{Val: traitName, Type: "String"}, Constant{Val: methodName, Type: "String"}}
	operands = append(operands, args...)
	b.Emit(OpCallTrait, dest, operands...)
	return dest
}

// CreateNewStruct creates a new struct
func (b *Builder) CreateNewStruct(typeName string, fields ...Value) *Register {
	dest := b.NewRegister()
	operands := []Value{Constant{Val: typeName, Type: "String"}}
	operands = append(operands, fields...)
	b.Emit(OpNewStruct, dest, operands...)
	return dest
}

// CreateGetField accesses a field of a struct
func (b *Builder) CreateGetField(structVal Value, fieldName string) *Register {
	dest := b.NewRegister()
	b.Emit(OpGetField, dest, structVal, Constant{Val: fieldName, Type: "String"})
	return dest
}

// CreateGetMethod finds a method on a value whose type is only known when
// the program runs
func (b *Builder) CreateGetMethod(value Value, methodName string) *Register {
	dest := b.NewRegister()
	b.Emit(OpGetMethod, dest, value, Constant{Val: methodName, Type: "String"})
	return dest
}

// CreateJmp creates an unconditional jump
func (b *Builder) CreateJmp(target *BasicBlock) {
	b.Emit(OpJmp, nil, target)
}

// CreateBr creates a conditional branch
func (b *Builder) CreateBr(cond Value, thenBlock, elseBlock *BasicBlock) {
	b.Emit(OpBr, nil, cond, thenBlock, elseBlock)
}

// CreateCall creates a function call instruction
func (b *Builder) CreateCall(fn Value, args ...Value) *Register {
	dest := b.NewRegister()
	operands := []Value{fn}
	operands = append(operands, args...)
	b.Emit(OpCall, dest, operands...)
	return dest
}

// CreateLoadUpvalue loads from an upvalue
func (b *Builder) CreateLoadUpvalue(index int) *Register {
	dest := b.NewRegister()
	b.Emit(OpLoadUpvalue, dest, Constant{Val: index, Type: "Int"})
	return dest
}

// CreateStoreUpvalue stores to an upvalue
func (b *Builder) CreateStoreUpvalue(index int, val Value) {
	b.Emit(OpStoreUpvalue, nil, Constant{Val: index, Type: "Int"}, val)
}

// CreateMakeClosure creates a closure with captured variables
func (b *Builder) CreateMakeClosure(fn Value, captures ...Value) *Register {
	dest := b.NewRegister()
	operands := []Value{fn}
	operands = append(operands, captures...)
	b.Emit(OpMakeClosure, dest, operands...)
	return dest
}
