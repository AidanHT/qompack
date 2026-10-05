package checkpoint_test

// Finalize's behaviour suite: the immutability, manifest and pointer-validation rows of
// plans/V4-SP-10-checkpointer-l4.md §13. It reuses writer_test.go's fx fixture, which already
// supplies real store/graph/ledger backends over a temp project.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/checkpoint/checkpointtest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// finalizeBudget is wide enough that nothing is truncated, so these rows assert Finalize's own
// behaviour rather than Truncate's. The truncation rows live in truncate_test.go.
const finalizeBudget = core.Tokens(200000)

// seedDraft builds a draft with one closed, encoded segment behind it, which is the minimum shape
// Finalize needs to produce a non-trivial artifact.
func seedDraft(t *testing.T, f *fx) *checkpoint.Draft {
	t.Helper()
	f.prompt(0, "Fix the intermittent 500s on the refresh endpoint.", true)
	f.tool("toolu_finalize_0001", 1, "Read", "src/auth.ts", "export function refreshToken() {}", false)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	f.advance(d, 1)
	return d
}

func TestFinalizeWritesAnImmutableArtifact(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), ref.Seq, "the first checkpoint of a project is seq 1")

	st, err := os.Stat(paths.Long(ref.Path))
	require.NoError(t, err)
	// Immutability is the file mode, not a convention: paths.CreateNew chmods to 0444, and every
	// write bit must be clear or a later process could edit a sealed checkpoint in place.
	require.Zero(t, st.Mode().Perm()&0o222,
		"a finalized checkpoint carries no write bit; got %v", st.Mode().Perm())

	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), ref.Bytes, "Ref.Bytes is the artifact's real size")
	require.Equal(t, core.Hash(sha256.Sum256(raw)), ref.SHA256,
		"Ref.SHA256 is the undomained digest of the bytes on disk")
}

func TestFinalizeAppendsAManifestLineThatVerifies(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Len(t, entries, 1, "one Finalize appends exactly one manifest line")

	e := entries[0]
	require.Equal(t, ref.Seq, e.Seq)
	require.Equal(t, ref.Bytes, e.Bytes)

	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	// The whole point of the manifest line is that it can be checked against the file. If these
	// two ever disagree the Reader must refuse the artifact, so the agreement is asserted here at
	// the moment it is created rather than only where it is enforced.
	require.Equal(t, core.Hash(sha256.Sum256(raw)).String(), e.SHA256)
}

func TestFinalizeArtifactUnmarshalsAsTheDraftItSealed(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	wantSeq := d.Seq()

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	require.Equal(t, checkpoint.SchemaVersion, cp.Version)
	require.Equal(t, f.sess, cp.Session)
	require.Equal(t, wantSeq, cp.Seq)
	require.Contains(t, cp.EncodedSegments, core.SegmentID(1),
		"the segment Advance encoded must appear in the sealed artifact")
	require.NotEmpty(t, cp.Created, "Created is stamped at Finalize, not left zero")
}

func TestFinalizeRetiresTheDraftAndOpensItsSuccessor(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	// The successor is what keeps the NEXT residual span O(delta): frontier advancement resumes on
	// the very next idle tick instead of starting from zero again after every compaction.
	succ := f.w.DraftFor(f.sess)
	require.NotNil(t, succ, "Finalize opens a successor draft for the same session")
	require.NotEqual(t, d, succ, "the successor is a new draft, not the retired one")
	require.Greater(t, int(succ.Seq()), int(ref.Seq),
		"the successor claims a sequence number above the one just sealed")
	require.Contains(t, f.w.OpenDrafts(), f.sess)
}

