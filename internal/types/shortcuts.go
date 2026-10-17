package types

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// What the operand of a `value?` is (Info.Tries).
const (
	TryResult   = "result"
	TryOptional = "optional"
	// In main, which has no caller to hand a failure to: the program ends
	// there, saying why. TryEndDescribed when the reason has describe()
	TryEnd          = "end"
	TryEndDescribed = "end, described"
)

// ResultEnum is the enum of the standard library that `value?` knows: a
// value, or the reason there is none.
const ResultEnum = "std.result.Result"

// resultType returns t as a Result<T, E>, or nil if it is not one.
func resultType(t Type) *StructType {
	st, ok := t.(*StructType)
	if !ok || st.BaseName != ResultEnum || len(st.TypeArgs) != 2 {
		return nil
	}
	return st
}

// nullable reports whether a value of the type may be null.
func nullable(t Type) bool {
	if t.Equals(Null) {
		return true
	}
	u, ok := t.(*UnionType)
	return ok && hasNull(u)
}

// checkSafeAccess checks `value?.member` and `value?.method(args)`: the
// access, made on the value when it is not null. The result is what the
// access gives, or null.
func (c *Checker) checkSafeAccess(e *ast.BinaryExpr) Type {
	left := c.checkExpr(e.Left)
	if left == nil || isUnknown(left) {
		// The name standing for the value has no type either: the access
		// is not looked at, and adds no error of its own
		return Unknown
	}

	if resultType(left) != nil {
		c.errorfWithSuggestion(e.Token(), ErrNeverNull,
			"To go on with its value and pass a failure on to the caller, write (value?).member; to look at both cases, use a match",
			"'?.' is for a value that may be null, and a %s never is", describe(left))
		return Unknown
	}
	if !nullable(left) && !left.Equals(Any) {
		c.errorfWithSuggestion(e.Token(), ErrNeverNull, "Use '.' here",
			"'?.' is for a value that may be null, and this %s never is", describe(left))
	}

	right := c.checkExpr(e.Right)
	if right == nil || isUnknown(right) {
		return Unknown
	}
	if right.Equals(Void) {
		// A method called for what it does: there is no value either way
		return Void
	}
	return optional(right)
}

// checkTry checks `value?`. On a Result it gives the value, and returns
// the failure from the enclosing function if there is none; on an
// optional it gives the value, and returns null if there is none. Either
// way the function has to be one that can return that; in a function
// that returns nothing, `value?` on an optional simply ends it.
func (c *Checker) checkTry(e *ast.UnaryExpr) Type {
	if c.inDefer {
		c.errorfWithSuggestion(e.Token(), ErrBadDefer,
			"A defer runs while its block is being left and cannot return; handle the failure there, with a match or orElse",
			"'?' inside a defer")
	}
	operand := c.checkExpr(e.Expr)
	if operand == nil || isUnknown(operand) {
		return Unknown
	}

	var returns Type = Void
	if c.currentFn != nil && c.currentFn.ReturnType != nil {
		returns = c.resolveType(c.currentFn.ReturnType)
	}

	if result := resultType(operand); result != nil {
		value, reason := result.TypeArgs[0], result.TypeArgs[1]
		outer := resultType(returns)
		switch {
		case isUnknown(returns):
		case outer == nil && c.currentFn != nil && c.currentFn.Name == "main" && (returns.Equals(Int) || returns.Equals(Void)):
			// main has no caller: a failure ends the program, as a panic
			// does, with the reason as its message
			c.info.Tries[e] = TryEnd
			if ft, ok := c.methodType(reason, "describe", nil); ok && len(ft.Params) == 0 && ft.ReturnType.Equals(String) {
				c.info.Tries[e] = TryEndDescribed
			}
			return value
		case outer == nil:
			c.errorfWithSuggestion(e.Token(), ErrBadTry,
				fmt.Sprintf("Make the function return Result<..., %s>, or handle the failure here with a match or orElse", describe(reason)),
				"'?' passes a failure on to the caller, and this function returns %s, not a Result", describe(returns))
			return value
		case !isUnknown(reason) && !isUnknown(outer.TypeArgs[1]) && !outer.TypeArgs[1].Equals(reason):
			c.errorfWithSuggestion(e.Token(), ErrBadTry,
				"Turn one into the other first: value.mapErr(convert)?",
				"'?' would pass on a failure of type %s, and this function returns failures of type %s", describe(reason), describe(outer.TypeArgs[1]))
			return value
		}
		c.info.Tries[e] = TryResult
		return value
	}

	if nullable(operand) {
		value := withoutNull(operand)
		if value == nil {
			value = Null
		}
		// A function that returns nothing just ends there
		if !isUnknown(returns) && !nullable(returns) && !returns.Equals(Any) && !returns.Equals(Void) {
			suggestion := "Give a fallback with '??', or test against null"
			if resultType(returns) == nil {
				suggestion = fmt.Sprintf("Make the function return %s?, give a fallback with '??', or test against null", describe(returns))
			}
			c.errorfWithSuggestion(e.Token(), ErrBadTry, suggestion,
				"'?' on a value that may be null returns null to the caller, and this function returns %s", describe(returns))
			return value
		}
		c.info.Tries[e] = TryOptional
		return value
	}

	c.errorfWithSuggestion(e.Token(), ErrBadTry, "Remove the '?'",
		"'?' is for a Result or a value that may be null, and this is a %s", describe(operand))
	return operand
}

