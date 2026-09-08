package grammar

import (
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// Sequitur incrementally induces a context-free grammar over an appended Symbol stream
// (00-ARCHITECTURE.md §5.11), maintaining Sequitur's two classical invariants as it goes: no
// digram appears twice, and every rule is used more than once.
type Sequitur interface {
	// Append folds one more Symbol into the grammar.
	Append(s Symbol)
	// Rules returns every rule currently in the grammar.
	Rules() []Rule
	// Thrash returns rules whose multiplicity exceeds minUses and whose expansion length is at
	// least 2: the candidates for a loop-detection Warning.
	Thrash(minUses int) []Rule
	// Compressed returns the grammar-compressed action history for the checkpoint: the top-level
	// sequence with repeated structure folded into rule references.
	Compressed() []Symbol
	// Reset clears the grammar back to empty.
	Reset()
	MarshalBinary() ([]byte, error)
	UnmarshalBinary([]byte) error
}

// New returns a Sequitur: as of SP-15 the real incremental grammar induction of
// 00-ARCHITECTURE.md §5.11, not the SP-01 stub it replaced.
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, symbols.New, redact.New): building a Sequitur performs no I/O by itself, so there is
// nothing for it to fail at. That was true of the stub and it stays true of the real thing — the
// grammar's whole state is in memory, and the only operation that can fail is UnmarshalBinary,
// which is reading someone else's bytes.
func New() Sequitur {
	return newSequiturGrammar()
}

// Append folds one more Symbol into the grammar, in the one step Sequitur is named for: the symbol
// joins the top-level sequence, and the single digram its arrival created is offered to the
// induction machinery, which cascades as far as it needs to.
//
// The cascade is the reason Append is O(1) amortized rather than a re-parse: appending a symbol
// can only create ONE new adjacent pair, and every consequence of that pair — a rule minted, a
// rule reused, an under-used rule inlined — is a bounded splice of a list, not a rescan of the
// stream. That is what makes this analyzer affordable on the observer's hot path
// (00-ARCHITECTURE.md §5.11: L2 runs over the action log, per tool use).
//
// Append has no error return, and there is nothing here that could produce one: the grammar is
// in-memory, every symbol is admissible, and an empty Symbol is as valid a terminal as any other.
func (g *sequiturGrammar) Append(s Symbol) {
	n := &node{val: s}
	insertAfter(g.start.guard.prev, n)
	g.check(n.prev)
	g.enforceRuleUtility()
}

// Rules returns every rule currently in the grammar, ordered by ID ascending, each with its
// immediate Body, its reference count, its full Expansion back to terminals and that expansion's
// Span.
//
// The order is ascending ID and not, say, "most used first", because ID order is the only order
// that is stable across appends: a rule's use count changes as the session runs, and a consumer
// that diffed two Rules() calls would see rules apparently move when nothing about them had
// changed. Ordering is also the determinism requirement of plans/sdd/V5-SP-15/contract.md §4 —
// nothing here may depend on Go's randomized map iteration.
//
// Every slice returned is freshly built, so a caller may keep or mutate what it is handed without
// reaching into the live grammar the next Append will splice. It returns nil, not an empty slice,
// for a grammar with no rules: that is the answer the SP-01 stub documented, and consumers that
// treat nil as "nothing induced yet" keep working unchanged.
func (g *sequiturGrammar) Rules() []Rule {
	return g.snapshotRules()
}

// Thrash returns the rules whose multiplicity exceeds minUses and whose expansion is at least two
// terminals long: the candidates for a loop-detection Warning (00-ARCHITECTURE.md §5.11).
//
// Both conditions matter and they say different things. The use count is the "this recurred" test.
// The two-terminal floor is the "and it was a sequence" test: a rule spanning a single terminal
// describes one repeated action, which is ordinary work — a build run twice is not a loop — while
// a rule spanning two or more describes a repeated PATTERN of actions, which is the shape §8.3
// calls thrashing. Reporting the former would bury the latter in noise, and the observer injects
// this straight into the user's prompt (internal/observer/prompt.go), where noise is expensive.
//
// The comparison is strictly greater than minUses, matching §5.11's "exceeds": Thrash(2) reports a
// rule used three times, not one used twice.
func (g *sequiturGrammar) Thrash(minUses int) []Rule {
	all := g.snapshotRules()
	var out []Rule
	for _, r := range all {
		if r.Uses > minUses && len(r.Expansion) >= 2 {
			out = append(out, r)
		}
	}
	return out
}

