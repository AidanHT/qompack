package checkpoint

// This file is ExtractDecisions (00-ARCHITECTURE.md §5.14, plan §9): the sole producer of tier-2
// Decisions and therefore of core.DecisionID, which is what makes the `why(decision_id)` MCP tool
// answerable (§5.16). Decisions are derived from three durable sources — EdgeExplains chains in
// the dependence DAG, elimination records with a rejected alternative, and pins recorded as
// decisions — merged by id, ranked by backward-slice relevance, capped, and emitted back into the
// DAG so the next slice can rank them too. Nothing here reads live context text: every byte comes
// out of SourceSet's seams, which is §4.6's never-compress-a-compression invariant doing its job.

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/tokens"
)

// everyNodeKind lists the nine real node kinds, in declaration order, for routing the §9
// full-graph scan through nodesInRange with no kind filtered away.
var everyNodeKind = []dag.NodeKind{
	dag.KindToolUse, dag.KindToolResult, dag.KindAssistant, dag.KindUserPrompt,
	dag.KindFile, dag.KindSymbol, dag.KindDecision, dag.KindElimination, dag.KindSegment,
}

// maxExplainingReadBytes caps how much of an explaining node's stored content a derivation reads
// (§9: 32 KiB). The why is at most two sentences capped at maxWhyRunes, so anything past the
// first pages of a large tool result can never reach the artifact; reading it anyway would put an
// unbounded store read on the checkpoint path for no output.
const maxExplainingReadBytes = 32 << 10

// The §9 rune caps, applied at rune boundaries: a decision's what is a headline, its why a short
// rationale, and both must stay well under the tier-2 budget no matter what the transcript held.
const (
	maxWhatRunes = 160
	maxWhyRunes  = 400
)

// readCacheEntries bounds the per-extraction text cache. Evidence nodes repeat across explains
// edges — one assistant turn explains several targets — so caching by content root keeps the pass
// linear in DISTINCT evidence rather than in edges, while the cap (256 × 32 KiB = 8 MiB at worst)
// keeps a pathological graph from pinning the daemon's memory. Only successful reads are cached,
// so the read-error counter still counts once per skipped decision.
const readCacheEntries = 256

// The source (c) spellings of plan §9: a pin whose Source is decisionPinSource is an explicitly
// recorded decision, split into what/why on the first decisionPinSeparator; a pin without the
// separator keeps its whole text as what and takes defaultPinWhy as its rationale.
const (
	decisionPinSource    = "decision"
	decisionPinSeparator = " because "
	defaultPinWhy        = "pinned by the user as a standing decision"
)

// normalizeWS collapses every run of unicode.IsSpace to a single U+0020 and trims both ends —
// the same rule pins applies to its own id seeds. It is this package's own copy deliberately:
// pins' normalizeWS is unexported, and importing pins for a helper would tie the decision seed's
// stability to another package's internals.
//
// The already-normal fast path matters: MintDecisionID runs this twice per candidate and an
// extraction over a session-sized graph derives thousands of candidates, almost all of whose
// texts are already single-spaced — returning s unchanged skips the builder and its allocation
// on the §9 benchmark's hottest slice.
func normalizeWS(s string) string {
	if isNormalWS(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	pending := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pending = false
		b.WriteRune(r)
	}
	return b.String()
}

// isNormalWS reports whether s is already in normalizeWS's output form: no leading or trailing
// whitespace, no whitespace other than U+0020, and no two spaces in a row. The loop touches each
// byte once and decodes a rune only for non-ASCII, so the common all-ASCII text costs a plain
// byte scan.
func isNormalWS(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return false
	}
	prevSpace := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ':
			if prevSpace {
				return false
			}
			prevSpace = true
			i++
		case c < utf8.RuneSelf:
			if c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' {
				return false
			}
			prevSpace = false
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if unicode.IsSpace(r) {
				return false
			}
			prevSpace = false
			i += size
		}
	}
	return true
}

