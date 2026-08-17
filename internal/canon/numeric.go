package canon

// WHY THE SEVEN NUMERIC SHAPES ARE A BYTE SCANNER AND NOT SEVEN REGEXES.
//
// Seven of the nine rules the timestamps and durations canonicalizers are built from begin with
// wordEdge and then a digit. Neither half gives Go's regexp anything to skip on — wordEdge is an
// alternation of an assertion with an escape class, and a digit is not a literal — so each of the
// seven simulates its automaton over every byte of the buffer at roughly 20 MB/s. Nor can any of
// them carry the `need` prefilter documented above reRule: the literal they would have to name is
// "a digit", and tool output without one is not the input a coding session produces. Measured over
// 100 KB of captured `npm install` output the seven cost 32.4 ms between them, against the 3 ms
// budget plans/V2-SP-04 gives the WHOLE canonicalization call and the 15 ms Qompack.md §8.1 gives
// the entire PostToolUse hook. The two passes this file replaces them with — one per class —
// cost about 450 µs together over the same bytes, which is roughly seventy times less.
//
// The two rules that did NOT move — timestampRules' RFC-1123 "GMT" date and its syslog month-name
// form — stay regexes precisely because they begin with a letter run that IS a literal, so `need`
// rejects them with a bytes.Index and they never pay for a scan at all.
//
// WHAT MUST BE REPRODUCED, EXACTLY. This is a rewrite of a matcher, not a redesign of one: the
// multiset of Match values below has to be the one the seven patterns produced, because
// testdata/golden/canon/** froze the output of those patterns and TestGoldenCorpus_AllFiles will
// not accept a byte of drift. numeric_test.go therefore keeps all seven regexes as its oracle and
// compares against them over the corpus, over generated input and under go-fuzz. Four properties
// of Go's regexp are load bearing here, and each one is called out again at the code that
// implements it:
//
//   - LEFTMOST is measured from the start of the WHOLE match, not of the capture group. Under
//     wordEdge's escape branch the whole match starts at the first ESC of the run, which can be
//     EARLIER than a capture that another position would have produced — so in
//     "\x1b[12:34:56~01:02:03" the clock rule reports "01:02:03" and never sees the "12:34:56"
//     sitting inside the CSI sequence's parameter bytes. The scan below is therefore driven by
//     candidate MATCH starts and not by candidate span starts.
//   - FIRST, not longest, among alternatives, with backtracking. `(?:\.\d{1,9})?` tries nine
//     fraction digits before eight, and the trailing \b is what rejects them; `(?:ns|µs|us|ms|s|
//     m|h)` tries "ns" before "s" because leftmost-first would otherwise stop "12ms" at "12m".
//   - GREEDY repetition inside wordEdge itself. `(?:escAny)+` prefers one more escape sequence to
//     stopping, and escAny prefers CSI, then OSC, then the two-byte form, then the bare ESC — so
//     the run is a depth-first walk in that order and NOT simply "the longest run".
//   - EACH RULE SCANS INDEPENDENTLY. FindAllSubmatchIndex resumes at the end of the match it just
//     returned, and it does so per pattern, so seven separate resume cursors are needed. Spans
//     from different rules overlap freely; acceptCandidates resolves that centrally, which is why
//     this file never suppresses one rule's match on account of another's.
//
// Options.Strip is not consulted here for the same reason no other Matcher consults it: gating is
// acceptCandidates' job and happens once.

// The seven rules, named. Each index selects a resume cursor in numScan.next and one bit in
// numScan.dead, so numRuleCount must stay at or below eight.
const (
	// numISO is `\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?`.
	numISO = iota
	// numEpoch is `\d{10,13}`.
	numEpoch
	// numClock is `\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?`.
	numClock
	// numHourMinSec is `\d+h\d+m\d+(?:\.\d+)?s`.
	numHourMinSec
	// numMinSec is `\d+m\d+(?:\.\d+)?s`.
	numMinSec
	// numMagnitude is `\d+(?:\.\d+)?\s?(?:ns|µs|us|ms|s|m|h)`.
	numMagnitude
	// numInPhrase is `in (\d+(?:\.\d+)?\s?(?:ms|s))`, the only one whose SPAN is not the whole
	// body: "in " is context that stays in the canonical text.
	numInPhrase
	// numRuleCount sizes numScan.next.
	numRuleCount
)

