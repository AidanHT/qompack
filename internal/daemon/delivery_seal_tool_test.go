package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// toolTestProject builds a project whose two seals are v2 and whose daemon is gone, holding n
// leases and one acknowledgement of the first of them. It returns the project root.
//
// v2 on disk with no daemon running is what a CRASH leaves: a clean Release downgrades both seals
// to v1 (closeSeals), and a journal carrying a fault is the state in which it does not. Poisoning
// the handle reproduces exactly that without killing a process, and it is the state the offline
// tool exists for — design §4.4's "step 2 to pre-step-1, after a crash or a failed Release".
//
// The acknowledgement is what makes the ack side a journal rather than an empty file, and both
// halves of that matter. A 0-byte acknowledgement journal loads whatever its seal says, so with one
// the ack SCAN can fail — which is what "--to v1 converts only after a successful full load" is a
// claim about, the load being both journals — and loadAcksFrom's "every acknowledgement names a
// surviving lease" check stops being vacuous.
//
// n is at least one, and at most six: testDeliveryToken repeats one rune, and a delivery token must
// be lowercase hex.
func toolTestProject(t *testing.T, n int) string {
	t.Helper()
	require.Positive(t, n, "the fixture acknowledges the first lease, so there must be one")

	root := t.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = 2
	j, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	var first deliveryLease
	for i := range n {
		delivery := testDeliveryToken(rune('a' + i))
		leased, err := j.lease(context.Background(), delivery, "s1", testDeliveryRequest(delivery))
		require.NoError(t, err)
		if i == 0 {
			first = leased
		}
	}
	require.NoError(t, j.acknowledge(context.Background(), first.Delivery, first.ObservationID, core.Hash{}))
	_ = j.poison(deliveryJournalError())
	require.NoError(t, lock.Release())
	return root
}

// toolTestWriter is the tool's report writer with a hook: do runs once, on the first line that
// contains when.
//
// It is how a test acts at a moment INSIDE the conversion loop, which the tool offers no seam of
// its own for. The report line a conversion prints after its WriteAtomic is the one point between
// the two seals' writes, and "a daemon takes the project over there" is the state that leaves the
// pair half converted.
type toolTestWriter struct {
	buf  bytes.Buffer
	when string
	do   func()
}

