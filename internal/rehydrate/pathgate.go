package rehydrate

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Section 6's pointers never show a path the host currently refuses or a path outside the project
// (owner decision D50, C4.6; UAT-12 F4). re_read refuses a path the host's permission rules deny or
// ask about, and withholds one outside the project; the rehydration payload is another way the same
// path reaches the model, so it follows the same two rules and points by hash instead.
//
// Containment is decided here, against Request.ProjectRoot. The host's rules are the daemon's to
// load (this package may not import hostperm): Deps.HostPaths hands Build one judgement for the
// whole build.

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
}

func newPathJudge(r Request, d Deps) pathJudge {
	j := pathJudge{root: r.ProjectRoot}
	if d.HostPaths != nil {
		j.host = true
		j.refuses = d.HostPaths()
	}
	return j
}

// withheld reports whether path may not be shown: it lies outside the project, or the host's rules
// refuse it, or they could not be established.
func (j pathJudge) withheld(path string) bool {
	p := strings.TrimSpace(strings.ReplaceAll(path, `\\`, `\`))
	if p == "" {
		return false
	}
	if !j.inside(p) {
		return true
	}
	if !j.host {
		return false
	}
	return j.refuses == nil || j.refuses(p)
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
// form, a POSIX root, a Windows drive or a UNC share — whichever platform recorded it.
func absLike(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || driveRoot.MatchString(p)
}

var driveRoot = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// summaryTokens splits a tool pointer's summary — a path, a command line, or the tool's JSON
// arguments — into the pieces that could each name a path.
var summaryTokens = regexp.MustCompile("[\\s\"'`,;(){}\\[\\]<>|=]+")

// summaryWithheld reports whether a tool pointer's summary names any path withheld() refuses. A
// piece is judged when it could be a path at all: it is rooted, or it carries a separator or a dot.
func (j pathJudge) summaryWithheld(s string) bool {
	for _, tok := range summaryTokens.Split(strings.ReplaceAll(s, `\\`, `\`), -1) {
		tok = strings.TrimRight(tok, ".:")
		if tok == "" || !(absLike(tok) || strings.ContainsAny(tok, `/\.`)) {
			continue
		}
		if j.withheld(tok) {
			return true
		}
	}
	return false
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
