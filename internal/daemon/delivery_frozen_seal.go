package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The old-reader barrier on the archived legacy segment (SP20-D4; V6 close-out C1.10, following
// rollover-compatibility-critical-review.md §2).
//
// Segment 0 is the four legacy files under state/, left in place when the store first rotates. Every
// build that predates segments resolves state/delivery-leases.jsonl as THE journal and, before it can
// append a lease, must read that journal's position seal: a v2 A/B image (readDeliverySealImage), or
// else the strict v1 sidecar (loadDeliveryPosition). An archived segment 0 whose seal still reads as
// either is openable and appendable by such a build, which would number new arrivals from segment 0's
// last ones — arrivals later segments have already assigned. Nothing in the files that build ignores
// (the segment authority, the generation store) can stop it.
//
// So when segment 0 is archived its two seals are rewritten as FROZEN documents: the same sealed
// position (bytes, count, chain) under a different, canonical JSON shape that carries no "v". Both
// pre-segment readers refuse it by construction — it is not a 32 KiB v2 image, and its missing
// version decodes as 0, never core.EvidenceVersion — so such a build's journal open fails and it cannot
// assign an identity. The journal bytes are never touched, a frozen seal is never downgraded or thawed,
// and every current reader that checks segment 0 (the offline tool, store GC, fsck) reads the frozen
// position and checks the journal against it exactly as before.
//
// This is a barrier against APPENDING, not a compatibility claim: an older build still refuses the
// store (degrading as it does for any unreadable journal), and a store is rolled back only through a
// verified backup restored by a compatible reader.

const (
	// deliveryFrozenSealFormat names the frozen document. Changing it is a format change: every
	// reader of segment 0's seal (the offline tool, store GC's mirror, fsck) must change with it.
	deliveryFrozenSealFormat = "qompack.delivery.frozen-seal.v1"
)

// deliveryFrozenSeal is the frozen document. Field order is the canonical encoding.
type deliveryFrozenSeal struct {
	Format  string    `json:"format"`
	Segment uint64    `json:"segment"`
	Bytes   int64     `json:"bytes"`
	Count   int       `json:"count"`
	Chain   core.Hash `json:"chain"`
}

// encodeFrozenSeal returns the canonical frozen document for segment 0 sealing pos. It refuses a
// position no journal reader would accept, so it never writes a seal its own reader refuses.
func encodeFrozenSeal(pos deliveryPosition, seed core.Hash) ([]byte, error) {
	if !sealInBounds(pos.Bytes, pos.Count, pos.Chain, seed) {
		return nil, deliveryJournalError()
	}
	return json.Marshal(deliveryFrozenSeal{
		Format: deliveryFrozenSealFormat, Segment: 0, Bytes: pos.Bytes, Count: pos.Count, Chain: pos.Chain,
	})
}

// parseFrozenSeal decodes raw as a canonical frozen segment-0 document for the journal whose seed is
// seed, returning the position it seals. Anything else — another shape, another segment, unknown or
// reordered keys, an out-of-bounds position — is not a frozen seal.
func parseFrozenSeal(raw []byte, seed core.Hash) (deliveryPosition, bool) {
	if len(raw) > deliveryLeaseMaxLine {
		return deliveryPosition{}, false
	}
	var doc deliveryFrozenSeal
	if json.Unmarshal(raw, &doc) != nil || doc.Format != deliveryFrozenSealFormat || doc.Segment != 0 ||
		!sealInBounds(doc.Bytes, doc.Count, doc.Chain, seed) {
		return deliveryPosition{}, false
	}
	canonical, err := json.Marshal(doc)
	if err != nil || !bytes.Equal(canonical, raw) {
		return deliveryPosition{}, false
	}
	return deliveryPosition{Version: core.EvidenceVersion, Bytes: doc.Bytes, Count: doc.Count, Chain: doc.Chain}, true
}

// FrozenDeliverySeal reports whether raw, the bytes of the legacy seal file named name (state/'s
// delivery-lease-position.json or delivery-ack-position.json), is the frozen seal of an archived
// segment 0, and the journal length and entry count it seals. It is the classifier fsck's read-only row
// uses, so fsck needs no copy of the format.
func FrozenDeliverySeal(name string, raw []byte) (sealed int64, entries int, ok bool) {
	var seed core.Hash
	switch name {
	case deliveryPositionFile:
		seed = deliveryChainSeed
	case deliveryAckPositionFile:
		seed = deliveryAckChainSeed
	default:
		return 0, 0, false
	}
	pos, ok := parseFrozenSeal(raw, seed)
	return pos.Bytes, pos.Count, ok
}

// readFrozenSealFile reads a seal file bounded and reports whether it is a frozen document, and the
// position it seals when it is. A file that cannot be read is an error, never "not frozen".
func readFrozenSealFile(p string, seed core.Hash) (deliveryPosition, bool, error) {
	info, err := os.Lstat(paths.Long(p))
	if err != nil || !info.Mode().IsRegular() {
		return deliveryPosition{}, false, deliveryJournalError()
	}
	if info.Size() > deliveryLeaseMaxLine {
		return deliveryPosition{}, false, nil // too large to be a frozen document (a v2 image is 32 KiB)
	}
	raw, err := paths.ReadFileShared(p)
	if err != nil {
		return deliveryPosition{}, false, deliveryJournalError()
	}
	pos, ok := parseFrozenSeal(raw, seed)
	return pos, ok, nil
}

// freezeLegacySeals rewrites segment 0's two seals as frozen documents sealing exactly lease and ack,
// the positions the archived journals hold. Each is replaced atomically; the journals are not touched.
// The caller has closed every handle on the two seal files.
func freezeLegacySeals(stateDir string, lease, ack deliveryPosition) error {
	for _, s := range []struct {
		name string
		pos  deliveryPosition
		seed core.Hash
	}{
		{deliveryPositionFile, lease, deliveryChainSeed},
		{deliveryAckPositionFile, ack, deliveryAckChainSeed},
	} {
		doc, err := encodeFrozenSeal(s.pos, s.seed)
		if err != nil {
			return err
		}
		if err := paths.WriteAtomic(filepath.Join(stateDir, s.name), doc, 0o600); err != nil {
			return deliveryJournalError()
		}
	}
	return nil
}

// legacySealFrozen reports whether either of segment 0's seals under stateDir is a frozen document. A
// seal file that cannot be read is an error, never "not frozen"; one that does not exist yet (a fresh
// tree) is not frozen.
func legacySealFrozen(stateDir string) (bool, error) {
	for _, s := range []struct {
		name string
		seed core.Hash
	}{{deliveryPositionFile, deliveryChainSeed}, {deliveryAckPositionFile, deliveryAckChainSeed}} {
		p := filepath.Join(stateDir, s.name)
		if _, err := os.Lstat(paths.Long(p)); os.IsNotExist(err) {
			continue
		}
		_, frozen, err := readFrozenSealFile(p, s.seed)
		if err != nil {
			return false, err
		}
		if frozen {
			return true, nil
		}
	}
	return false, nil
}

// loadLegacySegment loads segment 0's two journals into j against their seals, a frozen seal at the
// position it seals and an ordinary one through the dual reader, with the loader's own checks.
func (j *deliveryJournal) loadLegacySegment() error {
	leaseSeal, leaseFrozen, err := readFrozenSealFile(j.positionPath(), deliveryChainSeed)
	if err != nil {
		return err
	}
	ackSeal, ackFrozen, err := readFrozenSealFile(j.ackSealPath(), deliveryAckChainSeed)
	if err != nil {
		return err
	}
	if leaseFrozen {
		if _, err := j.loadFrom(leaseSeal, nil); err != nil {
			return err
		}
	} else {
		pos, older, _, err := loadDeliverySeal(j.positionPath(), deliveryChainSeed, deliveryChainDomain)
		if err != nil {
			return err
		}
		if _, err := j.loadFrom(pos, older); err != nil {
			return err
		}
	}
	if ackFrozen {
		_, err = j.loadAcksFrom(ackSeal, nil)
		return err
	}
	pos, older, _, err := loadDeliverySeal(j.ackSealPath(), deliveryAckChainSeed, deliveryAckChainDomain)
	if err != nil {
		return err
	}
	_, err = j.loadAcksFrom(pos, older)
	return err
}

// finishFrozenLegacyRotation opens a journal whose authority still names segment 0 while a seal of
// segment 0 is frozen. A rotation out of segment 0 archives the window, stages segment 1, freezes
// segment 0 and only then commits the transition (doRotate), so this is a rotation a crash stopped
// between the freeze and the commit, and it is finished here, before anything is assigned: segment 0
// is loaded against its seals and, only when the generation store already holds the window's first
// lease — the proof the window was archived, as recoverGenerations reads it — the rest of the rotation
// runs. A frozen seal with no archived window behind it names a segment 0 nothing archived, and the
// open is refused with every byte preserved: a frozen seal is never thawed into a writable journal.
func (l *Lock) finishFrozenLegacyRotation(stateDir string, seg *deliverySegments) (*deliveryJournal, error) {
	ctx := context.Background()
	j := newDeliveryJournal(l, segmentLeasePath(stateDir, 0))
	j.stateDir, j.segment, j.seg = stateDir, 0, seg
	j.arrivalBase = j.segmentArrivalBase
	j.ackPath = filepath.Join(stateDir, deliveryAckFile)
	if err := j.loadLegacySegment(); err != nil {
		_ = seg.close()
		return nil, err
	}
	first, ok := firstWindowLease(j.leases)
	if info, err := os.Lstat(paths.Long(filepath.Join(stateDir, deliveryGenerationsDirName))); !ok || err != nil || !info.IsDir() {
		_ = seg.close()
		return nil, deliveryJournalError() // no window, or no generation store: nothing was archived
	}
	if err := j.openGenerationsStore(); err != nil {
		_ = seg.close()
		return nil, err
	}
	if _, archived, err := j.gen.resolveLease(ctx, first.Delivery); err != nil || !archived {
		_ = j.gen.close()
		_ = seg.close()
		return nil, deliveryJournalError()
	}
	if err := j.loadTerminalDispositions(); err != nil {
		_ = j.gen.close()
		_ = seg.close()
		return nil, err
	}
	l.journal = j // ownership retains even an uncertain close on a failed finish
	if err := j.rotateAtOpen(ctx); err != nil {
		_ = j.poison(err)
		_ = j.closeLocked()
		return nil, err
	}
	return j, nil
}

// ensureLegacyFrozen runs at open on a store whose active segment is past 0.
//
// Both seals frozen is the normal case, and the open checks only that each journal still has exactly
// the length its frozen seal names: the daemon never reads segment 0 again (every record in it is in
// the generation store), so the full scan belongs to the offline check, not to every start. An
// ORDINARY seal there means a rotation stopped between committing its transition and freezing segment
// 0: both journals are then scanned in full against their seals with the loader's own checks, and only
// then frozen. Any disagreement refuses the open and leaves every byte as it is.
func ensureLegacyFrozen(stateDir string) error {
	j := newDeliveryJournal(nil, filepath.Join(stateDir, deliveryLeaseFile))
	j.ackPath = filepath.Join(stateDir, deliveryAckFile)

	leaseSeal, leaseFrozen, err := readFrozenSealFile(j.positionPath(), deliveryChainSeed)
	if err != nil {
		return err
	}
	ackSeal, ackFrozen, err := readFrozenSealFile(j.ackSealPath(), deliveryAckChainSeed)
	if err != nil {
		return err
	}
	if leaseFrozen && ackFrozen {
		for _, c := range []struct {
			path  string
			bytes int64
		}{{j.path, leaseSeal.Bytes}, {j.ackPath, ackSeal.Bytes}} {
			info, err := os.Lstat(paths.Long(c.path))
			if err != nil || !info.Mode().IsRegular() || info.Size() != c.bytes {
				return deliveryJournalError()
			}
		}
		return nil
	}
	if leaseFrozen {
		if _, err := j.loadFrom(leaseSeal, nil); err != nil {
			return err
		}
	} else {
		pos, older, _, err := loadDeliverySeal(j.positionPath(), deliveryChainSeed, deliveryChainDomain)
		if err != nil {
			return err
		}
		if _, err := j.loadFrom(pos, older); err != nil {
			return err
		}
	}
	if ackFrozen {
		if _, err := j.loadAcksFrom(ackSeal, nil); err != nil {
			return err
		}
	} else {
		pos, older, _, err := loadDeliverySeal(j.ackSealPath(), deliveryAckChainSeed, deliveryAckChainDomain)
		if err != nil {
			return err
		}
		if _, err := j.loadAcksFrom(pos, older); err != nil {
			return err
		}
	}
	return freezeLegacySeals(stateDir,
		deliveryPosition{Version: core.EvidenceVersion, Bytes: j.bytes, Count: len(j.leases), Chain: j.chain},
		deliveryPosition{Version: core.EvidenceVersion, Bytes: j.ackBytes, Count: len(j.acks), Chain: j.ackChain})
}
