package modules

import (
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/token"
)

// rewriter replaces, in one module, every name that refers to a top-level
// declaration by that declaration's program-wide name: its own
// declarations, names brought in by `import m.{name}`, and members of
// imported modules (`body.new`, `body.Body`).
//
// It follows the scoping of the language as far as needed to tell such a
// name from a local one: a variable or parameter hides a function and a
// module of the same name, a type parameter hides a type.
type rewriter struct {
	*loader
	m *module

	locals     []map[string]bool
	typeParams []map[string]bool

	// The enums each name of a variant is a variant of, among those the
	// module names without a dot; made when first needed
	variants map[string][]*item

	// The names of modules and the names imported that were used
	usedAliases map[string]bool
	usedNames   map[string]bool
	imports     []*ast.ImportDecl // As written, before rewriting drops them
}

// unusedImports warns of the imports of the module nothing uses: of a
// module whose name is never written, or of names never written. The
// imports the loader adds, those passed on with pub, and those of the
// library are left alone; so is the import of a module that implements
// traits for the types of others, or gives methods to the language's own
// (std.time gives every number days()), which may be what it is imported
// for.
func (r *rewriter) unusedImports() {
	if strings.HasPrefix(r.m.name, stdlib.Package+".") || r.m.name == stdlib.Package {
		return
	}
	for _, imp := range r.imports {
		if imp.Implicit || imp.Pub {
			continue
		}
		if target := r.loader.modules[imp.Path]; target != nil && implementsForOthers(target) {
			continue
		}
		if len(imp.Names) == 0 {
			alias := imp.Alias
			if alias == "" {
				alias = imp.Path[strings.LastIndex(imp.Path, ".")+1:]
			}
			if !r.usedAliases[alias] {
				r.warn(imp.Token(), ErrUnusedImport, "Remove the import", "module '%s' is imported and never used", imp.Path)
			}
			continue
		}
		for _, name := range imp.Names {
			if !r.usedNames[name.Name] {
				r.warn(name.Token(), ErrUnusedImport, "Remove it from the import", "'%s' is imported and never used", name.Name)
			}
		}
	}
}

// builtinTypes are the types of the language an impl can give methods to.
var builtinTypes = map[string]bool{"Int": true, "Float": true, "String": true, "Bool": true}

// implementsForOthers reports whether a module implements a trait for a
// type it does not declare, or gives methods to a type of the language.
func implementsForOthers(m *module) bool {
	own := make(map[string]bool)
	for name, it := range m.types {
		own[name], own[it.qualified] = true, true
	}
	for _, decl := range m.prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		if impl.TraitName != "" && !own[impl.StructName] {
			return true
		}
		if impl.TraitName == "" && builtinTypes[impl.StructName] {
			return true
		}
	}
	return false
}

// staticsOf takes the functions of the type out of an impl, as functions
// of their own named Type::name, the impl's type parameters theirs too,
// and rewrites them.
func (r *rewriter) staticsOf(impl *ast.ImplDecl) []ast.Decl {
	var kept []*ast.FnDecl
	var out []ast.Decl
	for _, method := range impl.Methods {
		if !method.Static {
			kept = append(kept, method)
			continue
		}
		if ast.UsesSelf(method) {
			r.report(method.Token(), ErrStaticSelf, "Take the value as a parameter, or make it a method: remove 'static'",
				"static function '%s' has no self: it is called on the type, %s.%s()", method.Name, impl.StructName, method.Name)
		}
		if method.Pub {
			r.exposes(method, "function '"+impl.StructName+"."+method.Name+"'")
		}
		fn := method
		fn.Name = ast.StaticName(r.m.qualify(impl.StructName), method.Name)
		fn.Module = r.m.name
		fn.TypeParams = append(append([]string{}, impl.TypeParams...), method.TypeParams...)
		if len(impl.Bounds) > 0 {
			// Copies: the impl's are rewritten with it
			bounds := make(map[string][]*ast.Type)
			cloner := &ast.Cloner{}
			for name, b := range impl.Bounds {
				for _, bound := range b {
					bounds[name] = append(bounds[name], cloner.Type(bound))
				}
			}
			for name, b := range method.Bounds {
				bounds[name] = b
			}
			fn.Bounds = bounds
		}
		r.fn(fn)
		out = append(out, fn)
	}
	impl.Methods = kept
	return out
}

