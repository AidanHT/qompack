package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// toolTestProject builds a project whose two seals are v2 and whose daemon is gone, holding n
// leases. It returns the project root.
//
// v2 on disk with no daemon running is what a CRASH leaves: a clean Release downgrades both seals
// to v1 (closeSeals), and a journal carrying a fault is the state in which it does not. Poisoning
// the handle reproduces exactly that without killing a process, and it is the state the offline
// tool exists for — design §4.4's "step 2 to pre-step-1, after a crash or a failed Release".
//
// n is at most six: testDeliveryToken repeats one rune, and a delivery token must be lowercase hex.
func toolTestProject(t *testing.T, n int) string {
	t.Helper()

	root := t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = 2
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	for i := range n {
		delivery := testDeliveryToken(rune('a' + i))
		_, err := j.lease(context.Background(), delivery, "s1", testDeliveryRequest(delivery))
		require.NoError(t, err)
	}
	_ = j.poison(deliveryJournalError())
	require.NoError(t, lock.Release())
	return root
}

// toolTestState is every file the tool could touch, as bytes. A refusal must leave all four equal.
func toolTestState(t *testing.T, root string) map[string][]byte {
	t.Helper()

	state := paths.Of(root).State
	files := map[string][]byte{}
	for _, name := range []string{
		deliveryLeaseFile, deliveryPositionFile, deliveryAckFile, deliveryAckPositionFile,
	} {
		b, err := os.ReadFile(filepath.Join(state, name))
		require.NoError(t, err)
		files[name] = b
	}
	return files
}

// toolTestSeals describes both seal files: what a refusal was looking at.
func toolTestSeals(t *testing.T, root string) string {
	t.Helper()

	const head = 48
	state := paths.Of(root).State
	var b bytes.Buffer
	for _, name := range []string{deliveryPositionFile, deliveryAckPositionFile} {
		raw, err := os.ReadFile(filepath.Join(state, name))
		require.NoError(t, err)
		if len(raw) > head {
			raw = raw[:head]
		}
		fmt.Fprintf(&b, "\n  %s: %q", name, raw)
	}
	return b.String()
}

// toolTestRun runs the tool against root and returns its report.
func toolTestRun(t *testing.T, root string, o DeliverySealOptions) (string, error) {
	t.Helper()

	var out bytes.Buffer
	o.ProjectRoot, o.Out, o.Clock = root, &out, newFakeClock(epoch)
	// The call first, then the report: a single return statement would evaluate out.String() before
	// RepairDeliverySeal ran and hand every assertion an empty string.
	err := RepairDeliverySeal(o)
	t.Logf("report:\n%s", out.String())
	return out.String(), err
}

// tearNewestSlot makes the seal's effective slot unreadable and leaves the older one valid: the
// valid-plus-invalid image the strict reader refuses (design §2.9) and the only one Rule R accepts.
func tearNewestSlot(t *testing.T, root string) {
	t.Helper()

	p := filepath.Join(paths.Of(root).State, deliveryPositionFile)
	image, err := os.ReadFile(p)
	require.NoError(t, err)
	require.True(t, isDeliverySealImage(image), "the crash should have left a v2 image")

	effective, _, err := selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.NoError(t, err)
	// One byte inside the record's first key: still a v2 image, still padded to the region's end,
	// no longer the canonical JSON of a record, so the slot is invalid rather than empty.
	const inFirstKey = 3
	image[slotFor(effective.Seq).offset()+inFirstKey] = 'X'
	require.NoError(t, os.WriteFile(p, image, 0o600))

	_, _, err = selectSeal(image, deliveryChainDomain, deliveryChainSeed)
	require.Error(t, err, "the strict reader must refuse a torn slot; otherwise this fixture tests nothing")
}

// requireOpensWithFormat asserts that a daemon writing seal format wantFormat opens root and
// recovers wantLeases identities: the question the whole repair is asked on behalf of.
func requireOpensWithFormat(t *testing.T, root string, wantFormat, wantLeases int) {
	t.Helper()

	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = wantFormat
	t.Cleanup(func() { _ = lock.Release() })

	j, err := lock.openDeliveryJournal()
	require.NoError(t, err, "seals: %s", toolTestSeals(t, root))
	j.st.Lock()
	defer j.st.Unlock()
	require.Len(t, j.leases, wantLeases)
}

