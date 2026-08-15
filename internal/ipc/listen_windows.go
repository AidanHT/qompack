//go:build windows

package ipc

import (
	"fmt"
	"net"
	"os/user"

	winio "github.com/Microsoft/go-winio"

	"github.com/qompack/qompack/internal/logging"
)

// pipeBufferBytes is the kernel-allocated buffer hint per pipe instance, not a message-size limit:
// byte-mode streams a 1 MiB NDJSON line through a 64 KiB buffer without truncation, and maxLine is
// enforced by LineReader in user space. Sizing the buffers at maxLine would pin ~2 MiB of
// non-paged pool per concurrent pipe instance for no benefit, which is why maxLine is accepted
// here only so this file and listen_unix.go share one signature.
const pipeBufferBytes = 64 << 10

// windowsFallbackSDDL is the creator-owner-only descriptor listen falls back to when the current
// user's SID cannot be resolved. It is never more permissive than the per-user ACL: CO restricts
// the pipe to whichever account created it, exactly as the per-SID ACE would have.
const windowsFallbackSDDL = "D:P(A;;GA;;;CO)"

// listen creates the named pipe a.Path names (00-ARCHITECTURE.md §2.4), ACL'd to the current
// user's SID only via a protected DACL — "P" so no inherited ACE ever widens it.
//
// Unlike listen_unix.go's dial-probe-then-reclaim, this function has no equivalent liveness check
// and cannot return ErrAddrInUse: winio.ListenPipe happily creates another instance of an
// already-existing pipe name (named pipes are multi-instance by design), so two daemons could in
// principle bind the same pipe name and silently round-robin connections between them. The real
// defence against that is the daemon-level singleton lock (daemon.AcquireLock, Task 3+), not
// anything this transport layer can detect on its own on this platform.
func listen(a Addr, log logging.Logger, maxLine int) (net.Listener, error) {
	sddl, fellBack := windowsSDDL()
	if fellBack && log != nil {
		log.Loud("ipc: could not resolve the current user's SID — named pipe ACL'd to creator-owner only", "addr", a.Path)
	}

	ln, err := winio.ListenPipe(a.Path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false, // byte mode: NDJSON is a stream protocol
		InputBufferSize:    pipeBufferBytes,
		OutputBufferSize:   pipeBufferBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("ipc: listen: %w", err)
	}
	return ln, nil
}

// windowsSDDL builds the per-user security descriptor a named pipe listener is created with:
// GENERIC_ALL for the current user's SID only, in a protected (non-inheriting) DACL. fellBack is
// true when the current user's SID could not be resolved and the creator-owner fallback was used
// instead — never a more permissive descriptor than the per-user ACE would have granted.
func windowsSDDL() (sddl string, fellBack bool) {
	u, err := user.Current()
	if err != nil {
		return windowsFallbackSDDL, true
	}
	return "D:P(A;;GA;;;" + u.Uid + ")", false
}
