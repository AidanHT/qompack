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
	b.WriteString("and loading continues (§11.3). Unknown keys produce a warning, never an error. A value of the\n")
	b.WriteString("wrong type is ignored with a warning, and the leaf keeps the value from the layer below.\n\n")
	b.WriteString("The hooks load configuration by the same per-leaf rules, with two kinds of problem that stop\n")
	b.WriteString("recording rather than fall back: input the hooks cannot read safely (a config file that does\n")
	b.WriteString("not parse, is not a plain file or is over its size bound, or an oversized `QOMPACK_*` or\n")
	b.WriteString("`--set` value), and any problem inside `runtime.redact`, where a fallback would record under\n")
	b.WriteString("a privacy policy you did not write. `qompack self-test` reports either as `config.capture`; see\n")
	b.WriteString("[docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility).\n\n")
	b.WriteString("A default that differs by platform names every value in its Default cell, portable one first.\n\n")
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
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n",
				r.Key, r.Type, defaultCell(r), rng, section, r.Description)
		}
		b.WriteString("\n")
	}

	writeConfigMetadata(&b)

	return []byte(b.String()), nil
}

// originMeanings is the one-phrase gloss each Origin gets in the Provenance origins table, taken
// from that constant's doc comment in internal/config/config.go. It is keyed by the constant, not
// by a spelling, so an origin that is renamed keeps its meaning and an origin that is ADDED loses
// its row's prose rather than acquiring a wrong one: writeOriginSection then falls back to the
// String() name alone, which is all the enum itself can tell a reader.
var originMeanings = map[config.Origin]string{
	config.OriginDefault:     "came from `config.Defaults()` and was never overridden",
	config.OriginUserFile:    "set by `~/.qompack/config.json`",
	config.OriginProjectFile: "set by `<project>/.qompack/config.json`",
	config.OriginEnv:         "set by a `QOMPACK_*` environment variable",
	config.OriginFlag:        "set by a `--set <dotted.key>=<value>` flag",
}

// writeConfigMetadata appends the four metadata sections: where a value came from, which blocks
// carry their own schema version, which switches ship off behind a gate, and which keys are still
// read but no longer mean what they used to.
//
// Every row is rendered from internal/config — Origin, config.VersionedSections,
// config.MigrationGates, config.MigrationBuildGates and config.RetiredMeaningKeys — so this
// generator holds no config key of its own and the page cannot describe a build it was not
// generated from.
func writeConfigMetadata(b *strings.Builder) {
	writeOriginSection(b)
	writeVersionedSection(b)
	writeGateSection(b)
	writeRetiredSection(b)
}

// writeOriginSection renders one row per Origin, ascending from OriginDefault. The enum bounds
// itself: String() answers "unknown" for the first value the enum does not define, which is where
// the table stops.
func writeOriginSection(b *strings.Builder) {
	b.WriteString("## Provenance origins\n\n")
	b.WriteString("`qompack config print --provenance` labels every leaf with the layer that produced its\n")
	b.WriteString("effective value. These are the labels, lowest precedence first.\n\n")
	b.WriteString("| Origin | Meaning |\n")
	b.WriteString("|---|---|\n")
	for o := config.Origin(0); o.String() != "unknown"; o++ {
		meaning := originMeanings[o]
		if meaning == "" {
			meaning = o.String()
		}
		fmt.Fprintf(b, "| `%s` | %s |\n", escapePipes(o.String()), escapePipes(meaning))
	}
	b.WriteString("\n")
}

// writeVersionedSection renders the independently versioned blocks and what a newer file does.
func writeVersionedSection(b *strings.Builder) {
	b.WriteString("## Versioned blocks\n\n")
	b.WriteString("The blocks below carry their own `settingsVersion` and are versioned independently, so a\n")
	b.WriteString("schema change to one never resets the other.\n\n")
	b.WriteString("| Block | `settingsVersion` this build understands | Behaviour |\n")
	b.WriteString("|---|---|---|\n")
	for _, s := range config.VersionedSections() {
		fmt.Fprintf(b, "| `%s` | `%d` | %s |\n", escapePipes(s.Path), s.Version,
			escapePipes("a file written for a newer version has its whole block reset to defaults, "+
				"so unknown future switches stay off"))
	}
	b.WriteString("\n")
}

