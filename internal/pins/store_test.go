package pins_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/testutil"
)

// The two ids the frozen fixtures carry. They are supplied to Add explicitly rather than minted,
// because the fixtures were written before MintID existed and Rule W-2 forbids editing them: the
// implementation must reproduce the RECORD, and an id a caller supplies is passed through
// untouched, which is exactly what this proves.
const (
	fixtureAddID  = "inv_7c1a9e2f4b60"
	fixtureTombID = "inv_2d8f0a6c3e15"
)

// fixtureAddText is the add fixture's pinned text, verbatim.
const fixtureAddText = "The refresh-token rotation must remain single-use; reuse detection is a security requirement, not a preference."

// dirPerm and filePerm are the modes .qompack's runtime tree uses (paths.EnsureLayout).
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// seedLog writes content verbatim into a fresh project's .qompack/pins/invariants.jsonl through
// paths.AppendOnly — the only door §7.4 opens onto that file — and returns the project root. It
// exists so a replay test can put bytes on disk that no correct writer would ever produce.
func seedLog(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Pins, dirPerm))

	w, err := paths.AppendOnly(filepath.Join(l.Pins, "invariants.jsonl"))
	require.NoError(t, err)
	_, err = io.WriteString(w, content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return root
}

// openSeeded opens a store over root with a Nop logger and a fresh registry frozen at
// plainMillis, and returns both so a replay test can read the pins.badline counter.
func openSeeded(t *testing.T, root string) (pins.Store, obs.Registry) {
	t.Helper()
	clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
	m := obs.New(clk)
	s, err := pins.OpenWith(root, logging.Nop(), m, clk)
	require.NoError(t, err)
	return s, m
}

// TestPinsMatchesFrozenAddFixture is Rule W-2 for the add record: the line Add appends must be
// byte-identical to testdata/golden/contracts/pins/want/add.jsonl. Note what the fixture pins —
// the invariant is NESTED under "invariant", the record carries its own "ts" beside the
// invariant's own "pinned", and the key order is op, ts, invariant / id, text, source, pinned.
func TestPinsMatchesFrozenAddFixture(t *testing.T) {
	_, want, frozen := testutil.ContractFixture(t, "pins", "add")
	require.True(t, frozen, "the pins add fixture is declared frozen in its MANIFEST.json")

	h := newHarness(t, addFixtureMillis)
	require.NoError(t, h.store.Add(context.Background(), pins.Invariant{
		ID:     fixtureAddID,
		Text:   fixtureAddText,
		Source: "user",
		Pinned: core.UnixMilli(addFixtureMillis),
	}))

	lines := h.lines(t)
	require.Len(t, lines, 1)
	require.Equal(t, string(want), string(lines[0]))
}

// TestPinsRemoveWritesTombstone is Rule W-2 for the deletion record, and the plan's §1168 remove
// row. The verb is "remove" (never "del"), the record carries no "pinned" key at all, the two
// earlier add records are untouched — a tombstone is an append, never a rewrite — and All stops
// reporting the removed invariant.
func TestPinsRemoveWritesTombstone(t *testing.T) {
	_, want, frozen := testutil.ContractFixture(t, "pins", "tombstone")
	require.True(t, frozen, "the pins tombstone fixture is declared frozen in its MANIFEST.json")

	h := newHarness(t, tombFixtureMillis)
	ctx := context.Background()

	first := pins.Invariant{
		ID: fixtureTombID, Text: "the pin that is removed", Source: "user",
		Pinned: core.UnixMilli(addFixtureMillis),
	}
	second := pins.Invariant{
		ID: "inv_aaaaaaaaaaaa", Text: "the pin that survives", Source: "agent",
		Pinned: core.UnixMilli(addFixtureMillis + 10000),
	}
	require.NoError(t, h.store.Add(ctx, first))
	require.NoError(t, h.store.Add(ctx, second))

	before := h.lines(t)
	require.Len(t, before, 2)

	require.NoError(t, h.store.Remove(ctx, fixtureTombID))

	lines := h.lines(t)
	require.Len(t, lines, 3)
	require.Equal(t, string(want), string(lines[2]))
	require.Equal(t, string(before[0]), string(lines[0]), "Remove must never rewrite an earlier record")
	require.Equal(t, string(before[1]), string(lines[1]), "Remove must never rewrite an earlier record")
	require.NotContains(t, string(lines[2]), `"pinned"`)

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Equal(t, []pins.Invariant{second}, got)
}

