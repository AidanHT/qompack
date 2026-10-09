// SP-11's end-to-end rows: SessionStart(source=compact) driven through the REAL binary against a
// REAL daemon, which is the only place the whole L5 path — hook client -> ipc -> session.start
// route -> contract monitor -> Rehydrator seam -> rehydrate.Build -> additionalContext on stdout —
// is exercised as the host actually exercises it.
//
// Two cases in this file are deliberately NOT end-to-end and say so at their own doc comment: the
// mcp.DropReporter satisfaction check and the verbatim-prompt-id agreement check. Both live here
// because test/e2e is the composition root that may import both halves of a pair internal/*
// packages are forbidden to join (§3.2): internal/rehydrate may not import internal/mcp, and
// nothing may import both internal/rehydrate and internal/observer except a composition root.
//
// L5 has landed, so these pass against the real rehydrator. One case stays gated: the items whose
// only source is a checkpoint cannot be asserted until SP-10's reader replaces SP-01's stub, and
// TestE2E_SessionStartCompactRestoresCheckpointItems probes for that rather than assuming it.
package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// scSession is the session id every case in this file drives, spelled exactly as the SP-11 e2e
// table gives it.
const scSession = core.SessionID("e2e-1")

// The three §8.6 section headings a rehydrated digest must carry. They are asserted as literals
// rather than derived, because they are what a MODEL reads: a heading that silently changes
// spelling changes the injected document without changing any type.
const (
	scHeadingInvariants = "## 1. Invariants"
	scHeadingNoLonger   = "## 7. No longer in context"
	scHeadingRetrieval  = "## 8. Retrieval"
)

// scProbePrefix is the fixed prefix contract.RenderSentinel emits. It is spelled here so the
// "outside the injection span" assertion names the bytes it is looking for.
const scProbePrefix = "<!-- qompack-contract-probe "

// scRulesFixture is the project tree every case is built over: 00-ARCHITECTURE's proj-a, which
// carries a root CLAUDE.md and three nested ones. The nested files are the point — §8.6 item 6a
// exists because the host does not restore them after a compaction (G4.1, G4.2).
const scRulesFixture = "testdata/fixtures/rules/proj-a"

// scGoldenCheckpoint is the frozen §8.5 artifact (Rule W-2). This file COPIES it into the
// project's checkpoints/ directory and appends the MANIFEST line by hand: checkpoint.Writer
// belongs to a same-wave sibling and may not be called from here.
const scGoldenCheckpoint = "testdata/golden/contracts/checkpoint/want/0001.json"

// scLatencyP99 is the §11.2 first-turn-after budget this file grades a warm session-start against.
const scLatencyP99 = 1500 * time.Millisecond

// scLatencyRuns is how many samples the latency case takes.
const scLatencyRuns = 30

// scColoadJudges names the in-tree lanes that run test/e2e alone without obs.UnderColoadEnv, and so
// apply scLatencyP99 when a co-loaded run only reports it: ci.yml's test-e2e job, and devtool's own
// test/e2e pass (wholeTreePasses), which test, cover, ci-local and release-check run, as ci.yml's
// cover job does.
const scColoadJudges = "ci.yml's test-e2e and cover jobs, and devtool test, cover, ci-local and " +
	"release-check, each of which runs test/e2e in a pass of its own"

// ── setup helpers ───────────────────────────────────────────────────────────────────────────

// scProject builds a disposable project whose source tree is the proj-a rules fixture and whose
// .qompack/checkpoints/ holds the frozen §8.5 artifact plus its MANIFEST line.
func scProject(t *testing.T) *testutil.Project {
	t.Helper()

	root, err := moduleRoot()
	require.NoError(t, err)

	p := testutil.NewProject(t)
	p.WithFiles(t, scFixtureFiles(t, filepath.Join(root, filepath.FromSlash(scRulesFixture))))
	scSeedCheckpoint(t, root, p.Root)
	return p
}

