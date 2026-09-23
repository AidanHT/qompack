// V5 §4.4 — PreCompact (SP-10) → SessionStart(source=compact) rehydration (SP-11) → the persisted
// drop report → MCP `dropped` (SP-13) → `qompack dropped --json`, with no PostCompact anywhere.
//
// Current criterion (V5-VERIFY §4 row 4.4): "SP-10/SP-11/SP-13 bounded recovery/coverage without
// optional-PostCompact dependency." Three facts, each asserted against a real producer:
//
//   - RECOVERY: the checkpoint the real PreCompact hook sealed is the one the real rehydrator
//     resolves, and the payload restores what the host does not — the `paths:`-scoped rule and the
//     nested CLAUDE.md that the checkpoint's own file pointer pulls in, plus the skill index.
//   - BOUNDED: the payload never exceeds runtime.rehydrate.maxTokens, and when the budget cannot
//     hold everything the rehydrator says so — item 7 names or counts every omission and the
//     persisted state file carries the complete list regardless of what fit.
//   - COVERAGE: the MCP `dropped` tool (reached through a real `qompack mcp` child over stdio and
//     the daemon's real `mcp` op) and the `qompack dropped` frontend (a real process through the
//     shipped proxy) both return exactly the entries the rehydrator persisted, byte for byte.
//
// The "without optional-PostCompact dependency" clause is asserted two ways: the hook sequence
// driven here is PreCompact then SessionStart(compact) and nothing else, and the shipped plugin
// manifest declares no PostCompact hook at all — there is no event to have depended on.
//
// Retired clauses this row must NOT assert (historical §4.4 text): nothing claims the host
// accepted the customInstructions or shortened native history; nothing claims the payload's token
// count is a host-observed figure (it is the supported estimator's count, recorded as such); and
// the historical "≤ 12 000 tokens" literal is replaced by the configured bound the daemon actually
// ran under, read from config rather than restated.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/pluginmanifest"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// The three arms' session identities, distinct from every other file's and from each other so a
// drop report can never be read across arms.
const (
	x4v5Session        = core.SessionID("sess-e2e-v5-x04")
	x4v5BoundedSession = core.SessionID("sess-e2e-v5-x04-bounded")
	x4v5ControlSession = core.SessionID("sess-e2e-v5-x04-control")
)

// x4v5Intent is the verbatim first prompt, carrying a nonce that appears nowhere else in the tree
// so item 2's presence is evidence of verbatim carriage rather than of a paraphrase.
const x4v5Intent = "make /api/orders reject a duplicate idempotency key, nonce Q5X4-VERBATIM-7b1e"

// x4v5Path is the file every seeded tool use reads. It is under the proj-a fixture's
// `paths: ["src/api/**"]` rule and beside its nested src/api/CLAUDE.md, which is what makes the
// checkpoint's file pointer the thing that pulls both into item 6a.
const x4v5Path = "src/api/routes.ts"

// x4v5Turns is how many tool uses the live half of the session drives — the historical §4.4
// setup's own figure.
const x4v5Turns = 40

// x4v5Segments is how many cpSegmentTurns-wide segments are closed before the PreCompact, so the
// real advancer has real encoded evidence and the sealed artifact carries the file pointer.
const x4v5Segments = 3

// x4v5SealedSeq is the sequence the first checkpoint of a fresh project carries.
const x4v5SealedSeq = core.CheckpointSeq(1)

// x4v5TightBudget is the bounded arm's runtime.rehydrate budget in tokens. Basis: the injection
// wrapper and document header cost well under a hundred tokens, while the seeded session's forty
// tool pointers alone cost several hundred, so this bound is large enough to inject something and
// small enough that the rehydrator provably cannot carry everything.
const x4v5TightBudget = 600

// x4v5ReinjectionOffJSON is Qompack.md v1.5 Appendix C's injection kill switch, spelled as the
// project config file the daemon reads. It is the negative control's real runtime switch.
const x4v5ReinjectionOffJSON = `{"runtime":{"migration":{"reinjection":{"sessionStartCompact":false}}}}`

