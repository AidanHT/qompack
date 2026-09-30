// SP-11's daemon-side unit coverage for the L5 rehydrator service: the seam that turns a
// SessionStart(source=compact|clear) into an additionalContext payload, records what it emitted,
// and refuses to act at all when §12.1 has degraded the session.
//
// PACKAGE CHOICE. This file is `package daemon_test`, not `package daemon`, and that is forced
// rather than stylistic: the table for these cases asks for testutil.FakeClock, and
// internal/testutil imports internal/cli (project.go's in-process RunHook mode) while internal/cli
// imports internal/daemon (hookclient.go, cli/daemon.go). An in-package daemon test importing
// testutil would therefore close a daemon -> testutil -> cli -> daemon cycle in the test build
// graph — the exact cycle internal/daemon/fakeclock_test.go's own doc comment records. An EXTERNAL
// test package has no such edge, and every symbol these cases need (NewRehydrateService,
// RehydrateOptions, BindRehydrate, Options.Bind, New, Services) is exported.
//
// EVERY TEST IN THIS FILE IS EXPECTED TO FAIL TO COMPILE until the main session lands
// internal/daemon/rehydrate_service.go and internal/rehydrate/drops.go. That is the point: the
// assertions below are the specification those files are being written against, and nothing here
// stubs them out to make the file build.
package daemon_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// rsSession is the one session id every case in this file drives the service under.
const rsSession = core.SessionID("sess-sp11-rehydrate")

// rsStateFile is the state artifact the Reporter writes, relative to .qompack/state/. The name is
// spelled here once so a rename shows up as one failing constant rather than nine failing
// assertions.
const rsStateFile = "rehydrate-" + string(rsSession) + ".json"

// ── fakes and spies ─────────────────────────────────────────────────────────────────────────

// rsFakeReader is the checkpoint.Reader every case drives the service through. Rule W-2 forbids
// this file from calling checkpoint.Writer/OpenWriter/Begin/Advance/Finalize, so the Checkpoint it
// hands back is decoded from the frozen fixture testdata/golden/contracts/checkpoint/want/0001.json
// and nothing on the writer side is ever touched.
type rsFakeReader struct {
	cp      checkpoint.Checkpoint
	ref     checkpoint.Ref
	err     error
	panics  bool
	latests int
}

func (r *rsFakeReader) Latest(context.Context, core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	r.latests++
	if r.panics {
		panic("rsFakeReader: deliberate panic from Latest")
	}
	if r.err != nil {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, r.err
	}
	return r.cp, r.ref, nil
}

func (r *rsFakeReader) Get(context.Context, core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	if r.err != nil {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, r.err
	}
	return r.cp, r.ref, nil
}

func (r *rsFakeReader) List(context.Context) ([]checkpoint.Ref, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []checkpoint.Ref{r.ref}, nil
}

func (r *rsFakeReader) Chain(context.Context, core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []checkpoint.Checkpoint{r.cp}, nil
}

func (r *rsFakeReader) Verify(context.Context) ([]core.CheckpointSeq, error) { return nil, r.err }

// rsFakeReader must satisfy the seam the service takes.
var _ checkpoint.Reader = (*rsFakeReader)(nil)

// rsLogger records what the service said. It is a local recording logger rather than
// logging.LastLoud because that ring is process-wide and shared with every other test in the
// binary: "one Loud" has to mean "one Loud from THIS service call", not "at least one Loud
// somewhere in this process".
type rsLogger struct {
	mu    sync.Mutex
	louds []string
	warns []string
	errs  []string
}

func (l *rsLogger) With(...any) logging.Logger { return l }
func (l *rsLogger) Debug(string, ...any)       {}
func (l *rsLogger) Info(string, ...any)        {}

func (l *rsLogger) Warn(msg string, _ ...any) {
	l.mu.Lock()
	l.warns = append(l.warns, msg)
	l.mu.Unlock()
}

func (l *rsLogger) Error(msg string, _ ...any) {
	l.mu.Lock()
	l.errs = append(l.errs, msg)
	l.mu.Unlock()
}

func (l *rsLogger) Loud(msg string, _ ...any) {
	l.mu.Lock()
	l.louds = append(l.louds, msg)
	l.mu.Unlock()
}

func (l *rsLogger) loudCount() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.louds) }

func (l *rsLogger) loudMsgs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.louds...)
}

func (l *rsLogger) warnCount() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.warns) }

var _ logging.Logger = (*rsLogger)(nil)

// rsSpyTokens is the "Build was never called" probe.
//
// rehydrate.Build is a package FUNCTION, not a seam on RehydrateOptions, so it cannot be replaced
// with a recorder. What it can be observed through is its Deps: Build must price every item it
// emits against Request.Budget (§8.6), so it cannot produce a payload without asking the
// Estimator. A zero call count is therefore the strongest available statement that no build
// happened, and it is exact in the direction that matters — a build that emitted nothing is
// indistinguishable from no build at all, and both satisfy "emits nothing".
type rsSpyTokens struct {
	mu    sync.Mutex
	calls int
}

func (s *rsSpyTokens) bump() { s.mu.Lock(); s.calls++; s.mu.Unlock() }

func (s *rsSpyTokens) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

func (s *rsSpyTokens) Estimate(b []byte, _ tokens.Class) core.Tokens {
	s.bump()
	return core.Tokens(len(b) / 4)
}

func (s *rsSpyTokens) EstimateString(v string, _ tokens.Class) core.Tokens {
	s.bump()
	return core.Tokens(len(v) / 4)
}

func (s *rsSpyTokens) EstimateRoot(context.Context, []core.ChunkRef, tokens.Class) core.Tokens {
	s.bump()
	return 0
}

// Calibrate and Factor complete the seam. Calibrate counts too: it is a mutation, and a
// rehydration that calibrated the estimator would have done work in a mode that forbids it.
func (s *rsSpyTokens) Calibrate(core.Tokens, core.Tokens) { s.bump() }

func (s *rsSpyTokens) Factor() float64 { return 1 }

var _ tokens.Estimator = (*rsSpyTokens)(nil)

// rsSpyStore counts every call made through it, so "the store was untouched" is an assertion with
// a number behind it rather than a nil that proves nothing. Every method returns a zero value; the
// clear branch must never reach any of them.
type rsSpyStore struct {
	mu    sync.Mutex
	calls int
}

func (s *rsSpyStore) bump() { s.mu.Lock(); s.calls++; s.mu.Unlock() }

func (s *rsSpyStore) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

