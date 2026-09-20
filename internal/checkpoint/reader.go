package checkpoint

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// maxChainDepth is how many links Chain walks back before it stops. It is a WINDOW, not a health
// check: the strictly-decreasing parent check below is what proves the walk terminates, and this
// bounds how many artifacts one call reads and holds in memory.
//
// Reaching it is not an error. Finalize opens every successor draft with parent = seq, so a
// project's checkpoints form one unbroken chain for its whole life and every PreCompact and every
// cadence tick mints a link; passing this cap is what using Qompack for a long time looks like.
// See ErrChainTruncated.
const maxChainDepth = 1024 //nomagic:allow chain-walk window, a Reader bound; not an Appendix-C config default

// ErrChainTruncated reports that Chain stopped at maxChainDepth links and returned the newest
// maxChainDepth of them instead of the whole ancestry. The returned slice is complete and usable
// as far as it goes and is still ordered oldest-first; what is missing is the OLDEST end.
//
// It is deliberately NOT core.ErrContract. A corrupt artifact, an unreadable manifest and a cycle
// are all ErrContract because something is wrong with the store; a chain longer than the window is
// a store where nothing is wrong at all, and a caller that could not tell those apart would tell a
// user their session is broken because their project is old.
//
// The oldest end is the right end to drop for the first consumer: a rehydrator replays the chain
// oldest-first precisely so a later checkpoint's view of a decision overwrites an earlier one's, so
// the links it loses are the ones it would have overwritten. A caller that needs to go further back
// calls Chain again from the oldest checkpoint it received.
var ErrChainTruncated = errors.New("checkpoint: chain truncated at the depth cap")

// fileReader is the Reader over a project's checkpoints directory: the manifest is the index and
// every artifact is verified against it before it is decoded (00-ARCHITECTURE.md §5.14, §12).
//
// It holds no cache. A checkpoint artifact is immutable once written (paths.CreateNew chmods it
// 0444), but the manifest grows underneath a long-lived reader, so caching the index would make
// Latest blind to the checkpoint the writer appended a moment ago — which is exactly the
// checkpoint a rehydration wants.
type fileReader struct {
	l   paths.Layout
	log logging.Logger
	m   obs.Registry

	// integrityMu guards integrityNoted.
	integrityMu sync.Mutex
	// integrityNoted names each artifact this reader has already announced as orphaned, missing or
	// mismatched, so a long-lived reader Louds each one ONCE rather than on every List. A reader is
	// cheap and usually short-lived, so this bounds the common case; the set is per reader, never
	// package-level, because a process-wide memo would silently suppress a second project's report.
	integrityNoted map[string]bool
}

var _ Reader = (*fileReader)(nil)

// OpenReader returns a Reader rooted at root, backing rehydration (L5), the `why` retrieval tool
// (L6) and `qompack fsck` (00-ARCHITECTURE.md §5.14). Constructing it touches no disk: a project
// with no checkpoint yet is not an error, it is a Latest that reports core.ErrNotFound.
//
// EVERY Ref this Reader returns leaves Tokens and Frontier ZERO. Both are writer-only fields:
// Finalize is the only thing that prices an artifact against the token estimator and the only
// thing that knows what turn the encoding frontier reached, and neither value is recorded in
// checkpoints/MANIFEST.jsonl, so there is nothing for a reader to recover them from. A consumer
// may use the writer's Ref only while it remains in memory; a durable reader frontier requires a
// versioned, immutable seq-to-frontier record. Treating a Reader's zero as "frontier 0" would
// read "nothing encoded yet" out of a checkpoint that encoded plenty.
//
// It does NOT call SetObservers. OpenWriter owns that one-time package-level wiring (see obs.go);
// a reader installing its own observers would silently redirect the receiver-less functions'
// counters away from the writer that set them.
func OpenReader(root string, log logging.Logger, m obs.Registry) (Reader, error) {
	if log == nil {
		log = logging.Nop()
	}
	if m == nil {
		m = nopRegistry{}
	}
	return &fileReader{l: paths.Of(root), log: log, m: m}, nil
}

