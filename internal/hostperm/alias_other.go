//go:build !windows

package hostperm

// osAlias reports no alias: a POSIX filesystem opens a path under the spelling it is given, apart
// from the case folding and the symlinks Evaluate already handles.
func osAlias(string) string { return "" }
