package irgen

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/ir"
)

// Cleanup: `defer`.
//
// What a block defers runs when the block is left, whichever way. There
// are two kinds of ways, compiled differently.
//
// The ways the compiler sees, it writes the cleanup into: the end of the
// block, a return, a `?` that returns, a break or continue of a loop
// around the block. Each gets a copy of the deferred statements, the last
// deferred first.
//
// A failure (a panic, a division by zero, anything `attempt` could
// survive) is not in the code: it can come from any instruction. For it
// the function tells the VM, when the defer is reached, where a handler
// is: a block that runs the same statements and then passes the failure
// on. Every copy written for the ordinary ways starts by taking that
// handler back.
//
//	defer close();        try handler, body
//	work();               body:    work()
//	                               end_try; close()      <- the end of the block
//	                      handler: close(); rethrow      <- a failure in work()

// deferred is a defer of a scope that has not been left yet.
type deferred struct {
	stmt *ast.DeferStmt

	// The variables as they were where the defer was written: a name
	// declared after it must not be what its statements find
	scopes []map[string]*ir.Register
}

// compileDeferStmt registers the cleanup with the scope it is written in.
func (fc *FunctionCompiler) compileDeferStmt(stmt *ast.DeferStmt) {
	d := &deferred{stmt: stmt, scopes: make([]map[string]*ir.Register, len(fc.scopes))}
	for i, scope := range fc.scopes {
		d.scopes[i] = make(map[string]*ir.Register, len(scope))
		for name, reg := range scope {
			d.scopes[i][name] = reg
		}
	}

	body := fc.builder.NewBlock()
	handler := fc.builder.NewBlock()
	fc.builder.Emit(ir.OpTry, nil, handler, body)

	fc.builder.SetBlock(handler)
	fc.runDeferred(d)
	if !fc.unreachable() {
		fc.builder.Emit(ir.OpRethrow, nil)
	}

	fc.builder.SetBlock(body)
	top := len(fc.defers) - 1
	fc.defers[top] = append(fc.defers[top], d)
}

// runDefers emits what the scopes from depth inwards deferred, for a way
// of leaving all of them: the innermost scope first, and in each the last
// deferred first.
func (fc *FunctionCompiler) runDefers(depth int) {
	for i := len(fc.defers) - 1; i >= depth && i >= 0; i-- {
		list := fc.defers[i]
		for j := len(list) - 1; j >= 0; j-- {
			// From here a failure is no longer this defer's to clean up
			// after: it is running, or has run
			fc.builder.Emit(ir.OpEndTry, nil)
			fc.runDeferred(list[j])
		}
	}
}

// hasDefers reports whether leaving the function now has cleanup to run.
func (fc *FunctionCompiler) hasDefers() bool {
	for _, list := range fc.defers {
		if len(list) > 0 {
			return true
		}
	}
	return false
}

// returned gives the register a returned value is kept in while the
// cleanup runs. The value is worked out before the defers run: what they
// assign to a variable afterwards does not change what is returned.
func (fc *FunctionCompiler) returned(value *ir.Register) *ir.Register {
	if value != nil && fc.varRegs[value.ID] && fc.hasDefers() {
		return fc.builder.CreateMov(value)
	}
	return value
}

// runDeferred emits the statements of a defer, seeing the variables that
// were there when it was written.
func (fc *FunctionCompiler) runDeferred(d *deferred) {
	scopes, defers := fc.scopes, fc.defers
	fc.scopes = make([]map[string]*ir.Register, len(d.scopes))
	copy(fc.scopes, d.scopes)
	// Nothing in a defer leaves the scopes around it: they have nothing
	// more to run from inside it
	fc.defers = make([][]*deferred, len(d.scopes))

	fc.compileBlock(d.stmt.Body)

	fc.scopes, fc.defers = scopes, defers
}

// compileBlockExpr compiles statements that end in a value. Without a
// value they always leave, and what is returned is never read.
func (fc *FunctionCompiler) compileBlockExpr(expr *ast.BlockExpr) *ir.Register {
	fc.pushScope()
	defer fc.popScope()

	for _, stmt := range expr.Stmts {
		fc.compileStmt(stmt)
	}
	if expr.Value == nil {
		return fc.builder.CreateMov(ir.Constant{Val: nil, Type: "Null"})
	}
	return fc.compileExpr(expr.Value)
}

// compileCondExpr compiles a choice between two values; only the one
// chosen is evaluated.
//
//	br cond, then, else
//	then: result = <then>; jmp end
//	else: result = <else>; jmp end
//	end:
func (fc *FunctionCompiler) compileCondExpr(expr *ast.CondExpr) *ir.Register {
	cond := fc.compileExpr(expr.Cond)
	fc.setPos(expr)

	// The result register is written on both paths
	result := fc.builder.NewRegister()
	fc.varRegs[result.ID] = true

	thenBlock := fc.builder.NewBlock()
	elseBlock := fc.builder.NewBlock()
	endBlock := fc.builder.NewBlock()
	fc.builder.CreateBr(cond, thenBlock, elseBlock)

	for _, side := range []struct {
		block *ir.BasicBlock
		value ast.Expr
	}{{thenBlock, expr.Then}, {elseBlock, expr.Else}} {
		fc.builder.SetBlock(side.block)
		value := fc.compileExpr(side.value)
		if !fc.unreachable() {
			fc.builder.Emit(ir.OpMov, result, value)
			fc.builder.CreateJmp(endBlock)
		}
	}

	fc.builder.SetBlock(endBlock)
	return result
}
