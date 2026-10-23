package flowd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Trying a flow in the console: an instance of a draft, run by the daemon
// on a store in memory and a clock that moves only when the person moves
// it, as flow.test does in a test. They start it with its fields, move
// its clock, send it messages, cancel it, and say what a step returns in
// its place; nothing is deployed, nothing is written in the store. A step
// they do not answer for runs as it is, for real.

const (
	sandboxIdle = 30 * time.Minute
	sandboxMax  = 32
	sandboxID   = "try"
)

type sandbox struct {
	mu       sync.Mutex
	id       string
	owner    string
	program  string
	flow     string
	schema   *Schema
	store    *flowrt.Memory
	rt       *flowrt.Runtime
	started  int64 // the clock when it started, to say how far it moved
	now      int64
	replaced map[string]json.RawMessage
	calls    map[string]int
	output   []Line
	said     []string // what happened, a line each: started, moved, sent
	touched  time.Time
}

// SandboxView is a trial as the console shows it.
type SandboxView struct {
	ID       string                     `json:"id"`
	Program  string                     `json:"program"`
	Flow     string                     `json:"flow"`
	Clock    string                     `json:"clock"`   // the trial's time
	Elapsed  int64                      `json:"elapsed"` // how far its clock moved, in milliseconds
	Instance Detail                     `json:"instance"`
	Calls    map[string]int             `json:"calls"`
	Replaced map[string]json.RawMessage `json:"replaced"`
	Said     []string                   `json:"said"`
}

type sandboxes struct {
	mu  sync.Mutex
	all map[string]*sandbox
}

func (s *sandboxes) put(b *sandbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.all == nil {
		s.all = map[string]*sandbox{}
	}
	for id, other := range s.all {
		if time.Since(other.touched) > sandboxIdle {
			delete(s.all, id)
		}
	}
	if len(s.all) >= sandboxMax {
		return fmt.Errorf("%d trials are open already: close one", sandboxMax)
	}
	s.all[b.id] = b
	return nil
}

// get is a trial of the person asking: theirs, or any for the token.
func (s *sandboxes) get(id string, p Principal) *sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.all[id]
	if b == nil || (!p.Token && b.owner != p.User) {
		return nil
	}
	b.touched = time.Now()
	return b
}

func (s *sandboxes) remove(id string, p Principal) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.all[id]
	if b == nil || (!p.Token && b.owner != p.User) {
		return false
	}
	delete(s.all, id)
	return true
}

// lines keeps what the trial's instance prints, line by line.
type lines struct{ b *sandbox }

func (l lines) Write(p []byte) (int, error) {
	for _, text := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if len(l.b.output) < 2000 {
			l.b.output = append(l.b.output, Line{At: time.UnixMilli(l.b.now).UTC().Format(time.RFC3339Nano), ID: sandboxID, Text: text})
		}
	}
	return len(p), nil
}

// answerFor makes a step answer what a person said, in its place.
func (b *sandbox) answerFor(step string) {
	b.rt.Replace[step] = func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error) {
		v, err := m.ValueOfData(b.replaced[step])
		return v, nil, err
	}
}

// settle runs what is due until nothing is.
func (b *sandbox) settle() error {
	for rounds := 0; rounds < 10000; rounds++ {
		n, err := b.rt.RunDue()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return errors.New("the trial's instance did not settle: it runs without waiting")
}

func (b *sandbox) say(format string, args ...any) {
	b.said = append(b.said, fmt.Sprintf("+%s  ", span(b.now-b.started))+fmt.Sprintf(format, args...))
}

// span is a length of time as the trial's log says it: 0s, 90s, 2h30m.
func span(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).String()
}

func (d *Daemon) sandboxView(b *sandbox) SandboxView {
	inst, _, _ := b.store.Get(sandboxID)
	view := SandboxView{
		ID: b.id, Program: b.program, Flow: b.flow,
		Clock: time.UnixMilli(b.now).UTC().Format(time.RFC3339Nano), Elapsed: b.now - b.started,
		Calls: b.calls, Replaced: b.replaced, Said: b.said,
	}
	if inst != nil {
		view.Instance = d.detailOf(b.rt, inst)
	}
	view.Instance.Output = append([]Line{}, b.output...)
	return view
}