// numInPhraseLead is the length of the "in " numInPhrase matches but does not replace.
const numInPhraseLead = 3

// The rules each canonicalizer owns, in the order the two retired tables listed them. Order is
// immaterial to the result — sortCandidates re-orders everything — and is kept only so a reader
// can line this file up against the tables it replaced.
var (
	numTimestampIDs = []int{numISO, numEpoch, numClock}
	numDurationIDs  = []int{numHourMinSec, numMinSec, numMagnitude, numInPhrase}
)

// The two replacement tokens, hoisted to package level for the same reason reRule.token is: a
// Match only ever has its Token appended FROM, so one backing array per token serves every span
// and the hot path stays free of per-candidate allocation.
var (
	numTokenTimestamp = []byte(tokenTimestamp)
	numTokenDuration  = []byte(tokenDuration)
)

// The two bytes of 'µ'. The unit alternation spells the micro prefix as a literal rune, which the
// compiled pattern matches as its UTF-8 encoding — so comparing these two bytes asks exactly the
// same question, and a lone 0xB5 (invalid UTF-8, decoding to RuneError) matches neither.
const (
	numMicroLead = 0xc2
	numMicroTail = 0xb5
)

// numInByte is the first byte of the "in " only numInPhrase looks for. It is the one non-digit a
// body can begin with, and it is a common letter, which is why the scan below only treats it as a
// candidate for the class that has that rule.
const numInByte = 'i'

// numericMatches appends every timestamp- or duration-shaped span in in to dst, whichever of the
// two classes is asked for, and returns the extended slice.
//
// One forward pass serves every rule of the class, and the per-byte test that drives it is two
// comparisons on the byte itself rather than a table lookup: everything above '9' is skipped
// unless it is the 'i' of "in ", everything below '0' unless it is an ESC. That leaves the loop
// with no memory reference at all for the letters and spaces that are most of any log, which is
// what makes the whole scan cost a couple of nanoseconds a byte instead of the three hundred the
// seven automata did.
func numericMatches(dst []Match, in []byte, class Class) []Match {
	var frames [numEscInlineDepth]escFrame
	sc := numScan{
		in: in, ids: numDurationIDs, token: numTokenDuration, class: class,
		phrase: true, stack: frames[:0], altAt: -1,
	}
	if class == ClassTimestamps {
		sc.ids, sc.token, sc.phrase = numTimestampIDs, numTokenTimestamp, false
	}

	for s := 0; s < len(in); s++ {
		c := in[s]
		switch {
		case c > '9':
			// Every letter, and every punctuation byte above '9'. Only the phrase rule can
			// begin here, and only for the class that owns it.
			if c != numInByte || !sc.phrase {
				continue
			}
		case c < '0':
			// wordEdge's escape branch is the only thing that starts down here.
			if c != escByte {
				continue
			}
			dst = sc.atEscape(dst, s)
			continue
		}

		// A digit or the 'i', and both are word bytes — so wordEdge's \b branch reduces to "the
		// byte before it is not one", which at offset 0 is vacuously true.
		if s == 0 || !numIsWordByte(in[s-1]) {
			dst = sc.atWordBoundary(dst, s)
		}
		if c <= '9' {
			// Every later position of this digit run is preceded by a digit, which is a word
			// byte, so \b cannot hold at any of them; and no digit is an ESC, so the escape
			// branch cannot start at one either. Skip the rest of the run outright.
			s += numDigitRun(in, s) - 1
		}
	}
	return dst
}

