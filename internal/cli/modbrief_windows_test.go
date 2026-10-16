//go:build windows

package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// TestModBrief_ABusyDaemonIsRecordedBusy: a named pipe that exists but does not accept is a daemon
// that exists, so the brief answers busy, spawns nothing and records the call.
func TestModBrief_ABusyDaemonIsRecordedBusy(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	fakeModDaemon(t, p.root, nil)

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, modwire.ReasonBusy, decodeBriefOut(t, stdout).Reason)
	require.Zero(t, spawns.count())
	recs := p.briefRecords(t)
	require.Len(t, recs, 1)
	require.Equal(t, modwire.OutcomeBusy, recs[0].Outcome)
}
