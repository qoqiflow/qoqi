package flowrt

// The queues that carry out steps of the application: the daemon sends
// each such step to its queue's address, signed, and reads what comes
// back (see docs/flow-app-steps.md).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/vm"
	"github.com/qoqiflow/qoqi/pkg/flow"
)

// Queue is where a queue is served, the secret its requests are signed
// with, and how many of its steps may be under way at once (8 when not
// said).
type Queue struct {
	URL         string
	Secret      string
	Concurrency int
}

// What a queue that fails is given, in a row, before it is paused.
const pauseAfter = 5

// Keep is how long the daemon keeps an ended instance, as a delivery
// tells the application: what it keeps under a key need be kept no
// longer.
const Keep = 30 * 24 * time.Hour

// HTTPQueues sends steps to queues over HTTP.
type HTTPQueues struct {
	Queues map[string]Queue
	Client *http.Client

	// Now is the clock, in milliseconds since 1970
	Now func() int64

	// Backoff is the first wait before a queue that failed is sent to
	// again, doubled at each failure in a row up to Longest
	Backoff, Longest time.Duration

	// ManifestAge is how long a queue's manifest is taken as it was
	// read: a step of a shape it does not list waits for the next
	ManifestAge time.Duration

	mu        sync.Mutex
	health    map[string]*health
	manifests map[string]*manifest
}

// manifest is what a queue said of itself, when it was read: the shapes
// it knows, those it keeps under once and under started. listed is false
// for a queue that said nothing of its shapes, an application without
// the library, which is sent every step.
type manifest struct {
	listed                bool
	shapes, once, started map[string]bool
	read                  int64
}

// health is how a queue has been answering: the failures in a row, when
// it is next sent to, and whether it is paused, sent nothing but a GET
// now and then until one is answered.
type health struct {
	failures int
	next     int64
	paused   bool
	why      string
}

// NewHTTPQueues sends to the queues given, on the clock of the runtime.
func NewHTTPQueues(queues map[string]Queue) *HTTPQueues {
	return &HTTPQueues{
		Queues: queues, Client: &http.Client{},
		Now:     func() int64 { return time.Now().UnixMilli() },
		Backoff: time.Second, Longest: time.Minute, ManifestAge: time.Minute,
	}
}

// Knows says whether a queue knows a step's shape, as its manifest says,
// read again once it is old: a step it does not know waits, as the
// queue's own wait, until an application that answers it is deployed.
func (q *HTTPQueues) Knows(name, shape, step string) (bool, string) {
	m := q.manifestOf(name, false)
	if m == nil || !m.listed || m.shapes[shape] {
		return true, ""
	}
	return false, fmt.Sprintf("the queue %s does not know the shape %s of %s: deploy the application that answers it", name, shape, step)
}

// manifestOf is a queue's manifest, read if it is not, or old, or again.
func (q *HTTPQueues) manifestOf(name string, again bool) *manifest {
	q.mu.Lock()
	m := q.manifests[name]
	q.mu.Unlock()
	if m != nil && !again && time.Duration(q.Now()-m.read)*time.Millisecond < q.ManifestAge {
		return m
	}
	if _, read := q.probe(q.Queues[name], name); read != nil {
		q.mu.Lock()
		if q.manifests == nil {
			q.manifests = map[string]*manifest{}
		}
		q.manifests[name] = read
		q.mu.Unlock()
		return read
	}
	return m
}

// Kept says how a queue keeps the answers of a shape, as its manifest
// says: under once, safe to send again; under started, to be probed.
func (q *HTTPQueues) Kept(name, shape string) (once, started bool) {
	m := q.manifestOf(name, false)
	if m == nil {
		return false, false
	}
	return m.once[shape], m.started[shape]
}

// Concurrency is how many steps of a queue may be under way at once.
func (q *HTTPQueues) Concurrency(queue string) int {
	if n := q.Queues[queue].Concurrency; n > 0 {
		return n
	}
	return 8
}

// Paused says whether a queue is paused, and why.
func (q *HTTPQueues) Paused(queue string) (bool, string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	h := q.health[queue]
	if h == nil || !h.paused {
		return false, ""
	}
	return true, h.why
}

