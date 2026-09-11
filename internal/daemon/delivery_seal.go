package daemon

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strconv"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The v2 delivery seal (SP20-D1 design §2.9).
//
// Today a journal's position is sealed by a v1 sidecar that paths.WriteAtomic replaces for every
// lease batch (savePosition): a temp file, its sync, a rename and, on POSIX, a directory sync. The
// v2 seal keeps the same position in one fixed-size file that the journal holds open for its life
// and overwrites in place. Each seal is one record in one of two slots, alternately, written with
// one WriteAt and one SyncData, so a crash can leave at most the slot being written in doubt, and
// never the other one.
//
// The file is one valid JSON document of exactly deliverySealFileSize bytes:
//
//	offset  len    content
//	0       11     {"v":2,"a":                       static
//	11      480    slot a: a record or null, then spaces to the region's end
//	491     15893  spaces                            static
//	16384   5      ,"b":                             static
//	16389   480    slot b: a record or null, then spaces to the region's end
//	16869   15898  spaces                            static
//	32767   1      }                                 static
//
// Each slot lies inside one 512-byte sector and one 4 KiB block (slot a in sector 0 and block 0,
// slot b in sector 32 and block 4), and the two lie in different 4 KiB and 16 KiB pages.
//
// This is part 3a of SP20-D1's seal work: the seal as a unit, not yet wired into the journal, which
// still seals with savePosition. Part 3b adds the journal integration: the dual reader, the open
// sequence (design O1-O6), the older-seal checkpoint in load, and the downgrade at Release.

const (
	// deliverySealVersion is a v2 seal's "v". An older binary's loadDeliveryPosition requires
	// core.EvidenceVersion (1) there, so it refuses a v2 file rather than misreading it.
	deliverySealVersion = 2
	// deliverySealPrefixA opens the document and names slot a, whose region starts right after it.
	// The "v" it spells is deliverySealVersion.
	deliverySealPrefixA = `{"v":2,"a":`
	// deliverySealPrefixB starts the second stride and names slot b.
	deliverySealPrefixB = `,"b":`
	// deliverySealSuffix closes the document, in the file's last byte.
	deliverySealSuffix = `}`
	// deliverySealEmpty is an empty slot's value: JSON null.
	deliverySealEmpty = `null`
	// deliverySealPad is every byte that is neither static text nor a slot's value.
	deliverySealPad = ' '

	// deliverySealSector is the unit a block device is assumed to write atomically. Each slot's
	// region lies inside one (the compile-time checks below).
	deliverySealSector = 512
	// deliverySealStride is where the second stride, which carries slot b, starts: 16 KiB in, so
	// the two slots never share a 4 KiB or a 16 KiB page.
	deliverySealStride = 16 << 10
	// deliverySealFileSize is two strides. The file never changes size, which is what lets a seal
	// be made durable by SyncData alone.
	deliverySealFileSize = 2 * deliverySealStride
	// deliverySealSlotRegion is each slot's region: room for the longest record (221 bytes, see
	// TestDeliverySeal_LongestRecordFitsItsSlot) with the rest padding, and short enough to sit
	// inside one sector behind either prefix.
	deliverySealSlotRegion = 480
	// deliverySealSlotAOffset and deliverySealSlotBOffset are where the slot regions start: each
	// right after its prefix.
	deliverySealSlotAOffset = len(deliverySealPrefixA)
	deliverySealSlotBOffset = deliverySealStride + len(deliverySealPrefixB)

	// deliverySealSumDomain is the core.HashBytes domain of every record's sum (sealSum).
	deliverySealSumDomain = "qompack.delivery.seal.v2"

	// deliverySealWriteFormat is the seal format this build writes. It is 1, today's v1 sidecar,
	// until step 2 of the rollout (design §4.3) flips it to 2 in its own reviewed commit.
	// TestDeliverySeal_WriteFormatIsDeliberate pins it.
	deliverySealWriteFormat = 1
)

