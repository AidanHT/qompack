package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// V6 close-out (e2e lane's review finding, plans/sdd/V6-closeout/e2e/runs/
// review-repro-drained-control-sidecar_test.go.txt): the hook client mints a delivery nonce for EVERY
// hook it sends, so a session start, a checkpoint or a SessionEnd flush that fell back to the client
// spool reaches the drain leased, and dispatchPending published a capture sidecar for any leased line.
// A control line is not an observation: nothing ever references such a sidecar, and the publication
// audit and fsck, which classify only the three observe ops, then reported the project incomplete
// ("capture sidecar with an unrecognized op", "capture publication requirement is unknown") for the
// rest of its life.

// TestDrain_ControlLinesPublishNoCaptureSidecar: a drained control line is dispatched and reaches the
// committed frontier exactly as before, and publishes no capture sidecar, so the publication audit
// stays complete.
func TestDrain_ControlLinesPublishNoCaptureSidecar(t *testing.T) {
	for _, tc := range []struct {
		op   ipc.Op
		hook string
	}{
		{ipc.OpFlush, "SessionEnd"},
		{ipc.OpCheckpoint, "PreCompact"},
		{ipc.OpSessionStart, "SessionStart"},
	} {
		t.Run(string(tc.op), func(t *testing.T) {
			root := t.TempDir()
			_, dd, _ := wireTestDaemon(t, root, nil)
			lock := lockFor(t, dd, root)
			t.Cleanup(func() { _ = lock.Release() })
			t.Cleanup(func() {
				grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
				defer cancel()
				dd.stopPromptRecordings(grace)
			})
			dd.drain.Store(newDrainer(dd.drainConfig()))

			const sess core.SessionID = "sess-drained-control"
			req := ipc.Request{
				Op: tc.op, Session: sess, Reply: true, Nonce: testDeliveryToken('c'), TS: core.NowMilli(dd.clk),
				Event:   &hookio.Event{HookEventName: tc.hook, SessionID: sess, CWD: root, Source: "startup"},
				Capture: admittedCapture(`{"hook_event_name":"` + tc.hook + `"}`),
			}
			writeSpoolLines(t, root, "client-77777.ndjson", req)

			_, err := dd.Drain(context.Background())
			require.NoError(t, err)

			require.Empty(t, drainControlSidecarOps(t, root),
				"a %s line is not an observation; draining it must publish no capture sidecar", tc.op)

			j, err := dd.deliveryJournal()
			require.NoError(t, err)
			lease, held, err := j.leaseHeld(req.Nonce)
			require.NoError(t, err)
			require.True(t, held, "the drain still leases the control line")
			require.True(t, j.acknowledged(lease.Delivery),
				"the control line still reaches the committed frontier, so it is never replayed again")
			require.NoFileExists(t, filepath.Join(paths.Of(root).Spool, "client-77777.ndjson"),
				"the consumed client spool is released")

			s, err := store.OpenReadOnly(root, testConfig(), store.Deps{})
			require.NoError(t, err)
			defer func() { _ = s.Close() }()
			auditor, ok := s.(store.PublicationAuditor)
			require.True(t, ok)
			a, err := auditor.AuditPublication(context.Background(), store.DefaultPublicationScanCap())
			require.NoError(t, err)
			require.False(t, a.Incomplete, "the audit stays complete: notes=%v", a.Notes)
		})
	}
}

// drainControlSidecarOps lists the op of every capture sidecar under root.
func drainControlSidecarOps(t *testing.T, root string) []string {
	t.Helper()
	var ops []string
	for _, p := range sidecarFiles(t, root) {
		b, err := os.ReadFile(paths.Long(p))
		require.NoError(t, err)
		var sc struct {
			Op string `json:"op"`
		}
		require.NoError(t, json.Unmarshal(b, &sc))
		ops = append(ops, sc.Op)
	}
	return ops
}
