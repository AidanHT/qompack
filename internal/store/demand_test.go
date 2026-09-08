package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// newDemandLog opens a demand log over a fresh project.
func newDemandLog(t *testing.T) (*store.DemandLog, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	return store.OpenDemandLog(root), root
}

// obs is one observation, so a test can say what happened in one line.
func obs(kind store.DemandKind, key string, ts int64) store.DemandObservation {
	return store.DemandObservation{Kind: kind, Key: key, TS: core.UnixMilli(ts)}
}

// TestDemand_FrequencyIsNotUsefulness is the reason this file exists. Content asked for many times
// that never helped must not read as valuable, and the type offers no way to make it.
func TestDemand_FrequencyIsNotUsefulness(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	for i := 0; i < 50; i++ {
		require.NoError(t, l.Record(obs(store.DemandRequested, "loop", int64(i))))
	}
	for i := 0; i < 50; i++ {
		require.NoError(t, l.Record(obs(store.DemandFailed, "loop", int64(i))))
	}
	require.NoError(t, l.Record(obs(store.DemandRequested, "gold", 1)))
	require.NoError(t, l.Record(obs(store.DemandUseful, "gold", 2)))

	agg, err := l.Aggregate()
	require.NoError(t, err)

	loop, gold := agg["loop"], agg["gold"]
	require.Equal(t, 50, loop.Requests)
	require.Equal(t, 1, gold.Requests)

	loopRate, known := loop.Usefulness()
	require.True(t, known)
	require.Zero(t, loopRate, "fifty requests that never helped are worth nothing")

	goldRate, known := gold.Usefulness()
	require.True(t, known)
	require.Equal(t, 1.0, goldRate, "one request that helped is worth everything it was asked for")
}

// TestDemand_UnknownIsNotZero pins the two-value Usefulness contract: a key nobody instrumented has
// an unknown rate, and there is no accessor that hands a caller the bare float.
func TestDemand_UnknownIsNotZero(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	require.NoError(t, l.Record(obs(store.DemandRequested, "unmeasured", 1)))
	require.NoError(t, l.Record(obs(store.DemandRequested, "unmeasured", 2)))

	agg, err := l.Aggregate()
	require.NoError(t, err)

	rate, known := agg["unmeasured"].Usefulness()
	require.False(t, known, "nobody looked; that is not the same as it never helping")
	require.Zero(t, rate)

	// The zero Demand is unknown too, so an aggregate over a key that does not exist cannot
	// masquerade as a measured zero.
	rate, known = store.Demand{}.Usefulness()
	require.False(t, known)
	require.Zero(t, rate)
}

// TestDemand_UsefulnessDenominatorIsOutcomesNotRequests pins that thin instrumentation lowers
// confidence rather than the rate: a key with two outcomes out of forty requests reports the rate
// its outcomes support, and Instrumented is what says the rate covers only part of the traffic.
func TestDemand_UsefulnessDenominatorIsOutcomesNotRequests(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	for i := 0; i < 40; i++ {
		require.NoError(t, l.Record(obs(store.DemandRequested, "thin", int64(i))))
	}
	require.NoError(t, l.Record(obs(store.DemandUseful, "thin", 100)))
	require.NoError(t, l.Record(obs(store.DemandUseful, "thin", 101)))

	agg, err := l.Aggregate()
	require.NoError(t, err)
	d := agg["thin"]

	rate, known := d.Usefulness()
	require.True(t, known)
	require.Equal(t, 1.0, rate, "the two outcomes we have both say useful")
	require.False(t, d.Instrumented(), "but they cover two of forty requests, and the report must say so")

	// A fully instrumented key reports so.
	require.NoError(t, l.Record(obs(store.DemandRequested, "full", 1)))
	require.NoError(t, l.Record(obs(store.DemandUseful, "full", 2)))
	agg, err = l.Aggregate()
	require.NoError(t, err)
	require.True(t, agg["full"].Instrumented())
}

// TestDemand_DistinctTouchesAreNotASecondRequestCounter pins the deduplication: two requests inside
// one turn are one touch, which is what separates "wanted by the work" from "asked twice".
func TestDemand_DistinctTouchesAreNotASecondRequestCounter(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	for _, touch := range []string{"turn-1", "turn-1", "turn-1", "turn-2"} {
		require.NoError(t, l.Record(store.DemandObservation{
			Kind: store.DemandTouched, Key: "k", Touch: touch, TS: 1,
		}))
	}

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 2, agg["k"].DistinctTouches)
	require.Zero(t, agg["k"].Requests, "a touch is not a request")
}

