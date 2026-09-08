package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// expand and re_read, driven through Dispatch rather than through the handler bodies.
//
// Every call here goes the route a real `tools/call` takes, because most of what these two tools
// promise is not in their own function bodies: the schema validation, the B-F metering and the
// ephemeral tagging all live in the run() preamble, and a test that called h.expand directly would
// assert the §5.16 contract while skipping three quarters of the machinery that implements it.
//
// The other rule this file exists to pin is handlers_common.go's: a SEMANTIC MISS — an unknown hash,
// a path with no version — is IsError:false plus found:false, and IsError is reserved for invalid
// arguments and backend failures. Getting that backwards is the single most likely way to break the
// contract without noticing, so every miss case below asserts the flag as well as the body.

// spanTurnEarly and spanTurnLate are the turns the two recorded versions the "turn:7" lookup chooses
// between are written at. Nothing is recorded at turn 7 itself, which is the whole point.
const (
	spanTurnEarly core.TurnIndex = 4
	spanTurnLate  core.TurnIndex = 9
)

// spanContentOf decodes a content tool's successful body, which expand and re_read share.
func spanContentOf(t *testing.T, f *fixture, name string, args map[string]any) contentBody {
	t.Helper()
	var body contentBody
	f.callOK(t, name, args, &body)
	return body
}

// spanVersionedFile stores two versions of one path at two turns, advancing the fake clock between
// them, and returns the fixture plus each version's text and root hash.
//
// The clock advance is what makes the timestamp lookup meaningful: §6.1 bans wall-clock sleeps, and
// two versions stamped in the same millisecond would let a "give me the older one" query pass by
// picking either.
func spanVersionedFile(t *testing.T, path string) (f *fixture, early, late string, earlyRoot core.Hash) {
	t.Helper()
	f = newFixture(t)
	early, late = "export const version = 1;\n", "export const version = 2;\n"

	earlyRoot, _ = f.putAndRecord(t, "Read", path, early, spanTurnEarly)
	f.Clock.Advance(time.Hour)
	f.putAndRecord(t, "Read", path, late, spanTurnLate)
	return f, early, late, earlyRoot
}

// TestExpandByToolUseID covers expand's first address form: the host's own tool_use_id, which is what
// a tombstone carries and therefore the only handle a model has after its result was cleared.
func TestExpandByToolUseID(t *testing.T) {
	f, text, _, id := spanAuthObject(t)

	body := spanContentOf(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)})

	require.True(t, body.Found, "a recorded tool use must be found")
	require.NotEmpty(t, body.Content, "expand must return content, not merely a pointer")
	require.Equal(t, f.rootOfUse(t, id).String(), body.Hash, "the hash must be the record's own root")
	require.Equal(t, spanAuthPath, body.Path, "the path must come from the tool-use record")
	require.Equal(t, "Read", body.Tool, "the tool must come from the tool-use record")
	require.Equal(t, int64(authTotalBytes), body.TotalBytes, "total_bytes must be the whole object")
	require.Contains(t, text, body.Content, "the body must be a window of the stored object")
}

// TestExpandByHash covers the second address form and pins that the two agree: addressing the same
// object by id and by hash must resolve the same window, or paging a tombstone and paging a recall hit
// would hand the model different bytes for the same content.
func TestExpandByHash(t *testing.T) {
	f, _, root, id := spanAuthObject(t)

	byID := spanContentOf(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)})
	byHash := spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.Hash.String()})

	require.True(t, byHash.Found, "a stored root must be found by hash")
	require.Equal(t, byID.Content, byHash.Content, "both address forms must resolve the same window")
	require.Equal(t, byID.Span, byHash.Span, "both address forms must resolve the same span")
	require.Empty(t, byHash.Path, "a hash carries no path of its own")
}

