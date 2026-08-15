package store

import (
	"encoding/json"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
)

// Supersession records whether a ToolUseRecord is still the current answer for its Path, or has
// been superseded by a later tool use on the same Path (00-ARCHITECTURE.md §5.8).
//
// Values start at 0 and are frozen in this exact order by
// testdata/golden/contracts/store/want/tool_use_line.jsonl (Rule W-2): that fixture's record is
// StatusOK, and its "status" field is the literal integer 0, which is only correct if StatusOK is
// this const block's first value (0-indexed). Do not reorder these.
type Supersession uint8

const (
	// StatusOK marks a ToolUseRecord as the current answer for its Path.
	StatusOK Supersession = iota
	// StatusSuperseded marks a ToolUseRecord as replaced by a later tool use on the same Path.
	StatusSuperseded
)

// ToolUseRecord is one index/tool_use.jsonl entry: the durable record of a single tool
// invocation's stored content, keyed by the host's own tool_use_id (00-ARCHITECTURE.md §5.8).
//
// The json shape is explicit and frozen: this reproduces
// testdata/golden/contracts/store/want/tool_use_line.jsonl byte-for-byte (Rule W-2), field for
// field, in this exact order. See MarshalJSON's own comment for why ToolUseRecord carries custom
// JSON methods rather than plain struct tags.
type ToolUseRecord struct {
	ID      core.ToolUseID
	Session core.SessionID
	Turn    core.TurnIndex
	TS      core.UnixMilli
	Tool    string
	// ArgsDigest is the domain-separated digest of the tool's arguments (core.DomainArgs).
	ArgsDigest core.Hash
	// ArgsPreview is a human-readable preview of the tool's arguments, truncated to at most 120
	// characters, used by tombstones (observer.Tombstone) and the `timeline` retrieval tool.
	ArgsPreview string
	Root        core.Hash
	Path        string
	Bytes       int64
	Tokens      core.Tokens
	// Signature is the MinHash signature computed over this record's canonicalized content.
	Signature sketch.Signature
	Status    Supersession
	// SupersededBy is the tool_use_id that replaced this record; "" while Status is StatusOK.
	SupersededBy core.ToolUseID
	// Ephemeral marks this record as born ephemeral (00-ARCHITECTURE.md §8.7).
	Ephemeral bool
	// Subagent is "" for the main agent, or the subagent's name for a SubagentStop capture.
	Subagent string
}

// toolUseRecordWire is ToolUseRecord's exact JSON wire shape, field for field in the order
// testdata/golden/contracts/store/want/tool_use_line.jsonl freezes.
//
// It exists because sketch.Signature — embedded as ToolUseRecord.Signature — carries no json
// tags of its own: sketch.Signature round-trips via MarshalBinary/UnmarshalBinary (its sketch
// file format), not encoding/json, and internal/sketch is a package this package may not modify
// (00-ARCHITECTURE.md §3.2). ToolUseRecord is the one place a Signature is embedded inside a
// JSON-serialized record, so it supplies the "perms"/"mins" wire tags at this boundary via
// signatureWire rather than reaching into a package it does not own.
type toolUseRecordWire struct {
	ID           core.ToolUseID `json:"id"`
	Session      core.SessionID `json:"session"`
	Turn         core.TurnIndex `json:"turn"`
	TS           core.UnixMilli `json:"ts"`
	Tool         string         `json:"tool"`
	ArgsDigest   core.Hash      `json:"args_digest"`
	ArgsPreview  string         `json:"args_preview"`
	Root         core.Hash      `json:"root"`
	Path         string         `json:"path"`
	Bytes        int64          `json:"bytes"`
	Tokens       core.Tokens    `json:"tokens"`
	Signature    signatureWire  `json:"signature"`
	Status       Supersession   `json:"status"`
	SupersededBy core.ToolUseID `json:"superseded_by"`
	Ephemeral    bool           `json:"ephemeral"`
	Subagent     string         `json:"subagent"`
}

// signatureWire mirrors sketch.Signature with explicit lowercase json tags — see
// toolUseRecordWire's doc comment for why this package supplies them instead of sketch.
type signatureWire struct {
	Perms uint16   `json:"perms"`
	Mins  []uint64 `json:"mins"`
}

// MarshalJSON renders r via toolUseRecordWire, the shape
// testdata/golden/contracts/store/want/tool_use_line.jsonl freezes (Rule W-2).
func (r ToolUseRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(toolUseRecordWire{
		ID: r.ID, Session: r.Session, Turn: r.Turn, TS: r.TS, Tool: r.Tool,
		ArgsDigest: r.ArgsDigest, ArgsPreview: r.ArgsPreview, Root: r.Root, Path: r.Path,
		Bytes: r.Bytes, Tokens: r.Tokens,
		Signature:    signatureWire{Perms: r.Signature.Perms, Mins: r.Signature.Mins},
		Status:       r.Status,
		SupersededBy: r.SupersededBy, Ephemeral: r.Ephemeral, Subagent: r.Subagent,
	})
}

// UnmarshalJSON is MarshalJSON's inverse.
func (r *ToolUseRecord) UnmarshalJSON(b []byte) error {
	var w toolUseRecordWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*r = ToolUseRecord{
		ID: w.ID, Session: w.Session, Turn: w.Turn, TS: w.TS, Tool: w.Tool,
		ArgsDigest: w.ArgsDigest, ArgsPreview: w.ArgsPreview, Root: w.Root, Path: w.Path,
		Bytes: w.Bytes, Tokens: w.Tokens,
		Signature:    sketch.Signature{Perms: w.Signature.Perms, Mins: w.Signature.Mins},
		Status:       w.Status,
		SupersededBy: w.SupersededBy, Ephemeral: w.Ephemeral, Subagent: w.Subagent,
	}
	return nil
}

// FileVersion is one entry in a file's version history (00-ARCHITECTURE.md §5.8, §8.2):
// index/files.json maps a paths.Key path to a list of these, ordered by TS.
type FileVersion struct {
	TS    core.UnixMilli `json:"ts"`
	Root  core.Hash      `json:"root"`
	Turn  core.TurnIndex `json:"turn"`
	Bytes int64          `json:"bytes"`
}
