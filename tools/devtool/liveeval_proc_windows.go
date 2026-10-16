//go:build windows

package main

// Ending a host process tree, and asking whether a lock holder still runs, on Windows (V7 W6 item 8,
// re-review I1, M8, N6 and N9).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// liveSetProcessGroup prepares cmd so its whole tree can be ended: nothing on Windows, where taskkill
// walks the tree from the pid the harness started.
func liveSetProcessGroup(*exec.Cmd) {}

// liveProcPin holds a handle to the process the harness started, taken right after Start and closed
// only after Wait. While it is open Windows cannot give the pid to another process, so the tree kill
// never names a pid the harness no longer owns, and the handle says whether the process has already
// exited (re-review N6).
type liveProcPin struct {
	mu       sync.Mutex
	h        syscall.Handle
	pinned   bool // the handle is open
	released bool // Wait has returned: the pid is no longer the harness's
}

// synchronizeRight is SYNCHRONIZE: the pin waits on the handle to learn whether the process exited.
const synchronizeRight = 0x00100000

// pin opens the handle; a failure leaves the process unpinned, and the kill then relies on Go's own
// handle, which pins the pid until Wait.
func (p *liveProcPin) pin(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	h, err := syscall.OpenProcess(processQueryLimited|synchronizeRight, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	p.mu.Lock()
	p.h, p.pinned = h, true
	p.mu.Unlock()
}

// release closes the handle once Wait has returned.
func (p *liveProcPin) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pinned {
		_ = syscall.CloseHandle(p.h)
		p.pinned = false
	}
	p.released = true
}

// exited reports that the pinned process has exited, by waiting on its handle with no timeout: an
// exit code of 259 is STILL_ACTIVE's value and would read as running (re-review F4). Unpinned, it
// says no, unless Wait has returned.
func (p *liveProcPin) exited() bool {
	if p.released {
		return true
	}
	if !p.pinned {
		return false
	}
	ev, err := syscall.WaitForSingleObject(p.h, 0)
	return err == nil && ev == syscall.WAIT_OBJECT_0
}

// liveTaskkillPath is taskkill's absolute path in the system directory: %SystemRoot%\System32 when
// SystemRoot is an absolute path, else the system directory Windows reports, else %windir%\System32.
// It is never looked up on PATH, so a taskkill earlier on PATH cannot stand in for it (re-review N9,
// F2).
func liveTaskkillPath() (string, error) {
	if root := os.Getenv("SystemRoot"); root != "" && filepath.IsAbs(root) {
		return filepath.Join(root, "System32", "taskkill.exe"), nil
	}
	if sys, err := windows.GetSystemDirectory(); err == nil && filepath.IsAbs(sys) {
		return filepath.Join(sys, "taskkill.exe"), nil
	}
	if win := os.Getenv("windir"); win != "" && filepath.IsAbs(win) {
		return filepath.Join(win, "System32", "taskkill.exe"), nil
	}
	return "", errors.New("neither SystemRoot, the reported system directory nor windir names an absolute path, so " +
		"taskkill.exe cannot be named")
}

// liveTaskkill ends the tree under pid with taskkill /T /F. A variable so a test can watch whether it
// runs.
var liveTaskkill = func(pid int) error {
	bin, err := liveTaskkillPath()
	if err != nil {
		return err
	}
	out, err := exec.Command(bin, "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill /T /F /PID %d: %w: %s", pid, err, out)
	}
	return nil
}

// liveKillTree ends the process the harness started and every process it started in turn: the
// default host is npm's claude.cmd shim, so the direct child is cmd.exe and claude.exe its child,
// which a kill of the direct child alone leaves running. taskkill /T only walks down from the given
// pid, so it touches nothing the harness did not start.
//
// Once the process has exited, or Wait has returned, nothing is killed and os.ErrProcessDone tells exec
// not to report a cancellation, so a kill racing a normal exit never turns a clean trial into a
// harness error. Before the pin is taken the tree is still walked: Go's own handle pins the pid until
// Wait. If the walk fails, the direct child is killed and the walk's error is returned with it, so a
// degraded kill is never silent (re-review F2).
func liveKillTree(cmd *exec.Cmd, pin *liveProcPin) error {
	if cmd.Process == nil {
		return nil
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.exited() {
		return os.ErrProcessDone
	}
	walkErr := liveTaskkill(cmd.Process.Pid)
	if walkErr == nil {
		return nil
	}
	if pin.exited() {
		// The root exited between the check and the walk (re-review F4).
		return os.ErrProcessDone
	}
	degraded := fmt.Errorf("the process tree could not be ended, so only the direct child was killed: %w", walkErr)
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(degraded, err)
	}
	return degraded
}

// errNoSuchProcess is ERROR_INVALID_PARAMETER, what OpenProcess answers for a pid no process has.
const errNoSuchProcess = syscall.Errno(87)

// processQueryLimited is PROCESS_QUERY_LIMITED_INFORMATION, the least right that reads an exit code.
const processQueryLimited = 0x1000

// stillActive is STILL_ACTIVE, the exit code of a process that has not exited.
const stillActive = 259

// liveHolderGone reports that err says the process does not exist. Anything else, access denied
// included, means it may be alive.
func liveHolderGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, errNoSuchProcess)
}

// liveProcessAlive reports whether a process with this pid may still run on this machine: only a
// pid no process has, or a process that has exited, reads as gone.
func liveProcessAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimited, false, uint32(pid))
	if err != nil {
		return !liveHolderGone(err)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
