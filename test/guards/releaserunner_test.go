package guards

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The release path's runner image (V6 close-out D62(h)). GitHub moves the ubuntu-latest label to
// Ubuntu 26 on 2026-10-19 (the annotation on run 36981590450; actions/runner-images issue 14748).
// The hosted release evidence a tag leans on came from Ubuntu 24.04, so a job on the moving label
// would run the release gate, or build and upload the release, on an image no run of this
// repository has been green on. ci.yml's release-dry-run and every job of release.yml (any job the
// release job could need included) therefore name the image itself. The other Linux jobs of ci.yml
// and nightly.yml stay on ubuntu-latest on purpose: they are where a regression on the new image
// shows first.
//
// The parse is line-based, like every workflow guard here (release_test.go says why there is no
// YAML decoder), and reads the live text only (liveYAMLText), so this prose never counts.

// releaseRunnerImage is the runner image the release jobs pin.
//
// DERIVATION: it is the image candidate 7's hosted release evidence ran on. ci.yml run 36981590450,
// job release-dry-run (110757119491), logged "Image: ubuntu-24.04", version 20260927.320.1, under
// "Runner Image" in its set-up step, and that run's release-version bundles were byte-identical to
// candidate 7's frozen ones (docs/release.md). Moving the pin is an owner decision that needs a
// green hosted run on the new image first.
const releaseRunnerImage = "ubuntu-24.04"

var (
	// releaseRunsOnRE is a job-level runs-on line: four spaces in, under a two-space job header.
	releaseRunsOnRE = regexp.MustCompile(`^    runs-on:\s*(.*?)\s*$`)
	// releaseNeedsRE is a job-level needs line, its value scalar, flow list or empty (block list).
	releaseNeedsRE = regexp.MustCompile(`^    needs:\s*(.*?)\s*$`)
	// releaseNeedsItemRE is one item of a block-list needs value.
	releaseNeedsItemRE = regexp.MustCompile(`^      -\s+(\S+)\s*$`)
)

// workflowJobBodies splits a workflow's live text into each job's lines, keyed by job name, reading
// only the headers under `jobs:` (an `on:` trigger's `push:` has the same indent).
func workflowJobBodies(live string) map[string][]string {
	jobs := map[string][]string{}
	inJobs := false
	var name string
	for _, line := range strings.Split(live, "\n") {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			break // the next top-level key
		}
		if m := workflowJobHeaderRE.FindStringSubmatch(line); m != nil {
			name = m[1]
			jobs[name] = []string{}
			continue
		}
		if name != "" {
			jobs[name] = append(jobs[name], line)
		}
	}
	return jobs
}

// releaseJobNeeds returns the job names one job's needs value lists, in any of YAML's three
// spellings.
func releaseJobNeeds(body []string) []string {
	var needs []string
	for i, line := range body {
		m := releaseNeedsRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[1])
		switch {
		case v == "":
			for _, item := range body[i+1:] {
				im := releaseNeedsItemRE.FindStringSubmatch(item)
				if im == nil {
					break
				}
				needs = append(needs, strings.Trim(im[1], `'"`))
			}
		case strings.HasPrefix(v, "["):
			for _, n := range strings.Split(strings.Trim(v, "[]"), ",") {
				if n = strings.Trim(strings.TrimSpace(n), `'"`); n != "" {
					needs = append(needs, n)
				}
			}
		default:
			needs = append(needs, strings.Trim(v, `'"`))
		}
	}
	return needs
}

