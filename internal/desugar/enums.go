package desugar

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// --- Constants ---

// constOf returns the top-level constant an identifier refers to.
func (d *Desugarer) constOf(ident *ast.Ident) *ast.ConstDecl {
	sym := d.resolveTable.Defs[ident]
	if sym == nil {
		return nil
	}
	decl, _ := sym.Decl.(*ast.ConstDecl)
	return decl
}

// inlineConstant builds the value of a constant in place of a use of it.
// The value is made of literals, operators and other constants, which are
// replaced in turn, so what comes out only has literals and operators.
//
// The new nodes are positioned at the use: if working the value out fails
// when the program runs (a division by zero), that is the line to show.
func (d *Desugarer) inlineConstant(value ast.Expr, at ast.BaseNode, depth int) ast.Expr {
	if composite(d, value) {
		// Without the constant it belongs to (a constant used inside the
		// value of another is asked for by its name, below): made here
		return d.desugarExpr(value)
	}
	inlined := d.inlineConstantAsWritten(value, at, depth)
	if node, ok := value.(ast.Widenable); ok && node.IsWidened() {
		// const HALF: Float = 1; is a Float wherever it is used
		return d.asFloat(inlined)
	}
	return inlined
}

// composite reports whether the value of a constant is a struct, an array
// or a value of an enum: something that is made, where a number or a text
// is written.
func composite(d *Desugarer, value ast.Expr) bool {
	switch e := value.(type) {
	case *ast.StructInitExpr, *ast.ArrayLiteralExpr:
		return true
	case *ast.MemberExpr:
		_, variant := d.variantOf(e)
		return variant != nil
	case *ast.CallExpr:
		_, variant := d.variantOf(e.Func)
		return variant != nil
	}
	return false
}

// madeOnce is a use of a constant that is a struct, an array or a value of
// an enum: a call of the function that makes its value, which the machine
// only runs the first time (see vm.OpONCE). Every use of the constant is
// then the same value, and reading it costs what reading a variable does.
//
//	const RED = Color { r: 230, g: 41, b: 55 };
//
//	fn __const.RED() -> Color { return Color { r: 230, g: 41, b: 55, a: 255 }; }
//	... __const.RED() ...
//
// It returns nil for a constant that is not made that way.
func (d *Desugarer) madeOnce(decl *ast.ConstDecl, at ast.BaseNode) ast.Expr {
	if !composite(d, decl.Value) {
		return nil
	}
	maker, known := d.constants[decl]
	if !known {
		returns := d.typeToASTType(d.typeInfo.Types[decl.Value], decl.Position())
		if returns == nil {
			return nil
		}
		if d.constants == nil {
			d.constants = make(map[*ast.ConstDecl]*ast.FnDecl)
		}
		maker = &ast.FnDecl{BaseNode: decl.BaseNode, Name: resolve.ConstantPrefix + decl.Name, ReturnType: returns}
		// Known before its value is made: a constant that held itself
		// would otherwise be made for ever
		d.constants[decl] = maker
		d.constantOrder = append(d.constantOrder, maker)
		maker.Body = &ast.BlockStmt{BaseNode: decl.BaseNode, Stmts: []ast.Stmt{
			&ast.ReturnStmt{BaseNode: decl.BaseNode, Value: d.desugarExpr(decl.Value)},
		}}
	}
	return &ast.CallExpr{BaseNode: at, Func: &ast.Ident{BaseNode: at, Name: maker.Name}}
}

func (d *Desugarer) inlineConstantAsWritten(value ast.Expr, at ast.BaseNode, depth int) ast.Expr {
	// The type checker rejects a constant defined in terms of itself; this
	// only keeps a mistake there from becoming an endless loop here
	if depth > 1000 {
		return &ast.Literal{BaseNode: at, Value: "null", Kind: "null"}
	}

	switch e := value.(type) {
	case *ast.Literal:
		return &ast.Literal{BaseNode: at, Value: e.Value, Kind: e.Kind}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{BaseNode: at, Op: e.Op, Expr: d.inlineConstant(e.Expr, at, depth+1)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{
			BaseNode: at,
			Left:     d.inlineConstant(e.Left, at, depth+1),
			Op:       e.Op,
			Right:    d.inlineConstant(e.Right, at, depth+1),
		}
	case *ast.Ident:
		if decl := d.constOf(e); decl != nil {
			if once := d.madeOnce(decl, at); once != nil {
				return once
			}
			return d.inlineConstant(decl.Value, at, depth+1)
		}
	case *ast.CallExpr:
		// The call a `${...}` in a string stands for, the only one a
		// constant may hold
		args := make([]ast.Expr, len(e.Args))
		for i, arg := range e.Args {
			args[i] = d.inlineConstant(arg, at, depth+1)
		}
		callee := e.Func
		if ident, ok := callee.(*ast.Ident); ok {
			callee = &ast.Ident{BaseNode: at, Name: ident.Name}
		}
		return &ast.CallExpr{BaseNode: at, Func: callee, Args: args}
	}
	return value
}

// --- Enum values ---

// enumOf returns the enum an expression names, when it is the bare name
// of one.
func (d *Desugarer) enumOf(expr ast.Expr) *ast.EnumDecl {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil
	}
	sym := d.resolveTable.Defs[ident]
	if sym == nil || sym.Kind != resolve.SymbolEnum {
		return nil
	}
	decl, _ := sym.Decl.(*ast.EnumDecl)
	return decl
}

