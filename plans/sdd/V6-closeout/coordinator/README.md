# V6 close-out coordinator tooling

The coordinator's own scripts, kept here so a later session can resume without the original
session's scratchpad. None of them ships; they drive the close-out.

| File | What it does |
|---|---|
| `wave1.js`, `wave2b.js`, `wave3.js`, `wave4.js` | The Workflow scripts for each dispatch wave. Each is implement → adversarial review → fix seat, one worktree per workstream (`../qompack-cx-<wN>-<ws>`, branch `closeout/<wN>-<ws>`). |
| `resume-w3w4.js` | The 2026-09-26 relaunch of the four workstreams the overnight pause stopped. Each seat merges `closeout/integration` `6aff949`, adopts or revises the earlier draft, and finishes. A verify stage checks the fix seat's resolutions. Committed `wave4.js` says `w4-e2ereds` in two places where the real report is `w3-e2ereds`; the resume script corrects it. |
| `digest.py` | `python digest.py <agent-transcript.jsonl> <out.txt>` writes one line per tool call and narration block, in order, from a stopped workflow agent's transcript, for a resuming seat to read. |
| `keepawake.ps1` | `pwsh -File keepawake.ps1 <sentinel>` holds a Windows keep-awake request (`SetThreadExecutionState`) while the sentinel file exists, so long gates and agent runs do not die to host sleep (the 2026-09-23 whole-tree run did). It changes no power settings. Create the sentinel first and delete it to release; a sentinel missing at start means it holds nothing and exits (it never creates one, so an early refusal cannot leave it holding). |
| `phase3.sh` | `sh phase3.sh <candidate-repo> <evidence-dir> <step…>`: the Phase 3 gates on the frozen candidate, one recorded run per step and strictly sequential (steps: `win-tree`, `win-race`, `win-timing`, `win-e2e-timing`, `win-x11-alone`, `c116-rig`, `lint`, `cover`, `gens`, `fuzz`, `bundles`, `linux-tree`, `linux-e2e`, `linux-timing`, `linux-e2e-timing`, `linux-child`, `release`). Its header maps each step to its checklist item; `c116-rig` is C1.16's load rig as w2-lifetime ran it (D62(c)). |
| `quiet.sh` | `sh quiet.sh <candidate-repo> <base-rev> <evidence-dir> <step…>`: Phase 5's quiet benchmark runs, strictly sequential, no co-load, after Phase 3 on an idle host (steps: `c51-win`, `c51-linux` for C5.1 bench-hotpath 5000 iterations plus TestBudgetBF for B-F; `c52-win`, `c52-linux` for C5.2, the carried and D37 benchmarks against `<base-rev>` = `cf31e01` in 10 balanced ABBA rounds (above C5.2's `-count 5`), with pinned benchstat, a paired table and a per-row completeness check that fails on skipped or short benchmarks). Windows runs go through `recrun.sh` with an isolated home for every benchmark binary, Linux through the non-root gate. It refuses a candidate repo with tracked changes and warns when the host is not idle. `QUIET_*` variables shrink it for a dry run. A `homeguard.py` check brackets the run. |
| `wave5.js` | Wave 5: cold start (D17), home-directory refusal (D18), Windows file semantics, shutdown helpers, directory-fsync redundancy (D20), dependency bumps. |
| `live-uat.js` | **Not yet run.** The Phase 4 live lane: four strictly sequential agents (install, sessions, retrieval, recovery; 8/12/12/4 = 36 real Haiku sessions, which with C5.5's 40 pre-registered trials and the 6 smoke/pilot sessions already used comes to about 82 against D3's ~40-80) covering C4.1–C4.11, C1.6, C1.7 and C5.6 on the owner's host (D3: agent-executed, never human UAT), then an independent read-only evidence and coverage audit. A failed real-home guard stops the lane. Before launch: merge `closeout/w7-livelane` into the candidate, create worktree `../qompack-cx-live` (branch `closeout/live`) at the frozen candidate, and replace `__CANDIDATE__` and `__BUNDLE__` (the script refuses to run otherwise). |
| `homeguard.py` | Real-home guard the live lane runs around every session and plugin step: `snap`/`check`/`clean` over `~/.claude/settings.json`, `plugins/installed_plugins.json` and `plugins/known_marketplaces.json` (SHA-256), the entry names of `plugins/{cache,marketplaces,data}`, and a name listing of `~/.qompack`. It never opens credentials. Self-test: `python homeguard_test.py` (fake home). |
| `live_driver.py` | The live lane's stream-json session driver (from `eval/runs/smoke_driver.py`): one message per turn, optional `before` argv per turn, host-seen hook arrival deltas (`hooks.json`, approximate; pairs flagged `measured: false` are not measured), planted-secret scrubbing of every output (`secrets_file`) and a `scan-staged` pre-commit secret gate, daemon RSS/CPU and `.qompack/` size per turn (`meta.json`). Self-test: `python live_driver_test.py` (fake host, no model). |
| `wfreport.py` | Renders a workstream's `report.md` from a workflow journal: `python wfreport.py <journal.jsonl> <ws-label> <run-id> <title> <out.md> [branch]`. Subagents cannot write report files, so the coordinator commits these. |
| `recrun.sh` | Records a run the V6 way: `sh recrun.sh <repo> <evidence-dir> <run-id> -- <command…>` writes `<id>.json` (argv, head, dirty, env, times, exit, log sha256) and `<id>.log`, and refuses to overwrite. |
| `shacheck.sh` | `sh shacheck.sh <worktree> <file>`: every quoted SHA must be reachable from HEAD (report-SHA reachability). |
| `rpwaive.py` | Applies `runpatterns` inline waivers from `devtool lint --only=runpatterns` output. It auto-handles alternations the checker splits at a bare pipe and `<placeholder>` patterns, and takes an explicit `file:line=reason` for anything else. Correct a quote instead when its only problem is trailing punctuation. |
| `c8-night.sh` | Candidate 8's night: preconditions (all checked before anything runs), the merged-tree check, the pre-freeze check, the freeze, bundles, push, then `overnight-c8.sh` with the night's deadline as an epoch, through release-check. See "Candidate 8" below. |
| `overnight-c8.sh` | The frozen candidate's local night: AC-gated Windows timing with D57(d)'s power verdicts and per-try records, a power record for every step, Windows -race and bundles, the Linux lanes, quiet C5.1 on both OSes (Linux report only), then release-check in an isolated scratch clone, each long step started only when its estimate ends by the deadline. With `C8_C52_ONLY=1` it runs the C5.2 night instead (D62(c)): C1.16's rig, then C5.2 on both OSes in AC-gated chunks by package group, by default the full list (`C8_C52_SET=full`, D62(b); `derived` measures c52derive.py's selection); `C8_C52_STEPS` runs only the steps it names. It refuses a checkout that is not exactly the candidate and clean, an evidence directory that already holds a night's records, and a mistyped switch. Its header states every rule. |
| `prefreeze.sh` | `sh prefreeze.sh <repo> <evidence-dir> [step…]`: D53(a)'s pre-freeze check (gate, e2e, e2efunc, hotpath, integration, testpkgs, internal). A fresh `summary.log` per run, every line tagged with `PREFREEZE_RUN`, and a power verdict per step. e2efunc skips test/e2e's timing rows, named exactly in its header and judged by the night on AC, then runs their functional arms by themselves; `sh prefreeze.sh --e2e-skips <repo>` prints its `-skip` pattern, or exits 2 when the names have drifted from the tree or from ci.yml's timing lane. |
| `power.sh` | Sourced by the three scripts above: the power reading, the System log's power events (Kernel-Power 105, 506, 42 and 107), the VALID / INVALID-POWER / NOT-REFERENCE verdict over a run's start and end readings, the bounded AC wait, the deadline and its daytime-launch guard. |
| `nightabort.ps1` | `pwsh -File nightabort.ps1 -MsysPid <msys> -WinPid <windows-pid> [-Stop]`: lists, and with `-Stop` stops, exactly one night's process tree (README "Candidate 8", Abort step 1). It walks MSYS's own parent pids from the night's shell (Windows records a dead parent for everything an MSYS shell starts), adds their native Windows children (a child only when created after its parent), and refuses unless the root still is the logged c8-night.sh or overnight-c8.sh shell. |
| `stamped.sh` | `sh stamped.sh <command…>`: prefixes each output line with its epoch second and keeps the command's exit status; release-check's AC-sensitive windows are read from it. |
| `nightharness.sh` | `sh nightharness.sh [case…]` (`-l` lists them): the dry harness for everything above (86 cases). Stubs for powershell, pwsh, docker, go, claude, gh, timeout, date and sleep, real git and python on scratch repositories, a fake clock; nothing outside its temporary directory is touched. The real processes outside the stubs are K1's keepawake.ps1 with nothing to hold, and K2's probe tree (sh, sleep, cmd and ping, under a shell named c8-night.sh), which the real nightabort.ps1 lists and stops. Run it after any change to a night script. |
| `c52derive.py` | `python c52derive.py <candidate-repo> <base-rev> <out-dir>`: D57(e) by construction. Traces each C5.2 benchmark on the candidate at its own listed benchtime with a coverage profile and selects those whose executed product files, the other changed files of a package they execute (declarations have no coverage block), own benchmark file, fixtures or adjacent assets changed since `<base-rev>`; reports, without selecting on it, whether an executed block covers a changed line; writes `report.txt`, `selection.tsv`, `pkgs.txt` and `filter.txt` for quiet.sh. A failed trace is selected, fail-closed; when every trace fails, or a stray .go file would join the trace, it writes no selection (exit 2) and overnight-c8.sh measures the full C5.2 list. overnight-c8.sh runs it only with `C8_C52_SET=derived`: candidate 8 measures the full list (D62(b)). Self-test: `python c52derive.py --selftest`. |
| `mkrecheck8.py` | `python mkrecheck8.py live-rerun-c7.js <out.js> <candidate-sha> <bundle-dir>`: generates candidate 8's live re-check from candidate 7's lane, with the diff from candidate 7 that its D53(f) carry-forward notes are checked against. It refuses a candidate without ADR 0011 section 23, a bundle without BUNDLE.json, and an output that keeps a candidate 7 string, reads the clock or does not parse. |

