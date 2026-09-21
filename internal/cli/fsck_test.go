package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
)

// The fsck and doctor tests assert the JSON DOCUMENT rather than this package's own structs.
// `--json` is the contract an operator's script branches on, and a test written against the Go
// types would keep passing through a rename that breaks every such script. The one exception is
// the repair seam (TestFsck_RepairSeamGetsTheOperatorsOptions), which is about wiring this package
// owns and nothing outside it can observe.

// fsckDispatch runs one subcommand in process through the real dispatch table.
func fsckDispatch(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var out, errw bytes.Buffer
	argv := append([]string{"qompack"}, args...)
	code := Dispatch(context.Background(), All(), argv, Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(nil),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	return code, out.String(), errw.String()
}

// fsckDispatchIn is fsckDispatch with QOMPACK_PROJECT_ROOT bound, for the subcommands that take no
// --project flag of their own (status).
func fsckDispatchIn(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()

	var out, errw bytes.Buffer
	argv := append([]string{"qompack"}, args...)
	code := Dispatch(context.Background(), All(), argv, Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   bytes.NewReader(nil),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	return code, out.String(), errw.String()
}

// fsckJSON runs `qompack fsck --json --project <root>` and decodes the report generically.
func fsckJSON(t *testing.T, root string, extra ...string) (int, map[string]any, string) {
	t.Helper()

	args := append([]string{"fsck", "--project", root, "--json"}, extra...)
	code, out, errw := fsckDispatch(t, args...)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &doc), "stdout=%s stderr=%s", out, errw)
	return code, doc, errw
}

// fsckRow returns the check row with id, and whether it is present.
func fsckRow(t *testing.T, doc map[string]any, id string) (map[string]any, bool) {
	t.Helper()

	checks, _ := doc["checks"].([]any)
	for _, c := range checks {
		row, _ := c.(map[string]any)
		if row["id"] == id {
			return row, true
		}
	}
	return nil, false
}

// fsckRequireRow returns the check row with id, failing when it is absent.
func fsckRequireRow(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()

	row, ok := fsckRow(t, doc, id)
	require.True(t, ok, "no check row %q in %v", id, fsckRowIDs(doc))
	return row
}

// fsckRowIDs lists every row id, for a failure message that says what WAS reported.
func fsckRowIDs(doc map[string]any) []string {
	checks, _ := doc["checks"].([]any)
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		row, _ := c.(map[string]any)
		id, _ := row["id"].(string)
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// fsckDetail joins one row's detail list, for substring assertions about the class named.
func fsckDetail(row map[string]any) string {
	details, _ := row["detail"].([]any)
	parts := make([]string, 0, len(details))
	for _, d := range details {
		s, _ := d.(string)
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

// seededProject is one temp project with a store, a tool_use record, a file version, a pin and one
// finalized checkpoint — enough content that every fsck check has a population to scan and a
// zero-defect result is distinguishable from a vacuous one.
type seededProject struct {
	Root  string
	Layot paths.Layout
	Chunk core.Hash // the one object every root in the fixture is made of
	CPSeq core.CheckpointSeq
}

// seedFsckProject builds that project through the product's own writers.
func seedFsckProject(t *testing.T) seededProject {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))
	l := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(l))

	ctx := context.Background()
	s, err := store.Open(root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)

	res, err := s.PutBytes(ctx, []byte("package main\n\nfunc main() {}\n"),
		store.PutOptions{Tool: "Read", Path: "src/main.go"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Root.Chunks)

	require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
		ID: core.ToolUseID("tu-fsck-1"), Session: core.SessionID("s-fsck"), Turn: 1,
		TS: 1, Tool: "Read", Root: res.Root.Hash, Path: "src/main.go", Bytes: res.Root.RawBytes,
	}))
	require.NoError(t, s.AppendFileVersion(ctx, "src/main.go", store.FileVersion{
		TS: 1, Root: res.Root.Hash, Turn: 1, Bytes: res.Root.RawBytes,
	}))
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())

	ps, err := pins.OpenWith(root, logging.Nop(), obs.New(testClock()), testClock())
	require.NoError(t, err)
	require.NoError(t, ps.Add(ctx, pins.Invariant{Text: "the daemon owns the single writer", Source: "user"}))
	require.NoError(t, ps.Materialize(ctx))

	seq := writeCheckpointArtifact(t, l, checkpoint.Checkpoint{
		Session: core.SessionID("s-fsck"), Seq: 1, Created: "2026-08-12T10:30:00.000Z",
	})

	return seededProject{Root: root, Layot: l, Chunk: res.Root.Chunks[0].Hash, CPSeq: seq}
}

// writeCheckpointArtifact writes one checkpoint the way Finalize does: Marshal, CreateNew, then
// the manifest line through paths.AppendManifest, the only legal writer.
func writeCheckpointArtifact(t *testing.T, l paths.Layout, cp checkpoint.Checkpoint) core.CheckpointSeq {
	t.Helper()

	b, err := checkpoint.Marshal(cp)
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, cp.Seq), b))
	sum := sha256.Sum256(b)
	require.NoError(t, paths.AppendManifest(l, paths.ManifestEntry{
		Seq: cp.Seq, SHA256: core.Hash(sum).String(), Bytes: int64(len(b)), Created: 1,
	}))
	return cp.Seq
}

// objectFile returns the on-disk path of h's object, trying both spellings the store's own reader
// tries (internal/store/objects.go objectCandidates).
func objectFile(t *testing.T, l paths.Layout, h core.Hash) string {
	t.Helper()

	hx := strings.TrimPrefix(h.String(), "sha256:")
	dir := filepath.Join(l.Objects, hx[0:2], hx[2:4])
	for _, name := range []string{hx + ".zst", hx} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(paths.Long(p)); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	t.Fatalf("no object file for %s under %s", h.Short(), dir)
	return ""
}

// appendLine appends one raw line to a JSONL file, which is how every "a torn line survived" seed
// here is made: the product's own writers cannot produce one.
func appendLine(t *testing.T, p, line string) {
	t.Helper()

	f, err := os.OpenFile(paths.Long(p), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // a temp fixture path
	require.NoError(t, err)
	_, werr := f.WriteString(line + "\n")
	require.NoError(t, werr)
	require.NoError(t, f.Close())
}

// hexDigest is the bare sha256 a backup manifest records.
func hexDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestFsck_CleanProjectFindsNoDefect is the calibration row: a project built entirely through the
// product's own writers must report no defect at all, and must report a POPULATION for the checks
// that have one, so a later zero-defect answer is distinguishable from a scan that ran over
// nothing (the reason test/fault's own audit carries counts).
func TestFsck_CleanProjectFindsNoDefect(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	code, doc, errw := fsckJSON(t, p.Root)

	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	require.EqualValues(t, 1, doc["schema"])
	require.Equal(t, false, doc["daemon_running"])
	require.Equal(t, true, doc["read_only"])
	require.EqualValues(t, ExitOK, doc["exit"])

	checks, _ := doc["checks"].([]any)
	require.NotEmpty(t, checks)
	for _, c := range checks {
		row, _ := c.(map[string]any)
		require.Equal(t, true, row["ok"], "check %v reported a defect on a clean project: %s",
			row["id"], fsckDetail(row))
	}

	for _, id := range []string{"objects", "index.roots", "index.tool_use", "checkpoints", "pins"} {
		row := fsckRequireRow(t, doc, id)
		require.NotNil(t, row["scanned"], "check %q must report what it scanned", id)
	}
}

// TestFsck_AbsentStoreIsReportedAndNothingIsCreated pins the §3.3 read-only discipline every other
// non-hook command follows: a query may not bring a project into existence.
func TestFsck_AbsentStoreIsReportedAndNothingIsCreated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))

	code, doc, errw := fsckJSON(t, dir)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	require.EqualValues(t, ExitOK, doc["exit"])

	row := fsckRequireRow(t, doc, "store")
	require.Equal(t, true, row["ok"])
	require.Contains(t, fsckDetail(row), "no store")

	_, err := os.Stat(filepath.Join(dir, ".qompack"))
	require.True(t, os.IsNotExist(err), "fsck must not create .qompack: %v", err)
}

