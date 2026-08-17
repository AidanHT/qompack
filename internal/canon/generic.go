package canon

import (
	"bytes"
	"regexp"
)

// The replacement tokens every rule in this package substitutes for a volatile span
// (00-ARCHITECTURE.md §5.6).
//
// Every token is bracketed with '<' and '>'. That is not decoration: it is what makes the
// idempotence §5.6 demands — Canonicalize(Canonicalize(x)) == Canonicalize(x), byte for byte — a
// STRUCTURAL property rather than a hoped-for one. No pattern in this package can match a token,
// because no pattern admits '<' or '>' except inside a body it can only reach through a prefix the
// token does not reproduce (an ESC for the ANSI bodies, a `W/"` or `nonce="` for the quoted
// webfetch bodies, a `/tmp/`-class prefix for the temp-path tails). A second pass therefore has
// nothing left to find, and the property holds for every input rather than for the inputs someone
// happened to test.
//
// The corollary is that these strings may not be widened casually. Adding a digit, a colon or a
// dot to any token below would put it back inside the language of the rule that emits it and
// silently break idempotence for that rule alone.
const (
	// tokenTimestamp replaces a wall-clock timestamp in any of the five shapes timestampRules
	// recognizes. Four bytes, against a 19-byte minimum span (an ISO-8601 date-time).
	tokenTimestamp = "<ts>"
	// tokenDuration replaces an elapsed-time value. Three bytes: SHORTER than "<ts>" because a
	// duration can legitimately be as short as "3ns", and longer than the shortest span the simple
	// duration rule can produce ("5s"), which the registry's non-growing guard therefore drops.
	tokenDuration = "<d>"
	// tokenNumber replaces a small volatile integer: a PID, a goroutine number, a jest worker or a
	// pytest xdist slot. Three bytes, which is longer than a two-digit capture; see the guard note
	// on pidRules.
	tokenNumber = "<n>"
	// tokenAddress replaces a memory address or an opaque hex identity.
	tokenAddress = "<addr>"
	// tokenPort replaces the port of a loopback address. Three bytes against a 4-digit minimum.
	tokenPort = "<p>"
	// tokenTmpPath replaces a temp-directory path in any of the five layouts tmpPathRules covers.
	tokenTmpPath = "<tmp>"
)

// lf and slash are the two non-bracketed replacement tokens: the structural normalizations of
// 00-ARCHITECTURE.md §4 rewrite one byte to another rather than eliding a volatile value, so there
// is nothing for a '<'…'>' token to stand in for. Idempotence for these is argued separately, at
// the rule that emits them, from the fact that the OUTPUT byte can no longer participate in the
// pattern (a lone '\n' is not "\r+\n"; a '/' is not a '\\').
var (
	lf    = []byte("\n")
	slash = []byte("/")
)

// The three span selections a reRule can ask for. spans are capture-group indices in
// (*regexp.Regexp).FindAllSubmatchIndex's coordinate system, where 0 is the whole match.
var (
	wholeMatch = []int{0}
	firstGroup = []int{1}
	bothGroups = []int{1, 2}
)

// monthNames is the syslog timestamp rule's prefilter. Twelve literals is more than any other rule
// needs, and it is still two orders of magnitude cheaper than scanning: the rule cannot match
// without one of them, and syslog-shaped output is rare enough in a coding session that the
// twelve misses are what normally happens.
var monthNames = []string{
	"Jan ", "Feb ", "Mar ", "Apr ", "May ", "Jun ", "Jul ", "Aug ", "Sep ", "Oct ", "Nov ", "Dec ",
}