// TestDeliverySealRuleR_AcceptsOnlyOneValidSlotBesideOneTornSlot pins Rule R's narrowness, which is
// the whole of what makes an operator flag able to accept a position the daemon's own reader
// refuses (design §2.9, §2.12 X8, §4.5).
//
// The load the tool runs afterwards proves that the accepted record seals a real prefix of the
// journal and that every line past it is canonical. It can never prove that the SLOT PAIR was
// crash-reachable, and that is the whole of what this function decides. Widened by one clause, Rule
// R would accept valid(seq 5) beside an empty slot — the stale restore or foreign write selectSeal
// singles out as the format's one rollback window — and the load would still pass.
//
// A slot whose record has the wrong parity, a sum for the other slot, or a sum for the other
// journal is INVALID rather than valid, so those images are Rule R's to accept, with consent: they
// are the foreign writes and the media damage it exists for.
func TestDeliverySealRuleR_AcceptsOnlyOneValidSlotBesideOneTornSlot(t *testing.T) {
	j := sealTestLease
	r := sealTestRecordPtr
	fresh := j.fresh()
	sumA := func(rec *sealRecord) sealRecord { return j.summed(*rec, sealSlotA) }
	sumB := func(rec *sealRecord) sealRecord { return j.summed(*rec, sealSlotB) }
	torn := make([]byte, deliverySealSlotRegion) // a slot of zero bytes, as a hole reads
	steady := j.image(t, r(3), r(2))
	pair := j.image(t, r(1), r(2))
	// position builds two records whose seqs are adjacent but whose positions do not grow, which is
	// the second way the strict reader refuses an image with two VALID slots.
	position := func(seq uint64, size int64, count int) *sealRecord {
		return &sealRecord{Seq: seq, Bytes: size, Count: count, Chain: sealTestChain("position")}
	}

	t.Run("accepts the valid record beside a torn slot", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			img  []byte
			want sealRecord
		}{
			{"the newest slot is torn", withRegion(steady, sealSlotA, torn), sumB(r(2))},
			{"the older slot is torn", withRegion(steady, sealSlotB, torn), sumA(r(3))},
			{"a fresh file whose empty slot was overwritten", withRegion(j.image(t, &fresh, nil), sealSlotB, torn), sumA(&fresh)},
			{
				"a slot summed for the other slot: a foreign write",
				withRegion(pair, sealSlotB, sealRawRegion(t, *r(2), sealSlotA, j.domain)),
				sumA(r(1)),
			},
			{
				"a slot whose seq has the other slot's parity",
				withRegion(pair, sealSlotB, sealRawRegion(t, *r(3), sealSlotB, j.domain)),
				sumA(r(1)),
			},
			{
				"a slot summed for the other journal",
				withRegion(pair, sealSlotB, sealRawRegion(t, *r(2), sealSlotB, sealTestAck.domain)),
				sumA(r(1)),
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				j.requireRefused(t, tc.img, "the fixture must be an image the STRICT reader refuses")

				got, err := sealRuleR(tc.img, j.domain, j.seed)
				require.NoError(t, err)
				require.Equal(t, tc.want, got, "Rule R takes the valid record and no other")
			})
		}
	})

	t.Run("refuses every image that is not one valid slot beside one torn slot", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			img  []byte
			// strict is true for the two images the STRICT reader accepts: they never reach Rule R,
			// and the guard below asks selectSeal for the opposite answer.
			strict bool
		}{
			// The two the strict reader accepts. They never reach Rule R, and they are refused here
			// too: this function answers for itself, not for its caller's control flow.
			{"a fresh seq 1 beside an empty slot", j.image(t, &fresh, nil), true},
			{"two valid slots one seq apart", pair, true},
			// An empty slot is not damage, and a seq other than 1 beside one is the format's single
			// rollback window: a stale restore or a foreign write, never a torn write.
			{"a valid seq 3 beside an empty slot", j.image(t, r(3), nil), false},
			{"an empty slot beside a valid seq 2", j.image(t, nil, r(2)), false},
			{"a valid seq 1001 beside an empty slot", j.image(t, r(1001), nil), false},
			{"both slots empty", sealImageTemplate(), false},
			// Two valid slots the strict reader refuses. Both records survive, so neither is torn.
			{"two valid slots with a seq gap", j.image(t, r(1), r(4)), false},
			{"two valid slots whose older seals as many bytes", j.image(t, position(1, 500, 5), position(2, 500, 6)), false},
			{"two valid slots whose older seals as many entries", j.image(t, position(1, 500, 5), position(2, 600, 5)), false},
			{"two valid slots whose older seals more", j.image(t, position(3, 500, 5), position(2, 600, 6)), false},
			// No valid record to take at all.
			{"both slots torn", withRegion(withRegion(steady, sealSlotA, torn), sealSlotB, torn), false},
			{"a torn slot beside an empty one", withRegion(sealImageTemplate(), sealSlotA, torn), false},
			{"both slots belong to the other journal", sealTestAck.image(t, r(1), r(2)), false},
			// Not a v2 image at all, which is the v1 reader's business and never Rule R's.
			{"a v2 image one byte short", steady[:deliverySealFileSize-1], false},
			{"a static byte changed", withByte(steady, 0, 'x'), false},
			{"an empty file", nil, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.strict {
					_, _, err := selectSeal(tc.img, j.domain, j.seed)
					require.NoError(t, err, "guard: this is one of the two images the strict reader ACCEPTS")
				} else {
					j.requireRefused(t, tc.img, "guard: the strict reader must refuse it too")
				}

				got, err := sealRuleR(tc.img, j.domain, j.seed)
				require.ErrorIs(t, err, core.ErrDegraded,
					"accepting this would accept a position no crash can produce")
				require.Zero(t, got)
			})
		}
	})
}

