package admission

import "reflect"

// Span is a half-open byte range [Start, End) in a delivered result's source.
//
// The zero Span is the UNKNOWN span, not a span at offset 0. Qompack.md asks for "source
// span/offsets where available", and the two are different answers: an absent anchor tells a reader
// to go look, while an anchor at the head of the file tells them the declaration is there. A false
// anchor is worse than a missing one.
type Span struct {
	Start int64
	End   int64
}

// Known reports whether this span anchors anything. An empty or inverted range does not.
func (s Span) Known() bool { return s.End > s.Start }

// Source is a delivered result's identity and, where available, its span.
type Source struct {
	// ID is the source identity — a tool-result node id, a file key, whatever the producer names
	// its output by. Admission carries it; it does not interpret it.
	ID string

	// Span is the byte range within that source, or the unknown span.
	Span Span
}

// Meaning is one delivered result's structured and displayed meaning, as a PARSER produced it.
//
// Invariant 7's field list lives here, with two deliberate absences: fidelity and coverage. Neither
// is the parser's to claim. Fidelity is the capture's answer about whether the original bytes can
// come back, and coverage is a lifecycle observation; a parser that could set either could declare
// a canonical-only capture "exact" and the admission record would carry that forward as fact. Both
// live on Record, filled from the capture.
//
// Every field here must survive into the emitted representation. Preserves is what checks that, and
// it walks this struct reflectively so a field added below is covered the moment it lands.
type Meaning struct {
	// Status is the result's own disposition — an exit status, an error class, whatever the
	// schema calls it.
	Status string

	// Stderr is the diagnostic stream, kept separate from Displayed because a summary that folds
	// stderr into prose loses the reader's ability to tell output from complaint.
	Stderr string

	// Diagnostics are the structured findings the result reported.
	Diagnostics []string

	// Interrupted reports that the underlying work did not run to completion. It survives because
	// a truncated result that looks complete is a wrong answer, not a short one.
	Interrupted bool

	// Media reports a binary or multimodal payload. Such a payload selects no representation at
	// all: there is no evidence that a capsule preserves what an image or an archive means.
	Media bool

	// InputCount and OutputCount are the counts the result declared — matches searched, files
	// written, whatever it counted. They survive so a capsule cannot quietly change a total.
	InputCount  int
	OutputCount int

	// Signature is the failure signature. A change to it resets delta eligibility (invariant 9),
	// because two failures that differ are not a difference a delta may describe.
	Signature string

	// Source is the identity and span the result came from.
	Source Source

	// Schema is the parser schema and version this meaning was produced under. A change resets
	// delta eligibility: a delta against a baseline parsed by a different version compares two
	// things that were never comparable.
	Schema Target

	// Structured is the machine-readable form, retained verbatim.
	Structured []byte

	// Displayed is the human-readable form, retained verbatim. Both halves survive together;
	// invariant 7 is not satisfied by either alone.
	Displayed string
}

// Preserves reports which fields of want are absent or altered in got, by name.
//
// An empty result means every field survived. This is invariant 7 in checkable form, and the reason
// it is reflective rather than a written-out comparison: an enumeration has to be maintained, and
// the failure of a stale enumeration is silent. A dropped Interrupted flag looks exactly like a
// result that was never interrupted.
func Preserves(want, got Meaning) []string {
	wv, gv := reflect.ValueOf(want), reflect.ValueOf(got)
	typ := wv.Type()

	var lost []string
	for i := range typ.NumField() {
		if !reflect.DeepEqual(wv.Field(i).Interface(), gv.Field(i).Interface()) {
			lost = append(lost, typ.Field(i).Name)
		}
	}
	return lost
}

// Form is the shape of the single representation admission may emit.
//
// FormNone is the zero value for the same reason OutcomePassThrough is: a Selection nobody
// populated must authorize nothing. If FormCapsule were zero, every unfilled Selection would
// authorize a replacement.
type Form int

const (
	// FormNone emits no representation, which the pipeline turns into a pass-through.
	FormNone Form = iota

	// FormCapsule is the self-contained representation. It carries the whole meaning and names no
	// baseline, so it is readable alone. Invariant 8 makes it precede every delta.
	FormCapsule

	// FormDelta is relative to a verified compatible baseline.
	FormDelta
)

// Emits reports whether this form produces a representation at all.
func (f Form) Emits() bool { return f == FormCapsule || f == FormDelta }

// String renders a Form for admission records and failure messages.
func (f Form) String() string {
	switch f {
	case FormNone:
		return "none"
	case FormCapsule:
		return "capsule"
	case FormDelta:
		return "delta"
	default:
		return "unknown"
	}
}

// ResetCause is why a delta was refused and a capsule chosen instead.
//
// Every cause below produces the same form, so the form alone proves nothing about which rule
// fired. Recording the cause is what makes the baseline decision auditable: an operator who sees
// every delta resetting on ResetSchemaChanged learns something a bare "capsule" never tells them.
type ResetCause int

