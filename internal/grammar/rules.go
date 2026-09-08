package grammar

import "slices"

// This file is the live, mutable half of the L2 grammar analyzer (00-ARCHITECTURE.md §5.11): the
// linked-symbol representation an INCREMENTAL Sequitur needs, the digram index that turns "have I
// seen this adjacent pair before?" into an O(1) question, and the four structural operations —
// check, match, substitute and expand — that between them keep Sequitur's two invariants true
// after every single Append.
//
// It is deliberately separate from sequitur.go. sequitur.go is the CONTRACT surface: the interface
// §5.11 froze, New, and the read-only projections (Rules, Thrash, Compressed, Snapshot) that
// consumers and the codec seam (plans/sdd/V5-SP-15/contract.md §4) see. This file is the machine
// underneath. Nothing in it is exported and nothing in it ever escapes: a *node's meaning is its
// position in a list this package rewrites on every append, so a consumer holding one would be
// holding a pointer whose meaning changes under it.
//
// The two invariants, restated exactly as this file enforces them:
//
//  1. DIGRAM UNIQUENESS. No adjacent pair of symbols appears twice across the whole grammar — the
//     top-level sequence plus every rule body, counted together, because a rule body is grammar
//     too. When a second occurrence appears it is either folded into the existing rule whose body
//     is exactly that pair, or a fresh rule is minted and BOTH occurrences are replaced by
//     references to it. The one classical exception is documented at check below.
//  2. RULE UTILITY. Every rule is referenced more than once. A rule with a single reference buys
//     no compression and costs an indirection, so it is inlined at its one remaining use site and
//     retired. This is what keeps Rules() a list of REPEATED structure rather than a parse tree,
//     and it is what makes Thrash's answer mean "this actually recurred".
//
// Both are load-bearing beyond tidiness: 00-ARCHITECTURE.md §6.1 names them as the property under
// test for this package, and grammartest asserts them after every single Append rather than at the
// end of a batch, because an analyzer whose invariants only held at quiescence would report
// nonsense to any consumer that asked mid-session — which is every consumer this package has
// (observer's thrash warning on UserPromptSubmit, checkpoint's compressed action history).

// rule is one induced grammar rule while it is still LIVE — that is, while its body is a linked
// list this package is still splicing symbols into and out of. It is the mutable counterpart of
// the exported Rule value type, which is a snapshot taken by Rules().
//
// uses is the reference count, maintained incrementally rather than recomputed, because rule
// utility has to be decidable the instant a reference disappears: recomputing it would mean
// walking the whole grammar on every splice, and the utility repair happens inside the splice.
type rule struct {
	// id is the rule's identity, minted monotonically so that two identical Append sequences
	// produce identical ids (the determinism requirement in plans/sdd/V5-SP-15/contract.md §4).
	id RuleID
	// guard is the sentinel node of the rule's circular body list: guard.next is the first body
	// symbol, guard.prev is the last, and an empty body is the guard linked to itself.
	guard *node
	// uses is how many nodes anywhere in the grammar reference this rule.
	uses int
}

// node is one cell of a rule's body: either a terminal Symbol, a reference to another rule, or the
// sentinel guard that closes the circular list.
//
// A doubly linked list, rather than a slice, is what makes Sequitur incremental at all. Every
// operation here — replacing a digram with a rule reference, inlining an under-used rule at its
// one remaining site — is a splice of a bounded number of links, and it must not move the symbols
// on either side, because the digram index holds pointers to them. A slice-backed body would
// invalidate that index on every insertion.
//
// The guard is the standard Sequitur sentinel: it makes "is this symbol the first or last of a
// rule body?" a pointer comparison, which is exactly the test match needs to decide whether a
// matched digram IS an existing rule (reuse it) or merely occurs inside one (mint a new rule).
type node struct {
	// val is the terminal symbol this node carries; meaningful only when ref and guard are nil.
	val Symbol
	// ref is the rule this node references; nil for a terminal.
	ref *rule
	// guard names the rule this node is the sentinel of; nil for every ordinary body node.
	guard *rule
	// prev and next are the circular body links.
	prev *node
	next *node
}

// isGuard reports whether n is a body list's sentinel rather than a symbol. Guards carry no symbol
// value, so no digram may begin or end at one.
func (n *node) isGuard() bool { return n.guard != nil }

