package checkpoint

// In-package unit tests for the Draft state machine and the writer's small pure helpers. These
// deliberately do NOT import internal/testutil: an in-package test file compiles into the package
// itself, and §3.2's composition-root rule forbids that edge (see tools/devtool/importgraph.go),
// so everything here builds its fixtures from t.TempDir and the foundation packages directly.
// The full behavioural suite, which does want testutil's project helper, lives in writer_test.go
// as an x_test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// SetReaderForTesting swaps w's Reader. It exists because OpenWriter wires the package's own
// OpenReader, which is still the SP-01 stub on this branch: the parent-intent test in
// writer_test.go needs a Reader that can answer Get, and an interface field on an unexported
// seam is only reachable from inside the package. Declared in a _test.go file, so it is part of
// no shipped API.
func SetReaderForTesting(w *FileWriter, r Reader) { w.reader = r }

// newDraftForTest builds a minimal live Draft the way Begin's fresh path does, persisted under
// t.TempDir.
func newDraftForTest(t *testing.T) *Draft {
	t.Helper()
	d := &Draft{
		session:  "sess_draft_unit",
		seq:      3,
		parent:   2,
		encoded:  map[core.SegmentID]bool{7: true, 9: true},
		frontier: 41,
		started:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		path:     filepath.Join(t.TempDir(), "draft-sess_draft_unit.json"),
	}
	d.cp = Checkpoint{Version: SchemaVersion, Session: d.session, Seq: d.seq}
	return d
}

func TestDraftAccessorsReflectState(t *testing.T) {
	d := newDraftForTest(t)
	require.Equal(t, core.CheckpointSeq(3), d.Seq())
	require.Equal(t, core.TurnIndex(41), d.Frontier())
	require.Equal(t, 2, d.EncodedCount())
}

func TestSetCurrentWorkMarksExplicitAndPersists(t *testing.T) {
	d := newDraftForTest(t)
	blocked := "waiting on CI"
	d.SetCurrentWork(CurrentWork{Goal: "X", NextStep: "run the suite", BlockedOn: &blocked})

	d.mu.Lock()
	explicit := d.workExplicit
	d.mu.Unlock()
	require.True(t, explicit, "SetCurrentWork stops Advance's derivation for good")

	snap := d.snapshot()
	require.Equal(t, "X", snap.CurrentWork.Goal)
	require.NotNil(t, snap.CurrentWork.BlockedOn)

	_, err := os.Stat(paths.Long(d.path))
	require.NoError(t, err, "every mutating call persists the draft")
}

func TestSetCacheAndAddDropsAccumulate(t *testing.T) {
	d := newDraftForTest(t)
	d.SetCache(CacheInfo{PChosen: 9, RewriteTokens: 4, TTLState: "warm"})
	d.AddDrops(DropEntry{Kind: "narrative", ID: "n1"}, DropEntry{Kind: "tool_output", ID: "t1"})
	d.AddDrops(DropEntry{Kind: "narrative", ID: "n2"})

	snap := d.snapshot()
	require.Equal(t, "warm", snap.Cache.TTLState)
	require.Len(t, snap.Dropped, 3)
	require.Equal(t, "n1", snap.Dropped[0].ID)
}

