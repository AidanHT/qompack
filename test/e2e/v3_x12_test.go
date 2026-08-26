package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is V3-VERIFY §5 X12: the full committed corpus (testdata/sessions/synthetic/, 24
// sessions) driven end to end through the SP-08 observer over a real store and DAG, with every
// record_eliminated call the session carries ingested into a real negknow ledger, then
// OnSessionEnd. It is the broadest cross-component smoke surface in the repo and the cheapest
// place to catch a paths.Key / normalization mismatch between packages.
//
// Seams: eval corpus -> observer -> store/dag/sketch -> negknow -> eval metrics
// (SP-02 + SP-08 + SP-06 + SP-07 + SP-03 + SP-09).
//
// Where a wave-3 component would sit in the flow — the MCP server that would carry a
// record_eliminated call to the ledger — the test composes the seam itself: each such tool call's
// arguments are handed to negknow.Maintainer.IngestMCP directly, per §5's Rule.

// x12ToolRecordEliminated is the tool name eval.Synthesize gives an injected elimination
// (internal/eval's unexported toolRecordEliminated, spelled here for the same reason
// counterSupersededName is).
const x12ToolRecordEliminated = "record_eliminated"

// x12SessionEndHook is the SessionEnd hook event name as this package's payloads spell it.
const x12SessionEndHook = "SessionEnd"

// x12ReadHeavyShape is CorpusSpecs' read-heavy shape name: the sessions the §10 Phase 1 exit
// ratio (phase1ExitRatio) is corroborated on, per the X12 row's H15 clause.
const x12ReadHeavyShape = "read-heavy"

// x12CorpusSessions is the committed corpus size: eight shapes, three seeds each.
const x12CorpusSessions = 24

// x12TriedBloomName is negknow's on-disk bloom filename under sketches/.
const x12TriedBloomName = "tried.bloom"

// The two dependency files the generator's DependencyChangeAt turns write
// (internal/eval/synth.go synthDependencyFiles) — the files SP-09's staleness path keys on.
const (
	x12DepCompose = "docker-compose.yml"
	x12DepLock    = "package-lock.json"
)

// The v0 baselines this test seeds the store's file-version history with before driving a
// session, standing in for what the observer would have recorded in an earlier session. Their
// bytes are this test's own and match no committed corpus capture, so a session's own Write of
// the same path (which serves corpus bytes) is a REAL content change: the elimination records
// whose DependsOn baseline predates that write are exactly the ones RefreshStaleness must flip.
const (
	x12ComposeV0 = "services:\n  pgbouncer:\n    image: pgbouncer:1.18\n# x12 staleness baseline v0\n"
	x12LockV0    = "{\"name\":\"x12\",\"lockfileVersion\":3}\n"
)

// x12DepFiles orders the seeding deterministically (a map would randomize PutBytes order).
var x12DepFiles = []struct{ path, content string }{
	{x12DepCompose, x12ComposeV0},
	{x12DepLock, x12LockV0},
}

// x12Run is everything one driven session leaves behind that the aggregate pass reads.
type x12Run struct {
	id        string
	shape     string
	depChange bool
	toolCalls int
	prompts   int
	elimCalls int
	stats     store.Stats
	graph     dag.GraphStats
	health    negknow.Health
	flipped   int
	bloom     bool
}

// x12SpecByID maps a committed session's ID (SynthesizeNamed's "<shape>-<seed>") back to its
// NamedSpec, which is the only place DependencyChangeAt survives — Session itself has no field
// for it.
func x12SpecByID() map[string]eval.NamedSpec {
	out := map[string]eval.NamedSpec{}
	for _, n := range eval.CorpusSpecs() {
		out[n.Shape+"-"+strconv.FormatInt(n.Seed, 10)] = n
	}
	return out
}

// x12ToolCalls flattens s's tool calls in exactly the order eventsFor emits their PostToolUse
// events (one per ToolCall of every non-user turn), so the drive loop can walk the two in
// lockstep and read the record_eliminated arguments that toolInputFor does not carry into the
// hook event.
func x12ToolCalls(s eval.Session) []eval.ToolCall {
	var out []eval.ToolCall
	for _, turn := range s.Turns {
		if turn.Role == "user" {
			continue
		}
		out = append(out, turn.ToolCalls...)
	}
	return out
}

// x12ElimArgs reads one record_eliminated call's arguments, as callFor spells them.
func x12ElimArgs(t *testing.T, raw json.RawMessage) (target, approach, reason string) {
	t.Helper()
	var a struct {
		Target   string `json:"target"`
		Approach string `json:"approach"`
		Reason   string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(raw, &a))
	require.NotEmpty(t, a.Target, "a record_eliminated call must carry a target")
	require.NotEmpty(t, a.Approach, "a record_eliminated call must carry an approach")
	return a.Target, a.Approach, a.Reason
}

// x12SeedDepBaselines writes a v0 file version for both dependency files under the SAME
// paths.Norm+Key spelling the observer stores under (tooluse.go step 4/7), so negknow's
// resolveDeps finds a staleness baseline for every elimination regardless of where in the
// session it lands.
func x12SeedDepBaselines(t *testing.T, ctx context.Context, st store.Store, root string, clk *testutil.FakeClock) {
	t.Helper()
	for _, dep := range x12DepFiles {
		n, err := paths.Norm(root, dep.path)
		require.NoError(t, err)
		key := paths.Key(n)
		res, err := st.PutBytes(ctx, []byte(dep.content), store.PutOptions{Tool: "FileRead", Path: key})
		require.NoError(t, err)
		require.NoError(t, st.AppendFileVersion(ctx, key, store.FileVersion{
			TS:    core.UnixMilli(clk.Now().UnixMilli()),
			Root:  res.Root.Hash,
			Turn:  0,
			Bytes: res.Root.RawBytes,
		}))
	}
}

// x12Drive materializes s's hook events with eventsFor, drives them in order through a real
// observer over a fresh testutil.NewProject, ingests every record_eliminated call into a real
// negknow ledger (opened lazily at the first one, so sketches/tried.bloom exists exactly for
// the sessions that carried eliminations), calls OnSessionEnd, and then runs the ingest-layer
// staleness assertions the X12 row names.
func x12Drive(t *testing.T, s eval.Session, spec eval.NamedSpec, c corpus) *x12Run {
	t.Helper()
	ctx := context.Background()

	p := testutil.NewProject(t)
	st := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	x12SeedDepBaselines(t, ctx, st, p.Root, p.Clock)

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root, Cfg: p.Cfg, Store: st, Graph: g,
		Touch:   sketch.NewCMS(phase1CMSEpsilon, phase1CMSDelta),
		Explore: sketch.NewHLL(phase1HLLRegisters),
		Hot:     sketch.NewMisraGries(phase1MGCounters),
		Log:     p.Log, Metrics: obs.New(p.Clock), Clock: p.Clock,
	})
	require.NoError(t, err)

	events := eventsFor(s, c)
	require.NotEmpty(t, events, "session %s produced no events", s.ID)
	calls := x12ToolCalls(s)

	var (
		led   negknow.Ledger
		maint negknow.Maintainer
		recs  []negknow.Record
		seen  = map[string]bool{}
	)
	openLedger := func() {
		if led != nil {
			return
		}
		var openErr error
		led, openErr = negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
			Store: st, Graph: g, Session: core.SessionID(s.ID),
			Log: p.Log, Clock: p.Clock,
		})
		require.NoError(t, openErr)
		t.Cleanup(func() { _ = led.Close() })
		m, ok := led.(negknow.Maintainer)
		require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")
		maint = m
	}

	toolCalls, prompts, elimCalls, ci := 0, 0, 0, 0
	for _, e := range events {
		p.Clock.Advance(time.Second)
		switch e.HookEventName {
		case hookUserPromptSubmit:
			prompts++
			_, err := obsv.OnUserPrompt(ctx, e)
			require.NoError(t, err)
		case hookPostToolUse:
			require.Less(t, ci, len(calls), "eventsFor emitted more PostToolUse events than the session has tool calls")
			tc := calls[ci]
			ci++
			require.Equal(t, tc.ID, e.ToolUseID, "the turn walk and eventsFor disagree about call order")

			toolCalls++
			_, err := obsv.OnToolUse(ctx, e)
			require.NoError(t, err)

			if tc.Name != x12ToolRecordEliminated {
				continue
			}
			elimCalls++
			openLedger()
			target, approach, reason := x12ElimArgs(t, tc.Args)
			rec, warns, err := maint.IngestMCP(ctx, negknow.MCPArgs{
				Target: target, Approach: approach, Reason: reason,
				DependsOn: []string{x12DepCompose, x12DepLock},
			})
			require.NoError(t, err)
			require.Empty(t, warns, "both dependency files were seeded, so nothing should be warned about")
			require.Len(t, rec.DependsOn, len(x12DepFiles), "the record must carry a staleness baseline for both dependency files")
			if !seen[rec.ID] {
				seen[rec.ID] = true
				recs = append(recs, rec)
			}
		default:
			t.Fatalf("eventsFor emitted an unexpected hook event %q", e.HookEventName)
		}
	}
	require.Equal(t, len(calls), ci, "every synthesized tool call must have been driven")

	_, err = obsv.OnSessionEnd(ctx, hookio.Event{
		HookEventName: x12SessionEndHook, SessionID: core.SessionID(s.ID),
	})
	require.NoError(t, err)

	// The ingest-layer staleness pass (the X12 row's I13-at-the-ingest-layer clause). The
	// expected flip set is derived from the production seam itself — store.ChangedSince over
	// each record's own DependsOn — and RefreshStaleness must flip exactly that set.
	flipped := 0
	if led != nil {
		expected := map[string]bool{}
		for _, r := range recs {
			changed, err := st.ChangedSince(ctx, r.DependsOn)
			require.NoError(t, err)
			if len(changed) > 0 {
				expected[r.ID] = true
			}
		}

		got, err := led.RefreshStaleness(ctx, st)
		require.NoError(t, err)
		flipped = len(got)
		gotSet := map[string]bool{}
		for _, id := range got {
			gotSet[id] = true
		}
		require.Equal(t, expected, gotSet,
			"RefreshStaleness must flip exactly the records store.ChangedSince reports a changed dependency for")

		if !x12HasDepChange(spec) {
			require.Empty(t, got,
				"session %s has no DependencyChangeAt turn, so no dependency content ever changed and nothing may flip", s.ID)
		}

		for _, r := range recs {
			if !expected[r.ID] {
				continue
			}
			cur, err := led.Get(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, negknow.StatusStale, cur.Status,
				"an elimination whose dependency changed must be stale after RefreshStaleness")
			ans, err := led.Query(ctx, r.Target, r.Approach, negknow.ScopeSession)
			require.NoError(t, err)
			require.NotEqual(t, negknow.AnswerActive, ans.State,
				"I13 at the ingest layer: Query must never answer active for a dependency-changed elimination")
		}
	}

	stats, err := st.Stats(ctx)
	require.NoError(t, err)

	health := negknow.Health{}
	if led != nil {
		health = led.Health()
	}

	_, bloomErr := os.Stat(paths.Long(filepath.Join(paths.Of(p.Root).Sketches, x12TriedBloomName)))

	return &x12Run{
		id: s.ID, shape: spec.Shape, depChange: x12HasDepChange(spec),
		toolCalls: toolCalls, prompts: prompts, elimCalls: elimCalls,
		stats: stats, graph: g.Stats(), health: health,
		flipped: flipped, bloom: bloomErr == nil,
	}
}

