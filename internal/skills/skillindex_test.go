package skills_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/skills"
)

// The end-to-end assertions V4-SP11-05 and V6 1.11.8 run by name. Every other test in this package
// takes one behaviour apart; this file answers G4.4's question directly — does the compact index
// fit the budget the CONFIG names, and does it say honestly what it left out?

// TestSkillIndex_FitsTheConfiguredBudget is the whole of G4.4 in one assertion.
//
// The budget is read from runtime.rehydrate.skillIndexTokens rather than written here, and that is
// the point of the test rather than a nicety: 450 is a §11.6 forbidden literal precisely so that
// the number lives in exactly one place, and an indexer that happened to fit a hard-coded 450
// would silently stop honouring the key the moment an operator changed it.
func TestSkillIndex_FitsTheConfiguredBudget(t *testing.T) {
	root := t.TempDir()
	// Enough skills that the budget genuinely binds. A set that fits whole would satisfy every
	// assertion below without the truncation ever running.
	for i := 0; i < 40; i++ {
		writeDirSkill(t, root, fmt.Sprintf("skill-%02d", i), fmt.Sprintf(
			"---\nname: skill-%02d\ndescription: Do the %02dth thing, carefully, and report what it changed.\n---\nbody\n",
			i, i))
	}

	budget := core.Tokens(config.Defaults().Runtime.Rehydrate.SkillIndexTokens)
	require.Positive(t, int(budget), "the shipped default must name a real budget")

	ix := skills.New(skills.WithLogger(logging.Nop()))
	kept, cost, err := ix.Index(context.Background(), root, budget)
	require.NoError(t, err)
	require.NotEmpty(t, kept, "a budget this size fits many one-line entries")

	require.LessOrEqual(t, int(totalCost(kept)), int(budget),
		"the rendered index must fit runtime.rehydrate.skillIndexTokens")
	require.LessOrEqual(t, int(cost), int(budget),
		"the reported cost must be the cost of what was returned, not of what was scanned")

	// PREFIX truncation, never cheapest-first: the kept set is the first N of the full set in the
	// indexer's own order. Reordering under budget pressure would make the index non-deterministic
	// across replays, which is what the divergence measurement is compared with.
	all, _, err := ix.Index(context.Background(), root, 0)
	require.NoError(t, err)
	require.Greater(t, len(all), len(kept), "this fixture must overflow the budget")
	require.Equal(t, all[:len(kept)], kept,
		"the compact index is a PREFIX of the full index, not the cheapest subset")

	// One more entry would not have fitted — the fill is tight, not merely legal.
	next := totalCost(kept) + lineCost(all[len(kept)])
	require.Greater(t, int(next), int(budget),
		"the index stopped early: another entry still fitted the budget")
}

// TestSkillIndex_ZeroBudgetIsTheFullSet pins the two-call protocol item 6b depends on.
//
// Budget 0 is documented as "give me everything". It is what lets the caller tell "there are no
// other skills" from "there are others and they did not fit" — and the second of those is a drop
// report entry the agent can act on, where silence is not.
func TestSkillIndex_ZeroBudgetIsTheFullSet(t *testing.T) {
	root := fixtureRoot(t)
	ix := skills.New(skills.WithLogger(logging.Nop()))

	all, _, err := ix.Index(context.Background(), root, 0)
	require.NoError(t, err)
	require.NotEmpty(t, all, "the committed fixture carries three skills; an empty scan is a false pass")

	kept, _, err := ix.Index(context.Background(), root,
		core.Tokens(config.Defaults().Runtime.Rehydrate.SkillIndexTokens))
	require.NoError(t, err)
	require.Equal(t, all, kept,
		"the fixture's index is far inside the budget, so both calls return the same set")

	for _, e := range all {
		require.NotEmpty(t, e.Name, "an entry with no name cannot be invoked")
		require.NotEmpty(t, e.Description, "an entry with no description restores no awareness")
		require.NotEmpty(t, e.Source, "the source path is what expand() would re-read")
	}
}
