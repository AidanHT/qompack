package integration

// storepipeline_test.go implements §4.1 of plans/V2-VERIFY-primitives-store-dag-and-baseline.md:
// store ingest with the real chunker, canonicalizers and MinHash. On every wave-1 branch
// store.Deps{Chunker, Canon, Symbols, Tokens, Redact} were stubs or fakes; these tests are the
// first execution of the real Qompack.md §8.1 item-1 pipeline — REDACT, then canonicalize, then
// chunk — with every dependency real, and they pin the seams no in-package test can reach:
// the store's internal pipeline order against canon's public output, canon.Restore against the
// store's delta-root wire format, SP-03's MinHash consumed through SP-06's near-dup surface, the
// §10 Phase 1 dedup criterion composed end to end, and §13 invariant 7 under canonicalization.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// corpusRel locates the committed tool-output corpus from this package's directory. The fixtures
// are shared with internal/canon's goldens and test/dedup's harness, which is deliberate: the
// bytes these tests push through the real store are the same bytes those suites measured.
const corpusRel = "../../testdata/corpora/toolout"

// specChunkMin and specChunkMax are the chunk-length window §4.1 pins: "every chunk length in
// [1024, 16384] except the last". They must equal the loaded store.chunk configuration — asserted
// as fixture sanity — because the window below is quoted from the spec, not read from config, so
// a config-default drift would otherwise silently change what this file verifies.
const (
	specChunkMin = 1024
	specChunkMax = 16384
)

// nearDupThresholdWant is the Jaccard threshold §4.1 requires the loaded configuration to carry
// ("nearDupThreshold = 0.9 from config"): the assertion below uses the config value, and this
// constant is what proves the config value is the one the spec named.
const nearDupThresholdWant = 0.9

// phase1PathCount is the read-heavy session's breadth: ten synthetic file paths, four reads each,
// forty puts (§4.1, TestIntegration_Phase1DedupRatioWithRealPipeline).
const phase1PathCount = 10

// phase1RatioFloor is Qompack.md §10's Phase 1 exit criterion, quoted: "store size vs. raw
// transcript ratio ≥ 4:1 on read-heavy sessions". The measured number is far above it (see the
// test's log line); the floor stays at the criterion so a future regression fails loudly instead
// of hiding under a tracked measurement.
const phase1RatioFloor = 4.0

// disabledCanonJSON is the project config that turns store.canonicalize.enabled off, for the
// "without canonicalization" half of the Phase 1 measurement.
const disabledCanonJSON = `{"store":{"canonicalize":{"enabled":false}}}`

// secretRunLimit is the longest run of secret bytes §4.1 tolerates inside any stored object:
// "no object contains a substring of any secret longer than 8 characters". The window scanned
// below is secretRunLimit+1 bytes wide.
const secretRunLimit = 8

// corpus reads one committed tool-output fixture.
func corpus(t *testing.T, group, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusRel, group, name))
	require.NoError(t, err, "corpus fixture missing: %s/%s", group, name)
	return b
}

// readRoot re-materializes root's full content through the store's own read path.
func readRoot(t *testing.T, ctx context.Context, s store.Store, root core.Hash) []byte {
	t.Helper()
	rc, err := s.Open(ctx, root)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	return b
}

