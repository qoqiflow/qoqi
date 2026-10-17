package desugar

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// A pattern test, `value is Pattern`, is written here as what a case of a
// match on the value would be: the tests that say whether the value fits,
// and a variable for each name of the pattern (see lowerPattern).
//
//	if shape is Shape.Rect(w, 0.0) { body } else { other }
//
// becomes
//
//	if shape is Shape.Rect && shape.Rect#1 == 0.0 { let w = shape.Rect#0; { body } } else { other }
//
// In a chain of `&&`, what stands after a test sees its names. The chain
// is regrouped so that all of it does, which changes nothing to the order
// things are evaluated in, and the names are declared a second time for
// the block:
//
//	if shape is Shape.Circle(r) && r > 1.0 && ready { body }
//
//	if shape is Shape.Circle && { let r = shape.Circle#0; r > 1.0 && ready } {
//	    let r = shape.Circle#0;
//	    { body }
//	}
//
// A value that is not a plain variable is evaluated once, into a
// temporary declared before the statement. That is where it is evaluated
// too when the test comes first; behind an `&&` it is only evaluated when
// its turn comes, so the temporary starts empty and is filled there:
//
//	if ready && next() is Option.Some(item) { body }
//
//	{
//	    let __is_1: Option<Int>? = null;
//	    if ready && { __is_1 = next(); __is_1 is Option.Some } { let item = __is_1.Some#0; { body } }
//	}
//
// A `while` whose condition needs such a temporary evaluates it on every
// turn:
//
//	while queue.take() is Option.Some(job) { body }
//
//	while true {
//	    let __is_1 = queue.take();
//	    if __is_1 is Option.Some { let job = __is_1.Some#0; { body } } else { break; }
//	}

// loweredCondition is a condition with its pattern tests written out.
type loweredCondition struct {
	// before declares the temporaries; it runs ahead of the condition
	before []ast.Stmt
	// test is the condition itself
	test ast.Expr
	// names declares what the patterns name, for what the condition guards
	names []ast.Stmt
}

// hasPatternTest reports whether a condition holds a pattern test whose
// names reach past it: the condition is one, or a chain of `&&` with one.
func hasPatternTest(cond ast.Expr) bool {
	return len(ast.PatternTests(cond)) > 0
}

// lowerCondition writes out a condition that hasPatternTest.
func (d *Desugarer) lowerCondition(cond ast.Expr) loweredCondition {
	var lowered loweredCondition
	pos := cond.Position()

	// What each part of the chain becomes: its test, what has to run just
	// before it, and the names it leaves to the parts after it
	type part struct {
		fill  ast.Stmt
		test  ast.Expr
		names []ast.Stmt
	}
	conjuncts := ast.Conjuncts(cond)
	parts := make([]part, len(conjuncts))

	for i, conjunct := range conjuncts {
		test, ok := conjunct.(*ast.PatternTestExpr)
		if !ok {
			parts[i].test = d.desugarExpr(conjunct)
			continue
		}
		bindings := test.Pattern.Bindings()
		subject := d.desugarExpr(test.Expr)

		// A variable is read where it stands, unless it could be another
		// value by the time its names are read: when more of the
		// condition runs in between, or when the pattern reuses its name
		keep := d.isComplexExpr(subject) || (len(bindings) > 0 && i < len(conjuncts)-1)
		if ident, ok := subject.(*ast.Ident); ok {
			for _, binding := range bindings {
				keep = keep || binding.Name == ident.Name
			}
		}
		if keep {
			name := d.generateIsVar()
			tmp := d.createIdent(name, pos)
			if exprType, ok := d.typeInfo.Types[test.Expr]; ok {
				d.typeInfo.Types[tmp] = exprType
			}
			declared := d.inferTypeFromExpr(test.Expr, pos)
			at := ast.BaseNode{Pos: pos, Tok: test.Tok}
			if i == 0 {
				lowered.before = append(lowered.before, &ast.LetStmt{BaseNode: at, Name: name, Type: declared, Value: subject})
			} else {
				lowered.before = append(lowered.before, &ast.LetStmt{
					BaseNode: at,
					Name:     name,
					Type:     optionalOf(declared),
					Value:    &ast.Literal{BaseNode: at, Value: "null", Kind: "null"},
				})
				parts[i].fill = &ast.ExprStmt{
					BaseNode: at,
					Expr:     &ast.BinaryExpr{BaseNode: at, Left: d.createIdent(name, pos), Op: "=", Right: subject},
				}
			}
			subject = tmp
		}

		tests, names := d.lowerPattern(subject, test.Pattern, pos)
		parts[i].test = d.allOf(tests, pos)
		parts[i].names = names

		// The same names again for the guarded block, with what the rest
		// of the condition established about them: after
		// `value is Option.Some(next) && next != null`, next is not null
		for j, stmt := range copyStmts(names) {
			let := stmt.(*ast.LetStmt)
			if j < len(bindings) {
				if narrowed, ok := d.typeInfo.NarrowedNames[bindings[j]]; ok {
					let.Type = d.typeToASTType(narrowed, let.Position())
				}
			}
			lowered.names = append(lowered.names, let)
		}
	}

	// From the last part to the first, each test taking what follows it
	// under the names it declares
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		at := ast.BaseNode{Pos: pos, Tok: conjuncts[i].Token()}
		test := p.test
		if lowered.test != nil {
			rest := lowered.test
			if len(p.names) > 0 {
				rest = &ast.BlockExpr{BaseNode: at, Stmts: p.names, Value: rest}
			}
			test = &ast.BinaryExpr{BaseNode: at, Left: test, Op: "&&", Right: rest}
		}
		if p.fill != nil {
			test = &ast.BlockExpr{BaseNode: at, Stmts: []ast.Stmt{p.fill}, Value: test}
		}
		lowered.test = test
	}
	return lowered
}

