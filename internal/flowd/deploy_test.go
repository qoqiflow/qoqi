package flowd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// A daemon that runs without stopping, given its programs by deploys: a
// version that keeps the shape of its stages is served at once, one that
// changes a stage needs a migration while an instance is there, and what
// was deployed is served again by a daemon started on the store.

// tally counts the notes it is sent until it is told to stop. Each
// version records a note its own way, so that what a note printed says
// which version took it.
func tally(version int) string {
	record := fmt.Sprintf(`step fn record(note: String) -> Int {
    println("v%d ${note}");
    return %d;
}`, version, version)
	if version < 3 {
		return `import std.flow;
import std.flow.{Flow, Inbox};

struct Tally {
    owner: String;
    notes: Inbox<String> = flow.inbox<String>(8);
}

impl Flow<String> for Tally {
    fn run() -> String {
        let seen = 0;

        while true {
            stage listening;

            let note = wait self.notes.receive();

            if note == "stop" {
                return "${self.owner} ${seen}";
            }

            seen = seen + record(note);
        }

        return "";
    }
}

` + record + `

fn main() {}
`
	}
	// The third keeps the last note too: a variable more at the stage
	return `import std.flow;
import std.flow.{Flow, Inbox};

struct Tally {
    owner: String;
    notes: Inbox<String> = flow.inbox<String>(8);
}

struct Listening {
    seen: Int;
    last: String;
}

impl Flow<String> for Tally {
    fn run() -> String {
        let seen = 0;
        let last = "";

        while true {
            stage listening;

            let note = wait self.notes.receive();

            if note == "stop" {
                return "${self.owner} ${seen} after ${last}";
            }

            seen = seen + record(note);
            last = note;
        }

        return "";
    }

    // The instances of the version before had no last note
    migrate fn listening(seen: Int) -> Listening {
        return Listening { seen: seen, last: "none yet" };
    }
}

` + record + `

fn main() {}
`
}

// withoutMigration is the third version, its migration taken out.
func withoutMigration(src string) string {
	start := strings.Index(src, "    // The instances of the version before")
	end := strings.Index(src, "\n}\n\n"+"step fn")
	return src[:start] + src[end:]
}

func packageOf(t *testing.T, name, src string) *flowd.Package {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.Compile(src, name+".qi", errs)
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	pkg, err := flowd.PackageOf(name, name+".qi", result.AST, result.Chunk, map[string]string{name + ".qi": src})
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// lines prints what instances print, line by line.
type lines struct {
	mu   sync.Mutex
	seen []string
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		l.seen = append(l.seen, line)
	}
	return len(p), nil
}

func (l *lines) has(line string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.seen {
		if s == line {
			return true
		}
	}
	return false
}

// emptyDaemon is a daemon that serves nothing yet, on stores in memory:
// one store a program, as a daemon on SQLite has its tables.
type emptyDaemon struct {
	d      *flowd.Daemon
	server *httptest.Server
	out    *lines
	stores map[string]*flowrt.Memory
	kept   *flowrt.Memory // where the deployments are kept
	stop   func()
}

func newEmptyDaemon(t *testing.T, stores map[string]*flowrt.Memory, kept *flowrt.Memory) *emptyDaemon {
	t.Helper()
	d, err := flowd.NewOf(nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &emptyDaemon{d: d, out: &lines{}, stores: stores, kept: kept}
	var mu sync.Mutex
	d.Open = func(name string) (flowrt.Store, error) {
		mu.Lock()
		defer mu.Unlock()
		if e.stores[name] == nil {
			e.stores[name] = flowrt.NewMemory()
		}
		return e.stores[name], nil
	}
	d.Deployments, d.Workers, d.Out = kept, 4, e.out
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx, 5*time.Millisecond)
		close(done)
	}()
	e.server = httptest.NewServer(d.Handler())
	e.stop = func() {
		e.server.Close()
		cancel()
		<-done
	}
	t.Cleanup(e.stop)
	return e
}

func (e *emptyDaemon) deploy(t *testing.T, dry bool, pkgs ...*flowd.Package) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"programs": pkgs, "dry": dry})
	return call(t, e.d, e.server, "POST", "/api/deploy", string(body))
}

