package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// sealTestJournal is one journal's seal identity: its chain domain and the chain it starts from.
type sealTestJournal struct {
	name   string
	domain string
	seed   core.Hash
}

var (
	sealTestLease    = sealTestJournal{name: "lease", domain: deliveryChainDomain, seed: deliveryChainSeed}
	sealTestAck      = sealTestJournal{name: "ack", domain: deliveryAckChainDomain, seed: deliveryAckChainSeed}
	sealTestJournals = []sealTestJournal{sealTestLease, sealTestAck}
)

// sealTestChain is a stand-in chain digest for a journal that is not empty.
func sealTestChain(label string) core.Hash {
	return core.HashBytes("qompack.delivery.seal.test.v1", []byte(label))
}

// sealTestRecord is the unsummed record the tests seal as seq: a non-empty position that grows with
// seq, so that any two of them are ordered as consecutive seals are.
func sealTestRecord(seq uint64) sealRecord {
	return sealRecord{Seq: seq, Bytes: int64(seq) * 1000, Count: int(seq) * 3, Chain: sealTestChain(strconv.FormatUint(seq, 10))}
}

// sealTestRecordPtr is sealTestRecord's address, for the image builders' nil-able slots.
func sealTestRecordPtr(seq uint64) *sealRecord {
	rec := sealTestRecord(seq)
	return &rec
}

// fresh is a fresh journal's first record: seq 1 over the empty position.
func (j sealTestJournal) fresh() sealRecord { return sealRecord{Seq: 1, Chain: j.seed} }

// summed is rec with the sum slot gives it in j.
func (j sealTestJournal) summed(rec sealRecord, slot sealSlot) sealRecord {
	rec.Sum = sealSum(j.domain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain)
	return rec
}

// image is buildSealImage for j, which must succeed.
func (j sealTestJournal) image(t *testing.T, a, b *sealRecord) []byte {
	t.Helper()
	img, err := buildSealImage(a, b, j.domain, j.seed)
	require.NoError(t, err)
	return img
}

// open writes img as a seal file in a fresh directory and opens a seal over it, closed when the
// test ends. It returns the file's path and the seal.
func (j sealTestJournal) open(t *testing.T, img []byte) (string, *deliverySeal) {
	t.Helper()
	p := sealTestFile(t, img)
	s, err := openDeliverySeal(p, img, j.domain, j.seed)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close() })
	return p, s
}

// requireRefused asserts that selectSeal refuses img for j and returns no record at all.
func (j sealTestJournal) requireRefused(t *testing.T, img []byte, msgAndArgs ...any) {
	t.Helper()
	eff, older, err := selectSeal(img, j.domain, j.seed)
	require.ErrorIs(t, err, core.ErrDegraded, msgAndArgs...)
	require.Zero(t, eff, msgAndArgs...)
	require.Nil(t, older, msgAndArgs...)
}

// requireSelects asserts that selectSeal selects eff, beside older, from img for j.
func (j sealTestJournal) requireSelects(t *testing.T, img []byte, eff sealRecord, older *sealRecord, msgAndArgs ...any) {
	t.Helper()
	gotEff, gotOlder, err := selectSeal(img, j.domain, j.seed)
	require.NoError(t, err, msgAndArgs...)
	require.Equal(t, eff, gotEff, msgAndArgs...)
	require.Equal(t, older, gotOlder, msgAndArgs...)
}

// sealRawRegion is rec as a slot region, carrying the sum sumSlot and domain give it, built with
// none of encodeSlot's checks: how the tests build records the reader must refuse.
func sealRawRegion(t *testing.T, rec sealRecord, sumSlot sealSlot, domain string) []byte {
	t.Helper()
	rec.Sum = sealSum(domain, sumSlot, rec.Seq, rec.Bytes, rec.Count, rec.Chain)
	body, err := json.Marshal(rec)
	require.NoError(t, err)
	region := bytes.Repeat([]byte{deliverySealPad}, deliverySealSlotRegion)
	copy(region, body)
	return region
}

// withRegion is a copy of img whose slot holds region.
func withRegion(img []byte, slot sealSlot, region []byte) []byte {
	out := bytes.Clone(img)
	copy(out[slot.offset():slot.offset()+deliverySealSlotRegion], region)
	return out
}

// withByte is a copy of img whose byte at off is c.
func withByte(img []byte, off int, c byte) []byte {
	out := bytes.Clone(img)
	out[off] = c
	return out
}

// sealChainDigit is the offset, within img, of the first hex digit of the chain in slot's record,
// and a different hex digit to put there: a change that keeps the record canonical JSON.
func sealChainDigit(t *testing.T, img []byte, slot sealSlot) (int, byte) {
	t.Helper()
	const key = `"chain":"sha256:`
	i := bytes.Index(slot.region(img), []byte(key))
	require.GreaterOrEqual(t, i, 0, "slot %c holds no record", slot)
	off := slot.offset() + i + len(key)
	if img[off] == '0' {
		return off, '1'
	}
	return off, '0'
}

// sealTestFile writes img as a seal file in a fresh directory and returns its path.
func sealTestFile(t *testing.T, img []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), deliveryPositionFile)
	require.NoError(t, os.WriteFile(p, img, 0o600))
	return p
}

// readSealFile is the file's bytes.
func readSealFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return b
}

// patchSealFile overwrites the byte at off of the file at p with c, through a handle of its own.
func patchSealFile(t *testing.T, p string, off int, c byte) {
	t.Helper()
	f, err := paths.OpenSharedRW(p)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte{c}, int64(off))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// TestDeliverySeal_V2IsOneJSONDocumentAndTheOldReaderRefusesIt is design T21. Every image the seal
