package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// deliveryGenerations is the SP20-D4 capacity mechanism: an immutable, append-only sequence of radix
// generations plus a durable, atomic head that names the last committed root. It gives bounded-memory,
// exact, on-disk membership across generations within the declared manifest and authority byte limits.
// A lookup holds one head record and one radix path, rather than a lifetime map, while never forgetting a
// committed nonce, never reassigning a session arrival, and never renaming away an anchor.
//
// # Why one radix is a complete history
//
// The radix (delivery_radix.go) is path-copy: inserting a nonce into generation N's root yields
// generation N+1's root, which SHARES every page of N and adds only the new path. So the LATEST root
// is a superset index over EVERY nonce ever leased; a point lookup in it is O(log n) on disk. Every
// intermediate root is retained (immutable), so an older generation stays exactly readable. The head
// names the latest root; the append-log records every generation's root as a tamper-evident,
// chained anchor — bounded metadata, one small record per generation.
//
// # Commit authority (exact)
//
// A generation is COMMITTED when its manifest record is durably appended AND fsynced. That fsynced,
// chain-valid record IS the commit — it survives the crash itself. The head is a fast-start
// CHECKPOINT that names the last record the writer had committed; it is written atomically after the
// record and lets recovery start in O(1) instead of re-deriving the chain from seq 0.
//
// The two can only disagree in ONE direction, and recovery resolves it deterministically:
//
//   - A crash after the record's fsync but before the head write leaves the LOG one (chain-valid)
//     record ahead of the head. That record is committed; recovery ADOPTS it and repairs the head
//     forward (idempotent). Group-commit serialises commits under g.mu and every commit writes the
//     head before returning, so the un-headed tail is at most one complete record, plus at most a
//     torn partial from a crash mid-append.
//   - A torn (incomplete) trailing record is NOT a commit. Recovery REFUSES the open (unavailable)
//     and PRESERVES the bytes untouched — never truncates, never invents, never a fresh identity.
//   - A head that names bytes the log does not contain (head ahead of log), or whose {seq,root,chain}
//     do not match the record ending at head.LogBytes, is a CONFLICTING/uncommitted head: refused
//     (unavailable). A synced tail is recovery evidence; a head that contradicts the log is never
//     trusted.
//
// # Bounded STARTUP work (not merely bounded memory)
//
// Recovery does NOT scan the whole manifest on every open. The atomic head is root-anchored: it
// carries {Seq, Root, Chain, LogBytes, LastLen}. Recovery reads the head, verifies in O(1) that the
// record of length LastLen ending at LogBytes decodes to exactly {Seq,Root,Chain} on a record
// boundary (predecessor-chained identity, anchored at the head), trusts everything at or below
// LogBytes, and then validates ONLY the tail (LogBytes, size) as a chain extension of head.Chain.
// A full-lifetime chain audit is an fsck-class integrity check, not a startup cost; per-generation
// page corruption is caught at lookup time anyway (pages are content-addressed).
//
// # Path confinement parity
//
// Head and manifest IO is confined to the store directory by an os.Root, exactly as the radix confines
// its pages: a swapped symlink cannot redirect a head/manifest read or write outside the store.
//
// # This is ADDITIVE and DOES NOT touch the legacy journal
//
// It lives in its OWN directory (`<state>/delivery-generations/`), separate from
// `delivery-leases.jsonl` and its position sidecar, which stay the live anchor untouched. Nothing here
// renames, truncates or rewrites a legacy file. It is a NEW structure the journal consults; the live
// wiring (delivery_lease.go) keeps it behind a seam that is default-OFF, because the capacity-freeing
// rollover it enables needs a cross-package retention change (see delivery-capacity-integration-work.md).
//
// # Keyed spaces (one radix, domain-prefixed so no space can collide with another)
//
//   - nonce   → the canonical lease record bytes (ALL identity fields). Membership + idempotency.
//   - arrival → a session's last arrival seq (8 bytes). Continuity for a dormant session, so its next
//     arrival is dense and never restarts at zero.
//   - order   → (session, arrival) → nonce. Ordered-by-arrival access.
//   - ack     → the canonical acknowledgement bytes. Durable ACK membership: a settled delivery is
//     recorded here only after its lease resolves and its identity matches (an exact join, never a
//     blind marker).
//   - terminal→ a terminal disposition's observation id. Durable terminal membership, same exact join.
//   - frontier→ a session's oldest NOT-known-settled arrival (8 bytes): all arrivals below it are
//     settled. Advanced amortized-O(1) at settle time so a per-session ready check is O(1) and never
//     walks all arrivals.
//
// # Failure vocabulary (never a fresh identity)
//
// A missing/corrupt/torn/unknown-version head or manifest, or a corrupt page, is
// `errGenerationUnavailable` — never a false absence and never a newly minted identity. A resolved
// leaf whose original key collides is `errRadixCollision`. An archived ACK/terminal whose resolved
// lease disagrees on any identity field is `errGenerationConflict` — never an unchecked join.

const (
	genPagesDir  = "pages"
	genLogFile   = "manifest.jsonl"
	genHeadFile  = "manifest-head.json"
	genFormat    = "qompack.delivery.generations.v2"
	genVersion   = 1
	genChainSalt = "qompack.delivery.generations.chain.v1"
	genMaxLine   = 4096 //nomagic:allow generation metadata format bound, independent of config defaults.
	// genMaxLog bounds the manifest size a store will open. Recovery no longer scans it whole, but a
	// file past this bound is treated as damaged rather than opened.
	genMaxLog = 1 << 30
	genTemp   = ".tmp-"
)

var (
	// errGenerationUnavailable marks a head/manifest that cannot be read as itself, or a resolvable
	// record that is absent where the caller required one. Never a fresh identity, never a false absence.
	errGenerationUnavailable = errors.New("delivery generations: unavailable")
	// errGenerationConflict marks an archived record whose resolved lease disagrees on an identity
	// field — the "compare all identity fields" refusal that replaces an unchecked join.
	errGenerationConflict = errors.New("delivery generations: identity conflict")
)