// TestPinsRemoveUnknownIsNotFound is the plan's §1168 unknown-id row: an id this project has
// never seen is core.ErrNotFound, wrapped so errors.Is finds it, and nothing is appended.
func TestPinsRemoveUnknownIsNotFound(t *testing.T) {
	h := newHarness(t, plainMillis)

	err := h.store.Remove(context.Background(), "inv_deadbeefcafe")
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Contains(t, err.Error(), "inv_deadbeefcafe")

	_, statErr := os.Stat(h.logPath)
	require.True(t, os.IsNotExist(statErr), "a refused Remove must not create the log")
}

// TestPinsRemoveTwiceIsANoOp is the case the conformance suite pins directly
// (pinstest.RunPinsSuite / remove_writes_a_tombstone_and_all_excludes_it): removing an id that is
// ALREADY tombstoned must not error and must not resurrect it. "Not live" therefore splits in
// two — never seen is core.ErrNotFound, already tombstoned is a silent no-op — and neither
// appends a second tombstone.
func TestPinsRemoveTwiceIsANoOp(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	require.NoError(t, h.store.Add(ctx, pins.Invariant{ID: "inv_gone00000000", Text: "temporary"}))
	require.NoError(t, h.store.Remove(ctx, "inv_gone00000000"))
	require.Len(t, h.lines(t), 2)

	require.NoError(t, h.store.Remove(ctx, "inv_gone00000000"))
	require.Len(t, h.lines(t), 2, "a second Remove appends nothing")

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestPinsReaddAfterRemoveAppendsAgain is the other side of idempotence: once tombstoned, an id
// is no longer live, so re-adding it is a real append and it comes back.
func TestPinsReaddAfterRemoveAppendsAgain(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	inv := pins.Invariant{ID: "inv_cycle0000000", Text: "back again", Source: "user", Pinned: core.UnixMilli(plainMillis)}
	require.NoError(t, h.store.Add(ctx, inv))
	require.NoError(t, h.store.Remove(ctx, inv.ID))
	require.NoError(t, h.store.Add(ctx, inv))

	require.Len(t, h.lines(t), 3)

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Equal(t, []pins.Invariant{inv}, got)

	// And the same is true of a replay from the log alone: add, remove, add is one live pin.
	reopened, _ := openSeeded(t, h.root)
	got, err = reopened.All(ctx)
	require.NoError(t, err)
	require.Equal(t, []pins.Invariant{inv}, got)
}

// TestPinsReplaySkipsMalformedLine is the plan's §1168 malformed-line row. A line that does not
// parse is skipped, counted on the obs.Registry OpenWith was handed, and reported once per open
// at Warn — it never aborts the replay, because a single corrupt line must not cost a project
// every pin it ever recorded.
func TestPinsReplaySkipsMalformedLine(t *testing.T) {
	good1 := `{"op":"add","ts":1700000000000,"invariant":{"id":"inv_first0000000","text":"first","source":"user","pinned":1700000000000}}` + "\n"
	good2 := `{"op":"add","ts":1700000000001,"invariant":{"id":"inv_second000000","text":"second","source":"agent","pinned":1700000000001}}` + "\n"
	root := seedLog(t, good1+"not json\n"+good2)

	s, m := openSeeded(t, root)

	got, err := s.All(context.Background())
	require.NoError(t, err, "a malformed line must never abort the replay")
	require.Len(t, got, 2)
	require.Equal(t, "inv_first0000000", got[0].ID)
	require.Equal(t, "inv_second000000", got[1].ID)

	require.Equal(t, int64(1), m.Counter("pins.badline").Value())
	require.Equal(t, int64(1), m.Snapshot().Counters["pins.badline"])
}

// TestPinsReplayBadLineShapes covers every way a line can be unusable — unparseable JSON, an
// unknown verb, an add with no invariant, an add whose invariant has no id, a remove with no id —
// and confirms blank lines are simply ignored rather than counted as damage.
func TestPinsReplayBadLineShapes(t *testing.T) {
	good := `{"op":"add","ts":1700000000000,"invariant":{"id":"inv_survivor00000","text":"kept","source":"user","pinned":1700000000000}}` + "\n"
	bad := strings.Join([]string{
		`{not json`,
		`{"op":"rewrite","ts":1,"id":"inv_x"}`,
		`{"op":"add","ts":1}`,
		`{"op":"add","ts":1,"invariant":{"id":"","text":"no id","source":"user","pinned":1}}`,
		`{"op":"remove","ts":1}`,
	}, "\n") + "\n"
	root := seedLog(t, "\n"+good+"\n"+bad+"   \n")

	s, m := openSeeded(t, root)

	got, err := s.All(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "inv_survivor00000", got[0].ID)
	require.Equal(t, int64(5), m.Counter("pins.badline").Value(),
		"five unusable records, and blank lines are not damage")
}

// TestPinsReplayHandlesAFinalLineWithNoNewline proves the reader does not silently drop a record
// a crashed writer left unterminated.
func TestPinsReplayHandlesAFinalLineWithNoNewline(t *testing.T) {
	root := seedLog(t, `{"op":"add","ts":1700000000000,"invariant":{"id":"inv_unterminated","text":"tail","source":"user","pinned":1700000000000}}`)

	s, _ := openSeeded(t, root)
	got, err := s.All(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// TestPinsReplayRefreshesADuplicateAdd covers the "add inserts or refreshes" half of the replay:
// a log that records the same id twice (which no correct writer produces, but an older writer or
// a concurrent one might) keeps the LAST value and reports the invariant exactly once.
func TestPinsReplayRefreshesADuplicateAdd(t *testing.T) {
	first := `{"op":"add","ts":1,"invariant":{"id":"inv_dup000000000","text":"old","source":"user","pinned":1700000000000}}` + "\n"
	second := `{"op":"add","ts":2,"invariant":{"id":"inv_dup000000000","text":"new","source":"agent","pinned":1700000000000}}` + "\n"
	root := seedLog(t, first+second)

	s, m := openSeeded(t, root)
	got, err := s.All(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "new", got[0].Text)
	require.Equal(t, int64(0), m.Snapshot().Counters["pins.badline"], "a refresh is not damage")
}

// TestPinsStateSurvivesReopen is the durability claim behind the in-memory index: the log is the
// truth, so a second OpenWith over the same root must reconstruct exactly what the first store
// reported without any shared state between them.
func TestPinsStateSurvivesReopen(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	for i, text := range []string{"alpha", "beta", "gamma"} {
		require.NoError(t, h.store.Add(ctx, pins.Invariant{
			Text: text, Source: "user", Pinned: core.UnixMilli(plainMillis + int64(i)),
		}))
	}
	require.NoError(t, h.store.Remove(ctx, pins.MintID("beta")))

	want, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Len(t, want, 2)

	reopened, _ := openSeeded(t, h.root)
	got, err := reopened.All(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestPinsAllSortsByPinnedThenID pins the ordering contract: Pinned ascending, ties broken by ID
// ascending. The tiebreak is what makes All deterministic for the common case of several pins
// minted inside the same millisecond.
func TestPinsAllSortsByPinnedThenID(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	// Appended newest-first and with the two same-instant pins in reverse id order, so insertion
	// order can never accidentally look like the required order.
	require.NoError(t, h.store.Add(ctx, pins.Invariant{ID: "inv_cccccccccccc", Text: "c", Pinned: core.UnixMilli(300)}))
	require.NoError(t, h.store.Add(ctx, pins.Invariant{ID: "inv_bbbbbbbbbbbb", Text: "b", Pinned: core.UnixMilli(100)}))
	require.NoError(t, h.store.Add(ctx, pins.Invariant{ID: "inv_aaaaaaaaaaaa", Text: "a", Pinned: core.UnixMilli(100)}))

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"inv_aaaaaaaaaaaa", "inv_bbbbbbbbbbbb", "inv_cccccccccccc"},
		[]string{got[0].ID, got[1].ID, got[2].ID})
}

// TestPinsMaterializeView is the plan's §1168 materialize row: invariants.json is the derived
// view — a 2-space-indented JSON array with a trailing newline, holding exactly the live
// invariants in All's order — regenerated from the log and never the source of truth.
func TestPinsMaterializeView(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	one := pins.Invariant{ID: "inv_one000000000", Text: "one", Source: "user", Pinned: core.UnixMilli(plainMillis)}
	two := pins.Invariant{ID: "inv_two000000000", Text: "two", Source: "agent", Pinned: core.UnixMilli(plainMillis + 1)}
	three := pins.Invariant{ID: "inv_three00000000", Text: "three", Source: "decision", Pinned: core.UnixMilli(plainMillis + 2)}
	require.NoError(t, h.store.Add(ctx, one))
	require.NoError(t, h.store.Add(ctx, two))
	require.NoError(t, h.store.Add(ctx, three))
	require.NoError(t, h.store.Remove(ctx, two.ID))

	require.NoError(t, h.store.Materialize(ctx))

	b, err := os.ReadFile(h.viewPath)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(b), "}\n]\n"), "one trailing newline after the array")

	var got []pins.Invariant
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, []pins.Invariant{one, three}, got, "live invariants only, sorted by Pinned")

	indented, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(indented)+"\n", string(b), "2-space indent, exactly one trailing newline")
}