// Ready says how long a step of a queue waits before it may leave: none
// for a queue that answers well; until its next try for one that failed;
// for one paused, until a GET on its address is answered, which Ready
// asks itself once that wait is over.
func (q *HTTPQueues) Ready(name string) (wait int64, why string) {
	q.mu.Lock()
	h := q.health[name]
	if h == nil || q.Now() >= h.next && !h.paused {
		q.mu.Unlock()
		return 0, ""
	}
	if now := q.Now(); now < h.next {
		wait, why = h.next-now, h.why
		q.mu.Unlock()
		return wait, why
	}
	q.mu.Unlock()

	// Paused, and its wait over: asked whether it is back
	queue := q.Queues[name]
	if ok, read := q.probe(queue, name); ok {
		if read != nil {
			q.mu.Lock()
			if q.manifests == nil {
				q.manifests = map[string]*manifest{}
			}
			q.manifests[name] = read
			q.mu.Unlock()
		}
		q.answered(name)
		return 0, ""
	}
	// Still down: it stays paused for what paused it
	q.mu.Lock()
	why = h.why
	q.mu.Unlock()
	q.failed(name, why, false)
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.health[name].next - q.Now(), why
}

// probe asks a queue of itself: a signed GET on its address, answered
// with a 2xx, and with its manifest by the library.
func (q *HTTPQueues) probe(queue Queue, name string) (bool, *manifest) {
	address, err := url.Parse(queue.URL)
	if err != nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return false, nil
	}
	req.Header.Set(flow.SignatureHeader, q.sign(queue, http.MethodGet, address, name, nil))
	resp, err := q.Client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var said struct {
		Shapes  *[]string `json:"shapes"`
		Once    []string  `json:"once"`
		Started []string  `json:"started"`
	}
	m := &manifest{shapes: map[string]bool{}, once: map[string]bool{}, started: map[string]bool{}, read: q.Now()}
	if json.Unmarshal(data, &said) == nil && said.Shapes != nil {
		m.listed = true
		for _, s := range *said.Shapes {
			m.shapes[s] = true
		}
		for _, s := range said.Once {
			m.once[s] = true
		}
		for _, s := range said.Started {
			m.started[s] = true
		}
	}
	return true, m
}

// answered notes that a queue answered well: it is well again.
func (q *HTTPQueues) answered(name string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.health, name)
}

// failed notes that a queue failed: the next try waits longer, up to
// Longest, and the queue is paused after pauseAfter failures in a row,
// or at once when pause says so (its secret is wrong on a side).
func (q *HTTPQueues) failed(name, why string, pause bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.health == nil {
		q.health = map[string]*health{}
	}
	h := q.health[name]
	if h == nil {
		h = &health{}
		q.health[name] = h
	}
	h.failures++
	wait := q.Backoff
	for i := 1; i < h.failures && wait < q.Longest; i++ {
		wait *= 2
	}
	if wait > q.Longest {
		wait = q.Longest
	}
	h.next, h.why = q.Now()+wait.Milliseconds(), why
	if pause || h.failures >= pauseAfter {
		h.paused = true
	}
}

// pathOf is the path of an address as a signature covers it.
func pathOf(address *url.URL) string {
	if path := address.EscapedPath(); path != "" {
		return path
	}
	return "/"
}

// sign is the signature of a request to a queue, as the queue's
// library checks it (flow.Verify).
func (q *HTTPQueues) sign(queue Queue, method string, address *url.URL, name string, body []byte) string {
	return flow.Sign(queue.Secret, time.UnixMilli(q.Now()), method, pathOf(address), name, body)
}

