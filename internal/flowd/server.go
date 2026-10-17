// Package flowd is the daemon of flows, qoqi serve: it runs the
// flows of a program against a store, wakes what is due, and answers the
// HTTP API every client goes through, the qoqi commands first (see
// docs/flow.md, "The daemon, from outside").
package flowd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/sqldb"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Daemon runs flows and answers for them: those of one program, or of
// every program of a folder, each on tables of its own.
type Daemon struct {
	Programs []Program
	Flows    []string // the flows of every program, by name
	Token    string   // what every request carries: Authorization: Bearer <token>
	Log      io.Writer

	// Schema is what the daemon knows of its programs, from their syntax:
	// the types of the fields of each flow, their steps. Without it, a
	// start's fields are taken as the program's data, "$type" and all
	Schema *Schema

	// Open gives the store of a program deployed, its tables in the
	// daemon's store: without it, the daemon serves the programs it was
	// made with, and no other. Deployments keeps what was deployed;
	// Workers and Out are what a program deployed runs with
	Open        func(name string) (flowrt.Store, error)
	Deployments flowrt.Deployments
	Workers     int
	Out         io.Writer

	deploying sync.Mutex                   // one deploy at a time
	pmu       sync.RWMutex                 // Programs, Flows and Schema, which a deploy changes
	schemas   map[string]*Schema           // by program
	sources   map[string]map[string]string // by program, its files

	hub     *hub
	wake    chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
	failure error // the runtime could not go on
	// followed is the last deploy to the store this daemon serves
	followed int64
}

// Program is a program a daemon serves: its runtime, and its flows.
type Program struct {
	Name    string
	Runtime *flowrt.Runtime
	Flows   []string
}

// New makes a daemon for the runtime of one program; the token is drawn.
func New(rt *flowrt.Runtime, flows []string) *Daemon {
	d, _ := NewOf([]Program{{Runtime: rt, Flows: flows}})
	return d
}

// NewOf makes a daemon for several programs. A flow belongs to one of
// them: two that have a flow of one name are refused.
func NewOf(programs []Program) (*Daemon, error) {
	d := &Daemon{Programs: programs, Token: NewToken(), Log: io.Discard, hub: newHub(), wake: make(chan struct{}, 1), stopped: make(chan struct{})}
	owner := map[string]string{}
	for _, p := range programs {
		d.observe(p.Runtime)
		for _, flow := range p.Flows {
			if other, taken := owner[flow]; taken {
				return nil, fmt.Errorf("%s and %s both have a flow %s: a flow belongs to one program", other, p.Name, flow)
			}
			owner[flow] = p.Name
			d.Flows = append(d.Flows, flow)
		}
	}
	return d, nil
}

// snapshot is what the daemon serves now: a deploy changes it.
func (d *Daemon) snapshot() ([]Program, []string, *Schema) {
	d.pmu.RLock()
	defer d.pmu.RUnlock()
	return append([]Program(nil), d.Programs...), append([]string(nil), d.Flows...), d.Schema
}

func (d *Daemon) programs() []Program {
	programs, _, _ := d.snapshot()
	return programs
}

func (d *Daemon) flows() []string {
	_, flows, _ := d.snapshot()
	return flows
}

func (d *Daemon) schema() *Schema {
	_, _, schema := d.snapshot()
	return schema
}

// runtimeOf is the runtime of the program a flow belongs to.
func (d *Daemon) runtimeOf(flow string) *flowrt.Runtime {
	for _, p := range d.programs() {
		for _, name := range p.Flows {
			if name == flow {
				return p.Runtime
			}
		}
	}
	return nil
}

// find is an instance, and the runtime of the program it belongs to.
func (d *Daemon) find(id string) (*flowrt.Runtime, *flowrt.Instance, bool) {
	for _, p := range d.programs() {
		if inst, ok := p.Runtime.Store.Get(id); ok {
			return p.Runtime, inst, true
		}
	}
	return nil, nil, false
}