// TestIntegration_StorePutUsesRealChunkerAndCanon is §4.1's first seam: one Put through the real
// pipeline, with the store's output checked against the same collaborators driven directly. The
// closing equality — store read-back versus canon.Default(...).Run over the raw fixture — is the
// load-bearing one: it pins the store's internal pipeline order (redact → canonicalize → chunk)
// against the canonicalizer's public output, which no in-package test can do because neither
// package may depend on the other's real implementation in its own tests.
func TestIntegration_StorePutUsesRealChunkerAndCanon(t *testing.T) {
	p := testutil.NewProject(t)
	s := openRealStore(t, p)
	ctx := context.Background()

	require.Equal(t, specChunkMin, p.Cfg.Store.Chunk.Min,
		"fixture sanity: §4.1 pins the [%d, %d] window from the default config", specChunkMin, specChunkMax)
	require.Equal(t, specChunkMax, p.Cfg.Store.Chunk.Max,
		"fixture sanity: §4.1 pins the [%d, %d] window from the default config", specChunkMin, specChunkMax)

	raw := corpus(t, "testrunner", "go-test-pass.txt")
	opts := canon.OptionsFrom(p.Cfg.Store.Canonicalize, true)

	res, err := s.PutBytes(ctx, raw, store.PutOptions{Tool: "Bash", Canon: opts})
	require.NoError(t, err)
	require.Zero(t, res.Redacted,
		"fixture sanity: the reference Run below is over the RAW fixture, which is only equal to "+
			"the store's redact-then-canonicalize output while the redactor finds nothing in it")

	// Canonicalization actually ran: go-test output is full of "(0.00s)" durations the real
	// canonicalizers collapse to "(<d>)".
	require.Less(t, res.Root.CanonBytes, res.Root.RawBytes,
		"CanonBytes must shrink below RawBytes when the real canonicalizers run")

	// Every chunk inside the FastCDC window except the last, which may legitimately be short.
	require.NotEmpty(t, res.Root.Chunks)
	for i, c := range res.Root.Chunks {
		require.Positive(t, c.Len, "chunk %d has no bytes", i)
		require.LessOrEqual(t, c.Len, specChunkMax, "chunk %d exceeds the configured max", i)
		if i < len(res.Root.Chunks)-1 {
			require.GreaterOrEqual(t, c.Len, specChunkMin,
				"chunk %d of %d is below the configured min; only the last chunk may be short",
				i, len(res.Root.Chunks))
		}
	}

	ref, err := canon.Default(p.Cfg.Store.Canonicalize).Run("Bash", "", raw, opts)
	require.NoError(t, err)

	// The root hash is the chunker's own Merkle root over the canonical bytes: same split, same
	// hash, no store-private re-derivation.
	chunks := chunk.New(chunk.FromConfig(p.Cfg)).Split(ref.Canonical)
	require.Equal(t, chunk.RootHash(chunks), res.Root.Hash,
		"PutResult.Root.Hash must equal chunk.RootHash over the real chunker's split of the canonical bytes")

	require.Equal(t, ref.Canonical, readRoot(t, ctx, s, res.Root.Hash),
		"the store's read-back must equal canon.Default(...).Run's Canonical byte-for-byte — "+
			"this pins the redact → canonicalize → chunk pipeline order")
}

// rootsLine is the slice of one index/roots.jsonl record this file needs: enough to find a
// content root's volatile-delta side record. The full wire shape is internal/store's and is
// golden-pinned there (Rule W-2); parsing two fields of it here is reading a frozen format, not
// re-specifying it.
type rootsLine struct {
	Root   string `json:"root"`
	Deltas string `json:"deltas"`
}

// deltaRootOf returns the delta root recorded alongside content root want, by scanning the
// project's index/roots.jsonl. The index file is the only public place the association exists —
// store.Root deliberately does not carry it — so reading the frozen wire format is the seam.
func deltaRootOf(t *testing.T, p *testutil.Project, want core.Hash) core.Hash {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Index, "roots.jsonl")))
	require.NoError(t, err)

	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rl rootsLine
		require.NoError(t, json.Unmarshal([]byte(line), &rl), "unparseable roots.jsonl line: %s", line)
		if rl.Root != want.String() || rl.Deltas == "" {
			continue
		}
		h, err := core.ParseHash(rl.Deltas)
		require.NoError(t, err)
		return h
	}
	t.Fatalf("no roots.jsonl line for %s carries a delta root; KeepRaw did not store the side record", want.Short())
	return core.Hash{}
}

// decodeDeltas parses the store's compact {"o","l","c","s"} delta array back into canon.Delta
// values — the wire format marshalDeltas writes and the store's goldens pin.
func decodeDeltas(t *testing.T, blob []byte) []canon.Delta {
	t.Helper()
	var wire []struct {
		O int    `json:"o"`
		L int    `json:"l"`
		C string `json:"c"`
		S string `json:"s"`
	}
	require.NoError(t, json.Unmarshal(blob, &wire))
	out := make([]canon.Delta, 0, len(wire))
	for _, w := range wire {
		out = append(out, canon.Delta{Offset: w.O, Len: w.L, Original: w.S, Class: canon.Class(w.C)})
	}
	return out
}

