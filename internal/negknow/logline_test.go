package negknow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// These tests hold logline.go's fast path to the one promise that makes it safe: for every line,
// it either produces EXACTLY the logLine json.Unmarshal produces, or it declines and leaves the
// caller's logLine untouched — so that the replay's original decode path runs on the same zero
// value it always did.
//
// The comparison is reflect.DeepEqual, which is deliberately stricter than the codec's own
// round-trip tests: it tells a nil slice from an empty one, so the fast path cannot, say, hand
// back a nil stale_because where encoding/json would have handed back [].

// fastLineVerdict runs the fast path over line and checks it against json.Unmarshal. It reports
// whether the fast path accepted the line, and a non-empty problem when the promise was broken.
func fastLineVerdict(line []byte, in *wireInterner) (accepted bool, problem string) {
	sentinel := logLine{Op: "untouched", Because: []string{"sentinel"}}
	got := sentinel
	if !decodeRecordLineFast(line, &got, in) {
		if !reflect.DeepEqual(got, sentinel) {
			return false, fmt.Sprintf("declined %q but modified the logLine: %#v", line, got)
		}
		return false, ""
	}
	var want logLine
	if err := json.Unmarshal(line, &want); err != nil {
		return true, fmt.Sprintf("accepted %q, which json.Unmarshal rejects: %v", line, err)
	}
	if !reflect.DeepEqual(got, want) {
		return true, fmt.Sprintf("decoded %q differently:\n fast %#v\n json %#v", line, got, want)
	}
	return true, ""
}

// canonicalLines is every writer-produced record line these tests have to hand: the contract
// goldens' record lines, the §11.2 Open fixture's lines, and appendLine's output for records that
// exercise the shapes the fixtures do not — escapes, non-ASCII text, staleness fields, negative and
// zero integers, empty and multi-entry arrays.
func canonicalLines(t *testing.T) [][]byte {
	t.Helper()
	var lines [][]byte
	addFile := func(b []byte) {
		for _, line := range bytes.Split(b, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var probe logProbe
			if json.Unmarshal(line, &probe) == nil && probe.Op == "" && json.Valid(line) {
				lines = append(lines, line)
			}
		}
	}
	addFile(readContractFile(t, "eliminations.golden.jsonl"))
	addFile(readContractFile(t, "want/elimination_record.jsonl"))
	addFile(benchLogBytes(t, 64))

	h := core.HashBytes("negknow.test.logline", []byte("x"))
	for _, r := range []Record{
		{ID: "elim_escapes0001", Target: `src/"quoted"\path.ts:fn`, Approach: "tab\there", Reason: "line\nbreak / slash \r\f\b"},
		{ID: "elim_unicode0001", Target: "src/café/日本.ts", Approach: "élargir le délai", Reason: "ünïcödé ✓"},
		{ID: "elim_stale000001", Status: StatusStale, StaleSince: 1754985600000, StaleBecause: []string{"a changed", `b "moved"`}},
		{ID: "elim_negative001", TS: -17, StaleSince: -1, Status: StatusStale, StaleBecause: []string{""}},
		{ID: "elim_zero0000001", Evidence: h, DependsOn: []Dep{{Path: "a.lock", Hash: h}, {Path: "b.lock", Hash: h}}},
		{ID: "", Session: "s", Scope: ScopeProject, Source: SourceUserStatement},
	} {
		var buf bytes.Buffer
		require.NoError(t, appendLine(&buf, r))
		lines = append(lines, bytes.TrimSpace(buf.Bytes()))
	}
	require.NotEmpty(t, lines)
	return lines
}

// TestDecodeRecordLineFast_CanonicalLinesAccepted pins that the fast path actually ENGAGES on what
// this package writes — a fast path that silently declined every line would pass every
// equivalence check and leave Open exactly as slow as before — and that each accepted line
// decodes exactly as json.Unmarshal decodes it. One interner is shared across all of them, as the
// replay shares one across a log.
func TestDecodeRecordLineFast_CanonicalLinesAccepted(t *testing.T) {
	var in wireInterner
	for _, line := range canonicalLines(t) {
		accepted, problem := fastLineVerdict(line, &in)
		require.Empty(t, problem)
		require.True(t, accepted, "the fast path must accept the writer's own line %s", line)
	}
}