// observe keeps what a runtime's instances print, each with the instance
// that printed it, and tells of the steps that leave as they leave.
func (d *Daemon) observe(rt *flowrt.Runtime) {
	out := rt.Out
	rt.OutOf = func(id string) io.Writer {
		if out == nil {
			return d.hub.writer(id)
		}
		return io.MultiWriter(out, d.hub.writer(id))
	}
	ran := rt.Ran
	rt.Ran = func(step, key string, once bool) {
		if ran != nil {
			ran(step, key, once)
		}
		d.hub.send(Event{Kind: "step", At: stamp(), ID: instanceOfKey(key), Step: step, Key: key, Once: once})
	}
}

// instanceOfKey is the name of the instance a key is of: a key is read
// from its right, where its parts are fixed (name/birth/pass:task:step#n)
func instanceOfKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) < 3 {
		return ""
	}
	// The birth and what follows it hold no "/" but the task's own
	for i := len(parts) - 2; i >= 1; i-- {
		if !strings.Contains(parts[i], ":") && !strings.Contains(parts[i], "#") {
			return strings.Join(parts[:i], "/")
		}
	}
	return parts[0]
}

// NewToken draws a token.
func NewToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Beside is where the files about a store are kept on this machine: next
// to a file of SQLite; for a store on a server, in the user's folder of
// Qoqi, under a name drawn from its address without its password.
func Beside(store string) string {
	if !sqldb.IsServer(store) {
		return store
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "qoqi", "stores")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, sqldb.Fingerprint(store))
}

// TokenFile is where a daemon keeps its token, beside its store,
// readable by its user alone: a daemon started again on the store has the
// same, so that the pages open on it and the programs that talk to it go
// on as they were.
func TokenFile(store string) string { return Beside(store) + ".token" }

