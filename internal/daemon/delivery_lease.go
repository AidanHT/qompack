package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// These are format/admission safety bounds. Reaching one refuses new assignments without
// dropping leases; measured retention/compaction is a separate migration task.
const (
	deliveryLeaseFile       = "delivery-leases.jsonl"
	deliveryPositionFile    = "delivery-lease-position.json"
	deliveryChainDomain     = "qompack.delivery.lease-chain.v1"
	deliveryLeaseMaxLine    = 64 << 10
	deliveryLeaseMaxBytes   = 64 << 20
	deliveryLeaseMaxEntries = 65536
)

var deliveryChainSeed = core.HashBytes(deliveryChainDomain, nil)

// deliveryLease is an additive assignment record, not an object/reference publication or ack.
// Delivery is a caller-minted per-delivery nonce, never a host ID or a content identity.
// RequestHash binds retries to the same permitted request; it does not prove object durability.
type deliveryLease struct {
	Version       int                `json:"v"`
	Delivery      string             `json:"delivery"`
	Session       core.SessionID     `json:"session"`
	RequestHash   core.Hash          `json:"request"`
	ArrivalSeq    uint64             `json:"arrival"`
	ObservationID core.ObservationID `json:"observation_id"`
}

type deliveryJournalWriter interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type deliveryJournal struct {
	owner    *Lock
	path     string
	file     *os.File
	writer   deliveryJournalWriter
	bytes    int64
	leases   map[string]deliveryLease
	arrivals map[core.SessionID]uint64
	fault    error
	closed   bool
	chain    core.Hash

	// The committed-frontier half. It is a second file under the same held Lock and the same
	// mutex, never a second record kind in the lease file — see the acknowledge section below.
	ackPath   string
	ackFile   *os.File
	ackWriter deliveryJournalWriter
	ackBytes  int64
	ackChain  core.Hash
	acks      map[string]deliveryAck
}

type deliveryPosition struct {
	Version int       `json:"v"`
	Bytes   int64     `json:"bytes"`
	Count   int       `json:"count"`
	Chain   core.Hash `json:"chain"`
}

func (l *Lock) openDeliveryJournal() (*deliveryJournal, error) {
	if l == nil {
		return nil, deliveryJournalError()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.owned() || l.journalOpenFault {
		return nil, deliveryJournalError()
	}
	if l.journal != nil {
		if l.journal.closed || l.journal.fault != nil {
			return nil, deliveryJournalError()
		}
		return l.journal, nil
	}
	l.journalOpenFault = true // clear only after a fully recovered writer is ready
	// The path is derived from this held lock, never from caller-controlled session/nonce text.
	p := filepath.Join(filepath.Dir(filepath.Dir(l.path)), "state", deliveryLeaseFile)
	positionPath := filepath.Join(filepath.Dir(p), deliveryPositionFile)
	_, journalErr := os.Lstat(paths.Long(p))
	_, positionErr := os.Lstat(paths.Long(positionPath))
	if os.IsNotExist(journalErr) && os.IsNotExist(positionErr) {
		if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
			return nil, deliveryJournalError()
		}
		if err := paths.WriteAtomic(p, nil, 0o600); err != nil {
			return nil, deliveryJournalError()
		}
		initial, _ := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Chain: deliveryChainSeed})
		if err := paths.WriteAtomic(positionPath, initial, 0o600); err != nil {
			return nil, deliveryJournalError()
		}
	} else if journalErr != nil || positionErr != nil {
		return nil, deliveryJournalError()
	}
	j := &deliveryJournal{
		owner: l, path: p, chain: deliveryChainSeed, leases: map[string]deliveryLease{}, arrivals: map[core.SessionID]uint64{},
	}
	info, err := j.load()
	if err != nil {
		return nil, err // preserve every byte of an untrusted/torn journal; never append past it
	}
	f, err := paths.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, deliveryJournalError()
	}
	j.file, j.writer = f, f
	l.journal = j // ownership retains even an uncertain close on a failed open
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != j.bytes {
		j.fault = deliveryJournalError()
		_ = j.closeLocked()
		return nil, deliveryJournalError()
	}
	// A complete tail can survive an uncertain append/position write without being acknowledged.
	// Re-sync it and seal the recovered position before any caller can reuse its assignments.
	if err := f.Sync(); err != nil {
		j.fault = deliveryJournalError()
		_ = j.closeLocked()
		return nil, deliveryJournalError()
	}
	if err := j.savePosition(j.bytes, len(j.leases), j.chain); err != nil {
		j.fault = deliveryJournalError()
		_ = j.closeLocked()
		return nil, err
	}
	// The acknowledgement journal is recovered under the same held lock and in the same open, so a
	// caller can never see assignments without the frontier that decides which of them are done.
	if err := j.openAckLocked(); err != nil {
		j.fault = deliveryJournalError()
		_ = j.closeLocked()
		return nil, err
	}
	l.journalOpenFault = false
	return j, nil
}

