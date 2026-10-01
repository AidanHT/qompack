package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// seedDanglingToolRef appends a tool_use record whose root nothing holds and no gc tombstone
// retired: bytes a backup copies faithfully, and a store no reader can fully serve.
func seedDanglingToolRef(t *testing.T, p seededProject) {
	t.Helper()
	appendLine(t, filepath.Join(p.Layot.Index, "tool_use.jsonl"),
		`{"v":1,"id":"toolu_dangling","s":"s-fsck","turn":2,"ts":2,"tool":"Read","root":"sha256:`+
			strings.Repeat("cd", 32)+`"}`)
}

// TestBackupCLI_VerifyJudgesWhatRestoreJudges is the second half of F-C49-2: `backup verify` passed
// on a backup that `backup restore` then refused, because verify only re-hashed the backup's files
// while restore also proved a reader and ran fsck. Verify now restores into a scratch destination,
// runs the same proof and the same integrity scan, and removes the scratch copy, so a backup verify
// certifies is one restore accepts, and one restore refuses is one verify refuses.
//
// The scratch destination is inside the source store's own tmp/, never the system temporary
// directory: 00-ARCHITECTURE.md §13 invariant 7 keeps `$TMPDIR` at large out of the product write
// set, and a scratch restore is a full copy of captured prompts and tool output.
func TestBackupCLI_VerifyJudgesWhatRestoreJudges(t *testing.T) {
	isolateUserGlobal(t)

	t.Run("a healthy backup: verify proves it the way restore does", func(t *testing.T) {
		sysTmp := redirectSystemTemp(t)
		p := seedFsckProject(t)
		code, out, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "ok", "--json")
		require.Equal(t, ExitOK, code, "%s %s", out, stderr)

		before := dirNames(t, sysTmp)
		code, out, stderr = fsckDispatch(t, "backup", "verify", "--project", p.Root, "--id", "ok", "--json")
		require.Equal(t, ExitOK, code, "%s %s", out, stderr)
		require.Equal(t, before, dirNames(t, sysTmp), "verify writes nothing to the system temporary directory")
		var verified backupReport
		require.NoError(t, json.Unmarshal([]byte(out), &verified))
		require.NotNil(t, verified.Manifest)
		require.NotNil(t, verified.Restore, "verify reports the reader proof restore would make")
		require.Positive(t, verified.Restore.ContentRootsProven)
		require.NotNil(t, verified.Integrity, "verify reports the integrity scan restore would run")
		require.Equal(t, ExitOK, verified.Integrity.Exit)
		require.Contains(t, verified.Restore.Note, "scratch destination")
		requireUnder(t, p.Layot.Tmp, verified.Restore.Destination)
		scratch := filepath.Dir(verified.Restore.Destination)
		_, err := os.Stat(paths.Long(scratch))
		require.True(t, os.IsNotExist(err), "the scratch restore is removed: %s", scratch)
	})

	t.Run("a backup no reader can serve: both refuse", func(t *testing.T) {
		sysTmp := redirectSystemTemp(t)
		p := seedFsckProject(t)
		seedDanglingToolRef(t, p)
		code, out, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "bad", "--json")
		require.Equal(t, ExitOK, code, "create copies the bytes it finds: %s %s", out, stderr)

		before := dirNames(t, sysTmp)
		code, out, _ = fsckDispatch(t, "backup", "verify", "--project", p.Root, "--id", "bad", "--json")
		require.Equal(t, ExitError, code, "verify must refuse what restore refuses: %s", out)
		require.Equal(t, before, dirNames(t, sysTmp), "a failed verify keeps nothing in the system temporary directory")
		var refused backupReport
		require.NoError(t, json.Unmarshal([]byte(out), &refused))
		require.Contains(t, refused.Error, "does not resolve")
		require.NotNil(t, refused.Restore)
		scratch := filepath.Dir(refused.Restore.Destination)
		requireUnder(t, p.Layot.Tmp, scratch)
		require.Contains(t, refused.Error, "scratch restore is kept at "+scratch, "the kept scratch restore is named")
		_, err := os.Stat(paths.Long(scratch))
		require.NoError(t, err, "the failed scratch restore is kept for inspection")

		dest := filepath.Join(t.TempDir(), "restored")
		code, out, _ = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "bad",
			"--destination", dest, "--json")
		require.Equal(t, ExitError, code, "%s", out)
		require.Contains(t, out, "does not resolve")
	})
}

// redirectSystemTemp points the process's temporary directory at a fresh directory for the rest of
// the test, so what a command writes there can be seen, and returns it.
func redirectSystemTemp(t *testing.T) string {
	t.Helper()
	// Not t.TempDir: the daemon's socket resolves under TMPDIR (ipc.Resolve), and on macos-latest
	// /var/folders/<2>/<30>/T/ plus a subtest-named directory left even the short fallback
	// <TMPDIR>/qp-<hash8>.sock past ipc's 100-byte budget, so backup create refused with
	// "ipc address exceeds sun_path limit" (run 36816905394). A short prefix under the same temp
	// directory keeps the ordinary per-uid candidate inside it.
	tmp, err := os.MkdirTemp("", "qbk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(k, tmp)
	}
	require.Equal(t, filepath.Clean(tmp), filepath.Clean(os.TempDir()), "fixture: the redirect took effect")
	return tmp
}

// dirNames lists dir's entries by name.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(dir))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// requireUnder fails unless p lies strictly inside dir.
func requireUnder(t *testing.T, dir, p string) {
	t.Helper()
	rel, err := filepath.Rel(dir, p)
	require.NoError(t, err)
	require.False(t, rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)),
		"%s must lie inside %s", p, dir)
}

// TestBackupCLI_RestoreNoteMatchesItsIntegrityReport is F-UAT/C1.7's low finding: every restore
// response said checkpoint-chain and delivery-seal integrity were NOT covered and told the operator
// to run `fsck --seal-check`, while the same response's integrity report — which restore runs with
// the seal check — said the full dual-reader seal check passed. The note now says what ran.
func TestBackupCLI_RestoreNoteMatchesItsIntegrityReport(t *testing.T) {
	isolateUserGlobal(t)
	p := seedFsckProject(t)
	code, out, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "note", "--json")
	require.Equal(t, ExitOK, code, "%s %s", out, stderr)

	dest := filepath.Join(t.TempDir(), "restored")
	code, out, stderr = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "note",
		"--destination", dest, "--json")
	require.Equal(t, ExitOK, code, "%s %s", out, stderr)
	var restored backupReport
	require.NoError(t, json.Unmarshal([]byte(out), &restored))
	require.NotNil(t, restored.Integrity)
	require.Contains(t, fsckDetail(fsckRowOf(t, restored.Integrity, "delivery")), "the full dual-reader seal check passed")
	require.NotContains(t, restored.Restore.Note, "NOT covered")
	require.Contains(t, restored.Restore.Note, "checked on the destination by the integrity report")

	code, out, _ = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "note",
		"--destination", dest, "--json")
	require.Equal(t, ExitError, code)
	var refused backupReport
	require.NoError(t, json.Unmarshal([]byte(out), &refused))
	require.Nil(t, refused.Integrity)
	require.Contains(t, refused.Restore.Note, "No integrity check ran")
}

// fsckRowOf returns one check of a typed report as the JSON-document row the fsck tests read.
func fsckRowOf(t *testing.T, rep *fsckReport, id string) map[string]any {
	t.Helper()
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b, &doc))
	return fsckRequireRow(t, doc, id)
}
