package guards

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// nightlyFuzzMatrixRE extracts the {pkg, fn} pairs from the nightly workflow's fuzz matrix. Parsing
// the YAML with a real parser would mean adding a dependency to the guard suite for twenty lines of
// flow mapping; the entries are written one per line in a fixed shape, and a change that broke this
// regexp would be a change that also wants a human to look at this test.
var nightlyFuzzMatrixRE = regexp.MustCompile(`(?m)^\s*-\s*\{\s*pkg:\s*(\S+?),\s*fn:\s*(\w+)\s*\}`)

// nightlyFuzzMatrixLen is the number of rows .github/workflows/nightly.yml's fuzz matrix declares:
// nineteen targets that exist in the tree today plus internal/checkpoint's FuzzCheckpointJSON,
// which SP-10 owns and wave 4 writes.
//
// The assertion exists because a matrix repair can be made by RENAMING rows instead of adding them,
// and a rename leaves the count untouched. Eight was the V1 number and survived the whole of wave 1
// unchanged while fifteen shipped targets went unregistered; that is what this constant is for.
const nightlyFuzzMatrixLen = 20

// nightlyFuzzLandedSubplans is a transcription of tools/devtool/cover.go's landedSubplans, and must
// be kept identical to it: a subplan adds itself there in the commit that lands it, and the same
// line belongs here in the same commit.
//
// It is transcribed rather than imported because tools/devtool is package main and no test can
// import it — the same constraint that makes v1_integration_test.go's v1CoverageFloors a
// transcription of 00-ARCHITECTURE.md §6.4 rather than a read of plans/OWNERS.tsv.
// TestNightlyFuzz_LandedSubplansMirrorsCoverGo below is the mechanical half of the pin: it reads
// cover.go's literal and fails when the two sets drift, so the transcription cannot rot silently
// the way a comment alone would let it.
var nightlyFuzzLandedSubplans = map[string]bool{
	"SP-01": true,
	"SP-02": true,
	"SP-03": true,
	"SP-04": true,
	"SP-05": true,
	"SP-06": true,
	"SP-07": true,
	"SP-08": true,
	"SP-09": true,
}

// TestNightlyFuzzMatrix keeps .github/workflows/nightly.yml honest about what it actually fuzzes.
//
// The workflow reports and skips a target it cannot find rather than failing the leg, because a
// matrix that names the finished tree's targets necessarily names some nobody has written yet.
// This test bounds that skip, and the bound is mechanical rather than a judgement call:
//
//   - A target that EXISTS satisfies the matrix whoever owns its package. The nightly leg will
//     run it, which is the whole point; there is nothing left to waive.
//   - A MISSING target is waivable only while its owning subplan (plans/OWNERS.tsv) has not
//     landed. Once the owner is in nightlyFuzzLandedSubplans the waiver is gone and the row is a
//     hard failure: the package is real code, the matrix claims it is fuzzed, and the nightly leg
//     would print a warning nobody reads.
//
// The second clause used to read `require.NotEqual(t, "SP-01", owner)` — SP-01 was the only subplan
// that had landed, so "owned by anyone else" and "still a stub" were the same predicate. Wave 1
// separated them and the test did not notice: on develop it waived internal/redact with the message
// "redact is owned by SP-06 and is still a stub" while SP-06's redact was shipped, real, and
// exporting two fuzz targets the matrix had never heard of. A waiver whose stated reason is false
// is worse than a missing check, because it reads in the job log as a decision somebody made.
//
// The first clause was itself a repair, and must not be undone. It was originally the reverse — a
// waived package whose target had appeared failed as a stale waiver, on the theory that the waiver
// list would then shrink as owners landed. SP-04 showed the theory was wrong. It landed
// internal/chunk with FuzzSplit and internal/canon with FuzzCanonicalize, and the only remedies
// that rule offered were to move those packages to SP-01 in OWNERS.tsv or to drop the targets from
// the matrix. Both are worse than the problem. The owner column records who WROTE a package, and
// `devtool lint`'s stubskips sub-check reads it to decide that a Rule W-1 behaviour skip is a merge
// blocker — so relabelling chunk as SP-01-owned would turn chunktest's own deliberate stub-suite
// skip into a hard failure. And dropping a target that now exists would stop the nightly leg
// fuzzing real code.
//
// What this still cannot catch is the other direction: a Fuzz* function that ships with no matrix
// row at all. `go list` cannot be asked for fuzz targets across a tree cheaply enough to run here,
// so that half belongs to the wave checkpoint, which re-runs the inventory in both directions
// (V2-VERIFY §2.0, V2-MERGE-01).
func TestNightlyFuzzMatrix(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "nightly.yml"))
	require.NoError(t, err)

	matches := nightlyFuzzMatrixRE.FindAllStringSubmatch(string(b), -1)
	require.NotEmpty(t, matches, "no fuzz matrix entries parsed out of nightly.yml")
	require.Len(t, matches, nightlyFuzzMatrixLen,
		"the fuzz matrix should declare %d targets", nightlyFuzzMatrixLen)

	owners := ownersByPackage(t, root)

	for _, m := range matches {
		pkgPath, fn := m[1], m[2]
		t.Run(pkgKey(pkgPath)+"_"+fn, func(t *testing.T) {
			t.Parallel()

			pkgName := pkgKey(pkgPath)
			owner, known := owners[pkgName]
			require.True(t, known, "nightly.yml fuzzes %s, which has no plans/OWNERS.tsv row", pkgPath)

			if fuzzTargetExists(t, root, pkgPath, fn) {
				t.Logf("live: %s declares %s; the nightly leg fuzzes it for real", pkgPath, fn)
				return
			}

			require.False(t, nightlyFuzzLandedSubplans[owner],
				"nightly.yml declares fuzz target %s in %s, which %s owns and has landed, but the "+
					"target does not exist — the nightly leg would warn and exit 0, so this row "+
					"fuzzes nothing. Write %s, or drop the row from the matrix.",
				fn, pkgPath, owner, fn)

			t.Logf("waived: %s has not landed (%s owns %s); %s is owed when %s lands",
				pkgName, owner, pkgName, fn, owner)
		})
	}
}