// staticOf gives the function of a type a member expression names, as the
// name of that function: Point.origin, geo.Point.origin.
func (r *rewriter) staticOf(e *ast.MemberExpr) ast.Expr {
	var owner *item
	switch base := e.Expr.(type) {
	case *ast.Ident:
		if r.isLocal(base.Name) || r.value(base.Name) != nil {
			return nil
		}
		owner = r.typeItem(base.Name)
	case *ast.MemberExpr:
		target, ok := r.moduleOf(base.Expr)
		if !ok {
			return nil
		}
		if it, _ := target.lookup(base.Name, make(map[*module]bool)); it != nil && it.isType {
			owner = it
		}
	}
	if owner == nil || owner.module == nil {
		return nil
	}
	pub, ok := owner.module.statics[owner.name][e.Name]
	if !ok {
		return nil
	}
	tok := memberToken(e)
	if !pub && owner.module != r.m {
		r.report(tok, ErrPrivate, "Declare it with 'pub' in "+owner.module.file+" to use it from other modules",
			"function '%s.%s' of %s is private", owner.name, e.Name, owner.module.display())
	}
	return &ast.Ident{BaseNode: ast.BaseNode{Pos: tok.Pos, Tok: tok}, Name: ast.StaticName(owner.qualified, e.Name), TypeArgs: e.TypeArgs}
}

func (r *rewriter) rewrite() {
	var statics []ast.Decl
	defer func() { r.m.prog.Decls = append(r.m.prog.Decls, statics...) }()
	for _, global := range r.m.prog.Globals {
		global.Name = r.m.qualify(global.Name)
		r.typ(global.Type)
		global.Value = r.expr(global.Value)
	}
	for _, decl := range r.m.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if d.Pub {
				r.exposes(d, "function '"+d.Name+"'")
			}
			d.Name = r.m.qualify(d.Name)
			d.Module = r.m.name
			r.fn(d)

		case *ast.ConstDecl:
			d.Name = r.m.qualify(d.Name)
			d.Module = r.m.name
			r.typ(d.Type)
			d.Value = r.expr(d.Value)

		case *ast.StructDecl:
			d.Name = r.m.qualify(d.Name)
			d.Module = r.m.name
			r.withTypeParams(d.TypeParams, func() {
				r.bounds(d.Bounds)
				for _, field := range d.Fields {
					r.typ(field.Type)
					if field.Default != nil {
						r.scoped(func() { field.Default = r.expr(field.Default) })
					}
				}
			})

		case *ast.EnumDecl:
			d.Name = r.m.qualify(d.Name)
			r.withTypeParams(d.TypeParams, func() {
				r.bounds(d.Bounds)
				for _, variant := range d.Variants {
					for _, value := range variant.Values {
						r.typ(value)
					}
				}
			})

		case *ast.TraitDecl:
			d.Name = r.m.qualify(d.Name)
			r.withTypeParams(d.TypeParams, func() {
				for _, method := range d.Methods {
					// What a trait promises is as visible as the trait
					method.Pub = true
					method.Module = r.m.name
					r.method(method)
				}
			})

		case *ast.ImplDecl:
			if r.loader.unused && d.TraitName == "" && !strings.HasPrefix(r.m.name, stdlib.Package+".") {
				for _, method := range d.Methods {
					if !method.Static && !method.Migrate && !ast.UsesSelf(method) {
						r.warn(method.Token(), ErrCouldBeStatic, "Write it static fn "+method.Name+"(...), and call it "+d.StructName+"."+method.Name+"(...)",
							"method '%s' does not use self", method.Name)
					}
				}
			}
			// The static functions of the impl are the type's:
			// Point.origin(), not point.origin()
			if r.m.statics[d.StructName] != nil {
				statics = append(statics, r.staticsOf(d)...)
			}
			// The pub methods of a type others can name are theirs too
			if owner := r.m.types[d.StructName]; d.TraitName == "" && (owner == nil || owner.pub) {
				for _, method := range d.Methods {
					if method.Pub {
						r.exposes(method, "method '"+method.Name+"'")
					}
				}
			}
			if item, arrays := ast.IsArrayImpl(d.StructName); !arrays && d.StructName != ast.OptionalImpl {
				d.StructName = r.typeName(d.StructName, d.Token())
			} else if item != "" && r.typeItem(item) != nil {
				// impl [Point]: the items are named in full, like any type
				d.StructName = ast.ArrayImplOf(r.typeName(item, d.Token()))
			}
			if d.TraitName != "" {
				d.TraitName = r.typeName(d.TraitName, d.Token())
			}
			r.withTypeParams(d.TypeParams, func() {
				r.bounds(d.Bounds)
				r.types(d.TraitArgs)
				for _, method := range d.Methods {
					method.Module = r.m.name
					if d.TraitName != "" {
						method.Pub = true
					}
					r.method(method)
				}
			})
		}
	}
	r.imports = r.m.prog.Imports
	r.m.prog.Imports = nil
}

