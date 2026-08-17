package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// historyVersion is the current on-disk schema version of state/history.json.
const historyVersion = 1

// historyPerm is the permission history.json is written with: owner-only, like every other file
// under .qompack/.
const historyPerm = 0o600

// maxPrecompactWallSamples caps History.PrecompactWallMs at its newest samples, per task-4-spec.md
// ("the 64 newest") — enough for a meaningful p99 without the file growing without bound across a
// long-lived project.
const maxPrecompactWallSamples = 64

// maxPrecompactInstrChars caps the persisted custom_instructions probe phrase.
const maxPrecompactInstrChars = 256

// maxLastResults caps History.Last at one entry per §5.19 assertion.
const maxLastResults = 9

// SentinelState is the cross-session record of the hook.additional_context_delivered probe
// (§12.1): SessionStart mints and emits a token, and the daemon's UserPromptSubmit route scans the
// transcript tail for it and calls RecordSentinelScan with what it found. The CAdditionalContext
// assertion only ever READS this — Chances tracking belongs to the scan, not the assertion, so
// that "two chances" means two actual scans, not two evaluations of the same scan.
type SentinelState struct {
	// Token is the most recently minted token, kept so a caller can re-render or re-scan for it
	// without re-deriving it from Session/MintedAt.
	Token string `json:"token,omitempty"`
	// Session is the id of the session the current Token was minted for.
	Session core.SessionID `json:"session,omitempty"`
	// MintedAt is when Token was minted.
	MintedAt core.UnixMilli `json:"minted_at,omitempty"`
	// Observed is true once a scan has ever found Token (or a predecessor) in a transcript tail.
	// It never resets to false: once §12.1's mechanism is proven to work, it stays proven.
	Observed bool `json:"observed"`
	// Chances counts consecutive scans that did NOT find the sentinel since it was last observed.
	// The CAdditionalContext assertion fails once this reaches 2 ("not found ⇒ fail" after two
	// chances) and is never itself incremented by that assertion's Check.
	Chances int `json:"chances"`
}

