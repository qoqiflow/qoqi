package vm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Steps of a flow, and the data they are given and give (see docs/flow.md).
//
// A step is a function the compiler marked: the VM asks its OnStep hook
// before calling one. The hook can answer the call itself, with what the
// step returned once already, or run the step and keep what it returns.
// What crosses from a program to the store is data, written as JSON.

// StepCall is a call of a step, as the OnStep hook is asked about it.
// Once says that the step returns a std.flow.Settled: it is never run
// twice, and gives Unknown when nothing says what its first run did.
type StepCall struct {
	Name string
	Args []Value
	Once bool
	// ByQueue says that the application carries it out, through its queue
	ByQueue bool

	fn *FunctionObj
	vm *VM

	// What it gave, for the task that waits for it (Wait, Give)
	task   *task
	given  bool
	result Value
	err    error
}

// Wait, from the OnStep hook of a program run for a flow, lets the task
// that calls the step wait until Give says what the step gave: the
// OnIdle hook runs it once no task can go on (RunStep), or a test gives
// what stands for it. Frozen meanwhile, the task is at the call, and
// calls the step again once taken up.
func (c *StepCall) Wait() (Value, error) {
	c.task = c.vm.sched.running
	for !c.given {
		if err := c.vm.park(taskWaiting, "step"); err != nil {
			return NewNull(), err
		}
	}
	return c.result, c.err
}

// Give says what the step gave, and lets the task that waits for it go
// on: with the turn, from the OnIdle hook or a step's end.
func (c *StepCall) Give(result Value, err error) {
	s := c.vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	c.result, c.err, c.given = result, err, true
	// Alive until the task takes it
	c.task.ctx.pinned = append(c.task.ctx.pinned, result)
	s.wakeUp(c.task)
}

// Undo is what flow.undo was given: a step, and the argument it will be
// called with, as data. From is the key of the step that noted it, ""
// when the flow's own tasks did.
type Undo struct {
	Step     string          `json:"step"`
	Argument json.RawMessage `json:"argument"`
	From     string          `json:"-"`
}

// TakeUndos gives the undos noted by the step of a key, or, for "", by
// the flow's own tasks, in the order they were noted, and forgets them.
func (vm *VM) TakeUndos(from string) []Undo {
	var taken, left []Undo
	for _, u := range vm.undos {
		if u.From == from {
			taken = append(taken, u)
		} else {
			left = append(left, u)
		}
	}
	vm.undos = left
	return taken
}

// FlowHost is the runtime of flows, as the program of an instance asks
// it about other instances: flow.start, and wait on what it gave.
type FlowHost interface {
	// StartInstance starts an instance under a name, its fields data
	// with their "$type": detached, it is no one's child. A name taken
	// gives the instance that has it.
	StartInstance(name string, fields []byte, detached bool) error
	// Ended gives what waits for an instance gets once it has ended, as
	// data: Ok with what its run returned, or Err with a Cancelled.
	Ended(name string) (result []byte, ended bool, err error)
	// Message gives the next message of an inbox of the instance, as
	// data, taking it or not: there is none while ok is false.
	Message(inbox string, take bool) (data []byte, ok bool, err error)
	// Cancelled gives why the instance is cancelled, taking it or not:
	// it is not, or was taken already, while ok is false.
	Cancelled(take bool) (reason string, ok bool)
}

// SetFlowHost gives the program of an instance the runtime it runs in.
func (vm *VM) SetFlowHost(host FlowHost) {
	vm.flowHost = host
}

