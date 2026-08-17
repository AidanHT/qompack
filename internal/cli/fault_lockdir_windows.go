//go:build windows && !noinject

package cli

import (
	"fmt"
	"os/exec"
	"os/user"
)

// denyWriteDir makes dir non-writable for the spool-readonly fault site on Windows. Plain
// os.Chmod's FILE_ATTRIBUTE_READONLY bit is close to a no-op for a DIRECTORY on modern Windows —
// it does not stop new files from being created inside one — so this instead adds a real deny-ACE
// for the current user via icacls (os/exec is permitted in cli per §3.2's import discipline), the
// same mechanism 00-ARCHITECTURE.md's own Windows notes for this fault site name. Best-effort: the
// caller (fault.go's faultLockSpoolDirIfNeeded) swallows any error this returns, so an icacls
// failure on an unusual host degrades to "this one fault site's OS mechanism did not engage" rather
// than to a hook failure.
//
// Gated !noinject as well as windows (fix round 1, Minor M-11): the only caller
// (faultLockSpoolDirIfNeeded, fault.go) is itself !noinject-only, so a -tags noinject build has no
// reference to this function at all — keeping it out of that build too means the hardened variant
// never contains a code path that shells out to icacls, and golangci-lint's unused check (were it
// ever run against that tag) would have nothing to flag.
func denyWriteDir(dir string) error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("cli: fault: spool-readonly: resolving current user: %w", err)
	}
	// (OI)(CI)W: inherit onto objects and containers, deny Write.
	cmd := exec.Command("icacls", dir, "/deny", u.Username+":(OI)(CI)W") //nolint:gosec // G204: fixed subcommand, dir/username are not external input
	return cmd.Run()
}
