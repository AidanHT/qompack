//go:build windows

package hostperm

import (
	"path/filepath"
	"strings"
)

// shortName is the 8.3 name the volume records for root's entry name, or "" when it records none.
func shortName(root, name string) string {
	s, ok := getShortPathName(filepath.Join(root, name))
	if !ok {
		return ""
	}
	if base := filepath.Base(s); !strings.EqualFold(base, name) {
		return base
	}
	return ""
}
