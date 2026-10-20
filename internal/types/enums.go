package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/resolve"
)

// Error codes for enums and constants
const (
	ErrNonExhaustiveMatch = "E2028" // match on an enum that leaves variants out
	ErrInvalidPattern     = "E2029" // Pattern that cannot match the value
	ErrInvalidEnumUse     = "E2030" // Unknown variant, wrong number of values, enum used as a value
	ErrConstantCycle      = "E2031" // Constant defined in terms of itself
	ErrNotConstant        = "E2032" // Value of a constant that is not made of constants
)

// An enum is a type whose values are one of several variants, each with
// values of its own:
//
//	enum Shape { Circle(Float), Rect(Float, Float), Empty }
//
// To the checker the type of a value of Shape is a StructType named Shape.
// That gives enums, without more code, what structs have: type arguments
// (Option<Int>), methods (impl Shape { ... }) and traits (impl Area for
// Shape). What differs is handled here: a value is made by naming a
// variant (Shape.Circle(2.0)) and taken apart by a match.

// enumOf returns the enum an expression names, if it is the bare name of
// one, as in the `Shape` of `Shape.Circle`.
func (c *Checker) enumOf(expr ast.Expr) (*ast.EnumDecl, *ast.Ident) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil, nil
	}
	sym := c.table.Defs[ident]
	if sym == nil || sym.Kind != resolve.SymbolEnum {
		return nil, nil
	}
	decl, _ := sym.Decl.(*ast.EnumDecl)
	return decl, ident
}

// variantToFind returns the `Enum.Variant` an expression is, or calls,
// when its enum is still to be found (see ast.EnumToFind).
func variantToFind(expr ast.Expr) *ast.MemberExpr {
	if call, ok := expr.(*ast.CallExpr); ok {
		expr = call.Func
	}
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return nil
	}
	if ident, ok := member.Expr.(*ast.Ident); ok && ident.Name == ast.EnumToFind {
		return member
	}
	return nil
}

// findEnum puts in place of ast.EnumToFind the enum a value of the type
// given is, when it is one the variant may be of, and reports whether it
// did.
func (c *Checker) findEnum(member *ast.MemberExpr, t Type) bool {
	marker, ok := member.Expr.(*ast.Ident)
	if !ok || marker.Name != ast.EnumToFind || t == nil {
		return false
	}
	decl, _ := c.enumOfType(withoutNullOrNil(t))
	if decl == nil {
		return false
	}
	for _, candidate := range marker.Candidates {
		if candidate == decl.Name {
			enum := &ast.Ident{BaseNode: marker.BaseNode, Name: decl.Name}
			c.table.Defs[enum] = &resolve.Symbol{Name: decl.Name, Kind: resolve.SymbolEnum, Decl: decl}
			member.Expr = enum
			return true
		}
	}
	return false
}

// reportAmbiguousVariant says that nothing tells which enum a variant
// written alone is of.
func (c *Checker) reportAmbiguousVariant(marker *ast.Ident, variant string) {
	if variant == "" {
		// The placeholder stands where the variant was written
		variant = marker.Token().Literal
	}
	var written []string
	for _, candidate := range marker.Candidates {
		written = append(written, displayName(candidate)+"."+variant)
	}
	c.errorfWithSuggestion(marker.Token(), ErrAmbiguousVariant, "Write which: "+strings.Join(written, " or "),
		"'%s' is a variant of more than one enum, and nothing here says which", variant)
}

// isVariantRef reports whether an expression is `Enum.Variant`.
func (c *Checker) isVariantRef(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	decl, _ := c.enumOf(member.Expr)
	return decl != nil
}

