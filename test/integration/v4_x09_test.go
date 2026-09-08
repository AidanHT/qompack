// V4 §4.9 — the §11.3 growth guardrail holds with real checkpoints AND real ephemeral retrieval
// records resident, and the growth file the replay gate consumes is the one this run produced.
//
// It joins TestIntegration_RealStoreGrowthIsSublinear (the sublinear verdict over a real store) and
// TestIntegration_ReplayGateAcceptsRealGrowthFile (the CI gate over that file), and adds the two
// wave-3 residents the historical row never had: sealed checkpoints written by the real
// checkpoint.FileWriter over a real SourceSet, and ephemeral records written by the real MCP
// `expand` handler through the real store.
//
// The wider GC roots units B/C/M introduced (delta bases, the delivery-lease journal, migration and
// rollback material) are held LIVE throughout: nothing here collects, so the guardrail is measured
// with every retention root standing.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// x9v4Session is this row's session identity.
const x9v4Session = core.SessionID("sess-integration-v4-x09")

// x9v4CheckpointEvery seals a real checkpoint every N sampled turns, so several artifacts and their
// MANIFEST lines are resident while growth is measured.
const x9v4CheckpointEvery = 3

// x9v4Rig is the real collaborator set the walk drives.
type x9v4Rig struct {
	P      *testutil.Project
	Store  store.Store
	Writer *checkpoint.FileWriter
	Src    checkpoint.SourceSet
	Server mcp.Server
}

// x9v4Open composes the real store, DAG, ledger, pin store, checkpoint writer and MCP retrieval
// surface over one disposable project. Every collaborator is the production type.
func x9v4Open(t *testing.T) *x9v4Rig {
	t.Helper()

	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	s, err := store.Open(p.Root, p.Cfg, store.Deps{
		Chunker: chunk.New(chunk.FromConfig(p.Cfg)),
		Canon:   canon.Default(p.Cfg.Store.Canonicalize),
		Symbols: symbols.New(),
		Tokens:  tokens.NewExact(p.Cfg, tokens.DefaultCalibPath(), filepath.Join(l.State, growthChunkCacheFile)),
		Redact:  redact.New(p.Cfg),
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: s, Graph: g, Session: x9v4Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	pinStore, err := pins.OpenWith(p.Root, p.Log, nil, p.Clock)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, nil, p.Clock)
	require.NoError(t, err)

	src := checkpoint.SourceSet{
		Store: s, Segments: s.Segments(), Ledger: led, Pins: pinStore, Graph: g,
		Grammar: grammar.New(),
		Tokens:  tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root),
	}
	require.NoError(t, src.Validate(), "the SourceSet must be complete before Begin is called")
	w.SetSources(src)

	srv := mcp.NewServer(mcp.ServerName, "v4-x09", logging.Nop())
	require.NoError(t, mcp.RegisterAll(srv, mcp.ToolDeps{
		Store: s, Cfg: p.Cfg, ProjectRoot: p.Root, DisableWhy: true,
	}))

	return &x9v4Rig{P: p, Store: s, Writer: w, Src: src, Server: srv}
}

// x9v4Expand drives the real `expand` handler, which writes an ephemeral-at-birth ToolUseRecord
// into the same store the growth walk is measuring.
func x9v4Expand(t *testing.T, r *x9v4Rig, id core.ToolUseID) {
	t.Helper()
	args, err := json.Marshal(map[string]any{"tool_use_id": string(id)})
	require.NoError(t, err)
	for _, tool := range r.Server.Tools() {
		if tool.Name != mcp.ToolExpand {
			continue
		}
		_, hErr := tool.Handler(context.Background(), mcp.Request{
			Session: x9v4Session, Name: mcp.ToolExpand, Args: args,
		})
		require.NoError(t, hErr, "expand must not raise a protocol error")
		return
	}
	require.FailNow(t, "the registered tool set must include expand")
}

// x9v4Walk is growthWalk with the two wave-3 residents added. unique selects the negative control:
// with it every turn's payload is entirely fresh, so nothing deduplicates and growth must become
// linear.
func x9v4Walk(t *testing.T, r *x9v4Rig, unique bool) []eval.StatsSample {
	t.Helper()
	ctx := context.Background()

	opts := store.PutOptions{
		Tool:  "Bash",
		Path:  growthPath,
		Canon: canon.OptionsFrom(r.P.Cfg.Store.Canonicalize, false),
	}

	lines := make([]string, growthLines)
	for i := range lines {
		lines[i] = growthLine(i, 0)
	}

	var samples []eval.StatsSample
	sealed := 0
	for turn := 1; turn <= growthTurns; turn++ {
		if unique {
			// Every line fresh every turn: the store has nothing to deduplicate against.
			for i := range lines {
				lines[i] = growthLine(i, turn)
			}
		} else {
			growthMutate(lines, turn)
		}
		body := []byte(strings.Join(lines, ""))
		require.Len(t, body, growthPayloadBytes)

		res, err := r.Store.PutBytes(ctx, body, opts)
		require.NoError(t, err, "turn %d", turn)

		if !growthSampleTurns[turn] {
			continue
		}

		// A real capture, then a real ephemeral retrieval record over it.
		id := core.ToolUseID(fmt.Sprintf("toolu_v4x09_%03d", turn))
		require.NoError(t, r.Store.RecordToolUse(ctx, store.ToolUseRecord{
			ID: id, Session: x9v4Session, Tool: "Bash", Path: growthPath,
			Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
			Turn: core.TurnIndex(turn),
		}))
		x9v4Expand(t, r, id)

		// A real sealed checkpoint every few samples, artifacts and MANIFEST lines and all.
		if sealed%x9v4CheckpointEvery == 0 {
			d, err := r.Writer.Begin(ctx, x9v4Session, 0, r.Src)
			require.NoError(t, err, "Begin at turn %d", turn)
			_, err = r.Writer.Finalize(ctx, d, core.Tokens(r.P.Cfg.Checkpoint.BudgetTokens))
			require.NoError(t, err, "Finalize at turn %d", turn)
		}
		sealed++

		st, statsErr := r.Store.Stats(ctx)
		require.NoError(t, statsErr)
		samples = append(samples, eval.StatsSample{
			Turn:       core.TurnIndex(turn),
			Objects:    st.Objects,
			Bytes:      st.Bytes,
			RawBytes:   st.RawBytes,
			DedupRatio: st.DedupRatio,
		})
	}
	return samples
}

