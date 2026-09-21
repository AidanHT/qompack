package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The publication accounting is the production discovery V6-RECOVERY-1 says is missing. These tests
// pin its criteria against `qompack fsck`'s own (internal/cli/fsck.go checkCaptures/checkObjects) and
// against the four properties its doc comment promises — the gap criterion, incomplete-is-not-empty,
// no false positives (prompt/denied/empty captures, KeepRaw objects), and the explicit budgets.
//
// They live in package store because they seed the same unexported paths the scanner walks
// (objectPath, the pending helper) and check the in-memory index membership objects are decided by.

// auditObsID mints a valid observation identity (a 64-hex digest) from a seed.
func auditObsID(seed string) core.ObservationID {
	return core.ObservationID(core.HashBytes("audit.test.obs", []byte(seed)).String())
}

// seedCapture writes one capture sidecar with the op, published flag, outcome and (optional) bytes a
// real dispatch leaves. Non-empty body makes WriteCaptureSidecar record a durable BytesHash.
func seedCapture(t *testing.T, root, seed, op string, published bool, outcome core.EvidenceOutcome, body []byte) {
	t.Helper()
	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: auditObsID(seed),
		Session:       "sess-audit",
		Op:            op,
		Published:     published,
		Outcome:       outcome,
		Bytes:         body,
	}))
}

// writeRawSidecar drops arbitrary bytes at a valid sidecar path, for the schema/parse cases a
// well-formed WriteCaptureSidecar cannot produce.
func writeRawSidecar(t *testing.T, root, seed string, content []byte) {
	t.Helper()
	p, err := CaptureSidecarPath(root, auditObsID(seed))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), content, 0o600))
}

// writeBareObject writes raw bytes to h's on-disk object path WITHOUT an index line, standing in for
// the content a crash between putObject and appendRoot leaves. The accounting never reads or hashes
// object bytes, so the content need not be the real object; only the content-addressed NAME matters.
func writeBareObject(t *testing.T, tp *testProject, h core.Hash) {
	t.Helper()
	p := tp.Store.objectPath(h)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("bytes with no index line"), 0o600))
}

// TestAuditPublication_ClassifiesCaptureSidecars pins fsck's own gap criterion: the ONLY gap is a
// tool/stop delivery with an ok outcome and durable bytes, still unpublished. Everything else —
// published, prompt, denied, ok-without-bytes — is legitimately unpublished, never a gap.
func TestAuditPublication_ClassifiesCaptureSidecars(t *testing.T) {
	tp := newTestStore(t)
	body := []byte("captured tool result")

	seedCapture(t, tp.Root, "tool-published", auditOpObserveTool, true, core.OutcomeOK, body)
	seedCapture(t, tp.Root, "tool-gap", auditOpObserveTool, false, core.OutcomeOK, body)
	seedCapture(t, tp.Root, "stop-gap", auditOpObserveStop, false, core.OutcomeOK, []byte(`{"hook_event_name":"SubagentStop"}`))
	seedCapture(t, tp.Root, "prompt-open", auditOpObservePrompt, false, core.OutcomeOK, body)
	seedCapture(t, tp.Root, "tool-denied", auditOpObserveTool, false, core.OutcomeDenied, nil)
	seedCapture(t, tp.Root, "tool-ok-nobytes", auditOpObserveTool, false, core.OutcomeOK, nil)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Equal(t, 6, a.CapturesScanned, "every sidecar on disk must be examined")
	require.Equal(t, 3, a.UnpublishedCaptures,
		"tool, prompt and subagent stop require a publication reference under the current contract")
	require.Equal(t, 2, a.LegitimatelyUnpublished,
		"denied and ok-without-bytes captures require no reference")
	require.True(t, a.HasGaps())
	require.False(t, a.Incomplete,
		"a fully readable, known-schema, known-outcome tree is a COMPLETE accounting")
}

// TestAuditPublication_DeniedCaptureIsNotAGap isolates the denied case main called out: a denied
// capture admitted no bytes and must never be promoted to a publication gap.
func TestAuditPublication_DeniedCaptureIsNotAGap(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "denied", auditOpObserveTool, false, core.OutcomeDenied, nil)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures, "a denied capture is not a gap")
	require.Equal(t, 1, a.LegitimatelyUnpublished)
	require.False(t, a.HasGaps())
	require.False(t, a.Incomplete)
}

