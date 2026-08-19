package store

import (
	"context"
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
	s.mu.RLock()
	existing, known := s.rootIndex[root]
	var priorRoot rootEntry
	if known {
		priorRoot = *existing
	}
	s.mu.RUnlock()
	if known {
		raw := res.Root.RawBytes // THIS put's raw size…
		res.Root, res.Novel, res.Reused = priorRoot.Root, 0, len(priorRoot.Root.Chunks)
		res.Root.RawBytes = raw // …not the stored root's.
		res.NearDup = s.nearDup(o.Path, root, res.Root.CanonBytes, res.Signature)
		s.countRaw(raw)
		return res, nil
	}

	// 5. Chunk-level dedup + object writes + token measurement.
	class := tokens.Classify(o.Tool, o.Path, canonical)
	refs := make([]core.ChunkRef, len(chunks))
	for i, c := range chunks {
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
		refs[i] = core.ChunkRef{Hash: c.Hash, Len: c.Len}
	}
	res.Root.Chunks = refs
	res.Root.Tokens = s.deps.Tokens.EstimateRoot(ctx, refs, class)

	// 6. Volatile side record (§8.1 "keep the volatile deltas as a tiny side record").
	var deltaRoot core.Hash
	if o.KeepRaw && len(cr.Deltas) > 0 {
		deltaRoot, err = s.putDeltas(ctx, cr.Deltas)
		if err != nil {
			return res, err
		}
	}

	// 7. Append the roots.jsonl line and publish to the in-memory index.
	if err := s.appendRoot(rootEntry{
		Root: res.Root, TS: s.now(), Tool: o.Tool, Path: o.Path,
		Class: uint8(class), Eph: o.Ephemeral, Sig: res.Signature, Deltas: deltaRoot,
	}); err != nil {
		return res, err
	}
	res.NearDup = s.nearDup(o.Path, root, res.Root.CanonBytes, res.Signature)
	s.countRaw(res.Root.RawBytes)
	return res, nil
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
	if len(o.Canon.Strip) > 0 {
		opts.Strip = o.Canon.Strip
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
func (s *FSStore) putDeltas(ctx context.Context, deltas []canon.Delta) (core.Hash, error) {
	payload := marshalDeltas(deltas)
	chunks := s.splitChecked(payload)
	root := chunk.RootHash(chunks)

	s.mu.RLock()
	_, known := s.rootIndex[root]
	s.mu.RUnlock()
	if known {
		return root, nil
	}

	refs := make([]core.ChunkRef, len(chunks))
	for i, c := range chunks {
		n, novel, err := s.putObject(c.Hash, payload[c.Offset:c.Offset+int64(c.Len)])
		if err != nil {
			return core.Hash{}, fmt.Errorf("store: put delta chunk %s: %w", c.Hash.Short(), err)
		}
		if novel {
			// Delta bytes count toward Stats.Bytes but NEVER toward Stats.RawBytes: they are store
			// overhead, not transcript, and folding them into the numerator would inflate
			// DedupRatio — the one number the Phase 1 exit criterion turns on.
			s.addBytes(n)
		}
		refs[i] = core.ChunkRef{Hash: c.Hash, Len: c.Len}
	}

	dr := Root{
		Hash: root, Chunks: refs,
		CanonBytes: int64(len(payload)), RawBytes: int64(len(payload)),
		Tokens: s.deps.Tokens.EstimateRoot(ctx, refs, tokens.ClassJSON),
	}
	if err := s.appendRoot(rootEntry{
		Root: dr, TS: s.now(), Tool: deltaToolName, Path: "", Class: uint8(tokens.ClassJSON),
	}); err != nil {
		return core.Hash{}, err
	}
	return root, nil
}

// marshalDeltas renders deltas as one compact JSON array with keys in a fixed order, so the side
// record is byte-stable and therefore dedups against an identical prior record.
func marshalDeltas(deltas []canon.Delta) []byte {
	dst := make([]byte, 0, len(deltas)*64)
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