// TestFsck_RepairWithoutYesIsAUsageError pins the both-halves usage contract admin delivery-seal
// established: the command writes its own message and the exit code says the operator typed it
// wrong rather than that the run failed.
func TestFsck_RepairWithoutYesIsAUsageError(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	code, out, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, errw, "--yes")
	require.Empty(t, out, "a usage error writes nothing to stdout")
}

// TestFsck_ReportsEachSeededDefectAndRepairsNone is the check-class table.
//
// Every row damages ONE thing through a write the product's own writers cannot make, then asserts
// three things: the named check reports not-ok, its detail names the CLASS of defect (not merely
// "something is wrong"), and the default run repaired nothing — `repairs` is empty and the damage
// is still on disk afterwards. "No destructive default cleanup" is plan §3's wording and it is the
// property an operator relies on before they have read the output.
func TestFsck_ReportsEachSeededDefectAndRepairsNone(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		check string
		// seed damages the project and returns a path whose CONTENT must be unchanged afterwards.
		seed  func(t *testing.T, p seededProject) string
		names string
	}{
		{
			name: "an object whose compressed frame fails its CRC", check: "objects", names: "does not decode",
			seed: func(t *testing.T, p seededProject) string {
				return flipOneBit(t, objectFile(t, p.Layot, p.Chunk))
			},
		},
		{
			name:  "an object that decodes cleanly to content that is not its own name",
			check: "objects", names: "re-hash",
			seed: func(t *testing.T, p seededProject) string {
				// A VALID zstd frame under the wrong name: it decodes, and only the content
				// address catches it. A bit flip cannot produce this — zstd's own CRC catches
				// that first — so the re-hash step needs its own seed or it is never exercised.
				path := objectFile(t, p.Layot, p.Chunk)
				encoded, err := store.Encode([]byte("bytes that are not what this address names\n"))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(path), encoded, 0o600))
				return path
			},
		},
		{
			name: "a roots.jsonl line that does not parse", check: "index.roots", names: "does not parse",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Index, "roots.jsonl")
				appendLine(t, path, `{"v":1,"op":"","root":`)
				return path
			},
		},
		{
			name: "a roots.jsonl record version this build does not know", check: "index.roots", names: "record version",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Index, "roots.jsonl")
				appendLine(t, path, `{"v":99,"op":"","root":"`+p.Chunk.String()+`","chunks":[]}`)
				return path
			},
		},
		{
			name: "a roots.jsonl op this build does not know", check: "index.roots",
			names: `carries op "vacuum", which this build does not know`,
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Index, "roots.jsonl")
				appendLine(t, path, `{"v":1,"op":"vacuum","root":"`+p.Chunk.String()+`"}`)
				return path
			},
		},
		{
			name: "a tool_use.jsonl line that does not parse", check: "index.tool_use", names: "does not parse",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Index, "tool_use.jsonl")
				appendLine(t, path, `{"id":`)
				return path
			},
		},
		{
			name:  "an encode record naming a checkpoint seq the manifest never recorded",
			check: "index.segments", names: "MANIFEST",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Index, "segments.jsonl")
				appendLine(t, path, `{"v":1,"op":"encode","id":7,"seq":4242,"ts":1}`)
				return path
			},
		},
		{
			name: "a capture sidecar left at stage one", check: "captures", names: "stage 1",
			seed: seedStageOneSidecar,
		},
		{
			name:  "a checkpoint artifact that no longer matches its manifest digest",
			check: "checkpoints", names: "digest",
			seed: func(t *testing.T, p seededProject) string {
				return flipOneBit(t, paths.CheckpointPath(p.Layot, p.CPSeq))
			},
		},
		{
			name: "a checkpoint artifact with no manifest line", check: "checkpoints", names: "orphan",
			seed: func(t *testing.T, p seededProject) string {
				b, err := checkpoint.Marshal(checkpoint.Checkpoint{
					Session: core.SessionID("s-fsck"), Seq: 9, Created: "2026-08-12T10:31:00.000Z",
				})
				require.NoError(t, err)
				path := paths.CheckpointPath(p.Layot, core.CheckpointSeq(9))
				require.NoError(t, paths.CreateNew(path, b))
				return path
			},
		},
		{
			name: "a pins tombstone naming an add nobody recorded", check: "pins", names: "tombstone",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.Pins, "invariants.jsonl")
				appendLine(t, path, `{"op":"remove","id":"inv_ffffffffffff","ts":2}`)
				return path
			},
		},
		{
			name: "a retention root naming bytes nothing holds", check: "retention", names: "not held",
			seed: func(t *testing.T, p seededProject) string {
				path := store.RetentionRootsPath(p.Root)
				appendLine(t, path, `{"hash":"sha256:`+strings.Repeat("ab", 32)+`","class":"lease","reason":"a lease that outlived its bytes"}`)
				return path
			},
		},
		{
			name: "a drain record whose offset is past its size", check: "spool", names: "offset",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.State, "drain.json")
				require.NoError(t, os.WriteFile(paths.Long(path),
					[]byte(`{"wal-0001.jsonl":{"size":10,"offset":99}}`), 0o600))
				return path
			},
		},
		{
			name: "a delivery journal line that does not parse", check: "delivery", names: "does not parse",
			seed: func(t *testing.T, p seededProject) string {
				path := filepath.Join(p.Layot.State, "delivery-leases.jsonl")
				appendLine(t, path, `{"v":1,`)
				return path
			},
		},
		{
			name: "a backup file that drifted from its manifest", check: "migrate", names: "digest",
			seed: seedDriftedBackup,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := seedFsckProject(t)
			damaged := tc.seed(t, p)
			before, err := os.ReadFile(paths.Long(damaged))
			require.NoError(t, err)

			code, doc, errw := fsckJSON(t, p.Root)
			require.Equal(t, ExitError, code, "a defect must exit 1; stderr=%s", errw)
			require.EqualValues(t, ExitError, doc["exit"])

			row := fsckRequireRow(t, doc, tc.check)
			require.Equal(t, false, row["ok"], "check %q must report the seeded defect", tc.check)
			require.Contains(t, strings.ToLower(fsckDetail(row)), strings.ToLower(tc.names),
				"check %q must name the class of defect, got %s", tc.check, fsckDetail(row))

			repairs, _ := doc["repairs"].([]any)
			require.Empty(t, repairs, "the default run repairs nothing")

			after, err := os.ReadFile(paths.Long(damaged))
			require.NoError(t, err)
			require.Equal(t, before, after, "the default run must leave %s exactly as it found it", damaged)
		})
	}
}