var genChainSeed = radixDigest(genChainSalt, nil)

// deliveryGenerationsDirName is the generation store's directory under <state>, the same name main's
// refuseDeliveryRecreation reserves as migration evidence (delivery_migration_guard.go).
const deliveryGenerationsDirName = "delivery-generations"

// enableDeliveryGenerations gates segmented rollover of the delivery journals: the segment authority,
// this generation store, and the rotation that replaces the 65,536-entry / 64 MiB refusal. It is ON by
// default since the V6 close-out (C1.10, owner decision D2, after the gates in
// plans/sdd/V6-closeout/rollover/report.md): a journal at its cap rotates to a fresh segment and keeps
// assigning identities, an archived nonce's redelivery resolves its ORIGINAL lease from this store, and
// store GC harvests every segment. With it off a current build refuses any store that has migration
// evidence (existingDeliveryMigration), because disabling a mechanism cannot authorize an older writer;
// a focused test turns it off only to pin that refusal.
var enableDeliveryGenerations = true

// genHead is the atomic root-anchored checkpoint: the last committed generation. LastLen is the byte
// length of the manifest record ending at LogBytes, which lets recovery verify the head sits on a real
// record boundary and matches it in O(1), with no whole-lifetime scan.
type genHead struct {
	Version  int    `json:"v"`
	Format   string `json:"format"`
	Seq      int64  `json:"seq"`
	Root     string `json:"root"`
	Chain    string `json:"chain"`
	LogBytes int64  `json:"log_bytes"`
	LastLen  int64  `json:"last_len"`
}

// genRecord is one manifest-log line: an immutable root pointer, chained to its predecessor.
type genRecord struct {
	Version int    `json:"v"`
	Seq     int64  `json:"seq"`
	Root    string `json:"root"`
	Chain   string `json:"chain"`
}

type deliveryGenerations struct {
	dir      string
	headPath string   // absolute path of the head file, for in-package tests and diagnostics
	logPath  string   // absolute path of the manifest, for in-package tests and diagnostics
	confine  *os.Root // head + manifest confinement, parity with the radix's page confinement
	radix    *deliveryRadix
	log      *os.File // append handle, opened THROUGH confine

	mu       sync.Mutex
	seq      int64 // -1 when empty
	root     radixHash
	chain    radixHash
	logBytes int64
	fault    error // once set, every operation refuses (preserve, never fresh identity)
}

// openDeliveryGenerations opens (creating if absent) the generation store rooted at dir and recovers
// the last committed generation. A torn or inconsistent manifest is refused (unavailable), never read
// as an empty store.
func openDeliveryGenerations(dir string) (*deliveryGenerations, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.Long(abs), 0o700); err != nil {
		return nil, err
	}
	confine, err := pinDeliveryDirectory(abs)
	if err != nil {
		return nil, err
	}
	pages, err := pinDeliveryChild(confine, genPagesDir, true)
	if err != nil {
		_ = confine.Close()
		return nil, err
	}
	rx := &deliveryRadix{dir: filepath.Join(abs, genPagesDir), root: pages}
	rx.hashKey = rx.defaultKeyHash
	g := &deliveryGenerations{
		dir:      abs,
		headPath: filepath.Join(abs, genHeadFile),
		logPath:  filepath.Join(abs, genLogFile),
		confine:  confine,
		radix:    rx,
		seq:      -1,
		chain:    genChainSeed,
	}
	if err := g.recover(); err != nil {
		_ = rx.close()
		_ = confine.Close()
		return nil, err
	}
	f, err := openDeliveryAppend(confine, genLogFile, g.logBytes)
	if err != nil {
		_ = rx.close()
		_ = confine.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != g.logBytes {
		_ = f.Close()
		_ = rx.close()
		_ = confine.Close()
		return nil, errGenerationUnavailable
	}
	g.log = f
	return g, nil
}

func (g *deliveryGenerations) close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var err error
	if g.log != nil {
		err = g.log.Close()
		g.log = nil
	}
	if g.radix != nil {
		err = errors.Join(err, g.radix.close())
	}
	if g.confine != nil {
		err = errors.Join(err, g.confine.Close())
		g.confine = nil
	}
	return err
}

// recover establishes in-memory state from the atomic head and the tail beyond it, in bounded time.
func (g *deliveryGenerations) recover() error {
	head, hasHead, err := readGenHead(g.confine)
	if err != nil {
		return err
	}
	linfo, lerr := g.confine.Lstat(genLogFile)
	hasLog := lerr == nil
	if lerr != nil && !os.IsNotExist(lerr) {
		return errGenerationUnavailable
	}
	if hasLog && (!linfo.Mode().IsRegular() || linfo.Size() > genMaxLog) {
		return errGenerationUnavailable
	}
	if !hasHead {
		if hasLog && linfo.Size() > 0 {
			return errGenerationUnavailable // a manifest with no head is inconsistent; preserve
		}
		return nil // fresh, empty store
	}
	if !hasLog {
		return errGenerationUnavailable // a head with no manifest is inconsistent; preserve
	}
	size := linfo.Size()
	if size < head.LogBytes {
		return errGenerationUnavailable // head names bytes the log does not hold; conflicting head
	}
	lf, err := g.confine.Open(genLogFile)
	if err != nil {
		return errGenerationUnavailable
	}
	defer func() { _ = lf.Close() }()
	opened, err := lf.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(linfo, opened) || opened.Size() != size {
		return errGenerationUnavailable
	}
	if err := verifyHeadBoundary(lf, head); err != nil {
		return err // head not on a boundary or disagrees with its record: never trust it
	}
	g.seq, g.root, g.chain, g.logBytes = head.Seq, mustRadixHash(head.Root), mustRadixHash(head.Chain), head.LogBytes
	if size == head.LogBytes {
		return nil // clean: head is the last record
	}
	// A complete, chain-valid record beyond the head is a generation whose head write was lost to a
	// crash; adopt it (idempotent). A torn trailing record is refused and preserved.
	adopted, err := scanGenTail(lf, head, size)
	if err != nil {
		return err
	}
	g.seq, g.root, g.chain, g.logBytes = adopted.seq, adopted.root, adopted.chain, adopted.end
	return g.writeHead(adopted.lastLen)
}

