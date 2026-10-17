package monomorph

import "github.com/qoqiflow/qoqi/internal/ast"

// The three passes over a program (finding the uses of generics, what
// those uses need in turn, and writing the names of the specialized
// functions at the calls) each take statements and expressions apart
// themselves. What they do not name, they go through with these: a match,
// a defer, an if or a match that gives a value, a range. A generic call in
// one of those is a call like any other, and has to be found and written
// like any other: left alone, it would run the function as it was written
// for no type in particular, where a cast to its type parameter verifies
// nothing.

// insideStmt lists the expressions and statements directly inside a
// statement the passes do not take apart themselves.
func insideStmt(stmt ast.Stmt) (exprs []ast.Expr, stmts []ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.MatchStmt:
		exprs = append(exprs, s.Cond)
		for _, matchCase := range s.Cases {
			if matchCase.Guard != nil {
				exprs = append(exprs, matchCase.Guard)
			}
			switch body := matchCase.Body.(type) {
			case *ast.BlockStmt:
				if body != nil {
					stmts = append(stmts, body)
				}
			case ast.Expr:
				exprs = append(exprs, body)
			}
		}
	case *ast.DeferStmt:
		if s.Body != nil {
			stmts = append(stmts, s.Body)
		}
	}
	return exprs, stmts
}

// insideExpr is insideStmt for an expression.
func insideExpr(expr ast.Expr) (exprs []ast.Expr, stmts []ast.Stmt) {
	switch e := expr.(type) {
	case *ast.MatchExpr:
		if e.Match != nil {
			stmts = append(stmts, e.Match)
		}
	case *ast.CondExpr:
		exprs = append(exprs, e.Cond, e.Then, e.Else)
	case *ast.BlockExpr:
		stmts = append(stmts, e.Stmts...)
		if e.Value != nil {
			exprs = append(exprs, e.Value)
		}
	case *ast.RangeExpr:
		exprs = append(exprs, e.Low, e.High)
	}
	return exprs, stmts
}
