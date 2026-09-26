# V6 close-out coordinator tooling

The coordinator's own scripts, kept here so a later session can resume without the original
session's scratchpad. None of them ships; they drive the close-out.

| File | What it does |
|---|---|
| `wave1.js`, `wave2b.js`, `wave3.js`, `wave4.js` | The Workflow scripts for each dispatch wave. Each is implement → adversarial review → fix seat, one worktree per workstream (`../qompack-cx-<wN>-<ws>`, branch `closeout/<wN>-<ws>`). |
| `resume-w3w4.js` | The 2026-09-26 relaunch of the four workstreams the overnight pause stopped. Each seat merges `closeout/integration` `6aff949`, adopts or revises the earlier draft, and finishes. A verify stage checks the fix seat's resolutions. Committed `wave4.js` says `w4-e2ereds` in two places where the real report is `w3-e2ereds`; the resume script corrects it. |
| `digest.py` | `python digest.py <agent-transcript.jsonl> <out.txt>` writes one line per tool call and narration block, in order, from a stopped workflow agent's transcript, for a resuming seat to read. |
| `keepawake.ps1` | `pwsh -File keepawake.ps1 <sentinel>` holds a Windows keep-awake request (`SetThreadExecutionState`) while the sentinel file exists, so long gates and agent runs do not die to host sleep (the 2026-09-23 whole-tree run did). It changes no power settings; delete the sentinel to release. |
| `phase3.sh` | `sh phase3.sh <candidate-repo> <evidence-dir> <step…>`: the Phase 3 gates on the frozen candidate, one recorded run per step and strictly sequential (steps: `win-tree`, `win-race`, `lint`, `cover`, `gens`, `fuzz`, `bundles`, `linux-tree`, `linux-e2e`, `linux-child`, `release`). Its header maps each step to its checklist item. |
| `wave5.js` | Wave 5: cold start (D17), home-directory refusal (D18), Windows file semantics, shutdown helpers, directory-fsync redundancy (D20), dependency bumps. |
| `live-uat.js` | **Draft, not yet run.** The Phase 4 live lane: three sequential agents (install, sessions, retrieval) running UAT-01…12 as real `claude -p` sessions on the owner's host (D3: agent-executed, never human UAT), then an independent evidence audit. Before launch, replace `__CANDIDATE__` and `__BUNDLE__`, and create worktree `../qompack-cx-live` (branch `closeout/live`) at the frozen candidate. |
| `wfreport.py` | Renders a workstream's `report.md` from a workflow journal: `python wfreport.py <journal.jsonl> <ws-label> <run-id> <title> <out.md> [branch]`. Subagents cannot write report files, so the coordinator commits these. |
| `recrun.sh` | Records a run the V6 way: `sh recrun.sh <repo> <evidence-dir> <run-id> -- <command…>` writes `<id>.json` (argv, head, dirty, env, times, exit, log sha256) and `<id>.log`, and refuses to overwrite. |
| `shacheck.sh` | `sh shacheck.sh <worktree> <file>`: every quoted SHA must be reachable from HEAD (report-SHA reachability). |
| `rpwaive.py` | Applies `runpatterns` inline waivers from `devtool lint --only=runpatterns` output. It auto-handles alternations the checker splits at a bare pipe and `<placeholder>` patterns, and takes an explicit `file:line=reason` for anything else. Correct a quote instead when its only problem is trailing punctuation. |

Conventions: Linux gates use `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh` with Windows-style
paths for `--repo` and `--out` (a POSIX `/c/...` path is refused). Never SendMessage a running
workflow agent: it resumes a duplicate in the same worktree.
