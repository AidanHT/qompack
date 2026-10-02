package rehydrate

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
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
// path reaches the model, so it follows the same two rules and points by hash instead.
//
// Containment is decided here, against Request.ProjectRoot. The host's rules are the daemon's to
// load (this package may not import hostperm): Deps.HostPaths hands Build one judgement for the
// whole build.
//
// A tool pointer's summary is the call's arguments, and a path reaches them in more shapes than a
// bare token (UAT-12 F1 on candidate 7: `{"query":"path:private/deny.txt"}` passed the gate under the
// deny rule Read(./private/deny.txt)): behind a selector prefix, as the value of an argument named
// for a path whatever its spelling, as a summary that is the path argument's value alone, as
// several words (a path with a space in it), as a directory and a pattern the store's preview
// joined with a space (Glob), as a basename (recall's plain selector selects by path-segment
// suffix), and as a glob that selects a withheld path. readSummary finds each, and summaryWithheld
// judges it.
//
// The checkpointer's own drop entries are pointers too: five kinds are keyed by a file pointer's
// path (checkpointPathDrops), and section 7 and dropped() name a withheld one by hash
// (gateCheckpointDrops).

// HostPaths, when set on Deps, returns this build's judgement of the host's current Read rules:
// refuses(path) reports whether the rules deny, or ask before, reading path (as a pointer records
// it: project-relative or absolute). A nil refuses means the rules could not be established, and
// every path is then withheld (re_read fails closed the same way). A nil HostPaths applies
// containment alone.
type HostPaths func() (refuses func(path string) bool)

// pathJudge decides, for one build, which recorded paths the payload may show.
type pathJudge struct {
	root    string
	host    bool
	refuses func(string) bool
	// known holds every concrete path this build's pointers record that withheld() refuses inside
	// the project — a file pointer's own, and one a tool summary names — in project-relative
	// paths.Key form. A glob in a summary is withheld when it selects one of them (globWithheld).
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
	j := pathJudge{root: r.ProjectRoot, judged: make(map[string]bool)}
	if d.HostPaths != nil {
		j.host = true
		j.refuses = d.HostPaths()
	}
	return j
}

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
	for _, t := range r.Checkpoint.Pointers.Tools {
		_, pieces := readSummary(t.Summary)
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

// summarySegments is summaryTokens without the whitespace: it splits a summary where no path the
// gate reads continues, so that within a segment whitespace may be part of a path, or the space the
// store's preview joined two arguments with (store.argsPreview).
var summarySegments = regexp.MustCompile("[\"'`,;(){}\\[\\]<>|=]+")

// summaryWord is one whitespace-separated word of a segment.
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
	texts, pieces := readSummary(s)
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
	if p.text == "" || ((p.kind == pieceRun || p.kind == pieceJoin) && !j.rulesKnown()) {
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
// about a piece named as a path or shaped like one, as before; a run or a join is never asked
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
	// pieceToken is one token of a summary.
	pieceToken pieceKind = iota
	// pieceNamed is a path by position rather than by shape — the value of an argument named for a
	// path, the value of a selector so named, or a summary that is one bare word (the store's
	// preview of a Read or a path argument is the value alone) — so it is judged even when it carries
	// no separator, dot or `*`: a file needs none of them.
	pieceNamed
	// pieceRun is two or more consecutive words of a segment read as one: a path with a space in it
	// (`private/my secret.txt`), which no single token spells.
	pieceRun
	// pieceJoin is a whole segment's words joined at one space by a separator: the store's preview of
	// Glob or Grep is its directory, a space and its pattern, and the join is the path that pattern
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
// argument named for a path, the summary itself when it is one bare word, and for every text each
// token, each run of words and each directory-pattern join of a segment (segmentPieces), with the
// value behind each one's selector prefix (`path:private/deny.txt`).
func readSummary(s string) (texts []string, pieces []summaryPiece) {
	args := jsonArgs(s)
	texts = []string{strings.ReplaceAll(s, `\\`, `\`)}
	for _, a := range args {
		texts = append(texts, a.value)
		if pathArgName.MatchString(a.key) {
			pieces = append(pieces, summaryPiece{text: strings.TrimSpace(a.value), kind: pieceNamed})
		}
	}
	if toks := nonEmptyTokens(texts[0]); len(toks) == 1 {
		pieces = append(pieces, summaryPiece{text: toks[0], kind: pieceNamed})
	}
	for _, t := range texts {
		pieces = segmentPieces(pieces, t)
	}
	return texts, pieces
}

// segmentPieces appends the pieces of t's segments: every word (the tokens summaryTokens splits),
// and, in a segment no wider than maxOneLineRunes, every run of two or more consecutive words and
// every join of its words at one space by a separator. A wider segment is read word by word only:
// a summary is shown cut to that width, and the store's preview is never wider
// (store.argsPreviewMax), so only a summary Qompack did not write reaches that case, and the
// reading stays linear in the summary's length rather than quadratic in a segment's.
func segmentPieces(pieces []summaryPiece, t string) []summaryPiece {
	for _, seg := range summarySegments.Split(t, -1) {
		words := summaryWord.FindAllStringIndex(seg, -1)
		for _, w := range words {
			pieces = appendPiece(pieces, seg[w[0]:w[1]], pieceToken)
		}
		n := len(words)
		if n < 2 || utf8.RuneCountInString(seg) > maxOneLineRunes {
			continue
		}
		for i := 0; i < n; i++ {
			for k := i + 1; k < n; k++ {
				pieces = appendPiece(pieces, seg[words[i][0]:words[k][1]], pieceRun)
			}
		}
		for k := 1; k < n; k++ {
			dir := seg[words[0][0]:words[k-1][1]]
			pattern := seg[words[k][0]:words[n-1][1]]
			pieces = appendPiece(pieces, dir+"/"+pattern, pieceJoin)
		}
	}
	return pieces
}

// appendPiece appends text, its trailing sentence punctuation cut, and the value behind its selector
// prefix: named when the selector is named for a path, and read as text was otherwise. A join's
// value stays a join, since the join is not a spelling found in the summary.
func appendPiece(pieces []summaryPiece, text string, kind pieceKind) []summaryPiece {
	if text = strings.TrimRight(text, ".:"); text == "" {
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
// known. Section 7 and dropped() both read the result. r's slice is never modified, and the host's
// rules are consulted only when some entry is keyed by a path.
func gateCheckpointDrops(r Request, d Deps) []checkpoint.DropEntry {
	drops := r.Checkpoint.Dropped
	keyed := false
	for _, e := range drops {
		keyed = keyed || (checkpointPathDrops[e.Kind] && e.ID != "")
	}
	if !keyed {
		return drops
	}
	j := newPathRules(r, d)
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
