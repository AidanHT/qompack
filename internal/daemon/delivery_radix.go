package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/paths"
)

// deliveryRadix is a small, exact, immutable on-disk Merkle radix (binary Patricia) index over the
// 256-bit hash of an opaque key, for the delivery journal's lease/ACK/session keys. It is a PRIMITIVE:
// it stores and retrieves (key → value) with sound proof of absence, path-copy persistence and
// content-addressed pages. It does NOT interpret keys, own the "current root", validate lease/ACK
// semantics, close the journal's capacity bound, or enable any migration — the parent (the journal
// owner) does all of that. This file has no dependency beyond the standard library and internal/paths
// (long-path and directory-fsync helpers already used by the store's os.Root code).
//
// # Shape
//
// The tree is a binary Patricia trie over the bits of H(domain‖key) (MSB first). Path compression
// keeps a branch node's common bits as a "skip" so a leaf costs O(distinct-prefix) pages, never 256.
// Two page kinds, each immutable and content-addressed:
//
//   - LEAF: the full 32-byte key hash, the ORIGINAL key bytes (compared at the leaf so a hash
//     collision can never be mistaken for a match or silently overwrite another key), and the value.
//   - BRANCH: skipLen + skipBits (the common prefix bits below this node) and two non-zero child
//     hashes; it splits on the bit at depth+skipLen. Both children are always present (the Patricia
//     invariant: a one-child branch is always collapsed).
//
// A page's identity is the hash of its bytes, so a corrupt, truncated or tampered page cannot be read
// as anything but itself: every read recomputes the hash and refuses a mismatch. The ROOT of a tree is
// the content hash of its root page; the zero hash denotes the empty tree.
//
// # Physical storage: packs and root pointers (V6 close-out C1.10)
//
// The page format above is unchanged; where pages LIVE is not. The first layout wrote every page as a
// file of its own with a file fsync and a directory fsync, and path-copied on every key, so one
// committed lease cost dozens of file creations: 1.85 s per lease on Windows and 1.70 s on Linux in the
// recorded probe (plans/sdd/V6-closeout/rollover/runs/00-*). Pages are now written in batches:
//
//   - A write transaction (radixTxn) holds the pages its inserts create in memory. Pages the batch
//     replaces are dropped as it goes, so the batch keeps only its live new pages.
//   - Committing a transaction appends every new page reachable from the new root to ONE new pack file,
//     children before parents, in a single sequential write with one fsync, then publishes the pack
//     under packs/<id>.pack by an atomic create-new link. A pack is immutable once published.
//   - Each committed root also gets a small ROOT POINTER file, <shard>/<root hash>.root, naming the pack
//     and offset of the root's record, so a root known only by its hash (a generation head, a segment
//     base root) is found in O(1).
//   - A pack record is the page bytes plus, for a branch, the pack locations of its two children. The
//     locations are hints, not identity: the page hash covers only the page bytes, and every read checks
//     the bytes found at a location against the hash its parent (or the root pointer's name) expects. A
//     wrong or tampered location therefore reads as UNAVAILABLE, never as a different page.
//
// So one generation costs one pack file and one root pointer whatever its size, and a lookup is a
// bounded number of positioned reads on already-open, immutable files.
//
// # Soundness of absence (why "valid page" is not "valid placement")
//
// A lookup follows the target's bits. At each BRANCH it verifies the target's bits match the stored
// skip; a mismatch proves the target is not in that subtree (Patricia: the whole subtree shares those
// bits) and is a sound ABSENCE. At a LEAF it verifies the leaf's hash agrees with the path bits taken
// so far — a page that merely parses is not proof it sits where the tree says it does — and only then
// compares the full hash and the original key. Because pages are content-addressed and the caller
// trusts a root it committed, a page whose bytes were corrupted fails the hash check and is reported
// UNAVAILABLE, never a wrong value and never a wrong absence.
//
// # Failure vocabulary (unavailable is never absence)
//
// A missing, corrupt, truncated or unknown-version page (or pack, or root pointer) under a NON-ZERO root
// is `errRadixUnavailable` (an error), never (nil,false,nil). A genuine absence proven by a complete
// traversal is (nil,false,nil). A hash collision is `errRadixCollision` — an unavailable-class error,
// never an overwrite and never an absence. Oversize input is `errRadixTooLarge`. All three are distinct
// so the parent can map them; none is ever silently downgraded to absence.

const (
	radixMagic      = "QRDX"
	radixVersion    = 1
	radixKindLeaf   = byte(0)
	radixKindBranch = byte(1)

	radixHashBytes = sha256.Size
	radixHashBits  = radixHashBytes * 8

	// Bounds. Keys and values for lease/ACK/session records are small; these caps keep one page
	// bounded and stop a hostile file from forcing an unbounded read or allocation.
	radixMaxKeyBytes   = 4096 //nomagic:allow max original-key bytes stored in a leaf (format bound)
	radixMaxValueBytes = 1 << 16
	radixMaxPageBytes  = radixMaxKeyBytes + radixMaxValueBytes + 128

	// radixKeyDomain and radixPageDomain separate the two hashes so a page digest and a key digest
	// can never be confused, and neither can collide with any other digest in the tree.
	radixKeyDomain  = "qompack.delivery.radix.key.v1"
	radixPageDomain = "qompack.delivery.radix.page.v1"

	radixTempPrefix = ".tmp-"

	// Physical storage (packs + root pointers). Both headers carry their own magic and version so a
	// file of the wrong kind, or of a layout this build does not read, is unavailable, never guessed at.
	radixPacksDir      = "packs"
	radixPackSuffix    = ".pack"
	radixRootSuffix    = ".root"
	radixPackMagic     = "QRDXPK\x00\x01"
	radixRootMagic     = "QRDXRT\x00\x01"
	radixPackHeaderLen = len(radixPackMagic)
	// radixRootFileLen is a root pointer's exact size: the magic, then the pack id and record offset.
	radixRootFileLen = len(radixRootMagic) + 16
	// radixRecordHeaderLen is a pack record's length prefix; radixTrailerLen is a branch record's
	// two child locations (pack id + offset each).
	radixRecordHeaderLen = 4
	radixTrailerLen      = 32
	// radixRecordReadAhead is how much of a record the first positioned read takes: enough for the
	// header, a lease-sized leaf or a branch and its trailer.
	radixRecordReadAhead = 512
	// radixMaxOpenPacks bounds the pack handles one index keeps open.
	radixMaxOpenPacks = 64
	// radixPackWriteBuffer sizes the sequential pack writer.
	radixPackWriteBuffer = 1 << 20
	// radixNodeCacheMax bounds the decoded committed BRANCH pages one index keeps in memory (a few
	// hundred bytes each). The upper levels of the tree are on every path, so caching them turns most
	// of a lookup's positioned reads into map hits.
	radixNodeCacheMax = 1 << 15
)

var (
	// errRadixUnavailable marks a page that could not be read as itself: missing, unreadable,
	// truncated, over the size bound, an unknown version, a structurally invalid node, or a
	// placement inconsistent with the path taken. It is NEVER an assertion of absence.
	errRadixUnavailable = errors.New("delivery radix: page unavailable")
	// errRadixCollision marks two distinct original keys that hash to the same 256-bit digest. The
	// primitive refuses rather than overwriting the resident key, and a lookup of the intruding key
	// is unavailable rather than a false match or a false absence.
	errRadixCollision = errors.New("delivery radix: key-hash collision")
	// errRadixTooLarge marks a key or value beyond the fixed bound.
	errRadixTooLarge = errors.New("delivery radix: input exceeds bound")
)

// radixHash is a page or key digest. The zero value is the empty-tree root and the "no child"
// sentinel (which is illegal inside a branch, where both children must be present).
type radixHash [radixHashBytes]byte

func (h radixHash) isZero() bool { return h == radixHash{} }