// key reduces a node to the identity a digram is compared on. A terminal is identified by its
// Symbol value and a reference by the rule POINTER, never by the rule's rendered RuleRef text:
// building a string per comparison would allocate on the hottest path in the package, and the
// pointer is the sharper answer anyway — two distinct rules can never collide the way two RuleRef
// renderings could if a terminal ever happened to look like one.
func (n *node) key() symKey { return symKey{ref: n.ref, val: n.val} }

// encoded renders n as the Symbol a caller outside this package sees: a terminal as itself, a
// reference through RuleRef, which is Main's encoding in types.go and the one the codec seam
// agrees on (plans/sdd/V5-SP-15/contract.md §4).
func (n *node) encoded() Symbol {
	if n.ref != nil {
		return RuleRef(n.ref.id)
	}
	return n.val
}

// symKey is one node's identity for digram comparison. Exactly one of ref and val is meaningful,
// and a nil rule pointer is what distinguishes the two cases.
type symKey struct {
	ref *rule
	val Symbol
}

// digramKey is an adjacent pair of symbol identities — the thing digram uniqueness is about.
type digramKey struct{ a, b symKey }

// sequiturGrammar is a live induced grammar: a start sequence, the rules induced from it, and the
// digram index over both.
//
// The index is a map, and maps in Go iterate in a randomized order, so it is used for LOOKUP ONLY
// and never walked to produce an answer. Every value this type hands out — Rules, Thrash,
// Compressed, Snapshot — is derived from the linked lists (whose order is the grammar's own) and
// from rule ids sorted ascending. That is the whole of the determinism argument required by
// plans/sdd/V5-SP-15/contract.md §4: identical Append sequences produce identical output because
// no map iteration order can reach it.
type sequiturGrammar struct {
	// start is rule 0, the top-level sequence. It is never in byID and never appears in Rules():
	// it is the sequence Compressed() returns, not an induced rule, and it is exempt from rule
	// utility because nothing references it.
	start *rule
	// byID holds every induced rule. Lookup only; iteration is always over sorted ids.
	byID map[RuleID]*rule
	// index maps a digram to the node that begins its one recorded occurrence.
	index map[digramKey]*node
	// nextID is the id the next induced rule takes.
	nextID RuleID
	// utilitySweeps counts how many times the end-of-append rule-utility sweep found work the
	// in-cascade repair had missed. It exists so a test can assert that the belt-and-braces sweep
	// is what it claims to be — a safety net over a subtle argument — rather than load-bearing
	// machinery whose cost nobody has measured.
	utilitySweeps int
}

// startRuleID is the id reserved for the top-level sequence. Induced rules start at 1 so that a
// RuleRef can never ambiguously name "the start sequence", which is not a rule any consumer may
// reference: nothing in the grammar refers to the top level, and a snapshot that claimed otherwise
// would be describing a cycle.
const startRuleID RuleID = 0

// firstInducedRuleID is the id the first induced rule takes.
const firstInducedRuleID RuleID = 1

// newSequiturGrammar returns an empty live grammar: no rules, no symbols, an empty digram index.
func newSequiturGrammar() *sequiturGrammar {
	g := &sequiturGrammar{}
	g.clear()
	return g
}

// clear returns g to the empty state. It is the shared body of the constructor and Reset, which
// have to agree exactly: a Reset grammar that differed from a fresh one in ANY field — a retained
// nextID above all — would make two identical Append sequences produce different rule ids, which
// is the determinism property contract §4 pins.
func (g *sequiturGrammar) clear() {
	g.byID = make(map[RuleID]*rule)
	g.index = make(map[digramKey]*node)
	g.nextID = firstInducedRuleID
	g.utilitySweeps = 0
	g.start = &rule{id: startRuleID}
	g.start.guard = newGuard(g.start)
}

// newGuard builds r's sentinel node, linked to itself: the empty body.
func newGuard(r *rule) *node {
	n := &node{guard: r}
	n.prev, n.next = n, n
	return n
}

// newRule mints an empty rule carrying the next id.
func (g *sequiturGrammar) newRule() *rule {
	r := &rule{id: g.nextID}
	g.nextID++
	r.guard = newGuard(r)
	g.byID[r.id] = r
	return r
}

// link joins two nodes. Every structural change in this file is expressed as a small number of
// link calls, and every one of them is preceded by the digram bookkeeping for the pairs it is
// about to destroy — because a digram's key is read off the links, so it can only be computed
// while those links still describe it.
func link(a, b *node) {
	a.next = b
	b.prev = a
}

// insertAfter splices n in immediately after at.
func insertAfter(at, n *node) {
	nxt := at.next
	link(at, n)
	link(n, nxt)
}

