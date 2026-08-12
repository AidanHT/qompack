// Package checkpointtest is the conformance suite for checkpoint.Writer, checkpoint.Reader and
// checkpoint.Truncate (00-ARCHITECTURE.md §5.22): every implementation SP-10 ships must pass
// RunWriterSuite, RunReaderSuite and RunTruncateSuite. SP-01 ships the suites themselves,
// including the behaviour assertions SP-10 inherits (Rule W-1) — only each guarded /behaviour
// block is skipped until a real implementation lands.
//
// Three of the seams here cannot be driven from inside this package alone. A <pkg>test
// subpackage's import allow-set is its own base package plus testutil and core
// (00-ARCHITECTURE.md §3.2), so checkpointtest may not import store, negknow, pins, dag, grammar,
// tokens or config — and a populated checkpoint.SourceSet needs six of those, while
// checkpoint.Truncate's own signature names the other two. The suites therefore take a fixture
// from the caller (WriterFixture, ReaderFixture) or a pre-bound closure (TruncateFunc): the
// caller, which is allowed to import those packages, supplies the values; the suite, which is
// not, supplies every assertion.
package checkpointtest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// WriterFixture is everything RunWriterSuite needs to drive a checkpoint.Writer end to end.
//
// Source is supplied by the caller rather than built here because a populated SourceSet holds a
// store.Store, a store.SegmentLog, a negknow.Ledger, a pins.Store, a dag.Graph, a
// grammar.Sequitur and a tokens.Estimator, none of which this package may import (§3.2). A stub
// caller may leave it zero; a real caller must populate it, because Begin reads exclusively from
// it — that is the whole point of §5.14's source rule.
type WriterFixture struct {
	// Writer is the implementation under test.
	Writer checkpoint.Writer
	// Source is the SourceSet Begin reads from.
	Source checkpoint.SourceSet
	// Session is the session to write a checkpoint for.
	Session core.SessionID
	// Segments are CLOSED, UNENCODED segment ids Advance may encode. Encoding each of them twice
	// is what exercises the §4.6 DPI guard, so a real caller must supply at least one.
	Segments []core.SegmentID
	// Budget is the token budget Finalize is called with.
	Budget core.Tokens
}

// ReaderFixture is everything RunReaderSuite needs to drive a checkpoint.Reader.
//
// A real caller supplies a Reader over a store that already holds at least one finalized
// checkpoint, plus the session and sequence number that identify it; a stub caller may leave
// everything but Reader zero.
type ReaderFixture struct {
	// Reader is the implementation under test.
	Reader checkpoint.Reader
	// Session is a session with at least one checkpoint on disk.
	Session core.SessionID
	// Seq is a checkpoint sequence number that exists on disk.
	Seq core.CheckpointSeq
	// AbsentSeq is a sequence number that does NOT exist on disk, used to pin the ErrNotFound
	// answer. A real caller must make it genuinely absent.
	AbsentSeq core.CheckpointSeq
}

// TruncateFunc is checkpoint.Truncate with its config.TiersCfg and tokens.Estimator arguments
// already bound by the caller — this package may import neither (§3.2), so the caller supplies
// the tier assignment and the estimator, and the suite supplies every assertion about the
// resulting tier ORDER, which is the part §6.9 actually fixes.
type TruncateFunc func(c checkpoint.Checkpoint, budget core.Tokens) (checkpoint.Checkpoint, []checkpoint.DropEntry)

// RunWriterSuite is the conformance suite for checkpoint.Writer. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use fixture on
// every call.
func RunWriterSuite(t *testing.T, name string, factory func(t *testing.T) WriterFixture) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		f := factory(t)
		require.NotNil(t, f.Writer)
		ctx := context.Background()

		d, err := f.Writer.Begin(ctx, f.Session, core.CheckpointSeq(0), f.Source)
		requireKnownError(t, err)

		_, err = f.Writer.Advance(ctx, d, f.Segments)
		requireKnownError(t, err)

		_, err = f.Writer.Finalize(ctx, d, f.Budget)
		requireKnownError(t, err)

		requireKnownError(t, f.Writer.Abort(d))
	})

	if skipIfStubWriter(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("begin_advance_finalize_produces_a_verifiable_ref", func(t *testing.T) {
			runWriterRoundTripCase(t, factory)
		})
		t.Run("advance_is_the_dpi_guard", func(t *testing.T) { runAdvanceDPIGuardCase(t, factory) })
		t.Run("abort_writes_nothing", func(t *testing.T) { runAbortCase(t, factory) })
	})
}

