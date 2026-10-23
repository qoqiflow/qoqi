package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/token"
)

const (
	ErrTypeMismatch      = "E2001"
	ErrReturnOutsideFn   = "E2002"
	ErrReturnMismatch    = "E2003"
	ErrMissingReturn     = "E2004"
	ErrInvalidCondition  = "E2005"
	ErrInvalidOperation  = "E2006"
	ErrInvalidUnary      = "E2007"
	ErrNotFunction       = "E2008"
	ErrArgCount          = "E2009"
	ErrArgType           = "E2010"
	ErrNotIterable       = "E2011"
	ErrMissingMethod     = "E2012"
	ErrSignatureMismatch = "E2013"
	ErrUnknownField      = "E2014"
	ErrUnknownMethod     = "E2015"
	ErrUndefinedStruct   = "E2016"
	ErrPossiblyNull      = "E2017"
	ErrMissingField      = "E2018"
	ErrDuplicateMethod   = "E2019"
	ErrUntypedEmptyArray = "E2020"
	ErrInvalidCast       = "E2021"
	ErrAssignToConst     = "E2022"
	ErrUnreachableCode   = "E2023"
	ErrMethodTypeArgs    = "E2024"
	ErrIntOutOfRange     = "E2025"
	ErrAssignToLoopVar   = "E2026"
	ErrPrivateMethod     = "E2027"
	ErrNoMembers         = "E2033" // `.length` on an array or a text
	ErrBadTry            = "E2034" // `value?` where there is no failure to pass on, or nowhere to pass it
	ErrNeverNull         = "E2035" // `value?.member` on a value that cannot be null
	ErrBoundNotMet       = "E2036" // A type argument that does not implement what its parameter requires
	ErrShortFn           = "E2037" // A function written with `->` where nothing says what its types are
	ErrBadRange          = "E2038" // A range that is not of whole numbers, or used as a value
	ErrBadDefer          = "E2039" // A defer that tries to leave: return, '?', break or continue of an outer loop
	ErrMatchValue        = "E2040" // A match that gives a value with a case that gives none
	ErrAmbiguousVariant  = "E2041" // A variant written alone that several enums have, and nothing says which
	ErrPrivateField      = "E2042" // A field of a struct of another module that is not pub
)

type Checker struct {
	// The statements after the one being checked, in its block, and the
	// let without a type whose value is a call being checked: what a
	// generic call says nothing of may be said by how its variable is
	// used further on (see typeOfUse)
	rest    []ast.Stmt
	letCall *pendingLet

	// What has been reported, so that nothing is reported twice
	said map[string]bool

	// While a cast is checked, an array may stand for one of a wider
	// element type: the cast checks each element when it runs
	castingArrays bool

	prog        *ast.Program
	table       *resolve.Table
	errs        *errors.Handler
	info        *Info
	sourceLines map[int]string

	// Context for return type checking
	currentFn *ast.FnDecl

	// Type parameters in scope, with the type each one is bound to. Inside
	// a generic declaration a parameter is in scope but unbound (nil): it is
	// an opaque type that only fits itself. At a use with explicit type
	// arguments, the parameters are bound to those arguments.
	typeParams map[string]Type

	// What `Self` stands for: the struct inside an impl, nil elsewhere
	selfType Type

	// Types of the names bound by match patterns, and of the top-level
	// constants checked so far (see enums.go)
	bindings map[*ast.BindingPattern]Type

	// Inside the body of a defer, and how many loops of that body are open:
	// a defer cannot leave, except a loop of its own
	inDefer      bool
	loopsInDefer int
	constTypes   map[*ast.ConstDecl]Type
	constBusy    map[*ast.ConstDecl]bool

	// Declarations indexed by name, and which structs implement which traits
	structs  map[string]*ast.StructDecl
	enums    map[string]*ast.EnumDecl
	traits   map[string]*ast.TraitDecl
	methods  map[string]map[string]*ast.FnDecl // struct -> method -> declaration
	impls    map[*ast.FnDecl]*ast.ImplDecl     // method -> the impl block it is in
	registry *ImplRegistry
	// By the name of a type, then of a trait: the impl that makes the type
	// implement the trait
	traitImpls map[string]map[string]*ast.ImplDecl
	// What the type parameters in scope are required to implement
	bounds map[string][]*ast.Type
	// The types whose requirements were checked, each once
	boundsChecked map[*ast.Type]bool

	// Flow typing (see flow.go): variables currently known to be non-null,
	// and how often each variable has been assigned so far
	narrowed       map[*resolve.Symbol]Type
	narrowedPaths  map[string]pathFact // field paths such as `node.next`
	pathEpoch      int                 // advanced whenever field facts are dropped
	writes         map[*resolve.Symbol]int
	shared         map[*resolve.Symbol]bool // assigned by a lambda that does not own them
	reassigned     map[*resolve.Symbol]bool // assigned somewhere after being declared
	miscounted     map[string]bool          // tuples taken apart with the wrong number of names
	enteringFnExpr bool                     // the function about to be checked is a function expression

	// Which functions may assign a field when called (see findFieldWriters)
	fieldWriters map[*ast.FnDecl]bool
	methodsNamed map[string][]*ast.FnDecl
	fieldNames   map[string]bool

	// The type the surrounding code expects of the call being checked,
	// used to infer type arguments the call's own arguments leave open
	expected Type
}

func NewChecker(prog *ast.Program, table *resolve.Table, errs *errors.Handler) *Checker {
	return &Checker{
		prog:          prog,
		table:         table,
		errs:          errs,
		info:          NewInfo(),
		sourceLines:   make(map[int]string),
		typeParams:    make(map[string]Type),
		structs:       make(map[string]*ast.StructDecl),
		enums:         make(map[string]*ast.EnumDecl),
		bindings:      make(map[*ast.BindingPattern]Type),
		constTypes:    make(map[*ast.ConstDecl]Type),
		constBusy:     make(map[*ast.ConstDecl]bool),
		traits:        make(map[string]*ast.TraitDecl),
		methods:       make(map[string]map[string]*ast.FnDecl),
		impls:         make(map[*ast.FnDecl]*ast.ImplDecl),
		registry:      NewImplRegistry(),
		traitImpls:    make(map[string]map[string]*ast.ImplDecl),
		bounds:        make(map[string][]*ast.Type),
		boundsChecked: make(map[*ast.Type]bool),
		narrowed:      make(map[*resolve.Symbol]Type),
		narrowedPaths: make(map[string]pathFact),
		writes:        make(map[*resolve.Symbol]int),
		shared:        make(map[*resolve.Symbol]bool),
		reassigned:    make(map[*resolve.Symbol]bool),
		miscounted:    make(map[string]bool),

		fieldWriters: make(map[*ast.FnDecl]bool),
		methodsNamed: make(map[string][]*ast.FnDecl),
		fieldNames:   make(map[string]bool),
	}
}

func (c *Checker) SetSourceLines(lines map[int]string) {
	c.sourceLines = lines
}

func (c *Checker) Check() *Info {
	c.info.implements = func(t Type, trait string) bool {
		_, ok := c.traitArgsOf(t, trait)
		return ok
	}
	c.collectDecls()
	c.findFieldWriters()

	for _, decl := range c.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			c.findSharedVariables(d.Body)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				c.findSharedVariables(method.Body)
			}
		}
	}

	// The variables outside functions, before the functions that use
	// them: a function can change one between any two lines of another,
	// so nothing a test learns of one holds, as for a variable a closure
	// assigns
	globals := make(map[ast.Node]bool)
	for _, global := range c.prog.Globals {
		globals[global] = true
		c.checkLetStmt(global)
	}
	if len(globals) > 0 {
		for _, sym := range c.table.Defs {
			if globals[sym.Decl] {
				c.shared[sym] = true
			}
		}
	}

	for _, decl := range c.prog.Decls {
		c.checkDecl(decl)
	}
	return c.info
}

func (c *Checker) checkDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		c.checkFnDecl(d)
	case *ast.StructDecl:
		c.checkStructDecl(d)
	case *ast.EnumDecl:
		c.checkEnumDecl(d)
	case *ast.ConstDecl:
		c.constType(d)
	case *ast.TraitDecl:
		c.checkTraitDecl(d)
	case *ast.ImplDecl:
		c.checkImplDecl(d)
	}
}

func (c *Checker) checkFnDecl(d *ast.FnDecl) {
	prevFn := c.currentFn
	c.currentFn = d
	defer func() { c.currentFn = prevFn }()
	defer c.bindTypeParams(d.TypeParams)()
	defer c.bindBounds(d.Bounds)()

	if d.Body == nil {
		return
	}
	c.recordSignature(d, d)
	c.checkDefaults(d.Params)
	c.checkStep(d)

	// A function body may run at any later time, so it cannot rely on what
	// is known here about outer variables that change; and what it assigns
	// is no longer known afterwards.
	//
	// A variable that is never assigned after its declaration cannot
	// change, whenever the body runs: what a test established about it
	// before a function expression still holds inside.
	outer := c.flowSnapshot()
	isFnExpr := c.enteringFnExpr
	c.enteringFnExpr = false
	// A function written inside a defer is a function like any other
	inDefer, loopsInDefer := c.inDefer, c.loopsInDefer
	c.inDefer, c.loopsInDefer = false, 0
	defer func() { c.inDefer, c.loopsInDefer = inDefer, loopsInDefer }()
	c.narrowed = make(map[*resolve.Symbol]Type)
	if isFnExpr {
		for sym, typ := range outer.facts.vars {
			if !c.reassigned[sym] {
				c.narrowed[sym] = typ
			}
		}
	}
	c.narrowedPaths = make(map[string]pathFact)
	c.checkBlockStmt(d.Body)
	c.flowRestore(outer)

	// A script that reaches its end exits with 0
	if d.ReturnType != nil && !d.Script && !alwaysReturns(d.Body) {
		if returnType := c.resolveType(d.ReturnType); !returnType.Equals(Void) && !isUnknown(returnType) {
			what := "function"
			if d.Name != "" {
				what = "function '" + d.Name + "'"
			}
			c.errorfWithSuggestion(d.Token(), ErrMissingReturn,
				fmt.Sprintf("Return a value of type %s at the end of every path", describe(returnType)),
				"%s can reach its end without returning a value", what)
		}
	}
}

