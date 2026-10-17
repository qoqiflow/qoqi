package types

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// Functions written without types: `item -> item.name`.
//
// Such a function is typed from outside: the function type expected where
// it stands says what its parameters are, and what it gives if that is
// known. The checker writes those types into the function expression, so
// that every later stage sees `fn(item: Person) -> String { ... }` and
// knows nothing of the short form.

// shortFnIsTyped reports whether a function expression has the types of
// its parameters: always for one written with `fn`, and for a short one
// once they were filled in.
func shortFnIsTyped(e *ast.FnExpr) bool {
	return !e.Short || e.Typed
}

// checkShortFn types a short function against the type expected of it.
func (c *Checker) checkShortFn(e *ast.FnExpr, expected Type) Type {
	want, ok := expectedFunction(expected)
	if !ok {
		// Nothing useful is expected: only a function typed on an earlier
		// pass can go on
		return c.checkFnExpr(e)
	}
	if len(want.Params) != len(e.Params) {
		c.errorfWithSuggestion(e.Token(), ErrShortFn,
			fmt.Sprintf("Name %s: %s", parameters(len(want.Params)), map[bool]string{true: "name -> ...", false: "(first, second) -> ..."}[len(want.Params) == 1]),
			"a function of %s is expected here, and this one names %d", parameters(len(want.Params)), len(e.Params))
		return Unknown
	}

	params := make([]Type, len(want.Params))
	for i, param := range want.Params {
		if isUnknown(param) && e.Typed {
			// Its types were settled, and its body checked, while the
			// call around it was being worked out; what is expected now
			// knows less than that did
			if known := c.info.Types[e]; known != nil {
				return known
			}
			return Unknown
		}
		if isUnknown(param) {
			c.errorfWithSuggestion(e.Params[i].Token(), ErrShortFn,
				fmt.Sprintf("Write the types: fn(%s: Type) -> Type { ... }", e.Params[i].Name),
				"nothing here says what type '%s' is", e.Params[i].Name)
			return Unknown
		}
		params[i] = param
	}

	// What it gives is what is expected, when that is known; otherwise
	// what its body gives
	result := want.ReturnType
	if result == nil || isUnknown(result) {
		result = c.shortFnResult(e, params)
		if result == nil {
			result = Unknown
		}
	}
	if !c.annotateShortFn(e, params, result) {
		c.errorfWithSuggestion(e.Token(), ErrShortFn, "Write the types: fn(name: Type) -> Type { ... }",
			"the types of this function cannot be written out")
		return Unknown
	}
	return c.checkFnExpr(e)
}

// expectedFunction returns the function type among what is expected of an
// expression: the type itself, or what an optional of it holds.
func expectedFunction(expected Type) (*FunctionType, bool) {
	if expected == nil {
		return nil, false
	}
	if value := withoutNull(expected); value != nil {
		expected = value
	}
	fn, ok := expected.(*FunctionType)
	return fn, ok && !fn.IsVariadic
}

// annotateShortFn writes the types of a short function into it.
func (c *Checker) annotateShortFn(e *ast.FnExpr, params []Type, result Type) bool {
	types := make([]*ast.Type, len(params))
	for i, param := range params {
		if types[i] = typeToAST(param, e.Params[i].BaseNode); types[i] == nil {
			return false
		}
	}
	for i, param := range e.Params {
		param.Type = types[i]
	}
	e.Typed = true

	returnsNothing := result.Equals(Void)
	e.ReturnType = nil
	if !returnsNothing && !isUnknown(result) {
		e.ReturnType = typeToAST(result, e.BaseNode)
	}
	if !returnsNothing && e.ReturnType == nil {
		// What it gives could not be worked out, or not in full: that is
		// reported where the type was needed, not once more here
		e.ReturnType = &ast.Type{BaseNode: e.BaseNode, Kind: ast.TypeBasic, Name: "unknown"}
	}

	// `item -> log(item)` where nothing is to be given back: the
	// expression is evaluated, not returned
	if e.ExprBody && len(e.Body.Stmts) == 1 {
		switch stmt := e.Body.Stmts[0].(type) {
		case *ast.ReturnStmt:
			if returnsNothing && stmt.Value != nil {
				e.Body.Stmts[0] = &ast.ExprStmt{BaseNode: stmt.BaseNode, Expr: stmt.Value}
			}
		case *ast.ExprStmt:
			if !returnsNothing {
				e.Body.Stmts[0] = &ast.ReturnStmt{BaseNode: stmt.BaseNode, Value: stmt.Expr}
			}
		}
	}
	return true
}