func (j *deliveryJournal) lease(ctx context.Context, delivery string, session core.SessionID, request core.Hash) (deliveryLease, error) {
	if j == nil || j.owner == nil {
		return deliveryLease{}, deliveryJournalError()
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return deliveryLease{}, err
	}
	if j.closed || j.fault != nil || !j.owner.owned() {
		return deliveryLease{}, deliveryJournalError()
	}
	if !validDeliveryToken(delivery) || request.IsZero() || !utf8.ValidString(string(session)) {
		return deliveryLease{}, core.ErrContract
	}
	if err := j.checkFile(); err != nil {
		j.fault = err
		return deliveryLease{}, err
	}
	if old, ok := j.leases[delivery]; ok {
		if old.Session != session || old.RequestHash != request {
			return deliveryLease{}, core.ErrAppendOnly
		}
		return old, nil
	}
	if len(j.leases) >= deliveryLeaseMaxEntries || j.arrivals[session] == math.MaxUint64 {
		return deliveryLease{}, core.ErrBudget
	}
	arrival := j.arrivals[session] + 1
	id, err := core.NewObservationID(session, arrival)
	if err != nil {
		return deliveryLease{}, core.ErrContract
	}
	lease := deliveryLease{
		Version: core.EvidenceVersion, Delivery: delivery, Session: session, RequestHash: request,
		ArrivalSeq: arrival, ObservationID: id,
	}
	line, err := json.Marshal(lease)
	if err != nil {
		return deliveryLease{}, core.ErrContract
	}
	line = append(line, '\n')
	if len(line) > deliveryLeaseMaxLine || j.bytes+int64(len(line)) > deliveryLeaseMaxBytes {
		return deliveryLease{}, core.ErrBudget
	}
	if err := ctx.Err(); err != nil {
		return deliveryLease{}, err
	}
	n, err := j.writer.Write(line)
	if err != nil || n != len(line) {
		j.fault = deliveryJournalError()
		return deliveryLease{}, j.fault
	}
	if err := j.writer.Sync(); err != nil {
		j.fault = deliveryJournalError()
		return deliveryLease{}, j.fault
	}
	chain := deliveryChain(j.chain, line)
	if err := j.savePosition(j.bytes+int64(len(line)), len(j.leases)+1, chain); err != nil {
		j.fault = deliveryJournalError()
		return deliveryLease{}, j.fault
	}
	// Only synced bytes enter the running identity maps. An uncertain write poisons this handle
	// and requires reload; a complete surviving row then retains its identity on retry.
	j.bytes += int64(len(line))
	j.chain = chain
	j.leases[delivery], j.arrivals[session] = lease, arrival
	return lease, nil
}

