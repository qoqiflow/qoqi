package compiler

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/types"
)

// A parameter may have a value of its own, which a call that leaves it out
// gives it:
//
//	fn open(path: String, mode: String = "r") -> File { ... }
//	open("notes.txt")          is open("notes.txt", "r")
//
// Once the program is checked, every such call is given a copy of the
// value of each parameter it leaves out: from here on every call has all
// its arguments, and nothing below knows a parameter had a value of its
// own. The value names what the module of the function names, already
// written in full by the loading of modules, so it means the same where
// the call is.

// giveDefaults adds to the calls that leave parameters out the values of
// those parameters.
func giveDefaults(info *types.Info) {
	for call, defaults := range info.Defaults {
		for _, value := range defaults {
			call.Args = append(call.Args, (&ast.Cloner{}).Expr(value))
		}
	}
}
