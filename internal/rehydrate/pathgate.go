package rehydrate

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
)

// Section 6's pointers never show a path the host currently refuses or a path outside the project
// (owner decision D50, C4.6; UAT-12 F4). re_read refuses a path the host's permission rules deny or
// ask about, and withholds one outside the project; the rehydration payload is another way the same
// path reaches the model, so it follows the same two rules and points by hash instead. D50 covers
// pointers: file and tool pointers, their summaries, and section 7's drop entries (D60(i)), except
// the drop entries that carry the model's own text (already_tried targets, open questions).
//
// Containment is decided here, against Request.ProjectRoot. The host's rules are the daemon's to
// load (this package may not import hostperm): Deps.HostPaths hands Build one snapshot of them for
// the whole build.
//
// A tool pointer's summary is the call's arguments as the store previews them (store.argsPreview):
// the values of file_path, path, pattern, command and url joined by spaces, or the canonical JSON of
// a call that has none of them, cut at 120 bytes with `…`. Coordinator decision D63 (ADR 0011 §23)
// judges it in two halves, after three review rounds proved that modelling every shell's quoting
// cannot be made complete:
//
//   - A STRUCTURED value — a summary that is one path argument (a Read's, Write's or Edit's
//     file_path, a lone Glob or Grep argument: one word once the project root's own spelling is held
//     together; or the one path-named value of a canonical-JSON preview; a Glob preview of a
//     directory under the root, rootGlobWithheld; or the path part of a summary that starts at the
//     root and goes on below it with a space) — is judged WHOLE, as a file pointer's path is: the
//     host's rules and containment, one path per summary, once per build (valueWithheld,
//     pathNamedWithheld). Containment reads a glob as a glob (globClimbs, classReading) and a `file:`
//     URL as outside. Several path-named values, and a cut one among them, ask the host nothing:
//     containment and the screen judge them, and a glob among them is judged by what it selects
//     (globSelectsKnown). Every path-named value is also judged, with no host judgement, for a path
//     outside the project wherever a reader starts one in it: each piece of a list, after an opening
//     quote or bracket, a `:`, `=` or `@`, and a run of punctuation that leads a piece
//     (valueNamesOutside, audit 2's finding 26). A one-word summary must ALSO pass the free-text
//     whitelist (so a one-word `$HOME/.ssh/id_rsa` can never be shown), a pattern one-word its
//     pattern rules (patternWithheld);
//     a structured value's names are whole names (screenWhole), so the project's `.env.example` is
//     not the denied `.env`, and a rooted value in one separator style is screened by the rules'
//     literals alone (screenExact), so an outside README.md never withholds the project's own.
//   - Everything else is FREE TEXT (commands, queries, prompts, URLs). It costs no host judgement. It
//     is SHOWN only when every whitespace-delimited token, and every piece of one split at a glued
//     `;`, `|`, `&&` or `||` (operatorPieces), is whitelist-safe (tokenSafe) and names no absolute or
//     escaping path in either reading of its backslashes (tokenOutside), and the text holds no Read
//     rule's literal, no withheld name and no refusing recall selector where a name starts
//     (textWithheld). Anything else — a quote outside a simple quoted run or between letters, `$`, a
//     glob or regex metacharacter, a `%` an escape, a variable or a batch parameter could use, a
//     backslash that ends a token or doubles, a home path, an absolute path, a drive or a PowerShell
//     provider drive (providerPath) or a `..` anywhere, the root after an apostrophe outside a quoted
//     run (rootInApostropheSpan), a URL character a shell splits at (urlTokenSafe), a letter or mark
//     that Windows' ANSI best-fit conversion turns into ASCII punctuation (bestFitPunct, D64), a
//     rule's literal — withholds the summary. A cut summary's last token is judged as a prefix
//     (cutTokenUnsafe, cutPrefixNamed), and one that ends right after a drive's `:` as if a name
//     followed it (cutAtDriveColon, D64(2)). The project root is held together as one unit only
//     when its own spelling has no character a shell splits or reinterprets a word at (nor one a best
//     fit turns into punctuation) and no word that starts with `-` after a space, and a sanitized text
//     spells it exactly (rootUnitAdmitted, rootSpelledExactly, D64(1) and its rulings on wave 19f).
//     A cut path-named value is the project only when it starts the root's own spelling byte for
//     byte (rootPrefix). Every judgement takes the safe side under both readings of the root (the
//     two-fold rule, ADR 0011 §23): one that decides a thing is SHOWN (containment, the root's unit, a
//     cut value's start) folds an ASCII letter's case where the platform's paths fold, and no other
//     character's (foldLiteral, asciiFoldEqual, RootRelative); one that LEARNS or WITHHOLDS (a
//     withheld path's relative names, recordedPath's hold of the root, a drop reason's screen, the
//     daemon's host adapter) also reads the root with case folded by Unicode, as filepath.Rel, RE2's
//     `(?i)` and paths.Key fold it, and learns or withholds under the union (RootRelativeBroad,
//     broadRootSpellingOf). A rule anchored outside the project is matched against the root as the
//     host matches it, its case folded as the host folds it (rootCover).
//     When the host's rules are unavailable, or a rule covers the whole project, every free text is
//     withheld, as re_read fails closed.
//
// The checkpointer's own drop entries are pointers too: five kinds are keyed by a file pointer's
// path (checkpointPathDrops), and section 7 and dropped() name a withheld one by hash
// (gateCheckpointDrops); and no drop entry's reason shows an out-of-project or withheld path
// (redactReason), screened by a simple product-string rule, since a drop reason is Qompack's own
// error prose, not a shell command. Item 6a restores no instruction file the build withholds, and
// hands the rule scanner only the pointers section 6 shows (buildRestoredInstructions); item 6b
// indexes no skill whose file the build withholds (buildSkillIndex).

// HostRules is one build's snapshot of the host's current Read rules (HostPaths).
type HostRules struct {
	// Refuses reports whether the rules deny, or ask before, reading path (as a pointer records it:
	// project-relative or absolute). Nil means the rules could not be established: every path, and
	// every free-text summary, is then withheld (re_read fails closed the same way).
	Refuses func(path string) bool
	// Patterns are the path specifiers of every Read deny and ask rule in force, as the settings spell
	// them inside Read(…) (`./private/deny.txt`, `./secrets/**`, `**/*.env`), and "" for a rule that
	// refuses every read. Free text is screened by each one's literal part (literalOf).
	Patterns []string
}

// HostPaths, when set on Deps, returns this build's snapshot of the host's current Read rules. A nil
// HostPaths applies containment alone. Build calls it once per build, and calls Refuses once for
// each distinct path among the file pointers, the structured summaries (one path each), the
// instruction and skill files items 6a and 6b would restore, and, while a rule anchored outside the
// project is in force, rootProbe (screenBy), and, while a Read rule's pattern is in force, for at
// most maxDropJudgements of the path-keyed checkpoint drops' other paths (dropJudgements), from one
// goroutine; free text never reaches it.
type HostPaths func() HostRules

// pathJudge decides, for one build, which recorded paths and summaries the payload may show.
type pathJudge struct {
	root    string
	host    bool
	refuses func(string) bool
	// rootSpelling finds the project root spelled in a text (rootSpellingOf), which holdRoot holds
	// together as one rootMark; nil when there is no root.
	rootSpelling *regexp.Regexp
	// broadSpelling finds the root in a text with case folded by Unicode (broadRootSpellingOf), the
	// broad reading a judgement that learns or withholds adds (holdRootBroad); nil when there is no
	// root or the platform's paths do not fold, where both readings are rootSpelling's.
	broadSpelling *regexp.Regexp
	// rootUnit is set when the root's own spelling admits the root unit in a summary
	// (rootUnitAdmitted, D64(1)); markRoot holds the root together only then.
	rootUnit bool
	// rootExact is set when a sanitized text can spell the root exactly (rootSpelledExactly); a drop
	// reason holds the root together only then (reasonWithheld).
	rootExact bool
	// rootSegs are the project root's segments as the host's rules match them (hostRootSegments):
	// a rule anchored outside the project is matched against them (rootCover). A cut stretch is the
	// root only in the root's own spelling (rootPrefix).
	rootSegs []string
	// screens are the rules' literals in screen form taken from a whole segment (literalOf, litWhole),
	// which a text holds where a name starts (namedAt); screenAll is set when one rule's literal cannot
	// tell texts apart (literalOf, rootCover), and then every free-text summary is withheld.
	screens   []string
	screenAll bool
	// openScreens are the literals that start a glob segment (litPrefix: `secret` for `./secret*`):
	// every name the rule refuses begins with one, so a text holds one where a name starts, whatever
	// follows it, in a structured value too.
	openScreens []string
	// midScreens are the literals taken from a glob's literal run with `*`, `?` or a class before it
	// (litMid: `.env` for `**/*.env`): every name they occur in may begin before them, so a text holds
	// one anywhere, and a cut summary's tail need not start at a boundary to begin one.
	midScreens []string
	// rulePaths are the rules' specifiers in screen form, which a cut summary's tail may begin.
	rulePaths []string
	// known holds every concrete in-project path the build withholds, in project-relative paths.Key
	// form, in the project under either reading of the root (note): a structured glob, or recall's
	// path: selector, that selects one is withheld.
	known []string
	// knownText holds the basename and the relative path of every path the build withholds (a file
	// pointer's, a path-keyed checkpoint drop's, a structured summary's) in screen form: free text
	// that holds one where a name starts names the withheld file. Nothing read from free text, and no
	// fragment the store's cut left, is ever added.
	knownText []string
	// knownPaths holds the whole spelling of every path the build withholds, in screen form with `/`
	// separators: project-relative for one in the project, as written for one outside it, and both
	// for one the two readings of the root place differently. A drop reason names a withheld path by
	// one of them (namesKnownIn).
	knownPaths []string
	// judged memoizes the host's judgement (hostRefuses) for the build: a path is named by a file
	// pointer and again by a summary, and each host judgement may consult the filesystem. It holds
	// only answers the host gave.
	judged map[string]bool
	// unjudged holds each path-keyed checkpoint drop past the drops' bound (dropJudgements), answered
	// as refused without a host judgement. Only the drops read it: newPathJudge's drop loop, which
	// learns each as withheld, and gateCheckpointDrops (dropWithheld), which section 7 and dropped()
	// read. Every other judgement of the same key still asks the host: item 6a's rule files and item
	// 6b's skill files carry the very project-relative keys a file pointer's drop does, and an answer
	// kept in judged withheld a rule or a skill the host allows whenever the session's drops of it lay
	// past the bound (wave 22's verify, fix round 2).
	unjudged map[string]bool
	// drops counts the host judgements made while newPathJudge reads the path-keyed checkpoint drops
	// (dropJudgements); nil outside a judge newPathJudge made.
	drops *dropJudgements
	// rootTail is the last segment of the root's spelling as rootSpelling matches it, its ASCII
	// letters lower-cased where the platform's paths fold: every match holds it (held's prefilter);
	// "" when the root has no last segment.
	rootTail string
	// memo is the build's memory of the screen's answers (buildMemo); nil outside a judge newPathJudge
	// made.
	memo *buildMemo
	// learned indexes known, knownText and knownPaths once learning ends (learnedIndex); nil while
	// they are short, or outside a judge newPathJudge made, and the screen then reads each list.
	learned *learnedIndex
}

// buildMemo is a judge's memory of the screen's answers for one build, shared by every copy of the
// judge (pathJudge is passed by value), as judged is (audit 2's finding 33: Build's CPU rose 2.6-2.9x
// with the D63 screen, which ran the root's expression two to four times on each text and screened
// every drop's reason, twice per build, though most share one reason). Each entry is keyed by
// everything its answer depends on besides the judge, whose root, readings of the root, rules and
// learned paths are fixed once newPathJudge returns: held by the root-spelling expression (one for
// each reading of the root, so each fold has its own entries) and the text; summaries and reasons
// by the text alone, made only when newPathJudge returns, so no answer computed while it was still
// learning withheld paths is ever kept.
type buildMemo struct {
	held      map[heldKey]string
	summaries map[string]bool
	reasons   map[string]gatedReason
}

// heldKey keys buildMemo.held: the expression that finds the root (a reading of it) and the text.
type heldKey struct {
	spelling *regexp.Regexp
	text     string
}

// gatedReason is a drop reason's screen answer (gateDropReasons): whether it shows a path the build
// withholds, and its redacted text when it does.
type gatedReason struct {
	withheld bool
	text     string
}

// dropJudgements bounds the host judgements a build makes for its path-keyed checkpoint drops (audit
// 2's finding 28, ADR 0011 §23 item 10). The checkpointer keeps every file a session touched as a
// pointer and names each one its budget cuts, so their number grows with the session, while each
// host judgement may read the disk (about a millisecond on Windows, and more under load): a thousand
// drops took a build towards the compaction answer's budget. While reading is set and a Read rule's
// pattern is in force (bounded: with none the host's rules are empty, refuse nothing and read no
// file, hostperm's RuleSet.Empty, so there is nothing to bound), hostRefuses judges at most
// maxDropJudgements fresh paths, in dropOrder's order (first the drops a text names, then the rest as
// the checkpoint lists them), and answers every later fresh path as refused without a judgement. That
// answer is the drops' own (pathJudge.unjudged): section 7 and dropped() withhold such a drop, and the
// build learns it as withheld like any refused path, whatever its spelling, so every text that names
// it is withheld too (fail closed; wave 22's verify: learning only a drop whose spelling held a rule's
// literal showed a drop the host refuses through a link, a junction or an 8.3 name wherever a text
// named it, which eca33155 withheld). It is never the build's memo of the host's answers (judged):
// every other judgement of the same path, which the project's configuration or the checkpoint's own
// budget bounds and not the drops (item 6a's rule files, item 6b's skill files, a structured summary's
// value), asks the host, so a rule or a skill the host allows is restored whatever the drops before
// it (wave 22's verify, fix round 2). The screen reads the learned paths through learnedIndex, so
// learning a long session's drops costs it a pass over each text, not one per drop.
type dropJudgements struct {
	reading, bounded bool
	made             int
}

// maxDropJudgements is the most host judgements a build makes for its path-keyed checkpoint drops
// (dropJudgements). With the daemon's adapter, which may Evaluate a path more than once, it keeps the
// drops' term near a tenth of a second on an idle Windows host, whatever the session's length.
const maxDropJudgements = 64

// screenBy adds each rule pattern's literal and specifier to the build's screens (D63(4), kept from
// D61(2)(a)). A rule with no literal refuses everything it is anchored at; a rule anchored outside
// the project is also matched against the root, segment by segment (rootCover), since its literal may
// lie in the root's own path where no project-relative spelling holds it.
//
// The host resolves what rootCover cannot: a link, a junction or an 8.3 name in the root, a rule
// written through a link. So while a rule anchored outside the project is in force and none has
// already set screenAll, the host is asked once whether it refuses rootProbe, a fresh name directly
// below the root that no rule names; when it does, a rule covers the project through another
// spelling of its root, and every free text is withheld (D63(4)). This is the build's one host
// judgement that no recorded path asks for.
func (j *pathJudge) screenBy(patterns []string) {
	outside := false
	for _, p := range patterns {
		segs, _ := ruleSegments(p)
		lit, kind := literalOf(segs)
		if lit == "" || j.coversRoot(p) {
			j.screenAll = true
			continue
		}
		outside = outside || anyAnchored(hostReadings(p))
		j.addScreen(lit, kind)
		if rp := screenText(strings.TrimPrefix(strings.TrimSpace(p), "./"), true); rp != "" {
			j.rulePaths = appendDistinct(j.rulePaths, rp)
		}
	}
	if outside && !j.screenAll && j.hostRefuses(rootProbe) {
		j.screenAll = true
	}
}

// rootProbe is the fresh name below the project root the host is asked about (screenBy): no
// extension, no leading dot and no 8.3 shape, so no rule a user writes for files names it.
const rootProbe = "qompack-rehydrate-root-probe"

// addScreen adds lit, a rule's literal in screen form, to the build's screens of its kind.
func (j *pathJudge) addScreen(lit string, kind litKind) {
	switch kind {
	case litMid:
		j.midScreens = appendDistinct(j.midScreens, lit)
	case litPrefix:
		j.openScreens = appendDistinct(j.openScreens, lit)
	default:
		j.screens = appendDistinct(j.screens, lit)
	}
}

// coversRoot matches spec, a rule's specifier, against the project root in each reading of it that is
// anchored outside the project (hostReadings, rootCover), and reports whether one may refuse the root
// or a path below it whose relative spelling need hold no literal. Every reading is matched, since
// each may add the literal of what follows the root to the screens.
func (j *pathJudge) coversRoot(spec string) bool {
	all := false
	for _, rd := range hostReadings(spec) {
		if rd.anchored && j.rootCover(rd.segs, rd.fromStart) {
			all = true
		}
	}
	return all
}

// hostReading is one reading of a rule's specifier as the host matches it against a path
// (hostReadings): its segments below its anchor, whether it is anchored outside the project (as
// ruleSegments reports it), and whether it is measured from the filesystem root (`//`, a drive).
type hostReading struct {
	segs                []string
	anchored, fromStart bool
}

// hostReadings are the readings of spec, a rule's specifier, that the host matches against a path
// (internal/hostperm: compileOne, finishPattern, prefixRow). Its raw segments: split at `/` only,
// nothing deleted, a `\` left in its segment, where path.Match reads it as an escape; its case folded
// where the platform's paths fold, as paths.Key and hostperm both fold it, by Unicode lower-casing;
// `..` resolved lexically (cleanLiteral). And when spec holds a backslash, the same with every
// backslash read as a separator: hostperm's alias of such a rule on Windows, read on every platform
// as ruleSegments has always read it, since a reading can only add screens. The home anchor is where
// the rule is measured from, not a segment, as in ruleSegments.
func hostReadings(spec string) []hostReading {
	s := paths.Key(strings.TrimSpace(spec))
	out := []hostReading{hostReadingOf(s)}
	if strings.Contains(s, `\`) {
		out = append(out, hostReadingOf(strings.ReplaceAll(s, `\`, "/")))
	}
	return out
}

// hostReadingOf is one reading of s, a folded specifier, split at its `/` (hostReadings).
func hostReadingOf(s string) hostReading {
	home := s == "~" || strings.HasPrefix(s, "~/")
	rd := hostReading{fromStart: strings.HasPrefix(s, "//") || driveSpelling.MatchString(s)}
	rd.anchored = rd.fromStart || home || strings.HasPrefix(s, "/") || strings.HasPrefix(path.Clean(s), "..")
	for i, seg := range strings.Split(s, "/") {
		switch {
		case seg == "" || seg == "." || (i == 0 && home):
		case seg == "..":
			if len(rd.segs) > 0 {
				rd.segs = rd.segs[:len(rd.segs)-1]
			}
		default:
			rd.segs = append(rd.segs, seg)
		}
	}
	return rd
}

// anyAnchored reports whether any of readings is anchored outside the project.
func anyAnchored(readings []hostReading) bool {
	for _, rd := range readings {
		if rd.anchored {
			return true
		}
	}
	return false
}

// hostRootSegments is root as the host's rules are matched against it (internal/hostperm's
// posixSegments): on Windows without a `\\?\` prefix, cleaned, slash-separated, each segment as
// written, with nothing deleted, and its case folded where the platform's paths fold, by Unicode
// lower-casing (paths.Key) as hostperm folds both a path and a rule: a rule spelled with the Kelvin
// sign (U+212A) for the root's `k` refuses the project at the host, so the screen must read it so
// too, though the root's own spelling folds ASCII letters only (RootRelative). A drive stays `c:`,
// which segMatch reads a rule's `c` as.
func hostRootSegments(root string) []string {
	r := root
	if runtime.GOOS == "windows" {
		r = strings.TrimPrefix(strings.TrimPrefix(r, `\\?\UNC\`), `\\?\`)
	}
	key := paths.Key(filepath.ToSlash(filepath.Clean(r)))
	return strings.FieldsFunc(key, func(c rune) bool { return c == '/' })
}

// rootCover matches segs, one reading of a rule anchored outside the project (below its anchor,
// hostReadings), against the project root's segments (hostRootSegments), from the filesystem root
// when fromStart and from any of them otherwise (a home or settings anchor is not known here, so
// every alignment is assumed). It reports true when the rule may refuse the root itself, or a path
// below it whose relative spelling need hold no literal: segs end, or end in a wholly unliteral glob,
// at or above the root (`//c/Users/me/**`, `~/config/**` for a project under them). When the rule
// may refuse project paths through what follows the root (`~/Documents/**/*.key` for a project under
// Documents), the literal that part spells, in screen form, is added to the screens. A rule whose
// literal merely occurs in the root's path, segment or substring, and that refuses nothing in the
// project (`~/Documents/*.pdf`, `~/.kube/config` beside `config-service`, `//etc/**` beside
// `fetcher`), adds nothing.
func (j *pathJudge) rootCover(segs []string, fromStart bool) (all bool) {
	root := j.rootSegs
	seen := make(map[[2]int]bool)
	var walk func(si, ri int)
	walk = func(si, ri int) {
		if all || seen[[2]int{si, ri}] {
			return
		}
		seen[[2]int{si, ri}] = true
		switch {
		case ri == len(root):
			// The root is matched: what is left applies to project-relative paths.
			lit, kind := literalOf(screenSegments(segs[si:]))
			if lit == "" {
				all = true
				return
			}
			j.addScreen(lit, kind)
		case si == len(segs):
			// The rule names a directory the root is in, and so everything in the project.
			all = true
		case segs[si] == "**":
			walk(si+1, ri)
			walk(si, ri+1)
		case segMatch(segs[si], root[ri]):
			walk(si+1, ri+1)
		}
	}
	if fromStart {
		walk(0, 0)
		return all
	}
	for i := 0; i <= len(root); i++ {
		walk(0, i)
	}
	return all
}

// screenSegments is segs, a rule's raw segments, each in screen form (screenText), as literalOf
// reads them.
func screenSegments(segs []string) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = screenText(s, true)
	}
	return out
}

