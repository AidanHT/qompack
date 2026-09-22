package hostperm

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// makeDirLink installs a directory link at linkPath pointing at target: a real symlink where
// privilege permits, and otherwise an NTFS junction, which needs none. It is internal/mcp's test
// helper of the same name, repeated because a test helper cannot be shared across packages.
func makeDirLink(linkPath, target string) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", linkPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