// numScan is the state one numericMatches call carries across its single pass.
type numScan struct {
	// in is the buffer being scanned, in Match's coordinate system.
	in []byte
	// ids are the rules of the class being scanned.
	ids []int
	// phrase is true when those rules include numInPhrase, which is the only one whose body can
	// begin with something other than a digit.
	phrase bool
	// token and class are stamped onto every Match this scan emits.
	token []byte
	class Class
	// next[id] is the offset before which rule id may no longer start a match: exactly the point
	// FindAllSubmatchIndex resumes its own scan at, which is the END of the match it last
	// returned. Each rule keeps its own, because each pattern is scanned over the buffer alone.
	next [numRuleCount]int
	// stack is escFind's unwind stack, reused across every node of every rule so a colourized
	// buffer pays for it once — and, backed by an array in numericMatches' own frame, usually
	// not at all.
	stack []escFrame
	// altAt, altEnds and altN memoize the escape alternatives of the last node expanded, and
	// altAt is -1 when there is none.
	//
	// One entry is enough because of how the node is asked about: atEscape puts the same ESC to
	// every rule of the class in turn, and escFind re-derives a node's alternatives every time it
	// resumes one, so the same three or four bytes get parsed three or four times over — and a
	// CSI or an OSC parse is a scan of the sequence, not a lookup. On the ANSI-heavy corpus file
	// that repetition was a tenth of the whole pass.
	altAt   int
	altEnds [numEscAlts]int
	altN    int
	// dead and steps are the escape-run walk's overflow protection; see escFind.
	dead  []uint8
	steps int
}

// atWordBoundary tries every rule still allowed to start at s, taking wordEdge's \b branch — the
// branch the alternation lists first, and therefore the one preferred when both could apply.
func (sc *numScan) atWordBoundary(dst []Match, s int) []Match {
	for _, id := range sc.ids {
		if sc.next[id] > s {
			continue
		}
		if e := sc.body(id, s); e >= 0 {
			dst = sc.emit(dst, id, s, e)
		}
	}
	return dst
}

// atEscape tries every rule still allowed to start at s, taking wordEdge's escape-run branch. The
// run is consumed by the MATCH and never by the SPAN, so the Match this can produce begins after
// the escapes — which is what leaves those bytes to ansiCanon, exactly as the capture group did.
func (sc *numScan) atEscape(dst []Match, s int) []Match {
	for _, id := range sc.ids {
		if sc.next[id] > s {
			continue
		}
		if q, e := sc.escFind(id, s); e >= 0 {
			dst = sc.emit(dst, id, q, e)
		}
	}
	return dst
}

// emit records rule id's match, whose body runs from q to e, and advances that rule's cursor.
func (sc *numScan) emit(dst []Match, id, q, e int) []Match {
	sc.next[id] = e
	span := q
	if id == numInPhrase {
		// Only the value is replaced. "in " is kept because it is the phrase that makes the
		// canonical line still readable, which is why that rule captured a group in the first
		// place.
		span += numInPhraseLead
	}
	return append(dst, Match{Offset: span, Len: e - span, Token: sc.token, Class: sc.class})
}

// body runs rule id's pattern — everything after wordEdge, including the trailing \b — at q, and
// returns the offset just past it, or -1 when it does not match there.
func (sc *numScan) body(id, q int) int {
	switch id {
	case numISO:
		return numISOEnd(sc.in, q)
	case numEpoch:
		return numEpochEnd(sc.in, q)
	case numClock:
		return numClockEnd(sc.in, q)
	case numHourMinSec:
		return numHourMinSecEnd(sc.in, q)
	case numMinSec:
		return numMinSecEnd(sc.in, q)
	case numMagnitude:
		return numMagnitudeEnd(sc.in, q)
	case numInPhrase:
		return numInPhraseEnd(sc.in, q)
	default:
		return -1
	}
}

// numEscAlts is the most positions one escAny match can end at: a CSI or an OSC sequence (never
// both — one wants '[' after the ESC and the other ']'), the two-byte form, and the bare ESC.
const numEscAlts = 3

// numEscMemoAfter is how many escape-run nodes ONE numericMatches call expands before it starts
// recording the ones that led nowhere.
//
// The walk escFind performs is over a DAG rather than a tree: "\x1b]\x1b\\" reaches the same
// position both as a single OSC sequence terminated by ST and as two two-byte escapes, so a buffer
// made of nothing but that four-byte pattern doubles the number of distinct paths every four
// bytes, and a buffer of "\x1bA" repeated makes every ESC in it the root of a walk as long as the
// tail. Real output does neither — an escape run is one or two sequences and the walk ends after
// two or three nodes — so allocating a memo on every call would tax every colourized log to insure
// against an input no tool emits. Deferring it until a call has expanded this many nodes bounds
// the adversarial case at O(len(in)) per rule and leaves the ordinary one allocation-free.
const numEscMemoAfter = 512

