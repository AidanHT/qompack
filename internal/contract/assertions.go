package contract

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// now reads e.Clock, substituting the system clock when it is nil. RunAll always fills in
// e.Clock before invoking any Check (monitor.go), but an assertion's Check may also be called
// directly — by a test, or by self-test synthesizing an Env from persisted History
// (task-4-spec.md's nil-tolerance note) — so every Check in this file reads the clock through here
// rather than assuming RunAll's guarantee holds.
func now(e Env) core.UnixMilli {
	if e.Clock == nil {
		return core.NowMilli(core.SystemClock())
	}
	return core.NowMilli(e.Clock)
}

// gated is the §12.1 not-yet-implemented mechanism, implemented exactly once: an assertion whose
// producer is absent from the build reports OK/SevInfo/not-yet-implemented and check never runs at
// all — not "runs and is ignored", never runs — so a wave-1 build cannot degrade itself into
// passivity over a subsystem a later wave has not shipped yet. Every one of the nine standard
// assertions is constructed through this wrapper.
func gated(id ID, sev Severity, desc string, check func(context.Context, Env) Result) Assertion {
	return Assertion{
		ID: id, Severity: sev, Description: desc,
		Check: func(ctx context.Context, e Env) Result {
			if !HasProducer(id) {
				return Result{
					ID: id, OK: true, Severity: SevInfo,
					Expected: desc, Observed: notYetImplementedObserved, TS: now(e),
				}
			}
			r := check(ctx, e)
			r.ID = id
			if r.Severity == 0 && !r.OK {
				r.Severity = sev
			}
			return r
		},
	}
}

// noObservationYet is what every real Check reports when it cannot form an opinion at all: no
// SessionHistory to read (e.Env.History is nil or some other History implementation — see
// task-4-spec.md's Env.History type-assertion note), or an Env too half-filled to observe anything
// from. It is OK, never a failure: absence of evidence is not evidence of a broken contract.
func noObservationYet(id ID, desc string, e Env) Result {
	return Result{ID: id, OK: true, Expected: desc, Observed: "no observation yet", TS: now(e)}
}

// historyOf type-asserts e.History to *SessionHistory, the only History implementation this
// package ships. A caller supplying a different implementation (or none) is legitimate — Env's own
// doc comment says every field is optional — so a failed assertion is reported by the caller as
// noObservationYet, never a panic.
func historyOf(e Env) (*SessionHistory, bool) {
	h, ok := e.History.(*SessionHistory)
	return h, ok && h != nil
}