// TestFsck_UnreadableFilesViewIsNamedAsTheViewNotTheLog is finding 5: a present but
// unreadable index/files.json used to be reported as index/files.jsonl:0 and then as
// "index/files.json is absent". The defect names the view file, and it must not claim
// the file is missing.
func TestFsck_UnreadableFilesViewIsNamedAsTheViewNotTheLog(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	view := filepath.Join(p.Layot.Index, "files.json")
	require.NoError(t, os.Remove(paths.Long(view)))
	require.NoError(t, os.MkdirAll(paths.Long(view), 0o700))

	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitError, code, "stderr=%s", errw)
	row := fsckRequireRow(t, doc, "index.files")
	require.Equal(t, false, row["ok"])
	detail := fsckDetail(row)
	require.Contains(t, detail, "index/files.json")
	require.NotContains(t, detail, "is absent")
	require.NotContains(t, detail, "index/files.jsonl:0")
	require.NotContains(t, detail, "declares view version")
	require.NotContains(t, detail, "omits")
	require.EqualValues(t, 1, row["count"], "an unreadable view is one defect, not phantom comparisons")
}

// flipOneBit is checkpoint/reader_test.go's own corruption seed, reused for objects as well: the
// artifact stays the same length and stays readable, so only a re-hash can tell it apart from the
// one the manifest recorded. It returns the path it damaged.
func flipOneBit(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.NotEmpty(t, b)
	require.NoError(t, os.Chmod(paths.Long(p), 0o600))
	b[len(b)/2] ^= 0x20
	require.NoError(t, os.WriteFile(paths.Long(p), b, 0o600))
	return p
}

// seedStageOneSidecar writes the ONE sidecar state that is a gap: a tool delivery whose outcome is
// ok and whose bytes are durable, with no reference ever joined to it. Every other unpublished
// sidecar is by design (commit4-evidence.md §8, calibration rule 2).
func seedStageOneSidecar(t *testing.T, p seededProject) string {
	t.Helper()

	id := core.ObservationID(strings.Repeat("ab", 32))
	require.NoError(t, store.WriteCaptureSidecar(p.Root, store.CaptureSidecar{
		ObservationID: id,
		Session:       core.SessionID("s-fsck"),
		Op:            string(ipc.OpObserveTool),
		Outcome:       core.OutcomeOK,
		Bytes:         []byte("the tool result these bytes came from"),
	}))
	path, err := store.CaptureSidecarPath(p.Root, id)
	require.NoError(t, err)
	return path
}

// seedDriftedBackup writes one backup whose manifest no longer describes the bytes under its tree.
func seedDriftedBackup(t *testing.T, p seededProject) string {
	t.Helper()

	dir := filepath.Join(p.Layot.Backup, "bk-fsck")
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(dir, "tree", "index")), 0o700))
	body := []byte("{\"v\":1}\n")
	file := filepath.Join(dir, "tree", "index", "roots.jsonl")
	require.NoError(t, os.WriteFile(paths.Long(file), body, 0o600))

	man := map[string]any{
		"version": 1, "id": "bk-fsck", "root": p.Root, "taken_at": 1,
		"consistent": true, "snapshot_id": "", "frontier": 0,
		"files": []any{map[string]any{
			"name": "index/roots.jsonl", "size": len(body), "sha256": hexDigest(body),
		}},
	}
	encoded, err := json.Marshal(man)
	require.NoError(t, err)
	manifest := filepath.Join(dir, "manifest.json")
	require.NoError(t, os.WriteFile(paths.Long(manifest), encoded, 0o600))

	// The drift: the tree's bytes move, the manifest does not.
	require.NoError(t, os.WriteFile(paths.Long(file), []byte("{\"v\":2}\n"), 0o600))
	return manifest
}

// TestFsck_ANewerCheckpointSchemaIsNotCorrupt keeps two classes apart that a naive reader collapses.
// checkpoint/reader_test.go:515 already pins that a newer-version artifact is not a manifest
// mismatch; this pins that fsck REPORTS it as written by a newer plugin rather than as corruption,
// which is §7.1's "unknown host/schema environments report unsupported/degraded capability instead
// of guessing".
func TestFsck_ANewerCheckpointSchemaIsNotCorrupt(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)

	// A well-formed artifact whose only unusual property is a version this build does not know,
	// with a manifest line whose digest is correct: nothing about it is damaged.
	raw := []byte(`{"version":99,"session":"s-fsck","seq":5,"created":"2026-08-12T10:32:00.000Z"}` + "\n")
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(p.Layot, core.CheckpointSeq(5)), raw))
	sum := sha256.Sum256(raw)
	require.NoError(t, paths.AppendManifest(p.Layot, paths.ManifestEntry{
		Seq: core.CheckpointSeq(5), SHA256: core.Hash(sum).String(), Bytes: int64(len(raw)), Created: 1,
	}))

	_, doc, errw := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "checkpoints")
	detail := fsckDetail(row)
	require.Equal(t, true, row["ok"], "a newer schema is not a defect: %s", detail)
	require.Contains(t, detail, "newer plugin", "stderr=%s", errw)
	require.NotContains(t, strings.ToLower(detail), "corrupt",
		"a newer schema is not corruption: %s", detail)
	require.NotContains(t, detail, "does not re-hash")
}

// TestFsck_CanonicalStaysCanonicalInTheFidelitySummary is plan §3's "without converting
// missing/unknown fidelity into exactness", pinned where it can actually be got wrong: a root
// stored with no verbatim claim and no retained original restores to CANONICAL bytes, which read
// back perfectly and are still not the original bytes.
func TestFsck_CanonicalStaysCanonicalInTheFidelitySummary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	ctx := context.Background()
	s, err := store.Open(root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	// Trailing whitespace the canonicalizer removes, with KeepRaw off: the store keeps the
	// canonical form and makes no recovery claim about the original.
	_, err = s.PutBytes(ctx, []byte("alpha   \nbeta\t\t\n"), store.PutOptions{Tool: "Read", Path: "a.txt"})
	require.NoError(t, err)
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())

	_, doc, errw := fsckJSON(t, root)
	fidelity, ok := doc["fidelity"].(map[string]any)
	require.True(t, ok, "the report carries a fidelity summary; stdout had %v, stderr=%s", doc, errw)
	require.Zero(t, fidelity["exact"], "a canonical-only root must never be counted exact: %v", fidelity)
	require.NotZero(t, fidelity["canonical"], "the canonical root must be counted as canonical: %v", fidelity)
}

// TestFsck_ALiveDaemonMakesTheAnswerASnapshot pins the one thing a read-only integrity scan owes a
// user when it cannot promise a quiet project: saying so. It must still run and still exit 0 on a
// clean project rather than refusing.
//
// It binds a REAL listener rather than only taking the lock, because "is a daemon running" is now
// decided by the staleness protocol's own liveness dial and not by the lock file (ruling R5-C): a
// lock with nothing answering behind it is a stale lock, which is the sibling test below.
func TestFsck_ALiveDaemonMakesTheAnswerASnapshot(t *testing.T) {
	// Not parallel: it takes the project's singleton lock and binds its endpoint.
	p := seedFsckProject(t)
	serveFakeDaemon(t, p.Root)

	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	require.Equal(t, true, doc["daemon_running"])

	row := fsckRequireRow(t, doc, "daemon")
	require.Contains(t, fsckDetail(row), "snapshot of a moving target")
}

