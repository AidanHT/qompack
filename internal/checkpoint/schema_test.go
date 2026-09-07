package checkpoint_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// checkpointsGoldenDir is testdata/golden/checkpoints/, relative to this package's directory.
// Unlike the frozen contract fixtures under testdata/golden/contracts/, these goldens are the
// CANONICAL Marshal form — two-space indent, HTML escaping off, one trailing newline — so
// Unmarshal→Marshal must reproduce each file byte-for-byte.
const checkpointsGoldenDir = "../../testdata/golden/checkpoints"

// readCheckpointsGoldens returns every golden checkpoint file, name → raw bytes. It fails if the
// directory is empty: a round-trip suite with nothing to round-trip would pass vacuously.
func readCheckpointsGoldens(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(checkpointsGoldenDir)
	require.NoError(t, err, "golden checkpoints directory missing")
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(checkpointsGoldenDir, e.Name()))
		require.NoError(t, err)
		require.NotEmpty(t, b)
		out[e.Name()] = b
	}
	require.NotEmpty(t, out, "no golden checkpoints found; the suite would assert nothing")
	return out
}

// minimalGoldenCheckpoint is the value 0001-minimal.json was generated from, kept so
// TestMinimalGoldenIsReproducible can prove the file on disk is checkpoint.Marshal's own output
// and not a hand-typed approximation of it: seq 1, no parent, one invariant, user_intent.original
// set with an empty evolution, everything else empty (Marshal supplies version, the [] slices,
// blocked_on:null and the two-key sketch_refs).
func minimalGoldenCheckpoint() checkpoint.Checkpoint {
	return checkpoint.Checkpoint{
		Session: "sess_01JD2Q0M8RWZK4Y6T1B3N5C7E9",
		Seq:     1,
		Created: "2026-02-01T09:00:00.000Z",
		Invariants: []checkpoint.Invariant{{
			ID:     "inv_9a1c4f7b0e3d",
			Text:   "Do not hand-edit files under generated/; regenerate them from the schema.",
			Source: "user",
			Pinned: core.UnixMilli(1770000000000),
		}},
		UserIntent: checkpoint.UserIntent{
			Original: "Add pagination to the audit-log listing endpoint; the mobile client times out fetching the full history.",
		},
	}
}

// TestMinimalGoldenIsReproducible pins testdata/golden/checkpoints/0001-minimal.json as the
// byte-exact Marshal of minimalGoldenCheckpoint. It is the field-order golden's provenance
// proof, and it would fail against any drift in Marshal's canonical form — indent, escaping,
// trailing newline, ensureNonNil's [] policy, the sketch_refs default, or the struct's field
// order — as well as against anyone "fixing up" the fixture by hand.
func TestMinimalGoldenIsReproducible(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(checkpointsGoldenDir, "0001-minimal.json"))
	require.NoError(t, err)

	got, err := checkpoint.Marshal(minimalGoldenCheckpoint())
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"0001-minimal.json must be checkpoint.Marshal's own output for the documented value")
}

// TestSchemaFieldOrderIsImportanceOrder scans Marshal's output with json.Decoder.Token and
// requires the top-level key sequence to be exactly the §6.9 importance order §8.5 freezes.
// Struct field declaration order IS the serialization order, so this is the test that catches a
// reordered field, an added field, or a renamed json tag in types.go — every one of which is a
// schema amendment that must instead bump SchemaVersion.
//
// The checkpoint under test HAS a parent: Parent is `omitempty`, so only a parented checkpoint
// exercises the full 17-key sequence.
func TestSchemaFieldOrderIsImportanceOrder(t *testing.T) {
	c := minimalGoldenCheckpoint()
	c.Seq = 2
	c.Parent = "0001.json"
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)

	require.Equal(t, []string{
		"version", "session", "seq", "created", "parent", "encoded_segments",
		"invariants", "user_intent", "eliminated",
		"decisions", "open_questions", "current_work",
		"pointers", "narrative",
		"sketch_refs", "dropped", "cache",
	}, topLevelKeys(t, b))
}

// topLevelKeys returns the top-level object keys of doc in encounter order, using
// json.Decoder.Token so nested objects and arrays are skipped rather than mistaken for keys.
func topLevelKeys(t *testing.T, doc []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(doc))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok, "a checkpoint document is one JSON object")

	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		require.NoError(t, err)
		key, ok := keyTok.(string)
		require.True(t, ok, "object key must be a string token")
		keys = append(keys, key)
		skipJSONValue(t, dec)
	}
	return keys
}

