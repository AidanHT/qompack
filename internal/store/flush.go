package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// storeStateFile holds the two cumulative counters that cannot be recovered exactly from the
// append-only indices alone: compressed bytes actually written, and the pre-dedup raw total.
const storeStateFile = "store.json"

// storeState is the on-disk shape of <root>/.qompack/state/store.json.
type storeState struct {
	Version  int            `json:"version"`
	Bytes    int64          `json:"bytes"`
	RawBytes int64          `json:"rawbytes"`
	Objects  int            `json:"objects"`
	Updated  core.UnixMilli `json:"updated"`
}

// sessionRecord is one index/sessions.jsonl line.
//
// Every counter is scoped to THIS SESSION, deliberately, not to the whole store: a store-wide
// snapshot would change whenever any other session wrote, which would re-dirty and re-append every
// session on every Flush and make "append only what actually moved" meaningless.
type sessionRecord struct {
	V        int            `json:"v"`
	S        core.SessionID `json:"s"`
	Start    core.UnixMilli `json:"start"`
	End      core.UnixMilli `json:"end"`
	Turns    int            `json:"turns"`
	ToolUses int            `json:"tooluses"`
	Roots    int            `json:"roots"`
	Objects  int            `json:"objects"`
	Bytes    int64          `json:"bytes"`
	RawBytes int64          `json:"rawbytes"`
	Dedup    float64        `json:"dedup"`
}

// Flush durably persists every buffered write. It is the mechanics SP-08's SessionEnd and SP-05's
// idle loop invoke; SP-06 does not own those entry points.
//
// The order is fixed: sync the open append-only handles first (so nothing already written can be
// lost), then materialize the derived views, then persist the estimator's caches, then the store
// counters, then the session index.
func (s *FSStore) Flush(ctx context.Context) error {
	if err := s.use(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 1. Durably flush every open append-only handle. paths.AppendOnly returns an io.WriteCloser,
	// not an *os.File, so appendFile.sync asserts for a Sync method and skips silently when the
	// handle has none: a wrapper that buffers is still correct, merely not fsynced, and that must
	// never be the reason SessionEnd fails.
	for _, a := range []*appendFile{s.rootsW, s.tuW, s.filesW, s.sessionsW} {
		if a != nil {
			note(a.sync())
		}
	}
	if s.seg != nil {
		note(s.seg.sync())
	}

	// 2. Materialize index/files.json, the view §7.4 names over the files.jsonl log.
	s.mu.RLock()
	filesDirty := s.filesDirty
	s.mu.RUnlock()
	if filesDirty {
		if err := s.materializeFilesJSON(); err != nil {
			note(err)
		} else {
			s.mu.Lock()
			s.filesDirty = false
			s.mu.Unlock()
		}
	}

	// 3. Persist the token chunk cache and the calibration factor.
	if f, ok := s.deps.Tokens.(interface{ Flush() error }); ok {
		note(f.Flush())
	}

	// 4. Persist the cumulative store counters.
	note(s.persistStoreState())

	// 5. Append a session record for every session whose counters moved since its last record.
	note(s.flushSessions())

	return firstErr
}

// flushSessions recomputes each session's counters from the tool_use index and appends a record
// for every session that actually changed. Re-running Flush with no work in between appends
// nothing, which is what makes Flush idempotent.
func (s *FSStore) flushSessions() error {
	current := s.recomputeSessions()

	s.mu.Lock()
	var changed []*sessionEntry
	for id, next := range current {
		prev, ok := s.sessions[id]
		if ok && !sessionChanged(prev, next) {
			continue
		}
		next.dirty = false
		s.sessions[id] = next
		changed = append(changed, next)
	}
	s.mu.Unlock()

	sort.Slice(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })
	for _, e := range changed {
		rec := sessionRecord{
			V: indexRecordVersion, S: e.ID, Start: e.Start, End: e.End,
			Turns: e.Turns, ToolUses: e.ToolUses, Roots: e.Roots, Objects: e.Objects,
			Bytes: e.Bytes, RawBytes: e.RawBytes, Dedup: e.Dedup,
		}
		line, err := marshalLine(rec)
		if err != nil {
			return err
		}
		if err := s.sessionsW.write(line); err != nil {
			return err
		}
	}
	return nil
}

// sessionChanged reports whether any persisted counter of b differs from a.
func sessionChanged(a, b *sessionEntry) bool {
	return a.Start != b.Start || a.End != b.End || a.Turns != b.Turns ||
		a.ToolUses != b.ToolUses || a.Roots != b.Roots || a.Objects != b.Objects ||
		a.Bytes != b.Bytes || a.RawBytes != b.RawBytes
}

