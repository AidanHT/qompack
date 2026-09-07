package pins_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/pins/pinstest"
	"github.com/qompack/qompack/internal/testutil"
)

// The three fixed instants every test in this package drives its FakeClock with.
//
// addFixtureMillis and tombFixtureMillis are the timestamps the two FROZEN Rule W-2 fixtures
// carry (testdata/golden/contracts/pins/want/{add,tombstone}.jsonl); plainMillis is the plan's
// own §1168 table value for the non-fixture tests. They are constants rather than literals at
// each call site so a fixture's instant can never drift from the clock a test sets.
const (
	addFixtureMillis  int64 = 1767225120000
	tombFixtureMillis int64 = 1767226000000
	plainMillis       int64 = 1700000000000
)

// invariantIDPattern is the shape MintID promises: "inv_" plus exactly 12 lowercase hex digits.
const invariantIDPattern = `^inv_[0-9a-f]{12}$`

// harness is one disposable project plus the seams pins.OpenWith takes, so a test never has to
// re-derive .qompack/pins/invariants.jsonl's path or remember which registry the store counts on.
type harness struct {
	root     string
	logPath  string
	viewPath string
	metrics  obs.Registry
	clk      *testutil.FakeClock
	logDir   string
	store    pins.Store
}

// newHarness opens a real *pinStore over a fresh temp project with its clock frozen at atMillis
// and a Nop logger. 00-ARCHITECTURE.md §6.1 bans wall-clock sleeps, so every timestamp a test
// asserts is one the test itself chose.
func newHarness(t *testing.T, atMillis int64) *harness {
	t.Helper()
	return newHarnessWithLogger(t, atMillis, logging.Nop(), "")
}

// newLoggingHarness is newHarness with a real file-backed logger at Warn, for the two tests that
// must prove a Warn was actually emitted (a rejected Source, an over-long Text). logging.Nop
// discards everything below Loud, so those assertions need a real sink.
func newLoggingHarness(t *testing.T, atMillis int64) *harness {
	t.Helper()
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Warn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	return newHarnessWithLogger(t, atMillis, log, dir)
}

func newHarnessWithLogger(t *testing.T, atMillis int64, log logging.Logger, logDir string) *harness {
	t.Helper()
	root := t.TempDir()
	clk := testutil.NewFakeClock(time.UnixMilli(atMillis).UTC())
	m := obs.New(clk)
	s, err := pins.OpenWith(root, log, m, clk)
	require.NoError(t, err)
	l := paths.Of(root)
	return &harness{
		root:     root,
		logPath:  filepath.Join(l.Pins, "invariants.jsonl"),
		viewPath: filepath.Join(l.Pins, "invariants.json"),
		metrics:  m,
		clk:      clk,
		logDir:   logDir,
		store:    s,
	}
}

// lines returns the append-only log's records with their terminating newlines intact, because a
// frozen fixture's bytes include that newline and a comparison that stripped it would not be the
// byte-for-byte comparison Rule W-2 asks for.
func (h *harness) lines(t *testing.T) [][]byte {
	t.Helper()
	b, err := os.ReadFile(h.logPath)
	require.NoError(t, err, "reading the append-only log")
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}

// logText returns everything the harness's file-backed logger has written so far.
func (h *harness) logText(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, h.logDir, "logText needs a newLoggingHarness")
	matches, err := filepath.Glob(filepath.Join(h.logDir, "qompack-*.log"))
	require.NoError(t, err)
	var sb strings.Builder
	for _, p := range matches {
		b, readErr := os.ReadFile(p)
		require.NoError(t, readErr)
		sb.Write(b)
	}
	return sb.String()
}

