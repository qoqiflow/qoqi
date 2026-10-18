package flowd_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// What the console asks of the daemon: the program described, starts
// written as plain JSON and typed by the daemon, where an instance waits
// and what its steps returned, the source, the events as they happen, and
// the pages themselves.

const expenses = `import std.flow.{Flow, Settled};
import std.time;

// Who asks, and how much.
struct Claim {
    who: Person;
    amount: Int;  // in cents
    tags: [String];
    note: String?;
    rate: Float;
    urgent: Bool;
    kind: Kind;
}

struct Person {
    name: String;
    email: String;
}

enum Kind {
    Travel,
    Meal(Int),
    Other { label: String, code: Int },
}

enum Decision {
    Approved,
    Refused(String, String),
}

// An expense that waits an hour before it is approved.
struct Expense {
    claim: Claim;
}

impl Flow<Result<Decision, String>> for Expense {
    fn run() -> Result<Decision, String> {
        if !check(self.claim) {
            return Err("nothing to pay");
        }

        nap();
        return Ok(Decision.Approved);
    }
}

fn nap() {
    wait time.after(1.hours());
}

// Checks a claim.
step fn check(claim: Claim) -> Bool {
    println("checking ${claim.who.name}");
    return claim.amount > 0;
}

step fn pay(amount: Int) -> Settled<String> within 5000.milliseconds() {
    return Settled.Done("paid ${amount}");
}

fn main() {}
`

// The lines of expenses where things are, as the tests look for them.
const (
	lineWait     = 48 // wait time.after(1.hours());
	lineNapCall  = 42 // nap();
	lineRun      = 37 // fn run()
	lineExpense  = 32 // struct Expense
	lineCheckFn  = 52 // step fn check
	lineClaimDoc = 5  // struct Claim
)

// described serves expenses from a file, with what the daemon knows of
// it, on a store in memory.
func described(t *testing.T) (*flowd.Daemon, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "expense.qi")
	if err := os.WriteFile(file, []byte(expenses), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := lerrors.NewHandler()
	result, err := compiler.CompileFileOptions(file, compiler.Options{}, errs)
	if err != nil || errs.HasErrors() {
		t.Fatalf("%v %v", err, errs.Diagnostics)
	}
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	d := flowd.New(rt, []string{"Expense"})
	d.Schema = flowd.SchemaOf(file, result.AST, []string{file})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	t.Cleanup(func() {
		server.Close()
		cancel()
	})
	return d, server, file
}

const aClaim = `{"claim": {"who": {"name": "ada", "email": "ada@example.com"}, "amount": 1200, "tags": ["travel", "q4"], "rate": 1, "urgent": true, "kind": {"Meal": 3}}}`

