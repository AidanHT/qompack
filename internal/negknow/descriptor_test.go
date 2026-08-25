package negknow_test

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// goldenNegknowRoot is testdata/golden/contracts/negknow/, the parent of the frozen want/
// directory fixture_test.go reads. descriptors.golden.json lives here rather than under want/
// because want/ is frozen by Rule W-2 and this file is SP-09's own, hand-computed golden.
const goldenNegknowRoot = "../../testdata/golden/contracts/negknow"

// descriptorGoldenRow is one row of descriptors.golden.json.
//
// Most rows are a (target, approach, reason) triple plus everything Canonicalize derives from it.
// Row 1 is the exception and carries Literal: it is key_test.go's fixedDescriptor spelled as
// descriptor literals, because its ApproachClass ("widen-timeout") is a hand-chosen label from
// the frozen contract fixture and is NOT what ApproachClass("widen pool timeout") returns. Row 1
// exists so that key_hex can be compared against wantDescriptorKeyHex character for character,
// which is what proves this file was not regenerated around a changed Descriptor.Key.
type descriptorGoldenRow struct {
	Target        string `json:"target"`
	Approach      string `json:"approach"`
	Reason        string `json:"reason"`
	Path          string `json:"path"`
	Symbol        string `json:"symbol"`
	Class         string `json:"class"`
	ReasonHashHex string `json:"reason_hash_hex"`
	KeyHex        string `json:"key_hex"`
	MatchKeyHex   string `json:"match_key_hex"`
	Literal       bool   `json:"literal,omitempty"`
}

func readDescriptorGolden(t *testing.T) []descriptorGoldenRow {
	t.Helper()
	raw := readNegknowGolden(t, goldenNegknowRoot, "descriptors.golden.json")
	var rows []descriptorGoldenRow
	require.NoError(t, json.Unmarshal(raw, &rows))
	return rows
}

// TestSplitTarget_Table pins the three-rule target grammar: '#' wins over ':', a ':' suffix is a
// symbol only when it looks like an identifier, and everything else is a bare path.
//
// The drive-letter row is why rule 3 tests the suffix rather than splitting unconditionally: for
// "C:/proj/src/auth.ts" the text after the last colon is "/proj/src/auth.ts", which is not an
// identifier, so no split happens and the whole string is the path. Its expectation is asserted
// against paths.Key of the input rather than a hardcoded string because Key case-folds on Windows
// and macOS and does not on Linux.
//
// Rule 3 decides three ways, per controller ruling R17, and the three rows below are one of each:
// an identifier suffix is a SYMBOL and splits; a pure-digit suffix is a LINE NUMBER and is
// dropped, because a line number is a cursor position that moves with every edit above it and
// would re-key the same elimination each time; anything else is NOT a split at all. The
// drive-letter row is the third case and is what stops the second from being written as "strip
// any suffix that is not a symbol" — that rule would turn "C:/proj/src/auth.ts" into "C:".
func TestSplitTarget_Table(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantPath   string
		wantSymbol string
	}{
		{"path and symbol", "src/auth.ts:refreshToken", "src/auth.ts", "refreshToken"},
		{"bare path", "src/auth.ts", "src/auth.ts", ""},
		{"drive letter", "C:/proj/src/auth.ts", paths.Key("C:/proj/src/auth.ts"), ""},
		{"hash separator", "src/a.ts#Foo.bar", "src/a.ts", "Foo.bar"},
		{"line number dropped", "pkg/mod.go:12", "pkg/mod.go", ""},
		{"line number zero", "src/x.go:0", "src/x.go", ""},
		{"line number over empty prefix", ":12", "", ""},
		{"empty", "", "", ""},
		{"symbol only", ":sym", "", "sym"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotSymbol := negknow.SplitTarget(tc.target)
			require.Equal(t, tc.wantPath, gotPath)
			require.Equal(t, tc.wantSymbol, gotSymbol)
		})
	}
}

