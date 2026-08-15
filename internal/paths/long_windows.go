//go:build windows

package paths

import (
	"path/filepath"
	"strings"
)

// Long returns the \\?\-prefixed, cleaned, absolute form of p once its absolute length reaches
// longPathThreshold characters — the point past which Windows' legacy MAX_PATH machinery starts
// rejecting CreateFile/MoveFile calls that would otherwise succeed. Below the threshold, and for
// a p that already carries a \\?\ prefix, Long returns p unchanged: it is the identity function
// in every case that does not need the workaround. UNC paths (\\server\share\...) get the
// \\?\UNC\... form instead of a bare \\?\ prefix, per Windows' own convention.
//
// Every os.OpenFile, os.Stat and os.Rename call this package makes routes its path arguments
// through Long, which is what lets checkpoints, pins and every other .qompack artifact live at
// an arbitrary depth without the project silently failing to write a file once a user's
// directory nesting gets deep enough.
func Long(p string) string {
	if p == "" || strings.HasPrefix(p, longPrefix) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	abs = filepath.Clean(abs)
	if len(abs) < longPathThreshold {
		return p
	}
	if strings.HasPrefix(abs, `\\`) {
		return longUNCPrefix + strings.TrimPrefix(abs, `\\`)
	}
	return longPrefix + abs
}
