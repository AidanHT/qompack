package paths_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func TestIsProtected_Table(t *testing.T) {
	l := newLayout(t)

	cases := []struct {
		name string
		p    string
		want bool
	}{
		{"the live bloom file", filepath.Join(l.Sketches, "tried.bloom"), true},
		{"a bloom backup is not the live file", filepath.Join(l.Sketches, "tried.bloom.1.bak"), false},
		{"a checkpoint artifact", filepath.Join(l.Checkpoints, "0001.json"), true},
		{"the checkpoint manifest", paths.ManifestPath(l), true},
		{"pins invariants log", filepath.Join(l.Pins, "invariants.jsonl"), true},
		{"an unrelated state file", filepath.Join(l.State, "bocd.json"), false},
		{"outside .qompack entirely", filepath.Join(l.Root, "README.md"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, paths.IsProtected(l.Root, tc.p))
		})
	}
}

// TestAppendOnlyGuard is the central conformance test of this package: every one of the five
// listed operations against a fully populated layout must fail, and the pre-existing protected
// artifacts must be byte-for-byte untouched afterward.
func TestAppendOnlyGuard(t *testing.T) {
	l := newLayout(t)

	cpContent := []byte(`{"seq":1}`)
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, cpContent))

	pins := filepath.Join(l.Pins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(pins, map[string]string{"id": "inv-1"}))
	pinsBefore, err := os.ReadFile(pins)
	require.NoError(t, err)

	bloom := filepath.Join(l.Sketches, "tried.bloom")
	require.NoError(t, os.WriteFile(bloom, []byte("bloomdata"), 0o600))

	t.Run("a_trunc_on_checkpoint", func(t *testing.T) {
		_, err := paths.OpenFile(cp, os.O_WRONLY|os.O_TRUNC, 0o600)
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	t.Run("b_nonappend_write_on_pins", func(t *testing.T) {
		_, err := paths.OpenFile(pins, os.O_WRONLY, 0o600)
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	t.Run("c_writeatomic_on_bloom", func(t *testing.T) {
		err := paths.WriteAtomic(bloom, []byte("replacement"), 0o600)
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	t.Run("d_createnew_twice", func(t *testing.T) {
		err := paths.CreateNew(cp, cpContent)
		require.ErrorIs(t, err, os.ErrExist)
	})

	t.Run("e_appendonly_wrong_extension", func(t *testing.T) {
		_, err := paths.AppendOnly(filepath.Join(l.Records, "x.json"))
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	// None of the five attempts above may have altered anything already on disk.
	cpAfter, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Equal(t, cpContent, cpAfter)

	pinsAfter, err := os.ReadFile(pins)
	require.NoError(t, err)
	require.Equal(t, pinsBefore, pinsAfter)

	bloomAfter, err := os.ReadFile(bloom)
	require.NoError(t, err)
	require.Equal(t, "bloomdata", string(bloomAfter))
}

// TestOpenFile_HandlesUnresolvableRootGracefully drives rootOf's own filepath.Abs failure path
// (a NUL byte is invalid anywhere in a Windows path): OpenFile must fall back to treating p as
// unprotected rather than panicking or misbehaving when it cannot even determine a root.
func TestOpenFile_HandlesUnresolvableRootGracefully(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: this exercises the windows-specific Abs failure path")
	}
	_, err := paths.OpenFile("a\x00b", os.O_RDONLY, 0)
	require.Error(t, err, "the underlying OS call must still fail on an invalid path")
	require.NotErrorIs(t, err, core.ErrAppendOnly, "an unresolvable root must not be treated as protected")
}

func TestAppendOnly_ExtensionAllowlist(t *testing.T) {
	l := newLayout(t)

	for _, name := range []string{"a.jsonl", "b.ndjson", "c.log"} {
		w, err := paths.AppendOnly(filepath.Join(l.Records, name))
		require.NoError(t, err, name)
		require.NoError(t, w.Close())
	}
	for _, name := range []string{"d.json", "e.txt", "noext"} {
		_, err := paths.AppendOnly(filepath.Join(l.Records, name))
		require.ErrorIs(t, err, core.ErrAppendOnly, name)
	}
}

func TestAppendJSONL_OneLinePerRecord(t *testing.T) {
	l := newLayout(t)
	p := filepath.Join(l.Index, "tool_use.jsonl")

	type rec struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	records := []rec{
		{ID: "1", Note: "first"},
		{ID: "2", Note: "line\nbreak in a string field"},
		{ID: "3", Note: "third"},
	}
	for _, r := range records {
		require.NoError(t, paths.AppendJSONL(p, r))
	}

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	trimmed := strings.TrimRight(string(b), "\n")
	lines := strings.Split(trimmed, "\n")
	require.Len(t, lines, 3, "exactly one line per record")

	for i, line := range lines {
		require.NotContains(t, line, "\n", "line %d must not itself contain a raw newline", i)
		var got rec
		require.NoError(t, json.Unmarshal([]byte(line), &got))
		require.Equal(t, records[i], got)
	}
	require.Contains(t, lines[1], `\n`, "the embedded newline must be JSON-escaped, not raw")
}

func TestAppendJSONL_PropagatesEncodeError(t *testing.T) {
	l := newLayout(t)
	p := filepath.Join(l.Index, "bad.jsonl")

	type rec struct {
		X float64 `json:"x"`
	}
	// encoding/json refuses to encode NaN and ±Inf: Encode must fail, and AppendJSONL must
	// propagate that failure rather than write a partial or corrupt line.
	err := paths.AppendJSONL(p, rec{X: math.NaN()})
	require.Error(t, err)

	_, statErr := os.Stat(p)
	require.True(t, os.IsNotExist(statErr), "a failed encode must not create the file")
}

func TestAppendJSONL_PropagatesAppendOnlyExtensionError(t *testing.T) {
	l := newLayout(t)
	p := filepath.Join(l.Index, "bad.json") // wrong extension: AppendOnly must refuse it

	err := paths.AppendJSONL(p, map[string]int{"a": 1})
	require.ErrorIs(t, err, core.ErrAppendOnly)
}

// TestAppendJSONL_CompactsEmbeddedRawMessageWhitespace documents why AppendJSONL's raw-newline
// guard cannot be exercised through the public API with a legitimate value: encoding/json itself
// compacts a nested json.RawMessage's whitespace away rather than passing it through verbatim, so
// even a field deliberately holding pre-formatted, multi-line JSON still lands as one line.
func TestAppendJSONL_CompactsEmbeddedRawMessageWhitespace(t *testing.T) {
	l := newLayout(t)
	p := filepath.Join(l.Index, "raw.jsonl")

	type rec struct {
		Blob json.RawMessage `json:"blob"`
	}
	require.NoError(t, paths.AppendJSONL(p, rec{Blob: json.RawMessage("{\n\"a\":1\n}")}))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(b), "\n"), "exactly one line despite the embedded whitespace in Blob")

	var got rec
	require.NoError(t, json.Unmarshal(bytes.TrimRight(b, "\n"), &got))
	require.JSONEq(t, `{"a":1}`, string(got.Blob))
}

func TestAppendJSONL_DisablesHTMLEscaping(t *testing.T) {
	l := newLayout(t)
	p := filepath.Join(l.Index, "segments.jsonl")

	type rec struct {
		Cmp string `json:"cmp"`
	}
	require.NoError(t, paths.AppendJSONL(p, rec{Cmp: "a<b && b>c"}))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(b), "a<b && b>c", "literal <, & and > must survive uninterpreted")
	require.NotContains(t, string(b), "\\u003c", "must not fall back to the default HTML-safe escaping")
}

func TestCreateNew_SetsReadOnly(t *testing.T) {
	l := newLayout(t)
	p := paths.CheckpointPath(l, 2)
	require.NoError(t, paths.CreateNew(p, []byte("{}")))

	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Zero(t, fi.Mode().Perm()&0o222, "no write bit should remain")

	err = os.WriteFile(p, []byte("overwrite"), 0o600)
	require.Error(t, err, "a read-only file must refuse a subsequent os.WriteFile")

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "{}", string(b))
}

func TestRestoreLog_CreatesAnAppendableLogAndRefusesToOverwrite(t *testing.T) {
	l := newLayout(t)
	p := paths.ManifestPath(l)
	first := []byte("{\"seq\":1}\n")
	require.NoError(t, paths.RestoreLog(p, first))

	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode().Perm()&0o200, "a restored log must stay owner-writable")

	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, first, got)

	require.NoError(t, paths.AppendJSONL(p, map[string]int{"seq": 2}))
	got, err = os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(got), `"seq":1`)
	require.Contains(t, string(got), `"seq":2`)

	err = paths.RestoreLog(p, []byte("overwrite\n"))
	require.ErrorIs(t, err, os.ErrExist)
	got, err = os.ReadFile(p)
	require.NoError(t, err)
	require.NotContains(t, string(got), "overwrite")

	err = paths.RestoreLog(filepath.Join(l.Checkpoints, "0001.json"), []byte("{}"))
	require.ErrorIs(t, err, core.ErrAppendOnly)
}