// Deliver sends a step to its queue and waits for its answer, until the
// step's time is up. What never arrived is held, to be sent again as the
// same attempt once the queue's wait is over; what may have arrived is
// said so. Either is a failure of the queue, which backs off.
func (q *HTTPQueues) Deliver(d vm.Delivery) vm.Answer {
	queue, ok := q.Queues[d.Queue]
	if !ok {
		return vm.Answer{Outcome: vm.Halt, Reason: fmt.Sprintf("no queue %s is served: qoqi.json declares the queues, and the environment says where", d.Queue)}
	}
	address, err := url.Parse(queue.URL)
	if err != nil || address.Host == "" {
		return vm.Answer{Outcome: vm.Halt, Reason: fmt.Sprintf("the queue %s is served at %q, which is not an address", d.Queue, queue.URL)}
	}
	if d.Token == "" {
		d.Token, d.Until = drawToken(), q.Now()+d.Within
	}
	until := d.Until
	body, err := json.Marshal(flow.Task{
		Key: d.Key, Attempt: d.Attempt, Token: d.Token, Until: until,
		Keep: Keep.Milliseconds(), Step: d.Step, Shape: d.Shape, Args: d.Args, Reclaim: d.Reclaim,
	})
	if err != nil {
		return vm.Answer{Outcome: vm.Halt, Reason: err.Error()}
	}

	if wait, why := q.Ready(d.Queue); wait > 0 {
		return vm.Answer{Outcome: vm.Held, Reason: why, After: wait}
	}
	left := time.Duration(until-q.Now()) * time.Millisecond
	if left <= 0 {
		left = time.Millisecond
	}
	a, arrived, status := q.send(queue, address, d.Queue, body, left)
	switch {
	case !arrived:
		q.failed(d.Queue, a.Reason, false)
		wait, _ := q.Ready(d.Queue)
		return vm.Answer{Outcome: vm.Held, Reason: a.Reason, After: wait}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		q.failed(d.Queue, a.Reason+": the secret is wrong on a side, which a person fixes", true)
	case a.Outcome == vm.Uncertain:
		q.failed(d.Queue, a.Reason, false)
	default:
		q.answered(d.Queue)
	}
	if a.Outcome == vm.Uncertain {
		// Sent again once the queue's wait is over
		if wait, _ := q.Ready(d.Queue); wait > a.After {
			a.After = wait
		}
	}
	return a
}

// send makes one request, and says whether it may have arrived, and the
// status it was answered with: what did not arrive is sent again as it
// is.
func (q *HTTPQueues) send(queue Queue, address *url.URL, name string, body []byte, left time.Duration) (vm.Answer, bool, int) {
	ctx, cancel := context.WithTimeout(context.Background(), left)
	defer cancel()
	wrote := false
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { wrote = true },
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), bytes.NewReader(body))
	if err != nil {
		return vm.Answer{Outcome: vm.Halt, Reason: err.Error()}, true, 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(flow.SignatureHeader, q.sign(queue, http.MethodPost, address, name, body))
	resp, err := q.Client.Do(req)
	if err != nil {
		if !wrote {
			// Refused, not resolved, not connected in time: it never left
			return vm.Answer{Reason: fmt.Sprintf("the queue %s could not be reached: %v", name, err)}, false, 0
		}
		return q.uncertain("the connection was cut after the step was sent: %v", err), true, 0
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return q.uncertain("the answer was cut: %v", err), true, resp.StatusCode
	}

	if resp.StatusCode == http.StatusAccepted {
		return vm.Answer{Outcome: vm.Working}, true, resp.StatusCode
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return q.uncertain("the queue %s answered %d: %s", name, resp.StatusCode, snippet(data)), true, resp.StatusCode
	}
	var a flow.Answer
	if json.Unmarshal(data, &a) == nil && a.UnknownShape != "" {
		// The application has no handler for it, and did nothing
		return vm.Answer{Reason: fmt.Sprintf("the queue %s has no handler for this step's shape", name)}, false, resp.StatusCode
	}
	if got, ok := readAnswer(data); ok {
		if got.Outcome == vm.Working {
			// An attempt before acts under the key still: asked again,
			// after a while, spending nothing
			return vm.Answer{Outcome: vm.Held, Reason: "an attempt before is under way", After: q.Backoff.Milliseconds()}, true, resp.StatusCode
		}
		return got, true, resp.StatusCode
	}
	return q.uncertain("the queue %s answered none of value, retry, failed: %s", name, snippet(data)), true, resp.StatusCode
}

