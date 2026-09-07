package rules

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// scanner is the real L5 Scanner: two disk walks, no state, safe to share across goroutines.
type scanner struct{ log logging.Logger }

// Option configures a Scanner. New takes options, not required arguments, because
// internal/rules/rulestest/suite_test.go calls rules.New() argument-free and — being a <pkg>test
// subpackage restricted to its own package, testutil and core — cannot construct a logger to
// pass. A variadic parameter keeps that call site compiling untouched.
type Option func(*scanner)

// WithLogger routes the scanner's Debug-level skip reporting to log. Every skip the scanner makes
// is silent by design at Info and above — a rule file it cannot read is degraded context, not a
// hook failure — so this is the only way to see why a rule did not come back. A nil logger is
// ignored rather than installed, so a caller that has not built one yet cannot panic the walk.
func WithLogger(log logging.Logger) Option {
	return func(s *scanner) {
		if log != nil {
			s.log = log
		}
	}
}

// The two discovery roots, relative to projectRoot and searched in this order. They are
// normative and fixed: there is no config key, because a project that could move them could
// silently make the rehydrator restore rules Claude Code itself never loads.
const (
	// rulesDir is searched recursively: everything below .claude/rules is a rule file, and
	// projects nest them by area.
	rulesDir = ".claude/rules"
	// dotClaudeDir is searched at depth 1 only. .claude also holds skills/, agents/, settings
	// files and other machinery, none of which are rules; only a *.md sitting directly in it is.
	dotClaudeDir = ".claude"
)

// scanGlobs is the discovery-root list in search order.
var scanGlobs = []string{rulesDir, dotClaudeDir}

// mdExt is the extension a rule file carries. It is compared case-insensitively, because a
// Windows or macOS author can produce RULE.MD and mean the same file.
const mdExt = ".md"

// claudeMDName is the canonical spelling of a nested rule file. On a case-folding filesystem a
// claude.md on disk satisfies a stat for it, which is correct; the Path reported back is always
// this spelling, so goldens downstream stay stable across platforms.
const claudeMDName = "CLAUDE.md"

// maxRuleBytes is the read ceiling for one rule file: past 256 KiB it is not a rule file, it is
// something else that happens to end in .md, and reading it is a cost the rehydration path — a
// hook the user is waiting on — cannot absorb. The nomagic annotation sits on the literal's own
// line because that is the line the analyzer keys its allow-list on.
const maxRuleBytes = 262144 //nomagic:allow byte ceiling for rule-file reads; unrelated to runtime.mcp.maxResponseBytes

// maxAncestorDepth bounds the upward CLAUDE.md walk. It is a guard, not a policy: a pointer more
// than 32 directories below the project root is a symlink loop or a generated tree, and either
// way the rules above it are not what the user is waiting on.
const maxAncestorDepth = 32

// bytesPerToken is the divisor of Rule.Tokens' baseline estimate — see baselineTokens.
const bytesPerToken = 4

