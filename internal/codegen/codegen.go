// Package codegen lowers Qoqi IR to luvm bytecode.
package codegen

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Error codes for bytecode generation
const (
	ErrTooManyRegisters = "E6001" // Function needs more registers than an instruction can address
	ErrTooManyConstants = "E6002" // Constant pool is full
	ErrTooManyFunctions = "E6003" // Function table is full
	ErrUnsupportedIR    = "E6004" // IR construct with no bytecode equivalent
	ErrUndefinedFunc    = "E6005" // Reference to a function that does not exist
	ErrMissingMain      = "E6006" // Program has no usable entry point
	ErrCallTooLarge     = "E6007" // Call has more arguments than an instruction can encode
)

// Operands are 16 bits wide, which bounds everything they index.
const (
	maxRegisters = vm.MaxOperand + 1
	maxConstants = vm.MaxOperand + 1
	maxFunctions = vm.MaxOperand + 1
)

// EntryPoint is the function the VM starts executing.
const EntryPoint = "main"

// Generator turns an IR program into a bytecode chunk.
type Generator struct {
	prog        *ir.Program
	errs        *errors.Handler
	sourceLines map[int]string

	chunk *vm.Chunk

	// Names of host-provided functions that may be referenced
	externs map[string]bool
	// The functions of C libraries among them, and how each is described

	protoIndex map[string]int // Function name -> prototype index
	// The functions that capture no variable: one value of each is all
	// there ever is, and it can be called by its number
	capturesNothing map[string]bool
	externIndex     map[string]int   // Extern name -> index in chunk.ExternNames
	constIndex      map[vm.Value]int // Constant value -> index in chunk.Constants
	stringIndex     map[string]int   // String -> index in chunk.StringPool

	debugIndex map[place]*vm.DebugInfo // The debug record of each place
}

// NewGenerator creates a bytecode generator. externs lists the host
// functions the program is allowed to reference.
func NewGenerator(prog *ir.Program, externs []string, errs *errors.Handler) *Generator {
	g := &Generator{
		prog:            prog,
		errs:            errs,
		sourceLines:     make(map[int]string),
		chunk:           vm.NewChunk(),
		externs:         make(map[string]bool),
		protoIndex:      make(map[string]int),
		capturesNothing: make(map[string]bool),
		externIndex:     make(map[string]int),
		constIndex:      make(map[vm.Value]int),
		stringIndex:     make(map[string]int),
		debugIndex:      make(map[place]*vm.DebugInfo),
	}
	for _, name := range externs {
		g.externs[name] = true
	}
	return g
}

// SetSourceLines sets source lines for error reporting and debug info.
func (g *Generator) SetSourceLines(lines map[int]string) {
	g.sourceLines = lines
}

// Generate produces the chunk. Problems are reported to the error handler;
// the chunk must not be executed if it recorded any error.
func (g *Generator) Generate() *vm.Chunk {
	if len(g.prog.Functions) > maxFunctions {
		g.report(token.Position{}, ErrTooManyFunctions,
			fmt.Sprintf("program has %d functions, the limit is %d", len(g.prog.Functions), maxFunctions),
			"Split the program or remove unused functions")
		return g.chunk
	}

	// Prototype indices are assigned up front so that functions can refer
	// to ones defined later.
	g.chunk.Private = g.prog.Private

	functions := g.reachableFunctions()
	for i, fn := range functions {
		g.protoIndex[fn.Name] = i
		g.capturesNothing[fn.Name] = fn.UpvalueCount == 0
	}
	g.checkEntryPoint(functions)

	for _, fn := range functions {
		newFuncGen(g, fn).generate()
	}
	return g.chunk
}

