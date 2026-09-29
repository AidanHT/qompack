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
// held to the session's order like every other record; only the unattributed turn-0 shape is exempt.
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
