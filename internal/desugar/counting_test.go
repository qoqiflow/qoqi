package desugar

import (
	"strings"
	"testing"
)

// A loop over numbers counts with its own variable when nothing can tell
// the two apart, and with a counter of its own otherwise.

func TestALoopOverNumbersCountsWithItsVariable(t *testing.T) {
	out := desugarBody(t, `let total = 0; for i in 0..10 { if i == 3 { continue; } total += i; }`)

	if strings.Contains(out, "__iter_") {
		t.Fatalf("expected no counter besides i, got: %s", out)
	}
	if !before(out, "let i: Int = 0;", "while (i < __len_") {
		t.Errorf("expected i to be the counter, got: %s", out)
	}
	// The increment comes after the body, where `continue` goes on too
	if !before(out, "continue;", "update (i = (i + 1));") {
		t.Errorf("expected the increment after the body, got: %s", out)
	}
}

// A loop that includes its end tests for the last number before it counts
// past it: it can end at the largest Int
func TestAnInclusiveLoopStopsAtItsEndBeforeCountingPastIt(t *testing.T) {
	out := desugarBody(t, `for i in 1..=5 { print(i); }`)

	if strings.Contains(out, "__iter_") {
		t.Errorf("expected i to be the counter, got: %s", out)
	}
	if !before(out, "if (i <= __len_0) { while {", "print(i);") {
		t.Errorf("expected the first number to be tested once, before the loop, got: %s", out)
	}
	if !strings.Contains(out, "update { if (i == __len_0) { break; } (i = (i + 1)); }") {
		t.Errorf("expected the loop to end at the last number before adding one, got: %s", out)
	}
}

func TestAnInclusiveLoopWithACounterStopsTheSameWay(t *testing.T) {
	out := desugarBody(t, `for i in 1..=5 { let f = () -> i; print(f()); }`)

	if !strings.Contains(out, "update { if (__iter_0 == __len_1) { break; } (__iter_0 = (__iter_0 + 1)); }") {
		t.Errorf("expected the counter to stop at the last number, got: %s", out)
	}
	if !before(out, "let i: Int = __iter_0;", "let f = ") {
		t.Errorf("expected an i for each iteration, got: %s", out)
	}
}

func TestALoopKeepsACounterWhenItsVariableCouldBeToldApart(t *testing.T) {
	cases := map[string]string{
		"a function made in the body keeps it": `for i in 0..3 { let square = () -> i * i; print(square()); }`,
		"a function written out keeps it":      `for i in 0..3 { let f = fn() -> Int { return i; }; print(f()); }`,
		"a deferred statement reads it":        `for i in 0..3 { defer print(i); }`,
		"with its position":                    `for n, i in 0..3 { print(n + i); }`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			out := desugarBody(t, body)
			// The counter is copied into an i of the iteration, and
			// counts after the body
			if !before(out, "let i: Int = __iter_0;", "update (__iter_0 = (__iter_0 + 1));") {
				t.Errorf("expected a counter of its own, got: %s", out)
			}
		})
	}
}

// What makes no difference leaves the variable counting
func TestALoopCountsWithItsVariableBesideWhatDoesNotConcernIt(t *testing.T) {
	for _, body := range []string{
		`for i in 0..3 { defer print("done"); }`,
		`let j = 0; for i in 0..3 { let f = fn() -> Int { return j; }; print(f() + i); }`,
		`for i in 0..3 { for j in 0..i { print(j); } }`,
	} {
		out := desugarBody(t, body)
		if !strings.Contains(out, "update (i = (i + 1));") {
			t.Errorf("%s: expected i to be the counter, got: %s", body, out)
		}
	}
}

func TestChangesOrKeeps(t *testing.T) {
	// Whether the loop around the body keeps a counter of its own
	parsed := func(body string) bool {
		return strings.Contains(desugarBody(t, "for i in 0..2 {\n"+body+"\n}"), "__iter_")
	}
	// A loop inside with a variable of the same name counts by assigning
	// to it: a name is all that is looked at, so that counts
	if !parsed(`for i in 0..2 { print(i); }`) {
		t.Error("expected an inner loop of the same name to be taken for a change")
	}
	if parsed(`print(i);`) {
		t.Error("expected reading the variable to change nothing")
	}
}
