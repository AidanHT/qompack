// Package skillstest is the conformance suite for skills.Indexer (00-ARCHITECTURE.md §5.22):
// every implementation SP-11 ships must pass RunIndexerSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-11 inherits (Rule W-1) — only the guarded /behaviour
// block is skipped until a real Indexer lands.
package skillstest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/skills"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeBudgetTokens is an arbitrary, generous budget used only by the shape-block probe call.
const probeBudgetTokens = 500

// RunIndexerSuite is the conformance suite for skills.Indexer. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Indexer on
// every call.
func RunIndexerSuite(t *testing.T, name string, factory func(t *testing.T) skills.Indexer) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		idx := factory(t)
		require.NotNil(t, idx)

		ctx := context.Background()
		_, _, err := idx.Index(ctx, t.TempDir(), core.Tokens(probeBudgetTokens))
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("index_never_contains_skill_bodies", func(t *testing.T) {
			idx := factory(t)
			ctx := context.Background()
			root := writeSkillFixtures(t, 1)

			entries, _, err := idx.Index(ctx, root, core.Tokens(probeBudgetTokens))
			require.NoError(t, err)
			require.NotEmpty(t, entries)
			for _, e := range entries {
				require.NotContains(t, e.Name, bodyMarker)
				require.NotContains(t, e.Description, bodyMarker,
					"Index must return names and one-line descriptions only, never skill bodies")
			}
		})

		t.Run("respects_the_budget", func(t *testing.T) {
			idx := factory(t)
			ctx := context.Background()
			root := writeSkillFixtures(t, manySkillCount)

			const tightBudget = core.Tokens(smallBudgetTokens)
			entries, total, err := idx.Index(ctx, root, tightBudget)
			require.NoError(t, err)
			require.LessOrEqual(t, total, tightBudget,
				"Index must never return more tokens than the passed budget")
			require.LessOrEqual(t, len(entries), manySkillCount)
		})

		t.Run("deterministic_order", func(t *testing.T) {
			idx := factory(t)
			ctx := context.Background()
			root := writeSkillFixtures(t, manySkillCount)

			first, _, err := idx.Index(ctx, root, core.Tokens(probeBudgetTokens))
			require.NoError(t, err)
			second, _, err := idx.Index(ctx, root, core.Tokens(probeBudgetTokens))
			require.NoError(t, err)
			require.Equal(t, first, second, "Index must return the same order on every call for an unchanged tree")
		})
	})
}

// manySkillCount and smallBudgetTokens size the "respects_the_budget" fixture: enough skills that
// their combined names and descriptions cannot possibly fit in a tight budget, so Index is forced
// to actually enforce the cap rather than trivially satisfy it.
const (
	manySkillCount    = 12
	smallBudgetTokens = 16
)

// bodyMarker appears only inside a skill's body, never in its name or description; the
// index_never_contains_skill_bodies case asserts no Entry field contains it.
const bodyMarker = "BODY-ONLY-CONTENT-should-never-reach-the-index"

// skillFrontmatter renders one SKILL.md file's content for skill index i.
func skillFrontmatter(i int) string {
	return fmt.Sprintf(
		"---\nname: skill-%d\ndescription: One-line description of skill %d.\n---\n%s\nStep-by-step instructions and examples would go here, at length.\n",
		i, i, bodyMarker)
}

// writeSkillFixtures builds a temp project containing n skill files under .claude/skills/ (the
// Claude Code plugin convention: one directory per skill, one SKILL.md each) and returns the
// project root.
func writeSkillFixtures(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := range n {
		dir := filepath.Join(root, ".claude", "skills", fmt.Sprintf("skill-%d", i))
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillFrontmatter(i)), 0o600))
	}
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

// isStub reports whether factory currently produces a stub Indexer, using Index as the probe
// (plans/OWNERS.tsv: skills's probe method is Index).
func isStub(t *testing.T, factory func(t *testing.T) skills.Indexer) bool {
	t.Helper()
	_, _, err := factory(t).Index(context.Background(), t.TempDir(), core.Tokens(probeBudgetTokens))
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Indexer, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) skills.Indexer) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
