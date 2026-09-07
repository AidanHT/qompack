package core

import (
	"encoding/binary"
	"errors"
)

// EvidenceVersion versions additive evidence sidecars, independently of frozen records.
const EvidenceVersion = 1

// EvidenceHashVersion identifies the hash construction used by evidence records.
const EvidenceHashVersion = "sha256/v1"

// ObservationID identifies a delivery, independently of its payload and optional host ID.
type ObservationID string

// NewObservationID derives an identity from a durably assigned arrival sequence. Callers must
// persist the session and sequence before handling a delivery, and reuse them on retry. The
// fixed-width sequence suffix makes the concatenation unambiguous, even for an empty session.
func NewObservationID(session SessionID, arrival uint64) (ObservationID, error) {
	if arrival == 0 {
		return "", errors.New("qompack: observation arrival is unassigned")
	}
	b := make([]byte, len(session)+8)
	copy(b, session)
	binary.BigEndian.PutUint64(b[len(session):], arrival)
	return ObservationID(HashBytes("qompack.observation.v1", b).String()), nil
}

// Fidelity describes the retained source bytes; exact means the captured host delivery, not
// completeness of an underlying process, file, or native conversation.
type Fidelity string

const (
	FidelityExact     Fidelity = "exact"
	FidelityPrefix    Fidelity = "prefix"
	FidelityPartial   Fidelity = "partial"
	FidelityRedacted  Fidelity = "redacted"
	FidelityTruncated Fidelity = "truncated"
	FidelityBinary    Fidelity = "binary"
	FidelityFailure   Fidelity = "failure"
	FidelityUnknown   Fidelity = "unknown"
)

// Coverage describes an observed location or lifecycle outcome, never complete native history.
type Coverage string

const (
	CoverageQompackIncluded    Coverage = "qompack_included"
	CoverageArchiveOnly        Coverage = "archive_only"
	CoverageNativeLoadObserved Coverage = "native_load_observed"
	CoverageExpiredDeleted     Coverage = "expired_deleted"
	CoverageUnknown            Coverage = "unknown"
)

// EvidenceOutcome is domain data. Protocol errors are represented by the transport separately.
type EvidenceOutcome string

const (
	OutcomeOK          EvidenceOutcome = "ok"
	OutcomeAbsent      EvidenceOutcome = "absent"
	OutcomeUnavailable EvidenceOutcome = "unavailable"
	OutcomeDenied      EvidenceOutcome = "denied"
	OutcomeCorrupt     EvidenceOutcome = "corrupt"
	OutcomeExpired     EvidenceOutcome = "expired"
	OutcomeUncertain   EvidenceOutcome = "uncertain"
)

// Authority describes a derived statement's source; content cannot promote its own authority.
type Authority string

const (
	AuthorityUserCorrection      Authority = "user_correction"
	AuthorityExplicitDecision    Authority = "explicit_decision"
	AuthorityToolObservation     Authority = "tool_observation"
	AuthorityCandidateExtraction Authority = "candidate_extraction"
	AuthorityHypothesis          Authority = "hypothesis"
	AuthorityConflict            Authority = "conflict"
)

// EvidenceValidity is an observed turn interval and the generation in which it was assessed.
// A zero generation means freshness is unknown. To == 0 is an open interval, not proof that
// the evidence remains applicable to current intent or dependencies.
type EvidenceValidity struct {
	From       TurnIndex `json:"from"`
	To         TurnIndex `json:"to"`
	Generation uint64    `json:"generation"`
}

type EvidencePage struct {
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"has_more"`
}

// Omission explains a coverage limitation and the next recovery action. Neither field contains
// omitted payload bytes or secrets. Reasons are descriptive data, not an authority assertion.
type Omission struct {
	Reason   string `json:"reason"`
	Recovery string `json:"recovery,omitempty"`
}

// EvidenceEnvelope accompanies a retrieval result without altering frozen payload records.
// It is not the on-disk observation record: store sidecars must additionally persist their
// transform registry and hash construction versions.
// HostID is optional and is copied only from a supplied host field. Root is a content identifier,
// not an authorization token. Producers may assert absent only with complete, fresh coverage.
type EvidenceEnvelope struct {
	Version       int              `json:"v"`
	ObservationID ObservationID    `json:"observation_id,omitempty"`
	HostID        string           `json:"host_id,omitempty"`
	Root          Hash             `json:"root"`
	Fidelity      Fidelity         `json:"fidelity"`
	Coverage      Coverage         `json:"coverage"`
	Validity      EvidenceValidity `json:"validity"`
	Omissions     []Omission       `json:"omissions,omitempty"`
	Page          EvidencePage     `json:"page"`
	Outcome       EvidenceOutcome  `json:"outcome"`
}

// Qualified prevents legacy, missing, or future enum fields from acquiring a success meaning.
// It does not certify publication, authorization, current applicability, or coverage freshness.
func (e EvidenceEnvelope) Qualified() EvidenceEnvelope {
	if e.Version != EvidenceVersion {
		e.Fidelity, e.Coverage, e.Outcome = FidelityUnknown, CoverageUnknown, OutcomeUncertain
		return e
	}
	switch e.Fidelity {
	case FidelityExact, FidelityPrefix, FidelityPartial, FidelityRedacted, FidelityTruncated,
		FidelityBinary, FidelityFailure, FidelityUnknown:
	default:
		e.Fidelity = FidelityUnknown
		e.Outcome = OutcomeUncertain
	}
	switch e.Coverage {
	case CoverageQompackIncluded, CoverageArchiveOnly, CoverageNativeLoadObserved,
		CoverageExpiredDeleted, CoverageUnknown:
	default:
		e.Coverage = CoverageUnknown
		e.Outcome = OutcomeUncertain
	}
	switch e.Outcome {
	case OutcomeOK, OutcomeAbsent, OutcomeUnavailable, OutcomeDenied, OutcomeCorrupt,
		OutcomeExpired, OutcomeUncertain:
	default:
		e.Outcome = OutcomeUncertain
	}
	// An envelope alone has no proof of a complete, fresh lookup. Until its producer supplies
	// that independently verified witness, absent would overstate what this type can establish.
	if e.Outcome == OutcomeAbsent {
		e.Outcome = OutcomeUncertain
	}
	if e.Validity.From < 0 || e.Validity.To < 0 || (e.Validity.To != 0 && e.Validity.To < e.Validity.From) {
		e.Validity = EvidenceValidity{}
		e.Outcome = OutcomeUncertain
	}
	return e
}

// Valid refuses future or missing authority labels; callers must retain them as unknown rather
// than treating the zero value as user authority. Validation alone does not authorize a source.
func (a Authority) Valid() bool {
	switch a {
	case AuthorityUserCorrection, AuthorityExplicitDecision, AuthorityToolObservation,
		AuthorityCandidateExtraction, AuthorityHypothesis, AuthorityConflict:
		return true
	default:
		return false
	}
}
