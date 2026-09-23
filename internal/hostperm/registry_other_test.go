//go:build !windows

package hostperm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A registry source named where there is no registry is unreadable, not absent: a caller that
// named one expected policy to be there.
func TestRegistrySourceOffWindowsFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	e.pol.o.Managed.Registry = []RegistryValue{{Hive: "HKLM", Key: policyKey, Name: "Settings"}}
	_, err := e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
	_, err = readRegistry(RegistryValue{}, nil)
	require.Error(t, err)
}
