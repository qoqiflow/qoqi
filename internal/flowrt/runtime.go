// Package flowrt runs flows: instances of a struct that implements
// std.flow.Flow, kept in a store while they wait and taken up where they
// were (see docs/flow.md and docs/flow-runtime.md).
//
// An instance runs until none of its tasks can go on. It is then written
// whole, before the steps its tasks wait for leave, together, or when it
// goes to sleep; what a step returns is kept under its key as soon as it
// comes, and an instance is taken up from what was last written, never
// by playing it again.
package flowrt

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Status is where an instance is in its life.
type Status string

const (
	Waiting Status = "waiting" // asleep, or not started yet: due at WakeAt
	Running Status = "running" // taken up by a runtime
	Stopped Status = "stopped" // a mistake in the program, for a person
	Done    Status = "done"    // run returned
	Failed  Status = "failed"  // cancelled, its undos run
)

// Finish is the phase of an instance that is ending in failure: run
// returned an Err, or it was cancelled, and its undos run.
const Finish = "finish"

// Instance is what the store keeps of one instance.
type Instance struct {
	ID     string
	Birth  string // drawn at the start, in every key
	Flow   string // the struct that implements Flow
	Status Status

	Fields []byte         // what it was started with, as data
	State  []byte         // the program as it was last written; nil before it starts
	Counts map[string]int // by task and step, how many times it was asked for, as State was written
	Tries  map[string]int // how many times each step key has left, as State was written
	// The keys of the steps that said flow.retry, and when their next
	// attempt leaves: it is run even for a step never run twice, since
	// its author said nothing left
	Retried map[string]int64
	// The keys of the steps the application answers later, and when each
	// is given up
	Working map[string]int64
	WakeAt  int64 // milliseconds since 1970: when it is due

	Result  []byte // done: what run returned, as data
	Stopped string // stopped: why

	Phase string    // "" while it carries out run; Finish once it is ending
	Undos []vm.Undo // what flow.undo was given, kept with the instance
	// Cancel is why a cancellation was asked: only the store's Cancel
	// writes it, and a save leaves it as it is
	Cancel string

	// Pass is how many stages it has passed: in the keys of its steps,
	// whose counts start again at each. AtStage is the last stage it
	// passed with run alone and its variables data: where it can be taken
	// up from, in this version or another
	Pass    int
	AtStage *AtStage

	// Version is the fingerprint of the bytecode the instance runs: the
	// one it was started in, until it moves to a newer one at a stage
	Version string

	// Replaying says that the instance was resumed from its stage, and
	// has not passed the next one yet: what its steps are asked is
	// compared with what they were, and what they gave has to be taken
	Replaying bool

	// Drawn are the values its tasks drew without a step since AtStage,
	// the time and random numbers, by task, in the order each drew them:
	// kept with the instance, as what its steps gave is kept under their
	// keys. Giving are those a replay gives back, by task, before it
	// draws new ones: what was drawn when the instance was resumed
	Drawn  map[string][]vm.Drawn
	Giving map[string][]vm.Drawn

	// Ended is why a person ended it as it was, stopped
	Ended string

	// Taking are the messages it took since it was last written: the save
	// marks them taken, in the same write, and keeps nothing of them here
	Taking []Taken
	untake bool // a resume gives back the messages taken since the stage

	// CancelTaken says that the flow, which waits for its cancellation,
	// was given it: it ends its own way, and is not stopped for it
	CancelTaken bool

	Parent   string   // the instance that started it, if it is a child
	Children []string // the instances it started, which it cancels if it fails
	// Awaits are the instances it waits for: the save that ends one of
	// them makes it due
	Awaits []string
}

// AtStage is an instance at a stage: the stage, the pass, and the
// variables alive there, as data.
type AtStage struct {
	Name string                     `json:"name"`
	Pass int                        `json:"pass"`
	Vars map[string]json.RawMessage `json:"vars"`

	// How many undos the instance had noted there: one resumed from the
	// stage notes those after it again
	Undos int `json:"undos,omitempty"`
}

// Never is when an instance that waits only for other instances wakes:
// the save that ends one of them makes it due.
const Never = int64(1) << 62

// ended says whether an instance has ended.
func (i *Instance) ended() bool {
	return i.Status == Done || i.Status == Failed
}

func (i *Instance) copy() *Instance {
	c := *i
	c.Counts = copyCounts(i.Counts)
	c.Tries = copyCounts(i.Tries)
	c.Retried = copyMoments(i.Retried)
	c.Working = copyMoments(i.Working)
	c.Drawn = copyDrawn(i.Drawn)
	c.Giving = copyDrawn(i.Giving)
	c.Undos = append([]vm.Undo(nil), i.Undos...)
	c.Children = append([]string(nil), i.Children...)
	c.Awaits = append([]string(nil), i.Awaits...)
	if i.AtStage != nil {
		at := *i.AtStage
		c.AtStage = &at
	}
	return &c
}

// Runtime runs the flows of one program against a store.
type Runtime struct {
	Chunk *vm.Chunk
	Store Store
	Out   io.Writer

	// Now is the clock, in milliseconds since 1970
	Now func() int64

	// MaxTries is how many attempts a step that keeps asking to be run
	// again is given before the instance stops for a person
	MaxTries int

	// Lease is how long an instance is held by this daemon, on a store
	// several share, without a word from it: 30 seconds when not said
	Lease time.Duration

	// Replace runs, for a step by name, a function in its place: a trial
	// gives a step of the program the function its test wrote. It gives
	// what the step would have, and what it gave flow.undo
	Replace map[string]func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error)

	// Queues sends the steps the application carries out to their
	// queues; without it, such a step stops its instance, naming it
	Queues vm.Queues
	// Boundary, when set, is the form what crosses to a queue takes, as
	// the program's types say it; without it, data goes as the store has it
	Boundary Boundary

	// Poison is how many attempts in a row of a step by a queue may have
	// arrived, never answered, before its instance stops
	Poison int
	// Garbled is how many answers in a row of a step may be none of the
	// answers a step has, before its instance stops: an address that is
	// no application of the library answers so every time
	Garbled int
	gate    sync.Mutex // a step takes room on its queue one at a time

	// Ran, when set, is told of every step that leaves: its name, its
	// key, and whether it is one never run twice
	Ran func(step, key string, once bool)

	// Workers is how many instances run at once, each in a goroutine of
	// its own: one, the default, runs them one after another as RunDue
	// finds them. Finished, when set, is told each time a worker is done
	// with one, for whoever waits to look for more
	Workers  int
	Finished func()

	// dry, in a dry run of a resume, is told what the instance would do
	dry *DryRun

	// The instances the workers have taken, and the first fault one met
	claims  sync.Mutex
	taken   map[string]bool
	working int
	failure error

	// swap keeps a new version out while an instance runs on the one
	// before: each run, and each call from outside, holds it to read
	swap sync.RWMutex

	// The database flow.store() opens on a store that is not in one
	scratchOnce sync.Once
	scratch     *sql.DB
	scratchErr  error

	versions sync.Mutex
	newest   string               // the fingerprint of Chunk
	kept     bool                 // whether the store has Chunk's bytecode
	chunks   map[string]*vm.Chunk // older versions, read back from the store

	// OutOf, when set, is where an instance prints, by its name: instances
	// run side by side, and what each prints is told apart. Without it,
	// they all print to Out
	OutOf func(id string) io.Writer
}