// scFixtureFiles reads the fixture tree under dir into the slash-relative map WithFiles takes.
func scFixtureFiles(t *testing.T, dir string) map[string]string {
	t.Helper()

	out := map[string]string{}
	err := filepath.WalkDir(paths.Long(dir), func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(paths.Long(dir), p)
		if relErr != nil {
			return relErr
		}
		b, readErr := os.ReadFile(p) //nolint:gosec // a committed fixture path
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	require.NoError(t, err, "reading the %s fixture tree", scRulesFixture)
	require.NotEmpty(t, out, "the %s fixture tree is empty", scRulesFixture)
	return out
}

// scSeedCheckpoint copies the frozen artifact to <project>/.qompack/checkpoints/0001.json through
// paths.CreateNew — the ONLY sanctioned door onto an immutable checkpoint artifact (§7.4) — and
// records it in checkpoints/MANIFEST.jsonl so a reader that verifies before it reads finds it.
func scSeedCheckpoint(t *testing.T, repoRoot, projectRoot string) {
	t.Helper()

	golden, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(scGoldenCheckpoint)))
	require.NoError(t, err, "the frozen §8.5 checkpoint fixture must exist")

	l := paths.Of(projectRoot)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, core.CheckpointSeq(1)), golden))
	require.NoError(t, paths.AppendManifest(l, paths.ManifestEntry{
		Seq:     core.CheckpointSeq(1),
		SHA256:  hex.EncodeToString(sha256Of(golden)),
		Bytes:   int64(len(golden)),
		Created: core.UnixMilli(testutil.Epoch.UnixMilli()),
	}))
}

// sha256Of is the plain digest checkpoints/MANIFEST.jsonl records. It is NOT a domain-separated
// core.Hash: ManifestEntry.SHA256 is a bare string and §3.3 describes `qompack fsck` as re-hashing
// the artifact itself, so the entry has to be reproducible by anything that can read the file.
func sha256Of(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// scDeleteStore removes everything under .qompack/ that a rehydration could read.
//
// It is not a bare os.RemoveAll of the directory, and the reason is Windows rather than caution:
// testutil.Project holds its file-backed day log open until t.Cleanup runs, and Windows refuses to
// unlink a file another handle has open, so removing the whole tree fails on the log and leaves
// half the store behind — a failure that reads as "the rehydrator crashed" rather than "the test
// could not set up its own precondition". Every directory the L5 path reads is removed instead,
// and the two that matter most are asserted gone.
func scDeleteStore(t *testing.T, root string) {
	t.Helper()

	l := paths.Of(root)
	entries, err := os.ReadDir(paths.Long(l.Dot))
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() == filepath.Base(l.Logs) {
			continue
		}
		require.NoError(t, os.RemoveAll(paths.Long(filepath.Join(l.Dot, e.Name()))))
	}
	require.NoDirExists(t, paths.Long(l.Checkpoints), "the checkpoint store must be gone")
	require.NoDirExists(t, paths.Long(l.State), "the rehydration state must be gone")
}

// scStartPayload builds the SessionStart hook payload for source. transcript names the transcript
// file the host would have written; an empty transcript leaves the field unset.
func scStartPayload(t *testing.T, root, source, transcript string) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName:  "SessionStart",
		SessionID:      scSession,
		CWD:            root,
		Source:         source,
		TranscriptPath: transcript,
	})
	require.NoError(t, err)
	return b
}

// scRunStart runs `qompack session-start` against the real binary, asserts §2.3's only permitted
// outcome (exit 0 with one valid hookio.Output on stdout), and returns the decoded Output.
func scRunStart(t *testing.T, bin string, env map[string]string, payload []byte) hookio.Output {
	t.Helper()

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, payload, env)
	require.Equal(t, 0, code, "session-start must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	var out hookio.Output
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &out),
		"stdout must be one hookio.Output:\n%s", stdout)
	return out
}

