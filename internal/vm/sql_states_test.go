package vm

import "testing"

// The runtime of flows asks a machine for its step's transaction after
// every step: a machine that opened nothing is not kept for it. Each one
// kept held its whole heap, and a daemon grew by one machine a step.
func TestTakingNoTransactionKeepsNothing(t *testing.T) {
	machine := NewVM()
	tx, ended := machine.TakeStoreTx("order:1/b/1:main:charge#1")
	if tx != nil || ended {
		t.Fatalf("expected no transaction, got %v, ended %v", tx, ended)
	}
	if _, kept := sqlStates.Load(machine); kept {
		t.Error("the machine is kept, though it opened nothing")
	}
}

// Looking for a database or a transaction by its number, on a machine
// that has none, keeps nothing either.
func TestLookingForAHandleKeepsNothing(t *testing.T) {
	machine := NewVM()
	if h := machine.sqlOpen().get(1); h != nil {
		t.Fatalf("expected no handle, got %v", h)
	}
	if _, kept := sqlStates.Load(machine); kept {
		t.Error("the machine is kept, though it opened nothing")
	}
}
