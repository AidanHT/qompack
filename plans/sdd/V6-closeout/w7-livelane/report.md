# w7-livelane: the Phase 4 live-lane workflow, home guard and session driver

Branch `closeout/w7-livelane`. Workflow `wf_00490efb-ff4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `7f94d4fd`

### Summary

SEAT livelane. The Phase 4 live-lane workflow is finished and committed on branch closeout/w7-livelane in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-livelane/plans/sdd/V6-closeout/coordinator/. Two new tools sit beside it, each with a self-test I ran. No real Claude sessions were run, and I did not read or touch ~/.claude or ~/.qompack.

WHAT CHANGED AND WHY
1. live-uat.js
- The coordinator only has to replace __CANDIDATE__ and __BUNDLE__. The script throws if either is still a placeholder.
- __BUDGET__ is gone. Each part now has an explicit budget of Haiku sessions: install 8, sessions 12, retrieval 12, and a new recovery part with 4. That is 36 in total, inside D3's 40-80.
- The parts run strictly one after another (a for/await loop). The loop stops if a part returns guard_ok false. Each part runs at effort high.
- The new recovery part covers C1.6, C1.7, C5.6 and C4.11.
- The read-only audit stage is kept. It now also returns a coverage table for every checklist item and checks planted-secret and guard.json leaks.
- The lane now uses the committed coordinator/homeguard.py and live_driver.py.
- Planted secrets are built at run time and replaced by @@SEC_PLANTED_n@@ in committed evidence, because the repository is public and push protection blocks credential-shaped strings.
- Every part records each session in live/sessions.tsv.
- UAT-01 runs with no QOMPACK_* variables, as its preconditions require. Every other session sets QOMPACK_HOME to scratch.

2. homeguard.py (new)
- A homeguard.py already exists at the integration head under eval/runs, but it compared installed_plugins.json and known_marketplaces.json by size only, and its check stopped daemons with taskkill.
- The new one hashes all three files byte for byte and lists plugins/{cache,marketplaces,data} by entry name. It lists ~/.qompack by name: additions are reported as information, losing an entry that existed before is a failure.
- It never opens credentials, and CLAUDE_CONFIG_DIR is ignored. snap refuses to overwrite an existing snapshot. check is read-only. clean removes only empty qompack* data directories that the run created.

3. live_driver.py (new, derived from smoke_driver.py)
- Records hook latency as the host saw it. The stream has no durationMs field, so it pairs each hook_started with its hook_response by arrival time.
- Supports a per-turn "before" command for the degraded-path steps (C4.9).
- After every turn it samples the daemon's working set and CPU (checking the qompack.exe image first) and the .qompack/ size, for C5.6.
- Scrubs the scratch path prefix from driver-config.json and meta.json.

4. README.md: the live-uat.js row is updated and rows for the two tools are added.

HOST FACTS VERIFIED (Claude Code 2.1.280, from `claude --version` and the help commands)
- `claude --help`:
  - `--input-format <format> ... "stream-json"`, `--output-format ... "stream-json"`, `--include-hook-events ... (only works with --output-format=stream-json)`.
  - `--setting-sources <sources> Comma-separated list of setting sources to load (user, project, local)`.
  - `--permission-mode <mode> (choices: "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan")`.
  - `--plugin-dir <path> Load a plugin from a directory or .zip for this session only`.
  - `-r, --resume` and `--fork-session`.
  - `--autocompact <auto|tokens> Auto-compact window size (auto, or 100k–1M tokens)`. This flag is new to the draft; it is listed as an unrehearsed alternative.
  - `-p ... Settings files that fail validation are silently ignored in this mode`. This is new: the lane must prove the project deny rule took effect before relying on it.
  - `--bare` skips hooks, so it is forbidden.
  - `--max-turns` is NOT listed. It was kept because every C5.4 smoke and pilot run on 2.1.280 passed it successfully (eval/runs/smoke3-plugin-dir-autocompact/driver-config.json).
- `claude plugin marketplace add --help`: `--scope <scope> Where to declare the marketplace: user (default), project, or local`.
- `claude plugin marketplace remove --help`: `--scope ... Omit to remove it from every scope`.
- `claude plugin install --help`: `-s, --scope <scope> Installation scope: user, project, or local (default: "user")`; `-y ... (required when stdin or stdout is not a TTY)`; `--json Print one machine-readable result line`.
- `claude plugin uninstall --help`: `--keep-data Preserve the plugin's persistent data directory (~/.claude/plugins/data/{id}/)`; `-s, --scope`; `--json`.
- `claude plugin update --help`: `-s, --scope <scope> ... user, project, local, managed`.
- `claude plugin --help`: `details <name> Show a plugin's component inventory`; `list [--json]`; `validate [--strict] [--json]`.
- `claude plugin marketplace list --help`: `--json`.
- Headless forced compaction has no help line. The evidence is the committed streams: a "/compact" user turn (smoke2, pilots) and the env pair CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 plus CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30 (smoke3).
- From the pilot and smoke stream.jsonl init events: MCP tools are mcp__plugin_qompack_qompack__<tool> (all 8), the server is "plugin:qompack:qompack", the slash commands are qompack:checkpoint, dropped, eval, pin, recall, status and why, and claude_code_version is 2.1.280. hook_started/hook_response events carry type, subtype, hook_id, hook_name, hook_event, output, stdout, stderr, exit_code, outcome and no duration field. So the draft's "durationMs in the hook events" was wrong, and live_driver.py measures latency instead.
- Against docs at the integrated head (qompack-cx-int):
  - install.md §3 says "Only validate and list accept --json", which is wrong for 2.1.280. The lane records it as a docs finding.
  - install.md §5's two-step upgrade (marketplace update, then plugin update) is used as written.
  - install.md §6 says staged ~/.qompack/bin copies are removed by hand; this is used.
  - uat.md says there is no operator stop command, so the lane relies on idle exit or a pid + image check.
  - 301a8e9 (the previous build) has devtool bundle but NO `qompack backup`, which arrived in 4a12eff (checked with git merge-base). So C4.8/UAT-12 backups use the frozen CLI on the old build's store.
  - hostperm reads project .claude/settings.json deny rules, so the UAT-12 deny rule is project scope.
  - The home directory is not a git repository, so scratch projects are not refused under D18.

