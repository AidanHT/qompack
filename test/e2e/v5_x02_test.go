// V5 §4.2 — exact authorized handle recovery with fidelity, and no historical/current
// substitution (SP-20 × SP-13).
//
// Wired, all through the real binary: a real PostToolUse hook → the hook client's own capture
// admission → the daemon's durable capture sidecar (SP-20 M1-01/M1-02, publication stage one) →
// the real observer's put/index/link (stage two) → observer.Tombstone's addressable §8.1 marker →
// a real `qompack mcp` child over real stdio → the daemon-side `expand` and `re_read` handlers →
// the captured bytes back out, page by page.
//
// The historical §4.2 text asserted a chunk-aligned span, an ephemeral ToolUseRecord and a
// `recall` envelope. The current criterion is narrower and stronger: the handle a tombstone
// carries must recover EXACTLY what the host delivered — byte for byte, at a fidelity the SP-20
// sidecar records rather than assumes — and it must never be answered by a different version
// of the same path: not the file that is on disk now, not the newest capture when an older handle
// was asked for, and not a historical capture when nothing was ever captured. Each of those three
// substitutions has a real, differing artifact in this test, so a substituting implementation
// returns bytes this test can recognise as the wrong ones.
//
// The negative control is the last subtest. It corrupts one of the captured object's chunks on
// disk — a real fault the store's integrity check quarantines — while the same path still holds a
// readable file in the working tree, and asserts that the handle now reports an honest failure
// rather than the working-tree bytes. That is the strongest available proof that the fidelity
// assertions above are not vacuous: the only way to still say "found" would be to substitute.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

const (
	// x02Session is this row's session identity, distinct from every other e2e row's.
	x02Session = core.SessionID("sess-e2e-v5-x02")

	// x02FirstToolUseID is the handle under test: the capture whose tombstone is rendered.
	x02FirstToolUseID = "toolu_v5_x02_01"
	// x02SecondToolUseID is a later capture of the SAME path, the "newest version" a substituting
	// implementation would hand back for the first handle.
	x02SecondToolUseID = "toolu_v5_x02_02"
	// x02EscapedToolUseID is a record whose stored path fails authorization NOW.
	x02EscapedToolUseID = "toolu_v5_x02_esc"

	// x02Path is the file the capture read. The working tree holds a DIFFERENT version of it for
	// the whole test, so a live read is always distinguishable from the capture.
	x02Path = "src/auth.ts"
	// x02NeverCapturedPath exists on disk and was never observed by any hook: the one shape where
	// "read the working tree" would be the tempting answer and is the forbidden one.
	x02NeverCapturedPath = "src/never-captured.ts"
	// x02EscapedPath is outside the project root, stored verbatim the way v4_x05 does it.
	x02EscapedPath = "../outside-the-root/secrets.ts"

	// The three markers, each unique to one artifact, so every response can be attributed to the
	// version it actually came from.
	x02HistoricalMarker = "x02-historical-refresh-token-body"
	x02CurrentMarker    = "x02-current-disk-body"
	x02TailMarker       = "x02-second-capture-tail"

	// x02BodyBytes is the historical §4.2 setup's "one 200 KB FileRead". It is well under
	// ipc.CaptureFrameBudget (384 KiB on the shipped default), so the hook client's capture
	// crosses the frame WHOLE and the sidecar can honestly record exact fidelity; and it is well
	// over cfg.Store.Chunk.Max, so a minimal span cannot return it in one page.
	x02BodyBytes = 200 * 1024

	// x02Segments is how many generated declarations sit on each side of refreshToken.
	x02Segments = 1700
)

// x02Tombstone is §8.1's marker grammar, the same one v3_x01 and v4_x05 pin, for this path.
var x02Tombstone = regexp.MustCompile(
	`^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$`)

// x02HistoricalBody is the tool result the hook delivers: the version the store captures.
//
// It is LF-only ASCII with no token any configured canonicalization class strips (no timestamps,
// ANSI, pids, addresses, temp paths or durations), so the canonical bytes ARE the raw bytes and a
// byte-for-byte comparison against the delivered content is a fair fidelity test rather than a
// test of the canonicalizer. Every line is distinct so the store cannot collapse it, and
// refreshToken sits deep enough that no head window would contain it by accident.
func x02HistoricalBody() string {
	var b strings.Builder
	b.Grow(x02BodyBytes + 512)
	b.WriteString("// " + x02Path + " — the version the hook captured.\n")
	for i := 0; i < x02Segments; i++ {
		fmt.Fprintf(&b, "export const authSegment%06d = \"segment-%06d\";\n", i, i)
	}
	b.WriteString("export function refreshToken(session) {\n")
	b.WriteString("  // " + x02HistoricalMarker + "\n")
	b.WriteString("  return session.renew();\n}\n")
	for i := 0; b.Len() < x02BodyBytes; i++ {
		fmt.Fprintf(&b, "export const renewalStep%06d = \"renewal-%06d\";\n", i, i)
	}
	return b.String()
}