// coverGoLandedSubplansRE pulls the SP-NN keys out of tools/devtool/cover.go's landedSubplans map
// literal. It is anchored on the literal's own opening line so a second map in the file cannot be
// mistaken for it, and the surrounding require.NotEmpty makes a regexp that has stopped matching a
// failure rather than a vacuous pass.
var coverGoLandedSubplansRE = regexp.MustCompile(`(?s)var landedSubplans = map\[string\]bool\{(.*?)\n\}`)

var coverGoSubplanKeyRE = regexp.MustCompile(`"(SP-\d+)":\s*true`)

// TestNightlyFuzz_LandedSubplansMirrorsCoverGo pins nightlyFuzzLandedSubplans to the set
// tools/devtool/cover.go actually enforces.
//
// Two consumers reading the same "has this subplan landed?" fact out of two hand-maintained lists
// is exactly the shape V2-MERGE-12 exists to check, and a drifted copy here fails OPEN: a subplan
// that landed in cover.go but not here goes on being waived, and its declared fuzz targets go on
// not existing, silently. Reading cover.go's literal is not tautological — this test asserts the
// two agree, and cover.go's own TestLandedSubplansMatchesTheBranch is what asserts either is right.
func TestNightlyFuzz_LandedSubplansMirrorsCoverGo(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "tools", "devtool", "cover.go"))
	require.NoError(t, err)

	block := coverGoLandedSubplansRE.FindSubmatch(b)
	require.NotNil(t, block,
		"could not find `var landedSubplans = map[string]bool{...}` in tools/devtool/cover.go; "+
			"if it was renamed, update this guard rather than deleting it")

	fromCover := map[string]bool{}
	for _, k := range coverGoSubplanKeyRE.FindAllSubmatch(block[1], -1) {
		fromCover[string(k[1])] = true
	}
	require.NotEmpty(t, fromCover, "parsed no SP-NN keys out of cover.go's landedSubplans")

	require.Equal(t, fromCover, nightlyFuzzLandedSubplans,
		"tools/devtool/cover.go's landedSubplans and this file's transcription of it disagree. "+
			"A subplan adds itself to both in the commit that lands it: cover.go's copy turns on "+
			"its §6.4 coverage floor, this one turns off its nightly-fuzz waiver.")
}

// pkgKey maps a matrix package path such as "./internal/canon" to the plans/OWNERS.tsv key
// "canon". Package paths outside internal/ come back unchanged and fail the OWNERS.tsv lookup,
// which is the correct answer: the matrix has no business naming one.
func pkgKey(pkgPath string) string {
	return strings.TrimPrefix(pkgPath, "./internal/")
}

// fuzzTargetExists asks the toolchain whether pkgPath declares a fuzz target named fn, using the
// same `go test -list` probe the workflow uses so the two cannot disagree about what "exists"
// means. A package that fails to build reports false, which is the conservative answer: that
// failure surfaces from `go build ./...`, not from here.
func fuzzTargetExists(t *testing.T, root, pkgPath, fn string) bool {
	t.Helper()

	cmd := exec.Command("go", "test", "-run", "^$", "-list", "^"+fn+"$", pkgPath)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == fn {
			return true
		}
	}
	return false
}

// ownersByPackage reads plans/OWNERS.tsv into a package-name to owning-subplan map.
func ownersByPackage(t *testing.T, root string) map[string]string {
	t.Helper()

	f, err := os.Open(filepath.Join(root, "plans", "OWNERS.tsv"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	owners := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "package" {
			continue
		}
		owners[fields[0]] = strings.TrimSpace(fields[1])
	}
	require.NoError(t, sc.Err())
	return owners
}