// TestCreateNew_DirectoryAtPathIsNotACollision pins the one error CreateNew reserves: os.ErrExist
// means a regular file already at p, the collision checkpoint.Finalize retries past by bumping its
// sequence number. A directory there is not that. POSIX open(2) answers EEXIST for a directory
// exactly as for a file, so before this pin Finalize on Linux and macOS skipped past a directory
// sitting at its artifact path (TestFinalizeReopensTheDraftWhenTheArtifactNeverLands, red on the
// first CI run of those runners); Windows refused it with ERROR_ACCESS_DENIED all along.
func TestCreateNew_DirectoryAtPathIsNotACollision(t *testing.T) {
	l := newLayout(t)
	p := paths.CheckpointPath(l, 3)
	require.NoError(t, os.MkdirAll(paths.Long(p), 0o700))

	err := paths.CreateNew(p, []byte("{}"))
	require.Error(t, err, "a directory at the artifact path must refuse the write")
	require.NotErrorIs(t, err, os.ErrExist,
		"a directory is not a sequence collision: ErrExist would make Finalize move to the next number")

	fi, statErr := os.Stat(paths.Long(p))
	require.NoError(t, statErr)
	require.True(t, fi.IsDir(), "the directory must be left exactly where it was")
}

func TestReplaceBloom_KeepsOneBackup(t *testing.T) {
	l := newLayout(t)
	live := filepath.Join(l.Sketches, "tried.bloom")
	require.NoError(t, os.WriteFile(live, []byte("v0"), 0o600))

	require.NoError(t, paths.ReplaceBloom(l, []byte("v1"), 1))
	require.NoError(t, paths.ReplaceBloom(l, []byte("v2"), 2))

	cur, err := os.ReadFile(live)
	require.NoError(t, err)
	require.Equal(t, "v2", string(cur))

	entries, err := os.ReadDir(l.Sketches)
	require.NoError(t, err)
	var baks []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			baks = append(baks, e.Name())
		}
	}
	require.Equal(t, []string{"tried.bloom.2.bak"}, baks)

	bakContent, err := os.ReadFile(filepath.Join(l.Sketches, "tried.bloom.2.bak"))
	require.NoError(t, err)
	require.Equal(t, "v1", string(bakContent))
}

