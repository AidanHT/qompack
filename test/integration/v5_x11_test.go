// V5 §4.11 — SP-16 §3's per-segment filters, generated from the store's exact index, narrow a
// path lookup to the segment that holds it, and do so under the two rules that keep a filter
// honest: a positive is only ever a candidate the exact index confirms, and a negative is trusted
// only from a complete, current filter — stale and incomplete filters bypass to the scan.
//
// Every component is the production one: store.Open over a real project, the real append-only
// SegmentLog, the real tool_use index as the filter's key source, store.BuildSegmentFilter,
// WriteSegmentFilter, the segment log's PublishFilter record, ReadSegmentFilter after a store
// reopen, store.Search (the producer behind `recall`) for the exact confirmation, and
// SweepSegmentFilters as the maintenance switch the negative control severs the filters with.
//
// This row is in-process rather than e2e because on this tree the filters have no cross-process
// consumer: `recall` does not consult Segment.BloomRef, no daemon task builds a filter, and the
// runtime.phase7.filters.segmentBloom switch is refused by config.Validate until M6-G16-E passes.
// The last arm pins that refusal, so the disabled feature is recorded as disabled and never as
// passed. Retired clauses this row must NOT assert: no store method counts object reads on a
// filter's behalf (there is no SegmentsMayContain), and no Loud is emitted for a corrupt filter —
// ReadSegmentFilter reports sketch.ErrMalformed and the caller scans.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x11Session is this row's session identity.
const x11Session = core.SessionID("sess-integration-v5-x11")

// x11Segments and x11TurnsPerSegment reproduce the historical setup: twelve closed segments of
// fifty tool uses each, one turn per tool use.
const (
	x11Segments        = 12
	x11TurnsPerSegment = 50
)

// x11CommonPaths is how many paths every segment shares. Each segment's fifty tool uses cycle
// through them, so every segment's filter holds the same ten keys plus, in one segment, the
// distinctive path.
const x11CommonPaths = 10

// x11DistinctiveSegment is the one segment that holds x11DistinctivePath, and x11DistinctiveOffset
// is where inside it (a turn offset from the segment's start). The segment log assigns IDs from 1
// in open order, so the seventh segment opened IS segment 7.
const (
	x11DistinctiveSegment = core.SegmentID(7)
	x11DistinctiveOffset  = 25
)

// x11DistinctivePath appears in exactly one tool use in the whole store. The nonce keeps it from
// colliding with any other row's fixture in this package.
const x11DistinctivePath = "src/auth/refresh_q5x11.go"

// x11Generation is the segment generation every filter is built at and looked up at. On this tree
// nothing rewrites a closed segment, so the caller-supplied generation is the only one there is;
// x11Generation+1 is what a reader passes to say "this segment has been rewritten since".
const x11Generation = 1

// x11TruncatedBytes is how much of a filter file the corruption arm keeps: enough to be a file,
// too little to hold the one-line coverage header the reader needs (the historical row's "8
// bytes").
const x11TruncatedBytes = 8

// x11FilePerm is the owner-only mode the corruption arm rewrites a filter file with, matching
// what the producer wrote it with.
const x11FilePerm = 0o600

// x11SwitchKey is the SP-16 config switch this feature sits behind.
const x11SwitchKey = "runtime.phase7.filters.segmentBloom"

// x11SwitchOn is a project config file that asks for the switch.
const x11SwitchOn = `{"runtime":{"phase7":{"filters":{"segmentBloom":true}}}}`

// x11CommonPath is the i-th shared path.
func x11CommonPath(i int) string {
	return fmt.Sprintf("src/common/f%02d.go", i%x11CommonPaths)
}

// x11PathUniverse is every path this row records: the ten common ones and the distinctive one.
// It is the KEY universe the exact index is asked about, not a stand-in for the index — which
// records fall in which segment is read back from the store, never from test-side bookkeeping.
func x11PathUniverse() []string {
	out := make([]string, 0, x11CommonPaths+1)
	for i := 0; i < x11CommonPaths; i++ {
		out = append(out, x11CommonPath(i))
	}
	return append(out, x11DistinctivePath)
}

