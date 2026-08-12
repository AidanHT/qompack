package testutil

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// goldenSandbox makes an isolated repository root the current directory for the duration of the
// test and returns it, so a golden written by this test lands under t.TempDir() instead of in the
// checkout's own testdata/. That is what lets `go test ./internal/testutil/ -update` run without
// perturbing a single committed fixture.
//
// A go.mod is all repoRoot looks for, so a two-line module file is a complete fake repository.
func goldenSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.test/goldensandbox\n\ngo 1.26\n"), goldenPerm))
	t.Chdir(dir)
	return dir
}

// withUpdate sets the shared -update flag for the duration of the test and restores it afterwards,
// so both branches are exercised in one run regardless of how `go test` was invoked.
func withUpdate(t *testing.T, v bool) {
	t.Helper()
	prev := strconv.FormatBool(update())
	require.NoError(t, flag.Set(updateFlagName, strconv.FormatBool(v)))
	t.Cleanup(func() { require.NoError(t, flag.Set(updateFlagName, prev)) })
}

// TestUpdateFlag_AdoptsAnExistingRegistration asserts registerUpdateFlag reuses an already-declared
// -update rather than redefining it, which is what stops a test binary that links both testutil and
// a package with its own `var update = flag.Bool("update", …)` from panicking at initialization.
func TestUpdateFlag_AdoptsAnExistingRegistration(t *testing.T) {
	require.NotNil(t, flag.Lookup(updateFlagName), "the flag must be registered exactly once")

	adopted := registerUpdateFlag()
	require.Equal(t, update(), adopted(), "a second registration must read the same flag, not panic")

	withUpdate(t, true)
	require.True(t, adopted(), "the adopted reader must see the flag's current value, not a stale copy")
}

// TestGolden_UpdateFlag asserts the three behaviours §17 pins on Golden: -update rewrites the
// file, the default compares against it, and a golden that arrived through a Windows checkout
// with CRLF line endings does not produce a false failure.
func TestGolden_UpdateFlag(t *testing.T) {
	sandbox := goldenSandbox(t)
	goldenPath := filepath.Join(sandbox, "testdata", "golden", "testutil", "sample.txt")
	content := []byte("alpha\nbeta\n")

	t.Run("update rewrites", func(t *testing.T) {
		withUpdate(t, true)
		Golden(t, "sample.txt", content)

		written, err := os.ReadFile(goldenPath)
		require.NoError(t, err, "-update must create the golden, directories and all")
		require.Equal(t, content, written, "the file itself is always written with LF")
	})

	t.Run("without update it compares", func(t *testing.T) {
		withUpdate(t, false)
		Golden(t, "sample.txt", content)
	})

	t.Run("CRLF in the file is not a false failure", func(t *testing.T) {
		withUpdate(t, false)
		require.NoError(t, os.WriteFile(goldenPath, []byte("alpha\r\nbeta\r\n"), goldenPerm))
		Golden(t, "sample.txt", content)
	})

	t.Run("update normalizes CRLF on the way out", func(t *testing.T) {
		withUpdate(t, true)
		Golden(t, "sample.txt", []byte("alpha\r\nbeta\r\n"))

		written, err := os.ReadFile(goldenPath)
		require.NoError(t, err)
		require.Equal(t, content, written, "-update writes LF even when the produced bytes were CRLF")
	})
}

// TestGoldenEqual asserts the comparison Golden is built on directly, including the difference it
// must still catch. Golden itself reports through *testing.T, so the negative case is asserted
// here — at the level of the pure function — rather than by faking a test harness.
func TestGoldenEqual(t *testing.T) {
	tests := []struct {
		name       string
		want, got  string
		wantEquals bool
	}{
		{"identical", "a\nb\n", "a\nb\n", true},
		{"CRLF want, LF got", "a\r\nb\r\n", "a\nb\n", true},
		{"LF want, CRLF got", "a\nb\n", "a\r\nb\r\n", true},
		{"both CRLF", "a\r\nb\r\n", "a\r\nb\r\n", true},
		{"different content", "a\nb\n", "a\nc\n", false},
		{"trailing newline matters", "a\nb\n", "a\nb", false},
		{"a bare CR is not a line ending", "a\rb", "a\nb", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantEquals, goldenEqual([]byte(tc.want), []byte(tc.got)))
		})
	}
}

// TestGoldenJSON_WritesUnescapedJSON asserts GoldenJSON emits indented JSON with HTML escaping
// off, matching every other JSON writer in this codebase: a "<" in a value must reach the golden
// literally, not as <.
func TestGoldenJSON_WritesUnescapedJSON(t *testing.T) {
	sandbox := goldenSandbox(t)
	withUpdate(t, true)

	GoldenJSON(t, "doc.json", map[string]string{"expr": "a<b&c"})

	written, err := os.ReadFile(filepath.Join(sandbox, "testdata", "golden", "testutil", "doc.json"))
	require.NoError(t, err)
	require.Equal(t, "{\n  \"expr\": \"a<b&c\"\n}\n", string(written))
}

// TestGolden_ResolvesItsPath asserts the two lookups that decide WHERE a golden lives: repoRoot
// finds the nearest ancestor holding a go.mod, and callerPkg names the calling test's own package
// directory. Together they are what makes testdata/golden/<pkg>/<name> resolve to one shared tree
// at the repository root no matter which package is under test.
func TestGolden_ResolvesItsPath(t *testing.T) {
	sandbox := goldenSandbox(t)
	root, err := repoRoot()
	require.NoError(t, err)
	// EvalSymlinks on both sides: a temp directory is reached through a symlink on macOS
	// (/var -> /private/var), so the two spellings name the same directory but differ as strings.
	wantRoot, err := filepath.EvalSymlinks(sandbox)
	require.NoError(t, err)
	gotRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, wantRoot, gotRoot, "repoRoot must resolve to the nearest ancestor holding a go.mod")
	require.Equal(t, "testutil", callerPkg(1), "callerPkg must name this package's directory")
}
