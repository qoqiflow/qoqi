package types

import (
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// The functions a literal of a dictionary is made with.
const (
	DictOf = "std.dict.of"
	MapOf  = "std.map.of"
)

// entries makes the call of a literal of a dictionary,
// __entries([(key, value), ...]), the call of the function that makes
// what it is: a Dict when its keys are texts, a Map otherwise, or what is
// expected where it stands. It reports whether it did; the call is
// then checked as any call. When it did not, what is wrong is said.
//
//	let ages = {"ada": 36};                   dict.of([("ada", 36)])
//	let seats = {(3, 4): "ada"};              map.of([((3, 4), "ada")])
//	let none: Map<Int, String> = {};          map.of<Int, String>([])
func (c *Checker) entries(e *ast.CallExpr, expected Type) bool {
	ident := e.Func.(*ast.Ident)
	array, ok := e.Args[0].(*ast.ArrayLiteralExpr)
	if len(e.Args) != 1 || !ok {
		c.checkArgsOnly(e)
		return false
	}

	// What is expected says which, and what the entries hold
	maker := ""
	var typeArgs []Type
	if st, ok := withoutNullOrNil(expected).(*StructType); ok {
		switch st.BaseName {
		case "std.dict.Dict":
			maker, typeArgs = DictOf, st.TypeArgs
		case "std.map.Map":
			maker, typeArgs = MapOf, st.TypeArgs
		}
	}
	// The keys are of one type
	var firstKey Type
	for _, element := range array.Elements {
		pair, ok := element.(*ast.CallExpr)
		if !ok || len(pair.Args) != 2 || (maker != "" && len(typeArgs) > 0) {
			break
		}
		key := c.checkExpr(pair.Args[0])
		if key == nil || isUnknown(key) {
			continue
		}
		if firstKey == nil {
			firstKey = key
			continue
		}
		if !c.assignable(firstKey, key) {
			c.errorfWithSuggestion(pair.Args[0].Token(), ErrTypeMismatch,
				"Give every key the type of the first, or make the keys of a union: let m: Map<Int | String, V> = {...};",
				"the keys of a dictionary are of one type: this one is %s, the first is %s", describe(key), describe(firstKey))
			return false
		}
	}

	if maker == "" {
		if len(array.Elements) == 0 {
			c.errorfWithSuggestion(e.Token(), ErrUntypedEmptyArray,
				"Say what it holds: let ages: Dict<Int> = {}; or map.empty<Int, String>()",
				"nothing says what an empty {} holds")
			c.checkArgsOnly(e)
			return false
		}
		// By the type of the keys
		maker = MapOf
		if firstKey != nil && firstKey.Equals(String) {
			maker = DictOf
		}
	}

	decl := c.libraryFunction(maker)
	if decl == nil {
		c.errorfWithSuggestion(e.Token(), ErrUnknownMethod,
			"A literal of a dictionary is made with std.dict and std.map, which the library of this program does not have",
			"cannot make a dictionary here")
		c.checkArgsOnly(e)
		return false
	}
	ident.Name = maker
	c.table.Defs[ident] = &resolve.Symbol{Name: maker, Kind: resolve.SymbolFunc, Decl: decl}
	if len(typeArgs) == len(decl.TypeParams) {
		ident.TypeArgs = nil
		for _, arg := range typeArgs {
			ident.TypeArgs = append(ident.TypeArgs, TypeToAST(arg, ident.BaseNode))
		}
	}
	return true
}

// libraryFunction finds a function of the standard library in the
// program, by its full name.
func (c *Checker) libraryFunction(name string) *ast.FnDecl {
	for _, decl := range c.prog.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == name {
			return fn
		}
	}
	return nil
}