// The pattern fragments every LINE-ANCHORED rule is built from.
//
// Composition is a single pass over the ORIGINAL bytes (see the rationale on Match), which means a
// rule anchored with ^ or $ sees the line exactly as the tool wrote it — not as the other thirteen
// canonicalizers will leave it. Four things routinely sit between a line boundary and the token an
// anchored rule keys on, and all four are removed by some OTHER canonicalizer in the same pass:
//
//   - SGR escape sequences, from any colour-capable tool. `git diff --color` emits
//     "\x1b[1mindex 7d805ef..5bfe6ae\x1b[m", so a bare `^index ` matches nothing on the original
//     and matches on the canonical output.
//   - A carriage-returned prefix, from any progress bar. bashCanon deletes it, so the line that
//     actually printed only reaches the start of a line after canonicalization.
//   - A CR before the LF, from any Windows pipe. crlfCanon removes it, so a bare `$` fails on the
//     original and succeeds afterwards.
//   - Trailing horizontal whitespace, which the per-tool canonicalizers delete for the same reason.
//
// In every one of those cases an unaugmented anchor would produce a DIFFERENT result on the second
// pass than on the first, which is precisely the idempotence failure 00-ARCHITECTURE.md §5.6
// forbids — and it is not a hypothetical: it is what the captured `git diff --color` and
// `curl -v` corpus files do. Letting the anchors see past exactly the bytes other rules remove is
// what makes one pass reach the fixed point.
const (
	// The four escape shapes ansiRules strips, named so that everything which has to look PAST them
	// can be built from the same definitions rather than from a copy that drifts.
	//
	// escCSI is the ECMA-48 CSI shape: ESC '[' parameter bytes (0x30-0x3F) intermediate bytes
	// (0x20-0x2F) final byte (0x40-0x7E) — every colour, cursor-move and erase sequence.
	escCSI = `\x1b\[[0-?]*[ -/]*[@-~]`
	// escOSC is ESC ']' … terminated by BEL or by ST. The body excludes both terminators so the
	// match cannot run past the first one. Its inner alternation is non-capturing on purpose:
	// escAny is embedded in rules that select firstGroup, and a capture here would renumber theirs.
	escOSC = `\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`
	// escTwo is a two-byte escape: ESC followed by 0x40-0x5A or 0x5C-0x5F. The gap at 0x5B is '['.
	escTwo = `\x1b[\x40-\x5a\x5c-\x5f]`
	// escAny is anything ansiRules will delete, longest-first because Go's regexp is leftmost-first.
	// The bare ESC last is the residual rule, and it matters as much as the others: an escape that
	// is invisible to a look-past fragment blocks it in pass 1 and stops blocking it in pass 2,
	// once ansiCanon has deleted the escape — which is the whole failure mode these fragments
	// exist to prevent. Covering only escCSI leaves exactly that hole open for the other three.
	escAny = `(?:` + escCSI + `|` + escOSC + `|` + escTwo + `|\x1b)`
	// escAnyInline is escAny restricted to what can occur WITHIN one line: escOSC's unterminated
	// body is the only shape that could otherwise run across a '\n'.
	//
	// The three fragments below are line-scoped by definition, and every rule built from them is
	// anchored with (?m)^ and marked perLine, which scans one line at a time. A fragment that could
	// swallow a newline would make those two descriptions disagree — the whole-buffer scan would
	// find a match the per-line scan cannot — and TestPrefilterAgreesWithFullScan would fail.
	// Restricting it here rather than dropping perLine keeps the meaning and the speed: an OSC
	// sequence containing a raw newline is malformed anyway, and ansiRules still strips it, because
	// ansiRules is built from escOSC and not from this.
	escOSCInline = `\x1b\][^\x07\x1b\n]*(?:\x07|\x1b\\)`
	escAnyInline = `(?:` + escCSI + `|` + escOSCInline + `|` + escTwo + `|\x1b)`
	// lineLead is everything that may precede an anchored rule's first significant byte: an
	// overwritten prefix, escape sequences and indentation, in any combination.
	lineLead = `(?:[^\n]*\r)?(?:` + escAnyInline + `|[ \t])*`
	// lineTail is everything that may follow an anchored rule's last significant byte before the
	// line ends: escape sequences, trailing whitespace and carriage returns. The CR quantifier is
	// '*' rather than '?' because a doubly-converted stream really does carry "\r\r\n" — the
	// captured `curl -v` corpus file is full of them.
	lineTail = `(?:` + escAnyInline + `|[ \t])*\r*$`
	// sepRun is a run of whitespace that may have escape sequences mixed into it, which is what
	// separates the columns of a colourized test-runner line. The whitespace class is horizontal:
	// a column separator that swallowed a '\n' would let one line's "ok" pair with the next line's
	// duration, which is both wrong and the one thing that would stop the rules using it being
	// scanned per line.
	sepRun = `(?:` + escAnyInline + `|[^\S\n])+`
	// wordEdge is the LEADING word-boundary assertion every rule uses in place of a bare \b.
	//
	// A bare \b in front of a span is not stable under composition, for exactly the reason a bare ^
	// is not. An SGR sequence ends with a byte in 0x40-0x7E, which for every colour and cursor code
	// is a LETTER — a word byte. So in " \x1b[32mpid=41235" the position in front of "pid" has 'm'
	// on its left and 'p' on its right: no boundary, and `\bpid` correctly does not fire. But
	// ansiCanon deletes that escape in the same pass, so on the NEXT pass the space abuts "pid", the
	// boundary appears, and the rule fires. Pass 2 differs from pass 1 — the same idempotence
	// failure as the token-mediated one documented on reRule, reached by deletion instead of by a
	// token, and far easier to hit because every colour-capable tool produces it.
	//
	// The alternation fixes it: either a real word boundary is there, or the span is preceded by
	// escape sequences. Those escapes are consumed by the MATCH but never by the SPAN — every rule
	// using wordEdge puts its span in a capture group — so ansiCanon still owns those bytes, still
	// classifies its own Delta for them, and Options.Strip still gates them. The \b branch is
	// zero-width, so adjacent matches are unaffected.
	//
	// Only LEADING boundaries need this. An escape sequence BEGINS with ESC, a non-word byte, so a
	// trailing \b already succeeds in front of one and goes on succeeding once it is deleted.
	//
	// TestWordBoundary_EscapeDeletionNeverCreatesOne pins this half of the class.
	wordEdge = `(?:\b|(?:` + escAny + `)+)`
)

