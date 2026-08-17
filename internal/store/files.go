package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// fileVersionRec is one index/files.jsonl line: the append-only truth behind the index/files.json
// view Qompack.md §7.4 names.
//
// The log-plus-view split is the same pattern 00-ARCHITECTURE.md §3.3 mandates for
// pins/invariants.jsonl → invariants.json, and for the same reason: rewriting the whole map on
// every file read would blow budget B-C, so the hot path appends one line and the view is
// regenerated only on Flush.
type fileVersionRec struct {
	V     int            `json:"v"`
	Path  string         `json:"path"`
	TS    core.UnixMilli `json:"ts"`
	Turn  core.TurnIndex `json:"turn"`
	Root  core.Hash      `json:"root"`
	Bytes int64          `json:"bytes"`
}

// filesView is the materialized index/files.json document.
type filesView struct {
	Version   int                      `json:"version"`
	Generated core.UnixMilli           `json:"generated"`
	Files     map[string][]FileVersion `json:"files"`
}

// filesLogPath is index/files.jsonl, the append-only log.
func (s *FSStore) filesLogPath() string { return filepath.Join(s.l.Index, filesLogFile) }

// filesViewPath is index/files.json, the materialized view.
func (s *FSStore) filesViewPath() string { return filepath.Join(s.l.Index, filesViewNam) }

// loadFiles replays index/files.jsonl into the in-memory per-path version history.
//
// The log is the truth, not the view: a crash between the last append and the next Flush leaves
// files.json stale, and rebuilding from the log is what makes that survivable.
func (s *FSStore) loadFiles() error {
	bad, err := scanIndexJSONL(s.filesLogPath(), func(line []byte) (bool, error) {
		var r fileVersionRec
		if err := json.Unmarshal(line, &r); err != nil {
			return false, err
		}
		if r.V != indexRecordVersion || r.Path == "" {
			return false, nil
		}
		k := storeKey(r.Path)
		s.fileHist[k] = append(s.fileHist[k], FileVersion{
			TS: r.TS, Root: r.Root, Turn: r.Turn, Bytes: r.Bytes,
		})
		return true, nil
	})
	if err != nil {
		return err
	}
	// Versions are read back ascending by TS regardless of the order they were appended in, so a
	// hook that recorded two reads out of order cannot make FileHistory non-monotonic.
	for k := range s.fileHist {
		hist := s.fileHist[k]
		sort.SliceStable(hist, func(i, j int) bool { return hist[i].TS < hist[j].TS })
		s.fileHist[k] = hist
	}
	if bad > 0 {
		s.count("store.index.badline", int64(bad))
		s.log.Warn("store: skipped malformed file-version index lines", "file", s.filesLogPath(), "lines", bad)
	}
	return nil
}

