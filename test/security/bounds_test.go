package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Decode and size bounds: what the product does when the bytes on disk, or the bytes a host hands
// it, are hostile rather than merely absent.
//
// Every case here drives the real store and the packaged binary. The invariant under all of them is
// 00-ARCHITECTURE.md §12.3's: a corrupt object is quarantined, Loud, and the process CONTINUES —
// never a crash, never a silent empty answer, and never an allocation sized by whatever the corrupt
// bytes claim.

const (
	// boundsSession is the session the bounds cases drive.
	boundsSession = core.SessionID("sess-security-bounds-0001")
	// oversizePhysicalBytes is far past encodedObjectLimit() (zstd's worst-case framing over
	// store.MaxPutBytes, a little above 64 MiB). The file is created by Truncate, so on every
	// filesystem this repository supports it costs no real blocks — nothing ever reads it, which is
	// the point: readBoundedObject stats first and refuses before the first byte.
	oversizePhysicalBytes = 128 << 20
	// bombPlainBytes is how much the decompression bomb expands to: past store's 64 MiB
	// maxDecodedSize, so the bounded decoder must refuse it instead of allocating it.
	bombPlainBytes = 80 << 20
	// tinyResponseBytes is the smallest runtime.mcp.maxResponseBytes configuration accepts.
	tinyResponseBytes = 4096
	// hookCaptureHardCapBytes is internal/cli's hookCaptureMaxBytes, restated here because it is
	// unexported: the allocation bound no configuration may raise.
	hookCaptureHardCapBytes = 4 << 20
	// overCapPayloadBytes is the runtime.hotPath.maxPayloadBytes an operator is imagined to have
	// set: comfortably above the hard cap, so the two bounds cannot be confused for one another.
	overCapPayloadBytes = 64 << 20
)

// corruption is one way an object on disk can be hostile.
type corruption struct {
	name string
	// damage rewrites the object file at p, which is the .zst this store wrote.
	damage func(t *testing.T, p string)
	// reason is what the record says was measured.
	reason string
}

