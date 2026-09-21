package store

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// readExactSize and readBoundedObject are the W1 pre-sized read: one buffer sized from the Stat'd
// length, plus a trailing-byte probe that keeps the growth/append detection the old
// io.ReadAll(io.LimitReader(f, limit+1)) provided. These tests pin the truncation and growth handling
// deterministically against crafted readers — a real file cannot be made to grow mid-read on demand —
// and confirm the filesystem bounds through readBoundedObject itself. They change no integrity check:
// the plaintext content hash, the indexed-size check and the Lstat/fstat/SameFile path checks are all
// downstream or upstream of this buffer and are exercised by the existing corruption tests.

// This file reuses the package's existing errReader (ingest_edge_test.go): a pointer errReader with
// hands back its (empty) prefix and then the error on the first read, which is the truncated-pipe
// shape, and here stands in for a read that fails rather than a short object.

func TestReadExactSize(t *testing.T) {
	t.Run("exact_size_reads_and_requires_eof", func(t *testing.T) {
		want := []byte("exactly these bytes")
		got, err := readExactSize(bytes.NewReader(want), int64(len(want)))
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("a_short_reader_is_a_shrink", func(t *testing.T) {
		// Ask for one more byte than the reader holds: ReadFull sees EOF before the buffer is full.
		_, err := readExactSize(bytes.NewReader([]byte("four")), 5)
		require.ErrorContains(t, err, "shorter than its recorded size")
	})

	t.Run("a_trailing_byte_is_a_growth", func(t *testing.T) {
		// The reader holds more than the recorded size: the probe must catch the surplus, or an
		// appended suffix would be accepted under the valid prefix's hash.
		_, err := readExactSize(bytes.NewReader([]byte("prefix+suffix")), int64(len("prefix")))
		require.ErrorContains(t, err, "grew past its recorded size")
	})

	t.Run("zero_size_empty_reader_is_ok", func(t *testing.T) {
		got, err := readExactSize(bytes.NewReader(nil), 0)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("zero_size_with_bytes_is_a_growth", func(t *testing.T) {
		_, err := readExactSize(bytes.NewReader([]byte("x")), 0)
		require.ErrorContains(t, err, "grew past its recorded size")
	})

	t.Run("a_read_error_is_propagated", func(t *testing.T) {
		boom := errors.New("disk went away")
		_, err := readExactSize(&errReader{err: boom}, 4)
		require.ErrorIs(t, err, boom)
	})
}

func TestRequireEOF(t *testing.T) {
	require.NoError(t, requireEOF(bytes.NewReader(nil)), "an exhausted reader is at EOF")
	require.ErrorContains(t, requireEOF(bytes.NewReader([]byte("more"))), "grew past its recorded size")

	boom := errors.New("read failed")
	require.ErrorIs(t, requireEOF(&errReader{err: boom}), boom, "a non-EOF probe error is propagated")
	require.ErrorIs(t, requireEOF(noProgressObjectReader{}), io.ErrNoProgress,
		"a zero-byte successful read is not proof of EOF")
}

type noProgressObjectReader struct{}

func (noProgressObjectReader) Read([]byte) (int, error) { return 0, nil }

func TestReadBoundedObject_FilesystemBounds(t *testing.T) {
	dir := t.TempDir()
	const limit = int64(1 << 20)

	t.Run("reads_a_regular_file_exactly", func(t *testing.T) {
		p := filepath.Join(dir, "regular")
		body := bytes.Repeat([]byte("chunk bytes\n"), 40)
		require.NoError(t, os.WriteFile(paths.Long(p), body, 0o600))

		got, err := readBoundedObject(p, limit)
		require.NoError(t, err)
		require.Equal(t, body, got)
	})

	t.Run("reads_a_zero_byte_file", func(t *testing.T) {
		p := filepath.Join(dir, "empty")
		require.NoError(t, os.WriteFile(paths.Long(p), nil, 0o600))

		got, err := readBoundedObject(p, limit)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("a_file_past_the_limit_is_too_large", func(t *testing.T) {
		p := filepath.Join(dir, "toobig")
		body := bytes.Repeat([]byte("a"), 64)
		require.NoError(t, os.WriteFile(paths.Long(p), body, 0o600))

		_, err := readBoundedObject(p, int64(len(body)-1))
		require.ErrorIs(t, err, errObjectTooLarge)
	})

	t.Run("a_directory_is_not_a_regular_file", func(t *testing.T) {
		sub := filepath.Join(dir, "adir")
		require.NoError(t, os.MkdirAll(paths.Long(sub), 0o700))

		_, err := readBoundedObject(sub, limit)
		require.ErrorContains(t, err, "not a regular file")
	})

	t.Run("a_missing_file_reports_not_exist", func(t *testing.T) {
		_, err := readBoundedObject(filepath.Join(dir, "absent"), limit)
		require.True(t, os.IsNotExist(err), "a missing object must surface as os.IsNotExist for readObjectFile's absence branch")
	})
}
