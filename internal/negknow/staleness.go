package negknow

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// This file is §8.3's staleness machinery: the mechanism that lets negative knowledge expire.
//
// An elimination is a CONDITIONAL fact — "widening the pool timeout doesn't work given pgbouncer
// 1.18" — so upgrading pgbouncer voids it, and a false already_tried that blocks a now-viable
// approach inverts the whole feature from asset to liability. §12 rates that High. Every record
// therefore carries depends_on hashes, one store.ChangedSince call per refresh compares them
// against the current file versions, and a changed dependency flips the record to stale with a
// per-dependency reason.
//
// Two properties are load-bearing and are asserted by staleness_test.go.
//
// RefreshStaleness is idempotent and re-runnable. SP05-D1 means the file-version history this
// reads may be MISSING an edit — a drain aborted by idle-budget expiry consumes the line it
// interrupted — so a refresh can produce a missed flip rather than a false one. A missed flip is
// the §12 High-severity direction, and re-running the refresh is what recovers from it; nothing
// here may assume it saw every change.
//
// The comparison stays re-derivable. SP04-D2/D3 leave canonicalization unstable, so a hash minted
// before that fix and one minted after are not comparable, and this package must never bake a
// canonical-form hash into a durable identity it cannot recompute. RefreshStaleness compares only
// what store.ChangedSince compares — the dep hash on the record against the newest recorded
// version — and mints no generation field of its own, because Record's json tags are frozen and
// two byte-frozen fixtures transcribe them.

// MarkStale flips every known, currently-active id to StatusStale.
//
// Unknown ids and already-stale ones are skipped and counted rather than reported: MarkStale is
// called from a refresh that may legitimately name a record another session has already flipped.
// IDs are processed in the order given, and the first append error is returned with the appends
// that already succeeded left in place.
func (l *ledger) MarkStale(ctx context.Context, ids []string, because []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.markStaleLocked(ctx, ids, because)
	return err
}

// markStaleLocked is MarkStale's body for a caller already holding mu. It reports which ids it
// actually flipped, which is what RefreshStaleness returns.
func (l *ledger) markStaleLocked(ctx context.Context, ids []string, because []string) ([]string, error) {
	if l.closed {
		return nil, os.ErrClosed
	}

	var (
		flipped  []string
		firstErr error
	)
	for _, id := range ids {
		i, ok := l.byID[id]
		if !ok || l.recs[i].Status != StatusActive {
			l.m.Counter(counterStaleSkipped).Add(1)
			continue
		}
		ts := core.UnixMilli(l.clk.Now().UnixMilli())
		if l.f == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: negknow: the elimination log is not appendable", core.ErrDegraded)
			}
			continue
		}
		// The flip is a control line, not a rewrite: records/eliminations.jsonl is append-only
		// (§7.4), and replayLog applies an op:"stale" line by REPLACING the record's staleness
		// fields, which is what makes a re-flip idempotent across restarts too.
		if err := appendLine(l.f, logControl{Op: opStale, ID: id, TS: int64(ts), Because: because}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		l.recs[i].Status = StatusStale
		l.recs[i].StaleSince = ts
		l.recs[i].StaleBecause = because
		flipped = append(flipped, id)
	}

	if len(flipped) > 0 {
		l.m.Counter(counterStaleFlipped).Add(int64(len(flipped)))
		switch l.elim.RebuildOnStale {
		case rebuildImmediate:
			if _, _, err := l.rebuildLocked(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		case rebuildNextIdle:
			l.pending = true
		}
	}
	return flipped, firstErr
}

// depKey identifies one dependency for deduplication and for the owners index: the normalized
// path plus the hash the record was recorded against.
func depKey(d core.Dep) string { return paths.Key(d.Path) + string(rune(fieldSep)) + d.Hash.String() }

