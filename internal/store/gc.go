package store

import "time"

// GCPolicy configures one GC call (00-ARCHITECTURE.md §5.8, §8.2).
//
// GCPolicy carries no default constructor: the retention defaults Qompack.md §8.2 documents (30
// days or 10 sessions, whichever is longer) live in config.StoreCfg.Retention
// (internal/config/defaults.go), and the real GC implementation reads them from the config.Config
// it is opened with rather than from a literal here (D11, §11.6) — GCPolicy itself is simply the
// typed argument shape callers (SessionEnd, idle O3 work) fill in from that config.
type GCPolicy struct {
	// RetainDays and RetainSessions are the retention window: objects unreferenced by any
	// checkpoint, pin, or recent index entry for at least this long are collectible.
	RetainDays, RetainSessions int
	// DryRun reports what GC would collect without collecting it.
	DryRun bool
	// Deadline bounds GC's wall-clock budget; GC must be resumable when it runs out (GCReport.Truncated).
	Deadline time.Duration
}

// GCReport is the outcome of one GC call.
type GCReport struct {
	ScannedObjects, LiveObjects, DeletedObjects int
	BytesFreed                                  int64
	// Roots is the number of GC roots this pass marked from (checkpoints, pins, elimination
	// evidence/depends_on, and recent tool_use/file-version entries).
	Roots    int
	Duration time.Duration
	// Truncated reports whether Deadline cut this pass short before it finished sweeping.
	Truncated bool
}

// Stats summarizes the store's current size and health (00-ARCHITECTURE.md §5.8): what
// `/qompack:status` and the Phase 1 exit-criterion check (DedupRatio ≥ 4:1) both read.
type Stats struct {
	Objects int
	Bytes   int64
	// RawBytes is the total pre-dedup, pre-compression size of everything ever stored.
	RawBytes int64
	// DedupRatio is RawBytes / Bytes — the Phase 1 exit criterion (≥ 4:1).
	DedupRatio                float64
	ToolUses, Segments, Files int
	// Sketches maps each sketch name (tried, touch, explore) to its current size in bytes.
	Sketches map[string]int
}
