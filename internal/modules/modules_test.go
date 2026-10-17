package modules

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// files is a program held in memory: file path to source text. The entry
// file is main.qi.
type files map[string]string

// load runs the loader on files and returns the merged program, the
// diagnostics and the files that were read, in order.
func load(fs files) (*ast.Program, *errors.Handler, []string) {
	return loadWith(fs, "main.qi", Options{})
}

// loadWith is load with an entry file and options of the caller's choice.
func loadWith(fs files, entry string, opts Options) (*ast.Program, *errors.Handler, []string) {
	var read []string
	errs := errors.NewHandler()
	opts.Read = func(path string) (string, error) {
		read = append(read, path)
		src, ok := fs[path]
		if !ok {
			return "", fmt.Errorf("no such file: %s", path)
		}
		return src, nil
	}
	prog := Load(fs[entry], entry, opts, errs)
	return prog, errs, read
}

// loadOK is load for programs that must load without diagnostics.
func loadOK(t *testing.T, fs files) *ast.Program {
	t.Helper()
	prog, errs, _ := load(fs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}
	return prog
}

// decl finds a top-level declaration by its program-wide name.
func decl(t *testing.T, prog *ast.Program, name string) ast.Decl {
	t.Helper()
	for _, d := range prog.Decls {
		switch d := d.(type) {
		case *ast.FnDecl:
			if d.Name == name {
				return d
			}
		case *ast.StructDecl:
			if d.Name == name {
				return d
			}
		case *ast.TraitDecl:
			if d.Name == name {
				return d
			}
		}
	}
	t.Fatalf("no declaration named %s in:\n%s", name, prog)
	return nil
}

func body(t *testing.T, prog *ast.Program, fn string) string {
	t.Helper()
	return decl(t, prog, fn).(*ast.FnDecl).Body.String()
}

func TestSingleFileIsUnchanged(t *testing.T) {
	src := `
		struct Point { x: Int; }
		trait Named { fn name() -> String; }
		impl Named for Point { fn name() -> String { return "p"; } }
		fn helper(p: Point) -> Int { return p.x; }
		fn main() -> Int { return helper(Point { x: 1 }); }
	`
	prog := loadOK(t, files{"main.qi": src})

	if len(prog.Decls) != 5 || len(prog.Imports) != 0 {
		t.Fatalf("expected the five declarations and no imports, got:\n%s", prog)
	}
	for _, name := range []string{"Point", "Named", "helper", "main"} {
		decl(t, prog, name)
	}
	if got := body(t, prog, "main"); got != "{ return helper(Point { x: 1 }); }" {
		t.Errorf("expected the body to be left alone, got %s", got)
	}
	if fn := decl(t, prog, "main").(*ast.FnDecl); fn.Module != "" {
		t.Errorf("expected the entry file to be the module \"\", got %q", fn.Module)
	}
}

