package types

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
)

// Traits a type parameter is required to implement, traits with type
// parameters of their own, and the traits of the standard library that
// stand behind operators and loops.

// The traits the compiler knows by name. They are declared in the module
// std.ops of the standard library; a program that implements one has
// imported that module, which is how the declarations come to be there.
const (
	TraitEqual      = "std.ops.Equal"      // a == b       a.equals(b)
	TraitOrdered    = "std.ops.Ordered"    // a < b        a.compare(b) < 0
	TraitAdd        = "std.ops.Add"        // a + b        a.plus(b)
	TraitSubtract   = "std.ops.Subtract"   // a - b        a.minus(b)
	TraitMultiply   = "std.ops.Multiply"   // a * b        a.times(b)
	TraitDivide     = "std.ops.Divide"     // a / b        a.dividedBy(b)
	TraitIterable   = "std.ops.Iterable"   // for x in a   for x in a.iterate()
	TraitIndexed    = "std.ops.Indexed"    // a[k]         a.at(k)
	TraitIndexedSet = "std.ops.IndexedSet" // a[k] = v     a.put(k, v)
	TraitShow       = "std.ops.Show"       // "${a}"       a.show()

	MethodIterate = "iterate"
	MethodAt      = "at"
	MethodPut     = "put"
	MethodShow    = "show"
)

// OperatorMethod says which method of which trait an operator stands for
// on a value that is not a number or a text.
type OperatorMethod struct {
	Trait  string
	Method string
}

// Operators maps each operator a trait can give a struct.
var Operators = map[string]OperatorMethod{
	"==": {TraitEqual, "equals"},
	"!=": {TraitEqual, "equals"},
	"<":  {TraitOrdered, "compare"},
	"<=": {TraitOrdered, "compare"},
	">":  {TraitOrdered, "compare"},
	">=": {TraitOrdered, "compare"},
	"+":  {TraitAdd, "plus"},
	"-":  {TraitSubtract, "minus"},
	"*":  {TraitMultiply, "times"},
	"/":  {TraitDivide, "dividedBy"},
}

// typeKey names a type the way an impl does: the name of a struct or an
// enum, or of a builtin type. It is "" for a type nothing can be
// implemented for.
// ImplKey is the name impls are kept under for a type: that of its struct
// or enum, of the builtin type, "[]" for an array; "" for anything else.
func ImplKey(t Type) string { return typeKey(t) }

func typeKey(t Type) string {
	switch t := t.(type) {
	case *StructType:
		if t.BaseName != "" {
			return t.BaseName
		}
		return t.Name
	case *ArrayType:
		return ast.ArrayImpl
	case *PrimitiveType:
		switch t.Name {
		case PrimitiveInt:
			return "Int"
		case PrimitiveFloat:
			return "Float"
		case PrimitiveString:
			return "String"
		case PrimitiveBool:
			return "Bool"
		}
	}
	return ""
}

// IsBuiltinTypeName reports whether an impl of that name is for a builtin
// type rather than for a struct or an enum.
func IsBuiltinTypeName(name string) bool {
	if _, arrays := ast.IsArrayImpl(name); arrays {
		return true
	}
	return name == "Int" || name == "Float" || name == "String" || name == "Bool" || name == ast.OptionalImpl
}

// bindBounds brings the requirements of type parameters into scope, for
// as long as their names are.
func (c *Checker) bindBounds(bounds map[string][]*ast.Type) func() {
	if len(bounds) == 0 {
		return func() {}
	}
	previous := make(map[string][]*ast.Type, len(bounds))
	had := make(map[string]bool, len(bounds))
	for name, traits := range bounds {
		previous[name], had[name] = c.bounds[name]
		c.bounds[name] = traits
	}
	return func() {
		for name := range bounds {
			if had[name] {
				c.bounds[name] = previous[name]
			} else {
				delete(c.bounds, name)
			}
		}
	}
}

// boundsOf returns the traits a type parameter in scope has to implement.
func (c *Checker) boundsOf(param *TypeParam) []*BehaviorType {
	var traits []*BehaviorType
	for _, bound := range c.bounds[param.Name] {
		if trait, ok := c.resolveType(bound).(*BehaviorType); ok {
			traits = append(traits, trait)
		}
	}
	return traits
}

