package store

import (
	"time"

	"github.com/qompack/qompack/internal/core"
)

// Query is one Search call's parameters (00-ARCHITECTURE.md §5.8): it backs the `recall`
// retrieval tool.
type Query struct {
	// Text is free-text to search for.
	Text string
	// Path restricts the search by path. A plain path matches a record's paths.Key-form path by
	// equality, by path-segment suffix, or by containment; one carrying a glob metacharacter
	// (`*`, `?`, `[`) is a path.Match pattern on slash paths, matched against the whole path and
	// against every path-segment suffix of it. A malformed pattern is ErrBadPathGlob.
	Path string
	// Symbol restricts the search to a specific symbol name.
	Symbol string
	// Tool restricts the search to results produced by a specific tool, named either the host's
	// way (Read) or by the display name the index records it under (FileRead), case-insensitively.
	Tool string
	// Since restricts the search to results at or after this time.
	Since time.Time
	// K bounds the number of Hits returned.
	K int
}

// Hit is one Search result.
type Hit struct {
	Root      core.Hash
	ToolUseID core.ToolUseID
	Path      string
	Tool      string
	TS        core.UnixMilli
	// Score is this hit's relevance score.
	Score float64
	// Summary is a short, human-readable description of this hit.
	Summary string
	// Span is the [start, end) byte range within Root that this hit's content occupies —
	// the minimum sufficient span backing retrieval.defaultSpan = "minimal" (00-ARCHITECTURE.md
	// §8.7).
	Span [2]int64
}
