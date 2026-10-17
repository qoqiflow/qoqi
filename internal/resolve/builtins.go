package resolve

import "github.com/qoqiflow/qoqi/internal/ast"

// LengthFunction is the builtin that gives the length of an array or of
// a text. The standard library's two `len` are written with it; the
// language has no `.length`.
const LengthFunction = "__len"

// PushFunction is the builtin that adds an item at the end of an array.
// The method `push` of arrays is written with it.
const PushFunction = "__push"

// SliceFunction is the builtin that takes a part of an array or of a
// text: what items[1..3] is made of, with what the range is made of.
const SliceFunction = "__slice"

// VariablePrefix starts the name of the function that makes the place of a
// variable declared outside functions: an array of one item, made the
// first time the variable is used and changed in place from then on.
const VariablePrefix = "__var."

// ConstantPrefix starts the name of the function that makes the value of
// a constant that is a struct, an array or a value of an enum. No program
// writes such a function: the compiler does, one for each of those
// constants, and a use of the constant is a call of it that is only run
// the first time.
const ConstantPrefix = "__const."

// Builtins returns declarations for the host-provided functions that every
// module can call without importing anything. They have no body; the VM
// supplies the implementation as extern functions of the same name.
func Builtins() []*ast.FnDecl {
	basic := func(name string) *ast.Type {
		return &ast.Type{Kind: ast.TypeBasic, Name: name}
	}
	variadicAny := func(name string) []*ast.Param {
		return []*ast.Param{{
			Name: name,
			Type: &ast.Type{Kind: ast.TypeVariadic, ElemType: basic("Any")},
		}}
	}

	return []*ast.FnDecl{
		{Name: "print", Params: variadicAny("args"), ReturnType: basic("Void")},
		{Name: "println", Params: variadicAny("args"), ReturnType: basic("Void")},
		{
			Name:       "toString",
			Params:     []*ast.Param{{Name: "value", Type: basic("Any")}},
			ReturnType: basic("String"),
		},
		// What `${expr}` in a string calls: toString under a name no
		// program writes
		{
			Name:       "__text",
			Params:     []*ast.Param{{Name: "value", Type: basic("Any")}},
			ReturnType: basic("String"),
		},
		// How many items an array holds, or characters a text: what
		// arrays.len and strings.len are made of, and what a loop over a
		// collection counts with. One instruction, under a name no
		// program writes.
		{
			Name:       LengthFunction,
			Params:     []*ast.Param{{Name: "collection", Type: basic("Any")}},
			ReturnType: basic("Int"),
		},
		// Stops the program with a message. It never returns.
		{Name: "panic", Params: variadicAny("message"), ReturnType: basic("Void")},
		// Adds an item at the end of an array: what items.push(value) is
		// made of, under a name no program writes
		{
			Name: PushFunction,
			Params: []*ast.Param{
				{Name: "array", Type: basic("Any")},
				{Name: "value", Type: basic("Any")},
			},
			ReturnType: basic("Void"),
		},
		// What a literal of a dictionary calls, until the type checker
		// makes it dict.of or map.of
		{Name: ast.EntriesFunction, Params: variadicAny("entries"), ReturnType: basic("Any")},
		// The items of an array, or the characters of a text, at the
		// positions a range goes through: items[1..3]
		{
			Name: SliceFunction,
			Params: []*ast.Param{
				{Name: "collection", Type: basic("Any")},
				{Name: "from", Type: basic("Int")},
				{Name: "to", Type: basic("Int")},
				{Name: "inclusive", Type: basic("Bool")},
				{Name: "by", Type: basic("Int")},
			},
			ReturnType: basic("Any"),
		},
	}
}

// IsBuiltin reports whether name is a host-provided function.
func IsBuiltin(name string) bool {
	for _, fn := range Builtins() {
		if fn.Name == name {
			return true
		}
	}
	return false
}