// skipJSONValue consumes exactly one JSON value (scalar, object or array) from dec.
func skipJSONValue(t *testing.T, dec *json.Decoder) {
	t.Helper()
	tok, err := dec.Token()
	require.NoError(t, err)
	d, ok := tok.(json.Delim)
	if !ok {
		return // scalar
	}
	switch d {
	case '{':
		for dec.More() {
			_, err := dec.Token() // key
			require.NoError(t, err)
			skipJSONValue(t, dec)
		}
	case '[':
		for dec.More() {
			skipJSONValue(t, dec)
		}
	default:
		t.Fatalf("unexpected delimiter %v", d)
	}
	_, err = dec.Token() // closing delimiter
	require.NoError(t, err)
}

// TestGoldenCheckpointRoundTrip requires Unmarshal→Marshal to reproduce every golden checkpoint
// byte-for-byte. Byte identity — not JSONEq — is the assertion, because the manifest digest and
// Reader.Verify hash the exact bytes: a re-marshal that reordered a key, dropped a space, or
// turned [] into null would still be "equal JSON" and would still corrupt every digest. The test
// globs the directory, so the goldens other seats add (0002-full, 0003-truncated,
// 0004-tier1-over-budget) are covered the moment they land.
func TestGoldenCheckpointRoundTrip(t *testing.T) {
	for name, raw := range readCheckpointsGoldens(t) {
		t.Run(name, func(t *testing.T) {
			c, err := checkpoint.Unmarshal(raw)
			require.NoError(t, err)
			out, err := checkpoint.Marshal(c)
			require.NoError(t, err)
			require.Equal(t, string(raw), string(out),
				"Unmarshal→Marshal must be byte-identical to the golden")
		})
	}
}

// TestEmptySlicesSerializeAsArrays marshals checkpoints whose slice fields were never populated
// and requires every one of them to serialize as [] — never null — with no null anywhere in the
// document except the explicit "blocked_on": null. It would fail against a Marshal that skipped
// ensureNonNil (Go marshals a nil slice as null), and against a CurrentWork.BlockedOn tagged
// omitempty.
//
// The second case is the one the top-level walk cannot see. A nil slice NESTED inside an element
// of c.Decisions is still a nil slice at encode time, and two of ExtractDecisions' three sources
// mint a Decision without ever touching AlternativesRejected — so a real checkpoint reaches disk
// with "alternatives_rejected": null where §8.5 shows an array, and SP-11's rehydrator and the jq
// expressions in /qompack:status range over it. Every golden on disk populates the field on every
// decision, so no golden can catch this; only a decision left deliberately empty can.
func TestEmptySlicesSerializeAsArrays(t *testing.T) {
	cases := map[string]struct {
		in   checkpoint.Checkpoint
		want []string
	}{
		"zero checkpoint": {
			in: checkpoint.Checkpoint{},
			want: []string{
				`"encoded_segments": []`,
				`"invariants": []`,
				`"eliminated": []`,
				`"decisions": []`,
				`"open_questions": []`,
				`"evolution": []`,
				`"dropped": []`,
				`"files": []`,
				`"tools": []`,
			},
		},
		"a decision and an elimination that carry no nested collections": {
			in: checkpoint.Checkpoint{
				Decisions:  []checkpoint.Decision{{What: "w", Why: "y"}},
				Eliminated: []negknow.Record{{ID: "nk_1"}},
			},
			want: []string{
				`"alternatives_rejected": []`,
				`"depends_on": []`,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := checkpoint.Marshal(tc.in)
			require.NoError(t, err)
			doc := string(b)

			for _, want := range tc.want {
				require.Contains(t, doc, want)
			}

			require.Equal(t, 1, strings.Count(doc, "null"),
				"the only null in a checkpoint is the explicit blocked_on")
			require.Contains(t, doc, `"blocked_on": null`)
		})
	}
}

