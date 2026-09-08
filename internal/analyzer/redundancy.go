package analyzer

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The two bounds that keep one DetectRedundancy pass a function of the session rather than of the
// whole store.
//
// They are deliberately equal to internal/store/search.go's own maxCandidates. That is not a
// coincidence to be tidied away later: the discovery phase asks Search for this many hits and the
// store clamps the request to its own cap, so writing a larger number here would state a coverage
// this package cannot actually obtain.
const (
	// redundancyScanHits is how many recent tool uses the discovery phase asks Search for. The
	// store clamps it; see scanToolUses for what that clamp costs and how the second phase repairs
	// most of it.
	redundancyScanHits = 512
	// maxNearDupComparisons bounds the pair scan at this many records. The scan is quadratic in
	// the record count, and a report that took longer than the compaction it informs would be
	// worse than a smaller one that says it is smaller.
	maxNearDupComparisons = 512
)

// DetectRedundancy scans session sess in s for two kinds of waste: tool results a later tool use
// on the same path superseded, and tool results that are near-duplicates of one another under the
// configured MinHash Jaccard threshold (00-ARCHITECTURE.md §5.12, §8.1).
//
// It reads the threshold from config.Defaults(). §5.12 freezes this function's three-argument
// shape — analyzertest binds it, test/integration calls it — so there is nowhere in the signature
// to put a caller's configuration, and DetectRedundancyWithConfig exists for the composition root
// that actually holds one. A daemon running with a non-default
// store.canonicalize.minhash.nearDupThreshold must call that one; this entry point answers with
// the shipped default and says so rather than pretending to have consulted a config it was never
// handed.
//
// See DetectRedundancyWithConfig for the qualification each half of the report carries.
func DetectRedundancy(ctx context.Context, s store.Store, sess core.SessionID) (RedundancyReport, error) {
	return DetectRedundancyWithConfig(ctx, s, sess, config.Defaults())
}

