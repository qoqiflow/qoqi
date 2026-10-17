package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// editor plays the part of an editor talking to `qoqi lsp`: it writes
// messages to the server's input and reads the ones that come back.
type editor struct {
	t      *testing.T
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
	// What the server said without being asked, by method, oldest first
	notes chan map[string]any
	// Answers, by the number of their question
	answers map[int]chan map[string]any
	stderr  strings.Builder
	// The last number the editor gave the text of each file it sent
	versions map[string]float64
}

func startEditor(t *testing.T, dir string) *editor {
	t.Helper()
	e := &editor{t: t, cmd: exec.Command(qoqi, "lsp"), nextID: 1, notes: make(chan map[string]any, 64), answers: map[int]chan map[string]any{}}
	e.cmd.Dir = dir
	// The server writes the library's files out for the editor to open:
	// under a home of its own, not the one of whoever runs the tests
	home := t.TempDir()
	e.cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"))
	e.cmd.Stderr = &e.stderr
	in, err := e.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	e.in, e.out = in, bufio.NewReader(out)
	if err := e.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.cmd.Process.Kill() })
	return e
}

func (e *editor) send(message map[string]any) {
	e.t.Helper()
	message["jsonrpc"] = "2.0"
	body, _ := json.Marshal(message)
	if _, err := fmt.Fprintf(e.in, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		e.t.Fatalf("cannot write to the server: %v\n%s", err, e.stderr.String())
	}
}

// read takes the next message off the server's output.
func (e *editor) read() map[string]any {
	e.t.Helper()
	type read struct {
		message map[string]any
		err     error
	}
	done := make(chan read, 1)
	go func() {
		length := 0
		for {
			line, err := e.out.ReadString('\n')
			if err != nil {
				done <- read{err: err}
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(name, "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(value))
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(e.out, body); err != nil {
			done <- read{err: err}
			return
		}
		var message map[string]any
		done <- read{message: message, err: json.Unmarshal(body, &message)}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			e.t.Fatalf("cannot read from the server: %v\n%s", got.err, e.stderr.String())
		}
		return got.message
	case <-time.After(10 * time.Second):
		e.t.Fatalf("the server said nothing for 10 seconds\n%s", e.stderr.String())
		return nil
	}
}

// ask sends a question and gives its answer; what the server says
// meanwhile without being asked is kept for note.
func (e *editor) ask(method string, params any) map[string]any {
	e.t.Helper()
	id := e.nextID
	e.nextID++
	e.send(map[string]any{"id": id, "method": method, "params": params})
	for {
		message := e.read()
		if got, ok := message["id"].(float64); ok && int(got) == id {
			return message
		}
		e.notes <- message
	}
}

func (e *editor) tell(method string, params any) {
	e.t.Helper()
	if fields, ok := params.(map[string]any); ok {
		if document, ok := fields["textDocument"].(map[string]any); ok {
			uri, _ := document["uri"].(string)
			if version, ok := document["version"].(int); ok {
				if e.versions == nil {
					e.versions = make(map[string]float64)
				}
				e.versions[uri] = float64(version)
			}
		}
	}
	e.send(map[string]any{"method": method, "params": params})
}

// note gives the next thing the server said without being asked.
func (e *editor) note() map[string]any {
	e.t.Helper()
	select {
	case message := <-e.notes:
		return message
	default:
		return e.read()
	}
}

// diagnostics waits for what the server has to say about a file, and
// writes it as lines: "line:column-column code message".
func (e *editor) diagnostics(uri string) []string {
	e.t.Helper()
	for {
		message := e.note()
		if message["method"] != "textDocument/publishDiagnostics" {
			continue
		}
		params := message["params"].(map[string]any)
		if params["uri"] != uri {
			continue
		}
		// What was found in a text the editor has changed since: a
		// check that started before the last change came in
		if version, ok := params["version"].(float64); ok && version < e.versions[uri] {
			continue
		}
		out := []string{}
		for _, item := range params["diagnostics"].([]any) {
			d := item.(map[string]any)
			if d["code"] == "E8010" || d["code"] == "E1004" {
				// The program imports a module only to ask about it, and
				// edits leave names unused: what is asked here is the rest
				continue
			}
			start, end := at(d["range"], "start"), at(d["range"], "end")
			out = append(out, fmt.Sprintf("%d:%d-%d %v %v [%v]", start[0], start[1], end[1], d["code"], d["message"], d["severity"]))
		}
		return out
	}
}

// at reads one end of a range: its line and character.
func at(rng any, end string) [2]int {
	position := rng.(map[string]any)[end].(map[string]any)
	return [2]int{int(position["line"].(float64)), int(position["character"].(float64))}
}

func position(uri string, line, character int) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	}
}

