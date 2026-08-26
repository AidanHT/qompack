package negknow

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// frozenClock is a Clock that never moves.
//
// internal/testutil.FakeClock is the house fake and the plan's test template names it, but
// testutil imports internal/cli -> internal/daemon -> internal/negknow, so an INTERNAL test in
// this package (which is what a test of replayLog/appendLine/normalizeRecord has to be) cannot
// import testutil without an import cycle. Nothing here needs time to move, so a four-line frozen
// clock is the whole substitute; the external negknow_test package remains free to use testutil.
type frozenClock struct{}

func (frozenClock) Now() time.Time { return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC) }

func (c frozenClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// newMetrics builds a real obs.Registry: replayLog's counters are asserted by value, so the
// registry has to be the real one, not a nop.
func newMetrics() obs.Registry { return obs.New(frozenClock{}) }

func counterValue(t *testing.T, m obs.Registry, name string) int64 {
	t.Helper()
	return m.Counter(name).Value()
}

// replayBytes runs replayLog over b with a real registry, returning everything the caller needs to
// assert on.
func replayBytes(t *testing.T, b []byte) ([]Record, map[string]int, int, obs.Registry) {
	t.Helper()
	m := newMetrics()
	recs, byID, lines, err := replayLog(bytes.NewReader(b), logging.Nop(), m)
	require.NoError(t, err, "a damaged log degrades, it never fails the replay")
	return recs, byID, lines, m
}

// TestReplayLog_Golden materializes the six-line golden: four bare record lines, one stale control
// line that flips the first record, and one unknown-op line that is skipped as forward-compatible.
func TestReplayLog_Golden(t *testing.T) {
	golden := readContractFile(t, "eliminations.golden.jsonl")

	recs, byID, lines, m := replayBytes(t, golden)

	require.Len(t, recs, 4)
	require.Equal(t, 6, lines)
	require.Equal(t, []string{
		"elim_3f9b2c7d1a48", "elim_7c0f3b6e9a2d", "elim_b4e7a0d3c6f9", "elim_2d5c8f1b4e7a",
	}, []string{recs[0].ID, recs[1].ID, recs[2].ID, recs[3].ID}, "records come back in log order")
	for id, i := range byID {
		require.Equal(t, id, recs[i].ID, "the index must point at the record it names")
	}

	stale := recs[byID["elim_3f9b2c7d1a48"]]
	require.Equal(t, StatusStale, stale.Status)
	require.Equal(t, core.UnixMilli(1754985600000), stale.StaleSince)
	require.Len(t, stale.StaleBecause, 1)

	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.unknown_op"))
	require.Zero(t, counterValue(t, m, "negknow.log.corrupt_lines"))
	require.Zero(t, counterValue(t, m, "negknow.log.duplicate_add"))
	require.Zero(t, counterValue(t, m, "negknow.log.orphan_stale"))

	// The other three records keep the scope/depends_on mix the fixture exists to carry.
	require.Equal(t, ScopeProject, recs[1].Scope)
	require.Len(t, recs[1].DependsOn, 2)
	require.Equal(t, ScopeSession, recs[2].Scope)
	require.Empty(t, recs[2].DependsOn)
	require.Equal(t, ScopeSession, recs[3].Scope)

	// Self-check: every record line in the golden is exactly what MarshalJSON emits for it, so a
	// change to the wire form breaks this golden and the frozen fixture together.
	for i, line := range bytes.Split(bytes.TrimSuffix(golden, []byte("\n")), []byte("\n")) {
		var probe logProbe
		require.NoError(t, json.Unmarshal(line, &probe))
		if probe.Op != "" {
			continue
		}
		var rec Record
		require.NoError(t, json.Unmarshal(line, &rec))
		got, err := rec.MarshalJSON()
		require.NoError(t, err)
		require.Equal(t, string(line), string(got), "golden line %d is not the marshaller's own output", i+1)
	}

	// Row 1 is the frozen fixture's single line, copied verbatim.
	frozen := readContractFile(t, "want/elimination_record.jsonl")
	require.True(t, bytes.HasPrefix(golden, frozen), "golden row 1 must be want/elimination_record.jsonl verbatim")
}

// TestReplayLog_Corrupt pins the degrade-never-fail rule: one unparseable line and one unknown op
// cost exactly those two lines, and the two good records still materialize.
func TestReplayLog_Corrupt(t *testing.T) {
	recs, _, _, m := replayBytes(t, readContractFile(t, "corrupt.jsonl"))

	require.Len(t, recs, 2)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.corrupt_lines"))
	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.unknown_op"))
}

// TestReplayLog_TruncatedTail pins the expected shape of a crash mid-append: the unterminated final
// line is dropped silently — it is not damage, so it is not counted as corruption.
func TestReplayLog_TruncatedTail(t *testing.T) {
	golden := readContractFile(t, "eliminations.golden.jsonl")
	chopped := golden[:len(golden)-20]

	recs, byID, _, m := replayBytes(t, chopped)

	require.Len(t, recs, 3, "all but the record whose line was torn")
	require.NotContains(t, byID, "elim_2d5c8f1b4e7a")
	require.Zero(t, counterValue(t, m, "negknow.log.corrupt_lines"))
	require.Equal(t, StatusStale, recs[byID["elim_3f9b2c7d1a48"]].Status, "the control lines before the tear still applied")
}

// TestReplayLog_DuplicateAdd pins that a repeated ID keeps the FIRST line: a replay is a
// materialization of history, and history does not get overwritten by a later duplicate.
func TestReplayLog_DuplicateAdd(t *testing.T) {
	first := Record{ID: "elim_dup000000001", Reason: "first", Scope: ScopeSession, Status: StatusActive}
	second := first
	second.Reason = "second"

	var buf bytes.Buffer
	require.NoError(t, appendLine(&buf, first))
	require.NoError(t, appendLine(&buf, second))

	recs, _, _, m := replayBytes(t, buf.Bytes())

	require.Len(t, recs, 1)
	require.Equal(t, "first", recs[0].Reason)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.duplicate_add"))
}

// TestReplayLog_BareRecordLine pins the on-disk form: a record line is an unwrapped Record with no
// {"op":"add"} envelope, which is what the frozen contract fixture carries.
func TestReplayLog_BareRecordLine(t *testing.T) {
	recs, _, lines, m := replayBytes(t, readContractFile(t, "want/elimination_record.jsonl"))

	require.Len(t, recs, 1)
	require.Equal(t, 1, lines)
	require.Equal(t, "elim_3f9b2c7d1a48", recs[0].ID)
	require.Equal(t, SourceSlashCommand, recs[0].Source)
	require.Equal(t, StatusActive, recs[0].Status)
	require.Zero(t, counterValue(t, m, "negknow.log.corrupt_lines"))
}

// TestReplayLog_OrphanStale pins that a control line naming an unknown record is counted and
// skipped: it must never invent the record it refers to.
func TestReplayLog_OrphanStale(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, appendLine(&buf, logControl{Op: opStale, ID: "elim_missing0001", TS: 17, Because: []string{"gone"}}))

	recs, byID, lines, m := replayBytes(t, buf.Bytes())

	require.Empty(t, recs)
	require.Empty(t, byID)
	require.Equal(t, 1, lines)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.orphan_stale"))
}