// code produces is one valid JSON document whose "v" is deliverySealVersion: fresh, after one, two
// and three writes, and converted from a v1 position. Today's v1 reader, loadDeliveryPosition,
// refuses every one of them, and on the version: fail-closed, never a misread. The layout's offsets,
// sectors, blocks and pages are pinned as numbers, independently of the constants they derive from.
func TestDeliverySeal_V2IsOneJSONDocumentAndTheOldReaderRefusesIt(t *testing.T) {
	t.Run("layout", func(t *testing.T) {
		const sector, block, page = 512, 4096, 16384
		require.Equal(t, 32768, deliverySealFileSize)
		require.Equal(t, 16384, deliverySealStride)
		require.Equal(t, 480, deliverySealSlotRegion)
		require.Equal(t, 11, deliverySealSlotAOffset)
		require.Equal(t, 16389, deliverySealSlotBOffset)

		img := sealImageTemplate()
		require.Len(t, img, 32768)
		require.Equal(t, `{"v":2,"a":`, string(img[0:11]))
		require.Equal(t, "null", string(img[11:15]))
		require.True(t, sealPadded(img[15:16384]), "slot a's padding, then static spaces, up to 16384")
		require.Equal(t, `,"b":`, string(img[16384:16389]))
		require.Equal(t, "null", string(img[16389:16393]))
		require.True(t, sealPadded(img[16393:32767]), "slot b's padding, then static spaces, up to the last byte")
		require.Equal(t, byte('}'), img[32767])

		aFirst, aLast := 11, 11+480-1
		bFirst, bLast := 16389, 16389+480-1
		require.Equal(t, []int{0, 0}, []int{aFirst / sector, aLast / sector}, "slot a lies in sector 0")
		require.Equal(t, []int{32, 32}, []int{bFirst / sector, bLast / sector}, "slot b lies in sector 32")
		require.Equal(t, []int{0, 0}, []int{aFirst / block, aLast / block}, "slot a lies in 4 KiB block 0")
		require.Equal(t, []int{4, 4}, []int{bFirst / block, bLast / block}, "slot b lies in 4 KiB block 4")
		require.NotEqual(t, aFirst/page, bFirst/page, "the slots lie in different 16 KiB pages")
		require.Equal(t, aLast/page, aFirst/page)
		require.Equal(t, bLast/page, bFirst/page)
	})

	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			fresh := j.fresh()
			images := map[string][]byte{}
			p, s := j.open(t, j.image(t, &fresh, nil))
			images["fresh"] = readSealFile(t, p)
			for seq := uint64(2); seq <= 4; seq++ {
				rec := sealTestRecord(seq)
				require.NoError(t, s.write(rec.Bytes, rec.Count, rec.Chain))
				images["after write "+strconv.FormatUint(seq-1, 10)] = readSealFile(t, p)
			}

			v1 := filepath.Join(t.TempDir(), "v1.json")
			const convertedBytes, convertedCount = 4096, 7
			require.NoError(t, writeDeliveryPositionV1(v1, convertedBytes, convertedCount, sealTestChain("converted")))
			position, err := loadDeliveryPosition(v1, j.seed)
			require.NoError(t, err)
			converted, err := newSealImage(position.Bytes, position.Count, position.Chain, j.domain, j.seed)
			require.NoError(t, err)
			images["converted from v1"] = converted
			require.Len(t, images, 5)

			for name, img := range images {
				require.True(t, json.Valid(img), "%s: one valid JSON document", name)
				var doc struct {
					V int             `json:"v"`
					A json.RawMessage `json:"a"`
					B json.RawMessage `json:"b"`
				}
				require.NoError(t, json.Unmarshal(img, &doc), name)
				require.Equal(t, deliverySealVersion, doc.V, name)
				require.NotEmpty(t, doc.A, name)
				require.NotEmpty(t, doc.B, name)

				_, err := loadDeliveryPosition(sealTestFile(t, img), j.seed)
				require.ErrorIs(t, err, core.ErrDegraded, "%s: today's reader must refuse a v2 file", name)
				var old deliveryPosition
				require.NoError(t, json.Unmarshal(img, &old))
				require.NotEqual(t, core.EvidenceVersion, old.Version, "%s: it refuses on the version", name)
			}
		})
	}
}

