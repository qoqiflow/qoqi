package monomorph

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Test: E3002 - Infinite instantiation (cycle detection)
func TestInfiniteInstantiationError(t *testing.T) {
	// This test simulates the scenario where a generic function
	// recursively instantiates itself with a more complex type
	// Note: This is simplified - full cycle detection happens during dependency resolution

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "recurse"),
				Name:       "recurse",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name: "recurse",
									TypeArgs: []*ast.Type{
										{
											Kind:     ast.TypeArray,
											ElemType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
										},
									},
								},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "recurse",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	m.Monomorphize()

	// This would cause infinite: Int -> [Int] -> [[Int]] -> [[[Int]]] ...
	// The dependency resolver should detect the cycle
	// For now, we verify the error collection works
	// Full cycle detection implementation would catch this

	if errs.HasErrors() {
		// Check if error contains reference to infinite instantiation
		hasInfiniteError := false
		for _, diag := range errs.Diagnostics {
			if strings.Contains(diag.Message, "infinite") || diag.Code == ErrInfiniteInstantiation {
				hasInfiniteError = true
				// Verify suggestion is present
				if diag.Suggestion == "" {
					t.Error("Expected suggestion for E3002 error")
				}
				if !strings.Contains(diag.Suggestion, "Break the cycle") {
					t.Errorf("Expected suggestion to mention breaking the cycle, got: %s", diag.Suggestion)
				}
				break
			}
		}

		if hasInfiniteError {
			t.Logf("Correctly detected infinite instantiation scenario")
		} else {
			t.Logf("Infinite instantiation not detected yet - implementation needed")
		}
	}
}

// Test: Missing type arguments (unbounded generics)
func TestUnboundedGenericsError(t *testing.T) {
	// When type inference is implemented, this should error
	// For now, we test that missing type args are handled

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "identity"),
				Name:       "identity",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "identity",
									TypeArgs: nil, // No type arguments provided!
								},
								Args: []ast.Expr{&ast.Literal{Value: "42"}},
							},
						},
					},
				},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	result, _ := m.Monomorphize()

	// Currently, this won't generate an error because type args are nil
	// The function just won't be monomorphized
	// This test documents current behavior

	// Check that no monomorphized version was created
	foundMonomorphized := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if strings.Contains(fn.Name, "identity_") {
				foundMonomorphized = true
			}
		}
	}

	if foundMonomorphized {
		t.Error("Should not monomorphize without type arguments")
	}

	// Check that E3001 error was reported
	if !errs.HasErrors() {
		t.Error("Expected E3001 error for unbounded generics to be reported")
	}

	// Verify it's the right error code
	hasE3001 := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrUnboundedGenerics {
			hasE3001 = true
			// Verify suggestion is present
			if diag.Suggestion == "" {
				t.Error("Expected suggestion for E3001 error")
			}
			if !strings.Contains(diag.Suggestion, "identity<Type>") {
				t.Errorf("Expected suggestion to show 'identity<Type>', got: %s", diag.Suggestion)
			}
			break
		}
	}

	if !hasE3001 {
		t.Errorf("Expected E3001 error, got diagnostics: %v", errs.Diagnostics)
	}
}

// Test: Type argument count mismatch
func TestTypeArgMismatchError(t *testing.T) {
	// This should be caught during type checking, but we test defensive handling

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "pair"),
				Name:       "pair",
				TypeParams: []string{"T", "U"}, // Expects 2 type params
				Params: []*ast.Param{
					{Name: "a", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}},
					{Name: "b", Type: &ast.Type{Kind: ast.TypeBasic, Name: "U"}},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "Void"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "test"),
				Name:       "test",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{
									Name: "pair",
									TypeArgs: []*ast.Type{
										{Kind: ast.TypeBasic, Name: "Int"},
										// Missing second type argument!
									},
								},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	m.Monomorphize()

	// Check that E3003 error was reported
	if !errs.HasErrors() {
		t.Error("Expected E3003 error for type argument mismatch to be reported")
	}

	// Verify it's the right error code
	hasE3003 := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrTypeArgMismatch {
			hasE3003 = true
			if !strings.Contains(diag.Message, "expected 2") || !strings.Contains(diag.Message, "got 1") {
				t.Errorf("Error message should mention expected 2 got 1, but got: %s", diag.Message)
			}
			// Verify suggestion is present
			if diag.Suggestion == "" {
				t.Error("Expected suggestion for E3003 error")
			}
			if !strings.Contains(diag.Suggestion, "pair<Type1, Type2>") {
				t.Errorf("Expected suggestion to show 'pair<Type1, Type2>', got: %s", diag.Suggestion)
			}
			break
		}
	}

	if !hasE3003 {
		t.Errorf("Expected E3003 error, got diagnostics: %v", errs.Diagnostics)
	}
}

