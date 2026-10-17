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

// Test that demonstrates all three error suggestions
func TestErrorSuggestions(t *testing.T) {
	t.Run("E3001_UnboundedGenerics_Suggestion", func(t *testing.T) {
		prog := &ast.Program{
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
					BaseNode: baseNode(token.IDENT, "main"),
					Name:     "main",
					Body: &ast.BlockStmt{
						Stmts: []ast.Stmt{
							&ast.ExprStmt{
								Expr: &ast.CallExpr{
									Func: &ast.Ident{Name: "identity"}, // Missing type args!
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
		m.Monomorphize()

		if !errs.HasErrors() {
			t.Fatal("Expected error")
		}

		// Find the E3001 error
		for _, diag := range errs.Diagnostics {
			if diag.Code == ErrUnboundedGenerics {
				t.Logf("✓ Error Message: %s", diag.Message)
				t.Logf("✓ Suggestion: %s", diag.Suggestion)

				if !strings.Contains(diag.Suggestion, "identity<Type>") {
					t.Errorf("Expected suggestion to include 'identity<Type>', got: %s", diag.Suggestion)
				}
				return
			}
		}
		t.Error("E3001 error not found")
	})

	t.Run("E3003_TypeArgMismatch_Suggestion", func(t *testing.T) {
		prog := &ast.Program{
			Decls: []ast.Decl{
				&ast.FnDecl{
					BaseNode:   baseNode(token.IDENT, "pair"),
					Name:       "pair",
					TypeParams: []string{"T", "U"},
					Params: []*ast.Param{
						{Name: "a", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}},
						{Name: "b", Type: &ast.Type{Kind: ast.TypeBasic, Name: "U"}},
					},
					ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "Void"},
					Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
				},
				&ast.FnDecl{
					BaseNode: baseNode(token.IDENT, "main"),
					Name:     "main",
					Body: &ast.BlockStmt{
						Stmts: []ast.Stmt{
							&ast.ExprStmt{
								Expr: &ast.CallExpr{
									Func: &ast.Ident{
										Name:     "pair",
										TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}}, // Only 1 type arg, needs 2!
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

		if !errs.HasErrors() {
			t.Fatal("Expected error")
		}

		// Find the E3003 error
		for _, diag := range errs.Diagnostics {
			if diag.Code == ErrTypeArgMismatch {
				t.Logf("✓ Error Message: %s", diag.Message)
				t.Logf("✓ Suggestion: %s", diag.Suggestion)

				if !strings.Contains(diag.Suggestion, "pair<Type1, Type2>") {
					t.Errorf("Expected suggestion to include 'pair<Type1, Type2>', got: %s", diag.Suggestion)
				}
				return
			}
		}
		t.Error("E3003 error not found")
	})

	t.Run("E3002_InfiniteInstantiation_Suggestion", func(t *testing.T) {
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
					BaseNode: baseNode(token.IDENT, "main"),
					Name:     "main",
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

		if !errs.HasErrors() {
			t.Fatal("Expected error for infinite instantiation")
		}

		// Find the E3002 error
		for _, diag := range errs.Diagnostics {
			if diag.Code == ErrInfiniteInstantiation {
				t.Logf("✓ Error Message: %s", diag.Message)
				t.Logf("✓ Suggestion: %s", diag.Suggestion)

				if !strings.Contains(diag.Suggestion, "Break the cycle") {
					t.Errorf("Expected suggestion to mention breaking the cycle, got: %s", diag.Suggestion)
				}
				return
			}
		}
		t.Error("E3002 error not found")
	})
}