func findVariant(decl *ast.EnumDecl, name string) *ast.EnumVariant {
	for _, v := range decl.Variants {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// enumType is the type of the values of an enum.
func enumType(decl *ast.EnumDecl, typeArgs []Type) *StructType {
	return &StructType{Name: decl.Name, BaseName: decl.Name, TypeArgs: typeArgs}
}

// enumOfType returns the enum a type is, if it is one.
func (c *Checker) enumOfType(t Type) (*ast.EnumDecl, *StructType) {
	if t == nil {
		return nil, nil
	}
	st, ok := t.(*StructType)
	if !ok {
		return nil, nil
	}
	// A type made by filling in type parameters is named after its
	// arguments ("List[int]"); BaseName is the enum's own name
	decl := c.enums[st.Name]
	if decl == nil {
		decl = c.enums[st.BaseName]
	}
	if decl == nil {
		return nil, nil
	}
	return decl, st
}

// variantFields returns the types of the values of a variant, in an enum
// with the given type arguments. A type parameter without an argument is
// unknown.
func (c *Checker) variantFields(decl *ast.EnumDecl, variant *ast.EnumVariant, typeArgs []Type) []Type {
	bindings := make(map[string]Type, len(decl.TypeParams))
	for i, name := range decl.TypeParams {
		bindings[name] = Unknown
		if i < len(typeArgs) && typeArgs[i] != nil {
			bindings[name] = typeArgs[i]
		}
	}
	defer c.bind(bindings)()

	fields := make([]Type, len(variant.Values))
	for i, value := range variant.Values {
		fields[i] = c.resolveType(value)
	}
	return fields
}

// describeVariant writes a variant the way it is declared: Rect(Float, Float).
// The one variant of the enum behind tuples is named as what was written.
func describeVariant(decl *ast.EnumDecl, variant *ast.EnumVariant) string {
	if ast.IsTupleName(decl.Name) {
		return fmt.Sprintf("tuple of %d values", len(variant.Values))
	}
	return decl.Name + "." + variant.String()
}

func (c *Checker) checkEnumDecl(d *ast.EnumDecl) {
	defer c.bindTypeParams(d.TypeParams)()
	defer c.bindBounds(d.Bounds)()

	seen := make(map[string]bool)
	for _, variant := range d.Variants {
		if seen[variant.Name] {
			c.errorf(variant.Token(), ErrInvalidEnumUse, "enum %s already has a variant '%s'", d.Name, variant.Name)
		}
		seen[variant.Name] = true
		for _, value := range variant.Values {
			c.resolveType(value)
		}
		// Shape.area would be both the variant and the method
		if c.methods[d.Name][variant.Name] != nil {
			c.errorf(variant.Token(), ErrInvalidEnumUse, "'%s' is both a variant and a method of enum %s", variant.Name, d.Name)
		}
	}
}

// checkVariant types `Enum.Variant` and `Enum.Variant(args)`: the creation
// of a value of an enum, or, for a variant with values named without
// calling it, the function that creates one. call is the call, or nil.
func (c *Checker) checkVariant(member *ast.MemberExpr, call *ast.CallExpr, expected Type) Type {
	decl, ident := c.enumOf(member.Expr)
	called := call != nil
	var args []ast.Expr
	if called {
		args = call.Args
	}

	variant := findVariant(decl, member.Name)
	if variant == nil {
		var names []string
		for _, v := range decl.Variants {
			names = append(names, v.Name)
		}
		suggestion := "Its variants are " + strings.Join(names, ", ")
		if similar := resolve.ClosestName(member.Name, names); similar != "" {
			suggestion = "Did you mean '" + similar + "'?"
		}
		c.errorfWithSuggestion(member.Token(), ErrInvalidEnumUse, suggestion,
			"enum %s has no variant '%s'", decl.Name, member.Name)
		c.typesOf(args)
		return Unknown
	}

	// Type arguments written after the enum's name: Option<Int>.None
	found := make(map[string]Type)
	isParam := make(map[string]bool, len(decl.TypeParams))
	for _, name := range decl.TypeParams {
		isParam[name] = true
	}
	if len(ident.TypeArgs) > 0 {
		if len(ident.TypeArgs) != len(decl.TypeParams) {
			c.errorf(ident.Token(), ErrInvalidEnumUse, "enum %s takes %d type arguments, got %d",
				decl.Name, len(decl.TypeParams), len(ident.TypeArgs))
		}
		for i, arg := range ident.TypeArgs {
			if i < len(decl.TypeParams) {
				found[decl.TypeParams[i]] = c.resolveType(arg)
			}
		}
	}

	// The types of the values as declared, type parameters left open
	restore := c.bindTypeParams(decl.TypeParams)
	declared := make([]Type, len(variant.Values))
	for i, value := range variant.Values {
		declared[i] = c.resolveType(value)
	}
	restore()

	hint := c.hintFrom(decl, expected)
	var argTypes []Type
	if called {
		switch {
		case len(args) == 0 && len(variant.Values) == 1 && c.holdsNothing(declared[0], isParam, found, hint):
			// Result.Ok() of a Result<Void, E>: the value it holds is
			// nothing, which is null where the program runs, as what a
			// function of Void returns
			call.Args = []ast.Expr{&ast.Literal{BaseNode: member.BaseNode, Value: "null", Kind: "null"}}
			args = call.Args
		case len(variant.Values) == 0:
			c.errorfWithSuggestion(member.Token(), ErrInvalidEnumUse,
				fmt.Sprintf("Write %s.%s without parentheses", decl.Name, variant.Name),
				"%s.%s holds no values", decl.Name, variant.Name)
			c.typesOf(args)
			return enumType(decl, c.typeArgsFrom(decl, found, expected))
		case len(args) != len(variant.Values):
			c.errorfWithSuggestion(member.Token(), ErrInvalidEnumUse,
				"It is declared as "+describeVariant(decl, variant),
				"%s.%s holds %s, got %d", decl.Name, variant.Name, values(len(variant.Values)), len(args))
			c.typesOf(args)
			return enumType(decl, c.typeArgsFrom(decl, found, expected))
		}

		// What each argument is says what the type parameters are. Until
		// then, the type the whole value is expected to have says what
		// each argument is expected to be, so that the type arguments reach
		// values inside values: let o: Option<Result<Int, String>> =
		// Option.Some(Result.Ok(1));
		for i, arg := range args {
			argType := c.checkExprAs(arg, c.partial(declared[i], isParam, found, hint))
			if isNullLiteral(arg) && c.holdsNothing(declared[i], isParam, found, hint) {
				// Result.Ok(null), written so or for Result.Ok()
				argType = Void
			}
			argTypes = append(argTypes, argType)
			if argType != nil {
				c.unify(declared[i], argType, isParam, found)
			}
		}
	}

	typeArgs := c.typeArgsFrom(decl, found, expected)
	for i, arg := range typeArgs {
		if isUnknown(arg) && ast.IsTupleName(decl.Name) {
			c.errorfWithSuggestion(member.Token(), ErrInvalidEnumUse,
				"Say what the tuple is where it goes: let pair: (Int, String?) = (1, null);",
				"nothing here says what type value %d of this tuple has", i+1)
			break
		}
		if isUnknown(arg) {
			c.errorfWithSuggestion(member.Token(), ErrInvalidEnumUse,
				fmt.Sprintf("Write the type: %s<...>.%s", decl.Name, variant.Name),
				"nothing here says what %s of %s is", decl.TypeParams[i], decl.Name)
			break
		}
	}
	result := enumType(decl, typeArgs)
	fields := c.variantFields(decl, variant, typeArgs)

	if !called {
		if len(variant.Values) == 0 {
			return result
		}
		// The variant as a function: arrays.map(numbers, Shape.Circle)
		return NewFunction(fields, result)
	}

	for i, arg := range args {
		if argTypes[i] != nil && !c.assignable(fields[i], argTypes[i]) {
			c.errorf(arg.Token(), ErrArgType, "cannot use type %s as value %d of %s.%s, which is of type %s",
				argTypes[i], i+1, decl.Name, variant.Name, fields[i])
		}
	}
	return result
}

// holdsNothing reports whether a value of a variant is declared with a
// type parameter that is known, or expected, to be Void: the T of a
// Result<Void, E>.
func (c *Checker) holdsNothing(declared Type, isParam map[string]bool, found, hint map[string]Type) bool {
	param, ok := declared.(*TypeParam)
	if !ok || !isParam[param.Name] {
		return false
	}
	t, known := found[param.Name]
	if !known {
		t, known = hint[param.Name]
	}
	return known && t.Equals(Void)
}

func isNullLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.Literal)
	return ok && lit.Kind == "null"
}