// try starts a trial of a draft: POST /api/workspace/{program}/try,
// {"flow", "fields", "replace": {"step": value}, "files"}.
func (d *Daemon) try(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name := r.PathValue("program")
	if err := checkProgramName(name); err != nil {
		fail(w, http.StatusBadRequest, "program", err.Error())
		return
	}
	var asked struct {
		draftAsked
		Flow    string                     `json:"flow"`
		Fields  json.RawMessage            `json:"fields"`
		Replace map[string]json.RawMessage `json:"replace"`
	}
	files, err := d.filesInto(name, r, &asked)
	if err != nil {
		fail(w, http.StatusBadRequest, "body", err.Error())
		return
	}
	result, pkg, checked, err := d.compileDraft(name, files)
	if err != nil {
		fail(w, http.StatusInternalServerError, "compile", err.Error())
		return
	}
	if pkg == nil {
		answer(w, http.StatusUnprocessableEntity, checked)
		return
	}
	if asked.Flow == "" && len(pkg.Flows) == 1 {
		asked.Flow = pkg.Flows[0]
	}
	if named := flowNamed(pkg.Flows, asked.Flow); named != "" {
		asked.Flow = named
	}
	known := false
	for _, f := range pkg.Flows {
		known = known || f == asked.Flow
	}
	if !known {
		fail(w, http.StatusBadRequest, "flow", fmt.Sprintf("%s has no flow %q; it has %s", name, asked.Flow, strings.Join(pkg.Flows, ", ")))
		return
	}
	fields := asked.Fields
	if len(fields) == 0 || string(fields) == "null" {
		fields = json.RawMessage(`{}`)
	}
	if fields, err = pkg.Schema.Conform(asked.Flow, fields); err != nil {
		fail(w, http.StatusBadRequest, "fields", err.Error())
		return
	}

	now := time.Now().UnixMilli()
	b := &sandbox{
		id: NewToken()[:16], owner: principalOf(r).User, program: name, flow: asked.Flow, schema: pkg.Schema,
		store: flowrt.NewMemory(), started: now, now: now,
		replaced: map[string]json.RawMessage{}, calls: map[string]int{}, touched: time.Now(),
	}
	b.rt = flowrt.New(result.Chunk, b.store, lines{b})
	b.rt.Now = func() int64 { return b.now }
	b.rt.Ran = func(step, key string, once bool) { b.calls[step]++ }
	b.rt.Replace = map[string]func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error){}
	for step, value := range asked.Replace {
		if !json.Valid(value) {
			fail(w, http.StatusBadRequest, "replace", "what "+step+" returns in its place is JSON")
			return
		}
		b.replaced[step] = value
		b.answerFor(step)
	}
	if err := d.sandboxes.put(b); err != nil {
		fail(w, http.StatusTooManyRequests, "trials", err.Error())
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.rt.Start(sandboxID, asked.Flow, fields); err != nil {
		d.sandboxes.remove(b.id, Principal{Token: true})
		fail(w, http.StatusBadRequest, "start", err.Error())
		return
	}
	b.say("started %s", asked.Flow)
	if err := b.settle(); err != nil {
		b.say("%v", err)
	}
	answer(w, http.StatusCreated, d.sandboxView(b))
}

// filesInto reads a request on a draft into asked, and gives the files of
// the program with those the editor has.
func (d *Daemon) filesInto(name string, r *http.Request, asked any) (map[string]string, error) {
	var body json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16*maxFile)).Decode(&body); err != nil {
		return nil, errors.New("the body is JSON")
	}
	if err := json.Unmarshal(body, asked); err != nil {
		return nil, err
	}
	var given draftAsked
	_ = json.Unmarshal(body, &given)
	files := map[string]string{}
	opened, err := d.open(name)
	if err != nil {
		return nil, err
	}
	if opened != nil {
		for _, f := range opened.Files {
			files[f.File] = f.Text
		}
	}
	for f, text := range given.Files {
		if err := checkFileName(f); err != nil {
			return nil, err
		}
		files[f] = text
	}
	return files, nil
}