// StoreToken is the token of the daemons of a store: the one kept next to
// it, or one drawn now and kept there.
func StoreToken(store string) (string, error) {
	data, err := os.ReadFile(TokenFile(store))
	if err == nil {
		if token := strings.TrimSpace(string(data)); len(token) >= 32 {
			return token, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token := NewToken()
	if err := os.WriteFile(TokenFile(store), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// Run wakes what is due until ctx is done: at once when told (a start),
// otherwise every tick.
func (d *Daemon) Run(ctx context.Context, tick time.Duration) error {
	defer close(d.stopped)
	timer := time.NewTicker(tick)
	defer timer.Stop()
	// A worker done with an instance may have woken another: look again
	for _, p := range d.programs() {
		p.Runtime.Finished = d.poke
	}
	defer d.drain(5 * time.Second)
	looked := time.Now()
	for {
		// What other daemons deployed, every second
		if time.Since(looked) >= time.Second {
			looked = time.Now()
			if err := d.Follow(); err != nil {
				fmt.Fprintf(d.Log, "qoqi: %v\n", err)
			}
		}
		for _, p := range d.programs() {
			_, err := p.Runtime.RunDue()
			d.hub.observe(p.Runtime.Store)
			if err != nil {
				d.mu.Lock()
				d.failure = err
				d.mu.Unlock()
				fmt.Fprintf(d.Log, "qoqi: %v\n", err)
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.wake:
		case <-timer.C:
		}
	}
}

// drain lets the workers finish the instances they run, for a while: one
// cut short is taken up from its last write all the same.
func (d *Daemon) drain(limit time.Duration) {
	end := time.Now().Add(limit)
	busy := func() int {
		n := 0
		for _, p := range d.programs() {
			n += p.Runtime.Busy()
		}
		return n
	}
	for busy() > 0 && time.Now().Before(end) {
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *Daemon) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Handler is the HTTP API. Every request carries the token; the host a
// request names has to be the daemon's own, so that a page of another
// site cannot reach it under a name of its own.
func (d *Daemon) Handler(hosts ...string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flows", d.listFlows)
	mux.HandleFunc("GET /api/instances", d.listInstances)
	mux.HandleFunc("POST /api/instances", d.start)
	mux.HandleFunc("GET /api/instances/{id}", d.show)
	mux.HandleFunc("GET /api/instances/{id}/result", d.result)
	mux.HandleFunc("POST /api/instances/{id}/cancel", d.cancel)
	mux.HandleFunc("POST /api/instances/{id}/resume", d.resume)
	mux.HandleFunc("POST /api/instances/{id}/end", d.end)
	mux.HandleFunc("POST /api/instances/{id}/answer", d.answerByHand)
	mux.HandleFunc("POST /api/instances/{id}/inboxes/{inbox}", d.send)
	mux.HandleFunc("GET /api/program", d.program)
	mux.HandleFunc("POST /api/deploy", d.deploy)
	mux.HandleFunc("POST /api/undeploy", d.undeploy)
	mux.HandleFunc("GET /api/deployments", d.deployments)
	mux.HandleFunc("GET /api/deployments/{seq}", d.deployment)
	mux.HandleFunc("GET /api/deployments/{seq}/diff", d.diff)
	mux.HandleFunc("GET /api/source", d.source)
	mux.HandleFunc("GET /api/events", d.events)
	mux.HandleFunc("GET /api/output", d.output)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(r.Host, hosts) {
			fail(w, http.StatusForbidden, "host", "this daemon answers to its own address only, not to "+r.Host)
			return
		}
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
			// The pages hold no data: what they show, they ask the API for,
			// with the token the address gave them
			servePage(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/attempts" {
			// An application's answer to a step: the attempt's token,
			// in its body, answers for it
			d.attempt(w, r)
			return
		}
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(d.Token)) != 1 {
			fail(w, http.StatusUnauthorized, "token", "every request carries the daemon's token: Authorization: Bearer <token>")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func allowedHost(host string, hosts []string) bool {
	if len(hosts) == 0 {
		return true
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	for _, allowed := range hosts {
		if host == allowed || name == allowed {
			return true
		}
	}
	return false
}

// View is an instance as the API shows it.
type View struct {
	ID      string          `json:"id"`
	Flow    string          `json:"flow"`
	Status  string          `json:"status"`
	Birth   string          `json:"birth"`
	WakeAt  string          `json:"wakeAt,omitempty"`
	Fields  json.RawMessage `json:"fields,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Stopped string          `json:"stopped,omitempty"`
	Steps   map[string]int  `json:"steps,omitempty"`
	Phase   string          `json:"phase,omitempty"`
	Undos   []vm.Undo       `json:"undos,omitempty"`
	Cancel  string          `json:"cancel,omitempty"` // why it was cancelled

	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
	Awaits   []string `json:"awaits,omitempty"` // the instances it waits for

	Version   string `json:"version,omitempty"`   // the bytecode it runs, by its fingerprint
	Replaying bool   `json:"replaying,omitempty"` // resumed, before its next stage
	Ended     string `json:"ended,omitempty"`     // why a person ended it
	Stage     string `json:"stage,omitempty"`     // the last stage it passed alone
	Pass      int    `json:"pass,omitempty"`      // how many stages it passed
}

func viewOf(inst *flowrt.Instance) View {
	v := View{ID: inst.ID, Flow: inst.Flow, Status: string(inst.Status), Birth: inst.Birth, Fields: inst.Fields, Result: inst.Result, Stopped: inst.Stopped, Steps: inst.Counts, Phase: inst.Phase, Undos: inst.Undos, Cancel: inst.Cancel,
		Parent: inst.Parent, Children: inst.Children, Awaits: inst.Awaits, Pass: inst.Pass, Version: inst.Version,
		Replaying: inst.Replaying, Ended: inst.Ended}
	if inst.AtStage != nil {
		v.Stage = inst.AtStage.Name
	}
	if inst.Status == flowrt.Waiting && inst.WakeAt != flowrt.Never {
		v.WakeAt = time.UnixMilli(inst.WakeAt).UTC().Format(time.RFC3339Nano)
	}
	return v
}

func (d *Daemon) listFlows(w http.ResponseWriter, r *http.Request) {
	type flow struct {
		Name      string         `json:"name"`
		Instances map[string]int `json:"instances"`
	}
	var out []flow
	for _, name := range d.flows() {
		counts := map[string]int{}
		for _, inst := range d.runtimeOf(name).Store.List(name, "") {
			counts[string(inst.Status)]++
		}
		out = append(out, flow{Name: name, Instances: counts})
	}
	answer(w, http.StatusOK, out)
}

func (d *Daemon) listInstances(w http.ResponseWriter, r *http.Request) {
	views := []View{}
	flow, status := r.URL.Query().Get("flow"), flowrt.Status(r.URL.Query().Get("status"))
	for _, p := range d.programs() {
		if flow != "" && d.runtimeOf(flow) != p.Runtime {
			continue
		}
		for _, inst := range p.Runtime.Store.List(flow, status) {
			views = append(views, viewOf(inst))
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	answer(w, http.StatusOK, views)
}

func (d *Daemon) start(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		Flow   string          `json:"flow"`
		ID     string          `json:"id"`
		Fields json.RawMessage `json:"fields"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&asked); err != nil {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"flow\": ..., \"id\": ..., \"fields\": {...}}: "+err.Error())
		return
	}
	if asked.Flow == "" {
		fail(w, http.StatusBadRequest, "request", "a start names the flow, and the instance if it wants: {\"flow\": \"Order\", \"id\": \"order:7\", \"fields\": {...}}")
		return
	}
	if !d.knows(asked.Flow) {
		if flows := d.flows(); len(flows) > 0 {
			fail(w, http.StatusNotFound, "flow", fmt.Sprintf("the program has no flow %s; it has %s", asked.Flow, strings.Join(flows, ", ")))
		} else {
			fail(w, http.StatusNotFound, "flow", fmt.Sprintf("this daemon serves no program yet, and no flow %s: qoqi deploy gives it one", asked.Flow))
		}
		return
	}
	fields := asked.Fields
	if len(fields) == 0 || string(fields) == "null" {
		fields = json.RawMessage(`{}`)
	}
	var err error
	if schema := d.schema(); schema != nil {
		fields, err = schema.Conform(asked.Flow, fields)
	} else {
		fields, err = typed(fields, asked.Flow)
	}
	if err != nil {
		fail(w, http.StatusBadRequest, "fields", err.Error())
		return
	}
	if r.URL.Query().Get("dry") != "" {
		// Checked, not started: what the fields are as data, and whether
		// the name is taken
		dry := map[string]any{"flow": asked.Flow, "fields": json.RawMessage(fields)}
		if asked.ID != "" {
			if _, before, ok := d.find(asked.ID); ok {
				dry["taken"] = viewOf(before)
			}
		}
		answer(w, http.StatusOK, dry)
		return
	}
	if asked.ID == "" {
		// A name of its own: the flow's, and a few letters drawn
		asked.ID = strings.ToLower(asked.Flow) + ":" + NewToken()[:8]
	}
	_, before, existed := d.find(asked.ID)
	if existed && before.Flow != asked.Flow {
		fail(w, http.StatusConflict, "taken", fmt.Sprintf("%s is an instance of %s", asked.ID, before.Flow))
		return
	}
	inst, err := d.runtimeOf(asked.Flow).Start(asked.ID, asked.Flow, fields)
	if err != nil {
		fail(w, http.StatusInternalServerError, "store", err.Error())
		return
	}
	d.poke()
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	answer(w, status, viewOf(inst))
}

func (d *Daemon) knows(flow string) bool {
	for _, name := range d.flows() {
		if name == flow {
			return true
		}
	}
	return false
}

// typed gives fields written without their type the type of the flow:
// {"id": "A1"} is {"$type": "Order", "id": "A1"}.
func typed(fields json.RawMessage, flow string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(fields))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("the fields of a flow are an object, not %s", trimmed)
	}
	if strings.Contains(trimmed, `"$type"`) {
		return fields, nil
	}
	name, _ := json.Marshal(flow)
	rest := strings.TrimSpace(trimmed[1:])
	if rest == "}" {
		return json.RawMessage(`{"$type": ` + string(name) + `}`), nil
	}
	return json.RawMessage(`{"$type": ` + string(name) + `, ` + rest), nil
}

func (d *Daemon) show(w http.ResponseWriter, r *http.Request) {
	rt, inst, ok := d.find(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+r.PathValue("id"))
		return
	}
	answer(w, http.StatusOK, d.detailOf(rt, inst))
}

// Detail is an instance as one page shows it: its view, where each of its
// tasks is, and what its steps returned.
type Detail struct {
	View
	Tasks   []vm.Place        `json:"tasks"`
	Kept    []flowrt.Listed   `json:"kept"`
	Tries   map[string]int    `json:"tries,omitempty"`   // by key: how many times it left
	Retries map[string]string `json:"retries,omitempty"` // by key: when it is tried again
	Output  []Line            `json:"output"`
}

func (d *Daemon) detailOf(rt *flowrt.Runtime, inst *flowrt.Instance) Detail {
	detail := Detail{View: viewOf(inst), Tasks: []vm.Place{}, Kept: []flowrt.Listed{}, Tries: inst.Tries, Output: d.hub.output(inst.ID, 200)}
	if len(inst.State) > 0 && inst.Status != flowrt.Done && inst.Status != flowrt.Failed {
		// Read with the bytecode of the version it runs
		if chunk, err := rt.ChunkOf(inst); err == nil {
			if places, err := vm.Where(chunk, inst.State); err == nil {
				detail.Tasks = places
			}
		}
	}
	if lister, ok := rt.Store.(flowrt.Lister); ok {
		if kept := lister.Results(flowrt.Prefix(inst)); kept != nil {
			detail.Kept = kept
		}
	}
	for key, at := range inst.Retried {
		if detail.Retries == nil {
			detail.Retries = map[string]string{}
		}
		detail.Retries[key] = time.UnixMilli(at).UTC().Format(time.RFC3339Nano)
	}
	return detail
}

// program is what the daemon knows of the program: its flows, the types
// of their fields, its steps, its files.
func (d *Daemon) program(w http.ResponseWriter, r *http.Request) {
	schema := d.schema()
	if schema == nil {
		fail(w, http.StatusNotFound, "program", "this daemon was given no description of its program")
		return
	}
	answer(w, http.StatusOK, schema)
}

// source is a file of the program, line by line: one of those it was
// compiled from, and no other.
func (d *Daemon) source(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	// A program deployed brought its files; one the daemon was made with
	// is read where it is
	d.pmu.RLock()
	var text string
	found := false
	for _, files := range d.sources {
		if t, ok := files[file]; ok {
			text, found = t, true
		}
	}
	d.pmu.RUnlock()
	schema := d.schema()
	allowed := found
	if !found && schema != nil {
		for _, f := range schema.Files {
			if f == file {
				allowed = true
			}
		}
	}
	if !allowed {
		fail(w, http.StatusNotFound, "source", "the program was not compiled from "+file)
		return
	}
	if !found {
		data, err := os.ReadFile(file)
		if err != nil {
			fail(w, http.StatusNotFound, "source", err.Error())
			return
		}
		text = string(data)
	}
	answer(w, http.StatusOK, map[string]any{"file": file, "lines": strings.Split(strings.TrimRight(text, "\n"), "\n")})
}

// output is what the program printed lately, of one instance or of all.
func (d *Daemon) output(w http.ResponseWriter, r *http.Request) {
	answer(w, http.StatusOK, d.hub.output(r.URL.Query().Get("instance"), 500))
}

// result gives what run returned, waiting for it as long as ?wait= says.
func (d *Daemon) result(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, _ := time.ParseDuration(r.URL.Query().Get("wait"))
	if wait > time.Minute {
		wait = time.Minute
	}
	until := time.Now().Add(wait)
	for {
		_, inst, ok := d.find(id)
		if !ok {
			fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
			return
		}
		switch inst.Status {
		case flowrt.Done:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(inst.Result)
			return
		case flowrt.Stopped:
			fail(w, http.StatusConflict, "stopped", inst.Stopped)
			return
		case flowrt.Failed:
			why := inst.Cancel
			if inst.Ended != "" {
				why = inst.Ended
			}
			fail(w, http.StatusConflict, "cancelled", "cancelled: "+why)
			return
		}
		if time.Now().After(until) {
			answer(w, http.StatusAccepted, viewOf(inst))
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// cancel asks that an instance end: what it gave flow.undo runs, and it
// ends failed. An instance ending already is left as it is.
func (d *Daemon) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var asked struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&asked); err != nil && err != io.EOF {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"reason\": ...}: "+err.Error())
		return
	}
	if asked.Reason == "" {
		asked.Reason = "cancelled from outside"
	}
	rt, _, ok := d.find(id)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
		return
	}
	if err := rt.Cancel(id, asked.Reason); err != nil {
		fail(w, http.StatusConflict, "ended", err.Error())
		return
	}
	d.poke()
	inst, _ := rt.Store.Get(id)
	answer(w, http.StatusAccepted, viewOf(inst))
}

// resume takes a stopped instance up again; {"dry": true} only says
// what it would take by key and do next.
func (d *Daemon) resume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var asked struct {
		Dry bool `json:"dry"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&asked); err != nil && err != io.EOF {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"dry\": true}: "+err.Error())
		return
	}
	rt, _, ok := d.find(id)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
		return
	}
	if asked.Dry {
		report, err := rt.DryResume(id)
		if err != nil {
			fail(w, http.StatusConflict, "resume", err.Error())
			return
		}
		answer(w, http.StatusOK, report)
		return
	}
	if err := rt.Resume(id); err != nil {
		fail(w, http.StatusConflict, "resume", err.Error())
		return
	}
	d.poke()
	inst, _ := rt.Store.Get(id)
	answer(w, http.StatusAccepted, viewOf(inst))
}

// end ends a stopped instance as it is, with no more undos.
func (d *Daemon) end(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var asked struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&asked); err != nil && err != io.EOF {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"reason\": ...}: "+err.Error())
		return
	}
	rt, _, ok := d.find(id)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
		return
	}
	if err := rt.End(id, asked.Reason); err != nil {
		fail(w, http.StatusConflict, "end", err.Error())
		return
	}
	inst, _ := rt.Store.Get(id)
	answer(w, http.StatusAccepted, viewOf(inst))
}

// send gives an instance a message on one of its inboxes: {"message":
// ..., "key": ...}. A key sent before is one message.
func (d *Daemon) send(w http.ResponseWriter, r *http.Request) {
	id, inbox := r.PathValue("id"), r.PathValue("inbox")
	var asked struct {
		Message json.RawMessage `json:"message"`
		Key     string          `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&asked); err != nil || len(asked.Message) == 0 {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"message\": ..., \"key\": \"evt-1\"}, the key to send a message once")
		return
	}
	rt, inst, ok := d.find(id)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
		return
	}
	message := asked.Message
	if schema := d.schema(); schema != nil {
		// Written as a person writes it, or as the program does: typed
		// from the program either way
		typed, err := schema.ConformMessage(inst.Flow, inbox, message)
		if err != nil {
			fail(w, http.StatusBadRequest, "message", err.Error())
			return
		}
		message = typed
	}
	already, err := rt.Send(id, inbox, asked.Key, message)
	switch {
	case errors.Is(err, flowrt.ErrEnded):
		fail(w, http.StatusConflict, "ended", id+" has ended")
	case errors.Is(err, flowrt.ErrFull):
		fail(w, http.StatusTooManyRequests, "full", "the inbox "+inbox+" of "+id+" is full: send it again later")
	case err != nil:
		fail(w, http.StatusNotFound, "inbox", err.Error())
	case already:
		answer(w, http.StatusOK, map[string]string{"sent": "already"})
	default:
		d.poke()
		answer(w, http.StatusOK, map[string]string{"sent": "delivered"})
	}
}