// partial is a declared type with the type parameters known so far filled
// in, to be the expected type of an argument; nil when one is still open,
// since an expected type with a hole would only mislead.
func (c *Checker) partial(declared Type, isParam map[string]bool, found, hint map[string]Type) Type {
	open := false
	bindings := make(map[string]Type)
	for name := range isParam {
		if t, ok := found[name]; ok {
			bindings[name] = t
		} else if t, ok := hint[name]; ok {
			bindings[name] = t
		} else {
			open = true
		}
	}
	if open && mentionsParam(declared, isParam) {
		return nil
	}
	return Instantiate(declared, bindings)
}

func mentionsParam(t Type, isParam map[string]bool) bool {
	switch t := t.(type) {
	case *TypeParam:
		return isParam[t.Name]
	case *ArrayType:
		return mentionsParam(t.ElementType, isParam)
	case *UnionType:
		for _, member := range t.Types {
			if mentionsParam(member, isParam) {
				return true
			}
		}
	case *FunctionType:
		for _, p := range t.Params {
			if mentionsParam(p, isParam) {
				return true
			}
		}
		return mentionsParam(t.ReturnType, isParam)
	case *StructType:
		for _, arg := range t.TypeArgs {
			if mentionsParam(arg, isParam) {
				return true
			}
		}
	}
	return false
}

// hintFrom returns the type arguments the expected type gives an enum.
func (c *Checker) hintFrom(decl *ast.EnumDecl, expected Type) map[string]Type {
	hint := make(map[string]Type)
	if expected == nil {
		return hint
	}
	if hintDecl, st := c.enumOfType(withoutNull(expected)); hintDecl == decl {
		for i, name := range decl.TypeParams {
			if i < len(st.TypeArgs) && st.TypeArgs[i] != nil && !isUnknown(st.TypeArgs[i]) {
				hint[name] = st.TypeArgs[i]
			}
		}
	}
	return hint
}