// scAdditionalContext returns the payload the host would inject, failing when there is none.
func scAdditionalContext(t *testing.T, out hookio.Output) string {
	t.Helper()
	require.NotNil(t, out.HookSpecificOutput, "session-start must emit hookSpecificOutput")
	require.Equal(t, "SessionStart", out.HookSpecificOutput.HookEventName)
	return out.HookSpecificOutput.AdditionalContext
}

// scInjectedSpan returns the injected span of ac — the open tag through the close tag inclusive —
// and nothing around it.
//
// It exists because the daemon appends a freshly minted contract probe to every additionalContext
// it emits (internal/daemon/handlers.go), and that token is derived from the wall clock, so two
// otherwise identical runs never produce byte-identical additionalContext. The INJECTED span is
// the deterministic part, and it is the part §8.6 specifies.
func scInjectedSpan(t *testing.T, ac string) string {
	t.Helper()

	openAt := strings.Index(ac, scInjectOpen)
	require.GreaterOrEqual(t, openAt, 0, "the payload carries no §8.5 injection open tag:\n%s", ac)

	closeAt := strings.Index(ac[openAt:], checkpoint.InjectionCloseTag)
	require.GreaterOrEqual(t, closeAt, 0, "the payload carries no §8.5 injection close tag:\n%s", ac)

	return ac[openAt : openAt+closeAt+len(checkpoint.InjectionCloseTag)]
}

// scInjectOpen is the injection open tag up to its first verb, derived from the constant so these
// rows follow a rename of the tag; scLegacyInjectOpen is the spelling 0.3.x wrote, which a negative
// control must refuse as well, or it would pass trivially against a binary that still writes it.
var (
	scInjectOpen       = checkpoint.InjectionOpenTag[:strings.IndexByte(checkpoint.InjectionOpenTag, '%')]
	scLegacyInjectOpen = checkpoint.LegacyInjectionOpenTag[:strings.IndexByte(checkpoint.LegacyInjectionOpenTag, '%')]
)

// scWarmDaemon runs one session-start to bring the daemon up and waits for it to answer, so every
// later measurement in a case is against a warm daemon rather than a cold start.
func scWarmDaemon(t *testing.T, bin string, p *testutil.Project, env map[string]string) {
	t.Helper()
	scRunStart(t, bin, env, scStartPayload(t, p.Root, "startup", ""))
	e2eWaitDaemonUp(t, p.Root)
}

// scStatePath is <root>/.qompack/state/rehydrate-<sess>.json.
func scStatePath(root string) string {
	return filepath.Join(paths.Of(root).State, "rehydrate-"+string(scSession)+".json")
}

// scStateRecordBound bounds the wait for a compact rehydration's state file after the compact
// SessionStart that built it has answered.
//
// The file is written AFTER the answer, by design. C1.16 (e7c1954) hands the built payload to the
// waiting session.start route first, so that the drop report's durable write (a paths.WriteAtomic:
// staging file, fsync, rename) is never on the answer's path. internal/daemon/rehydrate_service.go
// says so where it offers the answer. The hook's reply is therefore no evidence that the file
// exists yet, and a row that reads it the moment the hook returns is racing the daemon's own write.
// What this waits for is one durable file write that follows a reply, the same class of work
// obsProcessAllowance already bounds for the observer's post-ACK store writes, so it reuses that
// allowance instead of adding a second number for the same mechanism.
const scStateRecordBound = obsProcessAllowance

