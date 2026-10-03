package eval_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/eval"
)

// evalOrderReads is how many times each row below re-runs one validation of one unchanged input.
// Every input carries at least two problems a validator found by ranging over a Go map. The runtime
// randomizes a small map's iteration by its starting slot, so a two-entry map yields one of its two
// orders far more often than the other: at 20 reads the two-key rows came back green on the
// unfixed code in 2 of 10 runs. If the rarer order comes up about one read in eight, an accidental
// pass at 200 reads is below (7/8)^199, about 3e-12; each read costs microseconds.
const evalOrderReads = 200

// requireSameEveryRead runs read evalOrderReads times and requires every answer to equal the first.
func requireSameEveryRead(t *testing.T, read func() string) string {
	t.Helper()
	first := read()
	for i := range evalOrderReads - 1 {
		require.Equal(t, first, read(), "read %d of one unchanged input differs from the first", i+2)
	}
	return first
}

// TestEvalValidators_NameTheSameProblemEveryRead is D53(a) for `qompack eval`'s input errors: an
// input with two or more problems is rejected with the same message every time, the first problem
// in a fixed order (w20 status audit nit). The validators returned the first bad field of a map
// they ranged over, so one malformed ledger or provenance file drew a different error on each run.
func TestEvalValidators_NameTheSameProblemEveryRead(t *testing.T) {
	errText := func(err error) string {
		t.Helper()
		require.Error(t, err)
		return err.Error()
	}

	t.Run("request money with no currency", func(t *testing.T) {
		r := validRecord()
		r.PricingMode = eval.PricingInvoice
		r.Estimate = &eval.Money{Micros: 1}
		r.Invoice = &eval.Money{Micros: 1}
		got := requireSameEveryRead(t, func() string { return errText(r.Validate()) })
		require.Contains(t, got, "an estimate with no currency")
	})

	t.Run("request reports unknown categories", func(t *testing.T) {
		r := validRecord()
		r.Reported["speculative_decode"] = eval.KnownTokens(1)
		r.Reported["another_unknown"] = eval.KnownTokens(1)
		got := requireSameEveryRead(t, func() string { return errText(r.Validate()) })
		require.Contains(t, got, `"another_unknown"`, "sorted order names the lesser category first")
	})

	t.Run("rate schedule provenance", func(t *testing.T) {
		s := testSchedule()
		s.Model, s.Source = "", ""
		got := requireSameEveryRead(t, func() string { return errText(s.Validate()) })
		require.Contains(t, got, "has no model")
	})

	t.Run("rate schedule multipliers", func(t *testing.T) {
		s := testSchedule()
		s.CacheReadMultiplier, s.CacheWrite1hMultiplier = 0, -1
		got := requireSameEveryRead(t, func() string { return errText(s.Validate()) })
		require.Contains(t, got, "cache read multiplier")
	})

	t.Run("rate schedule prices unknown categories", func(t *testing.T) {
		s := testSchedule()
		s.PerMillion["zz_unknown"] = eval.Money{Micros: 1, Currency: "USD"}
		s.PerMillion["aa_unknown"] = eval.Money{Micros: 1, Currency: "USD"}
		got := requireSameEveryRead(t, func() string { return errText(s.Validate()) })
		require.Contains(t, got, `"aa_unknown"`)
	})

	t.Run("baseline provenance fields", func(t *testing.T) {
		p := loadPhase0Provenance(t)
		require.NoError(t, p.Validate())
		p.Seed, p.Snapshot, p.Date = "", "", ""
		got := requireSameEveryRead(t, func() string { return errText(p.Validate()) })
		require.Contains(t, got, "records no snapshot", "the fields are checked in declaration order")
	})

	t.Run("comparable caveats", func(t *testing.T) {
		p := loadPhase0Provenance(t)
		p.DirtyChanges = ""
		got := requireSameEveryRead(t, func() string {
			ok, notes := eval.Comparable(p, p)
			require.True(t, ok)
			require.Len(t, notes, 2, "both baselines leave the tree state unrecorded")
			return notes[0] + "\n" + notes[1]
		})
		require.Regexp(t, `^the earlier baseline's .*\nthe later baseline's `, got)
	})
}

// TestAccountHostStream_NamesBesideProblemsInModelOrder is D53(a) for the live account's problems:
// a turn in which two models' running totals both fall leaves one "main-loop … exceeds" line per
// model in SessionAccount.Problems, and those lines come in the same (model) order on every read
// (w20 status review nit). beside() ranged over the turn's Delta map, so the two lines swapped from
// run to run while the "decreased" lines before them, already sorted, did not.
func TestAccountHostStream_NamesBesideProblemsInModelOrder(t *testing.T) {
	const modelA, modelB = "model-a", "model-b"
	base := eval.AccountBaseline{ModelUsage: map[string]eval.HostModelUsage{
		modelA: {InputTokens: 100}, modelB: {InputTokens: 100},
	}}
	s := eval.HostStream{Turns: []eval.HostTurn{{
		Index: 0,
		Result: &eval.HostResult{ModelUsage: map[string]eval.HostModelUsage{
			modelA: {InputTokens: 10}, modelB: {InputTokens: 10},
		}},
	}}}
	got := requireSameEveryRead(t, func() string {
		a := eval.AccountHostStream(s, base)
		require.False(t, a.Consistent)
		return strings.Join(a.Problems, "\n")
	})
	require.Regexp(t, `exceeds the turn's model-a running-total change\n.*exceeds the turn's model-b `, got,
		"the beside lines are in model order")
}
