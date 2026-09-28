//go:build !noinject

package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestSessionStartBudget_AnUnspoolableStartWithNoTimeLeftIsLoud (w5-coldstart review nit): with no
// time left to wait for an answer, session-start spools its request without dialling. When that
// append is refused, the request is lost, and the hook must say so loudly instead of dropping the
// error and logging that the request was spooled. The refusal is the disk-full fault site, whose
// spool fails every append. Not parallel: t.Setenv, and fault.go's once-per-process parse is reset.
func TestSessionStartBudget_AnUnspoolableStartWithNoTimeLeftIsLoud(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultDiskFull)
	root := unansweredProject(t, nil)
	requests := silentDaemon(t, root)
	host := hookExitReserve + budgetTestReply + hookConnectDeadlineFloor + hookConnectDeadlineFloor
	spec := sessionStartSpec(blockUntil(func(by time.Time) time.Time {
		return by.Add(budgetTestReply + hookConnectDeadlineFloor + 100*time.Millisecond) // past the bound
	}))
	spec.hostTimeout, spec.deadline = host, budgetTestReply

	out, _, _ := runBudgetedSessionStart(t, root, "startup", spec)
	require.Equal(t, "{}\n", out, "the hook still answers")
	require.Zero(t, requests.Load(), "no answer can be waited for, so nothing is sent")
	require.False(t, spooledStart(t, root), "the fault refused the append")

	loud, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Logs, "LOUD.log"))
	require.NoError(t, err, "a lost request leaves a Loud line")
	require.Contains(t, string(loud), unsentSpoolRefusedMsg)
	require.Contains(t, string(loud), errFaultDiskFull.Error())
}
