package guards

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The release contract this file guards (SP-17 Task 7, coordinator ruling R7-1):
//
// goreleaser stays the PUBLISHER — 00-ARCHITECTURE.md §2.6 names it and a tag still reaches it —
// but it stops being a BUILDER. Every shipped byte now comes from `devtool bundle --archive`,
// which goes through the one `goBuildArgs` invocation `build` and `build-all` also use, so the
// determinism flags (-trimpath, -buildvcs=false, -buildid=) cannot differ between what CI checks
// and what a tag ships. Two build paths writing the same -X symbol is exactly the disagreement
// packaging/README.md §4 called "the goreleaser gap", and the only way to close it permanently is
// to delete one of them.
//
// A line-based parse rather than a YAML decode: gopkg.in/yaml.v3 is an INDIRECT requirement of
// this module (go.mod), and importing it here would promote it to a direct one — a new declared
// dependency for a guard that needs to find four keys. The assertions below are deliberately
// written against text a human also reads.

var (
	// goreleaserListItemRE counts every builds list entry, with or without an id. goreleaser's
	// build id is optional (it defaults from the binary name); counting only `id:` lines would
	// let an un-skipped second entry pass the guard while goreleaser still compiled it.
	goreleaserListItemRE = regexp.MustCompile(`(?m)^  - `)
	goreleaserDistRE     = regexp.MustCompile(`(?m)^dist:\s*(\S+)\s*$`)
)

// liveYAMLText returns a file with its comment lines removed, so a guard never reads the prose
// that explains it as a setting (liveWorkflowText's rule, applied to any YAML).
func liveYAMLText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var live []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			live = append(live, line)
		}
	}
	return strings.Join(live, "\n")
}

// yamlSection returns the lines of the top-level block named key, without its header.
func yamlSection(text, key string) string {
	lines := strings.Split(text, "\n")
	var out []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, key+":") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// goreleaserBuildChunks splits the builds section into one chunk per `  - ` list item.
func goreleaserBuildChunks(builds string) []string {
	idxs := goreleaserListItemRE.FindAllStringIndex(builds, -1)
	if len(idxs) == 0 {
		return nil
	}
	var chunks []string
	for i, idx := range idxs {
		end := len(builds)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		chunks = append(chunks, builds[idx[0]:end])
	}
	return chunks
}

// goreleaserDistValue returns the top-level dist: value, if any.
func goreleaserDistValue(text string) (string, bool) {
	m := goreleaserDistRE.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.Trim(m[1], `"'`), true
}

// goreleaserDistIsSafe reports whether dist is neither `dist` nor a path prefix of dist/bundle
// or of dist/release-notes.md's directory `dist`. goreleaser --clean removes its dist folder;
// that folder must not be the one holding the archives or the notes it is asked to upload.
func goreleaserDistIsSafe(value string) bool {
	v := path.Clean(filepath.ToSlash(value))
	v = strings.TrimSuffix(v, "/")
	if v == "" || v == "." || v == "dist" {
		return false
	}
	return !pathIsPrefix(v, "dist/bundle") && !pathIsPrefix(v, "dist")
}

// pathIsPrefix reports whether prefix is path or a parent of path, using slash form.
func pathIsPrefix(prefix, path string) bool {
	prefix = strings.TrimSuffix(filepath.ToSlash(prefix), "/")
	path = filepath.ToSlash(path)
	return prefix == path || strings.HasPrefix(path, prefix+"/")
}

