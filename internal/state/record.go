// Package state is the derived-state model of V4-SP-20 M2-01: what Qompack believes, as opposed
// to what it observed.
//
// An observation is immutable and lives elsewhere; a Record here is a statement DERIVED from one
// or more observations and is identified only by their [core.ObservationID]s. That separation is
// required invariant 7 — "derived state cannot silently acquire user authority or overwrite
// immutable evidence" — expressed as a type: nothing in this package holds observation bytes,
// rewrites an observation, or lets a record change its own [core.Authority] after it is admitted.
//
// The package is a pure, storage-agnostic model. [Set] holds records in memory and applies the
// authority rules; [Set.Encode] and [Decode] give it a versioned durable form so a later unit can
// persist it. There is no I/O, no clock and no dependency beyond internal/core.
//
// The rules that are worth knowing before reading further:
//
//   - Records are append-only. An id is written once ([core.ErrAppendOnly] on a second Add), and
//     no transition deletes or rewrites a record: a superseded, corrected or conflicted record
//     stays readable through [Set.Get] and [Set.All], with links in BOTH directions and a
//     retained [Lineage.History].
//   - Authority is not self-asserted. Only [AuthorityUserCorrection] and
//     [AuthorityExplicitDecision] are authoritative; only they may supersede an authoritative
//     record, correct anything, or resolve a conflict. A hypothesis or a candidate extraction
//     never becomes an instruction by being newer.
//   - A conflict is rendered AS a conflict ([Set.Conflicts]) until an authorized source resolves
//     it. It is never silently resolved, dropped, or averaged into a third answer.
//   - Scope fails closed. A record scoped to a session applies to that session only, and the zero
//     [Scope] applies nowhere.
package state

