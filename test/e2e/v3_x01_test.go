// X1 — the G3.2 round trip (plans/V3-VERIFY-observer-and-negative-knowledge.md §5): a PostToolUse
// hook event through the real binary and the real daemon, into the store's redact → canonicalize →
// chunk pipeline, out through observer.Tombstone's addressable marker, and back via store.Open /
// store.OpenSpan using the marker's own hash as the retrieval key.
//
// Where a wave-3 component would sit (SP-13's retrieval layer consuming the minimal sufficient
// span), the test composes the seam itself: symbols.New().Enclosing supplies the span and
// store.OpenSpan serves it, and the two halves are asserted to fit (§5 Rule).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
)

// x1FileSize is the fixture's exact byte length: 24 KB at the binary divisor observer.humanBytes
// renders with, so the tombstone's size field reads exactly "24.0KB".
const x1FileSize = 24 * 1024

// x1ToolUseID is the spec's fixed tool_use_id — the retrieval key the index half of the round
// trip is queried by.
const x1ToolUseID = "toolu_v3_001"

// x1Path is the project-relative path of the fixture, already in paths.Key form.
const x1Path = "src/auth.ts"

// x1SecretPrefix is the credential prefix that must never appear in anything the store persists.
// It is split across a `+` so no contiguous credential-shaped run appears in this source file for
// a secret scanner to match (the same convention as internal/testutil/secrettokens.go).
var x1SecretPrefix = []byte("sk-" + "ant-")

// x1Timestamp is the fixture's one volatile timestamp, which the timestamps canonicalizer class
// must strip to "<ts>".
const x1Timestamp = "2026-08-11T09:14:22.318Z"

// x1TombstoneRe is the §5 X1 row's exact grammar for the marker this fixture must render.
var x1TombstoneRe = regexp.MustCompile(
	`^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$`)

// x1AuthTS builds the 24 KB TypeScript fixture: CRLF line endings throughout, the
// @@SEC_ANTHROPIC_AB@@ credential literal on line 40, one ISO-8601 timestamp, and an
// `export function refreshToken` declaration at column 0 for the span half of the round trip.
//
// Filler lines deliberately carry no digits, so no canonicalizer class other than crlf and
// timestamps has anything to strip and the byte-identity assertion below stays legible.
func x1AuthTS(t *testing.T) []byte {
	t.Helper()

	secret, ok := testutil.SecretTokenValue("@@SEC_ANTHROPIC_AB@@")
	require.True(t, ok, "testutil no longer knows @@SEC_ANTHROPIC_AB@@")

	var lines []string
	for range 39 {
		lines = append(lines, "// auth module filler commentary, deliberately digit-free padding text")
	}
	// Line 40: the credential literal the redactor must scrub before anything is stored.
	lines = append(lines,
		`const apiKey = "`+secret+`";`,
		"",
		"// key rotated at "+x1Timestamp+" by the ops rotation job",
		"",
		"export function refreshToken(token: string): string {",
		`  const trimmed = token.trim();`,
		`  if (trimmed === "") {`,
		`    return "";`,
		"  }",
		`  return trimmed + apiKey;`,
		"}",
		"",
	)

	base := strings.Join(lines, "\r\n") + "\r\n"
	remaining := x1FileSize - len(base) - len("\r\n")
	require.Greater(t, remaining, len("//"), "fixture base outgrew the 24 KB budget")
	content := base + "//" + strings.Repeat("x", remaining-len("//")) + "\r\n"
	require.Len(t, content, x1FileSize)

	// The secret must sit on line 40 exactly, as the §5 X1 setup specifies.
	require.Contains(t, strings.Split(content, "\r\n")[39], secret,
		"the credential literal must be on line 40")
	return []byte(content)
}

