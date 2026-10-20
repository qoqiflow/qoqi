package vm

// Steps the application carries out (see docs/flow-app-steps.md).
//
// Such a step has no body of its own: the compiler gives it one that
// calls std.flow.deliver, which sends the step to its queue, and what the
// application answers is what the step returns. The runtime of flows
// says how a step reaches its queue (SetQueues); the VM reads the answer
// as Qoqi reads what a step in Qoqi does (Settle).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"
)

// Delivery is a step as it leaves for its queue.
type Delivery struct {
	Queue   string
	Step    string // its name, as the application knows it
	Shape   string // its signature, as a digest: which handler answers it
	Key     string
	Attempt int
	Within  int64  // milliseconds it may take, from when it is sent
	Settled bool   // it returns a std.flow.Settled
	Undo    string // the step that undoes it, by its name in the program; "" for none
	Args    json.RawMessage

	// Reclaim is the attempt whose claim died with its process, and that
	// a person said did nothing: the application drops it, and acts
	Reclaim int

	// Token answers for the attempt, and Until is when it is given up:
	// drawn and written by the runtime before it leaves, or by the
	// queues when no runtime did
	Token string
	Until int64
}

// Outcome is what came of a delivery.
type Outcome int

const (
	// Answered: Value is what the step returns, and Undo, for a step
	// that names one, the argument of its undo.
	Answered Outcome = iota
	// Again: nothing was done, the application said so or the step never
	// arrived; it is sent again after After milliseconds, as its next
	// attempt.
	Again
	// Failed: the application's body went wrong, as a panic of a step.
	Failed
	// Uncertain: it may have arrived, and nothing says what came of it.
	// A plain step is sent again; a Settled one is Unknown.
	Uncertain
	// Unknown: the application acted, and cannot say how. A Settled step
	// is Unknown; a plain one stops its instance, for a person.
	Unknown
	// Working: the application is at it, and answers later, before
	// Until; the task that called waits for that, out of memory.
	Working
	// Held: it did not leave, or did not arrive: its queue is full,
	// paused or backing off. It leaves again after After milliseconds,
	// as the same attempt: an outage spends none.
	Held
	// Halt: something a person looks at stops the instance.
	Halt
)

// Answer is what a queue answered a delivery. Reason says why, for
// every outcome but Answered.
type Answer struct {
	Outcome Outcome
	Value   json.RawMessage
	Undo    json.RawMessage
	Reason  string
	After   int64
	Until   int64
}

// Queues sends steps to the queues that carry them out. Deliver is
// called without the turn: other tasks go on while it waits.
type Queues interface {
	Deliver(d Delivery) Answer
}

// WorkingError is a step the application answers later: it ends the
// step, for the runtime of flows to wait for the answer, until Until.
type WorkingError struct {
	Until int64
}

func (e *WorkingError) Error() string { return "the application answers the step later" }

// HeldError is a step that did not leave for its queue, or did not
// arrive: the runtime of flows sends it again after After milliseconds,
// as the same attempt.
type HeldError struct {
	Reason string
	After  int64
}

func (e *HeldError) Error() string { return "the step waits for its queue: " + e.Reason }

// SetQueues says how the steps of queues reach them, for a program run
// for a flow. Without it, such a step stops the program, naming it.
func (vm *VM) SetQueues(queues Queues) {
	vm.queues = queues
}

// SettledUnknown is std.flow.Settled.Unknown, as data.
const SettledUnknown = `{"$type": "std.flow.Settled.Unknown", "$variant": true}`

