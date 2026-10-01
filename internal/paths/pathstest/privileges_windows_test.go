//go:build windows

package pathstest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege: inside the helper the effective
// token holds neither SeBackupPrivilege nor SeRestorePrivilege enabled, whatever the process token
// holds; the log line names which the process token holds, so a hosted run records the account it
// ran under. On an ordinary user's token both lists are empty and the row still checks that the
// helper runs fn and returns.
func TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege(t *testing.T) {
	t.Logf("process token enables: %v", pathstest.EnabledBypassPrivileges(t))
	ran := false
	pathstest.WithoutBackupPrivileges(t, func() {
		ran = true
		require.Empty(t, pathstest.EnabledBypassPrivileges(t),
			"an ACL fixture would be bypassed by a backup-semantics open")
	})
	require.True(t, ran)
}
