package sketch_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is package sketch_test, not package sketch, and that is load-bearing rather than
// stylistic: internal/testutil imports internal/store, which imports internal/sketch, so an
// in-package _test.go that reached for testutil would be an import cycle and would not compile. Go
// permits an EXTERNAL test package to depend on packages that depend on the package under test,
// and everything asserted below is part of the exported surface, so the split costs nothing.

// The sketch dimensions every case here builds with. They are small because none of these tests is
// about accuracy — a one-key Bloom exercises Save, Load and ReplaceGenerational exactly as a
// ten-thousand-key one does, and a smaller frame keeps the corruption helpers legible.
const (
	testBloomCapacity = 1000
	testBloomFPRate   = 0.01
	testCMSEpsilon    = 0.001
	testCMSDelta      = 0.01
	testHLLRegisters  = 256
)

// crcTrailerLen is the width of the CRC32C every QPKS frame ends with. corruptBodyByte needs it to
// aim a flipped bit at the body rather than at the checksum itself.
const crcTrailerLen = 4

// logRecord is one call made through the logging.Logger seam, with the fields the With chain had
// accumulated at the time already merged into kv.
type logRecord struct {
	level string
	msg   string
	kv    []any
}

// recordingLogger is a logging.Logger that keeps every call instead of writing one.
//
// It exists because the Load-versus-LoadWithLog split is a contract about WHO logs, and the only
// way to assert "Load produced zero records" is to hold a logger Load was never given and show it
// stayed empty. A file-backed logger could not distinguish "Load logged nothing" from "Load logged
// somewhere else".
//
// The records live behind a pointer so that a logger derived through With shares its parent's
// buffer, which is what a real Logger implementation does too.
type recordingLogger struct {
	base []any
	recs *[]logRecord
}

// newRecordingLogger returns a recordingLogger with an empty buffer.
func newRecordingLogger() recordingLogger {
	return recordingLogger{recs: &[]logRecord{}}
}

// records returns every call made through this logger or any logger derived from it.
func (l recordingLogger) records() []logRecord { return *l.recs }

// loud returns only the Loud calls — the §12 "degradation is loud" channel.
func (l recordingLogger) loud() []logRecord {
	var out []logRecord
	for _, r := range *l.recs {
		if r.level == "loud" {
			out = append(out, r)
		}
	}
	return out
}

func (l recordingLogger) With(kv ...any) logging.Logger {
	return recordingLogger{base: mergeKV(l.base, kv), recs: l.recs}
}

func (l recordingLogger) Debug(msg string, kv ...any) { l.record("debug", msg, kv) }
func (l recordingLogger) Info(msg string, kv ...any)  { l.record("info", msg, kv) }
func (l recordingLogger) Warn(msg string, kv ...any)  { l.record("warn", msg, kv) }
func (l recordingLogger) Error(msg string, kv ...any) { l.record("error", msg, kv) }
func (l recordingLogger) Loud(msg string, kv ...any)  { l.record("loud", msg, kv) }

// record appends one call, merging the With-accumulated fields ahead of the call's own.
func (l recordingLogger) record(level, msg string, kv []any) {
	*l.recs = append(*l.recs, logRecord{level: level, msg: msg, kv: mergeKV(l.base, kv)})
}

// mergeKV concatenates two key-value runs into a fresh slice, so no derived logger can write
// through into its parent's backing array.
func mergeKV(base, extra []any) []any {
	merged := make([]any, 0, len(base)+len(extra))
	merged = append(merged, base...)
	return append(merged, extra...)
}

// kvOf returns the value recorded under key in r's key-value run.
func kvOf(r logRecord, key string) (any, bool) {
	for i := 0; i+1 < len(r.kv); i += 2 {
		if name, ok := r.kv[i].(string); ok && name == key {
			return r.kv[i+1], true
		}
	}
	return nil, false
}

// sketchesOf returns <root>/.qompack/sketches for p — the §7.4 directory every sketch file lives
// in, and the one ReplaceGenerational reconstructs a paths.Layout from.
func sketchesOf(p *testutil.Project) string { return paths.Of(p.Root).Sketches }

