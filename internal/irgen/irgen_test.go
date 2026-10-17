package irgen

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

func TestSimpleFunctionGeneration(t *testing.T) {
	// fn add(a: Int, b: Int) -> Int {
	//     return a + b;
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.ReturnStmt{
				Value: &ast.BinaryExpr{
					Left:  &ast.Ident{Name: "a"},
					Op:    "+",
					Right: &ast.Ident{Name: "b"},
				},
			},
		},
	}

	fn := &ast.FnDecl{
		Name: "add",
		Params: []*ast.Param{
			{Name: "a", Type: nil},
			{Name: "b", Type: nil},
		},
		Body: body,
	}

	prog.Decls = append(prog.Decls, fn)

	// Setup minimal context
	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}
	captureInfo := analysis.NewCaptureInfo()
	errs := errors.NewHandler()

	// Generate IR
	gen := NewIRGenerator(prog, typeInfo, resolveTable, captureInfo, errs)
	irProg := gen.Generate()

	// Verify IR program was created
	if irProg == nil {
		t.Fatal("Expected IR program to be created")
	}

	// Verify function was generated
	if len(irProg.Functions) != 1 {
		t.Fatalf("Expected 1 function, got %d", len(irProg.Functions))
	}

	irFunc := irProg.Functions[0]
	if irFunc.Name != "add" {
		t.Errorf("Expected function name 'add', got '%s'", irFunc.Name)
	}

	// Verify parameters
	if len(irFunc.Params) != 2 {
		t.Errorf("Expected 2 parameters, got %d", len(irFunc.Params))
	}

	// Verify at least one basic block
	if len(irFunc.Blocks) == 0 {
		t.Error("Expected at least one basic block")
	}

	// Verify instructions were generated
	if len(irFunc.Blocks[0].Instructions) == 0 {
		t.Error("Expected instructions in first block")
	}
}

func TestLiteralExpression(t *testing.T) {
	// fn constant() -> Int {
	//     return 42;
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.ReturnStmt{
				Value: &ast.Literal{Kind: "int", Value: "42"},
			},
		},
	}

	fn := &ast.FnDecl{
		Name:   "constant",
		Params: []*ast.Param{},
		Body:   body,
	}

	prog.Decls = append(prog.Decls, fn)

	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}
	captureInfo := analysis.NewCaptureInfo()
	errs := errors.NewHandler()

	gen := NewIRGenerator(prog, typeInfo, resolveTable, captureInfo, errs)
	irProg := gen.Generate()

	if len(irProg.Functions) != 1 {
		t.Fatalf("Expected 1 function, got %d", len(irProg.Functions))
	}

	// Check for MOV and RET instructions
	instrs := irProg.Functions[0].Blocks[0].Instructions
	foundMov := false
	foundRet := false

	for _, instr := range instrs {
		if instr.Op == ir.OpMov {
			foundMov = true
		}
		if instr.Op == ir.OpRet {
			foundRet = true
		}
	}

	if !foundMov {
		t.Error("Expected MOV instruction for literal")
	}
	if !foundRet {
		t.Error("Expected RET instruction")
	}
}

func TestIfStatement(t *testing.T) {
	// fn test(x: Int) -> Int {
	//     if x > 0 {
	//         return 1;
	//     } else {
	//         return 0;
	//     }
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.IfStmt{
				Cond: &ast.BinaryExpr{
					Left:  &ast.Ident{Name: "x"},
					Op:    ">",
					Right: &ast.Literal{Kind: "int", Value: "0"},
				},
				Then: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "int", Value: "1"},
						},
					},
				},
				Else: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{
							Value: &ast.Literal{Kind: "int", Value: "0"},
						},
					},
				},
			},
		},
	}

	fn := &ast.FnDecl{
		Name: "test",
		Params: []*ast.Param{
			{Name: "x", Type: nil},
		},
		Body: body,
	}

	prog.Decls = append(prog.Decls, fn)

	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}
	captureInfo := analysis.NewCaptureInfo()
	errs := errors.NewHandler()

	gen := NewIRGenerator(prog, typeInfo, resolveTable, captureInfo, errs)
	irProg := gen.Generate()

	if len(irProg.Functions) != 1 {
		t.Fatalf("Expected 1 function, got %d", len(irProg.Functions))
	}

	// Verify multiple basic blocks were created (for if-then-else)
	if len(irProg.Functions[0].Blocks) < 3 {
		t.Errorf("Expected at least 3 basic blocks for if-else, got %d", len(irProg.Functions[0].Blocks))
	}

	// Check for branch instruction
	foundBr := false
	for _, block := range irProg.Functions[0].Blocks {
		for _, instr := range block.Instructions {
			if instr.Op == ir.OpBr {
				foundBr = true
				break
			}
		}
	}

	if !foundBr {
		t.Error("Expected BR (branch) instruction for if statement")
	}
}

func TestWhileLoop(t *testing.T) {
	// fn loop(n: Int) -> Int {
	//     while n > 0 {
	//         n = n - 1;
	//     }
	//     return n;
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.WhileStmt{
				Cond: &ast.BinaryExpr{
					Left:  &ast.Ident{Name: "n"},
					Op:    ">",
					Right: &ast.Literal{Kind: "int", Value: "0"},
				},
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.BinaryExpr{
								Left:  &ast.Ident{Name: "n"},
								Op:    "-",
								Right: &ast.Literal{Kind: "int", Value: "1"},
							},
						},
					},
				},
			},
			&ast.ReturnStmt{
				Value: &ast.Ident{Name: "n"},
			},
		},
	}

	fn := &ast.FnDecl{
		Name: "loop",
		Params: []*ast.Param{
			{Name: "n", Type: nil},
		},
		Body: body,
	}

	prog.Decls = append(prog.Decls, fn)

	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}
	captureInfo := analysis.NewCaptureInfo()
	errs := errors.NewHandler()

	gen := NewIRGenerator(prog, typeInfo, resolveTable, captureInfo, errs)
	irProg := gen.Generate()

	if len(irProg.Functions) != 1 {
		t.Fatalf("Expected 1 function, got %d", len(irProg.Functions))
	}

	// Verify multiple basic blocks for loop (cond, body, exit)
	if len(irProg.Functions[0].Blocks) < 3 {
		t.Errorf("Expected at least 3 basic blocks for while loop, got %d", len(irProg.Functions[0].Blocks))
	}

	// Check for jump instructions (loop back)
	foundJmp := false
	for _, block := range irProg.Functions[0].Blocks {
		for _, instr := range block.Instructions {
			if instr.Op == ir.OpJmp {
				foundJmp = true
				break
			}
		}
	}

	if !foundJmp {
		t.Error("Expected JMP instruction for while loop")
	}
}