// traitArgsOf returns the type arguments with which a type implements a
// trait (none for a trait without type parameters), and whether it
// implements it at all.
//
// A struct implements what its impls say; a type parameter what it is
// required to; and a value seen through a trait implements that trait.
func (c *Checker) traitArgsOf(t Type, trait string) ([]Type, bool) {
	switch t := t.(type) {
	case *TypeParam:
		for _, bound := range c.boundsOf(t) {
			if bound.Name == trait {
				return bound.TypeArgs, true
			}
		}
	case *BehaviorType:
		if t.Name == trait {
			return t.TypeArgs, true
		}
	default:
		impl := c.traitImpls[typeKey(t)][trait]
		if impl == nil {
			return nil, false
		}
		// impl<T> Source<T> for List<T>: for a List<Int>, T is Int
		bindings := make(map[string]Type, len(impl.TypeParams))
		var given []Type
		if st, ok := t.(*StructType); ok {
			given = st.TypeArgs
		}
		for i, name := range impl.TypeParams {
			bindings[name] = Unknown
			if i < len(given) {
				bindings[name] = given[i]
			}
		}
		defer c.bind(bindings)()
		args := make([]Type, len(impl.TraitArgs))
		for i, arg := range impl.TraitArgs {
			args[i] = c.resolveType(arg)
		}
		return args, true
	}
	return nil, false
}

// implements reports whether a type implements a trait, with the type
// arguments the trait is given.
func (c *Checker) implements(t Type, trait *BehaviorType) bool {
	if t == nil || isUnknown(t) {
		return true
	}
	args, ok := c.traitArgsOf(t, trait.Name)
	if !ok {
		return false
	}
	for i, want := range trait.TypeArgs {
		if i < len(args) && !c.sameType(want, args[i]) {
			return false
		}
	}
	return true
}

// Implements reports whether a type implements a trait of the given
// name, whatever its type arguments. It is for the stages after the
// checker, which have types and no checker.
func (info *Info) Implements(t Type, trait string) bool {
	if info.implements == nil {
		return false
	}
	return info.implements(t, trait)
}

// requireBounds checks the type arguments given for type parameters
// against what the parameters require. owner names what they are the
// parameters of, for the message.
func (c *Checker) requireBounds(names []string, bounds map[string][]*ast.Type, args []Type, at token.Token, owner string) {
	if len(bounds) == 0 || len(args) != len(names) {
		return
	}
	// A requirement may name the other parameters: <T, S: Source<T>>
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		bindings[name] = args[i]
	}
	defer c.bind(bindings)()

	for i, name := range names {
		for _, bound := range bounds[name] {
			trait, ok := c.resolveType(bound).(*BehaviorType)
			if !ok || c.implements(args[i], trait) {
				continue
			}
			// As the program writes it: Ordered, not std.ops.Ordered
			written := displayName(trait.Name) + strings.TrimPrefix(describe(trait), trait.Name)
			suggestion := fmt.Sprintf("Add: impl %s for %s { ... }", written, describe(args[i]))
			if param, isParam := args[i].(*TypeParam); isParam {
				suggestion = fmt.Sprintf("Require it where %s is introduced: <%s: %s>", param.Name, param.Name, written)
			}
			c.errorfWithSuggestion(at, ErrBoundNotMet, suggestion,
				"%s does not implement %s, which %s requires of %s", args[i], written, owner, name)
		}
	}
}

