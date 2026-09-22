package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Read-ONLY segmented views for the offline delivery-seal tool (delivery_seal_tool.go).
//
// The daemon's openers (openDeliverySegments / openDeliveryGenerations) CREATE files (O_CREATE log,
// Mkdir) and RECOVER by WRITING a head — safe for a live owner, but exactly what an offline `--check`
// must never do to evidence. These views open only existing files and write nothing: no O_CREATE, no
// Mkdir, no writeHead, no rename.
//
// # Confinement (what it does and does not promise)
//
// Every directory is reached by pinning it from the trusted state root through main's pinDeliveryChild:
// the delivery-generations, delivery-segments and each zero-padded sequence directory are opened as an
// os.Root only after Lstat rejects a symlink/irregular STATIC alias and SameFile confirms the opened
// identity, and every file (head, log, pages, journals, seals) is then read THROUGH that pinned root, so
// a symlink component cannot redirect a read. The required files are re-verified under the pinned
// identity after the scan. This checks the STATIC path and the opened-handle identity; it is NOT a claim
// of atomic protection against every dynamic path race (a directory swapped between the pin and a read),
// which is the same honest bound main's own path helpers state.
//
// A full chain scan is justified for an offline integrity tool. The manifest and authority logs are read
// as BOUNDED STREAMS (a line at a time, capped by genMaxLine / deliverySegmentMaxLine, refused past
// genMaxLog / deliverySegmentMaxLog before any large allocation). A complete committed tail beyond an
// atomic head is carried forward COHERENTLY as the recovered last view with an explicit diagnostic and
// no on-disk checkpoint mutation. Torn, conflicting, missing or unknown evidence refuses; missing
// history is never read as an empty store; migration evidence without an authority refuses.

// errSegmentReaderRefused marks any read-only integrity refusal. The tool wraps it with the file and
// reason; the sentinel keeps "refused" distinct from a genuine unmigrated tree (which returns migrated
// == false with a nil error).
var errSegmentReaderRefused = errors.New("delivery segments: read-only integrity check refused")

// segmentAuthorityReading is the result of validating the authority read-only.
type segmentAuthorityReading struct {
	// transitions is every committed transition in order (seq 0..N), including a complete tail beyond
	// the head that was carried forward as the recovered view.
	transitions []segTransition
	// recoveredTail is true when a complete, chain-valid record beyond the atomic head was carried
	// forward as the recovered view. The tool reports it; nothing is written.
	recoveredTail bool
}

// segmentSeqName is the zero-padded directory name for a segment sequence, matching the producer.
func segmentSeqName(active uint64) string {
	return fmt.Sprintf("%0*d", deliverySegmentSeqWidth, active)
}

// readonlySegmentAuthority validates the segment authority under stateDir read-only. migrated is false
// with a nil error ONLY when neither the head nor the log exists (a genuinely unmigrated tree); every
// present-but-inconsistent authority, or migration evidence without an authority, is
// errSegmentReaderRefused. It creates and writes nothing.
func readonlySegmentAuthority(ctx context.Context, stateDir string) (segmentAuthorityReading, bool, error) {
	if _, err := os.Lstat(paths.Long(stateDir)); os.IsNotExist(err) {
		return segmentAuthorityReading{}, false, nil // no state dir: unmigrated
	}
	stateRoot, err := pinDeliveryDirectory(stateDir)
	if err != nil {
		return segmentAuthorityReading{}, false, errSegmentReaderRefused
	}
	defer func() { _ = stateRoot.Close() }()

	head, hasHead, herr := readSegHead(stateRoot)
	if herr != nil {
		return segmentAuthorityReading{}, false, errSegmentReaderRefused
	}
	linfo, lerr := stateRoot.Lstat(deliverySegmentLogFile)
	hasLog := lerr == nil
	if lerr != nil && !os.IsNotExist(lerr) {
		return segmentAuthorityReading{}, false, errSegmentReaderRefused
	}
	if hasLog && (!linfo.Mode().IsRegular() || linfo.Size() > deliverySegmentMaxLog) {
		return segmentAuthorityReading{}, false, errSegmentReaderRefused
	}
	if !hasHead {
		if hasLog && linfo.Size() > 0 {
			return segmentAuthorityReading{}, false, errSegmentReaderRefused // log without head: head loss
		}
		// A generation store or segments directory surviving without an authority is head loss, never a
		// genuinely unmigrated tree — refuse rather than checking only the legacy segment.
		if migrationEvidenceExists(stateDir) {
			return segmentAuthorityReading{}, false, errSegmentReaderRefused
		}
		return segmentAuthorityReading{}, false, nil // no authority at all: unmigrated
	}
	if !hasLog {
		return segmentAuthorityReading{}, false, errSegmentReaderRefused
	}
	transitions, tail, err := scanAuthorityChain(ctx, stateRoot, head, linfo)
	if err != nil {
		return segmentAuthorityReading{}, false, err
	}
	return segmentAuthorityReading{transitions: transitions, recoveredTail: tail}, true, nil
}

