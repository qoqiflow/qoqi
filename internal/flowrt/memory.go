package flowrt

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/qoqiflow/qoqi/internal/sqldb"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Store keeps instances and what their steps returned.
type Store interface {
	// Create keeps a new instance, unless its name is taken: then it
	// gives the instance that has it.
	Create(inst *Instance) (created bool, existing *Instance, err error)
	// Save writes an instance as it is now, in one piece, but for the
	// cancellation asked, which it leaves as it is. The save that ends an
	// instance makes those that wait for it due, in the same write.
	Save(inst *Instance) error
	// Get gives an instance, ok false when there is none of that name. An
	// error is a read that failed: it says nothing of whether it is there
	Get(id string) (inst *Instance, ok bool, err error)
	// Due are the instances waiting whose time has come.
	Due(now int64) []string
	AnyWaiting() bool
	// PutResult keeps what a step gave under its key, in one write; the
	// first one kept stands.
	PutResult(key string, kept Kept) error
	Result(key string) (kept Kept, ok bool, err error)
	// ForgetResult forgets what a step gave: a person said the world
	// changed, and the step is to run again (an undo that gave an Err).
	ForgetResult(key string) error
	// Keys are the keys of what steps gave that start with a prefix.
	Keys(prefix string) []string
	// Cancel asks that an instance end, for a reason: noted on it, and
	// due at once if it waits or stopped. One that is ending already is
	// left as it is; one that has ended is a mistake.
	Cancel(id, reason string, now int64) error
	// List gives the instances, by name, of a flow and in a status when
	// those are given ("" for any).
	List(flow string, status Status) []*Instance
	// ChangedSince gives at least the instances written since a moment,
	// in milliseconds of the wall clock: their status, their wake, their
	// cancellation, their state. Leases are not changes. A store may give
	// more than those, never fewer.
	ChangedSince(since int64) []*Instance
	// Send keeps a message for an inbox of an instance, and makes the
	// instance due if it waits. A key sent to that inbox before gives
	// already, and nothing is kept; an instance that has ended is
	// ErrEnded; an inbox that holds capacity messages not taken, ErrFull.
	Send(id, inbox, key string, data []byte, capacity int) (already bool, err error)
	// Messages are the messages of an inbox not taken yet, in the order
	// they came.
	Messages(id, inbox string) []Message
	// Untake gives back to their inboxes the messages an instance took
	// from a pass on: an instance resumed from its stage takes them again.
	Untake(id string, fromPass int) error

	// PutAttempt writes an attempt of a step by a queue before it
	// leaves, and gives up the one before it under its key. Attempt
	// gives the last under a key.
	PutAttempt(a Attempt) error
	Attempt(key string) (a Attempt, ok bool, err error)
	Attempts(key string) []Attempt
	// Mark moves an attempt on in its life, unless an answer or its
	// giving up landed first, or, to Working, it is no longer Sent: it
	// gives the attempt as it is then.
	Mark(key string, attempt int, to AttemptState, said string) (Attempt, error)
	// UnderWay is how many attempts of a queue are sent or working, and
	// not given up by now: what its concurrency counts
	UnderWay(queue string, now int64) int
	// AnswerAttempt keeps an answer that comes later, if its token is
	// the attempt's and it is under way, and makes the instance due if it
	// waits; a ping says only that the step is under way, and keeps
	// nothing. One answered gives already; one given up keeps the answer
	// aside, ErrLate.
	AnswerAttempt(key string, attempt int, token string, answer []byte, ping bool, now int64) (already bool, err error)

	// PutVersion keeps the bytecode of a version of the program, by its
	// fingerprint, for the instances that run it; Version gives it back.
	PutVersion(fingerprint string, bytecode []byte) error
	Version(fingerprint string) ([]byte, bool)
}

// Leased is a store several daemons share: an instance is run by the one
// that holds its lease, which it holds again while it runs, and lets go
// of after. A save by a daemon whose lease another took is ErrNotHeld.
type Leased interface {
	Claim(id string, now, until int64) (bool, error)
	Unclaim(id string) error
}

