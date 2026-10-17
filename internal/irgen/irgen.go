package irgen

import (
	"sort"
	"strconv"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Error codes for IR generation
const (
	ErrUnsupportedConstruct = "E5001" // Language feature not yet supported in IR
	ErrNotAValue            = "E5002" // Name cannot be used as a value
	ErrInvalidAssignment    = "E5003" // Target of an assignment cannot be written
	ErrLoopControl          = "E5005" // break/continue outside of a loop
)

// CastIntrinsic is the host function that implements `value as Type`. It
// takes the value and a description of the type and returns the value, or
// fails when the value is something else.
const CastIntrinsic = "__cast"

// TypeTestIntrinsic is the host function that implements `value is Type`.
// It takes the value and a description of the type and returns a Bool.
const TypeTestIntrinsic = "__is"

// method describes one impl method of a struct.
type method struct {
	name   string // Name used at call sites: p.name(...)
	irName string // Name of the compiled function: Struct.name
	module string // The module the method is written in

	// forwards names the function a method of the library only hands its
	// receiver and its arguments to: text.trim() is strings.trim(text).
	// A call of the method is compiled as a call of that function.
	forwards string

	// A method reachable through a trait is also stored on each value of
	// the struct, so it can be found when only the trait is known.
	fromTrait bool
}

// IRGenerator converts AST to IR
type IRGenerator struct {
	prog         *ast.Program
	typeInfo     *types.Info
	resolveTable *resolve.Table
	captureInfo  *analysis.CaptureInfo
	errs         *errors.Handler
	sourceLines  map[int]string

	// Output IR program
	irProg *ir.Program

	// Current function being compiled
	currentFunc *FunctionCompiler

	// Lambda counter for generating unique names
	lambdaCount int

	// Top-level functions by name (user declarations and builtins)
	funcs map[string]*ast.FnDecl

	// Methods of each struct, and for each method name the structs having it
	structMethods map[string][]method
	methodOwners  map[string][]string
	structNames   map[string]bool // Every struct the program declares
}

// NewIRGenerator creates a new IR generator
func NewIRGenerator(
	prog *ast.Program,
	typeInfo *types.Info,
	resolveTable *resolve.Table,
	captureInfo *analysis.CaptureInfo,
	errs *errors.Handler,
) *IRGenerator {
	return &IRGenerator{
		prog:          prog,
		typeInfo:      typeInfo,
		resolveTable:  resolveTable,
		captureInfo:   captureInfo,
		errs:          errs,
		sourceLines:   make(map[int]string),
		irProg:        &ir.Program{Functions: make([]*ir.Function, 0)},
		lambdaCount:   0,
		funcs:         make(map[string]*ast.FnDecl),
		structMethods: make(map[string][]method),
		methodOwners:  make(map[string][]string),
		structNames:   make(map[string]bool),
	}
}

// SetSourceLines sets source lines for error reporting
func (g *IRGenerator) SetSourceLines(lines map[int]string) {
	g.sourceLines = lines
}

// Generate converts the entire program to IR
func (g *IRGenerator) Generate() *ir.Program {
	g.collectDecls()

	// Generate IR for each top-level declaration
	for _, decl := range g.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			g.compileFunction(d, d.Name, false)
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				g.compileFunction(m, methodIRName(d.StructName, m.Name), true)
				if d.TraitName == FlowTrait && m.Name == "run" && len(g.irProg.Functions) > 0 {
					// Where an instance of a flow starts, as main is
					// where the program does
					fn := g.irProg.Functions[len(g.irProg.Functions)-1]
					fn.Entry = true
					fn.Inboxes = g.inboxesOf(d.StructName)
					fn.Listens = Listens(m)
				}
				if m.Migrate && len(g.irProg.Functions) > 0 {
					// Run by the runtime of flows, never by the program
					fn := g.irProg.Functions[len(g.irProg.Functions)-1]
					fn.Entry, fn.Migrate = true, true
					for _, param := range m.Params {
						fn.ParamNames = append(fn.ParamNames, param.Name)
					}
				}
			}
		}
	}

	return g.irProg
}

