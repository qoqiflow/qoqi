package desugar

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

func TestDesugarForLoop(t *testing.T) {
	// Create a simple for loop: for x in arr { print(x); }
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	arrIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "arr",
	}

	printCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "print",
		},
		Args: []ast.Expr{
			&ast.Ident{
				BaseNode: ast.BaseNode{Pos: pos},
				Name:     "x",
			},
		},
	}

	forStmt := &ast.ForStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Var:      "x",
		Iterable: arrIdent,
		Body: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts: []ast.Stmt{
				&ast.ExprStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Expr:     printCall,
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
					Stmts:    []ast.Stmt{forStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[arrIdent] = types.NewArray(types.Int)

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map (no source context needed for this test)
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugaredProg, info := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	if desugaredProg == nil {
		t.Fatal("Expected desugared program, got nil")
	}

	// Check that temporary variables were generated
	if len(info.TmpVars) == 0 {
		t.Error("Expected temporary variables to be generated")
	}

	// Check the desugared function
	fnDecl := desugaredProg.Decls[0].(*ast.FnDecl)
	blockStmt := fnDecl.Body.Stmts[0].(*ast.BlockStmt)

	// The for loop should be desugared into a block with 3 statements:
	// 1. let __iter_N = 0
	// 2. let __len_N = __len(arr)
	// 3. while __iter_N < __len_N { ... }
	if len(blockStmt.Stmts) != 3 {
		t.Errorf("Expected 3 statements in desugared for loop, got %d", len(blockStmt.Stmts))
	}

	// Check first statement is iterator initialization
	iterInit, ok := blockStmt.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatal("Expected first statement to be let statement for iterator")
	}
	if iterInit.Value.String() != "0" {
		t.Errorf("Expected iterator to be initialized to 0, got %s", iterInit.Value.String())
	}

	// Check second statement is length initialization
	lenInit, ok := blockStmt.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatal("Expected second statement to be let statement for length")
	}
	call, ok := lenInit.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Args[0].String() != "arr" {
		t.Fatal("Expected length variable to be initialized to __len(arr)")
	}
	if callee, ok := call.Func.(*ast.Ident); !ok || callee.Name != "__len" {
		t.Error("Expected the length to be asked with the builtin __len")
	}

	// Check third statement is while loop
	whileStmt, ok := blockStmt.Stmts[2].(*ast.WhileStmt)
	if !ok {
		t.Fatal("Expected third statement to be while loop")
	}

	// Check while condition
	condBinary, ok := whileStmt.Cond.(*ast.BinaryExpr)
	if !ok || condBinary.Op != "&&" {
		t.Error("Expected while condition to be __iter_N < __len_N && __iter_N < __len(collection)")
	} else if first, ok := condBinary.Left.(*ast.BinaryExpr); !ok || first.Op != "<" {
		t.Error("Expected the first half of the condition to be __iter_N < __len_N")
	}

	// Check while body contains the loop variable initialization and original body
	if len(whileStmt.Body.Stmts) < 2 {
		t.Errorf("Expected at least 2 statements in while body, got %d", len(whileStmt.Body.Stmts))
	}

	// First statement should be loop variable initialization
	loopVarInit, ok := whileStmt.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatal("Expected first statement in while body to be loop variable initialization")
	}
	if loopVarInit.Name != "x" {
		t.Errorf("Expected loop variable name to be 'x', got %s", loopVarInit.Name)
	}
}