// TestExpandAcceptsChunkHash covers §5.16's chunk-hash escape hatch: a model that pasted a CHUNK hash
// rather than a root hash asked a reasonable question with the wrong noun, so resolveExpandTarget
// synthesizes a one-chunk root and answers it rather than reporting a miss.
//
// The read has to come from the bytes resolveExpandTarget already fetched, NOT from the store: a
// synthesized root is absent from the root index by construction, and FSStore.OpenSpan resolves
// through GetRoot, so reading it back would fail with "not found: root <hex>" with the chunk
// sitting in hand. That is what ResolveSpanFromBytes exists for, and this test is what pins it —
// it failed exactly that way before the read path was bound.
func TestExpandAcceptsChunkHash(t *testing.T) {
	f := newFixture(t)
	text := buildFiller("chunky", spanSmallBytes)
	root := spanRootOf(t, f, f.put(t, "src/chunky.ts", text))
	require.Len(t, root.Chunks, 1, "the fixture must be one chunk so its bytes are known exactly")

	body := spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.Chunks[0].Hash.String()})

	require.True(t, body.Found, "an addressable chunk must be answered, not reported missing")
	require.Equal(t, text, body.Content, "the body must be exactly that chunk's bytes")
	require.Equal(t, int64(len(text)), body.TotalBytes, "a chunk's total is its own length")
}

// TestExpandBothArgsRejected pins that "exactly one" is not "at least one". Two addresses that disagree
// have no defensible resolution, and silently picking one would hand back content nobody asked for.
func TestExpandBothArgsRejected(t *testing.T) {
	f, _, root, id := spanAuthObject(t)

	msg := f.callErr(t, ToolExpand, map[string]any{
		"hash": root.Hash.String(), "tool_use_id": string(id),
	})
	require.Equal(t, "expand requires exactly one of hash or tool_use_id", msg)
}

// TestExpandNeitherArgRejected is the same rule from the other side: neither address is as unanswerable
// as both, and it reports the same message so the model learns one rule rather than two.
func TestExpandNeitherArgRejected(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolExpand, map[string]any{})
	require.Equal(t, "expand requires exactly one of hash or tool_use_id", msg)
}

// TestExpandUnknownHashFoundFalse pins the semantic-miss rule for a well-formed hash nothing was ever
// stored under: found:false naming where the search went, NOT an error. "I looked here and here and it
// is not there" is information the model can act on; isError is not.
func TestExpandUnknownHashFoundFalse(t *testing.T) {
	f := newFixture(t)

	var body missBody
	resp := f.callOK(t, ToolExpand, map[string]any{"hash": sampleHash}, &body)

	require.False(t, resp.IsError, "an unknown hash is a miss, not a failure")
	require.False(t, body.Found, "nothing is stored under an all-zero hash")
	require.Equal(t, "object store (root and chunk index)", body.Searched, "the miss must name where it looked")
}

// TestExpandMalformedHashIsError is the other half of that rule: a string that is not a hash at all is a
// bad ARGUMENT, and an argument error is exactly what isError is reserved for.
func TestExpandMalformedHashIsError(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolExpand, map[string]any{"hash": "deadbeef"})
	require.Equal(t, `expand failed: hash must be "sha256:" followed by 64 hex characters`, msg)
}

// TestExpandFullEscapeHatch pins that full=true reaches the resolver from the tool's arguments: the
// default is the matching hunk, and this is the one way a caller gets the object back whole.
func TestExpandFullEscapeHatch(t *testing.T) {
	f, text, _, id := spanAuthObject(t)

	body := spanContentOf(t, f, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true})

	require.Len(t, body.Content, authTotalBytes, "full must return the whole object")
	require.Equal(t, text, body.Content, "full must return the object byte for byte")
	require.False(t, body.Truncated, "an object inside the response cap is not truncated")
	require.Equal(t, [2]int64{0, authTotalBytes}, body.Span, "the reported span must be the whole object")
}

