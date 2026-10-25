// Package flowd is the daemon of flows, qoqi serve: it runs the
// flows of a program against a store, wakes what is due, and answers the
// HTTP API every client goes through, the qoqi commands first (see
// docs/flow.md, "The daemon, from outside").
package flowd

import (
	"context"
	"crypto/rand"
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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/flows"
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

	// Keep is what the console keeps: its users, its drafts, what
	// happened. Without it, the daemon answers to its token alone
	Keep *Keep

	looks map[string]look // by program: when its instances were last looked at

	accountsMu sync.Mutex // one change of the users at a time
	logins     throttle
	steps      stepCounts
	drafting   sync.Mutex    // one change of the drafts at a time
	testing    chan struct{} // one run of tests at a time
	sandboxes  sandboxes     // the trials of drafts, in memory

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
	d := &Daemon{Programs: programs, Token: NewToken(), Log: io.Discard, hub: newHub(), wake: make(chan struct{}, 1), stopped: make(chan struct{}), testing: make(chan struct{}, 1)}
	owner := map[string]string{}
	for _, p := range programs {
		d.observe(p.Runtime)
		if err := oneOfEachName(p.Name, p.Flows); err != nil {
			return nil, err
		}
		for _, flow := range p.Flows {
			if other, taken := owner[flows.FlowName(flow)]; taken {
				return nil, fmt.Errorf("%s and %s both have a flow %s: a flow belongs to one program", other, p.Name, flows.FlowName(flow))
			}
			owner[flows.FlowName(flow)] = p.Name
			d.Flows = append(d.Flows, flow)
		}
	}
	return d, nil
}

// oneOfEachName says when two flows of a program have one name, in two
// of its modules: a flow is started by its name.
func oneOfEachName(program string, names []string) error {
	seen := map[string]string{}
	for _, flow := range names {
		name := flows.FlowName(flow)
		if other, taken := seen[name]; taken {
			return fmt.Errorf("%s has two flows %s, %s and %s: a flow is started by its name, so rename one", program, name, other, flow)
		}
		seen[name] = flow
	}
	return nil
}

// flowNamed is the flow of those given that a name names: the flow of
// that name in the program (easy.Easy), or the one a module declares
// under it (Easy). "" when there is none, or more than one.
func flowNamed(names []string, name string) string {
	found := ""
	for _, flow := range names {
		if flow == name {
			return flow
		}
		if flows.FlowName(flow) == name {
			if found != "" {
				return ""
			}
			found = flow
		}
	}
	return found
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
		if inst, ok, _ := p.Runtime.Store.Get(id); ok {
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
		id := instanceOfKey(key)
		d.countStep(d.hub.flow(id), step)
		d.hub.send(Event{Kind: "step", At: stamp(), ID: id, Step: step, Key: key, Once: once})
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
			d.keepWhatHappened(d.hub.observe(d.look(p), p.Name))
			if err != nil {
				d.mu.Lock()
				d.failure = err
				d.mu.Unlock()
				fmt.Fprintf(d.Log, "qoqi: %v\n", err)
				return err
			}
		}
		d.keepSteps(false)
		select {
		case <-ctx.Done():
			d.keepSteps(true)
			return nil
		case <-d.wake:
		case <-timer.C:
		}
	}
}

// How far back a look reaches before the last one, for writes stamped
// before it and committed after; and how often every instance is looked
// at, for a write a look missed, of a daemon whose clock runs ahead.
const (
	lookBack = 5 * time.Second
	lookAll  = time.Minute
)

// look is when a program's instances were last looked at.
type look struct {
	at  int64     // the last look, in milliseconds
	all time.Time // the last look at all of them
}