// TestDescriptorKey_Golden replays descriptors.golden.json: for every row it rebuilds the
// Descriptor and requires the canonical fields, the reason hash, Key() and MatchKey() to match
// byte for byte. That is what pins hashing stability across platforms and Go versions.
//
// The file is hand-computed and committed once; there is deliberately no -update flag, because
// key_hex is the output of the already-frozen Descriptor.Key and a regeneration switch would be a
// switch for silently re-keying every bloom entry already on disk.
func TestDescriptorKey_Golden(t *testing.T) {
	rows := readDescriptorGolden(t)
	require.Len(t, rows, 24, "the golden is specified as 24 rows")

	require.True(t, rows[0].Literal, "row 1 must be the fixedDescriptor literal row")
	require.Equal(t, wantDescriptorKeyHex, rows[0].KeyHex,
		"row 1's key_hex is the self-check that this file was not regenerated around a changed Key()")

	for i, row := range rows {
		t.Run(row.Class+"/"+row.Path, func(t *testing.T) {
			var d negknow.Descriptor
			if row.Literal {
				require.Empty(t, row.Target, "a literal row has no triple")
				require.Empty(t, row.Approach)
				require.Empty(t, row.Reason)
				d = negknow.Descriptor{
					NormalizedPath: row.Path,
					Symbol:         row.Symbol,
					ApproachClass:  row.Class,
					ReasonHash:     mustParseHash("sha256:" + row.ReasonHashHex),
				}
				require.Equal(t, fixedDescriptor, d,
					"row 1 must be key_test.go's fixedDescriptor spelled as literals")
			} else {
				d = negknow.Canonicalize(row.Target, row.Approach, row.Reason)
				require.Equal(t, row.Path, d.NormalizedPath, "row %d normalized_path", i+1)
				require.Equal(t, row.Symbol, d.Symbol, "row %d symbol", i+1)
				require.Equal(t, row.Class, d.ApproachClass, "row %d approach_class", i+1)
			}
			require.Equal(t, row.ReasonHashHex, hex.EncodeToString(d.ReasonHash[:]), "row %d reason_hash", i+1)
			require.Equal(t, row.KeyHex, hex.EncodeToString(d.Key()), "row %d key", i+1)
			require.Equal(t, row.MatchKeyHex, d.MatchHex(), "row %d match key", i+1)
		})
	}
}

// TestDescriptorGolden_CoversTheRequiredShapes asserts the golden actually exercises the four
// target/approach shapes the plan requires it to cover. Without this, a later edit could quietly
// drop the only symbol-less or only drive-letter row and the file would still pass.
//
// The drive-letter row is spelled with a lowercase drive letter and a lowercase path on purpose:
// paths.Key folds case on Windows and macOS but not on Linux, so any row whose path changes under
// folding would give this golden a different key_hex per platform, which is the opposite of what
// a cross-platform hashing pin is for. The row is still asserted through paths.Key of its input
// rather than against a hardcoded string.
func TestDescriptorGolden_CoversTheRequiredShapes(t *testing.T) {
	rows := readDescriptorGolden(t)

	var symbolless, hashSep, driveLetter, unclassified int
	for _, row := range rows {
		// This is what actually makes the file's key_hex column platform-independent, and it is
		// asserted rather than assumed: paths.Key folds case on Windows and macOS and does not on
		// Linux, so a row whose path contains an uppercase letter would hash two different ways
		// and this golden would fail on exactly one of the three CI platforms. Symbols are never
		// folded, so they are free to carry case (row 6's "Foo.bar" does).
		require.Equal(t, strings.ToLower(row.Path), row.Path,
			"every golden path must be fold-invariant, or key_hex differs per platform: %q", row.Path)

		if row.Literal {
			continue
		}
		if row.Symbol == "" && row.Path != "" {
			symbolless++
		}
		if strings.Contains(row.Target, "#") {
			hashSep++
		}
		if strings.HasPrefix(row.Path, "c:/") {
			driveLetter++
			require.Equal(t, paths.Key(row.Target), row.Path,
				"the drive-letter row is asserted via paths.Key of its input, not a hardcoded string")
		}
		if row.Class == "unclassified" {
			unclassified++
		}
	}
	require.Positive(t, symbolless, "golden needs at least one symbol-less target")
	require.Positive(t, hashSep, "golden needs at least one '#'-separated target")
	require.Positive(t, driveLetter, "golden needs at least one drive-letter target")
	require.Positive(t, unclassified, "golden needs at least one unclassified approach")
}

