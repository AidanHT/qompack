package canon

import "github.com/qompack/qompack/internal/config"

// Default returns a Registry pre-populated with the fourteen built-in canonicalizers, registered
// in the order 00-ARCHITECTURE.md §5.6 fixes: crlf, ansi, timestamps, durations, pids, addresses,
// tmpPaths, then the per-tool passes bash, testrunner, grep, glob, fileread, webfetch, git.
//
// Registration order is not cosmetic. It is the tie-breaker Registry.Run uses when two
// canonicalizers claim the same span at the same length, and — because composition is a single
// pass over the original input rather than a transform chain — it is also what Result.Applied
// means by "application order".
//
// When cfg.Enabled is false the registry contains ONLY crlf. CRLF→LF normalization is structural
// per 00-ARCHITECTURE.md §4, not one of Appendix C's six optional strip classes: turning it off
// would fork the dedup space between a Windows and a POSIX read of the same file, so that a
// project which disabled canonicalization would silently store every shared file twice. Disabling
// canonicalization means "do not strip volatile content", never "do not normalize line endings".
func Default(cfg config.CanonicalizeCfg) Registry {
	r := NewRegistry()
	for _, c := range builtins() {
		if !cfg.Enabled && c.Name() != nameCRLF {
			continue
		}
		// Register can only fail on a duplicate Name, and builtins is a fixed table whose names
		// TestDefault_Names pins; there is no runtime condition that could make this fail.
		_ = r.Register(c)
	}
	return r
}
