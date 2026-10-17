package desugar

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Error codes for desugaring phase
const (
	ErrUnsupportedIterable   = "E4001"
	ErrInvalidMatchPattern   = "E4002"
	ErrMissingTypeInfo       = "E4003"
	ErrNestedShortCircuit    = "E4004"
	ErrUnsupportedMatchType  = "E4005"
	ErrEnumVariantExtraction = "E4006"
)

// reportUnsupportedIterable reports an error when a for-in loop tries to iterate over a non-iterable type
func (d *Desugarer) reportUnsupportedIterable(forStmt *ast.ForStmt, typ types.Type) {
	pos := forStmt.Position()
	iterableTok := forStmt.Iterable.Token()

	message := fmt.Sprintf("Cannot iterate over type '%s'", typ.String())

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityError,
		Code:     ErrUnsupportedIterable,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: iterableTok.Pos.Col, EndCol: iterableTok.EndPos.Col},
		},
		Suggestion: "Only arrays and strings are iterable. Consider using a while loop or converting to an array.",
	})
}

// reportInvalidMatchPattern reports an error when a match pattern cannot be desugared
func (d *Desugarer) reportInvalidMatchPattern(pattern ast.Node, reason string) {
	pos := pattern.Position()
	tok := pattern.Token()

	message := fmt.Sprintf("Invalid match pattern: %s", reason)

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityError,
		Code:     ErrInvalidMatchPattern,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
		},
		Suggestion: "Match patterns must be literals, identifiers, or type names. Complex patterns are not yet supported.",
	})
}

// reportMissingTypeInfo reports an error when type information is missing for an expression
func (d *Desugarer) reportMissingTypeInfo(expr ast.Expr, context string) {
	pos := expr.Position()
	tok := expr.Token()

	message := fmt.Sprintf("Missing type information for %s", context)

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityError,
		Code:     ErrMissingTypeInfo,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
		},
		Suggestion: "This is likely an internal compiler error. Type checking should have completed before desugaring.",
	})
}

// reportUnsupportedMatchType reports an error for unsupported match type patterns
func (d *Desugarer) reportUnsupportedMatchType(typ *ast.Type, reason string) {
	pos := typ.Position()
	tok := typ.Token()

	message := fmt.Sprintf("Unsupported match type pattern: %s", reason)

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityError,
		Code:     ErrUnsupportedMatchType,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
		},
		Suggestion: "Type matching is currently supported for simple type names only. Union types and complex patterns are not yet supported.",
	})
}

// reportEnumVariantError reports an error when enum variant extraction fails
func (d *Desugarer) reportEnumVariantError(pattern ast.Node, reason string) {
	pos := pattern.Position()
	tok := pattern.Token()

	message := fmt.Sprintf("Invalid enum variant pattern: %s", reason)

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityError,
		Code:     ErrEnumVariantExtraction,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
		},
		Suggestion: "Enum variant patterns must be in the form: EnumName::Variant or EnumName::Variant(bindings).",
	})
}

// warnExcessiveNesting warns about deeply nested short-circuit operators
func (d *Desugarer) warnExcessiveNesting(expr ast.Expr, depth int) {
	if depth < 5 {
		return // Only warn for deeply nested expressions
	}

	pos := expr.Position()
	tok := expr.Token()

	message := fmt.Sprintf("Deeply nested short-circuit operators (depth: %d)", depth)

	d.errs.Add(errors.Diagnostic{
		Severity: errors.SeverityWarning,
		Code:     ErrNestedShortCircuit,
		Message:  message,
		Pos:      pos,
		Context:  d.getSourceLine(pos),
		Highlights: []errors.Highlight{
			{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
		},
		Suggestion: "Consider breaking complex conditions into separate variables for better readability and maintainability.",
	})
}

// getSourceLine retrieves the source line for a given position
func (d *Desugarer) getSourceLine(pos token.Position) string {
	if line, ok := d.errs.SourceLine(pos.File, pos.Line); ok {
		return line
	}
	if d.sourceLines == nil {
		return ""
	}
	if line, ok := d.sourceLines[pos.Line]; ok {
		return line
	}
	return ""
}

// validateForLoopIterable validates that a for loop's iterable is actually iterable
func (d *Desugarer) validateForLoopIterable(forStmt *ast.ForStmt) bool {
	iterableType, ok := d.typeInfo.Types[forStmt.Iterable]
	if !ok {
		d.reportMissingTypeInfo(forStmt.Iterable, "for loop iterable")
		return false
	}

	switch t := iterableType.(type) {
	case *types.ArrayType:
		// Arrays are iterable
		return true
	case *types.PrimitiveType:
		if t.Name == types.PrimitiveString || t.Name == types.PrimitiveUnknown {
			// Strings are iterable; an unknown type was already reported
			// or will be checked when the loop runs
			return true
		}
		// Other primitives are not iterable
		d.reportUnsupportedIterable(forStmt, t)
		return false
	default:
		// Other types are not iterable (yet)
		d.reportUnsupportedIterable(forStmt, t)
		return false
	}
}

// validateMatchPattern validates a match pattern
func (d *Desugarer) validateMatchPattern(pattern ast.Node) bool {
	switch p := pattern.(type) {
	case nil:
		// `default:` has no pattern and always matches
		return true
	case *ast.Literal, *ast.UnaryExpr, *ast.RangeExpr:
		// Literals (including negative numbers) and ranges are always valid
		return true
	case *ast.Ident, *ast.VariantPattern, *ast.StructPattern:
		// Identifiers are valid (variable binding or enum variant), and so
		// are the patterns of variants and of structs
		return true
	case *ast.Type:
		// Type patterns for type matching
		if p.Kind == ast.TypeUnion {
			d.reportUnsupportedMatchType(p, "union type patterns are not yet supported")
			return false
		}
		return true
	case *ast.CallExpr:
		// Enum variant with destructuring: Variant(x, y)
		// This is valid but requires special handling
		return true
	default:
		d.reportInvalidMatchPattern(pattern, fmt.Sprintf("unsupported pattern type: %T", pattern))
		return false
	}
}

// countShortCircuitDepth counts the nesting depth of short-circuit operators
func (d *Desugarer) countShortCircuitDepth(expr ast.Expr) int {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		if e.Op == "&&" || e.Op == "||" || e.Op == "??" {
			leftDepth := d.countShortCircuitDepth(e.Left)
			rightDepth := d.countShortCircuitDepth(e.Right)
			maxDepth := leftDepth
			if rightDepth > maxDepth {
				maxDepth = rightDepth
			}
			return maxDepth + 1
		}
		return 0
	default:
		return 0
	}
}

// checkShortCircuitComplexity checks if short-circuit expressions are too complex
func (d *Desugarer) checkShortCircuitComplexity(expr ast.Expr) {
	depth := d.countShortCircuitDepth(expr)
	if depth >= 5 {
		d.warnExcessiveNesting(expr, depth)
	}
}
