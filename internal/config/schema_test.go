package config_test

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// update reports whether -update was passed: `go test ./internal/config/... -run
// TestJSONSchema_Golden -update`. It is package-scoped so every golden test in this package
// shares one flag.
//
// It adopts an existing registration rather than calling flag.Bool outright, mirroring
// testutil.registerUpdateFlag. Only one flag of a given name may exist per test binary, and
// internal/testutil registers -update for the same purpose; if any test file in this package ever
// imports it, whichever init ran second would panic with "flag redefined". Two symmetric
// adopt-or-register helpers cannot collide in either order.
var update = registerUpdateFlag()

// registerUpdateFlag returns a reader for -update, registering the flag only if nothing else has.
// It returns a func because an already-registered flag exposes its value through flag.Value.
func registerUpdateFlag() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool("update", false, "update golden files")
	return func() bool { return *p }
}

const schemaGoldenPath = "../../testdata/golden/config/schema.json"

func TestJSONSchema_Golden(t *testing.T) {
	got := config.Defaults().JSONSchema()

	if update() {
		require.NoError(t, os.WriteFile(schemaGoldenPath, got, 0o644))
	}

	want, err := os.ReadFile(schemaGoldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))

	// Re-running JSONSchema must be byte-identical: nothing in its reflection walk depends on
	// map iteration order.
	require.Equal(t, got, config.Defaults().JSONSchema())

	var schema map[string]any
	require.NoError(t, json.Unmarshal(got, &schema))
	require.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
	assertLeavesHaveMetadata(t, schema, "")
}

// assertLeavesHaveMetadata walks the JSON Schema tree (not the Go type) and, for every node that
// is not itself an object ("properties" absent), asserts it carries a non-empty default,
// description and x-qompack-section — exactly the DoD in the subplan's test table.
func assertLeavesHaveMetadata(t *testing.T, node map[string]any, path string) {
	t.Helper()
	if props, ok := node["properties"].(map[string]any); ok {
		require.NotEmpty(t, props, "object node %q has no properties", path)
		for k, v := range props {
			child := k
			if path != "" {
				child = path + "." + k
			}
			childNode, ok := v.(map[string]any)
			require.True(t, ok, "schema node for %s is not an object", child)
			assertLeavesHaveMetadata(t, childNode, child)
		}
		return
	}
	require.Contains(t, node, "default", "leaf %s missing default", path)
	require.Contains(t, node, "description", "leaf %s missing description", path)
	require.NotEmpty(t, node["description"], "leaf %s has empty description", path)
	require.Contains(t, node, "x-qompack-section", "leaf %s missing x-qompack-section", path)
	require.NotEmpty(t, node["x-qompack-section"], "leaf %s has empty x-qompack-section", path)
}

// TestJSONSchema_EnumsPresentWhereExpected spot-checks that a handful of known enum leaves carry
// an "enum" array with the exact allowed values, and that a non-enum leaf does not.
func TestJSONSchema_EnumsPresentWhereExpected(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &schema))

	node := schemaNodeAt(t, schema, "store.compression")
	require.Equal(t, []any{"zstd", "none"}, node["enum"])
	require.Equal(t, "string", node["type"])

	node = schemaNodeAt(t, schema, "scheduler.cache.readMultiplier")
	require.NotContains(t, node, "enum")
	require.Equal(t, "number", node["type"])
	require.Equal(t, "(0,1]", node["x-qompack-range"])

	node = schemaNodeAt(t, schema, "scheduler.youngDaly.measuredDeltaSeconds")
	require.Equal(t, []any{"number", "null"}, node["type"])

	node = schemaNodeAt(t, schema, "store.canonicalize.strip")
	require.Equal(t, "array", node["type"])
	items, ok := node["items"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", items["type"])
}

func schemaNodeAt(t *testing.T, schema map[string]any, dotted string) map[string]any {
	t.Helper()
	cur := schema
	var last map[string]any
	segs := splitDotted(dotted)
	for i, s := range segs {
		props, ok := cur["properties"].(map[string]any)
		require.True(t, ok, "no properties at %q", dotted)
		next, ok := props[s].(map[string]any)
		require.True(t, ok, "no schema node for %q", dotted)
		if i == len(segs)-1 {
			last = next
			break
		}
		cur = next
	}
	return last
}

func splitDotted(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