// The layout's sector claims, checked by the compiler: each line fails to build if its operand is
// negative. Slot a's region ends inside sector 0, and slot b's inside the first sector of the
// second stride.
const (
	_ = uint(deliverySealSector - deliverySealSlotAOffset - deliverySealSlotRegion)
	_ = uint(deliverySealStride + deliverySealSector - deliverySealSlotBOffset - deliverySealSlotRegion)
)

// sealSlot names a slot by its letter in the document. The letter is also the byte every record's
// sum binds to its slot.
type sealSlot byte

const (
	sealSlotA sealSlot = 'a'
	sealSlotB sealSlot = 'b'
)

// slotFor is the slot a record with seq lives in: an odd seq in a, an even one in b. A write of
// seq+1 therefore always lands in the slot holding seq-1, never in the effective record's.
func slotFor(seq uint64) sealSlot {
	if seq%2 == 1 {
		return sealSlotA
	}
	return sealSlotB
}

// offset is where the slot's region starts in the file.
func (s sealSlot) offset() int {
	if s == sealSlotA {
		return deliverySealSlotAOffset
	}
	return deliverySealSlotBOffset
}

// region is the slot's region of a v2 image.
func (s sealSlot) region(img []byte) []byte {
	return img[s.offset() : s.offset()+deliverySealSlotRegion]
}

// sealRecord is one slot's record. Its fields are in their canonical JSON order, and Bytes, Count
// and Chain have deliveryPosition's types.
type sealRecord struct {
	Seq   uint64    `json:"seq"`
	Bytes int64     `json:"bytes"`
	Count int       `json:"count"`
	Chain core.Hash `json:"chain"`
	Sum   core.Hash `json:"sum"`
}

// sealSum is the sum a record carries: core.HashBytes(deliverySealSumDomain, input), where input
// is the bytes
//
//	chainDomain 0x00 slot 0x00 dec(seq) 0x00 dec(bytes) 0x00 dec(count) 0x00 chain
//
// in which:
//
//   - chainDomain is the journal's own chain domain, deliveryChainDomain for the lease seal and
//     deliveryAckChainDomain for the ack seal. It binds a record to its journal.
//   - slot is the one byte 'a' or 'b'. It binds a record to its slot, so a record copied into the
//     other slot is invalid there.
//   - dec(n) is n in base-10 ASCII with no sign, no leading zero, and "0" for zero: the digits the
//     record's JSON carries, which are strconv.AppendUint's and strconv.AppendInt's.
//   - chain is the chain digest's 32 raw bytes.
//
// No field but the last can contain a 0x00 byte, and the last has a fixed width, so an input
// parses one way only. Only records in bounds are summed for a seal, so no number is negative.
func sealSum(chainDomain string, slot sealSlot, seq uint64, size int64, count int, chain core.Hash) core.Hash {
	const decimal = 10
	input := append([]byte(chainDomain), 0, byte(slot), 0)
	input = strconv.AppendUint(input, seq, decimal)
	input = append(input, 0)
	input = strconv.AppendInt(input, size, decimal)
	input = append(input, 0)
	input = strconv.AppendInt(input, int64(count), decimal)
	input = append(input, 0)
	input = append(input, chain[:]...)
	return core.HashBytes(deliverySealSumDomain, input)
}

// sealInBounds reports whether a sealed position is one the journal's own reader accepts. These are
// loadDeliveryPosition's bounds, exactly: bytes and entries within the journal's caps, an empty
// journal (no bytes) exactly when there are no entries, never a zero chain, and an empty journal
// carrying its seed chain. TestDeliverySeal_BoundsAreTheJournalReaders keeps the two in step.
func sealInBounds(size int64, count int, chain, seed core.Hash) bool {
	return size >= 0 && size <= deliveryLeaseMaxBytes &&
		count >= 0 && count <= deliveryLeaseMaxEntries &&
		(size == 0) == (count == 0) && !chain.IsZero() &&
		(size != 0 || chain == seed)
}

