package desugar

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// desugarMatchStmt transforms a match statement into nested if-else statements
//
// Simple Value Matching:
//
//	match x {
//	  case 1: { action1(); }
//	  case 2: { action2(); }
//	  default: { defaultAction(); }
//	}
//
// Becomes:
//
//	if x == 1 {
//	  action1();
//	} else if x == 2 {
//	  action2();
//	} else {
//	  defaultAction();
//	}
//
// Type Matching:
//
//	match value {
//	  case Int: { handleInt(); }
//	  case String: { handleString(); }
//	}
//
// Becomes:
//
//	{
//	  let __match_tmp_N = value;
//	  if __is_type(__match_tmp_N, Int) {
//	    handleInt();
//	  } else if __is_type(__match_tmp_N, String) {
//	    handleString();
//	  }
//	}
func (d *Desugarer) desugarMatchStmt(match *ast.MatchStmt) ast.Stmt {
	pos := match.Position()

	// If we have no cases, return an empty block
	if len(match.Cases) == 0 {
		d.desugarExpr(match.Cond)
		return d.wrapInBlock([]ast.Stmt{}, pos)
	}

	subject, tmpInit := d.matchSubject(match)

	// Build the if-else chain
	var ifChain ast.Stmt
	for i := len(match.Cases) - 1; i >= 0; i-- {
		caseStmt := match.Cases[i]
		last := i == len(match.Cases)-1
		ifChain = d.buildMatchCase(subject, caseStmt, ifChain, pos, last && d.runsForSure(match, caseStmt))
	}

	if tmpInit != nil {
		block := d.wrapInBlock([]ast.Stmt{tmpInit, ifChain}, pos)
		d.info.Mapping[match] = block
		return block
	}

	d.info.Mapping[match] = ifChain
	return ifChain
}

// matchSubject desugars the value a match looks at, and makes it something
// that can be read once per case: anything but a name or a literal is kept
// in a temporary first, by the statement returned.
//
// A plain variable is tested directly, also for type patterns: the test
// `value is Int` then tells the type checker that `value` is an Int inside
// the case, which a copy in a temporary would not.
func (d *Desugarer) matchSubject(match *ast.MatchStmt) (subject ast.Expr, tmpInit ast.Stmt) {
	pos := match.Position()
	for _, caseStmt := range match.Cases {
		for _, pattern := range caseStmt.Patterns() {
			// Reported here; the other cases are still looked at
			d.validateMatchPattern(pattern)
		}
	}

	subject = d.desugarExpr(match.Cond)
	if !d.isComplexExpr(subject) && !d.rebinds(match, subject) {
		return subject, nil
	}

	// let __match_tmp_N = matched_expr;
	tmpVar := d.generateMatchVar()
	tmpInit = &ast.LetStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Name:     tmpVar,
		Type:     d.inferTypeFromExpr(match.Cond, pos),
		Value:    subject,
	}
	tmp := d.createIdent(tmpVar, pos)
	if exprType, ok := d.typeInfo.Types[match.Cond]; ok {
		d.typeInfo.Types[tmp] = exprType
	}
	return tmp, tmpInit
}

// rebinds reports whether a pattern of the match names a value as the
// variable being matched is named: `match pair { case (pair, rest): ... }`.
// The values of the pattern are then read from a copy, since the name
// stops meaning the matched value as soon as the first of them is declared.
func (d *Desugarer) rebinds(match *ast.MatchStmt, subject ast.Expr) bool {
	ident, ok := subject.(*ast.Ident)
	if !ok {
		return false
	}
	for _, caseStmt := range match.Cases {
		for _, pattern := range caseStmt.Patterns() {
			var bindings []*ast.BindingPattern
			switch p := pattern.(type) {
			case *ast.VariantPattern:
				bindings = p.Bindings()
			case *ast.StructPattern:
				bindings = p.Bindings()
			}
			for _, binding := range bindings {
				if binding.Name == ident.Name {
					return true
				}
			}
		}
	}
	return false
}

// runsForSure reports whether the last case of a match needs no test:
// when the cases cover every variant of an enum, nothing else is left.
// Written as the final `else`, it also shows the stages below that one of
// the cases always runs.
func (d *Desugarer) runsForSure(match *ast.MatchStmt, last *ast.MatchCase) bool {
	if !match.Exhaustive || last.Guard != nil || last.IsDefault() {
		return false
	}
	for _, pattern := range last.Patterns() {
		if _, ok := pattern.(*ast.VariantPattern); !ok {
			return false
		}
	}
	return true
}