func TestReplaceBloom_FirstCallWithNoPriorFile(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, paths.ReplaceBloom(l, []byte("v0"), 0))

	cur, err := os.ReadFile(filepath.Join(l.Sketches, "tried.bloom"))
	require.NoError(t, err)
	require.Equal(t, "v0", string(cur))

	entries, err := os.ReadDir(l.Sketches)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasSuffix(e.Name(), ".bak"), "no backup should exist yet: %s", e.Name())
	}
}

func TestReplaceBloom_IsTheOnlyWayToReplaceIt(t *testing.T) {
	l := newLayout(t)
	require.True(t, paths.IsProtected(l.Root, filepath.Join(l.Sketches, "tried.bloom")))
}

func TestReplaceBloom_IgnoresUnrelatedAndMalformedSketchFiles(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))

	// A sibling sketch file: must be left alone by pruning, since it neither starts with
	// "tried.bloom." nor ends in ".bak" together.
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "touch.cms"), []byte("cms"), 0o600))
	// A subdirectory that happens to live alongside the bloom file: must be skipped, not treated
	// as a candidate backup.
	require.NoError(t, os.MkdirAll(filepath.Join(l.Sketches, "tried.bloom.oops.bak"), 0o700))
	// A backup-shaped name whose "sequence" is not a valid integer: must be ignored by the prune
	// scan (and therefore never removed, since it was never tracked as a real backup).
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom.notanumber.bak"), []byte("stray"), 0o600))

	require.NoError(t, paths.ReplaceBloom(l, []byte("v1"), 1))
	require.NoError(t, paths.ReplaceBloom(l, []byte("v2"), 2))

	// The unrelated file, the directory, and the malformed-name file must all still be present.
	_, err := os.Stat(filepath.Join(l.Sketches, "touch.cms"))
	require.NoError(t, err)
	fi, err := os.Stat(filepath.Join(l.Sketches, "tried.bloom.oops.bak"))
	require.NoError(t, err)
	require.True(t, fi.IsDir())
	_, err = os.Stat(filepath.Join(l.Sketches, "tried.bloom.notanumber.bak"))
	require.NoError(t, err, "a malformed backup name is never tracked, so it is never pruned")

	// Exactly one real, numerically-sequenced backup must remain: tried.bloom.2.bak.
	_, err = os.Stat(filepath.Join(l.Sketches, "tried.bloom.2.bak"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(l.Sketches, "tried.bloom.1.bak"))
	require.True(t, os.IsNotExist(err), "the older real backup must have been pruned")
}

