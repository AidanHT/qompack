package guards

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// dotQompack is the one directory name the plugin is allowed to write into, under either the
// project root or the user's home.
const dotQompack = ".qompack"

// hookSubcommands is the six §7.3 entry points, in argv form.
var hookSubcommands = [][]string{
	{"observe", "tool"},
	{"observe", "prompt"},
	{"observe", "stop"},
	{"session-start"},
	{"checkpoint"},
	{"flush"},
}

// guardClock is a frozen clock so the guard's own runs are deterministic.
type guardClock struct{ t time.Time }

func (g guardClock) Now() time.Time                  { return g.t }
func (g guardClock) Since(t time.Time) time.Duration { return g.t.Sub(t) }

// TestGuard_WriteSetConfinedToQompack is §13 invariant 7 made mechanical.
//
// "Qompack writes to one gitignored directory and nowhere else" is the entire security story a
// user is asked to accept (D10, §3.3). Stating it in a doc is worth nothing; this snapshots the
// project tree and the home tree around a full run of all six hooks and fails on any created,
// modified or deleted path outside <root>/.qompack/ and <home>/.qompack/.
//
// It runs the hooks IN PROCESS rather than against the built binary on purpose: an in-process run
// shares this test's working directory and environment, so a stray relative-path write — the most
// likely way this invariant actually breaks — lands somewhere the snapshot can see.
func TestGuard_WriteSetConfinedToQompack(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	// A .git marker stops paths.Resolve's upward walk at our temp root instead of letting it
	// escape into whatever encloses the OS temp directory.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))

	// Decoys: ordinary project content the plugin has no business touching. If a future change
	// starts rewriting source files in place, these are what catch it.
	decoys := map[string]string{
		"README.md":               "# a project\n",
		"src/main.go":             "package main\n\nfunc main() {}\n",
		"src/my folder/a b.ts":    "export const x = 1;\n",
		".gitignore":              "/node_modules\n",
		"src/nested/deep/data.js": "module.exports = {};\n",
	}
	for rel, body := range decoys {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}

	before := snapshotTree(t, root, home)

	payload, err := json.Marshal(map[string]any{
		"session_id": "s-writeset",
		"cwd":        root,
		"tool_name":  "FileRead",
		"source":     "startup",
	})
	require.NoError(t, err)

	for _, sub := range hookSubcommands {
		var out, errw bytes.Buffer
		argv := append([]string{"qompack"}, sub...)
		code := cli.Dispatch(context.Background(), cli.All(), argv, cli.Env{
			Getenv:  func(string) string { return "" },
			Stdin:   bytes.NewReader(payload),
			Clock:   guardClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			HomeDir: home,
		}, &out, &errw)
		require.Equal(t, 0, code, "hook %v must exit 0: %s", sub, errw.String())

		var parsed hookio.Output
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &parsed),
			"hook %v must emit valid hookio.Output", sub)
	}

	after := snapshotTree(t, root, home)

	allowed := []string{
		filepath.Join(root, dotQompack) + string(filepath.Separator),
		filepath.Join(home, dotQompack) + string(filepath.Separator),
	}

	var offenders []string
	for p, sum := range after {
		if prev, existed := before[p]; !existed || prev != sum {
			if !underAny(p, allowed) {
				verb := "modified"
				if !existed {
					verb = "created"
				}
				offenders = append(offenders, verb+" "+p)
			}
		}
	}
	for p := range before {
		if _, still := after[p]; !still && !underAny(p, allowed) {
			offenders = append(offenders, "deleted "+p)
		}
	}

	require.Empty(t, offenders,
		"§13 invariant 7: the six hooks wrote outside %s and %s:\n  %s",
		allowed[0], allowed[1], strings.Join(offenders, "\n  "))

	// The guard would pass vacuously if the hooks had written nothing at all, so prove the store
	// really was exercised.
	touchedStore := false
	for p := range after {
		if _, existed := before[p]; !existed && underAny(p, allowed[:1]) {
			touchedStore = true
			break
		}
	}
	require.True(t, touchedStore,
		"the hooks created nothing under %s — the guard would pass without testing anything", allowed[0])
}

// TestGuard_WriteSetDetectsAStrayWrite proves the comparison has teeth: an ordinary file written
// outside .qompack/ between the two snapshots must be reported.
func TestGuard_WriteSetDetectsAStrayWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("a"), 0o600))

	before := snapshotTree(t, root, home)

	require.NoError(t, os.MkdirAll(filepath.Join(root, dotQompack, "logs"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, dotQompack, "logs", "x.log"), []byte("ok"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stray.txt"), []byte("bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("changed"), 0o600))

	after := snapshotTree(t, root, home)
	allowed := []string{filepath.Join(root, dotQompack) + string(filepath.Separator)}

	var offenders []string
	for p, sum := range after {
		if prev, existed := before[p]; (!existed || prev != sum) && !underAny(p, allowed) {
			offenders = append(offenders, p)
		}
	}

	require.Len(t, offenders, 2, "both the new stray file and the modified decoy must be caught")
	require.Contains(t, strings.Join(offenders, "\n"), "stray.txt")
	require.Contains(t, strings.Join(offenders, "\n"), "keep.txt")
}

// snapshotTree hashes every regular file under the given roots. Content hashing rather than
// mtime comparison is deliberate: mtime resolution is coarse enough on some filesystems that a
// rewrite within the same tick would go unnoticed, which is exactly the case this guard exists
// to catch.
func snapshotTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()

	got := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			b, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(b)
			got[p] = hex.EncodeToString(sum[:])
			return nil
		})
		require.NoError(t, err, "snapshotting %s", root)
	}
	return got
}

// underAny reports whether p sits under one of the given directory prefixes.
func underAny(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// compile-time proof that guardClock satisfies the Clock seam the hooks take.
var _ core.Clock = guardClock{}
