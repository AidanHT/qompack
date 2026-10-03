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
//     own spelling is held together) or a path-named argument of a canonical-JSON preview, is judged
//     whole, as a file pointer's path is: the host's rules and containment, once per build.
//   - Everything else is free text (commands, queries, prompts, other JSON arguments) and costs no
//     host judgement. It is screened (freeTextWithheld) for the literal part of every Read deny or
//     ask rule (screenLiteral), for the basename or relative path of every path the build withholds,
//     for an absolute path outside the project, and for recall's path: selector selecting a withheld
//     path; when the rules are unavailable it is withheld whole.
//   - A summary the store cut is also withheld when it ends in a prefix of one of those names
//     (truncatedWithheld).
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
	// rootKey is the project root cleaned, slash-separated and in screen form (screenText): a rule
	// anchored outside the project whose literal is part of it may refuse the whole project.
	rootKey string
	// screens are the rules' literals in screen form; screenAll is set when one rule's literal cannot
	// tell texts apart (screenLiteral), and then every free-text summary is withheld.
	screens   []string
	screenAll bool
	// rulePaths are the rules' specifiers in screen form, which a cut summary's tail may begin.
	rulePaths []string
	// known holds every concrete in-project path the build withholds, in project-relative paths.Key
	// form: a structured glob, or recall's path: selector, that selects one is withheld.
	known []string
	// knownText holds the basename and the relative path of every path the build withholds (a file
	// pointer's, a path-keyed checkpoint drop's, a structured summary's) in screen form: free text
	// that holds one names the withheld file. Nothing read from free text is ever added.
	knownText []string
	// judged memoizes the host's judgement (hostRefuses) for the build: a path is named by a file
	// pointer and again by a summary, and each host judgement may consult the filesystem.
	judged map[string]bool
}

// newPathRules is a judge with the host's rules and no known paths: enough for withheld().
func newPathRules(r Request, d Deps) pathJudge {
	j := pathJudge{root: r.ProjectRoot, judged: make(map[string]bool), rootSpelling: rootSpellingOf(r.ProjectRoot)}
	if r.ProjectRoot != "" {
		j.rootKey = screenText(filepath.ToSlash(filepath.Clean(r.ProjectRoot)), true)
	}
	if d.HostPaths != nil {
		j.host = true
		h := d.HostPaths()
		j.refuses = h.Refuses
		if h.Refuses != nil {
			j.screenBy(h.Patterns)
		}
	}
	return j
}

// screenBy adds each rule pattern's literal and specifier to the build's screens.
func (j *pathJudge) screenBy(patterns []string) {
	for _, p := range patterns {
		lit, anchored := screenLiteral(p)
		if lit == "" || (anchored && strings.Contains(j.rootKey, lit)) {
			// A rule whose literal is empty refuses everything it is anchored at, and one anchored
			// outside the project whose literal is part of the project's own path may refuse every
			// project path: no literal of it tells a free text naming such a path from one that does
			// not.
			j.screenAll = true
			continue
		}
		j.screens = appendDistinct(j.screens, lit)
		if rp := screenText(strings.TrimPrefix(strings.TrimSpace(p), "./"), true); rp != "" {
			j.rulePaths = appendDistinct(j.rulePaths, rp)
		}
	}
}

// newPathJudge is the build's judge. It must read r's checkpoint drop entries before
// gateCheckpointDrops withholds their paths: a withheld path the checkpoint records only as a drop
// (a pointer_missing file, a file_pointer cut at the checkpoint's budget) is still a path free text,
// a glob or a selector may name (w19 verifier V3).
func newPathJudge(r Request, d Deps) pathJudge {
	j := newPathRules(r, d)
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
		values, _ := j.classify(t.Summary)
		for _, v := range values {
			if !isGlob(v) && j.valueWithheld(v) {
				j.note(v)
			}
		}
	}
	return j
}

