package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/modules"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// collectDecls indexes structs, traits and methods before any body is
// checked, so a method can be called above the impl block that defines it.
func (c *Checker) collectDecls() {
	for _, decl := range c.prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			c.structs[d.Name] = d
		case *ast.EnumDecl:
			c.enums[d.Name] = d
		case *ast.TraitDecl:
			c.traits[d.Name] = d
		}
	}

	for _, decl := range c.prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		if impl.TraitName != "" {
			c.registry.Register(impl.StructName, impl.TraitName)
			if c.traitImpls[impl.StructName] == nil {
				c.traitImpls[impl.StructName] = make(map[string]*ast.ImplDecl)
			}
			if _, twice := c.traitImpls[impl.StructName][impl.TraitName]; twice {
				c.errorfWithSuggestion(impl.Token(), ErrDuplicateMethod, "A type implements a trait once, with one choice of type arguments",
					"%s already implements %s", impl.StructName, impl.TraitName)
			} else {
				c.traitImpls[impl.StructName][impl.TraitName] = impl
			}
		}
		if c.methods[impl.StructName] == nil {
			c.methods[impl.StructName] = make(map[string]*ast.FnDecl)
		}
		for _, m := range impl.Methods {
			if _, dup := c.methods[impl.StructName][m.Name]; dup {
				c.errorf(m.Token(), ErrDuplicateMethod, "method '%s' is already defined for %s", m.Name, impl.StructName)
				continue
			}
			c.methods[impl.StructName][m.Name] = m
			c.impls[m] = impl
		}
	}
}

// withSelf runs fn with `Self` standing for the given type.
func (c *Checker) withSelf(self Type, fn func()) {
	prev := c.selfType
	c.selfType = self
	defer func() { c.selfType = prev }()
	fn()
}

// bindStructParams brings the type parameters of a generic struct into
// scope, bound to the type arguments of one use of it. It returns a function
// that restores the previous scope.
func (c *Checker) bindStructParams(decl *ast.StructDecl, args []Type) func() {
	bindings := make(map[string]Type, len(decl.TypeParams))
	for i, name := range decl.TypeParams {
		if i < len(args) {
			bindings[name] = args[i]
		} else {
			bindings[name] = Unknown
		}
	}
	return c.bind(bindings)
}

// bind adds type parameter bindings and returns a function that removes
// them again. A nil binding marks a parameter that is in scope but not yet
// bound to a concrete type.
func (c *Checker) bind(bindings map[string]Type) func() {
	type saved struct {
		typ   Type
		found bool
	}
	previous := make(map[string]saved, len(bindings))
	for name, typ := range bindings {
		old, found := c.typeParams[name]
		previous[name] = saved{old, found}
		c.typeParams[name] = typ
	}
	return func() {
		for name, old := range previous {
			if old.found {
				c.typeParams[name] = old.typ
			} else {
				delete(c.typeParams, name)
			}
		}
	}
}

// fieldType returns the type of a struct field.
func (c *Checker) fieldType(recv *StructType, name string) (Type, bool) {
	decl := c.structs[recv.Name]
	if decl == nil {
		return Unknown, true // Monomorphized or unknown struct: nothing to check against
	}
	field := findField(decl, name)
	if field == nil {
		return nil, false
	}

	defer c.bindStructParams(decl, recv.TypeArgs)()
	return c.resolveType(field.Type), true
}

// methodDecl finds the declaration of a method of a struct, or of a
// method a trait requires.
func (c *Checker) methodDecl(recv Type, name string) *ast.FnDecl {
	decl, _ := c.methodOn(recv, name)
	return decl
}

// checkMethodVisible reports a use of a method from a module that may not
// see it. A method is visible in the module that declares it; elsewhere it
// has to be declared with 'pub', or implement a trait.
func (c *Checker) checkMethodVisible(recv Type, member *ast.MemberExpr) {
	decl := c.methodDecl(recv, member.Name)
	if decl == nil || decl.Pub || c.currentFn == nil || c.currentFn.Module == decl.Module {
		return
	}
	c.errorfWithSuggestion(member.Token(), ErrPrivateMethod,
		"Declare it with 'pub fn "+member.Name+"' to use it from other modules",
		"method '%s' of %s is private", member.Name, recv)
}

// methodType returns the signature of a method as seen on a receiver of the
// given type: a struct with an impl method of that name, or a trait that
// declares it. `Self` in the signature is the receiver's type, the type
// parameters of the impl are the receiver's type arguments, and the
// method's own type parameters are the given type arguments.
func (c *Checker) methodType(recv Type, name string, typeArgs []Type) (*FunctionType, bool) {
	decl, traitArgs := c.methodOn(recv, name)
	if decl == nil {
		return nil, false
	}

	// In `trait Source<T>`, T is what the receiver gives the trait
	bindings := make(map[string]Type)
	for param, arg := range traitArgs {
		bindings[param] = arg
	}

	// In `impl<T> Box<T>`, T is whatever the receiver has in that position
	if impl := c.impls[decl]; impl != nil {
		args := implTypeArgs(recv)
		for i, param := range impl.TypeParams {
			bindings[param] = Unknown
			if i < len(args) {
				bindings[param] = args[i]
			}
		}
	}

	// In `fn map<U>(...)`, U comes from the call: stack.map<String>(...)
	for i, param := range decl.TypeParams {
		bindings[param] = Unknown
		if i < len(typeArgs) {
			bindings[param] = typeArgs[i]
		}
	}
	defer c.bind(bindings)()

	var ft *FunctionType
	c.withSelf(recv, func() { ft, _ = c.resolveFnType(decl).(*FunctionType) })
	return ft, ft != nil
}