func TestTheProgramIsDescribed(t *testing.T) {
	d, server, file := described(t)
	status, body := call(t, d, server, "GET", "/api/program", "")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", status, body)
	}
	var p flowd.Schema
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Flows) != 1 || p.Flows[0].Name != "Expense" {
		t.Fatalf("expected the flow Expense, got %+v", p.Flows)
	}
	flow := p.Flows[0]
	if flow.Returns.Text != "Result<Decision, String>" || flow.Returns.Kind != "enum" {
		t.Errorf("what it returns: %+v", flow.Returns)
	}
	if flow.Doc != "An expense that waits an hour before it is approved." {
		t.Errorf("its doc: %q", flow.Doc)
	}
	if flow.At == nil || flow.At.Line != lineExpense || flow.Run == nil || flow.Run.Line != lineRun || flow.Run.File != file {
		t.Errorf("where it is written: %+v %+v", flow.At, flow.Run)
	}

	claim := p.Types["Claim"]
	if claim == nil || claim.Doc != "Who asks, and how much." || claim.At.Line != lineClaimDoc {
		t.Fatalf("the struct Claim, with its doc: %+v", claim)
	}
	kinds := map[string]string{}
	docs := map[string]string{}
	for _, f := range claim.Fields {
		kinds[f.Name] = f.Type.Kind + " " + f.Type.Text
		docs[f.Name] = f.Doc
	}
	want := map[string]string{
		"who":    "struct Person",
		"amount": "int Int",
		"tags":   "array [String]",
		"note":   "option String?",
		"rate":   "float Float",
		"urgent": "bool Bool",
		"kind":   "enum Kind",
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("Claim.%s: expected %s, got %s", name, kind, kinds[name])
		}
	}
	if docs["amount"] != "in cents" {
		t.Errorf("a comment at the end of a field's line is its doc: %q", docs["amount"])
	}

	kind := p.Types["Kind"]
	if kind == nil || len(kind.Variants) != 3 {
		t.Fatalf("the enum Kind: %+v", kind)
	}
	if v := kind.Variants[1]; v.Name != "Meal" || v.Named || len(v.Values) != 1 || v.Values[0].Name != "0" {
		t.Errorf("Meal(Int), by place: %+v", v)
	}
	if v := kind.Variants[2]; v.Name != "Other" || !v.Named || v.Values[0].Name != "label" || v.Values[1].Type.Kind != "int" {
		t.Errorf("Other { label, code }, by name: %+v", v)
	}
	result := p.Types["std.result.Result<Decision, String>"]
	if result == nil || result.Variants[0].Name != "Ok" || result.Variants[0].Values[0].Type.Text != "Decision" {
		t.Errorf("a generic enum of the library, with its arguments: %+v", result)
	}

	steps := map[string]*flowd.StepInfo{}
	for _, s := range p.Steps {
		steps[s.Name] = s
	}
	if s := steps["check"]; s == nil || s.Doc != "Checks a claim." || s.Returns.Kind != "bool" || s.Params[0].Type.Text != "Claim" || s.Once || s.At.Line != lineCheckFn {
		t.Errorf("the step check: %+v", s)
	}
	if s := steps["pay"]; s == nil || !s.Once || s.Returns.Text != "Settled<String>" {
		t.Errorf("a step never run twice says so: %+v", s)
	}
	if len(p.Files) != 1 || p.Files[0] != file {
		t.Errorf("the files the pages may show: %v", p.Files)
	}
}

