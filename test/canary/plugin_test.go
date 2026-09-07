package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// binaryRef is how every generated plugin file must refer to the platform binary. The host expands
// ${CLAUDE_PLUGIN_ROOT} to the installed plugin directory, so a command that hard-coded a path — or
// dropped the expansion — would work in this checkout and fail on every installed copy.
const binaryRef = "${CLAUDE_PLUGIN_ROOT}/bin/qompack"

// TestCanary_PluginValidate is M0-03's "Validate the installed package using the Claude CLI's
// documented plugin-validation command", and M0-G4's requirement that repository-only validation be
// LABELLED as such.
//
// The distinction the record carries is the whole point. `go run ./tools/devtool plugin-validate`
// checks that the committed plugin/ tree matches what internal/pluginmanifest generates: a
// self-consistency check, complementary to host validation and not a substitute for it. `claude
// plugin validate` is the HOST's own opinion of the same directory. Neither is an installed-plugin
// canary — the directory is validated where it sits, not where a host would load it from — and the
// reason says so.
func TestCanary_PluginValidate(t *testing.T) {
	tgt := hostTarget(t)
	rec := Record{Name: "plugin_validate", Capability: contract.CapObservation, Scope: ScopeInstalledCLI, Target: tgt}

	bin, err := exec.LookPath("claude")
	if err != nil {
		skipRecorded(t, rec, "no `claude` executable on PATH, so the host's own plugin validator "+
			"could not be run; the repository validator (`devtool plugin-validate`) is "+
			"complementary and does not certify installed packaging")
	}

	root := repoRoot(t)
	dir := filepath.Join(root, "plugin")
	require.DirExists(t, dir)

	// redact removes this checkout's absolute location from anything the CLI echoes back. The
	// validator names the directory it was pointed at in every message, and that directory lives
	// under the user's home — which this package's privacy boundary excludes from a retained
	// artifact. Both the plain and the JSON-escaped spelling are replaced, because the CLI emits
	// each in different fields.
	redact := func(s string) string {
		s = strings.ReplaceAll(s, root, "<repo>")
		s = strings.ReplaceAll(s, strings.ReplaceAll(root, `\`, `\\`), "<repo>")
		return s
	}

	type run struct {
		label string
		args  []string
		ok    bool
		out   string
	}
	runs := []run{{label: "plain"}, {label: "strict"}}
	for i := range runs {
		args := []string{"plugin", "validate", dir, "--json"}
		if runs[i].label == "strict" {
			args = append(args, "--strict")
		}
		runs[i].args = args

		ctx, cancel := context.WithTimeout(context.Background(), claudeProbeBound)
		out, runErr := exec.CommandContext(ctx, bin, args...).CombinedOutput() //nolint:gosec // G204: fixed subcommand over this repository's own plugin directory
		cancel()

		runs[i].out = string(out)
		// The CLI's own JSON verdict is authoritative; a non-zero exit with a parseable "success"
		// field is still an answer, so the exit code alone is not read as the result.
		var verdict struct {
			Success bool `json:"success"`
		}
		if json.Unmarshal(out, &verdict) == nil {
			runs[i].ok = verdict.Success
		} else {
			runs[i].ok = runErr == nil
		}
	}

	// The CLI's raw output is kept beside the record, so a later reader can see what the host
	// actually said rather than only this test's summary of it.
	artifact := filepath.Join(artifactDir(t), "plugin_validate.claude.txt")
	var buf strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&buf, "$ claude %s\n%s\n", redact(strings.Join(r.args, " ")), redact(r.out))
	}
	require.NoError(t, os.WriteFile(paths.Long(artifact), []byte(buf.String()), 0o600))

	version, _ := claudeVersion()
	rec.Artifact = artifact
	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("`claude plugin validate <repo>/plugin --json` (plain: %t, --strict: %t) "+
		"under Claude CLI %s. REPOSITORY-LEVEL ONLY: the committed plugin directory was validated "+
		"where it sits. The plugin is not installed in this host, so installed manifest resolution, "+
		"${CLAUDE_PLUGIN_ROOT} expansion and launcher discovery remain unverified (B01).",
		runs[0].ok, runs[1].ok, version)
	for _, r := range runs {
		if !r.ok {
			rec.Outcome = OutcomeFailed
			rec.Reason = "the host's own plugin validator rejected the committed plugin directory (" +
				r.label + "); see the artifact"
		}
	}
	writeRecord(t, rec)

	for _, r := range runs {
		require.True(t, r.ok, "claude %s rejected %s; output:\n%s", strings.Join(r.args, " "), dir, r.out)
	}
}

// TestCanary_PackagingShape is the repository half of M0-G4: the committed plugin/hooks/hooks.json
// declares exactly the events internal/pluginmanifest declares, every command it registers goes
// through ${CLAUDE_PLUGIN_ROOT}/bin/qompack, and every entry carries a timeout.
//
// It reads the committed JSON rather than the generator's output, deliberately. The generator
// agreeing with itself proves nothing about the bytes a host would load; what is on disk is what
// would be installed.
func TestCanary_PackagingShape(t *testing.T) {
	tgt := hostTarget(t)
	rec := Record{Name: "packaging_shape", Capability: contract.CapObservation, Scope: ScopeRepository, Target: tgt}

	raw, err := os.ReadFile(paths.Long(filepath.Join(repoRoot(t), "plugin", "hooks", "hooks.json")))
	require.NoError(t, err, "the committed hooks.json is what a host would load")

	var onDisk pluginmanifest.HooksJSON
	require.NoError(t, json.Unmarshal(raw, &onDisk))

	want := pluginmanifest.Default(core.Version).Hooks

	// 1. Exactly the declared event set — no missing event, and no event nothing implements.
	wantEvents := make([]string, 0, len(want.Hooks))
	for e := range want.Hooks {
		wantEvents = append(wantEvents, e)
	}
	gotEvents := make([]string, 0, len(onDisk.Hooks))
	for e := range onDisk.Hooks {
		gotEvents = append(gotEvents, e)
	}
	require.ElementsMatch(t, wantEvents, gotEvents,
		"the committed hooks.json declares a different event set than internal/pluginmanifest")

	// 2. Every registered command goes through the plugin-root reference, and carries a timeout.
	// A hook with no timeout is one the host may cut off at a bound nobody chose.
	var commands int
	for event, groups := range onDisk.Hooks {
		require.NotEmpty(t, groups, "%s declares no hook group", event)
		for _, g := range groups {
			require.NotEmpty(t, g.Hooks, "%s declares an empty hook group", event)
			for _, h := range g.Hooks {
				commands++
				require.Equal(t, "command", h.Type, "%s: hook type", event)
				require.True(t, strings.HasPrefix(h.Command, binaryRef),
					"%s: %q must invoke the plugin binary through %s, or it cannot resolve once installed",
					event, h.Command, binaryRef)
				require.Positive(t, h.Timeout, "%s: %q declares no timeout", event, h.Command)
			}
		}
	}

	// 3. The MCP server declaration must reach the same binary, since .mcp.json is how the host
	// launches the retrieval server.
	mcpRaw, err := os.ReadFile(paths.Long(filepath.Join(repoRoot(t), "plugin", ".mcp.json")))
	require.NoError(t, err)
	var mcpOnDisk pluginmanifest.MCPJSON
	require.NoError(t, json.Unmarshal(mcpRaw, &mcpOnDisk))
	require.Len(t, mcpOnDisk.MCPServers, 1)
	for name, s := range mcpOnDisk.MCPServers {
		require.Equal(t, binaryRef, s.Command, "the %s MCP server must launch the plugin binary", name)
		require.Equal(t, []string{"mcp"}, s.Args)
	}

	version, found := claudeVersion()
	if !found {
		version = "unknown (no `claude` on PATH)"
	}
	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("repository-level: the committed plugin/hooks/hooks.json declares the "+
		"%d events internal/pluginmanifest declares, across %d command entries, each invoking %s "+
		"with a timeout; plugin/.mcp.json launches the same binary. Installed-host version at the "+
		"time of this record: %s. This is the packaging SHAPE, not evidence that an installed host "+
		"resolves it.", len(wantEvents), commands, binaryRef, version)
	writeRecord(t, rec)
}
