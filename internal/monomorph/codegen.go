package monomorph

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/types"
)

// generateInstance generates a monomorphized instance for the given key.
func (m *Monomorphizer) generateInstance(key InstantiationKey) {
	// Check if already generated
	if _, exists := m.generated[key]; exists {
		return
	}

	// Check for infinite instantiation
	if m.generating[key] {
		return // Already being generated (cycle detected)
	}

	m.generating[key] = true
	defer func() { m.generating[key] = false }()

	inst := m.instantiations[key]

	switch decl := inst.Decl.(type) {
	case *ast.FnDecl:
		newDecl := m.generateFnInstance(decl, key)
		m.generated[key] = newDecl
		m.prog.Decls = append(m.prog.Decls, newDecl)
	case *ast.StructDecl:
		newDecl := m.generateStructInstance(decl, key)
		m.generated[key] = newDecl
		m.prog.Decls = append(m.prog.Decls, newDecl)
	}
}

// generateFnInstance generates a monomorphized function instance.
func (m *Monomorphizer) generateFnInstance(original *ast.FnDecl, key InstantiationKey) *ast.FnDecl {
	inst := m.instantiations[key]
	// Create type substitution map
	subst := m.createSubstitution(original.TypeParams, inst.TypeArgs)

	// Clone and substitute the function
	newFn := &ast.FnDecl{
		BaseNode:      original.BaseNode,
		Name:          m.mangleName(key),
		TypeParams:    m.typeParamsIn(inst.TypeArgs),
		Params:        m.cloneAndSubstituteParams(original.Params, subst),
		ReturnType:    m.substituteASTType(original.ReturnType, subst),
		Body:          m.cloneAndSubstituteBlock(original.Body, subst),
		IsTraitMethod: original.IsTraitMethod,
		Pub:           original.Pub,
		Module:        original.Module,
		CopyOf:        original.Name,
	}

	return newFn
}

// generateStructInstance generates a monomorphized struct instance.
func (m *Monomorphizer) generateStructInstance(original *ast.StructDecl, key InstantiationKey) *ast.StructDecl {
	inst := m.instantiations[key]
	// Create type substitution map
	subst := m.createSubstitution(original.TypeParams, inst.TypeArgs)

	// Clone and substitute the struct
	newStruct := &ast.StructDecl{
		BaseNode:   original.BaseNode,
		Name:       m.mangleName(key),
		TypeParams: nil, // No type parameters in monomorphized version
		Fields:     m.cloneAndSubstituteFields(original.Fields, subst),
	}

	return newStruct
}

// TypeSubstitution maps type parameter names to concrete types.
type TypeSubstitution map[string]types.Type

// createSubstitution creates a type substitution map.
func (m *Monomorphizer) createSubstitution(typeParams []string, typeArgs []types.Type) TypeSubstitution {
	subst := make(TypeSubstitution)
	for i, param := range typeParams {
		if i < len(typeArgs) {
			subst[param] = typeArgs[i]
		}
	}
	return subst
}

// cloneAndSubstituteParams clones and substitutes parameters.
func (m *Monomorphizer) cloneAndSubstituteParams(params []*ast.Param, subst TypeSubstitution) []*ast.Param {
	result := make([]*ast.Param, len(params))
	for i, param := range params {
		result[i] = &ast.Param{
			BaseNode: param.BaseNode,
			Name:     param.Name,
			Type:     m.substituteASTType(param.Type, subst),
		}
	}
	return result
}

// cloneAndSubstituteFields clones and substitutes struct fields.
func (m *Monomorphizer) cloneAndSubstituteFields(fields []*ast.Field, subst TypeSubstitution) []*ast.Field {
	result := make([]*ast.Field, len(fields))
	for i, field := range fields {
		result[i] = &ast.Field{
			BaseNode: field.BaseNode,
			Name:     field.Name,
			Type:     m.substituteASTType(field.Type, subst),
		}
	}
	return result
}

// cloner copies the body of a generic function for one choice of types:
// everything in it, with the type parameters replaced wherever a type is
// written. Every instance has nodes of its own, since the stages after
// this one record what they learn about a node under that node.
func (m *Monomorphizer) cloner(subst TypeSubstitution) *ast.Cloner {
	replaced := make(map[string]*ast.Type, len(subst))
	for name, concrete := range subst {
		replaced[name] = m.typeToASTType(concrete)
	}
	return &ast.Cloner{Types: replaced}
}

// cloneAndSubstituteBlock clones a block statement and substitutes types.
func (m *Monomorphizer) cloneAndSubstituteBlock(block *ast.BlockStmt, subst TypeSubstitution) *ast.BlockStmt {
	return m.cloner(subst).Block(block)
}

// cloneAndSubstituteStmt clones a statement and substitutes types.
func (m *Monomorphizer) cloneAndSubstituteStmt(stmt ast.Stmt, subst TypeSubstitution) ast.Stmt {
	return m.cloner(subst).Stmt(stmt)
}

// cloneAndSubstituteExpr clones an expression and substitutes types.
func (m *Monomorphizer) cloneAndSubstituteExpr(expr ast.Expr, subst TypeSubstitution) ast.Expr {
	return m.cloner(subst).Expr(expr)
}

// cloneAndSubstituteExprs clones and substitutes a slice of expressions.
func (m *Monomorphizer) cloneAndSubstituteExprs(exprs []ast.Expr, subst TypeSubstitution) []ast.Expr {
	result := make([]ast.Expr, len(exprs))
	for i, expr := range exprs {
		result[i] = m.cloneAndSubstituteExpr(expr, subst)
	}
	return result
}