// typeArgsFrom completes the type arguments of an enum: those already
// found, then, for what is left, those of the type the value is expected
// to have (`let o: Option<Int> = Option.None;`).
func (c *Checker) typeArgsFrom(decl *ast.EnumDecl, found map[string]Type, expected Type) []Type {
	if len(decl.TypeParams) == 0 {
		return nil
	}

	var hint *StructType
	if expected != nil {
		target := withoutNull(expected)
		if fn, ok := target.(*FunctionType); ok {
			target = fn.ReturnType // The variant used as a function
		}
		if hintDecl, st := c.enumOfType(target); hintDecl == decl {
			hint = st
		}
	}

	args := make([]Type, len(decl.TypeParams))
	for i, name := range decl.TypeParams {
		switch {
		case found[name] != nil:
			args[i] = found[name]
		case hint != nil && i < len(hint.TypeArgs):
			args[i] = hint.TypeArgs[i]
		default:
			args[i] = Unknown
		}
	}
	return args
}

// --- Patterns ---

// checkVariantPattern checks `Enum.Variant(patterns...)` against the type
// of the value being matched, and gives the names it binds their types.
// It returns the variant, and whether the pattern matches every value of
// that variant: a pattern with a literal or another variant inside it only
// matches some.
func (c *Checker) checkVariantPattern(p *ast.VariantPattern, subject Type) (variant string, covers bool) {
	// Whatever goes wrong, the names of the pattern exist in the case
	fail := func() (string, bool) {
		c.bindUnknown(p)
		return "", false
	}

	member, ok := p.Target.(*ast.MemberExpr)
	if !ok {
		c.errorf(p.Token(), ErrInvalidPattern, "'%s' is not a variant of an enum", p.Target)
		return fail()
	}
	if variantToFind(member) != nil && !c.findEnum(member, subject) {
		c.reportAmbiguousVariant(member.Expr.(*ast.Ident), member.Name)
		return fail()
	}
	decl, _ := c.enumOf(member.Expr)
	if decl == nil {
		c.errorfWithSuggestion(p.Token(), ErrInvalidPattern,
			"A case matches a literal, a type, or a variant written Enum.Variant",
			"'%s' is not a variant of an enum", p.Target)
		return fail()
	}
	v := findVariant(decl, member.Name)
	if v == nil {
		var names []string
		for _, each := range decl.Variants {
			names = append(names, each.Name)
		}
		suggestion := "Its variants are " + strings.Join(names, ", ")
		if similar := resolve.ClosestName(member.Name, names); similar != "" {
			suggestion = "Did you mean '" + similar + "'?"
		}
		c.errorfWithSuggestion(p.Token(), ErrInvalidPattern, suggestion, "enum %s has no variant '%s'", decl.Name, member.Name)
		return fail()
	}

	// The value has to be able to be of this enum
	var typeArgs []Type
	subjectDecl, subjectType := c.enumOfType(withoutNullOrNil(subject))
	switch {
	case subjectDecl == decl:
		typeArgs = subjectType.TypeArgs
	case subject == nil || isUnknown(subject) || subject.Equals(Any):
		// Only known when the program runs
	case c.assignable(subject, enumType(decl, nil)):
		// A union or a trait the enum is part of
	default:
		c.errorf(p.Token(), ErrInvalidPattern, "a value of type %s is never a %s", subject, describeVariant(decl, v))
		return fail()
	}

	fields := c.variantFields(decl, v, typeArgs)
	if !p.HasArgs {
		return v.Name, true
	}
	if len(p.Args) != len(fields) {
		form := decl.Name + "." + v.Name
		if len(fields) > 0 {
			names := make([]string, len(fields))
			for i := range names {
				names[i] = string(rune('a' + i%26))
			}
			form += "(" + strings.Join(names, ", ") + ")"
		}
		c.errorfWithSuggestion(p.Token(), ErrInvalidPattern, "Write the pattern as "+form,
			"%s holds %s, the pattern has %d", describeVariant(decl, v), values(len(fields)), len(p.Args))
		return fail()
	}

	covers = true
	for i, arg := range p.Args {
		switch a := arg.(type) {
		case *ast.BindingPattern:
			if !a.Ignored() {
				c.bindings[a] = fields[i]
			}
		case *ast.VariantPattern:
			c.checkVariantPattern(a, fields[i])
			covers = false
		case *ast.StructPattern:
			c.checkStructPattern(a, fields[i])
			covers = false
		case ast.Expr:
			literal := c.checkExpr(a)
			if literal != nil && fields[i] != nil && !isUnknown(fields[i]) && !c.comparable(fields[i], literal) {
				c.errorf(a.Token(), ErrInvalidPattern, "a value of type %s is never equal to a %s", fields[i], literal)
			}
			covers = false
		}
	}
	return v.Name, covers
}

// checkPatternTest checks `value is Pattern`, a Bool: the pattern against
// the type of the value, as for a case of a match on it. A pattern every
// value fits is not an error, but nothing is being asked: a warning.
func (c *Checker) checkPatternTest(e *ast.PatternTestExpr) Type {
	subject := c.checkExpr(e.Expr)
	_, covers := c.checkVariantPattern(e.Pattern, subject)

	decl := c.patternEnumDecl(e.Pattern)
	if !covers || decl == nil || len(decl.Variants) != 1 || subject == nil {
		return Bool
	}
	if subjectDecl, _ := c.enumOfType(subject); subjectDecl == decl {
		c.errs.ReportWarningFromToken(ErrUnreachableCode,
			fmt.Sprintf("this test always holds: every value of type %s fits the pattern", describe(subject)),
			e.Pattern.Token(), c.sourceLines)
	}
	return Bool
}

