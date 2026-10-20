package flowrt_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
	"github.com/qoqiflow/qoqi/pkg/flow"
)

// A shop whose steps its application carries out: the warehouse holds
// the cart, undone by a release should anything after fail; billing
// charges; the bank wires, which is settled or not, never sent twice
// blind.
const shop = `
import std.flow;
import std.flow.{Flow, Settled};

struct Cart { id: String; amount: Int; }
struct Hold { id: String; }
struct Charge { id: String; }

struct Order {
    cart: Cart;
    wired: Bool;
}

impl Flow<Result<String, String>> for Order {
    fn run() -> Result<String, String> {
        let hold = reserve(self.cart)?;
        let paid = charge(self.cart.id, self.cart.amount)?;
        notify("paid ${paid.id}");

        if self.wired {
            match wire(self.cart.amount) {
                case Done(Ok(w)): { return Ok("wired ${w}"); }
                case Done(Err(why)): { return Err(why); }
                case Unknown: { return Ok("wire unknown"); }
            }
        }

        return Ok("held ${hold.id}, paid ${paid.id}");
    }
}

step fn reserve(cart: Cart) -> Result<Hold, String> by warehouse undo release;
step fn release(hold: Hold) -> Result<Void, String> by warehouse;
step fn charge(id: String, amount: Int) -> Result<Charge, String> within 5000.milliseconds() by billing;
step fn notify(text: String) by billing;
step fn wire(amount: Int) -> Settled<Result<String, String>> within 5000.milliseconds() by bank;

fn main() {}
`

const shopSecret = "s3cret"

func compiledWithQueues(t *testing.T, src string, queues ...string) *vm.Chunk {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.CompileOptions(src, "flows.qi", compiler.Options{Queues: queues}, errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	return result.Chunk
}

// delivery is a step as the application received it.
type delivery struct {
	Queue   string
	Key     string          `json:"key"`
	Attempt int             `json:"attempt"`
	Token   string          `json:"token"`
	Until   int64           `json:"until"`
	Keep    int64           `json:"keep"`
	Step    string          `json:"step"`
	Shape   string          `json:"shape"`
	Args    json.RawMessage `json:"args"`
	Probe   bool            `json:"probe"`
	Reclaim int             `json:"reclaim"`
}

// application answers the steps of its queues as answer says, checking
// each signature, and keeps what it was sent.
type application struct {
	t        *testing.T
	mu       sync.Mutex
	got      []delivery
	probes   int
	down     bool   // a GET is answered 503
	manifest string // what a GET is answered; {} when not said
	clock    func() time.Time
	answer   func(d delivery, n int) (status int, body string)
}

func (a *application) handler(queue string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		signature := r.Header.Get(flow.SignatureHeader)
		now := time.Now()
		if a.clock != nil {
			now = a.clock()
		}
		if err := flow.Verify(signature, now, r.Method, r.URL.EscapedPath(), queue, body, shopSecret); err != nil {
			a.t.Errorf("a request to %s signed %q: %v", queue, signature, err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			// Asked whether it is back, after a pause
			a.mu.Lock()
			a.probes++
			down := a.down
			a.mu.Unlock()
			manifest := a.manifest
			if down {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if manifest == "" {
				manifest = `{}`
			}
			io.WriteString(w, manifest)
			return
		}
		var d delivery
		if err := json.Unmarshal(body, &d); err != nil {
			a.t.Errorf("a delivery that is not JSON: %s", body)
		}
		d.Queue = queue
		a.mu.Lock()
		a.got = append(a.got, d)
		n := 0
		for _, g := range a.got {
			if g.Step == d.Step {
				n++
			}
		}
		a.mu.Unlock()
		status, answer := a.answer(d, n)
		w.WriteHeader(status)
		io.WriteString(w, answer)
	})
}

func (a *application) deliveries() []delivery {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]delivery(nil), a.got...)
}

// steps are the deliveries, as step#attempt.
func (a *application) steps() string {
	var parts []string
	for _, d := range a.deliveries() {
		parts = append(parts, fmt.Sprintf("%s#%d", d.Step, d.Attempt))
	}
	return strings.Join(parts, " ")
}