// AppendFileVersion records that path had content v.Root at v.TS (Qompack.md §8.2 file version
// history: "what did this file look like when we made that decision").
//
// Appending the same Root at the same Turn twice is a no-op, so replaying a daemon WAL after a
// crash cannot duplicate a version. A different Turn with the same Root IS recorded: the same
// bytes observed at two different points in the session are two observations, and §8.3's
// staleness comparison reads the newest one.
func (s *FSStore) AppendFileVersion(ctx context.Context, path string, v FileVersion) error {
	if err := s.use(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.Root.IsZero() {
		return fmt.Errorf("%w: file version for %s has no root hash", core.ErrNotFound, path)
	}
	k := storeKey(path)
	if k == "" {
		return fmt.Errorf("%w: file version has no path", core.ErrNotFound)
	}

	s.mu.Lock()
	hist := s.fileHist[k]
	if n := len(hist); n > 0 && hist[n-1].Root == v.Root && hist[n-1].Turn == v.Turn {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	line, err := marshalLine(fileVersionRec{
		V: indexRecordVersion, Path: k, TS: v.TS, Turn: v.Turn, Root: v.Root, Bytes: v.Bytes,
	})
	if err != nil {
		return err
	}
	if err := s.filesW.write(line); err != nil {
		return err
	}

	s.mu.Lock()
	s.fileHist[k] = append(s.fileHist[k], v)
	s.filesDirty = true
	s.mu.Unlock()
	return nil
}

// FileHistory returns every recorded version of path, ascending by timestamp.
func (s *FSStore) FileHistory(ctx context.Context, path string) ([]FileVersion, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	hist, ok := s.fileHist[storeKey(path)]
	if !ok || len(hist) == 0 {
		return nil, fmt.Errorf("%w: no file history for %s", core.ErrNotFound, path)
	}
	out := make([]FileVersion, len(hist))
	copy(out, hist)
	return out, nil
}

// FileAt returns the version of path that was current at at — the last version recorded at or
// before that instant. A zero at means "the latest version".
func (s *FSStore) FileAt(ctx context.Context, path string, at time.Time) (FileVersion, error) {
	if err := s.use(); err != nil {
		return FileVersion{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	hist := s.fileHist[storeKey(path)]
	if len(hist) == 0 {
		return FileVersion{}, fmt.Errorf("%w: no file history for %s", core.ErrNotFound, path)
	}
	if at.IsZero() {
		return hist[len(hist)-1], nil
	}
	cutoff := core.UnixMilli(at.UnixMilli())
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].TS <= cutoff {
			return hist[i], nil
		}
	}
	return FileVersion{}, fmt.Errorf("%w: no version of %s at or before %s", core.ErrNotFound, path, at)
}

// ChangedSince returns the subset of deps whose current file-version hash DIFFERS from the hash
// recorded in the dep (00-ARCHITECTURE.md §8.3, Qompack.md §8.3 items 2–3).
//
// Three rules, and the third is the one consumers must code against:
//
//  1. history exists and the newest Root differs from dep.Hash → changed;
//  2. history exists and the newest Root equals dep.Hash → unchanged;
//  3. NO history for the path → UNCHANGED, plus store.changed_since.unknown_path.
//
// Rule 3 is deliberate. "Changed" means the hash differs, and the absence of a recorded version
// is not a difference — it is an absence of evidence. Reporting unknown paths as changed would
// flip every scope:"project" elimination to stale on the first session against a fresh clone with
// an empty store, destroying the feature §8.3 calls "the highest-value single feature in the
// plugin". The counter keeps the situation visible to /qompack:status instead.
//
// The result preserves input order and carries the INPUT Dep values unchanged, so a caller can
// correlate them positionally with whatever it derived them from.
func (s *FSStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var changed []core.Dep
	var unknown int64
	for _, d := range deps {
		hist := s.fileHist[storeKey(d.Path)]
		if len(hist) == 0 {
			unknown++
			continue // rule 3
		}
		if hist[len(hist)-1].Root != d.Hash {
			changed = append(changed, d) // rule 1
		}
		// rule 2: equal hashes contribute nothing
	}
	if unknown > 0 {
		s.count("store.changed_since.unknown_path", unknown)
	}
	return changed, nil
}

// materializeFilesJSON regenerates index/files.json from the in-memory history. Flush calls it;
// nothing on the hot path does.
func (s *FSStore) materializeFilesJSON() error {
	s.mu.RLock()
	view := filesView{
		Version:   indexRecordVersion,
		Generated: s.now(),
		Files:     make(map[string][]FileVersion, len(s.fileHist)),
	}
	for k, hist := range s.fileHist {
		cp := make([]FileVersion, len(hist))
		copy(cp, hist)
		view.Files[k] = cp
	}
	s.mu.RUnlock()

	// encoding/json sorts map keys itself, which is exactly the lexicographic path order the view
	// is specified to have; the per-path slices are already ascending by TS.
	b, err := json.Marshal(view)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(s.filesViewPath(), b, 0o600)
}
