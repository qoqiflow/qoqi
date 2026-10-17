package desugar

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// DesugarInfo holds metadata about the desugaring process
type DesugarInfo struct {
	// Maps original AST nodes to their desugared equivalents
	Mapping map[ast.Node]ast.Node

	// Maps generated temporary variables to their purposes (for debugging)
	TmpVars map[string]string
}

// Desugarer transforms complex syntactic constructs into simpler, more primitive forms
type Desugarer struct {
	prog         *ast.Program
	typeInfo     *types.Info
	resolveTable *resolve.Table
	errs         *errors.Handler

	// Counter for generating unique temporary variable names
	tmpCounter int

	// Info collected during desugaring
	info *DesugarInfo

	// Source lines for error reporting (map of line number to line content)
	sourceLines map[int]string

	// The functions that make the constants that are made once (see
	// madeOnce), by the constant, in the order they were first needed
	constants     map[*ast.ConstDecl]*ast.FnDecl
	constantOrder []*ast.FnDecl

	// The structs of the program by name, found when first asked for
	structs map[string]*ast.StructDecl
	// The value of each field that has one of its own, desugared
	defaults map[*ast.Field]ast.Expr
}

// NewDesugarer creates a new desugarer instance
func NewDesugarer(
	prog *ast.Program,
	typeInfo *types.Info,
	resolveTable *resolve.Table,
	errs *errors.Handler,
	sourceLines map[int]string,
) *Desugarer {
	return &Desugarer{
		prog:         prog,
		typeInfo:     typeInfo,
		resolveTable: resolveTable,
		errs:         errs,
		tmpCounter:   0,
		sourceLines:  sourceLines,
		info: &DesugarInfo{
			Mapping: make(map[ast.Node]ast.Node),
			TmpVars: make(map[string]string),
		},
	}
}

// Desugar performs the complete desugaring transformation on the program
func (d *Desugarer) Desugar() (*ast.Program, *DesugarInfo) {
	// Create a new program with desugared declarations
	newDecls := make([]ast.Decl, 0, len(d.prog.Decls))

	for _, decl := range d.prog.Decls {
		// A constant is replaced by its value wherever it is used; nothing
		// of it is left to compile
		if _, isConst := decl.(*ast.ConstDecl); isConst {
			continue
		}
		newDecl := d.desugarDecl(decl)
		newDecls = append(newDecls, newDecl)
	}

	// The functions of the constants that are made once. Making one may
	// need another, so the list can grow while it is gone through
	for i := 0; i < len(d.constantOrder); i++ {
		newDecls = append(newDecls, d.constantOrder[i])
	}

	newProg := &ast.Program{
		BaseNode: d.prog.BaseNode,
		Imports:  d.prog.Imports,
		Decls:    newDecls,
		Externs:  d.prog.Externs,
	}

	return newProg, d.info
}

// isGlobal reports whether a declaration is that of a variable outside
// functions.
func (d *Desugarer) isGlobal(decl ast.Node) bool {
	let, ok := decl.(*ast.LetStmt)
	if !ok {
		return false
	}
	for _, global := range d.prog.Globals {
		if global == let {
			return true
		}
	}
	return false
}

// desugarDecl desugars a declaration
func (d *Desugarer) desugarDecl(decl ast.Decl) ast.Decl {
	switch decl := decl.(type) {
	case *ast.FnDecl:
		return d.desugarFnDecl(decl)
	case *ast.StructDecl:
		return decl // Structs don't need desugaring
	case *ast.EnumDecl:
		return decl // Enums don't need desugaring
	case *ast.TraitDecl:
		return d.desugarTraitDecl(decl)
	case *ast.ImplDecl:
		return d.desugarImplDecl(decl)
	default:
		return decl
	}
}

