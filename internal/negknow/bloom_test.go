package negknow

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// ── helpers ───────────────────────────────────────────────────────────────────────────────────

// bloomPath is <root>/.qompack/sketches/tried.bloom.
func bloomPath(root string) string {
	return filepath.Join(paths.Of(root).Sketches, sketch.TriedBloomBase)
}

// bloomBackups returns the surviving tried.bloom.<seq>.bak file names, ascending.
func bloomBackups(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Of(root).Sketches)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "tried.bloom.") && strings.HasSuffix(e.Name(), ".bak") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// writeBloomFile writes b to sketches/tried.bloom through the one sanctioned door, at generation
// seq. It is named for the file rather than for the operation so it does not read like a call to
// (*ledger).persistBloom, which is a different thing with a different contract.
func writeBloomFile(t *testing.T, root string, b *sketch.Bloom, seq int) {
	t.Helper()
	_, err := sketch.ReplaceGenerational(bloomPath(root), b, seq)
	require.NoError(t, err)
}

// seedLogFile appends recs to records/eliminations.jsonl before any ledger opens it, so a test can
// set up an on-disk history without the ledger that will read it having written it.
func seedLogFile(t *testing.T, root string, recs ...Record) {
	t.Helper()
	w, err := openLog(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	for _, r := range recs {
		require.NoError(t, appendLine(w, r))
	}
}

// seededRecord builds a complete, self-consistent Record the way the ledger would have written it.
func seededRecord(sess core.SessionID, i int, target, approach, reason string) Record {
	r := Record{
		Session:  sess,
		TS:       core.UnixMilli(i + 1),
		Target:   target,
		Approach: approach,
		Reason:   reason,
		Evidence: testEvidence(target),
		Scope:    ScopeSession,
		Status:   StatusActive,
	}
	r.Desc = Canonicalize(target, approach, reason)
	r.ID = recordID(r.Session, r.TS, r.Desc)
	return r
}

// corruptBloomFile flips one byte of the filter's body, which is what a CRC check exists to catch.
func corruptBloomFile(t *testing.T, root string) {
	t.Helper()
	p := bloomPath(root)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Greater(t, len(b), 64)
	b[len(b)-1] ^= 0xFF
	require.NoError(t, os.Chmod(p, 0o600))
	require.NoError(t, os.WriteFile(p, b, 0o600))
}

// ── RebuildBloom: active records only ─────────────────────────────────────────────────────────

func TestRebuildBloom_ActiveOnly(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	var ids []string
	descs := make([]Descriptor, 0, 10)
	for i := range 10 {
		target := fmt.Sprintf("src/a%d.ts", i)
		ids = append(ids, mustRecord(t, l, newRecord(target, target, "widen pool timeout", "why")))
		descs = append(descs, Canonicalize(target, "widen pool timeout", "why"))
	}
	require.NoError(t, l.MarkStale(context.Background(), ids[:4], []string{"dep changed"}))

	nb, health, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)
	require.Equal(t, 12, nb.Count(), "six active records contribute Key and MatchKey each")
	require.Equal(t, 6, health.Active)
	require.Equal(t, 4, health.Stale)

	for i := 4; i < 10; i++ {
		require.True(t, nb.Test(descs[i].MatchKey()), "active record %d must survive the rebuild", i)
		require.True(t, nb.Test(descs[i].Key()))
	}
	absent := 0
	for i := range 4 {
		if !nb.Test(descs[i].MatchKey()) {
			absent++
		}
	}
	require.GreaterOrEqual(t, absent, 3, "stale records are dropped; the 1%% fp rate is the only slack")
}

func TestRebuildBloom_ExcludesForeignSession(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	for i := range 3 {
		target := fmt.Sprintf("src/own%d.ts", i)
		mustRecord(t, l, newRecord(target, target, "widen pool timeout", "why"))
	}
	for i := range 2 {
		target := fmt.Sprintf("src/foreign-session%d.ts", i)
		r := newRecord(target, target, "widen pool timeout", "why")
		r.Session, r.Scope = "other", ScopeSession
		mustRecord(t, l, r)
	}
	r := newRecord("src/foreign-project.ts", "src/foreign-project.ts", "widen pool timeout", "why")
	r.Session, r.Scope = "other", ScopeProject
	mustRecord(t, l, r)

	nb, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)
	require.Equal(t, 8, nb.Count(), "three own-session plus one foreign PROJECT-scoped record, two keys each")
}

