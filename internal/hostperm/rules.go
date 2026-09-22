package hostperm

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// readTool is the one tool whose rules govern archived reads. Edit rules never block a read, and
// the host checks file paths against Read and Edit rules only.
const readTool = "Read"

// readParams are the Read tool's own top-level input fields. A `Read(name:value)` rule naming one of
// them is a parameter rule, not a path rule: the host ignores one on file_path (the primary content
// field) and matches the others only against a call that sets that parameter, which an archived
// retrieval never does.
var readParams = map[string]bool{"file_path": true, "offset": true, "limit": true, "pages": true}

// paramRuleRe matches the `name:value` shape of a parameter rule. Whitespace around the colon is
// ignored, as the host documents.
var paramRuleRe = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*:`)

// drivePathRe matches a Windows drive-letter path written the Windows way, `C:\x` or `C:/x`.
var drivePathRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// errMalformedRule is the error for a Read rule this package cannot parse. The host skips such an
// entry with a warning; this package cannot know what the entry was meant to deny, so the caller
// fails closed instead.
var errMalformedRule = errors.New("malformed Read permission rule")

// anchorKind is the base a path pattern is measured from.
type anchorKind int

const (
	// anchorCwd is `path` or `./path`: the session's working directory, which is the project root
	// as far as a plugin can know.
	anchorCwd anchorKind = iota
	// anchorFS is `//path`: the filesystem root.
	anchorFS
	// anchorHome is `~/path`: the user's home directory.
	anchorHome
	// anchorSettings is `/path`: a directory tied to the settings source that defined the rule.
	anchorSettings
)

// pattern is one compiled path rule.
type pattern struct {
	// raw is the rule exactly as written, e.g. `Read(./.env)`. It is kept for diagnostics only and
	// never reaches a retrieval response.
	raw string
	// neg marks a `!` carve-out.
	neg bool
	// inert marks a carve-out that can never match: a `!` pattern is read relative to the working
	// directory even when `//` or `~/` follows the `!`, so it cannot reach an anchored rule, and
	// this package declines to guess what else it was meant to reopen.
	inert bool
	// carvable reports whether a later `!` in the same list can carve this pattern: only rules
	// written as `path` or `./path` can be.
	carvable bool
	kind     anchorKind
	// up counts leading `..` segments, which move the anchor towards the filesystem root.
	up int
	// segs are the pattern's segments below its anchor, already case-folded when the platform
	// compares paths case-insensitively. An empty list matches the anchor and everything under it.
	segs []string
	// literal marks a pattern that is not usable as a gitignore pattern. The host still guards that
	// exact path with it, so it is compared segment for segment, never as a glob.
	literal bool
	// anchors are the directories the pattern is measured from, as folded POSIX segments. They are
	// filled when a RuleSet is built, because they depend on the source and the environment.
	anchors [][]string
}

// parsedRule is what one permissions entry compiles to.
type parsedRule struct {
	// relevant is false for an entry that does not govern Read at all.
	relevant bool
	// toolLevel marks `Read` with no path: every read.
	toolLevel bool
	// patterns holds the path pattern and any conservative aliases of it.
	patterns []*pattern
}

// parseRule compiles one `permissions.deny` or `permissions.ask` entry.
//
// Tool names are matched exactly, or as a glob in the tool-name position (`*` matches every tool),
// which the host accepts in deny and ask rules. A rule for another tool is irrelevant. An entry that
// names Read but cannot be parsed is an error, because a plugin cannot tell what it was meant to
// deny.
func parseRule(entry, goos string, fold bool) (parsedRule, error) {
	s := strings.TrimSpace(entry)
	if s == "" {
		return parsedRule{}, nil
	}
	tool, spec, hasSpec := s, "", false
	if i := strings.IndexByte(s, '('); i >= 0 {
		tool = strings.TrimSpace(s[:i])
		if !strings.HasSuffix(s, ")") {
			if toolMatchesRead(tool) {
				return parsedRule{}, fmt.Errorf("%w: %q has no closing parenthesis", errMalformedRule, entry)
			}
			return parsedRule{}, nil
		}
		spec, hasSpec = strings.TrimSpace(s[i+1:len(s)-1]), true
	}
	if !toolMatchesRead(tool) {
		return parsedRule{}, nil
	}
	if !hasSpec || spec == "" {
		return parsedRule{relevant: true, toolLevel: true}, nil
	}
	if m := paramRuleRe.FindStringSubmatch(spec); m != nil && readParams[m[1]] {
		// A parameter rule: see readParams. It governs no archived read.
		return parsedRule{}, nil
	}
	return parsedRule{relevant: true, patterns: compilePath(entry, spec, goos, fold)}, nil
}