// desugarFnDecl desugars a function declaration: everything it was
// declared with (its type parameters and what they require, whether it is
// public or static, its module) and the new body.
func (d *Desugarer) desugarFnDecl(fn *ast.FnDecl) *ast.FnDecl {
	if fn.Body == nil {
		return fn
	}

	out := *fn
	out.Body = d.desugarStmt(fn.Body).(*ast.BlockStmt)
	return &out
}

// desugarTraitDecl desugars a trait declaration
func (d *Desugarer) desugarTraitDecl(trait *ast.TraitDecl) *ast.TraitDecl {
	out := *trait
	out.Methods = make([]*ast.FnDecl, len(trait.Methods))
	for i, method := range trait.Methods {
		out.Methods[i] = d.desugarFnDecl(method)
	}
	return &out
}

// desugarImplDecl desugars an impl declaration. Its type parameters stay:
// the methods of impl<T, E> Result<T, E> are checked again after this,
// and a T that is no longer declared would leave their types unknown.
func (d *Desugarer) desugarImplDecl(impl *ast.ImplDecl) *ast.ImplDecl {
	out := *impl
	out.Methods = make([]*ast.FnDecl, len(impl.Methods))
	for i, method := range impl.Methods {
		out.Methods[i] = d.desugarFnDecl(method)
	}
	return &out
}

// desugarStmt desugars a statement
func (d *Desugarer) desugarStmt(stmt ast.Stmt) ast.Stmt {
	switch stmt := stmt.(type) {
	case *ast.BlockStmt:
		return d.desugarBlockStmt(stmt)
	case *ast.LetStmt:
		return d.desugarLetStmt(stmt)
	case *ast.ReturnStmt:
		return d.desugarReturnStmt(stmt)
	case *ast.IfStmt:
		return d.desugarIfStmt(stmt)
	case *ast.WhileStmt:
		return d.desugarWhileStmt(stmt)
	case *ast.ForStmt:
		return d.desugarForStmt(stmt)
	case *ast.MatchStmt:
		return d.desugarMatchStmt(stmt)
	case *ast.ExprStmt:
		return d.desugarExprStmt(stmt)
	case *ast.DeferStmt:
		return &ast.DeferStmt{BaseNode: stmt.BaseNode, Body: d.desugarBlockStmt(stmt.Body)}
	case *ast.BreakStmt, *ast.ContinueStmt:
		return stmt // No desugaring needed
	default:
		return stmt
	}
}

// desugarBlockStmt desugars a block statement
func (d *Desugarer) desugarBlockStmt(block *ast.BlockStmt) *ast.BlockStmt {
	newStmts := make([]ast.Stmt, 0, len(block.Stmts))

	for i, stmt := range block.Stmts {
		if guard, ok := stmt.(*ast.IfStmt); ok {
			if test := ast.GuardTest(guard); test != nil {
				// if !(value is Ok(n)) { return; } rest: the rest runs
				// when the test held, with its names, the block otherwise
				rest := &ast.BlockStmt{BaseNode: guard.BaseNode, Stmts: block.Stmts[i+1:]}
				turned := &ast.IfStmt{BaseNode: guard.BaseNode, Cond: test, Then: rest, Else: guard.Then}
				newStmts = append(newStmts, d.desugarStmt(turned))
				break
			}
		}
		newStmt := d.desugarStmt(stmt)
		newStmts = append(newStmts, newStmt)
	}

	return &ast.BlockStmt{
		BaseNode: block.BaseNode,
		Stmts:    newStmts,
	}
}

// desugarLetStmt desugars a let statement
func (d *Desugarer) desugarLetStmt(let *ast.LetStmt) ast.Stmt {
	if let.Value == nil {
		return let
	}
	d.checkShortCircuitComplexity(let.Value)

	return &ast.LetStmt{
		BaseNode: let.BaseNode,
		Name:     let.Name,
		Type:     let.Type,
		Value:    d.desugarExpr(let.Value),
		IsConst:  let.IsConst,
	}
}

