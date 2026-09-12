package observer

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// §8.1 item 3, verbatim: "If the chunk set is a superset or near-duplicate of a prior read of the
// same path, mark the earlier one SUPERSEDED in the DAG. Superseded reads are the first candidates
// for eviction and should never appear in a summary."
//
// "Should never appear in a summary" is enforced STRUCTURALLY rather than by a predicate this
// package exports, because internal/checkpoint does not import internal/observer (§3.2): the fact
// lives on store.ToolUseRecord.Status == store.StatusSuperseded and on the EdgeSupersedes edge,
// both of which SP-10 and SP-15 already read, and TestSupersede_StatusSurvivesReopen pins that the
// status outlives the process that set it.
//
// The stage names soft reports this file's failures under. They are dotted, and they are here
// rather than in observer.go's flat block, because each is a step of THIS algorithm: a reader
// looking at observer.err.supersede.root wants the loop below, not the pipeline's stage list.
const (
	stageSupersedeList = "supersede.list"
	stageSupersedeRoot = "supersede.root"
	stageSupersedeMark = "supersede.mark"
)

// detectSupersession marks every earlier read of rec.Path that rec makes redundant, and returns
// their ids MOST RECENT FIRST.
//
// It writes to the STORE only and emits no DAG edges. EdgeSupersedes is part of the §8.1 item 4
// edge set dag.BuildToolUse owns, and graph.go hands marked[0] to it as ObservedTool.Supersedes,
// emitting the tail of the list itself in the same D-1 direction. Splitting it that way is what
// keeps this package from deciding an edge direction: get EdgeSupersedes backwards and the
// superseded read becomes a full-strength upstream source of relevance for everything the new read
// explains, which is the exact opposite of "first candidate for eviction".
//
// The sessionState is unused and named _ so unparam says so out loud, exactly as emitToolGraph's
// context is: supersession is a question about the STORE's per-path history, not about the
// session's in-memory window, and a scan that consulted st would answer a different question after
// a restart than before one. The parameter stays in the signature — same arity, same types —
// because it is the shape §5.21's plan pins.
//
// It counts observer.superseded and nothing else. observer.neardup belongs to the PUT, not to this
// scan: the store's near-duplicate signal is produced for pathless content too, and this function
// returns early on an empty Path, so tooluse.go bumps it at step 5a instead.
//
// Nothing here is exact, and it does not claim to be: SP05-D1 means a read the observer never
// saw cannot supersede an earlier one, so this is a best-effort marking over what arrived (see
// doc.go).
//
// This is the path for a Store WITHOUT the store.SupersedingRecorder capability, and the signature
// is unchanged because that is the shape §5.21's plan pins. The two separate writes it makes — the
// record by its caller, then one MarkSuperseded per mark here — are precisely carried defect
// SP08-D2's second mechanism: a handler cancelled between them leaves a record whose marks never
// landed, and the redelivery that follows appends those marks alone. A Store that HAS the
// capability marks through publishRecord instead, in the same write as the record, and never
// reaches this function. The SCAN is shared, so the two paths can never disagree about WHICH
// records are redundant — only about when the marks land.
func (o *observer) detectSupersession(ctx context.Context, _ *sessionState,
	rec store.ToolUseRecord, res store.PutResult,
) (marked []core.ToolUseID) {
	for _, id := range o.supersessionCandidates(ctx, rec, res) {
		// store.MarkSuperseded takes the OLDER id first: it is the record being flipped, and
		// rec.ID is what its SupersededBy comes to point at.
		if markErr := o.opt.Store.MarkSuperseded(ctx, id, rec.ID); markErr != nil {
			o.soft(stageSupersedeMark, markErr)
			continue // a record the store refused to mark is not reported as marked
		}
		o.count(counterSuperseded)
		marked = append(marked, id)
	}
	return marked
}

// publishRecord appends rec to the tool_use index together with the supersede marks it authors,
// and reports whether anything was written.
//
// recorded=false with a nil error means the index ALREADY held rec.ID with this exact Root: the
// record and the marks it authored both landed on an earlier run of this same delivery. It is the
// single fact the tool path's redelivery absorption turns on, and it is answerable only by the
// store, because on this path the host's own tool_use_id is the identity and two deliveries
// carrying it are indistinguishable here.
//
// The candidate scan runs BEFORE the record is in the index, which is a real difference from the
// legacy order and not merely a reshuffle: the lookback window then holds supersessionLookback real
// priors where the post-write scan saw one fewer plus rec itself, and a record another session
// appends between the scan and the write is not marked where the post-write MarkSuperseded would
// have seen it. Both differences are soft and in the safer direction (one more candidate
// considered; a lost mark, never a wrong one), and both are recorded in SP08-D2's resolution.
func (o *observer) publishRecord(ctx context.Context, rec store.ToolUseRecord,
	res store.PutResult, empty bool,
) (marked []core.ToolUseID, recorded bool, err error) {
	if o.idx == nil {
		// No capability: the record alone, and the caller marks afterwards through
		// detectSupersession. RecordToolUse cannot distinguish a replay from a fresh record, so the
		// legacy path reports recorded=true and re-runs the pipeline exactly as it does today.
		return nil, true, o.opt.Store.RecordToolUse(ctx, rec)
	}

	// An EMPTY result has no chunk set to contain a prior one and no root to compare against, so
	// the scan could only ever answer "nothing" — at the cost of a per-path index lookup on the
	// hot path. This is step 8's own skip, moved to where the candidates are now chosen.
	var older []core.ToolUseID
	if !empty {
		older = o.supersessionCandidates(ctx, rec, res)
	}

	marked, recorded, err = o.idx.RecordToolUseSuperseding(ctx, rec, older)
	if err != nil {
		return nil, false, err
	}
	// One bump per mark actually written, which is the meaning observer.superseded has always had
	// and what test/e2e/phase1_exit_test.go reads it as.
	for range marked {
		o.count(counterSuperseded)
	}
	return marked, recorded, nil
}