// TestFsck_AStaleLockDisablesNoCheck is the failure ruling R5-C exists to stop.
//
// daemon.ReadLock answers "a lock file exists and parses", and the first version took that for "a
// daemon is running" — so a lock naming a pid nothing is behind made the fidelity pass and the
// delivery-seal check skip themselves and still report ok, and fsck exited 0 on a project it had
// barely looked at. A stale lock must now be NAMED as stale and disable nothing.
func TestFsck_AStaleLockDisablesNoCheck(t *testing.T) {
	// Not parallel: it writes the project's singleton lock file.
	p := seedFsckProject(t)
	require.NoError(t, os.MkdirAll(paths.Long(p.Layot.Run), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(daemon.LockPath(p.Root)),
		[]byte(`{"pid":999999,"started":1,"addr":"","version":"0.1.0"}`), 0o600))

	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	require.Equal(t, false, doc["daemon_running"],
		"a lock file with nothing answering behind it is not a running daemon")
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "daemon")), "STALE")

	fidelity := fsckRequireRow(t, doc, "fidelity")
	require.NotNil(t, fidelity["scanned"],
		"the fidelity pass must still run: a stale lock may not disable a check")
	require.NotZero(t, doc["fidelity"], "and it must have counted something")
}

// serveFakeDaemon binds the project's own endpoint and holds its singleton lock for the test, so
// the liveness dial every diagnostic makes actually answers. It is not a daemon: it routes nothing.
func serveFakeDaemon(t *testing.T, root string) {
	t.Helper()

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true}
		})
	}()

	lock, err := daemon.AcquireLock(root, addr, testClock())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = lock.Release()
		cancel()
		_ = srv.Close()
		<-done
	})

	require.Eventually(t, func() bool { return ipc.Probe(addr, selfTestProbeTimeout) },
		5*time.Second, 10*time.Millisecond, "the fake daemon's endpoint never came up")
}

// TestFsck_RepairIsRefusedWhileADaemonHoldsTheLock is the same refusal the offline delivery-seal
// tool makes, and for the same reason: a repair on a project someone else is writing is a race, not
// a repair. Nothing may be written when it refuses.
func TestFsck_RepairIsRefusedWhileADaemonHoldsTheLock(t *testing.T) {
	// Not parallel: it takes the project's singleton lock.
	p := seedFsckProject(t)
	staleView := filepath.Join(p.Layot.Index, "files.json")
	require.NoError(t, os.WriteFile(paths.Long(staleView), []byte(`{"version":1,"files":{}}`), 0o600))

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	lock, err := daemon.AcquireLock(p.Root, addr, testClock())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	// AFTER the lock exists: daemon.lock and daemon.hb are the TEST's own files, and the claim
	// being made is about what the refused repair leaves behind.
	before := snapshotQompack(t, p.Layot)

	code, _, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes")
	require.Equal(t, ExitError, code)
	require.Contains(t, errw, "a daemon is running in")
	require.Contains(t, errw, "stop it first")

	require.Equal(t, before, snapshotQompack(t, p.Layot),
		"a refused repair must leave every file as it found it")
}

// TestFsck_RepairPerformsOnlyTheFiveAdditiveKinds is the rollback-safety row §3 asks for: after a
// repair, everything an older reader reads is unchanged. It snapshots .qompack/ before and after
// and asserts that the ONLY differences are the five explicit repairs — no journal, no index log,
// no sidecar, no checkpoint artifact and no backup moved.
func TestFsck_RepairPerformsOnlyTheFiveAdditiveKinds(t *testing.T) {
	// Not parallel: it takes the project's singleton lock.
	p := seedFsckProject(t)

	// Two derived views deliberately made to disagree with their logs, and one orphan artifact:
	// three of the five repairs, each of which is additive or a regeneration.
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(p.Layot.Index, "files.json")),
		[]byte(`{"version":1,"files":{}}`), 0o600))
	require.NoError(t, paths.ReplacePinsView(p.Layot, []byte("[]\n")))

	orphanBody, err := checkpoint.Marshal(checkpoint.Checkpoint{
		Session: core.SessionID("s-fsck"), Seq: 4, Created: "2026-08-12T10:33:00.000Z",
	})
	require.NoError(t, err)
	orphan := paths.CheckpointPath(p.Layot, core.CheckpointSeq(4))
	require.NoError(t, paths.CreateNew(orphan, orphanBody))

	before := snapshotQompack(t, p.Layot)

	code, out, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes", "--json")
	require.NotEqual(t, ExitUsage, code, "stderr=%s", errw)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &doc), "stdout=%s stderr=%s", out, errw)
	require.Equal(t, false, doc["read_only"])

	repairs, _ := doc["repairs"].([]any)
	require.NotEmpty(t, repairs, "the three seeded derived defects must be repaired")
	kinds := map[string]bool{}
	for _, r := range repairs {
		row, _ := r.(map[string]any)
		kind, _ := row["kind"].(string)
		kinds[kind] = true
		require.NotEmpty(t, row["before"], "repair %v must report what it found", row)
		require.NotEmpty(t, row["after"], "repair %v must report what it left", row)
	}
	require.True(t, kinds["files.view"], "repairs were %v", kinds)
	require.True(t, kinds["pins.view"], "repairs were %v", kinds)
	require.True(t, kinds["checkpoint.manifest"], "repairs were %v", kinds)

	after := snapshotQompack(t, p.Layot)
	for path, sum := range after {
		prev, existed := before[path]
		if existed && prev == sum {
			continue
		}
		require.True(t, isRepairablePath(path),
			"repair changed %s, which is not one of the five additive/derived repair targets", path)
	}
	for path := range before {
		_, still := after[path]
		require.True(t, still, "repair deleted %s; fsck never deletes", path)
	}

	// The orphan's own bytes are untouched: reconciling it appends a manifest line, it does not
	// rewrite the artifact.
	nowBody, err := os.ReadFile(paths.Long(orphan))
	require.NoError(t, err)
	require.Equal(t, orphanBody, nowBody)

	// And the project is clean afterwards: every live reference still resolves, and the three
	// defects the repair addressed are gone rather than merely reported differently.
	after2, doc2, errw2 := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, after2, "stderr=%s", errw2)
	for _, id := range []string{"index.roots", "index.tool_use", "index.files", "pins", "checkpoints"} {
		require.Equal(t, true, fsckRequireRow(t, doc2, id)["ok"],
			"check %q still reports a defect after the repair: %s",
			id, fsckDetail(fsckRequireRow(t, doc2, id)))
	}
}

// snapshotQompack digests EVERY path under .qompack, directories included, and skips nothing.
//
// It used to skip run/, tmp/, logs/ and metrics/ — the four a backup also refuses to copy — and that
// exemption is exactly what let the first version of these commands create twenty-odd files without
// a test noticing: doctor manufactured the five index files through store.Open, and fsck created
// .qompack/run/ by acquiring the daemon lock inside its seal check. A read-only claim is only
// checkable against the whole tree.
func snapshotQompack(t *testing.T, l paths.Layout) map[string]string {
	t.Helper()

	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(paths.Long(l.Dot), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(paths.Long(l.Dot), p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[rel] = hexDigest(b)
		return nil
	}))
	return out
}

// isRepairablePath reports whether a path under .qompack is one of the five explicit repair
// targets. Anything else changing is the failure the snapshot exists to catch.
func isRepairablePath(rel string) bool {
	switch {
	case rel == "index/files.json", rel == "pins/invariants.json":
		return true // the two derived views, regenerated from their logs
	case rel == "checkpoints/MANIFEST.jsonl":
		return true // an orphan artifact's line, appended through paths.AppendManifest
	case rel == "sketches/tried.bloom" || strings.HasPrefix(rel, "sketches/tried.bloom."):
		return true // the rebuilt filter and the one generational backup it leaves
	case rel == "tmp/" || rel == "tmp/quarantine/" || strings.HasPrefix(rel, "tmp/quarantine/"):
		return true // an object moved to quarantine keeps its bytes as evidence
	}
	return false
}