// TestReplaceBloom_PruneSurvivesUnreadableSketchesDir drives pruneBloomBackups' own os.ReadDir
// failure path using icacls (a standard Windows tool, invoked via os/exec — no new dependency)
// to deny list-directory access on l.Sketches after the file it needs to Stat and Rename already
// exists there. pruneBloomBackups swallows a ReadDir error rather than propagating it — pruning
// is best-effort — so ReplaceBloom as a whole must still succeed even though no pruning happens.
func TestReplaceBloom_PruneSurvivesUnreadableSketchesDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: icacls is windows-specific")
	}
	u, err := user.Current()
	if err != nil {
		t.Skipf("platform: could not determine current user: %v", err)
	}

	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))

	if out, denyErr := exec.Command("icacls", l.Sketches, "/deny", u.Username+":(RD)").CombinedOutput(); denyErr != nil {
		t.Skipf("platform: icacls deny unavailable in this environment: %v: %s", denyErr, out)
	}
	// /remove:d strips the deny ACE outright. A /grant of an allow ACE would not be enough on its
	// own — NTFS evaluates an explicit deny before any allow regardless of which was added later —
	// and t.TempDir()'s own RemoveAll cleanup (registered before this one, so it runs after it)
	// would otherwise fail to delete l.Sketches.
	t.Cleanup(func() {
		_, _ = exec.Command("icacls", l.Sketches, "/remove:d", u.Username).CombinedOutput()
	})

	require.NoError(t, paths.ReplaceBloom(l, []byte("v1"), 1), "pruning failure must not fail the replace")

	cur, readErr := os.ReadFile(filepath.Join(l.Sketches, "tried.bloom"))
	require.NoError(t, readErr)
	require.Equal(t, "v1", string(cur))
}