// A start is plain JSON: the daemon gives each struct its type and reads
// each variant in any of the forms a person writes. The instance it
// makes runs: the data is the program's.
func TestStartsAreTypedByTheDaemon(t *testing.T) {
	d, server, _ := described(t)
	forms := map[string]string{
		"travel":       `"kind": "Travel"`,
		"meal":         `"kind": {"Meal": 3}`,
		"meal-list":    `"kind": {"Meal": [3]}`,
		"other-named":  `"kind": {"Other": {"label": "taxi", "code": 7}}`,
		"other-placed": `"kind": {"Other": ["taxi", 7]}`,
		"as-data":      `"kind": {"$type": "Kind.Meal", "$variant": true, "0": 3}`,
	}
	for name, kind := range forms {
		fields := `{"claim": {"who": {"name": "ada", "email": "a@x"}, "amount": 5, "tags": [], "rate": 2, "urgent": false, ` + kind + `}}`
		status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "id": "e:`+name+`", "fields": `+fields+`}`)
		if status != http.StatusCreated {
			t.Errorf("%s: expected 201, got %d %s", name, status, body)
			continue
		}
		for _, typed := range []string{`"$type": "Claim"`, `"$type": "Person"`, `"$type": "Kind.`, `"rate": 2.0`, `"note": null`} {
			if !strings.Contains(body, typed) {
				t.Errorf("%s: expected %s in %s", name, typed, body)
			}
		}
		// A variant holds its values by place, as the program makes it,
		// named in the source or not
		if strings.HasPrefix(name, "other") && (!strings.Contains(body, `"0": "taxi"`) || !strings.Contains(body, `"1": 7`)) {
			t.Errorf("%s: expected the values of Other by place, in %s", name, body)
		}
	}
	// Each of them runs its step with what it was given, and sleeps
	deadline := time.Now().Add(5 * time.Second)
	for name := range forms {
		for {
			_, body := call(t, d, server, "GET", "/api/instances/e:"+name, "")
			var detail flowd.Detail
			_ = json.Unmarshal([]byte(body), &detail)
			if detail.Status == "waiting" && len(detail.Kept) == 1 {
				break
			}
			if detail.Status == "stopped" || time.Now().After(deadline) {
				t.Fatalf("%s: expected it asleep after its step, got %s", name, body)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// What is refused is refused with where and why.
func TestStartsSayWhatIsWrong(t *testing.T) {
	d, server, _ := described(t)
	refused := map[string]string{
		`{}`: "claim is missing: a Claim",
		`{"claim": {"who": {"name": "ada"}, "amount": 1, "tags": [], "rate": 1, "urgent": true, "kind": "Travel"}}`:                           "claim.who.email is missing: a String",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": "lots", "tags": [], "rate": 1, "urgent": true, "kind": "Travel"}}`:          `claim.amount is an Int, not the text \"lots\"`,
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1.5, "tags": [], "rate": 1, "urgent": true, "kind": "Travel"}}`:             "claim.amount is an Int, not the number 1.5",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": ["a", 2], "rate": 1, "urgent": true, "kind": "Travel"}}`:         "claim.tags[1] is a String, not the number 2",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": [], "rate": 1, "urgent": "yes", "kind": "Travel"}}`:              "claim.urgent is a Bool",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": [], "rate": 1, "urgent": true, "kind": "Bus"}}`:                  "claim.kind is a Kind: one of Travel, Meal, Other",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": [], "rate": 1, "urgent": true, "kind": "Meal"}}`:                 "Meal holds (Int)",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": [], "rate": 1, "urgent": true, "kind": {"Other": ["x"]}}}`:       "Other holds (label: String, code: Int), not 1 values",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 1, "tags": [], "rate": 1, "urgent": true, "kind": "Travel", "x": 1}}`:       "claim has no field x: it has who: Person",
		`{"claim": {"who": {"name": "a", "email": "e"}, "amount": 140737488355328, "tags": [], "rate": 1, "urgent": true, "kind": "Travel"}}`: "out of the range of an Int",
		`{"claim": {"$type": "Person", "name": "a", "email": "e"}}`:                                                                           "claim is a Claim, not a Person",
		`[1]`: "the fields of Expense is a Expense, written as an object",
	}
	for fields, message := range refused {
		status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "id": "x", "fields": `+fields+`}`)
		if status != http.StatusBadRequest || !strings.Contains(body, message) {
			t.Errorf("%s: expected 400 saying %q, got %d %s", fields, message, status, body)
		}
	}
	if _, body := call(t, d, server, "GET", "/api/instances", ""); strings.Contains(body, `"id"`) {
		t.Errorf("nothing refused was started: %s", body)
	}
}

// A dry run checks without starting, and says when the name is taken; a
// start without a name is given one.
func TestDryRunsAndNamesGiven(t *testing.T) {
	d, server, _ := described(t)
	status, body := call(t, d, server, "POST", "/api/instances?dry=1", `{"flow": "Expense", "id": "e:1", "fields": `+aClaim+`}`)
	if status != http.StatusOK || !strings.Contains(body, `"$type": "Claim"`) || strings.Contains(body, "taken") {
		t.Fatalf("a dry run: expected 200 with the data, got %d %s", status, body)
	}
	if _, list := call(t, d, server, "GET", "/api/instances", ""); strings.Contains(list, "e:1") {
		t.Errorf("a dry run starts nothing: %s", list)
	}
	if status, body := call(t, d, server, "POST", "/api/instances?dry=1", `{"flow": "Expense", "fields": {}}`); status != http.StatusBadRequest || !strings.Contains(body, "missing") {
		t.Errorf("a dry run refuses as a start would: %d %s", status, body)
	}

	status, body = call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "fields": `+aClaim+`}`)
	var started flowd.View
	_ = json.Unmarshal([]byte(body), &started)
	if status != http.StatusCreated || !strings.HasPrefix(started.ID, "expense:") || len(started.ID) != len("expense:")+8 {
		t.Fatalf("a start without a name: expected one drawn, expense:…, got %d %s", status, body)
	}
	_, body = call(t, d, server, "POST", "/api/instances?dry=1", `{"flow": "Expense", "id": "`+started.ID+`", "fields": `+aClaim+`}`)
	if !strings.Contains(body, `"taken"`) || !strings.Contains(body, started.ID) {
		t.Errorf("a dry run on a name taken says so: %s", body)
	}
}

