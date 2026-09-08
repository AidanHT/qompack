package analyzer

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/store"
)

// This file is the projection layer between the dependence DAG and the two shapes selection is
// expressed over: §5.12's Block, which prices a node, and contract §2's Candidate, which prices
// the several ways one node's content could be DELIVERED.
//
// One rule runs through all of it and is the reason several functions return core.ErrDegraded
// where a simpler implementation would return a clean zero value: an empty answer and an
// unestablished answer are different facts, and this package is not permitted to let the second
// be read as the first. A Block whose supersession status could not be read is not a current
// Block; a Candidate whose dependency closure could not be computed does not depend on nothing;
// and a node that no edge connects to the slice has not thereby been shown irrelevant (plan §2,
// contract §6).

// maxRequiresClosure bounds one representation's transitive dependency closure.
//
// The bound exists for two independent reasons and either alone would justify it. dag.Graph is an
// append-only log whose acyclicity is a property of its BUILDERS rather than of its loader — a
// hand-edited or torn deps.jsonl can present a cycle — so an unbounded walk is an unbounded walk
// on untrusted input. And a closure larger than this is not a dependency set a selector can
// satisfy inside any realistic budget anyway, so truncating it and saying so is strictly more
// useful than computing it in full.
const maxRequiresClosure = 512

// defaultCandidateWeight is the objective weight an item gets when the caller named none.
//
// It is 1 rather than 0 because Candidate.Weight multiplies coverage in contract §3's objective:
// at 0 an unnamed item contributes nothing however completely it is delivered, so a caller that
// simply had no weighting policy yet would silently get a selector that drops everything it did
// not explicitly ask for. Uniform weighting is a stated default; zero weighting is a silent one.
const defaultCandidateWeight = 1.0

// NewBlocks projects DAG nodes onto the §5.12 Blocks a Selector is constructed over, reading each
// tool-use node's supersession status back out of s.
//
// The result is sorted by ID ascending and deduplicated. Both matter: contract §3 sorts candidates
// by Item before its greedy loop, and a node id that appeared twice would be counted twice by
// every downstream sum — its tokens charged twice against one budget, its coverage credited twice
// against one objective.
//
// # Why a store read at all
//
// Every other Block field is already on the node. Superseded is not: §8.1 item 3 records it on
// store.ToolUseRecord.Status, because the fact outlives the process that discovered it and the DAG
// carries the same fact only as an EdgeSupersedes whose direction encodes something else. Reading
// it here is what lets a selector treat a superseded read as the first eviction candidate §8.1
// says it is.
//
// # Why an unreadable status is core.ErrDegraded rather than a false
//
// Block.Superseded is a two-valued field over a three-valued question: replaced, not replaced, and
// not established. A nil store, a record the index does not hold, a read the store refuses — each
// leaves the third answer, and writing `false` for it would state "this read is current" on
// exactly the evidence that says nothing at all. So the blocks are returned in full, the field is
// left false because there is nowhere else to put it, and the caller is handed core.ErrDegraded
// naming how many nodes are affected. This is internal/negknow's depCoverage rule applied to the
// same kind of gap: a failed comparison degrades COVERAGE, not the answer.
//
// Nodes that cannot be superseded at all — a file anchor, an assistant turn, a decision — are
// covered trivially and never contribute to that count.
func NewBlocks(ctx context.Context, s store.Store, nodes []dag.Node) ([]Block, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	seen := make(map[dag.NodeID]struct{}, len(nodes))
	blocks := make([]Block, 0, len(nodes))
	uncovered := 0

	for _, n := range nodes {
		if n.ID == "" {
			continue
		}
		if _, dup := seen[n.ID]; dup {
			continue
		}
		seen[n.ID] = struct{}{}

		b := Block{
			ID:        n.ID,
			Pos:       n.Pos,
			Tokens:    n.Tokens,
			Kind:      n.Kind,
			Root:      n.Root,
			Ephemeral: n.Ephemeral,
		}
		switch superseded, known := supersessionOf(ctx, s, n); {
		case !known:
			uncovered++
		default:
			b.Superseded = superseded
		}
		blocks = append(blocks, b)
	}

	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })

	if uncovered > 0 {
		return blocks, fmt.Errorf(
			"%w: supersession status is unestablished for %d of %d nodes; Block.Superseded is false "+
				"for them because the field has no third value, not because they are current",
			core.ErrDegraded, uncovered, len(blocks))
	}
	return blocks, nil
}