func TestReplaceBloom_RenameToBackupFails(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))
	// Block the exact backup destination with a directory: renaming a file onto an existing
	// directory always fails.
	require.NoError(t, os.MkdirAll(filepath.Join(l.Sketches, "tried.bloom.1.bak"), 0o700))

	err := paths.ReplaceBloom(l, []byte("v1"), 1)
	require.Error(t, err)

	// The live file must be untouched by the failed replacement.
	cur, readErr := os.ReadFile(filepath.Join(l.Sketches, "tried.bloom"))
	require.NoError(t, readErr)
	require.Equal(t, "v0", string(cur))
}

func TestReplaceBloom_MkdirTmpFails(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))
	// Replace l.Tmp with a plain file, so ReplaceBloom's own os.MkdirAll(l.Tmp, ...) must fail.
	require.NoError(t, os.RemoveAll(l.Tmp))
	require.NoError(t, os.WriteFile(l.Tmp, []byte("not a directory"), 0o600))

	err := paths.ReplaceBloom(l, []byte("v1"), 1)
	require.Error(t, err)
}

func TestReplaceBloom_WriteStagingFileFails(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))
	// Block the exact staging filename ReplaceBloom will write with a directory of the same name.
	require.NoError(t, os.MkdirAll(filepath.Join(l.Tmp, "tried.bloom.1"), 0o700))

	err := paths.ReplaceBloom(l, []byte("v1"), 1)
	require.Error(t, err)
}

// TestHighestBloomBackupSeq_Table pins the scan two callers depend on: sketch.ReplaceGenerational
// pre-flights against it so a low sequence is refused before pruning can delete the backup a
// rollback would need, and SP-09 resumes its rebuild counter from it after a restart.
//
// The rows that matter most are the ones that must NOT count. It shares its parser with
// pruneBloomBackups precisely so that "what the pruner will delete" and "what this reports" cannot
// drift, and a name only one of the two recognised would put the two out of step in the direction
// that loses a file.
func TestHighestBloomBackupSeq_Table(t *testing.T) {
	t.Run("a sketches directory that does not exist is not an error", func(t *testing.T) {
		// The ordinary state of a project whose first session has not written a filter yet. An
		// error here would make a cold start indistinguishable from an unreadable store.
		l := paths.Of(filepath.Join(t.TempDir(), "no-such-project"))
		seq, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.False(t, ok)
		require.Zero(t, seq)
	})

	t.Run("an empty sketches directory reports no backup", func(t *testing.T) {
		l := newLayout(t)
		seq, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.False(t, ok)
		require.Zero(t, seq)
	})

	t.Run("the live file alone is not a backup", func(t *testing.T) {
		l := newLayout(t)
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))
		_, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.False(t, ok, "tried.bloom itself carries no sequence")
	})

	t.Run("the highest sequence wins, not the newest file", func(t *testing.T) {
		l := newLayout(t)
		// Written in ascending order and then a lower one last, because the whole reason this
		// function exists is that pruneBloomBackups keeps the highest rather than the newest.
		for _, n := range []string{"2", "10", "9", "3"} {
			require.NoError(t, os.WriteFile(
				filepath.Join(l.Sketches, "tried.bloom."+n+".bak"), []byte("v"+n), 0o600))
		}
		seq, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, 10, seq, "10 beats 9 numerically; a lexical comparison would answer 9")
	})

	t.Run("unrelated, malformed and directory entries are ignored", func(t *testing.T) {
		l := newLayout(t)
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom.4.bak"), []byte("v4"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "touch.cms"), []byte("cms"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "segment-12.bloom"), []byte("seg"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom.notanumber.bak"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom.99.bak.old"), []byte("x"), 0o600))
		// A directory whose name IS backup-shaped: skipped like the pruner skips it, since a
		// directory is not a generation and must not raise the floor a caller's seq has to clear.
		require.NoError(t, os.MkdirAll(filepath.Join(l.Sketches, "tried.bloom.999.bak"), 0o700))

		seq, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, 4, seq)
	})

	t.Run("what ReplaceBloom leaves behind is what this reports", func(t *testing.T) {
		// The two must agree by construction, which is why they share bloomBackupSeq: this is the
		// arrangement sketch.ReplaceGenerational pre-flights against.
		l := newLayout(t)
		require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom"), []byte("v0"), 0o600))
		require.NoError(t, paths.ReplaceBloom(l, []byte("v1"), 1))
		require.NoError(t, paths.ReplaceBloom(l, []byte("v2"), 9))

		seq, ok, err := paths.HighestBloomBackupSeq(l)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, 9, seq, "pruning kept 9.bak, so 9 is the sequence a caller must exceed")
	})
}