// Where an instance waits: the line of the program, the function that
// waits first, the run that called it next, the library hidden behind
// them; what its step returned, and what it printed.
func TestWhereAnInstanceWaits(t *testing.T) {
	d, server, file := described(t)
	call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "id": "e:1", "fields": `+aClaim+`}`)
	var detail flowd.Detail
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, body := call(t, d, server, "GET", "/api/instances/e:1", "")
		detail = flowd.Detail{}
		_ = json.Unmarshal([]byte(body), &detail)
		if detail.Status == "waiting" && len(detail.Tasks) == 1 && len(detail.Kept) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected it asleep, got %s", body)
		}
		time.Sleep(5 * time.Millisecond)
	}
	task := detail.Tasks[0]
	if task.Task != "0" || task.Waiting != "suspendFor" || task.Until < time.Now().Add(59*time.Minute).UnixMilli() {
		t.Errorf("task 0 waits for the clock, an hour from now: %+v", task)
	}
	var own []vm.PlaceFrame
	for _, f := range task.Frames {
		if f.File == file {
			own = append(own, f)
		}
	}
	if len(own) != 2 || own[0].Function != "nap" || own[0].Line != lineWait || own[1].Function != "Expense.run" || own[1].Line != lineNapCall {
		t.Errorf("expected nap at %d, called by Expense.run at %d, got %+v", lineWait, lineNapCall, task.Frames)
	}
	if kept := detail.Kept[0]; !strings.HasPrefix(kept.Key, "e:1/"+detail.Birth+"/") || !strings.HasSuffix(kept.Key, ":check#1") || string(kept.Result) != "true" {
		t.Errorf("what check returned, under its key: %+v", kept)
	}
	if len(detail.Output) != 1 || detail.Output[0].Text != "checking ada" || detail.Output[0].ID != "e:1" {
		t.Errorf("what it printed, as its own: %+v", detail.Output)
	}
	if detail.Tries[detail.Kept[0].Key] != 1 {
		t.Errorf("how many times the step left: %+v", detail.Tries)
	}

	// Ended, it has no tasks: where it was is no more
	call(t, d, server, "POST", "/api/instances/e:1/cancel", `{"reason": "enough"}`)
	call(t, d, server, "GET", "/api/instances/e:1/result?wait=5s", "")
	_, body := call(t, d, server, "GET", "/api/instances/e:1", "")
	if !strings.Contains(body, `"tasks": []`) || !strings.Contains(body, `"status": "failed"`) {
		t.Errorf("an ended instance: %s", body)
	}
}

// The source of the program's own files is served, line by line; no
// other file is, whatever the path asked.
func TestTheSourceIsServed(t *testing.T) {
	d, server, file := described(t)
	status, body := call(t, d, server, "GET", "/api/source?file="+file, "")
	var source struct {
		File  string   `json:"file"`
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal([]byte(body), &source)
	if status != http.StatusOK || source.File != file || len(source.Lines) < lineWait || strings.TrimSpace(source.Lines[lineWait-1]) != "wait time.after(1.hours());" {
		t.Errorf("the program's file: %d %s", status, body)
	}
	for _, other := range []string{"/etc/passwd", filepath.Dir(file) + "/../expense.qi", "<std>/task.qi", ""} {
		if status, _ := call(t, d, server, "GET", "/api/source?file="+other, ""); status != http.StatusNotFound {
			t.Errorf("%q: expected 404, got %d", other, status)
		}
	}
}

// The events of an instance arrive as they happen: its step leaving,
// what it printed, and the instance as it changed, asleep.
func TestEventsAsTheyHappen(t *testing.T) {
	d, server, _ := described(t)
	req, _ := http.NewRequest("GET", server.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+d.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected a stream, got %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "id": "e/with/slashes", "fields": `+aClaim+`}`)

	seen := map[string]bool{}
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() && !(seen["step"] && seen["output"] && seen["asleep"]) {
		line := lines.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e flowd.Event
		if err := json.Unmarshal([]byte(line[6:]), &e); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if e.ID != "e/with/slashes" {
			t.Errorf("every event names its instance, a name with slashes too: %+v", e)
		}
		switch {
		case e.Kind == "step" && e.Step == "check" && strings.HasSuffix(e.Key, ":check#1"):
			seen["step"] = true
		case e.Kind == "output" && e.Text == "checking ada":
			seen["output"] = true
		case e.Kind == "instance" && e.Instance != nil && e.Instance.Status == "waiting" && e.Instance.WakeAt != "":
			seen["asleep"] = true
		}
	}
	if !(seen["step"] && seen["output"] && seen["asleep"]) {
		t.Errorf("expected the step, the output and the instance asleep, saw %v", seen)
	}

	// Without the token, no stream
	plain, err := http.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusUnauthorized {
		t.Errorf("the events without the token: expected 401, got %d", plain.StatusCode)
	}
}