// --- Scopes ---

func (r *rewriter) withTypeParams(names []string, body func()) {
	scope := make(map[string]bool, len(names))
	for _, name := range names {
		scope[name] = true
	}
	r.typeParams = append(r.typeParams, scope)
	body()
	r.typeParams = r.typeParams[:len(r.typeParams)-1]
}

func (r *rewriter) isTypeParam(name string) bool {
	for _, scope := range r.typeParams {
		if scope[name] {
			return true
		}
	}
	return false
}

func (r *rewriter) scoped(body func()) {
	r.locals = append(r.locals, make(map[string]bool))
	body()
	r.locals = r.locals[:len(r.locals)-1]
}

func (r *rewriter) declare(name string) {
	if len(r.locals) > 0 {
		r.locals[len(r.locals)-1][name] = true
	}
}

func (r *rewriter) isLocal(name string) bool {
	for _, scope := range r.locals {
		if scope[name] {
			return true
		}
	}
	return false
}

// --- Names ---

// value returns the declaration a bare name refers to as a value, if it is
// a function of this module or one imported by name.
func (r *rewriter) value(name string) *item {
	if it, ok := r.m.values[name]; ok {
		return it
	}
	it := r.m.importedValues[name]
	if it != nil && r.usedNames != nil {
		r.usedNames[name] = true
	}
	return it
}

// typeItem is value for types.
func (r *rewriter) typeItem(name string) *item {
	if it, ok := r.m.types[name]; ok {
		return it
	}
	it := r.m.importedTypes[name]
	if it != nil && r.usedNames != nil {
		r.usedNames[name] = true
	}
	return it
}

// typeName returns the program-wide name of a type as written: `Body`, or
// `body.Body` for a type of an imported module. Names it does not know
// (builtin types, type parameters, mistakes) are returned unchanged for the
// type checker to deal with.
func (r *rewriter) typeName(name string, tok token.Token) string {
	alias, member, qualified := strings.Cut(name, ".")
	if !qualified {
		if r.isTypeParam(name) {
			return name
		}
		if it := r.typeItem(name); it != nil {
			return it.qualified
		}
		if _, isModule := r.m.aliases[name]; isModule {
			r.report(tok, ErrNotAValue, "Name a type of the module: "+name+".Name",
				"'%s' is a module, not a type", name)
		}
		return name
	}

	target, ok := r.m.aliases[alias]
	if ok {
		r.usedAliases[alias] = true
	}
	if !ok {
		r.report(tok, ErrModuleNotFound, r.importSuggestion(alias),
			"'%s' is not an imported module", alias)
		return name
	}
	it := r.find(r.m, target, member, tok)
	if it == nil {
		return name
	}
	if !it.isType {
		r.report(tok, ErrNotAValue, "", "'%s' is a function, not a type", name)
		return name
	}
	return it.qualified
}

// importSuggestion proposes the import that would make `alias.name` work.
func (r *rewriter) importSuggestion(alias string) string {
	var known []string
	for name := range r.m.aliases {
		known = append(known, name)
	}
	if similar := resolve.ClosestName(alias, known); similar != "" {
		return "Did you mean '" + similar + "'?"
	}
	var paths []string
	for path, m := range r.modules {
		if m != nil && (path == alias || strings.HasSuffix(path, "."+alias)) {
			paths = append(paths, path)
		}
	}
	if len(paths) > 0 {
		sort.Strings(paths)
		return "Add: import " + paths[0] + ";"
	}
	// Not loaded by anything yet, but there may be a file of that name
	if _, err := r.read(r.path(alias)); err == nil {
		return "Add: import " + alias + ";"
	}
	return ""
}

// moduleOf returns the module an expression names, when it is the bare
// name of an imported module that no local variable hides.
func (r *rewriter) moduleOf(expr ast.Expr) (*module, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || len(ident.TypeArgs) > 0 || r.isLocal(ident.Name) || r.value(ident.Name) != nil {
		return nil, false
	}
	target, ok := r.m.aliases[ident.Name]
	if ok {
		r.usedAliases[ident.Name] = true
	}
	return target, ok
}

// --- Types ---

func (r *rewriter) typ(t *ast.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case ast.TypeBasic:
		t.Name = r.typeName(t.Name, t.Token())
		r.types(t.Generics)
	case ast.TypeArray, ast.TypeOption, ast.TypeVariadic:
		r.typ(t.ElemType)
	case ast.TypeUnion:
		r.types(t.UnionTypes)
	case ast.TypeFunction:
		r.types(t.ParamTypes)
		r.typ(t.ReturnType)
	}
}

