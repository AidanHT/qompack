package negknow

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// ── shared fixtures ───────────────────────────────────────────────────────────────────────────
//
// These live in ledger_test.go because staleness_test.go, bloom_test.go and dagedges_test.go all
// build on them and all four files are one test binary.
//
// internal/testutil is the house project fixture and the plan's test tables name it, but testutil
// imports internal/cli -> internal/daemon -> internal/negknow, so an INTERNAL test of this package
// cannot reach it without an import cycle (ruling R16). newProject below is the substitute: a real
// .qompack/ layout plus the REAL Appendix C defaults, read through config.Load rather than
// hand-written, so no default number is transcribed into a test either.

// stepClock is a Clock a test can move. log_test.go's frozenClock is the package's frozen clock
// and is reused wherever nothing needs time to pass; this is the other case — the record dedup is
// identity-based precisely so that an MCP retry a second later collapses, and proving that needs a
// clock that actually advances.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newProject returns a fresh project root with the .qompack/ layout created and the real, fully
// defaulted configuration loaded for it.
func newProject(t *testing.T) (string, config.Config) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(dir)))
	cfg, _, _, err := config.Load(config.Env{
		ProjectRoot: dir,
		Getenv:      func(string) string { return "" },
	})
	require.NoError(t, err)
	return dir, cfg
}

// openLedger opens a ledger and registers its Close.
//
// The Close is not optional and not tidiness: Windows refuses to unlink a file with an open
// handle, so a ledger left open makes t.TempDir()'s own RemoveAll cleanup fail the test with a
// message that says nothing about the ledger.
func openLedger(t *testing.T, root string, cfg config.Config, b *sketch.Bloom, deps Deps) *ledger {
	t.Helper()
	led, err := Open(root, cfg, b, deps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	l, ok := led.(*ledger)
	require.True(t, ok, "Open must return this package's own *ledger")
	return l
}

// testEvidence mints the non-zero evidence hash every test Record needs: the real default is
// eliminations.requireEvidence = true, so a Record without one is refused.
func testEvidence(name string) core.Hash { return core.HashBytes("test", []byte(name)) }

// newRecord builds a Record with evidence keyed on name. Scope/Status/Session/TS/ID are left zero
// so the ledger's own defaulting is what fills them in.
func newRecord(name, target, approach, reason string) Record {
	return Record{Target: target, Approach: approach, Reason: reason, Evidence: testEvidence(name)}
}

// mustRecord records r and returns its id.
func mustRecord(t *testing.T, l *ledger, r Record) string {
	t.Helper()
	id, err := l.Record(context.Background(), r)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	return id
}

// mustQuery queries and requires no error.
func mustQuery(t *testing.T, l *ledger, target, approach string, scope Scope) Answer {
	t.Helper()
	a, err := l.Query(context.Background(), target, approach, scope)
	require.NoError(t, err)
	return a
}

// deps builds a Deps with a real registry and a frozen clock, which is what most tests want.
func testDeps(sess core.SessionID, m obs.Registry) Deps {
	return Deps{Session: sess, Log: logging.Nop(), Metrics: m, Clock: frozenClock{}}
}

// capturingLogger records every level's messages, so a test can assert on a Warn the ledger is
// required to write. logging.New writes to real files and logging.Nop discards everything, so
// neither answers "was this logged?"; Loud has AttachLoudObserver, and the other levels have this.
type capturingLogger struct {
	mu   *sync.Mutex
	msgs *[]string
	kv   []any
}

func newCapturingLogger() capturingLogger {
	return capturingLogger{mu: &sync.Mutex{}, msgs: &[]string{}}
}

func (c capturingLogger) record(lvl, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.msgs = append(*c.msgs, lvl+" "+msg)
}

// lines returns every message captured so far, each prefixed by its level.
func (c capturingLogger) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(*c.msgs))
	copy(out, *c.msgs)
	return out
}

func (c capturingLogger) With(kv ...any) logging.Logger {
	return capturingLogger{mu: c.mu, msgs: c.msgs, kv: append(append([]any(nil), c.kv...), kv...)}
}
func (c capturingLogger) Debug(msg string, kv ...any) { c.record("debug", msg) }
func (c capturingLogger) Info(msg string, kv ...any)  { c.record("info", msg) }
func (c capturingLogger) Warn(msg string, kv ...any)  { c.record("warn", msg) }
func (c capturingLogger) Error(msg string, kv ...any) { c.record("error", msg) }
func (c capturingLogger) Loud(msg string, kv ...any)  { c.record("loud", msg) }

