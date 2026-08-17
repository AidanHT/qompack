package canon

import (
	"bytes"
	"regexp"
	"strings"
)

// The tool-name alias sets the seven per-tool canonicalizers dispatch on.
//
// 00-ARCHITECTURE.md §5.6 keys canonicalizers by TOOL, and Qompack.md §2.2 fixes the compactable
// tool set. A tool name reaches this package straight out of a PostToolUse hook payload, so both
// of Claude Code's spellings for the same operation have to be listed (Read/FileRead,
// Write/FileWrite, Edit/FileEdit, Grep/Search, Glob/LS), and matching is case-insensitive: the
// hook spells them TitleCase, a shell transcript or a replay fixture spells them lowercase, and a
// canonicalizer that silently stopped applying because of a capital letter would show up only as
// a slowly worsening dedup ratio.
var (
	// shellTools is the set of tools whose output is a raw terminal transcript. Three
	// canonicalizers share it — bash, testrunner and git — because a test run and a git command
	// both arrive as "the output of a shell tool" and there is no field in the payload that says
	// which; each one's RULES, not its Applies, decide whether it has anything to say about the
	// bytes.
	shellTools = []string{"bash", "powershell", "shell"}
	// grepTools covers Claude Code's Grep and its aliases.
	grepTools = []string{"grep", "search", "ripgrep"}
	// globTools covers the path-listing tools, whose every output line is a path.
	globTools = []string{"glob", "ls", "find"}
	// fileTools covers the file read/write/edit tools, whose output is file CONTENT.
	fileTools = []string{"read", "fileread", "write", "filewrite", "edit", "fileedit"}
	// webTools covers the network-fetch tools, whose output is URLs and HTTP headers.
	webTools = []string{"webfetch", "websearch", "fetch"}
	// gitTools is shellTools plus an explicit "git", for hosts that report the subcommand as the
	// tool name.
	gitTools = []string{"bash", "powershell", "shell", "git"}
)

// toolIn reports whether tool is one of aliases, compared case-insensitively. strings.EqualFold is
// used rather than a lowercased map lookup because the alias sets are tiny and Applies is called
// once per registered canonicalizer per tool call on the PostToolUse hot path (Qompack.md §8.1:
// p99 < 15ms), where an allocation-free scan of six strings beats a map that has to be built.
func toolIn(tool string, aliases []string) bool {
	for _, a := range aliases {
		if strings.EqualFold(tool, a) {
			return true
		}
	}
	return false
}

