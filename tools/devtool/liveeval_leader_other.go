//go:build !linux && !windows

package main

// liveLeaderExited is false where the group kill itself reports an exited leader (ESRCH, as on macOS).
func liveLeaderExited(int) bool { return false }

// liveLeaderExitedOnItsOwn is false there for the same reason.
func liveLeaderExitedOnItsOwn(int) bool { return false }