// desugarReturnStmt desugars a return statement
func (d *Desugarer) desugarReturnStmt(ret *ast.ReturnStmt) *ast.ReturnStmt {
	if ret.Value == nil {
		return ret
	}

	newValue := d.desugarExpr(ret.Value)

	return &ast.ReturnStmt{
		BaseNode: ret.BaseNode,
		Value:    newValue,
	}
}

// desugarIfStmt desugars an if statement
func (d *Desugarer) desugarIfStmt(ifStmt *ast.IfStmt) ast.Stmt {
	d.checkShortCircuitComplexity(ifStmt.Cond)
	if hasPatternTest(ifStmt.Cond) {
		return d.desugarIfPattern(ifStmt)
	}
	newCond := d.desugarExpr(ifStmt.Cond)
	newThen := d.desugarStmt(ifStmt.Then).(*ast.BlockStmt)

	var newElse ast.Stmt
	if ifStmt.Else != nil {
		newElse = d.desugarStmt(ifStmt.Else)
	}

	return &ast.IfStmt{
		BaseNode: ifStmt.BaseNode,
		Cond:     newCond,
		Then:     newThen,
		Else:     newElse,
	}
}

// desugarWhileStmt desugars a while statement
func (d *Desugarer) desugarWhileStmt(while *ast.WhileStmt) ast.Stmt {
	d.checkShortCircuitComplexity(while.Cond)
	if hasPatternTest(while.Cond) {
		return d.desugarWhilePattern(while)
	}

	return &ast.WhileStmt{
		BaseNode: while.BaseNode,
		Cond:     d.desugarExpr(while.Cond),
		Body:     d.desugarStmt(while.Body).(*ast.BlockStmt),
	}
}

// desugarExprStmt desugars an expression statement
func (d *Desugarer) desugarExprStmt(expr *ast.ExprStmt) *ast.ExprStmt {
	if expr.Stage != "" {
		// The same statement, which the shape of the stage is known by
		return expr
	}
	newExpr := d.desugarExpr(expr.Expr)

	return &ast.ExprStmt{
		BaseNode: expr.BaseNode,
		Expr:     newExpr,
		Stage:    expr.Stage,
	}
}

// desugarExpr desugars an expression
func (d *Desugarer) desugarExpr(expr ast.Expr) ast.Expr {
	lowered := d.desugarExprAsWritten(expr)
	if node, ok := expr.(ast.Widenable); ok && node.IsWidened() {
		return d.asFloat(lowered)
	}
	return lowered
}

// asFloat makes a Float of a whole number written where one is expected:
// let half: Float = 1; A literal becomes the literal of the Float. Anything
// else is multiplied by 1.0, which the machine works out as a Float.
func (d *Desugarer) asFloat(expr ast.Expr) ast.Expr {
	floatOf := func(lit *ast.Literal) *ast.Literal {
		tok := lit.Tok
		tok.Type, tok.Literal = token.FLOAT, lit.Value+".0"
		return &ast.Literal{BaseNode: ast.BaseNode{Pos: lit.Pos, Tok: tok}, Kind: "float", Value: lit.Value + ".0"}
	}
	switch e := expr.(type) {
	case *ast.Literal:
		if e.Kind == "int" {
			return floatOf(e)
		}
	case *ast.UnaryExpr:
		if lit, ok := e.Expr.(*ast.Literal); ok && e.Op == "-" && lit.Kind == "int" {
			return &ast.UnaryExpr{BaseNode: e.BaseNode, Op: "-", Expr: floatOf(lit)}
		}
	}
	pos := expr.Position()
	one := &ast.Literal{BaseNode: ast.BaseNode{Pos: pos, Tok: token.Token{Type: token.FLOAT, Literal: "1.0", Pos: pos}}, Kind: "float", Value: "1.0"}
	return &ast.BinaryExpr{BaseNode: ast.BaseNode{Pos: pos, Tok: expr.Token()}, Left: expr, Op: "*", Right: one}
}

