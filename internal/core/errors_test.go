package core_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestSentinels_AreDistinct(t *testing.T) {
	sentinels := map[string]error{
		"ErrNotImplemented": core.ErrNotImplemented,
		"ErrNotFound":       core.ErrNotFound,
		"ErrAppendOnly":     core.ErrAppendOnly,
		"ErrAlreadyEncoded": core.ErrAlreadyEncoded,
		"ErrBudget":         core.ErrBudget,
		"ErrDegraded":       core.ErrDegraded,
		"ErrContract":       core.ErrContract,
	}
	require.Len(t, sentinels, 7, "§4 fixes exactly seven sentinels")

	for an, a := range sentinels {
		require.Error(t, a)
		for bn, b := range sentinels {
			if an == bn {
				continue
			}
			require.False(t, errors.Is(a, b), "%s must not match %s", an, bn)
		}
	}
}

func TestIsNotImplemented_UnwrapsWrapping(t *testing.T) {
	require.True(t, core.IsNotImplemented(core.ErrNotImplemented))
	require.True(t, core.IsNotImplemented(fmt.Errorf("store.Put: %w", core.ErrNotImplemented)))
	require.True(t, core.IsNotImplemented(fmt.Errorf("outer: %w",
		fmt.Errorf("inner: %w", core.ErrNotImplemented))))

	require.False(t, core.IsNotImplemented(nil))
	require.False(t, core.IsNotImplemented(core.ErrNotFound))
	require.False(t, core.IsNotImplemented(errors.New("not implemented")),
		"a look-alike message must not satisfy the probe — conformance suites gate on this")
}

func TestVersion_IsSet(t *testing.T) {
	require.NotEmpty(t, core.Version, "overridden at link time by SP-17, never empty")
}