// captureLoud installs a process-wide Loud observer for the duration of the test and returns an
// accessor for every Loud message raised since.
//
// logging.LastLoud() is the ring /qompack:status reads, but it is a 32-entry window shared by the
// whole process, so counting against it is only reliable while nothing else in the binary is
// shouting. The observer is exact. Tests in one package run sequentially unless they opt into
// t.Parallel, and none here does, so installing a process-wide observer is safe.
func captureLoud(t *testing.T) func() []string {
	t.Helper()
	var (
		mu   sync.Mutex
		msgs []string
	)
	logging.AttachLoudObserver(func(msg string, kv ...any) {
		mu.Lock()
		defer mu.Unlock()
		msgs = append(msgs, msg)
	})
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(msgs))
		copy(out, msgs)
		return out
	}
}

// seedActive injects n synthetic active records straight into the ledger's in-memory index,
// bypassing the append path.
//
// The tests that use it are about RebuildBloom's SIZING — the configured capacity being honoured,
// the single resize retry, the Appendix A on-disk size — and about RefreshStaleness's one-call
// property, none of which observes the log. Writing thousands of JSONL lines per test to reach
// those record counts would make the package minutes long on Windows in exchange for no
// assertion; the semantics of Record itself are covered by the ledger_test.go table above.
func seedActive(t *testing.T, l *ledger, n int, depsFor func(i int) []Dep) {
	t.Helper()
	for i := range n {
		r := Record{
			ID:       fmt.Sprintf("elim_seed%06d", i),
			Session:  l.deps.Session,
			TS:       core.UnixMilli(i + 1),
			Target:   fmt.Sprintf("src/seed%06d.ts", i),
			Approach: "widen pool timeout",
			Reason:   "why",
			Evidence: testEvidence("seed"),
			Scope:    ScopeSession,
			Status:   StatusActive,
		}
		if depsFor != nil {
			r.DependsOn = depsFor(i)
		}
		r.Desc = Canonicalize(r.Target, r.Approach, r.Reason)
		idx := len(l.recs)
		l.recs = append(l.recs, r)
		l.byID[r.ID] = idx
		mh := r.Desc.MatchHex()
		l.byMatch[mh] = append(l.byMatch[mh], idx)
		l.byKey[dedupHex(r)] = idx
	}
}

// sortStrings sorts in place, ascending.
func sortStrings(s []string) { sort.Strings(s) }

// sortedStrings returns its arguments sorted ascending.
func sortedStrings(s ...string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// logLineCount returns the number of lines currently in the project's elimination log.
func logLineCount(t *testing.T, root string) int {
	t.Helper()
	b, err := os.ReadFile(logPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	if len(b) == 0 {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
}

// ── Query: the three-way already_tried response ───────────────────────────────────────────────

func TestQuery_Absent(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	a := mustQuery(t, l, "src/a.ts", "widen timeout", ScopeSession)
	require.Equal(t, AnswerAbsent, a.State)
	require.Nil(t, a.Record)
	require.False(t, a.BloomOnly)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.query.absent"))
}

func TestQuery_Active(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
		reason   = "pgbouncer 1.18 ignores it in transaction mode"
	)
	mustRecord(t, l, newRecord("active", target, approach, reason))

	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerActive, a.State)
	require.NotNil(t, a.Record)
	require.Equal(t, reason, a.Record.Reason)
	require.Equal(t, "", a.Note)
	require.False(t, a.BloomOnly)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.query.active"))
}

func TestQuery_ActiveViaSynonym(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const target = "src/auth.ts:refreshToken"
	mustRecord(t, l, newRecord("syn", target, "widen pool timeout", "pgbouncer ignores it"))

	a := mustQuery(t, l, target, "Increasing the connection-pool timeouts", ScopeSession)
	require.Equal(t, AnswerActive, a.State, "the approach-class canonicalization is what makes this work")
	require.NotNil(t, a.Record)
}

func TestQuery_Stale_FlagNote(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, "flag", cfg.Eliminations.StaleResponse)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
		reason   = "pgbouncer 1.18 ignores it in transaction mode"
	)
	id := mustRecord(t, l, newRecord("stale-flag", target, approach, reason))
	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"compose changed"}))

	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerStale, a.State)
	require.NotNil(t, a.Record)
	require.Equal(t, reason, a.Record.Reason)
	// The §8.3 item 4 sentence, em dash included, compared against the literal rather than against
	// the constant alone: exported or not, a paraphrase here is a change to the wire contract
	// SP-11 and SP-13 render.
	require.Equal(t,
		"previously eliminated, but the evidence has changed since — re-verification may be warranted",
		a.Note)
	require.Equal(t, StaleNote, a.Note)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.query.stale"))
}

