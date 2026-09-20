package store

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteCanonJSON_GivesEveryJSONShapeExactlyOneSpelling.
//
// This is the encoder behind ArgsDigest, and the digest is an IDENTITY: two tool calls with the
// same arguments have to hash the same whatever order the decoder happened to yield their keys in,
// and two calls with different arguments must not collide. That makes every branch here
// load-bearing — an object whose keys are not sorted, a number re-rendered through float64, or a
// value type that fell through to nothing would each change a digest without changing a tool call.
//
// The last case is the reason the default branch exists: canonicalization has to be TOTAL. A value
// this encoder has no case for is spelled defensively as a string rather than dropped, because a
// dropped value makes two different argument sets share one digest.
func TestWriteCanonJSON_GivesEveryJSONShapeExactlyOneSpelling(t *testing.T) {
	t.Parallel()

	canon := func(v any) string {
		var buf bytes.Buffer
		writeCanonJSON(&buf, v)
		return buf.String()
	}
	num := func(s string) json.Number { return json.Number(s) }

	for _, c := range []struct {
		name string
		in   any
		want string
	}{
		{"an empty object", map[string]any{}, `{}`},
		{"object keys are sorted", map[string]any{"b": num("2"), "a": num("1")}, `{"a":1,"b":2}`},
		{"an empty array", []any{}, `[]`},
		{"an array keeps its order", []any{num("2"), num("1")}, `[2,1]`},
		{"a large integer keeps every digit", num("9007199254740993"), `9007199254740993`},
		{"true", true, `true`},
		{"false", false, `false`},
		{"null", nil, `null`},
		{"a string is JSON-quoted", "a/b", `"a/b"`},
		{"HTML is not escaped", "<a>&</a>", `"<a>&</a>"`},
		{"a control character is escaped", "a\nb", `"a\nb"`},
		{"a value with no case of its own is still spelled", 42, `"42"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, canon(c.in))
		})
	}

	require.Equal(t,
		canon(map[string]any{"path": "src/a.go", "limit": num("10")}),
		canon(map[string]any{"limit": num("10"), "path": "src/a.go"}),
		"the same arguments must canonicalize identically whatever order they arrived in")

	nested := map[string]any{"a": []any{map[string]any{"z": nil, "y": true}}}
	require.Equal(t, `{"a":[{"y":true,"z":null}]}`, canon(nested),
		"nesting sorts at every level, not just the top one")
}