// Awaited are, from the OnIdle hook, the instances tasks wait for.
func (vm *VM) Awaited() []string {
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(vm.awaits))
	for name := range vm.awaits {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Arrived wakes the tasks that wait for an instance that has ended.
func (vm *VM) Arrived(name string) {
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range vm.awaits[name] {
		if t := s.tasks[id]; t != nil {
			s.wakeUp(t)
		}
	}
	delete(vm.awaits, name)
}

// message is the next message of an inbox, as a value.
func (vm *VM) message(args []Value, take bool) (Value, error) {
	host, err := vm.host("receive on an inbox")
	if err != nil {
		return NewNull(), err
	}
	inbox, err := argString(args, 0)
	if err != nil {
		return NewNull(), err
	}
	data, ok, err := host.Message(inbox, take)
	if err != nil || !ok {
		return NewNull(), err
	}
	return vm.ValueOfData(data)
}

// host is the runtime of flows, for the natives that need it.
func (vm *VM) host(what string) (FlowHost, error) {
	if vm.flowHost == nil {
		return nil, fmt.Errorf("%s is for a flow, run by the runtime of flows", what)
	}
	if vm.stepKey != "" {
		return nil, fmt.Errorf("%s is the flow's, not a step's", what)
	}
	return vm.flowHost, nil
}

// RunIn has the step run in another version of the program, from the
// OnIdle hook, before RunStep: the newest, so that a step fixed there is
// fixed for every instance at its next attempt. Only a step of the same
// name and signature there is taken; otherwise it runs where it was
// called, and RunIn says so.
func (c *StepCall) RunIn(chunk *Chunk) bool {
	idx := chunk.FindPrototype(c.fn.proto.Name)
	if idx < 0 || chunk == c.fn.chunk {
		return false
	}
	proto := &chunk.Prototypes[idx]
	if !proto.Step || proto.Signature != c.fn.proto.Signature || proto.ArgCount != c.fn.proto.ArgCount || proto.Once != c.fn.proto.Once {
		return false
	}
	fn, _, err := c.vm.heap.AllocFunction(chunk, idx, 0)
	if err != nil {
		return false
	}
	c.vm.pin(valueOf(&fn.header))
	c.fn = fn
	return true
}

// stepRun is a task that runs a step for the runtime of flows.
type stepRun struct {
	call    *StepCall
	key     string
	attempt int
	done    func(vm *VM, result Value, err error) error
}

// RunStep runs, from the OnIdle hook, a step a task waits for: in a task
// of its own, its body as the program would have run it, std.flow.key
// and attempt giving key and attempt. Steps run this way go on together
// while they wait for the world. done is told what came of it, with the
// turn; an error it returns ends the program.
func (vm *VM) RunStep(call *StepCall, key string, attempt int, done func(vm *VM, result Value, err error) error) {
	s := vm.sched
	t := &task{status: taskReady, wake: make(chan struct{}, 1), fn: valueOf(&call.fn.header), name: "step " + key}
	t.step = &stepRun{call: call, key: key, attempt: attempt, done: done}
	s.mu.Lock()
	s.lastID++
	t.id = s.lastID
	s.tasks[t.id] = t
	s.live++
	s.ready = append(s.ready, t)
	s.mu.Unlock()
	go vm.runTask(s, t)
}

// SleepUntil lets the task that asks wait, in a program run for a flow,
// until the clock reaches a moment: from the OnStep hook, a step asked
// to be run again later. Frozen meanwhile, the task is at the call.
func (vm *VM) SleepUntil(moment int64) error {
	for vm.now() < moment {
		if _, err := vm.sleepUntil(moment, "step"); err != nil {
			return err
		}
	}
	return nil
}

// SetOnStage sets what is asked when the program passes a stage of a
// flow, `stage polling;`: name is the stage's.
func (vm *VM) SetOnStage(hook func(vm *VM, name string) error) {
	vm.onStage = hook
}

// stageAt is the stage of the function running whose call is at ip.
func (vm *VM) stageAt() (*Stage, error) {
	proto := vm.currentFunction.proto
	for i := range proto.Stages {
		if proto.Stages[i].At == vm.ip {
			return &proto.Stages[i], nil
		}
	}
	return nil, fmt.Errorf("a stage passed in %s, which has none there", proto.Name)
}

// StageData gives, from the OnStage hook, the variables alive at the
// stage, as data: what the instance can be taken up from, in this
// version or another. A variable that is not data (a function, a task's
// handle) is an error.
func (vm *VM) StageData() (map[string]json.RawMessage, error) {
	stage, err := vm.stageAt()
	if err != nil {
		return nil, err
	}
	vars := make(map[string]json.RawMessage, len(stage.Vars))
	for _, v := range stage.Vars {
		data, err := vm.DataOf(vm.regs[v.Reg])
		if err != nil {
			return nil, fmt.Errorf("%s is not data: %v", v.Name, err)
		}
		vars[v.Name] = data
	}
	return vars, nil
}

// Alone reports whether the task that has the turn is the program's
// only one: a flow moves to a new version at a stage only then.
func (vm *VM) Alone() bool {
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live-s.background == 1 && s.running == s.main
}

// StartAtStage runs a function from one of its stages, its variables
// given as data, as Start runs it from its beginning: an instance taken
// up from a stage, in the version it was in or a newer one.
func (vm *VM) StartAtStage(chunk *Chunk, function, stage string, vars map[string]json.RawMessage) (result Value, err error) {
	if chunk == nil {
		return NewNull(), fmt.Errorf("cannot execute nil chunk")
	}
	if err := Verify(chunk); err != nil {
		return NewNull(), err
	}
	idx := chunk.FindPrototype(function)
	if idx == -1 {
		return NewNull(), fmt.Errorf("the program has no function %s", function)
	}
	proto := &chunk.Prototypes[idx]
	var at *Stage
	for i := range proto.Stages {
		if proto.Stages[i].Name == stage {
			at = &proto.Stages[i]
		}
	}
	if at == nil {
		return NewNull(), fmt.Errorf("%s has no stage %s", function, stage)
	}

	vm.execMu.Lock()
	defer vm.execMu.Unlock()
	defer vm.clearInterrupt()
	defer func() {
		if r := recover(); r != nil {
			vm.sched.stop()
			result, err = NewNull(), vm.internalError(r)
		}
	}()
	vm.reset(chunk)

	fn, _, err := vm.heap.AllocFunction(chunk, idx, 0)
	if err != nil {
		return NewNull(), err
	}
	vm.pin(valueOf(&fn.header))
	if proto.RegisterCount > len(vm.stk.vals) {
		vm.stk.grow(proto.RegisterCount)
	}
	vm.regs = vm.window(0, proto.RegisterCount)
	for i := range vm.regs {
		vm.regs[i] = valueNull
	}
	for _, v := range at.Vars {
		data, ok := vars[v.Name]
		if !ok {
			return NewNull(), fmt.Errorf("stage %s of %s has %s, which is not given", stage, function, v.Name)
		}
		value, err := vm.ValueOfData(data)
		if err != nil {
			return NewNull(), fmt.Errorf("%s: %v", v.Name, err)
		}
		vm.regs[v.Reg] = vm.pin(value)
	}
	// Right after the call that passed the stage
	vm.ip = at.At + 1
	vm.currentFunction = fn
	vm.pinned = vm.pinned[:0]
	return vm.finish(vm.run(0))
}

// TaskName is the name of the task that has the turn: "0" for the first,
// then the task that started it and how many that one had started,
// "0/task#2". The keys of a flow's steps hold it.
func (vm *VM) TaskName() string {
	return vm.sched.running.name
}

// InStep reports whether the task that has the turn runs a step: a step
// it calls is then a plain call.
func (vm *VM) InStep() bool {
	return vm.stepKey != ""
}

func init() {
	registerNatives("std.flow",
		// The key of the step running
		native{"key", 0, func(vm *VM, args []Value) (Value, error) {
			if vm.stepKey == "" {
				return NewNull(), fmt.Errorf("a step's key is known inside a step of a flow, run by the runtime of flows")
			}
			return vm.newString(vm.stepKey)
		}},
		// The step running asks to be run again, later
		native{"askAgain", 2, func(vm *VM, args []Value) (Value, error) {
			if vm.stepKey == "" {
				return NewNull(), fmt.Errorf("flow.retry is said inside a step of a flow, run by the runtime of flows")
			}
			reason, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			after, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if after < 0 {
				after = 0
			}
			return NewNull(), &RetryError{Reason: reason, After: after}
		}},
		// Which attempt of the step running this is
		native{"attempt", 0, func(vm *VM, args []Value) (Value, error) {
			if vm.stepKey == "" {
				return NewNull(), fmt.Errorf("a step's attempt is known inside a step of a flow, run by the runtime of flows")
			}
			return NewInt(int64(vm.stepTry)), nil
		}},
		// The instance passes a stage: what `stage name;` is
		native{"passStage", 1, func(vm *VM, args []Value) (Value, error) {
			if vm.onStage == nil {
				return NewNull(), nil
			}
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewNull(), vm.onStage(vm, name)
		}},
		// Starts an instance of another flow
		native{"startInstance", 3, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("flow.start")
			if err != nil {
				return NewNull(), err
			}
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			fields, err := vm.DataOf(args[1])
			if err != nil {
				return NewNull(), fmt.Errorf("the instance flow.start is given is data: %v", err)
			}
			return NewNull(), host.StartInstance(name, fields, args[2].IsBool() && args[2].AsBool())
		}},
		// Whether an instance has ended
		native{"instanceEnded", 1, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("wait on an instance")
			if err != nil {
				return NewNull(), err
			}
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			_, ended, err := host.Ended(name)
			return NewBool(ended), err
		}},
		// What waits for an instance that has ended gets
		native{"instanceResult", 1, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("wait on an instance")
			if err != nil {
				return NewNull(), err
			}
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			result, ended, err := host.Ended(name)
			if err != nil || !ended {
				return NewNull(), err
			}
			return vm.ValueOfData(result)
		}},
		// Whether a message has come to an inbox, what it is, and taking it
		native{"inboxReady", 1, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("receive on an inbox")
			if err != nil {
				return NewNull(), err
			}
			inbox, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			_, ok, err := host.Message(inbox, false)
			return NewBool(ok), err
		}},
		// Whether the instance is cancelled, and why
		native{"cancelReady", 0, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("flow.cancelled()")
			if err != nil {
				return NewNull(), err
			}
			_, ok := host.Cancelled(false)
			return NewBool(ok), nil
		}},
		native{"cancelReason", 1, func(vm *VM, args []Value) (Value, error) {
			host, err := vm.host("flow.cancelled()")
			if err != nil {
				return NewNull(), err
			}
			reason, _ := host.Cancelled(args[0].IsBool() && args[0].AsBool())
			return vm.newString(reason)
		}},
		native{"inboxPeek", 1, func(vm *VM, args []Value) (Value, error) {
			return vm.message(args, false)
		}},
		native{"inboxTake", 1, func(vm *VM, args []Value) (Value, error) {
			return vm.message(args, true)
		}},
		// Asks that the task that asks be woken when an instance ends
		native{"watchInstance", 1, func(vm *VM, args []Value) (Value, error) {
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s := vm.sched
			s.mu.Lock()
			defer s.mu.Unlock()
			if vm.awaits == nil {
				vm.awaits = map[string][]int64{}
			}
			for _, id := range vm.awaits[name] {
				if id == s.running.id {
					return NewNull(), nil
				}
			}
			vm.awaits[name] = append(vm.awaits[name], s.running.id)
			return NewNull(), nil
		}},
		// What undoes what was just done, should the flow end in failure
		native{"noteUndo", 2, func(vm *VM, args []Value) (Value, error) {
			h := header(args[0])
			if h == nil || h.Type != ObjTypeFunction || !(*FunctionObj)(unsafe.Pointer(h)).proto.Step {
				return NewNull(), fmt.Errorf("flow.undo is given a step fn, which the runtime runs as a step")
			}
			argument, err := vm.DataOf(args[1])
			if err != nil {
				return NewNull(), fmt.Errorf("what flow.undo gives its step is data: %v", err)
			}
			name := (*FunctionObj)(unsafe.Pointer(h)).proto.Name
			vm.undos = append(vm.undos, Undo{Step: name, Argument: argument, From: vm.stepKey})
			return NewNull(), nil
		}},
	)
	// The trials of flow.test are made by package flowtest, which runs a
	// flow in a machine of its own; it puts its natives in place of these.
	// A program that imports std.flow compiles without it
	for name, args := range map[string]int{
		"trialStart": 1, "trialReplace": 3, "trialGo": 1, "trialAdvance": 2,
		"trialCalls": 2, "trialResult": 1, "trialStatus": 1, "trialStopped": 1,
		"trialWrites": 1, "trialCopy": 2, "trialCompare": 3, "trialCancel": 2, "trialSend": 3,
	} {
		registerNatives("std.flow", native{name, args, func(vm *VM, args []Value) (Value, error) {
			return NewNull(), fmt.Errorf("flow.test runs under qoqi test")
		}})
	}
}