func (c *Checker) checkStructDecl(d *ast.StructDecl) {
	defer c.bindTypeParams(d.TypeParams)()
	defer c.bindBounds(d.Bounds)()

	// Check that all field types are valid
	fields := map[string]string{}
	c.info.StructFields[d.Name] = fields
	for _, field := range d.Fields {
		if field.Type == nil {
			continue
		}
		fieldType := c.resolveType(field.Type)
		fields[field.Name] = describe(fieldType)
		if field.Default == nil {
			continue
		}
		// port: Int = 8080; the value has to be one the field can hold
		value := c.checkExprAs(field.Default, fieldType)
		if value != nil && !c.assignable(fieldType, value) {
			c.errorfWithSuggestion(field.Default.Token(), ErrTypeMismatch,
				fmt.Sprintf("Give a value of type %s, or change the type of the field", describe(fieldType)),
				"field '%s' is of type %s, and its value when none is given is of type %s", field.Name, fieldType, value)
		}
	}
}

func (c *Checker) checkTraitDecl(d *ast.TraitDecl) {
	defer c.bindTypeParams(d.TypeParams)()
	c.withSelf(c.traitSelf(d), func() { c.checkTraitMethods(d) })
}

func (c *Checker) checkTraitMethods(d *ast.TraitDecl) {
	// Check all trait methods
	for _, method := range d.Methods {
		// Trait methods can be abstract (no body) or have default implementations
		// We check the signature is valid
		for _, param := range method.Params {
			if param.Type != nil {
				c.resolveType(param.Type)
			}
		}
		if method.ReturnType != nil {
			c.resolveType(method.ReturnType)
		}
		// If the method has a body, check it
		if method.Body != nil {
			c.checkFnDecl(method)
		}
	}
}

func (c *Checker) checkImplDecl(d *ast.ImplDecl) {
	// The struct's type parameters are in scope for the methods
	defer c.bindTypeParams(d.TypeParams)()
	defer c.bindBounds(d.Bounds)()

	if item, arrays := ast.IsArrayImpl(d.StructName); arrays && item != "" && d.TraitName != "" {
		// What an array holds is not known when the program runs, and a
		// value seen through a trait finds its method then
		c.errorfWithSuggestion(d.Token(), ErrUnknownMethod,
			fmt.Sprintf("Implement the trait for every array, impl<T> %s for [T] { ... }, or give these arrays methods of their own: impl [%s] { ... }", displayName(d.TraitName), item),
			"a trait is implemented for every array, not for the arrays of one type")
		return
	}

	// Inside the impl, `Self` and `self` refer to the struct
	c.withSelf(c.implSelfType(d), func() {
		// Check that all impl methods are valid functions
		for _, method := range d.Methods {
			c.checkFnDecl(method)
		}

		// If this is a trait implementation (not just inherent impl), verify completeness
		if d.TraitName != "" {
			traitDecl := c.findTraitDecl(d.TraitName)
			if traitDecl != nil && c.checkImplHeader(d, traitDecl) {
				c.verifyTraitImpl(d, traitDecl)
			}
		}
	})
}

// implSelfType returns the type of `self` in an impl block: the struct,
// with the impl's type parameters as its type arguments.
func (c *Checker) implSelfType(d *ast.ImplDecl) Type {
	self := &StructType{Name: d.StructName, BaseName: d.StructName}
	for _, name := range d.TypeParams {
		self.TypeArgs = append(self.TypeArgs, c.resolveType(&ast.Type{Kind: ast.TypeBasic, Name: name}))
	}
	if d.StructName == ast.OptionalImpl {
		// impl<T> T?: an optional of what the type parameter stands for
		if len(self.TypeArgs) == 1 {
			return optional(self.TypeArgs[0])
		}
		return Unknown
	}
	if item, arrays := ast.IsArrayImpl(d.StructName); arrays {
		switch {
		case item != "":
			// impl [Int]: the arrays of one type
			return NewArray(c.resolveType(&ast.Type{BaseNode: d.BaseNode, Kind: ast.TypeBasic, Name: item}))
		case len(self.TypeArgs) == 1:
			// impl<T> [T]: an array of what the type parameter stands for
			return NewArray(self.TypeArgs[0])
		}
		return NewArray(Unknown)
	}
	if _, isStruct := c.structs[d.StructName]; !isStruct && c.enums[d.StructName] == nil {
		// impl for a builtin type
		return c.resolveType(&ast.Type{Kind: ast.TypeBasic, Name: d.StructName})
	}
	return self
}

// declaredTargetType types the target of an assignment to a field or an
// element. Like for variables, the declared type counts: a field known to
// be non-null here can be set back to null.
func (c *Checker) declaredTargetType(target ast.Expr) Type {
	key, _, isPath := c.fieldPath(target)
	fact, narrowed := c.narrowedPaths[key]
	if !isPath || !narrowed {
		return c.checkExpr(target)
	}

	delete(c.narrowedPaths, key)
	typ := c.checkExpr(target)
	c.narrowedPaths[key] = fact
	return typ
}

// checkCompoundAssignment types `target op= value`: the target is read,
// combined with the value, and the result is stored back.
func (c *Checker) checkCompoundAssignment(e *ast.BinaryExpr, op string) Type {
	var declared Type
	var sym *resolve.Symbol

	if ident, ok := e.Left.(*ast.Ident); ok {
		declared = c.declaredIdentType(ident)
		sym = c.table.Defs[ident]
		c.checkNotConst(ident, sym)
	} else {
		c.checkNotInConst(e.Left)
		if target, ok := e.Left.(*ast.IndexExpr); ok {
			// stock["nails"] += 1 on a struct would read and write through
			// two methods, asking for the key twice
			if recv := c.checkExpr(target.Expr); recv != nil && typeKeyOrParam(recv) != "" {
				c.errorfWithSuggestion(e.Token(), ErrInvalidOperation,
					fmt.Sprintf("Write it out: target[key] = target[key] %s value;", op),
					"%s cannot be used on what is in the brackets of %s", e.Op, recv)
				c.checkExpr(e.Right)
				return Unknown
			}
		}
		declared = c.declaredTargetType(e.Left)
	}

	// The computation is an ordinary binary expression on the target's
	// current value, which may be narrowed: `n += 1` is fine for an Int?
	// known to be non-null here
	computation := &ast.BinaryExpr{BaseNode: e.BaseNode, Left: e.Left, Op: op, Right: e.Right}
	result := c.checkBinaryExpr(computation)
	if method, ok := c.info.OperatorMethods[computation]; ok {
		// position += step, on a struct that says what + is
		c.info.OperatorMethods[e] = method
	}

	if result != nil && !isUnknown(result) && declared != nil && !c.assignable(declared, result) {
		c.errorf(e.Token(), ErrTypeMismatch, "cannot assign the %s result of %s to %s", result, e.Op, declared)
	}

	if sym != nil && sym.Kind == resolve.SymbolVar {
		c.recordWrite(sym)
		// The result of arithmetic is never null
		if declared != nil && result != nil && !isUnknown(result) && !c.shared[sym] {
			if value := withoutNull(declared); value != nil && !value.Equals(declared) && c.assignable(value, result) {
				c.narrowed[sym] = value
			}
		}
	}
	if _, toField := e.Left.(*ast.MemberExpr); toField {
		c.forgetFields()
	}
	return Void
}

