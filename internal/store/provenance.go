package store

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// ContentOrigin identifies a recorded producer of archived bytes. It is not an
// authorization grant. Consumers must apply current policy to every origin.
type ContentOrigin struct {
	Path string
	Tool string
}

// ProvenanceReader is an additive read capability; the frozen Store interface
// and historical JSON records are unchanged. Missing support is not permission.
type ProvenanceReader interface {
	ContentOrigins(context.Context, core.Hash) ([]ContentOrigin, error)
}

// maxProvenanceEntries bounds work across roots, chunks, tool uses and file
// history. Crossing it reports unavailable rather than authorizing a partial set.
const maxProvenanceEntries = 65536

// ContentOrigins resolves a root or chunk address without reading its payload.
// All recorded origins are returned, including restrictive ones after dedup.
func (s *FSStore) ContentOrigins(ctx context.Context, hash core.Hash) ([]ContentOrigin, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	remaining := maxProvenanceEntries
	step := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining--
		if remaining < 0 {
			return core.ErrDegraded
		}
		return nil
	}
	roots := make(map[core.Hash]bool)
	seen := make(map[ContentOrigin]bool)
	var origins []ContentOrigin
	add := func(path, tool string) {
		o := ContentOrigin{Path: path, Tool: tool}
		if !seen[o] {
			seen[o] = true
			origins = append(origins, o)
		}
	}
	for id, entry := range s.rootIndex {
		if err := step(); err != nil {
			return nil, err
		}
		matches := id == hash
		for _, chunk := range entry.Root.Chunks {
			if err := step(); err != nil {
				return nil, err
			}
			matches = matches || chunk.Hash == hash
		}
		if matches {
			roots[id] = true
			add(entry.Path, entry.Tool)
		}
	}
	for _, rec := range s.toolUse {
		if err := step(); err != nil {
			return nil, err
		}
		if roots[rec.Root] {
			add(rec.Path, rec.Tool)
		}
	}
	for path, versions := range s.fileHist {
		for _, version := range versions {
			if err := step(); err != nil {
				return nil, err
			}
			if roots[version.Root] {
				add(path, "Read")
			}
		}
	}
	if len(origins) == 0 {
		return nil, core.ErrNotFound
	}
	return origins, nil
}
