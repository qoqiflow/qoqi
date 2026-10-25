package resolve

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/token"
)

// Error codes for name resolution
const (
	ErrUndefined     = "E1001" // Undefined identifier, type, or trait
	ErrRedeclaration = "E1002" // Redeclaration of a name in the same scope
	ErrUnusableName  = "E1003" // A pattern names a value where nothing could use the name
	ErrUnused        = "E1004" // A variable, a parameter or a name of a pattern nothing uses (a warning)
	ErrNotAConstant  = "E1005" // A name a case compares with that is not a constant
	ErrPanicName     = "E1006" // A variable, a parameter or a function named panic
)

type Resolver struct {
	table       *Table
	current     *Scope
	errs        *errors.Handler
	sourceLines map[int]string // Source lines for error context

	// Inside a pattern that is not the first of its case
	namesNothing bool

	// The variables a script declares outside any function, which its
	// functions do not see; inScript is set while its statements are
	// resolved
	scriptVars map[string]bool
	inScript   bool

	// The parameters whose values of their own are being resolved, which
	// may not name each other
	params []*ast.Param

	// The variable whose value is being resolved: a short function that
	// calls it is told how to write one that may
	defining string

	// The variables, parameters and names of patterns of the program's
	// own code, which are said to be unused when nothing names them; and
	// whether what is being resolved is the program's own code, and
	// whether its parameters are its to choose (not those of a trait)
	watched   []*Symbol
	ownCode   bool
	ownParams bool
	unused    bool

	// The variants of the program's enums, which a variant written alone
	// and misspelt may have meant
	variants []string
}

func NewResolver(errs *errors.Handler) *Resolver {
	r := &Resolver{
		table:       NewTable(),
		current:     NewScope(nil), // Global scope
		errs:        errs,
		sourceLines: make(map[int]string),
	}

	// Host-provided functions live in the global scope
	for _, fn := range Builtins() {
		r.current.Insert(fn.Name, &Symbol{Name: fn.Name, Kind: SymbolFunc, Decl: fn})
	}
	return r
}

// WarnUnused has the variables, parameters and names of patterns nothing
// uses said, as warnings.
func (r *Resolver) WarnUnused(on bool) {
	r.unused = on
}

// SetSourceLines sets the source lines for error reporting.
func (r *Resolver) SetSourceLines(lines map[int]string) {
	r.sourceLines = lines
}

func (r *Resolver) Resolve(prog *ast.Program) *Table {
	// Pass 1: Top-level declarations
	r.resolveTopLevel(prog)

	// Pass 2: Function bodies and other executable code
	r.resolveBody(prog)
	if r.unused {
		r.reportUnused()
	}

	return r.table
}

func (r *Resolver) resolveTopLevel(prog *ast.Program) {
	// Functions the runtime provides are names like any other function
	for _, fn := range prog.Externs {
		r.declare(fn.Name, SymbolFunc, fn)
	}
	// So are the variables outside functions, for all of them
	for _, global := range prog.Globals {
		r.declare(global.Name, SymbolVar, global)
	}

	for _, decl := range prog.Decls {
		if enum, ok := decl.(*ast.EnumDecl); ok && !strings.HasPrefix(enum.Name, stdlib.Package+".") {
			for _, variant := range enum.Variants {
				r.variants = append(r.variants, variant.Name)
			}
		}
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			r.declare(d.Name, SymbolFunc, d)
			if d.Script {
				r.scriptVars = map[string]bool{}
				for _, stmt := range d.Body.Stmts {
					if let, ok := stmt.(*ast.LetStmt); ok {
						r.scriptVars[let.Name] = true
					}
				}
			}
		case *ast.ConstDecl:
			// A constant is used like a variable that is never assigned
			r.declare(d.Name, SymbolVar, d)
		case *ast.StructDecl:
			r.declare(d.Name, SymbolStruct, d)
		case *ast.EnumDecl:
			r.declare(d.Name, SymbolEnum, d)
		case *ast.TraitDecl:
			r.declare(d.Name, SymbolTrait, d)
			// ImplDecl doesn't introduce a new name in the global scope in the same way,
			// but we might need to resolve the struct and trait names it refers to.
			// For now, we just register the named entities.
		}
	}
}

