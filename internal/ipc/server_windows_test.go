//go:build windows

package ipc

import (
	"os"
	"os/user"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWindowsSDDL_Shape asserts the SDDL string shape unconditionally (cheap, no ACL probing
// required): it begins "D:P" (a protected DACL, so no inherited ACE ever widens it) and, when the
// current user's SID resolves, contains that SID.
func TestWindowsSDDL_Shape(t *testing.T) {
	sddl, fellBack := windowsSDDL()
	require.True(t, strings.HasPrefix(sddl, "D:P"), "the DACL must be protected: got %q", sddl)

	if !fellBack {
		u, err := user.Current()
		require.NoError(t, err)
		require.Contains(t, sddl, u.Uid)
	} else {
		require.Equal(t, windowsFallbackSDDL, sddl)
	}
}

// TestWindowsPipeACLRejectsOtherUser is the table's row. It is gated behind QOMPACK_TEST_ACL=1 in
// both directions: unset, it is skipped outright ("runs only under QOMPACK_TEST_ACL=1"); set, it
// is still skipped, because proving denial requires dialling as a genuinely different Windows
// principal, which needs a second provisioned account this environment does not have — a fake
// "different token" in the same process would not exercise the kernel's own ACL check at all.
// TestWindowsSDDL_Shape above covers the unconditional, cheap half of this row: the SDDL this
// package builds is well-formed and scoped to one SID. The env var still gates this test (rather
// than deleting it) so a CI job that does provision a second principal has a named test to wire a
// real cross-principal dial into.
func TestWindowsPipeACLRejectsOtherUser(t *testing.T) {
	if os.Getenv("QOMPACK_TEST_ACL") != "1" {
		t.Skip("platform: requires QOMPACK_TEST_ACL=1 and a second Windows principal to dial as; unset here")
	}
	t.Skip("platform: QOMPACK_TEST_ACL=1 set, but no second Windows principal is provisioned in this environment")
}