// MintDecisionID builds the SEED and delegates. core.NewDecisionID is the shipped sole minter of
// core.DecisionID (internal/core/ids.go); it owns the dec_ prefix, the core.DomainDecision
// domain and the digest width. Re-deriving any of those here would fork a wire format that
// dag.DecisionNode ids and every stored checkpoint already depend on.
//
// The seed normalizes whitespace in what and why so a rewrapped rationale still names the same
// decision, and separates the parts with NUL so ("a", "bc") and ("ab", "c") can never collide.
func MintDecisionID(what, why string, evidence core.Hash) core.DecisionID {
	seed := normalizeWS(what) + "\x00" + normalizeWS(why) + "\x00" + evidence.String()
	return core.NewDecisionID([]byte(seed))
}

// ExtractDecisions mints tier-2 decisions from src, considering everything recorded at or after
// turn from: EdgeExplains chains in the dependence DAG, elimination records that carry an
// alternatives-rejected shape, and pins explicitly recorded as decisions (plan §9).
//
// Candidates are merged by Decision.ID — first wins, lowest Turn first — then ranked by backward-
// slice relevance from the latest user prompt and the current segment (score descending, then
// Turn descending, then ID ascending, for total determinism), truncated to maxDraftDecisions,
// and emitted into the DAG as KindDecision nodes with an explains edge from their evidence node.
//
// Failures degrade rather than propagate: an unreadable explaining node skips one decision and
// counts on checkpoint.decision_read_error, an unavailable ledger or pin store skips its source
// with a Warn, and a failed emission is logged and ignored. Only a SourceSet.Validate failure or
// a ctx cancellation returns an error.
func ExtractDecisions(ctx context.Context, src SourceSet, from core.TurnIndex) ([]Decision, error) {
	return extractDecisions(ctx, src, from, "")
}

// extractDecisions is ExtractDecisions for one session's draft: source (b) considers only the
// eliminations that draft carries — the session's own and the project-scoped ones (carriedBy) —
// because another session's session-scoped elimination is not this session's negative knowledge
// and must not surface as this session's decision. An empty session keeps ExtractDecisions' own
// rule of every record the ledger holds.
func extractDecisions(ctx context.Context, src SourceSet, from core.TurnIndex, s core.SessionID) ([]Decision, error) {
	if err := src.Validate(); err != nil {
		return nil, err
	}
	x := &decisionExtractor{src: src, from: from, session: s, texts: make(map[core.Hash]string)}
	return x.run(ctx)
}

// carriedBy reports whether r belongs in session s's checkpoint: its own records, and every
// project-scoped one (§8.3 item 5). It is the one statement of the rule the draft's eliminated[]
// and its elimination-sourced decisions both follow.
func carriedBy(r negknow.Record, s core.SessionID) bool {
	return r.Session == s || r.Scope == negknow.ScopeProject
}

// decisionCandidate pairs a derived Decision with the node its evidence lives at, which the DAG
// emission needs and the artifact does not carry: the explaining node for source (a), the
// elimination node for source (b), and nothing ("") for source (c).
type decisionCandidate struct {
	d        Decision
	evidence dag.NodeID
	// score is the candidate's backward-slice relevance, filled by rank. It is precomputed once
	// per candidate rather than looked up inside the sort comparator, because the lookup key is
	// dag.DecisionNode(id) — a constructor that allocates and sanitizes — and a comparator runs
	// O(n log n) times: on the §9 benchmark graph that difference alone is tens of milliseconds.
	score float32
}

// decisionExtractor carries one extraction's state: the seams, the from-turn cut, and the
// per-call evidence text cache.
type decisionExtractor struct {
	src  SourceSet
	from core.TurnIndex
	// session, when set, restricts source (b) to the records carriedBy it.
	session core.SessionID
	texts   map[core.Hash]string
}