// trailingWSMatches deletes horizontal whitespace at the end of a line. It is shared by bash,
// grep, glob and fileread.
//
// It is ClassCRLF, and therefore always-on and not gateable, for exactly the reason
// 00-ARCHITECTURE.md §4 makes CRLF→LF always-on: trailing whitespace is a line-terminator artefact
// that differs between the editors, shells and platforms that produced otherwise identical text,
// and letting a config turn it off would fork the dedup space in half in the same way. classes.go
// puts ClassCRLF in alwaysOn; Appendix C has no strip value that could disable it.
//
// The carriage returns are matched as CONTEXT and left out of the replaced span. A Windows pipe
// puts a CR between the whitespace and the LF, so a bare `[ \t]+$` would not fire at all on the
// original and would fire on the canonical output once crlfCanon had removed the CR — a two-pass
// convergence, which §5.6 forbids. Including the CR in the span instead would fix that but would
// steal it from crlfCanon's own Delta, and LineEndingClass reads those Deltas to report whether a
// payload was CRLF: a CRLF file with trailing whitespace on every line would then be reported as
// LF. Matching the CRs without replacing them is the only spelling that keeps both.
//
// Escape sequences ARE inside the replaced span, because there is no way to leave them out: a
// colourized tool writes " \x1b[m" at the end of a line, and a span that stopped at the space
// would leave the escape to be deleted by ansiCanon in the same pass — after which the space is
// once again trailing, for the NEXT pass to find. ansiCanon's own match for those bytes overlaps
// this one and loses in acceptCandidates, which is correct: both delete, the Delta records the
// original bytes either way, and Restore is unaffected.
//
// WHY THIS IS A REGION SCAN AND NOT ONE SPAN. The obvious spelling,
// `(?m)([ \t](?:escAny|[ \t])*)\r*$` replacing the group, is NOT idempotent, and rapid found the
// counter-example: "\t\r\t" converges only after three passes. Because the trailing CRs are
// context rather than span, deleting the last whitespace run promotes the CR to final byte — and
// the run BEFORE that CR, which was not trailing in the original, becomes trailing on the next
// pass. Any rule that leaves a byte it matched over in place has this shape.
//
// The fix is to stop treating "the trailing run" as one span. The scan finds the whole end-of-line
// tail — escape sequences, spaces, tabs and carriage returns in any order — and then emits one
// Match per whitespace-bearing run INSIDE it, splitting on the CRs it must not consume. Every run
// that could ever become trailing is therefore removed in the same pass, and a second pass sees a
// tail of nothing but CRs and escapes, which yields no matches at all. Idempotence is structural
// rather than argued.
//
// The tail admits every shape ansiCanon strips, not just a CSI sequence. A bare ESC or a two-byte
// escape sitting between the whitespace and the end of the line would otherwise be invisible here
// while still being deleted by ansiCanon, so " \x1b" left the space untrimmed on pass 1 and
// trimmed it on pass 2. The fuzzer found exactly that.
//
// WHY IT IS A BYTE LOOP AND NOT `(?m)(?:escAny|[ \t\r])+$`. That regexp is what this was, but it
// begins with an alternation rather than a literal, so Go's regexp has no prefix to skip on and
// simulates its automaton over every byte at roughly 20 MB/s — 5 ms per 100 KB, against a 3 ms
// budget for the whole canonicalization call, paid by every per-tool canonicalizer that trims. The
// loop below is the same language at memory speed. The retired pattern lives on in
// prefilter_test.go as the oracle TestTrailingWS_AgreesWithReference compares against, so the two
// can never quietly diverge.
//
// The scan is per line and escLen is bounded to the line, which makes the tail line-local by
// construction — the same restriction escAnyInline applies to the anchored rules, and for the same
// reason: an OSC sequence with an unterminated body could otherwise swallow a newline and let one
// line's tail start on the line before it.
//
// trailingWSMatches appends one Match per whitespace-bearing run in each end-of-line tail.
//
// A run holding only escape sequences and no horizontal whitespace is deliberately skipped: there
// is no trailing whitespace there to strip, and emitting it under ClassCRLF would both mislabel the
// Delta and take the span away from ansiCanon, which owns it.
func trailingWSMatches(dst []Match, in []byte) []Match {
	forEachLine(in, func(start, end int) {
		line := in[:end]

		// tail tracks the position just past the last byte that CANNOT be part of an end-of-line
		// tail, so when the line runs out it is the leftmost start of the maximal tail run —
		// exactly the leftmost match the retired pattern reported.
		tail := start
		for i := start; i < end; {
			if n := escLen(line, i); n > 0 {
				i += n
				continue
			}
			if c := in[i]; c == ' ' || c == '\t' || c == '\r' {
				i++
				continue
			}
			i++
			tail = i
		}

		for i := tail; i < end; {
			if in[i] == '\r' {
				i++
				continue
			}
			runStart, sawWS := i, false
			for ; i < end && in[i] != '\r'; i++ {
				if in[i] == ' ' || in[i] == '\t' {
					sawWS = true
				}
			}
			if sawWS {
				dst = append(dst, Match{Offset: runStart, Len: i - runStart, Class: ClassCRLF})
			}
		}
	})
	return dst
}

// ---------------------------------------------------------------------------------------------
// 8. bash
// ---------------------------------------------------------------------------------------------

// bashCanon collapses terminal progress-bar residue and trailing whitespace in shell transcripts
// (00-ARCHITECTURE.md §5.6).
type bashCanon struct{}

// newBash returns the bash canonicalizer, eighth in §5.6's registration order and first of the
// per-tool ones.
func newBash() bashCanon { return bashCanon{} }

// Name returns "bash".
func (bashCanon) Name() string { return nameBash }

// Applies reports whether tool is a shell tool. path is ignored: a shell transcript's shape is a
// property of the tool, not of any file it happened to mention.
func (bashCanon) Applies(tool, _ string) bool { return toolIn(tool, shellTools) }

// Matches reports the progress-collapse and trailing-whitespace spans.
func (bashCanon) Matches(in []byte, _ Options) []Match {
	return trailingWSMatches(bashProgressMatches(in), in)
}

