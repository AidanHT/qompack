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
func (o *observer) detectSupersession(ctx context.Context, _ *sessionState,
	rec store.ToolUseRecord, res store.PutResult,
) (marked []core.ToolUseID) {
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

		// store.MarkSuperseded takes the OLDER id first: it is the record being flipped, and
		// rec.ID is what its SupersededBy comes to point at.
		if markErr := o.opt.Store.MarkSuperseded(ctx, p.ID, rec.ID); markErr != nil {
			o.soft(stageSupersedeMark, markErr)
			continue // a record the store refused to mark is not reported as marked
		}
		o.count(counterSuperseded)
		marked = append(marked, p.ID) // no AddEdge here — see graph.go
	}

	return marked
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