// filledBloom returns a Bloom holding n distinct keys, so that Count() alone identifies which
// generation a decoded filter came from. A membership probe could not: a Bloom answers Test with a
// false-positive probability, and an assertion that is right 99 % of the time is not an assertion.
func filledBloom(n int) *sketch.Bloom {
	b := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	for i := range n {
		b.Add(fmt.Appendf(nil, "generation-key-%d", i))
	}
	return b
}

// corruptBodyByte flips the last byte before the trailing CRC32C — inside the body, clear of every
// declared length — so the ONLY check the file can now fail is the checksum. A flip in ParamCount,
// BodyLen or a NameLen would trip ErrTruncated or ErrMalformed first and the test would pass
// without ever exercising the CRC.
func corruptBodyByte(t *testing.T, p string) {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Greater(t, len(b), crcTrailerLen, "fixture sanity: the frame must have a body to corrupt")
	b[len(b)-crcTrailerLen-1] ^= 0xFF
	require.NoError(t, os.WriteFile(p, b, 0o600))
}

// bloomBackups lists the tried.bloom.<seq>.bak files in dir. The glob is deliberately loose so a
// backup written under an unexpected sequence format still shows up and fails the count assertion
// rather than being silently invisible to it.
func bloomBackups(t *testing.T, dir string) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, "tried.bloom.*.bak"))
	require.NoError(t, err)
	return got
}

// TestSave_Load_RoundTrip is the §5.7 happy path: a sketch saved through Save is byte-identically
// recoverable through Load, estimate for estimate.
func TestSave_Load_RoundTrip(t *testing.T) {
	p := testutil.NewProject(t)
	target := filepath.Join(sketchesOf(p), "touch.cms")

	keys := [][]byte{[]byte("src/main.go"), []byte("internal/sketch/io.go"), []byte("README.md")}
	want := sketch.NewCMS(testCMSEpsilon, testCMSDelta)
	for i, k := range keys {
		want.Add(k, uint32(i+1))
	}

	require.NoError(t, sketch.Save(target, want))
	fi, err := os.Stat(target)
	require.NoError(t, err)
	require.NotZero(t, fi.Size(), "Save must leave a non-empty frame on disk")

	got := sketch.NewCMS(testCMSEpsilon, testCMSDelta)
	require.NoError(t, sketch.Load(target, got))
	require.Equal(t, want.Total(), got.Total())
	for _, k := range keys {
		require.Equal(t, want.Estimate(k), got.Estimate(k), "estimate for %q must survive the round trip", k)
	}
}

// TestSave_Load_RoundTripOutsideAProject pins the directory contract the plan got wrong.
//
// paths.WriteAtomic stages under <root>/.qompack/tmp when the target resolves to a project root and
// under filepath.Dir(target) when it does not, so Save works in a bare t.TempDir() with no .qompack
// tree anywhere above it. SP-01's sketchtest shape block already relies on this; asserting it here
// keeps a future "Save requires a project layout" refactor from breaking that suite silently.
func TestSave_Load_RoundTripOutsideAProject(t *testing.T) {
	target := filepath.Join(t.TempDir(), "explore.hll")

	want := sketch.NewHLL(testHLLRegisters)
	for i := range 64 {
		want.Add(fmt.Appendf(nil, "path/%d", i))
	}
	require.NoError(t, sketch.Save(target, want))

	got := sketch.NewHLL(testHLLRegisters)
	require.NoError(t, sketch.Load(target, got))
	require.Equal(t, want.Cardinality(), got.Cardinality())
}