// TestDeliverySeal_StrictSelectionTable is design T22: every row of §2.9's selection table, as a
// pure function over the file's bytes. Only a valid seq-1 record beside an empty slot b, or two
// valid records one seq apart whose older one seals strictly less, is accepted; every other state
// refuses, returns no record at all, and never falls back to the other slot. An open that refuses
// leaves the file's bytes exactly as they were.
func TestDeliverySeal_StrictSelectionTable(t *testing.T) {
	j := sealTestLease
	fresh := j.fresh()
	r := sealTestRecordPtr
	sumA := func(rec *sealRecord) *sealRecord { s := j.summed(*rec, sealSlotA); return &s }
	sumB := func(rec *sealRecord) *sealRecord { s := j.summed(*rec, sealSlotB); return &s }

	t.Run("accepted", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			a, b       *sealRecord
			eff, older *sealRecord
		}{
			{"a fresh seq 1 beside an empty b", &fresh, nil, sumA(&fresh), nil},
			{"a converted seq 1 beside an empty b", r(1), nil, sumA(r(1)), nil},
			{"a seq 1 and b seq 2: b is effective", r(1), r(2), sumB(r(2)), sumA(r(1))},
			{"a fresh seq 1 and b seq 2", &fresh, r(2), sumB(r(2)), sumA(&fresh)},
			{"a seq 3 and b seq 2: a is effective", r(3), r(2), sumA(r(3)), sumB(r(2))},
			{"far along: a seq 1001 and b seq 1000", r(1001), r(1000), sumA(r(1001)), sumB(r(1000))},
		} {
			t.Run(tc.name, func(t *testing.T) {
				j.requireSelects(t, j.image(t, tc.a, tc.b), *tc.eff, tc.older)
			})
		}
	})

	// steady is the ordinary state: a holds the newest record (seq 3), b the older (seq 2), and the
	// next write, seq 4, goes to b.
	steady := j.image(t, r(3), r(2))
	next, _, err := encodeSlot(*r(4), sealSlotB, j.domain, j.seed)
	require.NoError(t, err)
	nextLen := bytes.IndexByte(next, deliverySealPad)
	old := sealSlotB.region(steady)
	// The two records share their first bytes ({"seq":), so a tear inside those leaves the old
	// record whole. firstDiff is the first byte at which a tear shows.
	firstDiff := 0
	for next[firstDiff] == old[firstDiff] {
		firstDiff++
	}
	torn := func(n int) []byte {
		return withRegion(steady, sealSlotB, append(bytes.Clone(next[:n]), old[n:]...))
	}
	rng := rand.New(rand.NewPCG(20, 1))
	garbage := make([]byte, deliverySealSlotRegion)
	for i := range garbage {
		garbage[i] = byte(rng.UintN(256))
	}
	bodyA := bytes.IndexByte(sealSlotA.region(steady), deliverySealPad)
	bodyB := bytes.IndexByte(sealSlotB.region(steady), deliverySealPad)
	rotOff, rotDigit := sealChainDigit(t, steady, sealSlotA)
	pair := j.image(t, r(1), r(2))
	emptyRegion := sealSlotB.region(sealImageTemplate())
	ackFresh := sealTestAck.fresh()
	v1, err := encodeDeliveryPositionV1(3000, 9, r(3).Chain)
	require.NoError(t, err)
	// canonA is seq 1's canonical record for slot a. The non-canonical rows below spell it in ways
	// json.Unmarshal still reads back as the very same record, sum included, so that only the
	// canonical-form check can refuse them; onlyA puts a body in slot a beside an empty b.
	summedA := j.summed(*r(1), sealSlotA)
	canonA, err := json.Marshal(summedA)
	require.NoError(t, err)
	onlyA := func(body []byte) []byte {
		region := bytes.Repeat([]byte{deliverySealPad}, deliverySealSlotRegion)
		copy(region, body)
		return withRegion(sealImageTemplate(), sealSlotA, region)
	}
	j.requireSelects(t, onlyA(canonA), summedA, nil, "guard: the canonical spelling selects")
	reordered, err := json.Marshal(map[string]any{
		"seq": summedA.Seq, "bytes": summedA.Bytes, "count": summedA.Count, "chain": summedA.Chain, "sum": summedA.Sum,
	})
	require.NoError(t, err)
	position := func(seq uint64, size int64, count int) *sealRecord {
		return &sealRecord{Seq: seq, Bytes: size, Count: count, Chain: sealTestChain("position " + strconv.FormatUint(seq, 10))}
	}

	refused := []struct {
		name string
		img  []byte
	}{
		{"a torn slot: a new record's first differing byte over the old one", torn(firstDiff + 1)},
		{"a torn slot: half a new record over the old one", torn(nextLen / 2)},
		{"a torn slot: all but the last byte of a new record", torn(nextLen - 1)},
		{"a slot of random garbage", withRegion(steady, sealSlotB, garbage)},
		{"a slot of zero bytes, as a hole reads", withRegion(steady, sealSlotB, make([]byte, deliverySealSlotRegion))},
		{"an empty slot beside seq 3", j.image(t, r(3), nil)},
		{"an empty a beside a valid b", j.image(t, nil, r(2))},
		{"both slots empty", sealImageTemplate()},
		{"a seq gap: 1 and 4", j.image(t, r(1), r(4))},
		{"a seq gap: 5 and 2", j.image(t, r(5), r(2))},
		{"a seq gap: 3 and 6", j.image(t, r(3), r(6))},
		{
			"parity: seq 2 in a, summed for a, beside seq 3 in b, summed for b",
			withRegion(withRegion(sealImageTemplate(), sealSlotA, sealRawRegion(t, *r(2), sealSlotA, j.domain)),
				sealSlotB, sealRawRegion(t, *r(3), sealSlotB, j.domain)),
		},
		{
			"parity: seq 2 in a, summed for a, beside an empty b",
			withRegion(sealImageTemplate(), sealSlotA, sealRawRegion(t, *r(2), sealSlotA, j.domain)),
		},
		{
			"parity: seq 1 in b, summed for b, beside seq 2 in a, summed for a",
			withRegion(withRegion(sealImageTemplate(), sealSlotA, sealRawRegion(t, *r(2), sealSlotA, j.domain)),
				sealSlotB, sealRawRegion(t, *r(1), sealSlotB, j.domain)),
		},
		{"slot b's record copied into slot a", withRegion(pair, sealSlotA, sealSlotB.region(pair))},
		{"slot a's record copied into slot b", withRegion(pair, sealSlotB, sealSlotA.region(pair))},
		{
			"a seq-1 record in a whose sum is for slot b",
			withRegion(sealImageTemplate(), sealSlotA, sealRawRegion(t, *r(1), sealSlotB, j.domain)),
		},
		{"a non-canonical record: its chain spelled as bare hex", onlyA(bytes.Replace(canonA, []byte(`"chain":"sha256:`), []byte(`"chain":"`), 1))},
		{"a non-canonical record: its keys reordered", onlyA(reordered)},
		{"a non-canonical record: a duplicated key", onlyA(append([]byte(`{"seq":1,`), canonA[1:]...))},
		{"a non-canonical record: a key in upper case", onlyA(bytes.Replace(canonA, []byte(`"seq"`), []byte(`"SEQ"`), 1))},
		{"a non-canonical record: an unknown key", onlyA(append([]byte(`{"x":0,`), canonA[1:]...))},
		{"records from the ack journal", sealTestAck.image(t, r(1), r(2))},
		{"the ack journal's fresh file", sealTestAck.image(t, &ackFresh, nil)},
		{"an older record with as many bytes", j.image(t, position(1, 500, 5), position(2, 500, 6))},
		{"an older record with as many entries", j.image(t, position(1, 500, 5), position(2, 600, 5))},
		{"an older record that seals more", j.image(t, position(3, 500, 5), position(2, 600, 6))},
		{"rot of the newest slot: one hex digit of its chain", withByte(steady, rotOff, rotDigit)},
		{"rot of the newest slot: its seq digit", withByte(steady, sealSlotA.offset()+len(`{"seq":`), '5')},
		{"rot of the newest slot: its record replaced by null", withRegion(steady, sealSlotA, emptyRegion)},
		{"rot of the newest slot: a torn record", withRegion(steady, sealSlotA, append(bytes.Clone(next[:nextLen/2]),
			sealSlotA.region(steady)[nextLen/2:]...))},
		{"a static byte: the document's first", withByte(steady, 0, 'x')},
		{"a static byte: the version", withByte(steady, len(`{"v":`), '3')},
		{"a static byte: the padding right after slot a", withByte(steady, deliverySealSlotAOffset+deliverySealSlotRegion, 'x')},
		{"a static byte: the last before the second stride", withByte(steady, deliverySealStride-1, 'x')},
		{"a static byte: slot b's prefix", withByte(steady, deliverySealStride+1, 'x')},
		{"a static byte: the padding right after slot b", withByte(steady, deliverySealSlotBOffset+deliverySealSlotRegion, 'x')},
		{"a static byte: the closing brace", withByte(steady, deliverySealFileSize-1, ' ')},
		{"padding: a byte right after slot a's record", withByte(steady, deliverySealSlotAOffset+bodyA, 'x')},
		{"padding: slot a's last byte", withByte(steady, deliverySealSlotAOffset+deliverySealSlotRegion-1, 'x')},
		{"padding: a NUL after slot b's record", withByte(steady, deliverySealSlotBOffset+bodyB+7, 0)},
		{"padding: a newline after slot b's record", withByte(steady, deliverySealSlotBOffset+bodyB+1, '\n')},
		{"padding: a space before slot a's record", withRegion(steady, sealSlotA, append([]byte{' '}, sealSlotA.region(steady)[:deliverySealSlotRegion-1]...))},
		{"the wrong size: one byte short", bytes.Clone(steady[:deliverySealFileSize-1])},
		{"the wrong size: one byte long", append(bytes.Clone(steady), ' ')},
		{"the wrong size: empty", []byte{}},
		{"a v1 position file", v1},
	}
	// And static bytes picked at random, outside both slot regions.
	for len(refused) < 64 {
		off := int(rng.UintN(deliverySealFileSize))
		inA := off >= deliverySealSlotAOffset && off < deliverySealSlotAOffset+deliverySealSlotRegion
		inB := off >= deliverySealSlotBOffset && off < deliverySealSlotBOffset+deliverySealSlotRegion
		if inA || inB {
			continue
		}
		refused = append(refused, struct {
			name string
			img  []byte
		}{"a static byte at offset " + strconv.Itoa(off), withByte(steady, off, steady[off]^0x01)})
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			j.requireRefused(t, tc.img)
			p := sealTestFile(t, tc.img)
			s, err := openDeliverySeal(p, tc.img, j.domain, j.seed)
			require.ErrorIs(t, err, core.ErrDegraded)
			require.Nil(t, s)
			require.True(t, bytes.Equal(tc.img, readSealFile(t, p)), "a refused open must leave the file exactly as it was")
			requireNoOpenHandle(t, p)
		})
	}
}

