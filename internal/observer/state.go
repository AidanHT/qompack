package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/paths"
)

// The observer's crash-tolerant resume state (§3.3): turn index, prefix position, segment
// bookkeeping and the SubagentStop window, written to <root>/.qompack/state/observer.json.
//
// state/ is daemon-persisted scratch rather than an append-only log, so this is a whole-file
// WriteAtomic rather than an append. What is deliberately NOT here is as important as what is:
// Recent is a feature window that legitimately restarts cold after a daemon restart, and
// WarnedRules and PendingThrash are warning-dedup state whose worst failure is one repeated
// thrash line.
const (
	// stateVersion is the on-disk schema version. Any other value is treated exactly like a
	// corrupt file — rename and start fresh — which is the migration path for a future field.
	stateVersion = 1
	// observerStateFile is the basename under <root>/.qompack/state.
	observerStateFile = "observer.json"
	// corruptStateSuffix is appended when an unreadable file is set aside.
	corruptStateSuffix = ".bad"
	// stateFilePerm is the permission every state file in this repository is written with.
	stateFilePerm fs.FileMode = 0o600
	// stateDirPerm is the permission the state directory is created with.
	stateDirPerm fs.FileMode = 0o700
)

// stateFilePath returns the observer state file's path under root.
func stateFilePath(root string) string {
	return filepath.Join(paths.Of(root).State, observerStateFile)
}

// persistedState is the whole file.
type persistedState struct {
	Version  int                         `json:"version"`
	Sessions map[string]persistedSession `json:"sessions"`
}

// persistedSession is one session's durable half.
type persistedSession struct {
	Turn         core.TurnIndex `json:"turn"`
	PrefixTokens int            `json:"prefix_tokens"`
	Segment      core.SegmentID `json:"segment"`
	PrevSegment  core.SegmentID `json:"prev_segment"`
	SegStartTurn core.TurnIndex `json:"seg_start_turn"`
	SegStartPos  int            `json:"seg_start_pos"`
	LastTS       core.UnixMilli `json:"last_ts"`
	// LastToolUseID is the ToolUseID, NOT a dag.NodeID: the builder mints node ids itself.
	LastToolUseID   string         `json:"last_tool_use_id"`
	LastToolUseTurn core.TurnIndex `json:"last_tool_use_turn"`
	// LastPromptTurn is beyond the shape the plan pins, and is persisted deliberately: it is
	// OnUserPrompt's own bookkeeping, it is not recoverable from any other persisted field, and
	// losing it on resume would make the first prompt after a daemon restart look like the first
	// prompt of the session.
	LastPromptTurn core.TurnIndex `json:"last_prompt_turn"`
	SubagentSince  int            `json:"subagent_since"`
	// TodoDone is persisted SORTED so the file is diffable and byte-stable across runs.
	TodoDone []string           `json:"todo_done"`
	ToolUses []persistedToolUse `json:"tool_uses"`
}