// radixLoc names where a committed page's record lives: a pack id and the record's byte offset. The
// zero value means "not yet committed" (a page still held by a write transaction) or "unknown".
type radixLoc struct {
	pack uint64
	off  uint64
}

func (l radixLoc) valid() bool { return l.pack != 0 && l.off >= uint64(radixPackHeaderLen) }

// radixNode is a decoded page held only for the duration of one step; the tree is never loaded whole.
type radixNode struct {
	kind byte
	// leaf
	keyHash radixHash
	key     []byte
	value   []byte
	// branch
	skipLen        int
	skipBits       []byte
	child0, child1 radixHash
	// loc0 and loc1 are the children's pack locations: read from a committed record's trailer, or, for
	// a page a transaction holds, the location of a child that was already committed (zero for a child
	// the same transaction holds).
	loc0, loc1 radixLoc
}

// deliveryRadix is one on-disk index rooted at a directory, confined by os.Root so a swapped symlink
// cannot redirect a read or a write outside it. It holds no tree state: every operation takes an
// explicit root hash and returns a new one, so the parent owns which root is live.
type deliveryRadix struct {
	dir  string
	root *os.Root
	// hashKey maps an original key to its 256-bit digest. It is a field only so a test can inject a
	// controlled digest (to exercise a collision or a long shared prefix without a real preimage);
	// production always uses defaultKeyHash.
	hashKey func([]byte) radixHash

	// packMu guards the open-pack cache. Packs are immutable once published, so a cached handle and
	// size stay valid; a handle is closed only when no read holds it (refs == 0).
	packMu   sync.Mutex
	packs    map[uint64]*radixPackHandle
	packTick uint64

	// cacheMu guards cache: decoded COMMITTED branch pages, by content hash, with the location each was
	// read from or written to. A committed page is immutable and named by its hash, so a cached entry
	// can never go stale; only pages read and verified from disk, or published by a commit that
	// succeeded, are ever entered. Bounded by radixNodeCacheMax.
	cacheMu sync.Mutex
	cache   map[radixHash]radixCached
}

// radixCached is one cached committed page and its own location.
type radixCached struct {
	node radixNode
	loc  radixLoc
}

type radixPackHandle struct {
	f    *os.File
	size int64
	refs int
	used uint64
}

// openDeliveryRadix opens (creating if absent) the index rooted at dir. Nothing else in this package
// interprets the returned handle; the journal owner drives it.
func openDeliveryRadix(dir string) (*deliveryRadix, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.Long(abs), 0o700); err != nil {
		return nil, err
	}
	root, err := pinDeliveryDirectory(abs)
	if err != nil {
		return nil, err
	}
	r := &deliveryRadix{dir: abs, root: root}
	r.hashKey = r.defaultKeyHash
	return r, nil
}

// close releases every cached pack handle and the confining root.
func (r *deliveryRadix) close() error {
	r.packMu.Lock()
	var err error
	for id, ph := range r.packs {
		err = errors.Join(err, ph.f.Close())
		delete(r.packs, id)
	}
	r.packMu.Unlock()
	return errors.Join(err, r.root.Close())
}

func (r *deliveryRadix) defaultKeyHash(key []byte) radixHash {
	return radixDigest(radixKeyDomain, key)
}

// lookup returns the value stored under key in the COMMITTED tree rooted at root. found is false with a
// nil error ONLY when a complete, valid traversal proved the key is not present; a missing or corrupt
// page, or a collision, returns an error instead (never a false absence).
func (r *deliveryRadix) lookup(ctx context.Context, root radixHash, key []byte) (value []byte, found bool, err error) {
	return r.lookupIn(ctx, nil, root, key)
}

// lookupIn is lookup over the committed pages plus a write transaction's held pages (pend may be nil).
func (r *deliveryRadix) lookupIn(ctx context.Context, pend map[radixHash]*radixNode, root radixHash, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if root.isZero() {
		return nil, false, nil // the empty tree holds nothing; a real absence, not unavailability
	}
	target := r.hashKey(key)
	cur, curLoc := root, radixLoc{}
	depth := 0
	for step := 0; step <= radixHashBits; step++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		node, _, err := r.nodeAt(pend, cur, curLoc)
		if err != nil {
			return nil, false, err
		}
		switch node.kind {
		case radixKindLeaf:
			// The leaf's hash must agree with the bits the path already committed to; a page that
			// merely parses is not proof of placement.
			if !bitsEqual(node.keyHash[:], target[:], 0, depth) {
				return nil, false, errRadixUnavailable
			}
			if node.keyHash == target {
				if bytes.Equal(node.key, key) {
					return append([]byte(nil), node.value...), true, nil
				}
				return nil, false, errRadixCollision
			}
			return nil, false, nil // shares the prefix, differs in the full hash: a sound absence
		case radixKindBranch:
			splitBit := depth + node.skipLen
			if splitBit >= radixHashBits {
				return nil, false, errRadixUnavailable
			}
			if _, matched := comparePrefix(target, depth, node.skipBits, node.skipLen); !matched {
				return nil, false, nil // diverges inside the common prefix: a sound absence
			}
			next, nextLoc := node.child0, node.loc0
			if bitAt(target[:], splitBit) == 1 {
				next, nextLoc = node.child1, node.loc1
			}
			if next.isZero() {
				return nil, false, errRadixUnavailable
			}
			cur, curLoc = next, nextLoc
			depth = splitBit + 1
		default:
			return nil, false, errRadixUnavailable
		}
	}
	// A well-formed tree consumes at most 256 bits before reaching a leaf; exceeding it means the
	// pages disagree with a valid Patricia shape.
	return nil, false, errRadixUnavailable
}

// nodeAt resolves page h: a page the transaction holds, a committed page at a known location, or a
// committed ROOT found through its root pointer. It returns the page's own location (zero for a held
// page). A committed non-root page reached without a location is unavailable.
func (r *deliveryRadix) nodeAt(pend map[radixHash]*radixNode, h radixHash, loc radixLoc) (radixNode, radixLoc, error) {
	return r.nodeAtRA(pend, nil, h, loc)
}

// nodeAtRA is nodeAt reading committed records through a transaction's read-ahead windows (ra may be
// nil).
func (r *deliveryRadix) nodeAtRA(pend map[radixHash]*radixNode, ra *radixReadAhead, h radixHash, loc radixLoc) (radixNode, radixLoc, error) {
	if pend != nil {
		if n, ok := pend[h]; ok {
			return *n, radixLoc{}, nil
		}
	}
	if c, ok := r.cached(h); ok {
		return c.node, c.loc, nil
	}
	var (
		n   radixNode
		err error
	)
	if loc.valid() {
		n, err = r.readRecordRA(h, loc, ra)
	} else {
		n, loc, err = r.readRoot(h)
	}
	if err != nil {
		return radixNode{}, radixLoc{}, err
	}
	r.remember(h, n, loc)
	return n, loc, nil
}

// cached returns the cached committed page named h, if any.
func (r *deliveryRadix) cached(h radixHash) (radixCached, bool) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	c, ok := r.cache[h]
	return c, ok
}

