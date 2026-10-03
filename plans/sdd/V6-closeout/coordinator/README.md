# V6 close-out coordinator tooling

The coordinator's own scripts, kept here so a later session can resume without the original
session's scratchpad. None of them ships; they drive the close-out.

| File | What it does |
|---|---|
| `wave1.js`, `wave2b.js`, `wave3.js`, `wave4.js` | The Workflow scripts for each dispatch wave. Each is implement → adversarial review → fix seat, one worktree per workstream (`../qompack-cx-<wN>-<ws>`, branch `closeout/<wN>-<ws>`). |
| `resume-w3w4.js` | The 2026-09-26 relaunch of the four workstreams the overnight pause stopped. Each seat merges `closeout/integration` `6aff949`, adopts or revises the earlier draft, and finishes. A verify stage checks the fix seat's resolutions. Committed `wave4.js` says `w4-e2ereds` in two places where the real report is `w3-e2ereds`; the resume script corrects it. |
| `digest.py` | `python digest.py <agent-transcript.jsonl> <out.txt>` writes one line per tool call and narration block, in order, from a stopped workflow agent's transcript, for a resuming seat to read. |
| `keepawake.ps1` | `pwsh -File keepawake.ps1 <sentinel>` holds a Windows keep-awake request (`SetThreadExecutionState`) while the sentinel file exists, so long gates and agent runs do not die to host sleep (the 2026-09-23 whole-tree run did). It changes no power settings; delete the sentinel to release. |
| `phase3.sh` | `sh phase3.sh <candidate-repo> <evidence-dir> <step…>`: the Phase 3 gates on the frozen candidate, one recorded run per step and strictly sequential (steps: `win-tree`, `win-race`, `win-timing`, `win-e2e-timing`, `lint`, `cover`, `gens`, `fuzz`, `bundles`, `linux-tree`, `linux-e2e`, `linux-timing`, `linux-e2e-timing`, `linux-child`, `release`). Its header maps each step to its checklist item. |
| `quiet.sh` | `sh quiet.sh <candidate-repo> <base-rev> <evidence-dir> <step…>`: Phase 5's quiet benchmark runs, strictly sequential, no co-load, after Phase 3 on an idle host (steps: `c51-win`, `c51-linux` for C5.1 bench-hotpath 5000 iterations plus TestBudgetBF for B-F; `c52-win`, `c52-linux` for C5.2, the carried and D37 benchmarks against `<base-rev>` = `cf31e01` in 10 balanced ABBA rounds (above C5.2's `-count 5`), with pinned benchstat, a paired table and a per-row completeness check that fails on skipped or short benchmarks). Windows runs go through `recrun.sh` with an isolated home for every benchmark binary, Linux through the non-root gate. It refuses a candidate repo with tracked changes and warns when the host is not idle. `QUIET_*` variables shrink it for a dry run. A `homeguard.py` check brackets the run. |
| `wave5.js` | Wave 5: cold start (D17), home-directory refusal (D18), Windows file semantics, shutdown helpers, directory-fsync redundancy (D20), dependency bumps. |
| `live-uat.js` | **Not yet run.** The Phase 4 live lane: four strictly sequential agents (install, sessions, retrieval, recovery; 8/12/12/4 = 36 real Haiku sessions, which with C5.5's 40 pre-registered trials and the 6 smoke/pilot sessions already used comes to about 82 against D3's ~40-80) covering C4.1–C4.11, C1.6, C1.7 and C5.6 on the owner's host (D3: agent-executed, never human UAT), then an independent read-only evidence and coverage audit. A failed real-home guard stops the lane. Before launch: merge `closeout/w7-livelane` into the candidate, create worktree `../qompack-cx-live` (branch `closeout/live`) at the frozen candidate, and replace `__CANDIDATE__` and `__BUNDLE__` (the script refuses to run otherwise). |
| `homeguard.py` | Real-home guard the live lane runs around every session and plugin step: `snap`/`check`/`clean` over `~/.claude/settings.json`, `plugins/installed_plugins.json` and `plugins/known_marketplaces.json` (SHA-256), the entry names of `plugins/{cache,marketplaces,data}`, and a name listing of `~/.qompack`. It never opens credentials. Self-test: `python homeguard_test.py` (fake home). |
| `live_driver.py` | The live lane's stream-json session driver (from `eval/runs/smoke_driver.py`): one message per turn, optional `before` argv per turn, host-seen hook arrival deltas (`hooks.json`, approximate; pairs flagged `measured: false` are not measured), planted-secret scrubbing of every output (`secrets_file`) and a `scan-staged` pre-commit secret gate, daemon RSS/CPU and `.qompack/` size per turn (`meta.json`). Self-test: `python live_driver_test.py` (fake host, no model). |
| `wfreport.py` | Renders a workstream's `report.md` from a workflow journal: `python wfreport.py <journal.jsonl> <ws-label> <run-id> <title> <out.md> [branch]`. Subagents cannot write report files, so the coordinator commits these. |
| `recrun.sh` | Records a run the V6 way: `sh recrun.sh <repo> <evidence-dir> <run-id> -- <command…>` writes `<id>.json` (argv, head, dirty, env, times, exit, log sha256) and `<id>.log`, and refuses to overwrite. |
| `shacheck.sh` | `sh shacheck.sh <worktree> <file>`: every quoted SHA must be reachable from HEAD (report-SHA reachability). |
| `rpwaive.py` | Applies `runpatterns` inline waivers from `devtool lint --only=runpatterns` output. It auto-handles alternations the checker splits at a bare pipe and `<placeholder>` patterns, and takes an explicit `file:line=reason` for anything else. Correct a quote instead when its only problem is trailing punctuation. |
| `c8-night.sh` | Candidate 8's night: preconditions, the merged-tree check, the pre-freeze check, the freeze, bundles, push, then `overnight-c8.sh`. See "Candidate 8" below. |
| `overnight-c8.sh` | The frozen candidate's local night: AC-gated Windows timing with D57(d)'s power verdicts and per-try records, Windows -race and bundles, the Linux lanes, quiet C5.1 and C5.2, and release-check in an isolated scratch clone. Its header states every rule. |
| `prefreeze.sh` | `sh prefreeze.sh <repo> <evidence-dir> [step…]`: D53(a)'s pre-freeze check (gate, e2e, e2efunc, hotpath, integration, testpkgs, internal). A fresh `summary.log` per run, every line tagged with `PREFREEZE_RUN`, and a power verdict per step. |
| `power.sh` | Sourced by the three scripts above: the power reading, the System log's power events (Kernel-Power 105 and 506), the VALID / INVALID-POWER / NOT-REFERENCE verdict, the bounded AC wait and the deadline. |
| `stamped.sh` | `sh stamped.sh <command…>`: prefixes each output line with its epoch second and keeps the command's exit status; release-check's AC-sensitive windows are read from it. |
| `nightharness.sh` | `sh nightharness.sh [case…]` (`-l` lists them): the dry harness for everything above. Stubs for powershell, pwsh, docker, go, claude, gh, timeout, date and sleep, real git on scratch repositories, a fake clock; no real process is started and nothing outside its temporary directory is touched. Run it after any change to a night script. |
| `mkrecheck8.py` | `python mkrecheck8.py live-rerun-c7.js <out.js> <candidate-sha> <bundle-dir>`: generates candidate 8's live re-check from candidate 7's lane. It refuses a candidate without ADR 0011 section 23, a bundle without BUNDLE.json, and an output that keeps a candidate 7 string, reads the clock or does not parse. |

