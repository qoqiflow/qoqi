package monomorph

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Error codes for monomorphization
const (
	ErrUnboundedGenerics     = "E3001" // Generic function/type used but type arguments cannot be inferred
	ErrInfiniteInstantiation = "E3002" // Recursive generic types causing infinite instantiation
	ErrTypeArgMismatch       = "E3003" // Number of type arguments doesn't match number of type parameters
)

// InstantiationKey uniquely identifies a monomorphized instance.
// We use a string representation for the key to make it comparable.
type InstantiationKey string

// Instantiation tracks a single monomorphized instance.
type Instantiation struct {
	Key      InstantiationKey
	BaseName string
	TypeArgs []types.Type
	Decl     ast.Node // Original generic declaration (*ast.FnDecl or *ast.StructDecl)
	UseSites []ast.Node
}

// MakeInstantiationKey creates an instantiation key from a base name and type arguments.
func MakeInstantiationKey(baseName string, typeArgs []types.Type) InstantiationKey {
	var parts []string
	parts = append(parts, baseName)
	for _, t := range typeArgs {
		parts = append(parts, t.String())
	}
	return InstantiationKey(strings.Join(parts, "::"))
}

// MonomorphizationInfo contains information about the monomorphization process.
type MonomorphizationInfo struct {
	// Maps instantiation keys to their generated declarations
	Generated map[InstantiationKey]ast.Node
}

func NewMonomorphizationInfo() *MonomorphizationInfo {
	return &MonomorphizationInfo{
		Generated: make(map[InstantiationKey]ast.Node),
	}
}

// Monomorphizer performs the monomorphization transformation.
type Monomorphizer struct {
	prog         *ast.Program
	typeInfo     *types.Info
	resolveTable *resolve.Table
	errs         *errors.Handler
	sourceLines  map[int]string

	// Tracks all discovered instantiations, and the order they were found
	// in, so that the generated program does not depend on map order
	instantiations map[InstantiationKey]*Instantiation
	order          []InstantiationKey

	// Tracks generated monomorphic functions/types
	generated map[InstantiationKey]ast.Node

	// Original generic declarations (by name)
	generics map[string]ast.Node

	// Tracks which instantiations we're currently generating (for cycle detection)
	generating map[InstantiationKey]bool
}

// NewMonomorphizer creates a new monomorphizer.
func NewMonomorphizer(
	prog *ast.Program,
	typeInfo *types.Info,
	resolveTable *resolve.Table,
	errs *errors.Handler,
) *Monomorphizer {
	return &Monomorphizer{
		prog:           prog,
		typeInfo:       typeInfo,
		resolveTable:   resolveTable,
		errs:           errs,
		sourceLines:    make(map[int]string),
		instantiations: make(map[InstantiationKey]*Instantiation),
		generated:      make(map[InstantiationKey]ast.Node),
		generics:       make(map[string]ast.Node),
		generating:     make(map[InstantiationKey]bool),
	}
}

// SetSourceLines sets the source lines for error reporting.
func (m *Monomorphizer) SetSourceLines(lines map[int]string) {
	m.sourceLines = lines
}

// Monomorphize performs the monomorphization transformation.
// Returns the transformed program and monomorphization info.
func (m *Monomorphizer) Monomorphize() (*ast.Program, *MonomorphizationInfo) {
	// Collect all generic declarations
	m.collectGenerics()

	// Phase 1: Discover all instantiations
	m.discoverInstantiations()

	// Phase 2: Resolve dependencies and determine instantiation order
	orderedKeys := m.resolveDependencies()

	// Phase 3: Generate monomorphized instances
	for _, key := range orderedKeys {
		m.generateInstance(key)
	}

	// Phase 4: Rewrite call sites
	m.rewriteCallSites()

	// Build the result info
	info := NewMonomorphizationInfo()
	info.Generated = m.generated

	return m.prog, info
}

// collectGenerics collects all generic function and struct declarations.
func (m *Monomorphizer) collectGenerics() {
	for _, decl := range m.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if len(d.TypeParams) > 0 {
				m.generics[d.Name] = d
			}
		case *ast.StructDecl:
			if len(d.TypeParams) > 0 {
				m.generics[d.Name] = d
			}
		}
	}
}
