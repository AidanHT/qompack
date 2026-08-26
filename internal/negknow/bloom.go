package negknow

import (
	"context"
	"errors"
	"math/bits"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// This file is 00-ARCHITECTURE.md §3.3's replacement rule, implemented:
//
//	sketches/tried.bloom may be replaced only by negknow.RebuildBloom, whose input is
//	records/eliminations.jsonl filtered to status:"active" — never a checkpoint, never a summary,
//	never context. The rebuild writes a new file and renames; the previous file is kept as
//	tried.bloom.<seq>.bak for one generation.
//
// Both halves of that sentence are enforced by tests rather than by convention. The key iterator
// below reads visibleActive() and nothing else, and bloom_test.go greps every non-test file under
// internal/ to prove there is exactly one sketch.RebuildBloom call site and that it is this one.
// The rename, the staging, the one-generation prune and the rollback all live in
// sketch.ReplaceGenerational and paths.ReplaceBloom; this package re-implements none of it, and a
// second grep proves there is no os.Rename here to do so with.

// RebuildBloom rebuilds tried.bloom from ACTIVE RECORDS ONLY and persists it.
func (l *ledger) RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.bloom, l.health(), os.ErrClosed
	}
	return l.rebuildLocked(ctx)
}

// rebuildLocked is RebuildBloom's body for a caller already holding mu.
func (l *ledger) rebuildLocked(ctx context.Context) (*sketch.Bloom, Health, error) {
	start := l.clk.Now()
	defer func() { l.m.Hist(histRebuild).Observe(l.clk.Since(start)) }()

	if l.blind {
		// The records that would feed the rebuild are unreadable, so rebuilding would replace a
		// good on-disk cache with an empty one on the strength of a transient I/O error. This is
		// the only use of ErrBlind, and it is why the sentinel exists.
		return l.bloom, l.health(), ErrBlind
	}
	if err := ctx.Err(); err != nil {
		// The caller has already given up — an idle window that closed, an Open that ran out of
		// its 250 ms. A rebuild is a size optimization and never a correctness requirement, since
		// a stale-inclusive filter still resolves to a stale record, so the right answer to an
		// expired context is to OWE the rebuild rather than to start one. It is checked here and
		// nowhere inside the pass below: a rebuild abandoned half-way would produce a filter
		// missing keys, which is a false negative and the one direction that must never happen.
		l.pending = true
		return l.bloom, l.health(), err
	}

	vis := l.visibleActive()
	capacity, fpRate := l.cfg.Sketches.Bloom.Capacity, l.cfg.Sketches.Bloom.FPRate
	if need := 2 * len(vis); need > capacity {
		// The configured capacity is honoured whenever it is SUFFICIENT — that is what keeps a
		// normal project's filter at the Appendix A size instead of silently allocating a
		// multiple of it. Growth happens only when the key count genuinely exceeds it.
		capacity = roundUpPow2(need)
	}

	keys := func(yield func([]byte) bool) {
		for _, r := range vis {
			if !yield(r.Desc.Key()) {
				return
			}
			if !yield(r.Desc.MatchKey()) {
				return
			}
		}
	}

	nb := sketch.RebuildBloom(capacity, fpRate, keys)
	if c, f, needed := nb.ResizeTarget(); needed {
		// At most one retry, never a loop. ResizeTarget doubles, and a filter loaded exactly to
		// capacity sits at fill ~0.52 with k rounded up to 7, so one doubling drops it to ~0.31.
		nb = sketch.RebuildBloom(c, f, keys)
	}
	if _, _, needed := nb.ResizeTarget(); needed {
		// A saturated filter degrades to more false positives, each of which the record lookup
		// then resolves. That is a cost, not a correctness failure, so it is loud and continues.
		l.log.Loud("negknow: bloom saturated after resize",
			"records", len(vis), "fill", nb.FillRatio(), "fp", nb.EstimatedFPRate())
	}

	err := l.persistBloom(nb)

	l.bloom = nb
	// A rebuild whose PERSISTENCE failed still owes the write, so pending stays set and the next
	// idle tick retries it (M7). Clearing it here would drop that retry on the floor and leave the
	// on-disk filter behind the log until something else happened to schedule a rebuild.
	l.pending = err != nil
	l.m.Counter(counterBloomRebuilds).Add(1)
	return nb, l.health(), err
}

// persistBloom writes nb through the one sanctioned door.
//
// paths.WriteAtomic refuses tried.bloom outright through paths.IsProtected, and sketch.Save
// refuses it too; sketch.ReplaceGenerational is the §3.3 exception, and it owns the pre-flight
// against the surviving generation, the rename to the backup name its own pruner parses, the
// staging and swap, and the checked rollback if staging fails.
//
// The backup carries the INCOMING seq, not seq-1: paths.pruneBloomBackups keeps the HIGHEST
// sequence rather than the newest file, so a seq-1 spelling would be pruned the instant it was
// created and a staging failure at that point would leave no filter at all.
func (l *ledger) persistBloom(nb *sketch.Bloom) error {
	l.seq++
	_, err := sketch.ReplaceGenerational(filepath.Join(l.lay.Sketches, sketch.TriedBloomBase), nb, l.seq)
	if err == nil {
		return nil
	}

	// The new filter is correct in memory even though its persistence failed, so the caller keeps
	// it and is not obliged to fail. l.seq is NOT decremented: a burned sequence number costs
	// nothing, and re-using one would violate the next call's pre-flight.
	l.log.Loud("negknow: could not persist tried.bloom; the in-memory filter is still correct",
		"seq", l.seq, "err", err)
	if errors.Is(err, sketch.ErrMalformed) {
		// The pre-flight refused the sequence, which means the counter did not resume above the
		// surviving backup. Re-seed it from the disk so the next attempt can succeed.
		if seq, ok, serr := paths.HighestBloomBackupSeq(l.lay); serr == nil && ok {
			l.seq = seq
		}
	}
	return err
}

// roundUpPow2 returns the smallest power of two at or above n, and 1 for anything below it.
func roundUpPow2(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}