// Deploy makes a new version of the program the newest, while the
// runtime runs: what runs now ends its turn on the version before, and
// each instance moves at its next stage, as it would after a restart.
// The queues and the boundary are the new version's.
func (r *Runtime) Deploy(chunk *vm.Chunk, queues vm.Queues, boundary Boundary) {
	r.swap.Lock()
	defer r.swap.Unlock()
	r.versions.Lock()
	defer r.versions.Unlock()
	if old := r.newest; old != "" && r.kept {
		// The instances of the version before read it from here, rather
		// than from the store
		if r.chunks == nil {
			r.chunks = map[string]*vm.Chunk{}
		}
		r.chunks[old] = r.Chunk
	}
	r.Chunk, r.Queues, r.Boundary = chunk, queues, boundary
	r.newest, r.kept = "", false
}

// Newest is the version of the program a new instance starts on.
func (r *Runtime) Newest() *vm.Chunk {
	r.swap.RLock()
	defer r.swap.RUnlock()
	return r.Chunk
}

// outOf is where an instance prints.
func (r *Runtime) outOf(id string) io.Writer {
	if r.OutOf != nil {
		return r.OutOf(id)
	}
	return r.Out
}

// newestVersion is the fingerprint of the runtime's program, kept in the
// store for the instances that will run it.
func (r *Runtime) newestVersion() (string, error) {
	r.versions.Lock()
	defer r.versions.Unlock()
	if r.newest == "" {
		r.newest = vm.Fingerprint(r.Chunk)
	}
	if !r.kept {
		bytecode, err := vm.SerializeChunk(r.Chunk)
		if err != nil {
			return "", err
		}
		if err := r.Store.PutVersion(r.newest, bytecode); err != nil {
			return "", err
		}
		r.kept = true
	}
	return r.newest, nil
}

// ChunkOf is the bytecode an instance runs, to read what is kept of it.
func (r *Runtime) ChunkOf(inst *Instance) (*vm.Chunk, error) { return r.chunkOf(inst) }

// chunkOf is the bytecode an instance runs: its version's, read back
// from the store, or the runtime's own.
func (r *Runtime) chunkOf(inst *Instance) (*vm.Chunk, error) {
	newest, err := r.newestVersion()
	if err != nil {
		return nil, err
	}
	if inst.Version == "" || inst.Version == newest {
		return r.Chunk, nil
	}
	r.versions.Lock()
	defer r.versions.Unlock()
	if chunk, ok := r.chunks[inst.Version]; ok {
		return chunk, nil
	}
	bytecode, ok := r.Store.Version(inst.Version)
	if !ok {
		return nil, fmt.Errorf("the bytecode of version %s, which %s runs, is not in the store", inst.Version, inst.ID)
	}
	chunk, err := vm.DeserializeChunk(bytecode)
	if err != nil {
		return nil, err
	}
	if r.chunks == nil {
		r.chunks = map[string]*vm.Chunk{}
	}
	r.chunks[inst.Version] = chunk
	return chunk, nil
}

// sameShape says whether a stage of a flow has the same variables, of
// the same types, the flow's fields with them, in two versions: an
// instance there may move from one to the other without a migration.
func sameShape(old, newer *vm.Chunk, flow, stage string) bool {
	a, b := stageOf(old, flow, stage), stageOf(newer, flow, stage)
	if a == nil || b == nil || len(a.Shape) != len(b.Shape) {
		return false
	}
	for name, t := range a.Shape {
		if b.Shape[name] != t {
			return false
		}
	}
	return true
}

func stageOf(chunk *vm.Chunk, flow, stage string) *vm.Stage {
	idx := chunk.FindPrototype(flow + ".run")
	if idx < 0 {
		return nil
	}
	for i := range chunk.Prototypes[idx].Stages {
		if chunk.Prototypes[idx].Stages[i].Name == stage {
			return &chunk.Prototypes[idx].Stages[i]
		}
	}
	return nil
}

// migrate takes an instance at a stage of an older version to the newest.
// Its fields first: changed, they go through the newest's migrate fn
// fields. Then the stage: as it was, or through the newest's migration
// for it, which gives the stage to go on from and its variables; what a
// loop keeps for itself is carried over as it was. Nil when a migration
// the instance needs is not there, or cannot take it: it stays on its
// version.
func (r *Runtime) migrate(old *vm.Chunk, flow string, at *AtStage) (*AtStage, error) {
	from := stageOf(old, flow, at.Name)
	fields := fieldsOf(r.Chunk, flow)
	if from == nil || fields == nil {
		return nil, nil
	}
	self := at.Vars["self"]
	if !sameFields(from.Shape, fields) {
		migrated, err := r.migrateFields(flow, self)
		if err != nil || migrated == nil {
			return nil, err
		}
		self = migrated
	}

	if to := stageOf(r.Chunk, flow, at.Name); to != nil && sameVariables(from.Shape, to.Shape) {
		vars := map[string]json.RawMessage{}
		for name, value := range at.Vars {
			vars[name] = value
		}
		vars["self"] = self
		return &AtStage{Name: at.Name, Pass: at.Pass, Vars: vars, Undos: at.Undos}, nil
	}

	function := flow + "." + at.Name
	idx := r.Chunk.FindPrototype(function)
	if idx < 0 || !r.Chunk.Prototypes[idx].Migrate {
		return nil, nil
	}
	args := [][]byte{self}
	for _, param := range r.Chunk.Prototypes[idx].Params {
		value, ok := at.Vars[param]
		if !ok {
			return nil, nil
		}
		args = append(args, value)
	}
	data, err := r.runMigration(function, args)
	if err != nil {
		return nil, err
	}
	var given map[string]json.RawMessage
	if err := json.Unmarshal(data, &given); err != nil {
		return nil, err
	}
	var typed string
	_ = json.Unmarshal(given["$type"], &typed)
	typed = typed[strings.LastIndex(typed, ".")+1:]
	if typed == "" {
		return nil, nil
	}
	target := strings.ToLower(typed[:1]) + typed[1:]
	to := stageOf(r.Chunk, flow, target)
	if to == nil {
		return nil, nil
	}

	vars := map[string]json.RawMessage{"self": self}
	for name, value := range given {
		if name != "$type" {
			vars[name] = value
		}
	}
	for name, t := range to.Shape {
		if name == "self" || strings.HasPrefix(name, "self.") {
			continue
		}
		if _, ok := vars[name]; ok {
			continue
		}
		// What a loop keeps for itself, as it was
		if from.Shape[name] != t || at.Vars[name] == nil {
			return nil, nil
		}
		vars[name] = at.Vars[name]
	}
	return &AtStage{Name: target, Pass: at.Pass, Vars: vars, Undos: at.Undos}, nil
}

