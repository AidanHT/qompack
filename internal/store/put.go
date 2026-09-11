package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/tokens"
)

// canonFallbackWarned records which store roots have already logged the "canonicalizer
// unavailable" warning, so the fallback is announced once per store rather than once per Put.
//
// It is keyed by project root — a string, not a *FSStore — deliberately: a pointer key would pin
// every store ever opened in this process for the process's lifetime, and the set of project roots
// a single process sees is bounded by the number of projects it serves.
//
// The gate was added because internal/canon was an SP-01 stub reporting core.ErrNotImplemented on
// every call, so without it every single Put emitted an identical Warn line. SP-04's real registry
// does not fail, which makes the fallback genuinely exceptional again — and that is exactly when
// once-per-store matters most: a canonicalizer that starts failing mid-session is one operator
// signal, not one per tool result, and the counter store.canon.fallback carries the rate.
var canonFallbackWarned sync.Map

// putBufPool recycles the read buffer Put fills from an io.Reader, so a hot path that streams tool
// results does not allocate a fresh multi-megabyte buffer per call.
var putBufPool = sync.Pool{New: func() any { return new([]byte) }}

// Put reads r (bounded by MaxPutBytes) and stores it.
//
// Reading more than MaxPutBytes sets Truncated and DISCARDS the tail rather than reporting an
// error: every producer of a Put is a hook, and §2.3 permits a hook no exit code but 0, so a
// pathologically large tool result must degrade to "we kept the first 64 MiB" rather than to a
// failed session.
func (s *FSStore) Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error) {
	if err := s.use(); err != nil {
		return PutResult{}, err
	}
	// Checked here as well as in PutBytes, because the read below happens first and a 64 MiB stream
	// is exactly the case where an already-expired budget must not be spent.
	if err := ctx.Err(); err != nil {
		return PutResult{}, err
	}
	if r == nil {
		return s.PutBytes(ctx, nil, o)
	}

	bufp, _ := putBufPool.Get().(*[]byte)
	if bufp == nil {
		bufp = new([]byte)
	}
	defer func() {
		*bufp = (*bufp)[:0]
		putBufPool.Put(bufp)
	}()

	// MaxPutBytes+1 so a stream that is exactly MaxPutBytes long is NOT reported truncated, while
	// one byte more is.
	b, err := readAllInto((*bufp)[:0], io.LimitReader(r, MaxPutBytes+1))
	*bufp = b
	if err != nil {
		return PutResult{}, fmt.Errorf("store: reading content to put: %w", err)
	}
	return s.PutBytes(ctx, b, o)
}

// readAllInto reads r to EOF, appending into dst's storage.
func readAllInto(dst []byte, r io.Reader) ([]byte, error) {
	for {
		if len(dst) == cap(dst) {
			dst = append(dst, 0)[:len(dst)]
		}
		n, err := r.Read(dst[len(dst):cap(dst)])
		dst = dst[:len(dst)+n]
		if err != nil {
			if err == io.EOF {
				return dst, nil
			}
			return dst, err
		}
	}
}

