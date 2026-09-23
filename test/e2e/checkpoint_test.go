// The SP-10 e2e rows: the L4 checkpointer driven through the REAL binary and a REAL daemon.
//
// This file owns the FULL-mode row (plans/V4-SP-10-checkpointer-l4.md, "Conformance and e2e"):
// `qompack checkpoint` arrives as a genuine PreCompact hook, the bound Services.PreCompact seam
// finalizes the live draft, and the artifact it leaves behind is immutable and manifest-consistent.
// checkpoint_degraded_test.go owns the degraded-passive row, which is a DIFFERENT outcome rather
// than a weaker one; the shared composition helpers below are defined here and used by both.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/pluginmanifest"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// cpSession is the session identity the full-mode row drives. It is distinct from
// cpDegradedSession so the two rows never share a draft file, a segment range or a registry entry
// even when the whole package runs in one process.
const cpSession = core.SessionID("sess-e2e-sp10")

// The replay shape of the full-mode row, spelled as named constants because three separate
// assertions below are derived from them and a silent edit to one would make the other two
// vacuous.
const (
	// cpToolReplays is the plan's "replay 60 synthetic turns through `qompack observe tool`".
	cpToolReplays = 60
	// cpPromptEvery inserts one `qompack observe prompt` before every cpPromptEvery-th tool use.
	//
	// It is not decoration. The SHIPPED observer advances its turn counter only on
	// UserPromptSubmit and Stop (internal/observer/prompt.go's st.Turn++ and stop.go's), never on
	// PostToolUse — so a pure tool-use replay leaves every DAG node at turn 0 and the phrase
	// "60 turns" would describe nothing on disk. The prompts are what make the replayed span a
	// real multi-turn session, and they also give Begin a UserPrompt node to take
	// UserIntent.Original from.
	cpPromptEvery = 20

	// cpSegments is how many segments this row closes DIRECTLY through SP-06's shipped
	// store.SegmentLog.Close. It deliberately does not use SP-12's changepoint detector: SP-12 is
	// a same-wave sibling and may not be merged when this runs.
	cpSegments = 3
	// cpSegmentTurns is the inclusive turn width of each closed segment, so segment i covers
	// [i*cpSegmentTurns, (i+1)*cpSegmentTurns-1] and the last one ends at cpFrontierTurn.
	cpSegmentTurns = 20
	// cpFrontierTurn is the turn the checkpoint frontier must reach once all three segments are
	// encoded: Advance sets d.frontier = max(d.frontier, maxEndTurn(encoded)) (§8, step 5), and
	// the highest EndTurn this row closes is cpSegments*cpSegmentTurns-1.
	cpFrontierTurn = core.TurnIndex(cpSegments*cpSegmentTurns - 1)
)

// cpRelCheckpointPathOf is the project-relative, forward-slash form of checkpoint seq's artifact —
// the exact string FocusInstructions renders into the O1 span paragraph through FileWriter.relPath
// (§14 item 2: forward slashes on every platform, so this form is correct on Windows too).
func cpRelCheckpointPathOf(seq core.CheckpointSeq) string {
	return fmt.Sprintf(".qompack/checkpoints/%04d.json", int(seq))
}

// cpIdleBudget is the budget handed to IdleController.RunOnce. Basis: B-E is 2 s for the whole
// PreCompact hook and the cadence task shares that shape, while advance_frontier's own budget
// (BenchmarkAdvanceSegment, < 25 ms per segment) is three orders of magnitude below it. Ten
// seconds is therefore not padding over a hang — past it, the tick has provably stopped making
// progress rather than merely being slow — and it matches the shape of X8's own 5 s tick budget
// with headroom for three registered tasks instead of one.
const cpIdleBudget = 10 * time.Second

// cpMinProbeRunes is contract.customInstrMinPhraseChars (internal/contract/assertions.go): the
// floor probePhrase imposes on the FIRST LINE of a customInstructions payload before it will build
// a probe from it. It is duplicated here rather than imported because the constant is unexported;
// the assertion that uses it is what keeps SP-05's precompact.custom_instructions_accepted from
// silently becoming a permanent non-observation.
const cpMinProbeRunes = 24