// x02CurrentBody is what the working tree holds for x02Path throughout: a different
// refreshToken, a different marker, a different size.
func x02CurrentBody() string {
	return "// " + x02Path + " — the version on disk, never captured.\n" +
		"export function refreshToken(session) {\n" +
		"  // " + x02CurrentMarker + "\n" +
		"  return session.renewWithBackoff();\n}\n"
}

// x02SecondBody is the later capture of the same path: the historical body plus a tail, so it is
// both a genuinely different object and the one a "newest wins" resolution would substitute.
func x02SecondBody() string {
	return x02HistoricalBody() + "// " + x02TailMarker + "\nexport const refreshedAfterCapture = true;\n"
}

// x02ContentBody is the wire shape `expand` and `re_read` share, decoded from the child's stdio.
type x02ContentBody struct {
	Found      bool     `json:"found"`
	Available  *bool    `json:"available"`
	Denied     bool     `json:"denied"`
	Reason     string   `json:"reason"`
	Hash       string   `json:"hash"`
	Path       string   `json:"path"`
	Source     string   `json:"source"`
	Span       [2]int64 `json:"span"`
	TotalBytes int64    `json:"total_bytes"`
	Truncated  bool     `json:"truncated"`
	NextSpan   string   `json:"next_span"`
	Content    string   `json:"content"`
}

// x02IndexLine is index/tool_use.jsonl's on-disk line shape — the store's compact tuRec keys, NOT
// ToolUseRecord's frozen wire shape. Decoding a line into store.ToolUseRecord directly compiles
// and "succeeds" while silently zeroing status, session and the ephemeral flag, which is exactly
// the vacuous read this file must not make; the keys are spelled out here so the record a test
// asserts on is the one the daemon wrote.
type x02IndexLine struct {
	ID      core.ToolUseID `json:"id"`
	Session core.SessionID `json:"s"`
	Turn    core.TurnIndex `json:"turn"`
	TS      core.UnixMilli `json:"ts"`
	Tool    string         `json:"tool"`
	ArgP    string         `json:"argp"`
	Root    core.Hash      `json:"root"`
	Path    string         `json:"path"`
	Bytes   int64          `json:"bytes"`
	St      *int           `json:"st"`
	Eph     bool           `json:"eph"`
}

// x02Decode decodes one index line into the ToolUseRecord it denotes. A line that is not a record
// (a supersession mutation, for instance, carries no "root") reports false.
func x02Decode(line string) (store.ToolUseRecord, bool) {
	var l x02IndexLine
	if json.Unmarshal([]byte(line), &l) != nil || l.ID == "" || l.St == nil || l.Root.IsZero() {
		return store.ToolUseRecord{}, false
	}
	return store.ToolUseRecord{
		ID: l.ID, Session: l.Session, Turn: l.Turn, TS: l.TS, Tool: l.Tool, ArgsPreview: l.ArgP,
		Root: l.Root, Path: l.Path, Bytes: l.Bytes, Status: store.Supersession(*l.St), Ephemeral: l.Eph,
	}, true
}

// x02RecordFor returns the daemon's own index record for id.
func x02RecordFor(t *testing.T, root, id string) store.ToolUseRecord {
	t.Helper()
	for _, line := range obsToolUseLines(root) {
		if rec, ok := x02Decode(line); ok && string(rec.ID) == id {
			return rec
		}
	}
	require.FailNowf(t, "record not indexed", "no index/tool_use.jsonl record with id %s", id)
	return store.ToolUseRecord{}
}

// x02WaitIndexed polls until the observer has indexed id. The hook's ACK precedes the store write,
// so this is a real wait, bounded the way mcp_e2e_test.go bounds the same gap.
func x02WaitIndexed(t *testing.T, root, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, line := range obsToolUseLines(root) {
			if strings.Contains(line, `"id":"`+id+`"`) {
				return true
			}
		}
		return false
	}, mcpE2EIndexBound, mcpE2EIndexTick, "the observer never indexed %s", id)
}