func (w *toolTestWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.do != nil && bytes.Contains(p, []byte(w.when)) {
		do := w.do
		w.do = nil
		do()
	}
	return n, err
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
// A successful fsync leaves no trace a test in this process can see, so it is pinned from two
// sides. The guard around it is asserted directly, which is the part that can be wrong: the tool
// syncs the file its own scan read, at the size that scan ended at, and refuses anything else
// rather than sealing a tail nothing scanned. It writes nothing through the handle it opens. That
// the RUN takes the step at all, and takes it before the first seal is written, is asserted by
// making the step fail through the syncData seam and finding that nothing was sealed.
func TestDeliveryOfflineTool_SyncsTheJournalItScannedBeforeSealingIt(t *testing.T) {
	t.Run("syncs the file its scan read and refuses any other", func(t *testing.T) {
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
	})

	// The step the run takes, rather than the step itself: a --to v1 run that cannot make one of the
	// journals durable writes no seal at all, for either side. The failure is injected through the
	// same kind of seam deliverySeal.syncData is, because the alternative — a journal the process
	// cannot open for writing — is a different thing on every platform, and one the scan would have
	// refused first anyway.
	//
	// A seal made durable over a tail that is not is the most expensive failure this stage has:
	// paths.WriteAtomic makes the seal durable by construction, so a power loss then leaves
	// info.Size() < position.Bytes, which loadFrom refuses permanently — to the daemon and to a
	// second run of this tool alike.
	t.Run("seals nothing when a journal cannot be made durable", func(t *testing.T) {
		for _, journal := range []string{deliveryLeaseFile, deliveryAckFile} {
			t.Run(journal, func(t *testing.T) {
				root := toolTestProject(t, 2)
				before := toolTestState(t, root)

				failed := errors.New("the platter never saw it")
				calls := 0
				report, err := toolTestRun(t, root, DeliverySealOptions{
					ToV1: true,
					syncData: func(f *os.File) error {
						if filepath.Base(f.Name()) != journal {
							return f.Sync()
						}
						calls++
						return failed
					},
				})

				require.ErrorIs(t, err, failed)
				require.ErrorContains(t, err, "durable before its seal names its tail")
				require.Equal(t, 1, calls, "the run takes the step for the journal it is about to seal")
				require.NotContains(t, report, "wrote v1")
				require.Equal(t, before, toolTestState(t, root),
					"no seal may name a tail this run could not make durable")
				info, err := os.Lstat(filepath.Join(paths.Of(root).State, deliveryPositionFile))
				require.NoError(t, err)
				require.Equal(t, int64(deliverySealFileSize), info.Size(),
					"the lease seal is still the v2 image the fixture left")
			})
		}
	})
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

	t.Run("refuses to convert once a daemon has taken the lock after the scan", func(t *testing.T) {
		root := toolTestProject(t, 2)
		before := toolTestState(t, root)

		// The dispossession lands after the check that follows the scan and before the first write,
		// which is the window no other subtest can reach: the earlier check has already passed, so
		// the one the conversion loop takes immediately before each write is the only thing left that
		// can refuse the run. The journal syncs are the last step before that loop, so the seam that
		// fails them is where a daemon "starts" here — the ack journal's sync being the second and
		// last of the two.
		report, err := toolTestRun(t, root, DeliverySealOptions{
			ToV1: true,
			syncData: func(f *os.File) error {
				if filepath.Base(f.Name()) == deliveryAckFile {
					replaceTestLock(t, root)
				}
				return f.Sync()
			},
		})

		require.ErrorContains(t, err, "before converting the lease seal",
			"the check immediately before the first write is what must refuse this run")
		require.ErrorContains(t, err, "no longer owns the daemon lock")
		require.NotContains(t, err.Error(), "already v1", "nothing was converted, so nothing is half done")
		require.NotContains(t, report, "wrote v1")
		require.Equal(t, before, toolTestState(t, root))
	})

	// The other half of holdsLock's refusal, which used to be worded as the first. Heartbeat fails
	// for two unrelated reasons, and only one of them means a daemon owns the journals now; the
	// other is the write of daemon.hb itself failing, which a removed run directory, a denied
	// os.Chtimes or an anti-virus holding the file all reduce to. Telling an operator to stop a
	// daemon that does not exist sends them somewhere there is nothing to do, and leaves the rerun
	// that WOULD finish the pair unmentioned.
	// holdsLock's own rationale has two halves, and only one of them was pinned. It goes through
	// Heartbeat rather than ownedByFile because that answers both questions at once: it refuses
	// unless this acquisition still owns the lock, AND it refreshes the very mtime the staleness
	// protocol reads, which shrinks the window a starting daemon judges this run by instead of only
	// reporting it afterwards. Replacing the call with a bare ownership check left every test green,
	// so the refresh — the half that is not the ownership check — was asserted nowhere.
	//
	// The clock is the lock's own fake, so this asserts the refresh without waiting for anything:
	// daemon.hb's mtime must move to the time the clock now reads.
	t.Run("each ownership check refreshes the heartbeat the staleness protocol reads", func(t *testing.T) {
		root := toolTestProject(t, 1)
		clk := newFakeClock(epoch)
		addr, err := ipc.Resolve(root)
		require.NoError(t, err)
		lock, err := AcquireLock(root, addr, clk)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		hb := filepath.Join(paths.Of(root).Run, heartbeatFileName)
		before, err := os.Stat(paths.Long(hb))
		require.NoError(t, err)
		require.Equal(t, epoch.UnixMilli(), before.ModTime().UnixMilli(),
			"AcquireLock's own initial heartbeat stamps the clock it was given")

		// Long enough that the lock would be judged stale on Windows, where daemon.hb's mtime is the
		// only barrier left, if nothing refreshed it.
		clk.Advance(2 * staleAfter)
		o := DeliverySealOptions{ProjectRoot: root, Check: true, Out: &bytes.Buffer{}, Clock: clk}
		require.NoError(t, o.holdsLock(lock, "in this test"))

		after, err := os.Stat(paths.Long(hb))
		require.NoError(t, err)
		require.Equal(t, clk.Now().UnixMilli(), after.ModTime().UnixMilli(),
			"holdsLock must REFRESH the heartbeat, not merely read the lock: a check that only "+
				"reported staleness would leave the window it is meant to shrink exactly as wide")
		require.Greater(t, after.ModTime().UnixMilli(), before.ModTime().UnixMilli())
	})

	t.Run("a run directory removed after the scan is not reported as a daemon takeover", func(t *testing.T) {
		root := toolTestProject(t, 2)
		before := toolTestState(t, root)

		// Removed at the ack journal's sync: after both scans, before the conversion loop, which is
		// where the first pre-write ownership check runs.
		report, err := toolTestRun(t, root, DeliverySealOptions{
			ToV1: true,
			syncData: func(f *os.File) error {
				if filepath.Base(f.Name()) == deliveryAckFile {
					require.NoError(t, os.RemoveAll(paths.Long(paths.Of(root).Run)))
				}
				return f.Sync()
			},
		})

		require.ErrorContains(t, err, "before converting the lease seal")
		require.ErrorContains(t, err, "its own heartbeat could not be written")
		require.NotContains(t, err.Error(), "a daemon that started meanwhile",
			"no daemon took this project: there is no lock file for one to have written")
		require.NotContains(t, report, "wrote v1")
		require.Equal(t, before, toolTestState(t, root), "and nothing was written")
	})

	t.Run("a run directory removed between the two seals says how to finish the pair", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State

		// The same window the takeover subtest below uses — the lease seal's own report line, after
		// its WriteAtomic and before the ack seal's ownership check — reached by the other of the two
		// causes. The advice must follow the cause, not the step.
		out := &toolTestWriter{when: "wrote v1", do: func() {
			require.NoError(t, os.RemoveAll(paths.Long(paths.Of(root).Run)))
		}}
		err := RepairDeliverySeal(DeliverySealOptions{
			ProjectRoot: root, ToV1: true, Out: out, Clock: newFakeClock(epoch),
		})
		t.Logf("report: %s", out.buf.String())

		require.ErrorContains(t, err, "before converting the ack seal")
		require.ErrorContains(t, err, "the lease seal is already v1")
		require.ErrorContains(t, err, "rerunning the same command converts what is left")
		require.NotContains(t, err.Error(), "stopping that daemon",
			"there is no daemon to stop, and the rerun is what finishes the pair")

		// The state the advice is about: half converted, and self-healing exactly as it says.
		position, perr := loadDeliveryPosition(filepath.Join(state, deliveryPositionFile), deliveryChainSeed)
		require.NoError(t, perr, "the lease seal the message calls converted is a v1 seal")
		require.Equal(t, 2, position.Count)
		require.Len(t, readTestFile(t, filepath.Join(state, deliveryAckPositionFile)), deliverySealFileSize,
			"and the ack seal is the one still v2")

		// The rerun the message prescribes: no daemon holds anything, so it is refused by nothing and
		// finishes the pair.
		_, rerunErr := toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, rerunErr, "the advice must be advice that works")
		ackPosition, aerr := loadDeliveryPosition(filepath.Join(state, deliveryAckPositionFile), deliveryAckChainSeed)
		require.NoError(t, aerr)
		require.Equal(t, 1, ackPosition.Count)
		requireOpensWithFormat(t, root, 1, 2)
	})

	t.Run("a daemon that takes the lock between the two seals leaves the pair half converted", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State
		before := toolTestState(t, root)

		// The takeover lands on the lease seal's own report line: after its WriteAtomic, before the
		// ack seal's ownership check. That is the one window in which this tool leaves a half
		// converted pair, and what the operator is told about it is the whole of the fix here — a
		// rerun is refused with ErrLockHeld until the daemon that took the project is stopped, so
		// "rerun the same command" on its own would be wrong.
		var taken *Lock
		out := &toolTestWriter{when: "wrote v1", do: func() { taken = replaceTestLock(t, root) }}
		err := RepairDeliverySeal(DeliverySealOptions{
			ProjectRoot: root, ToV1: true, Out: out, Clock: newFakeClock(epoch),
		})
		t.Logf("report:\n%s", out.buf.String())

		require.ErrorContains(t, err, "before converting the ack seal")
		require.ErrorContains(t, err, "the lease seal is already v1")
		require.ErrorContains(t, err, "stopping that daemon and rerunning converts what is left")
		require.NotNil(t, taken, "the takeover must actually have happened")

		after := toolTestState(t, root)
		require.Equal(t, before[deliveryLeaseFile], after[deliveryLeaseFile], "the journals are evidence")
		require.Equal(t, before[deliveryAckFile], after[deliveryAckFile], "the journals are evidence")
		require.Equal(t, before[deliveryAckPositionFile], after[deliveryAckPositionFile],
			"the refused seal is the one the message says is left")
		require.Len(t, after[deliveryAckPositionFile], deliverySealFileSize, "the ack seal is still v2")
		position, err := loadDeliveryPosition(filepath.Join(state, deliveryPositionFile), deliveryChainSeed)
		require.NoError(t, err, "the lease seal the message calls converted is a v1 seal: %s",
			toolTestSeals(t, root))
		require.Equal(t, 2, position.Count)

		// The message's own PREMISE, then its PROMISE. Asserting its three clauses says the sentence
		// is written; it does not say the recovery it prescribes exists. So: a rerun while that
		// daemon holds the lock is refused with ErrLockHeld, which is exactly why "rerun the same
		// command" alone would have been the wrong advice here; and once that daemon is stopped, the
		// identical command converts the ack seal and the build an operator rolled back to opens the
		// project with both identities.
		_, held := toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.ErrorIs(t, held, ErrLockHeld, "the daemon that took the project refuses the rerun")
		require.Equal(t, after, toolTestState(t, root), "and a refused rerun writes nothing")

		require.NoError(t, taken.Release())
		report, rerun := toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, rerun, "stopping that daemon is what the message says to do")
		require.Contains(t, report, "wrote v1")
		ackPosition, aerr := loadDeliveryPosition(
			filepath.Join(state, deliveryAckPositionFile), deliveryAckChainSeed)
		require.NoError(t, aerr, "the seal the first run left behind is now v1: %s", toolTestSeals(t, root))
		require.Equal(t, 1, ackPosition.Count)
		requireOpensWithFormat(t, root, 1, 2)
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
		// A full load is BOTH journals, and the report says so of each: the check is not a lease-side
		// answer with the ack side along for the ride.
		require.Contains(t, report, "lease journal")
		require.Contains(t, report, "loads, 2 entries")
		require.Contains(t, report, "ack journal")
		require.Contains(t, report, "loads, 1 entries")
		require.Equal(t, before, toolTestState(t, root), "--check must write nothing")

		report, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, err)
		require.Contains(t, report, "wrote v1")
		require.NotContains(t, report, "admitted",
			"a run that accepted nothing must not print an acceptance, not even one of zero lines")

		leasePosition, err := loadDeliveryPosition(leaseSeal, deliveryChainSeed)
		require.NoError(t, err, "lease seal: %s", toolTestSeals(t, root))
		require.Equal(t, 2, leasePosition.Count)
		ackPosition, err := loadDeliveryPosition(ackSeal, deliveryAckChainSeed)
		require.NoError(t, err, "ack seal: %s", toolTestSeals(t, root))
		require.Equal(t, 1, ackPosition.Count, "the ack seal names the acknowledgement the fixture wrote")

		after := toolTestState(t, root)
		require.Equal(t, before[deliveryLeaseFile], after[deliveryLeaseFile], "the journals are evidence")
		require.Equal(t, before[deliveryAckFile], after[deliveryAckFile], "the journals are evidence")

		// The point of the conversion: a build that writes v1 — the one an operator rolled back to
		// — now opens the project and recovers every identity.
		requireOpensWithFormat(t, root, 1, 2)
	})

	t.Run("a rerun reads the v1 seals it wrote and converts what is left", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State
		leaseSeal := filepath.Join(state, deliveryPositionFile)
		ackSeal := filepath.Join(state, deliveryAckPositionFile)

		_, err := toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, err)

		// Every later run reads both seals through the dual reader's V1 branch, which no other test
		// here reaches: the fixture is a crash image, so the first run is always the v2 one. This is
		// what makes the half-converted pair a rerun can finish (the convert loop's own error) a
		// recoverable state rather than a claim.
		_, err = loadDeliveryPosition(leaseSeal, deliveryChainSeed)
		require.NoError(t, err, "the first run left a v1 lease seal")
		_, err = loadDeliveryPosition(ackSeal, deliveryAckChainSeed)
		require.NoError(t, err, "the first run left a v1 ack seal")

		report, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.NoError(t, err)
		require.Contains(t, report, "v1")

		before := toolTestState(t, root)
		report, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.NoError(t, err)
		require.Contains(t, report, "wrote v1")
		require.Equal(t, before, toolTestState(t, root), "a rerun over v1 seals changes no byte")
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

	t.Run("refuses to convert either seal when the ACK journal does not load", func(t *testing.T) {
		root := toolTestProject(t, 2)
		state := paths.Of(root).State
		acks := filepath.Join(state, deliveryAckFile)
		info, err := os.Lstat(acks)
		require.NoError(t, err)
		require.NoError(t, os.Truncate(acks, info.Size()-1))
		before := toolTestState(t, root)

		// The lease side loads perfectly well here, and that is the point: "--to v1 converts only
		// after a successful full load" (design §4.5) is a claim about the pair, because the
		// acknowledgement scan is what checks every acknowledgement against a surviving lease and so
		// answers for both. A tool that converted each side as soon as that side alone loaded would
		// convert this project's lease seal and leave the operator with a repaired half of a project
		// whose evidence is torn.
		_, err = toolTestRun(t, root, DeliverySealOptions{ToV1: true})
		require.Error(t, err)
		require.ErrorContains(t, err, "ack")
		require.ErrorContains(t, err, "does not load against that seal")
		require.Equal(t, before, toolTestState(t, root))
		require.Len(t, before[deliveryPositionFile], deliverySealFileSize,
			"the lease seal was never converted: it is still the v2 image")
	})

	// The other half-present refusal: this pair is whole, and the pair its rows name is not there at
	// all. It is refused one level up from deliverySealPairPresent, because an acknowledgement
	// journal cannot be scanned without the leases its every row must name.
	//
	// The empty case is the one that needs the refusal rather than merely agreeing with it. An
	// acknowledgement journal with rows fails loadAcksFrom's "every acknowledgement names a
	// surviving lease" check anyway, against the empty lease map a lease-less scan would leave; a
	// 0-byte one passes that check vacuously, so without this branch the tool would seal it for a
	// project that has no lease journal at all.
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, state string)
	}{
		{"holding an acknowledgement", func(t *testing.T, state string) {}},
		{"empty, so that nothing else refuses it", func(t *testing.T, state string) {
			require.NoError(t, os.Truncate(filepath.Join(state, deliveryAckFile), 0))
			require.NoError(t, createEmptyDeliveryPositionV1(
				filepath.Join(state, deliveryAckPositionFile), deliveryAckChainSeed))
		}},
	} {
		t.Run("refuses an acknowledgement journal whose lease journal is gone, "+tc.name, func(t *testing.T) {
			root := toolTestProject(t, 2)
			state := paths.Of(root).State
			tc.prepare(t, state)
			for _, name := range []string{deliveryLeaseFile, deliveryPositionFile} {
				require.NoError(t, os.Remove(filepath.Join(state, name)))
			}
			acks := readTestFile(t, filepath.Join(state, deliveryAckFile))
			ackSeal := readTestFile(t, filepath.Join(state, deliveryAckPositionFile))

			_, err := toolTestRun(t, root, DeliverySealOptions{ToV1: true})
			require.ErrorContains(t, err, "the lease journal its rows name is not")
			require.Equal(t, acks, readTestFile(t, filepath.Join(state, deliveryAckFile)))
			require.Equal(t, ackSeal, readTestFile(t, filepath.Join(state, deliveryAckPositionFile)))
		})
	}

	// The older-seal checkpoint the tool forwards to the scan (delivery_seal_tool.go's inspect,
	// s.scan(read.position, read.older)). Replacing that argument with nil left the whole suite
	// green, and this tool is the one program in the system that turns a project the daemon REFUSES
	// into one it accepts: after --to v1 the older record is gone for good, so a regression here
	// launders exactly the class of journal the checkpoint exists to catch, permanently and
	// silently.
	//
	// The image is built so that only the SCAN can see the defect. The older record is re-summed for
	// its own slot, so classifySlot still calls it valid, and it still seals strictly fewer bytes
	// and entries than the effective record, so selectSeal still accepts the pair — which the
	// subtest requires rather than assumes.
	//
	// The review asked for an older record whose Count is one too high with its Bytes left alone.
	// That image cannot exist, and the code is why: selectSeal admits two valid slots only when
	// older.Count < eff.Count, and the two records are always one batch apart, so older.Count+1
	// equals eff.Count and the strict reader refuses the pair before any scan runs. Moving the
	// BYTES back one line produces the same defect in an image the strict reader accepts — a Count
	// one too high for the prefix it names — which is what the checkpoint is about.
	t.Run("refuses a seal whose older record is not a prefix of this journal", func(t *testing.T) {
		root := toolTestProject(t, 3)
		state := paths.Of(root).State
		sealPath := filepath.Join(state, deliveryPositionFile)

		image := readTestFile(t, sealPath)
		require.True(t, isDeliverySealImage(image), "the fixture leaves a v2 lease seal")
		eff, older, err := selectSeal(image, deliveryChainDomain, deliveryChainSeed)
		require.NoError(t, err)
		require.NotNil(t, older, "the seal must carry an older record, or this subtest pins nothing")
		require.Greater(t, older.Count, 1, "and that record must seal more than the first line")

		moved := *older
		moved.Bytes = toolTestLineBoundary(t, filepath.Join(state, deliveryLeaseFile), older.Count-1)
		require.Positive(t, moved.Bytes, "an older record sealing 0 bytes is checked against nothing")
		slot := slotFor(older.Seq)
		region, _, err := encodeSlot(moved, slot, deliveryChainDomain, deliveryChainSeed)
		require.NoError(t, err)
		tampered := withRegion(image, slot, region)

		gotEff, gotOlder, err := selectSeal(tampered, deliveryChainDomain, deliveryChainSeed)
		require.NoError(t, err, "guard: the STRICT reader must still accept this pair, or the scan "+
			"is not what refuses it")
		require.Equal(t, eff, gotEff, "guard: the effective record is untouched")
		require.Equal(t, moved.Bytes, gotOlder.Bytes, "guard: the older record is the tampered one")
		require.NoError(t, os.WriteFile(paths.Long(sealPath), tampered, 0o600))

		before := toolTestState(t, root)
		_, err = toolTestRun(t, root, DeliverySealOptions{Check: true})
		require.ErrorContains(t, err, "does not load against that seal",
			"the checkpoint the tool forwards is what refuses this journal")
		require.ErrorContains(t, err, "lease")
		require.Equal(t, before, toolTestState(t, root), "and a check writes nothing whatever it finds")
	})

	// A half-present pair, in both directions and by its own words. require.Error alone was not
	// enough: with deliverySealPairPresent's refusal disabled a DIFFERENT check supplies an error —
	// the ack scan's own Lstat — so the subtest passed while the branch it names did nothing. Every
	// half-present shape still fails closed under that mutation, so this is assertion strength
	// rather than a hole; it is also the gap that was closed one level up for the sibling refusal
	// and left open for its neighbour.
	for _, tc := range []struct {
		name    string
		missing string
	}{
		{"the journal is there and its seal is gone", deliveryAckPositionFile},
		{"the seal is there and its journal is gone", deliveryAckFile},
	} {
		t.Run("refuses a half-present pair: "+tc.name, func(t *testing.T) {
			root := toolTestProject(t, 1)
			require.NoError(t, os.Remove(filepath.Join(paths.Of(root).State, tc.missing)))
			before := readTestFile(t, filepath.Join(paths.Of(root).State, deliveryLeaseFile))

			_, err := toolTestRun(t, root, DeliverySealOptions{Check: true})
			require.ErrorContains(t, err, "must both be present or both absent",
				"the refusal must be deliverySealPairPresent's own, not whichever check errors first")
			require.ErrorContains(t, err, "this is a recovery decision, not a repair")
			require.ErrorContains(t, err, "ack", "and it must name the side it is about")
			require.Equal(t, before, readTestFile(t, filepath.Join(paths.Of(root).State, deliveryLeaseFile)),
				"a refusal leaves the evidence as it found it")
		})
	}

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

	// Design §4.5's "print exactly which lines --accept-torn-slot admitted" is the operator's only
	// record of what they took responsibility for, and under a closed pipe the conversion would
	// otherwise still happen while that record was lost. The first write error is latched, and a run
	// that accepted a torn slot consults it before anything is synced or written.
	t.Run("a conversion refuses when the acceptance could not be reported", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)
		before := toolTestState(t, root)

		closed := errors.New("broken pipe")
		var out failingWriter
		out.failAt, out.err = 1, closed
		err := RepairDeliverySeal(DeliverySealOptions{
			ProjectRoot: root, ToV1: true, AcceptTornSlot: true, Confirm: true,
			Out: &out, Clock: newFakeClock(epoch),
		})

		require.ErrorIs(t, err, closed)
		require.ErrorContains(t, err, "admitted lines that could not be reported")
		require.ErrorContains(t, err, "nothing was synced or written")
		require.Equal(t, before, toolTestState(t, root),
			"the consent record is what makes the acceptance an operator's decision, so no seal is written without it")

		// The rerun the refusal implies: the same command with a writer that works converts the pair.
		report, rerun := toolTestRun(t, root, DeliverySealOptions{
			ToV1: true, AcceptTornSlot: true, Confirm: true,
		})
		require.NoError(t, rerun)
		require.Contains(t, report, "admitted 1 line")
		requireOpensWithFormat(t, root, 1, 2)
	})

	// The same lost report on a run that changes nothing is not a refusal: a check is repeatable,
	// and a conversion with its record missing is not.
	t.Run("a check whose report was lost still reports nothing written", func(t *testing.T) {
		root := toolTestProject(t, 2)
		tearNewestSlot(t, root)
		before := toolTestState(t, root)

		var out failingWriter
		out.failAt, out.err = 1, errors.New("broken pipe")
		require.NoError(t, RepairDeliverySeal(DeliverySealOptions{
			ProjectRoot: root, Check: true, AcceptTornSlot: true, Confirm: true,
			Out: &out, Clock: newFakeClock(epoch),
		}))
		require.Equal(t, before, toolTestState(t, root))
	})

	// deliverySealTail's short-file guard, directly. It is called only after a scan succeeded, so
	// from is a line boundary of the file the scan read — but the file is re-opened to produce the
	// report, and a journal that shrank between the two turns the guard's removal from a refusal
	// into a slice-bounds panic in an operator's tool.
	t.Run("the accepted-lines re-read refuses a from past the file's end", func(t *testing.T) {
		root := toolTestProject(t, 2)
		journal := filepath.Join(paths.Of(root).State, deliveryLeaseFile)
		info, err := os.Lstat(journal)
		require.NoError(t, err)

		lines, err := deliverySealTail(journal, info.Size())
		require.NoError(t, err, "at the file's own size there is simply no tail")
		require.Empty(t, lines)

		lines, err = deliverySealTail(journal, info.Size()+1)
		require.ErrorIs(t, err, core.ErrDegraded,
			"a journal that shrank since the scan is refused, never sliced past its end")
		require.Nil(t, lines)
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

// deliveryLineTerminator is the byte every canonical journal line ends with.
const deliveryLineTerminator = byte(10)

// toolTestLineBoundary is the byte offset just past the journal's nth line: the position a seal
// sealing exactly n records names.
func toolTestLineBoundary(t *testing.T, journal string, lines int) int64 {
	t.Helper()

	b := readTestFile(t, journal)
	var at int64
	for range lines {
		i := bytes.IndexByte(b[at:], deliveryLineTerminator)
		require.GreaterOrEqual(t, i, 0, "the journal holds fewer than %d lines", lines)
		at += int64(i) + 1
	}
	return at
}

// failingWriter is a report writer that fails from its failAt-th write onwards: a closed pipe, which
// is what `| head` leaves an operator's tool holding.
type failingWriter struct {
	writes int
	failAt int
	err    error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failAt {
		return 0, w.err
	}
	return len(p), nil
}
