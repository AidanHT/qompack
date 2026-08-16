package daemon

import (
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// defaultMaxSessions mirrors config.Defaults().Runtime.Daemon.MaxSessions (8). It is not itself a
// default source (D11 lives in internal/config/defaults.go) — it exists only so a caller that
// hands Ensure a zero config.Config does not evict every session it has, which is what dividing by
// a limit of 0 would do.
const defaultMaxSessions = 8 //nomagic:allow mirrors config.Defaults(), not a new default (§6.1)

// SessionState is one session's live state (00-ARCHITECTURE.md §5.4).
type SessionState struct {
	ID   core.SessionID
	Hot  ipc.HotPathMode
	Live bool

	LastActivity core.UnixMilli
	Events       int

	StartedTS core.UnixMilli
	EndedTS   core.UnixMilli

	Source         string
	TranscriptPath string
}

// SessionRegistry holds per-session live state behind a sync.RWMutex, plus the daemon-wide
// hot-path submode §12.2's breach detector flips.
//
// The mutex is real even though the map operations were stubs in SP-01: a registry is touched by
// the accept loop and the idle loop concurrently by construction, so shipping it without
// synchronization would leave a data race for SP-05 to discover under -race rather than a correct
// skeleton to fill in.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[core.SessionID]*SessionState
	log      logging.Logger

	// hot and hotReason are the daemon-wide hot-path submode (§12.2): what the breach detector
	// last decided, and why. It is registry-wide rather than per-session because a single rolling
	// HDR histogram covers every hot-path request regardless of which session sent it (§2.4's "the
	// daemon keeps a rolling 512-sample HDR histogram per hook"), and the NAK-with-hint that
	// follows a breach applies to the very next ACK on ANY session, not just the one that tripped
	// it.
	hot       ipc.HotPathMode
	hotReason string

	// maxSessions is the eviction ceiling evictLocked enforces, set via SetMaxSessions rather than
	// threaded through Ensure as a config.Config parameter: Ensure runs on every accepted request
	// (the hot path), and forcing every caller to hold and pass a whole config.Config just to reach
	// one int is the kind of awkwardness the logger avoids the same way, via SetLogger.
	maxSessions int

	// maxSessionsLoud latches true the first time evictLocked finds nothing evictable (every
	// tracked session is live): §7.1 says refusing a session is never an option, so the daemon
	// keeps all of them and this fires the required Loud line exactly once rather than on every
	// subsequent Ensure.
	maxSessionsLoud bool
}

// NewSessionRegistry returns an empty registry. Its zero-arg signature is unchanged from SP-01's
// stub so daemon.New and every existing caller keep compiling; a logger and a max-sessions limit
// are wired in later via SetLogger/SetMaxSessions.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions:    map[core.SessionID]*SessionState{},
		log:         logging.Nop(),
		maxSessions: defaultMaxSessions,
	}
}

// SetLogger installs the logger Ensure's hot-mode-reset and eviction-Loud lines are written
// through. A nil log falls back to logging.Nop(), matching every other constructor in this
// package.
func (r *SessionRegistry) SetLogger(log logging.Logger) {
	if log == nil {
		log = logging.Nop()
	}
	r.mu.Lock()
	r.log = log
	r.mu.Unlock()
}

// SetMaxSessions installs the eviction ceiling Ensure enforces (cfg.Runtime.Daemon.MaxSessions in
// production). n <= 0 falls back to defaultMaxSessions, matching NewSessionRegistry's own initial
// value, so a caller can never accidentally set a limit that evicts everything.
func (r *SessionRegistry) SetMaxSessions(n int) {
	if n <= 0 {
		n = defaultMaxSessions
	}
	r.mu.Lock()
	r.maxSessions = n
	r.mu.Unlock()
}

// Get returns the state for id, if the session is known.
func (r *SessionRegistry) Get(id core.SessionID) (*SessionState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

// IsLive reports whether id is currently tracked as live, reading SessionState.Live under the
// registry's own RLock. Prefer this over Get+field-read for anything that only needs the
// liveness bool: Get hands back the shared *SessionState pointer, and reading any of its fields
// after the lock is dropped races Ensure/Touch/End, which mutate it under the write lock (fix
// round 2, FR-1).
func (r *SessionRegistry) IsLive(id core.SessionID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return ok && s.Live
}

// Len returns the number of tracked sessions (live and recently-ended).
func (r *SessionRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// HotMode returns the daemon-wide hot-path submode §12.2's breach detector maintains.
func (r *SessionRegistry) HotMode() ipc.HotPathMode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hot
}

// HotReason returns the reason recorded with the current hot-path submode, for /qompack:status.
func (r *SessionRegistry) HotReason() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hotReason
}

