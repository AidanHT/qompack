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
// quotes a path with a space), each decoded JSON string, each selector's value and each named path
// argument's value. summaryWithheld judges each piece, and a glob by what it selects.
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
	// `deny.txt`): a summary piece that spells one names the withheld file (namesKnown).
	tails map[string]bool
	// judged memoizes withheld() for the build: a path is named by a file pointer and again by the
	// summaries that read it, and each host judgement may consult the filesystem.
	judged map[string]bool
}

// newPathRules is a judge with the host's rules and no known paths: enough for withheld().
func newPathRules(r Request, d Deps) pathJudge {
	j := pathJudge{
		root: r.ProjectRoot, judged: make(map[string]bool), rootSpelling: rootSpellingOf(r.ProjectRoot),
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
	p := strings.TrimSpace(strings.ReplaceAll(path, `\\`, `\`))
	if p == "" {
		return false
	}
	if w, ok := j.judged[p]; ok {
		return w
	}
	w := !j.inside(p) || (j.host && (j.refuses == nil || j.refuses(p)))
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
// whole (summaryHomeOrVar).
func (j pathJudge) summaryWithheld(s string) bool {
	texts, pieces := j.readSummary(s)
	for _, t := range texts {
		if summaryHomeOrVar.MatchString(t) {
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
	if isGlob(p.text) {
		return j.judgedByRules(p) && j.globWithheld(p.text)
	}
	return j.namesKnown(p.text) || (j.judgedByRules(p) && j.withheld(p.text))
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
	case pieceNamed:
		return true
	case pieceToken:
		return absLike(p.text) || strings.ContainsAny(p.text, `/\.*`)
	}
	return false
}

// namesKnown reports whether p, a relative path, is a path this build withholds or one of its
// path-segment suffixes (a basename, say). recall's plain path: selector selects by equality and by
// path-segment suffix (store.pathSelector.weight), so `path:deny.txt` selects private/deny.txt, and
// Glob's preview spells its pattern apart from its directory: either way the piece names the file.
// A rooted path names one file, which withheld() judges as it is: the store's preview of a Read of
// the project's README.md is not withheld because private/README.md is.
func (j pathJudge) namesKnown(p string) bool {
	if len(j.tails) == 0 || absLike(strings.TrimSpace(p)) {
		return false
	}
	k, ok := j.key(p)
	return ok && j.tails[k]
}

// summaryPiece is one piece of a summary that may name a path.
type summaryPiece struct {
	text string
	kind pieceKind
}

// pieceKind is how a piece was read from a summary, which decides when it is judged
// (judgedByRules) and whether it can name a known path (newPathJudge, pieceWithheld).
type pieceKind uint8

const (
	// pieceToken is one word of a summary, one token of it (summaryTokens), or the value after a
	// word's first `=`.
	pieceToken pieceKind = iota
	// pieceNamed is a path by position rather than by shape — the value of an argument named for a
	// path, the value of a selector so named, or a summary that is one bare word (the store's
	// preview of a Read or a path argument is the value alone) — so it is judged even when it carries
	// no separator, dot or `*`: a file needs none of them.
	pieceNamed
	// pieceSpan is a stretch of a summary read whole where its producer delimits an argument: the
	// whole summary or JSON string, a prefix or a suffix of its words (Grep's and Glob's path, then
	// pattern, either of which may hold a space), or a quoted segment. A path with a space or a
	// delimiter in it (`private/deny (1).txt`) is one span, and no token spells it.
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
	pieces = appendPiece(pieces, t, pieceSpan)
	for _, tok := range summaryTokens.Split(t, -1) {
		pieces = appendPiece(pieces, tok, pieceToken)
	}
	for _, q := range quotedSegments(t) {
		pieces = appendPiece(pieces, q, pieceSpan)
	}
	words := summaryWord.FindAllStringIndex(t, -1)
	for _, w := range words {
		word := t[w[0]:w[1]]
		pieces = appendPiece(pieces, word, pieceToken)
		if _, v, ok := strings.Cut(word, "="); ok {
			pieces = appendPiece(pieces, v, pieceToken)
		}
	}
	n := len(words)
	if n < 2 || utf8.RuneCountInString(t) > maxOneLineRunes {
		return pieces
	}
	for k := 1; k < n; k++ {
		head := t[words[0][0]:words[k-1][1]]
		tail := t[words[k][0]:words[n-1][1]]
		pieces = appendPiece(pieces, head, pieceSpan)
		pieces = appendPiece(pieces, tail, pieceSpan)
		pieces = appendPiece(pieces, head+"/"+tail, pieceJoin)
	}
	return pieces
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

// appendPiece appends text, its project-root spellings restored (restoreRoot) and its trailing
// sentence punctuation cut, and the value behind its selector prefix: named when the selector is
// named for a path, and read as text was otherwise. A join's value stays a join, since the join is
// not a spelling found in the summary.
func appendPiece(pieces []summaryPiece, text string, kind pieceKind) []summaryPiece {
	if text = strings.TrimRight(restoreRoot(strings.TrimSpace(text)), ".:"); text == "" {
		return pieces
	}
	pieces = append(pieces, summaryPiece{text: text, kind: kind})
	if m := summarySelector.FindStringSubmatch(text); m != nil && !strings.HasPrefix(m[2], "//") {
		sel := kind
		if kind != pieceJoin && pathArgName.MatchString(m[1]) {
			sel = pieceNamed
		}
		pieces = append(pieces, summaryPiece{text: m[2], kind: sel})
	}
	return pieces
}

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
func (j pathJudge) globWithheld(g string) bool {
	if j.withheld(g) {
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