// persistedToolUse is one entry of the SubagentStop window.
type persistedToolUse struct {
	ID string `json:"id"`
	// Root is core.Hash.String() form ("sha256:…"), so the file stays greppable and the identity
	// stays the STORE's own root rather than a second one only this package can read.
	Root  string `json:"root"`
	Tool  string `json:"tool"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// loadState reads the state file into the session map. It runs at most once per process, tolerates
// a missing file, and never blocks a session: a JSON error or an unknown version logs Warn,
// renames the file aside, and starts fresh.
func (o *observer) loadState() {
	b, err := os.ReadFile(paths.Long(o.stateFile))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			o.soft(stageState, err)
		}
		return
	}

	var ps persistedState
	if err := json.Unmarshal(b, &ps); err != nil {
		o.setAsideCorruptState(err)
		return
	}
	if ps.Version != stateVersion {
		o.setAsideCorruptState(fmt.Errorf("observer: state file version %d is not %d",
			ps.Version, stateVersion))
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	for id, sess := range ps.Sessions {
		o.sess[core.SessionID(id)] = o.rehydrate(sess)
	}
}

// setAsideCorruptState renames the unreadable file and reports it. A failure to rename is itself
// soft: the next persistState overwrites the file anyway.
func (o *observer) setAsideCorruptState(cause error) {
	o.soft(stageState, cause)
	if err := os.Rename(paths.Long(o.stateFile), paths.Long(o.stateFile+corruptStateSuffix)); err != nil {
		o.opt.Log.Debug("observer: could not set aside the corrupt state file",
			"path", o.stateFile, "err", err)
	}
}

// rehydrate turns one persisted session back into live state.
func (o *observer) rehydrate(p persistedSession) *sessionState {
	st := &sessionState{
		Turn:            p.Turn,
		PrefixTokens:    p.PrefixTokens,
		Segment:         p.Segment,
		PrevSegment:     p.PrevSegment,
		SegStartTurn:    p.SegStartTurn,
		SegStartPos:     p.SegStartPos,
		LastTS:          p.LastTS,
		LastToolUseID:   core.ToolUseID(p.LastToolUseID),
		LastToolUseTurn: p.LastToolUseTurn,
		LastPromptTurn:  p.LastPromptTurn,
		SubagentSince:   p.SubagentSince,
		TodoDone:        make(map[string]bool, len(p.TodoDone)),
		WarnedRules:     make(map[grammar.RuleID]bool),
	}
	for _, done := range p.TodoDone {
		st.TodoDone[done] = true
	}
	for _, tu := range p.ToolUses {
		root, err := core.ParseHash(tu.Root)
		if err != nil {
			// One unreadable root drops one entry rather than the whole window: the entry is a
			// retrieval convenience for SubagentStop, not a durable identity.
			o.opt.Log.Debug("observer: dropping a tool-use window entry with an unparseable root",
				"id", tu.ID, "root", tu.Root, "err", err)
			continue
		}
		st.ToolUses = append(st.ToolUses, toolUseLite{
			ID: core.ToolUseID(tu.ID), Root: root, Tool: tu.Tool, Path: tu.Path, Bytes: tu.Bytes,
		})
	}
	if st.SubagentSince > len(st.ToolUses) {
		st.SubagentSince = len(st.ToolUses)
	}
	return st
}

// persistState writes every live session to the state file.
//
// It never holds o.mu and a sessionState.mu at the same time: it copies the map under o.mu,
// releases it, snapshots each session under that session's own lock, and re-takes o.mu only for
// the write itself — which is the half of decision 9's "guards sess and the state-file write".
func (o *observer) persistState() {
	// The load-before-persist guard. Persist is registered as daemon idle work, so it can fire
	// BEFORE any observer entry point has run — a daemon restarted mid-session with no hook event
	// in its first idle interval. Without this, the write below would serialize an EMPTY session
	// map over the crash-resume file and wipe it. Same sync.Once as session(): whichever runs
	// first loads, the other sees loaded state.
	o.once.Do(o.loadState)

	o.mu.Lock()
	ids := make([]core.SessionID, 0, len(o.sess))
	states := make([]*sessionState, 0, len(o.sess))
	for id, st := range o.sess {
		ids = append(ids, id)
		states = append(states, st)
	}
	o.mu.Unlock()

	ps := persistedState{Version: stateVersion, Sessions: make(map[string]persistedSession, len(ids))}
	for i, st := range states {
		ps.Sessions[string(ids[i])] = snapshotSession(st)
	}

	b, err := json.Marshal(ps)
	if err != nil {
		o.soft(stageState, err)
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if err := os.MkdirAll(paths.Long(filepath.Dir(o.stateFile)), stateDirPerm); err != nil {
		o.soft(stageState, err)
		return
	}
	o.soft(stageState, paths.WriteAtomic(o.stateFile, b, stateFilePerm))
}

// snapshotSession copies one session's durable half under its own lock.
func snapshotSession(st *sessionState) persistedSession {
	st.mu.Lock()
	defer st.mu.Unlock()

	done := make([]string, 0, len(st.TodoDone))
	for content := range st.TodoDone {
		done = append(done, content)
	}
	sort.Strings(done)

	window := st.ToolUses
	if len(window) > subagentWindowCap {
		window = window[len(window)-subagentWindowCap:]
	}
	tus := make([]persistedToolUse, 0, len(window))
	for _, tu := range window {
		tus = append(tus, persistedToolUse{
			ID: string(tu.ID), Root: tu.Root.String(), Tool: tu.Tool, Path: tu.Path, Bytes: tu.Bytes,
		})
	}

	return persistedSession{
		Turn:            st.Turn,
		PrefixTokens:    st.PrefixTokens,
		Segment:         st.Segment,
		PrevSegment:     st.PrevSegment,
		SegStartTurn:    st.SegStartTurn,
		SegStartPos:     st.SegStartPos,
		LastTS:          st.LastTS,
		LastToolUseID:   string(st.LastToolUseID),
		LastToolUseTurn: st.LastToolUseTurn,
		LastPromptTurn:  st.LastPromptTurn,
		SubagentSince:   st.SubagentSince,
		TodoDone:        done,
		ToolUses:        tus,
	}
}

// Persist writes the observer's state and flushes the graph. It is what the daemon's
// IdleController registers, so state survives a daemon kill between SessionEnds.
func (o *observer) Persist(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.persistState()
	o.soft(stageGraphOut, o.opt.Graph.Flush(ctx))
	return nil
}