// InDatabase is a store kept in a database a step can write in, with
// flow.store(): what the step writes there and what it gives are kept in
// one transaction, KeepIn's.
type InDatabase interface {
	Database() (*sql.DB, sqldb.Kind)
	KeepIn(tx *sql.Tx, key string, kept Kept) error
}

// ErrCrash is what a store that was told to fail at a write gives: the
// process stopped there, and nothing of that write was kept.
var ErrCrash = errors.New("the process stopped")

// What a send is refused with.
var (
	ErrEnded = errors.New("the instance has ended")
	ErrFull  = errors.New("the inbox is full")
)

// Message is a message an instance has not taken yet.
type Message struct {
	Seq  int64
	Data []byte
}

// Taken is a message an instance took, in a pass: marked taken by the
// save that follows, in the same write.
type Taken struct {
	Seq  int64
	Pass int
}

// message is a message as Memory keeps it.
type message struct {
	seq        int64
	inbox, key string
	data       []byte
	taken      bool
	pass       int
}

// Memory is a store in memory. It can be told to fail at its n-th write,
// to stand for a process that stops there.
type Memory struct {
	mu        sync.Mutex
	instances map[string]*Instance
	results   map[string]Kept
	versions  map[string][]byte
	messages  map[string][]*message
	deployed  []Deployed
	attempts  map[string][]*Attempt
	seq       int64

	writes    int
	FailAt    int // the write that fails, counting from 1; 0 for none
	ResultLog []string
}

// Kept is what a step gave, kept under its key: what it returned, what
// it gave flow.undo, and what it was asked, its arguments as data.
type Kept struct {
	Data  []byte
	Undos []vm.Undo
	Asked []byte
}

func (k Kept) copy() Kept {
	return Kept{append([]byte(nil), k.Data...), append([]vm.Undo(nil), k.Undos...), append([]byte(nil), k.Asked...)}
}

func NewMemory() *Memory {
	return &Memory{instances: map[string]*Instance{}, results: map[string]Kept{}, versions: map[string][]byte{}, messages: map[string][]*message{}}
}

// Writes is how many writes the store has taken.
func (m *Memory) Writes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

func (m *Memory) write() error {
	m.writes++
	if m.FailAt > 0 && m.writes == m.FailAt {
		return ErrCrash
	}
	return nil
}

func (m *Memory) Create(inst *Instance) (bool, *Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.instances[inst.ID]; ok {
		return false, existing.copy(), nil
	}
	if err := m.write(); err != nil {
		return false, nil, err
	}
	m.instances[inst.ID] = inst.copy()
	return true, nil, nil
}

func (m *Memory) Save(inst *Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.write(); err != nil {
		return err
	}
	for _, child := range inst.Starting {
		if made, ok := m.instances[child.ID]; ok {
			if child.Parent != "" && made.Parent != inst.ID {
				return fmt.Errorf("%w: %s, started by %s, was started meanwhile by another", ErrStartedMeanwhile, child.ID, inst.ID)
			}
			continue
		}
	}
	for _, child := range inst.Starting {
		if _, ok := m.instances[child.ID]; !ok {
			m.instances[child.ID] = child.copy()
		}
	}
	for _, taken := range inst.Taking {
		for _, msg := range m.messages[inst.ID] {
			if msg.seq == taken.Seq {
				msg.taken, msg.pass = true, taken.Pass
			}
		}
	}
	saved := inst.copy()
	saved.Taking, saved.Starting = nil, nil
	saved.Cancel = ""
	if old, ok := m.instances[inst.ID]; ok {
		saved.Cancel = old.Cancel
	}
	if saved.Cancel != "" && !saved.CancelTaken && saved.Phase != Finish && (saved.Status == Waiting || saved.Status == Stopped) {
		// A cancellation asked while it ran: due at once, to act on it
		saved.Status, saved.WakeAt = Waiting, 0
	}
	if saved.Status == Waiting {
		// One it waits for that ended while it ran, or a message that
		// came to an inbox it waits on: due at once
		for _, name := range saved.Awaits {
			if other, ok := m.instances[name]; ok && other.ended() {
				saved.WakeAt = 0
			}
			if inbox, ok := strings.CutPrefix(name, "inbox:"); ok {
				for _, msg := range m.messages[inst.ID] {
					if msg.inbox == inbox && !msg.taken {
						saved.WakeAt = 0
					}
				}
			}
		}
		// An answer that came to a step while it ran
		if m.answerWaited(saved) {
			saved.WakeAt = 0
		}
	}
	m.instances[inst.ID] = saved
	if saved.ended() {
		for _, other := range m.instances {
			if other.Status == Waiting && awaits(other, inst.ID) {
				other.WakeAt = 0
			}
		}
	}
	return nil
}

