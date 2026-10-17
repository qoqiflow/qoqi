package ast

// Walk calls the visitors for every expression and statement under a node,
// the node itself included, and the bodies of nested function expressions.
//
// A match that gives a value (MatchExpr) is visited as the expression it
// is, and the match it holds as a statement: whoever looks for matches
// finds both kinds the same way. The values a pattern compares with are
// visited as well: its literals, the bounds of a range.
func Walk(node Node, visitExpr func(Expr), visitStmt func(Stmt)) {
	var walkExpr func(expr Expr)
	var walkStmt func(stmt Stmt)
	var walkPattern func(pattern Node)

	walkPattern = func(pattern Node) {
		switch p := pattern.(type) {
		case *VariantPattern:
			for _, arg := range p.Args {
				walkPattern(arg)
			}
		case *StructPattern:
			for _, field := range p.Fields {
				walkPattern(field.Pattern)
			}
		case *BindingPattern, *Type:
		case Expr:
			walkExpr(p)
		}
	}

	walkExpr = func(expr Expr) {
		if expr == nil {
			return
		}
		visitExpr(expr)

		switch e := expr.(type) {
		case *BinaryExpr:
			walkExpr(e.Left)
			walkExpr(e.Right)
		case *UnaryExpr:
			walkExpr(e.Expr)
		case *CastExpr:
			walkExpr(e.Expr)
		case *TypeTestExpr:
			walkExpr(e.Expr)
		case *PatternTestExpr:
			walkExpr(e.Expr)
			walkPattern(e.Pattern)
		case *VariantExpr:
			for _, arg := range e.Args {
				walkExpr(arg)
			}
		case *VariantTestExpr:
			walkExpr(e.Expr)
		case *VariantFieldExpr:
			walkExpr(e.Expr)
		case *CallExpr:
			walkExpr(e.Func)
			for _, arg := range e.Args {
				walkExpr(arg)
			}
		case *MemberExpr:
			walkExpr(e.Expr)
		case *IndexExpr:
			walkExpr(e.Expr)
			walkExpr(e.Index)
		case *ArrayLiteralExpr:
			for _, elem := range e.Elements {
				walkExpr(elem)
			}
		case *StructInitExpr:
			for _, value := range e.Fields {
				walkExpr(value)
			}
		case *FnExpr:
			if e.Body != nil {
				walkStmt(e.Body)
			}
		case *MatchExpr:
			walkStmt(e.Match)
		case *RangeExpr:
			walkExpr(e.Low)
			walkExpr(e.High)
		case *BlockExpr:
			for _, inner := range e.Stmts {
				walkStmt(inner)
			}
			walkExpr(e.Value)
		case *CondExpr:
			walkExpr(e.Cond)
			walkExpr(e.Then)
			walkExpr(e.Else)
		}
	}

	walkStmt = func(stmt Stmt) {
		if block, ok := stmt.(*BlockStmt); ok && block == nil {
			return
		}
		if stmt == nil {
			return
		}
		visitStmt(stmt)

		switch s := stmt.(type) {
		case *BlockStmt:
			for _, inner := range s.Stmts {
				walkStmt(inner)
			}
		case *LetStmt:
			walkExpr(s.Value)
		case *ReturnStmt:
			walkExpr(s.Value)
		case *ExprStmt:
			walkExpr(s.Expr)
		case *IfStmt:
			walkExpr(s.Cond)
			walkStmt(s.Then)
			if s.Else != nil {
				walkStmt(s.Else)
			}
		case *WhileStmt:
			walkExpr(s.Cond)
			walkStmt(s.Body)
			if s.Update != nil {
				walkStmt(s.Update)
			}
		case *ForStmt:
			walkExpr(s.Iterable)
			walkStmt(s.Body)
		case *DeferStmt:
			walkStmt(s.Body)
		case *MatchStmt:
			walkExpr(s.Cond)
			for _, matchCase := range s.Cases {
				for _, pattern := range matchCase.Patterns() {
					walkPattern(pattern)
				}
				walkExpr(matchCase.Guard)
				switch body := matchCase.Body.(type) {
				case *BlockStmt:
					walkStmt(body)
				case Expr:
					walkExpr(body)
				}
			}
		}
	}

	switch n := node.(type) {
	case Stmt:
		walkStmt(n)
	case Expr:
		walkExpr(n)
	}
}
