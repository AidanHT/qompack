package contract

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gatedTestClock is this file's own fixed core.Clock: it lives in package contract (white-box, to
// reach the unexported gated), so it cannot use contract_test's newFakeClock.
type gatedTestClock struct{}

func (gatedTestClock) Now() time.Time                  { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
func (gatedTestClock) Since(t time.Time) time.Duration { return gatedTestClock{}.Now().Sub(t) }

// TestGated_ShortCircuitsBeforeCheckRuns is §12.1's central mechanism, tested against the wrapper
// itself rather than against any one assertion: with no producer declared, gated must never invoke
// the check function at all — not "invoke it and discard the result", never invoke it — which a
// counting closure is the only way to prove.
func TestGated_ShortCircuitsBeforeCheckRuns(t *testing.T) {
	const id ID = "test.gated_probe"
	t.Cleanup(ResetProducers)

	var calls int
	a := gated(id, SevCritical, "probe", func(ctx context.Context, e Env) Result {
		calls++
		return Result{OK: false, Severity: SevCritical}
	})

	r := a.Check(context.Background(), Env{Clock: gatedTestClock{}})

	require.Zero(t, calls, "the check body must never run while the producer is undeclared")
	require.True(t, r.OK)
	require.Equal(t, SevInfo, r.Severity)
	require.Equal(t, notYetImplementedObserved, r.Observed)
	require.Equal(t, "probe", r.Expected)

	DeclareProducer(id)
	r = a.Check(context.Background(), Env{Clock: gatedTestClock{}})
	require.Equal(t, 1, calls, "declaring the producer must let the check body run exactly once per call")
	require.False(t, r.OK)
	require.Equal(t, SevCritical, r.Severity)
}

// TestAdditionalContextGatedUntilRehydrator is task-4-spec.md's named pin for the CAdditionalContext
// case specifically: without DeclareProducer(CAdditionalContext), the check body never executes and
// the result is OK/SevInfo/not-yet-implemented; with it declared, the real check runs and can
// observe at SevCritical (its declared severity).
func TestAdditionalContextGatedUntilRehydrator(t *testing.T) {
	t.Cleanup(ResetProducers)

	var calls int
	a := gated(CAdditionalContext, SevCritical, "additionalContext reaches the transcript",
		func(ctx context.Context, e Env) Result {
			calls++
			return Result{OK: false}
		})

	r := a.Check(context.Background(), Env{Clock: gatedTestClock{}})
	require.Zero(t, calls, "the check body must never execute while SP-11 has not declared its producer")
	require.True(t, r.OK)
	require.Equal(t, SevInfo, r.Severity)
	require.Equal(t, notYetImplementedObserved, r.Observed)

	DeclareProducer(CAdditionalContext)
	r = a.Check(context.Background(), Env{Clock: gatedTestClock{}})
	require.Equal(t, 1, calls, "declaring the producer must let the real check run")
	require.False(t, r.OK)
	require.Equal(t, SevCritical, r.Severity, "an unset Severity on failure falls back to the DECLARED severity")
}

// TestPercentileMs pins the p99 arithmetic precompact.has_time_to_write depends on: nearest-rank,
// non-mutating, and well-defined at the boundaries.
func TestPercentileMs(t *testing.T) {
	require.EqualValues(t, 0, percentileMs(nil, 99))

	samples := []int64{100, 200, 300, 400, 500}
	orig := append([]int64(nil), samples...)
	got := percentileMs(samples, 99)
	require.Equal(t, orig, samples, "percentileMs must not mutate its input")
	require.Equal(t, int64(500), got, "p99 of 5 samples is the maximum by nearest-rank")

	require.Equal(t, int64(300), percentileMs([]int64{500, 100, 300, 200, 400}, 50))
}

// TestFirstLine pins the probe-phrase extraction precompact.custom_instructions_accepted uses.
func TestFirstLine(t *testing.T) {
	require.Equal(t, "one line only", firstLine("one line only"))
	require.Equal(t, "first", firstLine("first\nsecond\nthird"))
	require.Equal(t, "", firstLine(""))
}

// TestProbePhrase pins the >= customInstrMinPhraseChars selection rule directly against the
// unexported helper (Important I3): a qualifying first line is returned verbatim; a short one, and
// an empty one (the leading-newline case), report ok=false rather than a phrase bytes.Contains
// could match vacuously.
func TestProbePhrase(t *testing.T) {
	long := "this first line is exactly long enough to qualify"
	require.GreaterOrEqual(t, len(long), customInstrMinPhraseChars)

	phrase, ok := probePhrase(long + "\nsecond line")
	require.True(t, ok)
	require.Equal(t, long, phrase)

	_, ok = probePhrase("too short")
	require.False(t, ok)

	_, ok = probePhrase("\nsecond line is long enough but is not first")
	require.False(t, ok, "an empty first line (leading newline) must never qualify")

	_, ok = probePhrase("")
	require.False(t, ok)
}