// collectDecls indexes functions, struct fields and methods so that call
// sites can be compiled before the declarations they refer to.
func (g *IRGenerator) collectDecls() {
	for _, fn := range resolve.Builtins() {
		g.funcs[fn.Name] = fn
	}
	for _, fn := range g.prog.Externs {
		g.funcs[fn.Name] = fn
	}

	for _, decl := range g.prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			g.funcs[d.Name] = d
		case *ast.StructDecl:
			g.structNames[d.Name] = true
			for _, field := range d.Fields {
				if field.Private {
					if g.irProg.Private == nil {
						g.irProg.Private = make(map[string][]string)
					}
					g.irProg.Private[d.Name] = append(g.irProg.Private[d.Name], field.Name)
				}
			}
		case *ast.EnumDecl:
			// An enum has methods like a struct; its values name it too
			g.structNames[d.Name] = true
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				if m.Body == nil {
					continue
				}
				g.methodOwners[m.Name] = append(g.methodOwners[m.Name], d.StructName)
				g.structMethods[d.StructName] = append(g.structMethods[d.StructName], method{
					name:      m.Name,
					irName:    methodIRName(d.StructName, m.Name),
					module:    m.Module,
					forwards:  ast.ForwardedTo(m),
					fromTrait: d.TraitName != "",
				})
			}
		}
	}

	for _, methods := range g.structMethods {
		sort.Slice(methods, func(i, j int) bool { return methods[i].name < methods[j].name })
	}

	// Which function is which method of which type, for the calls that
	// only find out when the program runs what they are calling
	owners := make([]string, 0, len(g.structMethods))
	for owner := range g.structMethods {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		for _, m := range g.structMethods[owner] {
			g.irProg.Methods = append(g.irProg.Methods, ir.Method{Type: owner, Name: m.name, Func: m.irName})
		}
	}
}

// FlowTrait is what a struct implements to be a flow (see docs/flow.md),
// and SettledEnum what a step that is never run twice returns.
const (
	FlowTrait   = "std.flow.Flow"
	SettledEnum = "std.flow.Settled"
)

func methodIRName(structName, methodName string) string {
	return structName + "." + methodName
}

// hasMethod reports whether a struct has an impl method of that name.
func (g *IRGenerator) hasMethod(structName, name string) bool {
	for _, m := range g.structMethods[structName] {
		if m.name == name {
			return true
		}
	}
	return false
}

// onlyArraysWith returns the one impl for the arrays of a type that has a
// method of that name, or "" when none or several do; those of whole
// numbers when they are among several.
func (g *IRGenerator) onlyArraysWith(name string) string {
	if g.hasMethod(ast.ArrayImplOf("Int"), name) {
		// As the checker takes it for [].sum(): those of whole numbers
		// first, when others have it too
		return ast.ArrayImplOf("Int")
	}
	found := ""
	for owner := range g.structMethods {
		if item, arrays := ast.IsArrayImpl(owner); !arrays || item == "" || !g.hasMethod(owner, name) {
			continue
		}
		if found != "" {
			return ""
		}
		found = owner
	}
	return found
}

// forwarded returns the function a method of a type forwards to, or "".
func (g *IRGenerator) forwarded(structName, name string) string {
	for _, m := range g.structMethods[structName] {
		if m.name == name {
			return m.forwards
		}
	}
	return ""
}

// isLengthMethod reports whether a method is the library's `len` of
// arrays or of texts: items.len() and text.len(), which are one
// instruction and not a call.
func (g *IRGenerator) isLengthMethod(structName, name string) bool {
	if name != "len" {
		return false
	}
	for _, m := range g.structMethods[structName] {
		if m.name == name {
			return (structName == ast.ArrayImpl && m.module == "std.arrays") || (structName == "String" && m.module == "std.strings")
		}
	}
	return false
}

// dispatch says how a call `receiver.name(...)` reaches its target.
type dispatch int

const (
	dispatchField   dispatch = iota // Not a method: call the function stored in the field
	dispatchStatic                  // The struct is known: call its function directly
	dispatchDynamic                 // Only a trait is known: look the method up on the value
)

// resolveMethod decides how `receiver.name(...)` is called, from the static
// type of the receiver. For a static call it also returns the struct.
func (g *IRGenerator) resolveMethod(receiver ast.Expr, name string) (dispatch, string) {
	recvType := g.typeInfo.Types[receiver]
	if types.IsOptional(recvType) && g.hasMethod(ast.OptionalImpl, name) {
		// A method every optional has: impl<T> T? { ... }
		return dispatchStatic, ast.OptionalImpl
	}
	if union, ok := recvType.(*types.UnionType); ok {
		// An optional receiver: dispatch on what it is when it is not null
		var value types.Type
		for _, member := range union.Types {
			if !member.Equals(types.Null) {
				if value != nil {
					value = nil
					break
				}
				value = member
			}
		}
		recvType = value
	}

	switch t := recvType.(type) {
	case *types.StructType:
		if g.hasMethod(t.Name, name) {
			return dispatchStatic, t.Name
		}
		if g.structNames[t.Name] {
			return dispatchField, ""
		}
		// Not a struct of the program: the name of a type parameter that
		// was not replaced, as for the result of a generic method called
		// on a value whose type arguments are no longer known here
		// (`outer.get().get()` with outer a Box<Box<Int>>). Treated like
		// any receiver of unknown type, below.
	case *types.BehaviorType, *types.TypeParam:
		// Only what the value implements is known: a trait, or what a
		// type parameter is required to
		return dispatchDynamic, ""
	case *types.PrimitiveType:
		// A method a trait gives numbers or texts
		if key := builtinTypeName(t); key != "" && g.hasMethod(key, name) {
			return dispatchStatic, key
		}
	case *types.ArrayType:
		// A method an impl gives arrays: those of its items first
		if item := types.ImplKey(t.ElementType); item != "" && item != ast.ArrayImpl && g.hasMethod(ast.ArrayImplOf(item), name) {
			return dispatchStatic, ast.ArrayImplOf(item)
		}
		if g.hasMethod(ast.ArrayImpl, name) {
			return dispatchStatic, ast.ArrayImpl
		}
		// What the array holds is not known here any more (a type
		// parameter of a generic struct, in one of its methods). The
		// checker knew, and accepted the call: when only the arrays of
		// one type have a method of that name, it is that one.
		if owner := g.onlyArraysWith(name); owner != "" {
			return dispatchStatic, owner
		}
	}

	// The receiver's type is not known (its elements came from an untyped
	// `[]`, for instance). The name still identifies the method when only
	// one struct defines it. A number or a text cannot be asked for its
	// methods, so when one of those has the method too it is found by
	// looking at the value (see compileMethodCall).
	switch owners := g.methodOwners[name]; len(owners) {
	case 0:
		return dispatchField, ""
	case 1:
		if !types.IsBuiltinTypeName(owners[0]) {
			return dispatchStatic, owners[0]
		}
	}
	return dispatchDynamic, ""
}