func (r *Resolver) declare(name string, kind SymbolKind, node ast.Node) {
	if name == "panic" && r.ownCode {
		// What follows a call of panic is known never to run, by its name:
		// a panic of the program's own would make that wrong
		d := errors.NewDiagnosticFromToken(errors.SeverityError, ErrPanicName, "panic is the language's: it ends the program, and nothing may be named so", node.Token(), r.sourceLines)
		d.Suggestion = "Give it another name: fail, abort, stop"
		r.errs.Add(d)
	}
	sym := &Symbol{
		Name: name,
		Kind: kind,
		Decl: node,
	}
	if err := r.current.Insert(name, sym); err != nil {
		r.errs.ReportErrorFromToken(ErrRedeclaration, err.Error(), node.Token(), r.sourceLines)
		return
	}
	if kind == SymbolVar && r.ownCode && !strings.HasPrefix(name, "_") && name != "self" {
		if _, isParam := node.(*ast.Param); !isParam || r.ownParams {
			r.watched = append(r.watched, sym)
		}
	}
}

// reportUnused warns of the variables, parameters and names of patterns
// nothing uses: a mistake, or something left over.
func (r *Resolver) reportUnused() {
	for _, sym := range r.watched {
		if sym.Used {
			continue
		}
		what, fix := "variable", "Remove it, or start its name with _ if it has to be there"
		switch sym.Decl.(type) {
		case *ast.Param:
			what = "parameter"
		case *ast.BindingPattern:
			what, fix = "name", "Write _ in its place"
		case *ast.ForStmt, *ast.ForIndex:
			fix = "Write _ in its place"
		}
		r.errs.ReportWarningWithSuggestion(ErrUnused, fmt.Sprintf("%s '%s' is never used", what, sym.Name), fix, nameToken(sym.Decl), r.sourceLines)
	}
}

// nameToken is where a declaration writes its name, as far as it is known.
func nameToken(node ast.Node) token.Token {
	if let, ok := node.(*ast.LetStmt); ok && let.NameTok.Literal != "" {
		return let.NameTok
	}
	return node.Token()
}

func (r *Resolver) resolveBody(prog *ast.Program) {
	// The variables outside functions: what makes each sees the names of
	// the file
	for _, global := range prog.Globals {
		if global.Type != nil {
			r.visitType(global.Type)
		}
		r.visitExpr(global.Value)
	}
	for _, decl := range prog.Decls {
		r.visitDecl(decl)
	}
}

func (r *Resolver) visitDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		// A function's parameters are its own to choose
		r.ownParams = true
		r.visitFnDecl(d)
		r.ownParams = false
	case *ast.ConstDecl:
		if d.Type != nil {
			r.visitType(d.Type)
		}
		r.visitExpr(d.Value)
	case *ast.StructDecl:
		r.visitStructDecl(d)
	case *ast.EnumDecl:
		r.visitEnumDecl(d)
	case *ast.TraitDecl:
		r.visitTraitDecl(d)
	case *ast.ImplDecl:
		r.visitImplDecl(d)
	}
}

func (r *Resolver) visitFnDecl(d *ast.FnDecl) {
	// Create new scope for function
	r.pushScope(d)
	defer r.popScope()
	// What the library declares and does not use is its own business
	savedOwn, savedParams := r.ownCode, r.ownParams
	r.ownCode = !strings.HasPrefix(d.Module, stdlib.Package+".") && !strings.HasPrefix(d.Name, stdlib.Package+".") && d.Body != nil
	// A step its queue carries out has its parameters for the application
	r.ownParams = r.ownParams && d.Name != "main" && d.Queue == ""
	defer func() { r.ownCode, r.ownParams = savedOwn, savedParams }()
	inScript := r.inScript
	r.inScript = d.Script
	defer func() { r.inScript = inScript }()

	// Register type parameters
	for _, tp := range d.TypeParams {
		// Type parameters are declared in the function scope.
		// Since we don't have a specific AST node for the type parameter name,
		// we use the FnDecl node for error reporting context.
		sym := &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: d}
		r.current.Insert(tp, sym)
	}
	r.visitBounds(d.Bounds)

	// What the parameters are when left out names what the module does,
	// not the other parameters
	r.params = d.Params
	for _, param := range d.Params {
		if param.Default != nil {
			r.visitExpr(param.Default)
		}
	}
	r.params = nil

	// Register parameters
	for _, param := range d.Params {
		r.visitType(param.Type)
		r.declare(param.Name, SymbolVar, param)
	}

	// Resolve return type
	if d.ReturnType != nil {
		r.visitType(d.ReturnType)
	}

	// Resolve body
	if d.Body != nil {
		r.visitBlockStmt(d.Body)
	}
}

