package types

import "github.com/qoqiflow/qoqi/internal/ast"

// Info holds the result of type checking.
type Info struct {
	// Types maps expressions to their computed types.
	Types map[ast.Expr]Type

	// StageTypes gives, for each stage of a flow, the type of each
	// variable in scope there, and of each field of the flow as
	// "self.name": the shape of the stage
	StageTypes map[*ast.ExprStmt]map[string]string

	// StructFields gives, for each struct of the program, the type of
	// each field, as the source writes it
	StructFields map[string]map[string]string

	// Defs maps type names to their definitions (structs, enums, etc.)
	// This is populated during type checking for use by monomorphization
	Defs map[string]Type

	// Casts maps each cast that has to be verified at runtime to the
	// description of what the value must be. Casts that always succeed are
	// absent.
	Casts map[*ast.CastExpr]string

	// Tests maps each type test to the description of the type it tests
	// for, in the same form as Casts, and TestedTypes to that type.
	Tests       map[*ast.TypeTestExpr]string
	TestedTypes map[*ast.TypeTestExpr]Type

	// Tries maps each `value?` to what its operand is: TryResult or
	// TryOptional. One that was refused is absent.
	Tries map[*ast.UnaryExpr]string

	// What an operator, a loop or an index stands for when its subject is
	// a struct that implements the trait behind it (see traits.go). The
	// desugarer writes the method calls.
	OperatorMethods map[*ast.BinaryExpr]string        // a + b: the method to call on a
	Iterations      map[*ast.ForStmt]Type             // for x in a: the type of x, when a is an Iterable
	IndexReads      map[*ast.IndexExpr]bool           // a[k], when a is an Indexed
	TypeMethods     map[*ast.MemberExpr]*FunctionType // Point.norm: the method, without the value it is called on
	Defaults        map[*ast.CallExpr][]ast.Expr      // The values of the parameters a call leaves out
	IndexWrites     map[*ast.BinaryExpr]bool          // a[k] = v, when a is an IndexedSet

	// MethodValues maps each method used as a value, `users.get` without
	// a call, to the type of the function it stands for.
	MethodValues map[*ast.MemberExpr]*FunctionType

	// NarrowedNames maps a name of a pattern test, the `r` of
	// `if shape is Shape.Circle(r) && r != null`, to the type the rest of
	// the condition established for it where the guarded block starts,
	// when that is more than the type of the value it names.
	NarrowedNames map[*ast.BindingPattern]Type

	implements func(t Type, trait string) bool

	// Signatures maps each function declaration and function expression to
	// the description of its type, in the same form as Casts.
	Signatures map[ast.Node]string
}

func NewInfo() *Info {
	return &Info{
		Types:        make(map[ast.Expr]Type),
		StageTypes:   make(map[*ast.ExprStmt]map[string]string),
		StructFields: make(map[string]map[string]string),
		Defs:         make(map[string]Type),
		Casts:        make(map[*ast.CastExpr]string),

		Tests:       make(map[*ast.TypeTestExpr]string),
		TestedTypes: make(map[*ast.TypeTestExpr]Type),
		Signatures:  make(map[ast.Node]string),
		Tries:       make(map[*ast.UnaryExpr]string),

		OperatorMethods: make(map[*ast.BinaryExpr]string),
		Iterations:      make(map[*ast.ForStmt]Type),
		IndexReads:      make(map[*ast.IndexExpr]bool),
		TypeMethods:     make(map[*ast.MemberExpr]*FunctionType),
		Defaults:        make(map[*ast.CallExpr][]ast.Expr),
		IndexWrites:     make(map[*ast.BinaryExpr]bool),
		MethodValues:    make(map[*ast.MemberExpr]*FunctionType),
		NarrowedNames:   make(map[*ast.BindingPattern]Type),
	}
}
