//go:build !windows && !linux && !darwin

package paths

import "errors"

// RenameDirectoryNoReplace refuses unsupported platforms without a racy fallback.
func RenameDirectoryNoReplace(_, _ string) error {
	return errors.ErrUnsupported
}