// methodTypeArgs resolves the type arguments written at a method call and
// checks that the method takes that many.
func (c *Checker) methodTypeArgs(recv Type, member *ast.MemberExpr, e *ast.CallExpr, expected Type) []Type {
	c.inferMethodTypeArgs(recv, member, e, expected)

	var args []Type
	for _, arg := range member.TypeArgs {
		args = append(args, c.resolveType(arg))
	}

	decl := c.methodDecl(recv, member.Name)
	if decl == nil || len(args) == len(decl.TypeParams) {
		return args
	}

	params := strings.Join(decl.TypeParams, ", ")
	switch {
	case len(decl.TypeParams) == 0:
		c.errorf(member.Token(), ErrMethodTypeArgs, "method '%s' takes no type arguments", member.Name)
	case len(args) == 0 && hasShortFn(e.Args):
		// items.map((a, b) -> ...): what is wrong is in the function
		// written there, and is said when it is checked as an argument
	case len(args) == 0:
		c.errorfWithSuggestion(member.Token(), ErrMethodTypeArgs,
			fmt.Sprintf("Write the types for <%s>: %s<...>(...)", params, member.Name),
			"cannot infer the type arguments of method '%s'", member.Name)
	default:
		c.errorf(member.Token(), ErrMethodTypeArgs, "method '%s' takes %d type arguments (<%s>), got %d",
			member.Name, len(decl.TypeParams), params, len(args))
	}
	return nil // Unbound parameters become unknown, avoiding follow-up errors
}

// checkMethodCall types `receiver.name(args)`: a method of the receiver's
// struct or trait, or a call of a function stored in a field.
func (c *Checker) checkMethodCall(e *ast.CallExpr, member *ast.MemberExpr, expected Type) Type {
	recv := c.checkExpr(member.Expr)

	fail := func(format string, args ...interface{}) Type {
		c.errorf(member.Token(), ErrUnknownMethod, format, args...)
		c.checkArgsOnly(e)
		return Unknown
	}

	switch t := recv.(type) {
	case nil:
		c.checkArgsOnly(e)
		return Unknown

	case *StructType:
		typeArgs := c.methodTypeArgs(t, member, e, expected)
		if ft, ok := c.methodType(t, member.Name, typeArgs); ok {
			c.checkMethodVisible(t, member)
			c.checkMethodBounds(t, member, typeArgs)
			c.info.Types[member] = ft
			return c.checkArgs(e, ft)
		}
		if c.structs[t.Name] == nil {
			break // Unknown struct, reported where it was named
		}
		if fieldType, ok := c.fieldType(t, member.Name); ok {
			// A field that holds a function
			c.info.Types[member] = fieldType
			return c.callValue(e, fieldType)
		}
		if c.libraryFunction(ast.StaticName(t.Name, member.Name)) != nil {
			// A function of the type, which takes no value
			c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
				fmt.Sprintf("Call it on the type: %s.%s(...)", displayName(t.Name), member.Name),
				"%s is a static function of %s, which takes no value of it", member.Name, displayName(t.Name))
			c.checkArgsOnly(e)
			return Unknown
		}
		if similar := c.similarMember(t, member.Name); similar != "" {
			c.errorfWithSuggestion(member.Token(), ErrUnknownMethod, "Did you mean '"+similar+"'?",
				"%s has no method '%s'", t, member.Name)
			c.checkArgsOnly(e)
			return Unknown
		}
		return fail("%s has no method '%s'", t, member.Name)

	case *BehaviorType:
		if ft, ok := c.methodType(t, member.Name, c.methodTypeArgs(t, member, e, expected)); ok {
			if decl := c.methodDecl(t, member.Name); decl != nil && takesSelf(decl) && !isSelf(member.Expr) {
				// Self is the receiver's own type, which a value known by
				// its trait does not say: any other implementor would pass
				c.errorfWithSuggestion(member.Token(), ErrSelfOfATrait,
					fmt.Sprintf("Call it where the type is known, or on a type parameter: fn f<T: %s>(a: T, b: T)", displayName(t.Name)),
					"'%s' takes a %s of the receiver's own type (Self), which a value known as a %s does not say", member.Name, displayName(t.Name), displayName(t.Name))
				c.checkArgsOnly(e)
				return Unknown
			}
			c.info.Types[member] = ft
			return c.checkArgs(e, ft)
		}
		return fail("trait %s has no method '%s'", t, member.Name)

	case *UnionType:
		if ft, ok := c.methodType(t, member.Name, c.methodTypeArgs(t, member, e, expected)); ok && hasNull(t) {
			// A method every optional has: impl<T> T? { ... }
			c.checkMethodVisible(t, member)
			c.info.Types[member] = ft
			return c.checkArgs(e, ft)
		}
		if hasNull(t) {
			c.errorfWithSuggestion(member.Token(), ErrPossiblyNull,
				fmt.Sprintf("Test it against null first, call it only when there is a value with value?.%s(...), or say there always is one with value!.%s(...)", member.Name, member.Name),
				"cannot call '%s' on %s: the value may be null", member.Name, t)
			c.checkArgsOnly(e)
			return Unknown
		}

	case *PrimitiveType:
		if isUnknown(t) {
			c.checkArgsOnly(e)
			return Unknown
		}
		// A method an impl gives numbers or texts
		if ft, ok := c.methodType(t, member.Name, c.methodTypeArgs(t, member, e, expected)); ok {
			c.checkMethodVisible(t, member)
			c.info.Types[member] = ft
			return c.checkArgs(e, ft)
		}

	case *ArrayType:
		// A method an impl gives arrays: impl<T> [T] { ... }
		if ft, ok := c.methodType(t, member.Name, c.methodTypeArgs(t, member, e, expected)); ok {
			c.checkMethodVisible(t, member)
			c.info.Types[member] = ft
			if member.Name == "sorted" && len(e.Args) == 0 && len(ft.Params) == 1 && len(ft.Defaults) == 0 {
				// items.sorted() of what has no order of its own: numbers
				// and texts have one, other items are told which comes first
				c.errorfWithSuggestion(e.Token(), ErrArgCount,
					fmt.Sprintf("Say which comes first: ops.sorted(items) when %s is Ordered, items.sortedBy(item -> item.rank), or items.sorted((a, b) -> ...)", describe(t.ElementType)),
					"sorted takes the function that says which of two items comes first: only numbers and texts have an order of their own")
				return t
			}
			return c.checkArgs(e, ft)
		}
		if owners := c.arrayImplsWith(member.Name); len(owners) > 0 && !isUnknown(t.ElementType) {
			// [Float].sum() where only [Int] has one: say whose it is
			c.checkArgsOnly(e)
			c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
				fmt.Sprintf("Only %s %s it; give %s one of your own: impl %s { fn %s(...) { ... } }",
					strings.Join(owners, " and "), haveOrHas(len(owners)), describe(t), describe(t), member.Name),
				"%s has no method '%s'", t, member.Name)
			return Unknown
		}
		return c.noBuiltinMethod(e, member, "an array", ast.ArrayImpl, "len", "std.arrays")

	case *TypeParam:
		// What is required of the type parameter says what can be called
		if ft, ok := c.methodType(t, member.Name, c.methodTypeArgs(t, member, e, expected)); ok {
			c.info.Types[member] = ft
			return c.checkArgs(e, ft)
		}
		c.checkArgsOnly(e)
		if len(c.boundsOf(t)) == 0 {
			c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
				fmt.Sprintf("Require a trait that has the method where %s is introduced: <%s: Trait>", t.Name, t.Name),
				"nothing is known of a %s: it has no method '%s'", t.Name, member.Name)
			return Unknown
		}
		var traits []string
		for _, bound := range c.boundsOf(t) {
			// As the program writes it: Show, not std.ops.Show
			traits = append(traits, displayName(bound.Name)+strings.TrimPrefix(describe(bound), bound.Name))
		}
		c.errorf(member.Token(), ErrUnknownMethod, "%s is only known to implement %s, which has no method '%s'",
			t.Name, strings.Join(traits, " and "), member.Name)
		return Unknown
	}

	if recv.Equals(String) {
		return c.noBuiltinMethod(e, member, "a text", "String", "trim", "std.strings")
	}
	if recv.Equals(Int) || recv.Equals(Float) {
		return c.noBuiltinMethod(e, member, "a number", typeKey(recv), "abs", "std.math")
	}
	return fail("type %s has no method '%s'", recv, member.Name)
}

