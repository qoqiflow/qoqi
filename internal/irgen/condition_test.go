package irgen

import (
	"strings"
	"testing"
)

// A condition is compiled for where it leads: `a && b` in an if or a
// while needs no value, only two tests one after the other.
func TestConditionsLeadStraightToTheirBranches(t *testing.T) {
	src := `
		fn pick(a: Int, b: Int, c: Int) -> Int {
			if a < b && b < c || !(a == c) { return 1; }
			let n = 0;
			while n < a && n != b { n += 1; }
			return n;
		}
		fn main() -> Int { return pick(1, 2, 3); }`
	ir := dumpFunction(t, src, "pick")

	// Three tests for the if, two for the while, each straight into a branch
	if got := strings.Count(ir, "\tBR "); got != 5 {
		t.Errorf("expected 5 branches, found %d in:\n%s", got, ir)
	}
	// No value is made of the conditions: nothing is moved between the
	// tests, and no test of what a test gave
	for _, line := range strings.Split(ir, "\n") {
		if strings.Contains(line, "= MOV, %v") && !strings.Contains(line, "MOV, %v1") {
			t.Errorf("a value is moved in a condition: %s\n%s", strings.TrimSpace(line), ir)
		}
	}
	if strings.Contains(ir, "NOT") {
		t.Errorf("a negation is worked out instead of followed:\n%s", ir)
	}
}

// As a value, a condition is still worked out: both ways give the same.
func TestConditionsAsValuesAreStillValues(t *testing.T) {
	src := `
		fn both(a: Bool, b: Bool) -> Bool { let held = a && b; return held; }
		fn main() -> Int { if both(true, false) { return 1; } return 0; }`
	ir := dumpFunction(t, src, "both")
	if strings.Count(ir, "\tBR ") != 1 || !strings.Contains(ir, "RET") {
		t.Errorf("unexpected shape:\n%s", ir)
	}
}

// The blocks a condition makes come before what it guards, and what
// follows a loop after the loop: a test that holds falls into the next
// test, without a jump.
func TestBlocksAreInTheOrderTheyRun(t *testing.T) {
	src := `
		fn count(limit: Int) -> Int {
			let n = 0;
			let odd = 0;
			while n < limit {
				if n > 2 && n % 2 == 1 { odd += 1; }
				n += 1;
			}
			return odd;
		}
		fn main() -> Int { return count(10); }`
	ir := dumpFunction(t, src, "count")

	// The return, which follows the loop, is the last thing written
	lines := strings.Split(strings.TrimSpace(ir), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0 && last == ""; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "RET %") {
			last = line
			for _, after := range lines[i+1:] {
				after = strings.TrimSpace(after)
				if after != "" && after != "}" && !strings.HasSuffix(after, ":") && after != "RET" && !strings.HasPrefix(after, "JMP") {
					t.Errorf("code follows what follows the loop: %s\n%s", after, ir)
				}
			}
		}
	}
	if last == "" {
		t.Fatalf("no return found:\n%s", ir)
	}

	// The second test of the && comes right after the first
	first := strings.Index(ir, "GT,")
	second := strings.Index(ir, "MOD,")
	added := strings.Index(ir, "ADD,")
	if first < 0 || second < first || added < second {
		t.Errorf("the tests and what they guard are not in the order they run:\n%s", ir)
	}
}
