// Package rulestest is the conformance suite for rules.Scanner (00-ARCHITECTURE.md §5.22): every
// implementation SP-11 ships must pass RunScannerSuite. SP-01 ships the suite itself, including
// the behaviour assertions SP-11 inherits (Rule W-1) — only the guarded /behaviour block is
// skipped until a real Scanner lands.
package rulestest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/rules"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunScannerSuite is the conformance suite for rules.Scanner. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Scanner on
// every call.
func RunScannerSuite(t *testing.T, name string, factory func(t *testing.T) rules.Scanner) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		sc := factory(t)
		require.NotNil(t, sc)

		ctx := context.Background()
		root := t.TempDir()

		_, err := sc.PathScoped(ctx, root, []string{"src/main.go"})
		requireKnownError(t, err)

		_, err = sc.NestedClaudeMD(ctx, root, []string{"src/main.go"})
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("path_scoped_glob_matching", func(t *testing.T) {
			sc := factory(t)
			ctx := context.Background()
			root := writePathScopedFixture(t)

			matched, err := sc.PathScoped(ctx, root, []string{"src/api/handler.go"})
			require.NoError(t, err)
			require.Len(t, matched, 1)
			require.Equal(t, apiRuleGlob, matched[0].Globs[0])
			require.Contains(t, matched[0].Body, apiRuleBodyMarker)
			require.False(t, matched[0].Nested)

			unmatched, err := sc.PathScoped(ctx, root, []string{"src/other/thing.go"})
			require.NoError(t, err)
			require.Empty(t, unmatched, "a pointer outside every rule's glob must match nothing")
		})

		t.Run("nested_claude_md_discovery", func(t *testing.T) {
			sc := factory(t)
			ctx := context.Background()
			root := writeNestedClaudeMDFixture(t)

			matched, err := sc.NestedClaudeMD(ctx, root, []string{nestedPointerRelPath})
			require.NoError(t, err)
			require.Len(t, matched, 1)
			require.True(t, matched[0].Nested)
			require.Contains(t, matched[0].Body, nestedClaudeMDMarker)

			unmatched, err := sc.NestedClaudeMD(ctx, root, []string{"unrelated/elsewhere.go"})
			require.NoError(t, err)
			require.Empty(t, unmatched, "a pointer outside the nested directory must match nothing")
		})
	})
}

// The path-scoped-rule fixture: a rule file with `paths:` frontmatter scoped to src/api/**.
const (
	apiRuleGlob        = "src/api/**"
	apiRuleBodyMarker  = "Follow REST conventions in this directory."
	apiRuleFrontmatter = "---\npaths:\n  - \"" + apiRuleGlob + "\"\n---\n" + apiRuleBodyMarker + "\n"
)

// writePathScopedFixture builds a temp project containing one `paths:`-scoped rule file and
// returns the project root.
func writePathScopedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "rules")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api-conventions.md"), []byte(apiRuleFrontmatter), 0o600))
	return root
}

// The nested-CLAUDE.md fixture: src/pkg/CLAUDE.md, with a pointer file two directories below it.
const (
	nestedPointerRelPath = "src/pkg/deep/thing.go"
	nestedClaudeMDMarker = "Package-local conventions."
)

// writeNestedClaudeMDFixture builds a temp project containing one nested CLAUDE.md and the
// pointer file it should be discovered for, and returns the project root.
func writeNestedClaudeMDFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pkgDir := filepath.Join(root, "src", "pkg")
	deepDir := filepath.Join(pkgDir, "deep")
	require.NoError(t, os.MkdirAll(deepDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "CLAUDE.md"), []byte(nestedClaudeMDMarker+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(deepDir, "thing.go"), []byte("package deep\n"), 0o600))
	return root
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Scanner, using PathScoped as the
// probe (plans/OWNERS.tsv: rules's probe method is PathScoped).
func isStub(t *testing.T, factory func(t *testing.T) rules.Scanner) bool {
	t.Helper()
	_, err := factory(t).PathScoped(context.Background(), t.TempDir(), nil)
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Scanner, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) rules.Scanner) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