// ----------------------------------------------------------------------------------------------
// Digram index bookkeeping.
// ----------------------------------------------------------------------------------------------

// digramAt reads the digram that BEGINS at n, reporting false when there is none — n is a guard,
// or n is the last symbol of its body.
func digramAt(n *node) (digramKey, bool) {
	if n.isGuard() || n.next.isGuard() {
		return digramKey{}, false
	}
	return digramKey{a: n.key(), b: n.next.key()}, true
}

// setDigram records n as the occurrence of its digram, overwriting whatever was there.
//
// It is used only where the caller has already established that the digram has no other occurrence
// — match, after it has minted a rule and deleted both source occurrences. Everywhere else check
// is used instead, because blindly overwriting an entry would leave a real second occurrence
// unindexed and silently break invariant 1 for the rest of the session.
func (g *sequiturGrammar) setDigram(n *node) {
	if k, ok := digramAt(n); ok {
		g.index[k] = n
	}
}

// dropDigram removes the index entry that n's outgoing digram owns, if n is the node the index
// recorded. It MUST be called while n's links still describe the digram being destroyed.
func (g *sequiturGrammar) dropDigram(n *node) {
	k, ok := digramAt(n)
	if !ok {
		return
	}
	if g.index[k] == n {
		delete(g.index, k)
	}
}

// ----------------------------------------------------------------------------------------------
// The four structural operations.
// ----------------------------------------------------------------------------------------------

// check examines the digram beginning at n and reports whether it CONSUMED it — that is, whether
// it folded the pair into a rule and so changed the structure around n. A false return means the
// caller's nodes are still where it left them.
//
// The overlap case is the one classical exception to invariant 1, and it is deliberate. In "aaa"
// the pair (a,a) occurs at positions 0-1 and 1-2, but those two occurrences SHARE the middle
// symbol, so no rule can replace both: replacing one leaves a rule referenced exactly once, which
// invariant 2 then inlines straight back, and the algorithm would oscillate forever. Sequitur's
// original formulation therefore matches only NON-overlapping occurrences, and so does this. The
// consequence is worth stating plainly, because it is visible in Compressed(): after an odd-length
// run of one symbol the top level can hold three adjacent copies, and the pair (x,x) is then
// genuinely present twice. That is not a defect here. For a stream ending "u G G G" NO grammar
// exists that has every rule used more than once and no repeated adjacent pair at the same time —
// the three G's occupy three adjacent top-level slots, one of the two pairs among them must
// repeat, and no rule of two or more symbols can be used twice when the only repeated substring
// available is a single G.
func (g *sequiturGrammar) check(n *node) bool {
	k, ok := digramAt(n)
	if !ok {
		return false
	}
	m, seen := g.index[k]
	if !seen {
		g.index[k] = n
		return false
	}
	if m == n || m.next == n || n.next == m {
		// Already indexed as itself, or the two occurrences overlap: leave both alone.
		return false
	}
	g.match(n, m)
	return true
}

// match resolves a digram that now occurs twice: at n (the new occurrence) and at m (the one the
// index remembered). Afterwards the digram occurs exactly once — inside a rule's body — and both
// sites reference that rule.
//
// The reuse branch is what stops the grammar growing a second rule for structure it already names:
// when m's occurrence IS an entire rule body, that rule already means this digram, so n is simply
// replaced by a reference to it. That branch is also what makes rules of rules appear, which is
// the whole reason a thrashing loop compresses at all rather than merely being noticed.
func (g *sequiturGrammar) match(n, m *node) {
	var r *rule
	if m.prev.isGuard() && m.next.next.isGuard() {
		r = m.prev.guard
		g.substitute(n, r)
	} else {
		r = g.newRule()
		insertAfter(r.guard, g.copyOf(n))
		insertAfter(r.guard.next, g.copyOf(n.next))

		// m first, then n: m's replacement cannot disturb n, because the only digrams it creates
		// mention r, which is brand new and so cannot already be in the index — no cascade can
		// start there and reach n.
		g.substitute(m, r)
		g.substitute(n, r)

		// Only now is the pair's single surviving occurrence — r's own body — recorded. Doing it
		// before the substitutions would have had their deletions drop the entry again.
		g.setDigram(r.guard.next)
	}

	// Replacing both occurrences may have left the digram's first symbol referenced only from r's
	// body. This is the site the original algorithm repairs, and it is the common one;
	// enforceRuleUtility, called once per Append, is what covers the rest.
	if first := r.guard.next; first.ref != nil && first.ref.uses == 1 {
		g.expandRef(first)
	}
}

