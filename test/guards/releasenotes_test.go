package guards

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// releaseNotesPage stands for the tag's own notes, docs/release-notes/<tag>.md, in
// releaseInterimMarkers.
const releaseNotesPage = "docs/release-notes/<tag>.md"

// releaseInterimMarkers lists, per page, the text a candidate's pages carry while its own evidence
// is still owed (docs/release.md §1, step 2). Each marker is a sentence that is true of the tree
// only before that evidence is recorded: the candidate's record "when it is frozen", its hosted
// runs not yet run, its evidence owed, the notes' figures standing "until candidate N's are
// recorded". Step 2 rewrites these pages from the tagged candidate's recorded evidence in a
// docs-only descendant (D58(e)), so none may still be there when the tag is pushed. The list is
// the markers these pages carry today, and TestReleaseInterimMarkersStillMatchTheirPages keeps it
// so until the release is prepared: a reworded interim sentence fails that check instead of
// silently disabling its marker. Every page here is checked on the tag push.
var releaseInterimMarkers = map[string][]*regexp.Regexp{
	releaseNotesPage: {
		regexp.MustCompile(`until candidate [0-9]+'s are recorded`),
		regexp.MustCompile(`the release is not published before they are`),
	},
	"README.md": {
		regexp.MustCompile("-CANDIDATE\\.md` when it is frozen"),
		regexp.MustCompile(`own evidence is still owed`),
		regexp.MustCompile(`Neither workflow has run on candidate [0-9]+ yet`),
		regexp.MustCompile(`that record is owed`),
	},
	"CHANGELOG.md": {
		regexp.MustCompile("-CANDIDATE\\.md` when it is frozen"),
		regexp.MustCompile(`supply the release's evidence, and they are owed`),
	},
	"docs/release.md": {
		regexp.MustCompile("-CANDIDATE\\.md` when it is frozen"),
		regexp.MustCompile(`Still owed before the tag, all on candidate [0-9]+`),
		regexp.MustCompile(`Neither workflow has run on candidate [0-9]+ yet`),
	},
	"docs/architecture.md": {
		regexp.MustCompile(`C5\.2 night re-measures it`),
	},
}

// releasePageInterimProblems reports the interim markers left in a page's text when the run is a
// tag push (refType "tag", GitHub's GITHUB_REF_TYPE). On a branch push, and on the local
// `release-check --tag` rehearsal, which sets no GITHUB_REF_TYPE, the interim text is true of the
// tree and nothing is reported.
func releasePageInterimProblems(page, text, refType string) []string {
	if refType != "tag" {
		return nil
	}
	flat := strings.Join(strings.Fields(text), " ")
	var problems []string
	for _, re := range releaseInterimMarkers[page] {
		for _, m := range re.FindAllString(flat, -1) {
			problems = append(problems, page+" still carries interim text: "+m)
		}
	}
	return problems
}

// releaseNotesInterimProblems is releasePageInterimProblems for the tag's notes.
func releaseNotesInterimProblems(text, refType string) []string {
	return releasePageInterimProblems(releaseNotesPage, text, refType)
}

// TestReleaseNotesForAPushedTagCarryNoInterimSentence runs inside release.yml's release-check
// (its guards step) on the tag push: the published notes may not still say their figures stand
// until the candidate's own are recorded.
func TestReleaseNotesForAPushedTagCarryNoInterimSentence(t *testing.T) {
	t.Parallel()

	refType, tag := os.Getenv("GITHUB_REF_TYPE"), os.Getenv("GITHUB_REF_NAME")
	if refType != "tag" {
		tag = "v" + core.Version
	}
	b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(releaseNotesDir), tag+".md"))
	if os.IsNotExist(err) {
		return // no hand-written notes for this tag; release.yml publishes release-scope's table
	}
	require.NoError(t, err)
	require.Empty(t, releaseNotesInterimProblems(string(b), refType),
		"%s/%s.md: rewrite the verified-where paragraph and table from the tagged candidate's "+
			"recorded evidence before the tag (docs/release.md §1, step 2)", releaseNotesDir, tag)
}

// releaseInterimPages lists the pages releaseInterimMarkers names other than the tag's notes,
// sorted, so a page added to the table is checked without a second list to keep in step.
func releaseInterimPages() []string {
	var pages []string
	for page := range releaseInterimMarkers {
		if page != releaseNotesPage {
			pages = append(pages, page)
		}
	}
	sort.Strings(pages)
	return pages
}

// TestReleasePagesForAPushedTagCarryNoInterimText is the same check, on the tag push, for every
// other page step 2 rewrites (README.md, CHANGELOG.md, docs/release.md and docs/architecture.md's
// C1.16 paragraph among them): they ship in the tagged tree and may not still say the candidate's
// evidence is owed.
func TestReleasePagesForAPushedTagCarryNoInterimText(t *testing.T) {
	t.Parallel()

	refType := os.Getenv("GITHUB_REF_TYPE")
	root := repoRoot(t)
	for _, page := range releaseInterimPages() {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		require.NoError(t, err)
		for _, p := range releasePageInterimProblems(page, string(b), refType) {
			t.Errorf("%s; rewrite it from the tagged candidate's recorded evidence before the tag "+
				"(docs/release.md §1, step 2)", p)
		}
	}
}

