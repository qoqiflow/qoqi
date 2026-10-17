package analysis

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// CaptureInfo stores the results of closure analysis
type CaptureInfo struct {
	// Maps each function (FnDecl or FnExpr) to its captured variables
	Captures map[ast.Node]*FunctionCaptures

	// Maps variables to their escape status
	Escapes map[*resolve.Symbol]bool
}

// FunctionCaptures describes what a single function captures
type FunctionCaptures struct {
	Function ast.Node // The function node (FnDecl or FnExpr)

	// Variables captured from parent scopes (ordered list)
	CapturedVars []*CapturedVar

	// Nesting depth (0 = top-level, 1 = nested once, etc.)
	Depth int

	// Parent function (nil for top-level)
	Parent ast.Node
}

// CapturedVar represents a single captured variable
type CapturedVar struct {
	Symbol *resolve.Symbol // The variable being captured
	Name   string          // Variable name

	// How many scopes up is this variable defined?
	// 0 = parent scope, 1 = grandparent, etc.
	ScopeDistance int

	// Is this captured from a parent's upvalue, or from parent's local?
	FromUpvalue bool

	// The declaration node
	Decl ast.Node
}

// NewCaptureInfo creates a new CaptureInfo instance
func NewCaptureInfo() *CaptureInfo {
	return &CaptureInfo{
		Captures: make(map[ast.Node]*FunctionCaptures),
		Escapes:  make(map[*resolve.Symbol]bool),
	}
}

// GetCaptures returns the capture info for a function
func (ci *CaptureInfo) GetCaptures(fn ast.Node) *FunctionCaptures {
	return ci.Captures[fn]
}

// IsEscaped returns true if a variable escapes its scope
func (ci *CaptureInfo) IsEscaped(sym *resolve.Symbol) bool {
	return ci.Escapes[sym]
}

// AddCapture adds a captured variable to a function's capture list
func (ci *CaptureInfo) AddCapture(fn ast.Node, cv *CapturedVar) {
	if captures, ok := ci.Captures[fn]; ok {
		captures.CapturedVars = append(captures.CapturedVars, cv)
	}
}
