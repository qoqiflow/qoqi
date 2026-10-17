// Package memguard keeps a process from taking the machine down with it.
//
// The VM counts what a program allocates and refuses more than its limit
// (see vm.Heap). That count covers the program's values, not everything
// the process holds for it: a text being built by a native function, the
// compiler's own data, a mistake in the runtime. When one of those grows
// without end, nothing in the VM notices, and the machine starts swapping
// long before the system steps in.
//
// A guard looks at what the process really holds, a few times a second,
// and ends it when that passes a limit.
package memguard

import (
	"fmt"
	"os"
	"runtime/metrics"
	"time"
)

const (
	total    = "/memory/classes/total:bytes"
	released = "/memory/classes/heap/released:bytes"

	// How often the memory is looked at. Allocating takes time too: what
	// can be taken between two looks is a fraction of any sensible limit.
	interval = 50 * time.Millisecond
)

// InUse is the memory the process holds: what it got from the system and
// has not given back.
func InUse() uint64 {
	samples := []metrics.Sample{{Name: total}, {Name: released}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 || samples[1].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return samples[0].Value.Uint64() - samples[1].Value.Uint64()
}

// Watch calls exceeded, once, when the process holds more than limit
// bytes. The function it returns stops the watching.
func Watch(limit uint64, exceeded func(used uint64)) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if used := InUse(); used > limit {
					exceeded(used)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// ExitStatus is what a process ended by its guard exits with.
const ExitStatus = 3

// Enforce ends the process when it holds more than limit bytes, having
// said why on the error output. what names the process for that message.
func Enforce(limit uint64, what string) {
	Watch(limit, func(used uint64) {
		fmt.Fprintf(os.Stderr, "[ERROR] R0006: out of memory: %s holds %d MB, more than the %d MB it may\n", what, used>>20, limit>>20)
		os.Exit(ExitStatus)
	})
}

// TestLimit is what a test binary may hold. No test of this repository
// needs a fraction of it; one that reaches it has gone wrong, and is
// better ended than left to fill the machine.
const TestLimit = 4 << 30

// EnforceInTests is for TestMain: it guards the test binary.
func EnforceInTests() {
	Enforce(TestLimit, "the tests")
}
