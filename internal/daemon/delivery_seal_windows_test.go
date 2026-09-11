//go:build windows

package daemon

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireNoOpenHandle asserts that no handle on p is open, by opening p with no sharing at all:
// CreateFile then fails with ERROR_SHARING_VIOLATION while any other handle with read, write or
// delete access is open, whatever share mode that handle granted. A leaked seal handle, which has
// read and write access, fails it. p must exist.
func requireNoOpenHandle(t *testing.T, p string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(p)
	require.NoError(t, err)
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err, "a handle on %s is still open", p)
	require.NoError(t, syscall.CloseHandle(h))
}