// reachableFunctions returns the functions the program can come to run,
// in their original order: main, the functions it refers to, those they
// refer to, and so on. The rest is left out of the bytecode. A program
// that imports a module of the library for one function carries that
// function and what it needs, not the module.
//
// A function is reached through an instruction that names it, to call it
// or to make a value of it (a closure, the function a task runs), or as a
// method found on a value when the program runs (OpGetMethod): a call of
// `area` on a value of some type may reach the method `area` of any type,
// and all of them are kept. The machine finds one more by its name, to
// print a value of a type that implements Show (vm.ShowMethod).
//
// Without a main there is nowhere to start from; everything is kept, and
// the missing entry point is reported.
func (g *Generator) reachableFunctions() []*ir.Function {
	byName := make(map[string]*ir.Function, len(g.prog.Functions))
	for _, fn := range g.prog.Functions {
		byName[fn.Name] = fn
	}
	entry, ok := byName[EntryPoint]
	if !ok {
		return g.prog.Functions
	}

	// The methods of each name: a call that finds its method on a value
	// may reach any of them
	methods := make(map[string][]*ir.Function)
	for _, m := range g.prog.Methods {
		if fn, ok := byName[m.Func]; ok {
			methods[m.Name] = append(methods[m.Name], fn)
		}
	}

	reached := map[*ir.Function]bool{entry: true}
	work := []*ir.Function{entry}
	for _, fn := range g.prog.Functions {
		if fn.Entry && !reached[fn] {
			reached[fn] = true
			work = append(work, fn)
		}
	}
	reach := func(target *ir.Function) {
		if !reached[target] {
			reached[target] = true
			work = append(work, target)
		}
	}
	// The machine itself calls how a value of a type that implements Show
	// is written, when it prints one held by another value
	for _, target := range methods[vm.ShowMethod] {
		reach(target)
	}
	// and compares a value of a type that implements Equal held by another
	for _, target := range methods[vm.EqualMethod] {
		reach(target)
	}
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		for _, bb := range reachableBlocks(fn) {
			for _, instr := range body(bb) {
				for _, operand := range instr.Operands {
					if target, ok := byName[functionNamed(operand)]; ok {
						reach(target)
					}
				}
				if instr.Op == ir.OpGetMethod && len(instr.Operands) == 2 {
					if c, ok := instr.Operands[1].(ir.Constant); ok {
						name, _ := c.Val.(string)
						for _, target := range methods[name] {
							reach(target)
						}
					}
				}
			}
		}
	}

	kept := make([]*ir.Function, 0, len(reached))
	for _, fn := range g.prog.Functions {
		if reached[fn] {
			kept = append(kept, fn)
		}
	}
	return kept
}

// functionNamed returns the name an operand gives if it is a constant that
// can refer to a function, and "" otherwise. A text that happens to be
// spelled like a function is a String constant, and is not taken for one.
func functionNamed(operand ir.Value) string {
	c, ok := operand.(ir.Constant)
	if !ok || c.Type == "String" {
		return ""
	}
	name, _ := c.Val.(string)
	return name
}

func (g *Generator) checkEntryPoint(functions []*ir.Function) {
	idx, ok := g.protoIndex[EntryPoint]
	if !ok {
		g.report(token.Position{}, ErrMissingMain, "program has no 'main' function",
			"Add an entry point: fn main() -> Int { return 0; }")
		return
	}
	if n := len(functions[idx].Params); n != 0 {
		g.report(token.Position{}, ErrMissingMain,
			fmt.Sprintf("'main' must not take parameters, it has %d", n),
			"Declare it as: fn main() -> Int { ... }")
	}
}

// report records an error at a source position.
func (g *Generator) report(pos token.Position, code, msg, suggestion string) {
	tok := token.Token{Pos: pos, EndPos: pos}
	tok.EndPos.Col++

	if suggestion != "" {
		g.errs.ReportErrorWithSuggestion(code, msg, suggestion, tok, g.sourceLines)
		return
	}
	g.errs.ReportErrorFromToken(code, msg, tok, g.sourceLines)
}

// constant returns the pool index of a constant, adding it if needed.
func (g *Generator) constant(val vm.Value, pos token.Position) uint16 {
	if idx, ok := g.constIndex[val]; ok {
		return uint16(idx)
	}
	if len(g.chunk.Constants) >= maxConstants {
		g.report(pos, ErrTooManyConstants,
			fmt.Sprintf("program uses more than %d distinct constants", maxConstants),
			"Reduce the number of distinct literals and field names")
		return 0
	}
	idx := g.chunk.AddConstant(val)
	g.constIndex[val] = idx
	return uint16(idx)
}

// stringConstant returns the pool index of a constant referring to a string
// in the string pool, as NEWSTR and the field instructions expect.
func (g *Generator) stringConstant(s string, pos token.Position) uint16 {
	idx, ok := g.stringIndex[s]
	if !ok {
		idx = g.chunk.AddString(s)
		g.stringIndex[s] = idx
	}
	return g.constant(vm.NewInt(int64(idx)), pos)
}

// extern returns the index of a host function name.
func (g *Generator) extern(name string) uint16 {
	idx, ok := g.externIndex[name]
	if !ok {
		idx = g.chunk.AddExtern(name)
		g.externIndex[name] = idx
	}
	return uint16(idx)
}

// place is what a debug record is made from.
type place struct {
	pos      token.Position
	funcName string
}

// debugInfo returns the debug record for an instruction. Instructions
// compiled from the same place of the same function share one record.
func (g *Generator) debugInfo(pos token.Position, funcName string) *vm.DebugInfo {
	if pos.Line == 0 {
		return nil
	}
	key := place{pos, funcName}
	if info, ok := g.debugIndex[key]; ok {
		return info
	}
	// A program of several modules has its lines registered per file
	line, ok := g.errs.SourceLine(pos.File, pos.Line)
	if !ok {
		line = g.sourceLines[pos.Line]
	}
	info := vm.NewDebugInfo(pos.File, pos.Line, pos.Col).
		WithSourceLine(line).
		WithFuncName(funcName)
	g.debugIndex[key] = info
	return info
}
