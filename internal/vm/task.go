package vm

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Tasks: a program doing several things at once.
//
// A task is a function of the program running on its own: it has its own
// calls in progress and its own registers, and shares everything else with
// the other tasks, the heap first of all. The program itself, from main,
// is the first task.
//
// Only one task runs at a time. A task keeps running until it has to wait:
// for another task, for the clock, for an answer from the network. Then
// the next one that can go on does, and the first takes its turn again
// once what it waited for has come. Nothing switches tasks in the middle
// of a computation, so a task never sees a value half changed by another,
// and the heap and its collector need no locks.
//
// Each task has a goroutine of its own, which is where its calls from Go
// back into the program live (see callFunction): a task can wait in the
// middle of a function run by `attempt` without anything having to be
// unwound. The goroutines pass a single turn around: the VM's fields
// describe the task whose turn it is, and the others keep theirs in a
// context until it comes back to them.
//
// Waiting for the world (blocking) is the one moment a task's goroutine
// runs without the turn. It must not touch the VM or the heap then.

type taskStatus int

const (
	taskRunning   taskStatus = iota // It has the turn
	taskReady                       // It can go on, when its turn comes
	taskWaiting                     // For another task: to end, or to resume it
	taskFinishing                   // main has returned and waits for the others to end
	taskOutside                     // For the world: the clock, the network, another program
	taskEnded
)

// execution is what the VM's fields hold for the task whose turn it is, and
// what is put away for a task that waits.
type execution struct {
	frames     []CallFrame
	frameCount int
	ip         int
	stk        *regStack
	base       int
	regs       []Value
	function   *FunctionObj
	chunk      *Chunk
	pinned     []Value
	outcome    Value
	failure    *RuntimeError
	handlers   []handler
	pending    []pendingFailure
	depth      int    // loops run one inside the other (see Freeze)
	stepKey    string // the step it runs, for std.flow.key and attempt
	stepTry    int
	stepGiven  [][]byte
}

func (vm *VM) execution() execution {
	return execution{
		frames:     vm.frames,
		frameCount: vm.frameCount,
		ip:         vm.ip,
		stk:        vm.stk,
		base:       vm.base,
		regs:       vm.regs,
		function:   vm.currentFunction,
		chunk:      vm.chunk,
		pinned:     vm.pinned,
		outcome:    vm.outcome,
		failure:    vm.failure,
		handlers:   vm.handlers,
		pending:    vm.pending,
		depth:      vm.executing,
		stepKey:    vm.stepKey,
		stepTry:    vm.stepTry,
		stepGiven:  vm.stepGiven,
	}
}

func (vm *VM) setExecution(c execution) {
	vm.frames = c.frames
	vm.frameCount = c.frameCount
	vm.ip = c.ip
	vm.stk = c.stk
	vm.base = c.base
	vm.regs = c.regs
	vm.currentFunction = c.function
	vm.chunk = c.chunk
	vm.pinned = c.pinned
	vm.outcome = c.outcome
	vm.failure = c.failure
	vm.handlers = c.handlers
	vm.pending = c.pending
	vm.executing = c.depth
	vm.stepKey, vm.stepTry = c.stepKey, c.stepTry
	vm.stepGiven = c.stepGiven
}

// stackOf lists where a task is: the instruction being run, then the call
// that led to it, and so on outwards.
func stackOf(c *execution) []StackFrame {
	stack := make([]StackFrame, 0, c.frameCount+1)
	stack = append(stack, frameAt(c.chunk, c.ip, c.function))
	for i := c.frameCount - 1; i >= 0; i-- {
		frame := c.frames[i]
		stack = append(stack, frameAt(frame.function.chunk, frame.ip, frame.function))
	}
	return stack
}

// withoutStart leaves out of a task's calls the outermost ones, which are
// the library starting the task (std.task.start): the program's own
// function is where a task begins, for whoever reads a trace.
func withoutStart(stack []StackFrame) []StackFrame {
	for len(stack) > 1 && stack[len(stack)-1].File == "<std>/task.qi" {
		stack = stack[:len(stack)-1]
	}
	return stack
}