// TestLoadWithLog_IsTheLoudPath pins the split contract, and pins the two halves with two different
// instruments because they are observable in two different places.
//
// LoadWithLog is checked with the recording logger it is handed. Load cannot be: it takes no Logger,
// so a recorder proves only that no package-global default appeared behind its back — real, since
// that global would be exactly the mutable package state this package refuses and the
// silent-by-default path §13 invariant 10 forbids, but blind to what Load actually does. Load hands
// LoadWithLog a logging.Nop, and Nop writes no log LINE while still calling recordLoud and still
// firing the process-wide observer. That channel is what logging.AttachLoudObserver exposes, and it
// is the instrument with teeth for this half.
func TestLoadWithLog_IsTheLoudPath(t *testing.T) {
	p := testutil.NewProject(t)
	target := filepath.Join(sketchesOf(p), "split.bloom")
	require.NoError(t, sketch.Save(target, filledBloom(4)))
	corruptBodyByte(t, target)

	var observed []string
	logging.AttachLoudObserver(func(msg string, _ ...any) { observed = append(observed, msg) })
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })

	rec := newRecordingLogger()

	require.Error(t, sketch.Load(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate)))
	require.Empty(t, rec.records(),
		"this asserts only that no package-global logger appeared behind Load's back; the recorder "+
			"was never handed to Load, so it cannot observe what Load really emits")
	require.Equal(t, []string{"sketch corrupt — rebuilding from records"}, observed,
		"Load DOES reach the process-wide Loud channel through logging.Nop, so an obs counter wired "+
			"up at a composition root still counts this. The accurate claim about Load is the "+
			"narrower one — it writes no log line — not that it is silent")

	observed = nil
	require.Error(t, sketch.LoadWithLog(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate), rec))
	require.Len(t, rec.loud(), 1, "LoadWithLog delivers §5.7's Loud half exactly once")
	require.Len(t, rec.records(), 1, "and emits nothing on any other level")
	require.Empty(t, observed,
		"recordingLogger is a test double rather than a logging.logger, so it does not touch the "+
			"process-wide ring — which is precisely why the Load half above needed the observer")
}

// TestSave_RefusesTriedBloom is the mechanical enforcement of §7.4: the append-only invariant is a
// refusal in code rather than a comment asking callers to be careful.
func TestSave_RefusesTriedBloom(t *testing.T) {
	p := testutil.NewProject(t)
	target := filepath.Join(sketchesOf(p), "tried.bloom")

	err := sketch.Save(target, filledBloom(1))
	require.Error(t, err)
	require.ErrorIs(t, err, sketch.ErrGenerational, "the sentinel that names the sanctioned door")
	require.ErrorIs(t, err, core.ErrAppendOnly,
		"the sentinel the rest of the tree already branches on (paths.IsProtected, testutil.AssertAppendOnly)")

	_, statErr := os.Stat(target)
	require.ErrorIs(t, statErr, fs.ErrNotExist, "a refused Save must not have created the file")
}

// TestSave_AllowsOtherBloomNames pins that the refusal is keyed on the exact base name §3.3 names,
// not on the .bloom extension: SP-16's per-segment filters are ordinary, replaceable sketches.
func TestSave_AllowsOtherBloomNames(t *testing.T) {
	p := testutil.NewProject(t)
	target := filepath.Join(sketchesOf(p), "segment-12.bloom")

	want := filledBloom(3)
	require.NoError(t, sketch.Save(target, want))

	got := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(target, got))
	require.Equal(t, want.Count(), got.Count())
}

// TestSave_MarshalFailurePropagates asserts a sketch that cannot encode is reported rather than
// half-written: no file appears, and the encoder's own sentinel survives.
func TestSave_MarshalFailurePropagates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nil.bloom")

	err := sketch.Save(target, (*sketch.Bloom)(nil))
	require.ErrorIs(t, err, sketch.ErrMalformed)
	require.NoFileExists(t, target, "a frame that could not be built must not leave a file behind")
}

// TestLoad_MissingFile pins the ordinary cold-start case: no file yet is core.ErrNotFound, which is
// the same branch a caller takes for a corrupt one (§13 invariant 3 — a sketch is a cache).
func TestLoad_MissingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "absent.cms")

	err := sketch.Load(target, sketch.NewCMS(testCMSEpsilon, testCMSDelta))
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Equal(t, fmt.Sprintf("%v: %s", core.ErrNotFound, target), err.Error(),
		"the not-exist branch reports the path and nothing else; an OS error appended here would mean "+
			"the stat failed for some other reason and the wrong branch ran")
}

// TestLoad_CorruptIsNotFoundAndCorrupt is the filesystem half of the §5.7 CRC contract, and it is
// load-bearing by name: sketchtest/suite.go's runFlippedBitRejectionCase forward-references it,
// having deliberately kept its own assertion in memory so it would not depend on io.go. Renaming or
// merging this test away would silently drop the half that suite gave up.
func TestLoad_CorruptIsNotFoundAndCorrupt(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rotted.bloom")
	require.NoError(t, sketch.Save(target, filledBloom(5)))
	corruptBodyByte(t, target)

	err := sketch.Load(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate))
	require.ErrorIs(t, err, core.ErrNotFound,
		"§12.3: a caller's natural not-found path must also be its corrupt path")
	require.ErrorIs(t, err, sketch.ErrCorrupt,
		"and the underlying sentinel must stay inspectable, so an operator can tell bit rot from an absent file")
}

