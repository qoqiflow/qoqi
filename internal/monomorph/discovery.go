package monomorph

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/types"
)

// discoverInstantiations walks the AST to find all uses of generic functions and types.
func (m *Monomorphizer) discoverInstantiations() {
	for _, decl := range m.prog.Decls {
		m.visitDeclForDiscovery(decl)
	}
}

func (m *Monomorphizer) visitDeclForDiscovery(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		if d.Body != nil {
			m.visitStmtForDiscovery(d.Body)
		}
	case *ast.ImplDecl:
		for _, method := range d.Methods {
			if method.Body != nil {
				m.visitStmtForDiscovery(method.Body)
			}
		}
	}
}

func (m *Monomorphizer) visitStmtForDiscovery(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, st := range s.Stmts {
			m.visitStmtForDiscovery(st)
		}
	case *ast.LetStmt:
		if s.Value != nil {
			m.visitExprForDiscovery(s.Value)
		}
	case *ast.ReturnStmt:
		if s.Value != nil {
			m.visitExprForDiscovery(s.Value)
		}
	case *ast.IfStmt:
		m.visitExprForDiscovery(s.Cond)
		m.visitStmtForDiscovery(s.Then)
		if s.Else != nil {
			m.visitStmtForDiscovery(s.Else)
		}
	case *ast.WhileStmt:
		m.visitExprForDiscovery(s.Cond)
		m.visitStmtForDiscovery(s.Body)
	case *ast.ForStmt:
		m.visitExprForDiscovery(s.Iterable)
		m.visitStmtForDiscovery(s.Body)
	case *ast.ExprStmt:
		m.visitExprForDiscovery(s.Expr)
	default:
		exprs, stmts := insideStmt(stmt)
		for _, inner := range exprs {
			m.visitExprForDiscovery(inner)
		}
		for _, inner := range stmts {
			m.visitStmtForDiscovery(inner)
		}
	}
}

func (m *Monomorphizer) visitExprForDiscovery(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		// Check if this is a call to a generic function
		if ident, ok := e.Func.(*ast.Ident); ok {
			if genericDecl, isGeneric := m.generics[ident.Name]; isGeneric {
				// Extract type arguments from the identifier or infer them
				typeArgs := m.extractTypeArgsFromIdent(ident, genericDecl)
				if typeArgs != nil {
					key := MakeInstantiationKey(ident.Name, typeArgs)
					m.recordInstantiation(key, ident.Name, typeArgs, genericDecl, e)
				}
			}
		}

		m.visitExprForDiscovery(e.Func)
		for _, arg := range e.Args {
			m.visitExprForDiscovery(arg)
		}

	case *ast.BinaryExpr:
		m.visitExprForDiscovery(e.Left)
		m.visitExprForDiscovery(e.Right)
	case *ast.UnaryExpr:
		m.visitExprForDiscovery(e.Expr)
	case *ast.CastExpr:
		m.visitExprForDiscovery(e.Expr)
	case *ast.TypeTestExpr:
		m.visitExprForDiscovery(e.Expr)
	case *ast.PatternTestExpr:
		m.visitExprForDiscovery(e.Expr)
	case *ast.FnExpr:
		if e.Body != nil {
			m.visitStmtForDiscovery(e.Body)
		}
	case *ast.MemberExpr:
		m.visitExprForDiscovery(e.Expr)
	case *ast.IndexExpr:
		m.visitExprForDiscovery(e.Expr)
		m.visitExprForDiscovery(e.Index)
	case *ast.ArrayLiteralExpr:
		for _, elem := range e.Elements {
			m.visitExprForDiscovery(elem)
		}
	case *ast.StructInitExpr:
		for _, fieldExpr := range e.Fields {
			m.visitExprForDiscovery(fieldExpr)
		}
		// Also check if the struct itself is generic
		if len(e.TypeArgs) > 0 {
			if genericDecl, isGeneric := m.generics[e.Name]; isGeneric {
				typeArgs := m.convertASTTypesToTypes(e.TypeArgs)

				// Validate type argument count
				var expectedCount int
				if structDecl, ok := genericDecl.(*ast.StructDecl); ok {
					expectedCount = len(structDecl.TypeParams)
				}

				if len(typeArgs) != expectedCount {
					msg := fmt.Sprintf("Type argument count mismatch for struct '%s': expected %d type arguments, got %d",
						e.Name, expectedCount, len(typeArgs))

					// Build suggestion showing the correct syntax
					suggestion := fmt.Sprintf("Use '%s<", e.Name)
					for i := 0; i < expectedCount; i++ {
						if i > 0 {
							suggestion += ", "
						}
						suggestion += fmt.Sprintf("Type%d", i+1)
					}
					suggestion += "> { ... }' with the correct number of type arguments"

					m.errs.ReportErrorWithSuggestion(ErrTypeArgMismatch, msg, suggestion, e.Token(), m.sourceLines)
					return
				}

				key := MakeInstantiationKey(e.Name, typeArgs)
				m.recordInstantiation(key, e.Name, typeArgs, genericDecl, e)
			}
		}
	default:
		exprs, stmts := insideExpr(expr)
		for _, inner := range exprs {
			m.visitExprForDiscovery(inner)
		}
		for _, inner := range stmts {
			m.visitStmtForDiscovery(inner)
		}
	}
}

