package guards

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The release notes contract (V6 close-out F3, wave 17b). release.yml's notes step used to render
// only `devtool release-scope --markdown`, which reads the committed SP-17 records of 2026-09-16:
// for 0.3.0 those notes would have described an older tree than the one tagged. The step now uses
// docs/release-notes/<tag>.md when the tag has one and falls back to release-scope otherwise, and
// goreleaser publishes whichever it wrote. The checks below keep both branches, the order, and the
// hand-off to goreleaser, and keep the release version's own notes present.

// releaseNotesDir is where hand-written per-release notes live, one file per tag.
const releaseNotesDir = "docs/release-notes"

// releaseNotesFileRE is a notes file's name: the tag it is for, then .md.
var releaseNotesFileRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?\.md$`)

// releaseNotesPlaceholderRE matches the unfilled markers a notes file must never ship.
var releaseNotesPlaceholderRE = regexp.MustCompile(`\b(TODO|TBD|FIXME|XXX)\b|<placeholder>`)

// releaseNotesStepProblems reports what is wrong with release.yml's notes step, given the release
// job's run commands and the workflow's live (comment-free) text. An empty result is a pass.
func releaseNotesStepProblems(runs []string, live string) []string {
	var problems []string
	step := ""
	for _, run := range runs {
		if strings.Contains(run, "dist/release-notes.md") {
			step = run
			break
		}
	}
	if step == "" {
		return []string{"the release job has no run step that writes dist/release-notes.md"}
	}
	for _, want := range []struct{ text, why string }{
		{"docs/release-notes/${GITHUB_REF_NAME}.md", "the step must look for the tag's own notes file"},
		{"if [ -f ", "the tag's notes file must be used only when it exists"},
		{"release-scope --markdown", "a tag without a notes file must fall back to release-scope"},
		{"> dist/release-notes.md", "the release-scope fallback must write dist/release-notes.md"},
	} {
		if !strings.Contains(step, want.text) {
			problems = append(problems, want.why+" (missing "+want.text+")")
		}
	}
	notesAt := strings.Index(live, "dist/release-notes.md")
	publishAt := strings.Index(live, "goreleaser/goreleaser-action")
	if publishAt < 0 {
		problems = append(problems, "the release job has no goreleaser step")
	} else if notesAt < 0 || notesAt > publishAt {
		problems = append(problems, "the notes are written after goreleaser runs, so it publishes none")
	}
	if !strings.Contains(live, "--release-notes dist/release-notes.md") {
		problems = append(problems, "goreleaser is not handed dist/release-notes.md as --release-notes")
	}
	return problems
}

// TestReleaseNotesStepPrefersTheTagsOwnNotes pins the live release.yml notes step.
func TestReleaseNotesStepPrefersTheTagsOwnNotes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	job, ok := workflowJobs(t, path)["release"]
	require.True(t, ok, "release.yml has no `release` job; the tag-triggered path is gone")
	require.Empty(t, releaseNotesStepProblems(job.runs, liveYAMLText(t, path)),
		"release.yml's notes step must publish docs/release-notes/<tag>.md when it exists and "+
			"release-scope's table otherwise")
}

// TestReleaseNotesStepGuardRejectsReshapedSteps is the negative of the live check: a step that
// dropped either branch, or a publisher that is not handed the notes, must fail.
func TestReleaseNotesStepGuardRejectsReshapedSteps(t *testing.T) {
	t.Parallel()

	const tail = "\n      - name: goreleaser\n        uses: goreleaser/goreleaser-action@v6\n" +
		"        with:\n          args: release --clean --release-notes dist/release-notes.md\n"
	good := `notes="docs/release-notes/${GITHUB_REF_NAME}.md" if [ -f "$notes" ]; then ` +
		`cp "$notes" dist/release-notes.md else go run ./tools/devtool release-scope --markdown > ` +
		`dist/release-notes.md fi`
	require.Empty(t, releaseNotesStepProblems([]string{good}, good+tail))

	scopeOnly := "go run ./tools/devtool release-scope --markdown > dist/release-notes.md"
	require.NotEmpty(t, releaseNotesStepProblems([]string{scopeOnly}, scopeOnly+tail),
		"a step that never reads the tag's notes file must fail")

	fileOnly := `cp "docs/release-notes/${GITHUB_REF_NAME}.md" dist/release-notes.md`
	require.NotEmpty(t, releaseNotesStepProblems([]string{fileOnly}, fileOnly+tail),
		"a step with no release-scope fallback must fail")

	noNotes := strings.Replace(tail, " --release-notes dist/release-notes.md", "", 1)
	require.NotEmpty(t, releaseNotesStepProblems([]string{good}, good+noNotes),
		"a goreleaser step that is not handed the notes must fail")

	require.NotEmpty(t, releaseNotesStepProblems([]string{good}, tail+good),
		"notes written after goreleaser must fail")
}

// TestReleaseNotesFilesAreNamedForTheirTagAndFilled keeps docs/release-notes honest: every file is
// named for the tag it is for (or release.yml would never pick it up), the release version's own
// notes exist and name that version, and no notes file carries an unfilled marker.
func TestReleaseNotesFilesAreNamedForTheirTagAndFilled(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), filepath.FromSlash(releaseNotesDir))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s must exist: release.yml reads the tag's notes from it", releaseNotesDir)

	want := "v" + core.Version + ".md"
	found := false
	for _, e := range entries {
		require.False(t, e.IsDir(), "%s/%s: the notes directory holds one file per tag, no subdirectories",
			releaseNotesDir, e.Name())
		require.Regexp(t, releaseNotesFileRE, e.Name(),
			"%s/%s is not named v<semver>.md, so no tag would ever select it", releaseNotesDir, e.Name())
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, readErr)
		text := string(b)
		require.NotRegexp(t, releaseNotesPlaceholderRE, text,
			"%s/%s carries an unfilled marker; release notes ship as written", releaseNotesDir, e.Name())
		version := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "v"), ".md")
		require.True(t, strings.HasPrefix(text, "# Qompack "+version+"\n"),
			"%s/%s must open on the heading `# Qompack %s`, so a file copied for a new release "+
				"cannot keep the old version", releaseNotesDir, e.Name(), version)
		if e.Name() == want {
			found = true
		}
	}
	require.True(t, found,
		"%s/%s is missing: internal/core.Version is %s, and a tag for it must publish notes "+
			"written for it rather than release-scope's SP-17 table", releaseNotesDir, want, core.Version)
}