// note records p, a path the build withholds, as known: its relative key when it is inside the
// project and concrete, and its basename and relative path for the free-text screen.
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
	case !inside && !absLike(p) && namesAFile(path.Base(path.Clean(slash))):
		j.knownText = appendDistinct(j.knownText, screenText(path.Clean(slash), true))
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
// environment variable (`$VAR`, `${VAR}`, `%VAR%`). Whatever it expands to, it is not a path the
// project root anchors, and read as project-relative it would be joined under the root, where a host
// deny rule on ~/.ssh/** never matches it. So it counts as rooted, and inside() finds it outside the
// project. `~` must end the path or be followed by a user name and a separator, so a project file
// named like an editor's lock file (`~$report.docx`) stays project-relative.
var homeOrVarRoot = regexp.MustCompile(
	`^(~[A-Za-z0-9._-]*([\\/]|$)|\$\{?[A-Za-z_][A-Za-z0-9_]*|%[A-Za-z_][A-Za-z0-9_()]*%)`)

// homeOrVarPath finds a home- or variable-rooted path anywhere in a screened text: `~` alone (at a
// word's end) or followed by a separator, `~user` followed by a separator or ending the text, or a
// variable (`$VAR`, `${VAR}`, `%VAR%`, a `%NAME%` whose name holds parentheses such as
// `%ProgramFiles(x86)%`) followed by a separator. It must start the text or follow a separator, an
// assignment, a quote, or a flag's letters (`-i~/.ssh/key`). An approximate number (`~36800
// tokens`) is not a home directory, and a variable alone is a name assembled at run time, which the
// screen cannot see (ADR 0011 §23.2).
var homeOrVarPath = regexp.MustCompile(
	`(^|[\s"'` + "`" + `=,;|&<>(\[{:]|-[A-Za-z]*)` +
		`(~([\\/]|$|[\s|&;<>)])|~[A-Za-z_][A-Za-z0-9._-]*([\\/]|$)|` +
		`(\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|%[A-Za-z_][A-Za-z0-9_()]*%)[\\/])`)

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
	values, texts := j.classify(s)
	for _, v := range values {
		if j.valueWithheld(v) {
			return true
		}
	}
	return len(texts) > 0 && j.freeTextWithheld(texts)
}

// classify splits a summary into the structured values judged as paths and the free texts screened.
// A canonical-JSON preview's path-named arguments (pathArgName) are values and its other strings,
// keys included, are texts. Otherwise a summary of one word, once the project root's own spelling is
// held together, is the store's preview of one path argument and is a value, and a URL is a text
// only. A one-word value is a text as well unless it is a rooted plain path: Read, Write and Edit
// take an absolute file_path, so a rooted word is the one file a file pointer would name (judged as
// written, so the project's README.md is shown although private/README.md is withheld), while a
// relative word is a Glob or Grep argument, which may spell a withheld file's name or a rule's
// literal, and a word holding a character no plain path does is a command glued without spaces
// (`cat<x`, `a=b`, `path:x`). Every other summary is one text.
func (j pathJudge) classify(s string) (values, texts []string) {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return nil, nil
	case jsonShaped(t):
		for _, str := range jsonStrings(t) {
			if str.pathNamed {
				values = append(values, strings.TrimSpace(str.value))
			} else {
				texts = append(texts, str.value)
			}
		}
		return values, texts
	}
	held, _ := j.protect(sanitize(t))
	if strings.ContainsAny(held, " \t") || urlShaped.MatchString(t) {
		return nil, []string{t}
	}
	// The root's own spelling (a space or an apostrophe in it) is not the word's to answer for.
	if rest := strings.ReplaceAll(held, string(rootMark), ""); absLike(t) &&
		strings.IndexFunc(rest, notPlainPathRune) < 0 && strings.LastIndexByte(rest, ':') <= 1 {
		return []string{t}, nil
	}
	return []string{t}, []string{t}
}

// urlShaped matches a URL with an authority (`https://…`, `file://…`): a scheme of two characters or
// more, so a Windows drive is never one.
var urlShaped = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+://`)