// x4v5Headings are the §8.6 section headings a rehydrated payload may carry, in normative order.
// They are spelled as literals because they are what a MODEL reads (sessionstart_compact_test.go
// records the same reasoning).
var x4v5Headings = []string{
	"## 1. Invariants",
	"## 2. Original user intent",
	"## 3. Approaches already eliminated",
	"## 4. Decisions",
	"## 5. Current work",
	"## 6. Pointers",
	"## 6a. Restored instructions",
	"## 6b. Skill index",
	"## 7. No longer in context",
	"## 8. Retrieval",
}

// x4v5DroppedBody is the `dropped` tool's JSON body as SP-13 renders it.
type x4v5DroppedBody struct {
	Drops     []checkpoint.DropEntry `json:"drops"`
	Count     int                    `json:"count"`
	Available *bool                  `json:"available"`
	Reason    string                 `json:"reason"`
}

// x4v5Project is a disposable project whose source tree is the proj-a rules fixture — the root
// CLAUDE.md, three nested ones, the `paths:`-scoped rules and three skills — under an optional
// project config.
func x4v5Project(t *testing.T, cfgJSON string) *testutil.Project {
	t.Helper()
	root, err := moduleRoot()
	require.NoError(t, err)

	var opts []testutil.ProjectOpt
	if cfgJSON != "" {
		opts = append(opts, testutil.WithConfig(cfgJSON))
	}
	p := testutil.NewProject(t, opts...)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	p.WithFiles(t, scFixtureFiles(t, filepath.Join(root, filepath.FromSlash(scRulesFixture))))
	return p
}

// x4v5StartRig composes the daemon exactly as v4StartRig does — the shipped composition,
// transcribed — and additionally installs the L6 retrieval op the way internal/cli's
// installMCPTools does, through the exported cli.NewToolDeps and daemon.InstallMCPOp, BEFORE
// daemon.New (see installMCPTools for why the order is load-bearing).
//
// The v4 rows left retrieval uninstalled because reproducing the Widener/promoter adaptation by
// hand would have asserted a composition nobody ships. cli.NewToolDeps IS the shipped adaptation,
// so the daemon here answers `mcp` ops with the production handler set: a `qompack mcp` child and
// a `qompack dropped` process both reach the real `dropped` handler over the real transport.
func x4v5StartRig(t *testing.T, p *testutil.Project) *v4Rig {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	// opts.Clock stays NewOptions' SystemClock, never p.Clock: the PreCompact path derives a
	// context deadline from the clock it is handed (v4_harness_test.go records the same).

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opts.Store.Close() })
	t.Cleanup(func() {
		if led := opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
	})

	pinStore, err := pins.OpenWith(p.Root, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	gram := grammar.New()
	toks := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)

	sources := func() (checkpoint.SourceSet, error) {
		var segs store.SegmentLog
		if opts.Store != nil {
			segs = opts.Store.Segments()
		}
		src := checkpoint.SourceSet{
			Store:    opts.Store,
			Segments: segs,
			Ledger:   opts.LedgerHandle(),
			LedgerFn: opts.LedgerHandle,
			Pins:     pinStore,
			Graph:    opts.Graph,
			Grammar:  gram,
			Tokens:   toks,
		}
		if _, valErr := src.Resolve(); valErr != nil {
			return src, fmt.Errorf("%w: %w", valErr, core.ErrDegraded)
		}
		return src, nil
	}

	opts.Checkpoints = w
	snapshot, _ := sources()
	daemon.BindCheckpoint(&opts, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	// The L6 retrieval op, composed as installMCPTools composes it: a per-call checkpoint reader,
	// the real drop reporter over this project, the real promoter and the real symbol extractor,
	// with the ledger reached LIVE through Options.LedgerHandle.
	ckptReader, err := checkpoint.OpenReader(p.Root, p.Log, opts.Metrics)
	require.NoError(t, err)
	prom, err := mcp.NewPromoter(mcp.PromotionsPath(p.Root), p.Cfg.Retrieval.PromoteAfterExpansions, opts.Clock)
	require.NoError(t, err)
	deps := cli.NewToolDeps(p.Root, p.Cfg, opts.Store, opts.LedgerHandle, ckptReader,
		rehydrate.NewReporter(p.Root, p.Log), prom, symbols.New(), p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, daemon.InstallMCPOp(&opts, deps), "the eight retrieval tools must install")

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)
	daemon.WireCheckpoint(d, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() {
		if stopErr := d.Stop(context.Background()); stopErr != nil {
			t.Errorf("e2e: stopping the V5 daemon: %v", stopErr)
		}
		cancelRun()
		if runErr := <-runDone; runErr != nil {
			t.Errorf("e2e: the V5 daemon's Run returned: %v", runErr)
		}
	})
	e2eWaitDaemonUp(t, p.Root)

	return &v4Rig{D: d, W: w, Segs: opts.Store.Segments(), Src: sources, Opts: &opts, P: p, Bin: Build(t)}
}

