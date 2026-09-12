package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// sealBenchChain is the chain every benchmark write seals. Only a position's bytes and entries must
// grow from one seal to the next, so one non-zero chain serves them all.
var sealBenchChain = sealTestChain("bench")

// newSealBench opens a lease seal over a fresh file in the benchmark's own directory, closed
// before that directory is removed.
func newSealBench(b *testing.B) *deliverySeal {
	b.Helper()
	fresh := sealRecord{Seq: 1, Chain: deliveryChainSeed}
	img, err := buildSealImage(&fresh, nil, deliveryChainDomain, deliveryChainSeed)
	if err != nil {
		b.Fatal(err)
	}
	p := filepath.Join(b.TempDir(), deliveryPositionFile)
	if err := os.WriteFile(p, img, 0o600); err != nil {
		b.Fatal(err)
	}
	s, err := openDeliverySeal(p, img, deliveryChainDomain, deliveryChainSeed)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.close() })
	return s
}

// BenchmarkDeliverySealWrite is one v2 seal write (SP20-D1 design §6.3): the slot's WriteAt, its
// SyncData and the post-seal identity check, which is all a lease batch's seal costs once step 2
// writes v2. Its counterpart is internal/paths' BenchmarkPathsWriteAtomic_4KB, the v1 seal's
// paths.WriteAtomic, and the two are meant to be read side by side. As evidence they need §6.3's
// run rules: timing rows alone, on a quiet machine on AC power, one process at a time.
//
// Each write seals one more byte and one more entry than the last, as consecutive seals must; a
// seal that reaches the journal's entry cap is replaced by a fresh one with the timer stopped.
func BenchmarkDeliverySealWrite(b *testing.B) {
	s := newSealBench(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s.cur.Count == deliveryLeaseMaxEntries {
			b.StopTimer()
			s = newSealBench(b)
			b.StartTimer()
		}
		if err := s.write(s.cur.Bytes+1, s.cur.Count+1, sealBenchChain); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeliverySealComponents times the v2 seal's pieces one at a time, for the attribution
// BenchmarkDeliveryLeaseComponents starts (design §6.3 names the step-2 rows it lacks). Its rows
// are:
//
//   - check: deliverySeal.check against an untouched file, the seal's part of a batch's check. It
//     is an Lstat, a SameFile, and a whole-file ReadAt compared with the cached image.
//   - slotWriteSync: one slot's WriteAt and SyncData. Each rewrites the current record over itself,
//     so the file's content never changes and the rewrite is the device cost alone.
//   - postSealIdentity: the identity check write makes after the sync, an Lstat and a SameFile.
func BenchmarkDeliverySealComponents(b *testing.B) {
	b.Run("check", func(b *testing.B) {
		s := newSealBench(b)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("slotWriteSync", func(b *testing.B) {
		s := newSealBench(b)
		slot := slotFor(s.seq)
		region := bytes.Clone(slot.region(s.image))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := s.writeSlot(slot, region); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		if err := s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain); err != nil {
			b.Fatal("the rewrites must leave the seal exactly as it was: ", err)
		}
	})
	b.Run("postSealIdentity", func(b *testing.B) {
		s := newSealBench(b)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := s.verifyIdentity(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