func awaits(inst *Instance, id string) bool {
	for _, name := range inst.Awaits {
		if name == id {
			return true
		}
	}
	return false
}

func (m *Memory) Cancel(id, reason string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("there is no instance %s", id)
	}
	if err := cancelable(inst); err != nil || inst.Phase == Finish || inst.Cancel != "" {
		return err
	}
	if err := m.write(); err != nil {
		return err
	}
	inst.Cancel = reason
	if inst.Status == Waiting || inst.Status == Stopped {
		inst.Status, inst.WakeAt = Waiting, now
	}
	return nil
}

// cancelable says why an instance cannot be cancelled: it has ended.
func cancelable(inst *Instance) error {
	if inst.Status == Done || inst.Status == Failed {
		return fmt.Errorf("instance %s has ended: it is %s", inst.ID, inst.Status)
	}
	return nil
}

func (m *Memory) Get(id string) (*Instance, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return nil, false, nil
	}
	return inst.copy(), true, nil
}

// Due takes up, as a runtime that crashed and starts again does, the
// instances it was running too: what was running when the process
// stopped is due at once.
func (m *Memory) Due(now int64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []string
	for _, id := range sortedIDs(m.instances) {
		inst := m.instances[id]
		if inst.Status == Running || (inst.Status == Waiting && inst.WakeAt <= now) {
			due = append(due, id)
		}
	}
	return due
}

func (m *Memory) AnyWaiting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst.Status == Waiting || inst.Status == Running {
			return true
		}
	}
	return false
}

// NextWake is the earliest moment an instance waiting for the clock is
// due at, and false if none is: one waiting only for other instances or
// for a message waits for no moment.
func (m *Memory) NextWake() (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next, found := int64(0), false
	for _, inst := range m.instances {
		if inst.Status == Waiting && inst.WakeAt < Never && (!found || inst.WakeAt < next) {
			next, found = inst.WakeAt, true
		}
	}
	return next, found
}

// MostWakes is how many times instances on a clock of their own may wake
// while it is moved on once: one that sets timers due at once, again and
// again, is stopped there rather than run forever. A test may lower it.
var MostWakes = 100000

// Pass lets a span pass on a clock that only moves when it is told to, as
// the clock of a trial: it moves the clock to the earliest moment an
// instance is due at within the span, runs what that wakes until nothing
// is due, and goes on so until nothing is due within the span, then sets
// the clock at its end. A timer an instance sets on waking fires in the
// same span if it is due within it, and the instance sees the clock at
// the moment it was due at.
func Pass(store *Memory, now *int64, span int64, run func() error) error {
	end := *now + span
	for wakes := 0; ; wakes++ {
		at, ok := store.NextWake()
		if !ok || at > end {
			break
		}
		if wakes == MostWakes {
			return fmt.Errorf("the clock moved on %d ms, and the instances woke %d times in that span without settling: do they set timers due at once, again and again?", span, MostWakes)
		}
		if at > *now {
			*now = at
		}
		if err := run(); err != nil {
			return err
		}
	}
	*now = end
	return run()
}

