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
	// Path restricts the search to a paths.Key-form path, or a prefix of one.
	Path string
	// Symbol restricts the search to a specific symbol name.
	Symbol string
	// Tool restricts the search to results produced by a specific tool.
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
