package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unsafe"
)

// Freezing a program that waits, and taking it up again elsewhere.
//
// A task waits inside a native function: suspend, suspendFor or yield,
// or at the call of a step of a flow. There, everything that makes the
// program what it is at that moment is data of the virtual machine: the
// calls in progress of each task (functions, instruction pointers,
// registers), the objects of the heap they share, the variables of its
// modules. Freeze writes all of it; Thaw makes a virtual machine of it,
// on the same bytecode, and lets every task go on as if the native
// function it waited in had just returned.
//
// It is what a flow is kept as while it sleeps (see docs/flow.md).

// FreezeFormat names what Freeze writes.
const FreezeFormat = "qoqi.vm/2"

// FrozenError ends a program that its OnWait or OnIdle hook froze: Exec
// returns it, with what Freeze wrote, and Thaw takes the program up from
// there.
type FrozenError struct {
	State []byte
}

func (e *FrozenError) Error() string { return "the program was frozen while it waited" }

// Stop ends a program from one of its hooks, unwound as the program
// ending would be, nothing catching it on the way: Exec returns it, and
// Reason says why.
type Stop struct {
	Reason error
}

func (e *Stop) Error() string { return e.Reason.Error() }
func (e *Stop) Unwrap() error { return e.Reason }

// SetOnWait sets what is asked each time a task is about to wait: waiting
// is the native function it waits in. Returning a FrozenError stops the
// program there, to be taken up with Thaw.
func (vm *VM) SetOnWait(hook func(vm *VM, waiting string) error) {
	vm.onWait = hook
}

// WaitingFor says, from an OnWait hook, how long the task waits at most:
// milliseconds, or -1 for as long as it takes (until another task wakes it).
func (vm *VM) WaitingFor() int64 {
	return vm.waitingMs
}

// waits is called by the natives that make a task wait, before it does:
// ms is how long it waits at most, -1 for as long as it takes.
func (vm *VM) waits(native string, ms int64) error {
	if vm.onWait == nil {
		return nil
	}
	vm.waiting, vm.waitingMs = native, ms
	defer func() { vm.waiting = "" }()
	return vm.onWait(vm, native)
}

// frozenProgram is what Freeze writes.
type frozenProgram struct {
	Format string                     `json:"format"`
	Chunk  string                     `json:"chunk"`
	Heap   []frozenObject             `json:"heap"`
	Tasks  []frozenTask               `json:"tasks"`
	LastID int64                      `json:"lastID,omitempty"`
	Awaits map[string][]int64         `json:"awaits,omitempty"` // instances, and the tasks that wait for them
	Once   map[string]json.RawMessage `json:"once,omitempty"`
}

// frozenTask is one task: where it is, or, not started yet, what it will
// run, or, ended, the failure nobody has taken yet.
type frozenTask struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Started int    `json:"started,omitempty"`
	Waiting string `json:"waiting,omitempty"` // the native it waits in, or step
	Until   int64  `json:"until,omitempty"`   // when the clock wakes it
	Ready   int    `json:"ready,omitempty"`   // woken: its place in line for the turn, from 1

	Run     *int          `json:"run,omitempty"` // not started: its function
	Failure *RuntimeError `json:"failure,omitempty"`

	Stack    []json.RawMessage `json:"stack,omitempty"`
	Frames   []frozenFrame     `json:"frames,omitempty"`
	Function int               `json:"function"`
	IP       int               `json:"ip"`
	Base     int               `json:"base"`
	Size     int               `json:"size"`
	Open     []int             `json:"open,omitempty"`

	Waiters []int64 `json:"waiters,omitempty"` // the tasks woken when it ends
}

type frozenFrame struct {
	Function int `json:"function"`
	IP       int `json:"ip"`
	Base     int `json:"base"`
	Size     int `json:"size"`
}

