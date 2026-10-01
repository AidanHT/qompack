//go:build linux

package paths_test

// Write failures on a regular file the writer already holds open. A full disk or an exhausted quota
// is the production cause; here it is RLIMIT_FSIZE, the kernel's own per-process file-size limit,
// which fails write(2) with EFBIG at the limit (the Go runtime ignores the SIGXFSZ that accompanies
// it). It is the one way to make such a write fail that needs neither privilege nor a special mount,
// so it means the same thing for root and for an ordinary user.
//
// The limit is process-wide, so it is held only around the one call under test, and only in a child
// of this test binary that runs that one test and nothing else: no other test writes while it is
// lowered, and the child keeps no test log (see delegatedToFileSizeChild).

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// fileSizeChildEnv names the top-level test a re-executed child of this binary is to run.
const fileSizeChildEnv = "QOMPACK_PATHS_FSIZE_CHILD"

// delegatedToFileSizeChild runs t, a top-level test that lowers RLIMIT_FSIZE, in a child of this test
// binary and reports true once the child has run it and passed; the caller then returns. In that
// child it reports false, and the caller runs its body.
//
// The limit cannot be lowered in the process go test started. A cacheable run (no -count, as the CI
// test and cover jobs run it) hands the binary -test.testlogfile, a regular file the testing package
// appends to through a 4 KiB buffer on every Open and Stat. A flush inside a window fails with EFBIG,
// the error sticks, and at exit the binary reports "can't write testlog.txt" and exits 2 after every
// test passed. The child is given no test log. It is given the parent's -test.gocoverdir, so its
// coverage counters land where the parent merges its own into the profile.
func delegatedToFileSizeChild(t *testing.T) bool {
	t.Helper()
	if os.Getenv(fileSizeChildEnv) == t.Name() {
		return false
	}
	args := []string{"-test.run=^" + regexp.QuoteMeta(t.Name()) + "$", "-test.count=1", "-test.v"}
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+f.Value.String())
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = append(os.Environ(), fileSizeChildEnv+"="+t.Name())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the child running %s failed:\n%s", t.Name(), out)
	// A pattern that matched nothing would also exit 0; the child must have run this test and passed.
	require.Regexp(t, `(?m)^--- PASS: `+regexp.QuoteMeta(t.Name())+` \(`, string(out),
		"the child must run %s and pass it", t.Name())
	return true
}

// withFileSizeLimit runs fn with RLIMIT_FSIZE lowered to limit bytes and restores the previous limit
// before it returns, whatever fn does. It runs only in the child delegatedToFileSizeChild starts.
func withFileSizeLimit(t *testing.T, limit uint64, fn func()) {
	t.Helper()
	require.NotEmpty(t, os.Getenv(fileSizeChildEnv), "RLIMIT_FSIZE is lowered only in a delegated child")
	// A flush of the test log under the lowered limit would fail the whole binary (see
	// delegatedToFileSizeChild), so no test log may be open here.
	tl := flag.Lookup("test.testlogfile")
	require.NotNil(t, tl)
	require.Empty(t, tl.Value.String(), "RLIMIT_FSIZE must not be lowered in a process that keeps a test log")
	var old syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old))
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: old.Max}))
	defer func() {
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old), "restore RLIMIT_FSIZE")
	}()
	fn()
}

// TestWriteAtomic_AFailedWriteLeavesTheTargetAndNoStagingFile: WriteAtomic reports the failed write
// and leaves the target as it was, with no staging file behind it.
func TestWriteAtomic_AFailedWriteLeavesTheTargetAndNoStagingFile(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o600))

	var err error
	withFileSizeLimit(t, 0, func() { err = paths.WriteAtomic(p, []byte("new"), 0o600) })
	require.ErrorIs(t, err, syscall.EFBIG)

	got, rerr := os.ReadFile(p)
	require.NoError(t, rerr)
	require.Equal(t, "old", string(got), "a failed write must not replace the target")
	ents, rerr := os.ReadDir(dir)
	require.NoError(t, rerr)
	require.Len(t, ents, 1, "the staging file must be removed")
}

// TestCreateNew_AFailedWriteIsNotSealed: CreateNew marks a file read-only only once its bytes are
// written; a file whose write failed is reported and never presented as sealed.
func TestCreateNew_AFailedWriteIsNotSealed(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	p := filepath.Join(t.TempDir(), "0001.json")

	var err error
	withFileSizeLimit(t, 0, func() { err = paths.CreateNew(p, []byte(`{"seq":1}`)) })
	require.ErrorIs(t, err, syscall.EFBIG)

	fi, serr := os.Stat(p)
	require.NoError(t, serr)
	require.NotZero(t, fi.Mode().Perm()&0o200, "a file whose bytes did not land must not be marked read-only")
}

