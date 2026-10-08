# C4.4 on candidate 8 - the eight MCP tools in a real session

Agent-executed on the owner's real host under D3 (not human UAT), by a Claude Code workflow subagent (Opus 5.5).
Shared with UAT-07: session carried-c8 n=2 (`../UAT-07/session/`, id 5d758a40, turns 13-25), 2026-10-07 12:53-12:59
America/Toronto; claude-haiku-4-5-20251001, Claude Code 2.1.280, `--plugin-dir` the frozen c8 bundle (qompack 0.3.0,
commit 3ec62ad2, BUNDLE.json sha256 61ba9c37...dcd8b). Every call's arguments, host is_error flag and result text are in
`../UAT-07/session/tool-calls.txt` (and the stream). Re-run because wave 19c changed hostperm's patterns.go and
dropped()'s redaction (D60 ruling (iii)); candidate 7 passed this item.

Init event: server `plugin:qompack:qompack` connected, the eight `mcp__plugin_qompack_qompack__*` tools, exactly six
slash commands (qompack:dropped, eval, pin, recall, status, why); `qompack:checkpoint` absent, as D36 expects.

Refused paths: the project settings deny `Read(./private/vault.txt)` from the start (the host refused the turn-1 Read,
`../UAT-07/session/proofcheck.json`). Before turn 22 a before-step (`middeny.py`, `../UAT-07/settings/`) appended
`Read(./ops/runbook.md)`, a file the session had captured in turn 1, and appended one uncommitted line to it, so the
compaction's ground-truth check would carry a pointer_dirty drop naming a host-denied path.

| Tool | Success calls (result) | Error / miss / refusal calls (result) |
|---|---|---|
| already_tried | T14 before any record: `{"state":"absent"}`; T19 session-scoped record: `active` with reason, evidence, scope session, recorded_at 2026-10-07T16:56:35Z, depends_on; project-scoped: `active`, scope project; "halve the ttl": `absent` | T19 `{"target":"","approach":""}`: is_error "already_tried requires a non-empty target and a non-empty approach" |
| record_eliminated | T18 session scope, depends_on [src/cache.go, src/missing.go]: id elim_9f6ed064472b, descriptor (normalized_path, symbol, approach_class shrink-timeout, reason_hash), scope, evidence, depends_on (src/cache.go resolved), depends_on_unresolved ["src/missing.go"], status active; project scope: id elim_13fa3dafdf08, same shape, depends_on_unresolved [] | T18 scope "global": is_error "invalid arguments for record_eliminated: /scope: value not in enum"; empty strings: is_error "record_eliminated requires a non-empty target" |
| recall | T15 "violet anchor lattice": hits (the prompt, the src/cache.go FileRead, ...); "path:src/cache.go" k 2: 1 hit (toolu_01MSjeZh...); T13 "path:ops/runbook.md" before the rule: 1 hit (hash 3a792571, toolu_016YtAqF...) | k 0: is_error "invalid arguments for recall: /k: below minimum"; "": is_error "recall requires a non-empty query: free text, or a selector with a value ..."; "path:src/[": is_error "recall failed: qompack: store: the path selector is not a valid glob: \"src/[\""; "path:private/vault.txt" (never captured): `{"hits":[],"count":0,"found":false,...}`; T21 "path:ops/runbook.md" after the rule: 0 hits, `"denied":1` |
| expand | T9 by hash and by tool_use_id of the ledger.go FileEdit: same hash/span/content, no source (UAT-07 step 4); T16 tool_use_id of the src/cache.go hit: found, span [0,482], total_bytes 482 | "sha256:zz": is_error 'expand failed: hash must be "sha256:" followed by 64 hex characters'; "toolu_doesnotexist000": `{"found":false,"searched":"tool_use index"}`; {}: is_error "expand requires exactly one of hash or tool_use_id"; both: same error; T12 never-stored hash: found false, searched, available false, reason (UAT-07 step 7); T21 after the rule, by the runbook's hash and by its tool_use_id: each `{"found":false,"denied":true,"reason":"authorization denied: the host's current permission rules deny reading the associated path"}` |
| re_read | T17 src/cache.go and src/cache.go:Evict: found, source store, turn 25, span [0,482]; T10/T11 src/ledger.go (UAT-07 steps 5-6) | src/nope.go: `{"found":false,"available":false,"reason":"no historical version has been captured for this path yet"}`; ../outside.txt: is_error "path escapes the project root" (path not echoed); at "yesterday": is_error "at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>"; **private/vault.txt (host-denied, never captured): `{"found":false,"denied":true,"reason":"authorization denied: ..."}`**; T21 ops/runbook.md after the rule: the same denied answer |
| timeline | T14 {}: count 1, one open segment (start_turn 0, end_turn 27, tokens 8100); T20 {}: end_turn 39; {"from":"1","to":"6"}: from 1, to 6; T24 after /compact: count 2, segment 1 closed at turn 41 (checkpoint_seq 1), segment 2 open 42-46, frontier 41 | {"from":"not-a-turn"}: is_error "timeline: from must be a turn index, an RFC3339 timestamp, or empty"; {"from":"20","to":"2"}: is_error "timeline: from is after to; pass from at or before to" |
| why | T23 dec_951bc89c3930 from the injected block: found, checkpoint_seq 1, what 'rejected "raise the cache size" for src/cache.go:Evict', why, evidence, evidence_bytes 53, alternatives_rejected, turn 35, hint | dec_000000000000 and "not-a-decision": `{"found":false,"searched_checkpoints":[1]}` (not tool errors) |
| dropped | T14 before compaction: `{"count":0,"drops":[]}`; T24 after: count 40, `"denied":2`: 20 pointer, 18 user_intent_evolution, 2 pointer_dirty. **The runbook's pointer_dirty is listed, redacted**: `{"kind":"pointer_dirty","id":"sha256:3a792571...","detail":"modified since index on master; path withheld: the host's permission rules refuse it, or it is outside the project; restore: expand(hash=sha256:3a792571...)"}`; src/ledger.go's (dirty from UAT-07 step 6) is listed by path. No drop names ops/runbook.md, private/vault.txt or an out-of-project path | {"session":"x"}: is_error "invalid arguments for dropped: /session: unknown property" |