// TestDeliverySeal_WritesAlternateSlotsAndTheFileIsTheImage pins write's effect on disk. Each write
// changes exactly one slot, alternately b, a, b, ..., by seq parity, and leaves the other untouched.
// After every write the file is byte for byte the cached image and selects the new record beside
// its predecessor, and check passes against the new position.
func TestDeliverySeal_WritesAlternateSlotsAndTheFileIsTheImage(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			fresh := j.fresh()
			p, s := j.open(t, j.image(t, &fresh, nil))
			prev := j.summed(fresh, sealSlotA)
			var written []sealSlot
			for seq := uint64(2); seq <= 7; seq++ {
				before := readSealFile(t, p)
				want := sealTestRecord(seq)
				require.NoError(t, s.write(want.Bytes, want.Count, want.Chain))
				after := readSealFile(t, p)
				require.Equal(t, after, s.image, "the file must be the cached image after every write")

				aChanged := !bytes.Equal(sealSlotA.region(before), sealSlotA.region(after))
				bChanged := !bytes.Equal(sealSlotB.region(before), sealSlotB.region(after))
				require.NotEqual(t, aChanged, bChanged, "exactly one slot changes per write")
				slot := sealSlotB
				if aChanged {
					slot = sealSlotA
				}
				written = append(written, slot)
				require.Equal(t, withRegion(before, slot, slot.region(after)), after, "nothing outside the slot changes")

				require.Equal(t, seq, s.seq)
				require.Equal(t, j.summed(want, slot), s.cur)
				j.requireSelects(t, after, s.cur, &prev)
				require.NoError(t, s.check(want.Bytes, want.Count, want.Chain))
				prev = s.cur
			}
			require.Equal(t, []sealSlot{sealSlotB, sealSlotA, sealSlotB, sealSlotA, sealSlotB, sealSlotA}, written)
		})
	}
}

// TestDeliverySeal_EveryCrashReachableImageSelects is design §3 row 7 (and row 15, the same write
// at open). A crash during a write leaves the target slot holding its old record, the new one, or,
// on a device that is not block-atomic, a mix of the two. For every write: the image whose target
// slot still holds the old record selects the previous record, the new image selects the new one,
// and every torn mix of the target slot refuses, never a misread.
func TestDeliverySeal_EveryCrashReachableImageSelects(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			fresh := j.fresh()
			p, s := j.open(t, j.image(t, &fresh, nil))
			for seq := uint64(2); seq <= 5; seq++ {
				prevImg := bytes.Clone(s.image)
				prev, prevOlder, err := selectSeal(prevImg, j.domain, j.seed)
				require.NoError(t, err)
				require.Equal(t, s.cur, prev)

				want := sealTestRecord(seq)
				require.NoError(t, s.write(want.Bytes, want.Count, want.Chain))
				newImg := readSealFile(t, p)
				j.requireSelects(t, newImg, s.cur, &prev, "the new image selects the new record")

				slot := slotFor(seq)
				oldRegion, newRegion := slot.region(prevImg), slot.region(newImg)
				require.Equal(t, withRegion(newImg, slot, oldRegion), prevImg, "guard: only the target slot differs")
				for n := 1; n < deliverySealSlotRegion; n++ {
					mix := append(bytes.Clone(newRegion[:n]), oldRegion[n:]...)
					img := withRegion(prevImg, slot, mix)
					switch {
					case bytes.Equal(mix, oldRegion):
						j.requireSelects(t, img, prev, prevOlder, "the old record, cut after %d bytes", n)
					case bytes.Equal(mix, newRegion):
						j.requireSelects(t, img, s.cur, &prev, "the new record, cut after %d bytes", n)
					default:
						j.requireRefused(t, img, "a torn slot, cut after %d bytes", n)
					}
				}
			}
		})
	}
}

// TestDeliverySeal_CheckDetectsEveryChange pins check, the seal's part of the per-batch check. It
// passes on an untouched file. It fails on a size change, a replacement of the path with the same
// bytes (identity only), a directory or nothing at the path, a flipped digit in either record, a
// flipped padding byte, a flipped static byte, and a cur that differs from the journal's position.
// Check never writes: the file is left as the change left it.
func TestDeliverySeal_CheckDetectsEveryChange(t *testing.T) {
	j := sealTestLease
	// setup opens a seal and writes twice, so that both slots hold records.
	setup := func(t *testing.T) (string, *deliverySeal) {
		t.Helper()
		fresh := j.fresh()
		p, s := j.open(t, j.image(t, &fresh, nil))
		for seq := uint64(2); seq <= 3; seq++ {
			rec := sealTestRecord(seq)
			require.NoError(t, s.write(rec.Bytes, rec.Count, rec.Chain))
		}
		return p, s
	}

	t.Run("an untouched file passes", func(t *testing.T) {
		_, s := setup(t)
		require.NoError(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain))
		require.NoError(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain), "and check changes nothing it checks")
	})

	for _, tc := range []struct {
		name   string
		change func(t *testing.T, p string, s *deliverySeal)
	}{
		{"the file grows by a byte", func(t *testing.T, p string, _ *deliverySeal) {
			f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
			require.NoError(t, err)
			_, err = f.Write([]byte{' '})
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}},
		{"the file shrinks by a byte", func(t *testing.T, p string, _ *deliverySeal) {
			require.NoError(t, os.Truncate(p, deliverySealFileSize-1))
		}},
		{"the path names a replacement with the same bytes", func(t *testing.T, p string, _ *deliverySeal) {
			require.NoError(t, paths.WriteAtomic(p, readSealFile(t, p), 0o600))
		}},
		{"the path names a directory", func(t *testing.T, p string, _ *deliverySeal) {
			require.NoError(t, os.Remove(p))
			require.NoError(t, os.Mkdir(p, 0o700))
		}},
		{"the path is gone", func(t *testing.T, p string, _ *deliverySeal) {
			require.NoError(t, os.Remove(p))
		}},
		{"a digit of the effective record", func(t *testing.T, p string, s *deliverySeal) {
			off, c := sealChainDigit(t, s.image, slotFor(s.seq))
			patchSealFile(t, p, off, c)
		}},
		{"a digit of the older record", func(t *testing.T, p string, s *deliverySeal) {
			off, c := sealChainDigit(t, s.image, slotFor(s.seq-1))
			patchSealFile(t, p, off, c)
		}},
		{"a padding byte of a slot", func(t *testing.T, p string, _ *deliverySeal) {
			patchSealFile(t, p, deliverySealSlotBOffset+deliverySealSlotRegion-1, 'x')
		}},
		{"a static byte", func(t *testing.T, p string, _ *deliverySeal) {
			patchSealFile(t, p, deliverySealStride-1, 'x')
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, s := setup(t)
			tc.change(t, p, s)
			left, leftErr := os.ReadFile(p)
			require.ErrorIs(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain), core.ErrDegraded)
			after, afterErr := os.ReadFile(p)
			require.Equal(t, leftErr == nil, afterErr == nil)
			require.True(t, bytes.Equal(left, after), "check must never write")
		})
	}

	t.Run("a cur that differs from the journal's position", func(t *testing.T) {
		_, s := setup(t)
		require.ErrorIs(t, s.check(s.cur.Bytes+1, s.cur.Count, s.cur.Chain), core.ErrDegraded)
		require.ErrorIs(t, s.check(s.cur.Bytes, s.cur.Count+1, s.cur.Chain), core.ErrDegraded)
		require.ErrorIs(t, s.check(s.cur.Bytes, s.cur.Count, sealTestChain("another")), core.ErrDegraded)
		require.NoError(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain), "guard: the right position passes")
	})
}

