# MCP tools

**This file is generated. Do not edit it by hand.**
Run `go run ./tools/devtool gen-mcp-docs` after changing `mcp.ToolDefs`;
CI fails if it drifts (§8 `docs` job).

Qompack exposes these tools over the Model Context Protocol, on stdio, from
`qompack mcp` (registered by `plugin/.mcp.json`). The stdio process is a transcoder:
it speaks JSON-RPC 2.0 to the host and forwards every `tools/call` to the resident
daemon, which holds the warm store handles and is the single writer.

Two behaviours apply to every tool here.

**Results are ephemeral at birth.** Everything a retrieval tool returns is re-stored
with the ephemeral flag set, which makes it the *first* eviction candidate rather than
the last. Asking a question does not permanently enlarge the context; it borrows space
for as long as the answer is being used. The flag is surfaced to the client as
`_meta.qompack.ephemeral`. `record_eliminated` is the one exception — it writes a
durable ledger entry, which is the whole point of calling it.

**Spans are minimal by default.** A tool that returns file content returns the smallest
chunk-aligned span that covers what you asked for, widened to a symbol boundary where
one is known. Pass `full: true` when you genuinely need the whole object; the response
carries a `next_span` when there is more to page through.

A model should call `already_tried` before committing to an approach: Before committing to an approach, call already_tried.

## Tools at a glance

| Tool | Ephemeral result | Purpose |
|---|---|---|
| [`recall`](#recall) | yes | Search the store by content, path, or symbol; returns hashes and summaries, never content. |
| [`expand`](#expand) | yes | Re-materialize a cleared tool result by hash or tool_use_id. |
| [`re_read`](#re-read) | yes | Current or historical version of a file, from the store's own version history. |
| [`already_tried`](#already-tried) | yes | Bloom membership plus the stored reason when present, as one of three states: absent, active, or stale. |
| [`record_eliminated`](#record-eliminated) | no | Write negative knowledge: record that an approach does not work, with evidence and the files the reason rests on, so it survives compaction. |
| [`timeline`](#timeline) | yes | What happened between two points: the session's closed and open segments over a turn or timestamp range. |
| [`why`](#why) | yes | Retrieve a decision and its evidence from the checkpoint chain. |
| [`dropped`](#dropped) | yes | What is currently out of context: the explicit drop report for this session. |

## `recall`

Search the store by content, path, or symbol; returns hashes and summaries, never content. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

Re-materialize a cleared tool result by hash or tool_use_id. Returns the minimum sufficient span by default; pass full=true only when you genuinely need the whole object. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

Current or historical version of a file, from the store's own version history. Returns the minimum sufficient span by default; pass full=true only when you genuinely need the whole object. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

| Argument | Type | Required | Default | Valid values | Description |
|---|---|---|---|---|---|
| `path` | string | yes | — | — | Project-relative path. A :<symbol> or :<line> suffix anchors the minimal span. |
| `at` | string | no | — | — | Empty for the working-tree version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>. |
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
      "description": "Empty for the working-tree version; otherwise an RFC3339 timestamp, sha256:<64 hex>, or turn:<N>."
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

Bloom membership plus the stored reason when present, as one of three states: absent, active, or stale. Before committing to an approach, call already_tried.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

What happened between two points: the session's closed and open segments over a turn or timestamp range. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

Retrieve a decision and its evidence from the checkpoint chain. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

What is currently out of context: the explicit drop report for this session. Results are ephemeral and are evicted first.

*Result:* ephemeral — re-stored as the first eviction candidate, and reported as `_meta.qompack.ephemeral: true`.

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

