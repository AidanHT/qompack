# C5.6 resource cost of the live lane

Numbers only, aggregated mechanically by the recovery part from every part's committed live-lane
evidence under `plans/sdd/V6-closeout/live/` (29 sessions with a `meta.json`, 29 with a `hooks.json`; `sessions.tsv` has 29 rows). No budget is proposed here. Host: Windows 11 Home 10.0.26200, Claude Code 2.1.280, frozen bundle qompack-plugin-0.3.0-windows-amd64 (commit d5598eb4) in every session except uat/UAT-12/sessionA (the previous build, 0.2.99-prev, before the upgrade), model claude-haiku-4-5-20251001 in every session. Runs were agent-executed on the owner's real host per owner decision D3, not human UAT. The machine-readable form is `data.json` beside this file.

## How each number was taken

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

## Store growth per session

| evidence | turns | store before (B) | store after (B) | growth (B) |
|---|---:|---:|---:|---:|
| c4/C4.4/session | 13 | 0 | 709993 | 709993 |
| c4/C4.5/session | 16 | 1490697 | 1666759 | 176062 |
| c4/C4.7/off/session | 4 | 27 | 236 | 209 |
| c4/C4.7/reinj/session | 4 | 72 | 273490 | 273418 |
| c4/C4.9/session1 | 8 | 0 | 268003 | 268003 |
| c4/C4.9/session2-copy | 7 | 415648 | 649006 | 233358 |
| recovery/C1.6/session | 4 | 0 | 206764 | 206764 |
| recovery/C1.7/session1 | 3 | 0 | 187986 | 187986 |
| recovery/C1.7/session2 | 2 | 251229 | 358964 | 107735 |
| uat/UAT-01/session | 2 | 0 | 182988 | 182988 |
| uat/UAT-02/session | 10 | 397 | 1759874 | 1759477 |
| uat/UAT-03/session | 4 | 0 | 205163 | 205163 |
| uat/UAT-04/session | 10 | 0 | 1757861 | 1757861 |
| uat/UAT-05/run1-session | 8 | 0 | 278870 | 278870 |
| uat/UAT-05/run2-session | 8 | 454441 | 628769 | 174328 |
| uat/UAT-06/sessionA | 9 | 0 | 335740 | 335740 |
| uat/UAT-06/sessionB-resume | 4 | 278896 | 404522 | 125626 |
| uat/UAT-06/sessionC-fork | 5 | 404522 | 467354 | 62832 |
| uat/UAT-07/run1/session | 10 | 0 | 373273 | 373273 |
| uat/UAT-07/run2/session | 13 | 0 | 539418 | 539418 |
| uat/UAT-08/session | 14 | 0 | 487903 | 487903 |
| uat/UAT-09/session-s4-resume | 7 | 406891 | 631270 | 224379 |
| uat/UAT-09/session-s5-new | 4 | 562595 | 723791 | 161196 |
| uat/UAT-10/session | 8 | 0 | 339371 | 339371 |
| uat/UAT-11/diag-rerun/session | 7 | 0 | 1119778 | 1119778 |
| uat/UAT-11/session | 5 | 0 | 1670194 | 1670194 |
| uat/UAT-12/sessionA | 4 | 0 | 672436 | 672436 |
| uat/UAT-12/sessionB | 12 | 1229766 | 2789074 | 1559308 |
| uat/UAT-12/sessionC | 2 | 2031745 | 2158726 | 126981 |

Growth over 29 sessions: median 268003 B, p95 1757861 B, max 1759477 B, min 209 B, total 14320650 B.

## Daemon working set and CPU

