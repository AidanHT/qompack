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
	"sync"
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

	// The committed-frontier half. It is a second file under the same held Lock, never a second
	// record kind in the lease file — see the acknowledge section below.
	ackPath   string
	ackFile   *os.File
	ackWriter deliveryJournalWriter
	ackBytes  int64
	ackChain  core.Hash
	acks      map[string]deliveryAck

	// st guards fault, closed, closing and inflight, and every write of the admitted state: bytes,
	// chain, leases and arrivals, and ackBytes, ackChain and acks. It is taken below Lock.mu and
	// never above it, and it is never held across I/O. The one operation that may write a
	// pipeline's admitted state can read that state without st, because nothing else writes it;
	// every other reader holds st.
	st sync.Mutex
	// idle is broadcast, with L = &st, each time inflight drops to zero; closeLocked waits on it.
	idle sync.Cond
	// closing is set by closeLocked before it waits and is never cleared, so no operation passes
	// enter once a close has begun, and Release waits only for the operations already in flight.
	closing bool
	// inflight counts the operations between enter and leave: each one may write, sync or seal.
	inflight int

	// leaseQ group-commits lease (design §2.6): lease enqueues its request and waits, and the
	// caller that leads a batch answers every request queued behind it.
	leaseQ groupQueue[*leaseReq]
	// sealLease seals one lease batch's position: savePosition, the paths.WriteAtomic of the v1
	// sidecar, in production. It is a field only so that tests can observe, hold and fail one
	// batch's seal; nothing else sets it.
	sealLease func(size int64, count int, chain core.Hash) error
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
	if l.released || l.owner == "" || l.journalOpenFault {
		return nil, deliveryJournalError()
	}
	if l.journal != nil {
		// O1 (SP20-D1 design section 2.5, flagged for the owner's countersign as Q6): an open
		// journal is handed out without reading the lock FILE. That read ran under Lock.mu on every
		// Accept, a serial section in front of the durable path. Ownership is still checked against
		// the file for every operation on the journal, only there: a lease batch re-reads it
		// (Lock.ownedByFile) once every member has arrived and before anything is appended or
		// answered, and acknowledge and acknowledged read it through owned under this mutex.
		if !l.journal.usable() {
			return nil, deliveryJournalError()
		}
		return l.journal, nil
	}
	// Opening a journal keeps the full check.
	if !l.owned() {
		return nil, deliveryJournalError()
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
	j := newDeliveryJournal(l, p)
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
		_ = j.poison(deliveryJournalError())
		_ = j.closeLocked()
		return nil, deliveryJournalError()
	}
	// A complete tail can survive an uncertain append/position write without being acknowledged.
	// Re-sync it and seal the recovered position before any caller can reuse its assignments.
	if err := f.Sync(); err != nil {
		_ = j.poison(deliveryJournalError())
		_ = j.closeLocked()
		return nil, deliveryJournalError()
	}
	if err := j.savePosition(j.bytes, len(j.leases), j.chain); err != nil {
		_ = j.poison(deliveryJournalError())
		_ = j.closeLocked()
		return nil, err
	}
	// The acknowledgement journal is recovered under the same held lock and in the same open, so a
	// caller can never see assignments without the frontier that decides which of them are done.
	if err := j.openAckLocked(); err != nil {
		_ = j.poison(deliveryJournalError())
		_ = j.closeLocked()
		return nil, err
	}
	l.journalOpenFault = false
	return j, nil
}

// newDeliveryJournal returns an empty, unloaded journal for the lease file at p, owned by l.
func newDeliveryJournal(l *Lock, p string) *deliveryJournal {
	j := &deliveryJournal{
		owner: l, path: p, chain: deliveryChainSeed, leases: map[string]deliveryLease{}, arrivals: map[core.SessionID]uint64{},
		leaseQ: groupQueue[*leaseReq]{maxN: groupCommitMaxRequests, maxBytes: journalGroupCommitMaxBytes, size: leaseReqSize},
	}
	j.idle.L = &j.st
	j.sealLease = j.savePosition
	return j
}