func (e *emptyDaemon) waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s; printed %v", what, e.out.seen)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *emptyDaemon) status(t *testing.T, id string) string {
	_, body := call(t, e.d, e.server, "GET", "/api/instances/"+id, "")
	var v flowd.Detail
	json.Unmarshal([]byte(body), &v)
	return v.Status
}

func (e *emptyDaemon) send(t *testing.T, id, note, key string) {
	t.Helper()
	if status, body := call(t, e.d, e.server, "POST", "/api/instances/"+id+"/inboxes/notes", `{"message": "`+note+`", "key": "`+key+`"}`); status != http.StatusOK {
		t.Fatalf("send %s: %d %s", note, status, body)
	}
}

func TestADaemonStartedEmptyIsGivenItsPrograms(t *testing.T) {
	e := newEmptyDaemon(t, map[string]*flowrt.Memory{}, flowrt.NewMemory())

	// Nothing served yet: a start says how to give it something
	if status, body := call(t, e.d, e.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t"}`); status != http.StatusNotFound || !strings.Contains(body, "qoqi deploy") {
		t.Fatalf("a start before any deploy: %d %s", status, body)
	}

	// The first version
	status, body := e.deploy(t, false, packageOf(t, "tally", tally(1)))
	if status != http.StatusOK || !strings.Contains(body, `"new": true`) {
		t.Fatalf("first deploy: %d %s", status, body)
	}
	if status, body := call(t, e.d, e.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t", "fields": {"owner": "ada"}}`); status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, body)
	}
	e.send(t, "t", "a", "k1")
	e.waitFor(t, "v1 to take the first note", func() bool { return e.out.has("v1 a") })

	// Its source is served from what was deployed: no file on disk
	if status, body := call(t, e.d, e.server, "GET", "/api/source?file=tally.qi", ""); status != http.StatusOK || !strings.Contains(body, "stage listening") {
		t.Errorf("the source deployed: %d %s", status, body)
	}

	// A second version, the stage as it was: the instance asleep takes its
	// next note with the new step, with no restart
	status, body = e.deploy(t, false, packageOf(t, "tally", tally(2)))
	if status != http.StatusOK || !strings.Contains(body, `"moving": 1`) {
		t.Fatalf("second deploy: %d %s", status, body)
	}
	e.send(t, "t", "b", "k2")
	e.waitFor(t, "v2 to take the second note", func() bool { return e.out.has("v2 b") })

	// A third that changes the stage, without a migration: refused while
	// the instance is there, dry or not, and the second goes on
	for _, dry := range []bool{true, false} {
		status, body = e.deploy(t, dry, packageOf(t, "tally", withoutMigration(tally(3))))
		if status != http.StatusConflict || !strings.Contains(body, "listening") || !strings.Contains(body, "1 instances of Tally alive") {
			t.Fatalf("a change no migration answers, dry %v: %d %s", dry, status, body)
		}
	}
	e.send(t, "t", "c", "k3")
	e.waitFor(t, "v2 to go on after a refusal", func() bool { return e.out.has("v2 c") })

	// Dry with its migration: said, not done
	status, body = e.deploy(t, true, packageOf(t, "tally", tally(3)))
	if status != http.StatusOK || !strings.Contains(body, `"dry": true`) || !strings.Contains(body, "last: String is new") {
		t.Fatalf("a dry deploy: %d %s", status, body)
	}
	e.send(t, "t", "d", "k4")
	e.waitFor(t, "v2 still, after a dry deploy", func() bool { return e.out.has("v2 d") })

	// For real: the instance moves through the migration
	if status, body = e.deploy(t, false, packageOf(t, "tally", tally(3))); status != http.StatusOK {
		t.Fatalf("third deploy: %d %s", status, body)
	}
	e.send(t, "t", "e", "k5")
	e.send(t, "t", "stop", "k6")
	_, result := call(t, e.d, e.server, "GET", "/api/instances/t/result?wait=10s", "")
	// 1 from v1, 2 for each of b, c, d from v2, 3 from v3. The instance
	// waited for e in v2's run, which took it: its step ran in v3, the
	// newest that has it, and the instance moved at the stage after,
	// through the migration, which knew no last note
	if strings.TrimSpace(result) != `"ada 10 after none yet"` {
		t.Errorf("expected the notes counted by the version that took each, got %s; printed %v", result, e.out.seen)
	}

	// Three versions kept, the last first: what was refused or dry is not
	_, body = call(t, e.d, e.server, "GET", "/api/deployments", "")
	var history []flowd.DeployView
	if err := json.Unmarshal([]byte(body), &history); err != nil || len(history) != 3 {
		t.Fatalf("expected three deploys, got %s", body)
	}
	if !history[0].Serving || history[1].Serving || history[0].Seq <= history[1].Seq || history[2].Changes != nil {
		t.Errorf("the last serves, the first changed nothing before it: %s", body)
	}
	if len(history[0].Changes) == 0 || history[0].Moving != 1 || history[1].Moving != 1 {
		t.Errorf("what each changed, and how many were to move: %s", body)
	}

	// What the third changed of the second: the source, and the stage
	_, body = call(t, e.d, e.server, "GET", fmt.Sprintf("/api/deployments/%d/diff", history[0].Seq), "")
	var diff flowd.Diff
	if err := json.Unmarshal([]byte(body), &diff); err != nil || diff.From == nil || diff.From.Seq != history[1].Seq {
		t.Fatalf("a diff from the deploy before: %s", body)
	}
	if len(diff.Files) != 1 || diff.Files[0].Status != "changed" || len(diff.Stages) != 1 || !strings.Contains(diff.Stages[0], "last: String is new") {
		t.Fatalf("one file changed, one stage: %s", body)
	}
	var added, removed []string
	for _, h := range diff.Files[0].Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case "+":
				added = append(added, strings.TrimSpace(l.Text))
			case "-":
				removed = append(removed, strings.TrimSpace(l.Text))
			}
		}
	}
	for _, want := range []string{`let last = "";`, `migrate fn listening(seen: Int) -> Listening {`, `println("v3 ${note}");`} {
		if !containsLine(added, want) {
			t.Errorf("expected %q added: %v", want, added)
		}
	}
	if !containsLine(removed, `println("v2 ${note}");`) || containsLine(removed, `stage listening;`) {
		t.Errorf("expected v2's line removed, and the stage kept: %v", removed)
	}

	// From the first, and the first alone: all of it added
	_, body = call(t, e.d, e.server, "GET", fmt.Sprintf("/api/deployments/%d/diff?from=%d", history[0].Seq, history[2].Seq), "")
	if !strings.Contains(body, `v1 ${note}`) {
		t.Errorf("a diff from the first: %s", body)
	}
	_, body = call(t, e.d, e.server, "GET", fmt.Sprintf("/api/deployments/%d/diff", history[2].Seq), "")
	var first flowd.Diff
	if err := json.Unmarshal([]byte(body), &first); err != nil || first.From != nil || first.Files[0].Status != "added" {
		t.Errorf("the first, from nothing: %s", body)
	}
	if status, _ := call(t, e.d, e.server, "GET", "/api/deployments/99/diff", ""); status != http.StatusNotFound {
		t.Errorf("a deploy that is not: %d", status)
	}
	if status, body := call(t, e.d, e.server, "GET", fmt.Sprintf("/api/deployments/%d", history[2].Seq), ""); status != http.StatusOK || !strings.Contains(body, "v1 ${note}") {
		t.Errorf("a deploy, with its sources: %d %s", status, body)
	}
}