// served is a runtime of the shop whose queues the application serves.
func served(t *testing.T, app *application) (*flowrt.Runtime, *flowrt.Memory) {
	t.Helper()
	queues := map[string]flowrt.Queue{}
	for _, name := range []string{"warehouse", "billing", "bank"} {
		server := httptest.NewServer(app.handler(name))
		t.Cleanup(server.Close)
		queues[name] = flowrt.Queue{URL: server.URL + "/flow-steps", Secret: shopSecret}
	}
	store := flowrt.NewMemory()
	rt := flowrt.New(compiledWithQueues(t, shop, "warehouse", "billing", "bank"), store, &bytes.Buffer{})
	q := flowrt.NewHTTPQueues(queues)
	q.Backoff = 5 * time.Millisecond
	rt.Queues = q
	return rt, store
}

// The answers of a shop that works.
func working(d delivery, n int) (int, string) {
	switch d.Step {
	case "reserve":
		return 200, `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Hold", "id": "h-1"}}, "undo": {"$type": "Hold", "id": "h-1"}}`
	case "charge":
		return 200, `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Charge", "id": "ch-9"}}}`
	case "wire":
		return 200, `{"value": {"$type": "std.flow.Settled.Done", "$variant": true, "0": {"$type": "std.result.Result.Ok", "$variant": true, "0": "w-3"}}}`
	case "release":
		return 200, `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": null}}`
	}
	return 200, `{"value": null}`
}

// sameJSON says whether two texts are the same JSON, whatever the order
// of their keys.
func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return fmt.Sprint(x) == fmt.Sprint(y)
}

func shopOrder(wired bool) string {
	return fmt.Sprintf(`{"cart": {"$type": "Cart", "id": "c1", "amount": 1200}, "wired": %v}`, wired)
}

// Each step goes to its queue, signed, with its key, attempt, a token of
// its own and the time it has, its parameters by name; what comes back
// is what the step returns.
func TestStepsTheApplicationCarriesOut(t *testing.T) {
	app := &application{t: t, answer: working}
	rt, store := served(t, app)
	before := time.Now().UnixMilli()
	inst := runFlow(t, rt, store, "o", "Order", shopOrder(false))
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "held h-1, paid ch-9") {
		t.Fatalf("expected done, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	if got := app.steps(); got != "reserve#1 charge#1 notify#1" {
		t.Fatalf("expected each step once, got %s", got)
	}
	got := app.deliveries()
	tokens := map[string]bool{}
	for _, d := range got {
		if !strings.HasPrefix(d.Key, "o/") || !strings.HasSuffix(d.Key, ":"+d.Step+"#1") {
			t.Errorf("%s: a key of the instance and the step, got %s", d.Step, d.Key)
		}
		if len(d.Token) != 32 || tokens[d.Token] {
			t.Errorf("%s: a token of its own, got %q", d.Step, d.Token)
		}
		tokens[d.Token] = true
		if d.Shape == "" || d.Keep != flowrt.Keep.Milliseconds() {
			t.Errorf("%s: a shape and how long answers are kept, got %q %d", d.Step, d.Shape, d.Keep)
		}
	}
	if got[0].Queue != "warehouse" || got[1].Queue != "billing" {
		t.Errorf("each to its queue, got %s %s", got[0].Queue, got[1].Queue)
	}
	if want := `{"cart": {"$type": "Cart", "id": "c1", "amount": 1200}}`; !sameJSON(string(got[0].Args), want) {
		t.Errorf("reserve is given its cart by name, got %s", got[0].Args)
	}
	if !strings.HasPrefix(string(got[1].Args), `{"id":"c1","amount":1200}`) {
		t.Errorf("charge is given its parameters by name, got %s", got[1].Args)
	}
	if until := got[1].Until; until < before+5000 || until > time.Now().UnixMilli()+5000 {
		t.Errorf("charge has five seconds from when it left, got until %d", until)
	}
	if until := got[0].Until; until < before+60000 {
		t.Errorf("reserve has a minute, said nothing, got until %d", until)
	}
}

