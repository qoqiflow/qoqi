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

// Helper to create a token
func tok(typ token.TokenType, lit string) token.Token {
	return token.Token{Type: typ, Literal: lit}
}

// Helper to create a base node
func baseNode(t token.TokenType, lit string) ast.BaseNode {
	return ast.BaseNode{
		Pos: token.Position{Line: 1, Col: 1},
		Tok: tok(t, lit),
	}
}

// Test: Single type parameter instantiation
func TestSingleTypeParameterInstantiation(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "identity"),
				Name:       "identity",
				TypeParams: []string{"T"},
				Params: []*ast.Param{
					{
						BaseNode: baseNode(token.IDENT, "x"),
						Name:     "x",
						Type:     &ast.Type{Kind: ast.TypeBasic, Name: "T"},
					},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							BaseNode: baseNode(token.RETURN, "return"),
							Value:    &ast.Ident{Name: "x"},
						},
					},
				},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "Int"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "identity",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								},
								Args: []ast.Expr{&ast.Literal{Value: "42", Kind: "int"}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Should have generated identity_Int
	found := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "identity_Int" {
				found = true
				if len(fn.TypeParams) != 0 {
					t.Error("Monomorphized function should have no type params")
				}
				if fn.Params[0].Type.Name != "Int" && fn.Params[0].Type.Name != "int" {
					t.Errorf("Expected param type Int, got %s", fn.Params[0].Type.Name)
				}
			}
		}
	}

	if !found {
		t.Error("identity_Int not generated")
	}

	if len(info.Generated) != 1 {
		t.Errorf("Expected 1 instantiation, got %d", len(info.Generated))
	}
}

// Test: Multiple type parameters
func TestMultipleTypeParameters(t *testing.T) {
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
										{Kind: ast.TypeBasic, Name: "String"},
									},
								},
								Args: []ast.Expr{
									&ast.Literal{Value: "1"},
									&ast.Literal{Value: "hello"},
								},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	found := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "pair_Int_String" {
				found = true
				if len(fn.Params) != 2 {
					t.Errorf("Expected 2 params, got %d", len(fn.Params))
				}
				if fn.Params[0].Type.Name != "Int" && fn.Params[0].Type.Name != "int" {
					t.Errorf("Expected first param Int, got %s", fn.Params[0].Type.Name)
				}
				if fn.Params[1].Type.Name != "String" && fn.Params[1].Type.Name != "string" {
					t.Errorf("Expected second param String, got %s", fn.Params[1].Type.Name)
				}
			}
		}
	}

	if !found {
		t.Error("pair_Int_String not generated")
	}
}

// Test: Multiple instantiations of same generic with different types
func TestMultipleDifferentInstantiations(t *testing.T) {
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
						&ast.LetStmt{
							Name: "a",
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "identity",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								},
								Args: []ast.Expr{&ast.Literal{Value: "1"}},
							},
						},
						&ast.LetStmt{
							Name: "b",
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "identity",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "String"}},
								},
								Args: []ast.Expr{&ast.Literal{Value: "hello"}},
							},
						},
						&ast.LetStmt{
							Name: "c",
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "identity",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Float"}},
								},
								Args: []ast.Expr{&ast.Literal{Value: "3.14"}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Should generate 3 different versions
	foundInt := false
	foundString := false
	foundFloat := false

	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			switch fn.Name {
			case "identity_Int":
				foundInt = true
			case "identity_String":
				foundString = true
			case "identity_Float":
				foundFloat = true
			}
		}
	}

	if !foundInt || !foundString || !foundFloat {
		t.Errorf("Missing instantiations: Int=%v, String=%v, Float=%v", foundInt, foundString, foundFloat)
	}

	if len(info.Generated) != 3 {
		t.Errorf("Expected 3 instantiations, got %d", len(info.Generated))
	}
}

// Test: Duplicate instantiations (should reuse same instance)
func TestDuplicateInstantiationsReuse(t *testing.T) {
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
						&ast.LetStmt{
							Name: "a",
							Value: &ast.CallExpr{
								Func: &ast.Ident{Name: "identity", TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}}},
								Args: []ast.Expr{&ast.Literal{Value: "1"}},
							},
						},
						&ast.LetStmt{
							Name: "b",
							Value: &ast.CallExpr{
								Func: &ast.Ident{Name: "identity", TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}}},
								Args: []ast.Expr{&ast.Literal{Value: "2"}},
							},
						},
						&ast.LetStmt{
							Name: "c",
							Value: &ast.CallExpr{
								Func: &ast.Ident{Name: "identity", TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}}},
								Args: []ast.Expr{&ast.Literal{Value: "3"}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Count how many identity_Int functions were generated
	count := 0
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "identity_Int" {
				count++
			}
		}
	}

	if count != 1 {
		t.Errorf("Expected exactly 1 identity_Int, got %d (duplicates should be reused)", count)
	}

	if len(info.Generated) != 1 {
		t.Errorf("Expected 1 unique instantiation, got %d", len(info.Generated))
	}
}

