package negknow

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// contractsDir is testdata/golden/contracts/negknow/, relative to this package's own directory.
// Everything under its want/ subdirectory is frozen (Rule W-2): these tests read it and prove the
// codec reproduces it, and never write to it.
const contractsDir = "../../testdata/golden/contracts/negknow"

func mustHash(t *testing.T, s string) core.Hash {
	t.Helper()
	h, err := core.ParseHash(s)
	require.NoError(t, err)
	return h
}

func readContractFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractsDir, rel))
	require.NoError(t, err, "contract fixture missing: %s", rel)
	require.NotEmpty(t, b)
	return b
}

// frozenRecord is the Record of want/elimination_record.jsonl, built from literals transcribed out
// of that fixture — never from Canonicalize, whose classifier is free to move and whose output
// would therefore silently re-key a frozen golden. Desc.ApproachClass is the fixture's hand-chosen
// "widen-timeout" for the same reason.
func frozenRecord(t *testing.T) Record {
	t.Helper()
	return Record{
		ID:       "elim_3f9b2c7d1a48",
		Session:  core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"),
		TS:       core.UnixMilli(1767225480000),
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect",
		Desc: Descriptor{
			NormalizedPath: "src/auth.ts",
			Symbol:         "refreshToken",
			ApproachClass:  "widen-timeout",
			ReasonHash:     mustHash(t, "sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"),
		},
		Evidence: mustHash(t, "sha256:4d7a0c3f6b9e2158a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3"),
		DependsOn: []Dep{{
			Path: "docker-compose.yml",
			Hash: mustHash(t, "sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d"),
		}},
		Scope:  ScopeProject,
		Status: StatusActive,
		Source: SourceSlashCommand,
	}
}

// eightFiveEntry is Qompack.md §8.5's eliminated[] entry, transcribed from the design document and
// filled in with the frozen fixture's own values for the places §8.5 prints an ellipsis. It is
// deliberately a second, independent copy of the shape: it catches drift against the design
// document even if the fixture and the marshaller drifted together.
const eightFiveEntry = `{"target":"src/auth.ts:refreshToken",` +
	`"approach":"widen pool timeout",` +
	`"reason":"pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect",` +
	`"evidence":"sha256:4d7a0c3f6b9e2158a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3",` +
	`"depends_on":[{"path":"docker-compose.yml","hash":"sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d"}],` +
	`"scope":"project",` +
	`"status":"active"}`

// eightFiveKeys is the seven keys §8.5 shows, in the order it shows them. The wire form is a
// superset (id, session, ts, descriptor and source are additive), so the assertion is that these
// seven appear in this relative order, not that they are the only keys.
var eightFiveKeys = []string{"target", "approach", "reason", "evidence", "depends_on", "scope", "status"}

// topLevelKeyOrder returns b's top-level object keys, in the order the encoder emitted them.
func topLevelKeyOrder(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(b)))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)

	var keys []string
	for dec.More() {
		k, err := dec.Token()
		require.NoError(t, err)
		key, ok := k.(string)
		require.True(t, ok, "object key must be a string, got %T", k)
		keys = append(keys, key)
		var discard json.RawMessage
		require.NoError(t, dec.Decode(&discard))
	}
	return keys
}

// TestRecordJSON_Golden is the byte-level contract: MarshalJSON must reproduce the frozen
// want/elimination_record.jsonl line exactly. The fixture is never regenerated to make this pass
// (V3-VERIFY §1 rule 3) — if the two disagree, the marshaller is wrong.
func TestRecordJSON_Golden(t *testing.T) {
	golden := readContractFile(t, "want/elimination_record.jsonl")
	want := strings.TrimSuffix(string(golden), "\n")

	got, err := frozenRecord(t).MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, want, string(got))
	require.Contains(t, string(got), `"source":1`, "source is the integer SourceKind on the wire")

	// The §8.5 projection: the seven keys the design document prints, with its values and in its
	// relative order.
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got, &all))
	projected := map[string]json.RawMessage{}
	for _, k := range eightFiveKeys {
		v, ok := all[k]
		require.True(t, ok, "§8.5 key %q missing from the wire form", k)
		projected[k] = v
	}
	projectedJSON, err := json.Marshal(projected)
	require.NoError(t, err)
	require.JSONEq(t, eightFiveEntry, string(projectedJSON))

	var order []string
	for _, k := range topLevelKeyOrder(t, got) {
		for _, want85 := range eightFiveKeys {
			if k == want85 {
				order = append(order, k)
				break
			}
		}
	}
	require.Equal(t, eightFiveKeys, order, "the §8.5 keys must keep §8.5's relative order")
}

