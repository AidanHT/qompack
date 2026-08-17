package contract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// TestDeclareProducer_IsIdempotentAndScoped asserts DeclareProducer can be called any number of
// times for the same ID without error, that HasProducer answers only for what was actually
// declared, and that ResetProducers clears it back to the fresh-build state.
func TestDeclareProducer_IsIdempotentAndScoped(t *testing.T) {
	t.Cleanup(contract.ResetProducers)

	require.False(t, contract.HasProducer(contract.CMCPRegistered))

	contract.DeclareProducer(contract.CMCPRegistered)
	contract.DeclareProducer(contract.CMCPRegistered) // idempotent: must not panic or error
	require.True(t, contract.HasProducer(contract.CMCPRegistered))
	require.False(t, contract.HasProducer(contract.CAdditionalContext),
		"declaring one producer must not mark every ID as declared")

	contract.ResetProducers()
	require.False(t, contract.HasProducer(contract.CMCPRegistered), "ResetProducers must clear every declaration")
}
