package modules

import (
	"strings"
	"testing"
)

// lib is a module most error cases import.
const lib = `
	pub struct Point { x: Int; }
	struct Secret { x: Int; }
	pub trait Shape { fn area() -> Int; }
	pub fn make(x: Int) -> Point { return Point { x: x }; }
	fn helper() -> Int { return 1; }
`

func TestLoadErrors(t *testing.T) {
	entry := func(imports, body string) files {
		return files{
			"main.qi":       imports + "\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n",
			"lib.qi":        lib,
			"other.qi":      `pub struct Point { y: Int; } pub fn make() -> Int { return 1; }`,
			"geo/shapes.qi": `pub fn area() -> Int { return 1; }`,
		}
	}

	tests := []struct {
		name       string
		fs         files
		code       string
		msg        string
		suggestion string
	}{
		// Finding modules
		{"missing module", entry("import nowhere;", ""),
			ErrModuleNotFound, "module 'nowhere' not found", "Expected the file nowhere.qi"},
		{"missing module in a folder", entry("import geo.volumes;", ""),
			ErrModuleNotFound, "module 'geo.volumes' not found", "Expected the file geo/volumes.qi"},
		{"folder is not a module", entry("import geo;", ""),
			ErrModuleNotFound, "module 'geo' not found", "Expected the file geo.qi"},
		{"name imported as a module", entry("import lib.make;", ""),
			ErrModuleNotFound, "module 'lib.make' not found", "write: import lib.{make};"},
		{"name the module does not have", entry("import lib.nothing;", ""),
			ErrModuleNotFound, "module 'lib.nothing' not found", "Expected the file lib/nothing.qi"},
		{"re-exported name imported as a module", files{
			"main.qi":  "import front.make;\nfn main() -> Int { return 0; }",
			"front.qi": "pub import lib.{make};",
			"lib.qi":   lib,
		}, ErrModuleNotFound, "module 'front.make' not found", "write: import front.{make};"},
		{"entry file", entry("import main;", ""),
			ErrInvalidImport, "cannot import 'main': it is the program's entry file", "module of its own"},
		{"module not imported", entry("", "let p = lib.make(1);"),
			"", "", ""}, // Left to name resolution: lib is an unknown name
		{"qualified type without import", entry("import other;", "let p: lib.Point = other.make();"),
			ErrModuleNotFound, "'lib' is not an imported module", "Add: import lib;"},
		{"qualified struct without import", entry("import lib;", "let p = lob.Point { x: 1 };"),
			ErrModuleNotFound, "'lob' is not an imported module", "Did you mean 'lib'?"},

		// Names of a module
		{"unknown function", entry("import lib;", "let p = lib.mak(1);"),
			ErrUnknownName, "module 'lib' has no 'mak'", "Did you mean 'make'?"},
		{"unknown type", entry("import lib;", "let p: lib.Pint = lib.make(1);"),
			ErrUnknownName, "module 'lib' has no 'Pint'", "Did you mean 'Point'?"},
		{"unknown struct literal", entry("import lib;", "let p = lib.Circle { x: 1 };"),
			ErrUnknownName, "module 'lib' has no 'Circle'", ""},
		{"unknown name in a list", entry("import lib.{make, Shap};", ""),
			ErrUnknownName, "module 'lib' has no 'Shap'", "Did you mean 'Shape'?"},
		{"private function", entry("import lib;", "let n = lib.helper();"),
			ErrPrivate, "function 'helper' of module 'lib' is private", "Declare it with 'pub' in lib.qi"},
		{"private struct", entry("import lib;", "let s = lib.Secret { x: 1 };"),
			ErrPrivate, "struct 'Secret' of module 'lib' is private", ""},
		{"private type", entry("import lib;", "let s: lib.Secret? = null;"),
			ErrPrivate, "struct 'Secret' of module 'lib' is private", ""},
		{"private name in a list", entry("import lib.{helper};", ""),
			ErrPrivate, "function 'helper' of module 'lib' is private", ""},
		{"private type in a pattern", entry("import lib;", "match 1 { case lib.Secret: { return 1; } default: { return 2; } }"),
			ErrPrivate, "struct 'Secret' of module 'lib' is private", ""},
		// A private name is not suggested
		{"no private suggestion", entry("import lib;", "let n = lib.helpr();"),
			ErrUnknownName, "module 'lib' has no 'helpr'", "-"},

		// Modules, types and values are different things
		{"module as a value", entry("import lib;", "let m = lib;"),
			ErrNotAValue, "'lib' is a module and cannot be used as a value", "lib.name"},
		{"module as an argument", entry("import lib;", "println(lib);"),
			ErrNotAValue, "'lib' is a module and cannot be used as a value", ""},
		{"module as a type", entry("import lib;", "let m: lib? = null;"),
			ErrNotAValue, "'lib' is a module, not a type", "lib.Name"},
		{"type as a value", entry("import lib;", "let t = lib.Point;"),
			ErrNotAValue, "'lib.Point' is a struct and cannot be used as a value", "lib.Point { field: value }"},
		{"trait as a value", entry("import lib;", "let t = lib.Shape();"),
			ErrNotAValue, "'lib.Shape' is a trait and cannot be used as a value", ""},
		{"function as a type", entry("import lib;", "let f: lib.make? = null;"),
			ErrNotAValue, "'lib.make' is a function, not a type", ""},

		// Clashes
		{"two modules under one name", entry("import lib;\nimport other as lib;", ""),
			ErrNameConflict, "'lib' already names the module 'lib'", "import other as other;"},
		{"same last segment", files{"main.qi": "import a.util;\nimport b.util;\nfn main() -> Int { return 0; }", "a/util.qi": "", "b/util.qi": ""},
			ErrNameConflict, "'util' already names the module 'a.util'", "import b.util as other;"},
		{"module and own function", entry("import lib;\nfn lib() -> Int { return 1; }", ""),
			ErrNameConflict, "'lib' is both an imported module and a function of this file", ""},
		{"module and own struct", entry("import lib;\nstruct lib { x: Int; }", ""),
			ErrNameConflict, "'lib' is both an imported module and a struct of this file", ""},
		{"imported name and own function", entry("import lib.{make};\nfn make() -> Int { return 1; }", ""),
			ErrNameConflict, "'make' is already a function of this file", "write lib.make instead"},
		{"imported name and own struct", entry("import lib.{Point};\nstruct Point { z: Int; }", ""),
			ErrNameConflict, "'Point' is already a struct of this file", ""},
		{"same name from two modules", entry("import lib.{Point};\nimport other.{Point};", ""),
			ErrNameConflict, "'Point' is already imported from 'lib'", "write other.Point instead"},
		{"imported name and module", entry("import other;\nimport geo.shapes.{area};\nimport lib.{make};\nimport geo.shapes as make;", ""),
			ErrNameConflict, "'make' is both an imported module and a function imported from 'lib'", ""},
		{"module and imported name", entry("import geo.shapes as make;\nimport lib.{make};", ""),
			ErrNameConflict, "'make' already names the module 'geo.shapes'", ""},
		{"struct named like a module", files{
			"main.qi": "import a;\nimport a.B;\nfn main() -> Int { return 0; }",
			"a.qi":    "pub struct B { x: Int; }",
			"a/B.qi":  "pub fn m() -> Int { return 1; }",
		}, ErrNameConflict, "struct 'B' of module 'a' has the same name as the module 'a.B'", "a/B.qi"},

		// Re-exports
		{"re-exported module", entry("pub import lib;", ""),
			ErrInvalidImport, "a module cannot be exported again as a whole", "pub import lib.{Name};"},
		{"re-export of an unknown name", entry("pub import lib.{nothing};", ""),
			ErrUnknownName, "module 'lib' has no 'nothing'", ""},
		{"re-export of a private name", files{
			"main.qi":  "import front.{helper};\nfn main() -> Int { return 0; }",
			"front.qi": "pub import lib.{helper};",
			"lib.qi":   lib,
		}, ErrPrivate, "function 'helper' of module 'lib' is private", ""},
		{"re-exports going in a circle", files{
			"main.qi": "import a.{ghost};\nfn main() -> Int { return 0; }",
			"a.qi":    "pub import b.{ghost};",
			"b.qi":    "pub import a.{ghost};",
		}, ErrUnknownName, "has no 'ghost'", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := load(tt.fs)

			if tt.code == "" {
				if errs.HasErrors() {
					t.Fatalf("expected no diagnostic from the loader, got: %s", errs.Diagnostics[0].Message)
				}
				return
			}
			if len(errs.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != tt.code {
				t.Errorf("expected code %s, got %s (%s)", tt.code, d.Code, d.Message)
			}
			if !strings.Contains(d.Message, tt.msg) {
				t.Errorf("expected message containing %q, got %q", tt.msg, d.Message)
			}
			switch {
			case tt.suggestion == "-":
				if d.Suggestion != "" {
					t.Errorf("expected no suggestion, got %q", d.Suggestion)
				}
			case !strings.Contains(d.Suggestion, tt.suggestion):
				t.Errorf("expected suggestion containing %q, got %q", tt.suggestion, d.Suggestion)
			}
			if d.Context == "" {
				t.Error("expected the diagnostic to carry the source line")
			}
		})
	}
}