// What a step returns need not be a struct: a list, a number, nothing.
func TestAnswersThatAreNoObject(t *testing.T) {
	const lookup = `
import std.flow.{Flow};

struct Lookup { phone: String; }
impl Flow<String> for Lookup {
    fn run() -> String {
        let codes = find(self.phone);
        let count = countOf(self.phone);
        let name = nameOf(self.phone);
        return "${codes.join(",")} ${count} ${name ?? "nobody"}";
    }
}
step fn find(phone: String) -> [String] by app;
step fn countOf(phone: String) -> Int by app;
step fn nameOf(phone: String) -> String? by app;
fn main() {}
`
	app := &application{t: t, answer: func(d delivery, n int) (int, string) {
		switch d.Step {
		case "find":
			return 200, `{"value": ["A1", "B2"]}`
		case "countOf":
			return 200, `{"value": 2}`
		}
		return 200, `{"value": null}`
	}}
	server := httptest.NewServer(app.handler("app"))
	t.Cleanup(server.Close)
	store := flowrt.NewMemory()
	rt := flowrt.New(compiledWithQueues(t, lookup, "app"), store, &bytes.Buffer{})
	rt.Queues = flowrt.NewHTTPQueues(map[string]flowrt.Queue{"app": {URL: server.URL + "/steps", Secret: shopSecret}})

	inst := runFlow(t, rt, store, "l", "Lookup", `{"phone": "690"}`)
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "A1,B2 2 nobody") {
		t.Fatalf("expected done with the answers, got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
}

// What follows each answer of the application, or none.
func TestWhatAnAnswerSays(t *testing.T) {
	for _, c := range []struct {
		name    string
		wired   bool
		answer  func(d delivery, n int) (int, string)
		status  flowrt.Status
		result  string // in the result, or in why it stopped
		steps   string
		release bool
	}{
		{"a retry is sent again later, as its next attempt", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" && n == 1 {
				return 200, `{"retry": {"reason": "the card network is down", "after": 10}}`
			}
			return working(d, n)
		}, flowrt.Done, "paid ch-9", "reserve#1 charge#1 charge#2 notify#1", false},

		{"a 503 may have arrived: a plain step is sent again", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" && n < 3 {
				return 503, `deploying`
			}
			return working(d, n)
		}, flowrt.Done, "paid ch-9", "reserve#1 charge#1 charge#2 charge#3 notify#1", false},

		{"a body that is not JSON may have arrived", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" && n == 1 {
				return 200, `<html>ok</html>`
			}
			return working(d, n)
		}, flowrt.Done, "paid ch-9", "reserve#1 charge#1 charge#2 notify#1", false},

		{"failed is a panic of the step, and stops the instance", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" {
				return 200, `{"failed": "Stripe\\Exception: no such customer"}`
			}
			return working(d, n)
		}, flowrt.Stopped, "charge failed in the application: Stripe\\Exception: no such customer", "reserve#1 charge#1", false},

		{"an Err is an answer: what was held is released", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" {
				return 200, `{"value": {"$type": "std.result.Result.Err", "$variant": true, "0": "declined"}}`
			}
			return working(d, n)
		}, flowrt.Done, `"declined"`, "reserve#1 charge#1 release#1", true},

		{"a value without the undo its declaration names is a panic", false, func(d delivery, n int) (int, string) {
			if d.Step == "reserve" {
				return 200, `{"value": {"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Hold", "id": "h-1"}}}`
			}
			return working(d, n)
		}, flowrt.Stopped, "the application gave reserve no undo", "reserve#1", false},

		{"an undo with an Err is a panic", false, func(d delivery, n int) (int, string) {
			if d.Step == "reserve" {
				return 200, `{"value": {"$type": "std.result.Result.Err", "$variant": true, "0": "none left"}, "undo": {"$type": "Hold", "id": "h-1"}}`
			}
			return working(d, n)
		}, flowrt.Stopped, "an undo with an Err", "reserve#1", false},

		{"unknown stops a plain step's instance", false, func(d delivery, n int) (int, string) {
			if d.Step == "charge" {
				return 200, `{"unknown": true}`
			}
			return working(d, n)
		}, flowrt.Stopped, "charge may have acted", "reserve#1 charge#1", false},

		{"a settled step that may have arrived is Unknown, not sent again", true, func(d delivery, n int) (int, string) {
			if d.Step == "wire" {
				return 502, `bad gateway`
			}
			return working(d, n)
		}, flowrt.Done, "wire unknown", "reserve#1 charge#1 notify#1 wire#1", false},

		{"a settled step told to retry is sent again", true, func(d delivery, n int) (int, string) {
			if d.Step == "wire" && n == 1 {
				return 200, `{"retry": {"reason": "cut-off passed", "after": 10}}`
			}
			return working(d, n)
		}, flowrt.Done, "wired w-3", "reserve#1 charge#1 notify#1 wire#1 wire#2", false},

		{"a settled step that failed is Unknown", true, func(d delivery, n int) (int, string) {
			if d.Step == "wire" {
				return 200, `{"failed": "timeout talking to the bank"}`
			}
			return working(d, n)
		}, flowrt.Done, "wire unknown", "reserve#1 charge#1 notify#1 wire#1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := &application{t: t, answer: c.answer}
			rt, store := served(t, app)
			inst := runFlow(t, rt, store, "o", "Order", shopOrder(c.wired))
			said := string(inst.Result) + inst.Stopped
			if inst.Status != c.status || !strings.Contains(said, c.result) {
				t.Fatalf("expected %s with %s, got %s %s %q", c.status, c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := app.steps(); got != c.steps {
				t.Errorf("expected %s, got %s", c.steps, got)
			}
			if c.release {
				got := app.deliveries()
				last := got[len(got)-1]
				if last.Queue != "warehouse" || !sameJSON(string(last.Args), `{"hold":{"$type":"Hold","id":"h-1"}}`) {
					t.Errorf("release is given the hold the warehouse gave, got %s %s", last.Queue, last.Args)
				}
			}
		})
	}
}