// scanAuthorityChain streams the WHOLE transition log and validates it as a chain from seq 0: dense
// sequences, a DENSE active progression (transition 0 is active 0, each later transition is exactly
// prevActive+1 — the producer's protocol, so a skipped/committed segment is rejected), each record
// chained from its predecessor, and the atomic head matching the record ending at head.LogBytes. Records
// beyond the head are a complete chain-valid tail (carried forward as the recovered view) or a torn
// trailer (refused). It reads a bounded line at a time.
func scanAuthorityChain(ctx context.Context, root *os.Root, head segHead, linfo os.FileInfo) ([]segTransition, bool, error) {
	f, opened, err := openConfinedFile(root, deliverySegmentLogFile, linfo)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	size := opened.Size()

	r := bufio.NewReaderSize(io.LimitReader(f, deliverySegmentMaxLog+1), deliverySegmentMaxLine)
	var (
		transitions []segTransition
		prevChain         = segChainSeed
		prevSeq     int64 = -1
		prevActive  uint64
		offset      int64
		headMatched bool
	)
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line, complete, rerr := readBoundedLine(r)
		if rerr != nil {
			return nil, false, rerr
		}
		if line == nil {
			break // clean EOF at a record boundary
		}
		if !complete {
			return nil, false, errSegmentReaderRefused // torn trailing partial: preserve, refuse
		}
		rec, chain, ok := decodeSegTransition(line)
		if !ok || rec.Seq != prevSeq+1 || chain != segChain(prevChain, rec.Seq, rec.Active, rec.BaseRoot) {
			return nil, false, errSegmentReaderRefused
		}
		if rec.Seq == 0 {
			if rec.Active != 0 || rec.BaseRoot != "" {
				return nil, false, errSegmentReaderRefused
			}
		} else if prevActive == math.MaxUint64 || rec.Active != prevActive+1 || !validSegBaseRoot(rec.Active, rec.BaseRoot) {
			return nil, false, errSegmentReaderRefused // dense +1 progression, overflow-guarded
		}
		offset += int64(len(line))
		if offset > size {
			return nil, false, errSegmentReaderRefused
		}
		transitions = append(transitions, rec)
		prevChain, prevSeq, prevActive = chain, rec.Seq, rec.Active
		if offset == head.LogBytes {
			if rec.Seq != head.Seq || rec.Active != head.Active || rec.BaseRoot != head.BaseRoot ||
				hex.EncodeToString(chain[:]) != head.Chain {
				return nil, false, errSegmentReaderRefused
			}
			headMatched = true
		}
	}
	if !headMatched || offset != size {
		return nil, false, errSegmentReaderRefused // head not on a boundary/ahead, or trailing bytes
	}
	return transitions, offset > head.LogBytes, nil
}

