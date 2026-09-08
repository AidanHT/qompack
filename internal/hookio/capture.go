package hookio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

// CapturePolicy decides which bytes may cross the persistence boundary. Only OutcomeOK admits
// its returned bytes. The policy must cover the entire host payload, including unknown fields;
// a missing policy never implies permission. Policy implementations must not persist their input.
type CapturePolicy func([]byte) (core.CaptureDecision, error)

// CaptureFragment supplies the optional degraded-path behaviour CaptureHook applies when a host
// payload cannot be admitted whole. Without one, CaptureHook keeps its original contract exactly:
// a degraded payload yields an error and no bytes at all. With one, the same payload is still
// refused for observation, but what arrived is classified and a bounded prefix is retained as
// evidence instead of being discarded unrecorded.
type CaptureFragment struct {
	// Policy permits bytes from a payload that could not be admitted whole. It sees only the
	// bounded prefix, never the payload behind it, because a prefix is a different disclosure from
	// the document it came from: a policy that decides by structure has not examined the prefix as
	// a standalone value. Retaining less than the whole payload is therefore an explicit caller
	// decision, not a fallback CaptureHook may take on its own.
	Policy CapturePolicy
	// Incomplete records that the host read ended before the payload did. Partiality is an
	// observation of the transport; truncation is a bound this process chose. The two are recorded
	// distinctly and must never collapse into one another.
	Incomplete bool
}

// Capture is the versioned, permitted source for a future observation sidecar. Bytes use JSON's
// base64 encoding for []byte, so transport encoding cannot compact or re-escape the captured JSON.
// It carries no arrival identity or durability claim; those belong to the durable delivery lease.
// Exact fidelity describes the captured host delivery, never complete process or file output.
// Redacted and Truncated retain independent facts even when a failure determines Fidelity.
type Capture struct {
	Version       int                  `json:"v"`
	Bytes         []byte               `json:"bytes,omitempty"`
	SourceFormat  string               `json:"source_format"`
	PolicyVersion string               `json:"policy"`
	HashVersion   string               `json:"hash"`
	Fidelity      core.Fidelity        `json:"fidelity"`
	Outcome       core.EvidenceOutcome `json:"outcome"`
	Redacted      bool                 `json:"redacted"`
	Truncated     bool                 `json:"truncated"`
	// CaptureError names why this capture is degraded, from core's closed label set. It is never
	// built from payload bytes, so it can be logged and compared without disclosing content.
	CaptureError core.CaptureError `json:"capture_error,omitempty"`
	// SourceBytes is the size of the host delivery as it reached this process. It is retained even
	// when no bytes are, so an unrecoverable payload still leaves a measurable trace.
	SourceBytes int `json:"source_bytes,omitempty"`
	// HostFields lists the top-level keys the permitted payload actually carried, sorted. Event's
	// fields are value types with usable zeros, so a derived Event cannot distinguish an absent
	// host field from one the host sent empty. This list is the record that keeps an absent field
	// absent instead of promoting its zero value into a synthetic default.
	HostFields []string `json:"host_fields,omitempty"`
}

// Recorded reports whether c is a real admission record rather than the zero Capture a caller
// returns when admission never ran at all — no configuration, no compiled policy, and therefore no
// classification of anything. Every Capture CaptureHook builds carries core.EvidenceVersion, so a
// zero Version is exactly "nothing was examined". Only a recorded Capture may be published: a zero
// one would assert a delivery this process never classified, which is the mirror image of the
// silent drop this predicate exists to prevent.
func (c Capture) Recorded() bool { return c.Version != 0 }