// TestDecodeRecordLineFast_Boundary walks the edge of the accepted grammar line by line. Each case
// states whether the fast path takes it, and every case — taken or not — is also held to the
// equivalence promise.
func TestDecodeRecordLineFast_Boundary(t *testing.T) {
	const (
		head = `{"id":"elim_edge00000001","session":"s","ts":1,"target":"t","approach":"a","reason":"r",` +
			`"descriptor":{"normalized_path":"p","symbol":"","approach_class":"c","reason_hash":"h"},` +
			`"evidence":"e","depends_on":[],"scope":"session","status":"active"`
		tail = `,"source":0}`
	)
	cases := []struct {
		name   string
		line   string
		accept bool
	}{
		{"minimal canonical line", head + tail, true},
		{"empty stale_because decodes to an empty, non-nil slice", head + `,"stale_because":[]` + tail, true},
		{"stale_since without stale_because", head + `,"stale_since":5` + tail, true},
		{"zero stale_since", head + `,"stale_since":0` + tail, true},
		{"minus zero", head + `,"stale_since":-0` + tail, true},
		{"largest source", head + `,"source":255}`, true},
		{"eighteen-digit integer", head + `,"stale_since":999999999999999999` + tail, true},
		{"every single-character escape", `{"id":"\"\\\/\b\f\n\r\t"` + head[len(`{"id":"elim_edge00000001"`):] + tail, true},
		{"DEL is an ordinary byte", `{"id":"x` + "\x7f" + `"` + head[len(`{"id":"elim_edge00000001"`):] + tail, true},

		{"source past uint8", head + `,"source":256}`, false},
		{"nineteen-digit integer is left to encoding/json", head + `,"stale_since":1000000000000000000` + tail, false},
		{"integer overflowing int64", head + `,"stale_since":99999999999999999999` + tail, false},
		{"leading zero", head + `,"source":01}`, false},
		{"fraction", head + `,"stale_since":1.5` + tail, false},
		{"exponent", head + `,"stale_since":1e3` + tail, false},
		{"negative source", head + `,"source":-1}`, false},
		// The escapes are assembled from a lone backslash so that the source spells the JSON
		// escape itself, never the character it stands for.
		{"unicode escape", `{"id":"` + `\` + `u0041"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"surrogate escape", `{"id":"` + `\` + `ud83d` + `\` + `ude00"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"invalid surrogate escape", `{"id":"\ud83d"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"unknown escape", `{"id":"\q"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"raw control byte", `{"id":"x` + "\x01" + `"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"invalid UTF-8", `{"id":"x` + "\xff" + `"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"truncated UTF-8 sequence", `{"id":"x` + "\xc3" + `"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"null string", `{"id":null` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"null depends_on", `{"id":"x","session":"s","ts":1,"target":"t","approach":"a","reason":"r",` +
			`"descriptor":{"normalized_path":"p","symbol":"","approach_class":"c","reason_hash":"h"},` +
			`"evidence":"e","depends_on":null,"scope":"session","status":"active","source":0}`, false},
		{"null stale_because", head + `,"stale_because":null` + tail, false},
		{"whitespace between tokens", `{"id": "x"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"uppercase key json would still match", `{"ID":"x"` + head[len(`{"id":"elim_edge00000001"`):] + tail, false},
		{"keys out of order", `{"session":"s","id":"x"` + head[len(`{"id":"elim_edge00000001","session":"s"`):] + tail, false},
		{"missing key", `{"id":"x","ts":1` + head[len(`{"id":"elim_edge00000001","session":"s","ts":1`):] + tail, false},
		{"duplicate key", head + `,"status":"stale"` + tail, false},
		{"extra key", head + `,"extra":1` + tail, false},
		{"op key: a control line", `{"op":"stale","id":"x","ts":1,"because":["y"]}`, false},
		{"trailing garbage", head + tail + `x`, false},
		{"second value", head + tail + `{}`, false},
		{"truncated", head, false},
		{"dep with extra key", `{"id":"x","session":"s","ts":1,"target":"t","approach":"a","reason":"r",` +
			`"descriptor":{"normalized_path":"p","symbol":"","approach_class":"c","reason_hash":"h"},` +
			`"evidence":"e","depends_on":[{"path":"p","hash":"h","x":1}],"scope":"session","status":"active","source":0}`, false},
		{"trailing comma in array", head + `,"stale_because":["a",]` + tail, false},
		{"not an object", `[]`, false},
	}
	var in wireInterner
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accepted, problem := fastLineVerdict([]byte(tc.line), &in)
			require.Empty(t, problem)
			require.Equal(t, tc.accept, accepted, "fast path acceptance of %s", tc.line)
		})
	}
}

// TestDecodeRecordLineFast_RecordGenProperty holds the promise over arbitrary records as the
// writer renders them: every string drawn from all of Unicode (so control characters, U+2028 and
// invalid-looking text all reach the encoder), every hash a real digest.
func TestDecodeRecordLineFast_RecordGenProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rec := recordGen().Draw(rt, "record")
		var buf bytes.Buffer
		if err := appendLine(&buf, rec); err != nil {
			rt.Fatalf("appendLine: %v", err)
		}
		var in wireInterner
		if _, problem := fastLineVerdict(bytes.TrimSpace(buf.Bytes()), &in); problem != "" {
			rt.Fatal(problem)
		}
	})
}

// mutationBytes is the alphabet the mutation property edits lines with: every byte that is
// structural in JSON or at the edge of the fast path's grammar — quotes, escapes, separators,
// number characters, a control byte, bytes that begin or continue a UTF-8 sequence, and letters
// that spell null and \u.
var mutationBytes = []byte{
	'"', '\\', 'u', 'n', 'l', ',', ':', '{', '}', '[', ']', ' ', '\t', '0', '1', '9', '-', '+', '.', 'e', 'E',
	'/', 'b', 't', 0x00, 0x1f, 0x7f, 0x80, 0xbf, 0xc3, 0xe2, 0xff, 'I', 'D',
}

// TestDecodeRecordLineFast_MutationProperty is the fuzzing half of the argument, written as a
// rapid property so it runs in every `go test` (a new Fuzz target would also need a row in the
// nightly matrix test/guards pins). It takes a canonical line and applies up to four random byte
// edits — delete, insert or overwrite, drawn from mutationBytes — so the inputs sit exactly on the
// boundary between lines the fast path takes and lines it must decline, and holds the promise on
// each.
func TestDecodeRecordLineFast_MutationProperty(t *testing.T) {
	base := canonicalLines(t)
	rapid.Check(t, func(rt *rapid.T) {
		line := append([]byte(nil), rapid.SampledFrom(base).Draw(rt, "line")...)
		for range rapid.IntRange(1, 4).Draw(rt, "edits") {
			if len(line) == 0 {
				break
			}
			at := rapid.IntRange(0, len(line)-1).Draw(rt, "at")
			b := rapid.SampledFrom(mutationBytes).Draw(rt, "byte")
			switch rapid.IntRange(0, 2).Draw(rt, "op") {
			case 0:
				line = append(line[:at], line[at+1:]...)
			case 1:
				line = append(line[:at], append([]byte{b}, line[at:]...)...)
			default:
				line[at] = b
			}
		}
		var in wireInterner
		if _, problem := fastLineVerdict(line, &in); problem != "" {
			rt.Fatal(problem)
		}
	})
}

// TestWireInterner_Bounded pins the interner's bound: past maxInternedWireStrings distinct values
// it stops remembering, and still returns the right string every time.
func TestWireInterner_Bounded(t *testing.T) {
	var in wireInterner
	for i := range 2 * maxInternedWireStrings {
		s := fmt.Sprintf("value-%d", i)
		require.Equal(t, s, in.intern([]byte(s)))
	}
	require.Len(t, in.m, maxInternedWireStrings)
	require.Equal(t, "value-0", in.intern([]byte("value-0")))
}
