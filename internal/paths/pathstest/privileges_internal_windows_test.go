//go:build windows

package pathstest

import (
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// changeNotify is SeChangeNotifyPrivilege (bypass traverse checking), which every user token holds
// enabled, an ordinary user's included: the one privilege whose removal can be proven on any
// Windows machine, where the backup and restore privileges are held only by an elevated token.
const changeNotify = "SeChangeNotifyPrivilege"

// TestWithoutPrivileges_RemovesAHeldPrivilegeOnlyForFn proves withoutPrivileges' impersonate,
// remove and revert steps on a privilege this token really holds: inside fn the thread runs under
// its own token without the privilege, and once the helper returns the thread is back on the
// process token, which still holds it enabled. WithoutBackupPrivileges is the same steps over the
// backup and restore privileges, which a non-elevated token never holds.
func TestWithoutPrivileges_RemovesAHeldPrivilegeOnlyForFn(t *testing.T) {
	// The whole row runs on one thread, so "after" reads the thread the helper reverted.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	held, enabled := privilegeState(t, changeNotify)
	require.True(t, held && enabled, "precondition: every user token holds %s enabled", changeNotify)
	require.False(t, hasThreadToken(t), "precondition: the thread is not impersonating")

	ran := false
	withoutPrivileges(t, []string{changeNotify}, func() {
		ran = true
		require.True(t, hasThreadToken(t), "fn runs under the thread's own token")
		held, _ := privilegeState(t, changeNotify)
		require.False(t, held, "%s is removed from the token fn runs under", changeNotify)
	})
	require.True(t, ran)

	require.False(t, hasThreadToken(t), "the impersonation ends when the helper returns")
	held, enabled = privilegeState(t, changeNotify)
	require.True(t, held && enabled, "the process token keeps %s", changeNotify)
}

// hasThreadToken reports whether the calling thread is impersonating.
func hasThreadToken(t *testing.T) bool {
	t.Helper()
	var tok windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok)
	if errors.Is(err, windows.ERROR_NO_TOKEN) {
		return false
	}
	require.NoError(t, err, "OpenThreadToken")
	_ = tok.Close()
	return true
}