// frozenObject is one object of the heap. Kind says which of the fields
// are its.
type frozenObject struct {
	Kind string `json:"kind"` // string, array, object, function, upvalue, extern, index

	Text    string            `json:"text,omitempty"`    // string
	Items   []json.RawMessage `json:"items,omitempty"`   // array; object: its values
	Type    string            `json:"type,omitempty"`    // object
	Variant bool              `json:"variant,omitempty"` // object
	Keys    []string          `json:"keys,omitempty"`    // object
	Proto   string            `json:"proto,omitempty"`   // function: its prototype, by name
	Refs    []int             `json:"refs,omitempty"`    // function: its upvalues
	Slot    *int              `json:"slot,omitempty"`    // upvalue, open
	Task    int64             `json:"task,omitempty"`    // upvalue, open: whose registers
	Closed  json.RawMessage   `json:"closed,omitempty"`  // upvalue, closed
	Name    string            `json:"name,omitempty"`    // extern
	At      map[string]int32  `json:"at,omitempty"`      // index
	Frozen  bool              `json:"frozen,omitempty"`  // a constant's
}

// Fingerprint names a chunk by its bytecode: what was frozen on one is
// taken up on the same.
func Fingerprint(chunk *Chunk) string {
	h := sha256.New()
	for _, instr := range chunk.Code {
		fmt.Fprintf(h, "%v;", instr)
	}
	for _, proto := range chunk.Prototypes {
		fmt.Fprintf(h, "%s@%d;", proto.Name, proto.CodeStart)
	}
	for _, s := range chunk.StringPool {
		fmt.Fprintf(h, "%q;", s)
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// Freeze writes the program as it is: from an OnWait or OnStep hook,
// while the task that has the turn waits or is about to call a step, or
// from an OnIdle hook, while none can go on. Every task is written. It
// refuses a task that waits for the world, waits in a function the
// program passed to a native one, or catches a failure.
func (vm *VM) Freeze() ([]byte, error) {
	s := vm.sched
	idle := s.idle != nil && s.running == s.idle
	if vm.waiting == "" && !idle {
		return nil, fmt.Errorf("a program is frozen while it waits or calls a step, from its OnWait, OnStep or OnIdle hook")
	}
	if s.chunk.Compiled != nil {
		return nil, fmt.Errorf("cannot freeze a program compiled to Go")
	}

	s.mu.Lock()
	var tasks []*task
	for _, t := range s.tasks {
		// The steps under way are not kept: their tasks are at the call
		if t.step == nil && !t.inStep {
			tasks = append(tasks, t)
		}
	}
	lastID := s.lastID
	ready := map[*task]int{}
	for i, t := range s.ready {
		ready[t] = i + 1
	}
	s.mu.Unlock()
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].id < tasks[j].id })

	w := &freezer{vm: vm, refs: map[*ObjHeader]int{}, stacks: map[*regStack]int64{}}
	contexts := make([]execution, len(tasks))
	for i, t := range tasks {
		contexts[i] = t.ctx
		if t == s.running && !idle {
			contexts[i] = vm.execution()
		}
		if contexts[i].stk != nil {
			w.stacks[contexts[i].stk] = t.id
		}
	}

	state := &frozenProgram{Format: FreezeFormat, Chunk: Fingerprint(s.chunk), LastID: lastID}
	s.mu.Lock()
	for name, ids := range vm.awaits {
		if state.Awaits == nil {
			state.Awaits = map[string][]int64{}
		}
		state.Awaits[name] = append([]int64(nil), ids...)
	}
	s.mu.Unlock()
	for i, t := range tasks {
		c := &contexts[i]
		frozen := frozenTask{ID: t.id, Name: t.name, Started: t.started, Waiting: t.waiting, Until: t.until, Ready: ready[t]}
		if t == s.running && !idle {
			frozen.Waiting = vm.waiting
			if vm.waiting == "suspendFor" && vm.waitingMs >= 0 {
				frozen.Until = vm.now() + vm.waitingMs
			}
		}
		for _, waiter := range t.waiters {
			frozen.Waiters = append(frozen.Waiters, waiter.id)
		}
		switch {
		case t.status == taskEnded:
			frozen.Failure = t.failure
		case t.status == taskOutside:
			return nil, fmt.Errorf("cannot freeze a task that waits for the world")
		case c.function == nil:
			run := w.ref(header(t.fn))
			frozen.Run = &run
		case c.depth > 1:
			return nil, fmt.Errorf("cannot freeze a program that waits inside a function given to a native one")
		case len(c.handlers) > 0 || len(c.pending) > 0:
			return nil, fmt.Errorf("cannot freeze a program that waits while it catches a failure")
		default:
			for _, v := range c.stk.vals[:c.base+len(c.regs)] {
				frozen.Stack = append(frozen.Stack, w.value(v))
			}
			for _, frame := range c.frames[:c.frameCount] {
				frozen.Frames = append(frozen.Frames, frozenFrame{Function: w.ref(&frame.function.header), IP: frame.ip, Base: frame.base, Size: frame.size})
			}
			frozen.Function = w.ref(&c.function.header)
			frozen.IP, frozen.Base, frozen.Size = c.ip, c.base, len(c.regs)
			for _, upval := range c.stk.open {
				frozen.Open = append(frozen.Open, w.ref(&upval.header))
			}
		}
		state.Tasks = append(state.Tasks, frozen)
	}
	if kept := vm.caches[s.chunk]; kept != nil {
		for i, v := range kept.once {
			if v != 0 {
				if state.Once == nil {
					state.Once = map[string]json.RawMessage{}
				}
				state.Once[s.chunk.Prototypes[i].Name] = w.value(v)
			}
		}
	}
	if err := w.drain(); err != nil {
		return nil, err
	}
	state.Heap = w.objects
	return json.Marshal(state)
}