func TestFinalizeSecondCallClaimsTheNextSequence(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	first, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	f.closedSeg(2, 4, 7)
	second := f.w.DraftFor(f.sess)
	require.NotNil(t, second)
	f.advance(second, 2)

	ref2, err := f.w.Finalize(f.ctx(), second, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, first.Seq+1, ref2.Seq, "sequence numbers are dense and increasing")
	require.NotEqual(t, first.Path, ref2.Path)

	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

func TestFinalizeRefusesANilDraft(t *testing.T) {
	f := newFx(t)
	_, err := f.w.Finalize(f.ctx(), nil, finalizeBudget)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil draft")
}

// TestFinalizeRefusesAnAbortedDraft is checkpointtest's "abort_writes_nothing" stated where it can
// be read: an aborted draft must never produce an artifact.
//
// Abort drops the draft from the registry and deletes its file, but the caller may still be holding
// the pointer — and the sequence number that draft carried has since been handed to a live one. A
// Finalize on it would write a checkpoint nobody asked for, numbered the same as one that is about
// to be sealed for real.
func TestFinalizeRefusesAnAbortedDraft(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	require.NoError(t, f.w.Abort(d))

	_, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.ErrorIs(t, err, checkpoint.ErrDraftSealed)
	require.NoFileExists(t, paths.Long(paths.CheckpointPath(paths.Of(f.p.Root), 1)),
		"an aborted draft writes no artifact at all")

	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Empty(t, entries, "and indexes nothing")
}

// TestFinalizeReopensTheDraftWhenTheArtifactNeverLands pins the other half of the seal.
//
// Finalize seals the draft inside its snapshot's critical section so a concurrent Advance cannot
// add content to an artifact whose bytes are already decided. But a Finalize that FAILS before the
// artifact is committed has decided nothing, and leaving the draft sealed would strand the session:
// the cadence would retry forever against a draft that could no longer accept a segment.
func TestFinalizeReopensTheDraftWhenTheArtifactNeverLands(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	f.closedSeg(2, 4, 7)

	// A directory where the artifact belongs: CreateNew cannot write it, and the failure is not an
	// O_EXCL collision, so Finalize gives up.
	l := paths.Of(f.p.Root)
	blocked := paths.CheckpointPath(l, d.Seq())
	require.NoError(t, os.MkdirAll(paths.Long(blocked), 0o700))

	_, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.Error(t, err)
	require.NotErrorIs(t, err, checkpoint.ErrDraftSealed)

	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{2})
	require.NoError(t, err, "a write that never landed must not cost the draft its ability to encode")
	require.Equal(t, 2, d.EncodedCount())
}

func TestFinalizeRemovesPointersThatNoLongerResolve(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Trace the pool timeout.", true)
	// A file pointer to a path that was never created: ValidatePointers must classify it as
	// missing, and Finalize must then keep it out of the sealed artifact. A pointer that cannot be
	// re-read is worse than no pointer, because the reader would spend a turn discovering that.
	f.tool("toolu_finalize_gone", 1, "Read", "src/deleted-before-finalize.ts", "gone", false)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	f.advance(d, 1)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	for _, fp := range cp.Pointers.Files {
		require.NotEqual(t, "src/deleted-before-finalize.ts", fp.Path,
			"an unresolvable file pointer must not survive into the artifact")
	}
	// And the removal is reported rather than silent: a reader has to be able to tell the
	// difference between "this was never here" and "this was dropped".
	var kinds []string
	for _, dr := range cp.Dropped {
		kinds = append(kinds, dr.Kind)
	}
	require.Contains(t, strings.Join(kinds, ","), "pointer_",
		"the drop report names the pointer that was removed; got %v", kinds)
}

func TestFinalizeKeepsADirtyPointer(t *testing.T) {
	f := newFx(t)
	// A dirty file is precisely the file the agent is working on. pointer_dirty is informational
	// and must NOT remove the pointer -- dropping it would discard the most relevant pointer in
	// the set, which is the opposite of what importance ordering is for.
	p := filepath.Join(f.p.Root, "src", "live.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("export const live = 1\n"), 0o600))

	f.prompt(0, "Edit the live file.", true)
	f.tool("toolu_finalize_dirty", 1, "Read", "src/live.ts", "export const live = 1\n", false)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	f.advance(d, 1)

	// Change it on disk after the pointer was recorded, so its hash no longer matches.
	require.NoError(t, os.WriteFile(p, []byte("export const live = 2\n"), 0o600))

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	var found bool
	for _, fp := range cp.Pointers.Files {
		if fp.Path == "src/live.ts" {
			found = true
		}
	}
	require.True(t, found, "a dirty pointer survives; only missing and invalid ones are removed")
}

func TestFinalizeMaterializesPins(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	before := f.pins.materialized

	_, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	require.Greater(t, f.pins.materialized, before,
		"Finalize refreshes the materialized pins view, so invariants.json never lags a sealed checkpoint")
}

// TestFinalizeSealsAPinMadeAfterBegin is F-UAT05-2's second half. A draft seeds its invariants when
// it is begun, and a seal begins its successor at once, so the draft a compaction seals was usually
// begun long before: a pin made in between must still be in the checkpoint, because tier 1 is the
// set pinned when the checkpoint is SEALED, not when its draft happened to open.
func TestFinalizeSealsAPinMadeAfterBegin(t *testing.T) {
	f := newFx(t)
	f.pins.invs = []pins.Invariant{{ID: "inv_before000000", Text: "pinned before the draft", Source: "user", Pinned: 1}}
	d := seedDraft(t, f)
	f.pins.invs = append(f.pins.invs,
		pins.Invariant{ID: "inv_after0000000", Text: "pinned while the draft was open", Source: "user", Pinned: 2})

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, f.pins.invs, cp.Invariants, "the sealed tier 1 is the pin set at the seal, verbatim")
	for _, dr := range cp.Dropped {
		require.NotEqual(t, "invariants", dr.Kind, "a seal that read its pins names no pin drop: %+v", dr)
	}
}

