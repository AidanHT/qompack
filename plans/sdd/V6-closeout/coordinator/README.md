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
| `c8-night.sh` | Candidate 8's night: preconditions (all checked before anything runs), the merged-tree check, the pre-freeze check, the freeze, bundles, push, then `overnight-c8.sh` with the night's deadline as an epoch. See "Candidate 8" below. |
| `overnight-c8.sh` | The frozen candidate's local night: AC-gated Windows timing with D57(d)'s power verdicts and per-try records, a power record for every step, Windows -race and bundles, the Linux lanes, quiet C5.1 and C5.2 (the derived set, on both OSes), and release-check in an isolated scratch clone. It refuses an evidence directory that already holds a night's records. Its header states every rule. |
| `prefreeze.sh` | `sh prefreeze.sh <repo> <evidence-dir> [step…]`: D53(a)'s pre-freeze check (gate, e2e, e2efunc, hotpath, integration, testpkgs, internal). A fresh `summary.log` per run, every line tagged with `PREFREEZE_RUN`, and a power verdict per step. |
| `power.sh` | Sourced by the three scripts above: the power reading, the System log's power events (Kernel-Power 105, 506, 42 and 107), the VALID / INVALID-POWER / NOT-REFERENCE verdict over a run's start and end readings, the bounded AC wait, the deadline and its daytime-launch guard. |
| `stamped.sh` | `sh stamped.sh <command…>`: prefixes each output line with its epoch second and keeps the command's exit status; release-check's AC-sensitive windows are read from it. |
| `nightharness.sh` | `sh nightharness.sh [case…]` (`-l` lists them): the dry harness for everything above. Stubs for powershell, pwsh, docker, go, claude, gh, timeout, date and sleep, real git and python on scratch repositories, a fake clock; nothing outside its temporary directory is touched, and the one real process outside the stubs is K1's keepawake.ps1 with nothing to hold. Run it after any change to a night script. |
| `c52derive.py` | `python c52derive.py <candidate-repo> <base-rev> <out-dir>`: D57(e) by construction. Traces each C5.2 benchmark once on the candidate (`-benchtime=1x` with a coverage profile) and selects those whose executed product files, own benchmark file or fixtures changed since `<base-rev>`; writes `report.txt`, `selection.tsv`, `pkgs.txt` and `filter.txt` for quiet.sh. A failed trace is selected, fail-closed. Self-test: `python c52derive.py --selftest`. |
| `mkrecheck8.py` | `python mkrecheck8.py live-rerun-c7.js <out.js> <candidate-sha> <bundle-dir>`: generates candidate 8's live re-check from candidate 7's lane, with the diff from candidate 7 that its D53(f) carry-forward notes are checked against. It refuses a candidate without ADR 0011 section 23, a bundle without BUNDLE.json, and an output that keeps a candidate 7 string, reads the clock or does not parse. |

Conventions: Linux gates use `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh` with Windows-style
paths for `--repo` and `--out` (a POSIX `/c/...` path is refused). Never SendMessage a running
workflow agent: it resumes a duplicate in the same worktree.

## Candidate 8

Candidate 8 is frozen and tested by `c8-night.sh` (D60(f), D61). Everything below is the
coordinator's procedure; no agent launches or aborts the night.

### Before launch

0. D61(a) holds: "a candidate is frozen only after a broad parallel audit comes back dry, with
   every finding verified by two skeptics; wave 19c's areas get their own". The audit of
   everything candidate 8 adds, wave 19c and wave 20 included, has come back dry, and the ledger
   row that records it is cited in the launch note. Do not launch without that row.
1. The owner has said go: the night uses the whole machine, and no other seat runs a test.
   `tasklist | grep -iE 'go\.exe|\.test\.exe|devtool'` shows nothing of the coordinator's.
2. Every `closeout/w*` branch whose tip is not already in candidate 7 (`d20309c0`) is merged into
   `closeout/integration`, and `closeout/w19-rehydrate` and `closeout/w19b-cmdconnect` exist.
   `c8-night.sh` refuses otherwise, naming the branch, so a later round (`w20b-*`, `w21-*`) or a
   fix pushed onto a merged branch cannot be left out. A branch left out on purpose goes in
   `C8_EXEMPT` (space-separated), and each exemption is logged with its tip. On 2026-10-03 three
   old branches hold commits that neither candidate 7 nor integration has: `closeout/w15-docs`
   (`21c07942`), `closeout/w15-ledger` (`12817cd0`: two checkpoint fixes and a negknow fix) and
   `closeout/w15-services` (`c212712d`: a store fix and a fault-test fix). Merge them or exempt
   them, and record which in the ledger.