func (s *rsSpyStore) Put(context.Context, io.Reader, store.PutOptions) (store.PutResult, error) {
	s.bump()
	return store.PutResult{}, nil
}

func (s *rsSpyStore) PutBytes(context.Context, []byte, store.PutOptions) (store.PutResult, error) {
	s.bump()
	return store.PutResult{}, nil
}

func (s *rsSpyStore) GetChunk(context.Context, core.Hash) ([]byte, error) { s.bump(); return nil, nil }

func (s *rsSpyStore) GetRoot(context.Context, core.Hash) (store.Root, error) {
	s.bump()
	return store.Root{}, nil
}

func (s *rsSpyStore) Open(context.Context, core.Hash) (io.ReadCloser, error) {
	s.bump()
	return nil, core.ErrNotFound
}

func (s *rsSpyStore) OpenSpan(context.Context, core.Hash, int64, int64) (io.ReadCloser, error) {
	s.bump()
	return nil, core.ErrNotFound
}

func (s *rsSpyStore) Has(core.Hash) bool { s.bump(); return false }

func (s *rsSpyStore) RecordToolUse(context.Context, store.ToolUseRecord) error { s.bump(); return nil }

func (s *rsSpyStore) ToolUse(context.Context, core.ToolUseID) (store.ToolUseRecord, error) {
	s.bump()
	return store.ToolUseRecord{}, core.ErrNotFound
}

func (s *rsSpyStore) ToolUsesByPath(context.Context, string, int) ([]store.ToolUseRecord, error) {
	s.bump()
	return nil, nil
}

func (s *rsSpyStore) MarkSuperseded(context.Context, core.ToolUseID, core.ToolUseID) error {
	s.bump()
	return nil
}

func (s *rsSpyStore) AppendFileVersion(context.Context, string, store.FileVersion) error {
	s.bump()
	return nil
}

func (s *rsSpyStore) FileHistory(context.Context, string) ([]store.FileVersion, error) {
	s.bump()
	return nil, nil
}

func (s *rsSpyStore) FileAt(context.Context, string, time.Time) (store.FileVersion, error) {
	s.bump()
	return store.FileVersion{}, core.ErrNotFound
}

func (s *rsSpyStore) ChangedSince(context.Context, []core.Dep) ([]core.Dep, error) {
	s.bump()
	return nil, nil
}

func (s *rsSpyStore) Search(context.Context, store.Query) ([]store.Hit, error) {
	s.bump()
	return nil, nil
}

func (s *rsSpyStore) Segments() store.SegmentLog { s.bump(); return nil }

func (s *rsSpyStore) Stats(context.Context) (store.Stats, error) { s.bump(); return store.Stats{}, nil }

func (s *rsSpyStore) GC(context.Context, store.GCPolicy) (store.GCReport, error) {
	s.bump()
	return store.GCReport{}, nil
}

func (s *rsSpyStore) Flush(context.Context) error { s.bump(); return nil }
func (s *rsSpyStore) Close() error                { s.bump(); return nil }

var _ store.Store = (*rsSpyStore)(nil)

// rsSpyLedger is rsSpyStore's negative-knowledge twin: the clear branch must not read the
// eliminations either.
type rsSpyLedger struct {
	mu    sync.Mutex
	calls int
}

func (l *rsSpyLedger) bump() { l.mu.Lock(); l.calls++; l.mu.Unlock() }

func (l *rsSpyLedger) count() int { l.mu.Lock(); defer l.mu.Unlock(); return l.calls }

func (l *rsSpyLedger) Record(context.Context, negknow.Record) (string, error) {
	l.bump()
	return "", nil
}

func (l *rsSpyLedger) Query(context.Context, string, string, negknow.Scope) (negknow.Answer, error) {
	l.bump()
	return negknow.Answer{}, nil
}

func (l *rsSpyLedger) Get(context.Context, string) (negknow.Record, error) {
	l.bump()
	return negknow.Record{}, core.ErrNotFound
}

func (l *rsSpyLedger) Active(context.Context, negknow.Scope) ([]negknow.Record, error) {
	l.bump()
	return nil, nil
}

func (l *rsSpyLedger) All(context.Context) ([]negknow.Record, error) { l.bump(); return nil, nil }

func (l *rsSpyLedger) MarkStale(context.Context, []string, []string) error { l.bump(); return nil }

func (l *rsSpyLedger) RefreshStaleness(context.Context, store.Store) ([]string, error) {
	l.bump()
	return nil, nil
}

func (l *rsSpyLedger) RebuildBloom(context.Context) (*sketch.Bloom, negknow.Health, error) {
	l.bump()
	return nil, negknow.Health{}, nil
}

func (l *rsSpyLedger) Health() negknow.Health { l.bump(); return negknow.Health{} }
func (l *rsSpyLedger) Close() error           { l.bump(); return nil }

var _ negknow.Ledger = (*rsSpyLedger)(nil)

// ── fixtures ────────────────────────────────────────────────────────────────────────────────

// rsGoldenCheckpoint decodes the frozen §8.5 artifact (Rule W-2). Every case that needs a
// Checkpoint VALUE gets it from here; nothing in this file writes one.
func rsGoldenCheckpoint(t *testing.T) checkpoint.Checkpoint {
	t.Helper()
	_, want, frozen := testutil.ContractFixture(t, "checkpoint", "checkpoint_v1")
	require.True(t, frozen, "the §8.5 checkpoint fixture is frozen and must stay so")

	var cp checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(want, &cp))
	// The fixture carries its own session id; the service is driven under this file's session, and
	// a checkpoint whose session disagrees with the event's would be a different test.
	cp.Session = rsSession
	return cp
}

// rsRef is the Ref the fake reader returns beside the golden checkpoint.
func rsRef(root string) checkpoint.Ref {
	return checkpoint.Ref{
		Seq:      core.CheckpointSeq(1),
		Path:     paths.CheckpointPath(paths.Of(root), core.CheckpointSeq(1)),
		Frontier: core.TurnIndex(61),
		Created:  core.UnixMilli(testutil.Epoch.UnixMilli()),
	}
}

// rsFixture is one fully wired service plus everything a case needs to interrogate it.
type rsFixture struct {
	proj   *testutil.Project
	reader *rsFakeReader
	log    *rsLogger
	tok    *rsSpyTokens
	st     *rsSpyStore
	ledger *rsSpyLedger
	mon    contract.Monitor
	clock  *testutil.FakeClock
	svc    observer.Rehydrator
}

