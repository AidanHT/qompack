package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The journal driven by a build whose write format is 2 (design 4.3, step 2). deliverySealWriteFormat
// is still 1 and TestDeliverySeal_WriteFormatIsDeliberate still pins it, so nothing here changes what
// this build writes; these tests choose the other format through the lock's seam and exercise the
// wiring the shipped constant leaves inert.
//
// They are also the design 6.1 step-2 trace, executed: each row of that trace — the position
// corruption modes, an uncertain append, Release against a batch in flight, a replaced lock, and a
// failed close — is asserted here against a held v2 seal, beside the format-1 originals that go on
// passing unchanged.
//
// The last test in the file runs the other way round, and pins the half of the seam this wiring must
// not widen: the SHIPPED format-1 build refuses a v2 image at either sidecar.

// openSealFormat opens root's journal with the write format f, and returns the lock and the journal.
// Everything but the format is the production path: the same AcquireLock, the same
// openDeliveryJournal, the same existence rules, load, conversion and open sequence.
func openSealFormat(t *testing.T, root string, f int) (*Lock, *deliveryJournal) {
	t.Helper()
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = f
	t.Cleanup(func() { _ = lock.Release() })
	journal, err := lock.openDeliveryJournal()
	require.NoError(t, err)
	requireNoLeakedInflight(t, journal)
	return lock, journal
}

// newFormatTwoJournal is newTestDeliveryJournal for a format-2 build: a fresh project, whose
// position files O1 creates as v1 and O4a converts once the load has succeeded.
func newFormatTwoJournal(t *testing.T) (string, *Lock, *deliveryJournal) {
	t.Helper()
	root := t.TempDir()
	lock, journal := openSealFormat(t, root, 2)
	return root, lock, journal
}

// sealFileOf and ackSealFileOf are the two position files' bytes.
func sealFileOf(t *testing.T, root string) []byte {
	t.Helper()
	return readTestFile(t, filepath.Join(paths.Of(root).State, deliveryPositionFile))
}

func ackSealFileOf(t *testing.T, root string) []byte {
	t.Helper()
	return readTestFile(t, filepath.Join(paths.Of(root).State, deliveryAckPositionFile))
}

// requireSealsPosition asserts that file is a v2 image whose effective record seals exactly
// (size, count, chain) for the journal named by domain and seed.
func requireSealsPosition(
	t *testing.T, file []byte, domain string, seed core.Hash, size int64, count int, chain core.Hash,
) {
	t.Helper()
	require.True(t, isDeliverySealImage(file), "the position file is a v2 image")
	effective, _, err := selectSeal(file, domain, seed)
	require.NoError(t, err)
	require.Equal(t, size, effective.Bytes)
	require.Equal(t, count, effective.Count)
	require.Equal(t, chain, effective.Chain)
}

// requireV1Position asserts that file is exactly the v1 sidecar for (size, count, chain) — the bytes
// savePosition itself writes — and that today's v1 reader accepts it as that position.
func requireV1Position(
	t *testing.T, p string, seed core.Hash, size int64, count int, chain core.Hash,
) {
	t.Helper()
	file := readTestFile(t, p)
	require.False(t, isDeliverySealImage(file), "a downgraded position file is not a v2 image")
	want, err := encodeDeliveryPositionV1(size, count, chain)
	require.NoError(t, err)
	require.Equal(t, want, file, "the downgrade writes today's v1 bytes")
	position, err := loadDeliveryPosition(p, seed)
	require.NoError(t, err)
	require.Equal(t, deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: count, Chain: chain}, position)
}

// copyProjectState copies src's state directory into dst: what a backup taken of a running daemon
// holds, and what a restore puts back. The seal file is read while its handle is still open, which
// is exactly the live-backup case (design R10).
func copyProjectState(t *testing.T, src, dst string) {
	t.Helper()
	from, to := paths.Of(src).State, paths.Of(dst).State
	require.NoError(t, os.MkdirAll(paths.Long(to), 0o700))
	entries, err := os.ReadDir(paths.Long(from))
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(paths.Long(filepath.Join(from, e.Name())))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(to, e.Name())), b, 0o600))
	}
}

