//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestLiveKillTree_ZombieGroupOnDarwin: on darwin killpg on a group whose only member is an unreaped
// zombie can answer EPERM instead of ESRCH (CI run 38992500032, macos-15-intel). Such a group is gone:
// the tree kill reports os.ErrProcessDone, records no walk error, and Wait still succeeds (ruling R59).
func TestLiveKillTree_ZombieGroupOnDarwin(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/usr/bin/true")
	started, done, killErr := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	pid := cmd.Process.Pid
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	zombie := false
	for i := 0; i < 500 && !zombie; i++ {
		if kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid); err == nil && kp.Proc.P_stat == darwinSZOMB {
			zombie = true
			break
		}
		<-tick.C
	}
	require.True(t, zombie, "the leader became an unreaped zombie")
	require.True(t, liveZombieLeader(pid))
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	require.NoError(t, killErr(), "a zombie-only group is gone, not a failed kill")
	require.NoError(t, cmd.Wait())
	done()
}