// recordGen generates arbitrary Records for the round-trip property. Every string is valid UTF-8
// and every hash is a real 32-byte digest, because those are the only inputs the wire form claims
// to carry losslessly.
func recordGen() *rapid.Generator[Record] {
	return rapid.Custom(func(rt *rapid.T) Record {
		hash := func(label string) core.Hash {
			var h core.Hash
			copy(h[:], rapid.SliceOfN(rapid.Byte(), len(h), len(h)).Draw(rt, label))
			return h
		}
		deps := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) Dep {
			return Dep{Path: rapid.String().Draw(rt, "dep_path"), Hash: hash("dep_hash")}
		}), 0, 4).Draw(rt, "depends_on")

		return Record{
			ID:       rapid.String().Draw(rt, "id"),
			Session:  core.SessionID(rapid.String().Draw(rt, "session")),
			TS:       core.UnixMilli(rapid.Int64Range(0, 1<<62).Draw(rt, "ts")),
			Target:   rapid.String().Draw(rt, "target"),
			Approach: rapid.String().Draw(rt, "approach"),
			Reason:   rapid.String().Draw(rt, "reason"),
			Desc: Descriptor{
				NormalizedPath: rapid.String().Draw(rt, "normalized_path"),
				Symbol:         rapid.String().Draw(rt, "symbol"),
				ApproachClass:  rapid.String().Draw(rt, "approach_class"),
				ReasonHash:     hash("reason_hash"),
			},
			Evidence:     hash("evidence"),
			DependsOn:    deps,
			Scope:        rapid.SampledFrom([]Scope{ScopeSession, ScopeProject}).Draw(rt, "scope"),
			Status:       rapid.SampledFrom([]Status{StatusActive, StatusStale}).Draw(rt, "status"),
			StaleSince:   core.UnixMilli(rapid.Int64Range(0, 1<<62).Draw(rt, "stale_since")),
			StaleBecause: rapid.SliceOfN(rapid.String(), 0, 3).Draw(rt, "stale_because"),
			Source: rapid.SampledFrom([]SourceKind{
				SourceMCP, SourceSlashCommand, SourceHeuristic, SourceUserStatement,
			}).Draw(rt, "source"),
		}
	})
}

// TestRecordJSON_RoundTrip is the property half of the codec contract: whatever the ledger holds in
// memory survives a trip through the wire form unchanged. nil and empty depends_on/stale_because
// are equivalent on the wire, so the comparison uses cmpopts.EquateEmpty.
func TestRecordJSON_RoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rec := recordGen().Draw(rt, "record")

		b, err := json.Marshal(rec)
		if err != nil {
			rt.Fatalf("marshal: %v", err)
		}
		var got Record
		if err := json.Unmarshal(b, &got); err != nil {
			rt.Fatalf("unmarshal %s: %v", b, err)
		}
		if diff := cmp.Diff(rec, got, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("round trip changed the record (-want +got):\n%s", diff)
		}
	})
}

// TestRecordJSON_NilDependsOn pins the one thing the struct tags cannot express: a nil DependsOn
// renders as [], never null, so no consumer of a checkpoint has to handle a null there.
func TestRecordJSON_NilDependsOn(t *testing.T) {
	rec := Record{ID: "x", Scope: ScopeSession, Status: StatusActive}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	require.Contains(t, string(b), `"depends_on":[]`)
	require.NotContains(t, string(b), `"depends_on":null`)

	var got Record
	require.NoError(t, json.Unmarshal(b, &got))
	require.Empty(t, got.DependsOn)

	b2, err := json.Marshal(got)
	require.NoError(t, err)
	require.Equal(t, string(b), string(b2), "the empty-depends_on form must be a fixed point")
}

// TestRecordJSON_MissingFields pins the tolerant defaulting a hand-written or older-version line
// relies on: a missing scope, status or source is a default, not a decode failure.
func TestRecordJSON_MissingFields(t *testing.T) {
	var rec Record
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","target":"t","approach":"a","reason":"r"}`), &rec))

	require.Equal(t, "x", rec.ID)
	require.Equal(t, ScopeSession, rec.Scope)
	require.Equal(t, StatusActive, rec.Status)
	require.Equal(t, SourceMCP, rec.Source)
	require.True(t, rec.Evidence.IsZero(), "a missing evidence decodes to the zero hash, never an error")
	require.Empty(t, rec.DependsOn)
}