func TestDesugarMatchStatement(t *testing.T) {
	// Create a simple match: match x { case 1: { a(); } case 2: { b(); } }
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	xIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "x",
	}

	matchStmt := &ast.MatchStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     xIdent,
		Cases: []*ast.MatchCase{
			{
				BaseNode: ast.BaseNode{Pos: pos},
				Pattern: &ast.Literal{
					BaseNode: ast.BaseNode{Pos: pos},
					Value:    "1",
					Kind:     "int",
				},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							BaseNode: ast.BaseNode{Pos: pos},
							Expr: &ast.CallExpr{
								BaseNode: ast.BaseNode{Pos: pos},
								Func: &ast.Ident{
									BaseNode: ast.BaseNode{Pos: pos},
									Name:     "a",
								},
								Args: []ast.Expr{},
							},
						},
					},
				},
			},
			{
				BaseNode: ast.BaseNode{Pos: pos},
				Pattern: &ast.Literal{
					BaseNode: ast.BaseNode{Pos: pos},
					Value:    "2",
					Kind:     "int",
				},
				Body: &ast.BlockStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							BaseNode: ast.BaseNode{Pos: pos},
							Expr: &ast.CallExpr{
								BaseNode: ast.BaseNode{Pos: pos},
								Func: &ast.Ident{
									BaseNode: ast.BaseNode{Pos: pos},
									Name:     "b",
								},
								Args: []ast.Expr{},
							},
						},
					},
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
	desugaredProg, _ := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	// Check the desugared function
	fnDecl := desugaredProg.Decls[0].(*ast.FnDecl)

	// The match should be desugared into if-else chain
	ifStmt, ok := fnDecl.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("Expected match to be desugared to if statement, got %T", fnDecl.Body.Stmts[0])
	}

	// Check first condition: x == 1
	condBinary, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok || condBinary.Op != "==" {
		t.Error("Expected first condition to be equality check")
	}

	// Check there's an else clause for the second case
	if ifStmt.Else == nil {
		t.Error("Expected else clause for second case")
	}

	// The else should be another if statement (else if)
	elseIf, ok := ifStmt.Else.(*ast.IfStmt)
	if !ok {
		t.Errorf("Expected else to be another if statement, got %T", ifStmt.Else)
	}

	// Check second condition: x == 2
	condBinary2, ok := elseIf.Cond.(*ast.BinaryExpr)
	if !ok || condBinary2.Op != "==" {
		t.Error("Expected second condition to be equality check")
	}
}

func TestDesugarShortCircuitAnd(t *testing.T) {
	// Create: if a() && b() { action(); }
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	aCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "a",
		},
		Args: []ast.Expr{},
	}

	bCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "b",
		},
		Args: []ast.Expr{},
	}

	andExpr := &ast.BinaryExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Left:     aCall,
		Op:       "&&",
		Right:    bCall,
	}

	actionCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "action",
		},
		Args: []ast.Expr{},
	}

	ifStmt := &ast.IfStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     andExpr,
		Then: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts: []ast.Stmt{
				&ast.ExprStmt{
					BaseNode: ast.BaseNode{Pos: pos},
					Expr:     actionCall,
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
					Stmts:    []ast.Stmt{ifStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[aCall] = types.Bool
	typeInfo.Types[bCall] = types.Bool

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map (no source context needed for this test)
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugaredProg, info := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	// The condition keeps its && operator: IR generation turns it into
	// branches. Rewriting it into temporaries here would hide the condition
	// from the type checker when the program is checked again.
	if len(info.TmpVars) != 0 {
		t.Errorf("Expected no temporary variables for &&, got %v", info.TmpVars)
	}

	fnDecl := desugaredProg.Decls[0].(*ast.FnDecl)
	newIf, ok := fnDecl.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("Expected the if statement to stay an if statement, got %T", fnDecl.Body.Stmts[0])
	}
	cond, ok := newIf.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != "&&" {
		t.Errorf("Expected the condition to remain an && expression, got %s", newIf.Cond)
	}
}

func TestDesugarEmptyProgram(t *testing.T) {
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: pos},
		Decls:    []ast.Decl{},
	}

	typeInfo := types.NewInfo()
	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugaredProg, _ := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	if desugaredProg == nil {
		t.Fatal("Expected desugared program, got nil")
	}

	if len(desugaredProg.Decls) != 0 {
		t.Errorf("Expected empty program to remain empty, got %d declarations", len(desugaredProg.Decls))
	}
}

