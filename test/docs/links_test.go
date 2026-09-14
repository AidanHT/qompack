package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineLinkRe matches an inline markdown link and captures its target. The target stops at the
// first whitespace so an optional title ( [x](y "title") ) is not mistaken for part of the path.
var inlineLinkRe = regexp.MustCompile(`\[[^\]]*\]\(\s*([^)\s]+)(?:\s+"[^"]*")?\s*\)`)

// refDefRe matches a reference-style link definition: [label]: target.
var refDefRe = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(\S+)`)

// schemeRe matches an absolute URL scheme (https:, mailto:, …). Those targets are out of scope:
// this harness resolves what is on disk and makes no network request, ever (decision D10).
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// knownAnchorDefects are anchors this harness knows are broken and which this commit cannot fix,
// because the page that carries them is GENERATED and the CI `docs` job diffs it byte for byte.
//
// docs/mcp-tools.md's "Tools at a glance" table links each tool to its own `## ` heading through
// tools/devtool/genmcpdocs.go's anchorFor, which rewrites "_" as "-":
//
//	func anchorFor(name string) string { return strings.ReplaceAll(name, "_", "-") }
//
// GitHub does not do that. An underscore is a word character and survives slugging, so the
// heading "## `re_read`" offers #re_read and the link to #re-read lands nowhere. The fix is one
// line in the generator plus a regeneration, and it belongs to whoever owns that tool — not to a
// prose commit that is forbidden to touch either file.
//
// The list is enforced in BOTH directions: an unlisted broken anchor fails the test, and a listed
// anchor that starts resolving fails it too, so the fix cannot land without this entry being
// struck. Keys are "<file>#<anchor>".
var knownAnchorDefects = map[string]string{
	"docs/mcp-tools.md#re-read":           "genmcpdocs.go anchorFor rewrites _ as -; GitHub keeps _ (heading offers #re_read)",
	"docs/mcp-tools.md#already-tried":     "genmcpdocs.go anchorFor rewrites _ as -; GitHub keeps _ (heading offers #already_tried)",
	"docs/mcp-tools.md#record-eliminated": "genmcpdocs.go anchorFor rewrites _ as -; GitHub keeps _ (heading offers #record_eliminated)",
}

// TestRelativeLinksResolve is the reachability check: every scheme-less markdown link in README.md
// and in docs/**/*.md must name a file that exists, and every heading anchor must name a heading
// that exists in that file. Failures are reported as "file:line: target" and the scan continues,
// so one broken link does not hide the other nine.
func TestRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)
	files := markdownFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no markdown files found: the scan is looking in the wrong place")
	}

	// anchors are cached per target file: docs/mcp-tools.md is large and is linked many times.
	anchorCache := map[string]map[string]bool{}
	hit := map[string]bool{}

	for _, rel := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		for i, line := range scrubbed(t, abs) {
			lineNo := i + 1
			var targets []string
			for _, m := range inlineLinkRe.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			if m := refDefRe.FindStringSubmatch(line); m != nil {
				targets = append(targets, m[1])
			}
			for _, target := range targets {
				target = strings.Trim(target, "<>")
				if target == "" || schemeRe.MatchString(target) || strings.HasPrefix(target, "//") {
					continue
				}
				path, anchor, _ := strings.Cut(target, "#")

				// Resolve the file half, which is empty for a same-file "#anchor".
				targetRel := rel
				if path != "" {
					joined := filepath.Join(filepath.Dir(abs), filepath.FromSlash(path))
					info, err := os.Stat(joined)
					if err != nil || info.IsDir() {
						t.Errorf("%s:%d: %s (no such file)", rel, lineNo, target)
						continue
					}
					r, err := filepath.Rel(root, joined)
					if err != nil {
						t.Errorf("%s:%d: %s (outside the repository)", rel, lineNo, target)
						continue
					}
					targetRel = filepath.ToSlash(r)
				}
				if anchor == "" {
					continue
				}
				if !strings.HasSuffix(targetRel, ".md") {
					t.Errorf("%s:%d: %s (anchor into a non-markdown file)", rel, lineNo, target)
					continue
				}

				anchors, ok := anchorCache[targetRel]
				if !ok {
					anchors = anchorsOf(t, filepath.Join(root, filepath.FromSlash(targetRel)))
					anchorCache[targetRel] = anchors
				}
				key := targetRel + "#" + anchor
				_, known := knownAnchorDefects[key]
				switch {
				case anchors[anchor] && known:
					t.Errorf("%s:%d: %s now resolves: strike it from knownAnchorDefects", rel, lineNo, target)
				case anchors[anchor]:
					// resolved, as it should
				case known:
					hit[key] = true
				default:
					t.Errorf("%s:%d: %s (no heading with that anchor in %s)", rel, lineNo, target, targetRel)
				}
			}
		}
	}

	for key, why := range knownAnchorDefects {
		if !hit[key] {
			t.Errorf("knownAnchorDefects lists %s (%s) but no document links it: strike the entry", key, why)
		}
	}
}
