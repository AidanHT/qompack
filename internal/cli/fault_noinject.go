//go:build noinject

// The -tags noinject build variant: every fault site compiles to an unconditional no-op, so
// faultActive is always false regardless of the fault-injection environment variable's value — a
// compile-time guarantee rather than a runtime check. TestFaultSitesInertWhenUnset compares this
// build's hook output byte-for-byte against the default build's output with that variable unset,
// which is exactly what proves fault injection adds no observable behaviour when it is not asked
// for.
//
// This file deliberately never reads (or even spells) that variable's name: the security CI job's
// grep for its literal string finds it in exactly two non-test files — fault.go, which reads it,
// and internal/daemon/spawn.go, which strips it from a spawned daemon's environment so fault
// injection can never leak into a detached process — and this file's whole point is to not need
// it at all — including in its own comments (fix round 2, N-6: this comment previously claimed
// "exactly one non-test file", which flatly contradicted fault.go's own two-file contract and
// would have misdirected whoever next maintains the security grep).
package cli

import (
	"io"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

func faultActive(string) (string, bool) { return "", false }

func faultStdin(r io.Reader) io.Reader { return r }

func maybePanicHook() {}

func faultInflateToolResponse(*hookio.Event) {}

func wrapFaultSpool(sp ipc.SpoolWriter) ipc.SpoolWriter { return sp }

func wrapFaultClient(c ipc.Client) ipc.Client { return c }

func faultCorruptStateIfNeeded(string) {}

func faultCorruptConfigIfNeeded(string) {}

func faultLockSpoolDirIfNeeded(string) {}

func faultDaemonDownAddr(addr ipc.Addr) ipc.Addr { return addr }