// x11OpenStore opens the real store over p with p's own logger and clock, leaving Close to the
// caller so the row can close and reopen it to prove publication is durable.
func x11OpenStore(t *testing.T, p *testutil.Project) store.Store {
	t.Helper()
	s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	return s
}

// x11Now is the project clock as the store's own timestamps see it.
func x11Now(p *testutil.Project) core.UnixMilli {
	return core.UnixMilli(p.Clock.Now().UnixMilli())
}

// x11Seed records twelve closed segments of fifty tool uses each through the real store, with the
// distinctive path in exactly one tool use of segment 7. It returns that tool use's ID.
func x11Seed(t *testing.T, p *testutil.Project, s store.Store) core.ToolUseID {
	t.Helper()
	ctx := context.Background()
	var distinctive core.ToolUseID
	canonOpts := canon.OptionsFrom(p.Cfg.Store.Canonicalize, false)

	for n := 1; n <= x11Segments; n++ {
		start := core.TurnIndex((n-1)*x11TurnsPerSegment + 1)
		end := core.TurnIndex(n * x11TurnsPerSegment)
		id, err := s.Segments().Open(ctx, store.Segment{Session: x11Session, StartTurn: start})
		require.NoError(t, err)
		require.Equal(t, core.SegmentID(n), id, "the segment log assigns IDs in open order from 1")

		for i := 0; i < x11TurnsPerSegment; i++ {
			turn := start + core.TurnIndex(i)
			path := x11CommonPath(i)
			if id == x11DistinctiveSegment && i == x11DistinctiveOffset {
				path = x11DistinctivePath
			}
			body := []byte(fmt.Sprintf("// %s at turn %d\npackage x\n", path, turn))
			res, err := s.PutBytes(ctx, body, store.PutOptions{Tool: "Read", Path: path, Canon: canonOpts})
			require.NoError(t, err, "segment %d turn %d", id, turn)

			tu := core.ToolUseID(fmt.Sprintf("toolu_v5x11_%02d_%02d", n, i))
			require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
				ID: tu, Session: x11Session, Turn: turn, TS: x11Now(p), Tool: "Read", Path: path,
				Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
			}))
			if path == x11DistinctivePath {
				distinctive = tu
			}
		}
		require.NoError(t, s.Segments().Close(ctx, id, end, map[string]float64{"tokens": x11TurnsPerSegment}))
	}
	require.NotEmpty(t, distinctive, "the distinctive path must have been recorded once")
	return distinctive
}

// x11SegmentKeys reads seg's distinct path keys back from the store's EXACT index: every tool-use
// record whose turn falls inside the segment's range. This is the filter's key source, and it is
// the index, not the test, that says what a segment holds.
func x11SegmentKeys(t *testing.T, s store.Store, seg store.Segment) []string {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	for _, path := range x11PathUniverse() {
		recs, err := s.ToolUsesByPath(context.Background(), path, 0)
		require.NoError(t, err)
		for _, rec := range recs {
			if rec.Turn < seg.StartTurn || rec.Turn > seg.EndTurn || seen[rec.Path] {
				continue
			}
			seen[rec.Path] = true
			keys = append(keys, rec.Path)
		}
	}
	sort.Strings(keys)
	return keys
}

// x11ExactHits returns the tool uses the exact index holds for path inside seg's turn range: the
// confirmation step every filter positive must pass through.
func x11ExactHits(t *testing.T, s store.Store, seg store.Segment, path string) []core.ToolUseID {
	t.Helper()
	recs, err := s.ToolUsesByPath(context.Background(), path, 0)
	require.NoError(t, err)
	var ids []core.ToolUseID
	for _, rec := range recs {
		if rec.Turn >= seg.StartTurn && rec.Turn <= seg.EndTurn {
			ids = append(ids, rec.ID)
		}
	}
	return ids
}

// x11KeySource adapts a key slice to BuildSegmentFilter's pull interface.
func x11KeySource(keys []string) func() ([]byte, bool, error) {
	i := 0
	return func() ([]byte, bool, error) {
		if i >= len(keys) {
			return nil, false, nil
		}
		k := keys[i]
		i++
		return []byte(k), true, nil
	}
}