// TestFinalizeNamesPinsItCouldNotReread: when the pin set cannot be read at the seal, the artifact
// keeps the invariants its draft was begun with — they were pinned and nothing says otherwise — and
// names the gap, so a pin made since is a named drop rather than a silent absence.
func TestFinalizeNamesPinsItCouldNotReread(t *testing.T) {
	f := newFx(t)
	begun := []pins.Invariant{{ID: "inv_before000000", Text: "pinned before the draft", Source: "user", Pinned: 1}}
	f.pins.invs = begun
	d := seedDraft(t, f)
	f.pins.allErr = fmt.Errorf("pins: reading the invariant log: %w", os.ErrPermission)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err, "an unreadable pin set must not cost the checkpoint")
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, begun, cp.Invariants, "the draft's invariants are kept, never emptied")
	var named []checkpoint.DropEntry
	for _, dr := range cp.Dropped {
		if dr.Kind == "invariants" {
			named = append(named, dr)
		}
	}
	require.Len(t, named, 1, "exactly one drop names the unread pin set: %+v", cp.Dropped)
	require.Equal(t, "pins", named[0].ID)
	require.Contains(t, named[0].Detail, "permission denied")
}

func TestFinalizeArtifactIsCanonicalJSON(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)

	// The bytes on disk must be exactly what Marshal produces for that content -- that is what
	// makes a checkpoint's digest a function of its content alone, and what lets the Reader verify
	// it by re-hashing rather than by re-serializing.
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	round, err := checkpoint.Marshal(cp)
	require.NoError(t, err)
	require.Equal(t, string(raw), string(round), "the artifact is canonical Marshal output")

	require.True(t, strings.HasSuffix(string(raw), "}\n"), "exactly one trailing newline")
	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	require.Contains(t, probe, "version")
}

// ── performance ───────────────────────────────────────────────────────────────────────────────

// The BenchmarkFinalize fixture, from §13 of plans/V4-SP-10-checkpointer-l4.md: a draft holding 40
// segments, 200 file pointers and 200 tool pointers. Five distinct paths and five pathless tool
// uses per segment is what reaches 400 pointers with none of them deduping away.
const (
	benchFinalizeSegments    = 40
	benchFinalizeFilesPer    = 5
	benchFinalizeToolsPer    = 5
	benchFinalizeTurnsPerSeg = 12
	benchFinalizeSession     = core.SessionID("sess_bench_finalize")
)