// RetryError is a step that said flow.retry: it ends the step, unwound
// as the program ending would be, for the runtime of flows to run it
// again after a while.
type RetryError struct {
	Reason string
	After  int64 // milliseconds
}

func (e *RetryError) Error() string { return "the step asked to be run again: " + e.Reason }

// SetOnStep sets what is asked before a step is called. When it answers
// (answered true), the call gives what it says and the step is not
// called by the program; otherwise the program calls it as any function.
func (vm *VM) SetOnStep(hook func(vm *VM, call *StepCall) (result Value, answered bool, err error)) {
	vm.onStep = hook
}

// stepCalled asks the OnStep hook about a call of a step.
func (vm *VM) stepCalled(fn *FunctionObj, args []Value) (Value, bool, error) {
	// Not with a defer: a task stopped as the program ends unwinds
	// without the turn
	vm.waiting = "step"
	result, answered, err := vm.onStep(vm, &StepCall{Name: fn.proto.Name, Args: args, Once: fn.proto.Once, ByQueue: fn.proto.ByQueue, fn: fn, vm: vm})
	vm.waiting = ""
	return result, answered, err
}

// Start runs a function of a chunk with the arguments given, to its end,
// as Exec runs main: the run of a flow, on its fields.
func (vm *VM) Start(chunk *Chunk, function string, args ...[]byte) (Value, error) {
	return vm.start(chunk, function, "", 0, args)
}