// TestDeliverySeal_OpenVerifiesTheFileAndClosesOnFailure pins openDeliverySeal (design O4b). It
// opens a file that reads back exactly as the image its caller read, and starts from that image's
// effective record. It refuses a file that differs from that image, is longer than it, is missing
// or is a directory, and each refusal writes nothing and leaves no handle open.
func TestDeliverySeal_OpenVerifiesTheFileAndClosesOnFailure(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			img := j.image(t, sealTestRecordPtr(3), sealTestRecordPtr(2))

			t.Run("a file that reads back as the image", func(t *testing.T) {
				caller := bytes.Clone(img)
				p, s := j.open(t, caller)
				require.Equal(t, p, s.path)
				require.Equal(t, j.summed(sealTestRecord(3), sealSlotA), s.cur)
				require.Equal(t, uint64(3), s.seq)
				require.Equal(t, img, s.image)
				require.Nil(t, s.fault, "a fresh seal carries no fault")
				// The syncData seam's production default is what makes every seal durable, and only
				// a test ever replaces it (T24). Nothing else in this package can see a default that
				// does not sync — every test would still pass — so it is pinned here, by identity.
				require.Equal(t, reflect.ValueOf(paths.SyncData).Pointer(), reflect.ValueOf(s.syncData).Pointer(),
					"an open seal must sync through paths.SyncData itself")
				caller[0] = 'x'
				require.NoError(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain), "the seal keeps its own copy of the image")
			})

			for _, tc := range []struct {
				name string
				file []byte
			}{
				{"a file that selects, but is not the image the caller read", j.image(t, sealTestRecordPtr(1), sealTestRecordPtr(2))},
				{"a file one byte longer than the image", append(bytes.Clone(img), ' ')},
				{"a file one byte shorter than the image", bytes.Clone(img[:deliverySealFileSize-1])},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p := sealTestFile(t, tc.file)
					s, err := openDeliverySeal(p, img, j.domain, j.seed)
					require.ErrorIs(t, err, core.ErrDegraded)
					require.Nil(t, s)
					require.Equal(t, tc.file, readSealFile(t, p), "a refused open must write nothing")
					requireNoOpenHandle(t, p)
				})
			}

			t.Run("a missing file, which it does not create", func(t *testing.T) {
				p := filepath.Join(t.TempDir(), deliveryPositionFile)
				s, err := openDeliverySeal(p, img, j.domain, j.seed)
				require.ErrorIs(t, err, core.ErrDegraded)
				require.Nil(t, s)
				_, err = os.Lstat(p)
				require.ErrorIs(t, err, os.ErrNotExist)
			})

			t.Run("a directory", func(t *testing.T) {
				p := filepath.Join(t.TempDir(), deliveryPositionFile)
				require.NoError(t, os.Mkdir(p, 0o700))
				s, err := openDeliverySeal(p, img, j.domain, j.seed)
				require.ErrorIs(t, err, core.ErrDegraded)
				require.Nil(t, s)
			})
		})
	}
}

// TestDeliverySeal_PathReplacedDuringSealReleasesNothing is design T24, the J-B5 regression. The
// SyncData seam replaces the seal's path, between the slot's write and the post-seal identity
// check, with a copy of the image of the same size, so that only the file's identity differs. The
// write must fail and leave cur, seq and image as they were; the next check must fail; and the
// record must have gone only into the file the path no longer names.
func TestDeliverySeal_PathReplacedDuringSealReleasesNothing(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			fresh := j.fresh()
			p, s := j.open(t, j.image(t, &fresh, nil))
			first := sealTestRecord(2)
			require.NoError(t, s.write(first.Bytes, first.Count, first.Chain))

			image, cur, seq := bytes.Clone(s.image), s.cur, s.seq
			second := sealTestRecord(3)

			// The seam is also where the order of writeSlot's two steps is pinned. SyncData makes a
			// record that is already written durable, so the write must come first: a sync issued
			// before it flushes the previous state and leaves the new record in the page cache, and
			// every seal is then non-durable. No assertion on the file's bytes can see that, because
			// they all read back through the same cache. This one can: the seam runs inside
			// writeSlot, before it touches the path, so the slot the new record belongs in must
			// already hold it, read through the handle the seal itself writes.
			slot := slotFor(seq + 1)
			enc, _, encErr := encodeSlot(
				sealRecord{Seq: seq + 1, Bytes: second.Bytes, Count: second.Count, Chain: second.Chain},
				slot, j.domain, j.seed)
			require.NoError(t, encErr)

			replaced := false
			s.syncData = func(f *os.File) error {
				sealed := make([]byte, deliverySealSlotRegion)
				n, readErr := s.f.ReadAt(sealed, int64(slot.offset()))
				require.NoError(t, readErr)
				require.Equal(t, deliverySealSlotRegion, n)
				require.Equal(t, enc, sealed, "the slot's write precedes its sync, or the seal is not durable")
				err := paths.SyncData(f)
				require.NoError(t, paths.WriteAtomic(p, image, 0o600))
				replaced = true
				return err
			}
			require.ErrorIs(t, s.write(second.Bytes, second.Count, second.Chain), core.ErrDegraded)
			require.True(t, replaced, "guard: the seam ran between the write and the identity check")

			require.Equal(t, image, s.image, "memory follows the disk only on success")
			require.Equal(t, cur, s.cur)
			require.Equal(t, seq, s.seq)
			require.ErrorIs(t, s.check(cur.Bytes, cur.Count, cur.Chain), core.ErrDegraded,
				"the path no longer names the file the seal holds")

			onPath := readSealFile(t, p)
			require.Equal(t, image, onPath, "the path still holds the previous seal")
			j.requireSelects(t, onPath, cur, &sealRecord{Seq: 1, Chain: j.seed, Sum: j.summed(fresh, sealSlotA).Sum},
				"recovery reads what the path holds")
			orphan := make([]byte, deliverySealFileSize)
			_, err := s.f.ReadAt(orphan, 0)
			require.NoError(t, err)
			eff, _, err := selectSeal(orphan, j.domain, j.seed)
			require.NoError(t, err)
			require.Equal(t, uint64(3), eff.Seq, "the new record landed, but only in the orphaned file")
		})
	}
}

