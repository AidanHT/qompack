package paths_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestIsHome_ACaseVariantOfHomeIsHomeOnWindows is D18's case-insensitivity row. NTFS names are
// case-insensitive, so C:\USERS\ME and c:\users\me are one directory and either spelling of the
// home directory is refused. It lives in a Windows-only file rather than behind a skip: on Linux a
// case variant is a different path, which TestIsHome_EverythingElseIsNotHome's rows already cover.
func TestIsHome_ACaseVariantOfHomeIsHomeOnWindows(t *testing.T) {
	home := fakeHome(t)

	require.True(t, paths.IsHome(strings.ToUpper(home), home), "an upper-case spelling of home")
	require.True(t, paths.IsHome(home, strings.ToLower(home)), "a lower-case home candidate")
}