// TestDemand_GapsAreCountedNotSmoothedOver pins the missing-telemetry rule, including for a record
// whose kind this build cannot interpret: an observation that exists and cannot be counted is
// still evidence that something happened.
func TestDemand_GapsAreCountedNotSmoothedOver(t *testing.T) {
	t.Parallel()

	l, root := newDemandLog(t)
	require.NoError(t, l.Record(obs(store.DemandRequested, "k", 1)))
	require.NoError(t, l.Record(obs(store.DemandGap, "k", 2)))

	// A kind from a later build, written straight to the file: Record itself refuses to mint one.
	f, err := os.OpenFile(store.DemandPath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"v":1,"kind":"telepathy","key":"k","ts":3}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 2, agg["k"].TelemetryGaps,
		"the explicit gap and the uninterpretable record both count")
	require.Equal(t, 1, agg["k"].Requests)
	require.False(t, agg["k"].Instrumented())
}

// TestDemandLog_RefusesToMintAKindItCannotReadBack pins the asymmetry: this build will not write a
// kind it does not know, even though it tolerates reading one.
func TestDemandLog_RefusesToMintAKindItCannotReadBack(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	require.Error(t, l.Record(obs("telepathy", "k", 1)))
	require.Error(t, l.Record(obs(store.DemandRequested, "", 1)), "an observation needs a key")

	for _, k := range []store.DemandKind{
		store.DemandRequested, store.DemandTouched, store.DemandUseful,
		store.DemandFailed, store.DemandGap,
	} {
		require.True(t, k.Valid(), string(k))
		require.NoError(t, l.Record(obs(k, "k", 1)))
	}
	require.False(t, store.DemandKind("").Valid(), "a blank kind is not a default")
}

// TestDemand_RecoveryCostIsTheMaximumNotTheMean pins that unmeasured records cannot make an
// expensive thing look cheap.
func TestDemand_RecoveryCostIsTheMaximumNotTheMean(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	require.NoError(t, l.Record(store.DemandObservation{
		Kind: store.DemandRequested, Key: "k", TS: 1, RecoveryCostMs: 9000,
	}))
	for i := 0; i < 20; i++ {
		require.NoError(t, l.Record(obs(store.DemandRequested, "k", int64(i+2))))
	}

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, int64(9000), agg["k"].MaxRecoveryCostMs,
		"twenty unmeasured records must not average an expensive span down to cheap")
}

// TestDemandLog_AnUnreadableLineIsAGapNotAFailure pins that one bad line costs its own record and
// nothing else: an append-only evidence log is not discarded because part of it is damaged.
func TestDemandLog_AnUnreadableLineIsAGapNotAFailure(t *testing.T) {
	t.Parallel()

	l, root := newDemandLog(t)
	require.NoError(t, l.Record(obs(store.DemandRequested, "good", 1)))

	f, err := os.OpenFile(store.DemandPath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("not json at all\n\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, l.Record(obs(store.DemandUseful, "good", 2)))

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Equal(t, 1, agg["good"].Requests)
	require.Equal(t, 1, agg["good"].Useful, "records after the bad line are still read")
	require.Equal(t, 1, agg[""].TelemetryGaps, "the damage is recorded, not discarded")
}

// TestDemandLog_AMissingFileIsNotAnError pins that a project where nothing has been observed
// aggregates to nothing, and that an unused gated feature leaves no file behind.
func TestDemandLog_AMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	l := store.OpenDemandLog(root)

	agg, err := l.Aggregate()
	require.NoError(t, err)
	require.Empty(t, agg)

	_, statErr := os.Stat(l.Path())
	require.True(t, os.IsNotExist(statErr), "opening the log creates nothing")

	require.Equal(t, filepath.Join(root, ".qompack", "state", "demand.jsonl"), store.DemandPath(root))
}

// TestDemandLog_ReportKeepsTheTotalsApartAndSaysWhatIsUnmeasured pins the headline evidence:
// a report over mostly uninstrumented keys is a frequency report, and it has to say so.
func TestDemandLog_ReportKeepsTheTotalsApartAndSaysWhatIsUnmeasured(t *testing.T) {
	t.Parallel()

	l, _ := newDemandLog(t)
	require.NoError(t, l.Record(obs(store.DemandRequested, "a", 1)))
	require.NoError(t, l.Record(obs(store.DemandUseful, "a", 2)))
	require.NoError(t, l.Record(obs(store.DemandRequested, "b", 3)))
	require.NoError(t, l.Record(obs(store.DemandRequested, "c", 4)))
	require.NoError(t, l.Record(obs(store.DemandFailed, "c", 5)))
	require.NoError(t, l.Record(obs(store.DemandGap, "c", 6)))

	rep, err := l.Report()
	require.NoError(t, err)
	require.Equal(t, 3, rep.Requests)
	require.Equal(t, 1, rep.Useful)
	require.Equal(t, 1, rep.Failed)
	require.Equal(t, 1, rep.TelemetryGaps)
	require.Equal(t, 1, rep.UninstrumentedKeys, "b has no outcome telemetry at all")

	require.Len(t, rep.Keys, 3)
	require.Equal(t, []string{"a", "b", "c"},
		[]string{rep.Keys[0].Key, rep.Keys[1].Key, rep.Keys[2].Key},
		"sorted, so two reports of one log compare equal")
}