// noBuiltinMethod reports a method an array, a text or a number does not
// have, with the one of the library it most likely is, or how to write it.
// The library's come with a module that a program has only when it calls
// one of them, so a misspelt one is compared with the module's source.
// durationMethods are the methods std.time gives numbers: 2.days().
var durationMethods = map[string]bool{"milliseconds": true, "seconds": true, "minutes": true, "hours": true, "days": true, "weeks": true}

func (c *Checker) noBuiltinMethod(e *ast.CallExpr, member *ast.MemberExpr, what, key, known, module string) Type {
	c.checkArgsOnly(e)
	if (key == "Int" || key == "Float") && durationMethods[member.Name] {
		// 2.days() without std.time
		c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
			fmt.Sprintf("'%s' is a method std.time gives numbers, for a Duration: import std.time", member.Name),
			"%s has no method '%s'", what, member.Name)
		return Unknown
	}
	var candidates []string
	if c.methods[key][known] == nil {
		// The program calls none of the module's methods, so it does not
		// have them: compare with what the module would have given
		candidates = modules.LibraryMethods(module)
		if len(candidates) == 0 {
			c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
				fmt.Sprintf("The methods of %s come with %s, which the library of this program does not have", what, module),
				"%s has no method '%s'", what, member.Name)
			return Unknown
		}
	}
	for method := range c.methods[key] {
		candidates = append(candidates, method)
	}
	// As for a function of a module, which these methods were (lenght is
	// len), but no further than half the name: a short one is near too
	// many others, and ["a"].sum() did not mean map
	similar := resolve.ClosestName(member.Name, candidates)
	if similar != "" && resolve.Distance(member.Name, similar) <= len(member.Name)/2 {
		c.errorfWithSuggestion(member.Token(), ErrUnknownMethod, "Did you mean '"+similar+"'?",
			"%s has no method '%s'", what, member.Name)
		return Unknown
	}
	impl := "impl " + key
	if key == ast.ArrayImpl {
		impl = "impl<T> [T]"
	}
	c.errorfWithSuggestion(member.Token(), ErrUnknownMethod,
		fmt.Sprintf("Give %s a method of your own: %s { fn %s(...) { ... } }", what, impl, member.Name),
		"%s has no method '%s'", what, member.Name)
	return Unknown
}

// arrayImplsWith names the arrays of one type that have a method, as
// written: [Int], [Float].
func (c *Checker) arrayImplsWith(name string) []string {
	var owners []string
	for owner, methods := range c.methods {
		if item, arrays := ast.IsArrayImpl(owner); arrays && item != "" && methods[name] != nil {
			owners = append(owners, "["+displayName(item)+"]")
		}
	}
	sort.Strings(owners)
	return owners
}

func haveOrHas(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// hasShortFn reports whether one of the arguments of a call is a function
// written with `->`.
func hasShortFn(args []ast.Expr) bool {
	for _, arg := range args {
		if fn, ok := arg.(*ast.FnExpr); ok && fn.Short {
			return true
		}
	}
	return false
}

// similarMember suggests a field or method of a struct close to name.
func (c *Checker) similarMember(recv *StructType, name string) string {
	var candidates []string
	for method := range c.methods[recv.Name] {
		candidates = append(candidates, method)
	}
	if decl := c.structs[recv.Name]; decl != nil {
		for _, field := range decl.Fields {
			candidates = append(candidates, field.Name)
		}
	}
	sort.Strings(candidates)

	for _, candidate := range candidates {
		if strings.EqualFold(candidate, name) || editDistanceAtMostOne(candidate, name) {
			return candidate
		}
	}
	return ""
}

// editDistanceAtMostOne reports whether b is a by one inserted, deleted or
// replaced character, or two neighbours swapped: nmae is name.
func editDistanceAtMostOne(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return false
	}
	i := 0
	for i < len(a) && a[i] == b[i] {
		i++
	}
	if i == len(a) {
		return true
	}
	if len(a) == len(b) {
		swapped := i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
		return swapped || a[i+1:] == b[i+1:]
	}
	return a[i:] == b[i+1:]
}