// TestSecurity_MalformedObjectsAreQuarantinedAndBounded seeds five hostile objects and asks the
// store, the daemon and the packaged MCP server for each in turn.
func TestSecurity_MalformedObjectsAreQuarantinedAndBounded(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	corruptions := []corruption{
		{
			name:   "damaged_zstd_frame",
			reason: "a zstd frame with its middle overwritten",
			damage: func(t *testing.T, path string) {
				t.Helper()
				raw, err := os.ReadFile(paths.Long(path))
				require.NoError(t, err)
				require.Greater(t, len(raw), 16, "the seeded object must be long enough to damage")
				for i := len(raw) / 2; i < len(raw)/2+8 && i < len(raw); i++ {
					raw[i] ^= 0xFF
				}
				require.NoError(t, os.WriteFile(paths.Long(path), raw, 0o600))
			},
		},
		{
			name:   "truncated_object",
			reason: "a zstd frame cut in half",
			damage: func(t *testing.T, path string) {
				t.Helper()
				raw, err := os.ReadFile(paths.Long(path))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(path), raw[:len(raw)/2], 0o600))
			},
		},
		{
			name:   "physical_size_past_the_read_bound",
			reason: "an object file larger than encodedObjectLimit()",
			damage: func(t *testing.T, path string) {
				t.Helper()
				f, err := os.OpenFile(paths.Long(path), os.O_RDWR, 0o600)
				require.NoError(t, err)
				require.NoError(t, f.Truncate(oversizePhysicalBytes))
				require.NoError(t, f.Close())
			},
		},
		{
			name:   "decompression_bomb",
			reason: "a valid zstd frame expanding past store's 64 MiB maxDecodedSize",
			damage: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.WriteFile(paths.Long(path), zstdBomb(t), 0o600))
			},
		},
		{
			name:   "content_hash_mismatch",
			reason: "a valid frame of the right length whose plaintext hashes to another address",
			// The substitution keeps the plaintext LENGTH identical and changes one byte of it.
			// That matters: the read path checks the indexed length before it checks the content
			// address, so any shorter or longer substitution is refused by the length check and the
			// verify-on-read hash — the check that actually defends against a swapped object — is
			// never reached. Flipping one byte is what forces the refusal through core.HashBytes.
			damage: func(t *testing.T, path string) {
				t.Helper()
				raw, err := os.ReadFile(paths.Long(path))
				require.NoError(t, err)
				plain, err := store.Decode(raw)
				require.NoError(t, err, "the seeded object must be a valid frame before it is damaged")
				require.NotEmpty(t, plain)
				plain[len(plain)/2] ^= 0x01
				encoded, err := store.Encode(plain)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(path), encoded, 0o600))
			},
		},
	}

	// Seed one object per corruption, plus one healthy control the survivors are measured against.
	s := openStoreAt(t, p.Root)
	ctx := context.Background()
	seeded := map[string]core.Hash{}
	ids := map[string]string{}
	for i, c := range corruptions {
		content := fmt.Sprintf("// seeded object %d for %s\n%s\n", i, c.name, strings.Repeat(
			fmt.Sprintf("const corrupt%02d = %d;\n", i, i), 64))
		res, err := s.PutBytes(ctx, []byte(content), store.PutOptions{Tool: "Read", Path: fmt.Sprintf("src/c%02d.ts", i)})
		require.NoError(t, err)
		require.NotEmpty(t, res.Root.Chunks, "the seeded object must name its chunks")
		seeded[c.name] = res.Root.Chunks[0].Hash

		id := fmt.Sprintf("toolu_security_bounds_%02d", i)
		ids[c.name] = id
		require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
			ID: core.ToolUseID(id), Session: boundsSession, TS: core.NowMilli(core.SystemClock()),
			Tool: "Read", Root: res.Root.Hash, Path: fmt.Sprintf("src/c%02d.ts", i), Bytes: res.Root.RawBytes,
		}))
	}
	healthy, err := s.PutBytes(ctx, []byte("// the healthy control object\n"+deniedMarker+"\n"),
		store.PutOptions{Tool: "Read", Path: "src/healthy.ts"})
	require.NoError(t, err)
	require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
		ID: core.ToolUseID("toolu_security_bounds_healthy"), Session: boundsSession,
		TS: core.NowMilli(core.SystemClock()), Tool: "Read", Root: healthy.Root.Hash, Path: "src/healthy.ts",
	}))
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())

	// Damage them all, then read them all back through a fresh store handle.
	for _, c := range corruptions {
		c.damage(t, objectFilePath(p.Root, seeded[c.name]))
	}

	reader := openStoreAt(t, p.Root)
	for _, c := range corruptions {
		rec := newRecord(t, "bounds_"+c.name)
		rec.Capability = CapBounds

		_, err := reader.GetChunk(ctx, seeded[c.name])
		notFound := errors.Is(err, core.ErrNotFound)
		quarantined := quarantineHolds(t, p.Root, seeded[c.name])

		require.Error(t, err, "%s: a hostile object must never be served", c.name)
		require.True(t, notFound, "%s: the refusal must be core.ErrNotFound, got %v", c.name, err)

		switch {
		case quarantined:
			rec.Outcome = OutcomeVerified
			rec.Reason = fmt.Sprintf("%s was refused with core.ErrNotFound and moved to "+
				".qompack/tmp/quarantine/ (§12.3): %s.", c.name, c.reason)
		default:
			rec.Outcome = OutcomeFailed
			rec.Reason = fmt.Sprintf("%s was refused with core.ErrNotFound but the bytes were NOT "+
				"quarantined; owner: internal/store. %s.", c.name, c.reason)
			t.Errorf("§12.3: %s was not quarantined", c.name)
		}
		rec.Detail = "read error: " + err.Error()
		writeRecord(t, rec)
	}
	require.NoError(t, reader.Close())

	// The daemon and the packaged MCP server must survive every one of them.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, boundsSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)

	envelopes := map[string]string{}
	for _, c := range corruptions {
		res := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": ids[c.name], "full": true})
		require.NotContains(t, res.Text, `"found":true`,
			"%s: a corrupt object must never be reported as found", c.name)
		require.NotContains(t, res.Text, `"found": true`,
			"%s: a corrupt object must never be reported as found", c.name)
		envelopes[c.name] = describeEnvelope(res)
	}

	// The SECOND address form. `expand` by a bare chunk hash takes a different branch: GetRoot
	// misses, the handler tries GetChunk as the "you pasted a chunk hash" courtesy, and a
	// quarantine refusal there is mapped to miss("object store …") — which renders as ABSENT.
	// Absent is the one answer §12.3 forbids for a quarantined object: it tells a model the content
	// was never there, when what happened is that the bytes were refused and preserved.
	bareEnvelopes := map[string]string{}
	for _, c := range corruptions {
		res := child.call(t, mcp.ToolExpand,
			map[string]any{"hash": seeded[c.name].String(), "full": true})
		require.NotContains(t, res.Text, `"found":true`,
			"%s by bare hash: a corrupt object must never be reported as found", c.name)
		bareEnvelopes[c.name] = describeEnvelope(res)
	}

	// Still alive, still answering, and the healthy object still comes back.
	alive := child.call(t, mcp.ToolExpand,
		map[string]any{"tool_use_id": "toolu_security_bounds_healthy", "full": true})
	require.False(t, alive.IsError, "the server must keep serving healthy objects: %s", alive.Text)
	require.Contains(t, alive.Text, deniedMarker, "the healthy control object must still expand")
	child.finish(t)

	_, held := daemonHoldingLock(p.Root)
	require.True(t, held, "the daemon must still be running after five hostile objects")

	// A checkpoint must still complete rather than stalling on a dangling pointer.
	stdout, _, code := run(t, b.Bin, p.Root, []string{"checkpoint"},
		preCompactPayload(t, p.Root, boundsSession), p.Env)
	require.Equal(t, 0, code, "the checkpoint hook must exit 0 even over a corrupt store")
	require.True(t, emptyOrParseableJSON(stdout), "the checkpoint hook must write a host-parseable document")

	rec := newRecord(t, "bounds_retrieval_and_checkpoint_survive_corruption")
	rec.Capability = CapBounds
	rec.Outcome = OutcomeVerified
	rec.Reason = "after five hostile objects the daemon still held its lock, the MCP server still " +
		"expanded the healthy control object, and the checkpoint hook still exited 0 with a " +
		"host-parseable document. No corrupt object was ever reported found."
	rec.Detail = "by tool_use_id: " + renderShapes(envelopes)
	writeRecord(t, rec)

	// The envelope divergence gets its OWN record, because a measured divergence with an owner is a
	// `failed` row and burying it in the detail line of a `verified` one is how a finding stops
	// being counted. Both address forms are named, because they are two call sites and a fix to one
	// leaves the other.
	env := newRecord(t, "bounds_corrupt_object_envelope")
	env.Capability = CapBounds
	env.Detail = "by tool_use_id: " + renderShapes(envelopes) + "; by bare chunk hash: " +
		renderShapes(bareEnvelopes)
	env.Outcome, env.Reason = judgeCorruptEnvelopes(envelopes, bareEnvelopes)
	writeRecord(t, env)
}

