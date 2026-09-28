package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// radixMergeHash returns a key-hash seam for the merge tests. Mode 0 is the production digest; the
// others cluster the digests so the tree has the shapes a uniform digest almost never produces at test
// sizes — long compressed prefixes, branches re-based when a key diverges inside one, and leaves that
// split deep in the tree.
func radixMergeHash(r *deliveryRadix, mode int) func([]byte) radixHash {
	return func(k []byte) radixHash {
		h := r.defaultKeyHash(k)
		switch mode {
		case 1: // share 160 bits, then a few random ones
			for i := 0; i < 20; i++ {
				h[i] = 0
			}
			h[20] &= 0x0f
		case 2: // a random first byte, then 24 zero bytes: long skips below depth 8
			for i := 1; i < 25; i++ {
				h[i] = 0
			}
		case 3: // four clusters, each with its own long shared prefix
			c := h[0] & 0xc0
			for i := 0; i < 18; i++ {
				h[i] = 0
			}
			h[0] = c
			h[9] = c >> 2
		}
		return h
	}
}

// mergeDecision is a pure decision the merge tests apply both ways.
type mergeDecision struct {
	name   string
	decide func(value []byte) radixDecide
}

var mergeDecisions = []mergeDecision{
	{"keep-or-write", keepOrWrite},
	{"overwrite", func(v []byte) radixDecide {
		return func(old []byte, found bool) ([]byte, bool, error) {
			if found && bytes.Equal(old, v) {
				return nil, false, nil
			}
			return v, true, nil
		}
	}},
	{"only-if-absent", func(v []byte) radixDecide {
		return func(_ []byte, found bool) ([]byte, bool, error) { return v, !found, nil }
	}},
	{"only-if-present", func(v []byte) radixDecide {
		return func(old []byte, found bool) ([]byte, bool, error) {
			if !found {
				return nil, false, nil
			}
			return append(append([]byte(nil), old...), v...), true, nil
		}
	}},
	{"never", func([]byte) radixDecide {
		return func([]byte, bool) ([]byte, bool, error) { return nil, false, nil }
	}},
}

// TestDeliveryRadix_MergeEqualsSequentialUpdates is merge's contract: over a tree with committed and
// held pages, a sorted batch of decisions applied by merge yields exactly the root that applying them
// one at a time with update yields, in a shuffled order, and every key then reads back as the
// decisions say. It runs every digest shape radixMergeHash offers, batches that add, replace, keep and
// skip, and a batch split into several sorted merges.
func TestDeliveryRadix_MergeEqualsSequentialUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for seed := int64(1); seed <= 48; seed++ {
		mode := int(seed % 4)
		t.Run(fmt.Sprintf("seed%02d-mode%d", seed, mode), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			r := newTestRadix(t)
			r.hashKey = radixMergeHash(r, mode)
			want := map[string][]byte{}

			// A base tree built in several transactions: some pages committed, the last batch held.
			base := radixHash{}
			tx := r.begin()
			nBase := rng.Intn(400)
			for i := 0; i < nBase; i++ {
				k := []byte(fmt.Sprintf("base-%d-%d", seed, i))
				v := []byte(fmt.Sprintf("v%d", rng.Intn(5)))
				next, err := tx.insert(ctx, base, k, v)
				require.NoError(t, err)
				base = next
				want[string(k)] = v
				if rng.Intn(60) == 0 {
					require.NoError(t, tx.commit(ctx, base))
					tx = r.begin()
				}
			}
			require.NoError(t, tx.commit(ctx, base))

			// The batch: existing keys and new ones, each with a decision.
			type planned struct {
				key []byte
				d   mergeDecision
				v   []byte
			}
			var batch []planned
			seen := map[string]bool{}
			for k := range want {
				if rng.Intn(3) == 0 {
					batch = append(batch, planned{key: []byte(k)})
					seen[k] = true
				}
			}
			for i, n := 0, rng.Intn(500); i < n; i++ {
				k := fmt.Sprintf("new-%d-%d", seed, i)
				if !seen[k] {
					batch = append(batch, planned{key: []byte(k)})
					seen[k] = true
				}
			}
			for i := range batch {
				batch[i].d = mergeDecisions[rng.Intn(len(mergeDecisions))]
				batch[i].v = []byte(fmt.Sprintf("w%d", rng.Intn(4)))
				if old, found := want[string(batch[i].key)]; found && batch[i].d.name == "keep-or-write" {
					batch[i].v = old // keep-or-write refuses a different value; that refusal is tested below
				}
			}

			// One at a time, shuffled, through update.
			seq := r.begin()
			seqRoot := base
			order := rng.Perm(len(batch))
			for _, i := range order {
				p := batch[i]
				next, err := seq.update(ctx, seqRoot, p.key, p.d.decide(p.v))
				require.NoError(t, err)
				seqRoot = next
			}

			// Sorted, through merge — as one batch and as several.
			ops := make([]radixOp, len(batch))
			for i, p := range batch {
				ops[i] = radixOp{target: r.hashKey(p.key), key: p.key, decide: p.d.decide(p.v)}
			}
			sort.Slice(ops, func(a, b int) bool { return bytes.Compare(ops[a].target[:], ops[b].target[:]) < 0 })
			one := r.begin()
			oneRoot, err := one.merge(ctx, base, ops)
			require.NoError(t, err)
			require.Equal(t, seqRoot, oneRoot, "merge must reach the root sequential updates reach")

			chunked := r.begin()
			chunkRoot := base
			for rest := ops; len(rest) > 0; {
				n := 1 + rng.Intn(len(rest))
				next, err := chunked.merge(ctx, chunkRoot, rest[:n])
				require.NoError(t, err)
				chunkRoot, rest = next, rest[n:]
			}
			require.Equal(t, seqRoot, chunkRoot, "a batch merged in sorted pieces reaches the same root")

			// The merged tree commits and reads back exactly what the decisions stored.
			require.NoError(t, one.commit(ctx, oneRoot))
			for _, p := range batch {
				old, found := want[string(p.key)]
				v, write, err := p.d.decide(p.v)(old, found)
				require.NoError(t, err)
				if write {
					want[string(p.key)] = v
				}
			}
			for k, v := range want {
				got, found, err := r.lookup(ctx, oneRoot, []byte(k))
				require.NoError(t, err)
				require.True(t, found, "key %q", k)
				require.Equal(t, v, got, "key %q", k)
			}
			for _, p := range batch {
				if _, stored := want[string(p.key)]; stored {
					continue
				}
				_, found, err := r.lookup(ctx, oneRoot, p.key)
				require.NoError(t, err)
				require.False(t, found, "a key no decision wrote stays absent: %q", p.key)
			}
		})
	}
}