// radixReader is the read half both a committed radix and a write transaction offer: a lookup at an
// explicit root. The generation helpers take one so a commit reads its own uncommitted pages while a
// query reads only committed ones.
type radixReader interface {
	lookup(ctx context.Context, root radixHash, key []byte) ([]byte, bool, error)
}

// genTxnMaxHeld bounds the radix pages one write transaction holds in memory before the commit in
// progress publishes what it has as a generation of its own and continues in a fresh transaction. It
// is a memory bound, not a capacity bound: a large reconcile becomes several generations, each an
// exact, valid superset of the one before. //nomagic:allow in-memory page bound, not a budget
const genTxnMaxHeld = 1 << 15

// genWriter is one commit call's running state: the transaction, the root it has reached, the sessions
// whose settled frontier the batch may have moved, and the generation store it publishes intermediate
// generations to when the transaction reaches its bound.
type genWriter struct {
	g       *deliveryGenerations
	tx      *radixTxn
	root    radixHash
	settled []core.SessionID // sessions with a new settlement since their frontier was last advanced
	seen    map[core.SessionID]bool
	// window, when archiveWindow is the caller, answers the frontier walk for the window's own
	// arrivals (archiveWindowIndex); nil otherwise.
	window *archiveWindowIndex
}

func (g *deliveryGenerations) writer() *genWriter {
	return &genWriter{g: g, tx: g.radix.begin(), root: g.root, seen: map[core.SessionID]bool{}}
}

// spill publishes the transaction's work so far as a generation when it holds more than the bound, with
// every touched session's frontier advanced first so each published generation is consistent. The
// caller holds g.mu.
func (w *genWriter) spill(ctx context.Context) error {
	if w.tx.held() < genTxnMaxHeld {
		return nil
	}
	if err := w.advanceFrontiers(ctx); err != nil {
		return err
	}
	if _, err := w.g.finishCommit(ctx, w.tx, w.root); err != nil {
		return err
	}
	w.tx = w.g.radix.begin()
	return nil
}

func (w *genWriter) insert(ctx context.Context, key, value []byte) error {
	next, err := w.tx.insert(ctx, w.root, key, value)
	if err != nil {
		return err
	}
	w.root = next
	return nil
}

func (w *genWriter) update(ctx context.Context, key []byte, decide radixDecide) error {
	next, err := w.tx.update(ctx, w.root, key, decide)
	if err != nil {
		return err
	}
	w.root = next
	return nil
}

// touched records that session gained a settlement whose frontier advance is still owed.
func (w *genWriter) touched(session core.SessionID) {
	if !w.seen[session] {
		w.seen[session] = true
		w.settled = append(w.settled, session)
	}
}

// advanceFrontiers advances every touched session's frontier once. The watermark only moves past
// contiguous settled arrivals and stops at the first unsettled one, so advancing once after a session's
// settlements are all recorded reaches exactly the value advancing after each of them would.
func (w *genWriter) advanceFrontiers(ctx context.Context) error {
	for _, session := range w.settled {
		if err := w.advanceFrontier(ctx, session); err != nil {
			return err
		}
	}
	w.settled, w.seen = nil, map[core.SessionID]bool{}
	return nil
}

// commit stages every lease's keys into the radix from the current root and, if that changed the
// root, commits a new generation. Re-committing already-recorded leases is idempotent (the root does
// not move, so no generation is appended and the same identities stand). A batch large enough to reach
// genTxnMaxHeld is published as several generations; a failure after the first leaves the earlier ones
// committed, each an exact, valid record of leases that were really admitted, never a changed identity.
//
// Each key is checked and written in one traversal (radixTxn.update): a nonce already recorded with any
// other lease, or an arrival already assigned to any other nonce, is a conflict and nothing of the
// batch after it is published.
func (g *deliveryGenerations) commit(ctx context.Context, leases []deliveryLease) (radixHash, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fault != nil {
		return radixHash{}, g.fault
	}
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	w := g.writer()
	for _, l := range leases {
		if err := w.putLease(ctx, l); err != nil {
			return radixHash{}, err
		}
		if err := w.spill(ctx); err != nil {
			return radixHash{}, err
		}
	}
	return g.finishCommit(ctx, w.tx, w.root)
}

// putLease writes one lease's three keys: the nonce's full lease and the (session, arrival) order entry,
// both immutable once written (keepOrWrite), and the session's arrival watermark, which only ADVANCES —
// re-committing an already-recorded (older) lease must not regress it, which is what keeps a re-commit
// idempotent and dormant-session continuity monotonic.
func (w *genWriter) putLease(ctx context.Context, l deliveryLease) error {
	if !validDeliveryLease(l) {
		return core.ErrContract
	}
	if err := w.update(ctx, nonceGenKey(l.Delivery), keepOrWrite(mustLeaseValue(l))); err != nil {
		return err
	}
	if err := w.update(ctx, orderGenKey(l.Session, l.ArrivalSeq), keepOrWrite([]byte(l.Delivery))); err != nil {
		return err
	}
	arrival := l.ArrivalSeq
	return w.update(ctx, arrivalGenKey(l.Session), func(old []byte, found bool) ([]byte, bool, error) {
		if !found {
			return arrivalValue(arrival), true, nil
		}
		if len(old) != 8 {
			return nil, false, errGenerationUnavailable
		}
		if arrival <= binary.BigEndian.Uint64(old) {
			return nil, false, nil
		}
		return arrivalValue(arrival), true, nil
	})
}

// putAck records one acknowledgement against the lease the store holds for its delivery (the exact
// join: a missing lease is unavailable, a disagreeing identity is a conflict, neither is recorded).
func (w *genWriter) putAck(ctx context.Context, a deliveryAck) error {
	if a.Version != core.EvidenceVersion || !validDeliveryToken(a.Delivery) || a.ObservationID == "" {
		return core.ErrContract
	}
	lease, found, err := leaseIn(ctx, w.tx, w.root, a.Delivery)
	if err != nil {
		return err
	}
	if !found {
		return errGenerationUnavailable // an ack must name a resolvable lease
	}
	return w.putJoinedAck(ctx, lease, a)
}