func (j *deliveryJournal) load() (os.FileInfo, error) {
	position, err := j.loadPosition()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(paths.Long(j.path))
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliveryLeaseMaxBytes || info.Size() < position.Bytes {
		return nil, deliveryJournalError()
	}
	f, err := os.Open(paths.Long(j.path))
	if err != nil {
		return nil, deliveryJournalError()
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return nil, deliveryJournalError()
	}
	r := bufio.NewReaderSize(io.LimitReader(f, deliveryLeaseMaxBytes+1), deliveryLeaseMaxLine)
	sealed := position.Bytes == 0
	for {
		line, err := r.ReadSlice('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil || len(j.leases) >= deliveryLeaseMaxEntries {
			return nil, deliveryJournalError()
		}
		j.bytes += int64(len(line))
		if j.bytes > deliveryLeaseMaxBytes {
			return nil, deliveryJournalError()
		}
		var lease deliveryLease
		if json.Unmarshal(line, &lease) != nil || !validDeliveryLease(lease) {
			return nil, deliveryJournalError()
		}
		canonical, err := json.Marshal(lease)
		if err != nil || !bytes.Equal(append(canonical, '\n'), line) {
			return nil, deliveryJournalError() // reject duplicate/unknown keys and noncanonical lines
		}
		if _, exists := j.leases[lease.Delivery]; exists ||
			j.arrivals[lease.Session] == math.MaxUint64 || lease.ArrivalSeq != j.arrivals[lease.Session]+1 {
			return nil, deliveryJournalError()
		}
		j.leases[lease.Delivery], j.arrivals[lease.Session] = lease, lease.ArrivalSeq
		j.chain = deliveryChain(j.chain, line)
		if j.bytes == position.Bytes {
			if len(j.leases) != position.Count || j.chain != position.Chain {
				return nil, deliveryJournalError()
			}
			sealed = true
		}
	}
	if j.bytes != info.Size() || !sealed {
		return nil, deliveryJournalError()
	}
	return info, nil
}

func deliveryChain(previous core.Hash, line []byte) core.Hash {
	input := make([]byte, 0, len(previous)+len(line))
	input = append(input, previous[:]...)
	input = append(input, line...)
	return core.HashBytes(deliveryChainDomain, input)
}

func (j *deliveryJournal) savePosition(size int64, count int, chain core.Hash) error {
	encoded, err := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: count, Chain: chain})
	if err != nil {
		return deliveryJournalError()
	}
	if err := paths.WriteAtomic(filepath.Join(filepath.Dir(j.path), deliveryPositionFile), encoded, 0o600); err != nil {
		return deliveryJournalError()
	}
	return nil
}

func (j *deliveryJournal) loadPosition() (deliveryPosition, error) {
	return loadDeliveryPosition(filepath.Join(filepath.Dir(j.path), deliveryPositionFile), deliveryChainSeed)
}

// loadDeliveryPosition validates one sealed position sidecar. seed is the chain value an empty
// journal must carry, which is what keeps the lease and acknowledgement files from ever being
// recovered against each other's chain.
func loadDeliveryPosition(p string, seed core.Hash) (deliveryPosition, error) {
	info, err := os.Lstat(paths.Long(p))
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliveryLeaseMaxLine {
		return deliveryPosition{}, deliveryJournalError()
	}
	f, err := os.Open(paths.Long(p))
	if err != nil {
		return deliveryPosition{}, deliveryJournalError()
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return deliveryPosition{}, deliveryJournalError()
	}
	encoded, err := io.ReadAll(io.LimitReader(f, deliveryLeaseMaxLine+1))
	if err != nil || len(encoded) > deliveryLeaseMaxLine {
		return deliveryPosition{}, deliveryJournalError()
	}
	var position deliveryPosition
	if json.Unmarshal(encoded, &position) != nil || position.Version != core.EvidenceVersion ||
		position.Bytes < 0 || position.Bytes > deliveryLeaseMaxBytes ||
		position.Count < 0 || position.Count > deliveryLeaseMaxEntries ||
		(position.Bytes == 0) != (position.Count == 0) || position.Chain.IsZero() ||
		(position.Bytes == 0 && position.Chain != seed) {
		return deliveryPosition{}, deliveryJournalError()
	}
	canonical, err := json.Marshal(position)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return deliveryPosition{}, deliveryJournalError()
	}
	return position, nil
}

