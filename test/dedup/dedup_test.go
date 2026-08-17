package dedup

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeReport is the ONLY writer of testdata/canon-dedup-report.json, mirroring how -update works
// for every golden in this repository: an ordinary test run never touches testdata/.
var writeReport = flag.Bool("write-report", false,
	"rewrite testdata/canon-dedup-report.json from the corpus instead of comparing against it")

// reportPerm is the mode a rewritten report is created with: an ordinary readable data file, not
// the 0o600 the runtime store uses, because the report is committed source.
const reportPerm = 0o644

// testrunnerGainFloor is the gap O2 must show on test-output-heavy content.
//
// Qompack.md §8.1: "Test and build output is the noisiest content class in a coding session; this
// is where the dedup ratio is won or lost." A 25% improvement is the number SP-08 cites when it
// argues that canonicalization pays for itself, and it is deliberately a floor on the RATIO OF
// RATIOS rather than on the ratio itself: the absolute ratio depends on how repetitive the corpus
// happens to be, but the gap between chunking canonical text and chunking raw text is a property
// of the canonicalizers.
//
// If this fails, the corpus is under-exercising O2 — add another rerun pair (a suite differing
// from its sibling by one failure and its durations) rather than lowering the floor.
const testrunnerGainFloor = 1.25

// overallGainFloor states the weaker whole-corpus claim: canonicalization is never WORSE than raw.
// Some groups (a single web page, one glob listing) have nothing to collapse and legitimately show
// no gain; none of them may show a loss.
const overallGainFloor = 1.0

// fileReadRatioFloor asserts the two-version file-read pair actually shares chunks. It is the
// narrowest, most falsifiable claim in this file: read-auth-ts.txt and read-auth-ts-v2.txt differ
// by two lines, so content-defined chunking must collapse the rest (Qompack.md §6.1, "the same
// file read four times with two lines changed").
const fileReadRatioFloor = 1.0

// TestDedupRatio_WithVsWithout is the measurement Qompack.md §10 Phase 1 asks for: "measure with
// and without canonicalization".
func TestDedupRatio_WithVsWithout(t *testing.T) {
	root := repoRoot(t)

	rep, err := Measure(root)
	require.NoError(t, err)
	require.NotEmpty(t, rep.Groups, "the corpus produced no groups")

	byGroup := map[string]GroupStat{}
	for _, g := range rep.Groups {
		byGroup[g.Group] = g
		t.Logf("%-12s files=%2d raw=%7d canon=%7d ratio_without=%.4f ratio_with=%.4f gain=%.4f",
			g.Group, g.Files, g.RawBytes, g.CanonBytes, g.RatioWithout, g.RatioWith, g.Gain)
	}
	t.Logf("%-12s ratio_without=%.4f ratio_with=%.4f gain=%.4f",
		"OVERALL", rep.Overall.RatioWithout, rep.Overall.RatioWith, rep.Overall.Gain)

	tr, ok := byGroup["testrunner"]
	require.True(t, ok, "the corpus must contain a testrunner group — it is where O2 is won or lost")
	require.GreaterOrEqual(t, tr.Gain, testrunnerGainFloor,
		"canonicalization must improve the test-output dedup ratio by at least %.2fx (Qompack.md §8.1)",
		testrunnerGainFloor)

	require.GreaterOrEqual(t, rep.Overall.Gain, overallGainFloor,
		"canonicalization must never make the overall dedup ratio worse")

	fr, ok := byGroup["fileread"]
	require.True(t, ok, "the corpus must contain a fileread group")
	require.Greater(t, fr.RatioWith, fileReadRatioFloor,
		"two versions of the same file differing by two lines must share chunks (Qompack.md §6.1)")

	if !*writeReport {
		return
	}
	encoded, err := Encode(rep)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(reportPath)), encoded, reportPerm))
	t.Logf("wrote %s", reportPath)
}

// TestDedupReport_Written asserts the committed report still matches what the corpus actually
// measures, byte for byte.
//
// It recomputes rather than trusting the file, so the report can never drift from the corpus: a
// corpus file edited without rerunning -write-report, or a canonicalizer tuned without
// re-measuring, fails here rather than leaving SP-08 citing a stale number.
func TestDedupReport_Written(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, filepath.FromSlash(reportPath))

	committed, err := os.ReadFile(path) //nolint:gosec // a path assembled from this repo's own testdata tree
	require.NoError(t, err, "%s is missing; regenerate it with -args -write-report", reportPath)

	rep, err := Measure(root)
	require.NoError(t, err)
	encoded, err := Encode(rep)
	require.NoError(t, err)

	require.Equal(t, normalize(committed), normalize(encoded),
		"the committed report no longer matches the corpus; rerun with -args -write-report")
}

// normalize strips CRLF so the comparison survives a Windows checkout. The file is always WRITTEN
// with LF, so a -write-report run produces identical bytes on every platform.
func normalize(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// repoRoot resolves the module root via `go list`, so the harness works regardless of the
// directory the test binary was started from.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
