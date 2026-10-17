package inspect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const sample = `import std.math;
import std.arrays;
import std.task;
import std.task.{Task};

// A place on the grid.
struct Point { x: Int; y: Int; }

impl Point {
    // Both coordinates added.
    fn sum() -> Int { return self.x + self.y; }
}

enum Shape { Circle(Float), Dot }

const limit = 10;

// Twice a number.
fn double(n: Int) -> Int { return n * 2; }

fn main() -> Int {
    let p = Point { x: 1, y: 2 };
    let total = double(p.sum()) + (-3).abs();
    let names = [1, 2].map(n -> n + total);
    let job = task.start(fn() -> Int { return limit; });
    println(p.x, names, wait job, Shape.Dot);
    return 0;
}
`

// edit gives the sample with one line replaced, as it is while typing.
func edit(line int, text string) string {
	lines := strings.Split(sample, "\n")
	lines[line-1] = text
	return strings.Join(lines, "\n")
}

func TestOutline(t *testing.T) {
	var got []string
	for _, s := range Outline("main.qi", sample) {
		line := s.Kind + " " + s.Name
		if s.Container != "" {
			line = s.Kind + " " + s.Container + "." + s.Name
		}
		if s.Detail != "" {
			line += " | " + s.Detail
		}
		got = append(got, line)
		if text := strings.Split(sample, "\n")[s.Line-1]; !strings.HasPrefix(text[s.Column-1:], s.Name) {
			t.Errorf("%s is said to be at %d:%d, where there is %q", s.Name, s.Line, s.Column, text[s.Column-1:])
		}
	}
	want := []string{
		"struct Point",
		"field Point.x | Int",
		"field Point.y | Int",
		"method Point.sum | fn sum() -> Int",
		"enum Shape",
		"variant Shape.Circle | Circle(Float)",
		"variant Shape.Dot | Dot",
		"constant limit",
		"function double | fn double(n: Int) -> Int",
		"function main | fn main() -> Int",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("unexpected outline:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := Outline("main.qi", "fn main( {"); len(got) != 0 {
		t.Errorf("expected nothing for a file that cannot be read, got %v", got)
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		line, column       int
		signature, doc     string
		file               string
		declaredAt, extent [2]int
	}{
		// A function of the file, with the comment above it
		{23, 17, "fn double(n: Int) -> Int", "Twice a number.", "main.qi", [2]int{19, 4}, [2]int{17, 23}},
		// A method, through a value
		{23, 26, "fn Point.sum() -> Int", "Both coordinates added.", "main.qi", [2]int{11, 8}, [2]int{26, 29}},
		// A function of the library, named through its module: one name
		{23, 40, "fn Int.abs() -> Int", "", "<std>/math.qi", [2]int{213, 12}, [2]int{40, 43}},
		{24, 24, "fn map<U>(convert: fn(T) -> U) -> [U]", "What the function makes of each item, in the same order.", "<std>/arrays.qi", [2]int{879, 12}, [2]int{24, 27}},
		// A struct where a value of it is made
		{22, 14, "struct Point {\n    x: Int;\n    y: Int;\n}", "A place on the grid.", "main.qi", [2]int{7, 8}, [2]int{13, 18}},
		// Variables: a let, a parameter of a short function, a constant
		{23, 24, "let p: Point", "", "main.qi", [2]int{22, 9}, [2]int{24, 25}},
		{24, 33, "(parameter) n: Int", "", "main.qi", [2]int{24, 28}, [2]int{33, 34}},
		{25, 47, "const limit: Int", "", "main.qi", [2]int{16, 7}, [2]int{47, 52}},
		{26, 18, "let names: [Int]", "", "main.qi", [2]int{24, 9}, [2]int{18, 23}},
		// A field, an enum, a variant, a method of the library on a generic value
		{26, 15, "(field) Point.x: Int", "A place on the grid.", "main.qi", [2]int{7, 16}, [2]int{15, 16}},
		{26, 35, "enum Shape {\n    Circle(Float),\n    Dot,\n}", "", "main.qi", [2]int{14, 6}, [2]int{35, 40}},
		{26, 41, "(variant) Shape.Dot", "", "main.qi", [2]int{14, 29}, [2]int{41, 44}},
		{26, 26, "(operator) wait -> result.Result<Int, result.Failure>", "Waits until what is waited for has come, and gives it: `wait value`\nis this function, written as a word.", "<std>/task.qi", [2]int{85, 8}, [2]int{25, 29}},
		// A builtin has no place
		{26, 5, "fn println(args: ...Any)", "", "", [2]int{0, 0}, [2]int{5, 12}},
	}
	for _, c := range cases {
		d := Describe("main.qi", sample, c.line, c.column)
		if d == nil {
			t.Errorf("%d:%d: expected %q, got nothing", c.line, c.column, c.signature)
			continue
		}
		file := d.File
		if !strings.HasPrefix(file, "<") && file != "" {
			file = filepath.Base(file)
		}
		if d.Signature != c.signature || d.Doc != c.doc || file != c.file || [2]int{d.Line, d.Column} != c.declaredAt || [2]int{d.StartColumn, d.EndColumn} != c.extent {
			t.Errorf("%d:%d: unexpected description:\n got %+v\nwant %+v", c.line, c.column, *d, c)
		}
	}

	// Where there is no name: a space, a number, a keyword
	for _, at := range [][2]int{{23, 16}, {22, 24}, {22, 5}, {1, 1}, {400, 1}} {
		if d := Describe("main.qi", sample, at[0], at[1]); d != nil {
			t.Errorf("%d:%d: expected nothing, got %+v", at[0], at[1], *d)
		}
	}
	// A program that cannot be understood says nothing rather than something wrong
	if d := Describe("main.qi", edit(23, "    let total = double(;"), 22, 14); d != nil {
		t.Errorf("expected nothing from a program with a mistake, got %+v", *d)
	}
}

func TestComplete(t *testing.T) {
	labels := func(items []Completion) string {
		var out []string
		for _, c := range items {
			out = append(out, c.Label+":"+c.Kind)
		}
		return strings.Join(out, " ")
	}
	has := func(items []Completion, label, kind, detail string) bool {
		for _, c := range items {
			if c.Label == label && c.Kind == kind && (detail == "" || c.Detail == detail) {
				return true
			}
		}
		return false
	}

	// After a dot: what the name before it has. The line being typed does
	// not read as a program, which is the usual case
	if got := labels(Complete("main.qi", edit(26, "    p."), 26, 7)); got != "x:field y:field sum:method" {
		t.Errorf("after a struct value: %s", got)
	}
	if got := labels(Complete("main.qi", edit(26, "    let s = Shape.Ci"), 26, 21)); got != "Circle:variant Dot:variant" {
		t.Errorf("after an enum: %s", got)
	}
	job := Complete("main.qi", edit(26, "    job."), 26, 9)
	if !has(job, "isDone", "method", "") || has(job, "take", "method", "") || has(job, "ready", "method", "") {
		t.Errorf("after a value of a struct of the library: %s", labels(job))
	}
	// What a module keeps to itself is not suggested: abs is a method now
	module := Complete("main.qi", edit(26, "    let z = math."), 26, 18)
	if !has(module, "pi", "constant", "") || has(module, "abs", "", "") || has(module, "println", "", "") {
		t.Errorf("after a module: %s", labels(module))
	}
	// Only what the module makes public: Outcome is its own business
	tasks := Complete("main.qi", edit(26, "    task."), 26, 10)
	if !has(tasks, "start", "function", "") || !has(tasks, "Channel", "struct", "") || has(tasks, "Outcome", "", "") || has(tasks, "spawn", "", "") {
		t.Errorf("after std.task: %s", labels(tasks))
	}
	// An array has the methods the library gives arrays
	names := Complete("main.qi", edit(26, "    names."), 26, 11)
	if !has(names, "len", "method", "fn len() -> Int") || !has(names, "map", "method", "") || !has(names, "filter", "method", "") || has(names, "trim", "", "") {
		t.Errorf("after an array: %s", labels(names))
	}

	// Anywhere else: what is in reach
	plain := Complete("main.qi", edit(26, "    to"), 26, 7)
	for _, want := range [][3]string{
		{"total", "variable", "Int"}, {"p", "variable", "Point"}, {"names", "variable", "[Int]"}, {"job", "variable", "task.Task<Int>"},
		{"double", "function", "fn double(n: Int) -> Int"}, {"Point", "struct", ""}, {"Shape", "enum", ""}, {"limit", "constant", ""},
		{"math", "module", "std.math"}, {"Task", "function", "std.task"}, {"println", "function", "builtin"}, {"return", "keyword", ""},
	} {
		if !has(plain, want[0], want[1], want[2]) {
			t.Errorf("expected %v among %s", want, labels(plain))
		}
	}
	// Not what is declared further down, nor what belongs to the library
	early := Complete("main.qi", edit(23, "    "), 23, 5)
	if has(early, "names", "", "") || has(early, "job", "", "") || !has(early, "p", "variable", "") || has(early, "abs", "", "") {
		t.Errorf("at line 23: %s", labels(early))
	}
}

// errorsOf keeps the errors of what Check says: the sample imports more
// than it uses, for the questions asked of its imports.
func errorsOf(all []Diagnostic) []Diagnostic {
	var errs []Diagnostic
	for _, d := range all {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	return errs
}

func TestCheck(t *testing.T) {
	if got := errorsOf(Check("main.qi", sample)); len(got) != 0 {
		t.Errorf("expected nothing to say about the sample, got %+v", got)
	}
	got := errorsOf(Check("main.qi", sample+"fn bad() -> Int { return nope; }\n"))
	if len(got) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", got)
	}
	want := Diagnostic{Severity: "error", Code: "E1001", Message: "undefined identifier 'nope'", File: "main.qi", Line: 29, Column: 26, EndColumn: 30, Suggestion: "Did you mean 'Shape'?"}
	if got[0] != want {
		t.Errorf("unexpected diagnostic:\n got %+v\nwant %+v", got[0], want)
	}
	// A warning is not an error, and a mistake of syntax has a place too
	if got := Check("main.qi", "fn main() -> Int {\n    return 0;\n    println(1);\n}\n"); len(got) != 1 || got[0].Severity != "warning" || got[0].Line != 3 {
		t.Errorf("expected a warning on line 3, got %+v", got)
	}
	if got := Check("main.qi", "fn main( {"); len(got) == 0 || got[0].Severity != "error" || got[0].Line != 1 {
		t.Errorf("expected an error on line 1, got %+v", got)
	}
}

// A file being edited is what importing it gives, saved or not.
func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.qi")
	helper := filepath.Join(dir, "helper.qi")
	mainSrc := "import helper;\n\nfn main() -> Int {\n    return helper.twice(2);\n}\n"
	os.WriteFile(main, []byte(mainSrc), 0o644)
	os.WriteFile(helper, []byte("pub fn twice(n: Int) -> Int { return n * 2; }\n"), 0o644)

	if got := Check(main, mainSrc); len(got) != 0 {
		t.Fatalf("expected the program on disk to be fine, got %+v", got)
	}
	// The function is renamed in the editor, not on disk
	Overlay(helper, "pub fn double(n: Int) -> Int { return n * 2; }\n")
	if got := Check(main, mainSrc); len(got) != 1 || !strings.Contains(got[0].Message, "twice") {
		t.Errorf("expected the edited module to be the one imported, got %+v", got)
	}
	if got := Complete(main, mainSrc, 4, 19); len(got) != 1 || got[0].Label != "double" {
		t.Errorf("expected the edited module's names, got %+v", got)
	}
	// A mistake in the module is said to be in the module
	Overlay(helper, "pub fn twice(n: Int) -> Int { return n * missing; }\n")
	if got := Check(main, mainSrc); len(got) != 1 || got[0].File != helper || got[0].Line != 1 {
		t.Errorf("expected a diagnostic in the module, got %+v", got)
	}
	Forget(helper)
	if got := Check(main, mainSrc); len(got) != 0 {
		t.Errorf("expected the file on disk again, got %+v", got)
	}

	// Where a name of another file is declared
	if d := Describe(main, mainSrc, 4, 20); d == nil || d.File != helper || d.Line != 1 || d.Column != 8 || d.Signature != "fn helper.twice(n: Int) -> Int" {
		t.Errorf("expected the declaration in the module, got %+v", d)
	}
}

// The names of types where they are written, and names where they are
// declared, are described like the names used in expressions.
func TestDescribeTypesAndDeclarations(t *testing.T) {
	src := `import std.http;
import std.task.{Task};

// A box for one thing.
struct Box<T> { item: T; next: Box<T>?; }

trait Named { fn name() -> String; }

enum Colour { Red, Mix(Int, Float) }

// Answers a request.
fn handle(request: http.Request, items: [Int], job: Task<Box<Colour>>) -> http.Response? {
    let count: Int = 1;
    let pick = fn(c: Colour) -> Bool { return c is Colour; };
    return null;
}

fn main() -> Int { return 0; }
`
	type want struct {
		line, column   int
		signature, doc string
		file           string
		declaredAt     [2]int
		extent         [2]int
	}
	for _, c := range []want{
		// Types of the language: a word about each, no place
		{13, 16, "Int", "A whole number, from -140737488355328 to 140737488355327.", "", [2]int{0, 0}, [2]int{16, 19}},
		{12, 42, "Int", "A whole number, from -140737488355328 to 140737488355327.", "", [2]int{0, 0}, [2]int{42, 45}},
		{7, 28, "String", "A text.", "", [2]int{0, 0}, [2]int{28, 34}},
		{14, 33, "Bool", "true or false.", "", [2]int{0, 0}, [2]int{33, 37}},
		// Types of the program, inside others too
		{12, 58, "struct Box<T> {\n    item: T;\n    next: Box<T>?;\n}", "A box for one thing.", "main.qi", [2]int{5, 8}, [2]int{58, 61}},
		{12, 62, "enum Colour {\n    Red,\n    Mix(Int, Float),\n}", "", "main.qi", [2]int{9, 6}, [2]int{62, 68}},
		{14, 22, "enum Colour {\n    Red,\n    Mix(Int, Float),\n}", "", "main.qi", [2]int{9, 6}, [2]int{22, 28}},
		{14, 52, "enum Colour {\n    Red,\n    Mix(Int, Float),\n}", "", "main.qi", [2]int{9, 6}, [2]int{52, 58}},
		{5, 33, "struct Box<T> {\n    item: T;\n    next: Box<T>?;\n}", "A box for one thing.", "main.qi", [2]int{5, 8}, [2]int{32, 35}},
		{5, 23, "(type parameter) T", "", "", [2]int{0, 0}, [2]int{23, 24}},
		// Types of the library, by their module or by name
		{12, 25, "", "What someone asked a server.", "<std>/http.qi", [2]int{182, 12}, [2]int{20, 32}},
		{12, 53, "", "A function that is running, or has run, on its own.", "<std>/task.qi", [2]int{47, 12}, [2]int{53, 57}},
		{12, 80, "", "What a server answers, and what a server of your own answers with.", "<std>/http.qi", [2]int{0, 12}, [2]int{75, 88}},
		// Names where they are declared
		{12, 5, "fn handle(request: Request, items: [Int], job: Task<Box<Colour>>) -> Response?", "Answers a request.", "main.qi", [2]int{12, 4}, [2]int{4, 10}},
		{12, 12, "(parameter) request: Request", "Answers a request.", "main.qi", [2]int{12, 11}, [2]int{11, 18}},
		{13, 10, "let count: Int", "", "main.qi", [2]int{13, 9}, [2]int{9, 14}},
		{14, 10, "let pick: fn(Colour) -> Bool", "", "main.qi", [2]int{14, 9}, [2]int{9, 13}},
		{5, 9, "struct Box<T> {\n    item: T;\n    next: Box<T>?;\n}", "A box for one thing.", "main.qi", [2]int{5, 8}, [2]int{8, 11}},
		{5, 18, "(field) Box.item: T", "A box for one thing.", "main.qi", [2]int{5, 17}, [2]int{17, 21}},
		{9, 20, "(variant) Colour.Mix(Int, Float)", "", "main.qi", [2]int{9, 20}, [2]int{20, 23}},
		{7, 8, "trait Named {\n    fn name() -> String;\n}", "", "main.qi", [2]int{7, 7}, [2]int{7, 12}},
		{7, 18, "fn Named.name() -> String", "", "main.qi", [2]int{7, 18}, [2]int{18, 22}},
	} {
		d := Describe("main.qi", src, c.line, c.column)
		if d == nil {
			t.Errorf("%d:%d: expected %q, got nothing", c.line, c.column, c.signature)
			continue
		}
		file := d.File
		if !strings.HasPrefix(file, "<") && file != "" {
			file = filepath.Base(file)
		}
		// What a struct of the library holds, and where exactly, is the library's to change
		if strings.HasPrefix(c.file, "<std>") {
			if d.Doc != c.doc || file != c.file || d.Line == 0 || [2]int{d.StartColumn, d.EndColumn} != c.extent || !strings.HasPrefix(d.Signature, "struct ") {
				t.Errorf("%d:%d: unexpected description of a library type:\n got %+v\nwant %+v", c.line, c.column, *d, c)
			}
			continue
		}
		if d.Signature != c.signature || d.Doc != c.doc || file != c.file || [2]int{d.Line, d.Column} != c.declaredAt || [2]int{d.StartColumn, d.EndColumn} != c.extent {
			t.Errorf("%d:%d: unexpected description:\n got %+v\nwant %+v", c.line, c.column, *d, c)
		}
	}
}

// A module is a file without a main: nothing to say about that.
func TestModulesAreNotFlaggedForHavingNoMain(t *testing.T) {
	if got := Check("shapes.qi", "pub fn square(side: Float) -> Float { return side * side; }\n"); len(got) != 0 {
		t.Errorf("expected nothing to say about a module, got %+v", got)
	}
	if got := Check("shapes.qi", "pub fn square(side: Float) -> Float { return side * sides; }\n"); len(got) != 1 || got[0].Code != "E1001" {
		t.Errorf("expected the mistake in the module, got %+v", got)
	}
}

// The files of the library are inside qoqi; an editor needs one on disk
// to jump to.
func TestLibraryFilesOnDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	path := OnDisk("<std>/math.qi")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "pub fn abs(") {
		t.Fatalf("expected std.math written out at %q, got %v", path, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o222 != 0 {
		t.Errorf("expected the file not to be writable, got %v", info.Mode())
	}
	// Asked again, and after someone spoiled it: the same file, put right
	os.Chmod(path, 0o644)
	os.WriteFile(path, []byte("spoiled"), 0o644)
	if again := OnDisk("<std>/math.qi"); again != path {
		t.Errorf("expected the same path, got %q", again)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "pub fn abs(") {
		t.Error("expected the file to be put right")
	}
	if got := OnDisk("/somewhere/main.qi"); got != "/somewhere/main.qi" {
		t.Errorf("expected any other path as it is, got %q", got)
	}
	if got := OnDisk("<std>/nothing.qi"); got != "" {
		t.Errorf("expected nothing for a file the library does not have, got %q", got)
	}
}

// A file of the library opened in an editor is a module of std: its
// extern functions are the runtime's, and what is wrong in it is said at
// its own lines.
func TestCheckLibraryFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	for _, name := range []string{"path", "math", "fs", "task", "result", "http"} {
		copied := OnDisk("<std>/" + name + ".qi")
		data, err := os.ReadFile(copied)
		if err != nil {
			t.Fatalf("std.%s: %v", name, err)
		}
		for _, path := range []string{copied, filepath.Join("checkout", "internal", "stdlib", "lib", name+".qi")} {
			if got := Check(path, string(data)); len(got) != 0 {
				t.Errorf("%s: expected nothing to report, got %+v", path, got)
			}
		}
	}

	path := OnDisk("<std>/path.qi")
	data, _ := os.ReadFile(path)
	broken := strings.Replace(string(data), "return joined(parts);", "return joined(part);", 1)
	got := Check(path, broken)
	if len(got) != 1 || got[0].Code != "E1001" || got[0].File != path || !strings.Contains(strings.Split(broken, "\n")[got[0].Line-1], "joined(part)") {
		t.Errorf("expected the undefined name at its line of %s, got %+v", path, got)
	}
	// A file of the same name anywhere else is a program like any other
	if got := Check(filepath.Join(t.TempDir(), "path.qi"), string(data)); len(got) == 0 {
		t.Error("expected a file outside the library to be checked as a program")
	}
}

// On an import line what is suggested is modules, then what a module
// makes public.
func TestCompleteImports(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(text), 0o644)
		return path
	}
	main := write("main.qi", "fn main() -> Int { return 0; }\n")
	write("helper.qi", "pub fn twice(n: Int) -> Int { return n * 2; }\nfn hidden() { }\npub struct Pair { a: Int; b: Int; }\n")
	write("shop/cart.qi", "pub fn total() -> Int { return 0; }\n")
	write("shop/stock/items.qi", "pub const count = 3;\n")
	write("notes/readme.txt", "not a module")
	write("2fast.qi", "")
	write("vendor/geo/shapes.qi", "pub fn area() -> Int { return 1; }\n")
	write("qoqi.json", `{"packages": {"geo": "vendor/geo"}}`)

	at := func(line string) string {
		src := line + "\nfn main() -> Int { return 0; }\n"
		var out []string
		for _, c := range Complete(main, src, 1, len([]rune(line))+1) {
			out = append(out, c.Label+":"+c.Kind)
		}
		return strings.Join(out, " ")
	}

	// The library's modules, all of them, and nothing else
	std := at("import std.")
	for _, want := range []string{"arrays:module", "http:module", "task:module", "compiler:module", "regex:module"} {
		if !strings.Contains(" "+std+" ", " "+want+" ") {
			t.Errorf("expected %s after 'import std.', got %s", want, std)
		}
	}
	if strings.Contains(std, "keyword") || strings.Contains(std, "helper") || len(strings.Fields(std)) != len(Complete(main, "import std.", 1, 12)) {
		t.Errorf("expected only the library's modules, got %s", std)
	}
	if got := at("import std.ta"); got != std {
		t.Errorf("expected the same while a name is being typed, got %s", got)
	}
	if got := at("pub import std.math."); got != "" {
		t.Errorf("expected nothing under a module of the library, got %s", got)
	}

	// Where a path starts: the library, the project's packages, the program's own modules and folders
	if got, want := at("import "), "geo:module helper:module shop:module std:module vendor:module"; got != want {
		t.Errorf("after 'import ':\n got %s\nwant %s", got, want)
	}
	if got, want := at("import shop."), "cart:module stock:module"; got != want {
		t.Errorf("in a folder: got %s, want %s", got, want)
	}
	if got, want := at("import shop.stock.i"), "items:module"; got != want {
		t.Errorf("in a folder of a folder: got %s, want %s", got, want)
	}
	if got, want := at("import geo."), "shapes:module"; got != want {
		t.Errorf("in a package: got %s, want %s", got, want)
	}
	if got := at("import nothing.here."); got != "" {
		t.Errorf("expected nothing where there is no folder, got %s", got)
	}

	// Between the braces: what the module makes public
	if got, want := at("import helper.{"), "twice:function Pair:struct"; got != want {
		t.Errorf("names of a module: got %s, want %s", got, want)
	}
	if got := at("import std.task.{Task, "); !strings.Contains(got, "Channel:struct") || !strings.Contains(got, "start:function") || strings.Contains(got, "spawn") {
		t.Errorf("names of a module of the library: got %s", got)
	}
	// Once the line is written there is nothing to add; and a line that
	// only mentions the word is not an import
	if got := at("import std.math;"); got != "" {
		t.Errorf("expected nothing after the semicolon, got %s", got)
	}
	if got := at("import std.math as "); got != "" {
		t.Errorf("expected nothing for a name of one's own, got %s", got)
	}
	if got := at("    let important = "); !strings.Contains(got, "return:keyword") {
		t.Errorf("expected the usual suggestions on any other line, got %s", got)
	}
}