func TestNamesBecomeProgramWide(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import player;
			import physics.body;
			import physics.body as pb;
			import physics.collide.{overlaps, Shape};

			struct Wall { x: Int; }
			impl Shape for Wall { fn left() -> Int { return self.x; } }

			fn main() -> Int {
				let a = body.new(1);
				let b: body.Body = body.Body { x: 2 };
				let c = pb.count<body.Body>([a, b]);
				let d = player.spawn("ada");
				let e: [Shape] = [a, Wall { x: 3 }];
				let f = a is body.Body;
				let g = (b as Shape).left();
				let h = body.new;
				if overlaps(a, b) { return d.body.x; }
				return c;
			}
		`,
		"player.qi": `
			import physics.body.{Body, new};
			pub struct Player { name: String; body: Body; }
			pub fn spawn(name: String) -> Player { return Player { name: name, body: new(0) }; }
		`,
		"physics/body.qi": `
			import physics.collide.{Shape};
			pub struct Body { x: Int; }
			impl Shape for Body { fn left() -> Int { return self.x; } }
			pub fn new(x: Int) -> Body { return checked(Body { x: x }); }
			fn checked(b: Body) -> Body { return b; }
			pub fn count<T>(items: [T]) -> Int { return __len(items); }
		`,
		"physics/collide.qi": `
			pub trait Shape { fn left() -> Int; }
			pub fn overlaps(a: Shape, b: Shape) -> Bool { return a.left() == b.left(); }
		`,
	})

	// Declarations outside the entry file carry their module's path
	for _, name := range []string{
		"Wall", "main",
		"player.Player", "player.spawn",
		"physics.body.Body", "physics.body.new", "physics.body.checked", "physics.body.count",
		"physics.collide.Shape", "physics.collide.overlaps",
	} {
		decl(t, prog, name)
	}
	if len(prog.Imports) != 0 {
		t.Errorf("expected no imports in the merged program, got %v", prog.Imports)
	}

	// References through a module, an alias, and a list of names
	main := body(t, prog, "main")
	for _, want := range []string{
		"let a = physics.body.new(1);",
		"let b: physics.body.Body = physics.body.Body { x: 2 };",
		"let c = physics.body.count<physics.body.Body>([a, b]);",
		`let d = player.spawn("ada");`,
		"let e: [physics.collide.Shape] = [a, Wall { x: 3 }];",
		"let f = (a is physics.body.Body);",
		"let g = (b as physics.collide.Shape).left();",
		"let h = physics.body.new;",
		"if physics.collide.overlaps(a, b) { return d.body.x; }",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("expected main to contain %q, got:\n%s", want, main)
		}
	}

	// Inside a module, its own names and the ones it imports
	if got := body(t, prog, "player.spawn"); got != "{ return player.Player { body: physics.body.new(0), name: name }; }" &&
		got != "{ return player.Player { name: name, body: physics.body.new(0) }; }" {
		t.Errorf("unexpected body of player.spawn: %s", got)
	}
	if got := body(t, prog, "physics.body.new"); got != "{ return physics.body.checked(physics.body.Body { x: x }); }" {
		t.Errorf("unexpected body of physics.body.new: %s", got)
	}
	player := decl(t, prog, "player.Player").(*ast.StructDecl)
	if got := player.Fields[1].Type.String(); got != "physics.body.Body" {
		t.Errorf("expected the field to name the imported struct, got %s", got)
	}
	overlaps := decl(t, prog, "physics.collide.overlaps").(*ast.FnDecl)
	if got := overlaps.Params[0].Type.String(); got != "physics.collide.Shape" {
		t.Errorf("expected the parameter to name the module's trait, got %s", got)
	}

	// Impl blocks name their struct and trait the same way
	var impls []string
	for _, d := range prog.Decls {
		if impl, ok := d.(*ast.ImplDecl); ok {
			impls = append(impls, impl.TraitName+" for "+impl.StructName)
		}
	}
	want := "physics.collide.Shape for Wall, physics.collide.Shape for physics.body.Body"
	if got := strings.Join(impls, ", "); got != want {
		t.Errorf("expected impls %q, got %q", want, got)
	}
}

func TestLocalNamesHideModuleNames(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import util;
			import util.{twice, Pair};
			struct Thing { size: Int; }

			fn helper() -> Int { return 1; }

			fn params(util: Thing, twice: Int, helper: Int) -> Int {
				return util.size + twice + helper;
			}
			fn lets() -> Int {
				let before = twice(1);
				let twice = 5;
				let util = Thing { size: 2 };
				return before + twice + util.size;
			}
			fn blocks() -> Int {
				if true {
					let helper = 2;
					println(helper);
				}
				return helper();
			}
			fn loops(items: [Thing]) -> Int {
				for twice, util in items { println(twice, util.size); }
				return twice(util.one());
			}
			fn lambdas() -> Int {
				let f = fn(util: Thing) -> Int { return util.size; };
				return f(Thing { size: util.one() });
			}
			fn generic<Pair>(value: Pair) -> Pair { return value; }
			fn concrete(value: Pair) -> Pair { return value; }
			fn main() -> Int { return 0; }
		`,
		"util.qi": `
			pub struct Pair { a: Int; }
			pub fn twice(n: Int) -> Int { return n * 2; }
			pub fn one() -> Int { return 1; }
			pub struct Box<T> { value: T; }
			impl<T> Box<T> { pub fn get() -> T { return self.value; } }
			pub fn wrap<T>(value: T) -> Box<T> { return Box<T> { value: value }; }
		`,
	})

	tests := []struct{ fn, want string }{
		// Parameters hide a module, an imported function and a local one
		{"params", "{ return ((util.size + twice) + helper); }"},
		// A variable hides the function only after its declaration
		{"lets", "{ let before = util.twice(1); let twice = 5; let util = Thing { size: 2 }; return ((before + twice) + util.size); }"},
		// ...and only inside its block
		{"blocks", "{ if true { let helper = 2; println(helper); } return helper(); }"},
		// Loop variables hide for the body of the loop
		{"loops", "{ for twice, util in items { println(twice, util.size); } return util.twice(util.one()); }"},
		// Lambda parameters hide for the lambda
		{"lambdas", "{ let f = fn(util: Thing) -> Int { return util.size; }; return f(Thing { size: util.one() }); }"},
	}
	for _, tt := range tests {
		if got := body(t, prog, tt.fn); got != tt.want {
			t.Errorf("%s:\n  want %s\n   got %s", tt.fn, tt.want, got)
		}
	}

	// A type parameter hides an imported type of the same name
	generic := decl(t, prog, "generic").(*ast.FnDecl)
	if got := generic.Params[0].Type.String(); got != "Pair" {
		t.Errorf("expected the type parameter to win, got %s", got)
	}
	concrete := decl(t, prog, "concrete").(*ast.FnDecl)
	if got := concrete.ReturnType.String(); got != "util.Pair" {
		t.Errorf("expected the imported struct, got %s", got)
	}

	// Type parameters of a module's own generics are left alone
	wrap := decl(t, prog, "util.wrap").(*ast.FnDecl)
	if got := wrap.ReturnType.String(); got != "util.Box<T>" {
		t.Errorf("unexpected return type %s", got)
	}
	if got := wrap.Body.String(); got != "{ return util.Box<T> { value: value }; }" {
		t.Errorf("unexpected body %s", got)
	}
}

