package flowrt

// The attempts of steps the application carries out, as the store keeps
// them (see docs/flow-app-steps.md, "The life of an attempt"): each is
// written before it leaves, with the token that answers for it and when
// it is given up, so that an answer that comes later, on another
// request, finds it.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AttemptState is where an attempt is in its life.
type AttemptState string

const (
	Sent     AttemptState = "sent"     // it left, and nothing came back yet
	Working  AttemptState = "working"  // the application said it answers later
	Answered AttemptState = "answered" // an answer came, and stands
	GivenUp  AttemptState = "given up" // its time passed, or a next attempt was made
	// Nothing: a person said an attempt the application could not tell
	// of did nothing; the next is sent reclaiming it
	Nothing AttemptState = "nothing"
)

// Attempt is an attempt of a step by a queue.
type Attempt struct {
	Key      string
	Attempt  int
	Instance string
	Queue    string
	Step     string // the step's name, as the application knows it
	Shape    string
	Token    string
	Until    int64
	State    AttemptState
	Settled  bool   // the step returns a Settled
	Undo     string // the step that undoes it, "" for none
	Answer   []byte // the answer, as the application wrote it
	Late     []byte // an answer that came once it was given up
	// Said is, for an attempt given up as one that may have arrived,
	// what the queue said: a status and its body
	Said string
}

// What an answer that comes later is told.
var (
	ErrNoAttempt = errors.New("no attempt of a step has that key")
	ErrToken     = errors.New("the token is not the attempt's")
	ErrLate      = errors.New("the attempt was given up: the answer is kept aside, and not given to the flow")
)

// settled says whether an attempt is past moving to a state: answered,
// or given up, which a person may only say did nothing; or, to Working,
// no longer Sent.
func settled(at, to AttemptState) bool {
	switch {
	case at == Answered:
		return true
	case at == GivenUp:
		return to != Nothing
	case to == Working:
		return at != Sent
	}
	return false
}

// answered says whether an attempt has its answer: the instance that
// waits on it is due.
func (a Attempt) answered() bool { return a.State == Answered }

// answering says what an answer to an attempt as it is becomes: already
// for one answered, an error for one given up or of another token. ping
// is an answer that says only that the step is under way.
func answering(a *Attempt, attempt int, token string, answer []byte, ping bool, now int64) (already bool, err error) {
	switch {
	case a.Token != token:
		return false, ErrToken
	case a.Attempt != attempt, a.State == GivenUp, now > a.Until && a.State != Answered:
		if !ping {
			a.Late = append([]byte(nil), answer...)
		}
		return false, ErrLate
	case a.State == Answered:
		return true, nil
	case ping:
		return false, nil
	}
	a.State, a.Answer = Answered, append([]byte(nil), answer...)
	return false, nil
}

// --- In memory ---

func (m *Memory) PutAttempt(a Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.write(); err != nil {
		return err
	}
	if m.attempts == nil {
		m.attempts = map[string][]*Attempt{}
	}
	// A next attempt gives up the one before; the same attempt, sent
	// again since it did not arrive, takes its place
	list := m.attempts[a.Key][:0]
	for _, before := range m.attempts[a.Key] {
		if before.Attempt == a.Attempt {
			continue
		}
		if before.State == Sent || before.State == Working {
			before.State = GivenUp
		}
		list = append(list, before)
	}
	m.attempts[a.Key] = append(list, &a)
	return nil
}

func (m *Memory) UnderWay(queue string, now int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, list := range m.attempts {
		for _, a := range list {
			if a.Queue == queue && (a.State == Sent || a.State == Working) && a.Until > now {
				n++
			}
		}
	}
	return n
}

func (m *Memory) Attempt(key string) (Attempt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.attempts[key]
	if len(list) == 0 {
		return Attempt{}, false, nil
	}
	return *list[len(list)-1], true, nil
}

func (m *Memory) Attempts(key string) []Attempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Attempt
	for _, a := range m.attempts[key] {
		out = append(out, *a)
	}
	return out
}