// desugarExprAsWritten desugars an expression, before a whole number is
// made the Float it is used as.
func (d *Desugarer) desugarExprAsWritten(expr ast.Expr) ast.Expr {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		if expr.Op == "&&" && hasPatternTest(expr) {
			return d.desugarPatternValue(expr)
		}
		return d.desugarBinaryExpr(expr)
	case *ast.PatternTestExpr:
		return d.desugarPatternValue(expr)
	case *ast.UnaryExpr:
		return d.desugarUnaryExpr(expr)
	case *ast.CallExpr:
		if decl, variant := d.variantOf(expr.Func); decl != nil {
			return d.lowerVariantCall(expr, decl, variant)
		}
		if member, ok := expr.Func.(*ast.MemberExpr); ok && len(expr.Args) > 0 {
			if _, typeMethod := d.typeInfo.TypeMethods[member]; typeMethod {
				// Point.norm(p) is p.norm()
				var rest []ast.Expr
				for _, arg := range expr.Args[1:] {
					rest = append(rest, d.desugarExpr(arg))
				}
				return d.call(d.desugarExpr(expr.Args[0]), member.Name, expr.BaseNode, rest...)
			}
		}
		return d.desugarCallExpr(expr)
	case *ast.MemberExpr:
		if decl, variant := d.variantOf(expr); decl != nil {
			return d.lowerVariantRef(expr, decl, variant)
		}
		if signature, ok := d.typeInfo.MethodValues[expr]; ok {
			return d.methodValue(expr, signature)
		}
		if signature, ok := d.typeInfo.TypeMethods[expr]; ok {
			return d.typeMethod(expr, signature)
		}
		return d.desugarMemberExpr(expr)
	case *ast.IndexExpr:
		if d.typeInfo.IndexReads[expr] {
			// What a struct says its brackets give
			return d.call(d.desugarExpr(expr.Expr), types.MethodAt, expr.BaseNode, d.desugarExpr(expr.Index))
		}
		return d.desugarIndexExpr(expr)
	case *ast.StructInitExpr:
		return d.desugarStructInitExpr(expr)
	case *ast.ArrayLiteralExpr:
		return d.desugarArrayLiteralExpr(expr)
	case *ast.FnExpr:
		return d.desugarFnExpr(expr)
	case *ast.CastExpr:
		return &ast.CastExpr{BaseNode: expr.BaseNode, Expr: d.desugarExpr(expr.Expr), Type: expr.Type}
	case *ast.TypeTestExpr:
		return &ast.TypeTestExpr{BaseNode: expr.BaseNode, Expr: d.desugarExpr(expr.Expr), Type: expr.Type}
	case *ast.Ident:
		if sym := d.resolveTable.Defs[expr]; sym != nil && d.isGlobal(sym.Decl) {
			// A variable outside functions: the item of its place
			call := &ast.CallExpr{BaseNode: expr.BaseNode, Func: d.createIdent(resolve.VariablePrefix+expr.Name, expr.Position())}
			return &ast.IndexExpr{BaseNode: expr.BaseNode, Expr: call, Index: d.createIntLiteral("0", expr.Position())}
		}
		if decl := d.constOf(expr); decl != nil {
			if once := d.madeOnce(decl, expr.BaseNode); once != nil {
				return once
			}
			return d.inlineConstant(decl.Value, expr.BaseNode, 0)
		}
		return expr
	case *ast.VariantExpr:
		// Already lowered; its arguments may still need it
		args := make([]ast.Expr, len(expr.Args))
		for i, arg := range expr.Args {
			args[i] = d.desugarExpr(arg)
		}
		return &ast.VariantExpr{BaseNode: expr.BaseNode, Enum: expr.Enum, Variant: expr.Variant, TypeArgs: expr.TypeArgs, Args: args}
	case *ast.VariantTestExpr:
		return &ast.VariantTestExpr{BaseNode: expr.BaseNode, Expr: d.desugarExpr(expr.Expr), Enum: expr.Enum, Variant: expr.Variant}
	case *ast.VariantFieldExpr:
		return &ast.VariantFieldExpr{BaseNode: expr.BaseNode, Expr: d.desugarExpr(expr.Expr), Enum: expr.Enum, Variant: expr.Variant, Index: expr.Index}
	case *ast.MatchExpr:
		return d.desugarMatchExpr(expr)
	case *ast.CondExpr:
		// if ... { a } else { b } where a value is expected
		if hasPatternTest(expr.Cond) {
			// if shape is Shape.Circle(r) { ... }: the names of the test
			// are variables of the branch it guards
			cond := d.lowerCondition(expr.Cond)
			then := &ast.BlockExpr{BaseNode: expr.BaseNode, Stmts: cond.names, Value: d.desugarExpr(expr.Then)}
			chosen := &ast.CondExpr{BaseNode: expr.BaseNode, Cond: cond.test, Then: then, Else: d.desugarExpr(expr.Else)}
			if len(cond.before) == 0 {
				return chosen
			}
			return &ast.BlockExpr{BaseNode: expr.BaseNode, Stmts: cond.before, Value: chosen}
		}
		return &ast.CondExpr{BaseNode: expr.BaseNode, Cond: d.desugarExpr(expr.Cond), Then: d.desugarExpr(expr.Then), Else: d.desugarExpr(expr.Else)}
	case *ast.BlockExpr:
		block := &ast.BlockExpr{BaseNode: expr.BaseNode}
		for _, stmt := range expr.Stmts {
			block.Stmts = append(block.Stmts, d.desugarStmt(stmt))
		}
		if expr.Value != nil {
			block.Value = d.desugarExpr(expr.Value)
		}
		return block
	case *ast.Literal:
		return expr // No desugaring needed
	default:
		return expr
	}
}