type task struct {
	id     int64
	status taskStatus
	wake   chan struct{} // Told when its turn comes
	fn     Value         // What it runs
	ctx    execution     // Where it is, while it does not have the turn

	waiters []*task       // Tasks waiting for it to end
	failure *RuntimeError // How it failed, until a task that waited takes it

	// Told when another task resumes it while it waits with a deadline
	// (suspendFor): it is outside then, not waiting, so that the clock
	// brings it back by itself and no deadlock is seen meanwhile
	poke chan struct{}

	// Its name, for the keys of a flow's steps: the task that started it,
	// then how many that one had started. main is "0"
	name    string
	started int
	// What it waits in, while it does not have the turn: suspend,
	// suspendFor, yield or step. A program run for a flow waits for the
	// clock without a goroutine of its own: until is when it is woken,
	// timedOut that it was woken by the clock rather than a task
	waiting  string
	until    int64
	timedOut bool
	// A task that runs a step of a flow for the runtime: told when it ends
	step *stepRun
	// A task started inside a step, and the step's key: the body of a
	// step is not durable, and waits as any program does
	inStep  bool
	stepKey string
	stepTry int
}

// scheduler passes the turn around. Its fields are guarded by mu: a task
// coming back from the world reaches it without having the turn.
type scheduler struct {
	mu      sync.Mutex
	main    *task
	running *task   // Who has the turn; nil while every task is outside
	ready   []*task // Who can go on, in the order they will
	// Every task that has not ended, and those that failed until their
	// failure has been taken
	tasks   map[int64]*task
	lastID  int64
	live    int // Tasks that have not ended, main included
	outside int // Tasks waiting for the world: they come back by themselves
	// Of those, the ones waiting in the background (for a signal): they
	// do not keep the program alive once the others are done
	background int
	fatal      error // Why the program is ending, once it is
	stopped    bool
	quit       chan struct{} // Closed when the program has ended

	// For a program run for a flow (SetOnIdle): what has the turn when
	// no task can go on, and what it asks
	idle   *task
	onIdle func(vm *VM) error
	now    func() int64

	chunk *Chunk // the program's bytecode, as Freeze names it
}

// fatalError ends the program from a task that is not main, or for a
// reason no task can do anything about. Like a request to exit, nothing
// catches it on the way out.
type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }

// leaving reports whether an error is the program ending, as opposed to a
// failure that `attempt` may recover from.
func leaving(err error) bool {
	switch err.(type) {
	case *exitError, *fatalError, *FrozenError, *RetryError, *WorkingError, *HeldError, *Stop:
		return true
	}
	return false
}

func newScheduler() *scheduler {
	main := &task{status: taskRunning, wake: make(chan struct{}, 1), fn: NewNull(), name: "0"}
	return &scheduler{
		main:    main,
		running: main,
		tasks:   map[int64]*task{0: main},
		live:    1,
		quit:    make(chan struct{}),
	}
}

// run gives a task the turn. Call with mu held.
func (s *scheduler) run(t *task) {
	t.status = taskRunning
	s.running = t
	t.wake <- struct{}{}
}

// makeReady puts a task that was waiting in line for the turn. Call with
// mu held.
func (s *scheduler) makeReady(t *task) {
	if t.status == taskWaiting || t.status == taskFinishing {
		t.status = taskReady
		s.ready = append(s.ready, t)
	}
}

// wakeUp lets a task that waits go on: one waiting for the turn is put in
// line for it, one waiting no longer than some time is told at once.
// Nothing happens to a task that does not wait. Call with mu held.
func (s *scheduler) wakeUp(t *task) {
	if t.status == taskWaiting || t.status == taskFinishing {
		s.makeReady(t)
	} else if t.poke != nil {
		select {
		case t.poke <- struct{}{}:
		default:
		}
	}
}