// TestPinsMaterializeRunsAfterEveryAddAndRemove proves the view is kept current without the
// caller having to remember to regenerate it: checkpoint.FileWriter.Finalize calls Materialize
// too, but a project that only ever calls Add must still have a readable invariants.json.
func TestPinsMaterializeRunsAfterEveryAddAndRemove(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	_, statErr := os.Stat(h.viewPath)
	require.True(t, os.IsNotExist(statErr), "no view before the first write")

	inv := pins.Invariant{ID: "inv_view00000000", Text: "visible", Source: "user", Pinned: core.UnixMilli(plainMillis)}
	require.NoError(t, h.store.Add(ctx, inv))

	b, err := os.ReadFile(h.viewPath)
	require.NoError(t, err, "Add must leave the view regenerated")
	require.Contains(t, string(b), "inv_view00000000")

	require.NoError(t, h.store.Remove(ctx, inv.ID))
	b, err = os.ReadFile(h.viewPath)
	require.NoError(t, err, "Remove must leave the view regenerated")
	require.Equal(t, "[]\n", string(b), "an empty set is an empty array, never null")
}

// TestPinsMaterializeIsIdempotent proves regenerating the view twice produces the same bytes: it
// is a projection of the log, so nothing about it may depend on how many times it has run.
func TestPinsMaterializeIsIdempotent(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: "stable", Source: "user", Pinned: core.UnixMilli(plainMillis)}))
	first, err := os.ReadFile(h.viewPath)
	require.NoError(t, err)

	require.NoError(t, h.store.Materialize(ctx))
	second, err := os.ReadFile(h.viewPath)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

