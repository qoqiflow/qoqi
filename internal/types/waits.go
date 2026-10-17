package types

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// What `wait` waits for is a waitable: a value of a type that implements
// Waitable<T> of std.task. `wait value` is the call std.task.waitFor(value),
// written by the parser (see parser/wait.go).
const (
	TraitWaitable  = "std.task.Waitable"
	TaskStruct     = "std.task.Task"
	InstanceStruct = "std.flow.Instance"
	WaitFunction   = "std.task.waitFor"
	ErrNotWaited   = "E2043" // a waitable made and thrown away
	ErrNotWaitable = "E2044" // wait on what cannot be waited for
)

// checkWaited reports a waitable that a statement makes and throws away:
// `channel.send(value);` sends nothing, it says what a wait would send.
// One kept in a variable, or given to a function, is waited for later. A
// task is the exception: it runs whether or not anyone waits for it, and
// `task.run(work);` alone is how one is started and left to itself.
func (c *Checker) checkWaited(expr ast.Expr, t Type) {
	if t == nil || isUnknown(t) || madeByWait(expr) {
		return
	}
	if st, ok := t.(*StructType); ok && (st.BaseName == TaskStruct || st.Name == TaskStruct || st.BaseName == InstanceStruct || st.Name == InstanceStruct) {
		// Started for what it does: it goes on without anyone waiting
		return
	}
	if _, ok := c.traitArgsOf(t, TraitWaitable); !ok {
		return
	}
	c.errorfWithSuggestion(expr.Token(), ErrNotWaited,
		"Put wait before it, as in: wait channel.send(value);",
		"%s does nothing until it is waited for", describe(t))
}

// madeByWait reports what a wait match writes, which takes what came and
// may throw it away: w.take().
func madeByWait(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	ident, ok := member.Expr.(*ast.Ident)
	return ok && strings.HasPrefix(ident.Name, "__")
}

// waitsOnWaitable checks, for the call `wait value` stands for, that the
// value can be waited for, and says so in the words of `wait` when it
// cannot. It reports whether the call can go on being checked.
func (c *Checker) waitsOnWaitable(e *ast.CallExpr) bool {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || ident.Name != WaitFunction || len(e.Args) != 1 {
		return true
	}
	t := c.checkExpr(e.Args[0])
	if t == nil || isUnknown(t) {
		return false
	}
	if _, ok := c.traitArgsOf(t, TraitWaitable); ok {
		return true
	}
	c.errorfWithSuggestion(e.Args[0].Token(), ErrNotWaitable,
		"Wait for a task, a channel's receive() or send(value), time.after(2.seconds()), task.all(tasks), or a type that implements Waitable<T>",
		"%s cannot be waited for", describe(t))
	return false
}