// dispatch passes the turn on, from a task that has just given it up and
// put its context away. Call with mu held.
func (s *scheduler) dispatch(from *task) {
	s.running = nil
	if s.fatal == nil {
		if len(s.ready) == 0 && s.outside == 0 && s.live > 0 && s.onIdle != nil {
			s.wakeDue()
		}
		if len(s.ready) > 0 {
			next := s.ready[0]
			s.ready = s.ready[1:]
			s.run(next)
			return
		}
		if s.outside > 0 || s.live == 0 {
			// Whoever comes back from the world takes the turn
			return
		}
		if s.onIdle != nil && from != s.idle {
			// No task can go on: the runtime of flows says what happens
			s.running = s.idle
			s.idle.wake <- struct{}{}
			return
		}
		s.fatal = &fatalError{s.deadlock(from)}
	}

	// The program is ending: only main goes on, to say so. If it is
	// outside, it finds out when it comes back.
	main := s.main
	if main.status == taskOutside || main.status == taskEnded {
		return
	}
	for i, t := range s.ready {
		if t == main {
			s.ready = append(s.ready[:i], s.ready[i+1:]...)
			break
		}
	}
	s.run(main)
}

// deadlock describes the state where every task waits for another: none
// will ever go on. The place given is where the task that went to wait
// last is waiting; failing that where main is, or the oldest task.
func (s *scheduler) deadlock(from *task) *RuntimeError {
	var waiting []*task
	for _, t := range s.tasks {
		if t.status == taskWaiting {
			waiting = append(waiting, t)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].id < waiting[j].id })

	at := from
	if at.status != taskWaiting {
		at = waiting[0]
	}
	if at.step != nil {
		for _, t := range waiting {
			if t.step == nil {
				at = t
				break
			}
		}
	}
	message := "deadlock: the task waits for something no other task is left to do"
	if len(waiting) > 1 {
		message = fmt.Sprintf("deadlock: %d tasks wait for each other, and none can go on", len(waiting))
	}
	// The wait itself is inside the library, in a channel's receive for
	// one: what tells is the line of the program that led there
	stack := withoutStart(stackOf(&at.ctx))
	for len(stack) > 1 && strings.HasPrefix(stack[0].File, "<std>/") {
		stack = stack[1:]
	}
	return &RuntimeError{Kind: KindDeadlock, Message: message, Stack: stack, instruction: at.ctx.ip}
}

// stop ends the program's tasks: those that wait never get the turn again.
func (s *scheduler) stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.quit)
	}
	s.mu.Unlock()
}

// resumed waits for the turn to come back to a task that gave it up, and
// takes up where the task was. For main, the error is the program ending
// meanwhile; another task never returns from here then.
func (vm *VM) resumed(s *scheduler, me *task) error {
	select {
	case <-me.wake:
	case <-s.quit:
		runtime.Goexit()
	}
	vm.setExecution(me.ctx)
	me.ctx = execution{}

	s.mu.Lock()
	fatal := s.fatal
	s.mu.Unlock()
	return fatal
}

// park gives up the turn. With taskReady the task only lets the others
// that can go on do so, and takes its turn again after them; with
// taskWaiting it waits until another task makes it ready. waiting is
// the native it waits in.
func (vm *VM) park(status taskStatus, waiting string) error {
	s := vm.sched
	me := s.running

	s.mu.Lock()
	if status == taskReady {
		if len(s.ready) == 0 {
			s.mu.Unlock()
			return nil
		}
		s.ready = append(s.ready, me)
	}
	me.status = status
	me.waiting = waiting
	me.ctx = vm.execution()
	s.dispatch(me)
	s.mu.Unlock()
	err := vm.resumed(s, me)
	me.waiting = ""
	return err
}

// sleepUntil waits, in a program run for a flow, until another task
// resumes this one or the clock reaches until, with no goroutine
// waiting for the clock: whether it was resumed.
func (vm *VM) sleepUntil(until int64, waiting string) (bool, error) {
	me := vm.sched.running
	me.until, me.timedOut = until, false
	err := vm.park(taskWaiting, waiting)
	resumed := !me.timedOut
	me.until, me.timedOut = 0, false
	return resumed, err
}

