package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// UAT-05 read literally (owner decision D50, live/report-c4.md "Independent audit"): the
// correction must render ABOVE what it supersedes. Section 2 rendered the verbatim original first
// and the evolution under it, so the first requirement a reader met was the superseded one. It now
// renders the evolution, newest first, and then the original, still whole and labelled as the
// original request. Admission is unchanged: the original is tier 1, the newest restatement is
// admitted with it (D49), and every record is whole or named (D5).
func TestBuild_Section2RendersTheCorrectionAboveTheOriginal(t *testing.T) {
	cp := ckUAT05()
	res, err := Build(context.Background(), requestFor(t, cp, maxBudget()), uat05Deps(t, cp))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)

	body := sectionBody(res.Text, sectionHeading(ItemUserIntent))
	require.NotEmpty(t, body, "section 2 is present:\n%s", res.Text)
	correction := strings.Index(body, quoteLines(uat05Correction))
	original := strings.Index(body, originalRequestLabel+"\n"+quoteLines(uat05Original))
	require.GreaterOrEqual(t, correction, 0, "the correction is in section 2:\n%s", body)
	require.GreaterOrEqual(t, original, 0, "the original is whole and labelled as the original request:\n%s", body)
	require.Less(t, correction, original, "the correction renders above the original it supersedes:\n%s", body)
	require.True(t, strings.HasPrefix(body, "Evolution (most recent first):\n"+quoteLines(uat05Newest)),
		"the newest restatement is the first line of section 2:\n%s", body)
	require.True(t, strings.HasSuffix(body, quoteLines(uat05Original)), "the original closes section 2:\n%s", body)
}