// THE WORD-BOUNDARY INVARIANT. Every rule table in this package obeys the following, and every
// rule added to one later must:
//
//	A rule whose replaced SPAN begins with a word character [0-9A-Za-z_] must require a word
//	boundary immediately before that span; a rule whose span ENDS with a word character must
//	require one immediately after it.
//
// Without it, one rule's replacement silently enables another rule on the NEXT pass, which is the
// idempotence failure 00-ARCHITECTURE.md §5.6 forbids. The counter-example the fuzzer found, and
// the reason this is written down:
//
//	input   "0000-00-00 00:00:00Zpid 0000"
//	pass 1  "<ts>pid 0000"      the ISO rule had no trailing \b, so it matched through the 'Z'
//	                            even though a word character followed; `\bpid` correctly did not
//	                            fire, because 'Z' and 'p' are both word bytes
//	pass 2  "<ts>pid <n>"       '>' is NOT a word byte, so `\bpid` now has the boundary it wanted
//
// A replacement token created a word boundary that did not exist in the input. Because composition
// is one pass over the ORIGINAL (see Match), ANY rule needing \b can be enabled this way by ANY
// other rule's token.
//
// The invariant makes that impossible. Every token is bracketed with '<' and '>', both non-word,
// so at the left edge of a token the boundary depends only on the preceding byte, and at the right
// edge only on the following byte. If the span's own first byte was a word character, the
// invariant forced the preceding byte to be non-word, so a boundary was ALREADY there; if the
// span's first byte was non-word, the token's '<' is non-word too and nothing changes. Symmetric
// at the other end. A rewrite can therefore only ever REMOVE word boundaries, never add one — and
// fewer boundaries means fewer pass-2 matches, which is exactly what idempotence needs. The same
// argument covers the deletion rules (empty token): joining the two surviving neighbours cannot
// create a boundary either, because the invariant already pinned whichever side held a word byte.
//
// Three ways a rule satisfies it, all in use below:
//
//   - An explicit \b, as in `\b0x[0-9a-fA-F]{6,16}\b`.
//   - A literal non-word byte in the pattern next to the span. `\bprocess (\d{2,7})\b` needs no \b
//     in front of the group: the pattern pins a space there. This is why the invariant is about
//     the SPAN's edges and not the whole match's — for a firstGroup or bothGroups rule the context
//     outside the group is exactly what supplies the guarantee.
//   - A greedy class that contains EVERY word byte, so the byte that stops it cannot be one.
//     `[\w.+-]+` and `[^&\s"'<>]*` both work this way; it is why the temp-path and session-value
//     classes are spelled with \w rather than an explicit A-Za-z0-9 run that omits '_'.
//
// Do NOT add \b where the span edge is already a non-word byte ('/tmp/…', '@1f2e3d4a', '$TMPDIR/').
// \b asserts that the two bytes across the position DIFFER in wordness, so next to a non-word edge
// it asserts the opposite of what is wanted and would reject exactly the matches that are safe.
//
// TestWordBoundary_TokensNeverCreateOne pins the class.
//
// reRule binds one compiled pattern to the spans of each of its matches that get replaced, the
// bytes that replace them, and the Class that gates them.
//
// Splitting "which span" out from "which pattern" is what lets a rule strip the VALUE out of a
// piece of structure while leaving the structure itself in the canonical text. `pid=41235` becomes
// `pid=<n>` and not `<n>`, because `pid=` is signal — a later reader of the canonical bytes still
// learns that a process id was reported there — while the digits are pure churn that would fork
// the dedup space once per process (Qompack.md §8.1, O2). Every rule below whose pattern needs
// context to be unambiguous is therefore written with that context OUTSIDE the capture group.
type reRule struct {
	// re is compiled once, at package initialization, by regexp.MustCompile. RE2 has neither
	// lookahead nor backreferences, so every rule here is expressible as a plain automaton; the
	// three places that genuinely need lookbehind (crlf, bash's progress collapse, grep's
	// path-prefix scan) are hand-written byte loops instead.
	re *regexp.Regexp
	// spans selects which capture groups of each match are replaced: wholeMatch, firstGroup or
	// bothGroups.
	spans []int
	// token is the replacement, shared across every Match this rule emits. applyMatches only ever
	// appends from it, so one backing array per rule is safe and keeps Matches allocation-free on
	// the hot path (Qompack.md §8.1: PostToolUse p99 < 15ms).
	token []byte
	// class gates the rule in acceptCandidates. It is never the zero Class: a zero Class is
	// silently dropped there, which would turn the rule into dead code.
	class Class
	// need lists literals of which the scanned buffer must contain AT LEAST ONE for this rule to
	// be able to match — a NECESSARY condition, never a sufficient one. See the prefilter note
	// below for why every rule that can carry one must.
	need []string
	// needAll lists literals the buffer must contain EVERY one of. It is the conjunction to need's
	// disjunction, for a rule that pins more than one literal in sequence.
	//
	// One rule needs it and it is worth recording why, because the alternative looked fine and was
	// not. `^={2,} .* in (\d+\.\d+s) ={2,}` — pytest's summary line — pins both "==" and " in ".
	// Filtering on "==" alone leaves it scanning every `=== RUN` line of Go test output; filtering
	// on " in " alone leaves it scanning an npm log, where " in " is everywhere and "==" is
	// nowhere. Each literal on its own therefore makes one real corpus file fast and the other
	// slow, and measuring only one of them would have looked like a win.
	needAll []string
	// needFold matches need case-insensitively, for a rule compiled with (?i).
	needFold bool
	// perLine scans each '\n'-delimited line separately instead of the whole buffer. Only a rule
	// anchored with (?m)^ may set it, and only because such a rule cannot match across a newline
	// in the first place.
	perLine bool
}

// THE PREFILTER. Every rule that can name a literal it cannot match without must name one, and
// every (?m)^-anchored rule must set perLine. Both exist for one reason: Go's regexp is a linear
// automaton with no literal-prefix fast path once a pattern starts with \b, (?i) or (?m)^, and it
// then runs at roughly 20 MB/s. Forty rules times 100 KB is 110 ms, against a 3 ms budget for the
// whole hot-path call (plans/V2-SP-04, "Performance budget"; Qompack.md §8.1 gives PostToolUse
// 15 ms p99 in total).
//
// bytes.Index over the same 100 KB costs 24 µs, so a rule that cannot match is rejected roughly
// 250 times faster than it is scanned. That is the entire trick, and it works because tool output
// is overwhelmingly specific: an npm log contains none of "GMT", "pid", "0x", "127.0.0.1",
// "goroutine ", "--- FAIL", "index " or "commit ", so 34 of the 40 rules below never run on it.
//
// perLine is the same argument for anchored rules, which have no usable whole-buffer literal:
// splitting on '\n' costs an IndexByte scan, and the literal is then tested against a 100-byte
// line instead of a 100 KB buffer, so a rule like `^ok\s+\S+\s+(\d+\.\d{1,3}s)` pays the regex
// only on the handful of lines that actually start a "ok " row.
//
// A WRONG need silently disables a rule, which is the one failure mode here that has no symptom.
// TestPrefilterAgreesWithFullScan and FuzzPrefilterAgreesWithFullScan close that off by running
// every table both ways over the corpus and over arbitrary bytes and requiring identical Matches;
// a need that is not implied by its pattern fails them.

