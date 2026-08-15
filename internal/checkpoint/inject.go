package checkpoint

import "strings"

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
const (
	// InjectionOpenTag opens an injected span. Format arguments: seq, ver.
	InjectionOpenTag = "<!-- qompack:injected seq=%d ver=%d -->"
	// InjectionCloseTag closes an injected span.
	InjectionCloseTag = "<!-- /qompack:injected -->"
)

// injectionOpenPrefix is InjectionOpenTag's fixed, verb-free prefix ("<!-- qompack:injected
// seq="), derived from the constant itself rather than written out a second time so the two can
// never drift apart. It is what StripInjections scans for, since the seq and ver values in a real
// open tag vary.
var injectionOpenPrefix = InjectionOpenTag[:strings.IndexByte(InjectionOpenTag, '%')]

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
	var b strings.Builder
	rest := s
	for {
		open := strings.Index(rest, injectionOpenPrefix)
		if open < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:open])

		// Find the end of the open tag itself, so the search for the close tag starts after it.
		tagEnd := strings.Index(rest[open:], injectionTagSuffix)
		if tagEnd < 0 {
			// An unterminated open tag: everything from it onward is injected.
			return b.String()
		}
		bodyStart := open + tagEnd + len(injectionTagSuffix)

		closeAt := strings.Index(rest[bodyStart:], InjectionCloseTag)
		if closeAt < 0 {
			// Missing close tag: drop to the end of the string.
			return b.String()
		}
		rest = rest[bodyStart+closeAt+len(InjectionCloseTag):]
	}
}