// StartStep runs a step on its own, as Start does, std.flow.key and
// attempt giving key and attempt: an undo of a flow that is ending.
func (vm *VM) StartStep(chunk *Chunk, step, key string, attempt int, args ...[]byte) (Value, error) {
	return vm.start(chunk, step, key, attempt, args)
}

func (vm *VM) start(chunk *Chunk, function, key string, attempt int, args [][]byte) (result Value, err error) {
	if chunk == nil {
		return NewNull(), fmt.Errorf("cannot execute nil chunk")
	}
	if err := Verify(chunk); err != nil {
		return NewNull(), err
	}
	idx := chunk.FindPrototype(function)
	if idx == -1 {
		return NewNull(), fmt.Errorf("the program has no function %s", function)
	}
	proto := &chunk.Prototypes[idx]
	if proto.ArgCount != len(args) {
		return NewNull(), fmt.Errorf("%s takes %d arguments, not %d", function, proto.ArgCount, len(args))
	}

	vm.execMu.Lock()
	defer vm.execMu.Unlock()
	defer vm.clearInterrupt()
	defer func() {
		if r := recover(); r != nil {
			vm.sched.stop()
			result, err = NewNull(), vm.internalError(r)
		}
	}()
	vm.reset(chunk)

	values := make([]Value, len(args))
	for i, arg := range args {
		v, err := vm.ValueOfData(arg)
		if err != nil {
			return NewNull(), err
		}
		values[i] = vm.pin(v)
	}
	fn, _, err := vm.heap.AllocFunction(chunk, idx, 0)
	if err != nil {
		return NewNull(), err
	}
	if proto.RegisterCount > len(vm.stk.vals) {
		vm.stk.grow(proto.RegisterCount)
	}
	vm.regs = vm.window(0, proto.RegisterCount)
	for i := range vm.regs {
		vm.regs[i] = valueNull
	}
	copy(vm.regs, values)
	vm.ip = proto.CodeStart
	vm.currentFunction = fn
	vm.pinned = vm.pinned[:0]
	vm.stepKey, vm.stepTry = key, attempt
	vm.stepGiven = nil
	if proto.ByQueue {
		vm.stepGiven = args
	}
	return vm.finish(vm.run(0))
}

