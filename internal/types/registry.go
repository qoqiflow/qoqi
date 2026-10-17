package types

import "github.com/qoqiflow/qoqi/internal/ast"

// RegisterType registers a type definition in the type registry.
// This should be called during type checking when processing struct/enum declarations.
func (info *Info) RegisterType(name string, typ Type) {
	info.Defs[name] = typ
}

// LookupType retrieves a type definition from the registry.
func (info *Info) LookupType(name string) (Type, bool) {
	typ, ok := info.Defs[name]
	return typ, ok
}

// RegisterStructDecl is a convenience method to register a struct declaration.
func (info *Info) RegisterStructDecl(decl *ast.StructDecl, structType *StructType) {
	info.Defs[decl.Name] = structType
}

// RegisterEnumDecl is a convenience method to register an enum declaration.
// Note: EnumType doesn't exist yet in the types package, so we use Type interface
func (info *Info) RegisterEnumDecl(decl *ast.EnumDecl, enumType Type) {
	info.Defs[decl.Name] = enumType
}
