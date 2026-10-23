// Package flows checks the one rule of a flow: inside a flow, and in every
// function it calls, nothing reaches outside the program except through a
// step (see docs/flow.md).
//
// A program is closed: every function it can call is in it when it is
// compiled, and generic functions have been copied for each type they are
// used with. The check is a walk from the `run` of each flow:
//
//   - a call by name goes to that function;
//   - a call of a method goes to the method of the receiver's type, or,
//     when only a trait is known, to every method of that name;
//   - a call of a function value goes to every function of the same arity
//     whose value is taken in what the walk has reached, or kept in a
//     variable of the program's own, outside any function: the state of a
//     flow is data, and no other function value comes into it;
//   - a native given a function, which it may call, calls every function
//     whose value is taken, whatever its arity;
//   - what the machine calls of itself, the show, equals and compare of a
//     type, when it formats, compares or sorts a value that holds one, is
//     reached from every flow;
//   - a function written inside another is part of it;
//   - a step is where the walk stops.
//
// What the walk reaches may not call a native function of the world
// (files, the network, other programs). It reports, for each, the call in
// the program's own code that leads there.
package flows

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowlock"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

const (
	// FlowTrait is what a struct implements to be a flow.
	FlowTrait = "std.flow.Flow"

	// ErrReachesWorld is a flow that reaches outside the program outside
	// a step.
	ErrReachesWorld = "F0001"

	// maxNotes is how many calls of the way to the world are said; the
	// rest is counted
	maxNotes = 6
)

// fn is a function of the program, with what its body does.
type fn struct {
	decl  *ast.FnDecl
	owner string // the type of a method, "" for a function

	calls    []call // to functions of the program, by name or as methods
	world    []call // to natives of the world: target is nil, native says which
	values   []call // calls of a function value: arity says how many arguments
	taken    []*fn  // functions whose value is taken here
	suspends bool   // it waits itself, in a native where a task waits
	scanned  bool
	reached  bool
	from     *fn         // the function the walk came from
	fromCall token.Token // and the call it came by
}

type call struct {
	at     token.Token
	target *fn
	native string
	arity  int
}

// Check walks every flow of the program and reports what reaches the
// world outside a step. queues are those the project declares, which
// steps the application carries out are by.
// lock, when given, is the version deployed last: a stage this version
// changed of it, which no migration answers, is said.
func Check(prog *ast.Program, table *resolve.Table, info *types.Info, errs *errors.Handler, lines map[int]string, queues []string, lock *flowlock.Lock) {
	c := &checker{
		table: table, info: info, errs: errs, lines: lines,
		fns: map[*ast.FnDecl]*fn{}, methods: map[string]map[string][]*fn{}, byName: map[string][]*fn{},
	}
	c.index(prog)
	c.keptInGlobals(prog)
	c.checkQueues(prog, queues)
	roots := c.roots(prog)
	c.checkStages(prog, roots)
	c.checkCancelled(roots)
	// A migration computes, as run does: the world is not for it either
	migrations := c.checkMigrations(prog, roots)
	c.checkLock(prog, roots, lock)
	roots = append(roots, migrations...)
	if len(roots) == 0 {
		return
	}
	c.walk(roots)
	// Then what the machine calls of itself, unless a flow calls it: the
	// way to the world is said from the flow then
	c.walk(c.calledByTheMachine(prog))
	c.report()
	c.checkCallbacks(prog)
}

type checker struct {
	table *resolve.Table
	info  *types.Info
	errs  *errors.Handler
	lines map[int]string

	fns     map[*ast.FnDecl]*fn
	methods map[string]map[string][]*fn // type, then method name
	byName  map[string][]*fn            // methods by name alone, for a call through a trait
	order   []*fn
	// global are the functions whose value is kept in a variable outside
	// any function, where any code may have put it
	global []*fn
}

func (c *checker) add(decl *ast.FnDecl, owner string) {
	f := &fn{decl: decl, owner: owner}
	c.fns[decl] = f
	c.order = append(c.order, f)
	if owner != "" {
		if c.methods[owner] == nil {
			c.methods[owner] = map[string][]*fn{}
		}
		c.methods[owner][decl.Name] = append(c.methods[owner][decl.Name], f)
		c.byName[decl.Name] = append(c.byName[decl.Name], f)
	}
}

func (c *checker) index(prog *ast.Program) {
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			c.add(d, "")
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				c.add(method, d.StructName)
			}
		}
	}
}

