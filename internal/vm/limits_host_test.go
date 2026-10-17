package vm

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// What a host lets a program do: how long it runs, and what it reaches.

// countdown loops n times, calling println every turn, and returns 7.
func countdown(n uint16) *Chunk {
	return mainOf(
		NewInstr(OpLOADI, 1, n, 0),     // 0
		NewInstr(OpLOADI, 2, 1, 0),     // 1
		NewInstr(OpNEWEXFUNC, 0, 0, 0), // 2: the turn starts here
		NewInstr(OpSUB, 1, 1, 2),       // 3
		NewInstr(OpLOADI, 3, 0, 0),     // 4
		NewInstr(OpGT, 3, 1, 3),        // 5
		NewInstr(OpJMPIF, 3, 2, 0),     // 6
		NewInstr(OpLOADI, 0, 7, 0),     // 7
		NewInstr(OpRET, 0, 0, 0),       // 8
	)
}

func TestAProgramEndsWhenItIsOutOfSteps(t *testing.T) {
	machine := NewVM()
	machine.SetOutput(io.Discard)

	// Without a limit, and with one it stays under
	for _, limit := range []uint64{0, 1000, 99} {
		machine.SetStepLimit(limit)
		result, err := machine.Exec(countdown(100))
		if err != nil || result.AsInt() != 7 {
			t.Fatalf("limit %d: %v, %v", limit, result, err)
		}
	}
	// A hundred turns are ninety-nine jumps backwards
	if machine.Steps() != 99 {
		t.Errorf("expected 99 steps, counted %d", machine.Steps())
	}

	machine.SetStepLimit(98)
	_, err := machine.Exec(countdown(100))
	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindBudget || failure.Code() != "R0012" || !strings.Contains(failure.Message, "98 steps") {
		t.Fatalf("expected the program to run out of steps, got %v", err)
	}

	// The count starts again with each program: the same one ends at the
	// same place every time
	for round := 0; round < 3; round++ {
		if _, err := machine.Exec(countdown(100)); err == nil || machine.Steps() != 99 {
			t.Errorf("round %d: %v after %d steps", round, err, machine.Steps())
		}
	}

	// A loop that never ends, ended
	machine.SetStepLimit(10_000)
	if _, err := machine.Exec(spinning()); err == nil || err.(*RuntimeError).Kind != KindBudget {
		t.Errorf("expected the loop to be ended, got %v", err)
	}
}

// Calls count as steps too: a program made of calls and no loop ends.
func TestCallsAreSteps(t *testing.T) {
	machine := NewVM()
	machine.SetMaxCallDepth(1_000_000)
	machine.SetStepLimit(5000)
	_, err := machine.Exec(benchFib(25))
	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindBudget {
		t.Fatalf("expected the program to run out of steps, got %v", err)
	}
	if len(failure.Stack) < 2 || failure.Stack[0].Function != "fib" {
		t.Errorf("the error does not say where the program was: %+v", failure.Stack)
	}
}

func TestOnlyPermittedNativesAreCalled(t *testing.T) {
	var out strings.Builder
	machine := NewVM()
	machine.SetOutput(&out)
	call := mainOf(
		NewInstr(OpNEWEXFUNC, 1, 0, 0),
		NewInstr(OpLOADI, 2, 5, 0),
		NewInstr(OpCALL, 1, 1, 0),
		NewInstr(OpLOADI, 0, 1, 0),
		NewInstr(OpRET, 0, 0, 0),
	)

	// Made once under no rule, the function is not handed out under a
	// stricter one
	if _, err := machine.Exec(call); err != nil || out.String() != "5\n" {
		t.Fatalf("%q, %v", out.String(), err)
	}

	asked := ""
	machine.PermitNatives(func(name string) bool { asked = name; return name != "println" })
	out.Reset()
	_, err := machine.Exec(call)
	failure, ok := err.(*RuntimeError)
	if !ok || failure.Kind != KindLibrary || !strings.Contains(failure.Message, "println") || !strings.Contains(failure.Message, "not permitted") {
		t.Fatalf("expected println to be refused, got %v", err)
	}
	if out.String() != "" || asked != "println" {
		t.Errorf("wrote %q, asked about %q", out.String(), asked)
	}

	machine.PermitNatives(nil)
	if _, err := machine.Exec(call); err != nil || out.String() != "5\n" {
		t.Errorf("with everything permitted again: %q, %v", out.String(), err)
	}
}

func TestExecContextStopsAtItsDeadline(t *testing.T) {
	machine := NewVM()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := machine.ExecContext(ctx, spinning()); !IsInterrupted(err) {
		t.Fatalf("expected the program to be stopped, got %v", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %v to stop", took)
	}

	// A context that is done before the program starts stops it at once;
	// one that is never done lets it finish
	done, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := machine.ExecContext(done, spinning()); !IsInterrupted(err) {
		t.Errorf("expected the program to be stopped as it starts, got %v", err)
	}
	machine.SetOutput(io.Discard)
	for round := 0; round < 50; round++ {
		// The context of one run never stops the next
		short, cancelShort := context.WithTimeout(context.Background(), time.Duration(round%5)*100*time.Microsecond+time.Microsecond)
		machine.ExecContext(short, countdown(2000))
		cancelShort()
		if result, err := machine.Exec(countdown(3)); err != nil || result.AsInt() != 7 {
			t.Fatalf("round %d: the run after was stopped: %v", round, err)
		}
	}
}