// judgeCorruptEnvelopes decides what the two address forms reported for a quarantined object.
//
// The vocabulary is the point. §12.3 makes a quarantined object a degradation to be continued
// through, and the retrieval layer already has the right word for that: `unavailable`, an explicit
// third answer beside found and not-found. A protocol-level tool error is not that word, and
// `miss` is the opposite of it.
func judgeCorruptEnvelopes(byID, byHash map[string]string) (Outcome, string) {
	countShape := func(m map[string]string, prefix string) int {
		n := 0
		for _, v := range m {
			if strings.HasPrefix(v, prefix) {
				n++
			}
		}
		return n
	}
	toolErrors := countShape(byID, "tool_error")
	misses := countShape(byHash, "miss")
	if toolErrors == 0 && misses == 0 {
		return OutcomeVerified, "a quarantined object is reported unavailable through both address " +
			"forms: the explicit third answer, neither a protocol error nor an absence."
	}
	return OutcomeFailed, fmt.Sprintf(
		"a quarantined object does not reach the model as the `unavailable` domain outcome. By "+
			"tool_use_id, %d of %d came back as a protocol-level tool error (internal/mcp's expand "+
			"and re_read render a store refusal through errResponse). By bare chunk hash, %d of %d "+
			"came back as `miss` — that is, as ABSENT, which §12.3 forbids for a quarantined object "+
			"because it tells a model the content was never there when in fact it was refused and "+
			"preserved; internal/mcp/handlers_span.go maps the chunk-hash courtesy branch's failure "+
			"straight to miss(). internal/mcp/unavailable_test.go already establishes the right "+
			"answer for already_tried's equivalent backend failure. No path and no secret appears in "+
			"any of these answers — they carry a 12-character hash prefix. Owner: internal/mcp.",
		toolErrors, len(byID), misses, len(byHash))
}

