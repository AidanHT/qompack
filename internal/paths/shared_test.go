package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader is the whole reason OpenShared and
// WriteAtomic's POSIX-semantics replace exist, asserted as the one-line difference they make.
//
// A reader is not passive on Windows. os.Open takes a handle with
// FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE, and MoveFileEx — which is all
// os.Rename is — refuses to replace a destination that anyone holds open, at any share mode. So
// for as long as some process is reading a file, WriteAtomic on that file cannot finish: not
// "races with", cannot finish. That is a writer a reader can stall, and internal/ipc's state.bin
// is exactly that pair (the daemon writes it on every §12.2 hot-path transition; every hook
// client process reads it before deciding whether to dial).
//
// The two halves are asserted separately because each is load-bearing on its own and neither is
// sufficient alone:
//
//   - held with os.Open, WriteAtomic must still fail on Windows. If this ever starts passing,
//     Go or Windows changed the default share mode and OpenShared has stopped being necessary.
//   - held with paths.OpenShared, WriteAtomic must succeed — and the reader must still see the
//     bytes it opened, which is the POSIX guarantee this buys on Windows rather than a
//     consolation prize.
//
// Off Windows both halves succeed, and that is asserted rather than skipped: rename(2) has always
// had these semantics, so this test states the platform difference instead of hiding it.
func TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader(t *testing.T) {
	const before, after = "the record a reader already opened", "the replacement"

	t.Run("held with os.Open", func(t *testing.T) {
		l := newLayout(t)
		target := filepath.Join(l.Run, "state.bin")
		require.NoError(t, os.WriteFile(target, []byte(before), 0o600))

		f, err := os.Open(target)
		require.NoError(t, err)
		defer func() { _ = f.Close() }()

		err = paths.WriteAtomic(target, []byte(after), 0o600)
		if runtime.GOOS == "windows" {
			require.Error(t, err,
				"guard: os.Open's handle is supposed to block MoveFileEx's replace on Windows. "+
					"If it no longer does, paths.OpenShared is no longer load-bearing and this "+
					"whole file can go — do not just relax this assertion")
			return
		}
		require.NoError(t, err, "a POSIX file descriptor has never blocked a rename")
	})

	t.Run("held with paths.OpenShared", func(t *testing.T) {
		l := newLayout(t)
		target := filepath.Join(l.Run, "state.bin")
		require.NoError(t, os.WriteFile(target, []byte(before), 0o600))

		f, err := paths.OpenShared(target)
		require.NoError(t, err)
		defer func() { _ = f.Close() }()

		require.NoError(t, paths.WriteAtomic(target, []byte(after), 0o600),
			"a reader that consented to the replace must not be able to stall the writer")

		buf := make([]byte, len(before))
		n, readErr := f.Read(buf)
		require.NoError(t, readErr)
		require.Equal(t, before, string(buf[:n]),
			"the open handle must keep reading the version it opened, exactly as on POSIX")

		landed, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, after, string(landed), "the replacement must be what is on disk")
	})
}

// TestReadFileSharedMatchesOsReadFile pins the substitution internal/ipc.ReadState makes: it
// swapped os.ReadFile for paths.ReadFileShared, and ReadState's whole fallback contract is a
// switch on the error that read returns (missing => StateFromConfig, transient Windows contention
// => retry). A ReadFileShared that reported a missing file differently would silently convert the
// ordinary first-run case into a 512-attempt retry loop, so the shapes are asserted, not assumed.
func TestReadFileSharedMatchesOsReadFile(t *testing.T) {
	l := newLayout(t)

	t.Run("an ordinary file reads identically", func(t *testing.T) {
		p := filepath.Join(l.State, "payload.bin")
		// Larger than the 512-byte first chunk, and not a multiple of it, so the grow-and-read
		// loop is exercised rather than only its first pass.
		want := make([]byte, 1500)
		for i := range want {
			want[i] = byte(i)
		}
		require.NoError(t, os.WriteFile(p, want, 0o600))

		got, err := paths.ReadFileShared(p)
		require.NoError(t, err)
		require.Equal(t, want, got)

		std, stdErr := os.ReadFile(p)
		require.NoError(t, stdErr)
		require.Equal(t, std, got)
	})

	t.Run("an empty file reads as an empty slice", func(t *testing.T) {
		p := filepath.Join(l.State, "empty.bin")
		require.NoError(t, os.WriteFile(p, nil, 0o600))

		got, err := paths.ReadFileShared(p)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("a missing file is os.ErrNotExist, not contention", func(t *testing.T) {
		p := filepath.Join(l.State, "absent.bin")

		_, stdErr := os.ReadFile(p)
		require.ErrorIs(t, stdErr, os.ErrNotExist, "guard: os.ReadFile's own answer")

		_, err := paths.ReadFileShared(p)
		require.ErrorIs(t, err, os.ErrNotExist,
			"ReadState treats a missing state.bin as the first-run fallback by testing exactly "+
				"this; a different shape would turn every first run into a retry storm")
	})
}
