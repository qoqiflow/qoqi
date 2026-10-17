package compiler

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// The methods the library gives arrays, texts and numbers are its
// functions under another name:
//
//	impl<T> [T] { pub fn contains(wanted: T) -> Bool { return contains(self, wanted); } }
//
// A call of one is written back as the call of the function it stands for,
// here, before generic functions are compiled for the types they are used
// with: items.contains(point) becomes contains<Point>(items, point). The
// function is then compiled for Point, where `==` is the one Point says it
// has. Left as a method, it would be the method of every array, compiled
// once, which knows nothing of what the array holds.
//
// It also leaves one call where there were two, and nothing of the library
// between a program and what fails in it.

// forwardLibraryMethods rewrites the calls of the methods of the library
// that only forward, in place.
func forwardLibraryMethods(prog *ast.Program, info *types.Info) {
	// The function each such method forwards to, by what the impl is for
	// and the name of the method
	type forwarder struct {
		target   string
		generic  bool        // An impl for every array: its type parameter is the function's first
		typeArgs []*ast.Type // What an impl for one type writes: sorted<Int>(self, before)
	}
	forwarders := make(map[string]map[string]forwarder)
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != "" || !types.IsBuiltinTypeName(impl.StructName) {
			continue
		}
		for _, method := range impl.Methods {
			target := ast.ForwardedTo(method)
			if target == "" || target == resolve.PushFunction {
				// push is an instruction of its own, where it is
				continue
			}
			if forwarders[impl.StructName] == nil {
				forwarders[impl.StructName] = make(map[string]forwarder)
			}
			generic := impl.StructName == ast.ArrayImpl || impl.StructName == ast.OptionalImpl
			f := forwarder{target: target, generic: generic}
			if !generic {
				f.typeArgs = forwardedTypeArgs(method)
			}
			forwarders[impl.StructName][method.Name] = f
		}
	}
	if len(forwarders) == 0 {
		return
	}

	rewrite := func(expr ast.Expr) {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return
		}
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok {
			return
		}
		receiver := info.Types[member.Expr]
		if receiver == nil {
			return
		}

		// The impl the checker found the method in: for an array, that of
		// its items before that of every array
		var keys []string
		var items types.Type
		if types.IsOptional(receiver) {
			// An optional: what it holds is the function's first type
			// argument
			items = types.WithoutNull(receiver)
			keys = append(keys, ast.OptionalImpl)
		} else if array, isArray := receiver.(*types.ArrayType); isArray {
			items = array.ElementType
			if item := types.ImplKey(items); item != "" && item != ast.ArrayImpl {
				keys = append(keys, ast.ArrayImplOf(item))
			}
			keys = append(keys, ast.ArrayImpl)
		} else if key := types.ImplKey(receiver); key != "" {
			if _, isStruct := receiver.(*types.StructType); isStruct {
				return
			}
			keys = append(keys, key)
		}
		var found forwarder
		for _, key := range keys {
			if f, has := forwarders[key][member.Name]; has {
				found = f
				break
			}
		}
		if found.target == "" {
			return
		}

		callee := &ast.Ident{BaseNode: member.BaseNode, Name: found.target}
		if found.generic {
			// What the array holds, written out for the function
			item := types.TypeToAST(items, member.BaseNode)
			if item == nil {
				return // Not known ([]): the method of every array will do
			}
			callee.TypeArgs = append([]*ast.Type{item}, member.TypeArgs...)
		} else if len(found.typeArgs) > 0 {
			callee.TypeArgs = found.typeArgs
		}
		call.Func = callee
		call.Args = append([]ast.Expr{member.Expr}, call.Args...)
	}

	walk := func(body *ast.BlockStmt) {
		if body != nil {
			ast.Walk(body, rewrite, func(ast.Stmt) {})
		}
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			walk(d.Body)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				walk(method.Body)
			}
		case *ast.TraitDecl:
			for _, method := range d.Methods {
				walk(method.Body)
			}
		}
	}
}

// forwardedTypeArgs gives the type arguments of the call a method that
// forwards is made of, which an impl for the arrays of one type writes
// out: impl [Int] { fn sorted(...) { return sorted<Int>(self, before); } }.
func forwardedTypeArgs(method *ast.FnDecl) []*ast.Type {
	var call *ast.CallExpr
	switch stmt := method.Body.Stmts[0].(type) {
	case *ast.ReturnStmt:
		call, _ = stmt.Value.(*ast.CallExpr)
	case *ast.ExprStmt:
		call, _ = stmt.Expr.(*ast.CallExpr)
	}
	if call == nil {
		return nil
	}
	if callee, ok := call.Func.(*ast.Ident); ok {
		return callee.TypeArgs
	}
	return nil
}
