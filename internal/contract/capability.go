package contract

import (
	"errors"
	"fmt"
	"strings"
)

// This file is SP-19 M0-03's capability register: what this build claims about the HOST, on what
// evidence, and which of those claims is allowed to be switched on.
//
// It exists because the nine §5.19 assertions answer a different question. An Assertion asks "did
// this mechanism fire during this session"; a CapabilityRecord asks "is this mechanism supported
// at all, against which target, proven by what". 00-ARCHITECTURE.md §12.1 (v1.5) requires the two
// to stay separate: the monitor reports an absent producer as OK/SevInfo so that a wave-1 build is
// not degraded by a wave-4 subsystem, and that reading must never be spendable as evidence a
// capability works. The register is the thing that cannot be fooled by it.
//
// Nothing here reads the filesystem or the host. It is a declaration plus the rules that declaration
// must satisfy, so that "we enabled an optimization on documentation alone" is a test failure rather
// than a production incident.

// capabilityRegisterVersion is the schema version DefaultCapabilityRegister emits and Validate
// requires. It is a data-format number: a register carrying any other version is one this build
// cannot safely interpret, and Validate refuses it rather than guessing.
const capabilityRegisterVersion = 1

// Capability names one host capability SP-19's interface contract separates. The eight below are
// the complete set of the plan's "Capability register" row and plans/MIGRATION-EVIDENCE.md's
// "Capability decisions" table.
//
// They are separated precisely so evidence for one cannot be spent on another: observing that a
// hook fired says nothing about whether injected context was delivered, and neither says anything
// about whether the host would honour a compaction request. The string form is persisted, so a
// value may not be respelled.
type Capability string

// The eight capabilities, in the order the ledger's table states them.
const (
	// CapObservation: reading host hook payloads at all. This is the one capability the plugin
	// cannot work without, and the only recording capability that is on.
	CapObservation Capability = "observation"
	// CapInjection: getting Qompack-authored text into the session through SessionStart
	// additionalContext.
	CapInjection Capability = "injection"
	// CapNewResultReplacement: replacing a newly delivered tool result before the model sees it.
	CapNewResultReplacement Capability = "new_result_replacement"
	// CapUsageAttribution: attributing reported token/cache usage to a request and its parent.
	CapUsageAttribution Capability = "usage_attribution"
	// CapTokenEstimation: estimating the token cost of an assembled representation locally.
	CapTokenEstimation Capability = "token_estimation"
	// CapCompactionRequest: asking the host to compact. No supported plugin mechanism exists here.
	CapCompactionRequest Capability = "compaction_request"
	// CapCompactionBlocking: vetoing a compaction the host is about to perform.
	CapCompactionBlocking Capability = "compaction_blocking"
	// CapHistoryRewriting: rewriting, cutting or evicting native conversation history.
	CapHistoryRewriting Capability = "history_rewriting"
)

// capabilitiesInLedgerOrder is the normative order and the normative SET. Validate reads it to
// decide what a complete register looks like, so adding a capability here without adding its record
// to DefaultCapabilityRegister fails immediately rather than silently.
var capabilitiesInLedgerOrder = []Capability{
	CapObservation,
	CapInjection,
	CapNewResultReplacement,
	CapUsageAttribution,
	CapTokenEstimation,
	CapCompactionRequest,
	CapCompactionBlocking,
	CapHistoryRewriting,
}

// Capabilities returns the eight capabilities in the ledger's order. The returned slice is a copy:
// the package's own notion of the complete set is not something a caller may edit.
func Capabilities() []Capability {
	out := make([]Capability, len(capabilitiesInLedgerOrder))
	copy(out, capabilitiesInLedgerOrder)
	return out
}

// EvidenceStatus is how well established a capability's support is. SP-19's interface contract
// fixes these six spellings, and the distinctions between them are the whole point:
// implemented_unverified means OUR code exists, documented means the HOST's documentation says so,
// and neither is verified_in_target.
type EvidenceStatus string

