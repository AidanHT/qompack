# C4.4 — the real model calls each MCP tool

Agent-executed on the owner's real host per owner decision D3 by a Claude Code workflow subagent
(Opus 5.5) — not human UAT. Claude Code 2.1.280, model claude-haiku-4-5-20251001, Windows 11 Home
build 10.0.26200.9457, frozen bundle qompack-plugin-0.3.0-windows-amd64 (BUNDLE.json sha256
32600778ae6463cd47736fc6b0a8614ad782e5bbd0ef0440f4e2c3937ccf4505, commit d5598eb4) loaded with
`--plugin-dir`; the host's init event listed the server `plugin:qompack:qompack` connected and the
eight tools `mcp__plugin_qompack_qompack__<tool>`. Date 2026-09-29 (America/Toronto).

Main session: `session/` (retrieval part n=6, fresh project `c44`, defaults, 13 user turns, two
`/compact`s). `why`'s success call ran in the C4.5 session (n=7, `../C4.5/session/`, the UAT-08
project, which holds decision `dec_e760c8bbf50a`), because no checkpoint in this project carried a
decision (finding C44-4). Cross-references: UAT-07 run2 (`../../uat/UAT-07/run2/session/`), UAT-08
(`../../uat/UAT-08/session/`), UAT-09 (`../../uat/UAT-09/`). `Lx->Ly` = stream.jsonl line of the
tool_use -> line of its tool_result. "err" = the host's `is_error` on the tool_result.

The model cannot see result-level `_meta`; `cli/13-mcp-probe-meta.json` drives the same frozen
`qompack mcp` over stdio (no model) and shows `_meta.qompack.ephemeral: true` on all seven retrieval
tools and `ephemeral: false` on `record_eliminated`, as the page's "Result" lines say.