// rsNewFixture builds the service over a real disposable project, a real Reporter (so every
// state-file assertion is about the shipped writer, not a stand-in) and the real §12.1 monitor
// with persistence disabled.
func rsNewFixture(t *testing.T) *rsFixture {
	t.Helper()
	return rsNewFixtureWith(t, nil)
}

// rsNewFixtureWith is rsNewFixture with the project's resolved config edited before the service
// reads it; nil leaves it alone.
func rsNewFixtureWith(t *testing.T, mutate func(*config.Config)) *rsFixture {
	t.Helper()

	p := testutil.NewProject(t)
	cfg := p.Cfg
	if mutate != nil {
		mutate(&cfg)
	}
	log := &rsLogger{}
	clk := testutil.NewFakeClock(testutil.Epoch)
	reader := &rsFakeReader{cp: rsGoldenCheckpoint(t), ref: rsRef(p.Root)}
	tok := &rsSpyTokens{}
	sp := &rsSpyStore{}
	led := &rsSpyLedger{}

	// An empty statePath disables persistence entirely (contract.NewMonitor's own documented
	// behaviour) — the mode still changes, which is all these cases read.
	mon := contract.NewMonitor(log, obs.New(clk), "")

	f := &rsFixture{proj: p, reader: reader, log: log, tok: tok, st: sp, ledger: led, mon: mon, clock: clk}
	f.svc = daemon.NewRehydrateService(daemon.RehydrateOptions{
		ProjectRoot: p.Root,
		Cfg:         cfg,
		Checkpoints: reader,
		Deps: rehydrate.Deps{
			Store:  sp,
			Ledger: led,
			Tokens: tok,
			Log:    log,
			// The REAL scanner and indexer over an empty project, which is what the daemon wires.
			// They find nothing here and report no absence, so the payload is not degraded — where
			// leaving them nil would mark every build in this file degraded, because nothing else
			// supplies items 6a and 6b and Build says so. Graph stays nil: a missing slice costs
			// ranking, not content, and this file's subject is the SERVICE around Build.
			Rules:  rules.New(rules.WithLogger(log)),
			Skills: skills.New(skills.WithLogger(log)),
		},
		Reporter: rehydrate.NewReporter(p.Root, log),
		Contract: mon,
		Log:      log,
		Metrics:  obs.New(clk),
		Clock:    clk,
	})
	return f
}

// compactEvent and clearEvent are the two SessionStart payloads the seam branches on.
func rsCompactEvent(root string) hookio.Event {
	return hookio.Event{HookEventName: "SessionStart", SessionID: rsSession, CWD: root, Source: "compact"}
}

func rsClearEvent(root string) hookio.Event {
	return hookio.Event{HookEventName: "SessionStart", SessionID: rsSession, CWD: root, Source: "clear"}
}

// rsStatePath is <root>/.qompack/state/rehydrate-<sess>.json.
func rsStatePath(root string) string {
	return filepath.Join(paths.Of(root).State, rsStateFile)
}

// rsReadState decodes the state artifact, failing the test if it is absent.
func rsReadState(t *testing.T, root string) rehydrate.State {
	t.Helper()
	b, err := os.ReadFile(paths.Long(rsStatePath(root)))
	require.NoError(t, err, "the rehydrator must record what it emitted at %s", rsStatePath(root))

	var st rehydrate.State
	require.NoError(t, json.Unmarshal(b, &st))
	return st
}

// rsIsEmptyOutput reports whether out carries nothing the host would act on: §12.1's
// "everything that ACTS is off" is observable as an Output with no additionalContext at all.
func rsIsEmptyOutput(out hookio.Output) bool {
	return out.HookSpecificOutput == nil || out.HookSpecificOutput.AdditionalContext == ""
}

// ── the cases ───────────────────────────────────────────────────────────────────────────────

// TestService_CompactEmitsAdditionalContext is the happy path: a compact SessionStart produces a
// SessionStart-stamped Output whose additionalContext is a real, injection-tagged payload.
//
// The injection tags are not decoration. §4.6 forbids re-encoding Qompack's own injected text into
// the next checkpoint, and checkpoint.StripInjections is what removes it — a payload that is not
// tagged is a payload the next compaction will compress a second time.
func TestService_CompactEmitsAdditionalContext(t *testing.T) {
	f := rsNewFixture(t)

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput, "a compact rehydration must emit hookSpecificOutput")
	require.Equal(t, "SessionStart", out.HookSpecificOutput.HookEventName)

	ac := out.HookSpecificOutput.AdditionalContext
	require.NotEmpty(t, ac, "a compact rehydration with a readable checkpoint must emit a payload")

	openTag := fmt.Sprintf(checkpoint.InjectionOpenTag, int(f.reader.ref.Seq), checkpoint.SchemaVersion)
	require.Contains(t, ac, openTag, "the payload must open with the §8.5 injection tag")
	require.Contains(t, ac, checkpoint.InjectionCloseTag, "the payload must close with the §8.5 injection tag")
	require.Empty(t, checkpoint.StripInjections(ac),
		"everything the rehydrator emits must sit INSIDE the injection span, or the next "+
			"checkpoint re-encodes it (§4.6)")

	require.Equal(t, 1, f.reader.latests, "exactly one checkpoint read per compact")
}

// TestService_ReinjectionKillSwitchEmitsNothing pins Qompack.md v1.5 Appendix C's independent
// injection switch (SP-19): with runtime.migration.reinjection.sessionStartCompact false the
// service emits nothing and reads no checkpoint, while the mode stays ModeFull — recording is a
// different switch (runtime.mode) and is untouched.
func TestService_ReinjectionKillSwitchEmitsNothing(t *testing.T) {
	f := rsNewFixtureWith(t, func(c *config.Config) { c.Runtime.Migration.Reinjection.SessionStartCompact = false })

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out, "a disabled injection emits nothing")
	require.Zero(t, f.reader.latests, "a disabled injection must not even read the checkpoint")
	require.Equal(t, contract.ModeFull, f.mon.Mode(), "the kill switch is not a degradation")
}