// lease takes (or re-takes) the durable assignment for delivery. Its contract is unchanged: it
// blocks until its answer is final, and it returns a lease only once the line recording it is
// synced and sealed. It is enqueue-and-wait on leaseQ: a caller that finds no batch in flight
// commits one (commitLeases) inline on its own goroutine, and every caller that arrives meanwhile
// waits for the batch that answers it. So an isolated lease pays exactly the one Write, one Sync and
// one seal it always did, and leases that arrive together share them.
func (j *deliveryJournal) lease(ctx context.Context, delivery string, session core.SessionID, request core.Hash) (deliveryLease, error) {
	if j == nil || j.owner == nil {
		return deliveryLease{}, deliveryJournalError()
	}
	r := &leaseReq{ctx: ctx, delivery: delivery, session: session, request: request, err: deliveryJournalError()}
	j.leaseQ.run(r, j.commitLeases)
	return r.lease, r.err
}

// leaseReq is one lease call's request in leaseQ (design §2.6).
type leaseReq struct {
	ctx      context.Context
	delivery string
	session  core.SessionID
	request  core.Hash
	// lease and err are the call's answer. err starts as deliveryJournalError() and lease as the
	// zero lease, and only commitLeases' last phase replaces them (J-A2): a batch that returns early
	// or panics leaves every member it did not answer failed, never holding an unsealed lease.
	lease deliveryLease
	err   error
	// pend is the first phase's decision for a validated request, answered in the last phase.
	pend leasePending
}

// The estimate leaseQ batches by: an upper bound on the canonical line one request can append.
// json.Marshal writes at most six bytes (a \u00XX escape) per byte of the session, and every other
// field of a lease line has a fixed maximum width, 305 bytes in all with a 20-digit arrival.
const (
	leaseLineEscapeBound = 6
	leaseLineFixedBound  = 400
)

func leaseReqSize(r *leaseReq) int { return leaseLineEscapeBound*len(r.session) + leaseLineFixedBound }

// leasePending is the first phase's decision for one validated request.
type leasePending struct {
	// err is a refusal already decided (a budget, contract or context error) that no append in the
	// batch can change.
	err error
	// lease is what the request is answered with once its binding is checked: a known nonce's sealed
	// lease, or the lease this batch mints, for this request or for the earlier copy it joins.
	lease deliveryLease
	// minted is true when lease is minted in this batch, so that the answer depends on the append.
	minted bool
}

// answer sets r's final result. failed is the fault the batch's append failed with, or nil.
func (r *leaseReq) answer(failed error) {
	p := r.pend
	switch {
	case p.err != nil:
		r.err = p.err
	case p.minted && failed != nil:
		r.err = failed
	case p.lease.Session != r.session || p.lease.RequestHash != r.request:
		r.err = core.ErrAppendOnly
	default:
		r.lease, r.err = p.lease, nil
	}
}

// leaseBatch is one commitLeases call's running state: the journal as it will stand once the
// batch's mints are appended, sealed and admitted.
type leaseBatch struct {
	size  int64
	count int
	chain core.Hash
	next  map[core.SessionID]uint64 // the last arrival this batch assigned, per session
	fresh map[string]deliveryLease  // the leases this batch minted, by nonce
	order []deliveryLease           // the same leases, in queue order
	buf   []byte                    // their lines, in the same order
}

// decide is the first phase for one validated request: the checks a lease call has always made
// after its checkFile, in the same order, over the journal as this batch's earlier mints leave it.
// It reads j's admitted state without st: only a batch's leader writes that state, and the leader
// is the caller.
func (b *leaseBatch) decide(j *deliveryJournal, r *leaseReq) leasePending {
	if old, ok := j.leases[r.delivery]; ok {
		return leasePending{lease: old}
	}
	if minted, ok := b.fresh[r.delivery]; ok {
		return leasePending{lease: minted, minted: true} // a concurrent copy joins its mint (§2.7)
	}
	prev, ok := b.next[r.session]
	if !ok {
		prev = j.arrivals[r.session]
	}
	if b.count >= deliveryLeaseMaxEntries || prev == math.MaxUint64 {
		return leasePending{err: core.ErrBudget}
	}
	id, err := core.NewObservationID(r.session, prev+1)
	if err != nil {
		return leasePending{err: core.ErrContract}
	}
	lease := deliveryLease{
		Version: core.EvidenceVersion, Delivery: r.delivery, Session: r.session, RequestHash: r.request,
		ArrivalSeq: prev + 1, ObservationID: id,
	}
	line, err := json.Marshal(lease)
	if err != nil {
		return leasePending{err: core.ErrContract}
	}
	line = append(line, '\n')
	if len(line) > deliveryLeaseMaxLine || b.size+int64(len(line)) > deliveryLeaseMaxBytes {
		return leasePending{err: core.ErrBudget}
	}
	if err := r.ctx.Err(); err != nil {
		return leasePending{err: err}
	}
	b.next[r.session] = lease.ArrivalSeq
	b.count++
	b.size += int64(len(line))
	b.chain = deliveryChain(b.chain, line)
	b.fresh[r.delivery] = lease
	b.order = append(b.order, lease)
	b.buf = append(b.buf, line...)
	return leasePending{lease: lease, minted: true}
}