// copyOf builds a fresh node carrying the same symbol as n, taking a reference on n's rule when
// there is one. The copy is what goes into a new rule's body; the original is deleted by the
// substitution that follows, so the net effect on that rule's use count is what it should be.
func (g *sequiturGrammar) copyOf(n *node) *node {
	if n.ref != nil {
		n.ref.uses++
		return &node{ref: n.ref}
	}
	return &node{val: n.val}
}

// substitute replaces the two symbols beginning at n with a single reference to r, then offers the
// two digrams that replacement created to check.
//
// The second offer is conditional on the first not having fired, because if it did, the reference
// node this function just inserted may already have been consumed by the cascade — following a
// pointer into it would be reading a node that is no longer in any body.
func (g *sequiturGrammar) substitute(n *node, r *rule) {
	before := n.prev
	second := n.next

	// Three digrams die here: the one ending at n, the pair itself, and the one beginning at
	// second. Their keys are read off links that are about to change, so all three are dropped
	// first — each from the node that owns it.
	g.dropDigram(before)
	g.dropDigram(n)
	g.dropDigram(second)

	g.unlinkNode(n)
	g.unlinkNode(second)

	ref := &node{ref: r}
	r.uses++
	insertAfter(before, ref)

	if !g.check(before) {
		g.check(ref)
	}
}

// unlinkNode removes n from its body and releases the rule reference it held, if any.
func (g *sequiturGrammar) unlinkNode(n *node) {
	link(n.prev, n.next)
	n.prev, n.next = nil, nil
	if n.ref != nil {
		n.ref.uses--
		n.ref = nil
	}
}

// expandRef inlines rule r at n, its LAST remaining reference, and retires r — the enforcement of
// invariant 2.
//
// The body nodes are MOVED, not copied. That is not an optimization: every digram strictly inside
// r's body keeps its index entry, because the entry points at the very node that moved and the
// pair it names is unchanged by the move. Copying would have meant reindexing the whole body.
func (g *sequiturGrammar) expandRef(n *node) {
	r := n.ref
	before, after := n.prev, n.next
	first, last := r.guard.next, r.guard.prev

	// n's own two digrams die with it.
	g.dropDigram(before)
	g.dropDigram(n)

	// Detach the body wholesale, then retire the rule. Emptying the guard first is what makes the
	// retirement safe: a rule with an empty body owns no index entries, so nothing is left behind
	// pointing into a rule that no longer exists.
	link(r.guard, r.guard)
	delete(g.byID, r.id)
	r.uses = 0

	n.ref = nil
	n.prev, n.next = nil, nil

	link(before, first)
	link(last, after)

	// Two digrams are born: (before,first) and (last,after). The same conditional as substitute's,
	// and for the same reason.
	if !g.check(before) {
		g.check(last)
	}
}

// enforceRuleUtility inlines every rule that is down to a single reference, repeating until none is
// left. Append calls it once, after its cascade has settled.
//
// match already performs the repair the original algorithm performs, at the one site the original
// checks: the digram's FIRST symbol. The second symbol's rule can lose a reference in the same
// step, and the argument that it can never fall to one is subtle enough that resting a
// conformance-asserted invariant on it would be resting it on a proof this file does not contain.
// So the invariant is enforced directly instead. utilitySweeps counts the times this found real
// work, so a test can say out loud whether the net ever catches anything rather than leaving a
// reader to guess.
//
// The scan is over ids sorted ascending, never over map order, so the repair sequence — and hence
// the resulting grammar — is identical for identical inputs.
func (g *sequiturGrammar) enforceRuleUtility() {
	for {
		under := make([]RuleID, 0, 2)
		for id, r := range g.byID {
			if r.uses <= 1 {
				under = append(under, id)
			}
		}
		if len(under) == 0 {
			return
		}
		slices.Sort(under)
		g.utilitySweeps++

		for _, id := range under {
			r, ok := g.byID[id]
			if !ok {
				continue // an earlier expansion in this pass already retired it
			}
			switch {
			case r.uses == 0:
				// Unreachable from Append by construction, and rejected by Restore, so this is
				// reached only if a future change breaks one of those. Retiring the rule keeps
				// Rules() honest either way: a rule nothing references is not part of the grammar.
				g.discardRule(r)
			case r.uses == 1:
				ref := g.soleRef(r)
				if ref == nil {
					// The count says one reference and the grammar holds none, so the count is
					// the thing that is wrong: trust the structure and retire the rule.
					g.discardRule(r)
					continue
				}
				g.expandRef(ref)
			}
		}
	}
}