// x11Publish writes f's file and appends the segment log's bloom record for it — the two halves
// of one publication, in the order the producer requires (file first, then the reference).
func x11Publish(t *testing.T, root string, s store.Store, f *store.SegmentFilter) {
	t.Helper()
	ref, err := store.WriteSegmentFilter(root, f)
	require.NoError(t, err)
	require.Equal(t, store.SegmentFilterRef(f.Coverage.Segment), ref)
	pub, ok := s.Segments().(store.SegmentFilterPublisher)
	require.True(t, ok, "the real segment log must publish filters")
	require.NoError(t, pub.PublishFilter(context.Background(), f.Coverage.Segment, ref))
}

// x11AllSegments returns every segment of the row, in ID order.
func x11AllSegments(t *testing.T, s store.Store) []store.Segment {
	t.Helper()
	segs, err := s.Segments().Range(context.Background(), 1, core.TurnIndex(x11Segments*x11TurnsPerSegment))
	require.NoError(t, err)
	require.Len(t, segs, x11Segments)
	sort.Slice(segs, func(i, j int) bool { return segs[i].ID < segs[j].ID })
	return segs
}

// x11Verdicts looks path up in every segment's ON-DISK filter at generation gen and buckets the
// segments by verdict. A segment whose filter cannot be read is a bypass, exactly as a reader
// would treat it.
func x11Verdicts(t *testing.T, root string, segs []store.Segment, path string, gen int) map[store.FilterVerdict][]core.SegmentID {
	t.Helper()
	out := map[store.FilterVerdict][]core.SegmentID{}
	for _, seg := range segs {
		f, err := store.ReadSegmentFilter(root, seg.ID)
		if err != nil {
			f = nil
		}
		v := f.Lookup([]byte(path), gen)
		out[v] = append(out[v], seg.ID)
	}
	return out
}

// x11SearchIDs runs the exact search `recall` is built on and returns the tool uses it found.
func x11SearchIDs(t *testing.T, s store.Store, path string) []core.ToolUseID {
	t.Helper()
	hits, err := s.Search(context.Background(), store.Query{Path: path})
	require.NoError(t, err)
	ids := make([]core.ToolUseID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ToolUseID)
	}
	return ids
}

