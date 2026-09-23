package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

// Fix the mutable frontier before copying any content. A file may change before
// its own turn in the copy walk and then remain stable through the post-check;
// comparing only copied and post-check bytes would miss that change.
func (m *Migrator) snapshotBackupWriters(ctx context.Context) (map[string]BackupFile, error) {
	names := append([]string(nil), backupLiveWriterFiles...)
	segmentNames, err := m.backupSegmentNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: segment enumeration: %v", ErrBackupMoved, err)
	}
	names = append(names, segmentNames...)
	out := make(map[string]BackupFile, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(m.l.Dot, filepath.FromSlash(name))
		info, err := os.Lstat(paths.Long(path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: unreadable watched file %s", ErrBackupMoved, name)
		}
		digest, err := backupFileDigest(ctx, path, info)
		if err != nil {
			return nil, err
		}
		out[name] = BackupFile{Name: name, Size: info.Size(), SHA256: digest}
	}
	return out, nil
}

// Enumerate only the two-level segment layout in bounded batches. WalkDir reads
// an entire directory before invoking its callback, so a callback count limit
// alone would not bound the memory needed to discover a hostile directory.
func (m *Migrator) backupSegmentNames(ctx context.Context) ([]string, error) {
	dir := filepath.Join(m.l.State, deliverySegmentsDirectory)
	info, err := os.Lstat(paths.Long(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || maintNoFollow(m.root, dir) != nil {
		return nil, ErrBackupMoved
	}
	root, err := os.OpenRoot(paths.Long(dir))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrBackupMoved
	}
	const batch = 64 //nomagic:allow bounded directory enumeration batch
	count := 0
	var names []string
	var visit func(string, bool) error
	visit = func(rel string, insideSegment bool) error {
		before, err := root.Lstat(rel)
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return ErrBackupMoved
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		after, err := f.Stat()
		if err != nil || !os.SameFile(before, after) {
			return ErrBackupMoved
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, readErr := f.ReadDir(batch)
			for _, entry := range entries {
				count++
				if count > maintMaxManifestFiles || entry.Type()&os.ModeSymlink != 0 {
					return ErrBackupMoved
				}
				child := filepath.Join(rel, entry.Name())
				if entry.IsDir() {
					if insideSegment {
						return ErrBackupMoved
					}
					if err := visit(child, true); err != nil {
						return err
					}
					continue
				}
				name := "state/" + deliverySegmentsDirectory + "/" + filepath.ToSlash(child)
				if backupSegmentMutableFile(name) {
					names = append(names, name)
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	if err := visit(".", false); err != nil {
		return nil, err
	}
	return names, nil
}

// Each committed rotation changes the watched authority. Also compare the
// captured mutable files of every segment: an active segment can append without
// rotating. Immutable history files may be compared too, conservatively.
func backupSegmentMutableFile(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "state" || parts[1] != deliverySegmentsDirectory {
		return false
	}
	switch parts[3] {
	case deliveryLeaseFile, deliveryAckFile, deliveryLeasePositionFile, deliveryAckPositionFile, dcarryFile:
		return true
	default:
		return false
	}
}
