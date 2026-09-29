package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/obs"
)

// collectedAt is the fixed clock reading every case in this file collects at.
var collectedAt = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

// jsonFieldNames returns t's JSON member names in declaration order, which is the order the
// encoder emits them and therefore the order a golden fixture records.
func jsonFieldNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := tag
		for j, c := range tag {
			if c == ',' {
				name = tag[:j]
				break
			}
		}
		if name == "" {
			name = t.Field(i).Name
		}
		out = append(out, name)
	}
	return out
}

// TestStatus_DaemonPayloadMirrorIsCurrent is the schema comparison the plan names.
//
// internal/commands does not import internal/daemon in its non-test code: the decoupling is a
// deliberate choice (the plan's "commands → daemon avoidance is a decoupling choice"), so the
// status route's payload is mirrored here as commands.DaemonStatus. A mirror that silently falls
// behind is worse than no mirror at all — it renders a stale subset as if it were the whole
// answer — so this test fails the moment the daemon's own StatusSnapshot gains, loses or renames
// a member.
func TestStatus_DaemonPayloadMirrorIsCurrent(t *testing.T) {
	t.Parallel()

	want := jsonFieldNames(reflect.TypeOf(daemon.StatusSnapshot{}))
	got := jsonFieldNames(reflect.TypeOf(commands.DaemonStatus{}))
	require.Equal(t, want, got,
		"commands.DaemonStatus must mirror daemon.StatusSnapshot member for member, in order")
}

// TestStatus_OriginalOpStatusIsPreserved is the isolation half: SP-14 adds a status.full view on
// top of the ipc.OpStatus payload and must not reinterpret it. Whatever the daemon sent survives
// into the report byte for byte.
func TestStatus_OriginalOpStatusIsPreserved(t *testing.T) {
	t.Parallel()

	snap := commands.DaemonStatus{
		Mode:       "active",
		Hot:        "sync",
		Counters:   map[string]int64{"l0_admission_daemon": 7},
		SpoolFiles: 3,
		LoudTail:   []string{"one loud line"},
		Extra:      json.RawMessage(`{"sp15":"selection"}`),
	}

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return snap, collectedAt, nil
		},
	}, collectedAt)

	require.Equal(t, commands.SourceDaemon, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
	require.NotNil(t, rep.Snapshot)
	require.Equal(t, snap.Mode, rep.Snapshot.Mode)
	require.Equal(t, snap.Counters, rep.Snapshot.Counters)
	require.Equal(t, snap.SpoolFiles, rep.Snapshot.SpoolFiles)
	require.JSONEq(t, string(snap.Extra), string(rep.Snapshot.Extra),
		"an unknown Extra member is passed through, never reinterpreted")
}

// TestStatus_HotPathNames is the SP14-M7-01 gate: no aggregate presented as individual timing.
//
// The daemon keeps ONE hook_controlled histogram covering every hook that reaches it — B-A is
// recorded in recordHotPathSample, which runs on delivery regardless of which hook delivered. So
// there is no per-hook latency to report for six of the seven entry points, and reporting the
// aggregate against each of them would state six measurements that were never taken.
func TestStatus_HotPathNames(t *testing.T) {
	t.Parallel()

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{
				Mode: "active",
				Latency: map[string]obs.HistSnapshot{
					"hook_controlled":     {N: 100, P50: time.Millisecond, P99: 9 * time.Millisecond},
					"checkpoint_finalize": {N: 4, P50: 40 * time.Millisecond, P99: 90 * time.Millisecond},
				},
			}, collectedAt, nil
		},
	}, collectedAt)

	require.NotEmpty(t, rep.Hooks)

	byEvent := make(map[string]commands.HookRow, len(rep.Hooks))
	var order []string
	for _, h := range rep.Hooks {
		byEvent[h.Event] = h
		order = append(order, h.Event)
	}

	// Every §3.4 hook entry point gets a row, in manifest order.
	require.Equal(t, []string{
		"PostToolUse", "UserPromptSubmit", "SessionStart", "PreCompact", "Stop", "SubagentStop", "SessionEnd",
	}, order)

	// PreCompact is the one hook with a histogram of its own, so it is the one that may show a
	// number.
	pre := byEvent["PreCompact"]
	require.Equal(t, commands.AvailabilityOK, pre.Provenance.Status)
	require.NotNil(t, pre.Latency)
	require.Equal(t, "checkpoint_finalize", pre.Latency.Hist)
	require.Equal(t, int64(4), pre.Latency.N)

	// Every other hook is folded into the aggregate, so it reports unavailable with a reason —
	// never the aggregate's own numbers.
	for _, ev := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart", "Stop", "SubagentStop", "SessionEnd"} {
		row := byEvent[ev]
		require.Equal(t, commands.AvailabilityUnavailable, row.Provenance.Status,
			"%s has no per-hook histogram and must not borrow the aggregate's", ev)
		require.Nil(t, row.Latency, "%s must carry no timing at all", ev)
		require.Contains(t, row.Provenance.Reason, "hook_controlled",
			"%s must name the aggregate that covers it", ev)
	}
}