// x4v5SeedSession drives the live half of a session through the real binary: registration, the
// verbatim first prompt and x4v5Turns reads of x4v5Path, then closes real segments over them and
// lets the real advancer commit a frontier so the PreCompact seals from durable evidence.
func x4v5SeedSession(t *testing.T, r *v4Rig, sess core.SessionID) {
	t.Helper()
	env := e2eEnv(r.P)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, r.P.Root, sess), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"}, obsPromptPayload(t, r.P.Root, sess, x4v5Intent), env)
	for i := range x4v5Turns {
		obsRunHook(t, r.Bin, []string{"observe", "tool"},
			obsToolPayload(t, r.P.Root, sess, fmt.Sprintf("toolu_v5x04_%02d", i), x4v5Path,
				fmt.Sprintf("// %s revision %02d\nexport const orders%02d = router();\n", x4v5Path, i, i)), env)
	}
	r.WaitIndexed(t, x4v5Turns)

	cpCloseObserverSegment(t, r.Segs, sess)
	for i := range x4v5Segments {
		start := core.TurnIndex(i * cpSegmentTurns)
		cpCloseSegment(t, r.Segs, sess, start, start+cpSegmentTurns-1)
	}
	require.Contains(t, r.RunIdle(t), "advance_frontier", "the advancing idle task must have run")
}

// x4v5Seal drives the real PreCompact hook and asserts the SP-10 half of the row: the artifact
// sealed, verifying against its MANIFEST line, belonging to sess, carrying the file pointer item
// 6a will match against, and described by a span paragraph that names the LOCAL frontier the
// advancer committed.
func x4v5Seal(t *testing.T, r *v4Rig, sess core.SessionID) checkpoint.Checkpoint {
	t.Helper()

	// The span paragraph's CONTENT is asserted below, so the instruction is read on the IPC hop where
	// it still exists (v4Rig.PreCompactReply); the host never receives it.
	out, instr := r.PreCompactReply(t, sess)
	require.NotNil(t, out.HookSpecificOutput, "a full-mode checkpoint route answers through hookSpecificOutput")
	require.NotEmpty(t, instr, "a full-mode checkpoint route must render the focus instruction")

	require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(t, r.P.Root),
		"the PreCompact hook must have sealed exactly one artifact")
	entries := x4RequireManifestVerifies(t, r.P.Root)
	require.Len(t, entries, 1)
	require.Equal(t, x4v5SealedSeq, entries[0].Seq)

	art := x3v4ReadPreCompactArtifact(t, r.P.Root)
	require.Equal(t, x4v5SealedSeq, art.Seq)
	require.True(t, art.SpanInstruction, "IncrementalSpanInstruction is on by default")
	require.Contains(t, instr,
		fmt.Sprintf("A durable checkpoint (`.qompack/checkpoints/%04d.json`) fully covers the "+
			"session through turn %d", int(art.Seq), int(art.Frontier)),
		"the span paragraph must name the sealed artifact and the LOCAL frontier: %s", instr)
	require.Greater(t, int(art.Frontier), 0,
		"three closed and encoded segments must have advanced the frontier past turn 0")

	raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(r.P.Root), x4v5SealedSeq)))
	require.NoError(t, err)
	sealed, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err, "0001.json must parse as a versioned Checkpoint: %s", raw)
	require.Equal(t, sess, sealed.Session)
	require.Equal(t, x4v5SealedSeq, sealed.Seq)

	var pointsAtPath bool
	for _, fp := range sealed.Pointers.Files {
		if fp.Path == x4v5Path {
			pointsAtPath = true
		}
	}
	require.True(t, pointsAtPath,
		"the sealed checkpoint must carry a file pointer to %s — it is what item 6a matches the "+
			"`paths:` rule against; pointers=%+v", x4v5Path, sealed.Pointers.Files)
	return sealed
}

