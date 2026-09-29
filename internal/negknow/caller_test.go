package negknow

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
)

// The daemon's ledger: ONE ledger per project, opened with no session, serving every session of the
// project through the Caller its calls carry (caller.go). These rows are the unit half of the V6
// live lane's retrieval D2, D3 and D5 findings; internal/cli's TestLive* rows drive the same
// behaviour through the shipped daemon.

// asCaller returns a context carrying sess at turn.
func asCaller(sess core.SessionID, turn core.TurnIndex) context.Context {
	return WithCaller(context.Background(), Caller{Session: sess, Turn: turn})
}

// daemonLedger opens a ledger the way the daemon does: no session of its own.
func daemonLedger(t *testing.T, st *fakeStore) (*ledger, string) {
	t.Helper()
	root, cfg := newProject(t)
	deps := testDeps("", newMetrics())
	if st != nil {
		deps.Store = st
	}
	return openLedger(t, root, cfg, nil, deps), root
}

// TestLedger_CallerSessionStampsRecordAndScopesQuery is D2: a session-scoped record written for a
// caller carries that caller's session, answers that session, and does not answer another one.
func TestLedger_CallerSessionStampsRecordAndScopesQuery(t *testing.T) {
	l, _ := daemonLedger(t, nil)

	id, err := l.Record(asCaller("sess-a", 3), newRecord("caller", "src/pool.go:DialPool", "widen pool timeout", "max_idle"))
	require.NoError(t, err)
	rec, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, core.SessionID("sess-a"), rec.Session, "the record belongs to the session that made it")
	require.Equal(t, ScopeSession, rec.Scope)

	own, err := l.Query(asCaller("sess-a", 4), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, own.State, "the recording session sees its own record")

	other, err := l.Query(asCaller("sess-b", 1), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerAbsent, other.State, "another session must not see a session-scoped record")
	require.True(t, other.BloomOnly, "the filter holds its key; the record lookup is what refuses it")

	active, err := l.Active(asCaller("sess-b", 1), ScopeSession)
	require.NoError(t, err)
	require.Empty(t, active, "Active answers for the caller's session too")
	active, err = l.Active(asCaller("sess-a", 1), ScopeSession)
	require.NoError(t, err)
	require.Len(t, active, 1)
}

// TestLedger_ProjectScopeIsVisibleToEverySession: project scope is the cross-session carry-over.
func TestLedger_ProjectScopeIsVisibleToEverySession(t *testing.T) {
	l, _ := daemonLedger(t, nil)
	r := newRecord("project", "src/cache.go", "drop the cache", "the cache is load-bearing")
	r.Scope = ScopeProject
	_, err := l.Record(asCaller("sess-a", 1), r)
	require.NoError(t, err)

	got, err := l.Query(asCaller("sess-b", 1), "src/cache.go", "drop the cache", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State)
}

// TestLedger_MultiSessionReopenKeepsEverySessionsKeys: the filter a daemon ledger rebuilds from its
// records holds every session's keys. Rebuilt from the records visible to "no session" alone, a
// session's own eliminations dropped out of tried.bloom at the next Open and answered absent.
func TestLedger_MultiSessionReopenKeepsEverySessionsKeys(t *testing.T) {
	root, cfg := newProject(t)
	first := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	_, err := first.Record(asCaller("sess-a", 2), newRecord("reopen", "src/pool.go:DialPool", "widen pool timeout", "max_idle"))
	require.NoError(t, err)
	_, _, err = first.RebuildBloom(context.Background())
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	got, err := second.Query(asCaller("sess-a", 5), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State, "the recording session must still see its record after a reopen")
	require.Equal(t, 1, second.Health().Active, "a daemon ledger counts every session's active records")
}

// TestLedger_BoundLedgerKeepsItsOwnView: a ledger opened FOR a session (every pre-daemon caller)
// keeps answering for that session when a call carries no Caller.
func TestLedger_BoundLedgerKeepsItsOwnView(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess-bound", newMetrics()))
	id := mustRecord(t, l, newRecord("bound", "src/a.go", "inline the helper", "it recurses"))
	rec, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, core.SessionID("sess-bound"), rec.Session)
	require.Equal(t, AnswerActive, mustQuery(t, l, "src/a.go", "inline the helper", ScopeSession).State)
}

// TestLedger_RecordEmitsNodeAtCallerTurn is the D5 half negknow owns: the elimination node carries
// the turn it was recorded at, so a checkpoint's turn-ordered decision extraction can find it.
func TestLedger_RecordEmitsNodeAtCallerTurn(t *testing.T) {
	root, cfg := newProject(t)
	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(t, err)
	deps := testDeps("", newMetrics())
	deps.Graph = g
	l := openLedger(t, root, cfg, nil, deps)

	id, err := l.Record(asCaller("sess-a", 17), newRecord("turn", "src/pool.go", "widen pool timeout", "max_idle"))
	require.NoError(t, err)
	rec, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	n, ok := g.Node(NodeIDFor(rec))
	require.True(t, ok)
	require.Equal(t, core.TurnIndex(17), n.Turn, "the node is placed at the caller's turn, not turn 0")
}