// PutBytes stores b, returning the resulting Root and what it cost.
//
// The pipeline order is normative and is the whole reason this function exists as one place:
// REDACT, then canonicalize, then chunk (Qompack.md §8.1 item 1; 00-ARCHITECTURE.md §5.22a).
// Redaction runs first because objects are content-addressed and immutable — a secret that reaches
// objects/ cannot be deleted without breaking every root that references its chunk, which is
// exactly what §13 invariant 7 forbids.
func (s *FSStore) PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error) {
	if err := s.use(); err != nil {
		return PutResult{}, err
	}
	// The most expensive call in the package — redaction, canonicalization, chunking, compression
	// and the object writes — and the one SP-05 runs closest to budget B-C. Refusing an
	// already-cancelled context here is what keeps that budget from being advisory.
	if err := ctx.Err(); err != nil {
		return PutResult{}, err
	}

	var res PutResult
	if len(b) > MaxPutBytes {
		b, res.Truncated = b[:MaxPutBytes], true
	}
	res.Root.RawBytes = int64(len(b))

	// 1. REDACT — before canonicalization, before chunking. §13 invariant 7.
	red, matches := s.deps.Redact.Redact(b)
	res.Redacted = len(matches)

	// 2. CANONICALIZE (O2, §8.1 item 1).
	cr, err := s.deps.Canon.Run(o.Tool, o.Path, red, s.canonOptions(o))
	if err != nil {
		s.noteCanonFallback(o.Tool, err)
		cr = canon.Result{Canonical: red}
	}
	canonical := cr.Canonical
	res.Signature, res.Root.CanonBytes = cr.Signature, int64(len(canonical))

	// 3. CHUNK.
	chunks := s.splitChecked(canonical)
	root := chunk.RootHash(chunks)
	res.Root.Hash = root

	// 4. Root-level dedup: an identical root is a pure reference, zero writes.
	//
	// Checked twice. The first check takes no put lock, so the warm path — the four-reads-of-one-
	// file case — never waits on another put. A miss then takes this root's put lock and checks
	// again: a concurrent put of the same canonical root may have published while this one waited,
	// and it must then be a dedup hit of that put rather than a second writer. The lock is held
	// until this put's own content line is published, so the second check is conclusive.
	if hit, ok := s.dedupHit(res, o, red, canonical, cr.Deltas); ok {
		return hit, nil
	}
	lock := s.putLock(root)
	lock.Lock()
	defer lock.Unlock()
	if hit, ok := s.dedupHit(res, o, red, canonical, cr.Deltas); ok {
		return hit, nil
	}

	// 5. Chunk-level dedup + object writes + token measurement.
	//
	// The pending-write registry opens HERE, before the first object lands, and closes only once
	// the roots.jsonl line has: everything between the two is content that is durable on disk but
	// referenced by no index entry, which is exactly the window SP-20 invariant 9's "pending
	// write" clause covers. It is retired on the success path only — an error return deliberately
	// leaves the marker behind, because that is the crash it exists to survive.
	class := tokens.Classify(o.Tool, o.Path, canonical)
	refs := make([]core.ChunkRef, len(chunks))
	for i, c := range chunks {
		refs[i] = core.ChunkRef{Hash: c.Hash, Len: c.Len}
	}
	pw := s.pending(root, refs)
	for _, c := range chunks {
		plain := canonical[c.Offset : c.Offset+int64(c.Len)]
		n, novel, perr := s.putObject(c.Hash, plain)
		if perr != nil {
			return res, fmt.Errorf("store: put chunk %s: %w", c.Hash.Short(), perr)
		}
		if novel {
			res.Novel++
			s.addBytes(n)
			if sink, ok := s.deps.Tokens.(tokens.ChunkSink); ok {
				sink.NoteChunk(c.Hash, class, plain)
			}
		} else {
			res.Reused++
		}
	}
	res.Root.Chunks = refs
	res.Root.Tokens = s.deps.Tokens.EstimateRoot(ctx, refs, class)

	// 6. Recovery record (§8.1 "keep the volatile deltas as a tiny side record"), admitted only
	//    under SP-20 invariant 6.
	rec, fidelity, err := s.admitRecovery(ctx, root, refs, red, canonical, cr.Deltas, o)
	if err != nil {
		return res, err
	}
	res.Fidelity = fidelity

	// 7. Append the roots.jsonl line and publish to the in-memory index.
	if err := s.appendRoot(rootEntry{
		Root: res.Root, TS: s.now(), Tool: o.Tool, Path: o.Path,
		Class: uint8(class), Eph: o.Ephemeral, Sig: res.Signature,
		Deltas: rec.deltas, Orig: rec.orig, Verbatim: rec.verbatim,
	}); err != nil {
		return res, err
	}
	pw.done()
	res.NearDup = s.nearDup(o.Path, root, res.Root.CanonBytes, res.Signature)
	s.countRaw(res.Root.RawBytes)
	return res, nil
}

