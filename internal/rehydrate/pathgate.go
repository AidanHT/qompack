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
// pointers: file and tool pointers, their summaries, and section 7's drop entries (D60(i)).
//
// Containment is decided here, against Request.ProjectRoot. The host's rules are the daemon's to
// load (this package may not import hostperm): Deps.HostPaths hands Build one judgement for the
// whole build.
//
// A tool pointer's summary is the call's arguments as the store previews them (store.argsPreview):
// the values of file_path, path, pattern, command and url joined by spaces, or the canonical JSON of
// a call that has none of them. readSummary reads its pieces from those shapes (ADR 0011 §23.5),
// which keeps the reading linear in the summary's words: the whole summary (a Read's preview is its
// path alone), each word, each prefix and suffix of its words (Grep's and Glob's path then pattern,
// either with spaces in it), each directory/pattern join (Glob), each quoted segment (a command
// quotes a path with a space), each word as a POSIX shell and as PowerShell read it (an escaped or
// doubled quote, an escaped space), each decoded JSON string, each selector's value and each named
// path argument's value (§23.8). summaryWithheld judges each piece, a glob by what it selects and a
// path: selector's value by what the store's selector selects, and looks for every path the build
// withholds anywhere in each text (namesKnownIn).
//
// The checkpointer's own drop entries are pointers too: five kinds are keyed by a file pointer's
// path (checkpointPathDrops), and section 7 and dropped() name a withheld one by hash
// (gateCheckpointDrops).

// HostPaths, when set on Deps, returns this build's judgement of the host's current Read rules:
// refuses(path) reports whether the rules deny, or ask before, reading path (as a pointer records
// it: project-relative or absolute). A nil refuses means the rules could not be established, and
// every path is then withheld (re_read fails closed the same way). A nil HostPaths applies
// containment alone. One build calls refuses for every piece it reads, from one goroutine.
type HostPaths func() (refuses func(path string) bool)

// pathJudge decides, for one build, which recorded paths the payload may show.
type pathJudge struct {
	root    string
	host    bool
	refuses func(string) bool
	// rootSpelling finds the project root spelled in a summary when the root holds a character the
	// reading splits at (protectRoot), and is nil otherwise.
	rootSpelling *regexp.Regexp
	// known holds every concrete path this build's pointers record that withheld() refuses inside
	// the project — a file pointer's own, one a tool summary names, and one a path-keyed checkpoint
	// drop names — in project-relative paths.Key form. A glob in a summary is withheld when it
	// selects one of them (globWithheld).
	known []string
	// tails holds every known path and each of its path-segment suffixes (`private/deny.txt`,
	// `deny.txt`): a summary piece that spells one names the withheld file (namesKnown), and so
	// does a text that holds one between word boundaries (namesKnownIn).
	tails map[string]bool
	// rootKey is the project root in paths.Key form with forward slashes, which namesKnownIn
	// prefixes to a known path to find it spelled absolute.
	rootKey string
	// judged memoizes the host's judgement (hostRefuses) for the build: a path is named by a file
	// pointer and again by the summaries that read it, and each host judgement may consult the
	// filesystem.
	judged map[string]bool
}

// newPathRules is a judge with the host's rules and no known paths: enough for withheld().
func newPathRules(r Request, d Deps) pathJudge {
	j := pathJudge{
		root: r.ProjectRoot, judged: make(map[string]bool), rootSpelling: rootSpellingOf(r.ProjectRoot),
	}
	if r.ProjectRoot != "" {
		j.rootKey = paths.Key(filepath.ToSlash(filepath.Clean(r.ProjectRoot)))
	}
	if d.HostPaths != nil {
		j.host = true
		j.refuses = d.HostPaths()
	}
	return j
}

