package daemon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"

	"github.com/qompack/qompack/internal/core"
)

// The carried-lease file (SP20-D4, V6 close-out C1.10, review finding 1).
//
// Store GC retains what an OPEN delivery lease references, and a lease is open until an
// acknowledgement with its exact identity is recorded. Before this file existed, a rotated store could
// only answer "which leases are open" by reading every segment's lease and acknowledgement journals on
// every GC pass: the work and the memory of a pass grew with the project's whole delivery history, and
// the pass's acknowledgement bound (65,536 pairs, read oldest first) was filled by segment 0 alone, so
// every later segment's leases read as open for good.
//
// So each rotation writes, into the NEW segment's directory and before the transition commits, the
// archived leases that have no acknowledgement: the outgoing segment's own carry and its window, less
// every lease the window's acknowledgement journal settles by exact identity (the same join GC makes).
// The active segment's journals plus its carry then name every lease that can still be open, and GC
// reads those and nothing older (internal/store/gc_delivery_segments.go mirrors this format). A lease
// with a terminal disposition but no acknowledgement stays carried: GC never released on a terminal
// disposition, and this does not change what GC retains, only what it must read to know it.
//
// The file is written once, as the staged segment's fifth file, and never rewritten. Its header names
// the segment and the body's line count, byte length and digest, so a torn or altered body reads as
// damage — GC halts on it, and the next rotation refuses to build on it — never as fewer open leases.
const (
	deliveryCarryFile    = "delivery-carried-leases.jsonl"
	deliveryCarryFormat  = "qompack.delivery.carried-leases.v1"
	deliveryCarryVersion = 1
	// deliveryCarryMaxBytes bounds the carry a rotation reads back (the per-journal byte bound, about
	// 200,000 carried leases). Past it the rotation refuses — the journal fails closed as at its cap —
	// rather than holding an unbounded history in memory. Store GC stops harvesting far earlier, at
	// 65,536 carried leases, where it halts its pass and collects nothing.
	deliveryCarryMaxBytes = deliveryLeaseMaxBytes
)

// deliveryCarryHeader is the carry's first line. Field order is the wire order.
type deliveryCarryHeader struct {
	Version int    `json:"v"`
	Format  string `json:"format"`
	Segment uint64 `json:"segment"`
	Count   int    `json:"count"`
	Bytes   int64  `json:"bytes"`
	Digest  string `json:"digest"`
}

// encodeDeliveryCarry renders segment's carry: the header, then each lease's canonical line. leases
// must already be in (session, arrival) order with distinct nonces.
func encodeDeliveryCarry(segment uint64, leases []deliveryLease) ([]byte, error) {
	var body bytes.Buffer
	for _, l := range leases {
		if !validDeliveryLease(l) {
			return nil, core.ErrContract
		}
		line, err := json.Marshal(l)
		if err != nil {
			return nil, err
		}
		body.Write(line)
		body.WriteByte('\n')
	}
	digest := radixDigest(deliveryCarryFormat, body.Bytes())
	header, err := json.Marshal(deliveryCarryHeader{
		Version: deliveryCarryVersion, Format: deliveryCarryFormat, Segment: segment,
		Count: len(leases), Bytes: int64(body.Len()), Digest: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(header)+1+body.Len())
	out = append(append(out, header...), '\n')
	return append(out, body.Bytes()...), nil
}

// decodeDeliveryCarry reads segment's carry back, refusing anything the writer would not have written:
// a noncanonical or foreign header, a body that disagrees with it, a noncanonical or invalid lease, or
// leases out of (session, arrival) order or repeated.
func decodeDeliveryCarry(raw []byte, segment uint64) ([]deliveryLease, error) {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, errSegmentUnavailable
	}
	var h deliveryCarryHeader
	if json.Unmarshal(raw[:nl], &h) != nil || h.Version != deliveryCarryVersion || h.Format != deliveryCarryFormat ||
		h.Segment != segment || h.Count < 0 || h.Bytes < 0 {
		return nil, errSegmentUnavailable
	}
	if canon, err := json.Marshal(h); err != nil || !bytes.Equal(canon, raw[:nl]) {
		return nil, errSegmentUnavailable
	}
	body := raw[nl+1:]
	want, ok := hexToRadixHash(h.Digest)
	if !ok || int64(len(body)) != h.Bytes || radixDigest(deliveryCarryFormat, body) != want {
		return nil, errSegmentUnavailable
	}
	leases := make([]deliveryLease, 0, h.Count)
	for len(body) > 0 {
		end := bytes.IndexByte(body, '\n')
		if end < 0 {
			return nil, errSegmentUnavailable
		}
		var l deliveryLease
		if json.Unmarshal(body[:end], &l) != nil || !validDeliveryLease(l) {
			return nil, errSegmentUnavailable
		}
		if canon, err := json.Marshal(l); err != nil || !bytes.Equal(canon, body[:end]) {
			return nil, errSegmentUnavailable
		}
		if n := len(leases); n > 0 && !leaseBefore(leases[n-1], l) {
			return nil, errSegmentUnavailable
		}
		leases = append(leases, l)
		body = body[end+1:]
	}
	if len(leases) != h.Count {
		return nil, errSegmentUnavailable
	}
	return leases, nil
}