// leaseAndAck takes n leases on j, acknowledging each, and returns them.
func leaseAndAck(t *testing.T, j *deliveryJournal, n int, session core.SessionID) []deliveryLease {
	t.Helper()
	ctx := context.Background()
	out := make([]deliveryLease, n)
	for k := range n {
		lease, err := j.lease(ctx, leaseToken(k+1), session, testDeliveryRequest("format2"))
		require.NoError(t, err)
		require.NoError(t, j.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{}))
		out[k] = lease
	}
	return out
}

// A format-2 journal seals every batch in place, through the handle it holds for its life: the file
// stays exactly deliverySealFileSize bytes, the slots alternate by parity, and each seal names the
// whole synced journal. The identities are the ones a format-1 build assigns for the same calls.
func TestDeliverySeal_FormatTwoSealsEveryBatchInPlace(t *testing.T) {
	root, lock, journal := newFormatTwoJournal(t)
	require.NotNil(t, journal.seal, "a format-2 open holds its lease seal")
	require.NotNil(t, journal.ackSeal, "and its acknowledgement seal")

	// O4a converted the freshly created v1 sidecar: seq 1 over the empty journal, slot b still empty.
	requireSealsPosition(t, sealFileOf(t, root), deliveryChainDomain, deliveryChainSeed, 0, 0, deliveryChainSeed)
	require.Equal(t, uint64(1), journal.seal.seq)

	leases := leaseAndAck(t, journal, 4, "format2")
	for k, lease := range leases {
		require.Equal(t, uint64(k+1), lease.ArrivalSeq)
	}
	require.Equal(t, uint64(5), journal.seal.seq, "one seal per lease batch, seq 1 plus four")
	require.Equal(t, slotFor(journal.seal.seq), slotFor(5))

	file := sealFileOf(t, root)
	require.Len(t, file, deliverySealFileSize, "the seal file never changes size")
	requireSealsPosition(t, file, deliveryChainDomain, deliveryChainSeed,
		journal.bytes, len(journal.leases), journal.chain)
	requireSealsPosition(t, ackSealFileOf(t, root), deliveryAckChainDomain, deliveryAckChainSeed,
		journal.ackBytes, len(journal.acks), journal.ackChain)

	// loadPosition reads the v2 seal through the dual reader, so the tests that ask a journal what it
	// sealed get the same answer in either format.
	position, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, leasePositionOf(readTestFile(t, journal.path)), position)

	// A redelivery is a known nonce in either format, and appends nothing.
	before := readTestFile(t, journal.path)
	again, err := journal.lease(context.Background(), leases[0].Delivery, "format2", testDeliveryRequest("format2"))
	require.NoError(t, err)
	require.Equal(t, leases[0], again)
	require.Equal(t, before, readTestFile(t, journal.path))

	// A clean Release downgrades both positions to v1 at the final seal.
	size, count, chain := journal.bytes, len(journal.leases), journal.chain
	ackSize, ackCount, ackChain := journal.ackBytes, len(journal.acks), journal.ackChain
	require.NoError(t, lock.Release())
	requireV1Position(t, filepath.Join(paths.Of(root).State, deliveryPositionFile),
		deliveryChainSeed, size, count, chain)
	requireV1Position(t, filepath.Join(paths.Of(root).State, deliveryAckPositionFile),
		deliveryAckChainSeed, ackSize, ackCount, ackChain)
}