// substituteASTType applies type substitution to an AST type.
func (m *Monomorphizer) substituteASTType(astType *ast.Type, subst TypeSubstitution) *ast.Type {
	if astType == nil {
		return nil
	}

	switch astType.Kind {
	case ast.TypeBasic:
		// Check if this is a type parameter
		if concreteType, ok := subst[astType.Name]; ok {
			// Convert the concrete type back to AST type
			return m.typeToASTType(concreteType)
		}
		// Otherwise, keep the type as-is but substitute generics
		newGenerics := make([]*ast.Type, len(astType.Generics))
		for i, g := range astType.Generics {
			newGenerics[i] = m.substituteASTType(g, subst)
		}
		return &ast.Type{
			BaseNode: astType.BaseNode,
			Kind:     astType.Kind,
			Name:     astType.Name,
			Generics: newGenerics,
		}
	case ast.TypeArray, ast.TypeOption, ast.TypeVariadic:
		// [T], T? and ...T hold the type they are made of
		return &ast.Type{
			BaseNode: astType.BaseNode,
			Kind:     astType.Kind,
			ElemType: m.substituteASTType(astType.ElemType, subst),
		}
	case ast.TypeUnion:
		members := make([]*ast.Type, len(astType.UnionTypes))
		for i, member := range astType.UnionTypes {
			members[i] = m.substituteASTType(member, subst)
		}
		return &ast.Type{BaseNode: astType.BaseNode, Kind: astType.Kind, UnionTypes: members}
	case ast.TypeFunction:
		newParams := make([]*ast.Type, len(astType.ParamTypes))
		for i, p := range astType.ParamTypes {
			newParams[i] = m.substituteASTType(p, subst)
		}
		return &ast.Type{
			BaseNode:   astType.BaseNode,
			Kind:       astType.Kind,
			ParamTypes: newParams,
			ReturnType: m.substituteASTType(astType.ReturnType, subst),
		}
	default:
		return astType
	}
}

// typeToASTType converts a types.Type to an AST type.
func (m *Monomorphizer) typeToASTType(t types.Type) *ast.Type {
	switch typ := t.(type) {
	case *types.PrimitiveType:
		// Written as a program writes it, Int and not int, which is the
		// name the program is checked again with
		if written := types.TypeToAST(typ, ast.BaseNode{}); written != nil {
			return written
		}
		return &ast.Type{
			Kind: ast.TypeBasic,
			Name: string(typ.Name),
		}
	case *types.StructType:
		// If the struct has type arguments, include them
		var generics []*ast.Type
		for _, arg := range typ.TypeArgs {
			generics = append(generics, m.typeToASTType(arg))
		}
		return &ast.Type{
			Kind:     ast.TypeBasic,
			Name:     typ.Name,
			Generics: generics,
		}
	case *types.ArrayType:
		return &ast.Type{
			Kind:     ast.TypeArray,
			ElemType: m.typeToASTType(typ.ElementType),
		}
	case *types.BehaviorType:
		// A trait, with its type arguments if it has any: Source<Int>
		var generics []*ast.Type
		for _, arg := range typ.TypeArgs {
			generics = append(generics, m.typeToASTType(arg))
		}
		return &ast.Type{Kind: ast.TypeBasic, Name: typ.Name, Generics: generics}
	case *types.TypeParam:
		// A type parameter of the function the call is in: it is replaced
		// in turn when that function is instantiated
		return &ast.Type{Kind: ast.TypeBasic, Name: typ.Name}
	case *types.UnionType:
		// T | null is written T?
		for _, member := range typ.Types {
			if !member.Equals(types.Null) {
				return &ast.Type{Kind: ast.TypeOption, ElemType: m.typeToASTType(member)}
			}
		}
		return &ast.Type{Kind: ast.TypeBasic, Name: "unknown"}
	case *types.FunctionType:
		params := make([]*ast.Type, len(typ.Params))
		for i, p := range typ.Params {
			params[i] = m.typeToASTType(p)
		}
		return &ast.Type{
			Kind:       ast.TypeFunction,
			ParamTypes: params,
			ReturnType: m.typeToASTType(typ.ReturnType),
		}
	default:
		return &ast.Type{
			Kind: ast.TypeBasic,
			Name: "unknown",
		}
	}
}

// typeParamsIn names the type parameters the type arguments of an instance
// still mention, in the order they come. A generic method that stays
// generic (those of impl<T> [T], of a generic enum) calls len<T>(self):
// the instance len_T is for whatever T will be, and declares T so that it
// is checked again with T known as a type parameter, not as no type.
func (m *Monomorphizer) typeParamsIn(args []types.Type) []string {
	var names []string
	seen := make(map[string]bool)
	var visit func(t types.Type)
	visit = func(t types.Type) {
		switch t := t.(type) {
		case *types.TypeParam:
			if !seen[t.Name] {
				seen[t.Name] = true
				names = append(names, t.Name)
			}
		case *types.ArrayType:
			visit(t.ElementType)
		case *types.FunctionType:
			for _, param := range t.Params {
				visit(param)
			}
			visit(t.ReturnType)
		case *types.UnionType:
			for _, member := range t.Types {
				visit(member)
			}
		case *types.StructType:
			for _, arg := range t.TypeArgs {
				visit(arg)
			}
		case *types.BehaviorType:
			for _, arg := range t.TypeArgs {
				visit(arg)
			}
		}
	}
	for _, arg := range args {
		visit(arg)
	}
	return names
}