// commitLeases is leaseQ's commit (design §2.6, §2.7). It answers one batch of lease requests, in
// queue order, exactly as that many lease calls made one at a time would have, with one append:
//
//  0. The gate, once for the batch and only after every member has arrived: enter, then
//     Lock.ownedByFile. A lock this journal no longer owns refuses the batch without poisoning the
//     handle, as it always has.
//  1. Evaluation, CPU only, of each member in a lease call's order: ctx, the gate, validation, then
//     decide.
//  2. One checkFile, immediately before the append, so that nothing but the call itself separates
//     its last syscall from the Write (J-B4). It gates every validated member, known nonces
//     included, as it gated every validated call.
//  3. One Write of every minted line, one Sync, one seal (appendLeases).
//  4. Admission, under st, of exactly what the seal covers (appendLeases).
//  5. The answers. Nothing is released before commitLeases returns: the queue wakes the followers
//     only then, and the leader's own lease returns only after that.
//
// A failed Write, Sync or seal poisons the handle and fails every member whose answer depended on
// the append: the mints and the copies that joined them. Known nonces and refusals are answered as
// decided. Their answers never depended on this append, and an order of the calls that puts them
// before the failed append is a valid one.
func (j *deliveryJournal) commitLeases(batch []*leaseReq) {
	gate := j.enter()
	if gate == nil {
		defer j.leave()
		if !j.owner.ownedByFile() {
			gate = deliveryJournalError()
		}
	}
	defer j.poisonOnPanic()

	b := &leaseBatch{
		size: j.bytes, count: len(j.leases), chain: j.chain,
		next: map[core.SessionID]uint64{}, fresh: map[string]deliveryLease{},
	}
	var checked []*leaseReq
	for _, r := range batch {
		if err := r.ctx.Err(); err != nil {
			r.err = err
			continue
		}
		if gate != nil {
			r.err = gate
			continue
		}
		if !validDeliveryToken(r.delivery) || r.request.IsZero() || !utf8.ValidString(string(r.session)) {
			r.err = core.ErrContract
			continue
		}
		r.pend = b.decide(j, r)
		checked = append(checked, r)
	}
	if len(checked) == 0 {
		return
	}
	if err := j.checkFile(); err != nil {
		fault := j.poison(err)
		for _, r := range checked {
			r.err = fault
		}
		return
	}
	var failed error
	if len(b.order) > 0 {
		failed = j.appendLeases(b)
	}
	for _, r := range checked {
		r.answer(failed)
	}
}

// appendLeases is phases 3 and 4 for a batch that minted: one Write, one Sync and one seal, and
// then the admission of exactly what they made durable. It returns the fault a failure poisoned the
// journal with, or nil once the batch's leases are admitted.
func (j *deliveryJournal) appendLeases(b *leaseBatch) error {
	n, err := j.writer.Write(b.buf)
	if err != nil || n != len(b.buf) {
		return j.poison(deliveryJournalError())
	}
	if err := j.writer.Sync(); err != nil {
		return j.poison(deliveryJournalError())
	}
	if err := j.sealLease(b.size, b.count, b.chain); err != nil {
		return j.poison(deliveryJournalError())
	}
	// Only synced and sealed bytes enter the identity maps. An uncertain write poisons this handle
	// and requires a reload, and a complete surviving row then keeps its identity on retry.
	j.st.Lock()
	defer j.st.Unlock()
	j.bytes, j.chain = b.size, b.chain
	for _, l := range b.order {
		j.leases[l.Delivery], j.arrivals[l.Session] = l, l.ArrivalSeq
	}
	return nil
}

