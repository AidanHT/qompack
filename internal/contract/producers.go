package contract

import "sync"

// producersMu guards producers. It is a package-level map, not a monitor field, because the set of
// producers a build carries is a property of the BINARY (which subplans' code linked in and called
// DeclareProducer from their init or composition root), not of any one Monitor instance — exactly
// mirroring how HasProducer is read from inside gated without an Env or Monitor in scope.
var producersMu sync.Mutex

// producers is the set of assertion IDs whose producer is present in this build. A nil map behaves
// exactly like an empty one for every read in this file, so ResetProducers can restore it to nil
// rather than allocating.
var producers map[ID]bool

// DeclareProducer marks id's producer as present in this build (00-ARCHITECTURE.md §12.1). It is
// idempotent: declaring the same id twice is a no-op, not an error, so a composition root can call
// it unconditionally during startup without tracking what it already declared.
func DeclareProducer(id ID) {
	producersMu.Lock()
	defer producersMu.Unlock()
	if producers == nil {
		producers = make(map[ID]bool)
	}
	producers[id] = true
}

// HasProducer reports whether id's producer has been declared present in this build. gated calls
// this, and only this, to decide whether an assertion's real Check runs at all.
func HasProducer(id ID) bool {
	producersMu.Lock()
	defer producersMu.Unlock()
	return producers[id]
}

// ResetProducers clears every declared producer. Tests only: the set is process-wide, so any test
// that calls DeclareProducer must undo it with t.Cleanup(contract.ResetProducers), or it leaks into
// every other test sharing the binary — including the frozen golden fixture in golden_test.go, which
// asserts every one of the nine standard assertions is not-yet-implemented on an undeclared build.
func ResetProducers() {
	producersMu.Lock()
	defer producersMu.Unlock()
	producers = nil
}