// escFrame is one node of the escape-run walk that still has alternatives left to come back to.
type escFrame struct {
	// at is the node's offset, which is always an ESC.
	at int
	// next is the index of the alternative to resume with when the walk unwinds to this node.
	next int
}

// numEscInlineDepth is how deep the walk goes before its unwind stack has to reach the heap. A real
// escape run is one or two sequences deep, so eight is generous and the ordinary call never
// allocates at all; the bound is soft — append grows past it when an input insists.
const numEscInlineDepth = 8

// escFind walks the escape run rooted at s in the order Go's regexp explores `(?:escAny)+`, and
// returns the position rule id's body matched at together with where it ended, or (0, -1).
//
// The order is what makes this a walk rather than a loop, and it is not "the longest run wins".
// `+` is greedy, so from any node the walk descends into a LONGER run before it lets the run stop
// where it is; and escAny is an alternation, so at each node the shapes are tried in escAny's own
// order — CSI, OSC, two-byte, bare ESC. "\x1b]1234567890\x071234567890 " is the case that pins
// this: the OSC branch reaches the SECOND ten-digit run and wins, so the first one — which the
// two-byte branch would have reached, and which has a perfectly good word boundary on each side —
// is never reported at all.
//
// WHY THE STACK IS EXPLICIT. The natural spelling is recursion, and it dies: a node's only
// alternative is often one more ESC, so an input of nothing but ESC bytes recurses once per byte,
// and at MaxInputBytes that is 8 million frames — 256 MB of goroutine stack per megabyte of input,
// and a `stack overflow` fatal error, which is not a panic a caller can recover from. An unwind
// stack of two ints per node costs an eighth of that and cannot take the process down.
//
// A DESCENT OWES NOTHING TO THE ALTERNATIVE IT CAME FROM. `(?:escAny)+` may stop after the escape
// ending at e, in which case the body would have to match AT e — but every body begins with a digit
// or with the 'i' of "in ", and e holds an ESC, so that reading is a certain failure and the walk
// skips it. That is what lets a run of plain ESC bytes carry no unwind work at all.
//
// A node that leads nowhere for a rule leads nowhere for that rule no matter which run reached it,
// so the exhausted ones are worth remembering once numEscMemoAfter says the input is pathological.
// Successes are not recorded and do not need to be: a success advances the rule's cursor past
// everything the walk descended through.
func (sc *numScan) escFind(id, s int) (int, int) {
	bit := uint8(1) << id
	stack := sc.stack[:0]
	at, a := s, 0

	for {
		sc.steps++
		if sc.steps == numEscMemoAfter {
			sc.dead = make([]uint8, len(sc.in))
		}

		// An exhausted node expands to nothing, which the loop below then treats as exhausted
		// again — the same answer by the same path, reached without re-walking the subtree.
		n := 0
		if sc.dead == nil || sc.dead[at]&bit == 0 {
			n = sc.escAlts(at)
		}

		descended := false
		for ; a < n; a++ {
			e := sc.altEnds[a]
			if e < len(sc.in) && sc.in[e] == escByte {
				if sc.escPending(id, a+1, n) {
					stack = append(stack, escFrame{at: at, next: a + 1})
				} else if sc.dead != nil {
					// Nothing is owed to this node, so its fate is entirely the descent's and it
					// can be recorded dead now instead of costing a frame. If the descent
					// SUCCEEDS the mark is wrong — and unreachable: the rule's cursor then moves
					// past the whole run, every later walk for this rule starts to the right of
					// it, and every edge points further right still.
					sc.dead[at] |= bit
				}
				at, a, descended = e, 0, true
				break
			}
			if end := sc.body(id, e); end >= 0 {
				sc.stack = stack
				return e, end
			}
		}
		if descended {
			continue
		}

		if sc.dead != nil {
			sc.dead[at] |= bit
		}
		if len(stack) == 0 {
			sc.stack = stack
			return 0, -1
		}
		top := stack[len(stack)-1]
		stack, at, a = stack[:len(stack)-1], top.at, top.next
	}
}

