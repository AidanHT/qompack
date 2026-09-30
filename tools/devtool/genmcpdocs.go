package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/mcp"
)

// mcpDocPath is the generated tool reference, relative to the repository root.
//
// It is generated from mcp.ToolDefs for the same reason docs/config-reference.md is generated from
// config.Defaults(): the schemas here are a published interface. A model reads the description and
// the input schema to decide whether and how to call a tool, so a hand-written page that drifted
// from the code would not merely be stale documentation — it would be a wrong specification of a
// live API, and the CI `docs` job exists to make that impossible.
const mcpDocPath = "docs/mcp-tools.md"

// taskGenMCPDocs renders docs/mcp-tools.md from mcp.ToolDefs. With --check it diffs instead of
// writing, which is what the CI `docs` job runs.
func taskGenMCPDocs(args []string) error {
	fs := flag.NewFlagSet("gen-mcp-docs", flag.ContinueOnError)
	check := fs.Bool("check", false, "diff docs/mcp-tools.md against mcp.ToolDefs instead of writing it")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}

	want, err := renderMCPDoc()
	if err != nil {
		return fmt.Errorf("gen-mcp-docs: %w", err)
	}

	p := filepath.Join(root, filepath.FromSlash(mcpDocPath))

	if *check {
		got, readErr := os.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("gen-mcp-docs --check: %s is missing; run `devtool gen-mcp-docs`", mcpDocPath)
		}
		if !bytes.Equal(want, normalizeNewlines(got)) {
			return fmt.Errorf("gen-mcp-docs --check: %s is stale; run `devtool gen-mcp-docs`", mcpDocPath)
		}
		fmt.Printf("gen-mcp-docs: %s is up to date\n", mcpDocPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, want, 0o644); err != nil { // #nosec G306 -- generated public documentation
		return err
	}
	fmt.Printf("gen-mcp-docs: wrote %s\n", mcpDocPath)
	return nil
}

// mcpParam is one rendered property of a tool's input schema.
type mcpParam struct {
	Name, Type, Required, Default, Enum, Description string
}

// mcpSchema is the subset of a tool's published input schema this renderer reads.
type mcpSchema struct {
	Type       string                   `json:"type"`
	Required   []string                 `json:"required"`
	Properties map[string]mcpSchemaProp `json:"properties"`
}

// mcpSchemaProp is one property of that schema.
type mcpSchemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     any    `json:"default"`
	Enum        []any  `json:"enum"`
	Minimum     *int64 `json:"minimum"`
	Maximum     *int64 `json:"maximum"`
}