// TestHighestBloomBackupSeq_UnreadableDirIsAnError is the other half of the missing-directory rule:
// a directory that exists but cannot be listed must NOT read as "no backups". Answering false there
// would tell sketch.ReplaceGenerational that any sequence is safe, which is exactly the state the
// pre-flight exists to refuse.
//
// It uses icacls for the same reason TestReplaceBloom_PruneSurvivesUnreadableSketchesDir does: a
// chmod is a no-op for an administrator on Windows and would make the assertion vacuous.
func TestHighestBloomBackupSeq_UnreadableDirIsAnError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: icacls is windows-specific")
	}
	u, err := user.Current()
	if err != nil {
		t.Skipf("platform: could not determine current user: %v", err)
	}

	l := newLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(l.Sketches, "tried.bloom.7.bak"), []byte("v7"), 0o600))

	if out, denyErr := exec.Command("icacls", l.Sketches, "/deny", u.Username+":(RD)").CombinedOutput(); denyErr != nil {
		t.Skipf("platform: icacls deny unavailable in this environment: %v: %s", denyErr, out)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("icacls", l.Sketches, "/remove:d", u.Username).CombinedOutput()
	})

	seq, ok, err := paths.HighestBloomBackupSeq(l)
	require.Error(t, err, "an unreadable directory must not be reported as an empty one")
	require.False(t, ok)
	require.Zero(t, seq)
}

// TestReplacePinsViewLandsUnderAnOpenReader is the ReplacePinsView half of the property
// TestOpenSharedLetsWriteAtomicLandUnderAnOpenReader asserts for WriteAtomic, and it belongs here
// because pins/invariants.json is the file in this tree that is most continuously held open: it is
// read at session start to decide which invariants are live, and the daemon's materialize_pins
// idle task rewrites it on every idle tick with no dirty check. The window in which some reader
// holds it open is therefore not a rare race — it is re-opened for the life of the daemon.
//
// A reader is not passive on Windows. os.Rename is MoveFileEx, whose replace step fails with
// ERROR_ACCESS_DENIED whenever the destination has ANY open handle, at ANY share mode. So a
// ReplacePinsView that finished with a bare os.Rename could not replace the view for as long as
// anybody was reading it — not "races with", could not finish. Going through renameWithRetry, and
// therefore through posixReplace's FILE_RENAME_POSIX_SEMANTICS, is what makes the consenting-reader
// case land, exactly as it already does for WriteAtomic.
//
// Both rows are asserted rather than skipped off Windows: rename(2) has always had these
// semantics, so the table states the platform difference instead of hiding it.
func TestReplacePinsViewLandsUnderAnOpenReader(t *testing.T) {
	const before = `{"invariants":["the generation a reader already opened"]}`
	const after = `{"invariants":["the replacement"]}`

	cases := []struct {
		name string
		open func(p string) (*os.File, error)
		// windowsBlocks is whether a holder that opened the view this way still blocks the replace
		// on Windows. Only the plain os.Open handle does; that row is the guard half of the table.
		windowsBlocks bool
	}{
		{name: "held with os.Open", open: os.Open, windowsBlocks: true},
		{name: "held with paths.OpenShared", open: paths.OpenShared, windowsBlocks: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLayout(t)
			view := filepath.Join(l.Pins, "invariants.json")
			require.NoError(t, paths.ReplacePinsView(l, []byte(before)))

			f, err := tc.open(view)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()

			err = paths.ReplacePinsView(l, []byte(after))
			if tc.windowsBlocks && runtime.GOOS == "windows" {
				require.Error(t, err,
					"guard: os.Open's handle is supposed to block the replace on Windows. If it no "+
						"longer does, paths.OpenShared has stopped being load-bearing — do not just "+
						"relax this assertion")
				return
			}
			require.NoError(t, err,
				"a reader that consented to the replace must not be able to stall the materializer: "+
					"invariants.json is re-read at every session start and rewritten on every idle tick")

			buf := make([]byte, len(before))
			n, readErr := f.Read(buf)
			require.NoError(t, readErr)
			require.Equal(t, before, string(buf[:n]),
				"the open handle must keep reading the generation it opened, exactly as on POSIX")

			landed, err := os.ReadFile(view)
			require.NoError(t, err)
			require.Equal(t, after, string(landed), "the replacement must be what is on disk")
		})
	}
}