// x02SidecarFor finds the SP-20 capture sidecar the daemon linked to id. The sidecar is keyed by
// the delivery's observation identity, which the frozen index record does not carry, so the tree
// is walked and the join is made on the reference LinkCaptureReference wrote.
//
// It polls, bounded the way WaitIndexed is: publication order writes the sidecar (stage one),
// then the index record (stage two), then the link joining the two — and a test that has seen the
// index line has seen stage two land, not the link after it. Reading the sidecar in that window
// finds a durable capture with no reference yet, which is a real intermediate state and not a
// failure; only a link that never arrives is one.
func x02SidecarFor(t *testing.T, root, id string) store.CaptureSidecar {
	t.Helper()
	ticker := time.NewTicker(mcpE2EIndexTick)
	defer ticker.Stop()
	timeout := time.NewTimer(mcpE2EIndexBound)
	defer timeout.Stop()
	for {
		if sc, ok := x02ScanSidecars(t, root, id); ok {
			return sc
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "capture sidecar never linked",
				"no sidecar under records/captures names %s within %s\n%s", id, mcpE2EIndexBound, x02Diag(t, root))
		}
	}
}

// x02WaitEphemeral polls index/tool_use.jsonl for the Ephemeral records the expansions above
// wrote, bounded like WaitIndexed, and fails with the daemon's own log rather than a bare timeout:
// recordEphemeral reports every failure as a Warn and a counter, never as a tool error, so the log
// is the only place the reason can be.
func x02WaitEphemeral(t *testing.T, root string) []store.ToolUseRecord {
	t.Helper()
	ticker := time.NewTicker(mcpE2EIndexTick)
	defer ticker.Stop()
	timeout := time.NewTimer(mcpE2EIndexBound)
	defer timeout.Stop()
	for {
		var out []store.ToolUseRecord
		for _, line := range obsToolUseLines(root) {
			if r, ok := x02Decode(line); ok && r.Ephemeral {
				out = append(out, r)
			}
		}
		if len(out) > 0 {
			return out
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "no ephemeral record", "an expansion must leave an Ephemeral ToolUseRecord within %s\n%s",
				mcpE2EIndexBound, x02Diag(t, root))
		}
	}
}

// x02ScanSidecars walks the sidecar tree once and returns the record linked to id, if any.
func x02ScanSidecars(t *testing.T, root, id string) (store.CaptureSidecar, bool) {
	t.Helper()
	dir := filepath.Join(paths.Of(root).Records, "captures")
	var found *store.CaptureSidecar
	err := filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // the tree may not exist yet; the caller's bound decides when that is a failure
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil // a sidecar mid-replace is re-read on the next tick
		}
		var sc store.CaptureSidecar
		if json.Unmarshal(b, &sc) != nil {
			return nil
		}
		if string(sc.ToolUseID) == id && sc.Published {
			found = &sc
		}
		return nil
	})
	require.NoError(t, err)
	if found == nil {
		return store.CaptureSidecar{}, false
	}
	return *found, true
}

// x02Diag renders what the daemon left under .qompack/{records,state,logs} and its LOUD lines, so
// a missing sidecar fails with the daemon's own account of the delivery rather than a bare absence.
func x02Diag(t *testing.T, root string) string {
	t.Helper()
	l := paths.Of(root)
	var b strings.Builder
	for _, dir := range []string{l.Records, l.State, l.Logs} {
		fmt.Fprintf(&b, "-- %s\n", dir)
		_ = filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				fmt.Fprintf(&b, "   (walk: %v)\n", walkErr)
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, _ := d.Info()
			var size int64
			if info != nil {
				size = info.Size()
			}
			fmt.Fprintf(&b, "   %s (%d bytes)\n", strings.TrimPrefix(p, paths.Long(dir)), size)
			if dir == l.Logs {
				if raw, readErr := os.ReadFile(p); readErr == nil {
					lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
					if len(lines) > 40 {
						lines = lines[len(lines)-40:]
					}
					for _, line := range lines {
						fmt.Fprintf(&b, "      %s\n", line)
					}
				}
			}
			return nil
		})
	}
	fmt.Fprintf(&b, "-- LOUD: %v\n", loudLines(t, root))
	return b.String()
}