// CaptureHook applies the caller's explicit privacy policy before parsing a retained Event.
// It performs no I/O. Runtime capture remains disabled until ingress, publication and compatible
// readers consume this seam and their gates pass; callers must never persist raw on failure.
//
// An optional CaptureFragment makes the degraded paths evidential rather than silent: an oversize
// payload reports FidelityTruncated, a short host read reports FidelityPartial, and a payload that
// is not an admissible JSON object reports FidelityBinary, each with its own capture error, and
// each retaining a bounded prefix when — and only when — the fragment policy permits one. None of
// these produce an Event or an OutcomeOK capture: classification records what arrived, it never
// promotes a degraded delivery into an observation.
func CaptureHook(raw []byte, limit int, policyVersion string, policy CapturePolicy, fragment ...CaptureFragment) (Capture, Event, error) {
	var frag CaptureFragment
	if len(fragment) != 0 {
		frag = fragment[len(fragment)-1]
	}
	c := Capture{
		Version: core.EvidenceVersion, SourceFormat: "application/json",
		PolicyVersion: policyVersion, HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityFailure, Outcome: core.OutcomeUnavailable,
		SourceBytes: len(raw),
	}
	if limit < 0 || len(raw) > limit {
		c.Truncated, c.Fidelity, c.CaptureError = true, core.FidelityTruncated, core.CaptureErrorOversize
		c.retain(raw, limit, frag.Policy)
		return c, Event{}, core.ErrBudget
	}
	if policy == nil || strings.TrimSpace(policyVersion) == "" {
		c.CaptureError = core.CaptureErrorPolicy
		return c, Event{}, fmt.Errorf("%w: capture policy unavailable", core.ErrDegraded)
	}
	// A short read is decided before the payload is inspected: a prefix that happens to parse is
	// still a prefix, and reading it as a complete delivery would be exactly the synthetic default
	// this contract exists to prevent.
	if frag.Incomplete {
		c.Fidelity, c.CaptureError = core.FidelityPartial, core.CaptureErrorIncomplete
		c.retain(raw, limit, frag.Policy)
		return c, Event{}, fmt.Errorf("%w: hook payload incomplete", core.ErrDegraded)
	}
	if !captureJSONObject(raw) {
		c.Fidelity, c.CaptureError = core.FidelityBinary, core.CaptureErrorNotJSON
		if c.retain(raw, limit, frag.Policy) {
			return c, Event{}, fmt.Errorf("%w: capture is not an admissible JSON object", core.ErrDegraded)
		}
		return c, Event{}, fmt.Errorf("%w: invalid capture JSON object", core.ErrContract)
	}
	decision, err := callCapturePolicy(policy, bytes.Clone(raw))
	if err != nil {
		c.CaptureError = core.CaptureErrorPolicy
		return c, Event{}, fmt.Errorf("%w: capture policy failed", core.ErrDegraded)
	}
	if decision.Outcome == core.OutcomeDenied {
		c.Outcome, c.Fidelity = core.OutcomeDenied, core.FidelityUnknown
		return c, Event{}, nil
	}
	if decision.Outcome != core.OutcomeOK {
		c.CaptureError = core.CaptureErrorPolicy
		return c, Event{}, fmt.Errorf("%w: capture policy did not permit retention", core.ErrDegraded)
	}
	if len(decision.Bytes) > limit {
		c.Truncated, c.Fidelity, c.CaptureError = true, core.FidelityTruncated, core.CaptureErrorOversize
		// The policy permitted these bytes as a whole document; the fragment policy re-examines the
		// prefix, which is a narrower disclosure it has not yet decided on.
		c.retain(decision.Bytes, limit, frag.Policy)
		return c, Event{}, core.ErrBudget
	}
	if !validCaptureDecision(raw, decision) {
		c.CaptureError = core.CaptureErrorContract
		return c, Event{}, fmt.Errorf("%w: inconsistent capture fidelity", core.ErrContract)
	}
	permitted := bytes.Clone(decision.Bytes)
	ev, err := captureEvent(permitted)
	if err != nil {
		c.CaptureError = core.CaptureErrorNotJSON
		return c, Event{}, err
	}
	c.Bytes, c.Fidelity = permitted, decision.Fidelity
	c.Outcome, c.Redacted, c.Truncated = decision.Outcome, decision.Redacted, decision.Truncated
	c.HostFields = observedHostFields(permitted)
	return c, ev, nil
}

// retain permits at most limit bytes of a payload that could not be admitted whole, recording the
// result on c and reporting whether anything was retained. Only the fragment policy's own OutcomeOK
// retains bytes; a missing, failing or refusing fragment policy leaves the capture byte-free, which
// stays the default whenever a caller supplies no fragment policy at all.
func (c *Capture) retain(raw []byte, limit int, policy CapturePolicy) bool {
	if policy == nil || limit <= 0 || len(raw) == 0 {
		return false
	}
	// Redaction lengthens what it examines whenever a placeholder is longer than the secret it
	// replaces, so a prefix that exactly fills the budget can come back over it. The second attempt
	// scales the prefix by the expansion the first one actually measured, rather than reserving a
	// guessed margin up front or abandoning retention the moment a result overflows.
	size := min(len(raw), limit)
	for attempt := 0; attempt < 2 && size > 0; attempt++ {
		decision, err := callCapturePolicy(policy, bytes.Clone(raw[:size]))
		if err != nil || decision.Outcome != core.OutcomeOK || len(decision.Bytes) == 0 {
			return false
		}
		if len(decision.Bytes) <= limit {
			c.Bytes = bytes.Clone(decision.Bytes)
			c.Redacted = c.Redacted || decision.Redacted
			return true
		}
		size = int(int64(size) * int64(limit) / int64(len(decision.Bytes)))
	}
	return false
}

// observedHostFields lists the top-level keys of an already-permitted payload, sorted. It is
// computed from permitted bytes only, so a key spelling the policy removed is never reintroduced.
func observedHostFields(permitted []byte) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(permitted, &fields) != nil || len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func captureEvent(raw []byte) (Event, error) {
	if !captureJSONObject(raw) {
		return Event{}, fmt.Errorf("%w: invalid capture JSON object", core.ErrContract)
	}
	// ReadEvent owns its read buffer and copies RawMessage fields. Neither the policy's buffer
	// nor Capture.Bytes can mutate the derived Event after this call.
	ev, _, err := ReadEvent(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Event{}, fmt.Errorf("%w: invalid capture JSON object", core.ErrContract)
	}
	return ev, nil
}

func captureJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && trimmed[0] == '{' && utf8.Valid(raw) && json.Valid(raw)
}

func callCapturePolicy(policy CapturePolicy, raw []byte) (decision core.CaptureDecision, err error) {
	defer func() {
		if recover() != nil {
			decision, err = core.CaptureDecision{}, core.ErrDegraded
		}
	}()
	return policy(raw)
}

func validCaptureDecision(raw []byte, d core.CaptureDecision) bool {
	switch d.Fidelity {
	case core.FidelityExact:
		return !d.Redacted && !d.Truncated && bytes.Equal(raw, d.Bytes)
	case core.FidelityRedacted:
		return d.Redacted
	case core.FidelityTruncated:
		return d.Truncated
	case core.FidelityPrefix, core.FidelityPartial, core.FidelityBinary, core.FidelityUnknown:
		return true
	default:
		return false
	}
}
