package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/memguard"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

// folderOf writes the files of a project in a folder of the test's own.
func folderOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const easyModule = `import std.flow.{Flow};

pub struct Easy {
    who: String;
}

impl Flow<String> for Easy {
    fn run() -> String {
        return greet(self.who);
    }
}

step fn greet(who: String) -> String {
    return "hello ${who}";
}
`

// A folder deploys its programs: the entry qoqi.json names, with the
// modules it imports, which have no main and are not programs; and the
// flows of those modules are the entry's.
func TestAFolderDeploysItsProgramsNotTheirModules(t *testing.T) {
	dir := folderOf(t, map[string]string{
		"qoqi.json": `{"entry": "main.qi"}`,
		"main.qi":   "import easy;\nimport llm;\n\nfn main() {\n    println(llm.ask(\"hi\"));\n}\n",
		"llm.qi":    "pub fn ask(q: String) -> String {\n    return q;\n}\n",
		"easy.qi":   easyModule,
	})
	programs, read, err := packagesOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != 1 || filepath.Base(programs[0].file) != "main.qi" || strings.Join(programs[0].pkg.Flows, ",") != "easy.Easy" {
		t.Fatalf("got %+v, want main.qi alone, with the flow easy.Easy", programs)
	}
	// --watch watches the modules too
	for _, want := range []string{"main.qi", "llm.qi", "easy.qi"} {
		found := false
		for _, f := range read {
			found = found || filepath.Base(f) == want
		}
		if !found {
			t.Errorf("%s is not among the files read: %v", want, read)
		}
	}

	// A module given alone is not a program
	if _, _, err := packagesOf(filepath.Join(dir, "llm.qi")); err == nil || !strings.Contains(err.Error(), "llm.qi has no main: it is a module") {
		t.Errorf("a module deployed alone: got %v", err)
	}
}

// A program that imports another to try its flows, as a tour or a test
// does, serves none of them: they are the other's, deployed with it.
func TestAProgramImportedIsNotServedByWhatImportsIt(t *testing.T) {
	dir := folderOf(t, map[string]string{
		"easy.qi": easyModule + "\nfn main() {}\n",
		"tour.qi": "import easy.{Easy};\n\nfn main() {\n    println(Easy { who: \"ada\" }.who);\n}\n",
	})
	programs, _, err := packagesOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != 1 || filepath.Base(programs[0].file) != "easy.qi" || strings.Join(programs[0].pkg.Flows, ",") != "Easy" {
		t.Fatalf("got %+v, want easy.qi alone, with the flow Easy", programs)
	}
}

// qoqi gen writes the shape of every step of a queue, as the daemon sends
// it, whether the program calls the step or not.
func TestGenWritesTheShapeOfAStepNothingCalls(t *testing.T) {
	src := func(body string) string {
		return `import std.flow.{Flow};

struct Lookup {
    id: String;
}

impl Flow<String> for Lookup {
    fn run() -> String {
        ` + body + `
    }
}

step fn lookup(id: String) -> Result<String, String> by people;

fn main() {}
`
	}
	shapeOf := func(body string) (map[string]string, map[string]flowrt.StepShape) {
		dir := folderOf(t, map[string]string{
			"qoqi.json": `{"entry": "main.qi", "queues": {"people": {"url": "http://127.0.0.1:9/people", "secret": "s"}}}`,
			"main.qi":   src(body),
		})
		file := filepath.Join(dir, "main.qi")
		result := compileSource(file)
		shapes, err := stepShapes(flowd.SchemaOf(file, result.AST, nil), result.Chunk)
		if err != nil {
			t.Fatal(err)
		}
		return shapes, flowrt.StepsOf(result.Chunk)
	}
	uncalled, _ := shapeOf(`return self.id;`)
	_, called := shapeOf("let found = lookup(self.id);\n        println(found);\n        return self.id;")
	if uncalled["lookup"] == "" || uncalled["lookup"] != called["lookup"].Shape {
		t.Errorf("got the shape %q, want %q, as the daemon sends it", uncalled["lookup"], called["lookup"].Shape)
	}
}