// callValue types a call of a value that should be a function.
func (c *Checker) callValue(e *ast.CallExpr, callee Type) Type {
	if callee == nil || isUnknown(callee) {
		c.checkArgsOnly(e)
		return Unknown
	}

	ft, ok := callee.(*FunctionType)
	if !ok {
		c.errorf(e.Func.Token(), ErrNotFunction, "cannot call non-function type %s", callee)
		c.checkArgsOnly(e)
		return Unknown
	}
	return c.checkArgs(e, ft)
}

// checkArgsOnly checks the argument expressions of a call whose callee
// could not be typed, so mistakes inside them are still reported.
func (c *Checker) checkArgsOnly(e *ast.CallExpr) {
	for _, arg := range e.Args {
		if fn, ok := arg.(*ast.FnExpr); ok && !shortFnIsTyped(fn) {
			// x -> x == k given to what does not exist: nothing can say
			// what x is, and the call is what is wrong
			continue
		}
		c.checkExpr(arg)
	}
}

// checkArgs checks the arguments of a call against a signature and returns
// the call's type.
func (c *Checker) checkArgs(e *ast.CallExpr, ft *FunctionType) Type {
	// Check argument count; the last parameters may have values of their
	// own, which the call is given in their place
	required := len(ft.Params) - len(ft.Defaults)
	if len(e.Args) < len(ft.Params) && len(e.Args) >= required && len(ft.Defaults) > 0 {
		c.info.Defaults[e] = ft.Defaults[len(e.Args)-required:]
	} else if len(e.Args) != len(ft.Params) {
		if !ft.IsVariadic || len(e.Args) < len(ft.Params)-1 {
			if len(ft.Defaults) > 0 {
				c.errorf(e.Token(), ErrArgCount, "expected %d to %d arguments, got %d", required, len(ft.Params), len(e.Args))
			} else {
				c.errorf(e.Token(), ErrArgCount, "expected %d arguments, got %d", len(ft.Params), len(e.Args))
			}
		}
	}

	// Check argument types
	for i, arg := range e.Args {
		var paramType Type
		if ft.IsVariadic && i >= len(ft.Params)-1 {
			paramType = ft.Params[len(ft.Params)-1]
		} else if i < len(ft.Params) {
			paramType = ft.Params[i]
		} else {
			// Extra arguments for non-variadic function (already reported error)
			c.checkExpr(arg)
			continue
		}

		if asTemplate(&e.Args[i], paramType) {
			arg = e.Args[i]
		}

		argType := c.checkExprAs(arg, paramType)
		if argType == nil {
			continue
		}

		if !c.assignable(paramType, argType) {
			if c.refusedAsTemplate(arg, paramType, argType, ErrArgType) {
				continue
			}
			c.errorfWithSuggestion(arg.Token(), ErrArgType, c.arrayAdvice(paramType, argType, "Ensure the argument is of type "+describe(paramType)), "cannot use type %s as argument of type %s", argType, paramType)
		}
	}

	return ft.ReturnType
}

// checkExprAs checks an expression in a place where a type is expected.
// An array literal then takes its element type from that place instead of
// from its first element, so `let shapes: [Shape] = [rect, square];` and
// `let names: [String] = [];` have the declared type. A call of a generic
// function takes from it the type arguments it could not infer otherwise.
func (c *Checker) checkExprAs(expr ast.Expr, expected Type) Type {
	return c.widen(expr, expected, c.checkExprExpecting(expr, expected))
}

// widen lets a whole number stand where a Float is expected: an argument,
// a variable or a field declared Float, a value returned as one. The
// expression is marked, for the desugarer to make its value a Float; from
// here on it is one.
func (c *Checker) widen(expr ast.Expr, expected, typ Type) Type {
	if expr == nil || typ == nil || expected == nil || !typ.Equals(Int) {
		return typ
	}
	wanted := withoutNull(expected)
	if wanted == nil || !wanted.Equals(Float) {
		return typ
	}
	if node, ok := expr.(ast.Widenable); ok {
		node.MarkWidened(true)
		c.info.Types[expr] = Float
		return Float
	}
	return typ
}