// TestReplayLog_EmptyIDIsCorrupt pins that a record line with no id is unusable — nothing can index
// or stale-flip it later — so it is counted as corruption rather than materialized.
func TestReplayLog_EmptyIDIsCorrupt(t *testing.T) {
	recs, _, _, m := replayBytes(t, []byte("{\"target\":\"t\"}\n"))

	require.Empty(t, recs)
	require.Equal(t, int64(1), counterValue(t, m, "negknow.log.corrupt_lines"))
}

// TestReplayLog_SinglePassRecovery covers the recovery path the single-pass replay needs to make
// its behaviour-preservation claim true rather than nearly true (ruling R26).
//
// Every line is now decoded ONCE, into the combined logLine, which is a slightly larger shape than
// either line kind — and the cases below are the four ways a real line can fall outside it:
//
//   - a CONTROL line carrying a key recordWire also declares, with a type recordWire does not
//     expect. An unrecognized op is meant to be SKIPPED, not rejected — that is the forward-
//     compatibility rule for a log written by a newer plugin — and a stale flip is meant to apply.
//   - a RECORD line spelling "op" or "because", the two keys the union adds and recordWire does
//     not have, with a conflicting type. recordWire ignores unknown keys, so that line used to
//     materialize; dropping it would silently un-eliminate a real elimination, which is the
//     direction §12 rates High. It must still materialize.
//   - the two genuine failures, which must still be counted: a control line whose own fields will
//     not read, and a record line whose record fields will not read.
//
// Each case also pins WHICH counter moved, because the three Warn messages the replay writes for
// them are the only thing a reader has when they open a damaged log.
func TestReplayLog_SinglePassRecovery(t *testing.T) {
	known := Record{ID: "elim_recover00001", Reason: "still true", Scope: ScopeSession, Status: StatusActive}

	cases := []struct {
		name      string
		lines     []string
		records   int
		corrupt   int64
		unknownOp int64
		stale     bool
	}{{
		// "source" is an integer on the wire, so this line breaks the combined decode. It is a
		// control line from a hypothetical newer plugin and must still be skipped, not counted.
		name:      "unrecognized op with a conflicting record key",
		lines:     []string{`{"op":"prune","source":"other-log"}`},
		records:   1,
		unknownOp: 1,
	}, {
		name:    "stale flip with a conflicting record key still applies",
		lines:   []string{`{"op":"stale","id":"elim_recover00001","ts":17,"because":["dep moved"],"source":"other-log"}`},
		records: 1,
		stale:   true,
	}, {
		// The op is recoverable but the control fields themselves are not: ts is not a number in
		// logControl either, so there is nothing to apply and the line is corruption.
		name:    "stale flip whose own control fields are undecodable",
		lines:   []string{`{"op":"stale","id":"elim_recover00001","ts":"not-a-number"}`},
		records: 1,
		corrupt: 1,
	}, {
		// "because" is a key the UNION declares and a bare record does not, so a record spelling
		// it with a conflicting type breaks the combined decode — and recordWire, which has never
		// heard of it, ignores it entirely. The record must still materialize: losing it would
		// silently un-eliminate a real elimination.
		name:    "record line with a mistyped because key still materializes",
		lines:   []string{`{"id":"elim_recover00002","reason":"still recorded","because":42}`},
		records: 2,
	}, {
		// The union's OTHER added key is a different matter, and it is worth pinning that this is
		// unchanged rather than newly broken: "op" is the discriminator itself, so a line whose op
		// will not read as a string cannot be classified as either kind. logProbe rejected it
		// before the single-pass change for exactly the same reason, and it is corruption now as
		// it was then.
		name:    "record line with a mistyped op key is corrupt, as it always was",
		lines:   []string{`{"id":"elim_recover00003","reason":"also recorded","op":7}`},
		records: 1,
		corrupt: 1,
	}, {
		// No op key, so this is a record line — and a record whose OWN fields will not decode is
		// corruption, counted under the "undecodable record" message rather than the unparseable
		// one.
		name:    "record line with a conflicting record field is corrupt",
		lines:   []string{`{"id":"elim_recover00004","source":"other-log"}`},
		records: 1,
		corrupt: 1,
	}, {
		// Not JSON at all: the "unparseable line" message, and nothing recovered.
		name:    "line that is not JSON is corrupt",
		lines:   []string{`{"id":"elim_recover00005",`},
		records: 1,
		corrupt: 1,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, appendLine(&buf, known))
			for _, l := range tc.lines {
				buf.WriteString(l + "\n")
			}

			recs, byID, _, m := replayBytes(t, buf.Bytes())

			require.Len(t, recs, tc.records)
			require.Equal(t, tc.corrupt, counterValue(t, m, "negknow.log.corrupt_lines"))
			require.Equal(t, tc.unknownOp, counterValue(t, m, "negknow.log.unknown_op"))
			require.Zero(t, counterValue(t, m, "negknow.log.orphan_stale"))

			got := recs[byID[known.ID]]
			if !tc.stale {
				require.Equal(t, StatusActive, got.Status, "nothing in this case may flip the record")
				return
			}
			require.Equal(t, StatusStale, got.Status)
			require.Equal(t, core.UnixMilli(17), got.StaleSince)
			require.Equal(t, []string{"dep moved"}, got.StaleBecause)
		})
	}
}