// TestGoreleaserBuildsNothingAndDraftsTheRelease pins R7-1 on .goreleaser.yaml itself.
func TestGoreleaserBuildsNothingAndDraftsTheRelease(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".goreleaser.yaml")
	text := liveYAMLText(t, path)

	builds := yamlSection(text, "builds")
	chunks := goreleaserBuildChunks(builds)
	require.NotEmpty(t, chunks,
		".goreleaser.yaml has no build entry at all; R7-1 keeps the entry and disables it, so the "+
			"contract stays visible in the file a release engineer reads")
	for i, chunk := range chunks {
		require.Contains(t, chunk, "skip: true",
			".goreleaser.yaml build entry %d has no `skip: true`: every build entry must be skipped. "+
				"One build path (devtool's goBuildArgs) produces every shipped byte; a second "+
				"compiler writing the same -X symbol is the determinism gap packaging/README.md §4 names",
			i+1)
	}

	require.Contains(t, yamlSection(text, "checksum"), "disable: true",
		"checksum.disable must be true: `devtool bundle --archive` writes dist/bundle/checksums.txt "+
			"over the archives it produced, and a second checksum file over files goreleaser did not "+
			"build would describe nothing")

	dist, ok := goreleaserDistValue(text)
	require.True(t, ok, ".goreleaser.yaml must set top-level `dist:`; the default is ./dist, which "+
		"`release --clean` deletes, taking dist/bundle/** and dist/release-notes.md with it")
	require.True(t, goreleaserDistIsSafe(dist),
		"dist: %q is `dist` or a path prefix of dist/bundle (or of dist/release-notes.md's "+
			"directory dist); goreleaser must own a subdirectory so --clean cannot delete the archives",
		dist)

	release := yamlSection(text, "release")
	require.Contains(t, release, "draft: true",
		"release.draft must be true: a tag creates a DRAFT a human publishes, so nothing reaches "+
			"users without a person deciding it should")
	require.NotContains(t, release, "disable:",
		"release.disable is SP-01's placeholder and R7-1 replaces it with draft+extra_files; leaving "+
			"both would make the file say the release is off and drafted at once")

	for _, glob := range []string{
		"dist/bundle/*.zip",
		"dist/bundle/checksums.txt",
		"dist/bundle/marketplace.json",
		// Audit F4 (V6 close-out D53(e)): every zip carries LICENSE and THIRD_PARTY_NOTICES.md
		// (devtool bundle), and the release page offers both beside them.
		"glob: LICENSE",
		"glob: THIRD_PARTY_NOTICES.md",
	} {
		require.Contains(t, release, glob,
			"release.extra_files must name %s: the archives devtool assembled are the artifacts, and "+
				"anything goreleaser does not upload is not in the release", glob)
	}
	// C7.5: every target ships a .zip, so nothing produces a *.tar.gz any more, and an extra_files
	// glob that matches nothing fails goreleaser's upload rather than being skipped.
	require.NotContains(t, release, "*.tar.gz",
		"release.extra_files names *.tar.gz, which `devtool bundle --archive` no longer produces")
}

// TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck pins the ORDER of release.yml: the gate
// runs before the publisher, or the gate is decoration.
//
// The parse is workflowJobs', so a release.yml whose job header stops being two-space indented
// fails here too — deliberately: that parser is what coload_test.go and this file both read the
// workflows with, and a file it cannot see is a file no guard covers.
func TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	jobs := workflowJobs(t, path)

	job, ok := jobs["release"]
	require.True(t, ok, "release.yml has no `release` job; the tag-triggered path is gone")

	gate, publish := -1, -1
	for i, run := range job.runs {
		if strings.Contains(run, "devtool release-check") && gate < 0 {
			gate = i
		}
		if strings.Contains(run, "goreleaser") && publish < 0 {
			publish = i
		}
	}
	require.GreaterOrEqual(t, gate, 0,
		"release.yml runs no `devtool release-check`: a tag would publish whatever the tree happened "+
			"to hold, which is the gap survey-release-install.md §10.1 recorded (ci-local omits "+
			"build-all, govulncheck, the import allowlist and two doc checks)")

	// goreleaser is a uses: step rather than a run: step, so its absence from job.runs is the
	// ordinary case; what must never happen is a RUN step invoking it before the gate.
	if publish >= 0 {
		require.Greater(t, publish, gate,
			"release.yml runs goreleaser at step %d and release-check at step %d: the gate must come "+
				"first or it gates nothing", publish, gate)
	}

	text := liveYAMLText(t, path)
	gateAt := strings.Index(text, "devtool release-check")
	relAt := strings.Index(text, "goreleaser")
	require.GreaterOrEqual(t, gateAt, 0, "release.yml names no release-check step")
	require.Greater(t, relAt, gateAt,
		"the first mention of goreleaser in release.yml precedes release-check; a publisher that "+
			"runs before its gate is not gated")
}

// TestGoreleaserGuardRejectsReshapedYAML is the negative of the live-file assertions: an
// id-less second builds entry is still counted and must carry skip: true, and a disable: true
// under release: does not satisfy the checksum assertion.
func TestGoreleaserGuardRejectsReshapedYAML(t *testing.T) {
	t.Parallel()

	reshaped := "builds:\n  - id: qompack\n    skip: true\n  - main: ./cmd/x\n"
	chunks := goreleaserBuildChunks(yamlSection(reshaped, "builds"))
	require.Len(t, chunks, 2,
		"an id-less second builds entry must still count so the guard cannot be passed by omitting id")
	require.Contains(t, chunks[0], "skip: true")
	require.NotContains(t, chunks[1], "skip: true",
		"the id-less second entry has no skip: true; the live guard must fail this shape")

	onlyRelease := "checksum:\n  name_template: '{{ .ProjectName }}_checksums.txt'\nrelease:\n  disable: true\n  draft: true\n"
	require.NotContains(t, yamlSection(onlyRelease, "checksum"), "disable: true",
		"disable: true under release: alone must not satisfy the checksum assertion")
	require.False(t, goreleaserDistIsSafe("dist"))
	require.False(t, goreleaserDistIsSafe("./dist"))
	require.False(t, goreleaserDistIsSafe("dist/bundle"))
	require.False(t, goreleaserDistIsSafe("dist/bundle/.."))
	require.True(t, goreleaserDistIsSafe("dist/goreleaser"))
}