// ── capacity, resize and the on-disk size Appendix A fixes ────────────────────────────────────

func TestRebuildBloom_HonoursConfiguredCapacity(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, 10000, cfg.Sketches.Bloom.Capacity)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	seedActive(t, l, 3000, nil)

	nb, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)

	n, fp := nb.Capacity()
	require.Equal(t, 10000, n, "the configured capacity is not silently multiplied")
	require.Equal(t, cfg.Sketches.Bloom.FPRate, fp)
	require.LessOrEqual(t, nb.FillRatio(), 0.5)
	_, _, needed := nb.ResizeTarget()
	require.False(t, needed, "6 000 keys in a 10 000-entry filter needs no resize retry")
}

func TestRebuildBloom_Resizes(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	seedActive(t, l, 8000, nil)

	nb, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)

	n, _ := nb.Capacity()
	require.GreaterOrEqual(t, n, 16384, "16 000 keys round up past the configured 10 000")
	require.LessOrEqual(t, nb.FillRatio(), 0.5)
	require.Less(t, nb.EstimatedFPRate(), 0.02)
	_, _, needed := nb.ResizeTarget()
	require.False(t, needed, "one retry is enough; the rebuild never loops")
}

func TestBloomFileSize(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	seedActive(t, l, 3000, nil)

	_, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)

	info, err := os.Stat(bloomPath(root))
	require.NoError(t, err)
	require.GreaterOrEqual(t, info.Size(), int64(11264))
	require.LessOrEqual(t, info.Size(), int64(14336),
		"Appendix A: m ~ 95 850 bits ~ 12 KB, plus the sketch header and CRC")
}

// ── persistence through the one sanctioned door ───────────────────────────────────────────────

func TestRebuildBloom_Persistence(t *testing.T) {
	root, cfg := newProject(t)

	// A project whose bloom is already consistent with its records, so Open performs no rebuild of
	// its own and the generation the test observes is the one the test asked for.
	recs := []Record{
		seededRecord("sess", 0, "src/p0.ts", "widen pool timeout", "why"),
		seededRecord("sess", 1, "src/p1.ts", "widen pool timeout", "why"),
	}
	seedLogFile(t, root, recs...)
	seed := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	for _, r := range recs {
		seed.Add(r.Desc.Key())
		seed.Add(r.Desc.MatchKey())
	}
	writeBloomFile(t, root, seed, 1)
	require.Empty(t, bloomBackups(t, root), "nothing was displaced, so there is no backup yet")

	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))
	require.Equal(t, int64(0), counterValue(t, m, "negknow.bloom.rebuilds"), "the filter already agrees with the log")
	require.Equal(t, 0, l.seq, "no surviving backup means the counter resumes at zero")

	_, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"tried.bloom.1.bak"}, bloomBackups(t, root),
		"the backup carries the INCOMING seq, which is what pruneBloomBackups keeps")
	require.FileExists(t, bloomPath(root))

	lay := paths.Of(root)
	tmp, err := os.ReadDir(lay.Tmp)
	require.NoError(t, err)
	require.Empty(t, tmp, "the staging directory is left clean")
	state, err := os.ReadDir(lay.State)
	require.NoError(t, err)
	require.Empty(t, state, "there is no private rebuild-counter file to disagree with the disk")
}

func TestRebuildBloom_SeqResumesFromDisk(t *testing.T) {
	root, cfg := newProject(t)

	// A valid filter, then a surviving backup from a previous process's seventh rebuild. No
	// records, so Open's consistency check has nothing to reconcile and performs no rebuild.
	writeBloomFile(t, root, sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate), 1)
	require.NoError(t, os.WriteFile(
		filepath.Join(paths.Of(root).Sketches, "tried.bloom.7.bak"), []byte("stale generation"), 0o600))

	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	require.Equal(t, 7, l.seq, "the counter resumes from the disk, not from a private state file")

	_, _, err := l.RebuildBloom(context.Background())
	require.NoError(t, err, "the ReplaceGenerational pre-flight passes because 8 > 7")
	require.Equal(t, 8, l.seq)
	require.Equal(t, []string{"tried.bloom.8.bak"}, bloomBackups(t, root))
}

