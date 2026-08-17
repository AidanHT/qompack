package redact

import (
	"bytes"
	"regexp"
	"sort"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Redactor finds and replaces secrets in content on its way into the store
// (00-ARCHITECTURE.md §5.22a).
type Redactor interface {
	// Redact replaces every match with a fixed-width placeholder "«redacted:<rule>»". It MUST be
	// deterministic and MUST NOT grow the input beyond a bounded factor, so chunk boundaries stay
	// stable between a redacted and an unredacted read of the same file.
	Redact(in []byte) (out []byte, matches []Match)
	// Rules returns the names of every rule currently active.
	Rules() []string
}

// The placeholder a redacted span is replaced with: placeholderOpen + rule name + placeholderClose.
//
// placeholderClose is a STRING, not a rune constant, and that is load-bearing. "»" is U+00BB, two
// bytes in UTF-8; appending it to a []byte as a rune constant would write the single byte 0xBB and
// produce invalid UTF-8 whose shape placeholderRe can never match again — which silently disables
// the idempotence guard below, since a second pass would no longer recognize its own output.
const (
	placeholderOpen  = "«redacted:"
	placeholderClose = "»"
)

// placeholderRe matches an already-emitted placeholder. Every rule name is lowercase ASCII with
// underscores, plus the colon of custom:<i>, so this character class covers the whole namespace.
var placeholderRe = regexp.MustCompile(`«redacted:[a-z0-9_:]+»`)

// maxRuleNameLen bounds a rule name, and therefore a placeholder's width. custom:<i> stays inside
// it for any realistic pattern count, and every built-in name is well under it.
const maxRuleNameLen = 24

// minMatchBytes is the smallest full match a rule may report.
//
// It is applied to the FULL match span, not to the span actually replaced. That distinction is
// deliberate: assignment_secret legitimately replaces a one-byte value in "password=x", and
// refusing that would leave a real secret in objects/. What the growth bound actually needs is
// that matches cannot tile the input densely, and density is governed by the full match — for
// assignment_secret that is the key, separator and value together.
const minMatchBytes = 3

// maxPlaceholderBytes is the widest placeholder this package can emit:
// len("«redacted:") + maxRuleNameLen + len("»"). Both guillemets are two bytes in UTF-8.
const maxPlaceholderBytes = len(placeholderOpen) + maxRuleNameLen + len(placeholderClose)

// New returns a Redactor configured by cfg. It is the frozen 00-ARCHITECTURE.md §5.22a signature
// and carries neither a logger nor a metrics registry, so it delegates to NewWithObs with
// logging.Nop() — whose Loud calls still reach the process-wide ring logging.LastLoud reads, so a
// refused user pattern remains observable even through this constructor.
//
// When runtime.redact.enabled is false the returned Redactor's Redact is the identity and Rules is
// empty. store.Open still calls it, so the §13 invariant 7 choke point is never bypassed at the
// call site; only the rule set is empty.
func New(cfg config.Config) Redactor {
	return NewWithObs(cfg, logging.Nop(), nil)
}

// NewWithObs is New with an explicit logger and metrics registry, so a composition root that has
// both can see refused user patterns on its own logger and counter rather than only in the
// process-wide ring. It is additive: New keeps its frozen signature and delegates here.
//
// A nil registry means "do not count"; a nil logger means "do not log". Neither is an error.
func NewWithObs(cfg config.Config, log logging.Logger, m obs.Registry) Redactor {
	if !cfg.Runtime.Redact.Enabled {
		return &rx{}
	}
	rules := builtinRules()
	rules = append(rules, compileUserPatterns(cfg.Runtime.Redact.Patterns, log, m)...)
	for i := range rules {
		if len(rules[i].name) > maxRuleNameLen {
			rules[i].name = rules[i].name[:maxRuleNameLen]
		}
	}
	return &rx{enabled: true, rules: rules}
}

// rx is the real Redactor. It is immutable after construction and therefore safe for concurrent
// use by the daemon's worker pool: Redact allocates everything it touches.
type rx struct {
	enabled bool
	rules   []rule
}

// Rules returns every active rule name, built-ins first in §5.22a order, then user patterns as
// custom:<i>.
func (r *rx) Rules() []string {
	if !r.enabled {
		return nil
	}
	out := make([]string, len(r.rules))
	for i, rule := range r.rules {
		out[i] = rule.name
	}
	return out
}

// Redact replaces every rule match with its placeholder and reports the spans it replaced, using
// offsets into the ORIGINAL input.
//
// Growth bound. Every placeholder is at most maxPlaceholderBytes (37) wide, no two replaced spans
// overlap, and every replaced span is non-empty, so the output can never exceed
// maxPlaceholderBytes * len(in) + maxPlaceholderBytes. For user patterns — the only rules an
// operator controls, and so the only ones that could be adversarial — the admission rules in
// rules.go force a full match of at least minMatchBytes and the whole match is what is replaced,
// giving the far tighter 13 * len(in) + 37. On real content the practical figure is a rounding
// error: see TestRedact_BoundedGrowth, which pins len(out) <= 2*len(in) + 64 across the corpus.
//
// Idempotence. Placeholders emitted by a previous pass are seeded into the consumed set before any
// rule runs, so they are immutable and unmatchable: Redact(Redact(x)) == Redact(x), and the second
// pass reports no matches at all. This is what makes the choke point safe to re-run on a WAL
// replay or a retried hook.
func (r *rx) Redact(in []byte) (out []byte, matches []Match) {
	if len(in) == 0 || !r.enabled {
		return in, nil
	}

	var consumed intervals
	for _, loc := range placeholderRe.FindAllIndex(in, -1) {
		consumed.add(loc[0], loc[1])
	}

	// folded is the ASCII-lowercased copy the fold rules prefilter against. It is built at most
	// once per call, and only if a fold rule actually reaches the prefilter.
	var folded []byte

	var hits []Match
	for _, rule := range r.rules {
		if !rule.mayMatch(in, &folded) {
			continue
		}
		for _, loc := range rule.re.FindAllSubmatchIndex(in, -1) {
			matchStart, matchEnd := loc[0], loc[1]
			start, end := matchStart, matchEnd
			if rule.group > wholeMatch {
				start, end = loc[2*rule.group], loc[2*rule.group+1]
				if start < 0 {
					continue // the group did not participate in this match
				}
			}
			if matchEnd-matchStart < minMatchBytes {
				continue
			}
			if end <= start {
				continue
			}
			if consumed.overlaps(start, end) {
				continue
			}
			consumed.add(start, end)
			hits = append(hits, Match{Offset: start, Len: end - start, Rule: rule.name})
		}
	}
	if len(hits) == 0 {
		return in, nil
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Offset < hits[j].Offset })

	out = make([]byte, 0, len(in)+len(hits)*maxPlaceholderBytes)
	prev := 0
	for _, h := range hits {
		out = append(out, in[prev:h.Offset]...)
		out = append(out, placeholderOpen...)
		out = append(out, h.Rule...)
		out = append(out, placeholderClose...)
		prev = h.Offset + h.Len
	}
	return append(out, in[prev:]...), hits
}