const lspProgram = `import std.math;
import shapes;
import shapes.{Shape};

// How many there are.
const count = 3;

struct Tally { total: Float; seen: Int; }

impl Tally {
    // One more shape counted.
    fn add(shape: Shape) {
        self.total += shape.area();
        self.seen++;
    }
}

fn main() -> Int {
    let tally = Tally { total: 0.0, seen: 0 };
    tally.add(Shape.Circle(1.0));
    tally.add(shapes.square(2.0));
    println(tally.seen, tally.total.round(), count);
    return 0;
}
`

const lspModule = `// Shapes and what they cover.

pub enum Shape { Circle(Float), Rect(Float, Float) }

impl Shape {
    // The surface it covers.
    pub fn area() -> Float {
        match self {
            case Shape.Circle(r): { return 3.0 * r * r; }
            case Shape.Rect(w, h): { return w * h; }
        }
    }
}

// A rectangle with equal sides.
pub fn square(side: Float) -> Shape {
    return Shape.Rect(side, side);
}
`

// TestLanguageServer drives `qoqi lsp` through what an editor does with
// it: open a program of two files, ask about names, type a mistake, fix
// it, and leave.
func TestLanguageServer(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mainPath, modulePath := filepath.Join(dir, "main.qi"), filepath.Join(dir, "shapes.qi")
	os.WriteFile(mainPath, []byte(lspProgram), 0o644)
	os.WriteFile(modulePath, []byte(lspModule), 0o644)
	mainURI, moduleURI := "file://"+mainPath, "file://"+modulePath

	e := startEditor(t, dir)

	// --- Meeting ---
	hello := e.ask("initialize", map[string]any{"capabilities": map[string]any{}})
	can := hello["result"].(map[string]any)["capabilities"].(map[string]any)
	for _, name := range []string{"hoverProvider", "definitionProvider", "documentSymbolProvider"} {
		if can[name] != true {
			t.Errorf("expected the server to offer %s, got %v", name, can)
		}
	}
	if can["textDocumentSync"] != float64(1) || can["completionProvider"] == nil {
		t.Errorf("unexpected capabilities: %v", can)
	}
	e.tell("initialized", map[string]any{})

	// --- Opening a program that is fine ---
	e.tell("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": mainURI, "languageId": "qoqi", "version": 1, "text": lspProgram}})
	if got := e.diagnostics(mainURI); len(got) != 0 {
		t.Errorf("expected nothing to say about the program, got %v", got)
	}

	// --- What a name is ---
	hover := func(line, character int) (string, [2]int) {
		t.Helper()
		answer := e.ask("textDocument/hover", position(mainURI, line, character))
		result, ok := answer["result"].(map[string]any)
		if !ok {
			return "", [2]int{}
		}
		start, end := at(result["range"], "start"), at(result["range"], "end")
		return result["contents"].(map[string]any)["value"].(string), [2]int{start[1], end[1]}
	}
	for _, c := range []struct {
		line, character int
		want            string
		extent          [2]int
	}{
		// A method of the file, a variable, a field
		{19, 11, "```qoqi\nfn Tally.add(shape: Shape)\n```\n\nOne more shape counted.", [2]int{10, 13}},
		{19, 5, "```qoqi\nlet tally: Tally\n```", [2]int{4, 9}},
		{21, 19, "```qoqi\n(field) Tally.seen: Int\n```", [2]int{18, 22}},
		// What comes from the module next to it, and from the library
		{20, 22, "```qoqi\nfn shapes.square(side: Float) -> Shape\n```\n\nA rectangle with equal sides.", [2]int{14, 27}},
		{12, 30, "```qoqi\nfn shapes.Shape.area() -> Float\n```\n\nThe surface it covers.", [2]int{28, 32}},
		{21, 37, "```qoqi\nfn Float.round() -> Int\n```\n\nThe nearest whole number; halves go away from zero: round(2.5) is 3.", [2]int{36, 41}},
		{21, 46, "```qoqi\nconst count: Int\n```\n\nHow many there are.", [2]int{45, 50}},
		// The names of types where they are written, and names where they are declared
		{11, 20, "```qoqi\nenum shapes.Shape {\n    Circle(Float),\n    Rect(Float, Float),\n}\n```", [2]int{18, 23}},
		{7, 24, "```qoqi\nFloat\n```\n\nA number with decimals.", [2]int{22, 27}},
		{17, 14, "```qoqi\nInt\n```\n\nA whole number, from -140737488355328 to 140737488355327.", [2]int{13, 16}},
		{11, 8, "```qoqi\nfn Tally.add(shape: Shape)\n```\n\nOne more shape counted.", [2]int{7, 10}},
		{18, 9, "```qoqi\nlet tally: Tally\n```", [2]int{8, 13}},
	} {
		if got, extent := hover(c.line, c.character); got != c.want || extent != c.extent {
			t.Errorf("hover at %d:%d:\n got %q %v\nwant %q %v", c.line, c.character, got, extent, c.want, c.extent)
		}
	}
	if got, _ := hover(17, 0); got != "" {
		t.Errorf("expected nothing where there is no name, got %q", got)
	}

	// --- Where a name is declared ---
	definition := func(line, character int) string {
		t.Helper()
		result, ok := e.ask("textDocument/definition", position(mainURI, line, character))["result"].(map[string]any)
		if !ok {
			return "nowhere"
		}
		start := at(result["range"], "start")
		// What is jumped to has to be there to open
		file := strings.TrimPrefix(result["uri"].(string), "file://")
		if _, err := os.Stat(file); err != nil {
			t.Errorf("definition at %d:%d names a file that is not there: %v", line, character, err)
		}
		return fmt.Sprintf("%s:%d:%d", filepath.Base(file), start[0], start[1])
	}
	for _, c := range []struct {
		line, character int
		want            string
	}{
		{19, 11, "main.qi:11:7"},   // a method of this file
		{19, 5, "main.qi:18:8"},    // a variable
		{20, 22, "shapes.qi:15:7"}, // a function of the module next to it
		{12, 30, "shapes.qi:6:11"}, // a method of its enum
		{11, 20, "shapes.qi:2:9"},  // a type, where it is written
		{18, 20, "main.qi:7:7"},    // a struct, where a value of it is made
		{21, 37, "math.qi:276:11"}, // the library: its file is written out to be opened
		{17, 14, "nowhere"},        // a type of the language
		{21, 4, "nowhere"},         // a builtin
	} {
		if got := definition(c.line, c.character); got != c.want {
			t.Errorf("definition at %d:%d: got %s, want %s", c.line, c.character, got, c.want)
		}
	}

	// --- What the file declares ---
	var outline []string
	for _, item := range e.ask("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": mainURI}})["result"].([]any) {
		symbol := item.(map[string]any)
		line := fmt.Sprintf("%v/%v@%d", symbol["name"], symbol["kind"], at(symbol["selectionRange"], "start")[0])
		for _, child := range symbol["children"].([]any) {
			part := child.(map[string]any)
			line += fmt.Sprintf(" %v/%v", part["name"], part["kind"])
		}
		outline = append(outline, line)
	}
	if got, want := strings.Join(outline, "; "), "count/14@5; Tally/23@7 total/8 seen/8 add/6; main/12@17"; got != want {
		t.Errorf("unexpected outline:\n got %s\nwant %s", got, want)
	}

	// --- Typing: a line that is not finished yet ---
	change := func(version int, text string) {
		e.tell("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": mainURI, "version": version},
			"contentChanges": []any{map[string]any{"text": text}},
		})
	}
	complete := func(line, character int) string {
		t.Helper()
		var labels []string
		for _, item := range e.ask("textDocument/completion", position(mainURI, line, character))["result"].([]any) {
			suggestion := item.(map[string]any)
			labels = append(labels, fmt.Sprintf("%v/%v", suggestion["label"], suggestion["kind"]))
		}
		return strings.Join(labels, " ")
	}
	typing := func(text string) string {
		lines := strings.Split(lspProgram, "\n")
		lines[21] = text
		return strings.Join(lines, "\n")
	}
	change(2, typing("    tally."))
	if got, want := complete(21, 10), "total/5 seen/5 add/2"; got != want {
		t.Errorf("after a value: got %s, want %s", got, want)
	}
	change(3, typing("    shapes."))
	if got, want := complete(21, 11), "Shape/13 square/3"; got != want {
		t.Errorf("after a module: got %s, want %s", got, want)
	}
	change(4, typing("    let s = Shape.Re"))
	if got, want := complete(21, 20), "Circle/20 Rect/20 area/2"; got != want {
		t.Errorf("after an enum: got %s, want %s", got, want)
	}
	// On an import line: the modules there are, of the library and next to the file
	change(5, strings.Replace(lspProgram, "import std.math;", "import std.", 1))
	if got := complete(0, 11); !strings.Contains(got, "math/9") || !strings.Contains(got, "task/9") || strings.Contains(got, "/14") {
		t.Errorf("after 'import std.': got %s", got)
	}
	change(5, strings.Replace(lspProgram, "import shapes;", "import ", 1))
	if got, want := complete(1, 7), "shapes/9 std/9"; got != want {
		t.Errorf("after 'import ': got %s, want %s", got, want)
	}
	change(5, typing("    let s = Shape.Re"))
	// The changes came faster than the server checks: it says something
	// once, about the last of them
	if got := e.diagnostics(mainURI); len(got) != 1 || !strings.HasPrefix(got[0], "21:") {
		t.Errorf("expected one diagnostic on the unfinished line, got %v", got)
	}

	// --- A mistake, with what would fix it ---
	change(6, typing("    println(tally.seen, tally.totl.round(), count);"))
	if got, want := e.diagnostics(mainURI), "21:29-30 E2014 Tally has no field 'totl'"; len(got) != 1 || !strings.HasPrefix(got[0], want) || !strings.HasSuffix(got[0], "[1]") {
		t.Errorf("unexpected diagnostics:\n got %v\nwant %s...", got, want)
	}
	change(7, typing("    println(tally.seen, tally.total.round(), count);\n    return 1;"))
	if got := e.diagnostics(mainURI); len(got) != 1 || !strings.Contains(got[0], "E2023") || !strings.HasSuffix(got[0], "[2]") {
		t.Errorf("expected a warning for the unreachable return, got %v", got)
	}

	// --- A mistake in the other file, not saved: said to be there, and
	// cleared once it is fixed ---
	e.tell("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": moduleURI, "languageId": "qoqi", "version": 1, "text": lspModule}})
	e.diagnostics(moduleURI)
	change(8, lspProgram)
	if got := e.diagnostics(mainURI); len(got) != 0 {
		t.Errorf("expected the program to be fine again, got %v", got)
	}
	e.tell("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": moduleURI, "version": 2},
		"contentChanges": []any{map[string]any{"text": strings.Replace(lspModule, "return w * h;", "return w * depth;", 1)}},
	})
	if got := e.diagnostics(moduleURI); len(got) != 1 || !strings.HasPrefix(got[0], "9:48-53 E1001 undefined identifier 'depth'") {
		t.Errorf("unexpected diagnostics for the module: %v", got)
	}
	e.tell("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": mainURI}})
	if got := e.diagnostics(moduleURI); len(got) != 1 {
		t.Errorf("expected the program that imports the module to report its mistake too, got %v", got)
	}
	e.tell("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": moduleURI, "version": 3},
		"contentChanges": []any{map[string]any{"text": lspModule}},
	})
	if got := e.diagnostics(moduleURI); len(got) != 0 {
		t.Errorf("expected the module to be fine again, got %v", got)
	}

	// --- What the server does not know, and a question about a file it was not shown ---
	if refused, ok := e.ask("textDocument/rename", map[string]any{})["error"].(map[string]any); !ok || refused["code"] != float64(-32601) {
		t.Errorf("expected an unknown question to be refused, got %v", refused)
	}
	if answer := e.ask("textDocument/hover", position("file:///nowhere/else.qi", 0, 0)); answer["result"] != nil || answer["error"] != nil {
		t.Errorf("expected nothing about a file that is not open, got %v", answer)
	}

	// --- Leaving ---
	e.tell("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": mainURI}})
	if got := e.diagnostics(mainURI); len(got) != 0 {
		t.Errorf("expected a closed file to be cleared, got %v", got)
	}
	if answer := e.ask("shutdown", nil); answer["error"] != nil {
		t.Errorf("unexpected answer to shutdown: %v", answer)
	}
	e.tell("exit", nil)
	if err := e.cmd.Wait(); err != nil {
		t.Errorf("expected the server to leave with status 0, got %v\n%s", err, e.stderr.String())
	}
	if e.stderr.Len() > 0 {
		t.Errorf("expected nothing on the error output, got:\n%s", e.stderr.String())
	}
}