func TestQuery_Stale_Drop(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.StaleResponse = "drop"
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("stale-drop", target, approach, "pgbouncer ignores it"))
	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"compose changed"}))

	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerAbsent, a.State)
	require.Nil(t, a.Record)
	require.Equal(t, "", a.Note)
}

func TestQuery_StaleBeforeRebuild(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, "nextIdle", cfg.Eliminations.RebuildOnStale)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("stale-norebuild", target, approach, "pgbouncer ignores it"))
	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"compose changed"}))
	require.True(t, l.NeedsRebuild(), "nextIdle owes a rebuild rather than performing one")

	// No rebuild has run: the stale record's keys are still in the filter, and that is exactly why
	// nextIdle is safe — the record lookup, not the bloom, decides the answer.
	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerStale, a.State)
	require.Equal(t, StaleNote, a.Note)
}

func TestQuery_BloomOnly(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()

	// A key in the filter that no record backs: the bloom false positive §13 invariant 3 exists to
	// make visible rather than to hide.
	seeded := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	ghost := Canonicalize("src/ghost.ts", "widen pool timeout", "")
	seeded.Add(ghost.MatchKey())

	l := openLedger(t, root, cfg, seeded, testDeps("sess", m))

	a := mustQuery(t, l, "src/ghost.ts", "widen pool timeout", ScopeSession)
	require.Equal(t, AnswerAbsent, a.State)
	require.True(t, a.BloomOnly)
	require.Nil(t, a.Record)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.query.bloom_only"))
}

// TestQuery_UnknownStatusIsBloomOnly covers the fourth shape a bloom hit can resolve to: a
// visible record that is neither active nor stale, because the log carried a status no version of
// this package mints. Returning AnswerStale with a nil Record — which is what a naive "everything
// left is stale" branch does — would hand SP-13 a state whose contract promises a reason (R20).
func TestQuery_UnknownStatusIsBloomOnly(t *testing.T) {
	root, cfg := newProject(t)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	rec := seededRecord("sess", 0, target, approach, "pgbouncer ignores it")
	rec.Status = "retracted" // a newer plugin version, or a hand-edited log
	seedLogFile(t, root, rec)

	// The filter still carries the record's keys, which is the whole point: without a bloom hit
	// there is nothing for the record lookup to resolve.
	seed := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	seed.Add(rec.Desc.Key())
	seed.Add(rec.Desc.MatchKey())
	writeBloomFile(t, root, seed, 1)

	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerAbsent, a.State)
	require.True(t, a.BloomOnly, "the bloom hit is real; the record behind it is not an answer")
	require.Nil(t, a.Record)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.query.bloom_only"))

	// The status is left exactly as the log spelled it — rewriting it at materialization would
	// destroy the forward compatibility the unknown-op rule exists to provide.
	got, err := l.Get(context.Background(), rec.ID)
	require.NoError(t, err)
	require.Equal(t, Status("retracted"), got.Status)

	h := l.Health()
	require.Equal(t, 1, h.Records, "an unknown status is still a materialized record")
	require.Equal(t, 0, h.Active, "but it is neither active")
	require.Equal(t, 0, h.Stale, "nor stale")
}

func TestQuery_PrefersActiveOverStale(t *testing.T) {
	root, cfg := newProject(t)
	clk := newStepClock()
	deps := testDeps("sess", newMetrics())
	deps.Clock = clk
	l := openLedger(t, root, cfg, nil, deps)

	const target = "src/auth.ts:refreshToken"
	// Same MatchKey (same path/symbol/approach class), different reasons, so the two records are
	// distinct identities that both answer the same query.
	activeID := mustRecord(t, l, newRecord("pref-active", target, "widen pool timeout", "reason one"))
	clk.advance(time.Minute)
	staleID := mustRecord(t, l, newRecord("pref-stale", target, "widen pool timeout", "reason two"))
	require.NotEqual(t, activeID, staleID)
	require.NoError(t, l.MarkStale(context.Background(), []string{staleID}, []string{"dep changed"}))

	a := mustQuery(t, l, target, "widen pool timeout", ScopeSession)
	require.Equal(t, AnswerActive, a.State, "a later stale record must not shadow an earlier active one")
	require.Equal(t, activeID, a.Record.ID)
}

