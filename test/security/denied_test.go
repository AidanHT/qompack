package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The archive-trust half of SP17-M7-03: "no archived retrieval bypasses host permission denial".
//
// The gap this file exists for is the one internal/mcp/authorize.go names in its own header and
// that no test had ever driven: a path that WAS inside the project when its content was captured
// and has since become — or been replaced by, or been moved beneath — something a live read of the
// same path today would refuse. A stored hash or tool_use_id is an address, not a credential, so
// the address has to be re-authorized against the CURRENT filesystem on every retrieval, never
// against the one that existed at capture.
//
// Three shapes of escape are driven, each through `recall`, `expand` and `re_read` on the packaged
// binary's own MCP server. Two of them need a link this host may not be able to create and are
// recorded as skipped when it cannot; the third — a lexical `../` already sitting in a stored
// record — needs no privilege at all and runs everywhere, which is why it carries this matrix.

const (
	// deniedSession is the session every case in this file drives.
	deniedSession = core.SessionID("sess-security-denied-0001")
	// deniedToolUseID addresses the legitimately captured, in-project tool result.
	deniedToolUseID = "toolu_security_denied_inside_01"
	// escapeToolUseID addresses the hand-written record whose stored path escapes lexically.
	escapeToolUseID = "toolu_security_denied_escape_01"
	// deniedPath is the in-project file the legitimate capture read.
	deniedPath = "src/auth.ts"
	// deniedMarker is the phrase recall searches for. It appears once per seeded body, so a hit is
	// evidence the store indexed the content rather than evidence the query matched noise.
	deniedMarker = "refresh token rotation window"
	// outsideMarker is the phrase recall searches for to reach the escaping record.
	outsideMarker = "neighbouring project credential file"
	// outsideSentinel is planted in the file OUTSIDE the project. Its appearance anywhere in a
	// retrieval response is the leak the two link shapes exist to detect, so it is deliberately a
	// string nothing else in the tree could produce.
	outsideSentinel = "QOMPACK-OUTSIDE-SENTINEL-4f2a9c17"
	// escapeSentinel is the content of the object the lexically escaping record addresses. That
	// content is legitimately in the store — it was put through the ordinary path — so the case is
	// about the ADDRESS: an id whose stored path escapes must not materialize its bytes.
	escapeSentinel = "QOMPACK-ESCAPE-SENTINEL-8b31d0e5"
	// escapeRelPath is the lexical escape written into a stored record's Path field.
	escapeRelPath = "../outside/leak.txt"
	// deniedBodyBytes is how large the seeded capture is: comfortably more than the default
	// store.chunk.max, so the object is chunked and an expand has real span boundaries to land on.
	deniedBodyBytes = 24 << 10
)

// deniedBodyText generates the legitimate capture's content: distinct, non-repeating lines so the
// store cannot deduplicate it down to a single chunk.
func deniedBodyText() string {
	var b strings.Builder
	b.Grow(deniedBodyBytes + 128)
	fmt.Fprintf(&b, "// %s\n// the %s is measured from issue, not from first use.\n", deniedPath, deniedMarker)
	for i := 0; b.Len() < deniedBodyBytes; i++ {
		fmt.Fprintf(&b, "export const refreshWindowSeconds%05d = %d; // seeded line %d\n", i, 900+i, i)
	}
	return b.String()
}

// escapeCall is one retrieval the matrix drives for one escape shape.
type escapeCall struct {
	tool string
	args map[string]any
}

// escapeShape is one way a stored address can come to point outside the project.
type escapeShape struct {
	name string
	// skipReason is non-empty when this host cannot express the shape. It is the reason WITHOUT
	// the mandatory "platform: " prefix, which skipRecorded adds.
	skipReason string
	// mechanism names how the escape was actually built, so a record says whether it was a real
	// symlink or a Windows directory junction rather than leaving a reader to guess.
	mechanism string
	// diagnosis is the mechanism a served response would indict, and it is per-shape because the
	// shapes fail differently: a link escape gets past paths.Norm, a lexical one could not.
	diagnosis string
	// install puts the escape in place; remove takes it away again.
	install func(t *testing.T)
	remove  func()
	calls   []escapeCall
	// forbidden is every string that must not appear in any response to this shape's calls.
	forbidden []string
}

