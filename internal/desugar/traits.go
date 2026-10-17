package desugar

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/types"
)

// What a struct gets from the traits of std.ops, and from its own
// declaration, written out as the calls and values it stands for. The
// type checker found each of these (types.Info); here they become code
// the later stages already know.
//
//	a + b               a.plus(b)
//	a == b, a != b      a.equals(b), !a.equals(b)
//	a < b               a.compare(b) < 0
//	a += b              a = a.plus(b)
//	for x in a          for x in a.iterate()
//	a[k]                a.at(k)
//	a[k] = v            a.put(k, v)
//	println(a), "${a}"  println(a.show()), for a struct that says how it reads
//	users.get           fn(key: String) -> User? { return users.get(key); }
//	Config { }          Config { port: 8080 }, for a field declared `port: Int = 8080`

// call builds `receiver.method(args)`.
func (d *Desugarer) call(receiver ast.Expr, method string, at ast.BaseNode, args ...ast.Expr) *ast.CallExpr {
	member := &ast.MemberExpr{BaseNode: at, Expr: receiver, Name: method}
	return &ast.CallExpr{BaseNode: at, Func: member, Args: args}
}

// lowerOperator writes an operator on structs as the call of the method
// behind it. The operands are already desugared.
func (d *Desugarer) lowerOperator(binary *ast.BinaryExpr, op, method string, left, right ast.Expr) ast.Expr {
	call := d.call(left, method, binary.BaseNode, right)
	switch op {
	case "!=":
		return &ast.UnaryExpr{BaseNode: binary.BaseNode, Op: "!", Expr: call}
	case "<", "<=", ">", ">=":
		// compare gives a number below, at or above zero
		return &ast.BinaryExpr{BaseNode: binary.BaseNode, Left: call, Op: op, Right: d.createIntLiteral("0", binary.Position())}
	}
	return call
}

// lowerStructOperation handles the binary expressions a trait gives a
// meaning: an operator, an assignment through an operator, an assignment
// to what is in brackets. It returns nil for any other.
func (d *Desugarer) lowerStructOperation(binary *ast.BinaryExpr) ast.Expr {
	if d.typeInfo.IndexWrites[binary] {
		target := binary.Left.(*ast.IndexExpr)
		return d.call(d.desugarExpr(target.Expr), types.MethodPut, binary.BaseNode,
			d.desugarExpr(target.Index), d.desugarExpr(binary.Right))
	}

	method, ok := d.typeInfo.OperatorMethods[binary]
	if !ok {
		return nil
	}
	if base, compound := ast.CompoundOp(binary.Op); compound {
		// a += b: the target is read, then assigned
		cloner := &ast.Cloner{}
		value := d.lowerOperator(binary, base, method, d.desugarExpr(cloner.Expr(binary.Left)), d.desugarExpr(binary.Right))
		return &ast.BinaryExpr{BaseNode: binary.BaseNode, Left: d.desugarExpr(binary.Left), Op: "=", Right: value}
	}
	return d.lowerOperator(binary, binary.Op, method, d.desugarExpr(binary.Left), d.desugarExpr(binary.Right))
}

// iterated gives a loop over a struct the array it goes through:
// `for x in list` becomes `for x in list.iterate()`.
func (d *Desugarer) iterated(forStmt *ast.ForStmt) *ast.ForStmt {
	elem, ok := d.typeInfo.Iterations[forStmt]
	if !ok {
		return forStmt
	}
	items := d.call(forStmt.Iterable, types.MethodIterate, ast.BaseNode{Pos: forStmt.Iterable.Position(), Tok: forStmt.Iterable.Token()})
	d.typeInfo.Types[items] = types.NewArray(elem)

	loop := *forStmt
	loop.Iterable = items
	return &loop
}

// methodValue writes a method used as a value as the function that calls
// it: `users.get` is `fn(key: String) -> User? { return users.get(key); }`.
func (d *Desugarer) methodValue(member *ast.MemberExpr, signature *types.FunctionType) ast.Expr {
	at := member.BaseNode
	fn := &ast.FnExpr{BaseNode: at}
	var args []ast.Expr
	for i, param := range signature.Params {
		paramType := types.TypeToAST(param, at)
		if paramType == nil {
			d.reportMissingTypeInfo(member, "method used as a value")
			return member
		}
		name := fmt.Sprintf("__arg%d", i)
		fn.Params = append(fn.Params, &ast.Param{BaseNode: at, Name: name, Type: paramType})
		args = append(args, &ast.Ident{BaseNode: at, Name: name})
	}

	call := d.call(d.desugarExpr(member.Expr), member.Name, at, args...)
	fn.Body = &ast.BlockStmt{BaseNode: at}
	if signature.ReturnType == nil || signature.ReturnType.Equals(types.Void) {
		fn.Body.Stmts = []ast.Stmt{&ast.ExprStmt{BaseNode: at, Expr: call}}
		return fn
	}
	fn.ReturnType = types.TypeToAST(signature.ReturnType, at)
	if fn.ReturnType == nil {
		d.reportMissingTypeInfo(member, "method used as a value")
		return member
	}
	fn.Body.Stmts = []ast.Stmt{&ast.ReturnStmt{BaseNode: at, Value: call}}
	return fn
}