// scopeCarryOver records one record under sess-A, rebuilds so tried.bloom carries its keys, closes,
// and returns the project root and config so a second session can reopen it.
func scopeCarryOver(t *testing.T, scope Scope, rebuildOnStale string) (root string, cfg config.Config, target, approach string) {
	t.Helper()
	root, cfg = newProject(t)
	cfg.Eliminations.RebuildOnStale = rebuildOnStale
	target, approach = "src/auth.ts:refreshToken", "widen pool timeout"

	a := openLedger(t, root, cfg, nil, testDeps("sess-A", newMetrics()))
	r := newRecord("carry", target, approach, "pgbouncer ignores it")
	r.Scope = scope
	mustRecord(t, a, r)
	// Persist sess-A's keys, so the question the reopen answers is about the RECORDS, not about a
	// filter that happened to be empty.
	_, _, err := a.RebuildBloom(context.Background())
	require.NoError(t, err)
	require.NoError(t, a.Close())
	return root, cfg, target, approach
}

func TestQuery_ScopeSession_OtherSessionHidden(t *testing.T) {
	root, cfg, target, approach := scopeCarryOver(t, ScopeSession, "immediate")

	b := openLedger(t, root, cfg, nil, testDeps("sess-B", newMetrics()))
	got := mustQuery(t, b, target, approach, ScopeSession)
	require.Equal(t, AnswerAbsent, got.State)
	require.False(t, got.BloomOnly,
		"Open rebuilt from visibleActive(), which excludes the foreign session's keys")
}

func TestQuery_ScopeSession_OtherSessionHidden_NextIdle(t *testing.T) {
	root, cfg, target, approach := scopeCarryOver(t, ScopeSession, "nextIdle")

	b := openLedger(t, root, cfg, nil, testDeps("sess-B", newMetrics()))
	got := mustQuery(t, b, target, approach, ScopeSession)
	require.Equal(t, AnswerAbsent, got.State)
	require.True(t, got.BloomOnly,
		"under nextIdle the on-disk filter still carries sess-A's keys; the record lookup is what makes the answer correct")

	_, _, err := b.RebuildBloom(context.Background())
	require.NoError(t, err)
	got = mustQuery(t, b, target, approach, ScopeSession)
	require.Equal(t, AnswerAbsent, got.State)
	require.False(t, got.BloomOnly)
}

func TestQuery_ScopeProject_CrossSession(t *testing.T) {
	root, cfg, target, approach := scopeCarryOver(t, ScopeProject, "nextIdle")

	b := openLedger(t, root, cfg, nil, testDeps("sess-B", newMetrics()))
	for _, scope := range []Scope{ScopeSession, ScopeProject} {
		got := mustQuery(t, b, target, approach, scope)
		require.Equal(t, AnswerActive, got.State, "scope %q", scope)
		require.NotNil(t, got.Record)
		require.Equal(t, ScopeProject, got.Record.Scope)
	}
}

func TestQuery_ScopeProject_ExcludesSessionScoped(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	r := newRecord("own-session", target, approach, "flaky today")
	r.Scope = ScopeSession
	mustRecord(t, l, r)

	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)

	got := mustQuery(t, l, target, approach, ScopeProject)
	require.Equal(t, AnswerAbsent, got.State, "a project-scope query never sees a session-scoped record")
	require.Nil(t, got.Record)
}

// ── Record ────────────────────────────────────────────────────────────────────────────────────

func TestRecord_RequireEvidence(t *testing.T) {
	root, cfg := newProject(t)
	require.True(t, cfg.Eliminations.RequireEvidence)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	before, err := os.ReadFile(logPath(root))
	require.NoError(t, err)

	r := newRecord("no-evidence", "src/a.ts", "widen pool timeout", "why")
	r.Evidence = core.Hash{}
	id, err := l.Record(context.Background(), r)
	require.ErrorIs(t, err, ErrNoEvidence)
	require.Equal(t, "", id)

	after, err := os.ReadFile(logPath(root))
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused elimination appends nothing")
	require.Equal(t, int64(1), counterValue(t, m, "negknow.records.rejected_no_evidence"))
	require.Equal(t, 0, l.Health().Records)
}

func TestRecord_EvidenceOptional(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RequireEvidence = false
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	r := newRecord("optional", "src/a.ts", "widen pool timeout", "why")
	r.Evidence = core.Hash{}
	id, err := l.Record(context.Background(), r)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, l.Health().Records)
}

func TestRecord_Idempotent(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	r := newRecord("idem", "src/a.ts", "widen pool timeout", "why")
	first, err := l.Record(context.Background(), r)
	require.NoError(t, err)
	second, err := l.Record(context.Background(), r)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, logLineCount(t, root))
	require.Equal(t, 1, l.Health().Records)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.records.deduped"))
}

