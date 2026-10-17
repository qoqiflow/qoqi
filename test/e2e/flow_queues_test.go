package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/pkg/flow"
)

// A shop whose steps its application carries out, in a project that
// declares the application's queues.
const queuedShop = `import std.flow.{Flow};

struct Cart { id: String; amount: Int; }
struct Hold { id: String; }

struct Order { cart: Cart; }

impl Flow<Result<String, String>> for Order {
    fn run() -> Result<String, String> {
        let hold = reserve(self.cart)?;
        let paid = charge(self.cart.id, self.cart.amount)?;
        return Ok("${hold.id} ${paid}");
    }
}

step fn reserve(cart: Cart) -> Result<Hold, String> by warehouse undo release;
step fn release(hold: Hold) -> Result<Void, String> by warehouse;
step fn charge(id: String, amount: Int) -> Result<String, String> within 5000.milliseconds() by billing;

fn main() {}
`

const queuedProject = `{
    "name": "shop",
    "queues": {
        "warehouse": { "url": "env:WAREHOUSE_URL", "secret": "env:SHOP_SECRET" },
        "billing":   { "url": "env:BILLING_URL", "secret": "env:SHOP_SECRET" }
    }
}
`

// The application of the shop, as PHP would be: one page answers the
// steps of both queues, after checking their signature.
type shopApplication struct {
	mu   sync.Mutex
	seen []string
	bad  []string

	// later, when set, answers charge on a request of its own after a
	// 202, as a long step does: given the step's key, attempt and token
	later func(key string, attempt int, token string)
}

func (a *shopApplication) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	queue := r.URL.Query().Get("queue")
	signature := r.Header.Get(flow.SignatureHeader)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := flow.Verify(signature, time.Now(), r.Method, r.URL.EscapedPath(), queue, body, "s3cret"); err != nil {
		a.bad = append(a.bad, signature)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var task struct {
		Step    string          `json:"step"`
		Key     string          `json:"key"`
		Attempt int             `json:"attempt"`
		Token   string          `json:"token"`
		Args    json.RawMessage `json:"args"`
	}
	json.Unmarshal(body, &task)
	a.seen = append(a.seen, task.Step+" "+string(task.Args))
	switch task.Step {
	case "reserve":
		io.WriteString(w, `{"value": {"Ok": {"id": "h-1"}}, "undo": {"id": "h-1"}}`)
	case "charge":
		if a.later != nil {
			w.WriteHeader(http.StatusAccepted)
			go a.later(task.Key, task.Attempt, task.Token)
			return
		}
		var args struct{ Amount int }
		json.Unmarshal(task.Args, &args)
		if args.Amount > 1000 {
			io.WriteString(w, `{"value": {"Err": "declined"}}`)
			return
		}
		fmt.Fprintf(w, `{"value": {"Ok": "paid %d"}}`, args.Amount)
	case "release":
		io.WriteString(w, `{"value": {"Ok": null}}`)
	default:
		io.WriteString(w, `{"unknown_shape": "?"}`)
	}
}

func (a *shopApplication) steps() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.seen, "\n")
}

// qoqi serve sends the steps of the project's queues to the
// application, at the addresses the environment gives, and the flows go
// on with what it answers: an Err undone by the release it named.
func TestStepsTheApplicationCarriesOut(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"shop.qi": queuedShop, "qoqi.json": queuedProject} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := &shopApplication{}
	server := httptest.NewServer(app)
	defer server.Close()

	out := &lockedWriter{}
	cmd := exec.Command(qoqi, "serve", "shop.qi", "--listen", "127.0.0.1:0")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"WAREHOUSE_URL="+server.URL+"/flow-steps?queue=warehouse",
		"BILLING_URL="+server.URL+"/flow-steps?queue=billing",
		"SHOP_SECRET=s3cret")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "flows.db.daemon")); err == nil && strings.Contains(string(data), `"pid": `+itoa(cmd.Process.Pid)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, c := range []struct{ id, amount, variant, result string }{
		{"o1", "300", "Ok", "h-1 paid 300"},
		{"o2", "4000", "Err", "declined"},
	} {
		fields := `{"cart": {"id": "` + c.id + `", "amount": ` + c.amount + `}}`
		if got, code := flowCommand(t, dir, "start", "Order", c.id, fields); code != 0 {
			t.Fatalf("start %s: %d %s\n%s", c.id, code, got, out.String())
		}
		got, code := flowCommand(t, dir, "result", c.id, "--wait", "20s")
		if code != 0 || !strings.Contains(got, c.variant) || !strings.Contains(got, `"`+c.result+`"`) {
			t.Errorf("%s: expected %s, got %d %s\ndaemon:\n%s", c.id, c.result, code, got, out.String())
		}
	}
	steps := app.steps()
	for _, want := range []string{
		`reserve {"cart":`,
		`charge {"id":"o1","amount":300}`,
		`charge {"id":"o2","amount":4000}`,
		`release {"hold":{"id":"h-1"}}`,
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("expected the application to see %s, saw:\n%s", want, steps)
		}
	}
	if strings.Count(steps, "release") != 1 || len(app.bad) > 0 {
		t.Errorf("one release, every request signed right; saw:\n%s\nbad: %v", steps, app.bad)
	}
}

