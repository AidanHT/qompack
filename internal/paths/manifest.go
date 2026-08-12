package paths

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
)

// checkpointFilePattern is the printf pattern every checkpoint artifact's filename follows: four
// zero-padded digits, e.g. "0007.json" for seq 7 (§8.5).
const checkpointFilePattern = "%04d.json"

// CheckpointPath returns the path of the checkpoint artifact for seq under l.Checkpoints, e.g.
// <checkpoints>/0007.json.
func CheckpointPath(l Layout, seq core.CheckpointSeq) string {
	return filepath.Join(l.Checkpoints, fmt.Sprintf(checkpointFilePattern, int(seq)))
}

// ManifestPath returns checkpoints/MANIFEST.jsonl under l. It deliberately lives inside
// l.Checkpoints so IsProtected covers it exactly as it covers every checkpoint artifact, which
// makes AppendManifest the only function that can ever write to it.
func ManifestPath(l Layout) string {
	return filepath.Join(l.Checkpoints, "MANIFEST.jsonl")
}

// ManifestEntry is one line of checkpoints/MANIFEST.jsonl: the record `qompack fsck` re-hashes
// every checkpoint artifact against (§3.3).
type ManifestEntry struct {
	Seq     core.CheckpointSeq `json:"seq"`
	SHA256  string             `json:"sha256"`
	Bytes   int64              `json:"bytes"`
	Created core.UnixMilli     `json:"created"`
}

// AppendManifest appends e to checkpoints/MANIFEST.jsonl through AppendJSONL, the only legal way
// to write it: MANIFEST.jsonl lives under checkpoints/, so IsProtected refuses every other write
// path into it.
func AppendManifest(l Layout, e ManifestEntry) error {
	return AppendJSONL(ManifestPath(l), e)
}

// ReadManifest reads every entry of checkpoints/MANIFEST.jsonl back, in file order. A manifest
// that does not exist yet (no checkpoint has ever been written) returns a nil slice and a nil
// error, not a failure.
func ReadManifest(l Layout) ([]ManifestEntry, error) {
	f, err := os.Open(Long(ManifestPath(l)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []ManifestEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e ManifestEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("paths: ReadManifest: %w", err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