// writeGateSection renders the two gate tables: the config switches that ship off behind a gate,
// and the build gates that no config layer can reach at all.
func writeGateSection(b *strings.Builder) {
	b.WriteString("## Gated switches (ship off)\n\n")
	b.WriteString("These leaves default to `false` and stay refused until their gate passes: a `true` value is\n")
	b.WriteString("refused at load, the leaf falls back to its default and the refusal is reported as a\n")
	b.WriteString("warning, so editing a config file cannot enable a capability this build does not support.\n\n")
	b.WriteString("| Key | Default | Owner | Gate | Status |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, g := range config.MigrationGates() {
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s | %s |\n",
			escapePipes(g.Key), escapePipes(gateDefaultCell(g.Key)),
			escapePipes(g.Owner), escapePipes(g.Gate), gateStatus(g))
	}
	b.WriteString("\n")

	b.WriteString("### Build gates (no config key)\n\n")
	b.WriteString("These capabilities have no configuration leaf behind them. They cannot be set from any\n")
	b.WriteString("config layer, and are reachable only from a build whose gate has passed.\n\n")
	b.WriteString("| Key | Owner | Gate | Status |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, g := range config.MigrationBuildGates() {
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n",
			escapePipes(g.Key), escapePipes(g.Owner), escapePipes(g.Gate), gateStatus(g))
	}
	b.WriteString("\n")
}

// gateStatus renders a gate's Status cell.
func gateStatus(g config.MigrationGate) string {
	if g.Passed {
		return "passed"
	}
	return "pending"
}

// gateDefaultCell reads a gated leaf's default out of the schema rather than asserting it: the
// page must report what this build ships, and genconfigdocs_test.go is what requires that value
// to be false.
func gateDefaultCell(key string) string {
	var schema schemaNode
	if err := json.Unmarshal(config.Defaults().JSONSchema(), &schema); err != nil {
		return ""
	}
	node := schema
	for _, part := range strings.Split(key, ".") {
		child, ok := node.Properties[part]
		if !ok {
			return ""
		}
		node = child
	}
	return renderJSONValue(node.Default)
}

// writeRetiredSection renders the keys whose value is still applied but whose meaning is retired.
func writeRetiredSection(b *strings.Builder) {
	b.WriteString("## Retired-meaning keys\n\n")
	b.WriteString("These keys are still read and their value is still applied, so an existing config file keeps\n")
	b.WriteString("loading. Setting one from any non-default layer produces a deprecation warning naming the\n")
	b.WriteString("file and line it was set in, and what the key no longer means.\n\n")
	b.WriteString("| Key | What it no longer means |\n")
	b.WriteString("|---|---|\n")
	for _, r := range config.RetiredMeaningKeys() {
		fmt.Fprintf(b, "| `%s` | %s |\n", escapePipes(r.Key), escapePipes(r.Note))
	}
	b.WriteString("\n")
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

// platformDefaultCells overrides the rendered Default cell of every leaf whose built-in default
// differs by platform, naming the portable value first and each platform that departs from it.
//
// It exists because this page is generated once and checked in, and the schema it is rendered
// from carries one number per leaf — whichever one the generating host ships. A cell left to
// render straight from config.Defaults() would therefore document only the platform it happened
// to be generated on, and would make `gen-config-docs --check` (the CI docs job, on ubuntu) fail
// for any contributor who regenerated the page on Windows. Both values are read from config, so
// this table can drift from the defaults only by a leaf being added to config and not here —
// which shows up as a Default cell that is silently platform-dependent again, not as a wrong
// number.
var platformDefaultCells = map[string]string{
	"runtime.daemon.connectDeadlineMs": fmt.Sprintf("`%d` (`%d` on Windows)",
		config.ConnectDeadlineMsPortable, config.ConnectDeadlineMsWindows),
	"runtime.budgets.l0IngestMs": fmt.Sprintf("`%d` (`%d` on Windows, `%d` on macOS)",
		config.L0IngestMsPortable, config.L0IngestMsWindows, config.L0IngestMsDarwin),
	"runtime.daemon.ackDeadlineMs": fmt.Sprintf("`%d` (`%d` on Windows, `%d` on macOS)",
		config.AckDeadlineMsPortable, config.AckDeadlineMsWindows, config.AckDeadlineMsDarwin),
}

// defaultCell renders r's Default column: its own value in code span, unless the key is one of
// the platform-specific defaults platformDefaultCells spells out in full.
func defaultCell(r configRow) string {
	if cell, ok := platformDefaultCells[r.Key]; ok {
		return cell
	}
	return "`" + r.Default + "`"
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