func (r *rewriter) types(list []*ast.Type) {
	for _, t := range list {
		r.typ(t)
	}
}

// --- Declarations and statements ---

// bounds rewrites the traits type parameters are required to implement.
func (r *rewriter) bounds(bounds map[string][]*ast.Type) {
	for _, traits := range bounds {
		r.types(traits)
	}
}

// method rewrites a method: a function in which `self` is a variable.
func (r *rewriter) method(d *ast.FnDecl) {
	r.scoped(func() {
		r.declare("self")
		r.fn(d)
	})
}

func (r *rewriter) fn(d *ast.FnDecl) {
	r.withTypeParams(d.TypeParams, func() {
		r.bounds(d.Bounds)
		r.scoped(func() {
			r.params(d.Params)
			r.typ(d.ReturnType)
			r.block(d.Body)
		})
	})
}

func (r *rewriter) params(params []*ast.Param) {
	// What a parameter is when left out is written where the call is: it
	// names what the module names, never another parameter
	for _, param := range params {
		if param.Default != nil {
			param.Default = r.expr(param.Default)
		}
	}
	for _, param := range params {
		r.typ(param.Type)
		r.declare(param.Name)
	}
}

func (r *rewriter) block(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	r.scoped(func() {
		for _, stmt := range b.Stmts {
			r.stmt(stmt)
		}
	})
}

func (r *rewriter) stmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		r.block(s)
	case *ast.LetStmt:
		r.typ(s.Type)
		if s.Value != nil {
			s.Value = r.expr(s.Value)
		}
		r.declare(s.Name)
	case *ast.ReturnStmt:
		if s.Value != nil {
			s.Value = r.expr(s.Value)
		}
	case *ast.IfStmt:
		// The names of `if value is Shape.Circle(r)` are variables of the
		// block that follows, not of the `else`
		r.scoped(func() {
			s.Cond = r.condition(s.Cond)
			r.block(s.Then)
		})
		if s.Else != nil {
			r.stmt(s.Else)
		}
	case *ast.WhileStmt:
		r.scoped(func() {
			s.Cond = r.condition(s.Cond)
			r.block(s.Body)
		})
	case *ast.ForStmt:
		s.Iterable = r.expr(s.Iterable)
		r.scoped(func() {
			r.declare(s.Var)
			if s.Index != nil {
				r.declare(s.Index.Name)
			}
			r.block(s.Body)
		})
	case *ast.MatchStmt:
		s.Cond = r.expr(s.Cond)
		for _, c := range s.Cases {
			// The names a pattern binds are variables of its case
			r.scoped(func() {
				c.Pattern = r.pattern(c.Pattern)
				for i, alternative := range c.Alternatives {
					c.Alternatives[i] = r.pattern(alternative)
				}
				if c.Guard != nil {
					c.Guard = r.expr(c.Guard)
				}
				switch body := c.Body.(type) {
				case *ast.BlockStmt:
					r.block(body)
				case ast.Expr:
					c.Body = r.expr(body)
				}
			})
		}
	case *ast.DeferStmt:
		r.block(s.Body)
	case *ast.ExprStmt:
		s.Expr = r.expr(s.Expr)
	}
}

// pattern rewrites the pattern of a match case. A name that turns out to
// be a type of another module becomes a type pattern: the parser could only
// recognize the types declared in the file it was reading.
func (r *rewriter) pattern(pattern ast.Node) ast.Node {
	switch p := pattern.(type) {
	case nil:
		return nil
	case *ast.Type:
		r.typ(p)
		return p

	case *ast.Ident:
		if !r.isLocal(p.Name) && r.value(p.Name) == nil {
			if it := r.typeItem(p.Name); it != nil {
				r.types(p.TypeArgs)
				return &ast.Type{BaseNode: p.BaseNode, Kind: ast.TypeBasic, Name: it.qualified, Generics: p.TypeArgs}
			}
			// case Empty: a variant of the one enum that has it
			if target := r.variantRef(p); target != nil {
				return &ast.VariantPattern{BaseNode: p.BaseNode, Target: target}
			}
		}
		return r.expr(p)

	case *ast.VariantPattern:
		// `module.Type` reads like `Enum.Variant`; only here is it known
		// which of the two it is
		if member, ok := p.Target.(*ast.MemberExpr); ok && !p.HasArgs {
			if target, ok := r.moduleOf(member.Expr); ok {
				tok := memberToken(member)
				if it, _ := target.lookup(member.Name, make(map[*module]bool)); it != nil && it.isType {
					if it = r.find(r.m, target, member.Name, tok); it == nil {
						return p
					}
					return &ast.Type{BaseNode: ast.BaseNode{Pos: tok.Pos, Tok: tok}, Kind: ast.TypeBasic, Name: it.qualified}
				}
			}
		}
		p.Target = r.expr(p.Target)
		for i, arg := range p.Args {
			p.Args[i] = r.pattern(arg)
		}
		return p

	case *ast.StructPattern:
		if pattern := r.variantByNames(p); pattern != nil {
			return pattern
		}
		r.typ(p.Type)
		for _, field := range p.Fields {
			field.Pattern = r.pattern(field.Pattern)
		}
		return p

	case *ast.BindingPattern:
		// Option.Some(Empty): a variant without values, where it is one,
		// rather than a new name
		if !p.Ignored() && !r.isLocal(p.Name) && r.value(p.Name) == nil {
			ident := &ast.Ident{BaseNode: p.BaseNode, Name: p.Name}
			if target := r.variantRef(ident); target != nil {
				return &ast.VariantPattern{BaseNode: p.BaseNode, Target: target}
			}
		}
		if !p.Ignored() {
			r.declare(p.Name)
		}
		return p

	case *ast.Literal, *ast.UnaryExpr:
		return p

	case ast.Expr:
		return r.expr(p)
	}
	return pattern
}