const (
	// ResetNone means no reset happened — either a delta was selected, or no representation was.
	ResetNone ResetCause = iota

	// ResetNoBaseline means no baseline was offered at all.
	ResetNoBaseline

	// ResetUnverified means a baseline was offered but never read back. A prior delivery record
	// is this case, and it is the one Qompack.md calls out by name.
	ResetUnverified

	// ResetSignatureChanged means the failure signature differs from the baseline's.
	ResetSignatureChanged

	// ResetSchemaChanged means the parser schema or version differs from the baseline's.
	ResetSchemaChanged

	// ResetUncertainCapture means the baseline's own capture cannot return its original bytes, so
	// there is nothing dependable to be relative to.
	ResetUncertainCapture
)

// String renders a ResetCause for the baseline-verification record.
func (r ResetCause) String() string {
	switch r {
	case ResetNone:
		return "none"
	case ResetNoBaseline:
		return "no-baseline"
	case ResetUnverified:
		return "unverified"
	case ResetSignatureChanged:
		return "signature-changed"
	case ResetSchemaChanged:
		return "schema-changed"
	case ResetUncertainCapture:
		return "uncertain-capture"
	default:
		return "unknown"
	}
}

// Baseline is a prior capsule offered as a delta's base.
//
// Verification is unexported and settable only through VerifyBaseline, which takes the facts a
// caller can only hold by having READ the baseline back. That is deliberate: the shortcut this
// guards against is taking the previous delivery's identity off a session record and treating its
// existence as proof. Qompack.md states the rule directly — "a same-epoch prior delivery is not
// proof that a relative delta has a valid baseline" — and a composite literal, which is the only
// shape that shortcut can produce, is unverified by construction.
//
// This is an in-process API, so a determined caller can still call VerifyBaseline with invented
// arguments. The guarantee is not that lying is impossible; it is that the accidental version of
// this mistake cannot compile into a verified baseline.
type Baseline struct {
	// ID names the prior capsule. Empty means no baseline exists.
	ID string

	// Signature is the prior delivery's failure signature.
	Signature string

	// Schema is the prior delivery's parser schema and version.
	Schema Target

	// Fidelity is the prior capture's recoverability.
	Fidelity Fidelity

	// verified records that the baseline was resolved and read, not merely referenced.
	verified bool
}

// Verified reports whether this baseline was read back rather than assumed.
func (b Baseline) Verified() bool { return b.verified }

// VerifyBaseline returns a verified baseline from facts read back off the prior capsule.
//
// A caller reaches these arguments by resolving the baseline and reading it. An empty id is not a
// baseline, so it stays unverified regardless.
func VerifyBaseline(id, signature string, schema Target, f Fidelity) Baseline {
	return Baseline{ID: id, Signature: signature, Schema: schema, Fidelity: f, verified: id != ""}
}

// Selection is the single representation decision for one delivered result.
type Selection struct {
	// Form is the shape to emit. FormNone emits nothing.
	Form Form

	// Reason is why nothing was selected, and is meaningful only when Form is FormNone.
	Reason Reason

	// Reset is why a delta was refused in favour of a capsule. ResetNone when a delta was chosen
	// or when nothing was selected.
	Reset ResetCause

	// Base names the baseline a delta is against. It is empty for a capsule — that emptiness is
	// what "self-contained" means, and a capsule that carried a base would not be readable alone.
	Base string
}

// Select makes the single representation decision: nothing, a self-contained capsule, or a delta.
//
// The order is the contract:
//
//  1. A binary or multimodal payload selects nothing. There is no representation of an image this
//     package can justify.
//  2. A capture that cannot return the original selects nothing. Both forms point at the captured
//     original, and pointing away from a delivered result toward something that is not it is the
//     data loss M4 exists to prevent. The pipeline refuses this earlier as well; Select is
//     exported, so it locks the door on its own side too.
//  3. Everything a baseline could be doubtful about resets to a capsule — never to a refusal.
//     A capsule is always available, which is precisely why it is the form doubt falls back to,
//     and why invariant 8 makes it precede every delta.
func Select(m Meaning, captured Fidelity, base Baseline) Selection {
	if m.Media {
		return Selection{Form: FormNone, Reason: ReasonNoRepresentation}
	}
	if !captured.Recoverable() {
		return Selection{Form: FormNone, Reason: ReasonNoRepresentation}
	}

	if reset := baselineReset(m, base); reset != ResetNone {
		return Selection{Form: FormCapsule, Reset: reset}
	}
	return Selection{Form: FormDelta, Base: base.ID}
}

// baselineReset reports why base cannot support a delta for m, or ResetNone when it can.
//
// The checks run outermost-first: whether a baseline exists at all, then whether it was read,
// then whether it describes the same thing, then whether its own bytes are dependable. Reporting
// the outermost failure is what keeps the record deterministic when more than one is true.
func baselineReset(m Meaning, base Baseline) ResetCause {
	switch {
	case base.ID == "":
		return ResetNoBaseline
	case !base.Verified():
		return ResetUnverified
	case base.Schema != m.Schema:
		return ResetSchemaChanged
	case base.Signature != m.Signature:
		return ResetSignatureChanged
	case !base.Fidelity.Recoverable():
		return ResetUncertainCapture
	default:
		return ResetNone
	}
}
