package negknow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// TestReplayLogSized_MatchesUnsized pins that the capacity hint loadRecords passes decides nothing
// but capacity: for every log below and every hint — none, one that undershoots, the hint Open
// computes, the exact line count, and a wild overshoot — the sized replay returns the same records
// (nil and empty told apart), the same index, the same line count, the same error and the same
// counters as the unsized replay, and never keeps more spare capacity than a quarter of its length.
func TestReplayLogSized_MatchesUnsized(t *testing.T) {
	var mixed bytes.Buffer
	mixed.Write(benchLogBytes(t, 40))
	mixed.Write(readContractFile(t, "corrupt.jsonl"))
	mixed.WriteString(`{"op":"stale","id":"elim_b4e7a0d3c6f9","ts":9,"because":["x"]}` + "\n")
	mixed.WriteString(`{"op":"stale","id":"elim_missing","ts":9}` + "\n")
	for range 200 {
		mixed.WriteString(`{"op":"future"}` + "\n") // many non-record lines: the hint overshoots
	}

	logs := map[string][]byte{
		"golden":        readContractFile(t, "eliminations.golden.jsonl"),
		"corrupt":       readContractFile(t, "corrupt.jsonl"),
		"bench":         benchLogBytes(t, 300),
		"mixed":         mixed.Bytes(),
		"empty":         nil,
		"control only":  []byte(`{"op":"stale","id":"x","ts":1}` + "\n"),
		"torn tail":     benchLogBytes(t, 3)[:len(benchLogBytes(t, 3))-9],
		"no final line": bytes.TrimSuffix(benchLogBytes(t, 3), []byte("\n")),
		"blank lines":   append(benchLogBytes(t, 2), bytes.Repeat([]byte{'\n'}, 5000)...),
	}
	counters := []string{counterCorruptLines, counterDuplicateAdd, counterUnknownOp, counterOrphanStale}

	for name, b := range logs {
		exact := bytes.Count(b, []byte{'\n'}) + 1
		for _, hint := range []int{0, 1, replayCapacityHint(b), exact, 10 * exact} {
			m1, m2 := newMetrics(), newMetrics()
			wantRecs, wantByID, wantLines, wantErr := replayLog(bytes.NewReader(b), logging.Nop(), m1)
			gotRecs, gotByID, gotLines, gotErr := replayLogSized(bytes.NewReader(b), hint, logging.Nop(), m2)

			require.Equal(t, wantErr, gotErr, "%s, hint %d", name, hint)
			require.Equal(t, wantLines, gotLines, "%s, hint %d", name, hint)
			require.Equal(t, wantRecs, gotRecs, "%s, hint %d", name, hint)
			require.Equal(t, wantRecs == nil, gotRecs == nil, "%s, hint %d: nil-ness", name, hint)
			require.Equal(t, wantByID, gotByID, "%s, hint %d", name, hint)
			for _, c := range counters {
				require.Equal(t, counterValue(t, m1, c), counterValue(t, m2, c), "%s, hint %d: %s", name, hint, c)
			}
			require.LessOrEqual(t, cap(gotRecs)-len(gotRecs), len(gotRecs)/slackDivisor,
				"%s, hint %d: spare capacity", name, hint)
		}
	}
}

// TestReplayCapacityHint_NeverUndercutsCanonicalRecords pins the two sides of the hint Open passes.
// It is bounded by the bytes as well as the lines, so a file of lines that materialize nothing
// reserves room in proportion to its size and not to its newline count. And for every log this
// package writes it still covers every record, because minRecordLineBytes is below the shortest
// line the writer can produce: the zero Record, every string empty, still renders every key and
// two "sha256:"-prefixed digests.
func TestReplayCapacityHint_NeverUndercutsCanonicalRecords(t *testing.T) {
	zero, err := json.Marshal(Record{})
	require.NoError(t, err)
	require.Greater(t, len(zero), minRecordLineBytes,
		"the shortest record line the writer produces must be longer than the floor the hint assumes")

	blank := bytes.Repeat([]byte{'\n'}, 1_000_000)
	require.Equal(t, len(blank)/minRecordLineBytes, replayCapacityHint(blank),
		"a file of newlines is bounded by its bytes, not its line count")
	require.Zero(t, replayCapacityHint(nil))

	var withStale bytes.Buffer
	withStale.Write(benchLogBytes(t, 300))
	for i := range 300 {
		fmt.Fprintf(&withStale, `{"op":"stale","id":"x%d","ts":1}`+"\n", i)
	}
	for name, b := range map[string][]byte{
		"golden":     readContractFile(t, "eliminations.golden.jsonl"),
		"seed":       readContractFile(t, "input/ledger_seed.jsonl"),
		"bench":      benchLogBytes(t, 2000),
		"with stale": withStale.Bytes(),
	} {
		recs, _, _, err := replayLog(bytes.NewReader(b), logging.Nop(), newMetrics())
		require.NoError(t, err, name)
		hint := replayCapacityHint(b)
		require.GreaterOrEqual(t, hint, len(recs), "%s: the hint must cover every record", name)
		require.LessOrEqual(t, hint, bytes.Count(b, []byte{'\n'})+1, name)
	}
}

