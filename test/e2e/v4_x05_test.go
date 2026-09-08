// V4 §4.5 — tombstone → recall → expand, through real producers on both ends.
//
// Wired: a real PostToolUse hook through the real binary → the real observer's redact →
// canonicalize → chunk pipeline into the real store → observer.Tombstone's addressable §8.1 marker
// → the real MCP `recall` and `expand` handlers over the real ToolDeps → the original bytes back
// out of the store.
//
// Post-unit-F clauses covered here: `expand` redacts a secret the capture-time policy missed and
// never logs it, and `recall` omits hits whose stored path fails authorization.
package e2e

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// x5v4Session is this row's session identity.
const x5v4Session = core.SessionID("sess-e2e-v4-x05")

// x5v4ToolUseID is the capture `expand` addresses by identity.
const x5v4ToolUseID = "toolu_v4_x05_01"

// x5v4Path is the file the capture read.
const x5v4Path = "src/pool.ts"

// x5v4Marker appears exactly once in the seeded body, so a recall hit is evidence the store
// indexed the content rather than evidence the query matched noise.
const x5v4Marker = "pgbouncer transaction mode"

// x5v4Tombstone is §8.1's marker grammar, the same one v3_x01 pins.
var x5v4Tombstone = regexp.MustCompile(
	`^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/pool\.ts · re-expandable\]$`)

// x5v4Body is the seeded tool result: large enough to chunk (so a minimal span has real
// boundaries), non-repeating so the store cannot collapse it, and carrying the recall marker.
func x5v4Body() string {
	var b strings.Builder
	const target = 96 * 1024
	b.Grow(target + 128)
	b.WriteString("// " + x5v4Path + "\n// a pool timeout here means " + x5v4Marker + ".\n")
	for i := 0; b.Len() < target; i++ {
		b.WriteString("export const poolTimeoutMs")
		b.WriteString(strings.Repeat("0", 6-len(itoaV4(i))) + itoaV4(i))
		b.WriteString(" = ")
		b.WriteString(itoaV4(30000 + i))
		b.WriteString("; // line ")
		b.WriteString(itoaV4(i))
		b.WriteString(" of the seeded capture\n")
	}
	return b.String()
}

