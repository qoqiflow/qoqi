package compiler

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// A variable declared outside any function, in a module or next to a
// main, is a place every function of the file sees:
//
//	let visits = 0;
//	fn visit() { visits += 1; }
//
// Its place is an array of one item, made the first time the variable is
// used by a function of its own, and the same array from then on (the
// ONCE instruction, for a variable): reading it is visits[0], changing it
// changes that item.
//
// What makes it is moved into that function here, once the program is
// checked: the stages below compile it as any function, generic calls
// and all. The variable keeps its declaration, with its type written out
// and a value that reads the place, so that names and types are found as
// before; desugaring writes every use of it as the read of its place.

// lowerGlobals gives each variable outside functions the function that
// makes its place, and has main make them all before anything else, in
// the order they are written: what makes one is never run later than
// the program starts, whatever the program does first. One that uses
// another makes that one first, which ONCE then does not make again.
func lowerGlobals(prog *ast.Program, info *types.Info) {
	var main *ast.FnDecl
	for _, decl := range prog.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == "main" && fn.Body != nil {
			main = fn
		}
	}
	var made []ast.Stmt
	for _, global := range prog.Globals {
		at := global.BaseNode
		valueType := info.Types[global.Value]
		if global.Type != nil {
			valueType = nil // Declared: written as it was
		}
		declared := global.Type
		if declared == nil {
			declared = types.TypeToAST(valueType, at)
			if declared == nil {
				continue // Reported by the checker
			}
		}
		maker := &ast.FnDecl{
			BaseNode:   at,
			Name:       resolve.VariablePrefix + global.Name,
			ReturnType: &ast.Type{BaseNode: at, Kind: ast.TypeArray, ElemType: declared},
			Body: &ast.BlockStmt{BaseNode: at, Stmts: []ast.Stmt{
				&ast.ReturnStmt{BaseNode: at, Value: &ast.ArrayLiteralExpr{BaseNode: at, Elements: []ast.Expr{global.Value}}},
			}},
		}
		prog.Decls = append(prog.Decls, maker)
		global.Type = declared
		global.Value = PlaceOf(global.Name, at)
		made = append(made, &ast.ExprStmt{BaseNode: at, Expr: &ast.CallExpr{BaseNode: at, Func: &ast.Ident{BaseNode: at, Name: maker.Name}}})
	}
	if main != nil && len(made) > 0 {
		main.Body.Stmts = append(made, main.Body.Stmts...)
	}
}

// PlaceOf reads the place of a variable outside functions: name[0] of the
// array its function makes.
func PlaceOf(name string, at ast.BaseNode) ast.Expr {
	call := &ast.CallExpr{BaseNode: at, Func: &ast.Ident{BaseNode: at, Name: resolve.VariablePrefix + name}}
	zero := &ast.Literal{BaseNode: at, Kind: "int", Value: "0"}
	return &ast.IndexExpr{BaseNode: at, Expr: call, Index: zero}
}