// wakeDue makes ready, in a program run for a flow, the tasks whose
// moment has come: the earliest first. Call with mu held.
func (s *scheduler) wakeDue() {
	now := s.now()
	var due []*task
	for _, t := range s.tasks {
		if t.status == taskWaiting && t.until > 0 && t.until <= now {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].until != due[j].until {
			return due[i].until < due[j].until
		}
		return due[i].id < due[j].id
	})
	for _, t := range due {
		t.timedOut = true
		s.makeReady(t)
	}
}

// NextWake is, from the OnIdle hook, the earliest moment a task waits
// for, in milliseconds since 1970: -1 if none waits for the clock.
func (vm *VM) NextWake() int64 {
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	next := int64(-1)
	for _, t := range s.tasks {
		if t.status == taskWaiting && t.until > 0 && !t.inStep && (next < 0 || t.until < next) {
			next = t.until
		}
	}
	return next
}

// SetOnIdle runs a program for a flow: when no task can go on, hook is
// asked what happens, with the turn. It may freeze the program, run the
// steps its tasks wait for (RunStep), or end it with an error, a
// FrozenError for one that sleeps. A task that waits for the clock
// waits without a goroutine of its own: it is woken when the clock
// (SetClock) reaches its moment, as the hook sees with NextWake. If the
// hook leaves no task able to go on, the program ends in a deadlock.
func (vm *VM) SetOnIdle(hook func(vm *VM) error) {
	vm.onIdle = hook
}

// idling is the goroutine that has the turn when no task can go on, in
// a program run for a flow.
func (vm *VM) idling(s *scheduler) {
	me := s.idle
	for {
		select {
		case <-me.wake:
		case <-s.quit:
			return
		}
		vm.setExecution(execution{stk: newRegStack(16), outcome: NewNull()})
		err := s.onIdle(vm)
		vm.pinned = vm.pinned[:0]

		s.mu.Lock()
		if err != nil && s.fatal == nil {
			s.fatal = err
			if _, frozen := err.(*FrozenError); !frozen {
				s.fatal = &fatalError{err}
			}
		}
		if s.fatal == nil && len(s.ready) == 0 && s.outside == 0 {
			s.fatal = &fatalError{s.deadlock(s.main)}
		}
		s.dispatch(me)
		s.mu.Unlock()
	}
}

// blocking runs something that waits for the world, letting the other
// tasks go on meanwhile. The function runs without the turn: it must not
// touch the VM or the heap, only Go values taken before and used after.
func (vm *VM) blocking(wait func()) error {
	s := vm.sched
	me := s.running

	s.mu.Lock()
	if s.live == 1 {
		// Nobody to let go on
		s.mu.Unlock()
		wait()
		return nil
	}
	me.status = taskOutside
	me.ctx = vm.execution()
	s.outside++
	s.dispatch(me)
	s.mu.Unlock()

	wait()

	s.mu.Lock()
	s.outside--
	if s.stopped || (s.fatal != nil && me != s.main) {
		// The program ended, or is ending, while this task was away
		s.mu.Unlock()
		runtime.Goexit()
	}
	if s.running != nil {
		me.status = taskReady
		s.ready = append(s.ready, me)
		s.mu.Unlock()
		return vm.resumed(s, me)
	}
	// Every other task is waiting too: the turn is free
	me.status = taskRunning
	s.running = me
	fatal := s.fatal
	s.mu.Unlock()
	vm.setExecution(me.ctx)
	me.ctx = execution{}
	return fatal
}