// checkExprExpecting is checkExprAs before a whole number is taken for a
// Float.
func (c *Checker) checkExprExpecting(expr ast.Expr, expected Type) Type {
	if lit, ok := expr.(*ast.StructInitExpr); ok && len(lit.TypeArgs) == 0 && expected != nil {
		// Box { v: 1 } where a Box<Int?> is expected: of what is expected
		if want, ok := withoutNull(expected).(*StructType); ok && want.Name == lit.Name && len(want.TypeArgs) > 0 {
			if c.literalArgs == nil {
				c.literalArgs = map[*ast.StructInitExpr][]Type{}
			}
			c.literalArgs[lit] = want.TypeArgs
		}
	}
	if fn, ok := expr.(*ast.FnExpr); ok && fn.Short {
		// item -> item.name takes its types from what is expected here
		typ := c.checkShortFn(fn, expected)
		if typ != nil {
			c.info.Types[expr] = typ
		}
		return typ
	}
	if cond, ok := expr.(*ast.CondExpr); ok {
		// Each branch is checked as the value expected here
		typ := c.checkCondExprAs(cond, expected)
		if typ != nil {
			c.info.Types[expr] = typ
		}
		return typ
	}
	if block, ok := expr.(*ast.BlockExpr); ok && block.Value != nil {
		state := c.flowSnapshot()
		defer c.flowRestore(state)
		for _, stmt := range block.Stmts {
			c.checkStmt(stmt)
		}
		typ := c.checkExprAs(block.Value, expected)
		if typ != nil {
			c.info.Types[expr] = typ
		}
		return typ
	}
	if match, ok := expr.(*ast.MatchExpr); ok {
		// Each case is checked as the value expected here
		typ := c.checkMatch(match.Match, true, expected)
		if typ != nil {
			c.info.Types[expr] = typ
		}
		return typ
	}
	if member := variantToFind(expr); member != nil {
		// Empty, with Shape.Empty and Box.Empty in scope: the one of what
		// is expected here
		c.findEnum(member, expected)
	}
	if _, isCall := expr.(*ast.CallExpr); isCall || c.isVariantRef(expr) {
		// A generic call may take the type arguments its own arguments
		// leave open from here: let xs: [Int] = none();
		c.expected = expected
		typ := c.checkExpr(expr)
		c.expected = nil
		return typ
	}

	literal, isLiteral := expr.(*ast.ArrayLiteralExpr)
	want, isArray := expected.(*ArrayType)
	if !isLiteral || !isArray || isUnknown(want.ElementType) {
		return c.checkExpr(expr)
	}

	for i := range literal.Elements {
		asTemplate(&literal.Elements[i], want.ElementType)
		elem := literal.Elements[i]
		elemType := c.checkExprAs(elem, want.ElementType)
		if elemType != nil && !c.assignable(want.ElementType, elemType) {
			c.errorfWithSuggestion(elem.Token(), ErrTypeMismatch,
				"All array elements should be of type "+describe(want.ElementType),
				"array element has type %s, expected %s", elemType, want.ElementType)
		}
	}

	c.info.Types[expr] = want
	return want
}

// checkPush applies the one rule the signature of the builtin `push` cannot
// express: the value must fit the element type of the array.
func (c *Checker) checkPush(e *ast.CallExpr) {
	if len(e.Args) != 2 {
		return
	}
	arrType, valType := c.info.Types[e.Args[0]], c.info.Types[e.Args[1]]
	if arrType == nil || valType == nil || isUnknown(arrType) {
		return
	}

	arr, ok := arrType.(*ArrayType)
	if !ok {
		c.errorf(e.Args[0].Token(), ErrArgType, "push expects an array, got %s", arrType)
		return
	}
	valType = c.widen(e.Args[1], arr.ElementType, valType)
	if !c.assignable(arr.ElementType, valType) {
		c.errorf(e.Args[1].Token(), ErrArgType, "cannot push type %s onto %s", valType, arrType)
	}
}

// isBuiltinCall reports whether a call goes to the named host function.
func (c *Checker) isBuiltinCall(e *ast.CallExpr, name string) bool {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || ident.Name != name {
		return false
	}
	sym := c.table.Defs[ident]
	if sym == nil || sym.Kind != resolve.SymbolFunc {
		return false
	}
	decl, ok := sym.Decl.(*ast.FnDecl)
	return ok && decl.Body == nil && resolve.IsBuiltin(name)
}

// --- Assignability ---

// assignable reports whether a value of type source can be stored in a
// place of type target.
func (c *Checker) assignable(target, source Type) bool {
	if target == nil || source == nil || isUnknown(target) || isUnknown(source) {
		return true
	}
	if target.Equals(Any) {
		return true
	}

	// Structs are the same type when their type arguments agree too:
	// a Box<Int> is not a Box<String>
	if t, ok := target.(*StructType); ok {
		src, ok := source.(*StructType)
		if !ok || t.Name != src.Name {
			return false
		}
		if len(t.TypeArgs) != len(src.TypeArgs) {
			return len(t.TypeArgs) == 0 || len(src.TypeArgs) == 0
		}
		// The values of an enum cannot be changed once made, so one holds
		// whatever its type arguments allow: a Result<Lock, E> is a
		// Result<Lock?, E>. A struct's fields can be assigned, and its type
		// arguments have to be the same.
		decl, _ := c.enumOfType(t)
		for i := range t.TypeArgs {
			if decl != nil && c.assignable(t.TypeArgs[i], src.TypeArgs[i]) {
				continue
			}
			if !c.sameType(t.TypeArgs[i], src.TypeArgs[i]) {
				return false
			}
		}
		return true
	}

	if target.Equals(source) {
		return true
	}
	// `Self` outside of an impl (a trait's default method) is not bound yet
	if target.Equals(Self) || source.Equals(Self) {
		return true
	}

	// A union source fits when every alternative fits
	if src, ok := source.(*UnionType); ok {
		for _, member := range src.Types {
			if !c.assignable(target, member) {
				return false
			}
		}
		return true
	}

	switch t := target.(type) {
	case *UnionType:
		for _, member := range t.Types {
			if c.assignable(member, source) {
				return true
			}
		}

	case *ArrayType:
		// An array is changed in place, and through every name it has: an
		// [Int] seen as an [Int?] could be given a null that the [Int]
		// would then hold. So the element types are the same, except in a
		// cast, which checks every element when it runs.
		if src, ok := source.(*ArrayType); ok {
			if c.castingArrays {
				return c.assignable(t.ElementType, src.ElementType)
			}
			return c.sameType(t.ElementType, src.ElementType)
		}

	case *FunctionType:
		src, ok := source.(*FunctionType)
		if !ok || len(t.Params) != len(src.Params) || t.IsVariadic != src.IsVariadic {
			return false
		}
		// The function will be called with the target's parameter types,
		// so it must accept them (parameters go the other way round), and
		// what it returns must fit what the target promises.
		for i := range t.Params {
			if !c.assignable(src.Params[i], t.Params[i]) {
				return false
			}
		}
		return c.assignable(t.ReturnType, src.ReturnType)

	case *BehaviorType:
		// A value fits a trait its type implements
		return c.implements(source, t)
	}
	return false
}