// TestLoadWithLog_LoudOnCorrupt asserts the Loud line carries what an operator needs to act on it:
// which file rotted, and what the decoder said.
func TestLoadWithLog_LoudOnCorrupt(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rotted.bloom")
	require.NoError(t, sketch.Save(target, filledBloom(5)))
	corruptBodyByte(t, target)

	rec := newRecordingLogger()
	err := sketch.LoadWithLog(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate), rec)
	require.ErrorIs(t, err, sketch.ErrCorrupt)

	loud := rec.loud()
	require.Len(t, loud, 1)

	gotPath, ok := kvOf(loud[0], "path")
	require.True(t, ok, "the Loud line must name the file: %v", loud[0].kv)
	require.Equal(t, target, gotPath)

	gotErr, ok := kvOf(loud[0], "err")
	require.True(t, ok, "the Loud line must carry the decoder's own message: %v", loud[0].kv)
	require.Contains(t, gotErr, sketch.ErrCorrupt.Error())
}

// TestLoad_OversizeFileRejected asserts the size guard fires from os.Stat, BEFORE os.ReadFile.
//
// The distinguishing evidence is the message: reaching os.ReadFile would have pulled
// MaxFrameBytes+1 bytes into memory and then reported the corruption message from the
// UnmarshalBinary branch. Asserting the frame-limit message is therefore an assertion about which
// branch ran, which is the property that makes a hostile 64 MiB file cheap to reject.
func TestLoad_OversizeFileRejected(t *testing.T) {
	target := filepath.Join(t.TempDir(), "huge.bloom")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	oversize := int64(sketch.MaxFrameBytes) + 1
	// Truncate extends without writing, so the fixture costs no disk and no time.
	require.NoError(t, os.Truncate(target, oversize))

	rec := newRecordingLogger()
	err := sketch.LoadWithLog(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate), rec)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.ErrorIs(t, err, sketch.ErrTooLarge)

	loud := rec.loud()
	require.Len(t, loud, 1)
	require.Equal(t, "sketch file exceeds frame limit", loud[0].msg,
		"the corruption message here would mean os.ReadFile had already read the whole hostile file")
	gotBytes, ok := kvOf(loud[0], "bytes")
	require.True(t, ok, "the Loud line must report the size it refused: %v", loud[0].kv)
	require.Equal(t, oversize, gotBytes)
}

// TestLoad_UnreadableFileIsNotFound covers the os.ReadFile failure branch: a path that stats
// cleanly and reads back an error. A directory is the portable way to produce exactly that — Stat
// reports it with size 0, so the frame-limit guard passes, and the read then fails on every
// platform this ships to.
func TestLoad_UnreadableFileIsNotFound(t *testing.T) {
	dir := t.TempDir()

	rec := newRecordingLogger()
	err := sketch.LoadWithLog(dir, sketch.NewBloom(testBloomCapacity, testBloomFPRate), rec)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.ErrorContains(t, err, dir, "the failure must name the path it could not read")
	require.Empty(t, rec.records(),
		"an unreadable file is an I/O failure, not the corruption §12.3 asks to be shouted about")
}

// TestLoad_UnstattableFileIsNotFound covers the OTHER stat branch: a stat that fails for a reason
// that is not "does not exist". A NUL byte inside the path produces exactly that on both Windows
// and POSIX (syscall.UTF16PtrFromString / syscall.BytePtrFromString both report EINVAL), so the
// case is portable without depending on a permission model.
func TestLoad_UnstattableFileIsNotFound(t *testing.T) {
	target := filepath.Join(t.TempDir(), "bad\x00name.bloom")

	err := sketch.Load(target, sketch.NewBloom(testBloomCapacity, testBloomFPRate))
	require.ErrorIs(t, err, core.ErrNotFound)
	require.NotEqual(t, fmt.Sprintf("%v: %s", core.ErrNotFound, target), err.Error(),
		"this is the stat-FAILED branch, which appends the OS error; the not-exist branch reports the path alone")
}

