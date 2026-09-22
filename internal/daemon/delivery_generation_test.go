package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// These tests pin the capacity mechanism the SP20-D4 adjudication requires: immutable generations
// with an exact bounded on-disk index that never forgets a nonce, never restarts a session's arrivals,
// re-resolves an archived ACK's full identity (no unchecked joinOK), resolves a terminal's archived
// lease, and treats a missing/torn manifest as unavailable — never a fresh identity. They drive the
// store directly (>=70 generations) because the live journal's low-threshold rollover wiring is main's.

func newTestGenerations(t *testing.T) *deliveryGenerations {
	t.Helper()
	g, err := openDeliveryGenerations(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.close() })
	return g
}

func genNonce(i int) string { return fmt.Sprintf("%064x", i+1) }

func mkGenLease(t *testing.T, delivery string, session core.SessionID, arrival uint64) deliveryLease {
	t.Helper()
	id, err := core.NewObservationID(session, arrival)
	require.NoError(t, err)
	var rh core.Hash
	rh[0] = 0x11
	l := deliveryLease{
		Version: core.EvidenceVersion, Delivery: delivery, Session: session,
		RequestHash: rh, ArrivalSeq: arrival, ObservationID: id,
	}
	require.True(t, validDeliveryLease(l), "fixture lease must be valid")
	return l
}

// TestDeliveryGeneration_ManyGenerationsAndReplayIdentity commits far more than any 64-segment cap and
// checks every nonce resolves to its exact lease in the latest generation, and that re-committing an
// existing lease is idempotent (identity unchanged, no new generation).
func TestDeliveryGeneration_ManyGenerationsAndReplayIdentity(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	const n = 75

	leases := make([]deliveryLease, n)
	for i := 0; i < n; i++ {
		leases[i] = mkGenLease(t, genNonce(i), "sess-A", uint64(i+1))
		root, err := g.commit(ctx, []deliveryLease{leases[i]})
		require.NoError(t, err)
		require.False(t, root.isZero())
	}
	require.Equal(t, int64(n), g.generationCount())
	require.Equal(t, int64(0), g.oldestGenerationSeq())

	for i := 0; i < n; i++ {
		got, found, err := g.resolveLease(ctx, genNonce(i))
		require.NoError(t, err, "nonce %d", i)
		require.True(t, found, "nonce %d must resolve across %d generations", i, n)
		require.Equal(t, leases[i], got)
	}

	before := g.currentRoot()
	root, err := g.commit(ctx, []deliveryLease{leases[0]})
	require.NoError(t, err)
	require.Equal(t, before, root, "re-committing an existing lease must be idempotent")
	require.Equal(t, int64(n), g.generationCount(), "no new generation for an idempotent re-commit")
}

// TestDeliveryGeneration_DormantSessionArrivalContinuity: a session dormant across many generations
// still returns its last arrival, so its next arrival is dense and never restarts at zero.
func TestDeliveryGeneration_DormantSessionArrivalContinuity(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	i := 0
	commit := func(l deliveryLease) { _, err := g.commit(ctx, []deliveryLease{l}); require.NoError(t, err) }

	commit(mkGenLease(t, genNonce(i), "sess-A", 1))
	i++
	commit(mkGenLease(t, genNonce(i), "sess-B", 1)) // B leases once, then goes dormant
	i++
	for a := uint64(2); a <= 42; a++ { // A keeps leasing for 41 more generations
		commit(mkGenLease(t, genNonce(i), "sess-A", a))
		i++
	}

	last, found, err := g.lastArrival(ctx, "sess-B")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), last, "a dormant session's arrival continues from 1 → next is 2")

	last, found, err = g.lastArrival(ctx, "sess-A")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(42), last)

	_, found, err = g.lastArrival(ctx, "sess-never")
	require.NoError(t, err)
	require.False(t, found, "a session that never leased is a genuine absence, not a fresh zero")
}