// migrateFields gives the fields of an instance, as data, through the
// newest version's migrate fn fields: what it takes of the old fields,
// by name, and the flow it gives. Nil when there is none, or it takes a
// field the instance had not.
func (r *Runtime) migrateFields(flow string, self []byte) ([]byte, error) {
	function := flow + ".fields"
	idx := r.Chunk.FindPrototype(function)
	if idx < 0 || !r.Chunk.Prototypes[idx].Migrate {
		return nil, nil
	}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(self, &old); err != nil {
		return nil, err
	}
	args := [][]byte{self}
	for _, param := range r.Chunk.Prototypes[idx].Params {
		value, ok := old[param]
		if !ok {
			return nil, nil
		}
		args = append(args, value)
	}
	data, err := r.runMigration(function, args)
	if err != nil {
		return nil, err
	}
	run := r.Chunk.FindPrototype(flow + ".run")
	return withInboxes(data, r.Chunk.Prototypes[run].Inboxes)
}

// runMigration runs a migration of the newest version, in a machine of
// its own, and gives what it gave as data.
func (r *Runtime) runMigration(function string, args [][]byte) ([]byte, error) {
	machine := vm.NewVM()
	machine.SetClock(r.Now)
	if r.Out != nil {
		machine.SetOutput(r.Out)
	}
	result, err := machine.Start(r.Chunk, function, args...)
	if err != nil {
		return nil, fmt.Errorf("the migration %s failed: %v", function, err)
	}
	return machine.DataOf(result)
}

// fieldsOf is the shape of the fields of a flow in a version, "self" and
// "self.name", as its stages hold them; nil for a flow with no stage.
func fieldsOf(chunk *vm.Chunk, flow string) map[string]string {
	idx := chunk.FindPrototype(flow + ".run")
	if idx < 0 || len(chunk.Prototypes[idx].Stages) == 0 {
		return nil
	}
	fields := map[string]string{}
	for name, t := range chunk.Prototypes[idx].Stages[0].Shape {
		if name == "self" || strings.HasPrefix(name, "self.") {
			fields[name] = t
		}
	}
	return fields
}

// sameFields says whether a stage's shape has the fields given.
func sameFields(shape, fields map[string]string) bool {
	n := 0
	for name, t := range shape {
		if name == "self" || strings.HasPrefix(name, "self.") {
			n++
			if fields[name] != t {
				return false
			}
		}
	}
	return n == len(fields)
}

// sameVariables says whether two shapes have the same variables, of the
// same types, the fields of the flow aside.
func sameVariables(a, b map[string]string) bool {
	count := func(shape map[string]string) int {
		n := 0
		for name := range shape {
			if name != "self" && !strings.HasPrefix(name, "self.") {
				n++
			}
		}
		return n
	}
	if count(a) != count(b) {
		return false
	}
	for name, t := range a {
		if name != "self" && !strings.HasPrefix(name, "self.") && b[name] != t {
			return false
		}
	}
	return true
}

// listens says whether the flow of an instance waits for its
// cancellation.
func (r *Runtime) listens(inst *Instance) bool {
	chunk, err := r.chunkOf(inst)
	if err != nil {
		return false
	}
	idx := chunk.FindPrototype(inst.Flow + ".run")
	return idx >= 0 && chunk.Prototypes[idx].Listens
}

// sameStep says whether a step has the same signature in two versions.
func sameStep(old, newer *vm.Chunk, step string) bool {
	a, b := old.FindPrototype(step), newer.FindPrototype(step)
	if a < 0 || b < 0 {
		return false
	}
	pa, pb := &old.Prototypes[a], &newer.Prototypes[b]
	return pb.Step && pa.Signature == pb.Signature && pa.ArgCount == pb.ArgCount
}

// moving is how a machine stops when its instance is to go on in a newer
// version, from the stage it has just passed.
type moving struct{}

func (*moving) Error() string { return "moving to a newer version" }

// Unknown is what a step never run twice gives when the process stopped
// while it ran: std.flow.Settled.Unknown.
const Unknown = `{"$type": "std.flow.Settled.Unknown", "$variant": true}`

// New makes a runtime for a compiled program.
func New(chunk *vm.Chunk, store Store, out io.Writer) *Runtime {
	return &Runtime{Chunk: chunk, Store: store, Out: out, Now: func() int64 { return time.Now().UnixMilli() }, MaxTries: 100, Poison: 20, Garbled: 3}
}

// Start makes an instance of a flow, due at once. A name that is taken
// gives the instance that has it, whatever the fields: a request sent
// twice makes one instance.
func (r *Runtime) Start(id, flow string, fields []byte) (*Instance, error) {
	r.swap.RLock()
	defer r.swap.RUnlock()

	inst, _, err := r.start(id, flow, fields, "")
	return inst, err
}

// start makes an instance, a child of parent unless that is "", and says
// whether it made it.
func (r *Runtime) start(id, flow string, fields []byte, parent string) (*Instance, bool, error) {
	if r.Chunk.FindPrototype(flow+".run") < 0 {
		return nil, false, fmt.Errorf("the program has no flow %s", flow)
	}
	version, err := r.newestVersion()
	if err != nil {
		return nil, false, err
	}
	if fields, err = withInboxes(fields, r.Chunk.Prototypes[r.Chunk.FindPrototype(flow+".run")].Inboxes); err != nil {
		return nil, false, err
	}
	inst := &Instance{ID: id, Birth: birth(), Flow: flow, Status: Waiting, Fields: fields, Counts: map[string]int{}, WakeAt: r.Now(), Parent: parent, Version: version}
	created, existing, err := r.Store.Create(inst)
	if err != nil {
		return nil, false, err
	}
	if !created {
		return existing, false, nil
	}
	return inst, true, nil
}

// host is the runtime, as the program of one instance asks it about
// others.
type host struct {
	r        *Runtime
	inst     *Instance
	children *[]string
	fault    *error
	taking   *[]Taken // the messages taken since the last write
	pass     *int
	// the cancellation, given to the flow during this run
	cancelTaken bool
}

func (h *host) Cancelled(take bool) (string, bool) {
	if h.inst.CancelTaken || h.cancelTaken {
		return "", false
	}
	now, ok := h.r.Store.Get(h.inst.ID)
	if !ok || now.Cancel == "" {
		return "", false
	}
	if take {
		h.cancelTaken = true
	}
	return now.Cancel, true
}

func (h *host) Message(inbox string, take bool) ([]byte, bool, error) {
	pending := map[int64]bool{}
	for _, t := range *h.taking {
		pending[t.Seq] = true
	}
	for _, msg := range h.r.Store.Messages(h.inst.ID, inbox) {
		if pending[msg.Seq] {
			continue
		}
		if take {
			*h.taking = append(*h.taking, Taken{Seq: msg.Seq, Pass: *h.pass})
		}
		return msg.Data, true, nil
	}
	return nil, false, nil
}

// hasMessage says whether an inbox has a message the instance has not
// taken, as an "inbox:name" a task waits for.
func (h *host) hasMessage(awaited string) bool {
	inbox, ok := strings.CutPrefix(awaited, "inbox:")
	if !ok {
		return false
	}
	_, found, _ := h.Message(inbox, false)
	return found
}

