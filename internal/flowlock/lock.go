// Package flowlock is flow.lock: the shapes of the stages of a program as
// it was deployed last, and what a new version changed of them. The daemon
// reads it to refuse what would strand an instance; the compiler, to say
// it where the program is written.
package flowlock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// LockFormat names what flow.lock holds.
const LockFormat = "qoqi.flow.lock/1"

// Shapes are the shapes of the stages of a program's flows: by flow, by
// stage, the type of each variable alive there and of each field of the
// flow, "self.name".
type Shapes map[string]map[string]map[string]string

// Lock is flow.lock: the shapes of the stages of the program as it was
// last served, and its version. It lives next to the program, to be kept
// with its source: what a new version changes of a stage is said there.
type Lock struct {
	Format  string `json:"format"`
	Version string `json:"version"`
	Flows   Shapes `json:"flows"`

	// Steps are the steps the application carries out, by name, with
	// their shape; Before, those of the versions before that instances
	// still alive run, which may still send their own
	Steps  map[string]StepShape `json:"steps,omitempty"`
	Before []Earlier            `json:"before,omitempty"`
}

// StepShape is a step by a queue: its shape, and its signature.
type StepShape struct {
	Shape     string `json:"shape"`
	Signature string `json:"signature"`
}

// Earlier is a version before, by its fingerprint, and its steps by a
// queue.
type Earlier struct {
	Version string               `json:"version"`
	Steps   map[string]StepShape `json:"steps"`
}

// ReadLock reads a flow.lock; one that is not there is nil.
func ReadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if lock.Format != LockFormat {
		return nil, fmt.Errorf("%s is of format %q, not %q", path, lock.Format, LockFormat)
	}
	return &lock, nil
}

// WriteLock writes a flow.lock, its entries in order, so that a change
// reads as one in a diff.
func WriteLock(path string, lock *Lock) error {
	lock.Format = LockFormat
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetIndent("", "  ")
	// Inbox<Answer>, as the source writes it
	enc.SetEscapeHTML(false)
	if err := enc.Encode(lock); err != nil {
		return err
	}
	return os.WriteFile(path, data.Bytes(), 0o644)
}

// Change is what a newer version changed of a stage an older one had.
type Change struct {
	Flow, Stage string
	What        string
}

func (c Change) String() string {
	return fmt.Sprintf("stage %s of %s: %s", c.Stage, c.Flow, c.What)
}

// SameType says whether a variable of a stage has the same type in two
// shapes. A type made of structs or enums carries the hash of their
// layout after a #; a shape written before layouts were has none, and its
// types are compared by name alone, so that a lock or a version of then
// is not taken as changed everywhere.
func SameType(a, b string) bool {
	if a == b {
		return true
	}
	ia, ib := strings.LastIndex(a, "#"), strings.LastIndex(b, "#")
	if ia >= 0 && ib >= 0 {
		return false
	}
	if ia >= 0 {
		a = a[:ia]
	}
	if ib >= 0 {
		b = b[:ib]
	}
	return a == b
}

// Changes says what newer changed of the stages old had: a stage gone, a
// variable added, gone, or of another type. A stage added is no change:
// no instance is at it yet.
func Changes(old, newer Shapes) []Change {
	var changes []Change
	for _, flow := range sortedKeys(old) {
		for _, stage := range sortedKeys(old[flow]) {
			before := old[flow][stage]
			after, ok := newer[flow][stage]
			if !ok {
				changes = append(changes, Change{flow, stage, "it is gone"})
				continue
			}
			var said []string
			for _, name := range sortedKeys(before) {
				switch t, ok := after[name]; {
				case !ok:
					said = append(said, name+" is gone")
				case !SameType(before[name], t):
					said = append(said, fmt.Sprintf("%s: %s, now %s", name, before[name], t))
				}
			}
			for _, name := range sortedKeys(after) {
				if _, ok := before[name]; !ok {
					said = append(said, fmt.Sprintf("%s: %s is new", name, after[name]))
				}
			}
			if len(said) > 0 {
				changes = append(changes, Change{flow, stage, strings.Join(said, "; ")})
			}
		}
	}
	return changes
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Migration is a migrate fn, as the daemon checks it against flow.lock:
// the flow, the stage of the version before it is named after, and the
// type of each of its parameters.
type Migration struct {
	Flow, Stage string
	Params      map[string]string
}

// Unanswered says which of the changes no migration answers. A change
// of the fields of a flow is answered by its migrate fn fields, whose
// parameters are fields it had, of their types; a change of a stage's
// other variables, or a stage gone, by a migrate fn named after the
// stage, whose parameters are variables the stage had.
func Unanswered(changes []Change, old, newer Shapes, migrations []Migration) []Change {
	by := map[string]Migration{}
	for _, m := range migrations {
		by[m.Flow+"."+m.Stage] = m
	}
	var left []Change
	checked := map[string]bool{}
	for _, change := range changes {
		before, after := old[change.Flow][change.Stage], newer[change.Flow][change.Stage]
		fieldsChanged, variablesChanged := after == nil, after == nil
		for name, t := range before {
			if after != nil && after[name] != t {
				if strings.HasPrefix(name, "self") {
					fieldsChanged = true
				} else {
					variablesChanged = true
				}
			}
		}
		for name := range after {
			if _, ok := before[name]; !ok {
				if strings.HasPrefix(name, "self") {
					fieldsChanged = true
				} else {
					variablesChanged = true
				}
			}
		}

		if fieldsChanged && after != nil {
			m, ok := by[change.Flow+".fields"]
			switch {
			case !ok:
				left = append(left, Change{change.Flow, change.Stage, change.What + ", and no migrate fn fields says what an instance's fields become"})
			case !checked[change.Flow]:
				checked[change.Flow] = true
				for _, name := range sortedKeys(m.Params) {
					switch t, ok := before["self."+name]; {
					case !ok:
						left = append(left, Change{change.Flow, "fields", fmt.Sprintf("migrate fn fields takes %s, which the flow had not", name)})
					case !SameType(t, m.Params[name]):
						left = append(left, Change{change.Flow, "fields", fmt.Sprintf("migrate fn fields takes %s as a %s; it was a %s", name, m.Params[name], t)})
					}
				}
			}
		}
		if !variablesChanged {
			continue
		}
		m, ok := by[change.Flow+"."+change.Stage]
		if !ok {
			left = append(left, Change{change.Flow, change.Stage, change.What + ", and no migrate fn " + change.Stage + " says how an instance there goes on"})
			continue
		}
		for _, name := range sortedKeys(m.Params) {
			switch t, ok := before[name]; {
			case !ok:
				left = append(left, Change{change.Flow, change.Stage, fmt.Sprintf("migrate fn %s takes %s, which the stage had not", change.Stage, name)})
			case !SameType(t, m.Params[name]):
				left = append(left, Change{change.Flow, change.Stage, fmt.Sprintf("migrate fn %s takes %s as a %s; it was a %s there", change.Stage, name, m.Params[name], t)})
			}
		}
	}
	return left
}
