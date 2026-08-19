package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlush_SessionsLastRecordWinsAndSecondFlushAppendsNothing pins the two halves of
// V2-SP06-21's sessions.jsonl observable that had no test behind them (flagged by both V2-VERIFY
// passes): a Flush with nothing new appends no session record -- sessionChanged gates the append
// -- and an update is a NEW line whose reload wins, never a rewrite of the old one. The
// byte-level prefix assertion is the append-only half; the reopen-then-idle-Flush assertion is
// the last-record-wins half (had reload taken the first record, the counters would differ and
// that Flush would append).
func TestFlush_SessionsLastRecordWinsAndSecondFlushAppendsNothing(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	require.NoError(t, f.s.RecordToolUse(ctx, sampleToolUse("toolu_01AAA", "src/a.ts", 1000)))
	require.NoError(t, f.s.Flush(ctx))
	first := indexLines(t, f.root, sessionsFile)
	require.Len(t, first, 1, "one session, one record")

	require.NoError(t, f.s.Flush(ctx))
	require.Equal(t, first, indexLines(t, f.root, sessionsFile),
		"a Flush with no new activity must not append a sessions record")

	require.NoError(t, f.s.RecordToolUse(ctx, sampleToolUse("toolu_01BBB", "src/b.ts", 2000)))
	require.NoError(t, f.s.Flush(ctx))
	all := indexLines(t, f.root, sessionsFile)
	require.Len(t, all, 2, "an updated session appends one line, never rewrites")
	require.Equal(t, first[0], all[0], "the first record survives byte-for-byte")

	var last sessionRecord
	require.NoError(t, json.Unmarshal(all[1], &last))
	require.EqualValues(t, 2, last.ToolUses, "the appended record carries the updated counters")

	s2 := f.reopen(t)
	require.NoError(t, s2.Flush(ctx))
	require.Equal(t, all, indexLines(t, f.root, sessionsFile),
		"reload takes the last record per session, so an idle Flush after reopen appends nothing")
}