// putLockStripes is how many put locks FSStore.putLocks holds. putLock picks one by the root's
// first byte, which is uniform because a root is a SHA-256.
const putLockStripes = 256

// putLock is the lock that serializes the puts of root. See FSStore.putLocks.
func (s *FSStore) putLock(root core.Hash) *sync.Mutex {
	return &s.putLocks[int(root[0])%putLockStripes]
}

// dedupHit is PutBytes step 4 for a root that is already stored: this put becomes a pure
// reference to it, writes nothing, and reports the fidelity dedupFidelity derives for this put's
// own original. ok is false, and res is returned untouched, when the root is not stored.
func (s *FSStore) dedupHit(
	res PutResult, o PutOptions, red, canonical []byte, deltas []canon.Delta,
) (hit PutResult, ok bool) {
	root := res.Root.Hash
	s.mu.RLock()
	existing, known := s.rootIndex[root]
	var priorRoot rootEntry
	if known {
		priorRoot = *existing
	}
	s.mu.RUnlock()
	if !known {
		return res, false
	}
	raw := res.Root.RawBytes // THIS put's raw size…
	res.Root, res.Novel, res.Reused = priorRoot.Root, 0, len(priorRoot.Root.Chunks)
	res.Root.RawBytes = raw // …not the stored root's.
	res.NearDup = s.nearDup(o.Path, root, res.Root.CanonBytes, res.Signature)
	res.Fidelity = s.dedupFidelity(priorRoot, red, canonical, deltas)
	s.countRaw(raw)
	return res, true
}

// dedupFidelity reports how recoverable THIS put's original is when its canonical root is already
// stored, so a root-level dedup hit answers the question a fresh put answers.
//
// A dedup hit writes nothing, so the only original it can claim is the one the stored root's
// recovery record restores — and that is this put's original only sometimes. Two inputs that
// differ in nothing but a volatile token canonicalize to one root, and that root restores the
// FIRST input, never the second. So the stored record is re-derived from this put's own bytes and
// compared, with no object read:
//
//   - a retained full original is this put's when it has the address this put's bytes would have;
//   - a delta record is this put's when it declares this root as its base, has the address this
//     put's deltas would have (in the current or the pre-SP20-D3 encoding), and those deltas
//     replay onto the canonical bytes to exactly this put's bytes;
//   - a verbatim record is this put's when canonicalization changed nothing here either.
//
// Anything else is FidelityCanonical: of THIS original, the canonical bytes are all the store is
// known to hold. That can under-claim — verbatim bytes deduplicating onto a root first stored
// without KeepRaw — but it never claims an original the root does not restore, and in that case it
// is also what RestoreOriginal says about the root.
func (s *FSStore) dedupFidelity(e rootEntry, red, canonical []byte, deltas []canon.Delta) Fidelity {
	switch {
	case !e.Orig.IsZero():
		if s.sideRecordAddress(red) == e.Orig {
			return FidelityFull
		}
	case !e.Deltas.IsZero():
		if s.deltaRecordIsThisPuts(e, red, canonical, deltas) {
			return FidelityExact
		}
	case e.Verbatim:
		if bytes.Equal(canonical, red) {
			return FidelityExact
		}
	}
	return FidelityCanonical
}

// deltaRecordIsThisPuts is dedupFidelity's delta case: the stored root's delta record is owned by
// that root and is exactly the record this put would have written.
func (s *FSStore) deltaRecordIsThisPuts(e rootEntry, red, canonical []byte, deltas []canon.Delta) bool {
	if len(deltas) == 0 {
		return false // no deltas recorded here (no KeepRaw, or nothing volatile): nothing to compare
	}
	s.mu.RLock()
	rec, ok := s.rootIndex[e.Deltas]
	owned := ok && rec.Base == e.Root.Hash
	s.mu.RUnlock()
	if !owned {
		return false // a record another base declares is not this root's recovery (SP20-D3)
	}
	if e.Deltas != s.sideRecordAddress(marshalDeltaRecord(e.Root.Hash, deltas)) &&
		e.Deltas != s.sideRecordAddress(marshalDeltas(deltas)) {
		return false
	}
	got, err := canon.Restore(canonical, deltas)
	return err == nil && bytes.Equal(got, red)
}

