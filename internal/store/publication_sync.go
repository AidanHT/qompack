package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// PublicationSync flushes a root's recovery closure before its publication link.
// It is additive: legacy Store implementations cannot claim this guarantee.
type PublicationSync interface {
	SyncPublication(context.Context, core.Hash) error
}

const (
	publicationRootLimit   = 4096 //nomagic:allow recovery-closure safety bound, not a configuration default.
	publicationObjectLimit = 65536
)

func (s *FSStore) publicationObjects(ctx context.Context, root core.Hash) (map[core.Hash]bool, error) {
	if root.IsZero() {
		return nil, ctx.Err() // metadata-only capture; its index still needs syncing
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[core.Hash]bool)
	objects := make(map[core.Hash]bool)
	queue := []core.Hash{root}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[h] {
			continue
		}
		seen[h] = true
		entry := s.rootIndex[h]
		if entry == nil {
			return nil, core.ErrNotFound
		}
		for _, chunk := range entry.Root.Chunks {
			objects[chunk.Hash] = true
			if len(objects) > publicationObjectLimit {
				return nil, core.ErrDegraded
			}
		}
		if len(seen) > publicationRootLimit || len(objects) > publicationObjectLimit {
			return nil, core.ErrDegraded
		}
		for _, related := range []core.Hash{entry.Deltas, entry.Orig, entry.Base} {
			if !related.IsZero() && !seen[related] {
				queue = append(queue, related)
			}
		}
	}
	return objects, nil
}

// SyncPublication uses the existing single-writer/GC ownership contract. It
// verifies every referenced object, flushes its data and containing directories,
// then flushes the root and tool-use indices. Missing capabilities fail closed.
// OS/filesystem power-loss guarantees remain those of paths.SyncDir and File.Sync.
func (s *FSStore) SyncPublication(ctx context.Context, hash core.Hash) error {
	if err := s.mutate(); err != nil {
		return err
	}
	objects, err := s.publicationObjects(ctx, hash)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(paths.Long(s.l.Objects))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for h := range objects {
		if _, err := s.GetChunk(ctx, h); err != nil {
			return err
		}
		if err := s.syncPublicationObject(root, h); err != nil {
			return err
		}
	}
	for _, writer := range []*appendFile{s.rootsW, s.tuW} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := syncPublicationIndex(writer); err != nil {
			return err
		}
	}
	return paths.SyncDir(s.l.Index)
}

func syncPublicationIndex(writer *appendFile) error {
	if writer == nil {
		return core.ErrDegraded
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	sync, ok := writer.w.(syncer)
	if !ok {
		return core.ErrDegraded
	}
	return sync.Sync()
}

func (s *FSStore) syncPublicationObject(root *os.Root, h core.Hash) error {
	for _, path := range s.objectCandidates(h) {
		rel, err := filepath.Rel(s.l.Objects, path)
		if err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return core.ErrDegraded
		}
		// Windows FlushFileBuffers needs a handle opened for writing.
		f, err := root.OpenFile(rel, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		opened, statErr := f.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			_ = f.Close()
			return core.ErrDegraded
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			if err := paths.SyncDir(dir); err != nil {
				return err
			}
			if dir == s.l.Objects {
				return nil
			}
		}
	}
	return core.ErrNotFound
}