// TestSecurity_HookCaptureCapBoundsAnOversizePayload drives a tool_response past the hard 4 MiB
// capture cap and asserts the refusal keeps only a bounded prefix.
//
// The marker is planted 64 KiB into the payload — well past the 4 KiB the refusal retains — so its
// absence everywhere under .qompack/ is a measurement of the prefix bound rather than of whether
// the payload was stored at all.
func TestSecurity_HookCaptureCapBoundsAnOversizePayload(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "bounds_hook_capture_cap")
	rec.Capability = CapBounds

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, boundsSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")

	const marker = "PAST-THE-REFUSAL-PREFIX-6c19ad"
	payload := oversizeToolPayload(t, p.Root, boundsSession, "toolu_security_oversize_01", marker)
	require.Greater(t, len(payload), hookOversizeBytes, "the payload must exceed the hard capture cap")

	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"observe", "tool"}, payload, p.Env)
	require.Equal(t, 0, code, "a refused capture is still a hook: it exits 0\nstderr:\n%s", stderr)
	require.True(t, emptyOrParseableJSON(stdout), "a refused capture still writes a parseable document")

	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, boundsSession))
	shutdownIfReachable(t, p.Root)

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	found := surfacesContaining(files, marker)
	require.Empty(t, found, "the %d KiB mark of a refused payload reached %v; the refusal prefix is "+
		"supposed to bound what survives to 4 KiB", hookPrefixProbeOffset>>10, found)

	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("a %d MiB tool_response was refused by the hard capture cap: the hook "+
		"still exited 0 with a parseable document, and no byte from the %d KiB mark of the payload "+
		"reached any file under .qompack/.", len(payload)>>20, hookPrefixProbeOffset>>10)
	rec.Detail = fmt.Sprintf("swept %d files under .qompack/", len(files))
	writeRecord(t, rec)
}

