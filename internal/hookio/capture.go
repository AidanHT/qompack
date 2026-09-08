package hookio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
)

// CapturePolicy decides which bytes may cross the persistence boundary. Only OutcomeOK admits
// its returned bytes. The policy must cover the entire host payload, including unknown fields;
// a missing policy never implies permission. Policy implementations must not persist their input.
type CapturePolicy func([]byte) (core.CaptureDecision, error)

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
}

// CaptureHook applies the caller's explicit privacy policy before parsing a retained Event.
// It performs no I/O. Runtime capture remains disabled until ingress, publication and compatible
// readers consume this seam and their gates pass; callers must never persist raw on failure.
func CaptureHook(raw []byte, limit int, policyVersion string, policy CapturePolicy) (Capture, Event, error) {
	c := Capture{
		Version: core.EvidenceVersion, SourceFormat: "application/json",
		PolicyVersion: policyVersion, HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityFailure, Outcome: core.OutcomeUnavailable,
	}
	if limit < 0 || len(raw) > limit {
		c.Truncated = true
		return c, Event{}, core.ErrBudget
	}
	if policy == nil || strings.TrimSpace(policyVersion) == "" {
		return c, Event{}, fmt.Errorf("%w: capture policy unavailable", core.ErrDegraded)
	}
	if !captureJSONObject(raw) {
		return c, Event{}, fmt.Errorf("%w: invalid capture JSON object", core.ErrContract)
	}
	decision, err := callCapturePolicy(policy, bytes.Clone(raw))
	if err != nil {
		return c, Event{}, fmt.Errorf("%w: capture policy failed", core.ErrDegraded)
	}
	if decision.Outcome == core.OutcomeDenied {
		c.Outcome, c.Fidelity = core.OutcomeDenied, core.FidelityUnknown
		return c, Event{}, nil
	}
	if decision.Outcome != core.OutcomeOK {
		return c, Event{}, fmt.Errorf("%w: capture policy did not permit retention", core.ErrDegraded)
	}
	if len(decision.Bytes) > limit {
		c.Truncated = true
		return c, Event{}, core.ErrBudget
	}
	if !validCaptureDecision(raw, decision) {
		return c, Event{}, fmt.Errorf("%w: inconsistent capture fidelity", core.ErrContract)
	}
	permitted := bytes.Clone(decision.Bytes)
	ev, err := captureEvent(permitted)
	if err != nil {
		return c, Event{}, err
	}
	c.Bytes, c.Fidelity = permitted, decision.Fidelity
	c.Outcome, c.Redacted, c.Truncated = decision.Outcome, decision.Redacted, decision.Truncated
	return c, ev, nil
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