// suspendFor waits until another task resumes this one, or that long,
// whichever comes first. It reports whether it was resumed.
func (vm *VM) suspendFor(d time.Duration) (bool, error) {
	s := vm.sched
	if s.onIdle != nil && !s.running.inStep && s.running.step == nil {
		return vm.sleepUntil(vm.now()+d.Milliseconds(), "suspendFor")
	}
	me := s.running
	poke := make(chan struct{}, 1)
	s.mu.Lock()
	me.poke = poke
	s.mu.Unlock()

	quit := s.quit
	interrupted := vm.interruptSignal()
	resumed := false
	err := vm.blocking(func() {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-poke:
			resumed = true
		case <-timer.C:
		case <-quit:
		case <-interrupted:
		}
	})

	s.mu.Lock()
	me.poke = nil
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if vm.interrupted.Load() {
		return false, vm.interruption()
	}
	return resumed, nil
}

// spawn makes a task that will run a function of the program taking no
// arguments. It starts when its turn comes, not before the task that
// made it has to wait.
func (vm *VM) spawn(fn Value) (int64, error) {
	kind, ok := heapType(fn)
	if !ok || kind != ObjTypeFunction {
		return 0, fmt.Errorf("expected a function, got %s", typeName(fn))
	}
	proto := (*FunctionObj)(unsafe.Pointer(fn.AsHeapPtr())).Proto()
	if proto.ArgCount != 0 {
		return 0, fmt.Errorf("a task runs a function without arguments, %s takes %d", proto.Name, proto.ArgCount)
	}

	s := vm.sched
	t := &task{status: taskReady, wake: make(chan struct{}, 1), fn: fn}
	s.mu.Lock()
	s.lastID++
	t.id = s.lastID
	if parent := s.running; parent != nil {
		parent.started++
		t.name = fmt.Sprintf("%s/task#%d", parent.name, parent.started)
		t.inStep = parent.step != nil || parent.inStep
		t.stepKey, t.stepTry = vm.stepKey, vm.stepTry
	}
	s.tasks[t.id] = t
	s.live++
	s.ready = append(s.ready, t)
	s.mu.Unlock()

	go vm.runTask(s, t)
	return t.id, nil
}

// runTask is the goroutine of a task: it waits for its first turn, runs
// the function, and passes the turn on for good.
func (vm *VM) runTask(s *scheduler, t *task) {
	select {
	case <-t.wake:
	case <-s.quit:
		return
	}

	fn := (*FunctionObj)(unsafe.Pointer(t.fn.AsHeapPtr()))
	proto := fn.Proto()
	stack := newRegStack(proto.RegisterCount)
	regs := stack.vals[:proto.RegisterCount:proto.RegisterCount]
	for i := range regs {
		regs[i] = NewNull()
	}
	vm.setExecution(execution{stk: stack, regs: regs, function: fn, chunk: fn.chunk, ip: proto.CodeStart, outcome: NewNull()})
	if t.step != nil {
		copy(regs, t.step.call.Args)
		vm.stepKey, vm.stepTry = t.step.key, t.step.attempt
		vm.stepGiven = givenOf(vm, proto, t.step.call.Args)
	} else if t.inStep {
		vm.stepKey, vm.stepTry = t.stepKey, t.stepTry
	}
	vm.ended(s, t)
}

// ended runs a task from where its execution is to its end, and passes
// the turn on for good.
func (vm *VM) ended(s *scheduler, t *task) {
	result, err := vm.runGuarded()

	if t.step != nil {
		// A step run for the runtime of flows: what came of it is the
		// runtime's, not a failure of the program's
		if err != nil {
			vm.closeUpvalues(0)
		}
		fatal := t.step.done(vm, result, err)
		s.mu.Lock()
		t.status = taskEnded
		t.fn = NewNull()
		s.live--
		delete(s.tasks, t.id)
		if fatal != nil && s.fatal == nil {
			s.fatal = &fatalError{fatal}
		}
		s.dispatch(t)
		s.mu.Unlock()
		return
	}

	var failure *RuntimeError
	var fatal error
	switch e := err.(type) {
	case nil:
	case *RuntimeError:
		failure = e
	case *exitError:
		fatal = &fatalError{e}
	case *fatalError:
		fatal = e
	case *FrozenError:
		// Frozen from one of its waits: the whole program stops there
		fatal = e
	case *Stop:
		fatal = e
	default:
		failure = &RuntimeError{Kind: kindOf(err.Error()), Message: err.Error(), Stack: vm.callStack(), instruction: vm.ip}
	}
	if failure != nil {
		failure.Stack = withoutStart(failure.Stack)
		failure.atTheCall()
	}
	if err != nil {
		// A closure made by the task may live on in a value it shared,
		// still pointing at one of its variables
		vm.closeUpvalues(0)
	}

	s.mu.Lock()
	t.status = taskEnded
	t.fn = NewNull()
	t.failure = failure
	s.live--
	for _, waiter := range t.waiters {
		s.wakeUp(waiter)
	}
	t.waiters = nil
	if failure == nil {
		delete(s.tasks, t.id)
	}
	if fatal != nil && s.fatal == nil {
		s.fatal = fatal
	}
	if s.live-s.background == 1 && s.main.status == taskFinishing {
		s.makeReady(s.main)
	}
	s.dispatch(t)
	s.mu.Unlock()
}