// checkSessionStartFires is CSessionStartFires's real observation (task-4-spec.md's table):
// run/marker.json exists and names a session different from the current one -> OK. Absent (or
// naming THIS session when the start is not its own restart, described below, which is the same
// absence of proof) -> StartsWithoutMarker++, failing only once that reaches 2 ("absence across two
// sessions"). The very first session a project has ever seen has no prior terminal hook to have
// left a marker, so it reports OK regardless. A session's
// own restart is a start whose marker names it and which is either a start of the session the
// history last saw, a compaction or --resume (sessionRestartSource) of any session, or a start the
// host fired before that marker was written (ownMarkerAfterStart): with two sessions open in one
// project, the one that started first restarts while LastSessionID names the other, and a startup
// replayed from a spool after its own session's PreCompact finds the marker that PreCompact wrote.
// A restart counts nothing and moves neither field; it reads same-session-restart (holding) when no
// absence is counted, and otherwise the counted absence's own reading.
//
// An absence is counted only once the session the history last saw start (LastSessionID) is no
// longer live (Env.SessionLive): a session still running has had no terminal hook yet, so its
// marker cannot be due, and windows opened together on a project that has never had a terminal hook
// would otherwise fail the assertion at the third start (audit 2, #8). Such a start counts nothing,
// leaves LastSessionID naming the running session, whose terminal hook the next start still awaits,
// and reads prior-session-live (nothing to judge) when no absence is counted. A session the caller
// does not know reads as not live (a restarted daemon forgets its sessions), so an absence after a
// restart still counts, as it always did.
//
// The counter is bumped at most once per SESSION, keyed off History.LastSessionID: §12.1 says
// "absence across two SESSIONS", not "across two RunAll calls", and a second RunAll inside one
// session — a self-test synthesizing an Env (which the spec's own nil-tolerance paragraph
// anticipates), a daemon retry, a future status command — must not silently double-count toward the
// >= 2 degrade threshold on a single real miss.
func checkSessionStartFires(ctx context.Context, e Env) Result {
	const desc = "SessionStart hook fires"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CSessionStartFires, desc, e)
	}
	if e.ProjectRoot == "" {
		// Nil tolerance (task-4-spec.md): a Check handed a field the caller did not supply treats
		// it as "no observation yet", never as a failure, and — critically — mutates nothing. An
		// empty ProjectRoot would otherwise resolve to a relative, almost-always-missing
		// run/marker.json and silently count as an absence.
		return noObservationYet(CSessionStartFires, desc, e)
	}
	if h.Sessions() == 0 {
		h.LastSessionID = e.Event.SessionID
		return Result{OK: true, Expected: desc, Observed: "first-session", TS: now(e)}
	}
	rec, err := readMarker(e.ProjectRoot)
	if err == nil && rec.Session != "" && rec.Session != e.Event.SessionID {
		h.StartsWithoutMarker = 0
		h.LastSessionID = e.Event.SessionID
		return Result{OK: true, Expected: desc, Observed: "marker-found", TS: now(e)}
	}
	ownMarker := err == nil && rec.Session != "" && rec.Session == e.Event.SessionID
	restart := ownMarker && (h.LastSessionID == e.Event.SessionID ||
		sessionRestartSource(e.Event.Source) || ownMarkerAfterStart(rec, e))
	priorLive := false
	if !restart && h.LastSessionID != e.Event.SessionID {
		if sessionLive(e, h.LastSessionID) {
			priorLive = true
		} else {
			h.StartsWithoutMarker++
			h.LastSessionID = e.Event.SessionID
		}
	}
	if h.StartsWithoutMarker >= 2 {
		return Result{
			OK: false, Expected: desc,
			Observed: "no marker from a prior terminal hook across two consecutive sessions",
			TS:       now(e),
		}
	}
	if h.StartsWithoutMarker == 0 && restart {
		// A session's own restart, whose marker that session's own terminal hook wrote: its
		// compaction (PreCompact, then SessionStart source=compact) or a --resume that kept the id,
		// whether or not another session started in between. No absence is counted and this marker
		// proves the session's hooks still fire, so nothing is pending (F-C48-1). A count of 1
		// stays marker-absent-once below: that absence is still the one the next new session's
		// start decides.
		return Result{OK: true, Expected: desc, Observed: "same-session-restart", TS: now(e)}
	}
	if h.StartsWithoutMarker == 0 && priorLive {
		// The session the history last saw start is still running, so no terminal hook of it is
		// due and this start observed nothing either way (audit 2, #8).
		return Result{OK: true, Expected: desc, Observed: "prior-session-live", TS: now(e)}
	}
	return Result{OK: true, Expected: desc, Observed: "marker-absent-once", TS: now(e)}
}

// ownMarkerAfterStart reports whether rec, a marker naming the starting session itself, was written
// at or after the host fired this start (Env.StartTS): by that session's own terminal hook, which
// ran after its start. That happens to a start replayed from a spool after its session's PreCompact
// or SessionEnd was handled live (audit 2, #10). Its marker is no absence: it proves a terminal hook
// of this project fired, and it overwrote the marker the start would have read when the host fired
// it. An unknown start time decides nothing, so such a start counts as it always did.
func ownMarkerAfterStart(rec markerRecord, e Env) bool {
	return e.StartTS > 0 && rec.TS >= e.StartTS
}

// SessionStart sources (hookio.Event.Source) that keep the session id the host already ran.
const (
	sessionSourceCompact = "compact"
	sessionSourceResume  = "resume"
)

// sessionRestartSource reports whether a SessionStart source restarts a session the host already
// ran: a compaction or a --resume keeps the session id. A startup or clear carries an id the host
// has just minted, so a marker already naming it proves no prior terminal hook fired and stays an
// absence unless the history last saw that very session, or the marker was written after the host
// fired the start (ownMarkerAfterStart).
func sessionRestartSource(source string) bool {
	return source == sessionSourceCompact || source == sessionSourceResume
}