// cpPreCompactArtifact is the on-disk shape of .qompack/state/precompact.json — the
// Qompack-INTERNAL debug artifact §15 step 6 writes. Nothing in internal/contract reads this file,
// so no contract assertion may be claimed to depend on it; this row reads it for exactly one
// reason, which is that it is the only published carrier of the Ref.Frontier the emitted span
// paragraph was rendered from (Reader-returned Refs leave Frontier zero by design, §12).
type cpPreCompactArtifact struct {
	Seq               core.CheckpointSeq `json:"seq"`
	Sentinel          string             `json:"sentinel"`
	EmittedAt         core.UnixMilli     `json:"emitted_at"`
	WallMs            int64              `json:"wall_ms"`
	TimeoutMs         int64              `json:"timeout_ms"`
	InstructionsBytes int                `json:"instructions_bytes"`
	Frontier          core.TurnIndex     `json:"frontier"`
	SpanInstruction   bool               `json:"span_instruction"`
}

// cpDraftState is the on-disk shape of .qompack/state/draft-<session>.json (§7 "Persistence"):
// the live, unsealed draft the frontier advances into between checkpoints. Only the four fields
// both rows assert on are declared — the embedded "checkpoint" object is SP-10's own and is
// asserted through the sealed artifact instead.
type cpDraftState struct {
	Session core.SessionID     `json:"session"`
	Seq     core.CheckpointSeq `json:"seq"`
	Parent  core.CheckpointSeq `json:"parent"`
	// Frontier is the turn this draft covers through. It is the number "advanced" means.
	Frontier core.TurnIndex `json:"frontier"`
	// Encoded is the segment ids Advance has folded into this draft.
	Encoded []core.SegmentID `json:"encoded"`
}

// cpDaemon is one composed in-process daemon plus the three SP-10 handles a row drives it
// through: the writer the seam finalizes, the SourceSet it reads from, and the segment log the row
// closes segments on directly.
type cpDaemon struct {
	D    daemon.Daemon
	W    *checkpoint.FileWriter
	Src  checkpoint.SourceSet
	Segs store.SegmentLog
}

// cpStartDaemon composes the daemon IN PROCESS — daemon.NewOptions, daemon.WireObserver,
// daemon.BindCheckpoint, daemon.New, daemon.RegisterObserverIdleWork, daemon.WireCheckpoint — and
// runs it on its own goroutine, exactly as v3_x08_test.go composes the wave-2 daemon.
//
// It is in process for two reasons no spawned daemon can satisfy, and for no others. First,
// BindCheckpoint takes an ADDRESSABLE *Options and must run BEFORE daemon.New (New applies o.binds
// inside itself, so a bind registered afterwards never runs at all), while WireCheckpoint takes
// the CONSTRUCTED Daemon because Idle() is a method on Daemon and there is no Options.Idle —
// nothing outside the process can perform that two-phase split. Second, the rows below assert on
// IdleController.RunOnce's own `ran` list, which is the §12.1 act.-prefix mechanism made
// observable and which no hook's stdout carries.
//
// Everything the rows actually TEST still goes through the real binary: the 60+ hook calls and
// `qompack checkpoint` itself are real processes whose exit codes and stdout bytes are the
// evidence, connecting over the real transport to whatever daemon owns the project's endpoint.
func cpStartDaemon(t *testing.T, p *testutil.Project, sess core.SessionID) *cpDaemon {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	// opts.Clock is deliberately left as NewOptions' SystemClock rather than p.Clock. The
	// PreCompact path derives a context deadline from the clock it is given (§15 step 1:
	// hard = Deadline - finalizeGuard, floored at start+minFinalizeWindow), so a FakeClock frozen
	// at testutil.Epoch would hand Finalize a deadline decades in the past and cancel the very
	// write this row exists to assert. The store, ledger and graph keep p.Clock, where frozen time
	// is harmless and deterministic.

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opts.Store.Close() })

	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: opts.Store, Graph: opts.Graph, Session: sess, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	opts.Ledger = led

	pinStore, err := pins.Open(p.Root)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	// The SourceSet is the ONLY thing a checkpoint may be built from (§5.14): every field is a
	// seam onto durable, original content, and it structurally cannot carry live context text.
	src := checkpoint.SourceSet{
		Store:    opts.Store,
		Segments: opts.Store.Segments(),
		Ledger:   led,
		Pins:     pinStore,
		Graph:    opts.Graph,
		Grammar:  grammar.New(),
		Tokens:   tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root),
	}
	require.NoError(t, src.Validate(),
		"the SourceSet must be fully wired before the daemon binds the seam — Begin calls Validate first")

	// Phase 1, before New: bind the seam the SHIPPED handleCheckpoint route already calls. This is
	// NOT o.Handle(ipc.OpCheckpoint, ...) on purpose — re-registering the op would replace SP-05's
	// route and silently delete the History PreCompact observation, the terminal-hook marker, the
	// B-E timing and both PreCompact contract assertions along with it.
	daemon.BindCheckpoint(&opts, p.Cfg, w, src)

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)
	// Phase 2, after New: the three idle registrations, whose NAMES are the §12.1 mode gate.
	daemon.WireCheckpoint(d, p.Cfg, w, src)

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() {
		if stopErr := d.Stop(context.Background()); stopErr != nil {
			t.Errorf("e2e: stopping the SP-10 daemon: %v", stopErr)
		}
		cancelRun()
		if runErr := <-runDone; runErr != nil {
			t.Errorf("e2e: the SP-10 daemon's Run returned: %v", runErr)
		}
	})
	e2eWaitDaemonUp(t, p.Root)

	return &cpDaemon{D: d, W: w, Src: src, Segs: opts.Store.Segments()}
}

