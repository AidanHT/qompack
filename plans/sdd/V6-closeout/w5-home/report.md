# V6 close-out w5-home: home-directory refusal (D18)

Branch `closeout/w5-home`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> Coordinator note: the fix seat rewrote this branch from `086a477` on, and the final code tree is unchanged. The implementer section below quotes pre-rewrite SHAs, which are no longer on the branch. Old → new: `ee766c8`+`acbb440` → `086a477`, `0d1f638` → `96344bf`, `26a7b8f` → `6dbf63f`, `f213130` → `c48b82a`, `99550dc` → `7abddc7`, `ff3f519` → `da3c602`. `520058e6` and `d8c8e499` are tree/object ids, not commits.

## Implementer — status `done`, head `ff3f5191931eb49c35d39bb49307a6c7c4b033d5`

### Root cause

paths.Resolve returns QOMPACK_PROJECT_ROOT, else the nearest ancestor with .git, else the payload cwd. Each step can return the home directory itself: a session started there, a session below a home that is a git work tree (a dotfiles repo), or an override naming it. No entry point checked for this, so the project store became <home>/.qompack, the user-global layer's own directory. At the base (git archive of bb9b5a7, where no entry point refuses; fake home under a scratch TMP), a temporary diagnostic listed what each entry point created there (runs/11-diag-base-writes-under-home.log). Every one of the 7 hooks wrote logs/LOUD.log, logs/qompack-<day>.log, run/spawn.lock, spool/client-*.ndjson, state/config-violations.json and tmp/. `qompack mcp` laid out a whole store and wrote state/mcp.json. `self-test` laid out a whole store. `qompack daemon` ran a daemon there (29+ entries). `status` and `pin --list` wrote logs, pins/ and config-violations.json, and status also lazily spawned. `config print` wrote state/config-violations.json. `fsck` scanned ~/.qompack as a store and exited 0. backup and admin would take the lock there. Only the user-global loaders and the logger fallback were supposed to write under ~/.qompack.

### Summary

# w5-home: refuse the home directory as a project root (owner decision D18)

**Status: done.** Branch `closeout/w5-home` in `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-home`, HEAD `ff3f519`. The code is final at `acbb440`; `ff3f519` only adds evidence. The tree is clean, and nothing was pushed, merged or tagged.

## Resume
- **Earlier seat's committed work:** `bb9b5a7` (paths.IsHome / RefuseHome / HomeDirs / ErrHomeRoot). I reviewed it and kept it.
  - Spelling comparison first, then `os.SameFile` identity.
  - On Windows, a creation-time pre-check skips SameFile's two opens for an ordinary project. It is sound: one directory, seen through any spelling or link, has one creation time.
  - I added a nil-getenv guard in `31941a7`.
- **Earlier seat's untracked drafts** (`homeroot.go`, `homeroot_test.go`, `refused.go`) were stubs plus a test draft. I kept the test design and wrote the implementation.
  - I revised one draft row: the restore test asserted that `run/` was absent, but EnsureLayout creates `run/` itself. It now asserts that `daemon.lock` is absent.
  - I added a row pinning that a session started in the home directory stays refused.
  - I split the tests into a session file and an operator file.
- **Pre-restart evidence:** replaced. The 01–03 logs were re-run, and 04 (empty) was deleted.

## Design: where the check lives
- **paths (the one question):** `paths.IsHome(root, homes...)`. It makes both sides absolute and cleans them, compares spelling (case-folded on Windows), then compares identity with `os.SameFile` over `os.Stat`, which follows symlinks and junctions. `RefuseHome` wraps `ErrHomeRoot` with the root and the fix.
- **cli (every entry point):** `homeroot.go` holds `refuseHomeRoot` / `isHomeRoot`. The homes compared are:
  - HOME and USERPROFILE, read through `env.Getenv`;
  - plus `homeDir(env)`, the home the user-global layer uses: an injected HomeDir, else the environment, else `os.UserHomeDir`.
- **daemon (the gate every daemon and writer passes):** `daemon.AcquireLock` refuses against the process's HOME and USERPROFILE before it creates `run/` or `daemon.lock`. `Run` takes the same gate.

## Behaviour now
- **Hooks (all seven).** Each asks about the process's root first and the payload's root second, before anything is read, written, logged, locked or spawned.
  - SessionStart answers exactly `{"systemMessage":"Qompack is inactive in this session: its project root is your home directory, so it records nothing. Open a project directory (one with its own .git) to use Qompack."}`. That is 168 chars, far under D15's 1,000.
  - The other six answer `{}`.
  - A session started in the home directory stays refused even when a later payload cwd names a project below it (design choice; see needs_owner).