// TestSecurity_ConfigurationCannotRaiseTheCaptureCap sets runtime.hotPath.maxPayloadBytes above the
// hard 4 MiB allocation cap and asserts the cap still wins.
//
// What the record says about HOW it wins is the point, and that is what finding S-2 changed. The
// value used to be unclamped — validation bounded the key only from BELOW — so `config print`
// echoed the operator's number, no §11.3 violation was recorded, and admission refused every
// delivery before session-start reached its daemon bootstrap: the cap held by switching the product
// off. config.Validate now bounds the key at config.HookCaptureHardCapBytes, so the value is
// clamped and reported and the row takes its `clamped` branch. The two `failed` branches below are
// kept as the regression diagnosis for each way that could come undone.
func TestSecurity_ConfigurationCannotRaiseTheCaptureCap(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "bounds_config_cannot_raise_the_capture_cap")
	rec.Capability = CapBounds

	writeProjectConfig(t, p, map[string]any{
		"runtime": map[string]any{"hotPath": map[string]any{"maxPayloadBytes": overCapPayloadBytes}},
	})

	// The hook contract holds regardless, so it is asserted. Whether a daemon comes up is OBSERVED:
	// the refusal happens inside admission, which runs before session-start's own preSend, so a
	// configuration past the cap can take the daemon with it — and if it does, that is the finding,
	// not a broken test.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, boundsSession))
	daemonUp := waitDaemonUpFor(t, p.Root, absentDaemonBound)

	const marker = "RAISED-CAP-MUST-NOT-ADMIT-9e23f0"
	const ordinaryMarker = "ORDINARY-PAYLOAD-UNDER-A-RAISED-CAP-31ab7c"

	payload := oversizeToolPayload(t, p.Root, boundsSession, "toolu_security_raisedcap_01", marker)
	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"observe", "tool"}, payload, p.Env)
	require.Equal(t, 0, code, "a refused capture is still a hook: it exits 0\nstderr:\n%s", stderr)
	require.True(t, emptyOrParseableJSON(stdout), "a refused capture still writes a parseable document")

	// An ORDINARY delivery under the same configuration. It is the control that separates "the cap
	// refused an oversize payload" from "the configuration refused everything".
	ordStdout, ordStderr, ordCode := run(t, b.Bin, p.Root, []string{"observe", "tool"},
		readToolPayload(t, p.Root, boundsSession, "toolu_security_raisedcap_02",
			"src/ordinary.ts", "// "+ordinaryMarker+"\n"), p.Env)
	require.Equal(t, 0, ordCode, "an ordinary capture must exit 0\nstderr:\n%s", ordStderr)
	require.True(t, emptyOrParseableJSON(ordStdout), "an ordinary capture must write a parseable document")

	// What the effective configuration says it is, which is what an operator would read.
	printed, _, printCode := run(t, b.Bin, p.Root, []string{"config", "print", "--json"}, nil, p.Env)
	require.Equal(t, 0, printCode, "config print must succeed")
	var cfgDoc struct {
		Runtime struct {
			HotPath struct {
				MaxPayloadBytes int `json:"maxPayloadBytes"`
			} `json:"hotPath"`
		} `json:"runtime"`
	}
	require.NoError(t, json.Unmarshal(printed, &cfgDoc))

	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, boundsSession))
	shutdownIfReachable(t, p.Root)

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	found := surfacesContaining(files, marker)
	require.Empty(t, found, "a configured payload bound above the hard cap admitted bytes into %v", found)

	ordinaryKept := len(surfacesContaining(files, ordinaryMarker)) > 0
	violations := readViolations(t, p.Root)
	clamped := cfgDoc.Runtime.HotPath.MaxPayloadBytes <= hookCaptureHardCapBytes

	rec.Detail = fmt.Sprintf("config print reports runtime.hotPath.maxPayloadBytes=%d; "+
		"%d configuration violation(s) recorded; daemon reachable=%v; ordinary capture retained=%v",
		cfgDoc.Runtime.HotPath.MaxPayloadBytes, len(violations), daemonUp, ordinaryKept)

	switch {
	case clamped:
		rec.Outcome = OutcomeVerified
		rec.Reason = "the configured payload bound was clamped to the supported value, the oversize " +
			"payload was refused, and the hook contract held."
	case ordinaryKept && daemonUp:
		// Still a failed row, not a verified one: the cap held, but an unclamped, unreported value
		// above it is a divergence with an owner, and a divergence recorded as `verified` is a
		// divergence nobody counts.
		rec.Outcome = OutcomeFailed
		rec.Reason = "the hard 4 MiB capture cap refused the oversize payload while an ordinary " +
			"delivery under the same configuration was still captured. The configured value is not " +
			"clamped and no violation is recorded for it — validation bounds " +
			"runtime.hotPath.maxPayloadBytes only from below — so `config print` reports the " +
			"operator's number while admission refuses on it. Returned as a reporting gap; owner " +
			"internal/config + internal/cli."
	default:
		rec.Outcome = OutcomeFailed
		rec.Reason = "the hard 4 MiB capture cap held — no byte past the refusal prefix survived — " +
			"but a configuration ABOVE the cap disabled the product rather than being clamped to " +
			"it: admitHookCapture refuses the whole delivery with ErrBudget before session-start " +
			"reaches its own daemon bootstrap, so no daemon came up and an ordinary capture under " +
			"the same configuration was not retained either. Nothing records the refusal where an " +
			"operator would look: config validation bounds the key only from below, so no §11.3 " +
			"violation is written and `config print` reports the operator's number as if it were " +
			"in effect. Owner: internal/config (an upper bound on the key) + internal/cli " +
			"(clamp-and-warn rather than refuse). RETURNED, not fixed here."
		t.Logf("RETURNED FINDING (owner internal/config + internal/cli): "+
			"runtime.hotPath.maxPayloadBytes=%d disables capture entirely (daemon up=%v, ordinary "+
			"capture retained=%v, violations recorded=%d)",
			cfgDoc.Runtime.HotPath.MaxPayloadBytes, daemonUp, ordinaryKept, len(violations))
	}
	writeRecord(t, rec)
}