// checkNotInConst reports an assignment to a field or an item of a
// constant: ORIGIN.x = 1; A constant that is a struct or an array is made
// again wherever it is used, so the assignment would change a value nobody
// else sees, and look like it changed the constant.
func (c *Checker) checkNotInConst(target ast.Expr) {
	root := target
	for {
		switch e := root.(type) {
		case *ast.MemberExpr:
			root = e.Expr
			continue
		case *ast.IndexExpr:
			root = e.Expr
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || root == target {
		return
	}
	sym := c.table.Defs[ident]
	if sym == nil {
		return
	}
	if _, isConst := sym.Decl.(*ast.ConstDecl); isConst {
		c.errorfWithSuggestion(target.Token(), ErrAssignToConst,
			fmt.Sprintf("Copy it into a variable to change the copy: let mine = %s;", ident.Name),
			"cannot change what the constant '%s' holds", ident.Name)
	}
}

// checkNotConst reports an assignment to a name that cannot be assigned:
// one declared with const, or a variable of a for loop.
func (c *Checker) checkNotConst(ident *ast.Ident, sym *resolve.Symbol) {
	if sym == nil {
		return
	}

	switch decl := sym.Decl.(type) {
	case *ast.ConstDecl:
		c.errorfWithSuggestion(ident.Token(), ErrAssignToConst,
			"A constant keeps the value it is declared with; use a variable for something that changes",
			"cannot assign to constant '%s'", ident.Name)

	case *ast.LetStmt:
		if decl.IsConst {
			c.errorfWithSuggestion(ident.Token(), ErrAssignToConst,
				fmt.Sprintf("Declare it with 'let %s' to make it assignable", ident.Name),
				"cannot assign to constant '%s'", ident.Name)
		}

	case *ast.ForIndex:
		// Assigning would not move the loop, which is what it looks like
		c.errorfWithSuggestion(ident.Token(), ErrAssignToLoopVar,
			"The index follows the loop and cannot be changed; use a while loop to control the position",
			"cannot assign to loop index '%s'", ident.Name)

	case *ast.ForStmt:
		// Assigning would not change the collection, which is what it
		// looks like
		suggestion := fmt.Sprintf("'%s' is a copy of the element; to change the collection, assign to it by index", ident.Name)
		if _, counted := decl.Iterable.(*ast.RangeExpr); counted {
			suggestion = fmt.Sprintf("'%s' follows the range; use a while loop to control the count", ident.Name)
		}
		if decl.Index != nil {
			suggestion = fmt.Sprintf("'%s' is a copy of the element; to change the collection, assign to %s[%s]",
				ident.Name, decl.Iterable.String(), decl.Index.Name)
		}
		c.errorfWithSuggestion(ident.Token(), ErrAssignToLoopVar, suggestion,
			"cannot assign to loop variable '%s'", ident.Name)
	}
}

// checkAssignment types `target = value`.
func (c *Checker) checkAssignment(e *ast.BinaryExpr) Type {
	var left Type
	var sym *resolve.Symbol

	if ident, ok := e.Left.(*ast.Ident); ok {
		// Assigning is checked against the declared type: a variable known
		// to be non-null here may still be set back to null
		left = c.declaredIdentType(ident)
		c.info.Types[ident] = left
		sym = c.table.Defs[ident]

		c.checkNotConst(ident, sym)
	} else if value, ok := c.indexWrite(e); ok {
		c.checkNotInConst(e.Left)
		left = value
	} else {
		c.checkNotInConst(e.Left)
		left = c.declaredTargetType(e.Left)
		if index, ok := e.Left.(*ast.IndexExpr); ok && IsRange(c.info.Types[index.Index]) {
			c.errorfWithSuggestion(e.Token(), ErrInvalidOperation,
				"Assign the items one at a time, or make the whole anew: items = items[..1].concat(more).concat(items[3..])",
				"a part of an array or a text cannot be assigned to")
			left = nil
		}
		if member, ok := e.Left.(*ast.MemberExpr); ok {
			if _, byPosition := ast.TupleIndex(member.Name); byPosition && left != nil && !isUnknown(left) {
				c.errorfWithSuggestion(e.Token(), ErrAssignToConst,
					"Make a new tuple with the value changed, or use a struct, whose fields can be assigned",
					"a tuple cannot be changed once made")
			}
		}
	}

	var right Type
	if left != nil {
		asTemplate(&e.Right, left)
		right = c.checkExprAs(e.Right, left)
	} else {
		right = c.checkExpr(e.Right)
	}

	if _, toField := e.Left.(*ast.MemberExpr); toField {
		// A field changed: the same value may be reachable through other
		// paths
		c.forgetFields()
	}

	if sym != nil && sym.Kind == resolve.SymbolVar {
		c.recordWrite(sym)
		// A value that cannot be null makes an optional variable non-null
		if left != nil && right != nil && !isUnknown(right) && !c.shared[sym] {
			if value := withoutNull(left); value != nil && !value.Equals(left) && c.assignable(value, right) {
				c.narrowed[sym] = value
			}
		}
	}

	if left != nil && right != nil && !c.assignable(left, right) {
		c.errorfWithSuggestion(e.Right.Token(), ErrTypeMismatch, c.arrayAdvice(left, right, fmt.Sprintf("Ensure the value is of type %s", describe(left))), "cannot assign type %s to %s", right, left)
	}
	return Void
}

// findTraitDecl finds the trait declaration by name in the program
func (c *Checker) findTraitDecl(name string) *ast.TraitDecl {
	for _, decl := range c.prog.Decls {
		if traitDecl, ok := decl.(*ast.TraitDecl); ok {
			if traitDecl.Name == name {
				return traitDecl
			}
		}
	}
	return nil
}

// verifyTraitImpl verifies that an impl provides all required methods and signatures match
func (c *Checker) verifyTraitImpl(implDecl *ast.ImplDecl, traitDecl *ast.TraitDecl) {
	// Build a map of implemented methods
	implMethods := make(map[string]*ast.FnDecl)
	for _, method := range implDecl.Methods {
		implMethods[method.Name] = method
	}

	// Check each trait method
	for _, traitMethod := range traitDecl.Methods {
		implMethod, ok := implMethods[traitMethod.Name]
		if !ok && traitMethod.Body != nil {
			// The trait says what the method does when an impl does not
			continue
		}
		if !ok {
			c.errorfWithSuggestion(
				implDecl.Token(),
				ErrMissingMethod,
				fmt.Sprintf("Implement method '%s' for trait '%s'", traitMethod.Name, traitDecl.Name),
				"implementation missing required method '%s' from trait '%s'",
				traitMethod.Name,
				traitDecl.Name,
			)
			continue
		}

		// Verify method signatures match, the trait's type parameters
		// standing for what the impl gives them
		traitArgs := make(map[string]Type, len(traitDecl.TypeParams))
		for i, name := range traitDecl.TypeParams {
			traitArgs[name] = c.resolveType(implDecl.TraitArgs[i])
		}
		restore := c.bind(traitArgs)
		c.verifyMethodSignature(implMethod, traitMethod, traitDecl.Name)
		restore()
	}
}

// verifyMethodSignature checks that an impl method signature matches the trait method
func (c *Checker) verifyMethodSignature(implMethod, traitMethod *ast.FnDecl, traitName string) {
	// Check parameter count
	if len(implMethod.Params) != len(traitMethod.Params) {
		c.errorfWithSuggestion(
			implMethod.Token(),
			ErrSignatureMismatch,
			fmt.Sprintf("Method should have %d parameters to match trait '%s'", len(traitMethod.Params), traitName),
			"method '%s' has %d parameters, trait requires %d",
			implMethod.Name,
			len(implMethod.Params),
			len(traitMethod.Params),
		)
		return
	}

	if len(implMethod.TypeParams) != len(traitMethod.TypeParams) {
		c.errorfWithSuggestion(
			implMethod.Token(),
			ErrSignatureMismatch,
			fmt.Sprintf("Declare it as %s%s to match trait '%s'", implMethod.Name, typeParamList(traitMethod.TypeParams), traitName),
			"method '%s' has %d type parameters, trait requires %d",
			implMethod.Name,
			len(implMethod.TypeParams),
			len(traitMethod.TypeParams),
		)
		return
	}

	// The names of type parameters are placeholders: fn convert<U>(v: U)
	// implements fn convert<T>(v: T). Both signatures are therefore
	// resolved with the trait's names, the impl's parameters standing for
	// the trait's in the same position.
	signature := func(method *ast.FnDecl, typeParams map[string]Type) ([]Type, Type) {
		defer c.bind(typeParams)()
		var params []Type
		for _, param := range method.Params {
			params = append(params, c.resolveType(param.Type))
		}
		return params, c.resolveType(method.ReturnType)
	}

	traitScope := make(map[string]Type)
	implScope := make(map[string]Type)
	for i, name := range traitMethod.TypeParams {
		traitScope[name] = NewTypeParam(name)
		implScope[implMethod.TypeParams[i]] = NewTypeParam(name)
	}
	traitParams, traitReturnType := signature(traitMethod, traitScope)
	implParams, implReturnType := signature(implMethod, implScope)

	// Check each parameter type
	for i := range implParams {
		if !implParams[i].Equals(traitParams[i]) {
			c.errorfWithSuggestion(
				implMethod.Params[i].Token(),
				ErrSignatureMismatch,
				fmt.Sprintf("Parameter type should be %s to match trait '%s'", describe(traitParams[i]), traitName),
				"parameter '%s' has type %s, trait requires %s",
				implMethod.Params[i].Name,
				implParams[i],
				traitParams[i],
			)
		}
	}

	// Check return type
	if !implReturnType.Equals(traitReturnType) {
		c.errorfWithSuggestion(
			implMethod.Token(),
			ErrSignatureMismatch,
			fmt.Sprintf("Return type should be %s to match trait '%s'", describe(traitReturnType), traitName),
			"method '%s' returns %s, trait requires %s",
			implMethod.Name,
			implReturnType,
			traitReturnType,
		)
	}
}

// typeParamList renders type parameters as written: "<T, U>", or "".
func typeParamList(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "<" + strings.Join(names, ", ") + ">"
}

func (c *Checker) checkStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		c.checkBlockStmt(s)
	case *ast.LetStmt:
		c.checkLetStmt(s)
	case *ast.ReturnStmt:
		c.checkReturnStmt(s)
	case *ast.IfStmt:
		c.checkIfStmt(s)
	case *ast.WhileStmt:
		c.checkWhileStmt(s)
	case *ast.ForStmt:
		c.checkForStmt(s)
	case *ast.ExprStmt:
		t := c.checkExpr(s.Expr)
		c.checkWaited(s.Expr, t)
		if s.Stage != "" {
			c.noteStage(s)
		}
	case *ast.MatchStmt:
		c.checkMatchStmt(s)
	case *ast.DeferStmt:
		c.checkDeferStmt(s)
	case *ast.BreakStmt, *ast.ContinueStmt:
		c.checkLeavesDefer(s)
	}
}

func (c *Checker) checkBlockStmt(s *ast.BlockStmt) {
	c.checkBlockEnding(s)
}

// checkBlockEnding checks a block, and returns what is known about
// variables at its end, before that is forgotten: for the branches of an
// if, where what every branch ends with still holds after it.
func (c *Checker) checkBlockEnding(s *ast.BlockStmt) (ending map[*resolve.Symbol]Type) {
	// What a block learns about variables ends with the block
	state := c.flowSnapshot()
	defer func() {
		ending = copyVarFacts(c.narrowed)
		c.flowRestore(state)
	}()

	warned := false
	savedRest := c.rest
	defer func() { c.rest = savedRest }()
	for i, stmt := range s.Stmts {
		// What comes after, where a variable made of a generic call that
		// says nothing of its types may show them by how it is used
		c.rest = s.Stmts[i+1:]
		c.checkStmt(stmt)

		// Nothing after a statement that always leaves the block can run.
		// Reported once per block; the rest is still checked.
		if !warned && i+1 < len(s.Stmts) && (exits(stmt) || alwaysReturns(stmt)) {
			warned = true
			c.errs.ReportWarningFromToken(ErrUnreachableCode,
				"unreachable code: the statement before it always leaves this block",
				s.Stmts[i+1].Token(), c.sourceLines)
		}
	}
	return nil
}