// TestPinsAppendOnlyGuard is the plan's §1168 append-only row and the §7.4 invariant itself:
// invariants.jsonl is protected, so a truncating open through paths must be refused outright and
// the bytes already on disk must be untouched afterward.
func TestPinsAppendOnlyGuard(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()
	require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: "protected", Source: "user"}))

	before, err := os.ReadFile(h.logPath)
	require.NoError(t, err)

	f, err := paths.OpenFile(h.logPath, os.O_WRONLY|os.O_TRUNC, filePerm)
	if f != nil {
		_ = f.Close()
	}
	require.ErrorIs(t, err, core.ErrAppendOnly)

	f, err = paths.OpenFile(h.logPath, os.O_WRONLY, filePerm)
	if f != nil {
		_ = f.Close()
	}
	require.ErrorIs(t, err, core.ErrAppendOnly, "an in-place rewrite is neither an append nor an exclusive create")

	after, err := os.ReadFile(h.logPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

// TestPinsOpenWithCreatesThePinsDirectory pins OpenWith's other side effect: the store is usable
// against a project root that has no .qompack tree yet, because the L0 hooks that pin an
// invariant run before anything has called paths.EnsureLayout.
func TestPinsOpenWithCreatesThePinsDirectory(t *testing.T) {
	root := t.TempDir()
	clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
	s, err := pins.OpenWith(root, logging.Nop(), obs.New(clk), clk)
	require.NoError(t, err)
	require.NotNil(t, s)

	fi, err := os.Stat(paths.Of(root).Pins)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
}

// TestPinsOpenWithFailsWhenPinsIsNotADirectory is the error path of the same call: a store that
// cannot create its own directory must report that, not return a handle that fails later.
func TestPinsOpenWithFailsWhenPinsIsNotADirectory(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Dot, dirPerm))
	require.NoError(t, os.WriteFile(l.Pins, []byte("not a directory"), filePerm))

	clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
	s, err := pins.OpenWith(root, logging.Nop(), obs.New(clk), clk)
	require.Error(t, err)
	require.Nil(t, s)
}

