package daemon

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The fuzz input's two flag bits: sealFuzzInSlot aims the patch at a slot region rather than
// anywhere in the file, and sealFuzzTruncate cuts the image at the patch's offset.
const (
	sealFuzzInSlot   = uint32(1) << 31
	sealFuzzTruncate = uint8(1) << 7
)

// sealFuzzOffset maps the fuzzer's offset onto the image: into one of the two slot regions when
// at carries sealFuzzInSlot, and anywhere in the file, or just past it, otherwise.
func sealFuzzOffset(at uint32) int {
	if at&sealFuzzInSlot == 0 {
		return int(at % (deliverySealFileSize + 1))
	}
	r := int((at &^ sealFuzzInSlot) % (2 * deliverySealSlotRegion))
	if r < deliverySealSlotRegion {
		return deliverySealSlotAOffset + r
	}
	return deliverySealSlotBOffset + r - deliverySealSlotRegion
}

// FuzzDeliverySealSelect mutates a valid two-slot image and asks selectSeal about it. It must never
// panic, and whatever it selects must be a record the strict reader's rules allow: a sum that
// verifies for its slot and journal, a seq whose parity names the slot it was found in, and beside
// it either an empty slot b under a seq-1 record, or a valid older record exactly one seq behind
// that seals strictly less.
func FuzzDeliverySealSelect(f *testing.F) {
	j := sealTestLease
	fresh := j.fresh()
	var bases [][]byte
	for _, ab := range [][2]*sealRecord{
		{&fresh, nil},
		{sealTestRecordPtr(1), sealTestRecordPtr(2)},
		{sealTestRecordPtr(3), sealTestRecordPtr(2)},
	} {
		img, err := buildSealImage(ab[0], ab[1], j.domain, j.seed)
		if err != nil {
			f.Fatal(err)
		}
		bases = append(bases, img)
	}

	f.Add(uint8(0), uint32(0), []byte{})
	f.Add(uint8(1), uint32(0), []byte{})
	f.Add(uint8(2), uint32(0), []byte{})
	f.Add(uint8(2), sealFuzzInSlot|7, []byte("9"))
	f.Add(uint8(1), sealFuzzInSlot|uint32(deliverySealSlotRegion+40), []byte(`"`))
	f.Add(uint8(2), sealFuzzInSlot|100, bytes.Repeat([]byte{' '}, 64))
	f.Add(uint8(0), sealFuzzInSlot|uint32(deliverySealSlotRegion), []byte(`{"seq":2,`))
	f.Add(uint8(1), uint32(deliverySealStride), []byte(`,"a":`))
	f.Add(uint8(2), uint32(deliverySealFileSize-1), []byte("}}"))
	f.Add(sealFuzzTruncate|2, uint32(deliverySealFileSize-1), []byte{})

	f.Fuzz(func(t *testing.T, base uint8, at uint32, patch []byte) {
		img := bytes.Clone(bases[int(base&^sealFuzzTruncate)%len(bases)])
		off := sealFuzzOffset(at)
		for i, c := range patch {
			if off+i < len(img) {
				img[off+i] = c
			} else {
				img = append(img, c)
			}
		}
		if base&sealFuzzTruncate != 0 && off < len(img) {
			img = img[:off]
		}

		eff, older, err := selectSeal(img, j.domain, j.seed)
		if err != nil {
			require.ErrorIs(t, err, core.ErrDegraded)
			require.Zero(t, eff)
			require.Nil(t, older)
			return
		}
		sealFuzzRequireAllowed(t, j, img, eff, "effective")
		if older == nil {
			require.Equal(t, uint64(1), eff.Seq, "only a seq-1 record stands alone")
			_, state := classifySlot(sealSlotB.region(img), sealSlotB, j.domain, j.seed)
			require.Equal(t, sealSlotEmpty, state, "and only beside an empty slot b")
			return
		}
		sealFuzzRequireAllowed(t, j, img, *older, "older")
		require.Equal(t, eff.Seq-1, older.Seq)
		require.Less(t, older.Bytes, eff.Bytes)
		require.Less(t, older.Count, eff.Count)
	})
}

// sealFuzzRequireAllowed asserts that rec, which selectSeal returned from img, is a record the
// strict reader allows. Its canonical JSON starts the region of the slot its seq's parity names
// (odd in a, even in b), found by offset rather than through slotFor; its sum is the one sealSum
// gives for that slot in j; and its position is in j's bounds.
func sealFuzzRequireAllowed(t *testing.T, j sealTestJournal, img []byte, rec sealRecord, which string) {
	t.Helper()
	require.GreaterOrEqual(t, rec.Seq, uint64(1), which)
	slot, off := sealSlotB, deliverySealSlotBOffset
	if rec.Seq%2 == 1 {
		slot, off = sealSlotA, deliverySealSlotAOffset
	}
	body, err := json.Marshal(rec)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(img[off:], append(body, deliverySealPad)), "%s: its parity's slot holds it", which)
	require.Equal(t, sealSum(j.domain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain), rec.Sum, "%s: its sum verifies", which)
	require.True(t, sealInBounds(rec.Bytes, rec.Count, rec.Chain, j.seed), "%s: its position is in bounds", which)
}
