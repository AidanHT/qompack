package negknow

import "strings"

// maxClassTokens is the number of tokens an approach class keeps. Four is enough to distinguish
// "widen the pool timeout" from "widen the lock timeout" while still collapsing the long,
// discursive phrasings agents produce onto one class.
const maxClassTokens = 4

// maxTokenBytes caps one token. It is the bound that makes the whole result bounded for an
// arbitrarily long input word: four tokens of 24 bytes plus three separators is 99 bytes, so an
// approach class is never larger than 128 bytes however long the approach text was.
const maxTokenBytes = 24

// unclassifiedClass is the approach class of an approach that carries no classifiable content:
// empty, whitespace only, punctuation only, or nothing but stopwords. It is a real class rather
// than the empty string so the descriptor's third field is never silently optional.
const unclassifiedClass = "unclassified"

// stopwords is the closed set of tokens dropped before classification: 48 function words that
// carry no information about which approach was tried. It is closed on purpose — a stopword list
// that grows over time silently re-classes every elimination already on disk.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "but": {}, "by": {}, "can": {},
	"could": {}, "did": {}, "do": {}, "does": {}, "for": {}, "from": {}, "had": {}, "has": {}, "have": {},
	"in": {}, "into": {}, "is": {}, "it": {}, "its": {}, "just": {}, "of": {}, "on": {}, "or": {}, "our": {},
	"so": {}, "than": {}, "that": {}, "the": {}, "their": {}, "them": {}, "then": {}, "there": {}, "this": {},
	"to": {}, "too": {}, "try": {}, "up": {}, "was": {}, "we": {}, "were": {}, "will": {}, "with": {}, "you": {},
}

// synonyms maps a token to its stem. Every value is also a key mapping to itself, so stems are
// fixpoints and classifyToken is idempotent; TestApproachClass_SynonymFixpoint asserts it.
//
// The map is single-valued by construction, so "bump" classifies to widen even in "bump the
// version", and "connection" maps to pool, which is what collapses "connection-pool" and
// "pooling" onto one stem. That is a deliberate coarsening: a coarse class only widens the set of
// records a QUERY can match, and the record lookup remains authoritative — the returned
// Record.Reason is what disambiguates for the agent.
var synonyms = map[string]string{
	// magnitude up
	"widen": "widen", "wide": "widen", "increase": "widen", "raise": "widen", "bump": "widen",
	"enlarge": "widen", "expand": "widen", "grow": "widen", "extend": "widen", "lengthen": "widen", "lift": "widen",
	// magnitude down
	"shrink": "shrink", "reduce": "shrink", "decrease": "shrink", "lower": "shrink", "shorten": "shrink",
	"narrow": "shrink", "tighten": "shrink",
	// objects
	"timeout": "timeout", "deadline": "timeout", "ttl": "timeout", "expiry": "timeout", "expiration": "timeout",
	"pool": "pool", "pooling": "pool", "pooler": "pool", "connectionpool": "pool", "connection": "pool",
	"retry": "retry", "reattempt": "retry", "redo": "retry", "backoff": "retry",
	"cache": "cache", "caching": "cache", "memoize": "cache", "memoization": "cache",
	"lock": "lock", "mutex": "lock", "semaphore": "lock",
	"index": "index", "indice": "index",
	// verbs
	"disable": "disable", "off": "disable", "unset": "disable", "remove": "disable", "delete": "disable",
	"drop": "disable", "strip": "disable", "skip": "disable",
	"enable": "enable", "set": "enable", "add": "enable", "insert": "enable", "introduce": "enable",
	"upgrade": "upgrade", "update": "upgrade", "migrate": "upgrade", "upversion": "upgrade",
	"downgrade": "downgrade", "rollback": "downgrade", "revert": "downgrade", "pin": "downgrade",
	"rewrite": "rewrite", "refactor": "rewrite", "restructure": "rewrite", "reorder": "rewrite",
	"patch": "patch", "monkeypatch": "patch", "shim": "patch", "polyfill": "patch",
	"mock": "mock", "stub": "mock", "fake": "mock",
	"parallel": "parallel", "concurrent": "parallel", "async": "parallel", "goroutine": "parallel", "thread": "parallel",
}