// typeMethod writes a method of a type used as a value as the function
// that calls it on its first argument: `Point.plus` is
// `fn(self: Point, other: Point) -> Point { return self.plus(other); }`.
func (d *Desugarer) typeMethod(member *ast.MemberExpr, signature *types.FunctionType) ast.Expr {
	at := member.BaseNode
	self := d.typeInfo.Types[member]
	whole, ok := self.(*types.FunctionType)
	if !ok || len(whole.Params) == 0 {
		d.reportMissingTypeInfo(member, "method of a type used as a value")
		return member
	}
	fn := &ast.FnExpr{BaseNode: at}
	var args []ast.Expr
	for i, param := range whole.Params {
		paramType := types.TypeToAST(param, at)
		if paramType == nil {
			d.reportMissingTypeInfo(member, "method of a type used as a value")
			return member
		}
		name := fmt.Sprintf("__arg%d", i)
		fn.Params = append(fn.Params, &ast.Param{BaseNode: at, Name: name, Type: paramType})
		if i > 0 {
			args = append(args, &ast.Ident{BaseNode: at, Name: name})
		}
	}

	call := d.call(&ast.Ident{BaseNode: at, Name: "__arg0"}, member.Name, at, args...)
	fn.Body = &ast.BlockStmt{BaseNode: at}
	if signature.ReturnType == nil || signature.ReturnType.Equals(types.Void) {
		fn.Body.Stmts = []ast.Stmt{&ast.ExprStmt{BaseNode: at, Expr: call}}
		return fn
	}
	fn.ReturnType = types.TypeToAST(signature.ReturnType, at)
	if fn.ReturnType == nil {
		d.reportMissingTypeInfo(member, "method of a type used as a value")
		return member
	}
	fn.Body.Stmts = []ast.Stmt{&ast.ReturnStmt{BaseNode: at, Value: call}}
	return fn
}

// structDecl finds the declaration of a struct.
func (d *Desugarer) structDecl(name string) *ast.StructDecl {
	if d.structs == nil {
		d.structs = make(map[string]*ast.StructDecl)
		for _, decl := range d.prog.Decls {
			if s, ok := decl.(*ast.StructDecl); ok {
				d.structs[s.Name] = s
			}
		}
	}
	return d.structs[name]
}

// withDefaults adds to the fields of a struct literal the ones it leaves
// out and the struct declares with a value. Each literal gets a value of
// its own: two structs made from `tags: [String] = []` do not share an
// array.
func (d *Desugarer) withDefaults(name string, fields map[string]ast.Expr) {
	decl := d.structDecl(name)
	if decl == nil {
		return
	}
	for _, field := range decl.Fields {
		if _, given := fields[field.Name]; given || field.Default == nil {
			continue
		}
		// Desugared once, where the names in it are known (a constant is
		// replaced by its value there), then copied for each literal
		value, done := d.defaults[field]
		if !done {
			value = d.desugarExpr(field.Default)
			if d.defaults == nil {
				d.defaults = make(map[*ast.Field]ast.Expr)
			}
			d.defaults[field] = value
		}
		fields[field.Name] = (&ast.Cloner{}).Expr(value)
	}
}

// textBuiltins are the builtin functions that turn their arguments into
// text: what they are given is shown the way its type says, if it does.
var textBuiltins = map[string]bool{"print": true, "println": true, "toString": true, "panic": true, "__text": true}

// shown replaces, among the arguments of a call that turns values into
// text, each one whose type implements Show by what its `show` gives.
// args are the arguments as desugared, in the order of call.Args.
func (d *Desugarer) shown(call *ast.CallExpr, args []ast.Expr) {
	callee, ok := call.Func.(*ast.Ident)
	if !ok || !textBuiltins[callee.Name] {
		return
	}
	for i, arg := range call.Args {
		at := ast.BaseNode{Pos: arg.Position(), Tok: arg.Token()}
		argType := d.typeInfo.Types[arg]
		if value, optional := valueOf(argType); optional && d.shows(value) {
			// A value that may be missing: value?.show() ?? "null"
			holder := &ast.Ident{BaseNode: at, Name: ast.SafeHolderPrefix + d.generateTmpVar()}
			shown := &ast.BinaryExpr{BaseNode: at, Left: args[i], Op: ast.SafeAccessOp, Right: d.call(holder, types.MethodShow, at)}
			null := &ast.Literal{BaseNode: at, Value: "null", Kind: "string"}
			args[i] = &ast.BinaryExpr{BaseNode: at, Left: shown, Op: "??", Right: null}
		} else if d.shows(argType) {
			args[i] = d.call(args[i], types.MethodShow, at)
		}
	}
}

// shows reports whether a type is one that says how its values read. The
// builtin types are left to the machine, which writes them the same way.
func (d *Desugarer) shows(t types.Type) bool {
	switch t.(type) {
	case *types.StructType, *types.TypeParam, *types.BehaviorType:
		return d.typeInfo.Implements(t, types.TraitShow)
	}
	return false
}

// valueOf returns what an optional type holds when it is not null, and
// whether the type is an optional of one type.
func valueOf(t types.Type) (types.Type, bool) {
	union, ok := t.(*types.UnionType)
	if !ok {
		return nil, false
	}
	var value types.Type
	hasNull := false
	for _, member := range union.Types {
		if member.Equals(types.Null) {
			hasNull = true
			continue
		}
		if value != nil {
			return nil, false
		}
		value = member
	}
	return value, hasNull && value != nil
}