// TestDeliverySeal_WriteRefusesWhatItsReaderWouldRefuse pins write's preconditions. A position that
// does not strictly extend the sealed one (in bytes and in entries), one out of the journal's
// bounds, and a seq that would wrap are all refused before any I/O: the file, the image, cur and
// seq are unchanged, and check still passes.
func TestDeliverySeal_WriteRefusesWhatItsReaderWouldRefuse(t *testing.T) {
	j := sealTestLease
	cur := sealTestRecord(3)
	grown := sealTestChain("grown")
	for _, tc := range []struct {
		name  string
		size  int64
		count int
		chain core.Hash
	}{
		{"the sealed position again", cur.Bytes, cur.Count, cur.Chain},
		{"fewer bytes", cur.Bytes - 1, cur.Count + 1, grown},
		{"no more bytes", cur.Bytes, cur.Count + 1, grown},
		{"no more entries", cur.Bytes + 1, cur.Count, grown},
		{"fewer entries", cur.Bytes + 1, cur.Count - 1, grown},
		{"bytes past the journal's cap", deliveryLeaseMaxBytes + 1, cur.Count + 1, grown},
		{"entries past the journal's cap", cur.Bytes + 1, deliveryLeaseMaxEntries + 1, grown},
		{"a zero chain", cur.Bytes + 1, cur.Count + 1, core.Hash{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, s := j.open(t, j.image(t, sealTestRecordPtr(3), sealTestRecordPtr(2)))
			before, image, was := readSealFile(t, p), bytes.Clone(s.image), s.cur
			require.ErrorIs(t, s.write(tc.size, tc.count, tc.chain), core.ErrDegraded)
			require.Equal(t, before, readSealFile(t, p))
			require.Equal(t, image, s.image)
			require.Equal(t, was, s.cur)
			require.Equal(t, uint64(3), s.seq)
			require.NoError(t, s.check(was.Bytes, was.Count, was.Chain))
		})
	}

	t.Run("a seq that would wrap", func(t *testing.T) {
		p, s := j.open(t, j.image(t, sealTestRecordPtr(3), sealTestRecordPtr(2)))
		before := readSealFile(t, p)
		s.seq = math.MaxUint64
		require.ErrorIs(t, s.write(cur.Bytes+1, cur.Count+1, grown), core.ErrDegraded)
		require.Equal(t, before, readSealFile(t, p))
	})
}

// TestDeliverySeal_AFailedWriteLatchesTheSeal pins the seal's own half of design §3 row 13: once a
// write has begun its I/O, a failure leaves it uncertain whether the record landed, is torn or is
// durable, so the seal latches the fault and refuses every later write, check and downgrade. It is
// never retried — on Linux a second fdatasync after a failed one can report success without the
// data ever having been durable.
//
// The failure is injected through the SyncData seam, which puts the slot's old bytes back before it
// errors: the state a device that lost the write leaves. The file is then byte for byte the image
// the seal cached, at the path it already named, so every other check the seal makes passes and
// only the latch can tell that the seal is uncertain.
//
// A refusal before any I/O is not a fault: nothing was attempted, and the seal stays usable.
func TestDeliverySeal_AFailedWriteLatchesTheSeal(t *testing.T) {
	j := sealTestLease

	t.Run("a failure at the sync", func(t *testing.T) {
		fresh := j.fresh()
		p, s := j.open(t, j.image(t, &fresh, nil))
		first := sealTestRecord(2)
		require.NoError(t, s.write(first.Bytes, first.Count, first.Chain))

		image, cur, seq := bytes.Clone(s.image), s.cur, s.seq
		slot := slotFor(seq + 1)
		restored := bytes.Clone(slot.region(s.image))
		ran := false
		s.syncData = func(f *os.File) error {
			_, err := f.WriteAt(restored, int64(slot.offset()))
			require.NoError(t, err)
			require.NoError(t, paths.SyncData(f))
			ran = true
			return errors.New("the device refused the sync")
		}
		next := sealTestRecord(3)
		require.ErrorIs(t, s.write(next.Bytes, next.Count, next.Chain), core.ErrDegraded)
		require.True(t, ran, "guard: the seam ran")
		s.syncData = paths.SyncData

		require.Equal(t, image, readSealFile(t, p), "guard: the file is the image again, so only the latch can refuse")
		require.Equal(t, image, s.image, "memory follows the disk only on success")
		require.Equal(t, cur, s.cur)
		require.Equal(t, seq, s.seq)

		require.ErrorIs(t, s.check(cur.Bytes, cur.Count, cur.Chain), core.ErrDegraded, "a faulted seal passes no check")
		later := sealTestRecord(4)
		require.ErrorIs(t, s.write(later.Bytes, later.Count, later.Chain), core.ErrDegraded, "and is never written again")
		require.ErrorIs(t, s.downgradeToV1(), core.ErrDegraded, "and never downgrades an uncertain seal")
		require.Equal(t, image, readSealFile(t, p), "none of which wrote anything")
	})

	t.Run("a failure at the post-seal identity check", func(t *testing.T) {
		fresh := j.fresh()
		p, s := j.open(t, j.image(t, &fresh, nil))
		first := sealTestRecord(2)
		require.NoError(t, s.write(first.Bytes, first.Count, first.Chain))

		image, cur, seq := bytes.Clone(s.image), s.cur, s.seq
		ran := false
		s.syncData = func(f *os.File) error {
			err := paths.SyncData(f)
			require.NoError(t, os.Truncate(p, deliverySealFileSize-1))
			ran = true
			return err
		}
		next := sealTestRecord(3)
		require.ErrorIs(t, s.write(next.Bytes, next.Count, next.Chain), core.ErrDegraded)
		require.True(t, ran, "guard: the seam ran between the slot's write and the identity check")
		s.syncData = paths.SyncData

		// Put the path back to exactly the image the seal cached, through a second handle on it, so
		// that the file, its size and its every byte are what they were before the failed write.
		// Only the latched fault can refuse afterwards.
		restore, err := paths.OpenSharedRW(p)
		require.NoError(t, err)
		n, err := restore.WriteAt(image, 0)
		require.NoError(t, err)
		require.Equal(t, len(image), n)
		require.NoError(t, paths.SyncData(restore))
		require.NoError(t, restore.Close())
		require.Equal(t, image, readSealFile(t, p), "guard: the path holds the seal's image again")
		require.NoError(t, s.verifyIdentity(), "guard: at its right size, and still the file the seal holds")

		require.Equal(t, image, s.image)
		require.Equal(t, cur, s.cur)
		require.Equal(t, seq, s.seq)
		require.ErrorIs(t, s.check(cur.Bytes, cur.Count, cur.Chain), core.ErrDegraded, "a faulted seal passes no check")
		later := sealTestRecord(4)
		require.ErrorIs(t, s.write(later.Bytes, later.Count, later.Chain), core.ErrDegraded, "and is never written again")
		require.ErrorIs(t, s.downgradeToV1(), core.ErrDegraded, "and never downgrades an uncertain seal")
		require.Equal(t, image, readSealFile(t, p), "none of which wrote anything")
	})

	t.Run("a refusal before any I/O", func(t *testing.T) {
		p, s := j.open(t, j.image(t, sealTestRecordPtr(3), sealTestRecordPtr(2)))
		was := s.cur
		require.ErrorIs(t, s.write(was.Bytes, was.Count, was.Chain), core.ErrDegraded, "the sealed position again")
		require.Nil(t, s.fault, "a position the reader would refuse is not a fault: nothing was attempted")
		require.NoError(t, s.check(was.Bytes, was.Count, was.Chain))

		next := sealTestRecord(4)
		require.NoError(t, s.write(next.Bytes, next.Count, next.Chain), "and the next real seal still writes")
		require.NoError(t, s.check(next.Bytes, next.Count, next.Chain))
		j.requireSelects(t, readSealFile(t, p), s.cur, &was)
	})
}