// cpReplayTurns drives cpToolReplays PostToolUse calls through the real binary, inserting one
// UserPromptSubmit before every cpPromptEvery-th of them, and then waits for the asynchronous
// half — worker pool, observer, index append — to catch up.
func cpReplayTurns(t *testing.T, c *cpDaemon, bin string, p *testutil.Project, sess core.SessionID, n int) {
	t.Helper()
	env := e2eEnv(p)
	for i := range n {
		if i%cpPromptEvery == 0 {
			obsRunHook(t, bin, []string{"observe", "prompt"},
				obsPromptPayload(t, p.Root, sess, fmt.Sprintf("sp10 replay: work item %d", i/cpPromptEvery)), env)
		}
		obsRunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, sess, fmt.Sprintf("toolu_sp10_%02d", i),
				fmt.Sprintf("src/sp10_%02d.go", i), fmt.Sprintf("package sp10_%02d\n", i)), env)
	}
	cpWaitForToolUseIndex(t, c, p.Root, n)
}

// cpWaitForToolUseIndex waits until index/tool_use.jsonl holds at least want records, driving
// Drain itself rather than waiting on the daemon's own 30 s idle-tick backstop.
//
// A hot-path call whose 8 ms ACK wait expired against a healthy daemon is DURABLE in the client
// spool but invisible in the index until a drain, so a poll that only read the file would be
// waiting on a mechanism it never kicked — the same reasoning v3_x08_test.go's own wait documents.
// Drain is idempotent and ingest's seen-set collapses a WAL+spool duplicate back to one dispatch.
func cpWaitForToolUseIndex(t *testing.T, c *cpDaemon, root string, want int) {
	t.Helper()
	ctx := context.Background()

	// A ticker, not time.Sleep, per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck
	// sub-check). v3_x08_test.go's equivalent wait predates that check and still sleeps; copying
	// its shape here would have added a second offender rather than inheriting an exemption.
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	timeout := time.NewTimer(obsProcessBound)
	defer timeout.Stop()

	// ONE Drain, unconditionally, before the index is read for the first time. The loop below
	// evaluates its condition FIRST, so a wait whose records the async ingest had already published
	// drives no Drain at all while a slower one drives several — and Drain is not side-effect-free:
	// drainer.Drain calls saveState unconditionally (internal/daemon/drain.go), which always goes
	// through paths.WriteAtomic, so every drain rewrites state/drain.json and mints a transient
	// tmp/wa-* staging file. Without this drive, whether those artifacts exist is decided purely by
	// timing. v4Rig.WaitIndexed carries the same drive for the same reason, and §4.13's write-set
	// comparison is where that coin flip was actually caught — this helper is one call away from
	// cpCheckpointArtifacts, which many rows read. Do not "simplify" it back into the loop.
	_, _ = c.D.Drain(ctx)

	for len(obsToolUseLines(root)) < want {
		_, _ = c.D.Drain(ctx)
		if len(obsToolUseLines(root)) >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "the replayed turns never reached the index",
				"index/tool_use.jsonl never reached %d lines within %s (have %d); LOUD: %v",
				want, obsProcessBound, len(obsToolUseLines(root)), loudLines(t, root))
		}
	}
}