// putJoinedAck records an acknowledgement whose delivery's stored lease is lease.
func (w *genWriter) putJoinedAck(ctx context.Context, lease deliveryLease, a deliveryAck) error {
	if a.Version != core.EvidenceVersion || a.Delivery != lease.Delivery || a.ObservationID == "" {
		return core.ErrContract
	}
	if lease.ObservationID != a.ObservationID {
		return errGenerationConflict
	}
	if err := w.update(ctx, ackGenKey(a.Delivery), keepOrWrite(mustAckValue(a))); err != nil {
		return err
	}
	w.touched(lease.Session)
	return nil
}

// putTerminal records one terminal disposition against the lease the store holds for its delivery,
// joined exactly as putAck.
func (w *genWriter) putTerminal(ctx context.Context, tm deliveryTerminal) error {
	if !validDeliveryLease(tm.Lease) || tm != terminalFor(tm.Lease) {
		return core.ErrContract
	}
	lease, found, err := leaseIn(ctx, w.tx, w.root, tm.Lease.Delivery)
	if err != nil {
		return err
	}
	if !found {
		return errGenerationUnavailable
	}
	return w.putJoinedTerminal(ctx, lease, tm)
}

// putJoinedTerminal records a terminal disposition whose delivery's stored lease is lease.
func (w *genWriter) putJoinedTerminal(ctx context.Context, lease deliveryLease, tm deliveryTerminal) error {
	if tm != terminalFor(tm.Lease) {
		return core.ErrContract
	}
	if lease != tm.Lease {
		return errGenerationConflict
	}
	if err := w.insert(ctx, termGenKey(lease.Delivery), []byte(lease.ObservationID)); err != nil {
		return err
	}
	w.touched(lease.Session)
	return nil
}