// runGuarded runs a task's function. A mistake of the VM or of a native
// function ends the whole program, as an error, rather than the host.
func (vm *VM) runGuarded() (result Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = NewNull(), &fatalError{vm.internalError(r)}
		}
	}()
	return vm.run(0)
}

// finish ends the program once main has returned or failed: it lets the
// tasks still going reach their end, and says how the whole went.
func (vm *VM) finish(result Value, err error) (Value, error) {
	s := vm.sched
	me := s.main
	defer s.stop()

	if err == nil && s.onIdle == nil {
		// What main returned stays alive while the others run
		pinned := len(vm.pinned)
		vm.pin(result)
		s.mu.Lock()
		for s.live-s.background > 1 && err == nil {
			me.status = taskFinishing
			me.ctx = vm.execution()
			s.dispatch(me)
			s.mu.Unlock()
			err = vm.resumed(s, me)
			s.mu.Lock()
		}
		s.mu.Unlock()
		if err == nil {
			vm.pinned = vm.pinned[:pinned]
		}
	}
	if fatal, ok := err.(*fatalError); ok {
		err = fatal.err
	}
	if err != nil {
		if failure, ok := err.(*RuntimeError); ok {
			failure.atTheCall()
		}
		return NewNull(), err
	}

	// A task that failed with nobody waiting for it: its failure is the
	// program's, rather than lost
	s.mu.Lock()
	defer s.mu.Unlock()
	var lost *task
	for _, t := range s.tasks {
		if t.failure != nil && (lost == nil || t.id < lost.id) {
			lost = t
		}
	}
	if lost != nil {
		return NewNull(), lost.failure
	}
	return result, nil
}

// markTasks marks what the tasks that do not have the turn keep alive.
func (vm *VM) markTasks() {
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		vm.markValue(t.fn)
		c := &t.ctx
		if c.function != nil {
			vm.markObject(&c.function.header)
		}
		vm.markStack(c.stk, c.base+len(c.regs))
		if c.stk != vm.stk {
			// Where the task whose turn it is stopped last is not where
			// it is now
			clearAbove(c.stk, c.base+len(c.regs))
		}
		for i := 0; i < c.frameCount; i++ {
			if fn := c.frames[i].function; fn != nil {
				vm.markObject(&fn.header)
			}
		}
		vm.markValue(c.outcome)
		for _, v := range c.pinned {
			vm.markValue(v)
		}
	}
}

func (vm *VM) taskOf(args []Value) (*task, error) {
	id, err := argInt(args, 0)
	if err != nil {
		return nil, err
	}
	s := vm.sched
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id], nil
}

