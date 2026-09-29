package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/store"
)

// The handler halves of the V6 live lane's findings this package owns: the turn a retrieval's own
// record is filed at (F-UAT01-2), timeline's open segments and inverted ranges (retrieval D8), the
// two ledger tools with no ledger (D1), the caller's session on the ledger (D2) and `why` on a
// ledger-minted evidence (D5). internal/cli's TestLive* rows drive the same through the daemon.

// callLive dispatches one call under ctx, which may carry the daemon's live view (WithLive).
func (f *fixture) callLive(t *testing.T, ctx context.Context, turn core.TurnIndex, name string,
	args map[string]any,
) Response {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	resp, err := Dispatch(ctx, f.Server, Request{
		Session: testSession, Name: name, Args: raw, Turn: turn,
		Deadline: f.Clock.Now().Add(time.Minute),
	})
	require.NoError(t, err, "Dispatch(%s)", name)
	return resp
}

// selfRecordTurn files one retrieval under ctx and returns the turn its self-record carries.
func (f *fixture) selfRecordTurn(t *testing.T, ctx context.Context, turn core.TurnIndex, query string) core.TurnIndex {
	t.Helper()
	resp := f.callLive(t, ctx, turn, ToolRecall, map[string]any{"query": query})
	require.False(t, resp.IsError, "recall must answer: %s", responseText(resp))
	id, ok := resp.Meta[metaToolUseID].(string)
	require.True(t, ok, "a retrieval files a self-record; meta=%v", resp.Meta)
	rec, err := f.Store.ToolUse(t.Context(), core.ToolUseID(id))
	require.NoError(t, err)
	return rec.Turn
}

// TestEphemeralRecordReResolvesTheTurnAtWriteTime: the daemon re-resolves a turn it resolved itself
// at the moment the record is written, so hook records the workers publish while the tool runs
// cannot end up ahead of it; the re-resolution only ever raises the turn; an explicit turn (no
// Live.Turn) is used verbatim.
func TestEphemeralRecordReResolvesTheTurnAtWriteTime(t *testing.T) {
	f := newFixture(t)

	raised := WithLive(context.Background(), Live{Turn: func() core.TurnIndex { return 7 }})
	require.Equal(t, core.TurnIndex(7), f.selfRecordTurn(t, raised, 1, "pool timeout one"))

	lower := WithLive(context.Background(), Live{Turn: func() core.TurnIndex { return 0 }})
	require.Equal(t, core.TurnIndex(3), f.selfRecordTurn(t, lower, 3, "pool timeout two"),
		"a re-resolution never lowers the dispatch-time turn")

	require.Equal(t, core.TurnIndex(4), f.selfRecordTurn(t, context.Background(), 4, "pool timeout three"),
		"with no live view the call's own turn is used verbatim")
}

// TestTimelineOpenSegmentReportsLiveProgress: an open segment's end and running tokens come from
// the daemon's live view of its session; a closed segment is copied off the log untouched.
func TestTimelineOpenSegmentReportsLiveProgress(t *testing.T) {
	f := newFixture(t)
	closed := f.seedSegment(t, 0, 9, time.Minute)
	openID, err := f.Store.Segments().Open(t.Context(), store.Segment{
		Session: testSession, StartTurn: 10, StartTS: core.NowMilli(f.Clock),
	})
	require.NoError(t, err)

	var plain timelineBody
	f.callOK(t, ToolTimeline, map[string]any{}, &plain)
	require.Equal(t, core.TurnIndex(10), plain.Segments[1].EndTurn, "control: the log alone holds 10-10")

	progress := func(s core.SessionID) (SessionProgress, bool) {
		if s != testSession {
			return SessionProgress{}, false
		}
		return SessionProgress{Turn: 17, Segment: openID, SegmentTokens: 321}, true
	}
	ctx := WithLive(context.Background(), Live{Progress: progress})
	resp := f.callLive(t, ctx, 1, ToolTimeline, map[string]any{})
	require.False(t, resp.IsError, responseText(resp))
	var body timelineBody
	require.NoError(t, json.Unmarshal([]byte(responseText(resp)), &body))

	require.Len(t, body.Segments, 2)
	require.Equal(t, closed.EndTurn, body.Segments[0].EndTurn, "a closed segment is untouched")
	require.Equal(t, core.Tokens(segFixtureTokens), body.Segments[0].Tokens)
	require.False(t, body.Segments[1].Closed)
	require.Equal(t, core.TurnIndex(17), body.Segments[1].EndTurn, "the open segment ends where the session is")
	require.Equal(t, core.Tokens(321), body.Segments[1].Tokens, "with the running count the observer holds")
	require.Equal(t, core.TurnIndex(17), body.To, "the default upper bound is the session's current turn")
}