// toolMatchesRead reports whether a rule's tool position names the Read tool. The host documents
// `*` in the tool-name position; `?` and `[...]` are honoured as well, because reading one of them
// as a literal could only let a rule meant for Read slip.
func toolMatchesRead(tool string) bool {
	if tool == readTool {
		return true
	}
	if !strings.ContainsAny(tool, "*?[") {
		return false
	}
	ok, err := path.Match(tool, readTool)
	return err == nil && ok
}

// compilePath compiles a path specifier, plus the conservative aliases this package adds on
// Windows. The host normalizes paths to POSIX form there (C:\x is /c/x), so a rule written with a
// drive letter or backslashes is, as written, a different pattern; the alias applies the reading a
// user most plausibly meant as well, and only ever adds refusals.
func compilePath(raw, spec, goos string, fold bool) []*pattern {
	out := []*pattern{compileOne(raw, spec, fold)}
	if goos != "windows" || strings.HasPrefix(spec, "!") {
		return out
	}
	switch {
	case drivePathRe.MatchString(spec):
		alias := "//" + strings.ToLower(spec[:1]) + "/" + strings.ReplaceAll(spec[3:], `\`, "/")
		out = append(out, compileAlias(raw, alias, fold))
	case strings.Contains(spec, `\`):
		out = append(out, compileAlias(raw, strings.ReplaceAll(spec, `\`, "/"), fold))
	}
	return out
}

// compileAlias compiles an alias pattern that no carve-out may reach.
func compileAlias(raw, spec string, fold bool) *pattern {
	p := compileOne(raw, spec, fold)
	p.carvable = false
	return p
}

// compileOne compiles one gitignore-dialect path pattern.
func compileOne(raw, spec string, fold bool) *pattern {
	p := &pattern{raw: raw}
	rest := spec
	if strings.HasPrefix(rest, "!") {
		p.neg, p.carvable = true, true
		rest = rest[1:]
		switch {
		case strings.HasPrefix(rest, "//"), strings.HasPrefix(rest, "~/"), rest == "~":
			p.inert = true
			return p
		case strings.HasPrefix(rest, "/"):
			// gitignore's own leading slash: anchored at the working directory, not at any depth.
			return finishPattern(p, anchorCwd, rest[1:], true, fold)
		}
		return finishPattern(p, anchorCwd, strings.TrimPrefix(rest, "./"), false, fold)
	}
	switch {
	case strings.HasPrefix(rest, "//"):
		return finishPattern(p, anchorFS, rest[2:], true, fold)
	case rest == "~":
		return finishPattern(p, anchorHome, "", true, fold)
	case strings.HasPrefix(rest, "~/"):
		return finishPattern(p, anchorHome, rest[2:], true, fold)
	case strings.HasPrefix(rest, "/"):
		return finishPattern(p, anchorSettings, rest[1:], true, fold)
	}
	p.carvable = true
	return finishPattern(p, anchorCwd, strings.TrimPrefix(rest, "./"), false, fold)
}

// finishPattern splits rest into segments and decides how deep the pattern matches.
//
// anchored is true when the pattern's position is fixed by how it was written (`//`, `~/`, `/`, or a
// carve-out's leading slash). A working-directory pattern is otherwise gitignore's: a bare name
// matches at any depth, and a pattern with a slash in it is anchored. The one documented exception
// for deny and ask rules is a single directory segment followed only by wildcards, such as
// `secrets/**`, which matches a directory of that name at any depth.
func finishPattern(p *pattern, kind anchorKind, rest string, anchored, fold bool) *pattern {
	p.kind = kind
	if fold {
		rest = strings.ToLower(rest)
	}
	rest = strings.TrimRight(rest, " ")
	rest = strings.TrimSuffix(rest, "/") // a trailing slash means "directories"; see Evaluate.
	var segs []string
	for _, s := range strings.Split(rest, "/") {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	for len(segs) > 0 && segs[0] == ".." {
		p.up++
		segs = segs[1:]
	}
	if p.up > 0 {
		// A `..` pattern is measured from a directory above the working directory, so no carve-out
		// written relative to the working directory can reach it.
		p.carvable = false
		anchored = true
	}
	for _, s := range segs {
		if s == ".." {
			// A `..` after the first real segment is not a gitignore pattern the host can use as a
			// glob; it still guards the exact path it spells.
			p.literal = true
		}
		if _, err := path.Match(s, ""); err != nil {
			p.literal = true
		}
	}
	if p.literal {
		p.segs = cleanLiteral(segs)
		return p
	}
	if !anchored && (len(segs) == 1 || singleDirectorySegment(segs)) {
		segs = append([]string{"**"}, segs...)
	}
	p.segs = segs
	return p
}

// singleDirectorySegment reports whether segs is one plain segment followed only by `*` or `**`.
func singleDirectorySegment(segs []string) bool {
	if len(segs) < 2 || segs[0] == "**" || segs[0] == "*" {
		return false
	}
	for _, s := range segs[1:] {
		if s != "*" && s != "**" {
			return false
		}
	}
	return true
}

// cleanLiteral resolves `..` lexically in a literal pattern.
func cleanLiteral(segs []string) []string {
	var out []string
	for _, s := range segs {
		if s == ".." {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// matchSelfOrAncestor reports whether the pattern matches rel or any directory above it. A rule
// that matches a directory denies everything inside it, which is gitignore's own rule and the
// host's ("a carve-out can't reopen a file inside a directory that a rule blocks as a whole").
func (p *pattern) matchSelfOrAncestor(rel []string) bool {
	if len(p.segs) == 0 && !p.literal {
		return true
	}
	for i := 1; i <= len(rel); i++ {
		if p.match(rel[:i]) {
			return true
		}
	}
	return false
}

// match reports whether the pattern matches exactly rel.
func (p *pattern) match(rel []string) bool {
	if p.inert {
		return false
	}
	if p.literal {
		if len(p.segs) != len(rel) {
			return false
		}
		for i := range rel {
			if p.segs[i] != rel[i] {
				return false
			}
		}
		return true
	}
	if len(p.segs) == 0 {
		return true
	}
	return matchSegments(p.segs, rel)
}

// matchSegments matches glob segments against path segments. `**` matches zero or more segments,
// except in last position, where gitignore gives it "everything inside": one or more. The table is
// quadratic in the two lengths and never backtracks, so a hostile pattern in a cloned repository's
// settings cannot make a retrieval spin.
func matchSegments(pat, segs []string) bool {
	n, m := len(pat), len(segs)
	w := m + 1
	dp := make([]bool, (n+1)*w)
	dp[n*w+m] = true
	for i := n - 1; i >= 0; i-- {
		last := i == n-1
		for j := m; j >= 0; j-- {
			var v bool
			switch {
			case pat[i] == "**" && last:
				v = j < m
			case pat[i] == "**":
				v = dp[(i+1)*w+j] || (j < m && dp[i*w+j+1])
			case j < m:
				ok, _ := path.Match(pat[i], segs[j])
				v = ok && dp[(i+1)*w+j+1]
			}
			dp[i*w+j] = v
		}
	}
	return dp[0]
}
