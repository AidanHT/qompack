package daemon

import (
	"context"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/tokens"
)

// This file is SP-15's consumer integration, and the daemon is where it has to live.
//
// tools/devtool/importrules.go gives rehydrate the allow-set {checkpoint, store, negknow, dag,
// rules, skills, tokens} and analyzer the allow-set {store, dag, sketch, scheduler}. rehydrate
// therefore cannot call the selector and the selector cannot read an elimination — by design,
// since neither is the other's layer. A composition root may import both, and this is the only
// place in the tree that legitimately holds a negknow.Record and an analyzer.Candidate at once.
//
// So the translation lives here, and it is the whole integration: run the selector, map its
// Proposal onto the neutral rehydrate.SelectionOutcome, and hand that to Build as request data.
// Build stays a pure function of (Request, Deps), which is what PropBuild_Deterministic requires
// and what a live provider on Deps would have quietly broken.
//
// Every failure path returns nil, which is the shipped pre-SP-15 behaviour exactly. A session that
// starts with the old ranking is fine; a session that fails to start because a selector errored is
// not (§12.3), and this is a capability that defaults to off.

// selectionLambda is the redundancy penalty the rehydration selector spends.
//
// It reads config.selection.submodular.lambda rather than duplicating it: Appendix C already owns
// this number, and a second copy here would be the magic constant §11.6 forbids and would drift
// from the documented default the first time either moved.
func (s *rehydrateService) selectionLambda() float64 {
	return s.cfg().Selection.Submodular.Lambda
}

// selectionFor runs representation selection for one rehydration, or reports nil when selection is
// off, unavailable, or could not produce an answer.
//
// nil is not an error signal. It is the documented rollback path: rehydrate.applySelection returns
// its input untouched for a nil outcome, so the whole feature disappears and item 3 falls back to
// the slice-score ranking that shipped. That is why every branch below prefers nil to a partial
// answer — a half-built selection would silently change what the session is told it knows.
func (s *rehydrateService) selectionFor(
	ctx context.Context, cp checkpoint.Checkpoint, budget core.Tokens, led negknow.Ledger,
) *rehydrate.SelectionOutcome {
	// The ship-order gate, checked here as well as inside NewSelector. The constructor's refusal
	// is the structural guarantee; this one is the OPERATOR's switch, and it is checked first so
	// that a disabled selector costs no store reads, no candidate construction and no log line.
	if !s.cfg().Runtime.Selection.SubmodularEnabled {
		return nil
	}

	cands := s.eliminationCandidates(ctx, cp, led)
	if len(cands) == 0 {
		return nil
	}

	prop, err := analyzer.Propose(ctx, 0, cands, s.selectionLambda(), budget)
	if err != nil {
		// The commonest error here is core.ErrNotImplemented from the closing-note-3 gate, when
		// the operator switch is on in a process where p-selection never came up. That is a
		// configuration state, not a fault, so it is Debug rather than Loud and the rehydration
		// proceeds on the path that shipped.
		s.o.Log.Debug("rehydrate: selection unavailable, using the complete-record heuristic",
			"session", string(cp.Session), "err", err.Error())
		return nil
	}

	out := &rehydrate.SelectionOutcome{
		Keep:     make([]dag.NodeID, 0, len(prop.Chosen)),
		Archive:  prop.Archive,
		Tokens:   prop.Tokens,
		Overflow: prop.Overflow,
		Item:     string(prop.Item),
		Reason:   prop.Reason,
	}
	for _, r := range prop.Chosen {
		out.Keep = append(out.Keep, r.Item)
	}
	s.o.Log.Debug("rehydrate: selection applied",
		"session", string(cp.Session), "keep", len(out.Keep), "archive", len(out.Archive),
		"tokens", int(out.Tokens), "overflow", out.Overflow)
	return out
}