// escAlts loads the escape alternatives of the node at `at` into sc.altEnds and returns how many
// there are, reusing the last node's answer when it is the same node. in[at] must be ESC.
func (sc *numScan) escAlts(at int) int {
	if sc.altAt != at {
		sc.altAt, sc.altN = at, numEscAltEnds(sc.in, at, &sc.altEnds)
	}
	return sc.altN
}

// escPending reports whether any alternative from `from` onward could still produce a result once
// the descent in front of them has failed — that is, whether the node is worth an unwind frame.
//
// A remaining alternative earns one only if it is another descent, or if its body matches. A body
// test's answer does not depend on when it is asked, so asking now is free and almost always
// settles it: the bare-ESC residual always ends on the byte after the ESC, which for every shape
// longer than it is a '[', a ']' or something in 0x40-0x5F — never a digit and never the 'i' of
// "in ". That is what keeps a run of plain ESC bytes at constant stack however long it is, which
// at MaxInputBytes is the difference between eight million frames and none.
func (sc *numScan) escPending(id, from, n int) bool {
	for b := from; b < n; b++ {
		e := sc.altEnds[b]
		if e < len(sc.in) && sc.in[e] == escByte {
			return true
		}
		if sc.body(id, e) >= 0 {
			return true
		}
	}
	return false
}

// numEscAltEnds fills ends with every position one escAny match beginning at s can end at, in
// escAny's alternation order, and returns how many there are. in[s] must be ESC.
//
// It is escLen's enumeration rather than escLen itself. escLen answers "how many bytes does the
// escape here occupy", which is all a deletion pass needs, and it answers with the FIRST shape
// that matches; a backtracking assertion needs every shape, because a longer one that leads to no
// match does not stop a shorter one from leading to one — "\x1b]5s\x07" is exactly that, where the
// OSC sequence swallows the duration and the two-byte reading hands it back.
func numEscAltEnds(in []byte, s int, ends *[numEscAlts]int) int {
	n := 0
	if l := csiLen(in, s); l > 0 {
		ends[n] = s + l
		n++
	} else if l := oscLen(in, s); l > 0 {
		ends[n] = s + l
		n++
	}
	if s+1 < len(in) {
		// escTwo: ESC then 0x40-0x5A or 0x5C-0x5F. The gap at 0x5B is '[', which CSI owns.
		if c := in[s+1]; (c >= 0x40 && c <= 0x5a) || (c >= 0x5c && c <= 0x5f) {
			ends[n] = s + 2
			n++
		}
	}
	// The bare-ESC residual, which matches unconditionally and is therefore always last.
	ends[n] = s + 1
	return n + 1
}

// ---------------------------------------------------------------------------------------------
// The seven bodies
// ---------------------------------------------------------------------------------------------

// numISOEnd matches `\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?\b`
// at i and returns its end, or -1.
func numISOEnd(in []byte, i int) int {
	// isoBase is YYYY-MM-DDThh:mm:ss, the shortest form the rule accepts. Every index below is
	// inside it, so one bounds check covers them all.
	const isoBase = 19
	if i+isoBase > len(in) {
		return -1
	}
	if !numDigits(in, i, 4) || in[i+4] != '-' || !numDigits(in, i+5, 2) || in[i+7] != '-' ||
		!numDigits(in, i+8, 2) {
		return -1
	}
	// The SQL-style space separator is accepted alongside 'T' because every database client
	// prints it that way.
	if c := in[i+10]; c != 'T' && c != ' ' {
		return -1
	}
	if !numDigits(in, i+11, 2) || in[i+13] != ':' || !numDigits(in, i+14, 2) || in[i+16] != ':' ||
		!numDigits(in, i+17, 2) {
		return -1
	}

	sec := i + isoBase
	// `(?:\.\d{1,9})?` is greedy and so is `\d{1,9}`: the longest fraction the input offers,
	// capped at nine digits, is tried first and every shorter one after it, because only the
	// trailing \b — reached past the zone — can reject a fraction that parsed. Ten fraction
	// digits are what makes the fallbacks observable: all nine cappings then end on a digit, the
	// zone cannot start on one either, and the match settles for the bare 19-byte form.
	if sec < len(in) && in[sec] == '.' {
		const maxFrac = 9
		n := numDigitRun(in, sec+1)
		if n > maxFrac {
			n = maxFrac
		}
		for ; n >= 1; n-- {
			if e := numISOZoneEnd(in, sec+1+n); e >= 0 {
				return e
			}
		}
	}
	return numISOZoneEnd(in, sec)
}