// desugarFnExpr desugars the body of an anonymous function
func (d *Desugarer) desugarFnExpr(fn *ast.FnExpr) ast.Expr {
	if fn.Body == nil {
		return fn
	}

	return &ast.FnExpr{
		BaseNode:   fn.BaseNode,
		Params:     fn.Params,
		ReturnType: fn.ReturnType,
		Body:       d.desugarBlockStmt(fn.Body),
	}
}

// desugarUnaryExpr desugars a unary expression
func (d *Desugarer) desugarUnaryExpr(unary *ast.UnaryExpr) ast.Expr {
	newExpr := d.desugarExpr(unary.Expr)

	return &ast.UnaryExpr{
		BaseNode: unary.BaseNode,
		Op:       unary.Op,
		Expr:     newExpr,
	}
}

// desugarCallExpr desugars a call expression
func (d *Desugarer) desugarCallExpr(call *ast.CallExpr) ast.Expr {
	newFunc := d.desugarExpr(call.Func)
	newArgs := make([]ast.Expr, len(call.Args))

	for i, arg := range call.Args {
		newArgs[i] = d.desugarExpr(arg)
	}
	d.shown(call, newArgs)

	return &ast.CallExpr{
		BaseNode: call.BaseNode,
		Func:     newFunc,
		Args:     newArgs,
	}
}

// desugarMemberExpr desugars a member expression
func (d *Desugarer) desugarMemberExpr(member *ast.MemberExpr) ast.Expr {
	newExpr := d.desugarExpr(member.Expr)

	return &ast.MemberExpr{
		BaseNode: member.BaseNode,
		Expr:     newExpr,
		Name:     member.Name,
		TypeArgs: member.TypeArgs,
	}
}

// desugarIndexExpr desugars an index expression
func (d *Desugarer) desugarIndexExpr(index *ast.IndexExpr) ast.Expr {
	newExpr := d.desugarExpr(index.Expr)
	newIndex := d.desugarExpr(index.Index)

	return &ast.IndexExpr{
		BaseNode: index.BaseNode,
		Expr:     newExpr,
		Index:    newIndex,
	}
}

