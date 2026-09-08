package admission

import "github.com/qompack/qompack/internal/core"

// Pipeline is the synchronous admission sequence: capture, durability verification, one
// representation decision, resolvable-handle verification, then one transform.
//
// This slice declares the seam and implements none of it. SP-21's schedule makes main freeze the
// pipeline's input and output before roles A through D author against them, and Admit below is that
// signature. plans/OWNERS.tsv names Admit as this package's stub probe for the same reason: the
// policy in Decide is real and fully graded, the pipeline around it is not built.
//
// Commit 2 wires SP-20 capture and publication behind Admit, commit 4 the SP-13 handle resolution.
// Both reach those packages through ports declared here and satisfied at a composition root —
// internal/admission is foundation-only under 00-ARCHITECTURE §3.2, and that is a commitment rather
// than a stage.
type Pipeline struct {
	gate Gate
}

// NewPipeline returns a Pipeline bound to a gate.
//
// The gate is captured at construction rather than read per call so that one delivered result
// cannot be judged against a configuration that changed underneath it mid-sequence.
func NewPipeline(g Gate) *Pipeline { return &Pipeline{gate: g} }

// Admit runs the admission sequence for one delivered result and returns its record.
//
// It returns core.ErrNotImplemented until commit 2. The zero Record travels with that error
// deliberately: a stub that answered with a populated Record alongside its error would let a caller
// that ignored the error act on a decision no capture ever backed, and the whole point of M4
// following M1 to M3 is that nothing is replaced before it is durably recoverable.
//
// When it is built, the sequence is the one M4-02 fixes and Decide already grades: privacy first,
// then the gate, then the first failing stage, then at most one transform. Admit will not re-derive
// that policy — it will collect stage outcomes and hand them to Decide.
func (p *Pipeline) Admit(t Target) (Record, error) {
	return Record{}, core.ErrNotImplemented
}
