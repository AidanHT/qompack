package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
)

// deliveryRadix is a small, exact, immutable on-disk Merkle radix (binary Patricia) index over the
// 256-bit hash of an opaque key, for the delivery journal's lease/ACK/session keys. It is a PRIMITIVE:
// it stores and retrieves (key → value) with sound proof of absence, path-copy persistence and
// content-addressed page files. It does NOT interpret keys, own the "current root", validate
// lease/ACK semantics, close the journal's capacity bound, or enable any migration — the parent (the
// journal owner) does all of that. This file has no dependency beyond the standard library and
// internal/paths (long-path and directory-fsync helpers already used by the store's os.Root code).
//
// # Shape
//
// The tree is a binary Patricia trie over the bits of H(domain‖key) (MSB first). Path compression
// keeps a branch node's common bits as a "skip" so a leaf costs O(distinct-prefix) pages, never 256.
// Two page kinds, each an immutable content-addressed file:
//
//   - LEAF: the full 32-byte key hash, the ORIGINAL key bytes (compared at the leaf so a hash
//     collision can never be mistaken for a match or silently overwrite another key), and the value.
//   - BRANCH: skipLen + skipBits (the common prefix bits below this node) and two non-zero child
//     hashes; it splits on the bit at depth+skipLen. Both children are always present (the Patricia
//     invariant: a one-child branch is always collapsed).
//
// A page's content hash is its filename, so a corrupt, truncated or tampered page cannot be read as
// anything but itself: readPage recomputes the hash and refuses a mismatch. The ROOT of a tree is the
// content hash of its root page; the zero hash denotes the empty tree.
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
// A missing, corrupt, truncated or unknown-version page under a NON-ZERO root is `errRadixUnavailable`
// (an error), never (nil,false,nil). A genuine absence proven by a complete traversal is
// (nil,false,nil). A hash collision is `errRadixCollision` — an unavailable-class error, never an
// overwrite and never an absence. Oversize input is `errRadixTooLarge`. All three are distinct so the
// parent can map them; none is ever silently downgraded to absence.

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

	radixPageSuffix = ".page"
	radixTempPrefix = ".tmp-"
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

func (r *deliveryRadix) close() error { return r.root.Close() }

func (r *deliveryRadix) defaultKeyHash(key []byte) radixHash {
	return radixDigest(radixKeyDomain, key)
}

// lookup returns the value stored under key in the tree rooted at root. found is false with a nil
// error ONLY when a complete, valid traversal proved the key is not present; a missing or corrupt
// page, or a collision, returns an error instead (never a false absence).
func (r *deliveryRadix) lookup(ctx context.Context, root radixHash, key []byte) (value []byte, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if root.isZero() {
		return nil, false, nil // the empty tree holds nothing; a real absence, not unavailability
	}
	target := r.hashKey(key)
	cur := root
	depth := 0
	for step := 0; step <= radixHashBits; step++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		node, err := r.readNode(cur)
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
			next := node.child0
			if bitAt(target[:], splitBit) == 1 {
				next = node.child1
			}
			if next.isZero() {
				return nil, false, errRadixUnavailable
			}
			cur = next
			depth = splitBit + 1
		default:
			return nil, false, errRadixUnavailable
		}
	}
	// A well-formed tree consumes at most 256 bits before reaching a leaf; exceeding it means the
	// pages disagree with a valid Patricia shape.
	return nil, false, errRadixUnavailable
}

// insert returns the root of the tree that is root with (key → value) added or updated. Every page on
// the copied path is written and fsynced before the new root is returned; unchanged subtrees are
// referenced by their existing, already-durable pages. root is never mutated, so a caller holding an
// older root keeps a usable, immutable generation.
func (r *deliveryRadix) insert(ctx context.Context, root radixHash, key, value []byte) (radixHash, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	if len(key) == 0 || len(key) > radixMaxKeyBytes || len(value) > radixMaxValueBytes {
		return radixHash{}, errRadixTooLarge
	}
	target := r.hashKey(key)
	if root.isZero() {
		return r.writeLeaf(target, key, value)
	}
	return r.insertAt(ctx, root, 0, target, key, value)
}