type freezer struct {
	vm      *VM
	stacks  map[*regStack]int64 // whose registers, by task
	refs    map[*ObjHeader]int
	objects []frozenObject
	queue   []*ObjHeader
	err     error
}

// ref is the number of an object in the heap written, given the first
// time it is asked for.
func (w *freezer) ref(h *ObjHeader) int {
	if n, ok := w.refs[h]; ok {
		return n
	}
	n := len(w.objects)
	w.refs[h] = n
	w.objects = append(w.objects, frozenObject{})
	w.queue = append(w.queue, h)
	return n
}

// value writes a value: a whole number as a number, a float as
// {"float": x}, a boolean, null, an object as {"ref": n}.
func (w *freezer) value(v Value) json.RawMessage {
	var out any
	switch {
	case v.IsInt():
		out = v.AsInt()
	case v.IsBool():
		out = v.AsBool()
	case v.IsNull():
		out = nil
	case v.IsNumber():
		f := v.AsNumber()
		switch {
		case math.IsNaN(f):
			out = map[string]string{"float": "NaN"}
		case math.IsInf(f, 1):
			out = map[string]string{"float": "+Inf"}
		case math.IsInf(f, -1):
			out = map[string]string{"float": "-Inf"}
		default:
			out = map[string]float64{"float": f}
		}
	case v.IsHeapPtr():
		out = map[string]int{"ref": w.ref(header(v))}
	default:
		w.fail(fmt.Errorf("cannot freeze the value %v", v))
	}
	raw, _ := json.Marshal(out)
	return raw
}