// A queue the environment does not say where to find stops the daemon
// before it serves, naming the variable.
func TestAQueueWithoutItsAddress(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"shop.qi": queuedShop, "qoqi.json": queuedProject} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(qoqi, "serve", "shop.qi", "--listen", "127.0.0.1:0")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BILLING_URL=", "WAREHOUSE_URL=", "SHOP_SECRET=")
	done := make(chan []byte)
	go func() {
		output, _ := cmd.CombinedOutput()
		done <- output
	}()
	select {
	case output := <-done:
		if !strings.Contains(string(output), "the queue billing takes its url from BILLING_URL, which the environment does not set") {
			t.Errorf("expected the variable named, got:\n%s", output)
		}
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the daemon served without its queues")
	}
}

// A step by a queue qoqi.json does not declare is an error, as a name
// misspelt is.
func TestAQueueTheProjectDoesNotDeclare(t *testing.T) {
	dir := t.TempDir()
	project := strings.Replace(queuedProject, `"billing"`, `"payments"`, 1)
	for name, text := range map[string]string{"shop.qi": queuedShop, "qoqi.json": project} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(qoqi, "check", "shop.qi")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "F0012") || !strings.Contains(string(output), "charge is carried out by the queue billing, which the project does not declare") {
		t.Errorf("expected F0012 at charge, got %v:\n%s", err, output)
	}
}

// A charge that answers later: 202 on its request, then its answer to
// the daemon's POST /api/attempts, which takes the attempt's token and
// not the daemon's. The instance waits meanwhile, and goes on with it.
func TestAStepAnsweredLaterOverHTTP(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"shop.qi": queuedShop, "qoqi.json": queuedProject} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	told := make(chan string, 4)
	app := &shopApplication{}
	app.later = func(key string, attempt int, token string) {
		data, err := os.ReadFile(filepath.Join(dir, "flows.db.daemon"))
		var address struct{ URL string }
		if err != nil || json.Unmarshal(data, &address) != nil {
			told <- fmt.Sprintf("no address: %v", err)
			return
		}
		post := func(token, body string) string {
			k, _ := json.Marshal(key)
			resp, err := http.Post(address.URL+"/api/attempts", "application/json",
				strings.NewReader(fmt.Sprintf(`{"key": %s, "attempt": %d, "token": %q, %s}`, k, attempt, token, body)))
			if err != nil {
				return err.Error()
			}
			defer resp.Body.Close()
			said, _ := io.ReadAll(resp.Body)
			return fmt.Sprintf("%d %s", resp.StatusCode, strings.Join(strings.Fields(string(said)), ""))
		}
		told <- post(strings.Repeat("0", 32), `"working": true`)
		told <- post(token, `"value": {"Ok": "paid later"}`)
		told <- post(token, `"value": {"Ok": "twice"}`)
	}
	server := httptest.NewServer(app)
	defer server.Close()

	out := &lockedWriter{}
	cmd := exec.Command(qoqi, "serve", "shop.qi", "--listen", "127.0.0.1:0")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"WAREHOUSE_URL="+server.URL+"/flow-steps?queue=warehouse",
		"BILLING_URL="+server.URL+"/flow-steps?queue=billing",
		"SHOP_SECRET=s3cret")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "flows.db.daemon")); err == nil && strings.Contains(string(data), `"pid": `+itoa(cmd.Process.Pid)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fields := `{"cart": {"id": "o3", "amount": 300}}`
	if got, code := flowCommand(t, dir, "start", "Order", "o3", fields); code != 0 {
		t.Fatalf("start: %d %s\n%s", code, got, out.String())
	}
	got, code := flowCommand(t, dir, "result", "o3", "--wait", "20s")
	if code != 0 || !strings.Contains(got, `"h-1 paid later"`) {
		t.Fatalf("expected the later answer, got %d %s\ndaemon:\n%s", code, got, out.String())
	}
	for _, want := range []string{"403", `200 {"kept":true}`, `200 {"already":true}`} {
		select {
		case said := <-told:
			if !strings.HasPrefix(said, want) {
				t.Errorf("expected %s, the daemon said %s", want, said)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the application was not answered")
		}
	}
}
