package admission

import (
	"context"
	"errors"
)

// ErrPortsUnwired is the stage error for a Pipeline built with a nil port. It is a
// composition-root defect, reported rather than panicked: a panic here reaches a hook, and §2.3
// permits a hook no exit code but 0.
var ErrPortsUnwired = errors.New("admission: pipeline ports are not wired")

// ErrUnrecoverableCapture is the stage error for a capture whose fidelity cannot return the
// original bytes. The stage ran and retained something; what it retained is not what was delivered.
var ErrUnrecoverableCapture = errors.New("admission: capture cannot recover the original")

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

// Ports are the collaborators the pipeline reaches SP-20 and SP-13 through.
//
// A nil port is a composition-root mistake this package cannot prevent, so Admit refuses on one
// rather than panicking: a panic here reaches a hook, and §2.3 permits a hook no exit code but 0.
type Ports struct {
	Privacy Privacy
	Capture Capturer
	Publish Publisher
}

// Delivery is one newly delivered result offered for admission.
type Delivery struct {
	// Target is the schema and version the producer claims. Untrusted.
	Target Target

	// Payload is the delivered bytes.
	Payload []byte
}

// Pipeline is the synchronous admission sequence: privacy, capture, durability verification, one
// representation decision, resolvable-handle verification, then one transform.
//
// Commit 2 implements the first three. Representation selection and handle resolution are commits 3
// and 4; until they land, an admitted delivery reaches OutcomeTransform on the strength of its
// capture alone, and no caller may act on that outcome to replace anything — the feature switch is
// refused and Enable is not wired.
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
//  3. Capture, then publication verification. Both must succeed, and the capture must be
//     recoverable, before any handle is emitted.
func (p *Pipeline) Admit(ctx context.Context, d Delivery) (Record, error) {
	if fail := p.checkPrivacy(ctx, d); fail.Stage != StageNone {
		return Decide(p.gate, d.Target, fail), nil
	}

	if ok, _ := p.gate.Admits(d.Target); !ok {
		return Decide(p.gate, d.Target, Failure{}), nil
	}

	captured, fail := p.capture(ctx, d)
	if fail.Stage != StageNone {
		return Decide(p.gate, d.Target, fail), nil
	}

	rec := Decide(p.gate, d.Target, Failure{})
	rec.Handle = captured.Handle
	rec.Fidelity = captured.Fidelity
	return rec, nil
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
