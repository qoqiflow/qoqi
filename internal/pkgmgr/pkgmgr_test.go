package pkgmgr

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

// compile compiles a program that imports the modules of the package
// manager, as they are in the binary.
func compile(t *testing.T, src string) *compiler.Result {
	t.Helper()
	errs := errors.NewHandler()
	result := compiler.CompileOptions(src, Root+"/test_main.qi", Options(), errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}
	return result
}

// execute runs a program and gives what it printed.
func execute(t *testing.T, src string) string {
	t.Helper()
	result := compile(t, src)
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v\n%s", err, out.String())
	}
	return out.String()
}

// TestTheToolCompiles compiles the program qoqi pkg install runs.
func TestTheToolCompiles(t *testing.T) {
	compile(t, Source)
}

// catalogProgram is a catalog kept in memory, made from lines such as
//
//	acme/http 1.3.0 import=web : acme/log ^1.0, acme/json ~2.1
//
// and a main that resolves ROOTS with it, preferring PREFER, and prints
// the choices or why there are none.
const catalogProgram = `
import catalog;
import catalog.{Catalog, Candidate, Source};
import manifest;
import manifest.{Manifest};
import resolver;
import resolver.{Requirement};
import std.dict;
import std.dict.{Dict};
import std.semver;
import std.result.{Result};

struct Memory {
    lines: [String];
    asked: Dict<Int> = dict.empty<Int>();
}

impl Memory {
    fn parts(line: String) -> (String, String) {
        return line.cut(" : ") ?? (line, "");
    }
}

impl Catalog for Memory {
    fn candidates(name: String) -> Result<[Candidate], String> {
        self.asked.update(name, 0, n -> n + 1);

        if name == "broken/catalog" {
            return Result.Err("the catalog cannot be reached");
        }

        let found: [Candidate] = [];

        for line in self.lines {
            let words = self.parts(line).0.split(" ");

            if words[0] != name {
                continue;
            }

            let source = Source { kind: "git", url: "memory", reference: "ref-${words[1]}" };
            let version = semver.parse(words[1]).value();
            found.push(Candidate { name: name, label: labelOf(words[1]), version: version, source: source, anyVersion: words[1] == "path" });
        }

        return Result.Ok(found);
    }

    fn manifest(candidate: Candidate) -> Result<Manifest, String> {
        for line in self.lines {
            let (head, requires) = self.parts(line);
            let words = head.split(" ");
            if words[0] != candidate.name || labelOf(words[1]) != candidate.label {
                continue;
            }

            let found = Manifest { name: candidate.name, importName: manifest.defaultImport(candidate.name) };

            for word in words.drop(2) {
                if word.startsWith("import=") {
                    found.importName = word.from(7);
                }
            }

            for require in requires.split(",").map(part -> part.trim()).filter(part -> part != "") {
                let (required, wanted) = require.cut(" ") ?? (require, "*");
                found.require.set(required, wanted.trim());
            }

            return Result.Ok(found);
        }

        return Result.Err("no manifest for ${candidate.describe()}");
    }
}

// "1.0" is shown 1.0.0, "path" dev-path, a branch as it is.
fn labelOf(word: String) -> String {
    if word == "path" {
        return "dev-path";
    }

    let version = semver.parse(word).value();

    if version == null {
        return word;
    }

    return toString(version);
}

fn main() -> Int {
    let memory = Memory { lines: LINES };
    let roots = ROOTS.map(root -> {
        let (name, wanted) = root.cut(" ") ?? (root, "*");
        return Requirement { name: name, wanted: wanted, by: "qoqi.json" };
    });

    let prefer = dict.empty<String>();

    for entry in PREFER {
        let (name, label) = entry.cut("=") ?? (entry, "");
        prefer.set(name, label);
    }

    match resolver.resolve(memory, roots, prefer) {
        case Result.Ok(choices): {
            println(choices.map(choice -> "${choice.candidate.name} ${choice.candidate.label}").sortedTexts().join(", "));
        }

        case Result.Err(reason): {
            println("error: " + reason);
        }
    }

    for name in memory.asked.sortedKeys() {
        if (memory.asked.get(name) ?? 0) > 1 {
            println("asked more than once for ${name}");
        }
    }

    return 0;
}
`