Conventions: Linux gates use `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh` with Windows-style
paths for `--repo` and `--out` (a POSIX `/c/...` path is refused). Never SendMessage a running
workflow agent: it resumes a duplicate in the same worktree.

## Candidate 8

Candidate 8 takes two nights (D62(c)). Candidate 8's night, `c8-night.sh`, freezes the candidate
and tests it through release-check by its deadline (D60(f), D61). The C5.2 night, `overnight-c8.sh`
alone with `C8_C52_ONLY=1`, follows on the frozen candidate: C1.16's load rig, then the full C5.2
list on Windows and Linux (D62(b)), in chunks a single AC cut cannot void together. If a C5.2 night
leaves a step unmeasured, a further C5.2 night measures only that step. Everything below is the
coordinator's procedure; no agent launches or aborts either night.

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
4. The pre-freeze's e2efunc step skips test/e2e's timing rows, which the night judges alone on AC
   after the freeze (`win-e2e-timing` runs all of test/e2e, as ci.yml's `test-e2e` job does, and
   `win-x11-alone` runs X11). They are named exactly in `E2E_NIGHT_ROWS` in `prefreeze.sh`:
   `TestV3_HotPathUnchangedWithLedgerResident` (X11), `TestE2E_SessionStartLatency` and
   `TestV5_ThrashWarningVisibleInStatusAndCheckpoint` (X10), plus any test/e2e row ci.yml's
   `timing` job names (none today). e2efunc then runs X10's one arm that judges no wall clock, its
   detector arm, by itself (`E2E_FUNC_ARMS`), and that arm must report its own PASS. X10's other
   three arms fail on a late prompt reply on a reference disk, so they wait for `win-e2e-timing`.
   This follows wave 21's e2e seat at `closeout/w21-e2e` `ab73828a`. There the latency row's judges
   are the lanes that run test/e2e alone without a co-load declaration (`scColoadJudges`). Its
   earlier `03f6824a` also named the pre-freeze e2efunc step as one of them, and `ab73828a` removed
   that. If the seat's merged text names e2efunc again, or changes either row's classification,
   update `E2E_NIGHT_ROWS` and `E2E_FUNC_ARMS` before launch, and record the ruling in the ledger.
   `c8-night.sh` checks the names against integration's test/e2e and ci.yml among its
   preconditions (`sh prefreeze.sh --e2e-skips <integration>`) and refuses on drift, naming the row
   or arm; night.log logs the `-skip` pattern it will use.
