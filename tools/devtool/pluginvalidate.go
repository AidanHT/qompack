package main

import (
	"errors"
	"fmt"
	"path/filepath"
)

// taskPluginValidate regenerates the plugin bundle from internal/pluginmanifest into a temp dir,
// byte-compares it against plugin/, and asserts the 7 commands and 8 MCP tools are present.
//
// internal/pluginmanifest and plugin/ land in a later commit of this same subplan, so this task
// cannot import that package yet without making this commit unbuildable. Before the package
// exists it degrades to a zero-exit no-op; once it exists but before this task is wired against
// it, it returns a clear, typed error rather than silently doing nothing.
func taskPluginValidate(args []string) error {
	pmDir := filepath.Join(root, "internal", "pluginmanifest")
	if !dirHasGoFiles(pmDir) {
		fmt.Println("plugin-validate: internal/pluginmanifest not present yet (landed by a later commit of this subplan)")
		return nil
	}
	return errors.New("plugin-validate: internal/pluginmanifest exists but devtool's generator wiring has not been integrated yet")
}
