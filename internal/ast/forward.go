package ast

import "strings"

// ForwardedTo returns the function a method of the standard library does
// nothing but call, with its receiver first and its parameters after it in
// their order: `pub fn trim() -> String { return trim(self); }`. Such a
// method is the function under another name, and is called as the
// function: nothing stands between the program and what fails, and there
// is one call where there were two.
// UsesSelf reports whether a method names self anywhere in its body. One
// that does not is a function of its type: Point.origin().
func UsesSelf(m *FnDecl) bool {
	if m.Body == nil {
		return true
	}
	uses := false
	Walk(m.Body, func(e Expr) {
		if ident, ok := e.(*Ident); ok && ident.Name == "self" {
			uses = true
		}
	}, func(Stmt) {})
	return uses
}

// StaticName is the name of a function of a type, Point::origin, which no
// method or function of a program can have.
func StaticName(typeName, name string) string {
	return typeName + "::" + name
}

func ForwardedTo(m *FnDecl) string {
	if !strings.HasPrefix(m.Module, "std.") || m.Body == nil || len(m.Body.Stmts) != 1 {
		return ""
	}
	var call *CallExpr
	switch stmt := m.Body.Stmts[0].(type) {
	case *ReturnStmt:
		call, _ = stmt.Value.(*CallExpr)
	case *ExprStmt:
		call, _ = stmt.Expr.(*CallExpr)
	}
	if call == nil || len(call.Args) != len(m.Params)+1 {
		return ""
	}
	callee, ok := call.Func.(*Ident)
	if !ok {
		return ""
	}
	for i, arg := range call.Args {
		name, ok := arg.(*Ident)
		switch {
		case !ok:
			return ""
		case i == 0 && name.Name != "self":
			return ""
		case i > 0 && (name.Name != m.Params[i-1].Name || m.Params[i-1].Type == nil || m.Params[i-1].Type.Kind == TypeVariadic):
			return ""
		}
	}
	return callee.Name
}