// triggered reports whether buf can possibly contain a match for r, using only r.need and
// r.needAll.
func (r *reRule) triggered(buf []byte) bool {
	for _, lit := range r.needAll {
		if !r.hasLiteral(buf, lit) {
			return false
		}
	}
	if len(r.need) == 0 {
		return true
	}
	for _, lit := range r.need {
		if r.hasLiteral(buf, lit) {
			return true
		}
	}
	return false
}

// hasLiteral is bytes.Contains specialized for the two cases that matter on this path: a
// single-byte literal, where IndexByte beats the general search, and a rule compiled with (?i),
// where the haystack must not be folded into a fresh allocation.
func (r *reRule) hasLiteral(buf []byte, lit string) bool {
	switch {
	case r.needFold:
		return containsFold(buf, lit)
	case len(lit) == 1:
		return bytes.IndexByte(buf, lit[0]) >= 0
	default:
		return bytes.Contains(buf, []byte(lit))
	}
}

// containsFold is bytes.Contains for an ASCII needle matched case-insensitively, without
// allocating a folded copy of the haystack — the haystack is up to MaxInputBytes and this runs on
// the hot path, so the obvious bytes.Contains(bytes.ToLower(buf), lit) would cost more than the
// scan it is meant to avoid. lit must be ASCII lowercase.
// The two cases are searched in two INDEPENDENT passes rather than interleaved. Interleaving reads
// better but is quadratic: taking the nearer of IndexByte(rest, lo) and IndexByte(rest, up) rescans
// the whole tail for the case that did not win, once per candidate, and 'a' is common enough in
// real output to make that the dominant cost of the whole registry. Two monotone passes are O(n).
func containsFold(buf []byte, lit string) bool {
	if lit == "" {
		return true
	}
	lo := lit[0]
	for _, first := range [2]byte{lo, lo - ('a' - 'A')} {
		for off := 0; off+len(lit) <= len(buf); {
			i := bytes.IndexByte(buf[off:len(buf)-len(lit)+1], first)
			if i < 0 {
				break
			}
			off += i
			if equalFold(buf[off:off+len(lit)], lit) {
				return true
			}
			off++
		}
	}
	return false
}

