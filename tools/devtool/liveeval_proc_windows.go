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
)

// liveSetProcessGroup prepares cmd so its whole tree can be ended: nothing on Windows, where taskkill
// walks the tree from the pid the harness started.
func liveSetProcessGroup(*exec.Cmd) {}

// liveProcPin holds a handle to the process the harness started, taken right after Start and closed
// only after Wait. While it is open Windows cannot give the pid to another process, so the tree kill
// never names a pid the harness no longer owns, and the handle says whether the process has already
// exited (re-review N6).
type liveProcPin struct {
	mu sync.Mutex
	h  syscall.Handle
	ok bool
}

// synchronizeRight is SYNCHRONIZE, which with the limited query right keeps a waitable handle.
const synchronizeRight = 0x00100000

// pin opens the handle; a failure leaves the pin empty and the kill falls back to the direct child.
func (p *liveProcPin) pin(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	h, err := syscall.OpenProcess(processQueryLimited|synchronizeRight, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	p.mu.Lock()
	p.h, p.ok = h, true
	p.mu.Unlock()
}

// release closes the handle once Wait has returned.
func (p *liveProcPin) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ok {
		_ = syscall.CloseHandle(p.h)
		p.ok = false
	}
}

// exited reports that the pinned process has exited, or that the pin was released after Wait.
func (p *liveProcPin) exited() bool {
	if !p.ok {
		return true
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(p.h, &code); err != nil {
		return false
	}
	return code != stillActive
}

// liveTaskkillPath is taskkill's absolute path under %SystemRoot%: it is never looked up on PATH, so
// a taskkill earlier on PATH cannot stand in for it (re-review N9).
func liveTaskkillPath() (string, error) {
	root := os.Getenv("SystemRoot")
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("SystemRoot is not set to an absolute path, so taskkill.exe cannot be named")
	}
	return filepath.Join(root, "System32", "taskkill.exe"), nil
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
// pid, so it touches nothing the harness did not start. Once the pinned process has exited, or the
// pin was released after Wait, nothing is killed and os.ErrProcessDone tells exec not to report a
// cancellation, so a kill racing a normal exit never turns a clean trial into a harness error.
func liveKillTree(cmd *exec.Cmd, pin *liveProcPin) error {
	if cmd.Process == nil {
		return nil
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.ok || pinReleased(pin) {
		if pin.exited() {
			return os.ErrProcessDone
		}
		if err := liveTaskkill(cmd.Process.Pid); err == nil {
			return nil
		}
	}
	// No pin was taken (opening the process failed), or the tree walk failed: end the direct child,
	// whose handle Go still holds until Wait returns.
	if err := cmd.Process.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// pinReleased reports a pin that was taken and has since been released after Wait.
func pinReleased(p *liveProcPin) bool { return p.h != 0 && !p.ok }

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
