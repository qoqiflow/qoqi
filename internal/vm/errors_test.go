package vm

import (
	"strings"
	"testing"
)

// debugChunk builds a program of several functions whose instructions each
// carry a source position: instruction i of a function is on line i+1 of
// its "file", so a failure can be checked for where it says it happened.
func debugChunk(externs []string, constants []Value, funcs ...fnDef) *Chunk {
	chunk := chunkOf(externs, constants, funcs...)
	for _, proto := range chunk.Prototypes {
		for i := 0; i < proto.CodeSize; i++ {
			chunk.DebugInfo[proto.CodeStart+i] = NewDebugInfo(proto.Name+".qi", i+1, 5).
				WithSourceLine("    step" + string(rune('A'+i)) + "();").
				WithFuncName(proto.Name)
		}
	}
	return chunk
}

// failing runs a chunk that must stop with a runtime error and returns it.
func failing(t *testing.T, machine *VM, chunk *Chunk) *RuntimeError {
	t.Helper()
	_, err := machine.Exec(chunk)
	if err == nil {
		t.Fatal("expected a runtime error")
	}
	failure, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("expected a *RuntimeError, got %T: %v", err, err)
	}
	return failure
}

// A chain main -> middle -> inner, where inner does what the test gives it.
func chain(inner ...Instr) *Chunk {
	return debugChunk([]string{"panic", "__cast"}, []Value{NewInt(0), NewInt(1)},
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
			NewInstr(OpNOP, 0, 0, 0),
			NewInstr(OpNEWFUNC, 1, 1, 0),
			NewInstr(OpCALL, 1, 0, 0), // line 3
			NewInstr(OpRET, 0, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "middle", RegisterCount: 4}, []Instr{
			NewInstr(OpNEWFUNC, 1, 2, 0),
			NewInstr(OpCALL, 1, 0, 0), // line 2
			NewInstr(OpRET, 0, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "inner", RegisterCount: 8}, inner},
	)
}

func TestRuntimeErrorsHaveAKindAndACallStack(t *testing.T) {
	outOfBounds := []Instr{
		NewInstr(OpNEWARRAY, 1, 2, 0),
		NewInstr(OpLOADI, 2, 7, 0),
		NewInstr(OpGETINDEX, 3, 1, 2), // line 3
		NewInstr(OpRET, 3, 0, 0),
	}
	failure := failing(t, NewVM(), chain(outOfBounds...))

	if failure.Kind != KindBounds || failure.Code() != "R0002" || failure.Message != "index out of bounds: 7 (length 0)" {
		t.Errorf("unexpected failure: %s %s %q", failure.Kind, failure.Code(), failure.Message)
	}
	// Innermost first: where it happened, then each call that led there
	want := []struct {
		function string
		line     int
	}{{"inner", 3}, {"middle", 2}, {"main", 3}}
	if len(failure.Stack) != len(want) {
		t.Fatalf("expected %d frames, got %d: %+v", len(want), len(failure.Stack), failure.Stack)
	}
	for i, w := range want {
		frame := failure.Stack[i]
		if frame.Function != w.function || frame.Line != w.line || frame.File != w.function+".qi" || frame.Column != 5 {
			t.Errorf("frame %d: expected %s at line %d, got %+v", i, w.function, w.line, frame)
		}
	}
}

