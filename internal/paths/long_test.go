package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestLongPath_Over260 is the central Long test: a real write and read through a path over the
// legacy Windows MAX_PATH, exercised through the same WriteAtomic/OpenFile entry points every
// production caller uses. It must succeed on every platform.
func TestLongPath_Over260(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		dir = filepath.Join(dir, "abcdefghij")
	}
	target := filepath.Join(dir, "0001.json")
	require.Greater(t, len(target), 260, "fixture path must actually exceed MAX_PATH")

	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	require.NoError(t, paths.WriteAtomic(target, []byte("hello"), 0o600))

	got, err := os.ReadFile(paths.Long(target))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))

	f, err := paths.OpenFile(target, os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestLong_IdentityBelowThreshold(t *testing.T) {
	short := filepath.Join(t.TempDir(), "f.txt")
	require.Equal(t, short, paths.Long(short))
}

// TestLong_IdentityWhenAbsFails drives Long's own filepath.Abs failure path: a NUL byte is
// invalid anywhere in a Windows path, so GetFullPathName rejects it outright. Long must fall
// back to returning p unchanged rather than propagating the failure.
func TestLong_IdentityWhenAbsFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: this exercises the windows-specific Abs failure path")
	}
	bad := "a\x00b"
	require.Equal(t, bad, paths.Long(bad))
}

func TestLong_EmptyStringIsIdentity(t *testing.T) {
	require.Equal(t, "", paths.Long(""))
}

func TestLong_PrefixesOverThreshold(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: the \\\\?\\ prefix form is windows-specific")
	}
	p := filepath.Join(t.TempDir(), strings.Repeat("a", 300))
	got := paths.Long(p)
	require.True(t, strings.HasPrefix(got, `\\?\`), "got %q", got)
	require.True(t, strings.HasSuffix(got, strings.Repeat("a", 300)))
}

func TestLong_IdempotentOnAlreadyPrefixed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: the \\\\?\\ prefix form is windows-specific")
	}
	once := paths.Long(filepath.Join(t.TempDir(), strings.Repeat("a", 300)))
	require.True(t, strings.HasPrefix(once, `\\?\`))
	require.Equal(t, once, paths.Long(once), "Long must be a no-op on an already-prefixed path")
}

func TestLong_UNCGetsUNCPrefix(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: UNC handling is windows-specific")
	}
	tail := `server\share\` + strings.Repeat("d", 260)
	unc := `\\` + tail
	got := paths.Long(unc)
	require.Equal(t, `\\?\UNC\`+tail, got)
}
