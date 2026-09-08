package admission

import (
	"context"
	"errors"

	"github.com/qompack/qompack/internal/core"
)

// ErrPortsUnwired is the stage error for a Pipeline built with a nil port. It is a
// composition-root defect, reported rather than panicked: a panic here reaches a hook, and §2.3
// permits a hook no exit code but 0.
var ErrPortsUnwired = errors.New("admission: pipeline ports are not wired")

// ErrUnrecoverableCapture is the stage error for a capture whose fidelity cannot return the
// original bytes. The stage ran and retained something; what it retained is not what was delivered.
var ErrUnrecoverableCapture = errors.New("admission: capture cannot recover the original")

// ErrSchemaDisagrees is the stage error for a parser whose meaning declares a different schema
// than the delivery did. Dispatch selected that parser for the DECLARED target; a meaning under
// another schema means it saw something else, and the record would then carry two disagreeing
// answers about what the representation describes.
var ErrSchemaDisagrees = errors.New("admission: parsed schema does not match the declared target")

// ErrHandleUnresolvable is the stage error for a handle that did not resolve under current
// authorization. The state that caused it is on the record, and it is the part that matters: this
// error says the transform was blocked, HandleState says what to go fix.
var ErrHandleUnresolvable = errors.New("admission: handle does not resolve under current authorization")

// Fidelity is how recoverable a capture's ORIGINAL, pre-canonicalization bytes are.
//
// The values mirror store.Fidelity, which SP-20 invariant 6 guarantees is never a guess. They are
// restated here rather than imported because internal/admission is foundation-only under
// 00-ARCHITECTURE §3.2; the adapter at the composition root is what maps one to the other.
type Fidelity string

const (
	// FidelityExact means the original was reproduced byte-for-byte.
	FidelityExact Fidelity = "exact"

	// FidelityFull means the original was retained whole because no exact delta could be proven.
	FidelityFull Fidelity = "full"

	// FidelityCanonical means only canonicalized bytes exist. store.Fidelity's own words: they
	// "are NOT the original". A capture at this fidelity retained something, but not what was
	// delivered.
	FidelityCanonical Fidelity = "canonical"
)

// Recoverable reports whether a capture at this fidelity can return the original bytes.
//
// Only exact and full qualify. Every other value — canonical, and the empty string a zero Capture
// carries — is not recoverable, which is the answer that refuses. An unset fidelity is absent
// evidence, and absent evidence is not evidence of recoverability.
func (f Fidelity) Recoverable() bool {
	return f == FidelityExact || f == FidelityFull
}

// Capture is the outcome of durably capturing one delivered result.
//
// It mirrors the recovery-relevant half of store.PutResult — the handle, the fidelity, and the two
// flags that say the retained bytes are not the whole input — and deliberately nothing else. This
// package does not redefine SP-20's identity or publication semantics; it consumes them.
type Capture struct {
	// Handle is the resolvable pointer to the captured object, in store.Root's string form.
	Handle string

	// Fidelity is how recoverable the original is. Only a recoverable capture may be replaced.
	Fidelity Fidelity

	// Truncated reports that the input exceeded the store's put limit and its tail was discarded.
	Truncated bool

	// Redacted is how many spans privacy replaced on the way in.
	Redacted int
}

// Privacy decides whether a delivered result may be persisted at all.
//
// It is consulted before capture, never after: SP-20 invariant 1 puts privacy denial and redaction
// before persistence, and no later denial can unwrite bytes already archived.
type Privacy interface {
	Permits(ctx context.Context, payload []byte) (bool, error)
}

// Capturer durably captures a delivered result before anything replaces it.
//
// The composition root satisfies this with store.PutBytes. Capture must return an error rather than
// a zero Capture when it fails: a caller cannot distinguish "captured nothing" from "captured
// something empty" by inspecting the value.
type Capturer interface {
	Capture(ctx context.Context, payload []byte) (Capture, error)
}

// Publisher verifies that a capture is durably published — the object written, its references
// verified, and its frontier committed — before anything points at it.
//
// Capture and publication are separate stages because they fail separately. A capture that returned
// cleanly and a publication that never committed leave a handle that resolves today and not after
// the next restart, which is the failure M4 follows M1 to avoid.
type Publisher interface {
	VerifyPublished(ctx context.Context, c Capture) error
}