// checkSessionStartSourceCompact is CSessionStartSourceCompact's real observation: when a
// PreCompact was observed for a session (History.AwaitingCompactStart), the FOLLOWING SessionStart
// OF THAT SAME SESSION must arrive with source=="compact". The flag is cleared either way, because
// it is only ever evaluated once, on the next start.
//
// A compaction the user cancels, or one that fails, after its PreCompact hook ran starts no
// session at all, and the session goes on: it prompts, or it ends, and is later resumed. So a start
// that is no compaction fails the assertion only when nothing of its session came between: once the
// session prompted or ended after its PreCompact (History.CompactStartLapsed, recorded by
// SessionHistory.NoteCompactLapse), its next start reads precompact-not-completed, with nothing to
// judge, because a cancelled compaction and a compact start the host never sent look the same from
// here, and neither can be shown to be the host's (audit 2, #9).
//
// The session-id half is not decoration. 00-ARCHITECTURE §12.1 states the observable as "the next
// SessionStart carries source=compact within the same session id", and a pending flag alone cannot
// express it: a PreCompact in session X followed by a SessionStart of an unrelated session Y —
// a second project window, or a fresh session started while X sat compacting — would resolve X's
// pending assertion against Y's source and fail a contract nothing violated. The daemon has
// written LastPrecompactSession since SP-05 (internal/daemon/handlers.go); until this check read
// it, nothing did.
func checkSessionStartSourceCompact(ctx context.Context, e Env) Result {
	const desc = "SessionStart arrives with source=compact after PreCompact"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CSessionStartSourceCompact, desc, e)
	}
	if !h.AwaitingCompactStart {
		return Result{OK: true, Expected: desc, Observed: "no-precompact-pending", TS: now(e)}
	}
	if h.LastPrecompactSession != "" && h.LastPrecompactSession != e.Event.SessionID {
		// A different session is starting. The pending observation belongs to the session that
		// compacted and this start says nothing about it, so the flag is dropped rather than
		// resolved: keeping it would let the NEXT start of any session inherit a stale obligation,
		// which is the same wrong-session failure one step later.
		h.AwaitingCompactStart, h.CompactStartLapsed = false, false
		return Result{
			OK: true, Expected: desc,
			Observed: "precompact-pending-for-another-session",
			TS:       now(e),
		}
	}
	lapsed := h.CompactStartLapsed
	h.AwaitingCompactStart, h.CompactStartLapsed = false, false
	if e.Event.Source == "compact" {
		return Result{OK: true, Expected: "compact", Observed: e.Event.Source, TS: now(e)}
	}
	if lapsed {
		return Result{OK: true, Expected: desc, Observed: "precompact-not-completed", TS: now(e)}
	}
	return Result{OK: false, Expected: "compact", Observed: e.Event.Source, TS: now(e)}
}

// checkAdditionalContextDelivered is CAdditionalContext's real observation: it only ever READS
// History.Sentinel. Chances tracking belongs to the daemon's UserPromptSubmit scan
// (SessionHistory.RecordSentinelScan), never to this Check — see SentinelState's doc comment for
// why.
func checkAdditionalContextDelivered(ctx context.Context, e Env) Result {
	const desc = "additionalContext reaches the transcript"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CAdditionalContext, desc, e)
	}
	if h.Sentinel.Observed {
		return Result{OK: true, Expected: desc, Observed: "sentinel-observed", TS: now(e)}
	}
	if h.Sentinel.Chances < 2 {
		return Result{OK: true, Expected: desc, Observed: "not-yet-observed", TS: now(e)}
	}
	return Result{OK: false, Expected: desc, Observed: "sentinel not found after two chances", TS: now(e)}
}

// precompactWarnThreshold is §12.1's "p99 > 60% of timeout ⇒ warn" threshold.
const precompactWarnThreshold = 0.6

// precompactFailThreshold is §12.1's "timeout hit ⇒ fail" threshold: p99 at or beyond the timeout
// itself.
const precompactFailThreshold = 1.0