// poisonOnPanic is deferred by every lease batch. A batch that panics has left the journal's disk
// state uncertain (its Write may have landed without its seal), so it poisons the handle exactly as
// a failed Write does, and every member it had not answered keeps the failure it started with. The
// panic then continues on the leader's goroutine, as a WAL batch's does and as a panic inside lease
// always has: an Accept's is contained by the IPC server's dispatch, which NAKs, and a drain's by
// whatever ran that drain.
func (j *deliveryJournal) poisonOnPanic() {
	if p := recover(); p != nil {
		_ = j.poison(deliveryJournalError())
		panic(p)
	}
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

// enter admits one operation that may write, sync or seal (a lease, or an acknowledgement) into
// the section closeLocked waits for. It refuses a journal that is closing, closed or faulted: the
// per-call gate lease and acknowledge have always had, minus the ownership read, which each caller
// makes itself once it is inside. Every successful enter is paired with exactly one leave.
func (j *deliveryJournal) enter() error {
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.fault != nil {
		return deliveryJournalError()
	}
	j.inflight++
	return nil
}

// leave ends an operation enter admitted, waking closeLocked when it was the last one in flight.
func (j *deliveryJournal) leave() {
	j.st.Lock()
	defer j.st.Unlock()
	j.inflight--
	if j.inflight == 0 {
		j.idle.Broadcast()
	}
}

// poison records err as the journal's fault unless it already has one, and returns the fault it
// holds. There is one fault for the whole handle, lease and acknowledgement sides alike, so an
// uncertain write on either side refuses every later operation on both until the lock is released
// and the journal reopened.
func (j *deliveryJournal) poison(err error) error {
	j.st.Lock()
	defer j.st.Unlock()
	if j.fault == nil {
		j.fault = err
	}
	return j.fault
}

// usable reports whether the journal is open, not closing and not faulted.
func (j *deliveryJournal) usable() bool {
	j.st.Lock()
	defer j.st.Unlock()
	return !j.closing && !j.closed && j.fault == nil
}

// closeLocked requires the owner mutex: Release and the failed-open paths call it. It sets closing
// before anything else, so no operation passes enter from then on and every request still queued
// fails there; then it waits until nothing admitted is in flight, so Release never overtakes a
// write, a sync or a seal. Only then are the handles closed, exactly as they always were.
func (j *deliveryJournal) closeLocked() error {
	j.st.Lock()
	if j.closed {
		j.st.Unlock()
		return nil
	}
	j.closing = true
	for j.inflight > 0 {
		j.idle.Wait()
	}
	j.st.Unlock()
	if err := j.writer.Close(); err != nil {
		return j.poison(deliveryJournalError())
	}
	// The lease handle is closed first and the ack handle is dropped once closed, so a retry after
	// a failed close never closes the same descriptor twice.
	if w := j.ackWriter; w != nil {
		j.ackWriter = nil
		if err := w.Close(); err != nil {
			return j.poison(deliveryJournalError())
		}
	}
	j.st.Lock()
	j.closed = true
	j.st.Unlock()
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
	if err := j.enter(); err != nil {
		return err
	}
	defer j.leave()
	if !j.owner.owned() {
		return deliveryJournalError()
	}
	if !validDeliveryToken(delivery) || id == "" {
		return core.ErrContract
	}
	j.st.Lock() // a lease operation may be admitting a lease at this moment
	lease, ok := j.leases[delivery]
	j.st.Unlock()
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
		return j.poison(err)
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
		return j.poison(deliveryJournalError())
	}
	if err := j.ackWriter.Sync(); err != nil {
		return j.poison(deliveryJournalError())
	}
	chain := deliveryChain(j.ackChain, line)
	if err := j.saveAckPosition(j.ackBytes+int64(len(line)), len(j.acks)+1, chain); err != nil {
		return j.poison(deliveryJournalError())
	}
	j.st.Lock()
	j.ackBytes += int64(len(line))
	j.ackChain = chain
	j.acks[delivery] = ack
	j.st.Unlock()
	return nil
}

// acknowledged reports whether delivery has a committed frontier record. A closed, faulted or
// unowned journal answers false: "cannot currently tell" and "not published" both mean the caller
// must not release the record that would let it retry. It keeps owned() under Lock.mu, where
// owned's read of Lock.released is ordered against Release.
func (j *deliveryJournal) acknowledged(delivery string) bool {
	if j == nil || j.owner == nil {
		return false
	}
	j.owner.mu.Lock()
	defer j.owner.mu.Unlock()
	if !j.owner.owned() {
		return false
	}
	j.st.Lock()
	defer j.st.Unlock()
	if j.closing || j.closed || j.fault != nil {
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