// x4v5StatePath is <root>/.qompack/state/rehydrate-<sess>.json, the rehydrator's persisted record.
func x4v5StatePath(root string, sess core.SessionID) string {
	return filepath.Join(paths.Of(root).State, "rehydrate-"+string(sess)+".json")
}

// x4v5ReadState reads the persisted rehydration record the `dropped` tool answers from.
func x4v5ReadState(t *testing.T, root string, sess core.SessionID) rehydrate.State {
	t.Helper()
	b, err := os.ReadFile(paths.Long(x4v5StatePath(root, sess)))
	require.NoError(t, err, "the rehydrate service must persist state/rehydrate-%s.json", sess)
	var st rehydrate.State
	require.NoError(t, json.Unmarshal(b, &st), "state/rehydrate-%s.json: %s", sess, b)
	return st
}

// x4v5RequireHeadingOrder asserts every §8.6 heading present in ac appears in normative order.
// Sections with nothing admitted are legitimately absent (render.go's itemText), so this checks
// ORDER among what was emitted rather than presence of all ten.
func x4v5RequireHeadingOrder(t *testing.T, ac string) {
	t.Helper()
	last := -1
	for _, h := range x4v5Headings {
		at := strings.Index(ac, h)
		if at < 0 {
			continue
		}
		require.Greater(t, at, last, "heading %q is out of §8.6 order in: %s", h, ac)
		last = at
	}
}

// x4v5RequireDropCoverage asserts the payload accounts for every persisted drop entry.
//
// When item 7 is rendered, each entry is either its own line or covered by the counted "… and N
// more; call dropped()" tail. When it is NOT rendered, that is the hard-cap re-truncation of ADR
// 0011 §18 — item 7 is the last discretionary item and the first the loop evicts, frozen in the
// 400-token `degraded` golden — and the payload must then say so (Degraded) and still carry the
// recovery path: item 8's affordance line naming dropped(), which is tier 1 and survives. The
// COMPLETE list lives in the state file either way, which is what the tool answers from.
func x4v5RequireDropCoverage(t *testing.T, ac string, st rehydrate.State) {
	t.Helper()
	dropped := st.Dropped
	if len(dropped) == 0 {
		return
	}
	if !strings.Contains(ac, "## 7. No longer in context") {
		require.True(t, st.Degraded,
			"item 7 may only be absent from a non-empty drop report when the hard-cap loop evicted "+
				"it, and that loop marks the result degraded: %s", ac)
		require.Contains(t, ac, "## 8. Retrieval",
			"with item 7 evicted, the affordance line is the model's only pointer at the loss: %s", ac)
		require.Contains(t, ac, "dropped()",
			"the affordance must name the tool that returns the complete list: %s", ac)
		return
	}
	unlisted := 0
	for _, e := range dropped {
		line := "- " + e.Kind
		if e.ID != "" {
			line += " " + e.ID
		}
		if !strings.Contains(ac, line) {
			unlisted++
		}
	}
	if unlisted > 0 {
		require.Contains(t, ac, "more; call dropped()",
			"%d persisted drop entries are not rendered in item 7, so the counted tail that points "+
				"at dropped() must be present: %s", unlisted, ac)
	}
}

// x4v5DroppedOverStdio asks the `dropped` tool through a real `qompack mcp` child — JSON-RPC over
// stdio, forwarded to the daemon's real `mcp` op — and returns the decoded body and the text block
// exactly as the child rendered it.
func x4v5DroppedOverStdio(t *testing.T, r *v4Rig) (x4v5DroppedBody, string) {
	t.Helper()
	child := mcpE2EStart(t, r.Bin, r.P)
	t13Initialize(t, child, 1)
	var body x4v5DroppedBody
	res := mcpE2ECall(t, child, 2, mcp.ToolDropped, map[string]any{}, &body)
	child.finish(t)
	return body, res.Content[0].Text
}