// TestExpandSpanPaging pins that `span` reaches the resolver and that the reported span describes the
// bytes actually returned. A response whose span and content disagreed would break paging silently: the
// caller pages from the span it was told about, not from the content it received.
func TestExpandSpanPaging(t *testing.T) {
	f, text, root, _ := spanAuthObject(t)

	body := spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.Hash.String(), "span": "16384:16384"})

	require.LessOrEqual(t, body.Span[0], int64(16384), "the window must start at or before the requested offset")
	require.GreaterOrEqual(t, body.Span[1], int64(32768), "the window must reach the end of the requested span")
	require.Equal(t, text[body.Span[0]:body.Span[1]], body.Content, "the content must be the reported span")
	require.True(t, body.Truncated, "a window inside a larger object is truncated")
	require.NotEmpty(t, body.NextSpan, "a truncated window must carry a cursor to the remainder")
}

// TestReReadEmptyAtNeverReadsTheWorktree pins T13-HISTORY's central fidelity guarantee: `re_read`
// with no `at` answers from CAPTURED history, never from a live read of the working tree — even
// when the file is sitting right there on disk with nothing ever captured for it. Silently
// substituting current disk contents for a missing historical original is exactly the defect this
// pins against: a live read bypasses every capture-time policy (redaction, size bounds, host-denied
// paths) a real capture would have gone through, so treating "found on disk" as "found in history"
// would hand back bytes Qompack never actually observed.
func TestReReadEmptyAtNeverReadsTheWorktree(t *testing.T) {
	const path, text = "src/hello.ts", "export const hello = 1;\n"
	f := newFixture(t, withFiles(map[string]string{path: text}))

	var body missBody
	resp := f.callOK(t, ToolReRead, map[string]any{"path": path}, &body)

	require.False(t, resp.IsError, "a missing historical version is a domain outcome, not a failure")
	require.False(t, body.Found, "no version has ever been captured for this path")
	require.NotNil(t, body.Available, "the absence of history must be stated explicitly")
	require.False(t, *body.Available)
	require.NotContains(t, body.Reason, text, "the reason must never carry disk content")
}

// TestReReadEmptyAtReturnsCapturedVersionEvenWhenDiskDisagrees is the sharper half of the same
// guarantee: when a historical version DOES exist, re_read must return exactly that — never a
// newer version a human or another tool wrote to disk after the capture, even though that newer
// version is sitting right there and would look like a reasonable "current" answer.
func TestReReadEmptyAtReturnsCapturedVersionEvenWhenDiskDisagrees(t *testing.T) {
	const path = "src/drifted.ts"
	captured := "export const drifted = \"captured\";\n"
	onDisk := "export const drifted = \"edited after capture\";\n"
	f := newFixture(t, withFiles(map[string]string{path: onDisk}))
	f.put(t, path, captured)

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path})

	require.True(t, body.Found)
	require.Equal(t, captured, body.Content, "the captured version must win, never whatever is on disk now")
	require.NotEqual(t, onDisk, body.Content)
	require.Equal(t, sourceStore, body.Source)
}

// TestReReadFallsBackToStoreWhenFileDeleted pins the ordinary case re_read exists for: the file is
// gone from the tree, and its captured version history still answers.
func TestReReadFallsBackToStoreWhenFileDeleted(t *testing.T) {
	const path, text = "src/gone.ts", "export function gone(): void {}\n"
	f := newFixture(t, withFiles(map[string]string{path: text}))
	f.put(t, path, text)
	full := paths.Long(filepath.Join(f.Root, filepath.FromSlash(path)))
	require.NoError(t, os.Remove(full), "removing %s from the worktree", path)

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path})

	require.True(t, body.Found, "a deleted file with recorded history is still answerable")
	require.Equal(t, sourceStore, body.Source, "the bytes came from the store, and the response must say so")
	require.Equal(t, text, body.Content, "the content must be the newest recorded version")
}