// List returns a Ref for every checkpoint the manifest records, ascending by Seq. Ref.Seq,
// Ref.SHA256, Ref.Bytes and Ref.Created come straight from the manifest entry and Ref.Path from
// paths.CheckpointPath; Ref.Tokens and Ref.Frontier are writer-only and stay zero (see
// OpenReader).
//
// It does NOT re-hash the artifacts. List is the index; Get and Verify are the ones that re-hash,
// and making a listing pay for a full re-hash would put fsck's cost on every caller that only
// wanted to know what exists.
//
// It DOES sweep the directory for the two integrity defects a re-hash cannot find, and that is
// finding F4-9: a MANIFEST entry whose artifact is missing, and an artifact present with no entry
// of its own (the state finalize.go logs when CreateNew succeeded and AppendManifest did not).
// Neither reached any surface before — not LOUD.log, not status, not self-test, not a DropEntry —
// because nothing in a recovery path ever looked. The sweep is one directory read and one stat per
// entry, next to a re-hash of every artifact's bytes.
//
// What List RETURNS is unchanged: every entry the manifest records, defects included. List is the
// index, and an index that silently dropped rows would make "what does this project claim to have"
// unanswerable. The refusal §12.3 asks for already lives where the checkpoint is USED — Get, Latest
// and Chain each report core.ErrContract and step over to the parent — and this adds the Loud that
// was missing beside it.
func (r *fileReader) List(ctx context.Context) ([]Ref, error) {
	entries, err := r.manifest()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	refs := make([]Ref, 0, len(entries))
	for _, e := range entries {
		ref, err := r.ref(e)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	r.sweepIntegrity(refs)
	return refs, nil
}

// sweepIntegrity reports the two defects a digest check cannot see: an entry whose artifact is gone,
// and an artifact no entry claims. Each is announced once per reader and counted; nothing is moved,
// repaired or deleted (§12.3 is explicit that a checkpoint defect is refused, never repaired here).
//
// A directory that cannot be read is not itself reported: List has already succeeded in reading the
// manifest through it, so an unreadable listing is a race with a concurrent writer rather than a
// durability defect, and announcing it would be announcing the sweep's own failure as the store's.
func (r *fileReader) sweepIntegrity(refs []Ref) {
	claimed := make(map[string]bool, len(refs))
	for _, ref := range refs {
		claimed[filepath.Base(ref.Path)] = true
		if _, err := os.Stat(paths.Long(ref.Path)); err != nil {
			r.noteIntegrity(filepath.Base(ref.Path),
				"checkpoint artifact missing; the manifest still claims it",
				"seq", ref.Seq, "path", ref.Path, "sha256", ref.SHA256.String())
		}
	}

	entries, err := os.ReadDir(paths.Long(r.l.Checkpoints))
	if err != nil {
		return
	}
	manifestName := filepath.Base(paths.ManifestPath(r.l))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == manifestName || filepath.Ext(name) != ".json" || claimed[name] {
			continue
		}
		r.noteIntegrity(name,
			"checkpoint artifact present with no MANIFEST line; it is an orphan and is not used",
			"path", filepath.Join(r.l.Checkpoints, name))
	}
}

// noteIntegrity Louds and counts one integrity defect, once per artifact per reader.
func (r *fileReader) noteIntegrity(artifact, msg string, kv ...any) {
	r.integrityMu.Lock()
	if r.integrityNoted == nil {
		r.integrityNoted = map[string]bool{}
	}
	already := r.integrityNoted[artifact]
	r.integrityNoted[artifact] = true
	r.integrityMu.Unlock()
	if already {
		return
	}
	r.m.Counter(metricManifestMismatch).Add(1)
	r.log.Loud(msg, append([]any{"artifact", artifact}, kv...)...)
}

// Get returns the checkpoint with sequence number seq, after re-hashing its bytes against the
// digest the manifest recorded.
//
// The two absences answer differently, and the difference is the whole point. A seq the manifest
// never recorded is core.ErrNotFound: nothing was promised, so nothing is broken. A seq the
// manifest DID record whose file is missing, unreadable, or hashes differently is
// core.ErrContract, logged Loud and counted, because the manifest is the thing that promised the
// file exists and verifies (§12).
func (r *fileReader) Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error) {
	entries, err := r.manifest()
	if err != nil {
		return Checkpoint{}, Ref{}, err
	}
	e, ok := entryFor(entries, seq)
	if !ok {
		return Checkpoint{}, Ref{}, fmt.Errorf("checkpoint %04d: %w", int(seq), core.ErrNotFound)
	}
	return r.load(ctx, e)
}

// Latest returns the newest verifying checkpoint of session s, walking the manifest descending.
//
// Two behaviours are deliberate. A checkpoint that fails verification does not end the walk: it
// is reported Loud, counted, and stepped over to its parent — §12's "refuse to use the affected
// checkpoint, fall back to its parent". And a session with no checkpoint of its own inherits the
// project's newest verifying checkpoint from any session, because a resumed session legitimately
// continues the project's chain; the alternative is to hand a resumed session an empty context it
// had no way to ask for.
//
// It never degrades the mode itself. It returns the error and the daemon wiring is what calls
// contract.Monitor.Degrade — this package cannot import contract, and a read helper that could
// flip a process-wide mode would be reachable from `qompack fsck`.
func (r *fileReader) Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error) {
	entries, err := r.manifest()
	if err != nil {
		return Checkpoint{}, Ref{}, err
	}

	var (
		newest    Checkpoint
		newestRef Ref
		haveAny   bool
	)
	for i := len(entries) - 1; i >= 0; i-- {
		c, ref, err := r.load(ctx, entries[i])
		switch {
		case err == nil:
		case errors.Is(err, core.ErrContract):
			continue // step over to the parent
		default:
			return Checkpoint{}, Ref{}, err
		}
		if c.Session == s {
			return c, ref, nil
		}
		if !haveAny {
			newest, newestRef, haveAny = c, ref, true
		}
	}
	if haveAny {
		return newest, newestRef, nil
	}
	return Checkpoint{}, Ref{}, fmt.Errorf(
		"checkpoint: no verifiable checkpoint for session %q: %w", string(s), core.ErrNotFound)
}

