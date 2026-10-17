package desugar

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// TestErrorReportingWithSourceLines demonstrates rich error reporting with actual source code context
func TestErrorReportingWithSourceLines(t *testing.T) {
	// Create a for loop over an Int at line 5, column 14
	pos := token.Position{File: "example.qi", Line: 5, Col: 14}

	intIdent := &ast.Ident{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{
				Type:    token.IDENT,
				Literal: "count",
				Pos:     pos,
				EndPos:  token.Position{File: "example.qi", Line: 5, Col: 19},
			},
		},
		Name: "count",
	}

	forStmt := &ast.ForStmt{
		BaseNode: ast.BaseNode{
			Pos: token.Position{File: "example.qi", Line: 5, Col: 5},
			Tok: token.Token{
				Type:    token.FOR,
				Literal: "for",
				Pos:     token.Position{File: "example.qi", Line: 5, Col: 5},
				EndPos:  token.Position{File: "example.qi", Line: 5, Col: 8},
			},
		},
		Var:      "x",
		Iterable: intIdent,
		Body: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts:    []ast.Stmt{},
		},
	}

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: pos},
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "main",
				Params:   []*ast.Param{},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{forStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[intIdent] = types.Int

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create source lines with the actual source code
	sourceLines := map[int]string{
		1: "fn main() {",
		2: "    let count = 42;",
		3: "",
		4: "    // Try to iterate over an integer (this should fail)",
		5: "    for x in count {",
		6: "        print(x);",
		7: "    }",
		8: "}",
	}

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported an error
	if !errs.HasErrors() {
		t.Fatal("Expected error for non-iterable type")
	}

	// Print the rich error output for demonstration
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println(strings.Repeat("=", 70))
	for _, diag := range errs.Diagnostics {
		fmt.Println(diag.String())
	}
	fmt.Println(strings.Repeat("=", 70) + "\n")

	// Verify error details
	diag := errs.Diagnostics[0]
	if diag.Code != ErrUnsupportedIterable {
		t.Errorf("Expected error code %s, got %s", ErrUnsupportedIterable, diag.Code)
	}

	if diag.Context == "" {
		t.Error("Expected source context to be populated")
	}

	// Verify the context shows the actual source line
	if diag.Context != "    for x in count {" {
		t.Errorf("Expected context to show the for loop line, got: %q", diag.Context)
	}

	// Verify highlights are present
	if len(diag.Highlights) == 0 {
		t.Error("Expected highlights to be present")
	} else {
		// Should highlight "count" at columns 14-19
		if diag.Highlights[0].StartCol != 14 || diag.Highlights[0].EndCol != 19 {
			t.Errorf("Expected highlight at columns 14-19, got %d-%d",
				diag.Highlights[0].StartCol, diag.Highlights[0].EndCol)
		}
	}
}

// TestMultipleErrorsReporting demonstrates reporting multiple errors at once
func TestMultipleErrorsReporting(t *testing.T) {
	pos := token.Position{File: "errors.qi", Line: 1, Col: 1}

	// Create two invalid for loops
	forStmt1 := &ast.ForStmt{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "errors.qi", Line: 2, Col: 5}},
		Var:      "x",
		Iterable: &ast.Ident{
			BaseNode: ast.BaseNode{
				Pos: token.Position{File: "errors.qi", Line: 2, Col: 14},
				Tok: token.Token{
					Type:    token.IDENT,
					Literal: "num1",
					Pos:     token.Position{File: "errors.qi", Line: 2, Col: 14},
					EndPos:  token.Position{File: "errors.qi", Line: 2, Col: 18},
				},
			},
			Name: "num1",
		},
		Body: &ast.BlockStmt{BaseNode: ast.BaseNode{Pos: pos}, Stmts: []ast.Stmt{}},
	}

	forStmt2 := &ast.ForStmt{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "errors.qi", Line: 5, Col: 5}},
		Var:      "y",
		Iterable: &ast.Ident{
			BaseNode: ast.BaseNode{
				Pos: token.Position{File: "errors.qi", Line: 5, Col: 14},
				Tok: token.Token{
					Type:    token.IDENT,
					Literal: "num2",
					Pos:     token.Position{File: "errors.qi", Line: 5, Col: 14},
					EndPos:  token.Position{File: "errors.qi", Line: 5, Col: 18},
				},
			},
			Name: "num2",
		},
		Body: &ast.BlockStmt{BaseNode: ast.BaseNode{Pos: pos}, Stmts: []ast.Stmt{}},
	}

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: pos},
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "test",
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{forStmt1, forStmt2},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[forStmt1.Iterable] = types.Int
	typeInfo.Types[forStmt2.Iterable] = types.Float

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	sourceLines := map[int]string{
		1: "fn test() {",
		2: "    for x in num1 {  // Error: Int is not iterable",
		3: "        // ...",
		4: "    }",
		5: "    for y in num2 {  // Error: Float is not iterable",
		6: "        // ...",
		7: "    }",
		8: "}",
	}

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported TWO errors
	if len(errs.Diagnostics) != 2 {
		t.Fatalf("Expected 2 errors, got %d", len(errs.Diagnostics))
	}

	// Print both errors
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("DEMONSTRATION: Multiple Errors Reported Together")
	fmt.Println(strings.Repeat("=", 70))
	for i, diag := range errs.Diagnostics {
		fmt.Printf("\nError #%d:\n%s\n", i+1, diag.String())
	}
	fmt.Println(strings.Repeat("=", 70) + "\n")

	// Verify both errors are for different types
	if errs.Diagnostics[0].Message == errs.Diagnostics[1].Message {
		t.Error("Expected different error messages for Int and Float")
	}
}