// arrayAdvice is what to suggest when a value of type source does not fit
// target: otherwise, for an array whose elements would fit one by one,
// how to do without an array of the wider type.
func (c *Checker) arrayAdvice(target, source Type, otherwise string) string {
	if st, ok := withoutNullOrNil(target).(*StructType); ok && st.Name == "std.time.Duration" && (source.Equals(Int) || source.Equals(Float)) {
		// A number of milliseconds, as std.time once took: say what it is
		return "A number alone is not a length of time: write what it is, 500.milliseconds(), 30.seconds(), 2.days()"
	}
	t, ok := withoutNullOrNil(target).(*ArrayType)
	src, ok2 := source.(*ArrayType)
	if !ok || !ok2 || !c.assignable(t.ElementType, src.ElementType) {
		return otherwise
	}
	if _, isTrait := t.ElementType.(*BehaviorType); isTrait {
		return fmt.Sprintf("An array of %s could then be given another %s. Take any array of them: fn f<T: %s>(items: [T]), or make a new array: items.map(item -> item as %s)",
			describe(src.ElementType), describe(t.ElementType), describe(t.ElementType), describe(t.ElementType))
	}
	return fmt.Sprintf("An array of %s could then be given values of type %s, which it cannot hold. Make a new array: items.map(item -> item as %s)",
		describe(src.ElementType), describe(t.ElementType), describe(t.ElementType))
}

// checkMethodBounds checks a method call against what the impl and the
// method require of their type parameters: `impl<T: Ordered> Box<T>` has
// methods only a Box of something ordered has.
func (c *Checker) checkMethodBounds(recv *StructType, member *ast.MemberExpr, typeArgs []Type) {
	decl := c.methodDecl(recv, member.Name)
	if decl == nil {
		return
	}
	if impl := c.impls[decl]; impl != nil && len(recv.TypeArgs) == len(impl.TypeParams) {
		c.requireBounds(impl.TypeParams, impl.Bounds, recv.TypeArgs, member.Token(), "method '"+member.Name+"'")
	}
	c.requireBounds(decl.TypeParams, decl.Bounds, typeArgs, member.Token(), "method '"+member.Name+"'")
}

// sameType reports whether two types are interchangeable.
func (c *Checker) sameType(a, b Type) bool {
	return c.assignable(a, b) && c.assignable(b, a)
}

// checkCastExpr types `value as Type`. A cast is accepted when the value
// may be of that type: the type is more specific than the value's (out of
// Any, out of an optional or union, from a trait to a struct implementing
// it) or already fits. Whether it really is, is checked when it runs,
// including the elements of arrays and the fields of generic structs.
func (c *Checker) checkCastExpr(e *ast.CastExpr) Type {
	source := c.checkExpr(e.Expr)
	target := c.resolveType(e.Type)
	if param := c.unspecializedParam(e.Type); param != "" {
		c.errorfWithSuggestion(e.Type.Token(), ErrInvalidCast,
			"Cast in a function of its own, fn as<"+param+">(value: Any) -> "+param+" { return value as "+param+"; }, and call it from the method",
			"a cast to %s, a type parameter of a method, cannot be checked when the program runs: methods are not made again for each type they are used with", describe(target))
		return target
	}
	if source == nil || isUnknown(source) || isUnknown(target) {
		return target
	}

	c.castingArrays = true
	widens, narrows := c.assignable(target, source), c.assignable(source, target)
	c.castingArrays = false

	switch {
	case widens:
		// Always succeeds: nothing to check at runtime
	case narrows:
		descriptor, ok := c.castDescriptor(target)
		if !ok {
			c.errorf(e.Type.Token(), ErrInvalidCast, "a cast to %s cannot be checked", target)
			return target
		}
		c.info.Casts[e] = descriptor
	default:
		c.errorf(e.Token(), ErrInvalidCast, "cannot cast %s to %s: a value of that type can never be one", source, target)
	}
	return target
}

// checkTypeTest types `value is Type`, a question answered when the
// program runs. Inside a branch taken when it is true, the tested variable
// or field has that type (see flow.go).
func (c *Checker) checkTypeTest(e *ast.TypeTestExpr) Type {
	source := c.checkExpr(e.Expr)
	target := c.resolveType(e.Type)
	if param := c.unspecializedParam(e.Type); param != "" {
		c.errorfWithSuggestion(e.Type.Token(), ErrInvalidCast,
			"Test it in a function of its own, fn isA<"+param+">(value: Any) -> Bool { return value is "+param+"; }, and call it from the method",
			"whether a value is a %s, a type parameter of a method, cannot be told when the program runs: methods are not made again for each type they are used with", describe(target))
		return Bool
	}
	if isUnknown(target) {
		return Bool
	}

	if source != nil && !isUnknown(source) && !c.assignable(target, source) && !c.assignable(source, target) {
		c.errorf(e.Token(), ErrInvalidCast, "%s can never be %s", source, target)
		return Bool
	}

	descriptor, ok := c.castDescriptor(target)
	if !ok {
		c.errorf(e.Type.Token(), ErrInvalidCast, "a test for %s cannot be made", target)
		return Bool
	}
	c.info.Tests[e] = descriptor
	c.info.TestedTypes[e] = target
	return Bool
}

// castDescriptor encodes what a cast has to verify at runtime, in the form
// the VM's cast function understands:
//
//	int float bool string null   that kind of value
//	any                          anything
//	fn(D,D)->D                   a function usable with that signature
//	[D]                          an array whose elements all match D
//	?D                           null, or what D says
//	{A|B}                        a value of struct A or B
//	{A:f=D,g=D}                  a value of struct A whose fields f and g match
//
// A value does not record the type arguments of a generic struct, so a cast
// to Box<Int> is verified through the fields whose type depends on them:
// {Box:value=int}. A function type is compared with the signature recorded
// for the function value when it was compiled.
func (c *Checker) castDescriptor(target Type) (string, bool) {
	return c.describeCast(target, make(map[string]bool))
}