// TestV5_SegmentBloomNarrowsRecall is V5-VERIFY §4.11.
//
// The negative control is the "filters severed" arm: SweepSegmentFilters with an empty keep set,
// the producer's own maintenance switch, removes every filter file while the segment log still
// names them. The narrowing must then collapse to zero skippable segments — every lookup bypasses
// — and the exact index must still find the hit. Without that arm, "eleven segments were skipped"
// could also be reported by a lookup that never read a filter.
func TestV5_SegmentBloomNarrowsRecall(t *testing.T) {
	p := testutil.NewProject(t)
	ctx := context.Background()

	s := x11OpenStore(t, p)
	distinctiveTU := x11Seed(t, p, s)

	// ── Generation: one filter per closed segment, keyed from the exact index ────────────────────
	segs := x11AllSegments(t, s)
	for _, seg := range segs {
		require.True(t, seg.Closed)
		require.Empty(t, seg.BloomRef, "no filter exists before this row builds one")
		keys := x11SegmentKeys(t, s, seg)
		want := x11CommonPaths
		if seg.ID == x11DistinctiveSegment {
			want++
		}
		require.Len(t, keys, want, "the exact index must yield segment %d's distinct paths", seg.ID)

		f, err := store.BuildSegmentFilter(ctx, seg, x11Generation, x11Now(p), x11KeySource(keys))
		require.NoError(t, err)
		require.True(t, f.Coverage.Complete, "a build that consumed every key is complete")
		require.Equal(t, store.IncompleteNone, f.Coverage.Incomplete)
		require.Equal(t, want, f.Coverage.Keys, "coverage publishes how many keys the filter holds")
		require.Equal(t, seg.ID, f.Coverage.Segment)
		require.Equal(t, x11Generation, f.Coverage.Generation)
		require.Equal(t, seg.StartTurn, f.Coverage.StartTurn)
		require.Equal(t, seg.EndTurn, f.Coverage.EndTurn)
		x11Publish(t, p.Root, s, f)
	}

	// ── Publication is durable: close, reopen, and the log still names every filter ─────────────
	require.NoError(t, s.Close())
	s = x11OpenStore(t, p)
	t.Cleanup(func() { _ = s.Close() })
	segs = x11AllSegments(t, s)
	for _, seg := range segs {
		require.Equal(t, store.SegmentFilterRef(seg.ID), seg.BloomRef,
			"segment %d's bloom record must survive a reopen of the append-only log", seg.ID)
		_, err := os.Stat(store.SegmentFilterPath(p.Root, seg.ID))
		require.NoError(t, err, "the file the record names must exist")
		f, err := store.ReadSegmentFilter(p.Root, seg.ID)
		require.NoError(t, err)
		require.True(t, f.Coverage.Complete, "coverage round-trips with the bits it describes")
		require.Equal(t, seg.ID, f.Coverage.Segment)
	}

	// ── Narrowing: the distinctive path is a candidate in segment 7 and a miss elsewhere ─────────
	verdicts := x11Verdicts(t, p.Root, segs, x11DistinctivePath, x11Generation)
	t.Logf("v5 §4.11 verdicts for %s: maybe=%v miss=%v bypass=%v",
		x11DistinctivePath, verdicts[store.FilterMaybe], verdicts[store.FilterMiss], verdicts[store.FilterBypass])
	require.Empty(t, verdicts[store.FilterBypass],
		"every filter is complete and current, so nothing may bypass")
	require.Contains(t, verdicts[store.FilterMaybe], x11DistinctiveSegment,
		"the segment that holds the path can never be skipped")
	require.LessOrEqual(t, len(verdicts[store.FilterMaybe]), 2,
		"at most one false-positive segment beyond the true one (historical tolerance)")
	require.GreaterOrEqual(t, len(verdicts[store.FilterMiss]), x11Segments-2,
		"the filters must let the reader skip the segments that cannot hold the path")

	// A common path is in EVERY segment: no filter may ever say miss about it.
	common := x11Verdicts(t, p.Root, segs, x11CommonPath(0), x11Generation)
	require.Len(t, common[store.FilterMaybe], x11Segments,
		"a key every segment holds must be a candidate in every segment")

	// ── Exact positives: a maybe is confirmed by the index, never by the filter ──────────────────
	var confirmed []core.SegmentID
	for _, cand := range verdicts[store.FilterMaybe] {
		seg := segs[cand-1]
		require.Equal(t, cand, seg.ID)
		if ids := x11ExactHits(t, s, seg, x11DistinctivePath); len(ids) > 0 {
			require.Equal(t, []core.ToolUseID{distinctiveTU}, ids)
			confirmed = append(confirmed, seg.ID)
		}
	}
	require.Equal(t, []core.SegmentID{x11DistinctiveSegment}, confirmed,
		"after exact confirmation only the true segment remains")
	require.Equal(t, []core.ToolUseID{distinctiveTU}, x11SearchIDs(t, s, x11DistinctivePath),
		"the exact search behind `recall` returns the hit")

	// ── A positive is never a result: an over-approximate filter cannot add a hit ────────────────
	// Segment 3's filter is rebuilt from a superset key source that includes the distinctive path.
	// This is the shape a filter takes after keys are deleted beneath it, and the reader must not
	// turn its "maybe" into a hit: the exact index for segment 3 holds no such record.
	over := segs[2]
	require.NotEqual(t, x11DistinctiveSegment, over.ID)
	superset := append(x11SegmentKeys(t, s, over), x11DistinctivePath)
	fOver, err := store.BuildSegmentFilter(ctx, over, x11Generation, x11Now(p), x11KeySource(superset))
	require.NoError(t, err)
	x11Publish(t, p.Root, s, fOver)
	got, err := store.ReadSegmentFilter(p.Root, over.ID)
	require.NoError(t, err)
	require.Equal(t, store.FilterMaybe, got.Lookup([]byte(x11DistinctivePath), x11Generation))
	require.False(t, store.FilterMaybe.SkipsSegment())
	require.Empty(t, x11ExactHits(t, s, over, x11DistinctivePath),
		"segment %d's exact index has no such record, so the maybe confirms to nothing", over.ID)
	require.Equal(t, []core.ToolUseID{distinctiveTU}, x11SearchIDs(t, s, x11DistinctivePath),
		"the over-approximate filter changed no search result")

	// ── Stale negatives bypass: a reader ahead of the filter's generation gets no miss ───────────
	stale := x11Verdicts(t, p.Root, segs, x11DistinctivePath, x11Generation+1)
	require.Empty(t, stale[store.FilterMiss],
		"a filter behind the caller's generation may not report a miss")
	require.Contains(t, stale[store.FilterMaybe], x11DistinctiveSegment,
		"a positive from a stale filter is still a candidate")
	require.Equal(t, x11Segments, len(stale[store.FilterBypass])+len(stale[store.FilterMaybe]))

	// ── Incomplete negatives bypass: a partial rebuild of segment 7 must not hide its own key ────
	// The key source fails after one common key, before it reaches the distinctive path. A filter
	// that then answered "miss" for that path would be a false negative about the one segment
	// that holds it; coverage marks it incomplete and the reader bypasses instead.
	seg7 := segs[x11DistinctiveSegment-1]
	require.Equal(t, x11DistinctiveSegment, seg7.ID)
	boom := errors.New("v5x11: key source failed part-way")
	served := 0
	fPartial, err := store.BuildSegmentFilter(ctx, seg7, x11Generation, x11Now(p), func() ([]byte, bool, error) {
		served++
		if served == 1 {
			return []byte(x11CommonPath(0)), true, nil
		}
		return nil, false, boom
	})
	require.NoError(t, err, "a failing source ends the build; it does not fail the call")
	require.False(t, fPartial.Coverage.Complete)
	require.Equal(t, store.IncompleteSourceError, fPartial.Coverage.Incomplete)
	require.Equal(t, 1, fPartial.Coverage.Keys)
	x11Publish(t, p.Root, s, fPartial)

	partial, err := store.ReadSegmentFilter(p.Root, seg7.ID)
	require.NoError(t, err)
	require.False(t, partial.Coverage.Complete, "incompleteness is published with the bits")
	require.Equal(t, store.IncompleteSourceError, partial.Coverage.Incomplete)
	require.Equal(t, store.FilterBypass, partial.Lookup([]byte(x11DistinctivePath), x11Generation),
		"an incomplete filter has no usable negatives: bypass, never miss")
	require.Equal(t, store.FilterMaybe, partial.Lookup([]byte(x11CommonPath(0)), x11Generation),
		"its positives still narrow the search")
	require.Equal(t, []core.ToolUseID{distinctiveTU}, x11SearchIDs(t, s, x11DistinctivePath),
		"the exact search still finds the hit the partial filter could not vouch for")

	// ── A corrupt filter is no filter, never an empty one ────────────────────────────────────────
	raw, err := os.ReadFile(store.SegmentFilterPath(p.Root, seg7.ID))
	require.NoError(t, err)
	require.Greater(t, len(raw), x11TruncatedBytes)
	require.NoError(t, os.WriteFile(store.SegmentFilterPath(p.Root, seg7.ID), raw[:x11TruncatedBytes], x11FilePerm))
	corrupt, err := store.ReadSegmentFilter(p.Root, seg7.ID)
	require.ErrorIs(t, err, sketch.ErrMalformed, "a truncated file is reported malformed")
	require.Nil(t, corrupt, "it is never decoded into a filter that says miss to everything")
	require.Equal(t, store.FilterBypass, corrupt.Lookup([]byte(x11DistinctivePath), x11Generation))
	require.Equal(t, []core.ToolUseID{distinctiveTU}, x11SearchIDs(t, s, x11DistinctivePath),
		"the filter is an optimization, never a source of truth: the hit is still found")

	// ── Maintenance keeps what the log names ─────────────────────────────────────────────────────
	keep := map[string]bool{}
	for _, seg := range x11AllSegments(t, s) {
		keep[seg.BloomRef] = true
	}
	rep, err := store.SweepSegmentFilters(ctx, p.Root, keep)
	require.NoError(t, err)
	require.Equal(t, x11Segments, rep.Found)
	require.Equal(t, x11Segments, rep.Kept, "every file a live segment names survives a sweep")
	require.Empty(t, rep.Removed)

	// ── NEGATIVE CONTROL: sever the filters and the narrowing must vanish ────────────────────────
	// An empty keep set is the producer's own switch for "no live segment names any filter". With
	// every file gone the same lookup that skipped ten or more segments above may skip NONE, the
	// dangling BloomRef records the log still carries must be defended as "no filter", and the
	// exact index must still answer.
	rep, err = store.SweepSegmentFilters(ctx, p.Root, map[string]bool{})
	require.NoError(t, err)
	require.Len(t, rep.Removed, x11Segments, "the switch must have removed every filter file")
	require.Empty(t, rep.Failed)

	for _, seg := range x11AllSegments(t, s) {
		require.NotEmpty(t, seg.BloomRef, "the append-only log still names the file")
		_, err := store.ReadSegmentFilter(p.Root, seg.ID)
		require.ErrorIs(t, err, core.ErrNotFound, "a dangling reference reads as no filter")
	}
	severed := x11Verdicts(t, p.Root, x11AllSegments(t, s), x11DistinctivePath, x11Generation)
	require.Empty(t, severed[store.FilterMiss],
		"NEGATIVE CONTROL: with the filters severed no segment may be skipped. If a miss survives "+
			"here, the narrowing asserted above was not coming from the filters on disk")
	require.Empty(t, severed[store.FilterMaybe])
	require.Len(t, severed[store.FilterBypass], x11Segments)
	require.Equal(t, []core.ToolUseID{distinctiveTU}, x11SearchIDs(t, s, x11DistinctivePath),
		"severing every filter changes no search result")

	p.AssertAppendOnly(t)

	t.Run("switch is recorded as disabled, never as passed", x11SwitchIsRefused)
}

