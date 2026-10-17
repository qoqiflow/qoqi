package flowd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/memguard"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

const shop = `
import std.flow.{Flow};
import std.time;

struct Order { id: String; amount: Int; }
struct Broken { n: Int; }

impl Flow<String> for Order {
    fn run() -> String {
        let hold = reserve(self.id);
        wait time.after(30.milliseconds());
        return "${hold} paid ${self.amount * 100}";
    }
}

impl Flow<Int> for Broken {
    fn run() -> Int {
        return [1][self.n];
    }
}

step fn reserve(id: String) -> String {
    return "hold-" + id;
}

struct Nap { n: Int; }

impl Flow<Int> for Nap {
    fn run() -> Int {
        wait time.after(1.hours());
        return self.n;
    }
}

fn main() {}
`

// daemon serves the shop on a store in memory, its runtime running.
func daemon(t *testing.T) (*flowd.Daemon, *httptest.Server) {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.Compile(shop, "shop.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	rt.Workers = 4
	d := flowd.New(rt, []string{"Order", "Broken", "Nap"})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	t.Cleanup(func() {
		server.Close()
		cancel()
	})
	return d, server
}

func call(t *testing.T, d *flowd.Daemon, server *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+d.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// Every request carries the token, and names the daemon's own host.
func TestTheDaemonAsksForItsToken(t *testing.T) {
	d, server := daemon(t)
	for _, token := range []string{"", "Bearer wrong", "Bearer " + d.Token + "x"} {
		req, _ := http.NewRequest("GET", server.URL+"/api/instances", nil)
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("with %q: expected 401, got %d", token, resp.StatusCode)
		}
	}

	guarded := httptest.NewServer(d.Handler("127.0.0.1"))
	defer guarded.Close()
	req, _ := http.NewRequest("GET", guarded.URL+"/api/instances", nil)
	req.Header.Set("Authorization", "Bearer "+d.Token)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("another host: expected 403, got %d", resp.StatusCode)
	}
}

// A start, the same name again, another flow under the name, a flow the
// program has not, fields that are not an object.
func TestStartingInstances(t *testing.T) {
	d, server := daemon(t)
	status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Order", "id": "order:A1", "fields": {"id": "A1", "amount": 3}}`)
	if status != http.StatusCreated || !strings.Contains(body, `"$type": "Order"`) {
		t.Fatalf("expected 201 with the fields typed, got %d %s", status, body)
	}
	status, body = call(t, d, server, "POST", "/api/instances", `{"flow": "Order", "id": "order:A1", "fields": {"id": "A1", "amount": 99}}`)
	if status != http.StatusOK || !strings.Contains(body, `"amount": 3`) {
		t.Errorf("the name again: expected 200 and the first instance, got %d %s", status, body)
	}
	status, _ = call(t, d, server, "POST", "/api/instances", `{"flow": "Broken", "id": "order:A1", "fields": {"n": 0}}`)
	if status != http.StatusConflict {
		t.Errorf("another flow under the name: expected 409, got %d", status)
	}
	status, body = call(t, d, server, "POST", "/api/instances", `{"flow": "Refund", "id": "r", "fields": {}}`)
	if status != http.StatusNotFound || !strings.Contains(body, "Order, Broken") {
		t.Errorf("a flow the program has not: expected 404 naming those it has, got %d %s", status, body)
	}
	for _, bad := range []string{`not json`, `{"id": "x"}`, `{"flow": "Order", "id": "x", "fields": [1]}`} {
		if status, _ := call(t, d, server, "POST", "/api/instances", bad); status != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", bad, status)
		}
	}
}

