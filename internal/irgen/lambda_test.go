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

func TestLambdaExpression(t *testing.T) {
	// fn makeAdder(x: Int) -> fn(Int) -> Int {
	//     return fn(y: Int) -> Int { x + y };
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	// Create lambda expression: fn(y: Int) -> Int { x + y }
	lambda := &ast.FnExpr{
		Params: []*ast.Param{
			{Name: "y", Type: nil},
		},
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{
					Value: &ast.BinaryExpr{
						Left:  &ast.Ident{Name: "x"},
						Op:    "+",
						Right: &ast.Ident{Name: "y"},
					},
				},
			},
		},
	}

	// Create outer function
	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.ReturnStmt{
				Value: lambda,
			},
		},
	}

	fn := &ast.FnDecl{
		Name: "makeAdder",
		Params: []*ast.Param{
			{Name: "x", Type: nil},
		},
		Body: body,
	}

	prog.Decls = append(prog.Decls, fn)

	// Setup context
	resolveTable := resolve.NewTable()
	typeInfo := &types.Info{
		Types: make(map[ast.Expr]types.Type),
		Defs:  make(map[string]types.Type),
	}

	// Create symbol for x (parameter of makeAdder)
	xSymbol := &resolve.Symbol{
		Name: "x",
		Kind: resolve.SymbolVar,
		Decl: fn.Params[0],
	}

	// Create symbol for y (parameter of lambda)
	ySymbol := &resolve.Symbol{
		Name: "y",
		Kind: resolve.SymbolVar,
		Decl: lambda.Params[0],
	}

	// Create scopes
	makeAdderScope := resolve.NewScope(nil)
	makeAdderScope.Insert("x", xSymbol)
	resolveTable.Scopes[fn] = makeAdderScope

	lambdaScope := resolve.NewScope(makeAdderScope)
	lambdaScope.Insert("y", ySymbol)
	resolveTable.Scopes[lambda] = lambdaScope

	// Associate the actual AST ident nodes with symbols
	xIdentInBinary := lambda.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr).Left.(*ast.Ident)
	yIdentInBinary := lambda.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr).Right.(*ast.Ident)

	// Map the actual AST nodes to their symbols
	resolveTable.Defs[xIdentInBinary] = xSymbol
	resolveTable.Defs[yIdentInBinary] = ySymbol

	// Run closure analysis
	errs := errors.NewHandler()
	analyzer := analysis.NewAnalyzer(prog, resolveTable, typeInfo, errs)
	captureInfo := analyzer.Analyze()

	// Generate IR
	gen := NewIRGenerator(prog, typeInfo, resolveTable, captureInfo, errs)
	irProg := gen.Generate()

	// Verify IR program was created
	if irProg == nil {
		t.Fatal("Expected IR program to be created")
	}

	// Should have 2 functions: lambda and makeAdder (lambdas are compiled first)
	if len(irProg.Functions) != 2 {
		t.Fatalf("Expected 2 functions (lambda + makeAdder), got %d", len(irProg.Functions))
	}

	// First function should be the lambda (compiled during makeAdder's body compilation)
	lambdaFunc := irProg.Functions[0]
	if lambdaFunc.Name != "lambda_0" {
		t.Errorf("Expected first function to be 'lambda_0', got '%s'", lambdaFunc.Name)
	}

	// Second function should be makeAdder
	makeAdderFunc := irProg.Functions[1]
	if makeAdderFunc.Name != "makeAdder" {
		t.Errorf("Expected second function to be 'makeAdder', got '%s'", makeAdderFunc.Name)
	}

	// Lambda should have 1 parameter (y)
	if len(lambdaFunc.Params) != 1 {
		t.Errorf("Expected lambda to have 1 parameter, got %d", len(lambdaFunc.Params))
	}

	// Check for MAKE_CLOSURE instruction in makeAdder
	foundMakeClosure := false
	for _, block := range makeAdderFunc.Blocks {
		for _, instr := range block.Instructions {
			if instr.Op == ir.OpMakeClosure {
				foundMakeClosure = true
				// Should have at least 2 operands: function ref + captured x
				if len(instr.Operands) < 2 {
					t.Errorf("Expected MAKE_CLOSURE to have at least 2 operands, got %d", len(instr.Operands))
				}
			}
		}
	}

	if !foundMakeClosure {
		t.Error("Expected MAKE_CLOSURE instruction in makeAdder function")
	}

	// Check for LOAD_UPVALUE in lambda (to access captured x)
	foundLoadUpvalue := false
	for _, block := range lambdaFunc.Blocks {
		for _, instr := range block.Instructions {
			if instr.Op == ir.OpLoadUpvalue {
				foundLoadUpvalue = true
			}
		}
	}

	if !foundLoadUpvalue {
		t.Error("Expected LOAD_UPVALUE instruction in lambda function")
	}
}

