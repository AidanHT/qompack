package store

import (
	"time"

	"github.com/qompack/qompack/internal/core"
)

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
	// Deadline bounds the mark's hash harvest and the sweep. The tombstone phase and the mark's
	// in-memory index walks answer only to ctx, so a pass may overshoot the deadline by the
	// tombstone phase's cost (SP06-D1, adjudicated wontfix at V3-VERIFY: 100-260 ms measured at
	// 650 dead roots). GC must be resumable when it runs out (GCReport.Truncated).
	Deadline time.Duration
	// QuotaBytes is the maximum on-disk object size this store may keep, in bytes. Zero means no
	// quota; a negative value disables one explicitly, which reads the same but says so.
	//
	// A quota is enforced ONLY against roots that are live by the retention window alone. It can
	// never collect through a lease, pending write, checkpoint, pin, evidence reference, delta
	// base or rollback record (SP-20 invariant 9) - a quota that cannot be met that way is
	// reported as GCReport.QuotaExceeded with unsafe-to-collect outcomes naming what blocked it,
	// never satisfied by dropping something a producer still needs.
	//
	// It lives here rather than in config.StoreCfg because Appendix C's key set is frozen and its
	// golden reproduces it verbatim; GCPolicy is the argument shape 5.8 gives callers to fill in
	// from configuration, and a future authorized Appendix C revision would add store.quota.* and
	// map it onto this field without changing anything below.
	QuotaBytes int64
	// MaxOutcomes bounds GCReport.Outcomes. Zero means defaultMaxOutcomes; a negative value asks
	// for no per-root list at all. The COUNTERS are always exact regardless - capping the list
	// must never turn a decision into a silent one.
	MaxOutcomes int
}

// defaultMaxOutcomes bounds the per-root outcome list a report carries when a caller names no
// limit. A 50 000-root store must not materialize 50 000 records to answer "what did this pass
// decide"; the counters answer that, and the list is the sample that explains it.
//
// D11/§11.6 flags the value because store.chunk.min's own default happens to be 1024 too, but the
// set is values, not meanings: this is a count of report rows and no configuration key owns it, so
// there is no default for it to silently diverge from. Reading chunk.min here to satisfy the lint
// would MANUFACTURE the coupling D11 exists to prevent. GCOptions.MaxOutcomes is the seam a caller
// uses to change it.
const defaultMaxOutcomes = 1024 //nomagic:allow a report-row count, not store.chunk.min (§11.6)

// RootResult is one root's explicit lifecycle outcome for one GC pass (SP-20 M1-03: "quotas and
// expiry are visible policy outcomes").
type RootResult string

const (
	// RootRetained means the pass kept this root, for the reason the outcome names.
	RootRetained RootResult = "retained"
	// RootExpired means the retention window no longer covers this root and the pass retired it.
	RootExpired RootResult = "expired"
	// RootQuotaEvicted means the root was still inside the retention window and was retired only
	// to bring the store back under its size quota.
	RootQuotaEvicted RootResult = "quota_evicted"
	// RootUnsafeToCollect means the pass wanted this root's space and could not have it: a lease,
	// pending write, checkpoint, pin, evidence reference, delta base or rollback record holds it.
	RootUnsafeToCollect RootResult = "unsafe_to_collect"
)

// RootOutcome is one root's decision, with the reason that produced it. A drop is never silent:
// every retired root is either an expiry or a quota eviction, and both say so here.
type RootOutcome struct {
	Root   core.Hash
	Result RootResult
	// Class is the retention class that produced a retained or unsafe outcome, where one applies.
	Class RetentionClass
	// Reason is human-readable and always populated.
	Reason string
	// Bytes is the canonical size of the root the outcome is about, as an estimate of what the
	// decision costs or frees. It is not the compressed on-disk size.
	Bytes int64
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
	// -- explicit per-root outcomes (SP-20 M1-03) --

	// Retained, Expired, QuotaEvicted and Unsafe are exact counts of every root this pass decided
	// about. They stay exact even when Outcomes is capped.
	Retained, Expired, QuotaEvicted, Unsafe int
	// Outcomes is the per-root decision list, bounded by GCPolicy.MaxOutcomes.
	Outcomes []RootOutcome
	// OutcomesTruncated reports that Outcomes is a bounded sample, not the whole decision set.
	OutcomesTruncated bool
	// QuotaBytes is the quota this pass enforced, 0 when none.
	QuotaBytes int64
	// QuotaBytesBefore is the store's on-disk object size when the pass started.
	QuotaBytesBefore int64
	// QuotaTargetBytes is the estimated size the quota evictions aimed to free.
	QuotaTargetBytes int64
	// QuotaExceeded reports that the store is still over quota after evicting everything it was
	// safe to evict. It is the visible outcome of a quota that cannot be met.
	QuotaExceeded bool
	// PendingWrites is the number of pending-write markers that held content live this pass.
	PendingWrites int
	// PendingExpired is the number of abandoned markers this pass retired by age.
	PendingExpired int
	// RetentionRootsError reports that a retention-root source failed, so this pass deliberately
	// collected nothing: an unreadable lease set is indistinguishable from a full one.
	RetentionRootsError bool
	// RetentionRootsShed is how many duplicate lines this pass compacted out of
	// retention-roots.jsonl. The file gains a line per declaration and removes nothing, so without
	// a compaction it grows once per delivery forever; the number is how much of that growth was
	// pure repetition. Zero means the file was already the set it declares, or that the pass did
	// not reach the compaction (a dry run, a truncated sweep, or an append that raced it).
	RetentionRootsShed int
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
