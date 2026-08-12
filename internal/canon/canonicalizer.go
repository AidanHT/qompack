package canon

// Canonicalizer strips one class of volatile content from one tool's output
// (00-ARCHITECTURE.md §5.6). Canonicalize MUST be idempotent:
// Canonicalize(Canonicalize(x).Canonical, o) == Canonicalize(x, o).
//
// This package declares the interface only: the twelve concrete canonicalizers §5.6 names (crlf,
// ansi, timestamps, durations, pids, addresses, tmpPaths, then per-tool bash, testrunner, grep,
// glob, fileread, webfetch, git) are SP-04's implementation, not SP-01's stub.
type Canonicalizer interface {
	// Name identifies this canonicalizer; Registry.Register errors on a duplicate Name.
	Name() string
	// Applies reports whether this canonicalizer should run for tool's output at path.
	Applies(tool, path string) bool
	// Canonicalize strips this canonicalizer's class of content from in.
	Canonicalize(in []byte, o Options) (Result, error)
}