func (h *host) StartInstance(name string, fields []byte, detached bool) error {
	var typed struct {
		Type string `json:"$type"`
	}
	if err := json.Unmarshal(fields, &typed); err != nil || typed.Type == "" {
		return fmt.Errorf("flow.start is given an instance of a flow")
	}
	parent := h.inst.ID
	if detached {
		parent = ""
	}
	child, created, err := h.r.start(name, typed.Type, fields, parent)
	switch {
	case errors.Is(err, ErrCrash):
		*h.fault = err
		return err
	case err != nil:
		return err
	case child.Flow != typed.Type:
		return fmt.Errorf("%s is an instance of %s, not of %s", name, child.Flow, typed.Type)
	}
	// Its child if it made it, or made it before a stop it is taken up from
	if !detached && (created || child.Parent == h.inst.ID) {
		for _, known := range *h.children {
			if known == name {
				return nil
			}
		}
		*h.children = append(*h.children, name)
	}
	return nil
}

func (h *host) Ended(name string) ([]byte, bool, error) {
	return h.r.endedAs(name)
}

// endedAs is what waits for an instance gets once it has ended: Ok with
// what its run returned, or Err with how it was cancelled. One that is
// not there has gone.
func (r *Runtime) endedAs(name string) ([]byte, bool, error) {
	inst, ok := r.Store.Get(name)
	switch {
	case !ok:
		return cancelledAs("gone"), true, nil
	case inst.Status == Done:
		return []byte(`{"$type": "std.result.Result.Ok", "$variant": true, "0": ` + string(inst.Result) + `}`), true, nil
	case inst.Status == Failed && inst.Ended != "":
		return cancelledAs(inst.Ended), true, nil
	case inst.Status == Failed:
		return cancelledAs(inst.Cancel), true, nil
	}
	return nil, false, nil
}

func cancelledAs(reason string) []byte {
	quoted, _ := json.Marshal(reason)
	return []byte(`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "std.flow.Cancelled", "reason": ` + string(quoted) + `}}`)
}

// RunDue takes up every instance that is due, and runs each until it
// sleeps, ends or stops. It reports how many it took up. With workers,
// each goes to a worker of its own as one is free, and RunDue returns
// at once: an instance a worker has is not taken again meanwhile, and a
// fault a worker met is given by the next call.
func (r *Runtime) RunDue() (int, error) {
	ids := r.Store.Due(r.Now())
	if r.Workers <= 1 {
		for _, id := range ids {
			if err := r.take(id); err != nil {
				return 0, err
			}
		}
		return len(ids), nil
	}

	r.claims.Lock()
	defer r.claims.Unlock()
	if r.failure != nil {
		return 0, r.failure
	}
	if r.taken == nil {
		r.taken = map[string]bool{}
	}
	started := 0
	for _, id := range ids {
		if r.taken[id] || r.working >= r.Workers {
			continue
		}
		r.taken[id] = true
		r.working++
		started++
		go r.work(id)
	}
	return started, nil
}

// work is a worker running one instance.
func (r *Runtime) work(id string) {
	err := r.take(id)
	r.claims.Lock()
	delete(r.taken, id)
	r.working--
	if err != nil && r.failure == nil {
		r.failure = err
	}
	r.claims.Unlock()
	if r.Finished != nil {
		r.Finished()
	}
}

// Busy says how many instances the workers are running.
func (r *Runtime) Busy() int {
	r.claims.Lock()
	defer r.claims.Unlock()
	return r.working
}

// RunUntilIdle runs what is due, again and again, until no instance is
// waiting any more or limit has passed: for tests and small programs.
func (r *Runtime) RunUntilIdle(limit time.Duration) error {
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		if _, err := r.RunDue(); err != nil {
			return err
		}
		if r.Busy() == 0 && !r.Store.AnyWaiting() {
			r.claims.Lock()
			failure := r.failure
			r.claims.Unlock()
			return failure
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("instances still waiting after %v", limit)
}

// typedAs gives an object written without "$type" the type given.
func typedAs(data []byte, typeName string) []byte {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return data
	}
	if _, typed := object["$type"]; typed {
		return data
	}
	name, _ := json.Marshal(typeName)
	object["$type"] = name
	out, err := json.Marshal(object)
	if err != nil {
		return data
	}
	return out
}

// withInboxes gives the fields of an instance its inboxes, each named
// after its field: what the program wrote there is a capacity, the name
// is the runtime's to give.
func withInboxes(fields []byte, inboxes []vm.Inbox) ([]byte, error) {
	if len(inboxes) == 0 {
		return fields, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(fields, &object); err != nil {
		return nil, fmt.Errorf("the fields of a flow are an object: %v", err)
	}
	for _, inbox := range inboxes {
		name, _ := json.Marshal(inbox.Name)
		object[inbox.Name] = json.RawMessage(fmt.Sprintf(`{"$type": "std.flow.Inbox", "name": %s, "capacity": %d}`, name, inbox.Capacity))
	}
	return json.Marshal(object)
}

// Send gives an instance a message, on one of its inboxes, from outside:
// kept until the instance takes it. A key sent before to that inbox is
// already there, and nothing is kept: a webhook delivered twice is one
// message.
func (r *Runtime) Send(id, inbox, key string, message []byte) (already bool, err error) {
	r.swap.RLock()
	defer r.swap.RUnlock()

	inst, ok := r.Store.Get(id)
	if !ok {
		return false, fmt.Errorf("there is no instance %s", id)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(inst.Fields, &fields)
	var field struct {
		Type     string `json:"$type"`
		Capacity int    `json:"capacity"`
	}
	if err := json.Unmarshal(fields[inbox], &field); err != nil || field.Type != "std.flow.Inbox" {
		return false, fmt.Errorf("%s, an instance of %s, has no inbox %s", id, inst.Flow, inbox)
	}
	if !json.Valid(message) {
		return false, fmt.Errorf("a message is data, as JSON")
	}
	// A struct written without its type is of the inbox's
	if chunk, err := r.chunkOf(inst); err == nil {
		if idx := chunk.FindPrototype(inst.Flow + ".run"); idx >= 0 {
			for _, box := range chunk.Prototypes[idx].Inboxes {
				if box.Name == inbox && box.Message != "" {
					message = typedAs(message, box.Message)
				}
			}
		}
	}
	return r.Store.Send(id, inbox, key, message, field.Capacity)
}

// Cancel asks that an instance end: it stops where it is, and what it
// gave flow.undo runs. An instance ending already is left as it is.
func (r *Runtime) Cancel(id, reason string) error {
	return r.Store.Cancel(id, reason, r.Now())
}

// cancelled is how a machine stops when its instance is cancelled.
type cancelled struct{ reason string }

func (c *cancelled) Error() string { return "cancelled: " + c.reason }

// take runs one instance from where it was last written, on the version
// newest when it begins. On a store several daemons share, it holds the
// instance's lease while it runs, and leaves the instance to the daemon
// that holds it otherwise.
func (r *Runtime) take(id string) error {
	r.swap.RLock()
	defer r.swap.RUnlock()
	leased, shared := r.Store.(Leased)
	if !shared {
		return r.takeHeld(id)
	}
	lease := r.Lease
	if lease <= 0 {
		lease = 30 * time.Second
	}
	now := time.Now()
	held, err := leased.Claim(id, now.UnixMilli(), now.Add(lease).UnixMilli())
	if err != nil || !held {
		return err
	}
	// Held again, while it runs, long before the lease runs out
	done := make(chan struct{})
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				if held, err := leased.Claim(id, now.UnixMilli(), now.Add(lease).UnixMilli()); err == nil && !held {
					// Another took it: its next save is refused
					return
				}
			}
		}
	}()
	err = r.takeHeld(id)
	close(done)
	<-renewed
	if errors.Is(err, ErrNotHeld) {
		// Another daemon runs it now, from its last write
		return nil
	}
	if unclaimed := leased.Unclaim(id); err == nil {
		err = unclaimed
	}
	return err
}