// TestAuditPublication_EmptyOkCaptureIsNotAGap isolates the empty case: an ok outcome with no durable
// bytes has nothing to publish a reference to.
func TestAuditPublication_EmptyOkCaptureIsNotAGap(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "empty-ok", auditOpObserveTool, false, core.OutcomeOK, nil)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures, "an ok capture with no durable bytes is not a gap")
	require.Equal(t, 1, a.LegitimatelyUnpublished)
	require.False(t, a.Incomplete)
}

// TestAuditPublication_UnknownOutcomeIsIncomplete asserts a missing or future outcome is not this
// build's to interpret: incomplete, not a silent non-gap.
func TestAuditPublication_UnknownOutcomeIsIncomplete(t *testing.T) {
	tp := newTestStore(t)
	// A well-formed, current-version sidecar whose outcome is a value this build does not know.
	writeRawSidecar(t, tp.Root, "weird-outcome",
		[]byte(`{"v":1,"op":"observe.tool","published":false,"outcome":"martian","bytes_hash":"sha256:`+
			zeroHex()+`"}`))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures)
	require.True(t, a.Incomplete, "an unrecognized outcome makes the accounting incomplete")
	require.NotEmpty(t, a.Notes)
}

// TestAuditPublication_UnknownOpIsIncomplete asserts an unpublished sidecar whose op this build does
// not recognize is incomplete, not classified as clean or as a gap.
func TestAuditPublication_UnknownOpIsIncomplete(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "weird-op",
		[]byte(`{"v":1,"op":"observe.martian","published":false,"outcome":"ok","bytes_hash":"sha256:`+
			zeroHex()+`"}`))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures)
	require.True(t, a.Incomplete, "an unrecognized op makes the accounting incomplete")
}

// TestAuditPublication_FutureSchemaIsIncompleteNotEmpty writes a sidecar from a newer build. Published
// under an unknown schema is not this build's to call clean or a gap.
func TestAuditPublication_FutureSchemaIsIncompleteNotEmpty(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "future",
		[]byte(`{"v":99,"op":"observe.tool","published":false,"outcome":"ok","bytes_hash":"sha256:`+
			nonzeroHex()+`"}`))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Equal(t, 1, a.CapturesScanned)
	require.Zero(t, a.UnpublishedCaptures, "a newer schema's fields are not this build's to call a gap")
	require.True(t, a.Incomplete)
	require.NotEmpty(t, a.Notes)
}

// TestAuditPublication_MissingOrOlderSchemaIsIncomplete asserts version must equal the known one, not
// merely be no greater: a v0 (missing) or older record is unreadable, hence incomplete.
func TestAuditPublication_MissingOrOlderSchemaIsIncomplete(t *testing.T) {
	tp := newTestStore(t)
	// No "v" at all → decodes as version 0 → unreadable version.
	writeRawSidecar(t, tp.Root, "no-version",
		[]byte(`{"op":"observe.tool","published":false,"outcome":"ok","bytes_hash":"sha256:`+nonzeroHex()+`"}`))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures,
		"a record whose version is not exactly the known one must not be read as a gap")
	require.True(t, a.Incomplete)
}

// TestAuditPublication_MalformedTrailingJSONIsIncomplete asserts a sidecar with a valid object
// followed by trailing garbage is caught, not silently accepted the way a streaming decoder would.
func TestAuditPublication_MalformedTrailingJSONIsIncomplete(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "trailing",
		[]byte(`{"v":1,"op":"observe.tool","published":false,"outcome":"ok","bytes_hash":"sha256:`+
			nonzeroHex()+`"} and then some garbage`))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Zero(t, a.UnpublishedCaptures)
	require.True(t, a.Incomplete, "trailing garbage after a valid object must make the pass incomplete")
}

// TestAuditPublication_UnreadableSidecarIsIncomplete asserts a corrupt sidecar is incomplete, not a
// silent zero: a stage-one gap is exactly the state a torn record hides in.
func TestAuditPublication_UnreadableSidecarIsIncomplete(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "garbage", []byte("{ this is not json"))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Equal(t, 1, a.CapturesScanned)
	require.Zero(t, a.UnpublishedCaptures)
	require.True(t, a.Incomplete)
}

