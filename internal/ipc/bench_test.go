package ipc_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// BenchmarkReadState is the hot-path budget check from task-1-spec: ReadState is the only I/O a
// client performs before deciding whether to dial at all, and must stay well under 100 microseconds
// per operation on a warm file cache.
func BenchmarkReadState(b *testing.B) {
	root := b.TempDir()
	fallback := config.Defaults()
	if err := ipc.WriteState(root, ipc.State{
		Mode: contract.ModeFull, Hot: ipc.HotSync,
		ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: true,
		MaxPayloadBytes: 1048576, DaemonPID: 4242, Written: core.UnixMilli(1730000000000),
	}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ipc.ReadState(root, fallback)
	}
}

// BenchmarkEncodeRequest measures the pooled-buffer encode path a hook client exercises once per
// tool call.
func BenchmarkEncodeRequest(b *testing.B) {
	req := ipc.Request{
		Op:      ipc.OpObserveTool,
		Session: core.SessionID("sess-01"),
		TS:      core.UnixMilli(1730000000000),
		Event: &hookio.Event{
			HookEventName: "PostToolUse",
			ToolName:      "Read",
			ToolUseID:     core.ToolUseID("tu_1"),
			ToolInput:     []byte(`{"file_path":"src/auth.ts"}`),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ipc.EncodeRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}