// TestReReadAtTimestamp covers `at` as an RFC3339 instant: the newest version at or before it, which is
// the form a model reaches for when it knows WHEN something was true rather than which hash it had.
func TestReReadAtTimestamp(t *testing.T) {
	const path = "src/versioned.ts"
	f, early, late, _ := spanVersionedFile(t, path)

	at := epoch.Add(time.Second).Format(time.RFC3339)
	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path, "at": at})

	require.True(t, body.Found, "there is a version at or before that instant")
	require.Equal(t, early, body.Content, "a timestamp between the two versions must select the earlier one")
	require.NotEqual(t, late, body.Content, "the later version was not yet recorded at that instant")
	require.Equal(t, sourceStore, body.Source, "a historical read comes from the store")
	require.Equal(t, at, body.At, "the response must echo the point in time it answered for")
}

// TestReReadAtRootHash covers `at` as a content address: the exact bytes, with no dependence on the
// path's history at all — which is what makes it the form that still works after a file is renamed.
func TestReReadAtRootHash(t *testing.T) {
	const path = "src/hashed.ts"
	f, early, _, earlyRoot := spanVersionedFile(t, path)

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path, "at": earlyRoot.String()})

	require.True(t, body.Found, "a stored root must be found")
	require.Equal(t, early, body.Content, "the content must be the version that hash addresses")
	require.Equal(t, earlyRoot.String(), body.Hash, "the response must report the root it read")
	require.Equal(t, sourceStore, body.Source, "a hash-addressed read comes from the store")
}

// TestReReadAtTurn covers `at` as a turn index: the newest version at or BEFORE the turn, not the one
// recorded at it. Turn 7 recorded nothing, and answering "not found" for a turn between two versions
// would make the whole form useless — every turn a file was not written on would be a hole.
func TestReReadAtTurn(t *testing.T) {
	const path = "src/turned.ts"
	f, early, late, _ := spanVersionedFile(t, path)

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path, "at": "turn:7"})

	require.True(t, body.Found, "a turn between two versions resolves to the earlier one")
	require.Equal(t, early, body.Content, "turn 7 must select the version recorded at turn 4")
	require.NotEqual(t, late, body.Content, "the turn-9 version was not yet recorded at turn 7")
	require.NotNil(t, body.Turn, "a historical read must report which turn it answered from")
	require.Equal(t, int(spanTurnEarly), *body.Turn, "the reported turn must be the version's own")
}

// TestReReadSymbolSuffixAnchorsSpan pins splitPathAnchor's symbol form end to end: "file.ts:name" has to
// reach ResolveSpan as an AnchorSym, and the answer has to be the whole function rather than the chunk
// the name happened to land in.
func TestReReadSymbolSuffixAnchorsSpan(t *testing.T) {
	f, text, _, _ := spanAuthObject(t)

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": spanAuthPath + ":refreshToken"})

	require.True(t, body.Found, "the file has recorded history")
	require.Equal(t, spanAuthPath, body.Path, "the suffix must be stripped from the reported path")
	require.Positive(t, f.Widen.FindCalls, "a symbol suffix must reach the widener")
	require.Contains(t, body.Content, text[refreshTokenOff:refreshTokenEnd], "refreshToken must arrive whole")
	require.Less(t, len(body.Content), authTotalBytes, "an anchored read must narrow, not return the file")
}

// TestReReadLineSuffixAnchorsSpan pins the other suffix form, and with it splitPathAnchor's precedence
// rule: a digits-only tail is a line number, because "auth.ts:120" is unambiguous.
func TestReReadLineSuffixAnchorsSpan(t *testing.T) {
	f, text, _, _ := spanAuthObject(t)
	want := strings.TrimSuffix(strings.SplitAfter(text, "\n")[399], "\n")

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": spanAuthPath + ":400"})

	require.True(t, body.Found, "the file has recorded history")
	require.Equal(t, spanAuthPath, body.Path, "the suffix must be stripped from the reported path")
	require.Contains(t, body.Content, want, "the body must carry line 400")
	require.Less(t, len(body.Content), authTotalBytes, "an anchored read must narrow, not return the file")
}