func TestOpenQuestionMutatorsDedupAndCap(t *testing.T) {
	d := newDraftForTest(t)
	d.AddOpenQuestion("q-one")
	d.AddOpenQuestion("q-one")
	d.AddOpenQuestion("q-two")
	require.Equal(t, []string{"q-one", "q-two"}, d.snapshot().OpenQuestions)

	d.SetOpenQuestions([]string{"only"})
	require.Equal(t, []string{"only"}, d.snapshot().OpenQuestions, "SetOpenQuestions replaces the explicit list")

	for i := 0; i < 2*maxOpenQuestions; i++ {
		d.AddOpenQuestion(strings.Repeat("q", 3) + "-" + string(rune('a'+i%26)) + strings.Repeat("x", i/26+1))
	}
	require.LessOrEqual(t, len(d.snapshot().OpenQuestions), maxOpenQuestions)
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	d := newDraftForTest(t)
	blocked := "io"
	d.cp.Invariants = []Invariant{{ID: "inv_1", Text: "keep"}}
	d.cp.UserIntent = UserIntent{Original: "o", Evolution: []string{"e1"}}
	d.cp.Eliminated = []negknow.Record{{ID: "elim_1", StaleBecause: []string{"dep"}}}
	d.cp.Decisions = []Decision{{ID: "dec_1", AlternativesRejected: []string{"alt"}}}
	d.cp.CurrentWork = CurrentWork{Goal: "g", BlockedOn: &blocked}
	d.cp.Pointers = Pointers{
		Files: []FilePointer{{Path: "src/a.ts", Why: "referenced"}},
		Tools: []ToolPointer{{ToolUseID: "tu_1", Summary: "s"}},
	}
	d.cp.SketchRefs = defaultSketchRefs()

	snap := d.snapshot()
	snap.Invariants[0].Text = "mutated"
	snap.UserIntent.Evolution[0] = "mutated"
	snap.Eliminated[0].StaleBecause[0] = "mutated"
	snap.Decisions[0].AlternativesRejected[0] = "mutated"
	*snap.CurrentWork.BlockedOn = "mutated"
	snap.Pointers.Files[0].Path = "mutated"
	snap.Pointers.Tools[0].Summary = "mutated"
	snap.SketchRefs["tried"] = "mutated"
	snap.EncodedSegments[0] = 999

	fresh := d.snapshot()
	require.Equal(t, "keep", fresh.Invariants[0].Text)
	require.Equal(t, "e1", fresh.UserIntent.Evolution[0])
	require.Equal(t, "dep", fresh.Eliminated[0].StaleBecause[0])
	require.Equal(t, "alt", fresh.Decisions[0].AlternativesRejected[0])
	require.Equal(t, "io", *fresh.CurrentWork.BlockedOn)
	require.Equal(t, "src/a.ts", fresh.Pointers.Files[0].Path)
	require.Equal(t, "s", fresh.Pointers.Tools[0].Summary)
	require.Equal(t, "tried.bloom", fresh.SketchRefs["tried"])
	require.Equal(t, []core.SegmentID{7, 9}, fresh.EncodedSegments)
}

func TestDraftPersistRoundTrip(t *testing.T) {
	d := newDraftForTest(t)
	d.workExplicit = true
	d.userOQ = []string{"user-q"}
	d.derivedOQ = []string{"derived-q"}
	d.cp.UserIntent.Original = "the original"
	require.NoError(t, d.persist())

	b, err := os.ReadFile(paths.Long(d.path))
	require.NoError(t, err)
	df, cp, err := decodeDraftFile(b)
	require.NoError(t, err)

	require.Equal(t, d.session, df.Session)
	require.Equal(t, core.CheckpointSeq(3), df.Seq)
	require.Equal(t, core.CheckpointSeq(2), df.Parent)
	require.Equal(t, core.TurnIndex(41), df.Frontier)
	require.Equal(t, []core.SegmentID{7, 9}, df.Encoded)
	require.Equal(t, core.UnixMilli(d.started.UnixMilli()), df.Started)
	require.True(t, df.WorkExplicit)
	require.Equal(t, []string{"user-q"}, df.UserQuestions)
	require.Equal(t, "the original", cp.UserIntent.Original)
	require.Equal(t, []string{"user-q", "derived-q"}, cp.OpenQuestions,
		"explicit questions sort first in the materialized list")
}

func TestEstimatedTokensPricesTheSnapshot(t *testing.T) {
	d := newDraftForTest(t)
	require.Equal(t, core.Tokens(0), d.EstimatedTokens(), "no estimator prices nothing, without panicking")

	d.src.Tokens = tokens.New(config.Defaults(), filepath.Join(t.TempDir(), "calib.json"))
	d.cp.Narrative = strings.Repeat("seg 1 turns 1-5: 4 tool uses over 2 files\n", 20)
	require.Greater(t, int(d.EstimatedTokens()), 0)
}

// ── writer helpers ────────────────────────────────────────────────────────────────────────────

func TestRelPathIsProjectRelativeForwardSlash(t *testing.T) {
	root := t.TempDir()
	w := &FileWriter{root: root, l: paths.Of(root)}

	abs := paths.CheckpointPath(w.l, 7)
	require.Equal(t, ".qompack/checkpoints/0007.json", w.relPath(abs))

	outside := filepath.Join(filepath.Dir(root), "elsewhere.json")
	require.Equal(t, filepath.ToSlash(outside), w.relPath(outside),
		"a path outside the project falls back to its absolute slash form")
}