// condition rewrites what an `if` or a `while` tests, or a chain of `&&`.
// The names of its pattern tests are declared in the scope the caller
// opened, where what the condition guards is rewritten next.
func (r *rewriter) condition(cond ast.Expr) ast.Expr {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		if e.Op == "&&" {
			e.Left = r.condition(e.Left)
			e.Right = r.condition(e.Right)
			return e
		}
	case *ast.PatternTestExpr:
		return r.patternTest(e)
	}
	return r.expr(cond)
}

// patternTest rewrites `value is Pattern`. Two things written as patterns
// turn out here to be tests for a type, as they were before `is` took
// patterns: `value is geo.Shape`, a type of another module, and
// `value is (Point, Label)`, a tuple of types that read like names.
func (r *rewriter) patternTest(e *ast.PatternTestExpr) ast.Expr {
	e.Expr = r.expr(e.Expr)

	if tuple := r.tupleOfTypes(e.Pattern); tuple != nil {
		r.typ(tuple)
		return &ast.TypeTestExpr{BaseNode: e.BaseNode, Expr: e.Expr, Type: tuple}
	}
	switch pattern := r.pattern(e.Pattern).(type) {
	case *ast.Type:
		return &ast.TypeTestExpr{BaseNode: e.BaseNode, Expr: e.Expr, Type: pattern}
	case *ast.VariantPattern:
		e.Pattern = pattern
	}
	return e
}

// tupleOfTypes gives the tuple type a pattern stands for when it is a
// tuple of names that are all types, and nil otherwise.
func (r *rewriter) tupleOfTypes(p *ast.VariantPattern) *ast.Type {
	target, ok := p.Target.(*ast.MemberExpr)
	if !ok || len(p.Args) == 0 {
		return nil
	}
	enum, ok := target.Expr.(*ast.Ident)
	if !ok || !ast.IsTupleName(enum.Name) {
		return nil
	}

	tuple := &ast.Type{BaseNode: p.BaseNode, Kind: ast.TypeBasic, Name: enum.Name}
	for _, arg := range p.Args {
		name, ok := arg.(*ast.BindingPattern)
		if !ok || name.Ignored() || r.isLocal(name.Name) || r.value(name.Name) != nil {
			return nil
		}
		if !r.isTypeParam(name.Name) && r.typeItem(name.Name) == nil {
			return nil
		}
		tuple.Generics = append(tuple.Generics, &ast.Type{BaseNode: name.BaseNode, Kind: ast.TypeBasic, Name: name.Name})
	}
	return tuple
}

// memberToken spans `module.name`, so a diagnostic underlines all of it.
func memberToken(e *ast.MemberExpr) token.Token {
	tok := e.Expr.Token()
	tok.Literal += "." + e.Name
	tok.EndPos = e.Tok.EndPos
	tok.EndPos.Col += len(e.Name)
	return tok
}

// --- Expressions ---

func (r *rewriter) exprs(list []ast.Expr) {
	for i, e := range list {
		list[i] = r.expr(e)
	}
}