// TestReReadPathEscapeRejected pins the containment check. A retrieval tool that read outside the project
// root on request is an exfiltration primitive, so this is an ERROR rather than a miss: a miss would read
// as "that file does not exist", which invites a second, cleverer spelling.
func TestReReadPathEscapeRejected(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolReRead, map[string]any{"path": "../../etc/passwd"})
	require.Contains(t, msg, "path escapes the project root", "the refusal must name the rule it enforced")
}

// TestReReadUnknownPathReportsExplicitUnavailable pins re_read's answer when a path has no
// captured version at all: an honest, explicit "no historical version" outcome, never a generic
// found:false that leaves the model to guess whether re_read even looked, and never disk content.
func TestReReadUnknownPathReportsExplicitUnavailable(t *testing.T) {
	f := newFixture(t)

	var body missBody
	resp := f.callOK(t, ToolReRead, map[string]any{"path": "nope.ts"}, &body)

	require.False(t, resp.IsError, "no captured history is a domain outcome, not a failure")
	require.False(t, body.Found)
	require.NotNil(t, body.Available, "availability must be stated explicitly")
	require.False(t, *body.Available)
	require.NotEmpty(t, body.Reason, "the reason must say why, not just that it is missing")
}

// TestReReadBadAtIsError pins atFormatMsg: `at` has four legal spellings and an unparseable one is an
// argument error. The message is stated once in the implementation so the tool always says the same thing
// about the same mistake, and this asserts the whole of it rather than a substring.
//
// The malformed hash and the malformed turn report the SAME sentence as "yesterday", which is the point of
// stating it once: three different mistakes about one argument teach the model one rule.
func TestReReadBadAtIsError(t *testing.T) {
	f := newFixture(t, withFiles(map[string]string{"a.ts": "export const a = 1;\n"}))

	for name, at := range map[string]string{
		"a bare word":      "yesterday",
		"a malformed hash": "sha256:notactuallyhex",
		"a malformed turn": "turn:soon",
		"a negative turn":  "turn:-",
		"an RFC3339 stub":  "2026-01-01",
	} {
		t.Run(name, func(t *testing.T) {
			msg := f.callErr(t, ToolReRead, map[string]any{"path": "a.ts", "at": at})
			require.Equal(t, "at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>", msg)
		})
	}
}

// TestReReadAtWellFormedButUnknownIsAMiss separates "you spelled it wrong" from "it is not there". A
// well-formed `at` that resolves to nothing is a semantic miss, and reporting it as an argument error
// would tell the model to change its spelling when the spelling was right.
func TestReReadAtWellFormedButUnknownIsAMiss(t *testing.T) {
	const path = "src/known.ts"
	f, _, _, _ := spanVersionedFile(t, path)

	for name, args := range map[string]map[string]any{
		"a hash nothing was stored under":     {"path": path, "at": sampleHash},
		"a turn before the first version":     {"path": path, "at": "turn:1"},
		"a turn on a path with no history":    {"path": "src/absent.ts", "at": "turn:9"},
		"an instant before the first version": {"path": path, "at": "2020-01-01T00:00:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			var body missBody
			resp := f.callOK(t, ToolReRead, args, &body)
			require.False(t, resp.IsError, "a well-formed `at` that finds nothing is a miss, not a failure")
			require.False(t, body.Found, "there is no such version")
		})
	}
}