// run is the §9 pipeline: scan, derive from the three sources, merge, rank, cap, emit.
func (x *decisionExtractor) run(ctx context.Context) ([]Decision, error) {
	// ONE full node scan, through the package's nodesInRange seam. dag.Graph exposes no kind-,
	// turn- or edge-level accessor — edges are reachable only per node — and the underlying
	// NodesAfter(0) allocates every live node per call, so the seam is called exactly once, with
	// every kind and the whole turn range, and the result is partitioned locally: latestTurn
	// needs the unfiltered maximum, and the explains walk applies its own from-cut per node.
	nodes := nodesInRange(x.src.Graph, everyNodeKind, 0, core.TurnIndex(math.MaxInt))
	latest := core.TurnIndex(0)
	for _, n := range nodes {
		if n.Turn > latest {
			latest = n.Turn
		}
	}

	cands, err := x.fromExplains(ctx, nodes)
	if err != nil {
		return nil, err
	}
	if cands, err = x.fromEliminations(ctx, cands); err != nil {
		return nil, err
	}
	if cands, err = x.fromPins(ctx, cands); err != nil {
		return nil, err
	}

	merged := mergeByID(cands)
	x.rank(ctx, merged, latest)
	if len(merged) > maxDraftDecisions {
		merged = merged[:maxDraftDecisions]
	}
	x.emit(merged)

	out := make([]Decision, len(merged))
	for i, c := range merged {
		out[i] = c.d
	}
	return out, nil
}

// fromExplains derives source (a): one decision per EdgeExplains edge at or after the from-turn,
// reached through the single node scan (nodes with Turn >= from, their Out edges filtered to
// explains), deduplicated by (From, To, Turn). Chains are followed one hop only — an explains
// edge out of an explaining node is its own decision — which keeps the pass linear in edges.
func (x *decisionExtractor) fromExplains(ctx context.Context, nodes []dag.Node) ([]decisionCandidate, error) {
	type edgeIdentity struct {
		from, to dag.NodeID
		turn     core.TurnIndex
	}
	var cands []decisionCandidate
	seen := make(map[edgeIdentity]bool)
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, extractInterrupted(err)
		}
		if n.Turn < x.from {
			continue
		}
		for _, e := range x.src.Graph.Out(n.ID) {
			if e.Kind != dag.EdgeExplains || e.Turn < x.from {
				continue
			}
			id := edgeIdentity{from: e.From, to: e.To, turn: e.Turn}
			if seen[id] {
				continue
			}
			seen[id] = true
			if c, ok := x.explainCandidate(ctx, e); ok {
				cands = append(cands, c)
			}
		}
	}
	return cands, nil
}

// explainCandidate derives one candidate from one explains edge, reporting false when the edge is
// dangling or the backing content cannot be read (the read path counts the failure).
func (x *decisionExtractor) explainCandidate(ctx context.Context, e dag.Edge) (decisionCandidate, bool) {
	expl, ok := x.src.Graph.Node(e.From)
	if !ok {
		return decisionCandidate{}, false // dangling: the explaining node was never observed
	}
	tgt, ok := x.src.Graph.Node(e.To)
	if !ok {
		return decisionCandidate{}, false
	}
	explText, ok := x.text(ctx, expl.Root)
	if !ok {
		return decisionCandidate{}, false
	}

	var what string
	if tgt.Kind == dag.KindDecision {
		// A decision this extractor emitted points back at its own evidence: emit writes the
		// candidate's Evidence hash onto the decision node as Root and adds an
		// evidence--explains-->decision edge. Walking that edge on a later pass over the same turn
		// range would mint a SECOND decision whose what and why are just the evidence text
		// restated, with a different ID, and it would take one of the maxDraftDecisions slots
		// away from a decision that says something. Skipping it is what makes extraction over an
		// already-extracted range a fixed point on the first repeat rather than the second.
		//
		// A genuine decision->decision explains chain is untouched: its two decisions were minted
		// from different evidence, so their Roots differ.
		if tgt.Root == expl.Root {
			return decisionCandidate{}, false
		}
		tgtText, ok := x.text(ctx, tgt.Root)
		if !ok {
			return decisionCandidate{}, false
		}
		what = firstSentences(tgtText, 1)
	} else {
		// §9 spells this as fmt.Sprintf("%s %s", ...); plain concatenation produces the
		// identical string without fmt's reflection on the once-per-edge hot path.
		what = verbFor(tgt.Kind) + " " + tgt.Ref
	}
	what = capRunes(what, maxWhatRunes)
	why := capRunes(firstSentences(explText, 2), maxWhyRunes)

	d := Decision{What: what, Why: why, Evidence: expl.Root, Turn: e.Turn}
	d.ID = MintDecisionID(what, why, d.Evidence)
	return decisionCandidate{d: d, evidence: expl.ID}, true
}

