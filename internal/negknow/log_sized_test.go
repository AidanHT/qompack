package negknow

import (
	"bytes"
	"testing"

	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// TestReplayLogSized_MatchesUnsized pins that the capacity hint loadRecords passes decides nothing
// but capacity: for every log below and every hint — none, one that undershoots, the exact line
// count Open computes, and a wild overshoot — the sized replay returns the same records (nil and
// empty told apart), the same index, the same line count, the same error and the same counters as
// the unsized replay, and never keeps more spare capacity than a quarter of its length.
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
	}
	counters := []string{counterCorruptLines, counterDuplicateAdd, counterUnknownOp, counterOrphanStale}

	for name, b := range logs {
		exact := bytes.Count(b, []byte{'\n'}) + 1
		for _, hint := range []int{0, 1, exact, 10 * exact} {
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
