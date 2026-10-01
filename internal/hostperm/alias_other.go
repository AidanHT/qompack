//go:build !windows

package hostperm

// osAlias reports no alias: a POSIX filesystem opens a path under the spelling it is given, apart
// from the case folding and the symlinks Evaluate already handles. No name there is an alias that
// fails to resolve, so unresolved is always false.
func osAlias(string) (alias string, unresolved bool) { return "", false }