// TestIntegration_CanonKeepRawRestoresThroughStore asserts SP-04's Restore inverse and SP-06's
// delta-root encoding agree on the wire format: a KeepRaw put, read back as canonical bytes plus
// the stored delta record, must restore to the redacted input byte-for-byte.
//
// DEVIATION from §4.1's letter: the spec names bash/npm-install.txt "(timestamps + ANSI +
// durations)", but that fixture contains none of the three — its committed canon golden is
// byte-identical to the source, so a KeepRaw put of it stores no delta root at all and the test
// would pin nothing. bash/curl-verbose.txt is the same corpus group with genuinely volatile
// content (addresses, ports, durations), so it is what actually exercises the seam.
func TestIntegration_CanonKeepRawRestoresThroughStore(t *testing.T) {
	p := testutil.NewProject(t)
	s := openRealStore(t, p)
	ctx := context.Background()

	raw := corpus(t, "bash", "curl-verbose.txt")
	res, err := s.PutBytes(ctx, raw, store.PutOptions{Tool: "Bash", KeepRaw: true})
	require.NoError(t, err)
	require.Less(t, res.Root.CanonBytes, res.Root.RawBytes,
		"fixture sanity: the fixture must carry volatile spans, or there are no deltas to store")

	canonical := readRoot(t, ctx, s, res.Root.Hash)
	deltaRoot := deltaRootOf(t, p, res.Root.Hash)
	deltas := decodeDeltas(t, readRoot(t, ctx, s, deltaRoot))
	require.NotEmpty(t, deltas, "the stored delta record must not be empty")

	restored, err := canon.Restore(canonical, deltas)
	require.NoError(t, err)

	// The inverse reaches the REDACTED input, never the raw one: redaction runs before
	// canonicalization and is deliberately irreversible (§13 invariant 7).
	redacted, _ := redact.New(p.Cfg).Redact(raw)
	require.Equal(t, redacted, restored,
		"canon.Restore over the store's canonical bytes and stored deltas must reproduce the redacted input")
}

// TestIntegration_StoreNearDupUsesRealMinHash is the §8.1 "same test suite, one new failure"
// claim, proven across SP-03's MinHash, SP-04's canonicalizers and SP-06's store for the first
// time: two runs of one Go test suite, differing by a rerun's new failure, must be detected as
// near-duplicates above the configured Jaccard threshold.
func TestIntegration_StoreNearDupUsesRealMinHash(t *testing.T) {
	p := testutil.NewProject(t)
	mh := p.Cfg.Store.Canonicalize.MinHash
	require.True(t, mh.Enabled, "fixture sanity: §4.1 requires minhash enabled from config")
	require.Equal(t, nearDupThresholdWant, mh.NearDupThreshold,
		"fixture sanity: §4.1 requires nearDupThreshold %.1f from config", nearDupThresholdWant)

	s := openRealStore(t, p)
	ctx := context.Background()
	const logPath = "logs/go-test.txt"

	first, err := s.PutBytes(ctx, corpus(t, "testrunner", "go-test-pass.txt"),
		store.PutOptions{Tool: "Bash", Path: logPath})
	require.NoError(t, err)
	require.Nil(t, first.NearDup, "the first put has no prior version to be near")

	second, err := s.PutBytes(ctx, corpus(t, "testrunner", "go-test-rerun.txt"),
		store.PutOptions{Tool: "Bash", Path: logPath})
	require.NoError(t, err)

	require.NotNil(t, second.NearDup, "the rerun must be detected as a near-duplicate of the first run")
	require.GreaterOrEqual(t, second.NearDup.Jaccard, mh.NearDupThreshold,
		"one new failure in an otherwise identical suite must estimate at or above the configured threshold")
	require.Equal(t, first.Root.Hash, second.NearDup.PriorRoot,
		"PriorRoot must be the first put's root")
	require.Less(t, second.NearDup.DeltaBytes, second.Root.CanonBytes/2,
		"near-duplicates must differ by less than half the second version's canonical size")
}

