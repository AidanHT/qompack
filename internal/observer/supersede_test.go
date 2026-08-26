package observer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The chunk-set rows CALL detectSupersession rather than driving OnToolUse, because the chunk list
// is what they are about: fakeStore.PutBytes mints exactly one chunk per payload, so a
// superset/subset relation cannot be expressed through the pipeline at all. The end-to-end rows
// (identical root, different path, already-superseded, reopen, ordering) go through OnToolUse,
// where a store's own index is what answers ToolUsesByPath.

// supersedePath is the one path every single-path row reads.
const supersedePath = "src/auth.ts"

// chunkRef mints a deterministic chunk reference from a label, so a test can spell a chunk set as
// {c1, c2, c3} and have the hashes follow.
func chunkRef(label string) core.ChunkRef {
	return core.ChunkRef{Hash: core.HashBytes(core.DomainChunk, []byte(label)), Len: len(label)}
}

// rootOf builds the Root a chunk list would have produced. Its Hash is derived from the chunks, so
// two different chunk lists never collide on a root — which is what keeps the p.Root == rec.Root
// fast path from silently answering a row that is about isSuperset.
func rootOf(chunks ...core.ChunkRef) store.Root {
	var acc []byte
	for _, c := range chunks {
		acc = append(acc, c.Hash[:]...)
	}
	return store.Root{Hash: core.HashBytes(core.DomainRoot, acc), Chunks: chunks}
}

// priorRead is a staged tool_use index entry: a read of supersedePath that already happened.
func priorRead(id string, ts core.UnixMilli, root store.Root) store.ToolUseRecord {
	return store.ToolUseRecord{
		ID: core.ToolUseID(id), Session: testSession, TS: ts,
		Tool: "FileRead", Path: supersedePath, Root: root.Hash, Status: store.StatusOK,
	}
}

// newRead is the record OnToolUse would have built for the call under test.
func newRead(id string, ts core.UnixMilli, root store.Root) store.ToolUseRecord {
	return priorRead(id, ts, root)
}

// sigPair returns two signatures of width perms agreeing in exactly agree positions, so their
// Signature.Jaccard is agree/perms EXACTLY rather than approximately — a row that says "Jaccard
// 0.95" must not be at the mercy of a hash.
func sigPair(perms, agree int) (sketch.Signature, sketch.Signature) {
	a := make([]uint64, perms)
	b := make([]uint64, perms)
	for i := range a {
		a[i] = uint64(i + 1)
		if i < agree {
			b[i] = a[i]
		} else {
			b[i] = uint64(i+1) << 32 // distinct from a[i] for every i >= 0
		}
	}
	return sketch.Signature{Perms: uint16(perms), Mins: a}, sketch.Signature{Perms: uint16(perms), Mins: b}
}

// The near-duplicate rows' fixture. sigPerms is chosen so every threshold the plan's table names is
// representable as an exact fraction of it.
const (
	sigPerms      = 20
	nearDupThresh = 0.9
)

// countEdgeKind returns how many edges of kind appear in edges.
func countEdgeKind(edges []dag.Edge, kind dag.EdgeKind) int {
	n := 0
	for _, e := range edges {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// detect runs the supersession scan for rec over the chunk set of root.
func (h *harness) detect(rec store.ToolUseRecord, root store.Root) []core.ToolUseID {
	return h.obs.detectSupersession(context.Background(), h.state(testSession), rec,
		store.PutResult{Root: root})
}

// newRealStoreObserver builds an observer over a REAL SP-06 store and a REAL dag.Graph rooted at
// root. The reopen and ordering rows need the store's own tool_use index — a double asserting its
// own ordering would assert nothing — so they pay for the real one.
//
// It deliberately registers no Close cleanup: both callers close the store themselves, because
// when the close happens is part of what they assert.
func newRealStoreObserver(t *testing.T, root string, clock *fakeClock, metrics obs.Registry) (*observer, store.Store) {
	t.Helper()
	cfg := config.Defaults()

	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: clock})
	require.NoError(t, err)

	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(t, err)

	built, err := New(Options{
		ProjectRoot: root, Cfg: cfg, Store: st, Graph: g,
		Log: logging.Nop(), Metrics: metrics, Clock: clock,
	})
	require.NoError(t, err)
	impl, ok := built.(*observer)
	require.True(t, ok, "New must return the concrete observer")
	return impl, st
}