// fsckRepairProbe is written through the Out the front end handed the repair, so the test can prove
// that writer really is the one Dispatch was given rather than merely non-nil.
const fsckRepairProbe = "the repair wrote this through o.Out\n"

// TestFsck_RepairSeamGetsTheOperatorsOptions pins the wiring itself, for the reason admin.go's own
// seam exists: the most damaging edit is invisible to every other test in this package.
//
// A front end that passed Confirm: true unconditionally would grant an operator's consent on their
// behalf for every run, and performFsckRepairs' own gate would accept it — it refuses a MISSING
// confirmation, and such an edit supplies one. So the seam is swapped for a capture and the WHOLE
// option set is compared: a row that types neither --repair nor --yes must reach the repair not at
// all, and the row that types both must reach it with exactly those two.
func TestFsck_RepairSeamGetsTheOperatorsOptions(t *testing.T) {
	// Not parallel, and it must not become so: it swaps a package-level seam. Go resumes paused
	// parallel tests only after every sequential test has returned.
	dir := seedFsckProject(t).Root
	repairErr := errors.New("the project moved under the repair")

	for _, tc := range []struct {
		name  string
		args  []string
		ret   error
		want  int
		calls int
		opts  fsckRepairOptions
	}{
		{
			name: "no --repair reaches the repair at all", args: nil, want: ExitOK, calls: 0,
		},
		{
			name: "--repair --yes asks for the repair with the confirmation typed",
			args: []string{"--repair", "--yes"}, want: ExitOK, calls: 1,
			opts: fsckRepairOptions{ProjectRoot: dir, Confirm: true},
		},
		{
			name: "--yes alone confirms nothing, because no repair was asked for",
			args: []string{"--yes"}, want: ExitOK, calls: 0,
		},
		{
			name: "a refused repair is exit 1, not exit 2 and not exit 0",
			args: []string{"--repair", "--yes"}, ret: repairErr, want: ExitError, calls: 1,
			opts: fsckRepairOptions{ProjectRoot: dir, Confirm: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got fsckRepairOptions
			calls := 0
			prev := runFsckRepairs
			t.Cleanup(func() { runFsckRepairs = prev })
			runFsckRepairs = func(o fsckRepairOptions) ([]fsckRepair, error) {
				calls++
				got = o
				fmt.Fprint(o.Out, fsckRepairProbe)
				return nil, tc.ret
			}

			var out, errw bytes.Buffer
			clk := testClock()
			argv := append([]string{"qompack", "fsck", "--project", dir}, tc.args...)
			code := Dispatch(context.Background(), All(), argv, Env{
				Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: clk, HomeDir: t.TempDir(),
			}, &out, &errw)

			require.Equal(t, tc.calls, calls, "stderr=%s", errw.String())
			require.Equal(t, tc.want, code, "stderr=%s", errw.String())
			if tc.calls == 0 {
				return
			}

			// The whole struct, so a field this row did not ask for fails here whichever it is.
			want := tc.opts
			want.Out, want.Clock = got.Out, got.Clock
			require.Equal(t, want, got, "the flags the operator typed are the options the repair gets")
			require.Equal(t, clk, got.Clock,
				"the repair reads the clock Dispatch was handed, never core.SystemClock()")
			require.Contains(t, out.String(), fsckRepairProbe,
				"o.Out must be the stdout Dispatch was handed, so the report reaches the operator")
			if tc.ret != nil {
				require.Contains(t, errw.String(), tc.ret.Error())
				require.Contains(t, errw.String(), "qompack fsck:",
					"and it is reported once, by the command, not twice by Dispatch as well")
			}
		})
	}
}

// TestFsck_TheViewRepairGoesThroughTheStoresOwnRegenerator replaces the version pin SP-17 R5-1
// retired.
//
// index/files.json had no exported writer, so --repair carried a private copy of the view's shape
// and of its version constant, and the only thing holding that copy to the original was a test that
// read the number back out of a flushed project. internal/store now exports RegenerateFilesView and
// this command calls it, so there is no copy left to drift; what can still regress is the repair
// going back to writing the view itself. That is what this asserts. After a repair the view on disk
// is exactly the exported regenerator's projection of the log, and the regenerator agrees there is
// nothing left to do.
func TestFsck_TheViewRepairGoesThroughTheStoresOwnRegenerator(t *testing.T) {
	// Not parallel: --repair takes the project's singleton lock.
	p := seedFsckProject(t)
	viewPath := filepath.Join(p.Layot.Index, "files.json")

	raw, err := os.ReadFile(paths.Long(viewPath))
	require.NoError(t, err, "the seeded project flushes, so the store has written its own view")
	var flushed store.FilesView
	require.NoError(t, json.Unmarshal(raw, &flushed))
	require.Equal(t, store.FilesViewVersion, flushed.Version,
		"the version a flushed store writes is the one the exported constant names")

	// Stale the view so the repair has to regenerate it.
	require.NoError(t, os.WriteFile(paths.Long(viewPath), []byte(`{"version":1,"files":{}}`), 0o600))
	code, _, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes")
	require.NotEqual(t, ExitUsage, code, "stderr=%s", errw)

	repaired, err := os.ReadFile(paths.Long(viewPath))
	require.NoError(t, err)
	var got store.FilesView
	require.NoError(t, json.Unmarshal(repaired, &got))
	require.Equal(t, store.FilesViewVersion, got.Version)

	log, defects, err := store.ReplayFilesLog(p.Layot)
	require.NoError(t, err)
	require.Empty(t, defects, "the seeded log parses cleanly")
	require.Equal(t, log, got.Files,
		"the repaired view is not the exported regenerator's projection of index/files.jsonl")

	wrote, err := store.RegenerateFilesView(p.Layot, testClock())
	require.NoError(t, err)
	require.False(t, wrote,
		"the repaired view already describes its log; the regenerator rewrote it anyway")
}

// TestFsck_RegisteredInAllAndNotAHook asserts both commands are in the real dispatch table and that
// neither carries the Hook flag: §2.3 reserves the always-exit-0 guarantee for hooks, and fsck's
// whole contract is that it may exit 1.
func TestFsck_RegisteredInAllAndNotAHook(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}
	for _, c := range All() {
		switch c.Name {
		case "fsck", "doctor":
			require.False(t, c.Hook, "%s must not be a Hook command", c.Name)
			require.NotEmpty(t, c.Summary)
			found[c.Name] = true
		}
	}
	require.True(t, found["fsck"], "fsck is not registered in All()")
	require.True(t, found["doctor"], "doctor is not registered in All()")
}

// TestFsck_TableModeWritesAReadableSummary asserts the default (non-JSON) rendering names every
// check, the defect it found and the two tallies an operator reads at the bottom. The table is what
// a person sees; `--json` is what a script sees, and both have to be exercised.
func TestFsck_TableModeWritesAReadableSummary(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	appendLine(t, filepath.Join(p.Layot.Index, "roots.jsonl"), `{"v":1,"op":"","root":`)

	code, out, errw := fsckDispatch(t, "fsck", "--project", p.Root)
	require.Equal(t, ExitError, code, "stderr=%s", errw)
	require.Contains(t, out, "CHECK")
	require.Contains(t, out, "index.roots")
	require.Contains(t, out, "does not parse")
	require.Contains(t, out, "fidelity:")
	require.Contains(t, out, "quarantine:")
	require.Contains(t, out, "exit: 1")
	require.Contains(t, errw, "defective check")
}