// supersessionOf reports whether n's tool use was replaced, and whether that is known at all.
//
// Only tool-use and tool-result nodes carry the question: both key on the same core.ToolUseID —
// the NodeID prefix is what distinguishes them (dag/nodeid.go) — so both resolve to the one index
// record that holds the status. Every other kind returns (false, true): a file anchor is not a
// read that another read can replace, so "not superseded" is established rather than unknown.
//
// core.ErrNotFound is treated as UNKNOWN rather than as "not superseded". A tool use the store has
// no record of is one this scan cannot speak for; SP05-D1 means a drain aborted by idle-budget
// expiry can leave exactly that gap, and it is the direction §12 rates High.
func supersessionOf(ctx context.Context, s store.Store, n dag.Node) (superseded, known bool) {
	kind, key, ok := dag.ParseNodeID(n.ID)
	if !ok || (kind != dag.KindToolUse && kind != dag.KindToolResult) {
		return false, true
	}
	if s == nil {
		return false, false
	}
	rec, err := s.ToolUse(ctx, core.ToolUseID(key))
	if err != nil {
		return false, false
	}
	return rec.Status == store.StatusSuperseded, true
}

// RepresentationCosts prices the three DELIVERABLE representation kinds of contract §2 relative to
// an item's own token cost.
//
// Every number here is an UNCALIBRATED ESTIMATE and is named as one. SP-15's own blocker
// M5-U15-representation-overhead says so in the plan: the assembled model is not calibrated
// against provider-reported usage yet, per-chunk sums do not establish exact provider tokens, and
// until they are these are estimates that must be allowed to overflow rather than figures a
// consumer may serialize against. Contract §2's rule is what keeps that honest — AssembledCost is
// the WHOLE cost of delivering a representation, wrapper and handle included, so the number the
// objective spends is the number the serializer will pay, even while the number itself is a guess.
type RepresentationCosts struct {
	// Wrapper is the serialized overhead charged to EVERY representation: the envelope, the
	// provenance line and the drop-report entry that go with it. Charging it once per
	// representation rather than once per proposal is what stops a selector from fitting n items
	// into a budget that only holds n-1 of them plus their wrappers.
	Wrapper core.Tokens
	// Handle is what a RepPointer delivers instead of content: the durable id the retrieval layer
	// resolves. §4.4's "pointers, never contents" is only cheaper than a span if the handle is
	// priced, so it is.
	Handle core.Tokens
	// CapsuleShare is a capsule's assembled cost as a fraction of the exact span's, in (0,1].
	CapsuleShare float64
	// CapsuleCoverage is a capsule's saturating contribution to its item, in [0,1]. It is lower
	// than CapsuleShare on purpose: a summary that costs a quarter as much does not thereby carry
	// a quarter of the item's usefulness, and pricing coverage above cost would make the selector
	// prefer capsules to spans by construction.
	CapsuleCoverage float64
	// PointerCoverage is a pointer's saturating contribution to its item, in [0,1]. It is small
	// and deliberately not zero: a handle the model can resolve is worth more than nothing and far
	// less than the content.
	PointerCoverage float64
}

// DefaultRepresentationCosts returns the estimates NewCandidates uses when a caller supplies none.
//
// They are round numbers because they are estimates and rounding them advertises that. Nothing
// here duplicates a configuration default, and none of the five is drawn from §11.6's forbidden
// literal set.
func DefaultRepresentationCosts() RepresentationCosts {
	return RepresentationCosts{
		Wrapper:         24,
		Handle:          48,
		CapsuleShare:    0.25,
		CapsuleCoverage: 0.6,
		PointerCoverage: 0.15,
	}
}

