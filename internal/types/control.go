package types

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// --- match ---

func (c *Checker) checkMatchStmt(s *ast.MatchStmt) {
	c.checkMatch(s, false, nil)
}

// checkMatch checks a match: the statement, or the one that gives a value
// (asValue), whose type it returns. expected is the type the value is
// wanted as, when something says so.
//
// A match that gives a value has to give one whatever is matched: its
// cases cover every variant of the enum, or one of them is `default`. And
// each case gives a value of the same type, or leaves.
func (c *Checker) checkMatch(s *ast.MatchStmt, asValue bool, expected Type) Type {
	condType := c.checkExpr(s.Cond)

	// The facts of each case are worked out first, the bodies checked
	// after: only one case runs, so each body starts from what was known
	// before the match, whatever the cases above it assign.
	caseFacts := make([]facts, len(s.Cases))
	covered := make([][]string, len(s.Cases))
	hasDefault := false
	nullTaken := false
	for i, matchCase := range s.Cases {
		known := newFacts()
		caseFacts[i] = known
		hasDefault = hasDefault || matchCase.IsDefault()
		if nullTaken && condType != nil {
			// A case above took null: the value is not null here
			if value := withoutNull(condType); value != nil {
				c.learn(s.Cond, value, known)
			}
		}
		if isNullPattern(matchCase) {
			nullTaken = true
		}

		patterns := matchCase.Patterns()
		covered[i] = make([]string, len(patterns))
		for j, pattern := range patterns {
			switch pattern := pattern.(type) {
			case *ast.VariantPattern:
				// `case Shape.Rect(w, h):` names the values of the variant
				if name, covers := c.checkVariantPattern(pattern, condType); covers {
					covered[i][j] = name
				}
			case *ast.Type:
				// `case Int:` asks what the value is; inside the case it is one
				target := c.resolveType(pattern)
				if condType != nil && !isUnknown(condType) && !isUnknown(target) &&
					!c.assignable(target, condType) && !c.assignable(condType, target) {
					c.errorf(pattern.Token(), ErrInvalidCast, "%s can never be %s", condType, target)
				} else if _, ok := c.castDescriptor(target); !ok {
					c.errorf(pattern.Token(), ErrInvalidCast, "a test for %s cannot be made", target)
				} else if len(patterns) == 1 {
					// With several types, which one it is stays open
					c.learn(s.Cond, target, known)
				}
			case *ast.StructPattern:
				// `case Point { x: 0, y }:` names fields; inside the case
				// the value is that struct
				if st := c.checkStructPattern(pattern, condType); len(patterns) == 1 && !isUnknown(st) {
					c.learn(s.Cond, st, known)
				}
			case *ast.RangeExpr:
				c.checkRangePattern(pattern, condType)
			case ast.Expr:
				patternType := c.checkExpr(pattern)
				if c.patternConst(pattern) != nil && !isUnknown(patternType) && !isScalar(patternType) {
					c.errorfWithSuggestion(pattern.Token(), ErrInvalidPattern,
						"Compare it in a condition: case "+describeTypePattern(condType)+" if value == "+pattern.Token().Literal+":",
						"a constant in a pattern is a String, an Int, a Float or a Bool, and this one is %s", describe(patternType))
					continue
				}
				if condType != nil && patternType != nil && !c.comparable(condType, patternType) {
					c.errorf(pattern.Token(), ErrTypeMismatch, "pattern of type %s can never match a value of type %s", patternType, condType)
				}
			}
		}
	}

	for _, matchCase := range s.Cases {
		if len(matchCase.Alternatives) > 0 {
			c.checkSameNames(matchCase.Patterns())
		}
	}
	c.checkExhaustive(s, withoutNullOrNil(condType), covered)
	if s.Exhaustive && !hasDefault && !nullTaken && condType != nil && nullable(condType) && !isUnknown(condType) {
		// Every variant is handled, and null is not: no case runs for it
		s.Exhaustive = false
		if asValue {
			c.errorfWithSuggestion(s.Token(), ErrNonExhaustiveMatch,
				"Add a case for it: case null: value;",
				"match on %s does not handle null", describe(condType))
			s.Exhaustive = true // Reported once
		}
	}
	if s.WaitsOn {
		c.checkWaitedCases(s, condType, hasDefault, nullTaken)
	}
	if asValue && !hasDefault && !s.Exhaustive {
		c.errorfWithSuggestion(s.Token(), ErrNonExhaustiveMatch,
			"Add a case for what is left: default: value;",
			"a match that gives a value needs a case for every value, and this one has no 'default'")
	}

	// What the cases give: the first type that says something, widened by
	// the ones after it, made optional when one of them is null
	var result Type
	sawNull := false
	give := func(body ast.Expr, t Type) {
		switch {
		case t == nil || isUnknown(t):
		case t.Equals(Void):
			c.errorfWithSuggestion(body.Token(), ErrMatchValue,
				"Every case of a match that gives a value is a value, or leaves with return, break, continue or panic",
				"this case gives no value")
		case t.Equals(Null):
			sawNull = true
		case expected != nil && !isUnknown(expected) && c.assignable(expected, t):
			// What is wanted here takes every case: a trait its structs
			// implement, a union, an optional
			result = expected
		case result == nil || c.assignable(t, result):
			result = t
		case !c.assignable(result, t):
			c.errorfWithSuggestion(body.Token(), ErrTypeMismatch,
				"Every case of a match that gives a value gives the same type",
				"this case gives %s, the cases above give %s", t, result)
		}
	}

	c.withBranches(len(s.Cases), func(i int) facts { return caseFacts[i] }, func(i int) {
		matchCase := s.Cases[i]
		body := func() {
			switch body := matchCase.Body.(type) {
			case *ast.BlockStmt:
				c.checkBlockStmt(body)
				if asValue && !exits(body) {
					c.errorfWithSuggestion(matchCase.Token(), ErrMatchValue,
						"Write the value as an expression (case ...: value;), or leave the block with return, break, continue or panic",
						"this case gives no value")
				}
			case ast.Expr:
				switch {
				case !asValue:
					c.checkExpr(body)
				case isPanic(&ast.ExprStmt{Expr: body}):
					// Nothing comes after a panic: no value is needed
					c.checkExpr(body)
				default:
					want := expected
					if want == nil {
						want = result
					}
					give(body, c.checkExprAs(body, want))
				}
			}
		}

		if matchCase.Guard == nil {
			body()
			return
		}
		// The condition sees what the pattern established, and the body
		// what the condition did
		guardType := c.checkExpr(matchCase.Guard)
		if guardType != nil && !guardType.Equals(Bool) && !isUnknown(guardType) {
			c.errorfWithSuggestion(matchCase.Guard.Token(), ErrInvalidCondition, "The condition of a case must evaluate to a boolean", "condition must be Bool, got %s", guardType)
		}
		c.withNarrowing(c.factsFrom(matchCase.Guard, true), body)
	})

	if !asValue {
		return nil
	}
	switch {
	case result == nil && sawNull:
		return Null
	case result == nil:
		return Unknown
	case sawNull && !nullable(result):
		return optional(result)
	}
	return result
}