// TestReplaceGenerational_FirstWrite pins the cold-start shape: no previous file means no backup,
// and the empty backup path is how a caller learns that.
func TestReplaceGenerational_FirstWrite(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	backup, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.NoError(t, err)
	require.Empty(t, backup, "there was nothing to back up")
	require.FileExists(t, target)
	require.Empty(t, bloomBackups(t, l.Sketches))
}

// TestReplaceGenerational_KeepsOneGeneration is the §3.3 rule: the previous file survives as
// tried.bloom.<seq>.bak and exactly one generation is kept.
//
// The backup is identified by Count() rather than by a membership probe, because Test answers with
// a false-positive probability and a 99 %-reliable assertion is not one.
func TestReplaceGenerational_KeepsOneGeneration(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	for seq := 1; seq <= 3; seq++ {
		_, err := sketch.ReplaceGenerational(target, filledBloom(seq), seq)
		require.NoError(t, err, "seq %d", seq)
	}

	entries, err := os.ReadDir(l.Sketches)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"tried.bloom", "tried.bloom.3.bak"}, names,
		"exactly one generation is kept, and the seq-2 backup was pruned when seq 3 landed")

	current := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(target, current))
	require.Equal(t, 3, current.Count())

	previous := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(filepath.Join(l.Sketches, "tried.bloom.3.bak"), previous))
	require.Equal(t, 2, previous.Count(), "the surviving .bak must hold the generation seq 3 displaced")
}

// TestReplaceGenerational_BackupDecodes pins the backup FILENAME, which is the one place this
// package's behaviour diverges from the plan's sample code.
//
// internal/paths owns the tried.bloom.<seq>.bak family — it writes the name with %d and
// pruneBloomBackups parses it back with strconv.Atoi — so the name is "tried.bloom.7.bak", not the
// zero-padded "tried.bloom.0007.bak" the plan wrote. One writer owns the format; a second spelling
// would produce backups the pruner cannot see and the one-generation rule would quietly stop
// holding.
func TestReplaceGenerational_BackupDecodes(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(6), 1)
	require.NoError(t, err)

	backup, err := sketch.ReplaceGenerational(target, filledBloom(9), 7)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(l.Sketches, "tried.bloom.7.bak"), backup)

	restored := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(backup, restored))
	require.Equal(t, 6, restored.Count())
}

// TestReplaceGenerational_RollsBackOnWriteFailure asserts the store is never left without a bloom.
//
// paths.ReplaceBloom renames the current file to its backup BEFORE it stages the new content, so a
// staging failure would otherwise leave sketches/ with no tried.bloom at all — the one state §12.3's
// "rebuild from records" recovery cannot distinguish from a first run.
//
// The failure is provoked by replacing the .qompack/tmp DIRECTORY with a regular file, so
// os.MkdirAll fails identically on Windows and POSIX. chmod 0500 is deliberately not used: it is a
// no-op for an administrator on Windows and would turn this test into a tautology there.
func TestReplaceGenerational_RollsBackOnWriteFailure(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(l.Tmp))
	require.NoError(t, os.WriteFile(l.Tmp, nil, 0o600))

	backup, err := sketch.ReplaceGenerational(target, filledBloom(2), 2)
	require.Error(t, err, "the staging write must actually fail; a passing write here is a tautological test")
	require.Empty(t, backup)

	// Proving WHERE it failed matters as much as that it failed: an error from anywhere else would
	// mean the rollback below was never exercised on the path it exists for.
	var pathErr *fs.PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "mkdir", pathErr.Op)
	require.Equal(t, paths.Long(l.Tmp), pathErr.Path)
	t.Logf("staging write failed with: %v", err)

	restored := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(target, restored), "the seq-1 filter must be back in place")
	require.Equal(t, 1, restored.Count())
	require.Empty(t, bloomBackups(t, l.Sketches), "a rolled-back replacement leaves no backup behind")
}