// supersessionCandidates returns the ids of every earlier read of rec.Path that rec makes
// redundant, MOST RECENT FIRST — the order that makes marked[0] the right value for
// ObservedTool.Supersedes.
//
// It is READ-ONLY: it writes nothing and marks nothing, so it is safe to call before rec itself is
// in the index, which is what the atomic path does. The p.ID == rec.ID filter is kept for the
// legacy path, where rec IS already recorded by the time the scan runs.
func (o *observer) supersessionCandidates(ctx context.Context,
	rec store.ToolUseRecord, res store.PutResult,
) (candidates []core.ToolUseID) {
	// A result with no path is not a read OF anything, a retrieval result is already a
	// first-eviction candidate (resolved decision 6), and a tool outside the supersession classes
	// has no notion of "the same content read again".
	class := supersedableClass(rec.Tool)
	if rec.Path == "" || rec.Ephemeral || class == "" {
		return nil
	}

	// The lookback is bounded at the CALL, so a path read a thousand times in one session cannot
	// make this hook unbounded — the budget of §8.1 is 15 ms p99 for the whole pipeline.
	prior, err := o.opt.Store.ToolUsesByPath(ctx, rec.Path, supersessionLookback)
	if err != nil {
		o.soft(stageSupersedeList, err)
		return nil
	}

	thr := o.opt.Cfg.Store.Canonicalize.MinHash.NearDupThreshold

	// ToolUsesByPath returns the most recent records newest first (§5.8), so marked comes back
	// newest first too — which is what makes marked[0] the right value for ObservedTool.Supersedes.
	// The loop itself is order-INDEPENDENT: every candidate is filtered on p.TS <= rec.TS, so an
	// oldest-first store would change only which end of marked graph.go reads.
	for _, p := range prior {
		if p.ID == rec.ID {
			continue // the record this scan is for
		}
		if p.Status == store.StatusSuperseded {
			continue // already handled by an earlier read
		}
		if p.TS > rec.TS {
			continue // only ever mark EARLIER reads
		}
		if p.Ephemeral {
			continue // ephemeral results are already first-evicted
		}
		if supersedableClass(p.Tool) != class {
			continue // a Grep hit list is not a version of the file's content
		}
		if p.Root == (core.Hash{}) {
			// An EMPTY result is still indexed (step 6 of onToolUse writes the record before it
			// knows whether anything was stored), so the index carries records whose Root is the
			// zero hash and whose content does not exist. Skipping them here is the same rule
			// isSuperset applies to an empty older chunk list — nothing is not evidence of
			// anything — and it is what keeps GetRoot from failing, and warning, on every later
			// read of a path that was once empty.
			continue
		}

		superseded := p.Root == rec.Root // identical content: the later read wins
		if !superseded {
			pr, rootErr := o.opt.Store.GetRoot(ctx, p.Root)
			if rootErr != nil {
				o.soft(stageSupersedeRoot, rootErr)
				continue
			}
			superseded = isSuperset(res.Root.Chunks, pr.Chunks) ||
				(thr > 0 && rec.Signature.IsNearDup(p.Signature, thr))
		}
		if !superseded {
			continue
		}

		candidates = append(candidates, p.ID) // no AddEdge here — see graph.go
	}

	return candidates
}

// isSuperset reports whether every chunk of older is also present in newer.
//
// It is a SET containment test: multiplicity is ignored, per §8.1 item 3's "chunk set", so a result
// that repeats a chunk does not defeat it. An EMPTY older is never contained — returning true there
// would make every read a superset of every empty prior, and mark records nothing is known about.
func isSuperset(newer, older []core.ChunkRef) bool {
	if len(older) == 0 {
		return false
	}
	have := make(map[core.Hash]struct{}, len(newer))
	for _, c := range newer {
		have[c.Hash] = struct{}{}
	}
	for _, c := range older {
		if _, ok := have[c.Hash]; !ok {
			return false
		}
	}
	return true
}