// TestExpandUnknownToolUseIDFoundFalse covers the miss that names only the tool-use index: the id was
// never recorded, so there is no root to look for and no point reporting the object store as searched.
func TestExpandUnknownToolUseIDFoundFalse(t *testing.T) {
	f := newFixture(t)

	var body missBody
	resp := f.callOK(t, ToolExpand, map[string]any{"tool_use_id": "tu-never-recorded"}, &body)

	require.False(t, resp.IsError, "an unknown tool_use_id is a miss, not a failure")
	require.False(t, body.Found, "nothing was recorded under that id")
	require.Equal(t, "tool_use index", body.Searched, "the miss must name only the index it could search")
}

// TestExpandDeniesAToolUseRecordWhoseStoredPathEscapesTheProjectRoot pins T13-TRUST/T20-M2-04's
// authorization gate: a tool_use_id is an ADDRESS, not a credential, and expand must re-check the
// path it resolves to against the CURRENT path/symlink policy before materializing anything — a
// path that was inside the project when captured but no longer resolves safely must be refused,
// exactly as a live read of the same path would be. The refusal must say so explicitly (denied),
// never disguise itself as a plain miss, and the content must never reach the response.
func TestExpandDeniesAToolUseRecordWhoseStoredPathEscapesTheProjectRoot(t *testing.T) {
	f := newFixture(t)
	const secretText = "TOP-SECRET-CONTENT-outside-the-project"
	id := f.record(t, "Read", "../outside/secret.ts", secretText, 1)

	var body deniedBody
	resp := f.callOK(t, ToolExpand, map[string]any{"tool_use_id": string(id)}, &body)

	require.False(t, resp.IsError, "an authorization refusal is a domain outcome, not a tool failure")
	require.False(t, body.Found)
	require.True(t, body.Denied, "the refusal must be explicit, not a disguised miss")
	require.NotEmpty(t, body.Reason)
	require.NotContains(t, responseText(resp), secretText, "denied content must never reach the response")
}

// secretAWSExampleKey is AWS's own published example access key id — synthetic, never a real
// credential. It is split across a `+` so no contiguous credential-shaped string exists in this
// repository's source or history, matching internal/redact's own fixture convention.
const secretAWSExampleKey = "AKIA" + "IOSFODNN7EXAMPLE"

// TestExpandRedactsSecretsTheCaptureTimePolicyMissed pins T20-M2-04's retrieval-side secret check
// against the exact scenario it exists for: a record captured while redaction was off — an older
// build, a disabled policy, any other capture-time gap — must still never be served in the clear
// once TODAY'S policy would have caught it. withCaptureRedactionDisabled reproduces that gap by
// opening the store, and ONLY the store, without redaction: the handlers' own retrieval-side
// Redactor keeps the fixture's normal, enabled configuration, exactly as a build reading OLD data
// under a CURRENT policy would.
func TestExpandRedactsSecretsTheCaptureTimePolicyMissed(t *testing.T) {
	f := newFixture(t, withCaptureRedactionDisabled())
	body := "aws_access_key_id = " + secretAWSExampleKey + "\n"
	root, _ := f.putAndRecord(t, "Read", "config/creds.ini", body, 1)

	got := spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.String()})

	require.True(t, got.Found)
	require.NotContains(t, got.Content, secretAWSExampleKey,
		"the secret must never reach the model in the clear")
	require.Contains(t, got.Content, "«redacted:", "a redaction placeholder must stand in its place")
}

// TestReReadRedactsSecretsTheCaptureTimePolicyMissed is expand's test above, for re_read's own
// content path — the two tools share resolveContent but build their response bodies separately,
// so the redaction call has to be pinned on both.
func TestReReadRedactsSecretsTheCaptureTimePolicyMissed(t *testing.T) {
	const path = "config/creds2.ini"
	f := newFixture(t, withCaptureRedactionDisabled())
	f.put(t, path, "aws_access_key_id = "+secretAWSExampleKey+"\n")

	body := spanContentOf(t, f, ToolReRead, map[string]any{"path": path})

	require.True(t, body.Found)
	require.NotContains(t, body.Content, secretAWSExampleKey)
	require.Contains(t, body.Content, "«redacted:")
}