// takeHeld is take, the version held already: an instance moved at a
// stage is taken up again in the same hold.
func (r *Runtime) takeHeld(id string) error {
	inst, ok := r.Store.Get(id)
	if !ok {
		return fmt.Errorf("no instance %s", id)
	}
	if !(inst.Status == Running || (inst.Status == Waiting && inst.WakeAt <= r.Now())) {
		// No longer due: another worker, or a write since it was found
		// due, took care of it
		return nil
	}
	switch {
	case inst.Phase == Finish:
		return r.finish(inst)
	case inst.Cancel != "" && !inst.CancelTaken:
		if !r.listens(inst) {
			return r.ending(inst, nil)
		}
	}
	chunk, err := r.chunkOf(inst)
	if err != nil {
		stopped := inst.copy()
		stopped.Status, stopped.Stopped = Stopped, err.Error()
		return r.Store.Save(stopped)
	}
	counts := copyCounts(inst.Counts)
	tries := copyCounts(inst.Tries)
	retried := copyMoments(inst.Retried)
	working := copyMoments(inst.Working)
	undos := append([]vm.Undo(nil), inst.Undos...)
	children := append([]string(nil), inst.Children...)
	pass, atStage := inst.Pass, inst.AtStage
	// A replay: what was kept since the stage, and what it took of it
	replaying, taken := inst.Replaying, map[string]bool{}
	// What its tasks drew since the stage, and what a replay gives back
	drawn, giving := copyDrawn(inst.Drawn), copyDrawn(inst.Giving)

	machine := vm.NewVM()
	machine.SetClock(r.Now)
	machine.SetStore(r.database)
	if out := r.outOf(inst.ID); out != nil {
		machine.SetOutput(out)
	}
	var fault error // a write the store refused: the process stops here
	var taking []Taken
	flowHost := &host{r: r, inst: inst, children: &children, fault: &fault, taking: &taking, pass: &pass}
	machine.SetFlowHost(flowHost)
	var queues *attempting
	if r.Queues != nil {
		queues = &attempting{r: r, inst: id}
		machine.SetQueues(queues)
	}

	// The undos the flow's tasks noted come before those of a step whose
	// result they then take: in the order the flow noted them
	noted := func(m *vm.VM, fromStep []vm.Undo) {
		undos = append(undos, m.TakeUndos("")...)
		undos = append(undos, fromStep...)
	}

	// written is the instance as it is now, frozen
	written := func(m *vm.VM, status Status, wakeAt int64) (*Instance, error) {
		noted(m, nil)
		state, err := m.Freeze()
		if err != nil {
			return nil, err
		}
		w := inst.copy()
		w.Status, w.State, w.WakeAt = status, state, wakeAt
		w.Counts, w.Tries, w.Retried, w.Working = copyCounts(counts), copyCounts(tries), copyMoments(retried), copyMoments(working)
		w.Undos = append([]vm.Undo(nil), undos...)
		w.Children, w.Awaits = append([]string(nil), children...), m.Awaited()
		w.Pass, w.AtStage, w.Replaying = pass, atStage, replaying
		w.Drawn, w.Giving = copyDrawn(drawn), copyDrawn(giving)
		w.Taking, taking = taking, nil
		w.CancelTaken = inst.CancelTaken || flowHost.cancelTaken
		return w, nil
	}

	// The end of a replay, at the next stage or at the end: what the old
	// code had done and the new one has not asked for stops the instance
	replayed := func() error {
		if !replaying {
			return nil
		}
		replaying, giving = false, nil
		if left := r.untaken(inst, pass, taken); len(left) > 0 {
			return &vm.Stop{Reason: fmt.Errorf("%s: %s did something its new version does not account for: %s was kept, and nothing asked for it again", ErrUntaken, inst.ID, strings.Join(left, ", "))}
		}
		return nil
	}

	// A stage: the counts start again, and the keys say which pass they
	// are of; run alone there, with only data alive, the instance can be
	// taken up from it, kept with the next write
	machine.SetOnStage(func(m *vm.VM, name string) error {
		if err := replayed(); err != nil {
			return err
		}
		pass++
		counts = map[string]int{}
		if !m.Alone() {
			return nil
		}
		vars, err := m.StageData()
		if err != nil {
			return nil
		}
		noted(m, nil)
		atStage = &AtStage{Name: name, Pass: pass, Vars: vars, Undos: len(undos)}
		// What a replay from here gives back is drawn from here on
		drawn, giving = nil, nil
		if chunk == r.Chunk {
			return nil
		}
		// A newer version that has this stage as it is here: the instance
		// goes on there, from it. One that changed it, with a migration
		// for it: the instance goes on from the stage the migration gives
		switch {
		case sameShape(chunk, r.Chunk, inst.Flow, name):
		default:
			moved, err := r.migrate(chunk, inst.Flow, atStage)
			if err != nil {
				return err
			}
			if moved == nil {
				return nil
			}
			atStage = moved
		}
		noted(m, nil)
		return &vm.Stop{Reason: &moving{}}
	})

	// The time read and the random numbers drawn without a step: kept, by
	// task, and in a replay given back in the order they were drawn, as
	// long as the new code draws them as the old one did. Each task draws
	// in the order of its own code, whatever turns the tasks take; the
	// first value asked otherwise, or not drawn before, ends what the
	// task is given back, and from there it draws new ones. A step draws
	// for the world: what it gives is kept, not what it drew
	machine.SetOnDraw(func(m *vm.VM, fresh vm.Drawn) vm.Drawn {
		if m.InStep() {
			return fresh
		}
		task := m.TaskName()
		value := fresh
		if left := giving[task]; len(left) > 0 {
			if left[0].Of == fresh.Of {
				value = left[0]
				giving[task] = left[1:]
			}
			if left[0].Of != fresh.Of || len(left) == 1 {
				delete(giving, task)
			}
		}
		if drawn == nil {
			drawn = map[string][]vm.Drawn{}
		}
		drawn[task] = append(drawn[task], value)
		return value
	})

	// The steps asked for since the last write: they leave together
	type leaving struct {
		call *vm.StepCall
		key  string
	}
	var wave []leaving
	underway := 0

	machine.SetOnStep(func(m *vm.VM, call *vm.StepCall) (vm.Value, bool, error) {
		if m.InStep() {
			// A step called from a step is a plain call
			return vm.NewNull(), false, nil
		}
		asked := m.TaskName() + ":" + call.Name
		n := counts[asked] + 1
		key := Key(inst, pass, m.TaskName(), call.Name, n)
		for {
			if kept, found := r.Store.Result(key); found {
				if replaying && kept.Asked != nil {
					if now := askedOf(m, call.Args); now != nil && string(now) != string(kept.Asked) {
						return vm.NewNull(), false, &vm.Stop{Reason: fmt.Errorf("%s: %s asks %s otherwise than it was asked under %s: %s, not %s; the world answered a question the new code does not ask", ErrAskedOtherwise, inst.ID, call.Name, key, now, kept.Asked)}
					}
				}
				taken[key] = true
				if r.dry != nil {
					r.dry.Took = append(r.dry.Took, key)
				}
				counts[asked] = n
				noted(m, kept.Undos)
				v, err := m.ValueOfData(kept.Data)
				return v, true, err
			}
			if until, waits := working[key]; waits {
				// The application answers it later: the task waits for the
				// answer until its time is up, out of memory meanwhile
				a, _ := r.Store.Attempt(key)
				name := call.Name[strings.LastIndex(call.Name, ".")+1:]
				var answer vm.Answer
				switch {
				case a.State == Answered:
					answer, _ = readAnswer(a.Answer)
				case r.Now() < until:
					if err := m.AwaitUntil("answer:"+key, until); err != nil {
						return vm.NewNull(), false, err
					}
					continue
				default:
					// Given up, unless its answer landed first
					now, err := r.Store.Mark(key, a.Attempt, GivenUp, name+" gave no answer before its time was up")
					if err != nil {
						fault = err
						return vm.NewNull(), false, err
					}
					if now.State == Answered {
						continue
					}
					answer = vm.Answer{Outcome: vm.Uncertain, Reason: name + " gave no answer before its time was up", After: 1000}
				}
				delete(working, key)
				data, fromStep, retry, err := vm.Settle(name, call.Once, answer, a.Undo, key)
				switch {
				case err != nil:
					return vm.NewNull(), false, err
				case retry != nil:
					if tries[key] >= r.MaxTries {
						return vm.NewNull(), false, fmt.Errorf("%s asked to be run again %d times, and was not: %s", call.Name, tries[key], retry.Reason)
					}
					retried[key] = r.Now() + retry.After
					continue
				}
				if err := r.Store.PutResult(key, Kept{Data: data, Undos: fromStep, Asked: askedOf(m, call.Args)}); err != nil {
					fault = err
					return vm.NewNull(), false, err
				}
				continue
			}
			at, again := retried[key]
			if call.Once && tries[key] > 0 && !again && call.ByQueue && r.keptOrNeverSent(key, tries[key]) {
				// By a queue: it never left, its attempt not written before
				// the process stopped; or it left by a queue that keeps its
				// answers, sent again under once, asked of under started
				retried[key], at, again = r.Now(), r.Now(), true
			}
			if call.Once && tries[key] > 0 && !again {
				// It left once, and nothing says what came of it: it is not
				// run again, and the flow is told so
				taken[key] = true
				if err := r.Store.PutResult(key, Kept{Data: []byte(Unknown), Asked: askedOf(m, call.Args)}); err != nil {
					fault = err
					return vm.NewNull(), false, err
				}
				counts[asked] = n
				v, err := m.ValueOfData([]byte(Unknown))
				return v, true, err
			}
			if again && at > r.Now() {
				// Its next attempt is not due: the task sleeps until it is
				if err := m.SleepUntil(at); err != nil {
					return vm.NewNull(), false, err
				}
				continue
			}

			wave = append(wave, leaving{call, key})
			v, err := call.Wait()
			if queues != nil && queues.err() != nil {
				fault = queues.err()
				return vm.NewNull(), false, fault
			}
			var later *vm.WorkingError
			if errors.As(err, &later) {
				working[key] = later.Until
				continue
			}
			var held *vm.HeldError
			if errors.As(err, &held) {
				// It did not leave, or did not arrive: the same attempt
				// leaves again once its queue is ready, the task asleep
				tries[key]--
				retried[key] = r.Now() + held.After
				continue
			}
			var retry *vm.RetryError
			if errors.As(err, &retry) {
				if tries[key] >= r.MaxTries {
					return vm.NewNull(), false, fmt.Errorf("%s asked to be run again %d times, and was not: %s", call.Name, tries[key], retry.Reason)
				}
				retried[key] = r.Now() + retry.After
				continue
			}
			if err != nil {
				return vm.NewNull(), false, err
			}
			counts[asked] = n
			return v, true, nil
		}
	})

	// What a step gave: kept at once, with what it gave flow.undo, then
	// given to the task that waits
	// What it wrote with flow.store() is committed with it, or rolled
	// back when it gave no result
	stepped := func(m *vm.VM, s leaving, v vm.Value, fromStep []vm.Undo, err error) error {
		underway--
		tx, ended := m.TakeStoreTx(s.key)
		if ended {
			// The database ended its transaction for another: nothing it
			// wrote is there, and it runs again, whatever it gave
			rollback(tx)
			s.call.Give(vm.NewNull(), endedByTheDatabase())
			return nil
		}
		if err != nil {
			rollback(tx)
			s.call.Give(vm.NewNull(), err)
			return nil
		}
		data, err := m.DataOf(v)
		if err != nil {
			rollback(tx)
			s.call.Give(vm.NewNull(), fmt.Errorf("what %s returned: %v", s.call.Name, err))
			return nil
		}
		if err := r.keep(tx, s.key, Kept{Data: data, Undos: fromStep, Asked: askedOf(m, s.call.Args)}); err != nil {
			if again := r.againAfter(err); again != nil {
				// Ended by the database for another transaction: nothing
				// of it was kept, and it runs again
				s.call.Give(vm.NewNull(), again)
				return nil
			}
			fault = err
			return err
		}
		taken[s.key] = true
		undos = append(undos, fromStep...)
		s.call.Give(v, nil)
		return nil
	}

	// No task can go on: the steps asked for leave, written first, or the
	// instance sleeps until the clock wakes one of its tasks. A
	// cancellation asked meanwhile is found here, before anything leaves
	machine.SetOnIdle(func(m *vm.VM) error {
		if now, ok := r.Store.Get(id); ok && now.Cancel != "" && !inst.CancelTaken && !flowHost.cancelTaken {
			// A flow that waits for its cancellation is given it, at its
			// wait match; while its steps leave or come back it goes on.
			// One that waits elsewhere, or does not listen, stops here
			given := false
			if chunk.Prototypes[chunk.FindPrototype(inst.Flow+".run")].Listens {
				for _, name := range m.Awaited() {
					if name == "cancel:" {
						m.Arrived(name)
						given = true
					}
				}
			}
			switch {
			case given:
				return nil
			case chunk.Prototypes[chunk.FindPrototype(inst.Flow+".run")].Listens && (len(wave) > 0 || underway > 0):
			default:
				noted(m, nil)
				return &cancelled{now.Cancel}
			}
		}
		// The instances its tasks wait for that have ended wake them
		arrived := false
		for _, name := range m.Awaited() {
			if name == "cancel:" {
				// Given above, when there is one to give
				continue
			}
			if key, ok := strings.CutPrefix(name, "answer:"); ok {
				if a, found := r.Store.Attempt(key); found && a.State == Answered {
					m.Arrived(name)
					arrived = true
				}
				continue
			}
			if strings.HasPrefix(name, "inbox:") {
				if flowHost.hasMessage(name) {
					m.Arrived(name)
					arrived = true
				}
				continue
			}
			if _, ended, _ := r.endedAs(name); ended {
				m.Arrived(name)
				arrived = true
			}
		}
		if arrived {
			return nil
		}
		if r.dry != nil {
			// Nothing leaves in a dry run: what would is said
			for _, s := range wave {
				r.dry.Next = append(r.dry.Next, dryNext(m, s.call))
			}
			if len(wave) == 0 {
				switch wake := m.NextWake(); {
				case len(m.Awaited()) > 0:
					r.dry.Next = append(r.dry.Next, "it would wait for "+strings.Join(m.Awaited(), ", "))
				case wake >= 0:
					r.dry.Next = append(r.dry.Next, "it would sleep until "+time.UnixMilli(wake).UTC().Format(time.RFC3339))
				}
			}
			return &vm.Stop{Reason: errDry}
		}
		if len(wave) > 0 {
			for _, s := range wave {
				tries[s.key]++
				delete(retried, s.key)
			}
			w, err := written(m, Running, 0)
			if err != nil {
				return err
			}
			if err := r.Store.Save(w); err != nil {
				fault = err
				return err
			}
			for _, s := range wave {
				if r.Ran != nil {
					r.Ran(s.call.Name, s.key, s.call.Once)
				}
				underway++
				if replaced, ok := r.Replace[s.call.Name]; ok {
					v, fromStep, err := replaced(m, s.call.Args)
					if err := stepped(m, s, v, fromStep, err); err != nil {
						return err
					}
					continue
				}
				s := s
				if chunk != r.Chunk {
					// The newest version's, if it has the step as it was
					s.call.RunIn(r.Chunk)
				}
				m.RunStep(s.call, s.key, tries[s.key], func(m *vm.VM, v vm.Value, err error) error {
					return stepped(m, s, v, m.TakeUndos(s.key), err)
				})
			}
			wave = nil
			return nil
		}
		if underway > 0 {
			return fmt.Errorf("a step waits for something that nothing will give")
		}
		wake := m.NextWake()
		if wake < 0 && len(m.Awaited()) == 0 {
			// Nothing will ever wake it: the machine says where it is stuck
			return nil
		}
		if wake < 0 {
			wake = Never
		}
		w, err := written(m, Waiting, wake)
		if err != nil {
			return err
		}
		if err := r.Store.Save(w); err != nil {
			fault = err
			return err
		}
		return &vm.FrozenError{State: w.State}
	})

	var result vm.Value
	switch {
	case inst.State == nil && inst.AtStage != nil:
		// Taken up from its stage: moved to this version there
		result, err = machine.StartAtStage(chunk, inst.Flow+".run", inst.AtStage.Name, inst.AtStage.Vars)
	case inst.State == nil:
		result, err = machine.Start(chunk, inst.Flow+".run", inst.Fields)
	default:
		result, err = machine.Thaw(chunk, inst.State)
	}

	at := inst.copy()
	at.Counts, at.Tries, at.Retried, at.Working = copyCounts(counts), copyCounts(tries), copyMoments(retried), copyMoments(working)
	at.Children, at.Awaits = children, nil
	at.Pass, at.AtStage, at.Replaying = pass, atStage, replaying
	at.Drawn, at.Giving = drawn, giving
	at.Taking = taking
	at.CancelTaken = inst.CancelTaken || flowHost.cancelTaken
	if err == nil {
		// Run ended: a replay ends here too
		err = replayed()
		at.Replaying, at.Giving = replaying, giving
	}
	var frozen *vm.FrozenError
	var stop *cancelled
	var move *moving
	switch {
	case fault != nil:
		return fault
	case errors.As(err, &frozen):
		return nil // asleep, written
	case errors.Is(err, errDry):
		return errDry
	case errors.As(err, &move):
		// Written at its stage, in the newest version, and taken up there;
		// its fields those it has there, migrated if they were
		at.Undos = undos
		if atStage != nil && atStage.Vars["self"] != nil {
			at.Fields = atStage.Vars["self"]
		}
		at.Status, at.State, at.WakeAt, at.Version = Running, nil, 0, r.newest
		if err := r.Store.Save(at); err != nil {
			return err
		}
		return r.takeHeld(id)
	case errors.As(err, &stop):
		at.Undos = undos
		return r.ending(at, nil)
	case err != nil:
		// A mistake: nothing is undone, unless a person cancels it
		noted(machine, nil)
		at.Status, at.Stopped, at.Undos = Stopped, err.Error(), undos
		return r.Store.Save(at)
	}
	data, derr := machine.DataOf(result)
	if derr != nil {
		return derr
	}
	noted(machine, nil)
	at.Undos = undos
	if isErr(data) && (len(undos) > 0 || len(children) > 0) {
		return r.ending(at, data)
	}
	at.Status, at.Result, at.State, at.Undos = Done, data, nil, nil
	return r.Store.Save(at)
}