// methodOn finds a method as seen on a receiver, and what the type
// parameters of the trait that declares it stand for there, when it is a
// method of a trait: the T of `trait Source<T>` for a receiver that is a
// Source<Int>.
func (c *Checker) methodOn(recv Type, name string) (*ast.FnDecl, map[string]Type) {
	fromTrait := func(trait *BehaviorType) (*ast.FnDecl, map[string]Type) {
		decl := c.traits[trait.Name]
		if decl == nil {
			return nil, nil
		}
		for _, m := range decl.Methods {
			if m.Name != name {
				continue
			}
			bindings := make(map[string]Type, len(decl.TypeParams))
			for i, param := range decl.TypeParams {
				bindings[param] = Unknown
				if i < len(trait.TypeArgs) {
					bindings[param] = trait.TypeArgs[i]
				}
			}
			return m, bindings
		}
		return nil, nil
	}

	switch t := recv.(type) {
	case *StructType:
		return c.methods[t.Name][name], nil
	case *PrimitiveType:
		if key := typeKey(t); key != "" {
			return c.methods[key][name], nil
		}
	case *ArrayType:
		// What the arrays of its items have, then what every array has
		if item := typeKey(t.ElementType); item != "" && item != ast.ArrayImpl {
			if decl := c.methods[ast.ArrayImplOf(item)][name]; decl != nil {
				return decl, nil
			}
		}
		if decl := c.methods[ast.ArrayImpl][name]; decl != nil {
			return decl, nil
		}
		// [].sum(): nothing says what the array holds. When only the
		// arrays of one type have the method, it is theirs; when those of
		// whole numbers are among several, theirs, as 0 is an Int
		if isUnknown(t.ElementType) {
			if decl := c.methods[ast.ArrayImplOf("Int")][name]; decl != nil {
				return decl, nil
			}
			var found *ast.FnDecl
			for owner, methods := range c.methods {
				if item, arrays := ast.IsArrayImpl(owner); arrays && item != "" && methods[name] != nil {
					if found != nil {
						return nil, nil
					}
					found = methods[name]
				}
			}
			return found, nil
		}
		return nil, nil
	case *UnionType:
		// impl<T> T? { ... }: what every optional has
		if hasNull(t) {
			return c.methods[ast.OptionalImpl][name], nil
		}
	case *BehaviorType:
		return fromTrait(t)
	case *TypeParam:
		for _, bound := range c.boundsOf(t) {
			if decl, bindings := fromTrait(bound); decl != nil {
				return decl, bindings
			}
		}
	}
	return nil, nil
}

// checkImplHeader checks what an impl says before its methods: that the
// trait is given as many type arguments as it has parameters.
func (c *Checker) checkImplHeader(d *ast.ImplDecl, trait *ast.TraitDecl) bool {
	if len(d.TraitArgs) == len(trait.TypeParams) {
		return true
	}
	params := strings.Join(trait.TypeParams, ", ")
	if len(trait.TypeParams) == 0 {
		c.errorfWithSuggestion(d.Token(), ErrSignatureMismatch, fmt.Sprintf("Write: impl %s for %s", trait.Name, d.StructName),
			"trait %s takes no type arguments", trait.Name)
		return false
	}
	c.errorfWithSuggestion(d.Token(), ErrSignatureMismatch,
		fmt.Sprintf("Say what %s is for %s: impl %s<...> for %s", params, d.StructName, trait.Name, d.StructName),
		"trait %s takes %d type arguments (<%s>), got %d", trait.Name, len(trait.TypeParams), params, len(d.TraitArgs))
	return false
}

// traitSelf is the type of `self` in the methods a trait gives a body: a
// value of which only the trait is known.
func (c *Checker) traitSelf(d *ast.TraitDecl) *BehaviorType {
	self := NewBehavior(d.Name, nil)
	for _, name := range d.TypeParams {
		self.TypeArgs = append(self.TypeArgs, c.resolveType(&ast.Type{Kind: ast.TypeBasic, Name: name}))
	}
	return self
}

// operatorOn types an operator on values that are not numbers or texts:
// the type of the result when the left side implements the trait behind
// the operator, nil when it does not.
func (c *Checker) operatorOn(e *ast.BinaryExpr, left, right Type) Type {
	op, ok := Operators[e.Op]
	if !ok || typeKeyOrParam(left) == "" {
		return nil
	}
	if _, implemented := c.traitArgsOf(left, op.Trait); !implemented {
		return nil
	}
	if !c.assignable(left, right) {
		c.errorfWithSuggestion(e.Right.Token(), ErrTypeMismatch,
			fmt.Sprintf("Both sides of %s have to be of the same type", e.Op),
			"cannot use %s on %s and %s", e.Op, left, right)
		return Unknown
	}
	c.info.OperatorMethods[e] = op.Method
	switch op.Trait {
	case TraitEqual, TraitOrdered:
		return Bool
	}
	return left
}

// typeKeyOrParam is typeKey, with a type parameter and a trait counting
// as something that can implement a trait.
func typeKeyOrParam(t Type) string {
	switch t := t.(type) {
	case *TypeParam:
		return t.Name
	case *BehaviorType:
		return t.Name
	case *PrimitiveType:
		return "" // Operators on numbers and texts are the machine's own
	case *ArrayType:
		return "" // And so are brackets and loops on arrays
	}
	return typeKey(t)
}