// insertAt returns the new hash of the subtree rooted at cur (reached at depth) after adding
// (target,key,value). Path-copy happens as the recursion unwinds. Depth is bounded by the hash width.
func (r *deliveryRadix) insertAt(ctx context.Context, cur radixHash, depth int, target radixHash, key, value []byte) (radixHash, error) {
	if err := ctx.Err(); err != nil {
		return radixHash{}, err
	}
	if depth >= radixHashBits {
		return radixHash{}, errRadixUnavailable
	}
	node, err := r.readNode(cur)
	if err != nil {
		return radixHash{}, err
	}
	switch node.kind {
	case radixKindLeaf:
		if !bitsEqual(node.keyHash[:], target[:], 0, depth) {
			return radixHash{}, errRadixUnavailable
		}
		if node.keyHash == target {
			if !bytes.Equal(node.key, key) {
				return radixHash{}, errRadixCollision // same digest, different key: refuse, never overwrite
			}
			return r.writeLeaf(target, key, value) // same key: a new (possibly identical) value
		}
		// Two distinct keys diverge at the first differing bit at or after depth.
		p := firstDiffBit(node.keyHash[:], target[:], depth)
		if p < depth || p >= radixHashBits {
			return radixHash{}, errRadixUnavailable
		}
		return r.split(depth, p, target, key, value, cur) // the resident leaf is reused unchanged
	case radixKindBranch:
		splitBit := depth + node.skipLen
		if splitBit >= radixHashBits {
			return radixHash{}, errRadixUnavailable
		}
		diff, matched := comparePrefix(target, depth, node.skipBits, node.skipLen)
		if matched {
			b := bitAt(target[:], splitBit)
			childHash := node.child0
			if b == 1 {
				childHash = node.child1
			}
			if childHash.isZero() {
				return radixHash{}, errRadixUnavailable
			}
			newChild, err := r.insertAt(ctx, childHash, splitBit+1, target, key, value)
			if err != nil {
				return radixHash{}, err
			}
			if newChild == childHash {
				return cur, nil // nothing below changed; the existing, durable page still stands
			}
			c0, c1 := node.child0, node.child1
			if b == 1 {
				c1 = newChild
			} else {
				c0 = newChild
			}
			return r.writeBranch(node.skipLen, node.skipBits, c0, c1)
		}
		// The target leaves the branch's common prefix at bit diff. Re-base the existing branch one
		// level deeper (its skip loses the consumed bits) and split above it.
		p := diff
		newSkipLen := splitBit - (p + 1)
		if newSkipLen < 0 {
			return radixHash{}, errRadixUnavailable
		}
		rebased, err := r.writeBranch(newSkipLen, packBits(node.skipBits, (p+1)-depth, newSkipLen), node.child0, node.child1)
		if err != nil {
			return radixHash{}, err
		}
		return r.split(depth, p, target, key, value, rebased)
	default:
		return radixHash{}, errRadixUnavailable
	}
}

// split writes a new branch at depth that separates a new leaf (for target,key,value) from an
// existing subtree other at bit p. The common bits [depth,p) come from the target (equal to other's on
// those bits by construction). other is a leaf reused as-is, or a re-based branch.
func (r *deliveryRadix) split(depth, p int, target radixHash, key, value []byte, other radixHash) (radixHash, error) {
	leaf, err := r.writeLeaf(target, key, value)
	if err != nil {
		return radixHash{}, err
	}
	skipLen := p - depth
	c0, c1 := leaf, other
	if bitAt(target[:], p) == 1 {
		c0, c1 = other, leaf
	}
	return r.writeBranch(skipLen, packBits(target[:], depth, skipLen), c0, c1)
}

// ── page IO ──────────────────────────────────────────────────────────────────────────────────────

func (r *deliveryRadix) writeLeaf(keyHash radixHash, key, value []byte) (radixHash, error) {
	return r.writePage(encodeRadixLeaf(keyHash, key, value))
}

func (r *deliveryRadix) writeBranch(skipLen int, skipBits []byte, c0, c1 radixHash) (radixHash, error) {
	if skipLen < 0 || skipLen > radixHashBits || c0.isZero() || c1.isZero() {
		return radixHash{}, errRadixUnavailable
	}
	return r.writePage(encodeRadixBranch(skipLen, skipBits, c0, c1))
}