// patternConst returns the top-level constant a pattern names, `case
// HAND_OVER:`, and nil for any other pattern. The case compares the value
// with the constant's, as if it were written there.
func (c *Checker) patternConst(pattern ast.Node) *ast.ConstDecl {
	ident, ok := pattern.(*ast.Ident)
	if !ok {
		return nil
	}
	sym := c.table.Defs[ident]
	if sym == nil {
		return nil
	}
	decl, _ := sym.Decl.(*ast.ConstDecl)
	return decl
}

// constLiteral is the literal a constant stands for when its value is
// one, followed through constants that are other constants
// (`const ON = YES;`): what the check that a match is complete compares.
// nil for a value worked out with operators.
func (c *Checker) constLiteral(decl *ast.ConstDecl) *ast.Literal {
	for depth := 0; decl != nil && depth < 100; depth++ {
		switch value := decl.Value.(type) {
		case *ast.Literal:
			return value
		case *ast.Ident:
			decl = c.patternConst(value)
		default:
			return nil
		}
	}
	return nil
}

// isScalar reports whether a type is one a constant in a pattern can be.
func isScalar(t Type) bool {
	return t != nil && (t.Equals(Int) || t.Equals(Float) || t.Equals(String) || t.Equals(Bool))
}

// describeTypePattern writes the type pattern that takes every value of a
// type, for a suggestion: Int, or `_` when there is none to write.
func describeTypePattern(t Type) string {
	if t == nil || isUnknown(t) {
		return "_"
	}
	return t.String()
}

