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
		for _, leaf := range schemaPlatformLeaves(config.Defaults()) {
			require.Equal(t, leaf.portable, leaf.have,
				"-update must run on a host that ships the portable defaults: the golden holds one "+
					"number per leaf, and three are platform-specific — connectDeadlineMs, "+
					"l0IngestMs and ackDeadlineMs (see schemaGoldenWant). This host ships %s = %d, "+
					"not the portable %d", leaf.key, leaf.have, leaf.portable)
		}
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

// schemaPlatformLeaf is one leaf of the JSON Schema whose "default" differs by platform: its key,
// the value a portable-default host renders, and the value cfg actually holds.
type schemaPlatformLeaf struct {
	key      string
	portable int
	have     int
}

// schemaPlatformLeaves is the complete table of those leaves. A leaf belongs here exactly when
// internal/config/deadlines.go selects its default from runtime.GOOS:
//
//   - runtime.daemon.connectDeadlineMs — the Windows value is derived from the named-pipe dial's
//     ERROR_PIPE_BUSY retry quantum;
//   - runtime.budgets.l0IngestMs and runtime.daemon.ackDeadlineMs — SP20-D1's measured B-B
//     re-budget, three platforms each (Windows measured; linux and darwin provisional);
//   - runtime.hotPath.budgetMs — D41's B-A default, max(15, that platform's l0IngestMs).
//
// Adding a fifth platform-specific leaf to config without adding it here shows up immediately:
// the golden would then carry that leaf's portable value while the running platform emits its own,
// and TestJSONSchema_Golden's byte comparison fails on it.
func schemaPlatformLeaves(cfg config.Config) []schemaPlatformLeaf {
	return []schemaPlatformLeaf{
		{"connectDeadlineMs", config.ConnectDeadlineMsPortable, cfg.Runtime.Daemon.ConnectDeadlineMs},
		{"l0IngestMs", config.L0IngestMsPortable, cfg.Runtime.Budgets.L0IngestMs},
		{"ackDeadlineMs", config.AckDeadlineMsPortable, cfg.Runtime.Daemon.AckDeadlineMs},
		{"budgetMs", config.HotPathBudgetMsFor(config.L0IngestMsPortable), cfg.Runtime.HotPath.BudgetMs},
	}
}

// schemaGoldenWant returns the schema document the running platform must produce, given the
// checked-in golden.
//
// testdata/golden/config/schema.json is one file and a JSON Schema "default" is one number, but
// four leaves have platform-specific defaults (schemaPlatformLeaves). The golden is checked in as
// rendered on a portable-default host — which is also where CI regenerates it — so on Windows or
// macOS the expectation is that same document with exactly those leaves rewritten.
//
// This is not a normalisation and it does not soften the comparison. Every other byte is still
// compared byte for byte; each leaf is located by its own whole "<key>" + "default" snippet, never
// by a bare number (5, 15 and 17 all occur many times in this file); and each snippet must appear
// exactly once, so a golden that stopped carrying a portable value, or grew a second copy, fails
// here rather than quietly matching.
func schemaGoldenWant(t *testing.T, golden []byte) []byte {
	t.Helper()

	want := golden
	for _, leaf := range schemaPlatformLeaves(config.Defaults()) {
		indent := schemaLeafDefaultIndent(t, golden, leaf.key)
		from := schemaLeafDefaultSnippet(leaf.key, indent, leaf.portable)
		require.Equal(t, 1, bytes.Count(golden, from),
			"the checked-in schema golden must carry runtime's %s portable default exactly once, "+
				"spelled %q", leaf.key, from)
		if leaf.have == leaf.portable {
			continue
		}
		want = bytes.Replace(want, from, schemaLeafDefaultSnippet(leaf.key, indent, leaf.have), 1)
	}
	return want
}

// schemaLeafDefaultSnippet is the exact bytes JSONSchema() emits for key's "default" line, at that
// leaf's own indentation. The surrounding key is part of the pattern so the match can only be that
// one leaf.
func schemaLeafDefaultSnippet(key, indent string, ms int) []byte {
	return fmt.Appendf(nil, "%q: {\n%s\"default\": %d,\n", key, indent, ms)
}

// schemaLeafDefaultIndent reads key's "default" indentation out of the golden itself rather than
// hard-coding a run of spaces per leaf. The leaves do not have to sit at the same depth, and
// a hand-copied indentation that stopped matching would make schemaGoldenWant's count assertion
// fail for a reason that has nothing to do with the default it is guarding. Reading it here means
// the count assertion can only ever be reporting the thing it is about: whether the golden carries
// that leaf's portable VALUE, exactly once.
func schemaLeafDefaultIndent(t *testing.T, golden []byte, key string) string {
	t.Helper()

	open := fmt.Appendf(nil, "%q: {\n", key)
	require.Equal(t, 1, bytes.Count(golden, open),
		"the schema golden must contain exactly one %q object, spelled %q", key, open)

	rest := golden[bytes.Index(golden, open)+len(open):]
	n := 0
	for n < len(rest) && rest[n] == ' ' {
		n++
	}
	require.True(t, bytes.HasPrefix(rest[n:], []byte(`"default": `)),
		"%q's first member must be its \"default\" line (JSONSchema() emits leaf keys sorted)", key)
	return string(rest[:n])
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

// TestJSONSchema_SchedulerCacheRegimeLeaves spot-checks the two §11.5 cache-regime leaves SP-12
// adds, independently of the golden: they are the first leaves nested two objects deep inside
// runtime, so a wrong json tag anywhere on the path would move them rather than drop them.
func TestJSONSchema_SchedulerCacheRegimeLeaves(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &schema))

	node := schemaNodeAt(t, schema, "runtime.scheduler.cache.expiringTriggerFraction")
	require.Equal(t, "number", node["type"])
	require.Equal(t, 0.8, node["default"])
	require.Equal(t, "(0,1)", node["x-qompack-range"])
	require.NotEmpty(t, node["description"])
	require.NotEmpty(t, node["x-qompack-section"])

	node = schemaNodeAt(t, schema, "runtime.scheduler.cache.assumeMaxTTLSeconds")
	require.Equal(t, "integer", node["type"])
	require.Equal(t, float64(3600), node["default"])
	require.Equal(t, "[scheduler.cache.ttlSeconds,∞)", node["x-qompack-range"])
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