// scAwaitState waits, within scStateRecordBound, for the rehydration state file at p to exist, and
// returns it decoded.
//
// It reads through paths.ReadFileShared, never os.ReadFile, and that is half of the fix. The writer
// replaces the file with a POSIX-semantics rename issued through a handle that holds DELETE access
// (internal/paths/replace_windows.go, posixReplace). On Windows an os.ReadFile handle
// (FILE_SHARE_READ|FILE_SHARE_WRITE, no FILE_SHARE_DELETE) cannot open the file while that handle
// is open, and fails with ERROR_SHARING_VIOLATION: the co-load red w3-e2ereds recorded for
// TestV5_PreCompactToRehydrateToDroppedRoundTrip. The same os.ReadFile handle, held across the
// rename, also makes the daemon's replace fail, so a row reading that way can turn its own read
// into a failed Record. Measured on Windows over 10 s each (plans/sdd/V6-closeout/w4-e2eflakes/
// runs/diag-b-sharing-modes-rerun-windows.txt): an os.ReadFile reader racing WriteAtomic replaces
// saw 863 sharing violations in 53,980 reads and failed 98 of 402 replaces; a paths.ReadFileShared
// reader saw none in 63,201 reads and failed none of 449. The product's own reader of this file,
// the dropped tool's rehydrate CurrentDrops, already reads through paths.ReadFileShared.
//
// Only "does not exist yet" is waited out. Any other read error, and a file that does not decode,
// fails at once: the replace is atomic, so a reader never sees a partial file, and no other error
// is a transient this wait may absorb.
//
// A ticker paces the poll (§6.1 bans time.Sleep in tests), and the bound is a wall-clock
// comparison rather than a second select channel, for the reason x13v4Quiesce gives: an expired
// wait here is a hard failure, so its bound must not slip.
func scAwaitState(t *testing.T, p string) rehydrate.State {
	t.Helper()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	deadline := time.Now().Add(scStateRecordBound)
	for {
		b, err := paths.ReadFileShared(p)
		switch {
		case err == nil:
			var st rehydrate.State
			require.NoError(t, json.Unmarshal(b, &st), "%s must decode as a rehydrate.State: %s", p, b)
			return st
		case !errors.Is(err, fs.ErrNotExist):
			require.NoError(t, err, "reading the rehydration state file %s", p)
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, "the compact rehydration never persisted its state file",
				"%s did not appear within %s of the compact SessionStart's answer. The rehydrate "+
					"service writes it right after it offers the answer (C1.16), so its absence means "+
					"the Record never ran or failed, not that it was slow", p, scStateRecordBound)
		}
		<-ticker.C
	}
}

// scFailedSummaryTranscript writes the "the model called a tool instead of summarizing" tail: a
// transcript whose last assistant record is a tool_use block and which carries no <summary>
// anywhere. Qompack.md §8.5 names this as the compaction failure mode the checkpoint exists to
// absorb.
func scFailedSummaryTranscript(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "failed-summary.jsonl")
	body := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"fix the refresh 500s"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Reading the handler."},` +
			`{"type":"tool_use","id":"toolu_sc1","name":"Read","input":{"file_path":"src/db/pool.ts"}}],` +
			`"usage":{"output_tokens":120}}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_sc1",` +
			`"content":"export const pool = new Pool({ timeout: 30 });"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[` +
			`{"type":"tool_use","id":"toolu_sc2","name":"Grep","input":{"pattern":"refreshToken"}}],` +
			`"usage":{"output_tokens":40}}}`,
		"",
	}, "\n")
	require.NoError(t, os.WriteFile(paths.Long(path), []byte(body), 0o600))
	require.NotContains(t, body, "<summary>", "this fixture is the NO-summary failure mode")
	return path
}

// ── the cases ───────────────────────────────────────────────────────────────────────────────

// TestE2E_SessionStartCompact is the SP-11 headline row: the host's compact SessionStart gets a
// rehydrated §8.6 digest back, wrapped in the §4.6 injection tags so the next compaction strips it
// out again rather than compressing a compression.
func TestE2E_SessionStartCompact(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)

	out := scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))
	ac := scAdditionalContext(t, out)
	require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")

	// The headings that do not depend on a checkpoint. Items 7 and 8 are built from the drop set
	// and a fixed string, so they are present on every rehydration the daemon can perform on this
	// branch; the injection tags are what make the payload strippable at all.
	for _, heading := range []string{scHeadingNoLonger, scHeadingRetrieval} {
		require.Contains(t, ac, heading, "§8.6 item heading missing from the digest:\n%s", ac)
	}
	require.Contains(t, ac, checkpoint.InjectionCloseTag,
		"the digest must close with the §8.5 injection tag, or the next checkpoint re-encodes it")
}

