// Package daemon is the resident per-project process of 00-ARCHITECTURE.md §5.4: the thing that
// holds the store, the sketches and the scheduler in memory so a hook does not have to.
//
// The whole reason it exists is budget B-A (§2.4): a hook has 15 ms at p99, and opening a store,
// loading sketches and reconstructing scheduler state cannot be done in that time on every tool
// call. The daemon pays those costs once and the hook pays only a connect-write-ack.
//
// daemon is a composition root (§3.2): it may import anything, and nothing may import it. It is
// also one of the four packages permitted to use os/exec, because §5.4 has it re-spawn itself
// detached; SP-05 adds that call.
//
// SP-01 ships the type set below as real declarations with stub bodies returning
// core.ErrNotImplemented, and — importantly — the three extension seams already shaped:
//
//   - Handle registers an Op handler, so the op-routing table is DATA rather than a switch
//     statement every later wave would have to edit.
//   - IdleController.Register adds O3/O5 background work.
//   - Options carries the late-bound dependency set, where nil means "not built yet".
//
// Those seams are the point of shipping this package in wave 0. Waves 1–5 wire themselves in
// without editing daemon internals, which is what stops four wave-3 subplans from colliding
// inside one file. SP-05 owns the real implementation; there is no <pkg>test conformance suite,
// because a composition root exposes no seam for another wave to implement against.
package daemon