// archiveWindow archives one journal segment's whole window — its leases (in the caller's order), and
// its acknowledgements and terminal dispositions — as the rotation's reconcile. It records exactly what
// commit, commitAck and commitTerminal would record for the same three batches in that order, with the
// same exact joins, but in one pass: a settlement of a lease of this window is joined against the lease
// this pass has just written (the value the store holds for that nonce, since writing it either stored
// it or confirmed an identical one), rather than read back from the pack a spill already wrote. And a
// frontier walk over this window's arrivals answers "which nonce, and is it settled" from the window it
// is archiving (archiveWindowIndex) instead of reading each order and settlement key back; an arrival
// the window does not hold is read from the store as always.
func (g *deliveryGenerations) archiveWindow(ctx context.Context, leases []deliveryLease, acks []deliveryAck, terminals []deliveryTerminal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fault != nil {
		return g.fault
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ackBy := make(map[string]deliveryAck, len(acks))
	for _, a := range acks {
		ackBy[a.Delivery] = a
	}
	termBy := make(map[string]deliveryTerminal, len(terminals))
	for _, tm := range terminals {
		termBy[tm.Lease.Delivery] = tm
	}
	idx := &archiveWindowIndex{nonces: map[core.SessionID]map[uint64]string{}, settled: map[string]bool{}}
	w := g.writer()
	w.window = idx
	for _, l := range leases {
		if err := w.putLease(ctx, l); err != nil {
			return err
		}
		idx.add(l)
		if a, ok := ackBy[l.Delivery]; ok {
			if err := w.putJoinedAck(ctx, l, a); err != nil {
				return err
			}
			idx.settled[l.Delivery] = true
			delete(ackBy, l.Delivery)
		}
		if tm, ok := termBy[l.Delivery]; ok {
			if err := w.putJoinedTerminal(ctx, l, tm); err != nil {
				return err
			}
			idx.settled[l.Delivery] = true
			delete(termBy, l.Delivery)
		}
		if err := w.spill(ctx); err != nil {
			return err
		}
	}
	// Settlements of leases archived before this window (mirrored when they were admitted; recorded
	// again here, idempotently) join against the store's lease.
	for _, a := range acks {
		if _, rest := ackBy[a.Delivery]; !rest {
			continue
		}
		if err := w.putAck(ctx, a); err != nil {
			return err
		}
		if err := w.spill(ctx); err != nil {
			return err
		}
	}
	for _, tm := range terminals {
		if _, rest := termBy[tm.Lease.Delivery]; !rest {
			continue
		}
		if err := w.putTerminal(ctx, tm); err != nil {
			return err
		}
		if err := w.spill(ctx); err != nil {
			return err
		}
	}
	if err := w.advanceFrontiers(ctx); err != nil {
		return err
	}
	_, err := g.finishCommit(ctx, w.tx, w.root)
	return err
}

// archiveWindowIndex is what archiveWindow has written so far for its window: each lease's nonce by
// (session, arrival), and which of those nonces it has recorded a settlement for. A frontier walk reads
// it in place of the store's order and settlement keys for exactly those arrivals — the store holds the
// same answers, because the index is filled only after the matching keys are written.
type archiveWindowIndex struct {
	nonces  map[core.SessionID]map[uint64]string
	settled map[string]bool
}

func (x *archiveWindowIndex) add(l deliveryLease) {
	m := x.nonces[l.Session]
	if m == nil {
		m = map[uint64]string{}
		x.nonces[l.Session] = m
	}
	m[l.ArrivalSeq] = l.Delivery
}

// at answers (nonce, settled, known) for a session's arrival; known is false for an arrival this window
// does not hold, which the walk then reads from the store.
func (x *archiveWindowIndex) at(session core.SessionID, arrival uint64) (string, bool, bool) {
	if x == nil {
		return "", false, false
	}
	nonce, ok := x.nonces[session][arrival]
	if !ok {
		return "", false, false
	}
	return nonce, x.settled[nonce], true
}

// keepOrWrite is the decision for an immutable key: absent, write value; present with exactly value,
// keep it (idempotent); present with anything else, a conflict.
func keepOrWrite(value []byte) radixDecide {
	return func(old []byte, found bool) ([]byte, bool, error) {
		if !found {
			return value, true, nil
		}
		if !bytes.Equal(old, value) {
			return nil, false, errGenerationConflict
		}
		return nil, false, nil
	}
}

// commitAck records durable ACK membership and advances the per-session settled frontier. Each ack is
// joined against its resolved lease FIRST (identity compared), so membership is never a blind marker: a
// missing lease is unavailable, a disagreeing identity is a conflict, neither is recorded.
func (g *deliveryGenerations) commitAck(ctx context.Context, acks []deliveryAck) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fault != nil {
		return g.fault
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w := g.writer()
	for _, a := range acks {
		if err := w.putAck(ctx, a); err != nil {
			return err
		}
		if err := w.spill(ctx); err != nil {
			return err
		}
	}
	if err := w.advanceFrontiers(ctx); err != nil {
		return err
	}
	_, err := g.finishCommit(ctx, w.tx, w.root)
	return err
}

// commitTerminal records durable terminal membership and advances the settled frontier, joined against
// the resolved lease exactly as commitAck. It is the API main's terminal writer calls after handoff so
// an archived lease's terminal disposition settles the frontier without walking the active map.
// It returns error only (main's terminal adapter, delivery_terminal_generation.go, is wired to that).
func (g *deliveryGenerations) commitTerminal(ctx context.Context, terminals []deliveryTerminal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fault != nil {
		return g.fault
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w := g.writer()
	for _, tm := range terminals {
		if err := w.putTerminal(ctx, tm); err != nil {
			return err
		}
		if err := w.spill(ctx); err != nil {
			return err
		}
	}
	if err := w.advanceFrontiers(ctx); err != nil {
		return err
	}
	_, err := g.finishCommit(ctx, w.tx, w.root)
	return err
}

// finishCommit publishes one generation for a root that moved, or returns idempotently: the
// transaction's pages become one durable pack and root pointer, then the manifest record commits the
// generation. The caller holds g.mu and has verified fault/ctx. A pack that could not be written leaves
// the committed state as it was; a manifest append that failed latches the store's fault.
func (g *deliveryGenerations) finishCommit(ctx context.Context, tx *radixTxn, root radixHash) (radixHash, error) {
	if root == g.root {
		return g.root, nil // idempotent: nothing new to commit
	}
	if err := tx.commit(ctx, root); err != nil {
		return radixHash{}, err
	}
	if err := g.appendGeneration(root); err != nil {
		g.fault = errGenerationUnavailable
		return radixHash{}, g.fault
	}
	return g.root, nil
}

// advanceFrontier moves a session's settled-frontier watermark forward past every contiguous settled
// arrival, and records the new value. The watermark is monotonic, so this crosses each arrival at most
// once over the session's life — amortized O(1) per settlement, never a walk of all arrivals.
func (w *genWriter) advanceFrontier(ctx context.Context, session core.SessionID) error {
	f, err := frontierIn(ctx, w.tx, w.root, session)
	if err != nil {
		return err
	}
	last, hasLast, err := arrivalIn(ctx, w.tx, w.root, session)
	if err != nil {
		return err
	}
	if !hasLast {
		return nil // no leases for this session yet: nothing to advance
	}
	moved := f
	for moved <= last {
		settled, found, err := w.settledAt(ctx, session, moved)
		if err != nil {
			return err
		}
		if !found {
			break // a gap (should not occur for dense arrivals): stop rather than skip
		}
		if !settled {
			break
		}
		if moved == ^uint64(0) {
			return core.ErrBudget // the first-pending watermark cannot wrap to zero
		}
		moved++
	}
	if moved == f {
		return nil
	}
	return w.insert(ctx, frontierGenKey(session), arrivalValue(moved))
}

// settledAt reports whether a session's arrival is settled, and whether that arrival has a nonce at all:
// from the archiving window when it holds the arrival, otherwise from the store.
func (w *genWriter) settledAt(ctx context.Context, session core.SessionID, arrival uint64) (settled, found bool, err error) {
	if _, settled, known := w.window.at(session, arrival); known {
		return settled, true, nil
	}
	nonce, found, err := nonceIn(ctx, w.tx, w.root, session, arrival)
	if err != nil || !found {
		return false, found, err
	}
	settled, err = isSettledIn(ctx, w.tx, w.root, nonce)
	return settled, true, err
}

// isSettledIn reports whether a nonce has a durable ack or terminal membership record.
func isSettledIn(ctx context.Context, rd radixReader, root radixHash, nonce string) (bool, error) {
	if _, found, err := rd.lookup(ctx, root, ackGenKey(nonce)); err != nil {
		return false, err
	} else if found {
		return true, nil
	}
	_, found, err := rd.lookup(ctx, root, termGenKey(nonce))
	return found, err
}

// appendGeneration writes the new generation durably: log record + fsync, then the atomic head, then a
// directory fsync. The record's fsync is the commit; the head is the checkpoint written after it.
func (g *deliveryGenerations) appendGeneration(root radixHash) error {
	seq := g.seq + 1
	chain := genChain(g.chain, root)
	line, err := json.Marshal(genRecord{Version: genVersion, Seq: seq, Root: hex.EncodeToString(root[:]), Chain: hex.EncodeToString(chain[:])})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if len(line) > genMaxLine || g.logBytes > genMaxLog-int64(len(line)) {
		return errGenerationUnavailable
	}
	n, err := g.log.Write(line)
	if err != nil || n != len(line) {
		return errGenerationUnavailable
	}
	if err := g.log.Sync(); err != nil {
		return errGenerationUnavailable
	}
	newBytes := g.logBytes + int64(len(line))
	prevSeq, prevRoot, prevChain, prevBytes := g.seq, g.root, g.chain, g.logBytes
	g.seq, g.root, g.chain, g.logBytes = seq, root, chain, newBytes
	if err := g.writeHead(int64(len(line))); err != nil {
		g.seq, g.root, g.chain, g.logBytes = prevSeq, prevRoot, prevChain, prevBytes
		return err
	}
	return paths.SyncDir(g.dir)
}

func (g *deliveryGenerations) writeHead(lastLen int64) error {
	head := genHead{
		Version: genVersion, Format: genFormat, Seq: g.seq,
		Root: hex.EncodeToString(g.root[:]), Chain: hex.EncodeToString(g.chain[:]),
		LogBytes: g.logBytes, LastLen: lastLen,
	}
	b, err := json.Marshal(head)
	if err != nil {
		return err
	}
	if err := g.writeConfinedAtomic(genHeadFile, b); err != nil {
		return errGenerationUnavailable
	}
	return nil
}

// writeConfinedAtomic stages, fsyncs, and atomically renames a small file THROUGH the confined root,
// then fsyncs the directory — the same durable, symlink-safe pattern the radix uses for its pages.
func (g *deliveryGenerations) writeConfinedAtomic(name string, data []byte) error {
	tmp := genTemp + rand.Text()
	f, err := g.confine.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = g.confine.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = g.confine.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = g.confine.Remove(tmp)
		return err
	}
	if err := g.confine.Rename(tmp, name); err != nil {
		_ = g.confine.Remove(tmp)
		return err
	}
	return paths.SyncDir(g.dir)
}

// ── queries ──────────────────────────────────────────────────────────────────────────────────────

// snapshot returns the current root and any latched fault together, so a query fails closed once the
// store is poisoned (I/O poisoning) rather than serving a root a failed commit may have half-moved.
func (g *deliveryGenerations) snapshot() (radixHash, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.root, g.fault
}

// resolveLease returns the canonical lease recorded for delivery in the current (latest) root. found
// is false only on a proven absence; a missing/corrupt page is unavailable, and a collision or a
// non-canonical/invalid stored lease is refused — never a fresh identity.
func (g *deliveryGenerations) resolveLease(ctx context.Context, delivery string) (deliveryLease, bool, error) {
	root, fault := g.snapshot()
	if fault != nil {
		return deliveryLease{}, false, fault
	}
	return g.leaseAtRoot(ctx, root, delivery)
}

func (g *deliveryGenerations) leaseAtRoot(ctx context.Context, root radixHash, delivery string) (deliveryLease, bool, error) {
	return leaseIn(ctx, g.radix, root, delivery)
}

// leaseIn is leaseAtRoot through any reader (a committed radix, or a commit's own transaction).
func leaseIn(ctx context.Context, rd radixReader, root radixHash, delivery string) (deliveryLease, bool, error) {
	raw, found, err := rd.lookup(ctx, root, nonceGenKey(delivery))
	if err != nil {
		return deliveryLease{}, false, err
	}
	if !found {
		return deliveryLease{}, false, nil
	}
	var lease deliveryLease
	if json.Unmarshal(raw, &lease) != nil {
		return deliveryLease{}, false, errGenerationUnavailable
	}
	canon, err := json.Marshal(lease)
	if err != nil || !bytes.Equal(canon, raw) || !validDeliveryLease(lease) || lease.Delivery != delivery {
		return deliveryLease{}, false, errGenerationUnavailable
	}
	return lease, true, nil
}

// resolveTerminalLease is resolveLease under the name main uses to validate a terminal disposition
// whose original lease is ARCHIVED: the terminal loader must not fail merely because the lease left the
// active map — it resolves the archived lease here and compares identity.
func (g *deliveryGenerations) resolveTerminalLease(ctx context.Context, delivery string) (deliveryLease, bool, error) {
	return g.resolveLease(ctx, delivery)
}

func (g *deliveryGenerations) resolveAck(ctx context.Context, delivery string) (deliveryAck, bool, error) {
	root, err := g.snapshot()
	if err != nil {
		return deliveryAck{}, false, err
	}
	raw, found, err := g.radix.lookup(ctx, root, ackGenKey(delivery))
	if err != nil || !found {
		return deliveryAck{}, false, err
	}
	var ack deliveryAck
	if json.Unmarshal(raw, &ack) != nil {
		return deliveryAck{}, false, errGenerationUnavailable
	}
	canonical, err := json.Marshal(ack)
	if err != nil || !bytes.Equal(canonical, raw) || ack.Version != core.EvidenceVersion || ack.Delivery != delivery {
		return deliveryAck{}, false, errGenerationUnavailable
	}
	lease, held, err := g.leaseAtRoot(ctx, root, delivery)
	if err != nil || !held || ack.ObservationID != lease.ObservationID {
		return deliveryAck{}, false, errGenerationUnavailable
	}
	return ack, true, nil
}

// lastArrival returns a session's last committed arrival seq. found is false for a session that never
// leased anything (a genuine absence); the caller's next arrival is found+1, densely continuing even
// after the session was dormant across many generations.
func (g *deliveryGenerations) lastArrival(ctx context.Context, session core.SessionID) (uint64, bool, error) {
	root, fault := g.snapshot()
	if fault != nil {
		return 0, false, fault
	}
	return g.arrivalAtRoot(ctx, root, session)
}

func (g *deliveryGenerations) arrivalAtRoot(ctx context.Context, root radixHash, session core.SessionID) (uint64, bool, error) {
	return arrivalIn(ctx, g.radix, root, session)
}

// arrivalIn is arrivalAtRoot through any reader.
func arrivalIn(ctx context.Context, rd radixReader, root radixHash, session core.SessionID) (uint64, bool, error) {
	raw, found, err := rd.lookup(ctx, root, arrivalGenKey(session))
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}
	if len(raw) != 8 {
		return 0, false, errGenerationUnavailable
	}
	return binary.BigEndian.Uint64(raw), true, nil
}

