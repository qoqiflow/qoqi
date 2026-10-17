package types

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// Flow typing: inside the region guarded by a test, a variable or a field
// has a more precise type than the one it was declared with.
//
//	if user != null { user.name }          // user: User
//	if user == null { return; }  user.name // user: User from here on
//	if user == null { user = guest; }  user.name   // every way through gives a User
//	while node != null { node = node.next; }
//	if node.next != null { node.next.value }
//	if value is Int { value + 1 }
//
// What is known about a local variable or parameter ends where it is
// assigned. A variable that a lambda assigns is not narrowed at all (see
// findSharedVariables). Inside a function expression, which may run at
// any later time, only what is known about variables that are never
// assigned after their declaration carries over.
//
// A field path such as `node.next` can be narrowed too, but a field can be
// changed through any other reference to the same value. What is known
// about fields therefore ends at the next thing that could do that: an
// assignment to any field, or a call of something that may assign one (see
// findFieldWriters).

// facts is a set of things learned from a condition.
type facts struct {
	vars  map[*resolve.Symbol]Type
	paths map[string]pathFact
}

// pathFact is the narrowed type of a field path rooted at a variable.
type pathFact struct {
	root *resolve.Symbol
	typ  Type
}

func newFacts() facts {
	return facts{vars: make(map[*resolve.Symbol]Type), paths: make(map[string]pathFact)}
}

// flowState is a snapshot of what is known at a point.
type flowState struct {
	facts  facts
	writes map[*resolve.Symbol]int
	epoch  int // value of pathEpoch: changes whenever field facts are dropped
}

// flowSnapshot records the current facts and assignment counts.
func (c *Checker) flowSnapshot() flowState {
	state := flowState{
		facts:  newFacts(),
		writes: make(map[*resolve.Symbol]int, len(c.writes)),
		epoch:  c.pathEpoch,
	}
	for sym, typ := range c.narrowed {
		state.facts.vars[sym] = typ
	}
	for key, fact := range c.narrowedPaths {
		state.facts.paths[key] = fact
	}
	for sym, n := range c.writes {
		state.writes[sym] = n
	}
	return state
}

// flowRestore returns to a snapshot when leaving a block or branch. What was
// known before still holds, except for variables assigned in between, and
// for fields if anything in between could have changed one.
func (c *Checker) flowRestore(state flowState) {
	c.narrowed = make(map[*resolve.Symbol]Type, len(state.facts.vars))
	for sym, typ := range state.facts.vars {
		if !c.writtenSince(state, sym) {
			c.narrowed[sym] = typ
		}
	}

	c.narrowedPaths = make(map[string]pathFact, len(state.facts.paths))
	if c.pathEpoch == state.epoch {
		for key, fact := range state.facts.paths {
			if !c.writtenSince(state, fact.root) {
				c.narrowedPaths[key] = fact
			}
		}
	}
}

func (c *Checker) writtenSince(state flowState, sym *resolve.Symbol) bool {
	return c.writes[sym] != state.writes[sym]
}

// narrow adds facts that hold from here to the end of the enclosing block.
func (c *Checker) narrow(known facts) {
	for sym, typ := range known.vars {
		c.narrowed[sym] = typ
	}
	for key, fact := range known.paths {
		c.narrowedPaths[key] = fact
	}
}

// narrowAfter adds the facts that still hold after code that started at
// the given snapshot: those about variables not assigned since, and those
// about fields if nothing since could have changed a field.
func (c *Checker) narrowAfter(known facts, since flowState) {
	for sym, typ := range known.vars {
		if !c.writtenSince(since, sym) {
			c.narrowed[sym] = typ
		}
	}
	if c.pathEpoch != since.epoch {
		return
	}
	for key, fact := range known.paths {
		if !c.writtenSince(since, fact.root) {
			c.narrowedPaths[key] = fact
		}
	}
}