// TestFsck_RepairQuarantinesAFailingObjectAndRebuildsTheFilter covers the two repairs the derived
// views cannot reach, and it is the row that proves the quarantine repair preserves evidence rather
// than deleting it: the object's bytes must still exist somewhere afterwards.
func TestFsck_RepairQuarantinesAFailingObjectAndRebuildsTheFilter(t *testing.T) {
	// Not parallel: it takes the project's singleton lock.
	p := seedFsckProject(t)

	// A valid zstd frame under the wrong name: it decodes, and only the content address catches it.
	objPath := objectFile(t, p.Layot, p.Chunk)
	encoded, err := store.Encode([]byte("bytes that are not what this address names\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(objPath), encoded, 0o600))

	// One active elimination beside a tried.bloom that fails its own CRC.
	require.NoError(t, os.MkdirAll(paths.Long(p.Layot.Records), 0o700))
	appendLine(t, filepath.Join(p.Layot.Records, "eliminations.jsonl"),
		`{"id":"elm_1","session":"s-fsck","ts":1,"target":"a.go:f","approach":"widen the pool",`+
			`"reason":"it deadlocks","status":"active","scope":"project","source":"mcp"}`)
	require.NoError(t, os.MkdirAll(paths.Long(p.Layot.Sketches), 0o700))
	bloomPath := filepath.Join(p.Layot.Sketches, "tried.bloom")
	require.NoError(t, os.WriteFile(paths.Long(bloomPath), []byte("not a sketch at all"), 0o600))

	// The read-only pass names both, and repairs neither.
	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitError, code, "stderr=%s", errw)
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "objects")), "re-hash")
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "negknow")), "tried.bloom")
	require.FileExists(t, paths.Long(objPath), "the read-only pass never moves an object")

	code, out, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes", "--json")
	require.NotEqual(t, ExitUsage, code, "stderr=%s", errw)
	var repaired map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &repaired), "stdout=%s stderr=%s", out, errw)

	kinds := map[string]string{}
	repairs, _ := repaired["repairs"].([]any)
	for _, r := range repairs {
		row, _ := r.(map[string]any)
		kind, _ := row["kind"].(string)
		after, _ := row["after"].(string)
		kinds[kind] = after
	}
	require.Contains(t, kinds, "object.quarantine", "repairs were %v", kinds)
	require.Contains(t, kinds["object.quarantine"], "quarantine",
		"the object's bytes are preserved as evidence, never deleted")
	require.Contains(t, kinds, "negknow.bloom", "repairs were %v", kinds)

	require.NoFileExists(t, paths.Long(objPath), "the failing object left objects/")
	require.NotEmpty(t, quarantinedFiles(t, p.Layot), "and its bytes are under tmp/quarantine/")
}

// quarantinedFiles lists every file under tmp/quarantine.
func quarantinedFiles(t *testing.T, l paths.Layout) []string {
	t.Helper()

	var out []string
	_ = filepath.WalkDir(paths.Long(filepath.Join(l.Tmp, "quarantine")),
		func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
	return out
}

// TestFsck_AndDoctor_CreateNothingOnAnyProjectShape is ruling R5-A's enforcement, and it is the
// test the first version of both commands did not have.
//
// Without it, `doctor` on a directory holding only `.qompack/` created 26 paths — the five index
// files as empty, `.qompack/.gitignore`, `tmp/quarantine/` — because it reached the store through
// `store.Open`, which runs EnsureLayout and takes O_CREATE handles. Default `fsck` created 24 on a
// project with one object, and `.qompack/run/` even on one with no roots, because its delivery-seal
// check acquired the daemon lock. An operator inspecting a half-restored backup would have the
// missing index files manufactured as empty, and the next pass would report a clean empty index
// instead of a missing one.
//
// The snapshot covers the WHOLE tree, directories included: the exemption for run/, tmp/, logs/ and
// metrics/ is precisely what hid this.
func TestFsck_AndDoctor_CreateNothingOnAnyProjectShape(t *testing.T) {
	t.Parallel()

	for _, shape := range []struct {
		name  string
		build func(t *testing.T) string
	}{
		{name: "a bare .qompack directory", build: bareQompackProject},
		{name: "one roots line and one object", build: oneObjectProject},
		{name: "a laid-out project with no roots at all", build: emptyLaidOutProject},
		{name: "a fully seeded project", build: func(t *testing.T) string { return seedFsckProject(t).Root }},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			root := shape.build(t)
			l := paths.Of(root)

			for _, argv := range [][]string{
				{"fsck", "--project", root, "--json"},
				{"doctor", "--project", root, "--json"},
			} {
				before := snapshotQompack(t, l)
				code, _, errw := fsckDispatch(t, argv...)
				require.NotEqual(t, ExitUsage, code, "stderr=%s", errw)

				after := snapshotQompack(t, l)
				require.Equal(t, before, after,
					"`qompack %s` created or changed something; new/changed paths: %v",
					argv[0], treeDiff(before, after))
			}
		})
	}
}

// treeDiff names what a snapshot gained or lost, so a failure says WHICH path moved.
func treeDiff(before, after map[string]string) []string {
	var out []string
	for p, sum := range after {
		if prev, had := before[p]; !had {
			out = append(out, "created "+p)
		} else if prev != sum {
			out = append(out, "changed "+p)
		}
	}
	for p := range before {
		if _, still := after[p]; !still {
			out = append(out, "removed "+p)
		}
	}
	sort.Strings(out)
	return out
}

// bareQompackProject is a directory holding nothing but an empty `.qompack/`: the half-restored
// shape, where manufacturing the index files as empty is the damaging answer.
func bareQompackProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".qompack"), 0o700))
	return dir
}

// oneObjectProject holds one roots.jsonl line and the object it names, and nothing else at all.
func oneObjectProject(t *testing.T) string {
	t.Helper()

	dir := bareQompackProject(t)
	l := paths.Of(dir)
	body := []byte("one object and one index line\n")
	h := core.HashBytes(core.DomainChunk, body)
	encoded, err := store.Encode(body)
	require.NoError(t, err)

	hx := strings.TrimPrefix(h.String(), "sha256:")
	objDir := filepath.Join(l.Objects, hx[0:2], hx[2:4])
	require.NoError(t, os.MkdirAll(paths.Long(objDir), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(objDir, hx+".zst")), encoded, 0o600))

	require.NoError(t, os.MkdirAll(paths.Long(l.Index), 0o700))
	appendLine(t, filepath.Join(l.Index, "roots.jsonl"), fmt.Sprintf(
		`{"v":1,"op":"","root":%q,"ts":1,"tool":"Read","path":"a.txt","raw":%d,"canon":%d,`+
			`"chunks":[{"h":%q,"n":%d}]}`,
		h.String(), len(body), len(body), h.String(), len(body)))
	return dir
}

// emptyLaidOutProject is a project EnsureLayout has created and nothing has written to.
func emptyLaidOutProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
	require.NoError(t, paths.EnsureLayout(paths.Of(dir)))
	return dir
}

