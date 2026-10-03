//go:build !windows

package hostperm

// shortName reports none: a POSIX filesystem records no 8.3 names.
func shortName(string, string) string { return "" }