// Everything that names something says what, wherever it stands: on an
// import line, in a pattern, in a loop, in a struct being made.
func TestDescribeEverywhere(t *testing.T) {
	src := `import std.math;
import std.task.{Task, start};
import std.result.{Result};

enum Shape { Circle(Float), Rect(Float, Float) }
struct Price { cents: Int; label: String; }

trait Named { fn name() -> String; }
impl Named for Price {
    fn name() -> String { return self.label; }
}

fn area(shape: Shape) -> Float {
    match shape {
        case Shape.Circle(r): { return 3.0 * r * r; }
        case Shape.Rect(w, _): { return w; }
    }
}

fn main() -> Int {
    let price = Price { cents: 100, label: "tea" };
    for i, cost in [price.cents] { println(i, cost); }
    let job = start(n -> n);
    match wait job {
        case Result.Ok(value): { println(value); }
        case Result.Err(failure): { println(failure.message); }
    }
    return price.cents.abs();
}
`
	for _, c := range []struct {
		line, column int
		signature    string
		doc          string // Its first line
		at           string // file:line of the declaration, "" for none
	}{
		// Import lines: the module, and what is taken from it
		{1, 8, "module std.math", "Numbers.", "<std>/math.qi:1"},
		{1, 13, "module std.math", "Numbers.", "<std>/math.qi:1"},
		{2, 14, "module std.task", "Doing several things at once, and waiting for them.", "<std>/task.qi:1"},
		{2, 19, "struct task.Task<T> {", "A function that is running, or has run, on its own.", "<std>/task.qi:192"},
		{2, 25, "fn task.start<T>(work: fn() -> T) -> Task<T>", "Runs a function on its own and returns at once. The task starts when", "<std>/task.qi:199"},
		{3, 22, "enum result.Result<T, E> {", "The value a function produced, or the reason it could not.", "<std>/result.qi:27"},
		// The struct and the trait of an impl
		{9, 7, "trait Named {", "", "main.qi:8"},
		{9, 18, "struct Price {", "", "main.qi:6"},
		// Patterns: the enum, the variant, and the variables they make
		{15, 16, "enum Shape {", "", "main.qi:5"},
		{15, 22, "(variant) Shape.Circle(Float)", "", "main.qi:5"},
		{15, 27, "(variable) r: Float", "", "main.qi:15"},
		{16, 25, "(variable) w: Float", "", "main.qi:16"},
		{25, 14, "enum result.Result<T, E> {", "The value a function produced, or the reason it could not.", "<std>/result.qi:27"},
		{25, 24, "(variable) value: ", "", "main.qi:25"},
		// Loops and short functions
		{22, 9, "(variable) i: Int", "", "main.qi:22"},
		{22, 12, "(variable) cost: Int", "", "main.qi:22"},
		{23, 21, "(parameter) n: ", "", "main.qi:23"},
		// The fields of a struct being made
		{21, 25, "(field) Price.cents: Int", "", "main.qi:6"},
		{21, 37, "(field) Price.label: String", "", "main.qi:6"},
	} {
		d := Describe("main.qi", src, c.line, c.column)
		if d == nil {
			t.Errorf("%d:%d: expected %q, got nothing", c.line, c.column, c.signature)
			continue
		}
		at := ""
		if d.File != "" {
			file := d.File
			if !strings.HasPrefix(file, "<") {
				file = filepath.Base(file)
			}
			at = file + ":" + strconv.Itoa(d.Line)
		}
		doc, _, _ := strings.Cut(d.Doc, "\n")
		if !strings.HasPrefix(d.Signature, c.signature) || doc != c.doc || at != c.at {
			t.Errorf("%d:%d: unexpected description:\n got %q | %q | %s\nwant %q | %q | %s", c.line, c.column, d.Signature, doc, at, c.signature, c.doc, c.at)
		}
	}
	// Keywords, `_` and what a text holds are not names
	for _, at := range [][2]int{{1, 1}, {14, 5}, {16, 28}, {21, 46}, {22, 20}, {9, 1}, {9, 12}} {
		if d := Describe("main.qi", src, at[0], at[1]); d != nil {
			t.Errorf("%d:%d: expected nothing, got %+v", at[0], at[1], *d)
		}
	}
}