func TestNestedLambdas(t *testing.T) {
	// fn outer() -> fn() -> fn() -> Int {
	//     let x = 42;
	//     return fn() -> fn() -> Int {
	//         fn() -> Int { x }
	//     };
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	// Innermost lambda: fn() -> Int { x }
	innerLambda := &ast.FnExpr{
		Params: []*ast.Param{},
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{
					Value: &ast.Ident{Name: "x"},
				},
			},
		},
	}

	// Middle lambda: fn() -> fn() -> Int { fn() -> Int { x } }
	middleLambda := &ast.FnExpr{
		Params: []*ast.Param{},
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{
					Value: innerLambda,
				},
			},
		},
	}

	// Outer function
	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.LetStmt{
				Name:  "x",
				Value: &ast.Literal{Kind: "int", Value: "42"},
			},
			&ast.ReturnStmt{
				Value: middleLambda,
			},
		},
	}

	fn := &ast.FnDecl{
		Name:   "outer",
		Params: []*ast.Param{},
		Body:   body,
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

	// Should have 3 functions: 2 lambdas + outer (nested lambdas compiled first)
	if len(irProg.Functions) != 3 {
		t.Fatalf("Expected 3 functions (2 lambdas + outer), got %d", len(irProg.Functions))
	}

	// Verify function names
	// Middle lambda gets named first (lambda_0), then during its body compilation,
	// inner lambda is encountered and gets named (lambda_1).
	// Inner lambda is added to IR first (deepest first), then middle lambda, then outer.
	expectedNames := []string{"lambda_1", "lambda_0", "outer"}
	for i, expected := range expectedNames {
		if irProg.Functions[i].Name != expected {
			t.Errorf("Expected function %d to be '%s', got '%s'", i, expected, irProg.Functions[i].Name)
		}
	}
}

func TestLambdaWithoutCaptures(t *testing.T) {
	// fn identity() -> fn(Int) -> Int {
	//     return fn(x: Int) -> Int { x };
	// }

	prog := &ast.Program{
		BaseNode: ast.BaseNode{Pos: token.Position{File: "test.qi", Line: 1, Col: 1}},
		Decls:    []ast.Decl{},
	}

	// Lambda without captures
	lambda := &ast.FnExpr{
		Params: []*ast.Param{
			{Name: "x", Type: nil},
		},
		Body: &ast.BlockStmt{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{
					Value: &ast.Ident{Name: "x"},
				},
			},
		},
	}

	body := &ast.BlockStmt{
		Stmts: []ast.Stmt{
			&ast.ReturnStmt{
				Value: lambda,
			},
		},
	}

	fn := &ast.FnDecl{
		Name:   "identity",
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

	// Should NOT have MAKE_CLOSURE since there are no captures
	// Should just have MOV with function reference in identity function
	foundMakeClosure := false
	foundMov := false

	// Lambda is compiled first, then identity
	// Check identity function (second one)
	if len(irProg.Functions) < 2 {
		t.Fatalf("Expected at least 2 functions, got %d", len(irProg.Functions))
	}

	identityFunc := irProg.Functions[1]
	for _, block := range identityFunc.Blocks {
		for _, instr := range block.Instructions {
			if instr.Op == ir.OpMakeClosure {
				foundMakeClosure = true
			}
			if instr.Op == ir.OpMov {
				foundMov = true
			}
		}
	}

	if foundMakeClosure {
		t.Error("Should not have MAKE_CLOSURE for lambda without captures")
	}

	if !foundMov {
		t.Error("Expected MOV instruction for function reference")
	}
}
