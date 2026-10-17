package analysis

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Error codes for closure analysis
const (
	ErrClosureCycle = "E4101" // Circular closure dependency
)

// Analyzer performs closure analysis on the AST
type Analyzer struct {
	prog         *ast.Program
	resolveTable *resolve.Table
	typeInfo     *types.Info
	errs         *errors.Handler
	sourceLines  map[int]string

	info *CaptureInfo

	// Current function being analyzed (stack for nesting)
	funcStack []ast.Node

	// Current scope stack (for tracking depth)
	scopeStack []*functionScope
}

// functionScope tracks local variables in a function
type functionScope struct {
	function ast.Node
	locals   map[string]*resolve.Symbol
	depth    int
}

// NewAnalyzer creates a new closure analyzer
func NewAnalyzer(
	prog *ast.Program,
	resolveTable *resolve.Table,
	typeInfo *types.Info,
	errs *errors.Handler,
) *Analyzer {
	return &Analyzer{
		prog:         prog,
		resolveTable: resolveTable,
		typeInfo:     typeInfo,
		errs:         errs,
		sourceLines:  make(map[int]string),
		info:         NewCaptureInfo(),
		funcStack:    make([]ast.Node, 0),
		scopeStack:   make([]*functionScope, 0),
	}
}

// SetSourceLines sets source lines for error reporting
func (a *Analyzer) SetSourceLines(lines map[int]string) {
	a.sourceLines = lines
}

// Analyze performs closure analysis on the program
func (a *Analyzer) Analyze() *CaptureInfo {
	// Phase 1: Build capture graph
	a.buildCaptureGraph(a.prog)

	// Phase 2: Perform escape analysis
	a.performEscapeAnalysis()

	return a.info
}

// getParentFunction returns the parent function in the stack
func (a *Analyzer) getParentFunction() ast.Node {
	if len(a.funcStack) >= 2 {
		return a.funcStack[len(a.funcStack)-2]
	}
	return nil
}

// getCurrentScope returns the current function scope
func (a *Analyzer) getCurrentScope() *functionScope {
	if len(a.scopeStack) > 0 {
		return a.scopeStack[len(a.scopeStack)-1]
	}
	return nil
}
