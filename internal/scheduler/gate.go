package scheduler

import "sync/atomic"

// pSelectionAvailable is the ship-order guard of Qompack.md's closing note 3 and
// 00-ARCHITECTURE §5.12: analyzer.NewSelector refuses to construct while it is false, so
// submodular selection over a scattered keep-set is structurally inert until a real
// scheduler Runtime exists. This is the only package-level mutable state in scheduler.
//
// It is an atomic.Bool rather than the plain bool SP-01 shipped because SP-12 adds two writers
// (EnablePSelection on Runtime construction, DisablePSelection on shutdown and in every test's
// defer) while analyzer.NewSelector reads it from arbitrary goroutines.
var pSelectionAvailable atomic.Bool

// PSelectionAvailable reports whether p-selection is live for this process.
func PSelectionAvailable() bool { return pSelectionAvailable.Load() }

// EnablePSelection is called by daemon.NewSchedulerRuntime after the runtime has verified
// it can assemble candidates (non-nil store, graph and segment log).
func EnablePSelection() { pSelectionAvailable.Store(true) }

// DisablePSelection is called on runtime shutdown and by tests. Tests MUST defer it.
func DisablePSelection() { pSelectionAvailable.Store(false) }