// genReadonly is a READ-ONLY view of the generation store: a validated manifest chain + head, and a
// read-only radix over the existing pages (pinned from the state root, never created). It resolves a
// lease or a session's arrival at any committed root, with the producer's exact full-key and content-
// hash validation.
type genReadonly struct {
	root  radixHash // the recovered latest committed root (head root, or a coherent tail root)
	radix *deliveryRadix
}

// openGenReadonly validates the generation manifest+head chain read-only, pinned from stateRoot, and
// opens the pages read-only. recoveredTail is true when a complete committed generation beyond the head
// was carried forward as the recovered root. It creates nothing. A missing store, or any inconsistency,
// is refused — never read as empty.
func openGenReadonly(ctx context.Context, stateRoot *os.Root) (*genReadonly, bool, error) {
	genRoot, err := pinDeliveryChild(stateRoot, deliveryGenerationsDirName, false)
	if err != nil {
		return nil, false, errSegmentReaderRefused // a migrated store must have its generation store
	}
	defer func() { _ = genRoot.Close() }()

	head, hasHead, err := readGenHead(genRoot)
	if err != nil || !hasHead {
		return nil, false, errSegmentReaderRefused
	}
	recovered, tail, err := scanGenChain(ctx, genRoot, head)
	if err != nil {
		return nil, false, err
	}

	pagesRoot, err := pinDeliveryChild(genRoot, genPagesDir, false) // an independent handle; genRoot may close
	if err != nil {
		return nil, false, errSegmentReaderRefused
	}
	rx := &deliveryRadix{dir: genPagesDir, root: pagesRoot}
	rx.hashKey = rx.defaultKeyHash
	return &genReadonly{root: recovered, radix: rx}, tail, nil
}

func (g *genReadonly) close() error {
	if g == nil || g.radix == nil {
		return nil
	}
	return g.radix.close()
}

// scanGenChain streams the whole generation manifest as a chain from seq 0 (bounded: refused past
// genMaxLog, a line at a time capped by genMaxLine) and confirms the atomic head matches the record
// ending at head.LogBytes. A complete committed tail beyond the head is carried forward COHERENTLY: the
// returned root is the tail's last root (recoveredTail true), not the head's, so the view is consistent
// rather than proclaiming integrity while ignoring committed generations. It writes nothing.
func scanGenChain(ctx context.Context, root *os.Root, head genHead) (radixHash, bool, error) {
	linfo, lerr := root.Lstat(genLogFile)
	if lerr != nil || !linfo.Mode().IsRegular() || linfo.Size() > genMaxLog || linfo.Size() < head.LogBytes {
		return radixHash{}, false, errSegmentReaderRefused
	}
	f, opened, err := openConfinedFile(root, genLogFile, linfo)
	if err != nil {
		return radixHash{}, false, err
	}
	defer func() { _ = f.Close() }()
	size := opened.Size()

	r := bufio.NewReaderSize(io.LimitReader(f, genMaxLog+1), genMaxLine)
	var (
		prevChain       = genChainSeed
		prevSeq   int64 = -1
		offset    int64
		lastRoot  radixHash
		headSeen  bool
	)
	for {
		if err := ctx.Err(); err != nil {
			return radixHash{}, false, err
		}
		line, complete, rerr := readBoundedLine(r)
		if rerr != nil {
			return radixHash{}, false, rerr
		}
		if line == nil {
			break
		}
		if !complete {
			return radixHash{}, false, errSegmentReaderRefused // torn trailing partial
		}
		var rec genRecord
		rroot, chain, ok := decodeGenRecord(line, &rec)
		if !ok || rec.Seq != prevSeq+1 || chain != genChain(prevChain, rroot) {
			return radixHash{}, false, errSegmentReaderRefused
		}
		offset += int64(len(line))
		if offset > size {
			return radixHash{}, false, errSegmentReaderRefused
		}
		prevChain, prevSeq, lastRoot = chain, rec.Seq, rroot
		if offset == head.LogBytes {
			if rec.Seq != head.Seq || hex.EncodeToString(rroot[:]) != head.Root || hex.EncodeToString(chain[:]) != head.Chain {
				return radixHash{}, false, errSegmentReaderRefused
			}
			headSeen = true
		}
	}
	if !headSeen || offset != size {
		return radixHash{}, false, errSegmentReaderRefused
	}
	if offset > head.LogBytes {
		return lastRoot, true, nil // carry the committed tail forward as the recovered root
	}
	return mustRadixHash(head.Root), false, nil
}