// T25 — design 6.2. The conversion and the downgrade, in both directions and at their edges.
func TestDeliverySeal_ConversionAndDowngrade(t *testing.T) {
	ctx := context.Background()
	req := testDeliveryRequest("t25")

	t.Run("a v1 project converts, but only after a load that succeeded", func(t *testing.T) {
		root := t.TempDir()
		lock, journal := openSealFormat(t, root, 1)
		for k := range 2 {
			_, err := journal.lease(ctx, leaseToken(k+1), "t25", req)
			require.NoError(t, err)
		}
		size, count, chain := journal.bytes, len(journal.leases), journal.chain
		require.NoError(t, lock.Release())
		require.False(t, isDeliverySealImage(sealFileOf(t, root)), "a format-1 build leaves v1 behind")

		_, reopened := openSealFormat(t, root, 2)
		require.Len(t, reopened.leases, 2, "the identities survive the conversion")
		require.Equal(t, size, reopened.bytes)
		requireSealsPosition(t, sealFileOf(t, root), deliveryChainDomain, deliveryChainSeed, size, count, chain)
		// A converted file is the fresh image: seq 1 in slot a, slot b empty, so it has no older
		// record for load to check.
		_, older, err := selectSeal(sealFileOf(t, root), deliveryChainDomain, deliveryChainSeed)
		require.NoError(t, err)
		require.Nil(t, older)
	})

	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, root string, journal *deliveryJournal)
	}{
		{"a torn journal tail", func(t *testing.T, _ string, journal *deliveryJournal) {
			file := readTestFile(t, journal.path)
			require.NoError(t, os.WriteFile(paths.Long(journal.path), file[:len(file)-1], 0o600))
		}},
		{"a position that names a count the journal never had", func(t *testing.T, root string, _ *deliveryJournal) {
			p := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			position, err := loadDeliveryPosition(p, deliveryChainSeed)
			require.NoError(t, err)
			encoded, err := encodeDeliveryPositionV1(position.Bytes, position.Count+1, position.Chain)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(p), encoded, 0o600))
		}},
	} {
		t.Run("a failed load leaves the v1 bytes untouched: "+tc.name, func(t *testing.T) {
			root := t.TempDir()
			lock, journal := openSealFormat(t, root, 1)
			_, err := journal.lease(ctx, leaseToken(1), "t25", req)
			require.NoError(t, err)
			require.NoError(t, lock.Release())
			tc.damage(t, root, journal)
			v1, journalBytes := sealFileOf(t, root), readTestFile(t, journal.path)

			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			next.sealFormat = 2
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			require.ErrorIs(t, err, core.ErrDegraded)
			require.Nil(t, got)
			require.Equal(t, v1, sealFileOf(t, root), "a conversion must never run before a successful load")
			require.False(t, isDeliverySealImage(sealFileOf(t, root)))
			require.Equal(t, journalBytes, readTestFile(t, journal.path), "and the journal is preserved")
		})
	}

	t.Run("a faulted Release writes no downgrade", func(t *testing.T) {
		root, lock, journal := newFormatTwoJournal(t)
		_, err := journal.lease(ctx, leaseToken(1), "t25", req)
		require.NoError(t, err)
		sealed := sealFileOf(t, root)

		journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
			return errors.New("private backend fixture")
		}}
		_, err = journal.lease(ctx, leaseToken(2), "t25", req)
		require.ErrorIs(t, err, core.ErrDegraded)
		require.Error(t, journalFault(journal))

		journal.writer = journal.file // repair only the owned failure-injection seam
		require.NoError(t, lock.Release())
		require.Equal(t, sealed, sealFileOf(t, root),
			"an uncertain journal must not have its position rewritten from a cur it cannot vouch for")
		require.True(t, isDeliverySealImage(sealFileOf(t, root)))
	})

	t.Run("a Release by a replaced owner writes no downgrade", func(t *testing.T) {
		root, old, oldJournal := newFormatTwoJournal(t)
		_, err := oldJournal.lease(ctx, leaseToken(1), "t25", req)
		require.NoError(t, err)

		// The lock is legitimately lost and retaken, and the new owner opens its own format-2
		// journal, whose seal now holds the position file open.
		lockPath := LockPath(root)
		require.NoError(t, os.Chmod(paths.Long(lockPath), 0o600))
		require.NoError(t, os.Remove(paths.Long(lockPath)))
		require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(root).Run, heartbeatFileName))))
		_, newJournal := openSealFormat(t, root, 2)
		held := sealFileOf(t, root)

		require.NoError(t, old.Release(), "an old owner must not remove the new generation")
		require.Equal(t, held, sealFileOf(t, root),
			"a lost lock must not land a v1 file over the file the current owner holds open")
		require.True(t, isDeliverySealImage(sealFileOf(t, root)))
		// The condition is load bearing precisely because the new owner goes on checking that file.
		require.NoError(t, newJournal.checkFile())
		_, err = newJournal.lease(ctx, leaseToken(2), "t25", req)
		require.NoError(t, err, "the current owner remains able to lease after the old owner releases")
	})

	t.Run("a format-1 build converts a v2 file back", func(t *testing.T) {
		root, _, journal := newFormatTwoJournal(t)
		leases := leaseAndAck(t, journal, 2, "t25")
		size, count, chain := journal.bytes, len(journal.leases), journal.chain

		// A crash image: the project's state copied while the v2 seal is live, so nothing downgraded.
		crashed := t.TempDir()
		copyProjectState(t, root, crashed)
		require.True(t, isDeliverySealImage(sealFileOf(t, crashed)))

		_, rolled := openSealFormat(t, crashed, 1)
		require.Len(t, rolled.leases, 2, "the identities survive the rollback")
		requireV1Position(t, filepath.Join(paths.Of(crashed).State, deliveryPositionFile),
			deliveryChainSeed, size, count, chain)
		require.False(t, isDeliverySealImage(ackSealFileOf(t, crashed)), "the acknowledgement seal too")
		again, err := rolled.lease(ctx, leases[0].Delivery, "t25", testDeliveryRequest("format2"))
		require.NoError(t, err)
		require.Equal(t, leases[0], again, "a redelivery keeps the identity the v2 build assigned")
	})
}

