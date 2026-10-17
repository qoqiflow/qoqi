package irgen

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/ir"
)

// createClosure creates a closure with captured variables. A variable that
// is a local of this function is captured by register; one that this
// function itself captured is passed on by upvalue index.
func (fc *FunctionCompiler) createClosure(
	fn *ir.Function,
	captures *analysis.FunctionCaptures,
) *ir.Register {
	capturedVals := make([]ir.Value, len(captures.CapturedVars))

	for i, cv := range captures.CapturedVars {
		if reg, depth, ok := fc.lookupScope(cv.Name); ok {
			capturedVals[i] = reg
			fc.noteCapture(reg, depth)
		} else if upvalIdx, ok := fc.upvalues[cv.Name]; ok {
			capturedVals[i] = ir.Constant{Val: upvalIdx, Type: "Upvalue"}
		} else {
			fc.gen.errs.ReportErrorFromToken(
				ErrUnsupportedConstruct,
				fmt.Sprintf("captured variable '%s' is not available here", cv.Name),
				fc.function.Token(),
				fc.gen.sourceLines,
			)
			capturedVals[i] = ir.Constant{Val: nil, Type: "Null"}
		}
	}

	// Emit OpMakeClosure instruction
	funcRef := ir.Constant{Val: fn.Name, Type: "FuncRef"}
	return fc.builder.CreateMakeClosure(funcRef, capturedVals...)
}

// compileLambda compiles a lambda expression (FnExpr)
func (fc *FunctionCompiler) compileLambda(lambda *ast.FnExpr) *ir.Register {
	// First, compile the lambda as a separate function
	lambdaFunc := fc.gen.compileLambdaFunction(lambda)

	// Get capture info
	captures := fc.gen.captureInfo.GetCaptures(lambda)
	if captures == nil || len(captures.CapturedVars) == 0 {
		// No captures - just reference the function
		return fc.builder.CreateMov(ir.Constant{
			Val:  lambdaFunc.Name,
			Type: "FuncRef",
		})
	}

	// Has captures - create closure
	return fc.createClosure(lambdaFunc, captures)
}

// compileLambdaFunction compiles a lambda expression as a separate IR function
func (g *IRGenerator) compileLambdaFunction(lambda *ast.FnExpr) *ir.Function {
	// Generate unique name
	name := fmt.Sprintf("lambda_%d", g.lambdaCount)
	g.lambdaCount++

	fc := g.newFunctionCompiler(lambda, name)
	fc.builder.Func.Signature = g.typeInfo.Signatures[lambda]
	return fc.compileBody(lambda.Params, lambda.Body)
}