// shortFnResult works out what a short function gives, from its body,
// once the types of its parameters are known. Nothing is reported: the
// function is checked for good when its whole type is settled. It
// returns nil when the body does not say.
func (c *Checker) shortFnResult(e *ast.FnExpr, params []Type) Type {
	for i, param := range params {
		paramType := typeToAST(param, e.Params[i].BaseNode)
		if paramType == nil {
			return nil
		}
		e.Params[i].Type = paramType
	}
	// A body of one expression is looked at as returned, whatever an
	// earlier look made of it
	if e.ExprBody && len(e.Body.Stmts) == 1 {
		if stmt, ok := e.Body.Stmts[0].(*ast.ExprStmt); ok {
			e.Body.Stmts[0] = &ast.ReturnStmt{BaseNode: stmt.BaseNode, Value: stmt.Expr}
		}
	}

	reported, said := c.errs, c.said
	c.errs, c.said = errors.NewHandler(), nil
	fn := &ast.FnDecl{
		BaseNode:   e.BaseNode,
		Params:     e.Params,
		ReturnType: &ast.Type{BaseNode: e.BaseNode, Kind: ast.TypeBasic, Name: "unknown"},
		Body:       e.Body,
	}
	if c.currentFn != nil {
		fn.Module = c.currentFn.Module
	}
	c.enteringFnExpr = true
	c.checkFnDecl(fn)
	c.errs, c.said = reported, said

	// What its returns give: the first type that says something, made
	// optional when one of them is null
	var result Type
	sawNull, sawReturn := false, false
	var visit func(stmt ast.Stmt)
	visit = func(stmt ast.Stmt) {
		switch s := stmt.(type) {
		case *ast.BlockStmt:
			for _, inner := range s.Stmts {
				visit(inner)
			}
		case *ast.IfStmt:
			visit(s.Then)
			if s.Else != nil {
				visit(s.Else)
			}
		case *ast.WhileStmt:
			visit(s.Body)
		case *ast.ForStmt:
			visit(s.Body)
		case *ast.MatchStmt:
			for _, mc := range s.Cases {
				if body, ok := mc.Body.(ast.Stmt); ok {
					visit(body)
				}
			}
		case *ast.ReturnStmt:
			if s.Value == nil {
				return
			}
			sawReturn = true
			t := c.info.Types[s.Value]
			switch {
			case t == nil || isUnknown(t):
			case t.Equals(Null):
				sawNull = true
			case result == nil:
				result = t
			}
		}
	}
	visit(e.Body)

	switch {
	case result == nil && !sawReturn:
		return Void
	case result == nil:
		return nil
	case sawNull:
		return optional(result)
	}
	return result
}

// mentionsOpen reports whether a type names a type parameter that is
// being worked out and has not been found yet.
func mentionsOpen(t Type, isParam map[string]bool, found map[string]Type) bool {
	switch t := t.(type) {
	case *TypeParam:
		_, done := found[t.Name]
		return isParam[t.Name] && !done
	case *ArrayType:
		return mentionsOpen(t.ElementType, isParam, found)
	case *UnionType:
		for _, member := range t.Types {
			if mentionsOpen(member, isParam, found) {
				return true
			}
		}
	case *FunctionType:
		for _, param := range t.Params {
			if mentionsOpen(param, isParam, found) {
				return true
			}
		}
		return t.ReturnType != nil && mentionsOpen(t.ReturnType, isParam, found)
	case *StructType:
		for _, arg := range t.TypeArgs {
			if mentionsOpen(arg, isParam, found) {
				return true
			}
		}
	case *BehaviorType:
		for _, arg := range t.TypeArgs {
			if mentionsOpen(arg, isParam, found) {
				return true
			}
		}
	}
	return false
}

func parameters(n int) string {
	if n == 1 {
		return "1 parameter"
	}
	return fmt.Sprintf("%d parameters", n)
}