func TestSupersede_IdenticalRootMarksEarlier(t *testing.T) {
	h, g := newRealGraphHarness(t)
	body := strings.Repeat("export async function refreshToken() {}\n", 8)

	h.drive(readOf("toolu_1", supersedePath, body))
	h.Clock.Advance(time.Second)
	h.drive(readOf("toolu_2", supersedePath, body))

	require.Equal(t, []supersedeCall{{Older: "toolu_1", By: "toolu_2"}}, h.Store.Supersedes,
		"identical content: the LATER read wins, and MarkSuperseded takes older FIRST")
	require.Equal(t, int64(1), h.counter("observer.superseded"))

	edges := allEdges(g)
	require.Equal(t, 1, countEdgeKind(edges, dag.EdgeSupersedes), "exactly one supersedes edge")
	_, ok := findEdge(edges, dag.ToolUseNode("toolu_1"), dag.ToolUseNode("toolu_2"), dag.EdgeSupersedes)
	require.True(t, ok, "SP-07 D-1: EdgeSupersedes runs superseded (older) to superseding (newer)")
	_, ok = findEdge(edges, dag.ToolUseNode("toolu_2"), dag.ToolUseNode("toolu_1"), dag.EdgeSupersedes)
	require.False(t, ok, "reversed, the 0.30 multiplier would rank the superseded read as upstream")
}

func TestSupersede_SupersetChunkSet(t *testing.T) {
	h := newHarness(t)
	c1, c2, c3 := chunkRef("c1"), chunkRef("c2"), chunkRef("c3")

	older := rootOf(c1, c2)
	h.Store.setRoot(older)
	h.Store.seed(priorRead("toolu_1", 100, older))

	newer := rootOf(c1, c2, c3)
	marked := h.detect(newRead("toolu_2", 200, newer), newer)

	require.Equal(t, []core.ToolUseID{"toolu_1"}, marked)
	require.Equal(t, []supersedeCall{{Older: "toolu_1", By: "toolu_2"}}, h.Store.Supersedes,
		"{c1,c2,c3} holds everything {c1,c2} held, so the earlier read is redundant")
}

func TestSupersede_SubsetDoesNotSupersede(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Cfg.Store.Canonicalize.MinHash.NearDupThreshold = nearDupThresh
	})
	c1, c2, c3 := chunkRef("c1"), chunkRef("c2"), chunkRef("c3")

	// Jaccard 0.50, well below the 0.9 threshold, so the near-duplicate arm cannot rescue a
	// containment test the subset direction is supposed to fail.
	oldSig, newSig := sigPair(sigPerms, sigPerms/2)

	older := rootOf(c1, c2, c3)
	h.Store.setRoot(older)
	prior := priorRead("toolu_1", 100, older)
	prior.Signature = oldSig
	h.Store.seed(prior)

	newer := rootOf(c1, c2)
	rec := newRead("toolu_2", 200, newer)
	rec.Signature = newSig

	require.Empty(t, h.detect(rec, newer))
	require.Empty(t, h.Store.Supersedes, "a SUBSET of a prior read supersedes nothing")
	require.Zero(t, h.counter("observer.superseded"))
}

func TestSupersede_NearDuplicateAboveThreshold(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Cfg.Store.Canonicalize.MinHash.NearDupThreshold = nearDupThresh
	})
	// Jaccard 0.95 >= 0.9. The chunk sets are DISJOINT, so the superset arm cannot answer this row.
	oldSig, newSig := sigPair(sigPerms, sigPerms-1)

	older := rootOf(chunkRef("a1"), chunkRef("a2"))
	h.Store.setRoot(older)
	prior := priorRead("toolu_1", 100, older)
	prior.Signature = oldSig
	h.Store.seed(prior)

	newer := rootOf(chunkRef("b1"), chunkRef("b2"))
	rec := newRead("toolu_2", 200, newer)
	rec.Signature = newSig

	require.Equal(t, []core.ToolUseID{"toolu_1"}, h.detect(rec, newer))
	require.Equal(t, []supersedeCall{{Older: "toolu_1", By: "toolu_2"}}, h.Store.Supersedes)
	require.Equal(t, int64(1), h.counter("observer.superseded"))
}