// TestMatchKey_IgnoresReason is the property the whole match key exists for: Query has no reason
// argument, so already_tried must be answerable from (path, symbol, approach class) alone. Three
// different reasons therefore share one MatchKey and hold three distinct identity Keys.
func TestMatchKey_IgnoresReason(t *testing.T) {
	const target, approach = "src/auth.ts:refreshToken", "widen pool timeout"
	reasons := []string{
		"pgbouncer 1.18 ignores statement_timeout",
		"the proxy caps it at 30s",
		"",
	}

	seenKeys := make(map[string]bool, len(reasons))
	var wantMatch string
	for i, reason := range reasons {
		d := negknow.Canonicalize(target, approach, reason)
		if i == 0 {
			wantMatch = d.MatchHex()
		}
		require.Equal(t, wantMatch, d.MatchHex(), "reason %q must not move the match key", reason)
		seenKeys[hex.EncodeToString(d.Key())] = true
	}
	require.Len(t, seenKeys, len(reasons), "each reason must produce its own identity key")
}

// TestMatchKey_SeparatorInjection proves a caller cannot forge a colliding MatchKey by embedding
// the field separator in a target. sanitizeField replaces every 0x1F in a descriptor field with a
// space before hashing, so a one-field target "a\x1Fb" hashes "a b" with an empty symbol and
// cannot collide with the two-field descriptor ("a", "b").
//
// The assertion is on MatchKey ONLY. Descriptor.Key's byte layout is frozen and takes its fields
// raw, so it is deliberately not the subject of this test.
func TestMatchKey_SeparatorInjection(t *testing.T) {
	injected := negknow.Canonicalize("a\x1Fb", "widen timeout", "r")
	require.Equal(t, "a\x1fb", injected.NormalizedPath, "the target must survive splitting as one field")
	require.Empty(t, injected.Symbol)

	pair := negknow.Descriptor{
		NormalizedPath: "a",
		Symbol:         "b",
		ApproachClass:  injected.ApproachClass,
		ReasonHash:     injected.ReasonHash,
	}
	require.NotEqual(t, pair.MatchKey(), injected.MatchKey())
	require.NotEqual(t, pair.MatchHex(), injected.MatchHex())
}

// TestReasonHash_Normalization pins the reason normalization: lowercase, then collapse every
// whitespace run to a single space and trim. Two spellings of the same sentence must produce one
// hash, or a re-recorded elimination would land on a second identity key and defeat dedup.
//
// The hash function itself is unexported, so it is exercised through Canonicalize - which is also
// the only way any caller reaches it.
func TestReasonHash_Normalization(t *testing.T) {
	a := negknow.Canonicalize("", "", "Pgbouncer  1.18\n IGNORES it")
	b := negknow.Canonicalize("", "", "pgbouncer 1.18 ignores it")
	require.Equal(t, a.ReasonHash, b.ReasonHash)

	spaced := negknow.Canonicalize("", "", "   \t pgbouncer 1.18 ignores it \n ")
	require.Equal(t, b.ReasonHash, spaced.ReasonHash, "leading and trailing whitespace must be trimmed")

	different := negknow.Canonicalize("", "", "pgbouncer 1.19 ignores it")
	require.NotEqual(t, b.ReasonHash, different.ReasonHash)
}

// TestKey_Length asserts both keys are always a full 32-byte sha256 digest. The bloom derives its
// bit indices from that width, so a short key is a silently mis-keyed filter rather than an error.
func TestKey_Length(t *testing.T) {
	for _, d := range []negknow.Descriptor{
		{},
		fixedDescriptor,
		negknow.Canonicalize("src/a.ts#Foo.bar", "disable connection pooling", "it deadlocks"),
	} {
		require.Len(t, d.Key(), 32)
		require.Len(t, d.MatchKey(), 32)
		require.Len(t, d.MatchHex(), 64)
	}
}

// TestKey_NoAliasing proves neither key hands the caller a slice into shared state: mutating a
// returned key must not change what the next call returns. A ledger that inserts into a bloom and
// then reuses the key buffer would otherwise corrupt entries at a distance.
func TestKey_NoAliasing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		d := negknow.Canonicalize(
			rapid.StringMatching(`[a-z/.]{0,24}`).Draw(rt, "target"),
			rapid.StringMatching(`[a-z ]{0,32}`).Draw(rt, "approach"),
			rapid.StringMatching(`[a-z ]{0,32}`).Draw(rt, "reason"),
		)

		key := d.Key()
		want := append([]byte(nil), key...)
		for i := range key {
			key[i] ^= 0xFF
		}
		require.Equal(rt, want, d.Key(), "Key must not alias state a caller can mutate")

		match := d.MatchKey()
		wantMatch := append([]byte(nil), match...)
		for i := range match {
			match[i] ^= 0xFF
		}
		require.Equal(rt, wantMatch, d.MatchKey(), "MatchKey must not alias state a caller can mutate")
	})
}

