package rehydrate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuild_EmptySectionsRenderNothing pins that a section with no content renders no heading and no
// placeholder: with only a goal recorded, the payload carries section 5 as that one line, and none of
// sections 1, 3, 4, 6, 6a or 6b. The live eval (c55-c8) found 1, 3, 4, 6a and 6b empty on every
// block, and item 5's "blocked on: none" on every block.
func TestBuild_EmptySectionsRenderNothing(t *testing.T) {
	cp := ckEmpty()
	cp.CurrentWork.Goal = "g"
	d := fullDeps(t, cp)
	r := requestFor(t, cp, generousTestBudget)

	res, err := Build(bg(), r, d)
	require.NoError(t, err)

	require.Contains(t, res.Text, sectionHeading(ItemCurrentWork)+"\ngoal: g\n\n")
	require.NotContains(t, res.Text, "blocked on:")
	for _, k := range []ItemKind{
		ItemInvariants, ItemEliminations, ItemDecisions, ItemPointers,
		ItemRestoredInstructions, ItemSkillIndex,
	} {
		require.NotContains(t, res.Text, sectionHeading(k), "an empty %s section renders nothing", k)
	}
}