// TestStatus_AggregateRowsDeclareTheirScopeAndMeasure keeps the two honesty labels attached to the
// budget rows: which hooks a row actually mixes, and whether its number was measured or derived.
//
// B-A is recorded as observed + hotPathTailAllowance, so it is an estimate; hook_controlled_
// observed is the strict lower bound that was actually measured. Presenting the first as a
// measurement is the exact substitution the plan's "a configured estimator or rate is never billed
// usage" rule forbids, one layer down.
func TestStatus_AggregateRowsDeclareTheirScopeAndMeasure(t *testing.T) {
	t.Parallel()

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{
				Mode:    "active",
				Latency: map[string]obs.HistSnapshot{"hook_controlled": {N: 10, P99: 8 * time.Millisecond}},
			}, collectedAt, nil
		},
	}, collectedAt)

	byID := make(map[string]commands.BudgetRow, len(rep.Budgets))
	for _, b := range rep.Budgets {
		byID[string(b.ID)] = b
	}
	require.Len(t, byID, len(obs.Budgets()), "every §2.4 budget gets a row")

	ba := byID["B-A"]
	require.True(t, ba.Aggregate)
	require.Equal(t, commands.MeasureEstimated, ba.Measure)
	require.NotEmpty(t, ba.Covers, "an aggregate must name what it mixes")
	require.NotNil(t, ba.Latency)

	require.False(t, byID["B-E"].Aggregate, "B-E measures PreCompact alone")
	require.Equal(t, []string{"PreCompact"}, byID["B-E"].Covers)
	require.True(t, byID["B-F"].Aggregate, "B-F mixes every MCP tool call, so it names no single hook")
	require.Empty(t, byID["B-F"].Covers)

	// B-D is declared in the budget table and observed by nothing in the tree, which is a
	// different answer from "observed zero times so far".
	bd := byID["B-D"]
	require.Equal(t, commands.AvailabilityUnavailable, bd.Provenance.Status)
	require.Nil(t, bd.Latency)
	require.Contains(t, bd.Provenance.Reason, "instrument")
}

// TestStatus_DiskFallbackCarriesItsAge is the second half of the per-hook contract: the same rows
// must work from the persisted metrics file when no daemon answers, and must say how stale they
// are. A disk reading with no age is indistinguishable from a live one.
func TestStatus_DiskFallbackCarriesItsAge(t *testing.T) {
	t.Parallel()

	persisted := collectedAt.Add(-90 * time.Second)
	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{}, time.Time{}, errors.New("dial: no daemon")
		},
		Disk: func(context.Context) (obs.Snapshot, error) {
			return obs.Snapshot{
				TS:       core.UnixMilli(persisted.UnixMilli()),
				Hists:    map[string]obs.HistSnapshot{"checkpoint_finalize": {N: 2, P99: time.Second}},
				Counters: map[string]int64{"l0_admission_daemon": 1},
			}, nil
		},
	}, collectedAt)

	require.Equal(t, commands.SourceDisk, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
	require.NotNil(t, rep.Primary.AgeMS)
	require.Equal(t, int64(90_000), *rep.Primary.AgeMS)
	require.Contains(t, rep.Primary.Reason, "no daemon",
		"the reason the daemon was not used survives into the report")

	byEvent := make(map[string]commands.HookRow, len(rep.Hooks))
	for _, h := range rep.Hooks {
		byEvent[h.Event] = h
	}
	pre := byEvent["PreCompact"]
	require.Equal(t, commands.AvailabilityOK, pre.Provenance.Status)
	require.Equal(t, commands.SourceDisk, pre.Provenance.Source)
	require.NotNil(t, pre.Provenance.AgeMS)
	require.Equal(t, int64(90_000), *pre.Provenance.AgeMS)
}

