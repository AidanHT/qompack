package analyzer

import (
	"context"
	"io"
	"strings"
	"unicode"

	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// DeltaScorer prices blocks against the observed continuation (00-ARCHITECTURE.md §5.12): how
// much worse the continuation would have gone without each block. Mode names which cost tier the
// implementation is, so /qompack:status and the eval harness can report what was actually run.
type DeltaScorer interface {
	// Mode reports this scorer's cost tier.
	Mode() DeltaMode
	// Score returns a Δ(c) proxy in [0,1] for each block, measured against the OBSERVED
	// continuation. Every block passed in gets an entry.
	Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error)
}

// The three evidence weights the cheap tier combines, in the order 00-ARCHITECTURE.md §5.12's
// "token overlap plus symbol-reference counting" and contract §6's "token, symbol and path
// overlap" name them. They sum to 1, which is what puts the combined proxy in [0,1] before the
// clamp ever runs.
//
// The ORDER of the three is the argument, and it is the same one internal/store/search.go's
// recall weights make in prose: a path is an exact statement of intent, a symbol name is nearly as
// exact but can collide across files, and a bag of words is evidence that accumulates and is
// mostly noise. Weighting them the other way round would let a block full of common English score
// above the block that names the very file the continuation went on to edit.
//
// None of the three duplicates a configuration default, and none is drawn from §11.6's forbidden
// literal set — 0.4 in particular is in that set, which is why the path term is 0.45.
const (
	// wContinuationPath weights the fraction of the continuation's paths this block supplies.
	wContinuationPath = 0.45
	// wContinuationSymbol weights the fraction of the continuation's symbols this block mentions.
	wContinuationSymbol = 0.35
	// wContinuationToken weights the fraction of the continuation's vocabulary this block covers.
	wContinuationToken = 0.20
)

// The bounds that keep one Score call's cost a function of the candidate set rather than of the
// largest file anybody ever read.
//
// A Δ-score is an INPUT to selection, not the answer, so it must never cost more than the
// selection it feeds. Both limits truncate rather than fail: a block whose content is longer than
// maxScoredBytes is scored on its head, which is a weaker measurement of a real block, and that
// beats reporting an error over a prefix the caller has to price anyway.
const (
	// maxScoredBytes bounds how much of one block's content the cheap tier reads back.
	maxScoredBytes = 128 << 10
	// maxDistinctTokens bounds the vocabulary either side of the overlap contributes. Truncation
	// keeps the first tokens in CONTENT order, which is deterministic, rather than whichever ones
	// a map happened to yield.
	maxDistinctTokens = 8192
	// minTokenRunes is the shortest run of word runes that counts as a token. Two-rune fragments
	// ("if", "id", "os") collide across every document in the corpus and would make the token term
	// report overlap between blocks that share nothing.
	minTokenRunes = 3
)

// NewCheapScorer returns the cheap-tier Δ-scorer of 00-ARCHITECTURE.md §5.12: token overlap plus
// symbol-reference counting, reading block content back out of s.
//
// s may be nil, and that is not an error. NewCheapScorer is wired at a composition root that may
// not have opened a store yet — test/guards constructs it with a nil one on purpose — and the
// honest response to "no content is readable" is a score computed from the evidence that IS
// readable (each block's own DAG key), not a refusal that costs the caller its selection pass.
func NewCheapScorer(s store.Store) DeltaScorer { return cheapScorer{s: s} }

// cheapScorer is the DeltaCheap tier: a lexical overlap measurement between each block's content
// and the observed continuation.
//
// # What this number IS
//
// It is a BEHAVIOUR PROXY, and nothing more: the fraction of the observed continuation's paths,
// symbols and vocabulary that a block could have supplied. It is measured against what the
// session actually did next (Continuation), which is what makes it replayable — two runs over the
// same transcript produce the same numbers, so a change in a selection metric between commits is
// attributable to the change rather than to the scorer.
//
// # What this number is NOT, stated plainly because the plan rules each one out of scope
//
//   - It is NOT an unbiased KL estimate. Nothing here estimates a divergence between two
//     distributions; there is no distribution. Reporting a lexical overlap as a KL figure would
//     put a units label on it that no part of this computation earns.
//   - It is NOT an action-distribution divergence derived from edit distance. No edit distance is
//     computed, no action distribution is modelled, and the plan's §2 names both as diagnostics
//     rather than as measurements of either quantity.
//   - It is NOT a correctness label. A block scoring 0 is not established to be useless and a
//     block scoring 1 is not established to be necessary: absence of lexical overlap is absence of
//     THIS evidence, not evidence of irrelevance, and §2's "do not prove outside content
//     irrelevant" applies to the scorer exactly as it applies to the relation graph.
//
// A consumer that needs any of the three has to compute it; this tier will not be silently
// promoted into one by a caller reading its output as though it were.
type cheapScorer struct {
	// s is where block content is read back from. A nil store means no content is readable, which
	// zeroes the content-derived terms rather than failing the call.
	s store.Store
}