// caseTest builds what decides whether a case runs, and the variables its
// pattern names, to declare at the start of the case.
//
//	case 1:                          value == 1
//	case 1..10:                      value >= 1 && value < 10
//	case Int:                        value is Int
//	case Shape.Rect(w, 0):           value is Shape.Rect && value.Rect#1 == 0
//	case Light.Red | Light.Amber:    value is Light.Red || value is Light.Amber
//	case Shape.Circle(r) if r > 1.0: value is Shape.Circle && { let r = value.Circle#0; r > 1.0 }
//
// A `default` has no test: nil.
func (d *Desugarer) caseTest(subject ast.Expr, caseStmt *ast.MatchCase, pos token.Position) (test ast.Expr, names []ast.Stmt) {
	if caseStmt.IsDefault() {
		return nil, nil
	}
	at := ast.BaseNode{Pos: pos, Tok: caseStmt.Tok}

	for _, pattern := range caseStmt.Patterns() {
		var one ast.Expr
		switch pattern := pattern.(type) {
		case *ast.VariantPattern:
			// A variant of an enum: test for it, then name its values
			tests, bound := d.lowerPattern(subject, pattern, pos)
			one = d.allOf(tests, pos)
			names = append(names, bound...)

		case *ast.StructPattern:
			// A struct, by its fields
			tests, bound := d.lowerStructPattern(subject, pattern, pos)
			one = d.allOf(tests, pos)
			names = append(names, bound...)

		case *ast.Type:
			// Type matching: matched_expr is Type
			one = d.createTypeCheckCall(subject, pattern, pos)

		case *ast.RangeExpr:
			upTo := "<"
			if pattern.Inclusive {
				upTo = "<="
			}
			from := &ast.BinaryExpr{BaseNode: pattern.BaseNode, Left: subject, Op: ">=", Right: d.desugarExpr(pattern.Low)}
			to := &ast.BinaryExpr{BaseNode: pattern.BaseNode, Left: subject, Op: upTo, Right: d.desugarExpr(pattern.High)}
			one = &ast.BinaryExpr{BaseNode: pattern.BaseNode, Left: from, Op: "&&", Right: to}

		case ast.Expr:
			// Value matching: matched_expr == pattern
			one = d.createBinaryExpr(subject, "==", d.desugarExpr(pattern), pos)
			d.typeInfo.Types[one] = types.Bool

		default:
			one = d.createBoolLiteral(true, pos)
		}

		if test == nil {
			test = one
		} else {
			test = &ast.BinaryExpr{BaseNode: at, Left: test, Op: "||", Right: one}
		}
	}

	if len(caseStmt.Alternatives) > 0 && len(names) > 0 {
		// case Add(x, _) | Sub(x, _): each name is the value of whichever
		// pattern matched
		names = d.sharedNames(subject, caseStmt.Patterns(), pos)
	}

	if caseStmt.Guard != nil {
		// The condition is asked once the pattern matched, with its names
		guard := d.desugarExpr(caseStmt.Guard)
		if len(names) > 0 {
			guard = &ast.BlockExpr{BaseNode: at, Stmts: copyStmts(names), Value: guard}
		}
		test = &ast.BinaryExpr{BaseNode: at, Left: test, Op: "&&", Right: guard}
	}
	return test, names
}

// sharedNames declares the names that every pattern of a case gives, each
// the value of the first pattern that matched:
//
//	case Add(x, _) | Sub(x, _):    let x = value is Add ? value.Add#0 : value.Sub#0;
//
// The last pattern is not tested: the case only runs when one matched.
func (d *Desugarer) sharedNames(subject ast.Expr, patterns []ast.Node, pos token.Position) []ast.Stmt {
	lower := func(pattern ast.Node) (tests []ast.Expr, lets []ast.Stmt, ok bool) {
		switch p := pattern.(type) {
		case *ast.VariantPattern:
			tests, lets = d.lowerPattern(subject, p, pos)
			return tests, lets, true
		case *ast.StructPattern:
			tests, lets = d.lowerStructPattern(subject, p, pos)
			return tests, lets, true
		}
		return nil, nil, false
	}
	var first []*ast.BindingPattern
	switch p := patterns[0].(type) {
	case *ast.VariantPattern:
		first = p.Bindings()
	case *ast.StructPattern:
		first = p.Bindings()
	}
	var out []ast.Stmt
	for _, binding := range first {
		var value ast.Expr
		for i := len(patterns) - 1; i >= 0; i-- {
			tests, lets, ok := lower(patterns[i])
			if !ok {
				continue
			}
			var given ast.Expr
			for _, let := range lets {
				if let, ok := let.(*ast.LetStmt); ok && let.Name == binding.Name {
					given = let.Value
				}
			}
			if given == nil {
				continue
			}
			if value == nil {
				value = given
				continue
			}
			value = &ast.CondExpr{BaseNode: binding.BaseNode, Cond: d.allOf(tests, pos), Then: given, Else: value}
		}
		out = append(out, &ast.LetStmt{BaseNode: binding.BaseNode, Name: binding.Name, Value: value})
	}
	return out
}

// copyStmts copies statements that are needed in two places of the
// program: the stages below take each node to stand in one place.
func copyStmts(stmts []ast.Stmt) []ast.Stmt {
	cloner := &ast.Cloner{Types: make(map[string]*ast.Type)}
	out := make([]ast.Stmt, len(stmts))
	for i, stmt := range stmts {
		out[i] = cloner.Stmt(stmt)
	}
	return out
}

