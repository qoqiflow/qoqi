package flowrt

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Moving an instance that waits: an instance on an older version goes on
// in the newest, while it waits, when the newest, played again from its
// last stage, does what the instance did since: asks its steps for what
// they were asked, in the same order, and takes the messages it took.
// What the instance did is then something the newest version did, and it
// goes on from there, in that version only: it never runs half of one
// and half of another. Played again, it finds what its steps gave by
// their keys, and what it read of the clock and drew at random is given
// back, so that a wait for a time is the wait it was, to the same moment.
//
// A deploy asks it of each instance alive on an older version, and the
// runtime of each it takes up, before it runs it: one that waits does not
// run the code of the version before when it wakes. One whose newest
// version does otherwise stays on its own until its next stage, as it
// would have; why is said.

// Move is what became of an instance on an older version, asked to go on
// in the newest.
type Move struct {
	ID    string `json:"id"`
	Flow  string `json:"flow"`
	From  string `json:"from"` // the version it ran
	Moved bool   `json:"moved"`
	// Why it stays on its version, until its next stage, when it does
	Why string `json:"why,omitempty"`
}

// running is why an instance taken up elsewhere stays: it moves when it
// is taken up next, or at its next stage.
const running = "it is running: it moves at its next stage, or when it is taken up next"

// MoveAll asks each instance alive on an older version to move to the
// newest, as Move does: dry, it only says which would.
func (r *Runtime) MoveAll(dry bool) []Move {
	newest, err := r.Version()
	if err != nil {
		return nil
	}
	var moves []Move
	for _, inst := range r.Store.List("", "") {
		if inst.ended() || inst.Version == "" || inst.Version == newest {
			continue
		}
		m, err := r.Move(inst.ID, dry)
		if err != nil {
			m = Move{ID: inst.ID, Flow: inst.Flow, From: inst.Version, Why: err.Error()}
		}
		moves = append(moves, m)
	}
	return moves
}

// WouldMove says which instances of a store would move to a version of
// their program, before it is deployed: nothing is written.
func WouldMove(chunk *vm.Chunk, store Store) []Move {
	dry := New(chunk, &dryStore{Store: store, instances: map[string]*Instance{}, forgotten: map[string]bool{}}, io.Discard)
	return dry.MoveAll(true)
}

// Move takes an instance on an older version to the newest, while it
// waits, when the newest version does what it did since its last stage
// (see above): written there, it is due at once, and the worker that
// takes it up plays it again from the stage, in the newest version, and
// goes on. Dry, it only says whether it would.
func (r *Runtime) Move(id string, dry bool) (Move, error) {
	r.swap.RLock()
	defer r.swap.RUnlock()
	if dry {
		return r.moved(id, true)
	}
	// Held, in this process and on a store several daemons share, so that
	// no worker takes it up meanwhile
	if !r.hold(id) {
		return Move{ID: id, Why: running}, nil
	}
	defer r.release(id)
	if leased, shared := r.Store.(Leased); shared {
		now := time.Now()
		held, err := leased.Claim(id, now.UnixMilli(), now.Add(r.lease()).UnixMilli())
		if err != nil {
			return Move{ID: id}, err
		}
		if !held {
			return Move{ID: id, Why: running}, nil
		}
		defer leased.Unclaim(id)
	}
	return r.moved(id, false)
}

// moved is Move, the instance held.
func (r *Runtime) moved(id string, dry bool) (Move, error) {
	inst, ok, err := r.Store.Get(id)
	if err != nil {
		return Move{ID: id}, err
	}
	if !ok {
		return Move{ID: id}, fmt.Errorf("there is no instance %s", id)
	}
	m := Move{ID: id, Flow: inst.Flow, From: inst.Version}
	w, why, err := r.movable(inst)
	switch {
	case err != nil:
		return m, err
	case why != "":
		m.Why = why
		return m, nil
	}
	if !dry {
		// Taken back to its stage, in the newest version, and what it took
		// since given back, in one write
		if err := r.Store.Save(w); err != nil {
			return m, err
		}
	}
	m.Moved = true
	return m, nil
}