COVERAGE MAP (checklist item -> part/scenario -> evidence path under plans/sdd/V6-closeout/live/)
- C4.1 -> install (1) isolated CLAUDE_CONFIG_DIR marketplace install, list, details, validate, no session; plus (2) UAT-01 local-scope install whose session init event shows what was discovered -> c4/C4.1/, uat/UAT-01/
- C4.2 -> sessions / UAT-02: all 7 hook events (including SubagentStop via one subagent task), index counts vs transcript, status/doctor/fsck -> uat/UAT-02/
- C4.3 -> sessions / UAT-03, UAT-04, UAT-05: /compact and forced automatic compaction, additionalContext under 10,000 chars, fact recovery, no C1.12 rejection, no SessionEnd "Hook cancelled" -> uat/UAT-03..05/
- C4.4 -> retrieval / UAT-07..09: each of the 8 tools with a correct result and a correct error -> c4/C4.4/tool-matrix.md, uat/UAT-07..09/
- C4.5 -> retrieval -> c4/C4.5/commands-vs-docs.md
- C4.6 -> install / UAT-12: planted secrets grepped across every Qompack surface; deny-ruled and out-of-project reads through Read, recall, expand, re_read, /qompack:recall and the CLI -> c4/C4.6/, uat/UAT-12/
- C4.7 -> retrieval: runtime.mode off, and reinjection.sessionStartCompact false, each set alone -> c4/C4.7/
- C4.8 -> install: 301a8e9 old bundle, then upgrade, uninstall, reinstall, with backup -> restore -> fsck certified -> c4/C4.8/, uat/UAT-12/
- C4.9 -> sessions: daemon ended mid-session, unknown schema, unavailable object -> c4/C4.9/
- C4.10 -> all parts: Result blocks in docs/uat.md -> uat/UAT-01..12/
- C4.11 -> recovery: windows/amd64 from this lane; Linux no-model half only if the container is already up and has a claude binary; macOS and windows/arm64 recorded unknown -> c4/C4.11/
- C1.6 -> recovery: inject F4-1 and F4-4 gaps, restart the daemon, check status counters and loud_tail, fsck, and store fingerprints; states honestly whether anything was automatically recovered -> recovery/C1.6/
- C1.7 -> recovery: backup create/verify, pre-new-write restore, post-new-write restore, refusal checks -> recovery/C1.7/
- C5.6 -> recovery aggregates every part's meta.json and hooks.json -> resources/C5.6/summary.md
- Guard outputs -> guard/<part>.txt; session ledger -> sessions.tsv