// After a dot that follows a call, a chain of calls, an index or a
// field: what the value there has, whatever is being typed around it.
func TestCompleteAfterAnExpression(t *testing.T) {
	src := `import std.list;
import std.http;
import std.json;
import std.fs;
import std.task;

const PORT = 8094;

struct Shelf { books: [Book]; }
struct Book { title: String; pages: Int; }
impl Book {
    fn longer(than: Int) -> Bool { return self.pages > than; }
}
impl Shelf {
    fn first() -> Book { return self.books[0]; }
}

fn shelf() -> Shelf { return Shelf { books: [] }; }

fn main() -> Int {
    let names = list.of<String>(["Nghokeng", "Daniel"]);
    let api = http.router();
    let tree = list.of(fs.list(".").or([])).map(entries -> entries.name);
    api.get("/", fn(request: http.Request) -> http.Response {
        return http.sendJson(
            200,
            json.object()
                .text("hello", "world")
                .set("files", json.texts(tree.toArray()))
                .done()
        );
    });
    println("Listening on ${PORT}");
    http.serve(PORT, api.handler());
    return 0;
}

fn after() -> Int { return shelf().first().pages; }
`
	// typed gives what is suggested once `text` has replaced a line and
	// the cursor stands at its end
	typed := func(line int, text string) string {
		t.Helper()
		lines := strings.Split(src, "\n")
		lines[line-1] = text
		var out []string
		for _, c := range Complete("main.qi", strings.Join(lines, "\n"), line, len([]rune(text))+1) {
			out = append(out, c.Label)
		}
		return " " + strings.Join(out, " ") + " "
	}
	expect := func(what, got string, has []string, hasNot ...string) {
		t.Helper()
		for _, label := range has {
			if !strings.Contains(got, " "+label+" ") {
				t.Errorf("%s: expected %s among%s", what, label, got)
			}
		}
		for _, label := range hasNot {
			if strings.Contains(got, " "+label+" ") {
				t.Errorf("%s: did not expect %s among%s", what, label, got)
			}
		}
	}
	builder := []string{"text", "int", "set", "bool", "done"}

	// In the middle of a chain written one call per line, inside the
	// arguments of a call, inside a function passed to another
	expect("after a call of a module", typed(28, "                ."), builder, "return", "println")
	expect("further down the chain", typed(29, "                ."), builder, "return")
	expect("with a name being typed", typed(29, "                .te"), builder)
	expect("at the end of a chain", typed(30, `                .set("files", json.texts(tree.toArray())).`), builder)
	// On one line, and as a statement of its own
	expect("on one line", typed(22, `    json.object().text("a", "b").`), builder)
	expect("a call on a variable", typed(22, "    names.filter(n -> true)."), []string{"add", "map", "len", "toArray"}, "done")
	expect("a call with type arguments", typed(22, `    list.of<String>(["a"]).`), []string{"add", "len"})
	expect("a method of the program", typed(22, "    shelf().first()."), []string{"title", "pages", "longer"}, "books")
	expect("an index", typed(22, "    shelf().books[0]."), []string{"title", "pages", "longer"})
	expect("a field of a field", typed(22, "    shelf().first().title."), nil, "title", "return")
	expect("a value that may be missing", typed(22, "    let n = names.first()?."), nil, "return")
	expect("a router", typed(34, "    api."), []string{"get", "post", "handler"})
	expect("a task", typed(34, "    task.start(fn() -> Int { return 1; })."), []string{"isDone"}, "take", "ready", "watch")
	// What needs the lines above: a variable of the function, used in a chain
	expect("a variable declared above", typed(34, "    tree.toArray()."), nil, "return")
	expect("a list declared above", typed(34, "    tree.filter(name -> true)."), []string{"add", "len"})
	// The parameter of the function the cursor is in
	expect("a parameter of a function expression", typed(26, "        request."), []string{"method", "path", "header", "json"})
	// Where there is no dot before the cursor, the usual
	expect("no dot", typed(34, "    "), []string{"return", "names", "api", "shelf"}, "text")
	expect("a number", typed(34, "    let x = 3."), nil, "return", "text")
}