// x12HasDepChange reports whether spec injects any dependency-change turn.
func x12HasDepChange(spec eval.NamedSpec) bool { return len(spec.Spec.DependencyChangeAt) > 0 }

// TestV3_FullCorpusIngestThroughObserverAndLedger is V3-VERIFY §5 X12.
func TestV3_FullCorpusIngestThroughObserverAndLedger(t *testing.T) {
	root, err := moduleRoot()
	require.NoError(t, err)
	dir := filepath.Join(root, "testdata", "sessions", "synthetic")
	sessions, err := eval.New(eval.Options{}).Load(dir)
	require.NoError(t, err)
	require.Len(t, sessions, x12CorpusSessions, "the committed corpus must hold all %d sessions", x12CorpusSessions)

	specs := x12SpecByID()
	c := loadCorpus(t)

	var runs []*x12Run
	for _, s := range sessions {
		spec, ok := specs[s.ID]
		require.True(t, ok, "session %s has no eval.CorpusSpecs entry to recover DependencyChangeAt from", s.ID)

		t.Run(s.ID, func(t *testing.T) {
			r := x12Drive(t, s, spec, c)

			// The per-session X12 expected outputs.
			require.Greater(t, r.stats.DedupRatio, 1.0,
				"every session must dedup at better than 1:1 against a real store")
			// The X12 row says "Stats().ToolUses equals the session's tool call count"; in the
			// shipped store the SAME index also holds one record per non-empty user prompt
			// (observer/prompt.go RecordToolUse, §8.1 artifact (a)), so the exact production
			// reading of that row is one record per driven tool call PLUS one per user prompt —
			// still an equality, never a floor.
			require.Equal(t, r.toolCalls+r.prompts, r.stats.ToolUses,
				"Stats().ToolUses must equal the session's driven tool call count plus its user prompt count")
			require.GreaterOrEqual(t, r.graph.Nodes, 2*r.toolCalls,
				"the DAG must hold at least two nodes per tool call")
			require.Equal(t, r.elimCalls > 0, r.bloom,
				"sketches/tried.bloom must exist exactly for sessions that carried eliminations")

			runs = append(runs, r)
		})
	}
	require.Len(t, runs, len(sessions), "every session must have driven to completion without failing")

	// The aggregate table the X12 row asks to log, plus the two corpus-wide guards.
	atExit, flips := 0, 0
	readHeavyBest := 0.0
	for _, r := range runs {
		t.Logf("%-28s calls=%4d elims=%2d ratio=%7.4f objects=%6d dag{nodes=%6d edges=%6d} ledger{records=%2d active=%2d stale=%2d fill=%.4f} flipped=%d",
			r.id, r.toolCalls, r.elimCalls, r.stats.DedupRatio, r.stats.Objects,
			r.graph.Nodes, r.graph.Edges,
			r.health.Records, r.health.Active, r.health.Stale, r.health.FillRatio, r.flipped)
		if r.stats.DedupRatio >= phase1ExitRatio {
			atExit++
		}
		if r.shape == x12ReadHeavyShape {
			t.Logf("read-heavy ratio: %s = %.4f", r.id, r.stats.DedupRatio)
			if r.stats.DedupRatio > readHeavyBest {
				readHeavyBest = r.stats.DedupRatio
			}
		}
		if r.depChange {
			flips += r.flipped
		}
	}
	t.Logf("sessions with dedup ratio >= %.1f: %d / %d", phase1ExitRatio, atExit, len(runs))

	require.GreaterOrEqual(t, readHeavyBest, phase1ExitRatio,
		"at least one read-heavy session must reach the §10 exit ratio, corroborating H15 on independently-generated data")
	require.Positive(t, flips,
		"the dependency-change sessions must flip at least one elimination stale, or the staleness pass proved nothing")
}