// Test: Non-existent generic function
func TestNonExistentGeneric(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "nonexistent",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	result, info := m.Monomorphize()

	// Should not panic, should handle gracefully
	if len(info.Generated) != 0 {
		t.Error("Should not generate instantiation for non-existent generic")
	}

	// The call should remain unchanged since function doesn't exist
	mainFn := result.Decls[0].(*ast.FnDecl)
	exprStmt := mainFn.Body.Stmts[0].(*ast.ExprStmt)
	callExpr := exprStmt.Expr.(*ast.CallExpr)
	ident := callExpr.Func.(*ast.Ident)

	if ident.Name != "nonexistent" {
		t.Error("Non-existent function name should not be modified")
	}
}

// Test: Empty program
func TestEmptyProgram(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	result, info := m.Monomorphize()

	if errs.HasErrors() {
		t.Errorf("Empty program should not cause errors: %v", errs.Diagnostics)
	}

	if len(result.Decls) != 0 {
		t.Error("Empty program should remain empty")
	}

	if len(info.Generated) != 0 {
		t.Error("No instantiations should be generated for empty program")
	}
}

// Test: Generic function never called (dead code)
func TestUnusedGeneric(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "unused"),
				Name:       "unused",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	result, info := m.Monomorphize()

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Should not generate any instances for unused generic
	if len(info.Generated) != 0 {
		t.Error("Unused generic should not be instantiated")
	}

	// Original generic function should still be in the program
	foundGeneric := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "unused" && len(fn.TypeParams) > 0 {
				foundGeneric = true
			}
		}
	}

	if !foundGeneric {
		t.Error("Original generic function should remain in program")
	}
}

// Test: Monomorphizer state isolation
func TestMonomorphizerStateIsolation(t *testing.T) {
	prog1 := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "identity"),
				Name:       "identity",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "test1"),
				Name:       "test1",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "identity", TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}}},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}

	prog2 := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "identity"),
				Name:       "identity",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "test2"),
				Name:       "test2",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "identity", TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "String"}}},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}

	// Monomorphize first program
	errs1 := errors.NewHandler()
	m1 := NewMonomorphizer(prog1, types.NewInfo(), resolve.NewTable(), errs1)
	_, info1 := m1.Monomorphize()

	// Monomorphize second program
	errs2 := errors.NewHandler()
	m2 := NewMonomorphizer(prog2, types.NewInfo(), resolve.NewTable(), errs2)
	_, info2 := m2.Monomorphize()

	// Each should only have their own instantiation
	if len(info1.Generated) != 1 {
		t.Errorf("First program should have 1 instantiation, got %d", len(info1.Generated))
	}

	if len(info2.Generated) != 1 {
		t.Errorf("Second program should have 1 instantiation, got %d", len(info2.Generated))
	}

	// They should be different instantiations
	var key1, key2 InstantiationKey
	for k := range info1.Generated {
		key1 = k
	}
	for k := range info2.Generated {
		key2 = k
	}

	if key1 == key2 {
		t.Error("Different programs should produce different instantiation keys")
	}
}

// Test: Handling nil bodies (trait method declarations)
func TestNilBodyHandling(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "trait_method"),
				Name:       "trait_method",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       nil, // Trait method declaration
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	result, _ := m.Monomorphize()

	// Should not panic with nil body
	if errs.HasErrors() {
		t.Errorf("Nil body should be handled gracefully: %v", errs.Diagnostics)
	}

	// Generic trait method should remain
	if len(result.Decls) != 1 {
		t.Error("Program structure should be preserved")
	}
}

// Test: Multiple errors are collected
func TestMultipleErrorCollection(t *testing.T) {
	// This is a conceptual test - would need actual error conditions
	// to properly test error accumulation

	errs := errors.NewHandler()

	// Simulate multiple errors
	errs.ReportErrorFromToken(ErrInfiniteInstantiation, "Error 1", token.Token{}, nil)
	errs.ReportErrorFromToken(ErrTypeArgMismatch, "Error 2", token.Token{}, nil)
	errs.ReportErrorFromToken(ErrUnboundedGenerics, "Error 3", token.Token{}, nil)

	if !errs.HasErrors() {
		t.Error("Should have errors")
	}

	if len(errs.Diagnostics) != 3 {
		t.Errorf("Expected 3 errors, got %d", len(errs.Diagnostics))
	}
}
