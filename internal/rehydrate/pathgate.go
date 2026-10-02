package rehydrate

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strings"

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
// for a path whatever its spelling, as a summary that is the path argument's value alone, and as a
// glob that selects a withheld path. readSummary finds each, and summaryWithheld judges it.

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
	// judged memoizes withheld() for the build: a path is named by a file pointer and again by the
	// summaries that read it, and each host judgement may consult the filesystem.
	judged map[string]bool
}

func newPathJudge(r Request, d Deps) pathJudge {
	j := pathJudge{root: r.ProjectRoot, judged: make(map[string]bool)}
	if d.HostPaths != nil {
		j.host = true
		j.refuses = d.HostPaths()
	}
	var known []string
	note := func(p string) {
		if isGlob(p) || !j.withheld(p) {
			return
		}
		if k, ok := j.key(p); ok {
			known = append(known, k)
		}
	}
	for _, f := range r.Checkpoint.Pointers.Files {
		note(f.Path)
	}
	for _, t := range r.Checkpoint.Pointers.Tools {
		_, pieces := readSummary(t.Summary)
		for _, p := range pieces {
			if p.pathLike() {
				note(p.text)
			}
		}
	}
	j.known = known
	return j
}

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
// arguments — into the pieces that could each name a path.
var summaryTokens = regexp.MustCompile("[\\s\"'`,;(){}\\[\\]<>|=]+")

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
		if !p.pathLike() {
			continue
		}
		if isGlob(p.text) {
			if j.globWithheld(p.text) {
				return true
			}
			continue
		}
		if j.withheld(p.text) {
			return true
		}
	}
	return false
}

// summaryPiece is one piece of a summary that may name a path.
type summaryPiece struct {
	text string
	// named says the piece is a path by position rather than by shape — the value of an argument
	// named for a path, the value of a selector so named, or a summary that is one bare word (the
	// store's preview of a Read or a path argument is the value alone) — so it is judged even when
	// it carries no separator, dot or `*`: a file needs none of them.
	named bool
}

// pathLike reports whether p is judged at all: it is named as a path, or it is rooted, or it
// carries a separator, a dot or a `*`.
func (p summaryPiece) pathLike() bool {
	return p.text != "" && (p.named || absLike(p.text) || strings.ContainsAny(p.text, `/\.*`))
}

// readSummary reads a tool pointer's summary for paths. texts is the summary as recorded, with JSON's
// doubled backslash undone as before, then the decoded value of every JSON string argument in it, so
// an escaped quote inside a value is read the way the tool read it; a summary is often a JSON object
// cut at the preview's width, so its arguments are found by pattern (jsonArgs) rather than by a
// decoder that would refuse the cut. pieces are what could each name a path: every token of every
// text, the value behind a token's selector prefix (`path:private/deny.txt`), every value of an
// argument named for a path, and the summary itself when it is one bare word.
func readSummary(s string) (texts []string, pieces []summaryPiece) {
	args := jsonArgs(s)
	texts = []string{strings.ReplaceAll(s, `\\`, `\`)}
	for _, a := range args {
		texts = append(texts, a.value)
		if pathArgName.MatchString(a.key) {
			pieces = append(pieces, summaryPiece{text: strings.TrimSpace(a.value), named: true})
		}
	}
	if toks := nonEmptyTokens(texts[0]); len(toks) == 1 {
		pieces = append(pieces, summaryPiece{text: toks[0], named: true})
	}
	for _, t := range texts {
		for _, tok := range nonEmptyTokens(t) {
			pieces = append(pieces, summaryPiece{text: tok})
			if m := summarySelector.FindStringSubmatch(tok); m != nil && !strings.HasPrefix(m[2], "//") {
				pieces = append(pieces, summaryPiece{text: m[2], named: pathArgName.MatchString(m[1])})
			}
		}
	}
	return texts, pieces
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

// withheldPathLabel and withheldSummary replace what a withheld pointer would have shown. Neither
// names the rule or the path: a rule spells the very path it protects.
const (
	withheldPathLabel = "file (path withheld: the host's permission rules refuse it, or it is outside the project)"
	withheldSummary   = "summary withheld: it names a path the host's permission rules refuse, or one outside the project"
)

// hashPointer is the restore call for content known only by its hash.
func hashPointer(h core.Hash) string {
	if h == (core.Hash{}) {
		return ""
	}
	return "expand(hash=" + h.String() + ")"
}