// bashProgressMatches deletes everything a carriage return overwrote.
//
// npm, pip, cargo, docker and every other progress bar redraw their line by printing a bare '\r'
// and starting again. Captured to a pipe, the result is dozens of copies of the same line, of
// which only the LAST is what the user saw — the rest is the single largest source of junk bytes
// in shell output, and it is junk that changes on every run because the intermediate percentages
// do. For each bare '\r' this emits one deletion running from the start of the current segment
// (the byte after the previous '\r' or '\n') through the '\r' itself, so a line redrawn N times
// yields N non-overlapping deletions and exactly the final text survives.
//
// This is a hand-written loop because the rule is "a '\r' NOT followed by '\n'", and RE2 has no
// lookahead to express it. A '\r' that IS followed by '\n' is a line ending and belongs to
// crlfCanon; claiming it here would delete the whole line instead of normalizing its terminator.
//
// The Class is ClassANSI, not ClassCRLF: a carriage-return overwrite is terminal cursor control,
// the same artefact class as the CSI sequence a colour-capable version of the same tool would have
// used to do the same job (00-ARCHITECTURE.md §5.6).
//
// A RUN of carriage returns ending in '\n' is skipped whole, not just its last member. "\r\r\n" is
// a mangled line terminator — the text before it was displayed, because nothing was printed after
// the cursor returned — and crlfCanon claims the entire run. Treating only the final '\r' as the
// terminator would leave this rule claiming the first one, and the two spans would OVERLAP: the
// progress deletion starts earlier, wins in acceptCandidates, and crlfCanon's match is rejected,
// leaving a live "\r\n" in the canonical output for the next pass to normalize. That is exactly
// what the captured `curl -v` corpus file does, and it is an idempotence failure rather than a
// cosmetic one.
//
// Idempotence: every bare '\r' in the input is consumed by some emitted span, and the spans are
// non-overlapping, so the canonical output contains no bare '\r' at all. The text a deletion
// splices together cannot manufacture one either — a deletion always ends AT a '\r' and always
// begins at a segment boundary, so the bytes on either side of the join are a segment start and a
// '\n' or end of input.
func bashProgressMatches(in []byte) []Match {
	var out []Match
	segStart := 0
	for i := 0; i < len(in); i++ {
		switch in[i] {
		case '\n':
			segStart = i + 1
		case '\r':
			if run := crRunEnd(in, i); run < len(in) && in[run] == '\n' {
				i = run - 1
				continue
			}
			out = append(out, Match{Offset: segStart, Len: i + 1 - segStart, Token: nil, Class: ClassANSI})
			segStart = i + 1
		}
	}
	return out
}

