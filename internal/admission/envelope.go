package admission

import "strings"

// MarkerProducer is the value admission stamps into an envelope it produced, and the value it
// recognizes as its own on the way back in.
//
// It is admission's own constant rather than a reuse of checkpoint.SentinelPhrase. The two mark
// different things with different lifetimes — one survives a summarizer restating a prohibition,
// this one identifies a result this pipeline already replaced — and §3.2 forbids the import in any
// case: internal/admission is foundation-only.
const MarkerProducer = "qompack.admission.v1"

// Envelope is the set of processing markers an adapter OBSERVED on one delivered result.
//
// The adapter must read these from a structural position — a named field of a known schema — and
// never by scanning the payload for a substring. A result that quotes a marker is not a result that
// carries one: a transcript, a bug report or this source file would all match a substring scan, and
// the failure mode is admission silently declining to run on exactly the content that mentions it.
//
// The zero Envelope is a fresh result, which is the answer that lets admission proceed. That is the
// opposite polarity from the rest of this package's defaults, and it is deliberate: an adapter that
// cannot read markers must not accidentally disable admission for everything, which is a failure
// with no error anywhere to explain it. Guarding the other direction — a chained transform — is
// what the marker itself is for, and it only works if a real marker is what sets it.
type Envelope struct {
	// Markers are the producers that claim to have already processed this result, in the order
	// the adapter read them. That order is an observation of the payload, never a claim about the
	// order hooks ran in, which nothing here observes.
	Markers []string
}

// Processed reports whether any producer has already transformed this result.
//
// Blank and whitespace-only entries do not count. An adapter reading a field that is absent from
// the payload gets the empty string, and treating that as a marker would bypass admission for every
// unmarked result.
func (e Envelope) Processed() bool {
	for _, m := range e.Markers {
		if strings.TrimSpace(m) != "" {
			return true
		}
	}
	return false
}

// Ours reports whether admission itself produced one of the observed markers.
func (e Envelope) Ours() bool {
	for _, m := range e.Markers {
		if strings.TrimSpace(m) == MarkerProducer {
			return true
		}
	}
	return false
}

// Bypass reports whether this delivery must skip admission entirely, and why.
//
// Our own marker outranks a foreign one when both are present, so the record stays deterministic
// under a marker order nothing observed. Ours is the more specific fact: whatever else touched this
// result, we are looking at output we produced, which is what makes re-admitting it a chained
// transform of our own work.
func (e Envelope) Bypass() (bool, Reason) {
	switch {
	case e.Ours():
		return true, ReasonAlreadyProcessed
	case e.Processed():
		return true, ReasonForeignTransform
	default:
		return false, ReasonAdmitted
	}
}