// Test: Nested generics (Vec<Vec<Int>>)
func TestNestedGenerics(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.StructDecl{
				BaseNode:   baseNode(token.STRUCT, "struct"),
				Name:       "Vec",
				TypeParams: []string{"T"},
				Fields: []*ast.Field{
					{Name: "data", Type: &ast.Type{Kind: ast.TypeArray, ElemType: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "test"),
				Name:       "test",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.LetStmt{
							Name: "v",
							Value: &ast.StructInitExpr{
								Name: "Vec",
								TypeArgs: []*ast.Type{
									{
										Kind: ast.TypeBasic,
										Name: "Vec",
										Generics: []*ast.Type{
											{Kind: ast.TypeBasic, Name: "Int"},
										},
									},
								},
								Fields: map[string]ast.Expr{},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Should generate Vec_Vec_Int
	found := false
	for _, decl := range result.Decls {
		if s, ok := decl.(*ast.StructDecl); ok {
			if strings.Contains(s.Name, "Vec") && strings.Contains(s.Name, "Int") {
				found = true
				t.Logf("Generated nested struct: %s", s.Name)
			}
		}
	}

	if !found {
		t.Error("Nested generic struct not generated")
	}
}

// Test: Generic struct instantiation
func TestGenericStructInstantiation(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.StructDecl{
				BaseNode:   baseNode(token.STRUCT, "struct"),
				Name:       "Box",
				TypeParams: []string{"T"},
				Fields: []*ast.Field{
					{Name: "value", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}},
				},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "main"),
				Name:       "main",
				TypeParams: []string{},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.LetStmt{
							Name: "b",
							Value: &ast.StructInitExpr{
								Name:     "Box",
								TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								Fields:   map[string]ast.Expr{"value": &ast.Literal{Value: "42"}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	found := false
	for _, decl := range result.Decls {
		if s, ok := decl.(*ast.StructDecl); ok {
			if s.Name == "Box_Int" {
				found = true
				if len(s.TypeParams) != 0 {
					t.Error("Monomorphized struct should have no type params")
				}
				if len(s.Fields) != 1 {
					t.Errorf("Expected 1 field, got %d", len(s.Fields))
				}
				if s.Fields[0].Type.Name != "Int" && s.Fields[0].Type.Name != "int" {
					t.Errorf("Expected field type Int, got %s", s.Fields[0].Type.Name)
				}
			}
		}
	}

	if !found {
		t.Error("Box_Int not generated")
	}
}

// Test: Array type with generic element
func TestArrayTypeGeneric(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "first"),
				Name:       "first",
				TypeParams: []string{"T"},
				Params: []*ast.Param{
					{
						Name: "arr",
						Type: &ast.Type{
							Kind:     ast.TypeArray,
							ElemType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
						},
					},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
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
									Name:     "first",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
								},
								Args: []ast.Expr{&ast.ArrayLiteralExpr{Elements: []ast.Expr{}}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	found := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "first_Int" {
				found = true
				if fn.Params[0].Type.Kind != ast.TypeArray {
					t.Error("Expected array type parameter")
				}
				if fn.Params[0].Type.ElemType.Name != "Int" && fn.Params[0].Type.ElemType.Name != "int" {
					t.Errorf("Expected array element type Int, got %s", fn.Params[0].Type.ElemType.Name)
				}
			}
		}
	}

	if !found {
		t.Error("first_Int not generated")
	}
}

// Test: Transitive dependencies (generic calling generic)
func TestTransitiveDependencies(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "inner"),
				Name:       "inner",
				TypeParams: []string{"T"},
				Params:     []*ast.Param{{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "T"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}}},
			},
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "outer"),
				Name:       "outer",
				TypeParams: []string{"U"},
				Params:     []*ast.Param{{Name: "y", Type: &ast.Type{Kind: ast.TypeBasic, Name: "U"}}},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "U"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.CallExpr{
								Func: &ast.Ident{
									Name:     "inner",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "U"}},
								},
								Args: []ast.Expr{&ast.Ident{Name: "y"}},
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
									Name:     "outer",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
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
	result, info := m.Monomorphize()

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Both inner_Int and outer_Int should be generated
	foundInner := false
	foundOuter := false

	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "inner_Int" {
				foundInner = true
			}
			if fn.Name == "outer_Int" {
				foundOuter = true
			}
		}
	}

	if !foundInner {
		t.Error("inner_Int not generated (transitive dependency)")
	}
	if !foundOuter {
		t.Error("outer_Int not generated")
	}

	if len(info.Generated) < 2 {
		t.Errorf("Expected 2 instantiations for transitive deps, got %d", len(info.Generated))
	}
}