// readAnswer reads what an application answers a step, on the response
// to it or later: ok is false for what is none of the answers.
func readAnswer(data []byte) (vm.Answer, bool) {
	var a flow.Answer
	if json.Unmarshal(data, &a) != nil {
		return vm.Answer{}, false
	}
	switch {
	case a.Failed != nil:
		return vm.Answer{Outcome: vm.Failed, Reason: *a.Failed}, true
	case a.Retry != nil:
		after := a.Retry.After
		if after <= 0 {
			after = 1000
		}
		return vm.Answer{Outcome: vm.Again, Reason: a.Retry.Reason, After: after}, true
	case a.Unknown:
		return vm.Answer{Outcome: vm.Unknown, Reason: "the application says it acted, and cannot say how"}, true
	case a.Value != nil:
		return vm.Answer{Outcome: vm.Answered, Value: a.Value, Undo: a.Undo}, true
	case a.Working:
		return vm.Answer{Outcome: vm.Working}, true
	}
	return vm.Answer{}, false
}

// drawToken is a token for an attempt: unguessable, it answers for it.
func drawToken() string {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		panic(err)
	}
	return hex.EncodeToString(token)
}

// attempting is the queues as the program of an instance reaches them:
// each attempt written in the store before it leaves, with its token
// and when it is given up, and moved on in its life as it comes back,
// for an answer that comes later to find it. A write the store refuses
// is the fault of the run, as any is.
type attempting struct {
	r    *Runtime
	inst string

	mu    sync.Mutex
	fault error
}

func (a *attempting) Deliver(d vm.Delivery) vm.Answer {
	// A step that keeps failing alone, its queue answering the others,
	// is a poison: it stops its instance, for a person to look at
	if n, said := a.uncertainInARow(d.Key); n >= a.r.Poison {
		return vm.Answer{Outcome: vm.Halt, Reason: fmt.Sprintf("%s may have arrived %d times in a row, its queue answering others; the last: %s", d.Step, n, said)}
	}

	// It leaves once its queue is ready for it and has room: until then
	// it is held, and spends no attempt
	gate, gated := a.r.Queues.(interface {
		Ready(queue string) (int64, string)
		Concurrency(queue string) int
		Knows(queue, shape, step string) (bool, string)
	})
	if gated {
		if wait, why := gate.Ready(d.Queue); wait > 0 {
			return vm.Answer{Outcome: vm.Held, Reason: why, After: wait}
		}
		if known, why := gate.Knows(d.Queue, d.Shape, d.Step); !known {
			return vm.Answer{Outcome: vm.Held, Reason: why, After: 5000}
		}
	}
	// A Settled step kept under started whose attempt before may have
	// arrived: what became of it is asked first, and it is sent again
	// only once the application says it saw nothing
	if prober, ok := a.r.Queues.(interface {
		Kept(queue, shape string) (once, started bool)
		Probe(d vm.Delivery, of Attempt) vm.Answer
	}); ok && d.Settled {
		if _, started := prober.Kept(d.Queue, d.Shape); started {
			if before, ok := a.r.Store.Attempt(d.Key); ok && before.State != Answered {
				switch probed := prober.Probe(d, before); probed.Outcome {
				case vm.Again:
					// Unseen: sent again
				case vm.Answered:
					if a.r.Boundary != nil {
						probed = a.r.Boundary.Answer(d.Step, d.Undo, probed)
					}
					return probed
				default:
					return probed
				}
			}
		}
	}
	if a.r.Boundary != nil {
		args, err := a.r.Boundary.Args(d.Step, d.Args)
		if err != nil {
			return vm.Answer{Outcome: vm.Halt, Reason: fmt.Sprintf("what %s is given: %v", d.Step, err)}
		}
		d.Args = args
	}
	if before, ok := a.r.Store.Attempt(d.Key); ok && before.State == Nothing {
		d.Reclaim = before.Attempt
	}
	d.Token, d.Until = drawToken(), a.r.Now()+d.Within
	a.r.gate.Lock()
	if gated && a.r.Store.UnderWay(d.Queue, a.r.Now()) >= gate.Concurrency(d.Queue) {
		a.r.gate.Unlock()
		return vm.Answer{Outcome: vm.Held, Reason: fmt.Sprintf("the queue %s has %d steps under way, as many as it takes", d.Queue, gate.Concurrency(d.Queue)), After: 200}
	}
	err := a.r.Store.PutAttempt(Attempt{
		Key: d.Key, Attempt: d.Attempt, Instance: a.inst, Queue: d.Queue, Step: d.Step, Shape: d.Shape, Token: d.Token, Until: d.Until,
		State: Sent, Settled: d.Settled, Undo: d.Undo,
	})
	a.r.gate.Unlock()
	if err != nil {
		return a.failed(err)
	}

	answer := a.r.Queues.Deliver(d)
	if a.r.Boundary != nil {
		answer = a.r.Boundary.Answer(d.Step, d.Undo, answer)
	}
	if answer.Outcome == vm.Uncertain && d.Settled {
		// A Settled step that may have arrived is not sent again blind:
		// under once it may be, under started it is asked of first
		if kept, ok := a.r.Queues.(interface {
			Kept(queue, shape string) (once, started bool)
		}); ok {
			switch once, started := kept.Kept(d.Queue, d.Shape); {
			case once:
				answer = vm.Answer{Outcome: vm.Again, Reason: answer.Reason, After: answer.After}
			case started:
				answer = vm.Answer{Outcome: vm.Held, Reason: "what became of it is asked: " + answer.Reason, After: answer.After}
			}
		}
	}
	to, said := GivenUp, ""
	switch answer.Outcome {
	case vm.Answered:
		to = Answered
	case vm.Working:
		to, answer.Until = Working, d.Until
	case vm.Uncertain:
		said = answer.Reason
	}
	now, err := a.r.Store.Mark(d.Key, d.Attempt, to, said)
	if err != nil {
		return a.failed(err)
	}
	if to == Working && now.State == Answered {
		// Answered on a request of its own before the 202 came back
		if got, ok := readAnswer(now.Answer); ok {
			// Read against its type already, as it was kept
			return got
		}
	}
	return answer
}

