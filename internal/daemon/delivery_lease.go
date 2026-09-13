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

	// ackQ group-commits acknowledge (design §2.8) the way leaseQ does lease, as a pipeline of its
	// own: a lease batch never waits for an acknowledgement batch, nor the other way round.
	ackQ groupQueue[*ackReq]
	// sealAck seals one acknowledgement batch's position: saveAckPosition, the paths.WriteAtomic of
	// the v1 sidecar, in production. Like sealLease, it is a field only so that tests can observe,
	// hold and fail one batch's seal; nothing else sets it.
	sealAck func(size int64, count int, chain core.Hash) error

	// seal and ackSeal are the held v2 handles on the two position files, open for the journal's
	// life while sealFormat is 2 and nil while it is 1 (design 2.9). Each is used only by the
	// pipeline that commits its own journal, one batch at a time, which is what lets the seal itself
	// take no lock.
	seal, ackSeal *deliverySeal
	// sealFormat is the format this journal WRITES. Both formats are READ whatever it says, because
	// the reader decides from the file's own layout (loadDeliverySeal).
	sealFormat int
	// sealsClosed would make closeSeals, and the downgrade it may take, happen once. It is
	// UNREACHABLE by construction, and kept as a defensive guard on a helper whose downgrade must
	// never run twice — the same treatment the two acknowledgement checks in decide are given.
	//
	// What makes it unreachable: closeLocked runs under Lock.mu, so its calls are serialised, and it
	// sets j.closed immediately after closeSeals() with nothing between them that can fail, so every
	// later call returns at its `if j.closed` guard. A close that FAILS leaves closeLocked to be
	// called again, and there are two shapes of that, neither of which is a second closeSeals():
	//
	//   - The failing call returned at its poison, before closeSeals() — the first Release in
	//     TestDeliveryJournal_CloseFailureRetainsOwnership, which fails at j.writer.Close().
	//   - The retry then reaches closeSeals() for the FIRST time, which is what dropping each handle
	//     before its own Close is for: in
	//     TestDeliveryJournal_CloseRetryAfterAFailedLeaseCloseReleasesOwnership the second Release
	//     finds j.writer already nil, closes the acknowledgement writer, closes both held seals and
	//     releases ownership — and that same call sets j.closed, so nothing after it gets here.
	sealsClosed bool
	// downgradeResidual is what closeSeals could not finish: the first held seal whose downgrade to
	// v1 failed on an otherwise clean release. It is a RECORD, never a refusal — the release
	// completes, ownership is given up, and the v2 file that stayed behind is readable by every
	// build carrying this reader. It exists because the alternative was silence: the journal has no
	// logger, closeSeals returned nothing, and Release reported success, so §4.4's "a clean stop
	// leaves v1 on disk" could fail with nothing anywhere saying so. Guarded by st, like every other
	// field a release and a reader can touch.
	downgradeResidual error
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
		// the file for every operation on the journal, only there: a lease batch and an
		// acknowledgement batch each re-read it (Lock.ownedByFile) once every member has arrived and
		// before anything is appended or answered, and acknowledged reads it through owned under
		// this mutex.
		// One visible difference follows, and Q6 countersigns it too: a lock this process lost is
		// no longer refused here, silently, but by each lease, whose callers (ingest's and the
		// drainer's leaseDelivery) count the same unleased gap and also log the refusal at Warn,
		// once per delivery, for as long as the daemon runs without its lock.
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
		// The empty position, through the ONE v1 encoder (design §5). It used to be marshalled
		// inline here with its error discarded, which put a second producer of v1 bytes beside
		// writeDeliveryPositionV1; the bytes were the same, and the point of the shared call is that
		// they cannot drift apart later.
		if err := createEmptyDeliveryPositionV1(positionPath, deliveryChainSeed); err != nil {
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
	if err := j.openSeal(); err != nil {
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
		// Both journals start from their own seed here, not only the lease journal. openAckLocked
		// sets the acknowledgement pair again at the top of its own open, which is where it belongs
		// for a reopen; seeding it HERE is what makes "a fresh journal object starts at both seeds"
		// true of the object rather than of one caller's sequence. The offline tool (design §4.5)
		// scans with loadAcksFrom without going through openAckLocked, and an ack chain left at the
		// zero hash makes every acknowledged journal fail its chain check and every empty one seal a
		// zero chain that the position reader then refuses.
		ackChain: deliveryAckChainSeed, acks: map[string]deliveryAck{},
		leaseQ: groupQueue[*leaseReq]{maxN: groupCommitMaxRequests, maxBytes: journalGroupCommitMaxBytes, size: leaseReqSize},
		ackQ:   groupQueue[*ackReq]{maxN: groupCommitMaxRequests, maxBytes: journalGroupCommitMaxBytes, size: ackReqSize},
	}
	j.idle.L = &j.st
	j.sealFormat = deliverySealWriteFormat
	if l != nil {
		j.sealFormat = l.deliverySealFormat()
	}
	j.sealLease = j.savePosition
	j.sealAck = j.saveAckPosition
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
// decided, because the append's outcome cannot change them. Together these answers are the ones
// lease calls made one at a time would give in one order: the known nonces and the refusals first,
// then the mints and their copies, the first of which fails. One refusal is the exception: a budget
// refusal that an earlier mint in this batch caused, through the entry, bytes or arrival it took.
// It keeps ErrBudget, although a call made alone answers ErrDegraded in every order: ahead of every
// mint it is the first mint, whose append fails, and behind one it meets the fault. Design §2.6 has
// budget refusals resolve normally; only the error class differs, and no caller tells the two
// apart, since both count an unleased gap.
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

// poisonOnPanic is deferred by every lease batch and every acknowledgement batch. A batch that
// panics has left the journal's disk state uncertain (its Write may have landed without its seal),
// so it poisons the handle exactly as a failed Write does, and every member it had not answered
// keeps the failure it started with. The panic then continues on the leader's goroutine, as a WAL
// batch's does and as a panic inside lease or acknowledge always has: an Accept's is contained by
// the IPC server's dispatch, which NAKs, and an ingest worker's or a drain's by whatever ran it.
func (j *deliveryJournal) poisonOnPanic() {
	if p := recover(); p != nil {
		_ = j.poison(deliveryJournalError())
		panic(p)
	}
}

func (j *deliveryJournal) load() (os.FileInfo, error) {
	position, older, _, err := loadDeliverySeal(j.positionPath(), deliveryChainSeed, deliveryChainDomain)
	if err != nil {
		return nil, err
	}
	return j.loadFrom(position, older)
}

// loadFrom is load's scan, against a position and an older record the caller has already read. It
// is split out for the offline repair tool (design §4.5), which is the one caller that arrives with
// a position the dual reader did not hand it: Rule R's valid-plus-invalid image, which the strict
// reader refuses and an operator may accept with consent. The tool scans through THIS function
// rather than through a second copy of it, so a position an operator accepted is still admitted only
// by the checks an open makes — the sealed prefix, the canonical lines, the dense arrivals and the
// size — and Rule R widens exactly one thing, which record the scan starts from.
func (j *deliveryJournal) loadFrom(position deliveryPosition, older *sealRecord) (os.FileInfo, error) {
	// The older-seal checkpoint (design §2.9). A v2 image carries the seal one batch behind the
	// effective one, and the strict reader has already checked that it seals strictly less. What it
	// cannot check is that those bytes are a real prefix of THIS journal, so the same scan does it:
	// at older.Bytes the count and the chain must be the older record's. An older record sealing the
	// empty journal needs nothing checked — the reader admits it only with count 0 and the seed
	// chain, which is where every scan starts.
	olderSealed := older == nil || older.Bytes == 0
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
		if older != nil && j.bytes == older.Bytes {
			if len(j.leases) != older.Count || j.chain != older.Chain {
				return nil, deliveryJournalError()
			}
			olderSealed = true
		}
		if j.bytes == position.Bytes {
			if len(j.leases) != position.Count || j.chain != position.Chain {
				return nil, deliveryJournalError()
			}
			sealed = true
		}
	}
	// An older record whose bytes fall inside no line is not a prefix of this journal either, so
	// olderSealed stays false and the open refuses.
	if j.bytes != info.Size() || !sealed || !olderSealed {
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

// savePosition writes the v1 position sidecar for the lease journal: the seal a build whose write
// format is 1 makes, and the one a clean Release leaves behind whatever format it wrote. The v1
// bytes have one encoder, writeDeliveryPositionV1, which the downgrade uses as well, so the two can
// never drift (TestDeliverySeal_DowngradeWritesTodaysV1Bytes compares them).
func (j *deliveryJournal) savePosition(size int64, count int, chain core.Hash) error {
	return writeDeliveryPositionV1(j.positionPath(), size, count, chain)
}

// openSeal is the lease journal's O4 (design 2.9): it seals the position load recovered, in this
// build's write format, and in format 2 leaves the held handle open for the journal's life. It runs
// once per open, after the writer's identity check and its Sync, so a complete tail that survived an
// uncertain append is durable and sealed before any caller can reuse its assignments.
func (j *deliveryJournal) openSeal() error {
	if j.sealFormat != 2 {
		// Format 1 rewrites the v1 sidecar unconditionally, as this open always has. That is also
		// how a v2 file left by a step-2 build is converted back: the WriteAtomic replaces its
		// 32 KiB with v1 bytes, which is the rollback design 4.4 promises.
		return j.savePosition(j.bytes, len(j.leases), j.chain)
	}
	s, err := j.openSealHandle(j.positionPath(), deliveryChainDomain, deliveryChainSeed,
		j.bytes, len(j.leases), j.chain)
	if err != nil {
		return err
	}
	// Phase 3's seal becomes the slot write, which does its own WriteAt, SyncData and post-seal
	// identity check. It stays behind the same field, so the group-commit tests' seam goes on
	// numbering, holding and failing one batch's seal in either format.
	j.seal, j.sealLease = s, j.writeLeaseSeal
	return nil
}

// openSealHandle is O4a to O4c for one journal's seal file.
//
//   - O4a. A v1 file is CONVERTED, and only here, after a load that has already succeeded: a failed
//     load returns long before this and writes nothing at all. The image it converts to seals the
//     position the load recovered, so a complete tail recovered past the old v1 seal is sealed by
//     the conversion itself.
//   - O4b. The handle is opened over the image on disk (openDeliverySeal), which verifies that the
//     path still names the file it opened and that the file reads back as exactly that image.
//   - O4c. A v2 file whose effective record is behind the recovered position is re-sealed through
//     the handle, which writes seq+1 into the slot holding seq-1. O4a's conversion has already done
//     this for a v1 file, so only an untouched v2 file reaches it.
//
// Every failure returns deliveryJournalError() and leaves no handle open; the caller poisons the
// journal and closes it, which is what makes the open fault require a Release before a retry.
//
// O4a's write is the one residual this leaves, and it is inherent rather than an oversight: the
// handle is opened OVER the file, so the converted image must be on disk before openDeliverySeal can
// be asked whether it opens. An open that converted and then failed therefore leaves a v2 file
// behind although that daemon never sealed a batch, and the close that follows runs with
// clean == false, so nothing downgrades it back. Nothing is corrupted — the image seals the position
// the load recovered, and every build with the dual reader opens it — but §4.4's "a clean stop
// leaves v1" does not cover it, because the stop was not clean. The repair is §4.4's own:
// `qompack admin delivery-seal --to v1`, or one successful open and clean Release by a build that
// carries this reader. Before the flip this could not happen at all: openSeal returned at
// j.sealFormat != 2 and never converted.
func (j *deliveryJournal) openSealHandle(
	path, domain string, seed core.Hash, size int64, count int, chain core.Hash,
) (*deliverySeal, error) {
	image := readDeliverySealImage(path)
	if image == nil {
		converted, err := newSealImage(size, count, chain, domain, seed)
		if err != nil {
			return nil, deliveryJournalError()
		}
		if err := paths.WriteAtomic(path, converted, 0o600); err != nil {
			return nil, deliveryJournalError()
		}
		image = converted
	}
	s, err := openDeliverySeal(path, image, domain, seed)
	if err != nil {
		return nil, err
	}
	if s.cur.Bytes != size || s.cur.Count != count || s.cur.Chain != chain {
		if err := s.write(size, count, chain); err != nil {
			_ = s.close()
			return nil, err
		}
	}
	return s, nil
}

// writeLeaseSeal and writeAckSeal seal one batch's position through the held v2 handle: the whole
// slot region in one WriteAt, SyncData, and the post-seal identity check, all inside deliverySeal.
func (j *deliveryJournal) writeLeaseSeal(size int64, count int, chain core.Hash) error {
	return j.seal.write(size, count, chain)
}

func (j *deliveryJournal) writeAckSeal(size int64, count int, chain core.Hash) error {
	return j.ackSeal.write(size, count, chain)
}

// checkSeal is the seal half of a batch's per-batch check, in this journal's write format.
//
// Format 1 re-reads the sidecar BY PATH, as it always has: a file replaced by a rename is a
// different file, and only reopening the path can see it (design X4). Format 2 asks the held seal,
// which is strictly stronger — deliverySeal.check compares every byte of the file, read through its
// own handle, with the image it cached, padding and static bytes included, and proves that the path
// still names that file. Either way the journal's own three checks follow, unchanged.
func (j *deliveryJournal) checkSeal(
	seal *deliverySeal, load func() (deliveryPosition, error), size int64, count int, chain core.Hash,
) error {
	if seal != nil {
		if err := seal.check(size, count, chain); err != nil {
			return deliveryJournalError()
		}
		return nil
	}
	position, err := load()
	if err != nil || position.Bytes != size || position.Count != count || position.Chain != chain {
		return deliveryJournalError()
	}
	return nil
}

// closeSeals closes the held v2 handles and, when the close is CLEAN, leaves a v1 sidecar behind so
// that a build without this reader still opens the project (design 2.9, Release step 3).
//
// The downgrade needs both of its conditions. fault == nil, because a journal whose write was
// uncertain must not have its position rewritten from a cur that may not be what the file holds —
// the seal enforces its own half of that, since downgradeToV1 refuses a seal whose write latched a
// fault. And ownedByFile, because a lock this process has LOST must not write over the position
// file of the owner that holds it now: in
// TestDeliveryJournal_ReplacedLockCannotReleaseOrLeaseForNewOwner a replaced owner releases while
// the new owner holds its seal open, and a WriteAtomic there would land a v1 file over the new
// owner's held file and fault its very next check.
//
// It is best effort, and it reports. A failed downgrade leaves a valid v2 file, which any build
// carrying this reader opens, and it never blocks the release of ownership — but it does mean §4.4's
// "after a clean stop, v1 is on disk" was not kept for that file, and an operator planning a
// downgrade past step 1 had no way to learn it. So the FIRST failure is returned rather than
// discarded, the second seal is downgraded regardless, and the release goes on: the error becomes a
// residual the caller of Release logs once (Lock.SealDowngradeResidual), never a refusal.
func (j *deliveryJournal) closeSeals() error {
	if j.sealsClosed || (j.seal == nil && j.ackSeal == nil) {
		return nil
	}
	j.sealsClosed = true
	j.st.Lock()
	clean := j.fault == nil
	j.st.Unlock()
	clean = clean && j.owner != nil && j.owner.ownedByFile()
	var residual error
	for _, s := range []*deliverySeal{j.seal, j.ackSeal} {
		if s == nil {
			continue
		}
		_ = s.close()
		if clean {
			if err := s.downgradeToV1(); err != nil && residual == nil {
				residual = err
			}
		}
	}
	return residual
}

// downgradeResidualErr is what closeSeals could not finish, once a release has run: nil when every
// held seal was downgraded, or was not eligible to be.
func (j *deliveryJournal) downgradeResidualErr() error {
	j.st.Lock()
	defer j.st.Unlock()
	return j.downgradeResidual
}

// positionPath and ackSealPath are the two position sidecars, beside the journals they seal.
func (j *deliveryJournal) positionPath() string {
	return filepath.Join(filepath.Dir(j.path), deliveryPositionFile)
}

func (j *deliveryJournal) ackSealPath() string {
	return filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile)
}

// loadPosition is the position sealed for the lease journal, in whichever format the sidecar holds.
// It is the reader the TESTS call to ask a journal what it sealed, whatever format it writes (design
// §5), and they are its only callers. The per-batch check does NOT use it: checkFile reads the
// sidecar in the format this build WRITES — the held seal in format 2, the strict v1 reader in
// format 1 — so reading both formats here can never widen what a batch accepts.
//
// Its signature is unchanged, and so is its answer for the v1 file every build before this one wrote.
func (j *deliveryJournal) loadPosition() (deliveryPosition, error) {
	position, _, _, err := loadDeliverySeal(j.positionPath(), deliveryChainSeed, deliveryChainDomain)
	return position, err
}

// loadDeliverySeal is the dual reader (design §2.9, §4.2). It reads the sealed position at p in
// whichever format the file holds: the v2 A/B seal when the file is a v2 image, and today's v1
// sidecar otherwise. It returns the effective position, the older record beside it when the image
// carries one, and the image itself, which openDeliverySeal needs as the bytes its caller read.
//
// The format is decided by the file's own layout (isDeliverySealImage), never by a flag, so the two
// readers cannot disagree about which one owns a file. A v2 image whose slots do not select is
// REFUSED rather than handed to the v1 reader: it is this journal's seal, damaged, and the strict
// reader's refusal is what preserves the evidence. Only a file that is not a v2 image at all reaches
// the v1 reader, which refuses a v2 document on its version in any case.
//
// The older record is returned because selectSeal cannot see the journal: whether it seals a real
// journal prefix is load's question, checked in the same scan.
func loadDeliverySeal(p string, seed core.Hash, domain string) (deliveryPosition, *sealRecord, []byte, error) {
	image := readDeliverySealImage(p)
	if image == nil {
		position, err := loadDeliveryPosition(p, seed)
		return position, nil, nil, err
	}
	effective, older, err := selectSeal(image, domain, seed)
	if err != nil {
		return deliveryPosition{}, nil, nil, err
	}
	return sealedPosition(effective), older, image, nil
}

// sealedPosition is the journal position a seal record seals.
//
// The Version is the journal position record's, which is what every caller compares against and
// what a downgrade of this record writes; the v2 document's own "v" is deliverySealVersion.
func sealedPosition(rec sealRecord) deliveryPosition {
	return deliveryPosition{Version: core.EvidenceVersion, Bytes: rec.Bytes, Count: rec.Count, Chain: rec.Chain}
}

// readDeliverySealImage returns p's bytes when p is a v2 seal image, and nil when it is anything
// else — including missing, a directory, the wrong size, or a file whose static bytes are not a v2
// document's. Every one of those is handed to the v1 reader, which produces the refusal, so a
// failure has one error path and not two.
func readDeliverySealImage(p string) []byte {
	info, err := os.Lstat(paths.Long(p))
	if err != nil || !info.Mode().IsRegular() || info.Size() != deliverySealFileSize {
		return nil
	}
	f, err := os.Open(paths.Long(p))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil
	}
	image, err := io.ReadAll(io.LimitReader(f, deliverySealFileSize+1))
	if err != nil || !isDeliverySealImage(image) {
		return nil
	}
	return image
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

// checkFile is the lease journal's per-batch check. Its v1 body is today's, verbatim (design §5):
// the sidecar is re-read BY PATH with the STRICT v1 reader, never the dual one. A build that writes
// v1 must refuse a v2 image at its position path — that image is a foreign write for THAT build (a
// half-rolled-back step-2 binary, a restore, an operator), and accepting it would let the format
// seam widen what such a build admits. checkAckFile reads its own sidecar exactly the same way.
func (j *deliveryJournal) checkFile() error {
	loadV1 := func() (deliveryPosition, error) {
		return loadDeliveryPosition(j.positionPath(), deliveryChainSeed)
	}
	if err := j.checkSeal(j.seal, loadV1, j.bytes, len(j.leases), j.chain); err != nil {
		return err
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
// per-call gate lease and acknowledge have always had, minus the ownership read, which each batch
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
	// Each handle is dropped BEFORE its own Close is attempted — the lease handle exactly as the
	// acknowledgement handle already was — so no descriptor is ever closed twice and a retry always
	// makes progress past the one that failed.
	//
	// Retrying a failed Close cannot repair it. Go marks an *os.File closed on its FIRST Close
	// whatever the syscall returned, so a second Close of the same handle returns os.ErrClosed and
	// nothing else: a lease handle left in place would poison every later Release at this line and
	// return before closeSeals() below, for the life of the process. In format 2 that strands the two
	// held deliverySeal handles open, and Lock.Release goes on refusing to give up ownership ("do not
	// release singleton ownership with an uncertain writer handle"), so the lock file and its
	// heartbeat are never removed. Dropping first is what lets the NEXT Release close the
	// acknowledgement writer and reach the seals
	// (TestDeliveryJournal_CloseRetryAfterAFailedLeaseCloseReleasesOwnership).
	//
	// A failed close still poisons and still RETURNS here, rather than closing the seals in a defer:
	// design §2.9's Release order is writers, then seals, then the downgrade, and a defer would run
	// the seals' half before this function's own caller sees the failure. The retry keeps the order.
	if w := j.writer; w != nil {
		j.writer = nil
		if err := w.Close(); err != nil {
			return j.poison(deliveryJournalError())
		}
	}
	if w := j.ackWriter; w != nil {
		j.ackWriter = nil
		if err := w.Close(); err != nil {
			return j.poison(deliveryJournalError())
		}
	}
	// The held seals close after the writers and before the journal is marked closed, and a clean
	// close leaves v1 behind for a build without this reader. A downgrade that failed is latched as
	// a residual and NOT returned: this function's error means "an uncertain writer handle", which
	// Lock.Release treats as a reason not to give up ownership, and a v2 file left on disk is no
	// such reason. Lock.SealDowngradeResidual is where it surfaces.
	residual := j.closeSeals()
	j.st.Lock()
	if residual != nil {
		j.downgradeResidual = residual
	}
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
// The append-then-sync-then-position pattern is the lease journal's own, reused verbatim and group
// committed the same way (commitAcks): a batch's bytes reach the file and are synced, the position
// sidecar is sealed once for the batch, and only then does the in-memory set admit its records. An
// uncertain write poisons this handle and requires a reload, at which point a complete surviving
// row is recovered and an incomplete one is refused.
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
		// The acknowledgement journal's own empty position, through the same one call as the lease
		// journal's above, with its own seed.
		if err := createEmptyDeliveryPositionV1(positionPath, deliveryAckChainSeed); err != nil {
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
	return j.openAckSeal()
}

// openAckSeal is O5: openSeal for the acknowledgement journal, with its own chain domain and seed,
// so the two seals can never be recovered against each other.
func (j *deliveryJournal) openAckSeal() error {
	if j.sealFormat != 2 {
		return j.saveAckPosition(j.ackBytes, len(j.acks), j.ackChain)
	}
	s, err := j.openSealHandle(j.ackSealPath(), deliveryAckChainDomain, deliveryAckChainSeed,
		j.ackBytes, len(j.acks), j.ackChain)
	if err != nil {
		return err
	}
	j.ackSeal, j.sealAck = s, j.writeAckSeal
	return nil
}

// acknowledge commits one leased delivery's publication. It is idempotent: a redelivery already
// acknowledged returns without appending, which is what lets a drained line advance a spool offset
// without republishing anything. It refuses to acknowledge a delivery this journal never leased, or
// one whose identity disagrees with the lease — an acknowledgement that does not name a real
// assignment is not a frontier, it is a guess.
//
// Its contract is unchanged: it blocks until its answer is final, and it returns nil for a new
// acknowledgement only once the line recording it is synced and sealed. It is enqueue-and-wait on
// ackQ, exactly as lease is on leaseQ: a caller that finds no batch in flight commits one
// (commitAcks) inline on its own goroutine, and every caller that arrives meanwhile waits for the
// batch that answers it. It no longer holds Lock.mu, which every Accept's journal accessor takes,
// so no lease waits behind an acknowledgement's Write, Sync and seal (design §2.8, J-A3).
func (j *deliveryJournal) acknowledge(ctx context.Context, delivery string, id core.ObservationID, root core.Hash) error {
	if j == nil || j.owner == nil {
		return deliveryJournalError()
	}
	r := &ackReq{ctx: ctx, delivery: delivery, id: id, root: root, err: deliveryJournalError()}
	j.ackQ.run(r, j.commitAcks)
	return r.err
}

// ackReq is one acknowledge call's request in ackQ (design §2.8).
type ackReq struct {
	ctx      context.Context
	delivery string
	id       core.ObservationID
	root     core.Hash
	// err is the call's answer. It starts as deliveryJournalError(), and only commitAcks replaces it
	// (J-A2): a batch that returns early or panics leaves every member it did not answer failed,
	// never holding an acknowledgement that is not sealed.
	err error
	// pend is the first phase's decision for a new acknowledgement, answered in the last phase.
	pend ackPending
}

// ackReqSize is the estimate ackQ batches by: an upper bound on the canonical line one request can
// append. It is a lease line's bound, which covers an acknowledgement line: the fixed fields of one
// (a 64-character nonce and a hash) are narrower than a lease line's, and its one field of caller
// text, the observation identity, escapes to at most as many bytes per byte as a lease's session.
func ackReqSize(r *ackReq) int { return leaseLineEscapeBound*len(r.id) + leaseLineFixedBound }

// ackPending is the first phase's decision for a new acknowledgement: a request that names a leased
// delivery with no acknowledgement committed, whose answer the batch's checkAckFile gates.
type ackPending struct {
	// err is a refusal already decided (the entries, line or bytes bound) that no append in the
	// batch can change.
	err error
	// ack, when err is nil, is the acknowledgement this batch appends for the request's delivery:
	// the request's own, or the one an earlier copy of the delivery in the batch minted, which the
	// request joins. Either way the answer depends on the append.
	ack deliveryAck
}

// answer sets r's final result. failed is the fault the batch's append failed with, or nil.
func (r *ackReq) answer(failed error) {
	p := r.pend
	switch {
	case p.err != nil:
		r.err = p.err
	case failed != nil:
		r.err = failed
	// Deliberately defensive, and unreachable as the code stands. So is the acknowledgement side's
	// other binding check, the ErrAppendOnly in commitAcks' first phase: BOTH are defensive mirrors
	// of leaseReq.answer's binding check, which is the live one — there a copy really can join a
	// mint made for another session or request, and T11 answers it with ErrAppendOnly.
	//
	// One invariant makes each of the two unreachable: a delivery's lease identity never changes
	// once admitted (the maps are written only by a lease leader, and a known nonce is answered
	// with the admitted lease rather than a new one), and every request that gets this far has
	// already matched its own id against that identity.
	//
	//   - Here: decide hands a request either the acknowledgement built from its own id, or the one
	//     an earlier member of this batch minted for the same delivery. Both members passed the same
	//     j.leases[delivery].ObservationID match, so the two ids are the same id.
	//   - In the first phase: an admitted acknowledgement carries the ObservationID of the lease its
	//     delivery had when it was admitted — appendAcks admits only what decide built behind a
	//     passed lease match, and loadAcks refuses any surviving line whose ObservationID differs
	//     from its lease's — which is the identity this request has just matched too.
	//
	// Neither is deleted: each is one comparison standing between a later change to that invariant
	// and an acknowledgement recorded against an identity nothing checked.
	case p.ack.ObservationID != r.id:
		r.err = core.ErrAppendOnly
	default:
		r.err = nil
	}
}

// ackBatch is one commitAcks call's running state: the acknowledgement journal as it will stand
// once the batch's new acknowledgements are appended, sealed and admitted.
type ackBatch struct {
	size  int64
	count int
	chain core.Hash
	fresh map[string]deliveryAck // the acknowledgements this batch minted, by delivery
	order []deliveryAck          // the same acknowledgements, in queue order
	buf   []byte                 // their lines, in the same order
}

// decide is the first phase for a new acknowledgement: the checks an acknowledge call has always
// made after its checkAckFile, in the same order, over the journal as this batch's earlier
// acknowledgements leave it.
func (b *ackBatch) decide(r *ackReq) ackPending {
	if minted, ok := b.fresh[r.delivery]; ok {
		return ackPending{ack: minted} // a second acknowledgement of one delivery joins the first
	}
	if b.count >= deliveryLeaseMaxEntries {
		return ackPending{err: core.ErrBudget}
	}
	ack := deliveryAck{Version: core.EvidenceVersion, Delivery: r.delivery, ObservationID: r.id, Root: r.root}
	line, err := json.Marshal(ack)
	if err != nil {
		return ackPending{err: core.ErrContract}
	}
	line = append(line, '\n')
	if len(line) > deliveryLeaseMaxLine || b.size+int64(len(line)) > deliveryLeaseMaxBytes {
		return ackPending{err: core.ErrBudget}
	}
	b.count++
	b.size += int64(len(line))
	b.chain = deliveryChain(b.chain, line)
	b.fresh[r.delivery] = ack
	b.order = append(b.order, ack)
	b.buf = append(b.buf, line...)
	return ackPending{ack: ack}
}

// commitAcks is ackQ's commit (design §2.8), in commitLeases' shape. It answers one batch of
// acknowledge requests, in queue order, exactly as that many acknowledge calls made one at a time
// would have, with one append:
//
//  0. The gate, once for the batch and only after every member has arrived: enter, then
//     Lock.ownedByFile. A lock this journal no longer owns refuses the batch without poisoning the
//     handle, as it always has.
//  1. Evaluation, CPU only, of each member in an acknowledge call's order: ctx, the gate,
//     validation, the lease match (the leases read under st, since a lease batch may be admitting
//     beside it), then the answer for a delivery an earlier batch acknowledged, idempotent and, as
//     it always was, given without a checkAckFile. Last comes decide: a second acknowledgement of
//     one delivery joins the first, and the entries, line and bytes bounds run over the batch's own
//     earlier acknowledgements.
//  2. One checkAckFile, if any member reached decide, immediately before the append (J-B4). It
//     gates every such member, as it gated every acknowledge call that got that far.
//  3. One Write of every new line, one Sync, one seal (appendAcks).
//  4. Admission, under st, of exactly what the seal covers (appendAcks).
//  5. The answers. Nothing is released before commitAcks returns: the queue wakes the followers
//     only then, and the leader's own acknowledge returns only after that.
//
// Every failure poisons the one fault the journal's two halves share (J-B2), so it refuses leases
// as well. A failed Write, Sync or seal fails every member whose answer depended on the append: the
// new acknowledgements and the copies that joined them. The others keep the answer the first phase
// gave them, a panic included: an acknowledgement committed by an earlier batch, and the refusals.
// Together these are the answers acknowledge calls made one at a time give in one order, the others
// first, then the new acknowledgements and their copies, the first of which fails, with the one
// exception commitLeases names: a bound that an earlier acknowledgement in this batch made binding
// keeps ErrBudget, where calls made one at a time answer ErrDegraded.
func (j *deliveryJournal) commitAcks(batch []*ackReq) {
	gate := j.enter()
	if gate == nil {
		defer j.leave()
		if !j.owner.ownedByFile() {
			gate = deliveryJournalError()
		}
	}
	defer j.poisonOnPanic()

	b := &ackBatch{size: j.ackBytes, count: len(j.acks), chain: j.ackChain, fresh: map[string]deliveryAck{}}
	var checked []*ackReq
	for _, r := range batch {
		if err := r.ctx.Err(); err != nil {
			r.err = err
			continue
		}
		if gate != nil {
			r.err = gate
			continue
		}
		if !validDeliveryToken(r.delivery) || r.id == "" {
			r.err = core.ErrContract
			continue
		}
		j.st.Lock() // a lease batch may be admitting a lease at this moment
		lease, ok := j.leases[r.delivery]
		j.st.Unlock()
		if !ok || lease.ObservationID != r.id {
			r.err = core.ErrContract
			continue
		}
		// Only acknowledgement batches write j.acks, one at a time, and this is the one running.
		if old, ok := j.acks[r.delivery]; ok {
			r.err = nil
			// Defensive, and unreachable for the invariant ackReq.answer's comment states: an
			// admitted acknowledgement names the identity of the lease its delivery had when it was
			// admitted, which is the identity the lease match above has just accepted.
			if old.ObservationID != r.id {
				r.err = core.ErrAppendOnly
			}
			continue
		}
		r.pend = b.decide(r)
		checked = append(checked, r)
	}
	if len(checked) == 0 {
		return
	}
	if err := j.checkAckFile(); err != nil {
		fault := j.poison(err)
		for _, r := range checked {
			r.err = fault
		}
		return
	}
	var failed error
	if len(b.order) > 0 {
		failed = j.appendAcks(b)
	}
	for _, r := range checked {
		r.answer(failed)
	}
}

// appendAcks is phases 3 and 4 for a batch with new acknowledgements: one Write, one Sync and one
// seal, and then the admission of exactly what they made durable. It returns the fault a failure
// poisoned the journal with, or nil once the batch's acknowledgements are admitted.
func (j *deliveryJournal) appendAcks(b *ackBatch) error {
	n, err := j.ackWriter.Write(b.buf)
	if err != nil || n != len(b.buf) {
		return j.poison(deliveryJournalError())
	}
	if err := j.ackWriter.Sync(); err != nil {
		return j.poison(deliveryJournalError())
	}
	if err := j.sealAck(b.size, b.count, b.chain); err != nil {
		return j.poison(deliveryJournalError())
	}
	// Only synced and sealed bytes enter the frontier, under st, where acknowledged reads it.
	j.st.Lock()
	defer j.st.Unlock()
	j.ackBytes, j.ackChain = b.size, b.chain
	for _, a := range b.order {
		j.acks[a.Delivery] = a
	}
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
	position, older, _, err := loadDeliverySeal(j.ackSealPath(), deliveryAckChainSeed, deliveryAckChainDomain)
	if err != nil {
		return nil, err
	}
	return j.loadAcksFrom(position, older)
}

// loadAcksFrom is loadAcks' scan, over a position the caller has already read: loadFrom's split, for
// the same caller and the same reason. It still checks every acknowledgement against a surviving
// lease, so it runs after the lease scan has filled j.leases, exactly as the open runs it.
func (j *deliveryJournal) loadAcksFrom(position deliveryPosition, older *sealRecord) (os.FileInfo, error) {
	// The older-seal checkpoint, as in load: the record one batch behind the effective one must seal
	// a prefix of this acknowledgement journal.
	olderSealed := older == nil || older.Bytes == 0
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
		if older != nil && j.ackBytes == older.Bytes {
			if len(j.acks) != older.Count || j.ackChain != older.Chain {
				return nil, deliveryJournalError()
			}
			olderSealed = true
		}
		if j.ackBytes == position.Bytes {
			if len(j.acks) != position.Count || j.ackChain != position.Chain {
				return nil, deliveryJournalError()
			}
			sealed = true
		}
	}
	if j.ackBytes != info.Size() || !sealed || !olderSealed {
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

// saveAckPosition is savePosition for the acknowledgement journal, through the same one v1 encoder.
func (j *deliveryJournal) saveAckPosition(size int64, count int, chain core.Hash) error {
	return writeDeliveryPositionV1(filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile), size, count, chain)
}

func (j *deliveryJournal) checkAckFile() error {
	loadV1 := func() (deliveryPosition, error) {
		return loadDeliveryPosition(j.ackSealPath(), deliveryAckChainSeed)
	}
	if err := j.checkSeal(j.ackSeal, loadV1, j.ackBytes, len(j.acks), j.ackChain); err != nil {
		return err
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