func TestSupersede_NearDuplicateBelowThreshold(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Cfg.Store.Canonicalize.MinHash.NearDupThreshold = nearDupThresh
	})
	// Jaccard 0.80 < 0.9.
	oldSig, newSig := sigPair(sigPerms, sigPerms*4/5)

	older := rootOf(chunkRef("a1"), chunkRef("a2"))
	h.Store.setRoot(older)
	prior := priorRead("toolu_1", 100, older)
	prior.Signature = oldSig
	h.Store.seed(prior)

	newer := rootOf(chunkRef("b1"), chunkRef("b2"))
	rec := newRead("toolu_2", 200, newer)
	rec.Signature = newSig

	require.Empty(t, h.detect(rec, newer))
	require.Empty(t, h.Store.Supersedes, "0.80 is below the configured 0.9 threshold")
}

func TestSupersede_DifferentPathIgnored(t *testing.T) {
	h := newHarness(t)
	body := "the very same bytes\n"

	h.drive(readOf("toolu_1", "src/a.ts", body))
	h.Clock.Advance(time.Second)
	h.drive(readOf("toolu_2", "src/b.ts", body))

	require.Equal(t, 2, h.Store.ByPathCalls, "each read scans its OWN path")
	require.Empty(t, h.Store.Supersedes,
		"item 3 is per-path: identical bytes at another path are not a redundant read")
}

func TestSupersede_DifferentClassIgnored(t *testing.T) {
	h := newHarness(t)
	c1, c2, c3 := chunkRef("c1"), chunkRef("c2"), chunkRef("c3")

	older := rootOf(c1, c2)
	h.Store.setRoot(older)
	prior := priorRead("toolu_1", 100, older)
	prior.Tool = "Grep" // classSearch, not classFileContent
	h.Store.seed(prior)

	newer := rootOf(c1, c2, c3)
	require.Empty(t, h.detect(newRead("toolu_2", 200, newer), newer))
	require.Empty(t, h.Store.Supersedes,
		"a Grep hit list and a file's content answer different questions about the same path")
}

func TestSupersede_NeverMarksLaterRecord(t *testing.T) {
	h := newHarness(t)

	same := rootOf(chunkRef("c1"))
	h.Store.setRoot(same)
	h.Store.seed(priorRead("toolu_1", 300, same))

	require.Empty(t, h.detect(newRead("toolu_2", 200, same), same))
	require.Empty(t, h.Store.Supersedes,
		"a record with a LATER TS is not an earlier read, whatever order the index returned it in")
}

func TestSupersede_SkipsAlreadySuperseded(t *testing.T) {
	h := newHarness(t)
	body := "one file, read three times\n"

	h.drive(readOf("toolu_1", supersedePath, body))
	h.Clock.Advance(time.Second)
	h.drive(readOf("toolu_2", supersedePath, body))
	h.Clock.Advance(time.Second)
	h.drive(readOf("toolu_3", supersedePath, body))

	require.Equal(t, []supersedeCall{
		{Older: "toolu_1", By: "toolu_2"},
		{Older: "toolu_2", By: "toolu_3"},
	}, h.Store.Supersedes, "the third read marks only the record that was still current")
	require.Equal(t, int64(2), h.counter("observer.superseded"))
}

func TestSupersede_EphemeralNeitherDirection(t *testing.T) {
	same := rootOf(chunkRef("c1"))

	t.Run("EphemeralPrior", func(t *testing.T) {
		h := newHarness(t)
		h.Store.setRoot(same)
		prior := priorRead("toolu_1", 100, same)
		prior.Ephemeral = true
		h.Store.seed(prior)

		require.Empty(t, h.detect(newRead("toolu_2", 200, same), same))
		require.Empty(t, h.Store.Supersedes, "an ephemeral result is already a first-eviction candidate")
	})

	t.Run("EphemeralNewer", func(t *testing.T) {
		h := newHarness(t)
		h.Store.setRoot(same)
		h.Store.seed(priorRead("toolu_1", 100, same))

		rec := newRead("toolu_2", 200, same)
		rec.Ephemeral = true

		require.Empty(t, h.detect(rec, same))
		require.Empty(t, h.Store.Supersedes)
		require.Zero(t, h.Store.ByPathCalls, "retrieval is excluded before the scan, not after it")
	})
}