// Mode returns DeltaCheap. Mode has no error return, and the honest answer is not a zero value
// here but the tier this scorer is contractually the constructor for: NewCheapScorer's caller
// asked for the cheap tier, and reporting anything else — including "" — would misdescribe which
// implementation is wired in, which is the one thing Mode exists to tell /qompack:status.
func (cheapScorer) Mode() DeltaMode { return DeltaCheap }

// Score prices every block in blocks against the observed continuation, returning a proxy in
// [0,1] per block (00-ARCHITECTURE.md §5.12). See cheapScorer's own comment for the three claims
// this number does NOT make.
//
// Four properties are load-bearing, and analyzertest's /behaviour block asserts each of them:
//
//  1. EVERY block gets an entry. A missing key is indistinguishable, at a selector's call site,
//     from a block scored zero — and the two mean very different things, so the map is total over
//     its input and a block whose content could not be read is scored on what remains rather than
//     omitted.
//  2. It is deterministic. No float is accumulated over a map iteration anywhere below: the three
//     overlap terms are integer COUNTS divided once, and counting is order-independent, so two
//     calls on equal inputs return byte-identical maps.
//  3. An empty block set scores nothing and is not an error. Pricing a prefix that has no
//     candidates left is the normal end of a session, not a fault.
//  4. A continuation that references the blocks cannot score them lower in total than one that
//     observed nothing. That holds structurally rather than by tuning: an empty Continuation
//     zeroes all three numerators, so every block scores exactly 0, and each term is nonnegative.
//
// A nil ctx is accepted and replaced with context.Background(). store.OpenSpan dereferences the
// context it is handed, so the alternative is a panic — and a seam that panics costs the user
// their turn (§12.3), which is the one outcome this whole tier's degrade-rather-than-refuse rule
// exists to avoid. test/guards' reflective walk over every landed seam is the caller most likely
// to produce one; today it substitutes a real context for a context parameter (zeroArg), so this
// line is not what makes that walk pass, and it is one line either way.
func (c cheapScorer) Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ev := newContinuationEvidence(continuation)
	scores := make(map[dag.NodeID]float64, len(blocks))
	for _, b := range blocks {
		scores[b.ID] = ev.score(b, c.read(ctx, b))
	}
	return scores, nil
}

// read returns as much of b's content as the cheap tier is willing to look at, or nil when there
// is none to be had.
//
// Every failure path yields nil rather than an error, and each is a real state rather than a
// defensive branch: a nil store is the no-content composition root, a zero Root is a block whose
// node never carried content (an assistant turn, a file anchor), and a read error is a root GC
// tombstoned or quarantined out from under the index. In all four cases the block still exists and
// still has to be scored — on its DAG key alone, if that is all that is left.
func (c cheapScorer) read(ctx context.Context, b Block) []byte {
	if c.s == nil || b.Root.IsZero() {
		return nil
	}
	rc, err := c.s.OpenSpan(ctx, b.Root, 0, maxScoredBytes)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()

	content, err := io.ReadAll(io.LimitReader(rc, maxScoredBytes))
	if err != nil && len(content) == 0 {
		return nil
	}
	// A partial read is kept. The bytes that arrived are as true as the ones that did not, and a
	// weaker measurement of a real block beats treating it as empty.
	return content
}

// continuationEvidence is one Continuation reduced to the three comparable sets the cheap tier
// measures against, computed once per Score call rather than once per block.
//
// Each set is normalized the way its own domain is compared elsewhere in the tree: paths through
// paths.Key, so the platform's case-folding rule is the one the store and the DAG already use, and
// symbols and tokens through lowercasing, since the continuation's rendering of a name is not
// evidence about the block's.
type continuationEvidence struct {
	// tokens is the continuation text's distinct word tokens.
	tokens map[string]struct{}
	// symbols is the distinct, lowercased symbol names the continuation referenced.
	symbols []string
	// paths is the distinct, paths.Key-form file paths the continuation touched.
	paths []string
}

// newContinuationEvidence reduces c to its comparable sets.
func newContinuationEvidence(c Continuation) continuationEvidence {
	ev := continuationEvidence{tokens: make(map[string]struct{}, maxDistinctTokens)}
	addTokens(ev.tokens, string(c.Text))

	seenSym := make(map[string]struct{}, len(c.Symbols))
	for _, s := range c.Symbols {
		lower := strings.ToLower(strings.TrimSpace(s))
		if lower == "" {
			continue
		}
		if _, dup := seenSym[lower]; dup {
			continue
		}
		seenSym[lower] = struct{}{}
		ev.symbols = append(ev.symbols, lower)
	}

	seenPath := make(map[string]struct{}, len(c.Paths))
	for _, p := range c.Paths {
		key := paths.Key(strings.TrimSpace(p))
		if key == "" {
			continue
		}
		if _, dup := seenPath[key]; dup {
			continue
		}
		seenPath[key] = struct{}{}
		ev.paths = append(ev.paths, key)
	}
	return ev
}

