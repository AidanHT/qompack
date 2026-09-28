package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// A genuine allowed -> denied transition retains the original request/nonce and
// arrival. Retirement must free its successor without claiming capture.
func TestDeliveryTerminal_DrainAfterPhysicalScopeChanges(t *testing.T) {
	root := t.TempDir()
	_, dd, opts := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	ctx := context.Background()
	dir := filepath.Join(root, "allowed")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("before"), 0o600))
	req := sp08d2Read(testDeliveryToken('c'), "scope-transition", "toolu_denied_replay")
	req.Event.ToolInput = json.RawMessage(`{"file_path":"allowed/file.txt"}`)
	req.Event.CWD = root
	verdict := dd.admitDelivery(req)
	require.False(t, verdict.Denied)
	require.False(t, verdict.Failed)
	req = verdict.Request
	lease, ok := dd.ing.leaseDelivery(ctx, req)
	require.True(t, ok)
	later := spD3Prompt(dd, root, req.Session, testDeliveryToken('d'), "after denied capture")
	_, ok = dd.ing.leaseDelivery(ctx, later)
	require.True(t, ok)
	require.NoError(t, os.Rename(dir, dir+"-original"))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0o600))
	// A symlink where the host allows one, an NTFS junction otherwise: either makes the same
	// payload resolve outside the project.
	if err := makeDirLink(dir, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(dir) })
	require.True(t, dd.admitDelivery(req).Denied, "same payload now resolves outside permitted project")
	// Successor first exercises look-ahead retirement as well as prefix progress.
	const spool = "client-00001.ndjson"
	writeSpoolLines(t, root, spool, later, req)
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "only successor is captured")
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	denied, err := j.terminalDenied(lease)
	require.NoError(t, err)
	require.True(t, denied)
	require.False(t, j.acknowledged(req.Nonce), "policy retirement is never capture ACK")
	require.True(t, j.acknowledged(later.Nonce))
	_, err = opts.Store.ToolUse(ctx, req.Event.ToolUseID)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Equal(t, "after denied capture", spD3PromptText(t, opts, observer.VerbatimPromptID(req.Session, 0)))
	require.NoFileExists(t, filepath.Join(paths.Of(root).Spool, spool))
	require.False(t, dd.DrainGaps().Complete, "denied coverage remains explicit after progress")
}

func TestDeliveryTerminal_RetirementBeforeOffsetSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	_, dd, opts := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	ctx := context.Background()
	req := spD3Prompt(dd, root, "terminal-restart", testDeliveryToken('a'), "denied before restart")
	lease, ok := dd.ing.leaseDelivery(ctx, req)
	require.True(t, ok)
	later := spD3Prompt(dd, root, req.Session, testDeliveryToken('b'), "successor")
	_, ok = dd.ing.leaseDelivery(ctx, later)
	require.True(t, ok)
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	// The disposition fsync landed, but the source offset did not. On restart,
	// admission may now permit the request; retirement must still prevent capture.
	require.NoError(t, j.retireDenied(ctx, lease))
	writeSpoolLines(t, root, "client-00001.ndjson", req, later)
	require.NoError(t, lock.Release())
	lock = lockFor(t, dd, root)
	dd.ing.seen = newSeenSet(seenCapacity)
	spD3Drainer(dd, root)
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	j, err = dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, j.acknowledged(req.Nonce))
	require.True(t, j.acknowledged(later.Nonce))
	require.Equal(t, "successor", spD3PromptText(t, opts, observer.VerbatimPromptID(req.Session, 0)))
}