// The six evidence statuses.
const (
	// StatusDocumented: the host's own documentation describes the mechanism. The installed
	// version's behaviour is not established by this.
	StatusDocumented EvidenceStatus = "documented"
	// StatusVerifiedInTarget: a canary observed the mechanism working against a named
	// provider/version/platform on a named date. The only status that may enable an optimization.
	StatusVerifiedInTarget EvidenceStatus = "verified_in_target"
	// StatusImplementedUnverified: our adapter exists and runs; its integration against a real
	// target has not been certified (plans/MIGRATION-EVIDENCE.md's own definition).
	StatusImplementedUnverified EvidenceStatus = "implemented_unverified"
	// StatusUnsupported: there is no mechanism here to attempt. Distinct from "we looked and saw
	// nothing" — §12.1 requires reported absence, unknown observation and unsupported mechanism to
	// stay three different outcomes.
	StatusUnsupported EvidenceStatus = "unsupported"
	// StatusExperimental: exercised, but only as a dataset- or platform-bound observation.
	StatusExperimental EvidenceStatus = "experimental"
	// StatusUnknown: not established either way. Never a licence to act.
	StatusUnknown EvidenceStatus = "unknown"
)

// evidenceStatuses is the closed set Validate checks membership against.
var evidenceStatuses = map[EvidenceStatus]bool{
	StatusDocumented:            true,
	StatusVerifiedInTarget:      true,
	StatusImplementedUnverified: true,
	StatusUnsupported:           true,
	StatusExperimental:          true,
	StatusUnknown:               true,
}

// CapabilityClass is what a capability DOES, which is what decides how much evidence enabling it
// requires. Qompack.md §12's sidecar rule — "it must never be the reason a session gets worse" —
// is not equally at stake in all three: recording wrongly loses fidelity, acting wrongly adds
// noise, and optimizing wrongly can destroy a session's context.
type CapabilityClass string

// The three classes.
const (
	// ClassRecord observes and stores. Failure degrades Qompack's own data, not the session.
	ClassRecord CapabilityClass = "record"
	// ClassAct adds something to the session. Failure is visible to the user but recoverable.
	ClassAct CapabilityClass = "act"
	// ClassOptimize changes or suppresses what the host would otherwise do. Failure is not
	// recoverable by the user, which is why enabling one requires verified_in_target evidence.
	ClassOptimize CapabilityClass = "optimize"
)

// canonicalClass fixes each capability's class.
//
// Validate enforces it, and that enforcement is not bureaucratic: without it, a record could escape
// every optimize-class rule below simply by relabelling itself ClassRecord. The class is a property
// of what the capability does, not a field its author may choose.
var canonicalClass = map[Capability]CapabilityClass{
	CapObservation:          ClassRecord,
	CapUsageAttribution:     ClassRecord,
	CapTokenEstimation:      ClassRecord,
	CapInjection:            ClassAct,
	CapNewResultReplacement: ClassOptimize,
	CapCompactionRequest:    ClassOptimize,
	CapCompactionBlocking:   ClassOptimize,
	CapHistoryRewriting:     ClassOptimize,
}

// manualCompactPromise is the sentence Qompack.md §12 and 00-ARCHITECTURE.md §12.1 both commit to,
// and the one a user can actually be harmed by breaking: Qompack must never be the reason a manual
// /compact does not happen.
//
// Validate requires compaction_blocking's Fallback to contain it UNCONDITIONALLY — not only while
// the capability is disabled. Stated plainly: the register must always say manual compact is never
// blocked. Gating the check on Enabled would make the promise disappear from the record at exactly
// the moment it starts to matter.
const manualCompactPromise = "manual compact never blocked"

// Target is the host a capability's evidence was gathered against (M0-G2: "mechanism plus target
// canary artifact including host/provider/OS/version/date"). Its zero value means no target, which
// is the state every record in this build is in.
type Target struct {
	// Provider is the host product, e.g. "claude-code".
	Provider string `json:"provider,omitempty"`
	// Version is that host's own version string, e.g. "2.1.263".
	Version string `json:"version,omitempty"`
	// Platform is GOOS/GOARCH, e.g. "windows/amd64".
	Platform string `json:"platform,omitempty"`
	// Date is when the observation was made, as an ISO-8601 date or timestamp. Evidence without a
	// date cannot be aged out, and a host's behaviour is version- and time-bound.
	Date string `json:"date,omitempty"`
}

// Zero reports whether t names no target at all.
func (t Target) Zero() bool { return t == Target{} }