// score combines the three overlap terms for one block.
//
// The clamp is not decoration. The three weights sum to 1 in decimal, which does not oblige them
// to sum to 1 in float64, and §5.12 states the range as a contract analyzertest asserts on every
// entry — so the range is enforced here rather than assumed to fall out of the arithmetic.
func (ev continuationEvidence) score(b Block, content []byte) float64 {
	blockTokens := make(map[string]struct{}, maxDistinctTokens)
	addTokens(blockTokens, string(content))

	// The block's own DAG key is evidence in its own right, and often the ONLY evidence: a file
	// node carries its path as its key and no content at all, so a scorer that read content alone
	// would price every file anchor at zero however squarely the continuation landed on it.
	_, key, _ := dag.ParseNodeID(b.ID)
	addTokens(blockTokens, key)

	lowerContent := strings.ToLower(string(content))
	blockPathKey := paths.Key(key)

	return clamp01(
		wContinuationPath*ev.pathTerm(blockPathKey, lowerContent) +
			wContinuationSymbol*ev.symbolTerm(blockTokens, key) +
			wContinuationToken*ev.tokenTerm(blockTokens))
}

// pathTerm is the fraction of the continuation's paths this block supplies: its own path key is
// one of them, or its content names one of them.
//
// A block with no path and no content contributes 0, and a continuation that touched no path
// makes the whole term 0 — which is the empty-continuation floor property 4 of Score relies on.
func (ev continuationEvidence) pathTerm(blockKey, lowerContent string) float64 {
	if len(ev.paths) == 0 {
		return 0
	}
	hits := 0
	for _, p := range ev.paths {
		if blockKey == p || strings.Contains(lowerContent, strings.ToLower(p)) {
			hits++
		}
	}
	return float64(hits) / float64(len(ev.paths))
}

// symbolTerm is §5.12's "symbol-reference counting": the fraction of the continuation's symbols
// this block mentions, either in its content's vocabulary or as its own DAG key (which is the
// symbol name itself, for a KindSymbol node).
func (ev continuationEvidence) symbolTerm(blockTokens map[string]struct{}, blockKey string) float64 {
	if len(ev.symbols) == 0 {
		return 0
	}
	lowerKey := strings.ToLower(blockKey)
	hits := 0
	for _, s := range ev.symbols {
		if _, ok := blockTokens[s]; ok || strings.Contains(lowerKey, s) {
			hits++
		}
	}
	return float64(hits) / float64(len(ev.symbols))
}

// tokenTerm is the fraction of the continuation's vocabulary this block covers.
//
// It is CONTAINMENT of the continuation in the block, not a Jaccard: the question a Δ proxy is
// standing in for is "how much of what happened next could this block have supplied", and a
// Jaccard would penalize a large block for the words the continuation never needed. The cost of
// that choice is stated rather than hidden — a large block does score higher, all else equal —
// and it is why this term carries the SMALLEST of the three weights.
func (ev continuationEvidence) tokenTerm(blockTokens map[string]struct{}) float64 {
	if len(ev.tokens) == 0 {
		return 0
	}
	hits := 0
	for t := range ev.tokens {
		if _, ok := blockTokens[t]; ok {
			hits++
		}
	}
	// hits is an integer count over a map, which is order-independent; the single division below
	// is where the only float arithmetic happens, so no map iteration order can reach the result.
	return float64(hits) / float64(len(ev.tokens))
}

// addTokens folds s's word tokens into dst, lowercased, at most maxDistinctTokens of them.
//
// A word token is a maximal run of letters, digits and underscores — the shape of an identifier in
// every language the corpus contains — so "pool.Acquire(ctx)" contributes "pool", "acquire" and
// "ctx" rather than one unsearchable blob. Runs shorter than minTokenRunes are dropped; see that
// constant for why.
func addTokens(dst map[string]struct{}, s string) {
	if s == "" {
		return
	}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tok := b.String()
		b.Reset()
		if len([]rune(tok)) < minTokenRunes || len(dst) >= maxDistinctTokens {
			return
		}
		dst[tok] = struct{}{}
	}
	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
}

// clamp01 confines v to [0,1]. NaN — which no term above can produce, since every denominator is
// checked non-zero before the division — would fail both comparisons and fall through to v, so it
// is mapped to 0 explicitly rather than left to escape the range the contract states.
func clamp01(v float64) float64 {
	switch {
	case v != v:
		return 0
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// The seam this file implements. It is a compile-time assertion rather than a test because a
// scorer that stopped satisfying DeltaScorer would otherwise fail at the composition root instead
// of here, one wave later.
//
// Score returns no error on any path: every degradation it can meet — a nil store, a missing root,
// a truncated read — is a weaker measurement rather than a refusal, so none of the four legal
// sentinels (core.ErrNotImplemented, ErrNotFound, ErrBudget, ErrDegraded) is reachable from it.
var _ DeltaScorer = cheapScorer{}