// missingOperator explains what would make an operator work on a struct.
func missingOperator(op string, left Type) string {
	method, ok := Operators[op]
	if !ok || typeKeyOrParam(left) == "" {
		return ""
	}
	trait := method.Trait[strings.LastIndex(method.Trait, ".")+1:]
	if param, isParam := left.(*TypeParam); isParam {
		return fmt.Sprintf("Require it of the type parameter: <%s: %s>, with: import std.ops.{%s};", param.Name, trait, trait)
	}
	return fmt.Sprintf("Implement the trait %s for %s (method '%s'), with: import std.ops.{%s};", trait, describe(left), method.Method, trait)
}

// indexWrite types the target of `a[k] = v` when a is a struct that says
// what assigning to its brackets does. It returns the type a value has to
// be of.
func (c *Checker) indexWrite(e *ast.BinaryExpr) (Type, bool) {
	target, ok := e.Left.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	recv := c.checkExpr(target.Expr)
	if recv == nil || typeKeyOrParam(recv) == "" {
		return nil, false
	}
	args, ok := c.traitArgsOf(recv, TraitIndexedSet)
	if !ok || len(args) != 2 {
		if _, readable := c.traitArgsOf(recv, TraitIndexed); readable {
			c.errorfWithSuggestion(target.Token(), ErrInvalidOperation,
				fmt.Sprintf("Call the method that changes it, or implement IndexedSet<Key, Value> for %s (method 'put')", describe(recv)),
				"what is in the brackets of %s can be read, not assigned", recv)
			c.checkExpr(target.Index)
			return Unknown, true
		}
		return nil, false
	}
	key := c.checkExprAs(target.Index, args[0])
	if key != nil && !c.assignable(args[0], key) {
		c.errorf(target.Index.Token(), ErrTypeMismatch, "%s is indexed by %s, got %s", recv, args[0], key)
	}
	c.info.IndexWrites[e] = true
	c.info.Types[target] = args[1]
	return args[1], true
}

// checkTypeBounds checks a type written with type arguments, Box<Point>,
// against what the struct or enum requires of them. Each place a type is
// written is checked once, however often it is resolved.
func (c *Checker) checkTypeBounds(n *ast.Type, args []Type) {
	if len(args) == 0 || c.boundsChecked[n] {
		return
	}
	var names []string
	var bounds map[string][]*ast.Type
	what := ""
	if decl := c.structs[n.Name]; decl != nil {
		names, bounds, what = decl.TypeParams, decl.Bounds, "struct "+decl.Name
	} else if decl := c.enums[n.Name]; decl != nil {
		names, bounds, what = decl.TypeParams, decl.Bounds, "enum "+decl.Name
	}
	if len(bounds) == 0 {
		return
	}
	c.boundsChecked[n] = true
	c.requireBounds(names, bounds, args, n.Token(), what)
}

// checkCallBounds checks the type arguments of a call of a generic
// function, written or worked out, against what the function requires.
func (c *Checker) checkCallBounds(e *ast.CallExpr) {
	ident, ok := e.Func.(*ast.Ident)
	if !ok {
		return
	}
	sym := c.table.Defs[ident]
	if sym == nil {
		return
	}
	fn, ok := sym.Decl.(*ast.FnDecl)
	if !ok || len(fn.Bounds) == 0 || len(ident.TypeArgs) != len(fn.TypeParams) {
		return
	}
	args := make([]Type, len(ident.TypeArgs))
	for i, arg := range ident.TypeArgs {
		args[i] = c.resolveType(arg)
	}
	c.requireBounds(fn.TypeParams, fn.Bounds, args, ident.Token(), "function '"+displayName(fn.Name)+"'")
}

// displayName is a name as the program wrote it, without the module it
// was given when the modules were put together.
func displayName(name string) string {
	return name[strings.LastIndex(name, ".")+1:]
}

