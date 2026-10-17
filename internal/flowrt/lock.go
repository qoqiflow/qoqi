package flowrt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/flowlock"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// What flow.lock is, from flowlock: the daemon and the compiler read it.
const LockFormat = flowlock.LockFormat

type (
	Shapes    = flowlock.Shapes
	Lock      = flowlock.Lock
	StepShape = flowlock.StepShape
	Earlier   = flowlock.Earlier
	Change    = flowlock.Change
	Migration = flowlock.Migration
)

var (
	ReadLock   = flowlock.ReadLock
	WriteLock  = flowlock.WriteLock
	Changes    = flowlock.Changes
	Unanswered = flowlock.Unanswered
)

// StepsOf reads the steps by a queue of a program, as the daemon sends
// them.
func StepsOf(chunk *vm.Chunk) map[string]StepShape {
	steps := map[string]StepShape{}
	for _, p := range chunk.Prototypes {
		if !p.ByQueue {
			continue
		}
		name := p.Name[strings.LastIndex(p.Name, ".")+1:]
		steps[name] = StepShape{Shape: vm.StepShape(name, p.Signature), Signature: p.Signature}
	}
	if len(steps) == 0 {
		return nil
	}
	return steps
}

// StillSent are the versions before, from the lock of the version served
// last, whose instances still alive may send a step of a shape this
// version has not: alive counts the instances of a version that have not
// ended. What is said of each is for a person, who keeps the handlers of
// those shapes until none is said.
func StillSent(last *Lock, version string, steps map[string]StepShape, alive func(version string) int) ([]Earlier, []string) {
	if last == nil {
		return nil, nil
	}
	candidates := append([]Earlier(nil), last.Before...)
	if last.Version != version && len(last.Steps) > 0 {
		candidates = append([]Earlier{{Version: last.Version, Steps: last.Steps}}, candidates...)
	}
	var kept []Earlier
	var said []string
	seen := map[string]bool{}
	for _, e := range candidates {
		if seen[e.Version] || e.Version == version {
			continue
		}
		seen[e.Version] = true
		n := alive(e.Version)
		if n == 0 {
			continue
		}
		others := map[string]StepShape{}
		var names []string
		for name, s := range e.Steps {
			if now, ok := steps[name]; !ok || now.Shape != s.Shape {
				others[name] = s
				names = append(names, name)
			}
		}
		if len(others) == 0 {
			continue
		}
		sort.Strings(names)
		kept = append(kept, Earlier{Version: e.Version, Steps: others})
		for _, name := range names {
			said = append(said, fmt.Sprintf("%d instances of version %s may still send %s as %s (%s): keep its handler", n, short(e.Version), name, others[name].Shape, others[name].Signature))
		}
	}
	return kept, said
}

// short is a version's fingerprint, as a person reads it.
func short(version string) string {
	version = strings.TrimPrefix(version, "sha256:")
	if len(version) > 12 {
		return version[:12]
	}
	return version
}

// ShapesOf reads the shapes of the stages of the flows of a program.
func ShapesOf(chunk *vm.Chunk, flows []string) Shapes {
	shapes := Shapes{}
	for _, flow := range flows {
		stages := map[string]map[string]string{}
		idx := chunk.FindPrototype(flow + ".run")
		if idx < 0 {
			continue
		}
		for _, stage := range chunk.Prototypes[idx].Stages {
			stages[stage.Name] = stage.Shape
		}
		shapes[flow] = stages
	}
	return shapes
}