// crRunEnd returns the index of the first byte after the run of '\r' that starts at i.
func crRunEnd(in []byte, i int) int {
	for i < len(in) && in[i] == '\r' {
		i++
	}
	return i
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c bashCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 9. testrunner
// ---------------------------------------------------------------------------------------------

// testRunnerRules strips the two volatile values a test runner prints: how long something took,
// and which scheduler slot ran it.
//
// A test suite's output is the highest-value content Qompack stores — it is what a session comes
// back to over and over — and it is byte-identical between runs EXCEPT for these two. Stripping
// them turns "ran the suite again" from a fresh store write into a hit (Qompack.md §8.1, O2).
//
// Applies is unconditional over the shell tools rather than sniffing for a runner, because there
// is no reliable signal in a PostToolUse payload that says "this was `go test`". The rules
// themselves are the discriminator: each is anchored to a shape only one runner produces, so on
// non-runner output they simply find nothing.
//
// GUARD RELIANCE, in four places. Every id capture here is narrower than the three-byte tokenNumber
// and every duration capture can be narrower than the six-byte tokenSeed, so acceptCandidates
// drops the short ones instead of growing the input:
//
//   - `goroutine (\d{1,6})` — a 1- or 2-digit goroutine number survives verbatim; goroutine 1, the
//     main goroutine, is therefore never replaced.
//   - jest `worker (\d{1,3})` — likewise for workers 0-99.
//   - pytest `gw\d{1,2}` is the exception, and it is why the span is the WHOLE marker rather than
//     the digits. Capturing only the digits makes the rule inert in its entirety — every match it
//     could ever produce is one or two bytes against the three-byte tokenNumber, so
//     acceptCandidates rejects all of them — and a rule that can never fire is worse than one that
//     fires imperfectly: it compiles, it runs, it emits matches, and it changes nothing, with no
//     error anywhere. Matching "gw0".."gw15" whole is 3-4 bytes, which fits, and collapsing the
//     marker along with the slot is the better canonical form anyway: the same test landing on gw0
//     one run and gw3 the next is exactly the volatility this class exists to remove.
//   - `--randomly-seed=(\d+)` — a seed shorter than six digits survives verbatim.
//
// TestTestRunner_Go, _Jest and _Pytest name each of these reliances directly.
//
// WORD-BOUNDARY INVARIANT. Four groups here end in a word byte with nothing after them in the
// pattern — the go `ok` duration, the jest Time, the pytest seed and the cargo "finished in" — and
// each carries an explicit trailing \b. The other groups need none: every one of them is pinned by
// a literal non-word byte on both sides ("goroutine (…) [", "((…))", "worker (…)\b", "in (…) =").
//
// The one place the invariant is satisfied by argument rather than by an anchor is the LEFT edge of
// the `ok` and `Time:` groups, where the preceding pattern is sepRun and so the byte before the
// span may be the final byte of an SGR sequence — a word byte, with no boundary. An explicit \b
// there would break colourized runner output, which is the whole reason sepRun exists. It is safe
// without one: a boundary appearing at that position on a later pass could only enable a rule whose
// span ENDS there, and the only spans that can end on an escape sequence's final byte or on
// whitespace are ansiRules' and trailingWSMatches' — neither of which asserts a boundary at all.
var testRunnerRules = []reRule{
	// go test: the per-package summary line. "(cached)" is left completely alone — it is not a
	// duration, and it is the single most informative token on the line. The column separators are
	// sepRun rather than \s+ because gotestsum and go-junit-report colourize the columns.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `ok` + sepRun + `\S+` + sepRun + `(\d+\.\d{1,3}s)\b`), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, need: []string{"ok"}, perLine: true},
	// go test -v: the per-test result line, whose duration is always two decimal places.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `--- (?:PASS|FAIL|SKIP): \S+ \((\d+\.\d{2}s)\)`), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, need: []string{"--- PASS", "--- FAIL", "--- SKIP"}, perLine: true},
	// go panic/deadlock dumps: the goroutine number is a scheduler artefact, and the "[running]"
	// state that follows it is not.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `goroutine (\d{1,6}) \[`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"goroutine "}, perLine: true},
	// jest: the summary "Time:" line, whose label jest prints in bold.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `Time:` + sepRun + `(\d+(?:\.\d+)?\s?s)\b`), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, need: []string{"Time:"}, perLine: true},
	// jest -w: the worker slot a test landed on, which varies with machine load rather than with
	// anything about the test.
	{re: regexp.MustCompile(wordEdge + `worker (\d{1,3})\b`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"worker "}},
	// pytest: the "===== N passed in 0.12s =====" footer, anchored to a WHOLE line. Unanchored, the
	// greedy `.*` skips to the last " in <duration> ==" on the line, so a line carrying two footers
	// would have its second duration replaced on the first pass and its first duration on the
	// second — an idempotence failure. With the end anchored, the only duration the rule can ever
	// see is the one immediately before the closing rule, and once that is a token the line stops
	// matching. lineTail is what lets the anchor survive colour, trailing whitespace and CRs.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `={2,} .* in (\d+\.\d+s) ={2,}` + lineTail), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, needAll: []string{"==", " in "}, perLine: true},
	// pytest-randomly: the seed line. The seed is deliberately re-randomized per run, which makes
	// it the most volatile byte-run in the whole report.
	{re: regexp.MustCompile(`Using --randomly-seed=(\d+)\b`), spans: firstGroup, token: []byte(tokenSeed), class: ClassPIDs, need: []string{"Using --randomly-seed="}},
	// pytest-xdist: the worker slot, "gw0".."gw15". Whole-match, not the digits — see the GUARD
	// RELIANCE note above for why the digits alone can never survive the non-growing guard.
	{re: regexp.MustCompile(wordEdge + `(gw\d{1,2})\b`), spans: firstGroup, token: []byte(tokenNumber), class: ClassPIDs, need: []string{"gw"}},
	// cargo/nextest: "…finished in 1.23s".
	{re: regexp.MustCompile(`finished in (\d+\.\d+s)\b`), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, need: []string{"finished in "}},
	// cargo build: the "Finished `dev` profile … in 0.53s" line, where the duration is always the
	// last thing on the line. End-anchored for the same reason as the pytest footer above: with a
	// greedy `.*` and no anchor, a line holding two " in <duration>" phrases converges only after
	// two passes.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `Finished .* in (\d+\.\d+s)` + lineTail), spans: firstGroup, token: []byte(tokenDuration), class: ClassDurations, need: []string{"Finished "}, perLine: true},
}

// tokenSeed replaces a pytest-randomly seed. Six bytes: it is named rather than reusing
// tokenNumber because a seed is not a small ordinal and the canonical line reads better saying so,
// and it is sized against a realistic seed (pytest-randomly mints ten digits). A shorter seed is
// dropped by the non-growing guard rather than replaced.
const tokenSeed = "<seed>"

// testRunnerCanon strips test-runner durations and run ids (00-ARCHITECTURE.md §5.6).
type testRunnerCanon struct{}

// newTestRunner returns the testrunner canonicalizer, ninth in §5.6's registration order.
func newTestRunner() testRunnerCanon { return testRunnerCanon{} }

// Name returns "testrunner".
func (testRunnerCanon) Name() string { return nameTestRunner }

// Applies reports whether tool is a shell tool; see the discriminator note on testRunnerRules.
func (testRunnerCanon) Applies(tool, _ string) bool { return toolIn(tool, shellTools) }

// Matches reports every runner-shaped span testRunnerRules finds.
func (testRunnerCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, testRunnerRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c testRunnerCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 10. grep
// ---------------------------------------------------------------------------------------------

// grepCanon normalizes the "path:line:" prefix of a grep hit (00-ARCHITECTURE.md §5.6).
type grepCanon struct{}

// newGrep returns the grep canonicalizer, tenth in §5.6's registration order.
func newGrep() grepCanon { return grepCanon{} }

// Name returns "grep".
func (grepCanon) Name() string { return nameGrep }

// Applies reports whether tool is a search tool.
func (grepCanon) Applies(tool, _ string) bool { return toolIn(tool, grepTools) }

// Matches reports the prefix-separator and trailing-whitespace spans.
func (grepCanon) Matches(in []byte, _ Options) []Match {
	return trailingWSMatches(grepPrefixMatches(in), in)
}

// grepPrefixMatches normalizes path separators and strips a drive letter INSIDE a grep hit's
// leading "path:line:" prefix, and nowhere else.
//
// This is the whole point of having a grep-specific canonicalizer at all. 00-ARCHITECTURE.md §4
// makes separator normalization a precondition of cross-platform dedup, so a Windows and a Linux
// search for the same string must produce the same canonical bytes — but a grep hit's line is
// PATH followed by MATCHED SOURCE, and the matched source is arbitrary text that very often
// contains backslashes of its own (a regex, a Windows path in a string literal, an escape
// sequence). Rewriting those would corrupt the content the user searched for. Restricting the
// rewrite to the prefix is what makes the normalization safe, and TestGrep_PathPrefixOnly pins
// exactly that boundary.
//
// It is a hand-written scan because the rule is "inside the prefix", which is a lookbehind
// condition RE2 cannot express: the prefix is found first, and only then are the bytes within it
// rewritten one at a time. Each emitted Match is one byte replaced by one byte — Class ClassPaths,
// always-on, and trivially non-growing.
//
// A drive letter is folded into a single three-byte match spanning "C:" plus its separator, rather
// than left to overlap resolution between a drive rule and a separator rule. Both would produce
// the same output, but emitting one unambiguous span keeps the delta record honest about what was
// replaced.
//
// Idempotence: after one pass the prefix contains no backslash and does not begin with a drive
// letter, and the prefix's own extent is unchanged by either rewrite (the scan for ":<digits>:"
// skips a drive's colon, and '/' is not ':'), so a second pass finds the same prefix and nothing
// to do inside it.
func grepPrefixMatches(in []byte) []Match {
	var out []Match
	forEachLine(in, func(start, end int) {
		drive := 0
		if end-start >= driveLen && isASCIILetter(in[start]) && in[start+1] == ':' && isSep(in[start+2]) {
			drive = driveLen
		}
		stop := pathLinePrefixEnd(in, start+drive, end)
		if stop < 0 {
			return
		}
		if drive > 0 {
			out = append(out, Match{Offset: start, Len: drive, Token: slash, Class: ClassPaths})
		}
		out = appendSeparatorMatches(out, in, start+drive, stop)
	})
	return out
}

// driveLen is the length of a Windows drive prefix including its separator: "C:\" or "C:/".
const driveLen = 3

// isASCIILetter reports whether b is A-Z or a-z.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isSep reports whether b is either path separator.
func isSep(b byte) bool { return b == '/' || b == '\\' }

// appendSeparatorMatches appends a one-byte '\\' → '/' replacement for every backslash in
// in[from:to].
func appendSeparatorMatches(dst []Match, in []byte, from, to int) []Match {
	for i := from; i < to; i++ {
		if in[i] == '\\' {
			dst = append(dst, Match{Offset: i, Len: 1, Token: slash, Class: ClassPaths})
		}
	}
	return dst
}

// pathLinePrefixEnd returns the index just past the closing colon of a leading "path:line:" prefix
// in in[from:end), or -1 when the line has none. It is the hand-written equivalent of anchoring
// `[^\n:]+:\d+:` at from.
//
// The path component must be non-empty and colon-free (which the first-colon search enforces by
// construction), the line number must be at least one digit, and the digits must be followed
// immediately by a colon. Anything else — a plain output line, a grep context line joined with
// '-', a Windows path with no line number — reports -1 and is left completely untouched, which is
// the conservative answer: a line that is not a hit has no prefix to normalize.
func pathLinePrefixEnd(in []byte, from, end int) int {
	colon := bytes.IndexByte(in[from:end], ':')
	if colon <= 0 {
		return -1
	}
	i := from + colon + 1
	digitsStart := i
	for i < end && in[i] >= '0' && in[i] <= '9' {
		i++
	}
	if i == digitsStart || i >= end || in[i] != ':' {
		return -1
	}
	return i + 1
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c grepCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 11. glob
// ---------------------------------------------------------------------------------------------

// globCanon normalizes path separators in path-listing output (00-ARCHITECTURE.md §5.6).
//
// Unlike grep, every byte of a glob/ls/find line IS a path, so the separator rewrite applies to
// the whole line without the prefix restriction — there is no "matched content" half that could
// legitimately contain a backslash. That is the entire difference between the two canonicalizers,
// and it is why they are two canonicalizers rather than one parameterized one: the safe extent of
// the rewrite is a property of the tool's output format.
type globCanon struct{}

// newGlob returns the glob canonicalizer, eleventh in §5.6's registration order.
func newGlob() globCanon { return globCanon{} }

// Name returns "glob".
func (globCanon) Name() string { return nameGlob }

// Applies reports whether tool is a path-listing tool.
func (globCanon) Applies(tool, _ string) bool { return toolIn(tool, globTools) }

// Matches reports one one-byte replacement per backslash, plus the trailing-whitespace spans. The
// output contains no backslash, so a second pass finds nothing: idempotence is structural.
func (globCanon) Matches(in []byte, _ Options) []Match {
	return trailingWSMatches(appendSeparatorMatches(nil, in, 0, len(in)), in)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c globCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 12. fileread
// ---------------------------------------------------------------------------------------------

// utf8BOM is the UTF-8 byte-order mark. It carries no information in a UTF-8 stream — UTF-8 has no
// byte order — but Windows editors write it and POSIX ones do not, so the same file read on two
// machines hashes differently for three bytes' worth of nothing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// fileReadCanon strips a leading byte-order mark and trailing whitespace from file content
// (00-ARCHITECTURE.md §5.6).
//
// It deliberately does NOT strip a line-number gutter, which is the rule a reader might expect
// here. Claude Code's Read tool prefixes content with "<n>\t", but a CONTENT line that begins with
// digits and a tab is byte-for-byte indistinguishable from a gutter — a data file, a diff, a
// tab-separated table, or the canonical output of a previous pass. Stripping it would therefore be
// non-idempotent in the worst way: a second pass over already-canonical bytes would eat a column
// of real data, and Restore would have no record of what it removed because the delta would name a
// gutter that never existed. The BOM rule is safe precisely because it has none of those
// properties: it is anchored at offset 0 and its three bytes are legal nowhere else.
type fileReadCanon struct{}

// newFileRead returns the fileread canonicalizer, twelfth in §5.6's registration order.
func newFileRead() fileReadCanon { return fileReadCanon{} }

// Name returns "fileread".
func (fileReadCanon) Name() string { return nameFileRead }

// Applies reports whether tool is one of the file read/write/edit tools.
func (fileReadCanon) Applies(tool, _ string) bool { return toolIn(tool, fileTools) }

// Matches reports the leading BOM run and the trailing-whitespace spans.
//
// The RUN, rather than a single BOM, is what makes the rule idempotent: a doubly-marked file
// ("\xEF\xBB\xBF\xEF\xBB\xBF…") would otherwise lose one BOM per pass and never reach a fixed
// point. Consuming all of them leaves an output that provably does not start with a BOM.
//
// The Class is ClassPaths — always-on, alongside separator normalization — because a BOM is a
// file-encoding artefact of the platform that wrote the file, exactly the kind of cross-platform
// divergence 00-ARCHITECTURE.md §4 makes non-optional so the store cannot fork in two.
func (fileReadCanon) Matches(in []byte, _ Options) []Match {
	var out []Match
	n := 0
	for bytes.HasPrefix(in[n:], utf8BOM) {
		n += len(utf8BOM)
	}
	if n > 0 {
		out = append(out, Match{Offset: 0, Len: n, Token: nil, Class: ClassPaths})
	}
	return trailingWSMatches(out, in)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c fileReadCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 13. webfetch
// ---------------------------------------------------------------------------------------------

// The webfetch tokens. Both are seven bytes, against minimum spans of eight and sixteen.
const (
	// tokenValue replaces a session or cache-buster parameter value.
	tokenValue = "<v>"
	// tokenETag replaces a weak HTTP entity tag.
	tokenETag = "<etag>"
	// tokenNonce replaces a CSP or auth nonce.
	tokenNonce = "<nonce>"
)

// webFetchRules strips the volatile identifiers a fetched page or its headers carry.
//
// Every one of these is an opaque value minted per request: the same page fetched twice is
// byte-identical apart from them, so they are the whole reason two fetches of one URL do not
// dedup. The Class is ClassAddresses for all four — 00-ARCHITECTURE.md §5.6 fixes a CLOSED set of
// eight classes, and an etag, a session id and a heap pointer are the same kind of thing: an
// opaque identity with no meaning outside the run that produced it.
//
// GUARD RELIANCE: rule (b)'s capture is `[A-Za-z0-9._-]+`, so a one- or two-character session
// value is shorter than the three-byte tokenValue and acceptCandidates drops it.
// TestWebFetch_Table names that.
var webFetchRules = []reRule{
	// Analytics parameters, deleted outright: unlike a session id they have no structural role, so
	// there is nothing worth marking. The value class excludes '<' and '>' so a token can never be
	// swallowed by a later pass, and stops at '&' so only the one parameter is removed.
	{re: regexp.MustCompile(`[?&]utm_[a-z]+=[^&\s"'<>]*`), spans: wholeMatch, token: nil, class: ClassAddresses, need: []string{"utm_"}},
	// Session ids and cache busters. The PARAMETER NAME is kept and only the value replaced, so
	// the canonical URL still shows its shape. The value class is spelled with \w for the same
	// word-boundary reason as the temp-path classes: it has to contain every word byte, or a value
	// like "a_b" would match only "a" and leave word bytes on both sides of the join for the
	// token's '>' to separate.
	{re: regexp.MustCompile(`[?&](?:sid|sessionid|_t|ts|cb|nonce)=([\w.-]+)`), spans: firstGroup, token: []byte(tokenValue), class: ClassAddresses, need: []string{"sid=", "sessionid=", "_t=", "ts=", "cb=", "nonce="}},
	// Weak entity tags, W/"…". Strong etags are left alone: they are a content digest, which is
	// stable across fetches of unchanged content and therefore signal rather than churn.
	{re: regexp.MustCompile(wordEdge + `(W/"[^"]{4,}")`), spans: firstGroup, token: []byte(tokenETag), class: ClassAddresses, need: []string{`W/"`}},
	// CSP / auth nonces. The eight-character floor keeps a short literal attribute value that
	// happens to be spelled nonce="" out of scope.
	{re: regexp.MustCompile(wordEdge + `(nonce="[^"]{8,}")`), spans: firstGroup, token: []byte(tokenNonce), class: ClassAddresses, need: []string{`nonce="`}},
}

// webFetchCanon strips volatile URL and header identifiers (00-ARCHITECTURE.md §5.6).
type webFetchCanon struct{}

// newWebFetch returns the webfetch canonicalizer, thirteenth in §5.6's registration order.
func newWebFetch() webFetchCanon { return webFetchCanon{} }

// Name returns "webfetch".
func (webFetchCanon) Name() string { return nameWebFetch }

// Applies reports whether tool is a network-fetch tool.
func (webFetchCanon) Applies(tool, _ string) bool { return toolIn(tool, webTools) }

// Matches reports every volatile identifier webFetchRules finds.
func (webFetchCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, webFetchRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c webFetchCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}

// ---------------------------------------------------------------------------------------------
// 14. git
// ---------------------------------------------------------------------------------------------

// tokenSHA replaces a git object hash. Five bytes against a seven-hex-character minimum.
const tokenSHA = "<sha>"

// gitRules replaces git object hashes with tokenSHA.
//
// A commit hash changes every time a commit is amended or rebased, which turns an otherwise
// unchanged diff into a fresh store write. Replacing it costs nothing a later reader wants: the
// diff body, the paths and the messages are all preserved, and the hash was never actionable from
// inside a compacted transcript anyway. Class ClassAddresses, for the same reason as webfetch's
// identifiers — an object hash is an opaque identity, and §5.6's eight classes are closed.
//
// TWO shapes are deliberately NOT touched:
//
//   - Hunk headers, "@@ -1,7 +1,9 @@". Those numbers are SEMANTIC — they are the line coordinates
//     the hunk applies at, and a reader (or a patch tool) needs them exactly. No rule here can
//     match them: every pattern is anchored to a keyword and requires a hex run of at least seven
//     characters. TestGit_Table pins that directly.
//   - "git version 2.43.0" and any other version string, for the same reason: it identifies the
//     tool that produced the output, which is signal about the environment, not churn.
//
// All three rules are line-anchored, which is what keeps a 40-hex-character run appearing anywhere
// else in the output — in a diff body, a lockfile, a test fixture — out of scope.
var gitRules = []reRule{
	// `git log` / `git show`: the commit line. The '$' anchor means a decorated line
	// ("commit abc… (HEAD -> main)") is left alone rather than half-rewritten.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `commit ([0-9a-f]{40})` + lineTail), spans: firstGroup, token: []byte(tokenSHA), class: ClassAddresses, need: []string{"commit "}, perLine: true},
	// `git diff`: the blob index line, whose two abbreviated hashes are both replaced. This is the
	// line the captured `git diff --color` corpus file proves lineLead is needed for: git wraps it
	// in "\x1b[1m…\x1b[m", so a bare ^ anchor finds it only after ansiCanon has run.
	// The trailing \b is the word-boundary invariant: the second group ends in a hex digit and has
	// nothing after it in the pattern, so without it "index 1234567..89abcdeZ" would leave a word
	// byte on each side of the token. The first group needs none — the literal ".." pins it.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `index ([0-9a-f]{7,40})\.\.([0-9a-f]{7,40})\b`), spans: bothGroups, token: []byte(tokenSHA), class: ClassAddresses, need: []string{"index "}, perLine: true},
	// `git format-patch`: the mailbox From line.
	{re: regexp.MustCompile(`(?m)^` + lineLead + `From ([0-9a-f]{40}) `), spans: firstGroup, token: []byte(tokenSHA), class: ClassAddresses, need: []string{"From "}, perLine: true},
}

// gitCanon strips git object hashes (00-ARCHITECTURE.md §5.6).
type gitCanon struct{}

// newGit returns the git canonicalizer, fourteenth and last in §5.6's registration order.
func newGit() gitCanon { return gitCanon{} }

// Name returns "git".
func (gitCanon) Name() string { return nameGit }

// Applies reports whether tool is a shell tool or git itself: git output almost always arrives as
// the output of a Bash tool call, so restricting this to a literal "git" tool name would make the
// canonicalizer unreachable in practice.
func (gitCanon) Applies(tool, _ string) bool { return toolIn(tool, gitTools) }

// Matches reports every object hash gitRules finds.
func (gitCanon) Matches(in []byte, _ Options) []Match {
	return ruleMatches(in, gitRules)
}

// Canonicalize composes this canonicalizer's own matches exactly as Registry.Run would.
func (c gitCanon) Canonicalize(in []byte, o Options) (Result, error) {
	return canonicalizeVia(c, c.Name(), in, o)
}