// x4v5DroppedViaCommand runs `qompack dropped --json` as a real process — SP-14's frontend over
// the shipped retrieval proxy — and returns the decoded body and the tool's own text block.
func x4v5DroppedViaCommand(t *testing.T, r *v4Rig) (x4v5DroppedBody, string) {
	t.Helper()
	stdout, stderr, code := Run(t, r.Bin, []string{"dropped", "--json"}, nil, e2eEnv(r.P))
	require.Equal(t, 0, code, "`qompack dropped --json` must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	var env commands.Envelope
	require.NoError(t, json.Unmarshal(stdout, &env), "stdout must be one commands.Envelope:\n%s", stdout)
	require.True(t, env.OK, "the envelope must report success: %s", stdout)
	require.Equal(t, "dropped", env.Command)

	var res commands.ToolResult
	require.NoError(t, json.Unmarshal(env.Data, &res), "the Data member must be a ToolResult: %s", env.Data)
	require.Equal(t, mcp.ToolDropped, res.Tool)
	require.False(t, res.IsError, "the frontend must pass the handler's answer through, not an error: %+v", res)
	require.Len(t, res.Content, 1)

	var body x4v5DroppedBody
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), &body), "the body must be JSON: %s", res.Content[0].Text)
	return body, res.Content[0].Text
}