// patternEnumDecl returns the enum a pattern names a variant of, or nil
// when it names none.
func (c *Checker) patternEnumDecl(p *ast.VariantPattern) *ast.EnumDecl {
	member, ok := p.Target.(*ast.MemberExpr)
	if !ok {
		return nil
	}
	decl, _ := c.enumOf(member.Expr)
	return decl
}

func withoutNullOrNil(t Type) Type {
	if t == nil {
		return nil
	}
	return withoutNull(t)
}

// bindUnknown gives every name of a broken pattern an unknown type, so
// that the body of the case does not add errors of its own.
func (c *Checker) bindUnknown(p *ast.VariantPattern) {
	for _, binding := range p.Bindings() {
		c.bindings[binding] = Unknown
	}
}

// checkStructPattern checks `Point { x: 0, y }` against the type of the
// value matched, and gives the names it binds their types. It returns the
// struct type the value has when it matches.
func (c *Checker) checkStructPattern(p *ast.StructPattern, subject Type) Type {
	fail := func() Type {
		for _, binding := range p.Bindings() {
			c.bindings[binding] = Unknown
		}
		return Unknown
	}

	// The type with what the value says of its type arguments:
	// Box { value } against a Box<Int> has an Int for its value
	var st *StructType
	if known, ok := withoutNullOrNil(subject).(*StructType); ok && known.Name == p.Type.Name {
		st = known
	} else if resolved, ok := c.resolveType(p.Type).(*StructType); ok {
		st = resolved
	}
	if st == nil || c.structs[st.Name] == nil {
		c.errorfWithSuggestion(p.Token(), ErrInvalidPattern,
			"Name a struct before the braces: case Point { x: 0, y }:",
			"'%s' is not a struct", p.Type.Name)
		return fail()
	}
	if subject != nil && !isUnknown(subject) && !subject.Equals(Any) && !c.assignable(subject, st) && !c.assignable(st, withoutNullOrNil(subject)) {
		c.errorf(p.Token(), ErrInvalidPattern, "a value of type %s is never a %s", describe(subject), describe(st))
		return fail()
	}

	for _, field := range p.Fields {
		fieldType, ok := c.fieldType(st, field.Name)
		if ok {
			c.checkFieldVisible(st, field.Name, p.Token())
		}
		if !ok {
			c.errorf(p.Token(), ErrUnknownField, "struct %s has no field '%s'", displayName(st.Name), field.Name)
			for _, binding := range bindingsIn(field.Pattern) {
				c.bindings[binding] = Unknown
			}
			continue
		}
		switch a := field.Pattern.(type) {
		case *ast.BindingPattern:
			if !a.Ignored() {
				c.bindings[a] = fieldType
			}
		case *ast.VariantPattern:
			c.checkVariantPattern(a, fieldType)
		case *ast.StructPattern:
			c.checkStructPattern(a, fieldType)
		case ast.Expr:
			literal := c.checkExpr(a)
			if literal != nil && fieldType != nil && !isUnknown(fieldType) && !c.comparable(fieldType, literal) {
				c.errorf(a.Token(), ErrInvalidPattern, "a value of type %s is never equal to a %s", fieldType, literal)
			}
		}
	}
	return st
}

// bindingsIn lists the names any pattern gives.
func bindingsIn(pattern ast.Node) []*ast.BindingPattern {
	switch p := pattern.(type) {
	case *ast.BindingPattern:
		if !p.Ignored() {
			return []*ast.BindingPattern{p}
		}
	case *ast.VariantPattern:
		return p.Bindings()
	case *ast.StructPattern:
		return p.Bindings()
	}
	return nil
}