// remember enters a committed BRANCH page read or published at loc. At the bound it first drops an
// eighth of the entries (the map's own iteration order picks them), so the cache stays bounded and a
// dropped page is simply read again when it is next on a path.
func (r *deliveryRadix) remember(h radixHash, n radixNode, loc radixLoc) {
	if n.kind != radixKindBranch || !loc.valid() {
		return
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if r.cache == nil {
		r.cache = map[radixHash]radixCached{}
	}
	if len(r.cache) >= radixNodeCacheMax {
		drop := radixNodeCacheMax / 8
		for k := range r.cache {
			if drop == 0 {
				break
			}
			delete(r.cache, k)
			drop--
		}
	}
	r.cache[h] = radixCached{node: n, loc: loc}
}

// insert returns the root of the tree that is root with (key → value) added or updated, and makes it
// durable before returning: it is one write transaction committed on its own (one pack, one root
// pointer). root is never mutated, so a caller holding an older root keeps a usable, immutable
// generation. Callers with many keys use begin/insert/commit to pay for one pack instead of many.
func (r *deliveryRadix) insert(ctx context.Context, root radixHash, key, value []byte) (radixHash, error) {
	tx := r.begin()
	next, err := tx.insert(ctx, root, key, value)
	if err != nil {
		return radixHash{}, err
	}
	if err := tx.commit(ctx, next); err != nil {
		return radixHash{}, err
	}
	return next, nil
}

// radixTxn is one write transaction: the pages its inserts create, held in memory (decoded; their
// bytes are re-encoded deterministically when committed) until commit writes the ones the final root
// reaches. It is used by one goroutine; readers of committed roots never see it.
type radixTxn struct {
	r       *deliveryRadix
	pending map[radixHash]*radixNode
	// ra is merge's read-ahead over committed packs, created by the first merge and dropped at commit.
	ra *radixReadAhead
}

func (r *deliveryRadix) begin() *radixTxn {
	return &radixTxn{r: r, pending: map[radixHash]*radixNode{}}
}

// radixDecide chooses what an update stores under its key, in the same traversal that finds what is
// there: given the value already stored (found), it returns the value to store and whether to write
// at all. An error aborts the update with nothing changed.
type radixDecide func(old []byte, found bool) (value []byte, write bool, err error)

// lookup is lookup over the committed tree plus this transaction's held pages.
func (t *radixTxn) lookup(ctx context.Context, root radixHash, key []byte) ([]byte, bool, error) {
	return t.r.lookupIn(ctx, t.pending, root, key)
}

// held reports how many pages the transaction holds (a memory measure for callers that chunk).
func (t *radixTxn) held() int { return len(t.pending) }

// insert adds (key → value) to root inside the transaction and returns the new root. Nothing is written
// to disk until commit. An identical (key, value) leaves the root as it was.
func (t *radixTxn) insert(ctx context.Context, root radixHash, key, value []byte) (radixHash, error) {
	if len(value) > radixMaxValueBytes {
		return radixHash{}, errRadixTooLarge
	}
	return t.update(ctx, root, key, func(old []byte, found bool) ([]byte, bool, error) {
		if found && bytes.Equal(old, value) {
			return nil, false, nil
		}
		return value, true, nil
	})
}

// update is insert with the stored value decided by decide from what the one traversal finds under key
// — the check-then-write a caller would otherwise make as a lookup and a second traversal. Nothing is
// written to disk until commit; a decide that writes nothing leaves the root as it was.
func (t *radixTxn) update(ctx context.Context, root radixHash, key []byte, decide radixDecide) (radixHash, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	if len(key) == 0 || len(key) > radixMaxKeyBytes {
		return radixHash{}, errRadixTooLarge
	}
	target := t.r.hashKey(key)
	if root.isZero() {
		value, write, err := decideChecked(decide, nil, false)
		if err != nil || !write {
			return root, err
		}
		return t.writeLeaf(target, key, value)
	}
	h, _, err := t.insertAt(ctx, root, radixLoc{}, 0, target, key, decide)
	return h, err
}

// decideChecked runs decide and bounds the value it chose.
func decideChecked(decide radixDecide, old []byte, found bool) ([]byte, bool, error) {
	value, write, err := decide(old, found)
	if err != nil || !write {
		return nil, false, err
	}
	if len(value) > radixMaxValueBytes {
		return nil, false, errRadixTooLarge
	}
	return value, true, nil
}

// insertAt returns the new hash (and, when unchanged, the location) of the subtree rooted at cur
// (reached at depth) after deciding what (target,key) stores. Path-copy happens as the recursion
// unwinds; a page the transaction held and has now replaced is dropped. Depth is bounded by the hash
// width.
func (t *radixTxn) insertAt(ctx context.Context, cur radixHash, curLoc radixLoc, depth int, target radixHash, key []byte, decide radixDecide) (radixHash, radixLoc, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, radixLoc{}, err
	}
	if depth >= radixHashBits {
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
	node, ownLoc, err := t.r.nodeAt(t.pending, cur, curLoc)
	if err != nil {
		return radixHash{}, radixLoc{}, err
	}
	switch node.kind {
	case radixKindLeaf:
		if !bitsEqual(node.keyHash[:], target[:], 0, depth) {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		if node.keyHash == target {
			if !bytes.Equal(node.key, key) {
				return radixHash{}, radixLoc{}, errRadixCollision // same digest, different key: refuse, never overwrite
			}
			value, write, err := decideChecked(decide, node.value, true)
			if err != nil {
				return radixHash{}, radixLoc{}, err
			}
			if !write {
				return cur, ownLoc, nil // the decision keeps what is stored: the existing page stands
			}
			t.drop(cur)
			h, err := t.writeLeaf(target, key, value) // same key: a new value
			return h, radixLoc{}, err
		}
		// Two distinct keys diverge at the first differing bit at or after depth.
		p := firstDiffBit(node.keyHash[:], target[:], depth)
		if p < depth || p >= radixHashBits {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		value, write, err := decideChecked(decide, nil, false)
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		if !write {
			return cur, ownLoc, nil
		}
		h, err := t.split(depth, p, target, key, value, cur, ownLoc) // the resident leaf is reused unchanged
		return h, radixLoc{}, err
	case radixKindBranch:
		splitBit := depth + node.skipLen
		if splitBit >= radixHashBits {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		diff, matched := comparePrefix(target, depth, node.skipBits, node.skipLen)
		if matched {
			b := bitAt(target[:], splitBit)
			childHash, childLoc := node.child0, node.loc0
			if b == 1 {
				childHash, childLoc = node.child1, node.loc1
			}
			if childHash.isZero() {
				return radixHash{}, radixLoc{}, errRadixUnavailable
			}
			newChild, newLoc, err := t.insertAt(ctx, childHash, childLoc, splitBit+1, target, key, decide)
			if err != nil {
				return radixHash{}, radixLoc{}, err
			}
			if newChild == childHash {
				return cur, ownLoc, nil // nothing below changed; the existing page still stands
			}
			c0, l0, c1, l1 := node.child0, node.loc0, node.child1, node.loc1
			if b == 1 {
				c1, l1 = newChild, newLoc
			} else {
				c0, l0 = newChild, newLoc
			}
			t.drop(cur)
			h, err := t.writeBranch(node.skipLen, node.skipBits, c0, l0, c1, l1)
			return h, radixLoc{}, err
		}
		// The target leaves the branch's common prefix at bit diff. Re-base the existing branch one
		// level deeper (its skip loses the consumed bits) and split above it.
		p := diff
		newSkipLen := splitBit - (p + 1)
		if newSkipLen < 0 {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		value, write, err := decideChecked(decide, nil, false)
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		if !write {
			return cur, ownLoc, nil
		}
		rebased, err := t.writeBranch(newSkipLen, packBits(node.skipBits, (p+1)-depth, newSkipLen), node.child0, node.loc0, node.child1, node.loc1)
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		t.drop(cur)
		h, err := t.split(depth, p, target, key, value, rebased, radixLoc{})
		return h, radixLoc{}, err
	default:
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
}

// radixOp is one key write for radixTxn.merge: the key, its digest under the index's hashKey, and the
// decision update would make for it.
type radixOp struct {
	target radixHash
	key    []byte
	decide radixDecide
}

// errRadixMergeOrder marks merge input that is not strictly ascending by digest: a caller defect, never
// a statement about the tree.
var errRadixMergeOrder = errors.New("delivery radix: merge input is not strictly ordered")

// merge applies ops to root inside the transaction and returns the new root. ops must be sorted by
// target, strictly ascending; two distinct keys with one digest are a collision, as update reports it.
// The result is exactly what applying each op with update returns, in any order: the tree is canonical
// for its key set, and each decision sees only what is stored under its own key
// (TestDeliveryRadix_MergeEqualsSequentialUpdates). What differs is the work. update rewrites every
// page on its key's path, so n keys cost about n times the depth in page writes and reads; merge
// descends once for the whole batch, reads each committed page on the union of the paths once, writes
// each changed page once, and builds the new keys' subtrees bottom-up. Nothing is written to disk
// until commit.
func (t *radixTxn) merge(ctx context.Context, root radixHash, ops []radixOp) (radixHash, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	for i := range ops {
		if len(ops[i].key) == 0 || len(ops[i].key) > radixMaxKeyBytes {
			return radixHash{}, errRadixTooLarge
		}
		if i == 0 {
			continue
		}
		switch c := bytes.Compare(ops[i-1].target[:], ops[i].target[:]); {
		case c == 0 && !bytes.Equal(ops[i-1].key, ops[i].key):
			return radixHash{}, errRadixCollision // same digest, different key: refuse, never overwrite
		case c >= 0:
			return radixHash{}, errRadixMergeOrder
		}
	}
	if len(ops) == 0 {
		return root, nil
	}
	if root.isZero() {
		items, err := t.mergeLeaves(ops, nil, radixNode{})
		if err != nil || len(items) == 0 {
			return root, err
		}
		h, _, err := t.build(0, items)
		return h, err
	}
	h, _, err := t.mergeAt(ctx, root, radixLoc{}, 0, ops)
	return h, err
}

// radixItem is one member of a subtree merge builds: a leaf, named by its full key hash, or an existing
// branch, whose own prefix is bits [0, split) of pat (the rest zero). A branch item keeps its children
// so build can write it at a depth other than its own (re-based deeper when a new key diverges inside
// its prefix), and dirty marks one whose children changed, which must be written even where it stands.
type radixItem struct {
	pat    radixHash
	hash   radixHash // the page as it stands: a leaf, or the branch at its own depth
	loc    radixLoc
	branch bool
	split  int // branch: the bit it splits on
	depth  int // branch: the depth hash was written at
	dirty  bool
	c0, c1 radixHash
	l0, l1 radixLoc
}

// mergeAt returns the new hash (and, when unchanged, the location) of the subtree rooted at cur, reached
// at depth, after applying ops, which are sorted and all share the path's bits [0, depth).
func (t *radixTxn) mergeAt(ctx context.Context, cur radixHash, curLoc radixLoc, depth int, ops []radixOp) (radixHash, radixLoc, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, radixLoc{}, err
	}
	if depth >= radixHashBits {
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
	if t.ra == nil {
		t.ra = &radixReadAhead{windows: map[uint64]radixWindow{}}
	}
	node, ownLoc, err := t.r.nodeAtRA(t.pending, t.ra, cur, curLoc)
	if err != nil {
		return radixHash{}, radixLoc{}, err
	}
	switch node.kind {
	case radixKindLeaf:
		// The leaf's hash must agree with the bits the path already committed to (update's check).
		if !bitsEqual(node.keyHash[:], ops[0].target[:], 0, depth) {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		resident := radixItem{pat: node.keyHash, hash: cur, loc: ownLoc}
		items, err := t.mergeLeaves(ops, &resident, node)
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		if len(items) == 1 && items[0].hash == cur {
			return cur, ownLoc, nil // every decision kept what is stored: the existing page stands
		}
		return t.build(depth, items)
	case radixKindBranch:
		splitBit := depth + node.skipLen
		if splitBit >= radixHashBits {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		// The ops are sorted, so the ones inside this branch's prefix are one run; the ones before it and
		// after it diverge inside the prefix (with a 0 where the prefix has a 1, or the reverse) and are
		// new keys beside the branch.
		side := func(op radixOp) int {
			d, matched := comparePrefix(op.target, depth, node.skipBits, node.skipLen)
			switch {
			case matched:
				return 0
			case bitAt(op.target[:], d) == 0:
				return -1
			default:
				return 1
			}
		}
		lo := 0
		for lo < len(ops) && side(ops[lo]) < 0 {
			lo++
		}
		hi := lo
		for hi < len(ops) && side(ops[hi]) == 0 {
			hi++
		}
		for _, op := range ops[hi:] {
			if side(op) != 1 {
				return radixHash{}, radixLoc{}, errRadixMergeOrder
			}
		}
		inside := ops[lo:hi]
		mid := sortSearchBit(inside, splitBit)
		c0, l0, c1, l1 := node.child0, node.loc0, node.child1, node.loc1
		if len(inside[:mid]) > 0 {
			if c0.isZero() {
				return radixHash{}, radixLoc{}, errRadixUnavailable
			}
			if c0, l0, err = t.mergeAt(ctx, c0, l0, splitBit+1, inside[:mid]); err != nil {
				return radixHash{}, radixLoc{}, err
			}
		}
		if len(inside[mid:]) > 0 {
			if c1.isZero() {
				return radixHash{}, radixLoc{}, errRadixUnavailable
			}
			if c1, l1, err = t.mergeAt(ctx, c1, l1, splitBit+1, inside[mid:]); err != nil {
				return radixHash{}, radixLoc{}, err
			}
		}
		var pat radixHash
		for i := 0; i < depth; i++ {
			if bitAt(ops[0].target[:], i) == 1 {
				pat[i>>3] |= 1 << (7 - uint(i&7))
			}
		}
		for j := 0; j < node.skipLen; j++ {
			if bitAt(node.skipBits, j) == 1 {
				pat[(depth+j)>>3] |= 1 << (7 - uint((depth+j)&7))
			}
		}
		self := radixItem{
			pat: pat, hash: cur, loc: ownLoc, branch: true, split: splitBit, depth: depth,
			dirty: c0 != node.child0 || c1 != node.child1, c0: c0, l0: l0, c1: c1, l1: l1,
		}
		below, err := t.mergeLeaves(ops[:lo], nil, radixNode{})
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		above, err := t.mergeLeaves(ops[hi:], nil, radixNode{})
		if err != nil {
			return radixHash{}, radixLoc{}, err
		}
		if !self.dirty && len(below) == 0 && len(above) == 0 {
			return cur, ownLoc, nil // nothing below changed and no key was added beside it
		}
		items := make([]radixItem, 0, len(below)+1+len(above))
		items = append(append(append(items, below...), self), above...)
		return t.build(depth, items)
	default:
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
}

// sortSearchBit returns the index of the first op whose bit i is 1: ops sharing every bit before i are
// sorted by it, so the ones with 0 form a prefix.
func sortSearchBit(ops []radixOp, i int) int {
	lo, hi := 0, len(ops)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if bitAt(ops[m].target[:], i) == 1 {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return lo
}

// mergeLeaves decides each op against what is stored under its key — the resident leaf (node) when
// resident is non-nil and the op names its key, nothing otherwise — and returns the leaves the subtree
// then holds, sorted: the resident (kept, or replaced by a new value) and every new leaf written.
func (t *radixTxn) mergeLeaves(ops []radixOp, resident *radixItem, node radixNode) ([]radixItem, error) {
	var (
		items  []radixItem
		placed bool
	)
	for _, op := range ops {
		if resident != nil && !placed && bytes.Compare(resident.pat[:], op.target[:]) < 0 {
			items = append(items, *resident)
			placed = true
		}
		if resident != nil && op.target == resident.pat {
			placed = true
			if !bytes.Equal(node.key, op.key) {
				return nil, errRadixCollision // same digest, different key: refuse, never overwrite
			}
			value, write, err := decideChecked(op.decide, node.value, true)
			if err != nil {
				return nil, err
			}
			if !write {
				items = append(items, *resident)
				continue
			}
			t.drop(resident.hash)
			h, err := t.writeLeaf(op.target, op.key, value) // same key: a new value
			if err != nil {
				return nil, err
			}
			items = append(items, radixItem{pat: op.target, hash: h})
			continue
		}
		value, write, err := decideChecked(op.decide, nil, false)
		if err != nil {
			return nil, err
		}
		if !write {
			continue
		}
		h, err := t.writeLeaf(op.target, op.key, value)
		if err != nil {
			return nil, err
		}
		items = append(items, radixItem{pat: op.target, hash: h})
	}
	if resident != nil && !placed {
		items = append(items, *resident)
	}
	return items, nil
}

// build writes the canonical subtree at depth over items, which are sorted and pairwise diverge at or
// after depth (a branch item's whole prefix is shared by nothing else in the set): a branch at the first
// bit where the items differ, over the subtrees of the two halves. A lone leaf is itself; a lone branch
// item is itself at its own depth unless its children changed, and is otherwise re-based with the bits
// above depth dropped from its prefix, exactly as update re-bases it.
func (t *radixTxn) build(depth int, items []radixItem) (radixHash, radixLoc, error) {
	if len(items) == 0 {
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
	if len(items) == 1 {
		it := items[0]
		if !it.branch {
			return it.hash, it.loc, nil
		}
		if !it.dirty && it.depth == depth {
			return it.hash, it.loc, nil
		}
		skipLen := it.split - depth
		if skipLen < 0 {
			return radixHash{}, radixLoc{}, errRadixUnavailable
		}
		t.drop(it.hash)
		h, err := t.writeBranch(skipLen, packBits(it.pat[:], depth, skipLen), it.c0, it.l0, it.c1, it.l1)
		return h, radixLoc{}, err
	}
	first := items[0]
	p := firstDiffBit(first.pat[:], items[len(items)-1].pat[:], depth)
	if p >= radixHashBits {
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
	k := 0
	for k < len(items) && bitAt(items[k].pat[:], p) == 0 {
		k++
	}
	if k == 0 || k == len(items) {
		return radixHash{}, radixLoc{}, errRadixUnavailable
	}
	h0, l0, err := t.build(p+1, items[:k])
	if err != nil {
		return radixHash{}, radixLoc{}, err
	}
	h1, l1, err := t.build(p+1, items[k:])
	if err != nil {
		return radixHash{}, radixLoc{}, err
	}
	h, err := t.writeBranch(p-depth, packBits(first.pat[:], depth, p-depth), h0, l0, h1, l1)
	return h, radixLoc{}, err
}

// split writes a new branch at depth that separates a new leaf (for target,key,value) from an
// existing subtree other at bit p. The common bits [depth,p) come from the target (equal to other's on
// those bits by construction). other is a leaf reused as-is, or a re-based branch.
func (t *radixTxn) split(depth, p int, target radixHash, key, value []byte, other radixHash, otherLoc radixLoc) (radixHash, error) {
	leaf, err := t.writeLeaf(target, key, value)
	if err != nil {
		return radixHash{}, err
	}
	skipLen := p - depth
	c0, l0, c1, l1 := leaf, radixLoc{}, other, otherLoc
	if bitAt(target[:], p) == 1 {
		c0, l0, c1, l1 = other, otherLoc, leaf, radixLoc{}
	}
	return t.writeBranch(skipLen, packBits(target[:], depth, skipLen), c0, l0, c1, l1)
}

// drop forgets a page the transaction held once a path copy has replaced it. A page appears at most
// once in a tree (a leaf names its key; a branch names its two subtrees, which partition the keys), so
// the replaced page is unreachable from the new root. A committed page is never dropped.
func (t *radixTxn) drop(h radixHash) {
	delete(t.pending, h)
}

func (t *radixTxn) writeLeaf(keyHash radixHash, key, value []byte) (radixHash, error) {
	raw := encodeRadixLeaf(keyHash, key, value)
	node := radixNode{kind: radixKindLeaf, keyHash: keyHash, key: append([]byte(nil), key...), value: append([]byte(nil), value...)}
	return t.hold(raw, node)
}

func (t *radixTxn) writeBranch(skipLen int, skipBits []byte, c0 radixHash, l0 radixLoc, c1 radixHash, l1 radixLoc) (radixHash, error) {
	if skipLen < 0 || skipLen > radixHashBits || c0.isZero() || c1.isZero() {
		return radixHash{}, errRadixUnavailable
	}
	raw := encodeRadixBranch(skipLen, skipBits, c0, c1)
	node, err := decodeRadixPage(raw) // the canonical skip bytes, exactly as a reader will decode them
	if err != nil {
		return radixHash{}, err
	}
	node.loc0, node.loc1 = l0, l1
	return t.hold(raw, node)
}

// hold records a new page in the transaction and returns its content hash.
func (t *radixTxn) hold(raw []byte, node radixNode) (radixHash, error) {
	if len(raw) > radixMaxPageBytes {
		return radixHash{}, errRadixTooLarge
	}
	h := radixDigest(radixPageDomain, raw)
	t.pending[h] = &node
	return h, nil
}

// commit makes root durable. Every page the transaction holds that root reaches is appended, children
// before parents, to ONE new pack (one sequential write, one fsync), the pack is published by an atomic
// create-new link, and a root pointer for root is published the same way. A root the transaction does
// not hold is already committed (or empty) and needs nothing. On success the transaction is spent.
func (t *radixTxn) commit(ctx context.Context, root radixHash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root.isZero() {
		return nil
	}
	if _, held := t.pending[root]; !held {
		return nil
	}
	rootLoc, published, err := t.writePack(root)
	if err != nil {
		return err
	}
	if err := t.r.publishRoot(root, rootLoc); err != nil {
		return err
	}
	// The pages are durable and the root is published: the branches just written are the newest top
	// of the tree, the pages the next lookups and the next transaction read first.
	for _, c := range published {
		t.r.remember(c.hash, c.node, c.loc)
	}
	t.pending = map[radixHash]*radixNode{}
	t.ra = nil
	return nil
}

// radixPublished is one page a commit wrote, with the location it was written at.
type radixPublished struct {
	hash radixHash
	node radixNode
	loc  radixLoc
}

// writePack streams the held pages root reaches into a staged pack and publishes it. It returns the
// root record's location and the branch pages it wrote, with their locations.
func (t *radixTxn) writePack(root radixHash) (radixLoc, []radixPublished, error) {
	r := t.r
	if err := r.ensurePacksDir(); err != nil {
		return radixLoc{}, nil, err
	}
	id, err := radixPackID()
	if err != nil {
		return radixLoc{}, nil, err
	}
	suffix, err := radixTempSuffix()
	if err != nil {
		return radixLoc{}, nil, err
	}
	tmp := filepath.Join(radixPacksDir, radixTempPrefix+suffix)
	f, err := r.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return radixLoc{}, nil, err
	}
	discard := func(cause error) (radixLoc, []radixPublished, error) {
		_ = f.Close()
		_ = r.root.Remove(tmp)
		return radixLoc{}, nil, cause
	}
	var published []radixPublished
	w := bufio.NewWriterSize(f, radixPackWriteBuffer)
	if _, err := w.WriteString(radixPackMagic); err != nil {
		return discard(err)
	}
	offset := uint64(radixPackHeaderLen)
	var emit func(h radixHash) (radixLoc, error)
	emit = func(h radixHash) (radixLoc, error) {
		node := t.pending[h]
		if node == nil {
			return radixLoc{}, errRadixUnavailable // a held page names a child nobody holds or committed
		}
		if node.kind == radixKindBranch {
			if !node.loc0.valid() {
				loc, err := emit(node.child0)
				if err != nil {
					return radixLoc{}, err
				}
				node.loc0 = loc
			}
			if !node.loc1.valid() {
				loc, err := emit(node.child1)
				if err != nil {
					return radixLoc{}, err
				}
				node.loc1 = loc
			}
		}
		// The page bytes are re-encoded from the held node (the encoding is deterministic) and checked
		// against the name the tree already gave them, so a page is never written under a wrong name.
		raw := encodeRadixNode(*node)
		if radixDigest(radixPageDomain, raw) != h {
			return radixLoc{}, errRadixUnavailable
		}
		rec := encodeRadixRecord(raw, *node)
		loc := radixLoc{pack: id, off: offset}
		if _, err := w.Write(rec); err != nil {
			return radixLoc{}, err
		}
		offset += uint64(len(rec))
		if node.kind == radixKindBranch {
			published = append(published, radixPublished{hash: h, node: *node, loc: loc})
		}
		return loc, nil
	}
	rootLoc, err := emit(root)
	if err != nil {
		return discard(err)
	}
	if err := w.Flush(); err != nil {
		return discard(err)
	}
	if err := f.Sync(); err != nil {
		return discard(err)
	}
	if err := f.Close(); err != nil {
		_ = r.root.Remove(tmp)
		return radixLoc{}, nil, err
	}
	name := filepath.Join(radixPacksDir, radixPackName(id))
	// Link claims an absent name atomically; a pack is never replaced once published.
	if err := r.root.Link(tmp, name); err != nil {
		_ = r.root.Remove(tmp)
		return radixLoc{}, nil, err
	}
	if err := r.root.Remove(tmp); err != nil {
		return radixLoc{}, nil, err
	}
	if err := paths.SyncDir(filepath.Join(r.dir, radixPacksDir)); err != nil {
		return radixLoc{}, nil, err
	}
	return rootLoc, published, nil
}

// encodeRadixRecord is one pack record: a big-endian length, the page bytes, and for a branch the two
// child locations. The locations are outside the page hash; readRecord checks what they point at.
func encodeRadixRecord(raw []byte, node radixNode) []byte {
	n := radixRecordHeaderLen + len(raw)
	if node.kind == radixKindBranch {
		n += radixTrailerLen
	}
	rec := make([]byte, 0, n)
	rec = binary.BigEndian.AppendUint32(rec, uint32(len(raw)))
	rec = append(rec, raw...)
	if node.kind == radixKindBranch {
		rec = binary.BigEndian.AppendUint64(rec, node.loc0.pack)
		rec = binary.BigEndian.AppendUint64(rec, node.loc0.off)
		rec = binary.BigEndian.AppendUint64(rec, node.loc1.pack)
		rec = binary.BigEndian.AppendUint64(rec, node.loc1.off)
	}
	return rec
}

// publishRoot writes root's pointer file. An existing pointer for the same root (an earlier identical
// tree, or a crashed attempt whose pack was already published) is kept when it resolves to a record
// with exactly that hash, and refused — preserved, never overwritten — when it does not.
func (r *deliveryRadix) publishRoot(root radixHash, loc radixLoc) error {
	shard := r.shard(root)
	created, err := r.ensureShard(shard)
	if err != nil {
		return err
	}
	if created {
		if err := paths.SyncDir(r.dir); err != nil {
			return err
		}
	}
	name := r.rootName(root)
	if _, statErr := r.root.Lstat(name); statErr == nil {
		if _, _, err := r.readRoot(root); err != nil {
			return errRadixUnavailable // a conflicting pointer at this address: preserve, refuse
		}
		return nil
	} else if !os.IsNotExist(statErr) {
		return errRadixUnavailable
	}
	data := make([]byte, 0, radixRootFileLen)
	data = append(data, radixRootMagic...)
	data = binary.BigEndian.AppendUint64(data, loc.pack)
	data = binary.BigEndian.AppendUint64(data, loc.off)
	suffix, err := radixTempSuffix()
	if err != nil {
		return err
	}
	if err := r.stageAndRename(filepath.Join(shard, radixTempPrefix+suffix), name, data); err != nil {
		// A concurrent writer may have landed the identical pointer; accept it after verifying.
		if _, _, rerr := r.readRoot(root); rerr == nil {
			return nil
		}
		return err
	}
	return paths.SyncDir(filepath.Join(r.dir, shard))
}

// stageAndRename stages raw in a create-new temporary file, fsyncs it, and publishes it at name by an
// atomic link that refuses an existing destination, so a conflicting file that appeared after an
// earlier existence check is preserved rather than replaced.
func (r *deliveryRadix) stageAndRename(tmp, name string, raw []byte) error {
	f, err := r.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if n, err := f.Write(raw); err != nil || n != len(raw) {
		_ = f.Close()
		_ = r.root.Remove(tmp)
		if err == nil {
			return io.ErrShortWrite
		}
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = r.root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = r.root.Remove(tmp)
		return err
	}
	// Publication must not replace a conflicting file that appeared after the
	// earlier existence check. Link atomically claims an absent address;
	// unsupported filesystems refuse, preserving the staged bytes for diagnosis.
	if err := r.root.Link(tmp, name); err != nil {
		return err
	}
	return r.root.Remove(tmp)
}

func (r *deliveryRadix) ensureShard(shard string) (bool, error) {
	if info, err := r.root.Lstat(shard); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, errRadixUnavailable
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := r.root.Mkdir(shard, 0o700); err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ensurePacksDir creates the pack directory on first use (a real directory, never an alias).
func (r *deliveryRadix) ensurePacksDir() error {
	created, err := r.ensureShard(radixPacksDir)
	if err != nil {
		return err
	}
	if created {
		return paths.SyncDir(r.dir)
	}
	return nil
}

// ── reads ────────────────────────────────────────────────────────────────────────────────────────

// readNode reads the committed root page h through its root pointer.
func (r *deliveryRadix) readNode(h radixHash) (radixNode, error) {
	n, _, err := r.readRoot(h)
	return n, err
}

// readPage reads the committed root page h through its root pointer and returns its bytes. A missing,
// irregular, oversize or mismatched pointer or record is unavailable — never an empty tree.
func (r *deliveryRadix) readPage(h radixHash) ([]byte, error) {
	loc, err := r.readRootPointer(h)
	if err != nil {
		return nil, err
	}
	raw, _, err := r.readRecordBytes(h, loc)
	return raw, err
}

// readRoot resolves a committed root page by its hash: the root pointer names its record's location,
// and the record must hash to exactly h.
func (r *deliveryRadix) readRoot(h radixHash) (radixNode, radixLoc, error) {
	loc, err := r.readRootPointer(h)
	if err != nil {
		return radixNode{}, radixLoc{}, err
	}
	n, err := r.readRecord(h, loc)
	return n, loc, err
}

// readRootPointer reads h's root pointer file: a regular file of exactly radixRootFileLen bytes with
// this layout's magic, read through the confined root with the opened identity checked.
func (r *deliveryRadix) readRootPointer(h radixHash) (radixLoc, error) {
	shard, err := r.root.Lstat(r.shard(h))
	if err != nil || !shard.IsDir() || shard.Mode()&os.ModeSymlink != 0 {
		return radixLoc{}, errRadixUnavailable
	}
	name := r.rootName(h)
	before, err := r.root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() != int64(radixRootFileLen) {
		return radixLoc{}, errRadixUnavailable
	}
	f, err := r.root.Open(name)
	if err != nil {
		return radixLoc{}, errRadixUnavailable
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != int64(radixRootFileLen) {
		return radixLoc{}, errRadixUnavailable
	}
	raw := make([]byte, radixRootFileLen)
	if _, err := io.ReadFull(f, raw); err != nil || string(raw[:len(radixRootMagic)]) != radixRootMagic {
		return radixLoc{}, errRadixUnavailable
	}
	loc := radixLoc{
		pack: binary.BigEndian.Uint64(raw[len(radixRootMagic):]),
		off:  binary.BigEndian.Uint64(raw[len(radixRootMagic)+8:]),
	}
	if !loc.valid() {
		return radixLoc{}, errRadixUnavailable
	}
	return loc, nil
}

// readRecord reads the record at loc and decodes it, requiring its page bytes to hash to exactly h. A
// branch's child locations come from the record's trailer and are themselves checked when followed.
func (r *deliveryRadix) readRecord(h radixHash, loc radixLoc) (radixNode, error) {
	return r.readRecordRA(h, loc, nil)
}

// readRecordRA is readRecord through a transaction's read-ahead windows (ra may be nil).
func (r *deliveryRadix) readRecordRA(h radixHash, loc radixLoc, ra *radixReadAhead) (radixNode, error) {
	raw, trailer, err := r.readRecordBytesRA(h, loc, ra)
	if err != nil {
		return radixNode{}, err
	}
	node, err := decodeRadixPage(raw)
	if err != nil {
		return radixNode{}, err
	}
	if node.kind == radixKindBranch {
		if len(trailer) < radixTrailerLen {
			return radixNode{}, errRadixUnavailable
		}
		node.loc0 = radixLoc{pack: binary.BigEndian.Uint64(trailer[0:]), off: binary.BigEndian.Uint64(trailer[8:])}
		node.loc1 = radixLoc{pack: binary.BigEndian.Uint64(trailer[16:]), off: binary.BigEndian.Uint64(trailer[24:])}
		if !node.loc0.valid() || !node.loc1.valid() {
			return radixNode{}, errRadixUnavailable
		}
	}
	return node, nil
}

// radixReadAhead holds, for one write transaction, the last window read from each pack. merge reads
// committed pages in key order, and writePack lays a subtree out left half first, so a pack holds its
// pages in key order: most of merge's reads land in the window its previous miss fetched from the same
// pack, and a window-sized positioned read replaces hundreds of record-sized ones. A window is never
// shared or written to (packs are immutable once published), every record served from one is copied
// out and checked against the hash its parent names exactly as a direct read is, and a record the
// window does not hold whole is read directly.
type radixReadAhead struct {
	windows map[uint64]radixWindow
}

type radixWindow struct {
	off int64
	buf []byte
}

const (
	// radixReadAheadBytes is one read-ahead window.
	radixReadAheadBytes = 16 << 10
	// radixReadAheadPacks bounds the windows one transaction holds (one per pack).
	radixReadAheadPacks = radixMaxOpenPacks
)

// readRecordBytesRA is readRecordBytes served from ra's window for loc's pack when it holds the whole
// record, refilling that window from loc on a miss.
func (r *deliveryRadix) readRecordBytesRA(h radixHash, loc radixLoc, ra *radixReadAhead) ([]byte, []byte, error) {
	if ra == nil || !loc.valid() || loc.off > math.MaxInt64 {
		return r.readRecordBytes(h, loc)
	}
	off := int64(loc.off)
	w, ok := ra.windows[loc.pack]
	if !ok || off < w.off || off-w.off > int64(len(w.buf))-int64(radixRecordHeaderLen) {
		ph, err := r.acquirePack(loc.pack)
		if err != nil {
			return nil, nil, err
		}
		if off > ph.size || ph.size-off < int64(radixRecordHeaderLen) {
			r.releasePack(ph)
			return nil, nil, errRadixUnavailable
		}
		n := min(int64(radixReadAheadBytes), ph.size-off)
		buf := make([]byte, n)
		_, rerr := ph.f.ReadAt(buf, off)
		r.releasePack(ph)
		if rerr != nil {
			return nil, nil, errRadixUnavailable
		}
		if _, held := ra.windows[loc.pack]; !held && len(ra.windows) >= radixReadAheadPacks {
			for id := range ra.windows {
				delete(ra.windows, id)
				break
			}
		}
		w = radixWindow{off: off, buf: buf}
		ra.windows[loc.pack] = w
	}
	rest := w.buf[off-w.off:]
	n := int(binary.BigEndian.Uint32(rest[:radixRecordHeaderLen]))
	want := radixRecordHeaderLen + n + radixTrailerLen
	if n < 6 || n > radixMaxPageBytes || want > len(rest) {
		return r.readRecordBytes(h, loc) // not whole in the window (or not a record): read it directly
	}
	rec := append([]byte(nil), rest[radixRecordHeaderLen:want]...)
	raw := rec[:n]
	if radixDigest(radixPageDomain, raw) != h {
		return nil, nil, errRadixUnavailable // corrupt or tampered content, or a wrong location
	}
	return raw, rec[n:], nil
}

// readRecordBytes returns the page bytes at loc (verified against h) and up to radixTrailerLen bytes
// that follow them (a branch's trailer; for a leaf, whatever comes next, which the caller ignores).
func (r *deliveryRadix) readRecordBytes(h radixHash, loc radixLoc) ([]byte, []byte, error) {
	if !loc.valid() || loc.off > math.MaxInt64 {
		return nil, nil, errRadixUnavailable
	}
	ph, err := r.acquirePack(loc.pack)
	if err != nil {
		return nil, nil, err
	}
	defer r.releasePack(ph)
	off := int64(loc.off)
	if off > ph.size || ph.size-off < int64(radixRecordHeaderLen) {
		return nil, nil, errRadixUnavailable
	}
	// One positioned read covers the header, the page and a branch's trailer for almost every record
	// (radixRecordReadAhead); only a larger record needs a second read for the rest.
	avail := ph.size - off
	first := int64(radixRecordReadAhead)
	if first > avail {
		first = avail
	}
	buf := make([]byte, first)
	if _, err := ph.f.ReadAt(buf, off); err != nil {
		return nil, nil, errRadixUnavailable
	}
	n := int64(binary.BigEndian.Uint32(buf[:radixRecordHeaderLen]))
	body := avail - int64(radixRecordHeaderLen)
	if n < 6 || n > int64(radixMaxPageBytes) || n > body {
		return nil, nil, errRadixUnavailable
	}
	want := int64(radixRecordHeaderLen) + n + int64(radixTrailerLen)
	if want > avail {
		want = avail
	}
	if want > first {
		more := make([]byte, want)
		copy(more, buf)
		if _, err := ph.f.ReadAt(more[first:], off+first); err != nil {
			return nil, nil, errRadixUnavailable // a truncated pack included
		}
		buf = more
	}
	buf = buf[radixRecordHeaderLen:want]
	raw := buf[:n]
	if radixDigest(radixPageDomain, raw) != h {
		return nil, nil, errRadixUnavailable // corrupt or tampered content, or a wrong location
	}
	return raw, buf[n:], nil
}

// acquirePack returns an open handle on pack id, from the cache or freshly opened through the confined
// root (a regular file, identity checked, carrying this layout's header). The caller releases it.
func (r *deliveryRadix) acquirePack(id uint64) (*radixPackHandle, error) {
	r.packMu.Lock()
	if ph, ok := r.packs[id]; ok {
		ph.refs++
		r.packTick++
		ph.used = r.packTick
		r.packMu.Unlock()
		return ph, nil
	}
	r.packMu.Unlock()

	name := filepath.Join(radixPacksDir, radixPackName(id))
	before, err := r.root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() < int64(radixPackHeaderLen) {
		return nil, errRadixUnavailable
	}
	f, err := r.root.Open(name)
	if err != nil {
		return nil, errRadixUnavailable
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != before.Size() {
		_ = f.Close()
		return nil, errRadixUnavailable
	}
	var magic [radixPackHeaderLen]byte
	if _, err := f.ReadAt(magic[:], 0); err != nil || string(magic[:]) != radixPackMagic {
		_ = f.Close()
		return nil, errRadixUnavailable
	}

	r.packMu.Lock()
	defer r.packMu.Unlock()
	if ph, ok := r.packs[id]; ok { // another reader opened it meanwhile
		_ = f.Close()
		ph.refs++
		r.packTick++
		ph.used = r.packTick
		return ph, nil
	}
	if r.packs == nil {
		r.packs = map[uint64]*radixPackHandle{}
	}
	r.packTick++
	ph := &radixPackHandle{f: f, size: info.Size(), refs: 1, used: r.packTick}
	r.packs[id] = ph
	r.evictPacksLocked()
	return ph, nil
}

// releasePack drops one reference; the cache may then close the handle when it is over its bound.
func (r *deliveryRadix) releasePack(ph *radixPackHandle) {
	r.packMu.Lock()
	defer r.packMu.Unlock()
	ph.refs--
	r.evictPacksLocked()
}

// evictPacksLocked closes the least recently used idle handles while the cache is over its bound. A
// handle a read still holds is never closed under it.
func (r *deliveryRadix) evictPacksLocked() {
	for len(r.packs) > radixMaxOpenPacks {
		var victim uint64
		var oldest *radixPackHandle
		for id, ph := range r.packs {
			if ph.refs == 0 && (oldest == nil || ph.used < oldest.used) {
				victim, oldest = id, ph
			}
		}
		if oldest == nil {
			return // every handle is in use; the bound is restored as reads finish
		}
		_ = oldest.f.Close()
		delete(r.packs, victim)
	}
}

func (r *deliveryRadix) shard(h radixHash) string { return hex.EncodeToString(h[:1]) }
func (r *deliveryRadix) rootName(h radixHash) string {
	return filepath.Join(r.shard(h), hex.EncodeToString(h[:])+radixRootSuffix)
}

// pagePath is the absolute on-disk path of a committed ROOT's pointer file, for in-package tests that
// inject faults at the address a root is resolved through.
func (r *deliveryRadix) pagePath(h radixHash) string { return filepath.Join(r.dir, r.rootName(h)) }

// packPath is the absolute on-disk path of pack id, for in-package tests that inject faults.
func (r *deliveryRadix) packPath(id uint64) string {
	return filepath.Join(r.dir, radixPacksDir, radixPackName(id))
}

func radixPackName(id uint64) string { return fmt.Sprintf("%016x%s", id, radixPackSuffix) }

// radixPackID draws a fresh, non-zero pack id. A pack is published by a create-new link, so an id that
// is somehow taken refuses the commit rather than replacing anything.
func radixPackID() (uint64, error) {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if id := binary.BigEndian.Uint64(b[:]); id != 0 {
			return id, nil
		}
	}
}

// ── deterministic, versioned page encoding ───────────────────────────────────────────────────────

// encodeRadixNode re-encodes a decoded page exactly as it was first encoded.
func encodeRadixNode(n radixNode) []byte {
	if n.kind == radixKindBranch {
		return encodeRadixBranch(n.skipLen, n.skipBits, n.child0, n.child1)
	}
	return encodeRadixLeaf(n.keyHash, n.key, n.value)
}

func encodeRadixLeaf(keyHash radixHash, key, value []byte) []byte {
	buf := make([]byte, 0, 6+radixHashBytes+2*binary.MaxVarintLen64+len(key)+len(value))
	buf = append(buf, radixMagic...)
	buf = append(buf, radixVersion, radixKindLeaf)
	buf = append(buf, keyHash[:]...)
	var tmp [binary.MaxVarintLen64]byte
	buf = append(buf, tmp[:binary.PutUvarint(tmp[:], uint64(len(key)))]...)
	buf = append(buf, key...)
	buf = append(buf, tmp[:binary.PutUvarint(tmp[:], uint64(len(value)))]...)
	buf = append(buf, value...)
	return buf
}

func encodeRadixBranch(skipLen int, skipBits []byte, c0, c1 radixHash) []byte {
	nb := (skipLen + 7) / 8
	buf := make([]byte, 0, 6+2+nb+2*radixHashBytes)
	buf = append(buf, radixMagic...)
	buf = append(buf, radixVersion, radixKindBranch)
	var sl [2]byte
	binary.BigEndian.PutUint16(sl[:], uint16(skipLen)) //nolint:gosec // skipLen <= 256, checked by caller
	buf = append(buf, sl[:]...)
	sb := make([]byte, nb)
	copy(sb, skipBits) // caller supplies exactly nb bytes with trailing bits already zero
	buf = append(buf, sb...)
	buf = append(buf, c0[:]...)
	buf = append(buf, c1[:]...)
	return buf
}

func decodeRadixPage(raw []byte) (radixNode, error) {
	var n radixNode
	if len(raw) < 6 || string(raw[0:4]) != radixMagic {
		return n, errRadixUnavailable
	}
	if raw[4] != radixVersion {
		return n, errRadixUnavailable // an unknown version is unavailable, never guessed at
	}
	n.kind = raw[5]
	p := 6
	switch n.kind {
	case radixKindLeaf:
		if len(raw) < p+radixHashBytes {
			return n, errRadixUnavailable
		}
		copy(n.keyHash[:], raw[p:p+radixHashBytes])
		p += radixHashBytes
		klen, k := binary.Uvarint(raw[p:])
		if k <= 0 || klen > uint64(radixMaxKeyBytes) {
			return n, errRadixUnavailable
		}
		p += k
		if uint64(len(raw)-p) < klen {
			return n, errRadixUnavailable
		}
		n.key = append([]byte(nil), raw[p:p+int(klen)]...)
		p += int(klen)
		vlen, k2 := binary.Uvarint(raw[p:])
		if k2 <= 0 || vlen > uint64(radixMaxValueBytes) {
			return n, errRadixUnavailable
		}
		p += k2
		if uint64(len(raw)-p) != vlen { // exact consumption: no trailing bytes
			return n, errRadixUnavailable
		}
		n.value = append([]byte(nil), raw[p:]...)
	case radixKindBranch:
		if len(raw) < p+2 {
			return n, errRadixUnavailable
		}
		n.skipLen = int(binary.BigEndian.Uint16(raw[p : p+2]))
		p += 2
		if n.skipLen > radixHashBits {
			return n, errRadixUnavailable
		}
		nb := (n.skipLen + 7) / 8
		if len(raw) != p+nb+2*radixHashBytes { // exact size for a branch
			return n, errRadixUnavailable
		}
		n.skipBits = append([]byte(nil), raw[p:p+nb]...)
		p += nb
		if n.skipLen%8 != 0 { // canonical: unused low bits of the last skip byte are zero
			if n.skipBits[nb-1]&(0xFF>>uint(n.skipLen%8)) != 0 {
				return n, errRadixUnavailable
			}
		}
		copy(n.child0[:], raw[p:p+radixHashBytes])
		p += radixHashBytes
		copy(n.child1[:], raw[p:p+radixHashBytes])
		if n.child0.isZero() || n.child1.isZero() { // both children always present
			return n, errRadixUnavailable
		}
	default:
		return n, errRadixUnavailable
	}
	return n, nil
}

// ── bit and hash helpers ─────────────────────────────────────────────────────────────────────────

func radixDigest(domain string, b []byte) radixHash {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(b)
	var out radixHash
	h.Sum(out[:0])
	return out
}

// bitAt returns bit i of b, MSB-first (bit 0 is the top bit of byte 0).
func bitAt(b []byte, i int) int { return int((b[i>>3] >> (7 - uint(i&7))) & 1) }

// bitsEqual reports whether a and b agree on bits [from,to).
func bitsEqual(a, b []byte, from, to int) bool {
	for i := from; i < to; i++ {
		if bitAt(a, i) != bitAt(b, i) {
			return false
		}
	}
	return true
}

// firstDiffBit returns the first bit index at or after from where a and b differ, or radixHashBits.
func firstDiffBit(a, b []byte, from int) int {
	for i := from; i < radixHashBits; i++ {
		if bitAt(a, i) != bitAt(b, i) {
			return i
		}
	}
	return radixHashBits
}

// packBits copies bits [from,from+length) of src into a fresh, MSB-first, byte-packed slice whose
// unused trailing bits are zero.
func packBits(src []byte, from, length int) []byte {
	out := make([]byte, (length+7)/8)
	for j := 0; j < length; j++ {
		if bitAt(src, from+j) == 1 {
			out[j>>3] |= 1 << (7 - uint(j&7))
		}
	}
	return out
}

// comparePrefix compares target's bits [depth,depth+skipLen) against skipBits. On a match it returns
// (depth+skipLen, true); on a mismatch it returns the diverging bit index and false.
func comparePrefix(target radixHash, depth int, skipBits []byte, skipLen int) (int, bool) {
	for j := 0; j < skipLen; j++ {
		if bitAt(target[:], depth+j) != bitAt(skipBits, j) {
			return depth + j, false
		}
	}
	return depth + skipLen, true
}

func radixTempSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