// checkPreCompactTiming is CPreCompactTiming's real observation: the p99 of the 64 newest
// PreCompact wall-time samples against the manifest timeout.
func checkPreCompactTiming(ctx context.Context, e Env) Result {
	const desc = "PreCompact has time to write"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CPreCompactTiming, desc, e)
	}
	if h.PrecompactTimeoutMs <= 0 {
		return Result{OK: true, Expected: desc, Observed: "timeout-unknown", TS: now(e)}
	}
	if len(h.PrecompactWallMs) == 0 {
		return Result{OK: true, Expected: desc, Observed: "no-samples", TS: now(e)}
	}
	p99 := percentileMs(h.PrecompactWallMs, 99)
	ratio := float64(p99) / float64(h.PrecompactTimeoutMs)
	observed := "p99=" + formatMs(p99) + " timeout=" + formatMs(h.PrecompactTimeoutMs)
	switch {
	case ratio >= precompactFailThreshold:
		return Result{OK: false, Severity: SevCritical, Expected: desc, Observed: observed, TS: now(e)}
	case ratio > precompactWarnThreshold:
		return Result{OK: false, Severity: SevWarn, Expected: desc, Observed: observed, TS: now(e)}
	default:
		return Result{OK: true, Expected: desc, Observed: observed, TS: now(e)}
	}
}

// retiredCustomInstrDetail is why precompact.custom_instructions_accepted observes nothing.
const retiredCustomInstrDetail = "retired (C1.18): no host accepts a PreCompact instruction and Qompack " +
	"no longer emits one; custom_instructions is PreCompact input, not a summarizer setter (see docs/cannot-do.md)"

// checkPreCompactCustomInstr is CPreCompactCustomInstr's observation since C1.18: none, by design.
//
// The row used to search the transcript tail for the first line of the focus instruction the
// daemon last emitted (History.PrecompactInstr) and warn when it was absent. No host accepts that
// instruction — Claude Code has no PreCompact hookSpecificOutput variant, 2.1.280 rejected the whole
// response over one (C1.12), and custom_instructions is PreCompact INPUT (Qompack.md §7.3, §8.5
// "Retire O1's output setter") — so the probe could only warn about a mechanism that does not
// exist, or pass on a false positive: 2.1.280 replayed the rejected output, instruction included,
// into the post-compaction context. The producer is retired, so this row reports that and nothing
// else. It is OK because it can never fail and SevInfo because it is never a warning, and its
// Observed spelling is one noObservationSpellings lists: an `ok` here means "no assertion was made",
// never "the host accepted it". Its evidence is attributed to compaction_request, which the
// register calls unsupported (observation.go), so ClassifyResult reports `unsupported` either way.
//
// A History written by a pre-C1.18 daemon still carries an instruction; it is not read.
func checkPreCompactCustomInstr(ctx context.Context, e Env) Result {
	return Result{
		OK: true, Severity: SevInfo, Expected: "custom_instructions accepted",
		Observed: "retired", Detail: retiredCustomInstrDetail, TS: now(e),
	}
}

// hookEventKnownFields is the set of JSON keys hookio.Event's own struct tags claim, built once by
// reflection directly against hookio.Event so this set can never silently drift from
// hookio/event.go's own claimedKeys: any key in this set has ALREADY been routed to its typed
// field by hookio.ReadEvent, so its appearance in Event.Extra as well means something upstream
// duplicated it — the collision task-4-spec.md's hook.payload_shape row checks for.
var hookEventKnownFields = buildHookEventKnownFields()

func buildHookEventKnownFields() map[string]bool {
	t := reflect.TypeOf(hookio.Event{})
	keys := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "" {
			keys[tag] = true
		}
	}
	return keys
}

