package resolve

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

func TestResolveSimpleVar(t *testing.T) {
	// let x = 1;
	// let y = x;

	xDecl := &ast.LetStmt{
		Name:  "x",
		Value: &ast.Literal{Value: "1", Kind: "int"},
	}

	xRef := &ast.Ident{Name: "x"}
	yDecl := &ast.LetStmt{
		Name:  "y",
		Value: xRef,
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{xDecl, yDecl},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	table := resolver.Resolve(prog)

	if errs.HasErrors() {
		t.Fatalf("Resolution failed with errors: %v", errs.Diagnostics)
	}

	// Check if xRef is resolved to xDecl
	sym := table.Defs[xRef]
	if sym == nil {
		t.Fatal("xRef not resolved")
	}

	if sym.Decl != xDecl {
		t.Errorf("Expected xRef to resolve to xDecl, got %v", sym.Decl)
	}
}

func TestResolveUndefined(t *testing.T) {
	// let y = x; // x undefined

	xRef := &ast.Ident{Name: "x"}
	yDecl := &ast.LetStmt{
		Name:  "y",
		Value: xRef,
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{yDecl},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	resolver.Resolve(prog)

	if !errs.HasErrors() {
		t.Fatal("Expected errors for undefined variable")
	}

	if len(errs.Diagnostics) != 1 {
		t.Fatalf("Expected 1 error, got %d", len(errs.Diagnostics))
	}

	if errs.Diagnostics[0].Code != "E1001" {
		t.Errorf("Expected error code E1001, got %s", errs.Diagnostics[0].Code)
	}
}

func TestResolveShadowing(t *testing.T) {
	// let x = 1;
	// {
	//    let x = 2;
	//    let y = x; // should refer to inner x
	// }

	outerX := &ast.LetStmt{Name: "x", Value: &ast.Literal{Value: "1", Kind: "int"}}
	innerX := &ast.LetStmt{Name: "x", Value: &ast.Literal{Value: "2", Kind: "int"}}
	xRef := &ast.Ident{Name: "x"}
	yDecl := &ast.LetStmt{Name: "y", Value: xRef}

	innerBlock := &ast.BlockStmt{
		Stmts: []ast.Stmt{innerX, yDecl},
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{outerX, innerBlock},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	table := resolver.Resolve(prog)

	if errs.HasErrors() {
		t.Fatalf("Resolution failed: %v", errs.Diagnostics)
	}

	sym := table.Defs[xRef]
	if sym.Decl != innerX {
		t.Error("Shadowing failed: y should refer to inner x")
	}
}