// renderMCPDoc renders the whole page.
//
// The tools are emitted in mcp.ToolDefs order — the §8.7 design order, in which a session's own
// use of them tends to run — rather than alphabetically. Ordering matters here: a model reading
// this page top to bottom should meet `recall` before `expand`, because expanding something you
// have not recalled is not a thing you can do.
func renderMCPDoc() ([]byte, error) {
	// A zero ToolDeps is deliberate and safe: the definitions carry names, descriptions and
	// schemas, and the handlers a zero ToolDeps produces are never invoked here. Building them
	// with real collaborators would make the generated docs depend on a project on disk.
	defs := mcp.ToolDefs(mcp.ToolDeps{})

	var b strings.Builder
	b.WriteString("# MCP tools\n\n")
	b.WriteString("**This file is generated. Do not edit it by hand.**\n")
	b.WriteString("Run `go run ./tools/devtool gen-mcp-docs` after changing `mcp.ToolDefs`;\n")
	b.WriteString("CI fails if it drifts (§8 `docs` job).\n\n")
	b.WriteString("Qompack exposes these tools over the Model Context Protocol, on stdio, from\n")
	b.WriteString("`qompack mcp` (registered by `plugin/.mcp.json`). The stdio process is a transcoder:\n")
	b.WriteString("it speaks JSON-RPC 2.0 to the host and forwards every `tools/call` to the resident\n")
	b.WriteString("daemon, which holds the warm store handles and is the single writer.\n\n")

	b.WriteString("These behaviours apply to every tool here.\n\n")
	b.WriteString("**Ephemeral metadata describes Qompack records.** Retrieval responses expose\n")
	b.WriteString("`_meta.qompack.ephemeral`; this is not a host eviction control or proof of native\n")
	b.WriteString("context retention. Capture, archive availability and coverage may be partial or unknown.\n")
	b.WriteString("`record_eliminated` writes evidence; check its response before relying on persistence.\n\n")
	b.WriteString("**Query failures leave prior attempts unknown.** `already_tried` returns the added\n")
	b.WriteString("`unavailable` state, with `degraded: true`, when no elimination ledger can be opened or\n")
	b.WriteString("its query fails. Legacy JSON fields remain readable, but clients with a closed\n")
	b.WriteString("three-state enum must handle this outcome explicitly. Unavailable or unrecognized\n")
	b.WriteString("states never establish absence or prohibit an approach. `record_eliminated` answers a\n")
	b.WriteString("tool error, and records nothing, when no ledger can be opened.\n\n")
	b.WriteString("**The elimination ledger is live from the first call.** The daemon opens it the first\n")
	b.WriteString("time `already_tried` or `record_eliminated` is called, or at the first compaction,\n")
	b.WriteString("whichever comes first. A `session`-scoped elimination belongs to the session that\n")
	b.WriteString("recorded it and answers only there; `project` scope is shared across sessions. Each\n")
	b.WriteString("`already_tried` compares the matching record's `depends_on` files against the versions\n")
	b.WriteString("captured so far, so a dependency changed earlier in the same session answers `stale`.\n\n")
	b.WriteString("**Open segments report progress so far.** `timeline` gives a session's open segment\n")
	b.WriteString("the session's current turn as its `end_turn`, and its running token count when the\n")
	b.WriteString("daemon holds it; a closed segment reports what was recorded when it closed. A `from`\n")
	b.WriteString("after `to` is refused as a tool error.\n\n")
	b.WriteString("**Spans are minimal by default.** A tool that returns file content returns the smallest\n")
	b.WriteString("chunk-aligned span that covers what you asked for, widened to a symbol boundary where\n")
	b.WriteString("one is known. Pass `full: true` when you genuinely need the whole object; the response\n")
	b.WriteString("carries a `next_span` when there is more to page through.\n\n")
	b.WriteString("**Responses are bounded.** `runtime.mcp.maxResponseBytes` (default `262144`) bounds the\n")
	b.WriteString("result text `expand` and `re_read` return: the JSON body with its content escaped, not\n")
	b.WriteString("only the content. When the content does not fit, the response is cut, `truncated` is\n")
	b.WriteString("`true`, and `next_span` continues exactly where it stopped; pass it back as `span`, which\n")
	b.WriteString("takes precedence over `full` (`re_read` takes no `span`: continue a `re_read` page\n")
	b.WriteString("with `expand` and the response's `hash`). `truncated` is `true` exactly when the\n")
	b.WriteString("page stops before the object's end, and such a page always carries `next_span`; the\n")
	b.WriteString("page that reaches the end is not truncated, wherever it started. An explicit `span` is\n")
	b.WriteString("read the way `full: true` reads the whole object, over the range it names: the same\n")
	b.WriteString("cut, and a `next_span` that covers the rest of that range. A page never ends inside a\n")
	b.WriteString("multi-byte character. The JSON-RPC line that carries a result adds the transport's own\n")
	b.WriteString("framing.\n")
	b.WriteString("Only `expand` and `re_read` are measured against this key: `recall`, `already_tried`,\n")
	b.WriteString("`record_eliminated`, `timeline`, `why` and `dropped` results are not.\n\n")
	b.WriteString("**No fidelity or coverage field.** No retrieval response carries a capture fidelity\n")
	b.WriteString("or coverage value. `expand` and `re_read` report this read: `span`, `total_bytes`,\n")
	b.WriteString("`truncated` and `next_span`, and content the current privacy policy removed appears\n")
	b.WriteString("as a `«redacted:…»` placeholder. Capture fidelity is recorded on the capture's\n")
	b.WriteString("sidecar record, which no tool surfaces; the `fidelity:` line `qompack fsck` prints is\n")
	b.WriteString("store-level restore fidelity (`exact`, `full`, `canonical`, `unavailable`,\n")
	b.WriteString("`corrupt`), a different enumeration.\n\n")
	b.WriteString("**`recall` selectors.** `path:<glob>` is a `path.Match` pattern on slash paths (`*`\n")
	b.WriteString("does not cross `/`), matched against the whole project-relative path and against every\n")
	b.WriteString("trailing part of it, so `path:*.go` finds `src/ledger.go`; a malformed pattern is a tool\n")
	b.WriteString("error. A path with no `*`, `?` or `[` matches by equality, by trailing path segments, or\n")
	b.WriteString("as a substring. `tool:<name>` takes the host's tool name (`Read`, `Edit`, `Bash`) or\n")
	b.WriteString("Qompack's display name (`FileRead`, `FileEdit`), case-insensitively. A query with\n")
	b.WriteString("nothing to search for is a tool error, as is an `already_tried` call with an empty\n")
	b.WriteString("`target` or `approach`.\n\n")
	b.WriteString("**`recall` counts what it withheld.** `denied` counts the matched records that\n")
	b.WriteString("authorization withheld before any summary was built, and names none of them: a path\n")
	b.WriteString("outside the project or not safely resolvable, a path the host's saved Read rules deny\n")
	b.WriteString("or ask about, or a record with no usable path provenance. The last group includes\n")
	b.WriteString("Qompack's records of its own tool calls, which carry no file path and match a query\n")
	b.WriteString("whose words their responses echo; only a pathless `Bash`, `PowerShell`, prompt,\n")
	b.WriteString("`SubagentStop` or elimination-evidence record is served. `host_policy` is set when the\n")
	b.WriteString("host's settings could not be read, so path-bearing hits were withheld (fail closed).\n")
	b.WriteString("The check runs in the project's daemon against the host's saved settings files, not\n")
	b.WriteString("against anything a host session supplies, so `qompack mcp` started by hand outside a\n")
	b.WriteString("session is judged by the same rules; it can answer the same query differently because\n")
	b.WriteString("by then the store holds more of Qompack's own call records. A query can answer fewer\n")
	b.WriteString("hits than `k`, or none, with `denied` counting the ones withheld.\n\n")
	b.WriteString("**A session in the home directory is refused.** When a session's project root is the\n")
	b.WriteString("user's home directory, Qompack records nothing (owner decision D18), and `qompack mcp`\n")
	b.WriteString("answers every call with this tool error without starting a daemon:\n\n")
	fmt.Fprintf(&b, "> %s\n\n", mcp.HomeRootRefusedText)
	b.WriteString("**A project with `runtime.mode` \"off\" answers that.** `qompack mcp` writes nothing\n")
	b.WriteString("into such a project and starts no daemon, and every call answers this tool error:\n\n")
	fmt.Fprintf(&b, "> %s\n\n", mcp.ModeOffText)

	fmt.Fprintf(&b, "A model should call `%s` before committing to an approach: %s\n\n",
		mcp.ToolAlreadyTried, mcp.StandingInstruction)

	b.WriteString("## Tools at a glance\n\n")
	b.WriteString("| Tool | Ephemeral result | Purpose |\n")
	b.WriteString("|---|---|---|\n")
	for _, t := range defs {
		fmt.Fprintf(&b, "| [`%s`](#%s) | %s | %s |\n",
			t.Name, anchorFor(t.Name), yesNo(t.Ephemeral), escapePipes(firstSentence(t.Description)))
	}
	b.WriteString("\n")

	for _, t := range defs {
		fmt.Fprintf(&b, "## `%s`\n\n", t.Name)
		fmt.Fprintf(&b, "%s\n\n", t.Description)
		fmt.Fprintf(&b, "*Result:* %s.\n\n", ephemeralNote(t.Ephemeral))

		params, err := mcpParams(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		if len(params) == 0 {
			b.WriteString("Takes no arguments.\n\n")
		} else {
			b.WriteString("| Argument | Type | Required | Default | Valid values | Description |\n")
			b.WriteString("|---|---|---|---|---|---|\n")
			for _, p := range params {
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n",
					p.Name, orDash(p.Type), p.Required, orDash(p.Default), orDash(p.Enum),
					escapePipes(p.Description))
			}
			b.WriteString("\n")
		}

		pretty, err := prettySchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		b.WriteString("<details><summary>Input schema</summary>\n\n```json\n")
		b.WriteString(pretty)
		b.WriteString("\n```\n\n</details>\n\n")
	}

	return []byte(b.String()), nil
}