// cpCloseSegment opens one segment for sess spanning [start, end] and closes it at end through
// SP-06's shipped store.SegmentLog API, returning the id the log assigned.
//
// Closing DIRECTLY is the point of this helper and is required by the plan: SP-12's changepoint
// detector is the production closer, it is a same-wave sibling, and an e2e that waited for it
// would be red for a reason that has nothing to do with SP-10. A zero ID makes Open allocate
// maxID+1, so these never collide with the segment the observer itself opened at session start —
// which stays OPEN and is therefore excluded from Unencoded by construction.
//
// The "tokens" feature is supplied deliberately: Close warns once per segment and leaves
// Segment.Tokens zero permanently when it is absent, and a checkpoint priced from a zero-token
// segment is not the artifact this row means to assert on.
func cpCloseSegment(t *testing.T, segs store.SegmentLog, sess core.SessionID, start, end core.TurnIndex) core.SegmentID {
	t.Helper()
	ctx := context.Background()
	id, err := segs.Open(ctx, store.Segment{Session: sess, StartTurn: start})
	require.NoError(t, err, "opening a segment for turns %d-%d", start, end)
	require.NoError(t, segs.Close(ctx, id, end, map[string]float64{
		"tokens": float64((end - start + 1) * 64),
	}), "closing segment %d at turn %d", id, end)
	return id
}

// cpCloseObserverSegment closes whatever segment the real observer opened at session-start.
//
// It has to happen before the idle tick, and the reason is the whole point of the frontier's
// contract. store.SegmentLog.Frontier is the EndTurn of the last CONSECUTIVELY encoded segment: it
// walks the session's segments in order and stops at the first one that is not encoded. The
// observer opens a segment when the session starts and does not close it until a BOCD boundary
// fires, so that segment sits at the HEAD of the list, unencoded, and pins the frontier at 0 for
// as long as it is open -- correctly, because a checkpoint that does not contain turn 0 does not
// cover the session "through" any later turn either. The synthetic segments this row closes
// overlap it, which is a fixture artifact: production has one segment set, not two.
//
// Closing it makes the fixture answerable. It is left UNENCODED on purpose, so advance_frontier
// picks it up with the rest and the frontier the row asserts is one the checkpointer really earned.
func cpCloseObserverSegment(t *testing.T, segs store.SegmentLog, sess core.SessionID) {
	t.Helper()
	ctx := context.Background()
	cur, err := segs.Current(ctx, sess)
	if err != nil || cur.Closed {
		return // nothing open: the observer already closed it, which is equally fine
	}
	require.NoError(t, segs.Close(ctx, cur.ID, cur.StartTurn, map[string]float64{"tokens": 64}),
		"closing the observer's open segment %d", int(cur.ID))
}

// cpCloseSegments closes n consecutive segments of cpSegmentTurns turns each, starting at turn 0,
// and returns their ids in ascending order.
func cpCloseSegments(t *testing.T, segs store.SegmentLog, sess core.SessionID, n int) []core.SegmentID {
	t.Helper()
	ids := make([]core.SegmentID, 0, n)
	for i := range n {
		start := core.TurnIndex(i * cpSegmentTurns)
		ids = append(ids, cpCloseSegment(t, segs, sess, start, start+cpSegmentTurns-1))
	}
	return ids
}