// desugarStructInitExpr desugars a struct initialization expression
func (d *Desugarer) desugarStructInitExpr(structInit *ast.StructInitExpr) ast.Expr {
	newFields := make(map[string]ast.Expr)

	for name, expr := range structInit.Fields {
		newFields[name] = d.desugarExpr(expr)
	}
	d.withDefaults(structInit.Name, newFields)

	return &ast.StructInitExpr{
		BaseNode: structInit.BaseNode,
		Name:     structInit.Name,
		TypeArgs: structInit.TypeArgs,
		Fields:   newFields,
	}
}

// desugarArrayLiteralExpr desugars an array literal expression
func (d *Desugarer) desugarArrayLiteralExpr(array *ast.ArrayLiteralExpr) ast.Expr {
	newElements := make([]ast.Expr, len(array.Elements))

	for i, elem := range array.Elements {
		newElements[i] = d.desugarExpr(elem)
	}

	return &ast.ArrayLiteralExpr{
		BaseNode: array.BaseNode,
		Elements: newElements,
	}
}

// generateTmpVar generates a unique temporary variable name
func (d *Desugarer) generateTmpVar() string {
	name := fmt.Sprintf("__tmp_%d", d.tmpCounter)
	d.tmpCounter++
	d.info.TmpVars[name] = "temporary"
	return name
}

// generateIterVar generates a unique iterator variable name
func (d *Desugarer) generateIterVar() string {
	name := fmt.Sprintf("__iter_%d", d.tmpCounter)
	d.tmpCounter++
	d.info.TmpVars[name] = "iterator"
	return name
}

// generateLenVar generates a unique length variable name
func (d *Desugarer) generateLenVar() string {
	name := fmt.Sprintf("__len_%d", d.tmpCounter)
	d.tmpCounter++
	d.info.TmpVars[name] = "length"
	return name
}

// generateMatchVar generates a unique match temporary variable name
func (d *Desugarer) generateMatchVar() string {
	name := fmt.Sprintf("__match_tmp_%d", d.tmpCounter)
	d.tmpCounter++
	d.info.TmpVars[name] = "match"
	return name
}

// createIntLiteral creates an integer literal with the given value
func (d *Desugarer) createIntLiteral(value string, pos token.Position) *ast.Literal {
	return &ast.Literal{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.INT, Literal: value, Pos: pos},
		},
		Value: value,
		Kind:  "int",
	}
}

// createBoolLiteral creates a boolean literal with the given value
func (d *Desugarer) createBoolLiteral(value bool, pos token.Position) *ast.Literal {
	valueStr := "false"
	if value {
		valueStr = "true"
	}
	return &ast.Literal{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.TRUE, Literal: valueStr, Pos: pos},
		},
		Value: valueStr,
		Kind:  "bool",
	}
}

// createIdent creates an identifier with the given name
func (d *Desugarer) createIdent(name string, pos token.Position) *ast.Ident {
	return &ast.Ident{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.IDENT, Literal: name, Pos: pos},
		},
		Name: name,
	}
}

// createBinaryExpr creates a binary expression
func (d *Desugarer) createBinaryExpr(left ast.Expr, op string, right ast.Expr, pos token.Position) *ast.BinaryExpr {
	return &ast.BinaryExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Left:     left,
		Op:       op,
		Right:    right,
	}
}

// createMemberExpr creates a member access expression
func (d *Desugarer) createMemberExpr(expr ast.Expr, name string, pos token.Position) *ast.MemberExpr {
	return &ast.MemberExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Expr:     expr,
		Name:     name,
	}
}

// createIndexExpr creates an index expression
func (d *Desugarer) createIndexExpr(expr ast.Expr, index ast.Expr, pos token.Position) *ast.IndexExpr {
	return &ast.IndexExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Expr:     expr,
		Index:    index,
	}
}

// wrapInBlock wraps statements in a block statement
func (d *Desugarer) wrapInBlock(stmts []ast.Stmt, pos token.Position) *ast.BlockStmt {
	return &ast.BlockStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Stmts:    stmts,
	}
}
