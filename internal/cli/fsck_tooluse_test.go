package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// These rows pin index.tool_use against the file the store ACTUALLY writes. index/tool_use.jsonl
// is the store's compact line (tuRec: "s", "by", "st", ...) plus its append-only supersede mutation
// ({"op":"supersede","id","by"}), not ToolUseRecord's long-key contract shape. A reader decoding the
// long keys sees every session as "" and every supersession link as absent, so the per-session
// monotonicity check silently becomes a cross-session one and the link check never runs. Every
// record below is written through the store's own writers, never hand-built, so the rows prove
// agreement with the real on-disk shape rather than with a fixture of it.

// fsckRecordToolUses appends recs to the seeded project's index through the store's own writer and
// applies each supersession mark (older -> by) after them.
func fsckRecordToolUses(t *testing.T, root string, recs []store.ToolUseRecord, marks [][2]core.ToolUseID) {
	t.Helper()

	ctx := context.Background()
	s, err := store.Open(root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	res, err := s.PutBytes(ctx, []byte("package lib\n\nfunc Lib() {}\n"),
		store.PutOptions{Tool: "Read", Path: "src/lib.go"})
	require.NoError(t, err)
	for _, rec := range recs {
		rec.Root, rec.Bytes = res.Root.Hash, res.Root.RawBytes
		require.NoError(t, s.RecordToolUse(ctx, rec))
	}
	for _, m := range marks {
		require.NoError(t, s.MarkSuperseded(ctx, m[0], m[1]))
	}
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())
}

func fsckToolUseRecord(id, session string, turn core.TurnIndex) store.ToolUseRecord {
	return store.ToolUseRecord{
		ID: core.ToolUseID(id), Session: core.SessionID(session), Turn: turn,
		TS: 2, Tool: "Read", Path: "src/lib.go",
	}
}

// TestFsck_ToolUseTurnsAreMonotonePerSessionNotAcrossSessions: two sessions whose turns interleave
// in file order are each monotone, so the index is clean. Read with the long keys, every line is
// session "" and session b's turn 0 after session a's turn 3 is reported as a defect.
func TestFsck_ToolUseTurnsAreMonotonePerSessionNotAcrossSessions(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckToolUseRecord("tu-b-0", "s-b", 0),
		fsckToolUseRecord("tu-a-4", "s-a", 4),
		fsckToolUseRecord("tu-b-1", "s-b", 1),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, true, row["ok"], "interleaved sessions are each monotone: %s", fsckDetail(row))
	require.Equal(t, ExitOK, code, "doc=%v", doc)
}

// TestFsck_ToolUseNonMonotoneTurnNamesItsSession: a real regression inside ONE session is still a
// defect, and the defect names the session it happened in rather than an empty string.
func TestFsck_ToolUseNonMonotoneTurnNamesItsSession(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckToolUseRecord("tu-a-1", "s-a", 1),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "tool_use tu-a-1 reports turn 1 after turn 3 in session s-a;")
}