// DetectRedundancyWithConfig is DetectRedundancy with the near-duplicate threshold supplied by the
// caller (store.canonicalize.minhash.nearDupThreshold, §8.1).
//
// # It is READ-ONLY
//
// It reports, and it marks, drops and rewrites nothing. store.MarkSuperseded is the caller's call
// to make, and keeping the decision on the caller's side is what lets the same scan run from
// /qompack:status, from the idle scheduler and from a replay harness without any of the three
// mutating an append-only index the others are reading. analyzertest asserts this indirectly and
// exactly: two consecutive scans of an unchanged session must agree, which a scan that marked its
// own findings would fail on the second pass.
//
// # Superseded is EXACT, and may be asserted as such
//
// A tool use is reported superseded when either of two exact facts holds:
//
//  1. a strictly LATER tool use of this session names the same normalized path — the §5.12
//     definition, decided by normalizedPath over the recorded Path and by the total order (TS,
//     Turn, ID) over the records themselves; or
//  2. the store has already recorded store.StatusSuperseded on the record.
//
// Neither is an estimate, neither involves a sketch, and both are recomputable from the index by
// anyone who doubts the answer. The claim's SCOPE, however, is exactly the record set the scan
// observed — see scanToolUses — and it is a claim about the recorded history, not about the world:
// SP05-D1 means an edit the observer never saw is an edit this cannot know about, so a record NOT
// listed here is not thereby established to be current.
//
// # NearDups are CANDIDATES PENDING EXACT VERIFICATION
//
// A NearDups entry means one thing only: two MinHash signatures agreed on at least the configured
// fraction of their permutations. That is a similarity ESTIMATE over a sampled shingle set. It
// establishes neither equivalence nor an exact delta:
//
//   - Two results reported as near-duplicates may differ in the byte that matters. The whole point
//     of §8.1 item 1 is the case "same test suite, one new failure" — a pair that is 0.99 similar
//     and materially different — and internal/analyzer/testdata/diagnostics holds a committed
//     counterexample pair for exactly this reason (gate M5-G15-B).
//   - MinHash is a randomized estimator: signature agreement is an unbiased estimate of Jaccard
//     similarity, not a measurement of it, so a pair below the threshold may be more similar than
//     a pair above it.
//   - Jaccard similarity over shingles is not a delta. Nothing here computes what would have to
//     change to turn one result into the other, so no caller may size a saving from this report.
//
// A caller that needs equivalence has to verify it — comparing content roots is exact and cheap —
// and a caller that needs a delta has to compute one. Neither may be inferred from an entry here,
// and dropping or rewriting a result on the strength of one is the failure this wording exists to
// prevent.
//
// # Disk deduplication is not context reduction
//
// Nothing in this report claims delivered context has shrunk. Two near-duplicate results that
// share chunks cost the store almost nothing twice over and cost the MODEL the full price twice
// over, so a saving counted here would be counted in the wrong currency (plan §2).
//
// # Determinism and degradation
//
// Every slice this returns is sorted ascending by id and every list is deduplicated, so no map
// iteration order can reach the output. When the pair scan has to be truncated, or when the store
// refuses a read the scan depends on, the report is returned ALONGSIDE core.ErrDegraded rather
// than in place of it: the findings that were made are still true, and the caller is told that the
// coverage behind them is partial rather than left to assume it was total.
func DetectRedundancyWithConfig(ctx context.Context, s store.Store, sess core.SessionID,
	cfg config.Config,
) (RedundancyReport, error) {
	// A nil store is the composition root that has not opened one, which test/guards and
	// analyzertest both construct deliberately. There is no session to scan and nothing was found:
	// that is an empty report, not a failure, and not an ErrDegraded either — nothing degraded,
	// there was simply nothing to read.
	if s == nil {
		return RedundancyReport{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	recs, scanErr := scanToolUses(ctx, s, sess)
	if len(recs) == 0 {
		return RedundancyReport{}, scanErr
	}

	report := RedundancyReport{Superseded: supersededExactly(recs)}
	dups, dupErr := nearDupCandidates(recs, cfg.Store.Canonicalize.MinHash.NearDupThreshold)
	report.NearDups = dups

	if scanErr != nil {
		return report, scanErr
	}
	return report, dupErr
}

// scanToolUses returns every tool-use record of sess the store will show this package, in the
// total order (TS, Turn, ID) ascending.
//
// # Why it is two phases, and what the first one costs
//
// store.Store exposes no "list the tool uses of this session" query. ToolUsesByPath needs a path,
// Search returns K ranked hits rather than "all", and adding a session-scoped enumeration would be
// a Rule W-3 amendment against SP-06 that this package may not make — internal/mcp's `timeline`
// records the same finding and declines to invent one for the same reason. So:
//
//  1. DISCOVERY. One Search with no predicates returns the most recent hits the store will admit,
//     ranked by recency alone. Each hit is hydrated through ToolUse, which is what supplies the
//     Session field a Hit does not carry, and the PATH-LESS records of the session — a Bash
//     command, a Task result — enter the set here and only here.
//  2. COMPLETION. Every distinct non-empty path any hit named is then re-read through
//     ToolUsesByPath with no limit, which returns that path's COMPLETE recorded history. This is
//     what makes the supersession claim exact per path rather than exact only within a recency
//     window: a path the discovery phase saw once is then covered from its first read onward.
//
// The residual gap is stated rather than papered over: a path this session touched that no longer
// appears among the store's most recent hits is not covered at all, and neither are the session's
// path-less records outside that window. The scan reports what it observed; it does not claim to
// have observed everything, and a read the store refuses is returned as core.ErrDegraded with
// whatever was already gathered.
func scanToolUses(ctx context.Context, s store.Store, sess core.SessionID) ([]store.ToolUseRecord, error) {
	hits, err := s.Search(ctx, store.Query{K: redundancyScanHits})
	if err != nil {
		return nil, fmt.Errorf("%w: redundancy scan could not enumerate tool uses: %v", core.ErrDegraded, err)
	}

	var (
		byID     = make(map[core.ToolUseID]store.ToolUseRecord, len(hits))
		pathKeys = make(map[string]struct{}, len(hits))
		degraded error
	)
	keep := func(rec store.ToolUseRecord) {
		if rec.Session != sess {
			return
		}
		byID[rec.ID] = rec
	}

	for _, h := range hits {
		if key := normalizedPath(h.Path); key != "" {
			pathKeys[key] = struct{}{}
		}
		rec, recErr := s.ToolUse(ctx, h.ToolUseID)
		if recErr != nil {
			// A hit whose record cannot be read is coverage this scan does not have. It is not a
			// reason to abandon the records that ARE readable, so it lowers the coverage claim and
			// the loop continues.
			if degraded == nil {
				degraded = fmt.Errorf("%w: tool use %s named by search could not be read: %v",
					core.ErrDegraded, string(h.ToolUseID), recErr)
			}
			continue
		}
		keep(rec)
	}

	// Completion runs over a SORTED path list so the order of store reads — and therefore any
	// error this loop reports first — does not depend on map iteration.
	for _, key := range sortedKeys(pathKeys) {
		onPath, pathErr := s.ToolUsesByPath(ctx, key, 0)
		if pathErr != nil {
			if degraded == nil {
				degraded = fmt.Errorf("%w: path %s could not be re-read for completion: %v",
					core.ErrDegraded, key, pathErr)
			}
			continue
		}
		for _, rec := range onPath {
			keep(rec)
		}
	}

	recs := make([]store.ToolUseRecord, 0, len(byID))
	for _, rec := range byID {
		recs = append(recs, rec)
	}
	sortRecords(recs)
	return recs, degraded
}

// normalizedPath is the key "the same normalized path" means, and it mirrors internal/store's own
// unexported storeKey step for step: slash-separated, path.Clean'd, "./"-stripped, then folded
// through paths.Key.
//
// Mirroring it is the point. The store buckets index/tool_use.jsonl by storeKey, so a supersession
// claim grouped by any OTHER normalization would be a claim about a different partition of the
// records than the one the index — and store.ToolUsesByPath, which this scan reads through —
// actually uses. "./docs/CHANGELOG.md" and "docs/CHANGELOG.md" are one path to the store, and an
// exact claim that treated them as two would not be exact.
func normalizedPath(p string) string {
	if p == "" {
		return ""
	}
	q := path.Clean(filepath.ToSlash(p))
	q = strings.TrimPrefix(q, "./")
	if q == "." {
		return ""
	}
	return paths.Key(q)
}

// sortedKeys returns set's keys in ascending order, which is what keeps a loop over a set out of
// the output's determinism story.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortRecords puts recs into the total order (TS, Turn, ID) ascending.
//
// The order has to be TOTAL, not merely chronological: two tool uses of one assistant message are
// parallel siblings that share a turn, and a clock with millisecond resolution regularly gives
// them the same TS as well. The id breaks the remaining tie, which is what makes "the later tool
// use on this path" a well-defined record rather than whichever one the index happened to load
// first.
func sortRecords(recs []store.ToolUseRecord) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].TS != recs[j].TS {
			return recs[i].TS < recs[j].TS
		}
		if recs[i].Turn != recs[j].Turn {
			return recs[i].Turn < recs[j].Turn
		}
		return recs[i].ID < recs[j].ID
	})
}

