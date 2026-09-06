package mcp

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The golden-file helper for this package.
//
// internal/testutil already has one, and this is not a preference for a second: testutil imports
// internal/cli, cli imports mcp since SP-13, and an mcp test importing testutil closes an import
// cycle the test build refuses (see fakeclock_test.go). The three things testutil.Golden does that
// matter here — resolve the repository root, normalize CRLF, honour -update — are reproduced, and
// nothing else is.
//
// Goldens live under <repo>/testdata/golden/mcp/ rather than internal/mcp/testdata/ because they
// are the PUBLISHED wire shapes: the `initialize` result, the `tools/list` result and the eight
// input schemas are what a host sees, and the repository-level testdata/golden tree is where every
// other published shape in this project is frozen.

// updateGolden mirrors testutil's -update flag. It is registered defensively: another package's
// TestMain in the same binary may already own the name, in which case that registration is adopted.
var updateGolden = registerGoldenUpdateFlag()

// registerGoldenUpdateFlag registers -update, or adopts an existing registration of it.
func registerGoldenUpdateFlag() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	v := flag.Bool("update", false, "rewrite golden files instead of comparing against them")
	return func() bool { return *v }
}

// goldenPath resolves a golden's absolute path from its repository-relative slash form.
func goldenPath(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join(repoRootForGolden(t), filepath.FromSlash(rel))
}

// repoRootForGolden walks up from the working directory until it finds go.mod.
func repoRootForGolden(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err, "getwd")
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "no go.mod above %s", dir)
		dir = parent
	}
}

// requireGolden asserts got equals the golden at rel, rewriting it under -update.
//
// Newlines are normalized on the READ side only: the file on disk keeps LF, and a Windows checkout
// with autocrlf on does not read as a diff.
func requireGolden(t *testing.T, rel string, got []byte) {
	t.Helper()
	path := goldenPath(t, rel)

	if updateGolden() {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755), "mkdir for %s", rel)
		require.NoError(t, os.WriteFile(path, got, 0o644), "writing %s", rel) // #nosec G306 -- test fixture
		t.Logf("golden: rewrote %s", rel)
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "%s is missing; run `go test ./internal/mcp -update`", rel)
	require.Equal(t, string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))),
		string(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))),
		"%s is stale; run `go test ./internal/mcp -update` after reviewing the change", rel)
}