func (r *Resolver) visitStructDecl(d *ast.StructDecl) {
	// Type parameters are visible in the field types
	r.pushScope(d)
	defer r.popScope()
	for _, tp := range d.TypeParams {
		r.current.Insert(tp, &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: d})
	}

	r.visitBounds(d.Bounds)

	// Resolve field types, and the values fields have when a literal does
	// not give one: those see what the top level declares, nothing else
	for _, f := range d.Fields {
		r.visitType(f.Type)
		if f.Default != nil {
			r.visitExpr(f.Default)
		}
	}
}

// visitBounds resolves the traits type parameters are required to
// implement.
func (r *Resolver) visitBounds(bounds map[string][]*ast.Type) {
	for _, traits := range bounds {
		for _, trait := range traits {
			r.visitType(trait)
		}
	}
}

func (r *Resolver) visitEnumDecl(d *ast.EnumDecl) {
	// Type parameters are visible in the types of the variants' values
	r.pushScope(d)
	defer r.popScope()
	for _, tp := range d.TypeParams {
		r.current.Insert(tp, &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: d})
	}
	r.visitBounds(d.Bounds)

	for _, v := range d.Variants {
		for _, t := range v.Values {
			r.visitType(t)
		}
	}
}

func (r *Resolver) visitTraitDecl(d *ast.TraitDecl) {
	// The type parameters of the trait, and `self`, are visible in its
	// methods: in the signatures, and in the bodies of those that have one
	r.pushScope(d)
	defer r.popScope()
	r.current.Insert("self", &Symbol{Name: "self", Kind: SymbolVar, Decl: d})
	for _, tp := range d.TypeParams {
		r.current.Insert(tp, &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: d})
	}
	for _, m := range d.Methods {
		r.visitFnDecl(m)
	}
}

func (r *Resolver) visitImplDecl(d *ast.ImplDecl) {
	// Verify that the struct and trait names exist in the scope.
	// Note: Since these are stored as strings in the AST, we cannot link them
	// in the resolution table (which requires *ast.Ident).
	if sym := r.current.Lookup(d.StructName); sym == nil {
		// Check if it's a built-in type (e.g. impl MyTrait for Int)
		if !isBuiltinType(d.StructName) {
			r.reportUndefined("undefined type", d.StructName, d.Token())
		}
	}

	if d.TraitName != "" {
		if sym := r.current.Lookup(d.TraitName); sym == nil {
			r.reportUndefined("undefined trait", d.TraitName, d.Token())
		}
	}

	// impl<T: Ordered> Source<T> for List<T>: the requirements and the
	// trait's type arguments may name the impl's type parameters
	r.pushScope(d.TraitArgsScope())
	for _, tp := range d.TypeParams {
		r.current.Insert(tp, &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: d})
	}
	r.visitBounds(d.Bounds)
	for _, arg := range d.TraitArgs {
		r.visitType(arg)
	}
	r.popScope()

	for _, m := range d.Methods {
		r.visitMethodDecl(d, m)
	}
}

// visitMethodDecl resolves an impl method, which has an implicit `self`.
func (r *Resolver) visitMethodDecl(impl *ast.ImplDecl, m *ast.FnDecl) {
	r.pushScope(impl)
	defer r.popScope()

	r.current.Insert("self", &Symbol{Name: "self", Kind: SymbolVar, Decl: impl})
	for _, tp := range impl.TypeParams {
		r.current.Insert(tp, &Symbol{Name: tp, Kind: SymbolTypeParam, Decl: impl})
	}
	// The parameters of a method a trait asks for are the trait's
	saved := r.ownParams
	r.ownParams = impl.TraitName == ""
	r.visitFnDecl(m)
	r.ownParams = saved
}

func (r *Resolver) visitBlockStmt(b *ast.BlockStmt) {
	r.pushScope(b)
	defer r.popScope()

	for _, stmt := range b.Stmts {
		r.visitStmt(stmt)
	}
}