// withBranches checks the alternatives of a statement of which only one
// runs: the two sides of an `if`, the cases of a `match`. Each one starts
// from what was known before the statement, plus its own facts. What an
// earlier alternative assigned does not concern a later one, since the
// later one only runs when the earlier one did not.
//
// Afterwards, what was known before still holds, except for whatever any
// of them assigned.
func (c *Checker) withBranches(count int, facts func(i int) facts, check func(i int)) {
	before := c.flowSnapshot()
	for i := 0; i < count; i++ {
		if i > 0 {
			c.flowReset(before)
		}
		c.narrow(facts(i))
		check(i)
	}
	c.flowRestore(before)
}

// flowReset brings back the facts of a snapshot as they were, including
// those about variables assigned since. Unlike flowRestore it is for going
// back to a point the code about to be checked is reached from directly.
func (c *Checker) flowReset(state flowState) {
	c.narrowed = make(map[*resolve.Symbol]Type, len(state.facts.vars))
	for sym, typ := range state.facts.vars {
		c.narrowed[sym] = typ
	}
	c.narrowedPaths = make(map[string]pathFact, len(state.facts.paths))
	for key, fact := range state.facts.paths {
		c.narrowedPaths[key] = fact
	}
}

// withNarrowing runs check with extra facts that only hold inside it.
func (c *Checker) withNarrowing(known facts, check func()) {
	state := c.flowSnapshot()
	c.narrow(known)
	check()
	c.flowRestore(state)
}

// recordWrite notes an assignment to a variable, which ends what is known
// about it and about the fields reached through it.
func (c *Checker) recordWrite(sym *resolve.Symbol) {
	c.writes[sym]++
	delete(c.narrowed, sym)
	for key, fact := range c.narrowedPaths {
		if fact.root == sym {
			delete(c.narrowedPaths, key)
		}
	}
}

// forgetFields ends everything known about fields. It is called where a
// field may have been changed through a reference the checker cannot see:
// after an assignment to a field, and after a call that may assign one.
func (c *Checker) forgetFields() {
	if len(c.narrowedPaths) > 0 {
		c.narrowedPaths = make(map[string]pathFact)
	}
	c.pathEpoch++
}

// fieldPath identifies a field path such as `a.b.c` that starts at a local
// variable. It returns a key that is the same for every occurrence of the
// path, and the variable it starts at.
func (c *Checker) fieldPath(expr ast.Expr) (string, *resolve.Symbol, bool) {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return "", nil, false
	}

	names := []string{member.Name}
	inner := member.Expr
	for {
		next, ok := inner.(*ast.MemberExpr)
		if !ok {
			break
		}
		names = append(names, next.Name)
		inner = next.Expr
	}

	ident, ok := inner.(*ast.Ident)
	if !ok {
		return "", nil, false
	}
	sym := c.table.Defs[ident]
	if sym == nil || sym.Kind != resolve.SymbolVar || c.shared[sym] {
		return "", nil, false
	}

	key := fmt.Sprintf("%p", sym)
	for i := len(names) - 1; i >= 0; i-- {
		key += "." + names[i]
	}
	return key, sym, true
}

// forgetAssignedIn ends the narrowing of every variable assigned somewhere
// in a loop body or a lambda: such an assignment may run before code that
// comes earlier in the text.
func (c *Checker) forgetAssignedIn(node ast.Node) {
	if c.mayWriteFields(node) {
		c.forgetFields()
	}
	walkExprs(node, func(expr ast.Expr) {
		if sym := c.assignedVariable(expr); sym != nil {
			c.recordWrite(sym)
		}
	})
}

// assignedVariable returns the variable an expression assigns to, if it is
// an assignment to a plain variable.
func (c *Checker) assignedVariable(expr ast.Expr) *resolve.Symbol {
	assign, ok := expr.(*ast.BinaryExpr)
	if !ok || !ast.IsAssignment(assign.Op) {
		return nil
	}
	ident, ok := assign.Left.(*ast.Ident)
	if !ok {
		return nil
	}
	if sym := c.table.Defs[ident]; sym != nil && sym.Kind == resolve.SymbolVar {
		return sym
	}
	return nil
}

