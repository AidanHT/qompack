//go:build !windows

package pathstest

import "testing"

// WithoutBackupPrivileges runs fn. Off Windows no privilege a test process holds lets an open past
// a file's mode bits except running as root, which the Linux gate never does (it runs non-root),
// so there is nothing to remove; see privileges_windows.go.
func WithoutBackupPrivileges(t testing.TB, fn func()) {
	t.Helper()
	fn()
}

// EnabledBypassPrivileges returns nil: off Windows there are no such privileges to report.
func EnabledBypassPrivileges(t testing.TB) []string {
	t.Helper()
	return nil
}
