package vm

// FunctionPrototype represents metadata for a compiled function.
type FunctionPrototype struct {
	Name          string // Function name for debugging and entry point lookup
	CodeStart     int    // Instruction index where function begins
	CodeSize      int    // Number of instructions in function body
	RegisterCount int    // Number of registers needed by this function
	ArgCount      int    // Number of parameters this function accepts
	UpvalueCount  int    // Number of upvalues this function captures

	// Signature describes the parameter and return types, in the form used
	// by cast descriptors: "fn(int,string)->bool". Casts to a function type
	// compare it with the type asked for. Empty for hand-written assembly.
	Signature string

	// Step marks a step of a flow: the VM asks its OnStep hook before
	// calling one (see steps.go). Once marks one that returns a
	// std.flow.Settled, which is never run twice
	Step bool
	Once bool

	// Stages are the stages of a flow written in the function: where an
	// instance may be taken up from its variables alone
	Stages []Stage

	// Migrate marks a migration of a flow, which the runtime of flows
	// runs; Params are the names of its parameters after self, the
	// variables of the stage it takes
	Migrate bool
	Params  []string

	// Inboxes are, for the run of a flow, its fields that are inboxes, and
	// how many messages each holds
	Inboxes []Inbox

	// Listens marks the run of a flow that waits for its cancellation
	Listens bool

	// ByQueue marks a step the application carries out, through its
	// queue
	ByQueue bool
}

// Inbox is an inbox of a flow: its field, and how many messages it holds.
type Inbox struct {
	Name     string
	Capacity int
	Message  string // the struct its messages are, "" for another type
}

// Stage is a stage of a flow: the call that passes it, and the register
// of each variable alive there.
type Stage struct {
	Name string
	At   int // the instruction that calls std.flow.passStage
	Vars []StageVar

	// Shape is the type of each variable, and of each field of the flow
	// as "self.name": what a new version has to keep for an instance to
	// move to it there
	Shape map[string]string
}

type StageVar struct {
	Name string
	Reg  int
}

// Chunk represents a compiled module containing bytecode and metadata.
type Chunk struct {
	Constants   []Value
	Code        []Instr             // Flat instruction array (all functions concatenated)
	Prototypes  []FunctionPrototype // Function metadata table
	ExternNames []string            // Names of external functions
	StringPool  []string            // Pool of string literals
	DebugInfo   map[int]*DebugInfo  // Map from instruction index to debug info
	Breakpoints []int               // Instruction pointers marked with ;BREAK in source

	// Private lists the fields written priv, by the name of their
	// struct: what shows a value by its fields leaves them out.
	Private map[string][]string
	// NOTE: RegisterCount and ArgCount removed - now per-function in Prototypes

	// Compiled holds, by prototype, the functions compiled to Go, nil for
	// one that runs as bytecode (see compiled.go). It is set by a program
	// qoqi native made, and is not part of a bytecode file.
	Compiled []CompiledFunc
}

// NewChunk creates a new empty chunk.
func NewChunk() *Chunk {
	return &Chunk{
		Constants:   make([]Value, 0),
		Code:        make([]Instr, 0),
		Prototypes:  make([]FunctionPrototype, 0),
		ExternNames: make([]string, 0),
		StringPool:  make([]string, 0),
		DebugInfo:   make(map[int]*DebugInfo),
		Breakpoints: make([]int, 0),
	}
}

// AddBreakpoint marks an instruction pointer as having a breakpoint.
func (c *Chunk) AddBreakpoint(ip int) {
	c.Breakpoints = append(c.Breakpoints, ip)
}

// AddConstant adds a constant to the constant pool and returns its index.
func (c *Chunk) AddConstant(val Value) int {
	c.Constants = append(c.Constants, val)
	return len(c.Constants) - 1
}

// AddInstruction adds an instruction to the code array.
func (c *Chunk) AddInstruction(instr Instr) {
	c.Code = append(c.Code, instr)
}

// AddInstructionWithDebug adds an instruction with debug info to the code array.
func (c *Chunk) AddInstructionWithDebug(instr Instr, debug *DebugInfo) {
	index := len(c.Code)
	c.Code = append(c.Code, instr)
	if debug != nil {
		c.DebugInfo[index] = debug
	}
}

// GetDebugInfo returns the debug info for an instruction at the given index.
func (c *Chunk) GetDebugInfo(index int) *DebugInfo {
	return c.DebugInfo[index]
}

// AddExtern adds an external function name and returns its index.
func (c *Chunk) AddExtern(name string) int {
	c.ExternNames = append(c.ExternNames, name)
	return len(c.ExternNames) - 1
}

// AddString adds a string literal to the string pool and returns its index.
func (c *Chunk) AddString(str string) int {
	c.StringPool = append(c.StringPool, str)
	return len(c.StringPool) - 1
}

// FindPrototype finds a function prototype by name and returns its index.
// Returns -1 if not found.
func (c *Chunk) FindPrototype(name string) int {
	for i, proto := range c.Prototypes {
		if proto.Name == name {
			return i
		}
	}
	return -1
}

// Stripped returns the chunk without its debug information: the same
// program, smaller, whose errors say what went wrong and at which
// instruction but not at which line of the source.
func (c *Chunk) Stripped() *Chunk {
	stripped := *c
	stripped.DebugInfo = make(map[int]*DebugInfo)
	stripped.Breakpoints = nil
	return &stripped
}
