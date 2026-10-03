package rehydrate

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
// a call that has none of them, cut at 120 bytes with `…`. Coordinator decision D61 rules how it is
// judged (ADR 0011 §23.5), after two rounds that tried to find every path inside arbitrary text:
//
//   - A structured summary, the store's preview of one path argument (a Read's, Write's or Edit's
//     file_path, a lone Glob or Grep argument: a summary that is one word once the project root's
//     own spelling is held together, or one that starts at the root and goes on below it) or a
//     path-named argument of a canonical-JSON preview, is judged whole, as a file pointer's path is:
//     the host's rules and containment, once per build. Only a rooted plain path is judged so alone;
//     any other value is screened as free text too (classify).
//   - Everything else is free text (commands, queries, prompts, other JSON arguments) and costs no
//     host judgement. It is screened (freeTextWithheld) for the literal part of every Read deny or
//     ask rule (screenLiteral), for the basename or relative path of every path the build withholds
//     where a name starts, for an absolute path outside the project, and for recall's path: selector
//     selecting a withheld path; when the rules are unavailable it is withheld whole.
//   - A summary the store cut is also withheld when it ends in a prefix of one of those names
//     (truncatedWithheld), and the piece the cut fell inside is read as the start of what it was
//     (cutValueWithheld, cutRoot), never as a whole path or name.
//
// The checkpointer's own drop entries are pointers too: five kinds are keyed by a file pointer's
// path (checkpointPathDrops), and section 7 and dropped() name a withheld one by hash
// (gateCheckpointDrops); and no drop entry's reason shows an out-of-project or withheld path
// (redactReason).

// HostRules is one build's snapshot of the host's current Read rules (HostPaths).
type HostRules struct {
	// Refuses reports whether the rules deny, or ask before, reading path (as a pointer records it:
	// project-relative or absolute). Nil means the rules could not be established: every path, and
	// every free-text summary, is then withheld (re_read fails closed the same way).
	Refuses func(path string) bool
	// Patterns are the path specifiers of every Read deny and ask rule in force, as the settings spell
	// them inside Read(…) (`./private/deny.txt`, `./secrets/**`, `**/*.env`), and "" for a rule that
	// refuses every read. Free text is screened by each one's literal part (screenLiteral).
	Patterns []string
}

// HostPaths, when set on Deps, returns this build's snapshot of the host's current Read rules. A nil
// HostPaths applies containment alone. Build calls it once per build, and calls Refuses once for
// each distinct file pointer path, path-keyed checkpoint drop and structured summary, from one
// goroutine; free text never reaches it.
type HostPaths func() HostRules

// pathJudge decides, for one build, which recorded paths and summaries the payload may show.
type pathJudge struct {
	root    string
	host    bool
	refuses func(string) bool
	// rootSpelling finds the project root spelled in a text (rootSpellingOf), which protect holds
	// together as one rootMark; nil when there is no root.
	rootSpelling *regexp.Regexp
	// rootKey is the project root cleaned, slash-separated and in screen form (screenText), and
	// rootSegs its segments: a rule anchored outside the project is matched against them (rootCover),
	// and a cut stretch that begins rootKey is the root (rootPrefix).
	rootKey  string
	rootSegs []string
	// screens are the rules' literals in screen form; screenAll is set when one rule's literal cannot
	// tell texts apart (screenLiteral, rootCover), and then every free-text summary is withheld.
	screens   []string
	screenAll bool
	// midScreens are the screens taken from a glob's literal run with `*`, `?` or a class before it
	// (`.env` for `**/*.env`): every name they occur in may begin before them, so a cut summary's tail
	// need not start at a boundary to begin one (truncatedWithheld).
	midScreens []string
	// rulePaths are the rules' specifiers in screen form, which a cut summary's tail may begin.
	rulePaths []string
	// known holds every concrete in-project path the build withholds, in project-relative paths.Key
	// form: a structured glob, or recall's path: selector, that selects one is withheld.
	known []string
	// knownText holds the basename and the relative path of every path the build withholds (a file
	// pointer's, a path-keyed checkpoint drop's, a structured summary's) in screen form: free text
	// that holds one where a name starts names the withheld file. Nothing read from free text, and no
	// fragment the store's cut left, is ever added.
	knownText []string
	// knownPaths holds the whole spelling of every path the build withholds, in screen form with `/`
	// separators: project-relative for one in the project, as written for one outside it. A drop
	// reason names a withheld path by one of them (namesKnownIn).
	knownPaths []string
	// judged memoizes the host's judgement (hostRefuses) for the build: a path is named by a file
	// pointer and again by a summary, and each host judgement may consult the filesystem.
	judged map[string]bool
}

// screenBy adds each rule pattern's literal and specifier to the build's screens (D61(2)(a)). A rule
// with no literal refuses everything it is anchored at; a rule anchored outside the project is also
// matched against the root, segment by segment (rootCover), since its literal may lie in the root's
// own path where no project-relative spelling holds it.
func (j *pathJudge) screenBy(patterns []string) {
	for _, p := range patterns {
		segs, anchored, fromStart := ruleSegments(p)
		lit, mid := literalOf(segs)
		if lit == "" || (anchored && j.rootCover(segs, fromStart)) {
			j.screenAll = true
			continue
		}
		j.addScreen(lit, mid)
		if rp := screenText(strings.TrimPrefix(strings.TrimSpace(p), "./"), true); rp != "" {
			j.rulePaths = appendDistinct(j.rulePaths, rp)
		}
	}
}

// addScreen adds lit, a rule's literal in screen form, to the build's screens; mid marks one that
// may begin inside a name.
func (j *pathJudge) addScreen(lit string, mid bool) {
	j.screens = appendDistinct(j.screens, lit)
	if mid {
		j.midScreens = appendDistinct(j.midScreens, lit)
	}
}