// TestDeliveryGeneration_ArchivedAckExactJoinAndCorruptionRefusal: a good ACK verifies against its
// resolved lease; a tampered identity is a conflict; a never-leased ACK is unavailable; and a corrupt
// index page is unavailable — never a fresh or blindly-accepted identity.
func TestDeliveryGeneration_ArchivedAckExactJoinAndCorruptionRefusal(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	l := mkGenLease(t, genNonce(0), "sess-A", 1)
	_, err := g.commit(ctx, []deliveryLease{l})
	require.NoError(t, err)

	good := deliveryAck{Version: core.EvidenceVersion, Delivery: l.Delivery, ObservationID: l.ObservationID}
	require.NoError(t, g.verifyArchivedAck(ctx, good), "an ACK matching its resolved lease verifies")

	tampered := good
	tampered.ObservationID = core.ObservationID("sha256:" + genNonce(9))
	require.ErrorIs(t, g.verifyArchivedAck(ctx, tampered), errGenerationConflict,
		"an ACK whose observation id disagrees with the lease is a conflict, not accepted")

	never := deliveryAck{Version: core.EvidenceVersion, Delivery: genNonce(5), ObservationID: l.ObservationID}
	require.ErrorIs(t, g.verifyArchivedAck(ctx, never), errGenerationUnavailable,
		"an ACK whose lease does not resolve is unavailable, never a blind join")

	// Corrupt the current root page: the join can no longer be read → unavailable, never fresh.
	require.NoError(t, os.WriteFile(paths.Long(g.radix.pagePath(g.currentRoot())), []byte("corrupt"), 0o600))
	err = g.verifyArchivedAck(ctx, good)
	require.Error(t, err)
	require.True(t, err == errRadixUnavailable || err == errGenerationUnavailable, "corruption must be unavailable, got %v", err)
}

// TestDeliveryGeneration_OrderedByArrivalFrontier: (session,arrival) → nonce resolves, which is the
// ordered access a predecessor/oldest-pending query walks; a missing arrival is a sound absence.
func TestDeliveryGeneration_OrderedByArrivalFrontier(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	want := map[uint64]string{}
	for a := uint64(1); a <= 5; a++ {
		nonce := genNonce(int(a))
		want[a] = nonce
		_, err := g.commit(ctx, []deliveryLease{mkGenLease(t, nonce, "sess-A", a)})
		require.NoError(t, err)
	}
	// The oldest pending for the session is found by walking arrivals ascending from 1.
	oldest, found, err := g.nonceAtArrival(ctx, "sess-A", 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want[1], oldest)
	for a := uint64(2); a <= 5; a++ {
		got, found, err := g.nonceAtArrival(ctx, "sess-A", a)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, want[a], got)
	}
	_, found, err = g.nonceAtArrival(ctx, "sess-A", 99)
	require.NoError(t, err)
	require.False(t, found, "an unassigned arrival is a sound absence")

	_, found, err = g.nonceAtArrival(ctx, "sess-other", 1)
	require.NoError(t, err)
	require.False(t, found, "a session that never leased has no ordered access")
}

// TestDeliveryGeneration_TerminalArchivedLeaseResolves: a terminal disposition whose original lease is
// archived out of the active map still resolves through the store, so the terminal loader need not
// fail merely because the lease left RAM.
func TestDeliveryGeneration_TerminalArchivedLeaseResolves(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	first := mkGenLease(t, genNonce(0), "sess-A", 1)
	_, err := g.commit(ctx, []deliveryLease{first})
	require.NoError(t, err)
	for i := 1; i <= 30; i++ { // many later generations; the first lease is archived, never forgotten
		_, err := g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(i), "sess-A", uint64(i+1))})
		require.NoError(t, err)
	}
	got, found, err := g.resolveTerminalLease(ctx, first.Delivery)
	require.NoError(t, err)
	require.True(t, found, "a terminal's archived lease must be resolvable by exact nonce")
	require.Equal(t, first, got)
}

