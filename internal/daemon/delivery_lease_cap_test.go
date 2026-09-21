package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// sp20d4LeaseCap is the entry cap delivery_lease.go shipped with at V5 (deliveryLeaseMaxEntries).
// It is written out rather than read from that constant on purpose: a history sized FROM the
// constant would follow any change to it and stay exactly at the cap, so raising the cap would
// leave this test green while changing what it pins. Sized from the shipped value, a raised cap
// fails the refusal assertion, which is the negative control this test was checked against.
const sp20d4LeaseCap = 1 << 16

// TestCarriedDefect_SP20D4_LeaseJournalRefusesEveryDeliveryPastItsEntryCap retains
// the original capacity identifier. The cap remains a documented availability
// limitation. The corrected behavior refuses publication and retains durable
// input instead of ACKing, dispatching and later replaying an unleased delivery.
// This is a partial mitigation, not a claim of unbounded journal capacity.
func TestCarriedDefect_SP20D4_LeaseJournalRefusesEveryDeliveryPastItsEntryCap(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	const history core.SessionID = "sess-sp20d4-history"
	historyRequest := testDeliveryRequest("sp20d4 completed delivery")
	leaseBytes, ackBytes := sp20d4WriteCompletedHistory(t, root, history, historyRequest,
		sp20d4LeaseCap)
	t.Logf("fixture: %d acknowledged leases; lease journal %d bytes, ack journal %d bytes",
		sp20d4LeaseCap, leaseBytes, ackBytes)

	dd, _, ids := newIdentityRecordingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	// The history is a valid journal at exactly the entry cap: the real open loads and seals it,
	// and the byte bound is nowhere near, so what follows is the entry cap and nothing else.
	journal, err := dd.deliveryJournal()
	require.NoError(t, err, "a journal holding exactly the entry cap is valid and must open")
	sp20d4RequireHistoryOnly(t, root, journal, leaseBytes, ackBytes)
	require.Less(t, leaseBytes, int64(deliveryLeaseMaxBytes),
		"fixture: the entry cap binds, not the byte cap")

	// What the cap does not do: drop an assignment. A late copy of a delivery in the history (a
	// hook client's fallback spool line, drained after the fact) still takes its original lease
	// back and finds itself on the frontier, which is how the drain avoids publishing it twice. A
	// fix that retires acknowledged leases must keep an answer for every copy that can still arrive.
	old, err := journal.lease(ctx, sp20d4Token(1), history, historyRequest)
	require.NoError(t, err)
	require.Equal(t, uint64(1), old.ArrivalSeq)
	require.True(t, journal.acknowledged(old.Delivery))

	// SP20-D4: a fresh delivery, on a session with no history of its own, is refused. Nothing is in
	// flight and every lease is acknowledged; the refusal is permanent because nothing retires one.
	_, err = journal.lease(ctx, testDeliveryToken('e'), "sess-sp20d4", testDeliveryRequest("fresh"))
	require.ErrorIs(t, err, core.ErrBudget,
		"SP20-D4: once 65,536 deliveries are leased, every later delivery is refused a lease")

	// Exhaustion is an explicit pending delivery, never an unleased substitute.
	req := observeRequest(testDeliveryToken('d'), "sess-sp20d4", `{"hook_event_name":"PostToolUse"}`)
	resp := dd.dispatchOp(ctx, req)
	require.False(t, resp.OK, "the client must retain its fallback when identity cannot be assigned")
	require.NotEmpty(t, resp.Err)
	require.Equal(t, int64(1), dd.m.Counter(counterDeliveryUnleased).Value())
	require.Empty(t, dd.ing.ring, "no unleased observation may run")
	require.Empty(t, ids())
	require.Empty(t, sidecarFiles(t, root))
	require.NoError(t, dd.ing.Close())
	dd.ing.seen = newSeenSet(seenCapacity)
	dd.drain.Load().cfg.Seen = dd.ing.seen
	n, err := dd.Drain(ctx)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Zero(t, n, "a retry without identity must remain pending")
	require.Empty(t, ids(), "restart cannot replay an unleased substitute")
	require.False(t, dd.DrainGaps().Complete)
	require.Contains(t, gapKinds(dd.DrainGaps()), DrainGapUnleased)
	entries, err := os.ReadDir(paths.Of(root).Spool)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the durable source remains available for recovery")
	sp20d4RequireHistoryOnly(t, root, journal, leaseBytes, ackBytes)
}