- **`qompack mcp`.** It serves the same 8 tools. Every tools/call returns `mcp.HomeRootRefusedText` as a tool error (isError true), which says retrying will not help. There is no layout, log, handshake record, config report or client, so nothing is spawned. stderr says why, once. docs/mcp-tools.md renders the text from the constant.
- **`qompack daemon`.** Exits 0 with the reason on stderr, before the writer lease.
- **`status`.** Exits 0 with source `none (unavailable)`; the reason is the refusal (`StatusSources.Refused`). It asks no daemon and reads no metrics file.
- **`doctor`.** Exits 0.
  - `scope.root` is disabled with the reason, and there is no `scope.established` row.
  - `store.writable`, `store.open` and `runtime.recording` are disabled.
  - `status.primary` agrees with status.
  - No project history, ledger, lock, spool or store is read.
  - Side fix: doctor no longer reads a cwd-relative `config-violations.json` when no root resolves.
- **Commands that exit 1 with the reason:** `fsck` (all modes), `backup create`/`verify`/`restore` (source or destination, before the source is locked) and `admin delivery-seal`.
- **`self-test`.** Renders one critical `project.root` check (table or --json) with `mode: off`, exit 1.
- **recall / pin / why / dropped / checkpoint.** They report the refusal as `unavailable` through the new `commands.Deps.Refused`, in the --json envelope too. errors.Is still reaches the refusal, and --help still works. eval is exempt because it reads evaluation artifacts, not a project.
- **`config print`.** Shows the user-global layer alone, labelled `user`, with the reason on stderr and nothing persisted (`loadUserGlobalConfig`). `config schema` prints as before.
- **Regression rows.** A git project below a plain or a dotfiles home, and a plain directory below a plain home, record exactly as before, and the user-global layer is still applied.
- **Unchanged:** the user-global layer (config, calibration, D10 staged copies). Each row's whole-home snapshot proves nothing under the home was created, removed or rewritten.

## Entry-point audit
Every `resolveProjectRoot` / `paths.Resolve` caller in `internal/cli`:
- **Refused:** doHook (×2), runDaemon, runMCP, buildCommandDeps, doctor, fsck, backup, admin, self-test, and `loadForCommand` (config print / config schema).
- **Left as-is:** `eval import`. It reads configuration only and writes only to its --to destination. At the home it applies the user file as both layers, which gives identical values.
- **Other daemon-side resolution:** none. ipc `lazySpawn` and `daemon.EnsureRunning` / `SpawnDetached` were not changed (w5-coldstart owns them). Every caller refuses first, and the spawned daemon would refuse at runDaemon and AcquireLock.

## Docs
- `plans/00-ARCHITECTURE.md` §3.3 records the refusal and what each surface answers. §5.0 lists the four paths symbols, and §5.17 notes `Deps.Refused`.
- `docs/architecture.md` §2, `docs/user-guide.md`, `docs/install.md` §4, `docs/troubleshooting.md` (new entry "Qompack is inactive in the home directory", the `project.root` self-test row, and a note on what pre-D18 builds left in ~/.qompack), `docs/cannot-do.md` §4 and `docs/mcp-tools.md`.
- Qompack.md states no resolution order (§7.4 defers to the architecture doc), so it needed no Revision-log entry.

## Red evidence at the base
- `runs/10-cli-red-at-base.log`: git archive `bb9b5a7` plus the new tests plus constant-only stubs, running `go test -count=1 -timeout=10m -v -run '^TestHomeRoot_' ./internal/cli/` with `-skip` of the daemon row (at the base it would serve forever). Every refusal row fails; the notice-length row and the three regression rows pass.
- `runs/12-daemon-lock-red-at-base.log`: `go test -count=1 -v -run '^TestAcquireLock_RefusesTheHomeDirectory$' ./internal/daemon/` on the base copy fails: the lock is taken.
- `runs/01-paths-red.log`: HEAD with `home*.go` replaced by stubs, `go test -count=1 -v -run 'Home' ./internal/paths/`. 6 rows fail.
- `runs/11`: the base-writes diagnostic (temporary, scratch only, never committed).

## One pre-existing check needed a criterion change
Running the full cli package at `f213130` failed `TestHookCapture_HardBoundPrecedesConfiguration`, deterministically (alone as well: runs/31; also on Linux). The hook now reads HOME and USERPROFILE for the D18 decision before it reads the delivery, and the test's proxy counted any Getenv call except QOMPACK_PROJECT_ROOT as a configuration lookup. Fixed in `acbb440`; see criterion_changes. Bisect note: `ee766c8` alone fails that test.