func TestMatchPatternsOnImportedTypes(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import shapes;
			import shapes.{Circle};
			struct Local { x: Int; }

			fn main() -> Int {
				let value: Any = 1;
				match value {
					case Circle: { return 1; }
					case shapes.Square: { return 2; }
					case shapes.Box<Int>: { return 3; }
					case [shapes.Square]: { return 4; }
					case Local: { return 5; }
					case 6: { return 6; }
					default: { return 0; }
				}
			}
		`,
		"shapes.qi": `
			pub struct Circle { r: Int; }
			pub struct Square { side: Int; }
			pub struct Box<T> { value: T; }
		`,
	})

	match := decl(t, prog, "main").(*ast.FnDecl).Body.Stmts[1].(*ast.MatchStmt)
	wants := []string{"shapes.Circle", "shapes.Square", "shapes.Box<Int>", "[shapes.Square]", "Local"}
	for i, want := range wants {
		typ, ok := match.Cases[i].Pattern.(*ast.Type)
		if !ok {
			t.Errorf("case %d: expected a type pattern, got %T", i, match.Cases[i].Pattern)
			continue
		}
		if typ.String() != want {
			t.Errorf("case %d: expected %s, got %s", i, want, typ)
		}
	}
	if _, ok := match.Cases[5].Pattern.(*ast.Literal); !ok {
		t.Errorf("expected the literal pattern to stay a literal, got %T", match.Cases[5].Pattern)
	}
}

func TestMethodsRecordModuleAndVisibility(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import shapes;
			impl shapes.Square { fn local() -> Int { return 1; } }
			fn main() -> Int { return 0; }
		`,
		"shapes.qi": `
			pub trait Shape { fn area() -> Int; }
			pub struct Square { side: Int; }
			impl Shape for Square { fn area() -> Int { return self.side; } }
			impl Square {
				pub fn shown() -> Int { return self.hidden(); }
				fn hidden() -> Int { return self.side; }
			}
		`,
	})

	type method struct {
		module string
		pub    bool
	}
	got := make(map[string]method)
	for _, d := range prog.Decls {
		switch d := d.(type) {
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				got[d.StructName+"."+m.Name] = method{m.Module, m.Pub}
			}
		case *ast.TraitDecl:
			for _, m := range d.Methods {
				got[d.Name+"."+m.Name] = method{m.Module, m.Pub}
			}
		}
	}
	want := map[string]method{
		"shapes.Shape.area":    {"shapes", true}, // What a trait declares
		"shapes.Square.area":   {"shapes", true}, // What implements it
		"shapes.Square.shown":  {"shapes", true},
		"shapes.Square.hidden": {"shapes", false},
		"shapes.Square.local":  {"", false}, // Added by the entry file
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: expected %+v, got %+v", name, w, got[name])
		}
	}
}

