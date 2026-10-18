package flowd_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// kept is a daemon with what the console keeps: its users, its drafts,
// what happened.
func kept(t *testing.T) (*flowd.Daemon, *httptest.Server) {
	t.Helper()
	store, err := flowrt.OpenStore(filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := flowd.NewOf(nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Open = func(program string) (flowrt.Store, error) { return store.Program(program) }
	d.Deployments, d.Workers, d.Out = store, 2, &lines{}
	db, kind := store.Database()
	if d.Keep, err = flowd.OpenKeep(db, kind); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx, 5*time.Millisecond)
		close(done)
	}()
	server := httptest.NewServer(d.Handler())
	t.Cleanup(func() {
		server.Close()
		cancel()
		<-done
		store.Close()
	})
	return d, server
}

// browser is a person's browser: it keeps the cookies, and says it is
// the console when it changes something, unless told otherwise.
type browser struct {
	client *http.Client
	server *httptest.Server
	plain  bool // without the console's header
}

func newBrowser(t *testing.T, server *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{client: &http.Client{Jar: jar}, server: server}
}

func (b *browser) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, b.server.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if !b.plain {
		req.Header.Set("X-Qoqi-Console", "1")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// The console starts with admin, password admin, whose password is
// changed before anything else is done; then people sign in by name and
// password, and a session that changes something says it is the
// console's. The token is the commands', and signs no one in.
func TestTheConsoleStartsWithAdminWhoChoosesAPassword(t *testing.T) {
	d, server := kept(t)
	if made, err := d.Keep.EnsureAdmin(time.Now().UnixMilli()); err != nil || !made {
		t.Fatalf("admin made: %v %v", made, err)
	}
	if made, _ := d.Keep.EnsureAdmin(time.Now().UnixMilli()); made {
		t.Fatal("admin made twice")
	}
	ada := newBrowser(t, server)

	if status, _ := ada.do(t, "GET", "/api/instances", ""); status != http.StatusUnauthorized {
		t.Fatalf("no one signed in reads the instances: %d", status)
	}
	if status, said := ada.do(t, "POST", "/api/auth/setup", `{"name": "eve", "password": "a long password", "token": "`+d.Token+`"}`); status != http.StatusNotFound {
		t.Fatalf("there is no setup: %d %s", status, said)
	}
	if status, said := ada.do(t, "POST", "/api/auth/login", `{"token": "`+d.Token+`"}`); status == http.StatusOK {
		t.Fatalf("the token signs someone in: %d %s", status, said)
	}
	if status, said := ada.do(t, "POST", "/api/auth/login", `{"name": "admin", "password": "admin"}`); status != 200 || !strings.Contains(said, `"renew": true`) {
		t.Fatalf("admin signs in, and is told to change the password: %d %s", status, said)
	}
	if status, said := ada.do(t, "GET", "/api/instances", ""); status != http.StatusForbidden || !strings.Contains(said, `"renew"`) {
		t.Fatalf("before the password is changed, nothing else: %d %s", status, said)
	}
	if status, said := ada.do(t, "POST", "/api/users/admin/password", `{"password": "admin", "current": "admin"}`); status != http.StatusBadRequest {
		t.Fatalf("admin again: %d %s", status, said)
	}
	if status, said := ada.do(t, "POST", "/api/users/admin/password", `{"password": "a long password", "current": "admin"}`); status != http.StatusNoContent {
		t.Fatalf("the password changed: %d %s", status, said)
	}
	if status, said := ada.do(t, "GET", "/api/auth", ""); !strings.Contains(said, `"user": "admin"`) || strings.Contains(said, `"renew"`) {
		t.Fatalf("after the change: %d %s", status, said)
	}
	if status, said := ada.do(t, "GET", "/api/instances", ""); status != 200 {
		t.Fatalf("admin reads the instances: %d %s", status, said)
	}

	// A page of another site cannot send the console's header without
	// asking first: a change without it is refused
	ada.plain = true
	if status, _ := ada.do(t, "POST", "/api/users", `{"name": "eve", "password": "a long password"}`); status != http.StatusUnauthorized {
		t.Fatalf("a change without the console's header: %d", status)
	}
	ada.plain = false
	if status, said := ada.do(t, "POST", "/api/users", `{"name": "bob", "password": "another long one"}`); status != http.StatusCreated {
		t.Fatalf("admin adds bob: %d %s", status, said)
	}

	// A password given by another is changed first, as admin's was
	bob := newBrowser(t, server)
	if status, _ := bob.do(t, "POST", "/api/auth/login", `{"name": "bob", "password": "wrong password"}`); status != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", status)
	}
	if status, said := bob.do(t, "POST", "/api/auth/login", `{"name": "bob", "password": "another long one"}`); status != 200 || !strings.Contains(said, `"renew": true`) {
		t.Fatalf("bob signs in: %d %s", status, said)
	}
	if status, said := bob.do(t, "POST", "/api/users/bob/password", `{"password": "a third long one", "current": "wrong"}`); status != http.StatusUnauthorized {
		t.Fatalf("bob changes his password without the current one: %d %s", status, said)
	}
	if status, said := bob.do(t, "POST", "/api/users/bob/password", `{"password": "a third long one", "current": "another long one"}`); status != http.StatusNoContent {
		t.Fatalf("bob chooses his password: %d %s", status, said)
	}
	if status, said := bob.do(t, "DELETE", "/api/users/bob", ""); status != http.StatusConflict {
		t.Fatalf("bob takes himself away: %d %s", status, said)
	}
	if status, said := ada.do(t, "DELETE", "/api/users/bob", ""); status != http.StatusNoContent {
		t.Fatalf("admin takes bob away: %d %s", status, said)
	}
	if status, _ := bob.do(t, "GET", "/api/instances", ""); status != http.StatusUnauthorized {
		t.Fatalf("bob's session outlives him: %d", status)
	}

	if status, _ := ada.do(t, "POST", "/api/auth/logout", ""); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	if status, _ := ada.do(t, "GET", "/api/instances", ""); status != http.StatusUnauthorized {
		t.Fatalf("signed out, admin reads the instances: %d", status)
	}
	// The token still opens everything, for the commands
	if status, said := call(t, d, server, "GET", "/api/users", ""); status != 200 || !strings.Contains(said, `"admin"`) {
		t.Fatalf("the token lists the users: %d %s", status, said)
	}
}