// recovery is the record one put persisted to make its original recoverable: a delta record, a
// retained full original, or the verbatim claim that the canonical bytes are the original. At most
// one is set, and none is when no recovery record was asked for.
type recovery struct {
	deltas, orig core.Hash
	verbatim     bool
}

// errSideRecordTaken reports a delta record whose content address is already held by a root that
// does not declare the same base, so it cannot serve as this base's record.
var errSideRecordTaken = errors.New("store: side-record address is held by another root")

// admitRecovery decides how this put's ORIGINAL bytes are recoverable, and persists the record
// that makes the claim true.
//
// This is SP-20 invariant 6 in one place: "an exact delta must reconstruct exactly from a durable
// declared base; no base means no delta-only recovery claim." A delta is admitted ONLY when every
// chunk of its declared base is already on disk AND canon.Restore reproduces the redacted input
// byte for byte. Anything else — an unprovable delta list, a canonicalizer that changed bytes
// without recording them, a base whose objects did not land, a delta record whose address another
// root already holds — falls back to retaining the original as its own full object, which is a
// weaker representation but never a false claim.
//
// KeepRaw = false asks for no recovery record at all, so the canonical bytes are all that exist
// and the fidelity says exactly that.
func (s *FSStore) admitRecovery(
	ctx context.Context, base core.Hash, refs []core.ChunkRef, red, canonical []byte,
	deltas []canon.Delta, o PutOptions,
) (recovery, Fidelity, error) {
	if !o.KeepRaw {
		return recovery{}, FidelityCanonical, nil
	}
	// Canonicalization changed nothing: the stored bytes ARE the original, with no side record to
	// go wrong. This is the ordinary case for content with nothing volatile in it, and the content
	// line records it (verbatim) so a read reports the same exactness this put does.
	if len(deltas) == 0 && bytes.Equal(canonical, red) {
		return recovery{verbatim: true}, FidelityExact, nil
	}
	if len(deltas) > 0 && s.deltaProven(base, refs, red, canonical, deltas) {
		dr, derr := s.putDeltas(ctx, deltas, base, o.Ephemeral)
		if derr == nil {
			return recovery{deltas: dr}, FidelityExact, nil
		}
		if !errors.Is(derr, errSideRecordTaken) {
			return recovery{}, FidelityCanonical, derr
		}
		// Pointing at a record another root holds would claim a recovery that root cannot give;
		// the full original is still true.
	}
	or, oerr := s.putFullOriginal(ctx, red, base, o.Ephemeral)
	if oerr != nil {
		return recovery{}, FidelityCanonical, oerr
	}
	return recovery{orig: or}, FidelityFull, nil
}

// deltaProven reports whether deltas may be persisted as base's exact recovery record.
//
// Two independent checks, and both must hold. DURABILITY: every chunk of the declared base is
// present under objects/ — a delta that replays onto a base which is not there reconstructs
// nothing, so persisting one would file a recovery claim that cannot be honoured. EXACTNESS:
// canon.Restore(canonical, deltas) equals the redacted input byte for byte, which is the only
// evidence that the side record actually inverts the transform that produced it. Neither is
// asserted anywhere else on the write path; before SP-20 the round-trip was checked only in
// internal/canon's own tests, which say nothing about the bytes this store just wrote.
func (s *FSStore) deltaProven(base core.Hash, refs []core.ChunkRef, red, canonical []byte, deltas []canon.Delta) bool {
	for _, c := range refs {
		if !s.objectExists(c.Hash) {
			s.count("store.delta.baseNotDurable", 1)
			s.log.Warn("store: refusing a delta whose declared base is not durable",
				"base", base.Short(), "chunk", c.Hash.Short())
			return false
		}
	}
	got, err := canon.Restore(canonical, deltas)
	if err != nil || !bytes.Equal(got, red) {
		s.count("store.delta.roundTripUnproven", 1)
		s.log.Warn("store: refusing a delta whose round trip is not exact; retaining a full object instead",
			"base", base.Short(), "err", err, "want", len(red), "got", len(got))
		return false
	}
	return true
}