// TestDeliverySeal_ClosedSealWritesAndPassesNothing pins that a closed seal can neither write nor
// pass a check, and that closing it writes nothing.
func TestDeliverySeal_ClosedSealWritesAndPassesNothing(t *testing.T) {
	j := sealTestLease
	p, s := j.open(t, j.image(t, sealTestRecordPtr(3), sealTestRecordPtr(2)))
	before := readSealFile(t, p)
	require.NoError(t, s.close())
	require.Equal(t, before, readSealFile(t, p), "close writes nothing")
	require.ErrorIs(t, s.check(s.cur.Bytes, s.cur.Count, s.cur.Chain), core.ErrDegraded)
	next := sealTestRecord(4)
	require.ErrorIs(t, s.write(next.Bytes, next.Count, next.Chain), core.ErrDegraded)
	require.Equal(t, before, readSealFile(t, p))
	requireNoOpenHandle(t, p)
}

// TestDeliverySeal_DowngradeWritesTodaysV1Bytes pins the v1 downgrade. For the lease seal and the
// ack seal, and for a fresh, a written and a maximal position, it writes exactly the bytes today's
// savePosition or saveAckPosition writes for the same position, and loadDeliveryPosition accepts
// them with exactly that position.
//
// Its last subtest carries the same equality to the file a fresh project actually starts from, end
// to end through a real open. The empty position's own encoder is pinned separately and directly by
// TestDeliveryPosition_CreateEmptyIsTheOneV1Encoders.
func TestDeliverySeal_DowngradeWritesTodaysV1Bytes(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			dir := t.TempDir()
			v1 := &deliveryJournal{path: filepath.Join(dir, deliveryLeaseFile)}
			save, saved := v1.savePosition, filepath.Join(dir, deliveryPositionFile)
			if j.domain == deliveryAckChainDomain {
				save, saved = v1.saveAckPosition, filepath.Join(dir, deliveryAckPositionFile)
			}
			maximal := sealRecord{Seq: 1, Bytes: deliveryLeaseMaxBytes, Count: deliveryLeaseMaxEntries, Chain: sealTestChain("maximal")}

			for _, tc := range []struct {
				name   string
				start  sealRecord
				writes []uint64
			}{
				{"fresh", j.fresh(), nil},
				{"written twice", j.fresh(), []uint64{2, 3}},
				{"converted", sealTestRecord(1), nil},
				{"maximal", maximal, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					start := tc.start
					p, s := j.open(t, j.image(t, &start, nil))
					for _, seq := range tc.writes {
						rec := sealTestRecord(seq)
						require.NoError(t, s.write(rec.Bytes, rec.Count, rec.Chain))
					}
					require.NoError(t, s.close())
					require.NoError(t, s.downgradeToV1())
					requireOwnerOnlyMode(t, p)

					require.NoError(t, save(s.cur.Bytes, s.cur.Count, s.cur.Chain))
					requireOwnerOnlyMode(t, saved) // guard: the permissions savePosition itself writes
					require.Equal(t, readSealFile(t, saved), readSealFile(t, p), "the downgrade writes today's v1 bytes")
					loaded, err := loadDeliveryPosition(p, j.seed)
					require.NoError(t, err)
					require.Equal(t, deliveryPosition{Version: core.EvidenceVersion, Bytes: s.cur.Bytes, Count: s.cur.Count, Chain: s.cur.Chain}, loaded)
					require.False(t, isDeliverySealImage(readSealFile(t, p)))
				})
			}

			// The same equality over a fresh project's sidecar, end to end through a real open.
			//
			// What this subtest sees, stated exactly. At write format 1 the open's O4 rewrites both
			// sidecars before it returns (openSeal → savePosition, openAckSeal → saveAckPosition), so
			// these are the OPEN's bytes, not the create's: the create's own bytes are overwritten
			// inside openDeliveryJournal and no test can read them back without a production seam
			// that would exist only for the test. So this pins what a fresh project has ON DISK when
			// the open returns — the empty position, owner-only, loadable — and nothing about which
			// call wrote it. The create sites' shared encoder is pinned directly, one layer down, by
			// TestDeliveryPosition_CreateEmptyIsTheOneV1Encoders; that a create site producing some
			// other VALUE cannot reach here at all is loadDeliveryPosition's doing, because load()
			// reads the file the create just wrote and refuses a position whose chain is not this
			// journal's seed or whose bytes and count disagree with each other.
			t.Run("a fresh project's sidecar", func(t *testing.T) {
				_, journal := openSealFormat(t, t.TempDir(), 1)
				p := journal.positionPath()
				if j.domain == deliveryAckChainDomain {
					p = journal.ackSealPath()
				}
				want, err := encodeDeliveryPositionV1(0, 0, j.seed)
				require.NoError(t, err)
				require.Equal(t, want, readTestFile(t, p),
					"when the open returns, a fresh project's sidecar is the v1 empty position")
				requireOwnerOnlyMode(t, p)
				position, err := loadDeliveryPosition(p, j.seed)
				require.NoError(t, err)
				require.Equal(t, deliveryPosition{Version: core.EvidenceVersion, Chain: j.seed}, position)
			})
		})
	}
}

// TestDeliveryPosition_CreateEmptyIsTheOneV1Encoders pins createEmptyDeliveryPositionV1 — the one
// call both CREATE sites make (openDeliveryJournal's O1 for the lease position, openAckLocked's for
// the acknowledgement one) — directly, which is the only layer at which the empty position's own
// bytes can be read back at all: a real open re-seals both sidecars at O4 before it returns.
//
// For each journal's seed: the bytes are exactly the v1 encoder's for a zero position, the
// permissions are owner-only, loadDeliveryPosition accepts the file as that journal's EMPTY position
// — and refuses it for the other journal, which is what makes the seed argument load-bearing rather
// than decorative. It is not a v2 image either, so an older binary reads it.
func TestDeliveryPosition_CreateEmptyIsTheOneV1Encoders(t *testing.T) {
	for _, j := range sealTestJournals {
		t.Run(j.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), deliveryPositionFile)
			require.NoError(t, createEmptyDeliveryPositionV1(p, j.seed))

			want, err := encodeDeliveryPositionV1(0, 0, j.seed)
			require.NoError(t, err)
			require.Equal(t, want, readTestFile(t, p), "the empty position has one encoder")
			requireOwnerOnlyMode(t, p)
			require.False(t, isDeliverySealImage(readTestFile(t, p)), "a create writes v1, never an image")

			position, err := loadDeliveryPosition(p, j.seed)
			require.NoError(t, err)
			require.Equal(t, deliveryPosition{Version: core.EvidenceVersion, Chain: j.seed}, position)

			other := sealTestLease.seed
			if j.seed == other {
				other = sealTestAck.seed
			}
			_, err = loadDeliveryPosition(p, other)
			require.ErrorIs(t, err, core.ErrDegraded,
				"an empty position is the seed's own: the other journal's reader refuses it")
		})
	}
}

