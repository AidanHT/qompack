package hookio

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// makeDirLink installs a directory link at linkPath pointing at target, by whatever mechanism this
// host allows: a real symlink where privilege permits, and otherwise, on Windows, an NTFS junction,
// which needs no privilege. A junction is the directory alias an unprivileged Windows process can
// actually create, so a test that needs a directory alias runs on such a host instead of skipping.
// It repeats the helper of the same name in internal/paths, internal/mcp and internal/hostperm: an
// in-package test here cannot import internal/testutil, which itself depends on this package.
func makeDirLink(linkPath, target string) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// mklink is a cmd builtin rather than an executable, hence cmd /c: the standard library has no
	// call that creates a junction.
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", linkPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