// After a value of a struct of another module, its priv fields are not
// offered; after the name of a type, its static functions are.
func TestCompletePrivAndStatic(t *testing.T) {
	dir := t.TempDir()
	geo := "pub struct Point { x: Int; priv secret: Int = 0; }\nimpl Point {\n    pub static fn origin() -> Point { return Point { x: 0 }; }\n    pub fn moved(dx: Int) -> Point { return Point { x: self.x + dx }; }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "geo.qi"), []byte(geo), 0o644); err != nil {
		t.Fatal(err)
	}
	labels := func(src string, line, column int) string {
		var out []string
		for _, c := range Complete(filepath.Join(dir, "main.qi"), src, line, column) {
			out = append(out, c.Label)
		}
		return " " + strings.Join(out, " ") + " "
	}
	value := "import geo.{Point};\nfn main() -> Int {\n    let p = Point.origin();\n    p.\n    return 0;\n}\n"
	if got := labels(value, 4, 7); !strings.Contains(got, " x ") || !strings.Contains(got, " moved ") || strings.Contains(got, " secret ") {
		t.Errorf("after a value: got%s", got)
	}
	typeName := "import geo.{Point};\nfn main() -> Int {\n    Point.\n    return 0;\n}\n"
	if got := labels(typeName, 3, 11); !strings.Contains(got, " origin ") {
		t.Errorf("after the type: got%s", got)
	}

	// Hover says a field is priv
	own := "struct Safe { priv code: Int; }\nfn main() -> Int { let s = Safe { code: 1 }; return s.code; }\n"
	if d := Describe(filepath.Join(dir, "own.qi"), own, 2, 55); d == nil || !strings.Contains(d.Signature, "priv") {
		t.Errorf("hover on a priv field: got %+v", d)
	}
}
