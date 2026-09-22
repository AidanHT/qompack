package checkpoint

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// *FileWriter satisfies the §5.14 Writer interface only once Finalize exists, which is why this
// assertion lands with Finalize rather than with the writer: Begin, Advance and Abort alone do not
// make a Writer, and an assertion placed earlier would have had to be commented out.
var _ Writer = (*FileWriter)(nil)

// Finalize writes the draft as an immutable, importance-ordered artifact and appends its manifest
// line, inside budget B-E (Qompack.md §11.3: the whole PreCompact hook is 2 s p99).
//
// It can hold that budget because Advance has already done the O(session) work during idle
// windows: everything here is a validate, a truncate, a marshal and two file operations.
// BenchmarkFinalize in finalize_test.go prices the whole thing against the < 50 ms exit criterion.
//
// The order below is load-bearing. Pointers are validated BEFORE truncation so that a pointer
// removed for being unresolvable does not consume budget that a resolvable one could have used,
// and truncation happens BEFORE marshalling so the bytes written are the bytes measured.
func (w *FileWriter) Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error) {
	if d == nil {
		return Ref{}, fmt.Errorf("checkpoint: Finalize: nil draft")
	}

	// 1. Snapshot AND seal under the draft's own lock, then work on the copy. Sealing in the same
	//    critical section is what stops a concurrent Advance from marking segments as encoded into
	//    this checkpoint after its content has been decided — see Draft.sealForFinalize. The seal
	//    is lifted again if the artifact never lands, so a transient write failure costs nothing
	//    but the attempt.
	cp, src, ok := d.sealForFinalize()
	if !ok {
		return Ref{}, fmt.Errorf("checkpoint: Finalize: session %s, checkpoint %d: %w",
			d.session, int(d.seq), ErrDraftSealed)
	}
	committed := false
	defer func() {
		if !committed {
			d.unseal()
		}
	}()

	cp.Created = CreatedNow(w.clk)
	cp.Version = SchemaVersion
	cp.ensureNonNil()

	// 2. Ground truth (G2.5). ValidatePointers errors only on ctx cancellation, and a cancelled
	//    context here must not cost us the artifact — §12's PreCompact-timeout row says finalize
	//    as-is, so a cancellation falls through to truncation with whatever we have.
	drops, _ := ValidatePointers(ctx, w.root, cp.Pointers)
	cp.Pointers.Files = keepResolvableFiles(cp.Pointers.Files, drops)
	cp.Pointers.Tools, drops = keepResolvableTools(ctx, cp.Pointers.Tools, drops, src)
	cp.Dropped = append(cp.Dropped, drops...)

	// 3. Importance-ordered truncation (§6.9). Truncate has no error return and always yields a
	//    writable tier-1 document, which is what lets a session whose circuit breaker has tripped
	//    still resume from a durable artifact (G7.4).
	if budget <= 0 {
		budget = core.Tokens(w.cfg.Checkpoint.BudgetTokens)
	}
	var tdrops []DropEntry
	cp, tdrops = Truncate(cp, budget, w.cfg.Checkpoint.Tiers, src.Tokens)
	cp.Dropped = append(cp.Dropped, tdrops...)

	// 4-5. Marshal and write immutably, retrying on a claimed sequence number.
	seq := cp.Seq
	var b []byte
	var sum [32]byte
	for attempt := 0; ; attempt++ {
		cp.Seq = seq
		var err error
		if b, err = Marshal(cp); err != nil {
			return Ref{}, err
		}
		sum = sha256.Sum256(b)

		p := paths.CheckpointPath(w.l, seq)
		err = paths.CreateNew(p, b)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			// No manifest line has been appended, so the manifest can never reference a file that
			// does not verify. Remove the partial file if one was left behind.
			_ = os.Remove(paths.Long(p))
			return Ref{}, fmt.Errorf("checkpoint: write %04d: %w", seq, err)
		}
		if attempt >= maxSeqCollisionRetries {
			return Ref{}, fmt.Errorf(
				"checkpoint: sequence %d still claimed after %d attempts: %w",
				seq, maxSeqCollisionRetries, core.ErrAppendOnly)
		}
		seq++
	}
	w.reconcileEncodedSeq(ctx, d, src, cp.EncodedSegments, seq)

	// 6. The manifest is internal/paths' file, written only through AppendManifest.
	entry := paths.ManifestEntry{
		Seq:     seq,
		SHA256:  core.Hash(sum).String(),
		Bytes:   int64(len(b)),
		Created: core.NowMilli(w.clk),
	}
	if err := paths.AppendManifest(w.l, entry); err != nil {
		// The artifact is on disk and verifies; only its index line is missing. Say so loudly and
		// leave the file: `qompack fsck` reconciles an orphan by re-hashing it, whereas deleting a
		// good checkpoint here would lose the session.
		w.log.Loud("checkpoint written but manifest append failed",
			"seq", int(seq), "path", paths.CheckpointPath(w.l, seq), "err", err.Error())
		return Ref{}, fmt.Errorf("checkpoint: manifest append %04d: %w", seq, err)
	}
	// The artifact exists, verifies and is indexed: the draft has become a checkpoint and must stay
	// sealed whatever the tail of this function does.
	committed = true

	if err := src.Pins.Materialize(ctx); err != nil {
		// The materialized view is derived state; a stale invariants.json never costs correctness,
		// so this is a Warn and not a failed finalize.
		w.log.Warn("checkpoint: pins materialize failed after finalize", "seq", int(seq), "err", err.Error())
	}

	ref := Ref{
		Seq:      seq,
		Path:     paths.CheckpointPath(w.l, seq),
		SHA256:   core.Hash(sum),
		Bytes:    int64(len(b)),
		Tokens:   src.Tokens.Estimate(b, tokens.ClassJSON),
		Frontier: d.Frontier(),
		Created:  entry.Created,
	}

	// 7. Retire the draft and open its successor immediately, so frontier advancement resumes on
	//    the very next idle tick. This is what keeps the NEXT residual span O(delta) rather than
	//    letting it grow from zero again (§8.5, O5).
	w.retireDraft(d)
	if _, err := w.Begin(ctx, d.session, seq, src); err != nil {
		// The artifact is written and indexed; only the successor draft failed to open. Advance
		// will open one on its next call.
		w.log.Warn("checkpoint: could not open successor draft", "parent", int(seq), "err", err.Error())
	}

	return ref, nil
}

