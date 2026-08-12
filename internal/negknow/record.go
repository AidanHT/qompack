package negknow

import "github.com/qompack/qompack/internal/core"

// Record is one elimination: an evidence-linked, staleness-guarded statement that a specific
// approach to a specific target was tried and did not work (00-ARCHITECTURE.md §5.10, §8.3).
//
// The json tags are explicit and frozen, transcribed verbatim from 00-ARCHITECTURE.md §5.10: this
// is the shape testdata/golden/contracts/negknow/want/elimination_record.jsonl (one
// records/eliminations.jsonl line) and testdata/golden/contracts/checkpoint/want/0001.json's
// eliminated[0] entry pin byte-for-byte (Rule W-2), field for field, in this exact order.
type Record struct {
	ID      string         `json:"id"`
	Session core.SessionID `json:"session"`
	TS      core.UnixMilli `json:"ts"`
	// Target is the free-text thing the eliminated approach was tried against, e.g.
	// "src/auth.ts:refreshToken".
	Target string `json:"target"`
	// Approach is the free-text approach that was tried, e.g. "widen pool timeout".
	Approach string `json:"approach"`
	// Reason is the free-text explanation of why the approach did not work.
	Reason string `json:"reason"`
	// Desc is Target/Approach/Reason's canonical form: what Descriptor.Key hashes into the bloom.
	Desc Descriptor `json:"descriptor"`
	// Evidence is the content hash of whatever proved this elimination (a tool result, a log).
	Evidence core.Hash `json:"evidence"`
	// DependsOn is the staleness guard (00-ARCHITECTURE.md §8.3): the file hashes this
	// elimination's validity depends on. RefreshStaleness compares these against the store's
	// current file versions.
	DependsOn []Dep  `json:"depends_on"`
	Scope     Scope  `json:"scope"`
	Status    Status `json:"status"`
	// StaleSince is when Status flipped to StatusStale; zero (omitted) while Status is
	// StatusActive.
	StaleSince core.UnixMilli `json:"stale_since,omitempty"`
	// StaleBecause names which DependsOn entries changed, causing the flip to StatusStale;
	// nil (omitted) while Status is StatusActive.
	StaleBecause []string   `json:"stale_because,omitempty"`
	Source       SourceKind `json:"source"`
}
