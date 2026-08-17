package canon

import "sort"

// MaxInputBytes is the largest prefix of an input canonicalization examines. Beyond it the
// remainder is appended verbatim.
//
// The cap exists because Registry.Run sits on the PostToolUse hot path (Qompack.md §8.1: target
// < 15ms p99) and every rule below is linear in the bytes it scans. Truncating the SCAN rather
// than the CONTENT keeps the pass total — nothing is lost, the tail is simply not canonicalized —
// and keeps Delta offsets valid, because the tail is concatenated after composition has already
// produced its coordinates.
const MaxInputBytes = 8 << 20

// candidate is one Match plus the identity of the Canonicalizer that emitted it: rank is that
// canonicalizer's index in registration order and owner is its Name. Both exist only to make
// overlap resolution and Result.Applied deterministic.
type candidate struct {
	Match
	rank  int
	owner string
}

// splitCapped divides in into the prefix canonicalization scans and the verbatim tail.
func splitCapped(in []byte) (head, tail []byte) {
	if len(in) <= MaxInputBytes {
		return in, nil
	}
	return in[:MaxInputBytes], in[MaxInputBytes:]
}

// sortCandidates orders cands the way acceptCandidates needs to read them: by ascending Offset,
// then by DESCENDING Len, then by ascending rank.
//
// Longest-wins at a shared offset is what makes overlapping rules compose sensibly — an ISO-8601
// timestamp and the bare clock inside it start at different offsets, but a rule pair that starts
// at the same byte is nearly always a specific rule and a general one, and the specific rule is
// the longer match. Registration order breaks the remaining ties so the result never depends on
// map iteration or on which canonicalizer happened to be asked first.
func sortCandidates(cands []candidate) {
	sort.SliceStable(cands, func(a, b int) bool {
		x, y := cands[a], cands[b]
		if x.Offset != y.Offset {
			return x.Offset < y.Offset
		}
		if x.Len != y.Len {
			return x.Len > y.Len
		}
		return x.rank < y.rank
	})
}

// acceptCandidates filters sorted candidates down to the non-overlapping set that will actually
// be applied. It reuses cands' backing array: the loop never writes past the index it is reading.
//
// Four rejections, in order of how structural they are:
//
//   - Class not in gate. This is the ONE place Options.Strip is enforced. A Matcher emits every
//     span its rules find; whether that class is in play is the registry's decision, so a
//     canonicalizer cannot bypass a user's store.canonicalize.strip setting. A Match carrying the
//     zero Class is dropped here too, which is what TestMatcherClassAssigned guards against.
//   - Token longer than the span. 00-ARCHITECTURE.md §5.6: no canonicalizer ever grows its input.
//     Enforcing it structurally, rather than trusting fourteen implementations, means a two-digit
//     PID never becomes the five-byte "<n>" and the property holds by construction.
//   - Zero length. A zero-length span is an insertion, which no canonicalizer may perform.
//   - Overlap with an already-accepted match.
func acceptCandidates(cands []candidate, gate map[Class]bool) []candidate {
	accepted := cands[:0]
	end := 0
	for _, m := range cands {
		switch {
		case !gate[m.Class]:
		case len(m.Token) > m.Len:
		case m.Len == 0:
		case m.Offset < end:
		default:
			accepted = append(accepted, m)
			end = m.End()
		}
	}
	return accepted
}

// applyMatches rewrites in according to ms, which must already be sorted and non-overlapping, and
// returns the canonical bytes together with the Delta side record when keep is set.
//
// Delta.Offset is in CANONICAL coordinates and Delta.Len is the length of the token that landed
// there, so Restore can walk the deltas and the canonical buffer in one forward pass.
func applyMatches(in []byte, ms []candidate, keep bool) ([]byte, []Delta) {
	out := make([]byte, 0, len(in))
	var deltas []Delta
	if keep {
		deltas = make([]Delta, 0, len(ms))
	}
	prev := 0
	for _, m := range ms {
		out = append(out, in[prev:m.Offset]...)
		if keep {
			deltas = append(deltas, Delta{
				Offset:   len(out),
				Len:      len(m.Token),
				Original: string(in[m.Offset:m.End()]),
				Class:    m.Class,
			})
		}
		out = append(out, m.Token...)
		prev = m.End()
	}
	return append(out, in[prev:]...), deltas
}

// compose is the shared body of Registry.Run and of every built-in's standalone Canonicalize:
// sort, accept, apply, and report which owners contributed.
//
// An input that canonicalizes away entirely returns a nil Canonical, matching what Registry.Run
// and canonicalizeVia return for an empty input. applyMatches allocates its buffer up front, so
// without this an all-stripped input would come back as a non-nil zero-length slice while
// re-running on that result — which is the idempotence check — would take the empty-input short
// circuit and come back nil. Nothing distinguishes the two except reflect.DeepEqual, and that is
// exactly what surfaced it: a lone ESC strips to nothing, and Run(Run(x)) != Run(x) on a
// representation detail rather than on the bytes.
func compose(in []byte, cands []candidate, o Options) (canonical []byte, deltas []Delta, accepted []candidate) {
	head, tail := splitCapped(in)
	sortCandidates(cands)
	accepted = acceptCandidates(cands, gateSet(o.Strip))
	canonical, deltas = applyMatches(head, accepted, o.KeepDeltas)
	canonical = append(canonical, tail...)
	if len(canonical) == 0 {
		canonical = nil
	}
	return canonical, deltas, accepted
}

// appliedNames returns the distinct owners of accepted, in ascending rank — that is, in
// registration order.
//
// 00-ARCHITECTURE.md §5.6 calls Result.Applied "canonicalizer names, in application order".
// Because composition is a single pass over the original input rather than a transform chain,
// there is no sequence in which the canonicalizers ran one after another; registration order IS
// the application order, and it is the only ordering that is stable across inputs.
func appliedNames(accepted []candidate) []string {
	seen := make(map[string]bool, len(accepted))
	ranked := make([]candidate, 0, len(accepted))
	for _, m := range accepted {
		if seen[m.owner] {
			continue
		}
		seen[m.owner] = true
		ranked = append(ranked, m)
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].rank < ranked[b].rank })
	out := make([]string, len(ranked))
	for i, m := range ranked {
		out[i] = m.owner
	}
	return out
}

// reduced is 1 - len(canonical)/len(in), the fraction of the input canonicalization removed.
func reduced(inLen, outLen int) float64 {
	if inLen == 0 {
		return 0
	}
	return 1 - float64(outLen)/float64(inLen)
}

// canonicalizeVia is the standalone Canonicalize every built-in delegates to: it composes m's own
// matches exactly as Registry.Run would with m as the only registered canonicalizer, so a
// canonicalizer behaves identically whether it is used alone or inside the registry.
//
// Result.Signature is deliberately left zero here. Only Registry.Run computes a MinHash, because
// only Run sees the fully canonicalized bytes; a signature over one canonicalizer's partial
// output would be a near-duplicate score for a document that never gets stored.
func canonicalizeVia(m Matcher, name string, in []byte, o Options) (Result, error) {
	if len(in) == 0 {
		return Result{Applied: []string{}}, nil
	}
	head, _ := splitCapped(in)
	found := m.Matches(head, o)
	cands := make([]candidate, len(found))
	for i, f := range found {
		cands[i] = candidate{Match: f, owner: name}
	}
	canonical, deltas, accepted := compose(in, cands, o)
	return Result{
		Canonical: canonical,
		Deltas:    deltas,
		Applied:   appliedNames(accepted),
		Reduced:   reduced(len(in), len(canonical)),
	}, nil
}