func TestRecord_IdempotentAcrossTimestamps(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	clk := newStepClock()
	deps := testDeps("sess", m)
	deps.Clock = clk
	l := openLedger(t, root, cfg, nil, deps)

	r := newRecord("retry", "src/a.ts", "widen pool timeout", "why")
	first, err := l.Record(context.Background(), r)
	require.NoError(t, err)

	// The MCP-retry shape: the same elimination re-proposed a clock-tick later. recordID mixes in
	// TS, so an id-based dedup would miss this entirely and grow a duplicate line.
	clk.advance(30 * time.Second)
	second, err := l.Record(context.Background(), r)
	require.NoError(t, err)

	require.Equal(t, first, second, "the dedup is identity-based (byKey), not id-based")
	require.Equal(t, 1, logLineCount(t, root))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.records.deduped"))
}

func TestRecord_ReRecordAfterStaleIsNotDeduped(t *testing.T) {
	root, cfg := newProject(t)
	clk := newStepClock()
	deps := testDeps("sess", newMetrics())
	deps.Clock = clk
	l := openLedger(t, root, cfg, nil, deps)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
		reason   = "pgbouncer ignores it"
	)
	first := mustRecord(t, l, newRecord("reverify", target, approach, reason))
	require.NoError(t, l.MarkStale(context.Background(), []string{first}, []string{"compose changed"}))

	clk.advance(time.Minute)
	second := mustRecord(t, l, newRecord("reverify", target, approach, reason))

	require.NotEqual(t, first, second, "re-verification after a staleness flip must mint a new record")
	h := l.Health()
	require.Equal(t, 2, h.Records)
	require.Equal(t, 1, h.Active)
	require.Equal(t, 1, h.Stale)
	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)

	// And again with the clock NOT advanced. recordID mixes in TS, so this re-record derives the
	// id the record it is replacing already holds; the ledger has to advance past the collision
	// rather than short-circuit on it, or the re-verification would be swallowed by byID and then
	// dropped outright at the next Open, where replayLog keeps the FIRST line for a repeated id.
	require.NoError(t, l.MarkStale(context.Background(), []string{second}, []string{"compose changed again"}))
	third := mustRecord(t, l, newRecord("reverify", target, approach, reason))

	require.NotEqual(t, second, third, "a same-millisecond re-record still mints a distinct id")
	h = l.Health()
	require.Equal(t, 3, h.Records)
	require.Equal(t, 1, h.Active)
	require.Equal(t, 2, h.Stale)
	require.Equal(t, 3, logLineCount(t, root)-2, "three record lines, plus the two stale controls")
	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)
}

// secretToken is a credential-shaped string, split across a + so that the literal shape never
// appears whole in a source file (the convention internal/redact's own fixtures follow).
const secretToken = "sk-" + "live-4eC39HqLyjWDarjtT1zdp7dc"

// TestRecord_RedactsBeforeCanonicalizing is R19: redaction has to run before the descriptor is
// derived, not after.
//
// A Descriptor is not an opaque digest. NormalizedPath and Symbol are lifted verbatim out of the
// target and land as plaintext on the record line, and ReasonHash is a digest anyone holding a
// candidate secret can confirm by recomputing it — so canonicalizing first would write a secret
// into an append-only file (§7.4) that nothing in the system ever rewrites.
func TestRecord_RedactsBeforeCanonicalizing(t *testing.T) {
	root, cfg := newProject(t)
	deps := testDeps("sess", newMetrics())
	// The shape a composition root supplies: a fixed placeholder that is not itself a match, so
	// re-running the redactor over its own output changes nothing. Deps.Redact documents that
	// requirement, and Record leans on it — it redacts at entry AND inside normalizeRecord.
	deps.Redact = func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(secretToken), []byte("[REDACTED]"))
	}
	l := openLedger(t, root, cfg, nil, deps)

	var (
		target   = "src/" + secretToken + "/auth.ts:refreshToken"
		approach = "widen the " + secretToken + " pool timeout"
		reason   = "the " + secretToken + " credential is rejected in transaction mode"
	)
	id := mustRecord(t, l, newRecord("redact", target, approach, reason))

	// 1. Nothing unredacted reached the log at all — descriptor fields, reason and target alike.
	raw, err := os.ReadFile(logPath(root))
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.NotContains(t, string(raw), secretToken,
		"no unredacted byte may reach records/eliminations.jsonl")
	require.Contains(t, string(raw), "[REDACTED]", "the placeholder is what was written instead")

	got, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.NotContains(t, got.Desc.NormalizedPath, secretToken)
	require.NotContains(t, got.Desc.Symbol, secretToken)
	require.NotContains(t, got.Desc.ApproachClass, secretToken)
	require.NotContains(t, got.Target, secretToken)
	require.NotContains(t, got.Reason, secretToken)

	// 2. The reason hash is the digest of the REDACTED reason, which is what "the hash carries no
	// secret" means for a digest: it is not confirmable from a candidate plaintext.
	redacted := Canonicalize(
		string(deps.Redact([]byte(target))),
		string(deps.Redact([]byte(approach))),
		string(deps.Redact([]byte(reason))),
	)
	require.Equal(t, redacted.ReasonHash, got.Desc.ReasonHash)
	require.NotEqual(t, Canonicalize(target, approach, reason).ReasonHash, got.Desc.ReasonHash,
		"a hash over the raw reason would be confirmable by anyone holding the secret")

	// 3. And the two sides still meet: Query redacts before it canonicalizes too, so a caller
	// asking with the text it actually has still finds the record.
	a := mustQuery(t, l, target, approach, ScopeSession)
	require.Equal(t, AnswerActive, a.State, "Record and Query must key on the same redacted text")
	require.Equal(t, id, a.Record.ID)

	// Asking with the already-redacted spelling finds it too, which is what the idempotence
	// requirement on Deps.Redact buys.
	same := mustQuery(t, l, string(deps.Redact([]byte(target))), string(deps.Redact([]byte(approach))), ScopeSession)
	require.Equal(t, AnswerActive, same.State)
	require.Equal(t, id, same.Record.ID)
}

