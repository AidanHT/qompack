package contract_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// historyDegradedGolden is the frozen fixture added by this task: a degraded SessionHistory in its
// on-disk state/history.json shape, located the way golden_test.go locates result_set.json.
const historyDegradedGolden = "../../testdata/golden/contracts/contract/want/history_degraded.json"

// TestHistoryPath_IsStateHistoryJSON pins HistoryPath's location: state/history.json, deliberately
// distinct from state/contract.json (the Monitor's own persisted mode/reason/results) — two
// schemas sharing one path would corrupt each other.
func TestHistoryPath_IsStateHistoryJSON(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".qompack", "state", "history.json")
	require.Equal(t, want, contract.HistoryPath(root))
}

// TestLoadHistory_MissingFileIsFirstRun asserts a project with no history.json yet returns a zero
// SessionHistory at Version 1 rather than an error: a missing file IS the first-run case.
func TestLoadHistory_MissingFileIsFirstRun(t *testing.T) {
	h := contract.LoadHistory(contract.HistoryPath(t.TempDir()))
	require.NotNil(t, h)
	require.Equal(t, 1, h.Version)
	require.Equal(t, 0, h.SessionCount)
}

// TestLoadHistory_CorruptFileFallsBackToZero asserts corrupt JSON falls back exactly like a
// missing file, mirroring the Monitor's own §12.3 "fail toward do nothing" for state/contract.json.
func TestLoadHistory_CorruptFileFallsBackToZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	h := contract.LoadHistory(path)
	require.Equal(t, 1, h.Version)
}

// TestLoadHistory_UnrecognizedVersionFallsBackToZero (Minor M3) asserts a parseable file declaring
// a schema version this build does not recognise is treated exactly like a corrupt one — §12.3's
// "fail toward do nothing" — rather than trusted with a stale interpretation of a future shape.
func TestLoadHistory_UnrecognizedVersionFallsBackToZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":7,"sessions":42}`), 0o600))

	h := contract.LoadHistory(path)
	require.Equal(t, 1, h.Version)
	require.Equal(t, 0, h.SessionCount, "an unrecognised version must fall back to zero, not trust the rest of the file")
}

// TestLoadHistory_ReappliesCapsOnLoad (Minor M3) asserts every bound-checked field is re-clamped
// after unmarshal: a hand-edited or future-schema history.json carrying more than the setters would
// ever allow (100 wall samples, a 300-char instr, 12 Last entries) must not smuggle an unbounded
// field past LoadHistory — including the very p99 window precompact.has_time_to_write reads.
func TestLoadHistory_ReappliesCapsOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	samples := make([]string, 100)
	for i := range samples {
		samples[i] = fmt.Sprintf("%d", i)
	}
	lastEntries := make([]string, 12)
	for i := range lastEntries {
		lastEntries[i] = fmt.Sprintf(`{"id":"x%d","ok":true,"severity":0,"expected":"e","observed":"o","ts":1}`, i)
	}
	longInstr := strings.Repeat("x", 300)

	raw := fmt.Sprintf(`{"version":1,"precompact_wall_ms":[%s],"precompact_instr":%q,"last":[%s]}`,
		strings.Join(samples, ","), longInstr, strings.Join(lastEntries, ","))
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

	h := contract.LoadHistory(path)
	require.Len(t, h.PrecompactWallMs, 64)
	require.Equal(t, int64(99), h.PrecompactWallMs[len(h.PrecompactWallMs)-1], "the newest samples must survive the reclamp")
	require.LessOrEqual(t, len(h.PrecompactInstr), 256)
	require.Len(t, h.Last, 9)
}

// TestSaveHistory_RoundTripsThroughLoadHistory asserts SaveHistory/LoadHistory agree on the wire
// shape for every field this task adds, including the map-backed Seen the History interface reads.
func TestSaveHistory_RoundTripsThroughLoadHistory(t *testing.T) {
	path := contract.HistoryPath(t.TempDir())

	h := &contract.SessionHistory{Version: 1, SessionCount: 3}
	h.Record(contract.CHookPayloadShape, core.UnixMilli(1000))
	h.AddPrecompactWallSample(500)
	h.SetPrecompactInstr("an emitted focus instruction")
	h.RecordSentinelScan(false)

	require.NoError(t, contract.SaveHistory(path, h))

	got := contract.LoadHistory(path)
	require.Equal(t, 3, got.SessionCount)
	require.True(t, got.Saw(contract.CHookPayloadShape))
	ts, ok := got.LastSeen(contract.CHookPayloadShape)
	require.True(t, ok)
	require.Equal(t, core.UnixMilli(1000), ts)
	require.Equal(t, []int64{500}, got.PrecompactWallMs)
	require.Equal(t, "an emitted focus instruction", got.PrecompactInstr)
	require.Equal(t, 1, got.Sentinel.Chances)
}

// TestSaveHistory_CreatesTheStateDirectory asserts SaveHistory creates state/ if it does not exist
// yet, mirroring monitor.persist's own behaviour for state/contract.json.
func TestSaveHistory_CreatesTheStateDirectory(t *testing.T) {
	root := t.TempDir()
	path := contract.HistoryPath(root)
	_, err := os.Stat(filepath.Dir(path))
	require.True(t, os.IsNotExist(err), "fixture sanity: state/ must not already exist")

	require.NoError(t, contract.SaveHistory(path, &contract.SessionHistory{Version: 1}))
	_, err = os.Stat(path)
	require.NoError(t, err)
}