Nothing under live/ exists until the lane runs.

### Commits

- eab79b0d chore(v6): add the live lane's home guard and session driver
- 7f94d4fd chore(v6): finish the phase 4 live-lane workflow

### Tests

- `TMP=<scratch>/tmp python -W error::ResourceWarning plans/sdd/V6-closeout/coordinator/homeguard_test.py` — PASS, 9 tests on a fake home: byte-identical checks, cache and data leftovers, clean, ~/.qompack info vs loss, credentials never opened, snap refuses overwrite
- `TMP=<scratch>/tmp python -W error::ResourceWarning plans/sdd/V6-closeout/coordinator/live_driver_test.py` — PASS, 1 test against a fake stream-json host: turns, before step, hook latency pairing, unpaired SessionEnd, daemon image check, scrub
- `node <scratch>/check.js (live-uat.js wrapped with a stub agent(), placeholders substituted)` — PASS: parses and runs 4 sequential parts plus the audit, prompts 12-15k chars
- `go run ./tools/devtool lint --only=runpatterns` — PASS runpatterns (exit 0)

### Open issues

- The lane was not executed (no real sessions were allowed for this seat). Every host fact comes from the CLI's own help or the committed C5.4 streams; the lane's first steps will confirm them.
- docs/install.md §3 is stale for 2.1.280 (install/update/uninstall now take --json, and uninstall without --keep-data removes plugins/data/<id>). This seat may not edit docs; the lane records it as a finding for the Phase 6 docs pass.
- The older plans/sdd/V6-closeout/eval/runs/homeguard.py at the integration head compares the two plugin JSON files by size only. It is superseded for the live lane but left in place.

### Needs the owner

- Before launch, merge closeout/w7-livelane into the candidate, or homeguard.py and live_driver.py will be missing from the worktree and every part stops as blocked. Also create worktree ../qompack-cx-live (branch closeout/live) at the frozen candidate.
- Real sessions necessarily write the real ~/.qompack (logs/ and the D10 staged copies bin/<sha256>/), because Qompack resolves home from USERPROFILE, which the host also needs. They also write the host's own ~/.claude/projects transcripts and ~/.claude.json. The lane records the ~/.qompack additions and removes only staged copies it created that match its bundles. The coordinator must accept this or say otherwise.
- C4.1 literally says 'isolated profile'. The lane does the install and discovery half in an isolated CLAUDE_CONFIG_DIR with no model call; model sessions use the real profile with a local-scope install, because an isolated profile has no login and credentials may not be copied. A fully isolated session would need an owner-provided token (for example from `claude setup-token`).
- C4.11 Linux sessions need a Claude login inside the container. Without an owner-provided token they are recorded unknown. The no-model Linux half runs only if Docker is already up, and it needs a frozen linux/amd64 bundle from the coordinator.
- Budgets total 36 Haiku sessions (8/12/12/4), inside D3's 40-80. Raise a part's budget before launch if wanted.

## Independent review

### review:livelane: needs-fixes