// What run returned, waited for; an instance that stopped says why; one
// not there is 404; the flows count their instances.
func TestResultsAndLists(t *testing.T) {
	d, server := daemon(t)
	call(t, d, server, "POST", "/api/instances", `{"flow": "Order", "id": "order:A1", "fields": {"id": "A1", "amount": 3}}`)
	call(t, d, server, "POST", "/api/instances", `{"flow": "Broken", "id": "broken", "fields": {"n": 5}}`)

	if status, _ := call(t, d, server, "GET", "/api/instances/order:A1/result", ""); status != http.StatusAccepted && status != http.StatusOK {
		t.Errorf("not waiting: expected 202 while it sleeps, got %d", status)
	}
	status, body := call(t, d, server, "GET", "/api/instances/order:A1/result?wait=5s", "")
	if status != http.StatusOK || body != `"hold-A1 paid 300"` {
		t.Errorf("waited for: expected 200 and what run returned, got %d %s", status, body)
	}
	status, body = call(t, d, server, "GET", "/api/instances/broken/result?wait=5s", "")
	if status != http.StatusConflict || !strings.Contains(body, "index out of bounds") {
		t.Errorf("stopped: expected 409 and why, got %d %s", status, body)
	}
	if status, _ := call(t, d, server, "GET", "/api/instances/nobody", ""); status != http.StatusNotFound {
		t.Errorf("no instance: expected 404, got %d", status)
	}

	_, body = call(t, d, server, "GET", "/api/instances?status=done", "")
	var views []flowd.View
	if err := json.Unmarshal([]byte(body), &views); err != nil || len(views) != 1 || views[0].ID != "order:A1" {
		t.Errorf("done instances: expected order:A1, got %s", body)
	}
	_, body = call(t, d, server, "GET", "/api/flows", "")
	if !strings.Contains(body, `"done": 1`) || !strings.Contains(body, `"stopped": 1`) {
		t.Errorf("the flows and their instances: %s", body)
	}
}

// Where a daemon is, for the commands: written readable by its user
// alone, read back.
func TestTheAddressFile(t *testing.T) {
	store := t.TempDir() + "/flows.db"
	if _, err := flowd.ReadAddress(store); err == nil || !strings.Contains(err.Error(), "qoqi serve") {
		t.Errorf("no daemon: expected to be told how to start one, got %v", err)
	}
	want := flowd.Address{URL: "http://127.0.0.1:7070", Token: "secret", PID: 42}
	if err := flowd.WriteAddress(store, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(flowd.AddressFile(store))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("expected the file readable by its user alone, got %v %v", info.Mode(), err)
	}
	got, err := flowd.ReadAddress(store)
	if err != nil || got != want {
		t.Errorf("expected %+v back, got %+v %v", want, got, err)
	}
	_ = bytes.MinRead
}

// A cancellation: 202 and the instance ends failed; one that has ended,
// 409; one that is not there, 404. What waits for its result is told.
func TestCancellingAnInstance(t *testing.T) {
	d, server := daemon(t)
	call(t, d, server, "POST", "/api/instances", `{"flow": "Nap", "id": "nap", "fields": {"n": 1}}`)
	if status, body := call(t, d, server, "POST", "/api/instances/nap/cancel", `{"reason": "enough"}`); status != http.StatusAccepted || !strings.Contains(body, `"cancel": "enough"`) {
		t.Fatalf("expected 202 with the reason, got %d %s", status, body)
	}
	status, body := call(t, d, server, "GET", "/api/instances/nap/result?wait=5s", "")
	if status != http.StatusConflict || !strings.Contains(body, "cancelled: enough") {
		t.Errorf("expected 409 saying it was cancelled, got %d %s", status, body)
	}
	if status, body := call(t, d, server, "POST", "/api/instances/nap/cancel", ``); status != http.StatusConflict || !strings.Contains(body, "has ended") {
		t.Errorf("an ended instance: expected 409, got %d %s", status, body)
	}
	if status, _ := call(t, d, server, "POST", "/api/instances/nobody/cancel", ``); status != http.StatusNotFound {
		t.Errorf("no instance: expected 404, got %d", status)
	}
	_, body = call(t, d, server, "GET", "/api/instances?status=failed", "")
	if !strings.Contains(body, `"id": "nap"`) {
		t.Errorf("expected it among the failed: %s", body)
	}
}