Conventions: Linux gates use `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh` with Windows-style
paths for `--repo` and `--out` (a POSIX `/c/...` path is refused). Never SendMessage a running
workflow agent: it resumes a duplicate in the same worktree.

## Candidate 8

Candidate 8 is frozen and tested by `c8-night.sh` (D60(f), D61). Everything below is the
coordinator's procedure; no agent launches or aborts the night.

### Before launch

1. The owner has said go: the night uses the whole machine, and no other seat runs a test.
   `tasklist | grep -iE 'go\.exe|\.test\.exe|devtool'` shows nothing of the coordinator's.
2. Wave 19b, wave 19c and every wave 20 branch are merged into `closeout/integration`.
   `c8-night.sh` refuses otherwise. It checks `closeout/w19-rehydrate`, `closeout/w19b-cmdconnect`
   and every `closeout/w19c-*` and `closeout/w20-*` tip. A branch deliberately left out goes in
   `C8_EXEMPT` (space-separated), and each exemption is logged with its tip.
3. `qompack-cx-int` is clean. `qompack-v6` is on `verify/v6` with no tracked change, because the
   freeze commit lands there. `qompack-cx-cand` is clean, and `qompack-bundles/c8` does not exist.
   An earlier refused run's `phase3/c8/prefreeze` is moved aside automatically to
   `prefreeze.run-<n>`, and is never read as this run's.
4. `sh nightharness.sh` passes. It is dry and takes about 40 minutes, with no Go, Docker or Claude
   Code process. Re-run it after any change to a night script.
5. The laptop lid is open and the charger is connected. Docker Desktop may be up or down, and the
   container `qompack-v6-linux-verification` must exist.
6. Choose the deadline. `NIGHT_DEADLINE` (local `HH:MM`, default `08:00`) is the time after which
   no step and no AC wait starts. release-check starts only if `RC_EST_S` (3 h) lets it end by
   then. `AC_WAIT_BUDGET_MIN` (default 180) bounds the whole night's waiting for AC, counted in
   one-minute polls.