// rootCover matches segs, a rule anchored outside the project (below its anchor, ruleSegments),
// against the project root's segments, from the filesystem root when fromStart and from any of them
// otherwise (a home or settings anchor is not known here, so every alignment is assumed). It reports
// true when the rule may refuse the root itself, or a path below it whose relative spelling need hold
// no literal: segs end, or end in a wholly unliteral glob, at or above the root (`//c/Users/me/**`,
// `~/config/**` for a project under them). When the rule may refuse project paths through what
// follows the root (`~/Documents/**/*.key` for a project under Documents), the literal that part
// spells is added to the screens. A rule whose literal merely occurs in the root's path, segment or
// substring, and that refuses nothing in the project (`~/Documents/*.pdf`, `~/.kube/config` beside
// `config-service`, `//etc/**` beside `fetcher`), adds nothing.
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
			lit, mid := literalOf(segs[si:])
			if lit == "" {
				all = true
				return
			}
			j.addScreen(lit, mid)
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

// segMatch reports whether a rule's segment pat, in screen form, may match the root's segment seg:
// as a glob, or as a drive's POSIX spelling (`c` for `c:`). A pattern path.Match cannot read matches.
func segMatch(pat, seg string) bool {
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
	j := pathJudge{root: r.ProjectRoot, judged: make(map[string]bool), rootSpelling: rootSpellingOf(r.ProjectRoot)}
	if r.ProjectRoot != "" {
		j.rootKey = screenText(filepath.ToSlash(filepath.Clean(r.ProjectRoot)), true)
		j.rootSegs = strings.FieldsFunc(j.rootKey, func(c rune) bool { return c == '/' })
	}
	if d.HostPaths != nil {
		j.host = true
		h := d.HostPaths()
		j.refuses = h.Refuses
		if h.Refuses != nil {
			j.screenBy(h.Patterns)
		}
	}
	for _, f := range r.Checkpoint.Pointers.Files {
		if j.withheld(f.Path) {
			j.note(f.Path)
		}
	}
	for _, e := range r.Checkpoint.Dropped {
		if checkpointPathDrops[e.Kind] && j.withheld(e.ID) {
			j.note(e.ID)
		}
	}
	for _, t := range r.Checkpoint.Pointers.Tools {
		for _, p := range j.classify(t.Summary) {
			if p.value && recordedPath(p) && j.valueWithheld(p.s, false) {
				j.note(p.s)
			}
		}
	}
	return j
}