func TestDeliveryTerminal_UnprovedPolicyAndUnavailableJournalRemainPending(t *testing.T) {
	for _, mode := range []string{"policy unavailable", "journal unavailable", "binding conflict"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			_, dd, _ := wireTestDaemon(t, root, nil)
			lock := lockFor(t, dd, root)
			t.Cleanup(func() { _ = lock.Release() })
			ctx := context.Background()
			req := spD3Prompt(dd, root, "terminal-unknown", testDeliveryToken('a'), "retained")
			lease, ok := dd.ing.leaseDelivery(ctx, req)
			require.True(t, ok)
			j, err := dd.deliveryJournal()
			require.NoError(t, err)
			if mode == "binding conflict" {
				req.TS++
			}
			writeSpoolLines(t, root, "client-00001.ndjson", req)
			cfg := DrainConfig{
				Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk, Journal: dd.deliveryJournal,
				Dispatch: func(context.Context, ipc.Request) ipc.Response {
					t.Error("unavailable delivery dispatched")
					return ipc.Response{OK: true}
				},
				Admit: func(r ipc.Request) admissionVerdict {
					return admissionVerdict{Request: r, Denied: mode != "policy unavailable", Failed: mode == "policy unavailable", Reason: "test policy result"}
				},
			}
			if mode == "journal unavailable" {
				cfg.Journal = func() (*deliveryJournal, error) { return nil, deliveryJournalError() }
			}
			dr := newDrainer(cfg)
			_, _ = dr.Drain(ctx)
			denied, err := j.terminalDenied(lease)
			require.NoError(t, err)
			require.False(t, denied)
			require.False(t, j.acknowledged(req.Nonce))
			require.FileExists(t, filepath.Join(paths.Of(root).Spool, "client-00001.ndjson"))
		})
	}
}

// The worker must recheck policy after queueing; only its payload-free denial
// disposition may land. Its WAL source remains for the drain to account for.
func TestDeliveryTerminal_LiveQueuePolicyChangeCreatesNoCapture(t *testing.T) {
	root := t.TempDir()
	_, dd, opts := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	ctx := context.Background()
	req := spD3Prompt(dd, root, "queued-denial", testDeliveryToken('a'), "never captured")
	acceptPrompt(t, dd, req)
	job := <-dd.ing.ring
	dd.ing.admit = func(r ipc.Request) admissionVerdict {
		return admissionVerdict{Request: r, Denied: true, Reason: "test policy denial"}
	}
	dd.ing.dispatch(ctx, dd.runIngested, job)
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	denied, err := j.terminalDenied(job.lease)
	require.NoError(t, err)
	require.True(t, denied)
	require.False(t, j.acknowledged(req.Nonce))
	require.Empty(t, sidecarFiles(t, root), "no capture may precede the new policy check")
	_, err = opts.Store.ToolUse(ctx, observer.VerbatimPromptID(req.Session, 0))
	require.ErrorIs(t, err, core.ErrNotFound)
}

func TestDeliveryTerminal_DrainWaitsForLivePublicationOwner(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	ctx := context.Background()
	req := spD3Prompt(dd, root, "publication-owner", testDeliveryToken('a'), "published under prior policy")
	acceptPrompt(t, dd, req)
	job := <-dd.ing.ring
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		dd.ing.dispatch(ctx, func(c context.Context, r ipc.Request) ipc.Response {
			response := dd.runIngested(c, r)
			close(started)
			<-release
			return response
		}, job)
	}()
	<-started
	cfg := dd.drainConfig()
	cfg.Admit = func(r ipc.Request) admissionVerdict {
		return admissionVerdict{Request: r, Denied: true, Reason: "test policy denial"}
	}
	dr := newDrainer(cfg)
	writeSpoolLine(t, root, "client-00001.ndjson", req)
	_, drainErr := dr.Drain(ctx)
	j, openErr := dd.deliveryJournal()
	retired, terminalErr := j.terminalDenied(job.lease)
	// Always release/join the worker before assertions can terminate this test.
	close(release)
	<-done
	require.NoError(t, drainErr)
	require.NoError(t, openErr)
	require.NoError(t, terminalErr)
	require.False(t, retired, "a handler still owning the delivery prevents retirement")
	require.True(t, j.acknowledged(req.Nonce), "the already-permitted publication finishes")
	_, err := dr.Drain(ctx)
	require.NoError(t, err)
	retired, err = j.terminalDenied(job.lease)
	require.NoError(t, err)
	require.True(t, retired)
	require.True(t, j.acknowledged(req.Nonce), "subsequent denial preserves historical publication evidence")
}