// Five wrong passwords for a name, and it is refused for a while.
func TestGuessingPasswordsIsSlowedDown(t *testing.T) {
	d, server := kept(t)
	if status, said := call(t, d, server, "POST", "/api/users", `{"name": "ada", "password": "a long password"}`); status != http.StatusCreated {
		t.Fatalf("add ada: %d %s", status, said)
	}
	b := newBrowser(t, server)
	for i := 0; i < 5; i++ {
		b.do(t, "POST", "/api/auth/login", `{"name": "ada", "password": "guess"}`)
	}
	if status, _ := b.do(t, "POST", "/api/auth/login", `{"name": "ada", "password": "a long password"}`); status != http.StatusTooManyRequests {
		t.Fatalf("after five guesses: %d", status)
	}
}

const greeter = `import std.flow;
import std.flow.{Flow};

struct Greet {
    name: String;
}

impl Flow<String> for Greet {
    fn run() -> String {
        return hello(self.name);
    }
}

step fn hello(name: String) -> String {
    return "hello ${name}";
}

fn main() {}
`

// A program written in the console: saved file by file, checked, its
// deploy tried, then deployed; done with, the draft goes, and the
// program opens as what was deployed.
func TestAProgramIsWrittenAndDeployedFromTheConsole(t *testing.T) {
	d, server := kept(t)
	put := func(file, text string) (int, string) {
		body, _ := json.Marshal(map[string]string{"text": text})
		return call(t, d, server, "PUT", "/api/workspace/greet/files/"+file, string(body))
	}
	if status, said := put("Greet.qi", "x"); status != http.StatusBadRequest {
		t.Fatalf("a file out of the program: %d %s", status, said)
	}
	if status, said := put("greet.qi", strings.Replace(greeter, "hello(self.name)", "hello(self.nme)", 1)); status != 200 {
		t.Fatalf("save: %d %s", status, said)
	}
	status, said := call(t, d, server, "POST", "/api/workspace/greet/check", "")
	var checked flowd.Checked
	json.Unmarshal([]byte(said), &checked)
	var mistake flowd.Problem
	for _, p := range checked.Problems {
		if p.Severity == "error" && mistake.Message == "" {
			mistake = p
		}
	}
	if status != 200 || checked.OK || mistake.File != "greet.qi" || mistake.Line != 10 || mistake.Suggestion == "" {
		t.Fatalf("check of a mistake: %d %s", status, said)
	}
	// What the editor has, unsaved, is what is checked
	body, _ := json.Marshal(map[string]any{"files": map[string]string{"greet.qi": greeter}})
	status, said = call(t, d, server, "POST", "/api/workspace/greet/check", string(body))
	json.Unmarshal([]byte(said), &checked)
	if status != 200 || !checked.OK || len(checked.Flows) != 1 || checked.Flows[0] != "Greet" {
		t.Fatalf("check of what the editor has: %d %s", status, said)
	}
	put("greet.qi", greeter)

	status, said = call(t, d, server, "GET", "/api/workspace", "")
	if status != 200 || !strings.Contains(said, `"program": "greet"`) || !strings.Contains(said, `"draft": true`) {
		t.Fatalf("workspace: %d %s", status, said)
	}
	if status, said = call(t, d, server, "POST", "/api/workspace/greet/deploy?dry=1", ""); status != 200 || !strings.Contains(said, `"new": true`) {
		t.Fatalf("dry deploy: %d %s", status, said)
	}
	if status, said = call(t, d, server, "POST", "/api/workspace/greet/deploy", ""); status != 200 {
		t.Fatalf("deploy: %d %s", status, said)
	}
	if status, said = call(t, d, server, "POST", "/api/instances", `{"flow": "Greet", "id": "g", "fields": {"name": "ada"}}`); status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, said)
	}
	eventually(t, "g to end", func() bool {
		_, said := call(t, d, server, "GET", "/api/instances/g", "")
		return strings.Contains(said, `"done"`)
	})

	status, said = call(t, d, server, "GET", "/api/workspace/greet", "")
	var opened flowd.Opened
	json.Unmarshal([]byte(said), &opened)
	if status != 200 || opened.Draft || opened.Deployed == "" || len(opened.Files) != 1 || opened.Files[0].File != "greet.qi" || opened.Files[0].Text != greeter {
		t.Fatalf("deployed, the program opens as it was deployed: %d %s", status, said)
	}

	// Changed again, it is a draft again, made from what was deployed
	put("helpers.qi", "fn nothing() {}\n")
	status, said = call(t, d, server, "GET", "/api/workspace/greet", "")
	json.Unmarshal([]byte(said), &opened)
	if !opened.Draft || len(opened.Files) != 2 {
		t.Fatalf("a draft from what was deployed: %d %s", status, said)
	}
	if status, _ = call(t, d, server, "DELETE", "/api/workspace/greet", ""); status != http.StatusNoContent {
		t.Fatalf("discard: %d", status)
	}
	_, said = call(t, d, server, "GET", "/api/workspace/greet", "")
	json.Unmarshal([]byte(said), &opened)
	if opened.Draft || len(opened.Files) != 1 {
		t.Fatalf("discarded, the draft is gone: %s", said)
	}

	// What happened is kept, for the pages of activity
	eventually(t, "the activity of g", func() bool {
		_, said := call(t, d, server, "GET", "/api/stats?days=7", "")
		var stats flowd.Stats
		json.Unmarshal([]byte(said), &stats)
		last := stats.Days[len(stats.Days)-1]
		return len(stats.Days) == 7 && last.Started == 1 && last.Done == 1 && last.Steps == 1 && len(stats.Flows) == 1 && stats.Flows[0].Flow == "Greet" && len(stats.Steps) == 1
	})
}