- **major** `plans/sdd/V6-closeout/coordinator/live_driver.py main() (stream.jsonl/stderr.txt written raw) + live-uat.js COMMON 'Evidence under ${LIVE}/ ... driver outputs' and PARTS[install] (3)` — Planted secrets are kept out of committed evidence only by prose. The driver writes stream.jsonl and stderr.txt exactly as the host emits them, and it scrubs only the scratch prefix in driver-config.json and meta.json. The prompt tells the agent to put driver outputs under ${LIVE}/ and, separately, to keep raw copies in scratch. In UAT-12, session A reads the planted files, so their contents land in stream.jsonl (tool results, the model's text, any leaked hook additionalContext). If the agent points `out` at ${LIVE}/uat/UAT-12/... and commits the folder, a credential-shaped string enters branch history. The repository is public, push protection blocks such strings, and the lane may not rewrite history (no reset/rebase). The audit's secret search only runs after that commit already exists.
  - Evidence: live_driver.py docstring: 'driver-config.json and meta.json have cfg["scrub"] ... replaced; stream.jsonl and stderr.txt are kept exactly as the host wrote them'. live_driver_test.py asserts the raw prefix is still in stream.jsonl. COMMON: 'Evidence under ${LIVE}/ ...: driver outputs' together with 'Raw copies stay in scratch.'
  - Fix: Make the scrub mechanical. Add a config key `secrets` (a list of literal strings, built at run time and never written to the config file on disk; pass them through an environment variable or a scratch-only file). Apply it to every output the driver writes, stream.jsonl and stderr.txt included, replacing the n-th secret with @@SEC_PLANTED_n@@. Alternatively require `out` in scratch for every session and add a hard step before each `git add`: grep the staged tree for each planted secret and its sha256 fragments, and abort the commit on any hit. Add a self-test case for whichever you choose.
- **minor** `live-uat.js COMMON 'HARD RULES' guard bullet ('STOP: restore only what your run changed')` — When the guard fails, the agent is told to 'restore only what your run changed', but only hand-editing ~/.claude/settings.json is forbidden. A failed check on plugins/installed_plugins.json or known_marketplaces.json could lead the agent to hand-edit the real operator files, which the owner directive forbids. A diff it did not cause, for example a host marketplace auto-update rewriting known_marketplaces.json during one of the lane's own `claude -p` startups (the guard cannot tell the difference), could also lead it to 'restore' bytes it never owned.
  - Evidence: COMMON: 'A failure you cannot trace to your own run's still-open install window means STOP: restore only what your run changed, report guard_ok false. NEVER install at user scope in the real profile, never edit ~/.claude/settings.json'. The owner rule is 'never read or modify the real ... ~/.claude (settings, installed_plugins.json, known_marketplaces.json, credentials)'.
  - Fix: Spell it out: restoring means only `claude plugin uninstall -s local`, `claude plugin marketplace remove`, and deleting cache or data directories the run created. Never write any file under ~/.claude by hand. For a diff the run cannot attribute, stop with the check output and let the coordinator decide.
- **minor** `plans/sdd/V6-closeout/coordinator/homeguard.py fingerprint()/check() (.qompack handling)` — If ~/.qompack does not exist at snap time, _names() records the sentinel '<absent>'. When the first real session creates ~/.qompack, check reports the sentinel as 'lost pre-existing' and fails, which is a spurious guard failure that stops the lane. The self-test has no case for an absent ~/.qompack. It is probably not triggered on this host, where earlier pilots most likely created ~/.qompack, but the tool is wrong on a clean home.
  - Evidence: Reproduced on a fake home in scratch: `homeguard.py snap g.json --home <fake>` with no .qompack, then mkdir .qompack/logs and .qompack/bin/abc, then `check` printed "DIFF  .qompack/ lost pre-existing ['<absent>']" and 'REAL HOME CHANGED', exit 1.
  - Fix: Treat '<absent>' in the before-set as an empty set in the .qompack comparison (gains stay INFO). Add a homeguard_test case: .qompack absent at snap and created later means check passes with an INFO line.
- **minor** `live-uat.js PARTS[install] (3), UAT-12 ordering` — The lane runs UAT-12 steps 2-5 (denied read, oversized and binary expand) in session B, after step 6 (the upgrade). docs/uat.md orders them before the upgrade. The reorder is deliberate and sensible, because the old build 301a8e9 predates the C1.9 deny-rule support and the candidate's privacy is what needs proving. But nothing tells the agent to record it as a deviation, so the Result block could claim the row ran as written.
  - Evidence: uat.md UAT-12 Steps 2-5 come before '6. Upgrade the plugin to a newer bundle'. The lane: 'upgrade per install.md §5 ...; session B: the denied file through Read, recall, expand, re_read ...'.
  - Fix: Add: 'Record in the UAT-12 Result block and findings that steps 2-5 ran after step 6, on the frozen build, and why (the old build predates C1.9).' Optionally also run step 2 once on the old build and record the result as characterization only.
- **minor** `live-uat.js PARTS budgets (8/12/12/4) and the seat's claim '36 in total, inside D3's 40-80'` — The claim is wrong twice. 36 is below D3's ~40-80, not inside it. More importantly, D3's budget is shared: the C5.5 preregistration takes 40 sessions of it ('inside owner decision D3's 40–80 real-session budget, which the UAT phase shares'), and six smoke/pilot sessions have already been used. The lane plus C5.5 comes to about 82. The sessions part is also tight: its own estimates are UAT-02 1-2, UAT-03/04/05 about 5, UAT-06 3 and C4.9 2-3, which is 11-13 against a budget of 12, with failed sessions counted too.
  - Evidence: V6-CLOSEOUT-CHECKLIST.md D3 row. qompack-cx-int plans/sdd/V6-closeout/eval/preregistration.md §7 '10 tasks × 2 trials × 2 arms = 40 sessions, inside owner decision D3's 40–80 real-session budget, which the UAT phase shares'. marketplace-rehearsal.md: six sessions used.
  - Fix: Correct the README and summary wording. Tell the coordinator (who holds D33's delegation) the combined total: lane 36 + C5.5 40 + 6 used = about 82. Either raise sessions to about 14 and accept a small overrun, or keep 12 and let the part skip UAT-06's fork step first when it runs out, recording the skip.
- **minor** `live-uat.js COMMON (no mention of D32)` — D32 records Windows Defender flagging Qompack builds (Trojan:Win32/Bearfoos ML). The owner's exclusion covers the Go build cache/temp and the worktrees. The lane also executes qompack.exe from places that exclusion probably does not cover: the host's plugin cache (~/.claude/plugins/cache/qompack-live/...), the D10 staged copies under ~/.qompack/bin/<sha256>/, and the old bundle built into scratch. A quarantined binary would look like a product defect (a hook failure, a daemon that never comes up, a missing file).
  - Evidence: V6-CLOSEOUT-CHECKLIST.md D32 row. The COMMON prompt never mentions Defender.
  - Fix: Add one bullet: if a qompack.exe vanishes, fails with access denied, or a hook reports that its binary is missing, check `Get-MpThreatDetection` (read-only) and record the result as environmental under D32, not as a product defect. Never change Defender settings.
- **minor** `live-uat.js PARTS[install] (2) UAT-01` — UAT-01's preconditions also forbid a user config.json ('No project or user config.json'), which for Qompack is ~/.qompack/config.json. The lane removes only the QOMPACK_* variables and never checks for that file. If the operator has one, UAT-01 runs against non-default configuration without anyone noticing.
  - Evidence: docs/uat.md UAT-01 Preconditions: 'Configuration: defaults only. No project or user `config.json`, no `QOMPACK_*` variables'.
  - Fix: Have the agent check the name listing already in its guard snap (or `qompack config print --provenance`, step 7) for a user-layer config.json. If one exists, record the precondition as unmet in the Result block instead of reporting a clean pass. Do not read or move the file.
- **minor** `live-uat.js PARTS[recovery] C4.11` — The Linux no-model half needs 'the frozen linux/amd64 bundle if the coordinator built one', but the coordinator substitutes only __CANDIDATE__ and __BUNDLE__, the windows/amd64 directory. The agent has no path to a Linux bundle, so in practice the Linux half never runs.
  - Evidence: live-uat.js header: 'replacing ONLY __CANDIDATE__ ... and __BUNDLE__ (the frozen windows/amd64 bundle directory)'. The C4.11 text: 'with the frozen linux/amd64 bundle if the coordinator built one'.
  - Fix: Derive the Linux bundle as a sibling of BUNDLE (the same devtool bundle --out directory, with the -linux-amd64 suffix and a BUNDLE.json whose target is linux/amd64, verified by its sha256), or name the Phase 3 `bundles` step's evidence location. If neither exists, record the Linux half as unknown with that reason.
- **minor** `plans/sdd/V6-closeout/coordinator/live_driver.py docstring and pair_hooks(); live-uat.js HOST FACTS 'latency comes from live_driver.py's arrival times (hooks.json; an upper bound)'` — Calling the arrival delta 'an upper bound' is unverified and can be wrong. If the host buffers or batches stdout, hook_started and hook_response arrive together and the delta collapses toward 0, which understates latency. The committed smoke3 stream shows the SessionStart hook_started/hook_response pair emitted before the init event, which suggests startup hook events are flushed after the hook has already run. C5.6 would then report host-seen SessionStart latency near 0 ms.
  - Evidence: qompack-cx-int plans/sdd/V6-closeout/eval/runs/smoke3-plugin-dir-autocompact/stream.jsonl lines 1-2 (hook_started/hook_response for SessionStart:startup) come before the system/init line. live_driver.py: 'arrival deltas are the hook latency as the host saw it (an upper bound: it includes stream buffering)'.
  - Fix: Call it 'the stream arrival delta (approximate; can under-read when the host batches output)'. In the C5.6 summary, flag hooks whose pair arrived within a few ms, or before init, as not measured instead of reporting 0 ms.
- **nit** `plans/sdd/V6-closeout/coordinator/live_driver.py sample_daemon()` — Python's open() on Windows takes a handle without FILE_SHARE_DELETE. internal/daemon/lock.go documents exactly this hazard: a reader holding daemon.lock makes the daemon's Lock.Release os.Remove fail with ERROR_SHARING_VIOLATION. The driver holds the lock only for a moment after each turn, so a collision is unlikely.
  - Evidence: qompack-cx-int internal/daemon/lock.go, the LockPath comment: 'os.ReadFile, whose handle carries FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE ... made Lock.Release's os.Remove fail'.
  - Fix: Keep the read minimal (it already is), or read the lock through PowerShell with [IO.FileShare]'ReadWrite,Delete'. At least note the hazard in the docstring.
- **nit** `live-uat.js COMMON 'Model claude-haiku-4-5-20251001; use claude-sonnet-5 only when ...'` — 'claude-sonnet-5' is not checked against anything the host lists. `claude --help` only shows alias and full-name examples. The preregistration uses the same string, so it is probably fine.
  - Evidence: `claude --help`: '--model <model> Model for the current session. Provide an alias for the latest model (e.g. ...) or a model's full name'.
  - Fix: Use the alias 'sonnet', or confirm the id from a committed live-eval stream's init event.

## Fix seat (review resolution) — status `done`, head `e0bd39d95a9dae176503f49b9dbb2dce90cc4a4e`

### Summary

I checked all nine reviewer findings and all nine were correct. They are fixed in five commits on closeout/w7-livelane, in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-livelane/plans/sdd/V6-closeout/coordinator/: live-uat.js, live_driver.py, live_driver_test.py, homeguard.py, homeguard_test.py and README.md. Both self-tests pass: homeguard 10/10 and live_driver 3/3. I also ran the new test cases against the previous versions of the tools, and they failed there, so the tests catch the bugs they were written for. live-uat.js passes `node --check` with the placeholders filled in. A dry run with a stub agent() builds all five prompts with no template gaps and no __BUDGET__ left. No real sessions were run. Hook-latency figures remain approximate (see finding 9).

## Review resolution
1. MAJOR, planted secrets reaching committed evidence: correct, fixed (194b30f0, 5bdfb30e). The scrub is now done by the driver, not by instructions to the agent:
   - live_driver.py takes a new config key `secrets_file`, a JSON list of literal strings that the lane keeps in scratch. The driver refuses the file if it sits inside a git work tree, using `git rev-parse --is-inside-work-tree`.
   - Every output the driver writes has the n-th secret replaced with @@SEC_PLANTED_n@@. That covers stream.jsonl and stderr.txt (line by line), plus hooks.json, meta.json and driver-config.json. The replacement catches the literal secret and its JSON-escaped and double-escaped forms.
   - stderr is now read through a pipe so it can be scrubbed.
   - Other outputs (CLI responses, greps) need a commit gate. The new `live_driver.py scan-staged <repo> <secrets.json>` runs `git grep --cached -l -F` over every form of every secret. It prints only file names and exits 1 on any hit.
   - live-uat.js now makes that scan a hard step before every commit: unstage the named files with `git restore --staged`, scrub them, scan again, and commit only on exit 0. It also requires `secrets_file` on every session of a scenario that plants secrets.
   - Self-tests: secrets are built from split parts. One contains a quote and a backslash, to exercise the escaped forms. The tests assert that no form of either secret appears in any output file and that both placeholders do. A scan-staged case uses a temp repo with one leaking file and one clean file, and a refusal case puts the secrets file inside a work tree.
2. Minor, guard restore wording: correct, fixed (98b70cc8). Restoring now means only `claude plugin uninstall qompack@qompack-live -s local`, `claude plugin marketplace remove qompack-live`, and deleting cache or data directories the run created. The prompt says never to write, edit, copy over or delete any file under ~/.claude by hand. For a diff the run cannot attribute to itself, such as a marketplace auto-update, the agent stops, commits the check output, reports guard_ok false and leaves the decision to the coordinator.
3. Minor, homeguard with no ~/.qompack: correct, fixed (762421e3). I reproduced it: the new test fails on the old homeguard.py. check() now treats the `<absent>` sentinel as an empty set, so creating ~/.qompack later shows up as INFO. New test: test_qompack_absent_at_snap_then_created_is_info.
4. Minor, UAT-12 run order: correct, fixed (e0bd39d9). The lane must record in the UAT-12 Result block and findings that steps 2-5 ran after step 6 on the frozen build, because 301a8e9 predates C1.9. Running step 2 once on the old build is allowed as characterization only, within budget. I checked the order against docs/uat.md, which has UAT-12 at line 1071.
5. Minor, budget claim: correct. The earlier seat summary said 36 was inside D3's 40-80; it is below it, and D3's budget is shared. Checked against V6-CLOSEOUT-CHECKLIST.md line 22 (D3), preregistration.md line 116 (40 sessions, and it says the UAT phase shares D3's budget), and marketplace-rehearsal.md (6 sessions used). The README (5bdfb30e) and the live-uat.js header comment (e0bd39d9) now give the combined total: lane 36 + C5.5 40 + 6 used = about 82. I kept the sessions part at 12 and added a skip order for when it runs short: UAT-06's --fork-session step first, then UAT-06's correction session, then C4.9 (c). C4.2 and C4.3 are never skipped, and each skip is recorded.
6. Minor, Windows Defender (D32): correct, fixed (98b70cc8). Checked against checklist line 51. New bullet: if a qompack.exe vanishes, fails with access denied, or a hook reports its binary missing, run `Get-MpThreatDetection` (read-only). If it names the binary, record the step as environmental under D32, not as a product defect. Never change Defender settings or restore quarantined files.
7. Minor, UAT-01 user config.json: correct, fixed (e0bd39d9). Checked against docs/uat.md lines 115-116; docs/config-reference.md line 8 confirms the user layer is ~/.qompack/config.json. The agent checks only that the file exists (with `test -e`, or the user layer in step 7's `config print --provenance`), never reads or moves it, and records the precondition as unmet if it is there.
8. Minor, Linux bundle for C4.11: correct, fixed (e0bd39d9). tools/devtool/bundle.go bundleDirName names each target's directory `qompack-plugin-<version>-<os>-<arch>`, and phase3.sh's bundles step builds all six targets. The Linux bundle is now the -linux-amd64 sibling of BUNDLE. It is accepted only if its BUNDLE.json says target linux/amd64, source.commit matches the candidate, and the bin/qompack sha256 matches the files entry. Otherwise the Linux half is recorded as unknown with the reason.
9. Minor, hook latency called an upper bound: correct, fixed (194b30f0, 5bdfb30e, e0bd39d9). Confirmed in qompack-cx-int smoke3 stream.jsonl: lines 1-4 are the SessionStart and UserPromptSubmit hook_started/hook_response pairs, and line 5 is init.
   - hooks.json now carries `before_init` and `measured` fields. A pair is marked measured false if it arrived before init, within 5 ms of its start, or with no response.
   - The wording is now "stream arrival delta, approximate; can under-read when the host batches output".
   - The C5.6 summary reports median, p95 and max over measured pairs only, with the count of unmeasured pairs beside them. The self-test checks that a SessionStart pair emitted before init comes out measured false.

## Coverage map
Unchanged from the first seat, except that evidence now passes the secret gate:
- C4.1 → install (1) → live/c4/C4.1/
- UAT-01 → install (2) → live/uat/UAT-01/
- C4.8, C4.6, UAT-12 → install (3) → live/c4/C4.8/, live/c4/C4.6/, live/uat/UAT-12/
- C4.2 (in UAT-02) and C4.3 (UAT-03/04/05) → sessions → live/uat/UAT-0N/
- UAT-06 → sessions → live/uat/UAT-06/
- C4.9 → sessions → live/c4/C4.9/
- C4.4, C4.5, C4.7 and UAT-07..11 → retrieval → live/c4/C4.4/tool-matrix.md, live/c4/C4.5/commands-vs-docs.md, live/c4/C4.7/, live/uat/UAT-NN/
- C1.6 → recovery → live/recovery/C1.6/
- C1.7 → recovery → live/recovery/C1.7/
- C5.6 → recovery → live/resources/C5.6/summary.md
- C4.11 → recovery → live/c4/C4.11/
- C4.10 (Result blocks written into docs/uat.md) → every part, checked by the audit
- Guard evidence → live/guard/<part>.txt; session log → live/sessions.tsv

## Host facts
I re-checked nothing against the CLI in this round; all findings were about the lane itself. The Claude Code 2.1.280 help lines verified by the first seat still stand. The only new facts I relied on are the repo-file checks listed under each finding above.

### Commits

- 762421e3 fix(v6): treat a missing ~/.qompack as empty in the home guard
- 194b30f0 fix(v6): scrub planted secrets from every live driver output
- 5bdfb30e chore(v6): gate live-lane commits on planted-secret scans
- 98b70cc8 chore(v6): bound live-lane guard recovery and note d32
- e0bd39d9 chore(v6): record uat deviations and the linux bundle in live lane

### Tests

- `cd plans/sdd/V6-closeout/coordinator && python homeguard_test.py` — Ran 10 tests, OK (the new absent-.qompack case fails against the pre-fix homeguard.py)
- `cd plans/sdd/V6-closeout/coordinator && python live_driver_test.py` — Ran 3 tests, OK (the new secrets and scan-staged cases fail against the pre-fix live_driver.py)
- `node --check on live-uat.js with placeholders filled and wrapped in an async function; stub agent() dry run building all five prompts` — syntax ok; 4 part prompts and the audit prompt built, no leftover ${...} or __BUDGET__
- `git log 7f94d4fd..HEAD --format=%B | grep -ci 'co-authored|claude-session|generated with'` — 0 (the commit-msg hook accepted all five commits; the two fix commits carry the Refs: footer the hook requires)

### Open issues

- The combined real-session total is about 82 against D3's ~40-80: lane 36 + C5.5 40 + 6 smoke/pilot sessions already used. The sessions part stays at 12, with a recorded skip order for when it runs short.
- Host-seen hook latency can only be approximated from stream arrival times. Startup hooks (SessionStart:startup, the first UserPromptSubmit) arrive before init and will be reported as not measured, so C5.6 will have no host-seen number for them.
- C4.11's Linux half runs only if the Phase 3 bundles output leaves a -linux-amd64 sibling next to the frozen windows bundle, the container is already up, and a Linux claude is present in it; otherwise it is recorded as unknown.

### Needs the owner

- Coordinator (under D33): accept the combined ~82-session total against D3's ~40-80, or raise the sessions part to 14 and accept a slightly larger overrun instead of relying on the skip order.
- Coordinator: when freezing the candidate, keep the linux/amd64 bundle next to __BUNDLE__, with the same directory name but a -linux-amd64 suffix, if C4.11's Linux half should run.