// segMatch reports whether a rule's raw segment pat may match the root's raw segment seg, both folded
// as the host folds them: as the host matches them, by path.Match (wave 19g's final verify of D64: a
// `?` or a negated class for a quote, a backtick, a caret or a backslash in the root's name, and an
// escape `\'`, match where the host matches them), or as a drive's POSIX spelling (`c` for `c:`). A
// pattern path.Match cannot read matches. So do the two in screen form (screenText), as rootCover
// matched them before: that reading deletes ' " ` \ ^ and so matches more, never less, and can only
// add screens.
func segMatch(pat, seg string) bool {
	return globSegMatch(pat, seg) || globSegMatch(screenText(pat, true), screenText(seg, true))
}

// globSegMatch reports whether pat matches seg as a glob segment, as a drive's POSIX spelling, or
// as a pattern path.Match cannot read (segMatch).
func globSegMatch(pat, seg string) bool {
	if pat == seg || (len(seg) == 2 && seg[1] == ':' && pat == seg[:1]) {
		return true
	}
	ok, err := path.Match(pat, seg)
	return ok || err != nil
}

// newPathJudge is the build's judge. It must read r's checkpoint drop entries before
// gateCheckpointDrops withholds their paths: a withheld path the checkpoint records only as a drop
// (a pointer_missing file, a file_pointer cut at the checkpoint's budget) is still a path free text,
// a glob or a selector may name (w19 verifier V3).
func newPathJudge(r Request, d Deps) pathJudge {
	j := pathJudge{
		root: r.ProjectRoot, judged: make(map[string]bool), unjudged: make(map[string]bool),
		drops: &dropJudgements{}, rootSpelling: rootSpellingOf(r.ProjectRoot), broadSpelling: broadRootSpellingOf(r.ProjectRoot),
		rootUnit: rootUnitAdmitted(r.ProjectRoot), rootExact: rootSpelledExactly(r.ProjectRoot), rootTail: rootTailOf(r.ProjectRoot),
		memo: &buildMemo{held: make(map[heldKey]string)},
	}
	if r.ProjectRoot != "" {
		j.rootSegs = hostRootSegments(r.ProjectRoot)
	}
	if d.HostPaths != nil {
		j.host = true
		h := d.HostPaths()
		j.refuses = h.Refuses
		if h.Refuses != nil {
			j.screenBy(h.Patterns)
		}
		j.drops.bounded = len(h.Patterns) > 0
	}
	for _, f := range r.Checkpoint.Pointers.Files {
		if j.withheld(f.Path) {
			j.note(f.Path)
		}
	}
	// The drops' host judgements are bounded (dropJudgements); a file pointer's path among them was
	// judged above and costs nothing more. The drops a text names are judged first (dropOrder), and a
	// drop past the bound is withheld unjudged (unjudged) and learned as withheld like any refused path,
	// so every text that names it is withheld (fail closed).
	j.drops.reading = true
	for _, e := range dropOrder(r.Checkpoint, j) {
		if j.withheld(e.ID) {
			j.note(e.ID)
		}
	}
	j.drops.reading = false
	for _, t := range r.Checkpoint.Pointers.Tools {
		for _, v := range j.notedValues(t.Summary) {
			if !j.recordedPath(v) {
				continue
			}
			// A path outside the project is withheld by containment, so it is learned without a host
			// judgement; an in-project one asks the host whether it is denied, counted once per build.
			// Only what containment or the host withholds is learned: a value the whitelist alone
			// withholds (a `..` that stays in the project) names no withheld file.
			if j.withheld(v) {
				j.note(v)
			}
		}
	}
	// Answers that depend on the withheld paths learned above are memoized, and the paths indexed, only
	// now that learning ends.
	j.memo.summaries = make(map[string]bool)
	j.memo.reasons = make(map[string]gatedReason)
	j.learned = newLearnedIndex(j.known, j.knownText, j.knownPaths)
	return j
}