// sp20d4WriteCompletedHistory leaves root's state directory exactly as n completed deliveries
// would: n lease lines for session (arrivals 1..n, one delivery label each), an acknowledgement
// for every one of them, and the sealed position sidecar each journal is recovered against. Every
// byte is what lease and acknowledge themselves append (the same json.Marshal line, the same
// deliveryChain fold from the same seed, the same deliveryPosition seal), so openDeliveryJournal
// takes it through its full validation: canonical lines, dense arrivals, chain, count, byte seal,
// and every acknowledgement naming a surviving lease. It returns the two journals' sizes.
func sp20d4WriteCompletedHistory(
	t *testing.T, root string, session core.SessionID, request core.Hash, n int,
) (leaseBytes, ackBytes int64) {
	t.Helper()
	state := paths.Of(root).State
	require.NoError(t, os.MkdirAll(paths.Long(state), 0o700))
	leases := sp20d4CreateJournal(t, filepath.Join(state, deliveryLeaseFile), deliveryChainSeed)
	acks := sp20d4CreateJournal(t, filepath.Join(state, deliveryAckFile), deliveryAckChainSeed)
	for i := 1; i <= n; i++ {
		arrival := uint64(i)
		delivery := sp20d4Token(i)
		id, err := core.NewObservationID(session, arrival)
		if err != nil {
			t.Fatalf("fixture: observation id for arrival %d: %v", arrival, err)
		}
		leases.write(t, deliveryLease{
			Version: core.EvidenceVersion, Delivery: delivery, Session: session,
			RequestHash: request, ArrivalSeq: arrival, ObservationID: id,
		})
		acks.write(t, deliveryAck{Version: core.EvidenceVersion, Delivery: delivery, ObservationID: id})
	}
	leases.seal(t, filepath.Join(state, deliveryPositionFile), n)
	acks.seal(t, filepath.Join(state, deliveryAckPositionFile), n)
	return leases.size, acks.size
}

// sp20d4Token is the i-th fixture delivery label: 64 lowercase hex characters, never all zeros for
// i >= 1, and never equal to a testDeliveryToken, so no fixture label collides with a fresh one.
func sp20d4Token(i int) string { return fmt.Sprintf("%064x", i) }

// sp20d4Journal writes one fixture journal line by line, folding each line into the chain the
// loader recomputes, so the seal written at the end is the one the same appends would have left.
// It streams to disk rather than buffering: the history is tens of megabytes, and the race
// detector's shadow memory would multiply a buffer that size.
type sp20d4Journal struct {
	file  *os.File
	w     *bufio.Writer
	size  int64
	chain core.Hash
}

func sp20d4CreateJournal(t *testing.T, path string, seed core.Hash) *sp20d4Journal {
	t.Helper()
	f, err := os.OpenFile(paths.Long(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() }) // a fixture failure must not hold the TempDir open
	return &sp20d4Journal{file: f, w: bufio.NewWriter(f), chain: seed}
}

// write appends record as one canonical line: the json.Marshal encoding lease and acknowledge
// append, terminated the same way.
func (j *sp20d4Journal) write(t *testing.T, record any) {
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("fixture: encoding %T: %v", record, err)
	}
	line = append(line, '\n')
	if _, err := j.w.Write(line); err != nil {
		t.Fatalf("fixture: writing %T: %v", record, err)
	}
	j.size += int64(len(line))
	j.chain = deliveryChain(j.chain, line)
}

// seal flushes the journal and writes the position sidecar the loader validates it against.
func (j *sp20d4Journal) seal(t *testing.T, position string, count int) {
	t.Helper()
	require.NoError(t, j.w.Flush())
	require.NoError(t, j.file.Close())
	encoded, err := json.Marshal(deliveryPosition{
		Version: core.EvidenceVersion, Bytes: j.size, Count: count, Chain: j.chain,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(position), encoded, 0o600))
}

// sp20d4RequireHistoryOnly asserts both journals still hold exactly the fixture history, in memory
// and on disk: no assignment and no frontier record was added for any delivery past the cap.
// Sizes and counts are compared rather than contents, so a failure prints two numbers and not a
// map or a file of tens of megabytes.
func sp20d4RequireHistoryOnly(
	t *testing.T, root string, journal *deliveryJournal, leaseBytes, ackBytes int64,
) {
	t.Helper()
	require.Equal(t, sp20d4LeaseCap, len(journal.leases),
		"the lease journal holds the history and nothing else")
	require.Equal(t, sp20d4LeaseCap, len(journal.acks),
		"the ack journal holds the history and nothing else")
	state := paths.Of(root).State
	for _, f := range []struct {
		name string
		size int64
	}{{deliveryLeaseFile, leaseBytes}, {deliveryAckFile, ackBytes}} {
		info, err := os.Stat(paths.Long(filepath.Join(state, f.name)))
		require.NoError(t, err)
		require.Equal(t, f.size, info.Size(), "%s grew past the fixture history", f.name)
	}
}