// TestDeliverySeal_BoundsAreTheJournalReaders keeps the seal's bounds in step with the journal's.
// For every bound loadDeliveryPosition enforces, on both sides of it, a v1 file of the position is
// accepted exactly when encodeSlot accepts the position as seq 1 and classifySlot finds it valid.
func TestDeliverySeal_BoundsAreTheJournalReaders(t *testing.T) {
	j := sealTestLease
	chain := sealTestChain("bounds")
	for _, tc := range []struct {
		name  string
		size  int64
		count int
		chain core.Hash
		ok    bool
	}{
		{"the empty position", 0, 0, j.seed, true},
		{"one byte and one entry", 1, 1, chain, true},
		{"the caps", deliveryLeaseMaxBytes, deliveryLeaseMaxEntries, chain, true},
		{"negative bytes", -1, 1, chain, false},
		{"bytes past the cap", deliveryLeaseMaxBytes + 1, 1, chain, false},
		{"negative entries", 1, -1, chain, false},
		{"entries past the cap", 1, deliveryLeaseMaxEntries + 1, chain, false},
		{"bytes without entries", 1, 0, chain, false},
		{"entries without bytes", 0, 1, j.seed, false},
		{"a zero chain", 1, 1, core.Hash{}, false},
		{"the empty position with another chain", 0, 0, chain, false},
		{"the empty position with the ack journal's seed", 0, 0, deliveryAckChainSeed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := encodeDeliveryPositionV1(tc.size, tc.count, tc.chain)
			require.NoError(t, err)
			_, v1Err := loadDeliveryPosition(sealTestFile(t, encoded), j.seed)
			require.Equal(t, tc.ok, v1Err == nil, "guard: loadDeliveryPosition's own answer")

			rec := sealRecord{Seq: 1, Bytes: tc.size, Count: tc.count, Chain: tc.chain}
			require.Equal(t, tc.ok, sealInBounds(tc.size, tc.count, tc.chain, j.seed))
			_, _, encErr := encodeSlot(rec, sealSlotA, j.domain, j.seed)
			require.Equal(t, tc.ok, encErr == nil, "encodeSlot")
			_, state := classifySlot(sealRawRegion(t, rec, sealSlotA, j.domain), sealSlotA, j.domain, j.seed)
			require.Equal(t, tc.ok, state == sealSlotValid, "classifySlot")
		})
	}
}

// TestDeliverySeal_SumIsItsDocumentedEncoding pins sealSum's input byte for byte, as its comment
// defines it, and shows that every field it binds changes it.
func TestDeliverySeal_SumIsItsDocumentedEncoding(t *testing.T) {
	input := append([]byte("qompack.delivery.lease-chain.v1\x00a\x001\x000\x000\x00"), deliveryChainSeed[:]...)
	require.Equal(t, core.HashBytes("qompack.delivery.seal.v2", input),
		sealSum(deliveryChainDomain, sealSlotA, 1, 0, 0, deliveryChainSeed))

	chain := sealTestChain("documented")
	input = append([]byte("qompack.delivery.ack-chain.v1\x00b\x0018446744073709551614\x0067108864\x0065536\x00"), chain[:]...)
	require.Equal(t, core.HashBytes("qompack.delivery.seal.v2", input),
		sealSum(deliveryAckChainDomain, sealSlotB, math.MaxUint64-1, deliveryLeaseMaxBytes, deliveryLeaseMaxEntries, chain))

	base := sealSum(deliveryChainDomain, sealSlotA, 3, 3000, 9, chain)
	for name, other := range map[string]core.Hash{
		"domain": sealSum(deliveryAckChainDomain, sealSlotA, 3, 3000, 9, chain),
		"slot":   sealSum(deliveryChainDomain, sealSlotB, 3, 3000, 9, chain),
		"seq":    sealSum(deliveryChainDomain, sealSlotA, 5, 3000, 9, chain),
		"bytes":  sealSum(deliveryChainDomain, sealSlotA, 3, 3001, 9, chain),
		"count":  sealSum(deliveryChainDomain, sealSlotA, 3, 3000, 10, chain),
		"chain":  sealSum(deliveryChainDomain, sealSlotA, 3, 3000, 9, sealTestChain("other")),
	} {
		require.NotEqual(t, base, other, "the sum must bind the %s", name)
	}
}

// TestDeliverySeal_LongestRecordFitsItsSlot pins that the longest record the bounds allow (the
// largest seq, bytes and count, each at its widest) encodes into a slot and classifies back as
// itself, and that an empty slot classifies as empty.
func TestDeliverySeal_LongestRecordFitsItsSlot(t *testing.T) {
	j := sealTestLease
	longest := sealRecord{Seq: math.MaxUint64, Bytes: deliveryLeaseMaxBytes, Count: deliveryLeaseMaxEntries, Chain: sealTestChain("longest")}
	region, summed, err := encodeSlot(longest, sealSlotA, j.domain, j.seed)
	require.NoError(t, err)
	require.Len(t, region, deliverySealSlotRegion)
	body := bytes.IndexByte(region, deliverySealPad)
	require.Equal(t, 221, body, "the longest record's length, which deliverySealSlotRegion's comment cites")
	got, state := classifySlot(region, sealSlotA, j.domain, j.seed)
	require.Equal(t, sealSlotValid, state)
	require.Equal(t, summed, got)

	_, state = classifySlot(sealSlotA.region(sealImageTemplate()), sealSlotA, j.domain, j.seed)
	require.Equal(t, sealSlotEmpty, state)
}

// TestDeliverySeal_WriteFormatIsDeliberate is design T27. The format a build writes its seals in is
// a package constant, not a config gate (design §4.3, Q12). Step 1 of the rollout shipped the v2
// reader and this code while every seal was still today's v1 sidecar, so the constant was 1. Step 2
// flipped it to 2 in its own reviewed commit, which changed this test in the same commit and cites
// the evidence for it: T25 (TestDeliverySeal_ConversionAndDowngrade), which is the conversion, the
// two refusals to convert, the two Releases that write no downgrade, and a format-1 build converting
// a real v2 file back; T26 (TestDeliveryJournal_RollbackDrillAcrossFormats), which is the rollback
// drill SP-20 requires against a v2 artifact a running daemon made; and the §3 crash table in
// format 2 (TestDeliveryPath_CrashCutAtEveryStep). The design 6.1 step-2 trace is evidence twice
// over: TestDeliverySeal_StepTwoTraceHoldsInFormatTwo asserts each of its rows against a held seal
// by naming format 2, and since this constant became 2 the trace's ORIGINALS —
// …PositionCorruptionCannotBeRepairedByAnOpenWriter, …UncertainAppendPoisonsUntilRecovery,
// …LeaseSyncSerializesWithRelease, …ReplacedLockCannotReleaseOrLeaseForNewOwner and
// …CloseFailureRetainsOwnership — run against one without naming anything.
//
// The direction that still needs the same care is BACKWARD. Returning this constant to 1 is the
// step-2 → step-1 rollback, which design §4.4 calls always safe; it is safe only because the reader
// stays dual and openSeal goes on rewriting a v1 sidecar unconditionally. Both are pinned at format
// 1 through the lock's seam, by TestDeliverySeal_FormatOneRefusesAV2ImageAtEitherSidecar and by
// T25's own format-1 legs, so neither depends on this constant to be exercised. A change here in
// either direction without that evidence moves the on-disk format under a reader that was never
// shown to take it.
func TestDeliverySeal_WriteFormatIsDeliberate(t *testing.T) {
	require.Equal(t, 2, deliverySealWriteFormat,
		"step 2 flips the write format only together with this test and the T25/T26 evidence (design §4.3)")
}