// supersededExactly returns the ids of every record in recs that a later record replaced, sorted
// ascending.
//
// recs must already be in sortRecords' total order. The two grounds are the ones
// DetectRedundancyWithConfig documents, and both are exact; the function name says so because the
// distinction from nearDupCandidates below is the whole point of this file.
//
// The empty path is skipped, and that is a correctness rule rather than a shortcut: supersession
// is defined as a relation between reads OF SOMETHING, so two Bash results that touched no path
// have no shared path on which one could replace the other. Grouping them under the "" key would
// report every pathless result but the last one as superseded, which is a false claim about
// records whose content nothing here has compared.
//
// The result is sorted by ID rather than chronologically. Chronology is already recoverable from
// the store for any id listed, whereas an id order is TOTAL by construction and needs no tie-break
// argument of its own — and a report a caller can diff between two runs is worth more here than
// one that reads nicely.
func supersededExactly(recs []store.ToolUseRecord) []core.ToolUseID {
	lastOnPath := make(map[string]core.ToolUseID, len(recs))
	for _, rec := range recs {
		if key := normalizedPath(rec.Path); key != "" {
			lastOnPath[key] = rec.ID // recs is ascending, so the final write is the latest record
		}
	}

	var out []core.ToolUseID
	for _, rec := range recs {
		key := normalizedPath(rec.Path)
		replaced := key != "" && lastOnPath[key] != rec.ID
		if replaced || rec.Status == store.StatusSuperseded {
			out = append(out, rec.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nearDupCandidates returns, per record, the other records whose MinHash signature agrees with it
// on at least threshold of its permutations.
//
// Everything about the result is a CANDIDATE set pending exact verification — see
// DetectRedundancyWithConfig for the three specific things a caller may not infer from an entry.
// The comparison itself is sketch.Signature.IsNearDup, reused rather than reimplemented, so this
// package and internal/observer's supersession pass answer "near-duplicate" the same way and a
// disagreement between them is a bug in one caller rather than in two copies of an estimator.
//
// Three exclusions are structural rather than heuristic:
//
//   - The scan is over ordered pairs i < j, so no record can be its own near-duplicate. That is
//     not merely tidy: IsNearDup(x, x) is true for every real signature, so a scan that compared a
//     record with itself would report a saving that does not exist, and analyzertest asserts the
//     absence.
//   - A record with no signature is skipped. sketch's zero Signature means "MinHash was disabled",
//     which is a different fact from "nothing was found", and comparing against it would make
//     every unmeasured result a duplicate of the first thing it met.
//   - A non-positive threshold disables the scan entirely. At threshold 0 every pair of
//     comparable signatures qualifies, so a misconfigured zero would report the whole session as
//     mutually duplicated.
//
// Above maxNearDupComparisons records the scan is truncated to the most recent ones and
// core.ErrDegraded is returned with the (still true) partial result.
func nearDupCandidates(recs []store.ToolUseRecord, threshold float64) (map[core.ToolUseID][]core.ToolUseID, error) {
	if threshold <= 0 {
		return nil, nil
	}

	var degraded error
	if len(recs) > maxNearDupComparisons {
		degraded = fmt.Errorf("%w: near-duplicate scan truncated to the %d most recent of %d records",
			core.ErrDegraded, maxNearDupComparisons, len(recs))
		recs = recs[len(recs)-maxNearDupComparisons:]
	}

	var dups map[core.ToolUseID][]core.ToolUseID
	link := func(a, b core.ToolUseID) {
		if dups == nil {
			dups = make(map[core.ToolUseID][]core.ToolUseID)
		}
		dups[a] = append(dups[a], b)
	}

	for i := range recs {
		if recs[i].Signature.Perms == 0 {
			continue
		}
		for j := i + 1; j < len(recs); j++ {
			if !recs[i].Signature.IsNearDup(recs[j].Signature, threshold) {
				continue
			}
			// The relation is symmetric and is reported symmetrically. A caller holding one id
			// must be able to ask "what is this a candidate duplicate of" without having to
			// re-derive the other direction itself.
			link(recs[i].ID, recs[j].ID)
			link(recs[j].ID, recs[i].ID)
		}
	}

	for id := range dups {
		list := dups[id]
		sort.Slice(list, func(a, b int) bool { return list[a] < list[b] })
		dups[id] = list
	}
	return dups, degraded
}