// BenchmarkFinalize prices the whole seal against exit criterion 1570 — mean < 50 ms — over the §13
// fixture. Finalize is the one part of the checkpointer that runs inside the PreCompact hook, so its
// cost is charged directly against budget B-E (2 s p99, §11.3 L4).
//
// Every iteration builds a fresh project, corpus and draft with the timer stopped, because Finalize
// consumes the draft it is given: it seals it, retires it, opens a successor, and marks its segments
// encoded one-way, so the same draft cannot be sealed twice. The timed region is exactly the call —
// pointer validation, truncation, marshal, CreateNew, AppendManifest — which is what the criterion
// names.
//
// One honest caveat about the fixture. §13 also specifies 64 decisions, and there is no seam that
// puts decisions on a draft directly: cp.Decisions is filled only by ExtractDecisions, from the
// EdgeExplains chains the corpus happens to produce. So the decision count here is whatever the
// extractor mints from 40 segments of real tool use rather than a pinned 64. The segment and
// pointer counts — which dominate ValidatePointers and Marshal — are the exact ones.
func BenchmarkFinalize(b *testing.B) {
	ctx := context.Background()
	cfg := config.Defaults()
	nop := logging.Nop()

	for b.Loop() {
		b.StopTimer()

		root := b.TempDir()
		l := paths.Of(root)
		require.NoError(b, paths.EnsureLayout(l))
		clk := testutil.NewFakeClock(testutil.Epoch)

		st, err := store.Open(root, cfg, store.Deps{Log: nop, Clock: clk})
		require.NoError(b, err)
		g, err := dag.Open(root, cfg, nop)
		require.NoError(b, err)
		led, err := negknow.Open(root, cfg, nil, negknow.Deps{
			Session: benchFinalizeSession, Clock: clk, Log: nop, Graph: g, Store: st,
		})
		require.NoError(b, err)

		src := checkpoint.SourceSet{
			Store: st, Segments: st.Segments(), Ledger: led, Pins: &fakePins{},
			Graph: g, Grammar: grammar.New(),
			Tokens: tokens.New(cfg, filepath.Join(l.Tmp, "calib.json")),
		}
		w, err := checkpoint.OpenWriter(root, cfg, nop, obs.New(clk), clk)
		require.NoError(b, err)

		pos := 0
		segs := make([]core.SegmentID, 0, benchFinalizeSegments)
		for s := range benchFinalizeSegments {
			start := core.TurnIndex(s*benchFinalizeTurnsPerSeg + 1)
			end := start + benchFinalizeTurnsPerSeg - 1
			for j := range benchFinalizeFilesPer + benchFinalizeToolsPer {
				// The first half of each segment's tool uses write a path only that segment
				// touches, so no file pointer dedups against another segment's; the second half
				// are pathless, so they contribute a tool pointer and nothing else.
				pathKey := ""
				if j < benchFinalizeFilesPer {
					pathKey = fmt.Sprintf("src/s%02d_f%d.ts", s, j)
				}
				id := core.ToolUseID(fmt.Sprintf("toolu_bench_%02d_%02d", s, j))
				turn := start + core.TurnIndex(j)
				body := fmt.Sprintf("body for %s at turn %d", id, int(turn))
				res, perr := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "Edit", Path: pathKey})
				require.NoError(b, perr)
				require.NoError(b, st.RecordToolUse(ctx, store.ToolUseRecord{
					ID: id, Session: benchFinalizeSession, Turn: turn, TS: core.NowMilli(clk),
					Tool: "Edit", ArgsPreview: "Edit " + pathKey, Root: res.Root.Hash, Path: pathKey,
				}))
				pos += 64
				require.NoError(b, dag.BuildToolUse(g, dag.ObservedTool{
					ToolUseID: id, Turn: turn, TS: core.NowMilli(clk), Pos: pos,
					Tool: "Edit", PathKey: pathKey, Writes: pathKey != "", Root: res.Root.Hash,
					Tokens: core.Tokens(len(body) / 4),
				}))
				if pathKey != "" {
					require.NoError(b, st.AppendFileVersion(ctx, pathKey, store.FileVersion{
						TS: core.NowMilli(clk), Root: res.Root.Hash, Turn: turn, Bytes: int64(len(body)),
					}))
					// The file has to exist in the working tree, or ValidatePointers resolves it as
					// pointer_missing and Finalize drops it — which would price a checkpoint with
					// ZERO file pointers and 200 drop entries instead of the §13 fixture's 200
					// pointers.
					abs := filepath.Join(root, filepath.FromSlash(pathKey))
					require.NoError(b, os.MkdirAll(filepath.Dir(abs), 0o755))
					require.NoError(b, os.WriteFile(abs, []byte(body), 0o600))
				}
				clk.Advance(time.Millisecond)
			}
			segID := core.SegmentID(s + 1)
			_, serr := st.Segments().Open(ctx, store.Segment{
				ID: segID, Session: benchFinalizeSession, StartTurn: start,
			})
			require.NoError(b, serr)
			require.NoError(b, st.Segments().Close(ctx, segID, end, map[string]float64{"tokens": 400}))
			segs = append(segs, segID)
		}

		d, err := w.Begin(ctx, benchFinalizeSession, 0, src)
		require.NoError(b, err)
		_, err = w.Advance(ctx, d, segs)
		require.NoError(b, err)

		// Drain the corpus's own write-back off the clock: the timed region prices Finalize, not
		// the OS flush queue the setup above just filled, which Windows would otherwise bill to the
		// first fsync inside it.
		require.NoError(b, st.Flush(ctx))
		require.NoError(b, g.Flush(ctx))
		b.StartTimer()

		if _, err := w.Finalize(ctx, d, finalizeBudget); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		require.NoError(b, led.Close())
		require.NoError(b, st.Close())
		b.StartTimer()
	}
}