// TestPinsOpenWithFailsWhenTheLogIsUnreadable proves a replay that cannot read the log is an
// error rather than a silently empty store: reporting "no pins" for a log we failed to open
// would let a checkpoint drop every invariant the project has.
func TestPinsOpenWithFailsWhenTheLogIsUnreadable(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(filepath.Join(l.Pins, "invariants.jsonl"), dirPerm))

	clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
	s, err := pins.OpenWith(root, logging.Nop(), obs.New(clk), clk)
	require.Error(t, err, "a log that is a directory must not read as an empty log")
	require.Nil(t, s)
}

// TestPinsHonoursAnExpiredContext covers the ctx.Err() pre-check every Store method carries, the
// same shape store.FSStore uses: an already-cancelled call must spend nothing.
func TestPinsHonoursAnExpiredContext(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, h.store.Add(ctx, pins.Invariant{Text: "never written"}), context.Canceled)
	require.ErrorIs(t, h.store.Remove(ctx, "inv_whatever0000"), context.Canceled)
	require.ErrorIs(t, h.store.Materialize(ctx), context.Canceled)

	_, err := h.store.All(ctx)
	require.ErrorIs(t, err, context.Canceled)

	_, statErr := os.Stat(h.logPath)
	require.True(t, os.IsNotExist(statErr))
}

// TestPinsReplayTombstoneForAnUnknownID covers the shape a partially-GC'd or hand-edited log can
// have: a well-formed tombstone whose add record is not in the file. It is a USABLE record — it
// names an id — so it is not counted as damage; it simply removes nothing.
func TestPinsReplayTombstoneForAnUnknownID(t *testing.T) {
	good := `{"op":"add","ts":1,"invariant":{"id":"inv_present00000","text":"here","source":"user","pinned":1700000000000}}` + "\n"
	orphan := `{"op":"remove","ts":2,"id":"inv_neverAdded000"}` + "\n"
	root := seedLog(t, orphan+good)

	s, m := openSeeded(t, root)
	got, err := s.All(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(0), m.Snapshot().Counters["pins.badline"])

	// And the orphan is "seen", so removing it is the already-tombstoned no-op, not ErrNotFound.
	require.NoError(t, s.Remove(context.Background(), "inv_neverAdded000"))
}