func TestSupersede_LookbackCapped(t *testing.T) {
	h := newHarness(t)

	same := rootOf(chunkRef("c1"))
	h.Store.setRoot(same)
	const priors = 100
	for i := range priors {
		h.Store.seed(priorRead(fmt.Sprintf("toolu_prior_%03d", i), core.UnixMilli(i), same))
	}

	marked := h.detect(newRead("toolu_new", priors, same), same)

	require.Equal(t, []int{supersessionLookback}, h.Store.ByPathLimits,
		"the scan is bounded at the call, so a hot path cannot make the hook unbounded")
	require.Len(t, marked, supersessionLookback,
		"every record the capped lookback returned was examined, and no record beyond it was")
}

func TestSupersede_StatusSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()
	metrics := obs.New(clock)

	o, st := newRealStoreObserver(t, root, clock, metrics)
	body := strings.Repeat("export const sessionTTL = fromEnv()\n", 40)

	_, err := o.OnToolUse(ctx, readOf("toolu_1", supersedePath, body))
	require.NoError(t, err)
	clock.Advance(time.Second)
	_, err = o.OnToolUse(ctx, readOf("toolu_2", supersedePath, body))
	require.NoError(t, err)

	require.Equal(t, int64(1), metrics.Counter("observer.superseded").Value(),
		"the real store's index is what answered the scan")
	require.NoError(t, st.Flush(ctx))
	require.NoError(t, st.Close())

	reopened, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop(), Clock: clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	rec, err := reopened.ToolUse(ctx, "toolu_1")
	require.NoError(t, err)
	require.Equal(t, store.StatusSuperseded, rec.Status,
		`"never appears in a summary" is carried by the RECORD, not by an observer predicate`)
	require.Equal(t, core.ToolUseID("toolu_2"), rec.SupersededBy)
}

func TestSupersede_ToolUsesByPathIsMostRecentFirst(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()

	o, st := newRealStoreObserver(t, root, clock, obs.New(clock))
	t.Cleanup(func() { _ = st.Close() })

	for i, id := range []string{"toolu_1", "toolu_2", "toolu_3"} {
		_, err := o.OnToolUse(ctx, readOf(id, supersedePath,
			strings.Repeat(fmt.Sprintf("revision %d of this file\n", i), 20)))
		require.NoError(t, err)
		clock.Advance(time.Second)
	}

	got, err := st.ToolUsesByPath(ctx, supersedePath, supersessionLookback)
	require.NoError(t, err)
	require.Len(t, got, 3)

	ids := []core.ToolUseID{got[0].ID, got[1].ID, got[2].ID}
	require.Equal(t, []core.ToolUseID{"toolu_3", "toolu_2", "toolu_1"}, ids,
		"5.8 returns the most recent limit records NEWEST FIRST - the order marked[0], and "+
			"therefore ObservedTool.Supersedes, relies on")
}

func TestSupersede_EmitsNoEdgesItself(t *testing.T) {
	h := newHarness(t)

	same := rootOf(chunkRef("c1"), chunkRef("c2"))
	h.Store.setRoot(same)
	h.Store.seed(priorRead("toolu_1", 100, same), priorRead("toolu_2", 200, same))

	rec := newRead("toolu_3", 300, same)
	before := h.Graph.counts()
	marked := h.detect(rec, same)

	require.Equal(t, before, h.Graph.counts(),
		"detectSupersession writes to the STORE only: EdgeSupersedes belongs to dag.BuildToolUse")
	require.Equal(t, []core.ToolUseID{"toolu_2", "toolu_1"}, marked, "newest first")

	h.obs.emitToolGraph(context.Background(), h.state(testSession), rec, nil, marked)

	edges := h.Graph.edges()
	require.Equal(t, 2, countEdgeKind(edges, dag.EdgeSupersedes))
	for _, older := range marked {
		found := false
		for _, e := range edges {
			if e.From == dag.ToolUseNode(older) && e.To == dag.ToolUseNode("toolu_3") &&
				e.Kind == dag.EdgeSupersedes {
				found = true
			}
		}
		require.True(t, found, "%s to toolu_3 is missing; both edges run older to newer", older)
	}
}