// New returns the L5 rule Scanner of 00-ARCHITECTURE.md §5.15: PathScoped finds `paths:`-scoped
// rule files whose globs match the pointers, NestedClaudeMD finds the CLAUDE.md files above them,
// and between them they close Qompack.md gaps G4.1 and G4.2.
func New(opts ...Option) Scanner {
	s := &scanner{log: logging.Nop()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// PathScoped returns every rule under root whose `paths:` frontmatter glob matches any path in
// pointers, sorted by Path.
//
// Per-file failures are absorbed rather than reported: a rule file that was deleted mid-walk, or
// that this process cannot read, or that is too large, is skipped and logged at Debug. The caller
// is a hook with nothing useful to do about any of those, and failing it would cost the user the
// whole rehydration to save one rule. Only a failure to walk a discovery root itself — the one
// condition that means the result is not merely incomplete but unknown — returns a non-nil error.
func (s *scanner) PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := s.pointerKeys(root, pointers)
	if len(keys) == 0 {
		return nil, nil
	}

	var out []Rule
	seen := make(map[string]struct{})
	for _, g := range scanGlobs {
		files, err := s.walkDiscoveryRoot(ctx, root, g)
		if err != nil {
			return nil, err
		}
		for _, c := range files {
			if _, dup := seen[c.rel]; dup {
				continue
			}
			seen[c.rel] = struct{}{}
			if r, ok := s.scopedRule(c, keys); ok {
				out = append(out, r)
			}
		}
	}
	slices.SortFunc(out, func(a, b Rule) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// NestedClaudeMD returns every CLAUDE.md above a pointer — in its own directory or in any
// ancestor of it — sorted by Path, which puts an outer CLAUDE.md before the inner ones below it:
// the order Claude Code applies nested instructions in.
//
// The walk deliberately stops one level short of the project root. Claude Code re-injects the
// project-root CLAUDE.md from disk itself after a compaction (Qompack.md §2.7), so restoring it
// again would spend rehydration budget on context that is already back.
func (s *scanner) NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := s.pointerKeys(root, pointers)
	if len(keys) == 0 {
		return nil, nil
	}

	var out []Rule
	seen := make(map[string]struct{})
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := path.Dir(k)
		for depth := 0; depth < maxAncestorDepth && d != "." && d != "/" && d != ""; depth++ {
			c := path.Join(d, claudeMDName)
			if _, dup := seen[c]; dup {
				// Every ancestor of an already-visited directory was visited on the same pass, so
				// there is nothing left above this point. One CLAUDE.md covering forty pointers
				// costs one stat and one read, not forty.
				break
			}
			seen[c] = struct{}{}
			if r, ok := s.nestedRule(root, c); ok {
				out = append(out, r)
			}
			d = path.Dir(d)
		}
	}
	// Outermost-first: the general rule before the specific one that refines it, which is the
	// order Claude Code itself applies nested instructions in.
	//
	// This sorts by DEPTH and only then by path, rather than by path alone. A path-only sort gets
	// the right answer on Windows and macOS purely by accident: paths.Key case-folds there, so
	// every segment is lowercase and a shorter ancestor path sorts before the longer descendant
	// that extends it. On Linux there is no folding, and an uppercase-initial directory inverts
	// it — "src/API/CLAUDE.md" < "src/CLAUDE.md" byte-wise, because 'A' (0x41) < 'C' (0x43) —
	// which would emit the specific rule before the general one it refines, on exactly one
	// platform, and fork the goldens between platforms.
	slices.SortFunc(out, func(a, b Rule) int {
		if d := strings.Count(a.Path, "/") - strings.Count(b.Path, "/"); d != 0 {
			return d
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// pointerKeys normalizes pointers to deduplicated paths.Key form, preserving first-seen order. A
// pointer that will not normalize — one that escapes the project root, say — is dropped rather
// than fatal: the rest of the set is still worth answering for.
func (s *scanner) pointerKeys(root string, pointers []string) []string {
	keys := make([]string, 0, len(pointers))
	seen := make(map[string]struct{}, len(pointers))
	for _, p := range pointers {
		rel, ok := normPointer(root, p)
		if !ok {
			s.log.Debug("rules: skipping pointer that will not normalize", "pointer", p)
			continue
		}
		k := paths.Key(rel)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	return keys
}

// normPointer resolves one pointer to project-relative, forward-slash form.
//
// It exists because paths.Norm is far too expensive to call per pointer. Norm resolves symlinks
// with filepath.EvalSymlinks TWICE — once on projectRoot, which is identical on every call in a
// scan and recomputed anyway, and once on the target — and on Windows each of those costs
// milliseconds against an os.Lstat's tens of microseconds. Measured on this repository:
// EvalSymlinks(projectRoot) 4.6ms, EvalSymlinks(target) 7.7ms, Lstat(target) 0.042ms. At forty
// pointers that is ~0.5s per scan, and PathScoped plus NestedClaudeMD pay it twice — against
// L5-RULES' 50ms budget. BenchmarkPathScoped measured 479-714ms/op before this fast path.
//
// The fast path is pure string work and takes no filesystem call at all. It is safe to take
// precisely when the input is ALREADY the canonical form Norm would produce: a relative,
// forward-slash, cleaned path that does not escape the project. That is the overwhelmingly common
// case here, because the pointers come from Checkpoint.Pointers.Files[].Path, which the
// checkpointer already stores in paths.Key form. Anything else — an absolute path, a "..", a
// backslash, an uncleaned segment — falls through to Norm and pays full price, so the escape
// rejection and symlink canonicalization Norm exists for are unchanged for every input that
// actually needs them.
//
// What the fast path gives up is resolving a symlink that lives INSIDE the project, so a pointer
// naming a symlinked path keys by its link name rather than its target. Neither caller relies on
// that resolution: PathScoped matches globs against the key and reads only files WalkDir turned
// up, and WalkDir does not follow directory symlinks; NestedClaudeMD reaches its candidates by
// joining strings, and lstats each one so a symlinked CLAUDE.md is refused rather than read.
// A pointer that escapes the project is still rejected, by the ".." test below and by Norm on
// every input that does not take the fast path.
func normPointer(root, p string) (string, bool) {
	slashed := filepath.ToSlash(p)
	if !filepath.IsAbs(p) && !strings.ContainsRune(p, '\\') &&
		slashed == path.Clean(slashed) &&
		slashed != ".." && !strings.HasPrefix(slashed, "../") &&
		slashed != "." && slashed != "" {
		return slashed, true
	}
	rel, err := paths.Norm(root, p)
	if err != nil {
		return "", false
	}
	return rel, true
}

// candidate is one *.md file the discovery walk turned up, kept with both the absolute path the
// read needs and the project-relative path the Rule reports.
type candidate struct {
	abs string
	rel string
}

// walkDiscoveryRoot collects the *.md files under one discovery root. A root that does not exist
// is not an error — most projects have no .claude/rules — but a root that exists and cannot be
// walked is, because then the result is unknown rather than merely empty.
func (s *scanner) walkDiscoveryRoot(ctx context.Context, root, g string) ([]candidate, error) {
	base := filepath.Join(root, filepath.FromSlash(g))
	recursive := g == rulesDir

	var out []candidate
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if walkErr != nil {
			if p == base {
				return walkErr
			}
			s.log.Debug("rules: skipping unreadable directory entry", "path", p, "err", walkErr)
			return nil
		}
		if d.IsDir() {
			if !recursive && p != base {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks are skipped outright rather than followed: a link out of the project would let
		// a file the project does not own decide what is in the project's context.
		if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(d.Name()), mdExt) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			s.log.Debug("rules: skipping rule file that will not stat", "path", p, "err", ierr)
			return nil
		}
		if info.Size() > maxRuleBytes {
			s.log.Debug("rules: skipping oversize rule file", "path", p, "bytes", info.Size(), "max", maxRuleBytes)
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			s.log.Debug("rules: skipping rule file outside the project root", "path", p, "err", rerr)
			return nil
		}
		out = append(out, candidate{abs: p, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("rules: walk discovery root %s: %w", g, err)
	}
	return out, nil
}

// scopedRule reads and parses one candidate, and reports whether it is a scoped rule matching
// keys. A file with no frontmatter, or with frontmatter carrying no globs, is not: Claude Code
// re-injects unscoped rules itself (Qompack.md §2.7), so returning one would restore context that
// is already back.
func (s *scanner) scopedRule(c candidate, keys []string) (Rule, bool) {
	b, err := os.ReadFile(paths.Long(c.abs))
	if err != nil {
		s.log.Debug("rules: skipping unreadable rule file", "path", c.rel, "err", err)
		return Rule{}, false
	}
	front, off := Parse(b)
	if !front.Present || len(front.Paths) == 0 {
		return Rule{}, false
	}
	if !matchesAny(front.Paths, keys) {
		return Rule{}, false
	}
	body := normalizeBody(b[off:])
	return Rule{
		Path:   c.rel,
		Globs:  front.Paths,
		Body:   body,
		Tokens: baselineTokens(body),
	}, true
}

// nestedRule reads the CLAUDE.md at project-relative path c, if there is one. A directory with no
// CLAUDE.md is the ordinary case, not a failure, and it does not stop the walk above it.
func (s *scanner) nestedRule(root, c string) (Rule, bool) {
	full := paths.Long(filepath.Join(root, filepath.FromSlash(c)))
	// Lstat, not Stat: a symlink must be refused rather than followed. PathScoped already skips
	// symlinks (WalkDir reports them via d.Type()), and this is the matching guard on the walk-up
	// path, which reaches its candidates by joining strings rather than by walking. It is also
	// what lets normPointer skip paths.Norm's EvalSymlinks: the canonicalization Norm would have
	// done is not load-bearing here if a symlinked candidate is never read in the first place.
	info, err := os.Lstat(full)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Debug("rules: skipping CLAUDE.md that will not stat", "path", c, "err", err)
		}
		return Rule{}, false
	}
	if !info.Mode().IsRegular() {
		return Rule{}, false
	}
	if info.Size() > maxRuleBytes {
		s.log.Debug("rules: skipping oversize CLAUDE.md", "path", c, "bytes", info.Size(), "max", maxRuleBytes)
		return Rule{}, false
	}
	b, err := os.ReadFile(full)
	if err != nil {
		s.log.Debug("rules: skipping unreadable CLAUDE.md", "path", c, "err", err)
		return Rule{}, false
	}
	// Frontmatter is retained rather than stripped: a nested CLAUDE.md is scoped by its directory,
	// so whatever its header says is part of the instruction, not routing metadata consumed here.
	body := normalizeBody(b)
	return Rule{Path: c, Body: body, Tokens: baselineTokens(body), Nested: true}, true
}

// matchesAny reports whether any glob matches any pointer key. Globs are folded through
// paths.Key so a rule written as src/API/** still matches on the platforms where that is the same
// directory as src/api.
func matchesAny(globs, keys []string) bool {
	for _, g := range globs {
		folded := paths.Key(g)
		for _, k := range keys {
			if Match(folded, k) {
				return true
			}
		}
	}
	return false
}

// normalizeBody renders a rule body: CRLF collapsed to LF, trailing whitespace removed. The CRLF
// half is structural rather than cosmetic (00-ARCHITECTURE.md §4) — a Windows-authored rule and
// its Unix twin have to hash and price identically, or the same rule occupies two budget lines.
func normalizeBody(b []byte) string {
	return strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), " \t\r\n")
}

// baselineTokens is Rule.Tokens' advisory estimate: four bytes per token, rounded up. It is
// deliberately crude. internal/rules is foundation-only under the §3.2 layer table and may not
// import internal/tokens, so this exists to give a caller a non-zero size to reason about before
// rehydrate.Build overwrites it with the real estimator's answer. Nothing downstream should treat
// it as a budget input in its own right.
func baselineTokens(body string) core.Tokens {
	return core.Tokens((len(body) + bytesPerToken - 1) / bytesPerToken)
}