// metricSeqReferenceDrift counts artifacts written at a sequence number the segment log cannot be
// brought to agree with. §8.2 makes the per-segment flag an "encoded-once flag, AND checkpoint
// reference", and a drift means the reference half is wrong: `qompack fsck` is the tool that
// reconciles it, and this counter is how an operator learns to run it.
const metricSeqReferenceDrift = "checkpoint.seq_reference_drift"

// reconcileEncodedSeq brings the draft and the segment log into agreement with the sequence number
// the artifact was ACTUALLY written at, which is not always the one the draft carried: Finalize's
// O_EXCL retry bumps past a number another process claimed.
//
// Without this, a bumped write leaves every segment Advance marked pointing at a checkpoint that
// does not contain them, and a later core.ErrAlreadyEncoded names the wrong artifact.
//
// Reconciliation is best-effort by necessity, and the limit is worth stating: store.SegmentLog's
// MarkEncoded is deliberately ONE-WAY — it is idempotent for the same sequence and returns
// core.ErrAlreadyEncoded for a different one — so a segment that already names the pre-bump number
// cannot be re-pointed from here at all. That residue is reported Loud and counted rather than
// swallowed, because it is a real inconsistency that only fsck can settle. What this function does
// close is everything the writer controls: the draft's own seq now matches the artifact it became,
// and any segment not yet flagged is flagged against the right checkpoint. The collision itself is
// kept off the common path by Begin, which probes the disk before claiming a number.
func (w *FileWriter) reconcileEncodedSeq(
	ctx context.Context, d *Draft, src SourceSet, ids []core.SegmentID, seq core.CheckpointSeq,
) {
	if d.Seq() == seq {
		return
	}
	d.setSeq(seq)
	if len(ids) == 0 {
		return
	}
	if err := src.Segments.MarkEncoded(ctx, ids, seq); err != nil {
		w.m.Counter(metricSeqReferenceDrift).Add(1)
		w.log.Loud("checkpoint: segments still reference the sequence this checkpoint was NOT written at; run qompack fsck",
			"written_seq", int(seq), "segments", len(ids), "err", err.Error())
	}
}

// keepResolvableFiles returns the file pointers that survive validation. Only pointer_missing and
// pointer_invalid remove a pointer: pointer_dirty and pointer_untracked are informational, because
// a dirty file is exactly the file the agent is working on and dropping it would discard the most
// relevant pointer in the set.
func keepResolvableFiles(in []FilePointer, drops []DropEntry) []FilePointer {
	if len(drops) == 0 {
		return in
	}
	remove := make(map[string]bool, len(drops))
	for _, d := range drops {
		if d.Kind == dropPointerMissing || d.Kind == dropPointerInvalid {
			remove[d.ID] = true
		}
	}
	if len(remove) == 0 {
		return in
	}
	out := in[:0:0]
	for _, p := range in {
		if !remove[p.Path] {
			out = append(out, p)
		}
	}
	return out
}

