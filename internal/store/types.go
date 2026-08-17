package store

import (
	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
)

// ChunkRef aliases core.ChunkRef: the type lives in core, not store, precisely so that
// internal/tokens can size a root without importing store (00-ARCHITECTURE.md §3.2, §4).
type ChunkRef = core.ChunkRef

// Root is the Merkle-rooted description of one stored object: its content hash, its constituent
// chunks in order, and its size/token accounting (00-ARCHITECTURE.md §5.8).
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/store/want/roots_line.jsonl pins byte-for-byte (Rule W-2), field for
// field, in this exact order — one index/roots.jsonl line.
type Root struct {
	Hash core.Hash `json:"hash"`
	// Chunks lists every chunk this root is composed of, in chunking order.
	Chunks []ChunkRef `json:"chunks"`
	// CanonBytes is the size, in bytes, of the canonicalized content that was actually chunked.
	CanonBytes int64 `json:"canon_bytes"`
	// RawBytes is the size, in bytes, of the original (pre-canonicalization) content.
	RawBytes int64 `json:"raw_bytes"`
	// Tokens is the exact, chunk-level token accounting for this root (G10.2).
	Tokens core.Tokens `json:"tokens"`
}

// PutOptions configures one Put/PutBytes call (00-ARCHITECTURE.md §5.8).
type PutOptions struct {
	// Tool is the name of the tool that produced this content, if any.
	Tool string
	// Path is the paths.Key-form path this content is associated with; "" if none.
	Path string
	// Canon configures the canonicalization pass Put/PutBytes runs before chunking.
	Canon canon.Options
	// KeepRaw requests that the volatile canonicalization deltas be stored alongside the content.
	KeepRaw bool
	// Ephemeral marks the stored content as born ephemeral — a first-eviction-candidate
	// retrieval result (00-ARCHITECTURE.md §8.7).
	Ephemeral bool
}

// PutResult is the outcome of one Put/PutBytes call.
type PutResult struct {
	// Root is the resulting object's Merkle-rooted description.
	Root Root
	// Novel is the number of chunks this call actually wrote to objects/.
	Novel int
	// Reused is the number of chunks this call deduplicated against already-stored chunks.
	Reused int
	// Signature is the MinHash signature computed over the canonicalized content.
	Signature sketch.Signature
	// NearDup is set when a prior version of this content falls within the configured
	// near-duplicate Jaccard threshold; nil otherwise.
	NearDup *NearDupInfo
	// Truncated reports that the input exceeded MaxPutBytes and its tail was discarded. Put
	// truncates rather than failing, because every producer of a Put is a hook and §2.3 permits a
	// hook no exit code but 0.
	Truncated bool
	// Redacted is the number of redact.Match spans replaced on the way in (00-ARCHITECTURE.md
	// §5.22a), so a caller can tell "nothing was secret" from "we scrubbed nine things".
	Redacted int
}

// NearDupInfo describes a near-duplicate relationship a Put/PutBytes call detected against a
// prior stored version of the same logical content.
type NearDupInfo struct {
	// PriorRoot is the near-duplicate prior version's root hash.
	PriorRoot core.Hash
	// Jaccard is the MinHash-estimated Jaccard similarity between the two versions.
	Jaccard float64
	// DeltaBytes is the size difference, in bytes, between the two versions.
	DeltaBytes int64
}