func TestRecord_RejectsPresetStale(t *testing.T) {
	root, cfg := newProject(t)
	log := newCapturingLogger()
	deps := testDeps("sess", newMetrics())
	deps.Log = log
	l := openLedger(t, root, cfg, nil, deps)

	r := newRecord("preset", "src/a.ts", "widen pool timeout", "why")
	r.Status = StatusStale
	r.StaleSince = core.UnixMilli(1)
	r.StaleBecause = []string{"invented"}
	id := mustRecord(t, l, r)

	got, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, StatusActive, got.Status, "MarkStale is the only path to stale")
	require.Equal(t, core.UnixMilli(0), got.StaleSince)
	require.Nil(t, got.StaleBecause)
	require.Contains(t, log.lines(), "warn negknow: a record may not be created stale; storing it active",
		"the normalization is reported, never silent")
}

func TestRecord_DescriptorRecomputed(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
		reason   = "pgbouncer ignores it"
	)
	r := newRecord("bogus-desc", target, approach, reason)
	r.Desc = Descriptor{NormalizedPath: "not/the/path", Symbol: "nope", ApproachClass: "invented"}
	id := mustRecord(t, l, r)

	got, err := l.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, Canonicalize(target, approach, reason), got.Desc)
}

// ── Get / Active / All / TopActive ────────────────────────────────────────────────────────────

// seedFive records five eliminations under sess: three own-session active, two of which are then
// flipped stale, plus one active record belonging to a foreign session.
func seedFive(t *testing.T, l *ledger, clk *stepClock) (ids []string) {
	t.Helper()
	for i := range 4 {
		clk.advance(time.Second)
		r := newRecord(fmt.Sprintf("seed-%d", i), fmt.Sprintf("src/f%d.ts", i), "widen pool timeout", "why")
		ids = append(ids, mustRecord(t, l, r))
	}
	clk.advance(time.Second)
	foreign := newRecord("seed-foreign", "src/foreign.ts", "widen pool timeout", "why")
	foreign.Session = "other-session"
	foreign.Scope = ScopeSession
	ids = append(ids, mustRecord(t, l, foreign))

	require.NoError(t, l.MarkStale(context.Background(), ids[:2], []string{"dep changed"}))
	return ids
}

func TestGetActiveAll(t *testing.T) {
	root, cfg := newProject(t)
	clk := newStepClock()
	deps := testDeps("sess", newMetrics())
	deps.Clock = clk
	l := openLedger(t, root, cfg, nil, deps)

	ids := seedFive(t, l, clk)
	ctx := context.Background()

	got, err := l.Get(ctx, ids[2])
	require.NoError(t, err)
	require.Equal(t, ids[2], got.ID)
	got.Reason = "mutated by the caller"
	again, err := l.Get(ctx, ids[2])
	require.NoError(t, err)
	require.Equal(t, "why", again.Reason, "Get returns a copy, never a window into ledger state")

	_, err = l.Get(ctx, "elim_nope")
	require.ErrorIs(t, err, core.ErrNotFound)

	active, err := l.Active(ctx, ScopeSession)
	require.NoError(t, err)
	require.Len(t, active, 2, "two of five are stale and one belongs to another session")
	require.Equal(t, []string{ids[2], ids[3]}, []string{active[0].ID, active[1].ID})
	require.Less(t, int64(active[0].TS), int64(active[1].TS), "Active is TS-ascending")

	all, err := l.All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 5, "All ignores status, scope and session")
}

