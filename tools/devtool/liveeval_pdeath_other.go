//go:build !linux && !windows

package main

import "syscall"

// livePdeath does nothing where the system has no parent-death signal.
func livePdeath(*syscall.SysProcAttr) {}
