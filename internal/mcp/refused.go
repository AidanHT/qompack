package mcp

// HomeRootRefusedText is the tool error every retrieval tool answers with in a session Qompack
// refuses because its project root is the user's home directory (owner decision D18,
// 00-ARCHITECTURE.md §3.3). Such a session records nothing, so there is nothing to retrieve, and
// `qompack mcp` answers every tools/call with this text, as a tool error, without starting a daemon
// or touching the home directory's .qompack, which holds only the user-global layer.
//
// It is part of the published tool surface — docs/mcp-tools.md renders it from here — and it is
// stable: a model or a script may match on it. It tells the model that the condition is the
// session's and will not clear by retrying, and what the user can do instead, because a model that
// reads a transient "unavailable" keeps retrying for the rest of the session.
const HomeRootRefusedText = "qompack is inactive in this session: its project root is the user's " +
	"home directory, so nothing is recorded and there is nothing to retrieve. Retrying will not " +
	"help; the user can open a project directory (one with its own .git) to use qompack's tools."

// ModeOffText is the tool error every retrieval tool answers with when runtime.mode is "off" for
// the session's project. Qompack records nothing in that mode, starts no daemon and writes nothing
// into the project, so there is nothing to retrieve, and `qompack mcp` answers every tools/call
// with this text as a tool error. The forwarding handler gives the same answer when the transport
// reports the mode off, which is the path `qompack recall` and the other retrieval commands take.
//
// It is part of the published tool surface — docs/mcp-tools.md renders it from here — and it is
// stable: a model or a script may match on it. Like HomeRootRefusedText it says the condition will
// not clear by retrying and what the user can do instead; unlike "the qompack daemon returned an
// empty result", which is what the V6 live lane met, it names the switch.
const ModeOffText = "qompack is switched off for this project: runtime.mode is \"off\", so nothing " +
	"is recorded and there is nothing to retrieve. Retrying will not help; the user can set " +
	"runtime.mode to \"auto\" (for example in .qompack/config.json) to use qompack's tools."