// TestDeliveryOfflineTool_SyncsTheJournalItScannedBeforeSealingIt pins the step --to v1 takes
// between the scan and the seal.
//
// The position it seals is the RECOVERED one, which a complete canonical tail past the old seal
// moves forward — and such a tail is exactly what a crash between the journal's Write and its Sync
// leaves: visible to every reader, durable to none (design §3, row 5). writeDeliveryPositionV1 goes
// through paths.WriteAtomic, which makes the seal durable by construction, so a seal written over
// an unsynced tail can outlive it, and loadFrom then refuses the journal for good.
//
// A successful fsync leaves no trace a test in this process can see, so what is asserted here is
// the guard around it, which is the part that can be wrong: the tool syncs the file its own scan
// read, at the size that scan ended at, and refuses anything else rather than sealing a tail
// nothing scanned. It writes nothing through the handle it opens.
func TestDeliveryOfflineTool_SyncsTheJournalItScannedBeforeSealingIt(t *testing.T) {
	root := toolTestProject(t, 2)
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	var out bytes.Buffer
	o := DeliverySealOptions{ProjectRoot: root, ToV1: true, Out: &out, Clock: newFakeClock(epoch)}
	state := paths.Of(root).State
	j := newDeliveryJournal(lock, filepath.Join(state, deliveryLeaseFile))
	j.ackPath = filepath.Join(state, deliveryAckFile)
	sides, err := deliverySealSides(j)
	require.NoError(t, err)
	require.Len(t, sides, 2, "the fixture has both pairs")
	for i := range sides {
		require.NoError(t, o.inspect(&sides[i]))
	}
	before := toolTestState(t, root)

	// The ordinary case: both journals the scan read are made durable, and nothing is written.
	for i := range sides {
		require.NoError(t, o.syncJournal(&sides[i]))
	}
	require.Equal(t, before, toolTestState(t, root), "the sync writes no bytes of its own")

	// A different file at the same size is refused: the recovered position describes the file the
	// scan read, and a journal replaced since then is not that file.
	lease := &sides[0]
	scanned := lease.info
	twin := filepath.Join(t.TempDir(), deliveryLeaseFile)
	require.NoError(t, os.WriteFile(twin, readTestFile(t, lease.journal), 0o600))
	twinInfo, err := os.Lstat(twin)
	require.NoError(t, err)
	lease.info = twinInfo
	require.ErrorIs(t, o.syncJournal(lease), core.ErrDegraded)
	lease.info = scanned

	// A journal that has grown since the scan is refused too: its tail reaches past the position
	// this seal would name, which is a journal nobody has loaded.
	f, err := paths.OpenFile(lease.journal, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte("\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.ErrorIs(t, o.syncJournal(lease), core.ErrDegraded)
}

// TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock is design §6.2's T31.
//
// It pins the four properties §4.5 gives the tool: it refuses while a daemon holds the lock, it
// converts both seals to v1 through the full reader and the full load, it converts only AFTER that
// load succeeds, and Rule R — the one place a position the daemon's reader refuses may be accepted
// — needs an explicit confirmation and says exactly which lines it admitted.
func TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock(t *testing.T) {
	t.Run("refuses while a daemon holds the lock", func(t *testing.T) {
		root := toolTestProject(t, 1)
		before := toolTestState(t, root)

		lock, err := acquireTestDeliveryLock(root)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		for _, o := range []DeliverySealOptions{{Check: true}, {ToV1: true}} {
			_, err := toolTestRun(t, root, o)
			require.ErrorIs(t, err, ErrLockHeld,
				"a repair that raced a live daemon would be the corruption it exists to undo")
		}
		require.Equal(t, before, toolTestState(t, root))
	})

	t.Run("refuses once a daemon has taken the lock over mid-run", func(t *testing.T) {
		root := toolTestProject(t, 2)

		// The tool's own acquisition, exactly as RepairDeliverySeal makes it. The run is then driven
		// through run() rather than through RepairDeliverySeal, because the state under test is a
		// daemon that starts AFTER the lock was taken: the front door has already been passed, and
		// on Windows the staleness protocol has only daemon.hb's mtime to judge this run by.
		lock, err := acquireTestDeliveryLock(root)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		before := toolTestState(t, root)
		replaceTestLock(t, root)

		for _, o := range []DeliverySealOptions{{Check: true}, {ToV1: true}} {
			var out bytes.Buffer
			o.ProjectRoot, o.Out, o.Clock = root, &out, newFakeClock(epoch)
			err := o.run(lock)
			t.Logf("report:\n%s", out.String())
			require.ErrorContains(t, err, "no longer owns the daemon lock",
				"a dispossessed repair must not write over the project a daemon now serves")
			require.NotContains(t, out.String(), "wrote v1")
		}
		require.Equal(t, before, toolTestState(t, root))
	})

	t.Run("converts both seals to v1 after a check that writes nothing", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State
		leaseSeal := filepath.Join(state, deliveryPositionFile)
		ackSeal := filepath.Join(state, deliveryAckPositionFile)
		for _, p := range []string{leaseSeal, ackSeal} {
			info, err := os.Lstat(p)
			require.NoError(t, err)
			require.Equal(t, int64(deliverySealFileSize), info.Size(), "%s should be a v2 image", p)
		}

		before := toolTestState(t, root)
		report, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.NoError(t, err)
		require.Contains(t, report, "v2")
		require.Contains(t, report, "nothing was written")
		require.Equal(t, before, toolTestState(t, root), "--check must write nothing")

		report, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, err)
		require.Contains(t, report, "wrote v1")

		leasePosition, err := loadDeliveryPosition(leaseSeal, deliveryChainSeed)
		require.NoError(t, err, "lease seal: %s", toolTestSeals(t, root))
		require.Equal(t, 2, leasePosition.Count)
		ackPosition, err := loadDeliveryPosition(ackSeal, deliveryAckChainSeed)
		require.NoError(t, err, "ack seal: %s", toolTestSeals(t, root))
		require.Equal(t, 0, ackPosition.Count)

		after := toolTestState(t, root)
		require.Equal(t, before[deliveryLeaseFile], after[deliveryLeaseFile], "the journals are evidence")
		require.Equal(t, before[deliveryAckFile], after[deliveryAckFile], "the journals are evidence")

		// The point of the conversion: a build that writes v1 — the one an operator rolled back to
		// — now opens the project and recovers every identity.
		requireOpensWithFormat(t, root, 1, 2)
	})

	t.Run("refuses to convert a journal that does not load", func(t *testing.T) {
		root := toolTestProject(t, 2)
		journal := filepath.Join(paths.Of(root).State, deliveryLeaseFile)
		info, err := os.Lstat(journal)
		require.NoError(t, err)
		require.NoError(t, os.Truncate(journal, info.Size()-1))
		before := toolTestState(t, root)

		_, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.Error(t, err)
		require.Equal(t, before, toolTestState(t, root),
			"--to v1 converts only after a successful full load")
	})

	t.Run("refuses a half-present pair", func(t *testing.T) {
		root := toolTestProject(t, 1)
		require.NoError(t, os.Remove(filepath.Join(paths.Of(root).State, deliveryAckFile)))

		_, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.Error(t, err)
	})

	t.Run("reports a project with no delivery journal", func(t *testing.T) {
		root := t.TempDir()

		report, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.NoError(t, err)
		require.Contains(t, report, "no delivery journal")

		_, err = os.Lstat(filepath.Join(paths.Of(root).State, deliveryLeaseFile))
		require.True(t, os.IsNotExist(err), "a check must not bring a journal into existence")
	})

	t.Run("a torn slot is refused without consent", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)
		before := toolTestState(t, root)

		// The strict reader refuses it, exactly as the daemon's own open would.
		_, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.Error(t, err)

		// Rule R without its confirmation is refused before the project is touched at all.
		report, err := toolTestRun(t, root, DeliverySealOptions{ToV1: true, AcceptTornSlot: true})
		require.Error(t, err)
		require.Empty(t, report)

		require.Equal(t, before, toolTestState(t, root))
	})

	t.Run("a confirmed torn slot accepts the complete tail and names it", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)

		report, err := toolTestRun(t, root, DeliverySealOptions{
			ToV1: true, AcceptTornSlot: true, Confirm: true,
		})
		require.NoError(t, err)

		// Exactly which lines it accepted: the second lease's, which the surviving slot does not
		// seal. The first lease's line is sealed by that slot, so it is not part of the acceptance.
		require.Contains(t, report, "admitted 1 line")
		require.Contains(t, report, testDeliveryToken('b'))
		require.NotContains(t, report, testDeliveryToken('a'))

		position, err := loadDeliveryPosition(
			filepath.Join(paths.Of(root).State, deliveryPositionFile), deliveryChainSeed)
		require.NoError(t, err)
		require.Equal(t, 2, position.Count, "the repair seals the tail it accepted")
		requireOpensWithFormat(t, root, 1, 2)
	})
}
