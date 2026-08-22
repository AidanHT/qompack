package config_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
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
		require.Equal(t, config.ConnectDeadlineMsPortable, config.Defaults().Runtime.Daemon.ConnectDeadlineMs,
			"-update must run on a host that ships the portable defaults: the golden holds one "+
				"number per leaf and connectDeadlineMs's is platform-specific (see schemaGoldenWant)")
		require.NoError(t, os.WriteFile(schemaGoldenPath, got, 0o644))
	}

	golden, err := os.ReadFile(schemaGoldenPath)
	require.NoError(t, err)
	require.Equal(t, string(schemaGoldenWant(t, golden)), string(got))

	// Re-running JSONSchema must be byte-identical: nothing in its reflection walk depends on
	// map iteration order.
	require.Equal(t, got, config.Defaults().JSONSchema())

	var schema map[string]any
	require.NoError(t, json.Unmarshal(got, &schema))
	require.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
	assertLeavesHaveMetadata(t, schema, "")
}

// schemaGoldenWant returns the schema document the running platform must produce, given the
// checked-in golden.
//
// testdata/golden/config/schema.json is one file and a JSON Schema "default" is one number, but
// runtime.daemon.connectDeadlineMs's default is platform-specific: config derives the Windows
// value from the named-pipe dial's ERROR_PIPE_BUSY retry quantum and keeps the portable value
// everywhere else (internal/config/deadlines.go). The golden is checked in as rendered on a
// portable-default host — which is also where CI regenerates it — so on Windows the expectation
// is that same document with exactly that one leaf rewritten.
//
// This is not a normalisation and it does not soften the comparison. Every other byte is still
// compared byte for byte; the leaf is located by the whole "connectDeadlineMs" + "default"
// snippet, not by a bare number (5 occurs many times in this file); and the snippet must appear
// exactly once, so a golden that stopped carrying the portable value, or grew a second copy,
// fails here rather than quietly matching.
func schemaGoldenWant(t *testing.T, golden []byte) []byte {
	t.Helper()

	from := schemaConnectDeadlineSnippet(config.ConnectDeadlineMsPortable)
	require.Equal(t, 1, bytes.Count(golden, from),
		"the checked-in schema golden must carry runtime.daemon.connectDeadlineMs's portable "+
			"default exactly once, spelled %q", from)

	have := config.Defaults().Runtime.Daemon.ConnectDeadlineMs
	if have == config.ConnectDeadlineMsPortable {
		return golden
	}
	return bytes.Replace(golden, from, schemaConnectDeadlineSnippet(have), 1)
}

// schemaConnectDeadlineSnippet is the exact bytes JSONSchema() emits for
// runtime.daemon.connectDeadlineMs's "default" line, at that leaf's own indentation. The
// surrounding key is part of the pattern so the match can only be that one leaf.
func schemaConnectDeadlineSnippet(ms int) []byte {
	return fmt.Appendf(nil, "\"connectDeadlineMs\": {\n              \"default\": %d,\n", ms)
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
