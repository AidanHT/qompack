package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
)

// legacyAlreadyTriedResult is the four-string shape clients used before State's additional
// unavailable outcome grew Degraded and the other evidence fields. This checks JSON-shape
// compatibility only: callers with a closed three-state enum need an explicit behavior update.
type legacyAlreadyTriedResult struct {
	State    string `json:"state"`
	Reason   string `json:"reason"`
	Note     string `json:"note"`
	Evidence string `json:"evidence"`
}

// TestAlreadyTriedQueryErrorsReturnUnavailable keeps backend failures in the successful MCP
// domain-response channel. None of these errors establishes absence or an active elimination, and
// their details can contain private storage paths or query context that must not reach a client.
func TestAlreadyTriedQueryErrorsReturnUnavailable(t *testing.T) {
	privateErr := errors.New(`ledger read failed at C:\private\qompack\eliminations.json for token-refresh`)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: core.ErrNotFound},
		{name: "canceled", err: context.Canceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded},
		{name: "private backend detail", err: privateErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			spy := f.withSpyLedger(t)
			spy.Err = tc.err

			var result AlreadyTriedResult
			resp := f.callOK(t, ToolAlreadyTried, map[string]any{
				"target": elimTarget, "approach": elimApproach,
			}, &result)

			require.False(t, resp.IsError, "a query failure is an unavailable domain outcome")
			require.Equal(t, stateUnavailable, result.State)
			require.NotEqual(t, stateAbsent, result.State, "a failed query is never evidence of absence")
			require.NotEqual(t, stateActive, result.State, "a failed query is never an active prohibition")
			require.True(t, result.Degraded)
			require.Equal(t, "elimination ledger unavailable", result.Reason)
			require.Equal(t,
				"Prior attempts are unknown. Retry after ledger recovery; this is not evidence against the approach.",
				result.Note,
			)

			body := responseText(resp)
			require.NotContains(t, body, tc.err.Error(), "backend error details must not reach the client")
			var legacy legacyAlreadyTriedResult
			require.NoError(t, json.Unmarshal([]byte(body), &legacy), "a legacy client must decode the domain result")
			require.Equal(t, stateUnavailable, legacy.State)
		})
	}
}

// TestAlreadyTriedRendersUnavailableCoverage pins the FIRST of the two states SP-20 invariant 8
// added to negknow.Query: a ledger that could not be consulted at all. The ledger reports it with
// a reason and a recovery direction, and the tool boundary must carry both through rather than
// flattening the answer into an absence — an `already_tried` that says "absent" because the log
// would not open is the exact false negative the invariant forbids.
func TestAlreadyTriedRendersUnavailableCoverage(t *testing.T) {
	f := newFixture(t)
	spy := f.withSpyLedger(t)
	spy.Answer = &negknow.Answer{
		State: negknow.AnswerUnavailable,
		Coverage: core.Omission{
			Reason:   "the elimination ledger is in blind mode",
			Recovery: "repair the elimination log and restart",
		},
	}

	var res AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.Equal(t, stateUnavailable, res.State)
	require.NotEqual(t, stateAbsent, res.State, "a ledger that could not be read is not an absence")
	require.Equal(t, "the elimination ledger is in blind mode", res.Reason,
		"the omission's reason must reach the caller verbatim")
	require.Equal(t, "repair the elimination log and restart", res.Note,
		"the omission's recovery direction must reach the caller verbatim")
	require.True(t, res.Degraded, "an unconsultable ledger is a degraded answer")
}

// TestAlreadyTriedRendersUncertainCoverage pins the SECOND state, across all three shapes
// negknow.Query mints it in: a bloom hit resolving to an unrecognized status (Record nil,
// BloomOnly set), a coverage watermark that could not be verified (Record present), and a
// stale-and-dropped match (Record nil). Every one of them must read as `uncertain` with the
// omission's reason and recovery, never as `absent`.
func TestAlreadyTriedRendersUncertainCoverage(t *testing.T) {
	f := newFixture(t)
	id, _ := f.seedElimination(t, negknow.ScopeSession)
	rec, err := f.Ledger.Get(t.Context(), id)
	require.NoError(t, err, "Ledger.Get(%s)", id)

	cov := core.Omission{Reason: "coverage could not be established", Recovery: "retry after the store recovers"}
	for _, tc := range []struct {
		name string
		ans  negknow.Answer
	}{
		{
			name: "unrecognized record status",
			ans:  negknow.Answer{State: negknow.AnswerUncertain, BloomOnly: true, Coverage: cov},
		},
		{
			name: "unverified dependency coverage",
			ans:  negknow.Answer{State: negknow.AnswerUncertain, Record: &rec, Coverage: cov},
		},
		{
			name: "stale match dropped by configuration",
			ans:  negknow.Answer{State: negknow.AnswerUncertain, Coverage: cov},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := f.withSpyLedger(t)
			spy.Answer = &tc.ans

			var res AlreadyTriedResult
			f.callOK(t, ToolAlreadyTried, map[string]any{
				"target": elimTarget, "approach": elimApproach,
			}, &res)

			require.Equal(t, stateUncertain, res.State)
			require.NotEqual(t, stateAbsent, res.State, "uncertainty is never rendered as absence")
			require.NotEqual(t, stateActive, res.State, "uncertainty is never rendered as a prohibition")
			require.Equal(t, cov.Reason, res.Reason)
			require.Equal(t, cov.Recovery, res.Note)
		})
	}
}