// newPathJudge is the build's judge. It must read r's checkpoint drop entries before
// gateCheckpointDrops withholds their paths: a withheld path the checkpoint records only as a drop
// (a pointer_missing file, a file_pointer cut at the checkpoint's budget) is still a path a selector
// or a glob in a summary may select (w19 verifier V3).
func newPathJudge(r Request, d Deps) pathJudge {
	j := newPathRules(r, d)
	var known []string
	note := func(p string) {
		if k, ok := j.key(p); ok {
			known = append(known, k)
		}
	}
	for _, f := range r.Checkpoint.Pointers.Files {
		if !isGlob(f.Path) && j.withheld(f.Path) {
			note(f.Path)
		}
	}
	for _, e := range r.Checkpoint.Dropped {
		if checkpointPathDrops[e.Kind] && !isGlob(e.ID) && j.withheld(e.ID) {
			note(e.ID)
		}
	}
	for _, t := range r.Checkpoint.Pointers.Tools {
		_, pieces := j.readSummary(t.Summary)
		for _, p := range pieces {
			// A join is a reading of the preview, not a spelling found in it, so it names no path.
			if p.kind != pieceJoin && !isGlob(p.text) && j.judgedByRules(p) && j.withheld(p.text) {
				note(p.text)
			}
		}
	}
	j.known = known
	j.tails = make(map[string]bool, len(known))
	for _, k := range known {
		j.tails[k] = true
		for i := 0; i < len(k); i++ {
			if k[i] == '/' {
				j.tails[k[i+1:]] = true
			}
		}
	}
	return j
}

// pathJudgeFor is the judge Build made for d's build, or a new one for r when the item is built on
// its own.
func pathJudgeFor(r Request, d Deps) pathJudge {
	if d.judge != nil {
		return *d.judge
	}
	return newPathJudge(r, d)
}

// rulesKnown reports whether withheld() is a judgement rather than the fail-closed answer: the
// host's rules were established, or no host rules apply and containment alone decides. Only then
// is a piece that does not look like a path worth judging, since a fail-closed judge withholds
// every piece it is asked about.
func (j pathJudge) rulesKnown() bool { return !j.host || j.refuses != nil }

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

