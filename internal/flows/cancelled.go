package flows

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// ErrCancelUnheard is a wait match of a flow that waits for its
// cancellation with no case for it: the cancellation would not be heard
// there.
const ErrCancelUnheard = "F0006"

// checkCancelled checks the flows that wait for their cancellation:
// every wait match of their run has a case for flow.cancelled(), so
// that a cancellation is always given where the flow waits.
func (c *checker) checkCancelled(roots []*fn) {
	for _, root := range roots {
		if root.decl.Body == nil || !callsCancelled(root.decl.Body) {
			continue
		}
		// What each wait of a wait match is: let __wait0 = flow.cancelled()
		waits := map[string]ast.Expr{}
		ast.Walk(root.decl.Body, func(ast.Expr) {}, func(stmt ast.Stmt) {
			if let, ok := stmt.(*ast.LetStmt); ok {
				waits[let.Name] = let.Value
			}
		})
		ast.Walk(root.decl.Body, func(expr ast.Expr) {
			call, ok := expr.(*ast.CallExpr)
			if !ok || !calls(call, "std.task.first") || len(call.Args) != 1 {
				return
			}
			arms, ok := call.Args[0].(*ast.ArrayLiteralExpr)
			if !ok {
				return
			}
			for _, arm := range arms.Elements {
				if armCall, ok := arm.(*ast.CallExpr); ok && len(armCall.Args) > 0 {
					if name, ok := armCall.Args[0].(*ast.Ident); ok {
						if waited, ok := waits[name.Name].(*ast.CallExpr); ok && calls(waited, "std.flow.cancelled") {
							return
						}
					}
				}
			}
			c.stageError(call.Token(), ErrCancelUnheard,
				"a wait match of "+name(root)+", which waits for its cancellation, has no case for it",
				"Write `case let reason = flow.cancelled(): ...` in it: a cancellation is given at the wait match the flow is in")
		}, func(ast.Stmt) {})
	}
}

func callsCancelled(body *ast.BlockStmt) bool {
	found := false
	ast.Walk(body, func(expr ast.Expr) {
		if call, ok := expr.(*ast.CallExpr); ok && calls(call, "std.flow.cancelled") {
			found = true
		}
	}, func(ast.Stmt) {})
	return found
}

// calls says whether a call is of a function of that name, or of a copy
// of it for some types.
func calls(call *ast.CallExpr, function string) bool {
	id, ok := call.Func.(*ast.Ident)
	return ok && (id.Name == function || strings.HasPrefix(id.Name, function+"_"))
}
