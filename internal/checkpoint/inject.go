package checkpoint

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// The injection tags of Qompack.md §8.5's regeneration rule (00-ARCHITECTURE.md §5.14).
//
// Everything Qompack injects into the transcript — a rehydrated digest, a focus instruction, a
// thrash warning — is wrapped in an open/close pair so that it can be recognized and removed
// again on the way back in. Without that, the next checkpoint would encode the previous
// checkpoint's own injected text, which is precisely the compress-a-compression failure §4.6
// forbids.
//
// InjectionOpenTag is a fmt format string: seq is the checkpoint sequence the injected body came
// from, and ver is SchemaVersion.
//
// The tags are model-visible, so they name what the span IS — Qompack's saved record of the
// session — rather than how it arrived: the c55-c8 live eval saw a model distrust a block whose tag
// read "injected". Releases through 0.3.x wrote the Legacy spelling; transcripts still hold it, so
// StripInjections recognizes both, each with every guarantee documented on it.
const (
	// InjectionOpenTag opens an injected span. Format arguments: seq, ver.
	InjectionOpenTag = "<!-- qompack:session-record seq=%d ver=%d -->"
	// InjectionCloseTag closes an injected span.
	InjectionCloseTag = "<!-- /qompack:session-record -->"
	// LegacyInjectionOpenTag is the open tag 0.3.x and earlier wrote. Format arguments: seq, ver.
	LegacyInjectionOpenTag = "<!-- qompack:injected seq=%d ver=%d -->"
	// LegacyInjectionCloseTag is the close tag 0.3.x and earlier wrote.
	LegacyInjectionCloseTag = "<!-- /qompack:injected -->"
)

// injectionSpelling is one recognized spelling of the tags: the open tag's fixed, verb-free prefix
// ("<!-- qompack:session-record seq="), derived from the constant itself so the two can never drift
// apart, and the close tag that ends a span it opens. The prefix is what StripInjections scans for,
// since the seq and ver values in a real open tag vary.
type injectionSpelling struct{ open, close string }

// injectionSpellings are the spellings StripInjections recognizes: the current one and the legacy
// one. No prefix is a prefix of another, so two spellings never open at the same index.
var injectionSpellings = [...]injectionSpelling{
	{InjectionOpenTag[:strings.IndexByte(InjectionOpenTag, '%')], InjectionCloseTag},
	{LegacyInjectionOpenTag[:strings.IndexByte(LegacyInjectionOpenTag, '%')], LegacyInjectionCloseTag},
}

// nextOpen returns the index of the earliest open prefix of any spelling in s, and that spelling;
// at is -1 when s holds none.
func nextOpen(s string) (at int, sp injectionSpelling) {
	at = -1
	for _, c := range injectionSpellings {
		if i := strings.Index(s, c.open); i >= 0 && (at < 0 || i < at) {
			at, sp = i, c
		}
	}
	return at, sp
}

// injectionTagSuffix closes the open tag itself (not the injected span).
const injectionTagSuffix = " -->"

// StripInjections removes every open…close injected span from s, returning the surrounding text
// unchanged. It is used when reading any transcript-derived text, so that Qompack's own prior
// injections are never re-encoded into a new checkpoint (Qompack.md §8.5, §4.6).
//
// It is fully specified by the architecture, so SP-01 implements it for real rather than stubbing
// it (§14.1 rule 3 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): SP-08 strips prompts
// and SP-11 wraps and unwraps rehydrated digests, both long before SP-10's writer exists.
//
// Three properties are normative and are what the tests pin:
//
//   - Non-greedy. Each span ends at the FIRST close tag after its own open tag, so two adjacent
//     injected spans are two spans, not one span swallowing the text between them.
//   - Tolerant of a missing close tag. An open tag with no close tag after it drops everything
//     from the open tag to the end of the string: the alternative — keeping it — would re-encode
//     an injected body, and a truncated transcript is exactly when that happens.
//   - A close tag with no open tag before it is ordinary text. It delimits nothing, so removing
//     it would silently edit content Qompack did not write.
func StripInjections(s string) string {
	out, _ := StripInjectionsCount(s)
	return out
}

