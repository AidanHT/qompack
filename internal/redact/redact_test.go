package redact_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/redact"
	"github.com/stretchr/testify/require"
)

// TestNop pins redact.Nop to its §14.1 contract: input passes through byte-identical, with no
// matches and no rule names — even for input that looks exactly like a secret a real Redactor
// would catch. Unlike New's stub, Nop is real (not a placeholder), so this test runs
// unconditionally — it is never gated behind a Rule W-1 skip.
func TestNop(t *testing.T) {
	r := redact.Nop()
	require.NotNil(t, r)

	require.Empty(t, r.Rules())

	cases := [][]byte{
		nil,
		{},
		[]byte("ordinary text, nothing sensitive here"),
		[]byte("@@SEC_AWS_AKID_ALPHA@@"), // looks exactly like an AWS access key ID
	}
	for _, in := range cases {
		out, matches := r.Redact(in)
		require.Equal(t, in, out, "Nop must pass input through byte-identical")
		require.Empty(t, matches, "Nop must never report a match")
	}
}

// TestNewStub_CurrentlyPassesThrough asserts New(cfg)'s stub behaves the same as Nop today (both
// pass input through untouched, per New's own doc comment on why that is the safe stub
// direction) — but, unlike Nop, New's stub is a placeholder SP-06 replaces; nothing in this
// package derives one implementation from the other, so a future change to one cannot
// accidentally change the other's behaviour.
func TestNewStub_CurrentlyPassesThrough(t *testing.T) {
	r := redact.New(config.Config{})
	require.NotNil(t, r)
	require.Empty(t, r.Rules())

	in := []byte("@@SEC_AWS_AKID_ALPHA@@")
	out, matches := r.Redact(in)
	require.Equal(t, in, out)
	require.Empty(t, matches)
}