// TestReplaceGenerational_NonMonotonicSeqIsRefused pins the seq precondition as a REFUSAL: nothing
// on disk moves at all.
//
// paths.pruneBloomBackups keeps the backup with the highest sequence, not the newest one, so a call
// whose seq is below a surviving backup's would have its own backup deleted the instant it was
// written. Sequence 1, then 9, then 3 is the smallest arrangement that produces it: after seq 9
// there is a 9.bak to survive, and seq 3's backup would lose the comparison.
//
// The earlier form of this test asserted that the replacement landed and only the backup was
// pruned. That expectation moved deliberately, and it is the point of the fix: paths.ReplaceBloom
// renames before it stages, so the same arrangement plus a staging failure destroys tried.bloom
// outright — see TestReplaceGenerational_RefusalProtectsAgainstAStagingFailure. Detecting the
// violation after the fact reported a data-loss path accurately; refusing the call removes it.
func TestReplaceGenerational_NonMonotonicSeqIsRefused(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.NoError(t, err)
	backup, err := sketch.ReplaceGenerational(target, filledBloom(2), 9)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(l.Sketches, "tried.bloom.9.bak"), backup)

	for _, seq := range []int{3, 9} {
		t.Run(fmt.Sprintf("seq %d against a surviving 9.bak", seq), func(t *testing.T) {
			got, err := sketch.ReplaceGenerational(target, filledBloom(3), seq)
			require.ErrorIs(t, err, sketch.ErrMalformed)
			require.Empty(t, got, "a refused call returns no backup path")
			require.ErrorContains(t, err, fmt.Sprintf("seq %d", seq),
				"the failure must name the sequence that broke the rule")
			require.ErrorContains(t, err, "tried.bloom.9.bak",
				"and the surviving backup it did not exceed")
			t.Logf("refused: %v", err)

			// Nothing moved. The store still holds the seq-9 generation and its backup.
			current := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
			require.NoError(t, sketch.Load(target, current))
			require.Equal(t, 2, current.Count(), "the refused content must not have been written")
			require.Equal(t, []string{filepath.Join(l.Sketches, "tried.bloom.9.bak")},
				bloomBackups(t, l.Sketches), "and no backup generation may have been displaced")
		})
	}

	// Equality is refused and one more is accepted, so the boundary is where the doc says it is:
	// seq must STRICTLY exceed the survivor.
	backup, err = sketch.ReplaceGenerational(target, filledBloom(4), 10)
	require.NoError(t, err, "one above the surviving backup is the first legal sequence")
	require.Equal(t, filepath.Join(l.Sketches, "tried.bloom.10.bak"), backup)
}

// TestReplaceGenerational_RefusalProtectsAgainstAStagingFailure is the data-loss path the refusal
// exists to close, asserted as the loss NOT happening.
//
// The arrangement is the one that used to destroy the file: a surviving 9.bak, a call at seq 3 whose
// own backup the pruner would delete on sight, and a staging write that then fails. Under the old
// code paths.ReplaceBloom had already renamed tried.bloom to tried.bloom.3.bak and the prune had
// already deleted it, so the rollback found nothing to rename back and sketches/tried.bloom — the
// append-only negative-knowledge file of §7.4 — no longer existed. The error said so at length,
// which is not the same as preventing it.
//
// Now the call never reaches paths.ReplaceBloom, so the broken staging directory is never touched
// and the filter is exactly where it was.
func TestReplaceGenerational_RefusalProtectsAgainstAStagingFailure(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.NoError(t, err)
	_, err = sketch.ReplaceGenerational(target, filledBloom(2), 9)
	require.NoError(t, err)

	// Break staging the same way TestReplaceGenerational_RollsBackOnWriteFailure does: replace the
	// .qompack/tmp DIRECTORY with a regular file, so os.MkdirAll fails identically on Windows and
	// POSIX.
	require.NoError(t, os.RemoveAll(l.Tmp))
	require.NoError(t, os.WriteFile(l.Tmp, nil, 0o600))

	backup, err := sketch.ReplaceGenerational(target, filledBloom(3), 3)
	require.ErrorIs(t, err, sketch.ErrMalformed,
		"the sequence is refused before the staging write is even attempted")
	require.Empty(t, backup)

	// The failure is the SEQUENCE, not the staging directory: staging was never reached.
	var pathErr *fs.PathError
	require.NotErrorAs(t, err, &pathErr,
		"a filesystem error here would mean the refusal fired too late to prevent anything")

	require.FileExists(t, target,
		"the whole point: sketches/tried.bloom survives, where it used to be deleted outright")
	survivor := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(target, survivor))
	require.Equal(t, 2, survivor.Count(), "and it still holds the seq-9 generation")
	require.Equal(t, []string{filepath.Join(l.Sketches, "tried.bloom.9.bak")},
		bloomBackups(t, l.Sketches), "with its one backup generation untouched")
}