func quoted(items []string) string {
	var parts []string
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%q", item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func resolve(t *testing.T, lines, roots, prefer []string) string {
	t.Helper()
	src := catalogProgram +
		"\nconst LINES: [String] = " + quoted(lines) + ";" +
		"\nconst ROOTS: [String] = " + quoted(roots) + ";" +
		"\nconst PREFER: [String] = " + quoted(prefer) + ";\n"
	return strings.TrimSuffix(execute(t, src), "\n")
}

// The catalog most cases resolve against: a small ecosystem with a
// history, where versions of one package moved their requirements on.
var ecosystem = []string{
	"acme/log 1.0.0",
	"acme/log 1.4.2",
	"acme/log 2.0.0",
	"acme/log 2.1.0-beta.1",
	"acme/log dev-main",
	"acme/json 2.0.0",
	"acme/json 2.1.0",
	"acme/json 2.1.7",
	"acme/json 3.0.0",
	"acme/http 1.0.0 : acme/log ^1.0",
	"acme/http 1.3.0 : acme/log ^1.0, acme/json ~2.1",
	"acme/http 2.0.0 : acme/log ^2.0, acme/json ^3.0",
	"acme/router 0.3.0 : acme/http ^1.0",
	"acme/router 0.4.0 : acme/http ^2.0",
	"acme/app-kit 1.0.0 : acme/router ^0.3 || ^0.4, acme/log *",
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name   string
		roots  []string
		prefer []string
		want   string
	}{
		{"the newest that fits", []string{"acme/log ^1.0"}, nil,
			"acme/log 1.4.2"},
		{"a pre-release is not taken without asking for one", []string{"acme/log >=2.0"}, nil,
			"acme/log 2.0.0"},
		{"a pre-release asked for", []string{"acme/log ^2.1.0-beta"}, nil,
			"acme/log 2.1.0-beta.1"},
		{"a branch, only by its name", []string{"acme/log dev-main"}, nil,
			"acme/log dev-main"},
		{"what the packages require, transitively", []string{"acme/router ^0.3"}, nil,
			"acme/http 1.3.0, acme/json 2.1.7, acme/log 1.4.2, acme/router 0.3.0"},
		{"the newest of everything when nothing holds it back", []string{"acme/app-kit ^1.0"}, nil,
			"acme/app-kit 1.0.0, acme/http 2.0.0, acme/json 3.0.0, acme/log 2.0.0, acme/router 0.4.0"},

		// The newest router needs http 2, which needs log 2: with log held
		// at 1 the search goes back to router 0.3 and http 1.3
		{"going back on a choice", []string{"acme/app-kit ^1.0", "acme/log ^1.0"}, nil,
			"acme/app-kit 1.0.0, acme/http 1.3.0, acme/json 2.1.7, acme/log 1.4.2, acme/router 0.3.0"},
		{"going back on a choice made two levels up", []string{"acme/app-kit ^1.0", "acme/json ~2.1.0"}, nil,
			"acme/app-kit 1.0.0, acme/http 1.3.0, acme/json 2.1.7, acme/log 1.4.2, acme/router 0.3.0"},
		{"a requirement of the project after one of a package", []string{"acme/http *", "acme/json <2.1.5"}, nil,
			"acme/http 1.3.0, acme/json 2.1.0, acme/log 1.4.2"},

		// What the lock had is kept while it fits
		{"the locked version first", []string{"acme/router *"}, []string{"acme/router=0.3.0", "acme/http=1.0.0", "acme/log=1.0.0"},
			"acme/http 1.0.0, acme/log 1.0.0, acme/router 0.3.0"},
		{"a locked version that no longer fits", []string{"acme/log ^2.0"}, []string{"acme/log=1.4.2"},
			"acme/log 2.0.0"},
		{"only what is not preferred moves", []string{"acme/http ^1.0"}, []string{"acme/http=1.0.0"},
			"acme/http 1.0.0, acme/log 1.4.2"},

		{"nothing required", nil, nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(t, ecosystem, tt.roots, tt.prefer); got != tt.want {
				t.Errorf("expected\n  %s\ngot\n  %s", tt.want, got)
			}
		})
	}
}

