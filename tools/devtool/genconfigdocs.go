package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
)

// configDocPath is the generated reference, relative to the repository root. §11.4 requires it to
// list every key, its type, its default, its valid range, and the Qompack.md section that
// motivates it — and the CI `docs` job fails if it drifts from config.Defaults().
//
// It is the one page under docs/ that SP-01 owns; SP-18 owns every other page and may link to
// this one but must never hand-edit it.
const configDocPath = "docs/config-reference.md"

// taskGenConfigDocs renders docs/config-reference.md from config.Defaults() and its JSON Schema
// metadata. With --check it diffs instead of writing, which is what the CI `docs` job runs.
func taskGenConfigDocs(args []string) error {
	fs := flag.NewFlagSet("gen-config-docs", flag.ContinueOnError)
	check := fs.Bool("check", false, "diff docs/config-reference.md against config.Defaults() instead of writing it")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}

	want, err := renderConfigDoc()
	if err != nil {
		return fmt.Errorf("gen-config-docs: %w", err)
	}

	p := filepath.Join(root, filepath.FromSlash(configDocPath))

	if *check {
		got, readErr := os.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("gen-config-docs --check: %s is missing; run `devtool gen-config-docs`", configDocPath)
		}
		if !bytes.Equal(want, normalizeNewlines(got)) {
			return fmt.Errorf("gen-config-docs --check: %s is stale; run `devtool gen-config-docs`", configDocPath)
		}
		fmt.Printf("gen-config-docs: %s is up to date\n", configDocPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, want, 0o644); err != nil { // #nosec G306 -- generated public documentation
		return err
	}
	fmt.Printf("gen-config-docs: wrote %s\n", configDocPath)
	return nil
}

// normalizeNewlines collapses CRLF so a Windows checkout does not read as drift.
func normalizeNewlines(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// schemaNode is the subset of the emitted JSON Schema this renderer reads.
type schemaNode struct {
	Type        schemaType            `json:"type"`
	Description string                `json:"description"`
	Default     any                   `json:"default"`
	Enum        []any                 `json:"enum"`
	Range       string                `json:"x-qompack-range"`
	Section     string                `json:"x-qompack-section"`
	Properties  map[string]schemaNode `json:"properties"`
}

// schemaType is a JSON Schema `type`, which draft 2020-12 allows to be either a string or an
// array of strings. Qompack emits the array form for exactly one leaf —
// scheduler.youngDaly.measuredDeltaSeconds, whose null is meaningful ("measure at runtime", not
// zero, §11.2) — so the renderer has to accept both.
type schemaType []string

// UnmarshalJSON accepts both the scalar and the array spelling.
func (t *schemaType) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = schemaType{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*t = many
	return nil
}

// String renders the type for a table cell: "number or null" reads better than `["number","null"]`.
func (t schemaType) String() string {
	switch len(t) {
	case 0:
		return ""
	case 1:
		return t[0]
	default:
		return strings.Join(t, " or ")
	}
}

// configRow is one rendered leaf.
type configRow struct{ Key, Type, Default, Range, Enum, Section, Description string }

// renderConfigDoc walks the schema depth-first and renders one markdown table per top-level
// section, so a reader looking for `scheduler.cache.readMultiplier` finds it under `scheduler`
// rather than in one 120-row table.
func renderConfigDoc() ([]byte, error) {
	var schema schemaNode
	if err := json.Unmarshal(config.Defaults().JSONSchema(), &schema); err != nil {
		return nil, fmt.Errorf("decoding JSONSchema(): %w", err)
	}

	var b strings.Builder
	b.WriteString("# Configuration reference\n\n")
	b.WriteString("**This file is generated. Do not edit it by hand.**\n")
	b.WriteString("Run `go run ./tools/devtool gen-config-docs` after changing `config.Defaults()`;\n")
	b.WriteString("CI fails if it drifts (§8 `docs` job, §11.4).\n\n")
	b.WriteString("Values are resolved from five layers, lowest precedence first:\n")
	b.WriteString("`config.Defaults()` → `~/.qompack/config.json` → `<project>/.qompack/config.json`\n")
	b.WriteString("→ `QOMPACK_*` environment → `--set <dotted.key>=<value>` (§11.2). ")
	b.WriteString("The merge is deep and per leaf, so a project file that sets one key inherits every other default.\n\n")
	b.WriteString("An invalid value is never fatal: the offending leaf falls back to its default, the violation is\n")
	b.WriteString("reported through the `Loud` channel and recorded in `.qompack/state/config-violations.json`,\n")
	b.WriteString("and loading continues (§11.3). Unknown keys produce a warning, never an error.\n\n")
	b.WriteString("Run `qompack config print --provenance` to see the effective value of every key and where it came from.\n\n")

	sections := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		sections = append(sections, k)
	}
	sort.Strings(sections)

	for _, name := range sections {
		var rows []configRow
		collectRows(name, schema.Properties[name], &rows)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## `%s`\n\n", name)
		b.WriteString("| Key | Type | Default | Valid range | Section | Description |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, r := range rows {
			rng := r.Range
			if rng == "" && r.Enum != "" {
				rng = r.Enum
			}
			if rng == "" {
				rng = "—"
			}
			section := r.Section
			if section == "" {
				section = "—"
			}
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s |\n",
				r.Key, r.Type, r.Default, rng, section, r.Description)
		}
		b.WriteString("\n")
	}

	return []byte(b.String()), nil
}

// collectRows walks n, appending one row per leaf. prefix is the dotted key path so far.
func collectRows(prefix string, n schemaNode, out *[]configRow) {
	if len(n.Properties) > 0 {
		keys := make([]string, 0, len(n.Properties))
		for k := range n.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectRows(prefix+"."+k, n.Properties[k], out)
		}
		return
	}

	*out = append(*out, configRow{
		Key:         prefix,
		Type:        orDash(n.Type.String()),
		Default:     renderJSONValue(n.Default),
		Range:       escapePipes(n.Range),
		Enum:        renderEnum(n.Enum),
		Section:     n.Section,
		Description: escapePipes(n.Description),
	})
}

// renderJSONValue renders a default compactly: `null` stays null (it is meaningful on
// youngDaly.measuredDeltaSeconds, where it means "measure at runtime", not zero).
func renderJSONValue(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return escapePipes(string(b))
}

// renderEnum renders an enum constraint as a pipe-free alternation.
func renderEnum(vals []any) string {
	if len(vals) == 0 {
		return ""
	}
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("`%v`", v))
	}
	return "one of " + strings.Join(parts, ", ")
}

// escapePipes keeps a literal `|` from breaking the markdown table it lands in.
func escapePipes(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// orDash renders an empty string as an em dash so no table cell is blank.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
