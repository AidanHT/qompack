package checkpoint

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// SourceSet is the ONLY thing a checkpoint may be built from (00-ARCHITECTURE.md §5.14, §8.5).
// Every field is a seam onto durable, original content: the object store, the segment log, the
// elimination ledger, the pin log, the dependence DAG, the action grammar and the token
// estimator. It deliberately has NO field that can carry live context text.
//
// That absence is the mechanical enforcement of §4.6's "never compress a compression": with no
// way to hand the writer a summary, compilation itself makes "checkpoint from a summary"
// impossible. Adding a string, a []byte or a transcript-shaped field here would silently undo the
// invariant the whole design rests on.
type SourceSet struct {
	// Store is the content-addressed store the original bytes are read back from.
	Store store.Store
	// Segments is the segment log MarkEncoded enforces the DPI guard through.
	Segments store.SegmentLog
	// Ledger supplies the tier-1 eliminated[] records.
	Ledger negknow.Ledger
	// Pins supplies the tier-1 invariants[].
	Pins pins.Store
	// Graph supplies slice scores for ranking and the EdgeExplains chains ExtractDecisions mints
	// decisions from.
	Graph dag.Graph
	// Grammar supplies the compressed action history.
	Grammar grammar.Sequitur
	// Tokens prices every tier against the checkpoint budget.
	Tokens tokens.Estimator
}

// Draft is an in-progress checkpoint: opaque by design (00-ARCHITECTURE.md §5.14), it accumulates
// tiers incrementally as Advance encodes closed, unencoded segments during idle time (O5), so
// that Finalize has nothing heavy left to do and can complete inside budget B-E.
//
// SP-10 owns its fields. It is deliberately empty of MEANING here rather than speculatively
// shaped: a draft that carried plausible-looking accumulated state would be exactly the faked
// behaviour §14.1 rule 2 of plans/V1-SP-01-foundation-toolchain-and-contracts.md forbids.
type Draft struct {
	// _ is a size anchor, and it is not optional. Begin hands the caller a *Draft and Advance,
	// Finalize and Abort each take it back, so a *Draft is an IDENTITY — two concurrently open
	// drafts must be distinguishable, and an implementation that keys state by the pointer must
	// be able to. A struct{} cannot support that: the Go spec lets every pointer to a zero-sized
	// variable share one address, and on gc they do, so `&Draft{} != &Draft{}` is false and a
	// map[*Draft]state silently collapses two drafts into one.
	//
	// SP-10 replaces this field with the real accumulated tiers. It must not replace it with
	// nothing.
	_ [1]byte
}

// Ref is the durable reference to one finalized checkpoint artifact (00-ARCHITECTURE.md §5.14):
// enough to locate it, verify it, and know what it cost.
type Ref struct {
	// Seq is the checkpoint's 1-based sequence number.
	Seq core.CheckpointSeq
	// Path is the artifact's path on disk, e.g. "<root>/.qompack/checkpoints/0007.json".
	Path string
	// SHA256 is the artifact's content digest, as recorded in checkpoints/MANIFEST.jsonl and
	// re-verified by Reader.Verify.
	SHA256 core.Hash
	// Bytes is the artifact's size on disk.
	Bytes int64
	// Tokens is the artifact's estimated token cost.
	Tokens core.Tokens
	// Frontier is the turn index this checkpoint advanced the encoding frontier to.
	Frontier core.TurnIndex
	// Created is when the artifact was finalized.
	Created core.UnixMilli
}
