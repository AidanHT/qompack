package cli

import "github.com/qompack/qompack/internal/ipc"

// hookCmds returns the six thin-client hook entry points of §7.3, each a Cmd{Hook:true} dispatched
// through doHook's shared skeleton (hookclient.go) and the shipped Dispatch/runGuarded framework's
// always-exit-0 guarantee. session-start is the one exception with a body of its own
// (sessionstart.go): it additionally starts the daemon before Send, per §2.4/§5.21.
func hookCmds() []Cmd {
	return []Cmd{
		{
			Name: "observe tool", Hook: true,
			Summary: "PostToolUse — chunk and store tool results (thin ipc client)",
			Run:     doHook(hookSpec{op: ipc.OpObserveTool, reply: false}),
		},
		{
			Name: "observe prompt", Hook: true,
			Summary: "UserPromptSubmit — capture user intent verbatim (thin ipc client)",
			Run:     doHook(hookSpec{op: ipc.OpObservePrompt, reply: true, deadline: promptReplyDeadline}),
		},
		{
			Name: "observe stop", Hook: true,
			Summary: "Stop/SubagentStop — capture subagent detail (thin ipc client)",
			Run:     doHook(hookSpec{op: ipc.OpObserveStop, reply: false}),
		},
		{
			Name: "session-start", Hook: true,
			Summary: "SessionStart — daemon start + contract monitor before any other work",
			Run:     runSessionStart,
		},
		{
			Name: "checkpoint", Hook: true,
			Summary: "PreCompact — write an immutable checkpoint (thin ipc client)",
			Run:     doHook(hookSpec{op: ipc.OpCheckpoint, reply: true, deadline: checkpointReplyDeadline}),
		},
		{
			Name: "flush", Hook: true,
			Summary: "SessionEnd — flush, compact, write the session index (thin ipc client)",
			Run:     doHook(hookSpec{op: ipc.OpFlush, reply: true, deadline: flushReplyDeadline}),
		},
	}
}