// SessionHistory is the first real implementation of History (00-ARCHITECTURE.md §5.19): the
// persistent, cross-session record every §12.1 assertion phrased "across two sessions" or
// "evaluated on the FOLLOWING start" needs. It is loaded once at SessionStart (LoadHistory),
// mutated in place by the assertions that run against it, and saved back by the daemon after
// RunAll (the next task's job — this package only mutates the in-memory record).
//
// Ownership contract — NOT safe for concurrent use. SessionHistory carries no lock of its own, by
// design (this task's controller ruling): every Check in assertions.go mutates its exported fields
// directly, with no synchronization, and Seen is a bare Go map. A concurrent Record/Saw pair, or two
// goroutines racing to increment StartsWithoutMarker or call RecordSentinelScan, is not a benign
// data race — a concurrent map read and write on Seen is a hard, unrecoverable
// "fatal error: concurrent map read and map write" that crashes the process, not something the race
// detector merely flags. A single *SessionHistory value must be owned by exactly one goroutine at a
// time for its entire lifetime — load, every assertion Check that touches it, every daemon route
// that calls RecordSentinelScan or sets MCPInitialized/PrecompactTimeoutMs/etc., and the eventual
// SaveHistory. The daemon (this task's next one) is expected to enforce this with a single mutex
// around the session's History value; LoadHistory/SaveHistory themselves do no locking and assume
// the caller already has exclusive access.
//
// Wire shape is FROZEN, separately from the concurrency contract above. The testdata/golden/
// contracts/contract/want/history_degraded.json fixture is declared "frozen" in that package's
// MANIFEST.json, and TestHistoryDegradedGolden_Decodes compares json.MarshalIndent(this type)
// byte-for-byte against it. Adding, removing, or renaming any non-omitempty field on
// SessionHistory or SentinelState breaks that fixture under Rule W-2 (a verification failure, not a
// fixture bug) and needs controller sign-off plus a fixture re-freeze — it is not something a later
// task may adjust on its own. A new field must carry `omitempty` and be absent or zero in the
// fixture's own scenario to avoid touching it at all.
type SessionHistory struct {
	Version int `json:"version"`

	// SessionCount is the Go field backing the History interface's Sessions() method. It is
	// spelled differently from its "sessions" JSON tag only to avoid colliding with the method of
	// the same name on this type.
	SessionCount int `json:"sessions"`

	// LastSessionID is OWNED by the session_start.fires Check (checkSessionStartFires,
	// assertions.go) and by nothing else. It is the session id that last advanced
	// StartsWithoutMarker, and it is how that Check tells "a second RunAll inside the session
	// already counted" from "a genuinely new session, count it too" (Important I1's fix). The
	// daemon (this task's next one) MUST NEVER write this field itself — in particular, never
	// pre-set it to the incoming session's id at SessionStart before RunAll runs. Doing so would
	// make checkSessionStartFires see "already counted this session" on the very first RunAll of
	// every session, permanently suppressing the increment and disabling the SevCritical assertion
	// for good. Only checkSessionStartFires may assign to this field.
	LastSessionID core.SessionID `json:"last_session_id"`
	LastMarkerTS  core.UnixMilli `json:"last_marker_ts"`

	// StartsWithoutMarker counts consecutive SessionStarts that found no marker left by a prior
	// terminal hook. session_start.fires fails once this reaches 2 ("absence across two sessions").
	StartsWithoutMarker int `json:"starts_without_marker"`

	LastPrecompactTS      core.UnixMilli `json:"last_precompact_ts"`
	LastPrecompactSession core.SessionID `json:"last_precompact_session"`

	// PrecompactWallMs holds the 64 newest PreCompact wall-time samples, newest last, that
	// precompact.has_time_to_write computes a p99 over.
	PrecompactWallMs    []int64 `json:"precompact_wall_ms"`
	PrecompactTimeoutMs int64   `json:"precompact_timeout_ms"`
	// PrecompactInstr is the most recently emitted custom_instructions text, truncated to
	// maxPrecompactInstrChars, that precompact.custom_instructions_accepted probes for.
	PrecompactInstr string `json:"precompact_instr"`
	// AwaitingCompactStart is set when a PreCompact has been observed for the current session id
	// and cleared by session_start.source_compact on the FOLLOWING SessionStart, whichever way
	// that assertion resolves.
	AwaitingCompactStart bool `json:"awaiting_compact_start"`

	Sentinel SentinelState `json:"sentinel"`

	MCPInitialized bool `json:"mcp_initialized"`

	// CleanRuns mirrors the monitor's own clean-run streak into the cross-session record so a
	// caller inspecting History alone (e.g. self-test synthesizing an Env, per task-4-spec.md's
	// nil-tolerance note) can see it without a live Monitor.
	CleanRuns      int            `json:"clean_runs"`
	Mode           string         `json:"mode"`
	DegradedReason string         `json:"degraded_reason,omitempty"`
	DegradedSince  core.UnixMilli `json:"degraded_since,omitempty"`

	// Last holds the most recent RunAll's results, capped at maxLastResults (one per §5.19
	// assertion).
	Last []Result `json:"last,omitempty"`

	// Seen backs the History interface: Saw/LastSeen read it, Record writes it.
	Seen map[ID]core.UnixMilli `json:"seen,omitempty"`
}

// var assertion: *SessionHistory implements History.
var _ History = (*SessionHistory)(nil)

func (h *SessionHistory) Saw(id ID) bool {
	if h == nil || h.Seen == nil {
		return false
	}
	_, ok := h.Seen[id]
	return ok
}

func (h *SessionHistory) LastSeen(id ID) (core.UnixMilli, bool) {
	if h == nil || h.Seen == nil {
		return 0, false
	}
	ts, ok := h.Seen[id]
	return ts, ok
}

func (h *SessionHistory) Record(id ID, at core.UnixMilli) {
	if h == nil {
		return
	}
	if h.Seen == nil {
		h.Seen = make(map[ID]core.UnixMilli)
	}
	if prev, ok := h.Seen[id]; !ok || at > prev {
		h.Seen[id] = at
	}
}

func (h *SessionHistory) Sessions() int {
	if h == nil {
		return 0
	}
	return h.SessionCount
}

// AddPrecompactWallSample appends ms to PrecompactWallMs, keeping only the maxPrecompactWallSamples
// newest. A nil receiver is a no-op, matching every other SessionHistory method's typed-nil
// tolerance (historyOf in assertions.go hands out only a non-nil *SessionHistory, but a caller
// outside this package's Checks may not).
func (h *SessionHistory) AddPrecompactWallSample(ms int64) {
	if h == nil {
		return
	}
	h.PrecompactWallMs = append(h.PrecompactWallMs, ms)
	if len(h.PrecompactWallMs) > maxPrecompactWallSamples {
		h.PrecompactWallMs = h.PrecompactWallMs[len(h.PrecompactWallMs)-maxPrecompactWallSamples:]
	}
}

// SetPrecompactInstr truncates instr to maxPrecompactInstrChars runes (not bytes — task-4-spec.md
// says "≤256 chars", and a byte-boundary cut can split a multi-byte UTF-8 rune, which json.Marshal
// would then replace with U+FFFD and break the SaveHistory/LoadHistory round trip) before storing
// it. A nil receiver is a no-op.
func (h *SessionHistory) SetPrecompactInstr(instr string) {
	if h == nil {
		return
	}
	runes := []rune(instr)
	if len(runes) > maxPrecompactInstrChars {
		runes = runes[:maxPrecompactInstrChars]
	}
	h.PrecompactInstr = string(runes)
}

