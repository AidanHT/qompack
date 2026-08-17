package contract

// notYetImplementedObserved is the exact Observed string §12.1 gives an assertion whose producer is
// absent from the build. It is matched literally by test/guards' §12.1 assertion and by the
// contract golden fixture, so it is a frozen string, not a message.
const notYetImplementedObserved = "not-yet-implemented"

// StandardAssertions returns the nine host-contract assertions of 00-ARCHITECTURE.md §5.19, in the
// order §12.1's table lists them, each carrying its real DECLARED severity and gated behind
// HasProducer via gated (producers.go, assertions.go): an assertion whose producer has not been
// declared reports OK/SevInfo/not-yet-implemented and its real Check — checkSessionStartFires and
// its eight siblings in assertions.go — never runs at all.
//
// The five SP-05 always declares from wave 1 (session_start.fires, session_start.source_compact,
// hook.payload_shape, transcript.readable, plugin.root_resolves) therefore observe for real as soon
// as a daemon calls DeclareProducer for them (the next task's job); the four §12.1 names as
// later-wave (precompact.has_time_to_write, precompact.custom_instructions_accepted,
// hook.additional_context_delivered, mcp.server_registered) stay not-yet-implemented until their
// owning subplan (SP-10/SP-11/SP-13) binds its Services seam and declares its own producer — which
// is exactly why a fresh, wave-1-only build still reports ModeFull.
func StandardAssertions() []Assertion {
	return []Assertion{
		gated(CSessionStartFires, SevCritical, "SessionStart hook fires", checkSessionStartFires),
		gated(CSessionStartSourceCompact, SevCritical, "SessionStart arrives with source=compact after PreCompact", checkSessionStartSourceCompact),
		gated(CAdditionalContext, SevCritical, "additionalContext reaches the transcript", checkAdditionalContextDelivered),
		gated(CPreCompactTiming, SevWarn, "PreCompact has time to write", checkPreCompactTiming),
		gated(CPreCompactCustomInstr, SevWarn, "custom_instructions accepted", checkPreCompactCustomInstr),
		gated(CHookPayloadShape, SevCritical, "hook payload shape matches hookio.Event", checkHookPayloadShape),
		gated(CMCPRegistered, SevInfo, "MCP server received initialize", checkMCPServerRegistered),
		gated(CTranscriptReadable, SevWarn, "transcript_path exists and parses", checkTranscriptReadable),
		gated(CPluginRootResolves, SevWarn, "CLAUDE_PLUGIN_ROOT expands to an existing binary", checkPluginRootResolves),
	}
}