// CandidateOptions configures NewCandidates. Every field is optional; the zero value produces
// candidates priced at DefaultRepresentationCosts, uniformly weighted, none mandatory, all
// qualified QualUncertain, and with core.ErrDegraded reported for the missing dependency closure.
type CandidateOptions struct {
	// Graph is the dependence DAG the Requires closure is computed over. A nil Graph does not mean
	// "no dependencies" — see NewCandidates for what it does mean.
	Graph dag.Graph
	// Weights is the per-item objective weight. A missing entry means defaultCandidateWeight; a
	// negative one is clamped to 0, since contract §2 states Weight >= 0.
	Weights map[dag.NodeID]float64
	// Mandatory names the items the consumer must carry. Contract §3 turns a mandatory item that
	// does not fit into an explicit overflow with an archive-recovery path, never a silent drop.
	Mandatory map[dag.NodeID]bool
	// Qualification is how much authority each item's evidence still carries. Mapping a
	// negknow.Record's status onto this enum is the DAEMON's job, not this package's (contract
	// §1), so an item absent from this map is QualUncertain rather than QualCurrent.
	Qualification map[dag.NodeID]Qualification
	// Costs prices the representations. The zero value means DefaultRepresentationCosts.
	Costs RepresentationCosts
}

// NewCandidates turns DAG nodes into the contract §2 Candidate set a selector chooses over.
//
// Each node becomes one Candidate carrying the representations that are actually available for it:
// always RepExactSpan, and additionally RepCapsule and RepPointer when the node has a content root
// to summarize or to point at. A node with no root gets neither, because a capsule of nothing and
// a handle that resolves to nothing are not cheaper deliveries of the item — they are empty
// deliveries the selector would spend budget on.
//
// RepArchiveOnly is deliberately NOT among them. It is an OUTCOME, not a candidate representation:
// contract §3 makes overflow the case where a mandatory item does not fit "including at
// RepPointer", and offering a zero-cost archive representation would make that case unreachable
// and turn every overflow into a silent success.
//
// # Provenance, and why nothing here is QualCurrent by default
//
// Prov.Root is the node's own content root on every representation of an item, capsule included:
// contract §2 requires the ORIGINAL evidence root, and a derivative that overwrote it would break
// the chain back to the first observation that §4.4 needs to stay recoverable. Prov.Derived is set
// only on the capsule, which is the representation that is a summary rather than the thing.
//
// Prov.Qualification comes from the caller or is QualUncertain. This package cannot import negknow
// (§3.2) and therefore cannot establish that any record is still applicable; asserting QualCurrent
// on its own authority would manufacture exactly the false "already tried" that inverts negative
// knowledge from asset to liability. QualUncertain has Active() false, so an unqualified candidate
// travels, renders and stays recoverable while constraining nothing.
//
// # A nil Graph is core.ErrDegraded, not an empty closure
//
// Representation.Requires is a claim: "delivering this is not enough on its own". With no graph
// there is nothing to compute that claim from, and an empty Requires would state the opposite
// claim — that the item stands alone — on no evidence. So the candidates come back fully formed
// with empty closures AND core.ErrDegraded, and a caller that ignores the error gets a selector
// which may propose an item without its dependencies rather than one that silently pretends there
// were none. The same error is returned when a closure hit maxRequiresClosure.
//
// The result is sorted by Item ascending and each Reps list by Kind ascending, matching contract
// §3's `(gain desc, Item asc, Kind asc)` tie-break so that no ordering the selector relies on has
// to be re-established there.
func NewCandidates(nodes []dag.Node, o CandidateOptions) ([]Candidate, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	costs := o.Costs
	if costs == (RepresentationCosts{}) {
		costs = DefaultRepresentationCosts()
	}

	seen := make(map[dag.NodeID]struct{}, len(nodes))
	out := make([]Candidate, 0, len(nodes))
	truncated := 0

	for _, n := range nodes {
		if n.ID == "" {
			continue
		}
		if _, dup := seen[n.ID]; dup {
			continue
		}
		seen[n.ID] = struct{}{}

		requires, cut := requiresClosure(o.Graph, n.ID)
		if cut {
			truncated++
		}
		prov := Provenance{
			Origin:        n.ID,
			Root:          n.Root,
			Qualification: qualificationOf(o.Qualification, n.ID),
			Turn:          n.Turn,
		}
		out = append(out, Candidate{
			Item:      n.ID,
			Pos:       n.Pos,
			Weight:    weightOf(o.Weights, n.ID),
			Mandatory: o.Mandatory[n.ID],
			Reps:      representationsOf(n, requires, prov, costs),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })

	switch {
	case o.Graph == nil:
		return out, fmt.Errorf(
			"%w: no dependence graph was supplied, so every Requires is empty because it is "+
				"UNESTABLISHED, not because the %d items stand alone", core.ErrDegraded, len(out))
	case truncated > 0:
		return out, fmt.Errorf(
			"%w: the dependency closure of %d of %d items was truncated at %d entries; their "+
				"Requires is a subset of the real one", core.ErrDegraded, truncated, len(out), maxRequiresClosure)
	}
	return out, nil
}

// qualificationOf resolves one item's qualification, defaulting to QualUncertain.
//
// The lookup is two-valued on purpose, and this is the one place in the file where a map index
// would have been an outright bug: Qualification's zero value is QualCurrent — types.go says so,
// and it is fixed there because the enum's order is the frozen contract §2 order — so
// `o.Qualification[item]` silently reads "absent from the map" as "authoritative and applicable
// right now". That is precisely the false already_tried §12 rates High, minted by a missing map
// entry. The comma-ok form is what keeps the analyzer from asserting currency it cannot establish.
func qualificationOf(quals map[dag.NodeID]Qualification, item dag.NodeID) Qualification {
	if q, ok := quals[item]; ok {
		return q
	}
	return QualUncertain
}

// weightOf resolves one item's objective weight, clamping a negative caller value to 0 rather than
// letting it subtract coverage — contract §2 states Weight >= 0, and a negative weight would make
// delivering an item lower the objective, which is not a preference the objective can express.
func weightOf(weights map[dag.NodeID]float64, item dag.NodeID) float64 {
	w, ok := weights[item]
	if !ok {
		return defaultCandidateWeight
	}
	if w < 0 || math.IsNaN(w) {
		return 0
	}
	return w
}

// representationsOf builds the compatible representations of one node, in Kind order.
func representationsOf(n dag.Node, requires []dag.NodeID, prov Provenance, costs RepresentationCosts) []Representation {
	span := Representation{
		Item:          n.ID,
		Kind:          RepExactSpan,
		Coverage:      1,
		AssembledCost: n.Tokens + costs.Wrapper,
		Requires:      requires,
		Prov:          prov,
	}
	reps := []Representation{span}
	if n.Root.IsZero() {
		return reps
	}

	capsuleProv := prov
	capsuleProv.Derived = true
	reps = append(reps,
		Representation{
			Item:          n.ID,
			Kind:          RepCapsule,
			Coverage:      clamp01(costs.CapsuleCoverage),
			AssembledCost: scaleTokens(n.Tokens, costs.CapsuleShare) + costs.Wrapper,
			Requires:      requires,
			Prov:          capsuleProv,
		},
		Representation{
			Item:          n.ID,
			Kind:          RepPointer,
			Coverage:      clamp01(costs.PointerCoverage),
			AssembledCost: costs.Handle + costs.Wrapper,
			Requires:      requires,
			Prov:          prov,
		},
	)
	return reps
}

// scaleTokens prices a capsule at share of an exact span's content cost.
//
// It rounds UP and never below one token for a non-empty item. Rounding a cost down is the one
// direction that makes a proposal overrun its budget at serialization time, which contract §3
// forbids outright; a capsule that is one token dearer than modelled costs a selector one item of
// headroom, and a capsule that is one token cheaper than real costs the caller a failed assembly.
func scaleTokens(t core.Tokens, share float64) core.Tokens {
	if t <= 0 || share <= 0 {
		return 0
	}
	scaled := core.Tokens(math.Ceil(float64(t) * share))
	if scaled < 1 {
		return 1
	}
	if scaled > t {
		return t
	}
	return scaled
}

// isDependenceEdge reports whether k states that one node's content is not intelligible without
// another's.
//
// Exactly three kinds qualify, and they are the three SP-07 D-3 gives a 1.00 relevance multiplier
// because no information is lost across the hop: produces (a tool result IS its tool use's
// output), consumes (the assistant read that result verbatim) and explains (evidence to decision,
// §4.4's highest-value non-reconstructible link).
//
// The five that do NOT qualify are association, ordering or eviction facts, and reading any of
// them as a dependency would be a different and much stronger claim than the edge makes:
// shared_file and shared_symbol say two nodes touched the same state, which is §8.1 item 4's
// approximate relation graph; seq and control say one came after another, and §6.4 is explicit
// that recency is a proxy for relevance rather than relevance itself; supersedes points AT the
// replacement, so following it as a dependency would drag every superseded read back in behind the
// read that replaced it — the exact opposite of "first candidate for eviction".
//
// Their exclusion from the closure is not a claim that they are irrelevant. NewRelations preserves
// every one of them, kind and all, for precisely that reason.
func isDependenceEdge(k dag.EdgeKind) bool {
	switch k {
	case dag.EdgeProduces, dag.EdgeConsumes, dag.EdgeExplains:
		return true
	case dag.EdgeSequence, dag.EdgeSharedFile, dag.EdgeSharedSymbol, dag.EdgeSupersedes,
		dag.EdgeControlOnly, dag.EdgeInvalid:
		return false
	default:
		// An edge kind this build does not know is not assumed to be a dependency. A future kind
		// added to §5.9 gets whatever treatment its own subplan chooses; it does not silently
		// acquire the strongest one available.
		return false
	}
}

// requiresClosure returns item's transitively closed dependency set, sorted ascending, and reports
// whether the walk was cut short at maxRequiresClosure.
//
// It walks IN-edges. That is the direction the §8.1 item 4 builders emit: a tool use PRODUCES its
// result, so the result's dependency is the edge's From; evidence EXPLAINS a decision, so the
// decision's dependency is again the From. Walking Out instead would return what each node
// influenced, which is a different and much larger question.
//
// item itself is never included. A representation that required its own delivery would be
// unsatisfiable by construction, and a cycle in an untrusted log is exactly how one would appear.
func requiresClosure(g dag.Graph, item dag.NodeID) (requires []dag.NodeID, truncated bool) {
	if g == nil {
		return nil, false
	}

	visited := map[dag.NodeID]struct{}{item: {}}
	frontier := []dag.NodeID{item}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for _, e := range g.In(cur) {
			if !isDependenceEdge(e.Kind) {
				continue
			}
			if _, done := visited[e.From]; done {
				continue
			}
			if len(requires) >= maxRequiresClosure {
				truncated = true
				break
			}
			visited[e.From] = struct{}{}
			requires = append(requires, e.From)
			frontier = append(frontier, e.From)
		}
		if truncated {
			break
		}
	}

	sort.Slice(requires, func(i, j int) bool { return requires[i] < requires[j] })
	return requires, truncated
}