// String renders t for a log line or a canary artifact: "claude-code 2.1.263 windows/amd64
// 2026-09-07", or "unknown target" for the zero value.
func (t Target) String() string {
	if t.Zero() {
		return "unknown target"
	}
	parts := make([]string, 0, 4)
	for _, s := range []string{t.Provider, t.Version, t.Platform, t.Date} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// CapabilityRecord is one capability's declared support, evidence and production disposition.
type CapabilityRecord struct {
	Capability Capability      `json:"capability"`
	Class      CapabilityClass `json:"class"`
	// Mechanism names the concrete adapter that would be attempted. "none" — spelled out — is a
	// legitimate value and is what an unsupported capability carries.
	Mechanism string         `json:"mechanism"`
	Status    EvidenceStatus `json:"status"`
	// Target is the host the evidence was gathered against; zero while none has been.
	Target Target `json:"target"`
	// Source is where the evidence came from: a document, a ledger row, an artifact path.
	Source string `json:"source"`
	// Canary names the test that would falsify the claim. Required for an enabled acting
	// capability; informational elsewhere, but a claim nothing can falsify is worth recording as
	// such.
	Canary string `json:"canary,omitempty"`
	// Enabled is the production disposition: whether this build actually uses the mechanism.
	Enabled bool `json:"enabled"`
	// Fallback is what happens instead while Enabled is false. A disabled capability with no
	// stated fallback is an unanswered question, not a safe default.
	Fallback string `json:"fallback,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// CapabilityRegister is the whole declaration: a version plus one record per capability.
type CapabilityRegister struct {
	Version int                `json:"version"`
	Records []CapabilityRecord `json:"records"`
}

// ErrCapabilityRegister is the sentinel every Validate failure wraps, so a caller can distinguish
// "this register is malformed" from any other error on the same path.
var ErrCapabilityRegister = errors.New("contract: invalid capability register")

// DefaultCapabilityRegister returns the register this build ships: a transcription of
// plans/MIGRATION-EVIDENCE.md's "Capability decisions" table, which is the accepted decision record
// for what SP-19 may and may not switch on before M0-G2 has a target canary artifact.
//
// Every optimization is off, because none has verified_in_target evidence and B01 blocks target
// certification. Nothing carries a Target, for the same reason.
func DefaultCapabilityRegister() CapabilityRegister {
	return CapabilityRegister{
		Version: capabilityRegisterVersion,
		Records: []CapabilityRecord{
			{
				Capability: CapObservation,
				Class:      ClassRecord,
				Mechanism:  "hook payloads read through internal/hookio across the seven declared events",
				Status:     StatusImplementedUnverified,
				Source:     "plans/MIGRATION-EVIDENCE.md E02/E13 capability decisions; 00-ARCHITECTURE.md §12.1",
				Canary:     "TestCanary_InvalidHookPayload",
				Enabled:    true,
				Notes: "recording is preserved; payload and relationship gaps are reported, not a complete host " +
					"history. Distinct host events with equal content stay distinct, and missing event ids and " +
					"relationships remain unknown rather than reconstructed.",
			},
			{
				Capability: CapInjection,
				Class:      ClassAct,
				Mechanism:  "SessionStart additionalContext reinjection (source=compact)",
				Status:     StatusImplementedUnverified,
				Source: "documented on the host side at https://code.claude.com/docs/en/hooks " +
					"(plans/MIGRATION-EVIDENCE.md E13); the local adapter is internal/cli's session-start hook",
				Canary:  "TestCanary_SessionStartReinjection",
				Enabled: true,
				Notes: "documented on the host side; the adapter exists here, so the status is " +
					"implemented_unverified until a disposable-target canary observes a delivery. " +
					"Reinjection has no PostCompact prerequisite: it cannot wait for an optional event. " +
					"An observed sentinel documents one delivery under the tested contract, never complete " +
					"context or model compliance.",
			},
			{
				Capability: CapNewResultReplacement,
				Class:      ClassOptimize,
				Mechanism:  "PostToolUse updatedToolOutput replacement of a newly delivered result",
				Status:     StatusDocumented,
				Source:     "https://code.claude.com/docs/en/hooks (plans/MIGRATION-EVIDENCE.md E09)",
				Canary:     "TestCanary_NewResultReplacement",
				Enabled:    false,
				Fallback:   "pass-through: the host's original result is delivered unchanged",
				Notes: "off pending the SP-21 capture/retrieval/schema gate. Replacement requires exact " +
					"supported output shapes and a coexisting-hook decision, and capture must be durable " +
					"before anything is replaced. SP-21 M4-04 supplies the half of the coexistence " +
					"decision that needs no host: internal/admission bypasses any delivery already " +
					"marked by Qompack or by another hook, so admission never chains onto a " +
					"transformation somebody else made. The other half — the installed host's actual " +
					"hook order and its own output schema — stays unverified under B01, which is why " +
					"the host allowlist is empty and this capability is still disabled.",
			},
			{
				Capability: CapUsageAttribution,
				Class:      ClassRecord,
				Mechanism:  "host-reported usage and cache categories attributed to a request and its parent",
				Status:     StatusDocumented,
				Source:     "https://code.claude.com/docs/en/monitoring-usage (plans/MIGRATION-EVIDENCE.md E13)",
				Canary:     "TestCanary_UsageAttribution",
				Enabled:    false,
				Fallback:   "unknown stays unknown; cost is not claimed from an unattributed request",
				Notes: "disabled as a source of cost. Coverage is unknown, so a missing category is missing — " +
					"never a zero-filled ledger, and never an invoice claim derived from an estimate.",
			},
			{
				Capability: CapTokenEstimation,
				Class:      ClassRecord,
				Mechanism:  "local estimator over the whole serialized additional-context record",
				Status:     StatusImplementedUnverified,
				Source:     "plans/MIGRATION-EVIDENCE.md shared contract 6; Qompack.md §12",
				Canary:     "",
				Enabled:    true,
				Notes: "estimates are labelled as estimates and never reported as counted tokens. The whole " +
					"serialized output is counted, wrappers, handles and report overhead included; calibration " +
					"against a host counter is unverified.",
			},
			{
				Capability: CapCompactionRequest,
				Class:      ClassOptimize,
				Mechanism: "none: no supported plugin mechanism requests native compaction, and PreCompact " +
					"custom_instructions is INPUT to a compaction the host already decided to run",
				Status:   StatusUnsupported,
				Source:   "https://code.claude.com/docs/en/hooks (plans/MIGRATION-EVIDENCE.md E10); Qompack.md §7.3",
				Canary:   "",
				Enabled:  false,
				Fallback: "advisory cadence only: Qompack schedules its own work and never asks the host to compact",
				Notes: "the custom_instructions setter is not a summarizer-output control. Do not search a " +
					"summary for evidence an invented setter worked — a phrase found there is not evidence the " +
					"mechanism exists.",
			},
			{
				Capability: CapCompactionBlocking,
				Class:      ClassOptimize,
				Mechanism:  "PreCompact veto of a compaction the host is about to perform",
				Status:     StatusDocumented,
				Source:     "https://code.claude.com/docs/en/hooks (plans/MIGRATION-EVIDENCE.md E13); Qompack.md §12",
				Canary:     "TestCanary_CompactionBlocking",
				Enabled:    false,
				Fallback:   "automatic optimization veto off; " + manualCompactPromise,
				Notes: "a documented blocking surface does not establish a safe automatic veto: the " +
					"recovery/proactive distinction that would make one safe is missing. Whatever else changes, " +
					"a manual compact is never blocked for optimization.",
			},
			{
				Capability: CapHistoryRewriting,
				Class:      ClassOptimize,
				Mechanism:  "none: no validated plugin mechanism rewrites, cuts or evicts native history",
				Status:     StatusUnsupported,
				Source:     "plans/MIGRATION-EVIDENCE.md E12 and shared contract 7",
				Canary:     "",
				Enabled:    false,
				Fallback: "excluded: no native-history rewriting, cuts, cache-marker manipulation or deletion " +
					"of delivered results",
				Notes: "ephemeral metadata is a future representation hint only, never an eviction control.",
			},
		},
	}
}

// Get returns c's record. The second result distinguishes "this build has no opinion about c" from
// "c is off" — a distinction §12.1 requires and a bare zero value would destroy.
func (r CapabilityRegister) Get(c Capability) (CapabilityRecord, bool) {
	for _, rec := range r.Records {
		if rec.Capability == c {
			return rec, true
		}
	}
	return CapabilityRecord{}, false
}

// Validate reports whether r is a register this build may act on. The rules, in the order they are
// checked:
//
//  1. Version must be the schema version this build understands.
//  2. Every one of the eight capabilities appears exactly once, and nothing else appears.
//  3. Each record is filed under its capability's canonical class (see canonicalClass).
//  4. Status is one of the six recognised evidence statuses.
//  5. An ENABLED OPTIMIZATION must be verified_in_target, against a non-zero Target, with a named
//     Source. Nothing weaker may change or suppress what the host would otherwise do.
//  6. An ENABLED ACTING capability must be verified_in_target or implemented_unverified — our code
//     must at least exist — and must name the canary that would falsify it.
//  7. An unsupported or unknown mechanism may never be enabled.
//  8. compaction_blocking's Fallback must always promise that manual compact is never blocked.
//
// Every failure wraps ErrCapabilityRegister and names the offending capability, so the message is
// actionable without reading this function.
func (r CapabilityRegister) Validate() error {
	if r.Version != capabilityRegisterVersion {
		return fmt.Errorf("%w: version %d is not the supported version %d",
			ErrCapabilityRegister, r.Version, capabilityRegisterVersion)
	}

	seen := make(map[Capability]int, len(r.Records))
	for _, rec := range r.Records {
		want, known := canonicalClass[rec.Capability]
		if !known {
			return fmt.Errorf("%w: %q is not one of the eight separated capabilities",
				ErrCapabilityRegister, rec.Capability)
		}
		seen[rec.Capability]++
		if seen[rec.Capability] > 1 {
			return fmt.Errorf("%w: %s appears twice; one capability has one disposition",
				ErrCapabilityRegister, rec.Capability)
		}
		if rec.Class != want {
			return fmt.Errorf("%w: %s is filed under class %q but its canonical class is %q; "+
				"a capability may not change what it does by relabelling itself",
				ErrCapabilityRegister, rec.Capability, rec.Class, want)
		}
		if !evidenceStatuses[rec.Status] {
			return fmt.Errorf("%w: %s carries evidence status %q, which is not one of the six",
				ErrCapabilityRegister, rec.Capability, rec.Status)
		}
		if err := validateDisposition(rec); err != nil {
			return err
		}
	}

	for _, c := range capabilitiesInLedgerOrder {
		if seen[c] == 0 {
			return fmt.Errorf("%w: %s is missing; the register must state a disposition for every "+
				"separated capability, including the unsupported ones", ErrCapabilityRegister, c)
		}
	}
	return nil
}

// validateDisposition checks the four rules that relate one record's Enabled flag to its evidence.
// It is split out of Validate so each rule is readable on its own; the ordering is deliberate —
// the unsupported/unknown rule is checked LAST so that an enabled optimization with no evidence
// reports the missing evidence rather than the (also true) status objection.
func validateDisposition(rec CapabilityRecord) error {
	if rec.Capability == CapCompactionBlocking && !strings.Contains(rec.Fallback, manualCompactPromise) {
		return fmt.Errorf("%w: %s must always promise %q in its fallback, enabled or not",
			ErrCapabilityRegister, rec.Capability, manualCompactPromise)
	}
	if !rec.Enabled {
		return nil
	}

	switch rec.Class {
	case ClassOptimize:
		if rec.Status != StatusVerifiedInTarget {
			return fmt.Errorf("%w: %s is an enabled optimization with status %q; only %q evidence "+
				"may enable one, because a wrong optimization is not recoverable by the user",
				ErrCapabilityRegister, rec.Capability, rec.Status, StatusVerifiedInTarget)
		}
		if rec.Target.Zero() {
			return fmt.Errorf("%w: %s is enabled as %q but names no target; verified against which "+
				"provider, version, platform and date?",
				ErrCapabilityRegister, rec.Capability, StatusVerifiedInTarget)
		}
		if rec.Source == "" {
			return fmt.Errorf("%w: %s is enabled as %q but names no source artifact for that evidence",
				ErrCapabilityRegister, rec.Capability, StatusVerifiedInTarget)
		}
	case ClassAct:
		if rec.Status != StatusVerifiedInTarget && rec.Status != StatusImplementedUnverified {
			return fmt.Errorf("%w: %s is an enabled acting capability with status %q; documentation "+
				"alone is not an adapter, so it must be at least %q",
				ErrCapabilityRegister, rec.Capability, rec.Status, StatusImplementedUnverified)
		}
		if rec.Canary == "" {
			return fmt.Errorf("%w: %s is enabled but names no canary; a claim nothing can falsify "+
				"is not evidence", ErrCapabilityRegister, rec.Capability)
		}
	case ClassRecord:
		// Recording is the one thing the plugin cannot work without, and its failure mode degrades
		// Qompack's own data rather than the session. It carries no additional evidence bar beyond
		// the unsupported/unknown rule below.
	}

	if rec.Status == StatusUnsupported || rec.Status == StatusUnknown {
		return fmt.Errorf("%w: %s is enabled with status %q; there is nothing here to attempt",
			ErrCapabilityRegister, rec.Capability, rec.Status)
	}
	return nil
}
