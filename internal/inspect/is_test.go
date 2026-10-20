package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// patternTests uses `value is Pattern` in the conditions of an if, an
// else if and a while: what an editor is asked about its names.
const patternTests = `struct Point { x: Int; y: Int; }
impl Point { fn sum() -> Int { return self.x + self.y; } }
enum Shape { Circle(Float), At(Point), Dot }
enum Option<T> { Some(T), None }

fn next(count: Int) -> Option<Shape> {
    if count > 2 { return Option.None; }
    return Option.Some(Shape.At(Point { x: count, y: 1 }));
}

fn main() -> Int {
    let shape = Shape.Circle(2.0);
    if shape is Shape.Circle(radius) && radius > 1.0 {
        println(radius);
    } else if shape is Shape.At(place) {
        println(place.sum());
    }
    let count = 0;
    while next(count) is Option.Some(Shape.At(where)) {
        count += where.sum();
    }
    return count;
}
`

func TestPatternTestsInTheEditor(t *testing.T) {
	lines := strings.Split(patternTests, "\n")

	// Hover: a name where the pattern gives it, in the rest of the
	// condition and in the block; the variant and the enum of the pattern
	for _, c := range []struct {
		line      int
		at        string
		signature string
		declared  int
	}{
		{13, "radius)", "(variable) radius: Float", 13},
		{13, "radius >", "radius: Float", 13},
		{14, "radius", "radius: Float", 13},
		{13, "Circle", "(variant) Shape.Circle(Float)", 3},
		{15, "place)", "(variable) place: Point", 15},
		{16, "place.sum", "place: Point", 15},
		{16, "sum()", "fn Point.sum() -> Int", 2},
		{19, "where)", "(variable) where: Point", 19},
		{20, "where.sum", "where: Point", 19},
		{19, "Some", "(variant) Option.Some(T)", 4},
	} {
		column := strings.Index(lines[c.line-1], c.at) + 1
		d := Describe("main.qi", patternTests, c.line, column)
		if d == nil {
			t.Errorf("%d %q: expected %q, got nothing", c.line, c.at, c.signature)
		} else if d.Signature != c.signature || d.Line != c.declared {
			t.Errorf("%d %q: got %q declared at line %d, want %q at line %d", c.line, c.at, d.Signature, d.Line, c.signature, c.declared)
		}
	}

	// Completion, with the line being typed left unfinished
	complete := func(line int, old, typed string) []Completion {
		src := append([]string{}, lines...)
		src[line-1] = strings.Replace(src[line-1], old, typed, 1)
		column := strings.Index(src[line-1], typed) + len(typed) + 1
		return Complete("main.qi", strings.Join(src, "\n"), line, column)
	}
	labels := func(items []Completion) string {
		var out []string
		for _, c := range items {
			out = append(out, c.Label)
		}
		return strings.Join(out, " ")
	}
	if got := labels(complete(16, "place.sum()", "place.")); got != "x y sum" {
		t.Errorf("after a dot on a name of a pattern: %q", got)
	}
	if got := labels(complete(20, "where.sum()", "where.")); got != "x y sum" {
		t.Errorf("after a dot in the body of a while: %q", got)
	}
	if got := labels(complete(15, "Shape.At(place)", "Shape.")); got != "Circle At Dot" {
		t.Errorf("the variants after is: %q", got)
	}
	inBlock := complete(14, "println(radius);", "ra")
	found := false
	for _, c := range inBlock {
		found = found || (c.Label == "radius" && c.Kind == "variable")
	}
	if !found {
		t.Errorf("in the block of the if, before the name is used: %s", labels(inBlock))
	}

	// Diagnostics: none for the program, and those of the new checks
	if got := Check("main.qi", patternTests); len(got) != 0 {
		t.Errorf("expected a clean program, got %+v", got)
	}
	for _, c := range []struct{ old, new, code string }{
		{"shape is Shape.Circle(radius) && radius > 1.0", "!(shape is Shape.Circle(radius))", "E1003"},
		{"shape is Shape.At(place)", "shape is Shape.Square(place)", "E2029"},
		{"shape is Shape.At(place)", "shape is Ta(place)", "E1001"},
		{"println(place.sum());", "println(radius);", "E1001"},
	} {
		got := Check("main.qi", strings.Replace(patternTests, c.old, c.new, 1))
		if len(got) == 0 || got[0].Code != c.code {
			t.Errorf("%q: expected %s, got %+v", c.new, c.code, got)
		}
	}
}

// A constant a case names is described, and leads to its declaration,
// whether it is of the file, imported by name or reached through its
// module.
func TestConstantsInPatterns(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.qi")
	signals := filepath.Join(dir, "signals.qi")
	os.WriteFile(signals, []byte("pub const STOP = \"stop\";\npub const HAND_OVER = \"hand_over\";\n"), 0o644)
	src := `import signals;
import signals.{HAND_OVER};

const GREETING = "hello";

fn main() -> Int {
    match "x" {
        case GREETING | HAND_OVER: { return 1; }
        case signals.STOP: { return 2; }
        default: { return 0; }
    }
}
`
	os.WriteFile(main, []byte(src), 0o644)
	lines := strings.Split(src, "\n")
	for _, c := range []struct {
		line      int
		at        string
		signature string
		file      string
		declared  int
	}{
		{8, "GREETING", "const GREETING: String", main, 4},
		{8, "HAND_OVER", "const signals.HAND_OVER: String", signals, 2},
		{9, "STOP", "const signals.STOP: String", signals, 1},
	} {
		column := strings.Index(lines[c.line-1], c.at) + 1
		d := Describe(main, src, c.line, column)
		if d == nil {
			t.Errorf("%d %q: expected %q, got nothing", c.line, c.at, c.signature)
		} else if d.Signature != c.signature || d.File != c.file || d.Line != c.declared {
			t.Errorf("%d %q: got %q declared in %s:%d, want %q in %s:%d", c.line, c.at, d.Signature, d.File, d.Line, c.signature, c.file, c.declared)
		}
	}
}