// noteCanonFallback records that canonicalization was unavailable for this Put and warns once per
// store. See canonFallbackWarned for why the warning is gated.
func (s *FSStore) noteCanonFallback(tool string, err error) {
	s.count("store.canon.fallback", 1)
	if _, seen := canonFallbackWarned.LoadOrStore(s.root, struct{}{}); seen {
		return
	}
	s.log.Warn("store: canonicalize failed, storing redacted bytes", "tool", tool, "err", err)
}

// addBytes accumulates compressed object bytes for Stats.Bytes.
func (s *FSStore) addBytes(n int64) {
	if n == 0 {
		return
	}
	s.mu.Lock()
	s.bytesOnDisk += n
	s.statsDirty = true
	s.mu.Unlock()
}

// countRaw accumulates pre-dedup, pre-compression input size for Stats.RawBytes.
//
// It counts EVERY Put, including one whose content deduplicated away to nothing. That is what
// makes Stats.DedupRatio the Phase 1 exit criterion Qompack.md §10 names — "store size vs. raw
// transcript ratio" — rather than a mere compression ratio: four reads of one file must count as
// four files of raw transcript against one stored chunk set.
func (s *FSStore) countRaw(raw int64) {
	s.mu.Lock()
	s.rawBytes += raw
	s.statsDirty = true
	s.mu.Unlock()
}

// canonOptions maps this store's configuration, plus one call's PutOptions, onto canon.Options.
//
// When store.canonicalize.enabled is false, Strip is nil: only the CRLF normalization
// 00-ARCHITECTURE.md §4 mandates remains, so a Windows read and a Linux read of the same file
// still produce the same chunks and still dedup against each other.
//
// PutOptions.Canon.Strip carries the whole per-call override decision, and its NIL-ness is the
// signal — not its length. internal/canon's gateSet already draws exactly this distinction: a nil
// Strip means "every class", a non-nil but EMPTY Strip means "exactly these — i.e. none — plus the
// always-on structural crlf/paths". So:
//
//   - a NIL Canon.Strip means "I supplied no per-call canon override at all": the store's
//     configured classes AND the store's configured MinHash both stand;
//   - a NON-NIL Canon.Strip, empty included, means "this entire canon.Options is mine, MinHash
//     included" — the classes are the caller's, and Canon.MinHash.Enabled == false turns the
//     signature off for this one Put.
//
// The nil gate on the MinHash opt-out is load-bearing, not decoration. sketch.MinHashOptions.Enabled
// is a plain bool and PutOptions.Canon is a value field, so an explicit false is byte-identical to
// the zero value: an ungated rule would read every store.PutOptions{} in the tree as an opt-out,
// zero every PutResult.Signature, and silently retire FSStore.nearDup — §8.1 item 3's redundancy
// detector, which supersede.go depends on. Strip is the one field that CAN say "unset", which is
// why it carries the decision for both.
//
// The reverse direction stays config-wins: a caller may turn the signature OFF for one Put, never
// on, because the permutation count and the near-dup threshold are configuration the caller does
// not own. Canon.KeepDeltas is likewise never read from Canon — it is derived from
// PutOptions.KeepRaw, which is where §5.8 puts that decision.
func (s *FSStore) canonOptions(o PutOptions) canon.Options {
	cc := s.cfg.Store.Canonicalize
	opts := canon.Options{
		KeepDeltas: o.KeepRaw,
		MinHash: sketch.MinHashOptions{
			Enabled:          cc.MinHash.Enabled,
			Permutations:     cc.MinHash.Permutations,
			NearDupThreshold: cc.MinHash.NearDupThreshold,
		},
	}
	if cc.Enabled {
		opts.Strip = make([]canon.Class, 0, len(cc.Strip))
		for _, c := range cc.Strip {
			opts.Strip = append(opts.Strip, canon.Class(c))
		}
	}
	// A caller that supplied its own canon.Options wins: PutOptions.Canon is the per-call override
	// the §5.8 shape provides for exactly this.
	if o.Canon.Strip != nil {
		opts.Strip = o.Canon.Strip
		if !o.Canon.MinHash.Enabled {
			opts.MinHash.Enabled = false
		}
	}
	return opts
}