// TestStatus_LiveAnswerStampedAfterNowIsNotNegativelyAged pins the ordering the real binding
// has: Invocation.Now is read before the body runs, and the daemon's answer is stamped when the
// round trip that follows completes, so a live observation is always a little YOUNGER than the
// report's own clock reading. That must read as an age of 0 — as fresh as the report — and never
// as a negative number, which V5-VERIFY §4.1 observed as `"age_ms": -1` on a real round trip.
func TestStatus_LiveAnswerStampedAfterNowIsNotNegativelyAged(t *testing.T) {
	t.Parallel()

	// One millisecond after now: the smallest gap the millisecond resolution can show.
	answeredAt := collectedAt.Add(time.Millisecond)
	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{
				Latency: map[string]obs.HistSnapshot{"checkpoint_finalize": {N: 1, P99: time.Second}},
			}, answeredAt, nil
		},
	}, collectedAt)

	require.Equal(t, commands.SourceDaemon, rep.Primary.Source)
	require.NotNil(t, rep.Primary.AgeMS, "a live answer has a known age")
	require.Equal(t, int64(0), *rep.Primary.AgeMS, "an answer younger than the report is 0 ms old, not -1")
	for _, h := range rep.Hooks {
		require.NotNil(t, h.Provenance.AgeMS, h.Event)
		require.Equal(t, int64(0), *h.Provenance.AgeMS, h.Event)
	}
	for _, b := range rep.Budgets {
		require.NotNil(t, b.Provenance.AgeMS, b.ID)
		require.Equal(t, int64(0), *b.Provenance.AgeMS, b.ID)
	}
}

// TestStatus_NilDepsUnavailable is the SP14-M7-02 gate: with neither source reachable, every row
// says unavailable and none says zero. A zero-filled report is the failure this whole design is
// arranged against, because a rendered zero and an observed zero look identical.
func TestStatus_NilDepsUnavailable(t *testing.T) {
	t.Parallel()

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{}, collectedAt)

	require.Equal(t, commands.SourceNone, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityUnavailable, rep.Primary.Status)
	require.Nil(t, rep.Snapshot, "an absent snapshot is absent, not an empty one")
	require.Nil(t, rep.Primary.AgeMS, "an unknown age is null, not zero")

	require.NotEmpty(t, rep.Hooks)
	for _, h := range rep.Hooks {
		require.Equal(t, commands.AvailabilityUnavailable, h.Provenance.Status, h.Event)
		require.Nil(t, h.Latency, h.Event)
		require.NotEmpty(t, h.Provenance.Reason, h.Event)
	}
	for _, b := range rep.Budgets {
		require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status, b.ID)
		require.Nil(t, b.Latency, b.ID)
	}
}

// TestStatus_DaemonErrorIsReportedNotSwallowed distinguishes "there is no daemon" from "the daemon
// answered and the answer was broken". Both end in a fallback; only one of them is a defect.
func TestStatus_DaemonErrorIsReportedNotSwallowed(t *testing.T) {
	t.Parallel()

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{}, time.Time{}, errors.New("status refused: mcp not built")
		},
		Disk: func(context.Context) (obs.Snapshot, error) {
			return obs.Snapshot{}, errors.New("open latency.json: file does not exist")
		},
	}, collectedAt)

	require.Equal(t, commands.AvailabilityError, rep.Primary.Status)
	require.Contains(t, rep.Primary.Reason, "mcp not built")
	require.Contains(t, rep.Primary.Reason, "latency.json")
	require.Nil(t, rep.Snapshot)
}