// roots are the `run` of every struct that implements Flow<T>.
func (c *checker) roots(prog *ast.Program) []*fn {
	var roots []*fn
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != FlowTrait {
			continue
		}
		for _, method := range impl.Methods {
			if method.Name == "run" {
				roots = append(roots, c.fns[method])
			}
		}
	}
	return roots
}

// walk reaches every function the flows can run, until nothing more is
// found: a call of a function value can go to a function whose value is
// only taken in a function reached later.
func (c *checker) walk(roots []*fn) {
	var queue []*fn
	reach := func(f, from *fn, at token.Token) {
		if f == nil || f.reached {
			return
		}
		f.reached, f.from, f.fromCall = true, from, at
		if !f.decl.Step {
			queue = append(queue, f)
		}
	}
	for _, root := range roots {
		reach(root, nil, root.decl.Tok)
	}
	for {
		for len(queue) > 0 {
			f := queue[0]
			queue = queue[1:]
			c.scan(f)
			for _, call := range f.calls {
				reach(call.target, f, call.at)
			}
		}
		// Calls of function values, against what is taken by now
		taken := append([]*fn(nil), c.global...)
		for _, f := range c.order {
			if f.reached && !f.decl.Step {
				taken = append(taken, f.taken...)
			}
		}
		before := len(queue)
		for _, f := range c.order {
			if !f.reached || f.decl.Step {
				continue
			}
			for _, value := range f.values {
				for _, t := range taken {
					if value.arity == anyArity || arity(t) == value.arity {
						reach(t, f, value.at)
					}
				}
			}
		}
		if len(queue) == before {
			return
		}
	}
}

func arity(f *fn) int {
	return len(f.decl.Params)
}

// anyArity is the arity of a call a native makes of a function it is
// given: it may be any of them.
const anyArity = -1

// takesFunction says whether a native may be given a function, which it
// may call: result.attempt's protect, which takes Any, a sort given its
// order.
func takesFunction(decl *ast.FnDecl) bool {
	for _, param := range decl.Params {
		if param.Type != nil && (param.Type.Kind == ast.TypeFunction || param.Type.Name == "Any") {
			return true
		}
	}
	return false
}

// keptInGlobals finds the functions whose value is kept in a variable
// outside any function: written so, or put there by any code, a step
// among it, or code no flow reaches. A flow that reads the variable may
// call any of them.
func (c *checker) keptInGlobals(prog *ast.Program) {
	// The compiler has made each such variable a function that gives its
	// place, an array of one, made of the value it is declared with
	for _, f := range c.order {
		if f.decl.Body == nil {
			continue
		}
		if strings.Contains(f.decl.Name, resolve.VariablePrefix) {
			c.global = append(c.global, c.takenIn(f.decl.Body)...)
			continue
		}
		ast.Walk(f.decl.Body, func(expr ast.Expr) {
			if b, ok := expr.(*ast.BinaryExpr); ok && ast.IsAssignment(b.Op) && isGlobalPlace(b.Left) {
				c.global = append(c.global, c.takenIn(b.Right)...)
			}
		}, func(ast.Stmt) {})
	}
}

// isGlobalPlace says whether an expression is the place of a variable
// outside functions, as the compiler writes it: __var_name()[0].
func isGlobalPlace(e ast.Expr) bool {
	index, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	call, ok := index.Expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Func.(*ast.Ident)
	return ok && strings.Contains(ident.Name, resolve.VariablePrefix)
}

// takenIn are the functions whose value an expression takes.
func (c *checker) takenIn(e ast.Node) []*fn {
	var taken []*fn
	callees := map[ast.Expr]bool{}
	ast.Walk(e, func(expr ast.Expr) {
		switch e := expr.(type) {
		case *ast.CallExpr:
			callees[e.Func] = true
		case *ast.Ident:
			if target := c.function(e); target != nil && !callees[e] {
				taken = append(taken, target)
			}
		case *ast.MemberExpr:
			if !callees[e] {
				taken = append(taken, c.methodsOf(e)...)
			}
		}
	}, func(ast.Stmt) {})
	return taken
}

