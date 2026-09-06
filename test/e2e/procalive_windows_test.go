//go:build windows

package e2e

import "github.com/qompack/qompack/internal/testutil"

// e2eProcessAlive reports whether a process with this pid is still running, through
// OpenProcess + GetExitCodeProcess, which is decisive on Windows where
// internal/daemon's own pidAlive deliberately abstains.
//
// The probe itself lives in internal/testutil, which is the one home for it: test/guards needs
// the same answer for the same reason (v1StopDaemonAndWaitGone), and two copies of a platform
// syscall probe is two places for the ERROR_ACCESS_DENIED-is-not-death subtlety to be got wrong.
// See testutil.ProcessAlive for the measurements behind both platforms' answers.
func e2eProcessAlive(pid int) bool { return testutil.ProcessAlive(pid) }