func (r *Resolver) visitStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		r.visitBlockStmt(s)
	case *ast.LetStmt:
		r.visitLetStmt(s)
	case *ast.ReturnStmt:
		if s.Value != nil {
			r.visitExpr(s.Value)
		}
	case *ast.IfStmt:
		if test := ast.GuardTest(s); test != nil {
			// if !(value is Ok(n)) { return; }: what the test names is a
			// variable of what follows, which only runs when it held
			r.pushScope(s)
			r.visitBlockStmt(s.Then)
			r.popScope()
			r.visitCondition(test, true)
			return
		}
		// What `if value is Shape.Circle(r)` names is a variable of the
		// block that runs when the test held; the `else` does not see it
		r.pushScope(s)
		r.visitCondition(s.Cond, true)
		r.visitBlockStmt(s.Then)
		r.popScope()
		if s.Else != nil {
			r.visitStmt(s.Else)
		}
	case *ast.WhileStmt:
		r.pushScope(s)
		r.visitCondition(s.Cond, true)
		r.visitBlockStmt(s.Body)
		r.popScope()
	case *ast.ForStmt:
		r.visitForStmt(s)
	case *ast.MatchStmt:
		r.visitMatchStmt(s)
	case *ast.DeferStmt:
		r.visitBlockStmt(s.Body)
	case *ast.ExprStmt:
		r.visitExpr(s.Expr)
		if s.Stage != "" {
			r.table.Stages[s] = r.visibleVariables()
		}
	case *ast.BreakStmt, *ast.ContinueStmt:
		// Nothing to resolve
	}
}

func (r *Resolver) visitLetStmt(s *ast.LetStmt) {
	if s.Type != nil {
		r.visitType(s.Type)
	}
	if fn, ok := s.Value.(*ast.FnExpr); ok && !fn.Short && !s.IsConst {
		// let walk = fn(node: Node) { ... walk(child) ... }: a function
		// written with its types sees its own name
		r.declare(s.Name, SymbolVar, s)
		r.visitExpr(s.Value)
		return
	}
	if s.Value != nil {
		saved := r.defining
		r.defining = s.Name
		r.visitExpr(s.Value)
		r.defining = saved
	}
	// Declare variable AFTER resolving initializer
	r.declare(s.Name, SymbolVar, s)
}

func (r *Resolver) visitForStmt(s *ast.ForStmt) {
	r.visitExpr(s.Iterable)

	// For loop creates a scope for the loop variable
	r.pushScope(s)
	defer r.popScope()

	// Declare loop variable
	// We don't have a separate node for the variable name, so we use the ForStmt
	sym := &Symbol{Name: s.Var, Kind: SymbolVar, Decl: s}
	if err := r.current.Insert(s.Var, sym); err != nil {
		r.errs.ReportErrorFromToken(ErrRedeclaration, err.Error(), s.Token(), r.sourceLines)
	}
	if s.Index != nil {
		r.declare(s.Index.Name, SymbolVar, s.Index)
	}

	r.visitBlockStmt(s.Body)
}

func (r *Resolver) visitMatchStmt(s *ast.MatchStmt) {
	r.visitExpr(s.Cond)
	for _, c := range s.Cases {
		r.visitMatchCase(c)
	}
}

func (r *Resolver) visitMatchCase(c *ast.MatchCase) {
	// Match case creates a scope
	r.pushScope(c)
	defer r.popScope()

	// Visit pattern - this handles pattern matching variable binding
	r.visitPattern(c.Pattern)
	// A case with several patterns names nothing: the type checker says
	// so, better than a complaint here about a name declared twice
	r.namesNothing = true
	for _, alternative := range c.Alternatives {
		r.visitPattern(alternative)
	}
	r.namesNothing = false
	// The condition of the case sees the names of its pattern
	if c.Guard != nil {
		r.visitExpr(c.Guard)
	}

	// Body can be either BlockStmt or Expr
	switch body := c.Body.(type) {
	case *ast.BlockStmt:
		r.visitBlockStmt(body)
	case ast.Expr:
		r.visitExpr(body)
	}
}