// isNullPattern reports whether a case is `case null:` alone, without a
// condition: the cases after it never see null.
func isNullPattern(matchCase *ast.MatchCase) bool {
	if matchCase.Guard != nil || len(matchCase.Alternatives) > 0 {
		return false
	}
	lit, ok := matchCase.Pattern.(*ast.Literal)
	return ok && lit.Kind == "null"
}

// checkSameNames checks that the patterns of a case with several name the
// same values, of the same types: whichever matched, the body is the same,
// and a name only some of them give would not always exist.
//
//	case Add(x, _) | Sub(x, _): ...     x from either
func (c *Checker) checkSameNames(patterns []ast.Node) {
	namesOf := func(pattern ast.Node) map[string]*ast.BindingPattern {
		names := make(map[string]*ast.BindingPattern)
		for _, binding := range bindingsIn(pattern) {
			names[binding.Name] = binding
		}
		return names
	}
	first := namesOf(patterns[0])
	for _, pattern := range patterns[1:] {
		names := namesOf(pattern)
		for name, binding := range names {
			given, ok := first[name]
			if !ok {
				c.errorfWithSuggestion(binding.Token(), ErrInvalidPattern,
					"Name the same values in every pattern of the case, or write '_' in its place",
					"'%s' is not named by the first pattern of the case", name)
				continue
			}
			if a, b := c.bindings[given], c.bindings[binding]; a != nil && b != nil && !isUnknown(a) && !isUnknown(b) && !c.sameType(a, b) {
				c.errorf(binding.Token(), ErrInvalidPattern, "'%s' is a %s here and a %s in the first pattern of the case", name, describe(b), describe(a))
			}
		}
		for name, binding := range first {
			if _, ok := names[name]; !ok {
				c.errorfWithSuggestion(pattern.Token(), ErrInvalidPattern,
					"Name the same values in every pattern of the case, or write '_' in their place",
					"this pattern does not name '%s', which the first one does", binding.Name)
			}
		}
	}
}

// --- Ranges ---

// checkRangeBounds checks the two ends of a range. Those of a loop are
// whole numbers; a case may also be a range of floats.
func (c *Checker) checkRangeBounds(r *ast.RangeExpr, whole bool) Type {
	var of Type
	for _, bound := range []ast.Expr{r.Low, r.High} {
		t := c.checkExpr(bound)
		switch {
		case t == nil || isUnknown(t):
		case t.Equals(Int), !whole && t.Equals(Float):
			if of == nil {
				of = t
			}
		case whole:
			c.errorfWithSuggestion(bound.Token(), ErrBadRange,
				"A range goes from one Int to another: 0..10, or 0..=10 to include the last",
				"the bounds of a range are whole numbers, this is %s", t)
		default:
			c.errorf(bound.Token(), ErrBadRange, "the bounds of a range are numbers, this is %s", t)
		}
	}
	return of
}

func (c *Checker) checkRangePattern(r *ast.RangeExpr, subject Type) {
	of := c.checkRangeBounds(r, false)
	if of != nil && subject != nil && !isUnknown(subject) && !c.comparable(subject, of) {
		c.errorf(r.Token(), ErrInvalidPattern, "a value of type %s is never in a range of %s", subject, of)
	}
}

// --- defer ---

// checkDeferStmt checks the body of a defer. It runs later, when the block
// is left: like the body of a function written there, it can only rely on
// what was established about variables that never change.
func (c *Checker) checkDeferStmt(s *ast.DeferStmt) {
	outer := c.flowSnapshot()
	narrowed := make(map[*resolve.Symbol]Type)
	for sym, typ := range c.narrowed {
		if !c.reassigned[sym] {
			narrowed[sym] = typ
		}
	}
	c.narrowed = narrowed
	c.narrowedPaths = make(map[string]pathFact)

	inDefer, loops := c.inDefer, c.loopsInDefer
	c.inDefer, c.loopsInDefer = true, 0
	c.checkBlockStmt(s.Body)
	c.inDefer, c.loopsInDefer = inDefer, loops

	c.flowRestore(outer)
}

// checkLeavesDefer reports a break or continue that would leave a defer.
// Those of a loop inside the defer are its own business.
func (c *Checker) checkLeavesDefer(stmt ast.Stmt) {
	if !c.inDefer || c.loopsInDefer > 0 {
		return
	}
	word := "break"
	if _, ok := stmt.(*ast.ContinueStmt); ok {
		word = "continue"
	}
	c.errorfWithSuggestion(stmt.Token(), ErrBadDefer,
		"A defer runs while its block is being left and cannot decide where to go",
		fmt.Sprintf("'%s' inside a defer would leave it", word))
}

