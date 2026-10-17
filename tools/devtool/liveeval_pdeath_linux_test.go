//go:build linux

package main

import (
	"context"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLiveSetProcessGroup_Pdeathsig: on Linux the host also gets SIGKILL when devtool itself dies, so
// a crash of the harness cannot leave it running (re-review F3).
func TestLiveSetProcessGroup_Pdeathsig(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	liveSetProcessGroup(cmd)
	require.True(t, cmd.SysProcAttr.Setpgid)
	require.Equal(t, syscall.SIGKILL, cmd.SysProcAttr.Pdeathsig)
}
