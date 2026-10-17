package compiler

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/modules"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Analysis is a program understood but not compiled: parsed, its names
// resolved and its types checked. It is what a tool that reads programs
// works from, an editor first of all. A stage that did not run because an
// earlier one reported errors leaves its field nil.
type Analysis struct {
	// Program is the entry file and the modules it imports, as one
	// program, as the stages after parsing see it.
	Program *ast.Program

	// Table says what each name used stands for.
	Table *resolve.Table

	// Info says what type each expression has.
	Info *types.Info
}

// Analyze runs the front of the pipeline on the source text of a
// program's entry file: parsing and loading modules, name resolution,
// type checking. Diagnostics go to errs.
func Analyze(src, file string, opts Options, errs *errors.Handler) *Analysis {
	if opts.Read == nil {
		opts.Read = readSourceFile
	}
	opts = withStandardLibrary(opts)
	analysis := &Analysis{}
	lines := parser.SourceLines(src)

	// An analysis is for an editor: it has the methods of the builtin
	// types at hand before the program calls any
	prog := modules.Load(src, file, modules.Options{Read: opts.Read, Root: opts.Root, Packages: opts.Packages, EveryMethod: true}, errs)
	analysis.Program = prog
	if errs.HasErrors() || prog == nil {
		return analysis
	}
	declareTuples(prog)
	giveDefaultMethods(prog)
	checkSupported(prog, errs, lines)
	checkExterns(prog, opts.Hosted, errs, lines)
	if errs.HasErrors() {
		return analysis
	}

	resolver := resolve.NewResolver(errs)
	resolver.SetSourceLines(lines)
	analysis.Table = resolver.Resolve(prog)
	if errs.HasErrors() {
		return analysis
	}

	checker := types.NewChecker(prog, analysis.Table, errs)
	checker.SetSourceLines(lines)
	analysis.Info = checker.Check()
	return analysis
}