// A queue that cannot be reached: the step never left, and is sent
// again as the same attempt, once the queue is there.
func TestAStepThatNeverArrivedIsSentAgain(t *testing.T) {
	app := &application{t: t, answer: working}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		time.Sleep(100 * time.Millisecond)
		again, err := net.Listen("tcp", address)
		if err != nil {
			t.Error(err)
			return
		}
		server := &http.Server{Handler: app.handler("billing")}
		t.Cleanup(func() { server.Close() })
		go server.Serve(again)
	}()
	warehouse := httptest.NewServer(app.handler("warehouse"))
	defer warehouse.Close()

	store := flowrt.NewMemory()
	rt := flowrt.New(compiledWithQueues(t, shop, "warehouse", "billing", "bank"), store, &bytes.Buffer{})
	q := flowrt.NewHTTPQueues(map[string]flowrt.Queue{
		"warehouse": {URL: warehouse.URL, Secret: shopSecret},
		"billing":   {URL: "http://" + address + "/flow-steps", Secret: shopSecret},
	})
	q.Backoff, q.Longest = 10*time.Millisecond, 20*time.Millisecond
	rt.Queues = q
	inst := runFlow(t, rt, store, "o", "Order", shopOrder(false))
	<-served
	if inst.Status != flowrt.Done {
		t.Fatalf("expected done, got %s %q", inst.Status, inst.Stopped)
	}
	if got := app.steps(); got != "reserve#1 charge#1 notify#1" {
		t.Errorf("charge arrived once, its first attempt, got %s", got)
	}
}

// Without its queues, a step of a queue stops its instance, naming it.
func TestAStepOfAQueueNothingServes(t *testing.T) {
	store := flowrt.NewMemory()
	rt := flowrt.New(compiledWithQueues(t, shop, "warehouse", "billing", "bank"), store, &bytes.Buffer{})
	inst := runFlow(t, rt, store, "o", "Order", shopOrder(false))
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "reserve is carried out by the queue warehouse, which nothing serves here") {
		t.Fatalf("expected it stopped, naming reserve, got %s %q", inst.Status, inst.Stopped)
	}
}

