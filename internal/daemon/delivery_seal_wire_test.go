package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The tests for the journal's side of the v2 seal: the dual reader, the older-seal checkpoint, the
// open sequence and the downgrade. The seal as a unit is delivery_seal_test.go's subject; here it is
// wired to a real journal under a real lock.

// sealPrefixOf is the position sealing the first n complete lines of file, folded exactly as the
// loader folds them: the bytes, the count and the chain a seal written after those n lines carries.
func sealPrefixOf(t *testing.T, seed core.Hash, file []byte, n int) (int64, int, core.Hash) {
	t.Helper()
	off, chain := 0, seed
	for range n {
		end := bytes.IndexByte(file[off:], '\n')
		require.GreaterOrEqual(t, end, 0, "the journal holds fewer than %d complete lines", n)
		chain = deliveryChain(chain, file[off:off+end+1])
		off += end + 1
	}
	return int64(off), n, chain
}

// writeSealImage puts a two-slot v2 image at p: older in slot a (seq 1) and effective in slot b
// (seq 2), each summed for domain and seed. It refuses to build an image the strict reader would not
// select, so a test that means to be refused by the JOURNAL cannot be refused by the reader instead.
func writeSealImage(t *testing.T, p, domain string, seed core.Hash, older, effective sealRecord) {
	t.Helper()
	older.Seq, effective.Seq = 1, 2
	image, err := buildSealImage(&older, &effective, domain, seed)
	require.NoError(t, err)
	_, gotOlder, err := selectSeal(image, domain, seed)
	require.NoError(t, err, "guard: the reader must select this image, so only the journal can refuse it")
	require.NotNil(t, gotOlder)
	require.NoError(t, os.WriteFile(paths.Long(p), image, 0o600))
}

// T23 — design §6.2, §2.9. The strict reader checks that the older record seals strictly less than
// the effective one, but it cannot see the journal. load and loadAcks check the rest in the same
// scan: at older.Bytes the journal's count and chain must be the older record's. A record that seals
// a count or a chain the journal never had there, or a byte offset that falls inside no line, is not
// a prefix of this journal and the open refuses, leaving every byte as it was.
func TestDeliverySeal_OlderSlotMustSealAJournalPrefix(t *testing.T) {
	ctx := context.Background()
	req := testDeliveryRequest("t23")

	// leased returns a released project whose lease journal holds n lines, with the file's bytes.
	leased := func(t *testing.T, n int) (string, []byte) {
		t.Helper()
		root, lock, journal := newTestDeliveryJournal(t)
		for k := range n {
			_, err := journal.lease(ctx, leaseToken(k+1), "t23", req)
			require.NoError(t, err)
		}
		require.NoError(t, lock.Release())
		return root, readTestFile(t, journal.path)
	}

	t.Run("an older record that seals a real prefix opens", func(t *testing.T) {
		root, file := leased(t, 3)
		positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
		olderBytes, olderCount, olderChain := sealPrefixOf(t, deliveryChainSeed, file, 1)
		effBytes, effCount, effChain := sealPrefixOf(t, deliveryChainSeed, file, 3)
		writeSealImage(t, positionPath, deliveryChainDomain, deliveryChainSeed,
			sealRecord{Bytes: olderBytes, Count: olderCount, Chain: olderChain},
			sealRecord{Bytes: effBytes, Count: effCount, Chain: effChain})

		next, err := acquireTestDeliveryLock(root)
		require.NoError(t, err)
		t.Cleanup(func() { _ = next.Release() })
		recovered, err := next.openDeliveryJournal()
		require.NoError(t, err)
		require.Len(t, recovered.leases, 3)
		require.Equal(t, effBytes, recovered.bytes)
		require.Equal(t, file, readTestFile(t, recovered.path), "the journal itself is untouched")
	})

	for _, tc := range []struct {
		name  string
		older func(bytes int64, count int, chain core.Hash) sealRecord
	}{
		{"a count the journal never had at those bytes", func(b int64, c int, ch core.Hash) sealRecord {
			return sealRecord{Bytes: b, Count: c + 1, Chain: ch}
		}},
		{"a chain the journal never had at those bytes", func(b int64, c int, _ core.Hash) sealRecord {
			return sealRecord{Bytes: b, Count: c, Chain: testDeliveryRequest("t23 unrelated chain")}
		}},
		{"bytes that fall inside a line rather than after one", func(b int64, c int, ch core.Hash) sealRecord {
			return sealRecord{Bytes: b - 1, Count: c, Chain: ch}
		}},
		{"bytes of one boundary with the count of another", func(b int64, _ int, ch core.Hash) sealRecord {
			return sealRecord{Bytes: b, Count: 2, Chain: ch}
		}},
	} {
		t.Run(tc.name+" refuses", func(t *testing.T) {
			root, file := leased(t, 4)
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			olderBytes, olderCount, olderChain := sealPrefixOf(t, deliveryChainSeed, file, 1)
			effBytes, effCount, effChain := sealPrefixOf(t, deliveryChainSeed, file, 4)
			writeSealImage(t, positionPath, deliveryChainDomain, deliveryChainSeed,
				tc.older(olderBytes, olderCount, olderChain),
				sealRecord{Bytes: effBytes, Count: effCount, Chain: effChain})
			seal := readTestFile(t, positionPath)

			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			require.ErrorIs(t, err, core.ErrDegraded)
			require.Nil(t, got, "an older record that seals no prefix must not yield a writable journal")
			require.Equal(t, file, readTestFile(t, filepath.Join(paths.Of(root).State, deliveryLeaseFile)),
				"the journal is preserved")
			require.Equal(t, seal, readTestFile(t, positionPath), "and so is the seal that refused")
		})
	}

	t.Run("the acknowledgement journal checks its own older record", func(t *testing.T) {
		root, lock, journal := newTestDeliveryJournal(t)
		for k := range 3 {
			lease, err := journal.lease(ctx, leaseToken(k+1), "t23-ack", req)
			require.NoError(t, err)
			require.NoError(t, journal.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{}))
		}
		require.NoError(t, lock.Release())
		ackFile := readTestFile(t, journal.ackPath)
		olderBytes, olderCount, olderChain := sealPrefixOf(t, deliveryAckChainSeed, ackFile, 1)
		effBytes, effCount, effChain := sealPrefixOf(t, deliveryAckChainSeed, ackFile, 3)
		ackPosition := filepath.Join(paths.Of(root).State, deliveryAckPositionFile)
		writeSealImage(t, ackPosition, deliveryAckChainDomain, deliveryAckChainSeed,
			sealRecord{Bytes: olderBytes, Count: olderCount + 1, Chain: olderChain},
			sealRecord{Bytes: effBytes, Count: effCount, Chain: effChain})

		next, err := acquireTestDeliveryLock(root)
		require.NoError(t, err)
		t.Cleanup(func() { _ = next.Release() })
		got, err := next.openDeliveryJournal()
		require.ErrorIs(t, err, core.ErrDegraded)
		require.Nil(t, got)
		require.Equal(t, ackFile, readTestFile(t, journal.ackPath), "the acknowledgement journal is preserved")
	})
}