// --- Data ---
//
// What a step is given and returns, and the fields of a flow, written as
// JSON a person can read and another program can write:
//
//	Int, Bool, String     a number, a boolean, a text
//	Float                 a number with a point or an exponent, 2.0;
//	                      {"$float": "NaN"} and the infinities
//	null                  null
//	an array              an array
//	a struct              an object, with "$type": {"$type": "Point", "x": 1}
//	a value of an enum    the same, its type "Enum.Variant", with "$variant": true
//
// Data holds no functions and no object twice.

// DataOf writes a value as data.
func (vm *VM) DataOf(v Value) ([]byte, error) {
	out, err := dataOf(v, map[*ObjHeader]bool{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// maxDataDepth is how deep data may be nested: as deep as encoding/json
// reads it back.
const maxDataDepth = 10000

func dataOf(v Value, inside map[*ObjHeader]bool) (any, error) {
	switch {
	case v.IsInt():
		return v.AsInt(), nil
	case v.IsBool():
		return v.AsBool(), nil
	case v.IsNull():
		return nil, nil
	case v.IsNumber():
		f := v.AsNumber()
		switch {
		case math.IsNaN(f):
			return map[string]string{"$float": "NaN"}, nil
		case math.IsInf(f, 1):
			return map[string]string{"$float": "+Inf"}, nil
		case math.IsInf(f, -1):
			return map[string]string{"$float": "-Inf"}, nil
		}
		text := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return json.Number(text), nil
	}
	h := header(v)
	if h == nil {
		return nil, fmt.Errorf("cannot write %v as data", v)
	}
	if inside[h] {
		return nil, fmt.Errorf("a value that holds itself is not data")
	}
	if len(inside) >= maxDataDepth {
		// What holds it is as deep: data read back is as deep at most
		return nil, fmt.Errorf("a value nested more than %d deep is not data", maxDataDepth)
	}
	inside[h] = true
	defer delete(inside, h)
	switch h.Type {
	case ObjTypeString:
		return (*StringObj)(unsafe.Pointer(h)).Data(), nil
	case ObjTypeArray:
		a := (*ArrayObj)(unsafe.Pointer(h))
		items := make([]any, 0, len(a.elements))
		for _, item := range a.elements {
			d, err := dataOf(item, inside)
			if err != nil {
				return nil, err
			}
			items = append(items, d)
		}
		return items, nil
	case ObjTypeObject:
		o := (*ObjectObj)(unsafe.Pointer(h))
		fields := ordered{keys: []string{"$type"}, values: map[string]any{"$type": o.shape.typeName}}
		if o.shape.variant {
			fields.add("$variant", true)
		}
		for i, key := range o.shape.keys {
			d, err := dataOf(o.slots[i], inside)
			if err != nil {
				return nil, fmt.Errorf("%v, in the field %s of %s", err, key, o.shape.typeName)
			}
			fields.add(key, d)
		}
		return fields, nil
	case ObjTypeIndex:
		i := (*IndexObj)(unsafe.Pointer(h))
		keys := make([]string, 0, len(i.at))
		for k := range i.at {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		at := ordered{}
		for _, k := range keys {
			at.add(k, i.at[k])
		}
		return ordered{keys: []string{"$index"}, values: map[string]any{"$index": at}}, nil
	case ObjTypeFunction, ObjTypeExternFunction:
		return nil, fmt.Errorf("a function is not data")
	}
	return nil, fmt.Errorf("cannot write an object of kind %d as data", h.Type)
}

// ordered is an object of JSON whose keys keep their order: the fields of
// a struct, in the order of its declaration.
type ordered struct {
	keys   []string
	values map[string]any
}

func (o *ordered) add(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(o.values[key])
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// readOrdered reads one value of JSON, its objects as ordered.
func readOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok := tok.(type) {
	case json.Delim:
		switch tok {
		case '{':
			o := &ordered{values: map[string]any{}}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				value, err := readOrdered(dec)
				if err != nil {
					return nil, err
				}
				o.add(key, value)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			items := []any{}
			for dec.More() {
				item, err := readOrdered(dec)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			_, err := dec.Token()
			return items, err
		}
	}
	return tok, nil
}

// ValueOfData makes a value of data DataOf wrote, or another program did.
func (vm *VM) ValueOfData(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	decoded, err := readOrdered(dec)
	if err != nil {
		return valueNull, fmt.Errorf("cannot read data %s: %v", data, err)
	}
	return vm.valueOfData(decoded)
}

func (vm *VM) valueOfData(d any) (Value, error) {
	switch d := d.(type) {
	case nil:
		return valueNull, nil
	case bool:
		return NewBool(d), nil
	case json.Number:
		text := string(d)
		if strings.ContainsAny(text, ".eE") {
			f, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return valueNull, err
			}
			return NewNumber(f), nil
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return valueNull, err
		}
		if n < MinInt || n > MaxInt {
			return valueNull, fmt.Errorf("the whole number %d does not fit in an Int", n)
		}
		return NewInt(n), nil
	case string:
		s, _, err := vm.heap.AllocString(d)
		if err != nil {
			return valueNull, err
		}
		return vm.pin(valueOf(&s.header)), nil
	case []any:
		a, _, err := vm.heap.AllocArray(len(d))
		if err != nil {
			return valueNull, err
		}
		v := vm.pin(valueOf(&a.header))
		for _, item := range d {
			iv, err := vm.valueOfData(item)
			if err != nil {
				return valueNull, err
			}
			a.elements = append(a.elements, iv)
		}
		return v, nil
	case *ordered:
		if f, ok := d.values["$float"]; ok {
			switch fmt.Sprint(f) {
			case "NaN":
				return NewNumber(math.NaN()), nil
			case "+Inf":
				return NewNumber(math.Inf(1)), nil
			case "-Inf":
				return NewNumber(math.Inf(-1)), nil
			}
			return valueNull, fmt.Errorf("a float %v", f)
		}
		if at, ok := d.values["$index"].(*ordered); ok {
			i, err := vm.heap.AllocIndex(len(at.keys))
			if err != nil {
				return valueNull, err
			}
			for _, k := range at.keys {
				n, err := strconv.Atoi(fmt.Sprint(at.values[k]))
				if err != nil {
					return valueNull, err
				}
				i.at[k] = int32(n)
			}
			return vm.pin(valueOf(&i.header)), nil
		}
		typeName, _ := d.values["$type"].(string)
		variant, _ := d.values["$variant"].(bool)
		// In the order the compiler lays a struct out, by name: an object
		// made of data has the shape of one the program makes
		var fields []string
		for _, key := range d.keys {
			if !strings.HasPrefix(key, "$") {
				fields = append(fields, key)
			}
		}
		sort.Strings(fields)
		o, _, err := vm.heap.AllocObjectOf(vm.heap.RootShape(typeName, variant), len(fields))
		if err != nil {
			return valueNull, err
		}
		v := vm.pin(valueOf(&o.header))
		for _, key := range fields {
			fv, err := vm.valueOfData(d.values[key])
			if err != nil {
				return valueNull, err
			}
			o.Set(key, fv)
		}
		return v, nil
	}
	return valueNull, fmt.Errorf("cannot make a value of %v", d)
}

// SetClock makes the time the program reads, std.time.now and what waits
// for the clock, what clock says: milliseconds since 1970.
func (vm *VM) SetClock(clock func() int64) {
	vm.clock = clock
}

// now is the time the program reads.
func (vm *VM) now() int64 {
	if vm.clock != nil {
		return vm.clock()
	}
	return time.Now().UnixMilli()
}

// Drawn is a value the program drew without a step: the time, from
// std.time.now, or a random number, from std.random. A runtime of flows
// keeps what an instance drew since its stage, to give it back when the
// code after the stage runs again (see SetOnDraw).
type Drawn struct {
	// Of is what drew it, with what it was asked: "now", "below 10",
	// "token 3". A value is given back only to the same question
	Of    string  `json:"of"`
	Int   int64   `json:"int,omitempty"`
	Float float64 `json:"float,omitempty"`
	Text  string  `json:"text,omitempty"`
}

// SetOnDraw sets what is asked each time the program draws a value
// without a step: hook is given the value just drawn, and what it
// returns is what the program gets. A runtime of flows keeps the values,
// and gives back those kept in place of new ones when it runs the code
// after a stage again. std.time.ticks is not asked about: it measures.
func (vm *VM) SetOnDraw(hook func(vm *VM, fresh Drawn) Drawn) {
	vm.onDraw = hook
}

// draw is a value the program drew, as the OnDraw hook has it.
func (vm *VM) draw(fresh Drawn) Drawn {
	if vm.onDraw == nil {
		return fresh
	}
	return vm.onDraw(vm, fresh)
}

// CallValue calls a function value of the program, from a native function
// the program called: the instruction loop is entered again.
func (vm *VM) CallValue(fn Value, args []Value) (Value, error) {
	return vm.callFunction(fn, args)
}

// Output is where the program prints.
func (vm *VM) Output() io.Writer {
	return vm.out
}

// FunctionName is the name of a function value of the program: that of
// the function it is, or of the lambda it was written as.
func FunctionName(v Value) (string, bool) {
	h := header(v)
	if h == nil || h.Type != ObjTypeFunction {
		return "", false
	}
	return (*FunctionObj)(unsafe.Pointer(h)).proto.Name, true
}

// Pin keeps a value alive while a native works with it, until the native
// returns.
func (vm *VM) Pin(v Value) Value {
	return vm.pin(v)
}

// Chunk is the bytecode the program runs.
func (vm *VM) Chunk() *Chunk {
	return vm.chunk
}