func TestFirstSentenceSplitter(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fix the retry loop. Then ship.", "Fix the retry loop."},
		{"Check the pool wrapper? It hides timeouts.", "Check the pool wrapper?"},
		{"Stop! Now.", "Stop!"},
		{"line one.\nline two", "line one."},
		{"no terminator", "no terminator"},
		{"", ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, firstSentence(c.in), "input %q", c.in)
	}
}

func TestTruncRunesCutsAtRuneBoundary(t *testing.T) {
	require.Equal(t, "abc", truncRunes("abc", 5))
	require.Equal(t, "ab", truncRunes("abc", 2))
	require.Equal(t, "hél", truncRunes("héllo", 3), "runes, not bytes")
	require.Equal(t, "", truncRunes("abc", 0))
}

func TestFeatureSummarySortsKeysAscending(t *testing.T) {
	require.Equal(t, "", featureSummary(nil))
	require.Equal(t, "a=0.25, b=1.00", featureSummary(map[string]float64{"b": 1, "a": 0.25}))
}

func TestNodesInRangeIsOneScanFilteredByKindAndTurn(t *testing.T) {
	g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
	require.NoError(t, err)

	add := func(n dag.Node) { require.NoError(t, g.AddNode(n)) }
	add(dag.Node{ID: dag.UserPromptNode(3), Kind: dag.KindUserPrompt, Turn: 3, Pos: 10})
	add(dag.Node{ID: dag.ToolUseNode("tu_5"), Kind: dag.KindToolUse, Turn: 5, Pos: 20, Ref: "Read"})
	add(dag.Node{ID: dag.FileNode("src/a.ts"), Kind: dag.KindFile, Turn: 6, Pos: 30, Ref: "src/a.ts"})
	add(dag.Node{ID: dag.UserPromptNode(7), Kind: dag.KindUserPrompt, Turn: 7, Pos: 40})
	add(dag.Node{ID: dag.ToolUseNode("tu_12"), Kind: dag.KindToolUse, Turn: 12, Pos: 50, Ref: "Edit"})

	// nodesInRange returns NodesAfter's own (position) order, not turn order — Advance sorts its
	// partitions itself — so this asserts membership, not sequence.
	got := nodesInRange(g, []dag.NodeKind{dag.KindUserPrompt, dag.KindToolUse}, 4, 10)
	ids := make([]dag.NodeID, len(got))
	for i, n := range got {
		ids[i] = n.ID
	}
	require.ElementsMatch(t, []dag.NodeID{dag.ToolUseNode("tu_5"), dag.UserPromptNode(7)}, ids)
}

func TestOpenDraftsAndDraftForAndColdSources(t *testing.T) {
	da, db := newDraftForTest(t), newDraftForTest(t)
	da.session, db.session = "a", "b"
	w := &FileWriter{drafts: map[core.SessionID]*Draft{"b": db, "a": da}}

	require.Equal(t, []core.SessionID{"a", "b"}, w.OpenDrafts(), "sorted ascending for determinism")
	require.Same(t, da, w.DraftFor("a"))
	require.Nil(t, w.DraftFor("missing"))

	require.Nil(t, w.coldSources().Grammar, "no Begin yet: cold sources are zero")
	w.lastSrc = SourceSet{Grammar: grammar.New()}
	require.NotNil(t, w.coldSources().Grammar)
}

func TestRetireDraftIsIdempotentAndKeyedOnIdentity(t *testing.T) {
	d := newDraftForTest(t)
	require.NoError(t, d.persist())
	w := &FileWriter{
		log:    logging.Nop(),
		drafts: map[core.SessionID]*Draft{d.session: d},
	}

	w.retireDraft(d)
	require.Nil(t, w.DraftFor(d.session))
	_, err := os.Stat(paths.Long(d.path))
	require.True(t, os.IsNotExist(err))

	w.retireDraft(d) // idempotent: nothing to do, nothing to fail

	// A retired draft must not clobber a NEWER draft for the same session.
	fresh := newDraftForTest(t)
	fresh.session = d.session
	require.NoError(t, fresh.persist())
	w.drafts[d.session] = fresh
	w.retireDraft(d)
	require.Same(t, fresh, w.DraftFor(d.session), "retiring a stale draft leaves the live one alone")
	_, err = os.Stat(paths.Long(fresh.path))
	require.NoError(t, err, "the live draft's file survives")
}
