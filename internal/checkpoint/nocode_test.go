package checkpoint_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/stretchr/testify/require"
)

// codeLineRe matches string content that reads like source code: a line opening with a keyword,
// an if-condition, or a lone closing brace. It is deliberately a heuristic over decoded string
// VALUES — a checkpoint carries reasons and summaries, and none of these shapes belong in one.
var codeLineRe = regexp.MustCompile(`(?m)^\s*(func |class |def |import |package |const |return |if \(|\}\s*$)`)

// TestGoldenCheckpointsContainNoCodeBlocks is G3.4 / §13 invariant 5 asserted over EVERY golden
// checkpoint: a checkpoint carries pointers with one-line reasons, never code (§4.4). Three
// layers of the same rule:
//
//   - the raw bytes may not contain a fenced code block (nor its ` escape),
//   - no decoded string value anywhere in the document may read like a line of source code,
//   - pointers.files[].why and pointers.tools[].summary must be one line — no newline —
//     because a multi-line "reason" is where an excerpt would smuggle itself in.
//
// The test globs testdata/golden/checkpoints/, so it keeps working — and keeps biting — as other
// seats add 0002-full.json, 0003-truncated.json and 0004-tier1-over-budget.json. A fixture that
// legitimized a code snippet would legitimize the generator emitting one, so the fixtures are
// held to the rule the generator is.
func TestGoldenCheckpointsContainNoCodeBlocks(t *testing.T) {
	for name, raw := range readCheckpointsGoldens(t) {
		t.Run(name, func(t *testing.T) {
			require.NotContains(t, string(raw), "```",
				"§13 invariant 5: no fenced code block in a checkpoint")

			var doc any
			require.NoError(t, json.Unmarshal(raw, &doc))
			walkJSONStrings("$", doc, func(path, s string) {
				require.NotContains(t, s, "```",
					"%s: fenced code block hidden behind JSON escaping", path)
				require.NotRegexp(t, codeLineRe, s,
					"%s: string value reads like source code", path)
			})

			var c checkpoint.Checkpoint
			require.NoError(t, json.Unmarshal(raw, &c))
			for i, f := range c.Pointers.Files {
				require.NotContains(t, f.Why, "\n",
					"pointers.files[%d].why must be one line — a reason, never an excerpt", i)
			}
			for i, tp := range c.Pointers.Tools {
				require.NotContains(t, tp.Summary, "\n",
					"pointers.tools[%d].summary must be one line — a summary, never the result", i)
			}
		})
	}
}

// TestCodeLineHeuristicFires is the positive control for the guard above: the heuristic must
// actually match the code shapes it names and must not match the prose a checkpoint legitimately
// carries. Without this, a typo in codeLineRe could quietly turn the no-code guard into one that
// matches nothing and passes everything.
func TestCodeLineHeuristicFires(t *testing.T) {
	for _, code := range []string{
		"func refreshToken(ctx context.Context) error {",
		"class TokenRotator:",
		"def refresh_token(self):",
		"import { rotate } from './auth'",
		"package checkpoint",
		"const maxRetries = 3",
		"return nil",
		"if (pool.exhausted) {",
		"  }",
		"prose first line\n\treturn conn.Release()",
	} {
		require.Regexp(t, codeLineRe, code, "the heuristic must catch %q", code)
	}
	for _, prose := range []string{
		"",
		"refreshToken lives here; the lock-across-IO pattern is at the top of the function",
		"load test: 200 concurrent refreshes, 37 failures, all pool acquisition timeouts",
		"the import path of the module changed last week",
		"constant churn in the pool settings; returns to baseline after the fix",
	} {
		require.NotRegexp(t, codeLineRe, prose, "the heuristic must leave prose alone: %q", prose)
	}
}

// walkJSONStrings visits every string value in a decoded JSON document, depth-first, handing the
// visitor the value and its path for a failure message that names where the offense sits.
func walkJSONStrings(path string, v any, visit func(path, s string)) {
	switch x := v.(type) {
	case string:
		visit(path, x)
	case map[string]any:
		for k, vv := range x {
			walkJSONStrings(path+"."+k, vv, visit)
		}
	case []any:
		for i, vv := range x {
			walkJSONStrings(fmt.Sprintf("%s[%d]", path, i), vv, visit)
		}
	}
}