// sealAdmissible reports whether rec, whatever its sum, may live in slot for the journal whose
// seed is seed: a seq of at least 1 with the slot's parity, and a position in bounds.
func sealAdmissible(rec sealRecord, slot sealSlot, seed core.Hash) bool {
	return rec.Seq >= 1 && slotFor(rec.Seq) == slot && sealInBounds(rec.Bytes, rec.Count, rec.Chain, seed)
}

// encodeSlot sums rec for slot and returns its slot region, the canonical JSON record followed by
// spaces to deliverySealSlotRegion bytes, together with the summed record. It refuses a record
// that would not be valid in slot, and one too long for the region, so a seal never writes a
// record its own reader refuses.
func encodeSlot(rec sealRecord, slot sealSlot, domain string, seed core.Hash) ([]byte, sealRecord, error) {
	if !sealAdmissible(rec, slot, seed) {
		return nil, sealRecord{}, deliveryJournalError()
	}
	rec.Sum = sealSum(domain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain)
	body, err := json.Marshal(rec)
	if err != nil || len(body) > deliverySealSlotRegion {
		return nil, sealRecord{}, deliveryJournalError()
	}
	region := bytes.Repeat([]byte{deliverySealPad}, deliverySealSlotRegion)
	copy(region, body)
	return region, rec, nil
}

// sealSlotState is what a slot holds.
type sealSlotState uint8

const (
	// sealSlotInvalid is anything that is neither empty nor valid: media damage or a foreign write.
	sealSlotInvalid sealSlotState = iota
	// sealSlotEmpty is exactly deliverySealEmpty, then spaces.
	sealSlotEmpty
	// sealSlotValid is a valid record, then spaces.
	sealSlotValid
)

// classifySlot classifies one slot's region (design §2.9). The slot is empty when it holds exactly
// deliverySealEmpty followed by spaces. It is valid when every one of these holds:
//
//   - it holds a record's canonical JSON, followed by spaces only, up to the region's end;
//   - the record's position is in bounds;
//   - its seq is at least 1, with the slot's parity (odd in a, even in b);
//   - its sum is the one sealSum gives for this slot and this journal.
//
// Anything else is invalid. A record is returned only with sealSlotValid.
func classifySlot(region []byte, slot sealSlot, domain string, seed core.Hash) (sealRecord, sealSlotState) {
	if len(region) != deliverySealSlotRegion {
		return sealRecord{}, sealSlotInvalid
	}
	end := bytes.IndexByte(region, deliverySealPad)
	if end < 0 {
		end = len(region)
	}
	if !sealPadded(region[end:]) {
		return sealRecord{}, sealSlotInvalid
	}
	body := region[:end]
	if string(body) == deliverySealEmpty {
		return sealRecord{}, sealSlotEmpty
	}
	var rec sealRecord
	if json.Unmarshal(body, &rec) != nil {
		return sealRecord{}, sealSlotInvalid
	}
	// Re-encoding must give back exactly these bytes. That rejects unknown, duplicate, reordered or
	// differently cased keys, and every other spelling of a value (a bare-hex hash, an exponent).
	canonical, err := json.Marshal(rec)
	if err != nil || !bytes.Equal(canonical, body) || !sealAdmissible(rec, slot, seed) ||
		rec.Sum != sealSum(domain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain) {
		return sealRecord{}, sealSlotInvalid
	}
	return rec, sealSlotValid
}

// sealPadded reports whether b is padding only.
func sealPadded(b []byte) bool {
	for _, c := range b {
		if c != deliverySealPad {
			return false
		}
	}
	return true
}

// isDeliverySealImage reports whether img is laid out as a v2 seal: exactly deliverySealFileSize
// bytes, with every static byte exact. It does not look inside the slots. Anything else is not a
// v2 image, which is what hands a position file to the v1 reader.
func isDeliverySealImage(img []byte) bool {
	const (
		aEnd   = deliverySealSlotAOffset + deliverySealSlotRegion
		bEnd   = deliverySealSlotBOffset + deliverySealSlotRegion
		suffix = deliverySealFileSize - len(deliverySealSuffix)
	)
	return len(img) == deliverySealFileSize &&
		string(img[:deliverySealSlotAOffset]) == deliverySealPrefixA &&
		sealPadded(img[aEnd:deliverySealStride]) &&
		string(img[deliverySealStride:deliverySealSlotBOffset]) == deliverySealPrefixB &&
		sealPadded(img[bEnd:suffix]) &&
		string(img[suffix:]) == deliverySealSuffix
}

