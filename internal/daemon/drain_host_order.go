package daemon

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// hostTSKey is ipc.Request.TS's JSON key, the host timestamp the hook client stamps every request
// with before any transport attempt.
const hostTSKey = "t"

// orderClientSpoolsByHostTS returns files, as ipc.SpoolFiles listed them, with the hooks' client
// spools put in host order (SP08-D3, owner decision D35): by the req.TS of the first record each
// file still has to replay, the file name breaking a tie. Every other file — the ingest's WAL
// segments — keeps its place ahead of them, and a pass still reads each file front to back, so
// record order within a file stands.
//
// "Still has to replay" is the record at the file's consumed offset in st, the progress the pass
// has already validated against the spool (byte 0 for a file st has no entry for). A client spool
// is named by pid alone and opened for append, and a file stays until a drain consumes it, so a
// later hook that reuses the pid appends to an earlier hook's file; the record an earlier pass
// consumed says when that earlier hook ran, not the appended one. Within one pass the same reuse
// can put a later host prompt ahead of another file's earlier one, and file order cannot undo that;
// the observer's host-order notice names what it causes (docs/cannot-do.md).
//
// ipc.SpoolFiles sorts client-<pid>.ndjson by name, and a pid says nothing about time (nor does an
// unpadded decimal sort as a number: "client-10" precedes "client-9"). A client spool line is leased
// when a drain reaches it, so its prompt's turn is its place in the drain. HotSpool and
// runtime.daemon.enabled=false spool every prompt, one file per hook pid, and read in name order
// the host's later prompt could become prompt_<s>_0, the id the rehydrator serves as the verbatim
// original.
//
// A file whose next record has no readable host timestamp — a record its hook is still writing, a
// corrupt line, a zero stamp — sorts after every stamped file, by name. The newest file is the
// likeliest to be mid-write, and a record that cannot say when it was sent cannot claim a place
// ahead of one that can. The drain itself decides what such a line is; this only orders files.
func orderClientSpoolsByHostTS(files []string, st drainState) []string {
	type clientSpool struct {
		path, base string
		ts         core.UnixMilli
		stamped    bool
	}
	out := make([]string, 0, len(files))
	var client []clientSpool
	for _, path := range files {
		base := filepath.Base(path)
		if !isClientSpoolName(base) {
			out = append(out, path)
			continue
		}
		var offset int64
		if fs := st[base]; fs != nil {
			offset = fs.Offset
		}
		ts, ok := nextRecordHostTS(path, offset)
		client = append(client, clientSpool{path: path, base: base, ts: ts, stamped: ok})
	}
	sort.SliceStable(client, func(i, j int) bool {
		a, b := client[i], client[j]
		if a.stamped != b.stamped {
			return a.stamped
		}
		if a.stamped && a.ts != b.ts {
			return a.ts < b.ts
		}
		return a.base < b.base
	})
	for _, c := range client {
		out = append(out, c.path)
	}
	return out
}

// nextRecordHostTS reads the host timestamp of the record that starts at offset in path, a record
// boundary (drainFileState.Offset only ever rests on one), and reports false when there is none to
// read, as at the end of a fully consumed file. It decodes the record's top-level keys only as far as the timestamp, which the
// encoder writes third, so a large record costs a few bytes here and not its whole line; the read is
// bounded by ipc.MaxLineBytes either way, the most one record may measure.
func nextRecordHostTS(path string, offset int64) (core.UnixMilli, bool) {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, false
		}
	}
	dec := json.NewDecoder(io.LimitReader(f, ipc.MaxLineBytes))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, false
		}
		if key, _ := tok.(string); key == hostTSKey {
			var ts core.UnixMilli
			if err := dec.Decode(&ts); err != nil || ts <= 0 {
				return 0, false
			}
			return ts, true
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return 0, false
		}
	}
	return 0, false
}
