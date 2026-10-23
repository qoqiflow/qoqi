package flowlock

import "testing"

// A type made of structs or enums carries the hash of their layout: two
// hashes compare whole. A shape written before hashes were compares by
// name with one written after, so that it is not taken as changed.
func TestSameType(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"Int", "Int", true},
		{"Int", "String", false},
		{"Answer#aaaaaaaaaaaa", "Answer#aaaaaaaaaaaa", true},
		{"Answer#aaaaaaaaaaaa", "Answer#bbbbbbbbbbbb", false},
		{"Answer", "Answer#bbbbbbbbbbbb", true},
		{"Answer#aaaaaaaaaaaa", "Answer", true},
		{"Answer", "Question#bbbbbbbbbbbb", false},
		{"[Answer]#aaaaaaaaaaaa", "[Answer]", true},
	} {
		if got := SameType(c.a, c.b); got != c.same {
			t.Errorf("SameType(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// What a new version changed of a stage: a variable whose struct has
// other fields, though its name is the same.
func TestChangesSeeALayout(t *testing.T) {
	old := Shapes{"Ask": {"waiting": {"a": "Answer#aaaaaaaaaaaa", "n": "Int"}}}
	newer := Shapes{"Ask": {"waiting": {"a": "Answer#bbbbbbbbbbbb", "n": "Int"}}}
	if changes := Changes(old, newer); len(changes) != 1 {
		t.Errorf("changes: %v", changes)
	}
	legacy := Shapes{"Ask": {"waiting": {"a": "Answer", "n": "Int"}}}
	if changes := Changes(legacy, newer); len(changes) != 0 {
		t.Errorf("a lock of before layouts is changed everywhere: %v", changes)
	}
}