// extractTypeArgsFromIdent extracts type arguments from an identifier.
// This can come from explicit type arguments or from type inference.
func (m *Monomorphizer) extractTypeArgsFromIdent(ident *ast.Ident, genericDecl ast.Node) []types.Type {
	// First check if there are explicit type arguments in the identifier
	if len(ident.TypeArgs) > 0 {
		typeArgs := m.convertASTTypesToTypes(ident.TypeArgs)

		// Validate type argument count matches type parameters
		var expectedCount int
		switch decl := genericDecl.(type) {
		case *ast.FnDecl:
			expectedCount = len(decl.TypeParams)
		case *ast.StructDecl:
			expectedCount = len(decl.TypeParams)
		}

		if len(typeArgs) != expectedCount {
			msg := fmt.Sprintf("Type argument count mismatch for '%s': expected %d type arguments, got %d",
				ident.Name, expectedCount, len(typeArgs))

			// Build suggestion showing the correct syntax
			suggestion := fmt.Sprintf("Use '%s<", ident.Name)
			for i := 0; i < expectedCount; i++ {
				if i > 0 {
					suggestion += ", "
				}
				suggestion += fmt.Sprintf("Type%d", i+1)
			}
			suggestion += ">' with the correct number of type arguments"

			m.errs.ReportErrorWithSuggestion(ErrTypeArgMismatch, msg, suggestion, ident.Token(), m.sourceLines)
			return nil
		}

		return typeArgs
	}

	// Otherwise, try to infer from the type info
	// The type checker should have already performed type inference
	// and stored the result in the type info
	// For now, we'll need to rely on explicit type arguments
	// Full type inference integration would require the type checker
	// to store inferred type arguments separately

	// Report error for unbounded generics (no type arguments and no inference)
	msg := fmt.Sprintf("Cannot infer type arguments for generic '%s'. Please provide explicit type arguments.", ident.Name)

	// Determine expected type parameter count for suggestion
	var expectedCount int
	switch decl := genericDecl.(type) {
	case *ast.FnDecl:
		expectedCount = len(decl.TypeParams)
	case *ast.StructDecl:
		expectedCount = len(decl.TypeParams)
	}

	// Build helpful suggestion
	suggestion := fmt.Sprintf("Add explicit type arguments: '%s<", ident.Name)
	for i := 0; i < expectedCount; i++ {
		if i > 0 {
			suggestion += ", "
		}
		suggestion += "Type"
	}
	suggestion += ">'"

	m.errs.ReportErrorWithSuggestion(ErrUnboundedGenerics, msg, suggestion, ident.Token(), m.sourceLines)

	return nil
}

// convertASTTypesToTypes converts AST type nodes to types.Type.
func (m *Monomorphizer) convertASTTypesToTypes(astTypes []*ast.Type) []types.Type {
	result := make([]types.Type, len(astTypes))
	for i, astType := range astTypes {
		result[i] = m.astTypeToType(astType)
	}
	return result
}

// astTypeToType converts a single AST type to types.Type.
func (m *Monomorphizer) astTypeToType(astType *ast.Type) types.Type {
	switch astType.Kind {
	case ast.TypeBasic:
		// Check if it's a primitive type
		// Both spellings occur: `Int` as written in source, and `int` when
		// a type computed by the checker was turned back into syntax
		switch astType.Name {
		case "Int", "int":
			return types.Int
		case "Float", "float":
			return types.Float
		case "Bool", "bool":
			return types.Bool
		case "String", "string":
			return types.String
		case "Void", "void":
			return types.Void
		case "Any", "any":
			return types.Any
		default:
			// It's a named type (struct, enum, etc.)
			// Look it up in the type environment first
			if baseDef, ok := m.typeInfo.Defs[astType.Name]; ok {
				// If it has type arguments, we need to create an instantiated version
				if len(astType.Generics) > 0 {
					typeArgs := m.convertASTTypesToTypes(astType.Generics)

					// Create an instantiated version of the type
					if structType, ok := baseDef.(*types.StructType); ok {
						return &types.StructType{
							Name:       astType.Name, // Will be mangled later if needed
							BaseName:   structType.BaseName,
							Fields:     structType.Fields,
							TypeParams: nil, // Instantiated types have no type params
							TypeArgs:   typeArgs,
						}
					}
				}
				// Return the base type without type arguments
				return baseDef
			}

			// Fallback: Type not found in registry
			// This might happen if type checking hasn't populated the registry yet
			// or if we're dealing with a forward reference
			typeArgs := m.convertASTTypesToTypes(astType.Generics)
			return &types.StructType{
				Name:     astType.Name,
				BaseName: astType.Name,
				TypeArgs: typeArgs,
			}
		}
	case ast.TypeArray:
		elemType := m.astTypeToType(astType.ElemType)
		return &types.ArrayType{ElementType: elemType}
	case ast.TypeOption:
		return types.NewUnion(m.astTypeToType(astType.ElemType), types.Null)
	case ast.TypeFunction:
		params := m.convertASTTypesToTypes(astType.ParamTypes)
		returnType := m.astTypeToType(astType.ReturnType)
		return types.NewFunction(params, returnType)
	}
	return types.Void
}

// recordInstantiation records a generic instantiation.
func (m *Monomorphizer) recordInstantiation(key InstantiationKey, baseName string, typeArgs []types.Type, decl ast.Node, useSite ast.Node) {
	if inst, exists := m.instantiations[key]; exists {
		inst.UseSites = append(inst.UseSites, useSite)
	} else {
		m.order = append(m.order, key)
		m.instantiations[key] = &Instantiation{
			Key:      key,
			BaseName: baseName,
			TypeArgs: typeArgs,
			Decl:     decl,
			UseSites: []ast.Node{useSite},
		}
	}
}