// TestReplaceGenerational_RejectsForeignBaseName asserts the guard that keeps this function from
// becoming a general-purpose writer: it is the §3.3 door for one filename, and nothing else.
func TestReplaceGenerational_RejectsForeignBaseName(t *testing.T) {
	p := testutil.NewProject(t)
	target := filepath.Join(sketchesOf(p), "segment-12.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.ErrorIs(t, err, sketch.ErrMalformed)
	require.NoFileExists(t, target)
}

// TestReplaceGenerational_RejectsPathOutsideALayout asserts the other half of the same guard. The
// layout is reconstructed from the path, so a caller that hands over a tried.bloom outside any
// .qompack tree must get a clear failure rather than a silent write into the wrong directory.
func TestReplaceGenerational_RejectsPathOutsideALayout(t *testing.T) {
	target := filepath.Join(t.TempDir(), "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.ErrorIs(t, err, sketch.ErrMalformed)
	require.ErrorContains(t, err, ".qompack", "the message must say what shape the path had to have")
	require.NoFileExists(t, target)
}

// TestReplaceGenerational_MarshalFailurePropagates asserts an unencodable sketch is refused before
// anything on disk moves — in particular before the current tried.bloom is renamed away.
func TestReplaceGenerational_MarshalFailurePropagates(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(4), 1)
	require.NoError(t, err)

	backup, err := sketch.ReplaceGenerational(target, (*sketch.Bloom)(nil), 2)
	require.ErrorIs(t, err, sketch.ErrMalformed)
	require.Empty(t, backup)

	survivor := sketch.NewBloom(testBloomCapacity, testBloomFPRate)
	require.NoError(t, sketch.Load(target, survivor), "the live filter must be untouched")
	require.Equal(t, 4, survivor.Count())
	require.Empty(t, bloomBackups(t, l.Sketches))
}

// TestQuarantine asserts the §12.3 quarantine move: the corrupt file leaves its own name free and
// is still on disk for an operator to look at.
//
// Only the SHAPE of the new name is asserted. Quarantine is the one place in this package that
// reads a clock, and nothing it produces is compared against a fixed value anywhere, which is
// exactly why threading a core.Clock through a two-line rename would buy nothing.
func TestQuarantine(t *testing.T) {
	target := filepath.Join(t.TempDir(), "touch.cms")
	require.NoError(t, os.WriteFile(target, []byte("not a QPKS frame"), 0o600))

	moved, err := sketch.Quarantine(target)
	require.NoError(t, err)
	require.NoFileExists(t, target, "the corrupt name must be free for a rebuild to take")
	require.FileExists(t, moved)
	require.Regexp(t, "^"+regexp.QuoteMeta(target)+`\.corrupt\.\d+$`, moved)
}

// TestQuarantine_MissingFileErrors asserts the rename failure is reported rather than swallowed: a
// caller that believes it quarantined a file it did not would go on to overwrite the evidence.
func TestQuarantine_MissingFileErrors(t *testing.T) {
	moved, err := sketch.Quarantine(filepath.Join(t.TempDir(), "absent.cms"))
	require.Error(t, err)
	require.Empty(t, moved)
}

// TestAppendOnly_TriedBloomNeverTruncated runs testutil's §3.3 conformance list against a project
// that has already been through a ReplaceGenerational cycle, so the invariant is asserted about a
// tried.bloom this package wrote rather than about an empty directory.
func TestAppendOnly_TriedBloomNeverTruncated(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	target := filepath.Join(l.Sketches, "tried.bloom")

	_, err := sketch.ReplaceGenerational(target, filledBloom(1), 1)
	require.NoError(t, err)
	backup, err := sketch.ReplaceGenerational(target, filledBloom(2), 2)
	require.NoError(t, err)
	require.NotEmpty(t, backup, "the second replacement must have produced a generation")

	p.AssertAppendOnly(t)
}