// SetHotMode sets the daemon-wide hot-path submode and its transition reason. The budget-breach
// detector (Task 4) is the production caller.
func (r *SessionRegistry) SetHotMode(mode ipc.HotPathMode, reason string) {
	r.mu.Lock()
	r.hot = mode
	r.hotReason = reason
	r.mu.Unlock()
}

// Ensure returns the state for e.SessionID, creating it if this is the first time this session has
// been seen. Source and TranscriptPath are updated from e when non-empty, and the session is
// marked live.
//
// A brand-new session resets the daemon-wide hot-path submode to HotSync: §12.2 says the spool
// submode "automatically reverts after 3 clean windows in a subsequent session", and the
// controller ruling makes that deterministic — a session that has not itself sent a single request
// yet cannot have breached anything, so it must not inherit a submode an unrelated, already-ended
// session left behind. It re-degrades only if it breaches again. Re-Ensuring an already-known
// session id — the ordinary per-event call — never touches the hot submode.
func (r *SessionRegistry) Ensure(e *hookio.Event, now core.UnixMilli) *SessionState {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := e.SessionID
	s, ok := r.sessions[id]
	if !ok {
		if r.hot != ipc.HotSync {
			prevMode, prevReason := r.hot, r.hotReason
			r.hot = ipc.HotSync
			r.hotReason = ""
			r.log.Info("daemon: new session resets the hot-path submode to sync",
				"session", string(id), "previousMode", prevMode, "previousReason", prevReason)
		}
		s = &SessionState{ID: id, Hot: r.hot, StartedTS: now}
		r.sessions[id] = s
	}

	if e.Source != "" {
		s.Source = e.Source
	}
	if e.TranscriptPath != "" {
		s.TranscriptPath = e.TranscriptPath
	}
	s.Live = true
	s.LastActivity = now

	r.evictLocked()
	return s
}

// Touch records activity on an existing session: advances LastActivity and increments Events. It
// is a no-op for an id Ensure has never seen — the accept loop always calls Ensure first, so this
// is a defensive guard rather than a path any real caller exercises.
func (r *SessionRegistry) Touch(id core.SessionID, now core.UnixMilli) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return
	}
	s.LastActivity = now
	s.Events++
}

// End marks id no longer live. It is a no-op for an id Ensure has never seen.
func (r *SessionRegistry) End(id core.SessionID, now core.UnixMilli) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return
	}
	s.Live = false
	s.EndedTS = now
}

// Live returns the number of currently-live sessions — the input to the idle-exit timer, which
// starts counting down the instant this reaches zero.
func (r *SessionRegistry) Live() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, s := range r.sessions {
		if s.Live {
			n++
		}
	}
	return n
}

// Snapshot returns a defensive copy of every tracked session, for /qompack:status and tests.
func (r *SessionRegistry) Snapshot() []SessionState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SessionState, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, *s)
	}
	return out
}

// LastActivity returns the most recent activity timestamp across every tracked session — the
// input IdleController.Notify wants: the daemon is idle only once every session has been quiet
// long enough.
func (r *SessionRegistry) LastActivity() core.UnixMilli {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var last core.UnixMilli
	for _, s := range r.sessions {
		if s.LastActivity > last {
			last = s.LastActivity
		}
	}
	return last
}

// evictLocked drops the ended session with the oldest EndedTS once the tracked count exceeds
// r.maxSessions. If every tracked session is live, nothing is evicted — refusing a session is
// never an option (§7.1) — and a single Loud line fires once per registry lifetime. mu must be
// held.
func (r *SessionRegistry) evictLocked() {
	limit := r.maxSessions
	if limit <= 0 {
		limit = defaultMaxSessions
	}
	if len(r.sessions) <= limit {
		return
	}

	var oldestID core.SessionID
	var oldestEnded core.UnixMilli
	found := false
	for id, s := range r.sessions {
		if s.Live {
			continue
		}
		if !found || s.EndedTS < oldestEnded {
			oldestID, oldestEnded, found = id, s.EndedTS, true
		}
	}
	if !found {
		if !r.maxSessionsLoud {
			r.maxSessionsLoud = true
			r.log.Loud("daemon: live sessions exceed runtime.daemon.maxSessions; keeping all of them",
				"count", len(r.sessions), "max", limit)
		}
		return
	}
	delete(r.sessions, oldestID)
}
