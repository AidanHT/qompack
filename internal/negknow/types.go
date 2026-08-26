package negknow

import (
	"bytes"

	"github.com/qompack/qompack/internal/core"
)

// Scope is a Record's visibility: "session" (this session only) or "project" (visible across the
// whole project) (00-ARCHITECTURE.md §5.10).
type Scope string

const (
	// ScopeSession restricts a Record to the session that created it.
	ScopeSession Scope = "session"
	// ScopeProject makes a Record visible across the whole project.
	ScopeProject Scope = "project"
)

// Status is a Record's current standing: "active" (still believed true) or "stale" (a dependency
// changed since the record was made, so it may no longer hold) (00-ARCHITECTURE.md §5.10, §8.3).
type Status string

const (
	// StatusActive marks a Record as still believed true.
	StatusActive Status = "active"
	// StatusStale marks a Record whose depends_on hashes no longer match the store's current
	// file versions (00-ARCHITECTURE.md §8.3): it may no longer hold and is a candidate for
	// re-verification.
	StatusStale Status = "stale"
)

// SourceKind identifies how one elimination was discovered (00-ARCHITECTURE.md §5.10, §8.3).
//
// Values start at 0 and are frozen in this exact order by
// testdata/golden/contracts/negknow/want/elimination_record.jsonl and
// testdata/golden/contracts/checkpoint/want/0001.json's eliminated[0] entry (Rule W-2): both
// fixtures' record has "source":1, which is only correct if SourceSlashCommand is this const
// block's second value (0-indexed). Do not reorder these.
type SourceKind uint8

const (
	// SourceMCP marks an elimination recorded via the record_eliminated MCP tool.
	SourceMCP SourceKind = iota
	// SourceSlashCommand marks an elimination recorded via a `/qompack:*` slash command.
	SourceSlashCommand
	// SourceHeuristic marks an elimination Detector.Scan inferred from the DAG (§8.3's
	// test-fail -> revert -> different-approach pattern), not one an agent explicitly reported.
	SourceHeuristic
	// SourceUserStatement marks an elimination captured verbatim from a user prompt.
	SourceUserStatement
)

// Descriptor is the canonical, four-field description of one elimination (00-ARCHITECTURE.md
// §5.10, §8.3): the thing Key() hashes into a stable bloom key.
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/negknow/want/elimination_record.jsonl and
// testdata/golden/contracts/checkpoint/want/0001.json's eliminated[0].descriptor pin byte-for-byte
// (Rule W-2), field for field, in this exact order.
type Descriptor struct {
	// NormalizedPath is a paths.Key-form path; "" when the elimination is not file-scoped.
	NormalizedPath string `json:"normalized_path"`
	// Symbol is a symbol name within NormalizedPath; "" means null (no symbol scope).
	Symbol string `json:"symbol"`
	// ApproachClass is the canonicalized verb-phrase class of the approach that was eliminated,
	// e.g. "widen-timeout".
	ApproachClass string `json:"approach_class"`
	// ReasonHash is the domain-separated digest of the (normalized) reason text.
	ReasonHash core.Hash `json:"reason_hash"`
}

// Canonicalize turns free-text target/approach/reason strings into a canonical Descriptor
// (00-ARCHITECTURE.md §5.10, Qompack.md §8.3): SplitTarget supplies the normalized path and the
// symbol, ApproachClass the approach-class label, and reasonHash the normalized-reason digest.
// It is deterministic and does no I/O, so a phrase always canonicalizes to the same bloom key.
//
// Canonicalize has no error return because none of its three steps can fail: a target it cannot
// split is a path with no symbol, and an approach it cannot classify is "unclassified". A caller
// holding an ABSOLUTE path wants CanonicalizeAt instead, which normalizes against the project
// root first and does have an error return.
//
// The classification is deliberately coarse — see the synonyms table in classify.go — so two
// phrasings of one idea collide on purpose. That only widens the set of records a query can
// match; the record lookup stays authoritative and Record.Reason is what disambiguates.
//
// Never compare this output against testdata/golden/contracts/negknow/want/, whose
// approach_class ("widen-timeout") is a hand-chosen label in a frozen wire-shape fixture, not an
// assertion about ApproachClass — which yields "widen-pool-timeout" for that same phrase.
func Canonicalize(target, approach, reason string) Descriptor {
	p, sym := SplitTarget(target)
	return Descriptor{
		NormalizedPath: p,
		Symbol:         sym,
		ApproachClass:  ApproachClass(approach),
		ReasonHash:     reasonHash(reason),
	}
}

// Key returns d's stable bloom key: the domain-separated digest of its four fields, concatenated
// with 0x1f (ASCII Unit Separator) delimiters — a byte that cannot appear in any of the three text
// fields under normal use, and whose presence is what keeps ("a","bc") and ("ab","c") from hashing
// identically (00-ARCHITECTURE.md §5.10). This is a real implementation, not a stub (§14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): it is fully specified by the architecture
// and later subplans need a stable bloom key before SP-09's Ledger is real. Transcribed verbatim
// from §14.1; do not improvise the byte layout — TestDescriptorKey_Stable freezes its output as a
// golden, and any change here silently re-keys every bloom entry already on disk.
func (d Descriptor) Key() []byte {
	var b bytes.Buffer
	b.WriteString(d.NormalizedPath)
	b.WriteByte(0x1f)
	b.WriteString(d.Symbol)
	b.WriteByte(0x1f)
	b.WriteString(d.ApproachClass)
	b.WriteByte(0x1f)
	b.Write(d.ReasonHash[:])
	h := core.HashBytes(core.DomainNegKnow, b.Bytes())
	return h[:]
}

// Dep aliases core.Dep (00-ARCHITECTURE.md §4, §5.10): store must not import negknow (§3.2), so
// the staleness dependency type lives in core and this is only a convenience alias for callers
// working in negknow's own vocabulary.
type Dep = core.Dep