// mcpParams flattens one tool's input schema into rendered rows.
//
// Required properties are listed first, then optional ones, each group in schema declaration order
// — which is what a reader wants when deciding what to pass, and is stable across runs because it
// comes off the schema bytes rather than a map.
func mcpParams(raw json.RawMessage) ([]mcpParam, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s mcpSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decoding the input schema: %w", err)
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}

	order, err := schemaPropertyOrder(raw)
	if err != nil {
		return nil, err
	}

	var required, optional []mcpParam
	for _, name := range order {
		p, ok := s.Properties[name]
		if !ok {
			continue
		}
		row := mcpParam{
			Name:        name,
			Type:        p.Type,
			Required:    yesNo(req[name]),
			Default:     mcpDefaultCell(p.Default),
			Enum:        renderEnum(p.Enum),
			Description: p.Description,
		}
		if row.Enum == "" {
			row.Enum = renderBounds(p.Minimum, p.Maximum)
		}
		if req[name] {
			required = append(required, row)
		} else {
			optional = append(optional, row)
		}
	}
	return append(required, optional...), nil
}

// schemaPropertyOrder returns the property names in the order the schema bytes declare them.
func schemaPropertyOrder(raw json.RawMessage) ([]string, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("decoding the input schema: %w", err)
	}
	props, ok := outer["properties"]
	if !ok {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(props))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("decoding schema properties: %w", err)
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '{' {
		return nil, errors.New("schema properties is not an object")
	}
	var names []string
	for dec.More() {
		keyTok, keyErr := dec.Token()
		if keyErr != nil {
			return nil, fmt.Errorf("decoding schema properties: %w", keyErr)
		}
		key, isStr := keyTok.(string)
		if !isStr {
			return nil, errors.New("schema property name is not a string")
		}
		names = append(names, key)
		var skip json.RawMessage
		if decErr := dec.Decode(&skip); decErr != nil {
			return nil, fmt.Errorf("decoding schema property %q: %w", key, decErr)
		}
	}
	return names, nil
}

