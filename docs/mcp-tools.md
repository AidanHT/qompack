# MCP tools

**This file is generated. Do not edit it by hand.**
Run `go run ./tools/devtool gen-mcp-docs` after changing `mcp.ToolDefs`;
CI fails if it drifts (§8 `docs` job).

Qompack exposes these tools over the Model Context Protocol, on stdio, from
`qompack mcp` (registered by `plugin/.mcp.json`). The stdio process is a transcoder:
it speaks JSON-RPC 2.0 to the host and forwards every `tools/call` to the resident
daemon, which holds the warm store handles and is the single writer.

These behaviours apply to every tool here.

**Ephemeral metadata describes Qompack records.** Retrieval responses expose
`_meta.qompack.ephemeral`; this is not a host eviction control or proof of native
context retention. Capture, archive availability and coverage may be partial or unknown.
`record_eliminated` writes evidence; check its response before relying on persistence.

**Query failures leave prior attempts unknown.** `already_tried` returns the added
`unavailable` state, with `degraded: true`, when no elimination ledger can be opened or
its query fails. Legacy JSON fields remain readable, but clients with a closed
three-state enum must handle this outcome explicitly. Unavailable or unrecognized
states never establish absence or prohibit an approach. `record_eliminated` answers a
tool error, and records nothing, when no ledger can be opened.

**The elimination ledger is live from the first call.** The daemon opens it the first
time `already_tried` or `record_eliminated` is called, or at the first compaction,
whichever comes first. A `session`-scoped elimination belongs to the session that
recorded it and answers only there; `project` scope is shared across sessions. Each
`already_tried` compares the matching record's `depends_on` files against the versions
captured so far, so a dependency changed earlier in the same session answers `stale`.

**Open segments report progress so far.** `timeline` gives a session's open segment
the session's current turn as its `end_turn`, and its running token count when the
daemon holds it; a closed segment reports what was recorded when it closed. A `from`
after `to` is refused as a tool error.

**Spans are minimal by default.** A tool that returns file content returns the smallest
chunk-aligned span that covers what you asked for, widened to a symbol boundary where
one is known. Pass `full: true` when you genuinely need the whole object; the response
carries a `next_span` when there is more to page through.

