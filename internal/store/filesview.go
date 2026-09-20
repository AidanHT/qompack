package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// index/files.json, exported (SP-17 R5-1).
//
// The view is DERIVED: index/files.jsonl is the truth and this document is a projection of it that
// Flush rewrites. `qompack fsck` has to read the projection to compare it against its log, and
// `--repair` has to regenerate it — and until this file existed internal/cli did both through a
// private copy of the struct below, of the version constant, and of storeKey's path normalization.
// A copy nothing checks is a copy that drifts, and a drifted one makes --repair write a view every
// reader refuses. What follows is that copy's replacement: one shape, one acceptance rule, one
// writer, reached by Flush and by the repair alike.

// FilesView is the materialized index/files.json document.
type FilesView struct {
	Version   int                      `json:"version"`
	Generated core.UnixMilli           `json:"generated"`
	Files     map[string][]FileVersion `json:"files"`
}

// FilesViewVersion is the schema version index/files.json declares and this build's readers accept.
const FilesViewVersion = indexRecordVersion

// FilesLogDefect names one line of index/files.jsonl that does not parse: the line number as the
// FILE counts lines, from 1, and the reason a reader can print.
type FilesLogDefect struct {
	Line int
	Why  string
}

// ReadFilesView reads index/files.json.
//
// The bool reports whether the file EXISTS, which is a different question from whether it parsed:
// an absent view over an empty log is not a defect and must not be repaired into existence, while
// a view that is present and unreadable (unparseable, or the path is not a file) is both.
func ReadFilesView(l paths.Layout) (FilesView, bool, error) {
	raw, err := paths.ReadFileShared(filepath.Join(l.Index, filesViewNam))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FilesView{}, false, nil
		}
		return FilesView{}, true, err
	}
	var v FilesView
	if err := json.Unmarshal(raw, &v); err != nil {
		return FilesView{}, true, fmt.Errorf("index/%s does not parse: %w", filesViewNam, err)
	}
	return v, true, nil
}

// ReplayFilesLog replays index/files.jsonl into the per-path history the view is a projection of.
//
// It applies loadFiles's acceptance rule exactly: a record at a version this build does not read,
// or without a path, is skipped in silence — that is not corruption; an unparseable line is
// reported as a defect and skipped, because a truncated tail from a crash must not make the rest
// unreadable; and a missing log is an empty history rather than an error. Histories come back
// ascending by timestamp whatever order they were appended in, for the reason loadFiles sorts them.
func ReplayFilesLog(l paths.Layout) (map[string][]FileVersion, []FilesLogDefect, error) {
	raw, err := paths.ReadFileShared(filepath.Join(l.Index, filesLogFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string][]FileVersion{}, nil, nil
		}
		return nil, nil, err
	}
	hist := make(map[string][]FileVersion)
	var defects []FilesLogDefect
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r fileVersionRec
		if jsonErr := json.Unmarshal(line, &r); jsonErr != nil {
			defects = append(defects, FilesLogDefect{Line: n, Why: jsonErr.Error()})
			continue
		}
		if r.V != FilesViewVersion || r.Path == "" {
			continue
		}
		k := storeKey(r.Path)
		hist[k] = append(hist[k], FileVersion{TS: r.TS, Root: r.Root, Turn: r.Turn, Bytes: r.Bytes})
	}
	if scanErr := sc.Err(); scanErr != nil {
		// An over-long or truncated tail is malformed CONTENT, not a failure to read the file: the
		// records already replayed are good, and scanIndexJSONL keeps them for the same reason.
		defects = append(defects, FilesLogDefect{Line: 0, Why: scanErr.Error()})
	}
	for k := range hist {
		h := hist[k]
		sort.SliceStable(h, func(i, j int) bool { return h[i].TS < h[j].TS })
		hist[k] = h
	}
	return hist, defects, nil
}

// RegenerateFilesView rewrites index/files.json from index/files.jsonl when the two disagree, and
// reports whether it wrote. It is what `qompack fsck --repair` performs; nothing else calls it.
//
// It writes NOTHING when the view already describes the log, and nothing when an absent view stands
// over an empty log: regenerating the second case would create a file in a project that had nothing
// wrong with it, and a repair surface wider than the defect surface is how "no destructive default
// cleanup" starts to erode. A view that disagrees, or one that is present and unparseable, is
// replaced — replaced, not edited, because the log is the truth and the view is only ever its
// projection.
func RegenerateFilesView(l paths.Layout, clk core.Clock) (bool, error) {
	view, present, readErr := ReadFilesView(l)
	log, _, err := ReplayFilesLog(l)
	if err != nil {
		return false, err
	}
	switch {
	case present && readErr == nil && filesViewAgrees(view, log):
		return false, nil
	case !present && len(log) == 0:
		return false, nil
	}
	if err := writeFilesView(filepath.Join(l.Index, filesViewNam), FilesView{
		Version: FilesViewVersion, Generated: core.NowMilli(clk), Files: log,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// filesViewAgrees reports whether view already describes log, path for path and entry for entry.
func filesViewAgrees(view FilesView, log map[string][]FileVersion) bool {
	if view.Version != FilesViewVersion || len(view.Files) != len(log) {
		return false
	}
	for path, want := range log {
		got, ok := view.Files[path]
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
	}
	return true
}

// writeFilesView encodes v and replaces p atomically. It is the ONE writer of index/files.json:
// Flush reaches it through materializeFilesJSON and the repair through RegenerateFilesView, so the
// two cannot come to disagree about the document's shape.
//
// encoding/json sorts map keys itself, which is exactly the lexicographic path order the view is
// specified to have; the per-path slices are already ascending by TS. A nil map is written as an
// empty object rather than JSON null, so every view this package writes is readable by the same
// reader.
func writeFilesView(p string, v FilesView) error {
	if v.Files == nil {
		v.Files = map[string][]FileVersion{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(p, b, 0o600)
}