// calledByTheMachine are the methods the machine calls of itself on a
// value it formats, compares or sorts: those of Show, Equal and Ordered,
// and the equality the compiler gives a type of Equal. A value that
// holds one can be in any flow.
func (c *checker) calledByTheMachine(prog *ast.Program) []*fn {
	var out []*fn
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		switch impl.TraitName {
		case types.TraitShow, types.TraitEqual, types.TraitOrdered:
		default:
			continue
		}
		for _, method := range impl.Methods {
			if f := c.fns[method]; f != nil && !isLibrary(method) {
				out = append(out, f)
			}
		}
	}
	return out
}

// scan reads a body once: what it calls, and what function values it
// takes. A function written inside it is part of it.
func (c *checker) scan(f *fn) {
	if f.scanned || f.decl.Body == nil {
		f.scanned = true
		return
	}
	f.scanned = true
	callees := map[ast.Expr]bool{}
	ast.Walk(f.decl.Body, func(expr ast.Expr) {
		switch e := expr.(type) {
		case *ast.CallExpr:
			callees[e.Func] = true
			c.scanCall(f, e)
		case *ast.Ident:
			if callees[e] {
				return
			}
			if target := c.function(e); target != nil {
				f.taken = append(f.taken, target)
			}
		case *ast.MemberExpr:
			if callees[e] {
				return
			}
			// A method taken as a value: point.norm
			for _, target := range c.methodsOf(e) {
				f.taken = append(f.taken, target)
			}
		}
	}, func(ast.Stmt) {})
}

func (c *checker) scanCall(f *fn, e *ast.CallExpr) {
	at := callSite(e)
	switch callee := e.Func.(type) {
	case *ast.Ident:
		sym := c.table.Defs[callee]
		if sym == nil {
			f.values = append(f.values, call{at: at, arity: len(e.Args)})
			return
		}
		decl, ok := sym.Decl.(*ast.FnDecl)
		if !ok {
			if sym.Kind == resolve.SymbolFunc && sym.Decl == nil {
				return // A builtin: println, panic, ...
			}
			// A variable or a parameter that holds a function
			f.values = append(f.values, call{at: at, arity: len(e.Args)})
			return
		}
		if decl.Extern {
			if reachesWorld(decl) {
				f.world = append(f.world, call{at: at, native: decl.Name})
			}
			if suspends(decl) {
				f.suspends = true
			}
			if takesFunction(decl) {
				f.values = append(f.values, call{at: at, arity: anyArity})
			}
			return
		}
		f.calls = append(f.calls, call{at: at, target: c.fns[decl]})
	case *ast.MemberExpr:
		targets := c.methodsOf(callee)
		if len(targets) == 0 {
			// A function kept in a field
			f.values = append(f.values, call{at: at, arity: len(e.Args)})
			return
		}
		for _, target := range targets {
			if target.decl.Extern {
				if reachesWorld(target.decl) {
					f.world = append(f.world, call{at: at, native: target.decl.Name})
				}
				if takesFunction(target.decl) {
					f.values = append(f.values, call{at: at, arity: anyArity})
				}
				continue
			}
			f.calls = append(f.calls, call{at: at, target: target})
		}
	default:
		// What a call or an index gives, called
		f.values = append(f.values, call{at: at, arity: len(e.Args)})
	}
}

// function is the function of the program a name stands for, if any.
func (c *checker) function(ident *ast.Ident) *fn {
	sym := c.table.Defs[ident]
	if sym == nil {
		return nil
	}
	if decl, ok := sym.Decl.(*ast.FnDecl); ok {
		return c.fns[decl]
	}
	return nil
}

// methodsOf is what `receiver.name` can be: the method of the receiver's
// type, or, when only a trait is known of it, every method of that name.
// A module function written module.name is the function.
func (c *checker) methodsOf(member *ast.MemberExpr) []*fn {
	if ident, ok := member.Expr.(*ast.Ident); ok {
		if sym := c.table.Defs[ident]; sym == nil {
			// module.name, as the modules left it: one name
			return nil
		}
	}
	recv := c.info.Types[member.Expr]
	for _, owner := range owners(recv) {
		if found := c.methods[owner][member.Name]; len(found) > 0 {
			return found
		}
	}
	if c.isField(recv, member.Name) {
		// A function kept in a field: not the method of another type
		// that has the name
		return nil
	}
	return c.byName[member.Name]
}

// isField says whether a struct has a field of that name.
func (c *checker) isField(t types.Type, name string) bool {
	st, ok := t.(*types.StructType)
	if !ok {
		return false
	}
	for _, owner := range []string{st.Name, st.BaseName} {
		if _, ok := c.info.StructFields[owner][name]; ok {
			return true
		}
	}
	return false
}