// variantOf returns the enum and variant named by `Enum.Variant`.
func (d *Desugarer) variantOf(expr ast.Expr) (*ast.EnumDecl, *ast.EnumVariant) {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return nil, nil
	}
	decl := d.enumOf(member.Expr)
	if decl == nil {
		return nil, nil
	}
	for _, v := range decl.Variants {
		if v.Name == member.Name {
			return decl, v
		}
	}
	return nil, nil
}

// enumTypeArgs turns the type arguments the checker found for an enum
// value into syntax, for the stages that look at the program again.
func (d *Desugarer) enumTypeArgs(t types.Type, pos token.Position) []*ast.Type {
	st, ok := t.(*types.StructType)
	if !ok {
		return nil
	}
	var args []*ast.Type
	for _, arg := range st.TypeArgs {
		typ := d.typeToASTType(arg, pos)
		if typ == nil {
			return nil
		}
		args = append(args, typ)
	}
	return args
}

// lowerVariantCall replaces `Enum.Variant(args)` by the creation of the
// value.
func (d *Desugarer) lowerVariantCall(call *ast.CallExpr, decl *ast.EnumDecl, variant *ast.EnumVariant) ast.Expr {
	args := make([]ast.Expr, len(call.Args))
	for i, arg := range call.Args {
		args[i] = d.desugarExpr(arg)
	}
	return &ast.VariantExpr{
		BaseNode: call.BaseNode,
		Enum:     decl.Name,
		Variant:  variant.Name,
		TypeArgs: d.enumTypeArgs(d.typeInfo.Types[call], call.Position()),
		Args:     args,
	}
}

// lowerVariantRef replaces `Enum.Variant` that is not called. For a
// variant without values that is the value itself. For one with values it
// is the function that makes a value, written out as a lambda:
// `Shape.Circle` becomes `fn(a0: Float) -> Shape { return Shape.Circle(a0); }`.
func (d *Desugarer) lowerVariantRef(member *ast.MemberExpr, decl *ast.EnumDecl, variant *ast.EnumVariant) ast.Expr {
	pos := member.Position()
	typ := d.typeInfo.Types[member]

	if len(variant.Values) == 0 {
		return &ast.VariantExpr{
			BaseNode: member.BaseNode,
			Enum:     decl.Name,
			Variant:  variant.Name,
			TypeArgs: d.enumTypeArgs(typ, pos),
		}
	}

	fnType, _ := typ.(*types.FunctionType)
	lambda := &ast.FnExpr{BaseNode: member.BaseNode}
	value := &ast.VariantExpr{BaseNode: member.BaseNode, Enum: decl.Name, Variant: variant.Name}
	for i, declared := range variant.Values {
		name := fmt.Sprintf("__value_%d", i)
		paramType := declared
		if fnType != nil && i < len(fnType.Params) {
			if known := d.typeToASTType(fnType.Params[i], pos); known != nil {
				paramType = known
			}
		}
		lambda.Params = append(lambda.Params, &ast.Param{BaseNode: member.BaseNode, Name: name, Type: paramType})
		value.Args = append(value.Args, d.createIdent(name, pos))
	}
	if fnType != nil {
		lambda.ReturnType = d.typeToASTType(fnType.ReturnType, pos)
		value.TypeArgs = d.enumTypeArgs(fnType.ReturnType, pos)
	}
	if lambda.ReturnType == nil {
		lambda.ReturnType = &ast.Type{BaseNode: member.BaseNode, Kind: ast.TypeBasic, Name: decl.Name}
	}
	lambda.Body = d.wrapInBlock([]ast.Stmt{
		&ast.ReturnStmt{BaseNode: member.BaseNode, Value: value},
	}, pos)
	return lambda
}