// Parser turns a delivered payload into its structured and displayed meaning.
//
// There is one parser per admitted target, and the shipped set is small on purpose: the allowlist
// is empty until B01 target evidence exists, so the only surface a parser is written for today is
// a Qompack-owned result, whose shape Qompack itself produced. A payload that does not parse under
// its declared schema is a pass-through, never a guess at what it might have meant.
type Parser interface {
	Parse(ctx context.Context, d Delivery) (Meaning, error)
}

// Ports are the collaborators the pipeline reaches SP-20 and SP-13 through.
//
// A nil port is a composition-root mistake this package cannot prevent, so Admit refuses on one
// rather than panicking: a panic here reaches a hook, and §2.3 permits a hook no exit code but 0.
type Ports struct {
	Privacy Privacy
	Capture Capturer
	Publish Publisher
	Parse   Parser
	Resolve Resolver
}

// Delivery is one newly delivered result offered for admission.
type Delivery struct {
	// Target is the schema and version the producer claims. Untrusted.
	Target Target

	// Payload is the delivered bytes.
	Payload []byte

	// Envelope is the set of processing markers the adapter observed on this delivery.
	Envelope Envelope

	// Baseline is a prior capsule offered as a delta's base, or the zero Baseline when none is.
	//
	// It is carried on the delivery rather than fetched through a port because verification means
	// having READ the baseline back, which is work the caller has already done by the time it can
	// name one. An unverified baseline is not an error here; it resets to a capsule.
	Baseline Baseline
}

// Pipeline is the synchronous admission sequence: privacy, capture, durability verification, one
// representation decision, resolvable-handle verification, then one transform.
//
// Commits 2 through 4 implement the whole sequence up to the transform. The transform itself is not
// performed here and no caller may act on OutcomeTransform to replace anything: the feature switch
// is refused and Enable is not wired, and every T21 gate remains required before enablement.
type Pipeline struct {
	gate  Gate
	ports Ports
}

// NewPipeline returns a Pipeline bound to a gate and its ports.
//
// The gate is captured at construction rather than read per call so that one delivered result
// cannot be judged against a configuration that changed underneath it mid-sequence.
func NewPipeline(g Gate, p Ports) *Pipeline { return &Pipeline{gate: g, ports: p} }

// Admit runs the admission sequence for one delivered result and returns its record.
//
// The error return is for a caller's own failures, not for a refused admission: a stage that fails
// is a pass-through, which is a normal outcome and not an error. Admit returns a nil error in every
// case it currently reaches, and the signature keeps room for one so that a future caller-visible
// failure does not have to be smuggled through the record.
//
// The order below is the contract:
//
//  1. Privacy, before anything is written. An undecidable check is not permission.
//  2. The gate, before anything is captured. With admission off there is nothing to replace, so a
//     disabled pipeline must not write a second copy of every delivered result on the way to
//     refusing.
//     2a. The envelope bypass, before anything is captured. A processed envelope's original was
//     already captured on the pass that produced it; capturing the envelope too would archive a
//     transformation of that first object, and a store accumulating one per redelivery is the
//     recursion invariant 3 forbids, showing up as disk usage instead of as nested capsules.
//  3. Capture, then publication verification. Both must succeed, and the capture must be
//     recoverable, before any handle is emitted.
//  4. Parse, then select one representation. Both run only on a durably captured original,
//     because both forms point at it: a representation built first would describe bytes nobody
//     retained and then have nothing to point at.
//  5. Resolve the handle the selected representation will carry, under current authorization and
//     on every delivery. An answer that is anything but resolvable blocks the transform, and the
//     answer itself stays on the record.
func (p *Pipeline) Admit(ctx context.Context, d Delivery) (Record, error) {
	if fail := p.checkPrivacy(ctx, d); fail.Stage != StageNone {
		return p.record(Decide(p.gate, d.Target, fail), d), nil
	}

	if ok, _ := p.gate.Admits(d.Target); !ok {
		return p.record(Decide(p.gate, d.Target, Failure{}), d), nil
	}

	if bypass, why := d.Envelope.Bypass(); bypass {
		rec := Decide(p.gate, d.Target, Failure{})
		rec.Outcome, rec.Reason = OutcomePassThrough, why
		return p.record(rec, d), nil
	}

	captured, fail := p.capture(ctx, d)
	if fail.Stage != StageNone {
		return p.record(Decide(p.gate, d.Target, fail), d), nil
	}

	meaning, fail := p.parse(ctx, d)
	if fail.Stage != StageNone {
		return p.record(Decide(p.gate, d.Target, fail), d), nil
	}

	sel := Select(meaning, captured.Fidelity, d.Baseline)
	if !sel.Form.Emits() {
		return p.record(Decide(p.gate, d.Target, Failure{Stage: StageSelection}), d), nil
	}

	state, fail := p.resolve(ctx, captured.Handle)
	if fail.Stage != StageNone {
		blocked := Decide(p.gate, d.Target, fail)
		blocked.HandleState = state
		return p.record(blocked, d), nil
	}

	rec := Decide(p.gate, d.Target, Failure{})
	rec.HandleState = state
	rec.Handle = captured.Handle
	rec.Fidelity = captured.Fidelity
	rec.Coverage = core.CoverageArchiveOnly
	rec.Meaning = meaning
	rec.Form, rec.Base, rec.Reset = sel.Form, sel.Base, sel.Reset
	rec.Mark = MarkerProducer
	return p.record(rec, d), nil
}