// RefreshStaleness compares every active record's depends_on hashes against s's current file
// versions and flips the affected records to stale, returning the flipped ids sorted ascending.
//
// It makes exactly ONE ChangedSince call, and it makes it outside the ledger's lock: a store that
// is slow, or that has been handed a deadline, must not be able to stall a concurrent Query.
func (l *ledger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	start := l.clk.Now()
	defer func() { l.m.Hist(histRefresh).Observe(l.clk.Since(start)) }()

	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return nil, os.ErrClosed
	}
	// Every ACTIVE record, all scopes and all sessions: a session-scoped record from another
	// session cannot be queried here, but keeping the log truthful about it is free.
	var (
		deps   []core.Dep
		owners = map[string][]string{}
		seen   = map[string]struct{}{}
	)
	for _, r := range l.recs {
		if r.Status != StatusActive {
			continue
		}
		for _, d := range r.DependsOn {
			k := depKey(d)
			owners[k] = append(owners[k], r.ID)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			deps = append(deps, d)
		}
	}
	l.mu.RUnlock()

	// Deterministic order, so the argument this hands the store is a function of the ledger's
	// contents and nothing else.
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].Path != deps[j].Path {
			return deps[i].Path < deps[j].Path
		}
		return deps[i].Hash.String() < deps[j].Hash.String()
	})

	changed, err := s.ChangedSince(ctx, deps)
	if err != nil {
		// Failing toward doing nothing: the ledger stays usable and no record is flipped on
		// evidence we could not read.
		l.log.Warn("negknow: could not compare dependency hashes; nothing flipped", "err", err)
		return nil, err
	}

	reasons := map[string][]string{}
	for _, d := range changed {
		// d.Hash is the hash the record was recorded AGAINST — the value it changed FROM —
		// because ChangedSince returns the INPUT deps, not the current ones (§5.8). Short()
		// carries no prefix (§4), so the "sha256:" here is written by this format string.
		because := fmt.Sprintf("%s: dependency hash changed from sha256:%s", d.Path, d.Hash.Short())
		for _, id := range owners[depKey(d)] {
			reasons[id] = append(reasons[id], because)
		}
	}
	if len(reasons) == 0 {
		return nil, nil
	}

	var flipped []string
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, g := range groupStaleReasons(reasons) {
		got, merr := l.markStaleLocked(ctx, g.IDs, g.Because)
		flipped = append(flipped, got...)
		if merr != nil {
			sort.Strings(flipped)
			return flipped, merr
		}
	}
	sort.Strings(flipped)
	return flipped, nil
}

// staleGroup is one MarkStale call: the records that share a because, and the because itself.
type staleGroup struct {
	IDs     []string
	Because []string
}

// groupStaleReasons turns per-record reason lists into the sequence of MarkStale calls
// RefreshStaleness makes.
//
// §5.10 gives MarkStale a SINGLE because for a whole batch, so per-record precision has to be
// recovered by grouping: each record's reasons are sorted and deduplicated, records sharing the
// resulting text are batched together, and the batches are emitted in ascending order of that
// text with ids sorted inside each. The result is a total order, so two refreshes over the same
// change produce byte-identical control lines in the same sequence.
func groupStaleReasons(reasons map[string][]string) []staleGroup {
	byText := map[string][]string{}
	because := map[string][]string{}
	for id, rs := range reasons {
		sorted := append([]string(nil), rs...)
		sort.Strings(sorted)
		sorted = dedupSorted(sorted)
		text := strings.Join(sorted, "\n")
		byText[text] = append(byText[text], id)
		because[text] = sorted
	}

	texts := make([]string, 0, len(byText))
	for text := range byText {
		texts = append(texts, text)
	}
	sort.Strings(texts)

	out := make([]staleGroup, 0, len(texts))
	for _, text := range texts {
		ids := byText[text]
		sort.Strings(ids)
		out = append(out, staleGroup{IDs: ids, Because: because[text]})
	}
	return out
}

// dedupSorted removes adjacent duplicates from an already-sorted slice, in place.
func dedupSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// NeedsRebuild reports whether a tried.bloom rebuild is owed.
//
// It is false under blind mode and after Close, because in both cases the rebuild would be
// refused if it were attempted, and an idle controller should not be asked to schedule work that
// cannot run.
func (l *ledger) NeedsRebuild() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.pending && !l.blind && !l.closed
}

// MaintenanceTask returns the (name, priority, fn) triple SP-12's idle controller registers. Its
// shape matches daemon.IdleController.Register exactly, so the wiring is one line with no
// decisions left open.
//
// fn refreshes staleness and then rebuilds the filter if one is owed. Under blind mode or after
// Close it does neither and reports no error: an idle window is not the place to retry an I/O
// failure that the next Open will resolve properly.
func (l *ledger) MaintenanceTask(s store.Store) (string, int, func(context.Context) error) {
	return maintenanceTaskName, maintenancePriority, func(ctx context.Context) error {
		l.mu.RLock()
		skip := l.blind || l.closed
		l.mu.RUnlock()
		if skip {
			return nil
		}
		if _, err := l.RefreshStaleness(ctx, s); err != nil {
			return err
		}
		if !l.NeedsRebuild() {
			return nil
		}
		_, _, err := l.RebuildBloom(ctx)
		return err
	}
}