// fromEliminations derives source (b): every ledger record with a non-empty Approach whose turn —
// the elimination node's Turn when the graph has one, the from-turn otherwise — is at or after
// the cut becomes a rejected-alternative decision. An unavailable ledger skips the source with a
// Warn: §9 allows only Validate and cancellation to fail the extraction.
func (x *decisionExtractor) fromEliminations(ctx context.Context, cands []decisionCandidate) ([]decisionCandidate, error) {
	recs, err := x.src.Ledger.All(ctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, extractInterrupted(cerr)
		}
		pkgLog().Warn("checkpoint: decision extraction: ledger unavailable, skipping eliminations", "err", err)
		return cands, nil
	}
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return nil, extractInterrupted(err)
		}
		if x.session != "" && !carriedBy(r, x.session) {
			continue
		}
		turn := eliminationTurn(x.src.Graph, r, x.from)
		if turn < x.from {
			continue
		}
		if d, ok := eliminationDecision(r, turn); ok {
			cands = append(cands, decisionCandidate{d: d, evidence: dag.EliminationNode(r.ID)})
		}
	}
	return cands, nil
}

// eliminationTurn is the turn an elimination was recorded at — its DAG node's — or fallback when
// the graph holds no node for it.
func eliminationTurn(g dag.Graph, r negknow.Record, fallback core.TurnIndex) core.TurnIndex {
	if n, ok := g.Node(dag.EliminationNode(r.ID)); ok {
		return n.Turn
	}
	return fallback
}

// eliminationDecision is source (b)'s rule for one record: an elimination that names the approach
// it rejected is a rejected-alternative decision. ok is false for a record with no Approach.
func eliminationDecision(r negknow.Record, turn core.TurnIndex) (Decision, bool) {
	if r.Approach == "" {
		return Decision{}, false
	}
	what := fmt.Sprintf("rejected %q for %s", r.Approach, r.Target)
	d := Decision{
		What:                 what,
		Why:                  r.Reason,
		AlternativesRejected: []string{r.Approach},
		Evidence:             r.Evidence,
		Turn:                 turn,
	}
	d.ID = MintDecisionID(what, d.Why, d.Evidence)
	return d, true
}

// fromPins derives source (c): every pins.Invariant recorded with Source "decision", split into
// what/why on the first " because ". Pinned decisions carry no evidence hash and no turn of their
// own, so they take the zero hash and the from-turn. An unavailable pin store — including SP-01's
// not-yet-implemented stub — skips the source with a Warn.
func (x *decisionExtractor) fromPins(ctx context.Context, cands []decisionCandidate) ([]decisionCandidate, error) {
	invs, err := x.src.Pins.All(ctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, extractInterrupted(cerr)
		}
		pkgLog().Warn("checkpoint: decision extraction: pins unavailable, skipping decision pins", "err", err)
		return cands, nil
	}
	for _, inv := range invs {
		if inv.Source != decisionPinSource {
			continue
		}
		what, why, split := strings.Cut(inv.Text, decisionPinSeparator)
		if !split {
			what, why = inv.Text, defaultPinWhy
		}
		d := Decision{What: what, Why: why, Turn: x.from}
		d.ID = MintDecisionID(what, why, core.Hash{})
		cands = append(cands, decisionCandidate{d: d})
	}
	return cands, nil
}

// text reads the content at root through the store, capped at maxExplainingReadBytes, strips
// prior injections via fromStore, and trims surrounding whitespace so sentence extraction never
// leads with a transcript's blank lines. A failed open or read counts once on
// checkpoint.decision_read_error and reports false; successes are cached per extraction, so
// repeated evidence costs one read.
func (x *decisionExtractor) text(ctx context.Context, root core.Hash) (string, bool) {
	if s, ok := x.texts[root]; ok {
		return s, true
	}
	rc, err := x.src.Store.Open(ctx, root)
	if err == nil {
		var b []byte
		b, err = io.ReadAll(io.LimitReader(rc, maxExplainingReadBytes))
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			s := strings.TrimSpace(fromStore(b))
			if len(x.texts) < readCacheEntries {
				x.texts[root] = s
			}
			return s, true
		}
	}
	pkgMetrics().Counter("checkpoint.decision_read_error").Add(1)
	return "", false
}

