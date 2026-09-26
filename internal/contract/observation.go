package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file is 00-ARCHITECTURE.md §12.1's (v1.5) per-capability observation ledger.
//
// It sits BESIDE Result, not inside it. Result's JSON tags are the on-disk shape of
// state/contract.json, which /qompack:status renders and the next SessionStart loads, and
// testdata/golden/contracts/contract/want/result_set.json freezes those bytes; adding a field there
// would be a wire-format change to a file two other components parse. So the frozen fixture keeps
// reproducing byte-for-byte and the new information lives in its own file, its own schema, and its
// own vocabulary.
//
// What the ledger adds is the distinction §12.1 now demands. The monitor reports an assertion whose
// producer is absent as OK/SevInfo — deliberately, so a wave-1 build is not degraded by a wave-4
// subsystem — and reports "I could not form an opinion" as OK too. Both read as success in a
// boolean. Here they are `unavailable` and `not_observed`, an unsupported mechanism is
// `unsupported`, and only a mechanism that actually did what it promised is `observed`.
//
// The daemon is the one writer: internal/daemon/observations.go appends every SessionStart's
// RunAll results under historyMu, right after the monitor's own history save (00-ARCHITECTURE.md
// §0.2.6 gives the SessionStart persistence call to the coordinator). Everything here is pure or
// takes an explicit path, so the daemon's decision stays in the daemon.

// observationLedgerVersion is the schema version SaveObservationLedger writes and
// LoadObservationLedger accepts. An unrecognised version loads as empty (§12.3), because a stale
// interpretation of a future schema's fields is worse than no record at all.
const observationLedgerVersion = 1

// maxObservationsPerID bounds retention PER ASSERTION rather than over the whole ledger. A global
// cap would let one chatty assertion — session_start.fires fires every session — evict the single
// recorded observation of a rare one, which is precisely the evidence worth keeping.
const maxObservationsPerID = 64

// observationPerm is the permission the ledger is written with: owner-only, like state/contract.json
// and state/history.json. It records what a host did during a user's sessions.
const observationPerm = 0o600

// Outcome is what one Result actually established. The six values are mutually exclusive and the
// distinctions between them are the reason this file exists.
type Outcome string

// The six outcomes.
const (
	// OutcomeObserved: the mechanism did what it promised, this session, under the contract the
	// assertion tests. Never a claim about completeness.
	OutcomeObserved Outcome = "observed"
	// OutcomeNotObserved: the mechanism is supported and present, and nothing was seen. Absence of
	// evidence — not evidence of absence, and not success.
	OutcomeNotObserved Outcome = "not_observed"
	// OutcomeUnavailable: the producer is absent from this build, so nothing was even attempted.
	// This is the reading §12.1's OK/SevInfo would otherwise be mistaken for.
	OutcomeUnavailable Outcome = "unavailable"
	// OutcomeUnsupported: the register says there is no mechanism here to attempt. A passing
	// Result about an unsupported mechanism is not evidence the mechanism exists.
	OutcomeUnsupported Outcome = "unsupported"
	// OutcomeUnknown: this build cannot attribute the assertion to a capability, so it will not
	// guess which one its evidence belongs to.
	OutcomeUnknown Outcome = "unknown"
	// OutcomeFailed: the mechanism was attempted, and the host did not honour the contract.
	OutcomeFailed Outcome = "failed"
)

// The closed coverage vocabulary. plans/MIGRATION-EVIDENCE.md shared contract 6 lists the coverage
// states a representation may claim; what a transcript sentinel can support is exactly one of them,
// and "complete" is deliberately unspellable here.
//
// Qompack.md §12.1: a transcript sentinel "can document an observed delivery under its tested
// contract; it cannot prove complete context or model compliance."
const (
	// CoverageDeliveryUnderTestedContract: one delivery was observed under the contract the
	// assertion tests. It says nothing about anything not tested.
	CoverageDeliveryUnderTestedContract = "delivery_under_tested_contract"
	// CoverageNone: this observation supports no coverage claim at all.
	CoverageNone = "none"
)