func TestDiagnosticsQuoteTheirOwnFile(t *testing.T) {
	_, errs, _ := load(files{
		"main.qi": "import a;\n\n\n\nfn main() -> Int { return 0; }",
		"a.qi":    "import deep.b;\npub fn f() -> Int { return b.missing(); }",
		"deep/b.qi": "import a;\n" +
			"pub fn g() -> Int {\n" +
			"    return a.nothing();\n" +
			"}\n",
	})

	if len(errs.Diagnostics) != 2 {
		t.Fatalf("expected two diagnostics, got %d: %v", len(errs.Diagnostics), errs.Diagnostics)
	}
	first, second := errs.Diagnostics[0], errs.Diagnostics[1]

	if first.Pos.File != "a.qi" || first.Pos.Line != 2 || first.Context != "pub fn f() -> Int { return b.missing(); }" {
		t.Errorf("expected the first diagnostic to quote a.qi:2, got %s: %q", first.Pos, first.Context)
	}
	// Underlined from the module name to the end of the member
	if len(first.Highlights) != 1 || first.Highlights[0].EndCol-first.Highlights[0].StartCol != len("b.missing") {
		t.Errorf("expected 'b.missing' to be underlined, got %+v", first.Highlights)
	}
	if second.Pos.File != "deep/b.qi" || second.Pos.Line != 3 || second.Context != "    return a.nothing();" {
		t.Errorf("expected the second diagnostic to quote deep/b.qi:3, got %s: %q", second.Pos, second.Context)
	}
	if !strings.Contains(second.String(), "--> deep/b.qi:3:12") {
		t.Errorf("expected the rendering to name the file, got:\n%s", second)
	}
}

