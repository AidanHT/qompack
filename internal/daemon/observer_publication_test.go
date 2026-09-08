package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// publicationFaultStore keeps WireObserver on a real Store while making precisely the two
// publication steps that turn stored bytes into a retrievable observation fail on demand.
// Embedding preserves every unrelated Store method and lets the repaired retry use the same
// on-disk objects and indices as the failed attempt.
type publicationFaultStore struct {
	store.Store

	mu        sync.Mutex
	putErr    error
	recordErr error
}

func (s *publicationFaultStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	s.mu.Lock()
	err := s.putErr
	s.mu.Unlock()
	if err != nil {
		return store.PutResult{}, err
	}
	return s.Store.PutBytes(ctx, b, o)
}

func (s *publicationFaultStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	s.mu.Lock()
	err := s.recordErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.RecordToolUse(ctx, rec)
}

func (s *publicationFaultStore) repair() {
	s.mu.Lock()
	s.putErr = nil
	s.recordErr = nil
	s.mu.Unlock()
}

// TestObserverPublicationFailureRemainsDrainRetryable makes the acknowledgement boundary travel
// through the real daemon wiring: a Store failure is not an observer warning that may be ACKed.
// The WAL remains the retry source until a tool-use reference is published. This deliberately
// does not claim that the current store's separate Flush protocol is a durable frontier commit.
func TestObserverPublicationFailureRemainsDrainRetryable(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*publicationFaultStore)
	}{
		{name: "object write", fail: func(s *publicationFaultStore) { s.putErr = errors.New("object write refused") }},
		{name: "tool-use reference", fail: func(s *publicationFaultStore) { s.recordErr = errors.New("tool-use index refused") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			backing, err := store.Open(root, testConfig(), store.Deps{})
			require.NoError(t, err)
			storeOwned := true
			t.Cleanup(func() {
				if storeOwned {
					_ = backing.Close()
				}
			})
			faulty := &publicationFaultStore{Store: backing}
			tc.fail(faulty)

			_, dd, _ := wireTestDaemon(t, root, func(o *Options) { o.Store = faulty })
			storeOwned = false // wireTestDaemon now owns the supplied store's lifetime
			t.Cleanup(func() { _ = dd.ing.Close() })
			req := ipc.Request{
				Op: ipc.OpObserveTool, Session: "sess-observer-publication", TS: core.NowMilli(dd.clk),
				Event: &hookio.Event{
					HookEventName: "PostToolUse", SessionID: "sess-observer-publication", CWD: root,
					ToolName: "Read", ToolUseID: "toolu_observer_publication",
					ToolInput:    json.RawMessage(`{"file_path":"src/publication.go"}`),
					ToolResponse: json.RawMessage(`{"content":"package publication\n"}`),
				},
			}
			line, err := ipc.EncodeRequest(req)
			require.NoError(t, err)
			require.NoError(t, dd.ing.Accept(req, line))
			job := <-dd.ing.ring

			var first ipc.Response
			dd.ing.dispatch(context.Background(), func(ctx context.Context, got ipc.Request) ipc.Response {
				first = dd.runIngested(ctx, got)
				return first
			}, job)
			require.False(t, first.OK, "an unpublished observation must NAK so its WAL line is retryable")
			require.Equal(t, "observation handling failed", first.Err)
			require.NotContains(t, first.Err, "object write refused")
			require.NotContains(t, first.Err, "tool-use index refused")
			walFilePath := walPath(paths.Of(root).Spool, req.Session, 0)
			require.FileExists(t, walFilePath)
			wal, readErr := os.ReadFile(walFilePath)
			require.NoError(t, readErr)
			require.Equal(t, line, wal, "the failed observation remains byte-for-byte replayable")
			_, lookupErr := faulty.ToolUse(context.Background(), req.Event.ToolUseID)
			require.ErrorIs(t, lookupErr, core.ErrNotFound, "failed publication must not expose a tool-use reference")

			completed, acquired := dd.ing.seen.begin(job.key)
			require.False(t, completed, "only a successful observation may enter the completed seen set")
			require.True(t, acquired, "the failed work remains eligible for retry")
			dd.ing.seen.finish(job.key, false)

			faulty.repair()
			require.NoError(t, dd.ing.CloseSession(req.Session), "the inactive drainer cannot remove an open WAL on Windows")
			dr := newDrainer(DrainConfig{Root: root, Seen: dd.ing.seen, Dispatch: dd.runIngested})
			n, err := dr.Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, n, "the repaired drainer must actually re-run the retained line")
			rec, lookupErr := faulty.ToolUse(context.Background(), req.Event.ToolUseID)
			require.NoError(t, lookupErr)
			require.NotZero(t, rec.Root, "the repaired publication exposes the stored object through its reference")
			require.NoFileExists(t, walFilePath)
		})
	}
}
