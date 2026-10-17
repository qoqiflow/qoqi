package vm

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/token"
)

// ErrorKind says what sort of failure stopped a program. It is what a
// program that recovers from a failure can go by; the message is for
// people.
type ErrorKind string

const (
	KindPanic      ErrorKind = "panic"      // The program called panic
	KindBounds     ErrorKind = "bounds"     // An index outside an array or a text
	KindCast       ErrorKind = "cast"       // `value as Type` on a value of another type
	KindArithmetic ErrorKind = "arithmetic" // Division by zero, a result too large for an Int
	KindDepth      ErrorKind = "depth"      // Calls nested deeper than the limit
	KindMemory     ErrorKind = "memory"     // More memory than the limit
	KindLibrary    ErrorKind = "library"    // A function of the standard library refused its arguments
	KindDeadlock   ErrorKind = "deadlock"   // Every task waits for another
	KindOther      ErrorKind = "other"      // Anything else: a mistake in the bytecode, mostly

	// The two that follow end the program whatever it does: `attempt` does
	// not recover from them.
	KindInternal    ErrorKind = "internal"    // A mistake of the VM or of a native function, not of the program
	KindInterrupted ErrorKind = "interrupted" // The program was told to stop from outside (VM.Interrupt)
	KindBudget      ErrorKind = "budget"      // The program ran for longer than its host lets it (VM.SetStepLimit)
)

// StackFrame is one place in the program: an instruction, with the source
// line it was compiled from when that is known.
type StackFrame struct {
	Function   string
	File       string
	Line       int
	Column     int
	SourceLine string
}

// RuntimeError is a failure of a running program: what went wrong, of
// which kind, and where. Stack lists the calls that were in progress,
// innermost first: Stack[0] is where it happened, Stack[1] the call that
// led there, and so on up to main.
type RuntimeError struct {
	Kind    ErrorKind
	Message string
	Stack   []StackFrame

	// instruction is where it happened, for a program without debug
	// information, whose frames have no source positions
	instruction int
}

// maxPrintedFrames bounds how much of the call stack a message shows. A
// runaway recursion has thousands of frames, all alike.
const maxPrintedFrames = 12

// Codes of runtime errors, one per kind. They are to runtime errors what
// E0001 and its like are to errors found when compiling.
var runtimeCodes = map[ErrorKind]string{
	KindPanic:      "R0001",
	KindBounds:     "R0002",
	KindCast:       "R0003",
	KindArithmetic: "R0004",
	KindDepth:      "R0005",
	KindMemory:     "R0006",
	KindLibrary:    "R0007",
	KindOther:      "R0008",
	KindDeadlock:   "R0009",

	KindInternal:    "R0010",
	KindInterrupted: "R0011",
	KindBudget:      "R0012",
}

// Code is the code of the error: R0002 for an index out of bounds.
func (e *RuntimeError) Code() string {
	if code, ok := runtimeCodes[e.Kind]; ok {
		return code
	}
	return runtimeCodes[KindOther]
}

// Suggestion says what usually fixes an error of this kind.
func (e *RuntimeError) Suggestion() string {
	switch e.Kind {
	case KindBounds:
		return "Compare the index with the length (items.len()) before using it, or use items.at(index), which gives null instead of failing"
	case KindCast:
		return "Ask what the value is before casting it: 'if value is Type { ... }', or a match on its type"
	case KindArithmetic:
		if strings.Contains(e.Message, "overflow") {
			return "An Int holds whole numbers up to 140737488355327; use a Float for larger ones"
		}
		return "Test the divisor against 0 before dividing"
	case KindDepth:
		return "A function that calls itself needs a case where it stops; if the depth is intended, raise the limit with --max-depth=N"
	case KindMemory:
		return "Keep less alive at once, or raise the limit with --heap-mb=N"
	case KindInternal:
		return "This is a defect of Qoqi, not of the program: please report it, with the program that shows it"
	case KindDeadlock:
		return "A task that receives from a channel waits until a value is sent or the channel is closed: close it once nothing more will come"
	}
	return ""
}

