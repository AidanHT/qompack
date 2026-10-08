package guards

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A crashed test binary's evidence (V6 close-out D75(c)). Candidate 8's hosted ci.yml 37562946379
// attempt 1 ended internal/daemon on windows-latest at 1185 s with a runtime-class crash dump, and
// the job kept no line that said why: the reconciliation printed only the last lines of the
// package's output, which were goroutine stacks, and the event stream that held the cause died
// with the runner. So every workflow job that writes a whole-run test.json must (1) print, for a
// failed package, the crash blocks a tail cannot reach (a panic, a runtime fatal error, a -timeout
// kill) and the compiler errors of a failed build, and (2) upload test.json with its two failure
// lists when the job fails, under a name unique to the runner when the job is a matrix.
//
// The parse is line-based and reads the live text only (liveYAMLText), like every workflow guard
// here, so a comment that names a marker never satisfies the check.

// testJSONWorkflows are the workflows the guard reads.
var testJSONWorkflows = []string{"ci.yml", "nightly.yml", "release.yml", "marketplace.yml"}

var (
	// testJSONWriteRE is a shell redirect that writes test.json.
	testJSONWriteRE = regexp.MustCompile(`>\s*test\.json\b`)
	// testJSONIfRE is an upload step's condition that runs it when the job has failed.
	testJSONIfRE = regexp.MustCompile(`(?m)^\s*(- )?if:\s*.*\b(failure|always)\(\)`)
	// testJSONRetentionRE is a short artifact retention, in days.
	testJSONRetentionRE = regexp.MustCompile(`(?m)^\s*retention-days:\s*[1-9][0-9]?\s*$`)
	// testJSONNameRE is the upload's artifact name line.
	testJSONNameRE = regexp.MustCompile(`(?m)^\s*name:\s*(.+?)\s*$`)
)

// testJSONCrashMarkers are what the writing step must search a failed package's output for. The
// first three are the first lines of a crash; FailedBuild is the fail event's pointer to a build
// failure's compiler errors, which go test -json reports outside the package's output.
var testJSONCrashMarkers = []string{"panic: ", "fatal error: ", "test timed out", "FailedBuild"}

// jobBodyLines returns the lines of one job in a workflow's live text, without its header.
func jobBodyLines(live, job string) []string {
	var body []string
	in := false
	for _, line := range strings.Split(live, "\n") {
		if m := workflowJobHeaderRE.FindStringSubmatch(line); m != nil {
			in = m[1] == job
			continue
		}
		if in {
			body = append(body, line)
		}
	}
	return body
}

// testJSONEvidenceProblems reports what is wrong with the crash evidence of every job in one
// workflow's live text that writes test.json, and how many such jobs there are. An empty result
// is a pass.
func testJSONEvidenceProblems(live, file string) (problems []string, writers int) {
	for _, line := range strings.Split(live, "\n") {
		m := workflowJobHeaderRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		job := m[1]
		steps := jobStepBlocks(live, job)
		writeAt := -1
		for i, s := range steps {
			if testJSONWriteRE.MatchString(s) {
				writeAt = i
				break
			}
		}
		if writeAt < 0 {
			continue
		}
		writers++
		where := file + " job " + job
		write := steps[writeAt]
		for _, marker := range testJSONCrashMarkers {
			if !strings.Contains(write, marker) {
				problems = append(problems, where+": the step that writes test.json never searches a failed package's "+
					"output for "+strconv.Quote(marker)+", so that cause scrolls off a tail-only excerpt")
			}
		}
		var upload string
		for _, s := range steps[writeAt+1:] {
			if strings.Contains(s, "actions/upload-artifact@") && strings.Contains(s, "test.json") {
				upload = s
				break
			}
		}
		if upload == "" {
			problems = append(problems, where+": no actions/upload-artifact step after the run uploads test.json")
			continue
		}
		if !testJSONIfRE.MatchString(upload) {
			problems = append(problems, where+": the test.json upload has no if: failure() or always(), so it is "+
				"skipped exactly when the run failed")
		}
		for _, list := range []string{"failed-rows.txt", "failed-pkgs.txt"} {
			if strings.Contains(write, list) && !strings.Contains(upload, list) {
				problems = append(problems, where+": the step writes "+list+" but the upload leaves it out")
			}
		}
		if !testJSONRetentionRE.MatchString(upload) {
			problems = append(problems, where+": the test.json upload sets no short retention-days (1 to 99)")
		}
		matrix := strings.Contains(strings.Join(jobBodyLines(live, job), "\n"), "matrix.os")
		name := testJSONNameRE.FindStringSubmatch(upload)
		switch {
		case name == nil:
			problems = append(problems, where+": the test.json upload has no artifact name")
		case matrix && !strings.Contains(name[1], "matrix.os"):
			problems = append(problems, where+": the job is an OS matrix but its test.json artifact name "+
				name[1]+" does not name the runner, so the legs' uploads collide")
		}
	}
	return problems, writers
}