// TestFrontierAdvanceCutsResidualSpan is SP-10's half of the Phase 3 exit criterion: over one
// fixed synthetic session — 60 turns, 12 closed segments, the last two left unencoded — the idle
// Advance cadence must leave a residual span (lastTurn − frontier) at least 70% below the
// advancement-off run's. Both runs are deterministic (FakeClock, fixed corpus), so the comparison
// is exact.
//
// The criterion is stated over the FINALIZED checkpoint's frontier, so both arms run a real
// Finalize and both residuals are measured from Ref.Frontier. Reading Draft.Frontier() instead
// would leave the quantity the criterion names unmeasured on either arm.
//
// What the two arms are is worth being precise about, because the switch is NOT reachable from
// here. checkpoint.Frontier.AdvanceOnSegmentClose is read in exactly one place in the tree —
// internal/daemon/wire_checkpoint.go, where it decides whether the advance_frontier idle task is
// registered at all — and nothing in internal/checkpoint consults it. Setting it on a config
// handed to OpenWriter would therefore change nothing and prove nothing. The arms model what the
// flag actually switches: OFF is a writer whose idle task never runs, so no Advance happens
// between Begin and the compaction; ON is a writer whose idle task runs after every segment close,
// trailing the live edge by two segments, which is what leaves the last two unencoded.
func TestFrontierAdvanceCutsResidualSpan(t *testing.T) {
	f := newFx(t)
	f.prompt(1, "Build the widget pipeline end to end. Start with ingestion.", true)
	const lastTurn = core.TurnIndex(60)
	for k := 1; k <= 12; k++ {
		start := core.TurnIndex(5*k - 4)
		end := core.TurnIndex(5 * k)
		f.tool(fmt.Sprintf("tu_%d_a", k), start+1, "Edit", fmt.Sprintf("src/f%02d.ts", k), "edited body", false)
		f.tool(fmt.Sprintf("tu_%d_b", k), start+2, "Read", "src/shared.ts", "read body", false)
		f.closedSeg(core.SegmentID(k), start, end)
	}

	// Advancement OFF: nothing is ever encoded, so the sealed checkpoint covers nothing and the
	// summarizer is left the whole session. This run goes first, before anything is marked.
	wOff, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	dOff, err := wOff.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(0), dOff.Frontier(),
		"store.Frontier is 0 while nothing is encoded, so the off run starts and stays at 0")
	refOff, err := wOff.Finalize(f.ctx(), dOff, finalizeBudget)
	require.NoError(t, err)
	residualOff := int(lastTurn - refOff.Frontier)
	require.Equal(t, int(lastTurn), residualOff, "with no advancement the whole session is residual")
	// Retire the successor Finalize opened, so the ON arm begins from a clean slate for this
	// session rather than resuming the OFF arm's leftovers.
	require.NoError(t, wOff.Abort(wOff.DraftFor(f.sess)))

	// Advancement ON: the idle worker runs Advance after every segment close, trailing the live
	// edge by two segments — which is what leaves the last two unencoded at compaction time.
	wOn, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	dOn, err := wOn.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)

	prev := dOn.Frontier()
	for k := 1; k <= 12; k++ {
		var segs []core.SegmentID
		if k >= 3 {
			segs = []core.SegmentID{core.SegmentID(k - 2)}
		}
		fr, aerr := wOn.Advance(f.ctx(), dOn, segs)
		require.NoError(t, aerr)
		require.GreaterOrEqual(t, fr, prev, "the frontier is monotonically non-decreasing")
		prev = fr
	}
	refOn, err := wOn.Finalize(f.ctx(), dOn, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(50), refOn.Frontier,
		"the sealed checkpoint's frontier is segment 10's EndTurn")
	residualOn := int(lastTurn - refOn.Frontier)

	ratio := float64(residualOn) / float64(residualOff)
	t.Logf("residual span: on=%d off=%d ratio=%.4f", residualOn, residualOff, ratio)
	require.LessOrEqual(t, float64(residualOn), 0.30*float64(residualOff),
		"the advancement-on residual must be at least 70%% below the advancement-off residual")
}

