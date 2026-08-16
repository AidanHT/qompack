package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// errReader fails after handing back a prefix, which is what a truncated pipe from a killed hook
// process looks like from the store's side.
type errReader struct {
	prefix []byte
	off    int
	err    error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.off < len(r.prefix) {
		n := copy(p, r.prefix[r.off:])
		r.off += n
		return n, nil
	}
	return 0, r.err
}

func TestPut_ReaderEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("a_nil_reader_stores_nothing_rather_than_panicking", func(t *testing.T) {
		tp := newTestStore(t)

		res, err := tp.Store.Put(ctx, nil, PutOptions{Tool: "Bash"})
		require.NoError(t, err,
			"a hook that produced no output at all is an ordinary event, not an error: §2.3 gives a "+
				"hook no exit code but 0, so the store must absorb this rather than fail the session")
		require.False(t, res.Truncated)
		require.Zero(t, res.Root.RawBytes)
	})

	t.Run("a_reader_that_fails_midway_reports_the_error", func(t *testing.T) {
		tp := newTestStore(t)
		boom := errors.New("pipe closed by a killed hook")

		_, err := tp.Store.Put(ctx, &errReader{prefix: []byte("partial output"), err: boom},
			PutOptions{Tool: "Bash"})
		require.ErrorIs(t, err, boom,
			"a mid-stream read failure must surface. Storing the prefix as if it were the whole tool "+
				"result would put a silently truncated observation into the index, and every later "+
				"staleness check would compare against content that never existed")
		require.ErrorContains(t, err, "store: reading content to put")
	})

	t.Run("an_io_EOF_terminated_reader_is_stored_whole", func(t *testing.T) {
		tp := newTestStore(t)
		body := bytes.Repeat([]byte("streamed payload\n"), 512)

		res, err := tp.Store.Put(ctx, io.NopCloser(bytes.NewReader(body)), PutOptions{Tool: "Bash"})
		require.NoError(t, err)
		require.False(t, res.Truncated)
		require.Equal(t, int64(len(body)), res.Root.RawBytes,
			"readAllInto must grow its buffer across as many Reads as the source needs; a short read "+
				"treated as EOF would silently drop the tail")

		rc, err := tp.Store.Open(ctx, res.Root.Hash)
		require.NoError(t, err)
		t.Cleanup(func() { _ = rc.Close() })
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, body, got, "what went in must come back out byte for byte")
	})
}

// TestGC_IgnoresFilesItCannotIdentify pins the sweep's most dangerous branch.
//
// GC walks objects/ and deletes what the live set does not name. A file whose name is not a hash is
// not an object this store wrote, and the only safe reading of it is "not mine" — deleting it would
// make GC a wildcard rm over a directory the user, a backup tool or a later wave may legitimately
// have put something in. objectHashOf returning !ok is the guard, and this is what fires it.
func TestGC_IgnoresFilesItCannotIdentify(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	_, err := tp.Store.PutBytes(ctx, []byte("a real object, referenced and live\n"),
		PutOptions{Tool: "FileRead", Path: "src/live.ts"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))

	objects := paths.Of(tp.Root).Objects
	foreign := map[string]string{
		filepath.Join(objects, "README.txt"):              "not an object",
		filepath.Join(objects, "ab", "cd", "notahex.zst"): "wrong name shape",
		filepath.Join(objects, "ab", "cd", "deadbeef"):    "too short to be a sha256",
	}
	for p, body := range foreign {
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
	}

	// RetainDays/-Sessions negative is the "collect everything not live" policy, so this is the most
	// aggressive sweep the store can be asked for — exactly when the guard has to hold.
	_, err = tp.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err)

	for p := range foreign {
		_, statErr := os.Stat(paths.Long(p))
		require.NoError(t, statErr,
			"GC deleted %s, a file under objects/ whose name is not a content hash. GC must only ever "+
				"remove objects it can identify as its own", filepath.Base(p))
	}
}