// uncertainInARow is how many of the last attempts under a key were
// given up as ones that may have arrived, and what the queue said last.
func (a *attempting) uncertainInARow(key string) (int, string) {
	attempts := a.r.Store.Attempts(key)
	n, said := 0, ""
	for i := len(attempts) - 1; i >= 0 && attempts[i].State == GivenUp && attempts[i].Said != ""; i-- {
		if n == 0 {
			said = attempts[i].Said
		}
		n++
	}
	return n, said
}

func (a *attempting) failed(err error) vm.Answer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fault == nil {
		a.fault = err
	}
	return vm.Answer{Outcome: vm.Halt, Reason: err.Error()}
}

// err is the write the store refused, if one was.
func (a *attempting) err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.fault
}

// uncertain is a step that may have arrived: a plain one is sent again
// after the first wait of a backoff.
func (q *HTTPQueues) uncertain(format string, args ...any) vm.Answer {
	return vm.Answer{Outcome: vm.Uncertain, Reason: fmt.Sprintf(format, args...), After: q.Backoff.Milliseconds()}
}

// snippet is the start of a body, for a message.
func snippet(data []byte) string {
	text := strings.TrimSpace(string(data))
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	if text == "" {
		return "nothing"
	}
	return strconv.Quote(text)
}

// ErrNotAnAnswer is a later answer that is none of those a step has.
var ErrNotAnAnswer = errors.New(`an answer is {"value": ...}, with "undo" for a step that names one; {"retry": {"reason": ..., "after": ...}}; {"failed": ...}; {"unknown": true}; or {"working": true}, under way`)