func TestDesugarLetWithShortCircuit(t *testing.T) {
	// Create: let result = a() && b();
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	aCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "a",
		},
		Args: []ast.Expr{},
	}

	bCall := &ast.CallExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Func: &ast.Ident{
			BaseNode: ast.BaseNode{Pos: pos},
			Name:     "b",
		},
		Args: []ast.Expr{},
	}

	andExpr := &ast.BinaryExpr{
		BaseNode: ast.BaseNode{Pos: pos},
		Left:     aCall,
		Op:       "&&",
		Right:    bCall,
	}

	letStmt := &ast.LetStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "result",
		Type: &ast.Type{
			BaseNode: ast.BaseNode{Pos: pos},
			Kind:     ast.TypeBasic,
			Name:     "Bool",
		},
		Value:   andExpr,
		IsConst: false,
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
					Stmts:    []ast.Stmt{letStmt},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[aCall] = types.Bool
	typeInfo.Types[bCall] = types.Bool
	typeInfo.Types[andExpr] = types.Bool

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map (no source context needed for this test)
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugaredProg, info := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	// The let keeps its && initializer, as a direct statement of the block
	if len(info.TmpVars) != 0 {
		t.Errorf("Expected no temporary variables for &&, got %v", info.TmpVars)
	}

	fnDecl := desugaredProg.Decls[0].(*ast.FnDecl)
	if len(fnDecl.Body.Stmts) != 1 {
		t.Fatalf("Expected the let to remain a single statement, got %d", len(fnDecl.Body.Stmts))
	}
	newLet, ok := fnDecl.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("Expected a let statement, got %T", fnDecl.Body.Stmts[0])
	}
	value, ok := newLet.Value.(*ast.BinaryExpr)
	if !ok || value.Op != "&&" {
		t.Errorf("Expected the initializer to remain an && expression, got %s", newLet.Value)
	}
}

func TestDesugarNestedForLoops(t *testing.T) {
	// Create nested for loops: for i in arr1 { for j in arr2 { ... } }
	pos := token.Position{File: "test.qi", Line: 1, Col: 1}

	// Create the identifiers first so we can use the same instances
	arr1Ident := &ast.Ident{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "arr1",
	}
	arr2Ident := &ast.Ident{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     "arr2",
	}

	innerFor := &ast.ForStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Var:      "j",
		Iterable: arr2Ident,
		Body: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts:    []ast.Stmt{},
		},
	}

	outerFor := &ast.ForStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Var:      "i",
		Iterable: arr1Ident,
		Body: &ast.BlockStmt{
			BaseNode: ast.BaseNode{Pos: pos},
			Stmts:    []ast.Stmt{innerFor},
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
					Stmts:    []ast.Stmt{outerFor},
				},
			},
		},
	}

	typeInfo := types.NewInfo()
	typeInfo.Types[arr1Ident] = types.NewArray(types.Int)
	typeInfo.Types[arr2Ident] = types.NewArray(types.Int)

	resolveTable := resolve.NewTable()
	errs := errors.NewHandler()

	// Create empty source lines map (no source context needed for this test)
	sourceLines := make(map[int]string)

	desugarer := NewDesugarer(prog, typeInfo, resolveTable, errs, sourceLines)
	desugaredProg, info := desugarer.Desugar()

	if errs.HasErrors() {
		t.Fatalf("Desugaring failed with errors: %v", errs.Diagnostics)
	}

	// Both for loops should generate temporary variables
	if len(info.TmpVars) < 4 {
		t.Errorf("Expected at least 4 temporary variables for nested loops, got %d", len(info.TmpVars))
	}

	// Verify structure
	fnDecl := desugaredProg.Decls[0].(*ast.FnDecl)
	outerBlock := fnDecl.Body.Stmts[0].(*ast.BlockStmt)

	// Should have 3 statements: iter init, len init, while
	if len(outerBlock.Stmts) != 3 {
		t.Errorf("Expected 3 statements in outer desugared for loop, got %d", len(outerBlock.Stmts))
	}
}
