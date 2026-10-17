package irgen

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/ir"
)

// compileBlock compiles a block of statements in its own scope
func (fc *FunctionCompiler) compileBlock(block *ast.BlockStmt) {
	if block == nil {
		return
	}

	fc.pushScope()
	defer fc.popScope()

	for _, stmt := range block.Stmts {
		fc.compileStmt(stmt)
	}
}

// compileStmt compiles a single statement
func (fc *FunctionCompiler) compileStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	fc.setPos(stmt)

	switch s := stmt.(type) {
	case *ast.LetStmt:
		fc.compileLetStmt(s)
	case *ast.ReturnStmt:
		fc.compileReturnStmt(s)
	case *ast.IfStmt:
		fc.compileIfStmt(s)
	case *ast.WhileStmt:
		fc.compileWhileStmt(s)
	case *ast.ForStmt:
		fc.compileForStmt(s)
	case *ast.BlockStmt:
		fc.compileBlock(s)
	case *ast.ExprStmt:
		fc.compileExpr(s.Expr)
		if s.Stage != "" {
			fc.compileStage(s)
		}
	case *ast.BreakStmt:
		fc.compileBreakStmt(s)
	case *ast.ContinueStmt:
		fc.compileContinueStmt(s)
	case *ast.MatchStmt:
		fc.compileMatchStmt(s)
	case *ast.DeferStmt:
		fc.compileDeferStmt(s)
	default:
		fc.gen.errorAt(stmt, ErrUnsupportedConstruct, "Unsupported statement type in IR generation", "")
	}
}

// compileLetStmt compiles a variable declaration
func (fc *FunctionCompiler) compileLetStmt(stmt *ast.LetStmt) {
	if fn, ok := stmt.Value.(*ast.FnExpr); ok && !fn.Short && !stmt.IsConst {
		// let walk = fn(...) { ... walk(...) ... }: the variable exists
		// before the function is made, which captures it, and holds it
		// once it is
		reg := fc.builder.CreateMov(ir.Constant{Val: nil, Type: "Null"})
		fc.declare(stmt.Name, reg)
		closure := fc.compileExpr(stmt.Value)
		fc.setPos(stmt)
		fc.builder.Emit(ir.OpMov, reg, closure)
		return
	}

	// Compile initializer
	var reg *ir.Register
	if stmt.Value != nil {
		reg = fc.compileExpr(stmt.Value)
	} else {
		// No initializer - load null/zero
		reg = fc.builder.CreateMov(ir.Constant{Val: nil, Type: "Null"})
	}

	// The variable needs a register of its own. A temporary can be adopted;
	// another variable's register must be copied, or the two would alias.
	if fc.varRegs[reg.ID] {
		reg = fc.builder.CreateMov(reg)
	}
	fc.declare(stmt.Name, reg)
}

// compileReturnStmt compiles a return statement
func (fc *FunctionCompiler) compileReturnStmt(stmt *ast.ReturnStmt) {
	var val ir.Value
	if stmt.Value != nil {
		val = fc.returned(fc.compileExpr(stmt.Value))
	}
	fc.runDefers(0)
	fc.builder.CreateRet(val)
	fc.startDeadBlock()
}