// visitPattern handles pattern matching, including variable binding.
// Currently supports:
// - Literal patterns (e.g., 1, "hello")
// - Identifier patterns (e.g., x - binds a new variable)
// - Enum variant patterns (e.g., Some(x) - not fully implemented)
// - Struct patterns (e.g., Point { x, y } - not fully implemented)
func (r *Resolver) visitPattern(pattern ast.Node) {
	switch p := pattern.(type) {
	case *ast.VariantPattern:
		// `Shape.Rect(w, h)`: Shape is looked up, w and h are new variables
		r.visitExpr(p.Target)
		for _, arg := range p.Args {
			r.visitPattern(arg)
		}

	case *ast.StructPattern:
		// `Point { x: 0, y }`: the struct is looked up, y is a new variable
		r.visitType(p.Type)
		for _, field := range p.Fields {
			r.visitPattern(field.Pattern)
		}

	case *ast.BindingPattern:
		if !p.Ignored() && !r.namesNothing {
			r.declare(p.Name, SymbolVar, p)
		}

	case *ast.Ident:
		// A name by itself is what a case compares with, as if its value
		// were written there: a constant, `case HAND_OVER:`. Inside the
		// pattern of a variant or a struct a name is a variable to bind,
		// a BindingPattern
		r.visitConstPattern(p)

	case *ast.Literal:
		// Literal patterns don't bind variables, just match values
		// Nothing to resolve

	case *ast.CallExpr:
		// This could be an enum variant pattern like Some(x)
		// The function name should be resolved (it's the variant name)
		r.visitExpr(p.Func)
		// Arguments could be patterns that bind variables
		for _, arg := range p.Args {
			r.visitPattern(arg)
		}

	case *ast.StructInitExpr:
		// This could be a struct pattern like Point { x, y }
		// Field values could be patterns that bind variables
		for _, field := range p.Fields {
			r.visitPattern(field)
		}

	case ast.Expr:
		// Fallback: treat as expression (for backwards compatibility)
		// This handles other expression types that might appear in patterns
		r.visitExpr(p)

	default:
		// Unknown pattern type - ignore
	}
}

// visitConstPattern resolves the name a case is written with, which has
// to be a top-level constant: the case compares the value with it. A
// variable, a parameter or a function changes, or is not a value to
// compare with, and the value of a case is known when the program is
// compiled.
func (r *Resolver) visitConstPattern(p *ast.Ident) {
	if p.Name == "_" {
		r.errs.ReportErrorWithSuggestion(ErrNotAConstant,
			"'_' alone cannot be the pattern of a case",
			"A case for every other value is written default:", p.Token(), r.sourceLines)
		return
	}
	sym := r.current.Lookup(p.Name)
	if sym != nil {
		if _, ok := sym.Decl.(*ast.ConstDecl); ok {
			r.table.Defs[p] = sym
			sym.Used = true
			return
		}
	}
	what := "nothing of that name is declared"
	if sym != nil {
		// Named, if wrongly: not also said to be unused
		sym.Used = true
		switch decl := sym.Decl.(type) {
		case *ast.Param:
			what = "it is a parameter"
		case *ast.LetStmt:
			what = "it is a variable"
			if decl.IsConst {
				what = "it is a local constant"
			}
		case *ast.FnDecl:
			what = "it is a function"
		default:
			what = "it is not a constant"
		}
	}
	name := p.Tok.Literal
	if name == "" {
		name = p.Name
	}
	r.errs.ReportErrorWithSuggestion(ErrNotAConstant,
		fmt.Sprintf("'%s' cannot be a pattern: %s; a pattern names a constant or a literal", name, what),
		fmt.Sprintf("To compare with a variable, add a condition to a pattern that takes any value: case Int if value == %s:", name),
		p.Token(), r.sourceLines)
}

// visitCondition resolves what an `if` or a `while` tests, or a chain of
// `&&`. A pattern test that has to hold for the whole to hold declares its
// names in the scope the caller opened: they are seen by what stands to
// its right in the chain, and by what the caller resolves next in that
// scope, the block the condition guards.
//
// guards says whether there is such a block. Without one, in a chain that
// is only a value, the names of its last test have nothing to be seen by.
func (r *Resolver) visitCondition(cond ast.Expr, guards bool) {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		if e.Op == "&&" {
			r.visitCondition(e.Left, true)
			r.visitCondition(e.Right, guards)
			return
		}
	case *ast.PatternTestExpr:
		if !guards {
			r.rejectNames(e)
			return
		}
		r.visitExpr(e.Expr)
		r.visitPattern(e.Pattern)
		return
	}
	r.visitExpr(cond)
}