// StripInjectionsCount is StripInjections plus the number of injected spans it removed. The two
// share ONE scan — StripInjections delegates here — so they can never disagree about what an
// injected span is, and every property documented on StripInjections holds verbatim of this one.
//
// The count is the number of open tags consumed, so a span whose close tag is missing counts as
// one: it was recognized and its body was dropped, which is exactly the event a caller wants to
// know about. A neutralized splice (below) counts too — it is the same near-miss being caught.
//
// # The splice, and why one pass is not enough
//
// Removing a span concatenates the text before its open tag with the text after its close tag, and
// those two halves can SPLICE INTO A NEW OPEN TAG that nobody wrote. One pass over
//
//	INPUT = "<!-- qompack:session-record se" + OpenTag(1) + "X" + InjectionCloseTag + "q=7 ver=1 -->tail"
//
// yields "<!-- qompack:session-record seq=7 ver=1 -->tail" — a well-formed, unterminated open tag. Since
// fromStore runs on every store read, the NEXT read would obey the missing-close-tag property and
// drop "tail" as an injected body, silently deleting text a user wrote.
//
// Simply re-running the scan to a fixed point does NOT fix that. It makes the function idempotent
// by performing the same deletion one call earlier: "tail" is still lost, and a checkpoint that
// deletes the user's words is the one failure this whole layer exists to prevent.
//
// # Join-aware neutralization
//
// The fix rests on a property of the scan itself: every chunk stripInjectionsOnce keeps is written
// as rest[:open], where open is the index of the FIRST open prefix in rest, of either spelling — so
// no kept chunk can contain a full open prefix of any spelling, and neither can the trailing chunk,
// which is only written when no prefix of any spelling remains. Therefore an open prefix present in a pass's OUTPUT provably straddles a join
// that pass created. It is an artifact of stripping, never something the reader wrote.
//
// That proof establishes exactly one thing, and the code claims exactly that much: those
// len(injectionOpenPrefix) bytes are an artifact. It says nothing whatever about the bytes AFTER
// them, so those are kept. An artifact is therefore removed as TEXT rather than treated as a span
// — the assembled prefix, and only the assembled prefix. No close tag is searched for, because
// there is no injected body here: the body was removed on the pass that created the join. The
// splice above therefore yields "7 ver=1 -->tail" — visible residue, and the user's "tail" intact.
//
// Deleting on through the artifact's own " -->" instead would be unbounded, because the nearest
// " -->" after a join belongs to whatever the reader wrote next: an ordinary HTML comment, or the
// tail of an unmatched close tag that the third property above declares to be ordinary text. Every
// byte in between, however many paragraphs of it, would go. fromStore runs on every string entering
// a checkpoint from the store, so that is the checkpoint deleting the user's own words — the one
// failure this layer exists to prevent, and the opposite of §8.5, whose regeneration rule is that
// tagged material is IGNORED as a source for the next pass, not that its neighbours are erased.
// Residue is the strictly better trade.
//
// The postcondition is that the result contains no open prefix of any spelling at all, which makes StripInjections
// idempotent outright rather than by construction of a loop. Nothing more is promised: no byte that
// was not part of an assembled prefix is ever removed here.
//
// Cost on the common path is nothing: text with no injected span leaves the scan with n == 0 and
// returns before any of this runs.
func StripInjectionsCount(s string) (string, int) {
	out, n := stripInjectionsOnce(s)
	if n == 0 {
		return out, 0
	}
	out, m := neutralizeSplices(out)
	return out, n + m
}

// neutralizeSplices removes every open prefix left in a stripped string — exactly those
// len(injectionOpenPrefix) bytes and not one more — keeping everything else. See
// StripInjectionsCount for why every prefix it can find is an artifact of a join rather than
// reader-written text, and for why the deletion stops at the prefix instead of running on to the
// next " -->": that arrow belongs to the reader's own text, and following it deletes an unbounded
// run of it.
//
// It rescans from the start after each removal, because a removal creates a join of its own and can
// therefore assemble a further tag. The loop needs no iteration cap to be safe: every iteration
// removes exactly len(injectionOpenPrefix) bytes, a fixed positive amount, and a string of finite
// length admits finitely many such steps.
func neutralizeSplices(s string) (string, int) {
	n := 0
	for {
		open, sp := nextOpen(s)
		if open < 0 {
			return s, n
		}
		// Only the assembled prefix is provably an artifact. Everything after it — including any
		// " -->" downstream — is the reader's text and is kept verbatim.
		s = s[:open] + s[open+len(sp.open):]
		n++
	}
}

// stripInjectionsOnce is one pass of the scan. It is not idempotent on its own — see the splice
// case documented on StripInjectionsCount, which is why nothing outside that function may call it.
func stripInjectionsOnce(s string) (string, int) {
	var b strings.Builder
	rest := s
	n := 0
	for {
		open, sp := nextOpen(rest)
		if open < 0 {
			b.WriteString(rest)
			return b.String(), n
		}
		b.WriteString(rest[:open])
		n++

		// Find the end of the open tag itself, so the search for the close tag starts after it.
		tagEnd := strings.Index(rest[open:], injectionTagSuffix)
		if tagEnd < 0 {
			// An unterminated open tag: everything from it onward is injected.
			return b.String(), n
		}
		bodyStart := open + tagEnd + len(injectionTagSuffix)

		// A span ends at its OWN spelling's close tag: the other spelling's is ordinary text here.
		closeAt := strings.Index(rest[bodyStart:], sp.close)
		if closeAt < 0 {
			// Missing close tag: drop to the end of the string.
			return b.String(), n
		}
		rest = rest[bodyStart+closeAt+len(sp.close):]
	}
}

// OpenTag renders the open tag for a span injected from checkpoint seq, at the current schema
// version. It is the only place InjectionOpenTag's two format arguments are supplied, so a caller
// can never pair a sequence number with the wrong version.
func OpenTag(seq core.CheckpointSeq) string {
	return fmt.Sprintf(InjectionOpenTag, int(seq), SchemaVersion)
}

// fromStore renders bytes read back out of the object store as text fit to encode into a
// checkpoint, stripping any injected span the transcript carried and counting what it removed.
//
// Every string that enters a checkpoint from the store goes through here. That is the operational
// form of Qompack.md §8.5's regeneration rule: the rehydrator's own prior injection lives in
// message history, will be mangled by the next summarization pass, and must never become a source
// for the next checkpoint. A non-zero count is that near-miss being caught, so it is metered.
func fromStore(b []byte) string {
	s, n := StripInjectionsCount(string(b))
	if n > 0 {
		pkgMetrics().Counter("checkpoint.injection_stripped").Add(int64(n))
	}
	return s
}