// findFieldWriters works out which functions and methods may assign a field
// when called, directly or through what they call. A call of any other
// function cannot change a field, so what a null test established about
// `node.next` still holds after it.
//
// The result errs on the side of "may write": calling a function value or a
// function stored in a field counts as writing, because the target is not
// known, and a method call counts if any method of that name may write.
func (c *Checker) findFieldWriters() {
	var decls []*ast.FnDecl
	for _, decl := range c.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			decls = append(decls, d)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				decls = append(decls, method)
				c.methodsNamed[method.Name] = append(c.methodsNamed[method.Name], method)
			}
		case *ast.StructDecl:
			for _, field := range d.Fields {
				c.fieldNames[field.Name] = true
			}
		}
	}

	// A function writes if its body does, or calls one that does. Repeat
	// until nothing changes, so that callers of writers become writers.
	for changed := true; changed; {
		changed = false
		for _, decl := range decls {
			if !c.fieldWriters[decl] && decl.Body != nil && c.mayWriteFields(decl.Body) {
				c.fieldWriters[decl] = true
				changed = true
			}
		}
	}
}

// mayWriteFields reports whether running a piece of code may assign a
// field: it contains such an assignment, or a call that may make one.
func (c *Checker) mayWriteFields(node ast.Node) bool {
	writes := false
	walkExprs(node, func(expr ast.Expr) {
		switch e := expr.(type) {
		case *ast.BinaryExpr:
			if _, toField := e.Left.(*ast.MemberExpr); toField && ast.IsAssignment(e.Op) {
				writes = true
			}
		case *ast.CallExpr:
			if c.callMayWriteFields(e) {
				writes = true
			}
		}
	})
	return writes
}

// callMayWriteFields reports whether a call may assign a field.
func (c *Checker) callMayWriteFields(e *ast.CallExpr) bool {
	switch callee := e.Func.(type) {
	case *ast.Ident:
		// A function called by its name: known. Builtins have no body and
		// assign nothing.
		if sym := c.table.Defs[callee]; sym != nil && sym.Kind == resolve.SymbolFunc {
			if decl, ok := sym.Decl.(*ast.FnDecl); ok {
				return c.fieldWriters[decl]
			}
		}

	case *ast.MemberExpr:
		// A method: any method of that name may be the one that runs. If
		// the name is also a field, it may be a function stored in it.
		methods := c.methodsNamed[callee.Name]
		if len(methods) == 0 || c.fieldNames[callee.Name] {
			return true
		}
		for _, method := range methods {
			if c.fieldWriters[method] {
				return true
			}
		}
		return false
	}

	// A function value: the target is not known
	return true
}

// findSharedVariables records the variables that a function expression
// assigns but does not own: they belong to an enclosing function. Such a
// variable can change whenever the lambda is called, which may be in the
// middle of a region where a null test seemed to settle its type, so it is
// never narrowed.
func (c *Checker) findSharedVariables(body *ast.BlockStmt) {
	if body == nil {
		return
	}

	// Also noted here: every variable assigned at all, anywhere in the
	// function. The others keep, inside a function expression, what was
	// known about them where it is written (see checkFnDecl).
	walkExprs(body, func(expr ast.Expr) {
		if sym := c.assignedVariable(expr); sym != nil {
			c.reassigned[sym] = true
		}
	})

	walkExprs(body, func(expr ast.Expr) {
		lambda, ok := expr.(*ast.FnExpr)
		if !ok || lambda.Body == nil {
			return
		}

		// Everything declared by the lambda itself is its own
		own := make(map[ast.Node]bool)
		for _, param := range lambda.Params {
			own[param] = true
		}
		walk(lambda.Body, func(ast.Expr) {}, func(stmt ast.Stmt) {
			switch s := stmt.(type) {
			case *ast.LetStmt:
				own[s] = true
			case *ast.ForStmt:
				own[s] = true
				if s.Index != nil {
					own[s.Index] = true
				}
			}
		})

		walkExprs(lambda.Body, func(inner ast.Expr) {
			if sym := c.assignedVariable(inner); sym != nil && !own[sym.Decl] {
				c.shared[sym] = true
			}
		})
	})
}

