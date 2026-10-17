package analysis

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// buildCaptureGraph traverses the AST and identifies captures
func (a *Analyzer) buildCaptureGraph(prog *ast.Program) {
	for _, decl := range prog.Decls {
		a.visitDecl(decl)
	}
}

func (a *Analyzer) visitDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		if d.Body != nil {
			a.visitFunction(d, d.Body, d.Params)
		}
	case *ast.StructDecl, *ast.EnumDecl, *ast.TraitDecl:
		// No closure analysis needed for type declarations
	case *ast.ImplDecl:
		for _, method := range d.Methods {
			if method.Body != nil {
				// Methods receive the implicit `self` as a local
				a.visitFunction(method, method.Body, method.Params, "self")
			}
		}
	}
}

func (a *Analyzer) visitFunction(fn ast.Node, body *ast.BlockStmt, params []*ast.Param, implicitLocals ...string) {
	// Create new scope for this function
	scope := &functionScope{
		function: fn,
		locals:   make(map[string]*resolve.Symbol),
		depth:    len(a.scopeStack),
	}

	// Register parameters as locals
	fnScope := a.resolveTable.Scopes[fn]
	for _, param := range params {
		var sym *resolve.Symbol
		if fnScope != nil {
			sym = fnScope.Lookup(param.Name)
		}
		if sym == nil {
			sym = &resolve.Symbol{Name: param.Name, Kind: resolve.SymbolVar, Decl: param}
		}
		scope.locals[param.Name] = sym
	}
	for _, name := range implicitLocals {
		scope.locals[name] = &resolve.Symbol{Name: name, Kind: resolve.SymbolVar, Decl: fn}
	}

	// Push function onto stack
	a.funcStack = append(a.funcStack, fn)
	a.scopeStack = append(a.scopeStack, scope)

	// Create capture entry
	a.info.Captures[fn] = &FunctionCaptures{
		Function:     fn,
		CapturedVars: make([]*CapturedVar, 0),
		Depth:        scope.depth,
		Parent:       a.getParentFunction(),
	}

	// Visit body
	a.visitStmt(body, scope)

	// Pop function from stack
	a.funcStack = a.funcStack[:len(a.funcStack)-1]
	a.scopeStack = a.scopeStack[:len(a.scopeStack)-1]
}

func (a *Analyzer) visitStmt(stmt ast.Stmt, scope *functionScope) {
	if stmt == nil {
		return
	}

	switch s := stmt.(type) {
	case *ast.BlockStmt:
		defer scope.enterBlock()()
		for _, st := range s.Stmts {
			a.visitStmt(st, scope)
		}
	case *ast.LetStmt:
		if fn, ok := s.Value.(*ast.FnExpr); ok && !fn.Short && !s.IsConst {
			// A function written with its types sees its own name, which
			// it then captures
			scope.locals[s.Name] = &resolve.Symbol{Name: s.Name, Kind: resolve.SymbolVar, Decl: s}
			a.visitExpr(s.Value, scope)
			break
		}
		// The initializer is evaluated before the name comes into scope
		if s.Value != nil {
			a.visitExpr(s.Value, scope)
		}
		scope.locals[s.Name] = &resolve.Symbol{Name: s.Name, Kind: resolve.SymbolVar, Decl: s}
	case *ast.ReturnStmt:
		if s.Value != nil {
			a.visitExpr(s.Value, scope)
		}
	case *ast.IfStmt:
		a.visitExpr(s.Cond, scope)
		a.visitStmt(s.Then, scope)
		if s.Else != nil {
			a.visitStmt(s.Else, scope)
		}
	case *ast.WhileStmt:
		a.visitExpr(s.Cond, scope)
		a.visitStmt(s.Body, scope)
		if s.Update != nil {
			a.visitStmt(s.Update, scope)
		}
	case *ast.ForStmt:
		// For-in loop: for x in iterable { ... }
		// Loop variable is local, to the loop
		a.visitExpr(s.Iterable, scope)
		defer scope.enterBlock()()
		scope.locals[s.Var] = &resolve.Symbol{Name: s.Var, Kind: resolve.SymbolVar, Decl: s}
		if s.Index != nil {
			scope.locals[s.Index.Name] = &resolve.Symbol{Name: s.Index.Name, Kind: resolve.SymbolVar, Decl: s.Index}
		}
		a.visitStmt(s.Body, scope)
	case *ast.MatchStmt:
		a.visitExpr(s.Cond, scope)
		for _, matchCase := range s.Cases {
			// Pattern variables are local to the arm
			if body, ok := matchCase.Body.(*ast.BlockStmt); ok {
				a.visitStmt(body, scope)
			}
		}
	case *ast.DeferStmt:
		a.visitStmt(s.Body, scope)
	case *ast.ExprStmt:
		a.visitExpr(s.Expr, scope)
	case *ast.BreakStmt, *ast.ContinueStmt:
		// No expressions to visit
	}
}

