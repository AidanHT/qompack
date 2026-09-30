package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// F-UAT03-1 of the phase 4 live lane (plans/sdd/V6-closeout/live/report.md), reproduced through the
// shipped composition (the compactLoadRig: runDaemon through Dispatch and the shipped hook clients
// in process). A PreCompact checkpoint sealed after captured Reads carried empty encoded_segments,
// pointers.files and pointers.tools, because the segment holding those Reads was still open when
// the host compacted and only closed segments are ever encoded.

// pointerReadIDs are the two Reads the session makes before it compacts, as in UAT-03.
var pointerReadIDs = []string{"toolu_uat03_read_1", "toolu_uat03_read_2"}

// uat03Read is one PostToolUse Read of a project-relative file, in the shape the observer's own
// e2e rows deliver (test/e2e/observer_e2e_test.go obsToolPayload).
func uat03Read(r *compactLoadRig, id, rel, content string) map[string]any {
	p := r.base("PostToolUse")
	p["tool_name"] = "Read"
	p["tool_use_id"] = id
	p["tool_input"] = map[string]any{"file_path": rel}
	p["tool_response"] = map[string]any{"content": content}
	return p
}

// indexedToolUses reports whether index/tool_use.jsonl names every id.
func indexedToolUses(root string, ids []string) bool {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "tool_use.jsonl")))
	if err != nil {
		return false
	}
	for _, id := range ids {
		if !strings.Contains(string(b), `"id":"`+id+`"`) {
			return false
		}
	}
	return true
}

// adminDrain asks the rig's daemon for one synchronous drain, so a delivery whose acknowledgement
// wait expired into the spool is replayed now rather than at the idle backstop.
func adminDrain(root string) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, logging.Nop(), obs.New(testClock()), ipc.ClientOptions{
		ProjectRoot: root, ConnectDeadline: bootstrapCallDeadline, AckDeadline: bootstrapCallDeadline,
		Clock: testClock(),
	})
	defer func() { _ = c.Close() }()
	_, _ = c.Send(context.Background(), ipc.Request{Op: ipc.OpAdminDrain, Reply: true}, bootstrapCallDeadline)
}

// TestPreCompactCheckpointCarriesTheCompactedReads is F-UAT03-1. UAT-03's session: a prompt, two
// Reads, a Stop, and a manual compaction with no task boundary (no todo, test or commit) in between,
// so the observer's first segment is still open when the host compacts. The checkpoint sealed at
// that compaction must encode that span and so carry its segment, its file pointers and its tool
// pointers.
func TestPreCompactCheckpointCarriesTheCompactedReads(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()

	start := r.base("SessionStart")
	start["source"] = "startup"
	r.hook(t, []string{"session-start"}, start)
	prompt := r.base("UserPromptSubmit")
	prompt["prompt"] = "We are preparing the Tern key migration. Read handoff.md and src/app.py."
	r.hook(t, []string{"observe", "prompt"}, prompt)
	for i, id := range pointerReadIDs {
		rel := filepath.ToSlash(filepath.Join("src", fmt.Sprintf("uat03_%d.py", i+1)))
		content := fmt.Sprintf("K%d = %d\n", i+1, 985+i)
		// The file is on disk, as a real Read's is: a seal drops a file pointer whose file is gone.
		require.NoError(t, os.MkdirAll(filepath.Join(r.root, "src"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(r.root, filepath.FromSlash(rel)), []byte(content), 0o600))
		r.hook(t, []string{"observe", "tool"}, uat03Read(r, id, rel, content))
	}
	r.hook(t, []string{"observe", "stop"}, r.base("Stop"))

	// The Reads are fire-and-forget deliveries; the host's own compaction comes seconds later in a
	// real session. Wait for them to be indexed, driving a drain for any that spooled, and then
	// send one more reply-bearing prompt: the daemon publishes a session's leased arrivals in
	// order, so its answer is also the point by which the Reads' observers have run.
	require.Eventually(t, func() bool {
		adminDrain(r.root)
		return indexedToolUses(r.root, pointerReadIDs)
	}, bootstrapUpBound, bootstrapUpTick, "the Reads never reached index/tool_use.jsonl")
	barrier := r.base("UserPromptSubmit")
	barrier["prompt"] = "/compact"
	r.hook(t, []string{"observe", "prompt"}, barrier)

	r.compact(t)

	cp := latestCheckpoint(t, r.root)
	require.NotEmpty(t, cp.EncodedSegments,
		"the span the host compacted must be encoded into the checkpoint sealed for it (F-UAT03-1)")

	var tools []string
	for _, tp := range cp.Pointers.Tools {
		tools = append(tools, string(tp.ToolUseID))
	}
	require.Subset(t, tools, pointerReadIDs, "both Reads' tool results must be pointed to")
	var files []string
	for _, f := range cp.Pointers.Files {
		files = append(files, f.Path)
	}
	require.Contains(t, files, "src/uat03_1.py", "the Reads' files must be pointed to")
	require.Contains(t, files, "src/uat03_2.py")

	raw, err := json.Marshal(cp.Pointers)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "K1 = 985", "pointers carry no file content (§4.4)")
}

// TestSecondPreCompactCarriesItsOwnSpan is the same finding at the second compaction of a session.
// The first seal opens its successor draft at once, so every later compaction finds a draft already
// open; the span closed at that compaction must still be encoded into the checkpoint sealed for it,
// not left for an idle pass to fold into the one after.
func TestSecondPreCompactCarriesItsOwnSpan(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()

	start := r.base("SessionStart")
	start["source"] = "startup"
	r.hook(t, []string{"session-start"}, start)
	session := []struct{ id, rel string }{
		{"toolu_span1_read", "src/span1.py"},
		{"toolu_span2_read", "src/span2.py"},
	}
	require.NoError(t, os.MkdirAll(filepath.Join(r.root, "src"), 0o700))
	for i, read := range session {
		prompt := r.base("UserPromptSubmit")
		prompt["prompt"] = fmt.Sprintf("Read %s.", read.rel)
		r.hook(t, []string{"observe", "prompt"}, prompt)
		content := fmt.Sprintf("SPAN = %d\n", i+1)
		require.NoError(t, os.WriteFile(filepath.Join(r.root, filepath.FromSlash(read.rel)), []byte(content), 0o600))
		r.hook(t, []string{"observe", "tool"}, uat03Read(r, read.id, read.rel, content))
		r.hook(t, []string{"observe", "stop"}, r.base("Stop"))
		require.Eventually(t, func() bool {
			adminDrain(r.root)
			return indexedToolUses(r.root, []string{read.id})
		}, bootstrapUpBound, bootstrapUpTick, "%s never reached index/tool_use.jsonl", read.id)
		barrier := r.base("UserPromptSubmit")
		barrier["prompt"] = "/compact"
		r.hook(t, []string{"observe", "prompt"}, barrier)
		r.compact(t)
	}

	cp := latestCheckpoint(t, r.root)
	require.Equal(t, 2, int(cp.Seq), "fixture sanity: the second compaction's checkpoint")
	var tools []string
	for _, tp := range cp.Pointers.Tools {
		tools = append(tools, string(tp.ToolUseID))
	}
	require.Contains(t, tools, "toolu_span2_read",
		"the second compaction's checkpoint must encode the span that compaction closed")
}