// TestFsck_ToolUseSupersedeMarkIsNotATurnRegression: the store records supersession as an appended
// mutation line that carries no session and no turn. It is a link between two records, not a record
// at turn 0, so a session that has moved past turn 0 is still monotone after one lands.
func TestFsck_ToolUseSupersedeMarkIsNotATurnRegression(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-2", "s-a", 2),
		fsckToolUseRecord("tu-a-3", "s-a", 3),
	}, [][2]core.ToolUseID{{"tu-a-2", "tu-a-3"}})

	raw, err := os.ReadFile(paths.Long(filepath.Join(p.Layot.Index, "tool_use.jsonl")))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"op":"supersede"`, "the fixture must carry a real mutation line")

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, true, row["ok"], "a supersede mark is not a turn-0 record: %s", fsckDetail(row))
	require.Equal(t, ExitOK, code, "doc=%v", doc)
}

// TestFsck_ToolUseSupersedeMarkMustNameRecordsThisIndexCarries: the store refuses a mark whose
// either end is unknown (MarkSuperseded returns ErrNotFound), so a mark in the file naming a record
// the file does not carry is damage. Read with the long keys the link is invisible and the damage
// passes clean.
func TestFsck_ToolUseSupersedeMarkMustNameRecordsThisIndexCarries(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{fsckToolUseRecord("tu-a-2", "s-a", 2)}, nil)

	path := filepath.Join(p.Layot.Index, "tool_use.jsonl")
	f, err := os.OpenFile(paths.Long(path), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"v":1,"op":"supersede","id":"tu-a-2","by":"tu-ghost","ts":3}` + "\n" +
		`{"v":1,"op":"supersede","id":"tu-lost","by":"tu-a-2","ts":4}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, false, row["ok"])
	detail := fsckDetail(row)
	require.Contains(t, detail, "tool_use tu-a-2 is superseded by tu-ghost, which this index does not record")
	require.Contains(t, detail, "a supersede mark names tu-lost, which this index does not record")
	require.NotContains(t, detail, "reports turn", "a mark is not a record and has no turn to regress")
}

// fsckSelfRecord is a retrieval self-record as the MCP server files it: a synthetic id, the
// server's own tool name, born ephemeral.
func fsckSelfRecord(id, session string, turn core.TurnIndex) store.ToolUseRecord {
	return store.ToolUseRecord{
		ID: core.ToolUseID(mcp.SelfRecordIDPrefix + id), Session: core.SessionID(session), Turn: turn,
		TS: 2, Tool: "mcp__qompack__recall", Ephemeral: true,
	}
}

// TestFsck_ToolUseUnattributedSelfRecordIsNotARegression is the compatibility half of F-UAT01-2:
// a store written by a build that filed every retrieval self-record at turn 0 must not fail fsck
// for ever. The record is named in a note, and the session's order is judged without it.
func TestFsck_ToolUseUnattributedSelfRecordIsNotARegression(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckSelfRecord("93dd88341192", "s-a", 0),
		fsckToolUseRecord("tu-a-3b", "s-a", 3),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, true, row["ok"], "an unattributed self-record is not a turn regression: %s", fsckDetail(row))
	require.Contains(t, fsckDetail(row), "tool_use qompack-mcp:93dd88341192 is a retrieval self-record filed with no turn")
	require.Equal(t, ExitOK, code, "doc=%v", doc)
}

// TestFsck_ToolUseRegressionAfterASelfRecordIsStillADefect: leaving the unattributed record out of
// the order must not reset the baseline — a real regression behind it is still caught.
func TestFsck_ToolUseRegressionAfterASelfRecordIsStillADefect(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckSelfRecord("93dd88341192", "s-a", 0),
		fsckToolUseRecord("tu-a-1", "s-a", 1),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "tool_use tu-a-1 reports turn 1 after turn 3 in session s-a;")
}

// TestFsck_ToolUseAttributedSelfRecordIsOrderedLikeAnyOther: a self-record that DOES carry a turn is
// held to the session's order like every other record; only the pre-wave-13 producer's signature —
// turn 0, or a turn one of its session's segments opened at — is exempt, and this store has no
// segment log.
func TestFsck_ToolUseAttributedSelfRecordIsOrderedLikeAnyOther(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckSelfRecord("5e1f00000001", "s-a", 2),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Contains(t, fsckDetail(row), "tool_use qompack-mcp:5e1f00000001 reports turn 2 after turn 3 in session s-a;")
}

// fsckSegmentSpan is one segment of a session as the observer opens it, and, when closed is set,
// closes it at end.
type fsckSegmentSpan struct {
	session    string
	start, end core.TurnIndex
	closed     bool
}

// fsckRecordSegments writes spans to the seeded project's segment log through the store's own
// writer, in order.
func fsckRecordSegments(t *testing.T, root string, spans []fsckSegmentSpan) {
	t.Helper()

	ctx := context.Background()
	s, err := store.Open(root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	for _, sp := range spans {
		id, err := s.Segments().Open(ctx, store.Segment{Session: core.SessionID(sp.session), StartTurn: sp.start})
		require.NoError(t, err)
		if sp.closed {
			require.NoError(t, s.Segments().Close(ctx, id, sp.end, map[string]float64{"tokens": 1}))
		}
	}
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())
}

// fsckUAT11Shape is the V6 live lane's UAT-11 diag-rerun store in miniature
// (plans/sdd/V6-closeout/live/uat/UAT-11/diag-rerun/store): the session's first segment spans turns
// 0-9, a SubagentStop at turn 10 opens its second, and a pre-wave-13 build filed every retrieval's
// own record at that open segment's first turn — turn 10, interleaved after hook records of turn 12
// — and, once the segment had closed, at turn 0.
func fsckUAT11Shape(tail ...store.ToolUseRecord) []store.ToolUseRecord {
	recs := []store.ToolUseRecord{
		fsckToolUseRecord("tu-u11-9", "s-u11", 9),
		fsckToolUseRecord("tu-u11-10", "s-u11", 10),
		fsckToolUseRecord("tu-u11-12a", "s-u11", 12),
		fsckSelfRecord("908f1acbdbd2", "s-u11", 10),
		fsckToolUseRecord("tu-u11-12b", "s-u11", 12),
		fsckSelfRecord("8872503177bb", "s-u11", 10),
		fsckToolUseRecord("tu-u11-12c", "s-u11", 12),
		fsckSelfRecord("762640538da0", "s-u11", 0),
	}
	return append(recs, tail...)
}

// fsckUAT11Segments is fsckUAT11Shape's segment log: 0-9 closed, 10-13 closed.
var fsckUAT11Segments = []fsckSegmentSpan{
	{session: "s-u11", start: 0, end: 9, closed: true},
	{session: "s-u11", start: 10, end: 13, closed: true},
}

// TestFsck_ToolUsePreFixSelfRecordAtItsSegmentStartIsNotARegression is the compatibility half of
// F-UAT01-2 for a session whose segment had rolled: the pre-wave-13 producer (daemon resolveTurn)
// filed a retrieval's record at its open segment's StartTurn, which is non-zero after any
// changepoint, SubagentStop, resume or scheduler roll. Such a store must not fail fsck for ever.
func TestFsck_ToolUsePreFixSelfRecordAtItsSegmentStartIsNotARegression(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordSegments(t, p.Root, fsckUAT11Segments)
	fsckRecordToolUses(t, p.Root, fsckUAT11Shape(), nil)

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.tool_use")
	detail := fsckDetail(row)
	require.Equal(t, true, row["ok"], "a self-record at its segment's first turn is not a regression: %s", detail)
	require.Contains(t, detail, "tool_use qompack-mcp:908f1acbdbd2 is a retrieval self-record filed at turn 10, "+
		"the first turn of one of session s-u11's segments, after turn 12")
	require.Contains(t, detail, "tool_use qompack-mcp:762640538da0 is a retrieval self-record filed with no turn")
	require.Equal(t, ExitOK, code, "doc=%v", doc)
}

// TestFsck_ToolUseRegressionBesideSegmentStartSelfRecordsIsStillADefect: the exemption is the old
// producer's exact signature and nothing wider. A hook record behind the baseline is a defect, and
// so is a self-record at a turn no segment of its session opened at; neither is excused by the
// segment-start self-records around it, which do not move the baseline either.
func TestFsck_ToolUseRegressionBesideSegmentStartSelfRecordsIsStillADefect(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordSegments(t, p.Root, fsckUAT11Segments)
	fsckRecordToolUses(t, p.Root, fsckUAT11Shape(
		fsckSelfRecord("5e1f0000000b", "s-u11", 11),
		fsckToolUseRecord("tu-u11-12d", "s-u11", 12),
		fsckToolUseRecord("tu-u11-back", "s-u11", 10),
	), nil)

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, false, row["ok"])
	detail := fsckDetail(row)
	require.Contains(t, detail, "tool_use tu-u11-back reports turn 10 after turn 12 in session s-u11;")
	require.Contains(t, detail, "tool_use qompack-mcp:5e1f0000000b reports turn 11 after turn 12 in session s-u11;")
	require.NotContains(t, detail, "tool_use qompack-mcp:908f1acbdbd2 reports",
		"the segment-start self-records stay notes beside a real regression")
}

// TestFsck_ToolUseSegmentStartOfAnotherSessionDoesNotExcuseASelfRecord: the segment start that
// excuses a self-record must be one of ITS session's; another session opening a segment at that
// turn says nothing about where this session's old producer could have filed it.
func TestFsck_ToolUseSegmentStartOfAnotherSessionDoesNotExcuseASelfRecord(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordSegments(t, p.Root, []fsckSegmentSpan{
		{session: "s-a", start: 0},
		{session: "s-b", start: 2},
	})
	fsckRecordToolUses(t, p.Root, []store.ToolUseRecord{
		fsckToolUseRecord("tu-a-3", "s-a", 3),
		fsckSelfRecord("5e1f0000000c", "s-a", 2),
	}, nil)

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Contains(t, fsckDetail(row), "tool_use qompack-mcp:5e1f0000000c reports turn 2 after turn 3 in session s-a;")
}

// TestFsck_ToolUseSelfRecordBesideAnUnreadableSegmentLogIsReportedNotCounted: with the segment log
// unreadable, the start turns that would confirm the old producer's signature cannot be read. The
// self-record is reported rather than counted, a hook record behind the baseline is still a
// defect, and the unreadable log is index.segments' own defect, so the store still does not pass.
func TestFsck_ToolUseSelfRecordBesideAnUnreadableSegmentLogIsReportedNotCounted(t *testing.T) {
	p := seedFsckProject(t)
	fsckRecordToolUses(t, p.Root, fsckUAT11Shape(fsckToolUseRecord("tu-u11-back", "s-u11", 10)), nil)
	segLog := paths.Long(filepath.Join(p.Layot.Index, "segments.jsonl"))
	require.NoError(t, os.RemoveAll(segLog))
	require.NoError(t, os.Mkdir(segLog, 0o700), "a directory where the log belongs cannot be read as one")

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	require.Equal(t, false, fsckRequireRow(t, doc, "index.segments")["ok"])
	detail := fsckDetail(fsckRequireRow(t, doc, "index.tool_use"))
	require.Contains(t, detail, "tool_use qompack-mcp:908f1acbdbd2 is a retrieval self-record filed at turn 10, "+
		"which the unreadable segment log cannot place, after turn 12")
	require.Contains(t, detail, "tool_use tu-u11-back reports turn 10 after turn 12 in session s-u11;")
}