// TestDeliveryGeneration_RestartAfterNewWrites: a reopened store answers every prior generation and
// accepts new commits that also survive a further reopen.
func TestDeliveryGeneration_RestartAfterNewWrites(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err := g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(i), "sess-A", uint64(i+1))})
		require.NoError(t, err)
	}
	require.NoError(t, g.close())

	g2, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	require.Equal(t, int64(5), g2.generationCount())
	for i := 5; i < 9; i++ {
		_, err := g2.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(i), "sess-A", uint64(i+1))})
		require.NoError(t, err)
	}
	require.NoError(t, g2.close())

	g3, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g3.close() })
	require.Equal(t, int64(9), g3.generationCount())
	for i := 0; i < 9; i++ {
		_, found, err := g3.resolveLease(ctx, genNonce(i))
		require.NoError(t, err)
		require.True(t, found, "generation %d must survive two restarts", i)
	}
}

// TestDeliveryGeneration_MissingHeadIsUnavailable: a manifest with no head is inconsistent and refused.
func TestDeliveryGeneration_MissingHeadIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	_, err = g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(0), "sess-A", 1)})
	require.NoError(t, err)
	head := g.headPath
	require.NoError(t, g.close())

	require.NoError(t, os.Remove(paths.Long(head)))
	_, err = openDeliveryGenerations(dir)
	require.ErrorIs(t, err, errGenerationUnavailable, "a manifest with no head must be unavailable, not fresh")
}

// TestDeliveryGeneration_TornTailIsPreservedAndUnavailable: a torn partial record beyond the committed
// head refuses the open and leaves the ambiguous bytes on disk (never truncated, never fresh identity).
func TestDeliveryGeneration_TornTailIsPreservedAndUnavailable(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	_, err = g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(0), "sess-A", 1)})
	require.NoError(t, err)
	logPath := g.logPath
	require.NoError(t, g.close())

	before, err := os.ReadFile(paths.Long(logPath))
	require.NoError(t, err)
	f, err := os.OpenFile(paths.Long(logPath), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte(`{"v":1,"seq":1,"root":"deadbeef`)) // torn: no closing brace, no newline
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = openDeliveryGenerations(dir)
	require.ErrorIs(t, err, errGenerationUnavailable, "a torn manifest tail must be unavailable")

	after, err := os.ReadFile(paths.Long(logPath))
	require.NoError(t, err)
	require.Greater(t, len(after), len(before), "the ambiguous torn bytes must be preserved, not truncated")
}

// TestDeliveryGeneration_CompleteTailAfterLostHeadWriteIsAdopted simulates a crash after a generation's
// manifest record was appended but before its head write landed: reopen adopts the complete, chained
// tail rather than inventing a new identity or discarding the committed generation.
func TestDeliveryGeneration_CompleteTailAfterLostHeadWriteIsAdopted(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	_, err = g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(0), "sess-A", 1)})
	require.NoError(t, err)
	headAfterOne, err := os.ReadFile(paths.Long(g.headPath)) // the head naming generation 0
	require.NoError(t, err)
	_, err = g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(1), "sess-A", 2)}) // generation 1
	require.NoError(t, err)
	require.NoError(t, g.close())

	// Roll the head back to generation 0: the log holds record 0 and record 1, the head names 0.
	require.NoError(t, os.WriteFile(paths.Long(g.headPath), headAfterOne, 0o600))

	g2, err := openDeliveryGenerations(dir)
	require.NoError(t, err, "a complete chained tail beyond the head is adopted, not refused")
	t.Cleanup(func() { _ = g2.close() })
	require.Equal(t, int64(2), g2.generationCount(), "the recovered generation is adopted")
	_, found, err := g2.resolveLease(ctx, genNonce(1))
	require.NoError(t, err)
	require.True(t, found, "the recovered generation's lease resolves after adoption")
}

// TestDeliveryGeneration_EmptyStoreIsAbsentNotUnavailable: a fresh store answers a real absence.
func TestDeliveryGeneration_EmptyStoreIsAbsentNotUnavailable(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	require.Equal(t, int64(0), g.generationCount())
	require.Equal(t, int64(-1), g.oldestGenerationSeq())
	require.True(t, g.currentRoot().isZero())
	_, found, err := g.resolveLease(ctx, genNonce(0))
	require.NoError(t, err)
	require.False(t, found)
}