// noObservationSpellings is every Observed string in assertions.go that means "nothing was seen",
// as opposed to "something was seen" or "the contract was broken".
//
// It is a literal table rather than a heuristic because a heuristic over prose is exactly how a new
// spelling drifts into reading as success. TestObservedVocabulary_CoversEveryLiteralInAssertionsGo
// parses assertions.go and requires every literal Observed value to appear in this table or in
// observedSpellings, so a Check that grows a new spelling fails the build rather than silently
// classifying as `observed`.
//
// Each entry names the check it comes from:
//
//	no observation yet                        — noObservationYet: no usable History or Env (all)
//	first-session                             — checkSessionStartFires: no prior terminal hook existed
//	marker-absent-once                        — checkSessionStartFires: one absence, not yet two
//	no-precompact-pending                     — checkSessionStartSourceCompact: nothing to resolve
//	precompact-pending-for-another-session     — checkSessionStartSourceCompact: wrong session starting
//	not-yet-observed                          — checkAdditionalContextDelivered: fewer than two chances
//	timeout-unknown                           — checkPreCompactTiming: no manifest timeout recorded
//	no-samples                                — checkPreCompactTiming: no wall-time samples yet
//	no-transcript-path                        — checkTranscriptReadable
//	retired                                   — checkPreCompactCustomInstr: the mechanism is retired (C1.18)
//	unset                                     — checkPluginRootResolves: CLAUDE_PLUGIN_ROOT not set
//
// Three more are kept although no check emits them any more: checkPreCompactCustomInstr's spellings
// from before C1.18 retired it — no-instructions-emitted, no probe phrase long enough, and
// transcript unreadable, no observation yet. An observation ledger an older build wrote still
// carries them, and it must go on classifying the way it did.
var noObservationSpellings = map[string]bool{
	"no observation yet":                        true,
	"first-session":                             true,
	"marker-absent-once":                        true,
	"no-precompact-pending":                     true,
	"precompact-pending-for-another-session":    true,
	"not-yet-observed":                          true,
	"timeout-unknown":                           true,
	"no-samples":                                true,
	"retired":                                   true,
	"no-instructions-emitted":                   true,
	"no-transcript-path":                        true,
	"no probe phrase long enough":               true,
	"transcript unreadable, no observation yet": true,
	"unset": true,
}

// observedSpellings is the other half of the same vocabulary: every literal Observed string in
// assertions.go that reports a real, positive observation.
//
// ClassifyResult does not read it — anything not in noObservationSpellings and not otherwise
// disqualified is `observed`, so a positive spelling needs no entry to work. It exists so the two
// tables TOGETHER account for every literal the file emits, which is what makes the drift guard
// able to notice a new spelling at all. Without it, a new "no observation" spelling would be
// indistinguishable from a new success spelling and the guard would have nothing to fail on.
var observedSpellings = map[string]bool{
	"marker-found":      true, // checkSessionStartFires
	"sentinel-observed": true, // checkAdditionalContextDelivered
	"instruction phrase found in transcript tail": true, // checkPreCompactCustomInstr before C1.18 (unsupported mechanism)
	"payload shape valid":                         true, // checkHookPayloadShape
	"initialize-received":                         true, // checkMCPServerRegistered
	"transcript readable":                         true, // checkTranscriptReadable
	"resolved":                                    true, // checkPluginRootResolves
	// The failure spellings. They are classified by r.OK long before the tables are consulted, but
	// they are literals in the same file, so the guard needs them accounted for here.
	"no marker from a prior terminal hook across two consecutive sessions": true,
	"sentinel not found after two chances":                                 true,
	"instruction phrase not found in transcript tail":                      true, // before C1.18
	"missing hook_event_name":                                              true,
	"missing session_id":                                                   true,
	"missing both cwd and transcript_path":                                 true,
	"initialize-not-received":                                              true,
	"transcript_path does not exist":                                       true,
	"transcript_path has no readable content":                              true,
	"last transcript line is not valid JSON":                               true,
	"CLAUDE_PLUGIN_ROOT set but no plugin binary found beneath it":         true,
}

// assertionCapability maps each of the nine §5.19 assertions to the capability its evidence belongs
// to. Seven of them observe the host; one probes injection; one probes a mechanism that is not
// supported here at all.
//
// precompact.custom_instructions_accepted maps to compaction_request deliberately. Qompack.md v1.5
// §7.3 and 00-ARCHITECTURE.md §12.1 are explicit: custom_instructions is PreCompact INPUT, not a
// summarizer-output setter, and one must "not search a summary for evidence an invented setter
// worked". Mapping it here is what makes ClassifyResult report `unsupported` for that assertion
// however convincing its Result looks — the register, not a special case, does the work.
var assertionCapability = map[ID]Capability{
	CSessionStartFires:         CapObservation,
	CSessionStartSourceCompact: CapObservation,
	CHookPayloadShape:          CapObservation,
	CTranscriptReadable:        CapObservation,
	CPluginRootResolves:        CapObservation,
	CPreCompactTiming:          CapObservation,
	CMCPRegistered:             CapObservation,
	CAdditionalContext:         CapInjection,
	CPreCompactCustomInstr:     CapCompactionRequest,
}