// A prior whose Root is the zero hash is an EMPTY result: step 6 of onToolUse indexes the record
// before it knows whether anything was stored, so the index legitimately carries such rows. The
// seventh filter skips them BEFORE GetRoot, which is what keeps a path that was once empty from
// costing a failed lookup and an err.supersede.root warning on every subsequent read of it.
func TestSupersede_ZeroRootPriorSkipped(t *testing.T) {
	h := newHarness(t)
	h.Store.seed(priorRead("toolu_1", 100, store.Root{}))

	newer := rootOf(chunkRef("c1"), chunkRef("c2"))
	require.Empty(t, h.detect(newRead("toolu_2", 200, newer), newer))

	require.Empty(t, h.Store.GetRoots, "the zero root is never fetched")
	require.Zero(t, h.counter("observer.err.supersede.root"),
		"a permanent, boring condition must not read as a recurring I/O failure")
	require.Empty(t, h.Store.Supersedes, "an empty prior holds nothing this read makes redundant")
}

// §8.1 item 3 is "a superset OR near-duplicate of a prior read": the two arms are independent, and
// the near-duplicate arm is deliberate about later-read-wins even when the newer read is SMALLER —
// two renderings of the same file that differ by a trimmed tail are the same knowledge, and the
// fresher one is the one a summary should carry. This is the exact converse of
// TestSupersede_SubsetDoesNotSupersede, which holds the same chunk relation at a Jaccard the
// threshold rejects; the pair is what pins that the containment test and the similarity test are
// not collapsed into one.
func TestSupersede_NearDupSubsetStillSupersedes(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Cfg.Store.Canonicalize.MinHash.NearDupThreshold = nearDupThresh
	})
	c1, c2, c3 := chunkRef("c1"), chunkRef("c2"), chunkRef("c3")

	// Jaccard 0.95 >= 0.9, while the chunk set shrinks from three to two.
	oldSig, newSig := sigPair(sigPerms, sigPerms-1)

	older := rootOf(c1, c2, c3)
	h.Store.setRoot(older)
	prior := priorRead("toolu_1", 100, older)
	prior.Signature = oldSig
	h.Store.seed(prior)

	newer := rootOf(c1, c2)
	rec := newRead("toolu_2", 200, newer)
	rec.Signature = newSig

	require.False(t, isSuperset(newer.Chunks, older.Chunks),
		"the containment arm must be FALSE, or this row proves nothing about the near-dup arm")
	require.Equal(t, []core.ToolUseID{"toolu_1"}, h.detect(rec, newer))
	require.Equal(t, []supersedeCall{{Older: "toolu_1", By: "toolu_2"}}, h.Store.Supersedes)
	require.Equal(t, int64(1), h.counter("observer.superseded"))
}

func TestSupersede_StoreFailuresAreSoft(t *testing.T) {
	same := rootOf(chunkRef("c1"))
	other := rootOf(chunkRef("c2"))

	t.Run("List", func(t *testing.T) {
		h := newHarness(t)
		h.Store.ByPathErr = errors.New("index unreadable")

		require.Empty(t, h.detect(newRead("toolu_2", 200, same), same))
		require.Equal(t, int64(1), h.counter("observer.err.supersede.list"))
	})

	t.Run("Root", func(t *testing.T) {
		h := newHarness(t)
		h.Store.seed(priorRead("toolu_1", 100, other)) // its Root is never registered
		h.Store.GetRootErr = errors.New("root missing")

		require.Empty(t, h.detect(newRead("toolu_2", 200, same), same))
		require.Equal(t, int64(1), h.counter("observer.err.supersede.root"))
		require.Empty(t, h.Store.Supersedes)
	})

	t.Run("Mark", func(t *testing.T) {
		h := newHarness(t)
		h.Store.setRoot(same)
		h.Store.seed(priorRead("toolu_1", 100, same))
		h.Store.MarkErr = errors.New("index read-only")

		require.Empty(t, h.detect(newRead("toolu_2", 200, same), same),
			"a record the store refused to mark is not reported as marked")
		require.Equal(t, int64(1), h.counter("observer.err.supersede.mark"))
		require.Zero(t, h.counter("observer.superseded"))
	})
}