func containsLine(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}

// What was deployed is served by a daemon started again on the store,
// and the instances go on where they were.
func TestWhatWasDeployedIsServedAgain(t *testing.T) {
	stores, kept := map[string]*flowrt.Memory{}, flowrt.NewMemory()
	first := newEmptyDaemon(t, stores, kept)
	if status, body := first.deploy(t, false, packageOf(t, "tally", tally(2))); status != http.StatusOK {
		t.Fatalf("deploy: %d %s", status, body)
	}
	call(t, first.d, first.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t", "fields": {"owner": "lin"}}`)
	first.send(t, "t", "a", "k1")
	first.waitFor(t, "the first note", func() bool { return first.out.has("v2 a") })
	first.stop()

	again := newEmptyDaemon(t, stores, kept)
	if flows := again.d.Flows; len(flows) != 1 || flows[0] != "Tally" {
		t.Fatalf("expected Tally served again, got %v", flows)
	}
	again.send(t, "t", "b", "k2")
	again.send(t, "t", "stop", "k3")
	if _, result := call(t, again.d, again.server, "GET", "/api/instances/t/result?wait=10s", ""); strings.TrimSpace(result) != `"lin 4"` {
		t.Errorf("expected the instance to go on, got %s", result)
	}

	// The version served again is the one deployed: the same again is
	// said to be it
	if status, body := again.deploy(t, false, packageOf(t, "tally", tally(2))); status != http.StatusOK || !strings.Contains(body, `"same": true`) {
		t.Errorf("the same version: %d %s", status, body)
	}

	// Served again and deployed again as it was, it is one deploy
	if history, _ := kept.History("", false); len(history) != 1 {
		t.Errorf("expected one deploy kept, got %d", len(history))
	}
}

// Programs deployed together are checked together: a flow belongs to one,
// and none is served when one is refused.
func TestProgramsDeployedTogether(t *testing.T) {
	e := newEmptyDaemon(t, map[string]*flowrt.Memory{}, flowrt.NewMemory())
	status, body := e.deploy(t, false, packageOf(t, "a", tally(1)), packageOf(t, "b", tally(1)))
	if status != http.StatusConflict || !strings.Contains(body, "a and b both have a flow Tally") {
		t.Fatalf("two programs with one flow: %d %s", status, body)
	}
	if len(e.d.Flows) != 0 {
		t.Fatalf("expected nothing served, got %v", e.d.Flows)
	}
	if status, body := e.deploy(t, false, packageOf(t, "a", tally(1))); status != http.StatusOK {
		t.Fatalf("a alone: %d %s", status, body)
	}
	if status, body := e.deploy(t, false, packageOf(t, "b", tally(1))); status != http.StatusConflict || !strings.Contains(body, "a has a flow Tally already") {
		t.Errorf("b after a: %d %s", status, body)
	}
	if status, body := e.deploy(t, false, &flowd.Package{Name: "broken", Bytecode: []byte("not bytecode"), Flows: []string{"X"}}); status != http.StatusConflict || !strings.Contains(body, "cannot be read") {
		t.Errorf("bytecode that is not: %d %s", status, body)
	}
}

// Versions deployed while instances run: each run ends on the version it
// began, none is lost, and each note is counted once.
func TestDeployingWhileInstancesRun(t *testing.T) {
	e := newEmptyDaemon(t, map[string]*flowrt.Memory{}, flowrt.NewMemory())
	e.deploy(t, false, packageOf(t, "tally", tally(1)))
	v1, v2 := packageOf(t, "tally", tally(1)), packageOf(t, "tally", tally(2))

	const n = 12
	for i := 0; i < n; i++ {
		call(t, e.d, e.server, "POST", "/api/instances", fmt.Sprintf(`{"flow": "Tally", "id": "t%d", "fields": {"owner": "o%d"}}`, i, i))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 10; round++ {
			pkg := v1
			if round%2 == 0 {
				pkg = v2
			}
			if status, body := e.deploy(t, false, pkg); status != http.StatusOK {
				t.Errorf("deploy %d: %d %s", round, status, body)
			}
		}
	}()
	for note := 0; note < 5; note++ {
		for i := 0; i < n; i++ {
			e.send(t, fmt.Sprintf("t%d", i), fmt.Sprint("n", note), fmt.Sprint("k", note))
		}
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		e.send(t, fmt.Sprintf("t%d", i), "stop", "stop")
	}
	for i := 0; i < n; i++ {
		_, result := call(t, e.d, e.server, "GET", fmt.Sprintf("/api/instances/t%d/result?wait=20s", i), "")
		var text string
		json.Unmarshal([]byte(result), &text)
		var owner string
		var seen int
		if _, err := fmt.Sscanf(text, "%s %d", &owner, &seen); err != nil || owner != fmt.Sprint("o", i) || seen < 5 || seen > 10 {
			t.Errorf("t%d: expected five notes counted once each, by v1 or v2, got %s", i, result)
		}
	}
}

// A daemon that was not given a way to open stores serves the programs it
// was made with, and no other.
func TestADaemonMadeWithItsPrograms(t *testing.T) {
	errs := lerrors.NewHandler()
	result := compiler.Compile(tally(1), "tally.qi", errs)
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	d := flowd.New(rt, []string{"Tally"})
	if _, err := d.Deploy(packageOf(t, "other", strings.ReplaceAll(tally(1), "Tally", "Other")), false); err == nil || !strings.Contains(err.Error(), "serves the programs it was made with") {
		t.Errorf("expected a deploy refused, got %v", err)
	}
}

// A program taken away: refused while an instance is alive; then served
// no more, nor by a daemon started on the store; its versions kept, the
// taking away among them; deployed again as any.
func TestTakingAProgramAway(t *testing.T) {
	stores, kept := map[string]*flowrt.Memory{}, flowrt.NewMemory()
	e := newEmptyDaemon(t, stores, kept)
	e.deploy(t, false, packageOf(t, "tally", tally(1)))
	call(t, e.d, e.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t", "fields": {"owner": "ada"}}`)

	if status, body := call(t, e.d, e.server, "POST", "/api/undeploy", `{"program": "tally"}`); status != http.StatusConflict || !strings.Contains(body, "1 instances of it are alive: t") {
		t.Fatalf("taken away while an instance is alive: %d %s", status, body)
	}
	e.send(t, "t", "stop", "k1")
	call(t, e.d, e.server, "GET", "/api/instances/t/result?wait=10s", "")
	if status, body := call(t, e.d, e.server, "POST", "/api/undeploy", `{"program": "tally"}`); status != http.StatusOK {
		t.Fatalf("taken away: %d %s", status, body)
	}
	if status, body := call(t, e.d, e.server, "POST", "/api/undeploy", `{"program": "tally"}`); status != http.StatusConflict || !strings.Contains(body, "serves no program tally") {
		t.Errorf("taken away twice: %d %s", status, body)
	}
	if status, _ := call(t, e.d, e.server, "POST", "/api/instances", `{"flow": "Tally", "id": "u"}`); status != http.StatusNotFound {
		t.Errorf("a start of a flow taken away: %d", status)
	}
	_, body := call(t, e.d, e.server, "GET", "/api/deployments", "")
	var history []flowd.DeployView
	json.Unmarshal([]byte(body), &history)
	if len(history) != 2 || !history[0].Removed || history[1].Removed || history[1].Serving {
		t.Errorf("the versions, the taking away last: %s", body)
	}
	e.stop()

	// A daemon started on the store does not serve it
	again := newEmptyDaemon(t, stores, kept)
	if len(again.d.Flows) != 0 {
		t.Fatalf("expected nothing served, got %v", again.d.Flows)
	}
	// Deployed again, it is served, and what its instances were is there
	if status, body := again.deploy(t, false, packageOf(t, "tally", tally(2))); status != http.StatusOK {
		t.Fatalf("deployed again: %d %s", status, body)
	}
	if _, body := call(t, again.d, again.server, "GET", "/api/instances/t/result", ""); strings.TrimSpace(body) != `"ada 0"` {
		t.Errorf("the instance before: %s", body)
	}
}

// moduleProgram is a program whose flows are in the modules it imports:
// main.qi, and the files given, by name.
func moduleProgram(t *testing.T, modules map[string]string) (*flowd.Package, error) {
	t.Helper()
	files := map[string]string{"main.qi": "import tally;\n\nfn main() {}\n"}
	for name, src := range modules {
		files[name] = src
	}
	read := func(path string) (string, error) {
		if src, ok := files[filepath.Base(path)]; ok {
			return src, nil
		}
		return "", fmt.Errorf("no file %s", path)
	}
	errs := lerrors.NewHandler()
	result := compiler.CompileWith(files["main.qi"], "main.qi", read, errs)
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	return flowd.PackageOf("main", "main.qi", result.AST, result.Chunk, files)
}

// A flow declared in a module is the program's: deployed with it, started
// by its name, sent to, and what its run returns is kept.
func TestAFlowOfAModuleIsServedByTheProgramThatImportsIt(t *testing.T) {
	asModule := strings.Replace(tally(1), "\nfn main() {}\n", "", 1)
	pkg, err := moduleProgram(t, map[string]string{"tally.qi": asModule})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pkg.Flows, ",") != "tally.Tally" || len(pkg.Schema.Flows) != 1 || pkg.Schema.Flows[0].Name != "tally.Tally" {
		t.Fatalf("the flows of the program: %v", pkg.Flows)
	}

	e := newEmptyDaemon(t, map[string]*flowrt.Memory{}, flowrt.NewMemory())
	status, body := e.deploy(t, false, pkg)
	if status != http.StatusOK || !strings.Contains(body, `"tally.Tally"`) {
		t.Fatalf("deploy: %d %s", status, body)
	}
	if status, body := call(t, e.d, e.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t", "fields": {"owner": "ada"}}`); status != http.StatusCreated {
		t.Fatalf("a start by the flow's name: %d %s", status, body)
	}
	e.send(t, "t", "one", "k1")
	e.send(t, "t", "stop", "k2")
	e.waitFor(t, "the instance to end", func() bool { return e.status(t, "t") == "done" })
	_, body = call(t, e.d, e.server, "GET", "/api/instances/t", "")
	var v flowd.Detail
	json.Unmarshal([]byte(body), &v)
	if v.Flow != "tally.Tally" || string(v.Result) != `"ada 1"` {
		t.Errorf("got %s returning %s, want tally.Tally returning \"ada 1\"", v.Flow, v.Result)
	}

	// Two modules with a flow of one name: a start by the name could not
	// tell which
	_, err = moduleProgram(t, map[string]string{"tally.qi": "import other;\n\n" + asModule, "other.qi": asModule})
	if err == nil || !strings.Contains(err.Error(), "main has two flows Tally") {
		t.Errorf("two flows Tally: got %v", err)
	}
}

// named is a program of one flow, of that name.
func named(flow string) string {
	return `
import std.flow.{Flow};

struct ` + flow + ` { n: Int; }

impl Flow<Int> for ` + flow + ` {
    fn run() -> Int { return self.n; }
}

fn main() {}
`
}

// Two names whose tables are the same, a-b and a_b, Shop and shop: the
// second is refused, as it would run the instances of the first. Served
// again from a store where both were deployed before that was refused,
// the daemon serves one of them, and starts.
func TestTwoProgramsDoNotShareTheirTables(t *testing.T) {
	stores, kept := map[string]*flowrt.Memory{}, flowrt.NewMemory()
	first := newEmptyDaemon(t, stores, kept)
	if status, body := first.deploy(t, false, packageOf(t, "a-b", named("Alpha"))); status != http.StatusOK {
		t.Fatalf("deploy a-b: %d %s", status, body)
	}
	for _, name := range []string{"a_b", "A-B"} {
		if status, body := first.deploy(t, false, packageOf(t, name, named("Beta"))); status != http.StatusConflict || !strings.Contains(body, "tables of a-b") {
			t.Errorf("deploy %s beside a-b: %d %s", name, status, body)
		}
	}
	if status, body := first.deploy(t, false, packageOf(t, "a-b", named("Alpha"))); status != http.StatusOK {
		t.Errorf("a-b again: %d %s", status, body)
	}
	first.stop()

	// As a store of before had it: both deployed
	beta := packageOf(t, "a_b", named("Beta"))
	data, _ := json.Marshal(beta)
	if _, err := kept.PutDeployment(flowrt.Deployed{Name: "a_b", Version: "v-beta", At: time.Now().UnixMilli(), Package: data}); err != nil {
		t.Fatal(err)
	}
	again := newEmptyDaemon(t, stores, kept)
	defer again.stop()
	if flows := again.d.Flows; len(flows) != 1 {
		t.Errorf("served again, one of the two: %v", flows)
	}
}