// RecordLast replaces Last with a copy of results, capped at maxLastResults (the newest entries
// kept when results itself somehow exceeds that many). A nil receiver is a no-op.
func (h *SessionHistory) RecordLast(results []Result) {
	if h == nil {
		return
	}
	last := append([]Result(nil), results...)
	if len(last) > maxLastResults {
		last = last[len(last)-maxLastResults:]
	}
	h.Last = last
}

// RecordSentinelScan updates h.Sentinel after a transcript scan for the current sentinel token:
// found marks it Observed for good (it never resets to false); not found spends one more Chance.
// It is called by the daemon's UserPromptSubmit route (SP-05's next task) after
// ScanTranscriptTail, and directly by this package's own tests to drive the two-chances state
// machine the CAdditionalContext assertion reads — deliberately never by the assertion's Check
// itself (see SentinelState's doc comment). A nil receiver is a no-op.
func (h *SessionHistory) RecordSentinelScan(found bool) {
	if h == nil {
		return
	}
	if found {
		h.Sentinel.Observed = true
		h.Sentinel.Chances = 0
		return
	}
	h.Sentinel.Chances++
}

// applyCaps re-clamps every bound-checked field to its cap after an unmarshal, so a hand-edited,
// corrupted-but-parseable, or future-schema history.json cannot smuggle an unbounded field (10,000
// wall samples, a 40-entry Last, a multi-KB PrecompactInstr) past LoadHistory for the rest of the
// process's life — including the very p99 window precompact.has_time_to_write computes over.
func (h *SessionHistory) applyCaps() {
	if h == nil {
		return
	}
	if len(h.PrecompactWallMs) > maxPrecompactWallSamples {
		h.PrecompactWallMs = h.PrecompactWallMs[len(h.PrecompactWallMs)-maxPrecompactWallSamples:]
	}
	if h.PrecompactInstr != "" {
		h.SetPrecompactInstr(h.PrecompactInstr) // reuses the rune-safe cap
	}
	if len(h.Last) > maxLastResults {
		h.Last = h.Last[len(h.Last)-maxLastResults:]
	}
}

// HistoryPath returns <projectRoot>/.qompack/state/history.json — deliberately NOT
// state/contract.json, which remains the Monitor's own persisted mode/reason/results: two distinct
// schemas sharing one path would corrupt each other the first time both were written.
func HistoryPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).State, "history.json")
}

// LoadHistory reads path and returns the decoded SessionHistory. Any error — a missing file (the
// ordinary first-run case), a permission failure, or corrupt JSON — returns a zero SessionHistory
// with Version 1 rather than propagating: §12.3's "everything else fails toward do nothing" applies
// to the monitor's cross-session memory exactly as it does to state/contract.json itself. An
// unrecognised Version (present and not historyVersion) is treated the same way: a future schema
// this build does not understand must not be trusted with a stale interpretation of its fields.
// Every bound-checked field is re-clamped to its cap before returning (applyCaps), so a hand-edited
// or future-schema file cannot smuggle an unbounded field past this call.
//
// LoadHistory does no locking of its own — see SessionHistory's doc comment for the ownership
// contract every caller must honour.
func LoadHistory(path string) *SessionHistory {
	zero := &SessionHistory{Version: historyVersion}
	b, err := os.ReadFile(paths.Long(path))
	if err != nil {
		return zero
	}
	var h SessionHistory
	if err := json.Unmarshal(b, &h); err != nil {
		return zero
	}
	switch h.Version {
	case 0:
		h.Version = historyVersion
	case historyVersion:
		// recognised.
	default:
		return zero
	}
	h.applyCaps()
	return &h
}

// SaveHistory writes h to path through paths.WriteAtomic, creating the containing directory if
// needed. A nil h is treated as a fresh, empty history rather than an error. SaveHistory does no
// locking of its own — see SessionHistory's doc comment for the ownership contract every caller
// must honour.
func SaveHistory(path string, h *SessionHistory) error {
	if h == nil {
		h = &SessionHistory{Version: historyVersion}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("contract: marshalling %s: %w", path, err)
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700); err != nil {
		return fmt.Errorf("contract: mkdir for %s: %w", path, err)
	}
	if err := paths.WriteAtomic(path, b, historyPerm); err != nil {
		return fmt.Errorf("contract: writing %s: %w", path, err)
	}
	return nil
}
