//go:build !darwin && !windows

package main

// liveZombieLeader is false outside darwin: there a group kill's EPERM is a real failure (ruling R59).
func liveZombieLeader(int) bool { return false }