// TestService_DegradedPassiveEmitsNothing is §12.1's contract-failure state made mechanical: L0
// keeps recording, and everything that ACTS — additionalContext included — is off.
//
// The mode check must come FIRST. A service that reads the checkpoint and then discovers it may
// not act has already paid for work §12.1 forbids, so the reader's call count is asserted at zero
// alongside the output.
func TestService_DegradedPassiveEmitsNothing(t *testing.T) {
	f := rsNewFixture(t)
	f.mon.Degrade("test: forced into passive recording", nil)
	require.Equal(t, contract.ModeDegradedPassive, f.mon.Mode())

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "a degraded session still starts; it just starts with nothing injected")
	require.True(t, rsIsEmptyOutput(out), "ModeDegradedPassive may not inject additionalContext")

	require.Zero(t, f.tok.count(), "Build must never run in ModeDegradedPassive")
	require.Zero(t, f.reader.latests, "the mode is checked before the checkpoint is read")

	require.NoFileExists(t, paths.Long(rsStatePath(f.proj.Root)),
		"a rehydration that never happened has no state to record")
}

// TestService_CheckpointNotFoundStillEmits is §12.3: a session that starts with less context is
// recoverable, and a session that fails to start is not. No checkpoint is the ordinary first
// compaction of a fresh project, so it degrades the payload rather than suppressing it.
func TestService_CheckpointNotFoundStillEmits(t *testing.T) {
	f := rsNewFixture(t)
	f.reader.err = core.ErrNotFound

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	require.NotEmpty(t, out.HookSpecificOutput.AdditionalContext,
		"a missing checkpoint degrades the payload; it does not suppress it (§12.3)")

	st := rsReadState(t, f.proj.Root)
	require.True(t, st.Degraded, "the state file must record that this rehydration was degraded")
}

// rsNoteCeiling is how long a deferred note may ever be: far under the host's per-field cap, so it
// always reaches the model whole and never competes with anything for it
// (TestCompactDeferredNote_FitsTheHostCap pins the same ceiling in package daemon).
const rsNoteCeiling = 1000

// rsRequireDeferredNote asserts out is exactly the explicit deferred note for reason: a SessionStart
// additionalContext that says the rehydration did not arrive, and why, and nothing else.
func rsRequireDeferredNote(t *testing.T, out hookio.Output, reason string) {
	t.Helper()
	require.NotNil(t, out.HookSpecificOutput, "a rehydration that could not be built is answered, never silently")
	require.Equal(t, "SessionStart", out.HookSpecificOutput.HookEventName)
	ac := out.HookSpecificOutput.AdditionalContext
	require.Equal(t, daemon.CompactDeferredNote(rsSession, reason), ac, "the answer is the deferred note naming why")
	require.LessOrEqual(t, hookio.HostChars(ac), rsNoteCeiling, "the note stays far under the host's cap")
	require.Empty(t, hookio.HostCapOverruns(hookio.ConformOutput(hookio.EventSessionStart, out)))
}

// rsRequireNotBuiltReport asserts the drop report `dropped()` reads now leads with the rehydration
// that was never built, and why — never a report describing a payload as delivered.
func rsRequireNotBuiltReport(t *testing.T, root, why string) {
	t.Helper()
	st := rsReadState(t, root)
	require.Equal(t, rsSession, st.Session)
	require.True(t, st.Degraded, "a rehydration that was never built is a degraded one")
	require.Empty(t, st.Items, "nothing was emitted, so no item is listed as emitted")
	require.Zero(t, st.Tokens)
	require.NotEmpty(t, st.Dropped)
	lead := st.Dropped[0]
	require.Equal(t, "rehydration", lead.Kind, "the report leads with the whole rehydration: %+v", st.Dropped)
	require.True(t, strings.HasPrefix(lead.Detail, "not delivered: "), "and says it never reached the model: %q", lead.Detail)
	require.Contains(t, lead.Detail, why)

	drops, err := rehydrate.NewReporter(root, logging.Nop()).CurrentDrops(context.Background(), rsSession)
	require.NoError(t, err)
	require.Equal(t, st.Dropped, drops, "dropped() reads exactly this report")
}

// TestService_CheckpointErrorAnswersWithTheDeferredNote separates "there is no checkpoint" from "the
// checkpoint store is broken". The first is normal and still builds a payload
// (TestService_CheckpointNotFoundStillEmits); the second is a fault, and injecting a half-known
// context on top of a broken store would be worse than injecting none, so nothing is built. It is
// answered with the explicit deferred note all the same (owner decision D11), never with silence,
// and its drop report says the rehydration was never built, so dropped() does not describe an
// earlier one as if it were this compaction's.
//
// Criterion change (D11, 2026-09-25): this row was TestService_CheckpointErrorEmitsNothing and
// required an EMPTY output. D11 rules that an unreadable checkpoint store also answers with the
// deferred note C1.16 introduced for a late or failed rehydration: silence leaves the model with a
// compacted context and no word of what it lost. What the old row protected is kept — nothing is
// built on the broken store (zero estimator calls), and the failure is Loud exactly once.
func TestService_CheckpointErrorAnswersWithTheDeferredNote(t *testing.T) {
	f := rsNewFixture(t)
	f.reader.err = errors.New("io")

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "a hook may exit only 0 (§2.3); the failure is reported, never returned")
	rsRequireDeferredNote(t, out, daemon.DeferredCheckpointUnreadable)
	require.Zero(t, f.tok.count(), "nothing is built on top of a broken checkpoint store")
	require.Equal(t, 1, f.log.loudCount(), "a broken checkpoint store is never silent (§12)")
	rsRequireNotBuiltReport(t, f.proj.Root, "the checkpoint store could not be read")
}

// TestService_BuildFailureAnswersWithTheDeferredNote: a build that returns an error is answered
// with the deferred note (D11), Loud once, and recorded as never built. The only error
// rehydrate.BuildWithStats returns is its context's, and in the daemon that context ends only when
// Stop cancels the rehydration (startReplyWork), so the note and the report say the daemon was
// shutting down — the build did not fail on its merits, and nothing about it needs repairing.
//
// Criterion change (w3-startroute review, 2026-09-26): this row required DeferredFailed ("building
// it failed") and a report saying the build failed. That named the wrong cause for the one error a
// build can return: the rehydration was cut short by the daemon stopping. The row now requires the
// cause the rest of the route already uses for a stopping daemon (DeferredStopping), still Loud once
// and still recorded as never built. A failure on the build's merits is still DeferredFailed
// (TestService_PanicRecovered, and the route's
// TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote).
func TestService_BuildFailureAnswersWithTheDeferredNote(t *testing.T) {
	f := rsNewFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := f.svc.OnCompact(ctx, rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "a hook may exit only 0 (§2.3); the failure is reported, never returned")
	rsRequireDeferredNote(t, out, daemon.DeferredStopping)
	require.Equal(t, 1, f.log.loudCount(), "a rehydration lost to a stopping daemon is never silent (§12)")
	rsRequireNotBuiltReport(t, f.proj.Root, "the Qompack daemon was shutting down")
	require.NotContains(t, rsReadState(t, f.proj.Root).Dropped[0].Detail, "building the rehydration failed",
		"a build cut short by the daemon stopping did not fail on its merits")
}

// TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping: the shipped reader returns its
// context's error once that context has ended, before it reads a checkpoint (fileReader.load), and
// in the daemon the rehydration's context ends only when Stop cancels it (startReplyWork). That is
// the daemon stopping, not the store failing: the note and the drop report say so, and the store is
// not Loud'd as unreadable, so neither the transcript nor dropped() sends anyone to repair a store
// that is healthy. The same store read under a live context then rehydrates, which is what shows it
// was healthy.
func TestService_CancelledCheckpointReadAnswersThatTheDaemonWasStopping(t *testing.T) {
	f := rsNewFixture(t)
	body, err := checkpoint.Marshal(rsGoldenCheckpoint(t))
	require.NoError(t, err)
	rsWriteCheckpointArtifact(t, f.proj.Root, body)
	svc := rsRealReaderService(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := svc.OnCompact(ctx, rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "a hook may exit only 0 (§2.3); the failure is reported, never returned")
	rsRequireDeferredNote(t, out, daemon.DeferredStopping)
	require.Zero(t, f.tok.count(), "nothing is built once the daemon is stopping")
	require.Equal(t, 1, f.log.loudCount(), "a rehydration lost to a stopping daemon is never silent (§12)")
	require.NotContains(t, f.log.loudMsgs(), "rehydrate: checkpoint unreadable",
		"a healthy store is never reported unreadable")
	rsRequireNotBuiltReport(t, f.proj.Root, "the Qompack daemon was shutting down")
	require.NotContains(t, rsReadState(t, f.proj.Root).Dropped[0].Detail, "checkpoint store could not be read",
		"dropped() must not blame the store for the daemon stopping")

	out, err = svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	ac := out.HookSpecificOutput.AdditionalContext
	require.NotEmpty(t, ac, "the store the cancelled read did not blame is healthy: it rehydrates")
	require.NotContains(t, ac, daemon.DeferredNoteTag)
	require.Equal(t, 1, f.log.loudCount(), "and reading it is not a contract event")
}

// rsRealReaderService is rsNewFixture's service over the SHIPPED checkpoint reader instead of the
// fake, for the rows about what the real reader reports for a broken store. It writes nothing; each
// row lays out the store it needs by hand.
func rsRealReaderService(t *testing.T, f *rsFixture) observer.Rehydrator {
	t.Helper()
	reader, err := checkpoint.OpenReader(f.proj.Root, f.log, nil)
	require.NoError(t, err)
	return daemon.NewRehydrateService(daemon.RehydrateOptions{
		ProjectRoot: f.proj.Root,
		Cfg:         f.proj.Cfg,
		Checkpoints: reader,
		Deps: rehydrate.Deps{
			Store: f.st, Ledger: f.ledger, Tokens: f.tok, Log: f.log,
			Rules: rules.New(rules.WithLogger(f.log)), Skills: skills.New(skills.WithLogger(f.log)),
		},
		Reporter: rehydrate.NewReporter(f.proj.Root, f.log),
		Contract: f.mon,
		Log:      f.log,
		Metrics:  obs.New(f.clock),
		Clock:    f.clock,
	})
}

// rsWriteCheckpointArtifact lays out checkpoint 0001 with body and a manifest line whose digest
// matches it, exactly as the writer would for a body that decoded.
func rsWriteCheckpointArtifact(t *testing.T, root string, body []byte) {
	t.Helper()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Checkpoints), 0o700))
	p := paths.CheckpointPath(l, 1)
	require.NoError(t, os.WriteFile(paths.Long(p), body, 0o600))
	sum := sha256.Sum256(body)
	line, err := json.Marshal(paths.ManifestEntry{Seq: 1, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body))})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(paths.ManifestPath(l)), append(line, '\n'), 0o600))
}

// TestService_UnreadableCheckpointManifestAnswersWithTheDeferredNote: the shipped reader over a
// MANIFEST.jsonl it cannot parse — the whole store is unreadable, since the manifest is what every
// read verifies against — reports a fatal error, and the compaction is answered with the note (D11).
func TestService_UnreadableCheckpointManifestAnswersWithTheDeferredNote(t *testing.T) {
	f := rsNewFixture(t)
	l := paths.Of(f.proj.Root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Checkpoints), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(paths.ManifestPath(l)), []byte("{not a manifest line\n"), 0o600))

	out, err := rsRealReaderService(t, f).OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	rsRequireDeferredNote(t, out, daemon.DeferredCheckpointUnreadable)
	require.Zero(t, f.tok.count(), "nothing is built on top of an unreadable store")
	rsRequireNotBuiltReport(t, f.proj.Root, "the checkpoint store could not be read")
}

// TestService_UndecodableCheckpointAnswersWithTheDeferredNote: a checkpoint whose bytes match their
// manifest digest but whose fields do not decode is not one the reader can step over to its parent
// (it verified), so it is fatal, and the compaction is answered with the note (D11).
func TestService_UndecodableCheckpointAnswersWithTheDeferredNote(t *testing.T) {
	f := rsNewFixture(t)
	rsWriteCheckpointArtifact(t, f.proj.Root, []byte(`{"version":1,"session":["not","a","session"]}`))

	out, err := rsRealReaderService(t, f).OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	rsRequireDeferredNote(t, out, daemon.DeferredCheckpointUnreadable)
	require.Zero(t, f.tok.count(), "nothing is built on top of a checkpoint that cannot be read")
	rsRequireNotBuiltReport(t, f.proj.Root, "the checkpoint store could not be read")
}

// TestService_UnverifiableCheckpointStillRehydratesWithoutIt pins the boundary of D11: a checkpoint
// that fails its digest (or is not JSON, or names a newer schema) is one the reader steps over, and
// with no verifiable checkpoint left the compaction rehydrates from L0 and the ledger exactly as for
// a project with no checkpoint yet. That is a degraded payload, not a failure, so it is NOT the note.
func TestService_UnverifiableCheckpointStillRehydratesWithoutIt(t *testing.T) {
	f := rsNewFixture(t)
	rsWriteCheckpointArtifact(t, f.proj.Root, []byte(`{"version":1}`))
	require.NoError(t, os.WriteFile(paths.Long(paths.CheckpointPath(paths.Of(f.proj.Root), 1)), []byte(`{"version":1,"x":1}`), 0o600))

	out, err := rsRealReaderService(t, f).OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	ac := out.HookSpecificOutput.AdditionalContext
	require.NotEmpty(t, ac, "a checkpoint the reader steps over degrades the payload; it does not suppress it")
	require.NotContains(t, ac, daemon.DeferredNoteTag, "a degraded rehydration is delivered, not deferred")
	require.True(t, rsReadState(t, f.proj.Root).Degraded)
}