// TestPinsMintIDStable is the plan's §1168 MintID row: normalizeWS collapses every run of
// unicode.IsSpace to one U+0020 and trims both ends, so two spellings of the same sentence mint
// the same id, and the id is always "inv_" plus 12 hex digits (16 characters in total).
func TestPinsMintIDStable(t *testing.T) {
	padded := pins.MintID("  a  b ")
	tight := pins.MintID("a b")

	require.Equal(t, tight, padded, "internal and surrounding whitespace must not change the id")
	require.True(t, strings.HasPrefix(padded, "inv_"))
	require.Len(t, padded, 16)
	require.Regexp(t, invariantIDPattern, padded)

	require.Equal(t, tight, pins.MintID("\t\n a  \v b \r\n"),
		"every unicode space, not just ASCII, participates in the collapse")
	require.NotEqual(t, tight, pins.MintID("a c"), "different text must mint a different id")
	require.Regexp(t, invariantIDPattern, pins.MintID(""))
}

// TestPinsSourceNormalized is the plan's §1168 source row. The empty string is the common
// "caller did not care" case and normalizes to "agent" silently; any other invalid value is
// rewritten to "agent" AND logged at Warn naming the value that was rejected.
func TestPinsSourceNormalized(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"user survives", "user", "user"},
		{"agent survives", "agent", "agent"},
		{"decision survives", "decision", "decision"},
		{"empty becomes agent", "", "agent"},
		{"bogus becomes agent", "bogus", "agent"},
		{"case is not folded", "User", "agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, plainMillis)
			ctx := context.Background()
			require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: "x", Source: tc.in}))

			got, err := h.store.All(ctx)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, tc.want, got[0].Source)
			require.Contains(t, string(h.lines(t)[0]), `"source":"`+tc.want+`"`)
		})
	}
}

// TestPinsRejectedSourceIsLogged proves the second half of the rule above: the rejected value
// itself must appear in the Warn, so a caller can find the spelling that was thrown away.
func TestPinsRejectedSourceIsLogged(t *testing.T) {
	h := newLoggingHarness(t, plainMillis)
	require.NoError(t, h.store.Add(context.Background(), pins.Invariant{Text: "x", Source: "bogus"}))

	text := h.logText(t)
	require.Contains(t, text, "level=warn")
	require.Contains(t, text, "bogus")
}

// TestPinsEmptySourceIsNotLogged is the negative half: the empty string is the ordinary case and
// must normalize SILENTLY, or every hook that omits Source would fill the day log with noise.
func TestPinsEmptySourceIsNotLogged(t *testing.T) {
	h := newLoggingHarness(t, plainMillis)
	require.NoError(t, h.store.Add(context.Background(), pins.Invariant{Text: "x"}))
	require.NotContains(t, h.logText(t), "level=warn")
}

// TestPinsEmptyTextRejected is the plan's §1168 empty-text row: whitespace-only text is not an
// invariant, the error is not one of the four sentinels, and nothing at all reaches the log.
func TestPinsEmptyTextRejected(t *testing.T) {
	for _, text := range []string{"", "   ", "\t\n\v  "} {
		h := newHarness(t, plainMillis)
		ctx := context.Background()

		err := h.store.Add(ctx, pins.Invariant{Text: text})
		require.Error(t, err)
		require.EqualError(t, err, "pins: empty invariant text")

		_, statErr := os.Stat(h.logPath)
		require.True(t, os.IsNotExist(statErr), "a rejected Add must not create the log: %v", statErr)

		got, allErr := h.store.All(ctx)
		require.NoError(t, allErr)
		require.Empty(t, got)
	}
}

// TestPinsAddAppendsOneLine is the plan's §1168 first row: one Add is exactly one compact,
// newline-terminated JSON object, with the minted id, the defaulted source and both timestamps
// taken from the clock the store was opened with.
func TestPinsAddAppendsOneLine(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: "never edit generated/"}))

	lines := h.lines(t)
	require.Len(t, lines, 1)

	id := pins.MintID("never edit generated/")
	require.Regexp(t, invariantIDPattern, id)
	want := `{"op":"add","ts":1700000000000,"invariant":{"id":"` + id +
		`","text":"never edit generated/","source":"agent","pinned":1700000000000}}` + "\n"
	require.Equal(t, want, string(lines[0]))
}