func (c *Checker) checkLetStmt(s *ast.LetStmt) {
	if call, ok := s.Value.(*ast.CallExpr); ok && s.Type == nil {
		// let xs = none(); then total(xs): what none gives is what total
		// takes, if nothing else says
		saved := c.letCall
		c.letCall = &pendingLet{let: s, call: call, rest: c.rest}
		defer func() { c.letCall = saved }()
	}
	if fn, ok := s.Value.(*ast.FnExpr); ok && !fn.Short && s.Type == nil {
		// let walk = fn(node: Node) { ... walk(child) ... }: what the body
		// calls has the type the signature says
		c.info.Types[s.Value] = c.resolveFnType(&ast.FnDecl{BaseNode: fn.BaseNode, Params: fn.Params, ReturnType: fn.ReturnType})
	}
	if s.Type == nil {
		c.checkExpr(s.Value)

		// Nothing says what an empty literal holds, and a variable of
		// unknown element type would accept and yield anything
		if lit, ok := s.Value.(*ast.ArrayLiteralExpr); ok && len(lit.Elements) == 0 {
			c.errorfWithSuggestion(s.Value.Token(), ErrUntypedEmptyArray,
				fmt.Sprintf("Give the variable a type: let %s: [Int] = [];", s.Name),
				"cannot infer the element type of '[]'")
		}
		return
	}

	declType := c.resolveType(s.Type)
	asTemplate(&s.Value, declType)
	valType := c.checkExprAs(s.Value, declType)
	if valType != nil && !c.assignable(declType, valType) && !c.refusedAsTemplate(s.Value, declType, valType, ErrTypeMismatch) {
		c.errorfWithSuggestion(s.Value.Token(), ErrTypeMismatch, c.arrayAdvice(declType, valType, fmt.Sprintf("Ensure the value is of type %s", describe(declType))), "cannot assign type %s to %s", valType, declType)
	}
}

func (c *Checker) checkReturnStmt(s *ast.ReturnStmt) {
	if c.inDefer {
		c.errorfWithSuggestion(s.Token(), ErrBadDefer,
			"A defer runs while its block is being left and cannot decide where to go; set a variable, or do the work before leaving",
			"'return' inside a defer")
	}
	if c.currentFn == nil {
		c.errorf(s.Token(), ErrReturnOutsideFn, "return statement outside of function")
		return
	}

	var returnType Type = Void
	if c.currentFn.ReturnType != nil {
		returnType = c.resolveType(c.currentFn.ReturnType)
	}

	if s.Value != nil {
		asTemplate(&s.Value, returnType)
		valType := c.checkExprAs(s.Value, returnType)
		if valType != nil {
			if !c.assignable(returnType, valType) && !c.refusedAsTemplate(s.Value, returnType, valType, ErrReturnMismatch) {
				c.errorfWithSuggestion(s.Value.Token(), ErrReturnMismatch, c.arrayAdvice(returnType, valType, fmt.Sprintf("Ensure the return value is of type %s", describe(returnType))), "cannot return type %s from function returning %s", valType, returnType)
			}
		}
	} else {
		if !returnType.Equals(Void) {
			c.errorfWithSuggestion(s.Token(), ErrMissingReturn, fmt.Sprintf("Add a return statement with a value of type %s", describe(returnType)), "missing return value in function returning %s", returnType)
		}
	}
}

func (c *Checker) checkIfStmt(s *ast.IfStmt) {
	condType := c.checkExpr(s.Cond)
	if condType != nil && !condType.Equals(Bool) && !isUnknown(condType) {
		c.errorfWithSuggestion(s.Cond.Token(), ErrInvalidCondition, "Condition must evaluate to a boolean", "condition must be Bool, got %s", condType)
	}

	whenTrue := c.factsFrom(s.Cond, true)
	whenFalse := c.factsFrom(s.Cond, false)
	c.recordNarrowedNames(whenTrue)
	before := c.flowSnapshot()

	branches := 1
	if s.Else != nil {
		branches = 2
	}
	thenExits := exits(s.Then)
	elseExits := s.Else != nil && exits(s.Else)

	// What is known about variables at the end of each way through the
	// if that carries on after it
	var ends []map[*resolve.Symbol]Type
	c.withBranches(branches, func(i int) facts {
		if i == 0 {
			return whenTrue
		}
		return whenFalse
	}, func(i int) {
		ending := c.narrowed
		if i == 0 {
			ending = c.checkBlockEnding(s.Then)
		} else if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
			ending = c.checkBlockEnding(elseBlock)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			c.checkIfStmt(elseIf)
			ending = c.narrowed
		}
		if (i == 0 && !thenExits) || (i == 1 && !elseExits) {
			ends = append(ends, copyVarFacts(ending))
		}
	})
	if s.Else == nil {
		// Without an else, the condition was false
		skipped := copyVarFacts(before.facts.vars)
		for sym, typ := range whenFalse.vars {
			skipped[sym] = typ
		}
		ends = append(ends, skipped)
	}

	// When one branch never continues, the code after the if is only
	// reached through the other one: `if x == null { return; }` leaves x
	// non-null from here on.
	if thenExits && !elseExits {
		c.narrowAfter(whenFalse, before)
	} else if elseExits && !thenExits {
		c.narrowAfter(whenTrue, before)
	}

	// What every way through says still holds: after
	// `if x == null { x = load(); }`, x is not null
	c.narrowToCommon(ends)
}

func copyVarFacts(vars map[*resolve.Symbol]Type) map[*resolve.Symbol]Type {
	copied := make(map[*resolve.Symbol]Type, len(vars))
	for sym, typ := range vars {
		copied[sym] = typ
	}
	return copied
}

// narrowToCommon adds what each of the ways that meet here knows about a
// variable, when they all know the same.
func (c *Checker) narrowToCommon(ends []map[*resolve.Symbol]Type) {
	if len(ends) == 0 {
		return
	}
	for sym, typ := range ends[0] {
		common := true
		for _, other := range ends[1:] {
			if known, ok := other[sym]; !ok || !known.Equals(typ) {
				common = false
				break
			}
		}
		if common {
			c.narrowed[sym] = typ
		}
	}
}

func (c *Checker) checkWhileStmt(s *ast.WhileStmt) {
	condType := c.checkExpr(s.Cond)
	if condType != nil && !condType.Equals(Bool) && !isUnknown(condType) {
		c.errorfWithSuggestion(s.Cond.Token(), ErrInvalidCondition, "Condition must evaluate to a boolean", "condition must be Bool, got %s", condType)
	}

	// The body runs again after its own assignments, so only the condition,
	// which is tested before every iteration, can be relied on
	c.forgetAssignedIn(s.Body)
	c.loopsInDefer++
	whenTrue := c.factsFrom(s.Cond, true)
	c.recordNarrowedNames(whenTrue)
	c.withNarrowing(whenTrue, func() { c.checkBlockStmt(s.Body) })
	c.loopsInDefer--
}

func (c *Checker) checkForStmt(s *ast.ForStmt) {
	c.loopsInDefer++
	defer func() { c.loopsInDefer-- }()

	// for i in 0..10: the numbers themselves, no array is made
	if r, ok := s.Iterable.(*ast.RangeExpr); ok {
		c.checkRangeBounds(r, true)
		c.info.Types[r] = NewArray(Int)
		c.forgetAssignedIn(s.Body)
		c.checkBlockStmt(s.Body)
		return
	}

	// Check that the iterable is actually iterable (array or string)
	iterType := c.checkExpr(s.Iterable)
	if iterType != nil {
		switch t := iterType.(type) {
		case *ArrayType:
			// Valid: can iterate over arrays
			// The loop variable will have the element type
		case *PrimitiveType:
			// Only String is iterable among primitives
			if !t.Equals(String) && !isUnknown(t) {
				c.errorfWithSuggestion(s.Iterable.Token(), ErrNotIterable, "Use an array or string in for loops", "cannot iterate over type %s", iterType)
			}
		default:
			if args, ok := c.traitArgsOf(iterType, TraitIterable); ok && len(args) == 1 {
				// A struct that says what a loop over it goes through
				c.info.Iterations[s] = args[0]
				break
			}
			suggestion := "Use an array or string in for loops"
			if typeKeyOrParam(iterType) != "" {
				suggestion = fmt.Sprintf("Loop over an array it holds, or implement Iterable<...> for %s (method 'iterate'), with: import std.ops.{Iterable};", describe(iterType))
			}
			c.errorfWithSuggestion(s.Iterable.Token(), ErrNotIterable, suggestion, "cannot iterate over type %s", iterType)
		}
	}
	c.forgetAssignedIn(s.Body)
	c.checkBlockStmt(s.Body)
}

