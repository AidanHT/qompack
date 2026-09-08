package checkpoint

import (
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// This file is SP-16 commit 5: demand-driven promotion of tier-3 pointers.
//
// internal/mcp's promoter counts re-expansions and marks a hash promoted at
// retrieval.promoteAfterExpansions; its doc comment says "SP-13 counts and exposes; it does not
// promote anything itself" and names this as the consumer. This is that consumer.
//
// THE EVIDENCE ARRIVES AS VALUES, not through an import. internal/mcp imports internal/checkpoint,
// so the reverse edge is a cycle, and §3.2's allow-set excludes mcp regardless. PromotionCandidate
// is therefore filled in by the composition root from Promoter.Promoted and store.Demand — the
// same shape scheduler.PriorEvidence uses for the same reason, and the reason internal/checkpoint
// stays testable without a daemon.
//
// WHAT PROMOTION IS ALLOWED TO CHANGE is the whole design. Qompack.md §10 Phase 7: "Adaptive
// retrieval and promotion affect future Qompack representations; they do not evict native
// messages", and "Demand-based promotion only changes the next Qompack injection or result;
// same-epoch delivery does not prove native residency." Two mechanical consequences:
//
//   - The epoch guard below refuses outright when the checkpoint being written is not strictly
//     later than the one the demand was observed serving. Evidence gathered while answering from
//     checkpoint N may change checkpoint N+1. It may never change N, because N has already been
//     delivered and, in the archive, already been read.
//   - Promotion REORDERS Pointers.Tools. It adds no field, changes no json tag and creates no
//     record: Truncate cuts tool pointers tail-first, so moving a pointer forward is exactly and
//     only "this survives the next budget", expressed through the mechanism that already exists.
//     Rule W-2 pins this struct byte-for-byte and promotion must not be the thing that breaks it.
//
// FREQUENCY IS NOT USEFULNESS, and this is where SP-16's demand record earns its shape. A hash
// asked for forty times with no observed outcome is not evidence that having it helps; it is
// evidence that something is repeatedly missing, which is a different claim with a different fix.
// store.Demand.Instrumented() is consulted before any rate is trusted, and an uninstrumented
// candidate is WITHHELD — not promoted, not refused, because the truth is that nobody measured.
// Instrumented() alone is not enough, though: it is vacuously true for an empty record, so the
// usefulness rate must also be KNOWN before a candidate is eligible. Expansion count is the signal
// that says "look here"; it is never the evidence that says "this helped".

// promotionDropKind is the drop-report kind for a candidate that lost to the overhead budget.
//
// It is a distinct kind rather than dropBudgetExceeded because the two are answerable in
// different ways: a budget-exceeded checkpoint needs a smaller checkpoint, while this needs
// either a larger promotion overhead or a better-instrumented demand record, and a reader who
// cannot tell them apart cannot act on either.
const promotionDropKind = "promotion_overflow"

// defaultPromotionMinExpansions is the floor PromotionRequest.MinExpansions falls back to when it
// is left at zero: one re-expansion is not a pattern.
const defaultPromotionMinExpansions = 2

// PromotionDecision is what the gate concluded about one candidate.
type PromotionDecision uint8

const (
	// PromotionWithheld is the ZERO VALUE, and it means the evidence did not support a decision —
	// too few expansions, or demand that nobody instrumented. It is deliberately not Refused: an
	// unmeasured candidate has not been judged, and a later session that measures it may promote
	// it. Making this the zero value means a PromotedPointer nobody filled in claims nothing.
	PromotionWithheld PromotionDecision = iota
	// PromotionAdmitted means the gate accepted the candidate and the overhead budget had room.
	//
	// It says the pointer PASSED, not that anything moved: in report-only mode every check still
	// runs and every admission is recorded, while Order stays empty so Apply changes nothing.
	// Calling this state "applied" would make a disabled pass claim an effect it did not have,
	// which is the one thing report-only mode exists to avoid.
	PromotionAdmitted
	// PromotionRefused means the gate decided against it on evidence: a same-or-earlier epoch, an
	// unusable hash, an authority the vocabulary does not recognise, or a measured usefulness of
	// zero across attempts that did happen.
	PromotionRefused
	// PromotionOverflowed means the gate WANTED it and the overhead budget did not have room. It
	// is separate from Refused because it is not a judgement about the candidate at all, and it
	// is the one decision that always leaves a DropEntry behind.
	PromotionOverflowed
)

// String renders the decision for traces and drop details.
func (d PromotionDecision) String() string {
	switch d {
	case PromotionAdmitted:
		return "admitted"
	case PromotionRefused:
		return "refused"
	case PromotionOverflowed:
		return "overflowed"
	case PromotionWithheld:
		return "withheld"
	default:
		return "withheld"
	}
}

// PromotionCandidate is one tier-3 tool pointer offered for promotion, with the evidence that
// argues for it.
type PromotionCandidate struct {
	// Pointer is the tier-3 entry to promote. Its Hash is what internal/mcp counted expansions
	// against, and what `expand` resolves.
	Pointer ToolPointer
	// Expansions is how many times the session re-materialized this hash — mcp's count, the
	// signal §8.7 calls "a signal, not a cost".
	Expansions int
	// Demand is the SP-16 demand record for the same key. It supplies usefulness SEPARATELY from
	// frequency, and its Instrumented() reports whether the rate means anything.
	Demand store.Demand
	// Authority is the evidence class behind the promotion, in SP-20's vocabulary. A tool
	// observation is the normal case; an unrecognised authority is refused rather than assumed.
	Authority core.Authority
}

// PromotionRequest is one promotion pass.
type PromotionRequest struct {
	// Enabled is runtime.phase7.retrieval.demandPromotion. When false the pass still EVALUATES
	// every candidate and reports what it would have done, and applies nothing — report-only is a
	// real state, not a skipped one, and it is how the gate's evidence is gathered before the
	// switch is ever turned on.
	Enabled bool
	// ObservedSeq is the checkpoint the demand evidence was gathered while serving.
	ObservedSeq core.CheckpointSeq
	// TargetSeq is the checkpoint about to be written. It must be strictly greater than
	// ObservedSeq or the whole pass refuses.
	TargetSeq core.CheckpointSeq
	// Candidates are the offered pointers, in any order.
	Candidates []PromotionCandidate
	// Overhead is the token budget promotion may spend. Zero means promotion may reorder nothing,
	// which is honoured rather than treated as unlimited.
	Overhead core.Tokens
	// MinExpansions is the re-expansion floor; zero means defaultPromotionMinExpansions.
	MinExpansions int
	// Est prices a promoted pointer's overhead. A nil estimator prices everything at zero, which
	// makes the budget non-binding — so it is reported as an omission rather than silently
	// letting every candidate through.
	Est tokens.Estimator
}

// PromotedPointer is the trace for one candidate: what was decided, what it cost, and why.
type PromotedPointer struct {
	// Pointer is the candidate's pointer, unchanged.
	Pointer ToolPointer
	// Decision is the gate's conclusion.
	Decision PromotionDecision
	// Cost is what promoting it was priced at, whether or not it was admitted.
	Cost core.Tokens
	// Why explains every decision that is not Admitted. It is a core.Omission because the reader
	// who needs it is the one asking what is missing from the next checkpoint and what would put
	// it back.
	Why core.Omission
}

// PromotionPlan is the result of a pass: the consumer trace M6-G16-C asks for.
type PromotionPlan struct {
	// Enabled echoes the request, so a plan read from a log says whether it was ever applicable.
	Enabled bool
	// ObservedSeq and TargetSeq record the epoch pair the guard checked.
	ObservedSeq, TargetSeq core.CheckpointSeq
	// Considered is how many candidates were offered.
	Considered int
	// Order is the promoted hashes, best-supported first. It is empty when Enabled is false even
	// though Results is fully populated: that difference IS report-only mode.
	Order []core.Hash
	// Results is one entry per candidate, in the request's order, so a caller can join back.
	Results []PromotedPointer
	// Spent and Overhead are the budget accounting.
	Spent, Overhead core.Tokens
	// Dropped is the overflow trace: one entry per candidate the budget refused, never a silent
	// omission.
	Dropped []DropEntry
	// Omissions record what the pass could not determine.
	Omissions []core.Omission
}

// Admitted counts the candidates the gate accepted. In report-only mode this is positive while
// Order is empty — that difference is the whole of report-only mode.
func (p PromotionPlan) Admitted() int {
	n := 0
	for _, r := range p.Results {
		if r.Decision == PromotionAdmitted {
			n++
		}
	}
	return n
}

// Promote evaluates every candidate and returns the plan.
//
// It never returns an error and never mutates anything. Everything it cannot determine becomes an
// omission, for the same reason daemon.ObserveScope works that way: a session must not fail
// because a promotion could not be decided, and the safe outcome — promote nothing — is already
// the outcome of deciding nothing.
func Promote(req PromotionRequest) PromotionPlan {
	plan := PromotionPlan{
		Enabled:     req.Enabled,
		ObservedSeq: req.ObservedSeq,
		TargetSeq:   req.TargetSeq,
		Considered:  len(req.Candidates),
		Overhead:    req.Overhead,
		Results:     make([]PromotedPointer, len(req.Candidates)),
	}
	note := func(reason, recovery string) {
		plan.Omissions = append(plan.Omissions, core.Omission{Reason: reason, Recovery: recovery})
	}

	// GATE 1 — EPOCH. Evidence may only change a LATER checkpoint. This is checked before
	// anything else, and it refuses rather than withholds, because a same-epoch promotion is not
	// an unmeasured candidate: it is a request to change something already delivered.
	if req.TargetSeq <= req.ObservedSeq {
		why := core.Omission{
			Reason: "promotion evidence was observed at or after the checkpoint being written, " +
				"and same-epoch delivery does not prove native residency",
			Recovery: "the evidence still applies to the next checkpoint; nothing was changed here",
		}
		for i, c := range req.Candidates {
			plan.Results[i] = PromotedPointer{Pointer: c.Pointer, Decision: PromotionRefused, Why: why}
		}
		plan.Omissions = append(plan.Omissions, why)
		return plan
	}

	if req.Est == nil {
		note("no token estimator was supplied, so promotion overhead was priced at zero",
			"pass the writer's estimator; the overhead budget cannot bind without one")
	}
	minExp := req.MinExpansions
	if minExp <= 0 {
		minExp = defaultPromotionMinExpansions
	}

	// GATE 2 — PER CANDIDATE, on evidence alone. Nothing is admitted here; admission is a second
	// pass, because it depends on the order and the order depends on all the evidence.
	type ranked struct {
		idx    int
		rate   float64
		recov  int64
		expand int
	}
	var eligible []ranked
	for i, c := range req.Candidates {
		res := PromotedPointer{Pointer: c.Pointer, Cost: promotionCost(req.Est, c.Pointer)}

		switch {
		case c.Pointer.Hash.IsZero():
			res.Decision = PromotionRefused
			res.Why = core.Omission{
				Reason:   "the candidate has no content hash, so nothing could be expanded from it",
				Recovery: "record the pointer's hash at capture; the pointer is unchanged",
			}
		case !c.Authority.Valid():
			res.Decision = PromotionRefused
			res.Why = core.Omission{
				Reason:   "the promotion's authority is not one this build recognises",
				Recovery: "attribute the demand observation before promoting on it",
			}
		case c.Expansions < minExp:
			res.Decision = PromotionWithheld
			res.Why = core.Omission{
				Reason:   "the hash was not re-expanded often enough to be a pattern",
				Recovery: "the count persists; a later session may cross the threshold",
			}
		case !c.Demand.Instrumented():
			// The load-bearing case. Requests without outcomes are frequency, and frequency is
			// not usefulness — promoting on it would be exactly the substitution SP-16 §4 forbids.
			res.Decision = PromotionWithheld
			res.Why = core.Omission{
				Reason: "usefulness was not instrumented for this key, so its request count is " +
					"frequency and not evidence that having it helped",
				Recovery: "record useful/failed outcomes for the key; frequency alone cannot promote",
			}
		default:
			// Instrumented() is a consistency check, not a measurement, and it is VACUOUSLY TRUE
			// for an empty record: no gaps, and zero outcomes do account for zero requests. A
			// candidate can therefore arrive with four expansions from mcp's counter and a demand
			// record nobody ever wrote — and admitting that would be promoting on expansion count
			// alone, which is the exact substitution this gate exists to prevent. So the rate must
			// be KNOWN, not merely self-consistent.
			rate, known := c.Demand.Usefulness()
			switch {
			case !known:
				res.Decision = PromotionWithheld
				res.Why = core.Omission{
					Reason: "no retrieval outcome was ever recorded for this key, so there is no " +
						"usefulness to weigh — an empty demand record is not a measurement",
					Recovery: "record useful/failed outcomes for the key; expansion counts alone cannot promote",
				}
			case rate == 0:
				res.Decision = PromotionRefused
				res.Why = core.Omission{
					Reason:   "every measured attempt for this key failed to help",
					Recovery: "the pointer stays retrievable; promotion would spend budget on a measured miss",
				}
			default:
				eligible = append(eligible, ranked{
					idx: i, rate: rate, recov: c.Demand.MaxRecoveryCostMs, expand: c.Expansions,
				})
			}
		}
		plan.Results[i] = res
	}

	// Order: measured usefulness first, then what is most expensive to re-derive, then how
	// insistently it was asked for, then the hash. The last key is not a preference — it is what
	// makes the plan deterministic for equal evidence, which the golden traces depend on.
	sort.SliceStable(eligible, func(a, b int) bool {
		x, y := eligible[a], eligible[b]
		switch {
		case x.rate != y.rate:
			return x.rate > y.rate
		case x.recov != y.recov:
			return x.recov > y.recov
		case x.expand != y.expand:
			return x.expand > y.expand
		default:
			return req.Candidates[x.idx].Pointer.Hash.String() < req.Candidates[y.idx].Pointer.Hash.String()
		}
	})

	// GATE 3 — BUDGET. Admission in order until the overhead is gone; every refusal after that is
	// an overflow with a drop entry, never a silent loss.
	for _, e := range eligible {
		res := &plan.Results[e.idx]
		if plan.Spent+res.Cost > req.Overhead {
			res.Decision = PromotionOverflowed
			res.Why = core.Omission{
				Reason:   "the promotion overhead budget had no room left for this pointer",
				Recovery: "raise runtime overhead for promotion, or expand(hash) still resolves it",
			}
			plan.Dropped = append(plan.Dropped, DropEntry{
				Kind:   promotionDropKind,
				ID:     string(res.Pointer.ToolUseID),
				Detail: "demand-promoted pointer did not fit the overhead budget; expand(hash) still resolves",
			})
			continue
		}
		plan.Spent += res.Cost
		res.Decision = PromotionAdmitted
		if req.Enabled {
			plan.Order = append(plan.Order, res.Pointer.Hash)
		}
	}

	if !req.Enabled && plan.Admitted() > 0 {
		note("demand promotion is disabled, so the plan was evaluated and not applied",
			"set runtime.phase7.retrieval.demandPromotion once the gate's evidence is recorded")
	}
	return plan
}

// promotionCost prices one promoted pointer's overhead: the pointer as it will be written.
//
// A promotion adds no bytes to the checkpoint — it reorders — so this is not the marginal size of
// the document. It is the cost of the thing being kept that would otherwise have been cut, which
// is what the overhead budget is actually rationing.
func promotionCost(est tokens.Estimator, p ToolPointer) core.Tokens {
	if est == nil {
		return 0
	}
	return est.EstimateString(string(p.ToolUseID)+p.Hash.String()+p.Summary, tokens.ClassJSON)
}

// Apply reorders c's tool pointers so the promoted ones lead, and returns the new checkpoint plus
// the plan's drop entries.
//
// c IS NOT MUTATED. The slice is copied before it is reordered, which is not a detail: the
// checkpoint handed in may be one just read from the archive, and sorting its backing array in
// place would rewrite history that other readers still hold. "Preserve archives" is a property of
// this function, and TestPromote_ApplyDoesNotMutateTheArchivedCheckpoint is where it is pinned.
//
// A disabled or empty plan returns c unchanged, drops and all — including the OVERFLOW drops,
// which are reported whether or not anything was applied, because a candidate that lost to the
// budget was left out either way.
func (p PromotionPlan) Apply(c Checkpoint) (Checkpoint, []DropEntry) {
	if len(p.Order) == 0 || len(c.Pointers.Tools) == 0 {
		return c, p.Dropped
	}

	rank := make(map[core.Hash]int, len(p.Order))
	for i, h := range p.Order {
		if _, dup := rank[h]; !dup {
			rank[h] = i
		}
	}

	tools := make([]ToolPointer, len(c.Pointers.Tools))
	copy(tools, c.Pointers.Tools)

	// Stable so that everything the plan did not name keeps the order the writer chose. Promotion
	// says what should come first; it has no opinion about the rest, and inventing one would make
	// an unrelated pointer's survival depend on a promotion it was never part of.
	sort.SliceStable(tools, func(a, b int) bool {
		ra, oka := rank[tools[a].Hash]
		rb, okb := rank[tools[b].Hash]
		switch {
		case oka && okb:
			return ra < rb
		default:
			return oka && !okb
		}
	})

	out := c
	out.Pointers.Tools = tools
	return out, p.Dropped
}
