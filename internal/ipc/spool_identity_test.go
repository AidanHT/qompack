package ipc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
)

// Spool identity (known issue 19, owner decision D78(c), and D38's residual). Until 0.3.1 a hook's
// client spool was named by its pid alone, client-<pid>.ndjson, and opened for append. On Windows a
// later hook process can reuse an earlier one's pid while that earlier file waits undrained, so the
// later hook appended to it: the two shared one file, one record order and one 64 MiB cap. These
// rows stand two writers in for two hook processes with the same pid (newSpoolFor's pid is the
// seam), the case pid reuse produces, and the in-process case the C1.16 rig produced (D78(b)).

// spoolIdentityPID is the pid both writers of a row stand in for.
const spoolIdentityPID = 4242

// TestSpool_TwoWritersWithOnePIDWriteFilesOfTheirOwn: two writers for the same pid name two
// different files, and each file holds exactly what its own writer appended, in its own order.
func TestSpool_TwoWritersWithOnePIDWriteFilesOfTheirOwn(t *testing.T) {
	dir := t.TempDir()
	a := newSpoolFor(dir, spoolIdentityPID, nil, nil)
	b := newSpoolFor(dir, spoolIdentityPID, nil, nil)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	require.NotEqual(t, a.Path(), b.Path(), "two writers with one pid must not share a file")
	for _, s := range []*spool{a, b} {
		require.Equal(t, dir, filepath.Dir(s.Path()), "a writer's file is in the spool directory it was given")
		require.Regexp(t, `^client-4242-[0-9a-f]{16}\.ndjson$`, filepath.Base(s.Path()),
			"the name carries the pid and the writer's own id")
		require.Equal(t, SpoolFileClient, SpoolFileKindOf(filepath.Base(s.Path())),
			"%s must be classified as a client spool, or no drain replays it", filepath.Base(s.Path()))
	}

	reqA1 := Request{Op: OpObserveTool, Session: "sess-a", TS: 100}
	reqB := Request{Op: OpObserveTool, Session: "sess-b", TS: 200}
	reqA2 := Request{Op: OpObserveTool, Session: "sess-a", TS: 300}
	require.NoError(t, a.Append(reqA1))
	require.NoError(t, b.Append(reqB))
	require.NoError(t, a.Append(reqA2))

	require.Equal(t, encodedLines(t, reqA1, reqA2), readSpool(t, a.Path()), "a's file holds a's records alone")
	require.Equal(t, encodedLines(t, reqB), readSpool(t, b.Path()), "b's file holds b's record alone")
	files, err := SpoolFiles(dir)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{a.Path(), b.Path()}, files, "the drain lists both files")
}

// TestSpool_OneWriterAtItsCapDoesNotDropAnothersAppends is known issue 19: an earlier hook's file
// that reached spoolMaxBytes while it waited to be consumed, and a later hook with the same pid
// whose capture could not reach the daemon. The later hook's capture must be spooled, not dropped:
// the cap bounds one writer's own file.
func TestSpool_OneWriterAtItsCapDoesNotDropAnothersAppends(t *testing.T) {
	dir := t.TempDir()
	earlierReg := obs.New(core.SystemClock())
	earlier := newSpoolFor(dir, spoolIdentityPID, &loudCountingLogger{}, earlierReg)
	t.Cleanup(func() { _ = earlier.Close() })
	require.NoError(t, earlier.Append(Request{Op: OpObserveTool, Session: "sess-earlier", TS: 100}))
	require.NoError(t, earlier.Close(), "the earlier hook process has exited")
	// The earlier file grew to the cap while it waited: a file of that size on disk is what any
	// writer that opens the same name finds.
	require.NoError(t, os.Truncate(earlier.Path(), spoolMaxBytes))

	laterLoud := &loudCountingLogger{}
	laterReg := obs.New(core.SystemClock())
	later := newSpoolFor(dir, spoolIdentityPID, laterLoud, laterReg)
	t.Cleanup(func() { _ = later.Close() })
	req := Request{Op: OpObserveTool, Session: "sess-later", TS: 200}
	require.NoError(t, later.Append(req))

	require.Zero(t, laterReg.Counter(counterL0Dropped).Value(), "the later writer's capture must not be dropped")
	require.Zero(t, laterLoud.count(), "nothing failed, so nothing is Loud")
	require.Equal(t, encodedLines(t, req), readSpool(t, later.Path()), "the later writer's capture is spooled")
	info, err := os.Stat(earlier.Path())
	require.NoError(t, err)
	require.Equal(t, int64(spoolMaxBytes), info.Size(), "the earlier writer's file is left as it was")
}

// encodedLines is reqs as a writer leaves them on disk: each EncodeRequest line, back to back.
func encodedLines(t *testing.T, reqs ...Request) string {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range reqs {
		line, err := EncodeRequest(r)
		require.NoError(t, err)
		buf.Write(line)
	}
	return buf.String()
}

// readSpool returns the spool file at p, failing the test if it cannot be read.
func readSpool(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}