// checkExhaustive verifies that a match on an enum value handles every
// value it can have, and marks the match when it does. covered holds, for
// each case in order, the variant it handles completely ("" when it does
// not handle one completely); it is what the warning about a case that is
// never reached goes by.
func (c *Checker) checkExhaustive(s *ast.MatchStmt, subject Type, covered [][]string) {
	if subject != nil && subject.Equals(Bool) {
		c.markBoolExhaustive(s)
		return
	}
	if _, isStruct := subject.(*StructType); isStruct {
		// case Point { x, y }: takes every Point
		for _, matchCase := range s.Cases {
			if matchCase.Guard != nil {
				continue
			}
			for _, pattern := range matchCase.Patterns() {
				if _, ok := pattern.(*ast.StructPattern); ok && c.takesAll(pattern, subject) {
					s.Exhaustive = true
				}
			}
		}
	}
	decl, _ := c.enumOfType(subject)
	if decl == nil {
		return
	}

	handled := make(map[string]bool)
	hasDefault := false
	var rows [][]ast.Node
	for i, matchCase := range s.Cases {
		if matchCase.IsDefault() {
			hasDefault = true
			rows = append(rows, []ast.Node{nil})
			continue
		}
		// A case with a condition may not run although its pattern
		// matches: it handles nothing for certain
		certain := matchCase.Guard == nil
		for j, pattern := range matchCase.Patterns() {
			switch pattern := pattern.(type) {
			case *ast.VariantPattern:
				if name := c.patternVariant(pattern); name != "" && handled[name] {
					c.errs.ReportWarningFromToken(ErrUnreachableCode,
						fmt.Sprintf("this case is never reached: %s.%s is handled above", decl.Name, name),
						pattern.Token(), c.sourceLines)
				}
				if !certain {
					continue
				}
				if covered[i][j] != "" {
					handled[covered[i][j]] = true
				}
				rows = append(rows, []ast.Node{pattern})
			case *ast.Type:
				// `case Shape:` on a Shape takes every value
				if target := c.resolveType(pattern); certain && c.assignable(target, subject) {
					rows = append(rows, []ast.Node{nil})
				}
			}
		}
	}
	if hasDefault {
		return
	}

	// Every variant has to be taken, with every value it can hold. A
	// variant is reported once, by the first of its values left out.
	var missing []string
	for _, variant := range decl.Variants {
		if witness, open := c.uncovered(c.specialize(rows, decl, variant), c.variantFields(decl, variant, typeArgsOf(subject))); open {
			missing = append(missing, describeWitness(decl, variant, witness))
		}
	}
	if len(missing) == 0 {
		s.Exhaustive = true
		return
	}
	sort.Strings(missing)
	matched := decl.Name
	if ast.IsTupleName(decl.Name) {
		matched = describe(subject)
	}
	c.errorfWithSuggestion(s.Token(), ErrNonExhaustiveMatch,
		fmt.Sprintf("Add a case for each, or a 'default' case: case %s: { ... }", missing[0]),
		"match on %s does not handle %s", matched, strings.Join(missing, ", "))
	// Reported once: what follows the match is checked as if it were complete
	s.Exhaustive = true
}

// markBoolExhaustive marks a match on a Bool that has a case for true and
// one for false, without a condition, as handling every value. One that
// does not is left alone: a statement need not handle every value, and a
// match that gives a value says it lacks a default.
func (c *Checker) markBoolExhaustive(s *ast.MatchStmt) {
	var rows [][]ast.Node
	for _, matchCase := range s.Cases {
		if matchCase.IsDefault() {
			rows = append(rows, []ast.Node{nil})
			continue
		}
		if matchCase.Guard != nil {
			continue
		}
		for _, pattern := range matchCase.Patterns() {
			if decl := c.patternConst(pattern); decl != nil {
				// case YES: where const YES = true;
				if lit := c.constLiteral(decl); lit != nil {
					pattern = lit
				}
			}
			if lit, ok := pattern.(*ast.Literal); ok && lit.Kind == "bool" {
				rows = append(rows, []ast.Node{lit})
			}
		}
	}
	if _, open := c.uncovered(rows, []Type{Bool}); !open {
		s.Exhaustive = true
	}
}

func typeArgsOf(t Type) []Type {
	if st, ok := t.(*StructType); ok {
		return st.TypeArgs
	}
	return nil
}

// uncovered looks for values that no row of patterns matches. Each row has
// one pattern per column; types gives the type of each column. A nil
// pattern, like a name or `_`, matches anything.
//
// When something is left out it returns an example, one pattern per
// column. The method is the usual one for pattern matching: look at the
// first column, and for each form its values can take keep the rows that
// accept that form, with the patterns for what is inside it put in front.
func (c *Checker) uncovered(rows [][]ast.Node, types []Type) (witness []string, open bool) {
	if len(types) == 0 {
		// Nothing left to tell values apart: one row is enough
		return nil, len(rows) == 0
	}
	first, rest := types[0], types[1:]

	// Values are only told apart in this column if some row looks inside
	// them. When every row takes whatever is there, the column says
	// nothing, and going through the forms its values can take would never
	// end for a type that holds itself (an Expr inside an Expr).
	distinguishes := false
	for _, row := range rows {
		if !c.takesAll(row[0], first) {
			distinguishes = true
		}
	}

	// An enum: each of its variants, with what the variant holds
	if decl, st := c.enumOfType(withoutNullOrNil(first)); distinguishes && decl != nil && !isOptional(first) {
		for _, variant := range decl.Variants {
			fields := c.variantFields(decl, variant, st.TypeArgs)
			inner, open := c.uncovered(c.specialize(rows, decl, variant), append(append([]Type{}, fields...), rest...))
			if open {
				return append([]string{describeWitness(decl, variant, inner[:len(fields)])}, inner[len(fields):]...), true
			}
		}
		return nil, false
	}

	// A Bool: true and false
	if distinguishes && first != nil && first.Equals(Bool) {
		for _, value := range []string{"true", "false"} {
			var kept [][]ast.Node
			for _, row := range rows {
				if lit, ok := row[0].(*ast.Literal); ok {
					if lit.Kind != "bool" || lit.Value != value {
						continue
					}
				} else if !isWildcard(row[0]) {
					continue
				}
				kept = append(kept, row[1:])
			}
			if inner, open := c.uncovered(kept, rest); open {
				return append([]string{value}, inner...), true
			}
		}
		return nil, false
	}

	// Anything else has too many values to list: only a row that takes
	// them all counts
	var kept [][]ast.Node
	for _, row := range rows {
		if c.takesAll(row[0], first) {
			kept = append(kept, row[1:])
		}
	}
	if inner, open := c.uncovered(kept, rest); open {
		return append([]string{"_"}, inner...), true
	}
	return nil, false
}