// arrivalAt resolves a session's last committed arrival at a given root, read-only. found is false only
// on a proven absence; a corrupt/missing page is an error the caller MUST propagate (never a false new
// session).
func (g *genReadonly) arrivalAt(ctx context.Context, root radixHash, session core.SessionID) (uint64, bool, error) {
	raw, found, err := g.radix.lookup(ctx, root, arrivalGenKey(session))
	if err != nil {
		return 0, false, errSegmentReaderRefused
	}
	if !found {
		return 0, false, nil
	}
	if len(raw) != 8 {
		return 0, false, errSegmentReaderRefused
	}
	return binary.BigEndian.Uint64(raw), true, nil
}

// resolveLease resolves the canonical lease recorded for delivery at the recovered root, read-only, with
// the producer's exact full-key and canonical-record validation (not merely a filename).
func (g *genReadonly) resolveLease(ctx context.Context, delivery string) (deliveryLease, bool, error) {
	raw, found, err := g.radix.lookup(ctx, g.root, nonceGenKey(delivery))
	if err != nil {
		return deliveryLease{}, false, errSegmentReaderRefused
	}
	if !found {
		return deliveryLease{}, false, nil
	}
	var lease deliveryLease
	if json.Unmarshal(raw, &lease) != nil {
		return deliveryLease{}, false, errSegmentReaderRefused
	}
	canon, cerr := json.Marshal(lease)
	if cerr != nil || !bytes.Equal(canon, raw) || !validDeliveryLease(lease) || lease.Delivery != delivery {
		return deliveryLease{}, false, errSegmentReaderRefused
	}
	return lease, true, nil
}

