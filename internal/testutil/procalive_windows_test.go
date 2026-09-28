//go:build windows

package testutil

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// procAliveChildDirEnv, when set, turns this test binary into the child that
// TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles launches: it opens procAliveChildFiles files
// in that directory, touches procAliveChildMiB of memory, says procAliveReady, and exits with
// procAliveChildExit once its stdin closes.
const procAliveChildDirEnv = "QOMPACK_TESTUTIL_PROCALIVE_CHILD_DIR"

const (
	// procAliveChildFiles and procAliveChildMiB give the kernel real teardown work at exit: every
	// handle has to be closed and every page unmapped before the process object is signaled. A
	// daemon holds a handful of files and a few tens of MiB; more of both only widens the window
	// the test must not fall into, so a probe that falls into it is caught every round.
	procAliveChildFiles = 512
	procAliveChildMiB   = 64
	// procAliveRounds is how many children the test takes through exit. Measured with the old
	// probe (GetExitCodeProcess), a daemon-sized child still held its files at the instant the
	// probe first said "exited" in 38 of 40 exits, and its process object was not yet signaled
	// in 40 of 40, so three rounds miss that defect with odds well under one in ten thousand.
	procAliveRounds = 3
	// procAliveChildExit is the child's exit code. It is not zero because a test function that
	// calls os.Exit(0) is reported as a failure by the testing package itself.
	procAliveChildExit = 3
	// procAliveReady is the line the child prints once it holds everything it will hold.
	procAliveReady = "procalive-child-ready"
	// procAliveExitBound bounds how long a child may take to exit after its stdin closes.
	procAliveExitBound = 30 * time.Second
)

// procAliveHeldName is the name of the child's i-th held file.
func procAliveHeldName(i int) string { return fmt.Sprintf("held-%04d", i) }

// procAliveHoldAndExit is the child's whole life. os.OpenFile takes FILE_SHARE_READ|FILE_SHARE_WRITE
// and no FILE_SHARE_DELETE, as the daemon's day log (paths.AppendOnly) does, so while the child
// holds a file nobody can delete it.
func procAliveHoldAndExit(dir string) {
	held := make([]*os.File, 0, procAliveChildFiles)
	for i := range procAliveChildFiles {
		f, err := os.OpenFile(filepath.Join(dir, procAliveHeldName(i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "procalive child:", err)
			os.Exit(procAliveChildExit + 1)
		}
		held = append(held, f)
	}
	const page = 4096
	mem := make([]byte, procAliveChildMiB<<20)
	for i := 0; i < len(mem); i += page {
		mem[i] = 1
	}
	fmt.Println(procAliveReady, len(held), len(mem))
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(procAliveChildExit)
}

// TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles pins what "not alive" has to mean for every
// caller of ProcessAlive: the shutdown helpers in test/e2e, test/fault, test/platform, test/release,
// test/security and test/guards all return on it and hand the project directory to t.TempDir's
// RemoveAll, so "not alive" must mean the process can no longer hold anything open in that tree.
//
// On Windows the process's exit code is set BEFORE the kernel closes its handles and unmaps its
// memory; the process object is signaled only after both. GetExitCodeProcess therefore reports an
// exit while the process still holds its files. Measured here (w4-e2eflakes runs/
// diag-a-exitcode-vs-signaled-windows.txt): in 120 of 120 exits the exit code was set while the
// process object was not yet signaled, for up to 151 ms; in 115 of them a file the child had open
// could not yet be deleted; in 0 of 120 was a file still held once the object was signaled.
//
// So the test watches the one probe under test until it first says "not alive", and at that instant
// requires both: the process object is signaled (an independent SYNCHRONIZE handle this test holds
// says so), and a file the child had open without FILE_SHARE_DELETE can be deleted.
func TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles(t *testing.T) {
	if dir := os.Getenv(procAliveChildDirEnv); dir != "" {
		procAliveHoldAndExit(dir)
	}
	require.True(t, ProcessAlive(os.Getpid()), "this process is alive")

	self, err := os.Executable()
	require.NoError(t, err)
	for round := range procAliveRounds {
		dir := t.TempDir()
		cmd := exec.Command(self, "-test.run=^TestProcessAlive_V6_WindowsExitedChildHoldsNoHandles$")
		cmd.Env = append(os.Environ(), procAliveChildDirEnv+"="+dir)
		stdin, err := cmd.StdinPipe()
		require.NoError(t, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(t, err)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Start())
		reaped := false
		t.Cleanup(func() {
			if !reaped {
				_ = cmd.Process.Kill() // this test's own child, never another process
				_ = cmd.Wait()
			}
		})

		ready := false
		for sc := bufio.NewScanner(stdout); !ready && sc.Scan(); {
			ready = strings.HasPrefix(sc.Text(), procAliveReady)
		}
		require.True(t, ready, "round %d: the child never said it was ready; stderr:\n%s", round, stderr.String())
		pid := cmd.Process.Pid
		require.True(t, ProcessAlive(pid), "round %d: the child is alive while it waits on stdin", round)

		// The independent witness. The pid stays this child's until cmd.Wait releases the handle
		// exec holds, so the probe below can only ever be asking about this process.
		witness, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
		require.NoError(t, err)
		t.Cleanup(func() { _ = syscall.CloseHandle(witness) })

		require.NoError(t, stdin.Close(), "closing stdin tells the child to exit")
		deadline := time.Now().Add(procAliveExitBound)
		for ProcessAlive(pid) {
			require.False(t, time.Now().After(deadline),
				"round %d: the child was still alive %s after its stdin closed", round, procAliveExitBound)
		}
		event, err := syscall.WaitForSingleObject(witness, 0)
		require.NoError(t, err)
		require.Equal(t, uint32(syscall.WAIT_OBJECT_0), event,
			"round %d: ProcessAlive reported pid %d gone while its process object was not yet signaled: "+
				"the kernel was still closing its handles", round, pid)
		require.NoError(t, os.Remove(filepath.Join(dir, procAliveHeldName(0))),
			"round %d: ProcessAlive reported pid %d gone while a file it held open still could not be "+
				"deleted, which is what fails a caller's t.TempDir cleanup", round, pid)

		waitErr := cmd.Wait()
		reaped = true
		var exitErr *exec.ExitError
		require.ErrorAs(t, waitErr, &exitErr, "round %d: stderr:\n%s", round, stderr.String())
		require.Equal(t, procAliveChildExit, exitErr.ExitCode(), "round %d: the child exits as scripted", round)
	}
}