// RunReaderSuite is the conformance suite for checkpoint.Reader. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use fixture on
// every call.
func RunReaderSuite(t *testing.T, name string, factory func(t *testing.T) ReaderFixture) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		f := factory(t)
		require.NotNil(t, f.Reader)
		ctx := context.Background()

		_, _, err := f.Reader.Latest(ctx, f.Session)
		requireKnownError(t, err)

		_, _, err = f.Reader.Get(ctx, f.Seq)
		requireKnownError(t, err)

		_, err = f.Reader.List(ctx)
		requireKnownError(t, err)

		_, err = f.Reader.Chain(ctx, f.Seq)
		requireKnownError(t, err)

		_, err = f.Reader.Verify(ctx)
		requireKnownError(t, err)
	})

	if skipIfStubReader(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("latest_and_get_agree_and_round_trip_the_schema", func(t *testing.T) {
			runReaderSchemaRoundTripCase(t, factory)
		})
		t.Run("list_is_ascending_and_chain_walks_parents", func(t *testing.T) {
			runReaderListAndChainCase(t, factory)
		})
		t.Run("verify_reports_no_mismatch_on_an_untampered_store", func(t *testing.T) {
			runReaderVerifyCase(t, factory)
		})
		t.Run("an_absent_seq_is_err_not_found", func(t *testing.T) { runReaderAbsentSeqCase(t, factory) })
		t.Run("no_checkpoint_carries_a_code_block", func(t *testing.T) { runReaderNoCodeBlocksCase(t, factory) })
	})
}

// RunTruncateSuite is the conformance suite for checkpoint.Truncate. name distinguishes multiple
// factories run in the same test binary; factory must return a TruncateFunc bound to a fresh tier
// assignment and estimator on every call.
func RunTruncateSuite(t *testing.T, name string, factory func(t *testing.T) TruncateFunc) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		truncate := factory(t)
		require.NotNil(t, truncate)

		// Truncate has no error return, so "shape" here is only that it is callable with both an
		// empty and a populated checkpoint and returns a usable Checkpoint either way.
		got, drops := truncate(checkpoint.Checkpoint{}, generousBudget)
		require.Equal(t, checkpoint.Checkpoint{}, got, "an empty checkpoint has nothing to truncate")
		require.Empty(t, drops)

		got, _ = truncate(oversizedCheckpoint(), generousBudget)
		require.Equal(t, core.SessionID(fixtureSession), got.Session,
			"Truncate must preserve the artifact's identity, whatever it drops")
	})

	if skipIfStubTruncate(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("a_generous_budget_drops_nothing", func(t *testing.T) { runTruncateNoOpCase(t, factory) })
		t.Run("tier1_is_never_truncated", func(t *testing.T) { runTruncateTier1NeverCase(t, factory) })
		t.Run("tier3_is_exhausted_before_tier2", func(t *testing.T) { runTruncateTierOrderCase(t, factory) })
		t.Run("shrinking_the_budget_never_restores_content", func(t *testing.T) {
			runTruncateMonotoneCase(t, factory)
		})
		t.Run("truncate_is_deterministic_and_does_not_mutate_its_input", func(t *testing.T) {
			runTruncatePurityCase(t, factory)
		})
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStubWriter reports whether factory currently produces a stub Writer, using Begin as the probe
// (plans/OWNERS.tsv: checkpoint's probe method is Begin).
func isStubWriter(t *testing.T, factory func(t *testing.T) WriterFixture) bool {
	t.Helper()
	f := factory(t)
	_, err := f.Writer.Begin(context.Background(), f.Session, core.CheckpointSeq(0), f.Source)
	return core.IsNotImplemented(err)
}

// skipIfStubWriter calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub Writer, and reports whether it did.
func skipIfStubWriter(t *testing.T, factory func(t *testing.T) WriterFixture) bool {
	t.Helper()
	if isStubWriter(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubReader reports whether factory currently produces a stub Reader, using Latest as the
// probe: plans/OWNERS.tsv names Begin for the checkpoint package as a whole, which is a Writer
// method, so Latest is Reader's own closest analogue — the first read a real implementation must
// support, exactly as storetest uses SegmentLog.Open in place of Store.PutBytes.
func isStubReader(t *testing.T, factory func(t *testing.T) ReaderFixture) bool {
	t.Helper()
	f := factory(t)
	_, _, err := f.Reader.Latest(context.Background(), f.Session)
	return core.IsNotImplemented(err)
}

// skipIfStubReader calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub Reader, and reports whether it did.
func skipIfStubReader(t *testing.T, factory func(t *testing.T) ReaderFixture) bool {
	t.Helper()
	if isStubReader(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubTruncate reports whether factory currently produces a stub Truncate.
//
// Truncate has no error return, so core.IsNotImplemented cannot be asked: the probe has to be
// behavioural. Returning the input untouched, with no drops, under a budget of one token is a
// definitive stub signature — an oversized checkpoint cannot fit in one token, so any real
// implementation must remove something and say so. The trade-off is stated plainly: a real
// implementation that wrongly returned the identity here would be SKIPPED rather than FAILED by
// this suite. That is the price of a function §5.14 gives no error channel, and it is why SP-10
// must also keep the tier-order cases below green rather than relying on the probe alone.
func isStubTruncate(t *testing.T, factory func(t *testing.T) TruncateFunc) bool {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()
	got, drops := truncate(in, starvedBudget)
	return len(drops) == 0 && unchanged(in, got)
}

// skipIfStubTruncate calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub Truncate, and reports whether it did.
func skipIfStubTruncate(t *testing.T, factory func(t *testing.T) TruncateFunc) bool {
	t.Helper()
	if isStubTruncate(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
