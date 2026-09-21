package store

import (
	"encoding/json"
	"os"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Supersession is the only mutable part of a tool record. Bind all other
// metadata, so retaining a root hash cannot disguise a different target.
func observationRecordDigest(rec ToolUseRecord) core.Hash {
	rec.Status, rec.SupersededBy = StatusOK, ""
	data, _ := json.Marshal(recToTU(rec)) // this wire struct has no fallible values
	return core.HashBytes("qompack.observation.record.v1", data)
}

func (s *FSStore) openObservationIndex() (*os.Root, error) {
	dot, err := os.OpenRoot(paths.Long(s.l.Dot))
	if err != nil {
		return nil, err
	}
	defer func() { _ = dot.Close() }()
	info, err := dot.Lstat("index")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, core.ErrDegraded
	}
	index, err := dot.OpenRoot("index")
	if err != nil {
		return nil, err
	}
	opened, err := index.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = index.Close()
		return nil, core.ErrDegraded
	}
	return index, nil
}