// recordedPath reports whether p, a structured value, is a recorded path whose names the screen
// looks for: whole (the store's cut fell outside it), concrete (not a glob), and rooted or holding a
// separator. A one-word relative value with no separator is a Glob or Grep argument, often no path at
// all (a git revision such as HEAD~1, which the host may refuse as an 8.3 name it cannot resolve); it
// is screened as free text itself, and noting it would withhold every text that mentions the word.
func recordedPath(p part) bool {
	return !p.cut && !isGlob(p.s) && (absLike(p.s) || strings.ContainsAny(p.s, `/\`))
}

// note records p, a path the build withholds, as known: its relative key when it is inside the
// project and concrete, its basename and relative path for the free-text screen, and its whole
// spelling for the drop-reason screen.
func (j *pathJudge) note(p string) {
	p = judgedSpelling(p)
	if p == "" {
		return
	}
	k, inside := j.key(p)
	if inside && !isGlob(k) {
		j.known = appendDistinct(j.known, k)
	}
	slash := strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if base := path.Base(slash); namesAFile(base) {
		j.knownText = appendDistinct(j.knownText, screenText(base, true))
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

// appendDistinct appends s to set unless it is empty or already there.
func appendDistinct(set []string, s string) []string {
	if s == "" {
		return set
	}
	for _, e := range set {
		if e == s {
			return set
		}
	}
	return append(set, s)
}

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
// established; with no host rules in force it is false. Each answer is memoized for the build.
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
	w := j.refuses(p)
	if j.judged != nil {
		j.judged[p] = w
	}
	return w
}

// inside reports whether p, absolute or project-relative, names something within the project.
func (j pathJudge) inside(p string) bool {
	if !absLike(p) {
		c := filepath.Clean(filepath.FromSlash(p))
		return c != ".." && !strings.HasPrefix(c, ".."+string(filepath.Separator))
	}
	if j.root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(j.root), filepath.Clean(filepath.FromSlash(p)))
	return err == nil && !filepath.IsAbs(rel) && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// absLike reports whether p is rooted in any spelling a pointer may carry: the platform's absolute
// form, a POSIX root, a Windows drive (absolute, or drive-relative: `D:secret.txt` is secret.txt in
// drive D's current directory) or a UNC share — whichever platform recorded it — or a path spelled
// from the home directory or an environment variable (homeOrVarRoot).
func absLike(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
		driveSpelling.MatchString(p) || homeOrVarRoot.MatchString(p)
}

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

// varRef is one environment variable as a shell spells it: `$VAR`, `${VAR}`, PowerShell's `$env:VAR`
// and `${env:VAR}`, cmd.exe's `%VAR%` (whose name may hold parentheses, `%ProgramFiles(x86)%`) and
// its delayed `!VAR!`.
const varRef = `\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$env:[A-Za-z_][A-Za-z0-9_]*|` +
	`\$\{env:[^}]+\}|%[A-Za-z_][A-Za-z0-9_()]*%|![A-Za-z_][A-Za-z0-9_]*!`

// homeVarName names a variable that holds the home directory or a directory in it.
const homeVarName = `(?:HOME|USERPROFILE|HOMEPATH|APPDATA|LOCALAPPDATA|ONEDRIVE|XDG_[A-Z]+_HOME)`

// homeVarRef is a homeVarName in any of varRef's spellings, whatever its case.
const homeVarRef = `(?i:\$(?:env:)?` + homeVarName + `|\$\{(?:env:)?` + homeVarName + `\}|%` + homeVarName +
	`%|!` + homeVarName + `!)`

// homeOrVarPath finds a home- or variable-rooted path anywhere in a screened text: `~` alone (at a
// word's end) or followed by a separator, `~user` followed by a separator or ending the text, one
// variable or a chain of them (varRef; `%HOMEDRIVE%%HOMEPATH%`) followed by a separator, or a home
// directory's variable (homeVarRef) ending a word, which is that directory as `~` alone is
// (`cd $HOME && cat .ssh/id_rsa`). It must start the text or follow a separator, an assignment, a
// quote, or a flag's letters (`-i~/.ssh/key`). An approximate number (`~36800 tokens`) is not a home
// directory, and any other variable alone is a name assembled at run time, which the screen cannot
// see (ADR 0011 §23.2).
var homeOrVarPath = regexp.MustCompile(
	`(^|[\s"'` + "`" + `=,;|&<>(\[{:]|-[A-Za-z]*)` +
		`(~([\\/]|$|[\s|&;<>)])|~[A-Za-z_][A-Za-z0-9._-]*([\\/]|$)|` +
		`(?:` + varRef + `)+[\\/]|(?:` + varRef + `)*` + homeVarRef + `(?:$|[\s|&;<>)]))`)

// summaryWithheld reports whether a tool pointer's summary may not be shown (D61; the file's
// header): a cut summary ending in a prefix of a withheld name, a structured value the build
// withholds, or free text the screen withholds. The summary is judged as recorded and, when the
// rendering would cut it (pointerLine), as rendered.
func (j pathJudge) summaryWithheld(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	if j.truncatedWithheld(s) {
		return true
	}
	if r := truncRunes(oneLine(s), maxOneLineRunes, truncMark); r != oneLine(s) && j.truncatedWithheld(r) {
		return true
	}
	parts := j.classify(s)
	for _, p := range parts {
		if p.value && j.valueWithheld(p.s, p.cut) {
			return true
		}
	}
	return j.freeTextWithheld(parts)
}

// part is one piece of a tool pointer's summary as the gate reads it (classify).
type part struct {
	s string
	// value: judged whole as a path, as a file pointer's is (valueWithheld).
	value bool
	// text: screened as free text (freeTextWithheld).
	text bool
	// cut: the store's cut fell inside it, so s is only the start of what the call spelled.
	cut bool
}

// classify splits a summary into its parts. A canonical-JSON preview's strings are texts, keys
// included, and each value of a path-named argument (pathArgName) is a value; a JSON preview whose
// grammar a command breaks (`{"x":1} && cat …`, cut) is one text, with its strings beside it.
// Otherwise a summary of one word, once the project root's own spelling is held together, is the
// store's preview of one path argument and is a value; so is a summary that starts at the root and
// goes on below it with a space (a Read of `<root>/my docs/x.txt`, or Grep's path then its
// pattern); and a URL is a text only. A value is a text as well unless it is a rooted plain path:
// Read, Write and Edit take an absolute file_path, so a rooted plain word is the one file a file
// pointer would name (judged as written, so the project's README.md is shown although
// private/README.md is withheld), while a relative value is a Glob or Grep argument, which may spell
// a withheld file's name or a rule's literal, and a value holding a character no plain path does is
// a list, a locator or a command (`a.go b.go`, `x#L4`, `cat<x`, `path:x`). Every other summary is
// one text. The part the store's cut fell inside is marked cut, its `…` removed.
func (j pathJudge) classify(s string) []part {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	if jsonShaped(t) {
		strs, grammatical := jsonStrings(t)
		out := make([]part, 0, len(strs)+1)
		if !grammatical {
			body, cut := strings.CutSuffix(t, previewEllipsis)
			out = append(out, part{s: body, text: true, cut: cut})
		}
		for _, str := range strs {
			p := part{s: str.value, text: true, cut: str.cut}
			if str.pathNamed && grammatical {
				p.s = strings.TrimSpace(p.s)
				p.value = true
				p.text = p.cut || !j.plainRooted(p.s)
			}
			out = append(out, p)
		}
		return out
	}
	body, cut := strings.CutSuffix(t, previewEllipsis)
	held, _ := j.protect(sanitize(body))
	switch {
	case urlShaped.MatchString(body):
		return []part{{s: body, text: true, cut: cut}}
	case strings.ContainsAny(held, " \t"):
		below := len(held) > 1 && held[0] == rootMark && isSep(held[1])
		return []part{{s: body, value: below, text: true, cut: cut}}
	}
	return []part{{s: body, value: true, text: cut || !j.plainRooted(body), cut: cut}}
}

// plainRooted reports whether v is a rooted plain path: absolute (or home- or variable-rooted), and
// holding no character a plain path does not (notPlainPathRune) and no colon past a drive's, once
// the project root's own spelling (a space or an apostrophe in it) is held together.
func (j pathJudge) plainRooted(v string) bool {
	if !absLike(v) {
		return false
	}
	held, _ := j.protect(sanitize(v))
	rest := strings.ReplaceAll(held, string(rootMark), "")
	return strings.IndexFunc(rest, notPlainPathRune) < 0 && strings.LastIndexByte(rest, ':') <= 1
}

// urlShaped matches a URL with an authority (`https://…`, `file://…`): a scheme of two characters or
// more, so a Windows drive is never one.
var urlShaped = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+://`)

// notPlainPathRune reports a character a plain path argument does not hold: an ASCII one outside
// letters, digits and `. _ - / \ : ~ +`. A `#` or an `@` is a locator's more often than a name's
// (`x.go#L4`, `x.go@rev`), so a value holding one is screened as text too.
func notPlainPathRune(r rune) bool {
	if r >= utf8.RuneSelf || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return false
	}
	return !strings.ContainsRune(`._-/\:~+`, r)
}

// valueWithheld reports whether a structured value may not be shown: the build withholds it as it
// would a file pointer's path (withheld: containment and the host's rules), it holds an absolute,
// home or variable path outside the project in any spelling the screen reads (a file URL, a
// drive-relative path), or it is a glob that selects a path the build withholds (globSelectsKnown).
// A value the store's cut fell inside is judged by what it spells whole (cutValueWithheld).
func (j pathJudge) valueWithheld(v string, cut bool) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if cut {
		return j.cutValueWithheld(v)
	}
	if j.withheld(v) {
		return true
	}
	held, sibling := j.protect(sanitize(judgedSpelling(v)))
	if sibling {
		return true
	}
	if p := stripQuotes(held); homeOrVarPath.MatchString(p) || j.outsideIn(p) {
		return true
	}
	return isGlob(v) && j.globSelectsKnown(v)
}

// cutValueWithheld judges v, a structured value the store's cut fell inside: only its start is
// known, and its last segment is the start of a name, not a name. A start of the root's own spelling
// (rootPrefix) is the project. Otherwise the directory it spells whole, all but that last segment,
// is judged as a path (containment and the host's rules, one judgement), and the name being cut is
// left to the screen (truncatedWithheld). A value with no directory part is withheld only when it is
// rooted outside the project (`~`, `$HOM`). A cut value is never a withheld name (recordedPath).
func (j pathJudge) cutValueWithheld(v string) bool {
	p := judgedSpelling(v)
	if p == "" || j.rootPrefix(p) {
		return false
	}
	i := strings.LastIndexAny(p, `/\`)
	switch {
	case i < 0:
		return absLike(p) && !j.inside(p)
	case i == 0:
		return j.withheld(p[:1])
	}
	return j.withheld(p[:i])
}

// rootPrefix reports whether t, a stretch of a text, is the start of the project root's spelling
// (either slash, a separator repeated, quotes and escapes removed, case folded where paths fold):
// the store's cut fell inside the root, whose spelling protect could not find whole.
func (j pathJudge) rootPrefix(t string) bool {
	if j.rootKey == "" {
		return false
	}
	n := screenText(strings.ReplaceAll(t, `\`, "/"), true)
	for strings.Contains(n, "//") {
		n = strings.ReplaceAll(n, "//", "/")
	}
	return n != "" && strings.HasPrefix(j.rootKey, n)
}

// cutRoot is held, a protected text the store cut, with its last stretch replaced by rootMark when
// that stretch, from the start of a path to the cut, is the start of the root's own spelling
// (rootPrefix): the cut fell inside a spelling of the root, the project, not a path outside it.
func (j pathJudge) cutRoot(held string) string {
	for i := 0; i < len(held); i++ {
		if i > 0 && strings.IndexByte(pathStartAfter+quoteChars, held[i-1]) < 0 {
			continue
		}
		if j.rootPrefix(held[i:]) {
			return held[:i] + string(rootMark)
		}
	}
	return held
}

// freeTextWithheld reports whether the text parts of a summary may not be shown: the host's rules
// are unavailable, a rule's literal cannot tell texts apart, or one text, as written or
// percent-decoded, is withheld by textWithheld. It asks the host nothing. A summary with no text
// part is not free text.
func (j pathJudge) freeTextWithheld(parts []part) bool {
	texts := 0
	for _, p := range parts {
		if p.text {
			texts++
		}
	}
	if texts == 0 {
		return false
	}
	if j.rulesUnavailable() || j.screenAll {
		return true
	}
	for _, p := range parts {
		if !p.text {
			continue
		}
		s := sanitize(p.s)
		if j.textWithheld(s, p.cut) {
			return true
		}
		if d := percentDecode(s); d != s && j.textWithheld(sanitize(d), p.cut) {
			return true
		}
	}
	return false
}

// textWithheld screens one sanitized text; cut reports that the store's cut fell at its end. With
// the project root's spellings held together (protect, and cutRoot for a spelling the cut fell
// inside), it is withheld when one of its screen forms (screenForms) holds a rule's literal, or the
// basename or relative path of a path the build withholds where a name starts (namedAt); when, with
// quotes removed and separators kept, and also with a POSIX shell's `.\.` read as `..`, it holds an
// absolute path outside the project (outsideIn) or a home- or variable-rooted path; when it names a
// sibling of the root that runs on from the root's spelling inside one quoted argument; or when
// recall's path: selector in it selects a withheld path (selectorWithheld).
func (j pathJudge) textWithheld(t string, cut bool) bool {
	held, sibling := j.protect(t)
	if sibling {
		return true
	}
	if cut {
		held = j.cutRoot(held)
	}
	for _, form := range screenForms(held) {
		for _, s := range j.screens {
			if strings.Contains(form, s) {
				return true
			}
		}
		for _, k := range j.knownText {
			if namedAt(form, k) {
				return true
			}
		}
	}
	p := stripQuotes(held)
	if homeOrVarPath.MatchString(p) || j.outsideIn(p) {
		return true
	}
	if q := strings.ReplaceAll(p, `.\.`, ".."); q != p && j.outsideIn(q) {
		return true
	}
	for _, v := range selectorValues(t) {
		if j.selectorWithheld(v) {
			return true
		}
	}
	return false
}

// screenForms are the forms of held, a protected text, that the screen compares (screenText): with
// escape characters removed (`my\ secret.txt`), with them read as separators (`private\deny.txt`),
// and, when it holds one, with an escape character before a space removed together with the space:
// a line continuation inside a word (`de` and a backslash, a caret or a backtick before a newline)
// joins it, and the store's preview collapsed the newline to a space.
func screenForms(held string) []string {
	forms := []string{screenText(held, false), screenText(strings.ReplaceAll(stripQuotes(held), `\`, "/"), false)}
	if joined := escapedBreak.ReplaceAllString(held, ""); joined != held {
		forms = append(forms, screenText(joined, false))
	}
	return forms
}

// escapedBreak is an escape character followed by a space: a line continuation the store's preview
// collapsed to one.
var escapedBreak = regexp.MustCompile("[\\\\^`] ")

// namedAt reports whether name, a withheld path's basename or relative path in screen form, occurs
// in form where a name starts: at its start or after a byte that does not continue a name
// (nameByte). A name glued to a preceding name character is another file (`layout.txt` is not
// `out.txt`); what follows is not judged, so `my secret.txt.bak` holds `my secret.txt`.
func namedAt(form, name string) bool {
	for from := 0; from < len(form); {
		i := strings.Index(form[from:], name)
		if i < 0 {
			return false
		}
		at := from + i
		if at == 0 || !nameByte(form[at-1]) {
			return true
		}
		from = at + 1
	}
	return false
}

// minCutPrefix is the shortest tail of a cut summary that counts as the start of a withheld name: D61
// sets three characters, below which a prefix (`de`, `.e`) is in every other word and names nothing.
const minCutPrefix = 3

// truncatedWithheld reports whether s, a summary the store cut (it ends in `…`), ends in the first
// minCutPrefix characters or more of a rule's literal, of a path the build withholds or of a rule's
// specifier, starting at a word or path-segment boundary, or anywhere for a literal that may begin
// inside a name (midScreens): the cut fell inside that name, and its prefix is all a reader would
// need. The tail is read in each of screenForms, so `private\den…` and `my\ sec…` are both read; a
// tail that is the start of the root's own spelling is the root (cutRoot).
func (j pathJudge) truncatedWithheld(s string) bool {
	body, cut := strings.CutSuffix(strings.TrimSpace(s), previewEllipsis)
	if !cut {
		return false
	}
	held, _ := j.protect(sanitize(body))
	for _, form := range screenForms(j.cutRoot(held)) {
		for _, set := range [][]string{j.screens, j.knownText, j.rulePaths} {
			for _, name := range set {
				if endsWithPrefixOf(form, name, true) {
					return true
				}
			}
		}
		for _, name := range j.midScreens {
			if endsWithPrefixOf(form, name, false) {
				return true
			}
		}
	}
	return false
}

// previewEllipsis is what the store appends to a preview it cut (store.previewEllipsis).
const previewEllipsis = "…"

// endsWithPrefixOf reports whether text ends in a prefix of name at least minCutPrefix bytes long
// that, when bounded, starts text or follows a cutBoundary.
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
		if text[start:] == name[:k] &&
			(!bounded || start == 0 || strings.IndexByte(cutBoundary, text[start-1]) >= 0) {
			return true
		}
	}
	return false
}