// The two ways a replay goes wrong (see docs/flow.md, "Taking up a
// stopped instance").
const (
	ErrUntaken        = "F0101" // something done that the new code does not ask for
	ErrAskedOtherwise = "F0102" // a step asked otherwise than it was
)

// askedOf is what a step is asked, as data: its arguments, an array.
func askedOf(m *vm.VM, args []vm.Value) []byte {
	parts := make([]json.RawMessage, len(args))
	for i, arg := range args {
		data, err := m.DataOf(arg)
		if err != nil {
			return nil
		}
		parts[i] = data
	}
	data, _ := json.Marshal(parts)
	return data
}

// untaken are the steps of a pass whose results were kept, and that a
// replay of it did not take: undos are not among them.
func (r *Runtime) untaken(inst *Instance, pass int, taken map[string]bool) []string {
	var left []string
	for _, key := range r.Store.Keys(fmt.Sprintf("%s/%s/%d:", inst.ID, inst.Birth, pass)) {
		if !taken[key] && !strings.Contains(key, ":finish:") {
			left = append(left, key)
		}
	}
	return left
}

// isErr says whether what run returned is an Err of std.result.
func isErr(data []byte) bool {
	var v struct {
		Type string `json:"$type"`
	}
	return json.Unmarshal(data, &v) == nil && v.Type == "std.result.Result.Err"
}

