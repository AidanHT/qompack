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
	"unicode/utf8"

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
// naming THIS session, which is the same absence of proof) -> StartsWithoutMarker++, failing only
// once that reaches 2 ("absence across two sessions"). The very first session a project has ever
// seen has no prior terminal hook to have left a marker, so it reports OK regardless.
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
	if h.LastSessionID != e.Event.SessionID {
		h.StartsWithoutMarker++
		h.LastSessionID = e.Event.SessionID
	}
	if h.StartsWithoutMarker >= 2 {
		return Result{
			OK: false, Expected: desc,
			Observed: "no marker from a prior terminal hook across two consecutive sessions",
			TS:       now(e),
		}
	}
	return Result{OK: true, Expected: desc, Observed: "marker-absent-once", TS: now(e)}
}

// checkSessionStartSourceCompact is CSessionStartSourceCompact's real observation: when a
// PreCompact was observed for this session (History.AwaitingCompactStart), the FOLLOWING
// SessionStart must arrive with source=="compact". The flag is cleared either way, because it is
// only ever evaluated once, on the next start.
func checkSessionStartSourceCompact(ctx context.Context, e Env) Result {
	const desc = "SessionStart arrives with source=compact after PreCompact"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CSessionStartSourceCompact, desc, e)
	}
	if !h.AwaitingCompactStart {
		return Result{OK: true, Expected: desc, Observed: "no-precompact-pending", TS: now(e)}
	}
	h.AwaitingCompactStart = false
	if e.Event.Source == "compact" {
		return Result{OK: true, Expected: "compact", Observed: e.Event.Source, TS: now(e)}
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

// customInstrScanTailBytes bounds how much of the transcript's tail
// precompact.custom_instructions_accepted scans for the probe phrase (task-4-spec.md: 256<<10).
const customInstrScanTailBytes = 256 << 10

// customInstrMinPhraseChars is the minimum length task-4-spec.md expects the emitted instruction's
// first line to have, so the probe phrase is specific enough not to false-positive against
// unrelated transcript text.
const customInstrMinPhraseChars = 24

// checkPreCompactCustomInstr is CPreCompactCustomInstr's real observation: the emitted
// custom_instructions' first line, searched for in the transcript tail. Advisory by design (§8.5):
// its declared severity is SevWarn, never SevCritical.
func checkPreCompactCustomInstr(ctx context.Context, e Env) Result {
	const desc = "custom_instructions accepted"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CPreCompactCustomInstr, desc, e)
	}
	if h.PrecompactInstr == "" {
		return Result{OK: true, Expected: desc, Observed: "no-instructions-emitted", TS: now(e)}
	}
	if e.Event.TranscriptPath == "" {
		return Result{OK: true, Expected: desc, Observed: "no-transcript-path", TS: now(e)}
	}
	phrase, ok := probePhrase(h.PrecompactInstr)
	if !ok {
		// A first line shorter than customInstrMinPhraseChars (including empty — an instruction
		// beginning with a newline) is not specific enough to scan for: bytes.Contains against ""
		// is vacuously true, which would make this assertion unable to ever fail, and a short
		// phrase (a heading, a bullet marker) would false-positive against unrelated transcript
		// text. Neither is an observation; both report "no probe possible", not an automatic pass
		// dressed up as one.
		return Result{OK: true, Expected: desc, Observed: "no probe phrase long enough", TS: now(e)}
	}
	found, err := ScanTranscriptTail(e.Event.TranscriptPath, phrase, customInstrScanTailBytes)
	if err != nil {
		// An unreadable transcript proves nothing about whether the phrase was ever written —
		// sentinel.go's own doc comment states this rule and every caller in this package follows
		// it.
		return Result{OK: true, Expected: desc, Observed: "transcript unreadable, no observation yet", TS: now(e)}
	}
	if !found {
		return Result{OK: false, Expected: desc, Observed: "instruction phrase not found in transcript tail", TS: now(e)}
	}
	return Result{OK: true, Expected: desc, Observed: "instruction phrase found in transcript tail", TS: now(e)}
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
// received initialize at least once this session. Its declared severity is SevInfo (shipped by
// SP-01's StandardAssertions and pinned by standard_test.go), so a failure here is surfaced but
// never degrades a session on its own.
func checkMCPServerRegistered(ctx context.Context, e Env) Result {
	const desc = "MCP server received initialize"
	h, ok := historyOf(e)
	if !ok {
		return noObservationYet(CMCPRegistered, desc, e)
	}
	if h.MCPInitialized {
		return Result{OK: true, Expected: desc, Observed: "initialize-received", TS: now(e)}
	}
	return Result{OK: false, Expected: desc, Observed: "initialize-not-received", TS: now(e)}
}

// transcriptReadableTailBytes bounds how much of transcript_path the transcript.readable assertion
// reads for its last-line probe, so a multi-gigabyte transcript does not turn a SessionStart
// assertion into an unbounded read.
const transcriptReadableTailBytes = 64 << 10

// checkTranscriptReadable is CTranscriptReadable's real observation: transcript_path exists and
// its last non-empty line parses as JSON.
func checkTranscriptReadable(ctx context.Context, e Env) Result {
	const desc = "transcript_path exists and parses"
	path := e.Event.TranscriptPath
	if path == "" {
		return Result{OK: true, Expected: desc, Observed: "no-transcript-path", TS: now(e)}
	}
	if _, err := os.Stat(paths.Long(path)); err != nil {
		return Result{OK: false, Expected: desc, Observed: "transcript_path does not exist", TS: now(e)}
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

// firstLine returns s up to its first newline, or the whole of s if it contains none.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// probePhrase selects instr's probe phrase for precompact.custom_instructions_accepted: its first
// line, but only when that line is at least customInstrMinPhraseChars RUNES long (task-4-spec.md:
// "first line of >= 24 characters" — a character count, matching SetPrecompactInstr's own 256-char
// cap, which is likewise measured in runes, not bytes). ok is false when no qualifying phrase
// exists — a short or empty first line — so the caller can report "no probe possible" instead of
// scanning for a phrase too generic (or empty) to mean anything.
func probePhrase(instr string) (phrase string, ok bool) {
	line := firstLine(instr)
	if utf8.RuneCountInString(line) < customInstrMinPhraseChars {
		return "", false
	}
	return line, true
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