// Chain returns seq and every ancestor it descends from, OLDEST FIRST — the order a rehydrator
// replays them in, so that a later checkpoint's view of a decision overwrites an earlier one's
// rather than the other way round.
//
// A parent whose sequence number is not strictly less than its child's is a cycle and aborts with
// core.ErrContract instead of walking forever. That check is also the termination proof: cur
// strictly decreases through a positive int, so the walk always ends.
//
// Past maxChainDepth links Chain stops and returns the newest maxChainDepth checkpoints together
// with ErrChainTruncated — a non-empty result AND a non-nil error, on purpose. A long chain is a
// long project, not a broken store, so it is reported as a different error class from every
// corruption this reader can find (see ErrChainTruncated).
func (r *fileReader) Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error) {
	entries, err := r.manifest()
	if err != nil {
		return nil, err
	}

	var out []Checkpoint
	cur := seq
	for depth := 0; ; depth++ {
		if depth >= maxChainDepth {
			slices.Reverse(out) // the window is still handed back oldest-first
			return out, fmt.Errorf("checkpoint %04d: walked back %d links: %w",
				int(seq), maxChainDepth, ErrChainTruncated)
		}
		e, ok := entryFor(entries, cur)
		if !ok {
			return nil, fmt.Errorf("checkpoint %04d: %w", int(cur), core.ErrNotFound)
		}
		c, _, err := r.load(ctx, e)
		if err != nil {
			return nil, err
		}
		out = append(out, c)

		if c.Parent == "" {
			break
		}
		parent, err := seqFromFilename(c.Parent)
		if err != nil {
			return nil, fmt.Errorf("checkpoint %04d: parent %q: %w", int(cur), c.Parent, core.ErrContract)
		}
		if parent >= cur {
			return nil, fmt.Errorf("checkpoint %04d: parent %04d does not precede it: %w",
				int(cur), int(parent), core.ErrContract)
		}
		cur = parent
	}

	slices.Reverse(out)
	return out, nil
}

// Verify re-hashes every artifact the manifest records and returns the sequence numbers that do
// not match, ascending. It is what `qompack fsck` reports.
//
// It returns an EMPTY slice, never nil, on a clean store: nil marshals to JSON null, which is a
// different claim from "nothing mismatched".
//
// It used to be deliberately quiet — no Loud, no counter — on the grounds that it returns the
// mismatches to a caller whose job is to report them. Finding F4-9 is what overturned that: the
// callers that report are `qompack fsck`, which an operator has to run, and ResolveLatest, which
// reports into a rehydration's own account and nowhere durable. So a MANIFEST digest mismatch
// existed on disk with §12.3's "Loud, refuse to use the affected checkpoint" half missing, and
// test/fault found nothing naming it on any surface.
//
// The Loud is once per ARTIFACT per reader, not once per pass, so the repeated runs a daemon makes
// do not fill LOUD.log with one repeated fact, and fsck's own output is not duplicated on every
// invocation. Nothing is repaired, moved or deleted here.
func (r *fileReader) Verify(ctx context.Context) ([]core.CheckpointSeq, error) {
	entries, err := r.manifest()
	if err != nil {
		return nil, err
	}

	bad := make([]core.CheckpointSeq, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ref, err := r.ref(e)
		if err != nil {
			return nil, err
		}
		raw, readErr := os.ReadFile(paths.Long(ref.Path))
		switch {
		case readErr != nil:
			// The missing-artifact case List's sweep also names. noteIntegrity keys on the artifact,
			// so whichever runs first is the one that speaks.
			r.noteIntegrity(filepath.Base(ref.Path),
				"checkpoint artifact unreadable; the manifest still claims it",
				"seq", e.Seq, "path", ref.Path, "detail", readErr.Error())
			bad = append(bad, e.Seq)
		case core.Hash(sha256.Sum256(raw)) != ref.SHA256:
			r.noteIntegrity(filepath.Base(ref.Path),
				"checkpoint artifact does not match its MANIFEST digest; the checkpoint is refused",
				"seq", e.Seq, "expected", ref.SHA256.String(),
				"observed", core.Hash(sha256.Sum256(raw)).String())
			bad = append(bad, e.Seq)
		}
	}
	return bad, nil
}