// cpCheckpointArtifacts returns the base names of every sealed checkpoint artifact under
// checkpoints/, sorted ascending. MANIFEST.jsonl is not one: it ends in .jsonl, and it is asserted
// separately through paths.ReadManifest.
func cpCheckpointArtifacts(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Checkpoints))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name())
		}
	}
	return out
}

// cpReadDraft reads state/draft-<session>.json, reporting whether it exists yet. A missing draft
// is not a failure: no draft exists until the first Begin, which is itself an observable state the
// degraded row asserts on.
func cpReadDraft(t *testing.T, root string, sess core.SessionID) (cpDraftState, bool) {
	t.Helper()
	p := filepath.Join(paths.Of(root).State, "draft-"+string(sess)+".json")
	b, err := os.ReadFile(paths.Long(p))
	if os.IsNotExist(err) {
		return cpDraftState{}, false
	}
	require.NoError(t, err)
	var d cpDraftState
	require.NoError(t, json.Unmarshal(b, &d), "state/draft-%s.json must be valid JSON:\n%s", sess, b)
	return d, true
}

// cpPreCompactPayload builds the PreCompact payload a host writes for an automatic compaction:
// the §5.3 fields that hook populates, with a cwd that is a real native path.
func cpPreCompactPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PreCompact",
		"session_id":      sess,
		"cwd":             root,
		"transcript_path": filepath.Join(root, "transcript.jsonl"),
		"trigger":         "auto",
	})
	require.NoError(t, err)
	return b
}

// cpRequireHostConformingPreCompact holds a `qompack checkpoint` stdout to the host's PreCompact
// contract (testdata/host/hooks-output-schema.json): no hookSpecificOutput, because the host has no
// PreCompact variant and rejects the whole response over one (C1.12), and no systemMessage or
// continue, which the host discards for PreCompact.
func cpRequireHostConformingPreCompact(t *testing.T, out hookio.Output) {
	t.Helper()
	require.Nil(t, out.HookSpecificOutput,
		"`qompack checkpoint` must not answer the host with a hookSpecificOutput: Claude Code has no "+
			"PreCompact variant and rejects the whole response over one (C1.12)")
	require.Empty(t, out.SystemMessage, "the host discards a PreCompact systemMessage")
	require.Nil(t, out.Continue, "the host discards a PreCompact continue")
}

// cpPreCompactReply sends one PreCompact to the daemon's checkpoint route over the real IPC
// transport, exactly as the hook client frames it, and returns the daemon's reply and the focus
// instruction in it. See v4Rig.PreCompactReply for why a row reads the instruction here.
func cpPreCompactReply(t *testing.T, root string, sess core.SessionID) (hookio.Output, string) {
	t.Helper()
	return cpPreCompactReplyFor(t, root, hookio.Event{
		HookEventName:  "PreCompact",
		SessionID:      sess,
		CWD:            root,
		TranscriptPath: filepath.Join(root, "transcript.jsonl"),
		Trigger:        "auto",
	})
}

// cpPreCompactReplyFor is cpPreCompactReply for a caller that holds the whole PreCompact event, such
// as a frozen host payload, rather than only its session.
func cpPreCompactReplyFor(t *testing.T, root string, ev hookio.Event) (hookio.Output, string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	sp, err := ipc.NewSpool(paths.Of(root).Spool)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	raw, err := json.Marshal(map[string]string{"trigger": ev.Trigger})
	require.NoError(t, err)
	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpCheckpoint, Session: ev.SessionID, TS: core.NowMilli(core.SystemClock()), Reply: true,
		Event: &ev, Raw: raw,
	}, cpCheckpointReplyDeadline)
	require.NoError(t, err, "the checkpoint route must answer over IPC")
	require.True(t, resp.OK, "the checkpoint route must answer OK: %s", resp.Err)
	require.NotNil(t, resp.Output, "a Reply request is answered with an Output")
	out := *resp.Output
	if out.HookSpecificOutput == nil {
		return out, ""
	}
	return out, out.HookSpecificOutput.CustomInstructions
}