**Responses are bounded.** `runtime.mcp.maxResponseBytes` (default `262144`) bounds the
result text `expand` and `re_read` return: the JSON body with its content escaped, not
only the content. When the content does not fit, the response is cut, `truncated` is
`true`, and `next_span` continues exactly where it stopped; pass it back as `span`, which
takes precedence over `full` (`re_read` takes no `span`: continue a `re_read` page
with `expand` and the response's `hash`). A page never ends inside a multi-byte
character. The JSON-RPC line that carries a result adds the transport's own framing.
Only `expand` and `re_read` are measured against this key: `recall`, `already_tried`,
`record_eliminated`, `timeline`, `why` and `dropped` results are not.

**No fidelity or coverage field.** No retrieval response carries a capture fidelity
or coverage value. `expand` and `re_read` report this read: `span`, `total_bytes`,
`truncated` and `next_span`, and content the current privacy policy removed appears
as a `«redacted:…»` placeholder. Capture fidelity is recorded on the capture's
sidecar record, which no tool surfaces; the `fidelity:` line `qompack fsck` prints is
store-level restore fidelity (`exact`, `full`, `canonical`, `unavailable`,
`corrupt`), a different enumeration.

**`recall` selectors.** `path:<glob>` is a `path.Match` pattern on slash paths (`*`
does not cross `/`), matched against the whole project-relative path and against every
trailing part of it, so `path:*.go` finds `src/ledger.go`; a malformed pattern is a tool
error. A path with no `*`, `?` or `[` matches by equality, by trailing path segments, or
as a substring. `tool:<name>` takes the host's tool name (`Read`, `Edit`, `Bash`) or
Qompack's display name (`FileRead`, `FileEdit`), case-insensitively. A query with
nothing to search for is a tool error, as is an `already_tried` call with an empty
`target` or `approach`.

**`recall` counts what it withheld.** `denied` counts the matched records that
authorization withheld before any summary was built, and names none of them: a path
outside the project or not safely resolvable, a path the host's saved Read rules deny
or ask about, or a record with no usable path provenance. The last group includes
Qompack's records of its own tool calls, which carry no file path and match a query
whose words their responses echo; only a pathless `Bash`, `PowerShell`, prompt,
`SubagentStop` or elimination-evidence record is served. `host_policy` is set when the
host's settings could not be read, so path-bearing hits were withheld (fail closed).
The check runs in the project's daemon against the host's saved settings files, not
against anything a host session supplies, so `qompack mcp` started by hand outside a
session is judged by the same rules; it can answer the same query differently because
by then the store holds more of Qompack's own call records. A query can answer fewer
hits than `k`, or none, with `denied` counting the ones withheld.

**A session in the home directory is refused.** When a session's project root is the
user's home directory, Qompack records nothing (owner decision D18), and `qompack mcp`
answers every call with this tool error without starting a daemon:

> qompack is inactive in this session: its project root is the user's home directory, so nothing is recorded and there is nothing to retrieve. Retrying will not help; the user can open a project directory (one with its own .git) to use qompack's tools.

**A project with `runtime.mode` "off" answers that.** `qompack mcp` writes nothing
into such a project and starts no daemon, and every call answers this tool error:

> qompack is switched off for this project: runtime.mode is "off", so nothing is recorded and there is nothing to retrieve. Retrying will not help; the user can set runtime.mode to "auto" (for example in .qompack/config.json) to use qompack's tools.

A model should call `already_tried` before committing to an approach: Before committing to an approach, call already_tried.

## Tools at a glance

| Tool | Ephemeral result | Purpose |
|---|---|---|
| [`recall`](#recall) | yes | Search captured archive material by content, path, or symbol; returns references and summaries. |
| [`expand`](#expand) | yes | Retrieve available archived content by hash or tool_use_id; fidelity and coverage may be incomplete. |
| [`re_read`](#re_read) | yes | The latest captured, or a historical, version of a file, from the store's own version history — never a live read of disk. |
| [`already_tried`](#already_tried) | yes | Query recorded elimination evidence: legacy answers are absent, active, or stale; a failed query is unavailable. |
| [`record_eliminated`](#record_eliminated) | no | Write negative knowledge: record that an approach does not work, with evidence and the files the reason rests on, so it survives compaction. |
| [`timeline`](#timeline) | yes | Retrieve recorded session segments over a turn or timestamp range. |
| [`why`](#why) | yes | Retrieve an attributed decision and its evidence from the checkpoint chain. |
| [`dropped`](#dropped) | yes | Retrieve Qompack's recorded omissions for this session. |

## `recall`

Search captured archive material by content, path, or symbol; returns references and summaries. Capture and coverage may be partial or unavailable.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `query` | string | yes | — | — | Free text, or prefixed selectors combined with spaces: path:<glob>, symbol:<name>, tool:<ToolName>. |
| `k` | integer | no | 5 | 1–50 | Maximum number of hits. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Free text, or prefixed selectors combined with spaces: path:<glob>, symbol:<name>, tool:<ToolName>."
    },
    "k": {
      "type": "integer",
      "minimum": 1,
      "maximum": 50,
      "default": 5,
      "description": "Maximum number of hits."
    }
  },
  "required": [
    "query"
  ],
  "additionalProperties": false
}
```

</details>

## `expand`

Retrieve available archived content by hash or tool_use_id; fidelity and coverage may be incomplete. Returns the minimum sufficient span by default; pass full=true only when you need the whole available object. Ephemeral metadata describes Qompack records; host context retention is unknown.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `hash` | string | no | — | — | Root or chunk hash as sha256:<64 hex>. Provide exactly one of hash or tool_use_id. |
| `tool_use_id` | string | no | — | — | tool_use_id taken from a tombstone or a recall hit. |
| `full` | boolean | no | false | — | Return the whole object instead of the minimum sufficient span. |
| `span` | string | no | — | — | Explicit span: "<off>:<len>" in bytes, or "L<start>-L<end>" in lines. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "hash": {
      "type": "string",
      "description": "Root or chunk hash as sha256:<64 hex>. Provide exactly one of hash or tool_use_id."
    },
    "tool_use_id": {
      "type": "string",
      "description": "tool_use_id taken from a tombstone or a recall hit."
    },
    "full": {
      "type": "boolean",
      "default": false,
      "description": "Return the whole object instead of the minimum sufficient span."
    },
    "span": {
      "type": "string",
      "description": "Explicit span: \"<off>:<len>\" in bytes, or \"L<start>-L<end>\" in lines."
    }
  },
  "required": [],
  "additionalProperties": false
}
```

</details>

## `re_read`

The latest captured, or a historical, version of a file, from the store's own version history — never a live read of disk. Returns the minimum sufficient span by default; pass full=true only when you need the whole available object. Ephemeral metadata describes Qompack records; host context retention is unknown.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `path` | string | yes | — | — | Project-relative path. A :<symbol> or :<line> suffix anchors the minimal span. |
| `at` | string | no | — | — | Empty for the latest captured version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>. Never reads the working tree. |
| `full` | boolean | no | false | — | Return the whole file instead of the minimum sufficient span. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Project-relative path. A :<symbol> or :<line> suffix anchors the minimal span."
    },
    "at": {
      "type": "string",
      "description": "Empty for the latest captured version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>. Never reads the working tree."
    },
    "full": {
      "type": "boolean",
      "default": false,
      "description": "Return the whole file instead of the minimum sufficient span."
    }
  },
  "required": [
    "path"
  ],
  "additionalProperties": false
}
```

</details>

## `already_tried`

Query recorded elimination evidence: legacy answers are absent, active, or stale; a failed query is unavailable. Clients must treat unavailable or unrecognized states as unknown, never as absence or a prohibition. Before committing to an approach, call already_tried.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `target` | string | yes | — | — | File path, optionally :symbol — e.g. src/auth.ts:refreshToken. |
| `approach` | string | yes | — | — | The approach as one short verb phrase — e.g. widen pool timeout. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "target": {
      "type": "string",
      "description": "File path, optionally :symbol — e.g. src/auth.ts:refreshToken."
    },
    "approach": {
      "type": "string",
      "description": "The approach as one short verb phrase — e.g. widen pool timeout."
    }
  },
  "required": [
    "target",
    "approach"
  ],
  "additionalProperties": false
}
```

</details>

## `record_eliminated`

Write negative knowledge: record that an approach does not work, with evidence and the files the reason rests on, so it survives compaction.

*Result:* durable — this tool writes a persistent record.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `target` | string | yes | — | — |  |
| `approach` | string | yes | — | — |  |
| `reason` | string | yes | — | — | Why it does not work. Encode what a competent engineer with no session history would get wrong. |
| `scope` | string | no | "session" | one of `session`, `project` |  |
| `depends_on` | array | no | — | — | Project-relative paths whose contents this reason rests on; a change to any of them flips this record to stale. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "target": {
      "type": "string"
    },
    "approach": {
      "type": "string"
    },
    "reason": {
      "type": "string",
      "description": "Why it does not work. Encode what a competent engineer with no session history would get wrong."
    },
    "scope": {
      "type": "string",
      "enum": [
        "session",
        "project"
      ],
      "default": "session"
    },
    "depends_on": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "Project-relative paths whose contents this reason rests on; a change to any of them flips this record to stale."
    }
  },
  "required": [
    "target",
    "approach",
    "reason"
  ],
  "additionalProperties": false
}
```

</details>

## `timeline`

Retrieve recorded session segments over a turn or timestamp range. Missing events and native context coverage may be unknown.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `from` | string | no | — | — | Turn index, RFC3339 timestamp, or empty for the session start. |
| `to` | string | no | — | — | Turn index, RFC3339 timestamp, or empty for the current frontier. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "from": {
      "type": "string",
      "description": "Turn index, RFC3339 timestamp, or empty for the session start."
    },
    "to": {
      "type": "string",
      "description": "Turn index, RFC3339 timestamp, or empty for the current frontier."
    }
  },
  "required": [],
  "additionalProperties": false
}
```

</details>

## `why`

Retrieve an attributed decision and its evidence from the checkpoint chain. Recorded reasoning does not prove model compliance.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `decision_id` | string | yes | — | — | A dec_<12 hex> id from a checkpoint or a rehydrated decision list. |

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {
    "decision_id": {
      "type": "string",
      "description": "A dec_<12 hex> id from a checkpoint or a rehydrated decision list."
    }
  },
  "required": [
    "decision_id"
  ],
  "additionalProperties": false
}
```

</details>

## `dropped`

Retrieve Qompack's recorded omissions for this session. This report does not establish what remains in native context.

*Result:* marked ephemeral in Qompack metadata, with host retention unknown; reported as `_meta.qompack.ephemeral: true`.

Takes no arguments.

<details><summary>Input schema</summary>

```json
{
  "type": "object",
  "properties": {},
  "required": [],
  "additionalProperties": false
}
```

</details>