// An editor that goes away without a word: the server ends, with a status
// that says it was not asked to.
func TestLanguageServerLosesItsEditor(t *testing.T) {
	e := startEditor(t, t.TempDir())
	e.ask("initialize", map[string]any{})
	e.in.Close()
	err := e.cmd.Wait()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Errorf("expected status 1, got %v\n%s", err, e.stderr.String())
	}
}

// TestLanguageServerFormats asks the server to format a file, as an
// editor does for Format Document and on save: the answer is one change
// that replaces the file with what `qoqi fmt` would write, made from the
// text in the editor and not the one on disk. A file that is formatted
// already, or that does not read as a program, is left alone.
func TestLanguageServerFormats(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.qi")
	// On disk: something else than what the editor holds
	os.WriteFile(path, []byte("fn main() {}\n"), 0o644)
	uri := "file://" + path

	e := startEditor(t, dir)
	hello := e.ask("initialize", map[string]any{"capabilities": map[string]any{}})
	if can := hello["result"].(map[string]any)["capabilities"].(map[string]any); can["documentFormattingProvider"] != true {
		t.Fatalf("expected the server to offer formatting, got %v", can)
	}
	e.tell("initialized", map[string]any{})

	format := func() ([]any, bool) {
		t.Helper()
		answer := e.ask("textDocument/formatting", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"options":      map[string]any{"tabSize": 2, "insertSpaces": false},
		})
		if answer["error"] != nil {
			t.Fatalf("the server refused to format: %v", answer["error"])
		}
		edits, ok := answer["result"].([]any)
		return edits, ok
	}

	dense := "import std.arrays;\n// Adds them up.\nfn total(xs:[Int])->Int{let sum=0;for x in xs{sum+=x;}return sum;}\nfn main(){println(total([1,2,3]));}"
	tidy := "import std.arrays;\n\n// Adds them up.\nfn total(xs: [Int]) -> Int {\n    let sum = 0;\n\n    for x in xs {\n        sum += x;\n    }\n\n    return sum;\n}\n\nfn main() {\n    println(total([1, 2, 3]));\n}\n"
	e.tell("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "qoqi", "version": 1, "text": dense}})
	e.diagnostics(uri)

	edits, ok := format()
	if !ok || len(edits) != 1 {
		t.Fatalf("expected one change, got %v", edits)
	}
	edit := edits[0].(map[string]any)
	if got := edit["newText"]; got != tidy {
		t.Errorf("expected the file formatted, got:\n%v", got)
	}
	// From the start to past the last line: all of it, however it ends
	if start, end := at(edit["range"], "start"), at(edit["range"], "end"); start != [2]int{0, 0} || end[0] < 4 || end[1] != 0 {
		t.Errorf("expected a change of the whole file, got %v to %v", start, end)
	}

	change := func(version int, text string) {
		e.tell("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []any{map[string]any{"text": text}},
		})
	}

	// The editor applies the change: there is nothing left to do
	change(2, tidy)
	if edits, ok := format(); !ok || len(edits) != 0 {
		t.Errorf("expected nothing to change in a formatted file, got %v", edits)
	}

	// A program that does not compile is formatted all the same: names and
	// types are not the formatter's business
	change(3, "fn main(){println(nobody(1)+\"two\");}")
	if edits, ok := format(); !ok || len(edits) != 1 || edits[0].(map[string]any)["newText"] != "fn main() {\n    println(nobody(1) + \"two\");\n}\n" {
		t.Errorf("expected a program with a wrong name in it to be formatted, got %v", edits)
	}

	// One that does not read as a program is not touched
	change(4, "fn main() {\n    let a = 1\n}")
	if edits, ok := format(); !ok || len(edits) != 0 {
		t.Errorf("expected a file with a syntax error to be left alone, got %v", edits)
	}

	// A file the editor never opened is nothing to format
	answer := e.ask("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": "file://" + filepath.Join(dir, "other.qi")}})
	if answer["result"] != nil || answer["error"] != nil {
		t.Errorf("expected no answer for a file that is not open, got %v", answer)
	}

	// The server is still there
	if bye := e.ask("shutdown", nil); bye["error"] != nil {
		t.Errorf("expected the server to shut down, got %v", bye)
	}
}