// buildMatchCase builds a single case in the if-else chain
func (d *Desugarer) buildMatchCase(matchedExpr ast.Expr, caseStmt *ast.MatchCase, elseStmt ast.Stmt, pos token.Position, unconditional bool) ast.Stmt {
	// Desugar the case body
	var caseBody *ast.BlockStmt
	switch body := caseStmt.Body.(type) {
	case *ast.BlockStmt:
		caseBody = d.desugarStmt(body).(*ast.BlockStmt)
	case ast.Expr:
		// Convert expression to expression statement in block
		exprStmt := &ast.ExprStmt{
			BaseNode: ast.BaseNode{Pos: body.Position()},
			Expr:     d.desugarExpr(body),
		}
		caseBody = d.wrapInBlock([]ast.Stmt{exprStmt}, body.Position())
	case ast.Stmt:
		caseBody = d.wrapInBlock([]ast.Stmt{d.desugarStmt(body)}, body.Position())
	default:
		caseBody = d.wrapInBlock([]ast.Stmt{}, pos)
	}

	condition, names := d.caseTest(matchedExpr, caseStmt, pos)
	if len(names) > 0 {
		caseBody = d.wrapInBlock(append(names, caseBody.Stmts...), caseBody.Position())
	}
	if unconditional {
		return caseBody
	}
	if condition == nil {
		// Default case - always true
		condition = d.createBoolLiteral(true, pos)
		d.typeInfo.Types[condition] = types.Bool
	}

	return &ast.IfStmt{
		BaseNode: ast.BaseNode{Pos: pos},
		Cond:     condition,
		Then:     caseBody,
		Else:     elseStmt,
	}
}

// desugarMatchExpr transforms a match that gives a value into a chain of
// choices between the values of its cases:
//
//	let size = match shape {
//	    case Shape.Circle(r) if r > 10.0: "big";
//	    case Shape.Circle(_): "round";
//	    default: { return null; }
//	};
//
// becomes
//
//	let size = if shape is Shape.Circle && { let r = shape.Circle#0; r > 10.0 } { let r = shape.Circle#0; "big" }
//	           else if shape is Shape.Circle "round"
//	           else { return null; };
//
// written with the two expressions only desugaring makes: a choice
// (CondExpr) and statements that end in a value (BlockExpr). A case that
// is a block always leaves, the type checker saw to it: it gives none.
func (d *Desugarer) desugarMatchExpr(expr *ast.MatchExpr) ast.Expr {
	match := expr.Match
	pos := match.Position()
	subject, tmpInit := d.matchSubject(match)

	// What the last `else` gives when no case is left to run. A complete
	// match never gets there.
	var chain ast.Expr = &ast.Literal{BaseNode: expr.BaseNode, Value: "null", Kind: "null"}

	for i := len(match.Cases) - 1; i >= 0; i-- {
		caseStmt := match.Cases[i]
		at := ast.BaseNode{Pos: caseStmt.Position(), Tok: caseStmt.Tok}
		test, names := d.caseTest(subject, caseStmt, pos)

		var value ast.Expr
		switch body := caseStmt.Body.(type) {
		case *ast.BlockStmt:
			value = &ast.BlockExpr{BaseNode: at, Stmts: append(names, d.desugarBlockStmt(body).Stmts...)}
		case ast.Expr:
			value = d.desugarExpr(body)
			if len(names) > 0 {
				value = &ast.BlockExpr{BaseNode: at, Stmts: names, Value: value}
			}
		}

		last := i == len(match.Cases)-1
		if test == nil || (last && d.runsForSure(match, caseStmt)) {
			chain = value
			continue
		}
		chain = &ast.CondExpr{BaseNode: at, Cond: test, Then: value, Else: chain}
	}

	if tmpInit != nil {
		return &ast.BlockExpr{BaseNode: expr.BaseNode, Stmts: []ast.Stmt{tmpInit}, Value: chain}
	}
	return chain
}

// createTypeCheckCall creates the test `expr is Type` for a type pattern
func (d *Desugarer) createTypeCheckCall(expr ast.Expr, typ *ast.Type, pos token.Position) ast.Expr {
	test := &ast.TypeTestExpr{
		BaseNode: ast.BaseNode{Pos: pos, Tok: typ.Tok},
		Expr:     expr,
		Type:     typ,
	}

	d.typeInfo.Types[test] = types.Bool
	return test
}

// isComplexExpr determines if an expression is complex enough to need a temporary variable
func (d *Desugarer) isComplexExpr(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident, *ast.Literal:
		return false
	default:
		return true
	}
}

// hasTypeOrEnumMatching checks if any cases use type or enum matching
func (d *Desugarer) hasTypeOrEnumMatching(cases []*ast.MatchCase) bool {
	for _, c := range cases {
		if _, isType := c.Pattern.(*ast.Type); isType {
			return true
		}
		// Check for enum variant matching (CallExpr pattern)
		if _, isCall := c.Pattern.(*ast.CallExpr); isCall {
			return true
		}
	}
	return false
}

// inferTypeFromExpr infers the AST type from an expression
func (d *Desugarer) inferTypeFromExpr(expr ast.Expr, pos token.Position) *ast.Type {
	if exprType, ok := d.typeInfo.Types[expr]; ok {
		return d.typeToASTType(exprType, pos)
	}

	// Unknown: leave the declaration without an annotation
	return nil
}