func (m *Memory) PutResult(key string, kept Kept) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.write(); err != nil {
		return err
	}
	if _, ok := m.results[key]; !ok {
		m.results[key] = kept.copy()
		m.ResultLog = append(m.ResultLog, key)
	}
	return nil
}

func (m *Memory) Result(key string) (Kept, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.results[key]
	return k.copy(), ok, nil
}

func (m *Memory) ForgetResult(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.write(); err != nil {
		return err
	}
	delete(m.results, key)
	return nil
}

func (m *Memory) Keys(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key := range m.results {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// ChangedSince gives every instance: Memory keeps no time of change, and
// more than the changed ones is right. It serves tests and trials, whose
// instances are few.
func (m *Memory) ChangedSince(since int64) []*Instance {
	return m.List("", "")
}

func (m *Memory) List(flow string, status Status) []*Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Instance
	for _, id := range sortedIDs(m.instances) {
		inst := m.instances[id]
		if (flow == "" || inst.Flow == flow) && (status == "" || inst.Status == status) {
			out = append(out, inst.copy())
		}
	}
	return out
}

func (m *Memory) PutVersion(fingerprint string, bytecode []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.versions[fingerprint]; !ok {
		m.versions[fingerprint] = append([]byte(nil), bytecode...)
	}
	return nil
}

func (m *Memory) Version(fingerprint string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bytecode, ok := m.versions[fingerprint]
	return bytecode, ok
}

func (m *Memory) Send(id, inbox, key string, data []byte, capacity int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return false, fmt.Errorf("there is no instance %s", id)
	}
	if inst.ended() {
		return false, ErrEnded
	}
	waiting := 0
	for _, msg := range m.messages[id] {
		if msg.inbox != inbox {
			continue
		}
		if key != "" && msg.key == key {
			return true, nil
		}
		if !msg.taken {
			waiting++
		}
	}
	if waiting >= capacity {
		return false, ErrFull
	}
	m.seq++
	m.messages[id] = append(m.messages[id], &message{seq: m.seq, inbox: inbox, key: key, data: append([]byte(nil), data...)})
	if inst.Status == Waiting {
		inst.WakeAt = 0
	}
	return false, nil
}

func (m *Memory) Messages(id, inbox string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Message
	for _, msg := range m.messages[id] {
		if msg.inbox == inbox && !msg.taken {
			out = append(out, Message{Seq: msg.seq, Data: append([]byte(nil), msg.data...)})
		}
	}
	return out
}

func (m *Memory) Untake(id string, fromPass int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages[id] {
		if msg.taken && msg.pass >= fromPass {
			msg.taken = false
		}
	}
	return nil
}

// Deployed is a deploy of a program: its version, when, what was
// deployed of it as the daemon reads it, the lock it left, and what the
// daemon said of it.
type Deployed struct {
	Seq     int64
	Name    string
	Version string
	At      int64
	Package []byte
	Lock    []byte
	Report  []byte
}

// Deployments is a store that keeps what was deployed to it, every
// deploy after those before.
type Deployments interface {
	PutDeployment(d Deployed) (int64, error)
	Deployments() ([]Deployed, error)
	History(name string, packages bool) ([]Deployed, error)
	Deploy(seq int64) (Deployed, bool)
}

func (m *Memory) PutDeployment(d Deployed) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d.Seq = int64(len(m.deployed) + 1)
	d.Package = append([]byte(nil), d.Package...)
	m.deployed = append(m.deployed, d)
	return d.Seq, nil
}

func (m *Memory) Deployments() ([]Deployed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	last := map[string]Deployed{}
	for _, d := range m.deployed {
		last[d.Name] = d
	}
	var out []Deployed
	for _, d := range last {
		// A program taken away is deployed as no version
		if d.Version != "" {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) History(name string, packages bool) ([]Deployed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployed
	for _, d := range m.deployed {
		if name == "" || d.Name == name {
			if !packages {
				d.Package = nil
			}
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *Memory) Deploy(seq int64) (Deployed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq < 1 || seq > int64(len(m.deployed)) {
		return Deployed{}, false
	}
	return m.deployed[seq-1], true
}