// nonceAtArrival returns the delivery nonce a session assigned at a given arrival seq — ordered-by-
// arrival access. It is the point lookup a predecessor query is built from; the bounded per-session
// oldest-pending answer is sessionFrontier, which does not walk.
func (g *deliveryGenerations) nonceAtArrival(ctx context.Context, session core.SessionID, arrival uint64) (string, bool, error) {
	root, fault := g.snapshot()
	if fault != nil {
		return "", false, fault
	}
	return g.nonceAtRoot(ctx, root, session, arrival)
}

func (g *deliveryGenerations) nonceAtRoot(ctx context.Context, root radixHash, session core.SessionID, arrival uint64) (string, bool, error) {
	return nonceIn(ctx, g.radix, root, session, arrival)
}

// nonceIn is nonceAtRoot through any reader.
func nonceIn(ctx context.Context, rd radixReader, root radixHash, session core.SessionID, arrival uint64) (string, bool, error) {
	raw, found, err := rd.lookup(ctx, root, orderGenKey(session, arrival))
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	nonce := string(raw)
	if !validDeliveryToken(nonce) {
		return "", false, errGenerationUnavailable
	}
	return nonce, true, nil
}

// sessionFrontier returns the session's oldest still-pending arrival: the smallest arrival that is
// neither acknowledged nor terminal. It reads the watermark and one arrival bound — O(1), no walk over
// all arrivals — because advanceFrontier already skipped the contiguous settled prefix at settle time.
// hasPending is false when the session has no leases, or every arrival through its last is settled.
func (g *deliveryGenerations) sessionFrontier(ctx context.Context, session core.SessionID) (oldestPending uint64, hasPending bool, err error) {
	root, fault := g.snapshot()
	if fault != nil {
		return 0, false, fault
	}
	last, hasLast, err := g.arrivalAtRoot(ctx, root, session)
	if err != nil {
		return 0, false, err
	}
	if !hasLast {
		return 0, false, nil
	}
	f, err := g.frontierAtRoot(ctx, root, session)
	if err != nil {
		return 0, false, err
	}
	if f > last {
		return 0, false, nil
	}
	return f, true, nil
}

