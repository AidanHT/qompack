package negknow

import (
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// This file is SP-16 §1's lifetime half: what has happened to a piece of evidence since it was
// recorded, and which of those things must stop it being reused.
//
// §8.3's existing staleness machinery answers one specific version of that question — did a FILE
// this elimination depended on change? — and it stays the authority on it. Four more things can
// end an elimination's usefulness without touching any file it named:
//
//   - it EXPIRED, because whoever recorded it declared a shelf life and that time has passed;
//   - it was DELETED, by retention or by a user;
//   - it was SUPERSEDED, by a later record about the same thing;
//   - it was CORRECTED, because a user said it was wrong.
//
// None of the four is derivable from depends_on hashes, and all four must invalidate reuse. This
// file is the closed vocabulary for them and the one function that decides.
//
// The zero Lifecycle means "nothing has happened to this evidence", which is the correct reading
// of a record that carries no lifecycle sidecar at all — every record written before SP-16, for
// instance. That is safe here, and only here, because a lifecycle event is a POSITIVE claim about
// something that happened: the failure mode a zero value must not create is "reuse permitted
// because nobody said no", and permission is Authorize's decision, not this one. What the zero
// value does produce is an UNDECLARED expiry, which Applies treats as a gap rather than as an
// unlimited licence.

// LifecycleEvent is the closed set of things that can happen to one piece of evidence after it is
// recorded, other than a dependency changing under it.
//
// A value this build does not recognize is not a success state: Valid refuses it and Disposition
// maps it to DispositionUncertain, so a record written by a later plugin version cannot be reused
// on the strength of an event this one cannot interpret.
type LifecycleEvent string

const (
	// LifecycleNone means no event was recorded. It is the zero value and the ordinary state of
	// a live record.
	LifecycleNone LifecycleEvent = ""
	// LifecycleDeleted means the evidence was removed, by retention or by a user.
	LifecycleDeleted LifecycleEvent = "deleted"
	// LifecycleSuperseded means a later record about the same thing replaced it.
	LifecycleSuperseded LifecycleEvent = "superseded"
	// LifecycleCorrected means a user said the evidence was wrong. It outranks every other
	// event: see Lifecycle.Disposition.
	LifecycleCorrected LifecycleEvent = "corrected"
)

// Valid reports whether e is one of the closed lifecycle events.
func (e LifecycleEvent) Valid() bool {
	switch e {
	case LifecycleNone, LifecycleDeleted, LifecycleSuperseded, LifecycleCorrected:
		return true
	default:
		return false
	}
}

// Lifecycle is the additive, non-frozen sidecar recording what has happened to one piece of
// evidence. It is NOT part of Record: Record's json tags are frozen by two byte-for-byte contract
// fixtures (see record.go), and adding a field there would fork the wire format every existing
// reader parses.
type Lifecycle struct {
	// ExpiresAt is when the recorder declared this evidence stops applying. Zero means NO SHELF
	// LIFE WAS DECLARED, which is not the same as "never expires": Applies reports it as an
	// omission and withholds cross-scope reuse over it.
	ExpiresAt core.UnixMilli `json:"expires_at,omitempty"`
	// Event is what happened to the evidence, if anything.
	Event LifecycleEvent `json:"event,omitempty"`
	// By names what caused Event: a superseding record id, or the actor behind a correction or a
	// deletion. It is free text for a transcript to print and is never parsed back into an
	// authority.
	By string `json:"by,omitempty"`
	// At is when Event was recorded; zero when Event is LifecycleNone.
	At core.UnixMilli `json:"at,omitempty"`
	// Authority is the authority behind Event, using SP-20's closed vocabulary. An event whose
	// authority is missing or unrecognized still invalidates — invalidation is the safe
	// direction — but Applies says so in its omissions.
	Authority core.Authority `json:"authority,omitempty"`
}

// Disposition is what a Lifecycle establishes about whether evidence still stands.
//
// DispositionUncertain is the zero value, and Disposition() never returns it for a lifecycle it
// fully understood: it is reserved for an event this build cannot interpret, so an unrecognized
// value can never be read as DispositionLive.
type Disposition uint8

const (
	// DispositionUncertain means the lifecycle could not be interpreted — an event value this
	// build does not know. It is neither live nor invalid, and no reuse may proceed on it.
	DispositionUncertain Disposition = iota
	// DispositionLive means nothing has ended this evidence: no event, and either no declared
	// expiry or one still in the future.
	DispositionLive
	// DispositionExpired means a declared shelf life has passed.
	DispositionExpired
	// DispositionDeleted means the evidence was removed.
	DispositionDeleted
	// DispositionSuperseded means a later record replaced it.
	DispositionSuperseded
	// DispositionCorrected means a user said it was wrong.
	DispositionCorrected
)

// String returns the human-facing spelling of d, or "uncertain" for a value no version of this
// package has minted.
func (d Disposition) String() string {
	switch d {
	case DispositionUncertain:
		return "uncertain"
	case DispositionLive:
		return "live"
	case DispositionExpired:
		return "expired"
	case DispositionDeleted:
		return "deleted"
	case DispositionSuperseded:
		return "superseded"
	case DispositionCorrected:
		return "corrected"
	}
	return "uncertain"
}

// Live reports whether d permits reuse to be considered at all. Only DispositionLive does;
// DispositionUncertain does not, which is what keeps an uninterpretable event from reading as
// permission.
func (d Disposition) Live() bool { return d == DispositionLive }

// Disposition reports what l establishes about the evidence as of now.
//
// The precedence is fixed and is not a matter of which event happened last:
//
//  1. An unrecognized event yields DispositionUncertain. This build cannot say what happened, so
//     it does not get to say the evidence still stands.
//  2. A correction outranks everything else, including an expiry that has since passed. "A user
//     said this was wrong" is the strongest statement in the system (core.AuthorityUserCorrection),
//     and a transcript that reported such a record as merely expired would understate why it must
//     not come back.
//  3. Deletion, then supersession.
//  4. A declared expiry at or before now yields DispositionExpired. The comparison is
//     inclusive — an expiry timestamp is the first instant the evidence no longer applies — so a
//     shelf life of zero duration expires immediately rather than lasting one millisecond.
//  5. Otherwise DispositionLive.
func (l Lifecycle) Disposition(now core.UnixMilli) Disposition {
	if !l.Event.Valid() {
		return DispositionUncertain
	}
	switch l.Event {
	case LifecycleCorrected:
		return DispositionCorrected
	case LifecycleDeleted:
		return DispositionDeleted
	case LifecycleSuperseded:
		return DispositionSuperseded
	}
	if l.ExpiresAt != 0 && now >= l.ExpiresAt {
		return DispositionExpired
	}
	return DispositionLive
}

// ExpiryDeclared reports whether l names a shelf life at all.
//
// It is separate from Disposition because the two answer different questions and a caller needs
// both: Disposition says whether the evidence has ended, and this says whether anyone ever stated
// when it would. Evidence with no declared expiry is live indefinitely, which is fine inside the
// session that recorded it and is exactly the gap §1 refuses to carry across scopes.
func (l Lifecycle) ExpiryDeclared() bool { return l.ExpiresAt != 0 }

// omission renders d as the core.Omission an Applies transcript carries for it, or the zero
// Omission for DispositionLive.
//
// The recovery half never says "reuse it anyway": every direction here is either re-verification
// or an explicit re-record, because the whole point of a disposition that is not live is that the
// evidence cannot be restored by asserting it harder.
func (d Disposition) omission(l Lifecycle) core.Omission {
	switch d {
	case DispositionLive:
		return core.Omission{}
	case DispositionExpired:
		return core.Omission{
			Reason:   fmt.Sprintf("evidence expired at %d", int64(l.ExpiresAt)),
			Recovery: "re-verify the approach and record a fresh elimination",
		}
	case DispositionDeleted:
		return core.Omission{
			Reason:   describeEvent("evidence was deleted", l),
			Recovery: "re-verify the approach; the deleted record is not recoverable through reuse",
		}
	case DispositionSuperseded:
		return core.Omission{
			Reason:   describeEvent("evidence was superseded", l),
			Recovery: "consult the superseding record instead",
		}
	case DispositionCorrected:
		return core.Omission{
			Reason:   describeEvent("evidence was corrected", l),
			Recovery: "the correction is authoritative; do not reuse the corrected claim",
		}
	}
	return core.Omission{
		Reason:   fmt.Sprintf("unrecognized lifecycle event %q", string(l.Event)),
		Recovery: "upgrade the reader that wrote this lifecycle, or re-verify the approach",
	}
}

// describeEvent appends l's cause to base when one was recorded, so a transcript names the
// superseding record or the correcting actor instead of only the category.
func describeEvent(base string, l Lifecycle) string {
	if l.By == "" {
		return base
	}
	return base + " by " + l.By
}
