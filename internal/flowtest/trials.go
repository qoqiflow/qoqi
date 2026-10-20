// Package flowtest gives std.flow its trials: an instance of a flow run by
// the test that makes it, in a machine of its own, on a store in memory
// and a clock that only moves when the test moves it (see flow.test in
// std.flow, and docs/flow.md, "Testing a flow").
//
// The instance runs the program's own bytecode in another machine. A step
// the test replaced is called back in the test's machine; what crosses
// between the two is data, as what a step is given and returns always is.
package flowtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// trial is one instance under test.
type trial struct {
	host   *vm.VM
	chunk  *vm.Chunk
	flow   string
	fields []byte

	replaced map[string]vm.Value // step -> a function of the test's machine
	store    *flowrt.Memory
	rt       *flowrt.Runtime
	now      int64

	calls    map[string]int
	onceRuns map[string]int // by key: the runs of steps never run twice
}

const trialID = "trial"

var (
	mu     sync.Mutex
	trials = map[*vm.VM]map[int64]*trial{}
	last   int64
)

func register(host *vm.VM, t *trial) int64 {
	mu.Lock()
	defer mu.Unlock()
	last++
	if trials[host] == nil {
		trials[host] = map[int64]*trial{}
	}
	trials[host][last] = t
	return last
}

// Release forgets the trials of a machine done with: a process that runs
// tests again and again, as the daemon does for its console, keeps none.
func Release(host *vm.VM) {
	mu.Lock()
	defer mu.Unlock()
	delete(trials, host)
}

func find(host *vm.VM, args []vm.Value, i int) (*trial, error) {
	id, err := vm.ArgInt(args, i)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	t := trials[host][id]
	if t == nil {
		return nil, fmt.Errorf("no trial %d", id)
	}
	return t, nil
}