// itoaV4 is strconv.Itoa under a package-local name, so this file needs no extra import for one call.
func itoaV4(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestV4_TombstoneToRecallToExpandRoundTrip is V4-VERIFY §4.5.
//
// The negative control is the last arm: a record whose stored path is outside the authorized root
// must be OMITTED from recall — observed as an omission, not as an error — and must be refused by
// expand. Without it, "recall returns the tombstoned capture" would be satisfied by a recall that
// returns everything it can see.
func TestV4_TombstoneToRecallToExpandRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x5v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, x5v4Session, x5v4ToolUseID, x5v4Path, x5v4Body()), env)
	r.WaitIndexed(t, 1)

	// ── The tombstone the REAL observer renders for the record the REAL store holds ──────────────
	rec, err := r.Opts.Store.ToolUse(ctx, core.ToolUseID(x5v4ToolUseID))
	require.NoError(t, err, "the hook's capture must be addressable by its own tool_use_id")
	require.Equal(t, x5v4Path, rec.Path)
	require.Equal(t, store.StatusOK, rec.Status)
	require.False(t, rec.Root.IsZero(), "the record must carry a real store root")

	ts := observer.Tombstone(rec)
	require.Regexp(t, x5v4Tombstone, ts, "the §8.1 marker grammar")
	short := strings.TrimPrefix(ts, "[cleared: sha256:")[:12]
	require.Equal(t, rec.Root.Short(), short,
		"the marker's short hash IS the retrieval key — that is what makes a tombstone addressable")

	srv := v4Server(t, v4ToolDeps(t, r))

	// ── recall: the real handler finds the content the hook just stored ───────────────────────────
	var recalled struct {
		Hits []struct {
			Hash      string   `json:"hash"`
			Path      string   `json:"path"`
			ToolUseID string   `json:"tool_use_id"`
			Span      [2]int64 `json:"span"`
		} `json:"hits"`
		Count int  `json:"count"`
		Found bool `json:"found"`
	}
	v4Call(t, srv, x5v4Session, mcp.ToolRecall, map[string]any{"query": x5v4Marker, "k": 5}, &recalled)
	require.True(t, recalled.Found, "recall must find the content a real hook stored")
	require.Equal(t, len(recalled.Hits), recalled.Count)

	var hitHash string
	for _, h := range recalled.Hits {
		if h.ToolUseID == x5v4ToolUseID {
			hitHash = h.Hash
			require.Equal(t, x5v4Path, h.Path, "a hit must name the path it came from")
			require.Regexp(t, `^sha256:[0-9a-f]{64}$`, h.Hash, "a hit is a POINTER, ready for expand")
		}
	}
	require.NotEmpty(t, hitHash, "recall must return the seeded capture; hits=%+v", recalled.Hits)
	require.Equal(t, rec.Root.String(), hitHash,
		"the pointer recall returns must be the root the tombstone addresses — one identity, not two")

	// ── expand: the real handler returns the ORIGINAL bytes from the store ───────────────────────
	var expanded struct {
		Found      bool     `json:"found"`
		Hash       string   `json:"hash"`
		Path       string   `json:"path"`
		Span       [2]int64 `json:"span"`
		TotalBytes int64    `json:"total_bytes"`
		Truncated  bool     `json:"truncated"`
		Content    string   `json:"content"`
	}
	res := v4Call(t, srv, x5v4Session, mcp.ToolExpand,
		map[string]any{"tool_use_id": x5v4ToolUseID}, &expanded)
	require.False(t, res.IsError, "expand must re-materialize a cleared result, not report an error")
	require.True(t, expanded.Found)
	require.Equal(t, x5v4Path, expanded.Path)
	require.Equal(t, rec.Root.String(), expanded.Hash,
		"expand must serve the very object the tombstone named")
	require.EqualValues(t, len(expanded.Content), expanded.Span[1]-expanded.Span[0],
		"the returned bytes must be exactly the span the response reports")

	// The bytes are the store's, byte for byte, at the span expand reported.
	rc, err := r.Opts.Store.OpenSpan(ctx, rec.Root, expanded.Span[0], expanded.Span[1]-expanded.Span[0])
	require.NoError(t, err)
	fromStore := x1ReadAll(t, rc)
	require.Equal(t, string(fromStore), expanded.Content,
		"expand must return the store's own bytes for the span it named, not a re-read of disk")

	// Post-F: a secret the capture-time policy missed is redacted at expansion and never logged.
	require.NotContains(t, expanded.Content, "sk-"+"ant-",
		"no credential-shaped literal may survive an expansion")
	for _, line := range loudLines(t, p.Root) {
		require.NotContains(t, line, "sk-"+"ant-", "a redacted secret must never reach a log line")
	}

	// ── NEGATIVE CONTROL: a record whose stored path is outside the authorized root ──────────────
	//
	// recall must OMIT it — the omission is the observable, not an error — and expand must refuse.
	// The record is written straight through the real store's RecordToolUse rather than through a
	// hook: the observer normalizes a path at CAPTURE time, so a hook can no longer produce this
	// shape. What unit F's authorization guards is the other case — a record whose stored path is
	// not authorized NOW — and that is exactly what this writes.
	const escaped = "../outside-the-root/secrets.ts"
	escapedRec := rec
	escapedRec.ID = "toolu_v4_x05_esc"
	escapedRec.Path = escaped
	require.NoError(t, r.Opts.Store.RecordToolUse(ctx, escapedRec),
		"the store must accept the record; authorization is a RETRIEVAL-time gate, not a write gate")

	var afterEscape struct {
		Hits []struct {
			Path      string `json:"path"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"hits"`
	}
	v4Call(t, srv, x5v4Session, mcp.ToolRecall, map[string]any{"query": x5v4Marker, "k": 20}, &afterEscape)
	for _, h := range afterEscape.Hits {
		require.NotEqual(t, "toolu_v4_x05_esc", h.ToolUseID,
			"NEGATIVE CONTROL: recall must omit a hit whose stored path fails authorization; hits=%+v",
			afterEscape.Hits)
		require.NotContains(t, h.Path, "outside-the-root",
			"no hit may name a path outside the authorized root: %+v", afterEscape.Hits)
	}

	var denied struct {
		Found  bool   `json:"found"`
		Denied bool   `json:"denied"`
		Reason string `json:"reason"`
	}
	v4Call(t, srv, x5v4Session, mcp.ToolExpand,
		map[string]any{"tool_use_id": "toolu_v4_x05_esc"}, &denied)
	require.False(t, denied.Found,
		"expand must not materialize a capture whose path fails authorization")
	require.NotContains(t, denied.Reason, "outside-the-root",
		"a refusal must not echo the offending path — it would be an existence oracle")
}
