package negknow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// composeHash is the dependency hash the §8.3 golden `stale` line is written against: its Short()
// form is the "sha256:11aa22bb33cc" the reason string below quotes. core.Hash.Short() returns the
// first twelve hex characters WITHOUT a prefix, so the "sha256:" in the reason is written by the
// format string in RefreshStaleness, not by the hash.
func composeHash(t *testing.T) core.Hash {
	t.Helper()
	return mustHash(t, "sha256:11aa22bb33cc"+strings.Repeat("0", 52))
}

// recordWithDeps records one elimination carrying deps.
func recordWithDeps(t *testing.T, l *ledger, name, target string, deps ...core.Dep) string {
	t.Helper()
	r := newRecord(name, target, "widen pool timeout", "why")
	r.DependsOn = deps
	return mustRecord(t, l, r)
}

func TestRefreshStaleness_Flips(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	dep := core.Dep{Path: "docker-compose.yml", Hash: composeHash(t)}
	var ids []string
	for i := range 3 {
		ids = append(ids, recordWithDeps(t, l, fmt.Sprintf("flip-%d", i), fmt.Sprintf("src/f%d.ts", i), dep))
	}

	fake := newFakeStore().reportChanged(dep)
	flipped, err := l.RefreshStaleness(context.Background(), fake)
	require.NoError(t, err)

	want := append([]string(nil), ids...)
	sortStrings(want)
	require.Equal(t, want, flipped, "every flipped id, sorted ascending")

	for _, id := range ids {
		got, err := l.Get(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, StatusStale, got.Status)
		require.Equal(t,
			[]string{"docker-compose.yml: dependency hash changed from sha256:11aa22bb33cc"},
			got.StaleBecause)
		require.NotZero(t, got.StaleSince)
	}
	require.Equal(t, int64(3), counterValue(t, m, "negknow.stale.flipped"))
}

func TestRefreshStaleness_NoChange(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	dep := core.Dep{Path: "docker-compose.yml", Hash: composeHash(t)}
	recordWithDeps(t, l, "nochange", "src/a.ts", dep)
	lines := logLineCount(t, root)

	flipped, err := l.RefreshStaleness(context.Background(), newFakeStore())
	require.NoError(t, err)
	require.Nil(t, flipped)
	require.Equal(t, lines, logLineCount(t, root), "nothing flipped, nothing appended")
	require.Equal(t, 1, l.Health().Active)
}

func TestRefreshStaleness_SingleChangedSinceCall(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		records      = 5000
		distinctDeps = 2000
	)
	deps := make([]core.Dep, distinctDeps)
	for i := range deps {
		deps[i] = core.Dep{
			Path: fmt.Sprintf("dep/%04d.yml", i),
			Hash: core.HashBytes("test", fmt.Appendf(nil, "dep-%d", i)),
		}
	}
	// Seeded straight into the index: this test's subject is the ONE ChangedSince call and the
	// shape of its argument, not the append path, and 5 000 real appends buy no assertion here.
	seedActive(t, l, records, func(i int) []Dep { return []Dep{deps[i%distinctDeps]} })

	fake := newFakeStore()
	_, err := l.RefreshStaleness(context.Background(), fake)
	require.NoError(t, err)

	calls := fake.calls()
	require.Len(t, calls, 1, "RefreshStaleness makes exactly one ChangedSince call")
	got := calls[0]
	require.Len(t, got, distinctDeps, "the union is deduplicated")
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		require.True(t,
			prev.Path < cur.Path || (prev.Path == cur.Path && prev.Hash.String() < cur.Hash.String()),
			"deps are path-ascending then hash-ascending: %q then %q", prev.Path, cur.Path)
	}
}

