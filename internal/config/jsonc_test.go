package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestStripJSONC exercises line comments, block comments, a "//" that appears inside a JSON
// string (which must survive untouched), and trailing commas in both objects and arrays.
func TestStripJSONC(t *testing.T) {
	in := []byte("{\n" +
		"  // a leading line comment\n" +
		"  \"a\": 1, // trailing line comment\n" +
		"  \"b\": /* inline block */ 2,\n" +
		"  /* a\n" +
		"     multi-line\n" +
		"     block comment */\n" +
		"  \"c\": \"text with // not a comment and /* not a comment either\",\n" +
		"  \"d\": [1, 2, 3,],\n" +
		"  \"e\": {\"f\": 4,},\n" +
		"}\n")

	out := config.StripJSONC(in)
	require.Equal(t, len(in), len(out), "StripJSONC must preserve byte offsets (same length)")

	// Every newline must stay at exactly the same byte index: this is what keeps
	// Provenance.Location line numbers correct in the presence of comments above the line being
	// reported.
	for i, c := range in {
		if c == '\n' {
			require.Equal(t, byte('\n'), out[i], "newline at byte %d must be preserved", i)
		}
	}

	var v map[string]any
	require.NoError(t, json.Unmarshal(out, &v), "stripped output must be strict JSON")

	require.Equal(t, float64(1), v["a"])
	require.Equal(t, float64(2), v["b"])
	require.Equal(t, "text with // not a comment and /* not a comment either", v["c"],
		"a // or /* inside a string must never be treated as a comment start")
	require.Equal(t, []any{float64(1), float64(2), float64(3)}, v["d"], "trailing comma in an array must be removed")

	e, ok := v["e"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(4), e["f"], "trailing comma in an object must be removed")
}

// TestStripJSONC_NoComments confirms a comment-free, comma-clean document round-trips
// byte-for-byte (StripJSONC must not perturb ordinary JSON).
func TestStripJSONC_NoComments(t *testing.T) {
	in := []byte(`{"a":1,"b":[1,2,3]}`)
	out := config.StripJSONC(in)
	require.Equal(t, in, out)
}

// TestStripJSONC_CRLF confirms both newline conventions are preserved untouched inside and
// outside comments, since golden files and JSONC config files may be checked out with either
// line ending on Windows.
func TestStripJSONC_CRLF(t *testing.T) {
	in := []byte("{\r\n  // comment\r\n  \"a\": 1\r\n}")
	out := config.StripJSONC(in)
	require.Equal(t, len(in), len(out))

	var v map[string]any
	require.NoError(t, json.Unmarshal(out, &v))
	require.Equal(t, float64(1), v["a"])
}
