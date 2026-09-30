# C4.4 on candidate 4 — the eight MCP tools in a real session

Agent-executed on the owner's real host under D3 (not human UAT). Session retrieval-c4 n=5 (id 3187e738,
`session/`), turns 14-25; claude-haiku-4-5-20251001, Claude Code 2.1.280, `--plugin-dir` the frozen c4 bundle
(qompack 0.3.0, commit 9f6a2fad). Init event: server `plugin:qompack:qompack` connected, the eight
`mcp__plugin_qompack_qompack__*` tools, and exactly six commands (dropped, eval, pin, recall, status, why; no
`qompack:checkpoint`). One file per call in `mcp/` (arguments, host is_error, result). No-model stdio probes:
`cli/30-mcp-probe-error-calls.json`, `cli/31-mcp-probe-result-meta.json`.

| Tool | Success call (result) | Error / miss calls (result) |
|---|---|---|
| already_tried | T15 first ledger call of the project: `{"state":"absent"}` (the ledger opened on first use; candidate 3: "not present"); T20 session-scoped record: active with reason/evidence/scope/recorded_at/depends_on; project-scoped: active | T20 `{"target":"","approach":""}`: is_error "already_tried requires a non-empty target and a non-empty approach" (candidate 3: answered absent) |
| record_eliminated | T19 session scope, depends_on [src/cache.go, src/missing.go]: id, descriptor, scope, evidence, depends_on (cache.go resolved), depends_on_unresolved [src/missing.go], warnings ["no stored version for src/missing.go; not used as a staleness dependency"], status active; project scope: same shape | T19 scope "global": is_error "/scope: value not in enum"; empty strings: is_error "record_eliminated requires a non-empty target" |
| recall | T16 "violet anchor lattice" hits; "path:src/cache.go" k 2 | k 0: is_error "/k: below minimum"; "": is_error "recall requires a non-empty query …" (candidate 3: answered); "path:src/[": is_error "the path selector is not a valid glob" |
| expand | T17 tool_use_id of the src/cache.go hit: found, span, total_bytes | "sha256:zz": is_error 'hash must be "sha256:" followed by 64 hex characters'; unknown tool_use_id: `{"found":false,"searched":"tool_use index"}`; {}: is_error "expand requires exactly one of hash or tool_use_id"; both: same error |
| re_read | T18 src/cache.go and src/cache.go:Evict: found, source store, turn 27 | src/nope.go: found false "no historical version has been captured for this path yet"; ../outside.txt: is_error "path escapes the project root"; at "yesterday": is_error "at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>" |
| timeline | T15 live: one open segment, start_turn 0, **end_turn 29, tokens 6973**; T21 live: end_turn 41, tokens 11496; {"from":"1","to":"6"}: from 1, to 6; T24 after /compact: 3 segments (1 closed at turn 41 with checkpoint_seq 1, 2 closed 42-44, 3 open 45-46) | "not-a-turn": is_error "from must be a turn index, an RFC3339 timestamp, or empty"; **{"from":"20","to":"2"}: is_error "timeline: from is after to; pass from at or before to"** (candidate 3: turns 0-0 and from > to accepted) |
| why | T23 dec_951bc89c3930 from the injected block: found, checkpoint_seq 1, what/why/evidence/alternatives_rejected, turn 37 | dec_000000000000: `{"found":false,"searched_checkpoints":[1]}`; "not-a-decision": same shape, not a tool error |
| dropped | T15 before compaction: `{"count":0,"drops":[]}`; T24 after: count 29 (pointer_dirty src/ledger.go, user_intent_evolution entries) | the model dropped the unknown argument itself (passed {}); probe `{"session":"x"}`: is_error "invalid arguments for dropped: /session: unknown property" |

Checkpoint 0001 (`store-after-session/checkpoints_0001.json`) carries both eliminations (the session-scoped one with
session 3187e738) and both decisions (dec_951bc89c3930, dec_c770af84e084). Read-only fsck after the sessions:
`cli/40-fsck-after-sessions` exit 0.

Result: partial — every success and error call above is correct, including timeline's live turn ranges and the
from > to refusal; but already_tried answers `absent` for a STALE elimination once a daemon restarts (UAT-09
finding R4-1, observed in real sessions 2 and 7), which is a wrong result from this tool.