// CapabilityOf returns the capability id's evidence belongs to, or "" for an assertion this build
// does not know. An empty return is not a default: it is the reason ClassifyResult reports
// OutcomeUnknown rather than attributing unrecognised evidence to a plausible capability.
func CapabilityOf(id ID) Capability { return assertionCapability[id] }

// ClassifyResult reports what r actually established, given reg's view of which mechanisms are
// supported. It is pure: no clock, no filesystem, no host.
//
// The order of the tests is the contract, and each step is checked before the ones that could
// otherwise mask it:
//
//  1. An assertion with no capability is `unknown`. This build will not attribute evidence it
//     cannot place.
//  2. not-yet-implemented is `unavailable` — the producer is absent, so nothing was attempted.
//     §12.1 reports this as OK, and that OK must never read as a working capability.
//  3. A mechanism the register calls unsupported is `unsupported`, whatever r says. Checked before
//     r.OK precisely so a passing Result about an invented setter cannot be read as evidence.
//  4. !r.OK is `failed`: the mechanism was attempted and the host did not honour the contract.
//  5. A "nothing was seen" spelling is `not_observed`.
//  6. Anything else is `observed`.
func ClassifyResult(r Result, reg CapabilityRegister) Outcome {
	c := CapabilityOf(r.ID)
	if c == "" {
		return OutcomeUnknown
	}
	if r.Observed == notYetImplementedObserved {
		return OutcomeUnavailable
	}
	if rec, ok := reg.Get(c); ok && rec.Status == StatusUnsupported {
		return OutcomeUnsupported
	}
	if !r.OK {
		return OutcomeFailed
	}
	if noObservationSpellings[r.Observed] {
		return OutcomeNotObserved
	}
	return OutcomeObserved
}

// Observation is one Result read as evidence about one capability, carrying everything §12.1
// requires to travel with it: which host, which scope, when, what was seen, and how much that
// supports.
type Observation struct {
	ID         ID         `json:"id"`
	Capability Capability `json:"capability"`
	Outcome    Outcome    `json:"outcome"`
	// Mechanism is the register's own description of what would have been attempted, copied here
	// so a retained observation stays readable after the register moves on.
	Mechanism string `json:"mechanism,omitempty"`
	Target    Target `json:"target"`
	// Scope is what this observation is about — a session id, a run id — supplied by the caller.
	Scope string `json:"scope,omitempty"`
	// TS is the Result's own timestamp, not the time the ledger was written.
	TS core.UnixMilli `json:"ts"`
	// Observed is the assertion's own Observed string, kept verbatim so the classification can be
	// re-derived and disputed rather than merely trusted.
	Observed string `json:"observed,omitempty"`
	// Coverage is one of the two closed vocabulary values above. It is never "complete".
	Coverage string `json:"coverage"`
	// LocalAttempt correlates this observation with a locally identified compaction attempt
	// (shared contract 5: local attempts are locally identified and correlated conservatively,
	// never given fabricated host ids). Nothing fills it yet — the daemon seam that knows attempt
	// ids is M0-02's wiring — and an empty value means "not correlated", never "no attempt".
	LocalAttempt string `json:"local_attempt,omitempty"`
	// Artifact points at a canary record or other retained evidence, when one exists.
	Artifact string `json:"artifact,omitempty"`
}

// coverageOf returns the coverage an observation of capability c with outcome o may claim.
//
// Only an OBSERVED INJECTION supports a coverage claim, and only the narrow one: a sentinel found
// in the transcript tail documents that one delivery arrived under the contract the assertion
// tests. Everything else — including a successful observation of a hook firing — supports nothing,
// because a hook firing is not a statement about what reached the model.
func coverageOf(c Capability, o Outcome) string {
	if c == CapInjection && o == OutcomeObserved {
		return CoverageDeliveryUnderTestedContract
	}
	return CoverageNone
}

// ObservationsOf converts one RunAll's results into observations attributed to target and scope.
// The order is preserved and each Result keeps its own timestamp, so the ledger reads as a record
// of one run rather than of the moment it was written.
func ObservationsOf(results []Result, reg CapabilityRegister, target Target, scope string) []Observation {
	out := make([]Observation, 0, len(results))
	for _, r := range results {
		c := CapabilityOf(r.ID)
		o := ClassifyResult(r, reg)
		var mechanism string
		if rec, ok := reg.Get(c); ok {
			mechanism = rec.Mechanism
		}
		out = append(out, Observation{
			ID:         r.ID,
			Capability: c,
			Outcome:    o,
			Mechanism:  mechanism,
			Target:     target,
			Scope:      scope,
			TS:         r.TS,
			Observed:   r.Observed,
			Coverage:   coverageOf(c, o),
		})
	}
	return out
}