| tool | success call (stream ref) | error / miss calls (stream ref) | verdict vs docs/mcp-tools.md |
|---|---|---|---|
| `recall` | `{"query":"violet anchor lattice"}` L107->L110: hits with hash, path, tool, summary, ts, score, tool_use_id, span; `{"query":"path:src/cache.go","k":2}` L120->L123: the FileRead capture, count 1, found true | **malformed query** `k:0` L112->L113 err: `invalid arguments for recall: /k: below minimum`; `{"query":""}` L115->L118 not an error: 1 hit (the latest prompt) with an empty query echo | **pass** for the documented shape and the range check. Findings C44-1 (an empty query is answered, the CLI refuses it with exit 2) and UAT-07's path/tool selector findings |
| `expand` | `{"tool_use_id":"toolu_01PFwnukgjGXcpecDMqRCcXs"}` L149->L152: found true, hash, path, tool, span [0,385], total_bytes, truncated false, no `source`; by hash and by id resolve to the same content (UAT-07 run2 L127->L130, L131->L134) | **bad handle** `{"hash":"sha256:zz"}` L155->L156 err: `expand failed: hash must be "sha256:" followed by 64 hex characters`; unknown id L159->L162: `{"found":false,"searched":"tool_use index"}` (a miss, not an error); `{}` L165->L166 and both handles L169->L170 err: `expand requires exactly one of hash or tool_use_id`; well-formed never-stored hash (UAT-07 run2 L194->L197): `{"found":false,"available":false,"reason":"complete content provenance could not be established"}` | **pass**; finding C44-2 (the never-stored-hash miss does not say what was searched, the unknown-id miss does) |
| `re_read` | `{"path":"src/cache.go"}` L190->L193: found, `source":"store"`, turn 3, span; `src/cache.go:Evict` L210->L213 same; UAT-07 run2: latest = turn 7 (driftLimit 40), `at:"turn:1"` = the turn-1 version (25), and after an out-of-band disk edit (7) still 40 (L177->L180) | **unknown path** `src/nope.go` L196->L199: `{"found":false,"available":false,"reason":"no historical version has been captured for this path yet"}`; `../outside.txt` L202->L203 err: `path escapes the project root`; `at:"yesterday"` L206->L207 err: `at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>` | **pass** — never a live disk read |
| `already_tried` | after `record_eliminated`: L257->L260 `state:"active"` with reason, evidence, scope, recorded_at, depends_on; different approach (UAT-08 L127->L130) `{"state":"absent"}`; stale (UAT-09 s4) and uncertain under drop (UAT-09 s5) | before any compaction L17->L20: `{"found":false,"available":false,"reason":"elimination ledger not present in this build"}` — **no `state`, no `degraded`**; after the first compaction with no ledger file L83->L86: `{"state":"absent"}`; `{"target":"","approach":""}` L263->L266: `{"state":"absent"}` (no argument error) | **fail** for the pre-compaction answer (finding C44-3 = UAT-08 F1: the ledger opens only at the daemon's first compaction, and the nil-ledger answer is outside the tool's documented states); C44-5 (empty target/approach answered `absent`) |
| `record_eliminated` | L232->L235: id, descriptor, scope session, evidence, depends_on (src/cache.go resolved), `depends_on_unresolved:["src/missing.go"]`, status active, `warnings:["no stored version for src/missing.go; not used as a staleness dependency"]` | `scope:"global"` L238->L239 err: `invalid arguments for record_eliminated: /scope: value not in enum`; empty target L242->L243 err: `record_eliminated requires a non-empty target`; before any compaction (UAT-08 L36->L39): `{"found":false,"available":false,"reason":"elimination ledger not present in this build"}` | **pass after a compaction, fail before one** (C44-3). The record is stored with `"session":""` although scoped `session` and is visible to a later, different session (UAT-08 F3) |
| `timeline` | `{}` L27->L30 and L300->L303, `{"from":"0","to":"6"}` L307->L310: `count 1, found true`, one segment id 1, start_turn 0, **end_turn 0, closed false, tokens 0, frontier 0** — after 12 turns and two compactions | `{"from":"not-a-turn"}` L314->L315 err: `timeline: from must be a turn index, an RFC3339 timestamp, or empty`; reversed `{"from":"20","to":"2"}` L319->L322 accepted, same segment | **partial**: shapes and the malformed-bound error match; finding C44-6 (a live session's timeline carries no turn information; a reversed range is not rejected). After the session ended, a probe in the UAT-08 project shows closed segments with end_turn 24/36 (`../../uat/UAT-08/`, scratch probe) |
| `why` | C4.5 session L137->L140 `{"decision_id":"dec_e760c8bbf50a"}`: found true, checkpoint_seq 4, what, why, alternatives_rejected, evidence, turn 0, and `evidence_withheld: "authorization denied: the capture has no usable path provenance"` | `dec_000000000000` L375->L378: `{"decision_id":"dec_000000000000","found":false,"searched_checkpoints":[2,1]}`; malformed `not-a-decision` L379->L382: same miss shape, no format error | **pass** for shape (miss = `found:false` with what was searched); findings C44-4 (no checkpoint in this session carried the recorded elimination as a decision) and C44-7 (the decision's own evidence is withheld) |
| `dropped` | after two compactions L403->L406: `{"count":0,"drops":[]}`; before any compaction L22->L25: the same | unknown argument `{"session":"x"}` L407->L408 err: `invalid arguments for dropped: /session: unknown property`; outside a session (probe) `{"available":false,"count":0,"drops":[],"reason":"no live session"}` | **pass** (nothing was dropped in a session this small; the error matches `additionalProperties:false`) |

## Findings

- **C44-3 (defect, blocks UAT-08 as written).** In a daemon that has not yet handled a compaction,
  `record_eliminated` and `already_tried` answer
  `{"found":false,"available":false,"reason":"elimination ledger not present in this build"}`:
  `internal/cli/daemon.go` wires the tools to `liveLedger`, and the daemon opens the ledger lazily
  on its first compaction (`internal/daemon/rehydrate_service.go`, `wire_checkpoint.go`). A model
  cannot record negative knowledge in the first part of every session, nor after any daemon restart,
  until a compaction happens. The `already_tried` form has no `state` and no `degraded`, so it is
  outside the tool's documented answers (absent/active/stale/unavailable). Seen in UAT-05 (sessions
  part), UAT-08 T2/T3, UAT-09 s4 T5-T7 (after the daemon restarted), UAT-09 s5 T1 and here T1.
- **C44-4 / C44-7.** Checkpoints 0001 and 0002 here have `decisions: []` and `eliminated: []`
  although 0002 was sealed after an active elimination; the UAT-08 project's checkpoints 0001-0003
  likewise. Only checkpoint 0004 there (sealed by a daemon that opened the ledger at start) carries
  `dec_e760c8bbf50a`, with `turn: 0`. The MCP-origin records carry turn 0 (the fsck `index.tool_use`
  "reports turn 0 after turn N" rows in every post-run fsck of this part) and
  `checkpoint/decisions.go` `fromEliminations` skips a record whose turn is before the cut — a
  plausible cause, not proven here. When found, `why` withholds the evidence ("the capture has no
  usable path provenance").
- **C44-6.** `timeline` inside a live session returns one open segment with start/end turn 0 and 0
  tokens, and accepts `from` > `to`.
- **C44-1 / C44-5.** An empty `recall` query is answered (the CLI and `/qompack:recall` refuse it,
  exit 2); an empty `already_tried` target/approach is answered `absent`, while `record_eliminated`
  refuses an empty target.
- **C44-2.** The never-stored-hash miss (`expand`) says `available:false` with a provenance reason
  and does not name what was searched; the unknown-`tool_use_id` miss names `searched`.
- Every error above reached the model as a tool result, and the session continued; no call broke
  the host session.
