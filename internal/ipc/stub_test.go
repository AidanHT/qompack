package ipc_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/stretchr/testify/require"
)

// clock is the core.Clock obs.New needs. §6.1 bans wall-clock sleeps; a fixed clock also keeps
// metric timestamps deterministic.
type clock struct{}

func (clock) Now() time.Time                  { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
func (clock) Since(t time.Time) time.Duration { return clock{}.Now().Sub(t) }

// TestNewClient_ConstructsAndEveryOperationIsNotImplemented is the §14.1 stub contract for Client:
// constructing succeeds so a wave-0 composition root can wire one today, and every operation
// reports core.ErrNotImplemented rather than faking a delivery that never happened.
func TestNewClient_ConstructsAndEveryOperationIsNotImplemented(t *testing.T) {
	addr, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)

	spool, err := ipc.NewSpool(t.TempDir())
	require.NoError(t, err)

	c := ipc.NewClient(addr, spool, logging.Nop(), obs.New(clock{}))
	require.NotNil(t, c)

	deadline := time.Duration(config.Defaults().Runtime.Daemon.AckDeadlineMs) * time.Millisecond
	res, err := c.Send(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: "s", TS: 1}, deadline)
	require.ErrorIs(t, err, core.ErrNotImplemented)
	require.False(t, res.OK, "a stub must not report a delivery it did not make")

	require.ErrorIs(t, c.Close(), core.ErrNotImplemented)
}

// TestNewClient_ToleratesNilDependencies asserts constructing is total. A hook that failed to build
// a spool or a metrics registry must still be able to build a Client and reach the spool-and-exit
// path, because §2.3's rule is that a hook always exits 0.
func TestNewClient_ToleratesNilDependencies(t *testing.T) {
	require.NotPanics(t, func() {
		c := ipc.NewClient(ipc.Addr{}, nil, nil, nil)
		require.NotNil(t, c)
	})
}

// TestNewSpool_ConstructsWithoutTouchingTheFilesystem asserts the stub creates nothing. A stub that
// pre-created an empty client-<pid>.ndjson would leave one in every project that merely wired a
// client, and the daemon's drain would then have to distinguish an empty spool from a real one.
func TestNewSpool_ConstructsWithoutTouchingTheFilesystem(t *testing.T) {
	dir := t.TempDir()

	s, err := ipc.NewSpool(dir)
	require.NoError(t, err)
	require.NotNil(t, s)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "constructing a spool must not create a file")
}

// TestNewSpool_EveryOperationIsNotImplemented is the §14.1 stub contract for SpoolWriter, including
// the honest empty Path: a stub reporting the path it WOULD write to would be plausible-looking
// data for a file that does not exist.
func TestNewSpool_EveryOperationIsNotImplemented(t *testing.T) {
	s, err := ipc.NewSpool(t.TempDir())
	require.NoError(t, err)

	require.ErrorIs(t, s.Append(ipc.Request{Op: ipc.OpObserveTool}), core.ErrNotImplemented)
	require.Empty(t, s.Path())
}