// TestAppendOnly_Enforced pins that records/eliminations.jsonl is only ever added to.
//
// The plan's table describes this as a truncating open of the log. That probe cannot be written
// against the shipped paths package as an assertion about the log itself: paths.IsProtected covers
// checkpoints/, pins/ and sketches/tried.bloom, and records/ is deliberately not among them, so a
// truncating paths.OpenFile of the log is refused by nothing. What actually holds the invariant up
// for this file is the door — paths.AppendOnly, which never asks for O_TRUNC and refuses any name
// outside *.jsonl/*.ndjson/*.log — plus the fact that this package opens the log through no other
// call. All three halves are asserted here, and the project-wide §7.4 guard is run alongside them.
func TestAppendOnly_Enforced(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	rec := frozenRecord(t)

	w, err := openLog(root)
	require.NoError(t, err)
	require.NoError(t, appendLine(w, rec))
	require.NoError(t, w.Close())

	// A second open must add to the file, never replace it.
	w2, err := openLog(root)
	require.NoError(t, err)
	second := rec
	second.ID = "elim_second00001"
	require.NoError(t, appendLine(w2, second))
	require.NoError(t, w2.Close())

	onDisk, err := os.ReadFile(logPath(root))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(onDisk), "\n"), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `"id":"elim_3f9b2c7d1a48"`, "the first append must survive the second open")
	require.Contains(t, lines[1], `"id":"elim_second00001"`)

	// The door itself: AppendOnly launders nothing that is not a log file.
	_, err = paths.AppendOnly(filepath.Join(paths.Of(root).Records, "eliminations.txt"))
	require.ErrorIs(t, err, core.ErrAppendOnly)

	// And the §7.4 guard the project as a whole still stands on. (testutil.Project.AssertAppendOnly
	// is the packaged form of this check; see frozenClock above for why it cannot be called here.)
	_, err = paths.OpenFile(filepath.Join(paths.Of(root).Checkpoints, "0001.json"),
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)
}