### Launch

From a PowerShell window, not a Claude Code background shell. The reaper would stop the night.

```powershell
$env:NIGHT_DEADLINE = '08:00'          # optional; AC_WAIT_BUDGET_MIN and C8_EXEMPT likewise
Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden -ArgumentList @(
  'C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator/c8-night.sh')
```

Launch through `Git\bin\bash.exe`, never `Git\usr\bin\sh.exe`, which starts without `/usr/bin` on
PATH. The script sends its own output to `night.log`. Do not edit any night script while the night
runs, because sh reads its script from a byte offset.

### Watch

All evidence is under `plans/sdd/V6-closeout/phase3/c8/`:

- `night.log`: every decision, and the first line `start pid <msys> winpid <windows-pid>`.
- `merged-tree.txt`, then `prefreeze/summary.log`. Each step line there carries its exit code,
  `run=<id>` and `power=<verdict>`.
- After the freeze: `host-validate.txt`, then `chain.log` for overnight's steps.
- `power.tsv`: one row per power-checked try.
- `overnight-outcome.txt`: the counts. night.log's last lines repeat them.

The power verdicts:

- VALID means started on AC with no power event during the run.
- INVALID-POWER is neither a pass nor a fail. Try 1's records move to `<step>.invalid-power-1/`
  and the step is retried once.
- NOT-REFERENCE means no AC within the budget, or an unreadable power history. The step still
  ran, but its timings are not a reference measurement.
- SKIPPED means the deadline passed before the step could start.

### Abort

1. Take the Windows pid from night.log's first line. Then stop exactly that process tree, deepest
   first, and nothing else:

   ```powershell
   $tree = @(<winpid>); $all = Get-CimInstance Win32_Process; $i = 0
   while ($i -lt $tree.Count) { $tree += @($all | Where-Object { $_.ParentProcessId -eq $tree[$i] } | ForEach-Object { $_.ProcessId }); $i++ }
   [array]::Reverse($tree); $tree | ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
   ```

   Then look again for orphans: `go.exe`, `*.test.exe` or `devtool` whose command line names
   `qompack-cx-cand`, `qompack-cx-int` or the scratch clone paths from step 4. A killed shell can
   leave its Go children behind. Stop only those.
2. Delete `phase3/c8/keepawake.sentinel`. keepawake.ps1 then releases within 30 s. A normal end,
   a refusal and a catchable signal already delete it through the exit trap.
3. Docker. If chain.log has `container start exit=0` but no `container stopped`, run
   `docker stop qompack-v6-linux-verification`. If it has `engine started by this chain` but no
   `engine stopped`, run `docker desktop stop`. Never stop an engine the chain did not start,
   because the owner's stack runs on it (D56(g)).
4. Scratch. Delete the paths night.log names (`merged-tree scratch clone <path>`) and chain.log
   names (`release-check clone <path>`) if they survived a hard kill. The v0.3.0 tag only ever
   existed in that clone. `git tag -l v0.3.0` in any worktree must print nothing.
5. Remove any `phase3/c8/quiet*/.quiet.lock` a hard kill left behind.
6. Git. If night.log has `candidate 8 frozen at <sha>` but no `pushed verify/v6`, then `verify/v6`
   holds an unpushed freeze commit. Its pre-freeze head is on the `integration <H>, verify/v6 <V>`
   line. Resetting to `<V>` is a coordinator decision, and it is recorded in the ledger. If the push
   happened, the candidate stands and only the overnight part is re-run.
7. Re-run.
   - Before the freeze: relaunch `c8-night.sh` as above.
   - After the freeze: run the night on the frozen candidate into a fresh evidence directory.
     `qompack-bundles/c8` exists, so `c8-night.sh` would refuse.

     ```sh
     sh plans/sdd/V6-closeout/coordinator/overnight-c8.sh ../qompack-cx-cand <sha> plans/sdd/V6-closeout/phase3/c8-rerun-<n>
     ```

     Launch it the same detached way.

### The live re-check

After the night, generate the re-check and syntax-check it. The generator does both and refuses
a candidate whose ADR 0011 lacks section 23, so wave 19c's ADR must be in the candidate:

```sh
python plans/sdd/V6-closeout/coordinator/mkrecheck8.py plans/sdd/V6-closeout/coordinator/live-rerun-c7.js \
  plans/sdd/V6-closeout/coordinator/live-recheck-c8.js <sha> \
  C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c8/qompack-plugin-0.3.0-windows-amd64
```

Then follow the generated script's header. It creates or verifies branch `closeout/live8` at the
candidate in `../qompack-cx-live` and checks the bundle. Launch the script as a Workflow only with
the owner's go. It runs 23 real sessions in four parts.