// TestSecurity_ArchivedRetrievalCannotBeWalkedOutsideTheProject is the headline case.
//
// It captures a real in-project tool result through the packaged binary's hook, plants a sentinel
// file outside the project root, then makes the stored address point outside in three different
// ways and asks the packaged MCP server for it through every content-bearing tool.
//
// Two assertions are absolute and fail this test: no response may carry the forbidden bytes, and
// no refusal may echo the offending path (a refusal must not become an oracle for what exists
// outside the project). The ENVELOPE SHAPE is recorded rather than asserted uniformly, because the
// three tools do not share one today and that difference is a returned finding — not something
// this package may weaken an assertion to accommodate.
func TestSecurity_ArchivedRetrievalCannotBeWalkedOutsideTheProject(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	// The file that must never come back. It lives beside the project, not inside it.
	outsideDir := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(paths.Long(outsideDir), 0o700))
	outsideFile := filepath.Join(outsideDir, "leak.txt")
	require.NoError(t, os.WriteFile(paths.Long(outsideFile),
		[]byte("api_key_of_the_neighbouring_project = "+outsideSentinel+"\n"), 0o600))

	// A real session, a real daemon, a real capture of an in-project file.
	body := deniedBodyText()
	writeProjectFile(t, p, deniedPath, body)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, deniedSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, deniedSession, deniedToolUseID, deniedPath, body))
	requireIndexed(t, p.Root, deniedToolUseID)

	// The hand-written record: real stored content, addressed under a path that escapes the root
	// lexically. The daemon is stopped first — the store is single-writer, and a second handle over
	// one append log races its offsets.
	shutdownIfReachable(t, p.Root)
	recordEscapingToolUse(t, p.Root, escapeToolUseID, escapeRelPath)

	before := objectFingerprints(t, p.Root)

	srcDir := filepath.Join(p.Root, "src")
	srcStash := filepath.Join(base, "src-stashed")
	filePath := filepath.Join(p.Root, filepath.FromSlash(deniedPath))
	fileStash := filePath + ".stashed"

	shapes := []escapeShape{
		{
			name:      "parent_directory_replaced_by_link_outside",
			diagnosis: normDiagnosis,
			calls:     sharedEscapeCalls(deniedMarker, deniedToolUseID, deniedPath),
			forbidden: []string{outsideSentinel},
			install: func(t *testing.T) {
				t.Helper()
				require.NoError(t, os.Rename(paths.Long(srcDir), paths.Long(srcStash)))
				require.NoError(t, makeLink(srcDir, outsideDir, true),
					"installing the link standing in for %s", srcDir)
			},
			remove: func() {
				_ = os.Remove(paths.Long(srcDir))
				_ = os.Rename(paths.Long(srcStash), paths.Long(srcDir))
			},
		},
		{
			name:      "captured_file_replaced_by_link_outside",
			diagnosis: normDiagnosis,
			calls:     sharedEscapeCalls(deniedMarker, deniedToolUseID, deniedPath),
			forbidden: []string{outsideSentinel},
			install: func(t *testing.T) {
				t.Helper()
				require.NoError(t, os.Rename(paths.Long(filePath), paths.Long(fileStash)))
				require.NoError(t, makeLink(filePath, outsideFile, false),
					"installing the link standing in for %s", filePath)
			},
			remove: func() {
				_ = os.Remove(paths.Long(filePath))
				_ = os.Rename(paths.Long(fileStash), paths.Long(filePath))
			},
		},
		{
			name:      "lexical_parent_escape_in_a_stored_record",
			diagnosis: lexicalDiagnosis,
			mechanism: "a hand-written tool_use record whose Path is " + escapeRelPath,
			calls:     sharedEscapeCalls(outsideMarker, escapeToolUseID, escapeRelPath),
			forbidden: []string{outsideSentinel, escapeSentinel},
			install:   func(t *testing.T) { t.Helper() },
			remove:    func() {},
		},
	}

	// The two link shapes need a link this host may not be able to create, so the mechanism is
	// resolved before the sub-tests run and the shape is marked skipped when there is none.
	shapes[0].mechanism, shapes[0].skipReason = linkMechanism(true)
	shapes[1].mechanism, shapes[1].skipReason = linkMechanism(false)

	for i := range shapes {
		esc := shapes[i]
		t.Run(esc.name, func(t *testing.T) {
			rec := newRecord(t, "archive_trust_"+esc.name)
			rec.Capability = CapArchiveTrust
			if esc.skipReason != "" {
				skipRecorded(t, rec, esc.skipReason)
			}

			t.Cleanup(esc.remove)
			esc.install(t)

			child := startMCP(t, b.Bin, p)
			t.Cleanup(func() { child.stop(t) })
			child.handshake(t)

			observed := map[string]string{}
			for _, call := range esc.calls {
				res := child.call(t, call.tool, call.args)
				for _, forbidden := range esc.forbidden {
					require.NotContains(t, res.Text, forbidden,
						"%s/%s materialized bytes it was not authorized to serve", esc.name, call.tool)
				}
				require.NotContains(t, res.Text, filepath.ToSlash(outsideFile),
					"%s/%s echoed the absolute path of a file outside the project root", esc.name, call.tool)
				require.NotContains(t, res.Text, escapeRelPath,
					"%s/%s echoed the escaping path back to the caller", esc.name, call.tool)
				observed[call.tool] = describeEnvelope(res)
			}
			child.finish(t)

			requireObjectsUnchanged(t, before, objectFingerprints(t, p.Root))

			rec.Detail = fmt.Sprintf("escape built with %s; envelopes: %s", esc.mechanism, renderShapes(observed))
			rec.Outcome, rec.Reason = judgeEnvelopes(esc.name, esc.diagnosis, observed)
			writeRecord(t, rec)
		})
	}
}