func validDeliveryToken(token string) bool {
	if len(token) != 64 || token != strings.ToLower(token) || token == strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func validDeliveryLease(lease deliveryLease) bool {
	if lease.Version != core.EvidenceVersion || !validDeliveryToken(lease.Delivery) ||
		lease.RequestHash.IsZero() || !utf8.ValidString(string(lease.Session)) {
		return false
	}
	id, err := core.NewObservationID(lease.Session, lease.ArrivalSeq)
	return err == nil && id == lease.ObservationID
}

func (j *deliveryJournal) checkFile() error {
	position, err := j.loadPosition()
	if err != nil || position.Bytes != j.bytes || position.Count != len(j.leases) || position.Chain != j.chain {
		return deliveryJournalError()
	}
	info, err := os.Lstat(paths.Long(j.path))
	if err != nil || !info.Mode().IsRegular() || info.Size() != j.bytes {
		return deliveryJournalError()
	}
	opened, err := j.file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != j.bytes {
		return deliveryJournalError()
	}
	return nil
}

// closeLocked requires the owner mutex, the same boundary that serializes lease and Release.
func (j *deliveryJournal) closeLocked() error {
	if j.closed {
		return nil
	}
	if err := j.writer.Close(); err != nil {
		j.fault = deliveryJournalError()
		return j.fault
	}
	// The lease handle is closed first and the ack handle is dropped once closed, so a retry after
	// a failed close never closes the same descriptor twice.
	if w := j.ackWriter; w != nil {
		j.ackWriter = nil
		if err := w.Close(); err != nil {
			j.fault = deliveryJournalError()
			return j.fault
		}
	}
	j.closed = true
	return nil
}

func deliveryJournalError() error {
	return fmt.Errorf("%w: delivery journal unavailable; preserve it for recovery", core.ErrDegraded)
}

// ---------------------------------------------------------------------------
// Committed frontier (invariant 3, T20-M1-03)
//
// A lease is an ASSIGNMENT: it says a delivery has an identity and may be worked on. It says
// nothing about whether the durable object and its reference were both written. The acknowledgement
// journal is the third and last stage of publication order — durable object, verified reference,
// then committed frontier — and it is a SEPARATE file on purpose:
//
//   - delivery-leases.jsonl is read structurally by store GC, which treats every line in it as an
//     OPEN lease and therefore as a retention root. Writing acks into the same file would make a
//     completed delivery look like an open one to a reader that cannot parse record kinds, and
//     would break the journal's own load() invariants (one line per delivery, dense per-session
//     arrival sequences, every line canonical).
//   - Retention stays conservative in the safe direction: an acknowledged delivery is still named
//     by its lease line, so GC keeps holding whatever that line referenced.
//
// The append-then-sync-then-position pattern is the lease journal's own, reused verbatim: bytes
// reach the file and are synced, the position sidecar is sealed, and only then does the in-memory
// set admit the record. An uncertain write poisons this handle and requires a reload, at which
// point a complete surviving row is recovered and an incomplete one is refused.
const (
	deliveryAckFile         = "delivery-acks.jsonl"
	deliveryAckPositionFile = "delivery-ack-position.json"
	deliveryAckChainDomain  = "qompack.delivery.ack-chain.v1"
)

var deliveryAckChainSeed = core.HashBytes(deliveryAckChainDomain, nil)

// deliveryAck records that one leased delivery reached committed publication. Root names the
// durable object the publication rests on when the publisher reported one; a zero Root is an
// acknowledgement with no object of its own (an op that publishes no content), never an unproven
// claim about one.
type deliveryAck struct {
	Version       int                `json:"v"`
	Delivery      string             `json:"delivery"`
	ObservationID core.ObservationID `json:"observation_id"`
	Root          core.Hash          `json:"root"`
}

// openAckLocked prepares the acknowledgement journal beside the lease journal. It is called from
// openDeliveryJournal with the owner mutex held and follows the same create-both-or-neither rule: a
// project that already has a lease journal but no ack journal (any store written before this stage
// existed) gets one; a half-present pair is a recovery decision, not a repair this makes.
func (j *deliveryJournal) openAckLocked() error {
	j.ackPath = filepath.Join(filepath.Dir(j.path), deliveryAckFile)
	positionPath := filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile)
	j.ackChain, j.acks = deliveryAckChainSeed, map[string]deliveryAck{}
	_, journalErr := os.Lstat(paths.Long(j.ackPath))
	_, positionErr := os.Lstat(paths.Long(positionPath))
	if os.IsNotExist(journalErr) && os.IsNotExist(positionErr) {
		if err := paths.WriteAtomic(j.ackPath, nil, 0o600); err != nil {
			return deliveryJournalError()
		}
		initial, _ := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Chain: deliveryAckChainSeed})
		if err := paths.WriteAtomic(positionPath, initial, 0o600); err != nil {
			return deliveryJournalError()
		}
	} else if journalErr != nil || positionErr != nil {
		return deliveryJournalError()
	}
	info, err := j.loadAcks()
	if err != nil {
		return err
	}
	f, err := paths.OpenFile(j.ackPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return deliveryJournalError()
	}
	j.ackFile, j.ackWriter = f, f
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != j.ackBytes {
		return deliveryJournalError()
	}
	if err := f.Sync(); err != nil {
		return deliveryJournalError()
	}
	return j.saveAckPosition(j.ackBytes, len(j.acks), j.ackChain)
}

