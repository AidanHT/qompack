package daemon

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestIngest_AWALSegmentsNameIsDurableBeforeItsFirstLineIsAcked pins the directory barrier the WAL
// needs to be the durability boundary on POSIX: the ACK follows a line's Sync, and a Sync does not
// make a new file's name durable, so the spool directory is synced when a segment handle is opened —
// before the first line on it is synced — for a new session's segment and for a rotated one, and
// once per opened handle rather than once per line.
func TestIngest_AWALSegmentsNameIsDurableBeforeItsFirstLineIsAcked(t *testing.T) {
	ing, p, root := newWALIngest(t)
	spool := paths.Of(root).Spool
	ing.syncSpoolDir = func(dir string) error {
		p.add(walOp{kind: "dirsync", what: filepath.Base(dir)})
		require.Equal(t, filepath.Clean(spool), filepath.Clean(dir), "the directory that names the segment")
		return paths.SyncDir(dir)
	}
	const one, two = core.SessionID("dirsync-one"), core.SessionID("dirsync-two")

	for k, sess := range []core.SessionID{one, one, two, one} {
		r := newWALReq(t, sess, k)
		require.NoError(t, ing.Accept(r.req, r.line))
	}
	ing.mu.Lock()
	ing.wals[one].bytes = walRotateBytes - 1 // the next line on `one` rolls over to segment 1
	ing.mu.Unlock()
	r := newWALReq(t, one, 4)
	require.NoError(t, ing.Accept(r.req, r.line))

	// Every directory sync and every segment's FIRST write, in order: each segment's name is made
	// durable by exactly one directory sync, made when that segment was opened and before any line
	// was written (and so synced and ACKed) on it, and no line adds another.
	var got []string
	written := map[string]bool{}
	for _, op := range p.log() {
		switch {
		case op.kind == "dirsync":
			got = append(got, "dirsync")
		case op.kind == "write" && !written[op.seg]:
			written[op.seg] = true
			got = append(got, "first write "+op.seg)
		}
	}
	require.Equal(t, []string{
		"dirsync", "first write " + segName(one, 0),
		"dirsync", "first write " + segName(two, 0),
		"dirsync", "first write " + segName(one, 1),
	}, got)
}