// TestTimelineOpenSegmentTokensOnlyFromItsOwnCount: a running count for a DIFFERENT segment (the
// observer's, after the scheduler rolled a new one open) is never borrowed.
func TestTimelineOpenSegmentTokensOnlyFromItsOwnCount(t *testing.T) {
	f := newFixture(t)
	openID, err := f.Store.Segments().Open(t.Context(), store.Segment{
		Session: testSession, StartTurn: 4, StartTS: core.NowMilli(f.Clock),
	})
	require.NoError(t, err)
	ctx := WithLive(context.Background(), Live{Progress: func(core.SessionID) (SessionProgress, bool) {
		return SessionProgress{Turn: 9, Segment: openID + 1, SegmentTokens: 555}, true
	}})
	var body timelineBody
	require.NoError(t, json.Unmarshal([]byte(responseText(f.callLive(t, ctx, 1, ToolTimeline, map[string]any{}))), &body))
	require.Len(t, body.Segments, 1)
	require.Equal(t, core.TurnIndex(9), body.Segments[0].EndTurn)
	require.Zero(t, body.Segments[0].Tokens, "another segment's running count is not this one's")
}

// TestTimelineInvertedRangeIsError: a from after a to, both the caller's own, is refused; a from
// past a DEFAULTED upper bound is an ordinary empty answer.
func TestTimelineInvertedRangeIsError(t *testing.T) {
	f := newFixture(t)
	seedFourSegments(t, f)

	msg := f.callErr(t, ToolTimeline, map[string]any{"from": "30", "to": "12"})
	require.Equal(t, "timeline: from is after to; pass from at or before to", msg)

	var body timelineBody
	f.callOK(t, ToolTimeline, map[string]any{"from": "900"}, &body)
	require.Zero(t, body.Count, "past the session's end with no explicit to is a miss, not an error")
}

// TestLedgerToolsWithNoLedger: already_tried answers its documented unavailable state, degraded;
// record_eliminated is a tool error that records nothing.
func TestLedgerToolsWithNoLedger(t *testing.T) {
	f := newFixture(t, withoutLedger())

	var got AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, map[string]any{"target": "src/a.go", "approach": "inline it"}, &got)
	require.Equal(t, stateUnavailable, got.State)
	require.True(t, got.Degraded)
	require.Equal(t, ledgerUnavailableReason, got.Reason)

	msg := f.callErr(t, ToolRecordEliminated, map[string]any{
		"target": "src/a.go", "approach": "inline it", "reason": "it recurses",
	})
	require.Contains(t, msg, "nothing was recorded")
}

// TestRecordEliminatedFilesTheCallersSession: the record is written for the session the call is
// made for, so the daemon's one ledger can keep sessions apart.
func TestRecordEliminatedFilesTheCallersSession(t *testing.T) {
	f := newFixture(t)
	var ack eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": "src/a.go", "approach": "inline it", "reason": "it recurses",
	}, &ack)
	rec, err := f.Ledger.Get(t.Context(), ack.ID)
	require.NoError(t, err)
	require.Equal(t, testSession, rec.Session)
}

// TestWhyAuthorizesLedgerMintedEvidence: an elimination's evidence is the reason text the ledger
// stored under negknow.EvidenceTool with no path — a known pathless producer, answered in full.
func TestWhyAuthorizesLedgerMintedEvidence(t *testing.T) {
	f := newFixture(t)
	m, ok := f.Ledger.(negknow.Maintainer)
	require.True(t, ok)
	ctx := negknow.WithCaller(t.Context(), negknow.Caller{Session: testSession})
	rec, _, err := m.IngestMCP(ctx, negknow.MCPArgs{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
	})
	require.NoError(t, err)
	require.False(t, rec.Evidence.IsZero())

	cp := loadContractCheckpoint(t)
	cp.Decisions = []checkpoint.Decision{{
		ID: "dec_0000000000ef", What: `rejected "widen pool timeout" for src/pool.go:DialPool`,
		Why: rec.Reason, AlternativesRejected: []string{rec.Approach}, Evidence: rec.Evidence,
	}}
	f.Checks.Chained = []checkpoint.Checkpoint{cp}
	f.Checks.Refs = refsFor(cp.Seq)

	var body whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": "dec_0000000000ef"}, &body)
	require.True(t, body.Found)
	require.Empty(t, body.EvidenceWithheld, "reason text has no file to authorize")
	require.NotNil(t, body.EvidenceBytes)
	require.Equal(t, rec.Evidence.String(), body.Evidence)
}