// TestReplacePinsViewReplacesTheViewInFull pins the ordinary path: the view is a projection
// replaced whole rather than appended to, it stays owner-writable (it is derived state, not an
// immutable checkpoint artifact), and the staging file is cleaned up rather than left in l.Tmp.
func TestReplacePinsViewReplacesTheViewInFull(t *testing.T) {
	l := newLayout(t)
	view := filepath.Join(l.Pins, "invariants.json")

	require.NoError(t, paths.ReplacePinsView(l, []byte(`{"invariants":[]}`)))
	require.NoError(t, paths.ReplacePinsView(l, []byte(`{"invariants":["a"]}`)))

	got, err := os.ReadFile(view)
	require.NoError(t, err)
	require.Equal(t, `{"invariants":["a"]}`, string(got))

	fi, err := os.Stat(view)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode().Perm()&0o200, "the view must stay owner-writable")

	entries, err := os.ReadDir(l.Tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "no staging file may survive a successful replace")
}

// TestReplacePinsView_MkdirStagingFailsPreservesView covers the first staging boundary without
// changing l.Tmp itself: another caller may still own that directory. A copied layout names a
// regular file as its staging path, so MkdirAll must fail before ReplacePinsView has created any
// staging file or touched the prior materialized view.
func TestReplacePinsView_MkdirStagingFailsPreservesView(t *testing.T) {
	l := newLayout(t)
	view := filepath.Join(l.Pins, "invariants.json")
	const before = `{"invariants":["old evidence"]}`
	require.NoError(t, paths.ReplacePinsView(l, []byte(before)))

	bad := l
	bad.Tmp = filepath.Join(t.TempDir(), "tmp-is-a-file")
	require.NoError(t, os.WriteFile(bad.Tmp, []byte("not a directory"), 0o600))

	err := paths.ReplacePinsView(bad, []byte(`{"invariants":["new evidence"]}`))
	require.Error(t, err)

	got, readErr := os.ReadFile(view)
	require.NoError(t, readErr)
	require.Equal(t, before, string(got), "a staging setup failure must leave the prior view intact")

	entries, readDirErr := os.ReadDir(l.Tmp)
	require.NoError(t, readDirErr)
	require.Empty(t, entries, "the untouched real staging directory must contain no leaked file")
}

