package irgen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/ir"
)

// FunctionCompiler compiles a single function
type FunctionCompiler struct {
	gen      *IRGenerator
	function ast.Node // FnDecl or FnExpr
	builder  *ir.Builder

	// Variable to register mapping, one map per lexical block (innermost last)
	scopes []map[string]*ir.Register

	// Registers that hold a named variable. Expression results that are not
	// in this set are temporaries and can be adopted by a `let`.
	varRegs map[int]bool

	// Upvalue mapping (for closures)
	upvalues map[string]int // variable name -> upvalue index

	// Loop context stack (for break/continue)
	loopStack []*loopContext

	// The defers of each open scope, in the order they were written; one
	// list per entry of scopes (see defer.go)
	defers [][]*deferred

	// Blocks started after a return, break or continue: nothing reaches them
	dead map[*ir.BasicBlock]bool
}

// loopContext tracks loop blocks for break/continue
type loopContext struct {
	breakTarget    *ir.BasicBlock
	continueTarget *ir.BasicBlock

	// Number of scopes open when the loop started: a variable in a scope at
	// this depth or deeper is declared inside the loop body.
	scopeDepth int

	// Registers of variables declared in the body that a closure captured.
	// They are detached from those closures when an iteration ends.
	captured []*ir.Register
}

// newLoop starts the context of a loop about to be compiled.
func (fc *FunctionCompiler) newLoop(breakTarget, continueTarget *ir.BasicBlock) *loopContext {
	ctx := &loopContext{
		breakTarget:    breakTarget,
		continueTarget: continueTarget,
		scopeDepth:     len(fc.scopes),
	}
	fc.loopStack = append(fc.loopStack, ctx)
	return ctx
}

// noteCapture records that a closure captured the variable in reg, declared
// at the given scope depth, with every enclosing loop whose body declares it.
func (fc *FunctionCompiler) noteCapture(reg *ir.Register, depth int) {
	for _, ctx := range fc.loopStack {
		if depth < ctx.scopeDepth {
			continue
		}
		known := false
		for _, r := range ctx.captured {
			known = known || r.ID == reg.ID
		}
		if !known {
			ctx.captured = append(ctx.captured, reg)
		}
	}
}

// endIteration emits what has to happen when an iteration of a loop ends,
// before jumping back: the closures created in it stop following the body's
// variables. The next iteration reuses those registers; without this, every
// closure made in the loop would end up seeing the last iteration's values.
func (fc *FunctionCompiler) endIteration(ctx *loopContext) {
	if len(ctx.captured) == 0 {
		return
	}
	operands := make([]ir.Value, len(ctx.captured))
	for i, reg := range ctx.captured {
		operands[i] = reg
	}
	fc.builder.Emit(ir.OpCloseUpvalues, nil, operands...)
}

func (g *IRGenerator) newFunctionCompiler(fn ast.Node, name string) *FunctionCompiler {
	return &FunctionCompiler{
		gen:       g,
		function:  fn,
		builder:   ir.NewBuilder(name),
		scopes:    []map[string]*ir.Register{make(map[string]*ir.Register)},
		varRegs:   make(map[int]bool),
		upvalues:  make(map[string]int),
		loopStack: make([]*loopContext, 0),
		defers:    make([][]*deferred, 1),
		dead:      make(map[*ir.BasicBlock]bool),
	}
}

// compileFunction compiles a function declaration under the given IR name.
// Methods take the receiver as an implicit first parameter named `self`.
func (g *IRGenerator) compileFunction(fn *ast.FnDecl, name string, isMethod bool) {
	if fn.Body == nil {
		return // External function declaration
	}

	fc := g.newFunctionCompiler(fn, name)
	fc.builder.Func.Step = fn.Step
	fc.builder.Func.ByQueue = fn.Queue != ""
	// A step of the application is in the bytecode whether the program
	// calls it or not: its shape, from its signature, is what the
	// application is written against (qoqi gen, the flow.lock)
	fc.builder.Func.Entry = fn.Queue != ""
	fc.builder.Func.Once = fn.Step && fn.ReturnType != nil && fn.ReturnType.Name == SettledEnum
	if isMethod {
		fc.declare("self", fc.builder.AddParam())
	} else {
		fc.builder.Func.Signature = g.typeInfo.Signatures[fn]
	}
	fc.compileBody(fn.Params, fn.Body)
}

// compileBody compiles parameters and body, then adds the finished function
// to the program. Nested lambdas are added before their enclosing function.
func (fc *FunctionCompiler) compileBody(params []*ast.Param, body *ast.BlockStmt) *ir.Function {
	g := fc.gen
	parent := g.currentFunc
	g.currentFunc = fc
	defer func() { g.currentFunc = parent }()

	fc.setPos(fc.function)

	// Allocate registers for parameters
	for _, param := range params {
		fc.declare(param.Name, fc.builder.AddParam())
	}

	// Check if this function captures variables
	if captures := g.captureInfo.GetCaptures(fc.function); captures != nil && len(captures.CapturedVars) > 0 {
		fc.setupUpvalues(captures)
	}

	// Compile function body
	fc.compileBlock(body)

	// Add implicit return if needed
	fc.addImplicitReturn()

	// Add to program
	g.irProg.Functions = append(g.irProg.Functions, fc.builder.Func)
	return fc.builder.Func
}