func (m *Memory) Mark(key string, attempt int, to AttemptState, said string) (Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts[key] {
		if a.Attempt != attempt {
			continue
		}
		if settled(a.State, to) {
			// What landed first stands
			return *a, nil
		}
		if err := m.write(); err != nil {
			return Attempt{}, err
		}
		a.State, a.Said = to, said
		return *a, nil
	}
	return Attempt{}, ErrNoAttempt
}

func (m *Memory) AnswerAttempt(key string, attempt int, token string, answer []byte, ping bool, now int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.attempts[key]
	var a *Attempt
	for _, each := range list {
		if each.Attempt == attempt {
			a = each
		}
	}
	if a == nil && len(list) > 0 && list[len(list)-1].Token == token {
		a = list[len(list)-1]
	}
	if a == nil {
		return false, ErrNoAttempt
	}
	if a.Token != token {
		return false, ErrToken
	}
	before := *a
	already, err := answering(a, attempt, token, answer, ping, now)
	if (a.State != before.State || len(a.Late) != len(before.Late)) && !ping {
		if werr := m.write(); werr != nil {
			*a = before
			return false, werr
		}
	}
	if err == nil && !already && a.State == Answered {
		if inst, ok := m.instances[a.Instance]; ok && inst.Status == Waiting {
			inst.WakeAt = 0
		}
	}
	return already, err
}

// answerWaited says whether an instance waits, among its names, for an
// attempt that has an answer. Call with mu held.
func (m *Memory) answerWaited(inst *Instance) bool {
	for _, name := range inst.Awaits {
		if key, ok := strings.CutPrefix(name, "answer:"); ok {
			if list := m.attempts[key]; len(list) > 0 && list[len(list)-1].answered() {
				return true
			}
		}
	}
	return false
}

// --- SQL ---

func (s *SQL) PutAttempt(a Attempt) error {
	if err := s.write(); err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(s.q(`UPDATE attempts SET state = ? WHERE key = ? AND attempt != ? AND state IN (?, ?)`), GivenUp, a.Key, a.Attempt, Sent, Working); err != nil {
			return err
		}
		_, err := tx.Exec(s.q(`INSERT INTO attempts (key, attempt, instance, queue, step, token, until, state, settled, undo, answer, late, said, shape)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (key, attempt) DO UPDATE SET instance = excluded.instance, queue = excluded.queue, step = excluded.step,
			token = excluded.token, until = excluded.until, state = excluded.state, settled = excluded.settled, undo = excluded.undo,
			answer = excluded.answer, late = excluded.late, said = excluded.said, shape = excluded.shape`),
			a.Key, a.Attempt, a.Instance, a.Queue, a.Step, a.Token, a.Until, a.State, a.Settled, a.Undo, string(a.Answer), string(a.Late), a.Said, a.Shape)
		return err
	})
}

const attemptColumns = `key, attempt, instance, queue, step, token, until, state, settled, undo, answer, late, said, shape`

func scanAttempt(row interface{ Scan(...any) error }) (Attempt, error) {
	var a Attempt
	var state, answer, late string
	if err := row.Scan(&a.Key, &a.Attempt, &a.Instance, &a.Queue, &a.Step, &a.Token, &a.Until, &state, &a.Settled, &a.Undo, &answer, &late, &a.Said, &a.Shape); err != nil {
		return Attempt{}, err
	}
	a.State = AttemptState(state)
	if answer != "" {
		a.Answer = []byte(answer)
	}
	if late != "" {
		a.Late = []byte(late)
	}
	return a, nil
}

func (s *SQL) Attempt(key string) (Attempt, bool, error) {
	a, err := scanAttempt(s.db.QueryRow(s.q(`SELECT `+attemptColumns+` FROM attempts WHERE key = ? ORDER BY attempt DESC LIMIT 1`), key))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, nil
	}
	return a, err == nil, err
}