// notPlainPathRune reports a character a plain path argument does not hold: an ASCII one outside
// letters, digits and `. _ - / \ : ~ + @ #`.
func notPlainPathRune(r rune) bool {
	if r >= utf8.RuneSelf || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return false
	}
	return !strings.ContainsRune(`._-/\:~+@#`, r)
}

// valueWithheld reports whether a structured value may not be shown: the build withholds it as it
// would a file pointer's path (withheld: containment and the host's rules), it holds an absolute,
// home or variable path outside the project in any spelling the screen reads (a file URL, a
// drive-relative path), or it is a glob that selects a path the build withholds (globSelectsKnown).
func (j pathJudge) valueWithheld(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
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

// freeTextWithheld reports whether free texts may not be shown: the host's rules are unavailable,
// a rule's literal cannot tell texts apart, or one text, as written or percent-decoded, is withheld
// by textWithheld. It asks the host nothing.
func (j pathJudge) freeTextWithheld(texts []string) bool {
	if j.rulesUnavailable() || j.screenAll {
		return true
	}
	for _, t := range texts {
		s := sanitize(t)
		if j.textWithheld(s) {
			return true
		}
		if d := percentDecode(s); d != s && j.textWithheld(sanitize(d)) {
			return true
		}
	}
	return false
}

// textWithheld screens one sanitized text. With the project root's spellings held together
// (protect), and with ' " ` \ ^ removed, whitespace collapsed and case folded where the platform's
// paths fold (screenText), it is withheld when it holds a rule's literal or the basename or relative
// path of a path the build withholds; when, with quotes removed and separators kept, it holds an
// absolute path outside the project (outsideIn) or a home- or variable-rooted path; when it names a
// sibling of the root that runs on from the root's spelling inside one quoted argument; or when
// recall's path: selector in it selects a withheld path (selectorWithheld).
func (j pathJudge) textWithheld(t string) bool {
	held, sibling := j.protect(t)
	if sibling {
		return true
	}
	lit := screenText(held, false)
	for _, s := range j.screens {
		if strings.Contains(lit, s) {
			return true
		}
	}
	for _, k := range j.knownText {
		if strings.Contains(lit, k) {
			return true
		}
	}
	if p := stripQuotes(held); homeOrVarPath.MatchString(p) || j.outsideIn(p) {
		return true
	}
	for _, v := range selectorValues(t) {
		if j.selectorWithheld(v) {
			return true
		}
	}
	return false
}

// minCutPrefix is the shortest tail of a cut summary that counts as the start of a withheld name: D61
// sets three characters, below which a prefix (`de`, `.e`) is in every other word and names nothing.
const minCutPrefix = 3

// truncatedWithheld reports whether s, a summary the store cut (it ends in `…`), ends in the first
// minCutPrefix characters or more of a rule's literal, of a path the build withholds or of a rule's
// specifier, starting at a word or path-segment boundary: the cut fell inside that name, and its
// prefix is all a reader would need. The tail is read with escape characters removed and with them
// read as separators, so `private\den…` and `my\ sec…` are both read.
func (j pathJudge) truncatedWithheld(s string) bool {
	body, cut := strings.CutSuffix(strings.TrimSpace(s), previewEllipsis)
	if !cut {
		return false
	}
	held, _ := j.protect(sanitize(body))
	forms := []string{screenText(held, false), screenText(strings.ReplaceAll(stripQuotes(held), `\`, "/"), false)}
	for _, form := range forms {
		for _, set := range [][]string{j.screens, j.knownText, j.rulePaths} {
			for _, name := range set {
				if endsWithPrefixOf(form, name) {
					return true
				}
			}
		}
	}
	return false
}

// previewEllipsis is what the store appends to a preview it cut (store.previewEllipsis).
const previewEllipsis = "…"

// endsWithPrefixOf reports whether text ends in a prefix of name at least minCutPrefix bytes long
// that starts text or follows a cutBoundary.
func endsWithPrefixOf(text, name string) bool {
	n := len(name)
	if n > len(text) {
		n = len(text)
	}
	for k := n; k >= minCutPrefix; k-- {
		if k < len(name) && !utf8.RuneStart(name[k]) {
			continue
		}
		start := len(text) - k
		if text[start:] == name[:k] && (start == 0 || strings.IndexByte(cutBoundary, text[start-1]) >= 0) {
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
// stretch, cut short when the preview was, or a word.
var pathSelector = regexp.MustCompile(`(?i)(?:^|[\s(])` + recallPathSelector + `:("[^"]*"?|'[^']*'?|[^\s"']+)`)

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
	s := strings.TrimSpace(spec)
	anchored = strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || strings.HasPrefix(s, "~") ||
		driveSpelling.MatchString(s) || strings.HasPrefix(path.Clean(strings.ReplaceAll(s, `\`, "/")), "..")
	var segs []string
	for i, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch {
		case seg == "." || seg == "..":
		case i == 0 && strings.HasPrefix(seg, "~"):
			// The home anchor (`~`, `~user`) is where the rule is measured from, not a segment of the
			// paths it refuses.
		default:
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return "", anchored
	}
	last := segs[len(segs)-1]
	if !isGlob(last) {
		return screenText(last, true), anchored
	}
	for i := len(segs) - 2; i >= 0; i-- {
		if !isGlob(segs[i]) {
			return screenText(segs[i], true), anchored
		}
	}
	return screenText(longestLiteralRun(last), true), anchored
}

// longestLiteralRun is the longest stretch of seg, a glob segment, outside `*`, `?` and `[…]`.
func longestLiteralRun(seg string) string {
	var best, run strings.Builder
	keep := func() {
		if run.Len() > best.Len() {
			best.Reset()
			best.WriteString(run.String())
		}
		run.Reset()
	}
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '*', '?':
			keep()
		case '[':
			keep()
			if end := strings.IndexByte(seg[i+1:], ']'); end >= 0 {
				i += 1 + end
			} else {
				i = len(seg)
			}
		default:
			run.WriteByte(seg[i])
		}
	}
	keep()
	return best.String()
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

// protect is t, a sanitized text, with each spelling of the project root replaced by rootMark.
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
		if a > 0 && nameByte(t[a-1]) {
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
// `D:secret.txt`), a UNC share, a file:// URL, the root's spelling followed by `..` that leaves it,
// a POSIX absolute path of two segments or more (a single segment such as the flag `/c` is not one),
// or a relative path whose `..` leaves the project. A path starts p or follows whitespace or a
// delimiter, an assignment, a colon or an `@`. An http(s) or other URL is not a path.
func (j pathJudge) outsideIn(p string) bool {
	for i := 0; i < len(p); i++ {
		if i > 0 && strings.IndexByte(pathStartAfter, p[i-1]) < 0 {
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

// tokenOutside judges the stretch p[i:end] that starts a path (outsideIn).
func (j pathJudge) tokenOutside(p string, i, end int) bool {
	tok := p[i:end]
	c := tok[0]
	switch {
	case c == rootMark:
		return !rootRelativeInside(tok[1:])
	case len(tok) > 2 && tok[1] == ':' && (c|0x20 >= 'a' && c|0x20 <= 'z'):
		// A drive, absolute or drive-relative; a spelling of the root is already a rootMark.
		return true
	case len(tok) > 1 && isSep(c) && isSep(tok[1]):
		if scheme := schemeBefore(p, i); scheme != "" {
			return scheme == "file" && fileURLOutside(tok[2:])
		}
		return true
	case isSep(c):
		return hasSecondSegment(tok[1:])
	}
	return strings.Contains(tok, "..") && !absLike(tok) && relativeEscapes(tok)
}

func isSep(c byte) bool { return c == '/' || c == '\\' }

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
// inside the project: nothing, sentence punctuation, or a separator and segments whose `..` never
// climb above the root. A name glued to the root (`"C:\q\proj"x`, which a shell joins into projx)
// is a sibling.
func rootRelativeInside(rest string) bool {
	if strings.Trim(rest, ".,:") == "" {
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

// jsonShaped reports whether s is a canonical-JSON preview: valid JSON (an object, an array or a
// string), or one the store cut at its width (it starts an object or an array with a string and
// ends in `…`).
func jsonShaped(s string) bool {
	if s == "" || strings.IndexByte(`{["`, s[0]) < 0 {
		return false
	}
	return json.Valid([]byte(s)) ||
		(strings.HasSuffix(s, previewEllipsis) && (strings.HasPrefix(s, `{"`) || strings.HasPrefix(s, `["`)))
}

// jsonStr is one string of a JSON preview, decoded, and whether it is the value of a path-named
// argument (pathArgName), directly or as an element of that argument's array.
type jsonStr struct {
	value     string
	pathNamed bool
}

// jsonStrings returns every string of s, a JSON preview possibly cut at the store's width (the last
// string then runs to the end), in order. A string followed by `:` is an object's key, and the
// strings after it, up to the next `,` of that object, are its value's.
func jsonStrings(s string) []jsonStr {
	type frame struct {
		array bool
		key   string
	}
	var (
		out     []jsonStr
		stack   []frame
		pending string
	)
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
			if end > len(s) {
				end = len(s)
			}
			value := jsonUnquote(strings.TrimSuffix(s[i+1:end], previewEllipsis))
			next := end + 1
			for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
				next++
			}
			f := top()
			switch {
			case !f.array && next < len(s) && s[next] == ':':
				pending = value
				out = append(out, jsonStr{value: value})
			case f.array:
				out = append(out, jsonStr{value: value, pathNamed: pathArgName.MatchString(f.key)})
			default:
				out = append(out, jsonStr{value: value, pathNamed: pathArgName.MatchString(pending)})
			}
			i = end
		}
	}
	return out
}

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
// (or a sibling of the root) or names a path the build withholds: its basename or relative path as
// a whole word or path segment (namesKnownIn). A reason is the package's or the checkpointer's own
// prose with paths set in it as words, not a command that may glue them to anything, so a withheld
// file named `w` does not withhold every reason holding the letter; and a rule's literal alone does
// not withhold it, since an allowed path that merely holds one is shown in section 6 too.
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

// namesKnownIn reports whether t, in screen form with separators as `/`, holds the basename or
// relative path of a path the build withholds with a word or path-segment boundary on either side
// (a closing `.` before a boundary counts as one).
func (j pathJudge) namesKnownIn(t string) bool {
	for _, k := range j.knownText {
		for from := 0; from+len(k) <= len(t); {
			i := strings.Index(t[from:], k)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(k)
			before := start == 0 || strings.IndexByte(reasonBoundary, t[start-1]) >= 0
			after := end == len(t) || strings.IndexByte(reasonBoundary, t[end]) >= 0 ||
				(t[end] == '.' && (end+1 == len(t) || strings.IndexByte(reasonBoundary, t[end+1]) >= 0))
			if before && after {
				return true
			}
			from = start + 1
		}
	}
	return false
}

// reasonBoundary is what may stand on either side of a path named in a drop entry's reason.
const reasonBoundary = " /()[]{}=:,;@|&<>" + string(rootMark)

// redactReason is detail with the path it shows replaced by withheldDropID. A reason is read as an
// error chain, its parts joined by ": " (`checkpoint: git index unsupported: reading index: open
// <path>: The system cannot find the file specified.`): the parts before the first that shows the
// path stay, that part goes, and each later part that holds no separator (the system's own message)
// stays, up to the first that holds one. A reason whose path no part holds alone is replaced whole.
func (j pathJudge) redactReason(detail string) string {
	parts := strings.Split(detail, ": ")
	for i, p := range parts {
		if !j.reasonWithheld(p) {
			continue
		}
		kept := append(append([]string(nil), parts[:i]...), withheldDropID)
		for _, q := range parts[i+1:] {
			if strings.ContainsAny(q, `/\`) || j.reasonWithheld(q) {
				break
			}
			kept = append(kept, q)
		}
		return strings.Join(kept, ": ")
	}
	return withheldDropID
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
