//go:build linux

package daemon

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// pidAlive also recognizes exited, unreaped Linux processes. They still answer
// kill(pid, 0), but cannot own a running daemon or publish any further writes.
// A live IPC listener still wins before this probe in lockIsStale. Read failures
// and ambiguous process state remain alive, including permission errors.
func pidAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, true
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH), true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true, true
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true, true
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) == 0 {
		return true, true
	}
	return fields[0] != "Z" && fields[0] != "X", true
}