// readSegmentCarry reads a segment's carry through pinned directories, bounded by deliveryCarryMaxBytes.
func readSegmentCarry(stateDir string, seq uint64) ([]byte, error) {
	state, err := pinDeliveryDirectory(stateDir)
	if err != nil {
		return nil, errSegmentUnavailable
	}
	defer func() { _ = state.Close() }()
	segments, err := pinDeliveryChild(state, deliverySegmentsDir, false)
	if err != nil {
		return nil, errSegmentUnavailable
	}
	defer func() { _ = segments.Close() }()
	segment, err := pinDeliveryChild(segments, segmentSeqName(seq), false)
	if err != nil {
		return nil, errSegmentUnavailable
	}
	defer func() { _ = segment.Close() }()
	return readCarryConfined(segment)
}

// readCarryConfined reads the carry in a pinned segment directory: a regular file, the same file once
// opened, within deliveryCarryMaxBytes.
func readCarryConfined(segment *os.Root) ([]byte, error) {
	before, err := segment.Lstat(deliveryCarryFile)
	if err != nil || !before.Mode().IsRegular() || before.Size() > deliveryCarryMaxBytes {
		return nil, errSegmentUnavailable
	}
	f, err := segment.Open(deliveryCarryFile)
	if err != nil {
		return nil, errSegmentUnavailable
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.Size() != before.Size() {
		return nil, errSegmentUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, deliveryCarryMaxBytes+1))
	if err != nil || int64(len(raw)) != before.Size() {
		return nil, errSegmentUnavailable
	}
	return raw, nil
}

// carriedLeases is what the segment after this one carries: this segment's carry (segment 0 has none)
// and its window, less every lease its acknowledgement journal settles, in (session, arrival) order.
// It runs under the rotation barrier (or at open, before the journal is shared), so the window is read
// without st. A carried nonce that the window also holds must be the same lease.
func (j *deliveryJournal) carriedLeases() ([]deliveryLease, error) {
	byNonce := make(map[string]deliveryLease, len(j.leases))
	if j.segment >= 1 {
		raw, err := readSegmentCarry(j.stateDir, j.segment)
		if err != nil {
			return nil, err
		}
		prev, err := decodeDeliveryCarry(raw, j.segment)
		if err != nil {
			return nil, err
		}
		for _, l := range prev {
			byNonce[l.Delivery] = l
		}
	}
	for _, l := range j.leases {
		if held, ok := byNonce[l.Delivery]; ok && held != l {
			return nil, errSegmentUnavailable
		}
		byNonce[l.Delivery] = l
	}
	out := make([]deliveryLease, 0, len(byNonce))
	for _, l := range byNonce {
		if a, ok := j.acks[l.Delivery]; ok && a.Version == core.EvidenceVersion && a.ObservationID == l.ObservationID {
			continue // settled by the exact join GC makes
		}
		out = append(out, l)
	}
	sort.Slice(out, func(a, b int) bool { return leaseBefore(out[a], out[b]) })
	return out, nil
}