5. `sh nightharness.sh` passes (86 cases). It is dry and takes up to about 2 hours (80 cases took
   2447 s on 2026-10-04 at night; 57 took 6512 s on 2026-10-03 with other seats running), with no
   Go, Docker or Claude Code process (K1 starts the real pwsh once, with nothing to hold; K2
   starts a probe tree of sh, sleep, cmd and ping and stops it with nightabort.ps1). Re-run it
   after any change to a night script, and `python c52derive.py --selftest`.
6. The laptop lid is open and the charger is connected. Docker Desktop may be up or down, and the
   container `qompack-v6-linux-verification` must exist.
7. Choose the deadline and the launch time. `NIGHT_DEADLINE` (local `HH:MM`, default `08:00`) is
   the time after which no overnight step and no AC wait starts, and a step with an estimate
   (release-check here; c116-rig, c52-derive and each C5.2 chunk in the C5.2 night) starts only
   when it can end by then. `c8-night.sh` passes it to `overnight-c8.sh` as an epoch, so a late
   pre-freeze cannot carry the night into the next day. A deadline more than 16 h away (a daytime
   launch, whose next `HH:MM` is tomorrow's) is refused unless `NIGHT_ALLOW_FAR=1`. The night, in order,
   from candidate 6's and candidate 5's records:
   - the pre-freeze, about 1 h (candidate 6: 53 min);
   - the overnight steps before release-check, about 3 h 15 min (candidate 6's chain ran the same
     steps in 3 h 13 min: Windows timing 41 min, win-race and bundles 52 min, the Linux lanes
     93 min, C5.1 on both OSes 8 min);
   - release-check, 3 h (`RC_EST_S`). It must start by `NIGHT_DEADLINE` minus 3 h, which
     chain.log's first line prints.

   **Launch by `NIGHT_DEADLINE` minus 8.5 h (23:30 for 08:00), or release-check is SKIPPED and
   C3.12 and D57(a)'s local reference run stay unmet.** The pre-freeze and the steps before
   release-check take about 4 h 15 min and release-check 3 h; the last hour absorbs a slow step or
   an AC wait. C5.2 and the C1.16 rig are not part of this night at all: chain.log's last lines say
   `not in this night (D62(c)): c116-rig c52-win-observer … c52-linux-other`, and
   overnight-outcome.txt ends with `pending_c52_night=[c116-rig c52-win-observer … c52-linux-other]`
   (the rig and the eight C5.2 chunks); they count neither for nor against its exit status.
   `AC_WAIT_BUDGET_MIN` (default 180) bounds the whole night's waiting for AC, counted in
   one-minute polls, and every wait comes out of the same night.

### Launch

From a PowerShell window, not a Claude Code background shell. The reaper would stop the night.
`c8-night.sh` refuses when `C8_C52_ONLY` is set (a C5.2 night's switch left in the window would turn
the overnight part into a C5.2 night after the freeze), so clear it first:

```powershell
Remove-Item Env:C8_C52_ONLY, Env:C8_C52_SET, Env:C8_C52_STEPS -ErrorAction SilentlyContinue
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
  pid <pid>`. The preconditions include `precondition: e2efunc will skip -skip '<pattern>'`.
- `merged-tree.txt`, then `prefreeze/summary.log`. Each step line there carries its exit code,
  `run=<id>` and `power=<verdict>`. `prefreeze/e2efunc.log` starts with the `-skip` pattern.
- After the freeze: `host-validate.txt`, then `chain.log` for overnight's steps.
- `quiet-c51-linux/`: quiet C5.1 on Linux, report only. B-D, B-E and B-F are recorded there for
  inventory rows 1.10.16, 1.17.5 and 1.17.6, whose Linux halves cite candidate 6. Its exit status
  is neither a pass nor a fail, because the container's B-A and B-B are not verified in target
  (D53(b)). It counts under `reported=`, and as failed only when it wrote no harness JSON.
- `power.tsv`: one row per step try, the AC-independent steps included.
- `overnight-outcome.txt`: the counts, then `pending_c52_night=[…]`. night.log's last lines repeat
  them.

The power verdicts (both nights):

- VALID means started and ended on AC with no power, standby, sleep or resume event during the
  run.
- INVALID-POWER is neither a pass nor a fail. For a gated step, try 1's records move to
  `<step>.invalid-power-1/` and the step is retried once.
- NOT-REFERENCE means no AC within the budget, a battery run, or an unreadable power history. The
  step still ran, but its timings are not a reference measurement. A Linux `*-timing` lane counts
  as a pass or a fail only when VALID; the other AC-independent steps count by exit status.
- release-check is judged over its AC-sensitive windows only: a window with a power event is
  INVALID-POWER, one wholly on battery NOT-REFERENCE. Either is retried once, in a fresh clone,
  only on AC and only when a second run can end by the deadline: one as long as try 1 when try 1
  passed, and `RC_EST_S` when it failed (release-check stops at its first FAIL).
- SKIPPED means the deadline passed before the step could start, or the step's estimate
  (release-check, c116-rig, c52-derive, a C5.2 chunk) would have ended after it.

### Abort

1. Take the two pids from the latest `start pid <msys> winpid <windows-pid>` line in night.log
   (night.log is appended across relaunches), or, for a night started alone (step 7, and the C5.2
   night), from chain.log's `start candidate=<sha> pid <msys> winpid <windows-pid>` line. Stop
   exactly that shell's process tree and nothing else, with `nightabort.ps1`. A walk down Windows'
   ParentProcessId cannot do it, two ways. An MSYS shell starts every program by fork and exec,
   and the fork's stub exits, so the night's sh, go, docker and python processes record a parent
   that no longer exists: such a walk finds the root alone, and stopping it leaves
   overnight-c8.sh running (2026-10-03, a probe tree of bash, sh, sleep, cmd and ping: the walk
   listed the bash only, and its sleeps outlived it). And Windows never clears a dead parent's
   pid, so a process whose dead parent's pid was reused by the night would be taken for a child
   (59 of 460 processes had a dead parent, the owner's com.docker.backend.exe among them).
   nightabort.ps1 walks MSYS's own parent pids from the root, adds the native Windows children of
   what it found (a child only when created after its parent), refuses unless the root still is
   that shell (the MSYS pid maps to the logged Windows pid, and its command line names c8-night.sh
   or overnight-c8.sh), and lists before it stops:

   ```powershell
   $co = 'C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator'
   pwsh -NoProfile -File "$co/nightabort.ps1" -MsysPid <msys> -WinPid <windows-pid>         # list only
   pwsh -NoProfile -File "$co/nightabort.ps1" -MsysPid <msys> -WinPid <windows-pid> -Stop   # then stop
   ```

   Read the list first: the night's bash.exe and sh.exe, go.exe, *.test.exe, devtool, python,
   docker and the keep-awake pwsh, nothing else. `-Stop` stops the shells top-down first (a stopped
   shell starts nothing more), then the rest deepest first, each only while it is still the
   process listed.

   Then look again for orphans. A shell can start one between the listing and its own stop, and a
   night aborted another way (a closed window) leaves them all. Their command lines do not name a
   checkout: Go runs a test binary from its own go-build temporary directory with only `-test.*`
   flags, and go.exe and devtool get relative package paths. So list them by name and start time.
   `<start>` is the UTC time at the end of the start line used above; PowerShell reads a time that
   ends in `Z` as UTC and compares it in local time:

   ```powershell
   $t0 = [datetime]'<start>'      # for example '2026-10-05T03:30:12Z'
   Get-CimInstance Win32_Process | Where-Object {
       $_.Name -match '^(go|devtool|.+\.test)\.exe$' -and $_.CreationDate -gt $t0 } |
     Select-Object ProcessId, ParentProcessId, CreationDate, CommandLine | Format-List
   ```

   With the owner's go nothing else runs Go during the night (Before launch item 1), so these are
   the night's. Match each command line to the step that ran then (night.log,
   `prefreeze/summary.log`, chain.log): its package path, its row name, or a scratch clone path
   from step 4. Then stop only those, with `Stop-Process -Id <pid>`. Leave anything started before
   `<start>`, and anything whose command line names another worktree.
2. Delete `phase3/c8/keepawake.sentinel` (or the re-run's, step 7). A keepawake.ps1 that survived
   exits within 30 s, and it never re-creates a missing sentinel. A normal end, a refusal and a
   catchable signal already delete it and stop the keep-awake process through the exit trap.
3. Docker. In candidate 8's night the container runs twice: for the Linux lanes and for
   `c51-linux`. If chain.log's last `container start exit=0` line has no `container stopped` line
   after it, run `docker stop qompack-v6-linux-verification`. If the last `engine started by this
   chain` has no `engine stopped` after it, run `docker desktop stop`. Never stop an engine the
   chain did not start (`engine up at start=1`), because the owner's stack runs on it (D56(g)).
4. Scratch. Delete the paths night.log names (`merged-tree scratch clone <path>`) and chain.log
   names (`release-check clone <path>`, one per try) if they survived a hard kill. The v0.3.0 tag
   only ever existed in those clones. `git tag -l v0.3.0` in any worktree must print nothing.
5. Remove any `phase3/c8/quiet*/.quiet.lock` a hard kill left behind, and the quiet.sh scratch
   directory each `quiet*/quiet-run.txt` names on its `work=` line.
6. Git. If night.log has `candidate 8 frozen at <sha>` but no `pushed verify/v6`, then `verify/v6`
   holds an unpushed freeze commit. Its pre-freeze head is on the `integration <H>, verify/v6 <V>`
   line. Resetting to `<V>` is a coordinator decision, and it is recorded in the ledger. If the push
   happened, the candidate stands and only the overnight part is re-run.
7. Re-run.
   - Before the freeze: relaunch `c8-night.sh` as above.
   - After the freeze: run the overnight part alone on the frozen candidate into a fresh evidence
     directory (`phase3/c8-rerun-<n>`; overnight-c8.sh refuses one that already holds a night's
     records). `c8-night.sh` would refuse, because `qompack-bundles/c8` exists. Use the C5.2
     night's launch below with two changes: `$e` is `.../phase3/c8-rerun-<n>`, and the
     `$env:C8_C52_ONLY = '1'` line is left out (`C8_C52_STEPS` stays unset, which overnight-c8.sh
     checks; the final `Remove-Item` line stays). The overnight part alone takes about 6 h 15 min
     (the steps before release-check, then release-check), so launch it by `NIGHT_DEADLINE` minus
     7.5 h (00:30 for 08:00); release-check must start by `NIGHT_DEADLINE` minus 3 h, which
     chain.log's first line prints. Its abort is this list with chain.log's start line (step 1)
     and the re-run directory's sentinel (step 2).

### The C5.2 night

It runs on the frozen candidate after candidate 8's night has frozen it (night.log has
`candidate 8 frozen at <sha>`), on another night, with the owner's go: it uses the whole machine
too. `overnight-c8.sh` with `C8_C52_ONLY=1` runs these steps, each AC-gated (D57(d)), in this
order and nothing else:

- `c116-rig` (Windows): C1.16's load rig as w2-lifetime produced its distributions
  (`plans/sdd/V6-closeout/w2-lifetime/runs/08-17` and `36`), on the frozen candidate, with
  `QOMPACK_C116_ROUNDS=30 QOMPACK_C116_WORKERS=8 QOMPACK_C116_READ_BYTES=262144`:

  ```sh
  go test ./internal/cli/ -run '^TestSessionStartCompact_UnderSameSessionIngest$' -count=1 -v -timeout=30m
  ```

  once with no extra load (`p3-c116-rig-noextra`, as runs/17 and 36) and once with the rig's
  in-process co-load added, `QOMPACK_C116_FSYNC_COLOAD=16 QOMPACK_C116_CPU_COLOAD=4`
  (`p3-c116-rig-coload`, as runs/15). The old distributions are stale: before `3f2da1b3` the rig's
  Reads never reached its daemon, so the same-session ingest they describe never ran. The step
  passes when both runs pass (every compact answer the rehydration or the deferred note; every Read
  routed to the rig, the check `3f2da1b3` added) and each logged its n=30 distribution and its
  Read-routing line; chain.log carries both lines per run (`c116-rig noextra: …`,
  `c116-rig coload: …`). The numbers are recorded, not judged against a bound: compare them with
  runs/17 (p99 188 ms) and runs/15 (p99 278 ms) in the ledger. The external fsync generator of
  runs/09 and 16, the source of docs/architecture.md's 1.85 s to 0.66 s, was never committed,
  so that condition has no reproduction. It goes first, so C1.16 never waits behind C5.2. Its
  estimate, `C116_EST_S` (65 min), is an upper bound (two `-timeout=30m` runs and the build): the
  runs it reproduces took under a minute each, but none has run with its Reads reaching the daemon.
- The C5.2 chunks, Windows first, then Linux: C5.2's full list (D62(b), `C8_C52_SET=full`, the
  default), every row of quiet.sh's list (65 rows in 19 packages) against `cf31e01`, in eight
  chunks, one per OS and package group. A chunk is one quiet.sh run with its own `QUIET_PKGS`, its
  own 10 ABBA rounds and its own directory `quiet-<chunk>/`, so its evidence stands on its own,
  whichever night measured it. The groups are the three longest packages and the rest, read from
  quiet.sh's own list, so the chunks' packages together are exactly the list's:

  | Chunks | Packages | Estimate, Windows | Estimate, Linux |
  |---|---|---|---|
  | `c52-win-observer`, `c52-linux-observer` | observer | 93 min | 61 min |
  | `c52-win-store`, `c52-linux-store` | store | 65 min | 71 min |
  | `c52-win-checkpoint`, `c52-linux-checkpoint` | checkpoint | 52 min | 56 min |
  | `c52-win-other`, `c52-linux-other` | the other 16 | 42 min | 38 min |

  Each estimate is `C52_EST_WIN` or `C52_EST_LINUX` (candidate 5's measured time for the chunk's
  packages) plus 10 min. chain.log says `c52: the FULL C5.2 list is measured: all 65 rows …`, then
  one line per chunk with its `QUIET_PKGS` and estimate, then the night's total.

