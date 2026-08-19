package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// storeIngestPayloads is how many tool results TestE2E_StoreSurvivesProcessRestart ingests before
// closing the store.
const storeIngestPayloads = 200

// TestE2E_StoreSurvivesProcessRestart ingests a full session's worth of content, closes the store
// completely, reopens it over the same directory, and asserts nothing was lost.
//
// A note on what "process" means here, because it is narrower than the name suggests and should not
// be read as more than it is. The store is not yet reachable from the qompack binary: every hook
// that would drive it (PostToolUse and friends) is still an SP-08 stub, so there is no command this
// test could spawn that would put content into objects/. What it therefore exercises is a full
// close/reopen cycle — every file handle released, every in-memory index dropped, and the whole
// state rebuilt by replaying the append-only logs from disk — which is precisely the recovery path
// a real restart depends on. When SP-08 lands its hooks, this test should be extended to drive the
// ingest through the built binary; the assertions below are already written against the public
// Store interface so that change is additive.
func TestE2E_StoreSurvivesProcessRestart(t *testing.T) {
	p := testutil.NewProject(t)
	ctx := context.Background()

	type expectation struct {
		root   core.Hash
		bytes  int64
		path   string
		toolID core.ToolUseID
	}

	var want []expectation
	var beforeStats store.Stats
	var segIDs []core.SegmentID

	// ── first "process": ingest ──
	func() {
		s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
		require.NoError(t, err)

		for i := 0; i < storeIngestPayloads; i++ {
			path := fmt.Sprintf("src/pkg%02d/file%03d.ts", i%10, i)
			payload := []byte(fmt.Sprintf(
				"// generated tool result %d\nexport function handler%d(req: Request): Response {\n"+
					"  const id = req.headers.get(\"x-request-id\");\n  return new Response(id);\n}\n", i, i))

			res, err := s.PutBytes(ctx, payload, store.PutOptions{Tool: "FileRead", Path: path})
			require.NoError(t, err)

			id := core.ToolUseID(fmt.Sprintf("toolu_e2e_restart_%04d", i))
			require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
				ID: id, Session: "sess_e2e_restart", Turn: core.TurnIndex(i),
				TS: core.UnixMilli(p.Clock.Now().UnixMilli()), Tool: "FileRead",
				Root: res.Root.Hash, Path: path, Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
			}))
			require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
				TS: core.UnixMilli(p.Clock.Now().UnixMilli()), Root: res.Root.Hash,
				Turn: core.TurnIndex(i), Bytes: res.Root.RawBytes,
			}))

			want = append(want, expectation{root: res.Root.Hash, bytes: res.Root.RawBytes, path: path, toolID: id})
			p.Clock.Advance(1)
		}

		// Two segments, one of them encoded, so the DPI state has to survive the restart too.
		for i := 0; i < 2; i++ {
			id, err := s.Segments().Open(ctx, store.Segment{
				Session: "sess_e2e_restart", StartTurn: core.TurnIndex(i * 100),
			})
			require.NoError(t, err)
			require.NoError(t, s.Segments().Close(ctx, id, core.TurnIndex(i*100+99),
				map[string]float64{"tokens": float64(1000 * (i + 1))}))
			segIDs = append(segIDs, id)
		}
		require.NoError(t, s.Segments().MarkEncoded(ctx, segIDs[:1], core.CheckpointSeq(3)))

		require.NoError(t, s.Flush(ctx))
		beforeStats, err = s.Stats(ctx)
		require.NoError(t, err)
		require.NoError(t, s.Close())
	}()

	require.Positive(t, beforeStats.Objects, "fixture sanity: the ingest must have written objects")

	// ── second "process": reopen over the same directory and verify ──
	s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	for i, w := range want {
		root, err := s.GetRoot(ctx, w.root)
		require.NoError(t, err, "root %d (%s) did not survive the restart", i, w.root.Short())
		require.Equal(t, w.bytes, root.RawBytes)

		rc, err := s.Open(ctx, w.root)
		require.NoError(t, err)
		content, err := io.ReadAll(rc)
		require.NoError(t, rc.Close())
		require.NoError(t, err)
		require.Len(t, content, int(w.bytes), "root %d re-materialized to the wrong length", i)

		rec, err := s.ToolUse(ctx, w.toolID)
		require.NoError(t, err, "tool_use %s did not survive the restart", w.toolID)
		require.Equal(t, w.root, rec.Root)
		require.Equal(t, w.path, rec.Path)

		hist, err := s.FileHistory(ctx, w.path)
		require.NoError(t, err, "file history for %s did not survive the restart", w.path)
		require.NotEmpty(t, hist)
		require.Equal(t, w.root, hist[len(hist)-1].Root)
	}

	// Segment state, including the encoded-once flag and its checkpoint sequence.
	encoded, err := s.Segments().Get(ctx, segIDs[0])
	require.NoError(t, err)
	require.True(t, encoded.EncodedOnce, "the encoded-once flag must survive a restart — it is the DPI guard")
	require.Equal(t, core.CheckpointSeq(3), encoded.CheckpointSeq)
	require.True(t, encoded.Closed)

	unencoded, err := s.Segments().Unencoded(ctx, "sess_e2e_restart")
	require.NoError(t, err)
	require.Len(t, unencoded, 1, "exactly the un-encoded segment must come back as un-encoded")
	require.Equal(t, segIDs[1], unencoded[0].ID)

	// And the accounting the Phase 1 exit criterion reads.
	afterStats, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeStats.Objects, afterStats.Objects, "object count changed across the restart")
	require.Equal(t, beforeStats.Bytes, afterStats.Bytes, "stored bytes changed across the restart")
	require.Equal(t, beforeStats.RawBytes, afterStats.RawBytes, "raw byte accounting changed across the restart")
	require.Equal(t, beforeStats.ToolUses, afterStats.ToolUses)
	require.Equal(t, beforeStats.Files, afterStats.Files)
}

