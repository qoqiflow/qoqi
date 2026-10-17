package memguard

import (
	"testing"
	"time"
)

func TestWatchTellsOnceWhenTheLimitIsPassed(t *testing.T) {
	if InUse() == 0 {
		t.Fatal("the memory in use is not known")
	}

	// A limit already passed: told at the first look, and only then
	told := make(chan uint64, 4)
	stop := Watch(1, func(used uint64) { told <- used })
	defer stop()
	select {
	case used := <-told:
		if used <= 1 {
			t.Errorf("told with %d bytes in use", used)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never told")
	}
	select {
	case <-told:
		t.Error("told twice")
	case <-time.After(4 * interval):
	}
}

func TestWatchSaysNothingBelowTheLimit(t *testing.T) {
	told := make(chan uint64, 1)
	stop := Watch(1<<50, func(used uint64) { told <- used })
	time.Sleep(4 * interval)
	stop()
	select {
	case used := <-told:
		t.Errorf("told with %d bytes in use", used)
	default:
	}
}

// Held memory shows: what the guard measures follows what is allocated.
func TestInUseFollowsWhatIsHeld(t *testing.T) {
	before := InUse()
	held := make([]byte, 64<<20)
	for i := range held {
		held[i] = 1
	}
	if grown := InUse() - before; grown < 32<<20 {
		t.Errorf("64 MB more held, %d MB more measured", grown>>20)
	}
	_ = held[len(held)-1]
}