// TestFsck_ReportsEveryDanglingReferenceClass seeds the four classes commit4-evidence.md §8 calls
// ABSORB that a torn LINE cannot produce: a reference whose index line is perfectly well-formed and
// whose bytes are gone.
//
// They are the classes that matter most and the easiest to get vacuously right: an audit that asks
// store.Has answers from an in-memory chunk set built at Open and never stats a file, so every one
// of these reads as held and the whole tier reports clean. Deleting the object under a live index
// line is the only seed that tells the two implementations apart.
func TestFsck_ReportsEveryDanglingReferenceClass(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		check string
		names string
	}{
		{name: "a root's chunk", check: "index.roots", names: "which the object store does not hold"},
		{name: "a tool_use record's root", check: "index.tool_use", names: "cannot be materialized"},
		{name: "a checkpoint's file pointer", check: "checkpoints", names: "points at file"},
		{name: "a checkpoint's tool pointer", check: "checkpoints", names: "points at tool result"},
		{name: "a published capture sidecar's root", check: "captures", names: "does not resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := seedFsckProject(t)
			seedDanglingFixtures(t, p)
			require.NoError(t, os.Remove(paths.Long(objectFile(t, p.Layot, p.Chunk))),
				"the index lines survive; only the bytes are gone")

			code, doc, errw := fsckJSON(t, p.Root)
			require.Equal(t, ExitError, code, "stderr=%s", errw)

			row := fsckRequireRow(t, doc, tc.check)
			require.Equal(t, false, row["ok"], "check %q must report the dangling reference", tc.check)
			require.Contains(t, fsckDetail(row), tc.names,
				"check %q must name the class; got %s", tc.check, fsckDetail(row))
		})
	}
}

// TestFsck_ReportsADanglingDeltaPointer covers the v2 recovery pointers, whose absence is what turns
// a delta representation into an unrecoverable one (SP-20 invariant 6).
func TestFsck_ReportsADanglingDeltaPointer(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	absent := strings.Repeat("cd", 32)
	appendLine(t, filepath.Join(p.Layot.Index, "roots.jsonl"), fmt.Sprintf(
		`{"v":2,"op":"","root":"sha256:%s","ts":2,"tool":"Read","path":"b.txt","chunks":[],`+
			`"base":"sha256:%s","orig":"sha256:%s"}`,
		strings.Repeat("ef", 32), absent, absent))

	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitError, code, "stderr=%s", errw)
	detail := fsckDetail(fsckRequireRow(t, doc, "index.roots"))
	require.Contains(t, detail, "base pointer")
	require.Contains(t, detail, "orig pointer")
	require.Contains(t, detail, "does not resolve")
}

// seedDanglingFixtures adds the references the seeded project does not already carry: a checkpoint
// whose file pointer AND tool pointer name the fixture's only root, and a PUBLISHED capture sidecar
// whose root names it too. All three resolve until the object is deleted.
//
// The tool pointer is here because it was the one pointer class checkCheckpointPointers resolved
// with no seeded test behind it (SP-17 fix round 2, finding N-3): Pointers.Tools is a separate loop
// over a separate field, and a file-pointer test says nothing about whether it runs.
func seedDanglingFixtures(t *testing.T, p seededProject) {
	t.Helper()

	root := fixtureRootHash(t, p)
	writeCheckpointArtifact(t, p.Layot, checkpoint.Checkpoint{
		Session: core.SessionID("s-fsck"), Seq: 2, Created: "2026-08-12T10:34:00.000Z",
		Pointers: checkpoint.Pointers{
			Files: []checkpoint.FilePointer{{Path: "src/main.go", Hash: root, Why: "the entry point"}},
			Tools: []checkpoint.ToolPointer{{
				ToolUseID: core.ToolUseID("toolu_01FSCKDANGLINGAAAAAAAAAA"), Hash: root,
				Summary: "the tool result the checkpoint kept",
			}},
		},
	})

	id := core.ObservationID(strings.Repeat("cd", 32))
	require.NoError(t, store.WriteCaptureSidecar(p.Root, store.CaptureSidecar{
		ObservationID: id, Session: core.SessionID("s-fsck"),
		Op: string(ipc.OpObserveTool), Outcome: core.OutcomeOK, Bytes: []byte("the tool result"),
	}))
	require.NoError(t, store.LinkCaptureReference(p.Root, id, store.CaptureReference{
		ToolUseID: core.ToolUseID("tu-fsck-1"), Root: root,
	}))
}

// fixtureRootHash is the root the seeded project's one tool_use record points at.
func fixtureRootHash(t *testing.T, p seededProject) core.Hash {
	t.Helper()

	lines, err := os.ReadFile(paths.Long(filepath.Join(p.Layot.Index, "tool_use.jsonl")))
	require.NoError(t, err)
	var rec struct {
		Root string `json:"root"`
	}
	first := strings.SplitN(strings.TrimSpace(string(lines)), "\n", 2)[0]
	require.NoError(t, json.Unmarshal([]byte(first), &rec))
	h, err := core.ParseHash(rec.Root)
	require.NoError(t, err)
	return h
}

// TestFsck_AnUnreadableArtifactIsAReportedRow is ruling R5-D: a check that could not run is a
// finding, never a silent `continue`.
//
// The seed is a DIRECTORY where a file belongs (and a file where a directory belongs), which fails
// the read on every platform for a reason that is not fs.ErrNotExist — unlike a permission deny,
// which does not bite for a process that can override it. "The check did not run" and "the check
// found nothing" are the two answers this exists to keep apart: a diagnostic that reports the second
// when the first is true is worse than one that refuses to run at all.
func TestFsck_AnUnreadableArtifactIsAReportedRow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		check string
		// blockFile is replaced by a directory; blockDir is replaced by a file.
		blockFile func(l paths.Layout) string
		blockDir  func(l paths.Layout) string
		names     string
	}{
		{
			name: "migrate/cursor.json", check: "migrate", names: "unreadable",
			blockFile: func(l paths.Layout) string { return filepath.Join(l.Migrate, "cursor.json") },
		},
		{
			name: "migrate/mapping.jsonl", check: "migrate", names: "unreadable",
			blockFile: func(l paths.Layout) string { return filepath.Join(l.Migrate, "mapping.jsonl") },
		},
		{
			name: "state/delivery-lease-position.json", check: "delivery", names: "unreadable",
			blockFile: func(l paths.Layout) string {
				return filepath.Join(l.State, "delivery-lease-position.json")
			},
		},
		{
			name: "state/pending", check: "retention", names: "could not be listed",
			blockDir: func(l paths.Layout) string { return filepath.Join(l.State, "pending") },
		},
		{
			name: "checkpoints/", check: "checkpoints", names: "could not be listed",
			blockDir: func(l paths.Layout) string { return l.Checkpoints },
		},
		{
			name: "backup/", check: "migrate", names: "could not be listed",
			blockDir: func(l paths.Layout) string { return l.Backup },
		},
		{
			name: "pins/invariants.json", check: "pins", names: "does not parse",
			blockFile: func(l paths.Layout) string { return filepath.Join(l.Pins, "invariants.json") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := seedFsckProject(t)
			if tc.blockFile != nil {
				blockAsDirectory(t, tc.blockFile(p.Layot))
			}
			if tc.blockDir != nil {
				blockAsFile(t, tc.blockDir(p.Layot))
			}

			code, doc, errw := fsckJSON(t, p.Root)
			require.Equal(t, ExitError, code, "an I/O failure that prevented a check is a defect; stderr=%s", errw)
			row := fsckRequireRow(t, doc, tc.check)
			require.Equal(t, false, row["ok"], "check %q must report the read failure", tc.check)
			require.Contains(t, fsckDetail(row), tc.names, "got %s", fsckDetail(row))
		})
	}
}

