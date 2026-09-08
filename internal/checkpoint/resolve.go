package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// Explicit recovery over a Reader (00-ARCHITECTURE.md §12).
//
// Reader.Latest and Reader.Chain are the SHIPPED contract, consumed by SP-11's rehydrator, SP-13's
// `why` and `qompack fsck`, and their signatures cannot grow a third return value. But both of
// them lose something a recovering caller needs:
//
//   - Latest steps over a checkpoint that fails verification and returns the parent. That is the
//     right behaviour, and it is INDISTINGUISHABLE at the call site from a clean read: the caller
//     is handed an older checkpoint and never learns that it is older, or why.
//   - Chain refuses a broken ancestry outright. That is the right answer for fsck, whose job is
//     to report the break; it is the wrong answer for a rehydrator, which can legitimately replay
//     the usable SUFFIX so long as it is told that is what it got.
//
// These two functions are the reporting forms. They add no new reading rules — every verification
// decision is still the Reader's — and they never fabricate: what cannot be read is reported
// missing, not synthesized.

// Resolution is what ResolveLatest found: the checkpoint to use, and, when it is not the newest
// one the store records, what it stepped over to get there.
type Resolution struct {
	Checkpoint Checkpoint
	Ref        Ref
	// FellBack is true when the newest recorded checkpoint was not the one returned.
	FellBack bool
	// Skipped lists the sequence numbers that did not verify, newest first.
	Skipped []core.CheckpointSeq
	// Reason is a one-line explanation, non-empty exactly when FellBack is true.
	Reason string
}

// ResolveLatest returns the newest verifying checkpoint for s, together with an explicit account
// of any fallback it had to take.
//
// It reaches the same answer as Reader.Latest — this is deliberately not a second verification
// policy — and it establishes the FellBack account by asking the Reader what the newest recorded
// sequence numbers are (List) and which of them verify (Verify). A store with nothing usable is
// core.ErrNotFound, never an empty Checkpoint reported as success.
func ResolveLatest(ctx context.Context, r Reader, s core.SessionID) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if r == nil {
		return Resolution{}, fmt.Errorf("checkpoint: resolve latest: no reader: %w", core.ErrDegraded)
	}
	cp, ref, err := r.Latest(ctx, s)
	if err != nil {
		return Resolution{}, err
	}

	refs, err := r.List(ctx)
	if err != nil {
		// The checkpoint is usable; only the account of what was skipped is unavailable. Report
		// the checkpoint rather than failing a recovery over a missing explanation.
		return Resolution{Checkpoint: cp, Ref: ref}, nil //nolint:nilerr // the resolution stands; only its account is unavailable
	}
	bad, verifyErr := r.Verify(ctx)
	if verifyErr != nil {
		return Resolution{Checkpoint: cp, Ref: ref}, nil //nolint:nilerr // as above
	}
	badSet := make(map[core.CheckpointSeq]bool, len(bad))
	for _, b := range bad {
		badSet[b] = true
	}

	res := Resolution{Checkpoint: cp, Ref: ref}
	for i := len(refs) - 1; i >= 0; i-- {
		if refs[i].Seq <= ref.Seq {
			break
		}
		if badSet[refs[i].Seq] {
			res.Skipped = append(res.Skipped, refs[i].Seq)
		}
	}
	if len(res.Skipped) > 0 {
		res.FellBack = true
		res.Reason = fmt.Sprintf(
			"checkpoint %04d does not verify; rolled back to %04d",
			int(res.Skipped[0]), int(ref.Seq))
	}
	return res, nil
}

// ChainResolution is what ResolveChain found: the longest usable run of the ancestry, oldest
// first, and — when the walk could not reach the root — where it stopped and why.
type ChainResolution struct {
	Checkpoints []Checkpoint
	// Truncated is true when Checkpoints is a SUFFIX of the real ancestry rather than all of it.
	Truncated bool
	// MissingFrom is the sequence number the walk could not read, when one is known.
	MissingFrom core.CheckpointSeq
	// Reason is a one-line explanation, non-empty exactly when Truncated is true.
	Reason string
}

// ResolveChain walks seq's ancestry and returns the longest usable suffix.
//
// A clean ancestry is Reader.Chain's own answer, unchanged. A broken one — a corrupt ancestor, a
// missing artifact, an unparseable or cyclic parent link, or the depth cap — is reported as a
// truncated resolution with a reason, so a rehydrator replays what exists and KNOWS the older end
// is absent. The suffix is found by re-asking the Reader from each successively newer link, which
// keeps every verification decision the Reader's own.
//
// It never fabricates a link. A checkpoint that cannot be read is not in the result.
func ResolveChain(ctx context.Context, r Reader, seq core.CheckpointSeq) (ChainResolution, error) {
	if err := ctx.Err(); err != nil {
		return ChainResolution{}, err
	}
	if r == nil {
		return ChainResolution{}, fmt.Errorf("checkpoint: resolve chain: no reader: %w", core.ErrDegraded)
	}

	chain, err := r.Chain(ctx, seq)
	switch {
	case err == nil:
		return ChainResolution{Checkpoints: chain}, nil
	case errors.Is(err, ErrChainTruncated):
		// A long project, not a broken one: Chain already returned the newest window, oldest
		// first, and said so.
		return ChainResolution{
			Checkpoints: chain, Truncated: true,
			Reason: "ancestry is longer than the reader's depth cap; the oldest links are absent",
		}, nil
	case !errors.Is(err, core.ErrContract) && !errors.Is(err, core.ErrNotFound):
		return ChainResolution{}, err
	}

	// The ancestry is broken. Find the newest link whose own ancestry IS whole by walking forward
	// from seq's parent: the first sub-chain that reads cleanly is the usable tail, and everything
	// between it and seq is replayed from the single checkpoints we can still read individually.
	cur, _, getErr := r.Get(ctx, seq)
	if getErr != nil {
		return ChainResolution{}, err
	}
	out := []Checkpoint{cur}
	missing := core.CheckpointSeq(0)
	for {
		if cerr := ctx.Err(); cerr != nil {
			return ChainResolution{}, cerr
		}
		if cur.Parent == "" {
			break
		}
		parent, perr := seqFromFilename(cur.Parent)
		if perr != nil {
			break
		}
		missing = parent
		p, _, gerr := r.Get(ctx, parent)
		if gerr != nil {
			break
		}
		if p.Seq >= cur.Seq {
			break // a cycle; the Reader's own rule, applied here so the walk terminates
		}
		out = append(out, p)
		cur, missing = p, 0
	}
	reverse(out)
	return ChainResolution{
		Checkpoints: out, Truncated: true, MissingFrom: missing,
		Reason: fmt.Sprintf("ancestry breaks at checkpoint %04d: %v", int(missing), err),
	}, nil
}

// reverse flips a slice in place. The chain is accumulated newest-first and handed back
// oldest-first, the order a rehydrator replays it in.
func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
