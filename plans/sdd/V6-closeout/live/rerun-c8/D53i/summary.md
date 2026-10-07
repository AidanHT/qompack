# D53(i) on candidate 8's live re-check

Candidate `3ec62ad2e01b985640c0f1fb832df3917f766a5f`, frozen bundle `qompack-bundles/c8/qompack-plugin-0.3.0-windows-amd64` (BUNDLE.json sha256 `61ba9c37dda03c14c44acb6824646a8d7410751bba6f1ce3c5c864d6382dcd8b`), Claude Code 2.1.280, Windows 11 Home 25H2 build 10.0.26200.9457. Agent-executed on the owner's real host per owner decision D3 by a Claude Code workflow subagent (Opus 5.5), not human UAT. Computed 2026-10-07 (America/Toronto) by the resilience part from the committed evidence; numbers only. Candidate 7's C5.6 figures stand (D58(e)).

## Scope

20 session directories under `plans/sdd/V6-closeout/live/rerun-c8/` hold a meta.json or hooks.json; sessions.tsv has 20 rows for the parts sessions-c8, retrieval-c8, carried-c8, resilience-c8. Every session ran the candidate 8 bundle through --plugin-dir, model claude-haiku-4-5-20251001.

| session | exit | turns | hook pairs | outcomes | stderr bytes |
|---|---|---|---|---|---|
| C1.6/session | 0 | 7 | 22 | success 22 | 0 |
| C4.6/sessionC | 0 | 9 | 66 | success 66 | 0 |
| C4.7/off/session | 0 | 4 | 12 | success 12 | 0 |
| C4.7/reinj/session | 0 | 4 | 13 | success 13 | 0 |
| C4.9/session1 | 0 | 6 | 18 | success 18 | 0 |
| C4.9/session2 | 0 | 5 | 16 | success 16 | 0 |
| F-C48-1/session | 0 | 6 | 17 | success 17 | 0 |
| UAT-02/session | 0 | 10 | 38 | success 38 | 0 |
| UAT-04/session | 0 | 19 | 69 | success 69 | 0 |
| UAT-05/run2-session | 0 | 9 | 24 | success 24 | 0 |
| UAT-06/sessionA | 0 | 6 | 17 | success 17 | 0 |
| UAT-06/sessionB-resume | 0 | 4 | 10 | success 10 | 0 |
| UAT-06/sessionC-fork | 0 | 15 | 32 | success 32 | 0 |
| UAT-06/sessionD-parent-restart | 0 | 2 | 5 | success 5 | 0 |
| UAT-07/session | 0 | 25 | 99 | success 99 | 0 |
| UAT-08/session1 | 0 | 7 | 26 | success 26 | 0 |
| UAT-08/session2-isolation | 0 | 3 | 11 | success 11 | 0 |
| UAT-11/session | 0 | 5 | 26 | success 26 | 0 |
| UAT-12/sessionA | 0 | 14 | 71 | success 71 | 0 |
| UAT-12/sessionB | 0 | 15 | 58 | success 58 | 0 |

## Host-reported hook failures and timeouts

Counted: a hook_response whose outcome is not `success` or whose exit_code is not 0 (error, blocked, timeout, cancelled), a hook_started with no hook_response, a hook_response carrying an error field, a stderr.txt line naming a hook as failed, cancelled or timed out, and a host local-command output naming a hook as failed.

**Total: 0** across 650 hook calls in 20 sessions.

## Host-seen latency per hook event

live_driver.py's stream arrival delta between hook_started and hook_response (hooks.json). It is approximate and can under-read when the host batches output. Only pairs marked `"measured": true` enter n, median, p95 (nearest rank) and max; the rest are counted apart, never as 0 ms.

| hook event | n measured | median ms | p95 ms | max ms | not measured: before init | within 5 ms | no response | pairs |
|---|---|---|---|---|---|---|---|---|
| PostToolUse | 269 | 88 | 183 | 255 | 0 | 0 | 0 | 269 |
| SessionStart:compact | 26 | 135.5 | 395 | 796 | 1 | 0 | 0 | 27 |
| SessionStart:fork | 0 | None | None | None | 1 | 0 | 0 | 1 |
| SessionStart:resume | 0 | None | None | None | 2 | 0 | 0 | 2 |
| SessionStart:startup | 0 | None | None | None | 17 | 0 | 0 | 17 |
| Stop | 153 | 91 | 195 | 266 | 0 | 0 | 0 | 153 |
| SubagentStop | 27 | 99 | 193 | 204 | 1 | 0 | 0 | 28 |
| UserPromptSubmit | 134 | 83.0 | 204 | 263 | 19 | 0 | 0 | 153 |

All events: n measured 609, median 89 ms, p95 195 ms, max 796 ms; not measured 41.

PreCompact and SessionEnd emit no hook_started/hook_response pair in the stream, so no latency is measured for either. PreCompact is reported in each /compact turn's local-command output: 22 "completed successfully" lines, 0 failed. SessionEnd trouble would show only on stderr: every session's stderr.txt is empty.

Data: `data.json` beside this file (per session, every failure record, the per-event table).