// writePage stores raw as a content-addressed page and returns its hash. A page already on disk is
// re-read, verified against its own name and fsynced before reuse; unequal bytes at that name are
// refused, never overwritten. A new page is staged, fsynced, atomically renamed into place, and its
// directory fsynced before the hash is returned, so a returned root is durable.
func (r *deliveryRadix) writePage(raw []byte) (radixHash, error) {
	if len(raw) > radixMaxPageBytes {
		return radixHash{}, errRadixTooLarge
	}
	h := radixDigest(radixPageDomain, raw)
	shard := r.shard(h)
	name := r.pageName(h)

	created, err := r.ensureShard(shard)
	if err != nil {
		return radixHash{}, err
	}
	if created {
		if err := paths.SyncDir(r.dir); err != nil {
			return radixHash{}, err
		}
	}

	if info, statErr := r.root.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() {
			return radixHash{}, errRadixUnavailable
		}
		existing, rerr := r.readPage(h) // verifies content-hash == name
		if rerr != nil {
			return radixHash{}, rerr // a corrupt file at this address: refuse, do not overwrite
		}
		if !bytes.Equal(existing, raw) {
			return radixHash{}, errRadixUnavailable // bytes disagree at one address: preserve, refuse
		}
		if err := r.syncFile(name); err != nil {
			return radixHash{}, err
		}
		return h, nil
	} else if !os.IsNotExist(statErr) {
		return radixHash{}, errRadixUnavailable
	}

	suffix, err := radixTempSuffix()
	if err != nil {
		return radixHash{}, err
	}
	tmp := filepath.Join(shard, radixTempPrefix+suffix)
	if err := r.stageAndRename(tmp, name, raw); err != nil {
		// A concurrent writer may have landed the identical page; accept it after verifying.
		if _, statErr := r.root.Stat(name); statErr == nil {
			if existing, rerr := r.readPage(h); rerr == nil && bytes.Equal(existing, raw) {
				if serr := r.syncFile(name); serr == nil {
					return h, nil
				}
			}
		}
		return radixHash{}, err
	}
	if err := paths.SyncDir(filepath.Join(r.dir, shard)); err != nil {
		return radixHash{}, err
	}
	return h, nil
}

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
	// Publication must not replace a conflicting page that appeared after the
	// earlier existence check. Link atomically claims an absent content address;
	// unsupported filesystems refuse, preserving the staged bytes for diagnosis.
	if err := r.root.Link(tmp, name); err != nil {
		return err
	}
	return r.root.Remove(tmp)
}

// syncFile fsyncs an existing page. It opens for write because Windows FlushFileBuffers needs a write
// handle, matching internal/store's own os.Root sync path.
func (r *deliveryRadix) syncFile(name string) error {
	f, err := r.root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
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

func (r *deliveryRadix) readNode(h radixHash) (radixNode, error) {
	raw, err := r.readPage(h)
	if err != nil {
		return radixNode{}, err
	}
	return decodeRadixPage(raw)
}

// readPage reads the page named by h, bounds the read, and verifies the bytes hash to the name. Any
// missing, oversize, short or mismatched file is unavailable — never treated as an empty tree.
func (r *deliveryRadix) readPage(h radixHash) ([]byte, error) {
	name := r.pageName(h)
	shard, err := r.root.Lstat(r.shard(h))
	if err != nil || !shard.IsDir() || shard.Mode()&os.ModeSymlink != 0 {
		return nil, errRadixUnavailable
	}
	before, err := r.root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() > int64(radixMaxPageBytes) {
		return nil, errRadixUnavailable
	}
	f, err := r.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", errRadixUnavailable, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: stat: %v", errRadixUnavailable, err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != before.Size() || info.Size() > int64(radixMaxPageBytes) {
		return nil, errRadixUnavailable
	}
	raw := make([]byte, info.Size())
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, fmt.Errorf("%w: read: %v", errRadixUnavailable, err) // truncated included
	}
	if radixDigest(radixPageDomain, raw) != h {
		return nil, errRadixUnavailable // corrupt or tampered content at this address
	}
	return raw, nil
}

func (r *deliveryRadix) shard(h radixHash) string { return hex.EncodeToString(h[:1]) }
func (r *deliveryRadix) pageName(h radixHash) string {
	return filepath.Join(r.shard(h), hex.EncodeToString(h[:])+radixPageSuffix)
}

// pagePath is the absolute on-disk path of a page, for in-package tests that inject faults.
func (r *deliveryRadix) pagePath(h radixHash) string { return filepath.Join(r.dir, r.pageName(h)) }

// ── deterministic, versioned page encoding ───────────────────────────────────────────────────────

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