// pieceRefused is withheld() for a piece, except that an unquoted span or join that starts at a
// word inside the project, and only as a whole runs outside it, is judged by the host's rules
// alone. Such a stretch is the project root followed by more words (`<root> TODO`, Grep's preview
// with the root as its path, or `<root> && go test ./...` in a command): read whole it is the
// root's own name with a space and more after it, a sibling of the root, which is an artifact of
// reading words together, not a path anyone wrote, and every word of it is judged on its own
// (w19 round-2 review). A quoted segment, a path-named argument and a single word keep their
// containment, and a sibling that really holds a space after the root's own name
// (`<root> - Copy\x`) is the limit ADR 0011 §23.2 records.
func (j pathJudge) pieceRefused(p summaryPiece) bool {
	if p.lead != "" {
		text, lead := judgedSpelling(p.text), judgedSpelling(p.lead)
		if text != "" && absLike(text) && !j.inside(text) && absLike(lead) && j.inside(lead) {
			return j.hostRefuses(text)
		}
	}
	return j.withheld(p.text)
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
// form, a POSIX root, a Windows drive or a UNC share — whichever platform recorded it — or a path
// spelled from the home directory or an environment variable (homeOrVarRoot).
func absLike(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
		driveRoot.MatchString(p) || homeOrVarRoot.MatchString(p)
}

var driveRoot = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// homeOrVarRoot matches a path that starts at a shell's home directory (`~`, `~user/`) or at an
// environment variable (`$VAR`, `${VAR}`, `%VAR%`). Whatever it expands to, it is not a path the
// project root anchors, and read as project-relative it would be joined under the root, where a host
// deny rule on ~/.ssh/** never matches it. So it counts as rooted, and inside() finds it outside the
// project. `~` must end the path or be followed by a user name and a separator, so a project file
// named like an editor's lock file (`~$report.docx`) stays project-relative.
var homeOrVarRoot = regexp.MustCompile(
	`^(~[A-Za-z0-9._-]*([\\/]|$)|\$\{?[A-Za-z_][A-Za-z0-9_]*|%[A-Za-z_][A-Za-z0-9_()]*%)`)

// summaryTokens splits a tool pointer's summary — a path, a command line, or the tool's JSON
// arguments — into the tokens that could each name a path.
var summaryTokens = regexp.MustCompile("[\\s\"'`,;(){}\\[\\]<>|=]+")

// summaryWord is one whitespace-separated word of a summary.
var summaryWord = regexp.MustCompile(`\S+`)

// summaryHomeOrVar finds a home- or variable-rooted path anywhere in a summary, before it is split:
// a `%NAME%` whose name holds parentheses (`%ProgramFiles(x86)%`) would be cut apart by
// summaryTokens, and a path glued to a flag (`-i~/.ssh/key`) does not start its token. The segment
// must start the summary or follow a separator, a quote, an assignment, or a flag's letters.
var summaryHomeOrVar = regexp.MustCompile(
	`(^|[\s"'` + "`" + `=,;|<>(\[{:]|-[A-Za-z]*)` +
		`(~[A-Za-z0-9._-]*([\\/]|$)|\$\{?[A-Za-z_][A-Za-z0-9_]*|%[A-Za-z_][A-Za-z0-9_()]*%)`)

// summaryWithheld reports whether a tool pointer's summary names any path withheld() refuses, or a
// glob that selects one (globWithheld). A home- or variable-rooted path anywhere in it withholds it
// whole (summaryHomeOrVar), and so does a path the build withholds spelled anywhere in it
// (namesKnownIn).
func (j pathJudge) summaryWithheld(s string) bool {
	texts, pieces := j.readSummary(s)
	for _, t := range texts {
		if summaryHomeOrVar.MatchString(t) || j.namesKnownIn(t) {
			return true
		}
	}
	for _, p := range pieces {
		if j.pieceWithheld(p) {
			return true
		}
	}
	return false
}

// pieceWithheld reports whether one piece of a summary names a path the payload may not show: the
// rules refuse it, it lies outside the project, it is (or is a path-segment suffix of) a path the
// build withholds (namesKnown), or it is a glob that selects one (globWithheld).
func (j pathJudge) pieceWithheld(p summaryPiece) bool {
	if p.text == "" || ((p.kind == pieceSpan || p.kind == pieceJoin) && !j.rulesKnown()) {
		return false
	}
	if p.kind == pieceSelector && j.selectsKnown(p.text) {
		return true
	}
	if isGlob(p.text) {
		return j.judgedByRules(p) && j.globWithheld(p)
	}
	return j.namesKnown(p.text) || (j.judgedByRules(p) && j.pieceRefused(p))
}

// judgedByRules reports whether withheld() is asked about p. Against established rules every piece
// is: a file needs no dot, separator or `*`, and the store's preview of a built-in tool names no
// argument, so neither shape nor position marks every path (`credentials apikey` is Grep's path
// then its pattern). Failing closed, withheld() refuses whatever it is asked, so it is asked only
// about a piece named as a path or shaped like one, as before; a span or a join is never asked
// then, because every path in it is judged by its own words.
func (j pathJudge) judgedByRules(p summaryPiece) bool {
	if p.text == "" {
		return false
	}
	if j.rulesKnown() {
		return true
	}
	switch p.kind {
	case pieceNamed, pieceSelector:
		return true
	case pieceToken:
		return absLike(p.text) || strings.ContainsAny(p.text, `/\.*`)
	}
	return false
}

// namesKnown reports whether p, a relative path, is a path this build withholds or one of its
// path-segment suffixes (a basename, say): Glob's preview spells its pattern apart from its
// directory, and a word that spells a withheld file's basename names it as recall's selector would
// (selectsKnown). A rooted path names one file, which withheld() judges as it is: the store's
// preview of a Read of the project's README.md is not withheld because private/README.md is.
func (j pathJudge) namesKnown(p string) bool {
	if len(j.tails) == 0 || absLike(strings.TrimSpace(p)) {
		return false
	}
	k, ok := j.key(p)
	return ok && j.tails[k]
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

// namesKnownIn reports whether the text t spells a path this build withholds (each of its
// path-segment suffixes, and the whole path below the project root's absolute spelling), with a
// word boundary on either side, wherever its spaces fall. A path with a space that no producer
// delimits, in the middle of a free-text argument (`{"query":"find private/my secret.txt usages"}`)
// or a command (`cp private/my secret.txt backup/`), is read word by word, and no word or span
// spells it; this finds every such path the build records (w19 round-2 review). The text is
// compared in paths.Key form with forward slashes, so case folds where the platform's does. A
// path's separator is not a boundary, so the project's README.md is not found in another
// directory's path (namesKnown), and a path Qompack never recorded is the limit ADR 0011 §23.2
// records.
func (j pathJudge) namesKnownIn(t string) bool {
	if len(j.tails) == 0 {
		return false
	}
	s := paths.Key(strings.ReplaceAll(t, `\`, "/"))
	for k := range j.tails {
		if containsBounded(s, k) {
			return true
		}
	}
	if j.rootKey != "" {
		for _, k := range j.known {
			if containsBounded(s, j.rootKey+"/"+k) {
				return true
			}
		}
	}
	return false
}

// wordBoundary is what may stand before a path spelled in a text, or after it: whitespace, a quote,
// a delimiter a command or an argument list puts between words, a selector's or a drive's colon,
// and the `@` a mention starts with.
const wordBoundary = " \t\"'`,;(){}[]<>|=&:@"

// containsBounded reports whether s holds k with a word boundary (or s's start) before it and a
// word boundary, s's end, or a sentence's closing `.`, `!` or `?` before a boundary or the end
// after it.
func containsBounded(s, k string) bool {
	if k == "" {
		return false
	}
	for from := 0; from <= len(s)-len(k); {
		i := strings.Index(s[from:], k)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(k)
		if (start == 0 || strings.IndexByte(wordBoundary, s[start-1]) >= 0) && boundaryAfter(s, end) {
			return true
		}
		from = start + 1
	}
	return false
}

// boundaryAfter reports whether a path spelled in s may end at end.
func boundaryAfter(s string, end int) bool {
	if end == len(s) || strings.IndexByte(wordBoundary, s[end]) >= 0 {
		return true
	}
	if strings.IndexByte(".!?", s[end]) < 0 {
		return false
	}
	return end+1 == len(s) || strings.IndexByte(wordBoundary, s[end+1]) >= 0
}

// summaryPiece is one piece of a summary that may name a path.
type summaryPiece struct {
	text string
	kind pieceKind
	// lead is the first word of an unquoted span or join, which decides whether the stretch's own
	// containment is judged (pieceRefused); it is empty for every other piece.
	lead string
}

// pieceKind is how a piece was read from a summary, which decides when it is judged
// (judgedByRules) and whether it can name a known path (newPathJudge, pieceWithheld).
type pieceKind uint8

const (
	// pieceToken is one word of a summary, one token of it (summaryTokens), the value after a
	// word's first `=`, or a word as the shell that ran a command read it (shellWords).
	pieceToken pieceKind = iota
	// pieceNamed is a path by position rather than by shape — the value of an argument named for a
	// path, the value of a selector so named, or a summary that is one bare word (the store's
	// preview of a Read or a path argument is the value alone) — so it is judged even when it carries
	// no separator, dot or `*`: a file needs none of them.
	pieceNamed
	// pieceSelector is the value of recall's path: selector, a pieceNamed that is also judged by
	// what the store's selector would select (selectsKnown).
	pieceSelector
	// pieceSpan is a stretch of a summary read whole where its producer delimits an argument: the
	// whole summary or JSON string, a prefix or a suffix of its words (Grep's and Glob's path, then
	// pattern, either of which may hold a space), or a quoted segment. A path with a space or a
	// delimiter in it (`private/deny (1).txt`) is one span, and no token spells it. An unquoted
	// span carries its first word as its lead (pieceRefused).
	pieceSpan
	// pieceJoin is a summary's words joined at one space by a separator: the store's preview of Glob
	// or Grep is its directory, a space and its pattern, and the join is the path that pattern
	// selects there (`private deny.txt` → `private/deny.txt`). It is a reading of the preview, not a
	// spelling in it, so it never becomes a known path, and it is judged only against established
	// rules.
	pieceJoin
)

// readSummary reads a tool pointer's summary for paths. texts is the summary as recorded, with JSON's
// doubled backslash undone as before, then the decoded value of every JSON string argument in it, so
// an escaped quote inside a value is read the way the tool read it; a summary is often a JSON object
// cut at the preview's width, so its arguments are found by pattern (jsonArgs) rather than by a
// decoder that would refuse the cut. pieces are what could each name a path: every value of an
// argument named for a path, the summary itself when it is one bare word, and every piece
// textPieces reads from each text, with the value behind each one's selector prefix
// (`path:private/deny.txt`).
func (j pathJudge) readSummary(s string) (texts []string, pieces []summaryPiece) {
	args := jsonArgs(s)
	texts = []string{strings.ReplaceAll(s, `\\`, `\`)}
	for _, a := range args {
		texts = append(texts, a.value)
		if pathArgName.MatchString(a.key) {
			pieces = append(pieces, summaryPiece{text: strings.TrimSpace(a.value), kind: pieceNamed})
		}
	}
	if toks := nonEmptyTokens(j.protectRoot(texts[0])); len(toks) == 1 {
		pieces = append(pieces, summaryPiece{text: restoreRoot(toks[0]), kind: pieceNamed})
	}
	for _, t := range texts {
		pieces = textPieces(pieces, j.protectRoot(t))
	}
	return texts, pieces
}

// textPieces appends the pieces of t, read from the shapes the store's preview takes: t whole, each
// token (summaryTokens), each quoted segment whole, each word and the value after its first `=`,
// and, for a t no wider than maxOneLineRunes, each proper prefix and suffix of its words and each
// join of its words at one space. That is linear in t's words: a path whose spaces no producer
// delimits — unquoted between two other words of a command — is read word by word, as the shell
// that ran the command read it (ADR 0011 §23.5). A wider t is never the store's preview
// (store.argsPreviewMax), and its prefixes, suffixes and joins are not read: each is as wide as t.
// t's own spellings of the project root are protected (protectRoot), so no piece cuts the root
// apart.
func textPieces(pieces []summaryPiece, t string) []summaryPiece {
	words := summaryWord.FindAllStringIndex(t, -1)
	if len(words) == 0 {
		return pieces
	}
	lead := func(k int) string { return t[words[k][0]:words[k][1]] }
	pieces = appendPiece(pieces, summaryPiece{text: t, kind: pieceSpan, lead: lead(0)})
	for _, tok := range summaryTokens.Split(t, -1) {
		pieces = appendPiece(pieces, summaryPiece{text: tok, kind: pieceToken})
	}
	for _, q := range quotedSegments(t) {
		pieces = appendPiece(pieces, summaryPiece{text: q, kind: pieceSpan})
	}
	plain := make(map[string]bool, len(words))
	for _, w := range words {
		word := t[w[0]:w[1]]
		plain[word] = true
		pieces = appendWord(pieces, word)
	}
	// The words as the shell that ran a command read them, where that differs from the words as
	// written: an escape undone, a quoted stretch joined to its word, `&&` ending a word.
	for _, word := range shellWords(t) {
		if !plain[word] {
			plain[word] = true
			pieces = appendWord(pieces, word)
		}
	}
	n := len(words)
	if n < 2 || utf8.RuneCountInString(t) > maxOneLineRunes {
		return pieces
	}
	for k := 1; k < n; k++ {
		head := t[words[0][0]:words[k-1][1]]
		tail := t[words[k][0]:words[n-1][1]]
		pieces = appendPiece(pieces, summaryPiece{text: head, kind: pieceSpan, lead: lead(0)})
		pieces = appendPiece(pieces, summaryPiece{text: tail, kind: pieceSpan, lead: lead(k)})
		pieces = appendPiece(pieces, summaryPiece{text: head + "/" + tail, kind: pieceJoin, lead: lead(0)})
	}
	return pieces
}

// appendWord appends one word of a text and the value after its first `=` (an option's value).
func appendWord(pieces []summaryPiece, word string) []summaryPiece {
	pieces = appendPiece(pieces, summaryPiece{text: word, kind: pieceToken})
	if _, v, ok := strings.Cut(word, "="); ok {
		pieces = appendPiece(pieces, summaryPiece{text: v, kind: pieceToken})
	}
	return pieces
}

// shellWords returns t's words as a POSIX shell reads them and as PowerShell reads them (the two
// shells Claude Code's Bash and PowerShell tools run), each word once. A POSIX shell takes `'...'`
// literally, honours `\"`, `\\`, `\$` and “ \` “ inside `"..."`, takes the character after a
// backslash literally outside quotes, and ends a word at unquoted whitespace and at `&`, `|`, `;`,
// `<`, `>`, `(` and `)`. PowerShell reads `”` inside `'...'` and `""` inside `"..."` as one quote,
// escapes the next character with a backtick outside single quotes, and ends a word at whitespace
// and at `&`, `|`, `;`, `<`, `>`, `(`, `)`, `{`, `}` and `,`. So `cat private/John\'s\ notes.txt`,
// `Get-Content 'private/John”s notes.txt'` and `echo "a\"b" && cat "private/deny (1).txt"` each
// yield the path their shell opened (w19 round-2 review). A summary that is not a command yields
// words no rule refuses. Each reading is linear in t.
func shellWords(t string) []string {
	return append(posixWords(t), powershellWords(t)...)
}

// posixWords is t's words as a POSIX shell reads them (shellWords).
func posixWords(t string) []string {
	var w shellWord
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == ' ' || c == '\t' || strings.IndexByte("&|;<>()", c) >= 0:
			w.end()
		case c == '\\':
			w.open = true
			if i+1 < len(t) {
				i++
				w.b.WriteByte(t[i])
			}
		case c == '\'':
			w.open = true
			end := strings.IndexByte(t[i+1:], '\'')
			if end < 0 {
				end = len(t) - i - 1
			}
			w.b.WriteString(t[i+1 : i+1+end])
			i += 1 + end
		case c == '"':
			w.open = true
			for i++; i < len(t) && t[i] != '"'; i++ {
				if t[i] == '\\' && i+1 < len(t) && strings.IndexByte("\"\\$`", t[i+1]) >= 0 {
					i++
				}
				w.b.WriteByte(t[i])
			}
		default:
			w.open = true
			w.b.WriteByte(c)
		}
	}
	w.end()
	return w.words
}

// powershellWords is t's words as PowerShell reads them (shellWords).
func powershellWords(t string) []string {
	var w shellWord
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == ' ' || c == '\t' || strings.IndexByte("&|;<>(){},", c) >= 0:
			w.end()
		case c == '`':
			w.open = true
			if i+1 < len(t) {
				i++
				w.b.WriteByte(t[i])
			}
		case c == '\'' || c == '"':
			w.open = true
			for i++; i < len(t); i++ {
				if t[i] == c {
					if i+1 < len(t) && t[i+1] == c {
						i++
					} else {
						break
					}
				} else if c == '"' && t[i] == '`' && i+1 < len(t) {
					i++
				}
				w.b.WriteByte(t[i])
			}
		default:
			w.open = true
			w.b.WriteByte(c)
		}
	}
	w.end()
	return w.words
}

// shellWord collects the words of one shell reading.
type shellWord struct {
	b     strings.Builder
	open  bool
	words []string
}

// end closes the word being read, if any.
func (w *shellWord) end() {
	if w.open {
		w.words = append(w.words, w.b.String())
		w.b.Reset()
		w.open = false
	}
}

// quotedSegments returns the text inside each pair of matching quotes (double quotes, apostrophes
// or backticks) in t, left to right. A quote with no partner later in t, such as the apostrophe in
// an unquoted `John's notes.txt`, opens nothing. Backslashes escape nothing: on Windows they are
// separators.
func quotedSegments(t string) []string {
	var out []string
	unpaired := map[byte]bool{}
	for i := 0; i < len(t); i++ {
		q := t[i]
		if (q != '"' && q != '\'' && q != '`') || unpaired[q] {
			continue
		}
		end := strings.IndexByte(t[i+1:], q)
		if end < 0 {
			// No later quote of this kind, so none of the later ones has a partner either.
			unpaired[q] = true
			continue
		}
		out = append(out, t[i+1:i+1+end])
		i += 1 + end
	}
	return out
}

// appendPiece appends p, its text's project-root spellings restored (restoreRoot) and its trailing
// sentence punctuation cut, then the value behind its selector prefix: a pieceSelector behind
// recall's `path:`, named behind another selector named for a path, and read as p was otherwise. A
// join's value stays a join, since the join is not a spelling found in the summary.
func appendPiece(pieces []summaryPiece, p summaryPiece) []summaryPiece {
	if p.text = strings.TrimRight(restoreRoot(strings.TrimSpace(p.text)), ".:"); p.text == "" {
		return pieces
	}
	p.lead = strings.TrimSpace(restoreRoot(p.lead))
	pieces = append(pieces, p)
	if m := summarySelector.FindStringSubmatch(p.text); m != nil && !strings.HasPrefix(m[2], "//") {
		sel := summaryPiece{text: m[2], kind: p.kind}
		switch {
		case p.kind == pieceJoin:
		case strings.EqualFold(m[1], recallPathSelector):
			sel.kind = pieceSelector
		case pathArgName.MatchString(m[1]):
			sel.kind = pieceNamed
		}
		pieces = append(pieces, sel)
	}
	return pieces
}

// recallPathSelector is the name of recall's path selector (internal/mcp's selectorPath, `path:`),
// whose value the store matches against recorded paths (store.pathSelector.weight).
const recallPathSelector = "path"

// nonEmptyTokens is summaryTokens' split of t, each token's trailing sentence punctuation cut.
func nonEmptyTokens(t string) []string {
	var out []string
	for _, tok := range summaryTokens.Split(t, -1) {
		if tok = strings.TrimRight(tok, ".:"); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// The project root is often spelled with a space in it (C:\Users\John Smith\proj), and then the
// first word of every absolute summary in the project is the root cut at that space: an absolute
// path outside the project, which withheld every such summary (D60, round 1's open issue). So the
// reading never cuts the root apart: protectRoot swaps each character of a root spelling that the
// reading splits at for a control character no store preview carries (store.previewString strips
// them), and appendPiece swaps it back, so every piece is judged in the summary's own spelling.
// Text that is only part of the root's spelling (C:\Users\John alone) is not a spelling of it, and
// text that runs past it into a sibling (C:\Users\John Smith\proj2) is one word with it; either is
// judged, and withheld, as the path outside the project it is.
var (
	rootProtect = strings.NewReplacer(
		" ", "\x01", "\t", "\x02", `"`, "\x03", "'", "\x04", "`", "\x05", ",", "\x06", ";", "\x07",
		"(", "\x08", ")", "\x0e", "{", "\x0f", "}", "\x10", "[", "\x11", "]", "\x12", "<", "\x13",
		">", "\x14", "|", "\x15", "=", "\x16")
	rootRestore = strings.NewReplacer(
		"\x01", " ", "\x02", "\t", "\x03", `"`, "\x04", "'", "\x05", "`", "\x06", ",", "\x07", ";",
		"\x08", "(", "\x0e", ")", "\x0f", "{", "\x10", "}", "\x11", "[", "\x12", "]", "\x13", "<",
		"\x14", ">", "\x15", "|", "\x16", "=")
)

// rootSplitChars are the characters the reading splits a summary at: whitespace, quotes and
// summaryTokens' delimiters.
const rootSplitChars = " \t\"'`,;(){}[]<>|="

// rootSpellingOf matches root as a summary spells it, or is nil when root holds no character the
// reading splits at, so that no piece could cut it apart. On Windows a separator is either slash and
// the match ignores case, as the filesystem and filepath.Rel do; elsewhere it is exact.
func rootSpellingOf(root string) *regexp.Regexp {
	if root == "" || !strings.ContainsAny(root, rootSplitChars) {
		return nil
	}
	var b strings.Builder
	sep := "/"
	if runtime.GOOS == "windows" {
		b.WriteString("(?i)")
		sep = `[\\/]`
	}
	for _, r := range filepath.Clean(root) {
		if r == '/' || r == filepath.Separator {
			b.WriteString(sep)
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	return regexp.MustCompile(b.String())
}

// protectRoot is t with each spelling of the project root made one unsplittable word.
func (j pathJudge) protectRoot(t string) string {
	if j.rootSpelling == nil {
		return t
	}
	return j.rootSpelling.ReplaceAllStringFunc(t, rootProtect.Replace)
}

// restoreRoot undoes protectRoot.
func restoreRoot(t string) string { return rootRestore.Replace(t) }

// summarySelector splits a `name:value` token: a recall selector (`path:`, `symbol:`, `tool:`) or
// any prefix a query is spelled with the same way. The name is two characters or more, so a Windows
// drive is never read as one; a value starting `//` is a URL's authority, and readSummary leaves it.
var summarySelector = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]+):(.+)$`)

// pathArgName matches an argument or selector name that holds a path: path, file_path,
// notebook_path, filepath, paths, file, filename, dir, directory, folder, cwd and their kin.
var pathArgName = regexp.MustCompile(
	`(?i)(paths?$|(^|[_-])(files?|file_?names?|dirs?|directory|directories|folders?|cwd)$)`)

// jsonArg is one string argument found in a summary, its key and value decoded.
type jsonArg struct{ key, value string }

var (
	// jsonStringArg is a string-valued member; the value may run to the summary's end, cut there.
	jsonStringArg = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"\s*:\s*"((?:[^"\\]|\\.)*)`)
	// jsonArrayArg is an array-valued member, up to its close or the summary's end.
	jsonArrayArg = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"\s*:\s*\[([^\]]*)`)
	// jsonString is one string of an array.
	jsonString = regexp.MustCompile(`"((?:[^"\\]|\\.)*)`)
)

// jsonArgs returns a JSON-object summary's string arguments, each array element as its own argument
// under the array's key.
func jsonArgs(s string) []jsonArg {
	if !strings.Contains(s, `"`) {
		return nil
	}
	var out []jsonArg
	for _, m := range jsonStringArg.FindAllStringSubmatch(s, -1) {
		out = append(out, jsonArg{key: jsonUnquote(m[1]), value: jsonUnquote(m[2])})
	}
	for _, m := range jsonArrayArg.FindAllStringSubmatch(s, -1) {
		key := jsonUnquote(m[1])
		for _, e := range jsonString.FindAllStringSubmatch(m[2], -1) {
			out = append(out, jsonArg{key: key, value: jsonUnquote(e[1])})
		}
	}
	return out
}

// jsonUnquote decodes one JSON string body; a body the preview cut mid-escape is undone by hand.
func jsonUnquote(s string) string {
	var out string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &out); err == nil {
		return out
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\/`, `/`).Replace(s)
}

// globMeta is the glob syntax a summary's pattern can carry: the store's path-selector grammar
// (store.compilePathSelector), which recall's path: selector and the Glob tool share.
const globMeta = "*?["

// isGlob reports whether p is a pattern rather than a path.
func isGlob(p string) bool { return strings.ContainsAny(p, globMeta) }

// globWithheld reports whether the glob g may not be shown: as written it lies outside the project or
// is refused (withheld), or it selects a path this build withholds (pathJudge.known). A glob without
// a separator matches at any depth (rules.Match), as recall's path: selector does. Only the paths the
// build records are known, so a glob that selects nothing Qompack recorded is judged as written.
func (j pathJudge) globWithheld(p summaryPiece) bool {
	g := p.text
	if j.pieceRefused(p) {
		return true
	}
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
	p = strings.TrimSpace(strings.ReplaceAll(p, `\\`, `\`))
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

// withheldPathLabel, withheldSummary and withheldPathNote replace what a withheld pointer would have
// shown. None names the rule or the path: a rule spells the very path it protects.
const (
	withheldPathNote  = "path withheld: the host's permission rules refuse it, or it is outside the project"
	withheldPathLabel = "file (" + withheldPathNote + ")"
	withheldSummary   = "summary withheld: it names a path the host's permission rules refuse, or one outside the project"
	// withheldDropID stands in for the path a checkpoint drop entry was keyed by when the
	// checkpoint no longer holds the pointer's hash (gateCheckpointDrops).
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