// builtinTypeName is the name impls give a builtin type, or "".
func builtinTypeName(t *types.PrimitiveType) string {
	switch t.Name {
	case types.PrimitiveInt:
		return "Int"
	case types.PrimitiveFloat:
		return "Float"
	case types.PrimitiveString:
		return "String"
	case types.PrimitiveBool:
		return "Bool"
	}
	return ""
}

// builtinOwners lists the builtin types that have a method of that name,
// in a fixed order.
func (g *IRGenerator) builtinOwners(name string) []string {
	var owners []string
	for _, builtin := range []string{"Int", "Float", "String", "Bool", ast.ArrayImpl} {
		if g.hasMethod(builtin, name) {
			owners = append(owners, builtin)
		}
	}
	// A method only the arrays of one type have, asked of a value that
	// is only known to be an array when the program runs
	if !g.hasMethod(ast.ArrayImpl, name) {
		if owner := g.onlyArraysWith(name); owner != "" {
			owners = append(owners, owner)
		}
	}
	return owners
}

// errorAt reports an IR generation error at the given node.
func (g *IRGenerator) errorAt(node ast.Node, code, msg, suggestion string) {
	if suggestion != "" {
		g.errs.ReportErrorWithSuggestion(code, msg, suggestion, node.Token(), g.sourceLines)
		return
	}
	g.errs.ReportErrorFromToken(code, msg, node.Token(), g.sourceLines)
}

// Listens says whether the run of a flow waits for its cancellation:
// whether it calls flow.cancelled(), anywhere in it.
func Listens(run *ast.FnDecl) bool {
	found := false
	if run.Body != nil {
		ast.Walk(run.Body, func(expr ast.Expr) {
			if call, ok := expr.(*ast.CallExpr); ok {
				if id, ok := call.Func.(*ast.Ident); ok && id.Name == "std.flow.cancelled" {
					found = true
				}
			}
		}, func(ast.Stmt) {})
	}
	return found
}

// isStruct says whether a name is a struct of the program.
func (g *IRGenerator) isStruct(name string) bool {
	for _, decl := range g.prog.Decls {
		if s, ok := decl.(*ast.StructDecl); ok && s.Name == name {
			return true
		}
	}
	return false
}

// inboxesOf are the fields of a flow that are inboxes, Inbox<T> of
// std.flow, with the capacity their default gives: flow.inbox<T>(8).
func (g *IRGenerator) inboxesOf(flow string) []ir.Inbox {
	var inboxes []ir.Inbox
	for _, decl := range g.prog.Decls {
		s, ok := decl.(*ast.StructDecl)
		if !ok || s.Name != flow {
			continue
		}
		for _, field := range s.Fields {
			if field.Type == nil || (field.Type.Name != "Inbox" && field.Type.Name != "std.flow.Inbox") {
				continue
			}
			capacity := 1
			if call, ok := field.Default.(*ast.CallExpr); ok && len(call.Args) == 1 {
				if lit, ok := call.Args[0].(*ast.Literal); ok && lit.Kind == "int" {
					if n, err := strconv.Atoi(lit.Value); err == nil && n > 0 {
						capacity = n
					}
				}
			}
			inbox := ir.Inbox{Name: field.Name, Capacity: capacity}
			if len(field.Type.Generics) == 1 && g.isStruct(field.Type.Generics[0].Name) {
				inbox.Message = field.Type.Generics[0].Name
			}
			inboxes = append(inboxes, inbox)
		}
	}
	return inboxes
}