// releasePrepared reports whether CHANGELOG.md already carries the version's own heading, which
// docs/release.md §1 step 2 writes in the same docs-only commit that rewrites the interim text.
func releasePrepared(changelog, version string) bool {
	return strings.Contains(changelog, "\n## ["+version+"]")
}

// releaseInterimMarkersMissing reports the markers of page that no longer match its text. A
// marker that matches nothing guards nothing on the tag push.
func releaseInterimMarkersMissing(page, text string) []string {
	flat := strings.Join(strings.Fields(text), " ")
	var missing []string
	for _, re := range releaseInterimMarkers[page] {
		if !re.MatchString(flat) {
			missing = append(missing, page+": the interim marker "+re.String()+" matches nothing")
		}
	}
	return missing
}

// TestReleaseInterimMarkersStillMatchTheirPages keeps releaseInterimMarkers live while the release
// is not yet prepared: every marker must still match its page. A reworded interim sentence would
// otherwise disable its marker silently, and the tag-push check would pass over text that is still
// interim. Once step 2 has written the version's CHANGELOG heading the pages are meant to be free
// of interim text, and the tag-push checks above take over.
func TestReleaseInterimMarkersStillMatchTheirPages(t *testing.T) {
	t.Parallel()

	if os.Getenv("GITHUB_REF_TYPE") == "tag" {
		return // on the tag push the markers must match nothing; the checks above assert that
	}
	root := repoRoot(t)
	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	require.NoError(t, err)
	if releasePrepared(string(changelog), core.Version) {
		return
	}
	pages := map[string]string{releaseNotesPage: releaseNotesDir + "/v" + core.Version + ".md"}
	for _, page := range releaseInterimPages() {
		pages[page] = page
	}
	for page, file := range pages {
		b, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		require.NoError(t, readErr, "%s: a page releaseInterimMarkers names must exist", file)
		for _, p := range releaseInterimMarkersMissing(page, string(b)) {
			t.Errorf("%s; update releaseInterimMarkers with the page's current interim sentence", p)
		}
	}
}

// TestReleaseInterimMarkersLivenessRejectsAReword is the negative of the liveness check.
func TestReleaseInterimMarkersLivenessRejectsAReword(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, releaseInterimMarkersMissing("docs/architecture.md",
		"candidate 8's night re-runs the rig"), "a reworded interim sentence must be reported")
	require.Empty(t, releaseInterimMarkersMissing("docs/architecture.md",
		"candidate 8's C5.2 night\nre-measures it (D62(c))"), "the current sentence must match")
	require.False(t, releasePrepared("# Changelog\n\n## [Unreleased]\n", "0.3.0"))
	require.True(t, releasePrepared("# Changelog\n\n## [0.3.0] - 2026-10-05\n", "0.3.0"))
}

// TestReleaseNotesInterimGuardFiresOnlyOnATagPush is the negative of the live check.
func TestReleaseNotesInterimGuardFiresOnlyOnATagPush(t *testing.T) {
	t.Parallel()

	const interim = "The figures in the table below are candidate 6's and candidate 7's and stand\n" +
		"until candidate 8's are recorded; the release is not published before they are."
	require.NotEmpty(t, releaseNotesInterimProblems(interim, "tag"),
		"a pushed tag's notes that still carry the interim sentence must fail")
	require.Empty(t, releaseNotesInterimProblems(interim, "branch"),
		"on a branch push the interim sentence is true of the tree")
	require.Empty(t, releaseNotesInterimProblems(interim, ""),
		"the local release-check rehearsal sets no GITHUB_REF_TYPE")
	require.Empty(t, releaseNotesInterimProblems("Candidate 8's night chain passed.", "tag"),
		"notes rewritten from the candidate's own evidence must pass")
}

// TestReleasePagesInterimGuardFiresOnlyOnATagPush is the negative of the live check on the pages
// besides the notes that step 2 rewrites.
func TestReleasePagesInterimGuardFiresOnlyOnATagPush(t *testing.T) {
	t.Parallel()

	interim := map[string]string{
		"README.md":            "Neither workflow has run on\ncandidate 8 yet.",
		"CHANGELOG.md":         "live re-check and C5.5 supply the release's evidence, and they are\nowed",
		"docs/release.md":      "recorded in `plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md` when it\nis frozen.",
		"docs/architecture.md": "candidate 8's C5.2 night\nre-measures it (D62(c)).",
	}
	for page, text := range interim {
		require.NotEmpty(t, releasePageInterimProblems(page, text, "tag"),
			"%s: a pushed tag whose page still carries interim text must fail", page)
		require.Empty(t, releasePageInterimProblems(page, text, "branch"),
			"%s: on a branch push the interim text is true of the tree", page)
		require.Empty(t, releasePageInterimProblems(page, text, ""),
			"%s: the local release-check rehearsal sets no GITHUB_REF_TYPE", page)
		require.Empty(t, releasePageInterimProblems(page, "Candidate 8's night chain passed.", "tag"),
			"%s: a page rewritten from the candidate's own evidence must pass", page)
	}
}
