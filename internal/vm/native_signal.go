package vm

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Signals: a program that listens for them is told when it is asked to
// stop (Ctrl-C, or kill sending SIGTERM), and stops the way it chooses: a
// server finishes the requests it is answering, a tool removes its
// temporary files.
//
// Each listener of the program has a channel of its own here, and every
// signal goes to all of them. Waiting for one is waiting in the
// background: it does not keep a program alive whose other tasks are
// done.

type signalState struct {
	mu          sync.Mutex
	subscribers map[int64]chan string
	last        int64
	terminate   chan os.Signal // SIGTERM, asked for once a program listens
}

var signalStates sync.Map // *VM -> *signalState

func (vm *VM) signals() *signalState {
	if state, ok := signalStates.Load(vm); ok {
		return state.(*signalState)
	}
	state, _ := signalStates.LoadOrStore(vm, &signalState{subscribers: map[int64]chan string{}})
	return state.(*signalState)
}

// Deliver gives a signal to the program, from any thread: "interrupt"
// for Ctrl-C, "terminate" for SIGTERM. It reports whether the program
// listens for signals; when it does not, it is up to the caller to stop
// it (see Interrupt).
func (vm *VM) Deliver(name string) bool {
	s := vm.signals()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, subscriber := range s.subscribers {
		select {
		case subscriber <- name:
		default: // A listener that has not taken the last ones yet
		}
	}
	return len(s.subscribers) > 0
}

// subscribe makes a listener, and has SIGTERM delivered from then on.
func (s *signalState) subscribe(vm *VM) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminate == nil {
		s.terminate = make(chan os.Signal, 1)
		signal.Notify(s.terminate, syscall.SIGTERM)
		go func() {
			for range s.terminate {
				if !vm.Deliver("terminate") {
					os.Exit(143)
				}
			}
		}()
	}
	s.last++
	s.subscribers[s.last] = make(chan string, 4)
	return s.last
}

// waitInBackground waits for something from outside without keeping the
// program alive: once main has returned and the tasks that are not in
// the background have ended, the program ends.
func (vm *VM) waitInBackground(wait func()) error {
	s := vm.sched
	s.mu.Lock()
	s.background++
	if s.live-s.background == 1 && s.main.status == taskFinishing {
		s.makeReady(s.main)
	}
	s.mu.Unlock()

	err := vm.blocking(wait)

	s.mu.Lock()
	s.background--
	s.mu.Unlock()
	return err
}

func init() {
	registerNatives("std.os",
		// A listener of signals: its number
		native{"listenSignals", 0, func(vm *VM, args []Value) (Value, error) {
			return NewInt(vm.signals().subscribe(vm)), nil
		}},
		// The next signal a listener hears: "interrupt" or "terminate"
		native{"nextSignal", 1, func(vm *VM, args []Value) (Value, error) {
			id, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s := vm.signals()
			s.mu.Lock()
			subscriber := s.subscribers[id]
			s.mu.Unlock()
			if subscriber == nil {
				return NewNull(), nil
			}
			quit := vm.sched.quit
			var name string
			if gone := vm.waitInBackground(func() {
				select {
				case name = <-subscriber:
				case <-quit:
				}
			}); gone != nil {
				return NewNull(), gone
			}
			return vm.newString(name)
		}},
	)
}