// look gives the instances of a program to look at for what changed:
// those written since the last look (and lookBack before it), by the
// store's index; all of them at the first look and every lookAll. A look
// at every instance, at each tick, read each one's state twenty times a
// second: most of what an idle daemon did, and more with each instance.
// Run alone calls it.
func (d *Daemon) look(p Program) []*flowrt.Instance {
	if d.looks == nil {
		d.looks = map[string]look{}
	}
	now := time.Now()
	last, seen := d.looks[p.Name]
	var instances []*flowrt.Instance
	if !seen || now.Sub(last.all) >= lookAll {
		instances = p.Runtime.Store.List("", "")
		last.all = now
	} else {
		instances = p.Runtime.Store.ChangedSince(last.at - lookBack.Milliseconds())
	}
	last.at = now.UnixMilli()
	d.looks[p.Name] = last
	return instances
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
//
// What changes code or people is for admins alone: the token, or a user
// who is not an operator. Code deployed runs on the daemon's host with
// the daemon's rights, through its steps, so that whoever deploys can do
// there what the daemon can.
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
	mux.HandleFunc("POST /api/deploy", d.admin(d.deploy))
	mux.HandleFunc("POST /api/undeploy", d.admin(d.undeploy))
	mux.HandleFunc("GET /api/deployments", d.deployments)
	mux.HandleFunc("GET /api/deployments/{seq}", d.deployment)
	mux.HandleFunc("GET /api/deployments/{seq}/diff", d.diff)
	mux.HandleFunc("POST /api/deployments/{seq}/redeploy", d.admin(d.redeploy))
	mux.HandleFunc("GET /api/source", d.source)
	mux.HandleFunc("GET /api/events", d.events)
	mux.HandleFunc("GET /api/output", d.output)
	mux.HandleFunc("GET /api/users", d.listUsers)
	mux.HandleFunc("POST /api/users", d.admin(d.createUser))
	mux.HandleFunc("DELETE /api/users/{name}", d.admin(d.removeUser))
	mux.HandleFunc("POST /api/users/{name}/password", d.password)
	mux.HandleFunc("PUT /api/me/prefs", d.prefs)
	mux.HandleFunc("GET /api/workspace", d.workspace)
	mux.HandleFunc("GET /api/workspace/{program}", d.draft)
	mux.HandleFunc("DELETE /api/workspace/{program}", d.admin(d.discard))
	mux.HandleFunc("PUT /api/workspace/{program}/files/{file...}", d.admin(d.saveFile))
	mux.HandleFunc("DELETE /api/workspace/{program}/files/{file...}", d.admin(d.removeFile))
	mux.HandleFunc("POST /api/workspace/{program}/check", d.check)
	mux.HandleFunc("POST /api/workspace/{program}/deploy", d.admin(d.deployDraft))
	mux.HandleFunc("POST /api/workspace/{program}/test", d.admin(d.test))
	mux.HandleFunc("POST /api/workspace/{program}/try", d.admin(d.try))
	mux.HandleFunc("GET /api/sandboxes", d.listTrials)
	mux.HandleFunc("GET /api/sandboxes/{id}", d.showTrial)
	mux.HandleFunc("DELETE /api/sandboxes/{id}", d.admin(d.closeTrial))
	mux.HandleFunc("POST /api/sandboxes/{id}/{act}", d.admin(d.actTrial))
	mux.HandleFunc("POST /api/format", d.format)
	mux.HandleFunc("GET /api/stats", d.stats)
	public := http.NewServeMux()
	public.HandleFunc("GET /api/auth", d.authState)
	public.HandleFunc("POST /api/auth/login", d.login)
	public.HandleFunc("POST /api/auth/logout", d.logout)
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
		if strings.HasPrefix(r.URL.Path, "/api/auth") {
			// Before anyone signs in: who they are, setting up, signing in
			public.ServeHTTP(w, r)
			return
		}
		p, problem := d.authenticate(r)
		if problem != "" {
			fail(w, http.StatusUnauthorized, "token", problem)
			return
		}
		if p.Renew && r.URL.Path != "/api/users/"+p.User+"/password" {
			// A password given, admin's first: changed before anything else
			fail(w, http.StatusForbidden, "renew", "change your password first: it is one you were given")
			return
		}
		mux.ServeHTTP(w, withPrincipal(r, p))
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
	if named := flowNamed(d.flows(), flow); named != "" {
		flow = named
	}
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
	if named := flowNamed(d.flows(), asked.Flow); named != "" {
		asked.Flow = named
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
		asked.ID = strings.ToLower(flows.FlowName(asked.Flow)) + ":" + NewToken()[:8]
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
	// Unanswered are the steps that left and whose answer is not kept:
	// what a person may answer by hand, for a stopped instance
	Unanswered []string `json:"unanswered,omitempty"`
	// Sending are the steps by a queue on their way, their answer not
	// kept: their last attempt, and what became of it
	Sending []Sending `json:"sending,omitempty"`
}

// Sending is a step by a queue on its way.
type Sending struct {
	Key     string `json:"key"`
	Step    string `json:"step"`
	Queue   string `json:"queue"`
	Attempt int    `json:"attempt"` // the last
	State   string `json:"state"`
	Said    string `json:"said,omitempty"`  // what the queue answered the last that may have arrived
	Again   string `json:"again,omitempty"` // when it is sent again
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
	if inst.Status != flowrt.Done && inst.Status != flowrt.Failed {
		kept := map[string]bool{}
		for _, k := range detail.Kept {
			kept[k.Key] = true
		}
		var keys []string
		for key := range inst.Tries {
			keys = append(keys, key)
		}
		for key := range inst.Working {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			attempts := rt.Store.Attempts(key)
			if kept[key] || len(attempts) == 0 {
				continue
			}
			last := attempts[len(attempts)-1]
			s := Sending{Key: key, Step: last.Step, Queue: last.Queue, Attempt: last.Attempt, State: string(last.State), Said: last.Said}
			// What the queue said of the last attempt that may have arrived
			for i := len(attempts) - 1; s.Said == "" && i >= 0; i-- {
				s.Said = attempts[i].Said
			}
			if at, ok := inst.Retried[key]; ok {
				s.Again = time.UnixMilli(at).UTC().Format(time.RFC3339Nano)
			} else if inst.WakeAt > 0 && inst.Status == flowrt.Waiting {
				s.Again = time.UnixMilli(inst.WakeAt).UTC().Format(time.RFC3339Nano)
			}
			detail.Sending = append(detail.Sending, s)
		}
	}
	if inst.Status == flowrt.Stopped {
		kept := map[string]bool{}
		for _, k := range detail.Kept {
			kept[k.Key] = true
		}
		for key := range inst.Tries {
			if !kept[key] {
				detail.Unanswered = append(detail.Unanswered, key)
			}
		}
		for key := range inst.Working {
			if !kept[key] && !slices.Contains(detail.Unanswered, key) {
				detail.Unanswered = append(detail.Unanswered, key)
			}
		}
		sort.Strings(detail.Unanswered)
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
	inst, _, _ := rt.Store.Get(id)
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
	inst, _, _ := rt.Store.Get(id)
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
	inst, _, _ := rt.Store.Get(id)
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