// x02ExpandPaged follows `expand`'s own cursor from offset 0 until the object is exhausted and
// returns the concatenation, asserting the §8.7 span contract on every page: each page is exactly
// the span it reports, pages are contiguous, no page exceeds the configured response bound, the
// hash never changes mid-object, and every page is a born-ephemeral untrusted retrieval result.
// address is the one address form the caller chose (hash or tool_use_id); the cursor rides with it.
func x02ExpandPaged(t *testing.T, c *mcpE2EChild, next func() int, p *testutil.Project,
	address map[string]any, wantHash string,
) (content string, pages int, last x02ContentBody) {
	t.Helper()
	var b strings.Builder
	var prevEnd int64
	args := map[string]any{}
	for k, v := range address {
		args[k] = v
	}
	for {
		var body x02ContentBody
		res := mcpE2ECall(t, c, next(), mcp.ToolExpand, args, &body)
		require.True(t, body.Found, "expand must find the handle: %+v", body)
		require.Equal(t, wantHash, body.Hash, "every page must come from the object the handle names")
		require.EqualValues(t, len(body.Content), body.Span[1]-body.Span[0],
			"the returned bytes must be exactly the span the response reports")
		require.Equal(t, prevEnd, body.Span[0], "pages must be contiguous: following next_span must not skip or repeat")
		require.LessOrEqual(t, body.Span[1]-body.Span[0], int64(p.Cfg.Runtime.MCP.MaxResponseBytes),
			"no span may exceed runtime.mcp.maxResponseBytes")

		qm := res.Meta[mcp.ServerName]
		require.Equal(t, true, qm["ephemeral"], "a retrieval result is born ephemeral (§8.7); _meta=%v", res.Meta)
		require.Equal(t, true, qm["untrusted"], "archive bytes are untrusted content; _meta=%v", res.Meta)
		// The handler publishes the synthetic id only AFTER it stored and indexed the ephemeral
		// record; the flag above is configuration, this is the record's existence in its own words.
		require.NotEmpty(t, qm["tool_use_id"], "an expansion must have recorded its own ephemeral result; _meta=%v", res.Meta)

		// Truncated means "less than the whole object" — it is true of every page but a single
		// whole-object one, the last page of a walk included — so the cursor, not the flag, says
		// whether there is more.
		require.Equal(t, body.Span[0] > 0 || body.Span[1] < body.TotalBytes, body.Truncated,
			"truncated must report exactly whether this span is less than the whole object: %+v", body.Span)

		b.WriteString(body.Content)
		pages++
		prevEnd = body.Span[1]
		last = body
		if body.NextSpan == "" {
			require.Equal(t, body.TotalBytes, body.Span[1],
				"a walk that hands back no cursor must have reached the object's end")
			break
		}
		require.Less(t, body.Span[1], body.TotalBytes, "a page with a cursor must end before the object does")
		require.Less(t, pages, x02BodyBytes, "paging must terminate")
		args["span"] = body.NextSpan
	}
	return b.String(), pages, last
}

// x02ReapOnFailure kills the `qompack mcp` child if a failing assertion leaves it running: an
// orphaned child keeps the project's log open and makes t.TempDir's cleanup fail on Windows,
// which buries the real failure under an unrelated one. On the passing path finish has already
// waited for the process and the kill is a no-op.
func x02ReapOnFailure(t *testing.T, c *mcpE2EChild) {
	t.Helper()
	t.Cleanup(func() {
		if c.cmd.ProcessState == nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
			_ = c.cmd.Wait()
		}
	})
}

// x02RawCall sends one tools/call and returns the envelope WITHOUT asserting IsError, for the arms
// where either a tool error or a found:false body is an honest answer and only a hit is wrong.
func x02RawCall(t *testing.T, c *mcpE2EChild, id int, name string, args map[string]any) (mcpE2ECallResult, x02ContentBody) {
	t.Helper()
	c.send(t, mcpE2ERequest(t, id, "tools/call", map[string]any{"name": name, "arguments": args}))
	var res mcpE2ECallResult
	require.NoError(t, json.Unmarshal(c.await(t, id), &res), "decoding the %s result", name)
	require.Len(t, res.Content, 1, "a tool result carries exactly one text block: %+v", res.Content)
	var body x02ContentBody
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), &body),
			"a non-error %s body must be JSON: %s", name, res.Content[0].Text)
	}
	return res, body
}

