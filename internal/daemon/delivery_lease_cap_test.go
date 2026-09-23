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
// leave this test green while changing what it pins. Sized from the shipped value, the history is
// always exactly where the V5 journal began refusing every delivery.
const sp20d4LeaseCap = 1 << 16

// TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart is SP20-D4's evidence
// test, inverted at the V6 close-out (C1.10) when segmented rollover was enabled by default.
//
// Criterion change: it was TestCarriedDefect_SP20D4_LeaseJournalRefusesEveryDeliveryPastItsEntryCap,
// which pinned the defect — once 65,536 deliveries were leased every later delivery was refused, the
// hook kept its fallback and the drain recorded an unleased gap for good. It now pins the fix, on the
// same fixture (a store at exactly the shipped cap, every delivery acknowledged, written byte for
// byte as lease and acknowledge write it):
//
//   - a fresh delivery past the cap is admitted with a real identity, dispatched and acknowledged, and
//     the journal rotates to segment 1 instead of refusing; the history's journals are not rewritten;
//   - across a restart the rotated store opens on segment 1, new deliveries continue densely (for a
//     new session and for the history's own session, at 65,537), and the oldest and newest history
//     deliveries still resolve to their ORIGINAL identities and acknowledgements;
//   - a late copy of an archived, acknowledged history delivery (a hook's fallback spool line drained
//     after the rotation and the restart) is recognised and not published a second time.
//
// The acceptance in plans/V2-WAVE1-carried-defects.md §SP20-D4 asked for exactly these: a project
// past 65,536 leases leases its next delivery, and a late copy of a retired delivery is still skipped.
func TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart(t *testing.T) {
	require.True(t, enableDeliveryGenerations, "segmented rollover is enabled by default")
	ctx := context.Background()
	root := t.TempDir()
	const history core.SessionID = "sess-sp20d4-history"
	// The history's request binding is the one a real late copy of its first delivery carries, so that
	// copy can be drained below and matched against its original lease.
	lateCopy := observeRequest(sp20d4Token(1), string(history), `{"hook_event_name":"PostToolUse"}`)
	historyRequest := deliveryRequestHash(lateCopy)
	leaseBytes, ackBytes := sp20d4WriteCompletedHistory(t, root, history, historyRequest,
		sp20d4LeaseCap)
	t.Logf("fixture: %d acknowledged leases; lease journal %d bytes, ack journal %d bytes",
		sp20d4LeaseCap, leaseBytes, ackBytes)

	dd, _, ids := newIdentityRecordingDaemon(t, root)
	lock := lockFor(t, dd, root)
	journal, err := dd.deliveryJournal()
	require.NoError(t, err, "a journal holding exactly the entry cap is valid and must open")
	sp20d4RequireHistoryOnly(t, root, journal, leaseBytes, ackBytes)
	require.Less(t, leaseBytes, int64(deliveryLeaseMaxBytes), "fixture: the entry cap binds, not the byte cap")
	require.Equal(t, uint64(0), journal.segment)

	// Past the cap: the delivery is leased, dispatched and acknowledged, and the journal rotated.
	req := observeRequest(testDeliveryToken('d'), "sess-sp20d4", `{"hook_event_name":"PostToolUse"}`)
	resp := dd.dispatchOp(ctx, req)
	require.True(t, resp.OK, "a delivery past the old cap is admitted: %s", resp.Err)
	require.Zero(t, dd.m.Counter(counterDeliveryUnleased).Value(), "no delivery goes unleased")
	drainRing(t, dd)
	first, err := core.NewObservationID("sess-sp20d4", 1)
	require.NoError(t, err)
	require.Equal(t, []core.ObservationID{first}, ids(), "the observer ran under the delivery's own identity")
	require.True(t, journal.acknowledged(req.Nonce), "the delivery reached the committed frontier")
	require.Equal(t, uint64(1), journal.segment, "the journal rotated at the cap instead of refusing")
	sp20d4RequireLegacyUntouched(t, root, leaseBytes, ackBytes)

	old, err := journal.lease(ctx, sp20d4Token(1), history, historyRequest)
	require.NoError(t, err)
	require.Equal(t, uint64(1), old.ArrivalSeq, "an archived delivery keeps its original identity")
	require.True(t, journal.acknowledged(old.Delivery), "and its acknowledgement")
	require.NoError(t, lock.Release())

	// Restart: a new daemon on the rotated store.
	dd2, _, ids2 := newIdentityRecordingDaemon(t, root)
	lock2 := lockFor(t, dd2, root)
	defer func() { _ = lock2.Release() }()
	journal2, err := dd2.deliveryJournal()
	require.NoError(t, err, "the rotated store opens after a restart")
	require.Equal(t, uint64(1), journal2.segment)

	req2 := observeRequest(testDeliveryToken('e'), "sess-sp20d4", `{"hook_event_name":"PostToolUse"}`)
	resp = dd2.dispatchOp(ctx, req2)
	require.True(t, resp.OK, resp.Err)
	drainRing(t, dd2)
	second, err := core.NewObservationID("sess-sp20d4", 2)
	require.NoError(t, err)
	require.Equal(t, []core.ObservationID{second}, ids2(), "arrivals continue densely across the restart")
	require.True(t, journal2.acknowledged(req2.Nonce))

	cont, err := journal2.lease(ctx, testDeliveryToken('f'), history, testDeliveryRequest("after the cap"))
	require.NoError(t, err)
	require.Equal(t, uint64(sp20d4LeaseCap+1), cont.ArrivalSeq, "the history's session continues past the cap")
	for _, i := range []int{1, sp20d4LeaseCap} {
		got, err := journal2.lease(ctx, sp20d4Token(i), history, historyRequest)
		require.NoError(t, err)
		require.Equal(t, uint64(i), got.ArrivalSeq, "history delivery %d keeps its original identity", i)
		require.True(t, journal2.acknowledged(got.Delivery), "history delivery %d stays acknowledged", i)
	}

	// A late copy of an archived, acknowledged delivery: recognised, never published again.
	require.NoError(t, dd2.ing.Close())
	lateCopy.Capture = nil // an inherited record, exactly as a hook's fallback line arrives
	writeSpoolLine(t, root, "client-00001.ndjson", lateCopy)
	_, err = dd2.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.ObservationID{second}, ids2(), "a late copy of a retired delivery is skipped")
	require.True(t, dd2.DrainGaps().Complete, "the late copy is accounted for: %+v", dd2.DrainGaps().Gaps)
	sp20d4RequireLegacyUntouched(t, root, leaseBytes, ackBytes)
}

// sp20d4RequireLegacyUntouched asserts the history's two legacy journals still hold exactly the fixture
// bytes after the rotation archived them: a rotation never rewrites, truncates or extends segment 0.
func sp20d4RequireLegacyUntouched(t *testing.T, root string, leaseBytes, ackBytes int64) {
	t.Helper()
	state := paths.Of(root).State
	for _, f := range []struct {
		name string
		size int64
	}{{deliveryLeaseFile, leaseBytes}, {deliveryAckFile, ackBytes}} {
		info, err := os.Stat(paths.Long(filepath.Join(state, f.name)))
		require.NoError(t, err)
		require.Equal(t, f.size, info.Size(), "%s is archived as it was, never rewritten", f.name)
	}
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
