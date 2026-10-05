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
| `quiet.sh` | `sh quiet.sh <candidate-repo> <base-rev> <evidence-dir> <step…>`: Phase 5's quiet benchmark runs, strictly sequential, no co-load, after Phase 3 on an idle host (steps: `c51-win`, `c51-linux` for C5.1 bench-hotpath 5000 iterations plus TestBudgetBF for B-F; `c52-win`, `c52-linux` for C5.2, the carried and D37 benchmarks against `<base-rev>` = `cf31e01` in 10 balanced ABBA rounds (above C5.2's `-count 5`), with pinned benchstat, a paired table and a per-row completeness check that fails on skipped or short benchmarks). Windows runs go through `recrun.sh` with an isolated home for every benchmark binary, Linux through the non-root gate. It refuses a candidate repo with tracked changes and warns when the host is not idle (its host queries bounded). Row 1.1.27 is judged by its absolute budget only (`absolute_rows`, D67(j): `absolute-budget.tsv`, no ratio). It removes its scratch directory after a pass and keeps it, named in `quiet-run.txt`, after a failure. `QUIET_*` variables shrink it for a dry run. A `homeguard.py` check brackets the run. |
| `wave5.js` | Wave 5: cold start (D17), home-directory refusal (D18), Windows file semantics, shutdown helpers, directory-fsync redundancy (D20), dependency bumps. |
| `live-uat.js` | **Not yet run.** The Phase 4 live lane: four strictly sequential agents (install, sessions, retrieval, recovery; 8/12/12/4 = 36 real Haiku sessions, which with C5.5's 40 pre-registered trials and the 6 smoke/pilot sessions already used comes to about 82 against D3's ~40-80) covering C4.1–C4.11, C1.6, C1.7 and C5.6 on the owner's host (D3: agent-executed, never human UAT), then an independent read-only evidence and coverage audit. A failed real-home guard stops the lane. Before launch: merge `closeout/w7-livelane` into the candidate, create worktree `../qompack-cx-live` (branch `closeout/live`) at the frozen candidate, and replace `__CANDIDATE__` and `__BUNDLE__` (the script refuses to run otherwise). |
| `homeguard.py` | Real-home guard the live lane runs around every session and plugin step: `snap`/`check`/`clean` over `~/.claude/settings.json`, `plugins/installed_plugins.json` and `plugins/known_marketplaces.json` (SHA-256), the entry names of `plugins/{cache,marketplaces,data}`, and a name listing of `~/.qompack`. It never opens credentials. Self-test: `python homeguard_test.py` (fake home). |
| `live_driver.py` | The live lane's stream-json session driver (from `eval/runs/smoke_driver.py`): one message per turn, optional `before` argv per turn, host-seen hook arrival deltas (`hooks.json`, approximate; pairs flagged `measured: false` are not measured), planted-secret scrubbing of every output (`secrets_file`) and a `scan-staged` pre-commit secret gate, daemon RSS/CPU and `.qompack/` size per turn (`meta.json`). Self-test: `python live_driver_test.py` (fake host, no model). |
| `wfreport.py` | Renders a workstream's `report.md` from a workflow journal: `python wfreport.py <journal.jsonl> <ws-label> <run-id> <title> <out.md> [branch]`. Subagents cannot write report files, so the coordinator commits these. |
| `recrun.sh` | Records a run the V6 way: `sh recrun.sh <repo> <evidence-dir> <run-id> -- <command…>` writes `<id>.json` (argv, head, dirty, env, times, exit, log sha256) and `<id>.log`, and refuses to overwrite. |
| `shacheck.sh` | `sh shacheck.sh <worktree> <file>`: every quoted SHA must be reachable from HEAD (report-SHA reachability). |
| `rpwaive.py` | Applies `runpatterns` inline waivers from `devtool lint --only=runpatterns` output. It auto-handles alternations the checker splits at a bare pipe and `<placeholder>` patterns, and takes an explicit `file:line=reason` for anything else. Correct a quote instead when its only problem is trailing punctuation. |
| `c8-night.sh` | Candidate 8's night: preconditions (all checked before anything runs, a deadline too near or too far and the free disk among them), the merged-tree check, the pre-freeze check, the freeze, bundles, push, then `overnight-c8.sh` with the night's deadline as an epoch, through release-check and whichever C5.2 chunks fit. See "Candidate 8" below. |
| `overnight-c8.sh` | The frozen candidate's local night: AC-gated Windows timing with D57(d)'s power verdicts and per-try records, a power record for every step, Windows -race and bundles, the Linux lanes, quiet C5.1 on both OSes (Linux report only), then release-check in an isolated scratch clone under a watchdog, then (`C8_NIGHT1_C52`, default on) the C5.2 chunks that AC and the time left allow, each step started only when its estimate ends by the deadline. With `C8_C52_ONLY=1` it runs the C5.2 night instead (D62(c)): C1.16's rig, then C5.2 on both OSes in AC-gated chunks by package group, never on battery (D65(c)), by default the full list (`C8_C52_SET=full`, D62(b); `derived` measures c52derive.py's selection); `C8_C52_STEPS` runs only the steps it names. It refuses a checkout that is not exactly the candidate and clean, an evidence directory that already holds a night's records, a mistyped switch and a drive short of room. Its header states every rule, Docker engine ownership and signals among them. |
| `prefreeze.sh` | `sh prefreeze.sh <repo> <evidence-dir> [step…]`: D53(a)'s pre-freeze check (gate, e2e, e2efunc, hotpath, integration, testpkgs, internal). A fresh `summary.log` per run, every line tagged with `PREFREEZE_RUN`, and a power verdict per step. e2efunc skips test/e2e's timing rows, named exactly in its header and judged by the night on AC, then runs their functional arms by themselves; `sh prefreeze.sh --e2e-skips <repo>` prints its `-skip` pattern, or exits 2 when the names have drifted from the tree or from ci.yml's timing lane. |
| `power.sh` | Sourced by the three scripts above: the power reading, the System log's power events (Kernel-Power 105, 506, 42 and 107), the VALID / INVALID-POWER / NOT-REFERENCE verdict over a run's start and end readings, the bounded AC wait (no budget for a C5.2 chunk, D65(c)), the deadline and its daytime-launch guard, `RC_EST_S`, the free-disk check and govulncheck's two patterns. Every PowerShell query runs under a timeout. |
| `w2lt-stress/main.go.txt`, `w2lt-stress/go.mod.txt` | The external fsync and CPU co-load generator of w2-lifetime's C1.16 runs/09 and 16, run there with 16 writers of 64 KiB, 4 CPU spinners and 150 s on C: (runs/09's header; its `-k`, `-size`, `-c` and `-for` flags). It prints `writes <n>` (37446 in runs/09, 49960 in runs/16). It was never committed with those runs. These are its source files, byte for byte, recovered from the coordinator session's scratchpad (`w2lt-stress/`, written 2026-09-25 17:27, before runs/09). Built there with go1.26.4, they reproduce that session's `w2lt-stress.exe` exactly (sha256 `5d49a71ec8886fb0c66f4feeadb113455ee3ad0595dfd4635133888827d9c4f1`). Kept as `.txt`, as plans/ keeps other probe programs, so no Go tool reads them: to build, copy both into an empty directory as `main.go` and `go.mod`, then run `go build`. No night runs it (README "The C5.2 night"). |
| `nightabort.ps1` | `pwsh -File nightabort.ps1 -MsysPid <msys> -WinPid <windows-pid> [-Stop]`: lists, and with `-Stop` stops, exactly one night's process tree (README "Candidate 8", Abort step 1). It walks MSYS's own parent pids from the night's shell (Windows records a dead parent for everything an MSYS shell starts), adds their native Windows children (a child only when created after its parent), and refuses unless the root still is the logged c8-night.sh or overnight-c8.sh shell. |
| `stamped.sh` | `sh stamped.sh <command…>`: prefixes each output line with its epoch second and keeps the command's exit status; release-check's AC-sensitive windows are read from it. |
| `nightharness.sh` | `sh nightharness.sh [case…]` (`-l` lists them): the dry harness for everything above (112 cases). Stubs for powershell, pwsh, docker, go, claude, gh, timeout, date, sleep and df, real git and python on scratch repositories, a fake clock. Every case first checks that each stub name resolves to the stub directory, and refuses to run otherwise; the harness takes its scratch path in POSIX form and refuses one with a colon (a `C:/` TMPDIR used to split PATH at the drive colon, so every stub was bypassed and cases ran against the real Docker, gh and go: audit 2's #48). While that guard holds, nothing outside its temporary directory is touched (the Q cases run the real quiet.sh with HOME and USERPROFILE in a scratch home). The real processes outside the stubs are K1's keepawake.ps1 with nothing to hold, and K2's probe tree (sh, sleep, cmd and ping, under a shell named c8-night.sh), which the real nightabort.ps1 lists and stops. Run it after any change to a night script. |
| `c52derive.py` | `python c52derive.py <candidate-repo> <base-rev> <out-dir>`: D57(e) by construction. Traces each C5.2 benchmark on the candidate at its own listed benchtime with a coverage profile and selects those whose executed product files, the other changed files of a package they execute (declarations have no coverage block), own benchmark file, fixtures or adjacent assets changed since `<base-rev>`; reports, without selecting on it, whether an executed block covers a changed line; writes `report.txt`, `selection.tsv`, `pkgs.txt` and `filter.txt` for quiet.sh. A failed trace is selected, fail-closed; when every trace fails, or a stray .go file would join the trace, it writes no selection (exit 2) and overnight-c8.sh measures the full C5.2 list. overnight-c8.sh runs it only with `C8_C52_SET=derived`: candidate 8 measures the full list (D62(b)). Self-test: `python c52derive.py --selftest`. |
| `mkrecheck8.py` | `python mkrecheck8.py live-rerun-c7.js <out.js> <candidate-sha> <bundle-dir>`: generates candidate 8's live re-check from candidate 7's lane, with the diff from candidate 7 that its D53(f) carry-forward notes are checked against. Its parts judge against D58-D67: the D63/D64 whitelist (docs/security.md and ADR 0011 section 23 items 5-10 join the reading list, and D64(4)'s accepted over-withholding is not a finding), D62(f)'s section 2, D62's forkwork (a /compact in the fork after the six slash commands) and stable fsck and eval reads, and C4.5's two-read comparison after removing the named age and timestamp fields (data.collected_at_ms, provenance age_ms, the status header's collected time, doctor's "persisted <age> ago"). It refuses a candidate without ADR 0011 section 23, a bundle without BUNDLE.json, and an output that keeps a candidate 7 string, reads the clock or does not parse. |

Conventions: Linux gates use `plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh` with Windows-style
paths for `--repo` and `--out` (a POSIX `/c/...` path is refused). Never SendMessage a running
workflow agent: it resumes a duplicate in the same worktree.

## Candidate 8

Candidate 8 takes two nights (D62(c)). Candidate 8's night, `c8-night.sh`, freezes the candidate
and tests it through release-check by its deadline (D60(f), D61). If the machine is then on AC and
time is left, it runs C5.2 chunks too, in order, each one only when its estimate ends by the
deadline (D65(b): each chunk is a self-contained ABBA comparison), so the C5.2 night may owe only
the rig. The C5.2 night, `overnight-c8.sh` alone with `C8_C52_ONLY=1`, follows on the frozen
candidate: C1.16's load rig, then whatever is left of the full C5.2 list on Windows and Linux
(D62(b)), in chunks a single AC cut cannot void together. If a C5.2 night leaves a step unmeasured,
a further C5.2 night measures only that step. After the nights come the live re-check and the
close-out steps ("After the nights", below). Everything below is the coordinator's procedure; no
agent launches or aborts either night.

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
   `C8_EXEMPT` (space-separated), and each exemption is logged with its tip. Wave 22's branches
   (`closeout/w22-*`) are checked like every other. The three old branches that held commits
   neither candidate 7 nor integration had, `closeout/w15-docs` (`21c07942`), `closeout/w15-ledger`
   (`12817cd0`) and `closeout/w15-services` (`c212712d`), are merged into integration through wave
   22's w15carry seat (D67(a)), so they need no exemption once that seat's merge is in. A branch's
   tip counts as merged only when it is an ancestor of integration: if a w15-docs commit is
   recorded as superseded rather than merged, merge the branch itself with that record (its tip is
   then an ancestor) or name it in `C8_EXEMPT` and cite D67(a)'s supersession record in the ledger.
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
5. `sh nightharness.sh` passes (112 cases), every case behind its stub guard (a case whose stubs
   do not all resolve to the harness's own directory fails without running). It is dry, with no
   Go, Docker or Claude Code process while the guard holds (K1 starts the real pwsh once, with
   nothing to hold; K2 starts a probe tree of sh, sleep, cmd and ping and stops it with
   nightabort.ps1). Its run time depends on the machine's load: 80 cases took 2447 s on 2026-10-04
   at night, 57 took 6512 s on 2026-10-03 with other seats running, and 36 took about 1 h 40 min
   under audit 2's load; a sequential 112-case run takes 2-6 h. Run it at night, or split the cases
   over two or three processes (each makes its own scratch directory; `-l` lists the names to
   pass). Re-run it after any change to a night script, and `python c52derive.py --selftest`.
6. The laptop lid is open and the charger is connected. Docker Desktop may be up or down, and the
   container `qompack-v6-linux-verification` must exist. The night decides whether the engine was
   up at its start by three probes, and treats an engine as its own only when it was down then,
   `docker desktop status` said `stopped` just before the night started it, and nothing but the
   night's container runs on it when it would stop it (overnight-c8.sh's header, "Docker"; audit
   2's #47). The owner's engine, and any engine the owner's containers run on, is never stopped.
   TMPDIR's drive and GOCACHE's need `NIGHT_MIN_FREE_GB` (40) GiB free, or both scripts refuse:
   the drive had 54-65 GB free on 2026-10-04, and each quiet.sh run now removes its own scratch
   directory after a pass.
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
   - release-check, 3 h (`RC_EST_S`, power.sh). It must start by `NIGHT_DEADLINE` minus 3 h,
     which chain.log's first line prints. `RC_EST_S` has never been measured with release-check's
     current step list (w17-release's estimate rests on SP-17's 8451 s on a smaller tree), so a
     run still going at the deadline plus `RC_GRACE_S` (30 min) is stopped and recorded
     SKIPPED-OVERRUN, neither a pass nor a fail, and never retried. On battery, release-check first
     waits for AC (within the budget) until its latest start, then runs either way. The wait polls
     once a minute, so it ends at the first poll at or after the latest start and the run may start
     up to a poll late (chain.log says by how much); the estimate is not checked again after the
     wait, and the watchdog bounds the end.
   - then, with time left and AC at that moment, the C5.2 chunks in order (`C8_NIGHT1_C52`,
     default on; `0` turns it off): each chunk starts only when its estimate (the table under "The
     C5.2 night") ends by the deadline, and never on battery. A chunk done here (VALID, exit 0)
     counts as passed and need not run again; a VALID red counts as failed and must be classified;
     a chunk that did not fit, found no AC or ended INVALID-POWER twice is left for the C5.2 night
     and counts neither way.

   Every other step also starts only when its estimate, from candidate 6's night, ends by the
   deadline (`STEP_EST` in overnight-c8.sh: win-timing 15 min, win-e2e-timing 35, win-x11-alone
   15, win-race 50, bundles 15, the Linux lanes 15 to 45 each, c51-win 10, c51-linux 20);
   otherwise it is SKIPPED.

   **Launch by `NIGHT_DEADLINE` minus 8.5 h (23:30 for 08:00), or release-check is SKIPPED and
   C3.12 and D57(a)'s local reference run stay unmet.** The pre-freeze and the steps before
   release-check take about 4 h 15 min and release-check 3 h; the last hour absorbs a slow step or
   an AC wait, and any time left goes to C5.2 chunks. `c8-night.sh` logs a WARNING when it is
   launched later than that, and refuses a launch less than 90 min before the deadline
   (`NIGHT_MIN_AHEAD_MIN`) unless `NIGHT_ALLOW_LATE=1`: the freeze would otherwise be spent on a
   night whose every step is SKIPPED. The C1.16 rig is not part of this night: chain.log's last
   lines say `left for the C5.2 night (…): c116-rig …`, and overnight-outcome.txt ends with
   `pending_c52_night=[c116-rig …]`, naming the rig and every chunk this night did not finish;
   they count neither for nor against its exit status. `AC_WAIT_BUDGET_MIN` (default 180) bounds
   the whole night's waiting for AC, counted in one-minute polls, and every wait comes out of the
   same night (a C5.2 chunk's wait for AC is not bounded by it, D65(c), below).

   test/e2e's whole-package budget. `win-e2e-timing` runs all of test/e2e in one binary, and the
   binary took 1513 s (candidate 5) and 1529 s (candidate 6), 84-85 % of the old 30-minute
   `-timeout`, which earlier runs hit (1800.4 s twice). A timeout is a red with no wall-clock
   meaning, so phase3.sh's lane now has `-timeout=45m`, which changes no judgement. Audit 2's #84
   gives ci.yml's `test-e2e` job the same 45m through wave 22's cliwork seat; that seat's commit
   was not visible when phase3.sh was changed, so check that the two agree before launch, and the
   harness's F5 checks phase3.sh's exact line.

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
  pid <pid>`, then `keep-awake held (keepawake.log)`, or a WARNING when keepawake.ps1 did not say
  it holds within 15 s (the night still runs; a host sleep would show as INVALID-POWER). The
  preconditions include `precondition: e2efunc will skip -skip '<pattern>'` and the free space of
  each drive (`precondition: disk: …`).
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
  (release-check, c116-rig, c52-derive, a C5.2 chunk, `STEP_EST` for the others) would have
  ended after it, or, for a C5.2 chunk, no AC came by its latest start (D65(c)).
- SKIPPED-OVERRUN: release-check was still running at the deadline plus `RC_GRACE_S` and was
  stopped. Neither a pass nor a fail; `RC_EST_S` is too small for the tree, and the run's log ends
  where it stopped.
- A red release-check whose govulncheck section says the vulnerability database or the module
  proxy could not be reached is labelled `govulncheck unreachable (network), not a product red`
  and counts NOT-REFERENCE (prefreeze.sh's gate reads the same patterns as GOVULNCHECK-UNREACHABLE
  and does not fail on them). release-check stopped there, so its later steps never ran: re-run it
  (Abort step 7), or rely on hosted ci.yml's security job and record the ruling.

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
   catchable signal already delete it and stop the keep-awake process through the exit trap. A
   TERM, INT or HUP sent to the night's shell runs that trap at once (every long child runs in the
   background while the shell waits, audit 2's #51): the shell stops its running child, removes
   its scratch clone and exits 143, 130 or 129, and night.log or chain.log says
   `signal: stopped the running child (pid …)`. That child's own children (go test binaries,
   docker exec) may outlive it, so a signal is no substitute for step 1: list and stop the tree
   with nightabort.ps1, then look for orphans as step 1 says.
3. Docker. In candidate 8's night the container runs for the Linux lanes, for `c51-linux` and for
   each Linux C5.2 chunk the night ran. If chain.log's last `container start exit=0` line has no
   `container stopped` line after it, run `docker stop qompack-v6-linux-verification`. If the last
   `engine started by this chain` has no `engine stopped` or `engine left running` after it, run
   `docker ps` first: stop the engine (`docker desktop stop`) only when no container but
   `qompack-v6-linux-verification` runs. Never stop an engine the chain did not start (`engine up
   at start=1`, or a start logged as `not started by this chain` or `the owner's engine`), because
   the owner's stack runs on it (D56(g)).
4. Scratch. Delete the paths night.log names (`merged-tree scratch clone <path>`) and chain.log
   names (`release-check clone <path>`, one per try) if they survived a hard kill. The v0.3.0 tag
   only ever existed in those clones. `git tag -l v0.3.0` in any worktree must print nothing.
5. Remove any `.quiet.lock` a hard kill left in the night's evidence directory's `quiet*/`
   directories (`phase3/c8/quiet*/` for candidate 8's night, `phase3/c8-rerun-<n>/quiet*/` for a
   re-run of its overnight part, step 7), and the quiet.sh scratch directory each of their
   `quiet-run.txt` names on its `work=` line (quiet.sh removes it after a pass; it is left after a
   failure, and quiet-run.txt then says `kept: a step failed`).
6. Git. If night.log has `candidate 8 frozen at <sha>` but no `pushed verify/v6`, then `verify/v6`
   holds an unpushed freeze commit. Its pre-freeze head is on the `integration <H>, verify/v6 <V>`
   line. Resetting to `<V>` is a coordinator decision, and it is recorded in the ledger. If the push
   happened, the candidate stands and only the overnight part is re-run. If the night refused after
   the freeze, at the bundle build or host validation (a REFUSED line after `candidate 8 frozen
   at`, naming host-validate.txt, an outcome other than accepted, or a missing bundle file), step
   7's "Frozen, then refused" applies.
7. Re-run.
   - Before the freeze: relaunch `c8-night.sh` as above.
   - Frozen, then refused at the bundles or host validation (audit 2's #58). `c8-night.sh` cannot
     be relaunched: `qompack-bundles/c8` exists, and verify/v6 already holds the freeze. Keep the
     candidate (`<sha>`, night.log's `candidate 8 frozen at` line) and finish its step 5 by hand,
     with the owner's go, from a PowerShell window:
     1. Read `phase3/c8/host-validate.txt` and fix the cause (an `unverified` outcome means built
        but not validated, for example `claude` not on PATH; `rejected` is a real red, which the
        coordinator rules on before going further).
     2. Move the unvalidated bundle aside, never into a path a later step reads:
        `Move-Item <Projects>/qompack-bundles/c8 <Projects>/qompack-bundles/c8.refused-<n>`.
     3. Rebuild and host-validate from the candidate worktree, which must be at `<sha>` and clean
        (`git -C <Projects>/qompack-cx-cand rev-parse HEAD` prints `<sha>`;
        `git -C <Projects>/qompack-cx-cand status --porcelain` prints nothing), into the night's
        file name: `cd <Projects>/qompack-cx-cand; go run ./tools/devtool bundle -archive -version
        0.3.0 -host-validate -out <Projects>/qompack-bundles/c8 *> <v6>/plans/sdd/V6-closeout/phase3/c8/host-validate-2.txt`.
        Its `"outcome"` must read `accepted`, and the six `qompack-plugin-0.3.0-<target>/bin/`
        binaries, `windows-amd64/BUNDLE.json` and `checksums.txt` must exist, as the night checks.
     4. Push the freeze, bounded and without a prompt: `$env:GIT_TERMINAL_PROMPT = '0';
        git -C <v6> push -q origin verify/v6` (hosted ci.yml starts), then dispatch nightly.yml:
        `gh workflow run nightly.yml --repo AidanHT/qompack --ref verify/v6` (D4).
     5. Record in the ledger that step 5 was finished by hand, with host-validate-2.txt's outcome,
        then run the overnight part alone, as "After the freeze" below says. The overnight part
        refuses nothing for a missing night.log line, so its `start candidate=<sha>` line is the
        re-run's record.
   - After the freeze: run the overnight part alone on the frozen candidate into a fresh evidence
     directory (`phase3/c8-rerun-<n>`; overnight-c8.sh refuses one that already holds a night's
     records). `c8-night.sh` would refuse, because `qompack-bundles/c8` exists. First clear the
     C5.2 night's switches, as candidate 8's launch does:
     `Remove-Item Env:C8_C52_ONLY, Env:C8_C52_SET, Env:C8_C52_STEPS -ErrorAction SilentlyContinue`.
     overnight-c8.sh cannot tell a leftover `C8_C52_ONLY=1` from a C5.2 night's switch, because 1
     is a valid value, so the re-run would silently become a C5.2 night (chain.log's start line
     would say `mode=c52-only`). Then use the C5.2 night's launch below with two changes: `$e` is
     `.../phase3/c8-rerun-<n>`, and the `$env:C8_C52_ONLY = '1'` line is left out (`C8_C52_STEPS`
     stays unset, which overnight-c8.sh checks; the final `Remove-Item` line stays). Its refusal
     check (chain.log a minute after launch, `launch.err`) applies as written there. Check that
     chain.log's start line says `release-check must start by`, not `mode=c52-only`. The overnight
     part alone takes about 6 h 15 min (the steps before release-check, then release-check), so
     launch it by `NIGHT_DEADLINE` minus 7.5 h (00:30 for 08:00); release-check must start by
     `NIGHT_DEADLINE` minus 3 h, which chain.log's first line prints. Its abort is this list with
     chain.log's start line (step 1), the re-run directory's sentinel (step 2) and its `quiet*/`
     directories (step 5).

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
  runs/17 (p99 188 ms) and runs/15 (p99 278 ms) in the ledger. The night does not re-run the
  third condition, the external generator of runs/09 and 16 (runs/09's header: 16 goroutines each
  writing 64 KiB with write, fsync and rename in a loop on C:, plus 4 CPU spinners, for 150 s, in a
  process of its own). Its program was not committed with those runs; it is now, verbatim, as
  `w2lt-stress/` (the table at the top says how it was recovered). Re-running that condition on the
  frozen candidate is an owner decision, not part of this night, so docs/architecture.md's figure
  from that pair needs a ruling ("The docs figure", below). The step goes first, so C1.16
  never waits behind C5.2. Its estimate, `C116_EST_S` (65 min), is an upper bound (two
  `-timeout=30m` runs and the build): the runs it reproduces took under a minute each, but none
  has run with its Reads reaching the daemon.
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
  one line per chunk with its `QUIET_PKGS` and estimate, then the night's total. A chunk candidate
  8's night already finished (VALID, exit 0, in `phase3/c8/power.tsv`) is not measured again: its
  overnight-outcome.txt's `pending_c52_night=[…]` lists what is left, and the first C5.2 night's
  `C8_C52_STEPS` names exactly those steps (the rig always among them).

  Row 1.1.27 (`BenchmarkHookNoop_InProcess`) is judged by its absolute budget only, under 3 ms/op
  on candidate 8 (D67(j)): since `fbb32c13` the benchmark spools into a temp dir, while cf31e01's
  copy spools into its clone, so its ratio against cf31e01 does not compare like for like. quiet.sh
  prints no ratio for it: paired.txt's line for it reads `candidate <median> against its absolute
  budget 3 ms: PASS` (or `OVER-BUDGET`), and `absolute-budget.tsv` beside it holds the same verdict
  (quiet.sh's `absolute_rows`).

The docs figure. docs/architecture.md section 7 (line 714 at integration `67e0fddb`, line 721 at
`2bf29705`) said:
"Measured with `internal/cli`'s `TestSessionStartCompact_UnderSameSessionIngest` under concurrent
same-session ingest and an fsync co-load, the compact answer's p99 went from 1.85 s to 0.66 s
(`plans/sdd/V6-closeout/w2-lifetime/runs/`)". Those are runs/09 and 16 (w2-lifetime/report.md's
table, "Same external fsync co-load"). Both ran before `3f2da1b3`, so the same-session ingest the
sentence names never happened in them (D62(c)). This night re-measures neither figure: 1.85 s is
the tree before the fix, and the night does not run the generator (above). The in-process
co-load's figure after the fix (runs/15, p99 278 ms) is a different condition and does not stand
in for 0.66 s. Before the live re-check, a docs change (a seat the coordinator dispatches) does one
of two things, and the coordinator records the ruling in the ledger:

- restate the sentence from `p3-c116-rig-noextra` and `p3-c116-rig-coload` on the frozen
  candidate, naming their conditions (no extra load; the rig's in-process co-load) and giving no
  figure from before the fix, since none was measured with the Reads reaching the daemon; or
- mark the figure as measured before `3f2da1b3`, by a rig whose Reads never reached its daemon.

Marking needs no number from this night, so it went into candidate 8 before the freeze: the
figure is marked in integration (`458b583a`, D65(a)). What is left is the restatement from the
C5.2 night's `c116-rig` runs (D65(a)), which goes into a descendant whose changes reach no bundle
(D58(e)), with the ruling in the ledger. docs/ is
in no bundle: candidate 7's BUNDLE.json lists only the plugin manifest, `.mcp.json`, `bin/`,
`commands/`, `hooks/`, `LICENSE` and `THIRD_PARTY_NOTICES.md`.

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
AC-independent step to run meanwhile. A chunk never runs on battery (D65(c)): it waits for AC past
`AC_WAIT_BUDGET_MIN`, until its latest start, and is otherwise SKIPPED (`no AC by its latest
start`) and left for the next C5.2 night; it is never run NOT-REFERENCE. (The rig is not a chunk:
after the budget it runs on battery, NOT-REFERENCE, as any gated step.) So one night may leave
chunks unmeasured, and a further C5.2 night measures exactly those (below). The charger's AC
cutoff is the owner's hardware, and the night changes no system setting (D65(c)). An earlier
launch the owner allows absorbs a
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
New-Item -ItemType Directory -Force $e | Out-Null; New-Item -ItemType File "$e/keepawake.sentinel" | Out-Null
$env:NIGHT_DEADLINE = '08:00'
$env:C8_C52_ONLY = '1'
# $env:C8_C52_STEPS = 'c52-win-store c52-linux-other'   # a further C5.2 night only: the steps it measures
Start-Process pwsh -WindowStyle Hidden -ArgumentList @('-NoProfile', '-File', "$co/keepawake.ps1", "$e/keepawake.sentinel")
Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -WindowStyle Hidden `
  -RedirectStandardError "$e/launch.err" -ArgumentList @("$co/overnight-c8.sh", $cand, $sha, $e)
Remove-Item Env:C8_C52_ONLY, Env:C8_C52_STEPS, Env:NIGHT_DEADLINE -ErrorAction SilentlyContinue   # the night keeps its copy
```

overnight-c8.sh refuses (exit 2, writing nothing) a SHA that is not 40 characters, a checkout not
at it or not clean, an evidence directory that holds a night's records, a `C8_C52_ONLY` other than
empty or `1`, a `C8_C52_SET` other than `full` or `derived`, a `C8_C52_STEPS` name that is not one
of the night's nine steps (or `C8_C52_STEPS` without `C8_C52_ONLY=1`), and a quiet.sh whose list
it cannot read. A refusal goes only to stderr, which the hidden window would lose, so the launch
sends stderr to `$e/launch.err` (each launch replaces it). **A minute after launch, `$e/chain.log`
must exist.** Between its last refusal and chain.log's start line the script only sets traps
and variables, defines functions, writes power.tsv's header and reads the power source once (one
PowerShell call, a few seconds). If chain.log does not exist, read `$e/launch.err` and delete
`$e/keepawake.sentinel`:

- a line `overnight-c8.sh: REFUSED: …` is a refusal, which writes no records: fix the cause and
  relaunch with the whole block above into the same `$e`;
- anything else means the night stopped before its start line (launch.err holds the shell's
  error): find the cause, then relaunch into a fresh `$e`, because power.tsv may already be there.

overnight-c8.sh holds no keep-awake of its own: delete `$e/keepawake.sentinel` when chain.log's
`done:` line appears.

Watch, under the night's `$e` (`phase3/c8-c52/` for the first): `chain.log` (first line
`start candidate=<sha> pid <msys> winpid <windows-pid> … mode=c52-only c52_set=full steps=[…]`),
`launch.err` (a refusal's reason, and any stray shell error during the night),
`p3-c116-rig-noextra.{json,log}` and `p3-c116-rig-coload.{json,log}`, one `quiet-<chunk>/` per
chunk (`c52-names.tsv` and `quiet-run.txt` in it; `paired.txt`, `completeness.tsv` and the
benchstat logs in its `c52-win/` or `c52-linux/`), `power.tsv` and `overnight-outcome.txt`. Exit 0
means every step the night ran passed VALID.

A further C5.2 night. A step is done when one of its tries, in candidate 8's night (or a re-run
of its overnight part) or in any C5.2 night on this candidate, is VALID with exit 0. This lists
the done steps:

```sh
cd plans/sdd/V6-closeout/phase3 && awk -F'\t' 'FNR > 1 && $4 == "VALID" && $3 == "0" { print $1 }' \
  $(ls c8/power.tsv c8-rerun-*/power.tsv c8-c52*/power.tsv 2> /dev/null) | grep -E '^(c116-rig|c52-)' | sort -u
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
2. Delete the night's own `$e/keepawake.sentinel`, where `$e` is the evidence directory it was
   launched into (`phase3/c8-c52` for the first night, `c8-c52-2` and so on after it) and holds
   the chain.log of step 1.
3. Docker: the container runs once for each Linux chunk. If chain.log's last
   `container start exit=0 (c52-linux-<group>)` has no `container stopped` line after it, run
   `docker stop qompack-v6-linux-verification`; if the last `engine started by this chain` has no
   `engine stopped` after it, run `docker desktop stop`. Never stop an engine the chain did not
   start (`engine up at start=1`).
4. Remove any `$e/quiet-*/.quiet.lock` a hard kill left (the same `$e` as step 2), and the
   scratch directory each `$e/quiet-*/quiet-run.txt` names on its `work=` line (its clones and
   test binaries).
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
- every C5.2 night step (`c116-rig` and the eight chunks) is done, VALID with exit 0 in candidate
  8's night or a C5.2 night on the frozen candidate (the `awk` above lists them), or its rows are
  dispositioned in the ledger (before D62(c) a C5.2 that candidate 8's night SKIPPED needed the
  same);
- docs/architecture.md section 7's "1.85 s to 0.66 s" ("The docs figure", under the C5.2 night)
  is restated from the frozen candidate's `c116-rig` runs or marked as measured before
  `3f2da1b3`, in the candidate or in a descendant whose changes reach no bundle (D58(e)), and the
  ruling is in the ledger;
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

Its parts judge against D58-D67 (mkrecheck8.py's docstring lists what each adds). C4.5's two status
reads, and two doctor reads, are compared after removing the documented age and timestamp fields
(status --json's `data.collected_at_ms` and every provenance `age_ms`; the text status header's
`collected <time>` and its `, <age> old` suffixes; doctor --json's `(persisted <age> ago)` in the
delivery rollover row): those differ between any two reads of unchanged state, and anything else
that differs is a finding (audit 2's #14).

### After the nights

The release plan's remaining checklist items, in order, each with its artifact (D62(g), D67):

1. **C5.3** (D67(i)): the deterministic replay evaluation on the frozen candidate is the replay
   gate's report from hosted ci.yml's `replay-gate` job on the frozen SHA. Record its run id and
   the report (the 24-session phase-0 comparison) in the ledger; fraction-of-OPT is a diagnostic
   only, never a pass or fail.
2. **C3.3** (D67(h)): Windows runs `devtool test-race` (the night's `win-race`). The product-child
   race lane (`QOMPACK_REQUIRE_CHILD_RACE=1`) is satisfied by the Linux container's child-race lane
   (the night's `linux-child`, `p3-linux-child-host.log`) and hosted nightly.yml's
   `race-product-child` job on the frozen SHA; record both, with the child SHA the lane logs.
3. The live re-check (above), only once the nights' evidence allows it.
4. **C6.1**: docs match evidence. The docs-only descendant of the candidate whose changes reach no
   bundle (D58(e); docs/release.md section 1, step 2) rewrites the pages' verified-where paragraphs
   and tables, `CHANGELOG.md`'s Known limits (with wave 22's unclosed minors, D66(d), D67(o)) and
   the release status from the tagged candidate's recorded evidence, and restates the C1.16 figure
   from the C5.2 night's `c116-rig` runs (D65(a)).
5. **C6.2** (D67(k)): after the C5.2 night or nights and the live re-check, an inventory seat
   re-dispositions all 304 rows of `plans/sdd/V6-remediation/inventory-current.tsv` from candidate
   8's evidence, adding `c8_result` and `c8_evidence` columns filled from `phase3/c8` (and any
   `c8-rerun-<n>`), the C5.2 chunk evidence (`quiet-c52-*` under `phase3/c8` and `phase3/c8-c52*`;
   row 1.1.27 by its absolute budget, D67(j)) and the live re-check's `rerun-c8`.
   `verified_in_target` only where an executed artifact exists. D62(b) breaks D57(e)'s carry for
   every C5.2 row, so each C5.2-mapped row needs its candidate 8 disposition.
6. **C6.3**, then **C6.4**: the close-out report depends on C6.2's columns, so it starts only after
   them, with its independent final review; then **C6.5**, **C7.3** (with branch protection,
   including the replay gate) and **C7.6** (housekeeping of only what this work created), each
   with its own authorization (D53(h), D62(g)).