// selectSeal is the strict reader (design §2.9, the fix for J-B3) over a whole file's bytes. It
// returns the effective record, and the older record when there is one:
//
//	slots                             decision
//	a valid with seq 1, b empty       effective a, no older record: a fresh or a converted file
//	both valid, seqs one apart        effective the higher seq, and older the other, which must
//	                                  seal strictly fewer bytes and strictly fewer entries
//	anything else                     refuse
//
// Anything else includes a file that is not a v2 image (isDeliverySealImage), any invalid slot, an
// empty slot beside a seq other than 1, two empty slots, a seq gap and a parity violation. Every
// crash-reachable image is one of the two accepted rows, because a write goes to the slot holding
// seq-1 and only after the bytes it seals are durable. An invalid slot is therefore media damage or
// a foreign write, and a refusal, which writes nothing, preserves the evidence. selectSeal cannot
// see the journal: the older record is returned so that its caller can verify that it seals a
// prefix of the journal (part 3b's load).
//
// A refusal is deliveryJournalError(), the journal's error for an untrustworthy position.
func selectSeal(img []byte, domain string, seed core.Hash) (sealRecord, *sealRecord, error) {
	if !isDeliverySealImage(img) {
		return sealRecord{}, nil, deliveryJournalError()
	}
	a, aState := classifySlot(sealSlotA.region(img), sealSlotA, domain, seed)
	b, bState := classifySlot(sealSlotB.region(img), sealSlotB, domain, seed)
	switch {
	case aState == sealSlotValid && bState == sealSlotEmpty && a.Seq == 1:
		return a, nil, nil
	case aState == sealSlotValid && bState == sealSlotValid:
		eff, older := a, b
		if b.Seq > a.Seq {
			eff, older = b, a
		}
		// The slots' parities differ, so eff.Seq > older.Seq and this cannot wrap.
		if eff.Seq-older.Seq == 1 && older.Bytes < eff.Bytes && older.Count < eff.Count {
			return eff, &older, nil
		}
	}
	return sealRecord{}, nil, deliveryJournalError()
}

// sealImageTemplate is a v2 image with both slots empty: every static byte, deliverySealEmpty at
// the start of each slot's region, and padding everywhere else.
func sealImageTemplate() []byte {
	img := bytes.Repeat([]byte{deliverySealPad}, deliverySealFileSize)
	copy(img, deliverySealPrefixA)
	copy(img[deliverySealStride:], deliverySealPrefixB)
	copy(img[deliverySealFileSize-len(deliverySealSuffix):], deliverySealSuffix)
	copy(img[deliverySealSlotAOffset:], deliverySealEmpty)
	copy(img[deliverySealSlotBOffset:], deliverySealEmpty)
	return img
}

// buildSealImage returns the v2 image whose slot a holds a and whose slot b holds b, where nil
// means null. Each record is summed for its slot and must be valid there (encodeSlot). Whether the
// image as a whole selects is selectSeal's question, not this builder's.
func buildSealImage(a, b *sealRecord, domain string, seed core.Hash) ([]byte, error) {
	img := sealImageTemplate()
	if err := putSealRecord(img, a, sealSlotA, domain, seed); err != nil {
		return nil, err
	}
	if err := putSealRecord(img, b, sealSlotB, domain, seed); err != nil {
		return nil, err
	}
	return img, nil
}

// putSealRecord encodes rec into slot of img, leaving the slot as it is when rec is nil.
func putSealRecord(img []byte, rec *sealRecord, slot sealSlot, domain string, seed core.Hash) error {
	if rec == nil {
		return nil
	}
	region, _, err := encodeSlot(*rec, slot, domain, seed)
	if err != nil {
		return err
	}
	copy(img[slot.offset():], region)
	return nil
}

