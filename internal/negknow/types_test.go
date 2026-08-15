package negknow_test

import (
	"testing"

	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// TestCanonicalize_StubReturnsZeroDescriptor pins Canonicalize's documented stub behaviour (Rule
// 1/2 of the SP-01 stub template): with no closed-form classification algorithm to implement yet,
// it must return the zero Descriptor rather than a plausible-looking fabrication, for ANY input.
func TestCanonicalize_StubReturnsZeroDescriptor(t *testing.T) {
	require.Equal(t, negknow.Descriptor{}, negknow.Canonicalize("", "", ""))
	require.Equal(t, negknow.Descriptor{},
		negknow.Canonicalize("src/auth.ts:refreshToken", "widen pool timeout", "pgbouncer ignores it"))
}

// TestScopeAndStatusConstants_MatchDocumentedWireStrings pins that ScopeSession/ScopeProject and
// StatusActive/StatusStale serialize to exactly the string values 00-ARCHITECTURE.md §5.10
// documents inline ("session" | "project", "active" | "stale") — the same strings the frozen
// elimination_record.jsonl and checkpoint 0001.json fixtures use for "scope" and "status".
func TestScopeAndStatusConstants_MatchDocumentedWireStrings(t *testing.T) {
	require.Equal(t, "session", string(negknow.ScopeSession))
	require.Equal(t, "project", string(negknow.ScopeProject))
	require.Equal(t, "active", string(negknow.StatusActive))
	require.Equal(t, "stale", string(negknow.StatusStale))
}