func (s *SQL) Attempts(key string) []Attempt {
	rows, err := s.db.Query(s.q(`SELECT `+attemptColumns+` FROM attempts WHERE key = ? ORDER BY attempt`), key)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		if a, err := scanAttempt(rows); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (s *SQL) UnderWay(queue string, now int64) int {
	var n int
	s.db.QueryRow(s.q(`SELECT COUNT(*) FROM attempts WHERE queue = ? AND state IN (?, ?) AND until > ?`), queue, Sent, Working, now).Scan(&n)
	return n
}

func (s *SQL) Mark(key string, attempt int, to AttemptState, said string) (Attempt, error) {
	a, err := scanAttempt(s.db.QueryRow(s.q(`SELECT `+attemptColumns+` FROM attempts WHERE key = ? AND attempt = ?`), key, attempt))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrNoAttempt
	}
	if err != nil {
		return Attempt{}, err
	}
	// Where it is already: MySQL counts no row that an update leaves as
	// it was, and the update below would be tried for ever
	if settled(a.State, to) || (a.State == to && a.Said == said) {
		return a, nil
	}
	if err := s.write(); err != nil {
		return Attempt{}, err
	}
	// What landed first stands: the update holds only from where it was
	n, err := s.exec(s.q(`UPDATE attempts SET state = ?, said = ? WHERE key = ? AND attempt = ? AND state = ?`), to, said, key, attempt, a.State)
	if err != nil {
		return Attempt{}, err
	}
	if n == 0 {
		return s.Mark(key, attempt, to, said)
	}
	a.State, a.Said = to, said
	return a, nil
}

func (s *SQL) AnswerAttempt(key string, attempt int, token string, answer []byte, ping bool, now int64) (bool, error) {
	var already bool
	var aerr error
	wrote := false
	err := s.tx(func(tx *sql.Tx) error {
		a, err := scanAttempt(tx.QueryRow(s.q(`SELECT `+attemptColumns+` FROM attempts WHERE key = ? AND attempt = ?`), key, attempt))
		if errors.Is(err, sql.ErrNoRows) {
			// An attempt before the last, as the token of the last says
			a, err = scanAttempt(tx.QueryRow(s.q(`SELECT `+attemptColumns+` FROM attempts WHERE key = ? ORDER BY attempt DESC LIMIT 1`), key))
			if errors.Is(err, sql.ErrNoRows) || (err == nil && a.Token != token) {
				return ErrNoAttempt
			}
		}
		if err != nil {
			return err
		}
		if a.Token != token {
			return ErrToken
		}
		before := a
		already, aerr = answering(&a, attempt, token, answer, ping, now)
		if (a.State != before.State || len(a.Late) != len(before.Late)) && !ping {
			if !wrote {
				// Counted once, however many times the server has it
				// run again
				if err := s.write(); err != nil {
					return err
				}
				wrote = true
			}
			if _, err := tx.Exec(s.q(`UPDATE attempts SET state = ?, answer = ?, late = ? WHERE key = ? AND attempt = ?`),
				a.State, string(a.Answer), string(a.Late), a.Key, a.Attempt); err != nil {
				return err
			}
			if a.State == Answered {
				if _, err := tx.Exec(s.q(`UPDATE instances SET wake_at = 0 WHERE id = ? AND status = 'waiting'`), a.Instance); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return already, aerr
}

// answerWaited says, in a write, whether an instance waits on an
// attempt that has an answer.
func (s *SQL) answerWaited(tx *sql.Tx, name string) (bool, error) {
	key, ok := strings.CutPrefix(name, "answer:")
	if !ok {
		return false, nil
	}
	var state string
	err := tx.QueryRow(s.q(`SELECT state FROM attempts WHERE key = ? ORDER BY attempt DESC LIMIT 1`), key).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && AttemptState(state) == Answered, err
}

func (a Attempt) String() string {
	return fmt.Sprintf("%s#%d %s", a.Key, a.Attempt, a.State)
}
