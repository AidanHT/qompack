package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// recoveryRetireFixture ends sess through an accepted flush while another session's hook is part-way
// through writing a line to its client spool, so the end's own drain meets bytes it cannot replay yet
// and is incomplete for that alone. It returns the daemon, the project root, and finish, which writes
// the rest of that line as the hook would.
func recoveryRetireFixture(t *testing.T, sess core.SessionID, nonce int) (*daemon, string, func()) {
	t.Helper()
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	const base = "client-9401.ndjson"
	line := hookSpoolLine(t, liveOrderTool(dd, root, "sess-recovery-writer", 1))
	half := len(line) / 2
	writeRawSpool(t, root, base, line[:half])

	resp := dd.dispatchOp(context.Background(), flushAsyncRequest(dd, root, sess, orderNonce(nonce)))
	require.True(t, resp.OK, "the flush is durable, so it is acknowledged: %q", resp.Err)
	flushAsyncAwait(t, dd)
	gaps := dd.DrainGaps()
	require.True(t, gaps.Observed, "fixture: the end ran its drain")
	require.False(t, gaps.Complete, "fixture: the end's drain met the line still being written")
	for _, g := range gaps.Gaps {
		require.Equal(t, DrainGapPending, g.Kind, "fixture: the line still being written is the drain's only gap")
	}
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess, "fixture: an end whose drain was incomplete keeps its marker for now")
	require.Equal(t, recoveryStageDrain, sr.Sessions[sess].Stage, "fixture: the end got as far as its drain")

	finish := func() {
		f, err := os.OpenFile(paths.Long(filepath.Join(paths.Of(root).Spool, base)), os.O_APPEND|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		_, err = f.Write(line[half:])
		require.NoError(t, f.Close())
		require.NoError(t, err)
	}
	return dd, root, finish
}

// TestSessionRecovery_AnEndLeftOnlyBytesStillBeingWrittenIsClearedByTheNextCompletePass: a session's
// recovery marker says its end has not been observed to finish, and one that survives is meant to be a
// genuine interruption (clearRecoveryNeeded). The end cleared it only when its own last drain came out
// complete, and nothing looked again, so an end whose drain met another hook's line part-way through
// being written — nothing lost, the next pass replays it — stayed marked as needing recovery for good.
// That is the shape of the macos-15-intel red in which the integration suite waited 30 s for a marker
// that never cleared (ci.yml run 38478943482). The end now asks for another pass, and the first full
// pass after it that comes out complete clears the marker.
func TestSessionRecovery_AnEndLeftOnlyBytesStillBeingWrittenIsClearedByTheNextCompletePass(t *testing.T) {
	const sess core.SessionID = "sess-recovery-writing"
	dd, root, finish := recoveryRetireFixture(t, sess, 71)
	select {
	case <-dd.ing.drainKick:
	default:
		require.FailNow(t, "an end whose drain was left only bytes still being written asks for another pass")
	}

	finish()
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.True(t, dd.DrainGaps().Complete, "fixture: once the line is whole the pass replays the whole spool")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.NotContains(t, sr.Sessions, sess,
		"a complete pass after an end whose drain lost nothing observes that end finished")
}

// TestSessionRecovery_AnIncompletePassClearsNoEnd: until a pass comes out complete, the end stays
// marked.
func TestSessionRecovery_AnIncompletePassClearsNoEnd(t *testing.T) {
	const sess core.SessionID = "sess-recovery-still-writing"
	dd, root, _ := recoveryRetireFixture(t, sess, 72)
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.False(t, dd.DrainGaps().Complete, "fixture: the line is still being written")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess, "an incomplete pass has not observed the end finish")
}

// TestSessionRecovery_ALaterEndOfTheSameSessionKeepsItsOwnMarker: what a complete pass clears is the
// marker the waiting end left. A later end of the same session that has begun since — a resumed
// session ending again — is marked by its own begin, and that marker is its own end's to clear.
func TestSessionRecovery_ALaterEndOfTheSameSessionKeepsItsOwnMarker(t *testing.T) {
	const sess core.SessionID = "sess-recovery-ended-again"
	dd, root, finish := recoveryRetireFixture(t, sess, 73)
	dd.markRecoveryNeeded(sess, recoveryStageBegin, 0) // the later end begins

	finish()
	_, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.True(t, dd.DrainGaps().Complete, "fixture: once the line is whole the pass replays the whole spool")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess, "the later end's marker stays")
	require.Equal(t, recoveryStageBegin, sr.Sessions[sess].Stage)
}

// TestSessionRecovery_AnEndWhoseDrainLostALineStaysMarked: a drain gap that is a loss — here a line
// that would not decode, skipped for good — is not something a later pass replays, so a later complete
// pass says nothing about it. The end that met it stays marked as needing recovery.
func TestSessionRecovery_AnEndWhoseDrainLostALineStaysMarked(t *testing.T) {
	dd, hold, root := flushAsyncDaemon(t)
	hold.open()
	ctx := context.Background()
	const sess core.SessionID = "sess-recovery-lost"
	writeRawSpool(t, root, "client-9402.ndjson", []byte("{not json}\n"))

	resp := dd.dispatchOp(ctx, flushAsyncRequest(dd, root, sess, orderNonce(74)))
	require.True(t, resp.OK, "the flush is durable, so it is acknowledged: %q", resp.Err)
	flushAsyncAwait(t, dd)
	gaps := dd.DrainGaps()
	require.False(t, gaps.Complete, "fixture: the end's drain met the corrupt line")
	require.Equal(t, DrainGapCorruptLine, gapOfKind(t, gaps, DrainGapCorruptLine).Kind)

	_, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.True(t, dd.DrainGaps().Complete, "fixture: the corrupt line was consumed, so the next pass is complete")
	sr, err := LoadSessionRecovery(root)
	require.NoError(t, err)
	require.Contains(t, sr.Sessions, sess, "an end whose drain lost a line is not cleared by a later pass")
	require.Equal(t, recoveryStageDrain, sr.Sessions[sess].Stage)
}