// TestAdvanceRefusesASealedDraft pins the seal race.
//
// Finalize snapshots the draft and then spends the rest of its time outside d.mu — it has to, that
// stretch is file I/O. A concurrent Advance landing in that window marks segments as encoded into
// a checkpoint whose bytes were already decided, and those segments are then encodable into
// nothing at all: Unencoded filters out anything already flagged, and Advance's own DPI guard
// skips them for the successor because they name a different sequence. Landing slightly later, its
// trailing persist overwrites the successor's freshly written draft file, because the draft path
// is keyed on the session rather than on the draft.
func TestAdvanceRefusesASealedDraft(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	f.tool("toolu_after_seal", 5, "Edit", "src/late.ts", "late body", false)
	f.closedSeg(2, 4, 7)

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	succ := f.w.DraftFor(f.sess)
	require.NotNil(t, succ)

	_, err = f.w.Advance(f.ctx(), d, []core.SegmentID{2})
	require.ErrorIs(t, err, checkpoint.ErrDraftSealed,
		"a sealed draft takes no more content; the caller re-Begins into the successor")

	seg, err := f.store.Segments().Get(f.ctx(), 2)
	require.NoError(t, err)
	require.False(t, seg.EncodedOnce,
		"the segment must stay encodable: flagging it for a sealed checkpoint would strand it forever")

	wire, _ := f.persisted()
	require.Equal(t, succ.Seq(), wire.Seq,
		"the sealed draft must not overwrite the successor's file; the path is keyed on the session")

	// And the proof that nothing was lost: the content still reaches the NEXT checkpoint.
	f.advance(succ, 2)
	ref2, err := f.w.Finalize(f.ctx(), succ, finalizeBudget)
	require.NoError(t, err)
	require.Greater(t, int(ref2.Seq), int(ref.Seq))

	raw, err := os.ReadFile(paths.Long(ref2.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Contains(t, cp.EncodedSegments, core.SegmentID(2))
}

// TestBeginChainsToTheLatestSealedCheckpointByDefault pins the parent default.
//
// Only Finalize's successor call passes a non-zero parent; advanceAllSessions and
// draftForPreCompact both hard-code 0. So without a default the chain survives only within a
// single daemon lifetime: after a restart Reader.Chain stops at the restart boundary, and G2.3's
// promise that the user's own words survive arbitrarily many checkpoint generations breaks exactly
// where it matters.
func TestBeginChainsToTheLatestSealedCheckpointByDefault(t *testing.T) {
	f := newFx(t)
	original := "The words the user actually typed, once, at the start."
	f.prompt(0, original, true)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	f.advance(d, 1)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))

	// A second writer over the same root stands in for a daemon restart: nothing in memory, and a
	// caller that passes parent 0 because it has no idea what the parent is.
	restarted, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	_, err = restarted.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)

	wire, cp := f.persisted()
	require.Equal(t, ref.Seq, wire.Parent, "a caller that names no parent gets the latest sealed one")
	require.Equal(t, "0001.json", cp.Parent)
	require.Equal(t, original, cp.UserIntent.Original,
		"and the verbatim original is carried forward through the link rather than re-derived")
}

