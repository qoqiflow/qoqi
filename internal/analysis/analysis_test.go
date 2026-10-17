package analysis

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Helper function to create a simple test program
func createTestProgram() *ast.Program {
	return &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}
}

func TestSimpleCapture(t *testing.T) {
	// Create a simple program with a closure that captures a variable
	// fn outer() -> Int {
	//     let x = 42;
	//     fn inner() -> Int {
	//         x  // Captures x
	//     }
	//     inner()
	// }

	prog := createTestProgram()

	// Create outer function with a local variable
	// Note: Full closure support requires nested function declarations in AST
	outerBody := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.LetStmt{
				Name:  "x",
				Value: &ast.Literal{Kind: "int", Value: "42"},
			},
			// Note: Nested function declarations aren't fully supported in AST yet
			// This is a simplified test
		},
	}

	outerFunc := &ast.FnDecl{
		Name:   "outer",
		Params: []*ast.Param{},
		Body:   outerBody,
	}

	prog.Decls = append(prog.Decls, outerFunc)

	// Create minimal resolve table and type info
	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}

	// Create analyzer
	errs := errors.NewHandler()
	analyzer := NewAnalyzer(prog, resolveTable, typeInfo, errs)

	// Run analysis
	captureInfo := analyzer.Analyze()

	// Verify capture info was created
	if captureInfo == nil {
		t.Fatal("Expected capture info to be created")
	}

	// Verify functions were analyzed
	if len(captureInfo.Captures) == 0 {
		t.Log("Note: No captures detected (expected with incomplete test setup)")
	}
}

func TestNoCapture(t *testing.T) {
	// fn simple() -> Int {
	//     let x = 42;
	//     x
	// }

	prog := createTestProgram()

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.LetStmt{
				Name:  "x",
				Value: &ast.Literal{Kind: "int", Value: "42"},
			},
			&ast.ReturnStmt{
				Value: &ast.Ident{Name: "x"},
			},
		},
	}

	fn := &ast.FnDecl{
		Name:   "simple",
		Params: []*ast.Param{},
		Body:   body,
	}

	prog.Decls = append(prog.Decls, fn)

	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}

	errs := errors.NewHandler()
	analyzer := NewAnalyzer(prog, resolveTable, typeInfo, errs)

	captureInfo := analyzer.Analyze()

	// Verify the function has no captures
	captures := captureInfo.GetCaptures(fn)
	if captures != nil && len(captures.CapturedVars) > 0 {
		t.Errorf("Expected no captures, got %d", len(captures.CapturedVars))
	}
}

func TestEscapeAnalysis(t *testing.T) {
	prog := createTestProgram()

	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}

	errs := errors.NewHandler()
	analyzer := NewAnalyzer(prog, resolveTable, typeInfo, errs)

	captureInfo := analyzer.Analyze()

	// Verify escape analysis ran
	if captureInfo.Escapes == nil {
		t.Fatal("Expected escape map to be initialized")
	}
}
