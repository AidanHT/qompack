package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/rehydrate"
)

// Owner decision D11 (2026-09-25): a compact SessionStart whose rehydration cannot be built — the
// checkpoint store is unreadable, the build fails, the rehydration panics — is answered with the
// explicit deferred note, exactly as a late one is (C1.16), never with silence. These rows drive the
// session.start route over the SHIPPED rehydration service, so what they pin is the answer the hook
// client hands the host.

// failingReader is a checkpoint.Reader whose every read fails with err (or panics).
type failingReader struct {
	err    error
	panics bool
	reads  int
}

func (r *failingReader) Latest(context.Context, core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	r.reads++
	if r.panics {
		panic("failingReader: deliberate panic from Latest")
	}
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, r.err
}

func (r *failingReader) Get(context.Context, core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, r.err
}

func (r *failingReader) List(context.Context) ([]checkpoint.Ref, error) { return nil, r.err }

func (r *failingReader) Chain(context.Context, core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	return nil, r.err
}

func (r *failingReader) Verify(context.Context) ([]core.CheckpointSeq, error) { return nil, r.err }

var _ checkpoint.Reader = (*failingReader)(nil)

// failureFixture is a daemon whose Rehydrate seam is the shipped rehydration service over reader.
type failureFixture struct {
	dd     *daemon
	log    *recordingLogger
	reader *failingReader
}

func newFailureFixture(t *testing.T, reader *failingReader) *failureFixture {
	t.Helper()
	f := newFailureFixtureOver(t, reader)
	f.reader = reader
	return f
}

// newFailureFixtureOver is newFailureFixture over any checkpoint.Reader; f.reader stays nil.
func newFailureFixtureOver(t *testing.T, reader checkpoint.Reader) *failureFixture {
	t.Helper()
	root := t.TempDir()
	f := &failureFixture{log: newRecordingLogger()}
	o := NewOptions(root, testConfig())
	o.Log = f.log
	var mode func() contract.Mode
	o.Bind(func(s *Services) { mode = s.Mode })
	svc := NewRehydrateService(RehydrateOptions{
		ProjectRoot: root,
		Cfg:         o.Cfg,
		Checkpoints: reader,
		Reporter:    rehydrate.NewReporter(root, f.log),
		Mode:        func() contract.Mode { return mode() },
		Log:         f.log,
	})
	BindRehydrate(&o, svc)
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	// Only the failure itself can answer within compactTestBound: a row that got the note by running
	// out the budget instead would be testing lateness, which session_start_compact_test.go pins.
	dd.compactBudget = compactTestBound + time.Minute
	f.dd = dd
	return f
}

// requireFailureNote asserts the route answered with the deferred note naming reason, the §12.1
// probe after it, within the host cap, and counted and Loud'd as a deferral.
func requireFailureNote(t *testing.T, f *failureFixture, resp ipc.Response, sess core.SessionID, reason string) {
	t.Helper()
	require.True(t, resp.OK)
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, CompactDeferredNote(sess, reason)),
		"the answer must be the deferred note naming why, never silence: %q", ac)
	require.True(t, hasProbeLine(ac), "the contract probe still follows it: %q", ac)
	require.Empty(t, hookio.HostCapOverruns(hookio.ConformOutput(hookio.EventSessionStart, *resp.Output)),
		"every field the host receives stays under its cap")
	require.EqualValues(t, 1, f.dd.m.Snapshot().Counters[counterCompactDeferred])
	require.Contains(t, f.log.msgs(logLoud), "daemon: compact SessionStart answered without its rehydration")
}

// TestSessionStartCompact_UnreadableCheckpointStoreIsAnsweredWithTheNote: the checkpoint store
// cannot be read, so nothing is built — and the host is told so, at once, rather than answered with
// the probe alone. Before D11 the answer carried no note.
func TestSessionStartCompact_UnreadableCheckpointStoreIsAnsweredWithTheNote(t *testing.T) {
	f := newFailureFixture(t, &failingReader{err: errors.New("checkpoint: manifest: access is denied")})
	const sess = core.SessionID("sess-d11-store")

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound,
		"an unreadable checkpoint store must answer the route at once")
	requireFailureNote(t, f, resp, sess, DeferredCheckpointUnreadable)
	require.Equal(t, 1, f.reader.reads)

	joinReplyWork(t, f.dd)
	drops, err := rehydrate.NewReporter(f.dd.root, f.log).CurrentDrops(context.Background(), sess)
	require.NoError(t, err)
	require.NotEmpty(t, drops, "dropped() must describe this compaction, not an earlier one")
	require.Equal(t, undeliveredDropKind, drops[0].Kind)
	require.True(t, strings.HasPrefix(drops[0].Detail, "not delivered: "), "never reported as delivered: %q", drops[0].Detail)
}