func (w *freezer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

// drain writes every object reached, as long as writing one reaches more.
func (w *freezer) drain() error {
	for len(w.queue) > 0 {
		h := w.queue[0]
		w.queue = w.queue[1:]
		n := w.refs[h]
		w.objects[n] = w.object(h)
	}
	return w.err
}

func (w *freezer) object(h *ObjHeader) frozenObject {
	switch h.Type {
	case ObjTypeString:
		s := (*StringObj)(unsafe.Pointer(h))
		return frozenObject{Kind: "string", Text: s.Data()}
	case ObjTypeArray:
		a := (*ArrayObj)(unsafe.Pointer(h))
		out := frozenObject{Kind: "array", Items: []json.RawMessage{}, Frozen: h.Frozen}
		for _, v := range a.elements {
			out.Items = append(out.Items, w.value(v))
		}
		return out
	case ObjTypeObject:
		o := (*ObjectObj)(unsafe.Pointer(h))
		out := frozenObject{Kind: "object", Type: o.shape.typeName, Variant: o.shape.variant, Keys: append([]string{}, o.shape.keys...), Items: []json.RawMessage{}, Frozen: h.Frozen}
		for _, v := range o.slots[:len(o.shape.keys)] {
			out.Items = append(out.Items, w.value(v))
		}
		return out
	case ObjTypeFunction:
		f := (*FunctionObj)(unsafe.Pointer(h))
		if f.chunk != w.vm.sched.chunk {
			w.fail(fmt.Errorf("cannot freeze a function of another chunk: %s", f.proto.Name))
		}
		out := frozenObject{Kind: "function", Proto: f.proto.Name, Refs: []int{}}
		for _, upval := range f.upvalues {
			if upval == nil {
				out.Refs = append(out.Refs, -1)
				continue
			}
			out.Refs = append(out.Refs, w.ref(&upval.header))
		}
		return out
	case ObjTypeUpvalue:
		u := (*UpvalueObj)(unsafe.Pointer(h))
		if u.stack != nil {
			owner, ok := w.stacks[u.stack]
			if !ok {
				w.fail(fmt.Errorf("cannot freeze a variable of a task that is not kept"))
			}
			slot := u.slot
			return frozenObject{Kind: "upvalue", Slot: &slot, Task: owner}
		}
		return frozenObject{Kind: "upvalue", Closed: w.value(u.closed)}
	case ObjTypeExternFunction:
		e := (*ExternFunctionObj)(unsafe.Pointer(h))
		return frozenObject{Kind: "extern", Name: e.name}
	case ObjTypeIndex:
		i := (*IndexObj)(unsafe.Pointer(h))
		at := make(map[string]int32, len(i.at))
		for k, v := range i.at {
			at[k] = v
		}
		return frozenObject{Kind: "index", At: at}
	}
	w.fail(fmt.Errorf("cannot freeze an object of kind %d", h.Type))
	return frozenObject{}
}

// Thaw takes up a program Freeze wrote, on the chunk it was frozen on,
// and runs it to its end as Exec does. A task that waited to be resumed
// waits still; in a program run for a flow (SetOnIdle), one that waited
// for the clock waits for the same moment. Every other task goes on: the
// native function it waited in returns as if what it waited for had
// come, null, or false for suspendFor, and the library looks again and
// waits once more if it has not. A task frozen at a step calls it again.
func (vm *VM) Thaw(chunk *Chunk, data []byte) (result Value, err error) {
	var state frozenProgram
	if err := json.Unmarshal(data, &state); err != nil {
		return NewNull(), fmt.Errorf("cannot read a frozen program: %v", err)
	}
	if state.Format != FreezeFormat {
		return NewNull(), fmt.Errorf("a frozen program of format %q, not %q", state.Format, FreezeFormat)
	}
	if got := Fingerprint(chunk); got != state.Chunk {
		return NewNull(), fmt.Errorf("the program was frozen on other bytecode (%s, not %s)", state.Chunk, got)
	}
	if err := Verify(chunk); err != nil {
		return NewNull(), err
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

	t := &thawer{vm: vm, chunk: chunk, state: &state, protos: map[string]int{}, stacks: map[int64]*regStack{}}
	for i, proto := range chunk.Prototypes {
		t.protos[proto.Name] = i
	}
	main, err := t.thaw()
	if err != nil {
		vm.sched.stop()
		vm.pinned = vm.pinned[:0]
		return NewNull(), err
	}
	waiting := main.waiting
	vm.setExecution(main.ctx)
	main.ctx, main.waiting = execution{}, ""
	s := vm.sched
	var parked error
	switch {
	case main.status == taskWaiting && main.until > 0:
		_, parked = vm.sleepUntil(main.until, waiting)
	case main.status == taskWaiting:
		parked = vm.park(taskWaiting, waiting)
	case s.ready[0] != main:
		// Others were woken before it: they go first
		parked = vm.park(taskReady, waiting)
	default:
		s.ready = s.ready[1:]
		main.status = taskRunning
	}
	if parked != nil {
		return vm.finish(NewNull(), parked)
	}
	vm.wakeAt(waiting)
	return vm.finish(vm.run(0))
}

// asleep says whether a task taken up waits still: for a task to resume
// it, or, in a program run for a flow, for the clock.
func (t *thawer) asleep(waiting string, until int64) bool {
	return waiting == "suspend" || (waiting == "suspendFor" && until > 0 && t.vm.onIdle != nil)
}

// wakeAt lets a task taken up go on from the native it waited in.
func (vm *VM) wakeAt(waiting string) {
	if waiting == "step" {
		// Frozen as it was about to call a step: it calls it again, and
		// the OnStep hook says what it gave, or runs it
		return
	}
	given := valueNull
	if waiting == "suspendFor" {
		given = valueFalse
	}
	vm.regs[0] = given
	vm.ip++
}

// takenUp is the goroutine of a task taken up by Thaw.
func (vm *VM) takenUp(s *scheduler, t *task) {
	select {
	case <-t.wake:
	case <-s.quit:
		return
	}
	vm.setExecution(t.ctx)
	t.ctx = execution{}
	vm.wakeAt(t.waiting)
	t.waiting, t.until, t.timedOut = "", 0, false
	vm.ended(s, t)
}

type thawer struct {
	vm      *VM
	chunk   *Chunk
	state   *frozenProgram
	protos  map[string]int
	objects []Value
	stacks  map[int64]*regStack // the registers of each task
}

// thaw makes the heap and the tasks again, and gives main back, ready to
// run; the others are in line for the turn.
func (t *thawer) thaw() (*task, error) {
	vm, state := t.vm, t.state
	for _, frozen := range state.Tasks {
		if frozen.Failure == nil && frozen.Run == nil {
			size := len(frozen.Stack)
			if size < 1024 {
				size = 1024
			}
			stack := newRegStack(size)
			stack.used = len(frozen.Stack)
			t.stacks[frozen.ID] = stack
		}
	}

	// The objects first, empty, so that each can point at any other
	if err := t.make(); err != nil {
		return nil, err
	}
	if err := t.fill(); err != nil {
		return nil, err
	}

	s := vm.sched
	s.lastID = state.LastID
	vm.awaits = state.Awaits
	byID := map[int64]*task{}
	var main *task
	var woken []frozenTask
	for _, frozen := range state.Tasks {
		tk := s.main
		if frozen.ID != 0 {
			tk = &task{id: frozen.ID, wake: make(chan struct{}, 1), fn: NewNull()}
		}
		tk.name, tk.started, tk.waiting = frozen.Name, frozen.Started, frozen.Waiting
		if frozen.Ready == 0 && t.asleep(frozen.Waiting, frozen.Until) {
			tk.until = frozen.Until
			if frozen.Waiting != "suspendFor" {
				tk.until = 0
			}
		}
		byID[frozen.ID] = tk
		s.tasks[frozen.ID] = tk

		switch {
		case frozen.Failure != nil:
			tk.status, tk.failure = taskEnded, frozen.Failure
			continue
		case frozen.Run != nil:
			fn, err := t.function(*frozen.Run)
			if err != nil {
				return nil, err
			}
			tk.fn = valueOf(&fn.header)
		default:
			ctx, err := t.execution(&frozen)
			if err != nil {
				return nil, err
			}
			tk.ctx = ctx
		}
		if frozen.Run == nil && frozen.Ready == 0 && t.asleep(frozen.Waiting, frozen.Until) {
			tk.status = taskWaiting
		} else {
			tk.status = taskReady
			woken = append(woken, frozen)
		}
		if frozen.ID == 0 {
			main = tk
			continue
		}
		s.live++
		if frozen.Run != nil {
			go vm.runTask(s, tk)
		} else {
			go vm.takenUp(s, tk)
		}
	}
	if main == nil || main.ctx.function == nil {
		return nil, fmt.Errorf("a frozen program whose first task is not running")
	}
	// In line for the turn as they were; those that had it, or were about
	// to wait, after them
	sort.SliceStable(woken, func(i, j int) bool {
		a, b := woken[i].Ready, woken[j].Ready
		if a == 0 || b == 0 {
			return a != 0 && b == 0
		}
		return a < b
	})
	s.ready = nil
	for _, frozen := range woken {
		s.ready = append(s.ready, byID[frozen.ID])
	}
	for _, frozen := range state.Tasks {
		for _, id := range frozen.Waiters {
			if waiter := byID[id]; waiter != nil {
				byID[frozen.ID].waiters = append(byID[frozen.ID].waiters, waiter)
			}
		}
	}

	if len(state.Once) > 0 {
		kept := vm.cacheFor(t.chunk)
		kept.once = make([]Value, len(t.chunk.Prototypes))
		kept.making = make([]bool, len(t.chunk.Prototypes))
		for name, raw := range state.Once {
			i, ok := t.protos[name]
			if !ok {
				return nil, fmt.Errorf("a frozen program with a variable made by %s, which the bytecode has not", name)
			}
			v, err := t.value(raw)
			if err != nil {
				return nil, err
			}
			kept.once[i] = v
		}
	}
	vm.pinned = vm.pinned[:0]
	return main, nil
}

// execution is where a frozen task was.
func (t *thawer) execution(frozen *frozenTask) (execution, error) {
	stack := t.stacks[frozen.ID]
	for i, raw := range frozen.Stack {
		v, err := t.value(raw)
		if err != nil {
			return execution{}, err
		}
		stack.vals[i] = v
	}
	for _, ref := range frozen.Open {
		upval, err := t.upvalue(ref)
		if err != nil {
			return execution{}, err
		}
		stack.open = append(stack.open, upval)
	}
	sort.SliceStable(stack.open, func(i, j int) bool { return stack.open[i].slot < stack.open[j].slot })

	c := execution{stk: stack, chunk: t.chunk, ip: frozen.IP, base: frozen.Base, outcome: NewNull()}
	for _, frame := range frozen.Frames {
		fn, err := t.function(frame.Function)
		if err != nil {
			return execution{}, err
		}
		c.frames = append(c.frames, CallFrame{function: fn, ip: frame.IP, base: frame.Base, size: frame.Size})
	}
	c.frameCount = len(c.frames)
	fn, err := t.function(frozen.Function)
	if err != nil {
		return execution{}, err
	}
	if frozen.Base+frozen.Size > len(frozen.Stack) {
		return execution{}, fmt.Errorf("a frozen task whose registers go past its stack")
	}
	c.function = fn
	c.regs = stack.vals[frozen.Base : frozen.Base+frozen.Size : frozen.Base+frozen.Size]
	return c, nil
}

// make allocates every object, pinned until the program holds them.
func (t *thawer) make() error {
	heap := t.vm.heap
	for _, o := range t.state.Heap {
		var h *ObjHeader
		switch o.Kind {
		case "string":
			s, _, err := heap.AllocString(o.Text)
			if err != nil {
				return err
			}
			h = &s.header
		case "array":
			a, _, err := heap.AllocArray(len(o.Items))
			if err != nil {
				return err
			}
			h = &a.header
		case "object":
			obj, _, err := heap.AllocObjectOf(heap.RootShape(o.Type, o.Variant), len(o.Keys))
			if err != nil {
				return err
			}
			h = &obj.header
		case "function":
			proto, ok := t.protos[o.Proto]
			if !ok {
				return fmt.Errorf("a frozen program with a function %s, which the bytecode has not", o.Proto)
			}
			f, _, err := heap.AllocFunction(t.chunk, proto, len(o.Refs))
			if err != nil {
				return err
			}
			h = &f.header
		case "upvalue":
			var stack *regStack
			slot := 0
			if o.Slot != nil {
				stack, slot = t.stacks[o.Task], *o.Slot
				if stack == nil {
					return fmt.Errorf("a frozen variable of task %d, which is not kept", o.Task)
				}
			}
			u, _, err := heap.AllocUpvalue(stack, slot)
			if err != nil {
				return err
			}
			h = &u.header
		case "extern":
			v, err := t.vm.externObject(o.Name)
			if err != nil {
				return err
			}
			h = header(v)
		case "index":
			i, err := heap.AllocIndex(len(o.At))
			if err != nil {
				return err
			}
			h = &i.header
		default:
			return fmt.Errorf("a frozen object of kind %q", o.Kind)
		}
		h.Frozen = o.Frozen
		v := valueOf(h)
		t.objects = append(t.objects, v)
		t.vm.pin(v)
	}
	return nil
}

// fill gives every object what it holds.
func (t *thawer) fill() error {
	for n, o := range t.state.Heap {
		h := header(t.objects[n])
		switch o.Kind {
		case "array":
			a := (*ArrayObj)(unsafe.Pointer(h))
			for _, raw := range o.Items {
				v, err := t.value(raw)
				if err != nil {
					return err
				}
				a.elements = append(a.elements, v)
			}
		case "object":
			obj := (*ObjectObj)(unsafe.Pointer(h))
			if len(o.Keys) != len(o.Items) {
				return fmt.Errorf("a frozen object of %d fields and %d values", len(o.Keys), len(o.Items))
			}
			for i, key := range o.Keys {
				v, err := t.value(o.Items[i])
				if err != nil {
					return err
				}
				obj.Set(key, v)
			}
		case "function":
			f := (*FunctionObj)(unsafe.Pointer(h))
			for i, ref := range o.Refs {
				if ref < 0 {
					continue
				}
				upval, err := t.upvalue(ref)
				if err != nil {
					return err
				}
				f.upvalues[i] = upval
			}
		case "upvalue":
			if o.Slot == nil {
				u := (*UpvalueObj)(unsafe.Pointer(h))
				v, err := t.value(o.Closed)
				if err != nil {
					return err
				}
				u.closed = v
			}
		case "index":
			i := (*IndexObj)(unsafe.Pointer(h))
			for k, v := range o.At {
				i.at[k] = v
			}
		}
	}
	return nil
}

func (t *thawer) object(ref int) (*ObjHeader, error) {
	if ref < 0 || ref >= len(t.objects) {
		return nil, fmt.Errorf("a frozen program that names object %d of %d", ref, len(t.objects))
	}
	return header(t.objects[ref]), nil
}

func (t *thawer) function(ref int) (*FunctionObj, error) {
	h, err := t.object(ref)
	if err != nil {
		return nil, err
	}
	if h.Type != ObjTypeFunction {
		return nil, fmt.Errorf("a frozen program whose call is of an object that is not a function")
	}
	return (*FunctionObj)(unsafe.Pointer(h)), nil
}

func (t *thawer) upvalue(ref int) (*UpvalueObj, error) {
	h, err := t.object(ref)
	if err != nil {
		return nil, err
	}
	if h.Type != ObjTypeUpvalue {
		return nil, fmt.Errorf("a frozen program whose variable is not one")
	}
	return (*UpvalueObj)(unsafe.Pointer(h)), nil
}

// value reads back what freezer.value wrote.
func (t *thawer) value(raw json.RawMessage) (Value, error) {
	var decoded any
	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return valueNull, fmt.Errorf("cannot read a frozen value %s: %v", raw, err)
	}
	switch v := decoded.(type) {
	case nil:
		return valueNull, nil
	case bool:
		return NewBool(v), nil
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return valueNull, fmt.Errorf("a frozen whole number %s: %v", v, err)
		}
		return NewInt(n), nil
	case map[string]any:
		if ref, ok := v["ref"]; ok {
			n, err := strconv.Atoi(fmt.Sprint(ref))
			if err != nil {
				return valueNull, err
			}
			h, err := t.object(n)
			if err != nil {
				return valueNull, err
			}
			return valueOf(h), nil
		}
		if f, ok := v["float"]; ok {
			switch text := fmt.Sprint(f); text {
			case "NaN":
				return NewNumber(math.NaN()), nil
			case "+Inf":
				return NewNumber(math.Inf(1)), nil
			case "-Inf":
				return NewNumber(math.Inf(-1)), nil
			default:
				x, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return valueNull, err
				}
				return NewNumber(x), nil
			}
		}
	}
	return valueNull, fmt.Errorf("a frozen value %s that is no value", raw)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