// rejectNames reports the names of a pattern test that stands where
// nothing runs only when it held: under `||` or `!`, or as a value by
// itself. Its variant is still looked up.
func (r *Resolver) rejectNames(e *ast.PatternTestExpr) {
	r.visitExpr(e.Expr)
	for _, name := range e.Pattern.Bindings() {
		r.errs.ReportErrorWithSuggestion(ErrUnusableName,
			fmt.Sprintf("'%s' cannot be named here: nothing runs only when this test holds", name.Name),
			"Write '_' to test without naming. A name can be used when the test is the condition of an 'if' or a 'while', stands before an '&&', or is the test of an if that leaves when it fails: if !(value is Ok(n)) { return; }",
			name.Token(), r.sourceLines)
	}
	r.namesNothing = true
	r.visitPattern(e.Pattern)
	r.namesNothing = false
}

func (r *Resolver) visitExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.PatternTestExpr:
		r.rejectNames(e)
	case *ast.BinaryExpr:
		if e.Op == "&&" && len(ast.PatternTests(e)) > 0 {
			// `shape is Shape.Circle(r) && r > 1.0` as a value: the names
			// live as long as the chain
			r.pushScope(e)
			r.visitCondition(e, false)
			r.popScope()
			return
		}
		r.visitExpr(e.Left)
		if holder := ast.SafeHolder(e); holder != nil {
			// value?.member: the right side names the value by a name of
			// its own, which exists there only
			r.pushScope(e)
			r.declare(holder.Name, SymbolVar, e)
			r.visitExpr(e.Right)
			r.popScope()
			return
		}
		r.visitExpr(e.Right)
	case *ast.UnaryExpr:
		r.visitExpr(e.Expr)
	case *ast.CallExpr:
		r.visitExpr(e.Func)
		for _, arg := range e.Args {
			r.visitExpr(arg)
		}
	case *ast.MemberExpr:
		r.visitExpr(e.Expr)
		// e.Name is a property name, not a variable, so we don't resolve it here.
		for _, arg := range e.TypeArgs {
			r.visitType(arg)
		}
	case *ast.IndexExpr:
		r.visitExpr(e.Expr)
		r.visitExpr(e.Index)
	case *ast.StructInitExpr:
		// Resolve struct name? It is a string in AST.
		// Fields
		for _, v := range e.Fields {
			r.visitExpr(v)
		}
	case *ast.ArrayLiteralExpr:
		for _, elem := range e.Elements {
			r.visitExpr(elem)
		}
	case *ast.FnExpr:
		r.visitFnExpr(e)
	case *ast.CastExpr:
		r.visitExpr(e.Expr)
		r.visitType(e.Type)
	case *ast.TypeTestExpr:
		r.visitExpr(e.Expr)
		r.visitType(e.Type)
	case *ast.VariantExpr:
		for _, t := range e.TypeArgs {
			r.visitType(t)
		}
		for _, arg := range e.Args {
			r.visitExpr(arg)
		}
	case *ast.VariantTestExpr:
		r.visitExpr(e.Expr)
	case *ast.VariantFieldExpr:
		r.visitExpr(e.Expr)
	case *ast.MatchExpr:
		r.visitMatchStmt(e.Match)
	case *ast.RangeExpr:
		r.visitExpr(e.Low)
		r.visitExpr(e.High)
	case *ast.BlockExpr:
		r.pushScope(e)
		for _, stmt := range e.Stmts {
			r.visitStmt(stmt)
		}
		if e.Value != nil {
			r.visitExpr(e.Value)
		}
		r.popScope()
	case *ast.CondExpr:
		// As for the statement: what `value is Shape.Circle(r)` names is
		// a variable of the branch taken when the test held
		r.pushScope(e)
		r.visitCondition(e.Cond, true)
		r.visitExpr(e.Then)
		r.popScope()
		r.visitExpr(e.Else)
	case *ast.Ident:
		r.resolveIdent(e)
	case *ast.Literal:
		// Nothing to resolve
	}
}