func init() {
	registerNatives("std.task",
		// Makes a task of a function without arguments; gives its number
		native{"spawn", 1, func(vm *VM, args []Value) (Value, error) {
			id, err := vm.spawn(args[0])
			if err != nil {
				return NewNull(), err
			}
			return NewInt(id), nil
		}},
		// The number of the task that asks; 0 is main
		native{"running", 0, func(vm *VM, args []Value) (Value, error) {
			return NewInt(vm.sched.running.id), nil
		}},
		// Lets the other tasks that can go on do so
		native{"yield", 0, func(vm *VM, args []Value) (Value, error) {
			if err := vm.waits("yield", 0); err != nil {
				return NewNull(), err
			}
			return NewNull(), vm.park(taskReady, "yield")
		}},
		// Waits until another task calls resume with this one's number.
		// Whoever calls it checks, once back, that what it waited for is
		// there, and waits again if not.
		native{"suspend", 0, func(vm *VM, args []Value) (Value, error) {
			if err := vm.waits("suspend", -1); err != nil {
				return NewNull(), err
			}
			return NewNull(), vm.park(taskWaiting, "suspend")
		}},
		// Waits as suspend does, but no longer than that many
		// milliseconds. Whether it was resumed rather than out of time
		native{"suspendFor", 1, func(vm *VM, args []Value) (Value, error) {
			ms, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if err := vm.waits("suspendFor", ms); err != nil {
				return NewNull(), err
			}
			if ms <= 0 {
				return NewBool(false), vm.park(taskReady, "suspendFor")
			}
			resumed, err := vm.suspendFor(time.Duration(ms) * time.Millisecond)
			return NewBool(resumed), err
		}},
		// Lets a task that waits go on. Nothing happens if it does not wait
		native{"resume", 1, func(vm *VM, args []Value) (Value, error) {
			t, err := vm.taskOf(args)
			if err != nil || t == nil {
				return NewNull(), err
			}
			s := vm.sched
			s.mu.Lock()
			s.wakeUp(t)
			s.mu.Unlock()
			return NewNull(), nil
		}},
		// Whether a task has not ended yet
		native{"alive", 1, func(vm *VM, args []Value) (Value, error) {
			t, err := vm.taskOf(args)
			if err != nil {
				return NewNull(), err
			}
			// A task back from the world writes its status without the turn
			s := vm.sched
			s.mu.Lock()
			defer s.mu.Unlock()
			return NewBool(t != nil && t.status != taskEnded), nil
		}},
		// Asks that the task that asks be woken when a task ends, once.
		// Nothing happens if it has ended already: whoever asks looks
		// again before it waits
		native{"watchTask", 1, func(vm *VM, args []Value) (Value, error) {
			t, err := vm.taskOf(args)
			if err != nil || t == nil {
				return NewNull(), err
			}
			s := vm.sched
			s.mu.Lock()
			defer s.mu.Unlock()
			if t.status == taskEnded {
				return NewNull(), nil
			}
			for _, waiter := range t.waiters {
				if waiter == s.running {
					return NewNull(), nil
				}
			}
			t.waiters = append(t.waiters, s.running)
			return NewNull(), nil
		}},
		// Milliseconds since 1970, as std.time.now: what waits for the
		// clock waits for a moment every process agrees on, so that a
		// program frozen in one is taken up in another
		native{"clock", 0, func(vm *VM, args []Value) (Value, error) {
			return NewInt(vm.now()), nil
		}},
		// How a task that has ended failed, as std.result.failure gives
		// it: its kind, its message, then four entries per call. Empty if
		// it did not fail, or if this was asked before: the failure is
		// given once.
		native{"fault", 1, func(vm *VM, args []Value) (Value, error) {
			t, err := vm.taskOf(args)
			if err != nil {
				return NewNull(), err
			}
			if t == nil || t.status != taskEnded || t.failure == nil {
				return vm.newStringArray(nil)
			}
			s := vm.sched
			s.mu.Lock()
			delete(s.tasks, t.id)
			s.mu.Unlock()

			parts := []string{string(t.failure.Kind), t.failure.Message}
			for _, frame := range t.failure.Stack {
				parts = append(parts, frame.Function, frame.File, fmt.Sprint(frame.Line), fmt.Sprint(frame.Column))
			}
			return vm.newStringArray(parts)
		}},
	)
}
