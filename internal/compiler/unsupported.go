package compiler

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// checkSupported reports language features that parse but cannot be
// compiled yet, so they fail with a clear message instead of a confusing
// error from a later stage.
func checkSupported(prog *ast.Program, errs *errors.Handler, lines map[int]string) {
	unsupported := func(node ast.Node, what, suggestion string) {
		errs.ReportErrorWithSuggestion(ErrUnsupportedFeature, what+" not supported yet", suggestion, node.Token(), lines)
	}

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			checkBlockSupported(d.Body, unsupported)
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				checkBlockSupported(m.Body, unsupported)
			}
		}
	}
}

func checkBlockSupported(block *ast.BlockStmt, unsupported func(ast.Node, string, string)) {
	if block == nil {
		return
	}
	ast.Walk(block, func(ast.Expr) {}, func(stmt ast.Stmt) {
		match, ok := stmt.(*ast.MatchStmt)
		if !ok {
			return
		}
		for _, c := range match.Cases {
			for _, pattern := range c.Patterns() {
				switch pattern.(type) {
				case *ast.Literal, *ast.UnaryExpr, *ast.Type, *ast.VariantPattern, *ast.StructPattern, *ast.RangeExpr:
					// Value patterns, ranges, type patterns, enum variants and structs
				default:
					unsupported(c, "this kind of match pattern is", "Match on literal values, on ranges of numbers, on types or on the variants of an enum")
				}
			}
		}
	})
}