// dropOrder is cp's path-keyed drop entries in the order newPathJudge asks the host about them: while
// the drops' judgements are bounded (dropJudgements) and they outnumber the bound, first each one a
// text the build screens names by its basename or its path (textNames, dropNamed), then the rest,
// each group in checkpoint order; otherwise checkpoint order. A drop past the bound is withheld
// unjudged and learned as withheld, so every text that names it is withheld (fail closed); judging
// first the drops a text names keeps that over-withholding off the texts the payload shows, and a
// named drop the host allows is shown, in section 6 and in section 7, as eca33155 showed it (wave
// 22's verify of finding 28).
func dropOrder(cp checkpoint.Checkpoint, j pathJudge) []checkpoint.DropEntry {
	var drops []checkpoint.DropEntry
	for _, e := range cp.Dropped {
		if checkpointPathDrops[e.Kind] {
			drops = append(drops, e)
		}
	}
	if j.drops == nil || !j.drops.bounded || len(drops) <= maxDropJudgements {
		return drops
	}
	names := textNames(cp)
	first := make([]checkpoint.DropEntry, 0, len(drops))
	var rest []checkpoint.DropEntry
	for _, e := range drops {
		if j.dropNamed(e.ID, names) {
			first = append(first, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(first, rest...)
}

// textNames is the set of names the texts a build screens spell (dropOrder): each tool summary and
// each checkpoint drop's reason but the model's own (modelTextDrops), in screen form with every `\`
// a separator and, for a canonical-JSON preview, each of its decoded strings too, each distinct text
// read once (most drops share one reason), split at every ASCII character a file name rarely holds,
// each word with its trailing dots dropped and with each of its tails after a `/`. It only orders
// the drops' judgements, so a name it misses costs over-withholding, never a path shown.
func textNames(cp checkpoint.Checkpoint) map[string]bool {
	names := make(map[string]bool)
	seen := make(map[string]bool)
	add := func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		form := screenText(strings.ReplaceAll(sanitize(t), `\`, "/"), false)
		for _, w := range strings.FieldsFunc(form, notNameRune) {
			for w = strings.TrimRight(w, "."); w != ""; {
				names[w] = true
				k := strings.IndexByte(w, '/')
				if k < 0 {
					break
				}
				w = w[k+1:]
			}
		}
	}
	for _, tp := range cp.Pointers.Tools {
		add(tp.Summary)
		if jsonShaped(tp.Summary) {
			strs, _ := jsonStrings(tp.Summary)
			for _, s := range strs {
				add(s.value)
			}
		}
	}
	for _, e := range cp.Dropped {
		if !modelTextDrops[e.Kind] {
			add(e.Detail)
		}
	}
	return names
}

// notNameRune reports an ASCII character other than a letter, a digit or one of `. _ - ~ $ % # + @ /`
// (textNames).
func notNameRune(r rune) bool {
	return r < utf8.RuneSelf && !asciiLetter(byte(r)) && !(r >= '0' && r <= '9') && !strings.ContainsRune("._-~$%#+@/", r)
}

// dropNamed reports whether id, a path-keyed drop's path, is in names (textNames) by its basename,
// its project-relative path or its whole cleaned spelling, in screen form.
func (j pathJudge) dropNamed(id string, names map[string]bool) bool {
	p := judgedSpelling(id)
	slash := strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if names[screenText(path.Base(slash), true)] || names[screenText(path.Clean(slash), true)] {
		return true
	}
	// A relative path's key is its cleaned spelling, asked above.
	if !absLike(p) {
		return false
	}
	k, ok := j.key(p)
	return ok && names[screenText(k, true)]
}

// notedValues are the structured path values of summary the build judges WHOLE, and so may learn to
// withhold: the single path-named value of a canonical-JSON preview (a multi-valued preview costs no
// host judgement, D63, so it is not learned here), or any other uncut summary, which newPathJudge
// learns only when it is one word (recordedPath rejects a value with a space once the root is held
// together, so a rooted summary with a space, judged by the host, is never a withheld name). A cut
// summary teaches nothing (its value is only a prefix).
func (j pathJudge) notedValues(summary string) []string {
	t := strings.TrimSpace(summary)
	if t == "" {
		return nil
	}
	if jsonShaped(t) {
		strs, grammatical := jsonStrings(t)
		if !grammatical {
			return nil
		}
		var pv []string
		for _, s := range strs {
			if s.pathNamed && !s.cut {
				pv = append(pv, strings.TrimSpace(s.value))
			}
		}
		if len(pv) == 1 {
			return pv
		}
		return nil
	}
	if body, cut := strings.CutSuffix(t, previewEllipsis); !cut {
		return []string{body}
	}
	return nil
}

// recordedPath reports whether v, a structured value the build withholds, names a path whose names
// the screen may learn: concrete (not a glob), rooted or holding a separator, one word with no space
// once the root is held together, and not a value led by one separator with no drive that names no
// path on this platform (a slash command `/review`, a route `/api/v1/users`, a backslash-led pattern
// `\.test\.ts`), nor a URL (http, https or file). Learning the names of any other value would withhold
// unrelated free text with a fragment (w19c reviews): a git revision such as HEAD~1, a stretch of a
// command, a Grep preview of a directory then its pattern. The recorded files not learned are those
// whose path has a space, which a rule's literal screens instead (item 7(a)). The root is held
// together here whatever it holds (holdRoot), even when no text spells it exactly: learning a
// withheld path's names only ever withholds more, so neither D64(1)'s root unit, which decides what
// a summary may show, nor rootSpelledExactly, which decides how a reason is read, narrows it (a
// withheld path under a root `a  b` would otherwise keep its space and go unlearned). For the same
// reason it is held under either reading of the root (the two-fold rule): its own spelling with an
// ASCII letter's case folded, or its spelling with case folded by Unicode (holdRootBroad), so a value
// under `C:\q\Åsa berg\proj`, the project to the host for a root `C:\q\åsa berg\proj`, is learned.
func (j pathJudge) recordedPath(v string) bool {
	if isGlob(v) || isURL(v) || fileScheme(v) {
		return false
	}
	if !(absLike(v) || strings.ContainsAny(v, `/\`)) {
		return false
	}
	if t := strings.TrimSpace(v); len(t) > 0 && isSep(t[0]) && (len(t) == 1 || !isSep(t[1])) && !driveLessPath(t) {
		return false
	}
	s := sanitize(v)
	return !strings.Contains(j.holdRoot(s), " ") || !strings.Contains(j.holdRootBroad(s), " ")
}

// driveLessPath reports whether v, a value led by one separator, is a path on this platform: a POSIX
// absolute path of two segments or more. On Windows no tool takes a drive-less path, so none is; on
// POSIX a backslash-led word is a name, not a path, and a single segment (`/review`) is a flag, a
// slash command or a route as often as a directory.
func driveLessPath(v string) bool {
	return runtime.GOOS != "windows" && v[0] == '/' && hasSecondSegment(v[1:])
}

// hasSecondSegment reports whether rest, a POSIX absolute path less its leading separator, names a
// first segment and then a second.
func hasSecondSegment(rest string) bool {
	k := strings.IndexAny(rest, `/\`)
	return k > 0 && strings.Trim(rest[k:], `/\`) != ""
}

// note records p, a path the build withholds, as known: its relative key when it is inside the
// project and concrete, its basename and relative path for the free-text screen, and its whole
// spelling for the drop-reason screen. Learning only ever withholds more, so p is learned under both
// readings of the root (the two-fold rule): as the strict reading places it (key: an ASCII letter's
// case folded where paths fold), which withholds every path it finds outside the project; and, when
// that finds p outside but the broad reading finds it inside (broadKey: case folded by Unicode, as
// filepath.Rel and the host's lower-casing fold it) and withholds it there too (the host refuses it,
// or its rules could not be established), by its project-relative names as well.
// `C:\q\Åsa\proj\private\deny.txt` under a root `C:\q\åsa\proj` is a directory beside the project to
// the strict reading, which withholds it, and the project's denied private/deny.txt to the host and
// NTFS, so a glob that selects private/deny.txt is withheld; `C:\q\Åsa\proj\src\main.go`, which the
// host allows, teaches no project-relative name.
func (j *pathJudge) note(p string) {
	p = judgedSpelling(p)
	if p == "" {
		return
	}
	slash := strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if base := path.Base(slash); namesAFile(base) && len(base) >= minCutPrefix {
		// A basename of a byte or two (`b`, `id`) occurs where a name starts in most texts and names
		// nothing; the relative path, below, still counts.
		j.knownText = appendDistinct(j.knownText, screenText(base, true))
	}
	k, inside := j.key(p)
	j.learn(p, slash, k, inside)
	if kb, ok := j.broadKey(p); ok && !inside && j.hostRefuses(p) {
		j.learn(p, slash, kb, true)
	}
}

// learn records p (slash: p with `/` separators, no trailing one), a path the build withholds, under
// one reading of the root: by its relative key k when that reading places it inside the project, and
// by its whole spelling otherwise (note).
func (j *pathJudge) learn(p, slash, k string, inside bool) {
	if inside && !isGlob(k) {
		j.known = appendDistinct(j.known, k)
	}
	switch {
	case inside && k != ".":
		j.knownText = appendDistinct(j.knownText, screenText(k, true))
		j.knownPaths = appendDistinct(j.knownPaths, screenText(k, true))
	case !inside && !absLike(p) && namesAFile(path.Base(path.Clean(slash))):
		j.knownText = appendDistinct(j.knownText, screenText(path.Clean(slash), true))
		j.knownPaths = appendDistinct(j.knownPaths, screenText(path.Clean(slash), true))
	case !inside && namesAFile(path.Base(slash)):
		j.knownPaths = appendDistinct(j.knownPaths, screenText(path.Clean(slash), true))
	}
}

// namesAFile reports whether base, a path's last segment, names an entry rather than only where a
// path starts or climbs (`.`, `..`, `/`, a bare drive `D:`, `~`, `$HOME`, `%USERPROFILE%`): an
// anchor alone is in countless texts that name no withheld file, and would withhold them all.
func namesAFile(base string) bool {
	switch {
	case base == "" || base == "." || base == ".." || base == "/":
		return false
	case len(base) == 2 && driveSpelling.MatchString(base):
		return false
	}
	return homeOrVarRoot.FindString(base) != base
}

// appendDistinct appends s to set unless it is empty or, while set holds at most distinctScanMax
// entries, already there. A longer set is a learned list a long session grew (a drop past the host's
// bound is learned whatever its spelling), whose readers ask only whether some entry matches and read
// it through the build's learnedIndex, which holds each entry once: a scan on every append made
// learning quadratic in the drops (wave 22's verify of audit 2's finding 28).
func appendDistinct(set []string, s string) []string {
	if s == "" {
		return set
	}
	if len(set) > distinctScanMax {
		return append(set, s)
	}
	for _, e := range set {
		if e == s {
			return set
		}
	}
	return append(set, s)
}

// distinctScanMax is the longest set appendDistinct scans for a repeat; it is past learnedIndexMin,
// so every list that may hold a repeat is read through the index.
const distinctScanMax = 2 * learnedIndexMin

// pathJudgeFor is the judge Build made for d's build, or a new one for r when the item is built on
// its own.
func pathJudgeFor(r Request, d Deps) pathJudge {
	if d.judge != nil {
		return *d.judge
	}
	return newPathJudge(r, d)
}

// rulesUnavailable reports that host rules are in force but could not be established.
func (j pathJudge) rulesUnavailable() bool { return j.host && j.refuses == nil }

// withheld reports whether path may not be shown: it lies outside the project, or the host's rules
// refuse it, or they could not be established.
func (j pathJudge) withheld(path string) bool {
	p := judgedSpelling(path)
	return p != "" && (!j.inside(p) || j.hostRefuses(p))
}

// judgedSpelling is path as withheld() judges it: JSON's doubled backslash undone, and trimmed.
func judgedSpelling(path string) string {
	return strings.TrimSpace(strings.ReplaceAll(path, `\\`, `\`))
}

// hostRefuses reports whether the host's rules refuse p (a judgedSpelling), or could not be
// established; with no host rules in force it is false. Each answer the host gives is memoized for
// the build (judged). While newPathJudge reads the drops past their bound, p is answered as refused
// without a judgement, and that answer is kept for the drops alone (unjudged, dropWithheld).
func (j pathJudge) hostRefuses(p string) bool {
	if !j.host {
		return false
	}
	if j.refuses == nil {
		return true
	}
	if w, ok := j.judged[p]; ok {
		return w
	}
	if j.drops != nil && j.drops.reading && j.drops.bounded {
		if j.drops.made >= maxDropJudgements {
			// Past the drops' bound: refused without a judgement (fail closed), an answer only the
			// drops read.
			j.unjudged[p] = true
			return true
		}
		j.drops.made++
	}
	w := j.refuses(p)
	if j.judged != nil {
		j.judged[p] = w
	}
	return w
}

// inside reports whether p, absolute or project-relative, names something within the project. A glob
// whose segment may match `..` through glob syntax (globClimbs), or that names a path outside the
// project once its classes are read as what they nearly spell (classReading), is not: containment
// cleans p as a literal path, while Qompack's own matcher, a shell, a glob library or the model reads
// it as a pattern. An absolute p is within the project when it is the root or below it as
// RootRelative compares them, an ASCII letter's case folded where paths fold and no other: a
// judgement that shows takes the strict reading of the root (the two-fold rule), and every caller
// withholds what inside() finds outside.
func (j pathJudge) inside(p string) bool { return j.insideBy(p, RootRelative) }

// insideBy is inside() with an absolute p placed by rel, one reading of the root (RootRelative, or
// RootRelativeBroad for a judgement that learns, broadKey).
func (j pathJudge) insideBy(p string, rel func(root, p string) (string, bool)) bool {
	if globClimbs(p) {
		return false
	}
	if q := classReading(p); q != p && !j.insideBy(q, rel) {
		return false
	}
	if !absLike(p) {
		c := filepath.Clean(filepath.FromSlash(p))
		return c != ".." && !strings.HasPrefix(c, ".."+string(filepath.Separator))
	}
	_, ok := rel(j.root, p)
	return ok
}

// RootRelative is p, an absolute path, relative to root (`.` for root itself), and true; or false
// when p is not root or below it, or root is empty. Both are cleaned (p's `/` read as the platform's
// separator) and compared byte for byte, with an ASCII letter's case folded where the platform's paths
// fold (asciiFoldEqual: Windows and macOS) and no other character folded. filepath.Rel, which
// containment used, folds by Unicode on Windows, so a root spelled with the Kelvin sign (U+212A), the
// long s (U+017F) or the Angstrom sign (U+212B) where the project's has `k`, `s` or `å` was the
// project, though NTFS keeps it a directory beside it (wave 19g's final verify of D64); and on macOS
// it folded nothing, while the root's spelling (rootSpellingOf) and its cut prefix (rootPrefix) fold
// ASCII letters there. It is the strict reading of the root, which a judgement that shows takes; a
// judgement that learns or withholds adds the broad one (RootRelativeBroad).
func RootRelative(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	r, q := filepath.Clean(root), filepath.Clean(filepath.FromSlash(p))
	if len(q) < len(r) || !asciiFoldEqual(q[:len(r)], r, paths.DefaultFold()) {
		return "", false
	}
	switch rest := q[len(r):]; {
	case rest == "":
		return ".", true
	case r[len(r)-1] == filepath.Separator:
		return rest, true
	case rest[0] == filepath.Separator:
		return rest[1:], true
	}
	return "", false
}

// RootRelativeBroad is RootRelative under the broad reading of the root (the two-fold rule, ADR 0011
// §23): where the platform's paths fold, p is also the root or below it when each of root's cleaned
// path elements equals p's under Unicode's simple case folding (strings.EqualFold, as filepath.Rel
// and RE2's `(?i)` fold on Windows) or once both are lower-cased by Unicode (paths.Key, as the host's
// rules fold a path). Where paths do not fold it is RootRelative. It places more paths in the project
// than any of those readings alone, and so may only decide what is learned or withheld, never what is
// shown: `C:\q\Åsa\proj\x` is below a root `C:\q\åsa\proj` (NTFS and the host fold U+00C5 onto
// U+00E5), and so are the root's spellings with the Kelvin sign, the long s and the Angstrom sign,
// which NTFS keeps beside it, and with U+0130 for `i`, which only the host's lower-casing folds. The
// daemon's host adapter judges each reading's place below the resolved root.
func RootRelativeBroad(root, p string) (string, bool) {
	if rel, ok := RootRelative(root, p); ok || root == "" || !paths.DefaultFold() {
		return rel, ok
	}
	rs, qs := pathElems(filepath.Clean(root)), pathElems(filepath.Clean(filepath.FromSlash(p)))
	if len(qs) < len(rs) {
		return "", false
	}
	for i, e := range rs {
		if !strings.EqualFold(e, qs[i]) && paths.Key(e) != paths.Key(qs[i]) {
			return "", false
		}
	}
	if len(qs) == len(rs) {
		return ".", true
	}
	return strings.Join(qs[len(rs):], string(filepath.Separator)), true
}

// pathElems is c, a cleaned path, split at the platform's separator, with the empty element a root
// directory's trailing separator leaves (`C:\`, `/`) dropped.
func pathElems(c string) []string {
	e := strings.Split(c, string(filepath.Separator))
	if len(e) > 1 && e[len(e)-1] == "" {
		e = e[:len(e)-1]
	}
	return e
}

// globClimbs reports whether p, read as a glob, may match a `..` segment its spelling does not show
// (D63(3)): a segment that path.Match matches against `..` and that holds a class, a `?` or a
// backslash escape (`[.][.]`, `?.`, `.?`, `\.\.`), or starts with an explicit `.` (`.*`, `.[.]`: a
// shell without globskipdots matches either against `..`), in either reading of p's backslashes. A
// segment of stars and literals that does not start with a dot (`*`, `**`, `*.*`) is not counted:
// path.Match lets a star match `..`, but no directory walker yields `..`, and a shell's star never
// matches a leading dot. A segment path.Match cannot read counts when it is all dots and glob syntax.
func globClimbs(p string) bool {
	if !strings.ContainsAny(p, `*?[\`) {
		return false
	}
	for _, r := range []string{strings.ReplaceAll(p, `\`, "/"), p} {
		for _, seg := range strings.Split(r, "/") {
			escaped := strings.ContainsAny(seg, `?[\`) || strings.HasPrefix(seg, ".")
			if !strings.ContainsAny(seg, `*?[\`) || !escaped {
				continue
			}
			ok, err := path.Match(seg, "..")
			if ok || (err != nil && strings.Trim(seg, `.*?[]!^\`) == "") {
				return true
			}
		}
	}
	return false
}

// classReading is p with each glob class read as what it most nearly spells: a class of one character
// as that character (`[.]` is `.`, `[~]` is `~`), and a class that may match a separator (`[/]`,
// `[^a]`, `[!a]`; path.Match, which Qompack's selectors use, lets a class match `/`) as `/`. Every
// other class is left as written. Containment, the escaping checks and the name screen read a glob
// both as written and so (D63(3)): `[/]etc[/]passwd` is /etc/passwd, `de[n]y.txt` is deny.txt.
func classReading(p string) string {
	if !strings.Contains(p, "[") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); {
		end := classEnd(p, i)
		if p[i] != '[' || end < 0 {
			b.WriteByte(p[i])
			i++
			continue
		}
		class := p[i : end+1]
		if strings.HasPrefix(class, "[!") {
			class = "[^" + class[2:]
		}
		members := strings.ReplaceAll(class[1:len(class)-1], `\`, "")
		sep, _ := path.Match(class, "/")
		bsep, _ := path.Match(class, `\`)
		switch {
		case sep || bsep:
			b.WriteByte('/')
		case len(members) == 1 && members != "^":
			b.WriteString(members)
		default:
			b.WriteString(p[i : end+1])
		}
		i = end + 1
	}
	return b.String()
}

// classEnd is the index of the `]` that closes the glob class opened at p[i], or -1: a `]` first in
// the class, or after its `^` or `!`, is a member, and a backslash escapes the byte after it.
func classEnd(p string, i int) int {
	if i >= len(p) || p[i] != '[' {
		return -1
	}
	j := i + 1
	if j < len(p) && (p[j] == '^' || p[j] == '!') {
		j++
	}
	if j < len(p) && p[j] == ']' {
		j++
	}
	for ; j < len(p); j++ {
		switch p[j] {
		case '\\':
			j++
		case ']':
			return j
		}
	}
	return -1
}

// absLike reports whether p is rooted in any spelling a pointer may carry: the platform's absolute
// form, a POSIX root, a Windows drive (absolute, or drive-relative: `D:secret.txt` is secret.txt in
// drive D's current directory) or a UNC share — whichever platform recorded it — a path spelled from
// the home directory or an environment variable (homeOrVarRoot), or a `file:` URL (fileScheme).
func absLike(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
		driveLetter(p) || homeOrVarRooted(p) || fileScheme(p)
}

// homeOrVarRooted reports whether p starts at a home or a variable (homeOrVarRoot). Every spelling
// the expression matches starts with `~`, `$`, `%` or `!`, so it runs only on such a p (absLike is
// on the screen's hot path: wave 22's verify of audit 2's findings 28 and 33).
func homeOrVarRooted(p string) bool {
	return p != "" && strings.IndexByte("~$%!", p[0]) >= 0 && homeOrVarRoot.MatchString(p)
}

// fileScheme reports whether p starts a `file:` URL, which names a path on some host's filesystem
// that the project root never anchors (D63(3)).
func fileScheme(p string) bool { return len(p) >= 5 && strings.EqualFold(p[:5], "file:") }

// driveSpelling matches a path that starts at a Windows drive, absolute or drive-relative.
var driveSpelling = regexp.MustCompile(`^[A-Za-z]:`)

// homeOrVarRoot matches a path that starts at a shell's home directory (`~`, `~user/`) or at an
// environment variable (`$VAR`, `${VAR}`, PowerShell's `$env:VAR` and `${env:VAR}`, `%VAR%`, cmd.exe's
// delayed `!VAR!`). Whatever it expands to, it is not a path the project root anchors, and read as
// project-relative it would be joined under the root, where a host deny rule on ~/.ssh/** never
// matches it. So it counts as rooted, and inside() finds it outside the project. `~` must end the
// path or be followed by a user name and a separator, so a project file named like an editor's lock
// file (`~$report.docx`) stays project-relative.
var homeOrVarRoot = regexp.MustCompile(
	`^(~[A-Za-z0-9._-]*([\\/]|$)|\$\{?[A-Za-z_][A-Za-z0-9_]*|%[A-Za-z_][A-Za-z0-9_()]*%|![A-Za-z_][A-Za-z0-9_]*!)`)

// summaryWithheld reports whether a tool pointer's summary may not be shown (D63; the file's header).
// A canonical-JSON preview is judged value by value (jsonWithheld); a one-word summary as a
// structured value and as free text (oneWordWithheld); a Glob preview of a directory under the root
// as one structured glob (rootGlobWithheld); every other summary is free text, and one that starts at
// the project root and goes on below it with a space also has its path part judged by the host
// (rootStretch) once the free text has passed, so the host is never asked about a summary the
// whitelist withholds. Each distinct summary is judged once per build (buildMemo.summaries).
func (j pathJudge) summaryWithheld(s string) bool {
	if j.memo != nil && j.memo.summaries != nil {
		if w, ok := j.memo.summaries[s]; ok {
			return w
		}
		w := j.judgeSummary(s)
		j.memo.summaries[s] = w
		return w
	}
	return j.judgeSummary(s)
}

// judgeSummary is summaryWithheld's judgement of s, unmemoized.
func (j pathJudge) judgeSummary(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if jsonShaped(t) {
		return j.jsonWithheld(t)
	}
	body, cut := strings.CutSuffix(t, previewEllipsis)
	marked := j.markRoot(sanitize(body))
	toks := splitTokens(marked)
	switch {
	case len(toks) == 0:
		return false
	case len(toks) == 1 && !cut:
		return j.oneWordWithheld(body)
	case len(toks) == 2 && !cut && rootGlobShape(toks):
		return j.rootGlobWithheld(marked, toks)
	}
	if j.textWithheld(body, cut, screenFree) {
		return true
	}
	v, through, ok := j.rootStretch(toks)
	switch {
	case !ok:
		return false
	case cut && through == len(toks)-1:
		// The stretch runs into the token the store's cut fell inside: only its directory is a path.
		return j.cutValueWithheld(v, true)
	}
	return j.valueWithheld(v)
}

// oneWordWithheld judges a one-word summary (a single token once the project root is held together).
// An http(s) URL (a WebFetch's preview) is free text, never a path, and asks the host nothing. A
// pattern (a glob or a brace list: a lone Glob or recall argument) is judged by patternWithheld, then
// by the host once. Every other one-word summary must pass the free-text whitelist AND be judged
// whole as the one file a Read, Write or Edit names (so `$HOME/.ssh/id_rsa` can never be shown, D63).
// A one-word value is one path, whose names end where its segments end, so the name screen reads
// whole names in it (screenWhole: the project's own `.env.example` is not the denied `.env`); and a
// rooted value spelled in one separator style that holds no other path (oneValuePath) is the exact
// path the host judges, so only the rules' literals screen it (screenExact: an outside README.md never
// withholds the project's own). A word that glues a second path on (`<root>/a.go;lnk/token.txt`) is
// not that path: the host judged the whole word, and its pieces are screened by the withheld names
// too (candidate 8's diff verify, finding 9).
func (j pathJudge) oneWordWithheld(body string) bool {
	switch {
	case isURL(body):
		return j.textWithheld(body, false, screenFree)
	case isPattern(body):
		marked := j.markRoot(sanitize(body))
		toks := splitTokens(marked)
		if len(toks) != 1 || j.patternWithheld(marked, toks[0], "") {
			return true
		}
		return j.valueWithheld(body)
	}
	mode := screenWhole
	if j.exactRooted(body) && j.oneValuePath(body) {
		mode = screenExact
	}
	if j.textWithheld(body, false, mode) {
		return true
	}
	return j.valueWithheld(body)
}

// exactRooted reports whether v, a structured value the host judges whole, is the project root
// followed by a path spelled in one separator style (only `/`, or, on Windows, only `\`): the host,
// cmd.exe, PowerShell and a POSIX shell then read the same names after the root, so a name the build
// withholds elsewhere (an outside README.md) names another file, and only the rules' literals can
// tell its names from a refused file's (screenExact; ADR 0011 §23 item 6). A mixed spelling
// (`<root>/private/de\ny.txt`, deny.txt to a POSIX shell) is not exact. It reads only how v starts
// and which separators it holds; whether v is one path is oneValuePath's to say, and screenExact
// needs both.
func (j pathJudge) exactRooted(v string) bool {
	p := judgedSpelling(v)
	marked := j.markRoot(sanitize(p))
	if marked == "" || marked[0] != rootMark || (len(marked) > 1 && !isSep(marked[1])) {
		return false
	}
	if runtime.GOOS == "windows" {
		return !(strings.Contains(p, "/") && strings.Contains(p, `\`))
	}
	return !strings.Contains(p, `\`)
}

// isPattern reports whether p is a glob or a brace list rather than a path.
func isPattern(p string) bool { return isGlob(p) || strings.ContainsAny(p, "{}") }

// patternWithheld judges tok, the pattern in marked (a lone Glob or recall argument, or the pattern of
// a root-led Glob preview whose directory is dir, "" for none), by the structured-glob rules. One level
// of `{a,b}` is expanded (braceAlternatives), and the braces are also read as path starts (PowerShell
// opens a script block at `{` and starts an argument after `}`); every alternative of a brace list
// must itself be a glob, since a concrete path among them (`{docs/note.txt,x}`) would reach no host
// judgement. Each alternative must be built only from the whitelist plus `* ? [ ]`
// (plainTokenSafeGlob: a regular expression with `^`, `$`, `(`, `)` or `|` is no glob), name no
// absolute or escaping path in either backslash reading, stay inside the project (inside, which
// counts a class or `?` that respells `..`, globClimbs), and select no path the build withholds, read
// from dir and, for a preview, also alone (a script's argument, read from the working directory). The
// text and each alternative must name no rule literal or withheld name as a whole name. It asks the
// host nothing.
func (j pathJudge) patternWithheld(marked, tok, dir string) bool {
	if j.rulesUnavailable() || j.screenAll || j.textNamesWithheld(marked, screenWhole) {
		return true
	}
	alts, ok := braceAlternatives(tok)
	if !ok || tokenOutside(braceDelims.Replace(tok)) {
		return true
	}
	for _, a := range alts {
		if len(alts) > 1 && !isGlob(a) {
			return true
		}
		if !plainTokenSafeGlob(a) || tokenOutside(a) || tokenOutside(classReading(a)) ||
			j.textNamesWithheld(a, screenWhole) || j.textNamesWithheld(classReading(a), screenWhole) {
			return true
		}
		g := strings.ReplaceAll(a, string(rootMark), j.root)
		reads := []string{g}
		if dir != "" {
			reads = []string{dir + "/" + g, g}
		}
		for _, p := range reads {
			if !j.inside(p) || (isGlob(p) && j.globSelectsKnown(p)) {
				return true
			}
		}
	}
	return false
}

// braceDelims reads a brace list's braces as list delimiters, where a path may start (pathDelims).
var braceDelims = strings.NewReplacer("{", ",", "}", ",")

// braceAlternatives expands one level of a brace list in tok (`*.{ts,tsx}` is `*.ts` and `*.tsx`), as
// bash's brace expansion and a glob library's alternation read it; a token with no brace is its own
// one alternative. A sequence (`{1..3}`), a nested or unbalanced brace, or a brace with no comma is
// not expanded, and ok is false.
func braceAlternatives(tok string) (alts []string, ok bool) {
	open, end := strings.IndexByte(tok, '{'), strings.IndexByte(tok, '}')
	switch {
	case open < 0 && end < 0:
		return []string{tok}, true
	case open < 0 || end < open || strings.Count(tok, "{") != 1 || strings.Count(tok, "}") != 1:
		return nil, false
	}
	list := tok[open+1 : end]
	if !strings.Contains(list, ",") {
		return nil, false
	}
	for _, item := range strings.Split(list, ",") {
		alts = append(alts, tok[:open]+item+tok[end+1:])
	}
	return alts, true
}

// plainTokenSafeGlob reports whether tok is a lone glob pattern: plainTokenSafe once the glob
// metacharacters `* ? [ ]` are removed, so a pattern like `**/*.go` or `[sS]ecret*.key` is safe but a
// regular expression anchored with `^`, `$` or a group is not.
func plainTokenSafeGlob(tok string) bool { return plainTokenSafe(globMetaGone.Replace(tok)) }

// globMetaGone removes the glob metacharacters plainTokenSafeGlob reads past, built once (a replacer
// built per call was a tenth of a build's allocated bytes: wave 22's verify of audit 2's finding 33).
var globMetaGone = strings.NewReplacer("*", "", "?", "", "[", "", "]", "")

// rootGlobShape reports whether toks, a two-word summary with the root held together, is a Glob
// preview of a directory under the root (the store's `path pattern`): the first word is the root's
// mark, alone or followed by a separator and a plain relative path with no pattern in it, and the
// second is a pattern.
func rootGlobShape(toks []string) bool {
	d, g := toks[0], toks[1]
	if d == "" || d[0] != rootMark || !isPattern(g) {
		return false
	}
	rest := d[1:]
	return rest == "" ||
		(isSep(rest[0]) && !isPattern(rest) && !strings.ContainsRune(rest, rootMark) && plainTokenSafe(rest))
}

// rootGlobWithheld judges a Glob preview of a directory under the root (rootGlobShape) as one
// structured glob: the directory must name no escaping path and is judged by the host once, as a file
// pointer's path is, after the pattern has passed patternWithheld, read from the directory and alone.
func (j pathJudge) rootGlobWithheld(marked string, toks []string) bool {
	dir := strings.ReplaceAll(toks[0], string(rootMark), j.root)
	if tokenOutside(toks[0]) || j.patternWithheld(marked, toks[1], dir) {
		return true
	}
	return j.withheld(dir)
}

// rootStretch is toks, a summary tokenized with the project root held together as one mark, whose
// FIRST token is the root followed by a path below it, or the root followed later by a word that holds
// a separator: the stretch from the root through its last separator-bearing token, with the root
// spelled as the build's root. It is the whole path of a Read of `<root>/my docs/x.txt`, the script
// path of a command run from the root (`<root>/tools/lint.ps1 …`) or a Grep preview's directory; a
// later argument that holds a separator extends it (`<root>\run.ps1 -o out\x`), so it reaches the
// host too. The root alone, with no path below it, is not a stretch (`<root> status` asks the host
// nothing). through is the index of the stretch's last token.
func (j pathJudge) rootStretch(toks []string) (v string, through int, ok bool) {
	if len(toks) == 0 || toks[0] == "" || toks[0][0] != rootMark {
		return "", 0, false
	}
	last := -1
	for i, tk := range toks {
		if i == 0 {
			if len(tk) > 1 && strings.ContainsAny(tk[1:], `/\`) {
				last = 0
			}
			continue
		}
		if strings.ContainsAny(tk, `/\`) {
			last = i
		}
	}
	if last < 0 {
		return "", 0, false
	}
	v = strings.Join(toks[:last+1], " ")
	return strings.ReplaceAll(v, string(rootMark), j.root), last, true
}

// nameScreen is how textNamesWithheld reads a text's names (D63(4)).
type nameScreen int

const (
	// screenFree reads free text: a whole literal or a withheld name counts where a name starts,
	// whatever follows it (D61's accepted over-withholding, kept by D63).
	screenFree nameScreen = iota
	// screenWhole reads a structured value, one path: a whole literal or a withheld name counts only as
	// a whole name (nameEndsAt).
	screenWhole
	// screenExact reads a rooted structured value spelled in one separator style (exactRooted) that is
	// one path (oneValuePath), the exact path the host judged: whole names, and the rules' literals
	// alone, not the withheld names.
	screenExact
)

// textWithheld runs the free-text whitelist over v (freeTextWithheld) and judges each recall path:
// selector in it (selectorWithheld). cut marks a value the store's preview cut, whose last token is
// judged as a prefix. mode says how the name screen reads v: screenFree for a command, a query, a
// prompt or a JSON string value; screenWhole or screenExact for a one-word structured value. v is
// judged in each of its control readings (controlReadings), and withheld when one is.
func (j pathJudge) textWithheld(v string, cut bool, mode nameScreen) bool {
	for _, r := range controlReadings(v) {
		if j.freeTextWithheld(j.markRoot(sanitize(r)), cut, mode) {
			return true
		}
		for _, s := range selectorValues(r) {
			if j.selectorWithheld(s) {
				return true
			}
		}
	}
	return false
}

// controlReadings are the readings of t, a text, that the screen judges: t as the store's preview
// spells it, whose control characters sanitize drops (store.previewString), and, when t holds a
// control character sanitize drops (a C0 control but a tab, a line feed or a carriage return, or
// DEL), t with each such character read as a space, as a reader that splits a list at it does
// (`find -print0` and `git ls-files -z` write NUL-separated lists). The store's plain preview has
// already dropped them, so only a canonical-JSON preview's decoded string, which keeps them as JSON
// escapes, has a second reading. Dropped alone, a NUL glued `x` to `/etc/passwd` into the one
// relative word `x/etc/passwd`, and `src/a.ts` to `.env` into `src/a.ts.env`, where the denied name
// starts no name, and both were shown (candidate 8's diff verify, finding 8: coordinator decision
// D66(e) fails it closed, so a text either reading withholds is withheld).
func controlReadings(t string) []string {
	if s := strings.Map(droppedControlAsSpace, t); s != t {
		return []string{t, s}
	}
	return []string{t}
}

// droppedControlAsSpace maps a control character sanitize drops to an ASCII space (controlReadings).
func droppedControlAsSpace(r rune) rune {
	if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f {
		return ' '
	}
	return r
}

// freeTextWithheld runs the whitelist over marked, a text with the project root held together. When
// the host's rules are unavailable, or a rule covers the project (screenAll), every free text is
// withheld. Otherwise no root's mark may follow an apostrophe outside a quoted run
// (rootInApostropheSpan); each token, and each piece of one split at an operator glued into it
// (operatorPieces), must be safe (tokenSafe) and name no absolute or escaping path (tokenOutside); a
// cut final token is judged as a prefix (cutTokenUnsafe, and cutPrefixNamed on the text's end); and
// the whole text must hold no rule literal or withheld name (textNamesWithheld, read as mode says).
// A single `%` that no escape, variable or parameter can use is read as a name character
// (singlePercent). It asks the host nothing.
func (j pathJudge) freeTextWithheld(marked string, cut bool, mode nameScreen) bool {
	if marked == "" {
		return false
	}
	if j.rulesUnavailable() || j.screenAll {
		return true
	}
	safe := marked
	if singlePercent(marked, cut) {
		safe = strings.Replace(marked, "%", "_", 1)
	}
	toks := splitTokens(safe)
	if rootInApostropheSpan(toks) {
		return true
	}
	for i, tk := range toks {
		if cut && i == len(toks)-1 {
			if cutTokenUnsafe(tk) {
				return true
			}
			continue
		}
		for _, pc := range operatorPieces(tk) {
			if !tokenSafe(pc) || tokenOutside(pc) {
				return true
			}
		}
	}
	if cut && j.cutPrefixNamed(marked) {
		return true
	}
	return j.textNamesWithheld(marked, mode)
}

// rootInApostropheSpan reports whether toks, a free text's tokens, hold the root's mark after an
// apostrophe outside a quoted run. Such an apostrophe (one between two letters, plainTokenSafe) opens
// a quoted span that a POSIX shell and PowerShell close only at the next apostrophe, whatever lies
// between, a double-quoted run's apostrophe included, so after it the screen's tokens and runs are no
// longer the shell's words: the spaces and operators inside a span are literal and join tokens judged
// one by one into one word. The screen reads every token start as a path start and the whole text's
// names, which covers that word, except for the root's mark, the one unit judged by what follows it
// (rootEndsAt, dqSibling, operatorPieces): inside a span the root after a path start and a space or an
// operator is a sibling's name (`it's --o=<root> old y'z` is `its --o=/q/proj old yz`). So no root may
// follow such an apostrophe (freeTextWithheld). Before the first one, a double-quoted run's
// apostrophe is literal and a single-quoted run closes itself, so the shell's words are the screen's.
func rootInApostropheSpan(toks []string) bool {
	opened := false
	for _, tk := range toks {
		if opened {
			if strings.ContainsRune(tk, rootMark) {
				return true
			}
			continue
		}
		if _, run := quoteRun(tk); run {
			continue
		}
		if i := strings.IndexByte(tk, '\''); i >= 0 {
			opened = true
			if strings.ContainsRune(tk[i:], rootMark) {
				return true
			}
		}
	}
	return false
}

// singlePercent reports whether t, a free text, holds exactly one `%` that no escape, variable or
// parameter can use (D63(2)): what follows it is not two hex digits (a percent-escape `%XX`), nor `u`
// and four hex digits (the `%uXXXX` escape IIS and JavaScript's unescape decode), nor a digit, `*` or
// `~` (a cmd.exe batch parameter: `%1`, `%*`, `%~dp0`, the batch file's own directory); no second `%`
// makes it a cmd.exe `%VAR%` pair; and, in a cut text, the cut did not fall within the five bytes after
// it, where such digits might stand.
func singlePercent(t string, cut bool) bool {
	i := strings.IndexByte(t, '%')
	if i < 0 || strings.Count(t, "%") != 1 {
		return false
	}
	a := t[i+1:]
	switch {
	case cut && len(a) < 5:
		return false
	case a == "":
		return true
	case hexRun(a, 2), (a[0] == 'u' || a[0] == 'U') && hexRun(a[1:], 4):
		return false
	}
	return !(a[0] >= '0' && a[0] <= '9' || a[0] == '*' || a[0] == '~')
}

// hexRun reports whether s starts with n hex digits.
func hexRun(s string, n int) bool {
	if len(s) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if !isHex(s[i]) {
			return false
		}
	}
	return true
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'f' }

// jsonWithheld judges a canonical-JSON preview (D63(1)) by its decoded strings. A grammatical
// preview's path-named values are structured (pathNamedWithheld: the one path-named value is judged by
// the host, several ask the host nothing); every other string is free text, an object's keys among
// them: a key is a decoded string like a value, and may carry a path (`{"private/deny.txt":"x"}`), so
// it is screened as a value is, at the cost of withholding a key that spells a rule's literal
// (`{"secrets":true}` under `Read(./secrets/**)`). A preview whose grammar a command breaks (`{"x":1}
// && cat …`, cut) is one free text, its strings beside it.
func (j pathJudge) jsonWithheld(t string) bool {
	strs, grammatical := jsonStrings(t)
	if !grammatical {
		body, cut := strings.CutSuffix(t, previewEllipsis)
		if j.textWithheld(body, cut, screenFree) {
			return true
		}
		for _, s := range strs {
			if j.textWithheld(s.value, s.cut, screenFree) {
				return true
			}
		}
		return false
	}
	named := 0
	for _, s := range strs {
		if s.pathNamed {
			named++
		}
	}
	for _, s := range strs {
		if s.pathNamed {
			if j.pathNamedWithheld(s.value, s.cut, named == 1) {
				return true
			}
			continue
		}
		if j.textWithheld(s.value, s.cut, screenFree) {
			return true
		}
	}
	return false
}

// pathNamedWithheld judges one path-named JSON value. host reports that it is the preview's only
// path-named value, and so may cost one host judgement (containment and the host's rules); several
// values ask the host nothing, so containment alone screens them. A glob is judged by what it selects
// (globSelectsKnown), and so is each glob piece of a value that holds several paths; every value is
// screened, in each of its readings (valueReadings), for a rule's literal, a withheld name and a
// refusing selector (so `{"paths":["**/deny.txt"]}` is withheld by the literal `deny.txt`). A rooted
// value the host judged in one separator style is screened by the rules' literals alone (exactRooted)
// only when it is one path (oneValuePath): the host judged a value that holds several as one path
// that no reader reads, so its pieces are screened by the withheld names too (candidate 8's diff
// verify, finding 9). A value the store's cut fell inside is judged by the directory it spells whole,
// by the host only when it is the preview's one path-named value, and by the screen's prefix rule
// (cutValueWithheld).
func (j pathJudge) pathNamedWithheld(v string, cut, host bool) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if j.valueNamesOutside(v, cut) {
		return true
	}
	if cut {
		return j.cutValueWithheld(v, host)
	}
	mode := screenWhole
	if host {
		if j.withheld(v) {
			return true
		}
		if j.exactRooted(v) && j.oneValuePath(v) {
			mode = screenExact
		}
	} else if p := judgedSpelling(v); p == "" || !j.inside(p) {
		return p != ""
	}
	if isGlob(v) && j.globSelectsKnown(v) {
		return true
	}
	readings := valueReadings(v)
	if len(readings) > 1 {
		// Each glob piece of a value that holds several paths is judged by what it selects, as the
		// piece would be alone (`src/a.ts<NUL>lnk/tok*` selects lnk/token.txt).
		for _, pc := range strings.Fields(readings[len(readings)-1]) {
			if pc != v && isGlob(pc) && j.globSelectsKnown(pc) {
				return true
			}
		}
	}
	for _, r := range readings {
		if isGlob(r) && j.textNamesWithheld(j.markRoot(sanitize(classReading(r))), mode) {
			return true
		}
		if j.textNamesWithheld(j.markRoot(sanitize(r)), mode) {
			return true
		}
		for _, s := range selectorValues(r) {
			if j.selectorWithheld(s) {
				return true
			}
		}
	}
	return false
}

// valueReadings are the readings of v, a path-named value, that the name screen judges: v as the
// store's preview spells it, whose control characters sanitize drops and whose quotes screen form
// removes (screenText), and, when it differs, its name form (valueNameForm). A value either reading
// withholds is withheld (coordinator decision D66(e): over-withholding is accepted, showing a refused
// path is not), so the name form only ever withholds more.
func valueReadings(v string) []string {
	if f := valueNameForm(v); f != v {
		return []string{v, f}
	}
	return []string{v}
}

// valueNameForm is v, a path-named value, with each character at which one of its pieces may end or
// a path may start inside one, and which the screen would otherwise drop or read as part of a name,
// read as an ASCII space (candidate 8's diff verify, finding 8): every character a list splits at
// (valueListSep), a control character among them, C0 or DEL, which sanitize drops, or C1, and a
// space outside ASCII (NBSP, U+2028, U+3000), whose bytes nameByte reads as a name's; every
// character outside ASCII that the whitelist reads as no letter (wordRune), after which a path starts
// (valuePathStart): a letter whose ANSI best fit is punctuation (U+02BA), an invisible format
// character such as a zero-width space, a symbol; and a quote or a backtick, after which a path starts
// too and which screen form removes. Glued so, a piece that starts with a rule's literal or a withheld
// name (`.env` after `src/a.ts` and a NUL, `secrets/key.pem` after an NBSP) started no name, the
// screen never read it, and section 6 showed it (77374c3c). In the name form each piece starts where
// a name starts. A path's other starts (valueOpeners' `: = @ ( [ { < > &`) are no name's characters
// (nameByte), so a name already starts after them; the root's own spelling holds none of these
// characters when it has a unit (rootUnitAdmitted), so the name form holds it as v does.
func valueNameForm(v string) string {
	return strings.Map(func(r rune) rune {
		if valueListSep(r) || (r >= utf8.RuneSelf && !wordRune(r)) || r == '"' || r == '\'' || r == '`' {
			return ' '
		}
		return r
	}, v)
}

// oneValuePath reports whether v, a structured value, is one path as valueNamesOutside reads one:
// past the project root's own spelling that may lead it (rootSpanAt), no character a list splits at
// (valueListSep) and no place a path may start (valuePathStart). Only then is the path the host
// judged the one a reader reads, so only then may a rooted value be screened by the rules' literals
// alone (screenExact; candidate 8's diff verify, finding 9: `<root>/src/a.go <root>/lnk/token.txt`
// and `<root>/src/a.go;lnk/token.txt`, whose lnk is a link into a refused directory, were shown).
func (j pathJudge) oneValuePath(v string) bool {
	t := strings.TrimSpace(v)
	for i := j.rootSpanAt(t, 0); i < len(t); {
		if i > 0 && valuePathStart(t, 0, i) {
			return false
		}
		r, size := utf8.DecodeRuneInString(t[i:])
		if valueListSep(r) {
			return false
		}
		i += size
	}
	return true
}

// valueNamesOutside reports whether v, a path-named value (cut: the store's cut fell inside it), names
// a path outside the project in any of its pieces (audit 2's finding 26). A path-named value is
// judged whole, as one path, by containment and the host, and is exempt from the free-text whitelist;
// but a value may hold several paths, as a list a tool splits at whitespace, a comma, a semicolon or
// a bar (valueListSep), and judged whole, a list whose first piece is relative is a relative path the
// host, joining it under the root, refuses nothing about. So each piece is judged for a path outside
// the project wherever a path may start in it (valuePathStart, pieceOutside). Where a path may start,
// the project root's own spelling, as containment compares it (RootRelative: the cleaned root, `/`
// read as the platform's separator, an ASCII letter's case folded where paths fold), is read whole
// (rootSpanAt), so a project path under a root with a space, a comma or a semicolon stays one piece
// whatever else the root holds, and no character of the root starts a path: a value is no shell
// input, so D64(1)'s character set does not apply. The store's cut leaves only the start of the last
// piece: from a path start that begins the root's own spelling byte for byte (rootPrefix, D64(8)) to
// the cut it is the project. A single project path with a space in it is pieces that all stay in the
// project, and is shown. It asks the host nothing.
func (j pathJudge) valueNamesOutside(v string, cut bool) bool {
	t := strings.TrimSpace(v)
	if cut {
		start := 0
		for i := 0; i < len(t); {
			if valuePathStart(t, start, i) && j.rootPrefix(t[i:]) {
				t, cut = t[:i], false
				break
			}
			r, size := utf8.DecodeRuneInString(t[i:])
			if valueListSep(r) {
				start = i + size
			}
			i += size
		}
	}
	var held []bool
	start := 0
	for i := 0; i < len(t); {
		if valuePathStart(t, start, i) {
			if n := j.rootSpanAt(t, i); n > 0 {
				if held == nil {
					held = make([]bool, len(t))
				}
				for k := i; k < i+n; k++ {
					held[k] = true
				}
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(t[i:])
		if valueListSep(r) {
			if i > start && j.pieceOutside(t[start:i], heldIn(held, start, i), false) {
				return true
			}
			start = i + size
		}
		i += size
	}
	return start < len(t) && j.pieceOutside(t[start:], heldIn(held, start, len(t)), cut)
}

// heldIn is held[from:to], or nil when no root was held.
func heldIn(held []bool, from, to int) []bool {
	if held == nil {
		return nil
	}
	return held[from:to]
}

// valueListSep reports a character a tool that takes a list of paths in one value may split it at
// (valueNamesOutside): whitespace of any kind, a control character (C0, DEL or C1; a NUL-separated
// list is what `find -print0` and `git ls-files -z` write, and the store's preview keeps the NUL as
// `\u0000`, which jsonStrings decodes; wave 22's verify, fix round 2), a comma, a semicolon or a bar.
// No project path holds a control character, so none joins two names.
func valueListSep(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || r == ',' || r == ';' || r == '|'
}

// valueOpeners are the characters after which a path may start anywhere in a piece of a path-named
// value (valuePathStart): a PATH-style list's or an scp address's `:`, an option's `=`, `@` (a
// response file, a user's host), what opens a quoted or bracketed stretch, which a reader reads a
// path inside (`"/etc/passwd"`, `(~/.ssh/id_rsa)`, `<…>`; wave 22's verify of finding 26), and a
// glued redirect or command separator (`a.ts>/etc/passwd`, `a.ts&&/etc/passwd`; its fix round 2),
// `>` illegal in a Windows name and neither one a name's last character before a separator in a
// project's paths (`R&D/plan.md` and `Q&A.md` stay in the project, which is all the screen asks of
// what follows).
const valueOpeners = ":=@\"'`([{<>&"

// valueLeaders are the ASCII characters after which a path may start when they lead a piece
// (valuePathStart): every punctuation character but a name's (`. _ - ~ $ %`, the last three also a
// home's or a variable's, which containment reads at a path start), a separator, a glob's wildcard
// (`* ?`, as in `*/x.go`) and the list separators. Inside a piece most of them are a name's own
// characters, which a project's paths hold (`c++`, `C#`, SvelteKit's `+page`, Next.js's `(auth)` and
// `[id]`), so only an opener (valueOpeners) starts a path there; leading a piece they are a
// redirect, a negation, cmd.exe's next copy source, a comment or a closing bracket before the path
// (`>/etc/passwd`, `!/etc/passwd`, `+\Windows\win.ini`, `#/etc/passwd`, `)/etc/passwd`), as free
// text reads them (pathStartDelims).
const valueLeaders = "!\"#&'()+:<=>@[]^`{}"

// valuePathStart reports whether a path may start at i in t, a path-named value, within the piece
// that starts at start (valueNamesOutside): at the piece's start; after an opener (valueOpeners)
// other than a drive's `:` (one ASCII letter at a path start: `C:\q\proj`, `--dir=C:\x`); after a
// character outside ASCII that the free-text whitelist does not read as a letter (wordRune): one that
// is no letter, mark or digit (an invisible format character such as a zero-width space, which hides
// the start of a path from a reader, a symbol, a punctuation mark), or a letter or mark whose Windows
// ANSI best fit is ASCII punctuation (bestFitPunct: U+02BA reaches an ANSI program as `"`, U+01C0 as
// `|`; wave 22's verify, fix round 2); after a run of leaders that leads the piece (valueLeaders); and
// after a short option that leads it, after its first letter and after all its letters (`-I/opt`,
// `-C../x`), as free text reads one (pathStarts). Inside a piece `+ # ) ] } ! ^` stay a name's own
// characters (`c++/x`, `C#/x`, `(auth)/x`, `[id]/x`), a deliberate residual ADR 0011 §23 item 6
// names.
func valuePathStart(t string, start, i int) bool {
	if i == start {
		return true
	}
	r, size := utf8.DecodeLastRuneInString(t[start:i])
	switch {
	case r == ':':
		k := i - size - 1
		return k < start || !asciiLetter(t[k]) || !valuePathStart(t, start, k)
	case r < utf8.RuneSelf && strings.IndexByte(valueOpeners, byte(r)) >= 0:
		return true
	case r >= utf8.RuneSelf:
		return !wordRune(r)
	}
	if strings.IndexFunc(t[start:i], notValueLeader) < 0 {
		return true
	}
	k := shortOptionEnd(t[start:])
	return k > 0 && (i-start == 2 || i-start == k)
}

// notValueLeader reports a character that is no leader of a piece (valuePathStart): neither one of
// valueLeaders nor a character outside ASCII that the whitelist reads as no letter (wordRune).
func notValueLeader(c rune) bool {
	if c >= utf8.RuneSelf {
		return wordRune(c)
	}
	return !strings.ContainsRune(valueLeaders, c)
}

// rootSpanAt is the length of the project root's own spelling at t[i:], as containment compares it
// (RootRelative: the cleaned root, `/` read as the platform's separator, an ASCII letter's case folded
// where the platform's paths fold, nothing else), when the spelling ends t or a separator follows it;
// otherwise 0.
func (j pathJudge) rootSpanAt(t string, i int) int {
	if j.root == "" {
		return 0
	}
	r := filepath.Clean(j.root)
	e := i + len(r)
	if e > len(t) || !asciiFoldEqual(filepath.FromSlash(t[i:e]), r, paths.DefaultFold()) {
		return 0
	}
	if e < len(t) && r[len(r)-1] != filepath.Separator && filepath.FromSlash(t[e : e+1])[0] != filepath.Separator {
		return 0
	}
	return len(r)
}

// pieceOutside reports whether pc, one piece of a path-named value (cut: the store's cut fell inside
// it; held: which of its bytes spell the project root, read whole, or nil), names a path outside the
// project (valueNamesOutside) at any place a path may start in it (valuePathStart) but inside the
// root's own spelling: what runs from there to the piece's end names a path outside the project
// (restOutside). An http(s) URL that starts there is judged as free text judges one (urlOutside), up
// to a quote or an angle bracket, after which a path may start again.
func (j pathJudge) pieceOutside(pc string, held []bool, cut bool) bool {
	for s := 0; s < len(pc); {
		_, size := utf8.DecodeRuneInString(pc[s:])
		if (s > 0 && held != nil && held[s-1]) || !valuePathStart(pc, 0, s) {
			s += size
			continue
		}
		rest := pc[s:]
		if isURL(rest) {
			e := strings.IndexAny(rest, "\"'`<>")
			if e < 0 {
				e = len(rest)
			}
			if urlOutside(rest[:e]) {
				return true
			}
			s += e
			continue
		}
		if j.restOutside(rest, cut, namesDrive(pc, held, s)) {
			return true
		}
		s += size
	}
	return false
}

// namesDrive reports whether the name before the first `:` of pc[s:], a piece of a path-named value
// from a place a path may start in it, may be a PowerShell drive's or provider's whole name: no other
// place a path may start lies inside it outside the root's own spelling (held). One that does is the
// name of none: a reader reads the path that starts there, whose own name restOutside judges
// (`"C:\q\proj"` and `--out=C:\q\proj` are no drive named `"C` or `--out=C`; `--out=Temp:x` names Temp).
func namesDrive(pc string, held []bool, s int) bool {
	colon := strings.IndexByte(pc[s:], ':')
	for k := s + 1; colon > 0 && k < s+colon; k++ {
		if (held == nil || !held[k-1]) && valuePathStart(pc, 0, k) {
			return false
		}
	}
	return true
}

// restOutside reports whether rest, a piece of a path-named value from a place a path may start in it
// to the piece's end (cut: the store's cut fell inside it), names a path outside the project: it is
// not inside the project (inside: an absolute path, a home, a variable, a drive-relative path, a
// `file:` URL or a climb, which containment resolves, so `--out=../../x` and `"../x"` climb out while
// `src/../a.ts` stays in), with or without the closing quotes and brackets it ends in (`".."` is the
// parent); when drive is set (namesDrive), it is a PowerShell drive- or provider-qualified path
// (providerPath; a drive letter's is containment's to judge), or, when the cut fell right after its
// `:`, would be one with a name after it (D64(2)); or, when it is not rooted, it climbs out in either other reading of its backslashes,
// as a separator wherever the value was recorded and as a POSIX shell's escape (`..\x` on Linux,
// `.\./x`).
func (j pathJudge) restOutside(rest string, cut, drive bool) bool {
	if !j.inside(rest) {
		return true
	}
	if drive && !driveLetter(rest) && (providerPath(rest) || (cut && strings.HasSuffix(rest, ":") && providerPath(rest+"x"))) {
		return true
	}
	readings := []string{rest}
	if !absLike(rest) && strings.Contains(rest, `\`) {
		readings = append(readings, strings.ReplaceAll(rest, `\`, "/"), strings.ReplaceAll(rest, `\`, ""))
	}
	for _, r := range readings {
		if !j.inside(r) {
			return true
		}
		if c := strings.TrimRight(r, "\"'`)]}>"); c != r && c != "" && !j.inside(c) {
			return true
		}
	}
	return false
}

// driveLetter reports whether p starts with a Windows drive: one ASCII letter and a `:`.
func driveLetter(p string) bool { return len(p) >= 2 && asciiLetter(p[0]) && p[1] == ':' }

// valueWithheld reports whether a whole structured value may not be shown: the build withholds it as
// it would a file pointer's path (withheld: containment and the host's rules), or it is a glob that
// selects a path the build withholds (globSelectsKnown). The caller screens the value as free text
// where D63 requires it (oneWordWithheld), so this judges containment and the host alone. A value the
// store's cut fell inside is judged by cutValueWithheld, which the callers reach directly.
func (j pathJudge) valueWithheld(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if j.withheld(v) {
		return true
	}
	return isGlob(v) && j.globSelectsKnown(v)
}

// cutValueWithheld judges v, a structured value the store's cut fell inside: only its start is
// known, and its last segment is the start of a name, not a name. A start of the root's own spelling,
// byte for byte as v spells it (rootPrefix), is the project. Otherwise the directory it spells whole,
// all but that last segment, is judged as a path: by containment and, when host is set (the
// summary's one structured value), by the host's rules, one judgement; a value with no directory
// part is withheld only when it is rooted outside the project (`~`, `$HOM`). Then the value is
// screened as the end of a cut text is (cutPrefixNamed), in each of its readings (valueReadings): a
// rule's literal or a withheld name it holds, or the start of one it ends in (`private/den…`,
// `src/a.ts<NUL>.en…`), withholds it. A cut value is never a withheld name (recordedPath).
func (j pathJudge) cutValueWithheld(v string, host bool) bool {
	p := judgedSpelling(v)
	if p == "" || j.rootPrefix(v) {
		return false
	}
	dir := ""
	switch i := strings.LastIndexAny(p, `/\`); {
	case i < 0:
		if absLike(p) && !j.inside(p) {
			return true
		}
	case i == 0:
		dir = p[:1]
	default:
		dir = p[:i]
	}
	switch {
	case dir == "":
	case host && j.withheld(dir):
		return true
	case !host && !j.inside(dir):
		return true
	}
	for _, r := range valueReadings(v) {
		marked := j.markRoot(sanitize(r))
		if j.textNamesWithheld(marked, screenWhole) || j.cutPrefixNamed(marked) {
			return true
		}
	}
	return false
}

// rootPrefix reports whether t, a stretch of a text the store's cut fell inside, is the start of the
// project root's own spelling, byte for byte (coordinator decision D64's ruling on wave 19f's final
// verify): the root cleaned, in the platform's separators or, on Windows, in `/` throughout, with an
// ASCII letter's case folded only where the platform's paths fold (Windows and macOS). Nothing is
// deleted, no whitespace is folded and no repeated separator collapsed: a stretch that differs from
// the root's spelling only by a quote, a caret, a backslash or a whitespace run, which screen form
// (screenText) would erase, names a directory beside the project (`<q>/John'athan` beside
// `<q>/Johnathan`, `<q>/obrien` beside `<q>/o'brien`), and is judged as the path it spells
// (cutValueWithheld); a repeated separator, which names the root, is over-withheld.
func (j pathJudge) rootPrefix(t string) bool {
	if j.root == "" || t == "" {
		return false
	}
	clean := filepath.Clean(j.root)
	spellings := []string{clean}
	if runtime.GOOS == "windows" {
		spellings = append(spellings, filepath.ToSlash(clean))
	}
	for _, s := range spellings {
		if len(t) <= len(s) && asciiFoldEqual(t, s[:len(t)], paths.DefaultFold()) {
			return true
		}
	}
	return false
}

// asciiFoldEqual reports whether a and b are equal byte for byte, apart from the case of an ASCII
// letter when fold is set. No other character folds: the Kelvin sign is not `k`, which Unicode's
// lower-casing (paths.Key) would make it, and NTFS does not.
func asciiFoldEqual(a, b string, fold bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if x, y := a[i], b[i]; x != y && !(fold && asciiLetter(x) && x|0x20 == y|0x20) {
			return false
		}
	}
	return true
}

// textNamesWithheld reports whether marked, a text with the project root held together, names a Read
// rule's literal or a withheld path's basename or relative path in any of its screen forms
// (screenForms): a literal run from inside a glob segment (midScreens) anywhere; one that starts a
// glob segment (openScreens) where a name starts (namedAt), whatever follows; and a whole literal
// (screens) or a withheld name (knownText) where a name starts and, unless mode is screenFree, ends
// where a name ends. With mode screenExact the withheld names are not read.
func (j pathJudge) textNamesWithheld(marked string, mode nameScreen) bool {
	sets := [][]string{j.screens}
	if mode != screenExact && j.learned == nil {
		sets = append(sets, j.knownText)
	}
	for _, form := range screenForms(marked) {
		for _, name := range j.midScreens {
			if strings.Contains(form, name) {
				return true
			}
		}
		for _, name := range j.openScreens {
			if namedAt(form, name, true) {
				return true
			}
		}
		for _, set := range sets {
			for _, name := range set {
				if namedAt(form, name, mode == screenFree) {
					return true
				}
			}
		}
		if mode != screenExact && j.learned != nil && j.learned.text.namedAt(form, mode == screenFree) {
			return true
		}
	}
	return false
}

// screenForms are marked's readings in screen form (screenText): with every `\` a separator, as
// cmd.exe and PowerShell read a whitelisted token, and with every `\` removed, as a POSIX shell reads
// one (a safe token's backslash always escapes a character other than another backslash,
// plainTokenSafe, so one pass of the shell's escape rule leaves no backslash behind; ADR 0011 §23);
// and, when it holds a parenthesis (the whitelist allows one only inside a quoted run), both again
// with the parentheses removed, as a nested zsh reads a group (`de(n)y.txt` is `deny.txt`).
func screenForms(marked string) []string {
	texts := []string{marked}
	if strings.ContainsAny(marked, "()") {
		texts = append(texts, noParens.Replace(marked))
	}
	forms := make([]string, 0, 2*len(texts))
	for _, t := range texts {
		forms = append(forms,
			screenText(strings.ReplaceAll(t, `\`, "/"), false),
			screenText(strings.ReplaceAll(t, `\`, ""), false))
	}
	return forms
}

// noParens removes the parentheses a quoted run's words may hold.
var noParens = strings.NewReplacer("(", "", ")", "")

// cutTokenUnsafe reports whether the last token of a cut summary is unsafe or names an outside path
// (D63(5)); freeTextWithheld then judges the text's end as the start of a name (cutPrefixNamed). Split
// at a glued operator, every piece but the last is judged as a token, the last as the cut word. A cut
// that fell inside a quoted run leaves an open run: its content is judged as a closed run's is, its
// last word as the cut word. A backslash the cut left last escapes or separates what the cut hid, so
// the word before it is judged.
func cutTokenUnsafe(tk string) bool {
	if q := tk[0]; (q == '"' || q == '\'') && strings.IndexByte(tk[1:], q) < 0 {
		inner := tk[1:]
		if strings.ContainsAny(inner, "`$") || (q == '\'' && strings.ContainsAny(inner, `"\`)) {
			return true
		}
		words := dqWords(inner)
		if len(words) == 0 {
			return false
		}
		last := len(words) - 1
		for i, w := range words[:last] {
			if !wordSafe(w, true) || wordOutside(w, true) || dqSibling(words, i) {
				return true
			}
		}
		return cutWordUnsafe(words[last], true)
	}
	pieces := operatorPieces(tk)
	if len(pieces) == 0 {
		return false
	}
	last := len(pieces) - 1
	for _, pc := range pieces[:last] {
		if !tokenSafe(pc) || tokenOutside(pc) {
			return true
		}
	}
	return cutWordUnsafe(pieces[last], false)
}

// cutWordUnsafe reports whether w, the word the store's cut fell inside (a word of a quoted run when
// inRun), is unsafe or names an outside path once a backslash the cut left last is dropped, or ends
// right after a drive's or provider's `:` (cutAtDriveColon).
func cutWordUnsafe(w string, inRun bool) bool {
	if strings.HasSuffix(w, `\`) && !strings.HasSuffix(w, `\\`) {
		w = w[:len(w)-1]
	}
	if w == "" {
		return false
	}
	if inRun {
		return !wordSafe(w, true) || wordOutside(w, true) || cutAtDriveColon(w, true)
	}
	return !tokenSafe(w) || tokenOutside(w) || cutAtDriveColon(w, false)
}

// cutAtDriveColon reports whether w, the word the store's cut fell inside, ends right after the `:`
// of a PowerShell drive or provider name at a path start (`Temp:`, `HKCU:`, `x,Env:`, `--dir=Temp:`;
// coordinator decision D64(2)): the cut may have hidden the file after it (`Temp:secret.txt`), so w is
// judged as if a name followed its `:`, as a single-letter drive there already is (startsOutside). An
// uncut bare name (`Temp:`, `fix:`) names a drive root and reveals no file, so providerPath lets it
// stand (D64(3)); an inert prefix (`path:`, `sha256:`) stays inert here too.
func cutAtDriveColon(w string, inRun bool) bool {
	return strings.HasSuffix(w, ":") && wordOutside(w+"x", inRun)
}

// cutPrefixNamed reports whether marked, the end of a text the store's cut fell inside, ends in the
// first minCutPrefix bytes or more of a rule's literal, of a withheld path's basename or relative
// path, or of a rule's specifier, where a name starts, in any screen form; a literal taken from
// inside a glob run (`.env` for `**/*.env`) may begin inside a name, so its prefix counts with no
// boundary.
func (j pathJudge) cutPrefixNamed(marked string) bool {
	sets := [][]string{j.screens, j.openScreens, j.knownText, j.rulePaths}
	if j.learned != nil {
		sets[2] = nil
	}
	for _, form := range screenForms(marked) {
		for _, set := range sets {
			for _, name := range set {
				if endsWithPrefixOf(form, name, true) {
					return true
				}
			}
		}
		if j.learned != nil && j.learned.text.endsWithPrefix(form, true) {
			return true
		}
		for _, name := range j.midScreens {
			if endsWithPrefixOf(form, name, false) {
				return true
			}
		}
	}
	return false
}

// splitTokens splits a marked text into tokens on ASCII spaces (sanitize has already collapsed every
// whitespace run to one space), except inside a double-quoted run, and inside a single-quoted run that
// opens at a token's start, which keep their spaces so that the run is judged whole. An apostrophe
// inside a word opens no run here (plainTokenSafe judges it). Each token is a stretch of s, sliced,
// not copied (wave 22's verify of audit 2's finding 33: copying them byte by byte was a fifth of a
// build's allocations).
func splitTokens(s string) []string {
	var toks []string
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || (c == '\'' && i == start):
			quote = c
		case c == ' ':
			if i > start {
				toks = append(toks, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		toks = append(toks, s[start:])
	}
	return toks
}

// shellOperators are the standalone shell operators a safe token may be (D63(2)): a redirect, a
// descriptor duplication, a pipe or a sequence, as whole tokens. Glued into a word, `;`, `|`, `&&` and
// `||` split it (operatorPieces); any other operator's character is outside the whitelist there.
var shellOperators = map[string]bool{
	"&&": true, "||": true, "|": true, ";": true, ">": true, ">>": true, "2>&1": true, "2>": true, "<": true,
	">&2": true, "1>&2": true,
}

// operatorPieces is tok split at the operators `;`, `|`, `&&` and `||` glued into it (`TODO|FIXME`,
// `2>&1;tail`), each piece then judged as a token of its own (an extension of D63(2), ADR 0011 §23
// item 7): every POSIX shell and PowerShell end a word or a statement at each, and cmd.exe splits at
// `|`, `&&` and `||`. A quoted run, a token holding a double quote, a URL, a whole operator and a
// null-device token are not split. A lone `&` is not an operator here, so `a&b` stays unsafe.
func operatorPieces(tok string) []string {
	if shellOperators[tok] || nullDevice(tok) || isURL(tok) || strings.Contains(tok, `"`) {
		return []string{tok}
	}
	if _, ok := quoteRun(tok); ok {
		return []string{tok}
	}
	if !strings.ContainsAny(tok, ";|&") {
		// Nothing to split at: the split's answer, without building the replaced copy.
		if tok == "" {
			return []string{}
		}
		return []string{tok}
	}
	return strings.FieldsFunc(gluedOperators.Replace(tok), func(r rune) bool { return r == ';' })
}

// gluedOperators reads each operator that splits a word as a `;` (operatorPieces); `&&` and `||` are
// matched before a single `|`.
var gluedOperators = strings.NewReplacer("&&", ";", "||", ";", "|", ";")

// nullDevices are the null device's redirects and the standard streams a safe token may be, as whole
// tokens only (an extension of D63(2), ADR 0011 §23): a POSIX shell's `/dev/null`, `/dev/stdin`,
// `/dev/stdout` and `/dev/stderr`, and cmd.exe's `nul`. Each is a fixed spelling that holds no
// backslash, glob or variable, so no shell reads it as anything else, and none names a file whose
// content or name the payload could reveal.
var nullDevices = map[string]bool{
	"/dev/null": true, ">/dev/null": true, "1>/dev/null": true, "2>/dev/null": true, "&>/dev/null": true,
	">>/dev/null": true, "2>>/dev/null": true, ">nul": true, "1>nul": true, "2>nul": true,
	"/dev/stdin": true, "/dev/stdout": true, "/dev/stderr": true,
}

// nullDevice reports whether tok is a redirect to the null device (nullDevices; cmd.exe's `nul` in
// any case).
func nullDevice(tok string) bool { return nullDevices[tok] || nullDevices[strings.ToLower(tok)] }

// isURL reports whether tok starts an http(s) URL.
func isURL(tok string) bool {
	l := strings.ToLower(tok)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// quoteRun is the content of tok when tok is a simple quoted run, and whether it is one. A
// double-quoted run has one `"` at each end and none inside, and no backtick or `$` in its content:
// no shell expands anything else there, and a backslash in it is literal in every shell unless it
// stands before a quote, a space or another backslash, which plainTokenSafe keeps it from. A
// single-quoted run has one `'` at each end and none inside, and no `"`, backtick, `$` or backslash
// in its content: every shell keeps the content verbatim, and a POSIX shell would keep a backslash
// there, a reading the screen does not make.
func quoteRun(tok string) (string, bool) {
	if len(tok) < 2 {
		return "", false
	}
	q := tok[0]
	if (q != '"' && q != '\'') || tok[len(tok)-1] != q || strings.Count(tok, string(q)) != 2 {
		return "", false
	}
	inner, bad := tok[1:len(tok)-1], "`$"
	if q == '\'' {
		bad = "`$\"\\"
	}
	if strings.ContainsAny(inner, bad) {
		return "", false
	}
	return inner, true
}

// dqWords are the words of a quoted run's content, split at ASCII spaces only: a Unicode space stays
// inside its word, where the whitelist rejects it.
func dqWords(inner string) []string {
	var out []string
	for _, w := range strings.Split(inner, " ") {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// dqSibling reports whether words[i], a word of a quoted run, ends in the project root's mark and is
// followed by another word. Inside one quoted word the root followed by a space and a name is a
// sibling of the root (`"<root> old/x.txt"`). A nested shell's command line may go on after the root
// with an operator (`bash -c "cd <root> && make"`), so a standalone shell operator after the root is
// no sibling's name, but only when the root is a word of its own after a word that ends in no
// delimiter: a program that is no shell takes the run as one argument, whose path starts at the
// run's start, after a delimiter (a list's reader may trim the space after it) and after a short
// option, and from there the operator is a sibling's name (`cat "<root> && make"` opens
// `/q/proj && make`; `"--dir=<root> && make"` and `"a, <root> && make"` name it as a value).
func dqSibling(words []string, i int) bool {
	if !strings.HasSuffix(words[i], string(rootMark)) || i+1 >= len(words) {
		return false
	}
	prev := ""
	if i > 0 {
		prev = words[i-1]
	}
	bare := prev != "" && words[i] == string(rootMark) && strings.IndexByte(pathStartDelims, prev[len(prev)-1]) < 0
	return !(bare && shellOperators[words[i+1]])
}

// tokenSafe reports whether tok is whitelist-safe (D63(2)): a simple quoted run whose words are each
// safe (quoteRun, wordSafe), or a word that is (wordSafe).
func tokenSafe(tok string) bool {
	if inner, ok := quoteRun(tok); ok {
		for _, w := range dqWords(inner) {
			if !wordSafe(w, true) {
				return false
			}
		}
		return true
	}
	return wordSafe(tok, false)
}

// wordSafe reports whether w, a token or a word of a quoted run (inRun), is safe: a standalone shell
// operator or a null-device token; an http(s) URL (urlTokenSafe); or a plain token (plainTokenSafe).
// Inside a quoted run `(` and `)` may also stand in a word, since no shell gives them meaning there
// (safeChars with parens set), but not after `+` or `@`, which open an extglob group in bash and ksh.
func wordSafe(w string, inRun bool) bool {
	switch {
	case w == "" || shellOperators[w] || nullDevice(w):
		return true
	case isURL(w):
		return urlTokenSafe(w)
	case inRun:
		return !strings.Contains(w, "+(") && !strings.Contains(w, "@(") && safeChars(w, true)
	}
	return plainTokenSafe(w)
}

// urlTokenSafe reports whether tok, an http(s) URL, is safe: after its scheme every character is a
// letter, a mark or a digit the whitelist admits (wordRune: none whose best fit is ASCII punctuation)
// or one of urlChars, none of which a shell splits a word at or expands
// (PowerShell splits a bare argument at `,` into an array, so `https://x,/etc/passwd` hands a cmdlet
// /etc/passwd; `;`, `|`, `!`, `$`, a quote, a parenthesis, a glob's `*` or `[`, a brace and a
// backslash are left out with it); the URL proper is a plain token that may also hold `?` and `#` (a
// glob there, `?` or zsh's EXTENDED_GLOB `#`, can only match below a directory named `http:` in the
// project); and each part after an `&`, which a POSIX shell or cmd.exe starts a new command with, is a
// plain token. A `%` is safe only as freeTextWithheld's single `%` (singlePercent), which it reads as
// a name character. urlOutside judges where a path may start in it.
func urlTokenSafe(tok string) bool {
	rest := tok[strings.Index(tok, "://")+3:]
	if strings.IndexFunc(rest, notURLRune) >= 0 {
		return false
	}
	parts := strings.Split(rest, "&")
	if !plainTokenSafe(urlGlobs.Replace(parts[0])) {
		return false
	}
	for _, p := range parts[1:] {
		if !plainTokenSafe(p) {
			return false
		}
	}
	return true
}

// urlChars are the ASCII characters an http(s) URL token may hold after its scheme besides letters
// and digits (urlTokenSafe): `_` stands for a single `%` (singlePercent).
const urlChars = "-._~:/?#@&=+"

// notURLRune reports a rune an http(s) URL token may not hold after its scheme (urlTokenSafe).
func notURLRune(r rune) bool {
	return !(wordRune(r) || strings.ContainsRune(urlChars, r))
}

// urlGlobs removes the glob characters a URL proper may hold (urlTokenSafe).
var urlGlobs = strings.NewReplacer("?", "", "#", "")

// urlOutside reports whether w, an http(s) URL token, names an absolute or escaping path: a path that
// starts outside the project after any pathDelims character in the URL proper (`?f=/etc/passwd`,
// `?d=C:x`, `u@/etc/passwd`), a `..` in it (hasDotDot), or a part after an `&` that names one as a
// token. Its scheme, which PowerShell would read as a drive named http or https, is inert as
// providerPath reads it, and its host is not a path start, so a port (`example.com:8080`) is no drive.
func urlOutside(w string) bool {
	parts := strings.Split(w[strings.Index(w, "://")+3:], "&")
	u := parts[0]
	for i := 0; i+1 < len(u); i++ {
		if strings.IndexByte(pathDelims, u[i]) >= 0 && startsOutside(u[i+1:], true) {
			return true
		}
	}
	if hasDotDot(u) {
		return true
	}
	for _, p := range parts[1:] {
		if readingsOutside(p) {
			return true
		}
	}
	return false
}

// pathDelims are the whitelist characters after which a path may start inside a plain token (D63(3)):
// an option's value (`--out=/x`), a list (`a,/x`, a PATH-style `a:/x`, scp's `host:/x`), and a
// response file or curl's data file (`@/x`).
const pathDelims = "=:,@"

// pathStartDelims are pathDelims and the other characters a safe word may hold where an argument may
// begin: an apostrophe between letters (a quoted span may start an argument there for PowerShell),
// inside a quoted run a parenthesis (a nested shell's subshell, PowerShell's subexpression), the `#`
// that starts a token (a comment, whose text still names its path to the model: `#/home/u/x` names
// /home/u/x as `# /home/u/x` does), and `+`, where cmd.exe's copy starts its next source (`copy
// a.txt+\Windows\win.ini out` reads the drive-rooted file; found deciding `+` for the root unit under
// D64(1)). A path may start after each, and a `..` that touches one may climb.
const pathStartDelims = pathDelims + "'()#+"

// plainTokenSafe reports whether tok, a plain token, is built only from the whitelist (D63(2)):
// Unicode letters, marks and digits other than those Windows' ANSI best-fit conversion turns into
// ASCII punctuation (wordRune, bestFitPunct; D64), the project root's mark, the ASCII set
// `- _ . , : @ + /`, `=` and `~` other than at the token's start or after a pathDelims character,
// `#` only at the token's start, an apostrophe between two letters, and `\` before a character
// other than another backslash; a whole `@name` token is not safe. zsh replaces a word that starts
// with `=`, or an assignment's `=` after `:` or `=`, by a command's path (EQUALS, on by default),
// and repeats the character before a `#` under EXTENDED_GLOB (`de#ny.txt` matches deny.txt), while
// every shell reads a `#` that starts a word as a comment; PowerShell splats a variable from a
// whole `@name` argument (`@args`, `@env:HOME`), as it expands `$name`. A backslash is then read
// one of exactly two ways: as a separator (cmd.exe, PowerShell) or as escaping the next character,
// which stays (a POSIX shell); one that ends a token would escape the space after it or join a line
// the store collapsed (`de\ ny.txt`), and one before another backslash would leave a backslash for
// a nested shell to read again, so both make the token unsafe. An apostrophe between letters opens
// or closes a quoted span whose content is literal: the screen reads the text without it
// (screenText), a path may start after it (pathStartDelims), and no root's mark, separator, `~` or
// `..` can touch it. A `%` is never safe here (singlePercent).
func plainTokenSafe(tok string) bool { return safeChars(tok, false) }

// safeChars reports whether tok is built only from the whitelist plainTokenSafe states and, when
// parens is set (a word of a quoted run), `(` and `)`, each read where it stands: as a delimiter after
// which a `~` or a `=` would start a word for a nested shell (so either is unsafe there), and as no
// letter beside an apostrophe. zsh's `(#i)` flag is a `#` inside a word, so it is unsafe too.
func safeChars(tok string, parens bool) bool {
	if strings.HasPrefix(tok, "@") && psSplat.MatchString(tok) {
		return false
	}
	afterDelim := true
	prev := rune(-1)
	for i, r := range tok {
		switch {
		case r == rootMark || wordRune(r):
			afterDelim = false
		case parens && (r == '(' || r == ')'):
			afterDelim = true
		case r == '\'':
			next, _ := utf8.DecodeRuneInString(tok[i+1:])
			if !unicode.IsLetter(prev) || !unicode.IsLetter(next) {
				return false
			}
		case r == '#':
			// A token that starts with `#` is a comment to every shell, so a `#` later in it is too.
			if i > 0 && tok[0] != '#' {
				return false
			}
			afterDelim = false
		case r == '~' || (r == '=' && strings.Trim(tok[i:], "=") != ""):
			// zsh expands `=name`; a run of `=` with no name after it (`=`, `==`) names no command.
			if afterDelim {
				return false
			}
			afterDelim = r == '='
		case r == '=':
			afterDelim = true
		case strings.ContainsRune(pathDelims, r):
			afterDelim = true
		case r == '\\':
			if i+1 == len(tok) || tok[i+1] == '\\' {
				return false
			}
			afterDelim = false
		case strings.ContainsRune(`-_.+/`, r):
			afterDelim = false
		default:
			return false
		}
		prev = r
	}
	return true
}

// wordRune reports whether r is one of the Unicode letters, marks and digits the free-text whitelist
// and the root unit admit: every one but the code points that Windows' ANSI best-fit conversion turns
// into ASCII punctuation (bestFitPunct).
func wordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)) && !unicode.Is(bestFitPunct, r)
}

// bestFitPunct holds the code points that coordinator decision D64's ruling on wave 19f's open items
// takes out of the free-text whitelist and the root unit, on every platform. A program that takes its
// command line through the ANSI code page (a C program's argv, GetCommandLineA) receives a character
// that code page cannot hold as its best fit (WideCharToMultiByte without WC_NO_BEST_FIT_CHARS), and
// some best fits are ASCII punctuation, so a word the whitelist read as letters reaches such a program
// with a quote, an escape or a home in it. The table is every code point of the Spacing Modifier
// Letters block (U+02B0 to U+02FF), where most such mappings lie, and the letters and marks outside it
// that Windows' ANSI code pages 874, 932, 936, 949, 950 and 1250 to 1258 map to an ASCII character
// other than a letter or a digit, as measured on Windows 11
// (TestWhitelist_NoANSIBestFitToPunctuationIsSafe re-measures it): U+01C0 to `|`, U+01C3 to `!`,
// U+0300 to `'` or a backtick, U+0302 to `^`, U+0303 to `~`, U+030E to `"`, and U+0331 and U+0332
// to `_`. Inside the block they include U+02B9, U+02BC and U+02C8 to `'`, U+02BA to `"`, U+02C6 and
// U+02C7 to `^`, U+02CB to `'` or a backtick and U+02CD to `_`. No other modifier letter, and no
// modifier symbol (which the whitelist never admitted), has such a best fit in an ANSI code page;
// the OEM code pages' further ones (U+0301 and U+0308 in code page 437, which no command line is
// converted into) are left as they are.
var bestFitPunct = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x01c0, Hi: 0x01c0, Stride: 1},
		{Lo: 0x01c3, Hi: 0x01c3, Stride: 1},
		{Lo: 0x02b0, Hi: 0x02ff, Stride: 1},
		{Lo: 0x0300, Hi: 0x0300, Stride: 1},
		{Lo: 0x0302, Hi: 0x0303, Stride: 1},
		{Lo: 0x030e, Hi: 0x030e, Stride: 1},
		{Lo: 0x0331, Hi: 0x0332, Stride: 1},
	},
}

// psSplat matches a whole PowerShell splatting argument: `@` and a variable's name, optionally scoped
// or drive-qualified (`@args`, `@env:HOME`).
var psSplat = regexp.MustCompile(`^@[A-Za-z_][A-Za-z0-9_]*(:[A-Za-z_][A-Za-z0-9_]*)?$`)

// tokenOutside reports whether tok names an absolute or escaping path (D63(3)): a simple quoted run
// word by word, with the root's mark followed by a space and a name inside it a sibling (dqSibling),
// or a word (wordOutside).
func tokenOutside(tok string) bool {
	if inner, ok := quoteRun(tok); ok {
		words := dqWords(inner)
		for i, w := range words {
			if wordOutside(w, true) || dqSibling(words, i) {
				return true
			}
		}
		return false
	}
	return wordOutside(tok, false)
}

// wordOutside reports whether w, a token or a word of a quoted run (inRun), names an absolute or
// escaping path in either reading of its backslashes (readingsOutside). A standalone operator or a
// null device redirect names none. An http(s) URL is judged by urlOutside. A word of a quoted run is
// read again with its parentheses removed (a nested zsh's group: `(.)(.)/x` is `../x`).
func wordOutside(w string, inRun bool) bool {
	switch {
	case shellOperators[w] || nullDevice(w):
		return false
	case isURL(w):
		return urlOutside(w)
	}
	return readingsOutside(w) || (inRun && strings.ContainsAny(w, "()") && readingsOutside(noParens.Replace(w)))
}

// readingsOutside reports whether w names an absolute or escaping path read with every `\` a separator
// or with every `\` removed (pathOutside).
func readingsOutside(w string) bool {
	return pathOutside(w) || pathOutside(strings.ReplaceAll(w, `\`, ""))
}

// pathOutside reports whether tok, read with `\` as a separator, names an absolute or escaping path:
// at a path start (pathStarts) one that startsOutside, or a `..` segment anywhere (hasDotDot). The
// root's mark at a path start is the project root.
func pathOutside(tok string) bool {
	return pathStarts(tok, func(s int) bool { return s < len(tok) && startsOutside(tok[s:], s > 0) }) ||
		hasDotDot(tok)
}

// startsOutside reports whether rest, the text at a path start, begins a path outside the project: a
// leading separator (a POSIX root, a drive-less or UNC Windows path), a home `~` (inner: not at the
// token's start, where plainTokenSafe already rejects it), a drive `X:`, a `file:` URL, or a
// PowerShell drive- or provider-qualified path (providerPath).
func startsOutside(rest string, inner bool) bool {
	switch c := rest[0]; {
	case c == '/' || c == '\\':
		return true
	case c == '~' && inner:
		return true
	case len(rest) >= 2 && asciiLetter(c) && rest[1] == ':':
		return true
	}
	return fileScheme(rest) || providerPath(rest)
}

// providerPath reports whether rest, the text at a path start, begins a PowerShell drive- or
// provider-qualified path (D50; the final verify of wave 19d): a name, a `:` and more. PowerShell
// reads the name as a drive (`Temp:secret.txt` is in $env:TEMP; `Env:`, `HKLM:`, `HKCU:`, `Cert:`,
// `Function:`, `Alias:`, `Variable:`, `WSMan:`, and any name New-PSDrive defines), and before a `::`
// as a provider, optionally module-qualified (`Registry::HKEY_CURRENT_USER`,
// `Microsoft.PowerShell.Core\FileSystem::x`). A drive's name holds no separator, `.` or `~` (both
// PowerShell 5.1 and 7 refuse such a name; `git@github.com:org/x` and `127.0.0.1:8080` name no
// drive), and a provider's holds no `/`. A bare name with nothing after its `:` (`Temp:`, `Env:`,
// `HKCU:`, a conventional commit's `fix:` and `feat:`) names a drive root and reveals no file, which
// coordinator decision D64(3) rules inert: it is no path outside the project under D50 and D63, and
// withholding it would hide every conventional commit message. A single-letter bare drive (`C:`) is
// still withheld (startsOutside), and a store cut right after any drive's `:` is withheld
// (cutAtDriveColon). Every other such name is a path outside the project unless it is an inert
// prefix (inertPrefixes), or an http(s) URL's scheme before its `//` (isURL), which the URL rule
// judges (urlTokenSafe, urlOutside).
func providerPath(rest string) bool {
	if isURL(rest) {
		return false
	}
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 || colon+1 >= len(rest) {
		return false
	}
	name := rest[:colon]
	if rest[colon+1] == ':' {
		if strings.ContainsRune(name, '/') {
			return false
		}
		name = name[strings.LastIndexByte(name, '\\')+1:]
	} else if strings.ContainsAny(name, `/\.~`) {
		return false
	}
	return name != "" && !inertPrefixes[strings.ToLower(name)]
}

// inertPrefixes are the names before a `:` that providerPath lets stand, derived from what Qompack's
// own previews must show (the w19d corpus, ADR 0011 §23 item 7(b)): `path`, recall's selector, whose
// value selectorWithheld judges; `sha256`, the text form of the hash expand and re_read take
// (core.Hash); and `select`, ToolSearch's documented selector, with which Claude Code loads a
// deferred tool, Qompack's own among them (`select:mcp__plugin_qompack_qompack__record_eliminated`;
// coordinator decision D67(l), audit 2's finding 31). With an http(s) URL's scheme they are all: no
// PowerShell drive or provider has one of these names unless a user defines it (ADR 0011 §23 item
// 2's limits), and every other name is withheld, `localhost:3000`, `HEAD:x`, `--pretty=format:%h`, a
// plugin's `name:skill`, recall's other selectors (`symbol:`, `tool:`) and a scheme with no `//`
// (`http:x`) among them. What follows an inert prefix's `:` is still a path start.
var inertPrefixes = map[string]bool{"path": true, "sha256": true, "select": true}

// pathStarts reports whether at reports true for one of the offsets in tok where a path may begin:
// the start, just after a leading short option's first letter and just after all its letters
// (`-C../x`, `-I/opt`, `-oD:stash`), and just after each pathStartDelims character (`--out=/etc/x`,
// `a,/etc/x`, `@/tmp/args`). It visits them rather than listing them, so the screen's hot path
// allocates nothing for them (wave 22's verify of audit 2's finding 33).
func pathStarts(tok string, at func(int) bool) bool {
	if at(0) {
		return true
	}
	if k := shortOptionEnd(tok); k > 0 && (at(2) || at(k)) {
		return true
	}
	for i := 0; i < len(tok); i++ {
		if strings.IndexByte(pathStartDelims, tok[i]) >= 0 && at(i+1) {
			return true
		}
	}
	return false
}

// shortOptionEnd is the offset just after a leading short option in tok (`-` and one ASCII letter or
// more), or 0 when tok has none.
func shortOptionEnd(tok string) int {
	if len(tok) < 2 || tok[0] != '-' {
		return 0
	}
	k := 1
	for k < len(tok) && asciiLetter(tok[k]) {
		k++
	}
	if k == 1 {
		return 0
	}
	return k
}

// hasDotDot reports whether tok holds a `..` that may climb (D63(3)): a run of exactly two dots that
// touches the token's start or end, a separator, the root's mark or a pathStartDelims character on
// either side. That is every `..` segment, wherever it stands (`a,../x`, `-C../x`, `<root>/a/../b`: no
// path is resolved, so even one that stays inside the project is withheld), and also a `..` glued to
// a word before it (`cd..`, `type..\x`, which cmd.exe reads as a command and the parent directory,
// wherever a command may stand). A `..` between two name characters is a range (`HEAD~3..HEAD`), and
// `...` a Go package pattern.
func hasDotDot(tok string) bool {
	edge := func(i int) bool {
		return i < 0 || i >= len(tok) || isSep(tok[i]) || tok[i] == rootMark ||
			strings.IndexByte(pathStartDelims, tok[i]) >= 0
	}
	for i := 0; i < len(tok); {
		if tok[i] != '.' {
			i++
			continue
		}
		j := i
		for j < len(tok) && tok[j] == '.' {
			j++
		}
		if j-i == 2 && (edge(i-1) || edge(j)) {
			return true
		}
		i = j
	}
	return false
}

func isSep(c byte) bool       { return c == '/' || c == '\\' }
func asciiLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// namedAt reports whether name, a rule's whole-segment literal or a withheld path's basename or
// relative path in screen form, occurs in form where a name starts (nameStartsAt): a name glued to a
// name character before it is another file (`layout.txt` is not `out.txt`, `café.env` is not `.env`,
// `git fetch` holds no `etc`). Unless open is set it must also end where a name ends (nameEndsAt), so
// `.env.example` is not `.env`; free text leaves the end open (`my secret.txt.bak` holds
// `my secret.txt`).
func namedAt(form, name string, open bool) bool {
	for from := 0; from < len(form); {
		i := strings.Index(form[from:], name)
		if i < 0 {
			return false
		}
		at := from + i
		if nameStartsAt(form, at) && (open || nameEndsAt(form, at+len(name))) {
			return true
		}
		from = at + 1
	}
	return false
}

// nameStartsAt reports whether a name may start at i in t: at its start, after a byte that does not
// continue a name (nameByte), or glued to a short option (`-Csecrets`, flagBefore).
func nameStartsAt(t string, i int) bool {
	return i == 0 || !nameByte(t[i-1]) || flagBefore(t, i)
}

// nameEndsAt reports whether a name may end at i in t: at its end, or, once a run of dots is passed
// (Win32 drops a name's trailing dots, so `.env.` opens `.env`; `.env.example` is another file),
// before a byte that is not a letter, a digit, a non-ASCII character's, `_`, `-` or `~`. Anything
// else that may follow a name in a path (a separator, `: = , @ + # $ %`, a space) may end it: an
// alternate data stream (`.env:x`), a line locator (`deny.txt#L4`), a list.
func nameEndsAt(t string, i int) bool {
	for i < len(t) && t[i] == '.' {
		i++
	}
	if i == len(t) {
		return true
	}
	c := t[i]
	return !(c >= utf8.RuneSelf || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '~')
}

// flagBefore reports whether a short option, `-` and one ASCII letter or more at the start of a word,
// ends just before i in t.
func flagBefore(t string, i int) bool {
	k := i
	for k > 0 && asciiLetter(t[k-1]) {
		k--
	}
	return k < i && k > 0 && t[k-1] == '-' && (k == 1 || !nameByte(t[k-2]))
}

// nameByte reports a byte that continues a file name: an ASCII letter or digit, any byte of a
// non-ASCII character (in a whitelisted token, a Unicode letter, mark or digit), or one of
// `. _ - ~ $ % #`. A `+` does not: cmd.exe's copy starts its next source after one (`copy
// a.txt+.env out`), so a name may start there (pathStartDelims).
func nameByte(c byte) bool {
	return c >= utf8.RuneSelf || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("._-~$%#", c) >= 0
}

// minCutPrefix is the shortest tail of a cut summary that counts as the start of a withheld name: D63
// sets three characters, below which a prefix (`de`, `.e`) is in every other word and names nothing.
const minCutPrefix = 3

// previewEllipsis is what the store appends to a preview it cut (store.previewEllipsis).
const previewEllipsis = "…"

// endsWithPrefixOf reports whether text ends in a prefix of name at least minCutPrefix bytes long
// that, when bounded, starts where a name may start (nameStartsAt).
func endsWithPrefixOf(text, name string, bounded bool) bool {
	n := len(name)
	if n > len(text) {
		n = len(text)
	}
	for k := n; k >= minCutPrefix; k-- {
		if k < len(name) && !utf8.RuneStart(name[k]) {
			continue
		}
		start := len(text) - k
		if text[start:] == name[:k] && (!bounded || nameStartsAt(text, start)) {
			return true
		}
	}
	return false
}

// selectorValues returns the value of each recall path: selector in t, unquoted. A text that does
// not hold `path:` in any ASCII case holds none: no character outside ASCII folds onto those letters
// under RE2's `(?i)`, so the expression is not run on it.
func selectorValues(t string) []string {
	if !containsFolded(t, recallPathSelector+":", true) {
		return nil
	}
	var out []string
	for _, m := range pathSelector.FindAllStringSubmatch(t, -1) {
		v := strings.Trim(m[1], `"'`)
		if v = strings.TrimRight(v, ".,;:"); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// pathSelector finds recall's path: selector (internal/mcp's selectorPath) and its value: a quoted
// stretch, cut short when the preview was, or a word. The selector starts the text or follows
// whitespace, a quote or `=`, as a whitelisted command line spells it (`qompack recall "path:x"`,
// `qompack recall 'path:x'`, `--query=path:x`).
var pathSelector = regexp.MustCompile(`(?i)(?:^|[\s"'=])` + recallPathSelector + `:("[^"]*"?|'[^']*'?|[^\s"']+)`)

// recallPathSelector is the name of recall's path selector (internal/mcp's selectorPath, `path:`),
// whose value the store matches against recorded paths (store.pathSelector.weight).
const recallPathSelector = "path"

// selectorWithheld reports whether v, the value of recall's path: selector, may not be shown: it
// names a path outside the project, or it selects a path the build withholds by the store's own rule
// (selectsKnown). It asks the host nothing.
func (j pathJudge) selectorWithheld(v string) bool {
	if p := judgedSpelling(v); p == "" || !j.inside(p) {
		return p != ""
	}
	return j.selectsKnown(v)
}

// selectsKnown reports whether v, the value of recall's path: selector, selects a path this build
// withholds, by the store's own rule (store.pathSelector.weight over a storeKey-form selector): a
// plain value selects a key it equals, a key it is a path-segment suffix of, and a key it is a
// substring of; a glob selects a key path.Match matches whole, or at any path-segment suffix, and a
// key that spells it literally.
func (j pathJudge) selectsKnown(v string) bool {
	k := selectorKey(v)
	if k == "" {
		return false
	}
	if j.learned != nil && strings.IndexByte(k, 0) < 0 {
		return j.learned.selectsLearned(k, j.known)
	}
	glob := isGlob(k)
	for _, w := range j.known {
		switch {
		case w == k || strings.HasSuffix(w, "/"+k):
			return true
		case !glob:
			if strings.Contains(w, k) {
				return true
			}
		case globSelects(k, w):
			return true
		}
	}
	return false
}

// globSelects reports whether the glob g, path.Match'd on slash paths, matches the key w whole or
// at any of its path-segment suffixes, as the store's glob selector does.
func globSelects(g, w string) bool {
	if ok, _ := path.Match(g, w); ok {
		return true
	}
	for i := 0; i < len(w); i++ {
		if w[i] != '/' {
			continue
		}
		if ok, _ := path.Match(g, w[i+1:]); ok {
			return true
		}
	}
	return false
}

// selectorKey is v as the store keys a path selector (store.storeKey): slash-separated, cleaned,
// without a leading `./`, in paths.Key form. A backslash is a separator here on every platform,
// which only ever selects more.
func selectorKey(v string) string {
	q := strings.TrimSpace(strings.ReplaceAll(judgedSpelling(v), `\`, "/"))
	if q == "" {
		return ""
	}
	q = strings.TrimPrefix(path.Clean(q), "./")
	if q == "." {
		return ""
	}
	return paths.Key(q)
}

// globMeta is the glob syntax a summary's pattern can carry: the store's path-selector grammar
// (store.compilePathSelector), which recall's path: selector and the Glob tool share.
const globMeta = "*?["

// isGlob reports whether p is a pattern rather than a path.
func isGlob(p string) bool { return strings.ContainsAny(p, globMeta) }

// globSelectsKnown reports whether the glob g, a structured value, selects a path this build
// withholds (pathJudge.known). A glob without a separator matches at any depth (rules.Match), as
// recall's path: selector does. Only the paths the build records are known, so a glob that selects
// nothing Qompack recorded is judged as written (D60(iv)). It withholds, so it reads g under the
// broad reading of the root (broadKey; the two-fold rule): every caller has already withheld a g the
// strict reading puts outside the project, and inside it both readings agree.
func (j pathJudge) globSelectsKnown(g string) bool {
	k, ok := j.broadKey(g)
	if !ok {
		return false
	}
	if j.learned != nil {
		return j.learned.matchesLearned(k, j.known, rules.Match)
	}
	for _, w := range j.known {
		if rules.Match(k, w) {
			return true
		}
	}
	return false
}

// key is p's project-relative paths.Key form with forward slashes, and false when p is empty or
// outside the project, under the strict reading of the root (RootRelative).
func (j pathJudge) key(p string) (string, bool) { return j.keyBy(p, RootRelative) }

// broadKey is key under the broad reading of the root (RootRelativeBroad), which only a judgement
// that learns or withholds may take (note, globSelectsKnown).
func (j pathJudge) broadKey(p string) (string, bool) { return j.keyBy(p, RootRelativeBroad) }

// keyBy is key with an absolute p placed by rel, one reading of the root.
func (j pathJudge) keyBy(p string, rel func(root, p string) (string, bool)) (string, bool) {
	p = judgedSpelling(p)
	if p == "" || !j.insideBy(p, rel) {
		return "", false
	}
	if absLike(p) {
		r, ok := rel(j.root, p)
		if !ok {
			return "", false
		}
		p = r
	}
	return paths.Key(strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, `\`, "/")), "./")), true
}

// ruleSegments is spec, a rule's specifier, as the segments of the paths it refuses below its
// anchor, each in screen form, with `..` resolved lexically as hostperm resolves it (cleanLiteral:
// `./private/deny.txt/..` refuses private/**), a backslash read as a separator: hostReadings' reading
// of spec with every backslash a `/`, in screen form. anchored reports a specifier measured from
// outside the project (`//`, `/`, `~` or `~/`, a drive, a leading `..`). The home anchor is where the
// rule is measured from, not a segment; `~name` with no slash after `~` is hostperm's
// project-relative name, not a home.
func ruleSegments(spec string) (segs []string, anchored bool) {
	rd := hostReadingOf(strings.ReplaceAll(strings.TrimSpace(spec), `\`, "/"))
	return screenSegments(rd.segs), rd.anchored
}

// litKind is where a rule's literal stands in the names the rule refuses (literalOf).
type litKind int

const (
	// litWhole is a whole segment (`deny.txt`, `secrets` for `./secrets/**`): every path the rule
	// refuses holds it as a name of its own, so in a structured value it counts only as a whole name.
	litWhole litKind = iota
	// litPrefix is the literal run that starts a glob segment (`secret` for `./secret*`, `.env` for
	// `**/.env*`): every name the rule refuses begins with it, so it counts where a name starts
	// whatever follows, in a structured value too.
	litPrefix
	// litMid is a literal run with `*`, `?` or a class before it (`.env` for `**/*.env`): every name it
	// is in may begin before it, so it counts anywhere.
	litMid
)

// literalOf is a rule's screen literal over segs (ruleSegments; D63(4)), the part every path the
// rule refuses spells: the last segment when it has no glob syntax (`deny.txt`, `.env`), else the
// nearest all-literal segment before it (`secrets` for `./secrets/**`), else the longest literal run
// of the last segment (`.env` for `**/*.env`); "" when there is none (`./**`). kind says how a text
// may hold it (litKind).
func literalOf(segs []string) (lit string, kind litKind) {
	if len(segs) == 0 {
		return "", litWhole
	}
	last := segs[len(segs)-1]
	if !isGlob(last) {
		return last, litWhole
	}
	for i := len(segs) - 2; i >= 0; i-- {
		if !isGlob(segs[i]) {
			return segs[i], litWhole
		}
	}
	run, at := longestLiteralRun(last)
	switch {
	case run == "":
		return "", litWhole
	case at > 0:
		return strings.TrimSpace(run), litMid
	}
	return strings.TrimSpace(run), litPrefix
}

// longestLiteralRun is the longest stretch of seg, a glob segment, outside `*`, `?` and `[…]`, and
// the offset in seg where it starts.
func longestLiteralRun(seg string) (string, int) {
	best, bestAt, start := "", 0, 0
	keep := func(end int) {
		if end-start > len(best) {
			best, bestAt = seg[start:end], start
		}
	}
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '*', '?':
			keep(i)
			start = i + 1
		case '[':
			keep(i)
			if end := strings.IndexByte(seg[i+1:], ']'); end >= 0 {
				i += 1 + end
			} else {
				i = len(seg)
			}
			start = i + 1
		}
	}
	if start < len(seg) {
		keep(len(seg))
	}
	return best, bestAt
}

// screenText is t in the form the screen compares (D63): with ' " ` \ ^ removed, whitespace runs
// collapsed to one space as the store's preview collapses them (store.previewString), and case
// folded where the platform's paths fold (paths.Key). trim drops the outer spaces. Free text that
// passes the whitelist holds only `"` and `\` of those; the others are removed from a rule's
// specifier and a path-named JSON value alike, so a literal and the value spelling it still match.
func screenText(t string, trim bool) string {
	var b strings.Builder
	b.Grow(len(t))
	space := false
	for i := 0; i < len(t); i++ {
		switch c := t[i]; c {
		case '\'', '"', '`', '\\', '^':
		case ' ', '\t', '\n', '\r':
			if !space {
				b.WriteByte(' ')
			}
			space = true
		default:
			b.WriteByte(c)
			space = false
		}
	}
	out := paths.Key(b.String())
	if trim {
		out = strings.TrimSpace(out)
	}
	return out
}

// sanitize is t as the store's preview spells text (store.previewString): control characters
// dropped, whitespace runs collapsed to one space, outer spaces trimmed. It also guarantees that no
// rootMark reaches holdRoot except the ones holdRoot writes.
func sanitize(t string) string {
	var b strings.Builder
	b.Grow(len(t))
	space := false
	for _, r := range t {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// The project root is often spelled with a space in it (C:\Users\John Smith\proj), with either slash
// and any case on Windows. holdRoot holds each such spelling in a text together as one rootMark, a
// control character no sanitized text carries, so the tokenizer never splits the root at its own
// space and the screen never reads the root's own name as a withheld name. In a summary it does so
// only for a root whose spelling admits the unit (markRoot, rootUnitAdmitted, D64(1)): a root with a
// comma, an apostrophe, an `@` or any other character a shell splits or reinterprets a word at, a
// letter whose ANSI best fit is ASCII punctuation, or a word that starts with `-` after a space has
// none, and so has one that no sanitized text spells exactly (rootSpelledExactly). A spelling glued
// to a name character on either side (proj2, xC:\q\proj) is not the root and is judged as the path
// outside the project it is; a spelling glued to a short option is the root as the option's value
// (-I<root>/include).
const rootMark = '\x01'

// rootSpellingOf matches root as a text spells it: its separators repeated or not (a JSON escape
// doubles them), and on Windows the MSYS and WSL spellings of its drive (`/c/`, `/mnt/c/`) and the
// `\\?\` prefix. An ASCII letter's case folds where the platform's paths fold (foldLiteral), and no
// other character's: RE2's `(?i)` folds by Unicode's simple folding, which reads the Kelvin sign
// (U+212A) as `k`, the long s (U+017F) as `s` and the Angstrom sign (U+212B) as `å`, while NTFS keeps
// a name spelled with one a directory beside the name spelled with the letter (wave 19g's final
// verify of D64). Nil for no root. On Linux and macOS a
// spelling's separators are `/` alone, and on Windows one style throughout, every one `/` or every one
// `\`: a POSIX shell (Git Bash on Windows too) drops a backslash and joins the segments around it
// (`/q\proj` is /qproj, `C:/q\proj` is C:/qproj, a sibling of an ancestor of the root), so a spelling
// with a backslash where a POSIX shell would read it that way is not the root, and its text is judged
// as the free text it is (ADR 0011 §23 item 8). A spelling in backslashes alone is the root to cmd.exe
// and PowerShell, whose paths these are. It tracks no quote state (D63): a spelling inside a quoted
// run is marked too, and the run's words are judged around the mark (dqSibling).
func rootSpellingOf(root string) *regexp.Regexp {
	if root == "" {
		return nil
	}
	fold := paths.DefaultFold()
	return rootSpellingWith(root, "", func(s string) string { return foldLiteral(s, fold) })
}

// broadRootSpellingOf matches root as a text spells it, as rootSpellingOf does, with case folded by
// Unicode: compiled with RE2's `(?i)` from root lower-cased by paths.Key, and matched against a text
// lower-cased the same way (holdRootBroad), so a spelling the host's lower-casing or Unicode's simple
// folding pairs with the root's (`Åsa`, the Kelvin sign's `Kate`, `İris` for `iris`) is the root too.
// It is the broad reading of the root, which only a judgement that learns or withholds may take (the
// two-fold rule): rootSpellingOf's comment says why a judgement that shows may not. Nil for no root,
// and where the platform's paths do not fold, since both readings are then rootSpellingOf's.
func broadRootSpellingOf(root string) *regexp.Regexp {
	if root == "" || !paths.DefaultFold() {
		return nil
	}
	return rootSpellingWith(paths.Key(root), "(?i)", regexp.QuoteMeta)
}

// rootSpellingWith is rootSpellingOf's expression for root with flags before it and every character
// of the root's spelling written by lit.
func rootSpellingWith(root, flags string, lit func(string) string) *regexp.Regexp {
	clean := strings.Join(strings.Fields(filepath.ToSlash(filepath.Clean(root))), " ")
	if runtime.GOOS != "windows" {
		return regexp.MustCompile(flags + rootSegments(clean, `/+`, lit))
	}
	fwd, back, rest := "", "", clean
	if len(clean) >= 2 && clean[1] == ':' {
		drive := lit(clean[:2])
		fwd = `(?://[?.]/)?(?:` + drive + `|(?:` + lit("/mnt") + `)?/` + lit(strings.ToLower(clean[:1])) + `)`
		back = `(?:\\{2}[?.]\\)?` + drive
		rest = clean[2:]
	}
	return regexp.MustCompile(
		flags + `(?:` + fwd + rootSegments(rest, `/+`, lit) + `|` + back + rootSegments(rest, `\\+`, lit) + `)`)
}

// rootSegments is rest, a slash-separated stretch of the root's spelling, as a regular expression
// whose every separator is sep and every other character is written by lit.
func rootSegments(rest, sep string, lit func(string) string) string {
	var b strings.Builder
	for _, r := range rest {
		if r == '/' {
			b.WriteString(sep)
			continue
		}
		b.WriteString(lit(string(r)))
	}
	return b.String()
}

// foldLiteral is s as a regular expression that matches s, each ASCII letter as a class of its two
// cases when fold is set (`[kK]`) and every other character quoted as itself: the fold asciiFoldEqual
// compares by, written out for RE2, whose `(?i)` would fold by Unicode instead.
func foldLiteral(s string, fold bool) string {
	var b strings.Builder
	for _, r := range s {
		if fold && r < utf8.RuneSelf && asciiLetter(byte(r)) {
			lower := rune(byte(r) | 0x20)
			b.WriteString("[" + string(lower) + string(lower-('a'-'A')) + "]")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	return b.String()
}

// rootUnitAdmitted reports whether root's own spelling admits the root unit in a summary
// (coordinator decision D64(1)): a sanitized text spells it exactly (rootSpelledExactly), and,
// cleaned and slash-separated, every character of it is a Unicode letter, mark or digit the
// whitelist admits (wordRune), one of `- _ .`, the separator `/`, an ASCII space, or, on Windows, the
// drive's `:` after its letter; and no word of it starts with `-` after one of its spaces. These are
// the free-text whitelist's characters at which no shell splits or reinterprets a word. D64's
// rulings on wave 19f's open items left out the letters and marks that Windows' ANSI best-fit
// conversion turns into ASCII punctuation (bestFitPunct: a program reading an ANSI command line
// receives U+02BA as `"`), and a word that starts with `-` after a space, which PowerShell binds as a
// parameter of a cmdlet or advanced function (`g C:\q\John -Force\proj` sets `-Force`); a lone `-`,
// which PowerShell passes as text (OneDrive's `OneDrive - Contoso`), is such a word too, as the
// ruling words it. The whitelist's other characters are left out: `,` (PowerShell splits a bare
// argument into an array there, and cmd.exe's built-in commands split at it), `=` (cmd.exe's
// built-in commands split at it), `+` (cmd.exe's copy starts its next source there), `#` (zsh's
// EXTENDED_GLOB repeats the character before it), `@` (PowerShell splats a word of the root that is
// a whole `@name`, as in a root ending in ` @Work`) and a `:` past the drive (PowerShell reads a
// name before a `:` as a drive; a list's reader splits at it). So is every character the whitelist
// rejects: a quote of any kind, a backtick, `$ ! ; & | ( ) [ ] { } < > ^ % ~ * ?`, a backslash that
// is no separator (a POSIX shell drops it), a control character and a Unicode space. A root holding
// any of them, or a run of spaces, has no unit: a summary spelling it is judged as the free text it
// is, and withheld. An empty root admits nothing.
func rootUnitAdmitted(root string) bool {
	if root == "" || !rootSpelledExactly(root) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(root))
	if strings.Contains(clean, " -") {
		return false
	}
	for i, r := range clean {
		switch {
		case wordRune(r):
		case r == '/' || r == ' ' || strings.ContainsRune(rootUnitChars, r):
		case r == ':' && i == 1 && runtime.GOOS == "windows" && asciiLetter(clean[0]):
		default:
			return false
		}
	}
	return true
}

// rootUnitChars are the ASCII characters besides `/` and the space that a root's spelling may hold
// and keep its unit (rootUnitAdmitted).
const rootUnitChars = "-_."

// rootSpelledExactly reports whether a sanitized text (sanitize, the store's preview) can spell root
// exactly, as rootSpellingOf finds it: cleaned and slash-separated, it holds no control character and
// no whitespace but single ASCII spaces between other characters. rootSpellingOf folds each run of
// whitespace in the root, a tab or a Unicode space among it, to one ASCII space, as sanitize folds a
// text's, so under any other root what it finds may be a sibling spelled with one space (`a b` beside
// a root `a  b`, `a<U+00A0>b` or `a<TAB>b`, or `proj` beside a root `proj `). Such a root is held in
// no text: neither in a summary (rootUnitAdmitted) nor in a drop reason (reasonWithheld), where the
// root's own whitespace then splits a path under it and its first piece is outside the project.
func rootSpelledExactly(root string) bool {
	clean := filepath.ToSlash(filepath.Clean(root))
	return sanitize(clean) == clean && strings.Join(strings.Fields(clean), " ") == clean
}

// markRoot is t, a sanitized summary text, with the project root held together as one rootMark
// (holdRoot) when the root's own spelling admits the unit (rootUnitAdmitted, D64(1)), and t as it is
// otherwise, so that a summary spelling such a root is judged as the free text it is.
func (j pathJudge) markRoot(t string) string {
	if !j.rootUnit {
		return t
	}
	return j.holdRoot(t)
}

// holdRoot is t, a sanitized text, with each contiguous spelling of the project root replaced by
// rootMark. A spelling glued to a name character before it is not the root, unless that is a short
// option's letters (the root as the option's value, flagBefore); and a spelling is the root only when
// it ends the text or is followed by what ends a path's root (rootEndsAt). Any other spelling is
// judged as the path outside the project it is. A summary holds the root only through markRoot; a
// drop reason (reasonWithheld) holds it whenever a text can spell it exactly (rootSpelledExactly);
// and the learning of a withheld path (recordedPath) holds it always. The last two, which learn or
// withhold, also hold it under the broad reading (holdRootBroad).
func (j pathJudge) holdRoot(t string) string { return j.held(j.rootSpelling, t) }

// holdRootBroad is holdRoot under the broad reading of the root (broadRootSpellingOf): t lower-cased
// by paths.Key, with each spelling of the root that Unicode's case folding pairs with its own held as
// one rootMark. Where the platform's paths do not fold it is holdRoot. Only a judgement that learns or
// withholds reads it (recordedPath, reasonWithheld; the two-fold rule). It is memoized under its own
// expression (held), so the strict and the broad readings of one text never share an answer.
func (j pathJudge) holdRootBroad(t string) string {
	if j.broadSpelling == nil {
		return j.holdRoot(t)
	}
	return j.held(j.broadSpelling, paths.Key(t))
}

// held is holdWith(spelling, t) memoized for the build (buildMemo.held, keyed by the expression, one
// for each reading of the root, and the text). A text that cannot hold rootSpelling's match, because
// it does not hold the root's last segment folded as rootSpelling folds it (rootTail: an ASCII
// letter's case where the platform's paths fold, no other character's), is returned as it is without
// running the expression; another reading's expression is always run.
func (j pathJudge) held(spelling *regexp.Regexp, t string) string {
	if spelling == nil {
		return t
	}
	if spelling == j.rootSpelling && j.rootTail != "" && !containsFolded(t, j.rootTail, paths.DefaultFold()) {
		return t
	}
	if j.memo == nil {
		return holdWith(spelling, t)
	}
	k := heldKey{spelling, t}
	if s, ok := j.memo.held[k]; ok {
		return s
	}
	s := holdWith(spelling, t)
	j.memo.held[k] = s
	return s
}

// rootTailOf is root's rootTail: the last segment of the spelling rootSpellingOf matches, an ASCII
// letter lower-cased where the platform's paths fold; "" for no root, or one with no last segment.
func rootTailOf(root string) string {
	if root == "" {
		return ""
	}
	clean := strings.Join(strings.Fields(filepath.ToSlash(filepath.Clean(root))), " ")
	tail := clean[strings.LastIndexByte(clean, '/')+1:]
	if strings.Contains(tail, ":") {
		return "" // a drive alone (`C:`), whose spellings rootSpellingOf varies
	}
	if paths.DefaultFold() {
		tail = asciiLower(tail)
	}
	return tail
}

// asciiLower is s with each ASCII letter lower-cased and every other byte as it is.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// containsFolded reports whether t holds sub, sub's ASCII letters lower-case, an ASCII letter's case
// folded when fold is set and no other character's (asciiFoldEqual).
func containsFolded(t, sub string, fold bool) bool {
	if !fold {
		return strings.Contains(t, sub)
	}
	for i := 0; i+len(sub) <= len(t); i++ {
		if asciiFoldEqual(t[i:i+len(sub)], sub, true) {
			return true
		}
	}
	return false
}

// holdWith is t with each spelling of the root that spelling finds, and holdRoot's rules leave the
// root, replaced by rootMark; t when spelling is nil.
func holdWith(spelling *regexp.Regexp, t string) string {
	if spelling == nil {
		return t
	}
	var b strings.Builder
	last := 0
	for _, m := range spelling.FindAllStringIndex(t, -1) {
		a, e := m[0], m[1]
		if a > 0 && nameByte(t[a-1]) && !flagBefore(t, a) {
			continue
		}
		if !rootEndsAt(t[a:e], t[e:]) {
			continue
		}
		b.WriteString(t[last:a])
		b.WriteByte(rootMark)
		last = e
	}
	b.WriteString(t[last:])
	return b.String()
}

// rootEndsAt reports whether rest, the text after spelling (a spelling of the project root in a
// text), leaves the spelling the root: it is empty, or starts with a space, a quote or a `/`; a `|`,
// which every shell reads as a pipe and no Windows name holds; a `;` that ends its token, where a
// POSIX shell and PowerShell end the statement (cmd.exe passes `<root>;` to a program whole, a name
// that is the root's own spelling and a `;`, revealing no other name; ADR 0011 §23 item 8); or a `\`
// only after a spelling that itself uses backslashes. After a forward-slash spelling a POSIX shell
// reads a `\` as escaping the next character onto the root's last segment (`/q/proj\old` is
// /q/projold, a sibling), and any other byte (`proj2`, `proj.bak`, `proj,x`, `proj:x`, `proj=x`,
// `proj;x`) continues the name into a sibling for some program, so the spelling is not marked.
func rootEndsAt(spelling, rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '"', '\'', '/', '|':
		return true
	case ';':
		return len(rest) == 1 || rest[1] == ' '
	case '\\':
		return strings.IndexByte(spelling, '\\') >= 0
	}
	return false
}

// jsonShaped reports whether s may be a canonical-JSON preview: valid JSON (an object, an array or a
// string), or one the store cut at its width (it starts an object or an array with a string and
// ends in `…`). A cut one is read as JSON only when its grammar holds (jsonStrings).
func jsonShaped(s string) bool {
	if s == "" || strings.IndexByte(`{["`, s[0]) < 0 {
		return false
	}
	return json.Valid([]byte(s)) ||
		(strings.HasSuffix(s, previewEllipsis) && (strings.HasPrefix(s, `{"`) || strings.HasPrefix(s, `["`)))
}

// jsonStr is one string of a JSON preview, decoded; whether it is the value of a path-named argument
// (pathArgName), directly or as an element of that argument's array; and whether the store's cut fell
// inside it.
type jsonStr struct {
	value     string
	pathNamed bool
	cut       bool
}

// jsonStrings returns every string of s, a JSON preview possibly cut at the store's width (its `…`
// is the store's, and a string it falls inside runs to the end), in order. A string followed by `:`
// is an object's key, and the strings after it, up to the next `,` of that object, are its value's.
// grammatical reports that every byte outside the strings is JSON's own (jsonGrammar): a command
// that merely starts like JSON (`{"x":1} && cat …`) is not a preview, and is screened whole.
func jsonStrings(s string) (out []jsonStr, grammatical bool) {
	type frame struct {
		array bool
		key   string
	}
	var (
		stack   []frame
		pending string
	)
	cut := !json.Valid([]byte(s)) && strings.HasSuffix(s, previewEllipsis)
	if cut {
		s = strings.TrimSuffix(s, previewEllipsis)
	}
	grammatical = true
	top := func() frame {
		if len(stack) == 0 {
			return frame{}
		}
		return stack[len(stack)-1]
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			stack = append(stack, frame{})
			pending = ""
		case '[':
			key := pending
			if f := top(); f.array {
				key = f.key
			}
			stack = append(stack, frame{array: true, key: key})
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ',':
			if !top().array {
				pending = ""
			}
		case '"':
			end := i + 1
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			runsOut := end >= len(s)
			if end > len(s) {
				end = len(s)
			}
			str := jsonStr{value: jsonUnquote(s[i+1 : end]), cut: cut && runsOut}
			next := end + 1
			for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
				next++
			}
			f := top()
			switch {
			case !f.array && next < len(s) && s[next] == ':':
				pending = str.value
			case f.array:
				str.pathNamed = pathArgName.MatchString(f.key)
			default:
				str.pathNamed = pathArgName.MatchString(pending)
			}
			out = append(out, str)
			i = end
		default:
			grammatical = grammatical && strings.IndexByte(jsonGrammar, s[i]) >= 0
		}
	}
	return out, grammatical
}

// jsonGrammar is every byte JSON holds outside its strings, besides the brackets, commas and quotes
// jsonStrings reads: `:`, whitespace, a number's characters, and the letters of true, false and null.
const jsonGrammar = ": \t\r\n0123456789+-.eEtrufalsn"

// pathArgName matches an argument name that holds a path: path, file_path, notebook_path, filepath,
// paths, file, filename, dir, directory, folder, cwd and their kin.
var pathArgName = regexp.MustCompile(
	`(?i)(paths?$|(^|[_-])(files?|file_?names?|dirs?|directory|directories|folders?|cwd)$)`)

// jsonUnquote decodes one JSON string body; a body the preview cut mid-escape is undone by hand.
func jsonUnquote(s string) string {
	if plainJSONBody(s) {
		return s
	}
	var out string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &out); err == nil {
		return out
	}
	return jsonEscapes.Replace(s)
}

// plainJSONBody reports whether s, a JSON string's body, decodes to itself: it holds no escape, no
// quote, no control character and no invalid UTF-8 (which encoding/json would replace), so
// jsonUnquote need not decode it (wave 22's verify of audit 2's finding 33: decoding every string
// was a tenth of a build's allocations).
func plainJSONBody(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' || c == '"' || c < 0x20 {
			return false
		}
	}
	return utf8.ValidString(s)
}

// jsonEscapes undoes the escapes a body the preview cut mid-escape may still hold (jsonUnquote).
var jsonEscapes = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\/`, `/`)

// withheldPathLabel, withheldSummary and withheldPathNote replace what a withheld pointer would have
// shown. None names the rule or the path: a rule spells the very path it protects. Section 6 explains
// a withheld pointer once, in pointersLegend under its heading, so each withheld line there carries
// only its short label (audit 2's finding 30: the 97-character explanation on every withheld line,
// charged to the payload's fixed character ceiling, pushed real pointers out of the section). A drop
// entry keeps withheldPathNote, since dropped() returns it without section 6's legend.
const (
	withheldPathNote  = "path withheld: the host's permission rules refuse it, or it is outside the project"
	withheldPathLabel = "file (path withheld)"
	withheldSummary   = "(summary withheld)"
	// pointersLegend is the line under section 6's heading when a pointer the section was built from
	// is withheld (sectionLegend).
	pointersLegend = "Withheld entries name a path the host's permission rules refuse, or one outside the " +
		"project; restore them by hash."
	// withheldDropID stands in for the path a checkpoint drop entry was keyed by when the
	// checkpoint no longer holds the pointer's hash (gateCheckpointDrops), and for the path a drop
	// entry's reason named (redactReason).
	withheldDropID = "(path withheld)"
)

// checkpointPathDrops are the drop kinds internal/checkpoint keys by a file pointer's path, as the
// pointer recorded it: a pointer cut at the checkpoint's own budget (truncate.go cutFilePointers)
// and the ground-truth checks finalize always runs (validate.go ValidatePointers). Every other kind
// is keyed by an id, a record or nothing (TestCheckpointPathDrops_AreTheCheckpointersOwn).
var checkpointPathDrops = map[string]bool{
	"file_pointer":      true,
	"pointer_missing":   true,
	"pointer_invalid":   true,
	"pointer_untracked": true,
	"pointer_dirty":     true,
}

// modelTextDrops are the drop kinds whose reason is the model's own earlier text: an elimination's
// already_tried(target, approach) call (this package's), and an open question's text (the
// checkpointer's truncate.go cutOpenQuestions). Like sections 3 and 4, they stay outside D50
// (D60(i)), and redacting them would break the call that restores them.
var modelTextDrops = map[string]bool{
	dropKindElimination: true,
	"open_question":     true,
}

// dropWithheld reports whether a path-keyed checkpoint drop keyed by id may not be shown
// (gateCheckpointDrops): its path is one newPathJudge read past the drops' bound and answered as
// refused unjudged (unjudged), and no judgement since has asked the host about it; or withheld()
// withholds it, which asks the host nothing more, since newPathJudge judged or bounded every drop.
func (j pathJudge) dropWithheld(id string) bool {
	p := judgedSpelling(id)
	if _, asked := j.judged[p]; !asked && j.unjudged[p] {
		return true
	}
	return j.withheld(id)
}

// gateCheckpointDrops returns r's checkpoint drop entries with every one keyed by a path the
// payload may not show named the way section 6 names its pointer (D50, C4.6): by the pointer's hash
// while the checkpoint still holds it (finalize keeps an untracked or a dirty pointer), and by
// withheldDropID otherwise. The detail keeps the checkpointer's reason, without a re_read(path)
// call re_read would refuse, says the path is withheld, and restores by hash when the hash is
// known. Section 7 and dropped() both read the result. r's slice is never modified. j is the build's
// judge (newPathJudge), which read these entries' paths before this withholds them.
func gateCheckpointDrops(r Request, j pathJudge) []checkpoint.DropEntry {
	drops := r.Checkpoint.Dropped
	keyed := false
	for _, e := range drops {
		keyed = keyed || (checkpointPathDrops[e.Kind] && e.ID != "")
	}
	if !keyed {
		return drops
	}
	out := make([]checkpoint.DropEntry, len(drops))
	for i, e := range drops {
		out[i] = e
		if checkpointPathDrops[e.Kind] && j.dropWithheld(e.ID) {
			out[i] = withheldDrop(e, r.Checkpoint.Pointers.Files)
		}
	}
	return out
}

// gateDropReasons returns drops with every reason that shows an absolute path outside the project
// or a path the build withholds redacted (D63(3)), except the model's own text (modelTextDrops) and
// section 6's own pointer drops, whose reason names a pointer's path only once withheld() has let
// section 6 show it (buildPointers). The path goes and the error kind stays: pointer_git_unavailable
// carries the checkpointer's git error verbatim, which in a linked worktree names its gitdir outside
// the project, and a rule or skill scan error can name a directory above it. drops is never
// modified. Each distinct reason is screened once per build (gatedReasonOf): Build gates the drop
// list at step 9a and again at step 10, where it differs only by what min-fill re-admitted between
// them, and most drops share one reason.
func gateDropReasons(drops []checkpoint.DropEntry, j pathJudge) []checkpoint.DropEntry {
	var out []checkpoint.DropEntry
	for i, e := range drops {
		if modelTextDrops[e.Kind] || e.Kind == dropKindPointer {
			continue
		}
		g := j.gatedReasonOf(e.Detail)
		if !g.withheld {
			continue
		}
		if out == nil {
			out = append([]checkpoint.DropEntry(nil), drops...)
		}
		out[i].Detail = g.text
	}
	if out == nil {
		return drops
	}
	return out
}

// gatedReasonOf is detail's screen answer: whether it shows a path the build withholds
// (reasonWithheld) and, when it does, its redacted text (redactReason), memoized for the build
// (buildMemo.reasons).
func (j pathJudge) gatedReasonOf(detail string) gatedReason {
	if j.memo != nil && j.memo.reasons != nil {
		if g, ok := j.memo.reasons[detail]; ok {
			return g
		}
	}
	var g gatedReason
	if j.reasonWithheld(detail) {
		g = gatedReason{withheld: true, text: j.redactReason(detail)}
	}
	if j.memo != nil && j.memo.reasons != nil {
		j.memo.reasons[detail] = g
	}
	return g
}

// reasonWithheld reports whether a drop entry's reason shows an absolute path outside the project or
// names a path the build withholds. A reason is Qompack's own prose with paths set in it whole (a
// restore call, a git error's path), not a command that may glue a name to anything, so the screen is
// a simple product-string rule (D63): an operation on a path outside the project (operationOutside,
// which reads a `verb <path>` chain and judges the path whole), a whitespace-delimited token that is
// an absolute path outside the project, or a withheld path's whole spelling at a path boundary
// (namesKnownIn). Neither a withheld file's basename alone nor a rule's literal withholds it: an
// allowed README.md or src/CLAUDE.md beside a withheld private/README.md or ~/.claude/CLAUDE.md is a
// different file, which section 6 may show too.
func (j pathJudge) reasonWithheld(detail string) bool {
	t := unslashCommands(sanitize(detail))
	if t == "" {
		return false
	}
	if j.operationOutside(t) {
		return true
	}
	// The root is held together (holdRoot), as D63(1) holds it in a summary, so the first piece of a
	// root with a space (`C:\q\John`) is not read as a path outside the project, and a withheld path
	// named absolutely (`<root>/private/x`) starts a path run (pathRunStart). A reason is no shell
	// command, so D64(1)'s character set does not apply: the root is held whatever characters it
	// holds, but only when the text can spell it exactly (rootSpelledExactly). Otherwise what holdRoot
	// finds may be a sibling spelled with one space, and the reason is judged with the root unheld,
	// which fails closed: the root's own whitespace splits a path under it, outside the project.
	// The judgement withholds, so the reason is withheld when it is under either reading of the root
	// (the two-fold rule): with the root held as its own spelling folds ASCII letters, and as the broad
	// reading folds case by Unicode (holdRootBroad), where `path=C:\q\Åsa\proj\private\deny.txt` names
	// the withheld private/deny.txt of a root `C:\q\åsa\proj`.
	if !j.rootExact {
		return j.reasonNamesWithheld(t)
	}
	return j.reasonNamesWithheld(j.holdRoot(t)) ||
		(j.broadSpelling != nil && j.reasonNamesWithheld(j.holdRootBroad(t)))
}

// reasonNamesWithheld reports whether marked, a sanitized drop reason with the project root held as
// rootMark under one reading of the root (reasonWithheld), holds a whitespace-delimited token that is
// an absolute path outside the project, or names a withheld path's whole spelling (namesKnownIn).
func (j pathJudge) reasonNamesWithheld(marked string) bool {
	for _, w := range strings.Fields(marked) {
		// A path names a directory, so it holds a separator; a bare `~36800` or `25000-token` is an
		// approximate count, not a home path, though homeOrVarRoot would match the leading `~`.
		p := judgedSpelling(strings.ReplaceAll(strings.Trim(w, ".,;:"), string(rootMark), j.root))
		if p != "" && strings.ContainsAny(p, `/\`) && absLike(p) && !j.inside(p) {
			return true
		}
	}
	return j.namesKnownIn(screenText(strings.ReplaceAll(marked, `\`, "/"), false))
}

// unslashCommands is t, a sanitized drop reason, with the leading `/` taken from each word that is one
// of Qompack's own slash commands (qompackCommand), so the reason screen reads it as the command it
// is, not as an absolute path outside the project: the checkpointer's recovery instruction for pins
// it could not re-read at the seal (`run /qompack:pin --list: <err>`) was redacted to `(path
// withheld): <err>` (audit 2's finding 27). Only the screen reads the result; a redacted reason is
// cut from the reason as written, which keeps the command. Any other word led by `/`, a single
// segment or more, is still judged as a path.
func unslashCommands(t string) string {
	if !strings.Contains(t, "/"+qompackCommandPrefix) {
		return t
	}
	words := strings.Split(t, " ")
	for i, w := range words {
		if qompackCommand(strings.TrimRight(w, ".,;:")) {
			words[i] = w[1:]
		}
	}
	return strings.Join(words, " ")
}

// qompackCommandPrefix is what every one of Qompack's own slash commands starts with after its `/`
// (internal/commands).
const qompackCommandPrefix = "qompack:"

// qompackCommand reports whether w is one of Qompack's own slash commands: `/qompack:` and a command
// name of lower-case ASCII letters, digits and `-` (`/qompack:pin`, `/qompack:status`).
func qompackCommand(w string) bool {
	name, ok := strings.CutPrefix(w, "/"+qompackCommandPrefix)
	return ok && name != "" && strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}) < 0
}

// operationOutside reports whether a part of t, a reason read as an error chain joined by ": ", is a
// path outside the project, whole (git's `not a git repository: <path>`), or an operation on one, as
// Go's PathError and the Windows file APIs spell it (`open <path>`, `CreateFile <path>`: a word of
// letters, a space, the path to the next ": "). The path is judged whole, as a pointer's is, so a
// gitdir whose name is the root's own last segment, a space and more (`<root> main\.git`), or a
// single segment (`/repo.git`), is outside the project.
func (j pathJudge) operationOutside(t string) bool {
	for _, part := range strings.Split(t, ": ") {
		if p := judgedSpelling(part); absLike(p) && !j.inside(p) {
			return true
		}
		op, p, ok := strings.Cut(part, " ")
		if !ok || op == "" || strings.IndexFunc(op, notOperationRune) >= 0 {
			continue
		}
		if p = judgedSpelling(p); absLike(p) && !j.inside(p) {
			return true
		}
	}
	return false
}

// notOperationRune reports a rune that an operation's name (`open`, `CreateFile`) does not hold.
func notOperationRune(r rune) bool { return r >= utf8.RuneSelf || !asciiLetter(byte(r)) }

// namesKnownIn reports whether t, in screen form with separators as `/`, names a path the build
// withholds (knownPaths) as a path of its own: it starts where a path starts (pathRunStart) and ends
// at a word or path-segment boundary (a closing `.` before a boundary counts as one).
func (j pathJudge) namesKnownIn(t string) bool {
	if j.learned != nil {
		return j.learned.paths.namesPathIn(t)
	}
	for _, k := range j.knownPaths {
		for from := 0; from+len(k) <= len(t); {
			i := strings.Index(t[from:], k)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(k)
			if reasonEndsAt(t, end) && pathRunStart(t, start) {
				return true
			}
			from = start + 1
		}
	}
	return false
}

// pathRunStart reports whether a path named at start in t, a reason in screen form with separators
// as `/`, begins a path there: at the start of t, after a boundary other than a separator, or after a
// separator that follows the root's mark or a leading `.` (`<root>/private/x`, `./private/x`). After
// any other separator it is the tail of a longer path (`docs/README.md` is not `README.md`).
func pathRunStart(t string, start int) bool {
	if start == 0 {
		return true
	}
	if c := t[start-1]; c != '/' {
		return strings.IndexByte(reasonBoundary, c) >= 0
	}
	if start < 2 {
		return false
	}
	switch b := t[start-2]; {
	case b == rootMark:
		return true
	case b == '.':
		return start == 2 || strings.IndexByte(reasonBoundary, t[start-3]) >= 0
	}
	return false
}

// reasonBoundary is what may stand on either side of a path named in a drop entry's reason.
const reasonBoundary = " /()[]{}=:,;@|&<>" + string(rootMark)

// redactReason is detail with the path it shows replaced by withheldDropID. A reason is read as an
// error chain, its parts joined by ": " (`checkpoint: git index unsupported: reading index: open
// <path>: The system cannot find the file specified.`), and each part as clauses joined by "; ": the
// parts before the first that shows the path stay; in that part only the clauses that show it go
// (redactClauses); and each later part that holds no separator (the system's own message) stays, up
// to the first that holds one. A reason whose path no part holds alone is replaced whole.
func (j pathJudge) redactReason(detail string) string {
	parts := strings.Split(detail, ": ")
	for i, p := range parts {
		if !j.reasonWithheld(p) {
			continue
		}
		return strings.Join(append(append(parts[:i:i], j.redactClauses(p)), j.trailing(parts[i+1:])...), ": ")
	}
	return withheldDropID
}

// redactClauses is p, the part of a reason that shows a path, with the "; " clause that shows it
// replaced by withheldDropID: the clauses before it stay, and those after it while they hold no
// separator. A part whose path no clause holds alone is replaced whole.
func (j pathJudge) redactClauses(p string) string {
	clauses := strings.Split(p, "; ")
	for i, c := range clauses {
		if !j.reasonWithheld(c) {
			continue
		}
		return strings.Join(append(append(clauses[:i:i], withheldDropID), j.trailing(clauses[i+1:])...), "; ")
	}
	return withheldDropID
}

// trailing is the pieces of a reason after the one that showed a path, up to the first that holds a
// separator or shows a path itself: a later piece with a separator may be the rest of that path (a
// POSIX name may hold ": " or "; "), so it and everything after it go.
func (j pathJudge) trailing(pieces []string) []string {
	for k, q := range pieces {
		if strings.ContainsAny(q, `/\`) || j.reasonWithheld(q) {
			return pieces[:k]
		}
	}
	return pieces
}

// withheldDrop is e, a path-keyed checkpoint drop entry, with its path withheld.
func withheldDrop(e checkpoint.DropEntry, files []checkpoint.FilePointer) checkpoint.DropEntry {
	var h core.Hash
	for _, f := range files {
		if f.Path == e.ID {
			h = f.Hash
			break
		}
	}
	id := withheldDropID
	if h != (core.Hash{}) {
		id = h.String()
	}
	// The checkpointer's details are a reason, then "; " and the call that resolves the path.
	reason, _, _ := strings.Cut(e.Detail, "; ")
	detail := withheldPathNote
	if reason != "" {
		detail = reason + "; " + detail
	}
	if p := hashPointer(h); p != "" {
		detail += restorePrefix + p
	}
	return checkpoint.DropEntry{Kind: e.Kind, ID: id, Detail: detail}
}

// hashPointer is the restore call for content known only by its hash.
func hashPointer(h core.Hash) string {
	if h == (core.Hash{}) {
		return ""
	}
	return "expand(hash=" + h.String() + ")"
}
