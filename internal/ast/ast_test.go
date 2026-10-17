package ast

import (
	"fmt"
	"testing"
)

func TestNewFeaturesAST(t *testing.T) {
	// 1. Trait Cloneable
	cloneableTrait := &TraitDecl{
		Name: "Cloneable",
		Methods: []*FnDecl{
			{
				Name: "clone",
				ReturnType: &Type{
					Kind: TypeBasic,
					Name: "Self",
				},
				IsTraitMethod: true,
			},
		},
	}

	// 2. Struct Point
	pointStruct := &StructDecl{
		Name: "Point",
		Fields: []*Field{
			{Name: "x", Type: &Type{Kind: TypeBasic, Name: "Int"}},
			{Name: "y", Type: &Type{Kind: TypeBasic, Name: "Int"}},
		},
	}

	// 3. Impl Cloneable for Point
	pointImpl := &ImplDecl{
		StructName: "Point",
		TraitName:  "Cloneable",
		Methods: []*FnDecl{
			{
				Name: "clone",
				ReturnType: &Type{
					Kind: TypeBasic,
					Name: "Self",
				},
				Body: &BlockStmt{
					Stmts: []Stmt{
						&ReturnStmt{
							Value: &StructInitExpr{
								Name: "Point",
								Fields: map[string]Expr{
									"x": &MemberExpr{Expr: &Ident{Name: "self"}, Name: "x"},
									"y": &MemberExpr{Expr: &Ident{Name: "self"}, Name: "y"},
								},
							},
						},
					},
				},
			},
		},
	}

	// 4. fn print_all(prefix: String, items: ...Any) -> Void
	printAllFn := &FnDecl{
		Name: "print_all",
		Params: []*Param{
			{Name: "prefix", Type: &Type{Kind: TypeBasic, Name: "String"}},
			{Name: "items", Type: &Type{Kind: TypeVariadic, ElemType: &Type{Kind: TypeBasic, Name: "Any"}}},
		},
		ReturnType: &Type{Kind: TypeBasic, Name: "Void"},
		Body: &BlockStmt{
			Stmts: []Stmt{
				&ExprStmt{Expr: &CallExpr{Func: &Ident{Name: "print"}, Args: []Expr{&Ident{Name: "prefix"}}}},
				&ForStmt{
					Var:      "item",
					Iterable: &Ident{Name: "items"},
					Body: &BlockStmt{
						Stmts: []Stmt{
							&ExprStmt{Expr: &CallExpr{Func: &Ident{Name: "print"}, Args: []Expr{&Literal{Value: " ", Kind: "string"}}}},
							&ExprStmt{Expr: &CallExpr{Func: &Ident{Name: "print"}, Args: []Expr{&CallExpr{Func: &Ident{Name: "toString"}, Args: []Expr{&Ident{Name: "item"}}}}}},
						},
					},
				},
				&ExprStmt{Expr: &CallExpr{Func: &Ident{Name: "println"}, Args: []Expr{&Literal{Value: "", Kind: "string"}}}},
				&ReturnStmt{},
			},
		},
	}

	// 5. fn collect<T>(items: ...T) -> [T]
	collectFn := &FnDecl{
		Name:       "collect",
		TypeParams: []string{"T"},
		Params: []*Param{
			{Name: "items", Type: &Type{Kind: TypeVariadic, ElemType: &Type{Kind: TypeBasic, Name: "T"}}},
		},
		ReturnType: &Type{Kind: TypeArray, ElemType: &Type{Kind: TypeBasic, Name: "T"}},
		Body: &BlockStmt{
			Stmts: []Stmt{
				&ReturnStmt{Value: &Ident{Name: "items"}},
			},
		},
	}

	// 6. fn main() -> Int
	mainFn := &FnDecl{
		Name:       "main",
		ReturnType: &Type{Kind: TypeBasic, Name: "Int"},
		Body: &BlockStmt{
			Stmts: []Stmt{
				&LetStmt{
					Name: "p1",
					Value: &StructInitExpr{
						Name: "Point",
						Fields: map[string]Expr{
							"x": &Literal{Value: "10", Kind: "int"},
							"y": &Literal{Value: "20", Kind: "int"},
						},
					},
				},
				&LetStmt{
					Name: "p2",
					Value: &CallExpr{
						Func: &MemberExpr{Expr: &Ident{Name: "p1"}, Name: "clone"},
					},
				},
				&ExprStmt{
					Expr: &CallExpr{
						Func: &Ident{Name: "print_all"},
						Args: []Expr{
							&Literal{Value: "Values:", Kind: "string"},
							&Literal{Value: "1", Kind: "int"},
							&Literal{Value: "two", Kind: "string"},
							&Literal{Value: "true", Kind: "bool"},
						},
					},
				},
				&LetStmt{
					Name: "numbers",
					Value: &CallExpr{
						Func: &Ident{
							Name:     "collect",
							TypeArgs: []*Type{{Kind: TypeBasic, Name: "Int"}},
						},
						Args: []Expr{
							&Literal{Value: "1", Kind: "int"},
							&Literal{Value: "2", Kind: "int"},
							&Literal{Value: "3", Kind: "int"},
							&Literal{Value: "4", Kind: "int"},
						},
					},
				},
				&IfStmt{
					Cond: &BinaryExpr{
						Left:  &IndexExpr{Expr: &Ident{Name: "numbers"}, Index: &Literal{Value: "0", Kind: "int"}},
						Op:    "!=",
						Right: &Literal{Value: "1", Kind: "int"},
					},
					Then: &BlockStmt{
						Stmts: []Stmt{
							&ReturnStmt{Value: &Literal{Value: "1", Kind: "int"}},
						},
					},
				},
				&ReturnStmt{Value: &Literal{Value: "0", Kind: "int"}},
			},
		},
	}

	prog := &Program{
		Decls: []Decl{cloneableTrait, pointStruct, pointImpl, printAllFn, collectFn, mainFn},
	}

	fmt.Println(prog.String())
}

func TestUnionTypeAST(t *testing.T) {
	// let x: Int | String = 10;
	stmt := &LetStmt{
		Name: "x",
		Type: &Type{
			Kind: TypeUnion,
			UnionTypes: []*Type{
				{Kind: TypeBasic, Name: "Int"},
				{Kind: TypeBasic, Name: "String"},
			},
		},
		Value: &Literal{Value: "10", Kind: "int"},
	}
	fmt.Println(stmt.String())
}

func TestMatchAndEnumAST(t *testing.T) {
	// enum Option<T> { Some(T), None }
	enumDecl := &EnumDecl{
		Name:       "Option",
		TypeParams: []string{"T"},
		Variants: []*EnumVariant{
			{Name: "Some", Values: []*Type{{Kind: TypeBasic, Name: "T"}}},
			{Name: "None"},
		},
	}

	// match x { case 1: { return "one"; } case 2: { return "two"; } }
	matchStmt := &MatchStmt{
		Cond: &Ident{Name: "x"},
		Cases: []*MatchCase{
			{
				Pattern: &Literal{Value: "1", Kind: "int"},
				Body: &BlockStmt{
					Stmts: []Stmt{&ReturnStmt{Value: &Literal{Value: "one", Kind: "string"}}},
				},
			},
			{
				Pattern: &Literal{Value: "2", Kind: "int"},
				Body: &BlockStmt{
					Stmts: []Stmt{&ReturnStmt{Value: &Literal{Value: "two", Kind: "string"}}},
				},
			},
		},
	}

	fmt.Println(enumDecl.String())
	fmt.Println(matchStmt.String())
}