func TestReexports(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import physics;
			import physics.{Body};
			import front.{make};
			fn main() -> Int {
				let a: Body = physics.new(1);
				let b: physics.Body = make(2);
				return a.x + b.x;
			}
		`,
		// A front door for a folder: a file next to it passing names on
		"physics.qi": `
			pub import physics.body.{Body, new};
		`,
		// A chain of two, written before what it passes on
		"front.qi": `
			pub import middle.{make};
		`,
		"middle.qi": `
			pub import physics.body.{new};
			pub import physics.body.{make};
		`,
		"physics/body.qi": `
			pub struct Body { x: Int; }
			pub fn new(x: Int) -> Body { return Body { x: x }; }
			pub fn make(x: Int) -> Body { return new(x); }
		`,
	})

	want := "{ let a: physics.body.Body = physics.body.new(1); let b: physics.body.Body = physics.body.make(2); return (a.x + b.x); }"
	if got := body(t, prog, "main"); got != want {
		t.Errorf("expected re-exported names to lead to the declaration:\n  want %s\n   got %s", want, got)
	}
}

func TestModulesMayImportEachOther(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import even;
			fn main() -> Int { if even.isEven(10) { return 1; } return 0; }
		`,
		"even.qi": `
			import odd;
			import even;
			pub fn isEven(n: Int) -> Bool { if n == 0 { return true; } return odd.isOdd(n - 1); }
			pub fn same(n: Int) -> Bool { return even.isEven(n); }
		`,
		"odd.qi": `
			import even.{isEven};
			pub fn isOdd(n: Int) -> Bool { if n == 0 { return false; } return isEven(n - 1); }
		`,
	})

	if got := body(t, prog, "odd.isOdd"); !strings.Contains(got, "return even.isEven((n - 1));") {
		t.Errorf("unexpected body of odd.isOdd: %s", got)
	}
	// A module may name itself
	if got := body(t, prog, "even.same"); got != "{ return even.isEven(n); }" {
		t.Errorf("unexpected body of even.same: %s", got)
	}
}

func TestEachFileIsReadOnce(t *testing.T) {
	_, errs, read := load(files{
		"main.qi": `
			import a;
			import b;
			import a as again;
			import a.{f};
			fn main() -> Int { return 0; }
		`,
		"a.qi":      `import b; import deep.c; pub fn f() -> Int { return 1; }`,
		"b.qi":      `import a; import deep.c;`,
		"deep/c.qi": `import a;`,
	})
	if errs.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", errs.Diagnostics)
	}
	if got, want := strings.Join(read, " "), "a.qi b.qi deep/c.qi"; got != want {
		t.Errorf("expected the files to be read once each, in order of first import: %q, got %q", want, got)
	}
}