// releaseRunnerProblems lists every way the named jobs of a workflow's live text, and every job they
// need (transitively), fail to run on releaseRunnerImage. No names means every job of the workflow.
func releaseRunnerProblems(live string, names ...string) []string {
	jobs := workflowJobBodies(live)
	if len(jobs) == 0 {
		return []string{"no jobs parsed under `jobs:`; if the layout changed, update this guard"}
	}
	if len(names) == 0 {
		for name := range jobs {
			names = append(names, name)
		}
	}
	var problems []string
	seen := map[string]bool{}
	for len(names) > 0 {
		name := names[0]
		names = names[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		body, ok := jobs[name]
		if !ok {
			problems = append(problems, name+": no such job")
			continue
		}
		var images []string
		for _, line := range body {
			if m := releaseRunsOnRE.FindStringSubmatch(line); m != nil {
				images = append(images, strings.Trim(m[1], `'"`))
			}
		}
		switch {
		case len(images) == 0:
			problems = append(problems, name+": no job-level runs-on")
		case len(images) > 1:
			problems = append(problems, name+": runs-on given more than once")
		case images[0] != releaseRunnerImage:
			problems = append(problems, name+": runs-on "+images[0]+", not "+releaseRunnerImage)
		}
		names = append(names, releaseJobNeeds(body)...)
	}
	return problems
}

// TestReleaseJobsPinTheirRunnerImage pins the live workflows: ci.yml's release-dry-run, every job of
// release.yml, and anything either needs, run on releaseRunnerImage.
func TestReleaseJobsPinTheirRunnerImage(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	require.Empty(t, releaseRunnerProblems(liveYAMLText(t, filepath.Join(dir, "ci.yml")), "release-dry-run"),
		"ci.yml's release-dry-run runs the release gate a tag runs; it must stay on the image the hosted "+
			"release evidence came from, not on the moving ubuntu-latest label")
	require.Empty(t, releaseRunnerProblems(liveYAMLText(t, filepath.Join(dir, "release.yml"))),
		"release.yml builds and uploads the release; every one of its jobs must run on the pinned image")
}

// TestReleaseRunnerGuardRejectsReshapedJobs is the negative of the live check.
func TestReleaseRunnerGuardRejectsReshapedJobs(t *testing.T) {
	t.Parallel()

	const head = "on:\n  push:\n    tags: ['v*']\njobs:\n"
	job := func(name, runsOn, extra string) string {
		s := "  " + name + ":\n"
		if runsOn != "" {
			s += "    runs-on: " + runsOn + "\n"
		}
		return s + extra + "    steps:\n      - run: true\n"
	}

	good := head + job("release", releaseRunnerImage, "")
	require.Empty(t, releaseRunnerProblems(good))
	require.Empty(t, releaseRunnerProblems(head+job("release", "'"+releaseRunnerImage+"'", "")),
		"a quoted image is the same value")
	require.Empty(t, releaseRunnerProblems(good+job("docs", "ubuntu-latest", ""), "release"),
		"a job the named one does not need is not judged")

	require.NotEmpty(t, releaseRunnerProblems(head+job("release", "ubuntu-latest", "")),
		"the moving label must fail")
	require.NotEmpty(t, releaseRunnerProblems(head+job("release", "${{ matrix.os }}", "")),
		"an expression is not the pin")
	require.NotEmpty(t, releaseRunnerProblems(head+job("release", "", "")), "no runs-on must fail")
	require.NotEmpty(t, releaseRunnerProblems(head+job("release", "",
		"    steps:\n      - with:\n          runs-on: "+releaseRunnerImage+"\n")),
		"a runs-on deeper than the job's own level does not count")
	require.NotEmpty(t, releaseRunnerProblems(good, "release-dry-run"), "a named job that is gone must fail")
	require.NotEmpty(t, releaseRunnerProblems(good+job("docs", "ubuntu-latest", "")),
		"with no names every job is judged")

	for _, needs := range []string{"    needs: build\n", "    needs: [lint, build]\n", "    needs:\n      - build\n"} {
		shaped := head + job("release", releaseRunnerImage, needs) + job("build", "ubuntu-latest", "") +
			job("lint", releaseRunnerImage, "")
		require.NotEmpty(t, releaseRunnerProblems(shaped, "release"),
			"a needed job on the moving label must fail (%q)", needs)
		fixed := strings.Replace(shaped, "runs-on: ubuntu-latest", "runs-on: "+releaseRunnerImage, 1)
		require.Empty(t, releaseRunnerProblems(fixed, "release"), "control: %q", needs)
	}
}
