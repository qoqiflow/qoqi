package desugar

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

func TestErrorUnsupportedIterable(t *testing.T) {
	// Create a for loop over an Int (which is not iterable)
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	intIdent := &ast.Ident{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.IDENT, Literal: "num", Pos: pos, EndPos: token.Position{File: "test.qi", Line: 1, Col: 4}},
		},
		Name: "num",
	}

	forStmt := &ast.ForStmt{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.FOR, Literal: "for", Pos: pos, EndPos: token.Position{File: "test.qi", Line: 1, Col: 4}},
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
				Name:     "test",
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

	// Create source lines with actual source code for better error messages
	sourceLines := map[int]string{
		1: "fn main() {",
		2: "    let count = 42;",
		3: "    // This should fail - can't iterate over Int",
		4: "    for x in count {",
		5: "        print(x);",
		6: "    }",
		7: "}",
	}

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported an error
	if !errs.HasErrors() {
		t.Fatal("Expected error for non-iterable type")
	}

	// Check error details
	if len(errs.Diagnostics) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs.Diagnostics))
	}

	diag := errs.Diagnostics[0]
	if diag.Code != ErrUnsupportedIterable {
		t.Errorf("Expected error code %s, got %s", ErrUnsupportedIterable, diag.Code)
	}

	if !strings.Contains(diag.Message, "Cannot iterate over type") {
		t.Errorf("Expected message about non-iterable type, got: %s", diag.Message)
	}

	if diag.Suggestion == "" {
		t.Error("Expected suggestion for how to fix the error")
	}
}

func TestErrorMissingTypeInfo(t *testing.T) {
	// Create a for loop without type information
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	unknownIdent := &ast.Ident{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.IDENT, Literal: "unknown", Pos: pos, EndPos: token.Position{File: "test.qi", Line: 1, Col: 8}},
		},
		Name: "unknown",
	}

	forStmt := &ast.ForStmt{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.FOR, Literal: "for", Pos: pos, EndPos: token.Position{File: "test.qi", Line: 1, Col: 4}},
		},
		Var:      "x",
		Iterable: unknownIdent,
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
				Name:     "test",
				Params:   []*ast.Param{},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{forStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	// Deliberately not adding type info for unknownIdent

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported an error
	if !errs.HasErrors() {
		t.Fatal("Expected error for missing type info")
	}

	diag := errs.Diagnostics[0]
	if diag.Code != ErrMissingTypeInfo {
		t.Errorf("Expected error code %s, got %s", ErrMissingTypeInfo, diag.Code)
	}
}

func TestErrorInvalidMatchPattern(t *testing.T) {
	// Create a match with an unsupported pattern type
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	xIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "x",
	}

	// Use a struct init as a pattern (which is not supported)
	invalidPattern := &ast.StructInitExpr{
		BaseNode: ast.BaseNode{
			Pos: pos,
			Tok: token.Token{Type: token.IDENT, Literal: "Point", Pos: pos, EndPos: token.Position{File: "test.qi", Line: 1, Col: 6}},
		},
		Name:   "Point",
		Fields: map[string]ast.Expr{},
	}

	matchStmt := &ast.MatchStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     xIdent,
		Cases: []*ast.MatchCase{
			{
				BaseNode: ast.BaseNode{Pos: pos},
				Pattern:  invalidPattern,
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{},
				},
			},
		},
	}

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: pos},
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "test",
				Params:   []*ast.Param{},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{matchStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[xIdent] = types.Int

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported an error
	if !errs.HasErrors() {
		t.Fatal("Expected error for invalid match pattern")
	}

	diag := errs.Diagnostics[0]
	if diag.Code != ErrInvalidMatchPattern {
		t.Errorf("Expected error code %s, got %s", ErrInvalidMatchPattern, diag.Code)
	}

	if !strings.Contains(diag.Message, "Invalid match pattern") {
		t.Errorf("Expected message about invalid pattern, got: %s", diag.Message)
	}
}

func TestWarningDeeplyNestedShortCircuit(t *testing.T) {
	// Create a deeply nested && expression
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	// Build: a() && b() && c() && d() && e() && f()
	var buildNestedAnd func(depth int) ast.Expr
	buildNestedAnd = func(depth int) ast.Expr {
		if depth == 0 {
			return &ast.CallExpr{
				BaseNode: ast.BaseNode{Pos: pos},
				Func: &ast.Ident{
					BaseNode: ast.BaseNode{Pos: pos},
					Name:     "a",
				},
				Args: []ast.Expr{},
			}
		}

		left := buildNestedAnd(depth - 1)
		right := &ast.CallExpr{
			BaseNode: ast.BaseNode{Pos: pos},
			Func: &ast.Ident{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     string(rune('a' + depth)),
			},
			Args: []ast.Expr{},
		}

		return &ast.BinaryExpr{
			BaseNode: ast.BaseNode{
				Pos: pos,
				Tok: token.Token{Type: token.AND, Literal: "&&", Pos: pos, EndPos: pos},
			},
			Left:  left,
			Op:    "&&",
			Right: right,
		}
	}

	deepExpr := buildNestedAnd(6) // Depth of 6 should trigger warning

	ifStmt := &ast.IfStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     deepExpr,
		Then: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts:    []ast.Stmt{},
		},
	}

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: pos},
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "test",
				Params:   []*ast.Param{},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts:    []ast.Stmt{ifStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugarer.Desugar()

	// Should have reported a warning
	hasWarning := false
	for _, diag := range errs.Diagnostics {
		if diag.Severity == errors.SeverityWarning && diag.Code == ErrNestedShortCircuit {
			hasWarning = true
			if !strings.Contains(diag.Message, "Deeply nested") {
				t.Errorf("Expected warning about deep nesting, got: %s", diag.Message)
			}
			if diag.Suggestion == "" {
				t.Error("Expected suggestion for refactoring")
			}
		}
	}

	if !hasWarning {
		t.Error("Expected warning for deeply nested short-circuit operators")
	}
}

func TestValidIterables(t *testing.T) {
	// Test that arrays and strings don't produce errors
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	testCases := []struct {
		name     string
		iterType types.Type
	}{
		{"array", types.NewArray(types.Int)},
		{"string", types.String},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ident := &ast.Ident{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "iter",
			}

			forStmt := &ast.ForStmt{
				BaseNode: ast.BaseNode{Pos: pos},
				Var:      "x",
				Iterable: ident,
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
						Name:     "test",
						Params:   []*ast.Param{},
						Body: &ast.BlockStmt{
							BaseNode: ast.BaseNode{Pos: pos},
							Stmts:    []ast.Stmt{forStmt},
						},
					},
				},
			}

			typeInfo := types.NewInfo()
			typeInfo.Types[ident] = tc.iterType

			resolveTable := resolve.NewTable()
			errs := errors.NewHandler()

			// Create empty source lines map
			sourceLines := make(map[int]string)

			desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
			desugarer.Desugar()

			if errs.HasErrors() {
				t.Errorf("Expected no errors for %s iteration, got: %v", tc.name, errs.Diagnostics)
			}
		})
	}
}