// TestRebuildBloom_PersistenceFailureReseedsSeq covers three rows of the error table at once:
// ReplaceGenerational failing during persistence, its ErrMalformed seq pre-flight specifically,
// and the rebuild still owing its WRITE afterwards (M7).
func TestRebuildBloom_PersistenceFailureReseedsSeq(t *testing.T) {
	root, cfg := newProject(t)
	loud := captureLoud(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	mustRecord(t, l, newRecord("persist", target, approach, "pgbouncer ignores it"))
	require.Empty(t, loud(), "the setup is quiet: a cold start is not a degradation")

	// A surviving backup from a generation far above this ledger's counter. ReplaceGenerational
	// pre-flights seq against it and REFUSES, before anything on disk moves — the whole point of
	// the pre-flight being a refusal rather than a report.
	require.NoError(t, os.WriteFile(
		filepath.Join(paths.Of(root).Sketches, "tried.bloom.999.bak"),
		[]byte("a much later generation"), 0o600))
	before := readBloomFile(t, root)
	require.NotEmpty(t, before)

	nb, health, err := l.RebuildBloom(context.Background())
	require.ErrorIs(t, err, sketch.ErrMalformed)

	// The filter is correct in memory even though its persistence failed, and health is real: the
	// caller is handed a usable pair alongside the error and is not obliged to fail.
	require.NotNil(t, nb)
	require.True(t, nb.Test(Canonicalize(target, approach, "").MatchKey()))
	require.Equal(t, 1, health.Records)
	require.Equal(t, 1, health.Active)

	require.Len(t, loud(), 1, "the persistence failure is loud exactly once: %v", loud())
	require.Contains(t, loud()[0], "could not persist tried.bloom")
	require.Equal(t, before, readBloomFile(t, root), "the pre-flight refused before anything moved")
	require.Equal(t, 999, l.seq, "the counter is re-seeded from the disk after ErrMalformed")
	require.True(t, l.NeedsRebuild(), "a rebuild whose write failed still owes the write (M7)")

	// And the next attempt clears the pre-flight, because the counter now resumes above the
	// surviving backup rather than below it.
	_, _, err = l.RebuildBloom(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1000, l.seq)
	require.Equal(t, []string{"tried.bloom.1000.bak"}, bloomBackups(t, root),
		"the displaced generation takes the INCOMING seq, and the stale .999 is pruned")
	require.False(t, l.NeedsRebuild())
}

// TestOpen_UnsizedBloomIsRebuilt is M5: &sketch.Bloom{} holds no bits, so Test answers false for
// everything and Add records nothing. Adopting one would switch already_tried off with no symptom.
func TestOpen_UnsizedBloomIsRebuilt(t *testing.T) {
	root, cfg := newProject(t)

	// An EMPTY project is the case that distinguishes, and it is why this is a fix rather than a
	// tidy-up. With records already on disk the undercount branch rebuilds anyway, because a
	// zero-bit filter reports Count() == 0 < 2*len(active). With NO records there is nothing to
	// reconcile against — Count() == 0 and want == 0 agree — so an adopted unsized filter is
	// simply kept, and from then on every Add into it and every Test of it is a silent no-op:
	// already_tried answers absent for eliminations this very session recorded.
	m := newMetrics()
	l := openLedger(t, root, cfg, &sketch.Bloom{}, testDeps("sess", m))

	bits, _ := l.bloom.Bits()
	require.NotZero(t, bits, "the unsized filter must be replaced by a sized one at Open")

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("unsized", target, approach, "pgbouncer ignores it"))

	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerActive, a.State, "a filter with no bits answers absent for everything")
	require.Equal(t, id, a.Record.ID)
}