// optionalOf makes the type of a temporary that starts as null.
func optionalOf(t *ast.Type) *ast.Type {
	if t == nil || t.Kind == ast.TypeOption {
		return t
	}
	return &ast.Type{BaseNode: t.BaseNode, Kind: ast.TypeOption, ElemType: t}
}

// generateIsVar names the temporary that keeps the value of a pattern test.
func (d *Desugarer) generateIsVar() string {
	name := fmt.Sprintf("__is_%d", d.tmpCounter)
	d.tmpCounter++
	d.info.TmpVars[name] = "pattern test"
	return name
}

// guarded puts the names of a condition in front of the block it guards.
// The block stays a block of its own: it may declare a variable with the
// name of one of them.
func (d *Desugarer) guarded(names []ast.Stmt, block *ast.BlockStmt) *ast.BlockStmt {
	if len(names) == 0 {
		return block
	}
	return d.wrapInBlock(append(names, block), block.Position())
}

// desugarIfPattern desugars an `if` whose condition has pattern tests.
func (d *Desugarer) desugarIfPattern(ifStmt *ast.IfStmt) ast.Stmt {
	cond := d.lowerCondition(ifStmt.Cond)
	lowered := &ast.IfStmt{
		BaseNode: ifStmt.BaseNode,
		Cond:     cond.test,
		Then:     d.guarded(cond.names, d.desugarBlockStmt(ifStmt.Then)),
	}
	if ifStmt.Else != nil {
		lowered.Else = d.desugarStmt(ifStmt.Else)
	}
	if len(cond.before) == 0 {
		return lowered
	}
	return d.wrapInBlock(append(cond.before, lowered), ifStmt.Position())
}

// desugarWhilePattern desugars a `while` whose condition has pattern tests.
func (d *Desugarer) desugarWhilePattern(while *ast.WhileStmt) ast.Stmt {
	cond := d.lowerCondition(while.Cond)
	body := d.guarded(cond.names, d.desugarBlockStmt(while.Body))
	if len(cond.before) == 0 {
		return &ast.WhileStmt{BaseNode: while.BaseNode, Cond: cond.test, Body: body}
	}

	// The temporaries are evaluated again on every turn, `continue`
	// included: it goes back to the top of this loop
	pos := while.Position()
	leave := d.wrapInBlock([]ast.Stmt{&ast.BreakStmt{BaseNode: while.BaseNode}}, pos)
	turn := &ast.IfStmt{BaseNode: while.BaseNode, Cond: cond.test, Then: body, Else: leave}
	return &ast.WhileStmt{
		BaseNode: while.BaseNode,
		Cond:     d.createBoolLiteral(true, pos),
		Body:     d.wrapInBlock(append(cond.before, turn), pos),
	}
}

// desugarPatternValue desugars a pattern test, or a chain of `&&` with
// some, that stands as a value: `let round = shape is Shape.Circle(_);`.
func (d *Desugarer) desugarPatternValue(expr ast.Expr) ast.Expr {
	cond := d.lowerCondition(expr)
	if len(cond.before) == 0 {
		return cond.test
	}
	return &ast.BlockExpr{BaseNode: ast.BaseNode{Pos: expr.Position(), Tok: expr.Token()}, Stmts: cond.before, Value: cond.test}
}