func (c *Checker) checkExpr(expr ast.Expr) Type {
	if expr == nil {
		return nil
	}

	var t Type
	switch e := expr.(type) {
	case *ast.Literal:
		t = c.checkLiteral(e)
	case *ast.Ident:
		if e.Name == ast.EnumToFind {
			c.reportAmbiguousVariant(e, "")
			t = Unknown
			break
		}
		t = c.checkIdent(e)
	case *ast.BinaryExpr:
		t = c.checkBinaryExpr(e)
	case *ast.UnaryExpr:
		t = c.checkUnaryExpr(e)
	case *ast.CallExpr:
		t = c.checkCallExpr(e)
	case *ast.StructInitExpr:
		t = c.checkStructInitExpr(e)
	case *ast.ArrayLiteralExpr:
		t = c.checkArrayLiteralExpr(e)
	case *ast.MemberExpr:
		t = c.checkMemberExpr(e)
	case *ast.IndexExpr:
		t = c.checkIndexExpr(e)
	case *ast.FnExpr:
		t = c.checkFnExpr(e)
	case *ast.CastExpr:
		t = c.checkCastExpr(e)
	case *ast.TypeTestExpr:
		t = c.checkTypeTest(e)
	case *ast.VariantExpr:
		t = c.checkVariantExpr(e)
	case *ast.PatternTestExpr:
		t = c.checkPatternTest(e)
	case *ast.VariantTestExpr:
		c.checkExpr(e.Expr)
		t = Bool
	case *ast.VariantFieldExpr:
		t = c.checkVariantFieldExpr(e)
	case *ast.MatchExpr:
		t = c.checkMatch(e.Match, true, nil)
	case *ast.RangeExpr:
		c.checkRangeBounds(e, false)
		c.errorfWithSuggestion(e.Token(), ErrBadRange,
			"Loop over it (for i in 0..10 { ... }), match against it (case 0..10:), or make the array: arrays.range(0, 10)",
			"a range is not a value")
		t = Unknown
	case *ast.BlockExpr:
		t = c.checkBlockExpr(e)
	case *ast.CondExpr:
		t = c.checkCondExpr(e)
	default:
		t = Unknown
	}

	if t != nil {
		c.info.Types[expr] = t
	}
	return t
}

func (c *Checker) checkLiteral(l *ast.Literal) Type {
	switch l.Kind {
	case "int":
		c.checkIntLiteral(l, false)
		return Int
	case "float":
		return Float
	case "string":
		return String
	case "bool":
		return Bool
	case "null":
		return Null
	}
	return Unknown
}

func (c *Checker) checkIdent(id *ast.Ident) Type {
	if sym := c.table.Defs[id]; sym != nil {
		if narrowed, ok := c.narrowed[sym]; ok {
			return narrowed
		}
	}
	return c.declaredIdentType(id)
}

// declaredIdentType returns the type a name was declared with, ignoring
// anything flow typing knows about it. This is the type an assignment to
// the name is checked against.
func (c *Checker) declaredIdentType(id *ast.Ident) Type {
	sym := c.table.Defs[id]
	if sym == nil {
		// The resolver already reported the undefined name
		return Unknown
	}

	switch sym.Kind {
	case resolve.SymbolVar:
		// Find the declaration
		switch decl := sym.Decl.(type) {
		case *ast.LetStmt:
			if decl.Type != nil {
				return c.resolveType(decl.Type)
			}
			if t, ok := c.info.Types[decl.Value]; ok {
				return t
			}
		case *ast.Param:
			return c.resolveType(decl.Type)
		case *ast.ConstDecl:
			return c.constType(decl)
		case *ast.BindingPattern:
			if t, ok := c.bindings[decl]; ok && t != nil {
				return t
			}
		case *ast.ForStmt:
			// Loop variable: element type of the iterable
			switch iter := c.info.Types[decl.Iterable].(type) {
			case *ArrayType:
				return iter.ElementType
			case *PrimitiveType:
				if iter.Equals(String) {
					return String
				}
			}
			if elem, ok := c.info.Iterations[decl]; ok {
				return elem
			}
		case *ast.ForIndex:
			// Position variable of `for i, item in items`
			return Int
		case *ast.BinaryExpr:
			// The name the right side of `value?.member` gives the value:
			// there, it is not null
			if t, ok := c.info.Types[decl.Left]; ok && t != nil {
				if value := withoutNull(t); value != nil {
					return value
				}
			}
			return Unknown
		case *ast.ImplDecl:
			// Implicit `self` of an impl method
			return c.implSelfType(decl)
		case *ast.TraitDecl:
			// ...and of a method a trait gives a body
			return c.traitSelf(decl)
		}
	case resolve.SymbolFunc:
		if fn, ok := sym.Decl.(*ast.FnDecl); ok {
			return c.functionType(fn, id)
		}
	case resolve.SymbolEnum:
		c.errorfWithSuggestion(id.Token(), ErrInvalidEnumUse,
			fmt.Sprintf("Name one of its variants: %s.Variant", id.Name),
			"'%s' is an enum, not a value", id.Name)
	}
	return Unknown
}

// functionType returns the type of a function named by an identifier. For a
// generic function the identifier's type arguments are substituted for the
// type parameters, so `first<Int>` has type fn([int]) -> int.
func (c *Checker) functionType(fn *ast.FnDecl, id *ast.Ident) Type {
	if len(fn.TypeParams) == 0 {
		return c.resolveFnType(fn)
	}

	bindings := make(map[string]Type, len(fn.TypeParams))
	for i, name := range fn.TypeParams {
		if len(id.TypeArgs) == len(fn.TypeParams) {
			bindings[name] = c.resolveType(id.TypeArgs[i])
		} else {
			// Missing or mismatched type arguments are reported by
			// monomorphization; do not add errors about T on top
			bindings[name] = Unknown
		}
	}
	defer c.bind(bindings)()
	return c.resolveFnType(fn)
}

func (c *Checker) checkBinaryExpr(e *ast.BinaryExpr) Type {
	if e.Op == "=" {
		return c.checkAssignment(e)
	}
	if e.Op == ast.SafeAccessOp {
		return c.checkSafeAccess(e)
	}
	if base, ok := ast.CompoundOp(e.Op); ok {
		return c.checkCompoundAssignment(e, base)
	}

	// In a comparison each side says what the other is expected to be, so
	// that `option == Option.None` needs no type written on the variant
	var left Type
	equality := e.Op == "==" || e.Op == "!="
	if equality && c.isVariantRef(variantCallee(e.Left)) && !c.isVariantRef(variantCallee(e.Right)) {
		right := c.checkExpr(e.Right)
		left = c.checkExprAs(e.Left, right)
		if !c.comparable(left, right) {
			c.errorf(e.Token(), ErrInvalidOperation, "operator %s not defined for types %s and %s", e.Op, left, right)
		}
		return Bool
	}
	left = c.checkExpr(e.Left)
	var right Type
	switch e.Op {
	case "&&":
		// The right side only runs when the left one was true
		c.withNarrowing(c.factsFrom(e.Left, true), func() { right = c.checkExpr(e.Right) })
	case "||":
		c.withNarrowing(c.factsFrom(e.Left, false), func() { right = c.checkExpr(e.Right) })
	default:
		if equality {
			right = c.checkExprAs(e.Right, left)
		} else {
			right = c.checkExpr(e.Right)
		}
	}

	if left == nil || right == nil {
		return Unknown
	}

	switch e.Op {

	case "??":
		// `a ?? b` is `a` unless it is null, so the result is a's type
		// without null, and b has to fit it. A fallback that may be null
		// itself gives a result that may be: a ?? b ?? 0 tries a, then b.
		value := withoutNull(left)
		if value == nil || isUnknown(value) {
			return right
		}
		if isOptional(right) || right.Equals(Null) {
			inner := withoutNull(right)
			if inner == nil || right.Equals(Null) {
				return optional(value)
			}
			inner = c.widen(e.Right, value, inner)
			if !c.assignable(value, inner) {
				c.errorf(e.Right.Token(), ErrTypeMismatch, "fallback of type %s does not match %s", right, optional(value))
			}
			return optional(value)
		}
		right = c.widen(e.Right, value, right)
		if !c.assignable(value, right) {
			c.errorf(e.Right.Token(), ErrTypeMismatch, "fallback of type %s does not match %s", right, value)
		}
		return value

	case "==", "!=":
		if !c.comparable(left, right) {
			c.errorf(e.Token(), ErrInvalidOperation, "operator %s not defined for types %s and %s", e.Op, left, right)
		}
		// Two values of a type that says when its values are equal are
		// compared that way; anything else as before
		if typeKeyOrParam(left) != "" && !isUnknown(right) && !right.Equals(Null) {
			if _, ok := c.traitArgsOf(left, TraitEqual); ok && c.assignable(left, right) {
				c.info.OperatorMethods[e] = Operators[e.Op].Method
			}
		}
		return Bool
	}

	// One side could not be typed; its error is already reported
	if isUnknown(left) || isUnknown(right) {
		switch e.Op {
		case "<", "<=", ">", ">=", "&&", "||":
			return Bool
		}
		return Unknown
	}

	// Basic arithmetic
	if left.Equals(Int) && right.Equals(Int) {
		switch e.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>":
			return Int
		case "<", "<=", ">", ">=":
			return Bool
		}
	}

	if left.Equals(Float) && right.Equals(Float) {
		switch e.Op {
		case "+", "-", "*", "/", "%":
			return Float
		case "<", "<=", ">", ">=":
			return Bool
		}
	}

	// A whole number with a Float: worked out as Floats
	if isNumber(left) && isNumber(right) && !left.Equals(right) {
		switch e.Op {
		case "+", "-", "*", "/", "%":
			return Float
		case "<", "<=", ">", ">=":
			return Bool
		}
	}

	if left.Equals(String) && right.Equals(String) {
		switch e.Op {
		case "+":
			return String
		case "<", "<=", ">", ">=":
			return Bool
		}
	}

	if left.Equals(Bool) && right.Equals(Bool) {
		if e.Op == "&&" || e.Op == "||" {
			return Bool
		}
	}

	// A struct with the trait behind the operator
	if result := c.operatorOn(e, left, right); result != nil {
		return result
	}

	for _, side := range []struct {
		typ  Type
		expr ast.Expr
	}{{left, e.Left}, {right, e.Right}} {
		if u, ok := side.typ.(*UnionType); ok && hasNull(u) {
			c.errorfWithSuggestion(side.expr.Token(), ErrPossiblyNull,
				"Test it against null first, or provide a fallback with '??'",
				"operator %s cannot be applied to %s: the value may be null", e.Op, side.typ)
			return Unknown
		}
	}

	if suggestion := missingOperator(e.Op, left); suggestion != "" {
		c.errorfWithSuggestion(e.Token(), ErrInvalidOperation, suggestion, "operator %s not defined for types %s and %s", e.Op, left, right)
		return Unknown
	}
	c.errorf(e.Token(), ErrInvalidOperation, "operator %s not defined for types %s and %s", e.Op, left, right)
	return Unknown
}

