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

// CounterPublicationSync counts SyncPublication passes: one per call that gets past the write guard,
// whether or not the pass then succeeds. A pass re-reads and verifies every object of the root's
// recovery closure and fsyncs each of them, their directories, the root and tool-use indices and the
// index directory, so passes per capture is the publication path's durability cost in a unit that
// host load cannot move (carried defect SP08-D1). The observer's publication tests pin it.
const CounterPublicationSync = "store.publication.sync"

// CounterPublicationSyncFile and CounterPublicationSyncDir count the barriers one pass issues: a
// file fsync of each verified object and of the root and tool-use indices, and a directory fsync
// (paths.SyncDir) of each directory whose entries the pass makes durable, the index directory
// included. A barrier is counted when it is issued, whether or not it then succeeds. A directory
// barrier is counted on every platform, although paths.SyncDir is a no-op on Windows, so the counts
// are the same on every host. Barriers per pass times passes per capture is SP08-D1's fsync cost in
// a unit host load cannot move.
const (
	CounterPublicationSyncFile = "store.publication.sync.file"
	CounterPublicationSyncDir  = "store.publication.sync.dir"
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
// verifies every referenced object and flushes its data, then flushes each
// directory holding one, then the root and tool-use indices and the index directory.
// Missing capabilities fail closed. OS/filesystem power-loss guarantees remain those
// of paths.SyncDir and File.Sync.
//
// Every pass is a whole re-proof: each call verifies and fsyncs the entire closure,
// whatever an earlier pass proved (owner decision D20). Within the pass, though, each
// directory is fsynced ONCE, after every object file has been verified and fsynced,
// where it used to be fsynced once per object in it: an object's fanout leaf, its
// first-level fanout directory and the objects/ root, after each object (SP08-D1).
// The two make the same entries durable before the call returns. A directory fsync
// makes durable every entry created in that directory before the call. Every entry
// this pass depends on is in place before the pass fsyncs any directory: the object's
// rename and its fanout mkdirs happened in the put that came before the pass, and
// the pass found the object there when it verified it. And nothing the pass guards
// (the intent line, the record, the capture link, the frontier) runs until the call
// has returned. So a second fsync of one directory inside a pass made nothing more
// durable. The order of the directory fsyncs inside the pass carries nothing either,
// for the same reason: no step depends on a directory until they have all completed.
// A power loss inside the pass loses nothing the pass promised, because the pass has
// not returned and its caller has not taken the step it guards.
func (s *FSStore) SyncPublication(ctx context.Context, hash core.Hash) error {
	if err := s.mutate(); err != nil {
		return err
	}
	s.count(CounterPublicationSync, 1)
	objects, err := s.publicationObjects(ctx, hash)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(paths.Long(s.l.Objects))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	dirs := newPublicationDirs(s.l.Objects, len(objects))
	for h := range objects {
		if _, err := s.GetChunk(ctx, h); err != nil {
			return err
		}
		dir, err := s.syncPublicationObject(root, h)
		if err != nil {
			return err
		}
		dirs.add(dir)
	}
	for _, dir := range dirs.order {
		if err := s.syncPublicationDir(dir); err != nil {
			return err
		}
	}
	for _, writer := range []*appendFile{s.rootsW, s.tuW} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.syncPublicationIndex(writer); err != nil {
			return err
		}
	}
	return s.syncPublicationDir(s.l.Index)
}

// syncPublicationDir issues one of the pass's directory fsyncs and counts it.
func (s *FSStore) syncPublicationDir(dir string) error {
	s.count(CounterPublicationSyncDir, 1)
	if s.pubSyncDir != nil {
		return s.pubSyncDir(dir)
	}
	return paths.SyncDir(dir)
}

func (s *FSStore) syncPublicationIndex(writer *appendFile) error {
	if writer == nil {
		return core.ErrDegraded
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	sync, ok := writer.w.(syncer)
	if !ok {
		return core.ErrDegraded
	}
	s.count(CounterPublicationSyncFile, 1)
	return sync.Sync()
}

// publicationDirs is the set of directories one SyncPublication pass fsyncs: the directory of each
// object file the pass synced and every ancestor of it up to the objects/ root, each once, in the
// order first reached.
type publicationDirs struct {
	top   string
	seen  map[string]struct{}
	order []string
}

// fanoutLevels is how many fanout directories sit between the objects/ root and an object file
// (objects/ab/cd/<hash>, Qompack.md §7.4). It sizes publicationDirs up front, so a pass over n
// objects allocates its set once rather than regrowing it; a wrong value costs only a regrowth.
const fanoutLevels = 2

func newPublicationDirs(top string, objects int) *publicationDirs {
	n := fanoutLevels*objects + 1
	return &publicationDirs{top: top, seen: make(map[string]struct{}, n), order: make([]string, 0, n)}
}

// add records dir and each ancestor of it up to top. A directory only ever enters the set together
// with all of its ancestors up to top, so the walk stops at the first directory already there. A
// directory outside top ends its walk at the volume root, whose parent is itself.
func (d *publicationDirs) add(dir string) {
	for {
		if _, ok := d.seen[dir]; ok {
			return
		}
		d.seen[dir] = struct{}{}
		d.order = append(d.order, dir)
		if dir == d.top {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// syncPublicationObject fsyncs h's object file and returns the directory holding it, which the
// caller fsyncs once for the pass.
func (s *FSStore) syncPublicationObject(root *os.Root, h core.Hash) (string, error) {
	for _, path := range s.objectCandidates(h) {
		rel, err := filepath.Rel(s.l.Objects, path)
		if err != nil {
			return "", err
		}
		info, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", core.ErrDegraded
		}
		// Windows FlushFileBuffers needs a handle opened for writing.
		f, err := root.OpenFile(rel, os.O_RDWR, 0)
		if err != nil {
			return "", err
		}
		opened, statErr := f.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			_ = f.Close()
			return "", core.ErrDegraded
		}
		s.count(CounterPublicationSyncFile, 1)
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return "", err
		}
		return filepath.Dir(path), nil
	}
	return "", core.ErrNotFound
}