// checkLeaseJournal scans one segment's lease journal read-only THROUGH the pinned segment root, against
// its seal — canonical lines, the sealed chain/count/older checkpoint, dense per-session arrivals — but
// seeded from arrivalBase (the segment's predecessor root) so a segment carrying global arrivals passes.
// arrivalBase returns (predecessor, ok, error): a missing/corrupt predecessor page is an ERROR that
// refuses, never a silent "new session at 1". It is nil for the legacy segment (start at 1).
//
// Each lease is also checked against the generation store, by what the store must hold for it. An
// ARCHIVED segment was archived whole when it rotated, so each of its leases must be in the store with
// exactly its recorded identity. The ACTIVE segment is archived only when it rotates, so its leases are
// normally absent; one that is present (a rotation of this window was interrupted before its transition)
// must be identical, and a conflicting identity refuses. It returns the segment's leases, which the
// active segment's acknowledgement check joins against first. opts widens what the seal read admits
// (segSealOptions); accepted reports that Rule R chose the position.
func (g *genReadonly) checkLeaseJournal(ctx context.Context, segRoot *os.Root, journalName, sealName string, arrivalBase func(core.SessionID) (uint64, bool, error), archived bool, opts segSealOptions) (deliveryPosition, map[string]deliveryLease, bool, error) {
	read, err := readSealConfined(segRoot, sealName, deliveryChainSeed, deliveryChainDomain, opts)
	if err != nil {
		return deliveryPosition{}, nil, false, err
	}
	position, older := read.position, read.older
	info, f, err := readJournalConfined(segRoot, journalName, position.Bytes)
	if err != nil {
		return deliveryPosition{}, nil, false, err
	}
	defer func() { _ = f.Close() }()

	leases := map[string]deliveryLease{}
	arrivals := map[core.SessionID]uint64{}
	olderSealed := older == nil || older.Bytes == 0
	sealed := position.Bytes == 0
	chain := deliveryChainSeed
	var total int64
	r := bufio.NewReaderSize(io.LimitReader(f, deliveryLeaseMaxBytes+1), deliveryLeaseMaxLine)
	for {
		if err := ctx.Err(); err != nil {
			return deliveryPosition{}, nil, false, err
		}
		line, complete, rerr := readBoundedLine(r)
		if rerr != nil {
			return deliveryPosition{}, nil, false, rerr
		}
		if line == nil {
			break
		}
		if !complete || len(leases) >= deliveryLeaseMaxEntries {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		total += int64(len(line))
		if total > deliveryLeaseMaxBytes {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		var lease deliveryLease
		if json.Unmarshal(line, &lease) != nil || !validDeliveryLease(lease) {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		canonical, merr := json.Marshal(lease)
		if merr != nil || !bytes.Equal(append(canonical, '\n'), line) {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		if _, seen := arrivals[lease.Session]; !seen && arrivalBase != nil {
			base, ok, berr := arrivalBase(lease.Session)
			if berr != nil {
				return deliveryPosition{}, nil, false, berr // a missing/corrupt predecessor page refuses, never "new"
			}
			if ok {
				arrivals[lease.Session] = base
			}
		}
		if _, exists := leases[lease.Delivery]; exists ||
			arrivals[lease.Session] == math.MaxUint64 || lease.ArrivalSeq != arrivals[lease.Session]+1 {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		recorded, found, gerr := g.resolveLease(ctx, lease.Delivery)
		if gerr != nil {
			return deliveryPosition{}, nil, false, gerr
		}
		if (archived && !found) || (found && recorded != lease) {
			return deliveryPosition{}, nil, false, errSegmentReaderRefused
		}
		leases[lease.Delivery], arrivals[lease.Session] = lease, lease.ArrivalSeq
		chain = deliveryChain(chain, line)
		if older != nil && total == older.Bytes {
			if len(leases) != older.Count || chain != older.Chain {
				return deliveryPosition{}, nil, false, errSegmentReaderRefused
			}
			olderSealed = true
		}
		if total == position.Bytes {
			if len(leases) != position.Count || chain != position.Chain {
				return deliveryPosition{}, nil, false, errSegmentReaderRefused
			}
			sealed = true
		}
	}
	if total != info.Size() || !sealed || !olderSealed {
		return deliveryPosition{}, nil, false, errSegmentReaderRefused
	}
	return deliveryPosition{Version: core.EvidenceVersion, Bytes: total, Count: len(leases), Chain: chain}, leases, read.accepted, nil
}

// checkAckJournal scans one segment's ack journal read-only THROUGH the pinned segment root, against its
// seal, and joins EVERY acknowledgement against its ORIGINAL lease with an exact identity comparison:
// the lease from the segment's own window when window is given (the active segment, whose leases are
// not archived yet), otherwise from the generation store — the archived-ACK join (a segment's ack file
// may reference a lease archived into an earlier segment). opts widens what the seal read admits
// (segSealOptions); accepted reports that Rule R chose the position. It writes nothing.
func (g *genReadonly) checkAckJournal(ctx context.Context, segRoot *os.Root, journalName, sealName string, window map[string]deliveryLease, opts segSealOptions) (deliveryPosition, bool, error) {
	read, err := readSealConfined(segRoot, sealName, deliveryAckChainSeed, deliveryAckChainDomain, opts)
	if err != nil {
		return deliveryPosition{}, false, err
	}
	position, older := read.position, read.older
	info, f, err := readJournalConfined(segRoot, journalName, position.Bytes)
	if err != nil {
		return deliveryPosition{}, false, err
	}
	defer func() { _ = f.Close() }()

	acks := map[string]deliveryAck{}
	olderSealed := older == nil || older.Bytes == 0
	sealed := position.Bytes == 0
	chain := deliveryAckChainSeed
	var total int64
	r := bufio.NewReaderSize(io.LimitReader(f, deliveryLeaseMaxBytes+1), deliveryLeaseMaxLine)
	for {
		if err := ctx.Err(); err != nil {
			return deliveryPosition{}, false, err
		}
		line, complete, rerr := readBoundedLine(r)
		if rerr != nil {
			return deliveryPosition{}, false, rerr
		}
		if line == nil {
			break
		}
		if !complete || len(acks) >= deliveryLeaseMaxEntries {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		total += int64(len(line))
		if total > deliveryLeaseMaxBytes {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		var ack deliveryAck
		if json.Unmarshal(line, &ack) != nil || ack.Version != core.EvidenceVersion ||
			!validDeliveryToken(ack.Delivery) || ack.ObservationID == "" {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		canonical, merr := json.Marshal(ack)
		if merr != nil || !bytes.Equal(append(canonical, '\n'), line) {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		if _, exists := acks[ack.Delivery]; exists {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		lease, found := window[ack.Delivery]
		if !found {
			var gerr error
			lease, found, gerr = g.resolveLease(ctx, ack.Delivery)
			if gerr != nil {
				return deliveryPosition{}, false, gerr
			}
		}
		if !found || lease.ObservationID != ack.ObservationID {
			return deliveryPosition{}, false, errSegmentReaderRefused
		}
		acks[ack.Delivery] = ack
		chain = deliveryChain(chain, line)
		if older != nil && total == older.Bytes {
			if len(acks) != older.Count || chain != older.Chain {
				return deliveryPosition{}, false, errSegmentReaderRefused
			}
			olderSealed = true
		}
		if total == position.Bytes {
			if len(acks) != position.Count || chain != position.Chain {
				return deliveryPosition{}, false, errSegmentReaderRefused
			}
			sealed = true
		}
	}
	if total != info.Size() || !sealed || !olderSealed {
		return deliveryPosition{}, false, errSegmentReaderRefused
	}
	return deliveryPosition{Version: core.EvidenceVersion, Bytes: total, Count: len(acks), Chain: chain}, read.accepted, nil
}

// ── confined IO helpers ────────────────────────────────────────────────────────────────────────────

// openConfinedFile opens name through the pinned root and confirms the opened handle is the file Lstat
// saw (SameFile) and a regular file of the same size. It writes nothing.
func openConfinedFile(root *os.Root, name string, info os.FileInfo) (*os.File, os.FileInfo, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, errSegmentReaderRefused
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		_ = f.Close()
		return nil, nil, errSegmentReaderRefused
	}
	return f, opened, nil
}

// readJournalConfined opens a segment journal read-only through the pinned root for a bounded scan,
// refusing a non-regular file, an oversize file, a file smaller than its seal claims, or one whose
// pinned name no longer resolves to the opened file.
func readJournalConfined(root *os.Root, name string, sealedBytes int64) (os.FileInfo, *os.File, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliveryLeaseMaxBytes || info.Size() < sealedBytes {
		return nil, nil, errSegmentReaderRefused
	}
	f, opened, err := openConfinedFile(root, name, info)
	if err != nil {
		return nil, nil, err
	}
	return opened, f, nil
}

// segSealOptions widens what readSealConfined admits for one segment, and only ever for the segment
// the caller has established it applies to.
type segSealOptions struct {
	// frozenOK admits a frozen document: the archived legacy segment 0 only (delivery_frozen_seal.go).
	frozenOK bool
	// ruleR admits Rule R — one valid slot beside one torn slot, taking the valid record — with the
	// operator's confirmation, and only on the ACTIVE segment: the only seal a crash can tear in the
	// middle of a slot write, since an archived segment's last seal completed before it rotated.
	ruleR bool
}

// segSealRead is what readSealConfined made of one seal: the position to scan against, the older
// record for the scan's checkpoint, and whether Rule R chose the position.
type segSealRead struct {
	position deliveryPosition
	older    *sealRecord
	accepted bool
}

// readSealConfined reads a position seal through the pinned root, bounded, and decodes it with the
// producer's PURE decoders: a v2 image via selectSeal (a torn slot is refused unless opts.ruleR), a
// frozen document when opts.frozenOK, otherwise the v1 sidecar. It writes nothing.
func readSealConfined(root *os.Root, name string, seed core.Hash, domain string, opts segSealOptions) (segSealRead, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliverySealFileSize {
		return segSealRead{}, errSegmentReaderRefused
	}
	f, opened, err := openConfinedFile(root, name, info)
	if err != nil {
		return segSealRead{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, deliverySealFileSize+1))
	if err != nil || int64(len(raw)) != opened.Size() {
		return segSealRead{}, errSegmentReaderRefused
	}
	if len(raw) == deliverySealFileSize && isDeliverySealImage(raw) {
		eff, older, serr := selectSeal(raw, domain, seed)
		if serr == nil {
			return segSealRead{position: sealedPosition(eff), older: older}, nil
		}
		if !opts.ruleR {
			return segSealRead{}, serr
		}
		valid, rerr := sealRuleR(raw, domain, seed)
		if rerr != nil {
			return segSealRead{}, serr // refused for a reason Rule R does not cover
		}
		return segSealRead{position: sealedPosition(valid), accepted: true}, nil
	}
	if opts.frozenOK {
		if position, ok := parseFrozenSeal(raw, seed); ok {
			return segSealRead{position: position}, nil
		}
	}
	position, perr := parseDeliveryPositionV1(raw, seed)
	if perr != nil {
		return segSealRead{}, perr
	}
	return segSealRead{position: position}, nil
}

// parseDeliveryPositionV1 validates a v1 position sidecar's BYTES with loadDeliveryPosition's exact
// checks, so a seal read through a pinned root need not re-open it by absolute path.
func parseDeliveryPositionV1(encoded []byte, seed core.Hash) (deliveryPosition, error) {
	if len(encoded) > deliveryLeaseMaxLine {
		return deliveryPosition{}, errSegmentReaderRefused
	}
	var position deliveryPosition
	if json.Unmarshal(encoded, &position) != nil || position.Version != core.EvidenceVersion ||
		position.Bytes < 0 || position.Bytes > deliveryLeaseMaxBytes ||
		position.Count < 0 || position.Count > deliveryLeaseMaxEntries ||
		(position.Bytes == 0) != (position.Count == 0) || position.Chain.IsZero() ||
		(position.Bytes == 0 && position.Chain != seed) {
		return deliveryPosition{}, errSegmentReaderRefused
	}
	canonical, err := json.Marshal(position)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return deliveryPosition{}, errSegmentReaderRefused
	}
	return position, nil
}

// readBoundedLine reads one newline-terminated record from r. It returns (line, true, nil) for a full
// record, (partial, false, nil) for a torn trailing record at EOF, (nil, false, nil) at a clean EOF, and
// refuses a line longer than the reader's buffer (bufio.ErrBufferFull) or any other read error.
func readBoundedLine(r *bufio.Reader) ([]byte, bool, error) {
	line, err := r.ReadSlice('\n')
	switch {
	case err == nil:
		out := make([]byte, len(line))
		copy(out, line)
		return out, true, nil
	case err == io.EOF:
		if len(line) == 0 {
			return nil, false, nil // clean EOF
		}
		out := make([]byte, len(line))
		copy(out, line)
		return out, false, nil // torn trailing partial
	default:
		return nil, false, errSegmentReaderRefused // ErrBufferFull (line too long) or an IO error
	}
}
