package guards

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The pre-tag byte comparison (decisions D53(h)(4), D57(c), D58(a)). ci.yml's release-dry-run job
// builds the six bundles at the release version and uploads them as the release-version-bundles
// artifact, which the coordinator compares file by file with the frozen candidate's bundles before
// a tag is pushed. Every bundle carries .mcp.json and .claude-plugin/plugin.json, and
// actions/upload-artifact skips hidden files unless include-hidden-files is true: candidate 7's
// first comparison (phase3/c7/hosted-release-bundles.txt, run 36981590450) listed all twelve of
// those files as missing from the hosted side, and 79025ae3 added the setting. The check below
// keeps it on that step.

// releaseBundlesArtifact is the artifact name the coordinator's comparison downloads.
const releaseBundlesArtifact = "release-version-bundles"

var (
	// workflowStepRE matches the first line of a step in a job's steps list.
	workflowStepRE = regexp.MustCompile(`^(\s*)- `)
	// releaseBundlesNameRE matches the upload step's artifact name line.
	releaseBundlesNameRE = regexp.MustCompile(`(?m)^\s*name:\s*` + regexp.QuoteMeta(releaseBundlesArtifact) + `\s*$`)
	// includeHiddenFilesRE matches the setting that keeps dot-files in the artifact.
	includeHiddenFilesRE = regexp.MustCompile(`(?m)^\s*include-hidden-files:\s*true\s*$`)
)

// jobStepBlocks returns the steps of one job in a workflow's live text, each as its own lines
// joined by newlines. Steps are found as the list items at the first dash indent under `steps:`.
func jobStepBlocks(live, job string) []string {
	lines := strings.Split(live, "\n")
	in := false
	var body []string
	for _, line := range lines {
		if m := workflowJobHeaderRE.FindStringSubmatch(line); m != nil {
			in = m[1] == job
			continue
		}
		if in {
			body = append(body, line)
		}
	}
	var steps []string
	var cur []string
	indent := -1
	inSteps := false
	for _, line := range body {
		if strings.TrimSpace(line) == "steps:" {
			inSteps = true
			continue
		}
		if !inSteps {
			continue
		}
		if m := workflowStepRE.FindStringSubmatch(line); m != nil && (indent < 0 || len(m[1]) == indent) {
			if indent < 0 {
				indent = len(m[1])
			}
			if cur != nil {
				steps = append(steps, strings.Join(cur, "\n"))
			}
			cur = []string{line}
			continue
		}
		if cur != nil {
			cur = append(cur, line)
		}
	}
	if cur != nil {
		steps = append(steps, strings.Join(cur, "\n"))
	}
	return steps
}

// releaseBundlesUploadProblems reports what is wrong with release-dry-run's upload of the
// release-version bundles, given ci.yml's live text. An empty result is a pass.
func releaseBundlesUploadProblems(live string) []string {
	steps := jobStepBlocks(live, "release-dry-run")
	if len(steps) == 0 {
		return []string{"ci.yml has no release-dry-run job with steps"}
	}
	var upload string
	for _, s := range steps {
		if strings.Contains(s, "actions/upload-artifact@") && releaseBundlesNameRE.MatchString(s) {
			upload = s
			break
		}
	}
	if upload == "" {
		return []string{"release-dry-run has no actions/upload-artifact step named " + releaseBundlesArtifact}
	}
	if !includeHiddenFilesRE.MatchString(upload) {
		return []string{"the " + releaseBundlesArtifact + " upload does not set include-hidden-files: true, " +
			"so .mcp.json and .claude-plugin/ are missing from the hosted side of the byte comparison"}
	}
	return nil
}

// TestReleaseBundlesUploadKeepsHiddenFiles pins the live ci.yml upload step.
func TestReleaseBundlesUploadKeepsHiddenFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")
	require.Empty(t, releaseBundlesUploadProblems(liveYAMLText(t, path)),
		"ci.yml's release-dry-run must upload the release-version bundles with their hidden files")
}

// TestReleaseBundlesUploadGuardRejectsReshapedSteps is the negative of the live check: an upload
// without the setting, with it set false, or with it on another step must fail.
func TestReleaseBundlesUploadGuardRejectsReshapedSteps(t *testing.T) {
	t.Parallel()

	const head = "jobs:\n  release-dry-run:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - uses: actions/checkout@v4\n"
	const upload = "      - uses: actions/upload-artifact@v4\n        if: always()\n        with:\n" +
		"          name: release-version-bundles\n          path: ${{ runner.temp }}/release-bundles/\n"
	const tail = "      - run: go run ./tools/devtool release-check --skip-vulncheck\n" +
		"  docs:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"

	good := head + upload + "          include-hidden-files: true\n" + tail
	require.Empty(t, releaseBundlesUploadProblems(good))

	require.NotEmpty(t, releaseBundlesUploadProblems(head+upload+tail),
		"an upload without include-hidden-files must fail")
	require.NotEmpty(t, releaseBundlesUploadProblems(head+upload+"          include-hidden-files: false\n"+tail),
		"include-hidden-files: false must fail")

	elsewhere := head + upload + "      - uses: actions/upload-artifact@v4\n        with:\n" +
		"          name: other\n          path: x\n          include-hidden-files: true\n" + tail
	require.NotEmpty(t, releaseBundlesUploadProblems(elsewhere),
		"the setting on another upload step must not count")

	otherJob := head + upload + tail + "      - uses: actions/upload-artifact@v4\n        with:\n" +
		"          include-hidden-files: true\n"
	require.NotEmpty(t, releaseBundlesUploadProblems(otherJob),
		"the setting in another job must not count")

	noUpload := head + tail
	require.NotEmpty(t, releaseBundlesUploadProblems(noUpload),
		"a release-dry-run without the upload must fail")
}