// methodValue types a method used as a value, `users.get` without a call:
// the function that calls the method on that value. The value has to be
// something that can be read again whenever the function is called, a
// variable or a field of one, since that is what the function does.
func (c *Checker) methodValue(e *ast.MemberExpr, recv *StructType, decl *ast.FnDecl) Type {
	if len(decl.TypeParams) > 0 {
		c.errorfWithSuggestion(e.Token(), ErrUnknownField,
			fmt.Sprintf("Wrap the call in a function that says the types: fn(...) -> ... { return value.%s<...>(...); }", e.Name),
			"method '%s' has type parameters of its own, and cannot be used as a value without them", e.Name)
		return Unknown
	}
	if !isPlace(e.Expr) {
		c.errorfWithSuggestion(e.Expr.Token(), ErrUnknownField,
			fmt.Sprintf("Put the value in a variable first, then use variable.%s", e.Name),
			"a method can be used as a value on a variable or a field, not on the result of a computation")
		return Unknown
	}
	c.checkMethodVisible(recv, e)
	ft, ok := c.methodType(recv, e.Name, nil)
	if !ok {
		return Unknown
	}
	c.info.MethodValues[e] = ft
	return ft
}

// typeNamed returns the type an expression names, when it is the name of
// a struct, of an enum or of a builtin type that has methods: the Point of
// Point.norm.
func (c *Checker) typeNamed(expr ast.Expr) Type {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil
	}
	switch ident.Name {
	case "Int":
		return Int
	case "Float":
		return Float
	case "String":
		return String
	case "Bool":
		return Bool
	}
	sym := c.table.Defs[ident]
	if sym == nil || (sym.Kind != resolve.SymbolStruct && sym.Kind != resolve.SymbolEnum && sym.Kind != resolve.SymbolTrait) {
		// self, in a method, is a value
		return nil
	}
	switch decl := sym.Decl.(type) {
	case *ast.StructDecl:
		return c.resolveType(&ast.Type{BaseNode: ident.BaseNode, Kind: ast.TypeBasic, Name: decl.Name})
	case *ast.EnumDecl:
		return enumType(decl, nil)
	case *ast.TraitDecl:
		// Shape.area: whatever implements the trait
		return c.resolveType(&ast.Type{BaseNode: ident.BaseNode, Kind: ast.TypeBasic, Name: decl.Name})
	}
	return nil
}

// isTypeMethod reports whether an expression is `Type.method`: a method of
// a struct, a trait, a builtin type, or an enum that has no variant of
// that name.
func (c *Checker) isTypeMethod(member *ast.MemberExpr) bool {
	self := c.typeNamed(member.Expr)
	if self == nil {
		return false
	}
	if decl, _ := c.enumOf(member.Expr); decl != nil && findVariant(decl, member.Name) != nil {
		return false
	}
	return c.methodDecl(self, member.Name) != nil
}

// methodOfType types `Type.method`, the method as a function that takes
// the value first: Point.plus is a fn(Point, Point) -> Point, and
// names.map(String.upper) gives each name in capitals.
func (c *Checker) methodOfType(e *ast.MemberExpr, self Type) Type {
	decl := c.methodDecl(self, e.Name)
	if decl == nil {
		c.errorf(e.Token(), ErrUnknownMethod, "%s has no method '%s'", describe(self), e.Name)
		return Unknown
	}
	if st, ok := self.(*StructType); ok && len(st.TypeArgs) > 0 {
		c.errorfWithSuggestion(e.Token(), ErrUnknownMethod,
			fmt.Sprintf("Write the function, with the type: fn(value: %s<...>) -> ... { return value.%s(); }", displayName(st.Name), e.Name),
			"the methods of a generic type are used as values on a value of it, not on the type")
		return Unknown
	}
	if len(decl.TypeParams) > 0 {
		c.errorfWithSuggestion(e.Token(), ErrUnknownField,
			fmt.Sprintf("Wrap the call in a function that says the types: fn(...) -> ... { return value.%s<...>(...); }", e.Name),
			"method '%s' has type parameters of its own, and cannot be used as a value without them", e.Name)
		return Unknown
	}
	c.checkMethodVisible(self, e)
	ft, ok := c.methodType(self, e.Name, nil)
	if !ok {
		return Unknown
	}
	c.info.TypeMethods[e] = ft
	return &FunctionType{Params: append([]Type{self}, ft.Params...), ReturnType: ft.ReturnType}
}

// isPlace reports whether an expression is a variable or a path of
// fields from one: something with no effect and no cost to read again.
func isPlace(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return true
	case *ast.MemberExpr:
		return isPlace(e.Expr)
	}
	return false
}

// TypeToAST writes a type the way it would appear in source, for a stage
// that has to write code from what the checker found; nil for a type
// that has no source form.
func TypeToAST(t Type, at ast.BaseNode) *ast.Type {
	return typeToAST(t, at)
}
