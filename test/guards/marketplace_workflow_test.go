package guards

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// marketplace.yml (C7.5) runs once per full release, and it has no second chance
// that does not involve a person reading a failed run. Two properties decide whether it works:
//
//   - It must regenerate the marketplace with the RELEASE'S generator. The uploaded marketplace.json
//     was written by `devtool marketplace` at the tag; the job requires its own regeneration to be
//     byte-identical to it. Run with develop's devtool instead, any generator change that lands on
//     develop between tagging and publishing — even an entry description — fails that comparison
//     and no pull request is ever opened.
//   - A re-run must not fail on the state its first run left. After a partial failure the
//     marketplace/<tag> branch already exists, so a plain `git push` of a freshly created branch is
//     rejected, and `gh pr create` refuses a second pull request for the same head.
//
// The parse is workflowJobs', the same line-based reader the release and co-load guards use.

// marketplaceDevelopCheckoutRE is an actions/checkout `ref:` naming develop, in either YAML spelling.
var marketplaceDevelopCheckoutRE = regexp.MustCompile(`(?m)\bref:\s*['"]?develop['"]?\s*[,}\n]`)

// marketplaceLeasedPushRE is a push under an explicit lease on the full branch ref, with the expected
// value after the colon (quoted or not): the one --force-with-lease form git does not call
// experimental, and the one that means "must not exist yet" when the expected value is empty.
var marketplaceLeasedPushRE = regexp.MustCompile(`--force-with-lease="?refs/heads/[^:\s]+:`)

func TestMarketplaceWorkflowPinsWithTheReleasesGenerator(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "marketplace.yml")
	jobs := workflowJobs(t, path)
	job, ok := jobs["pin"]
	require.True(t, ok, "marketplace.yml has no `pin` job; the post-publish pin is gone")

	live := liveYAMLText(t, path)
	require.NotRegexp(t, marketplaceDevelopCheckoutRE, live+"\n",
		"marketplace.yml checks out develop: the generator it then runs is develop's, not the one that "+
			"wrote the release's marketplace.json, so any generator change made since the tag fails the "+
			"byte comparison and no pull request is opened")

	generate, compare, toDevelop := -1, -1, -1
	for i, run := range job.runs {
		if strings.Contains(run, "devtool marketplace --tag") && generate < 0 {
			generate = i
		}
		if strings.Contains(run, "cmp ") && compare < 0 {
			compare = i
		}
		if strings.Contains(run, "origin/develop") && toDevelop < 0 {
			toDevelop = i
		}
	}
	require.GreaterOrEqual(t, generate, 0, "marketplace.yml never regenerates the marketplace")
	require.GreaterOrEqual(t, compare, generate,
		"marketplace.yml must compare its regeneration with the uploaded marketplace.json after generating it")
	require.Greater(t, toDevelop, generate,
		"marketplace.yml must generate at the release's own checkout and only then move onto develop "+
			"(a step naming origin/develop) to open the pull request")
}

func TestMarketplaceWorkflowReRunsCleanly(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "marketplace.yml")
	job, ok := workflowJobs(t, path)["pin"]
	require.True(t, ok, "marketplace.yml has no `pin` job")

	var pr string
	for _, run := range job.runs {
		if strings.Contains(run, "gh pr create") {
			pr = run
		}
	}
	require.NotEmpty(t, pr, "marketplace.yml opens no pull request")

	push := strings.Index(pr, "git push")
	require.GreaterOrEqual(t, push, 0, "the pull-request step pushes no branch")
	require.Regexp(t, marketplaceLeasedPushRE, pr[push:],
		"a re-run finds marketplace/<tag> already pushed; the push must replace it under an explicit "+
			"lease (--force-with-lease=refs/heads/<branch>:<expected>) rather than fail")

	list, create := strings.Index(pr, "gh pr list"), strings.Index(pr, "gh pr create")
	require.GreaterOrEqual(t, list, 0,
		"a re-run must look for the pull request its first run opened (gh pr list --head) before "+
			"creating one; gh refuses a second pull request for the same head")
	require.Less(t, list, create, "the existing-PR check must come before gh pr create")
}

// marketplaceTypesRE is the `types:` list of the release trigger, in flow ([a, b]) spelling.
var marketplaceTypesRE = regexp.MustCompile(`(?m)^\s+types:\s*\[([^\]]*)\]\s*$`)

// marketplacePrereleaseGuardRE is the pin job's refusal to run for a pre-release.
var marketplacePrereleaseGuardRE = regexp.MustCompile(`(?m)^\s+if:.*!github\.event\.release\.prerelease\b`)

// TestMarketplaceWorkflowRunsOnceForAFullRelease pins the trigger (audit F2, V6 close-out D53(e)).
//
// GitHub's release activity types: `published` fires when a release or a PRE-RELEASE is published;
// `prereleased` when a pre-release is; `released` when a release is published OR a pre-release is
// changed to a release. The workflow listened to `published` alone and skipped pre-releases, so
// the documented route — publish as a pre-release, rehearse, then promote — fired `published` once
// for the pre-release (skipped) and never again: the promotion fires only `released`, and no
// marketplace pull request was ever opened. `released` alone is exactly once for a full release by
// either route. Listing `published` beside it would run the job twice for a release published
// directly, and the pre-release guard stays as a second line in case the trigger list ever grows.
func TestMarketplaceWorkflowRunsOnceForAFullRelease(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "marketplace.yml")
	live := liveYAMLText(t, path)

	on := yamlSection(live, "on")
	require.Contains(t, on, "release:", "marketplace.yml must be triggered by release events")
	for _, other := range []string{"push:", "pull_request:", "schedule:", "workflow_run:"} {
		require.NotContains(t, on, other, "marketplace.yml must run only for a release, not on %s", other)
	}
	m := marketplaceTypesRE.FindAllStringSubmatch(on, -1)
	require.Len(t, m, 1, "marketplace.yml's release trigger must list its activity types exactly once")
	var types []string
	for _, ty := range strings.Split(m[0][1], ",") {
		if ty = strings.Trim(strings.TrimSpace(ty), `'"`); ty != "" {
			types = append(types, ty)
		}
	}
	require.Equal(t, []string{"released"}, types,
		"only `released` fires both for a release published directly and for a pre-release promoted "+
			"to a release, and never for a pre-release; `published` misses the promotion and, beside "+
			"`released`, runs the job twice")

	require.Regexp(t, marketplacePrereleaseGuardRE, live,
		"the pin job must still refuse a pre-release (`!github.event.release.prerelease`)")
}