// recomputeSessions derives every session's counters from the in-memory tool_use index.
//
// Deriving rather than accumulating is deliberate: PutOptions carries no session, so a Put cannot
// attribute itself to one, and the tool_use index is the only place the session/turn/root triple
// is actually recorded. It also means a rebuilt index yields identical session records.
func (s *FSStore) recomputeSessions() map[core.SessionID]*sessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[core.SessionID]*sessionEntry)
	roots := make(map[core.SessionID]map[core.Hash]struct{})
	chunks := make(map[core.SessionID]map[core.Hash]struct{})
	turns := make(map[core.SessionID]map[core.TurnIndex]struct{})

	for _, rec := range s.toolUse {
		id := rec.Session
		if id == "" {
			continue
		}
		e, ok := out[id]
		if !ok {
			e = &sessionEntry{ID: id, Start: rec.TS, End: rec.TS}
			out[id] = e
			roots[id] = make(map[core.Hash]struct{})
			chunks[id] = make(map[core.Hash]struct{})
			turns[id] = make(map[core.TurnIndex]struct{})
		}
		if rec.TS < e.Start {
			e.Start = rec.TS
		}
		if rec.TS > e.End {
			e.End = rec.TS
		}
		e.ToolUses++
		e.RawBytes += rec.Bytes
		turns[id][rec.Turn] = struct{}{}

		if rec.Root.IsZero() {
			continue
		}
		if _, seen := roots[id][rec.Root]; seen {
			continue
		}
		roots[id][rec.Root] = struct{}{}
		if entry, ok := s.rootIndex[rec.Root]; ok {
			for _, c := range entry.Root.Chunks {
				chunks[id][c.Hash] = struct{}{}
			}
		}
	}

	for id, e := range out {
		e.Turns = len(turns[id])
		e.Roots = len(roots[id])
		e.Objects = len(chunks[id])
		// Bytes is the session's own deduplicated chunk footprint: every distinct chunk reachable
		// from a root this session recorded, counted once.
		var stored int64
		for h := range chunks[id] {
			stored += int64(s.chunkSet[h])
		}
		e.Bytes = stored
		if stored > 0 {
			e.Dedup = float64(e.RawBytes) / float64(stored)
		}
	}
	return out
}

// loadSessions replays index/sessions.jsonl into s.sessions, last record winning per session.
func (s *FSStore) loadSessions() error {
	p := filepath.Join(s.l.Index, sessionsFile)
	f, err := os.Open(paths.Long(p))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	bad := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(line, &rec); err != nil || rec.S == "" {
			bad++
			continue
		}
		s.sessions[rec.S] = &sessionEntry{
			ID: rec.S, Start: rec.Start, End: rec.End, Turns: rec.Turns,
			ToolUses: rec.ToolUses, Roots: rec.Roots, Objects: rec.Objects,
			Bytes: rec.Bytes, RawBytes: rec.RawBytes, Dedup: rec.Dedup,
		}
	}
	if bad > 0 {
		s.count("store.index.badline", int64(bad))
		s.log.Warn("store: skipped malformed sessions.jsonl lines", "count", bad, "file", p)
	}
	return sc.Err()
}

// RecentSessions returns the n most recent session IDs, newest first, as ordered by End then
// Start. GC's "10 sessions" retention axis reads this.
func (s *FSStore) RecentSessions(n int) []core.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]*sessionEntry, 0, len(s.sessions))
	for _, e := range s.sessions {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].End != all[j].End {
			return all[i].End > all[j].End
		}
		if all[i].Start != all[j].Start {
			return all[i].Start > all[j].Start
		}
		return all[i].ID > all[j].ID
	})
	if n >= 0 && n < len(all) {
		all = all[:n]
	}
	out := make([]core.SessionID, len(all))
	for i, e := range all {
		out[i] = e.ID
	}
	return out
}

// persistStoreState writes state/store.json when a counter has moved.
func (s *FSStore) persistStoreState() error {
	s.mu.RLock()
	dirty := s.statsDirty
	st := storeState{
		Version: indexRecordVersion, Bytes: s.bytesOnDisk, RawBytes: s.rawBytes,
		Objects: len(s.chunkSet), Updated: s.now(),
	}
	s.mu.RUnlock()
	if !dirty {
		return nil
	}

	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := paths.WriteAtomic(filepath.Join(s.l.State, storeStateFile), b, 0o600); err != nil {
		return err
	}
	s.mu.Lock()
	s.statsDirty = false
	s.mu.Unlock()
	return nil
}

// loadStoreState seeds the cumulative counters from state/store.json, recomputing what it can when
// the file is absent: Bytes by walking objects/, RawBytes by summing the roots index. Neither
// recovery is exact — that is precisely why the file exists — but both beat starting at zero.
func (s *FSStore) loadStoreState() {
	p := filepath.Join(s.l.State, storeStateFile)
	if b, err := os.ReadFile(paths.Long(p)); err == nil {
		var st storeState
		if err := json.Unmarshal(b, &st); err == nil {
			s.bytesOnDisk, s.rawBytes = st.Bytes, st.RawBytes
			return
		}
		s.log.Loud("store: state/store.json is unreadable; recomputing size counters", "file", p)
	}

	var total int64
	_ = filepath.WalkDir(paths.Long(s.l.Objects), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a partially readable objects/ still yields a usable total
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	s.bytesOnDisk = total
	for _, e := range s.rootIndex {
		s.rawBytes += e.Root.RawBytes
	}
}
