package daemon

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// The SessionStart degrade banner is a systemMessage, and the host caps every systemMessage at
// hookio.HostFieldMaxChars: over it, the user sees a file path and a 2,000-character preview in place
// of the banner (C1.20). degradeBanner quotes a contract Result's Expected and Observed, and Observed
// can carry host-supplied text — session_start.source_compact reports the SessionStart payload's own
// `source` verbatim — so nothing about the inputs bounds the banner. These tests pin the bound by
// construction.

// bannerPathologicalValues are the inputs that make an unbounded %q longest per input byte, or that
// could break a naive byte-wise cut: a huge ASCII run, runes %q escapes to ten characters each,
// astral runes the host counts as two UTF-16 units, invalid UTF-8, and quote/backslash runs %q
// doubles.
func bannerPathologicalValues() map[string]string {
	const n = 200_000
	return map[string]string{
		"ascii":       strings.Repeat("x", n),
		"escaped":     strings.Repeat("\x00\U0010FFFF​", n/3),
		"astral":      strings.Repeat("\U0001F600", n/2),
		"invalidUTF8": strings.Repeat("\xff\xfe", n/2),
		"quotes":      strings.Repeat(`"\`, n/2),
		"mixed":       strings.Repeat("abé\U0001F600\x01\"", n/6),
	}
}

func TestDegradeBanner_StaysFarUnderTheHostCap(t *testing.T) {
	require.LessOrEqual(t, degradeBannerMaxChars*10, hookio.HostFieldMaxChars,
		"the banner's own ceiling must sit far under the host's per-field cap, not merely under it")

	for name, v := range bannerPathologicalValues() {
		t.Run(name, func(t *testing.T) {
			banner := degradeBanner([]contract.Result{{
				ID: contract.ID(strings.Repeat("id.", 10_000)), OK: false, Severity: contract.SevCritical,
				Expected: v, Observed: v,
			}})
			require.LessOrEqual(t, hookio.HostChars(banner), degradeBannerMaxChars,
				"a degrade banner over its ceiling reaches the user as a file path and a preview")
			require.True(t, utf8.ValidString(banner), "the cut must land on a rune boundary")
			require.True(t, strings.HasPrefix(banner, "Qompack: degraded to passive recording — "), banner)
			require.True(t, strings.HasSuffix(banner, ". See /qompack:status."),
				"bounding the quoted values must never cost the pointer to /qompack:status: %s", banner)
		})
	}
}

// TestDegradeBanner_ShortValuesAreQuotedWhole pins that the bound only ever engages on a value that
// needs it: an ordinary failure renders exactly as it always has.
func TestDegradeBanner_ShortValuesAreQuotedWhole(t *testing.T) {
	got := degradeBanner([]contract.Result{
		{ID: contract.CTranscriptReadable, OK: false, Severity: contract.SevWarn, Expected: "w", Observed: "w"},
		{
			ID: contract.CSessionStartSourceCompact, OK: false, Severity: contract.SevCritical,
			Expected: "compact", Observed: "startup",
		},
	})
	require.Equal(t, `Qompack: degraded to passive recording — session_start.source_compact expected "compact", `+
		`observed "startup". See /qompack:status.`, got)
	require.Equal(t, "Qompack: degraded to passive recording. See /qompack:status.", degradeBanner(nil))
}

// TestBoundedQuote_CutsOnARuneBoundaryAndSaysSo pins the helper's contract directly: a value that
// fits is exactly strconv.Quote; one that does not keeps the longest whole-rune prefix whose quoted
// form, with the cut marker, fits the budget — and the marker is always there when anything was cut.
func TestBoundedQuote_CutsOnARuneBoundaryAndSaysSo(t *testing.T) {
	require.Equal(t, strconv.Quote("startup"), boundedQuote("startup", degradeBannerValueMaxChars))
	require.Equal(t, strconv.Quote(""), boundedQuote("", degradeBannerValueMaxChars))

	for name, v := range bannerPathologicalValues() {
		t.Run(name, func(t *testing.T) {
			q := boundedQuote(v, degradeBannerValueMaxChars)
			require.LessOrEqual(t, hookio.HostChars(q), degradeBannerValueMaxChars)
			body, err := strconv.Unquote(q)
			require.NoError(t, err, "the bounded value must still be one valid Go-quoted string: %s", q)
			require.True(t, strings.HasSuffix(body, bannerCutMarker), "a cut value must say it was cut: %s", q)
			prefix := strings.TrimSuffix(body, bannerCutMarker)
			require.True(t, strings.HasPrefix(v, prefix), "the kept part must be a prefix of the value")
			require.NotEmpty(t, prefix, "the budget must leave room for some of the value")
		})
	}

	// The boundary itself: a value whose quoted form is exactly the budget is kept whole, and one
	// character more is cut.
	exact := strings.Repeat("a", degradeBannerValueMaxChars-2)
	require.Equal(t, strconv.Quote(exact), boundedQuote(exact, degradeBannerValueMaxChars))
	over := exact + "a"
	require.NotEqual(t, strconv.Quote(over), boundedQuote(over, degradeBannerValueMaxChars))
}

// TestSessionStart_DegradeBannerFromAHostSuppliedSourceIsBounded drives the real route: a PreCompact
// was observed for the session, so session_start.source_compact evaluates the next start's `source`
// — a host-supplied string, here a pathological one — fails critically, degrades the daemon, and the
// banner quotes it. What the hook client would hand the host must stay under the cap.
//
// Deliberately NOT parallel: New declares producers into the process-wide set.
func TestSessionStart_DegradeBannerFromAHostSuppliedSourceIsBounded(t *testing.T) {
	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	const sess = core.SessionID("sess-banner")
	h := contract.LoadHistory(contract.HistoryPath(root))
	h.SessionCount = 1
	h.AwaitingCompactStart = true
	h.LastPrecompactSession = sess
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(root), h))

	source := strings.Repeat("\U0001F600\x00", 150_000)
	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: sess, CWD: root, Source: source}
	resp := dd.dispatchOp(context.Background(), ipc.Request{
		Op: ipc.OpSessionStart, Session: sess, Reply: true, Event: ev, TS: core.NowMilli(dd.clk),
	})
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Equal(t, contract.ModeDegradedPassive, dd.monitor.Mode(), "fixture: the start must degrade")
	require.Contains(t, resp.Output.SystemMessage, string(contract.CSessionStartSourceCompact),
		"fixture: the banner must name the failing assertion")

	require.LessOrEqual(t, hookio.HostChars(resp.Output.SystemMessage), degradeBannerMaxChars)
	require.Empty(t, hookio.HostCapOverruns(hookio.ConformOutput(hookio.EventSessionStart, *resp.Output)),
		"no field the host receives from this start may fall back to a file path and a preview")
}