// owners are the impls a value of a type takes its methods from, the
// closest first.
func owners(t types.Type) []string {
	var out []string
	if types.IsOptional(t) {
		out = append(out, ast.OptionalImpl)
	}
	if union, ok := t.(*types.UnionType); ok {
		for _, member := range union.Types {
			if !member.Equals(types.Null) {
				out = append(out, owners(member)...)
			}
		}
		return out
	}
	switch t := t.(type) {
	case *types.ArrayType:
		if item := types.ImplKey(t.ElementType); item != "" && item != ast.ArrayImpl {
			out = append(out, ast.ArrayImplOf(item))
		}
		out = append(out, ast.ArrayImpl)
	case *types.StructType:
		out = append(out, t.Name)
		if t.BaseName != "" {
			out = append(out, t.BaseName)
		}
	default:
		if key := types.ImplKey(t); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// report says, for every native of the world the walk reached, which call
// of the program's own code leads to it, once per such call.
func (c *checker) report() {
	seen := map[token.Position]bool{}
	var found []errors.Diagnostic
	for _, f := range c.order {
		if !f.reached || f.decl.Step {
			continue
		}
		for _, w := range f.world {
			// Up to the program's own code: a native is called by the
			// library, and the library by the program
			at, in := w.at, f
			for in != nil && isLibrary(in.decl) && in.from != nil {
				at, in = in.fromCall, in.from
			}
			if in == nil || seen[at.Pos] {
				continue
			}
			seen[at.Pos] = true
			found = append(found, c.diagnostic(in, at, w.native))
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i].Pos, found[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	for _, d := range found {
		c.errs.Add(d)
	}
}

func (c *checker) diagnostic(in *fn, at token.Token, native string) errors.Diagnostic {
	d := errors.NewDiagnosticFromToken(errors.SeverityError, ErrReachesWorld,
		fmt.Sprintf("a flow reaches %s outside a step", worldOf(native)), at, c.linesOf(at))
	d.Function = name(in)
	var chain []string
	for f := in; f.from != nil; f = f.from {
		chain = append(chain, fmt.Sprintf("'%s' is called by %s at %s", name(f), name(f.from), f.fromCall.Pos))
	}
	if len(chain) > maxNotes {
		root := in
		for root.from != nil {
			root = root.from
		}
		more := len(chain) - maxNotes
		chain = append(chain[:maxNotes], fmt.Sprintf("and %d more calls, up to %s", more, name(root)))
	}
	for i, link := range chain {
		if i == 0 {
			d.Notes = append(d.Notes, "= Note: "+link)
		} else {
			d.Notes = append(d.Notes, "        "+link)
		}
	}
	if in.from == nil {
		d.Suggestion = "Move the call into a step fn, and call the step here"
	} else {
		d.Suggestion = fmt.Sprintf("Make '%s' a step fn, or move the call into one", name(in))
	}
	return d
}

func (c *checker) linesOf(at token.Token) map[int]string {
	if text, ok := c.errs.SourceLine(at.Pos.File, at.Pos.Line); ok {
		return map[int]string{at.Pos.Line: text}
	}
	return c.lines
}

// name is a function as the program writes it: Refund.run, notify.
func name(f *fn) string {
	short := lastPart(f.decl.Name)
	if f.decl.CopyOf != "" {
		short = lastPart(f.decl.CopyOf)
	}
	if f.owner != "" {
		return lastPart(f.owner) + "." + short
	}
	return short
}

func lastPart(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && !strings.HasPrefix(name, "[") {
		return name[i+1:]
	}
	return name
}

func isLibrary(decl *ast.FnDecl) bool {
	return decl.Module == "std" || strings.HasPrefix(decl.Module, "std.")
}

// callSite is where a call is written, for the message: the start of
// what is called, http.post rather than the dot in it.
func callSite(e *ast.CallExpr) token.Token {
	var start ast.Expr = e.Func
	for {
		member, ok := start.(*ast.MemberExpr)
		if !ok {
			break
		}
		start = member.Expr
	}
	tok := start.Token()
	if tok.Pos.Line != e.Tok.Pos.Line || tok.Pos.Col > e.Tok.Pos.Col {
		return e.Tok
	}
	return tok
}