// ending writes an instance that fails, run having returned the Err
// given, or nil for a cancellation, then runs its undos.
func (r *Runtime) ending(inst *Instance, err []byte) error {
	w := inst.copy()
	w.Phase, w.Status, w.Result, w.State, w.Stopped, w.WakeAt = Finish, Running, err, nil, "", r.Now()
	if err := r.Store.Save(w); err != nil {
		return err
	}
	return r.finish(w)
}

// finish runs the undos of an instance that is ending, the last noted
// first, each as a step under its key: one that ran already is not run
// again, one that asks to be run again puts the instance to sleep. Then
// the instance ends: done with the Err run returned, or failed when it
// was cancelled.
func (r *Runtime) finish(inst *Instance) error {
	w := inst.copy()

	// Its children first: cancelled, and waited for, so that their undos
	// run before its own
	var left []string
	for _, child := range w.Children {
		c, ok := r.Store.Get(child)
		if !ok || c.ended() {
			continue
		}
		if err := r.Store.Cancel(child, "its parent "+w.ID+" is ending", r.Now()); err != nil {
			return err
		}
		left = append(left, child)
	}
	if len(left) > 0 {
		w.Status, w.WakeAt, w.Awaits = Waiting, Never, left
		return r.Store.Save(w)
	}
	w.Awaits = nil

	for n := 1; n <= len(w.Undos); n++ {
		undo := w.Undos[len(w.Undos)-n]
		key := Key(w, w.Pass, "finish", "undo", n)
		kept, found := r.Store.Result(key)
		data := kept.Data
		if !found {
			if at, again := w.Retried[key]; again && at > r.Now() {
				w.Status, w.WakeAt = Waiting, at
				return r.Store.Save(w)
			}
			if w.Tries[key] >= r.MaxTries {
				return r.stopFinishing(w, fmt.Sprintf("%s asked to be run again %d times, and has not undone", undo.Step, w.Tries[key]))
			}
			if r.dry != nil {
				r.dry.Next = append(r.dry.Next, fmt.Sprintf("undo %s %s", undo.Step, undo.Argument))
				return errDry
			}
			// Written before it leaves, as any step
			w.Tries[key]++
			delete(w.Retried, key)
			w.Status = Running
			if err := r.Store.Save(w); err != nil {
				return err
			}
			if r.Ran != nil {
				r.Ran(undo.Step, key, false)
			}
			var err error
			var tx *sql.Tx
			data, tx, err = r.undo(w, undo, key, w.Tries[key])
			var retry *vm.RetryError
			var fault *refused
			var held *vm.HeldError
			switch {
			case errors.As(err, &fault):
				// The store refused a write: the process stops here
				return fault.err
			case errors.As(err, &held):
				// Its queue is not ready for it: the same attempt, later
				w.Tries[key]--
				w.Retried[key] = r.Now() + held.After
				w.Status, w.WakeAt = Waiting, w.Retried[key]
				return r.Store.Save(w)
			case errors.As(err, &retry):
				w.Retried[key] = r.Now() + retry.After
				w.Status, w.WakeAt = Waiting, w.Retried[key]
				return r.Store.Save(w)
			case err != nil:
				return r.stopFinishing(w, fmt.Sprintf("%s could not undo: %v", undo.Step, err))
			}
			if err := r.keep(tx, key, Kept{Data: data, Asked: undo.Argument}); err != nil {
				if again := r.againAfter(err); again != nil {
					w.Retried[key] = r.Now() + again.After
					w.Status, w.WakeAt = Waiting, w.Retried[key]
					return r.Store.Save(w)
				}
				return err
			}
		}
		if isErr(data) {
			return r.stopFinishing(w, fmt.Sprintf("%s could not undo: %s", undo.Step, data))
		}
	}
	w.Status, w.Undos, w.WakeAt = Done, nil, 0
	if w.Result == nil {
		w.Status = Failed
	}
	return r.Store.Save(w)
}