// numISOZoneEnd matches `(?:Z|[+-]\d{2}:?\d{2})?\b` at p and returns the end of the timestamp, or
// -1 when no reading of the zone leaves a word break behind it.
func numISOZoneEnd(in []byte, p int) int {
	if p < len(in) {
		switch in[p] {
		case 'Z':
			if numWordBreak(in, p+1) {
				return p + 1
			}
		case '+', '-':
			// `:?` is greedy, so "+02:00" is tried before "+0200". A "+02:000" satisfies
			// neither — the second reading wants a digit where the ':' is — and falls through
			// to the absent zone below, which succeeds because '+' is not a word byte.
			if numDigits(in, p+1, 2) && p+3 < len(in) && in[p+3] == ':' && numDigits(in, p+4, 2) &&
				numWordBreak(in, p+6) {
				return p + 6
			}
			if numDigits(in, p+1, 4) && numWordBreak(in, p+5) {
				return p + 5
			}
		}
	}
	// The zone is optional, so the last reading is to have none and require the break where the
	// seconds — or the fraction — ended.
	if numWordBreak(in, p) {
		return p
	}
	return -1
}

// numEpochEnd matches `\d{10,13}\b` at i and returns its end, or -1.
//
// The two bounds are what keep a 10-to-13 digit run that is PART of something longer — a hash, an
// identifier, a 20-digit number — out of scope: `\d{10,13}` is greedy, so a longer run is tried at
// 13 digits and then at 12, 11 and 10, and every one of those ends on a digit, which is a word
// byte, which is what the trailing \b rejects.
func numEpochEnd(in []byte, i int) int {
	const (
		minEpochDigits = 10 // seconds
		maxEpochDigits = 13 // milliseconds
	)
	n := numDigitRun(in, i)
	if n > maxEpochDigits {
		n = maxEpochDigits
	}
	for ; n >= minEpochDigits; n-- {
		if numWordBreak(in, i+n) {
			return i + n
		}
	}
	return -1
}

// numClockEnd matches `\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?\b` at i and returns its end, or -1.
func numClockEnd(in []byte, i int) int {
	// clockBase is hh:mm:ss, and as in numISOEnd one bounds check covers every index in it.
	const clockBase = 8
	if i+clockBase > len(in) {
		return -1
	}
	if !numDigits(in, i, 2) || in[i+2] != ':' || !numDigits(in, i+3, 2) || in[i+5] != ':' ||
		!numDigits(in, i+6, 2) {
		return -1
	}

	sec := i + clockBase
	// Six fraction digits, not nine — this rule caps lower than the ISO one, so
	// "12:34:56.1234567" matches only "12:34:56": all six cappings end on a digit and the
	// fraction is dropped entirely rather than truncated.
	if sec < len(in) && in[sec] == '.' {
		const maxFrac = 6
		n := numDigitRun(in, sec+1)
		if n > maxFrac {
			n = maxFrac
		}
		for ; n >= 1; n-- {
			if numWordBreak(in, sec+1+n) {
				return sec + 1 + n
			}
		}
	}
	if numWordBreak(in, sec) {
		return sec
	}
	return -1
}