// newSealImage is the image a v2 seal starts from: seq 1 for the position in slot a, and slot b
// empty. It is the file a fresh journal's seal is created as (design O1), and the file a v1
// position converts to (O4a).
func newSealImage(size int64, count int, chain core.Hash, domain string, seed core.Hash) ([]byte, error) {
	return buildSealImage(&sealRecord{Seq: 1, Bytes: size, Count: count, Chain: chain}, nil, domain, seed)
}

// encodeDeliveryPositionV1 is the v1 sidecar for a position: the same json.Marshal of the same
// deliveryPosition that savePosition and saveAckPosition write, so its bytes are theirs.
// TestDeliverySeal_DowngradeWritesTodaysV1Bytes pins that.
func encodeDeliveryPositionV1(size int64, count int, chain core.Hash) ([]byte, error) {
	return json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: count, Chain: chain})
}

// writeDeliveryPositionV1 replaces the seal file at p with the v1 sidecar for a position, by
// paths.WriteAtomic with savePosition's permissions: the downgrade (design §2.9, Release step 3)
// and the offline tool's conversion to v1 (§4.5).
func writeDeliveryPositionV1(p string, size int64, count int, chain core.Hash) error {
	encoded, err := encodeDeliveryPositionV1(size, count, chain)
	if err != nil {
		return deliveryJournalError()
	}
	if err := paths.WriteAtomic(p, encoded, 0o600); err != nil {
		return deliveryJournalError()
	}
	return nil
}

// deliverySeal is one journal's v2 seal, held open for the journal's life. The lease journal and
// the ack journal each have one, bound to their own chain domain and seed. Only the operation that
// commits its journal's pipeline uses it, one at a time: it takes no lock of its own and is not
// safe for concurrent use.
type deliverySeal struct {
	f      *os.File    // the held read-write handle, from paths.OpenSharedRW
	path   string      // the seal file's path
	ident  os.FileInfo // f.Stat() at open: the file the seal writes, which the path must name
	image  []byte      // the file's bytes as the last successful write left them
	cur    sealRecord  // the effective record: the one image's newer slot holds
	seq    uint64      // cur.Seq
	domain string      // the journal's chain domain, which every record's sum binds
	seed   core.Hash   // the chain an empty journal carries
	// buf is verify's read buffer, deliverySealFileSize bytes, kept so that a check allocates none.
	buf []byte
	// syncData is paths.SyncData. It is a field only so that a test can act between a slot's write
	// and the post-seal identity check (T24); nothing else sets it.
	syncData func(*os.File) error
}

// openDeliverySeal opens the v2 seal at p for writing in place (design O4b). image is the file's
// content as the caller already read it, and it must select (selectSeal): the seal starts from
// its effective record. The file is opened with paths.OpenSharedRW, and before the seal is
// returned the path must still name the opened file and that file must read back exactly image.
// Every failure closes the handle and returns deliveryJournalError(). An open never writes.
func openDeliverySeal(p string, image []byte, domain string, seed core.Hash) (*deliverySeal, error) {
	eff, _, err := selectSeal(image, domain, seed)
	if err != nil {
		return nil, err
	}
	f, err := paths.OpenSharedRW(p)
	if err != nil {
		return nil, deliveryJournalError()
	}
	s := &deliverySeal{
		f: f, path: p, image: bytes.Clone(image), cur: eff, seq: eff.Seq, domain: domain, seed: seed,
		buf: make([]byte, deliverySealFileSize), syncData: paths.SyncData,
	}
	if s.ident, err = f.Stat(); err == nil {
		err = s.verify()
	}
	if err != nil {
		_ = f.Close()
		return nil, deliveryJournalError()
	}
	return s, nil
}

// verifyIdentity is the post-seal identity check (the fix for J-B5), and check's first two steps:
// the path is a regular file of deliverySealFileSize bytes, and it is the file the handle holds.
func (s *deliverySeal) verifyIdentity() error {
	info, err := os.Lstat(paths.Long(s.path))
	if err != nil || !info.Mode().IsRegular() || info.Size() != deliverySealFileSize || !os.SameFile(info, s.ident) {
		return deliveryJournalError()
	}
	return nil
}