func TestResolveFailures(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		roots []string
		want  string
	}{
		{"two requirements that cannot both hold", ecosystem, []string{"acme/http ^2.0", "acme/log ^1.0"},
			`error: No version of acme/log meets every requirement on it:
  - qoqi.json requires acme/log ^1.0
  - acme/http 2.0.0 requires acme/log ^2.0
  the versions there are: 2.1.0-beta.1, 2.0.0, 1.4.2, 1.0.0, dev-main`},
		{"no version in the range", ecosystem, []string{"acme/json ^4.0"},
			`error: No version of acme/json meets every requirement on it:
  - qoqi.json requires acme/json ^4.0
  the versions there are: 3.0.0, 2.1.7, 2.1.0, 2.0.0`},
		{"a package without versions", []string{"acme/a 1.0.0 : acme/ghost ^1.0"}, []string{"acme/a *"},
			`error: No version of acme/ghost meets every requirement on it:
  - acme/a 1.0.0 requires acme/ghost ^1.0
  acme/ghost has no versions at all`},
		{"a constraint that cannot be read", ecosystem, []string{"acme/log ^^1"},
			`error: the requirement acme/log "^^1" is not a version constraint: write it as ^1.2, ~1.2.3, >=1.0 <2.0, 1.2.* or dev-main`},
		{"a constraint of a package that cannot be read", []string{"acme/a 1.0.0 : acme/b one"}, []string{"acme/a *"},
			`error: acme/a 1.0.0: the requirement acme/b "one" is not a version constraint: write it as ^1.2, ~1.2.3, >=1.0 <2.0, 1.2.* or dev-main`},
		{"a catalog that fails", []string{"acme/a 1.0.0 : broken/catalog ^1.0"}, []string{"acme/a *"},
			`error: the catalog cannot be reached`},
		{"two packages imported by one name", []string{"acme/strings 1.0.0", "other/strings 1.0.0"}, []string{"acme/strings *", "other/strings *"},
			`error: other/strings 1.0.0 and acme/strings 1.0.0 are both imported as strings: one of them has to say another "import" in its qoqi.json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(t, tt.lines, tt.roots, nil); got != tt.want {
				t.Errorf("expected\n%s\ngot\n%s", tt.want, got)
			}
		})
	}

	// The same name, said otherwise by one of the two, is fine
	got := resolve(t, []string{"acme/strings 1.0.0", "other/strings 1.0.0 import=other_strings"}, []string{"acme/strings *", "other/strings *"}, nil)
	if got != "acme/strings 1.0.0, other/strings 1.0.0" {
		t.Errorf("expected a package with an import of its own to be fine, got %s", got)
	}

	// A folder package that says no version is accepted by any constraint
	got = resolve(t, []string{"me/tools path", "acme/a 1.0.0 : me/tools ^3.0"}, []string{"acme/a *", "me/tools ^1.0"}, nil)
	if got != "acme/a 1.0.0, me/tools dev-path" {
		t.Errorf("expected a folder package to fit every constraint, got %s", got)
	}
}

// A search that has to go back a long way asks the catalog for each
// package once, and still ends: ten packages of ten versions each, where
// only the oldest of the first fits what the last requires.
func TestResolveGoesBackFar(t *testing.T) {
	var lines []string
	for p := 0; p < 10; p++ {
		for v := 0; v < 10; v++ {
			line := fmt.Sprintf("chain/p%d %d.0.0", p, v)
			if p < 9 {
				line += fmt.Sprintf(" : chain/p%d *", p+1)
			} else {
				line += fmt.Sprintf(" : chain/p0 %d.0.0", 0)
			}
			lines = append(lines, line)
		}
	}
	got := resolve(t, lines, []string{"chain/p0 *"}, nil)
	want := "chain/p0 0.0.0, chain/p1 9.0.0, chain/p2 9.0.0, chain/p3 9.0.0, chain/p4 9.0.0, chain/p5 9.0.0, chain/p6 9.0.0, chain/p7 9.0.0, chain/p8 9.0.0, chain/p9 9.0.0"
	if got != want {
		t.Errorf("expected\n  %s\ngot\n  %s", want, got)
	}
}

// A wide graph where nothing fits: the search says so without trying
// every combination of the packages in between.
func TestResolveGivesUpQuickly(t *testing.T) {
	var lines []string
	for p := 0; p < 12; p++ {
		for v := 0; v < 8; v++ {
			lines = append(lines, fmt.Sprintf("wide/p%d %d.0.0 : wide/leaf ^1.0", p, v))
		}
	}
	lines = append(lines, "wide/leaf 1.0.0 : wide/base ^1.0", "wide/base 2.0.0")
	var roots []string
	for p := 0; p < 12; p++ {
		roots = append(roots, fmt.Sprintf("wide/p%d *", p))
	}
	got := resolve(t, lines, roots, nil)
	want := `error: No version of wide/base meets every requirement on it:
  - wide/leaf 1.0.0 requires wide/base ^1.0
  the versions there are: 2.0.0`
	if got != want {
		t.Errorf("expected\n%s\ngot\n%s", want, got)
	}
}

// The parts of qoqi.json the package manager reads, and what it writes
// back when a requirement is added or removed.
func TestManifest(t *testing.T) {
	got := execute(t, `
import manifest;
import std.result.{Result};

fn main() -> Int {
    let text = """
        {
            "name": "acme/shop",
            "entry": "main.qi",
            "require": { "acme/log": "^1.0" },
            "repositories": [
                { "type": "git", "url": "https://example.com/log.git" },
                { "type": "path", "url": "../kit" }
            ],
            "heap_mb": 64
        }
        """;

    let shop = manifest.parse(text).expect("a manifest");
    println(shop.name, shop.importName, shop.require.entries(), shop.repositories.map(r -> "${r.kind()} ${r.url()}"));

    manifest.setRequirement(shop, "require", "acme/json", "^2.1");
    manifest.setRequirement(shop, "require-dev", "acme/test", "*");
    manifest.setRequirement(shop, "require", "acme/log", "");
    print(manifest.text(shop));
    println(shop.requirements(false), shop.requirements(true).len());

    println(manifest.defaultImport("acme/text-kit"), manifest.defaultImport("acme/a.b"));

    for name in ["acme/log", "Acme/log", "acme", "acme/log/extra", "a-/b", "acme/my--lib", "acme/my---lib", "1/2"] {
        println(name, manifest.isPackageName(name));
    }

    for bad in ["[]", "{\"require\": []}", "{\"require\": {\"Log\": \"1\"}}", "{\"require\": {\"a/b\": 1}}", "{\"repositories\": [{\"type\": \"svn\", \"url\": \"x\"}]}", "{\"repositories\": [{\"type\": \"git\"}]}", "{\"name\": 3}"] {
        match manifest.parse(bad) {
            case Result.Ok(_): { println("accepted", bad); }
            case Result.Err(reason): { println(reason); }
        }
    }

    return 0;
}
`)
	want := `acme/shop shop [("acme/log", "^1.0")] ["git https://example.com/log.git", "path ../kit"]
{
    "name": "acme/shop",
    "entry": "main.qi",
    "require": {
        "acme/json": "^2.1"
    },
    "repositories": [
        {
            "type": "git",
            "url": "https://example.com/log.git"
        },
        {
            "type": "path",
            "url": "../kit"
        }
    ],
    "heap_mb": 64,
    "require-dev": {
        "acme/test": "*"
    }
}
[("acme/json", "^2.1")] 2
text_kit a_b
acme/log true
Acme/log false
acme false
acme/log/extra false
a-/b false
acme/my--lib true
acme/my---lib false
1/2 true
the file must hold an object: {}
"require" must be an object of packages and versions, as in {"acme/textkit": "^1.2"}
"Log" in "require" is not a package name: write vendor/name, in lower case
the version of a/b in "require" must be a string, as in "^1.2"
repository 1 has a "type" that is not "git" or "path"
repository 1 has no "url"
"name" must be a string
`
	if got != want {
		t.Errorf("expected\n%s\ngot\n%s", want, got)
	}
}