// TestRecordJSON_BadDepHash pins that one unparseable dep hash costs that dep, not the record: the
// dep is dropped, the decode still succeeds, and decodeRecord surfaces a warning naming the path.
func TestRecordJSON_BadDepHash(t *testing.T) {
	line := []byte(`{"id":"x","depends_on":[` +
		`{"path":"good.yml","hash":"sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d"},` +
		`{"path":"bad.yml","hash":"nothex"}]}`)

	var rec Record
	require.NoError(t, json.Unmarshal(line, &rec))
	require.Len(t, rec.DependsOn, 1)
	require.Equal(t, "good.yml", rec.DependsOn[0].Path)

	_, warnings, err := decodeRecord(line)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "bad.yml")
}

// TestRecordJSON_UnparseableEvidence pins that the codec never errors on a bad evidence hash: the
// requireEvidence decision belongs to the ledger, which sees the zero hash and decides. It also
// pins that the zeroing is not silent: decodeRecord surfaces a warning naming the bad hash, the
// same way a bad depends_on entry does.
func TestRecordJSON_UnparseableEvidence(t *testing.T) {
	var rec Record
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","evidence":"nothex"}`), &rec))
	require.True(t, rec.Evidence.IsZero())

	line := []byte(`{"id":"x","evidence":"nothex"}`)
	_, warnings, err := decodeRecord(line)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "nothex")
}

// TestSourceKind_RoundTrip pins the human-facing spellings and, with them, the frozen const order:
// SourceSlashCommand must be 1, which is what both frozen fixtures' "source":1 means.
func TestSourceKind_RoundTrip(t *testing.T) {
	for _, k := range []SourceKind{SourceMCP, SourceSlashCommand, SourceHeuristic, SourceUserStatement} {
		got, err := ParseSourceKind(k.String())
		require.NoError(t, err)
		require.Equal(t, k, got)
	}
	require.Equal(t, "mcp", SourceMCP.String())
	require.Equal(t, "slash", SourceSlashCommand.String())
	require.Equal(t, "heuristic", SourceHeuristic.String())
	require.Equal(t, "user", SourceUserStatement.String())
	require.Equal(t, "unknown", SourceKind(9).String())

	_, err := ParseSourceKind("nope")
	require.Error(t, err)
	_, err = ParseSourceKind("unknown")
	require.Error(t, err, `"unknown" is String's fallback, not a kind that parses back`)

	require.Equal(t, SourceKind(1), SourceSlashCommand)
}

// TestSourceKind_WireFormIsInteger pins that String() never reaches the wire: on disk and in a
// checkpoint, source is the integer the two frozen fixtures carry.
func TestSourceKind_WireFormIsInteger(t *testing.T) {
	for i, k := range []SourceKind{SourceMCP, SourceSlashCommand, SourceHeuristic, SourceUserStatement} {
		b, err := json.Marshal(Record{ID: "x", Source: k})
		require.NoError(t, err)
		require.Contains(t, string(b), `"source":`+strconv.Itoa(i))
		require.NotContains(t, string(b), `"source":"`)
	}
}