// TestDeliveryGeneration_InvalidLeaseAndCancellation: an identity-mismatched lease is refused before
// any write, and a cancelled context stops the operations.
func TestDeliveryGeneration_InvalidLeaseAndCancellation(t *testing.T) {
	g := newTestGenerations(t)

	bad := mkGenLease(t, genNonce(0), "sess-A", 1)
	bad.ObservationID = core.ObservationID("sha256:" + genNonce(2)) // no longer H(session,arrival)
	_, err := g.commit(context.Background(), []deliveryLease{bad})
	require.ErrorIs(t, err, core.ErrContract, "a lease whose identity does not check is refused, not committed")

	_, err = g.commit(context.Background(), []deliveryLease{mkGenLease(t, genNonce(0), "sess-A", 1)})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(1), "sess-A", 2)})
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = g.resolveLease(ctx, genNonce(0))
	require.ErrorIs(t, err, context.Canceled)
}

func mkAck(l deliveryLease) deliveryAck {
	return deliveryAck{Version: core.EvidenceVersion, Delivery: l.Delivery, ObservationID: l.ObservationID}
}

// TestDeliveryGeneration_AckFrontierBlocksOnOldestPending pins the bounded per-session ready check: the
// frontier is the oldest still-pending arrival, an out-of-order ack does not move it, and settling the
// gap jumps it past every already-settled arrival. It also survives a restart. The query never walks
// all arrivals — it reads one watermark.
func TestDeliveryGeneration_AckFrontierBlocksOnOldestPending(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)

	leases := make([]deliveryLease, 6) // by arrival 1..5
	for a := uint64(1); a <= 5; a++ {
		leases[a] = mkGenLease(t, genNonce(int(a)), "sess-A", a)
		_, err := g.commit(ctx, []deliveryLease{leases[a]})
		require.NoError(t, err)
	}
	requireFrontier := func(want uint64, wantPending bool) {
		t.Helper()
		f, has, err := g.sessionFrontier(ctx, "sess-A")
		require.NoError(t, err)
		require.Equal(t, wantPending, has)
		if wantPending {
			require.Equal(t, want, f)
		}
	}
	requireFrontier(1, true) // nothing settled

	err = g.commitAck(ctx, []deliveryAck{mkAck(leases[1])})
	require.NoError(t, err)
	requireFrontier(2, true)

	err = g.commitAck(ctx, []deliveryAck{mkAck(leases[3])}) // out of order
	require.NoError(t, err)
	requireFrontier(2, true) // still blocked on the oldest pending, arrival 2

	err = g.commitAck(ctx, []deliveryAck{mkAck(leases[2])})
	require.NoError(t, err)
	requireFrontier(4, true) // jumps past 2 AND the already-settled 3

	err = g.commitAck(ctx, []deliveryAck{mkAck(leases[4]), mkAck(leases[5])})
	require.NoError(t, err)
	requireFrontier(0, false) // every arrival settled

	require.NoError(t, g.close())
	g2, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g2.close() })
	_, has, err := g2.sessionFrontier(ctx, "sess-A")
	require.NoError(t, err)
	require.False(t, has, "a settled frontier survives a restart")
}

// TestDeliveryGeneration_AckMembershipJoinIsExact: commitAck records membership only after an exact
// identity join. An ack for a nonce no lease resolves is unavailable; an ack whose observation id
// disagrees with the lease is a conflict. Neither is recorded, so neither settles the frontier.
func TestDeliveryGeneration_AckMembershipJoinIsExact(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	l := mkGenLease(t, genNonce(0), "sess-A", 1)
	_, err := g.commit(ctx, []deliveryLease{l})
	require.NoError(t, err)

	orphan := deliveryAck{Version: core.EvidenceVersion, Delivery: genNonce(7), ObservationID: l.ObservationID}
	err = g.commitAck(ctx, []deliveryAck{orphan})
	require.ErrorIs(t, err, errGenerationUnavailable, "an ack whose lease does not resolve is unavailable")

	conflict := deliveryAck{Version: core.EvidenceVersion, Delivery: l.Delivery, ObservationID: core.ObservationID("sha256:" + genNonce(9))}
	err = g.commitAck(ctx, []deliveryAck{conflict})
	require.ErrorIs(t, err, errGenerationConflict, "an ack whose identity disagrees with the lease is a conflict")

	f, has, err := g.sessionFrontier(ctx, "sess-A")
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(1), f, "a refused ack settles nothing")
}