// cutBoundary is what may stand before the start of a name in a screened text.
const cutBoundary = " /=:,;|&<>()[]{}@" + string(rootMark)

// selectorValues returns the value of each recall path: selector in t, unquoted.
func selectorValues(t string) []string {
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
// whitespace, `(`, a quote or `=`, as a command line spells it (`qompack recall "path:x"`,
// `--query=path:x`).
var pathSelector = regexp.MustCompile(`(?i)(?:^|[\s("'=])` + recallPathSelector + `:("[^"]*"?|'[^']*'?|[^\s"']+)`)

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
// key that spells it literally (w19 round-2 review: `path:deny`, `path:rivate/deny.txt` and
// `path:keep/*.txt` select private/deny.txt and private/keep/a.txt).
func (j pathJudge) selectsKnown(v string) bool {
	k := selectorKey(v)
	if k == "" {
		return false
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
// nothing Qompack recorded is judged as written (D60(iv)).
func (j pathJudge) globSelectsKnown(g string) bool {
	k, ok := j.key(g)
	if !ok {
		return false
	}
	for _, w := range j.known {
		if rules.Match(k, w) {
			return true
		}
	}
	return false
}

// key is p's project-relative paths.Key form with forward slashes, and false when p is empty or
// outside the project.
func (j pathJudge) key(p string) (string, bool) {
	p = judgedSpelling(p)
	if p == "" || !j.inside(p) {
		return "", false
	}
	if absLike(p) {
		rel, err := filepath.Rel(filepath.Clean(j.root), filepath.Clean(filepath.FromSlash(p)))
		if err != nil {
			return "", false
		}
		p = rel
	}
	return paths.Key(strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, `\`, "/")), "./")), true
}

// screenLiteral is the part of a Read rule's specifier that every path the rule refuses spells
// (D61(2)(a)): its last segment when that has no glob syntax (`deny.txt`, `.env`, `John's
// notes.txt`); else the nearest all-literal segment before it (`secrets` for `./secrets/**`,
// `private` for `./private/*.txt`); else the longest literal run of the last segment (`.env` for
// `**/*.env`, `.pem` for `*.pem`). It is in screen form, and "" when the specifier has no literal
// part at all (`Read`, `./**`, `~/**`), which refuses everything below its anchor. anchored reports a
// specifier measured from outside the project (`//`, `/`, `~`, a drive, `..`), whose literal may lie
// in the project's own path.
func screenLiteral(spec string) (lit string, anchored bool) {
	segs, anchored, _ := ruleSegments(spec)
	lit, _ = literalOf(segs)
	return lit, anchored
}

// ruleSegments is spec, a rule's specifier, as the segments of the paths it refuses below its
// anchor, each in screen form, with `..` resolved lexically as hostperm resolves it (cleanLiteral:
// `./private/deny.txt/..` refuses private/**). anchored reports a specifier measured from outside
// the project (`//`, `/`, `~` or `~/`, a drive, a leading `..`), and fromStart one measured from the
// filesystem root (`//`, a drive). The home anchor is where the rule is measured from, not a segment;
// `~name` with no slash after `~` is hostperm's project-relative name, not a home.
func ruleSegments(spec string) (segs []string, anchored, fromStart bool) {
	s := strings.ReplaceAll(strings.TrimSpace(spec), `\`, "/")
	home := s == "~" || strings.HasPrefix(s, "~/")
	fromStart = strings.HasPrefix(s, "//") || driveSpelling.MatchString(s)
	anchored = fromStart || home || strings.HasPrefix(s, "/") || strings.HasPrefix(path.Clean(s), "..")
	for i, seg := range strings.Split(s, "/") {
		switch {
		case seg == "" || seg == "." || (i == 0 && home):
		case seg == "..":
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
		default:
			segs = append(segs, screenText(seg, true))
		}
	}
	return segs, anchored, fromStart
}

// literalOf is screenLiteral's choice over segs: the last segment when it has no glob syntax, else
// the nearest all-literal segment before it, else the longest literal run of the last segment. mid
// reports a run with `*`, `?` or a class before it, which every name it is in may begin before.
func literalOf(segs []string) (lit string, mid bool) {
	if len(segs) == 0 {
		return "", false
	}
	last := segs[len(segs)-1]
	if !isGlob(last) {
		return last, false
	}
	for i := len(segs) - 2; i >= 0; i-- {
		if !isGlob(segs[i]) {
			return segs[i], false
		}
	}
	run, at := longestLiteralRun(last)
	return strings.TrimSpace(run), run != "" && at > 0
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

// screenText is t in the form the screen compares (D61(2)): with ' " ` \ ^ removed, whitespace runs
// collapsed to one space as the store's preview collapses them (store.previewString), and case
// folded where the platform's paths fold (paths.Key). trim drops the outer spaces.
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

// stripQuotes is t with ' " ` ^ removed and its backslashes kept as separators, the form outsideIn
// reads; a backslash before a quote escapes the quote (`\"`), and goes with it.
func stripQuotes(t string) string {
	var b strings.Builder
	b.Grow(len(t))
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '\\' && i+1 < len(t) && strings.IndexByte(quoteChars, t[i+1]) >= 0:
			i++
		case strings.IndexByte(quoteChars, c) >= 0 || c == '^':
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// quoteChars are the quotes a shell or a JSON string may wrap a path in.
const quoteChars = "'\"`"

// sanitize is t as the store's preview spells text (store.previewString): control characters
// dropped, whitespace runs collapsed to one space, outer spaces trimmed. It also guarantees that no
// rootMark reaches protect except the ones protect writes.
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

// percentDecode is t with each %XX escape decoded; a `%` not followed by two hex digits stays.
func percentDecode(t string) string {
	if !strings.Contains(t, "%") {
		return t
	}
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '%' && i+2 < len(t) && isHex(t[i+1]) && isHex(t[i+2]) {
			b.WriteByte(unhex(t[i+1])<<4 | unhex(t[i+2]))
			i += 2
			continue
		}
		b.WriteByte(t[i])
	}
	return b.String()
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// The project root is often spelled with a space, a comma or an apostrophe in it
// (C:\Users\John Smith\proj), with either slash and any case on Windows, quoted or escaped the way
// a shell takes it. protect holds each spelling of the root in a text together as one rootMark, a
// control character no sanitized text carries, so the screen never cuts the root apart: its
// literals are looked for in the rest of the text, and an absolute path that starts at the root is
// judged from the root on (outsideIn). A spelling glued to a name character on either side
// (proj2, xC:\q\proj) is not the root and is judged as the path outside the project it is.
const rootMark = '\x01'

// rootSpellingOf matches root as a text spells it: its separators either slash, repeated or not (a
// JSON escape doubles them), a quote opened or closed between any two characters, an escape before a
// space or a shell-special character, and on Windows the MSYS and WSL spellings of its drive (`/c/`,
// `/mnt/c/`) and the `\\?\` prefix. Case folds where the platform's paths fold. Nil for no root.
func rootSpellingOf(root string) *regexp.Regexp {
	if root == "" {
		return nil
	}
	clean := strings.Join(strings.Fields(filepath.ToSlash(filepath.Clean(root))), " ")
	var b strings.Builder
	if paths.DefaultFold() {
		b.WriteString("(?i)")
	}
	rest := clean
	if runtime.GOOS == "windows" && len(clean) >= 2 && clean[1] == ':' {
		b.WriteString(`(?:[\\/]{2}[?.][\\/])?(?:` + regexp.QuoteMeta(clean[:2]) + `|(?:/mnt)?/` +
			regexp.QuoteMeta(strings.ToLower(clean[:1])) + `)`)
		rest = clean[2:]
	}
	for _, r := range rest {
		b.WriteString(`["'` + "`" + `]*`)
		switch {
		case r == '/':
			b.WriteString(`[\\/]+`)
		case r == ' ':
			b.WriteString(`[\\` + "`" + `^]? `)
		case r == '\'' || r == '"' || r == '`':
			b.WriteString(`(?:\\?["'` + "`" + `])*`)
		case strings.ContainsRune(rootEscapable, r):
			b.WriteString(`[\\` + "`" + `^]?` + regexp.QuoteMeta(string(r)))
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return regexp.MustCompile(b.String())
}

// rootEscapable are the characters a shell may escape inside the root's spelling.
const rootEscapable = "()&;,$!#[]{}=<>|*?"

// protect is t, a sanitized text, with each spelling of the project root replaced by rootMark. A
// spelling glued to a short option's letters (`-I<root>/include`) is the root as the option's value.
// sibling reports a spelling that runs on into more words inside one quoted argument, or through an
// escaped space (`"C:\q\proj old\x.txt"`, `/q/proj\ old/x.txt`): that argument names a sibling of
// the root, a path outside the project, and the text is withheld.
func (j pathJudge) protect(t string) (held string, sibling bool) {
	if j.rootSpelling == nil {
		return t, false
	}
	var b strings.Builder
	last := 0
	for _, m := range j.rootSpelling.FindAllStringIndex(t, -1) {
		a, e := m[0], m[1]
		if a > 0 && nameByte(t[a-1]) && !flagBefore(t, a) {
			continue
		}
		switch rootFollowedBy(t, e) {
		case followedByName:
			continue
		case followedBySibling:
			sibling = true
			continue
		case followedBySpace:
			if quoteOpen(t[:e]) {
				sibling = true
				continue
			}
		}
		b.WriteString(t[last:a])
		b.WriteByte(rootMark)
		last = e
	}
	b.WriteString(t[last:])
	return b.String(), sibling
}

// nameByte reports a byte that continues a file name: a letter, a digit, a non-ASCII byte, or one
// of `. _ - ~ $ % + #`.
func nameByte(c byte) bool {
	return c >= utf8.RuneSelf || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("._-~$%+#", c) >= 0
}

// rootFollowing is what follows a spelling of the root in a text.
type rootFollowing uint8

const (
	// followedByPath: the end, a separator, a quote or a shell operator; the spelling is the root.
	followedByPath rootFollowing = iota
	// followedBySpace: whitespace, which ends the root's word unless a quote is open.
	followedBySpace
	// followedBySibling: an escaped space, which continues the word into a sibling's name.
	followedBySibling
	// followedByName: anything else, which continues the name (proj2, proj.bak, proj,x).
	followedByName
)

// rootFollowedBy classifies what follows a root spelling that ends at e in t.
func rootFollowedBy(t string, e int) rootFollowing {
	if e == len(t) {
		return followedByPath
	}
	spaceAfter := e+1 == len(t) || t[e+1] == ' '
	switch c := t[e]; {
	case c == ' ':
		return followedBySpace
	case c == '\\' || c == '`' || c == '^':
		if e+1 < len(t) && t[e+1] == ' ' {
			return followedBySibling
		}
		return followedByPath
	case c == '/' || strings.IndexByte(`'"&|;<>)`, c) >= 0:
		return followedByPath
	case c == ':' && (e+1 == len(t) || isSep(t[e+1]) || strings.IndexByte(quoteChars, t[e+1]) >= 0):
		// A colon and a path after the root: a bind mount (`-v "<root>:/src"`) ends the root's word
		// there. A Windows name holds no colon, and a POSIX sibling named `proj:` is negligible.
		return followedByPath
	case strings.IndexByte(".,:", c) >= 0 && spaceAfter:
		// Sentence punctuation after the root (`… in C:\q\proj.`).
		return followedByPath
	}
	return followedByName
}

// quoteOpen reports whether a quote is open at the end of prefix in any of the readings a shell
// may give it: a POSIX shell's (backslash escapes outside single quotes and before " \ $ ` inside
// double quotes), PowerShell's (a backtick escapes, a doubled quote is one quote) and cmd.exe's
// (only double quotes, toggled). Any reading suffices: a sibling misread as the root followed by
// words would be shown.
func quoteOpen(prefix string) bool {
	return posixQuoteOpen(prefix) || powershellQuoteOpen(prefix) || strings.Count(prefix, `"`)%2 == 1
}

func posixQuoteOpen(t string) bool {
	var q byte
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case q == 0 && c == '\\':
			i++
		case q == 0 && (c == '\'' || c == '"'):
			q = c
		case q == '"' && c == '\\' && i+1 < len(t) && strings.IndexByte("\"\\$`", t[i+1]) >= 0:
			i++
		case q != 0 && c == q:
			q = 0
		}
	}
	return q != 0
}

func powershellQuoteOpen(t string) bool {
	var q byte
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case q != '\'' && c == '`':
			i++
		case q == 0 && (c == '\'' || c == '"'):
			q = c
		case q != 0 && c == q && i+1 < len(t) && t[i+1] == q:
			i++
		case q != 0 && c == q:
			q = 0
		}
	}
	return q != 0
}

// outsideIn reports whether p, a protected text with quotes removed and separators kept, holds an
// absolute path outside the project (D61(2)(c)): one starting at a drive (`C:\…`, or drive-relative
// `D:secret.txt`), a UNC share (a host and a share), a `\\?\` or `\\.\` device path, a file:// URL,
// the root's spelling followed by `..` that leaves it, a POSIX absolute path of two segments or more
// (a single segment such as the flag `/c` is not one, nor is a standard device such as /dev/null),
// or a relative path whose `..` leaves the project. A path starts p or follows whitespace or a
// delimiter, an assignment, a colon or an `@`, or is glued to a short option (`-I/usr/include`,
// flagGlued). An http(s) or other URL is not a path, though a stretch inside it that follows `=`,
// `:` or `@` is read as one.
func (j pathJudge) outsideIn(p string) bool {
	for i := 0; i < len(p); i++ {
		if i > 0 && strings.IndexByte(pathStartAfter, p[i-1]) < 0 && !flagGlued(p, i) {
			continue
		}
		end := i
		for end < len(p) && strings.IndexByte(pathEndsAt, p[end]) < 0 {
			end++
		}
		if end > i && j.tokenOutside(p, i, end) {
			return true
		}
	}
	return false
}

// pathStartAfter is what a path in a text may follow; pathEndsAt is what ends one.
const (
	pathStartAfter = " |&;<>(){}[],=:@"
	pathEndsAt     = " |&;<>(){}[],"
)

// flagGlued reports whether p[i], a separator, the root's mark or a drive, starts a path glued to a
// short option: `-` and one letter or more stand before it at the start of a word (flagBefore).
func flagGlued(p string, i int) bool {
	c := p[i]
	if !isSep(c) && c != rootMark && (i+1 >= len(p) || p[i+1] != ':' || !asciiLetter(c)) {
		return false
	}
	return flagBefore(p, i)
}

// flagBefore reports whether a short option, `-` and one ASCII letter or more at the start of a word,
// ends just before i in t.
func flagBefore(t string, i int) bool {
	k := i
	for k > 0 && asciiLetter(t[k-1]) {
		k--
	}
	return k < i && k > 0 && t[k-1] == '-' && (k == 1 || strings.IndexByte(pathStartAfter, t[k-2]) >= 0)
}

func asciiLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// tokenOutside judges the stretch p[i:end] that starts a path (outsideIn).
func (j pathJudge) tokenOutside(p string, i, end int) bool {
	tok := p[i:end]
	c := tok[0]
	switch {
	case c == rootMark:
		return !rootRelativeInside(tok[1:])
	case len(tok) > 2 && tok[1] == ':' && asciiLetter(c):
		// A drive, absolute or drive-relative; a spelling of the root is already a rootMark.
		return true
	case len(tok) > 1 && isSep(c) && isSep(tok[1]):
		if scheme := schemeBefore(p, i); scheme != "" {
			return scheme == "file" && fileURLOutside(tok[2:])
		}
		return uncOrDevice(tok[2:])
	case isSep(c):
		return !devicePaths[tok] && !devFD.MatchString(tok) && hasSecondSegment(tok[1:])
	}
	return strings.Contains(tok, "..") && !absLike(tok) && relativeEscapes(tok)
}

func isSep(c byte) bool { return c == '/' || c == '\\' }

// uncOrDevice reports whether rest, what follows a token's two leading separators, is a UNC share
// (`\\host\share`, `//host/share`: a host and a share, as a POSIX path needs two segments) or a
// Windows device path (`\\?\`, `\\.\`). Two separators and a word alone are no path: a comment
// marker (`// TODO`, `//nolint`, `//go:build`).
func uncOrDevice(rest string) bool {
	if len(rest) >= 2 && (rest[0] == '?' || rest[0] == '.') && isSep(rest[1]) {
		return true
	}
	return hasSecondSegment(rest)
}

// devicePaths are the standard POSIX devices a command redirects to or reads from (`2>/dev/null`);
// they hold no file content, and devFD is a process's own descriptor (`/dev/fd/3`). Any other path
// under /dev is judged as any absolute path is.
var (
	devicePaths = map[string]bool{
		"/dev/null": true, "/dev/stdin": true, "/dev/stdout": true, "/dev/stderr": true,
		"/dev/tty": true, "/dev/zero": true, "/dev/random": true, "/dev/urandom": true,
	}
	devFD = regexp.MustCompile(`^/dev/fd/[0-9]+$`)
)

// schemeBefore is the lower-cased URL scheme that ends just before the `//` at i (`https`, `file`),
// or "" when none does: a scheme is two characters or more, so a drive is never one.
func schemeBefore(p string, i int) string {
	if i == 0 || p[i-1] != ':' {
		return ""
	}
	k := i - 1
	for k > 0 && (nameByte(p[k-1]) && p[k-1] < utf8.RuneSelf) {
		k--
	}
	if s := p[k : i-1]; len(s) >= 2 && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z') {
		return strings.ToLower(s)
	}
	return ""
}

// fileURLOutside reports whether a file URL's text after `file://` names a path outside the project:
// anything but the root's spelling (on localhost or no host) followed by a path that stays in it.
func fileURLOutside(rest string) bool {
	if len(rest) >= len("localhost") && strings.EqualFold(rest[:len("localhost")], "localhost") {
		rest = rest[len("localhost"):]
	}
	rest = strings.TrimLeft(rest, `/\`)
	if rest == "" || rest[0] != rootMark {
		return true
	}
	return !rootRelativeInside(rest[1:])
}

// rootRelativeInside reports whether rest, what follows a rootMark up to the end of its token, stays
// inside the project: nothing, sentence punctuation, a colon that ends the root's word (a bind
// mount's `<root>:/src`; what follows the colon is read as a path of its own), or a separator and
// segments whose `..` never climb above the root. A name glued to the root (`"C:\q\proj"x`, which a
// shell joins into projx) is a sibling.
func rootRelativeInside(rest string) bool {
	if strings.Trim(rest, ".,:") == "" || rest[0] == ':' {
		return true
	}
	if !isSep(rest[0]) {
		return false
	}
	depth := 0
	for _, seg := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch seg {
		case ".":
		case "..":
			if depth--; depth < 0 {
				return false
			}
		default:
			depth++
		}
	}
	return true
}

// hasSecondSegment reports whether rest, a POSIX absolute path less its leading separator, names a
// first segment and then a second.
func hasSecondSegment(rest string) bool {
	k := strings.IndexAny(rest, `/\`)
	return k > 0 && strings.Trim(rest[k:], `/\`) != ""
}

// relativeEscapes reports whether tok, a relative path, climbs above the project's root through `..`
// (either slash).
func relativeEscapes(tok string) bool {
	c := path.Clean(strings.ReplaceAll(tok, `\`, "/"))
	return c == ".." || strings.HasPrefix(c, "../")
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
	var out string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &out); err == nil {
		return out
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\/`, `/`).Replace(s)
}

// withheldPathLabel, withheldSummary and withheldPathNote replace what a withheld pointer would have
// shown. None names the rule or the path: a rule spells the very path it protects.
const (
	withheldPathNote  = "path withheld: the host's permission rules refuse it, or it is outside the project"
	withheldPathLabel = "file (" + withheldPathNote + ")"
	withheldSummary   = "summary withheld: it names a path the host's permission rules refuse, or one outside the project"
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
		if checkpointPathDrops[e.Kind] && j.withheld(e.ID) {
			out[i] = withheldDrop(e, r.Checkpoint.Pointers.Files)
		}
	}
	return out
}

// gateDropReasons returns drops with every reason that shows an absolute path outside the project
// or a path the build withholds redacted (D61(3)), except the model's own text (modelTextDrops) and
// section 6's own pointer drops, whose reason names a pointer's path only once withheld() has let
// section 6 show it (buildPointers). The path goes and the error kind stays: pointer_git_unavailable
// carries the checkpointer's git error verbatim, which in a linked worktree names its gitdir outside
// the project, and a rule or skill scan error can name a directory above it. drops is never
// modified.
func gateDropReasons(drops []checkpoint.DropEntry, j pathJudge) []checkpoint.DropEntry {
	var out []checkpoint.DropEntry
	for i, e := range drops {
		if modelTextDrops[e.Kind] || e.Kind == dropKindPointer || !j.reasonWithheld(e.Detail) {
			continue
		}
		if out == nil {
			out = append([]checkpoint.DropEntry(nil), drops...)
		}
		out[i].Detail = j.redactReason(e.Detail)
	}
	if out == nil {
		return drops
	}
	return out
}

// reasonWithheld reports whether a drop entry's reason shows an absolute path outside the project
// (or a sibling of the root) or names a path the build withholds: its whole spelling, relative for
// one in the project, as a path of its own (namesKnownIn). A reason is the package's or the
// checkpointer's own prose with paths set in it whole (a restore call, a git error's path), not a
// command that may glue a name to anything; so neither a withheld file's basename alone nor a rule's
// literal withholds it: an allowed README.md or src/CLAUDE.md beside a withheld private/README.md or
// ~/.claude/CLAUDE.md is a different file, which section 6 may show too.
func (j pathJudge) reasonWithheld(detail string) bool {
	t := sanitize(detail)
	if t == "" {
		return false
	}
	held, sibling := j.protect(t)
	if sibling {
		return true
	}
	p := stripQuotes(held)
	return j.namesKnownIn(screenText(strings.ReplaceAll(p, `\`, "/"), false)) ||
		homeOrVarPath.MatchString(p) || j.outsideIn(p)
}

// namesKnownIn reports whether t, in screen form with separators as `/`, names a path the build
// withholds (knownPaths) as a path of its own: it starts where a path starts (pathRunStart) and ends
// at a word or path-segment boundary (a closing `.` before a boundary counts as one).
func (j pathJudge) namesKnownIn(t string) bool {
	for _, k := range j.knownPaths {
		for from := 0; from+len(k) <= len(t); {
			i := strings.Index(t[from:], k)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(k)
			after := end == len(t) || strings.IndexByte(reasonBoundary, t[end]) >= 0 ||
				(t[end] == '.' && (end+1 == len(t) || strings.IndexByte(reasonBoundary, t[end+1]) >= 0))
			if after && pathRunStart(t, start) {
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