// TestMarshalLeavesTheCallersCheckpointAlone pins the copy-on-write that normalizing a nested
// slice needs. Marshal takes its Checkpoint by value, but a value copy shares the decision
// ELEMENTS through the slice's backing array, so filling in alternatives_rejected in place would
// reach back into the caller's own checkpoint. Marshal is called from inside Truncate's size
// measurement, and Truncate's contract is that a checkpoint which fits its budget comes back
// unchanged — a measurement that mutates what it measures would break that silently, in the one
// direction (nil to empty) that most comparisons forgive.
func TestMarshalLeavesTheCallersCheckpointAlone(t *testing.T) {
	c := checkpoint.Checkpoint{Decisions: []checkpoint.Decision{{What: "w", Why: "y"}}}

	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)
	require.Contains(t, string(b), `"alternatives_rejected": []`, "the artifact still carries an array")
	require.Nil(t, c.Decisions[0].AlternativesRejected,
		"the caller's decision must come back exactly as it was handed over")
}

// TestBlockedOnNullIsExplicit requires a populated CurrentWork with nothing blocking it to carry
// "blocked_on": null — present and null, never omitted. The field is a *string precisely so the
// artifact distinguishes "nothing blocks this work" (null) from "" — an omitempty tag or a plain
// string field would fail here.
func TestBlockedOnNullIsExplicit(t *testing.T) {
	c := checkpoint.Checkpoint{CurrentWork: checkpoint.CurrentWork{Goal: "x"}}
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)
	require.Contains(t, string(b), `"blocked_on": null`)

	blocked := "waiting on staging access"
	c.CurrentWork.BlockedOn = &blocked
	b, err = checkpoint.Marshal(c)
	require.NoError(t, err)
	require.Contains(t, string(b), `"blocked_on": "waiting on staging access"`)
	require.NotContains(t, string(b), `"blocked_on": null`)
}

// TestHashMarshalsAsSha256Prefix requires a core.Hash embedded in a checkpoint to render as
// "sha256:" plus 64 lowercase hex characters — its String() form, via the MarshalJSON SP-01
// shipped — and to survive the round trip. It would fail against the default [32]byte
// marshalling (a JSON array of numbers) or a bare-hex rendering without the domain prefix.
func TestHashMarshalsAsSha256Prefix(t *testing.T) {
	h := core.HashBytes(core.DomainDecision, []byte("evidence bytes"))
	c := checkpoint.Checkpoint{
		Decisions: []checkpoint.Decision{{ID: "dec_a3f2c9e14b70", What: "w", Why: "y", Evidence: h, Turn: 3}},
	}
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)

	require.Regexp(t, regexp.MustCompile(`"evidence": "sha256:[0-9a-f]{64}"`), string(b))
	require.Contains(t, string(b), `"evidence": "`+h.String()+`"`)

	back, err := checkpoint.Unmarshal(b)
	require.NoError(t, err)
	require.Equal(t, h, back.Decisions[0].Evidence, "the hash must round-trip value-identically")
}

// TestEliminatedCarriesEverySection85Key decodes eliminated[] as raw maps and requires every
// entry to carry all seven §8.5 keys with valid scope/status enums. Extra negknow.Record keys
// (id, session, ts, descriptor, …) are permitted: §5.14 documents the field as a superset, and
// this test is what stops the superset from ever quietly becoming a subset — it would fail
// against a negknow.Record json-tag rename (e.g. depends_on → deps) that the Go-typed fixture
// test would happily follow.
//
// The frozen contract fixture always participates (it has a populated eliminated[]); golden
// checkpoints with eliminated entries are swept in as other seats add them.
func TestEliminatedCarriesEverySection85Key(t *testing.T) {
	docs := map[string][]byte{
		"contracts/checkpoint/want/0001.json": readCheckpointGolden(t, "0001.json"),
	}
	for name, raw := range readCheckpointsGoldens(t) {
		docs["checkpoints/"+name] = raw
	}

	sawEntries := 0
	for name, raw := range docs {
		var doc struct {
			Eliminated []map[string]any `json:"eliminated"`
		}
		require.NoError(t, json.Unmarshal(raw, &doc), name)
		for i, entry := range doc.Eliminated {
			sawEntries++
			for _, key := range []string{"target", "approach", "reason", "evidence", "depends_on", "scope", "status"} {
				require.Contains(t, entry, key, "%s eliminated[%d] must carry the §8.5 key %q", name, i, key)
			}
			require.Contains(t, []any{"session", "project"}, entry["scope"], "%s eliminated[%d].scope", name, i)
			require.Contains(t, []any{"active", "stale"}, entry["status"], "%s eliminated[%d].status", name, i)
		}
	}
	require.Positive(t, sawEntries, "no eliminated entries anywhere would make this test vacuous")
}