// mergeByID collapses candidates sharing a Decision.ID: first wins, lowest Turn first (plan §9).
// For each id the surviving candidate is the lowest-Turn one, with source order — explains, then
// eliminations, then pins — breaking equal turns, because a strictly-lower turn is the only thing
// that displaces an earlier candidate. One pass, no sort: the survivors' order is re-established
// by rank's total order anyway.
func mergeByID(cands []decisionCandidate) []decisionCandidate {
	best := make(map[core.DecisionID]int, len(cands))
	merged := make([]decisionCandidate, 0, len(cands))
	for _, c := range cands {
		i, ok := best[c.d.ID]
		if !ok {
			best[c.d.ID] = len(merged)
			merged = append(merged, c)
			continue
		}
		if c.d.Turn < merged[i].d.Turn {
			merged[i] = c
		}
	}
	return merged
}

// rank orders cands by the §9 total order: backward-slice score descending (absent scores are 0),
// then Turn descending (recent first), then ID ascending for total determinism.
func (x *decisionExtractor) rank(ctx context.Context, cands []decisionCandidate, latest core.TurnIndex) {
	scores := x.sliceScores(ctx, latest)
	for i := range cands {
		cands[i].score = scores[dag.DecisionNode(cands[i].d.ID)]
	}
	// The comparator is a total order (ID ascending is the final tiebreak), so the plain sort is
	// already deterministic and stability buys nothing.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].d.Turn != cands[j].d.Turn {
			return cands[i].d.Turn > cands[j].d.Turn
		}
		return cands[i].d.ID < cands[j].d.ID
	})
}

// sliceScores runs §9's ranking slice: a thin backward slice from the latest user prompt and the
// current segment, with §9's spelled-out options (Decay 0.85, MaxNodes 5000, Deadline 250ms). A
// slice failure degrades to recency ordering — nil scores read as all-zero — because ranking is
// best-effort and must never fail the extraction.
func (x *decisionExtractor) sliceScores(ctx context.Context, latest core.TurnIndex) map[dag.NodeID]float32 {
	criteria := []dag.NodeID{dag.UserPromptNode(latest)}
	if cur, ok := x.currentSegment(ctx, latest); ok {
		criteria = append(criteria, dag.SegmentNode(cur))
	}
	slice, err := x.src.Graph.BackwardSlice(criteria, dag.SliceOptions{
		Thin:     true,
		Decay:    0.85,
		MaxNodes: 5000,
		Deadline: 250 * time.Millisecond,
	})
	if err != nil {
		pkgLog().Warn("checkpoint: decision ranking slice failed; falling back to recency order", "err", err)
		return nil
	}
	return slice.Scores
}

// currentSegment resolves §9's "cur from src.Segments.Current". SegmentLog.Current takes a
// core.SessionID that ExtractDecisions' frozen signature does not carry, so the session is
// recovered from the segment log itself: the newest segment on record names the session whose
// open segment is the current work. When that session has no open segment the newest segment
// stands in, and when the log is empty the criterion is dropped — BackwardSlice skips unknown
// criteria, so both fallbacks degrade the ranking, never the extraction.
func (x *decisionExtractor) currentSegment(ctx context.Context, latest core.TurnIndex) (core.SegmentID, bool) {
	segs, err := x.src.Segments.Range(ctx, 0, latest)
	if err != nil || len(segs) == 0 {
		return 0, false
	}
	newest := segs[0]
	for _, s := range segs[1:] {
		if s.ID > newest.ID {
			newest = s
		}
	}
	if cur, err := x.src.Segments.Current(ctx, newest.Session); err == nil {
		return cur.ID, true
	}
	return newest.ID, true
}

