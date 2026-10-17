package desugar

import (
	"github.com/qoqiflow/qoqi/internal/ast"
)

// desugarBinaryExpr desugars binary expressions, particularly short-circuit and null coalescing operators
func (d *Desugarer) desugarBinaryExpr(binary *ast.BinaryExpr) ast.Expr {
	// An operator a trait gives a struct
	if lowered := d.lowerStructOperation(binary); lowered != nil {
		return lowered
	}

	// Check for operators that need special desugaring
	switch binary.Op {
	case "&&":
		return d.desugarLogicalAnd(binary)
	case "||":
		return d.desugarLogicalOr(binary)
	case "??":
		return d.desugarNullCoalesce(binary)
	default:
		// For other operators, just desugar the operands
		left := d.desugarExpr(binary.Left)
		right := d.desugarExpr(binary.Right)

		return &ast.BinaryExpr{
			BaseNode: binary.BaseNode,
			Left:     left,
			Op:       binary.Op,
			Right:    right,
		}
	}
}

// desugarLogicalAnd desugars the operands of the && operator.
//
// The operator itself is kept: IR generation lowers &&, || and ?? to
// branches wherever they appear. They are deliberately not rewritten into
// statements with temporaries here. The rewritten form hides what the
// condition says from the type checker when the program is checked again
// after desugaring (`if x != null && x.ready` no longer shows that x is
// non-null in the body), and the types computed then drive code generation.
func (d *Desugarer) desugarLogicalAnd(binary *ast.BinaryExpr) ast.Expr {
	// Recursively desugar operands
	left := d.desugarExpr(binary.Left)
	right := d.desugarExpr(binary.Right)

	return &ast.BinaryExpr{
		BaseNode: binary.BaseNode,
		Left:     left,
		Op:       "&&",
		Right:    right,
	}
}

// desugarLogicalOr desugars the operands of the || operator, which is kept
// for IR generation (see desugarLogicalAnd).
func (d *Desugarer) desugarLogicalOr(binary *ast.BinaryExpr) ast.Expr {
	// Recursively desugar operands
	left := d.desugarExpr(binary.Left)
	right := d.desugarExpr(binary.Right)

	return &ast.BinaryExpr{
		BaseNode: binary.BaseNode,
		Left:     left,
		Op:       "||",
		Right:    right,
	}
}

// desugarNullCoalesce desugars the operands of the ?? operator, which is
// kept for IR generation (see desugarLogicalAnd).
func (d *Desugarer) desugarNullCoalesce(binary *ast.BinaryExpr) ast.Expr {
	// Recursively desugar operands
	left := d.desugarExpr(binary.Left)
	right := d.desugarExpr(binary.Right)

	return &ast.BinaryExpr{
		BaseNode: binary.BaseNode,
		Left:     left,
		Op:       "??",
		Right:    right,
	}
}
