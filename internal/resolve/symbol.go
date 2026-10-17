package resolve

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// SymbolKind distinguishes different kinds of symbols.
type SymbolKind int

const (
	SymbolVar SymbolKind = iota
	SymbolFunc
	SymbolStruct
	SymbolEnum
	SymbolTrait
	SymbolTypeParam
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolVar:
		return "variable"
	case SymbolFunc:
		return "function"
	case SymbolStruct:
		return "struct"
	case SymbolEnum:
		return "enum"
	case SymbolTrait:
		return "trait"
	case SymbolTypeParam:
		return "type parameter"
	default:
		return "unknown"
	}
}

// Symbol represents a named entity in the program.
type Symbol struct {
	Name string
	Kind SymbolKind
	Decl ast.Node // Pointer to the AST node where it is declared
	Used bool     // A name refers to it
}

// Scope represents a lexical region where names are valid.
type Scope struct {
	Parent  *Scope
	Symbols map[string]*Symbol
}

func NewScope(parent *Scope) *Scope {
	return &Scope{
		Parent:  parent,
		Symbols: make(map[string]*Symbol),
	}
}

// Insert adds a symbol to the scope. Returns error if already defined.
func (s *Scope) Insert(name string, sym *Symbol) error {
	if existing, ok := s.Symbols[name]; ok {
		return fmt.Errorf("symbol '%s' is already declared (previous declaration at %s)", name, existing.Decl.Position())
	}
	s.Symbols[name] = sym
	return nil
}

// Lookup finds a symbol in the current scope or any parent scope.
func (s *Scope) Lookup(name string) *Symbol {
	if sym, ok := s.Symbols[name]; ok {
		return sym
	}
	if s.Parent != nil {
		return s.Parent.Lookup(name)
	}
	return nil
}

// Table stores the result of name resolution.
type Table struct {
	// Maps a usage identifier to its definition symbol
	Defs map[*ast.Ident]*Symbol

	// Maps a declaration node to the scope it introduces
	Scopes map[ast.Node]*Scope

	// Stages maps a stage of a flow to the variables in scope there
	Stages map[*ast.ExprStmt][]*Symbol
}

func NewTable() *Table {
	return &Table{
		Defs:   make(map[*ast.Ident]*Symbol),
		Scopes: make(map[ast.Node]*Scope),
		Stages: make(map[*ast.ExprStmt][]*Symbol),
	}
}