// eliminationCandidates turns the eliminations this rehydration could carry into the selector's
// neutral Candidate shape.
//
// TWO THINGS HERE ARE THE G6.3 CONTRACT, and both are about not overstating what is known.
//
// First, Mandatory is set only for a record the ledger reports ACTIVE. An elimination is a
// conditional fact — "widening the pool timeout doesn't work given pgbouncer 1.18" — so a stale
// one is exactly the record that must not be forced into the payload as though it still bound.
//
// Second, the qualification is carried, not collapsed. negknow.StatusActive maps to QualCurrent
// and StatusStale to QualStale, and a record whose status is neither is QualUncertain rather than
// being guessed either way. Qualification.Active() is false for both of the latter, so a stale or
// uncertain record can still be SELECTED on merit and rendered with its qualification, and can
// never become a binding constraint. A false already_tried that blocks a now-viable approach is
// the §12 High-severity direction, and it is reached by promoting exactly these records.
func (s *rehydrateService) eliminationCandidates(
	ctx context.Context, cp checkpoint.Checkpoint, led negknow.Ledger,
) []analyzer.Candidate {
	recs := s.selectableEliminations(ctx, cp, led)
	if len(recs) == 0 {
		return nil
	}

	cands := make([]analyzer.Candidate, 0, len(recs))
	for _, rec := range recs {
		qual := qualificationOf(rec)
		prov := analyzer.Provenance{
			Origin:        dag.NodeID(rec.ID),
			Root:          rec.Evidence,
			Derived:       false,
			Qualification: qual,
		}

		// One item, two compatible representations: the rendered elimination, and a pointer that
		// names the record for `already_tried` to resolve. The selector picks at most one.
		full := analyzer.Representation{
			Item:          dag.NodeID(rec.ID),
			Kind:          analyzer.RepExactSpan,
			Coverage:      1,
			AssembledCost: s.priceElimination(rec),
			Prov:          prov,
		}
		ptr := analyzer.Representation{
			Item:          dag.NodeID(rec.ID),
			Kind:          analyzer.RepPointer,
			Coverage:      selectionPointerCoverage,
			AssembledCost: s.priceString(rec.ID),
			Prov:          prov,
		}

		cands = append(cands, analyzer.Candidate{
			Item: dag.NodeID(rec.ID),
			// Pos is zero for every elimination, and Propose is called with p=0. An elimination is
			// not a prefix block: it has no token position, so §13 invariant 4 has nothing to
			// constrain here. The guard still runs — Propose routes through NewSelector — and
			// still refuses a negative position, which is the only way this could go wrong.
			Pos:       0,
			Weight:    1,
			Mandatory: qual.Active(),
			Reps:      []analyzer.Representation{full, ptr},
		})
	}
	return cands
}

// selectableEliminations is the population the selector ranks: the checkpoint's own frozen copy
// unioned with the ledger's currently-active records, deduplicated by id with the LEDGER copy
// winning because it carries the current status where the checkpoint's is frozen at write time.
//
// This deliberately mirrors rehydrate's own union rather than sharing it — that function is
// unexported, and §3.2 keeps these two packages apart on purpose. The duplication is safe because
// the two sets are allowed to disagree: applySelection ignores a Keep id it no longer holds and
// reports a record the selection never mentioned, so a divergence degrades into the shipped
// ranking rather than into a lost record. A ledger error is never fatal; the frozen copy stands.
func (s *rehydrateService) selectableEliminations(
	ctx context.Context, cp checkpoint.Checkpoint, led negknow.Ledger,
) []negknow.Record {
	seen := make(map[string]int, len(cp.Eliminated))
	out := make([]negknow.Record, 0, len(cp.Eliminated))
	add := func(rec negknow.Record) {
		if i, dup := seen[rec.ID]; dup {
			out[i] = rec // the later (ledger) copy wins
			return
		}
		seen[rec.ID] = len(out)
		out = append(out, rec)
	}

	for _, rec := range cp.Eliminated {
		add(rec)
	}
	if led != nil {
		for _, scope := range []negknow.Scope{negknow.ScopeSession, negknow.ScopeProject} {
			recs, err := led.Active(ctx, scope)
			if err != nil {
				s.o.Log.Debug("rehydrate: selection could not read the ledger",
					"scope", string(scope), "err", err.Error())
				continue
			}
			for _, rec := range recs {
				add(rec)
			}
		}
	}
	return out
}

// priceElimination estimates what carrying rec whole will cost once rendered.
//
// It prices the record's own text — target, approach and reason — rather than rehydrate's exact
// rendered line, which is unexported in a package this one may not reach into. That makes it an
// ESTIMATE and it is labelled as one: blocker M5-U15-representation-overhead is open precisely
// because assembled overhead has not been calibrated against a provider's reported usage. It is
// sound for RANKING, which is all the selector asks of it, because the per-record rendering
// overhead it omits is near-constant across records and so cancels in the comparison.
func (s *rehydrateService) priceElimination(rec negknow.Record) core.Tokens {
	return s.priceString(rec.Target + " " + rec.Approach + " " + rec.Reason)
}

// priceString prices s with the configured estimator, falling back to the same bare
// (len+3)/4 approximation rehydrate uses when no estimator is wired.
func (s *rehydrateService) priceString(str string) core.Tokens {
	if s.o.Deps.Tokens == nil {
		return core.Tokens((len(str) + 3) / 4)
	}
	return s.o.Deps.Tokens.EstimateString(str, tokens.ClassProse)
}

// selectionPointerCoverage is what a pointer to an elimination covers relative to the rendered
// line: the session learns the record EXISTS and can resolve it through `already_tried`, but not
// what it says without a round trip.
//
// It is a declared modelling choice, not a measurement. The number's job is to make a pointer
// worth less than the line and more than nothing, so that under pressure the selector prefers
// naming a record to dropping it.
const selectionPointerCoverage = 0.25

// qualificationOf maps a ledger status onto the analyzer's negknow-free qualification.
//
// The default arm is QualUncertain, not QualCurrent. That matters more than it looks: a status
// this build does not recognise is a record whose applicability is UNKNOWN, and the safe reading
// of unknown is "carry it, qualify it, do not let it bind".
func qualificationOf(rec negknow.Record) analyzer.Qualification {
	switch rec.Status {
	case negknow.StatusActive:
		return analyzer.QualCurrent
	case negknow.StatusStale:
		return analyzer.QualStale
	default:
		return analyzer.QualUncertain
	}
}