// checkUnwrap checks `value!`: the value of an optional, or the value of a
// Result that succeeded. When there is none the program stops there,
// saying so, or saying why for a Result.
func (c *Checker) checkUnwrap(e *ast.UnaryExpr) Type {
	operand := c.checkExpr(e.Expr)
	if operand == nil || isUnknown(operand) {
		return Unknown
	}
	if result := resultType(operand); result != nil {
		c.info.Tries[e] = TryEnd
		if ft, ok := c.methodType(result.TypeArgs[1], "describe", nil); ok && len(ft.Params) == 0 && ft.ReturnType.Equals(String) {
			c.info.Tries[e] = TryEndDescribed
		}
		return result.TypeArgs[0]
	}
	if nullable(operand) {
		c.info.Tries[e] = TryOptional
		if value := withoutNull(operand); value != nil {
			return value
		}
		return Null
	}
	c.errorfWithSuggestion(e.Token(), ErrBadTry, "Remove the '!'",
		"'!' is for a Result or a value that may be null, and a value of type %s is always there", describe(operand))
	return operand
}

// TemplateStruct is the struct of the standard library that a text written
// in the program becomes where one is expected: its parts and its values
// kept apart (see templateOf).
const TemplateStruct = "std.ops.Template"

// refusedAsTemplate reports a String given where a Template is expected,
// saying why, and whether it did: the text was made elsewhere, and nothing
// says any more which of it was written and which was put in.
func (c *Checker) refusedAsTemplate(expr ast.Expr, expected, actual Type, code string) bool {
	st, ok := withoutNull(expected).(*StructType)
	if !ok || st.Name != TemplateStruct || !withoutNull(actual).Equals(String) {
		return false
	}
	c.errorfWithSuggestion(expr.Token(), code,
		"Write the text here, with ${...} for what changes: what is written in the program and what is put in it are then told apart",
		"a Template is a text written in the program, and this is a String made elsewhere")
	return true
}

// asTemplate replaces a text written in the program, where a Template is
// expected, with the Template it stands for: its parts and its values
// apart. It is called wherever a value meets the type it must have: an
// argument, a variable or a field declared with it, a return, an item of
// an array. It reports whether it replaced the expression.
func asTemplate(expr *ast.Expr, expected Type) bool {
	if *expr == nil {
		return false
	}
	template := templateOf(*expr, expected)
	if template == nil {
		return false
	}
	*expr = template
	return true
}

// templateOf gives, for a text written in the program, with or without
// ${...} in it, given where a Template is expected, the Template it
// stands for; nil for anything else. The lexer writes "a ${x} b" as
// "a " + __text(x) + " b", which is taken apart again here.
func templateOf(arg ast.Expr, expected Type) ast.Expr {
	if st, ok := withoutNull(expected).(*StructType); !ok || st.Name != TemplateStruct {
		return nil
	}
	var pieces []ast.Expr
	var walk func(e ast.Expr) bool
	walk = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.BinaryExpr:
			return e.Op == "+" && walk(e.Left) && walk(e.Right)
		case *ast.Literal:
			pieces = append(pieces, e)
			return e.Kind == "string"
		case *ast.CallExpr:
			pieces = append(pieces, e)
			return isInterpolation(e)
		}
		return false
	}
	if !walk(arg) {
		return nil
	}

	text := func(value string) *ast.Literal {
		tok := arg.Token()
		tok.Literal = value
		return &ast.Literal{BaseNode: ast.BaseNode{Pos: arg.Position(), Tok: tok}, Kind: "string", Value: value}
	}
	parts := []ast.Expr{}
	values := []ast.Expr{}
	current := ""
	for _, piece := range pieces {
		if lit, ok := piece.(*ast.Literal); ok {
			current += lit.Value
			continue
		}
		parts = append(parts, text(current))
		values = append(values, piece)
		current = ""
	}
	parts = append(parts, text(current))

	at := ast.BaseNode{Pos: arg.Position(), Tok: arg.Token()}
	return &ast.StructInitExpr{BaseNode: at, Name: TemplateStruct, Fields: map[string]ast.Expr{
		"parts":  &ast.ArrayLiteralExpr{BaseNode: at, Elements: parts},
		"values": &ast.ArrayLiteralExpr{BaseNode: at, Elements: values},
	}}
}