func TestSameNameInTwoModules(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import a;
			import b;
			struct Node { own: Int; }
			fn make() -> Node { return Node { own: 0 }; }
			fn main() -> Int { return a.make().value + b.make().size + make().own; }
		`,
		"a.qi": `pub struct Node { value: Int; } pub fn make() -> Node { return Node { value: 1 }; }`,
		"b.qi": `pub struct Node { size: Int; } pub fn make() -> Node { return Node { size: 2 }; }`,
	})

	for _, name := range []string{"Node", "a.Node", "b.Node", "make", "a.make", "b.make"} {
		decl(t, prog, name)
	}
	if got := body(t, prog, "b.make"); got != "{ return b.Node { size: 2 }; }" {
		t.Errorf("unexpected body of b.make: %s", got)
	}
}

func TestRootOtherThanTheEntryFolder(t *testing.T) {
	// The entry file is two folders below the root; paths still start at
	// the root, for it and for every module
	fs := files{
		"proj/src/cmd/tool/main.qi": `
			import lib.text;
			import cmd.tool.flags;
			fn main() -> Int { return text.size(flags.name()); }
		`,
		"proj/src/cmd/tool/flags.qi": `import lib.text; pub fn name() -> String { return text.same("tool"); }`,
		"proj/src/lib/text.qi":       `pub fn size(s: String) -> Int { return __len(s); } pub fn same(s: String) -> String { return s; }`,
		// Next to the entry file, where it would be found without a root
		"proj/src/cmd/tool/lib/text.qi": `pub fn size(s: String) -> Int { return 0 - 1; }`,
	}
	prog, errs, read := loadWith(fs, "proj/src/cmd/tool/main.qi", Options{Root: "proj/src"})
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}

	if got, want := strings.Join(read, " "), "proj/src/lib/text.qi proj/src/cmd/tool/flags.qi"; got != want {
		t.Errorf("expected the files under the root to be read: %q, got %q", want, got)
	}
	for _, name := range []string{"main", "lib.text.size", "cmd.tool.flags.name"} {
		decl(t, prog, name)
	}

	// The entry file has a module path now, and still cannot be imported
	fs["proj/src/lib/text.qi"] = `import cmd.tool.main; pub fn size(s: String) -> Int { return 1; }`
	_, errs, _ = loadWith(fs, "proj/src/cmd/tool/main.qi", Options{Root: "proj/src"})
	if len(errs.Diagnostics) == 0 || errs.Diagnostics[0].Code != ErrInvalidImport {
		t.Errorf("expected the entry file to be refused as a module, got %v", errs.Diagnostics)
	}

	// A missing module is looked for under the root, and the message says so
	fs["proj/src/lib/text.qi"] = `import lib.nothing;`
	_, errs, _ = loadWith(fs, "proj/src/cmd/tool/main.qi", Options{Root: "proj/src"})
	if len(errs.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if d := errs.Diagnostics[0]; d.Code != ErrModuleNotFound ||
		!strings.Contains(d.Suggestion, "Expected the file proj/src/lib/nothing.qi") ||
		!strings.Contains(d.Suggestion, "the root of the project, proj/src") {
		t.Errorf("unexpected diagnostic: %s: %s (%s)", d.Code, d.Message, d.Suggestion)
	}
}

func TestPackages(t *testing.T) {
	opts := Options{Packages: map[string]string{
		"std":    "vendor/std",
		"shapes": "../shared/shapes",
	}}
	fs := files{
		"main.qi": `
			import std;
			import std.list;
			import std.text.pad.{left};
			import shapes.circle as c;
			import util;

			fn main() -> Int {
				let l = list.of(1);
				let s: std.Version = std.version();
				println(left("x", 3), c.area(2), util.helper());
				return l.size + s.major;
			}
		`,
		"util.qi": `import std.list; pub fn helper() -> Int { return list.of(2).size; }`,

		// `import std;` is the file next to the package's folder
		"vendor/std.qi": `
			pub import std.list.{List};
			pub struct Version { major: Int; }
			pub fn version() -> Version { return Version { major: 1 }; }
		`,
		// Modules of a package name each other through the package
		"vendor/std/list.qi": `
			import std.text.pad;
			pub struct List { size: Int; }
			pub fn of(n: Int) -> List { return List { size: pad.width(n) }; }
		`,
		"vendor/std/text/pad.qi": `
			pub fn left(s: String, n: Int) -> String { return s; }
			pub fn width(n: Int) -> Int { return n; }
		`,
		"../shared/shapes/circle.qi": `pub fn area(r: Int) -> Int { return 3 * r * r; }`,

		// A folder of the program named like a package is never looked at
		"std/list.qi": `pub fn of(n: Int) -> Int { return n; }`,
	}

	prog, errs, read := loadWith(fs, "main.qi", opts)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	// The program calls methods, and std is one of its packages: the
	// modules that would give the builtin types theirs are looked for
	// there, once each, and are not there
	want := "vendor/std/arrays.qi vendor/std/strings.qi vendor/std/math.qi vendor/std/option.qi " +
		"vendor/std.qi vendor/std/list.qi vendor/std/text/pad.qi ../shared/shapes/circle.qi util.qi"
	if got := strings.Join(read, " "); got != want {
		t.Errorf("expected the files\n  %s\ngot\n  %s", want, got)
	}

	// Names carry the package's name, not its folder
	for _, name := range []string{"std.Version", "std.version", "std.list.List", "std.list.of", "std.text.pad.left", "shapes.circle.area", "util.helper"} {
		decl(t, prog, name)
	}
	main := body(t, prog, "main")
	for _, want := range []string{
		"let l = std.list.of(1);",
		"let s: std.Version = std.version();",
		`println(std.text.pad.left("x", 3), shapes.circle.area(2), util.helper());`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("expected main to contain %q, got:\n%s", want, main)
		}
	}
	if got := body(t, prog, "std.list.of"); got != "{ return std.list.List { size: std.text.pad.width(n) }; }" {
		t.Errorf("unexpected body of std.list.of: %s", got)
	}

	// A module missing from a package is looked for in the package's folder
	fs["main.qi"] = "import std.maps;\nfn main() -> Int { return 0; }"
	_, errs, _ = loadWith(fs, "main.qi", opts)
	if len(errs.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if d := errs.Diagnostics[0]; d.Code != ErrModuleNotFound ||
		!strings.Contains(d.Suggestion, "Expected the file vendor/std/maps.qi: 'std' is a package in the folder vendor/std") {
		t.Errorf("unexpected diagnostic: %s: %s (%s)", d.Code, d.Message, d.Suggestion)
	}

	// Without the package, the same import is an ordinary path from the root
	_, errs, _ = loadWith(fs, "main.qi", Options{})
	if d := errs.Diagnostics[0]; !strings.Contains(d.Suggestion, "Expected the file std/maps.qi") {
		t.Errorf("expected a lookup under the root, got %s", d.Suggestion)
	}
}

func TestExternFunctionsAreSetApart(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import sys.clock;
			import sys.clock.{now};
			extern fn beep();
			fn main() -> Int {
				beep();
				let f = clock.now;
				return now() + clock.elapsed() + f();
			}
		`,
		"sys/clock.qi": `
			pub extern fn now() -> Int;
			extern fn raw(scale: Int) -> Int;
			pub fn elapsed() -> Int { return raw(1000) - now(); }
		`,
	})

	// Not among the declarations to compile...
	for _, d := range prog.Decls {
		if fn, ok := d.(*ast.FnDecl); ok && (fn.Extern || fn.Body == nil) {
			t.Errorf("did not expect %s among the declarations", fn.Name)
		}
	}
	// ...but kept, under the names the runtime will be asked for
	var names []string
	for _, fn := range prog.Externs {
		names = append(names, fn.Name)
		if !fn.Extern || fn.Body != nil {
			t.Errorf("%s: expected an extern declaration", fn.Name)
		}
	}
	if got := strings.Join(names, " "); got != "beep sys.clock.now sys.clock.raw" {
		t.Errorf("unexpected extern functions: %s", got)
	}
	if clock := prog.Externs[1]; clock.Module != "sys.clock" || !clock.Pub {
		t.Errorf("expected the extern to keep its module and visibility, got %q, pub %v", clock.Module, clock.Pub)
	}

	// References to them are rewritten like references to any function
	if got := body(t, prog, "main"); got != "{ beep(); let f = sys.clock.now; return ((sys.clock.now() + sys.clock.elapsed()) + f()); }" {
		t.Errorf("unexpected body of main: %s", got)
	}
	if got := body(t, prog, "sys.clock.elapsed"); got != "{ return (sys.clock.raw(1000) - sys.clock.now()); }" {
		t.Errorf("unexpected body of elapsed: %s", got)
	}

	// An extern function is private unless pub, like any other
	_, errs, _ := load(files{
		"main.qi":      "import sys.clock;\nfn main() -> Int { return clock.raw(1); }",
		"sys/clock.qi": "extern fn raw(scale: Int) -> Int;",
	})
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != ErrPrivate {
		t.Errorf("expected the extern to be private, got %v", errs.Diagnostics)
	}
}

