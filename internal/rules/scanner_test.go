package rules_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
	"github.com/stretchr/testify/require"
)

// newScanner returns the Scanner under test, wired to a no-op logger. Going through WithLogger
// rather than the bare New() keeps the Option seam exercised: rulestest calls New() with no
// arguments and would not otherwise cover it.
func newScanner(t testing.TB) rules.Scanner {
	t.Helper()
	return rules.New(rules.WithLogger(logging.Nop()))
}

// fixtureRoot returns the absolute path of a committed fixture project under
// testdata/fixtures/rules, failing the test if it is missing rather than silently scanning
// nothing — an empty scan satisfies most of the assertions below by accident.
func fixtureRoot(t testing.TB, name string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "rules", name))
	require.NoError(t, err)
	info, err := os.Stat(root)
	require.NoError(t, err, "fixture project %s is missing", name)
	require.True(t, info.IsDir())
	return root
}

// rulePaths projects a result set down to its Path values, which is what almost every assertion
// here is actually about.
func rulePaths(rs []rules.Rule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}

// writeFile writes one file below root, creating its parents. Paths go through paths.Long so the
// long-path cases below can build a tree past Windows' legacy MAX_PATH.
func writeFile(t testing.TB, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(full)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(full), []byte(content), 0o600))
}

// ---------------------------------------------------------------------------------------------
// PathScoped
// ---------------------------------------------------------------------------------------------

// TestPathScoped_MatchesOnePointer is the headline case: one edited file pulls back exactly the
// rules scoped to it — the directory-scoped one and the language-wide one — and nothing else.
// This is the gap G4.1 behaviour, so the assertion is on the exact set, not on containment.
func TestPathScoped_MatchesOnePointer(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-a"), []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{
		".claude/rules/api-conventions.md",
		".claude/rules/nested/deep-rule.md",
	}, rulePaths(got))
	for _, r := range got {
		require.False(t, r.Nested, "a path-scoped rule is not a nested CLAUDE.md")
		require.NotEmpty(t, r.Globs)
		require.NotEmpty(t, r.Body)
	}
}

// TestPathScoped_MatchesInlineRuleDirectlyUnderDotClaude pins the second discovery root: a rule
// file sitting directly in .claude/ is a rule too, not only the ones under .claude/rules/.
func TestPathScoped_MatchesInlineRuleDirectlyUnderDotClaude(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-a"), []string{"src/webhooks/retry.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{
		".claude/inline-rule.md",
		".claude/rules/nested/deep-rule.md",
	}, rulePaths(got))
}

// TestPathScoped_SkipsUnscoped pins that a rule file with no `paths:` key is never returned: the
// host re-injects unscoped rules itself after compaction (Qompack.md §2.7), so restoring one
// would spend rehydration budget on context that is already back.
func TestPathScoped_SkipsUnscoped(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-a"),
		[]string{"src/api/routes.ts", "src/db/pool.ts", "src/webhooks/retry.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{
		".claude/inline-rule.md",
		".claude/rules/api-conventions.md",
		".claude/rules/db-conventions.md",
		".claude/rules/nested/deep-rule.md",
	}, rulePaths(got))
	require.NotContains(t, rulePaths(got), ".claude/rules/unscoped.md")
}

// TestPathScoped_SkipsEmptyPaths pins the two degenerate frontmatter shapes proj-edge carries: an
// explicitly empty `paths: []`, and a block that is opened and never closed. Both are unscoped,
// and neither is an error — a malformed rule file must not fail the hook.
func TestPathScoped_SkipsEmptyPaths(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-edge"), []string{"crlf/win.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{".claude/rules/crlf-rule.md"}, rulePaths(got))
}

// oversizeRuleBytes is comfortably past the scanner's 256 KiB ceiling. The file is generated here
// rather than committed: 300 KiB of filler in testdata would read like a corpus.
const oversizeRuleBytes = 300 * 1024