// secretLiteral is one secret family's fixture text plus the exact substring that must never reach
// objects/ in any form.
type secretLiteral struct {
	family  string
	source  string // the corpus file the payload is taken from
	literal string // the substring no stored object may contain
}

// e2eSecretLiterals is all ten families of 00-ARCHITECTURE.md §5.22a. Each literal is copied from
// the corresponding testdata/corpora/secrets/ fixture, which is the source of truth for what a real
// positive looks like.
func e2eSecretLiterals() []secretLiteral {
	return []secretLiteral{
		{"pem_private_key", "pem_private_key.txt", "zSkiT7eDbUFdzwFiq467cZP31mAkEy0m11KPIeNZq1k=z3NP1YYk6MyU0qpAh+Fh"},
		{"aws_access_key_id", "aws_access_key_id.txt", "AKIA" + "IOSFODNN7EXAMPLE"},
		{"github_token", "github_token.txt", "ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12"},
		{"anthropic_key", "anthropic_key.txt", "sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz"},
		{"generic_sk_key", "generic_sk_key.txt", "@@SEC_GENERIC_SK@@"},
		{"jwt", "jwt.txt", "eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"bearer_token", "bearer_token.txt", "abcdefghijklmnopqrstuvwxyz012345"},
		{"credentialed_uri", "credentialed_uri.txt", "h4nter2"},
		{"assignment_secret", "assignment_secret.txt", "swordfishswordfish"},
		{"dotenv_value", "dotenv_value.txt", "sk_live_" + "aaaaaaaaaaa"},
	}
}

// TestE2E_SecretNeverLandsInObjects is §13 invariant 7, end to end.
//
// It is the single most important assertion in this slice. objects/ is content-addressed and
// immutable: a secret that reaches it cannot be removed without breaking every root that references
// its chunk, so "we will scrub it later" is not available as a recovery. The only defence is that it
// never gets written, which is why redaction happens inside Put/PutBytes before canonicalization and
// before chunking rather than as an audit pass afterwards.
//
// The test ingests all ten families through the ordinary Put path, then walks every object actually
// on disk, DECOMPRESSES it, and asserts none of the ten literals appears anywhere. Walking and
// decompressing — rather than trusting PutResult.Redacted — is the point: the claim being tested is
// about the bytes on disk, so the bytes on disk are what it reads.
func TestE2E_SecretNeverLandsInObjects(t *testing.T) {
	p := testutil.NewProject(t)
	ctx := context.Background()

	s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	literals := e2eSecretLiterals()

	// Ingest each family on its own, and then all of them concatenated, so both the isolated and
	// the mixed case are covered.
	var combined strings.Builder
	totalRedacted := 0
	for _, sl := range literals {
		payload := readSecretFixture(t, sl.source)
		require.Contains(t, string(payload), sl.literal,
			"fixture sanity: %s must actually contain the literal this test looks for", sl.source)
		combined.Write(payload)
		combined.WriteByte('\n')

		res, err := s.PutBytes(ctx, payload, store.PutOptions{
			Tool: "Bash", Path: "logs/" + sl.family + ".log",
		})
		require.NoError(t, err)
		require.Positive(t, res.Redacted, "%s: Put reported no redaction for a payload that is a known positive", sl.family)
		totalRedacted += res.Redacted
	}

	mixed, err := s.PutBytes(ctx, []byte(combined.String()), store.PutOptions{Tool: "Bash", Path: "logs/all.log"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, mixed.Redacted, len(literals),
		"the concatenated payload must redact at least once per family")

	require.NoError(t, s.Flush(ctx))

	// Walk every object on disk and decompress it.
	objectsDir := paths.Of(p.Root).Objects
	var objectCount int
	var plaintextBytes int64

	err = filepath.WalkDir(paths.Long(objectsDir), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		plain := raw
		if strings.HasSuffix(path, ".zst") {
			plain, readErr = store.Decode(raw)
			if readErr != nil {
				return fmt.Errorf("decoding object %s: %w", path, readErr)
			}
		}
		objectCount++
		plaintextBytes += int64(len(plain))

		for _, sl := range literals {
			require.NotContains(t, string(plain), sl.literal,
				"§13 invariant 7 VIOLATED: the %s secret reached objects/ (%s).\n"+
					"objects/ is content-addressed and immutable, so this cannot be scrubbed after the "+
					"fact — redaction must happen inside Put, before chunking", sl.family, filepath.Base(path))
		}
		return nil
	})
	require.NoError(t, err)

	require.Positive(t, objectCount, "fixture sanity: the walk must have found objects to inspect")
	require.Positive(t, plaintextBytes, "fixture sanity: the walk must have decompressed real content")
	t.Logf("walked %d objects, %d bytes decompressed, %d redactions reported across %d families",
		objectCount, plaintextBytes, totalRedacted+mixed.Redacted, len(literals))

	// The redaction placeholder MUST be present, which proves the content was stored redacted
	// rather than simply never stored at all.
	foundPlaceholder := false
	err = filepath.WalkDir(paths.Long(objectsDir), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || foundPlaceholder {
			return walkErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		plain := raw
		if strings.HasSuffix(path, ".zst") {
			if plain, readErr = store.Decode(raw); readErr != nil {
				return readErr
			}
		}
		if strings.Contains(string(plain), "«redacted:") {
			foundPlaceholder = true
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, foundPlaceholder,
		"no object contains a «redacted:…» placeholder — the secrets may have been dropped entirely "+
			"rather than redacted in place, which would mean this test proves nothing about redaction")
}

// readSecretFixture reads one testdata/corpora/secrets/ fixture.
func readSecretFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "secrets", name))
	require.NoError(t, err, "secret corpus fixture missing: %s", name)
	return testutil.ExpandSecretTokens(b)
}
