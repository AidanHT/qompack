package observer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// SP08-D3, owner decision D35: a prompt that reached only a hook's client spool can be published
// after a prompt its host sent later (the live-vs-spool race D35 rules out of the host-order
// guarantee). Published turns are not re-numbered; instead each prompt record carries its host
// timestamp, and a capture that lands behind a later-sent turn is counted and warned, naming the
// turn it was overtaken by.

// hostOrderLogger records Warn calls, message and fields, and discards the rest.
type hostOrderLogger struct {
	logging.Logger
	warns []hostOrderWarn
}

type hostOrderWarn struct {
	msg string
	kv  []any
}

func (l *hostOrderLogger) Warn(msg string, kv ...any) {
	l.warns = append(l.warns, hostOrderWarn{msg: msg, kv: kv})
}

// field returns the value logged under key, or nil.
func (w hostOrderWarn) field(key string) any {
	for i := 0; i+1 < len(w.kv); i += 2 {
		if k, _ := w.kv[i].(string); k == key {
			return w.kv[i+1]
		}
	}
	return nil
}

func newHostOrderRig(t *testing.T) (*rdxRig, *hostOrderLogger) {
	t.Helper()
	r := newRdxRig(t)
	log := &hostOrderLogger{Logger: logging.Nop()}
	r.o.opt.Log = log
	return r, log
}

// TestPromptHostOrder_RecordCarriesTheHostTimestamp: the worker and drain capture stamp a prompt's
// record with the host timestamp the daemon passes on the context, so its order stays readable
// after publication; an in-process caller that passes none keeps the observer's clock.
func TestPromptHostOrder_RecordCarriesTheHostTimestamp(t *testing.T) {
	r, _ := newHostOrderRig(t)
	ctx := context.Background()
	now := core.UnixMilli(r.clock.Now().UnixMilli())
	host := now - 5000

	_, err := r.o.OnUserPrompt(WithHostTS(WithObservation(ctx, r.sidecar(1, "observe.prompt")), host),
		promptOf("stamped"))
	require.NoError(t, err)
	_, err = r.o.OnUserPrompt(WithObservation(ctx, r.sidecar(2, "observe.prompt")), promptOf("unstamped"))
	require.NoError(t, err)

	rec0, err := r.st.ToolUse(ctx, VerbatimPromptID(testSession, 0))
	require.NoError(t, err)
	require.Equal(t, host, rec0.TS, "a capture given a host timestamp records it")
	rec1, err := r.st.ToolUse(ctx, VerbatimPromptID(testSession, 1))
	require.NoError(t, err)
	require.Equal(t, now, rec1.TS, "a capture given none records the observer's clock, as before")
}

// TestPromptHostOrder_HostEarlierCaptureIsCountedAndNamed: the host's second prompt is published
// first and takes turn 0; the host-first prompt is captured next, at turn 1, and says so. A later
// prompt in host order is not flagged.
func TestPromptHostOrder_HostEarlierCaptureIsCountedAndNamed(t *testing.T) {
	r, log := newHostOrderRig(t)
	ctx := context.Background()
	const firstTS, secondTS, thirdTS core.UnixMilli = 1_000, 1_001, 1_002

	capture := func(arrival uint64, text string, ts core.UnixMilli) {
		t.Helper()
		_, err := r.o.OnUserPrompt(WithHostTS(WithObservation(ctx, r.sidecar(arrival, "observe.prompt")), ts),
			promptOf(text))
		require.NoError(t, err)
	}
	capture(1, "second", secondTS)
	require.Zero(t, r.metrics.Counter(counterPromptOutOfHostOrder).Value(), "turn 0 has nothing to be behind")
	require.Empty(t, log.warns)

	capture(2, "first", firstTS)
	require.Equal(t, int64(1), r.metrics.Counter(counterPromptOutOfHostOrder).Value(),
		"the host-first prompt captured behind a later-sent turn is counted")
	require.Len(t, log.warns, 1, "and warned")
	w := log.warns[0]
	require.Equal(t, string(VerbatimPromptID(testSession, 0)), w.field("substituted_id"),
		"the warning names the turn that was published ahead of it")
	require.Equal(t, string(VerbatimPromptID(testSession, 1)), w.field("id"), "and the capture itself")

	capture(3, "third", thirdTS)
	require.Equal(t, int64(1), r.metrics.Counter(counterPromptOutOfHostOrder).Value(),
		"a prompt later than every published turn is in host order")
	require.Len(t, log.warns, 1)

	// Nothing was re-numbered: turns stay publication order.
	for turn, want := range []string{"second", "first", "third"} {
		rec, err := r.st.ToolUse(ctx, VerbatimPromptID(testSession, core.TurnIndex(turn)))
		require.NoError(t, err)
		require.Equal(t, want, readRoot(ctx, t, r.st, rec.Root))
	}
}

// TestPromptHostOrder_NamesTheEarliestOvertakenTurn: when several turns were published ahead of a
// host-earlier prompt, the warning names the lowest of them, the one whose claim to precede it is
// false first.
func TestPromptHostOrder_NamesTheEarliestOvertakenTurn(t *testing.T) {
	r, log := newHostOrderRig(t)
	ctx := context.Background()
	stamps := []core.UnixMilli{2_000, 3_000, 4_000, 2_500}
	for i, ts := range stamps {
		_, err := r.o.OnUserPrompt(
			WithHostTS(WithObservation(ctx, r.sidecar(uint64(i+1), "observe.prompt")), ts),
			promptOf("p"+string(rune('a'+i))))
		require.NoError(t, err)
	}
	require.Len(t, log.warns, 1)
	require.Equal(t, string(VerbatimPromptID(testSession, 1)), log.warns[0].field("substituted_id"),
		"turns 1 and 2 were sent after the prompt captured at turn 3; turn 1 is named")
}