func TestRefreshStaleness_GroupsByBecause(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	x := core.Dep{Path: "x.yml", Hash: core.HashBytes("test", []byte("x"))}
	y := core.Dep{Path: "y.yml", Hash: core.HashBytes("test", []byte("y"))}
	a := recordWithDeps(t, l, "grp-a", "src/a.ts", x)
	b := recordWithDeps(t, l, "grp-b", "src/b.ts", x)
	c := recordWithDeps(t, l, "grp-c", "src/c.ts", y)

	fake := newFakeStore().reportChanged(x, y)
	flipped, err := l.RefreshStaleness(context.Background(), fake)
	require.NoError(t, err)
	require.Len(t, flipped, 3)

	// The grouping itself is asserted directly, because "MarkStale ran exactly twice" is not
	// observable from outside the ledger: MarkStale appends one control line per record whatever
	// the grouping, so a line count cannot tell one call of two records from two calls of one.
	// groupStaleReasons IS the decision, so it is what the exact-match assertion is written
	// against; the end-to-end flip above proves it is wired in.
	xBecause := "x.yml: dependency hash changed from sha256:" + x.Hash.Short()
	yBecause := "y.yml: dependency hash changed from sha256:" + y.Hash.Short()
	groups := groupStaleReasons(map[string][]string{
		b: {xBecause},
		a: {xBecause},
		c: {yBecause},
	})
	wantGroups := []staleGroup{
		{IDs: sortedStrings(a, b), Because: []string{xBecause}},
		{IDs: []string{c}, Because: []string{yBecause}},
	}
	if xBecause > yBecause {
		wantGroups[0], wantGroups[1] = wantGroups[1], wantGroups[0]
	}
	require.Empty(t, cmp.Diff(wantGroups, groups), "exactly two groups, ascending by joined because, ids sorted within")

	for _, id := range []string{a, b} {
		got, err := l.Get(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, []string{xBecause}, got.StaleBecause)
	}
	gotC, err := l.Get(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, []string{yBecause}, gotC.StaleBecause)
}

func TestRefreshStaleness_MultiDep(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	x := core.Dep{Path: "a-first.yml", Hash: core.HashBytes("test", []byte("x"))}
	y := core.Dep{Path: "b-second.yml", Hash: core.HashBytes("test", []byte("y"))}
	id := recordWithDeps(t, l, "multi", "src/a.ts", y, x)

	before := logLineCount(t, root)
	fake := newFakeStore().reportChanged(x, y)
	flipped, err := l.RefreshStaleness(context.Background(), fake)
	require.NoError(t, err)
	require.Equal(t, []string{id}, flipped)
	require.Equal(t, before+1, logLineCount(t, root), "one stale control line for one record")

	got, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, []string{
		"a-first.yml: dependency hash changed from sha256:" + x.Hash.Short(),
		"b-second.yml: dependency hash changed from sha256:" + y.Hash.Short(),
	}, got.StaleBecause)
}

func TestRefreshStaleness_NilStore(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	recordWithDeps(t, l, "nilstore", "src/a.ts",
		core.Dep{Path: "x.yml", Hash: core.HashBytes("test", []byte("x"))})

	flipped, err := l.RefreshStaleness(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, flipped)
	require.Equal(t, 1, l.Health().Active)
}

func TestRefreshStaleness_StoreError(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	r := newRecord("storeerr", target, approach, "pgbouncer ignores it")
	r.DependsOn = []core.Dep{{Path: "x.yml", Hash: core.HashBytes("test", []byte("x"))}}
	mustRecord(t, l, r)

	boom := errors.New("store is on fire")
	fake := newFakeStore()
	fake.changedErr = boom

	flipped, err := l.RefreshStaleness(context.Background(), fake)
	require.ErrorIs(t, err, boom)
	require.Nil(t, flipped)
	require.Equal(t, 1, l.Health().Active, "failing toward doing nothing")
	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)
}

// ── MarkStale ─────────────────────────────────────────────────────────────────────────────────

func TestMarkStale_Idempotent(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	id := mustRecord(t, l, newRecord("markstale", "src/a.ts", "widen pool timeout", "why"))
	ctx := context.Background()

	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"first"}))
	lines := logLineCount(t, root)
	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"second"}))

	require.Equal(t, lines, logLineCount(t, root), "an already-stale record is skipped, not re-flipped")
	require.Equal(t, int64(1), counterValue(t, m, "negknow.stale.flipped"))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.stale.skipped"))

	got, err := l.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, got.StaleBecause, "because is replaced, never appended to")
	require.Equal(t, 1, l.Health().Stale)
}

func TestMarkStale_UnknownID(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))
	lines := logLineCount(t, root)

	require.NoError(t, l.MarkStale(context.Background(), []string{"elim_nosuchrecord"}, []string{"x"}))
	require.Equal(t, lines, logLineCount(t, root))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.stale.skipped"))
	require.Equal(t, int64(0), counterValue(t, m, "negknow.stale.flipped"))
}