// manifest reads checkpoints/MANIFEST.jsonl through its owner, paths.ReadManifest, and returns
// one entry per seq ascending.
//
// A malformed line makes paths.ReadManifest hard-fail, and this surfaces that failure wrapped in
// core.ErrContract rather than skipping the line: the manifest is the index every other read
// depends on, so a skip would HIDE a checkpoint instead of reporting a broken one.
//
// Duplicate seqs resolve last-line-wins. The file is append-only, so a seq recorded twice is the
// same artifact re-recorded, and the most recent line is the one that describes the bytes now on
// disk.
func (r *fileReader) manifest() ([]paths.ManifestEntry, error) {
	raw, err := paths.ReadManifest(r.l)
	if err != nil {
		r.m.Counter(metricManifestBadline).Add(1)
		r.log.Loud("checkpoint manifest unreadable",
			"path", paths.ManifestPath(r.l), "detail", err.Error())
		return nil, fmt.Errorf("checkpoint: manifest: %w", core.ErrContract)
	}

	bySeq := make(map[core.CheckpointSeq]paths.ManifestEntry, len(raw))
	for _, e := range raw {
		bySeq[e.Seq] = e
	}
	entries := make([]paths.ManifestEntry, 0, len(bySeq))
	for _, e := range bySeq {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b paths.ManifestEntry) int { return cmp.Compare(a.Seq, b.Seq) })
	return entries, nil
}

// entryFor returns the manifest entry for seq. manifest() has already collapsed duplicates, so at
// most one entry can match.
func entryFor(entries []paths.ManifestEntry, seq core.CheckpointSeq) (paths.ManifestEntry, bool) {
	for _, e := range entries {
		if e.Seq == seq {
			return e, true
		}
	}
	return paths.ManifestEntry{}, false
}

// ref converts one manifest entry into the Ref a Reader hands back. Ref.Path is derived from the
// layout rather than stored, because the manifest records a seq and internal/paths owns the
// %04d.json spelling; Ref.Tokens and Ref.Frontier stay zero because they are writer-only (see
// OpenReader).
func (r *fileReader) ref(e paths.ManifestEntry) (Ref, error) {
	h, err := core.ParseHash(e.SHA256)
	if err != nil {
		r.m.Counter(metricManifestBadline).Add(1)
		r.log.Loud("checkpoint manifest digest unparseable", "seq", e.Seq, "sha256", e.SHA256)
		return Ref{}, fmt.Errorf("checkpoint %04d: manifest digest: %w", int(e.Seq), core.ErrContract)
	}
	return Ref{
		Seq:     e.Seq,
		Path:    paths.CheckpointPath(r.l, e.Seq),
		SHA256:  h,
		Bytes:   e.Bytes,
		Created: e.Created,
	}, nil
}

// load reads, verifies and decodes one manifest entry's artifact. Unmarshal runs Migrate first,
// so a checkpoint written by an older schema version is read at the current one.
func (r *fileReader) load(ctx context.Context, e paths.ManifestEntry) (Checkpoint, Ref, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, Ref{}, err
	}
	ref, err := r.ref(e)
	if err != nil {
		return Checkpoint{}, Ref{}, err
	}
	raw, err := r.verified(e.Seq, ref)
	if err != nil {
		return Checkpoint{}, Ref{}, err
	}
	c, err := Unmarshal(raw)
	if err != nil {
		return Checkpoint{}, Ref{}, err
	}
	return c, ref, nil
}

// verified reads the artifact at ref.Path and re-hashes it against the digest the manifest
// recorded. A missing file and a changed byte are the same failure — the manifest's promise is
// broken either way — so both are reported through the same Loud and counter (§12).
func (r *fileReader) verified(seq core.CheckpointSeq, ref Ref) ([]byte, error) {
	raw, err := os.ReadFile(paths.Long(ref.Path))
	if err != nil {
		r.mismatch(seq, ref.SHA256.String(), "unreadable: "+err.Error())
		return nil, fmt.Errorf("checkpoint %04d: %w", int(seq), core.ErrContract)
	}
	if got := core.Hash(sha256.Sum256(raw)); got != ref.SHA256 {
		r.mismatch(seq, ref.SHA256.String(), got.String())
		return nil, fmt.Errorf("checkpoint %04d: %w", int(seq), core.ErrContract)
	}
	return raw, nil
}

// mismatch reports one artifact that does not match the manifest. It is Loud rather than Error
// because a checkpoint that no longer verifies is a durability contract violation, and §12
// requires those to be never silent.
func (r *fileReader) mismatch(seq core.CheckpointSeq, want, got string) {
	r.m.Counter(metricManifestMismatch).Add(1)
	r.log.Loud("checkpoint manifest mismatch", "seq", seq, "expected", want, "observed", got)
}