// ApproachClass canonicalizes a free-text approach into the third field of a Descriptor
// (00-ARCHITECTURE.md §5.10, Qompack.md §8.3). It is deterministic, allocation-bounded and does
// no I/O, so the same phrase always produces the same bloom key on every platform.
//
// The algorithm runs in exactly this order:
//
//  1. Lowercase.
//  2. Replace every run of characters outside [a-z0-9] with a single '-', then trim '-'.
//  3. Split on '-' into tokens.
//  4. Drop stopwords.
//  5. Map each surviving token to its stem with classifyToken.
//  6. Drop empty tokens; deduplicate, keeping the first occurrence.
//  7. Truncate each token to maxTokenBytes, then the list to maxClassTokens.
//  8. Join with '-'; an empty result becomes unclassifiedClass.
//
// Step 4 tests the RAW token, before stemming, which is why "try" is a stopword but "tries" is
// classified normally.
func ApproachClass(approach string) string {
	tokens := splitApproach(strings.ToLower(approach))

	out := make([]string, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		if _, stop := stopwords[tok]; stop {
			continue
		}
		stem := classifyToken(tok)
		if stem == "" {
			continue
		}
		if _, dup := seen[stem]; dup {
			continue
		}
		seen[stem] = struct{}{}
		out = append(out, truncateToken(stem))
		if len(out) == maxClassTokens {
			break
		}
	}

	if len(out) == 0 {
		return unclassifiedClass
	}
	return strings.Join(out, "-")
}

// splitApproach performs steps 2 and 3 on an already-lowercased string: every run of characters
// outside [a-z0-9] becomes one '-', leading and trailing '-' are trimmed, and the result is split
// into tokens.
//
// The scan is byte-wise rather than rune-wise, which is equivalent here and cheaper: every byte
// of a multi-byte UTF-8 rune is >= 0x80 and therefore outside [a-z0-9], so a run of non-ASCII
// runes collapses to the same single '-' either way. The consequence used elsewhere in this file
// is that every token is pure ASCII.
func splitApproach(lowered string) []string {
	var b strings.Builder
	b.Grow(len(lowered))
	dash := false
	for i := range len(lowered) {
		c := lowered[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "-")
}

// truncateToken applies the per-token byte cap of step 7.
//
// splitApproach guarantees every token is pure ASCII, so cutting at a byte index is always a
// valid UTF-8 rune boundary; there is no partial rune this could leave behind.
func truncateToken(tok string) string {
	if len(tok) <= maxTokenBytes {
		return tok
	}
	return tok[:maxTokenBytes]
}

// lemma is the crude, single-pass lemmatiser of step 5. Exactly these four rules fire, in this
// order, and at most one of them fires per token.
//
// It is deliberately not a real stemmer: over-stemming would collapse distinct approaches onto
// one class, and the length guards are what stop it ("is" is too short for any rule, "pass" keeps
// its double s). Known and accepted limitation: doubled-consonant gerunds are not undoubled, so
// "dropping" stems to "dropp" rather than reaching "drop". That is a recall loss on a query and
// never a wrong answer, because the record lookup is authoritative.
func lemma(tok string) string {
	switch {
	case len(tok) > 4 && strings.HasSuffix(tok, "ies"): // retries -> retry
		return tok[:len(tok)-3] + "y"
	case len(tok) > 4 && strings.HasSuffix(tok, "ing"): // pooling -> pool
		return tok[:len(tok)-3]
	case len(tok) > 3 && strings.HasSuffix(tok, "ed"): // widened -> widen
		return tok[:len(tok)-2]
	case len(tok) > 3 && strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss"):
		return tok[:len(tok)-1] // timeouts -> timeout
	}
	return tok
}

// classifyToken maps one token to its stem in exactly five steps, with no other lookup and no
// iteration: the raw token, then its lemma, then the lemma with a silent 'e' restored, then the
// lemma unchanged.
//
// The silent-e restore is what keeps the synonyms table small. Stripping "ing" or "ed" from an -e
// verb loses the e (increasing -> increas, disabling -> disabl, rewriting -> rewrit), so one
// restore attempt recovers the dictionary form instead of requiring a second table entry for
// every verb.
func classifyToken(tok string) string {
	if v, ok := synonyms[tok]; ok { // 1. raw token is a key
		return v
	}
	l := lemma(tok)               // 2. crude lemma, applied once
	if v, ok := synonyms[l]; ok { // 3. lemma is a key
		return v
	}
	if v, ok := synonyms[l+"e"]; ok { // 4. silent-e restore: increas -> increase
		return v
	}
	return l // 5. unmapped: keep the lemma
}