// TestDeliveryGeneration_TerminalMembershipSettlesFrontier: a terminal disposition, joined exactly
// against its lease, settles the frontier just as an ack does; a terminal whose lease does not resolve
// is unavailable.
func TestDeliveryGeneration_TerminalMembershipSettlesFrontier(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	l := mkGenLease(t, genNonce(0), "sess-A", 1)
	_, err := g.commit(ctx, []deliveryLease{l})
	require.NoError(t, err)

	f, has, err := g.sessionFrontier(ctx, "sess-A")
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, uint64(1), f)

	err = g.commitTerminal(ctx, []deliveryTerminal{terminalFor(l)})
	require.NoError(t, err)
	_, has, err = g.sessionFrontier(ctx, "sess-A")
	require.NoError(t, err)
	require.False(t, has, "a terminal disposition settles its arrival")

	orphan := terminalFor(mkGenLease(t, genNonce(9), "sess-A", 5)) // arrival 5 never leased
	err = g.commitTerminal(ctx, []deliveryTerminal{orphan})
	require.ErrorIs(t, err, errGenerationUnavailable, "a terminal whose lease does not resolve is unavailable")
}

// TestDeliveryGeneration_ConflictingHeadIsRefused: a head that disagrees with the manifest record it
// names (a tampered root), or names bytes the log does not hold (log_bytes past EOF), is refused —
// never trusted. This is the "a synced tail is evidence, a conflicting head is never trusted" contract.
func TestDeliveryGeneration_ConflictingHeadIsRefused(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(h *genHead)
	}{
		{"tampered root", func(h *genHead) { h.Root = fmt.Sprintf("%064x", 0xbad) }},
		{"log_bytes past eof", func(h *genHead) { h.LogBytes += 100 }},
		{"last_len past boundary", func(h *genHead) { h.LastLen = h.LogBytes + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			g, err := openDeliveryGenerations(dir)
			require.NoError(t, err)
			for i := 0; i < 2; i++ {
				_, err := g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(i), "sess-A", uint64(i+1))})
				require.NoError(t, err)
			}
			head := g.headPath
			require.NoError(t, g.close())

			raw, err := os.ReadFile(paths.Long(head))
			require.NoError(t, err)
			var h genHead
			require.NoError(t, json.Unmarshal(raw, &h))
			tc.mutate(&h)
			b, err := json.Marshal(h)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(head), b, 0o600))

			_, err = openDeliveryGenerations(dir)
			require.ErrorIs(t, err, errGenerationUnavailable, "a head disagreeing with the log must be refused")
		})
	}
}

// TestDeliveryGeneration_FaultPoisonsQueriesFailClosed: once a store latches a fault, every query fails
// closed (I/O poisoning) rather than serving a root a failed commit may have half-moved.
func TestDeliveryGeneration_FaultPoisonsQueriesFailClosed(t *testing.T) {
	g := newTestGenerations(t)
	ctx := context.Background()
	l := mkGenLease(t, genNonce(0), "sess-A", 1)
	_, err := g.commit(ctx, []deliveryLease{l})
	require.NoError(t, err)

	g.mu.Lock()
	g.fault = errGenerationUnavailable
	g.mu.Unlock()

	_, _, err = g.resolveLease(ctx, l.Delivery)
	require.ErrorIs(t, err, errGenerationUnavailable)
	_, _, err = g.lastArrival(ctx, "sess-A")
	require.ErrorIs(t, err, errGenerationUnavailable)
	_, _, err = g.sessionFrontier(ctx, "sess-A")
	require.ErrorIs(t, err, errGenerationUnavailable)
	_, _, err = g.nonceAtArrival(ctx, "sess-A", 1)
	require.ErrorIs(t, err, errGenerationUnavailable)
}
