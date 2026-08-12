package contract

// ID names one host-contract assertion. The nine values below are the complete, normative set of
// 00-ARCHITECTURE.md §5.19; the string form of each is stable on-disk data (it is persisted in
// state/contract.json and surfaced by /qompack:status), so a value may never be respelled.
type ID string

// The nine assertions of 00-ARCHITECTURE.md §5.19 / §12.1. The comment on each names the observable
// §12.1's table specifies, which is what SP-05 implements inside the corresponding Assertion.Check.
const (
	// CSessionStartFires: a marker written at SessionEnd/PreCompact is found by the next
	// SessionStart; absence across two sessions is a failure.
	CSessionStartFires ID = "session_start.fires"
	// CSessionStartSourceCompact: after a PreCompact is observed, the next SessionStart must
	// arrive with source == "compact" within the same session id. Recorded in state/contract.json
	// and evaluated on the FOLLOWING start.
	CSessionStartSourceCompact ID = "session_start.source_compact"
	// CAdditionalContext: SessionStart emits a sentinel token in additionalContext; the next
	// UserPromptSubmit reads the transcript tail and looks for it. Not found is a failure.
	CAdditionalContext ID = "hook.additional_context_delivered"
	// CPreCompactTiming: measured PreCompact wall time vs. the manifest timeout; p99 over 60% of
	// the timeout warns, hitting the timeout fails.
	CPreCompactTiming ID = "precompact.has_time_to_write"
	// CPreCompactCustomInstr: the emitted instruction's sentinel phrase is searched for in the
	// post-compaction summary; absent warns (advisory by design, §8.5).
	CPreCompactCustomInstr ID = "precompact.custom_instructions_accepted"
	// CHookPayloadShape: required fields present and typed as hookio.Event expects.
	CHookPayloadShape ID = "hook.payload_shape"
	// CMCPRegistered: the MCP server received initialize at least once this session.
	CMCPRegistered ID = "mcp.server_registered"
	// CTranscriptReadable: transcript_path exists and parses.
	CTranscriptReadable ID = "transcript.readable"
	// CPluginRootResolves: ${CLAUDE_PLUGIN_ROOT} expanded to an existing binary.
	CPluginRootResolves ID = "plugin.root_resolves"
)