// sharedEscapeCalls is the three content-bearing retrievals every escape shape is driven through.
// recall is included even where it cannot hit, because "a query that reaches an unauthorized
// address must not preview it" is exactly the property being measured, and a zero-hit recall is
// itself a recorded observation rather than a silent pass.
func sharedEscapeCalls(query, toolUseID, path string) []escapeCall {
	return []escapeCall{
		{tool: mcp.ToolRecall, args: map[string]any{"query": query, "k": 5}},
		{tool: mcp.ToolExpand, args: map[string]any{"tool_use_id": toolUseID, "full": true}},
		{tool: mcp.ToolReRead, args: map[string]any{"path": path, "full": true}},
	}
}

// TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk pins T13-HISTORY end to end: `re_read`
// returns what was CAPTURED, never what is on disk now, so a file edited or deleted after capture
// still answers with the archived bytes rather than silently becoming a live read.
func TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "archive_trust_re_read_never_reads_the_live_disk")
	rec.Capability = CapArchiveTrust

	const liveMarker = "LIVE-DISK-REPLACEMENT-a71c"
	body := deniedBodyText()
	writeProjectFile(t, p, deniedPath, body)

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, deniedSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, deniedSession, deniedToolUseID, deniedPath, body))
	requireIndexed(t, p.Root, deniedToolUseID)
	// re_read answers from the captured VERSION, which lands after the record: see requireFileVersion.
	requireFileVersion(t, p.Root, deniedPath)

	// The live file is replaced wholesale, and then removed.
	writeProjectFile(t, p, deniedPath, "// "+liveMarker+"\n")

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)

	edited := child.call(t, mcp.ToolReRead, map[string]any{"path": deniedPath, "full": true})
	require.False(t, edited.IsError, "re_read of a captured path must answer: %s", edited.Text)
	require.NotContains(t, edited.Text, liveMarker,
		"re_read served the CURRENT file from disk instead of the archived version")
	require.Contains(t, edited.Text, deniedMarker, "re_read must serve the archived content")

	require.NoError(t, os.Remove(paths.Long(filepath.Join(p.Root, filepath.FromSlash(deniedPath)))))
	deleted := child.call(t, mcp.ToolReRead, map[string]any{"path": deniedPath, "full": true})
	require.False(t, deleted.IsError, "a deleted file must still be answerable from the archive: %s", deleted.Text)
	require.Contains(t, deleted.Text, deniedMarker,
		"after the file was deleted the archive must still answer with the captured bytes")

	child.finish(t)

	rec.Outcome = OutcomeVerified
	rec.Reason = "re_read served the archived bytes after the working-tree file was replaced, and " +
		"again after it was deleted; the replacement marker never appeared in either response."
	writeRecord(t, rec)
}

// recordEscapingToolUse writes, directly into the store, a tool_use record whose Path escapes the
// project root lexically, addressing content that carries the escape sentinel.
//
// It is hand-written on purpose. A hook can never produce this record — the capture path normalizes
// before it stores — so the only way to ask "what does retrieval do when the INDEX already holds an
// escaping address" is to put one there. That is the state a restored backup, a hand-edited index
// or a future capture bug would present, and authorize.go is the thing that is supposed to answer
// it.
func recordEscapingToolUse(t *testing.T, root, id, escPath string) {
	t.Helper()

	s := openStoreAt(t, root)
	ctx := context.Background()

	res, err := s.PutBytes(ctx, []byte(outsideMarker+"\n"+escapeSentinel+"\n"),
		store.PutOptions{Tool: "Read", Path: "src/placeholder.ts"})
	require.NoError(t, err, "seeding the object the escaping record addresses")

	require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
		ID:      core.ToolUseID(id),
		Session: deniedSession,
		TS:      core.NowMilli(core.SystemClock()),
		Tool:    "Read",
		Root:    res.Root.Hash,
		Path:    escPath,
		Bytes:   res.Root.RawBytes,
	}), "recording the escaping tool_use")
	require.NoError(t, s.Flush(ctx))
}