// Ints are 48-bit signed: -(2^47) to 2^47 - 1.
const (
	maxInt = int64(1)<<47 - 1
	minInt = -(int64(1) << 47)
)

// checkIntLiteral reports an integer literal that does not fit an Int.
// negated says the literal is the operand of a unary minus, which makes
// the magnitude 2^47 valid.
func (c *Checker) checkIntLiteral(l *ast.Literal, negated bool) {
	limit := uint64(maxInt)
	if negated {
		limit++
	}
	if n, err := strconv.ParseUint(l.Value, 10, 64); err == nil && n <= limit {
		return
	}
	c.errorfWithSuggestion(l.Token(), ErrIntOutOfRange,
		fmt.Sprintf("Ints range from %d to %d; use a Float for larger values", minInt, maxInt),
		"integer literal %s is out of range", l.Value)
}

func (c *Checker) checkUnaryExpr(e *ast.UnaryExpr) Type {
	// The literal of `-140737488355328` is one larger than the largest Int;
	// it is in range only together with its sign
	if lit, ok := e.Expr.(*ast.Literal); ok && lit.Kind == "int" && e.Op == "-" {
		c.checkIntLiteral(lit, true)
		c.info.Types[lit] = Int
		return Int
	}

	if e.Op == ast.TryOp {
		return c.checkTry(e)
	}
	if e.Op == ast.UnwrapOp {
		return c.checkUnwrap(e)
	}

	operand := c.checkExpr(e.Expr)
	if operand == nil || isUnknown(operand) {
		return Unknown
	}

	if e.Op == "-" {
		if operand.Equals(Int) || operand.Equals(Float) {
			return operand
		}
	} else if e.Op == "!" {
		if operand.Equals(Bool) {
			return Bool
		}
	} else if e.Op == "~" {
		if operand.Equals(Int) {
			return Int
		}
	}

	c.errorf(e.Token(), ErrInvalidUnary, "operator %s not defined for type %s", e.Op, operand)
	return Any
}

func (c *Checker) checkCallExpr(e *ast.CallExpr) Type {
	// The expected type is for this call only, not for calls among its
	// arguments
	expected := c.expected
	c.expected = nil

	if !c.waitsOnWaitable(e) {
		return Unknown
	}
	if ident, isIdent := e.Func.(*ast.Ident); isIdent && ident.Name == ast.EntriesFunction {
		if !c.entries(e, expected) {
			return Unknown
		}
	}

	var result Type
	member, ok := e.Func.(*ast.MemberExpr)
	if ok && c.isTypeMethod(member) {
		// Point.norm(p): the method of the type, called on p
		result = c.callValue(e, c.checkMemberExpr(member))
	} else if ok && !c.isVariantRef(member) && c.typeNamed(member.Expr) != nil {
		// Money.one(): the type has no function or method of that name
		c.errorf(member.Token(), ErrUnknownMethod, "%s has no function or method '%s'", describe(c.typeNamed(member.Expr)), member.Name)
		c.checkArgsOnly(e)
		return Unknown
	} else if ok && c.isVariantRef(member) {
		// Shape.Circle(2.0): a value of an enum, not a method call
		return c.checkVariant(member, e, expected)
	} else if ok {
		result = c.checkMethodCall(e, member, expected)
	} else {
		c.inferFunctionTypeArgs(e, expected)
		c.checkCallBounds(e)
		result = c.callValue(e, c.checkExpr(e.Func))
		if c.isBuiltinCall(e, resolve.PushFunction) {
			c.checkPush(e)
		}
	}

	// The callee may have changed a field it can reach
	if c.callMayWriteFields(e) {
		c.forgetFields()
	}
	return result
}

func (c *Checker) checkStructInitExpr(e *ast.StructInitExpr) Type {
	if decl := c.enums[e.Name]; decl != nil {
		c.errorfWithSuggestion(e.Token(), ErrInvalidEnumUse,
			fmt.Sprintf("A value of an enum is made by naming a variant: %s.%s", decl.Name, firstVariant(decl)),
			"'%s' is an enum, not a struct", e.Name)
		for _, value := range e.Fields {
			c.checkExpr(value)
		}
		return Unknown
	}
	var typeArgs []Type
	for _, arg := range e.TypeArgs {
		typeArgs = append(typeArgs, c.resolveType(arg))
	}
	result := &StructType{Name: e.Name, BaseName: e.Name, TypeArgs: typeArgs}

	if e.Range != nil && c.structs[e.Name] != nil {
		// A range kept as a value. It was written as a range, and that
		// is what its ends are checked as
		c.checkRangeBounds(e.Range, true)
		for _, name := range []string{"by", "inclusive"} {
			c.checkExpr(e.Fields[name])
		}
		return result
	}

	// Field initializers are checked in name order, for stable diagnostics
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	decl := c.structs[e.Name]
	if decl == nil {
		for _, name := range names {
			c.checkExpr(e.Fields[name])
		}
		if c.traits[e.Name] != nil {
			c.errorf(e.Token(), ErrUndefinedStruct, "'%s' is a trait and cannot be instantiated", e.Name)
		} else {
			c.errorf(e.Token(), ErrUndefinedStruct, "undefined struct '%s'", e.Name)
		}
		return result
	}

	for _, name := range names {
		value := e.Fields[name]
		fieldType, ok := c.fieldType(result, name)
		if !ok {
			c.checkExpr(value)
			c.errorf(value.Token(), ErrUnknownField, "struct %s has no field '%s'", e.Name, name)
			continue
		}
		c.checkFieldVisible(result, name, value.Token())
		if asTemplate(&value, fieldType) {
			e.Fields[name] = value
		}
		valType := c.checkExprAs(value, fieldType)
		if valType != nil && !c.assignable(fieldType, valType) && !c.refusedAsTemplate(value, fieldType, valType, ErrTypeMismatch) {
			if advice := c.arrayAdvice(fieldType, valType, ""); advice != "" {
				c.errorfWithSuggestion(value.Token(), ErrTypeMismatch, advice, "cannot use type %s for field '%s' of type %s", valType, name, fieldType)
			} else {
				c.errorf(value.Token(), ErrTypeMismatch, "cannot use type %s for field '%s' of type %s", valType, name, fieldType)
			}
		}
	}

	c.requireBounds(decl.TypeParams, decl.Bounds, typeArgs, e.Token(), "struct "+decl.Name)

	var missing, private []string
	for _, field := range decl.Fields {
		if _, given := e.Fields[field.Name]; !given && field.Default == nil {
			missing = append(missing, "'"+field.Name+"'")
			if !c.fieldOpen(decl, field) {
				private = append(private, "'"+field.Name+"'")
			}
		}
	}
	if len(private) > 0 {
		// A field other modules cannot give: the struct is made by its
		// module, by a function it offers
		c.errorfWithSuggestion(e.Token(), ErrPrivateField,
			fmt.Sprintf("Make it with a function of its module, such as a static fn of %s; or give the priv field a value of its own", displayName(e.Name)),
			"struct %s cannot be made here: %s %s private to its module", displayName(e.Name), strings.Join(private, ", "), map[bool]string{true: "is", false: "are"}[len(private) == 1])
	} else if len(missing) > 0 {
		c.errorfWithSuggestion(e.Token(), ErrMissingField, "Every field needs a value; use null for an optional field, or give the field a value of its own in the struct: name: Type = value;",
			"struct literal of %s is missing %s", e.Name, strings.Join(missing, ", "))
	}
	return result
}