// TestNormalizeRecord_Bounds pins the §12.3 defence: no unbounded agent-supplied string reaches a
// JSONL line. Every truncation lands on a UTF-8 boundary and the result, ellipsis included, stays
// inside the limit.
func TestNormalizeRecord_Bounds(t *testing.T) {
	rec := Record{
		Target:   strings.Repeat("é", 4096), // 8 KiB of two-byte runes
		Approach: strings.Repeat("a", 4096), // 4 KiB of ASCII
		Reason:   strings.Repeat("héllo", 2048),
	}
	for i := range 40 {
		rec.DependsOn = append(rec.DependsOn, Dep{Path: "dep" + strconv.Itoa(i) + ".yml"})
		rec.StaleBecause = append(rec.StaleBecause, "because "+strconv.Itoa(i))
	}

	var warnings []string
	normalizeRecord(&rec, nil, func(w string) { warnings = append(warnings, w) })

	// The limits are asserted as the literals the subplan's bounds table states, not only as the
	// constants, so that a mis-derived constant cannot make its own test pass.
	require.LessOrEqual(t, len(rec.Target), 512)
	require.LessOrEqual(t, len(rec.Approach), 512)
	require.LessOrEqual(t, len(rec.Reason), 2048)
	require.Equal(t, 512, maxTextBytes)
	require.Equal(t, 2048, maxReasonBytes)
	require.Equal(t, 32, maxDeps)
	require.Equal(t, 32, maxStaleBecause)
	for _, s := range []string{rec.Target, rec.Approach, rec.Reason} {
		require.True(t, utf8.ValidString(s), "truncation must land on a UTF-8 boundary")
		require.True(t, strings.HasSuffix(s, "…"), "a truncated field is marked with an ellipsis")
	}
	require.Len(t, rec.DependsOn, maxDeps)
	require.Len(t, rec.StaleBecause, maxStaleBecause)
	require.NotEmpty(t, warnings, "dropping entries must be reported, never silent")

	// A field already inside its bound is left completely alone.
	short := Record{Target: "src/auth.ts", Approach: "widen pool timeout", Reason: "because"}
	normalizeRecord(&short, nil, nil)
	require.Equal(t, "src/auth.ts", short.Target)
	require.Equal(t, "widen pool timeout", short.Approach)
	require.Equal(t, "because", short.Reason)
}

// TestNormalizeRecord_Redact pins that redaction runs before the bounds check, so a secret can
// never survive by being past the truncation point, and that it covers all three text fields.
func TestNormalizeRecord_Redact(t *testing.T) {
	const secret = "sk-live"
	redact := func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), secret, "«redacted:apikey»"))
	}
	rec := Record{
		Target:   "src/auth.ts (" + secret + ")",
		Approach: "hardcode " + secret,
		Reason:   "the key " + secret + " is rejected by the gateway",
	}
	normalizeRecord(&rec, redact, nil)

	for _, s := range []string{rec.Target, rec.Approach, rec.Reason} {
		require.NotContains(t, s, secret)
		require.Contains(t, s, "«redacted:apikey»")
	}
}

// TestNormalizeRecord_DepsDedupedAndSorted pins the two properties that make an appended line
// byte-stable for a given set of dependencies: one entry per paths.Key, the first hash winning,
// and ascending path order.
func TestNormalizeRecord_DepsDedupedAndSorted(t *testing.T) {
	first := mustHash(t, "sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d")
	second := mustHash(t, "sha256:7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e")
	rec := Record{DependsOn: []Dep{
		{Path: "package-lock.json", Hash: first},
		{Path: "docker-compose.yml", Hash: first},
		{Path: "package-lock.json", Hash: second},
	}}
	normalizeRecord(&rec, nil, nil)

	require.Len(t, rec.DependsOn, 2)
	require.Equal(t, "docker-compose.yml", rec.DependsOn[0].Path)
	require.Equal(t, "package-lock.json", rec.DependsOn[1].Path)
	require.Equal(t, first, rec.DependsOn[1].Hash, "the first hash for a path wins")
}

// TestRecordID_Stable pins recordID's output shape and its exact value for a fixed input. The
// expected id was computed independently of this package (sha256 over the documented byte layout),
// so it is a real cross-check on the domain string and the field separator, not a self-portrait.
func TestRecordID_Stable(t *testing.T) {
	desc := Descriptor{
		NormalizedPath: "src/auth.ts",
		Symbol:         "refreshToken",
		ApproachClass:  "widen-timeout",
		ReasonHash:     mustHash(t, "sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"),
	}
	const wantID = "elim_c7ac933cd299"

	id := recordID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A", 1767225480000, desc)
	require.Equal(t, wantID, id)
	require.Equal(t, id, recordID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A", 1767225480000, desc), "recordID is a pure function")

	require.True(t, strings.HasPrefix(id, "elim_"), "the frozen fixture's id prefix is elim_, not elm_")
	suffix := strings.TrimPrefix(id, "elim_")
	require.Len(t, suffix, 12)
	require.Equal(t, strings.ToLower(suffix), suffix, "the digest is lowercase hex")
	_, err := hex.DecodeString(suffix)
	require.NoError(t, err)

	// Every component participates: change one and the id must change.
	require.NotEqual(t, id, recordID("sess_other", 1767225480000, desc))
	require.NotEqual(t, id, recordID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A", 1767225480001, desc))
	other := desc
	other.ApproachClass = "narrow-timeout"
	require.NotEqual(t, id, recordID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A", 1767225480000, other))
}
