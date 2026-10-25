package monomorph

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// Methods made again for each type they are used with.
//
// A value of a generic struct carries no type arguments when the program
// runs: one struct, Box, serves every Box<T>, and one method serves every
// Box<T> it is called on. A method that casts to a type parameter of its
// impl, or of its own (impl<T> Box<T> { fn get() -> T { return self.v as
// T; } }), would check nothing then. Such a method is made again for each
// type it is called with, as a generic function is: Box.get$Int, which
// casts to Int. The checker said, of each call of one, what it is called
// on (MemberExpr.Receiver); a call whose receiver is known here, in the
// program or in a function made for its types, goes to the method made
// for them.

// needed is a method made again for each type, with its impl, kept as it
// was before anything of the program was rewritten.
type needed struct {
	impl     *ast.ImplDecl
	method   *ast.FnDecl
	pristine *ast.FnDecl
}

// collectSpecialized finds the methods made again for each type.
func (m *Monomorphizer) collectSpecialized() {
	m.specialized = map[string]map[string]*needed{}
	m.madeMethods = map[string]string{}
	for _, decl := range m.prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != "" {
			// A method of a trait is called through the trait too, on a
			// value whose types are not known: it cannot be one made for them
			continue
		}
		for _, method := range impl.Methods {
			if method.Module == "std" || strings.HasPrefix(method.Module, "std.") {
				continue
			}
			if !ast.CastsToParams(method, impl.TypeParams) {
				continue
			}
			if m.specialized[impl.StructName] == nil {
				m.specialized[impl.StructName] = map[string]*needed{}
			}
			m.specialized[impl.StructName][method.Name] = &needed{impl: impl, method: method, pristine: (&ast.Cloner{}).Fn(method)}
		}
	}
}

// specializeMethods makes the methods the calls whose receiver is known
// go to, and sends those calls there. It says whether it made any.
func (m *Monomorphizer) specializeMethods() bool {
	made := false
	visit := func(fn *ast.FnDecl, params []string) {
		if fn.Body == nil {
			return
		}
		inScope := map[string]bool{}
		for _, p := range params {
			inScope[p] = true
		}
		for _, p := range fn.TypeParams {
			inScope[p] = true
		}
		ast.Walk(fn.Body, func(e ast.Expr) {
			member, ok := e.(*ast.MemberExpr)
			if !ok || member.Receiver == nil || ast.Mentions(member.Receiver, inScope) {
				return
			}
			if name, fresh := m.specializedCall(member); name != "" {
				member.Name, member.TypeArgs, member.Receiver = name, nil, nil
				made = made || fresh
			}
		}, func(ast.Stmt) {})
	}
	// What is made here is visited the next time, when it is in the impl
	decls := append([]ast.Decl(nil), m.prog.Decls...)
	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			visit(d, nil)
		case *ast.ImplDecl:
			for _, method := range append([]*ast.FnDecl(nil), d.Methods...) {
				visit(method, d.TypeParams)
			}
		}
	}
	return made
}

// specializedCall is the name of the method made for what a call is
// called on and with, made if it was not: "" when the method is not one
// made again for each type, or what it is called with is not all said.
func (m *Monomorphizer) specializedCall(member *ast.MemberExpr) (name string, fresh bool) {
	recv := member.Receiver
	for recv.Kind == ast.TypeOption && recv.ElemType != nil {
		recv = recv.ElemType
	}
	n := m.specialized[recv.Name][member.Name]
	if n == nil {
		return "", false
	}
	if len(recv.Generics) != len(n.impl.TypeParams) || len(member.TypeArgs) != len(n.method.TypeParams) {
		return "", false
	}
	subst := map[string]*ast.Type{}
	var said []string
	for i, p := range n.impl.TypeParams {
		subst[p] = recv.Generics[i]
		said = append(said, recv.Generics[i].String())
	}
	for i, p := range n.method.TypeParams {
		subst[p] = member.TypeArgs[i]
		said = append(said, member.TypeArgs[i].String())
	}
	key := recv.Name + "." + member.Name + "<" + strings.Join(said, ", ") + ">"
	if made, ok := m.madeMethods[key]; ok {
		return made, false
	}
	name = member.Name + "$" + mangleTypes(said)
	cloner := &ast.Cloner{Types: subst}
	copy := cloner.Fn(n.pristine)
	copy.Name, copy.TypeParams, copy.CopyOf = name, nil, n.method.Name
	n.impl.Methods = append(n.impl.Methods, copy)
	m.madeMethods[key] = name
	return name, true
}

// mangleTypes writes types as a part of a name: what is not a letter or
// a digit an underscore.
func mangleTypes(said []string) string {
	var b strings.Builder
	for _, c := range strings.Join(said, ",") {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