// TestE2E_SessionStartCompactRestoresCheckpointItems is the other half of the headline row: the
// items whose ONLY source is a checkpoint — invariants, intent, eliminations, decisions, current
// work and pointers.
//
// It cannot pass on feat/sp11-rehydrator-l5 and is not supposed to. checkpoint.OpenReader is
// SP-01's stub until SP-10 merges immediately ahead of this branch in the wave-3 order, so
// Latest reports ErrNotImplemented, the service takes its documented no-checkpoint path, and the
// digest is correctly built from the ledger and the skill index alone with Ref{Seq:0} — which is
// exactly what the payload above shows (`seq=0`, `checkpoint 0000`). Seeding
// .qompack/checkpoints/0001.json does not change that: the stub reader never opens the file.
//
// This is Rule W-2 working as intended rather than a defect. The rebase onto SP-10 is where the
// reader becomes real, and this test is the thing that then proves the seam actually carries a
// checkpoint end to end. Do not weaken it to pass early — an assertion that a real reader would
// satisfy is the whole point of leaving it here.
func TestE2E_SessionStartCompactRestoresCheckpointItems(t *testing.T) {
	if !scCheckpointReaderIsReal(t) {
		t.Skip("contract fixture not yet recorded (Rule W-2)")
	}
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)

	out := scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))
	ac := scAdditionalContext(t, out)

	require.Contains(t, ac, scHeadingInvariants,
		"§8.6 item 1 must carry the checkpoint's pinned invariants verbatim:\n%s", ac)
	require.NotContains(t, ac, "checkpoint 0000",
		"a real reader must resolve the seeded checkpoint rather than the no-checkpoint path")
}

// scCheckpointReaderIsReal reports whether checkpoint.OpenReader returns a working reader yet.
// It probes rather than hard-coding a branch name, so the test above activates itself the moment
// SP-10 merges, with no edit here.
func scCheckpointReaderIsReal(t *testing.T) bool {
	t.Helper()
	r, err := checkpoint.OpenReader(t.TempDir(), logging.Nop(), obs.New(core.SystemClock()))
	if err != nil || r == nil {
		return false
	}
	_, err = r.List(context.Background())
	return !core.IsNotImplemented(err)
}

// TestE2E_ContractSentinelIsAppendedByTheDaemon pins the ownership boundary §12.1's
// hook.additional_context_delivered mechanism depends on.
//
// The probe is minted by the session.start ROUTE (contract.MintSentinel, handlers.go), never by
// the rehydrator, and it must sit OUTSIDE the injection tags. Inside them,
// checkpoint.StripInjections would remove it on the way back in — and the very next
// UserPromptSubmit scans the transcript tail for exactly that token, so a swallowed probe reads as
// "additionalContext never reached the transcript" and degrades the session to passive recording
// over a bug in where a comment marker was placed.
func TestE2E_ContractSentinelIsAppendedByTheDaemon(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)

	ac := scAdditionalContext(t, scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", "")))
	require.Contains(t, ac, scProbePrefix, "the session.start route must append its contract probe")

	closeAt := strings.LastIndex(ac, checkpoint.InjectionCloseTag)
	require.GreaterOrEqual(t, closeAt, 0, "no injection close tag in:\n%s", ac)
	probeAt := strings.Index(ac, scProbePrefix)
	require.Greater(t, probeAt, closeAt,
		"the contract probe must follow the injection close tag; inside the span "+
			"checkpoint.StripInjections would swallow it")

	require.Contains(t, checkpoint.StripInjections(ac), scProbePrefix,
		"the probe must survive StripInjections, which is what the UserPromptSubmit scan reads")
}

