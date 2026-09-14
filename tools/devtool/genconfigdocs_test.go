package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// docs/config-reference.md is the page a user edits their config against, and §11.4 requires it to
// name every key this build honours. The CI `docs` job runs the --check half; these rows make the
// --check half able to fail, and pin the two properties a byte-identical regeneration cannot:
// that the page's key inventory IS config.Defaults()'s, and that the metadata sections are
// rendered from internal/config rather than transcribed.
//
// The package-level `root` is shared by every devtool test and these never run in parallel, which
// is the house pattern gencommanddocs_test.go and genmcpdocs_test.go already follow.

// configDocsSandbox points `root` at a scratch tree that holds page (when non-nil) at
// configDocPath, restoring the previous root afterwards. It returns the sandbox root.
func configDocsSandbox(t *testing.T, page []byte) string {
	t.Helper()

	dir := t.TempDir()
	if page != nil {
		p := filepath.Join(dir, filepath.FromSlash(configDocPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, page, 0o644)) // #nosec G306 -- test fixture
	}

	prev := root
	root = dir
	t.Cleanup(func() { root = prev })
	return dir
}

// sectionKeyRe matches the first cell of a key row: | `dotted.key` | …
var sectionKeyRe = regexp.MustCompile("^\\| `([^`]+)` \\|")

// keyTableHeadingRe matches a per-section heading, "## `scheduler`". The metadata sections
// appended after the tables have prose headings, so restricting to the code-span spelling keeps
// their keys (a build gate has no config leaf at all) out of the inventory comparison.
var keyTableHeadingRe = regexp.MustCompile("^## `([^`]+)`$")

// renderedConfigPage renders the page once for a test to read.
func renderedConfigPage(t *testing.T) string {
	t.Helper()
	b, err := renderConfigDoc()
	require.NoError(t, err, "renderConfigDoc")
	require.NotEmpty(t, b)
	return string(b)
}

// documentedLeaves returns every key the per-section tables document.
func documentedLeaves(t *testing.T, page string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	inKeyTable := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "## ") {
			inKeyTable = keyTableHeadingRe.MatchString(line)
			continue
		}
		if !inKeyTable {
			continue
		}
		if m := sectionKeyRe.FindStringSubmatch(line); m != nil {
			require.False(t, out[m[1]], "key %q is documented twice", m[1])
			out[m[1]] = true
		}
	}
	return out
}

// schemaLeaves flattens config.Defaults().JSONSchema() into dotted path -> that leaf's schema node.
func schemaLeaves(t *testing.T) map[string]any {
	t.Helper()
	var schema map[string]any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &schema))
	out := map[string]any{}
	var walk func(node map[string]any, prefix string)
	walk = func(node map[string]any, prefix string) {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}
		for k, v := range props {
			child, ok := v.(map[string]any)
			require.True(t, ok, "schema node %s.%s", prefix, k)
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if _, isObj := child["properties"]; isObj {
				walk(child, path)
				continue
			}
			out[path] = child
		}
	}
	walk(schema, "")
	return out
}

// tableRows returns the rows of the first markdown table under the given heading, each row split
// into trimmed cells, with the header and separator rows dropped.
func tableRows(t *testing.T, page, heading string) [][]string {
	t.Helper()
	i := strings.Index(page, "\n"+heading+"\n")
	require.GreaterOrEqual(t, i, 0, "the page must have a %q section", heading)
	var rows [][]string
	started := false
	for _, line := range strings.Split(page[i+1:], "\n") {
		if !strings.HasPrefix(line, "|") {
			if started {
				break
			}
			continue
		}
		started = true
		if strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		rows = append(rows, cells)
	}
	require.NotEmpty(t, rows, "the %q table must have rows", heading)
	return rows[1:] // drop the header row
}

// TestGenConfigDocs_LeavesMatchDefaultsOneToOne is the §11.4 assertion: the page documents every
// key config.Defaults() honours, and no key it does not. Both directions are reported by name, so
// a failure says which key was added without regenerating rather than "counts differ".
func TestGenConfigDocs_LeavesMatchDefaultsOneToOne(t *testing.T) {
	documented := documentedLeaves(t, renderedConfigPage(t))
	require.NotEmpty(t, documented, "the page must document some keys, or this row proves nothing")

	schema := schemaLeaves(t)
	var missing, extra []string
	for leaf := range schema {
		if !documented[leaf] {
			missing = append(missing, leaf)
		}
	}
	for leaf := range documented {
		if _, ok := schema[leaf]; !ok {
			extra = append(extra, leaf)
		}
	}
	require.Empty(t, missing, "keys in config.Defaults() that the page does not document")
	require.Empty(t, extra, "keys the page documents that config.Defaults() does not have")
}