// prettySchema re-indents a tool's published schema for the collapsible block.
func prettySchema(raw json.RawMessage) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return "", fmt.Errorf("indenting the input schema: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// renderBounds renders a numeric property's minimum and maximum as one cell.
func renderBounds(minimum, maximum *int64) string {
	switch {
	case minimum != nil && maximum != nil:
		return fmt.Sprintf("%d–%d", *minimum, *maximum)
	case minimum != nil:
		return fmt.Sprintf("≥ %d", *minimum)
	case maximum != nil:
		return fmt.Sprintf("≤ %d", *maximum)
	default:
		return ""
	}
}

// ephemeralNote is the one-line result note under each tool's description.
func ephemeralNote(ephemeral bool) string {
	if ephemeral {
		return "marked ephemeral in Qompack metadata, with host retention unknown; reported as " +
			"`_meta.qompack.ephemeral: true`"
	}
	return "durable — this tool writes a persistent record"
}

// anchorFor is the GitHub heading anchor for a tool's `## \x60name\x60` heading.
//
// It reproduces GitHub's heading slug rather than inventing one: the text is lowercased, a space
// becomes "-", "_" and "-" survive because an underscore is a word character to that slugger, and
// every other character is dropped. Rewriting "_" as "-" — which this did — offered #re-read for
// a heading that answers to #re_read, so every underscored tool's glance-table link landed
// nowhere.
func anchorFor(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstSentence trims a description to its first sentence, for the glance table.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// yesNo renders a boolean as a table cell.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// mcpDefaultCell renders a property's default, distinguishing "the schema declares no default"
// from "the schema declares null". A required argument has no default, and rendering one as the
// JSON literal `null` would read as a legal value to pass.
func mcpDefaultCell(v any) string {
	if v == nil {
		return ""
	}
	return renderJSONValue(v)
}
