package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
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

// TestIngest_AnExternalizedPayloadIsDurableBeforeItsLineIsAcked: a request whose tool response the
// hook moved to spool/blob-<pid>-<n>.bin carries only a descriptor, and the job that reads the blob
// runs after the ACK. So the blob's bytes and then the spool directory are synced before the WAL
// line naming the blob is written — and so before the ACK that follows its Sync. An ordinary request
// pays nothing, and a descriptor naming a blob that is not there is accepted as before (readBlob
// reports it later; there is nothing here to make durable).
func TestIngest_AnExternalizedPayloadIsDurableBeforeItsLineIsAcked(t *testing.T) {
	ing, p, root := newWALIngest(t)
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	const blob = "blob-7-1.bin"
	payload := []byte("a tool response too large to send inline")
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, blob)), payload, 0o600))
	ing.syncBlobFile = func(path string) error {
		p.add(walOp{kind: "blobsync", what: filepath.Base(path)})
		return syncSpoolFile(path)
	}
	ing.syncSpoolDir = func(dir string) error {
		p.add(walOp{kind: "dirsync", what: filepath.Base(dir)})
		return paths.SyncDir(dir)
	}
	const sess = core.SessionID("externalized")
	withBlob := func(k int, name string) walReq {
		r := newWALReq(t, sess, k)
		ref, err := json.Marshal(blobRef{Blob: name, Bytes: len(payload), Field: drainBlobToolResponse})
		require.NoError(t, err)
		r.req.Event, r.req.Raw = &hookio.Event{}, ref
		return r
	}

	for _, r := range []walReq{withBlob(0, blob), newWALReq(t, sess, 1), withBlob(2, "blob-7-2.bin")} {
		require.NoError(t, ing.Accept(r.req, r.line))
	}

	var got []string
	for _, op := range p.log() {
		switch op.kind {
		case "blobsync", "dirsync":
			got = append(got, op.kind+" "+op.what)
		case "write":
			got = append(got, "write")
		}
	}
	require.Equal(t, []string{
		"blobsync " + blob, "dirsync spool", // the payload and its name, before its line
		"dirsync spool", "write", // the session's WAL segment opened, then the line written
		"write",                          // an ordinary request: no payload to sync
		"blobsync blob-7-2.bin", "write", // a descriptor whose blob is missing: nothing to sync
	}, got)
}