// checkHookPayloadShape is CHookPayloadShape's real observation: the required fields
// hookio.ReadEvent's own contract promises are present, and no unclaimed key duplicates a claimed
// one.
func checkHookPayloadShape(ctx context.Context, e Env) Result {
	const desc = "hook payload shape matches hookio.Event"
	if e.Event.HookEventName == "" {
		return Result{OK: false, Expected: desc, Observed: "missing hook_event_name", TS: now(e)}
	}
	if e.Event.SessionID == "" {
		return Result{OK: false, Expected: desc, Observed: "missing session_id", TS: now(e)}
	}
	if e.Event.CWD == "" && e.Event.TranscriptPath == "" {
		return Result{OK: false, Expected: desc, Observed: "missing both cwd and transcript_path", TS: now(e)}
	}
	keys := make([]string, 0, len(e.Event.Extra))
	for k := range e.Event.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if hookEventKnownFields[k] {
			// hookio.ReadEvent never lets a claimed key reach Extra (it is populated only from
			// keys the struct tags do not claim), so this arm cannot fire for an Event that came
			// off the wire. It guards a hand-built or future-decoder Event instead — kept because
			// the spec asks for the check, not because ReadEvent's own output can trigger it.
			return Result{OK: false, Expected: desc, Observed: "extra field collides with known field " + k, TS: now(e)}
		}
	}
	return Result{OK: true, Expected: desc, Observed: "payload shape valid", TS: now(e)}
}

// checkMCPServerRegistered is CMCPRegistered's real observation: whether the MCP server has
// completed its handshake with this project's daemon. Its declared severity is SevInfo (shipped by
// SP-01's StandardAssertions and pinned by standard_test.go), so a failure here is surfaced but
// never degrades a session on its own.
//
// The host connects its MCP servers beside a session's first SessionStart, not before it, so the
// start that runs this Check is usually too early to have seen the handshake: the Phase 4 live lane
// read "initialize-not-received" in every healthy first session (install D4, retrieval D7). A start
// with no handshake on record is therefore pending (History.MCPAwaitSession), and so is every start
// while the awaited session is still live or has not had a prompt: a second window opened beside
// the first says nothing about the first's handshake. The assertion fails at a start of another
// session only once the awaited session has had a prompt (SessionHistory.NotePrompt) and is no
// longer live (Env.SessionLive), so a whole session with a turn passed without a handshake. The
// failure is reported once; the await then moves to the starting session.
func checkMCPServerRegistered(ctx context.Context, e Env) Result {
	const desc = "MCP server received initialize"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CMCPRegistered, desc, e)
	}
	if h.MCPInitialized {
		h.MCPAwaitSession, h.MCPAwaitTurned = "", false
		return Result{OK: true, Expected: desc, Observed: "initialize-received", TS: now(e)}
	}
	pending := Result{OK: true, Expected: desc, Observed: "initialize-pending", TS: now(e)}
	sess := e.Event.SessionID
	awaited := h.MCPAwaitSession
	if sess == "" || awaited == sess || (awaited != "" && sessionLive(e, awaited)) {
		return pending
	}
	overdue := awaited != "" && h.MCPAwaitTurned
	h.MCPAwaitSession, h.MCPAwaitTurned = sess, false
	if overdue {
		return Result{OK: false, Expected: desc, Observed: "initialize-not-received", TS: now(e)}
	}
	return pending
}

// transcriptReadableTailBytes bounds how much of transcript_path the transcript.readable assertion
// reads for its last-line probe, so a multi-gigabyte transcript does not turn a SessionStart
// assertion into an unbounded read.
const transcriptReadableTailBytes = 64 << 10

// checkTranscriptReadable is CTranscriptReadable's real observation: transcript_path exists and
// its last non-empty line parses as JSON.
//
// The host creates a session's transcript with its first prompt, after SessionStart:startup, so a
// start other than a compaction's that finds none reports it pending and remembers the path and its
// session (History.TranscriptAwaitPath, TranscriptAwaitSession). A start of another transcript
// resolves that record first. One that has appeared is forgotten. One whose session is still live is
// kept pending: a second window opened beside the first says nothing about the first's transcript.
// One whose session ended without a prompt is forgotten, since no transcript was due. Only one whose
// session had a prompt (SessionHistory.NotePrompt) and is no longer live fails the assertion, once.
// The record holds one awaited transcript; while it is kept, a later start's own missing transcript
// is reported pending without being recorded. A compaction starts from the transcript the host has
// been writing all session, so its absence then fails at once, as it always did.
func checkTranscriptReadable(ctx context.Context, e Env) Result {
	const desc = "transcript_path exists and parses"
	path := e.Event.TranscriptPath
	h, hasHistory := historyOf(e)
	neverAppeared := false
	if hasHistory && h.TranscriptAwaitPath != "" && h.TranscriptAwaitPath != path {
		_, statErr := os.Stat(paths.Long(h.TranscriptAwaitPath))
		switch {
		case statErr == nil:
			h.forgetTranscriptAwait()
		case sessionLive(e, h.TranscriptAwaitSession):
			// Still running: it may not have had its first prompt yet. Keep waiting.
		default:
			neverAppeared = h.TranscriptAwaitTurned
			h.forgetTranscriptAwait()
		}
	}
	r := observeTranscript(e, desc, path, h, hasHistory)
	if neverAppeared && r.OK {
		return Result{
			OK: false, Expected: desc, Observed: "an earlier session's transcript_path never appeared", TS: now(e),
		}
	}
	return r
}