func (r *rewriter) expr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.Ident:
		r.types(e.TypeArgs)
		if r.isLocal(e.Name) {
			return e
		}
		if it := r.value(e.Name); it != nil {
			e.Name = it.qualified
			return e
		}
		// An enum is named in expressions to reach its variants: Shape.Circle
		if it := r.typeItem(e.Name); it != nil && it.kind == "enum" {
			e.Name = it.qualified
			return e
		}
		// Circle(2.0), Ok(n): a variant of the one enum that has it
		if target := r.variantRef(e); target != nil {
			return target
		}
		if _, isModule := r.m.aliases[e.Name]; isModule {
			r.report(e.Token(), ErrNotAValue, "Use something the module declares: "+e.Name+".name",
				"'%s' is a module and cannot be used as a value", e.Name)
		}
		return e

	case *ast.MemberExpr:
		r.types(e.TypeArgs)
		if static := r.staticOf(e); static != nil {
			return static
		}
		if target, ok := r.moduleOf(e.Expr); ok {
			return r.moduleMember(e, target)
		}
		e.Expr = r.expr(e.Expr)
		return e

	case *ast.CallExpr:
		e.Func = r.expr(e.Func)
		r.exprs(e.Args)
	case *ast.BinaryExpr:
		if e.Op == "&&" {
			// What a pattern test on the left names is seen on the right
			r.scoped(func() { expr = r.condition(e) })
			return expr
		}
		e.Left = r.expr(e.Left)
		e.Right = r.expr(e.Right)
	case *ast.PatternTestExpr:
		r.scoped(func() { expr = r.patternTest(e) })
		return expr
	case *ast.UnaryExpr:
		e.Expr = r.expr(e.Expr)
	case *ast.CastExpr:
		e.Expr = r.expr(e.Expr)
		r.typ(e.Type)
	case *ast.TypeTestExpr:
		e.Expr = r.expr(e.Expr)
		// value is Empty: a variant, when the name is no type
		if e.Type.Kind == ast.TypeBasic && len(e.Type.Generics) == 0 && !strings.Contains(e.Type.Name, ".") &&
			!builtinTypeNames[e.Type.Name] && r.typeItem(e.Type.Name) == nil && !r.isTypeParam(e.Type.Name) {
			ident := &ast.Ident{BaseNode: e.Type.BaseNode, Name: e.Type.Name}
			if target := r.variantRef(ident); target != nil {
				return &ast.PatternTestExpr{BaseNode: e.BaseNode, Expr: e.Expr, Pattern: &ast.VariantPattern{BaseNode: e.Type.BaseNode, Target: target}}
			}
		}
		r.typ(e.Type)
	case *ast.IndexExpr:
		e.Expr = r.expr(e.Expr)
		e.Index = r.expr(e.Index)
	case *ast.ArrayLiteralExpr:
		r.exprs(e.Elements)
	case *ast.MatchExpr:
		r.stmt(e.Match)
	case *ast.CondExpr:
		e.Cond = r.expr(e.Cond)
		e.Then = r.expr(e.Then)
		e.Else = r.expr(e.Else)
	case *ast.BlockExpr:
		r.scoped(func() {
			for _, stmt := range e.Stmts {
				r.stmt(stmt)
			}
			if e.Value != nil {
				e.Value = r.expr(e.Value)
			}
		})
	case *ast.RangeExpr:
		e.Low = r.expr(e.Low)
		e.High = r.expr(e.High)
		if !e.Counted && r.loader.modules[RangeModule] != nil {
			// Kept as a value: a struct like any other from here on
			yes := "false"
			if e.Inclusive {
				yes = "true"
			}
			literal := func(kind, text string) ast.Expr {
				tok := e.Token()
				tok.Literal = text
				return &ast.Literal{BaseNode: ast.BaseNode{Pos: e.Position(), Tok: tok}, Kind: kind, Value: text}
			}
			return &ast.StructInitExpr{BaseNode: e.BaseNode, Name: RangeStruct, Range: e, Fields: map[string]ast.Expr{
				"from": e.Low, "to": e.High, "inclusive": literal("bool", yes), "by": literal("int", "1"),
			}}
		}

	case *ast.StructInitExpr:
		if call := r.variantLiteral(e); call != nil {
			return call
		}
		e.Name = r.typeName(e.Name, e.Token())
		r.types(e.TypeArgs)
		// In the order of the names, so that diagnostics come out the same
		// way on every run
		fields := make([]string, 0, len(e.Fields))
		for name := range e.Fields {
			fields = append(fields, name)
		}
		sort.Strings(fields)
		for _, name := range fields {
			e.Fields[name] = r.expr(e.Fields[name])
		}

	case *ast.FnExpr:
		r.scoped(func() {
			r.params(e.Params)
			r.typ(e.ReturnType)
			r.block(e.Body)
		})
	}
	return expr
}