// TestOpen_UnsizedBloomWithRecordsRebuilds is the same fix seen from the side where the undercount
// branch would also have caught it: the rebuild must come from the RECORDS, so the recorded
// elimination is answerable immediately.
func TestOpen_UnsizedBloomWithRecordsRebuilds(t *testing.T) {
	root, cfg := newProject(t)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	seedLogFile(t, root, seededRecord("sess", 0, target, approach, "pgbouncer ignores it"))

	m := newMetrics()
	l := openLedger(t, root, cfg, &sketch.Bloom{}, testDeps("sess", m))

	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.rebuilds"),
		"an unsized filter is a failed load, and a failed load rebuilds from the records")
	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)
}

// TestOpen_ZeroBloomConfigIsQuiet and its sibling are M8: an UNCONFIGURED sketches.bloom pair is
// defaulted silently, the way pickEnum defaults an empty enum, while a genuinely out-of-range one
// is loud. config.Load always fills these keys in, so (0, 0) only ever reaches Open from a caller
// holding a zero config.Config — and shouting at that caller trains an operator to ignore the
// channel real corruption uses.
func TestOpen_ZeroBloomConfigIsQuiet(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	loud := captureLoud(t)
	l := openLedger(t, root, config.Config{}, nil, testDeps("sess", newMetrics()))

	require.Empty(t, loud(), "an unconfigured (0, 0) pair is defaulted, not shouted at: %v", loud())
	require.NotNil(t, l.bloom)
	require.Equal(t, AnswerAbsent, mustQuery(t, l, "src/a.ts", "widen pool timeout", ScopeSession).State)
}

func TestOpen_OutOfRangeBloomConfigIsLoud(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Sketches.Bloom.FPRate = 0.9 // above the documented (0, 0.25) ceiling; sketch clamps it

	loud := captureLoud(t)
	openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	require.Len(t, loud(), 1, "a non-zero pair outside its range is a real mistake: %v", loud())
	require.Contains(t, loud()[0], "sketches.bloom values are outside their documented range")
}

func TestRebuildBloom_OneBakGeneration(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	mustRecord(t, l, newRecord("gen", "src/a.ts", "widen pool timeout", "why"))

	last := -1
	for i := range 3 {
		_, _, err := l.RebuildBloom(context.Background())
		require.NoError(t, err)
		baks := bloomBackups(t, root)
		require.Len(t, baks, 1, "exactly one generation survives rebuild %d", i)
		seq := backupSeq(t, baks[0])
		require.Greater(t, seq, last, "the backup sequence is strictly increasing")
		last = seq
	}
}

// backupSeq parses the <seq> out of tried.bloom.<seq>.bak.
func backupSeq(t *testing.T, name string) int {
	t.Helper()
	var seq int
	_, err := fmt.Sscanf(name, "tried.bloom.%d.bak", &seq)
	require.NoError(t, err)
	return seq
}

// ── §12.3: bloom load fails ───────────────────────────────────────────────────────────────────

func TestBloomLoadFailure_RebuildsFromRecords(t *testing.T) {
	root, cfg := newProject(t)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	rec := seededRecord("sess", 0, target, approach, "pgbouncer ignores it")
	seedLogFile(t, root, rec)
	seed := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	seed.Add(rec.Desc.Key())
	seed.Add(rec.Desc.MatchKey())
	writeBloomFile(t, root, seed, 1)
	corruptBloomFile(t, root)

	loud := captureLoud(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State,
		"the records are the source of truth; the filter is only a cache over them")
	require.Equal(t, []string{"sketch corrupt — rebuilding from records"}, loud(),
		"the one Loud line is sketch.LoadWithLog's, which is why Load must never be called here")
	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.corrupt_on_load"))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.rebuilds"))
}

func TestBloomLoad_ColdStartIsQuiet(t *testing.T) {
	root, cfg := newProject(t)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	seedLogFile(t, root, seededRecord("sess", 0, target, approach, "pgbouncer ignores it"))
	require.NoFileExists(t, bloomPath(root))

	loud := captureLoud(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)
	require.Empty(t, loud(), "a first session must not shout, or an operator learns to ignore the channel corruption uses")
	require.Equal(t, int64(0), counterValue(t, m, "negknow.bloom.corrupt_on_load"))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.rebuilds"))
}