// record attaches the delivery's observed markers to every record on its way out.
//
// It runs on the refusal paths as well as the admitted one, because the observed chain is most
// diagnostic precisely when admission declined: an operator asking why nothing was transformed
// wants to see what else claimed this result.
func (p *Pipeline) record(rec Record, d Delivery) Record {
	rec.Observed = d.Envelope.Markers
	return rec
}

// parse asks the parser port for the delivery's meaning, and requires that meaning to describe the
// target the delivery declared.
//
// An unwired port is StageParse rather than its own stage, for the same reason an unwired capture
// port is StageCapture: from the pipeline's side, a parser that is not there and a parser that
// failed produced the same nothing, and both pass the original through.
func (p *Pipeline) parse(ctx context.Context, d Delivery) (Meaning, Failure) {
	if p.ports.Parse == nil {
		return Meaning{}, Failure{Stage: StageParse, Err: ErrPortsUnwired}
	}
	m, err := p.ports.Parse.Parse(ctx, d)
	if err != nil {
		return Meaning{}, Failure{Stage: StageParse, Err: err}
	}
	if m.Schema != d.Target {
		return Meaning{}, Failure{Stage: StageParse, Err: ErrSchemaDisagrees}
	}
	return m, Failure{}
}

// checkPrivacy asks policy, and distinguishes the two ways that can go wrong.
//
// An explicit refusal is StagePrivacy, which denies: policy decided, and the delivered result is
// withheld. An unwired port or an errored check is StagePolicy, which passes through: nobody
// decided anything, so the original is delivered untouched and nothing is captured.
//
// Collapsing these was this pipeline's first real bug. Denial is the more destructive outcome — it
// withholds output the user would otherwise have seen — so a configuration mistake must not be able
// to cause one. Neither answer permits persistence; that is the property both share, and it is the
// only one they share.
func (p *Pipeline) checkPrivacy(ctx context.Context, d Delivery) Failure {
	if p.ports.Privacy == nil {
		return Failure{Stage: StagePolicy, Err: ErrPortsUnwired}
	}
	permitted, err := p.ports.Privacy.Permits(ctx, d.Payload)
	switch {
	case err != nil:
		return Failure{Stage: StagePolicy, Err: err}
	case !permitted:
		return Failure{Stage: StagePrivacy}
	default:
		return Failure{}
	}
}

// capture runs the capture and publication stages, returning the capture or the stage that failed.
//
// A canonical-only capture is reported as StageCapture rather than as its own stage. That is the
// honest classification: the stage ran, and what it retained is not the original, so for M4's
// purposes the original was not captured. store.FidelityCanonical says so in its own words.
func (p *Pipeline) capture(ctx context.Context, d Delivery) (Capture, Failure) {
	if p.ports.Capture == nil || p.ports.Publish == nil {
		return Capture{}, Failure{Stage: StageCapture, Err: ErrPortsUnwired}
	}

	c, err := p.ports.Capture.Capture(ctx, d.Payload)
	if err != nil {
		return Capture{}, Failure{Stage: StageCapture, Err: err}
	}
	if !c.Fidelity.Recoverable() {
		return Capture{}, Failure{Stage: StageCapture, Err: ErrUnrecoverableCapture}
	}

	if err := p.ports.Publish.VerifyPublished(ctx, c); err != nil {
		return Capture{}, Failure{Stage: StagePublication, Err: err}
	}
	return c, Failure{}
}