// TestE2E_AdditionalContextProducerIsDeclared closes §12.1's not-yet-implemented loop.
//
// Until a producer for hook.additional_context_delivered exists in the build, gated() reports
// OK/SevInfo/"not-yet-implemented" and the real Check never runs — which is correct for a wave-1
// build and wrong the moment L5 ships, because it would leave the one assertion that proves
// injection works permanently unevaluated. The literal is asserted rather than a constant because
// contract.notYetImplementedObserved is unexported.
func TestE2E_AdditionalContextProducerIsDeclared(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	p := scProject(t)

	// Built the way cmd/qompack builds it: NewOptions, then WireObserver (which reaches
	// WireRehydrator, the wave-3 daemon bootstrap block), then New — whose bind loop runs every
	// registered Bind and then calls DeclareProducers.
	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	opts.Metrics = obs.New(p.Clock)
	_, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	// WireObserver opens a store, and the WireRehydrator block inside it opens the ledger. Both
	// hold append-only handles, and an unreleased handle makes t.TempDir cleanup fail on Windows.
	t.Cleanup(func() {
		if led := opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
		if opts.Store != nil {
			_ = opts.Store.Close()
		}
	})

	d, err := daemon.New(opts)
	require.NoError(t, err)
	require.NotNil(t, d)

	mon := contract.NewMonitor(p.Log, obs.New(p.Clock), filepath.Join(paths.Of(p.Root).State, "contract.json"))
	for _, a := range contract.StandardAssertions() {
		require.NoError(t, mon.Register(a))
	}
	mon.RunAll(t.Context(), contract.Env{
		ProjectRoot: p.Root,
		Event:       hookio.Event{HookEventName: "SessionStart", SessionID: scSession, CWD: p.Root, Source: "compact"},
		Cfg:         p.Cfg,
		Log:         p.Log,
		Clock:       p.Clock,
		History:     contract.LoadHistory(contract.HistoryPath(p.Root)),
	})

	var found bool
	for _, r := range mon.Report() {
		if r.ID != contract.CAdditionalContext {
			continue
		}
		found = true
		require.NotEqual(t, "not-yet-implemented", r.Observed,
			"a build with an L5 rehydrator has a producer for %s, so its real Check must run",
			contract.CAdditionalContext)
	}
	require.True(t, found, "RunAll must report %s", contract.CAdditionalContext)
}

// TestE2E_SessionStartCompactUnderBudget: §8.6 targets 8-12K against the host's own 50K + 25K, and
// runtime.rehydrate.maxTokens is the hard cap. A digest over budget is not a slightly worse
// digest — it is the eager-restore behaviour Qompack exists to replace.
func TestE2E_SessionStartCompactUnderBudget(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)
	ac := scAdditionalContext(t, scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", "")))

	// The injected span only: the contract probe is the route's, not the rehydrator's, and
	// charging it to the rehydration budget would grade L5 on someone else's bytes.
	span := scInjectedSpan(t, ac)
	est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
	got := est.EstimateString(span, tokens.ClassProse)

	require.LessOrEqual(t, int(got), p.Cfg.Runtime.Rehydrate.MaxTokens,
		"the digest is %d estimated tokens, over runtime.rehydrate.maxTokens (%d)",
		int(got), p.Cfg.Runtime.Rehydrate.MaxTokens)
}