// TestRestoreLog_ReportsAFailedWrite: a restored log whose bytes could not be written is an error,
// never a restore reported done.
func TestRestoreLog_ReportsAFailedWrite(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	p := filepath.Join(t.TempDir(), "MANIFEST.jsonl")

	var err error
	withFileSizeLimit(t, 0, func() { err = paths.RestoreLog(p, []byte("{\"seq\":1}\n")) })
	require.ErrorIs(t, err, syscall.EFBIG)
}

// TestReplacePinsView_AFailedWriteKeepsTheViewAndCleansStaging: the pins view is replaced only by a
// fully written staging file; a failed write keeps the old view and removes the staging file.
func TestReplacePinsView_AFailedWriteKeepsTheViewAndCleansStaging(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	l := paths.Of(t.TempDir())
	require.NoError(t, paths.EnsureLayout(l))
	view := filepath.Join(l.Pins, "invariants.json")
	require.NoError(t, paths.ReplacePinsView(l, []byte(`{"v":1}`)))

	var err error
	withFileSizeLimit(t, 0, func() { err = paths.ReplacePinsView(l, []byte(`{"v":2}`)) })
	require.ErrorIs(t, err, syscall.EFBIG)

	got, rerr := os.ReadFile(view)
	require.NoError(t, rerr)
	require.Equal(t, `{"v":1}`, string(got))
	ents, rerr := os.ReadDir(l.Tmp)
	require.NoError(t, rerr)
	require.Empty(t, ents, "the staging file must be removed")
}

// TestAppendLinesDurable_AFailedWriteAppendsNothingAndIsNotANotDurableLine: when the lines cannot be
// written at all the call fails as "nothing appended" — not ErrLineNotDurable, which means the line
// is in the file — and the log is unchanged.
func TestAppendLinesDurable_AFailedWriteAppendsNothingAndIsNotANotDurableLine(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	p := filepath.Join(t.TempDir(), "roots.jsonl")
	require.NoError(t, os.WriteFile(p, []byte("{\"n\":1}\n"), 0o600))

	var err error
	withFileSizeLimit(t, uint64(len("{\"n\":1}\n")), func() {
		err = paths.AppendLinesDurable(p, []byte("{\"n\":2}\n"))
	})
	require.ErrorIs(t, err, syscall.EFBIG)
	require.NotErrorIs(t, err, paths.ErrLineNotDurable)

	got, rerr := os.ReadFile(p)
	require.NoError(t, rerr)
	require.Equal(t, "{\"n\":1}\n", string(got))
}

// TestAppendersStopWhenTheTornTailCannotBeTerminated: a log ending mid-record is terminated before
// the next record is appended (F4-2). When the terminator itself cannot be written, both appenders
// stop there and report it, rather than gluing the new record onto the torn one.
func TestAppendersStopWhenTheTornTailCannotBeTerminated(t *testing.T) {
	if delegatedToFileSizeChild(t) {
		return
	}
	const torn = `{"n":1`
	for name, appendOne := range map[string]func(p string) error{
		"AppendJSONL":        func(p string) error { return paths.AppendJSONL(p, map[string]int{"n": 2}) },
		"AppendLinesDurable": func(p string) error { return paths.AppendLinesDurable(p, []byte("{\"n\":2}\n")) },
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "log.jsonl")
			require.NoError(t, os.WriteFile(p, []byte(torn), 0o600))

			var err error
			withFileSizeLimit(t, uint64(len(torn)), func() { err = appendOne(p) })
			require.ErrorIs(t, err, syscall.EFBIG)

			got, rerr := os.ReadFile(p)
			require.NoError(t, rerr)
			require.Equal(t, torn, string(got), "nothing may be glued onto the torn record")
		})
	}
}

// TestSyncData_ReportsAHandleThatCannotBeSynced: fdatasync on a handle that does not support it (a
// pipe answers EINVAL) is reported as an fdatasync error on that handle, never as success.
func TestSyncData_ReportsAHandleThatCannotBeSynced(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	err = paths.SyncData(w)
	require.ErrorIs(t, err, syscall.EINVAL)
	var pe *os.PathError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, "fdatasync", pe.Op)
}

// TestReadFileShared_ReadsPastAnUnderstatedSize: procfs files stat with size 0 whatever they hold, so
// ReadFileShared must grow its buffer and read to EOF rather than trust the stat. /proc/self/status
// holds well over the initial capacity, and its Pid line is this process's.
func TestReadFileShared_ReadsPastAnUnderstatedSize(t *testing.T) {
	const p = "/proc/self/status"
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Zero(t, fi.Size(), "the fixture relies on procfs understating the size")

	got, err := paths.ReadFileShared(p)
	require.NoError(t, err)
	require.Greater(t, len(got), 512, "the read must continue past the initial capacity")
	require.Contains(t, string(got), "\nPid:\t"+strconv.Itoa(os.Getpid())+"\n")
	require.Equal(t, byte('\n'), got[len(got)-1], "the file must be read to its end")
}
