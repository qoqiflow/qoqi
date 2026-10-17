package irgen

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/ir"
)

// compileIfStmt compiles an if statement
func (fc *FunctionCompiler) compileIfStmt(stmt *ast.IfStmt) {
	// Create basic blocks
	thenBlock := fc.builder.NewBlock()
	mergeBlock := fc.builder.NewBlock()
	var elseBlock *ir.BasicBlock

	// Branch
	if stmt.Else != nil {
		elseBlock = fc.builder.NewBlock()
	}
	made := len(fc.builder.Func.Blocks)
	if stmt.Else != nil {
		fc.compileCondition(stmt.Cond, thenBlock, elseBlock)
	} else {
		fc.compileCondition(stmt.Cond, thenBlock, mergeBlock)
	}
	// What the condition made on its way comes before what it guards
	fc.builder.PlaceBefore(thenBlock, made)

	// Then block
	fc.builder.SetBlock(thenBlock)
	fc.compileStmt(stmt.Then)
	// Only add jump if the block doesn't already end with a return
	if !fc.blockEndsWithTerminator() {
		fc.builder.CreateJmp(mergeBlock)
	}

	// Else block (if present)
	if stmt.Else != nil {
		fc.builder.SetBlock(elseBlock)
		fc.compileStmt(stmt.Else)
		if !fc.blockEndsWithTerminator() {
			fc.builder.CreateJmp(mergeBlock)
		}
	}

	// Merge block
	fc.builder.SetBlock(mergeBlock)
}

// compileCondition compiles an expression for where it leads rather than
// for its value: to one block if it holds, to another if not.
//
// `a && b` as a value is worked out into a register on two paths, which is
// then tested. As a condition it needs no register: if a does not hold
// the answer is known and the code goes straight to where "not" leads,
// and b decides otherwise. The same for `||` and `!`, to any depth. A test
// of two numbers in a condition is then a single instruction that jumps
// (see codegen), where it was a comparison, a move and two jumps.
func (fc *FunctionCompiler) compileCondition(cond ast.Expr, holds, fails *ir.BasicBlock) {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		switch e.Op {
		case "&&":
			rest := fc.builder.NewBlock()
			fc.compileCondition(e.Left, rest, fails)
			fc.builder.SetBlock(rest)
			fc.compileCondition(e.Right, holds, fails)
			return
		case "||":
			rest := fc.builder.NewBlock()
			fc.compileCondition(e.Left, holds, rest)
			fc.builder.SetBlock(rest)
			fc.compileCondition(e.Right, holds, fails)
			return
		}
	case *ast.UnaryExpr:
		if e.Op == "!" {
			fc.compileCondition(e.Expr, fails, holds)
			return
		}
	}
	fc.builder.CreateBr(fc.compileExpr(cond), holds, fails)
}

// compileWhileStmt compiles a while loop. One the desugarer made may have
// no condition, and run until it is left; and it may have an update, run
// after each iteration and after `continue`, which may `break`.
func (fc *FunctionCompiler) compileWhileStmt(stmt *ast.WhileStmt) {
	// Create basic blocks, the condition's first: the order they are made
	// in is the order they are laid out in
	var condBlock *ir.BasicBlock
	if stmt.Cond != nil {
		condBlock = fc.builder.NewBlock()
	}
	bodyBlock := fc.builder.NewBlock()
	exitBlock := fc.builder.NewBlock()

	// Where each iteration starts: the condition, if there is one
	top := bodyBlock
	if stmt.Cond != nil {
		top = condBlock
		fc.builder.CreateJmp(top)
		fc.builder.SetBlock(top)
		made := len(fc.builder.Func.Blocks)
		fc.compileCondition(stmt.Cond, bodyBlock, exitBlock)
		fc.builder.PlaceBefore(bodyBlock, made)
	} else {
		fc.builder.CreateJmp(bodyBlock)
	}

	// An iteration goes on at the update, if there is one, `continue`
	// included
	next := top
	var updateBlock *ir.BasicBlock
	if stmt.Update != nil {
		updateBlock = fc.builder.NewBlock()
		next = updateBlock
	}

	// Push loop context
	loop := fc.newLoop(exitBlock, next)

	// Body block
	fc.builder.SetBlock(bodyBlock)
	fc.compileStmt(stmt.Body)
	if !fc.blockEndsWithTerminator() {
		fc.endIteration(loop)
		fc.builder.CreateJmp(next)
	}

	// The update comes after the body, which falls into it. The loop is
	// still the one a `break` in it leaves
	if updateBlock != nil {
		fc.builder.PlaceLast(updateBlock)
		fc.builder.SetBlock(updateBlock)
		fc.compileStmt(stmt.Update)
		if !fc.blockEndsWithTerminator() {
			fc.builder.CreateJmp(top)
		}
	}

	// Pop loop context
	fc.loopStack = fc.loopStack[:len(fc.loopStack)-1]

	// Exit block
	// What follows the loop comes after the loop: it was made first, to
	// have somewhere to leave to
	fc.builder.PlaceLast(exitBlock)
	fc.builder.SetBlock(exitBlock)
}