// --- Patterns ---

// lowerPattern turns a pattern on an enum value into what it means: the
// tests that say whether the value matches, and the variables to declare
// when it does.
//
//	case Shape.Rect(w, 0):     tests:  value is Shape.Rect, value.Rect#1 == 0
//	                           names:  let w = value.Rect#0;
//
// The tests are in the order they have to be made: a value of a variant is
// only read after the test that it is that variant.
func (d *Desugarer) lowerPattern(subject ast.Expr, pattern *ast.VariantPattern, pos token.Position) (tests []ast.Expr, names []ast.Stmt) {
	decl, variant := d.variantOf(pattern.Target)
	if decl == nil {
		// Rejected by the type checker
		return []ast.Expr{d.createBoolLiteral(false, pos)}, nil
	}
	base := pattern.BaseNode

	tests = append(tests, &ast.VariantTestExpr{BaseNode: base, Expr: subject, Enum: decl.Name, Variant: variant.Name})
	for i, arg := range pattern.Args {
		field := &ast.VariantFieldExpr{BaseNode: base, Expr: subject, Enum: decl.Name, Variant: variant.Name, Index: i}

		switch a := arg.(type) {
		case *ast.BindingPattern:
			if !a.Ignored() {
				names = append(names, &ast.LetStmt{BaseNode: a.BaseNode, Name: a.Name, Value: field})
			}
		case *ast.VariantPattern:
			innerTests, innerNames := d.lowerPattern(field, a, pos)
			tests = append(tests, innerTests...)
			names = append(names, innerNames...)
		case *ast.StructPattern:
			innerTests, innerNames := d.lowerStructPattern(field, a, pos)
			tests = append(tests, innerTests...)
			names = append(names, innerNames...)
		case ast.Expr:
			tests = append(tests, &ast.BinaryExpr{BaseNode: base, Left: field, Op: "==", Right: d.desugarExpr(a)})
		}
	}
	return tests, names
}

// lowerStructPattern does for `Point { x: 0, y }` what lowerPattern does
// for a variant:
//
//	tests:  value.x == 0
//	names:  let y = value.y;
//
// A value that is not known to be a Point (a trait, a union, Any) is
// tested for being one first, and its fields read through a cast.
func (d *Desugarer) lowerStructPattern(subject ast.Expr, pattern *ast.StructPattern, pos token.Position) (tests []ast.Expr, names []ast.Stmt) {
	base := pattern.BaseNode
	value := subject
	if known, ok := d.typeInfo.Types[subject].(*types.StructType); !ok || known.Name != pattern.Type.Name {
		tests = append(tests, &ast.TypeTestExpr{BaseNode: base, Expr: subject, Type: pattern.Type})
		value = &ast.CastExpr{BaseNode: base, Expr: subject, Type: pattern.Type}
	}
	for _, f := range pattern.Fields {
		field := &ast.MemberExpr{BaseNode: base, Expr: value, Name: f.Name}
		switch a := f.Pattern.(type) {
		case *ast.BindingPattern:
			if !a.Ignored() {
				names = append(names, &ast.LetStmt{BaseNode: a.BaseNode, Name: a.Name, Value: field})
			}
		case *ast.VariantPattern:
			innerTests, innerNames := d.lowerPattern(field, a, pos)
			tests = append(tests, innerTests...)
			names = append(names, innerNames...)
		case *ast.StructPattern:
			innerTests, innerNames := d.lowerStructPattern(field, a, pos)
			tests = append(tests, innerTests...)
			names = append(names, innerNames...)
		case ast.Expr:
			tests = append(tests, &ast.BinaryExpr{BaseNode: base, Left: field, Op: "==", Right: d.desugarExpr(a)})
		}
	}
	if len(tests) == 0 {
		tests = append(tests, d.createBoolLiteral(true, pos))
	}
	return tests, names
}

// allOf joins tests with `&&`, which only makes a test when the ones
// before it held.
func (d *Desugarer) allOf(tests []ast.Expr, pos token.Position) ast.Expr {
	result := tests[0]
	for _, test := range tests[1:] {
		result = &ast.BinaryExpr{BaseNode: ast.BaseNode{Pos: pos, Tok: test.Token()}, Left: result, Op: "&&", Right: test}
	}
	return result
}
