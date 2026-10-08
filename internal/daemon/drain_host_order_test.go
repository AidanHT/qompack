package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// SP08-D3, owner decision D35 (1): the drain replays client spools in host order — by the req.TS
// of each file's first record, with the file name only as a tie-break — and keeps record order
// within a file. WAL segments keep their place ahead of every client spool.

// hostOrderRequest is a spoolable request stamped at ts.
func hostOrderRequest(ts core.UnixMilli, nonce rune) ipc.Request {
	req := observeRequest(testDeliveryToken(nonce), "sess-host-order", "")
	req.TS = ts
	return req
}

// TestDrainOrder_ClientSpoolsReplayByFirstRecordHostTS lists a spool directory whose file-name
// order disagrees with its host order in every way a pid can: "client-10" sorts before "client-9",
// and a low pid can belong to a later hook. A client spool whose first record carries no readable
// timestamp (a record still being written, or a corrupt one) sorts after every stamped one, by name.
func TestDrainOrder_ClientSpoolsReplayByFirstRecordHostTS(t *testing.T) {
	root := t.TempDir()
	spool := paths.Of(root).Spool

	writeSpoolLines(t, root, "client-10.ndjson", hostOrderRequest(300, 'a'))
	writeSpoolLines(t, root, "client-9.ndjson", hostOrderRequest(100, 'b'), hostOrderRequest(900, 'c'))
	writeSpoolLines(t, root, "client-2.ndjson", hostOrderRequest(200, 'd'))
	writeSpoolLines(t, root, "client-3.ndjson", hostOrderRequest(200, 'e')) // ties client-2: name decides
	writeSpoolLines(t, root, "client-1.ndjson", hostOrderRequest(0, 'f'))   // no host timestamp
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, "client-0.ndjson")),
		[]byte(`{"op":"observe.prompt","s":"sess-host-order","t`), 0o600)) // a first record mid-write
	writeSpoolLines(t, root, "wal-sess-host-order.ndjson", hostOrderRequest(50, 'g'))

	files, err := ipc.SpoolFiles(spool)
	require.NoError(t, err)
	var got []string
	for _, f := range orderClientSpoolsByHostTS(files, nil) {
		got = append(got, filepath.Base(f))
	}
	require.Equal(t, []string{
		"wal-sess-host-order.ndjson", // WAL segments first, as before
		"client-9.ndjson",            // 100
		"client-2.ndjson",            // 200
		"client-3.ndjson",            // 200, after client-2 by name
		"client-10.ndjson",           // 300
		"client-0.ndjson",            // unreadable first record: after every stamped file, by name
		"client-1.ndjson",            // unstamped first record
	}, got)
}

// TestDrainOrder_PartlyConsumedClientSpoolOrdersByNextRecord: a 0.3.0 hook's client spool is named
// by pid alone and opened for append, so a later 0.3.0 hook that reused the pid appended to a file an
// earlier pass had already partly consumed (any writer appending after a pass does the same). What
// the next pass replays from that file starts at its consumed offset, so that is the record whose
// host timestamp places it: client-9's consumed record says 100, but the first record it still has
// to replay says 900, which is after client-10's 300.
func TestDrainOrder_PartlyConsumedClientSpoolOrdersByNextRecord(t *testing.T) {
	root := t.TempDir()
	spool := paths.Of(root).Spool

	consumed, err := ipc.EncodeRequest(hostOrderRequest(100, 'a'))
	require.NoError(t, err)
	next, err := ipc.EncodeRequest(hostOrderRequest(900, 'b'))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, "client-9.ndjson")),
		append(append([]byte{}, consumed...), next...), 0o600))
	writeSpoolLines(t, root, "client-10.ndjson", hostOrderRequest(300, 'c'))
	writeSpoolLines(t, root, "client-11.ndjson", hostOrderRequest(50, 'd'))

	st := drainState{
		"client-9.ndjson":  {Size: int64(len(consumed)), Offset: int64(len(consumed))},
		"client-11.ndjson": {}, // an entry at offset 0 reads its first record, like no entry
	}
	files, err := ipc.SpoolFiles(spool)
	require.NoError(t, err)
	var got []string
	for _, f := range orderClientSpoolsByHostTS(files, st) {
		got = append(got, filepath.Base(f))
	}
	require.Equal(t, []string{
		"client-11.ndjson", // 50
		"client-10.ndjson", // 300
		"client-9.ndjson",  // 900: its next unconsumed record, not its consumed 100
	}, got)
}