// describeCast builds a descriptor. visiting holds the generic struct types
// being described, so a struct that contains itself (a linked list) is
// verified one level deep instead of forever.
func (c *Checker) describeCast(target Type, visiting map[string]bool) (string, bool) {
	switch t := target.(type) {
	case *PrimitiveType:
		switch t.Name {
		case PrimitiveInt, PrimitiveFloat, PrimitiveBool, PrimitiveString, PrimitiveNull, PrimitiveAny:
			return string(t.Name), true
		case PrimitiveUnknown:
			return "any", true
		}

	case *TypeParam:
		// Not known until the generic function is used; nothing to verify
		return "any", true

	case *ArrayType:
		elem, ok := c.describeCast(t.ElementType, visiting)
		return "[" + elem + "]", ok

	case *FunctionType:
		return c.describeFunctionCast(t, visiting)

	case *StructType:
		if decl := c.enums[t.Name]; decl != nil {
			return "{" + strings.Join(describeEnumCast(decl), "|") + "}", true
		}
		return c.describeStructCast(t, visiting)

	case *BehaviorType:
		var names []string
		for name, decl := range c.enums {
			// A value of an enum carries the name of its variant
			if c.registry.Implements(name, t.Name) {
				names = append(names, describeEnumCast(decl)...)
			}
		}
		for name := range c.structs {
			if c.registry.Implements(name, t.Name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return "{" + strings.Join(names, "|") + "}", true

	case *UnionType:
		if value := withoutNull(t); hasNull(t) && value != nil {
			if _, nested := value.(*UnionType); !nested {
				inner, ok := c.describeCast(value, visiting)
				return "?" + inner, ok
			}
		}
	}
	return "", false
}

// describeFunctionCast describes a function type: fn(D,D)->D, with ...D
// for a variadic parameter and void for no result. The same form records
// the type of every compiled function, so that a cast to a function type
// can compare the two.
func (c *Checker) describeFunctionCast(t *FunctionType, visiting map[string]bool) (string, bool) {
	params := make([]string, len(t.Params))
	for i, param := range t.Params {
		descriptor, ok := c.describeCast(param, visiting)
		if !ok {
			return "", false
		}
		params[i] = descriptor
	}
	if t.IsVariadic && len(params) > 0 {
		params[len(params)-1] = "..." + params[len(params)-1]
	}

	result := "void"
	if t.ReturnType != nil && !t.ReturnType.Equals(Void) {
		descriptor, ok := c.describeCast(t.ReturnType, visiting)
		if !ok {
			return "", false
		}
		result = descriptor
	}
	return "fn(" + strings.Join(params, ",") + ")->" + result, true
}

// recordSignature stores the type of a function for the code generator,
// which attaches it to the compiled function.
func (c *Checker) recordSignature(node ast.Node, fn *ast.FnDecl) {
	ft, ok := c.resolveFnType(fn).(*FunctionType)
	if !ok {
		return
	}
	if descriptor, ok := c.castDescriptor(ft); ok {
		c.info.Signatures[node] = descriptor
	}
}

// describeStructCast describes a struct type. For a generic struct it adds
// the fields whose type involves a type parameter.
func (c *Checker) describeStructCast(t *StructType, visiting map[string]bool) (string, bool) {
	decl := c.structs[t.Name]
	if decl == nil && c.traits[t.Name] == nil {
		// Not the name of anything declared: the type parameter of a
		// generic struct, in one of its methods. A struct is compiled once
		// for every type it is used with, so what the parameter stands
		// for is not known there, and there is nothing to verify: as for
		// the parameter of a generic function before it is used.
		return "any", true
	}
	key := describe(t)
	if decl == nil || len(decl.TypeParams) == 0 || len(t.TypeArgs) == 0 || visiting[key] {
		return "{" + t.Name + "}", true
	}

	visiting[key] = true
	defer delete(visiting, key)

	var fields []string
	for _, field := range decl.Fields {
		if !mentionsAny(field.Type, decl.TypeParams) {
			continue
		}
		fieldType, _ := c.fieldType(t, field.Name)
		descriptor, ok := c.describeCast(fieldType, visiting)
		if !ok {
			return "", false
		}
		fields = append(fields, field.Name+"="+descriptor)
	}

	if len(fields) == 0 {
		return "{" + t.Name + "}", true
	}
	return "{" + t.Name + ":" + strings.Join(fields, ",") + "}", true
}

// mentionsAny reports whether a type annotation uses one of the names.
func mentionsAny(t *ast.Type, names []string) bool {
	if t == nil {
		return false
	}
	for _, name := range names {
		if t.Kind == ast.TypeBasic && t.Name == name {
			return true
		}
	}
	for _, sub := range append(append(append([]*ast.Type{t.ElemType, t.ReturnType}, t.Generics...), t.UnionTypes...), t.ParamTypes...) {
		if mentionsAny(sub, names) {
			return true
		}
	}
	return false
}

// comparable reports whether == and != make sense between two types.
func (c *Checker) comparable(left, right Type) bool {
	// Anything may be tested against null: a variable known to be non-null
	// at this point is often still tested by code written before it was
	if left.Equals(Null) || right.Equals(Null) {
		return true
	}
	if isNumber(left) && isNumber(right) {
		// 1 == 1.0: a whole number is compared as the Float it would be
		return true
	}
	return c.assignable(left, right) || c.assignable(right, left)
}

func isNumber(t Type) bool {
	return t != nil && (t.Equals(Int) || t.Equals(Float))
}

func isUnknown(t Type) bool {
	return t != nil && t.Equals(Unknown)
}

// hasNull reports whether a union admits null.
func hasNull(u *UnionType) bool {
	for _, member := range u.Types {
		if member.Equals(Null) {
			return true
		}
	}
	return false
}

// implTypeArgs is what the type parameters of an impl stand for when its
// method is called on a value of a type: those of the struct, in order;
// what an array holds, for `impl<T> [T]`; what an optional holds when it
// is not null, for `impl<T> T?`.
func implTypeArgs(recv Type) []Type {
	switch t := recv.(type) {
	case *StructType:
		return t.TypeArgs
	case *ArrayType:
		return []Type{t.ElementType}
	case *UnionType:
		if hasNull(t) {
			if value := withoutNull(t); value != nil {
				return []Type{value}
			}
		}
	}
	return nil
}

// optional returns the type T?, that is T | null.
func optional(t Type) Type {
	if t.Equals(Null) || isUnknown(t) || t.Equals(Any) {
		return t
	}
	if u, ok := t.(*UnionType); ok {
		if hasNull(u) {
			return u
		}
		return NewUnion(append(append([]Type{}, u.Types...), Null)...)
	}
	return NewUnion(t, Null)
}

// WithoutNull returns what remains of a type once null is ruled out, or
// nil if nothing does.
func WithoutNull(t Type) Type { return withoutNull(t) }

// IsOptional reports whether a type is that of a value that may be null,
// and something else when it is not: T?.
func IsOptional(t Type) bool {
	u, ok := t.(*UnionType)
	return ok && hasNull(u) && withoutNull(u) != nil
}

// withoutNull returns what remains of a type once null is ruled out, or nil
// if nothing does (the type was just null).
func withoutNull(t Type) Type {
	if t.Equals(Null) {
		return nil
	}
	u, ok := t.(*UnionType)
	if !ok {
		return t
	}

	var rest []Type
	for _, member := range u.Types {
		if !member.Equals(Null) {
			rest = append(rest, member)
		}
	}
	switch len(rest) {
	case 0:
		return nil
	case 1:
		return rest[0]
	}
	return NewUnion(rest...)
}

// describe renders a type the way it is written in source, for messages.
func describe(t Type) string {
	switch t := t.(type) {
	case *StructType:
		if len(t.TypeArgs) == 0 {
			return t.Name
		}
		args := make([]string, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			args[i] = describe(arg)
		}
		if ast.IsTupleName(t.BaseName) {
			// As it is written: (Int, String)
			return "(" + strings.Join(args, ", ") + ")"
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"

	case *BehaviorType:
		if len(t.TypeArgs) == 0 {
			return t.Name
		}
		args := make([]string, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			args[i] = describe(arg)
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"

	case *ArrayType:
		return "[" + describe(t.ElementType) + "]"

	case *UnionType:
		if rest := withoutNull(t); rest != nil && hasNull(t) {
			if _, nested := rest.(*UnionType); !nested {
				return describe(rest) + "?"
			}
		}
		parts := make([]string, len(t.Types))
		for i, member := range t.Types {
			parts[i] = describe(member)
		}
		return strings.Join(parts, " | ")

	case *FunctionType:
		params := make([]string, len(t.Params))
		for i, param := range t.Params {
			params[i] = describe(param)
		}
		if t.IsVariadic && len(params) > 0 {
			params[len(params)-1] = "..." + params[len(params)-1]
		}
		return "fn(" + strings.Join(params, ", ") + ") -> " + describe(t.ReturnType)

	case *PrimitiveType:
		// As the program writes it: String, not string
		switch t.Name {
		case PrimitiveInt, PrimitiveFloat, PrimitiveBool, PrimitiveString, PrimitiveVoid, PrimitiveAny:
			return strings.ToUpper(string(t.Name[:1])) + string(t.Name[1:])
		}
	}
	return t.String()
}

// takesSelf says whether a method takes a value of the receiver's own
// type, Self, or one made of it: [Self], Self?.
func takesSelf(decl *ast.FnDecl) bool {
	for _, param := range decl.Params {
		if mentionsSelf(param.Type) {
			return true
		}
	}
	return false
}

func mentionsSelf(t *ast.Type) bool {
	if t == nil {
		return false
	}
	if t.Name == "Self" {
		return true
	}
	for _, inner := range append(append(append([]*ast.Type{t.ElemType}, t.Generics...), t.UnionTypes...), t.ParamTypes...) {
		if mentionsSelf(inner) {
			return true
		}
	}
	return false
}

// isSelf says whether an expression is self: in a method of a trait, its
// Self is the type of the value it is called on, and of other values of
// Self there.
func isSelf(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "self"
}

// unspecializedParam is a type parameter of the method being checked, its
// own, that a written type names: a function generic of its own is made
// again for each type it is used with, and what it casts to is known
// then; a method is not, and its own type parameter is no type then.
func (c *Checker) unspecializedParam(t *ast.Type) string {
	if c.currentFn == nil || c.currentFn.Module == "std" || strings.HasPrefix(c.currentFn.Module, "std.") {
		// The library casts what it made itself, of the type it knows
		return ""
	}
	impl := c.impls[c.currentFn]
	if impl == nil {
		return ""
	}
	// The method's own: those of its impl, impl<T> Box<T>, are those of
	// the struct, which is made again for each type it is used with
	params := map[string]bool{}
	for _, p := range c.currentFn.TypeParams {
		params[p] = true
	}
	var found string
	var walk func(*ast.Type)
	walk = func(t *ast.Type) {
		if t == nil || found != "" {
			return
		}
		if params[t.Name] && len(t.Generics) == 0 {
			found = t.Name
			return
		}
		for _, inner := range append(append(append([]*ast.Type{t.ElemType}, t.Generics...), t.UnionTypes...), t.ParamTypes...) {
			walk(inner)
		}
	}
	walk(t)
	return found
}