// The output of the program, of one instance or of all.
func TestTheOutput(t *testing.T) {
	d, server, _ := described(t)
	for _, id := range []string{"e:1", "e:2"} {
		call(t, d, server, "POST", "/api/instances", `{"flow": "Expense", "id": "`+id+`", "fields": `+aClaim+`}`)
	}
	deadline := time.Now().Add(5 * time.Second)
	var all []flowd.Line
	for len(all) < 2 && time.Now().Before(deadline) {
		_, body := call(t, d, server, "GET", "/api/output", "")
		all = nil
		_ = json.Unmarshal([]byte(body), &all)
		time.Sleep(5 * time.Millisecond)
	}
	if len(all) != 2 {
		t.Fatalf("expected a line from each, got %+v", all)
	}
	_, body := call(t, d, server, "GET", "/api/output?instance=e:2", "")
	var one []flowd.Line
	_ = json.Unmarshal([]byte(body), &one)
	if len(one) != 1 || one[0].ID != "e:2" {
		t.Errorf("the output of e:2 alone: %+v", one)
	}
}

// A daemon without a description of its program says so, and takes
// starts as the program's data, as it did before.
func TestADaemonWithoutItsProgram(t *testing.T) {
	d, server := daemon(t)
	if status, _ := call(t, d, server, "GET", "/api/program", ""); status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
	if status, _ := call(t, d, server, "GET", "/api/source?file=shop.qi", ""); status != http.StatusNotFound {
		t.Errorf("no source without the program: %d", status)
	}
}

// The token of the daemon is kept in its home, for its user alone, and
// is the same each time it starts.
func TestTheTokenOfAHome(t *testing.T) {
	store := t.TempDir()
	first, err := flowd.HomeToken(store)
	if err != nil || len(first) < 32 {
		t.Fatalf("expected a token drawn, got %q %v", first, err)
	}
	info, err := os.Stat(flowd.TokenFile(store))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("expected the file readable by its user alone, got %v %v", info, err)
	}
	again, err := flowd.HomeToken(store)
	if err != nil || again != first {
		t.Errorf("a daemon started again has the same token: %q %q %v", first, again, err)
	}
	// A file that holds no token worth the name is replaced
	if err := os.WriteFile(flowd.TokenFile(store), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := flowd.HomeToken(store); fresh == "short" || len(fresh) < 32 {
		t.Errorf("expected a new token, got %q", fresh)
	}
}

// The home is QOQI_PATH, or ~/.qoqi; its store is flows.db in it, unless
// QOQI_STORE names another.
func TestTheHomeOfQoqi(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QOQI_PATH", dir)
	t.Setenv("QOQI_STORE", "")
	if flowd.Home() != dir {
		t.Errorf("home: %s", flowd.Home())
	}
	if err := flowd.MakeHome(dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "logs")); err != nil || !info.IsDir() {
		t.Errorf("the logs are made in the home: %v", err)
	}
	if got := flowd.HomeStore(dir); got != filepath.Join(dir, "flows.db") {
		t.Errorf("store: %s", got)
	}
	t.Setenv("QOQI_STORE", "postgres://u:p@db/flows")
	if got := flowd.HomeStore(dir); got != "postgres://u:p@db/flows" {
		t.Errorf("store of QOQI_STORE: %s", got)
	}
	t.Setenv("QOQI_PATH", "")
	if home, _ := os.UserHomeDir(); flowd.Home() != filepath.Join(home, ".qoqi") {
		t.Errorf("default home: %s", flowd.Home())
	}
}

// The console is served without the token: its page, its files with
// their types, and the page again for an address of the console itself.
// It may not be framed nor load from elsewhere; the API still asks.
func TestThePages(t *testing.T) {
	_, server := daemon(t)
	get := func(path string) (*http.Response, string) {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(body)
	}
	resp, index := get("/")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(index, "<div id=\"root\">") {
		t.Fatalf("the page: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, rule := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, rule) {
			t.Errorf("expected %s in %q", rule, csp)
		}
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("the page is not kept: %q", resp.Header.Get("Cache-Control"))
	}

	for _, path := range []string{"/instances/order:1", "/program", "/instances?flow=Order"} {
		if resp, body := get(path); resp.StatusCode != http.StatusOK || body != index {
			t.Errorf("%s: expected the page of the console, got %d", path, resp.StatusCode)
		}
	}

	// Its script, named after what it holds, is kept for good
	start := strings.Index(index, "/assets/")
	if start < 0 {
		t.Fatalf("the page names no script: %s", index)
	}
	script := index[start : start+strings.Index(index[start:], `"`)]
	resp, body := get(script)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") || len(body) == 0 {
		t.Errorf("%s: %d %s", script, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("%s is kept for good: %q", script, resp.Header.Get("Cache-Control"))
	}

	resp, _ = get("/api/instances")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the API without the token: expected 401, got %d", resp.StatusCode)
	}
}

