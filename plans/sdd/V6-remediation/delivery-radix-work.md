# Delivery radix primitive — work report

Exclusive scope delivered: `internal/daemon/delivery_radix.go`, `internal/daemon/delivery_radix_test.go`,
this report. No other file, source, test, config or commit touched. The unaccepted
`delivery_archive`/`delivery_rollover` prototypes and the ingest/drain/order/observation files owned by
other authors were read only as context, never edited. **This primitive alone never closes the journal
capacity bound and never enables migration** — the parent (journal owner) owns the root commit, the
lease/ACK/session semantics, and any capacity/migration decision.

## 1. Exact API (unexported, package `daemon`)

```go
type radixHash [32]byte            // page/key digest; zero value == empty tree, and the illegal "no child"
func (h radixHash) isZero() bool

type deliveryRadix struct{ … }     // holds dir + *os.Root + an injectable key-hash seam; NO tree state

func openDeliveryRadix(dir string) (*deliveryRadix, error)   // open/create, confined by os.Root
func (r *deliveryRadix) close() error

func (r *deliveryRadix) lookup(ctx context.Context, root radixHash, key []byte) (value []byte, found bool, err error)
func (r *deliveryRadix) insert(ctx context.Context, root radixHash, key, value []byte) (newRoot radixHash, err error)
```

- **Empty tree:** the zero `root` is the empty tree; `lookup` returns `(nil,false,nil)`.
- **Stateless roots:** `insert` returns a *new* root and never mutates `root`; the parent decides which
  root is live and persists it. An older root stays a usable, immutable generation.
- **Values** are opaque, bounded bytes (`≤ 65536`); keys are bounded (`1..4096`).
- The `hashKey func([]byte) radixHash` field is a **test-only seam** (inject a controlled digest to
  exercise a collision or a long shared prefix); production always uses `defaultKeyHash` =
  `sha256("qompack.delivery.radix.key.v1"‖0x00‖key)`.

## 2. Design

A binary **Patricia (compressed radix) trie** over the 256-bit key digest, MSB-first. Two immutable,
**content-addressed** page kinds (filename = `sha256("…page.v1"‖0x00‖bytes)`, 1-level sharded):

- **LEAF** = version + full 32-byte key hash + the **original key bytes** + value.
- **BRANCH** = version + `skipLen`(u16) + `skipBits` (the common prefix below this node) + two
  **non-zero** child hashes; splits on the bit at `depth+skipLen`.

Compression: the shared prefix of two keys is one branch edge, so a leaf costs O(distinct-prefix)
pages, **never 256 pages per leaf**. Depth is bounded by the 256 hash bits (`≤256` descents).

**Path-copy persistence:** `insert` rewrites only the pages on the copied path; unchanged sibling
subtrees are referenced by their existing, already-durable pages. An idempotent insert returns the
same root.

## 3. Invariants and failure vocabulary

- **Sound absence.** `(nil,false,nil)` is returned *only* after a complete valid traversal: at a
  BRANCH, the target's bits are checked against the stored skip (a mismatch proves the whole subtree
  cannot hold the target — a sound absence); at a LEAF, the leaf's hash is checked to **agree with the
  path bits already taken** (a page that merely parses is not proof of placement) before comparing the
  full hash. Because pages are content-addressed and the caller trusts a root it committed, a corrupted
  page fails the hash check and becomes *unavailable* — never a wrong value and never a wrong absence.
- **Unavailable is never absence.** A missing / unreadable / truncated / over-bound / unknown-version /
  structurally-invalid page under a **non-zero** root is `errRadixUnavailable`.
- **Collision never overwrites.** Two distinct original keys with the same 256-bit digest →
  `errRadixCollision` on insert (the resident key is untouched) and on lookup of the intruder (never a
  false match, never absence).
- **Bounds** → `errRadixTooLarge`. All three errors are distinct so the parent maps them; none is ever
  downgraded to absence.
- **Structural validation on decode:** magic + version==1, both branch children non-zero, canonical
  skip (unused trailing skip bits zero), exact-length consumption (no trailing bytes), key/value within
  bound. Any violation → unavailable.

## 4. Durability and safety

- **os.Root confinement** (`os.OpenRoot(paths.Long(dir))`) for every create/read/rename/mkdir/remove,
  so a swapped symlink cannot redirect a read or write outside the index directory (same idiom as
  `internal/store/publication_sync.go`).