// frontierAtRoot reads a session's settled-frontier watermark, defaulting to 1 (arrivals start at 1, so
// "nothing settled yet" is a frontier of 1). A malformed value is unavailable, never a guessed zero.
func (g *deliveryGenerations) frontierAtRoot(ctx context.Context, root radixHash, session core.SessionID) (uint64, error) {
	return frontierIn(ctx, g.radix, root, session)
}

// frontierIn is frontierAtRoot through any reader.
func frontierIn(ctx context.Context, rd radixReader, root radixHash, session core.SessionID) (uint64, error) {
	raw, found, err := rd.lookup(ctx, root, frontierGenKey(session))
	if err != nil {
		return 0, err
	}
	if !found {
		return 1, nil
	}
	if len(raw) != 8 {
		return 0, errGenerationUnavailable
	}
	f := binary.BigEndian.Uint64(raw)
	if f < 1 {
		return 0, errGenerationUnavailable
	}
	return f, nil
}

// verifyArchivedAck resolves an archived acknowledgement's ORIGINAL lease and compares every identity
// field. It replaces an unchecked join: a root's content hash proves byte integrity, not that an unread
// join is semantically correct, so the lease is read and its identity checked here. A missing lease is
// unavailable, an identity mismatch is a conflict, and a corrupt page is unavailable.
func (g *deliveryGenerations) verifyArchivedAck(ctx context.Context, ack deliveryAck) error {
	if !validDeliveryToken(ack.Delivery) || ack.ObservationID == "" {
		return core.ErrContract
	}
	lease, found, err := g.resolveLease(ctx, ack.Delivery)
	if err != nil {
		return err
	}
	if !found {
		return errGenerationUnavailable // an archived ack must resolve its lease; never accept blindly
	}
	if lease.Delivery != ack.Delivery || lease.ObservationID != ack.ObservationID {
		return errGenerationConflict
	}
	return nil
}

func (g *deliveryGenerations) currentRoot() radixHash {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.root
}

// generationCount is the number of committed generations; oldestGenerationSeq is 0 when any exist and
// -1 when empty. These are the bounded frontier metadata main pairs with its active lease map.
func (g *deliveryGenerations) generationCount() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seq + 1
}

func (g *deliveryGenerations) oldestGenerationSeq() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seq < 0 {
		return -1
	}
	return 0
}

// ── key and value encodings ──────────────────────────────────────────────────────────────────────

func nonceGenKey(delivery string) []byte    { return append([]byte("n\x00"), delivery...) }
func arrivalGenKey(s core.SessionID) []byte { return append([]byte("a\x00"), string(s)...) }
func ackGenKey(delivery string) []byte      { return append([]byte("k\x00"), delivery...) }
func termGenKey(delivery string) []byte     { return append([]byte("t\x00"), delivery...) }
func frontierGenKey(s core.SessionID) []byte {
	return append([]byte("f\x00"), string(s)...)
}

func orderGenKey(s core.SessionID, a uint64) []byte {
	return []byte(fmt.Sprintf("o\x00%s\x00%020d", string(s), a))
}

func arrivalValue(seq uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], seq)
	return b[:]
}

func mustLeaseValue(l deliveryLease) []byte {
	b, _ := json.Marshal(l) // validDeliveryLease already gated the fields; a marshal error cannot occur
	return b
}

func mustAckValue(a deliveryAck) []byte {
	b, _ := json.Marshal(a)
	return b
}

func genChain(prev, root radixHash) radixHash {
	buf := make([]byte, 0, 64)
	buf = append(buf, prev[:]...)
	buf = append(buf, root[:]...)
	return radixDigest(genChainSalt, buf)
}

// ── head and manifest IO ─────────────────────────────────────────────────────────────────────────