// Two programs of a folder, served by one daemon, four workers each: the
// console sees both as one, a flow with an inbox starts without it and
// takes messages, and what instances print side by side is told apart.
func TestTwoProgramsSideBySide(t *testing.T) {
	dir := t.TempDir()
	programs := map[string]string{
		"counting.qi": `import std.flow.{Flow};

// Counts aloud, one line a number.
struct Count {
    who: String;
    upTo: Int;
}

impl Flow<Int> for Count {
    fn run() -> Int {
        let total = 0;

        for n in 1..self.upTo + 1 {
            total = total + say(self.who, n);
        }

        return total;
    }
}

step fn say(who: String, n: Int) -> Int {
    println("${who} ${n}");
    return n;
}

fn main() {}
`,
		"mail.qi": `import std.flow;
import std.flow.{Flow, Inbox};

struct Note {
    text: String;
}

// Waits for two notes.
struct Mailbox {
    owner: String;
    notes: Inbox<Note> = flow.inbox<Note>(4);
}

impl Flow<String> for Mailbox {
    fn run() -> String {
        let first = wait self.notes.receive();
        let second = wait self.notes.receive();

        return "${self.owner}: ${first.text}, ${second.text}";
    }
}

fn main() {}
`,
	}
	var served []flowd.Program
	var schemas []*flowd.Schema
	for _, name := range []string{"counting.qi", "mail.qi"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(programs[name]), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := lerrors.NewHandler()
		result, err := compiler.CompileFileOptions(file, compiler.Options{}, errs)
		if err != nil || errs.HasErrors() {
			t.Fatalf("%s: %v %v", name, err, errs.Diagnostics)
		}
		rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
		rt.Workers = 4
		flows := []string{"Count"}
		if name == "mail.qi" {
			flows = []string{"Mailbox"}
		}
		served = append(served, flowd.Program{Name: strings.TrimSuffix(name, ".qi"), Runtime: rt, Flows: flows})
		schemas = append(schemas, flowd.SchemaOf(file, result.AST, []string{file}))
	}
	d, err := flowd.NewOf(served)
	if err != nil {
		t.Fatal(err)
	}
	d.Schema = flowd.MergeSchemas(dir, schemas)
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	defer func() {
		server.Close()
		cancel()
	}()

	// One description for both, the inbox apart from what a start gives
	_, body := call(t, d, server, "GET", "/api/program", "")
	var described flowd.Schema
	if err := json.Unmarshal([]byte(body), &described); err != nil {
		t.Fatal(err)
	}
	if len(described.Flows) != 2 || len(described.Files) != 2 {
		t.Fatalf("expected the flows and files of both programs: %s", body)
	}
	for _, flow := range described.Flows {
		if flow.Name != "Mailbox" {
			continue
		}
		if len(flow.Fields) != 1 || flow.Fields[0].Name != "owner" || len(flow.Inboxes) != 1 || flow.Inboxes[0].Type.Kind != "inbox" || flow.Inboxes[0].Type.Elem.Name != "Note" {
			t.Errorf("expected owner to be given and notes to be an inbox of Note: %s", body)
		}
	}

	// Eight counters at once, each printing its own lines
	names := []string{"ada", "bob", "cy", "dee", "eve", "fay", "gus", "hal"}
	for _, who := range names {
		if status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Count", "id": "count:`+who+`", "fields": {"who": "`+who+`", "upTo": 6}}`); status != http.StatusCreated {
			t.Fatalf("start %s: %d %s", who, status, body)
		}
	}
	if status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Mailbox", "id": "m", "fields": {"owner": "ada"}}`); status != http.StatusCreated {
		t.Fatalf("a start without its inbox: %d %s", status, body)
	}
	for i, text := range []string{"hello", "again"} {
		key := fmt.Sprint("n", i)
		if status, body := call(t, d, server, "POST", "/api/instances/m/inboxes/notes", `{"message": {"text": "`+text+`"}, "key": "`+key+`"}`); status != http.StatusOK {
			t.Fatalf("a note: %d %s", status, body)
		}
	}

	for _, who := range names {
		if status, body := call(t, d, server, "GET", "/api/instances/count:"+who+"/result?wait=10s", ""); status != http.StatusOK || strings.TrimSpace(body) != "21" {
			t.Errorf("count:%s: expected 21, got %d %s", who, status, body)
		}
	}
	if status, body := call(t, d, server, "GET", "/api/instances/m/result?wait=10s", ""); status != http.StatusOK || strings.TrimSpace(body) != `"ada: hello, again"` {
		t.Errorf("the mailbox: got %d %s", status, body)
	}

	// Every line is its instance's, in its order, whichever ran beside it
	for _, who := range names {
		_, body := call(t, d, server, "GET", "/api/output?instance=count:"+who, "")
		var lines []flowd.Line
		if err := json.Unmarshal([]byte(body), &lines); err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, l := range lines {
			texts = append(texts, l.Text)
		}
		want := []string{who + " 1", who + " 2", who + " 3", who + " 4", who + " 5", who + " 6"}
		if strings.Join(texts, "|") != strings.Join(want, "|") {
			t.Errorf("count:%s printed %q", who, texts)
		}
	}

	// An instance shown is read with its own program's bytecode and store
	if status, body := call(t, d, server, "GET", "/api/instances/m", ""); status != http.StatusOK || !strings.Contains(body, `"kept"`) {
		t.Errorf("the mailbox shown: %d %s", status, body)
	}
}