// TestQuery_SeesADependencyChangeWithoutARefresh is D3: once the store holds a new version of a
// dependency, the next Query answers stale — and persists the flip — without waiting for
// RefreshStaleness at the next idle window or restart.
func TestQuery_SeesADependencyChangeWithoutARefresh(t *testing.T) {
	st := newFakeStore()
	l, root := daemonLedger(t, st)
	dep := core.Dep{Path: "config/pool.yaml", Hash: composeHash(t)}
	r := newRecord("d3", "src/pool.go:DialPool", "widen pool timeout", "max_idle")
	r.DependsOn = []Dep{dep}
	id, err := l.Record(asCaller("sess-a", 1), r)
	require.NoError(t, err)

	ctx := asCaller("sess-a", 2)
	got, err := l.Query(ctx, "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State, "nothing has changed yet")

	st.reportChanged(dep)
	got, err = l.Query(ctx, "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State, "the captured change must reach the next answer")
	require.Equal(t, []string{"config/pool.yaml: dependency hash changed from sha256:11aa22bb33cc"},
		got.Record.StaleBecause)

	rec, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, StatusStale, rec.Status, "the flip is the ledger's, not only the answer's")
	raw, err := os.ReadFile(logPath(root))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"op":"stale"`, "and it is on disk, so the next Open agrees")
}

// TestQuery_OnlyComparesTheRecordsItCouldAnswerFrom keeps the read-time refresh cheap: one
// ChangedSince over the matching record's dependencies, never the whole ledger's.
func TestQuery_OnlyComparesTheRecordsItCouldAnswerFrom(t *testing.T) {
	st := newFakeStore()
	l, _ := daemonLedger(t, st)
	mine := core.Dep{Path: "config/pool.yaml", Hash: composeHash(t)}
	theirs := core.Dep{Path: "config/cache.yaml", Hash: core.HashBytes("test", []byte("cache"))}
	r := newRecord("mine", "src/pool.go:DialPool", "widen pool timeout", "max_idle")
	r.DependsOn = []Dep{mine}
	_, err := l.Record(asCaller("sess-a", 1), r)
	require.NoError(t, err)
	o := newRecord("other", "src/cache.go", "drop the cache", "load-bearing")
	o.DependsOn = []Dep{theirs}
	_, err = l.Record(asCaller("sess-a", 1), o)
	require.NoError(t, err)

	before := st.callCount()
	_, err = l.Query(asCaller("sess-a", 2), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	calls := st.calls()[before:]
	require.Len(t, calls, 1, "one comparison per query")
	require.Equal(t, []core.Dep{mine}, calls[0], "over the matching record's dependencies only")
}

// TestQuery_AFailedComparisonIsUncertainNotActive: a query whose own dependency comparison failed
// cannot back an active answer (§11.3 invariant 8), and must not claim absence either.
func TestQuery_AFailedComparisonIsUncertainNotActive(t *testing.T) {
	st := newFakeStore()
	l, _ := daemonLedger(t, st)
	r := newRecord("err", "src/pool.go:DialPool", "widen pool timeout", "max_idle")
	r.DependsOn = []Dep{{Path: "config/pool.yaml", Hash: composeHash(t)}}
	_, err := l.Record(asCaller("sess-a", 1), r)
	require.NoError(t, err)

	st.mu.Lock()
	st.changedErr = errors.New("store unavailable")
	st.mu.Unlock()
	got, err := l.Query(asCaller("sess-a", 2), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerUncertain, got.State)
	require.Equal(t, reasonDepCoverage, got.Coverage.Reason)
	require.NotNil(t, got.Record, "the record is on file; only its freshness is unconfirmed")
	require.True(t, strings.Contains(got.Coverage.Recovery, "retry"))
}

// TestQuery_ARecheckConfirmsFreshnessDespiteTheWatermark: the ledger-wide watermark says the last
// FULL refresh could not compare; a query that just compared its own record's dependencies
// successfully has confirmed that record's freshness, and answers active.
func TestQuery_ARecheckConfirmsFreshnessDespiteTheWatermark(t *testing.T) {
	st := newFakeStore()
	l, _ := daemonLedger(t, st)
	r := newRecord("watermark", "src/pool.go:DialPool", "widen pool timeout", "max_idle")
	r.DependsOn = []Dep{{Path: "config/pool.yaml", Hash: composeHash(t)}}
	_, err := l.Record(asCaller("sess-a", 1), r)
	require.NoError(t, err)

	failing := newFakeStore()
	failing.changedErr = errors.New("store unavailable")
	_, err = l.RefreshStaleness(context.Background(), failing)
	require.Error(t, err)
	require.NotZero(t, l.Health().DependencyCoverage, "fixture sanity: the watermark is set")

	got, err := l.Query(asCaller("sess-a", 2), "src/pool.go:DialPool", "widen pool timeout", ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerActive, got.State, "the query's own successful comparison backs the answer")
}