func readGenHead(confine *os.Root) (genHead, bool, error) {
	info, err := confine.Lstat(genHeadFile)
	if os.IsNotExist(err) {
		return genHead{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > genMaxLine {
		return genHead{}, false, errGenerationUnavailable
	}
	f, err := confine.Open(genHeadFile)
	if err != nil {
		return genHead{}, false, errGenerationUnavailable
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return genHead{}, false, errGenerationUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, genMaxLine+1))
	if err != nil || len(raw) > genMaxLine {
		return genHead{}, false, errGenerationUnavailable
	}
	var head genHead
	if json.Unmarshal(raw, &head) != nil || head.Version != genVersion || head.Format != genFormat ||
		head.Seq < 0 || head.LogBytes <= 0 || head.LastLen < 1 || head.LastLen > head.LogBytes ||
		head.LastLen > genMaxLine {
		return genHead{}, false, errGenerationUnavailable
	}
	canon, err := json.Marshal(head)
	if err != nil || !bytes.Equal(canon, raw) {
		return genHead{}, false, errGenerationUnavailable
	}
	if _, ok := hexToRadixHash(head.Root); !ok {
		return genHead{}, false, errGenerationUnavailable
	}
	if _, ok := hexToRadixHash(head.Chain); !ok {
		return genHead{}, false, errGenerationUnavailable
	}
	return head, true, nil
}

// verifyHeadBoundary confirms in O(1) that the record of length head.LastLen ending at head.LogBytes
// sits on a record boundary and decodes to exactly {head.Seq, head.Root, head.Chain}. This is the
// root-anchored predecessor-identity check that replaces a whole-lifetime scan.
func verifyHeadBoundary(lf *os.File, head genHead) error {
	start := head.LogBytes - head.LastLen
	if start < 0 {
		return errGenerationUnavailable
	}
	if start > 0 {
		var b [1]byte
		if _, err := lf.ReadAt(b[:], start-1); err != nil || b[0] != '\n' {
			return errGenerationUnavailable // head.LogBytes is not on a record boundary
		}
	}
	buf := make([]byte, head.LastLen)
	if _, err := lf.ReadAt(buf, start); err != nil {
		return errGenerationUnavailable
	}
	var rec genRecord
	root, chain, ok := decodeGenRecord(buf, &rec)
	if !ok || rec.Seq != head.Seq ||
		hex.EncodeToString(root[:]) != head.Root || hex.EncodeToString(chain[:]) != head.Chain {
		return errGenerationUnavailable
	}
	return nil
}

// genTailState is the last valid record scanGenTail found beyond the head.
type genTailState struct {
	seq     int64
	root    radixHash
	chain   radixHash
	end     int64
	lastLen int64
}

// scanGenTail validates the manifest bytes in (head.LogBytes, size) as chain extensions of the head. It
// adopts every complete, chain-valid record and returns the last state; a torn trailing record is
// refused (unavailable) and its bytes are left untouched. Work is bounded by the tail length, which is
// at most one committed-but-un-headed record plus a torn partial.
func scanGenTail(lf *os.File, head genHead, size int64) (genTailState, error) {
	if _, err := lf.Seek(head.LogBytes, io.SeekStart); err != nil {
		return genTailState{}, errGenerationUnavailable
	}
	rd := newGenLineReader(lf)
	last := genTailState{
		seq: head.Seq, root: mustRadixHash(head.Root), chain: mustRadixHash(head.Chain),
		end: head.LogBytes, lastLen: head.LastLen,
	}
	for last.end < size {
		line, complete, rerr := rd.next()
		if rerr != nil {
			return genTailState{}, errGenerationUnavailable
		}
		if len(line) == 0 && !complete {
			break // clean EOF exactly at the last record boundary
		}
		if !complete {
			return genTailState{}, errGenerationUnavailable // torn trailing partial: preserve, refuse
		}
		var rec genRecord
		root, chain, ok := decodeGenRecord(line, &rec)
		if !ok || rec.Seq != last.seq+1 || chain != genChain(last.chain, root) {
			return genTailState{}, errGenerationUnavailable
		}
		last = genTailState{seq: rec.Seq, root: root, chain: chain, end: last.end + int64(len(line)), lastLen: int64(len(line))}
	}
	if last.end != size {
		return genTailState{}, errGenerationUnavailable // trailing bytes that did not form a record
	}
	return last, nil
}

func decodeGenRecord(line []byte, rec *genRecord) (radixHash, radixHash, bool) {
	if json.Unmarshal(line, rec) != nil || rec.Version != genVersion {
		return radixHash{}, radixHash{}, false
	}
	canon, err := json.Marshal(*rec)
	if err != nil || len(canon)+1 != len(line) || !bytes.Equal(canon, line[:len(canon)]) || line[len(line)-1] != '\n' {
		return radixHash{}, radixHash{}, false
	}
	root, ok1 := hexToRadixHash(rec.Root)
	chain, ok2 := hexToRadixHash(rec.Chain)
	if !ok1 || !ok2 {
		return radixHash{}, radixHash{}, false
	}
	return root, chain, true
}

func hexToRadixHash(s string) (radixHash, bool) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != radixHashBytes {
		return radixHash{}, false
	}
	var h radixHash
	copy(h[:], b)
	return h, true
}

// mustRadixHash decodes a hash string already validated by readGenHead/decodeGenRecord.
func mustRadixHash(s string) radixHash {
	h, _ := hexToRadixHash(s)
	return h
}

// genLineReader reads newline-delimited records with a per-line bound, reporting whether the final
// bytes formed a complete (newline-terminated) record or a torn tail.
type genLineReader struct {
	f   *os.File
	buf []byte
	pos int
	n   int
}

func newGenLineReader(f *os.File) *genLineReader {
	return &genLineReader{f: f, buf: make([]byte, 64<<10)}
}

func (r *genLineReader) next() (line []byte, complete bool, err error) {
	var acc []byte
	for {
		for r.pos < r.n {
			c := r.buf[r.pos]
			r.pos++
			acc = append(acc, c)
			if len(acc) > genMaxLine {
				return nil, false, errGenerationUnavailable
			}
			if c == '\n' {
				return acc, true, nil
			}
		}
		m, rerr := r.f.Read(r.buf)
		r.n, r.pos = m, 0
		if m == 0 {
			if rerr == io.EOF || rerr == nil {
				return acc, len(acc) == 0, nil // empty acc at EOF = clean; non-empty = torn tail
			}
			return nil, false, rerr
		}
	}
}
