package modwire_test

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// allowedBytes lists every byte a character class body admits, by asking the regexp engine.
func allowedBytes(t *testing.T, class string) []byte {
	t.Helper()
	re := regexp.MustCompile("^[" + class + "]$")
	var out []byte
	for b := range 256 {
		if re.MatchString(string([]byte{byte(b)})) {
			out = append(out, byte(b))
		}
	}
	require.NotEmpty(t, out)
	return out
}

// TestFactIDChars_NeverGrowUnderJSON is why the alphabet is what it is: every byte a fact id may
// hold encodes as itself, with encoding/json's default HTML escaping on (so < > & are out) and in
// the mod's JSON.parse, so a fact id's encoded size is its length.
func TestFactIDChars_NeverGrowUnderJSON(t *testing.T) {
	for _, b := range allowedBytes(t, modwire.FactIDChars) {
		require.Less(t, b, byte(0x80), "only ASCII: a byte that is not valid UTF-8 alone would decode as U+FFFD")
		enc, err := json.Marshal(string([]byte{b}))
		require.NoError(t, err)
		require.Len(t, enc, 3, "byte %q must encode as itself", b)
	}
}

// TestFactIDs_TheRepositorysOwnIDsAreValid lists the id shapes the stored facts carry today, so a
// brief can name them unchanged: decision ids, pinned-invariant ids, negative-knowledge record ids,
// content hashes and host tool-use ids.
func TestFactIDs_TheRepositorysOwnIDsAreValid(t *testing.T) {
	for _, id := range []string{
		"dec_0123456789ab",
		"inv_0123456789ab",
		"elim_0123456789ab",
		"sha256:" + strings.Repeat("0f", 32),
		"toolu_01ABCdef234",
		"intent:original",
		"path/to/file.go",
	} {
		r := modwire.BriefResponse{Status: modwire.Served(), FactIDs: []string{id}}
		require.NoError(t, r.Validate(modwire.BriefMaxChars), id)
	}
}

func TestFactIDs_OutsideTheAlphabetAreInvalid(t *testing.T) {
	for _, id := range []string{"a<b", "a>b", "a&b", "a b", `a"b`, `a\b`, "é", "\xff", "a\nb", "a+b", "a,b"} {
		r := modwire.BriefResponse{Status: modwire.Served(), FactIDs: []string{id}}
		require.ErrorIs(t, r.Validate(modwire.BriefMaxChars), modwire.ErrInvalid, "%q", id)
	}
}

// TestTriggers_AcceptAnyWellFormedName: a trigger the host adds later is served and recorded, as
// long as it is a plain identifier; only a malformed one is refused.
func TestTriggers_AcceptAnyWellFormedName(t *testing.T) {
	in := `{"v":1,"session_id":"s","cwd":"/p","trigger":"autoCompact_2-x","max_chars":0}`
	req, err := modwire.DecodeBriefRequest(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, "autoCompact_2-x", req.Trigger)

	line, err := modwire.BriefRecord{V: modwire.Version, TS: 1, Trigger: "autoCompact", Outcome: modwire.OutcomeServed, Chars: 3}.Line()
	require.NoError(t, err)
	require.Contains(t, string(line), `"trigger":"autoCompact"`)

	for _, bad := range []string{"Auto Mode", "a.b", "a/b", "é", strings.Repeat("t", modwire.MaxTriggerBytes+1)} {
		_, err := modwire.DecodeBriefRequest(strings.NewReader(
			`{"v":1,"session_id":"s","cwd":"/p","trigger":"` + bad + `"}`))
		require.ErrorIs(t, err, modwire.ErrInvalid, "%q", bad)
	}
	for _, b := range allowedBytes(t, modwire.TriggerChars) {
		require.Less(t, b, byte(0x80))
	}
}

// TestSessionIDChars_MatchValidSessionID ties the published alphabet to the validator.
func TestSessionIDChars_MatchValidSessionID(t *testing.T) {
	allowed := map[byte]bool{}
	for _, b := range allowedBytes(t, modwire.SessionIDChars) {
		allowed[b] = true
	}
	for b := range 256 {
		require.Equal(t, allowed[byte(b)], modwire.ValidSessionID("a"+string([]byte{byte(b)})), "byte %d", b)
	}
}

// TestDetail_IsBounded: the one free-text field a response carries is bounded too, and the error
// object FailureFor builds stays inside the bound however long the error is.
func TestDetail_IsBounded(t *testing.T) {
	long := modwire.Failure(modwire.ReasonError, strings.Repeat("d", modwire.MaxDetailBytes+1))
	require.ErrorIs(t, modwire.BriefResponse{Status: long}.Validate(modwire.BriefMaxChars), modwire.ErrInvalid)
	require.ErrorIs(t, modwire.HelloResponse{Status: long}.Validate(), modwire.ErrInvalid)

	st := modwire.FailureFor(errors.New(strings.Repeat("é", modwire.MaxDetailBytes)))
	require.LessOrEqual(t, len(st.Detail), modwire.MaxDetailBytes)
	require.True(t, utf8.ValidString(st.Detail), "truncation never splits a character")
	require.NoError(t, modwire.HelloResponse{Status: st}.Validate())
}

// TestFailureFor_DetailIsValidUTF8: an error text holding bytes that are not UTF-8 reaches the
// detail as valid UTF-8. encoding/json writes each invalid byte as U+FFFD, three bytes, so a detail
// that kept them would fit MaxDetailBytes in Go and decode on the mod's side to a longer string,
// which the mod's own byte check (TextEncoder over the decoded text) then refuses.
func TestFailureFor_DetailIsValidUTF8(t *testing.T) {
	for name, text := range map[string]string{
		"all invalid":    strings.Repeat("\xff", modwire.MaxDetailBytes),
		"mixed":          strings.Repeat("ok\xfe", modwire.MaxDetailBytes),
		"truncated rune": "detail " + strings.Repeat("é", 10) + "\xc3",
	} {
		t.Run(name, func(t *testing.T) {
			st := modwire.FailureFor(errors.New(text))
			require.True(t, utf8.ValidString(st.Detail), "the detail is valid UTF-8")
			require.LessOrEqual(t, len(st.Detail), modwire.MaxDetailBytes)

			// What the mod decodes from the wire is byte for byte what Go measured.
			b, err := modwire.Encode(st)
			require.NoError(t, err)
			var got struct {
				Detail string `json:"detail"`
			}
			require.NoError(t, json.Unmarshal(b, &got))
			require.Equal(t, st.Detail, got.Detail)
			require.LessOrEqual(t, len(got.Detail), modwire.MaxDetailBytes)
		})
	}
}