import (
	"encoding/binary"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// RecordVersion versions a single derived-state record, independently of the snapshot that
// carries it. A reader that meets a record it cannot version refuses it rather than assuming this
// version's meaning.
const RecordVersion = 1

// TransformVersion identifies the derivation this package performs on the observations a record
// cites. It travels on every record so a consumer can tell a record derived by this version from
// one derived by another; it is a wire value, not an identifier to reformat.
const TransformVersion = "state/v1"

// DomainRecordID is the hash domain of a minted [RecordID]. It is declared here with the rest of
// this package's wire constants; changing it re-keys every record id already on disk.
const DomainRecordID = "qompack.state.record.v1"

// RecordID identifies a derived-state record. It is distinct from every observation identity a
// record cites: state is derived, observations are not, and conflating the two is what would let
// a correction look like a rewrite of the evidence underneath it.
type RecordID string

// NewRecordID derives a stable id from a scope and a durably assigned per-scope sequence. Callers
// persist the sequence before minting, and reuse it on retry, so a replayed derivation produces
// the same id instead of a second record. A zero sequence is unassigned and wraps
// [core.ErrContract]; the fixed-width suffix keeps the concatenation unambiguous for an empty
// scope field.
//
// Callers with their own identity scheme may supply any non-empty [RecordID] instead; this is a
// convenience, not a requirement.
func NewRecordID(scope Scope, sequence uint64) (RecordID, error) {
	if sequence == 0 {
		return "", fmt.Errorf("%w: state record sequence is unassigned", core.ErrContract)
	}
	preimage := []byte(scope.Worktree)
	preimage = append(preimage, fieldSep)
	preimage = append(preimage, scope.Session...)
	preimage = append(preimage, fieldSep)
	preimage = binary.BigEndian.AppendUint64(preimage, sequence)
	return RecordID(core.HashBytes(DomainRecordID, preimage).String()), nil
}

// fieldSep is ASCII Unit Separator: a control character that cannot appear in a normalized
// worktree path or a host session id, which is what makes it an unambiguous delimiter between
// them. It matches the separator negknow's key preimages use, for the same reason.
const fieldSep = byte(0x1F)

// Scope is where a derived record applies. It fails closed in both directions: the zero Scope
// applies nowhere, and a query that names no scope matches nothing. A record that is silently
// global is precisely the leak invariant 7's scope half exists to prevent.
//
// A record scoped to a worktree ([WorktreeScope]) applies to every session in it; a record scoped
// to a session ([SessionScope]) applies to that session only, and does not answer a
// worktree-wide question.
type Scope struct {
	// Worktree is the project root the record was derived in, in whatever normalized spelling
	// the caller uses consistently. This package compares it; it never interprets it as a path.
	Worktree string `json:"worktree,omitempty"`
	// Session is the host session the record is confined to. Empty means worktree-wide.
	Session core.SessionID `json:"session,omitempty"`
}

// WorktreeScope confines a record to one worktree, across all of its sessions.
func WorktreeScope(worktree string) Scope { return Scope{Worktree: worktree} }

// SessionScope confines a record to one session inside one worktree.
func SessionScope(worktree string, session core.SessionID) Scope {
	return Scope{Worktree: worktree, Session: session}
}

// IsZero reports whether s names nothing. A zero Scope is not "everywhere": it is a record whose
// applicability was never established, and [Scope.AppliesTo] answers false for it.
func (s Scope) IsZero() bool { return s == Scope{} }

// AppliesTo reports whether a record scoped to s is applicable to the situation q describes.
// Nothing about being newer, more authoritative or more confident widens a scope; the only way a
// record reaches another scope is for a caller to derive a new record there.
func (s Scope) AppliesTo(q Scope) bool {
	if s.IsZero() || q.IsZero() {
		return false
	}
	if s.Worktree != q.Worktree {
		return false
	}
	// An empty session on the record is worktree-wide and answers any session's question. An
	// empty session on the QUERY is a worktree-wide question, which a session-scoped record
	// cannot answer: it holds only for the one session it was derived in.
	return s.Session == "" || s.Session == q.Session
}

// DepCoverage says how much of a record's dependency set was actually established. It is carried,
// never inferred: an unreadable or missing label reads as [DepCoverageUnknown], because a
// derivation whose inputs are unknown must not present itself as complete (invariant 8).
type DepCoverage string

const (
	// DepCoverageComplete means every dependency of the derivation is listed and was observed.
	DepCoverageComplete DepCoverage = "complete"
	// DepCoveragePartial means dependencies are listed but the set is known to be incomplete.
	DepCoveragePartial DepCoverage = "partial"
	// DepCoverageUnknown means the completeness of the set could not be established at all.
	DepCoverageUnknown DepCoverage = "unknown"
)

// Valid refuses missing or future coverage labels. It does not assert that the coverage claimed
// is true, only that this version knows what the label means.
func (c DepCoverage) Valid() bool {
	switch c {
	case DepCoverageComplete, DepCoveragePartial, DepCoverageUnknown:
		return true
	default:
		return false
	}
}

// Dependencies is what a record's derivation depended on, and how well that set is known.
// Deps reuses [core.Dep] — the §8.3 staleness guard of path plus content hash — so a consumer can
// answer "is this still true of the tree" with the machinery it already has.
type Dependencies struct {
	Deps []core.Dep `json:"deps,omitempty"`
	// Coverage qualifies Deps. A record with an empty Deps list and DepCoverageComplete depends
	// on nothing; the same list with DepCoverageUnknown depends on something nobody enumerated.
	Coverage DepCoverage `json:"coverage"`
	// Reason explains an incomplete or unknown coverage and, where possible, names the recovery.
	// It is descriptive data, never an authority assertion.
	Reason string `json:"reason,omitempty"`
}

// LineageKind names one retained transition in a record's history.
type LineageKind string

const (
	// LineageSuperseded records that another record took this one's place.
	LineageSuperseded LineageKind = "superseded"
	// LineageCorrected records that an authorized source corrected this record. Every correction
	// is also a supersession, so both entries are written.
	LineageCorrected LineageKind = "corrected"
	// LineageConflicted records that this record became party to a conflict.
	LineageConflicted LineageKind = "conflicted"
	// LineageResolved records that an authorized source resolved a conflict.
	LineageResolved LineageKind = "resolved"
)

// LineageEntry is one transition, retained forever. It names the authority that made the
// transition, so a reader can tell a user correction from an automatic supersession without
// looking anything up.
type LineageEntry struct {
	Kind LineageKind `json:"kind"`
	// Other is the record on the far side of the transition.
	Other RecordID `json:"other"`
	// Authority is the authority of the record that caused the transition.
	Authority core.Authority `json:"authority"`
	// Turn is the turn the transition was applied at.
	Turn core.TurnIndex `json:"turn"`
	// Note is free text from the caller. It is never interpreted.
	Note string `json:"note,omitempty"`
}

// Lineage is a record's relationships, in both directions. Forward and reverse links are both
// stored because a consumer asking "what replaced this?" and one asking "what did this replace?"
// are different questions, and reconstructing either by scanning the whole set would make an
// unlinked record indistinguishable from a missing one.
//
// A producer cannot assert its own lineage: [Set.Add] refuses a record that arrives with links
// already in it. Only [Set.Supersede], [Set.Correct], [Set.Conflict] and [Set.Resolve] write
// here, and each checks authority first.
type Lineage struct {
	Supersedes   []RecordID `json:"supersedes,omitempty"`
	SupersededBy []RecordID `json:"superseded_by,omitempty"`
	Corrects     []RecordID `json:"corrects,omitempty"`
	CorrectedBy  []RecordID `json:"corrected_by,omitempty"`
	// ConflictedBy names the conflict records this record is a party to. It is retained after
	// resolution: the conflict happened, and the history says so.
	ConflictedBy []RecordID `json:"conflicted_by,omitempty"`
	// History is every transition in the order it was applied. It is additive only.
	History []LineageEntry `json:"history,omitempty"`
}

// IsZero reports whether l holds no links and no history, which is the only shape [Set.Add]
// accepts from a producer.
func (l Lineage) IsZero() bool {
	return len(l.Supersedes) == 0 && len(l.SupersededBy) == 0 && len(l.Corrects) == 0 &&
		len(l.CorrectedBy) == 0 && len(l.ConflictedBy) == 0 && len(l.History) == 0
}

func (l Lineage) clone() Lineage {
	return Lineage{
		Supersedes:   cloneIDs(l.Supersedes),
		SupersededBy: cloneIDs(l.SupersededBy),
		Corrects:     cloneIDs(l.Corrects),
		CorrectedBy:  cloneIDs(l.CorrectedBy),
		ConflictedBy: cloneIDs(l.ConflictedBy),
		History:      cloneSlice(l.History),
	}
}

// Conflict is disagreement, represented rather than settled. Its record carries
// [core.AuthorityConflict], and until Resolution is written by an authorized source the parties
// are withheld from [Set.Applicable] and the conflict itself is reported by [Set.Conflicts].
type Conflict struct {
	// Parties are the records that disagree, in the order the conflict was declared with. None
	// of them is preferred, ranked, merged, or averaged by this package.
	Parties []RecordID `json:"parties"`
	// Reason describes the disagreement for a human. It is not a resolution.
	Reason string `json:"reason,omitempty"`
	// Resolution is nil until an authorized source resolves the conflict.
	Resolution *Resolution `json:"resolution,omitempty"`
}

// Resolved reports whether an authorized source has resolved c. A nil Conflict is not resolved:
// it is not a conflict at all.
func (c *Conflict) Resolved() bool { return c != nil && c.Resolution != nil }

func (c *Conflict) clone() *Conflict {
	if c == nil {
		return nil
	}
	out := Conflict{Parties: cloneIDs(c.Parties), Reason: c.Reason}
	if c.Resolution != nil {
		r := *c.Resolution
		out.Resolution = &r
	}
	return &out
}

// Resolution records who ended a conflict and when. Authority is copied from the resolving
// record at resolution time so the audit trail survives even if that record is later superseded.
type Resolution struct {
	By        RecordID       `json:"by"`
	Authority core.Authority `json:"authority"`
	Turn      core.TurnIndex `json:"turn"`
	Reason    string         `json:"reason,omitempty"`
}

// Record is one derived-state statement: what is believed, on whose authority, where it applies,
// what it depended on, when it held, and which immutable observations it came from.
//
// The observations are referenced by identity only. Nothing in this type holds observed bytes,
// and no method rewrites Sources — that is the "never overwrite immutable evidence" half of
// invariant 7, and it is why a correction can supersede a record without touching what was seen.
type Record struct {
	// Version is [RecordVersion] for records this package produces.
	Version int `json:"v"`
	// ID is unique within a [Set] and written exactly once.
	ID RecordID `json:"id"`
	// Authority is which of core's six kinds produced this statement. It is fixed at admission:
	// no transition promotes it, and an unclassifiable label never becomes user authority.
	Authority core.Authority `json:"authority"`
	// Scope is where the statement applies. See [Scope].
	Scope Scope `json:"scope"`
	// Claim is the statement itself, as text. This package stores it and never interprets it.
	Claim string `json:"claim"`
	// Dependencies is what the derivation rested on, with its coverage.
	Dependencies Dependencies `json:"dependencies"`
	// Validity is the observed turn interval and the generation it was assessed in. To == 0 is
	// an open interval, not proof of current applicability (see [core.EvidenceValidity]).
	Validity core.EvidenceValidity `json:"validity"`
	// Sources are the immutable observation identities this record was derived from. At least
	// one is required: a derived record with no evidence behind it is not admitted.
	Sources []core.ObservationID `json:"sources"`
	// TransformVersion is the derivation version, normally [TransformVersion].
	TransformVersion string `json:"transform_version"`
	// HashVersion is the hash construction the derivation used, normally
	// [core.EvidenceHashVersion].
	HashVersion string `json:"hash_version"`
	// Lineage is supersession, correction and conflict linkage with retained history.
	Lineage Lineage `json:"lineage"`
	// Conflict is set only on a record whose Authority is [core.AuthorityConflict].
	Conflict *Conflict `json:"conflict,omitempty"`
	// RecordedAt is an optional wall-clock stamp supplied by the caller. This package has no
	// clock and never fills it in.
	RecordedAt core.UnixMilli `json:"recorded_at,omitempty"`
}

// Superseded reports whether some other record has taken r's place. A superseded record is still
// readable and still true of the past; it is simply not what applies now.
func (r Record) Superseded() bool { return len(r.Lineage.SupersededBy) > 0 }

// Clone returns a deep copy. Every value crossing the [Set] boundary is cloned in both
// directions, so a caller can neither alias the set's evidence citations nor edit a record's
// authority in place after reading it.
func (r Record) Clone() Record {
	out := r
	out.Dependencies.Deps = cloneSlice(r.Dependencies.Deps)
	out.Sources = cloneSlice(r.Sources)
	out.Lineage = r.Lineage.clone()
	out.Conflict = r.Conflict.clone()
	return out
}

// Validate reports whether r is admissible as a producer's record. Every failure wraps
// [core.ErrContract]. It is deliberately strict about the fields that carry meaning a consumer
// would otherwise infer — authority, scope, coverage, sources and the two versions — because
// each missing one is a place where a derived record could quietly acquire a standing it has not
// earned.
//
// Lineage and Conflict must be empty: they are written by the [Set] transitions, never asserted.
func (r Record) Validate() error {
	if r.Authority == core.AuthorityConflict {
		return fmt.Errorf("%w: state record %q claims conflict authority; conflicts are created by Set.Conflict",
			core.ErrContract, r.ID)
	}
	return r.validateFields()
}

// validateFields is [Record.Validate] minus the conflict-authority refusal, so that
// [Set.Conflict] can hold a conflict marker to every other rule. It is not exported: a conflict
// record is admissible through that one path and nowhere else.
func (r Record) validateFields() error {
	switch {
	case r.Version != RecordVersion:
		return fmt.Errorf("%w: state record version %d, want %d", core.ErrContract, r.Version, RecordVersion)
	case r.ID == "":
		return fmt.Errorf("%w: state record has no id", core.ErrContract)
	case !r.Authority.Valid():
		return fmt.Errorf("%w: state record %q has authority %q, which is not one of core's six kinds",
			core.ErrContract, r.ID, r.Authority)
	case r.Scope.IsZero():
		return fmt.Errorf("%w: state record %q has no scope; an unscoped record would apply everywhere",
			core.ErrContract, r.ID)
	case r.Claim == "":
		return fmt.Errorf("%w: state record %q has no claim", core.ErrContract, r.ID)
	case len(r.Sources) == 0:
		return fmt.Errorf("%w: state record %q cites no source observation", core.ErrContract, r.ID)
	case !r.Dependencies.Coverage.Valid():
		return fmt.Errorf("%w: state record %q has dependency coverage %q, which is not a known label",
			core.ErrContract, r.ID, r.Dependencies.Coverage)
	case r.TransformVersion == "":
		return fmt.Errorf("%w: state record %q has no transform version", core.ErrContract, r.ID)
	case r.HashVersion == "":
		return fmt.Errorf("%w: state record %q has no hash version", core.ErrContract, r.ID)
	case !validInterval(r.Validity):
		return fmt.Errorf("%w: state record %q has an impossible validity interval [%d,%d]",
			core.ErrContract, r.ID, r.Validity.From, r.Validity.To)
	case !r.Lineage.IsZero():
		return fmt.Errorf("%w: state record %q asserts its own lineage; use Supersede, Correct, Conflict or Resolve",
			core.ErrContract, r.ID)
	case r.Conflict != nil:
		return fmt.Errorf("%w: state record %q carries a conflict payload without conflict authority",
			core.ErrContract, r.ID)
	}
	for i, src := range r.Sources {
		if src == "" {
			return fmt.Errorf("%w: state record %q cites an empty observation id at position %d",
				core.ErrContract, r.ID, i)
		}
	}
	return nil
}

// validInterval mirrors [core.EvidenceEnvelope.Qualified]'s interval rule: turns are non-negative
// and a closed interval does not end before it starts.
func validInterval(v core.EvidenceValidity) bool {
	return v.From >= 0 && v.To >= 0 && (v.To == 0 || v.To >= v.From)
}

// Authoritative reports whether a may speak for the user. Exactly two of core's six labels can:
// an explicit user correction and an explicit decision. A tool observation is evidence, a
// candidate extraction is a guess about evidence, a hypothesis is a guess, and a conflict is a
// question — none of them is an instruction, however recent.
func Authoritative(a core.Authority) bool {
	return a == core.AuthorityUserCorrection || a == core.AuthorityExplicitDecision
}

// authorityRank orders the non-authoritative labels so that a supersession cannot lower the
// standing of what is believed. [core.AuthorityConflict] has no rank: a conflict marker states a
// question and never supersedes anything.
func authorityRank(a core.Authority) int {
	switch a {
	case core.AuthorityUserCorrection, core.AuthorityExplicitDecision:
		return 4
	case core.AuthorityToolObservation:
		return 3
	case core.AuthorityCandidateExtraction:
		return 2
	case core.AuthorityHypothesis:
		return 1
	default: // core.AuthorityConflict and every unknown label.
		return 0
	}
}

// CanSupersede reports whether a record with authority newer may take the place of one with
// authority existing.
//
// Two rules, and the first is the one that matters:
//
//  1. An authoritative record — a user correction or an explicit decision — may be superseded
//     only by another authoritative record. No accumulation of hypotheses, extractions or tool
//     observations promotes itself into an instruction.
//  2. Otherwise a record may be superseded by one of equal or greater standing, so a supersession
//     never quietly downgrades what is believed.
//
// A conflict never supersedes: it is raised by [Set.Conflict] and ended by [Set.Resolve]. An
// unknown label never supersedes either, since this version cannot tell what it would mean.
func CanSupersede(newer, existing core.Authority) bool {
	if !newer.Valid() || !existing.Valid() || newer == core.AuthorityConflict {
		return false
	}
	if Authoritative(existing) {
		return Authoritative(newer)
	}
	return authorityRank(newer) >= authorityRank(existing)
}

func cloneIDs(in []RecordID) []RecordID { return cloneSlice(in) }

// cloneSlice copies a slice of value types, preserving nil so that an absent field stays absent
// through an encode/decode round trip instead of becoming an empty list.
func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}