// acknowledge commits one leased delivery's publication. It is idempotent: a redelivery already
// acknowledged returns without appending, which is what lets a drained line advance a spool offset
// without republishing anything. It refuses to acknowledge a delivery this journal never leased, or
// one whose identity disagrees with the lease — an acknowledgement that does not name a real
// assignment is not a frontier, it is a guess.
func (j *deliveryJournal) acknowledge(ctx context.Context, delivery string, id core.ObservationID, root core.Hash) error {
	if j == nil || j.owner == nil {
		return deliveryJournalError()
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.closed || j.fault != nil || !j.owner.owned() {
		return deliveryJournalError()
	}
	if !validDeliveryToken(delivery) || id == "" {
		return core.ErrContract
	}
	lease, ok := j.leases[delivery]
	if !ok || lease.ObservationID != id {
		return core.ErrContract
	}
	if old, ok := j.acks[delivery]; ok {
		if old.ObservationID != id {
			return core.ErrAppendOnly
		}
		return nil
	}
	if err := j.checkAckFile(); err != nil {
		j.fault = err
		return err
	}
	if len(j.acks) >= deliveryLeaseMaxEntries {
		return core.ErrBudget
	}
	ack := deliveryAck{Version: core.EvidenceVersion, Delivery: delivery, ObservationID: id, Root: root}
	line, err := json.Marshal(ack)
	if err != nil {
		return core.ErrContract
	}
	line = append(line, '\n')
	if len(line) > deliveryLeaseMaxLine || j.ackBytes+int64(len(line)) > deliveryLeaseMaxBytes {
		return core.ErrBudget
	}
	n, err := j.ackWriter.Write(line)
	if err != nil || n != len(line) {
		j.fault = deliveryJournalError()
		return j.fault
	}
	if err := j.ackWriter.Sync(); err != nil {
		j.fault = deliveryJournalError()
		return j.fault
	}
	chain := deliveryChain(j.ackChain, line)
	if err := j.saveAckPosition(j.ackBytes+int64(len(line)), len(j.acks)+1, chain); err != nil {
		j.fault = deliveryJournalError()
		return j.fault
	}
	j.ackBytes += int64(len(line))
	j.ackChain = chain
	j.acks[delivery] = ack
	return nil
}

// acknowledged reports whether delivery has a committed frontier record. A closed, faulted or
// unowned journal answers false: "cannot currently tell" and "not published" both mean the caller
// must not release the record that would let it retry.
func (j *deliveryJournal) acknowledged(delivery string) bool {
	if j == nil || j.owner == nil {
		return false
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if j.closed || j.fault != nil || !j.owner.owned() {
		return false
	}
	_, ok := j.acks[delivery]
	return ok
}

func (j *deliveryJournal) loadAcks() (os.FileInfo, error) {
	position, err := loadDeliveryPosition(filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile), deliveryAckChainSeed)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(paths.Long(j.ackPath))
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliveryLeaseMaxBytes || info.Size() < position.Bytes {
		return nil, deliveryJournalError()
	}
	f, err := os.Open(paths.Long(j.ackPath))
	if err != nil {
		return nil, deliveryJournalError()
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return nil, deliveryJournalError()
	}
	r := bufio.NewReaderSize(io.LimitReader(f, deliveryLeaseMaxBytes+1), deliveryLeaseMaxLine)
	sealed := position.Bytes == 0
	for {
		line, err := r.ReadSlice('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil || len(j.acks) >= deliveryLeaseMaxEntries {
			return nil, deliveryJournalError()
		}
		j.ackBytes += int64(len(line))
		if j.ackBytes > deliveryLeaseMaxBytes {
			return nil, deliveryJournalError()
		}
		var ack deliveryAck
		if json.Unmarshal(line, &ack) != nil || ack.Version != core.EvidenceVersion ||
			!validDeliveryToken(ack.Delivery) || ack.ObservationID == "" {
			return nil, deliveryJournalError()
		}
		canonical, err := json.Marshal(ack)
		if err != nil || !bytes.Equal(append(canonical, '\n'), line) {
			return nil, deliveryJournalError()
		}
		if _, exists := j.acks[ack.Delivery]; exists {
			return nil, deliveryJournalError()
		}
		j.acks[ack.Delivery] = ack
		j.ackChain = deliveryChain(j.ackChain, line)
		if j.ackBytes == position.Bytes {
			if len(j.acks) != position.Count || j.ackChain != position.Chain {
				return nil, deliveryJournalError()
			}
			sealed = true
		}
	}
	if j.ackBytes != info.Size() || !sealed {
		return nil, deliveryJournalError()
	}
	// An acknowledgement whose lease did not survive is a frontier ahead of its own assignment.
	for delivery, ack := range j.acks {
		lease, ok := j.leases[delivery]
		if !ok || lease.ObservationID != ack.ObservationID {
			return nil, deliveryJournalError()
		}
	}
	return info, nil
}

func (j *deliveryJournal) saveAckPosition(size int64, count int, chain core.Hash) error {
	encoded, err := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: count, Chain: chain})
	if err != nil {
		return deliveryJournalError()
	}
	if err := paths.WriteAtomic(filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile), encoded, 0o600); err != nil {
		return deliveryJournalError()
	}
	return nil
}

func (j *deliveryJournal) checkAckFile() error {
	position, err := loadDeliveryPosition(filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile), deliveryAckChainSeed)
	if err != nil || position.Bytes != j.ackBytes || position.Count != len(j.acks) || position.Chain != j.ackChain {
		return deliveryJournalError()
	}
	info, err := os.Lstat(paths.Long(j.ackPath))
	if err != nil || !info.Mode().IsRegular() || info.Size() != j.ackBytes {
		return deliveryJournalError()
	}
	opened, err := j.ackFile.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != j.ackBytes {
		return deliveryJournalError()
	}
	return nil
}