// openHeapCost opens a ledger over a log holding one real record followed by filler, and reports
// what Open allocated in total and what the open ledger still holds once Open's garbage is gone.
// Each reading is taken after two collections, as TestMemoryFootprint's are, and the ledger is
// kept alive past the second one.
func openHeapCost(t *testing.T, filler []byte) (total, retained int64) {
	t.Helper()
	root, cfg := newProject(t)
	led, err := Open(root, cfg, nil, testDeps("sess-A", newMetrics()))
	require.NoError(t, err)
	_, err = led.Record(context.Background(), Record{
		Target: "src/a.ts:f", Approach: "widen pool timeout", Reason: "r", Evidence: testEvidence("e"),
	})
	require.NoError(t, err)
	require.NoError(t, led.Close())

	f, err := os.OpenFile(logPath(root), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.Write(filler)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var before, opened, after runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&before)
	led2, err := Open(root, cfg, nil, testDeps("sess-A", newMetrics()))
	runtime.ReadMemStats(&opened)
	require.NoError(t, err)
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	require.Equal(t, 1, led2.Health().Records, "the filler must materialize nothing")
	runtime.KeepAlive(led2)
	require.NoError(t, led2.Close())
	return int64(opened.TotalAlloc - before.TotalAlloc), int64(after.HeapAlloc) - int64(before.HeapAlloc)
}

// TestOpen_ReservesInProportionToTheFile is the regression test for the capacity hint's first
// form, which was the log's newline count. Lines that materialize nothing each reserved a Record
// and an index slot, and the index was kept: a megabyte of blank lines cost Open 337 MB and left
// 53 MB resident, and 200 000 stale control lines left 6.7 MB resident. §12.3 wants no untrusted
// file to become an unbounded allocation, and a damaged log must never take the session down.
//
// What Open allocates over blank lines is held to a small multiple of the file (a few MB here,
// against hundreds under the old hint), and what it retains is held near zero for both kinds of
// filler, whose lines leave nothing behind to keep. The retained ceiling is tight enough to see an
// index left sized for the hint rather than for its one entry: that alone keeps ~0.24 MB over the
// blank lines and ~0.9 MB over the control lines, where the whole open ledger keeps ~21 KB.
func TestOpen_ReservesInProportionToTheFile(t *testing.T) {
	const (
		allocPerFileByte = 8       // Open may allocate at most this multiple of the file
		retainedCeiling  = 1 << 17 // bytes an open ledger over filler may keep; the old hint kept 3-53 MB
	)

	blank := bytes.Repeat([]byte{'\n'}, 1_000_000)
	total, retained := openHeapCost(t, blank)
	t.Logf("blank lines: %d filler bytes, Open allocated %d, retained %d", len(blank), total, retained)
	require.LessOrEqual(t, total, int64(allocPerFileByte*len(blank)),
		"Open over %d blank lines allocated %d bytes", len(blank), total)
	require.LessOrEqual(t, retained, int64(retainedCeiling), "Open over blank lines retained %d bytes", retained)

	var stale bytes.Buffer
	for i := range 100_000 {
		fmt.Fprintf(&stale, `{"op":"stale","id":"elim_unknown","ts":%d,"because":["src/a.ts"]}`+"\n", 1000+i)
	}
	_, retained = openHeapCost(t, stale.Bytes())
	t.Logf("control lines: %d filler bytes, retained %d", stale.Len(), retained)
	require.LessOrEqual(t, retained, int64(retainedCeiling), "Open over control lines retained %d bytes", retained)
}