// setupUpvalues creates the upvalue mapping from capture info
func (fc *FunctionCompiler) setupUpvalues(captures *analysis.FunctionCaptures) {
	for i, cv := range captures.CapturedVars {
		fc.upvalues[cv.Name] = i
	}
	fc.builder.Func.UpvalueCount = len(captures.CapturedVars)
}

// addImplicitReturn adds a void return if the function doesn't end with one
func (fc *FunctionCompiler) addImplicitReturn() {
	if fc.blockEndsWithTerminator() {
		return
	}

	// Add void return
	fc.builder.CreateRet(nil)
}

// --- Scopes ---

func (fc *FunctionCompiler) pushScope() {
	fc.scopes = append(fc.scopes, make(map[string]*ir.Register))
	fc.defers = append(fc.defers, nil)
}

// popScope closes the innermost scope. Reaching the end of a scope is one
// of the ways of leaving it: what it deferred runs here.
func (fc *FunctionCompiler) popScope() {
	if !fc.unreachable() {
		fc.runDefers(len(fc.scopes) - 1)
	}
	fc.scopes = fc.scopes[:len(fc.scopes)-1]
	fc.defers = fc.defers[:len(fc.defers)-1]
}

// declare binds a variable name to a register in the innermost scope.
func (fc *FunctionCompiler) declare(name string, reg *ir.Register) {
	fc.scopes[len(fc.scopes)-1][name] = reg
	fc.varRegs[reg.ID] = true
}

// lookup finds the register of a local variable, innermost scope first.
func (fc *FunctionCompiler) lookup(name string) (*ir.Register, bool) {
	reg, _, ok := fc.lookupScope(name)
	return reg, ok
}

// lookupScope is lookup that also says how deep the declaring scope is.
func (fc *FunctionCompiler) lookupScope(name string) (*ir.Register, int, bool) {
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		if reg, ok := fc.scopes[i][name]; ok {
			return reg, i, true
		}
	}
	return nil, 0, false
}

// setPos records the source position for the instructions emitted next.
func (fc *FunctionCompiler) setPos(node ast.Node) {
	if node == nil {
		return
	}
	if pos := node.Position(); pos.Line > 0 {
		fc.builder.Pos = pos
	}
}

// startDeadBlock switches to a fresh block after a terminator, so code that
// follows a return/break/continue does not land in a finished block.
func (fc *FunctionCompiler) startDeadBlock() {
	block := fc.builder.NewBlock()
	fc.dead[block] = true
	fc.builder.SetBlock(block)
}

// unreachable reports whether the code being emitted can never run: it
// follows a return, break or continue, or the block is already finished.
func (fc *FunctionCompiler) unreachable() bool {
	return fc.dead[fc.builder.CurrentBlock] || fc.blockEndsWithTerminator()
}

// compileStage notes, after the call that passes a stage, the variables
// alive there: every one in scope, the hidden ones of the loops it is in
// named after their loop, so that a change elsewhere in the function
// does not rename them (see docs/flow.md, "New versions of the code").
func (fc *FunctionCompiler) compileStage(stmt *ast.ExprStmt) {
	name := stmt.Stage
	types := fc.gen.typeInfo.StageTypes[stmt]
	shape := map[string]string{}
	for variable, t := range types {
		if strings.HasPrefix(variable, "self.") {
			shape[variable] = t
		}
	}
	seen := map[string]bool{}
	type variable struct {
		name string
		reg  *ir.Register
	}
	var visible [][]variable
	for i := len(fc.scopes) - 1; i >= 0; i-- {
		var scope []variable
		for n, reg := range fc.scopes[i] {
			if !seen[n] {
				seen[n] = true
				scope = append(scope, variable{n, reg})
			}
		}
		visible = append([][]variable{scope}, visible...)
	}

	operands := []ir.Value{ir.Constant{Val: name, Type: "String"}, ir.Constant{Val: shape, Type: "Shape"}}
	counts := map[string]int{}
	for _, scope := range visible {
		var named, hidden []variable
		for _, v := range scope {
			if strings.HasPrefix(v.name, "__") {
				hidden = append(hidden, v)
			} else {
				named = append(named, v)
			}
		}
		sort.Slice(named, func(i, j int) bool { return named[i].name < named[j].name })
		// In the order the loops made them
		sort.Slice(hidden, func(i, j int) bool { return hiddenOrder(hidden[i].name) < hiddenOrder(hidden[j].name) })
		for _, v := range named {
			shape[v.name] = types[v.name]
		}
		for _, v := range hidden {
			kind := hiddenKind(v.name)
			counts[kind]++
			original := v.name
			v.name = fmt.Sprintf("%s#%d", kind, counts[kind])
			shape[v.name] = types[original]
			named = append(named, v)
		}
		for _, v := range named {
			operands = append(operands, ir.Constant{Val: v.name, Type: "String"}, v.reg)
		}
	}
	fc.builder.Emit(ir.OpStage, nil, operands...)
}

// hiddenKind names what a variable the compiler made is for.
func hiddenKind(name string) string {
	switch {
	case strings.HasPrefix(name, "__iter_"):
		return "loop.index"
	case strings.HasPrefix(name, "__len_"):
		return "loop.end"
	case strings.HasPrefix(name, "__match_tmp_"):
		return "match"
	}
	return "loop.items"
}

// hiddenOrder is the number a variable the compiler made ends with.
func hiddenOrder(name string) int {
	n, _ := strconv.Atoi(name[strings.LastIndex(name, "_")+1:])
	return n
}