// TestPinsAddIsIdempotent is the plan's §1168 idempotence row. A live id re-added is a no-op:
// nothing is appended and All still reports one invariant. That is what makes an L0 hook that
// re-asserts the same pin on every turn cheap instead of unbounded.
func TestPinsAddIsIdempotent(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	inv := pins.Invariant{Text: "never edit generated/"}
	require.NoError(t, h.store.Add(ctx, inv))
	require.NoError(t, h.store.Add(ctx, inv))

	require.Len(t, h.lines(t), 1)

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// TestPinsAddDefaultsPinnedFromTheClockOnly proves the two timestamps are genuinely distinct
// fields rather than one value written twice: they are EQUAL for a freshly minted Add, and they
// DIFFER the moment a caller supplies an explicit Pinned.
func TestPinsAddDefaultsPinnedFromTheClockOnly(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	require.NoError(t, h.store.Add(ctx, pins.Invariant{ID: "inv_minted00000", Text: "minted now"}))
	require.NoError(t, h.store.Add(ctx, pins.Invariant{
		ID: "inv_backdated00", Text: "pinned earlier", Pinned: core.UnixMilli(plainMillis - 5000),
	}))

	lines := h.lines(t)
	require.Len(t, lines, 2)
	require.Contains(t, string(lines[0]), `"ts":1700000000000`)
	require.Contains(t, string(lines[0]), `"pinned":1700000000000`)
	require.Contains(t, string(lines[1]), `"ts":1700000000000`)
	require.Contains(t, string(lines[1]), `"pinned":1699999995000`)
}

// TestPinsTruncatesOversizeText covers the 2000-byte cap. The cut lands on the last rune
// boundary at or below the cap — never in the middle of a multi-byte rune, which would put
// invalid UTF-8 into a file every later reader has to parse — and a Warn is logged.
func TestPinsTruncatesOversizeText(t *testing.T) {
	h := newLoggingHarness(t, plainMillis)
	ctx := context.Background()

	// 1999 ASCII bytes then a 3-byte rune: the cap falls INSIDE that rune.
	text := strings.Repeat("a", 1999) + "€"
	require.Len(t, []byte(text), 2002)
	require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: text}))

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, strings.Repeat("a", 1999), got[0].Text)
	require.True(t, utf8.ValidString(got[0].Text))
	require.Contains(t, h.logText(t), "level=warn")

	// The id is minted from the text that was actually STORED, not from the oversize input.
	require.Equal(t, pins.MintID(strings.Repeat("a", 1999)), got[0].ID)
}

// TestPinsTextAtTheCapIsUntouched is the boundary either side of the rule above: exactly 2000
// bytes is not "longer than 2000 bytes" and must survive verbatim.
func TestPinsTextAtTheCapIsUntouched(t *testing.T) {
	h := newHarness(t, plainMillis)
	ctx := context.Background()

	text := strings.Repeat("b", 2000)
	require.NoError(t, h.store.Add(ctx, pins.Invariant{Text: text}))

	got, err := h.store.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, text, got[0].Text)
}

// TestPinsOpenKeepsItsOneArgumentArity guards the call site internal/pins/pinstest/suite_test.go
// cannot change: that package may import only pins, testutil and core (00-ARCHITECTURE.md §3.2),
// so it cannot construct a Logger, a Registry or a Clock. Open must keep supplying them itself.
func TestPinsOpenKeepsItsOneArgumentArity(t *testing.T) {
	s, err := pins.Open(t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, s)

	ctx := context.Background()
	require.NoError(t, s.Add(ctx, pins.Invariant{Text: "real, not a stub"}))
	require.False(t, core.IsNotImplemented(s.Add(ctx, pins.Invariant{Text: "still real"})))

	got, err := s.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestRunPinsSuite_RealStore is the Rule W-1 flip: RunPinsSuite is reused UNCHANGED, pointed at
// the real *pinStore, and its behaviour block — skipped for as long as Open returned a stub — now
// runs. If this test ever skips again, the store has regressed to core.ErrNotImplemented.
func TestRunPinsSuite_RealStore(t *testing.T) {
	pinstest.RunPinsSuite(t, "pins.OpenWith", func(t *testing.T) pins.Store {
		clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
		s, err := pins.OpenWith(t.TempDir(), logging.Nop(), obs.New(clk), clk)
		require.NoError(t, err)
		return s
	})
}