// phase1Session ingests §4.1's read-heavy session — ten synthetic file paths, each read four
// times as the four committed fileread-auth versions (2, 18 and 240 lines edited between
// successive reads), forty puts — into a fresh project whose store.canonicalize.enabled is
// canonOn, and returns the resulting Stats.
func phase1Session(t *testing.T, canonOn bool) store.Stats {
	t.Helper()

	var opts []testutil.ProjectOpt
	if !canonOn {
		opts = append(opts, testutil.WithConfig(disabledCanonJSON))
	}
	p := testutil.NewProject(t, opts...)
	require.Equal(t, canonOn, p.Cfg.Store.Canonicalize.Enabled,
		"fixture sanity: the project config must actually carry canonicalize.enabled=%v", canonOn)

	// The knob must be LIVE, not decorative: canon.Default gates at registry construction, so a
	// disabled configuration builds a registry holding only the structural crlf normalizer while
	// an enabled one holds the full fourteen. Without this check, an equal with/without ratio
	// could also mean the disable knob silently stopped doing anything.
	names := canon.Default(p.Cfg.Store.Canonicalize).Names()
	if canonOn {
		require.Greater(t, len(names), 1,
			"an enabled configuration must register the volatile canonicalizers, not just crlf")
	} else {
		require.Equal(t, []string{"crlf"}, names,
			"a disabled configuration must register ONLY the structural crlf normalizer")
	}

	s := openRealStore(t, p)
	ctx := context.Background()

	versions := [][]byte{
		corpus(t, "sp06", "fileread-auth-v1.txt"),
		corpus(t, "sp06", "fileread-auth-v2.txt"),
		corpus(t, "sp06", "fileread-auth-v3.txt"),
		corpus(t, "sp06", "fileread-auth-v4.txt"),
	}

	var wantRaw int64
	for i := 0; i < phase1PathCount; i++ {
		path := fmt.Sprintf("src/svc%02d/auth.ts", i)
		for _, body := range versions {
			_, err := s.PutBytes(ctx, body, store.PutOptions{Tool: "FileRead", Path: path})
			require.NoError(t, err)
			wantRaw += int64(len(body))
		}
	}

	st, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, wantRaw, st.RawBytes,
		"RawBytes must count all %d puts, including every exact duplicate", phase1PathCount*len(versions))
	require.Positive(t, st.Bytes)
	return st
}

// TestIntegration_Phase1DedupRatioWithRealPipeline is the §10 Phase 1 exit criterion, composed:
// the ≥ 4:1 store-size-versus-raw-transcript ratio produced for the first time by SP-04's
// canonicalizers and SP-06's store together, on a read-heavy session, measured with and without
// canonicalization.
//
// DEVIATION from §4.1's letter, in one place. The spec asks that the enabled ratio be STRICTLY
// greater than the disabled one, but on the session the same sentence prescribes the two are
// provably identical: the sp06 corpus is canonically inert by design. §2.0b defect 3 records that
// its canon goldens are "byte-identical to source, matching the existing fileread group where
// canon is correctly a no-op", and testdata/canon-dedup-report.json measures both fileread groups
// at gain exactly 1.0. Identical canonical bytes mean identical chunks, identical compressed
// objects and an identical ratio, whichever way the knob points — the fixture-sanity loop below
// proves the inertness, and phase1Session proves the knob itself is live, so an equal pair of
// ratios is a property of this corpus and of nothing else.
//
// The two ratios are therefore asserted EQUAL rather than ordered. That is the strongest claim
// the corpus supports, and it is deliberately brittle in the right direction: the moment the
// session gains volatile content, the inertness sanity and the equality both fail, and whoever
// makes that change must restore the spec's strict inequality here.
func TestIntegration_Phase1DedupRatioWithRealPipeline(t *testing.T) {
	// Fixture sanity: the corpus really is canonically inert under the default configuration. If
	// content with volatile spans is ever added to the session, the strict with-versus-without
	// comparison becomes meaningful and must replace the equality below.
	p := testutil.NewProject(t)
	reg := canon.Default(p.Cfg.Store.Canonicalize)
	opts := canon.OptionsFrom(p.Cfg.Store.Canonicalize, false)
	for _, name := range []string{"fileread-auth-v1.txt", "fileread-auth-v2.txt", "fileread-auth-v3.txt", "fileread-auth-v4.txt"} {
		body := corpus(t, "sp06", name)
		ref, err := reg.Run("FileRead", "src/auth.ts", body, opts)
		require.NoError(t, err)
		require.Equal(t, body, ref.Canonical,
			"fixture sanity: %s is expected to be canonically inert (§2.0b defect 3); it no longer is, "+
				"so the equality assertion below must become the spec's strict inequality", name)
	}

	withCanon := phase1Session(t, true)
	withoutCanon := phase1Session(t, false)
	t.Logf("phase 1 read-heavy session (40 puts): DedupRatio %.2f with canonicalization "+
		"(raw=%d bytes=%d objects=%d), %.2f without (raw=%d bytes=%d objects=%d)",
		withCanon.DedupRatio, withCanon.RawBytes, withCanon.Bytes, withCanon.Objects,
		withoutCanon.DedupRatio, withoutCanon.RawBytes, withoutCanon.Bytes, withoutCanon.Objects)

	require.GreaterOrEqual(t, withCanon.DedupRatio, phase1RatioFloor,
		"Qompack.md §10 Phase 1 exit criterion: read-heavy sessions must dedup at ≥ %.1f:1 "+
			"(got %.2f: raw=%d bytes=%d)", phase1RatioFloor, withCanon.DedupRatio, withCanon.RawBytes, withCanon.Bytes)

	require.Equal(t, withCanon.DedupRatio, withoutCanon.DedupRatio,
		"the two ratios diverged, so the session is no longer canonically inert — that is the "+
			"strict comparison becoming meaningful, not a regression; restore §4.1's strict "+
			"'enabled > disabled' assertion here")
}