// nearDup reports whether sig is a near-duplicate of the most recent prior root stored for path.
//
// This is §8.1's "same test suite, one new failure" detector: content that still differs after
// canonicalization but is substantially the same as its predecessor.
func (s *FSStore) nearDup(path string, root core.Hash, canonBytes int64, sig sketch.Signature) *NearDupInfo {
	cc := s.cfg.Store.Canonicalize
	if path == "" || !cc.MinHash.Enabled {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	hashes := s.byPath[storeKey(path)]
	for i := len(hashes) - 1; i >= 0; i-- {
		prior, ok := s.rootIndex[hashes[i]]
		if !ok || prior.Root.Hash == root {
			continue
		}
		j := prior.Sig.Jaccard(sig)
		if j < cc.MinHash.NearDupThreshold {
			return nil
		}
		diff := canonBytes - prior.Root.CanonBytes
		if diff < 0 {
			diff = -diff
		}
		return &NearDupInfo{PriorRoot: prior.Root.Hash, Jaccard: j, DeltaBytes: diff}
	}
	return nil
}

// putDeltas stores the volatile side record canonicalization produced, returning its root.
//
// It deliberately does NOT recurse through PutBytes. The deltas were extracted from bytes step 1
// already redacted, so they are clean by construction and redacting them again would be wasted
// work on the hot path; and canonicalizing the record of what canonicalization removed is
// meaningless. The record is filed as an ordinary root under the synthetic tool name "«deltas»",
// so GC sees and retains it exactly like any other root rather than treating it as an orphan.
//
// The payload names its base (marshalDeltaRecord), which is what gives every base a record of its
// own. Before SP20-D3 it was the delta list alone, so two roots whose canonicalization removed the
// same token at the same offset produced byte-identical records: putSideRecord handed the second
// root the first root's record, and the second root's exactness claim failed on read.
func (s *FSStore) putDeltas(ctx context.Context, deltas []canon.Delta, base core.Hash, eph bool) (core.Hash, error) {
	return s.putSideRecord(ctx, marshalDeltaRecord(base, deltas), deltaToolName, tokens.ClassJSON, base, eph)
}

// putFullOriginal retains the redacted ORIGINAL bytes as their own full object, for a base whose
// exact delta could not be proven.
//
// It is the "otherwise retain a full object" half of invariant 6, and it is a deliberate storage
// trade: keeping the original whole costs more than a delta and is the only representation left
// that is still true. It is filed under the synthetic tool name "«raw»" for the same reason the
// delta record is filed under "«deltas»" — so GC sees an ordinary root rather than an orphan — and
// declares the same Base, so the two are coupled by exactly one rule in the mark phase.
func (s *FSStore) putFullOriginal(ctx context.Context, red []byte, base core.Hash, eph bool) (core.Hash, error) {
	return s.putSideRecord(ctx, red, rawToolName, tokens.ClassProse, base, eph)
}

// putSideRecord stores one recovery side record (a delta list or a retained original) as its own
// root, declaring the base it belongs to.
//
// Ephemerality is INHERITED from the content root. Without that a born-ephemeral retrieval result
// would acquire a non-ephemeral companion, and §8.7's "an ephemeral root is never in-window by the
// age clause" would be void through the coupling rule that keeps the pair together.
func (s *FSStore) putSideRecord(
	ctx context.Context, payload []byte, tool string, class tokens.Class, base core.Hash, eph bool,
) (core.Hash, error) {
	chunks := s.splitChecked(payload)
	root := chunk.RootHash(chunks)

	s.mu.RLock()
	prior, known := s.rootIndex[root]
	var priorTool string
	var priorBase core.Hash
	if known {
		priorTool, priorBase = prior.Tool, prior.Base
	}
	s.mu.RUnlock()
	if known {
		// A retained original may be shared by any root: it IS the bytes, and RestoreOriginal reads
		// it without consulting its base. A delta record may be reused only by the base it declares,
		// because replaying it onto any other root is the SP20-D3 failure. The base is inside the
		// payload, so the address already implies it; a mismatch here means some OTHER root holds
		// byte-identical content at this address, and the caller must not point at it.
		if tool == deltaToolName && (priorTool != deltaToolName || priorBase != base) {
			s.count("store.delta.addressTaken", 1)
			return core.Hash{}, fmt.Errorf("%w: %s", errSideRecordTaken, root.Short())
		}
		return root, nil
	}

	refs := make([]core.ChunkRef, len(chunks))
	for i, c := range chunks {
		refs[i] = core.ChunkRef{Hash: c.Hash, Len: c.Len}
	}
	pw := s.pending(root, refs)
	for _, c := range chunks {
		n, novel, err := s.putObject(c.Hash, payload[c.Offset:c.Offset+int64(c.Len)])
		if err != nil {
			return core.Hash{}, fmt.Errorf("store: put %s chunk %s: %w", tool, c.Hash.Short(), err)
		}
		if novel {
			// Side-record bytes count toward Stats.Bytes but NEVER toward Stats.RawBytes: they are
			// store overhead, not transcript, and folding them into the numerator would inflate
			// DedupRatio — the one number the Phase 1 exit criterion turns on.
			s.addBytes(n)
		}
	}

	dr := Root{
		Hash: root, Chunks: refs,
		CanonBytes: int64(len(payload)), RawBytes: int64(len(payload)),
		Tokens: s.deps.Tokens.EstimateRoot(ctx, refs, class),
	}
	if err := s.appendRoot(rootEntry{
		Root: dr, TS: s.now(), Tool: tool, Path: "", Class: uint8(class), Eph: eph, Base: base,
	}); err != nil {
		return core.Hash{}, err
	}
	pw.done()
	return root, nil
}

// sideRecordAddress is the root putSideRecord files payload under, computed without writing it.
func (s *FSStore) sideRecordAddress(payload []byte) core.Hash {
	return chunk.RootHash(s.splitChecked(payload))
}

// marshalDeltaRecord renders one delta side record — the base it reconstructs, then its deltas —
// with keys in a fixed order, so the record is byte-stable and a retried put finds its own record.
// Naming the base is what makes the record's address unique to that base (SP20-D3);
// unmarshalDeltaRecord is the inverse.
func marshalDeltaRecord(base core.Hash, deltas []canon.Delta) []byte {
	dst := make([]byte, 0, 96+len(deltas)*64)
	dst = append(dst, '{')
	dst = appendKey(dst, "base", true)
	dst = appendJSONString(dst, base.String())
	dst = appendKey(dst, "deltas", false)
	dst = appendDeltas(dst, deltas)
	return append(dst, '}')
}

// marshalDeltas renders deltas alone as one compact JSON array: the payload of every delta record
// written before SP20-D3. Nothing new is written in this shape; it is kept so dedupFidelity can
// recognise such a record as the one its own root owns, and so tests can plant one.
func marshalDeltas(deltas []canon.Delta) []byte {
	return appendDeltas(make([]byte, 0, len(deltas)*64), deltas)
}

// appendDeltas appends deltas as one compact JSON array with keys in a fixed order.
func appendDeltas(dst []byte, deltas []canon.Delta) []byte {
	dst = append(dst, '[')
	for i, d := range deltas {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		dst = appendKey(dst, "o", true)
		dst = strconv.AppendInt(dst, int64(d.Offset), 10)
		dst = appendKey(dst, "l", false)
		dst = strconv.AppendInt(dst, int64(d.Len), 10)
		dst = appendKey(dst, "c", false)
		dst = appendJSONString(dst, string(d.Class))
		dst = appendKey(dst, "s", false)
		dst = appendJSONString(dst, d.Original)
		dst = append(dst, '}')
	}
	return append(dst, ']')
}