// A stopped instance: a dry run of a resume says what it would do, a
// resume takes it up, an end ends it; what is not stopped is refused.
func TestResumingAndEnding(t *testing.T) {
	d, server := daemon(t)
	call(t, d, server, "POST", "/api/instances", `{"flow": "Broken", "id": "b", "fields": {"n": 3}}`)
	call(t, d, server, "GET", "/api/instances/b/result?wait=5s", "")

	status, body := call(t, d, server, "POST", "/api/instances/b/resume", `{"dry": true}`)
	if status != http.StatusOK || !strings.Contains(body, `"stopped": "`) || !strings.Contains(body, "index out of bounds") {
		t.Errorf("a dry run: expected 200 saying it would stop again, got %d %s", status, body)
	}
	if status, body := call(t, d, server, "POST", "/api/instances/b/resume", ``); status != http.StatusAccepted || !strings.Contains(body, `"replaying": true`) {
		t.Errorf("a resume: expected 202, got %d %s", status, body)
	}
	call(t, d, server, "GET", "/api/instances/b/result?wait=5s", "")
	if status, body := call(t, d, server, "POST", "/api/instances/b/end", `{"reason": "looked at"}`); status != http.StatusAccepted || !strings.Contains(body, `"ended": "looked at"`) {
		t.Errorf("an end: expected 202, got %d %s", status, body)
	}
	status, body = call(t, d, server, "GET", "/api/instances/b/result", "")
	if status != http.StatusConflict || !strings.Contains(body, "cancelled: looked at") {
		t.Errorf("the result of an ended instance: got %d %s", status, body)
	}
	for _, path := range []string{"/api/instances/b/resume", "/api/instances/b/end"} {
		if status, _ := call(t, d, server, "POST", path, ``); status != http.StatusConflict {
			t.Errorf("%s of an ended instance: expected 409, got %d", path, status)
		}
	}
	if status, _ := call(t, d, server, "POST", "/api/instances/nobody/resume", ``); status != http.StatusNotFound {
		t.Errorf("no instance: expected 404, got %d", status)
	}
}

// A message for an instance's inbox: delivered, the same key already,
// an inbox it has not 404, a full one 429, an ended instance 409.
func TestSendingAMessage(t *testing.T) {
	errs := lerrors.NewHandler()
	result := compiler.Compile(`
import std.flow;
import std.flow.{Flow, Inbox};

struct Note { text: String; }

struct Mailbox {
    owner: String;
    notes: Inbox<Note> = flow.inbox<Note>(1);
}

impl Flow<String> for Mailbox {
    fn run() -> String {
        let note = wait self.notes.receive();
        return self.owner + ": " + note.text;
    }
}

fn main() {}
`, "mail.qi", errs)
	if errs.HasErrors() {
		t.Fatalf("%v", errs.Diagnostics)
	}
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	d := flowd.New(rt, []string{"Mailbox"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	call(t, d, server, "POST", "/api/instances", `{"flow": "Mailbox", "id": "m", "fields": {"owner": "ada"}}`)
	if status, body := call(t, d, server, "POST", "/api/instances/m/inboxes/letters", `{"message": {"text": "hi"}}`); status != http.StatusNotFound {
		t.Errorf("an inbox it has not: expected 404, got %d %s", status, body)
	}
	if status, body := call(t, d, server, "POST", "/api/instances/m/inboxes/notes", `{"message": {"text": "hello"}, "key": "n1"}`); status != http.StatusOK || !strings.Contains(body, "delivered") {
		t.Errorf("a message: expected delivered, got %d %s", status, body)
	}
	status, body := call(t, d, server, "GET", "/api/instances/m/result?wait=5s", "")
	if status != http.StatusOK || body != `"ada: hello"` {
		t.Errorf("expected the note taken, got %d %s", status, body)
	}
	if status, _ := call(t, d, server, "POST", "/api/instances/m/inboxes/notes", `{"message": {"text": "late"}}`); status != http.StatusConflict {
		t.Errorf("an instance that has ended: expected 409, got %d", status)
	}

	call(t, d, server, "POST", "/api/instances", `{"flow": "Mailbox", "id": "full", "fields": {"owner": "lin"}}`)
	cancel() // nothing takes the messages now
	time.Sleep(20 * time.Millisecond)
	call(t, d, server, "POST", "/api/instances/full/inboxes/notes", `{"message": {"text": "one"}, "key": "n1"}`)
	if status, body := call(t, d, server, "POST", "/api/instances/full/inboxes/notes", `{"message": {"text": "one"}, "key": "n1"}`); status != http.StatusOK || !strings.Contains(body, "already") {
		t.Errorf("the same key: expected already, got %d %s", status, body)
	}
	if status, _ := call(t, d, server, "POST", "/api/instances/full/inboxes/notes", `{"message": {"text": "two"}}`); status != http.StatusTooManyRequests {
		t.Errorf("a full inbox: expected 429, got %d", status)
	}
}