func TestRuntimeErrorKinds(t *testing.T) {
	text := func(chunk *Chunk, s string) uint16 {
		return uint16(chunk.AddConstant(NewInt(int64(chunk.AddString(s)))))
	}

	tests := []struct {
		name       string
		inner      func(chunk *Chunk) []Instr
		kind       ErrorKind
		code       string
		message    string
		suggestion string
	}{
		{"index out of bounds", func(*Chunk) []Instr {
			return []Instr{NewInstr(OpNEWARRAY, 1, 2, 0), NewInstr(OpLOADI, 2, 3, 0), NewInstr(OpGETINDEX, 3, 1, 2)}
		}, KindBounds, "R0002", "index out of bounds: 3 (length 0)", "items.len()"},
		{"write out of bounds", func(*Chunk) []Instr {
			return []Instr{NewInstr(OpNEWARRAY, 1, 2, 0), NewInstr(OpLOADI, 2, 0, 0), NewInstr(OpSETELEM, 1, 2, 2)}
		}, KindBounds, "R0002", "index out of bounds: 0 (length 0)", "items.at(index)"},
		{"division by zero", func(*Chunk) []Instr {
			return []Instr{NewInstr(OpLOADI, 1, 4, 0), NewInstr(OpLOADI, 2, 0, 0), NewInstr(OpDIV, 3, 1, 2)}
		}, KindArithmetic, "R0004", "division by zero", "Test the divisor"},
		{"integer overflow", func(chunk *Chunk) []Instr {
			big := uint16(chunk.AddConstant(NewInt(MaxInt)))
			return []Instr{NewInstr(OpLOADK, 1, big, 0), NewInstr(OpLOADI, 2, 1, 0), NewInstr(OpADD, 3, 1, 2)}
		}, KindArithmetic, "R0004", "integer overflow", "An Int holds whole numbers up to 140737488355327"},
		{"failed cast", func(chunk *Chunk) []Instr {
			descriptor := text(chunk, "string")
			return []Instr{NewInstr(OpNEWEXFUNC, 1, 1, 0), NewInstr(OpLOADI, 2, 5, 0), NewInstr(OpNEWSTR, 3, descriptor, 0), NewInstr(OpCALL, 1, 2, 0)}
		}, KindCast, "R0003", "cannot cast Int to String", "if value is Type"},
		{"panic", func(chunk *Chunk) []Instr {
			message := text(chunk, "the queue is empty")
			return []Instr{NewInstr(OpNEWEXFUNC, 1, 0, 0), NewInstr(OpNEWSTR, 2, message, 0), NewInstr(OpLOADI, 3, 42, 0), NewInstr(OpCALL, 1, 2, 0)}
		}, KindPanic, "R0001", "the queue is empty 42", ""},
		{"something else", func(*Chunk) []Instr {
			return []Instr{NewInstr(OpLOADI, 1, 1, 0), NewInstr(OpCALL, 1, 0, 0)}
		}, KindOther, "R0008", "CALL requires function object", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := chain(NewInstr(OpRET, 0, 0, 0))
			code := append(tt.inner(chunk), NewInstr(OpRET, 0, 0, 0))
			// Replace the body of inner, which is last in the chunk
			inner := &chunk.Prototypes[2]
			chunk.Code = append(chunk.Code[:inner.CodeStart], code...)
			inner.CodeSize = len(code)
			for i := range code {
				chunk.DebugInfo[inner.CodeStart+i] = NewDebugInfo("inner.qi", i+1, 5).WithFuncName("inner")
			}

			failure := failing(t, NewVM(), chunk)
			if failure.Kind != tt.kind || failure.Code() != tt.code {
				t.Errorf("expected %s (%s), got %s (%s): %s", tt.kind, tt.code, failure.Kind, failure.Code(), failure.Message)
			}
			if !strings.Contains(failure.Message, tt.message) {
				t.Errorf("expected a message containing %q, got %q", tt.message, failure.Message)
			}
			if tt.suggestion == "" && failure.Suggestion() != "" {
				t.Errorf("expected no suggestion, got %q", failure.Suggestion())
			}
			if !strings.Contains(failure.Suggestion(), tt.suggestion) {
				t.Errorf("expected a suggestion containing %q, got %q", tt.suggestion, failure.Suggestion())
			}
			if len(failure.Stack) != 3 || failure.Stack[0].Function != "inner" || failure.Stack[2].Function != "main" {
				t.Errorf("expected the stack inner, middle, main; got %+v", failure.Stack)
			}
		})
	}

	// The two limits of the machine
	recursion := debugChunk(nil, nil, fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
		NewInstr(OpNEWFUNC, 1, 0, 0),
		NewInstr(OpCALL, 1, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
	}})
	machine := NewVM()
	machine.SetMaxCallDepth(50)
	deep := failing(t, machine, recursion)
	if deep.Kind != KindDepth || deep.Code() != "R0005" || len(deep.Stack) != 51 || !strings.Contains(deep.Suggestion(), "--max-depth") {
		t.Errorf("unexpected stack overflow: %s %s, %d frames, %q", deep.Kind, deep.Code(), len(deep.Stack), deep.Suggestion())
	}

	growing := debugChunk(nil, nil, fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
		NewInstr(OpNEWARRAY, 1, 8, 0),
		NewInstr(OpLOADI, 2, 1, 0),
		NewInstr(OpARRPUSH, 1, 2, 0),
		NewInstr(OpJMP, 2, 0, 0),
	}})
	full := failing(t, NewVMWithHeapSize(64*1024), growing)
	if full.Kind != KindMemory || full.Code() != "R0006" || !strings.Contains(full.Suggestion(), "--heap-mb") {
		t.Errorf("unexpected out of memory: %s %s %q", full.Kind, full.Code(), full.Suggestion())
	}
}

