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
	p := filepath.Join(filepath.Dir(j.path), deliveryPositionFile)
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
		(position.Bytes == 0 && position.Chain != deliveryChainSeed) {
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
	j.closed = true
	return nil
}

func deliveryJournalError() error {
	return fmt.Errorf("%w: delivery journal unavailable; preserve it for recovery", core.ErrDegraded)
}