// checkMemberExpr types `expr.name` where it is not being called: the
// length of an array or string, or a struct field.
func (c *Checker) checkMemberExpr(e *ast.MemberExpr) Type {
	if decl, _ := c.enumOf(e.Expr); decl != nil && findVariant(decl, e.Name) == nil && c.methodDecl(enumType(decl, nil), e.Name) != nil {
		// Color.name: a method of the enum, not a variant
		return c.methodOfType(e, enumType(decl, nil))
	}
	if c.isVariantRef(e) {
		expected := c.expected
		c.expected = nil
		return c.checkVariant(e, nil, expected)
	}
	if self := c.typeNamed(e.Expr); self != nil {
		return c.methodOfType(e, self)
	}
	recv := c.checkExpr(e.Expr)

	// A field tested against null is known to hold a value
	if key, _, ok := c.fieldPath(e); ok {
		if fact, narrowed := c.narrowedPaths[key]; narrowed {
			return fact.typ
		}
	}

	switch t := recv.(type) {
	case nil:
		return Unknown

	case *ArrayType:
		if e.Name == "length" {
			c.checkLengthMember(e, "arrays.len", "std.arrays")
			return Int
		}

	case *PrimitiveType:
		if isUnknown(t) {
			return Unknown
		}
		if t.Equals(String) && e.Name == "length" {
			c.checkLengthMember(e, "strings.len", "std.strings")
			return Int
		}

	case *StructType:
		// pair.0: a value of a tuple, by its position
		if index, byPosition := ast.TupleIndex(e.Name); byPosition {
			if !ast.IsTupleName(t.BaseName) {
				c.errorfWithSuggestion(e.Token(), ErrUnknownField, "Only the values of a tuple are read by position",
					"%s is not a tuple: it has no value at position %d", t, index)
				return Unknown
			}
			if e.TupleSize != 0 && e.TupleSize != len(t.TypeArgs) {
				// let (a, b) = triple: said once, for the first name
				holder, named := e.Expr.(*ast.Ident)
				if !named || !c.miscounted[holder.Name] {
					c.errorfWithSuggestion(e.Expr.Token(), ErrTypeMismatch,
						"Name every value, with '_' for those that are not needed",
						"%s holds %d values, and %d are named here", t, len(t.TypeArgs), e.TupleSize)
				}
				if named {
					c.miscounted[holder.Name] = true
				}
				return Unknown
			}
			if index >= len(t.TypeArgs) {
				c.errorfWithSuggestion(e.Token(), ErrUnknownField,
					fmt.Sprintf("Positions count from 0: the last one is .%d", len(t.TypeArgs)-1),
					"%s holds %d values: there is none at position %d", t, len(t.TypeArgs), index)
				return Unknown
			}
			return t.TypeArgs[index]
		}
		if decl := c.enums[t.Name]; decl != nil && ast.IsTupleName(decl.Name) {
			c.errorfWithSuggestion(e.Token(), ErrUnknownField,
				"Read a value by its position, as in pair.0, or name them all: let (first, second) = pair;",
				"a tuple has no '%s'", e.Name)
			return Unknown
		}
		if decl := c.methodDecl(t, e.Name); decl != nil && c.enums[t.Name] != nil {
			return c.methodValue(e, t, decl)
		}
		if decl := c.enums[t.Name]; decl != nil {
			c.errorfWithSuggestion(e.Token(), ErrUnknownField,
				fmt.Sprintf("Take the value apart with a match: match value { case %s.%s(...): { ... } }", decl.Name, firstVariant(decl)),
				"a value of enum %s has no fields", t)
			return Unknown
		}
		if c.structs[t.Name] == nil {
			return Unknown
		}
		if fieldType, ok := c.fieldType(t, e.Name); ok {
			c.checkFieldVisible(t, e.Name, e.Token())
			return fieldType
		}
		if decl := c.methodDecl(t, e.Name); decl != nil {
			return c.methodValue(e, t, decl)
		}
		if similar := c.similarMember(t, e.Name); similar != "" {
			c.errorfWithSuggestion(e.Token(), ErrUnknownField, "Did you mean '"+similar+"'?",
				"%s has no field '%s'", t, e.Name)
			return Unknown
		}

	case *UnionType:
		if hasNull(t) {
			c.errorfWithSuggestion(e.Token(), ErrPossiblyNull, "Test it against null first, or provide a fallback with '??'",
				"cannot read '%s' of %s: the value may be null", e.Name, t)
			return Unknown
		}
	}

	c.errorf(e.Token(), ErrUnknownField, "%s has no field '%s'", recv, e.Name)
	return Unknown
}

// checkLengthMember reports `.length` on an array or a text. Only structs
// and enums have members; the length of an array or a text is asked with a
// function of the standard library. It is only recognized to say so: a
// program that comes from a language where it exists is told what to write.
func (c *Checker) checkLengthMember(e *ast.MemberExpr, function, module string) {
	what := "an array"
	if function == "strings.len" {
		what = "a text"
	}
	suggestion := fmt.Sprintf("Call .len() on it, with: import %s;", module)
	if written := asWritten(e.Expr); written != "..." {
		suggestion = fmt.Sprintf("Write %s.len(), with: import %s;", written, module)
	}
	c.errorfWithSuggestion(e.Token(), ErrNoMembers, suggestion,
		"%s has no members: '.length' is not part of the language", what)
}

// asWritten returns an expression as the program has it, for a suggestion
// to copy, or "..." when it is not known any more: names of other modules
// were replaced by their full names when the modules were loaded.
func asWritten(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if !strings.Contains(e.Name, ".") {
			return e.Name
		}
	case *ast.MemberExpr:
		if inner := asWritten(e.Expr); inner != "..." {
			return inner + "." + e.Name
		}
	case *ast.Literal:
		return e.String()
	case *ast.IndexExpr:
		if inner, index := asWritten(e.Expr), asWritten(e.Index); inner != "..." && index != "..." {
			return inner + "[" + index + "]"
		}
	}
	return "..."
}

// checkIndexExpr types `expr[index]`.
func (c *Checker) checkIndexExpr(e *ast.IndexExpr) Type {
	recv := c.checkExpr(e.Expr)

	// A struct that says what its brackets give: the key is what it asks
	// for, not necessarily a number
	if recv != nil && typeKeyOrParam(recv) != "" {
		if args, ok := c.traitArgsOf(recv, TraitIndexed); ok && len(args) == 2 {
			key := c.checkExprAs(e.Index, args[0])
			if key != nil && !c.assignable(args[0], key) {
				c.errorf(e.Index.Token(), ErrTypeMismatch, "%s is indexed by %s, got %s", recv, args[0], key)
			}
			c.info.IndexReads[e] = true
			return args[1]
		}
	}

	index := c.checkExpr(e.Index)
	if IsRange(index) {
		// items[1..3]: the items at those positions, of the same type
		switch t := recv.(type) {
		case *ArrayType:
			return t
		case *PrimitiveType:
			if t.Equals(String) || isUnknown(t) {
				return t
			}
		}
	}
	if index != nil && !index.Equals(Int) && !isUnknown(index) {
		c.errorfWithSuggestion(e.Index.Token(), ErrTypeMismatch, "Use an Int as index", "index must be Int, got %s", index)
	}

	switch t := recv.(type) {
	case nil:
		return Unknown
	case *ArrayType:
		return t.ElementType
	case *PrimitiveType:
		if t.Equals(String) {
			return String
		}
		if isUnknown(t) {
			return Unknown
		}
	case *UnionType:
		if hasNull(t) {
			c.errorfWithSuggestion(e.Token(), ErrPossiblyNull, "Test it against null first, or provide a fallback with '??'",
				"cannot index %s: the value may be null", t)
			return Unknown
		}
	}

	if recv != nil && typeKeyOrParam(recv) != "" {
		c.errorfWithSuggestion(e.Token(), ErrInvalidOperation,
			fmt.Sprintf("Call one of its methods, or implement Indexed<Key, Value> for %s (method 'at'), with: import std.ops.{Indexed};", describe(recv)),
			"type %s cannot be indexed", recv)
		return Unknown
	}
	c.errorf(e.Token(), ErrInvalidOperation, "type %s cannot be indexed", recv)
	return Unknown
}

// checkFieldVisible reports a field of a struct of another module that
// is not pub, used where it is written.
func (c *Checker) checkFieldVisible(st *StructType, name string, at token.Token) {
	decl := c.structs[st.Name]
	if decl == nil {
		return
	}
	field := findField(decl, name)
	if field == nil || c.fieldOpen(decl, field) {
		return
	}
	c.errorfWithSuggestion(at, ErrPrivateField,
		fmt.Sprintf("Use what %s offers for it: its methods, or its static functions", displayName(decl.Name)),
		"field '%s' of %s is private to module '%s'", name, decl.Name, decl.Module)
}

// fieldOpen reports whether the code being checked may use a field: one
// that is not priv, or one of a struct of its own module.
func (c *Checker) fieldOpen(decl *ast.StructDecl, field *ast.Field) bool {
	if !field.Private || c.currentFn == nil {
		return true
	}
	return decl.Module == c.currentFn.Module
}

// RangeStruct is the struct a range kept as a value is.
const RangeStruct = "std.ops.Range"

// IsRange reports whether a type is that of a range kept as a value,
// 1..3, which between the brackets of an array or a text takes a part of it.
func IsRange(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st.Name == RangeStruct
}

// checkFnExpr types an anonymous function and checks its body.
func (c *Checker) checkFnExpr(e *ast.FnExpr) Type {
	if e.Short && !e.Typed && len(e.Params) == 0 {
		// () -> now() needs nothing from outside: its body says what it
		// gives
		result := c.shortFnResult(e, nil)
		if result == nil {
			result = Unknown
		}
		c.annotateShortFn(e, nil, result)
	}
	if !shortFnIsTyped(e) {
		// item -> item.name, where no function type is expected
		c.errorfWithSuggestion(e.Token(), ErrShortFn,
			"Use it where a function is expected (an argument, a variable or a field of a function type), or write the types: fn(name: Type) -> Type { ... }",
			"nothing here says what the parameters of this function are")
		return Unknown
	}
	// Reuse the FnDecl machinery so `return` is checked against the lambda
	fn := &ast.FnDecl{BaseNode: e.BaseNode, Params: e.Params, ReturnType: e.ReturnType, Body: e.Body}
	if c.currentFn != nil {
		// A lambda belongs to the module of the function it is written in
		fn.Module = c.currentFn.Module
	}
	c.enteringFnExpr = true
	c.checkFnDecl(fn)
	c.recordSignature(e, fn)
	return c.resolveFnType(fn)
}