// TestExpandRedactionNeverLogsTheSecret pins the log-safety half of the same requirement: a
// diagnostic ABOUT a redaction must never itself carry the redacted text.
func TestExpandRedactionNeverLogsTheSecret(t *testing.T) {
	f := newFixture(t, withCaptureRedactionDisabled())
	log := f.withSpyLogger(t)
	body := "aws_access_key_id = " + secretAWSExampleKey + "\n"
	root, _ := f.putAndRecord(t, "Read", "config/creds3.ini", body, 1)

	spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.String()})

	require.Equal(t, 1, log.loudCount(), "a caught secret is a Loud, observable event")
	require.NotContains(t, log.lastLoud(t), secretAWSExampleKey, "no log line may carry the secret")
}

// TestContentToolsMarkRetrievedTextAsUntrusted pins that expand, re_read and recall all flag their
// output as untrusted retrieved data via _meta.qompack.untrusted, so a host or model reading _meta
// knows this text has provenance elsewhere and must not be treated as an instruction.
func TestContentToolsMarkRetrievedTextAsUntrusted(t *testing.T) {
	f, c := newSeededFixture(t)

	expandResp := f.call(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()})
	require.Equal(t, true, expandResp.Meta[metaUntrusted], "expand must mark its content untrusted")

	reReadResp := f.call(t, ToolReRead, map[string]any{"path": spanAuthPath})
	require.Equal(t, true, reReadResp.Meta[metaUntrusted], "re_read must mark its content untrusted")

	recallResp := f.call(t, ToolRecall, map[string]any{"query": "pool timeout"})
	require.Equal(t, true, recallResp.Meta[metaUntrusted], "recall must mark its previews untrusted")
}

// TestContentToolsWithoutAStoreReportUnavailable pins handlers_common.go's third state. "This build cannot
// answer the question" is a different fact from "the answer is no", and collapsing the two would have a
// model conclude a file never existed because the store was not wired in.
func TestContentToolsWithoutAStoreReportUnavailable(t *testing.T) {
	f := newFixture(t, withoutStore())

	for name, args := range map[string]map[string]any{
		ToolExpand: {"hash": sampleHash},
		ToolReRead: {"path": "src/anything.ts"},
	} {
		t.Run(name, func(t *testing.T) {
			var body missBody
			resp := f.callOK(t, name, args, &body)
			require.False(t, resp.IsError, "an unwired collaborator is not a tool failure")
			require.NotNil(t, body.Available, "availability must be stated, not left to be inferred from found")
			require.False(t, *body.Available, "this build cannot answer at all")
			require.Equal(t, "store not present in this build", body.Reason, "the reason must name what is missing")
		})
	}
}