// builtinTypeNames are the types a test with `is` names that are no
// variant, whatever the enums in scope have: value is Int.
var builtinTypeNames = map[string]bool{"Int": true, "Float": true, "String": true, "Bool": true, "Any": true, "Void": true, "Self": true}

// variantRef gives `Enum.Variant` for the name of a variant written alone,
// Circle for Shape.Circle, when an enum the module names without a dot has
// a variant of that name and nothing else goes by it: not a variable, a
// function, a constant or a type. When several such enums have it, the
// enum is ast.EnumToFind, for the type checker to choose by the type the
// value has to be.
func (r *rewriter) variantRef(e *ast.Ident) ast.Expr {
	if len(e.TypeArgs) > 0 || r.isLocal(e.Name) || r.value(e.Name) != nil || r.typeItem(e.Name) != nil {
		return nil
	}
	owners := r.variantOwners(e.Name)
	if len(owners) == 0 {
		return nil
	}
	enum := &ast.Ident{BaseNode: e.BaseNode, Name: owners[0].qualified}
	if len(owners) > 1 {
		enum.Name = ast.EnumToFind
		for _, owner := range owners {
			enum.Candidates = append(enum.Candidates, owner.qualified)
		}
	}
	return &ast.MemberExpr{BaseNode: e.BaseNode, Expr: enum, Name: e.Name}
}

// exposes reports the types of the module that a pub function or method
// hands to other modules or asks of them, and that they cannot name: a
// caller would hold a value whose type it cannot write.
func (r *rewriter) exposes(d *ast.FnDecl, what string) {
	if r.m.name == "" {
		return // The entry file is imported by nothing
	}
	own := make(map[string]bool)
	for _, param := range d.TypeParams {
		own[param] = true
	}
	var visit func(t *ast.Type)
	visit = func(t *ast.Type) {
		if t == nil {
			return
		}
		if t.Kind == ast.TypeBasic && !own[t.Name] && !r.isTypeParam(t.Name) {
			if it := r.m.types[t.Name]; it != nil && !it.pub {
				r.report(t.Token(), ErrHiddenType, "Make "+t.Name+" pub, or the "+strings.SplitN(what, " ", 2)[0]+" private",
					"pub %s of %s uses %s, which other modules cannot name", what, r.m.display(), t.Name)
			}
		}
		for _, inner := range t.Generics {
			visit(inner)
		}
		visit(t.ElemType)
		for _, param := range t.ParamTypes {
			visit(param)
		}
		visit(t.ReturnType)
	}
	for _, param := range d.Params {
		visit(param.Type)
	}
	visit(d.ReturnType)
}

// namedVariant finds the variant a name written like a struct names, when
// it names one: Shape.Rect, or Rect alone. The enum is given with its
// name in the program.
func (r *rewriter) namedVariant(name string, tok token.Token) (*item, *ast.EnumVariant) {
	enumName, variantName, dotted := strings.Cut(name, ".")
	var owner *item
	if dotted {
		owner = r.typeItem(enumName)
		if owner == nil || owner.kind != "enum" {
			return nil, nil
		}
	} else {
		if r.typeItem(name) != nil || r.isLocal(name) || r.value(name) != nil {
			return nil, nil
		}
		owners := r.variantOwners(name)
		if len(owners) == 0 {
			return nil, nil
		}
		if len(owners) > 1 {
			var written []string
			for _, o := range owners {
				written = append(written, o.name+"."+name)
			}
			r.report(tok, ErrVariantNames, "Write which: "+strings.Join(written, " or "),
				"'%s' is a variant of more than one enum", name)
			return nil, nil
		}
		owner, variantName = owners[0], name
	}
	if decl := enumDeclOf(owner); decl != nil {
		for _, variant := range decl.Variants {
			if variant.Name == variantName {
				return owner, variant
			}
		}
	}
	return nil, nil
}