// TestOpenLog_CreatesRecordsDir pins that the first append works on a project whose records/
// directory does not exist yet: the ledger must not require paths.EnsureLayout to have run.
func TestOpenLog_CreatesRecordsDir(t *testing.T) {
	root := t.TempDir()
	w, err := openLog(root)
	require.NoError(t, err)
	require.NoError(t, appendLine(w, Record{ID: "elim_first000001"}))
	require.NoError(t, w.Close())

	b, err := os.ReadFile(logPath(root))
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(b, []byte("\n")))
}

// newlineHostileGen generates records whose every text field carries the three separators that
// would break the one-record-per-line contract if they reached the file raw.
func newlineHostileGen() *rapid.Generator[Record] {
	hostile := rapid.SliceOfN(rapid.SampledFrom([]string{"\n", "\r", " ", "\r\n", "a", "…"}), 0, 6)
	draw := func(rt *rapid.T, label string) string {
		return strings.Join(hostile.Draw(rt, label), "")
	}
	return rapid.Custom(func(rt *rapid.T) Record {
		return Record{
			ID:       "elim_hostile0001",
			Session:  core.SessionID(draw(rt, "session")),
			Target:   draw(rt, "target"),
			Approach: draw(rt, "approach"),
			Reason:   draw(rt, "reason"),
			Desc: Descriptor{
				NormalizedPath: draw(rt, "normalized_path"),
				Symbol:         draw(rt, "symbol"),
				ApproachClass:  draw(rt, "approach_class"),
			},
			DependsOn:    []Dep{{Path: draw(rt, "dep_path")}},
			Scope:        ScopeSession,
			Status:       StatusActive,
			StaleBecause: []string{draw(rt, "because")},
		}
	})
}

// TestAppendLine_NoInteriorNewline is the invariant every reader of this file assumes: one record
// is one line. Text that contains newlines is escaped into the line, not spread across two, and it
// comes back out of the replay byte-identical.
func TestAppendLine_NoInteriorNewline(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rec := newlineHostileGen().Draw(rt, "record")

		var buf bytes.Buffer
		if err := appendLine(&buf, rec); err != nil {
			rt.Fatalf("appendLine: %v", err)
		}
		line := buf.Bytes()
		if n := bytes.Count(line, []byte("\n")); n != 1 {
			rt.Fatalf("want exactly one newline per appended line, got %d: %q", n, line)
		}
		if line[len(line)-1] != '\n' {
			rt.Fatalf("the newline must terminate the line: %q", line)
		}

		recs, _, _, err := replayLog(bytes.NewReader(line), logging.Nop(), newMetrics())
		if err != nil {
			rt.Fatalf("replayLog: %v", err)
		}
		if len(recs) != 1 {
			rt.Fatalf("want 1 record back, got %d", len(recs))
		}
		if diff := cmp.Diff(rec, recs[0], cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("the round trip changed the record (-want +got):\n%s", diff)
		}
	})
}

// TestReplayLog_ReadErrorIsReturned pins the one failure replayLog does NOT swallow: it can degrade
// around a damaged line, but it must not report a partial materialization of a log it could not
// finish reading as a complete one.
func TestReplayLog_ReadErrorIsReturned(t *testing.T) {
	want := errors.New("disk fell over")
	_, _, _, err := replayLog(failingReader{err: want}, logging.Nop(), newMetrics())
	require.ErrorIs(t, err, want)
}

// failingReader fails every read.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }
