package compiler

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/types"
)

// What a program does not use of the standard library is left out early.
//
// A program that writes items.len() has all of std.arrays in it: fifty
// functions, as many methods, and the modules they are written with. The
// stages that follow would each go through all of it (generic functions
// compiled for their types, everything rewritten, named and checked again,
// twice) to end with the one function the program calls, the rest being
// dropped when the bytecode is written. Here it is dropped before: once
// the program has been checked, and the calls of the library's methods
// are calls of its functions (see forward.go), what the program can reach
// is found, and only that goes on.
//
// The program's own declarations all stay, used or not: what is wrong in
// one is still said, and they are few. Only the library is looked at, and
// kept on the side of caution: a method is kept when its name is written
// after a dot anywhere in what is kept, whatever it is written after.

// alwaysCalled are the methods the compiler calls without the program
// writing their name: what a loop over a range that was kept stops at,
// and what says why main ended when a `?` there met a failure.
var alwaysCalled = map[string]bool{"stop": true, "describe": true}

// It is done twice. Before the program is checked, the functions of the
// library that nothing can reach lose their bodies: they are still
// declared, so that what the program gets wrong about them is said as it
// was (a method that does not exist is still told from one that takes
// other arguments), but nothing in them is named or checked. After the
// check they go altogether.

// pruneLibrary returns the program without the declarations of the
// standard library that nothing reaches. The program it is given is left
// as it was: it is what the compilation hands back as the program read.
func pruneLibrary(prog *ast.Program) *ast.Program {
	return prune(prog, false)
}

// emptyLibrary returns the program with the functions of the standard
// library that nothing reaches left without their bodies.
func emptyLibrary(prog *ast.Program) *ast.Program {
	return prune(prog, true)
}

func prune(prog *ast.Program, onlyBodies bool) *ast.Program {
	inLibrary := func(name string) bool { return strings.HasPrefix(name, stdlib.Package+".") }
	libraryImpl := func(impl *ast.ImplDecl) bool {
		if len(impl.Methods) == 0 {
			return inLibrary(impl.StructName)
		}
		return inLibrary(impl.Methods[0].Module)
	}

	p := &pruner{
		named:   make(map[string][]ast.Decl),
		reached: make(map[ast.Decl]bool),
		kept:    make(map[*ast.FnDecl]bool),
		types:   make(map[string]bool),
		members: make(map[string]bool),
	}
	library := false
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			p.named[d.Name] = append(p.named[d.Name], d)
			library = library || inLibrary(d.Name)
		case *ast.ConstDecl:
			p.named[d.Name] = append(p.named[d.Name], d)
		case *ast.StructDecl:
			p.named[d.Name] = append(p.named[d.Name], d)
		case *ast.EnumDecl:
			p.named[d.Name] = append(p.named[d.Name], d)
		case *ast.TraitDecl:
			p.named[d.Name] = append(p.named[d.Name], d)
		case *ast.ImplDecl:
			if libraryImpl(d) {
				p.impls = append(p.impls, d)
				library = true
			}
		}
	}
	if !library {
		return prog
	}

	// Everything the program itself declares is where it starts from,
	// what makes its variables outside functions included
	for _, global := range prog.Globals {
		p.typ(global.Type)
		p.expr(global.Value)
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if !inLibrary(d.Name) {
				p.reach(d)
			}
		case *ast.ConstDecl:
			if !inLibrary(d.Name) {
				p.reach(d)
			}
		case *ast.StructDecl:
			if !inLibrary(d.Name) {
				p.reach(d)
			}
		case *ast.EnumDecl:
			if !inLibrary(d.Name) {
				p.reach(d)
			}
		case *ast.TraitDecl:
			if !inLibrary(d.Name) {
				p.reach(d)
			}
		case *ast.ImplDecl:
			if !libraryImpl(d) {
				p.types[d.StructName] = true
				p.name(d.StructName)
				p.name(d.TraitName)
				for _, arg := range d.TraitArgs {
					p.typ(arg)
				}
				for _, method := range d.Methods {
					p.function(method)
				}
			}
		}
	}

	// The methods of the library that what is kept may call, until no
	// more are found: one that is kept may call another
	for changed := true; changed; {
		changed = false
		p.drain()
		for _, impl := range p.impls {
			builtin := types.IsBuiltinTypeName(impl.StructName)
			if !builtin && !p.types[impl.StructName] {
				continue // Of a struct no value of which is ever made
			}
			// What a trait asks for is called by what the trait gives: an
			// operator, a loop, brackets, a value seen through it. On a
			// struct that is any of them, whether the trait is named or
			// not. On a number, a text or an array the machine does the
			// operators itself: only a program that names the trait (a
			// requirement on a type parameter, a value of the trait) can
			// reach its methods.
			forTrait := impl.TraitName != "" && (!builtin || p.reachedName(impl.TraitName))
			for _, method := range impl.Methods {
				if p.kept[method] {
					continue
				}
				if forTrait || p.members[method.Name] || alwaysCalled[method.Name] {
					p.name(impl.TraitName)
					for _, arg := range impl.TraitArgs {
						p.typ(arg)
					}
					p.function(method)
					changed = true
				}
			}
		}
	}

	kept := make([]ast.Decl, 0, len(prog.Decls))
	if onlyBodies {
		// Everything stays declared; what is not reached is a signature
		bodiless := func(fn *ast.FnDecl) *ast.FnDecl {
			if p.kept[fn] || fn.Body == nil {
				return fn
			}
			empty := *fn
			empty.Body = nil
			return &empty
		}
		for _, decl := range prog.Decls {
			switch d := decl.(type) {
			case *ast.FnDecl:
				if inLibrary(d.Name) {
					kept = append(kept, bodiless(d))
					continue
				}
			case *ast.ImplDecl:
				if libraryImpl(d) {
					slim := *d
					slim.Methods = make([]*ast.FnDecl, len(d.Methods))
					for i, method := range d.Methods {
						slim.Methods[i] = bodiless(method)
					}
					kept = append(kept, &slim)
					continue
				}
			}
			kept = append(kept, decl)
		}
		pruned := *prog
		pruned.Decls = kept
		return &pruned
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.ImplDecl:
			if !libraryImpl(d) {
				kept = append(kept, d)
				continue
			}
			var methods []*ast.FnDecl
			for _, method := range d.Methods {
				if p.kept[method] {
					methods = append(methods, method)
				}
			}
			if len(methods) > 0 {
				slim := *d
				slim.Methods = methods
				kept = append(kept, &slim)
			}
		default:
			if p.reached[decl] {
				kept = append(kept, decl)
			}
		}
	}
	pruned := *prog
	pruned.Decls = kept
	return &pruned
}