// Diagnostic turns the failure into the form used for errors found when
// compiling, so that both are shown the same way: the code and message,
// the place with its source line underlined, a suggestion, then the calls
// that led there.
func (e *RuntimeError) Diagnostic() errors.Diagnostic {
	d := errors.Diagnostic{
		Severity:   errors.SeverityError,
		Code:       e.Code(),
		Message:    e.Message,
		Suggestion: e.Suggestion(),
	}
	if len(e.Stack) == 0 {
		return d
	}

	at := e.Stack[0]
	d.Pos = token.Position{File: at.File, Line: at.Line, Col: at.Column}
	d.Context = at.SourceLine
	d.Function = at.Function
	if end := wordEnd(at.SourceLine, at.Column); end > at.Column {
		d.Highlights = []errors.Highlight{{StartCol: at.Column, EndCol: end}}
	}

	// The calls that led there. A function calling itself gives the same
	// line many times over; it is shown once, with how often.
	type call struct {
		text  string
		times int
	}
	var calls []call
	for _, frame := range e.Stack[1:] {
		text := frame.describe()
		if n := len(calls); n > 0 && calls[n-1].text == text {
			calls[n-1].times++
			continue
		}
		calls = append(calls, call{text, 1})
	}
	shown := calls
	if len(shown) > maxPrintedFrames {
		shown = shown[:maxPrintedFrames]
	}
	for _, c := range shown {
		note := "called from " + c.text
		if c.times > 1 {
			note += fmt.Sprintf(" (%d times in a row)", c.times)
		}
		d.Notes = append(d.Notes, note)
	}
	if hidden := len(calls) - len(shown); hidden > 0 {
		// The outermost call is the most telling of the ones left out
		d.Notes = append(d.Notes, fmt.Sprintf("... %d more, the first from %s", hidden, calls[len(calls)-1].text))
	}
	return d
}

// wordEnd returns the column after the name or number that starts at a
// column of a line, so that all of it is underlined. Debug information
// records where an instruction's source starts, not where it ends; for a
// call or a variable, what starts there is its name.
func wordEnd(line string, column int) int {
	runes := []rune(line)
	end := column - 1
	for end >= 0 && end < len(runes) {
		r := runes[end]
		if r != '_' && r != '.' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			break
		}
		end++
	}
	return end + 1
}

// Error writes the failure for a person, as a compile error is written.
func (e *RuntimeError) Error() string {
	if len(e.Stack) == 0 || e.Stack[0].File == "" {
		// A program without debug information: assembly written by hand
		return fmt.Sprintf("[ERROR] %s: %s (at instruction %d)", e.Code(), e.Message, e.instruction)
	}
	return e.Diagnostic().String()
}

// describe writes a frame on one line: file:line:column in function 'name'.
func (f StackFrame) describe() string {
	if f.File == "" {
		if f.Function != "" {
			return "function '" + f.Function + "'"
		}
		return "an unknown place"
	}
	text := fmt.Sprintf("%s:%d:%d", f.File, f.Line, f.Column)
	if f.Function != "" {
		text += " in function '" + f.Function + "'"
	}
	return text
}

// frameAt describes the instruction at ip of a chunk, in the function fn.
func frameAt(chunk *Chunk, ip int, fn *FunctionObj) StackFrame {
	frame := StackFrame{}
	if fn != nil {
		frame.Function = fn.Proto().Name
	}
	if chunk == nil {
		return frame
	}
	if info := chunk.GetDebugInfo(ip); info != nil {
		frame.File, frame.Line, frame.Column, frame.SourceLine = info.FilePath, info.Line, info.Column, info.SourceLine
		if info.FuncName != "" {
			frame.Function = info.FuncName
		}
	}
	return frame
}