// Compressed returns the grammar-compressed action history for the checkpoint: the top-level
// sequence, with repeated structure folded into rule references (00-ARCHITECTURE.md §5.11).
//
// It is the top-level sequence and not an expansion of it: that is the compression. A reader that
// wants the raw history expands the references through Rules(), and a reader that wants the shape
// of the session — which is what a checkpoint is for — reads this directly, where a loop that ran
// forty times is a handful of symbols instead of forty.
//
// Rule references are rendered by RuleRef, the encoding Main froze in types.go, so that this
// sequence and a Rule.Body speak the same language and the codec has one thing to encode rather
// than two.
func (g *sequiturGrammar) Compressed() []Symbol {
	return g.bodyOf(g.start)
}

// Reset clears the grammar back to empty — no rules, no sequence, no digrams, and rule ids
// starting again from one.
//
// Restarting the ids is the part worth being explicit about. A Reset grammar is indistinguishable
// from a fresh one, so replaying a session after a Reset produces the same rule ids as replaying
// it in a new process. Carrying a high-water mark across would have made the ids depend on what
// the grammar had been used for before, which is exactly the kind of hidden history that makes a
// replay test pass locally and fail in CI.
func (g *sequiturGrammar) Reset() {
	g.clear()
}

// Snapshot returns the grammar's complete serializable state (plans/sdd/V5-SP-15/contract.md §4).
//
// It is the seam between this grammar core and the codec: this file produces and restores a
// Snapshot, codec.go turns one into bytes and back, and neither has to read the other to do it.
// Everything in the returned value is freshly allocated, so the codec can hold it for as long as
// it likes while the session keeps appending.
func (g *sequiturGrammar) Snapshot() Snapshot {
	return Snapshot{
		Rules:    g.snapshotRules(),
		Sequence: g.Compressed(),
		NextID:   g.nextID,
	}
}

// Restore replaces the grammar's entire state with s, or reports core.ErrDegraded and leaves the
// receiver EXACTLY as it was.
//
// Leaving the receiver untouched is the contract's compatibility behaviour (§4), and it is the
// reason this builds the whole grammar into a fresh value and swaps only at the end: a restore
// that failed halfway and left a half-built grammar behind would look to every consumer like a
// session that had done half as much work, which is worse than a refusal because nothing would
// report it.
//
// Three things about what Restore trusts:
//
//   - Uses, Expansion and Span are DERIVED, so they are recomputed from Body and Sequence rather
//     than read. That makes round-trip identity independent of whether the codec chose to encode
//     the derived fields at all, which is the one degree of freedom the A/B seam leaves open.
//   - Structure is checked and refused: ids must be unique and ascending, NextID must be past the
//     last of them, every rule reference must resolve, the reference graph must be acyclic, and no
//     rule may be unreachable. Each of those is corruption rather than disagreement, and a grammar
//     built from any of them would be lying about what a session did.
//   - Rule utility is NOT re-imposed here. A peer that hands over a rule used once has given us
//     odd evidence, not corrupt evidence, and rewriting another process's grammar on read is not
//     this function's business. The next Append settles it, because enforceRuleUtility runs there.
func (g *sequiturGrammar) Restore(s Snapshot) error {
	fresh, err := buildFromSnapshot(s)
	if err != nil {
		return err
	}
	*g = *fresh
	return nil
}

// MarshalBinary encodes the grammar through the codec (plans/sdd/V5-SP-15/contract.md §4).
//
// It is deliberately four lines. The interface §5.11 froze puts MarshalBinary on the grammar, but
// the byte format — magic, version, the compatibility reader — is one decision with one owner, and
// splitting it across two files would give it two. So this method converts the live grammar to the
// Snapshot both sides agreed on and hands it over; EncodeSnapshot decides what the bytes look
// like. Snapshot cannot fail, so neither can this.
func (g *sequiturGrammar) MarshalBinary() ([]byte, error) {
	return EncodeSnapshot(g.Snapshot()), nil
}

// UnmarshalBinary decodes bytes through the codec and installs the result, or leaves the receiver
// unchanged (plans/sdd/V5-SP-15/contract.md §4).
//
// The two failure modes stay distinct on purpose. DecodeSnapshot's error is "these are not our
// bytes, or not bytes we can read yet" — a bad magic, a version from the future, a truncated
// payload. Restore's is "these are our bytes and they describe an impossible grammar". Both report
// core.ErrDegraded and both leave the grammar alone, which is what a compatibility reader owes its
// caller: an unreadable checkpoint must not become a session that looks like it never repeated
// anything.
func (g *sequiturGrammar) UnmarshalBinary(b []byte) error {
	s, err := DecodeSnapshot(b)
	if err != nil {
		return err
	}
	return g.Restore(s)
}

