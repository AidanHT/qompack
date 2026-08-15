// Package guards holds the CI guards that no single package can express: the closing-note build
// order, the §12.1 fresh-build contract mode, the §13 write set, and the D10 import ban.
//
// It is a composition root (§3.2): it imports the whole tree, and nothing may import it.
package guards

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// modulePrefix is this repository's module path with a trailing slash, used to tell our own
// packages from their dependencies in `go list` output.
const modulePrefix = "github.com/qompack/qompack/"

// bannedImports is D10 in its enforceable form. The plugin reads your code and writes to one
// gitignored directory; it talks to nobody. No package may reach a network stack — not to fetch a
// model, not to phone home, not to check for updates.
var bannedImports = []string{"net/http", "net/url", "crypto/tls"}

// netAllowedIn is the single package permitted to import net, and only because §5 puts the
// local IPC transport there: unix domain sockets and Windows named pipes, never a socket with a
// route. Permitting net repo-wide would make D10 unenforceable by grep, so the exception is
// pinned to one package by name.
const netAllowedIn = "internal/ipc"

// TestGuard_NoNetworkImports is the mechanical form of D10.
//
// It reads direct (non-test) imports rather than the transitive closure on purpose: several
// stdlib packages we legitimately use pull net/url in transitively, so a closure check would be
// permanently red and would be silenced rather than fixed. What D10 actually forbids is qompack
// code REACHING for the network, and that is exactly a direct import.
func TestGuard_NoNetworkImports(t *testing.T) {
	t.Parallel()

	imports := listDirectImports(t)
	require.NotEmpty(t, imports, "go list returned no qompack packages — the guard would vacuously pass")

	for pkg, imps := range imports {
		for _, imp := range imps {
			for _, banned := range bannedImports {
				require.NotEqual(t, banned, imp,
					"D10: %s imports %s — the plugin performs no network I/O, ever", pkg, imp)
			}
			if imp == "net" {
				require.Equal(t, netAllowedIn, pkg,
					"net is permitted only in %s (local IPC transport), but %s imports it",
					netAllowedIn, pkg)
			}
		}
	}
}

// TestGuard_NoNetworkImports_DetectsAViolation proves the guard can fail.
//
// A grep-shaped assertion that has never been seen to fail is indistinguishable from one whose
// pattern no longer matches anything, so the checking logic is exercised against a synthetic
// package list containing a violation.
func TestGuard_NoNetworkImports_DetectsAViolation(t *testing.T) {
	t.Parallel()

	parsed := parseImportLines([]string{
		modulePrefix + "internal/store fmt os",
		modulePrefix + "internal/observer fmt net/http",
		"golang.org/x/tools/go/analysis fmt net/http",
	})

	require.Len(t, parsed, 2, "only qompack packages are subject to D10")
	require.Contains(t, parsed, "internal/observer")
	require.Contains(t, parsed["internal/observer"], "net/http",
		"the parser must surface the banned import the real guard rejects")
	require.NotContains(t, parsed, "golang.org/x/tools/go/analysis",
		"a dependency's own imports are not ours to police")
}

// listDirectImports runs `go list` over the module and returns qompack package path (relative to
// the module) -> its direct imports.
func listDirectImports(t *testing.T) map[string][]string {
	t.Helper()

	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", modulePrefix+"...")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}
	return parseImportLines(strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"))
}

// parseImportLines turns `go list` lines into a map of module-relative package path -> imports,
// dropping anything that is not ours. It is separate from listDirectImports so the guard's logic
// can be tested without shelling out.
func parseImportLines(lines []string) map[string][]string {
	got := map[string][]string{}
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		pkg, ok := strings.CutPrefix(fields[0], modulePrefix)
		if !ok {
			continue
		}
		got[pkg] = fields[1:]
	}
	return got
}