// TestReplacePinsView_DestinationFailurePreservesEvidenceAndCleansStaging covers the other side
// of the write barrier: once staging has succeeded, a failed replacement must still retain the
// destination and defer-clean the staged file.
func TestReplacePinsView_DestinationFailurePreservesEvidenceAndCleansStaging(t *testing.T) {
	t.Run("nonempty destination directory", func(t *testing.T) {
		l := newLayout(t)
		bad := l
		bad.Pins = filepath.Join(l.Dot, "pins-blocked")
		view := filepath.Join(bad.Pins, "invariants.json")
		sentinel := filepath.Join(view, "old-evidence.json")
		require.NoError(t, os.MkdirAll(view, 0o700))
		require.NoError(t, os.WriteFile(sentinel, []byte(`{"invariants":["old evidence"]}`), 0o600))

		err := paths.ReplacePinsView(bad, []byte(`{"invariants":["new evidence"]}`))
		require.Error(t, err, "a file cannot replace a nonempty destination directory")

		got, readErr := os.ReadFile(sentinel)
		require.NoError(t, readErr)
		require.Equal(t, `{"invariants":["old evidence"]}`, string(got),
			"the destination evidence must survive a failed replacement")

		entries, readDirErr := os.ReadDir(l.Tmp)
		require.NoError(t, readDirErr)
		require.Empty(t, entries, "the staged replacement must be removed after a failed rename")
	})

	// On Windows a plain os.Open handle forbids the destination replacement. POSIX intentionally
	// permits renaming over an open file, so this row is Windows-only rather than a vacuous chmod
	// approximation on a privileged Unix runner.
	t.Run("plain reader blocks replacement on Windows", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("platform: POSIX permits renaming over an open file")
		}
		l := newLayout(t)
		view := filepath.Join(l.Pins, "invariants.json")
		const before = `{"invariants":["old evidence"]}`
		require.NoError(t, paths.ReplacePinsView(l, []byte(before)))

		f, err := os.Open(view)
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })

		err = paths.ReplacePinsView(l, []byte(`{"invariants":["new evidence"]}`))
		require.Error(t, err, "a plain Windows reader must block a replacement it did not share")

		got, readErr := os.ReadFile(view)
		require.NoError(t, readErr)
		require.Equal(t, before, string(got), "the old view must remain authoritative after failure")

		entries, readDirErr := os.ReadDir(l.Tmp)
		require.NoError(t, readDirErr)
		require.Empty(t, entries, "the failed replacement must not leak its staging file")
	})
}

// TestReplacePinsView_DeniedStagingCreatePreservesView drives CreateTemp's permission failure.
// Windows needs an ACL deny rather than chmod: an administrator can write through directory mode
// bits, which would make this row pass without exercising the failure. The deny and its removal
// apply only to this test's t.TempDir-owned staging directory.
func TestReplacePinsView_DeniedStagingCreatePreservesView(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: icacls is windows-specific")
	}
	u, err := user.Current()
	if err != nil {
		t.Skipf("platform: could not determine current user: %v", err)
	}

	l := newLayout(t)
	view := filepath.Join(l.Pins, "invariants.json")
	const before = `{"invariants":["old evidence"]}`
	require.NoError(t, paths.ReplacePinsView(l, []byte(before)))

	if out, denyErr := exec.Command("icacls", l.Tmp, "/deny", u.Username+":(WD)").CombinedOutput(); denyErr != nil {
		t.Skipf("platform: icacls deny unavailable in this environment: %v: %s", denyErr, out)
	}
	denied := true
	t.Cleanup(func() {
		if denied {
			_, _ = exec.Command("icacls", l.Tmp, "/remove:d", u.Username).CombinedOutput()
		}
	})

	err = paths.ReplacePinsView(l, []byte(`{"invariants":["new evidence"]}`))
	require.Error(t, err, "CreateTemp must report a staging directory that denies new files")

	got, readErr := os.ReadFile(view)
	require.NoError(t, readErr)
	require.Equal(t, before, string(got), "a denied staging create must leave the prior view intact")

	if out, clearErr := exec.Command("icacls", l.Tmp, "/remove:d", u.Username).CombinedOutput(); clearErr != nil {
		t.Fatalf("remove test-owned deny ACE: %v: %s", clearErr, out)
	}
	denied = false
	entries, readDirErr := os.ReadDir(l.Tmp)
	require.NoError(t, readDirErr)
	require.Empty(t, entries, "a denied CreateTemp must not leave a staging file")
}