// ── work unit H: lifecycle coverage (fresh/resume/fork/restart, repeated compaction, duplicate
// and out-of-order delivery) ────────────────────────────────────────────────────────────────────

// TestService_ResumedOrForkedSessionInheritsProjectCheckpoint is item 1's resume/fork case: "a
// resume, a fork ... must rehydrate without a preceding PostCompact event". A resumed or forked
// session starts with no checkpoint of its own, and checkpoint.Reader.Latest documents that it
// then "inherits the project's newest verifying checkpoint from any session" rather than
// reporting ErrNotFound (internal/checkpoint/reader.go's fileReader.Latest). This asserts the
// SERVICE layer passes that inherited checkpoint straight through rather than rejecting it
// because its Session field disagrees with the event's — there is no such check anywhere in
// OnCompact, and this pins that it stays that way.
func TestService_ResumedOrForkedSessionInheritsProjectCheckpoint(t *testing.T) {
	f := rsNewFixture(t)
	inherited := rsGoldenCheckpoint(t)
	inherited.Session = core.SessionID("sess-a-prior-session-this-one-resumed-or-forked-from")
	f.reader.cp = inherited

	// The EVENT carries the new (resumed/forked) session id; the checkpoint the reader hands back
	// belongs to a different one entirely — exactly Latest()'s documented cross-session fallback.
	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	require.NotEmpty(t, out.HookSpecificOutput.AdditionalContext,
		"a resumed/forked session must rehydrate from the project's inherited checkpoint, "+
			"never treat a session mismatch as no checkpoint")

	st := rsReadState(t, f.proj.Root)
	require.False(t, st.Degraded, "an inherited checkpoint is a normal, usable one, not a degraded path")
}

// TestService_FreshServiceInstanceNeedsNoWarmup is item 1's fresh-session/restart case, read at
// the daemon-process level: a brand new rehydrateService — as a daemon restart or a first
// SessionStart(compact) on a freshly started daemon both produce — must succeed on its VERY FIRST
// call with no prior event of any kind, PostCompact included, ever having reached it. OnCompact
// holds no state a warmup could populate: s.latest re-reads Checkpoints.Latest fresh every call,
// and the one lazily-opened handle (the ledger) opens on this same first call.
func TestService_FreshServiceInstanceNeedsNoWarmup(t *testing.T) {
	f := rsNewFixture(t) // constructed fresh; nothing has called it before this line

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))

	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	require.NotEmpty(t, out.HookSpecificOutput.AdditionalContext)
	require.Equal(t, 1, f.reader.latests, "the first-ever call already queried the checkpoint store directly")
}

// TestService_RepeatedCompactionIsIndependentAndDoesNotReingestItsOwnInjection covers item 5's
// "repeated compaction" lifecycle case together with item 3 ("previously injected material is not
// new primary evidence"): a session compacts twice. The SECOND payload must be built fresh from
// whatever the checkpoint store reports as latest at THAT call — carrying its own sequence, not
// the first call's — and the first call's own output must itself be fully removable by
// checkpoint.StripInjections, which is the mechanism a later capture pass relies on
// (internal/checkpoint/inject.go's fromStore) to never re-encode a prior rehydration as new
// evidence for the next one.
func TestService_RepeatedCompactionIsIndependentAndDoesNotReingestItsOwnInjection(t *testing.T) {
	f := rsNewFixture(t)

	first, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, first.HookSpecificOutput)
	firstText := first.HookSpecificOutput.AdditionalContext
	require.NotEmpty(t, firstText)
	require.True(t, strings.HasPrefix(firstText, fmt.Sprintf(checkpoint.InjectionOpenTag, 1, checkpoint.SchemaVersion)))

	// This is the round-trip the NEXT checkpoint's own construction depends on: if any byte of a
	// tagged span survived stripping, it would be indistinguishable from a user- or tool-authored
	// prompt to whatever reads the transcript next.
	require.Empty(t, checkpoint.StripInjections(firstText),
		"a rehydration payload must be fully removable by StripInjections; nothing may survive to "+
			"be mistaken for new primary evidence in the next checkpoint")

	// A second, later checkpoint: a repeated compaction of the same session.
	second := rsGoldenCheckpoint(t)
	second.Seq = core.CheckpointSeq(2)
	f.reader.cp = second
	f.reader.ref = checkpoint.Ref{Seq: core.CheckpointSeq(2), Path: f.reader.ref.Path}

	out2, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out2.HookSpecificOutput)
	secondText := out2.HookSpecificOutput.AdditionalContext
	require.NotEmpty(t, secondText)
	require.True(t, strings.HasPrefix(secondText, fmt.Sprintf(checkpoint.InjectionOpenTag, 2, checkpoint.SchemaVersion)),
		"the second compaction must be tagged with ITS OWN checkpoint sequence, not the first's")

	st := rsReadState(t, f.proj.Root)
	require.Equal(t, core.CheckpointSeq(2), st.Seq, "the recorded state reflects the LATEST call, not the first")
}

// TestService_DuplicateCompactEventIsHandledConsistently covers item 5's "duplicate events" case:
// the host redelivers the identical SessionStart(compact) event twice (a retry, a reconnect). Both
// calls must succeed identically — OnCompact keeps no per-event dedup state, so "duplicate" is
// simply "called again" — and the state file must reflect the last call rather than fail or
// double up.
func TestService_DuplicateCompactEventIsHandledConsistently(t *testing.T) {
	f := rsNewFixture(t)
	ev := rsCompactEvent(f.proj.Root)

	out1, err1 := f.svc.OnCompact(context.Background(), ev)
	out2, err2 := f.svc.OnCompact(context.Background(), ev)

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Equal(t, out1, out2, "an identical redelivered event must produce an identical payload")
	require.Equal(t, 2, f.reader.latests, "each delivery re-queries the checkpoint store; neither is silently skipped")
}