// TestV5_PreCompactToRehydrateToDroppedRoundTrip is V5-VERIFY §4.4.
//
// Three arms. The first is the round trip under the shipped budget: full restoration and one
// identical drop report across the state file, the MCP tool and the slash-command frontend. The
// second runs the same session under a budget the payload cannot fit, which is where "bounded"
// and "coverage" are actually tested. The third is the negative control: the real injection kill
// switch severs the producer, and every downstream surface must report the absence honestly.
func TestV5_PreCompactToRehydrateToDroppedRoundTrip(t *testing.T) {
	// The clause the row's title carries: nothing here can depend on a PostCompact event because
	// the shipped plugin declares none. The manifest is the real one, read from the package that
	// renders it, so this fails the day someone adds the hook without revisiting the criterion.
	hooks := pluginmanifest.Default(core.Version).Hooks.Hooks
	_, hasPostCompact := hooks["PostCompact"]
	require.False(t, hasPostCompact, "the plugin manifest must declare no PostCompact hook; got %v", hooks)
	require.Contains(t, hooks, "PreCompact")
	require.Contains(t, hooks, "SessionStart")

	t.Run("full_budget_round_trip", func(t *testing.T) {
		p := x4v5Project(t, "")
		r := x4v5StartRig(t, p)
		x4v5SeedSession(t, r, x4v5Session)
		x4v5Seal(t, r, x4v5Session)

		// ── SessionStart(source=compact): the real rehydrator resolves THAT checkpoint ─────────
		ac := r.CompactStart(t, x4v5Session)
		require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")
		seq, tagged := x4InjectedSeq(t, ac)
		require.True(t, tagged, "the payload must open with the §8.5 injection tag: %s", ac)
		require.Equal(t, x4v5SealedSeq, seq,
			"the checkpoint the PreCompact sealed must be the one Latest resolves; seq=0 is the "+
				"no-checkpoint path: %s", ac)
		require.Contains(t, ac, checkpoint.InjectionCloseTag)
		require.Contains(t, ac, scProbePrefix, "the session.start route appends its contract probe")
		x4v5RequireHeadingOrder(t, ac)

		// Item 2: the verbatim intent from L0, not a paraphrase.
		require.Contains(t, ac, "## 2. Original user intent")
		require.Contains(t, ac, "> "+x4v5Intent, "item 2 quotes the L0 prompt verbatim: %s", ac)

		// Item 6a: the rule the checkpoint's file pointer pulled in, body WHOLE, and the nested
		// CLAUDE.md beside the pointed-at file — the two things the host does not restore.
		require.Contains(t, ac, "## 6a. Restored instructions")
		require.Contains(t, ac, "api-conventions.md", "the `paths: [\"src/api/**\"]` rule must be restored: %s", ac)
		require.Contains(t, ac, "paths: src/api/**", "the restored rule names its own scope: %s", ac)
		require.Contains(t, ac, "Every handler validates its request body with zod",
			"the rule body is restored whole, from disk: %s", ac)
		require.Contains(t, ac, "src/api/CLAUDE.md", "the nested CLAUDE.md beside the pointer is restored: %s", ac)
		require.Contains(t, ac, "All API handlers validate with zod.", "the nested body is restored verbatim: %s", ac)

		// Item 6b: names and one-line descriptions of every skill, never a body.
		require.Contains(t, ac, "## 6b. Skill index")
		for _, entry := range []string{
			"- code-review: Review the current diff for correctness bugs.",
			"- migration-runner: Run every pending database migration in order, rolling back on failure.",
			"- quick-fmt: Format this file.",
		} {
			require.Contains(t, ac, entry, "the skill index must carry %q: %s", entry, ac)
		}
		require.NotContains(t, ac, "Apply the next migration and verify its checksum.",
			"a skill BODY must never ride the index (G4.4)")

		// ── Bounded: the record the service persisted agrees with the payload and the config ───
		st := x4v5ReadState(t, p.Root, x4v5Session)
		require.Equal(t, x4v5Session, st.Session)
		require.Equal(t, x4v5SealedSeq, st.Seq, "the state file records the checkpoint seq it rehydrated from")
		require.False(t, st.Degraded, "a full-budget rehydration from a real checkpoint is not degraded")
		require.Equal(t, core.Tokens(p.Cfg.Runtime.Rehydrate.MaxTokens), st.Budget,
			"the budget recorded is runtime.rehydrate.maxTokens as configured, not a literal")
		require.LessOrEqual(t, st.Tokens, st.Budget, "Result.Tokens never exceeds the budget")
		est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
		require.LessOrEqual(t, est.EstimateString(scInjectedSpan(t, ac), tokens.ClassProse), st.Budget,
			"the injected span, re-priced by the supported estimator, stays within the configured bound")
		x4v5RequireDropCoverage(t, ac, st)

		// ── Coverage: one drop report, three surfaces, byte-identical ──────────────────────────
		viaMCP, mcpText := x4v5DroppedOverStdio(t, r)
		require.Nil(t, viaMCP.Available, "a wired drop reporter must not report itself unavailable: %+v", viaMCP)
		require.Equal(t, len(st.Dropped), viaMCP.Count)
		require.Equal(t, st.Dropped, viaMCP.Drops,
			"`dropped` over stdio must return the persisted report entry for entry, in order")

		viaCmd, cmdText := x4v5DroppedViaCommand(t, r)
		require.Equal(t, viaMCP.Drops, viaCmd.Drops, "the slash-command frontend must not diverge from the tool")
		require.Equal(t, mcpText, cmdText,
			"SP-13's tool and SP-14's frontend must render the byte-identical text block")

		// A second severing, through the file itself: with the persisted record gone the tool
		// must answer from disk again — an empty report — rather than from anything it cached.
		require.NoError(t, os.Remove(paths.Long(x4v5StatePath(p.Root, x4v5Session))))
		afterRemove, _ := x4v5DroppedOverStdio(t, r)
		require.Zero(t, afterRemove.Count,
			"with the state file removed `dropped` must report no entries; a non-zero count would "+
				"mean the identity assertion above was satisfied by a cache, not by the file: %+v", afterRemove)
		require.Empty(t, afterRemove.Drops)
	})

	t.Run("bounded_budget_reports_complete_drops", func(t *testing.T) {
		p := x4v5Project(t, fmt.Sprintf(`{"runtime":{"rehydrate":{"minTokens":%d,"maxTokens":%d}}}`,
			x4v5TightBudget, x4v5TightBudget))
		require.Equal(t, x4v5TightBudget, p.Cfg.Runtime.Rehydrate.MaxTokens,
			"the project config must have narrowed the rehydration budget")
		r := x4v5StartRig(t, p)
		x4v5SeedSession(t, r, x4v5BoundedSession)
		x4v5Seal(t, r, x4v5BoundedSession)

		ac := r.CompactStart(t, x4v5BoundedSession)
		require.NotEmpty(t, ac, "the bound is large enough to inject something")
		seq, tagged := x4InjectedSeq(t, ac)
		require.True(t, tagged)
		require.Equal(t, x4v5SealedSeq, seq)
		x4v5RequireHeadingOrder(t, ac)

		st := x4v5ReadState(t, p.Root, x4v5BoundedSession)
		require.Equal(t, core.Tokens(x4v5TightBudget), st.Budget)
		require.LessOrEqual(t, st.Tokens, st.Budget,
			"a budget too small to hold everything is still a hard cap: %d > %d", st.Tokens, st.Budget)
		est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
		require.LessOrEqual(t, est.EstimateString(scInjectedSpan(t, ac), tokens.ClassProse), st.Budget,
			"the injected span, re-priced, stays within the tightened bound")
		require.NotEmpty(t, st.Dropped,
			"forty tool pointers, a rule, a nested CLAUDE.md and three skills cannot fit in %d "+
				"tokens, so the rehydrator must have dropped something and said so", x4v5TightBudget)
		t.Logf("bounded arm: tokens=%d budget=%d degraded=%v dropped=%d item7_rendered=%v",
			st.Tokens, st.Budget, st.Degraded, len(st.Dropped), strings.Contains(ac, "## 7. No longer in context"))
		x4v5RequireDropCoverage(t, ac, st)

		// Every omission is classified: a drop entry without a kind cannot be asked for again.
		for _, e := range st.Dropped {
			require.NotEmpty(t, e.Kind, "a drop entry must carry its kind: %+v", e)
		}

		viaMCP, mcpText := x4v5DroppedOverStdio(t, r)
		require.Nil(t, viaMCP.Available, "%+v", viaMCP)
		require.Equal(t, st.Dropped, viaMCP.Drops,
			"`dropped` returns the COMPLETE persisted list even when item 7 could only count it")
		viaCmd, cmdText := x4v5DroppedViaCommand(t, r)
		require.Equal(t, viaMCP.Drops, viaCmd.Drops)
		require.Equal(t, mcpText, cmdText)
	})

	t.Run("negative_control_reinjection_disabled", func(t *testing.T) {
		p := x4v5Project(t, x4v5ReinjectionOffJSON)
		require.False(t, p.Cfg.Runtime.Migration.Reinjection.SessionStartCompact,
			"the project config must have turned the injection kill switch off")
		r := x4v5StartRig(t, p)
		env := e2eEnv(p)

		// The live half only: a registered session with a prompt and a few turns, and NO
		// PreCompact, so that the append-only conformance list below can seed 0001.json itself.
		obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x4v5ControlSession), env)
		obsRunHook(t, r.Bin, []string{"observe", "prompt"}, obsPromptPayload(t, p.Root, x4v5ControlSession, x4v5Intent), env)
		r.SeedTurns(t, x4v5ControlSession, "v5x04c", 2)

		// With the real switch off the rehydrator must inject NOTHING — no injection span, not
		// even an empty tagged wrapper — and must persist no record of a rehydration. What the
		// route still appends is SP-05's §12.1 contract probe: it is minted on every session.start
		// in ModeFull regardless of what the rehydrator said (internal/daemon/handlers.go), so the
		// honest assertion is "nothing but the probe", not "nothing at all".
		ac := r.CompactStart(t, x4v5ControlSession)
		require.NotContains(t, ac, "<!-- qompack:injected",
			"NEGATIVE CONTROL: runtime.migration.reinjection.sessionStartCompact=false must suppress "+
				"the injection span entirely; got: %s", ac)
		for _, line := range strings.Split(ac, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			require.True(t, strings.HasPrefix(strings.TrimSpace(line), scProbePrefix),
				"NEGATIVE CONTROL: nothing but the route's contract probe may reach additionalContext "+
					"with injection off; got line %q in: %s", line, ac)
		}
		require.NoFileExists(t, paths.Long(x4v5StatePath(p.Root, x4v5ControlSession)),
			"no rehydration ran, so no state file may claim one did")
		require.Empty(t, cpCheckpointArtifacts(t, p.Root), "nothing sealed a checkpoint in this arm")

		// Both downstream surfaces must report the ABSENCE — zero entries — never a fabricated
		// coverage, and never a tool error.
		viaMCP, mcpText := x4v5DroppedOverStdio(t, r)
		require.Zero(t, viaMCP.Count, "NEGATIVE CONTROL: no rehydration means no drop report: %+v", viaMCP)
		require.Empty(t, viaMCP.Drops)
		viaCmd, cmdText := x4v5DroppedViaCommand(t, r)
		require.Zero(t, viaCmd.Count, "%+v", viaCmd)
		require.Equal(t, mcpText, cmdText, "the two surfaces agree on the absence too")

		// Recording is untouched by the switch: the turns still reached the index.
		require.GreaterOrEqual(t, len(obsToolUseLines(p.Root)), 2,
			"the kill switch governs injection only; L0 capture must keep running")

		// The append-only invariant holds on the project this arm wrote to.
		p.AssertAppendOnly(t)
	})
}