// newTrial makes a trial and starts its instance, the store failing at
// the write given (0 for none).
func newTrial(host *vm.VM, chunk *vm.Chunk, flow string, fields []byte, replaced map[string]vm.Value, failAt int) (*trial, error) {
	t := &trial{
		host: host, chunk: chunk, flow: flow, fields: fields,
		replaced: map[string]vm.Value{}, store: flowrt.NewMemory(),
		now: time.Now().UnixMilli(), calls: map[string]int{}, onceRuns: map[string]int{},
	}
	for k, v := range replaced {
		t.replaced[k] = v
	}
	out := host.Output()
	if out == nil {
		out = io.Discard
	}
	t.rt = flowrt.New(chunk, t.store, out)
	t.rt.Now = func() int64 { return t.now }
	t.rt.Ran = func(step, key string, once bool) {
		t.calls[step]++
		if once {
			t.onceRuns[key]++
		}
	}
	t.rt.Replace = map[string]func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error){}
	for name := range t.replaced {
		t.replace(name)
	}
	t.store.FailAt = failAt
	if _, err := t.rt.Start(trialID, flow, fields); err != nil {
		if !errors.Is(err, flowrt.ErrCrash) {
			return nil, err
		}
		// Stopped as it was being made: whoever started it starts it again
		t.store.FailAt = 0
		if _, err := t.rt.Start(trialID, flow, fields); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// replace gives the runtime the function that stands for a step: called
// in the test's machine, on the step's arguments made over as data there.
// What it gives flow.undo is the step's.
func (t *trial) replace(step string) {
	t.rt.Replace[step] = func(m *vm.VM, args []vm.Value) (vm.Value, []vm.Undo, error) {
		given := make([]vm.Value, len(args))
		for i, arg := range args {
			data, err := m.DataOf(arg)
			if err != nil {
				return vm.NewNull(), nil, err
			}
			v, err := t.host.ValueOfData(data)
			if err != nil {
				return vm.NewNull(), nil, err
			}
			given[i] = t.host.Pin(v)
		}
		t.host.TakeUndos("")
		result, err := t.host.CallValue(t.replaced[step], given)
		undos := t.host.TakeUndos("")
		if err != nil {
			return vm.NewNull(), nil, err
		}
		data, err := t.host.DataOf(result)
		if err != nil {
			return vm.NewNull(), nil, fmt.Errorf("what the function standing for %s returned: %v", step, err)
		}
		v, err := m.ValueOfData(data)
		return v, undos, err
	}
}

// run runs what is due until nothing is: a stop of the process is taken
// up at once, as a new process would.
func (t *trial) run() error {
	for rounds := 0; rounds < 100000; rounds++ {
		n, err := t.rt.RunDue()
		if errors.Is(err, flowrt.ErrCrash) {
			t.store.FailAt = 0
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return fmt.Errorf("the trial's instance did not settle")
}

func (t *trial) instance() *flowrt.Instance {
	inst, _ := t.store.Get(trialID)
	return inst
}

// stepName is the name of the step a function value is.
func stepName(v vm.Value) (string, error) {
	name, ok := vm.FunctionName(v)
	if !ok {
		return "", fmt.Errorf("a step is given by itself, as in trial.replace(charge, ...)")
	}
	return name, nil
}

func init() {
	vm.RegisterNative("std.flow.trialStart", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		fields, err := host.DataOf(args[0])
		if err != nil {
			return vm.NewNull(), fmt.Errorf("the instance of a trial is data: %v", err)
		}
		flow, err := typeOf(fields)
		if err != nil {
			return vm.NewNull(), err
		}
		t, err := newTrial(host, host.Chunk(), flow, fields, nil, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewInt(register(host, t)), nil
	})
	vm.RegisterNative("std.flow.trialReplace", 3, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		step, err := stepName(args[1])
		if err != nil {
			return vm.NewNull(), err
		}
		if _, ok := vm.FunctionName(args[2]); !ok {
			return vm.NewNull(), fmt.Errorf("what stands for %s is a function", step)
		}
		t.replaced[step] = args[2]
		t.replace(step)
		return vm.NewNull(), nil
	})
	vm.RegisterNative("std.flow.trialGo", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewNull(), t.run()
	})
	vm.RegisterNative("std.flow.trialAdvance", 2, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		ms, err := vm.ArgInt(args, 1)
		if err != nil {
			return vm.NewNull(), err
		}
		if ms < 0 {
			return vm.NewNull(), fmt.Errorf("a trial's clock goes forward, not by %d", ms)
		}
		return vm.NewNull(), flowrt.Pass(t.store, &t.now, ms, t.run)
	})
	vm.RegisterNative("std.flow.trialCancel", 2, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		reason, err := vm.ArgString(args, 1)
		if err != nil {
			return vm.NewNull(), err
		}
		err = t.rt.Cancel(trialID, reason)
		if errors.Is(err, flowrt.ErrCrash) {
			// Stopped as it was asked: whoever asked asks again
			t.store.FailAt = 0
			err = t.rt.Cancel(trialID, reason)
		}
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewNull(), t.run()
	})
	vm.RegisterNative("std.flow.trialSend", 3, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		inbox, err := vm.ArgString(args, 1)
		if err != nil {
			return vm.NewNull(), err
		}
		message, err := host.DataOf(args[2])
		if err != nil {
			return vm.NewNull(), fmt.Errorf("a message is data: %v", err)
		}
		if _, err := t.rt.Send(trialID, inbox, "", message); err != nil {
			return vm.NewNull(), err
		}
		return vm.NewNull(), t.run()
	})
	vm.RegisterNative("std.flow.trialCalls", 2, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		step, err := stepName(args[1])
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewInt(int64(t.calls[step])), nil
	})
	vm.RegisterNative("std.flow.trialResult", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		inst := t.instance()
		if inst == nil || inst.Status != flowrt.Done {
			return vm.NewNull(), nil
		}
		return host.ValueOfData(inst.Result)
	})
	vm.RegisterNative("std.flow.trialStatus", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		return host.ValueOfData(quoted(string(t.instance().Status)))
	})
	vm.RegisterNative("std.flow.trialStopped", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		inst := t.instance()
		if inst.Status != flowrt.Stopped {
			return vm.NewNull(), nil
		}
		return host.ValueOfData(quoted(inst.Stopped))
	})
	vm.RegisterNative("std.flow.trialWrites", 1, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewInt(int64(t.store.Writes())), nil
	})
	vm.RegisterNative("std.flow.trialCopy", 2, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		t, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		failAt, err := vm.ArgInt(args, 1)
		if err != nil {
			return vm.NewNull(), err
		}
		copied, err := newTrial(host, t.chunk, t.flow, t.fields, t.replaced, int(failAt))
		if err != nil {
			return vm.NewNull(), err
		}
		return vm.NewInt(register(host, copied)), nil
	})
	vm.RegisterNative("std.flow.trialCompare", 3, func(host *vm.VM, args []vm.Value) (vm.Value, error) {
		clean, err := find(host, args, 0)
		if err != nil {
			return vm.NewNull(), err
		}
		other, err := find(host, args, 1)
		if err != nil {
			return vm.NewNull(), err
		}
		write, _ := vm.ArgInt(args, 2)
		if wrong := compare(clean, other); wrong != "" {
			return host.ValueOfData(quoted(fmt.Sprintf("stopped at write %d of %d: %s", write, clean.store.Writes(), wrong)))
		}
		return vm.NewNull(), nil
	})
}

// compare says how a trial stopped along the way ended otherwise than one
// that was not, or "".
func compare(clean, other *trial) string {
	a, b := clean.instance(), other.instance()
	if a.Status != b.Status {
		return fmt.Sprintf("it is %s, and %s without the stop", b.Status, a.Status)
	}
	if string(a.Result) != string(b.Result) {
		return fmt.Sprintf("it gave %s, and %s without the stop", b.Result, a.Result)
	}
	if a.Stopped != b.Stopped {
		return fmt.Sprintf("it stopped with %q, and %q without the stop", b.Stopped, a.Stopped)
	}
	var twice []string
	for key, n := range other.onceRuns {
		if n > 1 {
			twice = append(twice, fmt.Sprintf("%s ran %d times", key[strings.LastIndex(key, ":")+1:], n))
		}
	}
	sort.Strings(twice)
	if len(twice) > 0 {
		return "a step never run twice ran twice: " + strings.Join(twice, ", ")
	}
	return ""
}

// typeOf is the flow an instance is, from its data.
func typeOf(fields []byte) (string, error) {
	text := string(fields)
	const marker = `"$type":"`
	i := strings.Index(text, marker)
	if i < 0 {
		return "", fmt.Errorf("the instance of a trial is a struct that implements Flow")
	}
	rest := text[i+len(marker):]
	return rest[:strings.Index(rest, `"`)], nil
}

func quoted(s string) []byte {
	data, _ := json.Marshal(s)
	return data
}