// ── rebuildOnStale, all three Appendix C values ───────────────────────────────────────────────

func TestRebuildOnStale_Immediate(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RebuildOnStale = "immediate"
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	id := mustRecord(t, l, newRecord("imm", "src/a.ts", "widen pool timeout", "why"))
	before := counterValue(t, m, "negknow.bloom.rebuilds")
	beforeSeq := l.seq

	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"dep changed"}))

	require.Equal(t, before+1, counterValue(t, m, "negknow.bloom.rebuilds"),
		"immediate rebuilds during MarkStale")
	require.Greater(t, l.seq, beforeSeq, "the rebuild burned a generation, i.e. it wrote through the sanctioned door")
	require.False(t, l.NeedsRebuild())
	require.Len(t, bloomBackups(t, root), 1, "exactly one surviving .bak generation")
}

func TestRebuildOnStale_NextIdle(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, "nextIdle", cfg.Eliminations.RebuildOnStale)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("nextidle", target, approach, "pgbouncer ignores it"))

	before := counterValue(t, m, "negknow.bloom.rebuilds")
	beforeBytes := readBloomFile(t, root)

	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"dep changed"}))

	require.Equal(t, before, counterValue(t, m, "negknow.bloom.rebuilds"), "nextIdle defers the rebuild")
	require.True(t, l.NeedsRebuild())
	require.Equal(t, beforeBytes, readBloomFile(t, root), "tried.bloom is untouched until the idle window")
	require.Equal(t, AnswerStale, mustQuery(t, l, target, approach, ScopeSession).State)
}

func TestRebuildOnStale_Never(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RebuildOnStale = "never"
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	// Open's own consistency check is a correctness branch and runs whatever rebuildOnStale says;
	// what "never" governs is everything after it.
	after := counterValue(t, m, "negknow.bloom.rebuilds")

	id := mustRecord(t, l, newRecord("never", "src/a.ts", "widen pool timeout", "why"))
	require.False(t, l.NeedsRebuild())
	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"dep changed"}))

	require.False(t, l.NeedsRebuild())
	require.Equal(t, after, counterValue(t, m, "negknow.bloom.rebuilds"), "never means never, after Open")
}

func TestMaintenanceTask_Shape(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	dep := core.Dep{Path: "docker-compose.yml", Hash: composeHash(t)}
	id := recordWithDeps(t, l, "maint", "src/a.ts", dep)
	require.True(t, l.NeedsRebuild(), "an appended record owes a rebuild under nextIdle")

	fake := newFakeStore().reportChanged(dep)
	name, prio, fn := l.MaintenanceTask(fake)
	require.Equal(t, "negknow.maintain", name)
	require.Equal(t, 30, prio)
	require.NotNil(t, fn)

	require.NoError(t, fn(context.Background()))
	got, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, StatusStale, got.Status, "fn refreshes staleness")
	require.False(t, l.NeedsRebuild(), "and then rebuilds")
	rebuilds := counterValue(t, m, "negknow.bloom.rebuilds")

	require.NoError(t, fn(context.Background()))
	require.Equal(t, rebuilds, counterValue(t, m, "negknow.bloom.rebuilds"), "the second call is a no-op")
	require.False(t, l.NeedsRebuild())
}

func TestOpenRefreshBounded(t *testing.T) {
	root, cfg := newProject(t)

	// A store whose ChangedSince never answers within Open's window. The fake waits on ctx.Done()
	// as well as on its own timer, so the 250 ms openRefreshDeadline is what returns first and no
	// goroutine is left blocked for the full two seconds.
	fake := newFakeStore()
	fake.blockChangedSince = 2 * time.Second

	deps := testDeps("sess", newMetrics())
	deps.Store = fake

	start := time.Now()
	l := openLedger(t, root, cfg, nil, deps)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 500*time.Millisecond, "Open is bounded by openRefreshDeadline, not by the store")
	require.GreaterOrEqual(t, elapsed, openRefreshDeadline/2, "the bounded refresh really ran")
	require.True(t, l.NeedsRebuild(), "the daemon's idle task finishes what Open could not")
	require.Equal(t, 1, fake.callCount())
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────────

// readBloomFile returns the current bytes of sketches/tried.bloom, or nil when there is none.
func readBloomFile(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(bloomPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return b
}
