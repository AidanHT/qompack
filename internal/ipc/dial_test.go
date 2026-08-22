package ipc

import (
	"net"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// dialSignature pins the one signature both platform files must implement (§14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). It is a compile-time assertion: if
// dial_windows.go and dial_other.go ever diverge, the build breaks on the platform that drifted
// rather than at the call site SP-05 eventually adds.
var dialSignature func(Addr, time.Duration) (net.Conn, error) = dial

// TestDial_SignatureIsIdenticalOnEveryPlatform exists so dialSignature is not merely declared.
func TestDial_SignatureIsIdenticalOnEveryPlatform(t *testing.T) {
	require.NotNil(t, dialSignature)
}

// TestDial_AbsentEndpointFailsWithoutAConnection dials the endpoint of a project that has never
// run a daemon, which is the single most common state on the hot path: the first hook of a session
// finds no listener. It must report an error and no connection, so Send's spool-and-exit fallback
// (§2.4) is reached rather than a nil-conn panic.
//
// This is also the only test in the tree that exercises the go-winio dependency for real, and it is
// deliberately a LOCAL endpoint — a named pipe on Windows, a Unix socket elsewhere. Nothing here
// touches a network stack (D10).
//
// The timeout comes from runtime.daemon.connectDeadlineMs rather than a literal, per D11: this is
// the same deadline a real hot-path client would use, so if that default is ever raised to
// something that would make this test slow, it is the config change that has to justify itself.
// The Windows default is 25ms for exactly that reason (internal/config/deadlines.go) and this test
// does not pay it: an endpoint no daemon ever created answers CreateFile with
// ERROR_FILE_NOT_FOUND, not ERROR_PIPE_BUSY, so tryDialPipe returns on its first attempt without
// ever reaching the retry sleep the budget is sized for.
func TestDial_AbsentEndpointFailsWithoutAConnection(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, addr.Path)

	timeout := time.Duration(config.Defaults().Runtime.Daemon.ConnectDeadlineMs) * time.Millisecond
	conn, err := dial(addr, timeout)
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}

	require.Error(t, err, "dialling an endpoint no daemon is listening on must fail")
	require.Nil(t, conn, "a failed dial must not hand back a connection")
}
