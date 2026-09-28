//go:build linux || darwin

package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestGetChunk_RefusesASymlinkedObjectEvenWithValidBytes replaces an object with a symbolic link to
// a byte-identical copy outside the store. The content hash alone would accept those bytes — they
// are the right bytes — so this is the test that the leaf check is not vacuous: an object path is
// read only when it is itself a regular file, never through a link.
func TestGetChunk_RefusesASymlinkedObjectEvenWithValidBytes(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, bytes.Repeat([]byte("linked object content\n"), 16), PutOptions{Path: "src/linked.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1)
	h := res.Root.Chunks[0].Hash
	want, err := tp.Store.GetChunk(ctx, h)
	require.NoError(t, err, "fixture sanity: the object reads before it is replaced")

	obj := tp.Store.objectPath(h)
	raw, err := os.ReadFile(obj)
	require.NoError(t, err)
	outside := filepath.Join(t.TempDir(), "valid-copy")
	require.NoError(t, os.WriteFile(outside, raw, 0o600))
	require.NoError(t, os.Remove(obj))
	require.NoError(t, os.Symlink(outside, obj))

	got, err := tp.Store.GetChunk(ctx, h)
	require.ErrorIs(t, err, ErrDamaged, "a linked object must be refused, even though its target holds %d valid bytes", len(want))
	require.Empty(t, got)
	fi, err := os.Lstat(obj)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSymlink, "a refused link was never read, so it is left in place")
}

// TestReadBoundedObject_RefusesASymlinkLeaf refuses a link to a regular file at the leaf.
func TestReadBoundedObject_RefusesASymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(paths.Long(target), []byte("bytes behind a link"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))

	_, err := readBoundedObject(link, 1<<20)
	require.True(t, errorsIsAny(err, errObjectNotRegular, syscall.ELOOP), "got %v", err)
	require.False(t, os.IsNotExist(err))
}

// TestReadBoundedObject_RefusesAFIFOLeafWithoutBlocking puts a named pipe with no writer at the
// leaf. Opening it for reading without O_NONBLOCK would wait for a writer forever; the leaf check
// must refuse it and return.
func TestReadBoundedObject_RefusesAFIFOLeafWithoutBlocking(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(p, 0o600))

	done := make(chan error, 1)
	go func() {
		_, err := readBoundedObject(p, 1<<20)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, errObjectNotRegular)
	case <-time.After(10 * time.Second):
		t.Fatal("readBoundedObject blocked on a FIFO leaf")
	}
}

// TestOpenObjectChecked_NoFollowRefusesALinkTheLstatDidNotSee drives the open half of the unix
// leaf check directly, as it would run if the leaf became a link after the Lstat: with the
// no-follow flags the open itself refuses the link instead of following it.
func TestOpenObjectChecked_NoFollowRefusesALinkTheLstatDidNotSee(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("bytes behind a link"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))

	f, err := os.OpenFile(link, os.O_RDONLY|objectOpenFlags, 0)
	if f != nil {
		_ = f.Close()
	}
	require.ErrorIs(t, err, syscall.ELOOP, "the object open must never follow a link at the leaf")
}

// errorsIsAny reports whether err matches any of targets; the unix leaf tests accept either of
// the two refusals a link or FIFO can meet, depending on which check sees it first.
func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
