package monomorph

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/types"
)

// maxInstantiationDepth bounds how long a chain of instantiations, each
// required by the one before, may get. No real program comes near it; a
// chain that long means every instance needs a bigger one, as when f<T>
// calls f<[T]>.
const maxInstantiationDepth = 100

// resolveDependencies finds the instantiations that the known ones need in
// turn, until nothing new turns up: sorted<Int> calls slice<T> with T = Int,
// so slice<Int> is needed too. It returns every instantiation to generate,
// in the order they were found.
//
// Instances that need each other, or themselves, are fine: that is
// recursion, and the set of instances is finite. What cannot be compiled is
// a function whose instances require ever larger types (E3002); that shows
// as a chain of new instantiations that does not end.
func (m *Monomorphizer) resolveDependencies() []InstantiationKey {
	depth := make(map[InstantiationKey]int)
	reported := make(map[string]bool)
	var result []InstantiationKey

	// m.order grows while it is walked: findDependencies records the
	// instantiations it comes across
	for i := 0; i < len(m.order); i++ {
		key := m.order[i]
		inst := m.instantiations[key]

		if depth[key] > maxInstantiationDepth {
			if !reported[inst.BaseName] && len(inst.UseSites) > 0 {
				reported[inst.BaseName] = true
				msg := fmt.Sprintf("Generic function '%s' causes infinite instantiation. Detected cycle in type argument expansion.", inst.BaseName)
				suggestion := "Break the cycle by using a concrete type instead of recursively expanding the type parameter, or add a termination condition"
				m.errs.ReportErrorWithSuggestion(ErrInfiniteInstantiation, msg, suggestion, inst.UseSites[0].Token(), m.sourceLines)
			}
			continue // Do not expand further
		}
		result = append(result, key)

		known := len(m.order)
		m.findDependencies(inst)
		for _, found := range m.order[known:] {
			depth[found] = depth[key] + 1
		}
	}
	return result
}

// findDependencies finds all instantiations that a given instantiation depends on.
// This occurs when a generic function calls another generic function with type parameters.
func (m *Monomorphizer) findDependencies(inst *Instantiation) []InstantiationKey {
	var deps []InstantiationKey

	// Get the declaration of this instantiation
	switch decl := inst.Decl.(type) {
	case *ast.FnDecl:
		if decl.Body != nil {
			deps = m.findDepsInStmt(decl.Body, inst.TypeArgs, decl.TypeParams)
		}
	case *ast.StructDecl:
		// Struct fields might reference generic types
		for _, field := range decl.Fields {
			if field.Type != nil {
				deps = append(deps, m.findDepsInType(field.Type, inst.TypeArgs, decl.TypeParams)...)
			}
		}
	}

	return deps
}

// findDepsInStmt finds dependencies in a statement by looking for calls to generic functions.
func (m *Monomorphizer) findDepsInStmt(stmt ast.Stmt, typeArgs []types.Type, typeParams []string) []InstantiationKey {
	var deps []InstantiationKey

	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, st := range s.Stmts {
			deps = append(deps, m.findDepsInStmt(st, typeArgs, typeParams)...)
		}
	case *ast.LetStmt:
		if s.Value != nil {
			deps = append(deps, m.findDepsInExpr(s.Value, typeArgs, typeParams)...)
		}
	case *ast.ReturnStmt:
		if s.Value != nil {
			deps = append(deps, m.findDepsInExpr(s.Value, typeArgs, typeParams)...)
		}
	case *ast.IfStmt:
		deps = append(deps, m.findDepsInExpr(s.Cond, typeArgs, typeParams)...)
		deps = append(deps, m.findDepsInStmt(s.Then, typeArgs, typeParams)...)
		if s.Else != nil {
			deps = append(deps, m.findDepsInStmt(s.Else, typeArgs, typeParams)...)
		}
	case *ast.WhileStmt:
		deps = append(deps, m.findDepsInExpr(s.Cond, typeArgs, typeParams)...)
		deps = append(deps, m.findDepsInStmt(s.Body, typeArgs, typeParams)...)
	case *ast.ForStmt:
		deps = append(deps, m.findDepsInExpr(s.Iterable, typeArgs, typeParams)...)
		deps = append(deps, m.findDepsInStmt(s.Body, typeArgs, typeParams)...)
	case *ast.ExprStmt:
		deps = append(deps, m.findDepsInExpr(s.Expr, typeArgs, typeParams)...)
	default:
		exprs, stmts := insideStmt(stmt)
		for _, inner := range exprs {
			deps = append(deps, m.findDepsInExpr(inner, typeArgs, typeParams)...)
		}
		for _, inner := range stmts {
			deps = append(deps, m.findDepsInStmt(inner, typeArgs, typeParams)...)
		}
	}

	return deps
}