// TestGenConfigDocs_GatedSwitchesRenderFromSource pins the metadata sections against
// internal/config. A page that carried a transcribed list would drift the moment a gate passed or
// a block's version moved; every row here is checked against the accessor it came from.
func TestGenConfigDocs_GatedSwitchesRenderFromSource(t *testing.T) {
	page := renderedConfigPage(t)
	schema := schemaLeaves(t)

	t.Run("gated switches", func(t *testing.T) {
		rows := map[string][]string{}
		for _, r := range tableRows(t, page, "## Gated switches (ship off)") {
			rows[strings.Trim(r[0], "`")] = r
		}
		for _, g := range config.MigrationGates() {
			row, ok := rows[g.Key]
			require.True(t, ok, "the gated-switch table must list %q", g.Key)
			require.Equal(t, "`false`", row[1], "a gated switch ships off, so its default is false")

			leaf, isLeaf := schema[g.Key].(map[string]any)
			require.True(t, isLeaf, "gate %q must name a schema leaf", g.Key)
			require.Equal(t, false, leaf["default"], "the schema default of %q must be false", g.Key)

			require.Equal(t, escapePipes(g.Owner), row[2])
			require.Equal(t, escapePipes(g.Gate), row[3])
			want := "pending"
			if g.Passed {
				want = "passed"
			}
			require.Equal(t, want, row[4], "status of %q", g.Key)
		}
		require.Len(t, rows, len(config.MigrationGates()), "no row the gate table does not have")
	})

	t.Run("build gates", func(t *testing.T) {
		rows := map[string][]string{}
		for _, r := range tableRows(t, page, "### Build gates (no config key)") {
			rows[strings.Trim(r[0], "`")] = r
		}
		for _, g := range config.MigrationBuildGates() {
			row, ok := rows[g.Key]
			require.True(t, ok, "the build-gate table must list %q", g.Key)
			require.Equal(t, escapePipes(g.Owner), row[1])
			require.Equal(t, escapePipes(g.Gate), row[2])
			_, isLeaf := schema[g.Key]
			require.False(t, isLeaf, "a build gate must have no config leaf: %q", g.Key)
		}
		require.Len(t, rows, len(config.MigrationBuildGates()))
	})

	t.Run("retired-meaning keys", func(t *testing.T) {
		rows := map[string][]string{}
		for _, r := range tableRows(t, page, "## Retired-meaning keys") {
			rows[strings.Trim(r[0], "`")] = r
		}
		for _, r := range config.RetiredMeaningKeys() {
			row, ok := rows[r.Key]
			require.True(t, ok, "the retired-meaning table must list %q", r.Key)
			require.Equal(t, escapePipes(r.Note), row[1])
			_, isLeaf := schema[r.Key]
			require.True(t, isLeaf, "retired key %q must still be a documented leaf", r.Key)
		}
		require.Len(t, rows, len(config.RetiredMeaningKeys()))
	})

	t.Run("versioned blocks", func(t *testing.T) {
		rows := map[string][]string{}
		for _, r := range tableRows(t, page, "## Versioned blocks") {
			rows[strings.Trim(r[0], "`")] = r
		}
		for _, s := range config.VersionedSections() {
			row, ok := rows[s.Path]
			require.True(t, ok, "the versioned-block table must list %q", s.Path)
			require.Equal(t, strconv.Itoa(s.Version), strings.Trim(row[1], "`"),
				"the version rendered for %q", s.Path)
		}
		require.Len(t, rows, len(config.VersionedSections()))
	})

	t.Run("provenance origins", func(t *testing.T) {
		rows := tableRows(t, page, "## Provenance origins")
		for i, r := range rows {
			require.Equal(t, config.Origin(i).String(), strings.Trim(r[0], "`"), //nolint:gosec // i is a table index
				"origins must be listed in ascending order from OriginDefault")
			require.NotEmpty(t, r[1], "every origin needs a meaning")
		}
		require.Equal(t, "unknown", config.Origin(len(rows)).String(), //nolint:gosec // the enum's own bound
			"the table must stop exactly where the Origin enum does")
	})
}

// TestGenConfigDocs_CommittedPageIsCurrent is the --check half, run here so a stale page fails the
// ordinary test run and not only the CI docs job.
func TestGenConfigDocs_CommittedPageIsCurrent(t *testing.T) {
	prev := root
	t.Cleanup(func() { root = prev })
	root = repoRootForTest(t)

	want, err := renderConfigDoc()
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(configDocPath)))
	require.NoError(t, err,
		"%s must be committed; run `go run ./tools/devtool gen-config-docs`", configDocPath)
	require.Equal(t, string(want), string(normalizeNewlines(got)),
		"%s is stale; run `go run ./tools/devtool gen-config-docs`", configDocPath)
}

// TestGenConfigDocs_CheckDetectsMissingAndStale drives the task's own --check against a sandbox
// root, which is what makes the CI job's failure mode real rather than assumed. Missing and stale
// are separate outcomes because they ask the reader for different things.
func TestGenConfigDocs_CheckDetectsMissingAndStale(t *testing.T) {
	want, err := renderConfigDoc()
	require.NoError(t, err)

	dir := configDocsSandbox(t, nil)
	p := filepath.Join(dir, filepath.FromSlash(configDocPath))

	err = taskGenConfigDocs([]string{"--check"})
	require.Error(t, err, "a page that is not there at all must be reported")
	require.Contains(t, err.Error(), "is missing")
	require.Contains(t, err.Error(), configDocPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, want, 0o644)) // #nosec G306 -- test fixture
	require.NoError(t, taskGenConfigDocs([]string{"--check"}),
		"a byte-identical page must not be reported as drift")

	require.NoError(t, os.WriteFile(p, append(append([]byte(nil), want...), '.'), 0o644)) // #nosec G306 -- test fixture
	err = taskGenConfigDocs([]string{"--check"})
	require.Error(t, err, "a page that differs from config.Defaults() must be reported")
	require.Contains(t, err.Error(), "is stale")
	require.Contains(t, err.Error(), configDocPath, "the error must name the file to regenerate")
}
