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

// Test instantiation key generation
func TestInstantiationKey(t *testing.T) {
	key1 := MakeInstantiationKey("identity", []types.Type{types.Int})
	key2 := MakeInstantiationKey("identity", []types.Type{types.Int})
	key3 := MakeInstantiationKey("identity", []types.Type{types.String})

	if key1 != key2 {
		t.Error("Same instantiations should produce same keys")
	}

	if key1 == key3 {
		t.Error("Different instantiations should produce different keys")
	}

	if !strings.Contains(string(key1), "identity") {
		t.Error("Key should contain function name")
	}
}

// Test name mangling
func TestNameMangling(t *testing.T) {
	errs := errors.NewHandler()
	typeInfo := types.NewInfo()
	resolveTable := resolve.NewTable()
	prog := &ast.Program{Decls: []ast.Decl{}}

	m := NewMonomorphizer(prog, typeInfo, resolveTable, errs)

	tests := []struct {
		name     string
		typeArgs []types.Type
		expected string
	}{
		{
			name:     "identity",
			typeArgs: []types.Type{types.Int},
			expected: "identity_Int",
		},
		{
			name:     "map",
			typeArgs: []types.Type{types.Int, types.String},
			expected: "map_Int_String",
		},
		{
			name:     "vec",
			typeArgs: []types.Type{&types.ArrayType{ElementType: types.Int}},
			expected: "vec_Array_Int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := MakeInstantiationKey(tt.name, tt.typeArgs)
			m.instantiations[key] = &Instantiation{
				Key:      key,
				BaseName: tt.name,
				TypeArgs: tt.typeArgs,
			}

			result := m.mangleName(key)
			if result != tt.expected {
				t.Errorf("mangleName(%s, %v) = %s; want %s", tt.name, tt.typeArgs, result, tt.expected)
			}
		})
	}
}

// Test that we can create a monomorphizer
func TestNewMonomorphizer(t *testing.T) {
	errs := errors.NewHandler()
	typeInfo := types.NewInfo()
	resolveTable := resolve.NewTable()
	prog := &ast.Program{Decls: []ast.Decl{}}

	m := NewMonomorphizer(prog, typeInfo, resolveTable, errs)

	if m == nil {
		t.Fatal("NewMonomorphizer returned nil")
	}

	if m.prog != prog {
		t.Error("Monomorphizer prog field not set correctly")
	}

	if m.instantiations == nil {
		t.Error("instantiations map not initialized")
	}

	if m.generated == nil {
		t.Error("generated map not initialized")
	}
}

// Test collecting generic declarations
func TestCollectGenerics(t *testing.T) {
	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				BaseNode: ast.BaseNode{
					Tok: token.Token{Type: token.IDENT, Literal: "identity"},
				},
				Name:       "identity",
				TypeParams: []string{"T"},
			},
			&ast.FnDecl{
				BaseNode: ast.BaseNode{
					Tok: token.Token{Type: token.IDENT, Literal: "main"},
				},
				Name:       "main",
				TypeParams: []string{}, // Not generic
			},
			&ast.StructDecl{
				BaseNode: ast.BaseNode{
					Tok: token.Token{Type: token.STRUCT, Literal: "struct"},
				},
				Name:       "Vec",
				TypeParams: []string{"T"},
			},
		},
	}

	errs := errors.NewHandler()
	m := NewMonomorphizer(prog, types.NewInfo(), resolve.NewTable(), errs)
	m.collectGenerics()

	// Should have collected identity and Vec
	if len(m.generics) != 2 {
		t.Errorf("Expected 2 generic declarations, got %d", len(m.generics))
	}

	if _, ok := m.generics["identity"]; !ok {
		t.Error("Generic function 'identity' not collected")
	}

	if _, ok := m.generics["Vec"]; !ok {
		t.Error("Generic struct 'Vec' not collected")
	}

	if _, ok := m.generics["main"]; ok {
		t.Error("Non-generic function 'main' should not be collected")
	}
}