3. `qompack-cx-int` and `qompack-cx-cand` are clean, `qompack-v6` is on `verify/v6` with no
   tracked change (the freeze commit lands there), `qompack-bundles/c8` does not exist, and
   `phase3/c8` holds no `chain.log`, `power.tsv` or `overnight-outcome.txt` from an earlier night.
   `c8-night.sh` checks all of these before anything runs. An earlier refused run's
   `phase3/c8/prefreeze` is moved aside automatically to `prefreeze.run-<n>`, and is never read as
   this run's.
4. `sh nightharness.sh` passes (60 cases). It is dry and takes up to about 2 hours (57 cases took
   6512 s on 2026-10-03 with other seats running), with no Go, Docker or Claude Code process (K1
   starts the real pwsh once, with nothing to hold). Re-run it after any change to a night script,
   and `python c52derive.py --selftest`.
5. The laptop lid is open and the charger is connected. Docker Desktop may be up or down, and the
   container `qompack-v6-linux-verification` must exist.
6. Choose the deadline and the launch time. `NIGHT_DEADLINE` (local `HH:MM`, default `08:00`) is
   the time after which no overnight step and no AC wait starts; `c8-night.sh` passes it to
   `overnight-c8.sh` as an epoch, so a late pre-freeze cannot carry the night into the next day. A
   deadline more than 16 h away (a daytime launch, whose next `HH:MM` is tomorrow's) is refused
   unless `NIGHT_ALLOW_FAR=1`. The night is about 9.5 h: the pre-freeze about 1 h (candidate 6:
   53 min), the overnight steps before release-check about 5.5 h (candidate 6's chain, 3 h 13 min;
   plus the derivation, an estimated 15-30 min for one traced iteration of each listed benchmark,
   and quiet C5.2 on both OSes, about 2 h for the rows today's diff reaches, from candidate 5's
   per-package times), then release-check 3 h (`RC_EST_S`).
   **Launch by about `NIGHT_DEADLINE` minus 9.5 h (22:30 for 08:00), earlier when the charger may
   cut AC (every AC wait comes out of the same night), or release-check is SKIPPED and C3.12 and
   D57(a)'s local reference run stay unmet.** `AC_WAIT_BUDGET_MIN` (default 180) bounds the whole
   night's waiting for AC, counted in one-minute polls.

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

- `night.log`: every decision, and the first line `start pid <msys> winpid <windows-pid> keep-awake
  pid <pid>`.
- `merged-tree.txt`, then `prefreeze/summary.log`. Each step line there carries its exit code,
  `run=<id>` and `power=<verdict>`.
- After the freeze: `host-validate.txt`, then `chain.log` for overnight's steps.
- `c52-derive/report.txt` and `selection.tsv`: the C5.2 rows whose executed files, own benchmark
  file or fixtures changed since candidate 7, which `c52-win` and `c52-linux` then measure
  (chain.log's `c52: derived set` line). If the derivation cannot run, chain.log says so, the
  static floor is measured, and the derivation counts as a failed step.
- `power.tsv`: one row per step try, the AC-independent steps included.
- `overnight-outcome.txt`: the counts. night.log's last lines repeat them.

The power verdicts:

- VALID means started and ended on AC with no power, standby, sleep or resume event during the
  run.
- INVALID-POWER is neither a pass nor a fail. For a gated step, try 1's records move to
  `<step>.invalid-power-1/` and the step is retried once. release-check is retried once, in a
  fresh clone, only on AC and only when a second run can end by the deadline.
- NOT-REFERENCE means no AC within the budget, a battery run, or an unreadable power history. The
  step still ran, but its timings are not a reference measurement. A Linux `*-timing` lane counts
  as a pass or a fail only when VALID; the other AC-independent steps count by exit status.
- SKIPPED means the deadline passed before the step could start.

### Abort

1. Take the Windows pid from night.log's latest `start pid … winpid <pid>` line (night.log is
   appended across relaunches). Then stop exactly that process tree, deepest first, and nothing
   else. The keep-awake pwsh is a child of that shell, so it goes with the tree:

   ```powershell
   $tree = @(<winpid>); $all = Get-CimInstance Win32_Process; $i = 0
   while ($i -lt $tree.Count) { $tree += @($all | Where-Object { $_.ParentProcessId -eq $tree[$i] } | ForEach-Object { $_.ProcessId }); $i++ }
   [array]::Reverse($tree); $tree | ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
   ```

   Then look again for orphans: `go.exe`, `*.test.exe` or `devtool` whose command line names
   `qompack-cx-cand`, `qompack-cx-int` or the scratch clone paths from step 4. A killed shell can
   leave its Go children behind. Stop only those.
2. Delete `phase3/c8/keepawake.sentinel`. A keepawake.ps1 that survived exits within 30 s, and it
   never re-creates a missing sentinel. A normal end, a refusal and a catchable signal already
   delete it and stop the keep-awake process through the exit trap.
3. Docker. The container runs twice: for the Linux lanes and for `c52-linux`. If chain.log's last
   `container start exit=0` line has no `container stopped` line after it, run
   `docker stop qompack-v6-linux-verification`. If the last `engine started by this chain` has no
   `engine stopped` after it, run `docker desktop stop`. Never stop an engine the chain did not
   start (`engine up at start=1`), because the owner's stack runs on it (D56(g)).
4. Scratch. Delete the paths night.log names (`merged-tree scratch clone <path>`) and chain.log
   names (`release-check clone <path>`, one per try) if they survived a hard kill. The v0.3.0 tag
   only ever existed in those clones. `git tag -l v0.3.0` in any worktree must print nothing.
5. Remove any `phase3/c8/quiet*/.quiet.lock` a hard kill left behind.
6. Git. If night.log has `candidate 8 frozen at <sha>` but no `pushed verify/v6`, then `verify/v6`
   holds an unpushed freeze commit. Its pre-freeze head is on the `integration <H>, verify/v6 <V>`
   line. Resetting to `<V>` is a coordinator decision, and it is recorded in the ledger. If the push
   happened, the candidate stands and only the overnight part is re-run.
7. Re-run.
   - Before the freeze: relaunch `c8-night.sh` as above.
   - After the freeze: run the night on the frozen candidate into a fresh evidence directory
     (overnight-c8.sh refuses one that already holds a night's records). `c8-night.sh` would
     refuse, because `qompack-bundles/c8` exists. overnight-c8.sh holds no keep-awake of its own,
     so start one beside it, and set the deadline: run alone, it takes the next `NIGHT_DEADLINE`
     and refuses one more than 16 h away. Delete the sentinel when chain.log's `done:` line
     appears.

     ```powershell
     $co = 'C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator'
     $e  = 'C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/c8-rerun-<n>'
     $env:NIGHT_DEADLINE = '08:00'
     New-Item -ItemType Directory $e | Out-Null; New-Item -ItemType File "$e/keepawake.sentinel" | Out-Null
     Start-Process pwsh -WindowStyle Hidden -ArgumentList @('-NoProfile', '-File', "$co/keepawake.ps1", "$e/keepawake.sentinel")
     Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden -ArgumentList @(
       "$co/overnight-c8.sh", 'C:/Users/Quant/Documents/Programming/Projects/qompack-cx-cand', '<sha>', $e)
     ```

### The live re-check

Generate it only when the night's evidence allows the candidate to stand, because its 25 real
sessions come out of an owner's budget that is already exceeded (D53(g)):

- `overnight-outcome.txt` shows `failed=0`;
- every INVALID-POWER, NOT-REFERENCE or SKIPPED step is re-run (Abort step 7) or dispositioned in
  the ledger;
- hosted `ci.yml` and `nightly.yml` on the frozen SHA are green, or every red is classified in the
  ledger.

Then generate the re-check and syntax-check it. The generator does both. It refuses a candidate
whose ADR 0011 lacks section 23, so wave 19c's ADR must be in the candidate, and it embeds the
`git diff --stat` from candidate 7 that the lane's D53(f) carry-forward notes are checked against:

```sh
python plans/sdd/V6-closeout/coordinator/mkrecheck8.py plans/sdd/V6-closeout/coordinator/live-rerun-c7.js \
  plans/sdd/V6-closeout/coordinator/live-recheck-c8.js <sha> \
  C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c8/qompack-plugin-0.3.0-windows-amd64
```

Then follow the generated script's header. It creates or verifies branch `closeout/live8` at the
candidate in `../qompack-cx-live` and checks the bundle. Launch the script as a Workflow only with
the owner's go. It runs 25 real sessions in four parts.