func (d *Daemon) trial(w http.ResponseWriter, r *http.Request) (*sandbox, bool) {
	b := d.sandboxes.get(r.PathValue("id"), principalOf(r))
	if b == nil {
		fail(w, http.StatusNotFound, "trial", "there is no trial "+r.PathValue("id")+": it may have been closed, or left an hour")
		return nil, false
	}
	return b, true
}

func (d *Daemon) showTrial(w http.ResponseWriter, r *http.Request) {
	b, ok := d.trial(w, r)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	answer(w, http.StatusOK, d.sandboxView(b))
}

func (d *Daemon) closeTrial(w http.ResponseWriter, r *http.Request) {
	if !d.sandboxes.remove(r.PathValue("id"), principalOf(r)) {
		fail(w, http.StatusNotFound, "trial", "there is no trial "+r.PathValue("id"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// actTrial is what a person does to a trial: POST
// /api/sandboxes/{id}/{act}, act one of advance {"ms"}, send {"inbox",
// "message"}, cancel {"reason"}, replace {"step", "value"} (null for the
// step to run as it is again).
func (d *Daemon) actTrial(w http.ResponseWriter, r *http.Request) {
	b, ok := d.trial(w, r)
	if !ok {
		return
	}
	var asked struct {
		MS      int64           `json:"ms"`
		Inbox   string          `json:"inbox"`
		Message json.RawMessage `json:"message"`
		Reason  string          `json:"reason"`
		Step    string          `json:"step"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&asked); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	switch act := r.PathValue("act"); act {
	case "advance":
		if asked.MS <= 0 {
			fail(w, http.StatusBadRequest, "advance", "a trial's clock goes forward, by some milliseconds")
			return
		}
		b.say("the clock moved %s", span(asked.MS))
		if err := flowrt.Pass(b.store, &b.now, asked.MS, b.settle); err != nil {
			b.say("%v", err)
		}
	case "send":
		message := asked.Message
		if message, err = b.schema.ConformMessage(b.flow, asked.Inbox, message); err != nil {
			fail(w, http.StatusBadRequest, "message", err.Error())
			return
		}
		if _, err = b.rt.Send(sandboxID, asked.Inbox, "", message); err != nil {
			fail(w, http.StatusConflict, "send", err.Error())
			return
		}
		b.say("sent %s on %s", message, asked.Inbox)
	case "cancel":
		reason := asked.Reason
		if reason == "" {
			reason = "cancelled in a trial"
		}
		if err = b.rt.Cancel(sandboxID, reason); err != nil {
			fail(w, http.StatusConflict, "cancel", err.Error())
			return
		}
		b.say("cancelled: %s", reason)
	case "replace":
		if asked.Step == "" {
			fail(w, http.StatusBadRequest, "replace", "a step is named: {\"step\": \"charge\", \"value\": ...}")
			return
		}
		if len(asked.Value) == 0 || string(asked.Value) == "null" {
			delete(b.replaced, asked.Step)
			delete(b.rt.Replace, asked.Step)
			b.say("%s runs as it is", asked.Step)
		} else {
			if !json.Valid(asked.Value) {
				fail(w, http.StatusBadRequest, "replace", "what the step returns is JSON")
				return
			}
			b.replaced[asked.Step] = asked.Value
			b.answerFor(asked.Step)
			b.say("%s returns %s", asked.Step, asked.Value)
		}
	default:
		fail(w, http.StatusNotFound, "act", "a trial is moved on (advance), sent to (send), cancelled (cancel), or a step answered for (replace), not "+act)
		return
	}
	if err := b.settle(); err != nil {
		b.say("%v", err)
	}
	answer(w, http.StatusOK, d.sandboxView(b))
}

// listTrials are the trials of the person asking.
func (d *Daemon) listTrials(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	d.sandboxes.mu.Lock()
	type item struct {
		ID      string `json:"id"`
		Program string `json:"program"`
		Flow    string `json:"flow"`
	}
	out := []item{}
	for _, b := range d.sandboxes.all {
		if p.Token || b.owner == p.User {
			out = append(out, item{b.id, b.program, b.flow})
		}
	}
	d.sandboxes.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	answer(w, http.StatusOK, out)
}