// TestV4_GrowthGuardrailWithCheckpointsAndEphemerals is V4-VERIFY §4.9.
//
// The negative control is the second arm: the SAME pipeline, with the same checkpoints and the same
// ephemeral records, fed content that cannot deduplicate. The guardrail must then FAIL. Without it,
// "growth is sublinear" would also be reported by a gate that says sublinear to everything.
func TestV4_GrowthGuardrailWithCheckpointsAndEphemerals(t *testing.T) {
	// Built before testutil.NewProject redirects HOME for the process; see initialEnv.
	driver := buildReplayDriver(t)

	r := x9v4Open(t)
	samples := x9v4Walk(t, r, false)
	require.Len(t, samples, growthSampleCount)

	// The residents are really resident: sealed artifacts on disk, ephemeral records in the index.
	l := paths.Of(r.P.Root)
	entries, err := paths.ReadManifest(l)
	require.NoError(t, err)
	require.NotEmpty(t, entries,
		"real checkpoints must have been sealed during the walk, or 'with checkpoints' is a claim "+
			"about nothing")

	recs, err := r.Store.ToolUsesByPath(context.Background(), growthPath, 200)
	require.NoError(t, err)
	ephemerals := 0
	for _, rec := range recs {
		if rec.Ephemeral {
			ephemerals++
		}
	}
	require.Positive(t, ephemerals,
		"real ephemeral retrieval records must have accumulated during the walk; records=%d", len(recs))

	// ── The guardrail, with both resident ────────────────────────────────────────────────────────
	got := eval.CheckSublinearGrowth(samples)
	t.Logf("v4 §4.9 growth with %d checkpoints and %d ephemeral records: exponent %.4f over %d "+
		"samples spanning %.1fx raw bytes (sublinear=%t)",
		len(entries), ephemerals, got.Exponent, got.Samples, got.RawSpan, got.Sublinear)
	require.Empty(t, got.Reason, "a conclusive verdict explains nothing")
	require.True(t, got.Sublinear,
		"store growth must stay sublinear with checkpoints and ephemerals both resident")
	require.Less(t, got.Exponent, 1.0,
		"an exponent of 1 is dedup achieving nothing: stored bytes tracking raw bytes one for one")

	// ── The gate reads the file THIS run produced ────────────────────────────────────────────────
	dir := t.TempDir()
	full := filepath.Join(dir, "growth-v4x09.json")
	growthWriteJSON(t, full, samples)

	roundTrip, err := json.Marshal(samples)
	require.NoError(t, err)
	onDisk := growthFieldSet(t, roundTrip)
	require.NotEmpty(t, onDisk, "the growth file must carry a field set the driver can read")

	// The gate's aggregate EXIT CODE is deliberately not asserted here. It covers every replay score
	// gate on this tree, and the inherited row that does assert it —
	// TestIntegration_ReplayGateAcceptsRealGrowthFile — is already red on this base for reasons that
	// have nothing to do with growth (baseline score sign-offs). What this row owns is the growth
	// verdict, and the gate prints it.
	_, stdout, stderr := runReplayGate(t, driver, "--growth", full)
	require.Contains(t, stdout, "sublinear=true",
		"the gate must report the verdict it reached over THIS run's samples\nstderr:\n%s", stderr)

	// ── NEGATIVE CONTROL: the same pipeline over content that cannot deduplicate ─────────────────
	rc := x9v4Open(t)
	linear := x9v4Walk(t, rc, true)
	require.Len(t, linear, growthSampleCount)

	bad := eval.CheckSublinearGrowth(linear)
	t.Logf("v4 §4.9 negative control: exponent %.4f (sublinear=%t)", bad.Exponent, bad.Sublinear)
	require.False(t, bad.Sublinear,
		"NEGATIVE CONTROL: with nothing to deduplicate the guardrail MUST fail. If it passes here, "+
			"the sublinear verdict above says nothing about the store: exponent %.4f", bad.Exponent)

	badFile := filepath.Join(dir, "growth-v4x09-linear.json")
	growthWriteJSON(t, badFile, linear)
	_, badStdout, badStderr := runReplayGate(t, driver, "--growth", badFile)
	require.NotContains(t, badStdout, "sublinear=true",
		"NEGATIVE CONTROL: the gate must not report a linear growth file as sublinear\nstderr:\n%s",
		badStderr)
	require.Contains(t, badStdout+badStderr, "sublinear=false",
		"and it must say so explicitly, so a CI log names the reason\nstdout:\n%s\nstderr:\n%s",
		badStdout, badStderr)
}
