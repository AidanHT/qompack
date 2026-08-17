//go:build windows

package daemon

// pidAlive has no cheap, dependency-free equivalent to POSIX kill(pid, 0) on Windows —
// os.FindProcess always succeeds there regardless of whether pid is actually running — so this
// reports "no opinion", and the heartbeat-mtime check (task-3-spec.md lock.go, staleness step 4)
// is what actually decides liveness on this platform.
func pidAlive(int) (alive bool, known bool) { return false, false }