// blockAsDirectory replaces p with a directory, so every read of it fails for a reason that is not
// "it is not there".
func blockAsDirectory(t *testing.T, p string) {
	t.Helper()

	_ = os.Remove(paths.Long(p))
	require.NoError(t, os.MkdirAll(paths.Long(p), 0o700))
}

// blockAsFile replaces directory p with a regular file, so ReadDir fails with ENOTDIR.
func blockAsFile(t *testing.T, p string) {
	t.Helper()

	require.NoError(t, os.RemoveAll(paths.Long(p)))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("not a directory"), 0o600))
}

// TestFsck_TheDefaultRunNeverTakesTheDaemonLock is ruling R5-B.
//
// The full dual-reader seal check goes through daemon.RepairDeliverySeal, which ACQUIRES the daemon
// lock for its whole run — so a default `fsck` that called it created `.qompack/run/` on every
// project it looked at, and competed for the singleton lock with whatever was about to start. It is
// now opt-in, and the default row says which question it did not ask.
func TestFsck_TheDefaultRunNeverTakesTheDaemonLock(t *testing.T) {
	// Not parallel: the --seal-check half takes the project's singleton lock.
	p := seedFsckProject(t)

	calls := 0
	prev := repairDeliverySeal
	t.Cleanup(func() { repairDeliverySeal = prev })
	repairDeliverySeal = func(o daemon.DeliverySealOptions) error {
		calls++
		require.True(t, o.Check, "fsck only ever asks the seal tool for a CHECK")
		return nil
	}

	_, doc, errw := fsckJSON(t, p.Root)
	require.Zero(t, calls, "the default run must not reach the seal tool at all; stderr=%s", errw)
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "delivery")), "--seal-check")

	_, doc, errw = fsckJSON(t, p.Root, "--seal-check")
	require.Equal(t, 1, calls, "--seal-check is what runs it; stderr=%s", errw)
	require.Contains(t, fsckDetail(fsckRequireRow(t, doc, "delivery")), "seal check passed")
}

// TestFsck_SealCheckIsNotReportedReadOnly is fix round 2's finding N-2.
//
// `read_only: true` was set from `!repairing` alone, so `--seal-check` — which acquires the daemon
// lock, and acquiring it creates `.qompack/run/` — reported itself as a run that wrote nothing. The
// field is the claim the whole command rests on, so a run that creates a directory must not make it.
func TestFsck_SealCheckIsNotReportedReadOnly(t *testing.T) {
	// Not parallel: it swaps the repairDeliverySeal seam, which is package state.
	p := seedFsckProject(t)

	prev := repairDeliverySeal
	t.Cleanup(func() { repairDeliverySeal = prev })
	repairDeliverySeal = func(daemon.DeliverySealOptions) error { return nil }

	_, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, true, doc["read_only"], "the default run writes nothing; stderr=%s", errw)
	require.Equal(t, false, doc["attempts_daemon_lock"], "and does not reach for the lock")
	require.Equal(t, false, doc["repairing"])

	_, doc, errw = fsckJSON(t, p.Root, "--seal-check")
	require.Equal(t, false, doc["read_only"],
		"--seal-check takes the daemon lock, which creates .qompack/run/; stderr=%s", errw)
	require.Equal(t, true, doc["attempts_daemon_lock"], "and the reason is named separately")
	require.Equal(t, false, doc["repairing"], "a seal check is not a repair")

	// The human table says so too: an operator reading the table must not have to consult the JSON
	// to learn that this run is the one that creates something.
	_, out, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--seal-check")
	require.Contains(t, out, "not read-only: this run attempts the daemon lock", "stderr=%s", errw)
	require.Contains(t, out, ".qompack/run/")

	// --repair takes the lock too, but the lock is the SMALLER half of what it does, so its line
	// leads with the writes rather than reusing the seal check's sentence.
	_, out, errw = fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes")
	require.Contains(t, out, "not read-only: --repair writes the repairs listed below",
		"stderr=%s", errw)
	require.Contains(t, out, ".qompack/run/")
}

// TestFsck_SealCheckAndRepairAreMutuallyExclusive: both take the daemon lock, and the repair holds
// it for its whole run, so a nested acquisition inside the scan would refuse itself.
func TestFsck_SealCheckAndRepairAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	code, _, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes", "--seal-check")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, errw, "both take the daemon lock")
}

// TestFsck_RepairOnACleanProjectWritesNothing is Minor M-1: the repair surface may not be wider
// than the defect surface.
//
// The first version regenerated index/files.json and pins/invariants.json unconditionally, so
// `--repair --yes` on a project with no defect at all still wrote two files — the scan called those
// same views ok on the same run. A repair that acts where the scan reported nothing is a repair an
// operator cannot predict.
func TestFsck_RepairOnACleanProjectWritesNothing(t *testing.T) {
	// Not parallel: it takes the project's singleton lock.
	p := seedFsckProject(t)

	code, doc, errw := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "the fixture must be clean first; stderr=%s", errw)
	require.Empty(t, doc["repairs"])

	before := snapshotQompack(t, p.Layot)
	code, out, errw := fsckDispatch(t, "fsck", "--project", p.Root, "--repair", "--yes", "--json")
	require.Equal(t, ExitOK, code, "stderr=%s", errw)

	var repaired map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &repaired), "stdout=%s", out)
	require.Empty(t, repaired["repairs"], "a clean project has nothing to repair")

	after := snapshotQompack(t, p.Layot)
	// run/ carries the lock the repair takes and releases; everything else must be untouched.
	for path, sum := range after {
		if strings.HasPrefix(path, "run/") {
			continue
		}
		require.Equal(t, before[path], sum, "--repair changed %s on a clean project", path)
	}
	require.Len(t, after, len(before), "and it created nothing: %v", treeDiff(before, after))
}

// TestFsck_TheObjectSizeLimitIsTheStoresOwn is fix round 2's finding N-5.
//
// checkObjects bounded every object file at 2*store.MaxPutBytes, a bound fsck invented so it would
// not read an unbounded file. But readObjectFile refuses a .zst candidate past the ENCODER's
// worst-case encoded size, which is barely above MaxPutBytes — so every compressed object between
// those two figures was a file fsck called acceptable and the store then refused, a defect the
// report did not have. The limits now come from the store, one per candidate spelling, exactly as
// readObjectFile splits them.
//
// It asserts the relation rather than writing a 64 MiB file: the bug was the CHOICE of bound, and a
// test that spent two minutes materializing one would still only be checking this arithmetic.
func TestFsck_TheObjectSizeLimitIsTheStoresOwn(t *testing.T) {
	t.Parallel()

	zst := fsckObjectSizeLimit("abcd" + fsckObjectSuffix)
	bare := fsckObjectSizeLimit("abcd")

	require.Equal(t, store.EncodedObjectLimit(), zst,
		"a .zst candidate is bounded at what the store's own reader refuses")
	require.Equal(t, int64(store.MaxPutBytes), bare,
		"a bare candidate is bounded at the plaintext limit, as readObjectFile bounds it")
	require.Greater(t, zst, bare, "framing overhead means the encoded limit is the larger one")
	require.Less(t, zst, int64(2*store.MaxPutBytes),
		"and it is well under the bound this build used to apply, which is the whole finding")
	require.Less(t, zst, bare+int64(store.MaxPutBytes)/2,
		"the overhead is framing, not a second copy of the object")
}