func findField(decl *ast.StructDecl, name string) *ast.Field {
	for _, field := range decl.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func (c *Checker) checkArrayLiteralExpr(e *ast.ArrayLiteralExpr) Type {
	// Check all elements and infer the array type
	if len(e.Elements) == 0 {
		// Nothing constrains the element type yet
		return NewArray(Unknown)
	}

	// The items in order, but for a variant written alone that several
	// enums have: [Circle(1.0), Empty] is an array of what the others are
	types := make([]Type, len(e.Elements))
	var known Type
	for i, elem := range e.Elements {
		if variantToFind(elem) != nil {
			continue
		}
		types[i] = c.checkExpr(elem)
		if known == nil && types[i] != nil && !isUnknown(types[i]) {
			known = types[i]
		}
	}
	for i, elem := range e.Elements {
		if variantToFind(elem) != nil {
			types[i] = c.checkExprAs(elem, known)
		}
	}

	// The first element determines the array type
	firstType := types[0]
	if firstType == nil {
		return NewArray(Unknown)
	}
	if mixesNumbers(types) {
		// [1, 2.5]: whole numbers among Floats are Floats
		for i, elem := range e.Elements {
			c.widen(elem, Float, types[i])
		}
		return NewArray(Float)
	}
	for i := 1; i < len(e.Elements); i++ {
		elemType := types[i]
		if elemType != nil && !c.assignable(firstType, elemType) {
			c.errorfWithSuggestion(
				e.Elements[i].Token(),
				ErrTypeMismatch,
				fmt.Sprintf("All array elements should be of type %s", describe(firstType)),
				"array element has type %s, expected %s",
				elemType,
				firstType,
			)
		}
	}

	return NewArray(firstType)
}

// mixesNumbers reports whether some types are all numbers, and not all
// of one kind: whole numbers and Floats together.
func mixesNumbers(types []Type) bool {
	ints, floats := false, false
	for _, t := range types {
		switch {
		case t != nil && t.Equals(Int):
			ints = true
		case t != nil && t.Equals(Float):
			floats = true
		default:
			return false
		}
	}
	return ints && floats
}

// Helpers

func (c *Checker) resolveType(n *ast.Type) Type {
	if n == nil {
		return Void
	}
	switch n.Kind {
	case ast.TypeBasic:
		if bound, inScope := c.typeParams[n.Name]; inScope {
			if bound != nil {
				return bound
			}
			return NewTypeParam(n.Name)
		}

		switch n.Name {
		case "Int", "int":
			return Int
		case "Float", "float":
			return Float
		case "Bool", "bool":
			return Bool
		case "String", "string":
			return String
		case "Void", "void":
			return Void
		case "Any", "any":
			return Any
		case "Null", "null":
			return Null
		case "unknown":
			return Unknown
		case "Self":
			if c.selfType != nil {
				return c.selfType
			}
			return Self
		}

		var typeArgs []Type
		for _, arg := range n.Generics {
			typeArgs = append(typeArgs, c.resolveType(arg))
		}
		if _, isTrait := c.traits[n.Name]; isTrait {
			trait := NewBehavior(n.Name, nil)
			trait.TypeArgs = typeArgs
			return trait
		}
		c.checkTypeBounds(n, typeArgs)
		return &StructType{Name: n.Name, BaseName: n.Name, TypeArgs: typeArgs}

	case ast.TypeArray, ast.TypeVariadic:
		// A variadic parameter is seen as an array inside the function
		elem := c.resolveType(n.ElemType)
		return NewArray(elem)
	case ast.TypeOption:
		return optional(c.resolveType(n.ElemType))
	case ast.TypeFunction:
		var params []Type
		for _, p := range n.ParamTypes {
			params = append(params, c.resolveType(p))
		}
		var ret Type = Void
		if n.ReturnType != nil {
			ret = c.resolveType(n.ReturnType)
		}
		return NewFunction(params, ret)
	case ast.TypeUnion:
		var types []Type
		for _, t := range n.UnionTypes {
			types = append(types, c.resolveType(t))
		}
		return NewUnion(types...)
	}
	return Unknown
}

func (c *Checker) resolveFnType(fn *ast.FnDecl) Type {
	defer c.bindTypeParams(fn.TypeParams)()

	variadic := false
	var params []Type
	for i, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ast.TypeVariadic && i == len(fn.Params)-1 {
			// Callers pass the elements one by one
			variadic = true
			params = append(params, c.resolveType(p.Type.ElemType))
			continue
		}
		params = append(params, c.resolveType(p.Type))
	}

	var ret Type = Void
	if fn.ReturnType != nil {
		ret = c.resolveType(fn.ReturnType)
	}

	if variadic {
		return NewVariadicFunction(params, ret)
	}
	ft := NewFunction(params, ret)
	for i, p := range fn.Params {
		if p.Default != nil {
			// The parameters from the first with a value of its own
			for _, rest := range fn.Params[i:] {
				ft.Defaults = append(ft.Defaults, rest.Default)
			}
			break
		}
	}
	return ft
}

// checkDefaults checks what the parameters with a value of their own are
// when a call leaves them out, against their types.
func (c *Checker) checkDefaults(params []*ast.Param) {
	for _, param := range params {
		if param.Default == nil {
			continue
		}
		want := c.resolveType(param.Type)
		got := c.checkExprAs(param.Default, want)
		if got != nil && want != nil && !c.assignable(want, got) {
			c.errorf(param.Default.Token(), ErrTypeMismatch, "the value of parameter '%s' is %s, and the parameter is %s", param.Name, describe(got), describe(want))
		}
	}
}

// bindTypeParams brings the type parameters of a declaration into scope as
// opaque types, unless a use site already bound them to concrete types. It
// returns a function that restores the previous scope.
func (c *Checker) bindTypeParams(names []string) func() {
	bindings := make(map[string]Type)
	for _, name := range names {
		if _, inScope := c.typeParams[name]; !inScope {
			bindings[name] = nil
		}
	}
	return c.bind(bindings)
}

func (c *Checker) errorf(tok token.Token, code string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, describeAll(args)...)
	if c.alreadySaid(tok, code, message) {
		return
	}
	c.errs.ReportErrorFromToken(code, message, tok, c.sourceLines)
}

func (c *Checker) errorfWithSuggestion(tok token.Token, code string, suggestion string, format string, args ...interface{}) {
	message := fmt.Sprintf(format, describeAll(args)...)
	if c.alreadySaid(tok, code, message) {
		return
	}
	c.errs.ReportErrorWithSuggestion(code, message, suggestion, tok, c.sourceLines)
}

// alreadySaid reports whether the very same thing was said of the very
// same place. An expression can be looked at twice (a function written
// with `->`, once to find what it gives and once as the argument it is):
// what is wrong with it is said once.
func (c *Checker) alreadySaid(tok token.Token, code, message string) bool {
	// What is said while something is only being tried is kept apart
	// (see typesOf): it is not said to the program
	key := fmt.Sprintf("%s:%d:%d:%s:%s", tok.Pos.File, tok.Pos.Line, tok.Pos.Col, code, message)
	if c.said == nil {
		c.said = make(map[string]bool)
	}
	if c.said[key] {
		return true
	}
	c.said[key] = true
	return false
}

// describeAll replaces types among message arguments by their source form.
func describeAll(args []interface{}) []interface{} {
	out := make([]interface{}, len(args))
	for i, arg := range args {
		if t, ok := arg.(Type); ok && t != nil {
			out[i] = describe(t)
		} else {
			out[i] = arg
		}
	}
	return out
}

// noteStage notes the shape of a stage: the declared type of each
// variable in scope, and of each field of the flow.
func (c *Checker) noteStage(s *ast.ExprStmt) {
	shape := map[string]string{}
	for _, sym := range c.table.Stages[s] {
		id := &ast.Ident{Name: sym.Name}
		c.table.Defs[id] = sym
		t := c.declaredIdentType(id)
		delete(c.table.Defs, id)
		if t == nil {
			continue
		}
		shape[sym.Name] = c.shapeOf(t)
		if st, ok := t.(*StructType); ok && sym.Name == "self" {
			// The flow by its name: its fields are each in the shape
			shape[sym.Name] = describe(t)
			if decl := c.structs[st.Name]; decl != nil {
				for _, field := range decl.Fields {
					shape["self."+field.Name] = c.shapeOf(c.resolveType(field.Type))
				}
			}
		}
	}
	c.info.StageTypes[s] = shape
}

// shapeOf is a type as a stage's shape keeps it: as it is written, and,
// for a type made of structs or enums, a hash of their layout after a #:
// Answer#1f2e3d4c5b6a. A struct whose fields changed, or an enum whose
// variants did, is then another shape, though its name is the same.
func (c *Checker) shapeOf(t Type) string {
	written := describe(t)
	layout := c.layout(t, map[string]bool{})
	if layout == written {
		return written
	}
	sum := sha256.Sum256([]byte(layout))
	return written + "#" + hex.EncodeToString(sum[:6])
}

// layout is a type with what it is made of: the fields of a struct, in
// their order, the variants of an enum and what each holds, each of their
// types the same way. A type met again inside itself is its name.
func (c *Checker) layout(t Type, seen map[string]bool) string {
	switch t := t.(type) {
	case *StructType:
		name := describe(t)
		if seen[name] {
			return name
		}
		seen[name] = true
		defer delete(seen, name)
		var parts []string
		if decl := c.structs[t.Name]; decl != nil {
			restore := c.bindStructParams(decl, t.TypeArgs)
			for _, field := range decl.Fields {
				parts = append(parts, field.Name+": "+c.layout(c.resolveType(field.Type), seen))
			}
			restore()
			return name + "{" + strings.Join(parts, ", ") + "}"
		}
		if decl := c.enums[t.Name]; decl != nil {
			for _, variant := range decl.Variants {
				var values []string
				for i, value := range c.variantFields(decl, variant, t.TypeArgs) {
					v := c.layout(value, seen)
					if i < len(variant.Names) {
						v = variant.Names[i] + ": " + v
					}
					values = append(values, v)
				}
				parts = append(parts, variant.Name+"("+strings.Join(values, ", ")+")")
			}
			return name + "{" + strings.Join(parts, " | ") + "}"
		}
		if len(t.TypeArgs) == 0 {
			return name
		}
		for _, arg := range t.TypeArgs {
			parts = append(parts, c.layout(arg, seen))
		}
		return name + "[" + strings.Join(parts, ", ") + "]"
	case *ArrayType:
		inner := c.layout(t.ElementType, seen)
		if inner == describe(t.ElementType) {
			return describe(t)
		}
		return "[" + inner + "]"
	case *UnionType:
		parts := make([]string, len(t.Types))
		changed := false
		for i, member := range t.Types {
			parts[i] = c.layout(member, seen)
			changed = changed || parts[i] != describe(member)
		}
		if !changed {
			return describe(t)
		}
		return strings.Join(parts, " | ")
	}
	return describe(t)
}