// TestService_OutOfOrderCheckpointDeliveryReflectsWhateverIsCurrentlyLatest covers item 5's
// "out-of-order delivery": OnCompact keeps no memory of a previously-seen sequence number, so it
// cannot itself go "out of order" — each call simply reflects whatever Checkpoints.Latest reports
// AT THAT MOMENT, even if that is numerically EARLIER than a sequence a previous call saw.
// Ordering enforcement belongs to the checkpoint reader (SP-10's T10-LIFE), not to this service.
func TestService_OutOfOrderCheckpointDeliveryReflectsWhateverIsCurrentlyLatest(t *testing.T) {
	f := rsNewFixture(t)
	later := rsGoldenCheckpoint(t)
	later.Seq = core.CheckpointSeq(5)
	f.reader.cp = later
	f.reader.ref = checkpoint.Ref{Seq: core.CheckpointSeq(5)}
	_, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(5), rsReadState(t, f.proj.Root).Seq)

	// A later call's reader now reports an EARLIER sequence — e.g. a redelivered or reordered
	// event surfacing a checkpoint a previous call had already moved past.
	earlier := rsGoldenCheckpoint(t)
	earlier.Seq = core.CheckpointSeq(2)
	f.reader.cp = earlier
	f.reader.ref = checkpoint.Ref{Seq: core.CheckpointSeq(2)}
	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "the service trusts the reader's current answer rather than rejecting a lower sequence")
	require.NotNil(t, out.HookSpecificOutput)
	require.Equal(t, core.CheckpointSeq(2), rsReadState(t, f.proj.Root).Seq)
}

// TestService_MissingSessionIDDoesNotPanic covers item 5's "missing events" case at the payload
// level: a SessionStart delivered with no session id (a malformed or truncated event) must degrade
// like any other unusable input, never panic the hook process.
func TestService_MissingSessionIDDoesNotPanic(t *testing.T) {
	f := rsNewFixture(t)
	ev := rsCompactEvent(f.proj.Root)
	ev.SessionID = ""

	var out hookio.Output
	var err error
	require.NotPanics(t, func() {
		out, err = f.svc.OnCompact(context.Background(), ev)
	})
	require.NoError(t, err, "a hook may exit only 0 even for a malformed event")
	_ = out // either an empty or a degraded payload is acceptable; not panicking is the contract
}

// TestService_PanicRecovered: a panic anywhere under the seam is recovered with a Loud line and
// answered with the deferred note. The hook process must still exit 0 — §2.3 permits it no other
// outcome — so a panic that escaped here would take the host's session start with it.
//
// Criterion change (D11, 2026-09-25): this row required an EMPTY output after the panic. A panic is
// a failed build, and D11 rules that a failed build answers with the deferred note, never silence;
// the session.start route already turned this panic into that note through its ticket (C1.16), so
// the service's own answer now says the same thing to any caller, and the drop report records the
// rehydration as never built. Recovery, the nil error and the single Loud are unchanged.
func TestService_PanicRecovered(t *testing.T) {
	f := rsNewFixture(t)
	f.reader.panics = true

	var out hookio.Output
	var err error
	require.NotPanics(t, func() {
		out, err = f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	}, "a panic under the rehydrator must not reach the hook process")
	require.NoError(t, err)
	rsRequireDeferredNote(t, out, daemon.DeferredFailed)
	require.Equal(t, 1, f.log.loudCount(), "a recovered panic is a contract-grade event: LOUD, once")
	rsRequireNotBuiltReport(t, f.proj.Root, "building the rehydration failed")
}

// TestService_RecordsState: the drop report backing the `dropped` MCP tool is only answerable if
// the rehydration wrote down what it did. The seq it records must be the ref's, not a guess.
func TestService_RecordsState(t *testing.T) {
	f := rsNewFixture(t)

	_, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)

	st := rsReadState(t, f.proj.Root)
	require.Equal(t, rsSession, st.Session)
	require.Equal(t, f.reader.ref.Seq, st.Seq, "the recorded seq is the ref's own sequence number")
	require.False(t, st.Degraded)
	require.Equal(t, core.UnixMilli(testutil.Epoch.UnixMilli()), st.Emitted,
		"the one wall-clock read goes through Options.Clock, so a FakeClock pins it exactly")
	require.LessOrEqual(t, int(st.Tokens), int(st.Budget), "a payload never exceeds its own budget")
	require.NotEmpty(t, st.Items, "a non-degraded rehydration emitted items and must say which")
}