// TestV5_TombstoneToExpandRoundTrip is V5-VERIFY §4.2.
func TestV5_TombstoneToExpandRoundTrip(t *testing.T) {
	bin := Build(t)
	historical, current, second := x02HistoricalBody(), x02CurrentBody(), x02SecondBody()
	require.GreaterOrEqual(t, len(historical), x02BodyBytes, "the capture must be the §4.2 200 KB read")
	require.NotEqual(t, historical, current, "the working tree must differ from the capture, or substitution is invisible")

	p := testutil.NewProject(t, testutil.WithFiles(map[string]string{
		x02Path:              current,
		x02NeverCapturedPath: "// " + x02CurrentMarker + "\nexport const neverObserved = true;\n",
	}))
	shutdown := sync.OnceFunc(func() { e2eShutdownIfReachable(t, p.Root) })
	t.Cleanup(shutdown)
	env := e2eEnv(p)

	// A real daemon, spawned by a real hook, and a real capture through the real hook binary.
	// The payload bytes are kept: they are what the SP-20 sidecar must reproduce exactly.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x02Session), env)
	e2eWaitDaemonUp(t, p.Root)
	payload := obsToolPayload(t, p.Root, x02Session, x02FirstToolUseID, x02Path, historical)
	obsRunHook(t, bin, []string{"observe", "tool"}, payload, env)
	x02WaitIndexed(t, p.Root, x02FirstToolUseID)

	rec := x02RecordFor(t, p.Root, x02FirstToolUseID)
	require.Equal(t, x02Path, rec.Path)
	require.Equal(t, x02Session, rec.Session)
	require.Equal(t, "FileRead", rec.Tool, "the record carries the display name, never the host's spelling")
	require.Equal(t, store.StatusOK, rec.Status)
	require.False(t, rec.Ephemeral, "a host capture is durable, never born ephemeral")
	require.False(t, rec.Root.IsZero(), "the record must carry a real store root")
	require.EqualValues(t, len(historical), rec.Bytes,
		"the record's raw size must be the delivered content's: canonicalization changed nothing")

	// ── SP-20: the durable capture is the host's delivery, byte for byte, at recorded fidelity ──
	sc := x02SidecarFor(t, p.Root, x02FirstToolUseID)
	require.Equal(t, core.FidelityExact, sc.Fidelity, "a whole, unredacted, in-budget delivery is captured exact")
	require.Equal(t, core.OutcomeOK, sc.Outcome)
	require.Equal(t, core.CaptureErrorNone, sc.CaptureError)
	require.False(t, sc.Redacted)
	require.False(t, sc.Truncated)
	require.True(t, sc.Published, "publication stage two must have joined the reference to the capture")
	require.Equal(t, rec.Root, sc.Root, "the sidecar's reference must name the record's root")
	require.Equal(t, x02Session, sc.Session)
	require.Contains(t, sc.HostFields, "tool_response", "host fields are recorded as observed")
	require.Equal(t, payload, sc.Bytes,
		"the sidecar must hold the exact bytes the hook read from the host — fidelity is measured, not claimed")

	// ── The tombstone the REAL observer renders for the record the REAL store holds ─────────────
	ts := observer.Tombstone(rec)
	require.Regexp(t, x02Tombstone, ts, "the §8.1 marker grammar")
	short := strings.TrimPrefix(ts, "[cleared: sha256:")[:len(rec.Root.Short())]
	require.Equal(t, rec.Root.Short(), short, "the marker's short hash IS the retrieval key")
	require.True(t, strings.HasPrefix(rec.Root.String(), "sha256:"+short),
		"the short form must be a prefix of the full handle expand takes")

	// ── SP-13 over real stdio: the handle recovers the delivered content exactly ────────────────
	child := mcpE2EStart(t, bin, p)
	x02ReapOnFailure(t, child)
	ids := 0
	next := func() int { ids++; return ids }
	t13Initialize(t, child, next())

	byID, pages, lastByID := x02ExpandPaged(t, child, next, p,
		map[string]any{"tool_use_id": x02FirstToolUseID}, rec.Root.String())
	require.Greater(t, pages, 1, "a %d-byte object must not fit one minimal span", len(historical))
	require.Equal(t, x02Path, lastByID.Path, "a tool_use_id handle carries its path")
	require.EqualValues(t, len(historical), lastByID.TotalBytes)
	require.Equal(t, historical, byID,
		"following expand's own cursor from offset 0 must reproduce the delivered content byte for byte")

	var byHash x02ContentBody
	mcpE2ECall(t, child, next(), mcp.ToolExpand, map[string]any{"hash": rec.Root.String(), "full": true}, &byHash)
	require.True(t, byHash.Found)
	require.False(t, byHash.Truncated, "a %d-byte object fits one full response under the %d-byte bound",
		len(historical), p.Cfg.Runtime.MCP.MaxResponseBytes)
	require.Equal(t, historical, byHash.Content, "the sha256 handle from the tombstone must recover the same bytes")

	// ── No current substitution: the working tree differs and is never consulted ──────────────
	onDisk, err := os.ReadFile(paths.Long(filepath.Join(p.Root, filepath.FromSlash(x02Path))))
	require.NoError(t, err)
	require.Equal(t, current, string(onDisk), "the premise: the working tree still holds the other version")
	require.Contains(t, byID, x02HistoricalMarker)
	require.NotContains(t, byID, x02CurrentMarker, "expand must never hand back the working tree")

	var reRead x02ContentBody
	mcpE2ECall(t, child, next(), mcp.ToolReRead, map[string]any{"path": x02Path, "full": true}, &reRead)
	require.True(t, reRead.Found, "re_read must resolve the newest CAPTURED version")
	require.Equal(t, "store", reRead.Source, "the only source re_read ever reports is the archive")
	require.Equal(t, rec.Root.String(), reRead.Hash)
	require.Equal(t, historical, reRead.Content, "re_read's newest captured version is the capture, not the disk")

	// ── No historical substitution: an uncaptured path is explicitly unavailable, not read ─────
	_, err = os.Stat(paths.Long(filepath.Join(p.Root, filepath.FromSlash(x02NeverCapturedPath))))
	require.NoError(t, err, "the premise: the uncaptured file exists on disk and could be read")
	var never x02ContentBody
	mcpE2ECall(t, child, next(), mcp.ToolReRead, map[string]any{"path": x02NeverCapturedPath}, &never)
	require.False(t, never.Found, "nothing was captured, so nothing may be found: %+v", never)
	require.NotNil(t, never.Available, "the answer must be an explicit unavailable, not a plain miss: %+v", never)
	require.False(t, *never.Available)
	require.NotEmpty(t, never.Reason)
	require.Empty(t, never.Content, "no bytes may accompany an unavailable answer")

	// ── No newest-version substitution: a later capture of the same path leaves the handle exact ─
	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, x02Session, x02SecondToolUseID, x02Path, second), env)
	x02WaitIndexed(t, p.Root, x02SecondToolUseID)
	rec2 := x02RecordFor(t, p.Root, x02SecondToolUseID)
	require.NotEqual(t, rec.Root, rec2.Root, "the second capture is a different object")

	var firstAgain, secondNow, newest, pinned x02ContentBody
	mcpE2ECall(t, child, next(), mcp.ToolExpand, map[string]any{"tool_use_id": x02FirstToolUseID, "full": true}, &firstAgain)
	require.Equal(t, rec.Root.String(), firstAgain.Hash, "the first handle still names the first object")
	require.Equal(t, historical, firstAgain.Content, "an exact handle is not answered by the newest version of its path")
	require.NotContains(t, firstAgain.Content, x02TailMarker)

	mcpE2ECall(t, child, next(), mcp.ToolExpand, map[string]any{"tool_use_id": x02SecondToolUseID, "full": true}, &secondNow)
	require.Equal(t, rec2.Root.String(), secondNow.Hash)
	require.Equal(t, second, secondNow.Content, "the second handle recovers the second delivery exactly")

	mcpE2ECall(t, child, next(), mcp.ToolReRead, map[string]any{"path": x02Path, "full": true}, &newest)
	require.Equal(t, rec2.Root.String(), newest.Hash, "an unpinned re_read is the newest capture")
	require.Equal(t, "store", newest.Source)
	require.Equal(t, second, newest.Content)

	mcpE2ECall(t, child, next(), mcp.ToolReRead,
		map[string]any{"path": x02Path, "at": rec.Root.String(), "full": true}, &pinned)
	require.Equal(t, rec.Root.String(), pinned.Hash, "a re_read pinned to a handle resolves that handle")
	require.Equal(t, historical, pinned.Content)

	// ── §8.7: the expansions themselves are born ephemeral and are the first eviction class ────
	ephemeral := x02WaitEphemeral(t, p.Root)
	for _, r := range ephemeral {
		require.True(t, strings.HasPrefix(string(r.ID), "qompack-mcp:"), "a retrieval record has a synthetic id: %s", r.ID)
		require.Equal(t, scheduler.DropEphemeral,
			scheduler.DropClassOf(r.Tool, r.Ephemeral, r.Status == store.StatusSuperseded),
			"the retrieval result is the FIRST eviction candidate")
	}

	child.finish(t)

	// ── NEGATIVE CONTROL ────────────────────────────────────────────────────────────────────────
	t.Run("CorruptObjectIsHonestFailureNotWorkingTreeSubstitution", func(t *testing.T) {
		// The store is single-writer: the daemon must be gone before the test opens it.
		shutdown()

		st, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
		require.NoError(t, err)
		rt, err := st.GetRoot(context.Background(), rec.Root)
		require.NoError(t, err)
		require.NotEmpty(t, rt.Chunks)
		// An authorization-time refusal, written exactly as v4_x05 writes it: a record whose
		// stored path is not authorized NOW. The store accepts it; retrieval must not serve it.
		escaped := rec
		escaped.ID = x02EscapedToolUseID
		escaped.Path = x02EscapedPath
		require.NoError(t, st.RecordToolUse(context.Background(), escaped))
		require.NoError(t, st.Close())

		// Corrupt the first chunk's object file in place. getObject verifies size, decoding and
		// the content address before returning plaintext, so this is a fault the store detects
		// and quarantines rather than one it could serve.
		hx := strings.TrimPrefix(rt.Chunks[0].Hash.String(), "sha256:")
		matches, err := filepath.Glob(filepath.Join(paths.Of(p.Root).Objects, hx[:2], hx[2:4], hx+"*"))
		require.NoError(t, err)
		require.NotEmpty(t, matches, "the first chunk must exist as an object file")
		for _, m := range matches {
			require.NoError(t, os.Chmod(paths.Long(m), 0o600))
			require.NoError(t, os.WriteFile(paths.Long(m), []byte("x02: not the captured bytes\n"), 0o600))
		}

		// A fresh daemon and a fresh `qompack mcp`, the way an installed agent gets them. The
		// parent's once-only shutdown has already fired, so this daemon gets its own.
		obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x02Session), env)
		e2eWaitDaemonUp(t, p.Root)
		t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
		child := mcpE2EStart(t, bin, p)
		x02ReapOnFailure(t, child)
		ids := 0
		next := func() int { ids++; return ids }
		t13Initialize(t, child, next())

		// The working tree STILL holds a readable file at the same path. If the handle is
		// answered at all now, the only bytes available are the wrong ones.
		for _, args := range []map[string]any{
			{"tool_use_id": x02FirstToolUseID, "full": true},
			{"hash": rec.Root.String(), "full": true},
		} {
			res, body := x02RawCall(t, child, next(), mcp.ToolExpand, args)
			require.False(t, body.Found,
				"NEGATIVE CONTROL: a handle whose object is corrupt must not report found for %v: %s",
				args, res.Content[0].Text)
			require.NotContains(t, res.Content[0].Text, x02CurrentMarker,
				"NEGATIVE CONTROL: the working tree must never be substituted for a lost capture")
			require.NotContains(t, res.Content[0].Text, x02HistoricalMarker,
				"NEGATIVE CONTROL: no captured bytes can be produced from a corrupt object")
		}

		// The same rule through re_read: whatever it says, it is never the working tree.
		res, body := x02RawCall(t, child, next(), mcp.ToolReRead, map[string]any{"path": x02Path, "full": true})
		require.NotContains(t, res.Content[0].Text, x02CurrentMarker,
			"NEGATIVE CONTROL: re_read must never fall back to a live read")
		if body.Found {
			require.Equal(t, "store", body.Source)
		}

		// Authorization precedes materialization: a hash or id is an address, not a credential.
		_, denied := x02RawCall(t, child, next(), mcp.ToolExpand, map[string]any{"tool_use_id": x02EscapedToolUseID})
		require.False(t, denied.Found, "expand must not materialize a capture whose path fails authorization")
		require.True(t, denied.Denied, "a policy refusal is spelled out as denied, not as a miss: %+v", denied)
		require.NotContains(t, denied.Reason, "outside-the-root", "a refusal must not echo the offending path")

		child.finish(t)
	})

	p.AssertAppendOnly(t)
}