// secretSeed is one of the ten built-in §5.22a secret families: the rule expected to fire, the
// transcript line carrying the planted secret, and the exact byte run that must never reach
// objects/ — for whole-match rules the entire match, for group-replacing rules (bearer_token,
// credentialed_uri, assignment_secret, dotenv_value) the secret component alone, since the key,
// scheme or header legitimately survives.
type secretSeed struct {
	rule   string
	line   string
	secret string
}

// plantedPEM is the pem_private_key family's planted block. The body is fabricated base64-shaped
// text — no real key material — kept free of "sk-", "eyJ" and ALL-CAPS assignment shapes so no
// other rule can match inside it.
const plantedPEM = `-----BEGIN RSA PRIV` + `ATE KEY-----
MIIEowIBAAKCAQEAvXjRmqLbQzWkcPnHhFuYdGxJsVeMNpBaZqRoCtDwEfUgIhLj
XrSyOBvAkTmHcQnZePldRsWuGxJfRbNvMYtCqLzDhoAiEVpSbXcWmDdQzRfLgNvJ
uHtEeYwRrTqPpOoIiKkLlZzXxCcVvBbNnMmQqWwEeRrTtYyUuIiOoPpAaSsDdFfG
@@SEC_PEM_RSA_END@@`

// secretSeeds returns one planted instance of each of the ten built-in redact rule families, in
// internal/redact's own §5.22a rule order. Every secret is longer than secretRunLimit bytes (so
// the fragment scan below has at least one window) and every 9-byte window of every secret is
// distinctive enough that it cannot occur in the surrounding npm transcript by coincidence.
func secretSeeds() []secretSeed {
	return []secretSeed{
		{rule: "pem_private_key", line: plantedPEM, secret: plantedPEM},
		{
			rule:   "aws_access_key_id",
			line:   "found credential AKIA" + "IOSFODNN7EXAMPLE in build environment",
			secret: "AKIA" + "IOSFODNN7EXAMPLE",
		},
		{
			rule:   "github_token",
			line:   "ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12",
			secret: "ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12",
		},
		{
			rule:   "anthropic_key",
			line:   "sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz",
			secret: "sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz",
		},
		{
			rule:   "generic_sk_key",
			line:   "sk-proj9f8e7d6c5b4a39281706fedcba",
			secret: "sk-proj9f8e7d6c5b4a39281706fedcba",
		},
		{
			rule:   "jwt",
			line:   "session grant eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJxb21wYWNrLXZlcmlmeSJ9.k9YxWvTqLmPnRuZsAbCdEfGhIjKlMnOp",
			secret: "eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJxb21wYWNrLXZlcmlmeSJ9.k9YxWvTqLmPnRuZsAbCdEfGhIjKlMnOp",
		},
		{
			rule:   "bearer_token",
			line:   "Authorization: Bearer mF9dXhKtbQvNwPzYcRjLuGeSaBoTk",
			secret: "mF9dXhKtbQvNwPzYcRjLuGeSaBoTk",
		},
		{
			rule:   "credentialed_uri",
			line:   "postgres://deploy:tr0ub4dor8gorgonzola@db.internal:5432/prod",
			secret: "tr0ub4dor8gorgonzola",
		},
		{
			rule:   "assignment_secret",
			line:   "client_secret=9uPh0ldZ4qW7eXcV1bNm6tYr",
			secret: "9uPh0ldZ4qW7eXcV1bNm6tYr",
		},
		{
			rule:   "dotenv_value",
			line:   "DATABASE_PASSWORD=correcthorsebatterystaple91",
			secret: "correcthorsebatterystaple91",
		},
	}
}