// T26 — design 6.2. The rollback drill across formats: a crash image holding v2, backed up and
// restored, opened by a build that writes v1, and then opened again by one that writes v2. Every
// identity is continuous across all of it, and each build leaves the format it writes.
func TestDeliveryJournal_RollbackDrillAcrossFormats(t *testing.T) {
	ctx := context.Background()
	root, _, journal := newFormatTwoJournal(t)
	leases := leaseAndAck(t, journal, 3, "t26")
	size, count, chain := journal.bytes, len(journal.leases), journal.chain
	journalBytes, ackBytes := readTestFile(t, journal.path), readTestFile(t, journal.ackPath)

	// The backup is taken with the daemon running, so it holds the v2 seal mid-life.
	backup := t.TempDir()
	copyProjectState(t, root, backup)
	require.True(t, isDeliverySealImage(sealFileOf(t, backup)))

	// The restore: the backup's state put back into a project of its own.
	restored := t.TempDir()
	copyProjectState(t, backup, restored)
	require.Equal(t, journalBytes, readTestFile(t, filepath.Join(paths.Of(restored).State, deliveryLeaseFile)))
	require.Equal(t, ackBytes, readTestFile(t, filepath.Join(paths.Of(restored).State, deliveryAckFile)))

	// Rolled back to a build that writes v1: it reads the v2 seal and leaves v1 behind.
	rolledLock, rolled := openSealFormat(t, restored, 1)
	require.Len(t, rolled.leases, 3)
	require.Equal(t, size, rolled.bytes)
	require.Equal(t, chain, rolled.chain)
	requireV1Position(t, filepath.Join(paths.Of(restored).State, deliveryPositionFile),
		deliveryChainSeed, size, count, chain)
	for _, lease := range leases {
		again, err := rolled.lease(ctx, lease.Delivery, "t26", testDeliveryRequest("format2"))
		require.NoError(t, err)
		require.Equal(t, lease, again, "every identity is continuous through the rollback")
		require.True(t, rolled.acknowledged(lease.Delivery), "and so is the frontier")
	}
	next, err := rolled.lease(ctx, leaseToken(99), "t26", testDeliveryRequest("format2"))
	require.NoError(t, err)
	require.Equal(t, uint64(4), next.ArrivalSeq, "the arrival sequence resumes where the v2 build left it")
	rolledSize, rolledCount, rolledChain := rolled.bytes, len(rolled.leases), rolled.chain
	require.NoError(t, rolledLock.Release())

	// Rolled forward again: the v1 file converts, and the identities are still the first build's.
	_, forward := openSealFormat(t, restored, 2)
	require.Len(t, forward.leases, 4)
	requireSealsPosition(t, sealFileOf(t, restored), deliveryChainDomain, deliveryChainSeed,
		rolledSize, rolledCount, rolledChain)
	for _, lease := range leases {
		again, err := forward.lease(ctx, lease.Delivery, "t26", testDeliveryRequest("format2"))
		require.NoError(t, err)
		require.Equal(t, lease, again)
	}
}