func TestSyntaxErrorInAModule(t *testing.T) {
	prog, errs, _ := load(files{
		"main.qi": "import broken;\nimport fine;\nfn main() -> Int { return fine.one(); }",
		"broken.qi": "pub fn f() -> Int {\n" +
			"    let x = ;\n" +
			"    return 1;\n" +
			"}\n",
		"fine.qi": "pub fn one() -> Int { return 1 }",
	})

	// Every file is parsed, so both mistakes are reported in one run
	if len(errs.Diagnostics) != 2 {
		t.Fatalf("expected two diagnostics, got %d: %v", len(errs.Diagnostics), errs.Diagnostics)
	}
	if d := errs.Diagnostics[0]; d.Pos.File != "broken.qi" || d.Pos.Line != 2 || d.Context != "    let x = ;" {
		t.Errorf("expected a syntax error at broken.qi:2, got %s: %q", d.Pos, d.Context)
	}
	if d := errs.Diagnostics[1]; d.Pos.File != "fine.qi" || !strings.Contains(d.Message, "expected ';'") {
		t.Errorf("expected a missing semicolon in fine.qi, got %s: %s", d.Pos, d.Message)
	}
	if prog == nil {
		t.Error("expected what was parsed of the entry file")
	}
}

func TestAllImportProblemsAreReported(t *testing.T) {
	_, errs, _ := load(files{
		"main.qi": `
			import lib;
			import lib.{helper, nothing, make};
			fn main() -> Int {
				let a = lib.helper();
				let b = lib.Secret { x: 1 };
				let c: lib.Missing? = null;
				return make(1).x;
			}
		`,
		"lib.qi": lib,
	})

	var got []string
	for _, d := range errs.Diagnostics {
		got = append(got, d.Code)
	}
	want := "E8003 E8002 E8003 E8003 E8002"
	if strings.Join(got, " ") != want {
		t.Errorf("expected %s, got %s", want, strings.Join(got, " "))
	}
}
