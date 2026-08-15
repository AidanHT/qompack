package core

// The scalar identity types. They are distinct named types rather than bare strings and ints so
// that a TurnIndex can never be passed where a SegmentID is wanted — the kind of mistake that is
// otherwise invisible until a replay diverges.
type (
	// SessionID is the host's session identifier, taken verbatim from the hook payload.
	SessionID string
	// ToolUseID is the host's per-tool-call identifier.
	ToolUseID string
	// TurnIndex is a 0-based index into the session's turn sequence.
	TurnIndex int
	// SegmentID is 1-based and monotonic per project.
	SegmentID int
	// CheckpointSeq is 1-based and monotonic per project; 0 means "none".
	CheckpointSeq int
	// DecisionID is "dec_" followed by the first 12 hex characters of a domain-separated digest.
	DecisionID string
	// Tokens is a token count.
	Tokens int
	// UnixMilli is a millisecond-resolution wall-clock timestamp.
	UnixMilli int64
)

// ChunkRef and Dep live in core rather than in store/negknow/tokens so that `tokens` can size a
// root without importing `store`, and `store` can answer staleness without importing `negknow`
// (§3.2). Both are comparable value types, so they work as map keys.

// ChunkRef names a stored chunk and its length.
//
// The json tags are explicit because ChunkRef serializes into index/roots.jsonl; Go's default
// field-name marshalling would put "Hash"/"Len" on disk and silently fork the record format.
type ChunkRef struct {
	Hash Hash `json:"hash"`
	Len  int  `json:"len"`
}

// Dep is a dependence of a negative-knowledge record on a file's content: the staleness guard of
// §8.3. Path is in paths.Key form.
//
// The json tags match the `depends_on` entries shown in Qompack.md §8.5, which is normative for
// the on-disk checkpoint shape.
type Dep struct {
	Path string `json:"path"`
	Hash Hash   `json:"hash"`
}

// decisionIDPrefix is the fixed prefix of every DecisionID.
const decisionIDPrefix = "dec_"

// NewDecisionID mints a stable decision identifier from seed. checkpoint.ExtractDecisions is the
// only production caller (§5.14): it is the sole producer of DecisionID and therefore the only
// thing that makes the `why(decision_id)` MCP tool answerable.
func NewDecisionID(seed []byte) DecisionID {
	return DecisionID(decisionIDPrefix + HashBytes(DomainDecision, seed).Short())
}