// TestDeliveryRadix_MergeRefusesWhatUpdateRefuses: a digest collision (with a resident key or inside
// the batch), a decision's error, an oversized value and unsorted input are refused, and a refusal
// leaves the committed tree as it was.
func TestDeliveryRadix_MergeRefusesWhatUpdateRefuses(t *testing.T) {
	ctx := context.Background()
	r := newTestRadix(t)
	shared := sha256.Sum256([]byte("shared"))
	r.hashKey = func(k []byte) radixHash {
		if bytes.HasPrefix(k, []byte("clash-")) {
			return shared
		}
		return r.defaultKeyHash(k)
	}
	root := mustInsert(t, r, radixHash{}, []byte("clash-resident"), []byte("v"))
	root = mustInsert(t, r, root, []byte("other"), []byte("v"))
	op := func(k string, d radixDecide) radixOp {
		return radixOp{target: r.hashKey([]byte(k)), key: []byte(k), decide: d}
	}
	write := func(v string) radixDecide { return keepOrWrite([]byte(v)) }

	_, err := r.begin().merge(ctx, root, []radixOp{op("clash-intruder", write("x"))})
	require.ErrorIs(t, err, errRadixCollision, "a digest a resident key owns is a collision")

	a, b := op("clash-a", write("x")), op("clash-b", write("y"))
	_, err = r.begin().merge(ctx, radixHash{}, []radixOp{a, b})
	require.ErrorIs(t, err, errRadixCollision, "two batch keys with one digest are a collision")

	boom := errors.New("decision refused")
	_, err = r.begin().merge(ctx, root, []radixOp{op("other", func([]byte, bool) ([]byte, bool, error) { return nil, false, boom })})
	require.ErrorIs(t, err, boom)

	_, err = r.begin().merge(ctx, root, []radixOp{op("big", write(string(make([]byte, radixMaxValueBytes+1))))})
	require.ErrorIs(t, err, errRadixTooLarge)

	x, y := op("k1", write("1")), op("k2", write("2"))
	if bytes.Compare(x.target[:], y.target[:]) < 0 {
		x, y = y, x
	}
	_, err = r.begin().merge(ctx, root, []radixOp{x, y})
	require.ErrorIs(t, err, errRadixMergeOrder)

	got, found, err := r.lookup(ctx, root, []byte("clash-resident"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v"), got, "no refused merge moved the committed tree")
}