// answerByHand takes a person's answer to a step of a stopped instance
// the application could not tell of: {"key": ..., "value": ...}, what it
// did, or {"key": ..., "nothing": true}.
func (d *Daemon) answerByHand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var asked struct {
		Key     string          `json:"key"`
		Value   json.RawMessage `json:"value"`
		Nothing bool            `json:"nothing"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&asked); err != nil || asked.Key == "" || (asked.Value == nil && !asked.Nothing) {
		fail(w, http.StatusBadRequest, "request", `the request is JSON: {"key": ..., "value": ...} for what the step did, or {"key": ..., "nothing": true}`)
		return
	}
	rt, _, ok := d.find(id)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no instance "+id)
		return
	}
	if err := rt.AnswerByHand(id, asked.Key, asked.Value, asked.Nothing); err != nil {
		fail(w, http.StatusConflict, "answer", err.Error())
		return
	}
	answer(w, http.StatusOK, map[string]bool{"answered": true})
}

// attempt takes what an application answers a step later, after a
// 202: {"key": ..., "attempt": ..., "token": ..., "value": ...}, or any
// other answer a step has. Told 410 once the attempt was given up, the
// answer kept aside; 403 with another token.
func (d *Daemon) attempt(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "request", err.Error())
		return
	}
	for _, p := range d.programs() {
		already, err := p.Runtime.Answer(body)
		switch {
		case errors.Is(err, flowrt.ErrNoAttempt):
			continue
		case errors.Is(err, flowrt.ErrToken):
			fail(w, http.StatusForbidden, "token", err.Error())
		case errors.Is(err, flowrt.ErrLate):
			fail(w, http.StatusGone, "late", err.Error())
		case errors.Is(err, flowrt.ErrMisfit):
			// Kept as a panic of the step, and the application told why
			fail(w, http.StatusUnprocessableEntity, "value", err.Error())
		case err != nil:
			fail(w, http.StatusBadRequest, "answer", err.Error())
		case already:
			answer(w, http.StatusOK, map[string]bool{"already": true})
		default:
			d.poke()
			answer(w, http.StatusOK, map[string]bool{"kept": true})
		}
		return
	}
	fail(w, http.StatusNotFound, "attempt", flowrt.ErrNoAttempt.Error())
}

func answer(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	answer(w, status, map[string]string{"error": code, "message": message})
}

// Address is what the daemon writes for its clients next to its store:
// where it listens and its token, readable by its user alone.
type Address struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// AddressFile is where a daemon on a store says where it is.
func AddressFile(store string) string { return Beside(store) + ".daemon" }

// WriteAddress writes where the daemon is, for the commands.
func WriteAddress(store string, a Address) error {
	data, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(AddressFile(store), data, 0o600)
}

// ReadAddress reads where the daemon on a store is.
func ReadAddress(store string) (Address, error) {
	var a Address
	data, err := os.ReadFile(AddressFile(store))
	if errors.Is(err, os.ErrNotExist) {
		return a, fmt.Errorf("no daemon is serving %s on this machine: start one with qoqi serve <program> --store %s", sqldb.Redacted(store), sqldb.Redacted(store))
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(data, &a)
	return a, err
}

// The pages: the console, built from console/ in the repository (npm run
// build there writes it here), served as it is. A path that is not a file
// of it is a page of the console, which finds its way from the address.
//
//go:embed all:console
var pages embed.FS

func servePage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	data, err := pages.ReadFile("console/" + name)
	if name == "" || err != nil {
		name = "index.html"
		data, err = pages.ReadFile("console/index.html")
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "pages", "the console was not built into this qoqi: npm run build in console/")
		return
	}
	h := w.Header()
	if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
		h.Set("Content-Type", kind)
	}
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(name, "assets/") {
		// Their names change with what they hold
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-store")
	}
	w.Write(data)
}