// moveBehind, as a worker takes an instance up, moves it first if it
// waits on an older version and may: it does not run the code of the
// version before. One that may not is taken up on its own version.
func (r *Runtime) moveBehind(id string) error {
	if r.dry != nil {
		return nil
	}
	inst, ok, err := r.Store.Get(id)
	if err != nil || !ok || inst.Status != Waiting {
		return err
	}
	newest, err := r.newestVersion()
	if err != nil || inst.Version == "" || inst.Version == newest {
		return err
	}
	w, why, err := r.movable(inst)
	if err != nil || why != "" {
		// Not moved: it goes on in its own version, as it would have
		return nil
	}
	return r.Store.Save(w)
}

// movable is an instance on an older version, taken back to its last
// stage in the newest, as it is to be written to move there; or why it
// may not move.
func (r *Runtime) movable(inst *Instance) (*Instance, string, error) {
	newest, err := r.newestVersion()
	if err != nil {
		return nil, "", err
	}
	switch {
	case inst.Version == "" || inst.Version == newest:
		return nil, "it runs the newest version", nil
	case inst.ended():
		return nil, "it has ended", nil
	case inst.Status == Running:
		return nil, running, nil
	case inst.Status == Stopped:
		return nil, "it is stopped: qoqi resume takes it up in the newest version", nil
	case inst.Phase == Finish:
		return nil, "it is ending: its undos run on its version", nil
	case inst.Cancel != "":
		return nil, "it is being cancelled", nil
	case r.isAside(inst.ID):
		return nil, "it is put aside after a fault", nil
	case inst.AtStage == nil && inst.Pass > 0, inst.AtStage != nil && inst.AtStage.Pass != inst.Pass:
		// What it did since its last stage alone is not all kept
		return nil, "it passed a stage while other tasks of it ran: it moves at a stage where run is alone", nil
	}
	from := "its start"
	if inst.AtStage != nil {
		from = "stage " + inst.AtStage.Name
	}

	w := inst.copy()
	w.Status, w.WakeAt = Waiting, 0
	w, err = r.backToStage(inst, w)
	if err != nil {
		return nil, "", err
	}
	if w == nil {
		return nil, fmt.Sprintf("%s is not in the newest version as it was, and no migrate fn takes it there", from), nil
	}
	report, err := r.dryRun(w, nil)
	if err != nil {
		return nil, "", err
	}
	played := "played again from " + from + ", the newest version"
	switch {
	case report.Stopped != "":
		return nil, played + " would stop: " + report.Stopped, nil
	case len(report.left) > 0:
		return nil, fmt.Sprintf("%s does something new before it asks again for %s", played, strings.Join(report.left, ", ")), nil
	}
	took := map[int64]bool{}
	for _, seq := range report.took {
		took[seq] = true
	}
	missed := 0
	for _, seq := range report.given {
		if !took[seq] {
			missed++
		}
	}
	if missed > 0 {
		return nil, fmt.Sprintf("%s does not take again %d of the messages it took", played, missed), nil
	}
	return w, "", nil
}

// Version is the fingerprint of the newest version of the program, kept
// in the store.
func (r *Runtime) Version() (string, error) {
	return r.newestVersion()
}

// hold keeps the workers of this runtime off an instance; false when one
// has it.
func (r *Runtime) hold(id string) bool {
	r.claims.Lock()
	defer r.claims.Unlock()
	if r.taken[id] {
		return false
	}
	if r.taken == nil {
		r.taken = map[string]bool{}
	}
	r.taken[id] = true
	return true
}

func (r *Runtime) release(id string) {
	r.claims.Lock()
	defer r.claims.Unlock()
	delete(r.taken, id)
}

// lease is how long an instance is held on a store several share.
func (r *Runtime) lease() time.Duration {
	if r.Lease <= 0 {
		return 30 * time.Second
	}
	return r.Lease
}