Why chunks. The charger cuts AC unattended at about 90-100 % charge and restores it at about
35-40 % (D57(d)). The System log (Kernel-Power 105) shows on-AC stretches of 1 h 26 min, 6 h 6 min
and 2 h 57 min in the last three loaded nights, and the two cuts inside a loaded night lasted
1 h 26 min and 1 h 18 min. Either OS's whole list (about 4 h) would often not fit in one stretch.
A cut now voids one chunk, at most about 93 min, and that chunk is retried once when AC returns;
the chunks before it keep their evidence.

The deadline. The rig's bound (65 min) and the chunks (4 h 12 min on Windows, 3 h 46 min on
Linux) come to 9 h 3 min with no AC cut. **Launch by `NIGHT_DEADLINE` minus 9.5 h (22:30 for
08:00)**: the last 27 min absorb the container starts and a short AC wait. A chunk starts only
when its estimate lets it end by the deadline (its latest start is the deadline minus its estimate
in the table); otherwise it is SKIPPED, and the next chunk is tried. With the charger's cutoff
active, expect a cut. It costs the chunk it voids plus the time on battery until AC returns: about
1 h 20 min under load, and longer while the night waits idle, because a C5.2 night has no
AC-independent step to run meanwhile. After `AC_WAIT_BUDGET_MIN` (default 180) minutes of waiting
in one night, a chunk runs on battery as NOT-REFERENCE, which measures nothing. So one night may
leave chunks unmeasured, and a further C5.2 night measures exactly those (below). Disabling the
cutoff for the night, if the owner can, avoids that. An earlier launch the owner allows absorbs a
cut (`NIGHT_MAX_AHEAD_H` allows a launch from 16:00 for an 08:00 deadline). A deadline more than
16 h away is refused, as in candidate 8's night.