// cpCheckpointReplyDeadline mirrors internal/cli's checkpointReplyDeadline (15 s), the reply
// deadline the hook client itself gives the checkpoint route.
const cpCheckpointReplyDeadline = 15 * time.Second

// cpRunCheckpointHook runs `qompack checkpoint` as a real process against a real daemon and
// asserts the two invariants §2.3 gives the host on EVERY path — exit 0, and a stdout that is one
// valid hookio.Output — returning the decoded response for the row's own mode-specific
// assertions.
func cpRunCheckpointHook(t *testing.T, bin string, p *testutil.Project, sess core.SessionID) hookio.Output {
	t.Helper()
	stdout, stderr, code := Run(t, bin, []string{"checkpoint"}, cpPreCompactPayload(t, p.Root, sess), e2eEnv(p))
	require.Equal(t, 0, code,
		"§2.3: `qompack checkpoint` must exit 0 whatever happens inside it\nstdout:\n%s\nstderr:\n%s",
		stdout, stderr)
	var out hookio.Output
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &out),
		"`qompack checkpoint` stdout must be one hookio.Output:\n%s", stdout)
	return out
}

// cpPreCompactTimeoutMs is the PreCompact timeout the plugin manifest declares, in milliseconds —
// read from internal/pluginmanifest rather than written as 20000, which is exactly what keeps the
// value single-owned (and what the `nomagic` contract requires of the package under test).
func cpPreCompactTimeoutMs(t *testing.T) int64 {
	t.Helper()
	groups, ok := pluginmanifest.Default(core.Version).Hooks.Hooks["PreCompact"]
	require.True(t, ok, "the plugin manifest must declare a PreCompact hook")
	require.NotEmpty(t, groups)
	require.NotEmpty(t, groups[0].Hooks)
	const msPerSecond = 1000
	return int64(groups[0].Hooks[0].Timeout) * msPerSecond
}