// discardRule drops a rule nothing references, along with the index entries its body owned.
func (g *sequiturGrammar) discardRule(r *rule) {
	for n := r.guard.next; !n.isGuard(); n = n.next {
		g.dropDigram(n)
	}
	for n := r.guard.next; !n.isGuard(); {
		nxt := n.next
		if n.ref != nil {
			n.ref.uses--
			n.ref = nil
		}
		n.prev, n.next = nil, nil
		n = nxt
	}
	link(r.guard, r.guard)
	delete(g.byID, r.id)
	r.uses = 0
}

// soleRef finds the one node referencing r, searching the start sequence first and then every rule
// body in ascending id order. The order matters only for determinism of the answer when the count
// is wrong; when it is right there is exactly one node to find.
func (g *sequiturGrammar) soleRef(r *rule) *node {
	if n := refIn(g.start, r); n != nil {
		return n
	}
	for _, id := range g.sortedIDs() {
		if id == r.id {
			continue
		}
		if n := refIn(g.byID[id], r); n != nil {
			return n
		}
	}
	return nil
}

// refIn returns the first node of owner's body that references r, or nil.
func refIn(owner, r *rule) *node {
	if owner == nil {
		return nil
	}
	for n := owner.guard.next; !n.isGuard(); n = n.next {
		if n.ref == r {
			return n
		}
	}
	return nil
}

// ----------------------------------------------------------------------------------------------
// Read-only projections. Every one of these builds a fresh value: a caller that mutates what it
// was handed must not be able to reach into the live grammar, because the live grammar is what the
// next Append splices.
// ----------------------------------------------------------------------------------------------

// sortedIDs returns every induced rule id, ascending. This is the only place map iteration order
// is admitted, and it is sorted away in the same breath.
func (g *sequiturGrammar) sortedIDs() []RuleID {
	ids := make([]RuleID, 0, len(g.byID))
	for id := range g.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// bodyOf renders a rule's immediate body as encoded Symbols, and nil for an empty one — the same
// nil the SP-01 stub documented for an empty grammar, so a consumer that read "no symbols yet" as
// nil keeps reading it that way.
func (g *sequiturGrammar) bodyOf(r *rule) []Symbol {
	var out []Symbol
	for n := r.guard.next; !n.isGuard(); n = n.next {
		out = append(out, n.encoded())
	}
	return out
}

// expansions computes every rule's full expansion back to terminals in one memoized pass, so that
// a single Rules() call costs one traversal of the grammar rather than one per rule.
//
// The guard against re-entry is not a live concern — Sequitur's grammars are acyclic by
// construction, and Restore refuses a snapshot that is not — but a cycle reached here would be an
// unbounded recursion rather than an error, and Rules() has no error to return. Treating a back
// edge as an empty expansion makes that failure finite and visible instead of fatal.
func (g *sequiturGrammar) expansions() map[RuleID][]Symbol {
	memo := make(map[RuleID][]Symbol, len(g.byID))
	busy := make(map[RuleID]bool, len(g.byID))
	for _, id := range g.sortedIDs() {
		g.expansionOf(g.byID[id], memo, busy)
	}
	return memo
}

// expansionOf expands one rule, filling memo as it goes.
func (g *sequiturGrammar) expansionOf(r *rule, memo map[RuleID][]Symbol, busy map[RuleID]bool) []Symbol {
	if e, ok := memo[r.id]; ok {
		return e
	}
	if busy[r.id] {
		return nil
	}
	busy[r.id] = true
	var out []Symbol
	for n := r.guard.next; !n.isGuard(); n = n.next {
		if n.ref != nil {
			out = append(out, g.expansionOf(n.ref, memo, busy)...)
			continue
		}
		out = append(out, n.val)
	}
	busy[r.id] = false
	memo[r.id] = out
	return out
}

// snapshotRules renders every induced rule, ascending by id, as the exported Rule value type.
func (g *sequiturGrammar) snapshotRules() []Rule {
	ids := g.sortedIDs()
	if len(ids) == 0 {
		return nil
	}
	exp := g.expansions()
	out := make([]Rule, 0, len(ids))
	for _, id := range ids {
		r := g.byID[id]
		e := exp[id]
		out = append(out, Rule{
			ID:        r.id,
			Body:      g.bodyOf(r),
			Uses:      r.uses,
			Expansion: slices.Clone(e),
			Span:      len(e),
		})
	}
	return out
}
