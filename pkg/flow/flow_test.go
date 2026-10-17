package flow_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/pkg/flow"
)

func TestASignature(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	body := []byte(`{"key": "k"}`)
	signed := flow.Sign("s1", now, "POST", "/steps", "billing", body)
	if err := flow.Verify(signed, now, "POST", "/steps", "billing", body, "s1"); err != nil {
		t.Fatalf("its own signature: %v", err)
	}
	// While a secret is changed, either is taken, and a header may carry both
	both := signed + ", v1=" + strings.Repeat("0", 64)
	if err := flow.Verify(both, now, "POST", "/steps", "billing", body, "old", "s1"); err != nil {
		t.Errorf("two secrets: %v", err)
	}
	for _, c := range []struct {
		name                string
		header, path, queue string
		body                string
		at                  time.Time
	}{
		{"another body", signed, "/steps", "billing", `{"key": "x"}`, now},
		{"another path", signed, "/other", "billing", `{"key": "k"}`, now},
		{"another queue", signed, "/steps", "bank", `{"key": "k"}`, now},
		{"played again later", signed, "/steps", "billing", `{"key": "k"}`, now.Add(6 * time.Minute)},
		{"none", "", "/steps", "billing", `{"key": "k"}`, now},
	} {
		if err := flow.Verify(c.header, c.at, "POST", c.path, c.queue, []byte(c.body), "s1"); !errors.Is(err, flow.ErrSignature) {
			t.Errorf("%s: expected ErrSignature, got %v", c.name, err)
		}
	}
}

type invoice struct {
	ID     string `json:"id"`
	Amount int    `json:"amount"`
}

// post sends a queue a step, signed, as the daemon does.
func post(t *testing.T, q *flow.Queue, method string, task flow.Task) (int, string) {
	t.Helper()
	body, _ := json.Marshal(task)
	if method == "GET" {
		body = nil
	}
	server := httptest.NewServer(q)
	defer server.Close()
	req, _ := http.NewRequest(method, server.URL+"/steps", strings.NewReader(string(body)))
	req.Header.Set(flow.SignatureHeader, flow.Sign("s1", time.Now(), method, "/steps", "billing", body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	said, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(said))
}

func TestAQueueAnswersItsSteps(t *testing.T) {
	q := flow.NewQueue("billing", "s1")
	q.Handle("charge-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		var args struct {
			Invoice invoice `json:"invoice"`
		}
		if err := task.Bind(&args); err != nil {
			return flow.Failure(err)
		}
		if args.Invoice.Amount > 1000 {
			a, _ := flow.Value(flow.Err[string]("declined"))
			return a
		}
		if _, ok := ctx.Deadline(); !ok {
			return flow.Failure(errors.New("no deadline"))
		}
		a, _ := flow.WithUndo(flow.Ok[string, string]("ch-"+args.Invoice.ID+":"+task.Key), map[string]string{"id": "ch-" + args.Invoice.ID})
		return a
	})
	q.Handle("busy-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		return flow.AnswerOf(flow.Retry("the gateway is down", 4*time.Second))
	})
	q.Handle("long-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		return flow.AnswerOf(flow.ErrLater)
	})
	q.Handle("broken-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		var none []int
		_ = none[3]
		return flow.Answer{}
	})

	until := time.Now().Add(time.Minute).UnixMilli()
	task := func(shape, args string) flow.Task {
		return flow.Task{Key: "o/k/1:0:charge#1", Attempt: 1, Token: "t", Until: until, Step: "charge", Shape: shape, Args: json.RawMessage(args)}
	}
	for _, c := range []struct {
		name   string
		task   flow.Task
		status int
		said   string
	}{
		{"a value with its undo", task("charge-1", `{"invoice": {"id": "7", "amount": 300}}`), 200, `{"value":{"Ok":"ch-7:o/k/1:0:charge#1"},"undo":{"id":"ch-7"}}`},
		{"an Err", task("charge-1", `{"invoice": {"id": "8", "amount": 3000}}`), 200, `{"value":{"Err":"declined"}}`},
		{"a retry", task("busy-1", `{}`), 200, `{"retry":{"reason":"the gateway is down","after":4000}}`},
		{"later", task("long-1", `{}`), 202, ``},
		{"a panic is a failure", task("broken-1", `{}`), 200, `index out of range`},
		{"a shape it does not know", task("refund-1", `{}`), 200, `{"unknown_shape":"refund-1"}`},
	} {
		status, said := post(t, q, "POST", c.task)
		if status != c.status || !strings.Contains(said, c.said) {
			t.Errorf("%s: expected %d %s, got %d %s", c.name, c.status, c.said, status, said)
		}
	}

	if status, said := post(t, q, "GET", flow.Task{}); status != 200 || said != `{"shapes":["broken-1","busy-1","charge-1","long-1"],"once":[],"started":[]}` {
		t.Errorf("the manifest: %d %s", status, said)
	}
	other := flow.NewQueue("billing", "another")
	other.Handle("charge-1", func(context.Context, *flow.Task) flow.Answer { return flow.Answer{} })
	if status, _ := post(t, other, "POST", task("charge-1", `{}`)); status != http.StatusUnauthorized {
		t.Errorf("signed with another secret: expected 401, got %d", status)
	}
}

func TestAResult(t *testing.T) {
	ok, _ := json.Marshal(flow.Ok[[]int, string]([]int{1, 2}))
	err, _ := json.Marshal(flow.Err[flow.Void]("no"))
	void, _ := json.Marshal(flow.Ok[flow.Void, string](flow.Void{}))
	if string(ok) != `{"Ok":[1,2]}` || string(err) != `{"Err":"no"}` || string(void) != `{"Ok":null}` {
		t.Errorf("written: %s %s %s", ok, err, void)
	}
	decode := flow.DecodeResult(flow.Decode[[]int], flow.Decode[string])
	r, e := decode(json.RawMessage(`{"Ok": [3]}`))
	if v, _, isOk := r.Get(); e != nil || !isOk || len(v) != 1 || v[0] != 3 {
		t.Errorf("read: %v %v", r, e)
	}
	if _, e := decode(json.RawMessage(`{"Maybe": 1}`)); e == nil {
		t.Errorf("a Result that is neither")
	}
	opt, e := flow.DecodeOption(flow.Decode[int])(json.RawMessage(`null`))
	if opt != nil || e != nil {
		t.Errorf("null is nil: %v %v", opt, e)
	}
}