// x1CanonOptions mirrors the observer's own canonOptions (internal/observer/tooluse.go): crlf is
// stripped unconditionally, the configured classes ride along when canonicalization is enabled,
// deltas are kept, and the MinHash options are the observer's. It is re-derived here, in the test,
// because reproducing the daemon's canonical bytes from the outside is exactly what the §5 X1
// byte-identity bullet asserts.
func x1CanonOptions(cfg config.Config) canon.Options {
	cc := cfg.Store.Canonicalize
	cls := []canon.Class{canon.Class("crlf")}
	if cc.Enabled {
		for _, s := range cc.Strip {
			if canon.Class(s) != canon.Class("crlf") {
				cls = append(cls, canon.Class(s))
			}
		}
	}
	return canon.Options{
		Strip:      cls,
		KeepDeltas: true,
		MinHash: sketch.MinHashOptions{
			Enabled:          cc.Enabled && cc.MinHash.Enabled,
			Permutations:     cc.MinHash.Permutations,
			ShingleSize:      5, //nomagic:allow mirrors observer.minHashShingleSize, a call-site property
			NearDupThreshold: cc.MinHash.NearDupThreshold,
		},
	}
}

// x1ReadAll drains rc, closes it, and fails the test on either error.
func x1ReadAll(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	b, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	return b
}

func TestV3_HookEventToTombstoneToRetrievalRoundTrip(t *testing.T) {
	bin := Build(t)
	original := x1AuthTS(t)
	p := testutil.NewProject(t, testutil.WithFiles(map[string]string{x1Path: string(original)}))
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	sess := core.SessionID("sess-e2e-v3-x1")
	ctx := context.Background()

	// cli → ipc → daemon: a real detached daemon, brought up by the real binary.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, sess), env)
	e2eWaitDaemonUp(t, p.Root)

	// Input 1: the PostToolUse payload, 24 KB of real file bytes in tool_response.
	stdout, stderr, code := Run(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, sess, x1ToolUseID, x1Path, string(original)), env)
	require.Equal(t, 0, code, "observe tool must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	// Expected output 1: stdout parses as a hookio.Output and deep-equals hookio.Empty().
	var out hookio.Output
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &out), "stdout:\n%s", stdout)
	require.Equal(t, hookio.Empty(), out, "PostToolUse emits nothing beyond the empty output")

	// The event is processed asynchronously behind the ACK: wait on the index itself, then flush.
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(obsToolUseLines(p.Root), "\n"), x1ToolUseID)
	}, obsProcessBound, obsProcessTick, "the tool use was never indexed")

	// Input 2: qompack flush; then take the daemon down so a fresh store.Open owns the tree.
	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, sess), env)
	e2eShutdownIfReachable(t, p.Root)

	st := p.Store(t)

	// Expected output 2: the index record, under the payload's own tool_use_id.
	rec, err := st.ToolUse(ctx, core.ToolUseID(x1ToolUseID))
	require.NoError(t, err)
	require.Equal(t, "FileRead", rec.Tool, "the record carries the display name, not the host's")
	wantNorm, err := paths.Norm(p.Root, x1Path)
	require.NoError(t, err)
	require.Equal(t, paths.Key(wantNorm), rec.Path)
	require.Equal(t, x1Path, rec.Path, "the paths.Key form of the fixture path is src/auth.ts")
	require.False(t, rec.Root.IsZero(), "the record must carry a real store root")
	require.Positive(t, rec.Tokens)
	require.Equal(t, store.StatusOK, rec.Status)
	require.Equal(t, int64(x1FileSize), rec.Bytes, "Bytes is the raw pre-canonicalization size")

	// Expected output 3: the tombstone, rendered against the §5 X1 grammar. This is the G3.2
	// round trip — the marker's hash is the retrieval key.
	ts := observer.Tombstone(rec)
	require.Regexp(t, x1TombstoneRe, ts)
	require.Contains(t, ts, "24.0KB", "24576 bytes at the binary divisor renders as 24.0KB")

	// Expected output 4: the short hash embedded in the marker is rec.Root's own Short form, and
	// core.ParseHash round-trips the record's hash to the same short form.
	short := strings.TrimPrefix(ts, "[cleared: sha256:")[:12]
	require.Equal(t, rec.Root.Short(), short)
	parsed, err := core.ParseHash(rec.Root.String())
	require.NoError(t, err)
	require.Equal(t, short, parsed.Short(), "the marker's short form addresses the parsed hash")

	// Expected output 5: store.Open serves the redacted-then-canonicalized bytes.
	got := x1ReadAll(t, mustOpen(t, st, ctx, rec.Root))

	require.NotContains(t, string(got), string(x1SecretPrefix),
		"(a) redaction ran before storage: no credential literal survives")
	require.Contains(t, string(got), "«redacted:anthropic_key»",
		"(b) the fixed-width placeholder stands where the credential was")
	require.Contains(t, string(got), "<ts>",
		"(c) the timestamps canonicalizer replaced the ISO timestamp")
	require.NotContains(t, string(got), x1Timestamp,
		"(c) the ISO timestamp itself is gone")
	require.NotContains(t, string(got), "\r",
		"(d) the crlf canonicalizer left only \\n line endings")

	// (e) Byte identity: the stored content is exactly redact → canon over the original, with the
	// observer's own canon options re-derived here.
	red, _ := redact.New(p.Cfg).Redact(original)
	cr, err := canon.Default(p.Cfg.Store.Canonicalize).Run("FileRead", x1Path, red, x1CanonOptions(p.Cfg))
	require.NoError(t, err)
	require.Equal(t, cr.Canonical, got,
		"(e) stored bytes == canon.Default(cfg).Run(FileRead, src/auth.ts, redact(original)).Canonical")

	// Expected output 6: no object on disk, decompressed, contains the credential prefix.
	x1AssertNoSecretInObjects(t, p.Root)

	// Expected output 7: the minimum-sufficient-span contract SP-13 will consume in wave 3,
	// composed here as §5's Rule requires — symbols supplies the span, OpenSpan serves it.
	off := bytes.Index(got, []byte("refreshToken"))
	require.GreaterOrEqual(t, off, 0, "the canonical bytes must still contain refreshToken")
	sym, ok := symbols.New().Enclosing(x1Path, got, off)
	require.True(t, ok, "Enclosing must resolve the refreshToken declaration")
	require.Equal(t, "refreshToken", sym.Name)
	span := x1ReadAll(t, mustOpenSpan(t, st, ctx, rec.Root, int64(sym.Offset), int64(sym.Len)))
	require.Equal(t, got[sym.Offset:sym.Offset+sym.Len], span,
		"OpenSpan must return exactly the enclosing function's bytes")
}