// mayMatch reports whether in could possibly contain a match for this rule, by testing the rule's
// mandatory literals with bytes.Contains before the far more expensive regex runs.
//
// It is a conservative gate: it may return true when there is no match (the regex then runs and
// finds nothing, exactly as before), but it must never return false when a match exists. That
// direction is what the literals' mandatory-by-construction property guarantees, and what
// TestPrefilter_IsBehaviourNeutral and FuzzRedactPrefilterEquivalence hold it to.
//
// folded is the caller's lazily-built ASCII-lowercased copy of in, shared across every fold rule
// in one Redact call so it is allocated at most once.
func (ru rule) mayMatch(in []byte, folded *[]byte) bool {
	if len(ru.lits) == 0 {
		return true // no prefilter: user patterns, which are rare and arbitrary
	}
	haystack := in
	if ru.fold {
		if *folded == nil {
			*folded = asciiLower(in)
		}
		haystack = *folded
	}
	for _, lit := range ru.lits {
		if bytes.Contains(haystack, lit) {
			return true
		}
	}
	return false
}

// asciiLower returns a copy of b with A-Z mapped to a-z and every other byte left alone.
//
// ASCII-only is deliberate. It keeps the copy byte-for-byte the same length as the input, so the
// two are positionally interchangeable, and it is all that is needed: Go's (?i) folds ASCII
// letters, and every folded literal in the rule table is pure ASCII. bytes.ToLower would additionally
// case-fold non-ASCII runes, which can change the encoded length — harmless here, since the copy is
// only ever used for a presence test, but slower and misleading.
func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

// interval is one half-open byte span [s, e) that has already been claimed.
type interval struct{ s, e int }

// intervals is a set of non-overlapping claimed spans, kept sorted by start offset so overlaps and
// add are both O(log n) searches plus an insert. Rules are applied in a fixed order and the first
// rule to claim a span keeps it, which is how anthropic_key wins over generic_sk_key and how an
// existing placeholder wins over everything.
type intervals []interval

// search returns the index of the first span whose end is strictly greater than s.
func (v intervals) search(s int) int {
	return sort.Search(len(v), func(i int) bool { return v[i].e > s })
}

// overlaps reports whether [s, e) intersects any claimed span.
func (v intervals) overlaps(s, e int) bool {
	i := v.search(s)
	return i < len(v) && v[i].s < e
}

// add claims [s, e). Callers check overlaps first, so the inserted span never intersects an
// existing one and the slice stays sorted.
func (v *intervals) add(s, e int) {
	i := v.search(s)
	*v = append(*v, interval{})
	copy((*v)[i+1:], (*v)[i:])
	(*v)[i] = interval{s: s, e: e}
}

// nopRedactor is Nop's real, permanent implementation: an honest no-op, not a placeholder for a
// future real one.
type nopRedactor struct{}

// Nop returns a Redactor that passes its input through unchanged, reporting no matches and an
// empty rule set. It is fully specified by the architecture, so SP-01 implements it for real
// rather than stubbing it (00-ARCHITECTURE.md §14.1): tests throughout the tree that need "a
// Redactor" but are not testing redaction itself need one that is honestly inert. Nop is for
// tests; production code always goes through New, and store.Open defaults a nil Deps.Redact to
// New — never to Nop, because a nil redactor must never mean "no redaction".
func Nop() Redactor { return nopRedactor{} }

// Redact returns in unchanged with no matches — always, by design, not as a stand-in for
// unimplemented behaviour.
func (nopRedactor) Redact(in []byte) (out []byte, matches []Match) { return in, nil }

// Rules always returns nil: Nop has no rules, by design.
func (nopRedactor) Rules() []string { return nil }