// Relation is one DAG edge incident to a candidate item, preserved verbatim with the kind it
// actually carries.
type Relation struct {
	// Item is the candidate this edge is incident to.
	Item dag.NodeID
	// Other is the edge's other endpoint.
	Other dag.NodeID
	// Kind is the edge's kind, unchanged — including a kind this build has no rule for.
	Kind dag.EdgeKind
	// Inbound reports the direction: true when the edge runs Other -> Item.
	Inbound bool
	// Dependence reports whether requiresClosure treats this kind as a dependency. It is recorded
	// rather than recomputed by the reader so that a later change to isDependenceEdge shows up as
	// a diff in preserved evidence instead of silently reinterpreting it.
	Dependence bool
}

// Relations is the approximate relation graph around a candidate set: EVERY edge incident to any
// of the items, whether or not this package knows what to do with it.
//
// # Why the unused edges are kept rather than filtered
//
// Shared files and tool ordering form an APPROXIMATE relation graph (plan §2, contract §6). The
// dependency closure reads three edge kinds and ignores five, and the five it ignores are exactly
// the ones a later reader is most likely to want: a shared_file edge is the trace of two turns
// touching one file, which is evidence about coupling even though it is not evidence about
// necessity. Filtering them out at this layer would destroy the record and leave the closure
// looking like the whole truth.
//
// # What an edge here does and does not mean
//
// An edge is an ASSOCIATION. A shared_file edge between two items says they touched the same path;
// it does not establish that either is relevant to the other, that the two contents overlap, or
// that keeping one licenses dropping the other. internal/analyzer/testdata/diagnostics holds a
// committed counterexample for exactly this (gate M5-G15-B): two tool uses joined by a shared_file
// edge whose contents have nothing to do with one another.
//
// And the converse is the more dangerous direction, so it is stated too: the ABSENCE of an edge
// between two items establishes NOTHING. The graph is built from what the observer saw, SP05-D1
// means it may have missed an edit, and §6.4's thin slicing drops control edges by design — so
// "not connected in this graph" is not "unrelated", and no caller may treat content outside a
// slice as shown to be irrelevant.
type Relations struct {
	// Edges is every incident edge, sorted by (Item, Other, Kind, Inbound) ascending.
	Edges []Relation
	// Items is the item set the edges were collected for, sorted ascending, so a reader can tell
	// "this item had no edges" from "this item was never asked about".
	Items []dag.NodeID
}