// The near-duplicate counter is claimed by this commit but lives on the PUT, not on the
// supersession scan, so its rows sit here beside the scan they were moved OUT of. §8.1 item 1 names
// test and build output as "the noisiest content class in a coding session … where the dedup ratio
// is won or lost", and that content is pathless — counting it inside detectSupersession, which
// returns early on an empty Path, would have made the counter read ~0 for the whole class.
func TestOnToolUse_NearDupCounterCountsPathlessOutput(t *testing.T) {
	h := newHarness(t)
	h.Store.NearDup = &store.NearDupInfo{Jaccard: nearDupThresh}

	h.drive(bashOf("toolu_1", "go test ./internal/observer/", "ok  internal/observer  3.3s\n"))

	require.Empty(t, h.Store.Records[0].Path, "Bash output is pathless — the class item 1 names")
	require.Zero(t, h.Store.ByPathCalls, "and therefore never a supersession candidate")
	require.Equal(t, int64(1), h.counter("observer.neardup"),
		"the near-duplicate the STORE found is counted at the Put, regardless of path")
}

func TestOnToolUse_NearDupCounterSkipsRetrieval(t *testing.T) {
	h := newHarness(t)
	h.Store.NearDup = &store.NearDupInfo{Jaccard: nearDupThresh}

	h.drive(toolUse("toolu_1", "mcp__qompack__expand",
		`{"path":"src/auth.ts"}`, `{"content":"the expanded result"}`))

	require.Zero(t, h.counter("observer.neardup"),
		"retrieval is not exploration (decision 6): re-reading our own result is not a dedup win")
}

func TestOnToolUse_NearDupCounterSilentWithoutASignal(t *testing.T) {
	h := newHarness(t)

	h.drive(readOf("toolu_1", supersedePath, "the only version of this file\n"))

	require.Equal(t, int64(1), h.counter("observer.tombstone"), "the pipeline did run")
	require.Zero(t, h.counter("observer.neardup"), "no PutResult.NearDup, no count")
}

func TestIsSuperset_EmptyOlder(t *testing.T) {
	newer := []core.ChunkRef{chunkRef("c1"), chunkRef("c2")}

	require.False(t, isSuperset(newer, nil), "nothing is not evidence of anything")
	require.False(t, isSuperset(newer, []core.ChunkRef{}))
	require.False(t, isSuperset(nil, nil))
}

func TestIsSuperset_IgnoresMultiplicity(t *testing.T) {
	c1, c2 := chunkRef("c1"), chunkRef("c2")

	require.True(t, isSuperset([]core.ChunkRef{c1, c2}, []core.ChunkRef{c1, c1, c2}),
		"item 3 says chunk SET: a file that repeats a chunk is still contained")
}

// TestIsSuperset_Properties holds the property rows. They are SUBTESTS of a Test function whose
// name the plans' -run patterns also match, because `go test -run PropertyIsSupersetReflexive`
// resolves its first element against TOP-LEVEL names only: a bare Property... function would never
// be selected by the very commands that document it.
func TestIsSuperset_Properties(t *testing.T) {
	draw := func(rt *rapid.T, label string) []core.ChunkRef {
		ids := rapid.SliceOfN(rapid.Byte(), 1, 8).Draw(rt, label)
		out := make([]core.ChunkRef, 0, len(ids))
		for _, id := range ids {
			out = append(out, chunkRef(string(rune('a'+int(id%26)))))
		}
		return out
	}

	t.Run("PropertyIsSupersetReflexive", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			x := draw(rt, "x")
			if !isSuperset(x, x) {
				rt.Fatalf("isSuperset(x, x) is false for a %d-chunk set", len(x))
			}
		})
	})

	t.Run("PropertyIsSupersetOfUnion", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			x := draw(rt, "x")
			y := draw(rt, "y")
			union := append(append([]core.ChunkRef(nil), x...), y...)
			if !isSuperset(union, x) {
				rt.Fatalf("isSuperset(x union y, x) is false for |x|=%d, |y|=%d", len(x), len(y))
			}
		})
	})
}
