package rehydrate

import (
	"sort"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
)

// This file is SP-15's consumer seam: how a representation selection reaches the L5 rehydrator
// (plans/sdd/V5-SP-15/contract.md §1).
//
// It is deliberately NOT an interface on Deps. tools/devtool/importrules.go denies rehydrate any
// analyzer import, so the selector cannot live behind a collaborator here — but the stronger
// reason is that Build is documented as a PURE function of (Request, Deps) and
// PropBuild_Deterministic requires byte-identical output for identical input. A live provider that
// went and computed a selection during Build would put a store read, a DAG walk and a token
// estimate inside a function whose whole contract is that it has none of those. So the daemon —
// a composition root, which may import anything — runs the selector BEFORE calling Build and
// passes the outcome in as ordinary request DATA. Selection becomes another input to a pure
// function rather than a behaviour hidden inside one.
//
// A nil Request.Selection is the rollback path and it is exact, not approximate: every function
// here returns its input untouched, so a build with selection disabled is byte-for-byte the build
// that shipped before SP-15. That is what makes "disable the selector" a real switch rather than
// a different code path with its own bugs.

// dropKindArchive is the archive-only outcome of a representation selection: an item the selector
// deliberately did not inject, which remains recoverable from the archive.
//
// It is its own kind rather than a reuse of dropKindElimination because the two answer different
// questions. An elimination drop says "this did not fit the budget"; an archive drop says "this
// was not chosen for injection, and here is how to get it back". Collapsing them would lose the
// recovery path, which is the only thing that makes an archive-only choice legitimate rather than
// a silent loss.
const dropKindArchive = "archive_only"

// archiveRecoveryDetail is the recovery path every archive-only drop carries. It is one string in
// one place so that no caller can mint an archive drop without one: an archive-only outcome
// without a way back is indistinguishable from a drop, and gate M5-G15-A exists to keep the two
// distinguishable.
const archiveRecoveryDetail = "not injected by representation selection; still recoverable — " +
	"call recall(id) or dropped() for the full accounting"

// SelectionOutcome is a PRE-COMPUTED representation selection, produced by the analyzer and
// carried into Build as request data (contract §1, §2).
//
// It is the neutral, analyzer-free spelling of analyzer.Proposal: rehydrate may not import
// analyzer, so the daemon translates. The translation is total — every field here has exactly one
// source in a Proposal — which is what keeps the two from drifting into two different ideas of
// what was selected.
type SelectionOutcome struct {
	// Keep lists the items the selector admitted, IN THE SELECTOR'S OWN ORDER. That order is the
	// selector's ranking and it replaces this package's slice-score ranking when a selection is
	// present; re-sorting it here would discard the decision the selector was run to make.
	Keep []dag.NodeID
	// Archive lists items delivered as archive-only: not injected, still recoverable. Each one
	// becomes a reported drop with a recovery path, never a silent omission.
	Archive []dag.NodeID
	// Tokens is the assembled cost the selector priced Keep at, INCLUDING serialized overhead. It
	// is carried for reconciliation against what this package actually renders — a selector whose
	// estimate and the rehydrator's rendering disagree is a bug worth seeing, not a rounding
	// difference to paper over.
	Tokens core.Tokens
	// Overflow reports that a mandatory record could not be carried at any representation.
	Overflow bool
	// Item is the overflowing record's id when Overflow is set: the drop report's id column.
	Item string
	// Reason is the human-readable explanation when Overflow is set: the drop report's detail.
	// Keeping the two apart is what stops a whole sentence landing where an identifier belongs.
	Reason string
}

// eliminationItemID is how an elimination record is named to the selector, and it is the whole of
// the naming contract between the two sides (contract §2).
//
// negknow.Record.ID and dag.NodeID are both strings, so the mapping is the identity — but writing
// it as a named function rather than an inline conversion is the point: the daemon builds its
// analyzer.Candidates through this same identity, and a single named function is what makes the
// two sides provably agree instead of coincidentally agreeing.
func eliminationItemID(rec negknow.Record) dag.NodeID { return dag.NodeID(rec.ID) }

// applySelection filters and reorders kept according to sel, and reports what the selection put
// beyond reach.
//
// With a nil sel it returns kept and no drops, unchanged and unsorted — the rollback path. With a
// selection it returns exactly the records sel admitted, in sel.Keep's order, plus one reported
// drop per archived record and one overflow drop when the selection overflowed.
//
// A Keep entry naming a record that is not in kept is IGNORED rather than treated as an error.
// The two sets are computed at different moments — the selector ran before Build, and the stale
// filter runs inside it — so a record that was admitted and then dropped for staleness is an
// expected disagreement, not a corrupt selection. The reverse case, a kept record that the
// selection mentions nowhere, is the one that matters: it is neither injected nor reported by the
// selector, so it is reported here as an archive-only drop rather than vanishing.
func applySelection(kept []negknow.Record, sel *SelectionOutcome) ([]negknow.Record, []checkpoint.DropEntry) {
	if sel == nil {
		return kept, nil
	}

	byID := make(map[dag.NodeID]negknow.Record, len(kept))
	for _, rec := range kept {
		byID[eliminationItemID(rec)] = rec
	}

	admitted := make([]negknow.Record, 0, len(sel.Keep))
	seen := make(map[dag.NodeID]bool, len(sel.Keep))
	for _, id := range sel.Keep {
		if seen[id] {
			continue // at most one representation per item; a repeated id is not a second copy
		}
		seen[id] = true
		if rec, ok := byID[id]; ok {
			admitted = append(admitted, rec)
		}
	}

	var drops []checkpoint.DropEntry
	for _, id := range sel.Archive {
		if seen[id] {
			continue // chosen wins over archived: an item is delivered once or not at all
		}
		seen[id] = true
		if _, ok := byID[id]; !ok {
			continue // not a candidate this build considered
		}
		drops = append(drops, checkpoint.DropEntry{
			Kind: dropKindArchive, ID: string(id), Detail: archiveRecoveryDetail,
		})
	}

	// Anything the selection mentioned nowhere is still accounted for. Sorting the remainder by id
	// keeps the report deterministic without imposing an order on the selector's own Keep list.
	var unmentioned []dag.NodeID
	for id := range byID {
		if !seen[id] {
			unmentioned = append(unmentioned, id)
		}
	}
	sort.Slice(unmentioned, func(i, j int) bool { return unmentioned[i] < unmentioned[j] })
	for _, id := range unmentioned {
		drops = append(drops, checkpoint.DropEntry{
			Kind: dropKindArchive, ID: string(id), Detail: archiveRecoveryDetail,
		})
	}

	if sel.Overflow {
		detail := "OVERFLOW: representation selection could not carry a mandatory record at any " +
			"representation; it is archived and recoverable — call recall(id) or dropped()"
		if sel.Reason != "" {
			detail += " (" + oneLine(sel.Reason) + ")"
		}
		drops = append(drops, checkpoint.DropEntry{Kind: dropKindOverflow, ID: sel.Item, Detail: detail})
	}
	return admitted, drops
}