// factsFrom returns what is known when cond evaluates to outcome.
func (c *Checker) factsFrom(cond ast.Expr, outcome bool) facts {
	known := newFacts()
	c.collectFacts(cond, outcome, known)
	return known
}

func (c *Checker) collectFacts(cond ast.Expr, outcome bool, known facts) {
	switch e := cond.(type) {
	case *ast.UnaryExpr:
		if e.Op == "!" {
			c.collectFacts(e.Expr, !outcome, known)
		}

	case *ast.TypeTestExpr:
		// `x is T` being true makes x a T
		if outcome {
			c.learn(e.Expr, c.info.TestedTypes[e], known)
		}

	case *ast.PatternTestExpr:
		// `x is Shape.Circle(r)` being true makes x a Shape
		if outcome {
			c.learnEnum(e.Expr, c.patternEnumDecl(e.Pattern), known)
		}

	case *ast.VariantTestExpr:
		// The same test once desugaring wrote it out
		if outcome {
			c.learnEnum(e.Expr, c.enums[e.Enum], known)
		}

	case *ast.BlockExpr:
		// Statements, then the condition that decides: what it says holds
		// once the whole was evaluated
		if e.Value != nil {
			c.collectFacts(e.Value, outcome, known)
		}

	case *ast.BinaryExpr:
		switch e.Op {
		case "&&":
			// Both sides are true only when the whole is true
			if outcome {
				c.collectFacts(e.Left, true, known)
				c.collectFacts(e.Right, true, known)
			}
		case "||":
			// Both sides are false only when the whole is false
			if !outcome {
				c.collectFacts(e.Left, false, known)
				c.collectFacts(e.Right, false, known)
			}
		case "==", "!=":
			tested := nullTested(e)
			if tested == nil || outcome != (e.Op == "!=") {
				return
			}
			if typ := c.info.Types[tested]; typ != nil {
				c.learn(tested, withoutNull(typ), known)
			}
		}
	}
}

// learn records that an expression has the given type, if it is something
// that can be narrowed: a local variable or a field path.
func (c *Checker) learn(expr ast.Expr, typ Type, known facts) {
	if typ == nil || isUnknown(typ) {
		return
	}

	if ident, ok := expr.(*ast.Ident); ok {
		sym := c.table.Defs[ident]
		if sym != nil && sym.Kind == resolve.SymbolVar && !c.shared[sym] {
			known.vars[sym] = typ
		}
		return
	}
	if key, root, ok := c.fieldPath(expr); ok {
		known.paths[key] = pathFact{root: root, typ: typ}
	}
}

// learnEnum records that a value which passed the test for a variant is
// of the enum: not null if it could be, and of that enum if it was an Any
// or a trait. For a generic enum reached from an Any nothing says what its
// type arguments are, and nothing is learned.
func (c *Checker) learnEnum(expr ast.Expr, decl *ast.EnumDecl, known facts) {
	subject := c.info.Types[expr]
	if decl == nil || subject == nil || isUnknown(subject) {
		return
	}
	var tested Type
	if subjectDecl, _ := c.enumOfType(withoutNull(subject)); subjectDecl == decl {
		tested = withoutNull(subject)
	} else if len(decl.TypeParams) == 0 {
		tested = enumType(decl, nil)
	}
	if tested != nil && !tested.Equals(subject) {
		c.learn(expr, tested, known)
	}
}

// recordNarrowedNames keeps, for desugaring, what a condition established
// about the names of its own pattern tests.
func (c *Checker) recordNarrowedNames(known facts) {
	for sym, typ := range known.vars {
		if name, ok := sym.Decl.(*ast.BindingPattern); ok {
			c.info.NarrowedNames[name] = typ
		}
	}
}