// Stopped at every write, the shop ends the same, the release given its
// hold; the application sees each step under one key, whatever the
// attempts.
func TestQueuesSurviveAStopAtEveryWrite(t *testing.T) {
	declined := func(d delivery, n int) (int, string) {
		if d.Step == "charge" {
			return 200, `{"value": {"$type": "std.result.Result.Err", "$variant": true, "0": "declined"}}`
		}
		return working(d, n)
	}
	chunk := compiledWithQueues(t, shop, "warehouse", "billing", "bank")
	run := func(app *application, store *flowrt.Memory) error {
		queues := map[string]flowrt.Queue{}
		for _, name := range []string{"warehouse", "billing"} {
			server := httptest.NewServer(app.handler(name))
			t.Cleanup(server.Close)
			queues[name] = flowrt.Queue{URL: server.URL, Secret: shopSecret}
		}
		rt := flowrt.New(chunk, store, &bytes.Buffer{})
		rt.Queues = flowrt.NewHTTPQueues(queues)
		if _, err := rt.Start("o", "Order", []byte(shopOrder(false))); err != nil {
			return err
		}
		return rt.RunUntilIdle(10 * time.Second)
	}
	plain := flowrt.NewMemory()
	if err := run(&application{t: t, answer: declined}, plain); err != nil {
		t.Fatal(err)
	}
	clean, _ := plain.Get("o")
	for k := 1; k <= plain.Writes(); k++ {
		app := &application{t: t, answer: declined}
		store := flowrt.NewMemory()
		store.FailAt = k
		if err := run(app, store); err != nil && !strings.Contains(err.Error(), flowrt.ErrCrash.Error()) {
			t.Fatalf("stopping at write %d: %v", k, err)
		}
		store.FailAt = 0
		if err := run(app, store); err != nil {
			t.Fatalf("after stopping at write %d: %v", k, err)
		}
		inst, _ := store.Get("o")
		if inst.Status != clean.Status || string(inst.Result) != string(clean.Result) {
			t.Errorf("stopped at write %d: expected %s %s, got %s %s %q", k, clean.Status, clean.Result, inst.Status, inst.Result, inst.Stopped)
		}
		keys := map[string]map[string]bool{}
		for _, d := range app.deliveries() {
			if keys[d.Step] == nil {
				keys[d.Step] = map[string]bool{}
			}
			keys[d.Step][d.Key] = true
		}
		for step, under := range keys {
			if len(under) != 1 {
				t.Errorf("stopped at write %d: %s sent under %d keys", k, step, len(under))
			}
		}
		if len(keys["release"]) != 1 {
			t.Errorf("stopped at write %d: the hold released, got %s", k, app.steps())
		}
	}
}

// An address that is no application of the library, a page that answers
// 200 to anything: three answers that are none of a step's, and the
// instance stops, saying so, rather than send its step again for ever.
// The log says the queue fails, and is paused.
func TestAnAddressThatIsNoApplicationStopsItsStep(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "This URL has no default content configured.")
	}))
	defer page.Close()
	store := flowrt.NewMemory()
	rt := flowrt.New(compiledWithQueues(t, shop, "warehouse", "billing", "bank"), store, &bytes.Buffer{})
	q := flowrt.NewHTTPQueues(map[string]flowrt.Queue{"warehouse": {URL: page.URL, Secret: shopSecret}})
	q.Backoff, q.Longest = time.Millisecond, 5*time.Millisecond
	var said []string
	var mu sync.Mutex
	q.Log = func(format string, args ...any) {
		mu.Lock()
		said = append(said, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	rt.Queues = q
	inst := runFlow(t, rt, store, "o", "Order", shopOrder(false))
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "3 times in a row with none of the answers a step has") || !strings.Contains(inst.Stopped, "Qoqi library") || !strings.Contains(inst.Stopped, "This URL has no default content") {
		t.Fatalf("expected it stopped, saying why, got %s %q", inst.Status, inst.Stopped)
	}
	if n := len(store.Attempts(inst.ID + "/" + inst.Birth + "/0:0:reserve#1")); n != 3 {
		t.Errorf("expected three attempts, got %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(said) == 0 || !strings.Contains(said[0], "the queue warehouse fails") {
		t.Errorf("the log: %q", said)
	}
}
