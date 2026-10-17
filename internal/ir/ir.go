package ir

import (
	"fmt"

	"github.com/qoqiflow/qoqi/internal/token"
)

// TypeRef is a reference to a type (just the name/reference).
type TypeRef string

// Value is an interface for operands (Registers, Constants, etc.)
type Value interface {
	String() string
}

// Register represents a virtual register (%v1, %v2...)
type Register struct {
	ID int
}

func (r Register) String() string {
	return fmt.Sprintf("%%v%d", r.ID)
}

// Constant represents a literal value
type Constant struct {
	Val  interface{}
	Type TypeRef
}

func (c Constant) String() string {
	if s, ok := c.Val.(string); ok {
		return fmt.Sprintf("\"%s\"", s)
	}
	return fmt.Sprintf("%v", c.Val)
}

// Instruction represents a single IR operation
type Instruction struct {
	Op       OpType
	Dest     *Register // The result goes here (can be nil)
	Operands []Value   // Variable number of operands

	// Source position the instruction was generated from (zero if unknown)
	Pos token.Position
}

// BasicBlock is a sequence of instructions without internal control flow
type BasicBlock struct {
	ID           int
	Instructions []*Instruction
	Predecessors []*BasicBlock
	Successors   []*BasicBlock
}

func (bb *BasicBlock) String() string {
	return fmt.Sprintf("b%d", bb.ID)
}

// Function represents a compiled function in IR
type Function struct {
	Name       string
	Params     []*Register
	Blocks     []*BasicBlock
	EntryBlock *BasicBlock
	RegCount   int // Counter for generating unique registers

	// Number of variables captured from enclosing functions
	UpvalueCount int

	// Type of the function as a cast descriptor ("fn(int)->string"), kept
	// with the compiled function so casts to function types can check it.
	// Empty for methods, which are not values.
	Signature string

	// Step marks a step of a flow (see docs/flow.md): the runtime of
	// flows is told when one is called
	Step bool
	Once bool // a step that returns a std.flow.Settled

	// Entry marks a function the program can be started at besides main:
	// the run of a flow, a migration
	Entry bool

	// Migrate marks a migration of a flow; ParamNames are the names of its
	// parameters, after self: the variables of the stage it takes
	Migrate    bool
	ParamNames []string

	// Inboxes are, for the run of a flow, the fields of the flow that are
	// inboxes, and how many messages each holds: the runtime gives an
	// instance its inboxes when it starts
	Inboxes []Inbox

	// Listens marks the run of a flow that waits for its cancellation,
	// flow.cancelled(): it is given it, rather than stopped
	Listens bool

	// ByQueue marks a step the application carries out, through its
	// queue: one that has no attempt written has not left
	ByQueue bool
}

// Program represents the complete compilation unit
type Program struct {
	Functions []*Function

	// The methods a value can be asked for when the program runs
	// (OpGetMethod): which function is the method of which type. A
	// function listed here may be reached without any instruction naming
	// it, through a value of its type.
	Methods []Method

	// The fields written priv, by the name of their struct: what
	// std.reflect neither shows nor changes.
	Private map[string][]string
}

// Method says that a function is the method Name of the struct or enum Type.
type Method struct {
	Type string
	Name string
	Func string
}

// Inbox is an inbox of a flow: its field, and how many messages it holds.
type Inbox struct {
	Name     string
	Capacity int
	Message  string // the struct its messages are, "" for another type
}