// NewRelations collects every DAG edge incident to items, in both directions, losing none of them.
//
// A nil graph yields an empty Relations whose Items is still populated: "asked, and nothing could
// be read" and "never asked" are different, and Items is what tells them apart.
func NewRelations(g dag.Graph, items []dag.NodeID) Relations {
	rel := Relations{Items: make([]dag.NodeID, 0, len(items))}
	seenItem := make(map[dag.NodeID]struct{}, len(items))
	for _, id := range items {
		if id == "" {
			continue
		}
		if _, dup := seenItem[id]; dup {
			continue
		}
		seenItem[id] = struct{}{}
		rel.Items = append(rel.Items, id)
	}
	sort.Slice(rel.Items, func(i, j int) bool { return rel.Items[i] < rel.Items[j] })
	if g == nil {
		return rel
	}

	seenEdge := make(map[Relation]struct{})
	add := func(r Relation) {
		if _, dup := seenEdge[r]; dup {
			return
		}
		seenEdge[r] = struct{}{}
		rel.Edges = append(rel.Edges, r)
	}
	for _, id := range rel.Items {
		for _, e := range g.In(id) {
			add(Relation{Item: id, Other: e.From, Kind: e.Kind, Inbound: true, Dependence: isDependenceEdge(e.Kind)})
		}
		for _, e := range g.Out(id) {
			add(Relation{Item: id, Other: e.To, Kind: e.Kind, Dependence: isDependenceEdge(e.Kind)})
		}
	}
	sort.Slice(rel.Edges, func(i, j int) bool {
		a, b := rel.Edges[i], rel.Edges[j]
		switch {
		case a.Item != b.Item:
			return a.Item < b.Item
		case a.Other != b.Other:
			return a.Other < b.Other
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		default:
			return a.Inbound && !b.Inbound
		}
	})
	return rel
}