// ObservationLedger is the persisted record: a schema version, the host it was gathered against,
// and the retained observations in arrival order, newest last.
type ObservationLedger struct {
	Version      int           `json:"version"`
	Target       Target        `json:"target"`
	Observations []Observation `json:"observations"`
}

// Append adds obs to the ledger and re-applies the per-ID cap. It is the only supported way to
// grow a ledger: appending to the field directly skips the cap.
func (l *ObservationLedger) Append(obs ...Observation) {
	if l == nil || len(obs) == 0 {
		return
	}
	l.Observations = append(l.Observations, obs...)
	l.applyCap()
}

// applyCap keeps at most maxObservationsPerID observations per assertion id, dropping the OLDEST
// of each and preserving the overall arrival order of what remains.
//
// It walks backwards precisely because "keep the newest" and "preserve arrival order" pull in
// opposite directions: the reverse walk decides what survives, and the forward rebuild puts the
// survivors back in the order they arrived.
func (l *ObservationLedger) applyCap() {
	if l == nil {
		return
	}
	counts := make(map[ID]int)
	keep := make([]bool, len(l.Observations))
	over := false
	for i := len(l.Observations) - 1; i >= 0; i-- {
		id := l.Observations[i].ID
		counts[id]++
		if counts[id] <= maxObservationsPerID {
			keep[i] = true
		} else {
			over = true
		}
	}
	if !over {
		return
	}
	kept := make([]Observation, 0, len(l.Observations))
	for i, o := range l.Observations {
		if keep[i] {
			kept = append(kept, o)
		}
	}
	l.Observations = kept
}

// ObservationLedgerPath returns <projectRoot>/.qompack/state/observations.json.
//
// It is deliberately neither state/contract.json (the Monitor's own mode/reason/results) nor
// state/history.json (the cross-session assertion memory): three distinct schemas sharing one path
// would corrupt each other the first time two of them were written, which is the same reason
// HistoryPath is its own file.
func ObservationLedgerPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).State, "observations.json")
}

// LoadObservationLedger reads path and returns the decoded ledger. Any error — a missing file (the
// ordinary first-run case), a permission failure, corrupt JSON, or a version this build does not
// recognise — returns a fresh ledger rather than propagating: §12.3's "everything else fails toward
// do nothing" applies here exactly as it does to the monitor's other state. It never returns nil.
//
// The per-ID cap is re-applied to whatever was read, so a hand-edited or future-schema file cannot
// smuggle an unbounded ledger past this call — the same rule LoadHistory follows.
//
// The read goes through paths.ReadFileShared rather than os.ReadFile so it cannot obstruct a
// concurrent SaveObservationLedger: on Windows an os.ReadFile handle grants no FILE_SHARE_DELETE,
// which is enough to make WriteAtomic's finishing replace fail outright.
func LoadObservationLedger(path string) *ObservationLedger {
	fresh := &ObservationLedger{Version: observationLedgerVersion}
	b, err := paths.ReadFileShared(path)
	if err != nil {
		return fresh
	}
	var l ObservationLedger
	if err := json.Unmarshal(b, &l); err != nil {
		return fresh
	}
	switch l.Version {
	case 0:
		l.Version = observationLedgerVersion
	case observationLedgerVersion:
		// recognised.
	default:
		return fresh
	}
	l.applyCap()
	return &l
}

// SaveObservationLedger writes l to path through paths.WriteAtomic, creating the containing
// directory if needed, owner-only. A nil l is written as a fresh, empty ledger rather than
// reported as an error — a caller with nothing to record should not have to special-case that.
//
// internal/daemon's recordCapabilityObservations is the production caller; see this file's header.
func SaveObservationLedger(path string, l *ObservationLedger) error {
	if l == nil {
		l = &ObservationLedger{Version: observationLedgerVersion}
	}
	if l.Version == 0 {
		l.Version = observationLedgerVersion
	}
	l.applyCap()
	b, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("contract: marshalling %s: %w", path, err)
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700); err != nil {
		return fmt.Errorf("contract: mkdir for %s: %w", path, err)
	}
	if err := paths.WriteAtomic(path, b, observationPerm); err != nil {
		return fmt.Errorf("contract: writing %s: %w", path, err)
	}
	return nil
}