// TestPinsAddReportsAnAppendFailure proves a log that cannot be appended to is reported rather
// than swallowed: the in-memory set must never claim a pin the file does not carry.
func TestPinsAddReportsAnAppendFailure(t *testing.T) {
	h := newHarness(t, plainMillis)
	require.NoError(t, os.MkdirAll(h.logPath, dirPerm), "a directory where the log file belongs")

	err := h.store.Add(context.Background(), pins.Invariant{Text: "cannot land", Source: "user"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pins: appending to")

	got, allErr := h.store.All(context.Background())
	require.NoError(t, allErr)
	require.Empty(t, got, "a failed append must not leave the invariant live in memory")
}

// TestPinsRemoveReportsAnAppendFailure is the same guarantee for the tombstone: if the record
// cannot be written, the invariant stays live, because a Remove that only happened in memory
// would come back on the next open and look like a resurrection.
func TestPinsRemoveReportsAnAppendFailure(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	inv := pins.Invariant{ID: "inv_stuck0000000", Text: "cannot be removed", Source: "user", Pinned: core.UnixMilli(plainMillis)}
	require.NoError(t, h.store.Add(ctx, inv))

	require.NoError(t, os.Remove(h.logPath))
	require.NoError(t, os.MkdirAll(h.logPath, dirPerm))

	err := h.store.Remove(ctx, inv.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pins: appending to")

	got, allErr := h.store.All(ctx)
	require.NoError(t, allErr)
	require.Equal(t, []pins.Invariant{inv}, got)
}

// TestPinsMaterializeReportsAStagingFailure covers the view writer's first error path: nowhere to
// stage the replacement.
func TestPinsMaterializeReportsAStagingFailure(t *testing.T) {
	h := newHarness(t, plainMillis)
	l := paths.Of(h.root)
	require.NoError(t, os.WriteFile(l.Tmp, []byte("not a directory"), filePerm))

	err := h.store.Materialize(context.Background())
	require.Error(t, err)
	// The staging directory is what failed, and both layers say so: paths.ReplacePinsView reports
	// the mkdir, and pins names the file it was replacing. Asserting on both halves is what pins
	// the layering — a Materialize that swallowed the error, or one that lost track of which file
	// it was writing, would each fail exactly one of these.
	require.Contains(t, err.Error(), "pins: replacing")
	require.Contains(t, err.Error(), "invariants.json")
	require.Contains(t, err.Error(), "tmp")
}

// TestPinsMaterializeReportsAReplaceFailure covers the last one: the staged bytes are written and
// fsynced, but the swap into place cannot happen.
func TestPinsMaterializeReportsAReplaceFailure(t *testing.T) {
	h := newHarness(t, plainMillis)
	require.NoError(t, os.MkdirAll(h.viewPath, dirPerm), "a directory where the view file belongs")

	err := h.store.Materialize(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "pins: replacing")
}

// TestPinsConcurrentAddsAreSerialized drives the sync.Mutex OpenWith promises guards every
// mutation. Under -race this is the test that would find an unguarded map write; without the
// mutex it would also interleave two appends into one line.
func TestPinsConcurrentAddsAreSerialized(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func() {
			defer wg.Done()
			require.NoError(t, h.store.Add(ctx, pins.Invariant{
				Text:   fmt.Sprintf("concurrent invariant %02d", i),
				Source: "agent",
				Pinned: core.UnixMilli(plainMillis + int64(i)),
			}))
		}()
	}
	wg.Wait()

	require.Len(t, h.lines(t), writers)

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, writers)

	reopened, _ := openSeeded(t, h.root)
	replayed, err := reopened.All(ctx)
	require.NoError(t, err)
	require.Equal(t, got, replayed)
}

// TestPinsAddRetryRepairsAViewThatFailedToWrite covers the gap between "the log took it" and "the
// view shows it". Add appends and updates memory BEFORE it regenerates invariants.json — the
// ordering is deliberate, since memory must never claim a pin the file does not carry — so a view
// write that fails leaves the invariant durably recorded and live while the view on disk does not
// list it. Add still reports the error, because the caller has to learn the view is behind; but a
// caller that reads that error as "not recorded" and retries the identical Add must not receive a
// success that regenerated nothing.
func TestPinsAddRetryRepairsAViewThatFailedToWrite(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()
	l := paths.Of(h.root)

	// A regular file where the staging directory belongs: the log append still lands, but the view
	// replacement cannot even be staged.
	require.NoError(t, os.WriteFile(l.Tmp, []byte("not a directory"), filePerm))

	inv := pins.Invariant{
		ID: "inv_recordedonly", Text: "recorded but not shown", Source: "user",
		Pinned: core.UnixMilli(plainMillis),
	}
	require.Error(t, h.store.Add(ctx, inv), "a view write that failed must still be reported")

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Equal(t, []pins.Invariant{inv}, got, "the append landed, so the invariant is live")
	_, statErr := os.Stat(h.viewPath)
	require.True(t, os.IsNotExist(statErr), "and the view was never written")

	require.NoError(t, os.Remove(l.Tmp))
	require.NoError(t, h.store.Add(ctx, inv), "the retry takes the idempotent no-op branch")

	b, err := os.ReadFile(h.viewPath)
	require.NoError(t, err, "the retry must regenerate the view the first attempt could not write")
	require.Contains(t, string(b), inv.ID)
	require.Len(t, h.lines(t), 1, "and must repair the view, not append a second add record")
}

// TestPinsRemoveRetryClearsAnInvariantLeftVisibleByAFailedViewWrite is the same hazard in the
// direction that does real damage. The tombstone is durable and the id is out of the live set,
// but invariants.json still LISTS the removed invariant, so every reader of the view — a
// rehydrating session, the MCP server — keeps presenting a pin the log says is gone, and the
// obvious retry hits the already-tombstoned no-op.
func TestPinsRemoveRetryClearsAnInvariantLeftVisibleByAFailedViewWrite(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()
	l := paths.Of(h.root)

	inv := pins.Invariant{
		ID: "inv_stillvisible", Text: "removed but shown", Source: "user",
		Pinned: core.UnixMilli(plainMillis),
	}
	require.NoError(t, h.store.Add(ctx, inv))
	b, err := os.ReadFile(h.viewPath)
	require.NoError(t, err)
	require.Contains(t, string(b), inv.ID)

	// Break the staging directory the successful Add just created, so the tombstone still appends
	// but the view replacement cannot be staged.
	require.NoError(t, os.RemoveAll(l.Tmp))
	require.NoError(t, os.WriteFile(l.Tmp, []byte("not a directory"), filePerm))
	require.Error(t, h.store.Remove(ctx, inv.ID))

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Empty(t, got, "the tombstone landed, so the invariant is gone from the live set")
	b, err = os.ReadFile(h.viewPath)
	require.NoError(t, err)
	require.Contains(t, string(b), inv.ID, "but the stale view still lists it — that is the hazard")

	require.NoError(t, os.Remove(l.Tmp))
	require.NoError(t, h.store.Remove(ctx, inv.ID), "the already-tombstoned retry must succeed")

	b, err = os.ReadFile(h.viewPath)
	require.NoError(t, err)
	require.Equal(t, "[]\n", string(b), "and must clear the removed invariant from the view")
	require.Len(t, h.lines(t), 2, "one add and one tombstone: the retry appended nothing")
}

// TestPinsReplayTreatsAnOversizeLineAsOneBadRecord pins the ceiling on a replayed line. A record
// this package writes is bounded — capText holds an invariant's text to maxTextBytes — so a line
// far past that is damage, and damage is what replay must survive rather than obey: it is counted
// like any other unusable line, the reader resynchronises at the next newline, and the good
// records on either side of it still land.
//
// The "structurally valid" case is the load-bearing one. Unparseable garbage was already skipped
// for being unparseable, which says nothing about whether the reader read it in bounded memory; a
// well-formed add record carrying half a megabyte of text is refused only if its SIZE is what
// refuses it.
func TestPinsReplayTreatsAnOversizeLineAsOneBadRecord(t *testing.T) {
	const oversizeBytes = 500_000

	before := `{"op":"add","ts":1,"invariant":{"id":"inv_before000000","text":"before","source":"user","pinned":1700000000000}}` + "\n"
	after := `{"op":"add","ts":3,"invariant":{"id":"inv_after0000000","text":"after","source":"agent","pinned":1700000000001}}` + "\n"

	for _, tc := range []struct {
		name     string
		oversize string
	}{
		{
			name:     "unparseable",
			oversize: strings.Repeat("x", oversizeBytes) + "\n",
		},
		{
			name:     "a structurally valid add record",
			oversize: `{"op":"add","ts":2,"invariant":{"id":"inv_oversize0000","text":"` + strings.Repeat("x", oversizeBytes) + `","source":"user","pinned":1700000000000}}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, m := openSeeded(t, seedLog(t, before+tc.oversize+after))

			got, err := s.All(context.Background())
			require.NoError(t, err, "an oversize line must never abort the replay")
			require.Len(t, got, 2, "the oversize record itself is refused")
			require.Equal(t, []string{"inv_before000000", "inv_after0000000"},
				[]string{got[0].ID, got[1].ID},
				"the reader resynchronised at the next newline and kept the record after it")
			require.Equal(t, int64(1), m.Snapshot().Counters["pins.badline"],
				"one oversize line is one bad line, not one per buffer-full")
		})
	}
}

// TestPinsReplayDoesNotAllocateAWholeOversizeLine is the defect itself rather than its symptom.
// bufio.Reader.ReadBytes allocates the ENTIRE line before anything can judge it, so a log
// truncated mid-line or concatenated with another file — no newline for a gigabyte — would make
// OpenWith ask the allocator for a gigabyte, inside the daemon, at session start, on the one file
// whose whole promise is that damage to it is survivable. A bounded reader takes it in
// fixed-size pieces and keeps none of them.
//
// The ceiling here is a quarter of the line, which no unbounded read can slip under: reading the
// line at all costs at least its own length, and append's doubling costs closer to twice that.
func TestPinsReplayDoesNotAllocateAWholeOversizeLine(t *testing.T) {
	const lineBytes = 16 << 20
	const allocCeiling = lineBytes / 4

	survivor := `{"op":"add","ts":1,"invariant":{"id":"inv_afterthehuge","text":"after","source":"user","pinned":1700000000000}}` + "\n"
	root := seedLog(t, strings.Repeat("x", lineBytes)+"\n"+survivor)

	var beforeStats, afterStats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&beforeStats)

	s, m := openSeeded(t, root)

	runtime.ReadMemStats(&afterStats)

	require.Less(t, afterStats.TotalAlloc-beforeStats.TotalAlloc, uint64(allocCeiling),
		"replaying a %d-byte line must not allocate the line", lineBytes)

	got, err := s.All(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1, "the record after the oversize line still replays")
	require.Equal(t, "inv_afterthehuge", got[0].ID)
	require.Equal(t, int64(1), m.Snapshot().Counters["pins.badline"])
}