// specialize keeps the rows whose first pattern accepts a given variant,
// and replaces that pattern by the patterns for the variant's values.
func (c *Checker) specialize(rows [][]ast.Node, decl *ast.EnumDecl, variant *ast.EnumVariant) [][]ast.Node {
	var kept [][]ast.Node
	for _, row := range rows {
		inner := make([]ast.Node, len(variant.Values))
		if pattern, ok := row[0].(*ast.VariantPattern); ok {
			patternDecl, name := c.patternEnum(pattern)
			if patternDecl != decl || name != variant.Name {
				continue
			}
			// Without parentheses, or with the wrong number of patterns
			// (reported elsewhere), it takes the variant whole
			if pattern.HasArgs && len(pattern.Args) == len(variant.Values) {
				copy(inner, pattern.Args)
			}
		} else if !isWildcard(row[0]) {
			continue
		}
		kept = append(kept, append(inner, row[1:]...))
	}
	return kept
}

// patternEnum returns the enum and the variant a pattern names.
func (c *Checker) patternEnum(p *ast.VariantPattern) (*ast.EnumDecl, string) {
	member, ok := p.Target.(*ast.MemberExpr)
	if !ok {
		return nil, ""
	}
	decl, _ := c.enumOf(member.Expr)
	return decl, member.Name
}

// takesAll reports whether a pattern matches every value of a type: a
// wildcard, or the pattern of a struct of that type whose fields are all
// taken whole, Line { from, to: Point { x, y } }.
func (c *Checker) takesAll(p ast.Node, t Type) bool {
	if isWildcard(p) {
		return true
	}
	sp, ok := p.(*ast.StructPattern)
	if !ok {
		return false
	}
	st, ok := withoutNullOrNil(t).(*StructType)
	if !ok || st.Name != sp.Type.Name || isOptional(t) {
		return false
	}
	for _, field := range sp.Fields {
		fieldType, _ := c.fieldType(st, field.Name)
		if !c.takesAll(field.Pattern, fieldType) {
			return false
		}
	}
	return true
}

// isWildcard reports whether a pattern matches every value: no pattern at
// all, a name, or `_`.
func isWildcard(p ast.Node) bool {
	if p == nil {
		return true
	}
	_, isBinding := p.(*ast.BindingPattern)
	return isBinding
}

func isOptional(t Type) bool {
	u, ok := t.(*UnionType)
	return ok && hasNull(u)
}

// describeWitness writes a value that a match leaves out, as a pattern:
// Shape.Rect when nothing of the variant is handled, Report.Line(_,
// Event.Closed) when only some of it is.
func describeWitness(decl *ast.EnumDecl, variant *ast.EnumVariant, inner []string) string {
	if ast.IsTupleName(decl.Name) {
		// A tuple is written as it is in the program
		return "(" + strings.Join(inner, ", ") + ")"
	}
	name := decl.Name + "." + variant.Name
	whole := true
	for _, part := range inner {
		if part != "_" {
			whole = false
		}
	}
	if whole {
		return name
	}
	return name + "(" + strings.Join(inner, ", ") + ")"
}

// patternVariant returns the name of the variant a pattern is about.
func (c *Checker) patternVariant(p *ast.VariantPattern) string {
	if member, ok := p.Target.(*ast.MemberExpr); ok {
		return member.Name
	}
	return ""
}

// --- Enum values after desugaring ---

func (c *Checker) checkVariantExpr(e *ast.VariantExpr) Type {
	c.typesOf(e.Args)
	decl := c.enums[e.Enum]
	if decl == nil {
		return Unknown
	}
	var typeArgs []Type
	for _, arg := range e.TypeArgs {
		typeArgs = append(typeArgs, c.resolveType(arg))
	}
	return enumType(decl, typeArgs)
}

