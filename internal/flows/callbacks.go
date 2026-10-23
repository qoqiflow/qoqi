package flows

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// ErrWaitInCallback is a flow that would wait, for a step or anything
// else, inside a function the machine calls from Go: the one given to
// result.attempt or result.guard, or the show, equals or compare of a
// type, which it calls as it formats, compares or sorts. The program is
// written whole where it waits; a call made from Go cannot be.
const ErrWaitInCallback = "F0013"

// calledFromGo are the natives that call a function they are given, by
// the place of the parameter that takes it.
var calledFromGo = map[string]int{
	"std.result.protect": 0,
}

// checkCallbacks says where a flow would wait in a function called from
// Go. Of a function value: where it is given to such a native, or to a
// function that hands it on to one. Of a method the machine calls: where
// it is declared.
func (c *checker) checkCallbacks(prog *ast.Program) {
	for _, f := range c.order {
		c.scan(f)
	}
	waits := c.waiting()
	bound := c.handedToGo()

	for _, f := range c.order {
		if !f.reached || f.decl.Step || f.decl.Body == nil {
			continue
		}
		ast.Walk(f.decl.Body, func(expr ast.Expr) {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return
			}
			for _, target := range c.declsOf(call) {
				for i, arg := range call.Args {
					if isBound(bound, target, i) && c.waitsIn(arg, waits) {
						called := lastPart(target.Name)
						if target.CopyOf != "" {
							called = lastPart(target.CopyOf)
						}
						c.stageError(arg.Token(), ErrWaitInCallback,
							fmt.Sprintf("a flow waits inside the function given to %s, which calls it from Go: the instance cannot be written there, and would stop", called),
							"Call the step, or wait, outside it: take what it gives, and give that to "+called)
					}
				}
			}
		}, func(ast.Stmt) {})
	}

	for _, f := range c.calledByTheMachine(prog) {
		if waits[f] && !strings.HasPrefix(lastPart(f.decl.Name), "__") {
			c.stageError(f.decl.Tok, ErrWaitInCallback,
				fmt.Sprintf("%s waits, and the machine calls it from Go as it formats, compares or sorts a value: a flow that does so cannot be written there, and would stop", name(f)),
				"Compute it from what the value holds, and call steps from the flow")
		}
	}
}

// waiting are the functions that may wait: they call a step, or wait for
// the clock, a message, another task, or call one that does.
func (c *checker) waiting() map[*fn]bool {
	waits := map[*fn]bool{}
	for _, f := range c.order {
		if f.suspends {
			waits[f] = true
		}
		for _, call := range f.calls {
			if call.target != nil && call.target.decl.Step {
				waits[f] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, f := range c.order {
			if waits[f] || f.decl.Step {
				continue
			}
			for _, call := range f.calls {
				if waits[call.target] {
					waits[f], changed = true, true
					break
				}
			}
		}
	}
	return waits
}

// handedToGo are, by function, the places of the parameters whose
// function a native calls from Go: given to it, or to a function that
// hands it on to one.
func (c *checker) handedToGo() map[*ast.FnDecl]map[int]bool {
	bound := map[*ast.FnDecl]map[int]bool{}
	for changed := true; changed; {
		changed = false
		for _, f := range c.order {
			if f.decl.Body == nil {
				continue
			}
			ast.Walk(f.decl.Body, func(expr ast.Expr) {
				call, ok := expr.(*ast.CallExpr)
				if !ok {
					return
				}
				for _, target := range c.declsOf(call) {
					for i, arg := range call.Args {
						if !isBound(bound, target, i) {
							continue
						}
						at := c.paramOf(f, arg)
						if at < 0 || bound[f.decl][at] {
							continue
						}
						if bound[f.decl] == nil {
							bound[f.decl] = map[int]bool{}
						}
						bound[f.decl][at], changed = true, true
					}
				}
			}, func(ast.Stmt) {})
		}
	}
	return bound
}

// isBound says whether a function calls from Go the function given at
// a place: a native that does, or a function that hands it on to one.
func isBound(bound map[*ast.FnDecl]map[int]bool, decl *ast.FnDecl, at int) bool {
	if decl.Extern {
		place, ok := calledFromGo[decl.Name]
		return ok && place == at
	}
	return bound[decl][at]
}

// declsOf are the functions a call may go to, natives among them.
func (c *checker) declsOf(call *ast.CallExpr) []*ast.FnDecl {
	if ident, ok := call.Func.(*ast.Ident); ok {
		if sym := c.table.Defs[ident]; sym != nil {
			if decl, ok := sym.Decl.(*ast.FnDecl); ok {
				return []*ast.FnDecl{decl}
			}
		}
		return nil
	}
	var out []*ast.FnDecl
	for _, f := range c.targetsOf(call) {
		out = append(out, f.decl)
	}
	return out
}

// paramOf is the place of the parameter of f an argument names, or -1.
func (c *checker) paramOf(f *fn, arg ast.Expr) int {
	ident, ok := arg.(*ast.Ident)
	if !ok {
		return -1
	}
	sym := c.table.Defs[ident]
	if sym == nil {
		return -1
	}
	for i, param := range f.decl.Params {
		if sym.Decl == param {
			return i
		}
	}
	return -1
}

// targetsOf are the functions a call may go to, by its name or as a
// method.
func (c *checker) targetsOf(call *ast.CallExpr) []*fn {
	switch callee := call.Func.(type) {
	case *ast.Ident:
		if f := c.function(callee); f != nil {
			return []*fn{f}
		}
	case *ast.MemberExpr:
		return c.methodsOf(callee)
	}
	return nil
}

// waitsIn says whether a function value may wait: a function written
// there whose body waits, or a function named there that does.
func (c *checker) waitsIn(e ast.Expr, waits map[*fn]bool) bool {
	switch e := e.(type) {
	case *ast.FnExpr:
		found := false
		ast.Walk(e.Body, func(expr ast.Expr) {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return
			}
			for _, target := range c.declsOf(call) {
				if waits[c.fns[target]] || target.Step || (target.Extern && suspends(target)) {
					found = true
				}
			}
		}, func(ast.Stmt) {})
		return found
	case *ast.Ident:
		f := c.function(e)
		return f != nil && (waits[f] || f.decl.Step)
	case *ast.MemberExpr:
		for _, f := range c.methodsOf(e) {
			if waits[f] || f.decl.Step {
				return true
			}
		}
	}
	return false
}

// suspends says whether a native is where a task waits.
func suspends(decl *ast.FnDecl) bool {
	return decl.Name == "std.task.suspend" || decl.Name == "std.task.suspendFor"
}