// verify is check's first three steps: verifyIdentity, then the held file, read through the
// handle, is byte for byte image.
func (s *deliverySeal) verify() error {
	if err := s.verifyIdentity(); err != nil {
		return err
	}
	n, err := s.f.ReadAt(s.buf, 0)
	if err != nil || n != len(s.buf) || !bytes.Equal(s.buf, s.image) {
		return deliveryJournalError()
	}
	return nil
}

// check is the seal's part of the journal's per-batch check (design §2.9), run before anything is
// appended. It passes only when all four of these hold:
//
//  1. the path is a regular file of deliverySealFileSize bytes;
//  2. the path names the file the handle holds (os.SameFile with ident);
//  3. that file, read through the handle, is image: every record, padding and static byte;
//  4. cur is the position the journal believes is sealed.
//
// Any difference is deliveryJournalError(). check never writes.
func (s *deliverySeal) check(size int64, count int, chain core.Hash) error {
	if err := s.verify(); err != nil {
		return err
	}
	if s.cur.Bytes != size || s.cur.Count != count || s.cur.Chain != chain {
		return deliveryJournalError()
	}
	return nil
}

// write seals the position (size, count, chain) as seq+1 (design §2.9). The record goes into the
// slot seq+1's parity names, which holds seq-1 and never the effective record. It is written as
// the whole region, in one WriteAt that must write all of it, and then made durable by SyncData.
// Then comes the post-seal identity check (the fix for J-B5): the record went into the HELD file,
// and it counts as sealed only if the path still names that file, so the durable seal is the one
// recovery will read. Only when all of that succeeds do image, cur and seq follow the disk.
//
// Before any I/O, write refuses a position that does not strictly extend the sealed one, in bytes
// and in entries, because the reader would refuse the image that left (selectSeal's second row).
// It refuses a position out of bounds, and a seq that would wrap, for the same reason.
//
// A failure is deliveryJournalError(). The file may then hold the old record, the new one, or a
// torn slot, and the journal must treat the seal as uncertain and poison its handle (design §3,
// row 13).
func (s *deliverySeal) write(size int64, count int, chain core.Hash) error {
	if s.seq == math.MaxUint64 || size <= s.cur.Bytes || count <= s.cur.Count {
		return deliveryJournalError()
	}
	seq := s.seq + 1
	slot := slotFor(seq)
	enc, rec, err := encodeSlot(sealRecord{Seq: seq, Bytes: size, Count: count, Chain: chain}, slot, s.domain, s.seed)
	if err != nil {
		return err
	}
	if err := s.writeSlot(slot, enc); err != nil {
		return err
	}
	if err := s.verifyIdentity(); err != nil {
		return err
	}
	copy(s.image[slot.offset():], enc)
	s.cur, s.seq = rec, seq
	return nil
}

// writeSlot writes one slot's whole region through the held handle, in one WriteAt that must write
// all of it, and makes it durable with SyncData: write's I/O, before its identity check.
func (s *deliverySeal) writeSlot(slot sealSlot, enc []byte) error {
	n, err := s.f.WriteAt(enc, int64(slot.offset()))
	if err != nil || n != len(enc) {
		return deliveryJournalError()
	}
	if err := s.syncData(s.f); err != nil {
		return deliveryJournalError()
	}
	return nil
}

// close closes the held handle. It writes nothing: the downgrade a clean Release takes afterwards
// is downgradeToV1.
func (s *deliverySeal) close() error { return s.f.Close() }

// downgradeToV1 replaces the seal file with the v1 sidecar for the last sealed position
// (writeDeliveryPositionV1), so that an older binary finds a file it reads. It is the step a clean
// Release takes after close (design §2.9, Release step 3).
func (s *deliverySeal) downgradeToV1() error {
	return writeDeliveryPositionV1(s.path, s.cur.Bytes, s.cur.Count, s.cur.Chain)
}