// compileForStmt compiles a for-in loop over an array or string.
// Desugaring normally rewrites these into while loops before IR generation;
// this handles any that reach it directly.
//
//	index = 0; length = len(iterable)
//	cond: br index < length, body, exit
//	body: item = iterable[index]; index = index + 1; ...; jmp cond
func (fc *FunctionCompiler) compileForStmt(stmt *ast.ForStmt) {
	iterable := fc.compileExpr(stmt.Iterable)
	fc.setPos(stmt)

	index := fc.builder.CreateMov(ir.Constant{Val: int64(0), Type: "Int"})
	fc.varRegs[index.ID] = true
	one := fc.builder.CreateMov(ir.Constant{Val: int64(1), Type: "Int"})
	length := fc.builder.NewRegister()
	fc.builder.Emit(ir.OpLen, length, iterable)

	condBlock := fc.builder.NewBlock()
	bodyBlock := fc.builder.NewBlock()
	exitBlock := fc.builder.NewBlock()

	fc.builder.CreateJmp(condBlock)

	fc.builder.SetBlock(condBlock)
	inRange := fc.builder.NewRegister()
	fc.builder.Emit(ir.OpLt, inRange, index, length)
	fc.builder.CreateBr(inRange, bodyBlock, exitBlock)

	loop := fc.newLoop(exitBlock, condBlock)

	fc.builder.SetBlock(bodyBlock)
	fc.pushScope()
	item := fc.builder.NewRegister()
	fc.builder.Emit(ir.OpGetIndex, item, iterable, index)
	fc.declare(stmt.Var, item)
	if stmt.Index != nil {
		// A copy of the position: assigning to it must not move the loop
		fc.declare(stmt.Index.Name, fc.builder.CreateMov(index))
	}
	// Advance before the body so `continue` cannot skip it
	fc.builder.Emit(ir.OpAdd, index, index, one)
	fc.compileStmt(stmt.Body)
	fc.popScope()
	if !fc.blockEndsWithTerminator() {
		fc.endIteration(loop)
		fc.builder.CreateJmp(condBlock)
	}

	fc.loopStack = fc.loopStack[:len(fc.loopStack)-1]

	// What follows the loop comes after the loop: it was made first, to
	// have somewhere to leave to
	fc.builder.PlaceLast(exitBlock)
	fc.builder.SetBlock(exitBlock)
}

// compileBreakStmt compiles a break statement
func (fc *FunctionCompiler) compileBreakStmt(stmt *ast.BreakStmt) {
	if len(fc.loopStack) == 0 {
		fc.gen.errorAt(stmt, ErrLoopControl, "'break' outside of a loop", "")
		return
	}
	ctx := fc.loopStack[len(fc.loopStack)-1]
	fc.runDefers(ctx.scopeDepth)
	fc.builder.CreateJmp(ctx.breakTarget)
	fc.startDeadBlock()
}

// compileContinueStmt compiles a continue statement
func (fc *FunctionCompiler) compileContinueStmt(stmt *ast.ContinueStmt) {
	if len(fc.loopStack) == 0 {
		fc.gen.errorAt(stmt, ErrLoopControl, "'continue' outside of a loop", "")
		return
	}
	ctx := fc.loopStack[len(fc.loopStack)-1]
	fc.runDefers(ctx.scopeDepth)
	// `continue` ends the iteration too
	fc.endIteration(ctx)
	fc.builder.CreateJmp(ctx.continueTarget)
	fc.startDeadBlock()
}

// compileMatchStmt rejects match statements: desugaring turns every match it
// supports into an if-else chain, so one reaching IR generation uses a
// pattern that cannot be lowered yet.
func (fc *FunctionCompiler) compileMatchStmt(stmt *ast.MatchStmt) {
	fc.gen.errorAt(stmt, ErrUnsupportedConstruct,
		"this match statement cannot be compiled yet",
		"Match on literal values, or use if/else")
}

// blockEndsWithTerminator checks if the current block ends with a terminator
func (fc *FunctionCompiler) blockEndsWithTerminator() bool {
	if len(fc.builder.CurrentBlock.Instructions) == 0 {
		return false
	}
	last := fc.builder.CurrentBlock.Instructions[len(fc.builder.CurrentBlock.Instructions)-1]
	switch last.Op {
	case ir.OpRet, ir.OpJmp, ir.OpBr, ir.OpTry, ir.OpRethrow:
		return true
	}
	return false
}