// npmTranscript embeds the planted secret lines in ANSI-coloured npm output with timestamps, the
// way a real `npm install` leak looks. The colouring wraps whole lines and never intersects a
// secret, because redaction runs on raw bytes BEFORE the ANSI canonicalizer strips the colour
// codes — an escape inside a token would defeat the rule, which is a different test's problem.
// The filler avoids every rule's mandatory prefilter literal so exactly the planted instances
// match.
func npmTranscript(seeds []secretSeed) []byte {
	var b strings.Builder
	stamp := func(i int) string {
		return fmt.Sprintf("\x1b[2m2026-01-01T00:00:%02d.%03dZ\x1b[22m", i%60, (i*137)%1000)
	}
	warn := "\x1b[33mnpm\x1b[39m \x1b[35mwarn\x1b[39m"

	b.WriteString(stamp(1) + " " + warn + " deprecated har-validator@5.1.5: this library is no longer supported\n")
	b.WriteString(stamp(2) + " " + warn + " deprecated uuid@3.4.0: uuid@10 and below is no longer supported\n")
	b.WriteString("\n> qompack-fixture@1.0.0 postinstall\n> node scripts/leak-audit.js\n\n")
	b.WriteString("[leak-audit] captured environment before scrubbing:\n")
	for i, s := range seeds {
		b.WriteString(stamp(3+i) + " " + warn + " leak-audit line " + fmt.Sprint(i) + "\n")
		b.WriteString(s.line + "\n")
	}
	b.WriteString("\n" + stamp(50) + " added 117 packages in 4s\n")
	b.WriteString(stamp(51) + " \x1b[32m30\x1b[39m packages are looking for funding\n")
	return []byte(b.String())
}

// storedObjects walks every file under the project's objects/ tree and returns each one's
// decompressed content, keyed by its path relative to objects/.
func storedObjects(t *testing.T, p *testutil.Project) map[string][]byte {
	t.Helper()
	root := paths.Of(p.Root).Objects
	out := map[string][]byte{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(paths.Long(path))
		require.NoError(t, rerr)
		content := raw
		if strings.HasSuffix(path, ".zst") {
			content, rerr = store.Decode(raw)
			require.NoError(t, rerr, "object %s must be a valid zstd frame", path)
		}
		rel, rerr := filepath.Rel(root, path)
		require.NoError(t, rerr)
		out[rel] = content
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestIntegration_SecretsNeverSurviveTheRealPipeline is §13 invariant 7 under canonicalization,
// which no single branch could test: one planted instance of each of the ten built-in secret
// families goes through the full redact → canonicalize → chunk → compress pipeline, and no stored
// object may contain a secret literal — nor any fragment of one longer than secretRunLimit bytes,
// which is what proves the canonicalizers did not reconstruct a secret by joining content across
// a placeholder boundary.
func TestIntegration_SecretsNeverSurviveTheRealPipeline(t *testing.T) {
	p := testutil.NewProject(t)
	s := openRealStore(t, p)
	ctx := context.Background()

	seeds := secretSeeds()
	for _, seed := range seeds {
		require.Greater(t, len(seed.secret), secretRunLimit,
			"fixture sanity: the %s secret must be longer than %d bytes or the fragment scan below is vacuous",
			seed.rule, secretRunLimit)
	}

	res, err := s.PutBytes(ctx, npmTranscript(seeds), store.PutOptions{Tool: "Bash", Path: "logs/npm-install.txt"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Redacted, len(seeds),
		"all %d planted secret families must fire on the way in", len(seeds))
	require.NoError(t, s.Flush(ctx))

	objects := storedObjects(t, p)
	require.NotEmpty(t, objects, "the put must have written at least one object")

	for _, seed := range seeds {
		for rel, content := range objects {
			text := string(content)
			require.NotContains(t, text, seed.secret,
				"object %s contains the %s secret literal", rel, seed.rule)
			for i := 0; i+secretRunLimit+1 <= len(seed.secret); i++ {
				window := seed.secret[i : i+secretRunLimit+1]
				require.False(t, strings.Contains(text, window),
					"object %s contains %q — a fragment of the %s secret longer than %d bytes, "+
						"so canonicalization reconstructed secret material across a placeholder boundary",
					rel, window, seed.rule, secretRunLimit)
			}
		}
	}
}
