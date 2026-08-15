package dag

import (
	"fmt"
	"strconv"

	"github.com/qompack/qompack/internal/core"
)

// GraphStats summarizes a Graph's current state (00-ARCHITECTURE.md §5.9's Graph.Stats()
// references this type without defining it; dag defines it here).
//
// The json tags exist because SP-14 renders this struct directly for `/qompack:status --json`.
// NodesByKind and EdgesByKind are deliberately keyed by the TEXT name — "tool_use", "shared_file"
// — rather than by the numeric NodeKind/EdgeKind, so that the emitted JSON is readable by a human
// reading a status dump, and so that adding a kind cannot silently renumber someone's dashboard.
//
// The counters split into two groups with different lifetimes, which matters when reading them:
// Nodes, Edges, NodesByKind, EdgesByKind, Tombstoned, Dangling, MaxPos, PendingRecords and
// NeedsCompaction are recomputed from live in-memory state on every Stats() call, while
// LogRecords, LogBytes, Generation, LastCompaction, LoadErrors and TruncatedTail are carried
// forward from Open and mutated only by Flush and Compact.
type GraphStats struct {
	// Nodes is the count of live (non-tombstoned) nodes.
	Nodes int `json:"nodes"`
	// Edges is the count of stored edges, including dangling ones.
	Edges int `json:"edges"`
	// NodesByKind counts live nodes per NodeKind, keyed by NodeKind.String().
	NodesByKind map[string]int `json:"nodes_by_kind"`
	// EdgesByKind counts edges per EdgeKind, keyed by EdgeKind.String().
	EdgesByKind map[string]int `json:"edges_by_kind"`
	// Tombstoned is the count of nodes marked dead but not yet reclaimed by Compact.
	Tombstoned int `json:"tombstoned"`
	// Dangling is the count of edges at least one of whose endpoints is absent or tombstoned.
	// Dangling edges are legal (the log may interleave, and an observer may emit an edge before
	// its endpoint); they are simply skipped by traversal and by CrossingEdges.
	Dangling int `json:"dangling"`
	// PendingRecords is the count of records mutated in memory but not yet appended to the log.
	PendingRecords int `json:"pending_records"`
	// LogRecords is the count of records currently on disk in dag/deps.jsonl.
	LogRecords int `json:"log_records"`
	// LogBytes is the on-disk size of dag/deps.jsonl.
	LogBytes int64 `json:"log_bytes"`
	// Generation is the compaction generation; 0 means the log has never been compacted.
	Generation int `json:"generation"`
	// MaxPos is the highest Pos of any live node, so a caller can tell whether a candidate cut
	// point lies inside the graph at all.
	MaxPos int `json:"max_pos"`
	// LastCompaction is when Compact last rewrote the log; 0 means never.
	LastCompaction core.UnixMilli `json:"last_compaction"`
	// LoadErrors is the count of records Open skipped because they failed to decode. A non-zero
	// value is degradation, not failure: the graph loaded, but it is missing something.
	LoadErrors int `json:"load_errors"`
	// TruncatedTail reports that the log's final line was incomplete and was discarded — the
	// expected artifact of a crash mid-append, not corruption.
	TruncatedTail bool `json:"truncated_tail"`
	// NeedsCompaction reports whether the log has accumulated enough waste to be worth rewriting.
	// It is advisory: Compact re-checks the same condition itself and no-ops when it does not hold.
	NeedsCompaction bool `json:"needs_compaction"`
}

// The decimal byte units GraphStats.String renders log sizes in. They are decimal rather than
// binary so that "log=1.2MB" means what a reader checking `ls -l` expects it to mean.
const (
	bytesPerKB = 1000
	bytesPerMB = bytesPerKB * 1000
	bytesPerGB = bytesPerMB * 1000
)

// String renders one log line, for example:
//
//	dag: nodes=812 edges=2104 dangling=3 tombstoned=0 log=1.2MB gen=2
//
// SP-14 renders the JSON form for `/qompack:status`; this package renders nothing else.
func (s GraphStats) String() string {
	return fmt.Sprintf("dag: nodes=%d edges=%d dangling=%d tombstoned=%d log=%s gen=%d",
		s.Nodes, s.Edges, s.Dangling, s.Tombstoned, humanBytes(s.LogBytes), s.Generation)
}

// humanBytes renders n as a short decimal size: bare bytes below 1KB, and one decimal place
// above it ("1.2MB"). A negative n cannot arise from a file size and is rendered verbatim rather
// than being clamped, so a bookkeeping bug shows up in the log instead of being hidden by it.
func humanBytes(n int64) string {
	switch {
	case n < 0:
		return strconv.FormatInt(n, 10) + "B"
	case n < bytesPerKB:
		return strconv.FormatInt(n, 10) + "B"
	case n < bytesPerMB:
		return fmt.Sprintf("%.1fKB", float64(n)/bytesPerKB)
	case n < bytesPerGB:
		return fmt.Sprintf("%.1fMB", float64(n)/bytesPerMB)
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/bytesPerGB)
	}
}
