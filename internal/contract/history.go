package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
	// ScanFrom is the size the minting session's transcript had when Token was minted (0 when it
	// did not exist yet): the host appends the SessionStart answer carrying Token after that
	// offset, so the prompt scan reads a bounded window from there as well as the transcript's tail
	// (ScanTranscriptForProbe). A history written before this field existed decodes it as 0.
	ScanFrom int64 `json:"scan_from,omitempty"`
	// Observed is true once a scan has ever found Token (or a predecessor) in a transcript tail.
	// It never resets to false: once §12.1's mechanism is proven to work, it stays proven.
	Observed bool `json:"observed"`
	// Chances counts consecutive scans that did NOT find the sentinel since it was last observed.
	// The CAdditionalContext assertion fails once this reaches 2 ("not found ⇒ fail" after two
	// chances) and is never itself incremented by that assertion's Check.
	Chances int `json:"chances"`
	// MissedBy names the prompt deliveries (their hook's delivery nonce) whose misses Chances has
	// counted for Token, so a delivery handed over again — a retry after a capture that failed, a
	// redelivery after a daemon restart — spends no second chance (RecordSentinelScanOf). It is reset
	// wherever Chances is, and holds at most maxSentinelMissedBy entries.
	MissedBy []string `json:"missed_by,omitempty"`
	// ScannedAt is when the daemon recorded the scan that last changed Observed or Chances: the find
	// that set Observed, or the miss that spent the newest counted Chance. A status or doctor read
	// that refreshes the probe's row from this record dates the row by it, never by the read
	// (D53(a)), so two reads of unchanged state agree. Zero is unknown (a history an older build
	// wrote, or a scan recorded without a time), and the reader then keeps the time of the
	// evaluation it refreshes (RefreshFromHistory).
	ScannedAt core.UnixMilli `json:"scanned_at,omitempty"`
}

// maxSentinelMissedBy caps SentinelState.MissedBy. The assertion fails at the second counted miss;
// the few entries past that keep a later redelivery of any counted prompt from counting again while
// the project sits degraded, and bound what a hand-edited history.json can carry.
const maxSentinelMissedBy = 8

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
	// StartsWithoutMarker or found a marker; while that session is still live, a start of another
	// session leaves it in place, since no terminal hook of it is due yet. It is how that Check tells "a second RunAll inside the session
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
	// PrecompactInstr is RETIRED (C1.18). A daemon before C1.18 recorded the focus instruction it
	// last emitted here, truncated to maxPrecompactInstrChars, for
	// precompact.custom_instructions_accepted to probe for. No host accepts a PreCompact
	// instruction, so nothing writes this field any more and nothing reads it; it stays so that a
	// history an older daemon wrote still loads and round-trips.
	PrecompactInstr string `json:"precompact_instr"`
	// AwaitingCompactStart is set when a PreCompact has been observed for the current session id
	// and cleared by session_start.source_compact on the FOLLOWING SessionStart, whichever way
	// that assertion resolves.
	AwaitingCompactStart bool `json:"awaiting_compact_start"`

	Sentinel SentinelState `json:"sentinel"`

	MCPInitialized bool `json:"mcp_initialized"`
	// MCPInitializedAt is when the `mcp` op recorded the handshake that set MCPInitialized
	// (RecordMCPInitialized). It dates the row a status or doctor read refreshes from it, as
	// SentinelState.ScannedAt does for the probe. Zero is unknown: a history an older build wrote,
	// or a handshake only the daemon's own seam saw.
	MCPInitializedAt core.UnixMilli `json:"mcp_initialized_at,omitempty"`

	// MCPAwaitSession is the session whose start first found no MCP handshake on record: the host
	// connects the MCP server beside a session's first start, not before it, so that start reports
	// mcp.server_registered pending. MCPAwaitTurned records that the session has since had a prompt
	// (NotePrompt). A start of another session fails the assertion only once the awaited session
	// has had a prompt and is no longer live (Env.SessionLive), so a whole session passed without a
	// handshake. Owned by checkMCPServerRegistered.
	MCPAwaitSession core.SessionID `json:"mcp_await_session,omitempty"`
	MCPAwaitTurned  bool           `json:"mcp_await_turned,omitempty"`

	// TranscriptAwaitPath is a transcript_path a start found not yet written, and
	// TranscriptAwaitSession the session it belongs to: the host creates a session's transcript with
	// its first prompt, after SessionStart:startup, so transcript.readable reported it pending.
	// TranscriptAwaitTurned records that the session has since had a prompt (NotePrompt). A start of
	// another transcript fails the assertion if the path still does not exist once its session has
	// had a prompt and is no longer live; a session that ended with no prompt had no transcript due.
	// Owned by checkTranscriptReadable.
	TranscriptAwaitPath    string         `json:"transcript_await_path,omitempty"`
	TranscriptAwaitSession core.SessionID `json:"transcript_await_session,omitempty"`
	TranscriptAwaitTurned  bool           `json:"transcript_await_turned,omitempty"`

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
// it. A nil receiver is a no-op. Since C1.18 its only production caller is applyCaps, which keeps a
// retired PrecompactInstr an older daemon wrote within its cap.
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