Launch, from a PowerShell window (the same rules as candidate 8's launch above):

```powershell
$v6   = 'C:/Users/Quant/Documents/Programming/Projects/qompack-v6'
$co   = "$v6/plans/sdd/V6-closeout/coordinator"
$e    = "$v6/plans/sdd/V6-closeout/phase3/c8-c52"   # a fresh directory per night: c8-c52-2, ... after it
$cand = 'C:/Users/Quant/Documents/Programming/Projects/qompack-cx-cand'
$sha  = (Select-String -Path "$v6/plans/sdd/V6-closeout/phase3/c8/night.log" `
          -Pattern 'candidate 8 frozen at ([0-9a-f]{40})' | Select-Object -Last 1).Matches[0].Groups[1].Value
$sha                                                   # must print the 40-character frozen SHA
git -C $cand checkout -q --detach $sha; git -C $cand status --porcelain   # must print nothing
New-Item -ItemType Directory $e | Out-Null; New-Item -ItemType File "$e/keepawake.sentinel" | Out-Null
$env:NIGHT_DEADLINE = '08:00'
$env:C8_C52_ONLY = '1'
# $env:C8_C52_STEPS = 'c52-win-store c52-linux-other'   # a further C5.2 night only: the steps it measures
Start-Process pwsh -WindowStyle Hidden -ArgumentList @('-NoProfile', '-File', "$co/keepawake.ps1", "$e/keepawake.sentinel")
Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden -ArgumentList @(
  "$co/overnight-c8.sh", $cand, $sha, $e)
