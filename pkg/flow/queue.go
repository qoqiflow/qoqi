package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// StepFunc carries out a step: what it answers is the step's answer.
type StepFunc func(ctx context.Context, task *Task) Answer

// Queue serves the steps of one queue: an http.Handler for the address
// qoqi.json gives the queue. It checks each request's signature, reads
// the step, and answers it with the handler of its shape; a GET is
// answered the manifest, which says which shapes it knows.
type Queue struct {
	Name    string
	Secrets []string // the queue's secret; two while one is changed

	// Now is the clock signatures are checked against
	Now func() time.Time

	mu      sync.RWMutex
	steps   map[string]StepFunc
	once    map[string]*Keeper
	started map[string]*Keeper
	// What keeps the steps not named, every step of the queue when only
	// one is said: once or started
	onceAll, startedAll *Keeper
}

// KeepOnce keeps the answers of the queue's steps under once: their
// effect is in the application's database, written with their answer in
// one transaction, which a step reaches with TxOf. Without shapes, every
// step of the queue; with them, those.
func (q *Queue) KeepOnce(k *Keeper, shapes ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(shapes) == 0 {
		q.onceAll = k
	}
	for _, shape := range shapes {
		q.once[shape] = k
		delete(q.started, shape)
	}
}

// KeepStarted keeps the answers of the queue's steps under started: their
// effect is elsewhere, a claim written before they act. Without shapes,
// every step of the queue; with them, those.
func (q *Queue) KeepStarted(k *Keeper, shapes ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(shapes) == 0 {
		q.startedAll = k
	}
	for _, shape := range shapes {
		q.started[shape] = k
		delete(q.once, shape)
	}
}

// keeping is how a shape's answers are kept: under once, under started,
// or neither. Call with mu held.
func (q *Queue) keeping(shape string) (once, started *Keeper) {
	if k, ok := q.once[shape]; ok {
		return k, nil
	}
	if k, ok := q.started[shape]; ok {
		return nil, k
	}
	if _, handled := q.steps[shape]; !handled {
		return nil, nil
	}
	if q.onceAll != nil {
		return q.onceAll, nil
	}
	return nil, q.startedAll
}

// kept are the shapes the queue keeps one way, sorted. Call with mu held.
func (q *Queue) kept(once bool) []string {
	out := []string{}
	for shape := range q.steps {
		o, s := q.keeping(shape)
		if (once && o != nil) || (!once && s != nil) {
			out = append(out, shape)
		}
	}
	sort.Strings(out)
	return out
}

// NewQueue makes a queue, by its name in qoqi.json, with its secret.
func NewQueue(name string, secrets ...string) *Queue {
	return &Queue{Name: name, Secrets: secrets, Now: time.Now, steps: map[string]StepFunc{}, once: map[string]*Keeper{}, started: map[string]*Keeper{}}
}

// Handle says what carries out the steps of a shape: the shape names a
// step and its signature, so a step whose signature changed has another,
// and an instance of an older version may still send the old one.
func (q *Queue) Handle(shape string, step StepFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.steps[shape] = step
}

// Shapes are the shapes the queue knows, sorted.
func (q *Queue) Shapes() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.shapes()
}

func (q *Queue) shapes() []string {
	shapes := make([]string, 0, len(q.steps))
	for shape := range q.steps {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	return shapes
}

// Manifest is what a queue says of itself: the shapes it knows.
type Manifest struct {
	Shapes  []string `json:"shapes"`
	Once    []string `json:"once"`
	Started []string `json:"started"`
}

func (q *Queue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := Verify(r.Header.Get(SignatureHeader), q.Now(), r.Method, r.URL.EscapedPath(), q.Name, body, q.Secrets...); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		q.mu.RLock()
		manifest := Manifest{Shapes: q.shapes(), Once: q.kept(true), Started: q.kept(false)}
		q.mu.RUnlock()
		write(w, http.StatusOK, manifest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "a queue takes steps, POST, and is asked of itself, GET", http.StatusMethodNotAllowed)
		return
	}
	var task Task
	if err := json.Unmarshal(body, &task); err != nil {
		http.Error(w, "a step is JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	q.mu.RLock()
	step := q.steps[task.Shape]
	once, started := q.keeping(task.Shape)
	q.mu.RUnlock()
	if task.Probe {
		keeper := started
		if keeper == nil {
			keeper = once
		}
		if keeper == nil {
			write(w, http.StatusOK, Answer{Unseen: true})
			return
		}
		write(w, http.StatusOK, keeper.probe(r.Context(), &task))
		return
	}
	if step == nil {
		write(w, http.StatusOK, Answer{UnknownShape: task.Shape})
		return
	}

	ctx, cancel := context.WithDeadline(r.Context(), task.Deadline())
	defer cancel()
	var answer Answer
	switch {
	case once != nil:
		answer = once.once(ctx, func(ctx context.Context, t *Task) Answer { return carryOut(ctx, step, t) }, &task)
	case started != nil:
		answer = started.started(ctx, func(ctx context.Context, t *Task) Answer { return carryOut(ctx, step, t) }, &task)
	default:
		answer = carryOut(ctx, step, &task)
	}
	if answer.later {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	write(w, http.StatusOK, answer)
}

// carryOut runs a step: a panic is a failure of its body.
func carryOut(ctx context.Context, step StepFunc, task *Task) (answer Answer) {
	defer func() {
		if r := recover(); r != nil {
			answer = Failure(fmt.Errorf("%s panicked: %v", task.Step, r))
		}
	}()
	return step(ctx, task)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