## Cost
`go test -count=1 -run '^$' -bench '^BenchmarkIsHome_AProjectBelowHome$' -benchtime=2000x -count=5 ./internal/paths` on Windows, co-loaded: 47–305 µs/op, median about 99 µs, 9–11 allocs. That is paid once per hook in the common case. It is a measurement, not a new budget.

### Commits

- bb9b5a7 feat(paths): add IsHome, the D18 home-directory check (earlier seat; reviewed and kept)
- 31941a7 fix(paths): name no home directory for a nil lookup
- 25e2a8b docs(paths): say every step of Resolve can name the home
- 25bed9e feat(daemon): refuse the home directory at the lock
- 46a2821 feat(commands): report a refused project as unavailable
- 8979fa3 feat(mcp): add the stable home-directory refusal text
- ee766c8 feat(cli): refuse a home-directory root in hooks, mcp and daemon (built from the earlier seat's untracked homeroot.go, homeroot_test.go and refused.go drafts)
- 0d1f638 feat(cli): refuse a home-directory root in operator commands
- 26a7b8f docs(plans): record D18's home-directory refusal in §3.3
- f213130 docs(v6): say Qompack is inactive in the home directory (D18)
- 99550dc docs(v6): say what an older build left in ~/.qompack
- acbb440 test(cli): tell the home lookup apart from configuration lookups
- ff3f519 docs(closeout): record the w5-home runs (evidence only)

### Tests

- `go test -count=1 -timeout=10m -v -run '^TestHomeRoot_' ./internal/cli/ (git archive bb9b5a7 + new tests + constant stubs, -skip '^TestHomeRoot_DaemonStartsNothing$'; runs/10)` — RED as intended: every refusal row fails (hooks answer a rehydration-deferred note, fsck/self-test/admin exit 0, backup reaches the config gate, status has no reason); notice-length and 3 regression rows pass
- `temporary diagnostic TestDiagW5HomeWritesAtBase (scratch-only copy of bb9b5a7, never committed; runs/11)` — base writes under a fake home: 9 entries per hook (logs, run/spawn.lock, spool, state/config-violations.json, tmp), whole layout + state/mcp.json for mcp, whole layout for self-test, a running daemon for `qompack daemon`, logs/pins for status and pin; fsck exits 0
- `go test -count=1 -v -run '^TestAcquireLock_RefusesTheHomeDirectory$' ./internal/daemon/ (base copy; runs/12)` — RED as intended: lock acquired, ErrHomeRoot not returned
- `go test -count=1 -v -run 'Home' ./internal/paths/ (HEAD with internal/paths/home*.go replaced by no-op stubs; runs/01)` — RED as intended: 6 IsHome/RefuseHome/HomeDirs rows fail
- `go test -count=1 -run '^TestHookCapture_HardBoundPrecedesConfiguration$' ./internal/cli/ (f213130, alone; runs/31)` — FAIL deterministically: readAtFirstLookup 0, because the D18 home lookup precedes the read; fixed by the criterion change in acbb440
- `go test -count=1 -timeout=30m <pkg>, one package at a time, Windows co-loaded, f213130 (runs/30-*)` — internal/paths, internal/commands, internal/mcp, tools/devtool, test/docs and internal/daemon (686 s) ok; internal/cli failed only TestHookCapture_HardBoundPrecedesConfiguration (see above)
- `go test -count=1 -timeout=30m ./internal/cli (Windows, acbb440; runs/32)` — ok
- `go test -count=1 -timeout=30m -v -run '^TestHomeRoot_' ./internal/cli (Windows, acbb440; runs/33)` — PASS: all 13 D18 cli tests with every subtest
- `go test -count=1 -timeout=30m -v -run 'Home' ./internal/paths (Windows, acbb440; runs/02)` — PASS
- `go test -count=1 -timeout=30m -v -run '^TestRefused_' ./internal/commands (Windows, acbb440; runs/34)` — PASS (5 tests)
- `go test -count=1 -timeout=30m -v -run '^TestAcquireLock_RefusesTheHomeDirectory$' ./internal/daemon (Windows, acbb440; runs/35)` — PASS
- `go test -count=1 -timeout=30m -v -run '^TestGuard_WriteSet' ./test/guards (Windows, acbb440; runs/36)` — PASS: TestGuard_WriteSetConfinedToQompack, TestGuard_WriteSetDetectsAStrayWrite
- `go test -count=1 -run '^$' -bench '^BenchmarkIsHome_AProjectBelowHome$' -benchtime=2000x -count=5 ./internal/paths (Windows co-loaded; runs/03)` — 47-305 us/op, median ~99 us, 9-11 allocs/op
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-home --repo <worktree> --out <runs/linux> f213130 pkgs-full -- ./internal/paths ./internal/cli ./internal/commands ./internal/mcp ./internal/daemon ./test/docs (non-root uid 10001, -race)` — paths 177 pass/18 skip, commands 126, mcp 387, test/docs 19 PASS. cli 413 pass, 1 fail: TestHookCapture_HardBoundPrecedesConfiguration (the same cause, fixed in acbb440). daemon 1500 pass, 1 fail: TestFeaturesFrom_LexicalCohesionShingleCap, wall clock 14.5 ms vs 10 ms under co-load
- `linux-nonroot-gate.sh ... acbb440 shingle-alone --run '^TestFeaturesFrom_LexicalCohesionShingleCap$' --count 10 -- ./internal/daemon` — PASS 10/10 alone: a co-load wall-clock red in untouched scheduler code, not this branch
- `linux-nonroot-gate.sh ... acbb440 cli-full -- ./internal/cli ./internal/commands` — PASS: cli 414/0, commands 126/0
- `linux-nonroot-gate.sh ... acbb440 homeroot-x3 --run '^TestHomeRoot_' --count 3 -- ./internal/cli` — PASS 120/0
- `go test -count=1 ./test/docs; devtool gen-mcp-docs/gen-command-docs/gen-config-docs --check; devtool fmt-check; go vet (GOOS=windows, linux, darwin) on paths/cli/commands/mcp/daemon/devtool (acbb440; runs/22)` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (acbb440; runs/23)` — all 8 sub-checks PASS
- `pinned golangci-lint (host build) run with GOOS=linux and GOOS=darwin on ./internal/paths/... ./internal/cli/... ./internal/commands/... ./internal/mcp/... ./internal/daemon/... ./tools/devtool/... (runs/21)` — exit 0 on both

### Criterion changes

- TestHookCapture_HardBoundPrecedesConfiguration (internal/cli/capture_admission_test.go, acbb440). OLD: records how much of the delivery had been read at the first Getenv call for any key except QOMPACK_PROJECT_ROOT, and requires that to equal hookCaptureMaxBytes+1. NEW: HOME and USERPROFILE are also recorded apart, in readAtHomeLookup. A new assertion requires readAtHomeLookup == 0, i.e. the D18 refusal is decided before a byte is read. WHY: the assertion's own claim is that the read bound fires 'before any configuration environment was scanned'. HOME and USERPROFILE are not configuration: config's env layer reads only QOMPACK_<SEC>__<KEY> names, and this test injects HomeDir. They are root resolution, deciding whether the root may be used at all, exactly as the already-exempt QOMPACK_PROJECT_ROOT decides which root. UNCHANGED: the read-bound assertion (reader.read == hookCaptureMaxBytes+1), the configuration-ordering assertion, and every spool-record assertion. The test is strictly stronger: it gains an assertion and relaxes none.

### Open issues

- Bisect note: ee766c8 alone fails TestHookCapture_HardBoundPrecedesConfiguration. acbb440 fixes it; history was not rewritten.
- Linux co-load red TestFeaturesFrom_LexicalCohesionShingleCap (14.5 ms vs 10 ms, -race, full daemon package) in untouched scheduler code. It passes 10/10 alone at acbb440 (artifacts runs/linux/cx-w5-home-shingle-alone-*).
- Defense in depth left to w5-coldstart's files: ipc.Client.lazySpawn and daemon.EnsureRunning/SpawnDetached do not re-check the root. Every caller refuses first, and a spawned daemon refuses at runDaemon and AcquireLock. But a future caller that skipped refuseHomeRoot would still have lazySpawn create run/spawn.lock under ~/.qompack before the spawned daemon refused.
- Merge coordination. With w5-coldstart: my doHook edits are two small inserts at the top of doHook and before the r2 branch in hookclient.go; session-start's preSend/EnsureRunning are untouched. With w5-winfiles: I added internal/paths/home*.go and a comment in resolve.go, and left appendonly.go and internal/config untouched. cli.loadUserGlobalConfig relies on config.Load building both files as <dir>/.qompack/config.json; a paths.Global-based spelling is equivalent. TestHomeRoot_ConfigPrintShowsOnlyTheUserGlobalLayer pins it.
- Not treated as homes: $QOMPACK_HOME, the calibration directory override, and tokens' os.TempDir()/.qompack last resort. A session rooted at the parent of $QOMPACK_HOME, or at the temp dir, could still share a directory with the calibration file. Out of D18's wording.
- Pre-D18 builds recorded home-directory sessions into ~/.qompack itself. After an upgrade that directory may hold a project store beside the user-wide files, and an old daemon may run until idle exit. This build never reads, writes or removes it; this is documented in docs/troubleshooting.md, and there is no migration.
- At the home, SessionStart shows the notice even if the user-global config sets runtime.mode=off, because configuration is deliberately not read for a refused root.
- test/guards was run only for the write-set rows; its known carried-defect red awaits the Phase 2 dispositions.
- Container /work/cx-w5-home-* directories remain (4 runs).

### Needs the owner

- No new budget or bound numbers were introduced. The notice is checked against D15's already-ratified 1,000-character banner ceiling. The IsHome cost (median about 99 us per hook on co-loaded Windows) is a measurement, not a budget.
- Default taken, owner may overrule: a session whose PROCESS root is the home directory stays refused even when a later payload cwd names a project below it, so the SessionStart notice ('records nothing') stays true. The alternative, routing each delivery by its payload root, would record later deliveries into the project behind that notice. Pinned by TestHomeRoot_ASessionStartedInHomeStaysRefusedWhenAPayloadNamesAProject.
- Default taken, owner may overrule: `config print` at the home directory shows the user-global layer (exit 0, reason on stderr, nothing persisted) rather than refusing with exit 1. `eval import` is not refused, because it touches no store.
- Default taken, owner may overrule: HOME and USERPROFILE are both treated as the home on every platform, so a root equal to either is refused even when they differ.

## Independent review

### review:home: needs-fixes

- **minor** `commit ee766c8 (internal/cli/hookclient.go:315, internal/cli/capture_admission_test.go:188-222; fixed only in acbb440)` — ee766c8 cannot be bisected. At that commit TestHookCapture_HardBoundPrecedesConfiguration fails deterministically, because the new D18 HOME/USERPROFILE lookup happens before the delivery is read and the test's proxy counts it as a configuration lookup. The criterion fix landed four commits later in acbb440, with 0d1f638, 26a7b8f, f213130 and 99550dc in between, so all five commits ee766c8..99550dc leave internal/cli red.
  - Evidence: The implementer's own open_issues says: 'Bisect note: ee766c8 alone fails TestHookCapture_HardBoundPrecedesConfiguration. acbb440 fixes it; history was not rewritten.' runs/31-hardbound-alone-at-f213130.log shows the failure at f213130, and runs/30-win-full__internal_cli-f213130.log shows the package red. The acbb440 commit message says it too: 'ee766c8 alone fails this test.' The branch is unpushed and unmerged.
  - Fix: Before integration, rewrite the unpushed branch so the test change lands with the behaviour that needs it. Fold acbb440 into ee766c8 with a fixup commit and a non-interactive `GIT_SEQUENCE_EDITOR=: git rebase --autosquash` from 90e1db3. Then re-run `go test -count=1 -run '^TestHookCapture_HardBoundPrecedesConfiguration$|^TestHomeRoot_' ./internal/cli/` at the rewritten ee766c8 and at HEAD. Also update the evidence log names that quote acbb440/f213130 (the report-SHA reachability rule). If rewriting is ruled out, record the red range ee766c8..99550dc in the workstream report so a later bisect skips it.
- **nit** `internal/cli/homeroot.go:64-70 (writeRefusedHookOutput); internal/cli/hookclient.go:315` — In a refused session, SessionStart shows the D18 notice even when the user-global config sets runtime.mode=off. The refusal deliberately reads no configuration, so a user who switched Qompack off everywhere still gets a Qompack message at the top of every session started in their home directory. This is an owner-visible behaviour choice. The implementer listed it only under open_issues, not needs_owner, where the other defaults taken are listed.
  - Evidence: open_issues: 'At the home, SessionStart shows the notice even if the user-global config sets runtime.mode=off, because configuration is deliberately not read for a refused root.' loadUserGlobalConfig already exists (homeroot.go:83), is read-only and persists nothing, and SessionStart is off the hot path. So honouring mode=off would cost one read-only load on that one hook.
  - Fix: Either move the item into needs_owner beside the other three defaults so the owner rules on it explicitly, or on SessionStart only, answer {} when loadUserGlobalConfig(env) yields Runtime.Mode == off. If you do the second, add a TestHomeRoot_ row with the fake home's config.json setting runtime.mode off, and keep requireUntouched.

## Fix seat (review resolution) — status `done`, head `da3c60236381ea96132a9616ac1dd8cf2923192d`

### Root cause

The D18 behaviour commit (originally ee766c8) added HOME/USERPROFILE lookups to doHook before the delivery is read. TestHookCapture_HardBoundPrecedesConfiguration counted every Getenv other than QOMPACK_PROJECT_ROOT as a configuration lookup, so it failed deterministically. The test change it needed landed four commits later (acbb440), so ee766c8..99550dc left internal/cli red and could not be bisected. Fixed by folding the test change into the behaviour commit (now 086a477) and rebuilding the evidence at reachable SHAs.

### Summary

w5-home FIX SEAT: final state

The branch closeout/w5-home is at da3c602, cut from 90e1db3. The worktree is clean. Nothing was pushed or merged. The one review finding was correct and is fixed by rewriting this unpushed branch. Every commit from 90e1db3 to 7abddc7 is now green on the packages it touches.

## Review resolution
- Finding (minor): "ee766c8 cannot be bisected; ee766c8..99550dc leave internal/cli red." This is CONFIRMED and FIXED.
  - How I checked it:
    - I checked out the original ee766c8 (detached, in my own worktree) and ran `go test -count=1 -run '^TestHookCapture_HardBoundPrecedesConfiguration$' ./internal/cli/`. It exited 1 with `expected: 4194305, actual: 0` at capture_admission_test.go:207.
    - Cause: from that commit on, doHook calls isHomeRoot (HOME/USERPROFILE lookups) before it reads the delivery. The test's proxy counted any Getenv other than QOMPACK_PROJECT_ROOT as a configuration lookup. The criterion fix landed only in acbb440, four commits later.
  - Fix: history rewrite of this branch only. The branch was only on refs/heads/closeout/w5-home, with no remote ref.
    - I ran `git rebase -i 90e1db3` non-interactively, with a prepared todo passed through GIT_SEQUENCE_EDITOR.
    - acbb440 is folded into ee766c8 with `fixup`, followed by `exec git commit --amend -F <msg>`. The folded message keeps ee766c8's text and adds acbb440's rationale for the criterion change. The commit-msg hook checked it.
    - The old evidence commit ff3f519 was dropped and rebuilt.
  - Old to new SHAs:
    - ee766c8 + acbb440 -> 086a477
    - 0d1f638 -> 96344bf
    - 26a7b8f -> 6dbf63f
    - f213130 -> c48b82a
    - 99550dc -> 7abddc7
    - ff3f519 -> da3c602
    - bb9b5a7, 31941a7, 25e2a8b, 25bed9e, 46a2821 and 8979fa3 keep their SHAs.
    - The old SHAs are no longer on the branch. Any coordinator note that quotes them must use the new ones.
  - Tree checks:
    - 7abddc7^{tree} is 520058e698eb…, identical to acbb440^{tree}. The final code did not change.
    - Each rewritten commit differs from its original only in internal/cli/capture_admission_test.go.
    - f213130:internal/paths and 7abddc7:internal/paths are the same tree (d8c8e499…).
  - Report-SHA reachability:
    - Every evidence log that named acbb440 or f213130 was re-run and renamed to 7abddc7.
    - A scan of the new evidence finds no old SHA. Every 7-hex commit it quotes passes `git merge-base --is-ancestor <sha> HEAD`.
    - The implementer's two Linux gate outputs had never been committed: `*.out` is gitignored. I moved them to my scratch (w5/home/prerewrite-ignored/) and did not delete them. The new gate output is committed as .txt.

## Root cause (of the finding)
The behaviour change (D18's pre-read HOME/USERPROFILE lookup in doHook) and the test that had to learn about it landed in separate commits. So five commits in a row failed TestHookCapture_HardBoundPrecedesConfiguration on both Windows and Linux.

## Criterion changes
There are no new criterion changes from this seat. The only criterion change on the branch is the implementer's, now inside 086a477: TestHookCapture_HardBoundPrecedesConfiguration.
- HOME and USERPROFILE are recorded apart from configuration lookups, because they are root resolution, like QOMPACK_PROJECT_ROOT.
- The configuration-ordering assertion and the read-bound assertion are unchanged.
- A new assertion (readAtHomeLookup == 0) pins that the D18 refusal is decided before any byte of the delivery is read.
- I checked that the test still has teeth: HomeDir is injected, so config lookups still go through the other env keys and are still caught.

## Evidence (committed under plans/sdd/V6-closeout/w5-home/runs/)
- **Finding reproduced on reachable commits:** 31-review-hardbound-red-without-the-fold.log. This is a git archive of 086a477 with capture_admission_test.go taken from 8979fa3; that blob equals the pre-fold ee766c8 blob, 02569141. It exits 1 with the same failure.
- **Bisect sweep:** 40-bisect-sweep-summary.txt plus per-commit logs. For every commit from 90e1db3 to 7abddc7, from a git archive of that exact commit:
  - vet exits 0 at every commit.
  - The full paths, commands, cli and docs packages exit 0 at every commit.
  - At 25bed9e the daemon lock row exits 0.
  - At 8979fa3, internal/mcp in full and gen-mcp-docs --check both exit 0.
  - From 8979fa3 on, the reviewer's focused command exits 0, including at 086a477 and at 7abddc7.
- **Windows full packages at 7abddc7** (30-win-full-*), all exit 0: paths 8 s, commands 3 s, cli 32 s, docs 3 s, devtool 107 s, mcp 173 s, daemon 384 s. They ran on a co-loaded machine and nothing needed a re-run.
- **Focused rows at 7abddc7** (02, 33, 34, 35, 36), all green: 41 PASS lines in 33.
- **Paths red against stubs at 7abddc7** (01): same verdicts as the implementer's run.
- **IsHome benchmark at 7abddc7** (03): 28–39 µs/op, 9 allocs, measured on a co-loaded machine.
- **Docs, fmt, vet and lint** (22, 23, 21): test/docs, gen-mcp, gen-command and gen-config --check, fmt-check, and go vet on windows, linux and darwin all exit 0. devtool lint (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns) passes. Pinned golangci-lint with GOOS=linux and GOOS=darwin exits 0. runpatterns and docmarkers pass again after the evidence commit.
- **Linux non-root -race gate at 7abddc7** (runs/linux/cx-w5-home-pkgs-full-7abddc7-20260926T231356Z-artifacts, tree 520058e6), all PASS:

  | Package | Pass | Skip |
  |---|---|---|
  | paths | 177 | 18 |
  | cli | 414 | 0 |
  | commands | 126 | 0 |
  | mcp | 387 | 0 |
  | daemon | 1501 | 1 |
  | docs | 19 | 0 |

  Exit 0. TestFeaturesFrom_LexicalCohesionShingleCap failed in the implementer's run at the old f213130; it did not fail here.
- **Kept unchanged from the old evidence** (no branch SHA in them): 10-cli-red-at-base.log, 11-diag-base-writes-under-home.log (it names bb9b5a7 and 90e1db3, both reachable), and 12-daemon-lock-red-at-base.log.

## Open items
There are no new ones. The bisect note in the implementer's open_issues is closed by the fold.

### Commits

- bb9b5a7 feat(paths): add IsHome, the D18 home-directory check (unchanged)
- 31941a7 fix(paths): name no home directory for a nil lookup (unchanged)
- 25e2a8b docs(paths): say every step of Resolve can name the home (unchanged)
- 25bed9e feat(daemon): refuse the home directory at the lock (unchanged)
- 46a2821 feat(commands): report a refused project as unavailable (unchanged)
- 8979fa3 feat(mcp): add the stable home-directory refusal text (unchanged)
- 086a477 feat(cli): refuse a home-directory root in hooks, mcp and daemon (was ee766c8 + acbb440, folded)
- 96344bf feat(cli): refuse a home-directory root in operator commands (was 0d1f638)
- 6dbf63f docs(plans): record D18's home-directory refusal in §3.3 (was 26a7b8f)
- c48b82a docs(v6): say Qompack is inactive in the home directory (D18) (was f213130)
- 7abddc7 docs(v6): say what an older build left in ~/.qompack (was 99550dc)
- da3c602 docs(closeout): record the w5-home runs (evidence only) (replaces ff3f519, every run redone at reachable SHAs)

### Tests

- `(original ee766c8, detached) go test -count=1 -run '^TestHookCapture_HardBoundPrecedesConfiguration$' ./internal/cli/` — FAIL exit=1 (expected 4194305, actual 0): finding confirmed
- `git archive 086a477 + capture_admission_test.go from 8979fa3; go test -count=1 -run '^TestHookCapture_HardBoundPrecedesConfiguration$' ./internal/cli/` — FAIL exit=1: the same red reproduced on reachable commits (31-review-hardbound-red-without-the-fold.log)
- `per commit 90e1db3..7abddc7 (git archive): go vet ./internal/paths ./internal/commands ./internal/mcp ./internal/cli ./internal/daemon ./tools/devtool ./test/docs; go test -count=1 -timeout=30m ./internal/paths ./internal/commands ./internal/cli ./test/docs` — all 11 commits exit 0 for both
- `go test -count=1 -v -run '^TestHookCapture_HardBoundPrecedesConfiguration$|^TestHomeRoot_' ./internal/cli/ at 8979fa3, 086a477, 96344bf, 6dbf63f, c48b82a, 7abddc7` — PASS at every commit, including the rewritten 086a477 and HEAD 7abddc7
- `25bed9e: go test -count=1 -v -run '^TestAcquireLock_RefusesTheHomeDirectory$' ./internal/daemon; 8979fa3: go test -count=1 ./internal/mcp and go run ./tools/devtool gen-mcp-docs --check` — PASS / ok / up to date
- `7abddc7 Windows: go test -count=1 -timeout=30m on ./internal/paths ./internal/commands ./internal/cli ./test/docs ./tools/devtool ./internal/mcp ./internal/daemon (one at a time)` — all exit 0 (8/3/32/3/107/173/384 s)
- `7abddc7: go test -count=1 -v -run '^TestIsHome_|^TestHomeDirs_|^TestRefuseHome_' ./internal/paths/; -run '^TestRefused_' ./internal/commands/; -run '^TestAcquireLock_RefusesTheHomeDirectory$' ./internal/daemon/; -run '^TestGuard_WriteSetConfinedToQompack$|^TestGuard_WriteSetDetectsAStrayWrite$' ./test/guards/` — all PASS
- `7abddc7 red: internal/paths/home.go stubbed; go test -count=1 -v -run '^TestIsHome_|^TestHomeDirs_|^TestRefuseHome_' ./internal/paths/` — FAIL as expected; same verdicts as the implementer's red run
- `7abddc7: go test -count=5 -run '^$' -bench '^BenchmarkIsHome_AProjectBelowHome$' -benchtime=2000x -benchmem ./internal/paths/` — 28-39 us/op, 9 allocs/op (co-loaded machine)
- `7abddc7: go test -count=1 ./test/docs; gen-mcp/command/config-docs --check; go run ./tools/devtool fmt-check; go vet (windows, GOOS=linux, GOOS=darwin) on touched packages` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns; pinned golangci-lint with GOOS=linux and GOOS=darwin` — all PASS, lint exit 0; runpatterns and docmarkers re-run after the evidence commit: PASS
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-home --repo C:/.../qompack-cx-w5-home --out C:/.../runs/linux 7abddc7 pkgs-full -- ./internal/paths ./internal/cli ./internal/commands ./internal/mcp ./internal/daemon ./test/docs` — gate exit 0, non-root with -race: paths 177, cli 414, commands 126, mcp 387, daemon 1501 (1 skip), docs 19 pass, 0 fail

### Criterion changes

- TestHookCapture_HardBoundPrecedesConfiguration (the implementer's change, now inside 086a477): HOME and USERPROFILE lookups are recorded apart from configuration lookups, because they are root resolution like QOMPACK_PROJECT_ROOT, not policy. The configuration-ordering and read-bound assertions are unchanged, and a new assertion pins that the D18 refusal is decided before any byte is read (readAtHomeLookup == 0). Nothing was loosened: HomeDir is injected, so config lookups still go through other env keys and stay caught. The fix seat added no criterion changes.

### Open issues

- The branch history was rewritten from 086a477 on, with old -> new SHAs: ee766c8+acbb440 -> 086a477, 0d1f638 -> 96344bf, 26a7b8f -> 6dbf63f, f213130 -> c48b82a, 99550dc -> 7abddc7, ff3f519 -> da3c602. Coordinator notes that quote the old SHAs must be updated. The final code tree is unchanged (520058e6 = acbb440's tree).
- The implementer's two Linux gate transcripts (*.out) were gitignored and never committed. I moved them to scratch w5/home/prerewrite-ignored/ and did not delete them. The new transcript is committed as runs/linux/gate-pkgs-full-7abddc7.txt.

### Needs the owner

- No new budget or bound numbers were introduced. The notice is checked against D15's already-ratified 1,000-character banner ceiling. The IsHome cost is a measurement, not a budget: a median of about 99 us per hook on co-loaded Windows in the implementer's run, and 28-39 us/op in the fix seat's re-run at 7abddc7.
- Default taken, owner may overrule: a session whose PROCESS root is the home directory stays refused even when a later payload cwd names a project below it, so the SessionStart notice ('records nothing') stays true. The alternative, routing each delivery by its payload root, would record later deliveries into the project behind that notice. Pinned by TestHomeRoot_ASessionStartedInHomeStaysRefusedWhenAPayloadNamesAProject.
- Default taken, owner may overrule: `config print` at the home directory shows the user-global layer (exit 0, reason on stderr, nothing persisted) rather than refusing with exit 1. `eval import` is not refused, because it touches no store.
- Default taken, owner may overrule: HOME and USERPROFILE are both treated as the home on every platform, so a root equal to either is refused even when they differ.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