// TestStatus_PanickingDaemonSourceIsRecoveredIntoItsSection is V5-VERIFY I-14.6: status exits 0
// in every case, and a source that panics is one of the cases. The daemon source is bound to real
// transport and decoding code, so a panic there must become the daemon section's own reason —
// carrying the panic value, so the defect is still visible — while the disk fallback proceeds
// exactly as it would after an ordinary error, and the report still renders in full.
func TestStatus_PanickingDaemonSourceIsRecoveredIntoItsSection(t *testing.T) {
	t.Parallel()

	persisted := collectedAt.Add(-30 * time.Second)
	var rep commands.StatusReport
	require.NotPanics(t, func() {
		rep = commands.CollectStatus(context.Background(), commands.StatusSources{
			Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
				panic("assignment to entry in nil map")
			},
			Disk: func(context.Context) (obs.Snapshot, error) {
				return obs.Snapshot{
					TS:    core.UnixMilli(persisted.UnixMilli()),
					Hists: map[string]obs.HistSnapshot{"checkpoint_finalize": {N: 2, P99: time.Second}},
				}, nil
			},
		}, collectedAt)
	}, "a panicking daemon source must not escape CollectStatus")

	// The disk fallback answered, so the report is a disk report, and the daemon section says
	// exactly why it is not a live one.
	require.Equal(t, commands.SourceDisk, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
	require.Nil(t, rep.Snapshot, "a daemon that panicked forwarded no payload")
	require.Contains(t, rep.Primary.Reason, "daemon: panic recovered: assignment to entry in nil map",
		"the panic value is the daemon section's reason, not an anonymous failure")
	require.NotNil(t, rep.Primary.AgeMS)
	require.Equal(t, int64(30_000), *rep.Primary.AgeMS)

	// The other sections still render from the source that did answer.
	byEvent := make(map[string]commands.HookRow, len(rep.Hooks))
	for _, h := range rep.Hooks {
		byEvent[h.Event] = h
	}
	require.Equal(t, commands.AvailabilityOK, byEvent["PreCompact"].Provenance.Status)
	require.Equal(t, commands.SourceDisk, byEvent["PreCompact"].Provenance.Source)
	require.Len(t, rep.Budgets, len(obs.Budgets()))

	var out bytes.Buffer
	require.NoError(t, commands.RenderStatus(&out, rep))
	require.Contains(t, out.String(), "hooks — per-entry-point latency")
	require.Contains(t, out.String(), "budgets — §2.4")
	require.Contains(t, out.String(), "panic recovered")
	_, err := json.Marshal(rep)
	require.NoError(t, err)
}

// TestStatus_PanickingDiskSourceIsRecoveredIntoItsSection is the disk half of I-14.6: with the
// daemon unreachable and the metrics reader panicking, the report is an honest error report — both
// reasons present, the panic value among them, every row unavailable with a reason — and it still
// renders. Nothing is invented for the section that broke.
func TestStatus_PanickingDiskSourceIsRecoveredIntoItsSection(t *testing.T) {
	t.Parallel()

	var rep commands.StatusReport
	require.NotPanics(t, func() {
		rep = commands.CollectStatus(context.Background(), commands.StatusSources{
			Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
				return commands.DaemonStatus{}, time.Time{}, errors.New("dial: no daemon")
			},
			Disk: func(context.Context) (obs.Snapshot, error) {
				panic(errors.New("index out of range [3] with length 3"))
			},
		}, collectedAt)
	}, "a panicking disk source must not escape CollectStatus")

	require.Equal(t, commands.SourceNone, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityError, rep.Primary.Status)
	require.Nil(t, rep.Snapshot)
	require.Nil(t, rep.Primary.AgeMS, "no observation was made, so no age is claimed")
	require.Contains(t, rep.Primary.Reason, "daemon: dial: no daemon")
	require.Contains(t, rep.Primary.Reason, "disk: panic recovered: index out of range [3] with length 3")

	require.NotEmpty(t, rep.Hooks)
	for _, h := range rep.Hooks {
		require.Equal(t, commands.AvailabilityUnavailable, h.Provenance.Status, h.Event)
		require.Nil(t, h.Latency, h.Event)
		require.NotEmpty(t, h.Provenance.Reason, h.Event)
		// PreCompact is the one hook with its own instrument, so it is the one whose emptiness
		// is explained by the sources rather than by the absence of a per-hook histogram.
		if h.Event == "PreCompact" {
			require.Contains(t, h.Provenance.Reason, "disk: panic recovered", h.Event)
		}
	}
	require.Len(t, rep.Budgets, len(obs.Budgets()))
	for _, b := range rep.Budgets {
		require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status, b.ID)
		require.Nil(t, b.Latency, b.ID)
		require.NotEmpty(t, b.Provenance.Reason, b.ID)
	}

	var out bytes.Buffer
	require.NoError(t, commands.RenderStatus(&out, rep))
	require.Contains(t, out.String(), "hooks — per-entry-point latency")
	require.Contains(t, out.String(), "budgets — §2.4")
	require.Contains(t, out.String(), "panic recovered")
}