// TestAuditPublication_UnindexedObjectCandidate writes content into objects/ with no index line and
// asserts the pass names it as a CANDIDATE (honestly: it may be a crash-orphan or a GC leftover).
func TestAuditPublication_UnindexedObjectCandidate(t *testing.T) {
	tp := newTestStore(t)
	h := core.HashBytes(core.DomainChunk, []byte("orphaned content with no index line"))
	writeBareObject(t, tp, h)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Equal(t, 1, a.ObjectsScanned)
	require.Equal(t, 1, a.UnindexedObjectCandidates,
		"an object no live index chunk references is an unindexed candidate")
	require.Zero(t, a.PendingObjects)
	require.True(t, a.HasGaps(), "the F4-1 cut must still surface")
	require.False(t, a.Incomplete)
}

// TestAuditPublication_IndexedObjectsAreNotCandidates asserts a normally-published Put leaves nothing
// to flag: membership is decided against the loaded index, by name.
func TestAuditPublication_IndexedObjectsAreNotCandidates(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	_, err := tp.Store.PutBytes(ctx, []byte("ordinary published tool result\n"),
		PutOptions{Tool: "FileRead", Path: "src/a.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, tp.objectPaths(t), "fixture sanity: the Put wrote at least one object")

	a, err := tp.Store.AuditPublication(ctx, DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Positive(t, a.ObjectsScanned)
	require.Zero(t, a.UnindexedObjectCandidates,
		"every object a Put wrote is referenced by its index line")
	require.False(t, a.HasGaps())
}

// TestAuditPublication_KeepRawPutCreatesNoCandidates is the false-positive guard main asked for. A
// KeepRaw Put whose canonicalization changes bytes writes a recovery side record (a delta or a
// retained original) as its own root through appendRoot, so its objects land in chunkSet. If the
// accounting used anything narrower than "referenced by any live index chunk", that side record's
// object would be flagged as an orphan.
func TestAuditPublication_KeepRawPutCreatesNoCandidates(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	// CRLF is normalized by the always-on structural canonicalization (§4), so KeepRaw records a
	// recovery side record rather than taking the verbatim shortcut.
	_, err := tp.Store.PutBytes(ctx, []byte("line one\r\nline two\r\nline three\r\n"),
		PutOptions{Tool: "Bash", Path: "src/crlf.log", KeepRaw: true})
	require.NoError(t, err)

	roots := tp.indexLines(t, "roots.jsonl")
	require.GreaterOrEqual(t, len(roots), 2,
		"fixture sanity: a KeepRaw put over CRLF content must record a recovery side record too")

	a, err := tp.Store.AuditPublication(ctx, DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Positive(t, a.ObjectsScanned)
	require.Zero(t, a.UnindexedObjectCandidates,
		"a KeepRaw side record's object is a live root's chunk and must never be flagged as an orphan")
	require.False(t, a.HasGaps())
}

// TestAuditPublication_PendingObjectIsNotACandidate covers an unindexed object an in-flight
// pending-write marker names: a Put whose root line has not landed is recovery's business.
func TestAuditPublication_PendingObjectIsNotACandidate(t *testing.T) {
	tp := newTestStore(t)
	h := core.HashBytes(core.DomainChunk, []byte("in-flight write, root line not yet appended"))
	writeBareObject(t, tp, h)

	pw := tp.Store.pending(core.HashBytes(core.DomainChunk, []byte("some-root")), []ChunkRef{{Hash: h, Len: 4}})
	require.NotNil(t, pw, "fixture sanity: the pending marker must have been written")

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.Equal(t, 1, a.PendingObjects, "an object a pending marker covers is in-flight, not a gap")
	require.Zero(t, a.UnindexedObjectCandidates)
	require.False(t, a.HasGaps())
}

// TestAuditPublication_EmptyProjectIsCleanAndComplete asserts the healthy answer is distinguishable
// from every degraded one.
func TestAuditPublication_EmptyProjectIsCleanAndComplete(t *testing.T) {
	tp := newTestStore(t)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.False(t, a.HasGaps())
	require.False(t, a.Incomplete,
		"a project with no captures and no objects has a COMPLETE, gapless accounting")
	require.False(t, a.Truncated)
	require.Empty(t, a.Notes)
}

// TestAuditPublication_RespectsTheCaptureCap asserts the explicit count cap stops the walk and reports
// it: a truncated scan is incomplete and its counts are a lower bound.
func TestAuditPublication_RespectsTheCaptureCap(t *testing.T) {
	tp := newTestStore(t)
	body := []byte("x")
	for i := 0; i < 5; i++ {
		seedCapture(t, tp.Root, "capped-"+string(rune('a'+i)), auditOpObserveTool, false, core.OutcomeOK, body)
	}

	a, err := tp.Store.AuditPublication(context.Background(),
		PublicationScanCap{MaxCaptures: 1, MaxObjects: 1, MaxEntries: 1_000, MaxBytes: 1 << 20})
	require.NoError(t, err)

	require.Equal(t, 1, a.CapturesScanned, "the scan must stop at the cap")
	require.True(t, a.Truncated, "a capped scan must report truncation")
	require.True(t, a.Incomplete, "truncation always implies incompleteness")
	require.LessOrEqual(t, a.UnpublishedCaptures, 1,
		"a truncated pass's gap count is a lower bound, not the tree's true size")
}

// TestAuditPublication_RespectsTheEntryBudget asserts the shared directory-entry budget is real: a
// pass that would visit more entries than its budget stops and reports truncation.
func TestAuditPublication_RespectsTheEntryBudget(t *testing.T) {
	tp := newTestStore(t)
	body := []byte("x")
	// Several captures whose ids fall in different shard directories, so the captures root alone holds
	// more than one entry to visit.
	for i := 0; i < 6; i++ {
		seedCapture(t, tp.Root, "budget-"+string(rune('a'+i)), auditOpObserveTool, false, core.OutcomeOK, body)
	}

	a, err := tp.Store.AuditPublication(context.Background(),
		PublicationScanCap{MaxCaptures: 100, MaxObjects: 100, MaxEntries: 1, MaxBytes: 1 << 20})
	require.NoError(t, err)

	require.True(t, a.Truncated, "an entry budget of 1 over a multi-entry tree must truncate")
	require.True(t, a.Incomplete)
}

// TestAuditPublication_ClosedStoreDegrades asserts a store that cannot be asked at all reports it.
func TestAuditPublication_ClosedStoreDegrades(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())

	_, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.ErrorIs(t, err, core.ErrDegraded,
		"a closed store must degrade rather than report a clean accounting of state it can no longer read")
}

// TestAuditPublication_HonoursCancellation asserts a cancelled context stops the pass and reports it
// (main wraps a short startup deadline around the call).
func TestAuditPublication_HonoursCancellation(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "cancel", auditOpObserveTool, false, core.OutcomeOK, []byte("x"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tp.Store.AuditPublication(ctx, DefaultPublicationScanCap())
	require.ErrorIs(t, err, context.Canceled,
		"a cancelled context must be honoured rather than walked to completion")
}

// zeroHex is 64 hex zeros: the text of the zero hash, an absent bytes_hash.
func zeroHex() string { return core.Hash{}.String()[len("sha256:"):] }

// nonzeroHex is the hex of a real, non-zero digest, for a durable bytes_hash in a raw sidecar.
func nonzeroHex() string {
	return core.HashBytes(core.DomainChunk, []byte("durable")).String()[len("sha256:"):]
}

func TestAuditPublication_StopReferenceRequirementIsQualified(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       []byte
		outcome    core.EvidenceOutcome
		gaps       int
		incomplete bool
	}{
		{"ordinary stop", []byte(`{"hook_event_name":"Stop"}`), core.OutcomeOK, 0, false},
		{"subagent stop", []byte(`{"hook_event_name":"SubagentStop"}`), core.OutcomeOK, 1, false},
		{"unknown stop", []byte(`{"future_field":true}`), core.OutcomeOK, 0, true},
		{"denied stop", nil, core.OutcomeDenied, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			seedCapture(t, tp.Root, tc.name, auditOpObserveStop, false, tc.outcome, tc.body)
			audit, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
			require.NoError(t, err)
			require.Equal(t, tc.gaps, audit.UnpublishedCaptures)
			require.Equal(t, tc.incomplete, audit.Incomplete)
		})
	}
}