// keepResolvableTools returns the tool pointers whose stored content is still present, appending a
// pointer_unresolvable drop for each one the store has collected. Tool pointers are validated here
// rather than in ValidatePointers because this is where the store is in scope.
//
// A tool pointer's Hash is the result's ROOT — the observer records store.PutResult.Root.Hash,
// and `expand` resolves it through GetRoot — and a root is not an object: chunk.RootHash digests
// the chunk list, so store.Has, which answers for objects only, is false for every root of every
// tool result the observer ever captured. Asking Has alone dropped the whole tier as unresolvable
// on the production path (V5-VERIFY §4.8). The pointer is therefore kept when its root resolves
// and every chunk the root names is still held, which is exactly what expand(hash) will need; a
// pointer that names a chunk directly is still honoured through Has.
func keepResolvableTools(ctx context.Context, in []ToolPointer, drops []DropEntry, src SourceSet) ([]ToolPointer, []DropEntry) {
	out := in[:0:0]
	present := oncePerHash(objectPresent(src.Store))
	for _, p := range in {
		if toolResultResolvable(ctx, src.Store, present, p.Hash) {
			out = append(out, p)
			continue
		}
		drops = append(drops, DropEntry{
			Kind:   dropPointerUnresolvable,
			ID:     string(p.ToolUseID),
			Detail: "object missing from the store (collected?)",
		})
	}
	return out, drops
}

// objectPresent returns the predicate this package uses to decide whether an object is really
// there, and it is finding F4-8.
//
// It used to be store.Has, which is the store's BELIEF: a yes from the in-memory chunk set is taken
// at face value and nothing is statted, so an object deleted, moved or quarantined underneath a live
// store still answered true. A checkpoint built on that keeps a pointer into bytes that are gone —
// a durable promise about content nothing can materialize — and §12.3's whole point is that a
// checkpoint must not make one. store.ObjectPresence asks the filesystem instead, at one stat per
// candidate spelling.
//
// A store that does not offer the capability falls back to Has, which is the weaker, index-only
// answer. No shipped store takes that branch — internal/store asserts *FSStore satisfies
// ObjectPresence at compile time — and it exists so a hand-written double in a test cannot turn a
// missing method into a panic.
func objectPresent(s store.Store) func(core.Hash) bool {
	if p, ok := s.(store.ObjectPresence); ok {
		return p.ObjectOnDisk
	}
	return s.Has
}

// oncePerHash memoizes present for the length of one keepResolvableTools call, so a chunk shared
// by several tool pointers — a file read twice, a test re-run whose output deduped — is statted
// once per Finalize rather than once per pointer (SP10-D1). The answer is the same point-in-time
// observation either way: a Finalize already reads each object's presence at some instant during
// the call, and nothing orders one pointer's stat against another's.
func oncePerHash(present func(core.Hash) bool) func(core.Hash) bool {
	seen := map[core.Hash]bool{}
	return func(h core.Hash) bool {
		if v, ok := seen[h]; ok {
			return v
		}
		v := present(h)
		seen[h] = v
		return v
	}
}

// toolResultResolvable reports whether h — a tool result's root, or a chunk named directly — can
// still be materialized from the store: the object itself is on disk, or the root resolves and every
// chunk it lists is on disk. A root whose chunks were collected is unresolvable even though the
// root index still remembers it, because expand(hash) reads chunks, not index entries.
//
// The two arms are asked in the cheap order, and the order cannot change the verdict because
// neither arm has a side effect: GetRoot is an in-memory index lookup, and a tool pointer names a
// ROOT, which is never an object file itself. Asking present(h) first — as this function did until
// SP10-D1 — cost every pointer two failing stats (one per candidate spelling) before the index was
// consulted, which on Windows was most of BenchmarkFinalize. A chunk named directly still gets
// its stat, one lookup later.
//
// present is objectPresent's predicate, resolved once by the caller rather than per pointer.
func toolResultResolvable(ctx context.Context, s store.Store, present func(core.Hash) bool, h core.Hash) bool {
	if root, err := s.GetRoot(ctx, h); err == nil && allChunksPresent(root.Chunks, present) {
		return true
	}
	return present(h)
}

// allChunksPresent reports whether present holds for every chunk, stopping at the first that is
// missing.
func allChunksPresent(chunks []store.ChunkRef, present func(core.Hash) bool) bool {
	for _, c := range chunks {
		if !present(c.Hash) {
			return false
		}
	}
	return true
}