// A message is written as a person writes it, and typed from the program:
// a variant by its name, what it holds after it; a wrong one is refused,
// and the refusal says where.
func TestMessagesAreTypedFromTheProgram(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tally.qi")
	if err := os.WriteFile(file, []byte(`import std.flow;
import std.flow.{Flow, Inbox};

enum Command {
    Add(Int),
    Label { text: String, loud: Bool },
    Stop,
}

struct Tally {
    commands: Inbox<Command> = flow.inbox<Command>(8);
}

impl Flow<String> for Tally {
    fn run() -> String {
        let total = 0;
        let label = "";

        while true {
            match wait self.commands.receive() {
                case Add(n): { total = total + n; }
                case Label { text, loud }: { label = if loud { text.upper() } else { text }; }
                case Stop: { return "${label} ${total}"; }
            }
        }

        return "";
    }
}

fn main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := lerrors.NewHandler()
	result, err := compiler.CompileFileOptions(file, compiler.Options{}, errs)
	if err != nil || errs.HasErrors() {
		t.Fatalf("%v %v", err, errs.Diagnostics)
	}
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	d := flowd.New(rt, []string{"Tally"})
	d.Schema = flowd.SchemaOf(file, result.AST, []string{file})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	defer func() {
		server.Close()
		cancel()
	}()

	if status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Tally", "id": "t"}`); status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, body)
	}
	for i, wrong := range map[string]string{
		`{"Add": "three"}`:           "is an Int",
		`"Halt"`:                     "Halt",
		`{"Label": {"text": "sum"}}`: "Label holds (text: String, loud: Bool)",
	} {
		if status, body := call(t, d, server, "POST", "/api/instances/t/inboxes/commands", `{"message": `+i+`}`); status != http.StatusBadRequest || !strings.Contains(body, wrong) {
			t.Errorf("%s: expected 400 saying %q, got %d %s", i, wrong, status, body)
		}
	}
	for i, message := range []string{`{"Add": 3}`, `{"Label": {"text": "sum", "loud": true}}`, `{"Add": 4}`, `"Stop"`} {
		if status, body := call(t, d, server, "POST", "/api/instances/t/inboxes/commands", fmt.Sprintf(`{"message": %s, "key": "c%d"}`, message, i)); status != http.StatusOK {
			t.Fatalf("%s: %d %s", message, status, body)
		}
	}
	if status, body := call(t, d, server, "GET", "/api/instances/t/result?wait=10s", ""); status != http.StatusOK || strings.TrimSpace(body) != `"SUM 7"` {
		t.Errorf("expected SUM 7, got %d %s", status, body)
	}
	if status, body := call(t, d, server, "POST", "/api/instances/t/inboxes/nothing", `{"message": 1}`); status != http.StatusBadRequest || !strings.Contains(body, "no inbox nothing") {
		t.Errorf("an inbox it has not: %d %s", status, body)
	}
}
