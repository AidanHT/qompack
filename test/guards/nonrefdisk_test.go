package guards

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// The non-reference-disk declaration (internal/obs.NonReferenceDiskEnv, D53(e), the owner's Q1
// third option) reports the fsync-bound wall rows — B-A, B-B, B-E's wall row — and the spool-submode
// transition on GitHub-hosted runners instead of gating them. It is safe only while two things stay
// true, and this file holds both:
//
//   - it is HOSTED-CI ONLY: honoured where GITHUB_ACTIONS=true and nowhere else, and set by exactly
//     the ci.yml jobs whose fsync-bound rows a hosted disk cannot judge alone on their runner
//     (bench-gate, timing, test-e2e, and cover and release-dry-run, whose test/e2e pass runs alone
//     as test-e2e does; devtool takes it back from their co-loaded pass) — never by the whole-tree
//     `test` job, whose own declaration is co-load — in nightly.yml by bench-deep alone (D55), and
//     in release.yml by the tag's `release` job, which runs the same release-check;
//   - the REFERENCE verdict on those rows survives: the owner's quiet local runs (quiet.sh's C5.1,
//     phase3.sh's isolated D28 rows, overnight.sh which chains them) never make the declaration,
//     and never claim to be GitHub Actions, the one thing that would make it honoured.

// workflowNonrefDiskEnvRE is the env-block line that makes the declaration, built from the Go
// constant so a rename that is not mirrored in the YAML reads here as no job declaring it.
var workflowNonrefDiskEnvRE = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(obs.NonReferenceDiskEnv) + `:\s*\S`)

// nonrefDiskJobs are the ci.yml jobs that make the declaration, each on every hosted OS of its
// matrix: the ones that judge fsync-bound wall rows alone on their runner. cover is one because its
// test/e2e pass runs X11 alone (tools/devtool/cover.go, coverPasses); its co-loaded pass has the
// declaration taken back by devtool, which tools/devtool's
// TestCoverPasses_RunsE2EAloneWithoutTheColoadDeclaration pins. release-dry-run is one for the same
// reason: `devtool release-check` runs ci-local's test and cover steps, so X11 runs alone there
// twice. It was the job D53(e) missed: run 36955046276's release-dry-run (job 110676058084) failed
// X11 on ubuntu-latest B-A/B-B p99 49.152 ms against 15 in the cover step, after the same package
// had passed in the test step on the same runner minutes earlier.
var nonrefDiskJobs = []string{"bench-gate", "cover", "release-dry-run", "test-e2e", "timing"}

// releaseNonrefDiskJobs are the release.yml jobs that make the declaration: `release` alone. It runs
// `devtool release-check --tag` on a hosted runner — the release-dry-run gate plus the tag/version
// agreement — so without the declaration a hosted fsync tail in X11 would refuse a real release on
// a figure that measures the runner's disk (owner Q1). The reference verdict on those rows at tag
// time is the coordinator's local `release-check --tag` on the reference host, where GITHUB_ACTIONS
// is unset and the declaration is ignored.
var releaseNonrefDiskJobs = []string{"release"}

// fsyncBoundRunRE matches a workflow `run:` command that judges a hot-path fsync-bound wall row:
// the devtool tasks that run test/e2e's isolated pass (test, cover, ci-local, release-check), the
// harness itself (bench-hotpath), and a `go test` naming test/e2e or a hot-path row by name. A job
// that runs one must say which cause its runner has — co-load or a non-reference disk — or it
// gates hosted disk weather.
var fsyncBoundRunRE = regexp.MustCompile(
	`devtool (release-check|ci-local|cover|test|bench-hotpath)(\s|$)|go test .*(test/e2e|HotPath)`)

// fsyncBoundWorkflows are the workflow files the cause check reads: every hosted workflow there is.
var fsyncBoundWorkflows = []string{"ci.yml", "nightly.yml", "release.yml", "marketplace.yml"}

// nightlyNonrefDiskJobs are the nightly.yml jobs that make the declaration: bench-deep alone.
// It runs the hot-path harness alone on hosted runners and meets the same fsync tail as
// bench-gate: windows-latest B-B p99 1310.7 ms against 50 in run 36820740318 (job 110235563683).
// The coordinator ruled under D33 that it reports those rows as bench-gate does (D55). Pinning the
// set means any further nightly job is ruled here, in a reviewed edit, and not by a workflow change
// nobody checks.
var nightlyNonrefDiskJobs = []string{"bench-deep"}

// referenceRunScripts are the coordinator's local scripts whose runs are the reference verdict on
// the rows the declaration reports on hosted runners.
var referenceRunScripts = []string{"quiet.sh", "phase3.sh", "overnight.sh"}

// githubActionsAssignRE matches a shell line that sets GITHUB_ACTIONS (an assignment, an export, or
// an `env GITHUB_ACTIONS=` prefix). A local script that did so would make the declaration honoured.
var githubActionsAssignRE = regexp.MustCompile(`(^|[\s;(])(export\s+)?` + regexp.QuoteMeta(obs.GitHubActionsEnv) + `=`)