// TestService_StateWriteFailureStillEmits: the payload is the product; the state file is
// bookkeeping. A project whose state/ directory cannot be written still gets its context back,
// with the failure warned rather than escalated.
func TestService_StateWriteFailureStillEmits(t *testing.T) {
	if runtime.GOOS == "windows" {
		// A read-only directory bit does not stop a file creation on Windows the way a POSIX mode
		// does; making the directory genuinely unwritable there needs an ACL edit, which would be
		// testing icacls rather than the rehydrator.
		t.Skip("platform: a directory mode bit does not deny file creation on Windows")
	}
	f := rsNewFixture(t)

	stateDir := paths.Of(f.proj.Root).State
	require.NoError(t, os.Chmod(stateDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	require.NotEmpty(t, out.HookSpecificOutput.AdditionalContext,
		"an unwritable state directory costs bookkeeping, never the payload")
	require.GreaterOrEqual(t, f.log.warnCount(), 1, "a failed state write is warned, not swallowed")
}

// TestService_ClearResetsState: `/clear` discards the context deliberately, so the rehydrator's
// record of what it had injected goes with it — and nothing else moves. The store and the ledger
// are the two things a mistaken "clear rebuilds context" implementation would touch, so both are
// asserted at zero calls.
func TestService_ClearResetsState(t *testing.T) {
	f := rsNewFixture(t)

	_, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.FileExists(t, paths.Long(rsStatePath(f.proj.Root)))

	storeBefore, ledgerBefore := f.st.count(), f.ledger.count()

	out, err := f.svc.OnClear(context.Background(), rsClearEvent(f.proj.Root))
	require.NoError(t, err)
	require.True(t, rsIsEmptyOutput(out), "a clear injects nothing")
	require.NoFileExists(t, paths.Long(rsStatePath(f.proj.Root)),
		"the drop report describes the CURRENT context, and a clear has emptied it")

	require.Equal(t, storeBefore, f.st.count(), "OnClear must not touch the store")
	require.Equal(t, ledgerBefore, f.ledger.count(), "OnClear must not touch the ledger")
}

// TestService_ClearOnMissingStateIsNoOp: a `/clear` on a session that never rehydrated is the
// ordinary case, not an error.
func TestService_ClearOnMissingStateIsNoOp(t *testing.T) {
	f := rsNewFixture(t)
	require.NoFileExists(t, paths.Long(rsStatePath(f.proj.Root)))

	out, err := f.svc.OnClear(context.Background(), rsClearEvent(f.proj.Root))
	require.NoError(t, err, "clearing nothing is not a failure")
	require.True(t, rsIsEmptyOutput(out))
	require.Zero(t, f.log.loudCount(), "the ordinary case is not a contract event")
}

// TestService_DeclaresAdditionalContextProducer closes §12.1's not-yet-implemented loop: until a
// producer for hook.additional_context_delivered exists in the build, that assertion reports
// OK/SevInfo/"not-yet-implemented" and its real Check never runs. BindRehydrate is what makes the
// producer exist, and daemon.New's DeclareProducers call is what declares it.
func TestService_DeclaresAdditionalContextProducer(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	require.False(t, contract.HasProducer(contract.CAdditionalContext),
		"a build with no rehydrator declares no additionalContext producer")

	f := rsNewFixture(t)

	o := daemon.NewOptions(f.proj.Root, config.Defaults())
	daemon.BindRehydrate(&o, f.svc)

	// Registered AFTER BindRehydrate so it observes the Services the bind loop has already
	// populated; Bind bodies run in registration order (Options.Bind).
	var seen *daemon.Services
	o.Bind(func(s *daemon.Services) { seen = s })

	d, err := daemon.New(o)
	require.NoError(t, err)
	require.NotNil(t, d)

	require.NotNil(t, seen, "New must run every registered bind")
	require.NotNil(t, seen.Rehydrate, "BindRehydrate must populate Services.Rehydrate")
	require.True(t, contract.HasProducer(contract.CAdditionalContext),
		"DeclareProducers declares CAdditionalContext exactly when Services.Rehydrate is bound")
}

// rsWriteCheckpointChain lays out checkpoints 0001..n of the golden checkpoint under rsSession,
// each chained to the one before it, with a manifest whose digests match, exactly as the writer
// would — through the files alone, as Rule W-2 requires of this file.
func rsWriteCheckpointChain(t *testing.T, root string, n int) {
	t.Helper()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Checkpoints), 0o700))
	var manifest []byte
	for seq := 1; seq <= n; seq++ {
		cp := rsGoldenCheckpoint(t)
		cp.Seq = core.CheckpointSeq(seq)
		cp.Parent = ""
		if seq > 1 {
			cp.Parent = filepath.Base(paths.CheckpointPath(l, core.CheckpointSeq(seq-1)))
		}
		body, err := checkpoint.Marshal(cp)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(paths.Long(paths.CheckpointPath(l, core.CheckpointSeq(seq))), body, 0o600))
		sum := sha256.Sum256(body)
		line, err := json.Marshal(paths.ManifestEntry{
			Seq: core.CheckpointSeq(seq), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body)),
		})
		require.NoError(t, err)
		manifest = append(append(manifest, line...), '\n')
	}
	require.NoError(t, os.WriteFile(paths.Long(paths.ManifestPath(l)), manifest, 0o600))
}

// rsCorruptOneByte flips one byte of checkpoint seq's artifact, as the UAT-03 probe did.
func rsCorruptOneByte(t *testing.T, root string, seq core.CheckpointSeq) {
	t.Helper()
	p := paths.Long(paths.CheckpointPath(paths.Of(root), seq))
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	b[len(b)/2] ^= 0x01
	require.NoError(t, os.WriteFile(p, b, 0o600))
}

// TestService_CheckpointFallbackIsNeverSilent is F-C4-UAT03-1 (owner decision D49) through the real
// reader: checkpoint 0002 does not verify (one byte flipped), the reader steps over it to 0001, and
// the compaction must not present 0001 as current. The payload names the rollback in its header and
// in section 7, the state file reads degraded with the reason and carries the fallback in the drop
// report dropped() serves, and the Loud says what the rehydration was rolled back to.
func TestService_CheckpointFallbackIsNeverSilent(t *testing.T) {
	f := rsNewFixture(t)
	rsWriteCheckpointChain(t, f.proj.Root, 2)
	rsCorruptOneByte(t, f.proj.Root, 2)

	out, err := rsRealReaderService(t, f).OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	ac := out.HookSpecificOutput.AdditionalContext
	require.Contains(t, ac, "checkpoint 0001 (rolled back from 0002)", "the header must not present 0001 as current")
	require.Contains(t, ac, "- checkpoint_fallback 0002 — checkpoint 0002 does not verify",
		"section 7 names the refused checkpoint")

	st := rsReadState(t, f.proj.Root)
	require.Equal(t, core.CheckpointSeq(1), st.Seq)
	require.True(t, st.Degraded, "a rehydration rebuilt from an older checkpoint is degraded")
	require.Equal(t, "checkpoint 0002 does not verify; rolled back to 0001", st.DegradedReason)
	require.NotEmpty(t, st.Dropped)
	require.Equal(t, "checkpoint_fallback", st.Dropped[0].Kind, "the drop report leads with the fallback: %+v", st.Dropped)

	var rolledBack bool
	for _, m := range f.log.loudMsgs() {
		rolledBack = rolledBack || strings.Contains(m, "rolled back to 0001")
	}
	require.True(t, rolledBack, "the Loud must say what the rehydration rolled back to: %q", f.log.loudMsgs())
}

// TestService_CheckpointFallbackToNothingIsNamed: when no recorded checkpoint verifies, the
// rehydration is built from L0 and the ledger, and it still says which checkpoints were refused
// rather than reading as a project that never had one.
func TestService_CheckpointFallbackToNothingIsNamed(t *testing.T) {
	f := rsNewFixture(t)
	rsWriteCheckpointChain(t, f.proj.Root, 1)
	rsCorruptOneByte(t, f.proj.Root, 1)

	out, err := rsRealReaderService(t, f).OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.NotNil(t, out.HookSpecificOutput)
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, "(rolled back from 0001)")

	st := rsReadState(t, f.proj.Root)
	require.True(t, st.Degraded)
	require.Equal(t, "checkpoint 0001 does not verify; rolled back to no checkpoint", st.DegradedReason)
	var rolledBack bool
	for _, m := range f.log.loudMsgs() {
		rolledBack = rolledBack || strings.Contains(m, "rolled back to no checkpoint")
	}
	require.True(t, rolledBack, "%q", f.log.loudMsgs())
}
