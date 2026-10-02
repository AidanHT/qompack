# C5.6 resource cost of the candidate 7 live lane

Numbers only, aggregated mechanically by the resilience part (last) from every part's committed
evidence under `plans/sdd/V6-closeout/live/rerun-c7/` that holds a `meta.json` or `hooks.json`: 20 sessions, 19 of them on the candidate 7 bundle and 1 on the previous build (kept apart below). `sessions.tsv` has 20 rerun-c7 rows (parts install-c7, sessions-c7, retrieval-c7, resilience-c7). No budget is proposed here.

Host: Windows 11 Home 25H2 build 10.0.26200.9457, Claude Code 2.1.280, model claude-haiku-4-5-20251001 in
every session. Candidate 7: commit d20309c03ffc364e4cc48663be73cfbb1f2309b2, frozen bundle
qompack-plugin-0.3.0-windows-amd64 (BUNDLE.json sha256 5212ae4e...e1f395). The previous-build session
is C4.8/sessionA, which loaded the candidate 5 bundle (qompack-bundles/c5) before the upgrade leg.
UAT-01/session and C4.8/sessionC loaded the candidate 7 bundle installed at local scope from the
qompack-live marketplace (no --plugin-dir; per sessions.tsv and the install part's notes). Runs were
agent-executed on the owner's real host per owner decision D3, not human UAT. Machine-readable form:
`data.json` beside this file.

## How each number was taken

Exactly as `plans/sdd/V6-closeout/live/resources/C5.6/summary.md` says:

- Store growth: `store_bytes_after - store_bytes_before` from live_driver.py, the byte total of every
  file under `<project>/.qompack/` at launch and at session end. It includes logs, spool, run files
  and any backup directory a scenario created in the project before the session. Sessions that
  shared a project (resume, fork, re-runs) each count only their own delta.
- Daemon memory/CPU: sampled after every turn by live_driver.py (PowerShell `Get-Process` on the pid in
  `.qompack/run/daemon.lock`, only when that pid is a live `qompack.exe`): WorkingSet64,
  PeakWorkingSet64 and CPU (cumulative processor seconds since that daemon started, which can be
  before the session when a daemon was already running). A turn with no running daemon is counted as
  not sampled. Nothing is sampled between turns or after the last turn, so the idle-exit tail and the
  SessionEnd flush are not in these numbers.
- Hook latency: the host-seen stream arrival delta between `system/hook_started` and
  `system/hook_response` for the same hook_id (live_driver.py `hooks.json`). The stream has no
  duration field, so this is APPROXIMATE: it can under-read when the host batches output. Only pairs
  with `"measured": true` are in n/median/p95/max; pairs that arrived before the init event, within
  5 ms of each other, or with no response are counted as not measured, never as ~0 ms. p95 is
  nearest-rank. SessionEnd does not appear at all: the host emits no hook events for it on the stream
  (its failures show only on stderr).
- D53(i), added for this candidate: a host-reported hook failure or timeout is a `hook_response` whose
  outcome is not `success` or whose exit code is not 0 (error, blocked, timeout), a hook_started with
  no hook_response, a line of the session's `stderr.txt` naming a hook as failed, cancelled or timed
  out ("SessionEnd hook [...] failed: Hook cancelled"), or a host local-command output naming a hook
  as failed (the `/compact` line "PreCompact [...] completed successfully" is the success form).

## Candidate 7 bundle sessions (19)

### Store growth per session

| evidence | turns | store before (B) | store after (B) | growth (B) |
|---|---:|---:|---:|---:|
| C4.8/sessionB | 4 | 493474 | 708658 | 215184 |
| C4.8/sessionC | 2 | 641724 | 787753 | 146029 |
| C4.9/session1 | 6 | 0 | 309625 | 309625 |
| C4.9/session2 | 6 | 206119 | 489160 | 283041 |
| UAT-01/session | 4 | 0 | 220643 | 220643 |
| UAT-03/session | 7 | 0 | 263206 | 263206 |
| UAT-04/session | 19 | 0 | 2724432 | 2724432 |
| UAT-05/run1-session | 8 | 0 | 267476 | 267476 |
| UAT-05/run2-session | 8 | 269458 | 360851 | 91393 |
| UAT-05/run2r-session | 9 | 0 | 341405 | 341405 |
| UAT-06/sessionA | 6 | 0 | 289149 | 289149 |
| UAT-06/sessionB-resume (resume) | 4 | 250509 | 339835 | 89326 |
| UAT-06/sessionC-fork (resume, fork) | 7 | 313831 | 496860 | 183029 |
| UAT-09/session1 | 9 | 0 | 446848 | 446848 |
| UAT-09/session2-resume (resume) | 10 | 307469 | 917975 | 610506 |
| UAT-09/session3-step5 | 3 | 0 | 201486 | 201486 |
| UAT-10/session | 7 | 0 | 428181 | 428181 |
| UAT-12/sessionA | 8 | 0 | 1796659 | 1796659 |
| UAT-12/sessionB | 16 | 1649327 | 7245437 | 5596110 |

Growth over 19 sessions: median 283041 B, p95 5596110 B, max 5596110 B, min 89326 B, total 14503728 B.

### Daemon working set and CPU

| evidence | turns sampled / not | daemon pids | WS max (MiB) | WS last (MiB) | peak WS max (MiB) | CPU s at last sample (sum over pids) | non-default env |
|---|---|---:|---:|---:|---:|---:|---|
| C4.8/sessionB | 4 / 0 | 1 | 38.0 | 38.0 | 38.0 | 0.344 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| C4.8/sessionC | 2 / 0 | 1 | 34.8 | 34.8 | 34.8 | 0.188 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| C4.9/session1 | 6 / 0 | 3 | 34.5 | 34.5 | 34.5 | 0.578 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| C4.9/session2 | 6 / 0 | 1 | 39.0 | 39.0 | 39.0 | 0.656 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| UAT-01/session | 4 / 0 | 1 | 70.9 | 70.9 | 70.9 | 0.281 | - |
| UAT-03/session | 7 / 0 | 1 | 37.7 | 37.7 | 37.7 | 0.266 | - |
| UAT-04/session | 19 / 0 | 1 | 53.6 | 53.6 | 53.6 | 1.953 | CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 |
| UAT-05/run1-session | 8 / 0 | 1 | 37.2 | 37.2 | 37.2 | 0.359 | - |
| UAT-05/run2-session | 8 / 0 | 2 | 41.3 | 28.4 | 41.3 | 0.875 | - |
| UAT-05/run2r-session | 9 / 0 | 2 | 40.9 | 38.3 | 40.9 | 1.094 | - |
| UAT-06/sessionA | 6 / 0 | 1 | 39.7 | 39.7 | 39.7 | 0.812 | - |
| UAT-06/sessionB-resume | 4 / 0 | 1 | 44.3 | 44.3 | 44.3 | 1.156 | - |
| UAT-06/sessionC-fork | 7 / 0 | 1 | 49.2 | 49.2 | 49.2 | 2.047 | - |
| UAT-09/session1 | 9 / 0 | 1 | 41.3 | 41.3 | 41.3 | 0.719 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| UAT-09/session2-resume | 10 / 0 | 1 | 44.8 | 44.8 | 44.8 | 1.109 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| UAT-09/session3-step5 | 3 / 0 | 1 | 31.5 | 31.5 | 31.5 | 0.312 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| UAT-10/session | 7 / 0 | 1 | 41.1 | 41.1 | 41.1 | 0.547 | - |
| UAT-12/sessionA | 8 / 0 | 1 | 54.1 | 54.1 | 54.1 | 1.141 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| UAT-12/sessionB | 16 / 0 | 1 | 61.9 | 61.3 | 62.0 | 3.578 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |

Across sessions with at least one sample (19): working set max per session median 41.1 MiB, max 70.9 MiB; peak working set max per session median 41.1 MiB, max 70.9 MiB; CPU seconds at the last sample (summed over the session's daemon pids) median 0.719, max 3.578.

### Host-seen hook arrival delta per hook event (approximate)

| event | n measured | median ms | p95 ms | max ms | not measured (before init / within 5 ms / no response) | outcome not success |
|---|---:|---:|---:|---:|---|---:|
| PostToolUse | 150 | 57.0 | 147 | 251 | 0 (0 / 0 / 0) | 0 |
| SessionStart:compact | 22 | 107.5 | 157 | 205 | 0 (0 / 0 / 0) | 0 |
| SessionStart:fork | 0 | None | None | None | 1 (1 / 0 / 0) | 0 |
| SessionStart:resume | 0 | None | None | None | 2 (2 / 0 / 0) | 0 |
| SessionStart:startup | 0 | None | None | None | 16 (16 / 0 / 0) | 0 |
| Stop | 119 | 54 | 109 | 148 | 0 (0 / 0 / 0) | 0 |
| SubagentStop | 22 | 58.0 | 91 | 200 | 0 (0 / 0 / 0) | 0 |
| UserPromptSubmit | 104 | 51.0 | 100 | 173 | 19 (19 / 0 / 0) | 0 |
| all events | 417 | 56 | 123 | 251 | 38 of 455 pairs | |

Largest measured pair: (251, 'UAT-12/sessionA', 'PostToolUse:Read').

## Previous-build session (C4.8/UAT-12 leg, candidate 5 bundle) (1)

### Store growth per session

| evidence | turns | store before (B) | store after (B) | growth (B) |
|---|---:|---:|---:|---:|
| C4.8/sessionA | 5 | 0 | 362475 | 362475 |

Growth over 1 sessions: median 362475 B, p95 362475 B, max 362475 B, min 362475 B, total 362475 B.

### Daemon working set and CPU

| evidence | turns sampled / not | daemon pids | WS max (MiB) | WS last (MiB) | peak WS max (MiB) | CPU s at last sample (sum over pids) | non-default env |
|---|---|---:|---:|---:|---:|---:|---|
| C4.8/sessionA | 5 / 0 | 1 | 75.0 | 75.0 | 75.0 | 0.375 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |

Across sessions with at least one sample (1): working set max per session median 75.0 MiB, max 75.0 MiB; peak working set max per session median 75.0 MiB, max 75.0 MiB; CPU seconds at the last sample (summed over the session's daemon pids) median 0.375, max 0.375.

### Host-seen hook arrival delta per hook event (approximate)

| event | n measured | median ms | p95 ms | max ms | not measured (before init / within 5 ms / no response) | outcome not success |
|---|---:|---:|---:|---:|---|---:|
| PostToolUse | 8 | 56.5 | 91 | 91 | 0 (0 / 0 / 0) | 0 |
| SessionStart:compact | 1 | 84 | 84 | 84 | 0 (0 / 0 / 0) | 0 |
| SessionStart:startup | 0 | None | None | None | 1 (1 / 0 / 0) | 0 |
| Stop | 4 | 49.0 | 53 | 53 | 0 (0 / 0 / 0) | 0 |
| SubagentStop | 1 | 49 | 49 | 49 | 0 (0 / 0 / 0) | 0 |
| UserPromptSubmit | 3 | 43 | 44 | 44 | 1 (1 / 0 / 0) | 0 |
| all events | 17 | 49 | 91 | 91 | 2 of 19 pairs | |

Largest measured pair: (91, 'C4.8/sessionA', 'PostToolUse:ToolSearch').

SessionStart is split by its host source (startup / compact / resume / fork). No PreCompact or
SessionEnd hook_started/hook_response pair appears in any session's stream (the host does not emit
them), so there is no host-seen delta for either; each manual `/compact` instead returns the host's
local-command line "PreCompact [${CLAUDE_PLUGIN_ROOT}/bin/qompack.exe checkpoint] completed successfully".

## D53(i): host-reported hook failures and timeouts

Total: **0** across all 20 sessions (both groups).

Every one of the 474 hook_response events is `success` with exit code 0, every hook_started has its response, every session's `stderr.txt` is empty of hook failures (20 of 20 stderr files are 0 bytes), and no local-command output names a hook as failed. None is therefore documented host behaviour under docs/cannot-do.md or docs/upstream-issues.md, because there is none to document.

Host errors that are not hook events (listed so they are not mistaken for one):

- UAT-05/run2-session: compaction failed (host/account error, no hook event involved) — "Error during compaction: You've hit your weekly limit · resets 7am (America/Toronto)" (rerun-c7/UAT-05/run2-session/stream.jsonl)
- UAT-05/run2-session: compaction failed (host/account error, no hook event involved) — "Error during compaction: You've hit your weekly limit · resets 7am (America/Toronto)" (rerun-c7/UAT-05/run2-session/stream.jsonl)

## sessions.tsv totals (rerun-c7 rows)

| part | sessions | wall s (sum) | non-zero exits | models |
|---|---:|---:|---:|---|
| install-c7 | 4 | 141.054 | 0 | claude-haiku-4-5-20251001 |
| sessions-c7 | 8 | 783.186 | 1 | claude-haiku-4-5-20251001 |
| retrieval-c7 | 5 | 426.999 | 0 | claude-haiku-4-5-20251001 |
| resilience-c7 | 3 | 140.606 | 0 | claude-haiku-4-5-20251001 |
| total | 20 | 1491.845 | 1 | |

The wall seconds are the driver's session wall (launch to process exit, including every hook the host
ran), not the model's time.