// warnCapturingLogger records Warn lines so a test can assert a Warn was emitted and what it
// named. Every other level discards; With returns the receiver, so derived loggers feed the same
// record.
type warnCapturingLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *warnCapturingLogger) With(kv ...any) logging.Logger { return l }
func (l *warnCapturingLogger) Debug(string, ...any)          {}
func (l *warnCapturingLogger) Info(string, ...any)           {}
func (l *warnCapturingLogger) Error(string, ...any)          {}
func (l *warnCapturingLogger) Loud(string, ...any)           {}

func (l *warnCapturingLogger) Warn(msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintln(append([]any{msg}, kv...)...))
}

func (l *warnCapturingLogger) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.warns, "")
}

// TestUnmarshalDropsUnknownFields requires a version-valid document carrying a field this build
// does not know to decode without error — forward compatibility — while a Warn names the dropped
// field, and the decoded value to equal the un-tampered document's. It would fail against an
// Unmarshal that ran with DisallowUnknownFields on (hard error), and against one that dropped
// the field silently with no Warn.
//
// The test installs a capturing logger through SetObservers and restores the package defaults on
// cleanup, so later tests still see the never-set observer state.
func TestUnmarshalDropsUnknownFields(t *testing.T) {
	lg := &warnCapturingLogger{}
	checkpoint.SetObservers(lg, nil)
	t.Cleanup(func() { checkpoint.SetObservers(nil, nil) })

	golden := readCheckpointGolden(t, "0001.json")
	tampered := bytes.Replace(golden, []byte("{\n"), []byte("{\n  \"future_field\": 1,\n"), 1)
	require.NotEqual(t, string(golden), string(tampered), "the tamper must have landed")

	got, err := checkpoint.Unmarshal(tampered)
	require.NoError(t, err, "an unknown field on a version-valid document must not fail the read")

	want, err := checkpoint.Unmarshal(golden)
	require.NoError(t, err)
	require.Equal(t, want, got, "the unknown field must be dropped, everything else kept")

	require.Contains(t, lg.all(), "future_field", "the Warn must name the dropped field")
	require.Contains(t, lg.all(), "unknown field")
}

// TestUnmarshalPropagatesErrors covers Unmarshal's two failure paths: a Migrate rejection (here a
// future version) must surface as-is with core.ErrContract, and bytes Migrate accepts but the
// current struct cannot decode must fail wrapped as a checkpoint unmarshal error. It would fail
// against an Unmarshal that best-effort-decoded past a Migrate rejection — exactly the "interpret
// a newer plugin's document anyway" behaviour the version gate exists to prevent.
func TestUnmarshalPropagatesErrors(t *testing.T) {
	_, err := checkpoint.Unmarshal(fmt.Appendf(nil, `{"version":%d}`, checkpoint.SchemaVersion+1))
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "newer plugin")

	_, err = checkpoint.Unmarshal([]byte(`{"version":1,"seq":"not a number"}`))
	require.Error(t, err)
	require.ErrorContains(t, err, "checkpoint: unmarshal")
}

// TestCreatedNowUsesFrozenLayout pins Checkpoint.Created's layout: RFC 3339, UTC, millisecond
// precision, literal trailing Z. It would fail against local-time formatting (the non-UTC clock
// case), second-only or nanosecond precision, or a numeric timestamp — the
// paths.ManifestEntry.Created conflation §12 warns about.
func TestCreatedNowUsesFrozenLayout(t *testing.T) {
	clk := testutil.NewFakeClock(time.Date(2026, 3, 5, 17, 4, 5, 60_500_000, time.UTC))
	require.Equal(t, "2026-03-05T17:04:05.060Z", checkpoint.CreatedNow(clk),
		"millisecond precision truncates sub-millisecond digits")

	east := time.FixedZone("UTC+2", 2*60*60)
	clkEast := testutil.NewFakeClock(time.Date(2026, 3, 5, 19, 4, 5, 60_500_000, east))
	require.Equal(t, "2026-03-05T17:04:05.060Z", checkpoint.CreatedNow(clkEast),
		"a non-UTC clock instant must be rendered in UTC")
}
