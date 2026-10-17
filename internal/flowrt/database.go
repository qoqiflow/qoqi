package flowrt

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/qoqiflow/qoqi/internal/sqldb"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A step that writes only to the database the flows are kept in asks
// for it with flow.store(): a transaction there, in which the runtime
// keeps what the step gives, and commits. The step's rows and its result
// are kept together or not at all, so that a step stopped half way runs
// again from nothing, and one that ended is never run again: certain,
// without a key in its rows.

// database is what flow.store() opens: the store's own database, or, on
// a store that is not in one (in memory, for a trial), a database in
// memory of the runtime's own, where a step's rows are committed just
// before its result is kept.
func (r *Runtime) database() (*sql.DB, sqldb.Kind, error) {
	if in, ok := r.Store.(InDatabase); ok {
		db, kind := in.Database()
		return db, kind, nil
	}
	r.scratchOnce.Do(func() {
		var name [8]byte
		rand.Read(name[:])
		r.scratch, r.scratchErr = sql.Open("sqlite", "file:flows-"+hex.EncodeToString(name[:])+"?mode=memory&cache=shared")
		if r.scratchErr == nil {
			// The database lives as long as a connection to it
			r.scratch.SetMaxOpenConns(1)
			r.scratch.SetConnMaxIdleTime(0)
			r.scratch.SetConnMaxLifetime(0)
		}
	})
	return r.scratch, sqldb.SQLite, r.scratchErr
}

// keep keeps what a step gave: in the transaction it wrote in, when it
// asked for the store, committed with it.
func (r *Runtime) keep(tx *sql.Tx, key string, kept Kept) error {
	if tx == nil {
		return r.Store.PutResult(key, kept)
	}
	if in, ok := r.Store.(InDatabase); ok {
		return in.KeepIn(tx, key, kept)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.Store.PutResult(key, kept)
}

// againAfter is, for a step whose transaction the database ended for
// another that ran at the same time, the step run again a little later:
// nothing of it was kept. nil for any other failure.
func (r *Runtime) againAfter(err error) *vm.RetryError {
	if errors.Is(err, ErrCrash) {
		return nil
	}
	_, kind, _ := r.database()
	if !kind.Retry(err) {
		return nil
	}
	return &vm.RetryError{Reason: fmt.Sprintf("its transaction was ended by the database: %v", err), After: 50}
}

// endedByTheDatabase runs again a step whose statement the database
// ended for another transaction.
func endedByTheDatabase() *vm.RetryError {
	return &vm.RetryError{Reason: "its transaction was ended by the database for another at the same time", After: 50}
}

// rollback rolls back what a step wrote, if it wrote anything.
func rollback(tx *sql.Tx) {
	if tx != nil {
		tx.Rollback()
	}
}