// cpFirstLine returns s up to its first newline — contract.probePhrase's own definition of the
// phrase it scans the transcript tail for.
func cpFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestE2E_CheckpointHookWritesImmutableArtifact is the full-mode SP-10 e2e row
// (plans/V4-SP-10-checkpointer-l4.md "Conformance and e2e"; V4-VERIFY row V4-SP10-18).
//
// The whole path, end to end and in one process tree: the real binary replays 60 turns through
// `qompack observe tool`, three segments are closed directly through SP-06's shipped
// store.SegmentLog.Close (never through SP-12's changepoint detector, which is a same-wave sibling
// and may not be merged), one idle tick advances the frontier into the live draft, and then a real
// `qompack checkpoint` process feeds a real PreCompact payload down stdin.
//
// What it pins that a unit test cannot: that the hook's own stdout is the host's PreCompact
// contract (the empty object, C1.12) while the seal still happens, that the focus instruction is
// still rendered on the daemon's side of the wire, and that the artifact on disk is immutable and
// manifest-consistent as a SEPARATE process left it. It asserts the artifact's identity three ways
// — it exists, it carries no write bit, and it re-hashes to its MANIFEST.jsonl line — because
// those are three different failures (not written, written mutably, written corrupt) that a single
// FileExists cannot tell apart.
func TestE2E_CheckpointHookWritesImmutableArtifact(t *testing.T) {
	ctx := context.Background()
	bin := Build(t)
	p := testutil.NewProject(t)
	// A hook whose connect transiently fails can lazily spawn a real detached daemon; whatever is
	// reachable at the end is shut down before t.TempDir()'s own cleanup runs.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	c := cpStartDaemon(t, p, cpSession)

	// session-start is what makes the session LIVE in the registry, which is what
	// advanceAllSessions iterates, and it does so before any replayed turn arrives. observe.tool's
	// Touch also registers a session it has never seen, but only once the first tool call lands.
	// This call makes liveness a stated precondition of the row rather than a side effect of the
	// replay, so the idle tick can never pass while doing nothing because no session was live.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, cpSession), env)
	require.True(t, c.D.Registry().IsLive(cpSession),
		"session-start must have registered %s as live before the idle tick runs", cpSession)

	cpReplayTurns(t, c, bin, p, cpSession, cpToolReplays)

	cpCloseObserverSegment(t, c.Segs, cpSession)
	segIDs := cpCloseSegments(t, c.Segs, cpSession, cpSegments)
	require.Len(t, segIDs, cpSegments)
	unencoded, err := c.Segs.Unencoded(ctx, cpSession)
	require.NoError(t, err)
	require.Len(t, unencoded, cpSegments+1,
		"Unencoded filters to Closed && !EncodedOnce: the %d segments this row closed plus the "+
			"observer's own session-start segment, now closed too; got %+v",
		cpSegments, unencoded)

	// ── One idle tick: the frontier advances, and nothing is sealed yet ──────────────────────────
	ran, err := c.D.Idle().RunOnce(ctx, cpIdleBudget)
	require.NoError(t, err)
	require.Subset(t, ran, []string{"advance_frontier", "act.checkpoint_cadence", "materialize_pins"},
		"in ModeFull all three SP-10 idle tasks must run, the act.-prefixed one included; ran=%v", ran)

	draft, ok := cpReadDraft(t, p.Root, cpSession)
	require.True(t, ok, "advance_frontier must have begun and persisted state/draft-%s.json", cpSession)
	require.Equal(t, cpFrontierTurn, draft.Frontier,
		"the frontier is store.SegmentLog.Frontier -- the EndTurn of the last CONSECUTIVELY encoded "+
			"segment, NOT max(EndTurn of encoded). A frontier of N is the O1 instruction's claim that "+
			"the checkpoint fully covers the session THROUGH turn N, so a gap anywhere below must hold "+
			"it back. Every segment of this session is encoded here, so the contiguous answer is %d",
		cpFrontierTurn)
	require.Subset(t, draft.Encoded, segIDs,
		"every segment closed above must have been folded into the draft; draft.encoded=%v", draft.Encoded)

	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"act.checkpoint_cadence must NOT have sealed anything yet: this draft is under "+
			"cfg.Checkpoint.BudgetTokens and holds %d encoded segments, below the 8-segment cadence "+
			"threshold (§8.5). A file here means the cadence fired and the artifact asserted below "+
			"would not be the one PreCompact wrote",
		cpSegments)

	// ── The hook: a real process, a real PreCompact payload on stdin ─────────────────────────────
	// What reaches the host is the empty response. This row once required the focus instruction on
	// the hook's stdout; Claude Code rejects exactly that shape (C1.12), so the host half is now the
	// documented contract and the instruction is read, further down, on the IPC hop it never leaves.
	out := cpRunCheckpointHook(t, bin, p, cpSession)
	cpRequireHostConformingPreCompact(t, out)

	// ── state/precompact.json: the debug artifact, and the only published carrier of Ref.Frontier ─
	artifactPath := filepath.Join(paths.Of(p.Root).State, "precompact.json")
	rawArtifact, err := os.ReadFile(paths.Long(artifactPath))
	require.NoError(t, err, "§15 step 6 writes state/precompact.json on every PreCompact")
	var art cpPreCompactArtifact
	require.NoError(t, json.Unmarshal(rawArtifact, &art), "state/precompact.json:\n%s", rawArtifact)
	require.Equal(t, core.CheckpointSeq(1), art.Seq, "the first checkpoint of a fresh project is seq 1")
	require.Equal(t, checkpoint.SentinelPhrase, art.Sentinel)
	require.Equal(t, cpFrontierTurn, art.Frontier,
		"the debug artifact's frontier is the Ref.Frontier the span paragraph was rendered from")
	require.True(t, art.SpanInstruction,
		"config.Defaults().Checkpoint.IncrementalSpanInstruction is true, so the span paragraph was requested")
	require.Equal(t, cpPreCompactTimeoutMs(t), art.TimeoutMs,
		"timeout_ms comes from the daemon's pluginmanifest read, never from a literal inside internal/checkpoint")
	require.GreaterOrEqual(t, art.WallMs, int64(0))
	require.Positive(t, art.InstructionsBytes,
		"the daemon rendered the focus instruction for this seal, even though the host never receives it")

	// ── The artifact: it exists, it is immutable, and it re-hashes to its manifest line ──────────
	require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(t, p.Root),
		"the hook call must have added exactly one artifact, and it must be the first of the chain")
	cpPath := paths.CheckpointPath(paths.Of(p.Root), 1)
	fi, err := os.Stat(paths.Long(cpPath))
	require.NoError(t, err)
	require.Zero(t, fi.Mode().Perm()&0o222,
		"paths.CreateNew chmods every checkpoint to 0o444 (FILE_ATTRIBUTE_READONLY on Windows): a "+
			"written checkpoint carries no write bit (§7.4)")

	raw, err := os.ReadFile(paths.Long(cpPath))
	require.NoError(t, err)
	entries, err := paths.ReadManifest(paths.Of(p.Root))
	require.NoError(t, err)
	require.Len(t, entries, 1, "one finalize appends exactly one MANIFEST.jsonl line")
	require.Equal(t, core.CheckpointSeq(1), entries[0].Seq)
	require.Equal(t, int64(len(raw)), entries[0].Bytes)
	sum := sha256.Sum256(raw)
	require.Equal(t, core.Hash(sum).String(), entries[0].SHA256,
		"the artifact must re-hash to its manifest line — the same undomained sha256 over the raw "+
			"file bytes that Finalize recorded and that `qompack fsck` re-computes")

	// The bytes are a real §8.5 document, not merely a file that happens to hash correctly.
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err, "0001.json must parse as a versioned Checkpoint:\n%s", raw)
	require.Equal(t, checkpoint.SchemaVersion, cp.Version)
	require.Equal(t, cpSession, cp.Session)
	require.Equal(t, core.CheckpointSeq(1), cp.Seq)
	require.Empty(t, cp.Parent, "the first checkpoint of a chain has no parent")
	require.Subset(t, cp.EncodedSegments, segIDs,
		"the DPI record: the artifact names the segments whose ORIGINAL content it encoded (§4.6)")

	// ── The instruction itself, read where it still exists: the daemon's IPC reply ───────────────
	// A second compaction of the same session, sent to the checkpoint route the way the hook client
	// frames it. It seals 0002.json over the same frontier, and its reply carries the O1 paragraphs
	// the hook client withholds from the host.
	_, instr := cpPreCompactReply(t, p.Root, cpSession)
	require.NotEmpty(t, instr, "the full-mode checkpoint route must still render the focus instruction")
	rawArtifact, err = os.ReadFile(paths.Long(artifactPath))
	require.NoError(t, err)
	var art2 cpPreCompactArtifact
	require.NoError(t, json.Unmarshal(rawArtifact, &art2), "state/precompact.json:\n%s", rawArtifact)
	require.Equal(t, core.CheckpointSeq(2), art2.Seq, "the second compaction seals seq 2")
	require.Equal(t, cpFrontierTurn, art2.Frontier, "no segment moved, so neither did the frontier")
	require.Contains(t, instr,
		fmt.Sprintf("A durable checkpoint (`%s`) fully covers the session through turn %d",
			cpRelCheckpointPathOf(art2.Seq), int(art2.Frontier)),
		"the O1 span paragraph must name the artifact's project-relative path and the frontier\ninstructions:\n%s", instr)
	require.Contains(t, instr, fmt.Sprintf("Summarize only what happened after turn %d", int(art2.Frontier)),
		"the span paragraph's whole purpose is narrowing the summarizer to the residual span\ninstructions:\n%s", instr)
	require.Contains(t, instr, checkpoint.SentinelPhrase,
		"ForbidSnippets is always true on the PreCompact path, so the sentinel appears in every rendered payload")
	require.NotContains(t, instr, `\`,
		"the span paragraph's path is rendered with forward slashes on every platform (§14 item 2)")
	require.GreaterOrEqual(t, utf8.RuneCountInString(cpFirstLine(instr)), cpMinProbeRunes,
		"the FIRST LINE is what contract.probePhrase turns into a probe; below %d runes "+
			"precompact.custom_instructions_accepted can never observe anything again",
		cpMinProbeRunes)
	require.Equal(t, len(instr), art2.InstructionsBytes, "the debug artifact records the rendered length")
}