D60 ruling (iii) holds: dropped() returns the host-denied path's checkpoint drop redacted by hash, not removed. The
no-model stdio probe after the session (`cli/30-mcp-probe-result-meta.json`) gives the same dropped answer, the same
denied answers for re_read ops/runbook.md and private/vault.txt, and `_meta.qompack.ephemeral: true` on every result.

The compaction's block (`../UAT-07/session/injected-2-SessionStart-compact.txt`, 9,354 chars, SessionStart:compact
138 ms measured): section 6 shows the runbook's file pointer as `file (path withheld) sha256:3a792571...`, and both
pointers whose summary named it, the runbook's Read and the recall `{"query":"path:ops/runbook.md"}`, as `(summary
withheld)` with their ids and hashes; section 7 shows ids only. No shown pointer or drop line names ops/runbook.md,
private/vault.txt, the outside file or an absolute path outside the project root (the in-project absolute Read of
src/ledger.go is shown, as D63/D64(1) allow for a plain root). Section 2 quotes the user's prompts naming both files
(outside D50, D62(f)). Section 5's `goal:` quotes the latest prompt, which names ops/runbook.md: observation O-1 in
`../UAT-07/notes.txt`, left for the coordinator because no ruling names section 5.

Checkpoint 0001 (`../UAT-07/store-after-session/checkpoints_0001.json`) carries both eliminations and both decisions.
After the daemon's own idle exit: doctor --json exit 0 with no degraded row, fsck --json and fsck --json --seal-check
exit 0 (`cli/40-*`).

Result: pass on candidate 8. Every success, error and refusal call above is correct against docs/mcp-tools.md,
including the refused paths and dropped()'s redaction.
Candidate 7 (d20309c0): pass - every success and error call correct, already_tried after a restart, evidence
plans/sdd/V6-closeout/live/rerun-c7/C4.4/
Candidate 4 (9f6a2fad): fail (D50 relabelled it from partial) - already_tried answered `absent` for a stale record
after a daemon restart (R4-1), evidence plans/sdd/V6-closeout/live/rerun-c4/C4.4/