// x11SwitchIsRefused pins the gate this feature sits behind: a project file that turns
// runtime.phase7.filters.segmentBloom on gets the default back and a warning, the gate table
// records the switch as pending, and Validate names the unpassed gate. Nothing here may be read
// as the feature passing — it is the honest record that it is off.
func x11SwitchIsRefused(t *testing.T) {
	p := testutil.NewProject(t, testutil.WithConfig(x11SwitchOn))
	require.False(t, p.Cfg.Runtime.Phase7.Filters.SegmentBloom,
		"the resolved configuration must hold the default, not the file's value")

	cfg, prov, warns, err := config.Load(config.Env{ProjectRoot: p.Root, HomeDir: p.Home(), Getenv: p.Getenv})
	require.NoError(t, err)
	require.False(t, cfg.Runtime.Phase7.Filters.SegmentBloom)
	require.Equal(t, config.OriginDefault, prov[x11SwitchKey].Origin)
	var keys []string
	for _, w := range warns {
		keys = append(keys, w.Key)
	}
	require.Contains(t, keys, x11SwitchKey, "the fallback must be warned about, not silent: %v", warns)

	found := false
	for _, g := range config.MigrationGates() {
		if g.Key != x11SwitchKey {
			continue
		}
		found = true
		require.False(t, g.Passed, "M6-G16-E has not passed in this build; this row records it, it does not pass it")
		require.Equal(t, "SP-16", g.Owner)
		require.Contains(t, g.Gate, "M6-G16-E")
	}
	require.True(t, found, "the gate table must list the switch")

	on := config.Defaults()
	on.Runtime.Phase7.Filters.SegmentBloom = true
	vs := on.Validate()
	require.Len(t, vs, 1)
	require.Equal(t, x11SwitchKey, vs[0].Key)
	require.Contains(t, vs[0].Message, "has not passed in this build")
}
