package rules_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The two end-to-end assertions the V4 and V6 verification documents run by name (V6 1.11.6 and
// 1.11.7). Every other test in this package takes one behaviour apart; these two answer the
// question the gap statements ask — after a compaction, does the agent get its operating rules
// back, and does it get them WHOLE?
//
// They are deliberately whole-scanner assertions over the committed fixture project rather than
// unit tests of a helper: G4.1 and G4.2 are about what reaches the model, and a scanner that
// parsed frontmatter perfectly while returning the wrong file set would satisfy every unit test
// here and close neither gap.

// TestRestored_PathScoped closes G4.1: every rule whose `paths:` globs match a pointer is re-read
// from disk, whole, and nothing else is.
func TestRestored_PathScoped(t *testing.T) {
	sc := newScanner(t)
	root := fixtureRoot(t, "proj-a")

	got, err := sc.PathScoped(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, got, "the fixture declares a rule scoped to src/api/**; an empty scan is a false pass")

	for _, r := range got {
		require.False(t, r.Nested, "PathScoped returns `paths:`-scoped rules, never nested CLAUDE.md files")
		require.NotEmpty(t, r.Globs, "a rule with no globs is unscoped and must not be restored")
		require.NotEmpty(t, r.Body, "a rule restored without its body restores nothing")
		require.Equal(t, core.Tokens((len(r.Body)+3)/4), r.Tokens,
			"%s: Tokens must be the baseline estimate of the body the caller will pay for", r.Path)

		// WHOLE, not excerpted: the body on disk and the body returned are the same text. A
		// partial instruction set is worse than an absent one (G4.3), so this is the property the
		// rehydrator's whole-rule-or-nothing admission depends on.
		require.NotContains(t, r.Body, "…", "%s: a restored rule body is never elided", r.Path)
	}

	// Only what MATCHES. The fixture also carries db-conventions.md, scoped to src/db/**, and an
	// unscoped rule; a scanner that returned the whole .claude/rules directory would pass every
	// assertion above and close nothing.
	paths := rulePaths(got)
	require.Contains(t, paths, ".claude/rules/api-conventions.md")
	require.NotContains(t, paths, ".claude/rules/db-conventions.md",
		"a rule scoped to src/db/** must not be restored for an src/api pointer")
	require.NotContains(t, paths, ".claude/rules/unscoped.md",
		"a rule with no `paths:` key is the host's business, not Qompack's")

	// An empty pointer set is an empty result and no error: nothing was in context, so nothing
	// needs restoring.
	none, err := sc.PathScoped(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestRestored_NestedClaudeMD closes G4.2: every nested CLAUDE.md the pointer set reaches is
// restored, and the project root's own is not — the host re-injects that one itself (§2.7).
func TestRestored_NestedClaudeMD(t *testing.T) {
	sc := newScanner(t)

	// A COPY of the fixture, with one file added: proj-a itself has no src/CLAUDE.md, and the
	// committed tree must keep it that way — TestNestedClaudeMD_ContainingDir asserts the exact
	// result set for that pointer. The ancestor half of the amended §8.6 reading needs a file above
	// the pointer's own directory, so this case supplies one rather than changing the fixture that
	// other cases pin.
	root := copyTree(t, fixtureRoot(t, "proj-a"))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "src", "CLAUDE.md"), []byte("src-wide conventions.\n"), 0o600))

	got, err := sc.NestedClaudeMD(context.Background(), root, []string{"src/api/routes.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, got, "the fixture puts a CLAUDE.md in src/api; an empty scan is a false pass")

	for _, r := range got {
		require.True(t, r.Nested, "every rule from NestedClaudeMD carries Nested == true")
		require.Empty(t, r.Globs, "a nested CLAUDE.md is scoped by directory, never by glob")
		require.NotEmpty(t, r.Body)
	}

	// Outermost first, so the more specific instructions read last and win — the same order the
	// host applies its own nested files in. The ancestor entry is what Qompack.md v1.4 widened
	// §8.6 to include; the project root's own file is excluded because the host re-injects it
	// (§2.7) and a duplicate would spend the budget twice.
	require.Equal(t, []string{"src/CLAUDE.md", "src/api/CLAUDE.md"}, rulePaths(got))

	none, err := sc.NestedClaudeMD(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, none)
}
