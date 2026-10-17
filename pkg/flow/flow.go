// Package flow is for a Go application that carries out steps of Qoqi's
// flows: the daemon sends each step to the application's queue, signed,
// and the application answers what the step returns.
//
// A step is declared in Qoqi with its queue and no body:
//
//	step fn charge(invoice: Invoice) -> Result<Charge, Decline> by billing;
//
// `qoqi gen go` writes the types of such steps for Go, and an
// interface with a method for each; the application implements it, and
// serves the queue as any page:
//
//	queue := billing.Queue(os.Getenv("BILLING_SECRET"), &Billing{stripe: client})
//	http.Handle("/flow-steps", queue)
//
// Without the code written for it, a queue is made with NewQueue, and a
// handler given for each shape of step (Handle).
//
// What a step returns is its answer. An error is not: a step whose body
// goes wrong returns one, and the flow is told the step failed, as a
// panic of a step in Qoqi. Three errors say something else: Retry (not
// done, to be sent again), Later (the answer comes on a request of its
// own, Complete) and ErrUnknown (acted, and cannot say how).
package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Task is a step the daemon sent: its key, the same for every attempt
// and no other step's, which goes to the world as an idempotency key;
// the attempt, from 1; the token that answers for the attempt; when it
// is given up; how long the daemon keeps an ended instance; the step's
// name and shape; and its arguments, by name, in the form of the
// boundary.
type Task struct {
	Key     string          `json:"key"`
	Attempt int             `json:"attempt"`
	Token   string          `json:"token"`
	Until   int64           `json:"until"` // milliseconds since 1970
	Keep    int64           `json:"keep"`  // milliseconds
	Step    string          `json:"step"`
	Shape   string          `json:"shape"`
	Args    json.RawMessage `json:"args,omitempty"`

	// Probe asks what became of an attempt, rather than sending it: the
	// daemon asks it of a step kept under started that may have arrived
	Probe bool `json:"probe,omitempty"`
	// Reclaim is the attempt whose claim died with its process, and that
	// a person said did nothing: the claim is dropped, and the step acts
	Reclaim int `json:"reclaim,omitempty"`
}

// Deadline is when the daemon gives the attempt up.
func (t *Task) Deadline() time.Time {
	return time.UnixMilli(t.Until)
}

// Bind reads the task's arguments into v, a struct whose fields are the
// step's parameters.
func (t *Task) Bind(v any) error {
	if err := json.Unmarshal(t.Args, v); err != nil {
		return fmt.Errorf("the arguments of %s: %w", t.Step, err)
	}
	return nil
}

// Answer is what the application answers a step, as the daemon reads
// it: a value, with its undo for a step that names one; or a retry, a
// failure, unknown, or under way.
type Answer struct {
	Value   json.RawMessage `json:"value,omitempty"`
	Undo    json.RawMessage `json:"undo,omitempty"`
	Retry   *RetryAnswer    `json:"retry,omitempty"`
	Failed  *string         `json:"failed,omitempty"`
	Unknown bool            `json:"unknown,omitempty"`
	Working bool            `json:"working,omitempty"`

	// UnknownShape says the application has no handler for a step of
	// this shape, and did nothing
	UnknownShape string `json:"unknown_shape,omitempty"`

	// Unseen answers a probe: nothing was done under the key, and the
	// step may be sent again
	Unseen bool `json:"unseen,omitempty"`

	// later says the answer comes on a request of its own: 202. Working
	// alone says an attempt before is under way, to be asked again
	later bool
}

// RetryAnswer is a step not acted on, to be sent again after a while.
type RetryAnswer struct {
	Reason string `json:"reason"`
	After  int64  `json:"after"` // milliseconds
}

// Value answers what the step returns: written as JSON, in the form of
// the boundary, which the types `qoqi gen go` writes give.
func Value(v any) (Answer, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return Answer{}, fmt.Errorf("what the step returns is not JSON: %w", err)
	}
	return Answer{Value: data}, nil
}

// WithUndo answers what the step returns and the argument of the step
// that undoes it, which its declaration names, `by warehouse undo
// release`: given with every value that is not an Err.
func WithUndo(v, undo any) (Answer, error) {
	a, err := Value(v)
	if err != nil {
		return a, err
	}
	if a.Undo, err = json.Marshal(undo); err != nil {
		return Answer{}, fmt.Errorf("what undoes the step is not JSON: %w", err)
	}
	return a, nil
}

// Failure answers that the step's body went wrong.
func Failure(err error) Answer {
	why := err.Error()
	return Answer{Failed: &why}
}

// Answered is the answer Value or WithUndo gives, or the failure of a
// value that cannot be written.
func Answered(a Answer, err error) Answer {
	if err != nil {
		return Failure(err)
	}
	return a
}

// Done is what a step that returns a Settled gives when it is done: the
// daemon gives Unknown when nothing says what came of it.
func Done(v any) any {
	return map[string]any{"Done": v}
}

// What a step returns as an error that is not a failure.
var (
	// ErrUnknown says the step acted, and nobody can say how: a process
	// died in the middle of an effect outside the application's own
	// database. A Settled step is Unknown in the flow; a plain one stops
	// its instance, for a person.
	ErrUnknown = errors.New("the step acted, and cannot say how")

	// ErrLater says the answer comes later, on a request of its own
	// (Complete), before the step's deadline: the daemon is told 202.
	ErrLater = errors.New("the answer comes later")
)

// Retry says the step did nothing and is to be sent again after a while,
// as its next attempt: a gateway down for a moment.
func Retry(reason string, after time.Duration) error {
	return &retryError{reason: reason, after: after}
}

type retryError struct {
	reason string
	after  time.Duration
}

func (e *retryError) Error() string { return "to be sent again: " + e.reason }

// AnswerOf is the answer an error from a step gives: a retry, unknown,
// under way, or a failure.
func AnswerOf(err error) Answer {
	var retry *retryError
	switch {
	case errors.As(err, &retry):
		return Answer{Retry: &RetryAnswer{Reason: retry.reason, After: retry.after.Milliseconds()}}
	case errors.Is(err, ErrUnknown):
		return Answer{Unknown: true}
	case errors.Is(err, ErrLater):
		return Answer{Working: true, later: true}
	}
	return Failure(err)
}
