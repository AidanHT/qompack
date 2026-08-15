// Package store implements the L1 content-addressed store of 00-ARCHITECTURE.md §5.8: the
// zstd-compressed object store (objects/), the tool_use/roots/segments indexes, per-path file
// version history, search (backing the `recall` retrieval tool), the append-only segment log, and
// reference-counted garbage collection. SP-06 owns the real implementation.
//
// store may import ONLY chunk, canon, sketch, symbols, redact and tokens, plus the foundation
// packages core, paths, config, logging and obs (00-ARCHITECTURE.md §3.2's store allow-set). In
// particular it must NEVER import negknow: negknow depends on store (§5.10), not the reverse, and
// that is precisely why Store.ChangedSince takes []core.Dep rather than []negknow.Dep — negknow.Dep
// is itself only an alias of core.Dep, so store never needs to know the negknow package exists.
//
// SP-01 ships the complete §5.8 type set — Root, PutOptions, PutResult, NearDupInfo, Supersession,
// ToolUseRecord, FileVersion, the Store interface (21 methods), Query, Hit, Segment, the SegmentLog
// interface (8 methods), GCPolicy, GCReport, Stats and Deps — as real declarations, and every
// Store/SegmentLog operation as a stub returning core.ErrNotImplemented (or the documented zero
// value, for the handful of methods with no error return). The one exception is compress.go's
// Encode/Decode: a real, fully specified zstd wrapper that SP-06's eventual Put/PutBytes depends on
// directly, so 00-ARCHITECTURE.md §14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md
// has SP-01 implement it for real rather than stubbing it.
package store
