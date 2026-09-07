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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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

	p := testutil.NewProject(t)
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
		Cfg:         p.Cfg,
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

// TestService_CheckpointErrorEmitsNothing separates "there is no checkpoint" from "the checkpoint
// store is broken". The first is normal; the second is a fault, and injecting a half-known context
// on top of a broken store would be worse than injecting nothing.
func TestService_CheckpointErrorEmitsNothing(t *testing.T) {
	f := rsNewFixture(t)
	f.reader.err = errors.New("io")

	out, err := f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	require.NoError(t, err, "a hook may exit only 0 (§2.3); the failure is reported, never returned")
	require.True(t, rsIsEmptyOutput(out), "an unreadable checkpoint store emits nothing")
	require.Equal(t, 1, f.log.loudCount(), "a broken checkpoint store is never silent (§12)")
}

// TestService_PanicRecovered: a panic anywhere under the seam becomes an empty payload and a Loud
// line. The hook process must still exit 0 — §2.3 permits it no other outcome — so a panic that
// escaped here would take the host's session start with it.
func TestService_PanicRecovered(t *testing.T) {
	f := rsNewFixture(t)
	f.reader.panics = true

	var out hookio.Output
	var err error
	require.NotPanics(t, func() {
		out, err = f.svc.OnCompact(context.Background(), rsCompactEvent(f.proj.Root))
	}, "a panic under the rehydrator must not reach the hook process")
	require.NoError(t, err)
	require.True(t, rsIsEmptyOutput(out))
	require.Equal(t, 1, f.log.loudCount(), "a recovered panic is a contract-grade event: LOUD, once")
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