func (c *Checker) checkVariantFieldExpr(e *ast.VariantFieldExpr) Type {
	subject := c.checkExpr(e.Expr)
	decl := c.enums[e.Enum]
	if decl == nil {
		return Unknown
	}
	variant := findVariant(decl, e.Variant)
	if variant == nil || e.Index >= len(variant.Values) {
		return Unknown
	}

	var typeArgs []Type
	if subjectDecl, st := c.enumOfType(withoutNullOrNil(subject)); subjectDecl == decl {
		typeArgs = st.TypeArgs
	}
	return c.variantFields(decl, variant, typeArgs)[e.Index]
}

// describeEnumCast is the runtime description of an enum type: the names
// its values can carry, one per variant.
func describeEnumCast(decl *ast.EnumDecl) []string {
	names := make([]string, len(decl.Variants))
	for i, v := range decl.Variants {
		names[i] = decl.Name + "." + v.Name
	}
	return names
}

// --- Constants ---

// constType returns the type of a top-level constant, checking its value
// the first time it is asked for. Constants may refer to each other in any
// order, so one is checked when it is first used, not when it is met.
func (c *Checker) constType(d *ast.ConstDecl) Type {
	if t, done := c.constTypes[d]; done {
		return t
	}
	if c.constBusy[d] {
		c.errorfWithSuggestion(d.Token(), ErrConstantCycle,
			"A constant can use other constants, but not, through them, itself",
			"constant '%s' is defined in terms of itself", d.Name)
		c.constTypes[d] = Unknown
		return Unknown
	}
	c.constBusy[d] = true

	c.checkConstantExpr(d.Value)
	var declared Type
	if d.Type != nil {
		declared = c.resolveType(d.Type)
	}
	value := c.checkExprAs(d.Value, declared)

	result := value
	if declared != nil {
		if value != nil && !c.assignable(declared, value) {
			c.errorf(d.Value.Token(), ErrTypeMismatch, "cannot use a value of type %s for constant '%s' of type %s", value, d.Name, declared)
		}
		result = declared
	}
	if result == nil {
		result = Unknown
	}

	delete(c.constBusy, d)
	// A cycle found on the way has already settled the type
	if _, done := c.constTypes[d]; !done {
		c.constTypes[d] = result
	}
	return c.constTypes[d]
}

// checkConstantExpr reports the part of a constant's value that cannot be
// worked out when the program is compiled.
func (c *Checker) checkConstantExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.Literal:
		return
	case *ast.UnaryExpr:
		c.checkConstantExpr(e.Expr)
		return
	case *ast.BinaryExpr:
		if !ast.IsAssignment(e.Op) {
			c.checkConstantExpr(e.Left)
			c.checkConstantExpr(e.Right)
			return
		}
	case *ast.Ident:
		if sym := c.table.Defs[e]; sym != nil {
			if _, isConst := sym.Decl.(*ast.ConstDecl); isConst {
				return
			}
		} else {
			return // Undefined: reported by name resolution
		}
	case *ast.CallExpr:
		// "size in ${UNIT}": a text made of constants is one
		if isInterpolation(e) {
			c.checkConstantExpr(e.Args[0])
			return
		}
		// Shape.Circle(1.0): a value of an enum that holds constants
		if c.isVariantRef(variantCallee(e)) {
			for _, arg := range e.Args {
				c.checkConstantExpr(arg)
			}
			return
		}
	case *ast.MemberExpr:
		// Light.Red: a value of an enum that holds nothing
		if c.isVariantRef(e) {
			return
		}
	case *ast.StructInitExpr:
		// Color { r: 230, g: 41, b: 55 }: a struct of constants. Named in
		// order, so that what is wrong is reported the same way every time
		names := make([]string, 0, len(e.Fields))
		for name := range e.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c.checkConstantExpr(e.Fields[name])
		}
		return
	case *ast.ArrayLiteralExpr:
		for _, elem := range e.Elements {
			c.checkConstantExpr(elem)
		}
		return
	}
	c.errorfWithSuggestion(expr.Token(), ErrNotConstant,
		"Use literals, operators, other constants, and structs, arrays and values of enums made of those; for anything else, write a function that returns the value",
		"the value of a constant has to be known when the program is compiled")
}

// isInterpolation reports whether a call is the one a `${...}` in a string
// stands for.
func isInterpolation(e *ast.CallExpr) bool {
	callee, ok := e.Func.(*ast.Ident)
	return ok && callee.Name == "__text" && len(e.Args) == 1
}

// values counts the values of a variant in words: "no values", "1 value".
func values(n int) string {
	switch n {
	case 0:
		return "no values"
	case 1:
		return "1 value"
	}
	return fmt.Sprintf("%d values", n)
}

// variantCallee returns `Enum.Variant` from an expression that is one, or
// a call of one.
func variantCallee(expr ast.Expr) ast.Expr {
	if call, ok := expr.(*ast.CallExpr); ok {
		return call.Func
	}
	return expr
}

func firstVariant(decl *ast.EnumDecl) string {
	if len(decl.Variants) == 0 {
		return "Variant"
	}
	return decl.Variants[0].Name
}