// emit writes every returned decision into the DAG: a KindDecision node and, when the source has
// one, an explains edge from its evidence node (plan §9).
//
// AddNode is a field-wise merge and AddEdge folds max Weight / min Turn — SP-07's shipped
// semantics, not the no-op §9 assumed — so everything emitted is a stable value: the same Turn,
// Ref, Root, Tokens and Weight on every extraction, making re-emission a fixed point of the graph
// rather than an accumulation. TS is deliberately zero (merge-neutral): §9's nowMilli has no
// source — SourceSet carries no clock and §5.14's signature admits none — and a wall-clock TS
// would rewrite the node on every pass. Emission failures are logged at Warn and never fail the
// extraction: the decisions are already minted, and the artifact must not lose them to a full
// graph log.
func (x *decisionExtractor) emit(cands []decisionCandidate) {
	for _, c := range cands {
		id := dag.DecisionNode(c.d.ID)
		want := dag.Node{
			ID:     id,
			Kind:   dag.KindDecision,
			Turn:   c.d.Turn,
			Ref:    string(c.d.ID),
			Root:   c.d.Evidence,
			Tokens: x.src.Tokens.EstimateString(c.d.What+" "+c.d.Why, tokens.ClassProse),
		}
		// Emit only when the merge would change something. The shipped AddNode appends a log
		// record and dirties the position index even when the field-wise merge is a value no-op,
		// so without this check every re-extraction would grow dag/deps.jsonl by one line per
		// decision and force the next NodesAfter to re-sort the whole index. The skip condition
		// compares exactly the fields emitted here — Pos, TS and Ephemeral are emitted zero and
		// zero never displaces a stored value — so skipping is observationally identical to
		// calling AddNode, and repeated extraction becomes a fixed point of the log as well as of
		// the graph.
		if prev, ok := x.src.Graph.Node(id); !ok || prev.Kind != want.Kind || prev.Turn != want.Turn ||
			prev.Ref != want.Ref || prev.Root != want.Root || prev.Tokens != want.Tokens {
			if err := x.src.Graph.AddNode(want); err != nil {
				pkgLog().Warn("checkpoint: decision node emission failed", "id", string(c.d.ID), "err", err)
			}
		}
		if c.evidence == "" {
			continue // source (c): a pinned decision has no evidence node to point from
		}
		// AddEdge needs no such guard: its fold already appends a record only `if changed` and
		// never dirties the position index on the fold path.
		e := dag.Edge{From: c.evidence, To: id, Kind: dag.EdgeExplains, Weight: 1, Turn: c.d.Turn}
		if err := x.src.Graph.AddEdge(e); err != nil {
			pkgLog().Warn("checkpoint: decision edge emission failed", "id", string(c.d.ID), "err", err)
		}
	}
}

// verbFor renders a non-decision explains target as §9's action phrase: what was done to the
// referent.
func verbFor(k dag.NodeKind) string {
	switch k {
	case dag.KindFile:
		return "changed"
	case dag.KindToolUse:
		return "ran"
	case dag.KindSymbol:
		return "modified"
	default:
		return "decided about"
	}
}

// sentenceTerminators are the boundaries §9's splitter recognizes, scanned forward for the first
// occurrence; end-of-string closes the last sentence.
var sentenceTerminators = [...]string{". ", ".\n", "! ", "? "}

// firstSentences returns the prefix of s holding its first n sentences, terminator punctuation
// included, trailing separator excluded.
func firstSentences(s string, n int) string {
	end := 0
	for i := 0; i < n && end < len(s); i++ {
		end = sentenceEnd(s, end)
	}
	return s[:end]
}

// sentenceEnd returns the byte index just past the sentence beginning at start: one past the
// earliest terminator's punctuation, or len(s) when none follows. The terminators are all
// single-byte punctuation plus a separator, so the +1 lands on a rune boundary by construction.
func sentenceEnd(s string, start int) int {
	end := len(s)
	for _, term := range sentenceTerminators {
		if i := strings.Index(s[start:], term); i >= 0 && start+i+1 < end {
			end = start + i + 1
		}
	}
	return end
}

// capRunes truncates s to at most limit runes, at a rune boundary, without allocating.
func capRunes(s string, limit int) string {
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
}

// extractInterrupted wraps a context error in the package's error register. Cancellation and a
// SourceSet.Validate failure are the only error returns §9 allows ExtractDecisions.
func extractInterrupted(err error) error {
	return fmt.Errorf("checkpoint: extracting decisions: %w", err)
}