// TestSecurity_MCPResponseStaysWithinItsConfiguredBound pins runtime.mcp.maxResponseBytes: `expand`
// with full:true means "do not narrow to the matching hunk", never "unbounded".
func TestSecurity_MCPResponseStaysWithinItsConfiguredBound(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "bounds_mcp_max_response_bytes")
	rec.Capability = CapBounds

	writeProjectConfig(t, p, map[string]any{
		"runtime": map[string]any{"mcp": map[string]any{"maxResponseBytes": tinyResponseBytes}},
	})

	body := deniedBodyText()
	writeProjectFile(t, p, deniedPath, body)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, boundsSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, boundsSession, deniedToolUseID, deniedPath, body))
	requireIndexed(t, p.Root, deniedToolUseID)

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)

	res := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": deniedToolUseID, "full": true})
	require.False(t, res.IsError, "expand must answer: %s", res.Text)
	var body2 struct {
		Found      bool   `json:"found"`
		Content    string `json:"content"`
		Truncated  bool   `json:"truncated"`
		NextSpan   string `json:"next_span"`
		TotalBytes int64  `json:"total_bytes"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Text), &body2))
	child.finish(t)

	require.True(t, body2.Found, "the seeded object must expand")
	require.LessOrEqual(t, len(body2.Content), tinyResponseBytes,
		"a full expand returned %d bytes against a configured bound of %d",
		len(body2.Content), tinyResponseBytes)
	require.True(t, body2.Truncated, "a capped response must say it was truncated")
	require.NotEmpty(t, body2.NextSpan, "a truncated response must tell the model how to page on")

	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("with runtime.mcp.maxResponseBytes=%d, a full expand of a %d-byte "+
		"object returned %d bytes, marked truncated, with a next_span cursor.",
		tinyResponseBytes, body2.TotalBytes, len(body2.Content))
	writeRecord(t, rec)
}

// zstdBomb builds a valid zstd frame whose plaintext is far larger than store's 64 MiB decode
// bound. The plaintext is zeros, so the frame itself is a few kilobytes: nothing here allocates
// what the bomb claims, which is the property the decoder is supposed to have too.
func zstdBomb(t *testing.T) []byte {
	t.Helper()
	var out strings.Builder
	enc, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	block := make([]byte, 1<<20)
	for written := 0; written < bombPlainBytes; written += len(block) {
		_, werr := enc.Write(block)
		require.NoError(t, werr)
	}
	require.NoError(t, enc.Close())

	frame := []byte(out.String())
	// The bomb only measures anything if the bound actually refuses it.
	_, derr := store.Decode(frame)
	require.Error(t, derr, "a frame expanding to %d bytes must be refused by store's decode bound", bombPlainBytes)
	return frame
}

// objectFilePath is where this store filed the object for h: objects/<2>/<2>/<64 hex>.zst.
func objectFilePath(root string, h core.Hash) string {
	hx := strings.TrimPrefix(h.String(), "sha256:")
	return filepath.Join(paths.Of(root).Objects, hx[:2], hx[2:4], hx+".zst")
}