// The design 6.1 step-2 trace, executed against a held v2 seal.
func TestDeliverySeal_StepTwoTraceHoldsInFormatTwo(t *testing.T) {
	ctx := context.Background()
	req := testDeliveryRequest("trace")

	// PositionCorruption, whose seven v1 modes become the ways a HELD v2 file can be made to
	// disagree with its handle. Each is detected by the next batch's check, before anything is
	// appended; the journal is poisoned, the files are left exactly as the corruption left them, the
	// faulted Release writes no downgrade, and the reopen refuses.
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, p string, j *deliveryJournal)
		// gone is whether the corruption leaves no file at the path at all.
		gone bool
		// reopens is whether a FRESH open afterwards is entitled to the file. The live journal
		// refuses either way, because the file it HELD was replaced or changed under it, and that is
		// what these cases exist to pin. But two of the corruptions leave a seal at the path that
		// agrees with the journal beside it, and a later open, which holds nothing yet and reads the
		// pair as it finds it, must accept exactly those: refusing them would strand a project whose
		// evidence is intact. The rest leave a seal that no build may trust, and the open refuses.
		reopens bool
	}{
		{"truncated", func(t *testing.T, p string, _ *deliveryJournal) {
			require.NoError(t, os.Truncate(paths.Long(p), deliverySealFileSize-1))
		}, false, false},
		{"overwritten with a v1 position that disagrees", func(t *testing.T, p string, j *deliveryJournal) {
			encoded, err := encodeDeliveryPositionV1(j.bytes, len(j.leases)+1, j.chain)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(p), encoded, 0o600))
		}, false, false},
		{"overwritten with the journal's own v1 position", func(t *testing.T, p string, j *deliveryJournal) {
			encoded, err := encodeDeliveryPositionV1(j.bytes, len(j.leases), j.chain)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(p), encoded, 0o600))
		}, false, true},
		{"removed", func(t *testing.T, p string, _ *deliveryJournal) {
			require.NoError(t, os.Remove(paths.Long(p)))
		}, true, false},
		{"replaced by a copy with the same bytes", func(t *testing.T, p string, _ *deliveryJournal) {
			require.NoError(t, paths.WriteAtomic(p, readTestFile(t, p), 0o600))
		}, false, true},
		{"a digit of the effective record", func(t *testing.T, p string, j *deliveryJournal) {
			off, c := sealChainDigit(t, j.seal.image, slotFor(j.seal.seq))
			patchSealFile(t, p, off, c)
		}, false, false},
		{"a padding byte", func(t *testing.T, p string, _ *deliveryJournal) {
			patchSealFile(t, p, deliverySealSlotBOffset+deliverySealSlotRegion-1, 'x')
		}, false, false},
		{"a static byte", func(t *testing.T, p string, _ *deliveryJournal) {
			patchSealFile(t, p, deliverySealStride-1, 'x')
		}, false, false},
	} {
		t.Run("a corrupted position cannot be repaired by an open writer: "+tc.name, func(t *testing.T) {
			root, lock, journal := newFormatTwoJournal(t)
			_, err := journal.lease(ctx, leaseToken(1), "trace", req)
			require.NoError(t, err)
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)

			tc.corrupt(t, positionPath, journal)
			left, leftErr := os.ReadFile(paths.Long(positionPath))
			journalBefore := readTestFile(t, journal.path)

			_, err = journal.lease(ctx, leaseToken(2), "trace", req)
			require.ErrorIs(t, err, core.ErrDegraded, "the seal's check must refuse a file that moved or changed")
			require.Error(t, journalFault(journal))
			require.Equal(t, journalBefore, readTestFile(t, journal.path), "nothing was appended")
			after, afterErr := os.ReadFile(paths.Long(positionPath))
			require.Equal(t, leftErr == nil, afterErr == nil)
			require.Equal(t, left, after, "the files are exactly as the corruption left them")
			if tc.gone {
				require.Error(t, afterErr, "the corruption removed the path, and nothing put it back")
			} else {
				require.NoError(t, afterErr)
			}

			require.NoError(t, lock.Release())
			nowErr := afterErr
			now, _ := os.ReadFile(paths.Long(positionPath))
			if nowErr == nil {
				require.Equal(t, after, now, "a faulted Release writes no downgrade")
			}
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			next.sealFormat = 2
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			if tc.reopens {
				// The same-content replacement and the journal's own position rewritten by hand are
				// the two the HELD seal detects and a fresh open cannot: the bytes at the path seal
				// exactly the journal beside them. The live refusal above is the whole of the
				// detection here, and it is what design 2.10 calls stronger than v1's.
				require.NoError(t, err, "a seal that agrees with its journal must still open")
				require.Len(t, got.leases, 1, "and the identity the poisoned journal took survives")
				return
			}
			require.Error(t, err, "a corrupted position must not yield a writable journal")
			require.Nil(t, got)
		})
	}

	t.Run("an uncertain append poisons until recovery", func(t *testing.T) {
		root, lock, journal := newFormatTwoJournal(t)
		positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
		captured := sealFileOf(t, root)

		// The path is removed and replaced by a directory during the journal's Sync. The slot write
		// then lands in the held, unlinked file, and the post-seal identity check fails: nothing is
		// admitted and nothing is released.
		journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
			err := journal.file.Sync()
			require.NoError(t, os.Remove(paths.Long(positionPath)))
			require.NoError(t, os.Mkdir(paths.Long(positionPath), 0o700))
			return err
		}}
		lease, err := journal.lease(ctx, leaseToken(1), "trace", req)
		require.ErrorIs(t, err, core.ErrDegraded)
		require.Equal(t, deliveryLease{}, lease)
		require.Empty(t, journal.leases, "an uncertain seal admits nothing")

		journal.writer = journal.file
		require.NoError(t, lock.Release())
		require.NoError(t, os.Remove(paths.Long(positionPath))) // the empty directory the test made
		require.NoError(t, os.WriteFile(paths.Long(positionPath), captured, 0o600))

		// The reopen reads valid(seq 1) beside an empty slot b, recovers the complete tail the failed
		// batch left, and re-seals it as seq 2 in slot b.
		_, recovered := openSealFormat(t, root, 2)
		require.Equal(t, 1, len(recovered.leases), "a surviving complete tail is recovered")
		position, err := recovered.loadPosition()
		require.NoError(t, err)
		require.Equal(t, 1, position.Count, "and sealed before any caller can reuse it")
		require.Equal(t, uint64(2), recovered.seal.seq)
		require.Equal(t, sealSlotB, slotFor(recovered.seal.seq))
	})

	t.Run("a lease sync serializes with release", func(t *testing.T) {
		root, lock, journal := newFormatTwoJournal(t)
		entered, proceed := make(chan struct{}), make(chan struct{})
		var once bool
		journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
			if !once {
				once = true
				close(entered)
				<-proceed
			}
			return journal.file.Sync()
		}}
		done := make(chan error, 1)
		go func() {
			_, err := journal.lease(ctx, leaseToken(1), "trace", req)
			done <- err
		}()
		awaitClosed(t, entered, "the lease's Sync")
		position, err := journal.loadPosition()
		require.NoError(t, err)
		require.Zero(t, position.Count, "the seal cannot advance ahead of the journal's Sync")

		released := make(chan error, 1)
		go func() { released <- lock.Release() }()
		requireNotReleased(t, released, "while a batch was in flight")
		close(proceed)
		require.NoError(t, <-done)
		require.NoError(t, awaitRelease(t, released))

		// Release waited for the batch, then downgraded to v1 at the final seal.
		requireV1Position(t, filepath.Join(paths.Of(root).State, deliveryPositionFile),
			deliveryChainSeed, journal.bytes, 1, journal.chain)
		position, err = journal.loadPosition()
		require.NoError(t, err)
		require.Equal(t, 1, position.Count)
	})

	t.Run("a close failure retains ownership and leaves v2 on disk", func(t *testing.T) {
		root, lock, journal := newFormatTwoJournal(t)
		_, err := journal.lease(ctx, leaseToken(1), "trace", req)
		require.NoError(t, err)
		sealed := sealFileOf(t, root)

		journal.writer = leaseFaultWriter{file: journal.file, close: func() error {
			return errors.New("private close fixture")
		}}
		require.Error(t, lock.Release())
		_, err = os.Stat(paths.Long(LockPath(root)))
		require.NoError(t, err, "a failed close retains ownership")
		_, err = journal.lease(ctx, leaseToken(2), "trace", req)
		require.Error(t, err, "and refuses at enter once closing is set")
		require.Equal(t, sealed, sealFileOf(t, root), "a poisoned handle downgrades nothing")
		require.True(t, isDeliverySealImage(sealFileOf(t, root)))

		journal.writer = journal.file // repair only the owned failure-injection seam
		require.NoError(t, lock.Release())
		require.True(t, isDeliverySealImage(sealFileOf(t, root)), "the second Release downgrades nothing either")
	})

	t.Run("a v1 fixture still opens a format-2 build", func(t *testing.T) {
		// FailedOpen, RejectsUntrustworthyRows and AcknowledgementAheadOfItsLease all write v1
		// fixtures; the dual reader accepts them as input whatever this build writes.
		root := t.TempDir()
		state := paths.Of(root).State
		require.NoError(t, os.MkdirAll(paths.Long(state), 0o700))
		lease := testLeaseRecord(t, testDeliveryToken('a'), "trace", req, 1)
		line := []byte(leaseLine(t, lease))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, deliveryLeaseFile)), line, 0o600))
		v1, err := encodeDeliveryPositionV1(int64(len(line)), 1, deliveryChain(deliveryChainSeed, line))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, deliveryPositionFile)), v1, 0o600))

		_, journal := openSealFormat(t, root, 2)
		require.Len(t, journal.leases, 1)
		requireSealsPosition(t, sealFileOf(t, root), deliveryChainDomain, deliveryChainSeed,
			int64(len(line)), 1, deliveryChain(deliveryChainSeed, line))
		again, err := journal.lease(ctx, lease.Delivery, "trace", req)
		require.NoError(t, err)
		require.Equal(t, lease, again)
	})
}