func TestTopActive(t *testing.T) {
	root, cfg := newProject(t)
	clk := newStepClock()
	deps := testDeps("sess", newMetrics())
	deps.Clock = clk
	l := openLedger(t, root, cfg, nil, deps)

	var ids []string
	for i := range 5 {
		clk.advance(time.Second)
		ids = append(ids, mustRecord(t, l,
			newRecord(fmt.Sprintf("top-%d", i), fmt.Sprintf("src/t%d.ts", i), "widen pool timeout", "why")))
	}
	score := map[string]float64{ids[2]: 0.9, ids[0]: 0.4}

	top, remaining, err := l.TopActive(context.Background(), ScopeSession, 2, score)
	require.NoError(t, err)
	require.Len(t, top, 2)
	require.Equal(t, []string{ids[2], ids[0]}, []string{top[0].ID, top[1].ID})
	require.Equal(t, 3, remaining, "the tail SP-11 renders as \"and N more\"")

	all, remaining, err := l.TopActive(context.Background(), ScopeSession, 10, score)
	require.NoError(t, err)
	require.Len(t, all, 5)
	require.Equal(t, 0, remaining)
	// Unscored records tie at 0 and fall back to TS-descending.
	require.Equal(t, []string{ids[2], ids[0], ids[4], ids[3], ids[1]},
		[]string{all[0].ID, all[1].ID, all[2].ID, all[3].ID, all[4].ID})
}

// ── Answer.MCPResult ──────────────────────────────────────────────────────────────────────────

func TestAnswerMCPResult(t *testing.T) {
	ev := testEvidence("mcp-result")
	rec := &Record{Reason: "pgbouncer ignores it", Evidence: ev}

	for _, tc := range []struct {
		name                            string
		in                              Answer
		state, reason, note, evidenceOK string
	}{
		{
			name:  "active",
			in:    Answer{State: AnswerActive, Record: rec},
			state: "active", reason: "pgbouncer ignores it", note: "", evidenceOK: ev.String(),
		},
		{
			name:  "stale",
			in:    Answer{State: AnswerStale, Record: rec, Note: StaleNote},
			state: "stale", reason: "pgbouncer ignores it", note: StaleNote, evidenceOK: ev.String(),
		},
		{
			name:  "absent",
			in:    Answer{State: AnswerAbsent},
			state: "absent", reason: "", note: "", evidenceOK: "",
		},
		{
			name:  "active_with_nil_record",
			in:    Answer{State: AnswerActive},
			state: "absent", reason: "", note: "", evidenceOK: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, reason, note, evidence := tc.in.MCPResult()
			require.Equal(t, tc.state, state)
			require.Equal(t, tc.reason, reason)
			require.Equal(t, tc.note, note)
			require.Equal(t, tc.evidenceOK, evidence)
		})
	}

	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, ev.String())
}

// ── degraded and closed states ────────────────────────────────────────────────────────────────

// blockLogWithDirectory makes records/eliminations.jsonl unreadable AND unappendable by creating a
// DIRECTORY where the file belongs. chmod is not portable to Windows, which is this repo's primary
// dev platform; a directory is refused by every opener on both.
func blockLogWithDirectory(t *testing.T, root string) {
	t.Helper()
	p := logPath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.Mkdir(p, 0o755))
}

func TestBlindMode(t *testing.T) {
	root, cfg := newProject(t)
	blockLogWithDirectory(t, root)
	m := newMetrics()

	// A key that IS in the filter Open is handed: blind mode must still answer absent, because a
	// membership answer with no record behind it is the one thing §12.3 forbids here.
	seeded := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	d := Canonicalize("src/a.ts", "widen pool timeout", "")
	seeded.Add(d.MatchKey())

	loud := captureLoud(t)
	l := openLedger(t, root, cfg, seeded, testDeps("sess", m))

	require.True(t, l.blind, "an unreadable record log is blind mode")
	require.Len(t, loud(), 1, "degradation is loud exactly once: %v", loud())
	require.Contains(t, loud()[0], "already_tried will answer absent for everything")
	require.Contains(t, strings.Join(logging.LastLoud(), "\n"), "already_tried will answer absent for everything",
		"the line also reaches the process-wide ring /qompack:status reads")
	require.Equal(t, int64(1), counterValue(t, m, "negknow.bloom.blind_mode"))

	a := mustQuery(t, l, "src/a.ts", "widen pool timeout", ScopeSession)
	require.Equal(t, AnswerAbsent, a.State)
	require.False(t, a.BloomOnly, "blind mode never reports a bloom hit it cannot back")
	require.Nil(t, a.Record)
}