// callStack lists where the program is: the instruction being run, then
// the call that led to it, and so on outwards.
func (vm *VM) callStack() []StackFrame {
	stack := make([]StackFrame, 0, vm.frameCount+1)
	stack = append(stack, frameAt(vm.chunk, vm.ip, vm.currentFunction))
	for i := vm.frameCount - 1; i >= 0; i-- {
		frame := vm.frames[i]
		if strings.HasSuffix(frame.function.proto.Name, "."+ShowMethod) {
			// The call the machine made to print a value: the show it calls
			// and the print that asked for it are what the program wrote
			continue
		}
		stack = append(stack, frameAt(frame.function.chunk, frame.ip, frame.function))
	}
	return stack
}

// kindOf tells the kind of a failure from its message. The VM raises its
// errors as plain messages in many places; this is where they are sorted.
func kindOf(message string) ErrorKind {
	switch {
	case strings.Contains(message, "index out of bounds"):
		return KindBounds
	case strings.HasPrefix(message, "cannot cast"):
		return KindCast
	case strings.Contains(message, "division by zero"), strings.Contains(message, "modulo by zero"), strings.Contains(message, "integer overflow"):
		return KindArithmetic
	case strings.HasPrefix(message, "stack overflow"):
		return KindDepth
	case strings.Contains(message, "out of memory"):
		// Also when it says first what could not be made: "string
		// allocation failed: out of memory: ..."
		return KindMemory
	case strings.HasPrefix(message, "std."):
		return KindLibrary
	}
	return KindOther
}

// runtimeError makes the error for a failure at the instruction being run.
func (vm *VM) runtimeError(message string) error {
	return vm.runtimeErrorOf(kindOf(message), message)
}

func (vm *VM) runtimeErrorOf(kind ErrorKind, message string) error {
	return &RuntimeError{Kind: kind, Message: message, Stack: vm.callStack(), instruction: vm.ip}
}

// libraryRoot starts the file of every module of the standard library.
const libraryRoot = "<std>/"

// atTheCall makes a failure raised inside the standard library one of
// the program's call into it: the calls inside the library are left out,
// and a function of the library that refused its arguments is named as
// the program called it, std.csv.parseWith rather than the private
// std.csv.rows it uses. What fails in a function the program handed to
// the library is the program's own place already, and stays; so does a
// failure with no call of the program to show, and one of the VM itself.
func (e *RuntimeError) atTheCall() {
	if e == nil || e.Kind == KindInternal || e.Kind == KindInterrupted || e.Kind == KindBudget {
		return
	}
	inside := 0
	for inside < len(e.Stack) && strings.HasPrefix(e.Stack[inside].File, libraryRoot) {
		inside++
	}
	if inside == 0 || inside == len(e.Stack) {
		return
	}

	// A panic of the library says what it needs to in its message; any
	// other failure is said to be the function's
	called := unspecialized(e.Stack[inside-1].Function)
	if called != "" && e.Kind != KindPanic {
		// A native of the library names itself; the program called the
		// function around it: std.json.parseText becomes std.json.parse
		if name, rest, ok := strings.Cut(e.Message, ": "); ok && strings.HasPrefix(name, "std.") && !strings.Contains(name, " ") {
			e.Message = called + ": " + rest
		} else if !strings.HasPrefix(e.Message, called+": ") {
			e.Message = called + ": " + e.Message
		}
	}
	e.Stack = e.Stack[inside:]
}

// unspecialized is the name of a function of the library as its module
// writes it, without the types a generic one was made for: the copy of
// std.arrays.swap for [Int] is std.arrays.swap_Int. The library names
// nothing with an underscore, so one followed by a capital starts them.
func unspecialized(name string) string {
	parts := strings.Split(name, ".")
	for i, part := range parts {
		for at := 0; at+1 < len(part); at++ {
			if part[at] == '_' && part[at+1] >= 'A' && part[at+1] <= 'Z' {
				parts[i] = part[:at]
				break
			}
		}
	}
	return strings.Join(parts, ".")
}

// panicError is what the builtin `panic` returns to the VM, which turns it
// into a RuntimeError of kind panic at the place of the call.
type panicError struct{ message string }

func (e *panicError) Error() string { return e.message }