func TestRuntimeErrorIsShownLikeACompileError(t *testing.T) {
	chunk := chain(
		NewInstr(OpNEWARRAY, 1, 2, 0),
		NewInstr(OpLOADI, 2, 7, 0),
		NewInstr(OpGETINDEX, 3, 1, 2),
		NewInstr(OpRET, 3, 0, 0),
	)
	// A real source line where it happens, to see it underlined
	inner := chunk.Prototypes[2]
	chunk.DebugInfo[inner.CodeStart+2] = NewDebugInfo("list.qi", 12, 12).WithSourceLine("    return items[index];").WithFuncName("pick")

	got := failing(t, NewVM(), chunk).Error()
	want := `[ERROR] R0002: index out of bounds: 7 (length 0)
  --> list.qi:12:12 in function 'pick'
    |
 12 |     return items[index];
    |            ^^^^^ 
    |
    = Suggestion: Compare the index with the length (items.len()) before using it, or use items.at(index), which gives null instead of failing
  called from middle.qi:2:5 in function 'middle'
  called from main.qi:3:5 in function 'main'
`
	if got != want {
		t.Errorf("unexpected rendering\n--- want ---\n%s--- got ---\n%s", want, got)
	}

	// As a diagnostic it has the parts of one, for tools
	d := failing(t, NewVM(), chunk).Diagnostic()
	if d.Code != "R0002" || d.Pos.File != "list.qi" || d.Pos.Line != 12 || d.Function != "pick" || len(d.Notes) != 2 || d.Suggestion == "" {
		t.Errorf("unexpected diagnostic: %+v", d)
	}
}

func TestLongCallStacksAreShortened(t *testing.T) {
	// A function that calls itself: the same line 200 times, shown once
	recursion := debugChunk(nil, nil,
		fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
			NewInstr(OpNEWFUNC, 1, 1, 0),
			NewInstr(OpCALL, 1, 0, 0),
			NewInstr(OpRET, 0, 0, 0),
		}},
		fnDef{FunctionPrototype{Name: "loop", RegisterCount: 4}, []Instr{
			NewInstr(OpNEWFUNC, 1, 1, 0),
			NewInstr(OpCALL, 1, 0, 0),
			NewInstr(OpRET, 0, 0, 0),
		}},
	)
	machine := NewVM()
	machine.SetMaxCallDepth(200)
	text := failing(t, machine, recursion).Error()

	if strings.Count(text, "called from") != 2 {
		t.Errorf("expected the repeated call once and the call from main, got:\n%s", text)
	}
	for _, want := range []string{"called from loop.qi:2:5 in function 'loop' (199 times in a row)", "called from main.qi:2:5 in function 'main'"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in:\n%s", want, text)
		}
	}

	// Many different calls: the first ones, then how many more and where
	// it all started
	var funcs []fnDef
	const depth = 40
	for i := 0; i < depth; i++ {
		name := "f" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		if i == 0 {
			name = "main"
		}
		code := []Instr{NewInstr(OpNEWFUNC, 1, uint16(i+1), 0), NewInstr(OpCALL, 1, 0, 0), NewInstr(OpRET, 0, 0, 0)}
		if i == depth-1 {
			code = []Instr{NewInstr(OpLOADI, 1, 1, 0), NewInstr(OpLOADI, 2, 0, 0), NewInstr(OpDIV, 3, 1, 2), NewInstr(OpRET, 0, 0, 0)}
		}
		funcs = append(funcs, fnDef{FunctionPrototype{Name: name, RegisterCount: 4}, code})
	}
	text = failing(t, NewVM(), debugChunk(nil, nil, funcs...)).Error()
	if strings.Count(text, "called from") != maxPrintedFrames {
		t.Errorf("expected %d calls to be shown, got:\n%s", maxPrintedFrames, text)
	}
	if !strings.Contains(text, "... 27 more, the first from main.qi:2:5 in function 'main'") {
		t.Errorf("expected the rest to be counted and the start named, got:\n%s", text)
	}
}

