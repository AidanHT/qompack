package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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