// TestTestJSONWritersKeepTheirCrashEvidence pins the live workflows: every job that writes a
// test.json prints a crash's first lines and uploads the stream when it fails. ci.yml's whole-tree
// `test` job is the one writer today, so the guard also requires that it finds at least one.
func TestTestJSONWritersKeepTheirCrashEvidence(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	total := 0
	for _, file := range testJSONWorkflows {
		problems, writers := testJSONEvidenceProblems(liveYAMLText(t, filepath.Join(dir, file)), file)
		require.Empty(t, problems, "%s: a job that writes test.json must keep a crash's cause (D75(c))", file)
		total += writers
	}
	require.Positive(t, total, "no workflow job writes test.json; if the whole-tree run moved, move this guard with it")
}

// TestTestJSONEvidenceGuardRejectsReshapedJobs is the negative of the live check: each piece of
// the evidence, removed or reshaped, must fail it.
func TestTestJSONEvidenceGuardRejectsReshapedJobs(t *testing.T) {
	t.Parallel()

	const head = "jobs:\n  test:\n    strategy:\n      matrix:\n        os: [ubuntu-latest, windows-latest]\n" +
		"    runs-on: ${{ matrix.os }}\n    steps:\n      - uses: actions/checkout@v4\n"
	const markers = "          grep -E '^(panic: |fatal error: )|test timed out after ' pkg-output.txt\n" +
		"          sed -n 's/.*\"FailedBuild\":\"\\([^\"]*\\)\".*/\\1/p'\n"
	const run = "      - name: test\n        shell: bash\n        run: |\n" +
		"          go test -json ./... > test.json\n" +
		"          grep fail test.json > failed-rows.txt\n          grep fail test.json > failed-pkgs.txt\n"
	const upload = "      - name: upload\n        if: failure()\n        uses: actions/upload-artifact@v4\n" +
		"        with:\n          name: test-json-${{ matrix.os }}\n          path: |\n            test.json\n" +
		"            failed-rows.txt\n            failed-pkgs.txt\n          retention-days: 7\n"
	const tail = "  docs:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"

	check := func(text string) []string {
		problems, writers := testJSONEvidenceProblems(text, "ci.yml")
		require.Equal(t, 1, writers, "the fixture has exactly one writer")
		return problems
	}

	require.Empty(t, check(head+run+markers+upload+tail), "the complete shape passes")
	require.Empty(t, check(head+run+markers+strings.Replace(upload, "if: failure()", "if: always()", 1)+tail),
		"always() keeps the evidence too")

	for _, tc := range []struct{ name, text string }{
		{"no crash markers", head + run + upload + tail},
		{"no panic marker", head + run + strings.Replace(markers, "panic: |", "", 1) + upload + tail},
		{"no build-failure marker", head + run + strings.Replace(markers, "FailedBuild", "Failed", 1) + upload + tail},
		{"no upload", head + run + markers + tail},
		{"upload before the run", head + upload + run + markers + tail},
		{"upload without a condition", head + run + markers + strings.Replace(upload, "        if: failure()\n", "", 1) + tail},
		{"upload on success only", head + run + markers + strings.Replace(upload, "if: failure()", "if: success()", 1) + tail},
		{"upload drops failed-pkgs.txt", head + run + markers + strings.Replace(upload, "            failed-pkgs.txt\n", "", 1) + tail},
		{"upload without retention", head + run + markers + strings.Replace(upload, "          retention-days: 7\n", "", 1) + tail},
		{"one name for every leg", head + run + markers + strings.Replace(upload, "test-json-${{ matrix.os }}", "test-json", 1) + tail},
		{"upload in another job", head + run + markers + tail + upload},
	} {
		require.NotEmpty(t, check(tc.text), "%s must fail the guard", tc.name)
	}
}