// Settle says what an answer of the application is to the flow, as what
// a step in Qoqi does would be: what the step returns, as data, with
// the undo it noted; a retry; or an error, which a Stop makes the
// instance's. name is the step's, settled says that it returns a
// Settled, undo names the step that undoes it, and key is its own.
//
// A value is given with an undo when the declaration names one, and is
// not an Err; with none otherwise. A Settled step whose answer is not a
// value, or not a value that fits, is Unknown: its body may have acted.
func Settle(name string, settled bool, a Answer, undo, key string) (data []byte, undos []Undo, retry *RetryError, err error) {
	switch a.Outcome {
	case Answered:
		if err := fits(name, a, undo); err != nil {
			if settled {
				return []byte(SettledUnknown), nil, nil, nil
			}
			return nil, nil, nil, err
		}
		if undo != "" && a.Undo != nil {
			undos = []Undo{{Step: undo, Argument: a.Undo, From: key}}
		}
		return a.Value, undos, nil, nil
	case Again:
		return nil, nil, &RetryError{Reason: a.Reason, After: a.After}, nil
	case Uncertain:
		if settled {
			return []byte(SettledUnknown), nil, nil, nil
		}
		return nil, nil, &RetryError{Reason: a.Reason, After: a.After}, nil
	case Failed:
		if settled {
			return []byte(SettledUnknown), nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("%s failed in the application: %s", name, a.Reason)
	case Unknown:
		if settled {
			return []byte(SettledUnknown), nil, nil, nil
		}
		return nil, nil, nil, &Stop{Reason: fmt.Errorf("%s may have acted, and the application cannot say how: %s; once a person knows, qoqi answer gives what it did under %s, or --nothing", name, a.Reason, key)}
	}
	return nil, nil, nil, &Stop{Reason: fmt.Errorf("%s: %s", name, a.Reason)}
}

// fits says what is wrong with a value an application gave, and the
// undo it gave with it.
func fits(name string, a Answer, undo string) error {
	var tag struct {
		Type string `json:"$type"`
	}
	if a.Value == nil {
		return fmt.Errorf("the application gave %s no value", name)
	}
	if !json.Valid(a.Value) {
		return fmt.Errorf("what the application answered for %s is not JSON", name)
	}
	// Only an object has a $type: an array, a text, a number or null is a
	// value as well
	if strings.HasPrefix(strings.TrimSpace(string(a.Value)), "{") {
		if err := json.Unmarshal(a.Value, &tag); err != nil {
			return fmt.Errorf("what the application answered for %s is not JSON: %v", name, err)
		}
	}
	failed := tag.Type == "std.result.Result.Err"
	switch {
	case undo == "" && a.Undo != nil:
		return fmt.Errorf("the application gave %s an undo, and its declaration names none", name)
	case undo == "":
	case failed && a.Undo != nil:
		return fmt.Errorf("the application gave %s an undo with an Err: what was not done is not undone", name)
	case failed:
	case a.Undo == nil:
		return fmt.Errorf("the application gave %s no undo: its declaration names %s, given with every value that is not an Err", name, undo[strings.LastIndex(undo, ".")+1:])
	default:
		var undone json.RawMessage
		if err := json.Unmarshal(a.Undo, &undone); err != nil {
			return fmt.Errorf("the undo the application gave %s is not JSON: %v", name, err)
		}
	}
	return nil
}

// givenOf is what a step its queue carries out is given, as data, from
// the values it is called with; nil for any other step, and for a value
// that is not data, which deliver says.
func givenOf(vm *VM, proto *FunctionPrototype, args []Value) [][]byte {
	if !proto.ByQueue {
		return nil
	}
	given := make([][]byte, len(args))
	for i, arg := range args {
		if data, err := vm.DataOf(arg); err == nil {
			given[i] = data
		}
	}
	return given
}

// deliver sends the step the running task carries out to its queue.
// undo is the step that undoes it, or nil.
func (vm *VM) deliver(args []Value, undo *FunctionObj) (Value, error) {
	queue, err := argString(args, 0)
	if err != nil {
		return NewNull(), err
	}
	params, err := argString(args, 1)
	if err != nil {
		return NewNull(), err
	}
	within, err := argInt(args, 2)
	if err != nil {
		return NewNull(), err
	}
	if vm.stepKey == "" || vm.currentFunction == nil {
		return NewNull(), fmt.Errorf("a step of the queue %s is carried out for a flow, by qoqi serve", queue)
	}
	// The step whose body calls: its parameters are its first registers,
	// whether the runtime runs it for a flow or as an undo
	proto := vm.currentFunction.Proto()
	name := proto.Name[strings.LastIndex(proto.Name, ".")+1:]
	if vm.queues == nil {
		return NewNull(), &Stop{Reason: fmt.Errorf("%s is carried out by the queue %s, which nothing serves here: a trial replaces it, trial.replace(%s, ...)", name, queue, name)}
	}
	if within <= 0 {
		return NewNull(), fmt.Errorf("%s may take %d milliseconds: within is more than nothing", name, within)
	}

	given := &ordered{}
	var names []string
	if params != "" {
		names = strings.Split(params, ",")
	}
	if len(names) != proto.ArgCount || len(vm.stepGiven) != proto.ArgCount {
		return NewNull(), fmt.Errorf("%s is sent with %d parameters, and takes %d", name, len(names), proto.ArgCount)
	}
	for i, param := range names {
		if vm.stepGiven[i] == nil {
			return NewNull(), fmt.Errorf("what %s is given is data: %s is not", name, param)
		}
		given.add(param, json.RawMessage(vm.stepGiven[i]))
	}
	body, err := json.Marshal(given)
	if err != nil {
		return NewNull(), err
	}
	undoing := ""
	if undo != nil {
		undoing = undo.proto.Name
	}
	d := Delivery{
		Queue: queue, Step: name, Shape: StepShape(name, proto.Signature),
		Key: vm.stepKey, Attempt: vm.stepTry, Within: within, Settled: proto.Once, Undo: undoing, Args: body,
	}

	var answer Answer
	if err := vm.blocking(func() { answer = vm.queues.Deliver(d) }); err != nil {
		return NewNull(), err
	}
	switch answer.Outcome {
	case Working:
		return NewNull(), &WorkingError{Until: answer.Until}
	case Held:
		return NewNull(), &HeldError{Reason: answer.Reason, After: answer.After}
	}
	data, undos, retry, err := Settle(name, proto.Once, answer, undoing, vm.stepKey)
	switch {
	case err != nil:
		return NewNull(), err
	case retry != nil:
		return NewNull(), retry
	}
	v, err := vm.ValueOfData(data)
	if err != nil {
		return NewNull(), fmt.Errorf("what the application answered for %s: %v", name, err)
	}
	vm.undos = append(vm.undos, undos...)
	return v, nil
}

// StepShape is the shape of a step, by its name and its signature: what
// says which handler of the application answers it. A step whose
// signature changed has another.
func StepShape(name, signature string) string {
	shape := sha256.Sum256([]byte(name + signature))
	return hex.EncodeToString(shape[:8])
}

// AwaitUntil lets the task that asks wait, in a program run for a flow,
// until Arrived is told the name or the clock reaches until, whichever
// comes first: from the OnStep hook, a step the application answers
// later. Frozen meanwhile, the task is at the call, and calls the step
// again once taken up.
func (vm *VM) AwaitUntil(name string, until int64) error {
	s := vm.sched
	s.mu.Lock()
	if vm.awaits == nil {
		vm.awaits = map[string][]int64{}
	}
	me := s.running.id
	vm.awaits[name] = append(vm.awaits[name], me)
	s.mu.Unlock()

	_, err := vm.sleepUntil(until, "step")

	s.mu.Lock()
	left := vm.awaits[name][:0]
	for _, id := range vm.awaits[name] {
		if id != me {
			left = append(left, id)
		}
	}
	if len(left) == 0 {
		delete(vm.awaits, name)
	} else {
		vm.awaits[name] = left
	}
	s.mu.Unlock()
	return err
}

func init() {
	registerNatives("std.flow",
		// The time a step in Qoqi may take, which it says first. The
		// runtime of flows does not give a step up yet; it says nothing
		// that is not more than nothing
		native{"limit", 1, func(vm *VM, args []Value) (Value, error) {
			within, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if within <= 0 {
				return NewNull(), fmt.Errorf("a step may take %d milliseconds: within is more than nothing", within)
			}
			return NewNull(), nil
		}},
		native{"deliver", 3, func(vm *VM, args []Value) (Value, error) {
			return vm.deliver(args, nil)
		}},
		native{"deliverUndone", 4, func(vm *VM, args []Value) (Value, error) {
			h := header(args[3])
			if h == nil || h.Type != ObjTypeFunction || !(*FunctionObj)(unsafe.Pointer(h)).proto.Step {
				return NewNull(), fmt.Errorf("what undoes a step of a queue is a step fn")
			}
			return vm.deliver(args, (*FunctionObj)(unsafe.Pointer(h)))
		}},
	)
}