// observeTranscript is checkTranscriptReadable's reading of this start's own transcript_path.
func observeTranscript(e Env, desc, path string, h *SessionHistory, hasHistory bool) Result {
	if path == "" {
		return Result{OK: true, Expected: desc, Observed: "no-transcript-path", TS: now(e)}
	}
	if _, err := os.Stat(paths.Long(path)); err != nil {
		if e.Event.Source != "compact" {
			if hasHistory && h.TranscriptAwaitPath == "" {
				h.TranscriptAwaitPath, h.TranscriptAwaitSession = path, e.Event.SessionID
				h.TranscriptAwaitTurned = false
			}
			return Result{OK: true, Expected: desc, Observed: "transcript-pending", TS: now(e)}
		}
		return Result{OK: false, Expected: desc, Observed: "transcript_path does not exist", TS: now(e)}
	}
	if hasHistory && h.TranscriptAwaitPath == path {
		h.forgetTranscriptAwait()
	}
	line, err := lastNonEmptyLine(path)
	if err != nil {
		return Result{OK: false, Expected: desc, Observed: "transcript_path has no readable content", TS: now(e)}
	}
	if !json.Valid([]byte(line)) {
		return Result{OK: false, Expected: desc, Observed: "last transcript line is not valid JSON", TS: now(e)}
	}
	return Result{OK: true, Expected: desc, Observed: "transcript readable", TS: now(e)}
}

// pluginRootBinaries are the platform-specific binary names plugin.root_resolves accepts under
// CLAUDE_PLUGIN_ROOT/bin/.
var pluginRootBinaries = []string{"bin/qompack", "bin/qompack.exe"}

// checkPluginRootResolves is CPluginRootResolves's real observation: CLAUDE_PLUGIN_ROOT, when set,
// must expand to a directory containing the plugin binary.
func checkPluginRootResolves(ctx context.Context, e Env) Result {
	const desc = "CLAUDE_PLUGIN_ROOT expands to an existing binary"
	root := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if root == "" {
		return Result{OK: true, Expected: desc, Observed: "unset", TS: now(e)}
	}
	for _, rel := range pluginRootBinaries {
		fi, err := os.Stat(paths.Long(filepath.Join(root, rel)))
		if err == nil && !fi.IsDir() {
			return Result{OK: true, Expected: desc, Observed: "resolved", TS: now(e)}
		}
	}
	return Result{OK: false, Expected: desc, Observed: "CLAUDE_PLUGIN_ROOT set but no plugin binary found beneath it", TS: now(e)}
}

// lastNonEmptyLine returns the last non-blank line within the final transcriptReadableTailBytes of
// path.
func lastNonEmptyLine(path string) (string, error) {
	b, err := readTail(path, transcriptReadableTailBytes)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line, nil
		}
	}
	return "", errors.New("contract: no non-empty line in transcript tail")
}

// percentileMs returns the p-th percentile (1-100) of samples, nearest-rank, without mutating
// samples.
func percentileMs(samples []int64, p int) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (p*len(sorted) + 99) / 100 // nearest-rank, ceiling
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

// formatMs renders a millisecond count as "<n>ms" for a Result's Observed text.
func formatMs(ms int64) string {
	return strconv.FormatInt(ms, 10) + "ms"
}