// numHourMinSecEnd matches `\d+h\d+m\d+(?:\.\d+)?s\b` at i and returns its end, or -1.
func numHourMinSecEnd(in []byte, i int) int {
	n := numDigitRun(in, i)
	if n == 0 || i+n >= len(in) || in[i+n] != 'h' {
		return -1
	}
	p := i + n + 1
	m := numDigitRun(in, p)
	if m == 0 || p+m >= len(in) || in[p+m] != 'm' {
		return -1
	}
	return numFracSecEnd(in, p+m+1)
}

// numMinSecEnd matches `\d+m\d+(?:\.\d+)?s\b` at i and returns its end, or -1.
func numMinSecEnd(in []byte, i int) int {
	n := numDigitRun(in, i)
	if n == 0 || i+n >= len(in) || in[i+n] != 'm' {
		return -1
	}
	return numFracSecEnd(in, i+n+1)
}

// numFracSecEnd matches `\d+(?:\.\d+)?s\b` at p, the tail both composite duration forms end with.
//
// Every `\d+` here takes the whole digit run and never backtracks, and that is a fact about the
// pattern rather than a shortcut: the byte that follows a `\d+` must be 'h', 'm', '.' or 's', and
// a shorter run would leave a digit there. The optional fraction is the same argument once
// removed — with a '.' present, dropping the group demands an 's' where the '.' is — so a '.' that
// begins a fraction commits the whole tail to it.
func numFracSecEnd(in []byte, p int) int {
	n := numDigitRun(in, p)
	if n == 0 {
		return -1
	}
	p += n
	if p < len(in) && in[p] == '.' {
		m := numDigitRun(in, p+1)
		if m == 0 {
			return -1
		}
		p += 1 + m
	}
	if p >= len(in) || in[p] != 's' || !numWordBreak(in, p+1) {
		return -1
	}
	return p + 1
}

// numMagnitudeEnd matches `\d+(?:\.\d+)?\s?(?:ns|µs|us|ms|s|m|h)\b` at i and returns its end, or
// -1.
func numMagnitudeEnd(in []byte, i int) int {
	n := numDigitRun(in, i)
	if n == 0 {
		return -1
	}
	return numUnitTail(in, i+n, false)
}

// numInPhraseEnd matches `in (\d+(?:\.\d+)?\s?(?:ms|s))\b` at i and returns its end, or -1. The
// SPAN starts numInPhraseLead bytes later; emit applies that offset.
func numInPhraseEnd(in []byte, i int) int {
	if i+numInPhraseLead > len(in) || in[i] != 'i' || in[i+1] != 'n' || in[i+2] != ' ' {
		return -1
	}
	n := numDigitRun(in, i+numInPhraseLead)
	if n == 0 {
		return -1
	}
	return numUnitTail(in, i+numInPhraseLead+n, true)
}

// numUnitTail matches `(?:\.\d+)?\s?(unit)\b` at p, the tail the two unit-suffixed rules share.
// phrase selects numInPhrase's shorter unit alternation.
func numUnitTail(in []byte, p int, phrase bool) int {
	if p < len(in) && in[p] == '.' {
		n := numDigitRun(in, p+1)
		if n == 0 {
			// The fraction cannot match, and the only fallback the pattern has is to drop it —
			// which then wants whitespace or a unit letter where the '.' is, and neither `\s`
			// nor any of the seven units admits one.
			return -1
		}
		if e := numSpaceUnit(in, p+1+n, phrase); e >= 0 {
			return e
		}
		// A fraction that DID parse is no different: dropping it lands on the same '.'.
		return -1
	}
	return numSpaceUnit(in, p, phrase)
}

// numSpaceUnit matches `\s?(unit)\b` at p.
func numSpaceUnit(in []byte, p int, phrase bool) int {
	if p < len(in) && numIsPerlSpace(in[p]) {
		if e := numUnitAt(in, p+1, phrase); e >= 0 {
			return e
		}
		// `\s?` backtracks to matching nothing, but a unit would then have to begin with the
		// whitespace byte, and none of the seven does.
		return -1
	}
	return numUnitAt(in, p, phrase)
}