// TestContentToolsWithoutARedactorFailClosed pins the direction the missing-redactor case must
// fail in (T20-M2-04).
//
// A build with no retrieval-side Redactor cannot tell a clean record from one captured before
// today's rules existed. The only honest answer is therefore to serve no archive text — never the
// bytes. The failure this test exists to make impossible is the OTHER direction: a handler that
// reads a nil redactor as "nothing to redact" and passes the content through, which would turn one
// wiring omission into every secret in the archive being served in the clear, silently and forever.
// The seeded content is a real credential shape, so a regression would not be a mere shape change:
// the secret itself would appear in the response.
//
// expand and re_read report themselves UNAVAILABLE, because archive text is the whole of what they
// return. recall keeps its pointers — a hash and a path were never redaction's subject — and
// withholds every summary, which is the one piece of archive text it renders.
func TestContentToolsWithoutARedactorFailClosed(t *testing.T) {
	f := newFixture(t, withoutRedactor())
	const path = "config/failclosed.ini"
	body := "aws_access_key_id = " + secretAWSExampleKey + "\n"
	root, id := f.putAndRecord(t, "Read", path, body, 1)
	require.NotEmpty(t, id, "fixture sanity: the record the tools are asked for must exist")

	for name, args := range map[string]map[string]any{
		ToolExpand: {"hash": root.String()},
		ToolReRead: {"path": path},
	} {
		t.Run(name, func(t *testing.T) {
			var miss missBody
			resp := f.callOK(t, name, args, &miss)

			require.False(t, resp.IsError, "an unwired collaborator is not a tool failure")
			require.NotNil(t, miss.Available, "availability must be stated, not inferred")
			require.False(t, *miss.Available, "with no redactor this build cannot answer at all")
			require.Equal(t, redactorMissingReason, miss.Reason, "the reason must name what is missing")
			require.False(t, miss.Found, "nothing was served")
			require.NotContains(t, responseText(resp), secretAWSExampleKey,
				"a missing redactor must never degrade into serving the secret in the clear")
			require.NotContains(t, responseText(resp), "aws_access_key_id",
				"no span of the record may be served")
		})
	}

	t.Run(ToolRecall, func(t *testing.T) {
		var hits recallBody
		resp := f.callOK(t, ToolRecall, map[string]any{"query": "aws_access_key_id"}, &hits)

		require.False(t, resp.IsError, "an unwired collaborator is not a tool failure")
		require.True(t, hits.Found, "the pointers are still true, and still worth returning")
		require.NotEmpty(t, hits.Hits, "fixture sanity: the seeded record must be findable")
		require.True(t, hits.SummariesWithheld, "the withholding must be stated, never silent")
		require.Equal(t, redactorMissingReason, hits.Reason)
		for _, hit := range hits.Hits {
			require.Empty(t, hit.Summary, "no summary may be rendered without today's policy behind it")
		}
		require.NotContains(t, responseText(resp), secretAWSExampleKey,
			"a missing redactor must never degrade into serving the secret in the clear")
	})
}

// TestRecallSummariesSurviveARedactor is TestContentToolsWithoutARedactorFailClosed's positive
// control: it is the same query against the same fixture WITH a redactor, so "the summaries were
// withheld" cannot pass vacuously because recall returns no summaries anyway.
func TestRecallSummariesSurviveARedactor(t *testing.T) {
	f := newFixture(t)
	const path = "config/withredactor.ini"
	f.putAndRecord(t, "Read", path, "aws_access_key_id = "+secretAWSExampleKey+"\n", 1)

	var hits recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "aws_access_key_id"}, &hits)

	require.True(t, hits.Found)
	require.False(t, hits.SummariesWithheld, "a wired redactor withholds nothing")
	require.Empty(t, hits.Reason)
	var summarized int
	for _, hit := range hits.Hits {
		if hit.Summary == "" {
			continue
		}
		summarized++
		require.NotContains(t, hit.Summary, secretAWSExampleKey,
			"a rendered summary passes through today's policy, never in the clear")
	}
	require.NotZero(t, summarized, "recall must render summaries when it can re-check them")
}

// TestContentToolsWithoutAPromoterStillAnswer pins that counting is a signal for the next checkpoint and
// never a precondition for answering: with no Promoter wired, expand still returns its bytes and reports
// an honest zero rather than failing or inventing a count.
func TestContentToolsWithoutAPromoterStillAnswer(t *testing.T) {
	f := newFixture(t, withoutPromoter())
	text := buildFiller("unpromoted", spanSmallBytes)
	root := spanRootOf(t, f, f.put(t, "src/unpromoted.ts", text))

	body := spanContentOf(t, f, ToolExpand, map[string]any{"hash": root.Hash.String()})

	require.True(t, body.Found, "a missing Promoter must not stop a retrieval")
	require.Equal(t, text, body.Content, "the content must still be returned in full")
	require.Zero(t, body.Expansions, "an uncounted expansion is reported as zero, not as one")
	require.False(t, body.Promoted, "nothing can be promoted without a Promoter")
}