// mustOpen and mustOpenSpan adapt the store's two readers to require-style call sites.
func mustOpen(t *testing.T, st store.Store, ctx context.Context, root core.Hash) io.ReadCloser {
	t.Helper()
	rc, err := st.Open(ctx, root)
	require.NoError(t, err)
	return rc
}

func mustOpenSpan(t *testing.T, st store.Store, ctx context.Context, root core.Hash, off, n int64) io.ReadCloser {
	t.Helper()
	rc, err := st.OpenSpan(ctx, root, off, n)
	require.NoError(t, err)
	return rc
}

// x1AssertNoSecretInObjects walks .qompack/objects/** and asserts no object, decompressed, holds
// the credential prefix. A file store.Decode rejects (a staging leftover, an uncompressed store)
// is scanned raw instead — either way its bytes are inspected, never skipped.
func x1AssertNoSecretInObjects(t *testing.T, root string) {
	t.Helper()
	objects := paths.Of(root).Objects
	seen := 0
	err := filepath.WalkDir(paths.Long(objects), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(paths.Long(p))
		require.NoError(t, rerr)
		plain, derr := store.Decode(b)
		if derr != nil {
			plain = b
		}
		require.NotContains(t, string(plain), string(x1SecretPrefix),
			"object %s holds a credential literal after decompression", p)
		seen++
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, seen, "the walk must have inspected at least one stored object")
}