Remove-Item Env:C8_C52_ONLY, Env:C8_C52_STEPS, Env:NIGHT_DEADLINE -ErrorAction SilentlyContinue   # the night keeps its copy
```

overnight-c8.sh refuses (exit 2, writing nothing) a SHA that is not 40 characters, a checkout not
at it or not clean, an evidence directory that holds a night's records, a `C8_C52_ONLY` other than
empty or `1`, a `C8_C52_SET` other than `full` or `derived`, a `C8_C52_STEPS` name that is not one
of the night's nine steps (or `C8_C52_STEPS` without `C8_C52_ONLY=1`), and a quiet.sh whose list
it cannot read. It holds no keep-awake of its own: delete `$e/keepawake.sentinel` when chain.log's
`done:` line appears.

Watch, under `phase3/c8-c52/`: `chain.log` (first line `start candidate=<sha> pid <msys> winpid
<windows-pid> … mode=c52-only c52_set=full steps=[…]`), `p3-c116-rig-noextra.{json,log}` and
`p3-c116-rig-coload.{json,log}`, one `quiet-<chunk>/` per chunk (`c52-names.tsv` and
`quiet-run.txt` in it; `paired.txt`, `completeness.tsv` and the benchstat logs in its `c52-win/`
or `c52-linux/`), `power.tsv` and `overnight-outcome.txt`. Exit 0 means every step the night ran
passed VALID.

A further C5.2 night. A step is done when one of its tries, in any C5.2 night on this candidate,
is VALID with exit 0. This lists the done steps:

```sh
awk -F'\t' 'FNR > 1 && $4 == "VALID" && $3 == "0" { print $1 }' plans/sdd/V6-closeout/phase3/c8-c52*/power.tsv | sort -u
```

A VALID try with a non-zero exit is a real red: classify it in the ledger before measuring that
chunk again. Every other step that is not done (SKIPPED, INVALID-POWER on both tries,
NOT-REFERENCE) goes into `C8_C52_STEPS` for the next C5.2 night, which is launched as above into a
fresh `$e`. Each chunk's evidence then sits in the night that measured it VALID.

Abort:

1. The pids from chain.log's `start candidate=<sha> pid <msys> winpid <windows-pid>` line, then
   `nightabort.ps1` exactly as in candidate 8's Abort step 1 (list, read, `-Stop`). For orphans,
   use the listing there with chain.log's start time. The rig's go.exe and its `cli.test.exe`
   carry its row name, `TestSessionStartCompact_UnderSameSessionIngest`, on their command lines.
   quiet.sh's builds and benchmark binaries carry the quiet.sh scratch directory of step 4 (the
   build's `-o <work>/bin-win/…`, the binary's own path under `<work>/bin-win/`). Stop only those.
2. Delete `phase3/c8-c52/keepawake.sentinel`.
3. Docker: the container runs once for each Linux chunk. If chain.log's last
   `container start exit=0 (c52-linux-<group>)` has no `container stopped` line after it, run
   `docker stop qompack-v6-linux-verification`; if the last `engine started by this chain` has no
   `engine stopped` after it, run `docker desktop stop`. Never stop an engine the chain did not
   start (`engine up at start=1`).
4. Remove any `phase3/c8-c52/quiet-*/.quiet.lock` a hard kill left, and the scratch directory each
   `quiet-*/quiet-run.txt` names on its `work=` line (its clones and test binaries).
5. Nothing else: the C5.2 night makes no clone of its own outside quiet.sh's, no tag, no commit
   and no push.
6. Re-run: a further C5.2 night (above) into a fresh `$e`, with `C8_C52_STEPS` naming the steps
   that are not done; the aborted step's try is not done.

### The live re-check

Generate it only when the night's evidence allows the candidate to stand, because its 25 real
sessions come out of an owner's budget that is already exceeded (D53(g)):

- `overnight-outcome.txt` shows `failed=0`;
- every INVALID-POWER, NOT-REFERENCE or SKIPPED step is re-run (Abort step 7) or dispositioned in
  the ledger;
- every C5.2 night step (`c116-rig` and the eight chunks) is done, VALID with exit 0 in a C5.2
  night on the frozen candidate (the `awk` above lists them), or its rows are dispositioned in the
  ledger (before D62(c) a C5.2 that candidate 8's night SKIPPED needed the same);
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
