// Package storetest is the conformance suite for store.Store and store.SegmentLog
// (00-ARCHITECTURE.md §5.22): every implementation SP-06 ships must pass RunStoreSuite and
// RunSegmentLogSuite. SP-01 ships the suite itself, including the behaviour assertions SP-06
// inherits (Rule W-1) — only each guarded /behaviour block is skipped until a real implementation
// lands.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeBytes is the payload the shape block and the stub probe both PutBytes.
var probeBytes = []byte("storetest-shape-probe")

// RunStoreSuite is the conformance suite for store.Store. name distinguishes multiple factories
// run in the same test binary; factory must return a fresh, ready-to-use Store on every call.
func RunStoreSuite(t *testing.T, name string, factory func(t *testing.T) store.Store) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)
		ctx := context.Background()

		// ── objects ──
		_, err := s.Put(ctx, bytes.NewReader(probeBytes), store.PutOptions{})
		requireKnownError(t, err)
		_, err = s.PutBytes(ctx, probeBytes, store.PutOptions{})
		requireKnownError(t, err)
		_, err = s.GetChunk(ctx, core.Hash{})
		requireKnownError(t, err)
		_, err = s.GetRoot(ctx, core.Hash{})
		requireKnownError(t, err)
		_, err = s.Open(ctx, core.Hash{})
		requireKnownError(t, err)
		_, err = s.OpenSpan(ctx, core.Hash{}, 0, 0)
		requireKnownError(t, err)
		_ = s.Has(core.Hash{}) // no error return; any bool is shape-valid

		// ── tool_use index ──
		err = s.RecordToolUse(ctx, store.ToolUseRecord{})
		requireKnownError(t, err)
		_, err = s.ToolUse(ctx, core.ToolUseID(""))
		requireKnownError(t, err)
		_, err = s.ToolUsesByPath(ctx, "", 0)
		requireKnownError(t, err)
		err = s.MarkSuperseded(ctx, core.ToolUseID(""), core.ToolUseID(""))
		requireKnownError(t, err)

		// ── file version history ──
		err = s.AppendFileVersion(ctx, "", store.FileVersion{})
		requireKnownError(t, err)
		_, err = s.FileHistory(ctx, "")
		requireKnownError(t, err)
		_, err = s.FileAt(ctx, "", time.Time{})
		requireKnownError(t, err)
		_, err = s.ChangedSince(ctx, nil)
		requireKnownError(t, err)

		// ── search ──
		_, err = s.Search(ctx, store.Query{})
		requireKnownError(t, err)

		// Segments has no error return: a usable (if stub) SegmentLog is required, not nil.
		require.NotNil(t, s.Segments())

		_, err = s.Stats(ctx)
		requireKnownError(t, err)
		_, err = s.GC(ctx, store.GCPolicy{})
		requireKnownError(t, err)
		requireKnownError(t, s.Flush(ctx))
		requireKnownError(t, s.Close())
	})

	if skipIfStubStore(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("put_get_round_trip", func(t *testing.T) { runPutGetRoundTripCase(t, factory) })
		t.Run("global_dedup_second_put_is_not_novel", func(t *testing.T) { runGlobalDedupCase(t, factory) })
		t.Run("changed_since_detects_a_hash_change", func(t *testing.T) { runChangedSinceCase(t, factory) })
		t.Run("file_history_is_append_only_and_never_shrinks", func(t *testing.T) { runFileHistoryAppendOnlyCase(t, factory) })
	})
}

// RunSegmentLogSuite is the conformance suite for store.SegmentLog. Unlike RunStoreSuite it takes
// no name parameter (00-ARCHITECTURE.md §5.22): factory must return a fresh, ready-to-use
// SegmentLog on every call.
func RunSegmentLogSuite(t *testing.T, factory func(t *testing.T) store.SegmentLog) {
	t.Helper()

	t.Run("shape", func(t *testing.T) {
		sl := factory(t)
		require.NotNil(t, sl)
		ctx := context.Background()

		_, err := sl.Open(ctx, store.Segment{})
		requireKnownError(t, err)
		err = sl.Close(ctx, core.SegmentID(0), core.TurnIndex(0), nil)
		requireKnownError(t, err)
		_, err = sl.Get(ctx, core.SegmentID(0))
		requireKnownError(t, err)
		_, err = sl.Range(ctx, core.TurnIndex(0), core.TurnIndex(0))
		requireKnownError(t, err)
		_, err = sl.Current(ctx, core.SessionID(""))
		requireKnownError(t, err)
		err = sl.MarkEncoded(ctx, nil, core.CheckpointSeq(0))
		requireKnownError(t, err)
		_, err = sl.Frontier(ctx, core.SessionID(""))
		requireKnownError(t, err)
		_, err = sl.Unencoded(ctx, core.SessionID(""))
		requireKnownError(t, err)
	})

	if skipIfStubSegmentLog(t, factory) {
		return
	}

	t.Run("behaviour", func(t *testing.T) {
		t.Run("mark_encoded_is_the_dpi_guard", func(t *testing.T) { runMarkEncodedDPIGuardCase(t, factory) })
		t.Run("range_never_loses_a_previously_returned_segment", func(t *testing.T) { runRangeNeverShrinksCase(t, factory) })
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

// isStubStore reports whether factory currently produces a stub Store, using PutBytes as the
// probe (plans/OWNERS.tsv: store's probe method is PutBytes).
func isStubStore(t *testing.T, factory func(t *testing.T) store.Store) bool {
	t.Helper()
	_, err := factory(t).PutBytes(context.Background(), probeBytes, store.PutOptions{})
	return core.IsNotImplemented(err)
}

// skipIfStubStore calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub Store, and reports whether it did.
func skipIfStubStore(t *testing.T, factory func(t *testing.T) store.Store) bool {
	t.Helper()
	if isStubStore(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubSegmentLog reports whether factory currently produces a stub SegmentLog, using Open as
// the probe: SegmentLog.Open is the closest analogue, within this interface, of Store's own
// PutBytes probe — the first, minimal write operation a real implementation must support.
func isStubSegmentLog(t *testing.T, factory func(t *testing.T) store.SegmentLog) bool {
	t.Helper()
	_, err := factory(t).Open(context.Background(), store.Segment{})
	return core.IsNotImplemented(err)
}

// skipIfStubSegmentLog calls t.Skip with the exact Rule W-1 message when factory still produces
// a stub SegmentLog, and reports whether it did.
func skipIfStubSegmentLog(t *testing.T, factory func(t *testing.T) store.SegmentLog) bool {
	t.Helper()
	if isStubSegmentLog(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
