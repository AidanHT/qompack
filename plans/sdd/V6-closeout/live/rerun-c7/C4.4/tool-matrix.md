# C4.4 on candidate 7 — the eight MCP tools in a real session

Agent-executed on the owner's real host under D3 (not human UAT), by a Claude Code workflow subagent (Opus 5.5).
Shared with UAT-09's second session: retrieval-c7 n=2, `../UAT-09/session2-resume/` (`--resume` of session
042883ea after the first daemon's idle exit), turns 1-10, 2026-10-02 15:27-15:29 America/Toronto;
claude-haiku-4-5-20251001, Claude Code 2.1.280, `--plugin-dir` the frozen c7 bundle (qompack 0.3.0, commit
d20309c0, BUNDLE.json sha256 5212ae4e...f395). Every call's arguments, host is_error flag and result text are in
`../UAT-09/session2-resume/tool-calls.txt` (and the stream). Session 1 (`../UAT-09/session1/`) adds the first
record_eliminated, active, absent, stale and uncertain answers; session 3 the unavailable form.

Init event (all three sessions): server `plugin:qompack:qompack` connected, the eight `mcp__plugin_qompack_qompack__*`
tools, and exactly six slash commands (qompack:dropped, eval, pin, recall, status, why); `qompack:checkpoint` absent,
as D36 expects.

| Tool | Success calls (result) | Error / miss calls (result) |
|---|---|---|
| already_tried | **after a daemon restart** (new pid 60480): T1 under drop `uncertain` (reason/note, no detail); T2 after a mid-session flag reload `stale` with reason, note, evidence, scope, recorded_at, depends_on, stale_because (candidate 4: `absent`, R4-1); T3 other approach `absent`; T7 project-scoped record `active` with all fields. Session 1: active, absent, stale, and uncertain after a mid-session drop reload | T3 `{"target":"","approach":""}`: is_error "already_tried requires a non-empty target and a non-empty approach"; session 3 (unreadable ledger): `unavailable`, degraded true |
| record_eliminated | T7 scope project, depends_on [config/pool.yaml]: id elim_36c719fae2c2, descriptor, scope, evidence, depends_on (resolved), depends_on_unresolved [], status active | T7 scope "global": is_error "invalid arguments for record_eliminated: /scope: value not in enum"; session 3: is_error "cannot record elimination: qompack: running in degraded mode: negknow: the elimination log is not appendable" |
| recall | T4 "DialPool": count 5, original captures (prompts, the src/pool.go FileRead) ahead of retrieval self-records; "path:src/pool.go" k 2: 1 hit (toolu_01HYukQC...) | k 0: is_error "/k: below minimum"; "": is_error "recall requires a non-empty query: ..."; "path:src/[": is_error "... the path selector is not a valid glob: \"src/[\"" |
| expand | T5 tool_use_id of the src/pool.go hit: found, hash, path, span [0,288], total_bytes 288, truncated false, no next_span | "sha256:zz": is_error 'hash must be "sha256:" followed by 64 hex characters'; "toolu_doesnotexist000": `{"found":false,"searched":"tool_use index"}`; {}: is_error "expand requires exactly one of hash or tool_use_id" |
| re_read | T6 src/pool.go: found, source store, turn 1, span [0,288] | src/nope.go: `{"found":false,"available":false,"reason":"no historical version has been captured for this path yet"}`; ../outside.txt: is_error "path escapes the project root"; at "yesterday": is_error "at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>" |
| timeline | T8 {}: count 3, frontier 13, segment 1 closed (turns 0-13, checkpoint_seq 1), segment 3 open (13-31), segment 2 closed (14-17); {"from":"1","to":"6"}: segment 1 only | {"from":"not-a-turn"}: is_error "timeline: from must be a turn index, an RFC3339 timestamp, or empty"; {"from":"20","to":"2"}: is_error "timeline: from is after to; pass from at or before to" |
| why | T9 dec_e760c8bbf50a (from checkpoint 0001): found, checkpoint_seq 1, what, why, evidence, evidence_bytes 145, alternatives_rejected, turn 3, hint | dec_000000000000 and "not-a-decision": `{"found":false,"searched_checkpoints":[1]}` (not tool errors) |
| dropped | T10 {}: count 1, pointer_dirty config/pool.yaml "modified since index on master" | {"session":"x"}: is_error "invalid arguments for dropped: /session: unknown property" |

Every success and every error call above is correct against docs/mcp-tools.md, and already_tried's restart answer is
now right. Read-only fsck, fsck --seal-check and doctor after the sessions exit 0 (`../UAT-09/cli/10-12`).

Observation (minor, not a documented contract): after the restart, the resumed session's open segment 3 starts at turn
13 (the frontier) and overlaps closed segment 2 (turns 14-17), and the list is ordered 1, 3, 2. mcp-tools.md promises
neither order nor disjointness; recorded for the coordinator next to D46's wave-14 follow-up on segments after a
mid-session daemon restart.

Result: pass on candidate 7.
Candidate 4 (9f6a2fad): fail (D50 relabelled it from partial) — already_tried answered `absent` for a stale record
after a daemon restart (R4-1), evidence plans/sdd/V6-closeout/live/rerun-c4/C4.4/