// Answer takes what an application answers a step later, on a request
// of its own, POST /api/attempts: the key, the attempt and its token,
// and the answer, as on the response to the step. One that says only
// {"working": true} keeps nothing: it is told whether the step is still
// under way, ErrLate once it was given up.
func (r *Runtime) Answer(body []byte) (already bool, err error) {
	r.swap.RLock()
	defer r.swap.RUnlock()

	var said struct {
		Key     string `json:"key"`
		Attempt int    `json:"attempt"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(body, &said); err != nil || said.Key == "" || said.Token == "" {
		return false, fmt.Errorf("an answer names its attempt: {\"key\": ..., \"attempt\": ..., \"token\": ..., and the answer}")
	}
	got, ok := readAnswer(body)
	if !ok {
		return false, ErrNotAnAnswer
	}
	// Read against the step's type, in the form of the boundary: what
	// does not fit is a panic of the step, and the application is told
	var misfit error
	if r.Boundary != nil && got.Outcome == vm.Answered {
		for _, a := range r.Store.Attempts(said.Key) {
			if a.Attempt == said.Attempt && a.Token == said.Token {
				read := r.Boundary.Answer(a.Step, a.Undo, got)
				if read.Outcome == vm.Failed {
					misfit = fmt.Errorf("%w: %s", ErrMisfit, read.Reason)
				}
				body = answerBody(read)
			}
		}
	}
	already, err = r.Store.AnswerAttempt(said.Key, said.Attempt, said.Token, body, got.Outcome == vm.Working, r.Now())
	if err == nil && !already && misfit != nil {
		return false, misfit
	}
	return already, err
}

// ErrMisfit is an answer that does not fit the type of its step: kept as
// a panic of the step.
var ErrMisfit = errors.New("the answer does not fit its step")

// answerBody is an answer as readAnswer reads it.
func answerBody(a vm.Answer) []byte {
	var out map[string]any
	switch a.Outcome {
	case vm.Answered:
		out = map[string]any{"value": a.Value}
		if a.Undo != nil {
			out["undo"] = a.Undo
		}
	case vm.Failed:
		out = map[string]any{"failed": a.Reason}
	case vm.Again:
		out = map[string]any{"retry": map[string]any{"reason": a.Reason, "after": a.After}}
	case vm.Unknown:
		out = map[string]any{"unknown": true}
	default:
		out = map[string]any{"working": true}
	}
	data, _ := json.Marshal(out)
	return data
}

// Boundary is the form data takes at the boundary with applications, as
// the program says it: a step's arguments written in it, its answer read
// from it against the type the step returns.
type Boundary interface {
	Args(step string, args json.RawMessage) (json.RawMessage, error)
	Answer(step, undo string, a vm.Answer) vm.Answer
}

// Probe asks a queue what became of an attempt kept under started that
// may have arrived: its answer; unseen, nothing done, to be sent again;
// under way, or a probe that failed, to be asked again after a while; or
// unknown, a process that died as it acted.
func (q *HTTPQueues) Probe(d vm.Delivery, of Attempt) vm.Answer {
	queue, ok := q.Queues[d.Queue]
	address, err := url.Parse(queue.URL)
	if !ok || err != nil {
		return vm.Answer{Outcome: vm.Halt, Reason: fmt.Sprintf("no queue %s is served", d.Queue)}
	}
	body, err := json.Marshal(flow.Task{Key: of.Key, Attempt: of.Attempt, Token: of.Token, Until: of.Until, Step: d.Step, Shape: d.Shape, Probe: true})
	if err != nil {
		return vm.Answer{Outcome: vm.Halt, Reason: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), bytes.NewReader(body))
	if err != nil {
		return vm.Answer{Outcome: vm.Halt, Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(flow.SignatureHeader, q.sign(queue, http.MethodPost, address, d.Queue, body))
	again := vm.Answer{Outcome: vm.Held, Reason: "what became of " + d.Step + " is asked again", After: q.Backoff.Milliseconds()}
	resp, err := q.Client.Do(req)
	if err != nil {
		return again
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return again
	}
	var said flow.Answer
	if json.Unmarshal(data, &said) != nil {
		return again
	}
	switch {
	case said.Unseen:
		return vm.Answer{Outcome: vm.Again, Reason: "the application saw nothing of it", After: 0}
	case said.Unknown:
		return vm.Answer{Outcome: vm.Unknown, Reason: "a process died as it acted"}
	case said.Value != nil:
		return vm.Answer{Outcome: vm.Answered, Value: said.Value, Undo: said.Undo}
	}
	return again
}