func (r *Resolver) visitFnExpr(e *ast.FnExpr) {
	r.pushScope(e)
	defer r.popScope()
	// The parameters of a function written where it is used are what it
	// is handed to asks for: (sum, item) -> sum
	saved := r.ownParams
	r.ownParams = false
	defer func() { r.ownParams = saved }()

	for _, param := range e.Params {
		r.visitType(param.Type)
		r.declare(param.Name, SymbolVar, param)
	}
	if e.ReturnType != nil {
		r.visitType(e.ReturnType)
	}
	if e.Body != nil {
		r.visitBlockStmt(e.Body)
	}
}

func (r *Resolver) resolveIdent(id *ast.Ident) {
	switch id.Name {
	case "Int", "Float", "String", "Bool":
		// String.upper: the type, whose method is named
		return
	}
	if id.Name == ast.EnumToFind {
		// The enum of a variant written alone, which the type checker
		// finds
		return
	}
	sym := r.current.Lookup(id.Name)
	if sym == nil {
		r.reportUndefined("undefined identifier", id.Name, id.Token())
		return
	}
	r.table.Defs[id] = sym
	sym.Used = true
}

func (r *Resolver) visitType(t *ast.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case ast.TypeBasic:
		// Check if the type exists in the current scope.
		sym := r.current.Lookup(t.Name)
		if sym == nil {
			// Is it a built-in type?
			if !isBuiltinType(t.Name) {
				r.reportUndefined("undefined type", t.Name, t.Token())
			}
		}
		for _, g := range t.Generics {
			r.visitType(g)
		}
	case ast.TypeFunction:
		for _, p := range t.ParamTypes {
			r.visitType(p)
		}
		r.visitType(t.ReturnType)
	case ast.TypeArray, ast.TypeOption, ast.TypeVariadic:
		r.visitType(t.ElemType)
	case ast.TypeUnion:
		for _, u := range t.UnionTypes {
			r.visitType(u)
		}
	}
}

func (r *Resolver) pushScope(node ast.Node) {
	scope := NewScope(r.current)
	r.table.Scopes[node] = scope
	r.current = scope
}

func (r *Resolver) popScope() {
	r.current = r.current.Parent
}

func isBuiltinType(name string) bool {
	if _, arrays := ast.IsArrayImpl(name); arrays {
		return true
	}
	switch name {
	case "Int", "Float", "String", "Bool", "Void", "Any", "Self", ast.ArrayImpl, ast.OptionalImpl:
		return true
	}
	return false
}

// findSimilarName finds a similar name in scope using Levenshtein distance.
// reportUndefined says that a name is not declared, with what usually
// fixes it: the import that brings it, when the standard library offers
// it, or a name in scope spelt nearly the same.
func (r *Resolver) reportUndefined(what, name string, tok token.Token) {
	msg := fmt.Sprintf("%s '%s'", what, name)
	suggestion := ""
	similar := r.findSimilarName(name)
	// A name the library offers exactly is a surer guess than one spelt
	// nearly the same, except for a value: a misspelt variable is likelier
	// than a function of the library called without its module
	if what == "undefined identifier" && r.scriptVars[name] && !r.inScript {
		msg = fmt.Sprintf("%s '%s': a variable of the script, which a function does not see", what, name)
		suggestion = fmt.Sprintf("Pass %s to the function, or declare it with const if it never changes", name)
	} else if what == "undefined identifier" && isLibraryModule(name) {
		suggestion = fmt.Sprintf("'%s' is a module of the library: import %s.%s;", name, stdlib.Package, name)
	} else if modules := stdlib.Declaring(name); len(modules) > 0 && (what != "undefined identifier" || similar == "") {
		suggestion = importSuggestion(name, modules)
	} else if similar != "" {
		suggestion = fmt.Sprintf("Did you mean '%s'?", similar)
	}
	for _, param := range r.params {
		if what == "undefined identifier" && param.Name == name {
			suggestion = "The value of a parameter is made where the call is, before the parameters exist: use a constant, a literal or a call of a function"
		}
	}
	if what == "undefined identifier" && name == r.defining && name != "" {
		suggestion = fmt.Sprintf("A function sees its own name when it is written with its types: let %s = fn(n: Int) -> Int { ... %s(...) ... };", name, name)
	}
	if what == "undefined identifier" && r.aloneInInsertion(tok) {
		// "cost: ${price}" may be a text that means the characters
		literally := fmt.Sprintf("To write ${%s} itself in the text, write \\${%s}", name, name)
		if suggestion == "" {
			suggestion = literally
		} else {
			suggestion += " " + literally
		}
	}
	if suggestion != "" {
		r.errs.ReportErrorWithSuggestion(ErrUndefined, msg, suggestion, tok, r.sourceLines)
	} else {
		r.errs.ReportErrorFromToken(ErrUndefined, msg, tok, r.sourceLines)
	}
}