// TestAddPrecompactWallSample_CapsAtTheNewest64 asserts the ring keeps only the 64 newest samples
// (task-4-spec.md: "the 64 newest"), so precompact.has_time_to_write's p99 is always computed over
// a bounded window.
func TestAddPrecompactWallSample_CapsAtTheNewest64(t *testing.T) {
	h := &contract.SessionHistory{}
	for i := int64(0); i < 70; i++ {
		h.AddPrecompactWallSample(i)
	}
	require.Len(t, h.PrecompactWallMs, 64)
	require.Equal(t, int64(6), h.PrecompactWallMs[0], "the OLDEST 6 samples must have been evicted")
	require.Equal(t, int64(69), h.PrecompactWallMs[len(h.PrecompactWallMs)-1], "the newest sample must survive")
}

// TestSetPrecompactInstr_CapsAt256Chars asserts the persisted probe phrase never exceeds
// task-4-spec.md's 256-character cap.
func TestSetPrecompactInstr_CapsAt256Chars(t *testing.T) {
	h := &contract.SessionHistory{}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	h.SetPrecompactInstr(string(long))
	require.Len(t, h.PrecompactInstr, 256)
}

// TestSetPrecompactInstr_CapsByRunesNotBytes (Minor M2) asserts the 256-char cap is a RUNE count,
// not a byte count: a byte-boundary truncation of multi-byte UTF-8 content can split a rune, which
// json.Marshal then replaces with U+FFFD, silently corrupting the SaveHistory/LoadHistory round
// trip. task-4-spec.md says "<=256 chars".
func TestSetPrecompactInstr_CapsByRunesNotBytes(t *testing.T) {
	h := &contract.SessionHistory{}
	// 300 multi-byte runes (3 bytes each in UTF-8): a byte-boundary cut at 256 BYTES would land
	// mid-rune; a rune-boundary cut at 256 RUNES must not.
	long := strings.Repeat("€", 300)
	h.SetPrecompactInstr(long)

	require.Equal(t, 256, utf8.RuneCountInString(h.PrecompactInstr), "the cap is 256 runes, not 256 bytes")
	require.True(t, utf8.ValidString(h.PrecompactInstr), "truncation must never split a multi-byte rune")
	require.NotContains(t, h.PrecompactInstr, "�", "a split rune would round-trip through json as U+FFFD")

	// And the round trip through SaveHistory/LoadHistory must reproduce exactly this string.
	path := contract.HistoryPath(t.TempDir())
	require.NoError(t, contract.SaveHistory(path, h))
	got := contract.LoadHistory(path)
	require.Equal(t, h.PrecompactInstr, got.PrecompactInstr)
}

// TestRecordLast_CapsAt9Results asserts History.Last never exceeds one entry per §5.19 assertion.
func TestRecordLast_CapsAt9Results(t *testing.T) {
	h := &contract.SessionHistory{}
	var results []contract.Result
	for i := 0; i < 12; i++ {
		results = append(results, contract.Result{ID: contract.ID(string(rune('a' + i)))})
	}
	h.RecordLast(results)
	require.Len(t, h.Last, 9)
	require.Equal(t, results[len(results)-9:], h.Last, "the newest 9 must survive")
}

// TestSessionHistory_NilReceiverMethodsAreNoOps (Minor M8) asserts every SessionHistory mutator
// tolerates a nil receiver exactly like Saw/LastSeen/Record/Sessions already did: either every
// method on the type honours nil, or none of them should claim to — this pins that the answer is
// "every method".
func TestSessionHistory_NilReceiverMethodsAreNoOps(t *testing.T) {
	var h *contract.SessionHistory
	require.NotPanics(t, func() {
		h.AddPrecompactWallSample(1)
		h.SetPrecompactInstr("x")
		h.RecordLast(nil)
		h.RecordSentinelScan(true)
		h.Record(contract.CSessionStartFires, 1)
		_ = h.Saw(contract.CSessionStartFires)
		_, _ = h.LastSeen(contract.CSessionStartFires)
		_ = h.Sessions()
	})
}

// TestHistoryDegradedGolden_Decodes asserts the frozen fixture this task adds decodes into
// SessionHistory and round-trips losslessly, exactly like golden_test.go's result_set pattern.
func TestHistoryDegradedGolden_Decodes(t *testing.T) {
	raw, err := os.ReadFile(historyDegradedGolden)
	require.NoError(t, err, "the frozen history_degraded fixture is missing")

	var h contract.SessionHistory
	require.NoError(t, json.Unmarshal(raw, &h))

	require.Equal(t, "degraded-passive", h.Mode)
	require.Equal(t, 2, h.StartsWithoutMarker)
	require.Equal(t, 2, h.Sentinel.Chances)
	require.False(t, h.Sentinel.Observed)
	require.Len(t, h.PrecompactWallMs, 5)
	require.Len(t, h.Last, 1)
	require.Equal(t, contract.CSessionStartFires, h.Last[0].ID)

	round, err := json.MarshalIndent(&h, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(raw), string(round)+"\n",
		"the fixture must round-trip losslessly through SessionHistory (Rule W-2)")
}