// stopFinishing stops an instance whose undo has not undone: a person
// puts the world right, then resumes or ends it.
func (r *Runtime) stopFinishing(w *Instance, why string) error {
	w.Status, w.Stopped = Stopped, why
	return r.Store.Save(w)
}

// keptOrNeverSent says, of a step by a queue that left once, whether it
// never did, its attempt not written, or did by a queue whose manifest
// keeps its answers, under once or under started: what may have arrived
// of it is then found out, not taken as Unknown.
func (r *Runtime) keptOrNeverSent(key string, attempt int) bool {
	a, ok := r.Store.Attempt(key)
	if !ok || a.Attempt < attempt {
		// Written before it leaves: not written, and it did not; the one
		// before was answered, or it would not have been sent again
		return true
	}
	kept, ok := r.Queues.(interface {
		Kept(queue, shape string) (once, started bool)
	})
	if !ok {
		return false
	}
	once, started := kept.Kept(a.Queue, a.Shape)
	return once || started
}

// refused is a write the store refused while an undo ran: not what the
// undo did, but the process stopping.
type refused struct{ err error }

func (r *refused) Error() string { return r.err.Error() }

// undo runs an undo's step on its own, or what stands for it in a trial.
func (r *Runtime) undo(inst *Instance, u vm.Undo, key string, attempt int) ([]byte, *sql.Tx, error) {
	machine := vm.NewVM()
	machine.SetClock(r.Now)
	machine.SetStore(r.database)
	if out := r.outOf(inst.ID); out != nil {
		machine.SetOutput(out)
	}
	queues := &attempting{r: r, inst: inst.ID}
	if r.Queues != nil {
		machine.SetQueues(queues)
	}
	if replaced, ok := r.Replace[u.Step]; ok {
		arg, err := machine.ValueOfData(u.Argument)
		if err != nil {
			return nil, nil, err
		}
		v, _, err := replaced(machine, []vm.Value{machine.Pin(arg)})
		if err != nil {
			return nil, nil, err
		}
		data, err := machine.DataOf(v)
		return data, nil, err
	}
	// The newest version's step, if it has it as the instance's version
	// did; otherwise the instance's own
	chunk := r.Chunk
	if own, err := r.chunkOf(inst); err == nil && own != r.Chunk && !sameStep(own, r.Chunk, u.Step) {
		chunk = own
	}
	v, err := machine.StartStep(chunk, u.Step, key, attempt, u.Argument)
	tx, ended := machine.TakeStoreTx(key)
	if ended {
		rollback(tx)
		return nil, nil, endedByTheDatabase()
	}
	if fault := queues.err(); fault != nil {
		rollback(tx)
		return nil, nil, &refused{fault}
	}
	var later *vm.WorkingError
	if errors.As(err, &later) {
		rollback(tx)
		return nil, nil, fmt.Errorf("%s answers later, which an undo cannot yet", u.Step)
	}
	if err != nil {
		rollback(tx)
		return nil, nil, err
	}
	data, err := machine.DataOf(v)
	if err != nil {
		rollback(tx)
		return nil, nil, err
	}
	return data, tx, nil
}

// Key is the key of a step's result: the instance, its birth, the
// stages passed, the task, and what was asked with its count.
func Key(inst *Instance, pass int, task, step string, n int) string {
	return fmt.Sprintf("%s/%s/%d:%s:%s#%d", inst.ID, inst.Birth, pass, task, step, n)
}

func copyMoments(moments map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(moments))
	for k, v := range moments {
		out[k] = v
	}
	return out
}

func copyDrawn(drawn map[string][]vm.Drawn) map[string][]vm.Drawn {
	if drawn == nil {
		return nil
	}
	c := make(map[string][]vm.Drawn, len(drawn))
	for task, values := range drawn {
		c[task] = append([]vm.Drawn(nil), values...)
	}
	return c
}

func copyCounts(counts map[string]int) map[string]int {
	out := make(map[string]int, len(counts))
	for k, v := range counts {
		out[k] = v
	}
	return out
}

func birth() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// sortedIDs is for the stores: instances in a stable order.
func sortedIDs(m map[string]*Instance) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