// TestNonReferenceDisk_IsHostedCIOnly pins the declaration to hosted CI: ignored outside GitHub
// Actions, set by exactly bench-gate, cover, release-dry-run, timing and test-e2e in ci.yml, by
// bench-deep in nightly.yml and by release in release.yml, and never set — nor made honourable by
// faking GitHub Actions — by the scripts that produce the reference verdict. Every hosted job that
// judges a fsync-bound row declares one cause or the other, which is the check that would have caught
// release-dry-run.
func TestNonReferenceDisk_IsHostedCIOnly(t *testing.T) {
	root := repoRoot(t)

	t.Run("ignored_outside_github_actions", func(t *testing.T) {
		t.Setenv(obs.NonReferenceDiskEnv, "1")
		for _, v := range []string{"", "false", "1", "TRUE", "yes"} {
			t.Setenv(obs.GitHubActionsEnv, v)
			require.False(t, obs.NonReferenceDisk(),
				"%s=1 must be ignored where %s=%q: a local run cannot waive an fsync-bound gate",
				obs.NonReferenceDiskEnv, obs.GitHubActionsEnv, v)
		}
		t.Setenv(obs.GitHubActionsEnv, "true")
		require.True(t, obs.NonReferenceDisk(), "and honoured on a GitHub Actions runner")
	})

	t.Run("ci_yml_sets_it_in_exactly_the_hosted_isolation_jobs", func(t *testing.T) {
		jobs := workflowJobs(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
		var setting []string
		for name, job := range jobs {
			if job.setsNonrefDisk {
				setting = append(setting, name)
			}
		}
		sort.Strings(setting)
		require.Equal(t, nonrefDiskJobs, setting,
			"ci.yml must declare %s in exactly %v: the jobs that judge fsync-bound wall rows alone on a hosted "+
				"runner. The whole-tree jobs declare co-load instead (%s), and a job that declared both "+
				"would hide which cause it meant", obs.NonReferenceDiskEnv, nonrefDiskJobs, obs.UnderColoadEnv)
		for _, name := range nonrefDiskJobs {
			require.False(t, jobs[name].setsCoload,
				"ci.yml's `%s` job must not also declare %s: a hosted runner running one job is not co-loaded",
				name, obs.UnderColoadEnv)
		}
	})

	t.Run("nightly_yml_declares_it_in_exactly_the_ruled_jobs", func(t *testing.T) {
		jobs := workflowJobs(t, filepath.Join(root, ".github", "workflows", "nightly.yml"))
		var setting []string
		for name, job := range jobs {
			if job.setsNonrefDisk {
				setting = append(setting, name)
			}
		}
		sort.Strings(setting)
		require.Equal(t, nightlyNonrefDiskJobs, setting,
			"nightly.yml must declare %s in exactly %v: a nightly job that reports hosted fsync-bound rows "+
				"needs the owner's ruling recorded here first", obs.NonReferenceDiskEnv, nightlyNonrefDiskJobs)
		for _, name := range nightlyNonrefDiskJobs {
			require.False(t, jobs[name].setsCoload,
				"nightly.yml's `%s` job must not also declare %s: a hosted runner running one job is not co-loaded",
				name, obs.UnderColoadEnv)
		}
	})

	t.Run("release_yml_declares_it_in_exactly_the_release_job", func(t *testing.T) {
		jobs := workflowJobs(t, filepath.Join(root, ".github", "workflows", "release.yml"))
		var setting []string
		for name, job := range jobs {
			if job.setsNonrefDisk {
				setting = append(setting, name)
			}
		}
		sort.Strings(setting)
		require.Equal(t, releaseNonrefDiskJobs, setting,
			"release.yml must declare %s in exactly %v: the tag's release-check runs X11 alone on a hosted "+
				"runner, and a further declaring job needs its ruling recorded here first",
			obs.NonReferenceDiskEnv, releaseNonrefDiskJobs)
		for _, name := range releaseNonrefDiskJobs {
			require.False(t, jobs[name].setsCoload,
				"release.yml's `%s` job must not also declare %s: a hosted runner running one job is not co-loaded",
				name, obs.UnderColoadEnv)
		}
	})

	t.Run("every_hosted_fsync_bound_job_declares_its_cause", func(t *testing.T) {
		for _, file := range fsyncBoundWorkflows {
			jobs := workflowJobs(t, filepath.Join(root, ".github", "workflows", file))
			for name, job := range jobs {
				for _, run := range job.runs {
					if !fsyncBoundRunRE.MatchString(run) {
						continue
					}
					require.True(t, job.setsNonrefDisk || job.setsCoload,
						"%s's `%s` job runs %q, which judges a fsync-bound hot-path row (X11, the harness), "+
							"on a GitHub-hosted runner without declaring %s or %s: it gates the runner's disk "+
							"rather than the product (owner Q1; run 36955046276's release-dry-run)",
						file, name, run, obs.NonReferenceDiskEnv, obs.UnderColoadEnv)
				}
			}
		}
	})

	t.Run("reference_run_scripts_never_declare_it", func(t *testing.T) {
		dir := filepath.Join(root, "plans", "sdd", "V6-closeout", "coordinator")
		// The reference-run scripts are maintainer-only, under plans/, and absent from a public
		// checkout. Only their absence skips; any other error still fails.
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Skip("platform: plans/sdd/V6-closeout/coordinator is maintainer-only and absent from this checkout")
		}
		for _, name := range referenceRunScripts {
			b, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err, "the reference-run script %s must exist where this guard reads it", name)
			for i, line := range strings.Split(string(b), "\n") {
				require.NotContains(t, line, obs.NonReferenceDiskEnv,
					"%s:%d mentions %s: the owner's quiet reference runs judge the rows it reports, so they "+
						"never make it", name, i+1, obs.NonReferenceDiskEnv)
				require.False(t, githubActionsAssignRE.MatchString(line),
					"%s:%d sets %s, which would make %s honoured in a reference run: %q",
					name, i+1, obs.GitHubActionsEnv, obs.NonReferenceDiskEnv, line)
			}
		}
	})
}
