package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// gen-command-docs' own tests, in the shape genmcpdocs_test.go uses: the package-level `root` is
// set here and restored in t.Cleanup, because devtool's tests share it and never run in parallel.
//
// docs/commands.md is not documentation in the ordinary sense either. It is the one page that
// states what the plugin can actually do, and the CI `docs` job runs the --check half; these rows
// make sure the --check half can fail, and that the page cannot advertise a command the binary
// does not route.

// section75Order is the §7.5 order of the shipped commands, written out rather than read from
// commands.Names(), so this file compares the surface against the design document instead of
// against itself. §7.5's checkpoint is not shipped: its only route is the PreCompact hook.
var section75Order = []string{"status", "recall", "pin", "why", "dropped", "eval"}

// TestGenCommandDocs_RendersEverySection75Command keeps the page complete.
func TestGenCommandDocs_RendersEverySection75Command(t *testing.T) {
	got, err := renderCommandDoc()
	require.NoError(t, err)

	page := string(got)
	for _, name := range section75Order {
		require.Contains(t, page, "## `/qompack:"+name+"`", "%s has no section", name)
	}

	// In order, so a reordering of the spec table is caught rather than absorbed.
	at := -1
	for _, name := range section75Order {
		i := strings.Index(page, "## `/qompack:"+name+"`")
		require.Greater(t, i, at, "%s is out of §7.5 order", name)
		at = i
	}
}

// TestGenCommandDocs_MatchesTheInstalledHelp is the SP14-M7-04 gate: the page, the binary's help
// and the installed manifest are one interface described three times, and only this compares them.
func TestGenCommandDocs_MatchesTheInstalledHelp(t *testing.T) {
	got, err := renderCommandDoc()
	require.NoError(t, err)
	page := string(got)

	installed := make(map[string]pluginmanifest.CommandDoc)
	for _, c := range pluginmanifest.Default(core.Version).Commands {
		installed[c.Name] = c
	}

	for _, s := range commands.Specs() {
		var help strings.Builder
		require.NoError(t, s.WriteHelp(&help))
		require.Contains(t, page, help.String(), "%s: the page must carry the binary's own help", s.Name)

		require.Contains(t, page, installed[s.Name].Description,
			"%s: the page must carry the installed description", s.Name)
		require.Contains(t, page, installed[s.Name].AllowedTools,
			"%s: the page must state the allowed-tools grant", s.Name)
	}
}

// TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand is the "unsafe unsupported features not
// advertised" rule.
//
// Every command the page lists is one the plugin installs and the binary routes, under the
// subcommand the page names. /qompack:checkpoint used to be listed as "not yet routed" beside six
// that worked, while the installed command shelled out to the PreCompact hook and wrote nothing. It
// is no longer shipped, so the unrouted rendering is retired with it: the page must neither list it
// nor carry the marker, and it must say where checkpoints come from instead.
func TestGenCommandDocs_DoesNotAdvertiseAnUnroutedCommand(t *testing.T) {
	got, err := renderCommandDoc()
	require.NoError(t, err)
	page := string(got)

	for _, s := range commands.Specs() {
		require.Contains(t, page, "| `/qompack:"+s.Name+"` | `qompack "+s.Subcommand+"` |",
			"%s is routed and must be listed as such", s.Name)
	}
	require.NotContains(t, page, "not yet routed", "the page must not list a command with no route")
	require.NotContains(t, page, "/qompack:checkpoint", "the plugin ships no checkpoint command")
	require.Contains(t, page, "writes a checkpoint automatically before\nevery compaction",
		"the page must say where checkpoints come from, since there is no command for one")
}

// TestGenCommandDocs_CommittedPageIsCurrent is the --check half, run here so a stale page fails
// the ordinary test run and not only the CI docs job.
func TestGenCommandDocs_CommittedPageIsCurrent(t *testing.T) {
	prev := root
	t.Cleanup(func() { root = prev })
	root = repoRootForTest(t)

	want, err := renderCommandDoc()
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(commandDocPath)))
	require.NoError(t, err,
		"%s must be committed; run `go run ./tools/devtool gen-command-docs`", commandDocPath)
	require.Equal(t, string(want), string(normalizeNewlines(got)),
		"%s is stale; run `go run ./tools/devtool gen-command-docs`", commandDocPath)
}

// TestGenCommandDocs_RejectsAMismatchedManifest proves the generator's own cross-check can fail,
// rather than trusting whichever of the two tables it happened to read.
//
// A generator that rendered one table and ignored the other would document a surface the plugin
// does not install. That page would be wrong in the direction that gets believed, so the check has
// to be real and it has to be exercised.
func TestGenCommandDocs_RejectsAMismatchedManifest(t *testing.T) {
	specs := commands.Specs()
	full := make(map[string]pluginmanifest.CommandDoc)
	for _, c := range pluginmanifest.Default(core.Version).Commands {
		full[c.Name] = c
	}

	// The real pair renders.
	_, err := renderCommandDocFrom(specs, full)
	require.NoError(t, err)

	// A manifest missing a command the binary offers.
	short := make(map[string]pluginmanifest.CommandDoc, len(full))
	for k, v := range full {
		short[k] = v
	}
	delete(short, "status")
	_, err = renderCommandDocFrom(specs, short)
	require.Error(t, err)
	require.Contains(t, err.Error(), "installed command")

	// A manifest whose description disagrees with the binary's help.
	drifted := make(map[string]pluginmanifest.CommandDoc, len(full))
	for k, v := range full {
		drifted[k] = v
	}
	changed := drifted["status"]
	changed.Description = "something else entirely"
	drifted["status"] = changed
	_, err = renderCommandDocFrom(specs, drifted)
	require.Error(t, err)
	require.Contains(t, err.Error(), "the manifest installs")
}

// repoRootForTest walks up from the test's working directory to the module root.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "no go.mod above %s", dir)
		dir = parent
	}
}