func TestBloomLoadFailure_NoRecords_NeverFalsePositive(t *testing.T) {
	root, cfg := newProject(t)
	writeBloomFile(t, root, sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate), 1)
	corruptBloomFile(t, root)
	blockLogWithDirectory(t, root)

	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	require.True(t, l.blind)

	// 100 rapid checks of 10 targets each: whatever the generator produces, a ledger that cannot
	// read its records answers absent — never a false positive (§12.3).
	rapid.Check(t, func(rt *rapid.T) {
		targets := rapid.SliceOfN(rapid.String(), 10, 10).Draw(rt, "targets")
		approach := rapid.String().Draw(rt, "approach")
		for _, target := range targets {
			a, err := l.Query(context.Background(), target, approach, ScopeSession)
			if err != nil {
				rt.Fatalf("Query(%q): %v", target, err)
			}
			if a.State != AnswerAbsent || a.BloomOnly || a.Record != nil {
				rt.Fatalf("Query(%q) = %+v, want a plain absent", target, a)
			}
		}
	})
}

func TestBloomUndercount_TriggersRebuild(t *testing.T) {
	root, cfg := newProject(t)

	var recs []Record
	for i := range 6 {
		recs = append(recs, seededRecord("sess", i, fmt.Sprintf("src/u%d.ts", i), "widen pool timeout", "why"))
	}
	seedLogFile(t, root, recs...)

	// A filter built from only half the log: the missing keys would be false NEGATIVES, which is
	// the one direction that silently switches the whole feature off.
	half := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	for _, r := range recs[:3] {
		half.Add(r.Desc.Key())
		half.Add(r.Desc.MatchKey())
	}
	writeBloomFile(t, root, half, 1)

	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.rebuilds"), "Open rebuilds immediately")
	require.False(t, l.NeedsRebuild())

	for i := range 6 {
		a := mustQuery(t, l, fmt.Sprintf("src/u%d.ts", i), "widen pool timeout", ScopeSession)
		require.Equal(t, AnswerActive, a.State, "record %d", i)
	}
}

func TestBloomOvercount_SchedulesRebuild(t *testing.T) {
	root, cfg := newProject(t)

	var recs []Record
	for i := range 4 {
		recs = append(recs, seededRecord("sess", i, fmt.Sprintf("src/o%d.ts", i), "widen pool timeout", "why"))
	}
	seedLogFile(t, root, recs...)

	full := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	for _, r := range recs {
		full.Add(r.Desc.Key())
		full.Add(r.Desc.MatchKey())
	}
	for i := range 20 {
		junk := core.HashBytes("test", fmt.Appendf(nil, "junk-%d", i))
		full.Add(junk[:])
	}
	writeBloomFile(t, root, full, 1)

	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))
	require.Equal(t, int64(0), counterValue(t, m, "negknow.bloom.rebuilds"),
		"extra keys are safe: each costs one record lookup that then answers correctly")
	require.True(t, l.NeedsRebuild(), "under nextIdle the rebuild is owed, not performed")

	for i := range 4 {
		a := mustQuery(t, l, fmt.Sprintf("src/o%d.ts", i), "widen pool timeout", ScopeSession)
		require.Equal(t, AnswerActive, a.State, "record %d", i)
	}
}

// ── Health ────────────────────────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	var ids []string
	for i := range 9 {
		target := fmt.Sprintf("src/h%d.ts", i)
		ids = append(ids, mustRecord(t, l, newRecord(target, target, "widen pool timeout", "why")))
	}
	foreign := newRecord("src/h-foreign.ts", "src/h-foreign.ts", "widen pool timeout", "why")
	foreign.Session, foreign.Scope = "other", ScopeSession
	mustRecord(t, l, foreign)
	require.NoError(t, l.MarkStale(context.Background(), ids[:4], []string{"dep changed"}))

	h := l.Health()
	require.Equal(t, 10, h.Records)
	require.Equal(t, 5, h.Active, "nine own-session records less four stale; the foreign one is invisible")
	require.Equal(t, 4, h.Stale)
	require.Greater(t, h.FillRatio, 0.0)
	require.LessOrEqual(t, h.FillRatio, 0.5)
	require.Less(t, h.EstFPRate, 0.02)
	require.False(t, h.NeedsResize)
}

