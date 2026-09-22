//go:build linux

package testutil

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ProcessAlive reports whether a process can still execute. Linux containers may
// retain an exited child as a zombie until PID 1 reaps it; kill(pid, 0) still
// succeeds for that process, although it cannot write to a test's directory.
// Inaccessible or malformed proc state remains conservatively alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH)
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	// comm is parenthesized and may itself contain spaces or ')'. State is the
	// first field after its final ')', not the third whitespace-delimited token.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(stat[end+1:]))
	return len(fields) == 0 || (fields[0] != "Z" && fields[0] != "X")
}
