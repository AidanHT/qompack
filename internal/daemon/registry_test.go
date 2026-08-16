package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// TestRegistryNewSessionResetsHotMode pins the §12.2 determinism ruling: a brand-new session
// always starts in sync, but re-Ensuring an already-known session never touches the hot submode.
func TestRegistryNewSessionResetsHotMode(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.SetHotMode(ipc.HotSpool, "breach")
	require.Equal(t, ipc.HotSpool, r.HotMode())

	// A brand-new session resets the daemon-wide submode back to sync.
	r.Ensure(&hookio.Event{SessionID: "sess-a"}, 1000)
	require.Equal(t, ipc.HotSync, r.HotMode())

	// Force a fresh breach against this now-known session's traffic, then Ensure the SAME id
	// again: the submode must be left exactly as it is.
	r.SetHotMode(ipc.HotSpool, "breach again")
	r.Ensure(&hookio.Event{SessionID: "sess-a"}, 2000)
	require.Equal(t, ipc.HotSpool, r.HotMode(), "re-Ensuring a known session must not reset the hot submode")
}

// TestRegistryEvictsEndedOverMax pins the eviction rule: over the configured max-sessions limit,
// drop the ended session with the oldest EndedTS; live sessions are never touched.
func TestRegistryEvictsEndedOverMax(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.SetMaxSessions(2)
	r.Ensure(&hookio.Event{SessionID: "a"}, 100)
	r.Ensure(&hookio.Event{SessionID: "b"}, 200)
	r.End("a", 300)

	// Ensuring a third session pushes the tracked count to 3, over the limit of 2: the ended
	// session ("a") must be evicted, leaving the two live sessions.
	r.Ensure(&hookio.Event{SessionID: "c"}, 400)

	require.Equal(t, 2, r.Len())
	_, ok := r.Get("a")
	require.False(t, ok, "the ended session must be evicted")
	_, ok = r.Get("b")
	require.True(t, ok)
	_, ok = r.Get("c")
	require.True(t, ok)
}

// TestRegistryAllLiveOverMaxKeepsAll pins §7.1's "refusing a session is never an option": when
// every tracked session is live, eviction keeps them all.
func TestRegistryAllLiveOverMaxKeepsAll(t *testing.T) {
	t.Parallel()

	var loudLines []string
	log := &captureLogger{loud: &loudLines}

	r := NewSessionRegistry()
	r.SetLogger(log)
	r.SetMaxSessions(2)
	r.Ensure(&hookio.Event{SessionID: "a"}, 100)
	r.Ensure(&hookio.Event{SessionID: "b"}, 200)
	r.Ensure(&hookio.Event{SessionID: "c"}, 300)

	require.Equal(t, 3, r.Len(), "no live session may be evicted")
	require.Len(t, loudLines, 1, "the over-max Loud line fires exactly once")

	// A further Ensure over the limit must not fire a second Loud line.
	r.Ensure(&hookio.Event{SessionID: "d"}, 400)
	require.Equal(t, 4, r.Len())
	require.Len(t, loudLines, 1)
}

// TestRegistryTouchAndEnd exercises the ordinary per-event lifecycle.
func TestRegistryTouchAndEnd(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.Ensure(&hookio.Event{SessionID: "s1"}, 100)
	require.Equal(t, 1, r.Live())

	r.Touch("s1", 200)
	s, ok := r.Get("s1")
	require.True(t, ok)
	require.Equal(t, core.UnixMilli(200), s.LastActivity)
	require.Equal(t, 1, s.Events)

	r.End("s1", 300)
	require.Equal(t, 0, r.Live())
	s, ok = r.Get("s1")
	require.True(t, ok)
	require.False(t, s.Live)
	require.Equal(t, core.UnixMilli(300), s.EndedTS)
}

// TestRegistryEnsureUpdatesSourceAndTranscript pins that non-empty fields overwrite and empty
// fields never blank out a previously-recorded value.
func TestRegistryEnsureUpdatesSourceAndTranscript(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.Ensure(&hookio.Event{SessionID: "s1", Source: "startup", TranscriptPath: "/tmp/t.jsonl"}, 100)
	r.Ensure(&hookio.Event{SessionID: "s1"}, 200) // no Source/TranscriptPath this time

	s, ok := r.Get("s1")
	require.True(t, ok)
	require.Equal(t, "startup", s.Source, "an empty field on a later Ensure must not blank a prior value")
	require.Equal(t, "/tmp/t.jsonl", s.TranscriptPath)
}

// TestSessionRegistry_IsLive pins the fix round 2 FR-1 seam: IsLive reads Live entirely under
// the registry's own RLock, so a caller (drainer's IsLive callback in particular) never needs to
// hold the shared *SessionState pointer Get returns — and therefore never races Ensure/Touch/End,
// which mutate that same struct under the write lock.
func TestSessionRegistry_IsLive(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	require.False(t, r.IsLive("unknown"), "an id Ensure has never seen must report not-live")

	r.Ensure(&hookio.Event{SessionID: "sess-1"}, 100)
	require.True(t, r.IsLive("sess-1"))

	r.End("sess-1", 200)
	require.False(t, r.IsLive("sess-1"), "IsLive must reflect End immediately")
}

// TestSessionRegistry_IsLiveConcurrentWithEndIsRaceFree exercises the exact interleaving FR-1
// diagnosed: one goroutine calling IsLive (the drainer's role) while another concurrently mutates
// the same session's Live field via End (a route ending a session mid-drain).
func TestSessionRegistry_IsLiveConcurrentWithEndIsRaceFree(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.Ensure(&hookio.Event{SessionID: "sess-1"}, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			_ = r.IsLive("sess-1")
		}
	}()
	for i := 0; i < 500; i++ {
		r.End("sess-1", core.UnixMilli(i))
		r.Ensure(&hookio.Event{SessionID: "sess-1"}, core.UnixMilli(i))
	}
	<-done
}

// TestRegistrySetMaxSessionsRejectsNonPositive pins that SetMaxSessions(n<=0) falls back to
// defaultMaxSessions rather than evicting everything.
func TestRegistrySetMaxSessionsRejectsNonPositive(t *testing.T) {
	t.Parallel()

	r := NewSessionRegistry()
	r.SetMaxSessions(0)
	for i := 0; i < defaultMaxSessions+1; i++ {
		id := core.SessionID(string(rune('a' + i)))
		r.Ensure(&hookio.Event{SessionID: id}, core.UnixMilli(i))
		r.End(id, core.UnixMilli(i))
	}
	require.Equal(t, defaultMaxSessions, r.Len(),
		"a non-positive limit must fall back to defaultMaxSessions, not evict down to (near) zero")
}

// captureLogger is a minimal logging.Logger that records Loud lines, for assertions that a Loud
// line fired exactly once.
type captureLogger struct {
	loud *[]string
}

func (c *captureLogger) With(...any) logging.Logger { return c }
func (c *captureLogger) Debug(string, ...any)       {}
func (c *captureLogger) Info(string, ...any)        {}
func (c *captureLogger) Warn(string, ...any)        {}
func (c *captureLogger) Error(string, ...any)       {}
func (c *captureLogger) Loud(msg string, kv ...any) { *c.loud = append(*c.loud, msg) }