- **Write path:** stage to a random `.tmp-<rand>` file → `f.Sync()` → atomic `os.Root.Rename` into the
  content-addressed name → `paths.SyncDir` the shard directory (and the root directory when a new shard
  is created). A crash between stage and rename leaves an ignored orphan; the content address is never
  poisoned by a partial write.
- **Existing page reuse:** a page already on disk is re-read, verified to hash to its own name, and
  fsynced before reuse; bytes that disagree at one address are **refused, never overwritten**.
- **Bounds:** `MaxKey=4096`, `MaxValue=65536`, `MaxPage=MaxKey+MaxValue+128`; reads are size-checked
  before allocation; no whole-index load and no history-length scan — only pages on the current path
  are opened.
- **Context** is checked at entry and at every descent, so a cancelled pass stops promptly.

## 5. Tests (`TestDeliveryRadix_*`, `./internal/daemon`)

Covers: empty-tree absence; **>64 generations** with every old root exact and later keys absent (not
unavailable); same-key value update forking a new root with the old immutable, plus idempotent
re-insert; reopen-persists; **collision refused** (injected digest) with the resident key untouched;
wrong root + corrupt page → unavailable; missing descendant → unavailable while the intact branch still
answers; **lookup never opens an irrelevant corrupt branch**; **long common prefix** compressed to one
`skipLen=200` branch with a sound in-skip absence; cancellation; **crash-orphan** left in place with old
generations still exact; input bounds; and a direct decoder table for malformed/wrong-version/zero-child
/over-long-skip/trailing-byte pages.

## 6. Run status — retained failure, test deferred (as instructed)

```
python dist/v6-remediation/run.py delivery-radix-focused-01 go test -run TestDeliveryRadix -count=1 ./internal/daemon
→ FAILED (exit 1, 1.6s): internal/store/fsstore.go:262:36: undefined: reservedIdentity
  FAIL github.com/qompack/qompack/internal/daemon [build failed]
```

The failure is **entirely in `internal/store`** — the observation-sidecar author's in-flight edit
(`fsstore.go:262` declares `map[core.ToolUseID]reservedIdentity` with the type not yet defined;
`tooluseindex.go` references `reservedIdentityConflictLocked`). The daemon package imports store, so the
whole package cannot build, and Go fails at the store dependency before type-checking the daemon files.
My two files contribute **zero** errors (they are not named in the build log) and are gofmt/gofumpt
(pinned) clean. Per the brief, I **retain this failure and defer** the test rather than edit another
author's file; the run harness recorded it under `plans/sdd/V6-remediation/runs/delivery-radix-focused-01.*`.
Re-run the identical command once `internal/store` compiles again.

Confidence that my code type-checks once the store builds: verified the two non-stdlib symbols I use —
`paths.SyncDir(dir string) error` and `paths.Long(p string) string` — match my usage, and the
`os.Root` methods used (Open/OpenFile/Stat/Mkdir/Remove/Rename/Close) exist in Go 1.26; syntax is
gofumpt-validated. It is source-inspected, not executed.

## 7. Limitations (honest)

- **Not a capacity or migration answer.** A bounded exact index does **not** certify or perform the
  delivery-journal rollover/migration; that is the parent's version-gated design, unaccepted here.
- **Soundness rests on two facts:** pages are content-addressed (any byte corruption → unavailable),
  and the caller only ever trusts a root it itself committed. A caller that hands the primitive a root
  it never built gets whatever that foreign tree encodes; the primitive validates structure, not the
  provenance of the root.
- **Symlink safety** is enforced on file ops via `os.Root`; directory fsync uses `paths.SyncDir` on the
  absolute shard/root path (matching the store's own pattern), which re-resolves the path — the residual
  is a directory the primitive itself created within the confined root.
- **No lease/ACK meaning.** Keys and values are opaque; the parent validates delivery semantics,
  ordering and the "current root". This file adds none of that.
- **No history scan / no whole-tree load**; memory is bounded to the pages on one path. A very large
  index is fine to read from but this primitive does no compaction/GC of superseded pages — orphaned
  old-generation pages accumulate until the parent prunes them (out of scope).
- **Not executed.** No PASS is claimed; the focused run is retained-failed on an unrelated store build
  break, and the test is deferred until the shared package compiles.
```