func TestClose_Idempotent(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))

	require.NoError(t, l.Close())
	require.NoError(t, l.Close(), "Close is idempotent")
}

func TestClosedLedgerRejectsWrites(t *testing.T) {
	root, cfg := newProject(t)
	l := openLedger(t, root, cfg, nil, testDeps("sess", newMetrics()))
	ctx := context.Background()

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("closed", target, approach, "pgbouncer ignores it"))
	lines := logLineCount(t, root)
	require.NoError(t, l.Close())

	_, err := l.Record(ctx, newRecord("after-close", "src/b.ts", "widen pool timeout", "why"))
	require.ErrorIs(t, err, os.ErrClosed)
	require.ErrorIs(t, l.MarkStale(ctx, []string{id}, []string{"x"}), os.ErrClosed)
	_, _, err = l.RebuildBloom(ctx)
	require.ErrorIs(t, err, os.ErrClosed)
	require.Equal(t, lines, logLineCount(t, root), "a closed ledger appends nothing")

	// Reads keep answering from the in-memory index.
	require.Equal(t, AnswerActive, mustQuery(t, l, target, approach, ScopeSession).State)
	got, err := l.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	active, err := l.Active(ctx, ScopeSession)
	require.NoError(t, err)
	require.Len(t, active, 1)
	all, err := l.All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, 1, l.Health().Records)
}

// ── concurrency ───────────────────────────────────────────────────────────────────────────────

func TestConcurrentRecordQuery(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("sess", m))

	const (
		writers        = 8
		readers        = 8
		recordsPerGoro = 200
		queriesPerGoro = 500
	)

	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := range recordsPerGoro {
				// Every descriptor is distinct, so nothing is deduped and the final count is exact.
				target := fmt.Sprintf("src/g%d/f%d.ts", g, i)
				r := newRecord(target, target, "widen pool timeout", "why")
				if _, err := l.Record(ctx, r); err != nil {
					t.Errorf("Record(%s): %v", target, err)
					return
				}
			}
		}()
	}
	for g := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := range queriesPerGoro {
				if _, err := l.Query(ctx, fmt.Sprintf("src/g%d/f%d.ts", g, i%recordsPerGoro),
					"widen pool timeout", ScopeSession); err != nil {
					t.Errorf("Query: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	require.Equal(t, writers*recordsPerGoro, l.Health().Records)
	require.Equal(t, int64(0), counterValue(t, m, "negknow.records.deduped"))
	require.Equal(t, int64(writers*recordsPerGoro), counterValue(t, m, "negknow.records.appended"))
}

// ── internals the tables above depend on ──────────────────────────────────────────────────────

func TestPick_PrefersLatestThenGreatestID(t *testing.T) {
	cands := []Record{
		{ID: "elim_a", TS: 10, Status: StatusActive},
		{ID: "elim_c", TS: 20, Status: StatusActive},
		{ID: "elim_b", TS: 20, Status: StatusActive},
		{ID: "elim_z", TS: 99, Status: StatusStale},
	}
	got := pick(cands, StatusActive)
	require.NotNil(t, got)
	require.Equal(t, "elim_c", got.ID, "greatest TS, ties broken by the greatest ID")

	got.ID = "mutated"
	require.Equal(t, "elim_c", cands[1].ID, "pick returns a copy")

	require.Nil(t, pick(cands[:1], StatusStale))
}

func TestDedupHex_SeparatesFields(t *testing.T) {
	d := Canonicalize("src/a.ts", "widen pool timeout", "why")
	a := Record{Session: "a", Scope: ScopeSession, Desc: d}
	b := Record{Session: "a", Scope: ScopeProject, Desc: d}
	c := Record{Session: "b", Scope: ScopeSession, Desc: d}

	require.Equal(t, dedupHex(a), dedupHex(a))
	require.NotEqual(t, dedupHex(a), dedupHex(b), "scope is part of the identity")
	require.NotEqual(t, dedupHex(a), dedupHex(c), "session is part of the identity")
	_, err := hex.DecodeString(dedupHex(a))
	require.NoError(t, err)
}
