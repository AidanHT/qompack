package guards

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	goModToolchain  = regexp.MustCompile(`(?m)^toolchain go(\S+)$`)
	workflowGoSetup = regexp.MustCompile(`go-version:\s*'([^']+)'`)
	workflowGoFile  = regexp.MustCompile(`go-version-file:`)
)

// TestWorkflowGoVersionMatchesToolchain fails when any workflow would install a Go other than the
// one go.mod pins.
//
// This exists because two weaker spellings both silently ran the wrong toolchain, and neither
// failed anything — which is the only reason it went unnoticed until a govulncheck report:
//
//   - `go-version: '1.26.x'` resolved to go1.26.5 against a go.mod pinning toolchain go1.26.6.
//     GOTOOLCHAIN=local, which every workflow sets, IGNORES the toolchain directive rather than
//     enforcing it — it only refuses a Go older than the `go` directive, and 1.26.5 satisfies
//     `go 1.26`. So the build ran happily on a Go still carrying four stdlib advisories.
//   - `go-version-file: 'go.mod'` is no better: setup-go reads the `go` directive out of it, not
//     `toolchain`, and resolves the same 1.26.5.
//
// Only the exact patch version works, so the exact patch version is what the workflows carry, and
// this test is what keeps it equal to go.mod. Bumping the toolchain means bumping both, and
// forgetting one fails here rather than in a vulnerability report months later.
func TestWorkflowGoVersionMatchesToolchain(t *testing.T) {
	repo := filepath.Join("..", "..")

	mod, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	require.NoError(t, err)
	m := goModToolchain.FindSubmatch(mod)
	require.NotNil(t, m, "go.mod must carry a toolchain directive for CI to pin against")
	want := string(m[1])

	dir := filepath.Join(repo, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	checked := 0
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		names = append(names, e.Name())
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, readErr)

		// Comment lines are skipped: the workflows explain in prose why go-version-file is wrong,
		// and a guard that fired on its own rationale would be unusable.
		var live []string
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				live = append(live, line)
			}
		}
		body := strings.Join(live, "\n")

		require.NotRegexp(t, workflowGoFile, body,
			"%s: go-version-file resolves the `go` directive, not `toolchain`, and installs the "+
				"wrong patch; pin the exact version instead", e.Name())

		found := workflowGoSetup.FindAllStringSubmatch(body, -1)
		require.NotEmpty(t, found, "%s: no go-version pin found", e.Name())
		for _, f := range found {
			require.Equal(t, want, f[1],
				"%s pins Go %s but go.mod's toolchain is go%s — CI would run a different Go than "+
					"the module declares, and GOTOOLCHAIN=local will not catch it",
				e.Name(), f[1], want)
			checked++
		}
	}
	sort.Strings(names)
	require.NotEmpty(t, names, "no workflows found — this guard would pass vacuously")
	t.Logf("%d go-version pins across %v all equal go.mod's toolchain go%s", checked, names, want)
}