// aloneInInsertion reports whether a name is all there is between the
// `${` and the `}` of a text.
func (r *Resolver) aloneInInsertion(tok token.Token) bool {
	line := r.sourceLines[tok.Pos.Line]
	start, end := tok.Pos.Col-1, tok.Pos.Col-1+len(tok.Literal)
	return start >= 2 && end < len(line) && line[start-2:start] == "${" && line[end] == '}'
}

// isLibraryModule reports whether a name is that of a module of the
// library, used without importing it: dict.empty<Int>().
func isLibraryModule(name string) bool {
	for _, module := range stdlib.Modules() {
		if module == stdlib.Package+"."+name {
			return true
		}
	}
	return false
}

// importSuggestion names the import that brings a name of the library.
func importSuggestion(name string, modules []string) string {
	imports := make([]string, len(modules))
	for i, module := range modules {
		imports[i] = fmt.Sprintf("import %s.{%s};", module, name)
	}
	if len(imports) == 1 {
		return fmt.Sprintf("'%s' is in %s: %s", name, modules[0], imports[0])
	}
	return fmt.Sprintf("'%s' is in %s: %s", name, strings.Join(modules, ", "), strings.Join(imports, " or "))
}

func (r *Resolver) findSimilarName(target string) string {
	bestDist := 1000
	bestName := ""

	// Walk scopes
	curr := r.current
	seen := make(map[string]bool)

	for curr != nil {
		for name := range curr.Symbols {
			if seen[name] {
				continue
			}
			seen[name] = true
			dist := levenshtein(target, name)
			if dist < bestDist {
				bestDist = dist
				bestName = name
			}
		}
		curr = curr.Parent
	}

	// Check built-ins too, and the variants, which are written alone
	builtins := append([]string{"Int", "Float", "String", "Bool", "Void", "Any"}, r.variants...)
	for _, name := range builtins {
		dist := levenshtein(target, name)
		if dist < bestDist {
			bestDist = dist
			bestName = name
		}
	}

	// Threshold: distance <= 3 or distance is less than half the target length
	if bestDist <= 3 || (bestDist < len(target)/2 && bestDist < 5) {
		return bestName
	}

	return ""
}

// ClosestName returns the candidate most similar to target, or "" when none
// is close enough to be worth suggesting.
func ClosestName(target string, candidates []string) string {
	bestDist := 1000
	bestName := ""
	for _, name := range candidates {
		if dist := levenshtein(target, name); dist < bestDist || (dist == bestDist && name < bestName) {
			bestDist = dist
			bestName = name
		}
	}
	if bestDist <= 3 || (bestDist < len(target)/2 && bestDist < 5) {
		return bestName
	}
	return ""
}

// Distance is how many characters must be inserted, removed or replaced
// to turn one name into the other.
func Distance(a, b string) int {
	return levenshtein(a, b)
}

// levenshtein computes the Levenshtein distance between two strings.
func levenshtein(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	n, m := len(r1), len(r2)

	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	row := make([]int, m+1)
	for i := 0; i <= m; i++ {
		row[i] = i
	}

	for i := 1; i <= n; i++ {
		prev := i
		var current int
		for j := 1; j <= m; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			current = min(min(row[j]+1, prev+1), row[j-1]+cost)
			row[j-1] = prev
			prev = current
		}
		row[m] = prev
	}

	return row[m]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// visibleVariables are the variables of the function being resolved that
// are in scope here, the innermost of a name only.
func (r *Resolver) visibleVariables() []*Symbol {
	seen := map[string]bool{}
	var vars []*Symbol
	for scope := r.current; scope != nil && scope.Parent != nil; scope = scope.Parent {
		for name, sym := range scope.Symbols {
			if sym.Kind == SymbolVar && !seen[name] {
				seen[name] = true
				vars = append(vars, sym)
			}
		}
	}
	return vars
}