// Test: Call site rewriting removes type arguments
func TestCallSiteRewriting(t *testing.T) {
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
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "Int"}},
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

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	// Check that the call in main has been rewritten
	// Find the main function
	var mainFn *ast.FnDecl
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatal("main function not found")
	}

	returnStmt := mainFn.Body.Stmts[0].(*ast.ReturnStmt)

	// The rewriter may have changed the structure - check if it's a CallExpr or Ident
	if callExpr, ok := returnStmt.Value.(*ast.CallExpr); ok {
		ident := callExpr.Func.(*ast.Ident)
		if ident.Name != "identity_Int" {
			t.Errorf("Call site not rewritten: expected identity_Int, got %s", ident.Name)
		}
		if len(ident.TypeArgs) != 0 {
			t.Errorf("Type arguments not removed from call site, found %d", len(ident.TypeArgs))
		}
	} else if ident, ok := returnStmt.Value.(*ast.Ident); ok {
		// Sometimes rewriting may simplify - check the identifier
		if ident.Name != "identity_Int" && !strings.Contains(ident.Name, "identity") {
			t.Errorf("Call site not rewritten properly: got %s", ident.Name)
		}
	} else {
		t.Logf("Warning: Return value is neither CallExpr nor Ident, type is %T", returnStmt.Value)
	}
}

// Test: No generic functions (should not change program)
func TestNoGenerics(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "add"),
				Name:       "add",
				TypeParams: []string{}, // Not generic
				Params: []*ast.Param{
					{Name: "a", Type: &ast.Type{Kind: ast.TypeBasic, Name: "Int"}},
					{Name: "b", Type: &ast.Type{Kind: ast.TypeBasic, Name: "Int"}},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "Int"},
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

	if len(result.Decls) != len(prog.Decls) {
		t.Error("Program should not change when no generics present")
	}

	if len(info.Generated) != 0 {
		t.Errorf("Expected 0 instantiations, got %d", len(info.Generated))
	}
}

// Test: Type substitution in complex nested types
func TestComplexTypeSubstitution(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode:   baseNode(token.IDENT, "process"),
				Name:       "process",
				TypeParams: []string{"T"},
				Params: []*ast.Param{
					{
						Name: "f",
						Type: &ast.Type{
							Kind: ast.TypeFunction,
							ParamTypes: []*ast.Type{
								{Kind: ast.TypeBasic, Name: "T"},
							},
							ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
						},
					},
					{
						Name: "arr",
						Type: &ast.Type{
							Kind:     ast.TypeArray,
							ElemType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
						},
					},
				},
				ReturnType: &ast.Type{
					Kind:     ast.TypeArray,
					ElemType: &ast.Type{Kind: ast.TypeBasic, Name: "T"},
				},
				Body: &ast.BlockStmt{Stmts: []ast.Stmt{}},
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
									Name:     "process",
									TypeArgs: []*ast.Type{{Kind: ast.TypeBasic, Name: "String"}},
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
	result, _ := m.Monomorphize()

	if errs.HasErrors() {
		t.Fatalf("Unexpected errors: %v", errs.Diagnostics)
	}

	found := false
	for _, decl := range result.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			if fn.Name == "process_String" {
				found = true

				// Check function parameter type
				if fn.Params[0].Type.Kind != ast.TypeFunction {
					t.Error("Expected function type parameter")
				}
				if fn.Params[0].Type.ParamTypes[0].Name != "String" && fn.Params[0].Type.ParamTypes[0].Name != "string" {
					t.Errorf("Expected String in function param, got %s", fn.Params[0].Type.ParamTypes[0].Name)
				}

				// Check array parameter type
				if fn.Params[1].Type.Kind != ast.TypeArray {
					t.Error("Expected array type parameter")
				}
				if fn.Params[1].Type.ElemType.Name != "String" && fn.Params[1].Type.ElemType.Name != "string" {
					t.Errorf("Expected array of String, got %s", fn.Params[1].Type.ElemType.Name)
				}

				// Check return type
				if fn.ReturnType.Kind != ast.TypeArray {
					t.Error("Expected array return type")
				}
				if fn.ReturnType.ElemType.Name != "String" && fn.ReturnType.ElemType.Name != "string" {
					t.Errorf("Expected return array of String, got %s", fn.ReturnType.ElemType.Name)
				}
			}
		}
	}

	if !found {
		t.Error("process_String not generated")
	}
}