func TestConstantsAcrossModules(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import config;
			import config.{LIMIT, NAME};
			import config as c;

			const DOUBLE = LIMIT * 2 + config.STEP;

			fn main() -> Int {
				let a = LIMIT + config.LIMIT + c.LIMIT + DOUBLE;
				let LIMIT = 5;
				return a + LIMIT + __len(NAME);
			}
			fn shadow(STEP: Int) -> Int { return STEP + config.STEP; }
		`,
		"config.qi": `
			pub const LIMIT = 100;
			pub const STEP = BASE + 1;
			pub const NAME: String = "app";
			const BASE = 1;
			pub fn scaled(n: Int) -> Int { return n * STEP + BASE; }
		`,
	})

	consts := make(map[string]*ast.ConstDecl)
	for _, d := range prog.Decls {
		if c, ok := d.(*ast.ConstDecl); ok {
			consts[c.Name] = c
		}
	}
	// A constant of a module carries the module's path; one of the entry
	// file keeps its name
	for _, name := range []string{"DOUBLE", "config.LIMIT", "config.STEP", "config.NAME", "config.BASE"} {
		if consts[name] == nil {
			t.Errorf("expected a constant named %s, got %v", name, consts)
		}
	}
	if c := consts["config.STEP"]; c.Module != "config" || !c.Pub || c.Value.String() != "(config.BASE + 1)" {
		t.Errorf("unexpected constant: %s (module %q, pub %v)", c, c.Module, c.Pub)
	}
	if got := consts["DOUBLE"].Value.String(); got != "((config.LIMIT * 2) + config.STEP)" {
		t.Errorf("unexpected value of DOUBLE: %s", got)
	}

	// Every way of naming it leads to the same constant; a local hides it
	// from its declaration on
	want := "{ let a = (((config.LIMIT + config.LIMIT) + config.LIMIT) + DOUBLE); let LIMIT = 5; return ((a + LIMIT) + __len(config.NAME)); }"
	if got := body(t, prog, "main"); got != want {
		t.Errorf("unexpected body of main:\n  want %s\n   got %s", want, got)
	}
	if got := body(t, prog, "shadow"); got != "{ return (STEP + config.STEP); }" {
		t.Errorf("expected the parameter to hide nothing but the bare name, got %s", got)
	}
	if got := body(t, prog, "config.scaled"); got != "{ return ((n * config.STEP) + config.BASE); }" {
		t.Errorf("unexpected body of scaled: %s", got)
	}

	// Private unless pub, like everything else
	for _, src := range []string{
		"import config;\nfn main() -> Int { return config.BASE; }",
		"import config.{BASE};\nfn main() -> Int { return BASE; }",
	} {
		_, errs, _ := load(files{"main.qi": src, "config.qi": "const BASE = 1;"})
		if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != ErrPrivate || !strings.Contains(errs.Diagnostics[0].Message, "constant 'BASE' of module 'config' is private") {
			t.Errorf("expected the constant to be private, got %v", errs.Diagnostics)
		}
	}
}

func TestEnumsAcrossModules(t *testing.T) {
	prog := loadOK(t, files{
		"main.qi": `
			import geo.shapes;
			import geo.shapes.{Shape, Result};
			import geo.shapes as g;

			enum Local { One, Two(Shape) }

			fn make() -> [Shape] {
				return [Shape.Circle(1.0), shapes.Shape.Empty, g.Shape.Rect(1.0, 2.0)];
			}
			fn ok() -> Result<Shape, String> { return Result<Shape, String>.Ok(Shape.Empty); }
			fn local() -> Local { return Local.Two(shapes.Shape.Empty); }
			fn asFunction() -> fn(Float) -> Shape { return shapes.Shape.Circle; }

			fn describe(value: Any, Shape: Int) -> Int {
				match value {
					case shapes.Shape.Circle(r): { return 1; }
					case g.Shape.Rect(w, _): { return 2; }
					case Local.Two(shapes.Shape.Empty): { return 3; }
					case shapes.Shape: { return 4; }
					case shapes.Point: { return 5; }
					default: { return Shape; }
				}
			}
			fn scoped(s: shapes.Shape, shapes: Int, r: Int) -> Int {
				match s {
					case Shape.Circle(r): { return 1; }
					case Shape.Rect(shapes, g): { return 2; }
					default: { return shapes + r; }
				}
			}
		`,
		"geo/shapes.qi": `
			pub enum Shape { Circle(Float), Rect(Float, Float), Empty }
			pub enum Result<T, E> { Ok(T), Err(E) }
			pub struct Point { x: Int; }
			enum Hidden { A }
			pub fn empty() -> Shape { return Shape.Empty; }
			pub fn isEmpty(s: Shape) -> Bool {
				match s { case Shape.Empty: { return true; } default: { return false; } }
			}
		`,
	})

	for _, name := range []string{"Local"} {
		found := false
		for _, d := range prog.Decls {
			if e, ok := d.(*ast.EnumDecl); ok && e.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an enum named %s", name)
		}
	}
	var enums []string
	for _, d := range prog.Decls {
		if e, ok := d.(*ast.EnumDecl); ok {
			enums = append(enums, e.Name)
		}
	}
	if got := strings.Join(enums, " "); got != "Local geo.shapes.Shape geo.shapes.Result geo.shapes.Hidden" {
		t.Errorf("unexpected enums: %s", got)
	}

	// The enum is named in full wherever it is used to reach a variant
	tests := []struct{ fn, want string }{
		{"make", "{ return [geo.shapes.Shape.Circle(1.0), geo.shapes.Shape.Empty, geo.shapes.Shape.Rect(1.0, 2.0)]; }"},
		{"ok", "{ return geo.shapes.Result<geo.shapes.Shape, String>.Ok(geo.shapes.Shape.Empty); }"},
		{"local", "{ return Local.Two(geo.shapes.Shape.Empty); }"},
		{"asFunction", "{ return geo.shapes.Shape.Circle; }"},
		{"geo.shapes.empty", "{ return geo.shapes.Shape.Empty; }"},
	}
	for _, tt := range tests {
		if got := body(t, prog, tt.fn); got != tt.want {
			t.Errorf("%s:\n  want %s\n   got %s", tt.fn, tt.want, got)
		}
	}
	if got := decl(t, prog, "ok").(*ast.FnDecl).ReturnType.String(); got != "geo.shapes.Result<geo.shapes.Shape, String>" {
		t.Errorf("unexpected return type: %s", got)
	}

	// Patterns: a variant through a module or an alias, a variant inside a
	// variant, and `module.Type`, which only looks like one
	match := decl(t, prog, "describe").(*ast.FnDecl).Body.Stmts[0].(*ast.MatchStmt)
	patterns := []string{
		"geo.shapes.Shape.Circle(r)", "geo.shapes.Shape.Rect(w, _)", "Local.Two(geo.shapes.Shape.Empty)",
		"geo.shapes.Shape", "geo.shapes.Point",
	}
	for i, want := range patterns {
		if got := match.Cases[i].Pattern.String(); got != want {
			t.Errorf("pattern %d: expected %s, got %s", i, want, got)
		}
	}
	for i := 0; i < 3; i++ {
		if _, ok := match.Cases[i].Pattern.(*ast.VariantPattern); !ok {
			t.Errorf("pattern %d: expected a variant pattern, got %T", i, match.Cases[i].Pattern)
		}
	}
	for i := 3; i < 5; i++ {
		if _, ok := match.Cases[i].Pattern.(*ast.Type); !ok {
			t.Errorf("pattern %d: expected a type pattern, got %T", i, match.Cases[i].Pattern)
		}
	}
	// A parameter called Shape hides the enum as a value...
	if got := match.Cases[5].Body.(*ast.BlockStmt).String(); got != "{ return Shape; }" {
		t.Errorf("expected the parameter, got %s", got)
	}

	// ...and the names a pattern binds hide modules and parameters, in
	// their own case only
	scoped := decl(t, prog, "scoped").(*ast.FnDecl).Body.Stmts[0].(*ast.MatchStmt)
	if got := scoped.Cases[1].Pattern.String(); got != "geo.shapes.Shape.Rect(shapes, g)" {
		t.Errorf("unexpected pattern: %s", got)
	}
	if got := scoped.Cases[2].Body.(*ast.BlockStmt).String(); got != "{ return (shapes + r); }" {
		t.Errorf("unexpected default body: %s", got)
	}

	// An enum is private unless pub; its variants come with it
	for _, src := range []string{
		"import geo.shapes;\nfn main() -> Int { let h = shapes.Hidden.A; return 0; }",
		"import geo.shapes.{Hidden};\nfn main() -> Int { return 0; }",
		"import geo.shapes;\nfn f(h: shapes.Hidden) -> Int { return 0; }\nfn main() -> Int { return 0; }",
		"import geo.shapes;\nfn f(v: Any) -> Int { match v { case shapes.Hidden.A: { return 1; } default: { return 0; } } }\nfn main() -> Int { return 0; }",
	} {
		_, errs, _ := load(files{"main.qi": src, "geo/shapes.qi": "enum Hidden { A }"})
		if len(errs.Diagnostics) == 0 || errs.Diagnostics[0].Code != ErrPrivate || !strings.Contains(errs.Diagnostics[0].Message, "enum 'Hidden' of module 'geo.shapes' is private") {
			t.Errorf("expected the enum to be private, got %v\n%s", errs.Diagnostics, src)
		}
	}
	// A variant is not a name of the module
	_, errs, _ := load(files{
		"main.qi":       "import geo.shapes;\nfn main() -> Int { let c = shapes.Circle(1.0); return 0; }",
		"geo/shapes.qi": "pub enum Shape { Circle(Float) }",
	})
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Code != ErrUnknownName {
		t.Errorf("expected the variant not to be found in the module, got %v", errs.Diagnostics)
	}
}

// The methods a module of the library gives are found in its text, before
// the module is read as a program: the functions written inside an impl.
func TestMethodNamesAreFoundInTheText(t *testing.T) {
	names := methodNames(`
// How many items an array holds.
fn len<T>(items: [T]) -> Int {
    return __len(items);
}

pub fn range(from: Int, to: Int) -> [Int] {
    let out: [Int] = [];
    return out;
}

impl<T> [T] {
    // How many items.
    pub fn len() -> Int {
        return len(self);
    }

    pub fn map<U>(convert: fn(T) -> U) -> [U] {
        return map(self, convert);
    }

	fn hidden() { }
}

impl [Int] {
    pub fn sum() -> Int { return sum(self); }
}
`)
	for _, want := range []string{"len", "map", "hidden", "sum"} {
		if !names[want] {
			t.Errorf("expected the method %s to be found, got %v", want, names)
		}
	}
	if names["range"] || len(names) != 4 {
		t.Errorf("expected the four methods and no function, got %v", names)
	}

	uses := func(members ...string) *ast.Program {
		prog := &ast.Program{Members: make(map[string]bool)}
		for _, member := range members {
			prog.Members[member] = true
		}
		return prog
	}
	if !callsMethod(uses("x", "sum"), names) || callsMethod(uses("x", "range"), names) || callsMethod(uses(), names) || callsMethod(nil, names) {
		t.Errorf("expected a file to call a method when it writes its name after a dot, and only then")
	}
	many := uses("a", "b", "c", "d", "e", "f", "g", "map")
	if !callsMethod(many, names) {
		t.Errorf("expected a file that writes many names to be found calling one of them")
	}
}