// --- What desugaring makes ---

// checkBlockExpr types statements that end in a value. One without a value
// always leaves, and has no type: nil.
func (c *Checker) checkBlockExpr(e *ast.BlockExpr) Type {
	state := c.flowSnapshot()
	defer c.flowRestore(state)

	for _, stmt := range e.Stmts {
		c.checkStmt(stmt)
	}
	if e.Value == nil {
		return nil
	}
	return c.checkExpr(e.Value)
}

// checkCondExpr types a choice between two values: the type both have, the
// wider when one takes the other, optional when one is null.
func (c *Checker) checkCondExpr(e *ast.CondExpr) Type {
	return c.checkCondExprAs(e, nil)
}

// checkCondExprAs is checkCondExpr where a type is expected: each branch
// is checked as a value of it, which is what gives a short function its
// parameters and an empty array its items.
func (c *Checker) checkCondExprAs(e *ast.CondExpr, expected Type) Type {
	branch := func(expr ast.Expr) Type {
		if expected == nil {
			return c.checkExpr(expr)
		}
		return c.checkExprAs(expr, expected)
	}
	if cond := c.checkExpr(e.Cond); cond != nil && !isUnknown(cond) && !cond.Equals(Bool) {
		c.errorfWithSuggestion(e.Cond.Token(), ErrInvalidCondition, "Condition must evaluate to a boolean", "condition must be Bool, got %s", cond)
	}

	var then, otherwise Type
	whenTrue := c.factsFrom(e.Cond, true)
	c.recordNarrowedNames(whenTrue)
	c.withNarrowing(whenTrue, func() { then = branch(e.Then) })
	c.withNarrowing(c.factsFrom(e.Cond, false), func() { otherwise = branch(e.Else) })

	if mixesNumbers([]Type{then, otherwise}) {
		// A whole number in one branch, a Float in the other: a Float
		then, otherwise = c.widen(e.Then, Float, then), c.widen(e.Else, Float, otherwise)
	}

	says := func(t Type) bool { return t != nil && !isUnknown(t) && !t.Equals(Null) }
	isNull := func(t Type) bool { return t != nil && t.Equals(Null) }
	switch {
	case says(then) && says(otherwise):
		if c.assignable(otherwise, then) {
			return otherwise
		}
		if expected != nil && !isUnknown(expected) && c.assignable(expected, then) && c.assignable(expected, otherwise) {
			// Two types the place takes both of: Int | String
			return expected
		}
		if !c.assignable(then, otherwise) {
			c.errorfWithSuggestion(e.Else.Token(), ErrTypeMismatch,
				"Both branches give the value of the 'if': make them of one type",
				"this branch gives %s, the one before gives %s", otherwise, then)
		}
		return then
	case says(then) && isNull(otherwise):
		return optional(then)
	case says(otherwise) && isNull(then):
		return optional(otherwise)
	case says(then):
		return then
	case says(otherwise):
		return otherwise
	case isNull(then) || isNull(otherwise):
		return Null
	}
	return Unknown
}

// checkWaitedCases checks the cases of a wait match on one channel, which
// the match marked WaitsOn stands for: a value none of them takes would
// stay in the channel, and the wait match would wait for good. Those of
// an enum are checked as any match's; the rest is here.
func (c *Checker) checkWaitedCases(s *ast.MatchStmt, condType Type, hasDefault, nullTaken bool) {
	if condType == nil || isUnknown(condType) || hasDefault {
		return
	}
	if decl, _ := c.enumOfType(withoutNullOrNil(condType)); decl == nil && !s.Exhaustive {
		c.errorfWithSuggestion(s.Token(), ErrNonExhaustiveMatch,
			"Take the rest too, with a case that names it: case let value = channel.receive(): ...",
			"the cases on this channel take some of its values and not the others: one they leave would stay in the channel, and this wait match would wait for it for good")
		return
	}
	if nullable(condType) && !nullTaken {
		c.errorfWithSuggestion(s.Token(), ErrNonExhaustiveMatch,
			"Say what happens then: case channel.receive() is null: ...",
			"a channel gives null once it is closed, and no case here takes it: this wait match would wait for good")
	}
}
