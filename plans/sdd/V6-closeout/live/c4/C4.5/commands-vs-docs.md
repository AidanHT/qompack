# C4.5 — the six /qompack: commands in a real session, against docs/commands.md

Agent-executed on the owner's real host per owner decision D3 by a Claude Code workflow subagent
(Opus 5.5) — not human UAT. Claude Code 2.1.280, model claude-haiku-4-5-20251001, Windows 11 Home
build 10.0.26200.9457, frozen bundle 0.3.0 (BUNDLE.json sha256 32600778...ccf4505, commit
d5598eb4) via `--plugin-dir`. Date 2026-09-29 (America/Toronto). Sessions: 1 (retrieval part n=7,
`session/`), run in the UAT-08/09 project after those rows were done (it holds decision
`dec_e760c8bbf50a` and a stale elimination; project config `{"eliminations":{"staleResponse":"drop"}}`
left by UAT-09; session env `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120`). Pre-run
`backup create/verify --id c45-before`: exit 0/0 (`cli/00-*`).

Evidence: `slash-commands.json` holds, per invocation, the expanded command body the host built
(the command file's text with the `!` output inlined) or the host's failure text, and the
`command_permissions` the host attached — extracted from this session's host transcript by
`slashx.py`; no user prose or model text is copied. `session/stream.jsonl` has the model's relay.
`cli/10-help-<cmd>` and `cli/11-*` are the same binary run directly for comparison.

## Host facts

- **`qompack` resolves on the command's PATH.** Turn 1 `where qompack` (Bash tool) printed exactly
  one hit, the bundle's own `...\qompack-plugin-0.3.0-windows-amd64\bin\qompack.exe` (stream L9->L13):
  with `--plugin-dir` the host puts the plugin's `bin/` on PATH, and every `!`qompack <cmd>`` below
  ran that binary. No PATH was pre-set; this was the first attempt. (Installed-plugin PATH is the
  install lane's C4.1/UAT-01 evidence, not re-tested here.)
- The init event lists exactly six commands, `qompack:dropped, eval, pin, recall, status, why`;
  **`qompack:checkpoint` is absent**, as D36 requires.
- Each command's `allowed-tools` reached the host as its `command_permissions`
  (`Bash(qompack <cmd>:*)`), matching the "Allowed tools" line of every section in commands.md.
- **Non-zero exit.** When the `!` command exits non-zero, the host does not send the command body
  to the model at all: it records `<local-command-stderr>Shell command failed for pattern
  "!`qompack eval `": [stderr] ...` and the turn ends with no assistant message (stream `result`
  with `num_turns: 0`, stream L109 and L112). The host puts stdout and stderr together under the `[stderr]` label; run directly,
  `qompack eval --json` writes the envelope to stdout and the one-line message to stderr
  (`cli/11-eval-json`). So `/qompack:eval` in a user project, `/qompack:recall` with no query and any
  other exit-1/2 path is visible to the user only as the host's failure line, never relayed.

## Per command

| invocation | exit (direct run) | what the host/model got | docs/commands.md (and user-guide) | verdict |
|---|---|---|---|---|
| `/qompack:status` | 0 | text report: `source: daemon (available, 0µs old)`, `host contract: 2 of 9 assertion(s) FAILING` (mcp.server_registered `initialize-not-received`, transcript.readable `transcript_path does not exist`), mode full, counters, per-hook table (every row `unavailable` with its reason), budgets B-A..B-G with measure and provenance, B-D "no instrument records this histogram in this build / it measures host process creation" | "mode, contracts, store, latency, last decision"; exit 0 | **pass** for shape and exit. Finding C45-1: two contract assertions FAILING in a session whose MCP server is connected and answering and whose transcript exists (cf. F-UAT01-1) |
| `/qompack:status --json` | 0 | envelope `{schema:1, command:"status", ok:true, data:{schema, collected_at_ms, primary, snapshot, hooks, budgets}}`; `primary {source daemon, status available, age_ms 0}`; each hook row has `latency:null` and a `provenance` with `reason` | versioned envelope; data carries primary, snapshot, hooks, budgets | **pass** |
| `/qompack:recall widen pool timeout --json` | 0 | envelope `{schema, command:"recall", ok:true, data:{tool, is_error:false, ephemeral:true, content:[{text: hits JSON}], meta}}`, 5 hits | `<query> [--k N]` plus `--json`; references and summaries, not bytes | **pass**. The hits are this project's own prompts and retrieval captures, not the files (see UAT-07's ranking note) |
| `/qompack:recall max_idle --k 2` | 0 | text form: the hits JSON (count 2) plus an `evidence` block (elapsed_ms, hash, tool_use_id, untrusted) and "marked ephemeral by Qompack; this says nothing about host eviction" | `--k <N>` return at most N | **pass** (`--k 2` honoured) |
| `/qompack:recall` (no query) | 2 | host failure: `qompack recall: qompack: usage: qompack recall: a query is required, e.g. qompack recall "open file handle"` | exit `2` a malformed invocation | **pass** |
| `/qompack:recall --help` | 0 | the usage block, byte-identical to the doc's block | `--help` prints the usage block | **pass** |
| `/qompack:pin The pool size must stay at 8 connections.` | 0 | `pinned inv_163d1f3c7e78 (source: user)`; `.qompack/pins/invariants.jsonl` holds it (`store/pins_*`) | `<text>`; default source user | **pass** |
| `/qompack:pin --list` | 0 | `inv_163d1f3c7e78  user    The pool size must stay at 8 connections.` | `--list` lists the pins in scope | **pass** |
| `/qompack:why dec_e760c8bbf50a` | 0 | found true, checkpoint_seq 4, what/why/alternatives_rejected, evidence, turn 0, `evidence_withheld: "authorization denied: the capture has no usable path provenance"`, plus the evidence block | `<decision-id>`; an attributed record of a decision | **pass**; the evidence is withheld (C44-7) |
| `/qompack:why dec_000000000000` | 0 | `{"decision_id":"dec_000000000000","found":false,"searched_checkpoints":[5,4,3,2,1]}` | user-guide: exit `1` "if the decision could not be read", `0` otherwise | **pass** (a clean miss is an answer, exit 0). `qompack why` with no id: exit 2 (`cli/11-why-missing`) as documented |
| `/qompack:dropped` | 0 | `{"count":0,"drops":[]}` plus the evidence block | "what the last compaction dropped and how to get it back" | **pass** (the text form is the JSON body plus the evidence block; nothing was dropped in these small sessions, so no entry's `Coverage` could be read — UAT-10 and UAT-11 have the same) |
| `/qompack:dropped --json` | 0 | envelope `{schema, command:"dropped", ok:true, data:{tool, is_error, ephemeral:true, content, meta}}` | versioned envelope | **pass** |
| `/qompack:eval` | 1 | host failure: `qompack eval: eval: qompack: unavailable: no evaluation artifacts: no live-eval run under <project>\dist\live-eval and no replay report at <project>\testdata\bench-replay.json. ... pass --corpus <path> to read one from elsewhere` | user-guide: in a project with no evaluation artifacts it reports unavailable and exits 1 | **pass** (the message names both paths) |
| `/qompack:eval --json` | 1 | host failure carrying the envelope `{schema:1, command:"eval", ok:false, error:{kind:"unavailable", message:...}}` and the stderr line | as above | **pass** |
| `qompack <cmd> --help`, all six (direct) | 0 each | `cli/10-help-*`: each is **byte-identical** to the fenced block under its section in commands.md | generated doc | **pass** |

## Findings

- **C45-1.** `/qompack:status` in a healthy session reports `host contract: 2 of 9 assertion(s)
  FAILING`: `mcp.server_registered` observed `initialize-not-received` while the same session's
  MCP server was connected and later answered `why`; `transcript.readable` observed
  `transcript_path does not exist`. Both look like ordering at session start (the stdio server's
  handshake notice and the transcript file are not there yet when the assertion runs) and match
  F-UAT01-1; they alarm a user who types `/qompack:status` first.
- **C45-2.** A command that exits non-zero is never relayed by the model: the host shows its own
  "Shell command failed" line. The documented exit codes are right; the experience of `/qompack:eval`
  is a host error line, which docs/commands.md does not mention.
- No command output differed from docs/commands.md's inventory, arguments, flags, help text or
  allowed-tools; `qompack:checkpoint` is absent.