// nullTested returns x for `x == null`, `null == x` and the != forms.
func nullTested(e *ast.BinaryExpr) ast.Expr {
	isNull := func(expr ast.Expr) bool {
		lit, ok := expr.(*ast.Literal)
		return ok && lit.Kind == "null"
	}

	switch {
	case isNull(e.Right) && !isNull(e.Left):
		return e.Left
	case isNull(e.Left) && !isNull(e.Right):
		return e.Right
	}
	return nil
}

// --- Reachability ---

// exits reports whether control never continues past a statement: it ends
// in return, break or continue on every path.
func exits(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		return isPanic(s)
	case *ast.BlockStmt:
		for _, inner := range s.Stmts {
			if exits(inner) {
				return true
			}
		}
	case *ast.IfStmt:
		return s.Else != nil && exits(s.Then) && exits(s.Else)
	}
	return false
}

// alwaysReturns reports whether every path through a statement ends in a
// return, so that nothing after it can run.
func alwaysReturns(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		// Nothing comes after a panic: the function does not reach its end
		return isPanic(s)
	case *ast.BlockStmt:
		if s == nil {
			return false
		}
		for _, inner := range s.Stmts {
			if alwaysReturns(inner) {
				return true
			}
		}
	case *ast.IfStmt:
		return s.Else != nil && alwaysReturns(s.Then) && alwaysReturns(s.Else)
	case *ast.WhileStmt:
		// `while true` without a break only ends by returning
		lit, ok := s.Cond.(*ast.Literal)
		return ok && lit.Kind == "bool" && lit.Value == "true" && !breaksOut(s.Body)
	case *ast.MatchStmt:
		hasDefault := false
		for _, matchCase := range s.Cases {
			if matchCase.Pattern == nil {
				hasDefault = true
			}
			body, ok := matchCase.Body.(*ast.BlockStmt)
			if !ok || !alwaysReturns(body) {
				return false
			}
		}
		// A match on an enum that handles every variant runs one of its
		// cases as surely as one with a default
		return hasDefault || s.Exhaustive
	}
	return false
}

// breaksOut reports whether a loop body contains a break that leaves that
// loop (breaks of nested loops do not count).
func breaksOut(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.BreakStmt:
		return true
	case *ast.BlockStmt:
		for _, inner := range s.Stmts {
			if breaksOut(inner) {
				return true
			}
		}
	case *ast.IfStmt:
		if breaksOut(s.Then) {
			return true
		}
		return s.Else != nil && breaksOut(s.Else)
	case *ast.MatchStmt:
		for _, matchCase := range s.Cases {
			if body, ok := matchCase.Body.(*ast.BlockStmt); ok && breaksOut(body) {
				return true
			}
		}
	}
	return false
}

// --- AST walking ---

// walkExprs calls visit for every expression under a node, including the
// bodies of nested function expressions.
func walkExprs(node ast.Node, visit func(ast.Expr)) {
	walk(node, visit, func(ast.Stmt) {})
}

// walk calls the visitors for every expression and statement under a node,
// including the bodies of nested function expressions.
func walk(node ast.Node, visitExpr func(ast.Expr), visitStmt func(ast.Stmt)) {
	ast.Walk(node, visitExpr, visitStmt)
}

// isPanic reports whether a statement is a call of the builtin `panic`,
// which stops the program: what follows it never runs, and a function
// that ends with it does not need to return a value.
func isPanic(stmt *ast.ExprStmt) bool {
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	callee, ok := call.Func.(*ast.Ident)
	return ok && callee.Name == "panic"
}

// WalkExprs calls visit for every expression under a node, including the
// bodies of nested function expressions. For tools that look a program
// over once it is checked.
func WalkExprs(node ast.Node, visit func(ast.Expr)) {
	walkExprs(node, visit)
}

// WalkStmts calls visit for every statement under a node.
func WalkStmts(node ast.Node, visit func(ast.Stmt)) {
	walk(node, func(ast.Expr) {}, visit)
}