// TestE2E_SessionStartClear: `/clear` is the user deliberately discarding context, so the
// rehydrator injects nothing and forgets what it had injected.
//
// The assertion is phrased against the rehydrator's own contribution rather than against
// additionalContext as a whole, because the session.start route appends its contract probe to
// EVERY start it may act on (handlers.go) — a clear included. Asserting the field were empty would
// be asserting a shipped, verified behaviour away.
func TestE2E_SessionStartClear(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)
	scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))
	// The compact run must have recorded what it injected. The record lands after the answer
	// (scStateRecordBound), so this waits for it, which also keeps it from landing after the
	// clear below and recreating the file the clear just removed.
	scAwaitState(t, scStatePath(p.Root))

	out := scRunStart(t, bin, env, scStartPayload(t, p.Root, "clear", ""))
	ac := ""
	if out.HookSpecificOutput != nil {
		ac = out.HookSpecificOutput.AdditionalContext
	}
	require.NotContains(t, ac, scInjectOpen, "a clear injects no rehydrated span")
	require.NotContains(t, ac, scLegacyInjectOpen, "a clear injects no rehydrated span, in either spelling")
	require.NotContains(t, ac, scHeadingInvariants, "a clear injects no §8.6 digest")

	// Whatever survives is the route's own probe line and nothing else.
	for _, line := range strings.Split(strings.TrimSpace(ac), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		require.True(t, strings.HasPrefix(strings.TrimSpace(line), scProbePrefix),
			"a clear emitted a line the rehydrator had no business emitting: %q", line)
	}

	require.NoFileExists(t, paths.Long(scStatePath(p.Root)),
		"a clear resets the drop report; the context it described is gone")
}

// TestE2E_SessionStartCompactNoStore: a project whose .qompack/ has been deleted between runs
// still starts. §12.3 — a session that starts with less context is recoverable; a session that
// fails to start is not — and §2.3 permits a hook no exit code but 0.
func TestE2E_SessionStartCompactNoStore(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)
	scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))

	// The daemon holds handles under .qompack/run and .qompack/spool; it goes first so the removal
	// is not racing a live writer.
	e2eShutdownIfReachable(t, p.Root)
	scDeleteStore(t, p.Root)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, scStartPayload(t, p.Root, "compact", ""), env)
	require.Equal(t, 0, code,
		"a compact start over a deleted .qompack/ must still exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	var out hookio.Output
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &out),
		"stdout must remain one hookio.Output:\n%s", stdout)

	if out.HookSpecificOutput != nil {
		require.NotContains(t, out.HookSpecificOutput.AdditionalContext, scHeadingInvariants,
			"with no store there is nothing to rehydrate; the payload degrades rather than inventing one")
	}
}

// TestE2E_SessionStartCompactAfterFailedSummary is Qompack.md §8.5's named compaction failure: the
// model calls a tool instead of writing the summary, so the transcript tail holds a tool-call block
// and no <summary> at all.
//
// The checkpoint is the fallback path, which is the whole reason it is written BEFORE the
// compaction rather than derived from its output. So a failed compaction call costs the API call,
// not the context — and the injected span must be the same one the well-formed run produced,
// byte for byte.
func TestE2E_SessionStartCompactAfterFailedSummary(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)

	wellFormed := scAdditionalContext(t, scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", "")))
	failed := scAdditionalContext(t, scRunStart(t, bin, env,
		scStartPayload(t, p.Root, "compact", scFailedSummaryTranscript(t, t.TempDir()))))

	require.Equal(t, scInjectedSpan(t, wellFormed), scInjectedSpan(t, failed),
		"a failed compaction call costs cost, not context: the checkpoint fallback produces the "+
			"same digest whether or not the host's summarizer succeeded")
}

// TestE2E_DropReporterSatisfiesMCP is a compile-time assertion, not an end-to-end run.
//
// internal/mcp declares DropReporter and internal/rehydrate implements it, because the dependency
// runs mcp -> checkpoint and never mcp -> rehydrate (§3.2). Neither package may name the other, so
// the only place the satisfaction can be stated at all is a composition root that imports both.
func TestE2E_DropReporterSatisfiesMCP(t *testing.T) {
	p := testutil.NewProject(t)

	var reporter mcp.DropReporter = rehydrate.NewReporter(p.Root, p.Log)
	require.NotNil(t, reporter)

	drops, err := reporter.CurrentDrops(t.Context(), scSession)
	require.NoError(t, err, "a session that never rehydrated has no drops, which is not an error")
	require.Empty(t, drops)
}

