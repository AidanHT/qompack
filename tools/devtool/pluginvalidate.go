package main

import (
	"flag"
	"fmt"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// The §7.5 bundle's expected shape. These are literals rather than len() of whatever the
// generator produced, because the check exists to catch a generator that silently stopped
// emitting something — and a self-referential count cannot.
//
// wantHookEvents is seven, not six: §7.3 names six hook ENTRY POINTS, but Stop and SubagentStop
// are separate host events that both route to `observe stop`.
//
// There is deliberately no MCP-tool count here. §7.5's eight tools are declared by
// internal/mcp, whose Tools() is a stub returning nil until SP-13 lands; asserting a count over
// an empty list would be a check that passes for the wrong reason and would keep passing after
// SP-13 got it wrong. The bundle fixes the MCP SERVER declaration, and that is what is checked.
const (
	wantCommands   = 7
	wantHookEvents = 7
	wantMCPServers = 1
)

// taskPluginValidate regenerates the plugin bundle from internal/pluginmanifest and byte-compares
// it against plugin/ on disk, asserting the 7 commands and 8 MCP tools are present.
//
// This is the mechanism behind §3.4's promise that the manifest, the physical hooks.json and the
// docs can never drift silently: the bundle has exactly one source, and CI fails if the committed
// tree stops matching it. With --write it regenerates the tree instead of comparing, which is how
// plugin/ is produced in the first place.
func taskPluginValidate(args []string) error {
	fs := flag.NewFlagSet("plugin-validate", flag.ContinueOnError)
	write := fs.Bool("write", false, "regenerate plugin/ from the manifest instead of comparing")
	// core.Version is the single source §3.4 names: the binary reports it, the bundle declares it,
	// and this task compares against it. Defaulting to anything else here — including the empty
	// string — would let `qompack version` and plugin.json disagree while both looked correct.
	version := fs.String("version", core.Version, "version to stamp into the bundle")
	if err := fs.Parse(args); err != nil {
		return err
	}

	m := pluginmanifest.Default(*version)
	// Manifest.Files() keys are repo-relative and already carry the "plugin/" prefix, so the base
	// directory is the repository root — not root/plugin, which would look for plugin/plugin/.
	dir := root

	if *write {
		if err := pluginmanifest.Write(dir, m); err != nil {
			return fmt.Errorf("plugin-validate --write: %w", err)
		}
		fmt.Println("plugin-validate: wrote plugin/ from internal/pluginmanifest")
		return nil
	}

	if err := checkBundleShape(m); err != nil {
		return err
	}

	diffs := pluginmanifest.Validate(dir, m)
	if len(diffs) == 0 {
		files, err := m.Files()
		if err != nil {
			return fmt.Errorf("plugin-validate: %w", err)
		}
		fmt.Printf("plugin-validate: OK (%d file(s), %d command(s), %d hook event(s))\n",
			len(files), len(m.Commands), len(m.Hooks.Hooks))
		return nil
	}

	for _, d := range diffs {
		fmt.Printf("  %s: %s\n", d.Path, d.Reason)
	}
	return fmt.Errorf("plugin-validate: plugin/ does not match internal/pluginmanifest "+
		"(%d discrepancy/ies); run `go run ./tools/devtool plugin-validate --write` and commit the result",
		len(diffs))
}

// checkBundleShape asserts the counts §7.5 fixes, so a generator that drops a command fails here
// with a message naming the count rather than as an opaque byte diff.
func checkBundleShape(m pluginmanifest.Manifest) error {
	if got := len(m.Commands); got != wantCommands {
		return fmt.Errorf("plugin-validate: manifest declares %d command(s), want %d (§7.5)", got, wantCommands)
	}
	if got := len(m.Hooks.Hooks); got != wantHookEvents {
		return fmt.Errorf("plugin-validate: manifest declares %d hook event(s), want %d "+
			"(§7.3's six entry points, with Stop and SubagentStop as separate host events)", got, wantHookEvents)
	}
	if got := len(m.MCP.MCPServers); got != wantMCPServers {
		return fmt.Errorf("plugin-validate: manifest declares %d MCP server(s), want %d (§7.5)", got, wantMCPServers)
	}
	return nil
}