// buildFromSnapshot validates s and builds the live grammar it describes, or reports
// core.ErrDegraded. It never touches an existing grammar, which is what lets Restore promise the
// receiver is unchanged on failure.
func buildFromSnapshot(s Snapshot) (*sequiturGrammar, error) {
	g := newSequiturGrammar()

	// Pass 1: mint every rule, checking identity before anything references it.
	prev := startRuleID
	for i, r := range s.Rules {
		switch {
		case r.ID < firstInducedRuleID:
			return nil, fmt.Errorf("grammar: restore: rule %d has id %d, below the first induced id %d: %w",
				i, r.ID, firstInducedRuleID, core.ErrDegraded)
		case i > 0 && r.ID <= prev:
			return nil, fmt.Errorf("grammar: restore: rule ids are not ascending at index %d (%d after %d): %w",
				i, r.ID, prev, core.ErrDegraded)
		case len(r.Body) == 0:
			return nil, fmt.Errorf("grammar: restore: rule %d has an empty body: %w", r.ID, core.ErrDegraded)
		}
		prev = r.ID
		nr := &rule{id: r.ID}
		nr.guard = newGuard(nr)
		g.byID[r.ID] = nr
	}

	switch {
	case len(s.Rules) == 0 && s.NextID < firstInducedRuleID:
		// A grammar with no rules has nothing for NextID to collide with, so a snapshot that left
		// the field at zero is a decoder that did not bother to write it, not corruption. Refusing
		// the zero Snapshot would make "an empty grammar" the one value the codec seam could fail
		// to round-trip, which is the opposite of what a compatibility reader is for.
		g.nextID = firstInducedRuleID
	case s.NextID <= prev:
		return nil, fmt.Errorf("grammar: restore: NextID %d would collide with rule %d: %w",
			s.NextID, prev, core.ErrDegraded)
	default:
		g.nextID = s.NextID
	}

	// Pass 2: fill in the bodies and the top-level sequence, resolving references.
	for _, r := range s.Rules {
		if err := g.fill(g.byID[r.ID], r.Body); err != nil {
			return nil, err
		}
	}
	if err := g.fill(g.start, s.Sequence); err != nil {
		return nil, err
	}

	// Pass 3: the two structural refusals. An unreachable rule and a cyclic reference graph are
	// both grammars no induction could have produced, and both would make Rules() nonsense: the
	// first reports a rule that describes nothing the session did, the second expands forever.
	for _, id := range g.sortedIDs() {
		if g.byID[id].uses == 0 {
			return nil, fmt.Errorf("grammar: restore: rule %d is referenced by nothing: %w", id, core.ErrDegraded)
		}
	}
	if id, ok := g.findCycle(); ok {
		return nil, fmt.Errorf("grammar: restore: rule %d takes part in a reference cycle: %w", id, core.ErrDegraded)
	}

	g.reindex()
	return g, nil
}

// fill appends body to owner's list, resolving rule references against the rules already minted.
func (g *sequiturGrammar) fill(owner *rule, body []Symbol) error {
	for _, sym := range body {
		id, isRef := ParseRuleRef(sym)
		if !isRef {
			insertAfter(owner.guard.prev, &node{val: sym})
			continue
		}
		target, ok := g.byID[id]
		if !ok {
			return fmt.Errorf("grammar: restore: rule %d references rule %d, which does not exist: %w",
				owner.id, id, core.ErrDegraded)
		}
		target.uses++
		insertAfter(owner.guard.prev, &node{ref: target})
	}
	return nil
}

// findCycle reports a rule that can reach itself through rule references. A grammar with one is
// not a grammar: its expansion is infinite, so Span and Expansion have no value to report.
func (g *sequiturGrammar) findCycle() (RuleID, bool) {
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := make(map[RuleID]int, len(g.byID))

	var walk func(r *rule) bool
	walk = func(r *rule) bool {
		state[r.id] = onStack
		for n := r.guard.next; !n.isGuard(); n = n.next {
			if n.ref == nil {
				continue
			}
			switch state[n.ref.id] {
			case onStack:
				return true
			case unvisited:
				if walk(n.ref) {
					return true
				}
			}
		}
		state[r.id] = done
		return false
	}

	for _, id := range g.sortedIDs() {
		if state[id] == unvisited && walk(g.byID[id]) {
			return id, true
		}
	}
	return 0, false
}

// reindex rebuilds the digram index over a freshly restored grammar, recording the FIRST occurrence
// of each digram in a fixed walk order — the top-level sequence, then each rule body by ascending
// id — so that two restores of the same snapshot index the same occurrences.
//
// It records rather than resolves. A snapshot that already contained a repeated digram gets one
// occurrence indexed and the other left alone: refusing it would reject readable evidence over a
// property we can re-establish, and rewriting it on read would silently edit another process's
// record of what a session did. The next Append is where the grammar becomes ours again.
func (g *sequiturGrammar) reindex() {
	record := func(r *rule) {
		for n := r.guard.next; !n.isGuard(); n = n.next {
			k, ok := digramAt(n)
			if !ok {
				continue
			}
			if _, seen := g.index[k]; !seen {
				g.index[k] = n
			}
		}
	}
	record(g.start)
	for _, id := range g.sortedIDs() {
		record(g.byID[id])
	}
}
