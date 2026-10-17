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
	"strings"
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
	// walkErr is a failed or partial tree walk, kept for the caller to report after Wait: exec's Wait
	// drops Cancel's error once the killed process exits non-zero, which it always does after a kill
	// (re-review 4, F2).
	walkErr error
}

// killError is the recorded walk failure, or nil.
func (p *liveProcPin) killError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.walkErr
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

// exited reports that the pinned process has exited, by waiting on its handle with a zero timeout: an
// exit code of 259 is STILL_ACTIVE's value and would read as running (re-review F4). Unpinned, it
// says no, unless Wait has returned.
func (p *liveProcPin) exited() bool { return p.exitedWithin(0) }

// exitedWithin is exited, waiting up to ms milliseconds: a process taskkill or TerminateProcess has
// just ended may take a moment to be signalled.
func (p *liveProcPin) exitedWithin(ms uint32) bool {
	if p.released {
		return true
	}
	if !p.pinned {
		return false
	}
	ev, err := syscall.WaitForSingleObject(p.h, ms)
	return err == nil && ev == syscall.WAIT_OBJECT_0
}

// liveWalkSettle is how long, after a failed walk, the pin waits for the root to be signalled.
const liveWalkSettle = 2000

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

// liveTaskkillNotFound is taskkill's exit code when a pid it was named is not found, an exited process
// whose handle is still held included. liveKillTree names exactly one pid, the root, so here it means
// the root was not found. Established on Windows 11 by running taskkill /T /F /PID on a pid no
// process has and on an exited child whose handle the prober held, both "ERROR: The process "<pid>"
// not found." with exit 128 (fix round 5; TestLiveTaskkill_NotFoundExit pins it). A walk that ended
// the root but not a descendant exits 255 (re-review 4's demonstration).
const liveTaskkillNotFound = 128

// liveTaskkillError is a taskkill run that exited non-zero.
type liveTaskkillError struct {
	PID    int
	Code   int
	Output string
}

func (e *liveTaskkillError) Error() string {
	return fmt.Sprintf("taskkill /T /F /PID %d: exit %d: %s", e.PID, e.Code, strings.TrimSpace(e.Output))
}

// liveTaskkill ends the tree under pid with taskkill /T /F. A variable so a test can watch whether it
// runs.
var liveTaskkill = func(pid int) error {
	bin, err := liveTaskkillPath()
	if err != nil {
		return err
	}
	out, err := exec.Command(bin, "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		return &liveTaskkillError{PID: pid, Code: exitErr.ExitCode(), Output: string(out)}
	default:
		return fmt.Errorf("taskkill /T /F /PID %d: %w: %s", pid, err, out)
	}
}

// liveKillTree ends the process the harness started and every process it started in turn: the
// default host is npm's claude.cmd shim, so the direct child is cmd.exe and claude.exe its child,
// which a kill of the direct child alone leaves running. taskkill /T only walks down from the given
// pid, so it touches nothing the harness did not start.
//
// Once the process has exited, or Wait has returned, nothing is killed and os.ErrProcessDone tells exec
// not to report a cancellation, so a kill racing a normal exit never turns a clean trial into a
// harness error. Before the pin is taken the tree is still walked: Go's own handle pins the pid until
// Wait. If the walk fails with the root still running, the direct child is killed and the walk's error
// is returned with it; if the walk ended the root but failed on a descendant (a partial walk), the
// direct child is already gone and only the walk's error is returned. Either way it is recorded for
// the caller to report after Wait, so a degraded kill is never silent (re-review F2, 4-F2; R53).
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
	var tk *liveTaskkillError
	ran := errors.As(walkErr, &tk)
	notFound := ran && tk.Code == liveTaskkillNotFound
	// The settle wait is for a root taskkill may just have ended; when taskkill never ran, nothing
	// was ended, and the direct child is killed at once (R53, N1).
	rootGone := pin.exited()
	if ran {
		rootGone = pin.exitedWithin(liveWalkSettle)
	}
	if notFound && rootGone {
		// The root exited between the check and the walk, so taskkill found nothing (re-review F4).
		return os.ErrProcessDone
	}
	if rootGone {
		// taskkill ended the root but failed on a descendant (exit 255): the root being gone does not
		// make the failure an exit, and a descendant may still run (re-review 4, F2).
		pin.walkErr = fmt.Errorf("the process tree was only partly ended, so a descendant may still run: %w", walkErr)
		return pin.walkErr
	}
	degraded := fmt.Errorf("the process tree could not be ended, so only the direct child was killed: %w", walkErr)
	if err := cmd.Process.Kill(); err != nil {
		if notFound && errors.Is(err, os.ErrProcessDone) {
			return os.ErrProcessDone // unpinned, and it had exited
		}
		if !errors.Is(err, os.ErrProcessDone) {
			degraded = errors.Join(degraded, err)
		}
	}
	pin.walkErr = degraded
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