// NotePrompt records that sess has had a prompt, which is what makes its awaited MCP handshake and
// transcript due (MCPAwaitTurned, TranscriptAwaitTurned). It reports whether it changed anything, so
// a caller saves the history only when it did.
func (h *SessionHistory) NotePrompt(sess core.SessionID) bool {
	if h == nil || sess == "" {
		return false
	}
	changed := false
	if h.MCPAwaitSession == sess && !h.MCPAwaitTurned {
		h.MCPAwaitTurned, changed = true, true
	}
	if h.TranscriptAwaitSession == sess && h.TranscriptAwaitPath != "" && !h.TranscriptAwaitTurned {
		h.TranscriptAwaitTurned, changed = true, true
	}
	return changed
}

// RecordSentinelScan updates h.Sentinel after a transcript scan for the current sentinel token:
// found marks it Observed for good (it never resets to false); not found spends one more Chance.
// It is called by the daemon's UserPromptSubmit route (SP-05's next task) after
// ScanTranscriptTail, and directly by this package's own tests to drive the two-chances state
// machine the CAdditionalContext assertion reads — deliberately never by the assertion's Check
// itself (see SentinelState's doc comment). A nil receiver is a no-op.
func (h *SessionHistory) RecordSentinelScan(found bool) { h.RecordSentinelScanOf(found, "") }

// RecordSentinelScanOf is RecordSentinelScan for the scan one prompt delivery made, named by its
// hook's delivery nonce. A delivery is one chance, however many times the daemon handles it: a miss
// by a delivery MissedBy already names changes nothing. A delivery with no name ("") counts every
// time, as every scan did before deliveries were named. A find clears the record with the count.
// It records no time (ScannedAt reads unknown); the daemon's scan uses RecordSentinelScanAt.
func (h *SessionHistory) RecordSentinelScanOf(found bool, delivery string) {
	h.RecordSentinelScanAt(found, delivery, 0)
}

// RecordSentinelScanAt is RecordSentinelScanOf for a scan the daemon ran at at: a scan that changes
// the record (a find, or a miss that counts) also sets Sentinel.ScannedAt to at.
func (h *SessionHistory) RecordSentinelScanAt(found bool, delivery string, at core.UnixMilli) {
	if h == nil {
		return
	}
	if found {
		h.Sentinel.Observed = true
		h.Sentinel.Chances = 0
		h.Sentinel.MissedBy = nil
		h.Sentinel.ScannedAt = at
		return
	}
	if delivery != "" {
		if slices.Contains(h.Sentinel.MissedBy, delivery) {
			return
		}
		if len(h.Sentinel.MissedBy) < maxSentinelMissedBy {
			h.Sentinel.MissedBy = append(h.Sentinel.MissedBy, delivery)
		}
	}
	h.Sentinel.Chances++
	h.Sentinel.ScannedAt = at
}

// RecordMCPInitialized records the MCP handshake the `mcp` op saw at at: MCPInitialized, and
// MCPInitializedAt the first time it is set. A handshake already on record keeps its first time,
// since MCPInitialized never resets. It reports whether it changed anything, so a caller saves the
// history only when it did. A nil receiver is a no-op.
func (h *SessionHistory) RecordMCPInitialized(at core.UnixMilli) bool {
	if h == nil || h.MCPInitialized {
		return false
	}
	h.MCPInitialized, h.MCPInitializedAt = true, at
	return true
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
	if len(h.Sentinel.MissedBy) > maxSentinelMissedBy {
		h.Sentinel.MissedBy = h.Sentinel.MissedBy[:maxSentinelMissedBy]
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
// contract every caller must honour. The one thing it does guarantee is that it will not obstruct
// SaveHistory: the read goes through paths.ReadFileShared, not os.ReadFile, so the WriteAtomic
// replace SaveHistory finishes with can land while this read is in flight. An os.ReadFile handle
// grants no FILE_SHARE_DELETE, and on Windows that is enough to make the replace fail outright
// (internal/paths/shared.go) — turning a lock-free load/save pair into one that can lose a whole
// session's history to a read that happened to overlap it. ReadFileShared reports a missing file
// as os.ReadFile does, which is the ordinary first-run case this function already folds into zero.
func LoadHistory(path string) *SessionHistory {
	zero := &SessionHistory{Version: historyVersion}
	b, err := paths.ReadFileShared(path)
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

// forgetTranscriptAwait clears the awaited transcript record checkTranscriptReadable keeps.
func (h *SessionHistory) forgetTranscriptAwait() {
	h.TranscriptAwaitPath, h.TranscriptAwaitSession, h.TranscriptAwaitTurned = "", "", false
}