// numUnitAt matches one unit at p together with the \b behind it, or returns -1.
//
// The alternation is `(?:ns|µs|us|ms|s|m|h)` for numMagnitude and `(?:ms|s)` for numInPhrase, and
// order inside it is load bearing: Go's regexp is leftmost-FIRST, so with "s" ahead of "ms" a
// "12ms" would match only "12m" and then fail its trailing \b. Every alternative is distinguished
// by its FIRST byte except "ms" and "m", so a switch on that byte preserves the order exactly
// while testing at most three bytes.
//
// That last part is not a micro-optimization. The obvious spelling — a bytes.HasPrefix over a
// slice of unit literals — calls runtime.memequal once per alternative per candidate, and it sat
// at a THIRD of this scanner's whole profile.
//
// A unit that matches but whose \b fails does not end the search; the next alternative is tried,
// which is how "5mss" is rejected — "ms" is followed by a word byte, and so is the bare "m" the
// fallback finds.
func numUnitAt(in []byte, p int, phrase bool) int {
	if p >= len(in) {
		return -1
	}
	switch in[p] {
	case 'm':
		if p+1 < len(in) && in[p+1] == 's' && numWordBreak(in, p+2) {
			return p + 2
		}
		if !phrase && numWordBreak(in, p+1) {
			return p + 1
		}
	case 's':
		if numWordBreak(in, p+1) {
			return p + 1
		}
	case 'n', 'u':
		if !phrase && p+1 < len(in) && in[p+1] == 's' && numWordBreak(in, p+2) {
			return p + 2
		}
	case numMicroLead:
		if !phrase && p+2 < len(in) && in[p+1] == numMicroTail && in[p+2] == 's' &&
			numWordBreak(in, p+3) {
			return p + 3
		}
	case 'h':
		if !phrase && numWordBreak(in, p+1) {
			return p + 1
		}
	}
	return -1
}

// ---------------------------------------------------------------------------------------------
// Byte predicates
// ---------------------------------------------------------------------------------------------

// numWordByte marks [0-9A-Za-z_], the class \b tests on each side of a position, as one indexed
// load. The class is ASCII only, exactly as Go's regexp's is: any byte at 0x80 or above — whether
// it starts a rune, is a continuation byte, or is not valid UTF-8 at all — is a non-word byte to
// \b, so testing the raw byte answers the same question decoding it would.
var numWordByte = newNumWordByteTable()

// newNumWordByteTable builds numWordByte at package initialization.
func newNumWordByteTable() [256]bool {
	var t [256]bool
	for b := range t {
		c := byte(b)
		t[b] = c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
	}
	return t
}

// numIsWordByte reports whether b is one of [0-9A-Za-z_].
func numIsWordByte(b byte) bool { return numWordByte[b] }

// numWordBreak reports whether a trailing \b succeeds at e: either the buffer ends there, or the
// byte there is not a word byte. The byte BEFORE e is always a word byte at every call site — it
// is the last byte of a body that ends in a digit, a letter or a 'Z' — so the other half of the
// assertion is already settled.
func numWordBreak(in []byte, e int) bool {
	return e >= len(in) || !numWordByte[in[e]]
}

// numIsPerlSpace reports whether b is one of the bytes Go's regexp `\s` accepts.
//
// It is neither unicode.IsSpace nor "any control byte below 0x20": Go's Perl class is exactly
// {'\t', '\n', '\f', '\r', ' '} and it pointedly excludes the vertical tab 0x0B. That "5\fms" is a
// duration and "5\vms" is not is not a distinction worth having, but it IS the distinction the
// rule this replaces drew, and reproducing it is the whole point of this file.
func numIsPerlSpace(b byte) bool {
	return b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

// numDigitRun returns how many ASCII digits run from p, which is 0 when p is past the end.
func numDigitRun(in []byte, p int) int {
	n := 0
	for p+n < len(in) && in[p+n] >= '0' && in[p+n] <= '9' {
		n++
	}
	return n
}

// numDigits reports whether in holds exactly n ASCII digits starting at at, bounds included.
func numDigits(in []byte, at, n int) bool {
	if at+n > len(in) {
		return false
	}
	for i := at; i < at+n; i++ {
		if in[i] < '0' || in[i] > '9' {
			return false
		}
	}
	return true
}
