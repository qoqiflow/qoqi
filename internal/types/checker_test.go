package types

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

func TestChecker_Simple(t *testing.T) {
	// let x: int = 10;
	val := &ast.Literal{Kind: "int", Value: "10"}
	decl := &ast.LetStmt{
		Name:  "x",
		Type:  &ast.Type{Kind: ast.TypeBasic, Name: "int"},
		Value: val,
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{decl},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	info := checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}

	if info.Types[val] != Int {
		t.Errorf("Expected type Int, got %s", info.Types[val])
	}
}

func TestChecker_TypeMismatch(t *testing.T) {
	// let x: int = "string";
	val := &ast.Literal{Kind: "string", Value: "hello"}
	decl := &ast.LetStmt{
		Name:  "x",
		Type:  &ast.Type{Kind: ast.TypeBasic, Name: "int"},
		Value: val,
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{decl},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if !errs.HasErrors() {
		t.Error("Expected errors, got none")
	}
}

func TestChecker_StructDecl(t *testing.T) {
	// struct Point { x: int, y: int }
	structDecl := &ast.StructDecl{
		Name: "Point",
		Fields: []*ast.Field{
			{Name: "x", Type: &ast.Type{Kind: ast.TypeBasic, Name: "int"}},
			{Name: "y", Type: &ast.Type{Kind: ast.TypeBasic, Name: "int"}},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{structDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_EnumDecl(t *testing.T) {
	// enum Option { Some(int), None }
	enumDecl := &ast.EnumDecl{
		Name: "Option",
		Variants: []*ast.EnumVariant{
			{
				Name:   "Some",
				Values: []*ast.Type{{Kind: ast.TypeBasic, Name: "int"}},
			},
			{
				Name:   "None",
				Values: nil,
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{enumDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_TraitDecl(t *testing.T) {
	// trait Display { fn show(): string; }
	traitDecl := &ast.TraitDecl{
		Name: "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body:       nil, // Abstract method
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_ImplDecl(t *testing.T) {
	// trait Display { fn show(): string; }
	// impl Display for Point { fn show(): string { return ""; } }
	traitDecl := &ast.TraitDecl{
		Name: "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body:       nil, // Abstract method
			},
		},
	}
	implDecl := &ast.ImplDecl{
		StructName: "Point",
		TraitName:  "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "string", Value: ""},
						},
					},
				},
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl, implDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_ForStmt_ValidString(t *testing.T) {
	// for char in str { }
	strLiteral := &ast.Literal{Kind: "string", Value: "hello"}
	forStmt := &ast.ForStmt{
		Var:      "char",
		Iterable: strLiteral,
		Body:     &ast.BlockStmt{Stmts: []ast.Stmt{}},
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{forStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	// Should not have errors since we're iterating over a string
	if errs.HasErrors() {
		t.Errorf("Expected no errors for string iteration, got %v", errs.Diagnostics)
	}
}

func TestChecker_ForStmt_InvalidIterable(t *testing.T) {
	// for x in 42 { }  // Error: can't iterate over int
	intLiteral := &ast.Literal{Kind: "int", Value: "42"}
	forStmt := &ast.ForStmt{
		Var:      "x",
		Iterable: intLiteral,
		Body:     &ast.BlockStmt{Stmts: []ast.Stmt{}},
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{forStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	// Should have error - can't iterate over int
	if !errs.HasErrors() {
		t.Error("Expected error for iterating over int, got none")
	}

	// Verify it's the right error code
	foundIterableError := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrNotIterable {
			foundIterableError = true
			break
		}
	}
	if !foundIterableError {
		t.Errorf("Expected ErrNotIterable error, got %v", errs.Diagnostics)
	}
}

func TestChecker_MatchStmt_WithExpr(t *testing.T) {
	// match x { case 1: 2 }
	matchStmt := &ast.MatchStmt{
		Cond: &ast.Literal{Kind: "int", Value: "1"},
		Cases: []*ast.MatchCase{
			{
				Pattern: &ast.Literal{Kind: "int", Value: "1"},
				Body:    &ast.Literal{Kind: "int", Value: "2"},
			},
		},
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{matchStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_MatchStmt_WithBlock(t *testing.T) {
	// match x { case 1: { return 2; } } return 0;
	matchStmt := &ast.MatchStmt{
		Cond: &ast.Literal{Kind: "int", Value: "1"},
		Cases: []*ast.MatchCase{
			{
				Pattern: &ast.Literal{Kind: "int", Value: "1"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "int", Value: "2"},
						},
					},
				},
			},
		},
	}
	fn := &ast.FnDecl{
		Name:       "main",
		ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "int"},
		Body: &ast.BlockStmt{
			// The match has no default, so a return has to follow it
			Stmts: []ast.Stmt{matchStmt, &ast.ReturnStmt{Value: &ast.Literal{Kind: "int", Value: "0"}}},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}
}

func TestChecker_ArrayLiteral_Valid(t *testing.T) {
	// [1, 2, 3]
	arrayExpr := &ast.ArrayLiteralExpr{
		Elements: []ast.Expr{
			&ast.Literal{Kind: "int", Value: "1"},
			&ast.Literal{Kind: "int", Value: "2"},
			&ast.Literal{Kind: "int", Value: "3"},
		},
	}
	exprStmt := &ast.ExprStmt{Expr: arrayExpr}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{exprStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	info := checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors, got %v", errs.Diagnostics)
	}

	// Check that the array has the correct type
	arrayType, ok := info.Types[arrayExpr].(*ArrayType)
	if !ok {
		t.Errorf("Expected ArrayType, got %T", info.Types[arrayExpr])
	} else if !arrayType.ElementType.Equals(Int) {
		t.Errorf("Expected array of Int, got array of %s", arrayType.ElementType)
	}
}

func TestChecker_ArrayLiteral_TypeMismatch(t *testing.T) {
	// [1, "string", 3]  // Error: mixed types
	arrayExpr := &ast.ArrayLiteralExpr{
		Elements: []ast.Expr{
			&ast.Literal{Kind: "int", Value: "1"},
			&ast.Literal{Kind: "string", Value: "hello"},
			&ast.Literal{Kind: "int", Value: "3"},
		},
	}
	exprStmt := &ast.ExprStmt{Expr: arrayExpr}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{exprStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	// Should have errors for type mismatch
	if !errs.HasErrors() {
		t.Error("Expected type mismatch error, got none")
	}
}

func TestChecker_ArrayLiteral_Empty(t *testing.T) {
	// []
	arrayExpr := &ast.ArrayLiteralExpr{
		Elements: []ast.Expr{},
	}
	exprStmt := &ast.ExprStmt{Expr: arrayExpr}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{exprStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	info := checker.Check()

	if errs.HasErrors() {
		t.Errorf("Expected no errors for empty array, got %v", errs.Diagnostics)
	}

	// Nothing constrains the elements of [] yet: the element type is
	// unknown, which fits any array type, rather than Any, which would
	// claim the array may hold values of every type.
	arrayType, ok := info.Types[arrayExpr].(*ArrayType)
	if !ok {
		t.Errorf("Expected ArrayType, got %T", info.Types[arrayExpr])
	} else if !arrayType.ElementType.Equals(Unknown) {
		t.Errorf("Expected array of unknown, got array of %s", arrayType.ElementType)
	}
}

func TestChecker_ForStmt_ArrayLiteral(t *testing.T) {
	// for x in [1, 2, 3] { }
	arrayExpr := &ast.ArrayLiteralExpr{
		Elements: []ast.Expr{
			&ast.Literal{Kind: "int", Value: "1"},
			&ast.Literal{Kind: "int", Value: "2"},
			&ast.Literal{Kind: "int", Value: "3"},
		},
	}
	forStmt := &ast.ForStmt{
		Var:      "x",
		Iterable: arrayExpr,
		Body:     &ast.BlockStmt{Stmts: []ast.Stmt{}},
	}
	fn := &ast.FnDecl{
		Name: "main",
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{forStmt},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{fn},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	// Should not have errors - iterating over array is valid
	if errs.HasErrors() {
		t.Errorf("Expected no errors for array iteration, got %v", errs.Diagnostics)
	}
}

func TestChecker_ImplDecl_MissingMethod(t *testing.T) {
	// trait Display { fn show(): string; fn describe(): string; }
	// impl Display for Point { fn show(): string { return ""; } }
	// Missing: describe method
	traitDecl := &ast.TraitDecl{
		Name: "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body:       nil,
			},
			{
				Name:       "describe",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body:       nil,
			},
		},
	}
	implDecl := &ast.ImplDecl{
		StructName: "Point",
		TraitName:  "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "string", Value: ""},
						},
					},
				},
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl, implDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if !errs.HasErrors() {
		t.Error("Expected error for missing method, got none")
	}

	// Verify it's the right error code
	foundMissingMethodError := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrMissingMethod {
			foundMissingMethodError = true
			break
		}
	}
	if !foundMissingMethodError {
		t.Errorf("Expected ErrMissingMethod error, got %v", errs.Diagnostics)
	}
}

func TestChecker_ImplDecl_SignatureMismatch_ReturnType(t *testing.T) {
	// trait Display { fn show(): string; }
	// impl Display for Point { fn show(): int { return 0; } }  // Wrong return type
	traitDecl := &ast.TraitDecl{
		Name: "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "string"},
				Body:       nil,
			},
		},
	}
	implDecl := &ast.ImplDecl{
		StructName: "Point",
		TraitName:  "Display",
		Methods: []*ast.FnDecl{
			{
				Name:       "show",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "int"},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "int", Value: "0"},
						},
					},
				},
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl, implDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if !errs.HasErrors() {
		t.Error("Expected error for signature mismatch, got none")
	}

	foundSignatureError := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrSignatureMismatch {
			foundSignatureError = true
			break
		}
	}
	if !foundSignatureError {
		t.Errorf("Expected ErrSignatureMismatch error, got %v", errs.Diagnostics)
	}
}

func TestChecker_ImplDecl_SignatureMismatch_ParamCount(t *testing.T) {
	// trait Printer { fn print(msg: string): void; }
	// impl Printer for Logger { fn print(): void { } }  // Wrong param count
	traitDecl := &ast.TraitDecl{
		Name: "Printer",
		Methods: []*ast.FnDecl{
			{
				Name: "print",
				Params: []*ast.Param{
					{Name: "msg", Type: &ast.Type{Kind: ast.TypeBasic, Name: "string"}},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "void"},
				Body:       nil,
			},
		},
	}
	implDecl := &ast.ImplDecl{
		StructName: "Logger",
		TraitName:  "Printer",
		Methods: []*ast.FnDecl{
			{
				Name:       "print",
				Params:     nil,
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "void"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl, implDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if !errs.HasErrors() {
		t.Error("Expected error for signature mismatch, got none")
	}

	foundSignatureError := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrSignatureMismatch {
			foundSignatureError = true
			break
		}
	}
	if !foundSignatureError {
		t.Errorf("Expected ErrSignatureMismatch error, got %v", errs.Diagnostics)
	}
}

func TestChecker_ImplDecl_SignatureMismatch_ParamType(t *testing.T) {
	// trait Printer { fn print(msg: string): void; }
	// impl Printer for Logger { fn print(msg: int): void { } }  // Wrong param type
	traitDecl := &ast.TraitDecl{
		Name: "Printer",
		Methods: []*ast.FnDecl{
			{
				Name: "print",
				Params: []*ast.Param{
					{Name: "msg", Type: &ast.Type{Kind: ast.TypeBasic, Name: "string"}},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "void"},
				Body:       nil,
			},
		},
	}
	implDecl := &ast.ImplDecl{
		StructName: "Logger",
		TraitName:  "Printer",
		Methods: []*ast.FnDecl{
			{
				Name: "print",
				Params: []*ast.Param{
					{Name: "msg", Type: &ast.Type{Kind: ast.TypeBasic, Name: "int"}},
				},
				ReturnType: &ast.Type{Kind: ast.TypeBasic, Name: "void"},
				Body:       &ast.BlockStmt{Stmts: []ast.Stmt{}},
			},
		},
	}
	prog := &ast.Program{
		Decls: []ast.Decl{traitDecl, implDecl},
	}

	errs := errors.NewHandler()
	table := resolve.NewTable()
	checker := NewChecker(prog, table, errs)
	checker.Check()

	if !errs.HasErrors() {
		t.Error("Expected error for signature mismatch, got none")
	}

	foundSignatureError := false
	for _, diag := range errs.Diagnostics {
		if diag.Code == ErrSignatureMismatch {
			foundSignatureError = true
			break
		}
	}
	if !foundSignatureError {
		t.Errorf("Expected ErrSignatureMismatch error, got %v", errs.Diagnostics)
	}
}