// TestUserIntent_FirstPromptIDMatchesObserver pins the join between L0's verbatim capture and L5's
// item 2.
//
// §8.6 item 2 is "the verbatim original intent, from L0 (G2.3)" — never regenerated from a
// summary. The rehydrator finds it by asking the store for the tool_use record L0 filed the first
// prompt under, and that identity is minted by observer.VerbatimPromptID. If the two ever disagree
// the digest silently loses the user's own words, which is the single failure G2.3 exists to
// prevent. This lives in test/e2e because only a composition root may import both packages (§3.2).
func TestUserIntent_FirstPromptIDMatchesObserver(t *testing.T) {
	// rehydrate exports no helper of its own, so the derived form is asserted as the literal the
	// two sides have to agree on.
	derived := core.ToolUseID("prompt_" + string(scSession) + "_0")
	require.Equal(t, observer.VerbatimPromptID(scSession, 0), derived,
		"L5 derives the first prompt's tool_use id the way L0 minted it")
}

// TestE2E_SessionStartLatency grades a warm compact start against §11.2's first-turn-after budget.
//
// It is a timing row, and it is gated the way the other intrinsically wall-clock rows are (ADR
// 0010 decision 2, obs.UnderColoadEnv). The number is a wall-clock p99 over 30 process spawns,
// each a whole hook process and its round trip to the daemon, so no CPU clock stands in for it.
// Run alone, as scColoadJudges names, it is judged against scLatencyP99. In a run that declares
// co-load it is measured and reported, with the limit it does not apply and where that limit is
// still applied, as X11 reports B-A. It is not fsync-bound: the rehydration state file's durable
// write follows the answer (C1.16, scStateRecordBound), so a non-reference disk declaration leaves
// it gated.
//
// Criterion change (wave 21, D53(a)): the row used to skip itself under -short, a guard nothing ran
// and ADR 0010 rules out, and it gated even under a declared co-load. Wave 20's status verifier
// measured p99 1.53 s in an undeclared daytime co-loaded run, and its whole distribution had moved,
// not one sample: 158 ms to 1.53 s, median about 380 ms. Runs on AC at wave 21, alone and inside
// whole test/e2e runs, measured p99 142 to 319 ms, and the worst tail seen was one 633 ms sample,
// the next at 254 ms, in a quiet test/e2e run (X11 skipped) at 03f6824a: nearest-rank p99 over 30
// samples is the maximum, so one spawn decides the row. So the row now runs under -short, reports
// under the declaration, and always logs its samples. The budget and the statistic are unchanged.
func TestE2E_SessionStartLatency(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)
	payload := scStartPayload(t, p.Root, "compact", "")

	samples := make([]time.Duration, 0, scLatencyRuns)
	for range scLatencyRuns {
		started := time.Now()
		scRunStart(t, bin, env, payload)
		samples = append(samples, time.Since(started))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	// Nearest rank, matching eval.percentilesOf: idx = ceil(0.99·N) − 1.
	idx := (99*len(samples)+99)/100 - 1
	p99 := samples[min(max(idx, 0), len(samples)-1)]

	t.Logf("SessionStart latency: p99 of %d warm compact session-starts = %s against the %s budget "+
		"(samples: %s)", len(samples), p99, scLatencyP99, fmt.Sprint(samples))
	if obs.UnderCoload() {
		t.Logf("%s is set: this co-loaded run reports the p99 above and does not apply the %s budget "+
			"(ADR 0010 decision 2). It is applied where test/e2e runs alone without the declaration: %s",
			obs.UnderColoadEnv, scLatencyP99, scColoadJudges)
		return
	}
	require.Less(t, p99, scLatencyP99,
		"p99 of %d warm compact session-starts was %s, over the %s budget (samples: %s)",
		len(samples), p99, scLatencyP99, fmt.Sprint(samples))
}