func TestTheConsoleFormatsAProgram(t *testing.T) {
	tidy, err := flowd.Format("fn main(){println(\"x\");}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tidy, "fn main() {\n    println(\"x\");\n}") {
		t.Fatalf("formatted: %q", tidy)
	}
	if _, err := flowd.Format("fn main( {"); err == nil {
		t.Fatal("a program that does not parse is formatted")
	}
}

// A version deployed before is deployed again, as a new deploy: what the
// console's rollback does.
func TestAVersionIsDeployedAgain(t *testing.T) {
	d, server := kept(t)
	deploy := func(text string) flowd.Report {
		body, _ := json.Marshal(map[string]any{"files": map[string]string{"greet.qi": text}})
		status, said := call(t, d, server, "POST", "/api/workspace/greet/deploy", string(body))
		var out flowd.DraftDeploy
		json.Unmarshal([]byte(said), &out)
		if status != 200 || out.Report == nil {
			t.Fatalf("deploy: %d %s", status, said)
		}
		return *out.Report
	}
	first := deploy(greeter)
	second := deploy(strings.Replace(greeter, `"hello ${name}"`, `"hi ${name}"`, 1))
	if first.Version == second.Version {
		t.Fatal("two versions are one")
	}
	if status, said := call(t, d, server, "POST", "/api/deployments/1/redeploy?dry=1", ""); status != 200 || !strings.Contains(said, first.Version) {
		t.Fatalf("dry: %d %s", status, said)
	}
	if status, said := call(t, d, server, "POST", "/api/deployments/1/redeploy", ""); status != 200 {
		t.Fatalf("redeploy: %d %s", status, said)
	}
	_, said := call(t, d, server, "GET", "/api/deployments", "")
	var deploys []flowd.DeployView
	json.Unmarshal([]byte(said), &deploys)
	if len(deploys) != 3 || !deploys[0].Serving || deploys[0].Version != first.Version {
		t.Fatalf("the first version serves again, as the third deploy: %s", said)
	}
	if status, _ := call(t, d, server, "POST", "/api/deployments/9/redeploy", ""); status != http.StatusNotFound {
		t.Fatalf("no such deploy: %d", status)
	}
}
