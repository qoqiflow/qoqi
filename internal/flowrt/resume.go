package flowrt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// ErrStageGone is a stopped instance whose stage the newest version has
// not, as it was, with no migration to take it there.
const ErrStageGone = "F0103"

// Resume takes a stopped instance up again, once a person has put the
// world or the code right.
//
// Stopped while it was ending, an undo having given an Err: that undo
// runs again, as a next attempt, then the ones after it; those that ran
// are found by their keys.
//
// Stopped by a mistake in run: it goes back to its last stage, in the
// newest version, through a migration if that one changed the stage, and
// runs what came after the stage again. What its steps did since is
// found by their keys, and nothing leaves twice; what the new code asks
// otherwise (F0102), or does not ask for (F0101), stops it again, for a
// person to look at. One stopped before its first stage starts again
// from the beginning of run.
func (r *Runtime) Resume(id string) error {
	r.swap.RLock()
	defer r.swap.RUnlock()

	w, forget, err := r.resumed(id)
	if err != nil {
		return err
	}
	for _, key := range forget {
		if err := r.Store.ForgetResult(key); err != nil {
			return err
		}
	}
	if w.untake {
		// What it took since its stage, it takes again
		if err := r.Store.Untake(id, w.Pass); err != nil {
			return err
		}
	}
	return r.Store.Save(w)
}

// resumed is the instance as Resume writes it, and the keys of the undos
// whose Err it forgets.
func (r *Runtime) resumed(id string) (*Instance, []string, error) {
	inst, ok := r.Store.Get(id)
	if !ok {
		return nil, nil, fmt.Errorf("there is no instance %s", id)
	}
	if inst.Status != Stopped {
		return nil, nil, fmt.Errorf("%s is %s, not stopped: only a stopped instance is resumed", id, inst.Status)
	}
	w := inst.copy()
	w.Stopped, w.Status, w.WakeAt = "", Waiting, 0

	if w.Phase == Finish {
		var forget []string
		for n := 1; n <= len(w.Undos); n++ {
			key := Key(w, w.Pass, "finish", "undo", n)
			if kept, ok := r.Store.Result(key); ok && isErr(kept.Data) {
				forget = append(forget, key)
			}
			delete(w.Retried, key)
		}
		return w, forget, nil
	}

	newest, err := r.newestVersion()
	if err != nil {
		return nil, nil, err
	}
	at := w.AtStage
	if at != nil && w.Version != "" && w.Version != newest {
		old, err := r.chunkOf(inst)
		if err != nil {
			return nil, nil, err
		}
		if !sameShape(old, r.Chunk, w.Flow, at.Name) {
			moved, err := r.migrate(old, w.Flow, at)
			if err != nil {
				return nil, nil, err
			}
			if moved == nil {
				return nil, nil, fmt.Errorf("%s: stage %s of %s is not in the newest version as it was, and no migrate fn takes an instance there: cancel %s, or serve the version it runs", ErrStageGone, at.Name, w.Flow, id)
			}
			at = moved
		}
	}
	w.Version, w.State, w.Counts, w.Replaying = newest, nil, map[string]int{}, true
	w.untake = true
	// What its tasks drew since the stage, they are given back, in order:
	// what a replay stopped before giving back follows what it drew
	w.Giving = copyDrawn(w.Drawn)
	for task, left := range inst.Giving {
		if w.Giving == nil {
			w.Giving = map[string][]vm.Drawn{}
		}
		w.Giving[task] = append(w.Giving[task], left...)
	}
	w.Drawn = nil
	if at != nil {
		w.AtStage, w.Pass = at, at.Pass
		if at.Vars["self"] != nil {
			w.Fields = at.Vars["self"]
		}
		if at.Undos <= len(w.Undos) {
			// Those noted after the stage are noted again
			w.Undos = w.Undos[:at.Undos]
		}
	} else {
		// Before its first stage: from the beginning of run
		w.Pass, w.Undos = 0, nil
	}
	return w, nil, nil
}

// End ends a stopped instance as it is, with no more undos: for when a
// person has put the world right by hand. One stopped as it was ending
// ends as it would have, done with the Err its run returned or failed
// for its cancellation; one stopped by a mistake fails. Its children
// still alive are cancelled.
func (r *Runtime) End(id, reason string) error {
	r.swap.RLock()
	defer r.swap.RUnlock()

	inst, ok := r.Store.Get(id)
	if !ok {
		return fmt.Errorf("there is no instance %s", id)
	}
	if inst.Status != Stopped {
		return fmt.Errorf("%s is %s, not stopped: only a stopped instance is ended", id, inst.Status)
	}
	if reason == "" {
		reason = "a person ended it"
	}
	for _, child := range inst.Children {
		if c, ok := r.Store.Get(child); ok && !c.ended() {
			if err := r.Store.Cancel(child, "its parent "+id+" was ended", r.Now()); err != nil {
				return err
			}
		}
	}
	w := inst.copy()
	w.Undos, w.State, w.Awaits, w.WakeAt, w.Ended = nil, nil, nil, 0, reason
	w.Status = Failed
	if w.Phase == Finish && w.Result != nil {
		w.Status = Done
	} else {
		w.Result = nil
	}
	return r.Store.Save(w)
}