// findDepsInExpr finds dependencies in an expression.
func (m *Monomorphizer) findDepsInExpr(expr ast.Expr, typeArgs []types.Type, typeParams []string) []InstantiationKey {
	var deps []InstantiationKey

	switch e := expr.(type) {
	case *ast.CallExpr:
		if ident, ok := e.Func.(*ast.Ident); ok {
			if _, isGeneric := m.generics[ident.Name]; isGeneric {
				if len(ident.TypeArgs) > 0 {
					// Substitute type parameters with concrete types
					concreteTypeArgs := m.substituteTypeParams(ident.TypeArgs, typeArgs, typeParams)
					key := MakeInstantiationKey(ident.Name, concreteTypeArgs)
					deps = append(deps, key)

					// Make sure this instantiation is recorded
					if _, exists := m.instantiations[key]; !exists {
						m.recordInstantiation(key, ident.Name, concreteTypeArgs, m.generics[ident.Name], e)
					}
				}
			}
		}

		deps = append(deps, m.findDepsInExpr(e.Func, typeArgs, typeParams)...)
		for _, arg := range e.Args {
			deps = append(deps, m.findDepsInExpr(arg, typeArgs, typeParams)...)
		}

	case *ast.BinaryExpr:
		deps = append(deps, m.findDepsInExpr(e.Left, typeArgs, typeParams)...)
		deps = append(deps, m.findDepsInExpr(e.Right, typeArgs, typeParams)...)
	case *ast.UnaryExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
	case *ast.CastExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
	case *ast.TypeTestExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
	case *ast.PatternTestExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
	case *ast.MemberExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
	case *ast.IndexExpr:
		deps = append(deps, m.findDepsInExpr(e.Expr, typeArgs, typeParams)...)
		deps = append(deps, m.findDepsInExpr(e.Index, typeArgs, typeParams)...)
	case *ast.ArrayLiteralExpr:
		for _, elem := range e.Elements {
			deps = append(deps, m.findDepsInExpr(elem, typeArgs, typeParams)...)
		}
	case *ast.StructInitExpr:
		for _, fieldExpr := range e.Fields {
			deps = append(deps, m.findDepsInExpr(fieldExpr, typeArgs, typeParams)...)
		}
	case *ast.FnExpr:
		if e.Body != nil {
			deps = append(deps, m.findDepsInStmt(e.Body, typeArgs, typeParams)...)
		}
	default:
		exprs, stmts := insideExpr(expr)
		for _, inner := range exprs {
			deps = append(deps, m.findDepsInExpr(inner, typeArgs, typeParams)...)
		}
		for _, inner := range stmts {
			deps = append(deps, m.findDepsInStmt(inner, typeArgs, typeParams)...)
		}
	}

	return deps
}

// findDepsInType finds dependencies in a type annotation.
func (m *Monomorphizer) findDepsInType(astType *ast.Type, typeArgs []types.Type, typeParams []string) []InstantiationKey {
	var deps []InstantiationKey

	switch astType.Kind {
	case ast.TypeBasic:
		// Check if this is a generic struct instantiation
		if len(astType.Generics) > 0 {
			if _, isGeneric := m.generics[astType.Name]; isGeneric {
				concreteTypeArgs := m.substituteTypeParams(astType.Generics, typeArgs, typeParams)
				key := MakeInstantiationKey(astType.Name, concreteTypeArgs)
				deps = append(deps, key)

				// Make sure this instantiation is recorded
				if _, exists := m.instantiations[key]; !exists {
					m.recordInstantiation(key, astType.Name, concreteTypeArgs, m.generics[astType.Name], astType)
				}
			}
		}
	case ast.TypeArray:
		deps = append(deps, m.findDepsInType(astType.ElemType, typeArgs, typeParams)...)
	case ast.TypeFunction:
		for _, param := range astType.ParamTypes {
			deps = append(deps, m.findDepsInType(param, typeArgs, typeParams)...)
		}
		if astType.ReturnType != nil {
			deps = append(deps, m.findDepsInType(astType.ReturnType, typeArgs, typeParams)...)
		}
	}

	return deps
}

// substituteTypeParams substitutes type parameters with concrete types.
func (m *Monomorphizer) substituteTypeParams(astTypes []*ast.Type, typeArgs []types.Type, typeParams []string) []types.Type {
	result := make([]types.Type, len(astTypes))

	for i, astType := range astTypes {
		result[i] = m.substituteTypeParam(astType, typeArgs, typeParams)
	}

	return result
}

// substituteTypeParam converts a type written inside a generic declaration
// to the type it stands for in one instance: type parameters are replaced
// by the instance's type arguments, wherever they occur in the type.
func (m *Monomorphizer) substituteTypeParam(astType *ast.Type, typeArgs []types.Type, typeParams []string) types.Type {
	if astType == nil {
		return types.Void
	}
	sub := func(t *ast.Type) types.Type { return m.substituteTypeParam(t, typeArgs, typeParams) }

	switch astType.Kind {
	case ast.TypeBasic:
		for i, param := range typeParams {
			if astType.Name == param && i < len(typeArgs) {
				return typeArgs[i]
			}
		}
		// Box<T>: the struct, with its arguments substituted
		if len(astType.Generics) > 0 {
			if st, ok := m.astTypeToType(astType).(*types.StructType); ok {
				instance := *st
				instance.TypeArgs = m.substituteTypeParams(astType.Generics, typeArgs, typeParams)
				return &instance
			}
		}
	case ast.TypeArray:
		return &types.ArrayType{ElementType: sub(astType.ElemType)}
	case ast.TypeOption:
		return types.NewUnion(sub(astType.ElemType), types.Null)
	case ast.TypeFunction:
		return types.NewFunction(m.substituteTypeParams(astType.ParamTypes, typeArgs, typeParams), sub(astType.ReturnType))
	}

	// No type parameter can occur in anything else
	return m.astTypeToType(astType)
}