// TestDescriptorJSON_Standalone pins Descriptor's wire form on its own, not only as a field of a
// Record: the four keys in their frozen order, symbol always a string (never null), and the
// reason hash as "sha256:<64 hex>". Round-tripping must be exact.
func TestDescriptorJSON_Standalone(t *testing.T) {
	const wantJSON = `{"normalized_path":"src/auth.ts","symbol":"refreshToken",` +
		`"approach_class":"widen-pool-timeout",` +
		`"reason_hash":"sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"}`

	d := negknow.Descriptor{
		NormalizedPath: "src/auth.ts",
		Symbol:         "refreshToken",
		ApproachClass:  "widen-pool-timeout",
		ReasonHash:     mustParseHash("sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"),
	}

	got, err := json.Marshal(d)
	require.NoError(t, err)
	require.Equal(t, wantJSON, string(got), "the four keys and their order are frozen")

	var back negknow.Descriptor
	require.NoError(t, json.Unmarshal(got, &back))
	require.Equal(t, d, back)
}

// TestDescriptorJSON_SymbolAndReasonHashLeniency pins the three input spellings of "no symbol" -
// null, absent, and "" - onto the same empty string (§8.3's symbol_or_null is the empty string in
// Go and in JSON), and pins that an unparseable reason_hash decodes to the zero Hash rather than
// failing the whole record. A single malformed hash in one line of an append-only log must not
// make the surrounding record unreadable.
func TestDescriptorJSON_SymbolAndReasonHashLeniency(t *testing.T) {
	zeroHex := strings.Repeat("0", 64)
	for _, in := range []string{
		`{"normalized_path":"a","symbol":null,"approach_class":"c","reason_hash":"sha256:` + zeroHex + `"}`,
		`{"normalized_path":"a","approach_class":"c","reason_hash":"sha256:` + zeroHex + `"}`,
		`{"normalized_path":"a","symbol":"","approach_class":"c","reason_hash":"sha256:` + zeroHex + `"}`,
	} {
		var d negknow.Descriptor
		require.NoError(t, json.Unmarshal([]byte(in), &d), "input %s", in)
		require.Equal(t, "a", d.NormalizedPath)
		require.Empty(t, d.Symbol)
		require.Equal(t, "c", d.ApproachClass)
		require.True(t, d.ReasonHash.IsZero())
	}

	var bad negknow.Descriptor
	require.NoError(t, json.Unmarshal(
		[]byte(`{"normalized_path":"a","symbol":"s","approach_class":"c","reason_hash":"not-a-hash"}`), &bad))
	require.True(t, bad.ReasonHash.IsZero(), "an unparseable reason_hash decodes to the zero Hash, not an error")

	out, err := json.Marshal(negknow.Descriptor{})
	require.NoError(t, err)
	require.Contains(t, string(out), `"symbol":""`, "symbol is always emitted as a string, never null")
}

// TestCanonicalizeAt_NormalizesAbsolutePaths covers the constructor a caller holding an absolute
// path uses: Norm against the project root first, then the ordinary canonicalization. The
// resulting descriptor must be identical to the one built from the project-relative spelling,
// because otherwise the same file would occupy two bloom keys depending on how it was named.
func TestCanonicalizeAt_NormalizesAbsolutePaths(t *testing.T) {
	root := testutil.NewProject(t).Root

	abs, err := negknow.CanonicalizeAt(root, root+"/src/auth.ts:refreshToken", "widen pool timeout", "it stalls")
	require.NoError(t, err)
	require.Equal(t, "src/auth.ts", abs.NormalizedPath)
	require.Equal(t, "refreshToken", abs.Symbol)

	rel, err := negknow.CanonicalizeAt(root, "src/auth.ts:refreshToken", "widen pool timeout", "it stalls")
	require.NoError(t, err)
	require.Equal(t, abs, rel)
	require.Equal(t, negknow.Canonicalize("src/auth.ts:refreshToken", "widen pool timeout", "it stalls"), rel)

	empty, err := negknow.CanonicalizeAt(root, "", "widen pool timeout", "it stalls")
	require.NoError(t, err)
	require.Empty(t, empty.NormalizedPath)

	_, err = negknow.CanonicalizeAt(root, "../outside.ts", "widen pool timeout", "it stalls")
	require.ErrorIs(t, err, core.ErrNotFound)
}