type pruner struct {
	named   map[string][]ast.Decl // What a name declares
	impls   []*ast.ImplDecl       // The impls of the library
	reached map[ast.Decl]bool
	kept    map[*ast.FnDecl]bool // The methods of the library that stay
	types   map[string]bool      // The structs and enums that are named
	members map[string]bool      // The names written after a dot
	pending []ast.Decl
}

// reachedName reports whether something a name declares was reached.
func (p *pruner) reachedName(name string) bool {
	for _, decl := range p.named[name] {
		if p.reached[decl] {
			return true
		}
	}
	return false
}

// name marks what a name declares as reached.
func (p *pruner) name(name string) {
	if name == "" {
		return
	}
	p.types[name] = true
	for _, decl := range p.named[name] {
		p.reach(decl)
	}
}

func (p *pruner) reach(decl ast.Decl) {
	if !p.reached[decl] {
		p.reached[decl] = true
		p.pending = append(p.pending, decl)
	}
}

// drain goes through what was reached and not looked at yet.
func (p *pruner) drain() {
	for len(p.pending) > 0 {
		decl := p.pending[len(p.pending)-1]
		p.pending = p.pending[:len(p.pending)-1]
		switch d := decl.(type) {
		case *ast.FnDecl:
			p.function(d)
		case *ast.ConstDecl:
			p.typ(d.Type)
			p.expr(d.Value)
		case *ast.StructDecl:
			p.types[d.Name] = true
			for _, bounds := range d.Bounds {
				for _, bound := range bounds {
					p.typ(bound)
				}
			}
			for _, field := range d.Fields {
				p.typ(field.Type)
				p.expr(field.Default)
			}
		case *ast.EnumDecl:
			p.types[d.Name] = true
			for _, bounds := range d.Bounds {
				for _, bound := range bounds {
					p.typ(bound)
				}
			}
			for _, variant := range d.Variants {
				for _, value := range variant.Values {
					p.typ(value)
				}
			}
		case *ast.TraitDecl:
			for _, method := range d.Methods {
				p.function(method)
			}
		}
	}
}

// function marks what a function names: in its signature and in its body.
func (p *pruner) function(fn *ast.FnDecl) {
	if p.kept[fn] {
		return
	}
	p.kept[fn] = true
	for _, param := range fn.Params {
		p.typ(param.Type)
		p.expr(param.Default)
	}
	p.typ(fn.ReturnType)
	for _, bounds := range fn.Bounds {
		for _, bound := range bounds {
			p.typ(bound)
		}
	}
	if fn.Body != nil {
		ast.Walk(fn.Body, p.visitExpr, p.visitStmt)
	}
}

func (p *pruner) expr(expr ast.Expr) {
	if expr != nil {
		ast.Walk(expr, p.visitExpr, p.visitStmt)
	}
}

func (p *pruner) typ(t *ast.Type) {
	if t == nil {
		return
	}
	if t.Kind == ast.TypeBasic {
		p.name(t.Name)
	}
	for _, sub := range t.Generics {
		p.typ(sub)
	}
	p.typ(t.ElemType)
	for _, sub := range t.UnionTypes {
		p.typ(sub)
	}
	for _, sub := range t.ParamTypes {
		p.typ(sub)
	}
	p.typ(t.ReturnType)
}

func (p *pruner) pattern(pattern ast.Node) {
	switch n := pattern.(type) {
	case *ast.Type:
		p.typ(n)
	case *ast.VariantPattern:
		p.expr(n.Target)
		for _, arg := range n.Args {
			p.pattern(arg)
		}
	}
}

func (p *pruner) visitExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.Ident:
		p.name(e.Name)
		if e.Name == ast.EntriesFunction {
			// A literal of a dictionary is made by one of these
			p.name(types.DictOf)
			p.name(types.MapOf)
		}
		for _, arg := range e.TypeArgs {
			p.typ(arg)
		}
	case *ast.MemberExpr:
		p.members[e.Name] = true
		for _, arg := range e.TypeArgs {
			p.typ(arg)
		}
	case *ast.StructInitExpr:
		p.name(e.Name)
		for _, arg := range e.TypeArgs {
			p.typ(arg)
		}
	case *ast.CastExpr:
		p.typ(e.Type)
	case *ast.TypeTestExpr:
		p.typ(e.Type)
	case *ast.PatternTestExpr:
		p.pattern(e.Pattern)
	case *ast.FnExpr:
		for _, param := range e.Params {
			p.typ(param.Type)
		}
		p.typ(e.ReturnType)
	}
}

func (p *pruner) visitStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.LetStmt:
		p.typ(s.Type)
	case *ast.MatchStmt:
		for _, matchCase := range s.Cases {
			for _, pattern := range matchCase.Patterns() {
				p.pattern(pattern)
			}
		}
	}
}