func (a *Analyzer) visitExpr(expr ast.Expr, scope *functionScope) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *ast.Ident:
		a.checkCapture(e, scope)
	case *ast.BinaryExpr:
		a.visitExpr(e.Left, scope)
		if holder := ast.SafeHolder(e); holder != nil {
			// The name the right side of `?.` gives the value is a local
			scope.locals[holder.Name] = &resolve.Symbol{Name: holder.Name, Kind: resolve.SymbolVar, Decl: e}
		}
		a.visitExpr(e.Right, scope)
	case *ast.UnaryExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.CastExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.TypeTestExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.VariantExpr:
		// The values of an enum value are expressions like any others
		for _, arg := range e.Args {
			a.visitExpr(arg, scope)
		}
	case *ast.VariantTestExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.VariantFieldExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.CallExpr:
		a.visitExpr(e.Func, scope)
		for _, arg := range e.Args {
			a.visitExpr(arg, scope)
		}
	case *ast.IndexExpr:
		a.visitExpr(e.Expr, scope)
		a.visitExpr(e.Index, scope)
	case *ast.MemberExpr:
		a.visitExpr(e.Expr, scope)
	case *ast.ArrayLiteralExpr:
		for _, elem := range e.Elements {
			a.visitExpr(elem, scope)
		}
	case *ast.StructInitExpr:
		for _, fieldValue := range e.Fields {
			a.visitExpr(fieldValue, scope)
		}
	case *ast.BlockExpr:
		defer scope.enterBlock()()
		for _, st := range e.Stmts {
			a.visitStmt(st, scope)
		}
		a.visitExpr(e.Value, scope)
	case *ast.CondExpr:
		a.visitExpr(e.Cond, scope)
		a.visitExpr(e.Then, scope)
		a.visitExpr(e.Else, scope)
	case *ast.FnExpr:
		// Nested lambda - recurse
		a.visitFunction(e, e.Body, e.Params)
	case *ast.Literal:
		// No variables to check
	}
}

// enterBlock starts a block of the function: what is declared in it is a
// local until the block ends, and a name it declares again is the outer
// one's after that. It returns what ends the block.
func (s *functionScope) enterBlock() func() {
	outer := make(map[string]*resolve.Symbol, len(s.locals))
	for name, sym := range s.locals {
		outer[name] = sym
	}
	return func() { s.locals = outer }
}

// checkCapture determines if an identifier is a captured variable
func (a *Analyzer) checkCapture(ident *ast.Ident, scope *functionScope) {
	// Check if this is a local variable in current scope
	if _, isLocal := scope.locals[ident.Name]; isLocal {
		return // Not a capture
	}

	// Look up in resolve table
	sym := a.resolveTable.Defs[ident]
	if sym == nil {
		return // Not resolved (error should be reported by resolver)
	}

	// Only capture variables (not functions, types, etc.)
	if sym.Kind != resolve.SymbolVar {
		return
	}

	// Find the enclosing function that declares the variable
	for i := len(a.scopeStack) - 2; i >= 0; i-- {
		if _, found := a.scopeStack[i].locals[ident.Name]; !found {
			continue
		}

		// Every function between the declaring one and the current one has
		// to carry the variable: the first takes it from the declaring
		// function's locals, the deeper ones from their parent's upvalues.
		for j := i + 1; j < len(a.scopeStack); j++ {
			fn := a.scopeStack[j].function
			if a.captures(fn, ident.Name) {
				continue
			}
			a.info.AddCapture(fn, &CapturedVar{
				Symbol:        sym,
				Name:          ident.Name,
				ScopeDistance: j - i,
				FromUpvalue:   j > i+1,
				Decl:          sym.Decl,
			})
		}
		return
	}
}

// captures reports whether fn already captures the named variable.
func (a *Analyzer) captures(fn ast.Node, name string) bool {
	for _, cv := range a.info.Captures[fn].CapturedVars {
		if cv.Name == name {
			return true
		}
	}
	return false
}