| evidence | turns sampled / not | daemon pids | WS max (MiB) | WS last (MiB) | peak WS max (MiB) | CPU s at last sample (sum over pids) | non-default runtime env |
|---|---|---|---:|---:|---:|---:|---|
| c4/C4.4/session | 13 / 0 | 1 | 45.7 | 45.7 | 45.7 | 1.078 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| c4/C4.5/session | 16 / 0 | 1 | 37.7 | 37.7 | 37.7 | 0.375 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| c4/C4.7/off/session | 0 / 4 (no lock) | 0 | None | None | None | None | - |
| c4/C4.7/reinj/session | 4 / 0 | 1 | 35.4 | 35.4 | 35.4 | 0.234 | - |
| c4/C4.9/session1 | 5 / 3 (not running as qompack.exe) | 2 | 36.6 | 36.6 | 36.6 | 0.578 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| c4/C4.9/session2-copy | 7 / 0 | 1 | 36.3 | 36.3 | 36.3 | 0.547 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| recovery/C1.6/session | 4 / 0 | 1 | 32.8 | 32.8 | 32.8 | 0.312 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| recovery/C1.7/session1 | 3 / 0 | 1 | 31.6 | 31.6 | 31.6 | 0.375 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| recovery/C1.7/session2 | 2 / 0 | 1 | 29.7 | 29.7 | 29.7 | 0.203 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-01/session | 2 / 0 | 1 | 67.1 | 67.1 | 67.1 | 0.328 | - |
| uat/UAT-02/session | 10 / 0 | 1 | 49.8 | 49.8 | 49.8 | 1.75 | - |
| uat/UAT-03/session | 4 / 0 | 1 | 34.1 | 34.1 | 34.1 | 0.594 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-04/session | 10 / 0 | 1 | 50.1 | 50.1 | 50.1 | 1.938 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-05/run1-session | 8 / 0 | 1 | 36.9 | 36.9 | 36.9 | 0.828 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-05/run2-session | 8 / 0 | 1 | 32.2 | 32.2 | 32.2 | 0.812 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-06/sessionA | 9 / 0 | 1 | 37.3 | 37.3 | 37.3 | 0.609 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-06/sessionB-resume | 4 / 0 | 1 | 42.1 | 42.1 | 42.1 | 0.953 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-06/sessionC-fork | 5 / 0 | 1 | 46.1 | 46.1 | 46.1 | 1.344 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 |
| uat/UAT-07/run1/session | 10 / 0 | 1 | 40.1 | 40.1 | 40.1 | 0.406 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-07/run2/session | 13 / 0 | 1 | 40.7 | 40.7 | 40.7 | 0.781 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-08/session | 11 / 3 (not running as qompack.exe) | 1 | 41.7 | 41.7 | 41.7 | 0.516 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-09/session-s4-resume | 7 / 0 | 2 | 34.8 | 27.8 | 34.8 | 0.453 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-09/session-s5-new | 4 / 0 | 1 | 32.7 | 32.7 | 32.7 | 0.234 | - |
| uat/UAT-10/session | 8 / 0 | 1 | 40.3 | 40.3 | 40.3 | 0.312 | - |
| uat/UAT-11/diag-rerun/session | 7 / 0 | 1 | 53.5 | 53.5 | 53.5 | 1.016 | - |
| uat/UAT-11/session | 5 / 0 | 1 | 50.3 | 50.3 | 50.3 | 0.391 | - |
| uat/UAT-12/sessionA | 4 / 0 | 1 | 107.0 | 107.0 | 107.0 | 0.688 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-12/sessionB | 12 / 0 | 1 | 53.7 | 53.7 | 53.7 | 2.266 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |
| uat/UAT-12/sessionC | 2 / 0 | 1 | 37.3 | 37.3 | 37.3 | 0.891 | QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 |

Across sessions with at least one sample (28): working set max per session median 38.9 MiB, max 107.0 MiB; peak working set max per session median 38.9 MiB, max 107.0 MiB.

## Host-seen hook arrival delta per hook event (approximate)

| event | n measured | median ms | p95 ms | max ms | not measured (before init / within 5 ms / no response) | outcome not success |
|---|---:|---:|---:|---:|---|---:|
| PostToolUse | 228 | 52.0 | 146 | 296 | 0 (0 / 0 / 0) | 0 |
| SessionStart:compact | 35 | 86 | 144 | 8289 | 1 (1 / 0 / 0) | 0 |
| SessionStart:fork | 0 | None | None | None | 1 (1 / 0 / 0) | 0 |
| SessionStart:resume | 0 | None | None | None | 2 (2 / 0 / 0) | 0 |
| SessionStart:startup | 0 | None | None | None | 26 (26 / 0 / 0) | 0 |
| Stop | 179 | 56 | 127 | 237 | 0 (0 / 0 / 0) | 0 |
| SubagentStop | 37 | 79 | 124 | 153 | 1 (1 / 0 / 0) | 0 |
| UserPromptSubmit | 152 | 49.0 | 114 | 220 | 28 (28 / 0 / 0) | 0 |
| all events | 631 | 55 | 138 | 8289 | 59 of 690 pairs | |

SessionStart is split by its host source (startup / compact / resume / fork). No PreCompact or
SessionEnd hook_started/hook_response pair appears in any session's stream (the host did not emit
them; UAT-04's stream, for one, has 14 compact boundaries and no PreCompact hook event), so there is
no host-seen delta for either. Largest measured pair: (8289, 'uat/UAT-08/session', 'SessionStart:compact').

## sessions.tsv totals

| part | sessions | wall s (sum) | non-zero exits | models |
|---|---:|---:|---:|---|
| install | 4 | 189.158 | 0 | claude-haiku-4-5-20251001 |
| sessions | 10 | 1089.001 | 0 | claude-haiku-4-5-20251001 |
| retrieval | 12 | 1152.836 | 0 | claude-haiku-4-5-20251001 |
| recovery | 3 | 35.054 | 0 | claude-haiku-4-5-20251001 |
| total | 29 | 2466.049 | 0 | |

The wall seconds are the driver's session wall (launch to process exit, including every hook the host
ran), not the model's time.
