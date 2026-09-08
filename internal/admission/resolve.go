package admission

import "context"

// HandleState is what resolving an emitted handle under current authorization found.
//
// The three refusing answers are kept apart deliberately. They block the transform identically, so
// the outcome cannot distinguish them — and they are three different operational facts with three
// different fixes: a permission to grant, an object to restore, a resolver to reach. Invariant 6
// requires that they "stay visible as such", and this enum is where that visibility lives.
//
// HandleUnknown is the zero value, and it refuses. A resolver that returns nothing has resolved
// nothing, which is not permission.
type HandleState int

const (
	// HandleUnknown means nothing resolved anything. The zero value, and it refuses.
	HandleUnknown HandleState = iota

	// HandleResolvable means the handle resolves and this reader is authorized. The only
	// admitting answer.
	HandleResolvable

	// HandleDenied means authorization refused this reader. It is a real decision about the
	// ARCHIVED copy, and it is not a privacy denial about the delivery.
	HandleDenied

	// HandleUnavailable means the object is not there — collected, not yet published, or a store
	// that has it no longer.
	HandleUnavailable

	// HandleUncertain means the resolver could not determine the answer. A failed resolver lands
	// here rather than in HandleDenied: a transport failure is not an authorization decision, and
	// recording it as one would send an operator to fix permissions on a store that was merely
	// unreachable.
	HandleUncertain
)

// Resolvable reports whether this state permits the transform. Only HandleResolvable does.
func (h HandleState) Resolvable() bool { return h == HandleResolvable }

// String renders a HandleState for the resolution audit.
//
// An unmapped state renders "unrecognized" rather than "unknown", because "unknown" is a real
// state here: a forgotten arm would otherwise masquerade as HandleUnknown and read as a resolver
// that answered nothing rather than as a gap in this switch.
func (h HandleState) String() string {
	switch h {
	case HandleUnknown:
		return "unknown"
	case HandleResolvable:
		return "resolvable"
	case HandleDenied:
		return "denied"
	case HandleUnavailable:
		return "unavailable"
	case HandleUncertain:
		return "uncertain"
	default:
		return "unrecognized"
	}
}

// Resolver resolves an emitted handle under CURRENT authorization.
//
// The composition root satisfies this against SP-13 retrieval and M2 authorization. "Current" is
// load-bearing: the question is asked per delivery, because a scope can expire and a policy can
// tighten between two deliveries of the same handle.
//
// An error means the resolver could not answer, which is HandleUncertain — never HandleDenied.
type Resolver interface {
	Resolve(ctx context.Context, handle string) (HandleState, error)
}

// resolve asks the resolver port whether the handle about to be emitted can be read back.
//
// It returns the state alongside the failure so the record can carry the state on both paths: a
// refusal that reported only "unresolvable" would lose the part an operator needs.
func (p *Pipeline) resolve(ctx context.Context, handle string) (HandleState, Failure) {
	if p.ports.Resolve == nil {
		return HandleUnknown, Failure{Stage: StageResolution, Err: ErrPortsUnwired}
	}

	state, err := p.ports.Resolve.Resolve(ctx, handle)
	if err != nil {
		return HandleUncertain, Failure{Stage: StageResolution, Err: err}
	}
	if !state.Resolvable() {
		return state, Failure{Stage: StageResolution, Err: ErrHandleUnresolvable}
	}
	return state, Failure{}
}