func TestRuntimeErrorWithoutDebugInformation(t *testing.T) {
	// Assembly written by hand has no source positions
	chunk := chunkOf(nil, nil, fnDef{FunctionPrototype{Name: "main", RegisterCount: 4}, []Instr{
		NewInstr(OpLOADI, 1, 1, 0), NewInstr(OpLOADI, 2, 0, 0), NewInstr(OpDIV, 3, 1, 2), NewInstr(OpRET, 0, 0, 0),
	}})
	failure := failing(t, NewVM(), chunk)
	if got, want := failure.Error(), "[ERROR] R0004: division by zero (at instruction 2)"; got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if len(failure.Stack) != 1 || failure.Stack[0].Function != "main" {
		t.Errorf("expected the function to be known all the same, got %+v", failure.Stack)
	}
}

// A failure inside the library is the program's call into it, naming the
// function the program called; one in the program's own code, or with no
// call of the program to show, stays as it was.
func TestFailureAtTheCall(t *testing.T) {
	inside := func() []StackFrame {
		return []StackFrame{
			{Function: "std.csv.parseWith", File: "<std>/csv.qi", Line: 38},
			{Function: "main", File: "app.qi", Line: 4},
		}
	}

	refused := &RuntimeError{Kind: KindLibrary, Message: "std.csv.rows: the separator must be one character", Stack: inside()}
	refused.atTheCall()
	if refused.Message != "std.csv.parseWith: the separator must be one character" {
		t.Errorf("message: %q", refused.Message)
	}
	if len(refused.Stack) != 1 || refused.Stack[0].File != "app.qi" {
		t.Errorf("stack: %+v", refused.Stack)
	}

	bounds := &RuntimeError{Kind: KindBounds, Message: "index out of bounds: 3 (length 1)", Stack: inside()}
	bounds.Stack[0].Function = "std.arrays.swap_Int"
	bounds.atTheCall()
	if bounds.Message != "std.arrays.swap: index out of bounds: 3 (length 1)" {
		t.Errorf("message: %q", bounds.Message)
	}

	// A native of another kind that names itself: the name is the
	// function the program called, once
	divided := &RuntimeError{Kind: KindArithmetic, Message: "std.big.quo: division by zero", Stack: inside()}
	divided.Stack[0].Function = "std.big.Big.dividedBy"
	divided.atTheCall()
	if divided.Message != "std.big.Big.dividedBy: division by zero" {
		t.Errorf("message: %q", divided.Message)
	}

	// A panic says what it means itself
	stopped := &RuntimeError{Kind: KindPanic, Message: "std.regex.must: '(' is not a regular expression", Stack: inside()}
	stopped.atTheCall()
	if stopped.Message != "std.regex.must: '(' is not a regular expression" || stopped.Stack[0].File != "app.qi" {
		t.Errorf("panic: %q at %+v", stopped.Message, stopped.Stack)
	}

	// Only the library: nothing to point at instead
	alone := &RuntimeError{Kind: KindPanic, Message: "x", Stack: inside()[:1]}
	alone.atTheCall()
	if len(alone.Stack) != 1 || alone.Stack[0].File != "<std>/csv.qi" {
		t.Errorf("alone: %+v", alone.Stack)
	}

	// The program's own place first: a function it handed to the library
	own := &RuntimeError{Kind: KindBounds, Message: "index out of bounds", Stack: []StackFrame{
		{Function: "lambda_1", File: "app.qi", Line: 3},
		{Function: "std.arrays.map", File: "<std>/arrays.qi", Line: 10},
		{Function: "main", File: "app.qi", Line: 3},
	}}
	own.atTheCall()
	if own.Message != "index out of bounds" || len(own.Stack) != 3 {
		t.Errorf("own: %q %+v", own.Message, own.Stack)
	}

	if got := unspecialized("std.list.List_Int.push"); got != "std.list.List.push" {
		t.Errorf("unspecialized: %q", got)
	}
}