// TestBeginSkipsASequenceNumberWhoseArtifactAlreadyExists is §8.2's "encoded-once flag, AND
// checkpoint reference" held as one statement.
//
// store.SegmentLog.MarkEncoded is one-way: a segment already flagged for one checkpoint cannot be
// re-pointed at another. So a draft whose number is taken on disk cannot have its segments moved
// to the number Finalize's O_EXCL retry bumps to — every one of them would end up naming a
// checkpoint that does not contain it, and a later ErrAlreadyEncoded would name the wrong
// artifact. Begin is where that is preventable: it probes the disk before claiming a number.
func TestBeginSkipsASequenceNumberWhoseArtifactAlreadyExists(t *testing.T) {
	f := newFx(t)
	// An artifact with no manifest line behind it — another process sealed it between our manifest
	// read and our write, which is exactly what CreateNew's O_EXCL exists to catch.
	l := paths.Of(f.p.Root)
	claimed := paths.CheckpointPath(l, 1)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(claimed)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(claimed), []byte(`{"version":1}`), 0o444))

	f.prompt(0, "Investigate the timeout.", true)
	f.tool("toolu_seq_probe", 1, "Read", "src/a.ts", "body", false)
	f.closedSeg(1, 0, 3)

	d := f.begin()
	require.Greater(t, int(d.Seq()), 1,
		"a draft must not claim a sequence number whose artifact is already on disk")

	f.advance(d, 1)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	seg, err := f.store.Segments().Get(f.ctx(), 1)
	require.NoError(t, err)
	require.Equal(t, ref.Seq, seg.CheckpointSeq,
		"every encoded segment must reference the checkpoint that actually contains it")
}

// TestWriterConformanceSuite runs SP-01's checkpointtest.RunWriterSuite against *FileWriter. It is
// what turns the suite's Rule W-1 skip off for this package: exit criterion 1568 requires all four
// SP-01 suites to run with zero skips, and RunWriterSuite's only driver on this branch fed it a
// stub, so the whole writer behaviour block SP-01 authored had never executed against the real
// implementation.
//
// The fixture is rebuilt on every call because the suite calls the factory once per case and once
// more to probe for a stub, and each case needs a project no earlier case has already written a
// checkpoint into.
func TestWriterConformanceSuite(t *testing.T) {
	checkpointtest.RunWriterSuite(t, "checkpoint.OpenWriter", func(t *testing.T) checkpointtest.WriterFixture {
		f := newFx(t)
		f.prompt(0, "Fix the intermittent 500s on the refresh endpoint.", true)
		f.tool("toolu_conformance_0001", 1, "Read", "src/auth.ts", "export function refreshToken() {}", false)
		f.closedSeg(1, 0, 3)
		return checkpointtest.WriterFixture{
			Writer:   f.w,
			Source:   f.src,
			Session:  f.sess,
			Segments: []core.SegmentID{1},
			Budget:   finalizeBudget,
		}
	})
}

// TestBeginReleasesTheNumberOfADraftItCouldNotBegin (wave 22, D67(a)): Begin claims a fresh draft's
// sequence number before it reads the draft's sources, and a Begin that then fails left the number
// claimed by no draft. The session's next draft skipped it, so its checkpoint was sealed one number
// later than the project's newest plus one. A number no draft holds is given out again.
func TestBeginReleasesTheNumberOfADraftItCouldNotBegin(t *testing.T) {
	f := newFx(t)
	f.pins.allErr = fmt.Errorf("pins: reading the invariant log: %w", os.ErrPermission)
	_, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.Error(t, err, "fixture sanity: a Begin whose pins cannot be read fails")

	f.pins.allErr = nil
	d := f.begin()
	require.Equal(t, core.CheckpointSeq(1), d.Seq(), "the failed Begin's number is held by no draft")
}

// TestASuccessorThatCouldNotBeginCostsTheSessionNoNumber is the same on the seal's path, where a
// load-dependent failure made it reachable: Finalize opens the successor draft on PreCompact's
// context, and a PreCompact that has spent its wall-clock budget hands it an expired one, so the
// successor's read of the checkpoint just sealed fails. Here the successor fails reading the pins,
// which the seal itself survives. The session's next draft takes the number after the seal.
func TestASuccessorThatCouldNotBeginCostsTheSessionNoNumber(t *testing.T) {
	f := newFx(t)
	d := seedDraft(t, f)
	f.pins.allErr = fmt.Errorf("pins: reading the invariant log: %w", os.ErrPermission)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err, "an unreadable pin set must not cost the checkpoint")
	require.Equal(t, core.CheckpointSeq(1), ref.Seq)

	f.pins.allErr = nil
	next := f.begin()
	require.Equal(t, core.CheckpointSeq(2), next.Seq(),
		"the successor that could not begin holds no number, so the next draft follows the seal")
}