func TestHealth_BlindIsZeroed(t *testing.T) {
	root, cfg := newProject(t)
	blockLogWithDirectory(t, root)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	h := l.Health()
	require.Equal(t, 0.0, h.FillRatio)
	require.Equal(t, 0.0, h.EstFPRate)
	require.False(t, h.NeedsResize)

	_, _, err := l.RebuildBloom(context.Background())
	require.ErrorIs(t, err, ErrBlind, "rebuilding from records we cannot read would destroy a good on-disk cache")
	require.NoFileExists(t, bloomPath(root))
}

// ── sizing arithmetic ─────────────────────────────────────────────────────────────────────────

func TestRoundUpPow2(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{-1, 1},
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{5, 8},
		{16000, 16384},
		{16384, 16384},
		{16385, 32768},
	} {
		require.Equal(t, tc.want, roundUpPow2(tc.in), "roundUpPow2(%d)", tc.in)
	}
}

// ── source greps: the bloom is a cache over the record log and nothing else ────────────────────

// goSourceLines returns dir's non-test .go files, mapped to their comment-stripped lines. A line
// whose first non-space characters are "//" is dropped, so prose about a forbidden call does not
// read as the call itself.
func goSourceLines(t *testing.T, dir string, skip ...string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(p)
		for _, s := range skip {
			if strings.Contains(slash, s) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		var code []string
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			code = append(code, line)
		}
		out[slash] = code
		return nil
	}))
	require.NotEmpty(t, out)
	return out
}

// filesContaining returns the files whose code lines contain needle.
func filesContaining(files map[string][]string, needle string) []string {
	var out []string
	for p, lines := range files {
		for _, line := range lines {
			if strings.Contains(line, needle) {
				out = append(out, p)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestRebuildBloom_NeverFromCheckpoint(t *testing.T) {
	// internal/sketch is where sketch.RebuildBloom is DEFINED and tested; every other non-test
	// caller under internal/ is what this asserts on.
	all := goSourceLines(t, "../../internal", "internal/sketch")

	callers := filesContaining(all, "sketch.RebuildBloom(")
	require.Equal(t, []string{"../../internal/negknow/bloom.go"}, callers,
		"there is exactly one non-test sketch.RebuildBloom call site under internal/, and it is fed by visibleActive()")

	require.Empty(t, filesContaining(map[string][]string{
		"bloom.go": all["../../internal/negknow/bloom.go"],
	}, "checkpoint"), "the rebuild's input is records/eliminations.jsonl — never a checkpoint (§3.3)")

	pkg := goSourceLines(t, ".")
	require.Empty(t, filesContaining(pkg, `"github.com/qompack/qompack/internal/checkpoint"`),
		"internal/negknow does not import internal/checkpoint at all")
}

func TestRebuildBloom_UsesSanctionedDoor(t *testing.T) {
	pkg := goSourceLines(t, ".")

	require.Empty(t, filesContaining(pkg, "os.Rename("), "the rename lives in paths.ReplaceBloom, not here")
	require.Empty(t, filesContaining(pkg, "paths.ReplaceBloom("),
		"sketch.ReplaceGenerational owns the §3.3 procedure; this package re-implements none of it")

	for p, lines := range pkg {
		for i, line := range lines {
			if !strings.Contains(line, "paths.WriteAtomic(") && !strings.Contains(line, "paths.CreateNew(") {
				continue
			}
			require.False(t, strings.Contains(line, "tried.bloom") || strings.Contains(line, "TriedBloomBase"),
				"%s:%d writes tried.bloom through a door §7.4 refuses it: %s", p, i+1, line)
		}
	}

	writers := filesContaining(pkg, "sketch.ReplaceGenerational(")
	require.Equal(t, []string{"bloom.go"}, trimDirs(writers),
		"sketch.ReplaceGenerational is the only writer of tried.bloom, and bloom.go is its only call site here")
}

// trimDirs reduces paths to their base names, so an assertion reads the same from any directory.
func trimDirs(in []string) []string {
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = filepath.Base(p)
	}
	return out
}