// equalFold compares an ASCII-lowercase literal against buf, ignoring case. strings.EqualFold
// would do the same thing but walks both sides as UTF-8; every literal here is ASCII.
func equalFold(buf []byte, lit string) bool {
	for i := 0; i < len(lit); i++ {
		c := buf[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lit[i] {
			return false
		}
	}
	return true
}

// ruleMatches returns every span every rule in rules finds in in.
//
// It reports what the rules find with NO regard for Options.Strip, exactly as Matcher documents:
// gating on Strip happens once, centrally, in acceptCandidates. Filtering here instead would let a
// canonicalizer bypass a project's store.canonicalize.strip setting and would spread one decision
// across fourteen implementations.
//
// A capture group that did not participate in a match reports (-1, -1) and is skipped, as is a
// group that matched empty: a zero-length Match is an insertion, which acceptCandidates rejects
// anyway, so emitting one would only cost a slot in the sort.
func ruleMatches(in []byte, rules []reRule) []Match {
	var dst []Match
	for i := range rules {
		r := &rules[i]
		if r.perLine {
			forEachLine(in, func(start, end int) {
				if line := in[start:end]; r.triggered(line) {
					dst = appendRuleSpans(dst, r, line, start)
				}
			})
			continue
		}
		if r.triggered(in) {
			dst = appendRuleSpans(dst, r, in, 0)
		}
	}
	return dst
}

// appendRuleSpans runs one rule over buf and appends the spans it selects, shifted by base so a
// per-line rule reports offsets in the whole input's coordinates.
func appendRuleSpans(dst []Match, r *reRule, buf []byte, base int) []Match {
	for _, loc := range r.re.FindAllSubmatchIndex(buf, -1) {
		for _, g := range r.spans {
			lo, hi := loc[2*g], loc[2*g+1]
			if lo < 0 || hi <= lo {
				continue
			}
			dst = append(dst, Match{Offset: base + lo, Len: hi - lo, Token: r.token, Class: r.class})
		}
	}
	return dst
}

// The two bytes that terminate an escape sequence, named because escLen tests them by value.
const (
	// escByte is ESC, which every shape in escAny begins with.
	escByte = 0x1b
	// belByte is BEL, one of the two terminators an OSC sequence may end with.
	belByte = 0x07
)

// escLen returns the length of the escape sequence beginning at in[i], or 0 if none does.
//
// It is escAny expressed as a byte loop, and it must stay that way: the alternation is tried in
// escAny's order — CSI, OSC, two-byte, then the bare ESC residual — because that order is what
// decides how many bytes a caller skips. Trying the longest shape first is also what makes greedy
// tokenization safe here: every longer shape's second byte ('[' for CSI, ']' for OSC, 0x40-0x5F for
// the two-byte form) is itself outside the tail alphabet, so whenever a longer shape matches and
// the rest of the line then fails to tokenize, falling back to the bare ESC would have failed at
// that same second byte. The two can therefore never disagree about where a tail begins.
func escLen(in []byte, i int) int {
	if i >= len(in) || in[i] != escByte {
		return 0
	}
	if n := csiLen(in, i); n > 0 {
		return n
	}
	if n := oscLen(in, i); n > 0 {
		return n
	}
	if i+1 < len(in) {
		// escTwo: ESC followed by 0x40-0x5A or 0x5C-0x5F. The gap at 0x5B is '[', which belongs to
		// the CSI shape already tried above.
		if c := in[i+1]; (c >= 0x40 && c <= 0x5a) || (c >= 0x5c && c <= 0x5f) {
			return 2
		}
	}
	return 1
}

// csiLen implements escCSI: ESC '[' then parameter bytes 0x30-0x3F, then intermediate bytes
// 0x20-0x2F, then one final byte 0x40-0x7E. A sequence with no final byte is not a CSI sequence.
func csiLen(in []byte, i int) int {
	if i+1 >= len(in) || in[i+1] != '[' {
		return 0
	}
	j := i + 2
	for ; j < len(in) && in[j] >= 0x30 && in[j] <= 0x3f; j++ {
	}
	for ; j < len(in) && in[j] >= 0x20 && in[j] <= 0x2f; j++ {
	}
	if j < len(in) && in[j] >= 0x40 && in[j] <= 0x7e {
		return j + 1 - i
	}
	return 0
}

// oscLen implements escOSC: ESC ']' then a body containing neither terminator, then BEL or ST
// (ESC '\'). An unterminated body is not an OSC sequence.
func oscLen(in []byte, i int) int {
	if i+1 >= len(in) || in[i+1] != ']' {
		return 0
	}
	for j := i + 2; j < len(in); j++ {
		switch in[j] {
		case belByte:
			return j + 1 - i
		case escByte:
			if j+1 < len(in) && in[j+1] == '\\' {
				return j + 2 - i
			}
			return 0
		}
	}
	return 0
}

// forEachLine calls fn with the [start, end) byte range of every '\n'-delimited line in in, with
// the terminator itself excluded. A trailing '\n' does not produce a final empty line.
//
// The per-line rules below (grep's path prefix, glob's separators) are line-scoped because tool
// output is line-oriented: a rule that scanned the whole buffer would let one line's structure
// leak into the next one's interpretation, which is precisely the bug grep's "only inside the
// prefix" restriction exists to prevent.
func forEachLine(in []byte, fn func(start, end int)) {
	for start := 0; start < len(in); {
		n := bytes.IndexByte(in[start:], '\n')
		if n < 0 {
			fn(start, len(in))
			return
		}
		fn(start, start+n)
		start += n + 1
	}
}

// ---------------------------------------------------------------------------------------------
// 1. crlf
// ---------------------------------------------------------------------------------------------

// crlfCanon rewrites CRLF line endings to LF (00-ARCHITECTURE.md §4).
//
// It is the one canonicalizer that is never optional. §4 makes CRLF→LF a PRECONDITION of
// cross-platform dedup: without it a Windows read and a Linux read of the same file produce
// disjoint chunk sets and the content-addressed store holds two copies of everything. classes.go
// therefore puts ClassCRLF in alwaysOn, and Appendix C's store.canonicalize.strip enum has no
// "crlf" value to switch it off with.
type crlfCanon struct{}

// newCRLF returns the crlf canonicalizer, first in §5.6's registration order.
func newCRLF() crlfCanon { return crlfCanon{} }

// Name returns "crlf".
func (crlfCanon) Name() string { return nameCRLF }

// Applies is always true: line endings are not a property of the tool that produced the bytes.
func (crlfCanon) Applies(_, _ string) bool { return true }

// Matches reports every run of one or more '\r' immediately followed by '\n', replacing the whole
// run with a single '\n'.
//
// The run, rather than the exact two-byte "\r\n" pair, is what makes this rule idempotent.
// Replacing only the pair in "\r\r\n" yields "\r\n" — a fresh CRLF that a second pass would
// rewrite again, violating §5.6's idempotence requirement. Consuming the whole run leaves an
// output in which no '\n' is preceded by a '\r' at all, so a second pass has nothing to find.
//
// A LONE '\r' with no '\n' after it is deliberately left alone: it is not a line ending but a
// terminal cursor return, and collapsing the progress bar it overwrote is bashCanon's job (and is
// classified as ClassANSI there, because that is what it is).
func (crlfCanon) Matches(in []byte, _ Options) []Match {
	var out []Match
	for i := 0; i < len(in); {
		n := bytes.IndexByte(in[i:], '\n')
		if n < 0 {
			return out
		}
		end := i + n
		if end > 0 && in[end-1] == '\r' {
			start := end - 1
			for start > 0 && in[start-1] == '\r' {
				start--
			}
			out = append(out, Match{Offset: start, Len: end + 1 - start, Token: lf, Class: ClassCRLF})
		}
		i = end + 1
	}
	return out
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c crlfCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 2. ansi
// ---------------------------------------------------------------------------------------------

// ansiRules strips terminal escape sequences.
//
// Colour and cursor control is the highest-volume, lowest-signal content in tool output: an
// npm or cargo run can spend a third of its bytes on SGR sequences that carry no information a
// later retrieval could ever want (Qompack.md §8.1, O2). All four rules replace with EMPTY rather
// than a token, because unlike a timestamp an escape sequence has no value worth remembering.
//
// The fourth rule is the idempotence anchor and is not in 00-ARCHITECTURE.md's sketch of this
// canonicalizer. Deleting spans can JOIN their neighbours: "\x1b[0" + "\x1b[0m" + "m" contains no
// CSI sequence at offset 0, so the inner one is deleted first and the remainder splices into a
// fresh "\x1b[0m" that a second pass would strip — non-idempotent. Removing every ESC byte that no
// longer-matching rule claimed leaves an output containing NO ESC at all, and since all four
// patterns begin with ESC, a second pass provably finds nothing. Deleting a stray ESC is also the
// right answer on its own terms: a bare ESC in tool output is terminal control by definition.
// The four patterns are the escCSI/escOSC/escTwo/bare-ESC constants above, so this table and every
// look-past fragment (lineLead, lineTail, sepRun, wordEdge, trailingWSRegion) are provably talking
// about the same set of bytes. When they drift apart, a shape one of them strips and the other
// cannot see past becomes a two-pass convergence.
var ansiRules = []reRule{
	// CSI — every colour, cursor-move and erase sequence.
	{re: regexp.MustCompile(escCSI), spans: wholeMatch, token: nil, class: ClassANSI, need: []string{"\x1b"}},
	// OSC; window-title sets arrive in this form.
	{re: regexp.MustCompile(escOSC), spans: wholeMatch, token: nil, class: ClassANSI, need: []string{"\x1b"}},
	// Two-byte escapes. The gap at 0x5B is '[' — CSI, handled above and always longer, so the
	// sort's longest-wins rule prefers it at a shared offset. 0x5D (']') IS included, which is what
	// truncates an UNTERMINATED OSC to its two-byte introducer instead of leaving it in the output.
	{re: regexp.MustCompile(escTwo), spans: wholeMatch, token: nil, class: ClassANSI, need: []string{"\x1b"}},
	// Residual ESC. See the idempotence argument on this var.
	{re: regexp.MustCompile(`\x1b`), spans: wholeMatch, token: nil, class: ClassANSI, need: []string{"\x1b"}},
}

// ansiCanon strips ANSI escape sequences (00-ARCHITECTURE.md §5.6).
type ansiCanon struct{}

// newANSI returns the ansi canonicalizer, second in §5.6's registration order.
func newANSI() ansiCanon { return ansiCanon{} }

// Name returns "ansi".
func (ansiCanon) Name() string { return nameANSI }

// Applies is always true: any tool writing to a TTY-shaped stream can emit escapes.
func (ansiCanon) Applies(_, _ string) bool { return true }

// Matches reports every escape sequence ansiRules finds.
func (ansiCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, ansiRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c ansiCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 3. timestamps
// ---------------------------------------------------------------------------------------------

// timestampRules replaces wall-clock timestamps with tokenTimestamp.
//
// A timestamp is the canonical volatile value: it differs on every single run of an otherwise
// byte-identical command, so leaving it in guarantees that no two runs of the same test suite ever
// share a chunk. Stripping it is what turns "the same log, an hour later" into a store hit
// (Qompack.md §8.1, O2).
//
// The rules overlap by construction — an ISO-8601 date-time CONTAINS a bare clock — and that is
// safe without any ordering care here: sortCandidates puts the earlier offset first and
// acceptCandidates rejects anything starting inside an accepted span, so the ISO match at the
// start of the timestamp always wins over the clock match nine bytes into it.
//
// Every span these rules replace is at least 8 bytes (the bare clock) against a 4-byte token, so
// none of them relies on the non-growing guard.
//
// THREE OF THE FIVE ARE NOT IN THIS TABLE, and everything above is still about all five: the
// ISO-8601 date-time, the Unix epoch and the bare wall clock are matched by numeric.go's byte
// scanner, and timestampsCanon.Matches puts the two halves back together. The split is the
// prefilter argument above taken to its conclusion. A rule keyed on "GMT" or on a month name is
// rejected by a bytes.Index and never costs a scan, so it belongs here; a rule keyed on a DIGIT can
// carry no prefilter at all and pays the full 20 MB/s automaton on every buffer, and the three that
// moved cost 20 ms per 100 KB between them. numeric.go reproduces their exact semantics —
// leftmost-first, greedy, backtracking and all — and numeric_test.go holds the retired patterns as
// the oracle it is checked against.
var timestampRules = []reRule{
	// RFC-1123 (HTTP Date headers), which webfetch output is full of. Both edges are letters, so
	// both are anchored.
	{re: regexp.MustCompile(wordEdge + `((?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} (?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{4} \d{2}:\d{2}:\d{2} GMT)\b`), spans: firstGroup, token: []byte(tokenTimestamp), class: ClassTimestamps, need: []string{"GMT"}},
	// Syslog / BSD style: "Jan  2 15:04:05", with the day space-padded to two columns. Begins with a
	// letter and ends with a digit, so likewise.
	{re: regexp.MustCompile(wordEdge + `((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [ \d]\d \d{2}:\d{2}:\d{2})\b`), spans: firstGroup, token: []byte(tokenTimestamp), class: ClassTimestamps, need: monthNames},
}

// timestampsCanon strips wall-clock timestamps (00-ARCHITECTURE.md §5.6).
type timestampsCanon struct{}

// newTimestamps returns the timestamps canonicalizer, third in §5.6's registration order.
func newTimestamps() timestampsCanon { return timestampsCanon{} }

// Name returns "timestamps".
func (timestampsCanon) Name() string { return nameTimestamps }

// Applies is always true: every tool can print a clock.
func (timestampsCanon) Applies(_, _ string) bool { return true }

// Matches reports every timestamp this canonicalizer's two halves find: the two literal-anchored
// rules still in timestampRules, plus the three digit-anchored shapes numeric.go scans for. The
// two halves may report overlapping spans, and so may either half on its own — an ISO-8601
// date-time CONTAINS a bare clock — which needs no care here: acceptCandidates resolves overlaps
// once, centrally.
func (timestampsCanon) Matches(in []byte, _ Options) []Match {
	return numericMatches(ruleMatches(in, timestampRules), in, ClassTimestamps)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c timestampsCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 4. durations
// ---------------------------------------------------------------------------------------------

// durationRules replaces elapsed-time values with tokenDuration.
//
// A duration is a timestamp's twin: "took 1.203s" and "took 1.198s" describe the same run and
// differ in every byte that matters to a rolling hash. Unlike a timestamp it is genuinely small,
// which is why the token is three bytes rather than four.
//
// The two composite rules come first only for readability; correctness does not depend on their
// order, because a composite duration and the simple rule cannot both match at the same offset —
// the simple rule's trailing \b fails against the digit that follows "1m" in "1m30.5s".
//
// GUARD RELIANCE. The simple rule can match a span as short as two bytes ("5s", "3m"), which is
// shorter than the three-byte token. acceptCandidates drops those rather than growing the input,
// so a two-byte duration survives canonicalization verbatim. That is deliberate — §5.6's
// no-growth property is enforced structurally, once, instead of being re-derived in fourteen
// implementations — and TestDurations_Table names it directly.
//
// ALL FOUR OF THEM ARE IN numeric.go, and everything above is still about all four. Every duration
// shape begins with a digit, so not one of them could ever carry the `need` prefilter documented
// above reRule, and the four together cost 12 ms per 100 KB of tool output — four times the budget
// for the whole canonicalization call. They are the same four patterns, scanned by hand: Go's
// time.Duration composites 1h2m3.5s and 2m3.5s, a single magnitude with an optional space before
// its unit, and the "…in 250ms" phrase whose span is the value alone. numeric_test.go keeps the
// retired patterns as the oracle the scanner is checked against, so the two can never quietly
// diverge.
//
// The table itself stays, empty, because it is what says this canonicalizer has no REGEX rules
// left rather than that someone forgot to register any — and because the prefilter properties are
// asserted over every table in the package, which is a claim worth keeping true vacuously.
var durationRules = []reRule{}

// durationsCanon strips elapsed-time values (00-ARCHITECTURE.md §5.6).
type durationsCanon struct{}

// newDurations returns the durations canonicalizer, fourth in §5.6's registration order.
func newDurations() durationsCanon { return durationsCanon{} }

// Name returns "durations".
func (durationsCanon) Name() string { return nameDurations }

// Applies is always true.
func (durationsCanon) Applies(_, _ string) bool { return true }

// Matches reports every duration numeric.go's scanner finds. durationRules is scanned too, and is
// empty, so the call costs one loop that runs zero times — kept because it is what makes the empty
// table a statement rather than an oversight.
func (durationsCanon) Matches(in []byte, _ Options) []Match {
	return numericMatches(ruleMatches(in, durationRules), in, ClassDurations)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c durationsCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 5. pids
// ---------------------------------------------------------------------------------------------

// pidRules replaces process ids with tokenNumber.
//
// Only the DIGITS are replaced, never the "pid=" or "process " that introduces them: the canonical
// text should still say that a process id was reported, because that is signal, while the number
// itself changes on every invocation and is not.
//
// GUARD RELIANCE. Every capture here is \d{2,7}, so a two-digit pid produces a two-byte span
// against a three-byte token and acceptCandidates drops it — a low-numbered pid survives
// canonicalization verbatim. §5.6's no-growth property is enforced there, structurally, rather
// than by giving this rule a shorter token that would read worse everywhere else.
// TestPIDs_Table names this reliance directly.
var pidRules = []reRule{
	// "pid=41235", "PID: 990", "pid 1234" — the three spellings that cover essentially every
	// runtime, daemon and process-manager log line.
	{re: regexp.MustCompile(`(?i)` + wordEdge + `pid[=: ]\s*(\d{2,7})\b`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"pid=", "pid:", "pid "}, needFold: true},
	// A bracketed pid at the very start of a line, which is syslog's and systemd's framing. It is
	// line-anchored on purpose: an unanchored "[12345]" is far more likely to be an array index or
	// a log sequence number than a pid.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `\[(\d{2,7})\]`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"["}, perLine: true},
	// "process 1234", as printed by kill/taskkill-shaped diagnostics.
	{re: regexp.MustCompile(wordEdge + `process (\d{2,7})\b`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"process "}},
}

// pidsCanon strips process ids (00-ARCHITECTURE.md §5.6).
type pidsCanon struct{}

// newPIDs returns the pids canonicalizer, fifth in §5.6's registration order.
func newPIDs() pidsCanon { return pidsCanon{} }

// Name returns "pids".
func (pidsCanon) Name() string { return namePIDs }

// Applies is always true.
func (pidsCanon) Applies(_, _ string) bool { return true }

// Matches reports every process id pidRules finds.
func (pidsCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, pidRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c pidsCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 6. addresses
// ---------------------------------------------------------------------------------------------

// addressRules replaces memory addresses, opaque hex identities and loopback ports.
//
// A panic trace or a heap dump is otherwise perfectly stable content wrapped around a handful of
// pointer values that are different in every process; the same is true of the ephemeral port a
// test server binds. Both are pure churn for the store and neither carries information a later
// retrieval can act on.
//
// The loopback rule is written with the word boundary INSIDE each alternative rather than in front
// of the group. `\b` requires a word character on one side of the position, and "[::1]" starts
// with '[' — a non-word byte — so a leading `\b(?:…|\[::1\])` can never match "[::1]:8080" when it
// is preceded by a space. Distributing the boundary is the only RE2-expressible fix (there is no
// lookbehind to say "not preceded by a word byte").
//
// No rule here relies on the non-growing guard: the shortest hex address is 8 bytes and the
// shortest '@' identity 7, both against a 6-byte token, and the shortest port capture is 4 bytes
// against a 3-byte one.
var addressRules = []reRule{
	// A hex pointer with the 0x prefix. The 6-to-16 digit floor keeps small hex constants
	// ("0xff", "0x1F4") out: those are usually part of the message, not an address.
	{re: regexp.MustCompile(wordEdge + `(0x[0-9a-fA-F]{6,16})\b`), spans: firstGroup, token: []byte(tokenAddress), class: ClassAddresses, need: []string{"0x"}},
	// The "@1a2b3c" identity suffix Java, Ruby and several JS runtimes print for an object. '@' is
	// non-word, so this one takes no leading assertion at all.
	{re: regexp.MustCompile(`@[0-9a-f]{6,8}\b`), spans: wholeMatch, token: []byte(tokenAddress), class: ClassAddresses, need: []string{"@"}},
	// The port of a loopback address. The HOST is kept — "listening on localhost:<p>" is still a
	// meaningful line — and only the ephemeral port is replaced. The span is the port group, whose
	// left edge is pinned by the ':'; the leading assertion is distributed into the three
	// alternatives that begin with a word byte, because "[::1]" begins with '[' and an assertion in
	// front of it would demand the opposite of what is wanted.
	{re: regexp.MustCompile(`(?:` + wordEdge + `127\.0\.0\.1|` + wordEdge + `localhost|` + wordEdge + `0\.0\.0\.0|\[::1\]):(\d{4,5})\b`), spans: firstGroup, token: []byte(tokenPort), class: ClassAddresses, need: []string{"127.0.0.1", "localhost", "0.0.0.0", "[::1]"}},
}

// addressesCanon strips memory addresses and loopback ports (00-ARCHITECTURE.md §5.6).
type addressesCanon struct{}

// newAddresses returns the addresses canonicalizer, sixth in §5.6's registration order.
func newAddresses() addressesCanon { return addressesCanon{} }

// Name returns "addresses".
func (addressesCanon) Name() string { return nameAddresses }

// Applies is always true.
func (addressesCanon) Applies(_, _ string) bool { return true }

// Matches reports every address addressRules finds.
func (addressesCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, addressRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c addressesCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 7. tmpPaths
// ---------------------------------------------------------------------------------------------

// tmpPathRules replaces temp-directory paths with tokenTmpPath.
//
// A temp path is volatile in the most expensive way: the random component sits in the MIDDLE of
// otherwise identical text, so every run of a test that touches t.TempDir() perturbs a whole
// chunk rather than a single token. Replacing the entire path — not just the random component —
// is what makes two runs of the same suite dedup against each other.
//
// All five layouts are covered because Qompack is cross-platform by construction
// (00-ARCHITECTURE.md §4): a Linux CI run and a Windows developer run of the same suite must
// canonicalize to the same bytes, which they cannot do if only one platform's temp root is
// recognized. The Windows layout is spelled twice — once with backslashes, once with forward
// slashes — because Go's os.TempDir, the shell and most tooling disagree about which they print,
// and the alternation is cheaper than depending on glob/grep having normalized separators first.
//
// The greedy tails are what make this idempotent even though the Windows and $TMPDIR patterns
// admit '<' and '>' in their tails: a token can only appear in the output where a whole path was
// replaced, and the prefix that would be needed to re-match it ("…\Temp\", "$TMPDIR/") was
// consumed with it. The shortest span any rule here produces is 6 bytes against a 5-byte token, so
// none of them relies on the non-growing guard.
// The component classes are spelled with \w, not with an explicit A-Za-z0-9 run, and that is the
// word-boundary invariant rather than a convenience. A greedy class containing EVERY word byte can
// only be stopped by a non-word byte, which is what guarantees the boundary after a span that ends
// in a letter or digit. Written as [A-Za-z0-9._+-] the class omits '_', so "/tmp/foo_bar" matched
// only "/tmp/foo" and left a word byte on each side of the join — and the token's '>' would then
// have created the boundary the invariant forbids. A \b at that edge is NOT the fix: it would
// reject the whole match rather than extend it, leaving the temp path unstripped.
var tmpPathRules = []reRule{
	// POSIX /tmp, as one or more slash-separated components.
	{re: regexp.MustCompile(`/tmp/[\w.+-]+(/[\w.+-]+)*`), spans: wholeMatch, token: []byte(tokenTmpPath), class: ClassTmpPaths, need: []string{"/tmp/"}},
	// macOS per-user temp roots, which live under /var/folders/<2>/<long-opaque-id>/T/.
	{re: regexp.MustCompile(`/var/folders/[\w.+/-]+`), spans: wholeMatch, token: []byte(tokenTmpPath), class: ClassTmpPaths, need: []string{"/var/folders/"}},
	// Windows %LOCALAPPDATA%\Temp, backslash form. The username is part of the match because it is
	// per-machine churn in exactly the same way the random leaf is. The leading wordEdge is the
	// invariant: the span starts at the drive letter, which is a word byte. The tail needs no
	// assertion — its class is a complement that admits every word byte, so whatever stops it is
	// non-word.
	{re: regexp.MustCompile(`(?i)` + wordEdge + `([A-Za-z]:\\Users\\[^\\]+\\AppData\\Local\\Temp\\[^\s"']*)`), spans: firstGroup, token: []byte(tokenTmpPath), class: ClassTmpPaths, need: []string{`\appdata\`}, needFold: true, perLine: true},
	// Windows %LOCALAPPDATA%\Temp, forward-slash form (Git Bash, MSYS, Go's own path printing).
	{re: regexp.MustCompile(`(?i)` + wordEdge + `([A-Za-z]:/Users/[^/]+/AppData/Local/Temp/[^\s"']*)`), spans: firstGroup, token: []byte(tokenTmpPath), class: ClassTmpPaths, need: []string{"/appdata/"}, needFold: true, perLine: true},
	// An unexpanded $TMPDIR reference, which shell transcripts carry verbatim. '$' is non-word, so
	// no leading \b — one there would assert the opposite of what is wanted.
	{re: regexp.MustCompile(`\$TMPDIR/[^\s"']*`), spans: wholeMatch, token: []byte(tokenTmpPath), class: ClassTmpPaths, need: []string{"$TMPDIR/"}},
}

// tmpPathsCanon strips temp-directory paths (00-ARCHITECTURE.md §5.6).
type tmpPathsCanon struct{}

// newTmpPaths returns the tmpPaths canonicalizer, seventh in §5.6's registration order and the
// last of the generic ones.
func newTmpPaths() tmpPathsCanon { return tmpPathsCanon{} }

// Name returns "tmpPaths", camelCase, matching Appendix C's store.canonicalize.strip spelling
// exactly — those strings are a wire format, not identifiers.
func (tmpPathsCanon) Name() string { return nameTmpPaths }

// Applies is always true.
func (tmpPathsCanon) Applies(_, _ string) bool { return true }

// Matches reports every temp path tmpPathRules finds.
func (tmpPathsCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, tmpPathRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c tmpPathsCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}