// TestPathScoped_SkipsOversizeFile pins the byte ceiling. A file that large is not a rule file,
// and reading it on the hot path is exactly the cost the ceiling exists to refuse.
func TestPathScoped_SkipsOversizeFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	front := "---\npaths: [\"**/*.ts\"]\n---\n"
	writeFile(t, root, ".claude/rules/small.md", front+"small enough\n")
	writeFile(t, root, ".claude/rules/huge.md", front+strings.Repeat("x", oversizeRuleBytes))

	got, err := newScanner(t).PathScoped(context.Background(), root, []string{"a.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{".claude/rules/small.md"}, rulePaths(got))
}

// TestPathScoped_NoPointers pins the empty-input contract: nothing to match against is not an
// error, and it must not become a walk either.
func TestPathScoped_NoPointers(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")

	got, err := sc.PathScoped(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = sc.PathScoped(context.Background(), root, []string{})
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestPathScoped_MissingClaudeDirIsNotAnError pins the common case, not an edge case: most
// projects have no .claude/rules tree at all, and filepath.WalkDir on a root that does not exist
// reports fs.ErrNotExist. Propagating that would make PathScoped return non-nil for every
// ordinary project — which rehydrate.Build would turn into a DropEntry and a Warn on every single
// rehydration, reporting a non-problem. A missing discovery root means "no rules here".
func TestPathScoped_MissingClaudeDirIsNotAnError(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)

	got, err := sc.PathScoped(context.Background(), t.TempDir(), []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Empty(t, got)

	// A project root that does not exist at all is the same answer, not a louder one.
	got, err = sc.PathScoped(context.Background(), filepath.Join(t.TempDir(), "no-such-project"),
		[]string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestNestedClaudeMD_MissingRootIsNotAnError pins the same contract for the ancestor walk: a
// directory with no CLAUDE.md is the ordinary case, and it neither stops the walk nor fails it.
func TestNestedClaudeMD_MissingRootIsNotAnError(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)

	got, err := sc.NestedClaudeMD(context.Background(), t.TempDir(), []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = sc.NestedClaudeMD(context.Background(), filepath.Join(t.TempDir(), "no-such-project"),
		[]string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Empty(t, got)
}

// determinismRuns is how many identical scans TestPathScoped_Deterministic compares. Directory
// iteration order is not guaranteed by the OS, so a scanner that forgot to sort would pass a
// single-run test and then fail a golden test downstream at random.
const determinismRuns = 10

// TestPathScoped_Deterministic pins byte-identical output across repeated scans of one tree.
func TestPathScoped_Deterministic(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")
	pointers := []string{"src/api/routes.ts", "src/db/pool.ts", "src/webhooks/retry.ts"}

	first, err := sc.PathScoped(context.Background(), root, pointers)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	for i := 1; i < determinismRuns; i++ {
		got, err := sc.PathScoped(context.Background(), root, pointers)
		require.NoError(t, err)
		if diff := cmp.Diff(first, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("run %d differs from run 0 (-first +got):\n%s", i, diff)
		}
	}
}

// TestPathScoped_CaseInsensitivePointer pins that on a case-folding filesystem a pointer's
// spelling does not change what it matches. It is skipped elsewhere: on Linux, SRC/API and
// src/api are genuinely different directories and folding them together would be the bug.
func TestPathScoped_CaseInsensitivePointer(t *testing.T) {
	t.Parallel()
	if !paths.DefaultFold() {
		// The "platform: " prefix is mandatory: devtool lint's stubskips sub-check hard-fails on
		// any t.Skip reason outside its allow-list, and a platform-gated case needs this form.
		t.Skipf("platform: case-folding pointers only apply on windows/darwin, not %s", runtime.GOOS)
	}
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")

	lower, err := sc.PathScoped(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	upper, err := sc.PathScoped(context.Background(), root, []string{"SRC/API/ROUTES.TS"})
	require.NoError(t, err)

	require.NotEmpty(t, lower)
	if diff := cmp.Diff(lower, upper, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("pointer case changed the result (-lower +upper):\n%s", diff)
	}
}

// TestPathScoped_BodyIsCRLFNormalized pins CRLF → LF on the body. It asserts against a
// programmatically written tree as well as the committed proj-edge fixture, because
// .gitattributes governs what CRLF actually survives a checkout and the committed half of the
// assertion would pass vacuously if it were ever normalized away.
func TestPathScoped_BodyIsCRLFNormalized(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, ".claude/rules/win.md", "---\r\npaths: [\"**/*.ts\"]\r\n---\r\nfirst\r\nsecond\r\n\r\n")

	got, err := newScanner(t).PathScoped(context.Background(), root, []string{"a.ts"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "first\nsecond", got[0].Body)

	edge, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-edge"), []string{"crlf/win.ts"})
	require.NoError(t, err)
	require.Len(t, edge, 1)
	require.NotContains(t, edge[0].Body, "\r", "a carriage return survived into Body")
	require.Equal(t, []string{"crlf/**"}, edge[0].Globs)
}

// TestPathScoped_TokensBaselineIsSet pins the advisory baseline estimate. internal/rules may not
// import internal/tokens — the §3.2 layer table makes rules foundation-only — so Tokens carries a
// four-bytes-per-token approximation that rehydrate.Build overwrites with the real estimator. The
// value still has to be set: a zero here would make a rule look free to any budget that read it
// before Build ran.
func TestPathScoped_TokensBaselineIsSet(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).PathScoped(context.Background(), fixtureRoot(t, "proj-a"),
		[]string{"src/api/routes.ts", "src/db/pool.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, got)
	for _, r := range got {
		require.Equal(t, core.Tokens((len(r.Body)+3)/4), r.Tokens, "rule %s", r.Path)
		require.Positive(t, int(r.Tokens))
	}
}

// ---------------------------------------------------------------------------------------------
// NestedClaudeMD
// ---------------------------------------------------------------------------------------------

// TestNestedClaudeMD_ContainingDir is the base case of gap G4.2: the CLAUDE.md in the pointer's
// own directory, and only that one.
func TestNestedClaudeMD_ContainingDir(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).NestedClaudeMD(context.Background(), fixtureRoot(t, "proj-a"), []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{"src/api/CLAUDE.md"}, rulePaths(got))
	require.Contains(t, got[0].Body, "zod")
}

// TestNestedClaudeMD_ExcludesProjectRoot pins that the project-root CLAUDE.md is never returned:
// Claude Code re-injects it from disk itself after compaction (Qompack.md §2.7), so the ancestor
// walk stops one level short of it.
func TestNestedClaudeMD_ExcludesProjectRoot(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")
	for _, pointer := range []string{"CLAUDE.md", "README.md", "package.json"} {
		got, err := sc.NestedClaudeMD(context.Background(), root, []string{pointer})
		require.NoError(t, err)
		require.Empty(t, got, "pointer %q sits at the project root and has no nested ancestor", pointer)
	}
}

// copyTree copies a committed fixture project into a temp directory so a test can mutate it. The
// committed trees are shared by every test in this package and by SP-11's sibling subagent, so
// nothing here writes into them.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.CopyFS(dst, os.DirFS(src)))
	return dst
}

// TestNestedClaudeMD_WalksAncestors pins the amended §5.15 reading ("containing, or ancestor
// to"): a CLAUDE.md one directory above the pointer is in scope too, and the two come back
// outermost-first, which is the order Claude Code applies nested instructions in.
func TestNestedClaudeMD_WalksAncestors(t *testing.T) {
	t.Parallel()
	root := copyTree(t, fixtureRoot(t, "proj-a"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "CLAUDE.md"), []byte("src-wide conventions.\n"), 0o600))

	got, err := newScanner(t).NestedClaudeMD(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{"src/CLAUDE.md", "src/api/CLAUDE.md"}, rulePaths(got))
}

// TestNestedClaudeMD_AncestorTwoLevelsUp mirrors the rulestest nested_claude_md_discovery fixture
// exactly: CLAUDE.md at src/pkg, pointer at src/pkg/deep/thing.go. It is the inherited
// conformance case restated here, so a regression names itself in this package rather than only
// in the suite.
func TestNestedClaudeMD_AncestorTwoLevelsUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "src/pkg/CLAUDE.md", "Package-local conventions.\n")
	writeFile(t, root, "src/pkg/deep/thing.go", "package deep\n")

	got, err := newScanner(t).NestedClaudeMD(context.Background(), root, []string{"src/pkg/deep/thing.go"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "src/pkg/CLAUDE.md", got[0].Path)
	require.True(t, got[0].Nested)
	require.Contains(t, got[0].Body, "Package-local conventions.")
}

// TestNestedClaudeMD_AncestorWalkStopsBeforeRoot pins the stop condition directly: proj-a has a
// project-root CLAUDE.md, and no depth of pointer may drag it in.
func TestNestedClaudeMD_AncestorWalkStopsBeforeRoot(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).NestedClaudeMD(context.Background(), fixtureRoot(t, "proj-a"),
		[]string{"src/api/routes.ts", "src/db/pool.ts", "src/webhooks/retry.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{"src/api/CLAUDE.md", "src/db/CLAUDE.md", "src/webhooks/CLAUDE.md"}, rulePaths(got))
	require.NotContains(t, rulePaths(got), "CLAUDE.md")
}

// dedupPointerCount is the pointer fan-in the dedup case models: forty files in one package is
// the ordinary post-compaction pointer set, not the exotic one.
const dedupPointerCount = 40

// TestNestedClaudeMD_Dedups pins that one CLAUDE.md covering many pointers is stat'd, read and
// emitted once.
func TestNestedClaudeMD_Dedups(t *testing.T) {
	t.Parallel()
	pointers := []string{"src/api/routes.ts", "src/api/routes.ts"}
	for i := range dedupPointerCount {
		pointers = append(pointers, fmt.Sprintf("src/api/handler%02d.ts", i))
	}

	got, err := newScanner(t).NestedClaudeMD(context.Background(), fixtureRoot(t, "proj-a"), pointers)
	require.NoError(t, err)
	require.Equal(t, []string{"src/api/CLAUDE.md"}, rulePaths(got))
}

// TestNestedClaudeMD_UnicodeAndSpaces pins the two directory-name shapes that break naive path
// handling: a space, and a non-ASCII rune.
func TestNestedClaudeMD_UnicodeAndSpaces(t *testing.T) {
	t.Parallel()
	got, err := newScanner(t).NestedClaudeMD(context.Background(), fixtureRoot(t, "proj-edge"),
		[]string{"dir with spaces/a.ts", "únïcodé/b.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{"dir with spaces/CLAUDE.md", "únïcodé/CLAUDE.md"}, rulePaths(got))
}

// longPathSegment, longPathDepth and maxPathLegacy build an absolute path past Windows' legacy
// MAX_PATH of 260 characters, which is the limit paths.Long exists to get past. The committed
// proj-edge deep/ fixture covers the shape; only a generated tree can guarantee the length, since
// how long the committed one measures depends on where the repository was cloned.
const (
	longPathSegment = "long-path-segment-x"
	longPathDepth   = 12
	maxPathLegacy   = 260
)

// TestNestedClaudeMD_LongPath pins discovery below Windows' legacy MAX_PATH, and against the
// committed deep fixture.
func TestNestedClaudeMD_LongPath(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)

	root := t.TempDir()
	relDir := strings.Repeat(longPathSegment+"/", longPathDepth)
	writeFile(t, root, relDir+"CLAUDE.md", "Rules for a directory past MAX_PATH.\n")
	writeFile(t, root, relDir+"thing.ts", "export const x = 1;\n")
	require.Greater(t, len(filepath.Join(root, filepath.FromSlash(relDir+"thing.ts"))), maxPathLegacy,
		"the generated tree must actually exceed the legacy MAX_PATH")

	got, err := sc.NestedClaudeMD(context.Background(), root, []string{relDir + "thing.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{relDir + "CLAUDE.md"}, rulePaths(got))

	deep := "deep/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s/t/"
	got, err = sc.NestedClaudeMD(context.Background(), fixtureRoot(t, "proj-edge"), []string{deep + "file.ts"})
	require.NoError(t, err)
	require.Equal(t, []string{deep + "CLAUDE.md"}, rulePaths(got))
}

// TestNestedClaudeMD_NestedFlag pins the flag that tells the two halves of the seam apart
// downstream: rehydrate.Build orders and prices nested CLAUDE.md content differently from a
// path-scoped rule, and Nested is the only thing distinguishing them once both are Rules.
func TestNestedClaudeMD_NestedFlag(t *testing.T) {
	t.Parallel()
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")

	nested, err := sc.NestedClaudeMD(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, nested)
	for _, r := range nested {
		require.True(t, r.Nested)
		require.Empty(t, r.Globs, "a nested CLAUDE.md is scoped by directory, not by glob")
		require.Equal(t, core.Tokens((len(r.Body)+3)/4), r.Tokens)
	}

	scoped, err := sc.PathScoped(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, scoped)
	for _, r := range scoped {
		require.False(t, r.Nested)
	}
}

// ---------------------------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------------------------

// The synthetic tree BenchmarkPathScoped scans: a project big enough that a per-file mistake
// shows up, with the rule count and pointer count a post-compaction hook actually sees.
const (
	benchDirs         = 40
	benchFilesPerDir  = 50
	benchRuleFiles    = 24
	benchPointerCount = 40
	benchRuleBodyReps = 20
)

// BenchmarkPathScoped measures the whole L5 path-scoped scan: the discovery-root walk, the
// frontmatter parse of every rule file, and the glob cross-product against the pointer set. It is
// a number budget B-A cares about, because PathScoped runs on the rehydration path — a compaction
// has just happened and the user is waiting on it.
func BenchmarkPathScoped(b *testing.B) {
	root := b.TempDir()
	for d := range benchDirs {
		for f := range benchFilesPerDir {
			writeFile(b, root, fmt.Sprintf("src/pkg%02d/file%02d.ts", d, f), "export const x = 1;\n")
		}
	}
	body := strings.Repeat("Conventions for this part of the tree.\n", benchRuleBodyReps)
	for i := range benchRuleFiles {
		writeFile(b, root, fmt.Sprintf(".claude/rules/rule%02d.md", i),
			fmt.Sprintf("---\npaths:\n  - \"src/pkg%02d/**\"\n  - \"**/*.md\"\n---\n%s", i, body))
	}
	pointers := make([]string, 0, benchPointerCount)
	for i := range benchPointerCount {
		pointers = append(pointers, fmt.Sprintf("src/pkg%02d/file%02d.ts", i%benchDirs, i%benchFilesPerDir))
	}

	sc := newScanner(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := sc.PathScoped(ctx, root, pointers)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("benchmark scanned nothing")
		}
	}
}

// TestNestedClaudeMD_OutermostFirstSurvivesCase pins the ordering NestedClaudeMD promises —
// outermost-first, the general rule before the specific one that refines it — against the one
// input that used to break it.
//
// A sort by Path alone gets this right on Windows and macOS by accident: paths.Key case-folds
// there, so every segment is lowercase and the shorter ancestor path sorts before the longer
// descendant that extends it. On Linux there is no folding, and an uppercase-initial directory
// inverts the comparison — "src/API/CLAUDE.md" < "src/CLAUDE.md" byte-wise, because 'A' (0x41)
// precedes 'C' (0x43) — which emitted the specific rule BEFORE the general one, on exactly one
// platform, and would have forked the rehydration goldens between platforms.
//
// The fixture is deliberately uppercase-initial. On a folding platform this passes trivially; on
// Linux it is the real assertion, and it fails against a path-only sort.
func TestNestedClaudeMD_OutermostFirstSurvivesCase(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "src", "API", "v2")
	require.NoError(t, os.MkdirAll(deep, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "CLAUDE.md"), []byte("outer\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "API", "CLAUDE.md"), []byte("middle\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "CLAUDE.md"), []byte("inner\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "handler.ts"), []byte("export {}\n"), 0o600))

	got, err := rules.New().NestedClaudeMD(context.Background(), root, []string{"src/API/v2/handler.ts"})
	require.NoError(t, err)
	require.Len(t, got, 3, "all three ancestors are in force for the pointer")

	bodies := make([]string, len(got))
	for i, r := range got {
		bodies[i] = strings.TrimSpace(r.Body)
	}
	require.Equal(t, []string{"outer", "middle", "inner"}, bodies,
		"nested CLAUDE.md files must be emitted outermost-first, independently of segment case")
}
