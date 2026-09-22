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
	genFormat    = "qompack.delivery.generations.v1"
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

// enableDeliveryGenerations gates the LIVE wiring of the generation store into the delivery journal
// (openGenerations + the decide-consult and the lease/ack mirrors). It is default OFF, and turning it
// on is the READER/WRITER half of rollover: every admitted lease and ack mirrors into the durable
// superset index, and decide resolves an archived nonce's redelivery from it instead of minting a
// fresh identity. The capacity-FREEING half — compacting settled leases out of the active journal so
// the 65536/64MiB budget is reclaimed — is deliberately NOT enabled here, because it needs a
// cross-package change store GC owns: gcrun.go reads the EXACT delivery-leases.jsonl (and
// delivery-acks.jsonl) as retention roots and does not glob segments, so rotating the active file away
// would strip retention from still-open leases and risk object loss. See
// delivery-capacity-integration-work.md §"Unresolved cross-package contract". A focused test flips this
// to exercise the wired path; production leaves it off until that contract is resolved.
var enableDeliveryGenerations = false

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

// commit stages every lease's keys into the radix from the current root and, if that changed the
// root, commits ONE new generation. Re-committing already-recorded leases is idempotent (the root does
// not move, so no generation is appended and the same identities stand).
func (g *deliveryGenerations) commit(ctx context.Context, leases []deliveryLease) (radixHash, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fault != nil {
		return radixHash{}, g.fault
	}
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	root := g.root
	for _, l := range leases {
		if !validDeliveryLease(l) {
			return radixHash{}, core.ErrContract
		}
		prior, exists, err := g.leaseAtRoot(ctx, root, l.Delivery)
		if err != nil {
			return radixHash{}, err
		}
		if exists && prior != l {
			return radixHash{}, errGenerationConflict
		}
		nonce, assigned, err := g.nonceAtRoot(ctx, root, l.Session, l.ArrivalSeq)
		if err != nil {
			return radixHash{}, err
		}
		if assigned && nonce != l.Delivery {
			return radixHash{}, errGenerationConflict
		}
		var next radixHash
		if next, err = g.radix.insert(ctx, root, nonceGenKey(l.Delivery), mustLeaseValue(l)); err != nil {
			return radixHash{}, err
		}
		root = next
		// The arrival watermark only ADVANCES: re-committing an already-recorded (older) lease must not
		// regress it, which is what keeps a re-commit idempotent and dormant-session continuity monotonic.
		cur, has, err := g.arrivalAtRoot(ctx, root, l.Session)
		if err != nil {
			return radixHash{}, err
		}
		if !has || l.ArrivalSeq > cur {
			if next, err = g.radix.insert(ctx, root, arrivalGenKey(l.Session), arrivalValue(l.ArrivalSeq)); err != nil {
				return radixHash{}, err
			}
			root = next
		}
		if next, err = g.radix.insert(ctx, root, orderGenKey(l.Session, l.ArrivalSeq), []byte(l.Delivery)); err != nil {
			return radixHash{}, err
		}
		root = next
	}
	return g.finishCommit(root)
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
	root := g.root
	for _, a := range acks {
		if a.Version != core.EvidenceVersion || !validDeliveryToken(a.Delivery) || a.ObservationID == "" {
			return core.ErrContract
		}
		lease, found, err := g.leaseAtRoot(ctx, root, a.Delivery)
		if err != nil {
			return err
		}
		if !found {
			return errGenerationUnavailable // an ack must name a resolvable lease
		}
		if lease.ObservationID != a.ObservationID {
			return errGenerationConflict
		}
		prior, exists, err := g.radix.lookup(ctx, root, ackGenKey(a.Delivery))
		if err != nil {
			return err
		}
		if exists && !bytes.Equal(prior, mustAckValue(a)) {
			return errGenerationConflict
		}
		next, err := g.radix.insert(ctx, root, ackGenKey(a.Delivery), mustAckValue(a))
		if err != nil {
			return err
		}
		root = next
		if root, err = g.advanceFrontier(ctx, root, lease.Session); err != nil {
			return err
		}
	}
	_, err := g.finishCommit(root)
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
	root := g.root
	for _, tm := range terminals {
		if !validDeliveryLease(tm.Lease) || tm != terminalFor(tm.Lease) {
			return core.ErrContract
		}
		lease, found, err := g.leaseAtRoot(ctx, root, tm.Lease.Delivery)
		if err != nil {
			return err
		}
		if !found {
			return errGenerationUnavailable
		}
		if lease != tm.Lease {
			return errGenerationConflict
		}
		next, err := g.radix.insert(ctx, root, termGenKey(lease.Delivery), []byte(lease.ObservationID))
		if err != nil {
			return err
		}
		root = next
		if root, err = g.advanceFrontier(ctx, root, lease.Session); err != nil {
			return err
		}
	}
	_, err := g.finishCommit(root)
	return err
}

// finishCommit appends one generation for a root that moved, or returns idempotently. The caller holds
// g.mu and has verified fault/ctx.
func (g *deliveryGenerations) finishCommit(root radixHash) (radixHash, error) {
	if root == g.root {
		return g.root, nil // idempotent: nothing new to commit
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
func (g *deliveryGenerations) advanceFrontier(ctx context.Context, root radixHash, session core.SessionID) (radixHash, error) {
	f, err := g.frontierAtRoot(ctx, root, session)
	if err != nil {
		return radixHash{}, err
	}
	last, hasLast, err := g.arrivalAtRoot(ctx, root, session)
	if err != nil {
		return radixHash{}, err
	}
	if !hasLast {
		return root, nil // no leases for this session yet: nothing to advance
	}
	moved := f
	for moved <= last {
		nonce, found, err := g.nonceAtRoot(ctx, root, session, moved)
		if err != nil {
			return radixHash{}, err
		}
		if !found {
			break // a gap (should not occur for dense arrivals): stop rather than skip
		}
		settled, err := g.isSettled(ctx, root, nonce)
		if err != nil {
			return radixHash{}, err
		}
		if !settled {
			break
		}
		if moved == ^uint64(0) {
			return radixHash{}, core.ErrBudget // the first-pending watermark cannot wrap to zero
		}
		moved++
	}
	if moved == f {
		return root, nil
	}
	return g.radix.insert(ctx, root, frontierGenKey(session), arrivalValue(moved))
}

// isSettled reports whether a nonce has a durable ack or terminal membership record.
func (g *deliveryGenerations) isSettled(ctx context.Context, root radixHash, nonce string) (bool, error) {
	if _, found, err := g.radix.lookup(ctx, root, ackGenKey(nonce)); err != nil {
		return false, err
	} else if found {
		return true, nil
	}
	_, found, err := g.radix.lookup(ctx, root, termGenKey(nonce))
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
	raw, found, err := g.radix.lookup(ctx, root, nonceGenKey(delivery))
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
	raw, found, err := g.radix.lookup(ctx, root, arrivalGenKey(session))
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
	raw, found, err := g.radix.lookup(ctx, root, orderGenKey(session, arrival))
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
	raw, found, err := g.radix.lookup(ctx, root, frontierGenKey(session))
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