// stoppingReader is a checkpoint.Reader whose Latest waits for its context to end and then returns
// that context's error, as the shipped reader does once its context has ended (fileReader.load).
type stoppingReader struct {
	failingReader
	entered chan struct{}
}

func (r *stoppingReader) Latest(ctx context.Context, _ core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	close(r.entered)
	<-ctx.Done()
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, ctx.Err()
}

// TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping: Stop cancels a compact
// rehydration's context (startReplyWork, through promptCtx) while it is reading the checkpoint store.
// The store did nothing wrong, so the route answers that the daemon was shutting down, and dropped()
// says the same — never that the checkpoint store could not be read.
func TestSessionStartCompact_StopDuringTheCheckpointReadIsAnsweredAsStopping(t *testing.T) {
	reader := &stoppingReader{entered: make(chan struct{})}
	f := newFailureFixtureOver(t, reader)
	const sess = core.SessionID("sess-d11-stopping")

	go func() {
		select {
		case <-reader.entered:
			f.dd.promptCancel() // what Stop does to the reply work it joins
		case <-time.After(compactTestBound):
		}
	}()
	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound,
		"a rehydration the daemon stopped must answer the route at once")
	requireFailureNote(t, f, resp, sess, DeferredStopping)
	require.NotContains(t, f.log.msgs(logLoud), "rehydrate: checkpoint unreadable",
		"a healthy store is never blamed")

	joinReplyWork(t, f.dd)
	drops, err := rehydrate.NewReporter(f.dd.root, f.log).CurrentDrops(context.Background(), sess)
	require.NoError(t, err)
	require.NotEmpty(t, drops, "dropped() must describe this compaction, not an earlier one")
	require.Equal(t, undeliveredDropKind, drops[0].Kind)
	require.True(t, strings.HasPrefix(drops[0].Detail, notBuiltStopping), "%q", drops[0].Detail)
	require.NotContains(t, drops[0].Detail, "checkpoint store could not be read")
}

// TestSessionStartCompact_PanickingRehydrationServiceIsAnsweredWithTheNote: the shipped service's own
// recovery, not the route's, catches a panic under the checkpoint read, and the route still answers
// with the note at once.
func TestSessionStartCompact_PanickingRehydrationServiceIsAnsweredWithTheNote(t *testing.T) {
	f := newFailureFixture(t, &failingReader{panics: true})
	const sess = core.SessionID("sess-d11-panic")

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound,
		"a panicking rehydration must answer the route at once")
	requireFailureNote(t, f, resp, sess, DeferredFailed)
}

// TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote: a Rehydrate seam that returns
// an error (a failed build reported rather than recovered) is a failure, not an empty rehydration:
// the route answers with the note. Before D11 it answered the probe alone.
func TestSessionStartCompact_FailedRehydrationSeamIsAnsweredWithTheNote(t *testing.T) {
	f := newCompactFixture(t, func(context.Context, *compactFixture) (hookio.Output, error) {
		return hookio.Empty(), errors.New("rehydrate: build failed")
	})
	f.dd.compactBudget = compactTestBound + time.Minute
	const sess = core.SessionID("sess-d11-seam-error")

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound,
		"a failed rehydration must answer the route at once")
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, CompactDeferredNote(sess, DeferredFailed)), "%q", ac)
	require.True(t, hasProbeLine(ac))
	require.EqualValues(t, 1, f.dd.m.Snapshot().Counters[counterCompactDeferred])
}

// TestSessionStartCompact_DegradedPassiveAnswersNothingForABrokenStore: D11 changes nothing about
// §12.1. Under degraded-passive nothing is injected — no rehydration, no note — and the checkpoint
// store is not even read.
func TestSessionStartCompact_DegradedPassiveAnswersNothingForABrokenStore(t *testing.T) {
	f := newFailureFixture(t, &failingReader{err: errors.New("io")})
	f.dd.monitor.Degrade("forced for test", []contract.Result{
		{ID: "x.forced", OK: false, Severity: contract.SevCritical, Expected: "e", Observed: "o"},
	})

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-d11-passive"), compactTestBound, "no answer")
	require.True(t, resp.OK)
	require.Empty(t, additionalContext(resp.Output), "degraded-passive injects nothing, a note included")
	require.Zero(t, f.reader.reads, "and does not read the store")
	require.Zero(t, f.dd.m.Snapshot().Counters[counterCompactDeferred])
}