// TestStatus_BothSourcesPanickingIsStillAReport closes the isolation argument: a panic in the
// daemon source does not skip the disk source, and a panic in both leaves a report with both
// values in it rather than a crash.
func TestStatus_BothSourcesPanickingIsStillAReport(t *testing.T) {
	t.Parallel()

	var rep commands.StatusReport
	require.NotPanics(t, func() {
		rep = commands.CollectStatus(context.Background(), commands.StatusSources{
			Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
				panic("daemon boom")
			},
			Disk: func(context.Context) (obs.Snapshot, error) {
				panic("disk boom")
			},
		}, collectedAt)
	})

	require.Equal(t, commands.AvailabilityError, rep.Primary.Status)
	require.Contains(t, rep.Primary.Reason, "daemon: panic recovered: daemon boom")
	require.Contains(t, rep.Primary.Reason, "disk: panic recovered: disk boom")
}

// TestStatus_ReportIsDeterministic pins ordering, which a golden fixture and a diffed status page
// both depend on.
func TestStatus_ReportIsDeterministic(t *testing.T) {
	t.Parallel()

	src := commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{
				Mode:     "active",
				Counters: map[string]int64{"b": 2, "a": 1, "c": 3},
				Latency:  map[string]obs.HistSnapshot{"hook_controlled": {N: 1}},
			}, collectedAt, nil
		},
	}

	first, err := json.Marshal(commands.CollectStatus(context.Background(), src, collectedAt))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := json.Marshal(commands.CollectStatus(context.Background(), src, collectedAt))
		require.NoError(t, err)
		require.JSONEq(t, string(first), string(again))
		require.Equal(t, string(first), string(again), "byte-identical, not merely equivalent")
	}
}

// TestStatus_ReportSchemaIsVersioned keeps the status.full document readable by the same rule the
// envelope uses: a reader can tell whether it understands what it was handed.
func TestStatus_ReportSchemaIsVersioned(t *testing.T) {
	t.Parallel()

	rep := commands.CollectStatus(context.Background(), commands.StatusSources{}, collectedAt)
	require.Equal(t, commands.StatusSchema, rep.Schema)
	require.Equal(t, collectedAt.UnixMilli(), rep.CollectedAtMS)
}

// TestStatus_APercentileNeverReadsAboveTheMax is the Phase 4 live lane's D9 row: status printed
// "p95 106.50ms p99 106.50ms max 99.00ms". The histogram's percentiles are its containing bucket's
// upper bound (up to ~9% above any sample in it, obs.histogram.Snapshot), while Max is the largest
// sample itself, so a percentile whose bucket holds the max read above it. No percentile of the
// samples can exceed their maximum, so the display clamps to it: the value stays an upper bound on
// the true percentile, and the page stops contradicting itself.
func TestStatus_APercentileNeverReadsAboveTheMax(t *testing.T) {
	t.Parallel()

	snap := obs.HistSnapshot{
		N: 7, P50: 22530 * time.Microsecond, P95: 106500 * time.Microsecond, P99: 106500 * time.Microsecond,
		Max: 99 * time.Millisecond,
	}
	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{Mode: "full", Latency: map[string]obs.HistSnapshot{"hook_controlled": snap}},
				collectedAt, nil
		},
	}, collectedAt)

	var ba *commands.Latency
	for _, b := range rep.Budgets {
		if b.ID == obs.BA {
			ba = b.Latency
		}
	}
	require.NotNil(t, ba)
	require.Equal(t, int64(99_000), ba.MaxUS)
	require.Equal(t, int64(99_000), ba.P95US, "p95 may not read above the max")
	require.Equal(t, int64(99_000), ba.P99US, "p99 may not read above the max")
	require.Equal(t, int64(22_530), ba.P50US, "a percentile below the max is left as measured")
}
