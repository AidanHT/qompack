//go:build linux

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

// TestLiveKillTree_ZombieLeaderIsAnExit: on Linux a kill of a process group whose leader has exited
// but is not yet reaped succeeds, so the tree kill must recognise the exited leader itself and report
// os.ErrProcessDone; otherwise exec turns a clean exit into "context canceled" (CI run 38986324968,
// ruling R56). The check must not reap the leader, or Wait would get ECHILD.
func TestLiveKillTree_ZombieLeaderIsAnExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	started, done, _ := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for i := 0; i < 500 && !liveLeaderExited(cmd.Process.Pid); i++ {
		<-tick.C
	}
	var info unix.Siginfo
	require.NoError(t, unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil))
	require.True(t, liveLeaderExited(cmd.Process.Pid), "the leader exited and is still unreaped")
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	require.NoError(t, cmd.Wait(), "the check did not reap the leader, so Wait still succeeds")
	done()
}

// TestLiveKillTree_LiveLeaderIsARealCancel: a leader still running when the group is killed dies of
// the kill (CLD_KILLED), and that is a real cancellation: the tree kill returns nil, not
// os.ErrProcessDone (ruling R56 refinement). The post-kill classification only turns a leader that
// exited on its own (CLD_EXITED) into an exit.
func TestLiveKillTree_LiveLeaderIsARealCancel(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	started, done, _ := liveTreeCancel(cmd)
	require.NoError(t, cmd.Start())
	started()
	require.NoError(t, cmd.Cancel())
	require.Error(t, cmd.Wait(), "killed by the group kill")
	done()
	require.False(t, liveLeaderExitedOnItsOwn(cmd.Process.Pid), "a reaped pid reads as not exited on its own")
}