// variantLiteral writes `Rect { width: 2.0, height: 1.0 }`, a variant
// with named values, as the call it is, Shape.Rect(2.0, 1.0), its values
// put in the order declared. It is nil for what is no variant.
func (r *rewriter) variantLiteral(e *ast.StructInitExpr) ast.Expr {
	owner, variant := r.namedVariant(e.Name, e.Token())
	if variant == nil {
		return nil
	}
	if len(variant.Names) == 0 {
		r.report(e.Token(), ErrVariantNames, "Write its values in parentheses: "+owner.name+"."+variant.Name+"(...)",
			"%s.%s holds its values by position, not by name", owner.name, variant.Name)
		return nil
	}
	call := &ast.CallExpr{BaseNode: e.BaseNode, Func: &ast.MemberExpr{BaseNode: e.BaseNode, Expr: &ast.Ident{BaseNode: e.BaseNode, Name: owner.qualified, TypeArgs: e.TypeArgs}, Name: variant.Name}}
	for _, name := range variant.Names {
		value, ok := e.Fields[name]
		if !ok {
			r.report(e.Token(), ErrVariantNames, "Give every value of the variant",
				"%s.%s needs a value for '%s'", owner.name, variant.Name, name)
			continue
		}
		call.Args = append(call.Args, r.expr(value))
	}
	for name := range e.Fields {
		if !contains(variant.Names, name) {
			r.report(e.Fields[name].Token(), ErrVariantNames, "Its values are "+strings.Join(variant.Names, ", "),
				"%s.%s has no value '%s'", owner.name, variant.Name, name)
		}
	}
	return call
}

// variantByNames writes `case Rect { width, height: 0.0 }:` as the
// pattern by position it is, Shape.Rect(width, 0.0), with `_` for the
// values not named. It is nil for what is no variant.
func (r *rewriter) variantByNames(p *ast.StructPattern) ast.Node {
	if len(p.Type.Generics) > 0 {
		return nil
	}
	owner, variant := r.namedVariant(p.Type.Name, p.Token())
	if variant == nil {
		return nil
	}
	if len(variant.Names) == 0 {
		r.report(p.Token(), ErrVariantNames, "Write a pattern for each value in parentheses: "+owner.name+"."+variant.Name+"(...)",
			"%s.%s holds its values by position, not by name", owner.name, variant.Name)
		return nil
	}
	target := &ast.MemberExpr{BaseNode: p.BaseNode, Expr: &ast.Ident{BaseNode: p.BaseNode, Name: owner.qualified}, Name: variant.Name}
	pattern := &ast.VariantPattern{BaseNode: p.BaseNode, Target: target, HasArgs: true}
	for _, name := range variant.Names {
		var arg ast.Node = &ast.BindingPattern{BaseNode: p.BaseNode, Name: "_"}
		for _, field := range p.Fields {
			if field.Name == name {
				arg = r.pattern(field.Pattern)
			}
		}
		pattern.Args = append(pattern.Args, arg)
	}
	for _, field := range p.Fields {
		if !contains(variant.Names, field.Name) {
			r.report(p.Token(), ErrVariantNames, "Its values are "+strings.Join(variant.Names, ", "),
				"%s.%s has no value '%s'", owner.name, variant.Name, field.Name)
		}
	}
	return pattern
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// enumDeclOf finds the declaration of an enum.
func enumDeclOf(it *item) *ast.EnumDecl {
	if it == nil || it.kind != "enum" || it.module == nil {
		return nil
	}
	for _, decl := range it.module.prog.Decls {
		if enum, ok := decl.(*ast.EnumDecl); ok && (enum.Name == it.name || enum.Name == it.qualified) {
			return enum
		}
	}
	return nil
}

// variantOwners lists the enums a name of a variant is a variant of, among
// those the module declares or imports by name.
func (r *rewriter) variantOwners(name string) []*item {
	if r.variants == nil {
		r.variants = make(map[string][]*item)
		add := func(it *item) {
			if it.kind != "enum" || it.module == nil {
				return
			}
			for _, decl := range it.module.prog.Decls {
				enum, ok := decl.(*ast.EnumDecl)
				if !ok || (enum.Name != it.name && enum.Name != it.qualified) {
					continue
				}
				for _, variant := range enum.Variants {
					r.variants[variant.Name] = append(r.variants[variant.Name], it)
				}
			}
		}
		for _, names := range []map[string]*item{r.m.types, r.m.importedTypes} {
			// In the order of the names, for messages that come out the
			// same on every run
			var keys []string
			for key := range names {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				add(names[key])
			}
		}
	}
	return r.variants[name]
}

// moduleMember turns `module.name` into a reference to the declaration.
func (r *rewriter) moduleMember(e *ast.MemberExpr, target *module) ast.Expr {
	tok := memberToken(e)
	it := r.find(r.m, target, e.Name, tok)
	if it == nil {
		return e
	}
	if it.isType && it.kind != "enum" {
		r.report(tok, ErrNotAValue, "A struct is created with "+tok.Literal+" { field: value }",
			"'%s' is a %s and cannot be used as a value", tok.Literal, it.kind)
		return e
	}
	return &ast.Ident{
		BaseNode: ast.BaseNode{Pos: tok.Pos, Tok: tok},
		Name:     it.qualified,
		TypeArgs: e.TypeArgs,
	}
}
