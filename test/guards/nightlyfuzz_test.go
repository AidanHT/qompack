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
// the YAML with a real parser would mean adding a dependency to the guard suite for eight lines of
// flow mapping; the entries are written one per line in a fixed shape, and a change that broke this
// regexp would be a change that also wants a human to look at this test.
var nightlyFuzzMatrixRE = regexp.MustCompile(`(?m)^\s*-\s*\{\s*pkg:\s*(\S+?),\s*fn:\s*(\w+)\s*\}`)

// TestNightlyFuzzMatrix keeps .github/workflows/nightly.yml honest about what it actually fuzzes.
//
// At V1 the matrix declares eight targets and two exist: FuzzConfigLoad and FuzzReadEvent. The
// other six name packages plans/OWNERS.tsv assigns to a later subplan, so they are stubs — writing
// their fuzz targets now would mean fuzzing a placeholder. The workflow reports and skips a target
// it cannot find rather than failing the leg, and this test bounds that skip.
//
// The waiver is mechanical, not a judgement call, and reads from the same OWNERS.tsv column that
// `devtool cover` and `gen-contract-fixtures` already use: a missing target is excused only while
// its package belongs to a subplan other than SP-01. Two things follow, and both matter more than
// the waiver itself:
//
//   - An SP-01 package must have every target the matrix claims for it, today.
//   - A waived package whose target has since appeared fails as a stale waiver, so the list
//     shrinks as owners land rather than being trimmed by someone remembering to.
//
// What this cannot catch is SP-04 landing internal/chunk without writing FuzzSplit; that belongs
// to SP-04's own definition of done and to the V2 checkpoint, which re-runs this inventory.
func TestNightlyFuzzMatrix(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "nightly.yml"))
	require.NoError(t, err)

	matches := nightlyFuzzMatrixRE.FindAllStringSubmatch(string(b), -1)
	require.NotEmpty(t, matches, "no fuzz matrix entries parsed out of nightly.yml")
	require.Len(t, matches, 8, "the fuzz matrix should declare eight targets")

	owners := ownersByPackage(t, root)

	for _, m := range matches {
		pkgPath, fn := m[1], m[2]
		t.Run(fn, func(t *testing.T) {
			t.Parallel()

			pkgName := strings.TrimPrefix(pkgPath, "./internal/")
			owner, known := owners[pkgName]
			require.True(t, known, "nightly.yml fuzzes %s, which has no plans/OWNERS.tsv row", pkgPath)

			exists := fuzzTargetExists(t, root, pkgPath, fn)

			if owner == "SP-01" {
				require.True(t, exists,
					"nightly.yml declares fuzz target %s in %s, which SP-01 owns, but the target "+
						"does not exist — the nightly leg would skip silently. Write %s, or drop "+
						"it from the matrix.", fn, pkgPath, fn)
				return
			}

			require.False(t, exists,
				"%s now exists in %s, so the waiver that let the nightly matrix skip it is stale. "+
					"Move %s to SP-01 ownership in plans/OWNERS.tsv, or reassign this target.",
				fn, pkgPath, pkgName)

			t.Logf("waived: %s is owned by %s and is still a stub; %s is owed when %s lands",
				pkgName, owner, fn, owner)
		})
	}
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