// DryRun is what resuming an instance would do, run in memory and
// stopped before anything leaves.
type DryRun struct {
	// Took are the steps it would find done, by their keys
	Took []string `json:"took"`
	// Next is what it would do then: the steps that would leave, and what
	// they would be asked, or how it would wait, or how it would end
	Next []string `json:"next"`
	// Forget are the undos whose Err it would forget, to run them again
	Forget []string `json:"forget,omitempty"`
	// Stopped is what would stop it again, if anything would
	Stopped string `json:"stopped,omitempty"`
}

// errDry stops a dry run where something would leave.
var errDry = errors.New("a dry run stops here")

// DryResume runs what Resume would, in memory: the instance resumed, up
// to the first step that would leave or the first wait that would wait.
// Nothing is written, and no step runs.
func (r *Runtime) DryResume(id string) (*DryRun, error) {
	r.swap.RLock()
	defer r.swap.RUnlock()

	w, forget, err := r.resumed(id)
	if err != nil {
		return nil, err
	}
	report := &DryRun{Forget: forget}
	store := &dryStore{Store: r.Store, instances: map[string]*Instance{id: w}, forgotten: map[string]bool{}}
	for _, key := range forget {
		store.forgotten[key] = true
	}
	dry := &Runtime{Chunk: r.Chunk, Store: store, Out: io.Discard, Now: r.Now, MaxTries: r.MaxTries, Replace: r.Replace, dry: report}
	if err := dry.take(id); err != nil && !errors.Is(err, errDry) {
		return nil, err
	}
	if after, ok := store.instances[id]; ok && after.Status == Stopped {
		report.Stopped = after.Stopped
	}
	if after, ok := store.instances[id]; ok && len(report.Next) == 0 {
		switch after.Status {
		case Done:
			report.Next = append(report.Next, "it would end: "+string(after.Result))
		case Failed:
			report.Next = append(report.Next, "it would end, failed")
		}
	}
	return report, nil
}

// dryStore reads a store and keeps what is written to it to itself.
type dryStore struct {
	Store
	instances map[string]*Instance
	forgotten map[string]bool
}

func (d *dryStore) Get(id string) (*Instance, bool) {
	if inst, ok := d.instances[id]; ok {
		return inst.copy(), true
	}
	return d.Store.Get(id)
}

func (d *dryStore) Save(inst *Instance) error {
	d.instances[inst.ID] = inst.copy()
	return nil
}

func (d *dryStore) Create(inst *Instance) (bool, *Instance, error) {
	if existing, ok := d.Get(inst.ID); ok {
		return false, existing, nil
	}
	d.instances[inst.ID] = inst.copy()
	return true, nil, nil
}

func (d *dryStore) Result(key string) (Kept, bool) {
	if d.forgotten[key] {
		return Kept{}, false
	}
	return d.Store.Result(key)
}

func (d *dryStore) PutResult(string, Kept) error { return nil }
func (d *dryStore) PutAttempt(Attempt) error     { return nil }
func (d *dryStore) Mark(key string, attempt int, _ AttemptState, _ string) (Attempt, error) {
	a, _ := d.Store.Attempt(key)
	return a, nil
}
func (d *dryStore) ForgetResult(string) error          { return nil }
func (d *dryStore) Cancel(string, string, int64) error { return nil }
func (d *dryStore) PutVersion(string, []byte) error    { return nil }
func (d *dryStore) Untake(string, int) error           { return nil }
func (d *dryStore) Send(string, string, string, []byte, int) (bool, error) {
	return false, nil
}

// dryNext says, in a dry run, what would leave: a step and what it would
// be asked.
func dryNext(m *vm.VM, call *vm.StepCall) string {
	return fmt.Sprintf("%s %s", call.Name, askedOf(m, call.Args))
}

// AnswerByHand is a person's answer to a step of a stopped instance that
// the application could not tell of, its process having died as it acted:
// what the step did, as data at the boundary, which the instance takes
// once resumed; or, nothing, that it did nothing, and is to be sent again,
// the application dropping its dead claim.
func (r *Runtime) AnswerByHand(id, key string, value json.RawMessage, nothing bool) error {
	r.swap.RLock()
	defer r.swap.RUnlock()

	inst, ok := r.Store.Get(id)
	if !ok {
		return fmt.Errorf("there is no instance %s", id)
	}
	if inst.Status != Stopped {
		return fmt.Errorf("%s is %s, not stopped: a step is answered by hand only for a stopped instance", id, inst.Status)
	}
	if !strings.HasPrefix(key, id+"/") {
		return fmt.Errorf("%s is not the key of a step of %s", key, id)
	}
	a, sent := r.Store.Attempt(key)
	if nothing {
		if !sent {
			return fmt.Errorf("no attempt of %s was sent: there is nothing to reclaim", key)
		}
		_, err := r.Store.Mark(key, a.Attempt, Nothing, "a person said it did nothing")
		return err
	}
	if !json.Valid(value) {
		return fmt.Errorf("what the step did is JSON, as the application would have answered it")
	}
	data := []byte(value)
	if r.Boundary != nil && sent {
		read := r.Boundary.Answer(a.Step, a.Undo, vm.Answer{Outcome: vm.Answered, Value: value})
		if read.Outcome != vm.Answered {
			return fmt.Errorf("%s", read.Reason)
		}
		data = read.Value
	}
	return r.Store.PutResult(key, Kept{Data: data})
}