// The other half of the format seam. The shipped build writes format 1 and holds no seal handle, so
// its per-batch check reads the sidecar from the PATH — and it reads it with the strict v1 reader,
// on both sides of the journal (design section 5: "the v1 bodies are kept verbatim").
//
// A v2 image at either position path is a foreign write: a half-rolled-back step-2 binary, a
// restore, or an operator. The check refuses it even when its effective record seals exactly the
// position this journal believes is sealed, which is the one case a dual reader would wave through.
// Only loadPosition reads either format, because the tests call it to ask a journal what it sealed.
func TestDeliverySeal_FormatOneRefusesAV2ImageAtEitherSidecar(t *testing.T) {
	ctx := context.Background()
	req := testDeliveryRequest("format1")
	for _, tc := range []struct {
		name string
		// file is the sidecar the foreign v2 image is planted at.
		file string
		// plant writes a VALID v2 image sealing exactly what this journal believes that sidecar holds.
		plant func(t *testing.T, p string, j *deliveryJournal)
		// act is the next operation on the journal, whose per-batch check must refuse.
		act func(j *deliveryJournal, first deliveryLease) error
	}{
		{
			name: "lease",
			file: deliveryPositionFile,
			plant: func(t *testing.T, p string, j *deliveryJournal) {
				t.Helper()
				image, err := newSealImage(j.bytes, len(j.leases), j.chain, deliveryChainDomain, deliveryChainSeed)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(p), image, 0o600))
			},
			act: func(j *deliveryJournal, _ deliveryLease) error {
				_, err := j.lease(ctx, leaseToken(2), "format1", req)
				return err
			},
		},
		{
			name: "acknowledgement",
			file: deliveryAckPositionFile,
			plant: func(t *testing.T, p string, j *deliveryJournal) {
				t.Helper()
				image, err := newSealImage(j.ackBytes, len(j.acks), j.ackChain,
					deliveryAckChainDomain, deliveryAckChainSeed)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(p), image, 0o600))
			},
			act: func(j *deliveryJournal, first deliveryLease) error {
				return j.acknowledge(ctx, first.Delivery, first.ObservationID, core.Hash{})
			},
		},
	} {
		t.Run("a format-1 build refuses a v2 image at its "+tc.name+" sidecar", func(t *testing.T) {
			root, _, journal := newTestDeliveryJournal(t)
			require.Nil(t, journal.seal, "the shipped build holds no lease seal")
			require.Nil(t, journal.ackSeal, "and no acknowledgement seal")
			first, err := journal.lease(ctx, leaseToken(1), "format1", req)
			require.NoError(t, err)

			p := filepath.Join(paths.Of(root).State, tc.file)
			tc.plant(t, p, journal)
			planted := readTestFile(t, p)
			require.True(t, isDeliverySealImage(planted), "the fixture is a valid v2 image")
			leaseBefore, ackBefore := readTestFile(t, journal.path), readTestFile(t, journal.ackPath)

			require.ErrorIs(t, tc.act(journal, first), core.ErrDegraded,
				"a format-1 check reads its sidecar with the v1 reader, which refuses a v2 document")
			require.Error(t, journalFault(journal))
			require.Equal(t, leaseBefore, readTestFile(t, journal.path), "nothing was appended")
			require.Equal(t, ackBefore, readTestFile(t, journal.ackPath))
			require.Equal(t, planted, readTestFile(t, p), "and the evidence is exactly as it was found")
		})
	}
}
